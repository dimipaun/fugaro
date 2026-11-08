package cli

// This file is the workspace scope of `fugaro budget kill` and `budget resume`
// (D1302, spec.md §6): the third scope the stop grew, beside --all (the
// project) and --repo (one repository).
//
// It stops a workspace, and the switch it writes is the one the workspace's own
// service reads as its stop flag: `--all` on the workspace's own database,
// which is `budget.PathKillGlobal` there, exactly as the project-wide switch is
// the stop flag of the credentials service a member runs
// (internal/credsvc/server.go:454, the read C1.11 gave the service). So the
// stop reaches every member without any member being written to — a member's
// runs that reach the workspace's shared credentials halt on the service's
// refusal — and that is the whole of its reach, which is why the scope is
// stated here rather than implied by the name: it touches nothing of a member's
// own (spec.md §9).
//
// The workspace is named, and the flags that name the project this command
// would otherwise act on are refused rather than ignored: a `--project` beside
// `--workspace` would be read as one scope and acted on as another.

import (
	"bufio"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// workspaceKillNoProjectFlags are the cloud flags the workspace scope refuses:
// the three that select the project the command acts on. --region is not among
// them (it names no project), and --reason, --yes and the scopes are the
// command's own.
var workspaceKillNoProjectFlags = []string{"config", "project", "gcp-project"}

func runBudgetKillWorkspace(cmd *cobra.Command, o *budgetKillOptions, kill bool) error {
	ctx := cmd.Context()
	for _, name := range workspaceKillNoProjectFlags {
		if cmd.Flags().Changed(name) {
			return userErr("--workspace %s acts on the workspace's own project and its own database, so --%s cannot be combined with it: this command would read one project and write another; leave --%s out, or run the scope it names with --project", o.workspace, name, name)
		}
	}
	// The workspace's own config is where the workspace's database is named,
	// and this machine has to hold it: a member's machine holds no config for
	// the workspace's project at all (it holds no grant on it), so the refusal
	// workspaceOwnerConfig gives names the project the workspace is named after.
	lc, _, err := workspaceOwnerConfig(o.workspace)
	if err != nil {
		return err
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return err
	}
	declined := "nothing was resumed"
	if kill {
		declined = "nothing was killed"
	}
	// The database is the workspace's own, and the ownership gate is the one
	// every writer into a budget database asks: it is also W2's publish stage's
	// own first question, so the database a record is published in and the
	// database a stop is written in are known to be the same one.
	if err := requireOwnBudgetDB(ctx, lc, db, declined); err != nil {
		return err
	}
	printProjectHeader(o.cloud.errw(), lc)
	me, err := lc.Me(ctx)
	if err != nil {
		return userErr("%v", err)
	}
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())
	what := "workspace " + lc.Name
	// The switch is the workspace's own global one and never a repository's: a
	// workspace's service answers its members, and the stop has to reach all of
	// them at once.
	res, err := budget.SetKill(ctx, db, budget.PathKillGlobal, kill, me, o.reason, budget.KillHooks{
		Confirm: func(budget.Kill) error {
			if kill {
				return confirmTyped(cmd, in, lc, o.yes, "this stops the runs of every member of "+what+": its own service refuses every shared-credential call, and nothing of a member's own — its credentials, its caps, its sign-in, a run that never reaches the workspace — is touched")
			}
			return confirmTyped(cmd, in, lc, o.yes, "this lets "+what+"'s members reach its shared credentials and run again")
		},
		Conflict: func() { fmt.Fprintln(out, "changed by someone else meanwhile; reading it again") },
		Now:      budgetNow,
	})
	if err := budgetKillWriteErr(lc, err); err != nil {
		return err
	}
	if res.Already {
		if kill {
			fmt.Fprintf(out, "%s is already killed (by %s at %s: %s); nothing changed\n", what, oneLine(res.Previous.By), ms(res.Previous.At), oneLine(res.Previous.Reason))
		} else {
			fmt.Fprintf(out, "%s is not killed; nothing changed\n", what)
		}
		return nil
	}
	if kill {
		fmt.Fprintf(out, "killed %s (by %s). Its members' runs halt within seconds; undo with fugaro budget resume --workspace %s\n", what, oneLine(me), lc.Name)
	} else {
		fmt.Fprintf(out, "resumed %s (by %s)\n", what, oneLine(me))
	}
	return nil
}

// budgetKillWriteErr words what a failed SetKill means, for both the project
// scopes and the workspace one: a write the database denied is the project's
// admin question (dbErr) and a lost race is worth another run. It is one
// function so the two scopes cannot answer the same failure differently.
// A nil err is nil.
func budgetKillWriteErr(lc *localcfg.Config, err error) error {
	var ke *budget.KillError
	switch {
	case errors.As(err, &ke):
		return dbErr(lc, err, ke.Write)
	case errors.Is(err, budget.ErrKillContention):
		return remote(fmt.Errorf("the kill switch kept changing under the write (%d attempts); run it again", setAttempts))
	}
	return err
}
