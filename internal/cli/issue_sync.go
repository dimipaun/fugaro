package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/issues"
)

// issue_sync.go holds the shared half of the `issue` family: the command that
// uploads this machine's pending issues, and the one resolution of the
// project's store that `sync`, `list`, `show` and `dismiss` all read through.
//
// Uploading is a cloud write, and `sync` and `dismiss` are an agent's to make
// (D3425): a session reaches the project's store with them like anyone else.
// The listing commands read the project's store too (D3425 lifts the read) and
// upload nothing on the way, and `submit --ask` is a person's.

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
	// deps is what the identity lookup runs against.
	deps issues.Deps
	// agent marks a coding agent's session. The session reads the project's
	// store like any other (D3425 lifts the read) and uploads nothing: the
	// upload is `issue sync`'s write, and a listing must not make it on the way.
	agent bool
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
// A caller that passes its session's marker, a listing, reads the store exactly
// as a person's session does: it resolves the project, opens the bucket and
// asks the credential, and uploadPending skips it on the way. `sync` and
// `dismiss` pass none, since a session may write the project's issues (D3425).
//
// The second return is D1541's fence and never a store problem: the store's
// own failures stay the notes they always were. The project's issues are the
// project's own state, so the fence is asked by the two commands that write
// them — `issue sync` and `issue dismiss` — and the read is made here, where
// the project's name becomes known and before mineOpen could build a client.
// An exempt command — every reader of this store — is answered without a read
// (requireManaged), so a listing never pays for the fence and never fails on
// it; a caller that ignores the error would be writing the store of a project
// the fence refused, which is why every caller returns it.
func openIssueShared(ctx context.Context, agent string) (*issueShared, error) {
	sh := &issueShared{
		agent: agent != "",
		deps: issues.Deps{
			Lookup: mineCaller,
			Getenv: os.Getenv,
			GOOS:   runtime.GOOS,
		},
	}
	if sh.agent {
		sh.notes = append(sh.notes, "note: this is a coding agent's session: the project's issues are read, and nothing is uploaded")
	}
	lc, err := mineProject(ctx)
	if err != nil {
		sh.notes = append(sh.notes, "note: the project's runs bucket could not be found ("+oneLine(err.Error())+"), so the project's issues are not read")
		return sh, nil
	}
	if err := requireManaged(ctx, managedCommandFrom(ctx), lc); err != nil {
		return sh, err
	}
	sh.installation = issues.Digest(lc.BucketURL())
	b, err := mineOpen(ctx, lc)
	if err != nil {
		sh.notes = append(sh.notes, "note: the project's shared store could not be opened ("+oneLine(err.Error())+")")
		return sh, nil
	}
	sh.bucket = b
	return sh, nil
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

// issueProjectName is the name of the project this machine's issues belong to,
// and nothing else. It reads the checkout and the local config, so it reaches
// no credential and no bucket, and a command that only has to tell this
// project's records from another's (F8b) needs no more than this: `dismiss` of
// an issue this machine has a file of is a local write, and it must not open a
// cloud client to find that out.
//
// A run that names no project answers with the empty name, which no record
// carries, so nothing is another project's and every record is kept — the same
// answer openIssueShared gives.
func issueProjectName(ctx context.Context) string {
	lc, err := mineProject(ctx)
	if err != nil {
		return ""
	}
	return issues.Digest(lc.BucketURL())
}

// issueOtherProjects reports whether a record this machine's folder holds
// belongs to another project (F8b). The folder is one for the whole machine, so
// a record stamped with a name that is not this project's is another project's
// issue: its number came from that project's store, and printing it or
// resolving a reference to it here would answer with the wrong issue.
//
// A record that names no installation is kept, and so is every record of a run
// that names no project: the first was saved before there was a field to stamp,
// and the second has nothing to attribute anything to. This is `mine`'s rule,
// deliberately — the listing and the store reads answer with the same rows —
// and `issueForeign` is the stricter half of it, for the one command that
// publishes.
func issueOtherProjects(i issues.Issue, installation string) bool {
	return i.Installation != "" && installation != "" && i.Installation != installation
}

// issueNumber is the number a reference names, and whether it names one at all.
// A number is the project's and a local ID is this machine's alone, so the two
// are told apart before anything is opened or read (F20), and a reference that
// is not a number never reaches a store.
func issueNumber(ref string) (int, bool) {
	n, err := strconv.Atoi(ref)
	return n, err == nil && n >= 1
}

// sharedIssueRef reads the issue a numbered reference names from the project's
// store. It is the one path by which `show` and `dismiss` reach an issue this
// machine has no file of, which is every row of the listing that is a
// teammate's: the store is the truth about an issue that has a number (F13),
// and a number is what the listing prints.
//
// A reference that is not a number names a local ID, and a local ID is a name
// only this machine has, so it is answered from the folder here or not at all.
// The number is read as a number and validated by the store's own reader before
// any key is built from it (F20).
func sharedIssueRef(ctx context.Context, sh *issueShared, ref string) (issues.Issue, bool) {
	n, numbered := issueNumber(ref)
	if !numbered || sh.bucket == nil {
		return issues.Issue{}, false
	}
	i, err := issues.SharedIssue(ctx, sh.bucket, n)
	if err != nil {
		sh.notes = append(sh.notes, "note: the project's issue "+issueRefOfNumber(n)+" could not be read ("+oneLine(err.Error())+")")
		return issues.Issue{}, false
	}
	return i, true
}

// uploadPending is the one call to SyncPending in this package: this machine's
// pending issues are uploaded under the verified credential's name, and what
// the upload could not do is a line in the store's own words.
//
// A store read that failed is the only error it can answer with, and it is a
// line here too: an upload that did not happen leaves the issue where it is and
// the next run retries it, which is no reason to take a listing down.
func (s *issueShared) uploadPending(ctx context.Context, store *issues.Store) {
	if s.bucket == nil || s.agent {
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
	ctx := cmd.Context()
	store, err := openIssueStore()
	if err != nil {
		return err
	}
	sh, err := openIssueShared(ctx, "")
	if err != nil {
		return err
	}
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
