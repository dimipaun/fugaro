package issues

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// issueState makes a state directory and the environment that names it, which
// is where the store's own directory rule is read from.
func issueState(t *testing.T) (string, func(string) string) {
	t.Helper()
	state := t.TempDir()
	return state, func(name string) string {
		if name == "XDG_STATE_HOME" {
			return state
		}
		return ""
	}
}

// issueStore opens the store a state directory places.
func issueStore(t *testing.T, env func(string) string) *Store {
	t.Helper()
	s, err := OpenStore(env, "linux")
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	return s
}

func TestStoreWritesPrivateFiles(t *testing.T) {
	state, env := issueState(t)
	s := issueStore(t, env)
	if err := s.Save(issueSample()); err != nil {
		t.Fatalf("saving: %v", err)
	}
	// The folder is `<UserDir state>/fugaro/issues` and the file is named by
	// the number and the run stem (D1723).
	path := filepath.Join(state, "fugaro", "issues", "0007-update-20261107183012-48213.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the saved issue is not at %s: %v", path, err)
	}
	if runtime.GOOS != "windows" {
		// The issue is the user's own text and can carry a path or a name, so
		// nobody else on the machine reads it.
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("file mode = %o, want 0600", got)
		}
		di, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat %s: %v", filepath.Dir(path), err)
		}
		if got := di.Mode().Perm(); got != 0o700 {
			t.Errorf("directory mode = %o, want 0700", got)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the saved issue: %v", err)
	}
	if !strings.Contains(string(b), "## Summary") {
		t.Errorf("the saved record has no body:\n%s", b)
	}
	got, err := s.Get("7")
	if err != nil {
		t.Fatalf("getting by number: %v", err)
	}
	if got.Summary != issueSample().Summary {
		t.Errorf("Get(7) summary = %q, want %q", got.Summary, issueSample().Summary)
	}
	if got, err := s.Get(issueSample().LocalID); err != nil || got.Number != 7 {
		t.Errorf("Get(%q) = %+v, %v", issueSample().LocalID, got, err)
	}
	if _, err := s.Get("nonsense"); err == nil {
		t.Error("Get accepted a reference that is neither a number nor a local ID")
	}
	if _, err := s.Get("8"); err == nil {
		t.Error("Get found an issue that was never saved")
	}
}

