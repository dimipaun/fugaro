// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// `fugaro coordinator show` (coordinator v2 C3, D816): one pull request as the
// host has it, whole — the description included.
//
// The credential is the coordinator's own and not a second path to one: the
// read goes through coordinatorReader, so it is the same installation token,
// minted from the GitHub App the environment holds with the read-only
// permission set, cached for its life and revoked when the command is done
// (github.CoordinatorTokenPermissions, no `write` anywhere). With the
// installation's credentials service in the environment the App's key is the
// service's and this command holds none, so it refuses — the property
// reconciliation F9 says every read of this shape inherits.
//
// The provider is the repository's own, resolved the way the stored view's
// provider field is (C1: the local config's entry, then the checkout's
// fugaro.yaml). A repository on a host the coordinator cannot read is refused
// with the project-wide read's own reason rather than silently served.

// coordinatorShowOptions is the argv: the repository and the pull request, and
// the cloud group's flags, which is how the provider is resolved.
type coordinatorShowOptions struct {
	cloud  fcli.CloudFlags
	repo   string
	pr     int
	asJSON bool
}

func newCoordinatorShowCmd() *cobra.Command {
	var o coordinatorShowOptions
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show one pull request as the host has it",
		Long: "One pull request as the host has it, whole: the number, the address, the\n" +
			"title, the description, the state, the draft flag, the source branch and the\n" +
			"branch it targets, and the author's account id, each exactly as the host\n" +
			"answered it.\n\n" +
			"The description is why this read exists: the stored view carries no body — a\n" +
			"body is unbounded text written by whoever opened the pull request, and one\n" +
			"per record would ride in the document every reader of the view fetches — so\n" +
			"a console that draws one reads it here. It is untrusted text, and a renderer\n" +
			"sanitises it before it reaches a line or a cell.\n\n" +
			"The read is the coordinator's own: one installation token for the\n" +
			"repository, minted from the GitHub App the environment holds and carrying\n" +
			"the named read-only permission set — pull requests and metadata (Q3 answered\n" +
			"(A)) — and revoked when the command is done, so the credential lives for the\n" +
			"read and no longer. No credential of the host's reaches the output.\n" +
			"With FUGARO_CREDENTIALS_SERVICE_URL set, the installation's credentials\n" +
			"service holds the App's key and this command holds none, so it refuses\n" +
			"(exit 1) before it reads the App's ID or key: the service mints a run's\n" +
			"token, and this read is not a run.\n" +
			"A repository on a host this read does not serve is refused the way the\n" +
			"project-wide view refuses it (reconciliation Q12 (A), finding #30): the\n" +
			"coordinator reads github only, because its bitbucket credential is one\n" +
			"repository's access token and cannot be widened to a project.\n\n" +
			"A pull request the host does not have is the host's own failure (exit 2),\n" +
			"carrying the host's status, and never an empty document: an empty one would\n" +
			"draw a pull request with no description, which is a different fact.\n" +
			"Exit codes: 1 for a missing or malformed App credential (or the refusals\n" +
			"above), 2 for a failure of the host.\n\n" +
			"With --json the document is printed as it is: number, url, title, body,\n" +
			"state, draft, branch, base and author, every field the host's own answer and\n" +
			"absent when it gave none.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCoordinatorShow(cmd, &o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.repo, "repo", "", "the repository the pull request is in (owner/name)")
	f.IntVar(&o.pr, "pr", 0, "the pull request number")
	f.BoolVar(&o.asJSON, "json", false, "print machine-readable output")
	fcli.AddCloudFlags(cmd, &o.cloud)
	return cmd
}

func runCoordinatorShow(cmd *cobra.Command, o *coordinatorShowOptions) error {
	ctx := cmd.Context()
	// The argv is judged first, and nothing is opened to judge it: a --repo
	// that is not owner/name and a --pr that is not a pull request number are
	// the caller's own, and neither is worth a config read, a token or a call
	// to the host.
	if _, _, ok := gitprov.SplitRepo(o.repo); !ok {
		return fcli.UserErr("--repo %q must look like owner/name", o.repo)
	}
	if o.pr <= 0 {
		return fcli.UserErr("--pr %d is not a pull request number", o.pr)
	}
	env, err := fcli.OpenCloud(ctx, &o.cloud)
	if err != nil {
		return err
	}
	defer env.Close()

	co := fcli.CheckoutOf(ctx, o.repo)()
	// RepoSlug is the read that refuses a repository whose provider cannot be
	// told at all, and it comes before anything is opened: a repository the
	// caller's own configuration cannot place is the caller's to fix and not a
	// failure of the host (coordinatorViewOf's own order).
	if _, err := env.RepoSlug(ctx, o.repo); err != nil {
		return err
	}

	reader, tokens, err := coordinatorReader(cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	// The token is taken back on every return, whatever happened on the way:
	// it lives for the read and no longer (coordinatorTokens.revoke).
	defer tokens.revoke(ctx)

	doc, err := reader.PullRequest(ctx, env.RepoProvider(o.repo, co), o.repo, o.pr)
	if err != nil {
		// The refusal of a project this read does not serve is the
		// configuration's (reconciliation Q12 (A), finding #30). Everything else
		// is the host's — unless the reader kept the class of what failed, which
		// it does for the mint's own refusal of a missing App credential
		// (coordinator's redactedError): fcli.RemoteErr() marks a failure exit 2
		// only when it does not already carry a code, so a user error the reader
		// rewrote the text of is still a user error here.
		if errors.Is(err, coordinator.ErrBitbucket) {
			return fcli.UserErr("%v", err)
		}
		return fcli.RemoteErr(err)
	}
	out := cmd.OutOrStdout()
	if o.asJSON {
		return writeCoordinatorJSON(out, doc)
	}
	return writeCoordinatorShow(out, o.repo, doc)
}

// writeCoordinatorShow prints one pull request as a person reads it: the
// host's own words, each through the sanitiser, and the description as a block
// of text. Nothing here is a link or an element, and nothing is derived — the
// lines say what the document says and no more.
func writeCoordinatorShow(w io.Writer, repo string, d coordinator.PRDocument) error {
	kind := "pull request"
	if d.Draft {
		kind = "draft pull request"
	}
	if _, err := fmt.Fprintf(w, "%s#%d: %s\n", safetext.TableCell(repo), d.Number, safetext.OneLine(d.Title)); err != nil {
		return err
	}
	parts := []string{kind}
	if d.State != "" {
		parts = append(parts, safetext.OneLine(d.State))
	}
	if d.Branch != "" {
		branch := safetext.OneLine(d.Branch)
		if d.Base != "" {
			branch += " -> " + safetext.OneLine(d.Base)
		}
		parts = append(parts, branch)
	}
	if d.Author != "" {
		parts = append(parts, "author "+safetext.OneLine(d.Author))
	}
	fmt.Fprintln(w, strings.Join(parts, ", "))
	if d.URL != "" {
		fmt.Fprintln(w, safetext.OneLine(d.URL))
	}
	if d.Body != "" {
		fmt.Fprintf(w, "\n%s\n", safetext.MultiLine(d.Body))
	}
	return nil
}
