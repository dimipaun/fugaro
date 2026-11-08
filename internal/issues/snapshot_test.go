package issues

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	snapInstall      = "0123456789abcdef"
	snapOtherInstall = "fedcba9876543210"
)

// snapTime parses a fixed RFC 3339 instant, so an expected time and a decoded
// one are the same value struct for struct and a round trip can compare them
// with DeepEqual.
func snapTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad test time %q: %v", s, err)
	}
	return parsed
}

func snapPtr(t *testing.T, s string) *time.Time {
	t.Helper()
	at := snapTime(t, s)
	return &at
}

// snapRows is a filled shared row and a filled local one: between them every
// field of the type is set at least once.
func snapRows(t *testing.T) []SnapshotRow {
	t.Helper()
	return []SnapshotRow{
		{
			Number:      "0007",
			Summary:     "fugaro run failed to bootstrap",
			Display:     "resolved",
			HitAt:       snapPtr(t, "2026-11-06T12:00:00Z"),
			SubmittedAt: snapPtr(t, "2026-11-07T12:00:00Z"),
			ResolvedAt:  snapPtr(t, "2026-11-08T10:00:00Z"),
			URL:         "https://github.com/dimipaun/fugaro/issues/7",
			ResolvedIn:  "0.22.1",
			Coverage:    "behind",
			OthersHit:   2,
		},
		{
			LocalID:  "local-2f9a1c",
			Summary:  "terraform apply was refused",
			Display:  "local",
			HitAt:    snapPtr(t, "2026-11-08T11:00:00Z"),
			Coverage: "none",
		},
	}
}

// snapWriteRaw puts body exactly where ReadSnapshot looks, standing in for a
// file written by another version, by hand, or half way through a crash.
func snapWriteRaw(t *testing.T, dir, installation, body string) {
	t.Helper()
	path, err := snapshotPath(dir, installation)
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write raw snapshot: %v", err)
	}
}

// snapRequireNoSnapshot is the rule that no file on disk, however broken, is
// ever an error the caller (offline `mine`) has to handle.
func snapRequireNoSnapshot(t *testing.T, dir, installation string) {
	t.Helper()
	got, err := ReadSnapshot(dir, installation)
	if err != nil {
		t.Fatalf("a bad snapshot must read as no snapshot, not an error: %v", err)
	}
	if got.Format != 0 || !got.AsOf.IsZero() || got.Rows != nil {
		t.Errorf("a bad snapshot must read as the zero Snapshot, got %+v", got)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := snapTime(t, "2026-11-08T12:00:00Z")
	rows := snapRows(t)

	if err := WriteSnapshot(dir, snapInstall, rows, now); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	path, err := snapshotPath(dir, snapInstall)
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the snapshot file: %v", err)
	}
	want := `{"format":1,"as_of":"2026-11-08T12:00:00Z","rows":[` +
		`{"number":"0007","summary":"fugaro run failed to bootstrap","display":"resolved",` +
		`"hit_at":"2026-11-06T12:00:00Z","submitted_at":"2026-11-07T12:00:00Z",` +
		`"resolved_at":"2026-11-08T10:00:00Z",` +
		`"url":"https://github.com/dimipaun/fugaro/issues/7","resolved_in":"0.22.1",` +
		`"coverage":"behind","others_hit":2},` +
		`{"local_id":"local-2f9a1c","summary":"terraform apply was refused","display":"local",` +
		`"hit_at":"2026-11-08T11:00:00Z","coverage":"none"}]}`
	if string(data) != want {
		t.Errorf("the snapshot file is not the pinned shape:\n got %s\nwant %s", data, want)
	}

	got, err := ReadSnapshot(dir, snapInstall)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if got.Format != 1 {
		t.Errorf("Format = %d, want 1", got.Format)
	}
	if !got.AsOf.Equal(now) {
		t.Errorf("AsOf = %v, want %v", got.AsOf, now)
	}
	if !reflect.DeepEqual(got.Rows, rows) {
		t.Errorf("rows did not survive the round trip:\n got %+v\nwant %+v", got.Rows, rows)
	}
}

