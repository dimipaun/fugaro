package infra

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The holder is this test binary, re-executed with lockHelperEnv set: a real
// second process, because that is what the lock is about — the kernel holds it
// on the open file, so it has to be a process the kernel can end.
const (
	lockHelperEnv      = "FUGARO_WORKDIRLOCK_HELPER"
	lockHelperReadyEnv = "FUGARO_WORKDIRLOCK_READY"
	lockHelperGoEnv    = "FUGARO_WORKDIRLOCK_GO"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(lockHelperEnv); mode != "" {
		runWorkdirLockHelper(mode)
		return
	}
	os.Exit(m.Run())
}

// runWorkdirLockHelper is the second process: it takes the lock, says so by
// writing the ready file, and then does what the mode names. It exits 3 when
// it cannot take the lock, which is a failed test and not a mode.
func runWorkdirLockHelper(mode string) {
	if _, err := LockWorkdirs(os.Getenv, "proj-1234", "fugaro update"); err != nil {
		fmt.Fprintln(os.Stderr, "the helper could not take the lock:", err)
		os.Exit(3)
	}
	if ready := os.Getenv(lockHelperReadyEnv); ready != "" {
		if err := os.WriteFile(ready, nil, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "the helper could not write the ready file:", err)
			os.Exit(3)
		}
	}
	switch mode {
	case "sleep":
		select {} // ended by the parent's kill
	case "wait":
		waitForFile(os.Getenv(lockHelperGoEnv))
		os.Exit(0)
	}
	os.Exit(3)
}

func waitForFile(path string) {
	for i := 0; i < 2000; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lockChildEnv is os.Environ with the helper's own variables replaced rather
// than appended: the child reads the first match, so a key the parent also has
// would otherwise win.
func lockChildEnv(state, mode, ready, goFile string) []string {
	drop := map[string]bool{
		lockHelperEnv: true, lockHelperReadyEnv: true, lockHelperGoEnv: true,
		"XDG_STATE_HOME": true, "XDG_CONFIG_HOME": true, "HOME": true,
	}
	out := []string{lockHelperEnv + "=" + mode, "XDG_STATE_HOME=" + state}
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && drop[k] {
			continue
		}
		out = append(out, kv)
	}
	if ready != "" {
		out = append(out, lockHelperReadyEnv+"="+ready)
	}
	if goFile != "" {
		out = append(out, lockHelperGoEnv+"="+goFile)
	}
	return out
}

// lockHolder is the second process, holding the lock until the test lets it
// go: killing it is the other way to end it, and the one a crash looks like.
type lockHolder struct {
	cmd    *exec.Cmd
	goFile string
}

func startLockHolder(t *testing.T, state, mode string) *lockHolder {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	goFile := filepath.Join(t.TempDir(), "go")
	cmd := exec.Command(os.Args[0])
	cmd.Env = lockChildEnv(state, mode, ready, goFile)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second process to take the workdir lock", func() bool {
		_, err := os.Stat(ready)
		return err == nil
	})
	return &lockHolder{cmd: cmd, goFile: goFile}
}

// letGo ends the holder by its own exit, so the lock is given up by an exit and
// not by a signal.
func (h *lockHolder) letGo(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(h.goFile, nil, 0o600); err != nil {
		t.Fatal(err)
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

// waitFor polls until cond, or fails the test: the lock is taken by another
// process, so every assertion about it is about something that happens in
// another process's time.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waited 20s for %s", what)
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
			waitFor(t, "the lock to be free once the holder ended", func() bool { return lockIsFree(t, getenv) })
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
