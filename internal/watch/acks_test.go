package watch

import (
	"os"
	"path/filepath"
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

func TestAckPathDefaultsUnderXDGStateHome(t *testing.T) {
	env := map[string]string{"XDG_STATE_HOME": "/tmp/xdg-state"}
	get := func(k string) string { return env[k] }
	want := filepath.Join("/tmp/xdg-state", "fugaro", "watch-acks.json")
	if got := AckPath(get); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
