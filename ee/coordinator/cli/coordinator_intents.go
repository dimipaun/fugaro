// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// `fugaro coordinator intents` (coordinator v2 C4, D827): the declarations the
// project's callers have reported, read out of the coordinator's own database.
//
// It is the read the console's transcript pane is built on. The records are in
// the tree — report writes them there (P4) and the sweep reads them to shape a
// view (P5) — and no command could print them: a view holds what the
// declarations mean for a collision, and never a declaration itself.
//
// Three properties are the read rather than niceties around it:
//
//   - It is the person's own database read, the one `status --from-store`
//     makes (D364): the client is newBudgetClient's — the caller's own
//     credential, on the project's budget.rtdb_url, which is the database the
//     coordinator's subtree lives in — and what admits it is
//     roles/firebasedatabase.viewer, the IAM role launchers and operators
//     hold. It is an IAM read and not a rules read.
//   - It opens NO host and holds no credential of one. No App ID, no App key,
//     no Bitbucket token, and no provider is opened: with the installation's
//     credentials service in the environment the command still succeeds,
//     where the project-wide read (coordinatorReader) refuses. Everything it
//     prints is what the report path wrote into the database once.
//   - The text in a record is another writer's, and it is carried as data. The
//     declaration, the branch, the release and the lists are strings a caller
//     wrote about itself; every one of them goes through safetext and is
//     clipped before it reaches a line or a cell, and declared by is a
//     declaration and never an identity (the note under the table).
//
// A repository with no records is an empty list and NOT a failure: the
// coordinator is off on most installations, and a console that had to know
// which before it picked an argv would be the one place that has to. A read
// the database refused is the other answer, and it prints no document at all —
// an empty one would say the project had declared nothing.

// coordinatorIntentsOptions is the argv: the repository selection, the window,
// and the cloud group's flags, which is how each repository's provider is
// resolved.
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
		Short: "Show what the project's callers have declared",
		Long: "What the project's callers have reported, read out of the coordinator's own\n" +
			"database: per repository, every declaration still standing — who declared\n" +
			"it, when, the branch and the target release, the files and the services —\n" +
			"each record whole and in no other command's output.\n\n" +
			"One repository by default, every repository the local config lists with\n" +
			"--all, each in its own block and compared with no other: a declaration is\n" +
			"about one repository's tree, and two repositories' paths say nothing about\n" +
			"each other.\n\n" +
			"A repository the database holds no records for prints no records, which is\n" +
			"a fact about the store and not a failed read: the coordinator is off on\n" +
			"most installations, and a caller with nothing declared is the ordinary\n" +
			"state of a project between two pieces of work. A read the database refused\n" +
			"is refused (exit 1) naming the role that admits it, and prints no document.\n\n" +
			"The read is the person's own and holds no key: it is the stored view's read\n" +
			"(status --from-store, D364), through the caller's own credentials, and what\n" +
			"admits it is roles/firebasedatabase.viewer, which launchers and operators\n" +
			"hold. No host is opened and no App ID, App key or Bitbucket credential is\n" +
			"read, so it works with FUGARO_CREDENTIALS_SERVICE_URL set, where the\n" +
			"project-wide read refuses.\n\n" +
			"Every string in a record was written by someone else, and it is carried as\n" +
			"data: declared by is the string the reporter's own machine said about it,\n" +
			"which is a declaration and never a verified identity.\n\n" +
			"With --json every record is printed as it is stored: id, declared_by, at,\n" +
			"branch, release, files and services — the fields an intent already carries\n" +
			"and no others. id is the node's key (IntentID), and a repository with no\n" +
			"records carries an empty list rather than a missing one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCoordinatorIntents(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository to read (owner/name)")
	f.BoolVar(&o.all, "all", false, "read every repository the local config lists")
	f.StringVar(&o.since, "since", coordinatorDefaultSince, "only declarations made within this long ("+coordinatorDefaultSince+", 36h, 90m; 0 for all)")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

