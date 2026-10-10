package watch

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAcksSurviveABadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "watch-acks.json")
	os.WriteFile(p, []byte("{not json"), 0o600)
	a := LoadAcks(p)
	if a.Has("a", "r") {
		t.Fatal("bad file acked something")
	}
	if err := a.Ack("a", "r", time.Now()); err != nil || !LoadAcks(p).Has("a", "r") {
		t.Fatalf("ack did not persist: %v", err)
	}
}

// A missing file is the same as a bad one: an empty set, never an error from
// LoadAcks (the caller has no good way to surface one before the screen even
// opens).
func TestAcksMissingFileIsEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "watch-acks.json")
	a := LoadAcks(p)
	if a.Has("a", "r") {
		t.Fatal("a missing file acked something")
	}
}

// An ack older than 7 days is pruned the next time anything is written, so
// the file does not grow forever.
func TestAcksPruneOldEntriesOnWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "watch-acks.json")
	a := LoadAcks(p)
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := a.Ack("a", "old", old); err != nil {
		t.Fatal(err)
	}
	if err := a.Ack("a", "new", time.Now()); err != nil {
		t.Fatal(err)
	}
	reloaded := LoadAcks(p)
	if reloaded.Has("a", "old") {
		t.Fatal("an ack older than 7 days was not pruned")
	}
	if !reloaded.Has("a", "new") {
		t.Fatal("a fresh ack was pruned too")
	}
}

// JSON null is valid JSON that unmarshals into a nil map with no error from
// encoding/json; before the fix, LoadAcks took that nil map as a.at, and the
// next Ack panicked ("assignment to entry in nil map") the first time a
// viewer pressed x against a watch-acks.json holding exactly "null" (an
// empty acks file an earlier, buggy write could plausibly have produced).
// Every other malformed shape here already errored out of json.Unmarshal
// before this fix; they are pinned alongside null so a future change to the
// target type cannot quietly reopen one of them.
func TestAcksSurviveMalformedShapes(t *testing.T) {
	for _, c := range []struct {
		name, content string
	}{
		{"null", `null`},
		{"array", `[]`},
		{"string", `"x"`},
		{"bad_time_value", `{"a": "not a time"}`},
		{"truncated", `{"a/r":"2026-10-0`},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "watch-acks.json")
			if err := os.WriteFile(p, []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			a := LoadAcks(p)
			if a.Has("a", "r") {
				t.Fatal("a malformed file acked something")
			}
			// The regression: Ack must not panic on a map LoadAcks left nil.
			if err := a.Ack("a", "r", time.Now()); err != nil {
				t.Fatalf("ack after a malformed file: %v", err)
			}
			if !LoadAcks(p).Has("a", "r") {
				t.Fatal("the ack after a malformed file did not persist")
			}
		})
	}
}

// A file bigger than LoadAcks trusts is read as empty, not read whole: a
// watch-acks.json is a handful of small entries, so anything past the cap
// is not something `fugaro watch` should load into memory on every launch.
// A file over ackMaxBytes is never read, not even partially: the content
// here is otherwise perfectly valid JSON (an acknowledgement padded with
// whitespace past the cap), so if LoadAcks read and parsed it despite the
// size, Has would wrongly report it. Only the size check can be catching
// this: a truncated read of this same content would still parse (JSON
// tolerates trailing whitespace before the final brace being cut off would
// not, but a read short by even one byte already breaks the object), so a
// weakened byte limit, not just a missing one, would also be caught.
func TestAcksOversizedFileIsEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "watch-acks.json")
	entry := `{"a/r":"` + time.Now().UTC().Format(time.RFC3339) + `"`
	pad := ackMaxBytes + 1 - len(entry) - 1 // leave room for the closing brace
	if pad < 0 {
		t.Fatalf("ackMaxBytes too small for this test's entry (%d bytes)", len(entry)+1)
	}
	content := entry + strings.Repeat(" ", pad) + "}"
	if len(content) <= ackMaxBytes {
		t.Fatalf("test content is %d bytes, want more than ackMaxBytes (%d)", len(content), ackMaxBytes)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a := LoadAcks(p)
	if a.Has("a", "r") {
		t.Fatal("an oversized file was read and parsed; LoadAcks must cap the read before unmarshalling")
	}
}

// The ack file and its directory are private: the file mode 0600, the
// directory 0700, whatever the process umask is (Ack sets both explicitly
// rather than relying on the create call's own default).
func TestAckFileAndDirAreModeChecked(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	dir := filepath.Join(t.TempDir(), "state", "fugaro")
	p := filepath.Join(dir, "watch-acks.json")
	a := LoadAcks(p)
	if err := a.Ack("a", "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o, want 0700", di.Mode().Perm())
	}
}

func TestAckPathDefaultsUnderXDGStateHome(t *testing.T) {
	env := map[string]string{"XDG_STATE_HOME": "/tmp/xdg-state"}
	get := func(k string) string { return env[k] }
	want := filepath.Join("/tmp/xdg-state", "fugaro", "watch-acks.json")
	if got := AckPath(get); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
