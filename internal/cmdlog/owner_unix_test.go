//go:build unix

package cmdlog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// What is pinned here is the check the writer and internal/srctree both make
// before they touch the same kind of path, and it cannot be asserted anywhere
// else: the ownership rule reads a uid out of syscall.Stat_t, which Windows has
// no field for — owner_other.go says what that half does instead of refusing —
// and a link cannot be made at all there. The rest of the package's tests are
// platform-neutral and live in cmdlog_test.go.

// TestAStoreAnotherUserOwnsIsNoLog: XDG_STATE_HOME is attacker-influenced and
// the store's names are predictable, so a directory another account owns would
// decide which run Latest answers and what Open hands the evidence collector.
// The log is really there and really this run's, and it is still not read: the
// answer is ErrNoLog, which is what a caller can act on, rather than another
// account's file.
func TestAStoreAnotherUserOwnsIsNoLog(t *testing.T) {
	getenv := env(t)
	writeLog(t, getenv, "update", logAt, "Failed in 1.2s, 2 steps\n")

	was := euid
	euid = func() int { return was() + 1 }
	t.Cleanup(func() { euid = was })

	if id, err := Latest(getenv, false); !errors.Is(err, ErrNoLog) {
		t.Errorf("Latest = %+v, %v, want ErrNoLog: a store another user owns decides nothing here", id, err)
	}
	if id, err := Latest(getenv, true); !errors.Is(err, ErrNoLog) {
		t.Errorf("Latest(failedOnly) = %+v, %v, want ErrNoLog", id, err)
	}
	if _, err := Open(getenv, ID{Command: "update", At: logAt, PID: os.Getpid()}); !errors.Is(err, ErrNoLog) {
		t.Errorf("Open = %v, want ErrNoLog", err)
	}
}

// TestALinkAtTheStoreIsNotReadThrough: the store is read through the same
// refusal the writer makes before it writes. The link stays inside the state
// directory — the target is relative, `elsewhere`, and not an absolute path:
// os.Root refuses a link out of its root on its own, so a link it will follow
// is the one this check has to refuse, and a test with an absolute target would
// pass with the check deleted. A log at the name such a link points to would be
// read — which is the point: a link at `<state>/fugaro` decides where the store
// is, and a reader that follows it answers with a file the run it is diagnosing
// never wrote.
func TestALinkAtTheStoreIsNotReadThrough(t *testing.T) {
	getenv := env(t)
	base, err := storeBase(getenv)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(base, "elsewhere")
	if err := os.MkdirAll(filepath.Join(elsewhere, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	name := "update-" + stamp(logAt) + "-48213" + logSuffix
	if err := os.WriteFile(filepath.Join(elsewhere, "logs", name), []byte("Failed in 1.2s, 2 steps\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere", filepath.Join(base, "fugaro")); err != nil {
		t.Fatal(err)
	}

	if id, err := Latest(getenv, true); !errors.Is(err, ErrNoLog) {
		t.Errorf("Latest = %+v, %v, want ErrNoLog: the store is where the run put it, not where a link points", id, err)
	}
	if _, err := Open(getenv, ID{Command: "update", At: logAt, PID: 48213}); !errors.Is(err, ErrNoLog) {
		t.Errorf("Open = %v, want ErrNoLog: a link at the store is not read through", err)
	}
}
