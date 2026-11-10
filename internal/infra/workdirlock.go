package infra

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WorkdirLockFile is the lock file one converge holds while it works in a
// project's terraform workdirs: a file in the directory they all live under
// (<state>/fugaro/terraform/<project> — the parent of InstallationWorkdir,
// and of FirebaseWorkdir, ManagedWorkdir and RepoWorkdir with them).
//
// It is one file for one project and not one per workdir because a single
// run uses several of them and rewrites them all: the installation's, the
// outputs workdir read from it, and the checkout's repository workdir. What
// two runs of fugaro update destroy is each other's TREE — PrepareWorkdir
// removes <workdir>/gcp and writes it again — so the exclusion has to be
// taken before the first of them is prepared and held to the end of the run.
const WorkdirLockFile = "workdir.lock"

// WorkdirLockPath is the lock file of project's terraform workdirs.
func WorkdirLockPath(getenv func(string) string, project string) (string, error) {
	dir, err := InstallationWorkdir(getenv, project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), WorkdirLockFile), nil
}

// WorkdirBusyError is another run's lock: what the holder's own lock file
// says about it, which is all a refused run can know. Holder is the name the
// holder took the lock under ("fugaro update"), and PID its process, or 0
// when the file named none.
type WorkdirBusyError struct {
	Project string
	Holder  string
	PID     int
}

// Error is the refusal's one line. It names the command that is running and
// the project, because on a machine where somebody starts a second update
// that is the whole answer: what to wait for, and that waiting — not a
// deletion of the lock file — is what fixes it.
func (e *WorkdirBusyError) Error() string {
	who := "another fugaro update"
	if e.Holder != "" {
		who = "another " + e.Holder
	}
	pid := ""
	if e.PID > 0 {
		pid = fmt.Sprintf(" (pid %d)", e.PID)
	}
	return fmt.Sprintf("%s is running on this machine for project %s%s, wait for it to finish, then rerun", who, e.Project, pid)
}

// errWorkdirLockHeld is what a platform's non-blocking lock reports when the
// lock is somebody else's. It is unexported: the caller's error is the
// *WorkdirBusyError it is turned into.
var errWorkdirLockHeld = errors.New("the workdir lock is held by another process")

// WorkdirLock is a held lock over one project's terraform workdirs. It is
// released by Release, and by the kernel when the process ends however it
// ends — a crash, a SIGKILL, a closed terminal — because the lock is the
// kernel's own on the open file, not a record in the file that a later run
// would have to judge stale.
type WorkdirLock struct {
	f    *os.File
	path string
}

// LockWorkdirs takes project's workdir lock for holder, or reports the run
// that holds it as *WorkdirBusyError. It never waits: a second converge is
// not a queue to join, it is a run that would delete the first one's tree.
//
// holder is written into the lock file, and is what a refused run prints; it
// is the command line as the user typed it, such as "fugaro update".
func LockWorkdirs(getenv func(string) string, project, holder string) (*WorkdirLock, error) {
	path, err := WorkdirLockPath(getenv, project)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("the workdir lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the workdir lock %s: %w", path, err)
	}
	if err := lockWorkdirFile(f); err != nil {
		defer f.Close()
		if errors.Is(err, errWorkdirLockHeld) {
			held, pid := readWorkdirLockHolder(f)
			return nil, &WorkdirBusyError{Project: project, Holder: held, PID: pid}
		}
		return nil, fmt.Errorf("the workdir lock %s: %w", path, err)
	}
	// What the refusal reads: the lock itself is the kernel's, and this line
	// is a courtesy for the person who is about to be told to wait. A write
	// that fails leaves the lock held and the file saying nothing, which is
	// why it is not the run's failure — the alternative is a converge that
	// refuses to run because it could not say who it was.
	body := holder + " " + strconv.Itoa(os.Getpid()) + "\n"
	_, _ = f.Seek(0, io.SeekStart)
	if _, err := f.WriteString(body); err == nil {
		_ = f.Truncate(int64(len(body)))
	}
	return &WorkdirLock{f: f, path: path}, nil
}

// readWorkdirLockHolder is what the holder's lock file says: its name and its
// pid, either of which may be missing (an empty file, a file an older fugaro
// wrote, a foreign file). Neither is trusted for anything but the refusal's
// wording — the lock is held or it is not, and that is the kernel's answer.
func readWorkdirLockHolder(f *os.File) (string, int) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0
	}
	b, err := io.ReadAll(io.LimitReader(f, 200))
	if err != nil {
		return "", 0
	}
	fields := strings.Fields(string(b))
	// Every line this format writes ends in the holder's pid, so a body that
	// does not is not one of ours — a foreign file, a file from another tool,
	// a truncated write — and it says nothing rather than naming whatever
	// words happen to be in it.
	if len(fields) == 0 {
		return "", 0
	}
	pid, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil || pid <= 0 {
		return "", 0
	}
	return strings.Join(fields[:len(fields)-1], " "), pid
}

// Release gives up the lock. It is safe to call on a lock that was never
// taken (nil), so a run can defer it beside the call that takes it.
func (l *WorkdirLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	err := unlockWorkdirFile(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
