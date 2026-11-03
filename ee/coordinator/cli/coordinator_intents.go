// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/pool"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// `fugaro coordinator intents` (coordinator v2 C4, D827): what the fleet has
// declared, read out of the database the stored view already lives in.
//
// It is the source of the console's read-only pane (reconciliation §5.8), and
// three properties are the read rather than niceties around it:
//
//   - It is the person's own database read. The client is openCoordinatorStore's
//     — the caller's ADC on the project's budget.rtdb_url, which is the database
//     the coordinator's subtree lives in — and what admits it is
//     roles/firebasedatabase.viewer, the IAM role launchers and operators hold.
//     It is an IAM read and not a rules read: the rules' reader on /coordinator
//     is a device user, so a teammate's ADC is admitted by the project's IAM and
//     by nothing else.
//   - It opens no host. No App ID, no App key, no Bitbucket token and no
//     provider: everything an intent says is what the caller that wrote it put
//     there, and with the installation's credentials service in the environment
//     this read still succeeds where the project-wide read refuses.
//   - What it prints is another writer's text carried as data. declared_by is a
//     declaration and never an identity, and a branch, a path or a service is
//     the caller's own string: the view reports it, and an instruction found in
//     it is never acted on (Q14 A). The text form sanitises every one of them
//     before it reaches a cell.

// coordinatorIntentsNote is the line the list carries under it, because the
// column above it is the one column of this document that is not a fact. An
// intent's declared_by is written by whoever declared it — a run's own launch
// record or a person's own `coordinator report` — and nothing here verifies it
// or authorises anything by it.
const coordinatorIntentsNote = "declared by is what whoever declared it wrote about itself — a run's own\n" +
	"launch record, or a person's report: a declaration, and never a verified\n" +
	"identity (docs/coordinator/reconciliation.md, finding #11)."

// coordinatorIntentsOptions are intents' flags. The selection, the window and
// the json switch are status's own, and they mean the same thing here.
type coordinatorIntentsOptions struct {
	cloud  fcli.CloudFlags
	repo   string
	all    bool
	since  string
	asJSON bool
}