func TestStoreTightensAFolderThatIsWider(t *testing.T) {
	// The folder can already be there — made by an older build, by hand, or by
	// a tool whose umask was loose — so a write tightens it rather than
	// trusting the mode it was created with: what is in it can name a path or
	// a project, and a group-readable issue hands both to every account on the
	// machine.
	if runtime.GOOS == "windows" {
		t.Skip("windows has no file modes")
	}
	_, env := issueState(t)
	s := issueStore(t, env)
	if err := os.MkdirAll(s.Dir(), 0o755); err != nil {
		t.Fatalf("making a wide folder: %v", err)
	}
	// Chmod explicitly: MkdirAll's mode is what the umask leaves of it.
	if err := os.Chmod(s.Dir(), 0o755); err != nil {
		t.Fatalf("widening the folder: %v", err)
	}
	if err := s.Save(issueSample()); err != nil {
		t.Fatalf("saving: %v", err)
	}
	fi, err := os.Stat(s.Dir())
	if err != nil {
		t.Fatalf("stat %s: %v", s.Dir(), err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %o, want 0700", got)
	}
}

func TestStoreRenameOnUpload(t *testing.T) {
	_, env := issueState(t)
	s := issueStore(t, env)
	local := issueLocal()
	if err := s.Save(local); err != nil {
		t.Fatalf("saving the local issue: %v", err)
	}
	before := filepath.Join(s.Dir(), "local-update-20261107183012-48213.md")
	if _, err := os.Stat(before); err != nil {
		t.Fatalf("a local issue is not named local-<stem>.md: %v", err)
	}
	const url = "https://github.com/dimipaun/fugaro/issues/7"
	if err := s.MarkUploaded(local.LocalID, 7, url); err != nil {
		t.Fatalf("marking uploaded: %v", err)
	}
	if _, err := os.Stat(before); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the local name survived the upload: %v", err)
	}
	after := filepath.Join(s.Dir(), "0007-update-20261107183012-48213.md")
	if _, err := os.Stat(after); err != nil {
		t.Fatalf("the uploaded issue is not named <NNNN>-<stem>.md: %v", err)
	}
	got, err := s.Get("7")
	if err != nil {
		t.Fatalf("getting the uploaded issue: %v", err)
	}
	if got.Number != 7 || !got.Uploaded || got.URL != url {
		t.Errorf("after upload: number=%d uploaded=%v url=%q", got.Number, got.Uploaded, got.URL)
	}
	// The same call twice is the same record: an upload that is retried does
	// not lose the file.
	if err := s.MarkUploaded(local.LocalID, 7, url); err != nil {
		t.Fatalf("marking uploaded twice: %v", err)
	}
	if err := s.SetStatus("7", StatusDismissed, issueAt); err != nil {
		t.Fatalf("dismissing: %v", err)
	}
	got, err = s.Get("7")
	if err != nil {
		t.Fatalf("getting the dismissed issue: %v", err)
	}
	if got.Status != StatusDismissed {
		t.Errorf("status = %q, want %q", got.Status, StatusDismissed)
	}
	if err := s.SetStatus("7", "closed", issueAt); err == nil {
		t.Error("SetStatus accepted a status outside the four")
	}
	// `resolved` is one of the four. No local command sets it — the resolver
	// writes the shared record (the update spec's F27) — but `issue sync`
	// brings one home, and the local store reads and writes what the shared
	// store holds.
	if err := s.SetStatus("7", StatusResolved, issueAt); err != nil {
		t.Errorf("SetStatus refused resolved: %v", err)
	}
}