func TestSnapshotRejectsUnknownFieldAndFormat(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "a field inside a row",
			body: `{"format":1,"as_of":"2026-11-08T12:00:00Z","rows":[` +
				`{"number":"0007","summary":"s","display":"resolved","command":"fugaro run --repo ."}]}`,
		},
		{
			name: "a field on the file",
			body: `{"format":1,"as_of":"2026-11-08T12:00:00Z","rows":[],"installation":"` + snapInstall + `"}`,
		},
		{
			name: "a newer format",
			body: `{"format":2,"as_of":"2026-11-08T12:00:00Z","rows":[]}`,
		},
		{
			name: "no format at all",
			body: `{"as_of":"2026-11-08T12:00:00Z","rows":[]}`,
		},
		{
			name: "trailing bytes",
			body: `{"format":1,"as_of":"2026-11-08T12:00:00Z","rows":[]}{"format":1}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			snapWriteRaw(t, dir, snapInstall, tc.body)
			snapRequireNoSnapshot(t, dir, snapInstall)
		})
	}
}

// The file is read by a printer that has no wire contract to keep and no way
// to tell a stale row from a fresh one, so what it holds is exactly the
// display data: the mistake this refuses is storing T-IM1's row whole, whose
// command (and, if it were ever added, a reporter) would then outlive the
// shared read it came from.
func TestSnapshotHoldsNoCommandAndNoReporter(t *testing.T) {
	banned := []string{"command", "reporter", "address", "digest", "email", "fingerprint"}
	typ := reflect.TypeOf(SnapshotRow{})
	for i := range typ.NumField() {
		field := strings.ToLower(typ.Field(i).Name)
		for _, word := range banned {
			if strings.Contains(field, word) {
				t.Errorf("SnapshotRow.%s holds %q, which the snapshot must not store", typ.Field(i).Name, word)
			}
		}
	}

	dir := t.TempDir()
	if err := WriteSnapshot(dir, snapInstall, snapRows(t), snapTime(t, "2026-11-08T12:00:00Z")); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	path, err := snapshotPath(dir, snapInstall)
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the snapshot file: %v", err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decode the snapshot file: %v", err)
	}
	snapWantKeys(t, file, "format", "as_of", "rows")

	// Every field of the type at once, so the key set this pins is the whole
	// of it and not what the two rows above happened to fill in. (A row is
	// either shared or local, so a real one never carries both identities;
	// this is a shape probe, not data.)
	full, err := json.Marshal(SnapshotRow{
		Number:      "0007",
		LocalID:     "local-2f9a1c",
		Summary:     "s",
		Display:     "resolved",
		HitAt:       snapPtr(t, "2026-11-06T12:00:00Z"),
		SubmittedAt: snapPtr(t, "2026-11-07T12:00:00Z"),
		ResolvedAt:  snapPtr(t, "2026-11-08T10:00:00Z"),
		DismissedAt: snapPtr(t, "2026-11-08T11:00:00Z"),
		URL:         "https://github.com/dimipaun/fugaro/issues/7",
		ResolvedIn:  "0.22.1",
		Coverage:    "behind",
		OthersHit:   2,
	})
	if err != nil {
		t.Fatalf("marshal a full row: %v", err)
	}
	var row map[string]json.RawMessage
	if err := json.Unmarshal(full, &row); err != nil {
		t.Fatalf("decode a full row: %v", err)
	}
	snapWantKeys(t, row, "number", "local_id", "summary", "display", "hit_at",
		"submitted_at", "resolved_at", "dismissed_at", "url", "resolved_in",
		"coverage", "others_hit")
}

// snapWantKeys is the whole key set, not a subset: a field added to the type
// reaches the file only by being named here, which is what makes this the pin
// on "display data only".
func snapWantKeys(t *testing.T, obj map[string]json.RawMessage, want ...string) {
	t.Helper()
	for _, k := range want {
		if _, ok := obj[k]; !ok {
			t.Errorf("the file has no %q key", k)
		}
	}
	for k := range obj {
		if !slices.Contains(want, k) {
			t.Errorf("the file holds %q, which is not display data", k)
		}
	}
}

func TestSnapshotIsPrivate(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSnapshot(dir, snapInstall, snapRows(t), snapTime(t, "2026-11-08T12:00:00Z")); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	path, err := snapshotPath(dir, snapInstall)
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat the snapshot: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the snapshot is mode %o, want 600: it holds another person's text and this user's rows", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat the snapshot directory: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("the snapshot directory is mode %o, want 700", perm)
	}
}

func TestSnapshotWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	first := snapRows(t)[:1]
	if err := WriteSnapshot(dir, snapInstall, first, snapTime(t, "2026-11-08T12:00:00Z")); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	path, err := snapshotPath(dir, snapInstall)
	if err != nil {
		t.Fatalf("snapshotPath: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("loosen the snapshot: %v", err)
	}

	now := snapTime(t, "2026-11-08T13:00:00Z")
	second := snapRows(t)
	if err := WriteSnapshot(dir, snapInstall, second, now); err != nil {
		t.Fatalf("second WriteSnapshot: %v", err)
	}

	// A write that opened the file and truncated it would have kept the loose
	// mode it found; one that renames a fresh 0600 file over it does not.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat the snapshot: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the rewritten snapshot is mode %o, want 600: the write replaced the file, it did not edit it", perm)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read the snapshot directory: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the snapshot directory holds %v, want the one file: a temporary file was left behind", names)
	}

	got, err := ReadSnapshot(dir, snapInstall)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if !got.AsOf.Equal(now) || !reflect.DeepEqual(got.Rows, second) {
		t.Errorf("the file is not the second write: %+v", got)
	}
}

func TestSnapshotCorruptIsNoSnapshot(t *testing.T) {
	const good = `{"format":1,"as_of":"2026-11-08T12:00:00Z","rows":[]}`
	cases := []struct {
		name string
		body string
	}{
		{"an empty file", ""},
		{"not json", "not json at all\n"},
		{"truncated", good[:len(good)/2]},
		{"an as_of that is not a time", `{"format":1,"as_of":"yesterday","rows":[]}`},
		{"a row whose hit_at is not a time", `{"format":1,"as_of":"2026-11-08T12:00:00Z",` +
			`"rows":[{"summary":"s","display":"local","hit_at":"2026-11-06"}]}`},
		{"rows of the wrong type", `{"format":1,"as_of":"2026-11-08T12:00:00Z","rows":{}}`},
		{"json is not an object", `[1,2,3]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			snapWriteRaw(t, dir, snapInstall, tc.body)
			snapRequireNoSnapshot(t, dir, snapInstall)
		})
	}

	t.Run("a snapshot that was never written", func(t *testing.T) {
		snapRequireNoSnapshot(t, t.TempDir(), snapInstall)
	})

	t.Run("a directory where the file belongs", func(t *testing.T) {
		dir := t.TempDir()
		path, err := snapshotPath(dir, snapInstall)
		if err != nil {
			t.Fatalf("snapshotPath: %v", err)
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		snapRequireNoSnapshot(t, dir, snapInstall)
	})
}

