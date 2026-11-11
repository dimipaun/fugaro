package issues

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// otherInstallation is the digest of another project's runs bucket: a machine
// that serves two projects keeps both projects' issues in one folder, and each
// one's `sync` uploads only its own (F8b).
const otherInstallation = "0123456789abcdef"

// The link a filed issue was filed as survives the upload: `issue submit` files
// with the person's own `gh` and records the URL GitHub answered with while the
// record is still `uploaded: false` (T-IS7, F28), and the shared store's upload
// numbers the issue without having a link of its own (Uploaded). This is the
// second caller of MarkUploaded, and it names no URL: a record that lost the one
// it carried would answer `issue show` with no link to the issue the person was
// told about, with nothing anywhere to restore it from.
func TestSyncKeepsTheURLTheIssueWasFiledAs(t *testing.T) {
	ctx := context.Background()
	_, env := issueState(t)
	store := issueStore(t, env)
	store.Installation = issueInstallation
	b := sharedBucket(t)

	const filedURL = "https://github.com/dimipaun/fugaro/issues/4242"
	filed := issueLocal()
	filed.Status, filed.URL = StatusSubmitted, filedURL
	if err := store.Save(filed); err != nil {
		t.Fatalf("saving the filed issue: %v", err)
	}

	deps := identityDeps(identityEnv(t), verifiedLookup(testMember))
	got, err := SyncPending(ctx, store, b, issueInstallation, deps)
	if err != nil {
		t.Fatalf("syncing: %v", err)
	}
	if got.Uploaded != 1 {
		t.Fatalf("uploaded %d issue(s), want the filed one: %v", got.Uploaded, got.Notes)
	}
	back, err := store.Get(filed.LocalID)
	if err != nil {
		t.Fatalf("reading the filed issue back: %v", err)
	}
	if !back.Uploaded || back.Number == 0 {
		t.Errorf("the synced record is uploaded=%v with number %d, want it numbered", back.Uploaded, back.Number)
	}
	if back.URL != filedURL {
		t.Errorf("the synced record's URL is %q, want the link it was filed as, %q", back.URL, filedURL)
	}
}

// An issue of another installation is left where it is and named in one line:
// the summary, diagnosis, log excerpts and command of one project must never
// reach the other project's store (F8b).
func TestSyncSkipsOtherInstallationsIssues(t *testing.T) {
	ctx := context.Background()
	_, env := issueState(t)
	store := issueStore(t, env)
	store.Installation = issueInstallation
	b := sharedBucket(t)

	mine := issueLocal()
	if err := store.Save(mine); err != nil {
		t.Fatalf("saving the local issue: %v", err)
	}
	theirs := issueLocal()
	theirs.Run = "update-20261107183013-48214"
	theirs.LocalID = localPrefix + theirs.Run
	theirs.Installation = otherInstallation
	if err := store.Save(theirs); err != nil {
		t.Fatalf("saving the other project's issue: %v", err)
	}

	deps := identityDeps(identityEnv(t), verifiedLookup(testMember))
	got, err := SyncPending(ctx, store, b, issueInstallation, deps)
	if err != nil {
		t.Fatalf("syncing: %v", err)
	}
	if got.Uploaded != 1 {
		t.Errorf("uploaded %d issue(s), want 1: only this installation's", got.Uploaded)
	}
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], theirs.LocalID) {
		t.Errorf("the notes are %q, want one line naming %s", got.Notes, theirs.LocalID)
	}
	// The other project's issue is still where it was, and still local.
	left, err := store.Get(theirs.LocalID)
	if err != nil {
		t.Fatalf("reading the other project's issue back: %v", err)
	}
	if left.Uploaded || left.Number != 0 {
		t.Errorf("the other project's issue = %+v, want it untouched", left)
	}
	if left.Installation != otherInstallation {
		t.Errorf("the other project's issue names %q, want %q", left.Installation, otherInstallation)
	}
}

// TestSyncRefusesTraversalName: a name read from the store is validated before
// any path is built from it (F20). The issues folder is a directory a person
// and any other process can write, so a record whose header names a run outside
// it must never become a path outside the store, and must never be uploaded as
// the reporter's own issue. A file whose own name is a traversal is the other
// half of the same rule: it is not one of ours, and nothing reads it.
func TestSyncRefusesTraversalName(t *testing.T) {
	ctx := context.Background()
	_, env := issueState(t)
	store := issueStore(t, env)
	store.Installation = issueInstallation
	b := sharedBucket(t)
	dir := store.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("making the store's folder: %v", err)
	}

	// One file of ours by name, whose header names a run outside the folder:
	// the name is what the store's reader selects files by, and the header is
	// what it would then believe.
	rec := issueLocal()
	raw, err := Marshal(rec)
	if err != nil {
		t.Fatalf("marshalling the record: %v", err)
	}
	evil := bytes.ReplaceAll(raw, []byte(rec.Run), []byte("../../evil"))
	if bytes.Equal(evil, raw) {
		t.Fatal("the fixture does not carry the run stem it replaces")
	}
	if err := os.WriteFile(filepath.Join(dir, rec.LocalID+fileSuffix), evil, 0o600); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "local-..-..-evil"+fileSuffix), raw, 0o600); err != nil {
		t.Fatalf("writing the second record: %v", err)
	}

	deps := identityDeps(identityEnv(t), verifiedLookup(testMember))
	got, err := SyncPending(ctx, store, b, issueInstallation, deps)
	if err != nil {
		t.Fatalf("syncing: %v", err)
	}
	if got.Uploaded != 0 || len(got.Notes) != 0 {
		t.Errorf("syncing uploaded %d issue(s) with notes %q, want nothing: neither file is a record of ours", got.Uploaded, got.Notes)
	}
	if rows, err := ListShared(ctx, b); err != nil {
		t.Fatalf("reading the shared store: %v", err)
	} else if len(rows) != 0 {
		t.Errorf("the shared store holds %d issue(s), want none", len(rows))
	}
	// The two directories `../../` points at from the store: nothing was built
	// there, and the folder itself is unchanged.
	for _, outside := range []string{
		filepath.Join(dir, "..", "..", "evil"+fileSuffix),
		filepath.Join(dir, "..", "evil"+fileSuffix),
	} {
		if _, err := os.Stat(outside); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("sync built the path %s (err = %v)", outside, err)
		}
	}
}