// TestSavedIssueCarriesInstallation covers F8b: the store stamps the project
// its issues belong to — the digest of the runs bucket URL — on every record
// it saves, so a machine that serves two projects never files one project's
// issue into the other's list.
func TestSavedIssueCarriesInstallation(t *testing.T) {
	_, env := issueState(t)
	s := issueStore(t, env)
	s.Installation = issueInstallation
	local := issueLocal()
	local.Installation = ""
	if err := s.Save(local); err != nil {
		t.Fatalf("saving: %v", err)
	}
	got, err := s.Get(local.LocalID)
	if err != nil {
		t.Fatalf("getting the saved issue: %v", err)
	}
	if got.Installation != issueInstallation {
		t.Errorf("the saved record names the installation %q, want %q", got.Installation, issueInstallation)
	}
	// A store with no project to name stamps nothing rather than clearing the
	// field: the record that came home from the shared store through `issue
	// sync` was filed in an installation, and a store that does not know its
	// own is not the one that unsays it.
	plain := issueStore(t, env)
	if err := plain.Save(got); err != nil {
		t.Fatalf("saving with no installation: %v", err)
	}
	if again, err := plain.Get(local.LocalID); err != nil {
		t.Fatalf("getting the record back: %v", err)
	} else if again.Installation != issueInstallation {
		t.Errorf("a store with no installation rewrote it as %q", again.Installation)
	}

	// An issue that already belongs to an installation keeps it, whatever store
	// writes it next: `SetStatus` and `MarkUploaded` rewrite a record through
	// Save, so a machine working in one project that dismissed or uploaded
	// another project's issue would otherwise hand the issue over — and the
	// listing and the `sync` of the next task filter on exactly this field, so
	// the wrong value here is the wrong project's issue (F8b).
	const otherInstallation = "0011223344556677"
	bStore := issueStore(t, env)
	bStore.Installation = otherInstallation
	if err := bStore.SetStatus(local.LocalID, StatusDismissed, issueAt); err != nil {
		t.Fatalf("dismissing through another store: %v", err)
	}
	if filed, err := bStore.Get(local.LocalID); err != nil {
		t.Fatalf("getting the dismissed issue back: %v", err)
	} else if filed.Installation != issueInstallation {
		t.Errorf("dismissing through another project's store relabelled the issue %q", filed.Installation)
	}
	if err := bStore.Save(got); err != nil {
		t.Fatalf("re-saving through another store: %v", err)
	}
	if again, err := bStore.Get(local.LocalID); err != nil {
		t.Fatalf("getting the record back: %v", err)
	} else if again.Installation != issueInstallation {
		t.Errorf("another project's store rewrote the issue as %q", again.Installation)
	}
	// A record that names none is the one this store's own installation is for:
	// the issue this project's command just saved.
	unclaimed := issueLocal()
	unclaimed.Run, unclaimed.LocalID = "update-20261109120000-5150", "local-update-20261109120000-5150"
	unclaimed.Installation = ""
	if err := bStore.Save(unclaimed); err != nil {
		t.Fatalf("saving a record with no installation: %v", err)
	}
	if claimed, err := bStore.Get(unclaimed.LocalID); err != nil {
		t.Fatalf("getting the second issue back: %v", err)
	} else if claimed.Installation != otherInstallation {
		t.Errorf("a record with no installation was saved as %q, want %q", claimed.Installation, otherInstallation)
	}

	// A record saved before there was a field to stamp is a record no project
	// claims, and reading it leaves it that way: the field is one the reader
	// supplies as empty, not one an old file is rewritten to grow.
	old := issueStore(t, env)
	old.Installation = issueInstallation
	data, err := Marshal(issueLocal())
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	head, body := issueHeaderAndBody(t, data)
	path := filepath.Join(old.Dir(), "local-"+issueLocal().Run+".md")
	stripped := []byte(withoutHeaderLine(t, head, "installation") + "\n---\n" + body)
	if err := os.WriteFile(path, stripped, 0o600); err != nil {
		t.Fatalf("seeding a record from before the field: %v", err)
	}
	ancient, err := old.Get(issueLocal().LocalID)
	if err != nil {
		t.Fatalf("reading an issue saved before the field: %v", err)
	}
	if ancient.Installation != "" {
		t.Errorf("a record with no installation read back as %q, want empty", ancient.Installation)
	}
	if b, err := os.ReadFile(path); err != nil {
		t.Fatalf("reading the old record back: %v", err)
	} else if !bytes.Equal(b, stripped) {
		t.Error("reading an issue saved before the field rewrote its file")
	}

	// The installation is part of the record, so a value that is not a digest
	// is refused by the record's own rules and nothing is written: a store that
	// built the field from something else — a URL, a path — would save a record
	// whose reader, in another installation, hides the issue as another
	// project's. The record saved here is one that names none, which is the one
	// this store's own value goes on.
	bad := issueStore(t, env)
	bad.Installation = "not a digest"
	unnamed := issueSample()
	unnamed.Installation = ""
	err = bad.Save(unnamed)
	if err == nil {
		t.Fatal("Save wrote an installation that is not a digest")
	}
	if !strings.Contains(err.Error(), "installation") {
		t.Errorf("Save refused it for another reason: %v", err)
	}
	entries, err := os.ReadDir(bad.Dir())
	if err != nil {
		t.Fatalf("reading the store directory: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "0007-") {
			t.Errorf("Save wrote %s although the record was refused", e.Name())
		}
	}
}

