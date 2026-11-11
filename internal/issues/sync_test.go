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

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// issueOtherInstallation is another project's name: the digest of its own runs
// bucket URL, which is what a record saved while working in it carries (F8b).
const issueOtherInstallation = "3b7d1c2a0e4f5a69"

// syncEnv places both directories SyncPending reads: the state directory the
// store lives in, and the cache the identity is resolved through.
func syncEnv(t *testing.T) func(string) string {
	t.Helper()
	state, cache := t.TempDir(), t.TempDir()
	return func(k string) string {
		switch k {
		case "XDG_STATE_HOME":
			return state
		case "XDG_CACHE_HOME":
			return cache
		}
		return ""
	}
}

// syncDeps is the identity a write is filed under: one verified credential,
// resolved without a network call.
func syncDeps(env func(string) string) Deps {
	return Deps{
		Lookup: func(context.Context) (string, error) { return testMember, nil },
		Getenv: env,
		GOOS:   "linux",
	}
}

// syncRec is a record this machine saved and has not uploaded: the shape
// SyncPending works on.
func syncRec() Issue {
	i := issueSample()
	i.Number, i.Uploaded, i.URL = 0, false, ""
	return i
}

// TestSyncRefusesTraversalName: a name read from the store is validated before
// any path is built from it (F20). The issues folder is a directory a person
// and any other process can write, so a record whose header names a run
// outside it — and a file whose own name is not one of ours — must never
// become a path outside the store, and must never be uploaded as the
// reporter's own issue.
func TestSyncRefusesTraversalName(t *testing.T) {
	env := syncEnv(t)
	store := issueStore(t, env)
	dir := store.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("making the store's folder: %v", err)
	}

	// One file of ours by name, whose header names a run outside the folder:
	// the name is what the store's own reader selects files by, and the header
	// is what it then believes.
	rec := syncRec()
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
	// A file beside it whose own name is the traversal: nothing selects it,
	// which is the other half of the same rule.
	if err := os.WriteFile(filepath.Join(dir, "local-..-..-evil"+fileSuffix), raw, 0o600); err != nil {
		t.Fatalf("writing the second record: %v", err)
	}

	before := syncDirNames(t, filepath.Dir(dir))
	b := gcpfake.NewGCS(t).Bucket(t, "runs")
	res, err := SyncPending(context.Background(), store, b, issueInstallation, syncDeps(env))
	if err != nil {
		t.Fatalf("SyncPending: %v", err)
	}
	if res.Uploaded != 0 || len(res.Notes) != 0 {
		t.Errorf("SyncPending uploaded %d issue(s) with notes %q, want nothing: neither file is a record of ours", res.Uploaded, res.Notes)
	}
	rows, err := ListShared(context.Background(), b)
	if err != nil {
		t.Fatalf("reading the shared store: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("the shared store holds %d issue(s), want none", len(rows))
	}
	// The folder's parent is where `../../evil` points: nothing was built
	// there, and nothing beside the store was touched at all.
	if after := syncDirNames(t, filepath.Dir(dir)); !equalNames(before, after) {
		t.Errorf("sync wrote outside the store: %v became %v", before, after)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "..", "evil"+fileSuffix)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a record outside the store was written (err = %v)", err)
	}
}

// syncDirNames is one directory's entries, sorted, for a comparison that says
// whether anything appeared beside the store.
func syncDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSyncPendingUploadsOnce: the record SyncPending uploads is the one the
// store holds, renamed to its number, and the second run has nothing left to
// do (F9): the number and the uploaded flag are what make a retry safe.
func TestSyncPendingUploadsOnce(t *testing.T) {
	env := syncEnv(t)
	store := issueStore(t, env)
	rec := syncRec()
	rec.Installation = issueInstallation
	if err := store.Save(rec); err != nil {
		t.Fatalf("saving: %v", err)
	}
	b := gcpfake.NewGCS(t).Bucket(t, "runs")
	deps := syncDeps(env)

	res, err := SyncPending(context.Background(), store, b, issueInstallation, deps)
	if err != nil {
		t.Fatalf("SyncPending: %v", err)
	}
	if res.Uploaded != 1 || len(res.Notes) != 0 {
		t.Fatalf("SyncPending uploaded %d with notes %q, want one and none", res.Uploaded, res.Notes)
	}
	got, err := store.Get("1")
	if err != nil {
		t.Fatalf("reading the uploaded issue: %v", err)
	}
	if got.Fingerprint != rec.Fingerprint || got.Status != rec.Status {
		t.Errorf("the uploaded record is %+v, want the one the store held", got)
	}
	// The reporter is the verified credential's and not the record's header:
	// the header is a file on this machine (F3).
	shared, err := SharedIssue(context.Background(), b, 1)
	if err != nil {
		t.Fatalf("reading the shared issue: %v", err)
	}
	if shared.Reporter != testMember {
		t.Errorf("the shared issue is filed by %q, want the credential's %q", shared.Reporter, testMember)
	}

	again, err := SyncPending(context.Background(), store, b, issueInstallation, deps)
	if err != nil {
		t.Fatalf("the second SyncPending: %v", err)
	}
	if again.Uploaded != 0 {
		t.Errorf("the second SyncPending uploaded %d issue(s) again", again.Uploaded)
	}
	if rows, err := ListShared(context.Background(), b); err != nil {
		t.Fatalf("reading the shared store: %v", err)
	} else if len(rows) != 1 {
		t.Errorf("the shared store holds %d issue(s), want the one", len(rows))
	}
}

// TestSyncPendingKeepsAnotherProjectsIssue: the folder is one for the whole
// machine, so a record stamped with another project's name is left where it is
// and named in one line (F8b) — its summary, diagnosis and log excerpts must
// never reach this project's store.
func TestSyncPendingKeepsAnotherProjectsIssue(t *testing.T) {
	env := syncEnv(t)
	store := issueStore(t, env)
	other := syncRec()
	other.Installation = issueOtherInstallation
	other.Fingerprint = Fingerprint("sig-other")
	if err := store.Save(other); err != nil {
		t.Fatalf("saving: %v", err)
	}
	mine := syncRec()
	mine.Run = "update-20261107183012-48214"
	mine.LocalID = localPrefix + mine.Run
	mine.Installation = issueInstallation
	mine.Fingerprint = Fingerprint("sig-mine")
	if err := store.Save(mine); err != nil {
		t.Fatalf("saving: %v", err)
	}

	b := gcpfake.NewGCS(t).Bucket(t, "runs")
	res, err := SyncPending(context.Background(), store, b, issueInstallation, syncDeps(env))
	if err != nil {
		t.Fatalf("SyncPending: %v", err)
	}
	if res.Uploaded != 1 {
		t.Errorf("SyncPending uploaded %d issue(s), want this project's one", res.Uploaded)
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], other.LocalID) {
		t.Errorf("the notes are %q, want one naming %s", res.Notes, other.LocalID)
	}
	rows, err := ListShared(context.Background(), b)
	if err != nil {
		t.Fatalf("reading the shared store: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the shared store holds %d issue(s), want one", len(rows))
	}
	if rows[0].Issue.Fingerprint == other.Fingerprint {
		t.Error("another project's issue reached this project's store")
	}
}
