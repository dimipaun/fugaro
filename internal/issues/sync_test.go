package issues

import (
	"context"
	"strings"
	"testing"
)

// otherInstallation is the digest of another project's runs bucket: a machine
// that serves two projects keeps both projects' issues in one folder, and each
// one's `sync` uploads only its own (F8b).
const otherInstallation = "0123456789abcdef"

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
