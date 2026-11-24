package mailbox

// This machine's mailbox state lock: the one rule the consumer's shared lock and
// the sweeper's exclusive one make together (§6 — the consumer is the writer of
// `ledger.json` and `pending.json`, and the sweep is not a second one).
//
// The cases are the four the rule is: two consumers hold it together (the store
// lease is what fences them, M1), an exclusive take is refused while any of them
// holds it, a shared take is refused while a sweep's exclusive is held, and the
// lock is given back when the holder releases — plus the refusal's wording, which
// names the process a person is told to stop. Every case is a real flock on a
// file in a `t.TempDir()`, which is what makes them cases at all: two open
// descriptions of one file conflict even inside one process on the platforms
// that have a lock (workdirlock_other.go's note is the same fact for the workdir
// lock), so this test is what holds the rule on Linux, macOS and Windows and
// says nothing on plan9, js, wasip1, solaris and aix, where statelock_other.go
// takes no lock at all.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lockAt isolates the state directory the lock lives in, the way every other
// test of this package's paths does.
func lockAt(t *testing.T, project string) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path, err := StateLockPath(project)
	if err != nil {
		t.Fatalf("the state lock's path: %v", err)
	}
	return path
}

func TestTheStateLockIsOneWriterAtATime(t *testing.T) {
	path := lockAt(t, "aurora")
	held, err := LockState(StateLockOptions{Project: "aurora", Holder: "fugaro mailbox", Shared: true})
	if err != nil {
		t.Fatalf("the consumer's lock: %v", err)
	}
	// The lock is beside the ledger and the memory, and it is the ledger's own
	// directory: M7's rule, one more file in it.
	if filepath.Base(path) != StateLockFile {
		t.Fatalf("the lock is %s, want a %s beside the ledger", path, StateLockFile)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the lock was taken and no file is there: %v", err)
	}

	// A second consumer: shared, and let through — two consumers on one machine
	// are the store's lease to refuse (M1), not this file's.
	second, err := LockState(StateLockOptions{Project: "aurora", Holder: "fugaro mailbox", Shared: true})
	if err != nil {
		t.Fatalf("a second consumer was refused the state lock (%v): the lease is the fence between two consumers, and refusing here would replace it", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("releasing the second consumer: %v", err)
	}

	// The sweep: exclusive, and refused while a consumer holds the state.
	_, err = LockState(StateLockOptions{Project: "aurora", Holder: "fugaro mailbox sweep"})
	var busy *StateBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("a sweep took the state lock under a running consumer (%v): it would drop a delivery the consumer has accepted and not yet committed", err)
	}
	if busy.Project != "aurora" || busy.Holder != "fugaro mailbox" || busy.PID != os.Getpid() {
		t.Fatalf("the refusal says %+v, want the consumer's own name and this process", busy)
	}
	if !strings.Contains(busy.Error(), "another fugaro mailbox is running") || !strings.Contains(busy.Error(), "one writer at a time") {
		t.Fatalf("the refusal reads %q, want the process that holds the state and the fix", busy.Error())
	}

	// And the holder gives it back: the sweep's take is the consumer's exit.
	if err := held.Release(); err != nil {
		t.Fatalf("releasing the consumer's lock: %v", err)
	}
	sweep, err := LockState(StateLockOptions{Project: "aurora", Holder: "fugaro mailbox sweep"})
	if err != nil {
		t.Fatalf("the sweep after the consumer stopped: %v", err)
	}
	// A consumer starting while the sweep is in its local half is refused in
	// turn — the short window the store half is free of and this half is not.
	if _, err := LockState(StateLockOptions{Project: "aurora", Holder: "fugaro mailbox", Shared: true}); !errors.As(err, &busy) {
		t.Fatalf("a consumer started inside a sweep's local half (%v): the sweep's set is derived from a state it read before that consumer", err)
	} else if !strings.Contains(busy.Error(), "fugaro mailbox sweep") {
		t.Fatalf("the consumer's refusal reads %q, want the sweep it met", busy.Error())
	}
	if err := sweep.Release(); err != nil {
		t.Fatalf("releasing the sweep's lock: %v", err)
	}
}

// TestTheStateLockProbeCreatesNothing is the `--dry-run` half: a plan writes
// nothing, so it may not create a lock file — but it still has to know whether
// the writer it would be refused by is there, or it would print a delete set the
// sweep never performs.
func TestTheStateLockProbeCreatesNothing(t *testing.T) {
	path := lockAt(t, "aurora")
	if _, _, held, err := StateLockHeld("aurora"); err != nil || held {
		t.Fatalf("a state nobody has written reads held=%v (%v), want not held", held, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the probe made a lock file (%v): a dry run writes nothing at all", err)
	}

	held, err := LockState(StateLockOptions{Project: "aurora", Holder: "fugaro mailbox", Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	who, pid, isHeld, err := StateLockHeld("aurora")
	if err != nil {
		t.Fatalf("the probe: %v", err)
	}
	if !isHeld || who != "fugaro mailbox" || pid != os.Getpid() {
		t.Fatalf("the probe reads held=%v %q %d, want the consumer's own line", isHeld, who, pid)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := StateLockHeld("aurora"); err != nil {
		t.Fatalf("the second probe: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("the probe rewrote the lock file: %q, want %q", after, before)
	}
	// And the probe released what it took: a sweep can still take the lock once
	// the consumer is gone.
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	lock, err := LockState(StateLockOptions{Project: "aurora", Holder: "fugaro mailbox sweep"})
	if err != nil {
		t.Fatalf("the sweep after the probe: %v", err)
	}
	_ = lock.Release()
}
