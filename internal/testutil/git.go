// Package testutil holds helpers shared by tests.
package testutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// gitWaitDelay bounds how long Git's Wait spends draining output after the
// git process itself has exited. Without it, a leaked descendant (a hook
// backgrounding a process, or some other process that inherits the output
// pipe) can make Wait — and so every test that calls Git or NewRemote —
// hang indefinitely. See the identical fix and its rationale in
// internal/gitops, which guards production git invocations the same way.
const gitWaitDelay = 5 * time.Second

// gitCommandTimeout is a backstop: gitWaitDelay only bounds Wait once git
// itself has exited (or ctx has ended); it does nothing if the tracked git
// process itself is the one not returning. That case was observed on a
// heavily loaded machine, where a plain `git clone`/`commit`/`push` against
// a local, empty fixture repo occasionally took minutes instead of
// milliseconds (goroutine dump: blocked in os/exec.(*Cmd).Wait →
// syscall.Wait4, i.e. genuinely waiting on the git process itself, not a
// leaked descendant). The 60s bound turns that into a loud, attributable
// test failure instead of stalling the whole binary.
const gitCommandTimeout = 60 * time.Second

// ModuleRoot returns the repository root.
func ModuleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// IsolateGit keeps the developer's global and system git config (signing,
// hooks, templates) out of the test, and sets a commit identity.
func IsolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.invalid")
	}
}

// Git runs git in dir and returns its trimmed stdout, failing the test on
// error, or if it doesn't finish within gitCommandTimeout.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = gitWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		// git itself exited 0; see internal/gitops's identical tolerance.
		err = nil
	}
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (git did not finish within %s)", err, gitCommandTimeout)
		}
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// WriteFiles writes files (path → content) under dir, creating directories.
// Every file is executable so fixture scripts can run directly.
func WriteFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// NewRemote creates a bare repository whose main branch holds files and returns its path.
func NewRemote(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	Git(t, root, "init", "--quiet", "--bare", "-b", "main", bare)
	// The client side's -c overrides don't reach receive-pack (git resets
	// GIT_CONFIG_PARAMETERS before invoking it for local transport too), so
	// this has to be set in the remote's own config: otherwise receive-pack
	// spawns a detached `git maintenance run --auto --quiet --detach` after
	// nearly every push. That process, once daemonized, can still hold the
	// push's inherited output pipe open (the same leaked-descendant class
	// gitWaitDelay guards elsewhere) — confirmed by tracing a real push with
	// GIT_TRACE=1 and observing it disappear once this is set to false.
	Git(t, bare, "config", "receive.autogc", "false")
	Git(t, bare, "config", "maintenance.auto", "false")
	seed := filepath.Join(root, "seed")
	Git(t, root, "clone", "--quiet", bare, seed)
	WriteFiles(t, seed, files)
	Git(t, seed, "add", "-A")
	Git(t, seed, "commit", "--quiet", "-m", "seed")
	Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return bare
}