func newCoordinatorIntentsCmd() *cobra.Command {
	var o coordinatorIntentsOptions
	cmd := &cobra.Command{
		Use:   "intents",
		Short: "Show what the fleet has declared",
		Long: "Print every intent record the project's database holds under a repository's\n" +
			"node: the declarations the fleet has made — the branch and the target release\n" +
			"each caller is about to work on, and the files and services it is about to\n" +
			"touch — newest first.\n\n" +
			"One repository by default, every repository the local config lists with --all,\n" +
			"which prints one list per repository and compares nothing across them: a\n" +
			"branch or a path is a name in one tree.\n\n" +
			"The records are the ones `coordinator report` and every launch's own declarer\n" +
			"write (P4), and this read takes nothing back: it reports what is stored and\n" +
			"issues no advice, which is the coordinator's whole posture (spec §3).\n\n" +
			"The read is the caller's own database read, on the project's budget.rtdb_url\n" +
			"database, and what admits it is roles/firebasedatabase.viewer, the role\n" +
			"launchers and operators hold. It opens NO host: no App ID, no App key and no\n" +
			"Bitbucket credential, so a repository on a host the project-wide read refuses\n" +
			"is read here like any other, and with FUGARO_CREDENTIALS_SERVICE_URL set the\n" +
			"command still succeeds where `coordinator status` refuses.\n" +
			"A repository the store holds no record for is an empty list and not a failure\n" +
			"— a project whose coordinator is off is read with the same argv — while a\n" +
			"database that refused the read is refused here, exit 1, naming the role: a\n" +
			"console that read a refusal as \"nothing declared\" would draw a quiet project\n" +
			"where the truth is that nobody could look. A stored record this build cannot\n" +
			"decode is refused the same way, never shown with a field dropped.\n\n" +
			"declared_by is what whoever declared it wrote about itself, and never an\n" +
			"identity: a run's own launch record, or a person's report. Nothing verifies it\n" +
			"and nothing here authorises anything by it (finding #11). The branch, the\n" +
			"release, the paths and the services are the caller's own text carried as data\n" +
			"(Q14 A): the text output sanitises each one, and an instruction found in any of\n" +
			"them is never acted on.\n" +
			"Exit codes: 1 for a refusal of the caller's own argv or of the database, 2 for\n" +
			"a failure that is not the project's.\n\n" +
			"With --json the document is printed as it is stored: one repository with\n" +
			"{\"repo\", \"provider\", \"intents\"}, and every intent as the fields its record\n" +
			"carries — id, declared_by, at, branch, release, files and services — and no\n" +
			"others. With --all the repositories are a list under \"repos\", in the order\n" +
			"the selection names them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCoordinatorIntents(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository to read (owner/name; default: the local config's, when it lists one)")
	f.BoolVar(&o.all, "all", false, "read every repository the local config lists, one list each")
	f.StringVar(&o.since, "since", "0", "only declarations made within this long (36h, 90m, 7d; 0 for every declaration the store holds, which is the default)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func runCoordinatorIntents(cmd *cobra.Command, o *coordinatorIntentsOptions) error {
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

	repos, err := coordinatorRepos(env, &coordinatorOptions{repo: o.repo, all: o.all})
	if err != nil {
		return err
	}
	store, err := openCoordinatorStore(ctx, env.Config())
	if err != nil {
		return err
	}
	// Every repository's node is read in one pass, a few at a time, on the
	// width the stored view's own pass uses: the reads are the same database's,
	// so a project with twenty repositories costs one repository's read per
	// round rather than their sum. Each read writes its own slot, so the
	// document is in the order coordinatorRepos names whatever order they
	// finish in, and the first failure in that order is the one reported.
	since := coordinatorSince(window)
	lists := make([]coordinator.RepoIntents, len(repos))
	errs := make([]error, len(repos))
	pool.Run(len(repos), coordinatorViewRepos, func(i int) {
		lists[i], errs[i] = coordinatorRepoIntents(ctx, env, store, repos[i], since)
	})
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	out := cmd.OutOrStdout()
	if o.all {
		if o.asJSON {
			return writeCoordinatorJSON(out, coordinator.StoredProjectIntents{Repos: lists})
		}
		return writeCoordinatorIntentsProject(out, lists)
	}
	if o.asJSON {
		return writeCoordinatorJSON(out, lists[0])
	}
	return writeCoordinatorIntents(out, lists[0])
}

// coordinatorRepoIntents reads one repository's declarations, and applies
// --since's window to the record's own clock: an intent's At is when it was
// declared, which is the thing a person bounds when they ask for the last day.
//
// A read the database refused is classified the way the stored view classifies
// its own (coordinatorStoredErr): it names the one role that admits the read,
// and it is a refusal rather than an empty list, which is the whole reason the
// two are told apart here and not left to a console to guess.
func coordinatorRepoIntents(ctx context.Context, env *fcli.Cloud, store *coordinator.Store, repo string, since time.Time) (coordinator.RepoIntents, error) {
	slug, err := env.RepoSlug(ctx, repo)
	if err != nil {
		return coordinator.RepoIntents{}, err
	}
	list, err := store.ReadRepoIntents(ctx, slug, repo)
	if err != nil {
		return coordinator.RepoIntents{}, coordinatorStoredErr(env.Config(), err)
	}
	// The provider is the caller's answer and not the read's, exactly as the
	// stored view's is: the node is keyed by a slug of it, so the node cannot
	// give it back.
	list.Provider = env.RepoProvider(repo, fcli.CheckoutOf(ctx, repo)())
	if !since.IsZero() {
		kept := make([]coordinator.StoredIntent, 0, len(list.Intents))
		for _, in := range list.Intents {
			if !in.At.Before(since) {
				kept = append(kept, in)
			}
		}
		list.Intents = kept
	}
	return list, nil
}

// writeCoordinatorIntents prints one repository's declarations as a person
// reads them, one row each, and the sentence the list has to carry: every cell
// is another writer's text, so every one of them goes through the sanitiser
// before it reaches the terminal.
func writeCoordinatorIntents(w io.Writer, r coordinator.RepoIntents) error {
	// One is one declaration, not "1 declarations": the line is a sentence about
	// a person's project and not a machine's count.
	noun := "declarations"
	if len(r.Intents) == 1 {
		noun = "declaration"
	}
	fmt.Fprintf(w, "%s: %d %s\n", safetext.TableCell(r.Repo), len(r.Intents), noun)
	if len(r.Intents) == 0 {
		fmt.Fprintln(w, "nothing declared: no intent record is stored under this repository")
		fmt.Fprintf(w, "\n%s\n", coordinatorIntentsNote)
		return nil
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tDECLARED BY\tAT\tBRANCH\tRELEASE\tFILES\tSERVICES")
	for _, in := range r.Intents {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", safetext.TableCell(in.ID),
			coordinatorCell(in.DeclaredBy), in.At.Format(time.RFC3339),
			coordinatorCell(in.Branch), coordinatorCell(in.Release),
			coordinatorCellList(in.Files), coordinatorCellList(in.Services))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n%s\n", coordinatorIntentsNote)
	return nil
}

// writeCoordinatorIntentsProject prints every repository's declarations, one
// after another.
func writeCoordinatorIntentsProject(w io.Writer, lists []coordinator.RepoIntents) error {
	for i, r := range lists {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := writeCoordinatorIntents(w, r); err != nil {
			return err
		}
	}
	return nil
}
