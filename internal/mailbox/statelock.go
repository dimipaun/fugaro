package mailbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// This machine's mailbox state has **one writer at a time**, and this file is
// how the two commands that could be it agree on that.
//
// §6 of the plan names the consumer as the writer of `ledger.json` and
// `pending.json`, and it opens both **once** and keeps what it read in memory
// for the life of its process. The sweeper's local half (T7, M8) writes the same
// two files out of a state it read at its own start, so a record the consumer
// accepts and commits in between — or an id it spends — would be absent from the
// sweeper's copy and **dropped from disk by the sweeper's commit**. It comes
// back on the consumer's next commit; a process killed before then leaves an
// accepted delivery unrecorded, and the restart re-reads the never-deleted inbox
// node and runs the action a second time, which is the at-most-once property the
// ledger exists for. The sweep is therefore not a second writer: it takes this
// lock, and it is refused — with the process to wait for — when somebody else
// holds it.
//
// **Two kinds of holder, one lock.** The consumer takes a **shared** lock and
// holds it for the life of its process: two consumers on one machine are already
// a pair, and the store's lease — not a file here — is what fences them from
// each other (M1, `TestTwoConsumersOneLease`), so refusing the second one here
// would replace the fence that check 83's step 7 records with a local one that
// says nothing about a second machine. The sweep takes an **exclusive** lock,
// which no shared holder can coexist with: a sweep met by a consumer, and a
// consumer met by a sweep's local half, is refused rather than run.
//
// The lock is the kernel's, on the open file (flock, or LockFileEx on Windows —
// see statelock_flock.go and statelock_windows.go): it is released when the
// process ends however it ends, a kill or a power loss included, so nothing here
// leaves a record a later run has to judge stale. What the file *says* — the
// holder's name and its pid — is a courtesy for the refusal's wording and is
// never what decides anything.

// StateLockFile is the name of the file the two writers lock, beside the ledger
// and the prompt memory in `$XDG_STATE_HOME/fugaro/mailbox/<project>/`. It is
// **not state**: nothing reads it for a decision, a sweep that finds nothing to
// let go of never creates it, and a machine whose consumer has ever run has one.
const StateLockFile = "state.lock"

// StateLockPath is project's state lock: beside the ledger, whose directory M7
// names.
func StateLockPath(project string) (string, error) {
	ledger, err := LedgerPath(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(ledger), StateLockFile), nil
}

// StateLockOptions configure a state lock. Project and Holder are the project
// the state belongs to and the command that is taking the lock, as the user
// would type it ("fugaro mailbox", "fugaro mailbox sweep"): the holder is what
// a refused command prints.
type StateLockOptions struct {
	Project string
	Holder  string
	// Shared is the consumer's half of the rule: many shared holders coexist
	// (two consumers are the lease's business, not this file's), and an
	// exclusive holder — the sweeper — coexists with none of them.
	Shared bool
}

// StateBusyError is the state lock held by another process: what the holder's
// own lock file says about it, which is all a refused command can know. Holder
// is the name the holder took the lock under, and PID its process, or 0 when the
// file named none.
type StateBusyError struct {
	Project string
	Holder  string
	PID     int
}

// Error is the refusal's one line. It names the process that is running and the
// project, and then the fix: this machine's state has one writer at a time, so
// waiting for that process — or stopping it, when it is the consumer — and
// rerunning is what makes a sweep or a consumer possible again.
func (e *StateBusyError) Error() string {
	who := "another fugaro mailbox"
	if e.Holder != "" {
		who = "another " + e.Holder
	}
	pid := ""
	if e.PID > 0 {
		pid = fmt.Sprintf(" (pid %d)", e.PID)
	}
	return fmt.Sprintf("%s is running on this machine for project %s%s: this machine's mailbox state has one writer at a time, so wait for it to finish — or stop it — and rerun", who, e.Project, pid)
}

// errStateLockHeld is what a platform's non-blocking lock reports when the lock
// is somebody else's. It is unexported: the caller's error is the
// *StateBusyError it is turned into.
var errStateLockHeld = errors.New("the mailbox state lock is held by another process")

