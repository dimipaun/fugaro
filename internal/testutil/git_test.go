package testutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestIsolateGitDropsEnvironmentConfig runs where a run's agent runs its tests:
// with the stamp's core.hooksPath in the environment (gitops.StampEnv), which
// outranks the test repository's own config and would replace its hooks.
func TestIsolateGitDropsEnvironmentConfig(t *testing.T) {
	elsewhere := t.TempDir()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", elsewhere)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hookspath'='"+elsewhere+"'")
	IsolateGit(t)
	dir := t.TempDir()
	Git(t, dir, "init", "--quiet", "-b", "main", dir)
	ran := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(dir, ".git", "hooks", "post-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+ran+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	WriteFiles(t, dir, map[string]string{"a.txt": "a\n"})
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "--quiet", "-m", "seed")
	if _, err := os.Stat(ran); err != nil {
		t.Fatalf("the repository's own post-commit hook did not run: %v", err)
	}
}

// TestGitDoesNotHangOnLeakedHookChild reproduces, against the exported Git
// helper itself, the same hang class fixed in internal/gitops: a repo hook
// that backgrounds a child hands it the git process's stdout/stderr pipes,
// and without a bound, Wait (and so Git, and every test that calls it via
// NewRemote) blocks until that leaked child closes them on its own. The
// commit itself succeeds, so this must not fail the test either.
func TestGitDoesNotHangOnLeakedHookChild(t *testing.T) {
	IsolateGit(t)
	dir := t.TempDir()
	Git(t, dir, "init", "--quiet", "-b", "main", dir)

	pidFile := filepath.Join(t.TempDir(), "leaked.pid")
	hook := filepath.Join(dir, ".git", "hooks", "post-commit")
	// sleep 600s: far longer than gitWaitDelay (5s) and the generous 180s
	// bound below, so a regression (no WaitDelay, waiting for the leaked
	// child to exit on its own) fails this test. The proof doesn't lean on
	// the bound: after Git returns, the leaked child must still be alive,
	// which shows WaitDelay, not the child exiting, ended the wait, however
	// loaded the machine is. Killed in Cleanup regardless.
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 600 & echo $! > "+pidFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	WriteFiles(t, dir, map[string]string{"a.txt": "a\n"})
	Git(t, dir, "add", "-A")
	start := time.Now()
	Git(t, dir, "commit", "--quiet", "-m", "seed") // fails the test itself on error or hang
	if d := time.Since(start); d > 180*time.Second {
		t.Fatalf("git commit took %s; a leaked hook child blocked it", d)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the post-commit hook never ran, so nothing leaked: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("leaked pid %q: %v", data, err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the leaked hook child (pid %d) is gone (%v), so it can't have been what the wait was bounded against", pid, err)
	}
}
