package cli

// This file is the workspace scope of `fugaro budget kill` and `budget resume`
// (D1302, spec.md §6): the third scope the stop grew, beside --all (the
// project) and --repo (one repository).
//
// It stops a workspace, and the switch it writes is the one the workspace's own
// service reads as its stop flag: `--all` on the workspace's own database,
// which is `budget.PathKillGlobal` there, exactly as the project-wide switch is
// the stop flag of the credentials service a member runs
// (internal/credsvc/server.go's RTDBKills.Stop, the read C1.11 gave the
// service). So the stop reaches every member without any member being written
// to — a member's runs that reach the workspace's shared credentials halt on
// the service's refusal — and that is the whole of its reach, which is why the
// scope is stated here rather than implied by the name: it touches nothing of a
// member's own (spec.md §9).
//
// The workspace is named, and the flags that name the project this command
// would otherwise act on are refused rather than ignored: a `--project` beside
// `--workspace` would be read as one scope and acted on as another.
//
// The name is a workspace's, and the config it resolves has to declare one
// (workspace_members): the switch written here is the project's own global one,
// the node the --all scope writes, so a plain project's name would be that
// scope's write — the whole of a project's stop — made through a flag that asks
// nothing and takes no typed word.

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// workspaceKillNoProjectFlags are the cloud flags the workspace scope refuses:
// the three that select the project the command acts on. --region is not among
// them (it names no project), and --reason, --yes and the scopes are the
// command's own.
var workspaceKillNoProjectFlags = []string{"config", "project", "gcp-project"}

func runBudgetKillWorkspace(cmd *cobra.Command, o *budgetKillOptions, kill bool) error {
	ctx := cmd.Context()
	// Before the database is opened, as openBudgetDB does: GODEBUG=http2debug
	// makes Go print HTTP/2 requests with their headers — the OAuth bearer
	// token of the person's own ADC, which is what this write authenticates
	// with — to stderr, and GODEBUG is read before main runs, so it cannot be
	// unset in time. This scope resolves its own project rather than selecting
	// one, so it never passes through openBudgetDB and has to refuse here
	// (refuseHTTP2Debug, the rule every cloud command of this repository
	// follows).
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
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
	// A workspace is a project that declares one, and this scope acts on the
	// workspace and not on whatever project its name resolves to. The switch
	// it writes is budget.PathKillGlobal, which is the same node the --all
	// scope writes, so a name that is any project this machine has configured
	// would be a second, quieter way to the whole of a project's stop: `budget
	// resume --workspace <a plain project>` would lift it while only the typed
	// project name (or --yes) lifts it through --all, and `budget kill
	// --workspace <a plain project>` would halt it without the typed name --all
	// asks for. The workspace_members key's presence is the config's own claim
	// to be a workspace's (localcfg.Config.WorkspaceMembers: an absent key is a
	// config that declares no workspace at all), and the question is asked
	// here, before the database is opened, so a project the scope has no
	// business in is never read or written.
	if lc.WorkspaceMembers == nil {
		verb := "kill"
		if !kill {
			verb = "resume"
		}
		return userErr("--workspace %s names a workspace, and the config of the project it names declares no workspace (its workspace_members key is absent): that project's own stop is what the --all scope writes, with the confirmation it asks for, so this is the wrong scope for it; run 'fugaro budget %s --all --project %s' instead, or name a workspace's own project", o.workspace, verb, o.workspace)
	}
	_, db, err := budgetDBFor(ctx, lc)
	if err != nil {
		return err
	}
	// This run's account and its log (D1352), opened where the project scopes
	// open theirs and under the same name (budgetKillLogName): a stop written
	// through --workspace is the same command's write as one written through
	// --all, so the log a person reads after it — and the path a failure sends
	// them to — has to hold it. Everything the refusals above answer, they
	// answer before this point, as a refusal that reaches no API keeps no log.
	rep, sink := reporterFor(cmd, budgetKillLogName(kill), nil)
	if sink != nil {
		defer func() { _ = sink.Close() }()
	}
	defer rep.Summary()
	out := sayThrough(rep, cmd.OutOrStdout())
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
	// And then the name check `fugaro budget set` and `fugaro budget kill`
	// make, as this scope's siblings do (openBudget holds it for the project
	// scopes): requireOwnBudgetDB asks whether this installation may write to
	// the database at all, and this asks whether the database's own
	// /fugaro/project is the workspace's name. A workspace whose
	// budget.firebase_project is its own gcp_project passes the first question
	// on an empty, unmarked database, so without this the stop would be written
	// into a database nothing proves is the workspace's and the operator would
	// read a success that stopped nothing.
	if err := checkDBProject(ctx, db, lc); err != nil {
		return err
	}
	printProjectHeader(o.cloud.errw(), lc)
	me, err := lc.Me(ctx)
	if err != nil {
		return userErr("%v", err)
	}
	what := "workspace " + lc.Name
	// The switch is the workspace's own global one and never a repository's: a
	// workspace's service answers its members, and the stop has to reach all of
	// them at once.
	res, err := budget.SetKill(ctx, db, budget.PathKillGlobal, kill, me, o.reason, budget.KillHooks{
		Confirm: func(budget.Kill) error {
			if kill {
				// A kill halts runs and asks nothing: it reads no stream and
				// waits for nobody, and the direction that would need holding
				// back is the one that lets them run again (D1542 as
				// docs/no-prompt-policy/implementation-plan.md T3 states it,
				// "Remove confirmTyped/confirmTypedStrict" and "List and fence
				// every raise form").
				return nil
			}
			// A resume is that raise, and this scope is fenced on exactly
			// what every other raise form is fenced on: the agent marker,
			// the one classification the package's agent refusals ask
			// (raiseOwnerOnly, budget.go, which `budget set`'s raises and
			// `budget resume --all|--repo` go through). The helper itself
			// cannot serve this scope, because the invocation it hands the
			// owner always carries --project (ownerRaiseCommand) and this
			// scope refuses that flag, so the command below is built here
			// and is the one that lifts this stop.
			//
			// The refusal carries the exact command its owner runs
			// themselves, because a session that reads only the refusal has
			// to be able to act on it.
			if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
				return userErr("resuming %s is the owner's own step: %s; nothing was changed. Run this yourself at your own terminal: fugaro budget resume --workspace %s", what, initflow.AgentRefusal(m), lc.Name)
			}
			return nil
		},
		Conflict: func() { fmt.Fprintln(out, "changed by someone else meanwhile; reading it again") },
		Now:      budgetNow,
	})
	if err := budgetKillWriteErr(lc, err); err != nil {
		return err
	}
	// One step for the switch this run moved, in one state or the other, as
	// the project scopes report theirs (G3's three outcomes): the step's own
	// name is the scope, so a workspace's stop reads the way a project's
	// does, and "already killed" is a skip rather than a tick.
	s := rep.Step(what)
	if res.Already {
		if kill {
			s.Skipped(fmt.Sprintf("is already killed (by %s at %s: %s); nothing changed", oneLine(res.Previous.By), ms(res.Previous.At), oneLine(res.Previous.Reason)))
		} else {
			s.Skipped("is not killed; nothing changed")
		}
		return nil
	}
	if kill {
		s.Done(fmt.Sprintf("killed (by %s). Its members' runs halt within seconds; undo with fugaro budget resume --workspace %s", oneLine(me), lc.Name))
	} else {
		s.Done(fmt.Sprintf("resumed (by %s)", oneLine(me)))
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