// StateLock is a held lock over one project's mailbox state. It is released by
// Release, and by the kernel when the process ends however it ends.
type StateLock struct {
	f    *os.File
	path string
}

// LockState takes project's state lock for holder, or reports whoever holds it
// as a *StateBusyError. It never waits: a sweep met by a running consumer is not
// a queue to join, it is a command whose whole local half must not run.
func LockState(o StateLockOptions) (*StateLock, error) {
	path, err := StateLockPath(o.Project)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mailbox: making the state lock's directory %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mailbox: the state lock %s: %w", path, err)
	}
	if err := lockStateFile(f, o.Shared); err != nil {
		defer f.Close()
		if errors.Is(err, errStateLockHeld) {
			held, pid := readStateLockHolder(f)
			return nil, &StateBusyError{Project: o.Project, Holder: held, PID: pid}
		}
		return nil, fmt.Errorf("mailbox: the state lock %s: %w", path, err)
	}
	// What the refusal reads: the lock itself is the kernel's, and this line is
	// a courtesy for the person who is about to be told which process to wait
	// for. A write that fails leaves the lock held and the file saying nothing,
	// which is why it is not the caller's failure — the alternative is a
	// consumer that refuses to run because it could not say who it was.
	body := o.Holder + " " + strconv.Itoa(os.Getpid()) + "\n"
	_, _ = f.Seek(0, io.SeekStart)
	if _, err := f.WriteString(body); err == nil {
		_ = f.Truncate(int64(len(body)))
	}
	return &StateLock{f: f, path: path}, nil
}

// StateLockHeld reports whether another process holds project's state lock right
// now, and what its lock file says about it — **without creating the file and
// without writing it**. That is what a `--dry-run` asks: a plan writes nothing,
// so it may not take a lock, but a reader that showed a delete set the sweep
// would refuse to perform would be a plan about a different thing.
//
// A lock file that is not there is not held: nothing can hold a lock on a file
// it never opened, and a consumer that starts a moment later is a consumer whose
// own LockState decides what happens then.
func StateLockHeld(project string) (holder string, pid int, held bool, err error) {
	path, err := StateLockPath(project)
	if err != nil {
		return "", 0, false, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("mailbox: the state lock %s: %w", path, err)
	}
	defer f.Close()
	// The probe takes the lock to learn whether it can: an exclusive request
	// fails against any holder, shared or exclusive, which is exactly the
	// question. It is given back at once, and the holder line above is not
	// written — this is not a holder, it is an answer.
	if err := lockStateFile(f, false); err != nil {
		if errors.Is(err, errStateLockHeld) {
			held, pid := readStateLockHolder(f)
			return held, pid, true, nil
		}
		return "", 0, false, fmt.Errorf("mailbox: the state lock %s: %w", path, err)
	}
	return "", 0, false, unlockStateFile(f)
}

// Path is where the lock is: the operator's own state directory, and not a
// secret.
func (l *StateLock) Path() string { return l.path }

// Release gives up the lock. It is safe to call on a lock that was never taken
// (nil), so a command can defer it beside the call that takes it.
func (l *StateLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	err := unlockStateFile(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// readStateLockHolder is what the holder's lock file says: its name and its pid,
// either of which may be missing (an empty file, a file an older fugaro wrote, a
// foreign file). Neither is trusted for anything but the refusal's wording.
func readStateLockHolder(f *os.File) (string, int) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0
	}
	b, err := io.ReadAll(io.LimitReader(f, 200))
	if err != nil {
		return "", 0
	}
	fields := strings.Fields(string(b))
	// The format ends in the holder's pid, so a body that does not is not one of
	// ours — a foreign file, a truncated write — and it says nothing rather than
	// naming whatever words happen to be in it.
	if len(fields) == 0 {
		return "", 0
	}
	pid, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || pid <= 0 {
		return "", 0
	}
	return strings.Join(fields[:len(fields)-1], " "), pid
}
