// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pool"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// coordinatorViewRepos is how many repositories' views this read holds open at
// once. A repository's read is not only the database's: one with nothing
// stored falls back to its runs, which reads the runs bucket at lsWorkers —
// the listing's own width, 32 — so this width is multiplied by that one, and
// the product is what the bucket is asked for at any moment. Four keeps it
// within the same order as the single repository a project without the
// coordinator pays for anyway, and still overlaps the handful of repositories
// a project has.
const coordinatorViewRepos = 4

// runCoordinatorStatusFromStore is --from-store's whole path: the read
// (StoredProject), the provider this command's own document carries, and the
// output.
func runCoordinatorStatusFromStore(cmd *cobra.Command, o *coordinatorOptions) error {
	ctx := cmd.Context()
	window, err := fcli.ParseSince(o.since)
	if err != nil {
		return fcli.UserErr("%v", err)
	}
	env, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	repos, err := coordinatorRepos(env, o)
	if err != nil {
		return err
	}
	project, err := StoredProject(ctx, env, repos, coordinatorSince(window), cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	// The provider is this command's step and not the read's: it is the field
	// the document below writes (D814), and resolving it can read a checkout
	// (env.RepoProvider falls back to the one the repository's fugary.yaml
	// declares), which is a thing this command may do and a reader of the same
	// node may not.
	coordinatorStatusProviders(ctx, env, repos, project.Repos)
	out := cmd.OutOrStdout()
	if o.all {
		if o.asJSON {
			return writeCoordinatorJSON(out, project)
		}
		return writeCoordinatorStoredProject(out, project.Repos)
	}
	if o.asJSON {
		return writeCoordinatorJSON(out, project.Repos[0])
	}
	return writeCoordinatorStored(out, project.Repos[0])
}

// StoredProject is the read `fugaro coordinator status --from-store` makes
// (D364): every named repository's stored view, in the order the names were
// given. It is the view the sweep already stored, which is what makes the
// project view the same for GitHub and Bitbucket and gives the console and a
// person's own CLI a read that holds no key.
//
// It is exported because a second command reads the same node for the same
// reason — `fugaro graph`'s coordinator scope, whose document is drawn from
// exactly this data (CG3) — and a node read by two commands is a node with two
// readers only if they may differ. What they must not differ on is the runs
// half: a repository with nothing stored is the runs alone and "stored": false,
// never a failure, and the graph's `down` state is that same fallback seen from
// one step further away.
//
// Three properties are the read rather than niceties around it:
//
//   - It is the person's own database read. The client is newBudgetClient's —
//     the caller's ADC, on the project's budget.rtdb_url, which is the
//     database the coordinator's subtree lives in — and what admits it is
//     roles/firebasedatabase.viewer, the IAM role launchers and operators
//     hold. It is an IAM read and not a rules read: the rules' reader on
//     /coordinator is a device user, so a teammate's ADC is admitted by the
//     project's IAM and by nothing else.
//   - It reads NO credential of the host's. No App ID, no App key, no
//     Bitbucket token, and no provider is opened: with the installation's
//     credentials service in the environment it still succeeds, where the
//     project-wide read (coordinatorReader) refuses. Everything a stored view
//     says about a pull request is what the sweep read once.
//   - A repository with no stored node is the runs half with "stored": false,
//     never a failure: the coordinator is off on most installations, and a
//     console that had to know which before it picked an argv would be the
//     one place that has to. What it consults to decide is the database, and
//     never the local config: the config a teammate's machine holds is the
//     published shared one, and localcfg.Config.Shared drops Terraform, which
//     is where the coordinator's switch lives — so a read gated on it would
//     report the coordinator off on every machine but the owner's.
//
// It stamps no provider of its own: RepoView.Provider is a field of the
// document `coordinator status` writes and not part of this read, so that
// command stamps it (coordinatorStatusProviders) and a caller with no use for a
// host — the graph names none — never reaches for one. What the read does
// resolve is the slug, because the node it reads is keyed by it: repoSlug,
// which takes the provider from the repository's repos entry in the local
// config and falls back to a checkout's fugaro.yaml only when the working
// directory is that repository's checkout. A repository with neither is refused
// there, naming the entry to set, and that is the one case in which this read
// is not checkout-free.
func StoredProject(ctx context.Context, env *fcli.Cloud, repos []string, since time.Time, warn io.Writer) (coordinator.StoredProjectView, error) {
	store, err := openCoordinatorStore(ctx, env.Config())
	if err != nil {
		return coordinator.StoredProjectView{}, err
	}
	// Every repository's view is read in one pass, a few at a time: a project
	// with twenty of them costs one repository's read per round rather than
	// their sum, which is the whole of this read's cost. Each read writes its
	// own slot, so the result is in the order the repository names were given
	// whatever order they finish in, and the first failure in that order is the
	// one reported.
	//
	// What a read warns about is buffered per repository rather than written as
	// it is made: written as it is made, twenty reads at once put one
	// repository's line between two of another's in whatever order they
	// happened to finish, so the warning a person needs is the one they cannot
	// find. The buffers are flushed below, in that same order.
	warns := make([]bytes.Buffer, len(repos))
	views := make([]coordinator.RepoView, len(repos))
	errs := make([]error, len(repos))
	pool.Run(len(repos), coordinatorViewRepos, func(i int) {
		views[i], errs[i] = coordinatorStoredView(ctx, env, store, repos[i], since, &warns[i])
	})
	// The flush stops at the first failure, and stops there for good: the
	// repository that failed is one the read did not finish, so neither its own
	// warnings nor any later repository's are about anything a person can act
	// on, and the error it returns is what the caller says instead.
	for i, err := range errs {
		if err != nil {
			return coordinator.StoredProjectView{}, err
		}
		// stderr is best effort everywhere else in this path, and a terminal
		// that would not take a warning is not a failed read.
		_, _ = warn.Write(warns[i].Bytes())
	}
	return coordinator.StoredProjectView{Repos: views}, nil
}

// coordinatorStatusProviders stamps each repository's provider onto the view
// read for it, in the same order: the field is the caller's answer and not the
// read's (the node is keyed by a slug of the provider, so the node cannot give
// it back), and it is what the command's document and the console's Coordinator
// pane build a pull request's URL from (D814).
func coordinatorStatusProviders(ctx context.Context, env *fcli.Cloud, repos []string, views []coordinator.RepoView) {
	for i, repo := range repos {
		views[i].Provider = env.RepoProvider(repo, fcli.CheckoutOf(ctx, repo)())
	}
}

// coordinatorStoredView reads one repository's stored view, and falls back to
// the runs half when the database holds none.
//
// The fallback is the runs bucket alone — the join of the runs with no pull
// requests, so the pull requests those runs name are listed with the host's
// state unknown rather than fetched. It is the half of the view that needs no
// credential at all, which is the point: a project whose coordinator has never
// run still gets a view out of this command, and the document says which of
// the two halves it is holding.
func coordinatorStoredView(ctx context.Context, env *fcli.Cloud, store *coordinator.Store, repo string, since time.Time, warn io.Writer) (coordinator.RepoView, error) {
	slug, err := env.RepoSlug(ctx, repo)
	if err != nil {
		return coordinator.RepoView{}, err
	}
	view, err := store.ReadRepoView(ctx, slug, repo)
	if err != nil {
		return coordinator.RepoView{}, coordinatorStoredErr(env.Config(), err)
	}
	if view.Stored {
		return view, nil
	}
	runs, err := coordinatorRuns(ctx, env, slug, since, warn)
	if err != nil {
		return coordinator.RepoView{}, err
	}
	view.View = coordinator.Join(repo, nil, runs)
	return view, nil
}

// coordinatorStoredErr classifies a stored-view read that failed. A database
// that refused it is the caller's, and it names the one role that admits the
// read: /coordinator is readable by a signed-in reader through the rules, and
// what admits an ADC against the project's IAM is
// roles/firebasedatabase.viewer, which operators and launchers hold. Anything
// else — an unreachable database, a bad address — is the host's.
func coordinatorStoredErr(lc *localcfg.Config, err error) error {
	if errors.Is(err, rtdb.ErrPermission) {
		return fcli.UserErr("project %s's database refused the read: /coordinator is readable by a signed-in reader and by the database role its caller holds (roles/firebasedatabase.viewer, which terraform.launchers and terraform.operators have); ask an owner to add you (%v)", lc.Name, err)
	}
	return fcli.RemoteErr(err)
}

// writeCoordinatorStored prints one repository's stored view: what the
// database held, the sweep's marker when there is one, and the view itself in
// the table status draws.
//
// The marker is printed as stored — its own Since, and its own reason, which
// goes through cellOneLine because the node is another writer's text — and no
// age is derived from it: the view beside it carries no time by design
// (sweep.go), so "stale for three hours" would be a claim about when the
// sweep last ran that this read cannot make.
func writeCoordinatorStored(w io.Writer, v coordinator.RepoView) error {
	fmt.Fprintln(w, coordinatorStoredLine(v))
	if v.Stale != nil {
		fmt.Fprintf(w, "stale since %s: %s\n", v.Stale.Since.Format(time.RFC3339), safetext.TableCell(v.Stale.Reason))
	}
	return writeCoordinatorStatus(w, v.View)
}

// writeCoordinatorStoredProject prints every repository's stored view, one
// after another.
func writeCoordinatorStoredProject(w io.Writer, views []coordinator.RepoView) error {
	for i, v := range views {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := writeCoordinatorStored(w, v); err != nil {
			return err
		}
	}
	return nil
}

// coordinatorStoredLine is the one line that says what the reader is looking
// at: the stored view, or the runs half and why it is that.
func coordinatorStoredLine(v coordinator.RepoView) string {
	if v.Stored {
		return fmt.Sprintf("%s: stored view", safetext.TableCell(v.Repo))
	}
	return fmt.Sprintf("%s: no stored view (the coordinator has not reconciled this repository); the runs alone", safetext.TableCell(v.Repo))
}