// coordinatorIntentsDeclaredNote is the line the table carries under it, and
// it is the same sentence the view's own table carries for the same reason:
// the column above it is the one column of the read that is not a fact.
const coordinatorIntentsDeclaredNote = "declared by is who the reporter's own machine said it was when the record was\n" +
	"written (localcfg.Config.Me: the local config's user, else git config\n" +
	"user.email, spelled by that caller): a declaration, and never a verified\n" +
	"identity (docs/coordinator/reconciliation.md, finding #11)."

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

	// The selection is status's own, judged by status's own rule: one
	// repository, or every one the config lists, and never both at once.
	repos, err := coordinatorRepos(env, &coordinatorOptions{repo: o.repo, all: o.all})
	if err != nil {
		return err
	}
	store, err := openCoordinatorStore(ctx, env.Config())
	if err != nil {
		return err
	}
	since := coordinatorSince(window)
	got := make([]coordinator.RepoIntents, len(repos))
	for i, repo := range repos {
		slug, err := env.RepoSlug(ctx, repo)
		if err != nil {
			return err
		}
		reads, err := store.ReadRepoIntents(ctx, slug, repo)
		if err != nil {
			return coordinatorStoredErr(env.Config(), err)
		}
		// The provider is the caller's answer and not the read's, exactly as
		// the stored view's is: the node is keyed by a slug of it, so the node
		// cannot give it back.
		reads.Provider = env.RepoProvider(repo, fcli.CheckoutOf(ctx, repo)())
		reads.Intents = coordinatorIntentsIn(reads.Intents, since)
		got[i] = reads
	}
	out := cmd.OutOrStdout()
	if o.all {
		if o.asJSON {
			return writeCoordinatorJSON(out, coordinator.ProjectIntents{Repos: got})
		}
		return writeCoordinatorIntentsProject(out, got)
	}
	if o.asJSON {
		return writeCoordinatorJSON(out, got[0])
	}
	return writeCoordinatorIntents(out, got[0])
}

// coordinatorIntentsIn keeps the declarations made at or after since, and
// every one of them when since is the zero time (--since 0). The window is
// measured against the record's own At — the declaration's clock, stamped by
// the reporter — and not against the read's.
//
// The answer is a list and never nil: a window with nothing in it is "declared
// nothing that recently", which is not the same answer as a read that failed.
func coordinatorIntentsIn(recs []coordinator.IntentRecord, since time.Time) []coordinator.IntentRecord {
	if since.IsZero() {
		return recs
	}
	out := make([]coordinator.IntentRecord, 0, len(recs))
	for _, rec := range recs {
		if !rec.At.Before(since) {
			out = append(out, rec)
		}
	}
	return out
}

// writeCoordinatorIntents prints one repository's declarations: a line saying
// how many there are, the table, and the note.
func writeCoordinatorIntents(w io.Writer, v coordinator.RepoIntents) error {
	fmt.Fprintln(w, coordinatorIntentsLine(v))
	if len(v.Intents) > 0 {
		fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "DECLARED BY\tAT\tBRANCH\tRELEASE\tFILES\tSERVICES")
		for _, rec := range v.Intents {
			// The record's ID is not a column: it is the node's key — a hash of
			// the declaration — and a person reading a table is not looking for
			// one. The document carries it for the console, which addresses a
			// record by it.
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				coordinatorCell(rec.DeclaredBy),
				rec.At.Format(time.RFC3339),
				coordinatorCell(rec.Branch),
				coordinatorCell(rec.Release),
				coordinatorCellList(rec.Files),
				coordinatorCellList(rec.Services))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	fmt.Fprintf(w, "\n%s\n", coordinatorIntentsDeclaredNote)
	return nil
}

// writeCoordinatorIntentsProject prints every repository's declarations, one
// block after another, in the order the caller read them.
func writeCoordinatorIntentsProject(w io.Writer, reads []coordinator.RepoIntents) error {
	for i, v := range reads {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := writeCoordinatorIntents(w, v); err != nil {
			return err
		}
	}
	return nil
}

// coordinatorIntentsLine is the one line that says what the block below it
// holds: how many declarations the repository has, or that it has none.
func coordinatorIntentsLine(v coordinator.RepoIntents) string {
	switch n := len(v.Intents); n {
	case 0:
		return fmt.Sprintf("%s: no intent records", safetext.TableCell(v.Repo))
	case 1:
		return fmt.Sprintf("%s: 1 intent", safetext.TableCell(v.Repo))
	default:
		return fmt.Sprintf("%s: %d intents", safetext.TableCell(v.Repo), n)
	}
}
