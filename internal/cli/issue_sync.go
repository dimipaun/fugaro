package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/issues"
)

// issue_sync.go holds the shared half of the `issue` family: the command that
// uploads this machine's pending issues, and the one resolution of the
// project's store that `sync`, `list`, `show` and `dismiss` all read through.
//
// Uploading is a cloud write, so every one of them goes through the guard
// `recipes publish` uses (F10): in a coding agent's session nothing is
// uploaded, `sync` refuses and names the command a person runs, and the
// listing commands read this machine's own files only (D1734).

// issueShared is the project's shared store as one `issue` command reaches it:
// the bucket, the name of the project it belongs to, and the lines the run has
// to print about what it could not reach.
//
// A nil bucket is the honest answer of a run that has no project, no credential
// or no network: every caller here then does what it can with this machine's
// own files, which is what the issue store is for. Nothing in it is an error —
// an issue is saved locally first and uploaded when a run can (spec 2).
type issueShared struct {
	bucket *blobx.Bucket
	// installation is the digest of the runs bucket URL: the name this
	// machine's own records and snapshots are filed under (F8b).
	installation string
	// deps is what the identity lookup runs against. A coding agent's session
	// sets NoLookup, so the credential is never asked (F10).
	deps  issues.Deps
	notes []string
}

// Close releases the bucket a run opened.
func (s *issueShared) Close() {
	if s.bucket != nil {
		_ = s.bucket.Close()
		s.bucket = nil
	}
}

// openIssueShared resolves the project this machine's issues belong to and
// opens its store. It is best effort by design: every failure is a line in
// notes and a nil bucket, never an error, because the commands that call it
// have this machine's own records to answer with either way.
//
// It reads the checkout and the local config and nothing else — no credential,
// no network — which is why a coding agent's session may call it and still
// reach no shared store (F10): what a session must not do is upload, and the
// project's own name is a local file.
func openIssueShared(ctx context.Context, agent string) *issueShared {
	sh := &issueShared{deps: issues.Deps{
		Lookup:   mineCaller,
		NoLookup: agent != "",
		Getenv:   os.Getenv,
		GOOS:     runtime.GOOS,
	}}
	if agent != "" {
		sh.notes = append(sh.notes, "note: this is a coding agent's session: nothing is uploaded, and the project's issues are not read")
		return sh
	}
	lc, err := mineProject(ctx)
	if err != nil {
		sh.notes = append(sh.notes, "note: the project's runs bucket could not be found ("+oneLine(err.Error())+"), so the project's issues are not read")
		return sh
	}
	sh.installation = issues.Digest(lc.BucketURL())
	b, err := mineOpen(ctx, lc)
	if err != nil {
		sh.notes = append(sh.notes, "note: the project's shared store could not be opened ("+oneLine(err.Error())+")")
		return sh
	}
	sh.bucket = b
	return sh
}

// why is the line this run has about what it could not reach, as the reason
// half of a refusal: a refusal already says what did not happen, so the "note: "
// a printed line carries would read as one note inside another.
func (s *issueShared) why() string {
	if len(s.notes) == 0 {
		return "the project's store could not be reached"
	}
	return strings.TrimPrefix(s.notes[0], "note: ")
}

// uploadPending is the one call to SyncPending in this package: this machine's
// pending issues are uploaded under the verified credential's name, and what
// the upload could not do is a line in the store's own words.
//
// A store read that failed is the only error it can answer with, and it is a
// line here too: an upload that did not happen leaves the issue where it is and
// the next run retries it, which is no reason to take a listing down.
func (s *issueShared) uploadPending(ctx context.Context, store *issues.Store) {
	if s.bucket == nil {
		return
	}
	res, err := issues.SyncPending(ctx, store, s.bucket, s.installation, s.deps)
	if err != nil {
		s.notes = append(s.notes, "note: this machine's pending issues could not be uploaded: "+oneLine(err.Error()))
		return
	}
	for _, n := range res.Notes {
		s.notes = append(s.notes, "note: "+oneLine(n))
	}
}

// newIssueSyncCmd builds `fugaro issue sync`: upload the issues this machine
// saved and has not filed yet.
func newIssueSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Upload the issues this machine saved and has not uploaded yet",
		Long: "Upload this machine's own pending issues to the project's store.\n\n" +
			"An issue is saved on the machine that hit the failure and uploaded\n" +
			"afterwards, so a run that was offline or that died before the upload\n" +
			"leaves it here. This is the command that retries those: each one takes\n" +
			"the project's next number, is renamed to it, and — when a teammate\n" +
			"already reported the same failure — is attached to their issue as one\n" +
			"more occurrence instead of becoming a second one.\n\n" +
			"An issue that cannot be uploaded stays where it is and is named in one\n" +
			"line, and the next run retries it: nothing here deletes an issue or\n" +
			"files one on GitHub.\n\n" +
			"A coding agent's session may not run this: uploading is a write to the\n" +
			"project's store, and a person runs `fugaro issue sync` (F10).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runIssueSync(cmd) },
	}
	return cmd
}

func runIssueSync(cmd *cobra.Command) error {
	// The guard `recipes publish` uses: an agent-set marker refuses whatever the
	// terminal, and the IDE extension's marker alone refuses only without one,
	// so a person in their own IDE terminal may sync (F10).
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("nothing was uploaded: %s", initflow.AgentRefusal(m))
	}
	ctx := cmd.Context()
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	sh := openIssueShared(ctx, "")
	defer sh.Close()
	if sh.bucket == nil {
		// The project is the command's whole subject: unlike a listing, there is
		// nothing to answer with here, so the reason is the failure.
		return userErr("nothing was uploaded: %s", sh.why())
	}
	// The store stamps the project on every record it writes, which is what
	// keeps one project's issue out of another project's store (F8b).
	store.Installation = sh.installation
	res, err := issues.SyncPending(ctx, store, sh.bucket, sh.installation, sh.deps)
	if err != nil {
		return userErr("nothing was uploaded: %v", err)
	}
	out := cmd.OutOrStdout()
	for _, n := range res.Notes {
		fmt.Fprintln(out, oneLine(n))
	}
	if res.Uploaded == 0 {
		fmt.Fprintln(out, "No pending issues.")
		return nil
	}
	fmt.Fprintf(out, "Uploaded %d issue(s).\n", res.Uploaded)
	return nil
}