func TestSnapshotIsPerInstallation(t *testing.T) {
	dir := t.TempDir()
	now := snapTime(t, "2026-11-08T12:00:00Z")
	mine := snapRows(t)[:1]
	theirs := []SnapshotRow{{Number: "0042", Summary: "another project's issue", Display: "pending"}}

	if err := WriteSnapshot(dir, snapInstall, mine, now); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	if err := WriteSnapshot(dir, snapOtherInstall, theirs, now); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	got, err := ReadSnapshot(dir, snapInstall)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if !reflect.DeepEqual(got.Rows, mine) {
		t.Errorf("this installation's rows are %+v, want %+v", got.Rows, mine)
	}
	other, err := ReadSnapshot(dir, snapOtherInstall)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if !reflect.DeepEqual(other.Rows, theirs) {
		t.Errorf("the other installation's rows are %+v, want %+v", other.Rows, theirs)
	}
	snapRequireNoSnapshot(t, dir, "0011223344556677")

	// One file per installation means the name is joined into a path, so a
	// name that is not a digest must be refused before it can reach one.
	for _, bad := range []string{"", "../escape", "..", snapInstall + "/../x", "not a digest", "0.1.2"} {
		if _, err := ReadSnapshot(dir, bad); err == nil {
			t.Errorf("ReadSnapshot(%q) was accepted: a name that is not a digest must not build a path", bad)
		}
		if err := WriteSnapshot(dir, bad, mine, now); err == nil {
			t.Errorf("WriteSnapshot(%q) was accepted: a name that is not a digest must not build a path", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.json")); !os.IsNotExist(err) {
		t.Errorf("../escape reached the file system: %v", err)
	}
}

// Whatever the file holds is data: the link and the release are shape-checked
// by the printer that puts them on a terminal, never by the store that
// carries them here, so a hostile row survives the round trip untouched.
func TestSnapshotHostileContentIsStillPrintedAsData(t *testing.T) {
	dir := t.TempDir()
	rows := []SnapshotRow{{
		Number:     "0007",
		Summary:    "\x1b[2Jrun \x07fugaro update",
		Display:    "resolved",
		URL:        "javascript:alert(1)",
		ResolvedIn: "../../etc/passwd",
		Coverage:   "behind",
	}}
	if err := WriteSnapshot(dir, snapInstall, rows, snapTime(t, "2026-11-08T12:00:00Z")); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	got, err := ReadSnapshot(dir, snapInstall)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if !reflect.DeepEqual(got.Rows, rows) {
		t.Errorf("a hostile row was changed on the way through:\n got %+v\nwant %+v", got.Rows, rows)
	}
}