// TestLocalDismissStampsDismissedAt covers this machine's half of D1781: the
// write that sets an issue aside is the one that stamps when, from the clock
// the caller passes, and the date it writes is UTC to the second. The date a
// record carries is when the issue was set aside — the retirement of a
// finished issue reads it (F4) — so a later write never moves it.
func TestLocalDismissStampsDismissedAt(t *testing.T) {
	_, env := issueState(t)
	s := issueStore(t, env)
	if err := s.Save(issueLocal()); err != nil {
		t.Fatalf("saving: %v", err)
	}
	// The clock is in another zone and carries sub-second precision, which a
	// record's field does not: both are the store's to settle, so a caller may
	// pass the clock it has.
	when := issueAt.In(time.FixedZone("EET", 2*60*60)).Add(1234 * time.Millisecond)
	want := issueAt.Add(time.Second)
	if err := s.SetStatus(issueLocal().LocalID, StatusDismissed, when); err != nil {
		t.Fatalf("dismissing: %v", err)
	}
	got, err := s.Get(issueLocal().LocalID)
	if err != nil {
		t.Fatalf("getting the dismissed issue: %v", err)
	}
	if got.DismissedAt == nil {
		t.Fatal("dismissing wrote no date")
	}
	if !got.DismissedAt.Equal(want) {
		t.Errorf("the dismiss date is %v, want %v", got.DismissedAt, want)
	}
	if got.DismissedAt.Location() != time.UTC {
		t.Errorf("the dismiss date is in %v, want UTC", got.DismissedAt.Location())
	}
	// Dismissing again keeps the first date: the field says when the issue was
	// set aside, and the second run is the same fact observed twice.
	when = issueAt.Add(72 * time.Hour)
	if err := s.SetStatus(issueLocal().LocalID, StatusDismissed, when); err != nil {
		t.Fatalf("dismissing twice: %v", err)
	}
	if again, err := s.Get(issueLocal().LocalID); err != nil {
		t.Fatalf("getting the issue back: %v", err)
	} else if !again.DismissedAt.Equal(want) {
		t.Errorf("a second dismiss moved the date to %v, want %v", again.DismissedAt, want)
	}

	// No other status stamps it: `resolved` is a transition of its own, and the
	// date it carries is the resolver's, written on the shared record and
	// brought home by `issue sync` — a machine that merely marks its own copy
	// resolved has not been told when that happened, and a date it made up
	// there would be the day it synced.
	other := issueStore(t, env)
	resolved := issueLocal()
	resolved.Run, resolved.LocalID = "update-20261109120000-5150", "local-update-20261109120000-5150"
	if err := other.Save(resolved); err != nil {
		t.Fatalf("saving the second issue: %v", err)
	}
	if err := other.SetStatus(resolved.LocalID, StatusResolved, issueAt); err != nil {
		t.Fatalf("setting the status: %v", err)
	}
	got, err = other.Get(resolved.LocalID)
	if err != nil {
		t.Fatalf("getting the issue back: %v", err)
	}
	if got.DismissedAt != nil {
		t.Errorf("marking an issue resolved stamped a dismiss date: %v", got.DismissedAt)
	}
}

