package infra

import (
	"bufio"
	"errors"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// The holder is this test binary, re-executed with lockHelperEnv set: a real
// second process, because that is what the lock is about — the kernel holds it
// on the open file, so it has to be a process the kernel can end.
//
// The two talk over the holder's own ends, and neither waits on a clock: the
// holder says it holds the lock on its stdout, and the test lets it go by
// closing its stdin. A file the holder wrote, a sleep or a deadline is a moment
// the test guessed at, and a holder that ends on the guess rather than on the
// test's word leaves the assertions beside it proving nothing.
const (
	lockHelperEnv        = "FUGARO_WORKDIRLOCK_HELPER"
	lockHelperLockedLine = "the workdir lock is held"
)

// lockHelperHeld is the lock the holder took, in a variable that outlives every
// collection. The kernel's lock is on the *os.File inside the *WorkdirLock, and
// an os.File nothing references is closed by a finalizer — so a holder that
// dropped the value (LockWorkdirs's first result is the one that matters, and
// the tempting `if _, err := LockWorkdirs(...)` drops it) would have the lock
// given up by a garbage collection at a moment nothing chose: still alive,
// still believing it holds the lock, and holding nothing. That is the flake of
// 2026-11-22 under load — the collection landed between the holder's ready line
// and the parent's next question, and the lock read as free while the holder
// was running.
var lockHelperHeld *WorkdirLock

func TestMain(m *testing.M) {
	if mode := os.Getenv(lockHelperEnv); mode != "" {
		runWorkdirLockHelper(mode)
		return
	}
	// D2313's door (config.RepoSecretsOff), opened for every other test in this
	// package exactly as internal/cli's TestMain opens it. A repository's
	// `secrets:` is disabled for now, so this package mounts none of it and
	// lists no step for one; the tests of the machinery the ruling keeps — a
	// repository's own container, its mount, its place in Readiness's missing
	// list and in SecretCommands' steps, all of it still in the tree because
	// the ruling keeps the code — drive it with the door open, exactly as they
	// did before the ruling. secrets_disabled_test.go is the test of the ruling
	// itself, and it turns the door on again for its own run.
	config.RepoSecretsOff = false
	os.Exit(m.Run())
}

// runWorkdirLockHelper is the second process: it takes the lock, says so on its
// stdout, and then does what the mode names. It exits 3 when it cannot take the
// lock, which is a failed test and not a mode.
func runWorkdirLockHelper(mode string) {
	var err error
	lockHelperHeld, err = LockWorkdirs(os.Getenv, "proj-1234", "fugaro update")
	if err != nil {
		fmt.Fprintln(os.Stderr, "the helper could not take the lock:", err)
		os.Exit(3)
	}
	fmt.Fprintln(os.Stdout, lockHelperLockedLine)
	switch mode {
	case "sleep":
		select {} // ended by the parent's kill
	case "wait":
		// Ended by the parent's closing of this process's stdin: the holder
		// goes when the test says so, which is what makes the exit this mode is
		// about the holder's own and not a timer's.
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	os.Exit(3)
}

// lockChildEnv is os.Environ with the helper's own variables and the state
// directory replaced rather than appended: the child reads the first match, so
// a key the parent also has would otherwise win.
func lockChildEnv(state, mode string) []string {
	drop := map[string]bool{
		lockHelperEnv:    true,
		"XDG_STATE_HOME": true, "XDG_CONFIG_HOME": true, "HOME": true,
	}
	out := []string{lockHelperEnv + "=" + mode, "XDG_STATE_HOME=" + state}
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && drop[k] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// lockHolder is the second process, holding the lock until the test lets it
// go: killing it is the other way to end it, and the one a crash looks like.
type lockHolder struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

func startLockHolder(t *testing.T, state, mode string) *lockHolder {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = lockChildEnv(state, mode)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The holder's own word that it holds the lock, read from its stdout. The
	// read ends when the holder writes the line and when it ends, so a holder
	// that could not take the lock is this failure at once — with the reason it
	// printed on stderr — rather than a deadline this test spent twenty seconds
	// on and then called the same thing.
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != lockHelperLockedLine {
		_ = cmd.Wait()
		t.Fatalf("the second process did not report the lock (%q, %v)", line, err)
	}
	return &lockHolder{cmd: cmd, stdin: stdin}
}

// letGo ends the holder by its own exit, so the lock is given up by an exit and
// not by a signal: closing the holder's stdin is the event it is waiting on,
// and the wait below is for the process itself.
func (h *lockHolder) letGo(t *testing.T) {
	t.Helper()
	if err := h.stdin.Close(); err != nil {
		t.Fatalf("letting the holder go: %v", err)
	}
	if err := h.cmd.Wait(); err != nil {
		t.Fatalf("the holder ended with %v, want a clean exit", err)
	}
}

// kill ends the holder with no chance to clean up: a crash, an out-of-memory
// kill, a laptop lid.
func (h *lockHolder) kill(t *testing.T) {
	t.Helper()
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatalf("killing the holder: %v", err)
	}
	_ = h.cmd.Wait()
}

// lockIsFree reports whether this process can take the lock right now, giving
// it straight back.
func lockIsFree(t *testing.T, getenv func(string) string) bool {
	t.Helper()
	l, err := LockWorkdirs(getenv, "proj-1234", "fugaro update")
	if err != nil {
		var busy *WorkdirBusyError
		if errors.As(err, &busy) {
			return false
		}
		t.Fatalf("taking the lock: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("releasing the lock: %v", err)
	}
	return true
}

func lockState(t *testing.T) (string, func(string) string) {
	t.Helper()
	state := t.TempDir()
	return state, env(map[string]string{"XDG_STATE_HOME": state})
}

// TestWorkdirLockPathIsOnePerProject: the lock file is the one file in the
// directory a project's workdirs live in, whichever of them a run is about to
// write, which is what makes a converge of one project and a converge of
// another independent.
func TestWorkdirLockPathIsOnePerProject(t *testing.T) {
	getenv := env(map[string]string{"XDG_STATE_HOME": "/s"})
	got, err := WorkdirLockPath(getenv, "proj-1234")
	if err != nil || got != "/s/fugaro/terraform/proj-1234/"+WorkdirLockFile {
		t.Fatalf("WorkdirLockPath = %q, %v", got, err)
	}
	if _, err := WorkdirLockPath(getenv, "../x"); err == nil {
		t.Fatal("a project that is not an ID made a lock path")
	}
}

// TestWorkdirLockRefusesTheSecondRun: while one run holds the lock, the next
// one is refused at once, in the sentence that names the command that holds it
// and its pid — and with nothing else, because waiting, retrying and removing
// the file are all the wrong thing to do about a run that is under way.
func TestWorkdirLockRefusesTheSecondRun(t *testing.T) {
	_, getenv := lockState(t)
	first, err := LockWorkdirs(getenv, "proj-1234", "fugaro update")
	if err != nil {
		t.Fatalf("the first run: %v", err)
	}
	defer func() { _ = first.Release() }()
	start := time.Now()
	_, err = LockWorkdirs(getenv, "proj-1234", "fugaro setup")
	if err == nil {
		t.Fatal("the second run took a lock the first one holds")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("the second run waited for the lock")
	}
	var busy *WorkdirBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("the second run's error is %T (%v), not *WorkdirBusyError", err, err)
	}
	if busy.Holder != "fugaro update" || busy.PID != os.Getpid() || busy.Project != "proj-1234" {
		t.Fatalf("the refusal is %+v, want the holder, its pid and the project", busy)
	}
	want := fmt.Sprintf("another fugaro update is running on this machine for project proj-1234 (pid %d), wait for it to finish, then rerun", os.Getpid())
	if got := err.Error(); got != want {
		t.Errorf("the refusal is\n  %s\nwant\n  %s", got, want)
	}
	// A refusal with no holder line has no pid to name, and then it is left out
	// rather than printed as 0.
	if got := (&WorkdirBusyError{Project: "proj-1234"}).Error(); got != "another fugaro update is running on this machine for project proj-1234, wait for it to finish, then rerun" {
		t.Errorf("a refusal with no holder line: %q", got)
	}
}

// TestWorkdirLockIsTakenAgainAfterRelease: the lock the first run gives back is
// the second run's, and the file says so — the pid in it is the new holder's,
// never the one that left it behind.
func TestWorkdirLockIsTakenAgainAfterRelease(t *testing.T) {
	_, getenv := lockState(t)
	first, err := LockWorkdirs(getenv, "proj-1234", "fugaro update")
	if err != nil {
		t.Fatalf("the first run: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("releasing: %v", err)
	}
	second, err := LockWorkdirs(getenv, "proj-1234", "fugaro setup")
	if err != nil {
		t.Fatalf("the second run after the first released it: %v", err)
	}
	defer func() { _ = second.Release() }()
	held, pid := readWorkdirLockHolder(second.f)
	if held != "fugaro setup" || pid != os.Getpid() {
		t.Errorf("the lock file says %q %d, want the new holder", held, pid)
	}
}

// TestWorkdirLockIsPerProject: two projects' runs are not each other's
// business, and a lock one holds says nothing about the other's workdirs.
func TestWorkdirLockIsPerProject(t *testing.T) {
	_, getenv := lockState(t)
	first, err := LockWorkdirs(getenv, "proj-1234", "fugaro update")
	if err != nil {
		t.Fatalf("the first project: %v", err)
	}
	defer func() { _ = first.Release() }()
	other, err := LockWorkdirs(getenv, "proj-5678", "fugaro update")
	if err != nil {
		t.Fatalf("a second project was refused by the first project's lock: %v", err)
	}
	defer func() { _ = other.Release() }()
}

// TestWorkdirLockGoesWithTheProcess: the lock is the kernel's on the open file,
// so a holder that ends — by its own exit, or killed with no chance to clean up
// — leaves nothing behind for the next run to judge stale. A pid file would go
// stale here, which is why there is not one.
func TestWorkdirLockGoesWithTheProcess(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		end  func(t *testing.T, h *lockHolder)
	}{
		{"the holder exits", "wait", func(t *testing.T, h *lockHolder) { h.letGo(t) }},
		{"the holder is killed", "sleep", func(t *testing.T, h *lockHolder) { h.kill(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, getenv := lockState(t)
			holder := startLockHolder(t, state, tc.mode)
			if lockIsFree(t, getenv) {
				t.Fatal("the lock was free while the second process held it")
			}
			tc.end(t, holder)
			// The holder has been waited for, so the question is asked once and
			// not polled for: the claim is that the lock goes with the process,
			// and a poll would pass on a lock freed a moment later, or one the
			// holder never took.
			if !lockIsFree(t, getenv) {
				t.Fatal("the lock outlived the process that held it")
			}
			// The file stays — it is the next run's to take — and holding the
			// lock means holding it, not finding a file.
			path, err := WorkdirLockPath(getenv, "proj-1234")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("the lock file is gone after the holder ended: %v", err)
			}
		})
	}
}

// TestWorkdirLockHolderMayBeUnreadable: the file is the holder's to write and
// mine to read, and a body that makes no sense is not a reason to refuse
// nothing — the lock is what refuses, and the sentence only loses its pid.
func TestWorkdirLockHolderMayBeUnreadable(t *testing.T) {
	_, getenv := lockState(t)
	first, err := LockWorkdirs(getenv, "proj-1234", "fugaro update")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()
	path, err := WorkdirLockPath(getenv, "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a pid in sight\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LockWorkdirs(getenv, "proj-1234", "fugaro setup")
	var busy *WorkdirBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("the second run: %v", err)
	}
	if busy.Holder != "" || busy.PID != 0 {
		t.Errorf("a body with no name and no pid still gave one: %+v", busy)
	}
	if got, want := busy.Error(), "another fugaro update is running on this machine for project proj-1234, wait for it to finish, then rerun"; got != want {
		t.Errorf("the refusal is\n  %s\nwant\n  %s", got, want)
	}
}

// TestWorkdirLockBodyIsOneLine: what the holder writes is the one line the
// refusal reads, so a reader never has to decide what part of a longer file
// means.
func TestWorkdirLockBodyIsOneLine(t *testing.T) {
	_, getenv := lockState(t)
	l, err := LockWorkdirs(getenv, "proj-1234", "fugaro update")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Release() }()
	path, err := WorkdirLockPath(getenv, "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), "fugaro update "+strconv.Itoa(os.Getpid())+"\n"; got != want {
		t.Errorf("the lock file holds %q, want %q", got, want)
	}
}

// TestWorkdirLockHalvesTakeThePlatformsThatHaveTheirCall pins which of the two
// halves a platform compiles, because the answer is a build constraint and a
// wrong one is not a wrong sentence: it is a package that does not build for
// that platform at all (syscall.Flock is missing on solaris and aix) or one
// that keeps no lock where it could. The two constraints are complements, and
// the third answer is windows, which takes the lock through LockFileEx and so
// compiles neither of these files.
//
// The rule applied is go/build's, which is the toolchain's own reading of a
// constraint — so this is the compiler's answer about the two files rather
// than a restatement of the lines it reads.
func TestWorkdirLockHalvesTakeThePlatformsThatHaveTheirCall(t *testing.T) {
	for _, tc := range []struct {
		goos          string
		flock, noLock bool
	}{
		// syscall.Flock is defined here.
		{"linux", true, false},
		{"darwin", true, false},
		{"freebsd", true, false},
		{"openbsd", true, false},
		{"netbsd", true, false},
		{"dragonfly", true, false},
		{"illumos", true, false},
		{"android", true, false},
		{"ios", true, false},
		// Not defined here, so the no-op is what states the loss.
		{"solaris", false, true},
		{"aix", false, true},
		{"plan9", false, true},
		{"js", false, true},
		{"wasip1", false, true},
		// LockFileEx, in workdirlock_windows.go.
		{"windows", false, false},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			ctx := build.Context{GOOS: tc.goos, GOARCH: "amd64"}
			flock, err := ctx.MatchFile(".", "workdirlock_flock.go")
			if err != nil {
				t.Fatal(err)
			}
			noLock, err := ctx.MatchFile(".", "workdirlock_other.go")
			if err != nil {
				t.Fatal(err)
			}
			if flock != tc.flock || noLock != tc.noLock {
				t.Errorf("GOOS=%s compiles flock=%v and the no-op=%v, want %v and %v", tc.goos, flock, noLock, tc.flock, tc.noLock)
			}
		})
	}
}
