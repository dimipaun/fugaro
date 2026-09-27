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
	// sleep 30s: comfortably longer than both gitWaitDelay (5s) and the
	// 20s assertion below, so a regression (no WaitDelay, waiting for the
	// leaked child to exit on its own) actually fails this test instead of
	// coincidentally finishing in time. The assertion leaves 15s of slack
	// over gitWaitDelay because a heavily loaded machine (load average
	// above 100 on 16 cores) was observed to stretch the commit itself to
	// 12s. Killed in Cleanup regardless.
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 30 & echo $! > "+pidFile+"\n"), 0o755); err != nil {
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
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("git commit took %s; a leaked hook child blocked it", d)
	}
}