func TestStoreListsSavedIssues(t *testing.T) {
	_, env := issueState(t)
	s := issueStore(t, env)
	if err := s.Save(issueLocal()); err != nil {
		t.Fatalf("saving the local issue: %v", err)
	}
	if err := s.Save(issueSample()); err != nil {
		t.Fatalf("saving the uploaded issue: %v", err)
	}
	// A file that is not one of ours is left alone rather than parsed.
	if err := os.WriteFile(filepath.Join(s.Dir(), "notes.md"), []byte("not a record\n"), 0o600); err != nil {
		t.Fatalf("seeding a foreign file: %v", err)
	}
	// So is one of ours that cannot be read: a hand-edit or a file from a
	// newer version must not take list, show and dismiss down with it, and
	// there is no command that deletes one. The file stays where it is.
	junk := filepath.Join(s.Dir(), "0009-update-20261107183012-48213.md")
	if err := os.WriteFile(junk, []byte("---\nsurprise: 1\n---\n\n## Summary\n\nx\n"), 0o600); err != nil {
		t.Fatalf("seeding an unreadable record: %v", err)
	}
	// A name this package does not write is not read even when it holds a whole
	// record: a temporary file a crash left between the write and the rename,
	// and an editor's backup of an issue file. Both would otherwise list the
	// same issue twice, and — the copy sort.Slice happens to leave first — make
	// Get return the stale one. Both names sort before the record they copy.
	stale := issueSample()
	stale.Status = StatusDismissed
	stale.URL = "https://github.com/dimipaun/fugaro/issues/999"
	staleRecord, err := Marshal(stale)
	if err != nil {
		t.Fatalf("marshalling the stale record: %v", err)
	}
	for _, name := range []string{".issue-1234567890.tmp", "0007-update-20261107183012-48213.md~"} {
		if err := os.WriteFile(filepath.Join(s.Dir(), name), staleRecord, 0o600); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d issues, want 2", len(got))
	}
	if got[0].Number != 0 || got[1].Number != 7 {
		t.Errorf("List is not ordered by number: %d, %d", got[0].Number, got[1].Number)
	}
	if got[1].Status != StatusPending {
		t.Errorf("List read a copy under a name this package does not write: %v", got[1].Status)
	}
	seven, err := s.Get("7")
	if err != nil {
		t.Errorf("an unreadable record beside it broke Get: %v", err)
	} else if seven.Status != StatusPending {
		t.Errorf("Get(7) returned the stale copy: status %q, want %q", seven.Status, StatusPending)
	}
	if _, err := os.Stat(junk); err != nil {
		t.Errorf("the unreadable record was removed rather than skipped: %v", err)
	}
}

func TestStoreRefusesTraversalStem(t *testing.T) {
	// F20: the file name is built from shared data, so the number and the stem
	// are checked by shape before any path is built. The directory a `../`
	// stem would reach is made first, so a store that built the name anyway
	// would write there and this test would find it.
	state, env := issueState(t)
	s := issueStore(t, env)
	if err := os.MkdirAll(filepath.Join(state, "fugaro"), 0o700); err != nil {
		t.Fatalf("making the directory a traversal would reach: %v", err)
	}
	bad := issueLocal()
	bad.Run = "../../../escape"
	bad.LocalID = "local-" + bad.Run
	err := s.Save(bad)
	if err == nil {
		t.Fatal("Save wrote an issue whose run stem is a path")
	}
	if !strings.Contains(err.Error(), "run stem") {
		t.Errorf("Save refused the stem for another reason: %v", err)
	}
	// The local ID is the other half of the name, and before the issue has a
	// number it is the whole of it: the run stem here is a real one, so only
	// the check that the local ID names it keeps Save from building a path out
	// of the header. The walk below is what would find the file it wrote.
	escape := issueLocal()
	escape.LocalID = "../../escape"
	err = s.Save(escape)
	if err == nil {
		// Not fatal: the walk below is what names the file this wrote.
		t.Error("Save wrote an issue whose local ID is a path")
	} else if !strings.Contains(err.Error(), "local ID") {
		t.Errorf("Save refused the local ID for another reason: %v", err)
	}
	err = filepath.WalkDir(state, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(state, path)
		if relErr != nil {
			return relErr
		}
		if !strings.HasPrefix(rel, filepath.Join("fugaro", "issues")) {
			t.Errorf("Save built a path outside the store: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the state directory: %v", err)
	}
	// A reference the user types is checked the same way, so the store never
	// looks for a file outside itself.
	for _, c := range []struct{ ref, want string }{
		{"local-../../../escape", "run stem"},
		{"local-../../etc/passwd", "run stem"},
		{"../../etc/passwd", "neither an issue number nor a local ID"},
		{"7/../..", "neither an issue number nor a local ID"},
		{"0", "not an issue number"},
		{"9999999", "not an issue number"},
	} {
		_, err := s.Get(c.ref)
		if err == nil {
			t.Errorf("Get(%q) resolved a name the store does not hold", c.ref)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Get(%q) refused for another reason: %v", c.ref, err)
		}
	}
}
