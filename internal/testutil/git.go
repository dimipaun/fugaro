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
//
// It also drops git's environment configuration (GIT_CONFIG_COUNT and its
// numbered entries, GIT_CONFIG_PARAMETERS), which outranks a test repository's
// own config, and the stamp's trailer values (FUGARO_STAMP_*): inside a run,
// the agent's environment carries both (gitops.StampEnv), so a test that runs
// git under the agent would otherwise have its repository's hooks replaced by
// the run's, and a test of the stamp would read the run's trailers.
func IsolateGit(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if IsGitConfigEnv(k) || strings.HasPrefix(k, "FUGARO_STAMP_") {
			// t.Setenv records the value to restore at cleanup; Unsetenv
			// leaves the test with the variable absent, as outside a run.
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "Test")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "test@example.invalid")
	}
}

// IsGitConfigEnv reports whether k is one of the names git reads configuration
// from the environment under (git-config(1)): the count, the numbered key and
// value pairs it counts, and the GIT_CONFIG_PARAMETERS git's own -c builds.
//
// The list is here, rather than written at each call site, because the two
// callers need the same names with different lifetimes: IsolateGit drops them
// for the test that calls it and restores them at cleanup, and a package whose
// tests run git before they reach any harness drops them once, for the whole
// test binary, in its TestMain.
func IsGitConfigEnv(k string) bool {
	return k == "GIT_CONFIG_COUNT" || k == "GIT_CONFIG_PARAMETERS" ||
		strings.HasPrefix(k, "GIT_CONFIG_KEY_") || strings.HasPrefix(k, "GIT_CONFIG_VALUE_")
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

// RecordGit puts a git first on PATH (FakeCommand) that appends each
// invocation's arguments to the returned file, one line per process, and
// then runs the real git with them: a test reads how many git processes a
// call cost. version, when not "", is what `git --version` answers instead,
// for a test of what the caller does with an older git.
func RecordGit(t *testing.T, version string) (calls string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := ""
	if version != "" {
		script = "for a; do [ \"$a\" = --version ] && { echo 'git version " + version + "'; exit 0; }; done\n"
	}
	return FakeCommand(t, "git", script+"exec '"+real+"' \"$@\"\n")
}

// GitCalls reads the lines RecordGit's file holds and empties it, so the
// next read counts only what ran after this one. A file not written yet is
// no calls.
func GitCalls(t *testing.T, calls string) []string {
	t.Helper()
	data, err := os.ReadFile(calls)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// GitSubcommands counts GitCalls' lines by git subcommand: the first
// argument after the global options (`-c <x>` and the like), or --version.
func GitSubcommands(lines []string) map[string]int {
	out := map[string]int{}
	for _, l := range lines {
		f := strings.Fields(l)
		for i := 0; i < len(f); i++ {
			if f[i] == "-c" {
				i++
				continue
			}
			if strings.HasPrefix(f[i], "-") && f[i] != "--version" {
				continue
			}
			out[f[i]]++
			break
		}
	}
	return out
}

// ManyBranches pushes n branches to remote (b000, b001, ...), each one
// commit of its own on top of main, in one fast-import and one push, and
// returns main's tip they were cut from: a repository with as many branches
// as a busy one has, for a test of what a call costs per branch.
func ManyBranches(t *testing.T, remote string, n int) string {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "seed-branches")
	Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	main := Git(t, seed, "rev-parse", "HEAD")
	var stream strings.Builder
	for i := range n {
		msg := fmt.Sprintf("branch %d\n", i)
		fmt.Fprintf(&stream, "commit refs/heads/b%03d\ncommitter Test <test@example.invalid> 1700000000 +0000\ndata %d\n%sfrom %s\n\n", i, len(msg), msg, main)
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir, cmd.Stdin = seed, strings.NewReader(stream.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import: %v\n%s", err, out)
	}
	Git(t, seed, "push", "--quiet", "origin", "refs/heads/b*:refs/heads/b*")
	return main
}
