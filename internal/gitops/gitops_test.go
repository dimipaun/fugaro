package gitops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

var ctx = context.Background()

func setup(t *testing.T) (*Repo, string) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{
		"README.md":  "hello\n",
		".gitignore": "build/\nnode_modules/\n",
	})
	repo, err := OpenOrClone(ctx, filepath.Join(t.TempDir(), "work"), remote, IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	return repo, remote
}

func TestOpenOrCloneNeedsRemote(t *testing.T) {
	if _, err := OpenOrClone(ctx, t.TempDir(), "", nil); err == nil {
		t.Fatal("want an error when there is no checkout and no remote")
	}
}

func TestCheckoutNewBranchKeepsIgnored(t *testing.T) {
	repo, _ := setup(t)
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"node_modules/dep/index.js": "warm cache\n",
		"stray.txt":                 "untracked\n",
	})
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, repo.Dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/x" {
		t.Fatalf("branch = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "stray.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file survived the reset")
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "node_modules", "dep", "index.js")); err != nil {
		t.Fatal("ignored warm cache was wiped:", err)
	}
}

func TestCommitAllAheadAndClean(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.CommitAll(ctx, "nothing")
	if err != nil || changed {
		t.Fatalf("CommitAll on a clean tree = %v, %v", changed, err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if clean, _ := repo.IsClean(ctx); clean {
		t.Fatal("IsClean = true with an untracked file")
	}
	changed, err = repo.CommitAll(ctx, "add a")
	if err != nil || !changed {
		t.Fatalf("CommitAll = %v, %v", changed, err)
	}
	if clean, _ := repo.IsClean(ctx); !clean {
		t.Fatal("IsClean = false after commit")
	}
	if n, err := repo.AheadOf(ctx, "main"); err != nil || n != 1 {
		t.Fatalf("AheadOf = %d, %v", n, err)
	}
	if err := repo.CommitEmpty(ctx, "empty"); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.AheadOf(ctx, "main"); n != 2 {
		t.Fatalf("AheadOf after empty commit = %d", n)
	}
	if got := testutil.Git(t, repo.Dir, "log", "-1", "--format=%an"); got != "Fugaro" {
		t.Fatalf("author = %q, want Fugaro", got)
	}
}

func TestCommitAllIgnoresFailingHook(t *testing.T) {
	repo, _ := setup(t)
	hook := filepath.Join(repo.Dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal("a failing pre-commit hook blocked the runner's commit:", err)
	}
}

// TestLeakedHookChildDoesNotHang: a repo hook that forks a background child
// (a shell "&") hands that child the git process's stdout/stderr pipes. Since
// git() captures output in a bytes.Buffer, exec.Cmd copies through an
// internal pipe; without a bound, Wait blocks until every holder of the
// write end closes it — including a child git itself no longer waits for.
// See internal/procgroup, which guards agent invocations the same way.
func TestLeakedHookChildDoesNotHang(t *testing.T) {
	repo, _ := setup(t)
	hook := filepath.Join(repo.Dir, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 20 &\necho leaked\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := repo.CheckoutNewBranch(ctx, "main", "fugaro/hangtest")
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("CheckoutNewBranch took %s (err=%v); a leaked hook child blocked it", d, err)
	}
}

func TestPushCreatesRemoteBranch(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	head, _ := repo.HeadSHA(ctx)
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != head {
		t.Fatalf("remote branch = %s, want %s", got, head)
	}
}
