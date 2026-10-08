package gitops

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

// writeFailingHook installs a hook named name in the checkout at dir that
// always exits 1.
func writeFailingHook(t *testing.T, dir, name string) {
	t.Helper()
	hook := filepath.Join(dir, ".git", "hooks", name)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho hook "+name+" ran >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestRunnerGitSkipsRepoHooks: runner-owned git never runs the repository's
// hooks. A failing pre-push must not block finalize's push, a failing
// post-checkout must not fail bootstrap, and prepare-commit-msg, which
// --no-verify does not skip, must not block the runner's commits.
func TestRunnerGitSkipsRepoHooks(t *testing.T) {
	for _, h := range []string{"post-checkout", "pre-push", "prepare-commit-msg"} {
		t.Run(h, func(t *testing.T) {
			repo, remote := setup(t)
			writeFailingHook(t, repo.Dir, h)
			if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
				t.Fatal("CheckoutNewBranch:", err)
			}
			testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
			if _, err := repo.CommitAll(ctx, "add a"); err != nil {
				t.Fatal("CommitAll:", err)
			}
			if err := repo.CommitEmpty(ctx, "empty"); err != nil {
				t.Fatal("CommitEmpty:", err)
			}
			if err := repo.Push(ctx, "fugaro/x"); err != nil {
				t.Fatal("Push:", err)
			}
			head, _ := repo.HeadSHA(ctx)
			if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != head {
				t.Fatalf("remote branch = %s, want %s", got, head)
			}
		})
	}
}

// TestLeakedChildDoesNotHang: a process git spawns that forks a background
// child hands that child git's stdout/stderr pipes. Since git() captures
// output in a bytes.Buffer, exec.Cmd copies through an internal pipe; without
// a bound, Wait blocks until every holder of the write end closes it,
// including a child git itself no longer waits for. See internal/procgroup,
// which guards agent invocations the same way. The fetch itself succeeds
// (git exits 0), so this must not surface as an error either: gitWaitDelay
// only forces the pipe closed early; it doesn't truncate git's own
// (already-complete) output.
//
// Runner git never runs repository hooks (see noHooks), so the leak comes
// from the remote side instead, as in production, where receive-pack's
// detached `git maintenance` is the known offender: remote.origin.uploadpack
// points at a wrapper that backgrounds a child holding stderr, then execs
// the real git-upload-pack.
func TestLeakedChildDoesNotHang(t *testing.T) {
	repo, _ := setup(t)
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "leaked.pid")
	wrapper := filepath.Join(tmp, "upload-pack.sh")
	// sleep 600s: far longer than gitWaitDelay (5s) and the generous 180s
	// bound below, so a regression (waiting for the leaked child to exit on
	// its own) fails this test. The proof doesn't lean on the bound, though:
	// after the call returns, the leaked child must still be alive, which
	// shows WaitDelay, not the child exiting, ended the wait, however slow
	// the machine is. Killed in Cleanup regardless. Only stderr is leaked:
	// holding the protocol pipe (stdout) would stall upload-pack itself.
	script := "#!/bin/sh\nsleep 600 </dev/null >/dev/null & echo $! > " + pidFile + "\nexec git-upload-pack \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "config", "remote.origin.uploadpack", wrapper)
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	start := time.Now()
	err := repo.CheckoutNewBranch(ctx, "main", "fugaro/hangtest")
	if d := time.Since(start); d > 180*time.Second {
		t.Fatalf("CheckoutNewBranch took %s (err=%v); a leaked child blocked it", d, err)
	}
	if err != nil {
		t.Fatalf("CheckoutNewBranch returned an error for a successful checkout with a leaked child: %v", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the upload-pack wrapper never ran, so nothing leaked: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("leaked pid %q: %v", data, err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the leaked child (pid %d) is gone (%v), so it can't have been what the wait was bounded against", pid, err)
	}
	if got := testutil.Git(t, repo.Dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/hangtest" {
		t.Fatalf("branch = %q, want fugaro/hangtest", got)
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

func TestPushOverwritesTheAgentsOwnPushAfterAmend(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	// The agent pushes on its own, then amends the pushed commit.
	testutil.Git(t, repo.Dir, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "--amend", "-m", "first, amended")
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if got, want := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"), testutil.Git(t, repo.Dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote branch %s, want the amended %s", got, want)
	}
}

func TestPushRefusesATipFromElsewhere(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	// Someone else pushes to the run branch from their own clone.
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", remote, other)
	testutil.Git(t, other, "commit", "--quiet", "--allow-empty", "-m", "theirs")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	theirs := testutil.Git(t, other, "rev-parse", "HEAD")

	err := repo.Push(ctx, "fugaro/x")
	if err == nil || !strings.Contains(err.Error(), "something else pushed") {
		t.Fatalf("err = %v", err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != theirs {
		t.Fatalf("remote branch %s was overwritten (want %s)", got, theirs)
	}
}

// TestPushRefusesATipFromElsewhereEvenAfterFetch: the lease must not be
// satisfied merely because the foreign commit is present in the local
// object store. A plain `git fetch origin` (something the agent might run
// on its own) brings such a commit in without making it an ancestor of HEAD
// or touching HEAD's reflog, so it must still be refused.
func TestPushRefusesATipFromElsewhereEvenAfterFetch(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", remote, other)
	testutil.Git(t, other, "commit", "--quiet", "--allow-empty", "-m", "theirs")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	theirs := testutil.Git(t, other, "rev-parse", "HEAD")

	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin")

	err := repo.Push(ctx, "fugaro/x")
	if err == nil || !strings.Contains(err.Error(), "something else pushed") {
		t.Fatalf("err = %v", err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != theirs {
		t.Fatalf("remote branch %s was overwritten (want %s)", got, theirs)
	}
}

func TestPushRefusesNonRunBranches(t *testing.T) {
	repo, remote := setup(t)
	before := testutil.Git(t, remote, "rev-parse", "refs/heads/main")
	if err := repo.CommitEmpty(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	branches := []string{
		"main", "fugaro/", "feature/fugaro/x",
		// Passes the prefix check but must be refused by check-ref-format,
		// so the run-branch guarantee can't be bypassed through the refspec.
		"fugaro/x y", "fugaro/x..y", "fugaro/x.lock", "fugaro/x~1", "fugaro/x:y",
	}
	for _, b := range branches {
		if err := repo.Push(ctx, b); err == nil || !strings.Contains(err.Error(), "refusing to push") {
			t.Errorf("Push(%q) err = %v", b, err)
		}
	}
	if after := testutil.Git(t, remote, "rev-parse", "refs/heads/main"); after != before {
		t.Fatalf("main moved from %s to %s", before, after)
	}
}

func TestShowFile(t *testing.T) {
	repo, remote := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", remote, other)
	testutil.WriteFiles(t, other, map[string]string{"docs/notes.md": "base notes\n\n"})
	testutil.Git(t, other, "add", "-A")
	testutil.Git(t, other, "commit", "--quiet", "-m", "notes")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	// The working tree says something else: ShowFile reads the revision.
	testutil.WriteFiles(t, repo.Dir, map[string]string{"docs/notes.md": "branch notes\n"})
	if err := repo.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ShowFile(ctx, "origin/main", "docs/notes.md")
	if err != nil || string(got) != "base notes\n\n" {
		t.Fatalf("ShowFile = %q, %v", got, err)
	}
	if _, err := repo.ShowFile(ctx, "origin/main", "missing.md"); err == nil {
		t.Fatal("ShowFile of a missing file succeeded")
	}
}

func TestShowFileRefusesTraversal(t *testing.T) {
	repo, _ := setup(t)
	for _, p := range []string{"", ".", "/etc/passwd", "../x", "a/../../x", "a/../b", "./a", "a//b", "a/", `a\..\b`, "-x"} {
		if _, err := repo.ShowFile(ctx, "HEAD", p); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("ShowFile(HEAD, %q) err = %v", p, err)
		}
	}
	for _, rev := range []string{"", "--output=/tmp/x", "-p"} {
		if _, err := repo.ShowFile(ctx, rev, "README.md"); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("ShowFile(%q, README.md) err = %v", rev, err)
		}
	}
}

func TestPushExistingUpdatesBranch(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	start, _ := repo.HeadSHA(ctx)
	if err := repo.CommitEmpty(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	if err := repo.PushExisting(ctx, "fugaro/x", start); err != nil {
		t.Fatal(err)
	}
	if got, want := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"), testutil.Git(t, repo.Dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote branch %s, want %s", got, want)
	}
}

func TestPushExistingRefusesAbsentBranch(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	start, _ := repo.HeadSHA(ctx)
	if err := repo.CommitEmpty(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	err := repo.PushExisting(ctx, "fugaro/x", start)
	if !errors.Is(err, ErrBranchGone) || !strings.Contains(err.Error(), "fugaro/x no longer exists on origin; not recreating it") {
		t.Fatalf("err = %v", err)
	}
	if out := testutil.Git(t, remote, "for-each-ref", "refs/heads/fugaro/"); out != "" {
		t.Fatalf("the branch was recreated: %s", out)
	}
}

func TestPushForeignTipIsErrForeignTip(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	start, _ := repo.HeadSHA(ctx)
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", "--branch", "fugaro/x", remote, other)
	testutil.Git(t, other, "commit", "--quiet", "--allow-empty", "-m", "theirs")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	if err := repo.CommitEmpty(ctx, "ours again"); err != nil {
		t.Fatal(err)
	}
	if err := repo.PushExisting(ctx, "fugaro/x", start); !errors.Is(err, ErrForeignTip) {
		t.Fatalf("err = %v, want ErrForeignTip", err)
	}
}

func TestRemoteTip(t *testing.T) {
	repo, remote := setup(t)
	if tip, err := repo.RemoteTip(ctx, "fugaro/x"); err != nil || tip != "" {
		t.Fatalf("RemoteTip of an absent branch = %q, %v", tip, err)
	}
	if tip, err := repo.RemoteTip(ctx, "main"); err != nil || tip != testutil.Git(t, remote, "rev-parse", "refs/heads/main") {
		t.Fatalf("RemoteTip(main) = %q, %v", tip, err)
	}
}

func TestTreeEntryMode(t *testing.T) {
	repo, remote := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", remote, other)
	if err := os.Symlink("README.md", filepath.Join(other, "LINK.md")); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, other, "add", "-A")
	testutil.Git(t, other, "commit", "--quiet", "-m", "link")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	if err := repo.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"README.md": "100755", "LINK.md": "120000", "missing.md": ""} {
		if got, err := repo.TreeEntryMode(ctx, "origin/main", p); err != nil || got != want {
			t.Errorf("TreeEntryMode(%s) = %q, %v; want %q", p, got, err, want)
		}
	}
	if _, err := repo.TreeEntryMode(ctx, "origin/main", "../x"); err == nil {
		t.Error("TreeEntryMode accepted ../x")
	}
}

func TestBlobSize(t *testing.T) {
	repo, remote := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", remote, other)
	if err := os.WriteFile(filepath.Join(other, "big.txt"), []byte(strings.Repeat("y", 4321)), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, other, "add", "-A")
	testutil.Git(t, other, "commit", "--quiet", "-m", "big")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	if err := repo.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.BlobSize(ctx, "origin/main", "big.txt"); err != nil || n != 4321 {
		t.Errorf("BlobSize(big.txt) = %d, %v", n, err)
	}
	if _, err := repo.BlobSize(ctx, "origin/main", "missing.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	for _, rev := range []string{"", "-x", "a:b"} {
		if _, err := repo.BlobSize(ctx, rev, "big.txt"); err == nil {
			t.Errorf("BlobSize accepted rev %q", rev)
		}
	}
	if _, err := repo.BlobSize(ctx, "origin/main", "../x"); err == nil {
		t.Error("BlobSize accepted ../x")
	}
}

// TestPushExistingRefusesRewoundBranch: a person who rewinds the branch
// during the run (reset and force-push) pushed too, even though the new
// tip is an ancestor of this run's HEAD; it must not be pushed over.
func TestPushExistingRefusesRewoundBranch(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"a", "b (to be dropped)"} {
		if err := repo.CommitEmpty(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	start, _ := repo.HeadSHA(ctx)
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", "--branch", "fugaro/x", remote, other)
	testutil.Git(t, other, "reset", "--quiet", "--hard", "HEAD~1")
	testutil.Git(t, other, "push", "--quiet", "-f", "origin", "HEAD:refs/heads/fugaro/x")
	rewound := testutil.Git(t, other, "rev-parse", "HEAD")
	if err := repo.CommitEmpty(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	if err := repo.PushExisting(ctx, "fugaro/x", start); !errors.Is(err, ErrForeignTip) {
		t.Fatalf("err = %v, want ErrForeignTip", err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != rewound {
		t.Fatalf("the rewind was pushed over: remote %s, want %s", got, rewound)
	}
}

// TestPushExistingAcceptsItsOwnEarlierPush: a retried push finds the
// branch already at HEAD.
func TestPushExistingAcceptsItsOwnEarlierPush(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	start, _ := repo.HeadSHA(ctx)
	if err := repo.CommitEmpty(ctx, "ours"); err != nil {
		t.Fatal(err)
	}
	if err := repo.PushExisting(ctx, "fugaro/x", start); err != nil {
		t.Fatal(err)
	}
	if err := repo.PushExisting(ctx, "fugaro/x", start); err != nil {
		t.Fatalf("a repeated push: %v", err)
	}
}

// TestPushExistingRaceIsForeignTip: someone pushes between the tip check
// and the push, so the lease refuses it; that is someone else's push too,
// not a generic failure. The remote's pre-receive hook plays that person,
// moving the branch and refusing the push. A refusal that leaves the tip
// where it was stays a plain error.
func TestPushExistingRaceIsForeignTip(t *testing.T) {
	for _, moves := range []bool{true, false} {
		repo, remote := setup(t)
		if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
			t.Fatal(err)
		}
		if err := repo.CommitEmpty(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if err := repo.Push(ctx, "fugaro/x"); err != nil {
			t.Fatal(err)
		}
		start, _ := repo.HeadSHA(ctx)
		if err := repo.CommitEmpty(ctx, "ours"); err != nil {
			t.Fatal(err)
		}
		main := testutil.Git(t, remote, "rev-parse", "refs/heads/main")
		hook := "#!/bin/sh\nexit 1\n"
		if moves {
			hook = "#!/bin/sh\nenv -u GIT_QUARANTINE_PATH -u GIT_OBJECT_DIRECTORY -u GIT_ALTERNATE_OBJECT_DIRECTORIES git update-ref refs/heads/fugaro/x " + main + "\nexit 1\n"
		}
		if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
		err := repo.PushExisting(ctx, "fugaro/x", start)
		if err == nil || errors.Is(err, ErrForeignTip) != moves {
			t.Fatalf("moves %v: err = %v", moves, err)
		}
		if moves && testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x") != main {
			t.Fatal("the hook did not move the branch")
		}
	}
}

// An agent can write .git/config, so a core.fsmonitor command runs under
// the runner's git at finalize: the model credentials must not be in its env.
func TestGitEnvStripsModelCredentials(t *testing.T) {
	repo, _ := setup(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-real-key")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-real")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-real")
	t.Setenv("KEEP_ME", "kept")
	dump := filepath.Join(t.TempDir(), "env.txt")
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nenv > "+dump+"\nprintf '\\0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "config", "core.fsmonitor", hook)
	repo.Env = append(repo.Env, "ANTHROPIC_API_KEY=sk-real-key-from-env")
	repo.StripEnv = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the fsmonitor hook never ran: %v", err)
	}
	if !strings.Contains(string(b), "KEEP_ME=kept") {
		t.Fatalf("the hook's env lost an ordinary variable:\n%s", b)
	}
	for _, leaked := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "real"} {
		if strings.Contains(string(b), leaked) {
			t.Errorf("the hook's env holds %s:\n%s", leaked, b)
		}
	}
}

// A provider key (FUGARO_PROVIDER_KEY_*, any case) never reaches git or a
// command git runs, even for a Repo whose StripEnv is empty.
func TestGitEnvStripsProviderKeys(t *testing.T) {
	repo, _ := setup(t)
	t.Setenv("FUGARO_PROVIDER_KEY_OPENROUTER", "or-real-key")
	t.Setenv("fugaro_provider_key_other", "other-real-key")
	t.Setenv("KEEP_ME", "kept")
	dump := filepath.Join(t.TempDir(), "env.txt")
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nenv > "+dump+"\nprintf '\\0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "config", "core.fsmonitor", hook)
	repo.Env = append(repo.Env, "FUGARO_PROVIDER_KEY_FROM_REPO_ENV=repo-env-key")
	repo.StripEnv = nil
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the fsmonitor hook never ran: %v", err)
	}
	if !strings.Contains(string(b), "KEEP_ME=kept") {
		t.Fatalf("the hook's env lost an ordinary variable:\n%s", b)
	}
	if strings.Contains(strings.ToUpper(string(b)), "PROVIDER_KEY") || strings.Contains(string(b), "real-key") {
		t.Errorf("the hook's env holds a provider key:\n%s", b)
	}
}

func TestOpenOrCloneStripsFromTheStart(t *testing.T) {
	_, remote := setup(t)
	dir := filepath.Join(t.TempDir(), "w")
	repo, err := OpenOrClone(ctx, dir, remote, IdentityEnv(), ModelCredentialVars...)
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.StripEnv) != len(ModelCredentialVars) {
		t.Fatalf("StripEnv = %v", repo.StripEnv)
	}
	again, _ := OpenOrClone(ctx, dir, remote, nil, "X")
	if len(again.StripEnv) != 1 {
		t.Fatalf("an existing checkout's StripEnv = %v", again.StripEnv)
	}
}

func TestPushFastForwardPushesAndSkipsWhenCurrent(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "fugaro/x", one); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != one {
		t.Fatalf("remote = %s, want %s", got, one)
	}
	if err := repo.CommitEmpty(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	two, _ := repo.HeadSHA(ctx)
	for range 2 { // the second finds the remote already there and pushes nothing
		if err := repo.PushFastForward(ctx, "fugaro/x", two); err != nil {
			t.Fatal(err)
		}
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != two {
		t.Fatalf("remote = %s, want %s", got, two)
	}
	// Pushed to the URL, not the remote's name: no remote-tracking ref,
	// so no ref lock the agent's own git could meet.
	if got := testutil.Git(t, repo.Dir, "for-each-ref", "refs/remotes/origin/fugaro/"); got != "" {
		t.Fatalf("the push wrote a local ref: %q", got)
	}
}

func TestPushFastForwardRefusesRewrittenHistory(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "fugaro/x", one); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "commit", "--quiet", "--amend", "--allow-empty", "-m", "one, amended")
	amended, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "fugaro/x", amended); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("err = %v, want ErrNotFastForward", err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != one {
		t.Fatalf("the pushed commit was replaced: remote at %s, want %s", got, one)
	}
}

func TestPushFastForwardRefusesNonRunBranches(t *testing.T) {
	repo, _ := setup(t)
	head, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "main", head); err == nil || !strings.Contains(err.Error(), "only pushes") {
		t.Fatalf("err = %v, want the run-branch refusal", err)
	}
}

func TestCountAheadOfACommit(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	base, _ := repo.HeadSHA(ctx)
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	// The tip a checkpoint read before the agent's commit is the base:
	// nothing of the run's own, whatever HEAD has meanwhile.
	if n, err := repo.CountAhead(ctx, "main", base); err != nil || n != 0 {
		t.Fatalf("CountAhead(base) = %d, %v; want 0", n, err)
	}
	one, _ := repo.HeadSHA(ctx)
	if n, err := repo.CountAhead(ctx, "main", one); err != nil || n != 1 {
		t.Fatalf("CountAhead(one) = %d, %v; want 1", n, err)
	}
	for _, until := range invalidSHAs(one) {
		if _, err := repo.CountAhead(ctx, "main", until); err == nil || !strings.Contains(err.Error(), "not a full commit SHA") {
			t.Errorf("CountAhead(%q) err = %v, want a refusal", until, err)
		}
	}
}

// invalidSHAs are what a checkpoint must never take for the commit it
// read: anything but the full lowercase SHA.
func invalidSHAs(sha string) []string {
	return []string{
		"+" + sha, "HEAD", "fugaro/x", "refs/heads/fugaro/x", sha[:12], sha[:39], "-" + sha[1:], "--output=/tmp/x", "",
		strings.ToUpper(sha), sha + " ", " " + sha[1:], sha + "\n", sha[:20] + "\n" + sha[21:], sha + "^", "@",
	}
}

// pushOne is a run branch with one commit pushed by PushFastForward.
func pushOne(t *testing.T) (*Repo, string, string) {
	t.Helper()
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "fugaro/x", one); err != nil {
		t.Fatal(err)
	}
	return repo, remote, one
}

func remoteBranch(t *testing.T, remote string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", remote, "rev-parse", "--verify", "--quiet", "refs/heads/fugaro/x").Output()
	return strings.TrimSpace(string(out))
}

func setSeam(t *testing.T, f func()) {
	t.Helper()
	pushFFSeam = f
	t.Cleanup(func() { pushFFSeam = nil })
}

func TestPushFastForwardRefusesAnythingButAFullSHA(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	setSeam(t, func() { t.Error("reached the push") })
	for _, sha := range invalidSHAs(one) {
		if err := repo.PushFastForward(ctx, "fugaro/x", sha); err == nil || !strings.Contains(err.Error(), "not a full commit SHA") {
			t.Errorf("PushFastForward(%q) err = %v, want a refusal", sha, err)
		}
	}
	if got := remoteBranch(t, remote); got != "" {
		t.Fatalf("the branch was pushed: remote at %s", got)
	}
}

func TestPushFastForwardRefusesAnOriginURLLikeAnOption(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	marker := filepath.Join(t.TempDir(), "ran")
	setSeam(t, func() { t.Error("reached the push") })
	for _, url := range []string{
		"--upload-pack=touch " + marker,
		"--receive-pack=touch " + marker,
		"-oProxyCommand=touch " + marker,
		"--tags",
		"-",
		remote + "\n--tags",
	} {
		testutil.Git(t, repo.Dir, "config", "remote.origin.url", url)
		if err := repo.PushFastForward(ctx, "fugaro/x", one); !errors.Is(err, errUnsafeURL) {
			t.Errorf("url %q: err = %v, want errUnsafeURL", url, err)
		}
	}
	if exists(marker) {
		t.Fatal("a command from origin's URL ran")
	}
	if got := remoteBranch(t, remote); got != "" {
		t.Fatalf("the branch was pushed: remote at %s", got)
	}
}

func TestPushFastForwardReadsOriginURLOnce(t *testing.T) {
	repo, remote := setup(t)
	other := testutil.NewRemote(t, map[string]string{"README.md": "other\n"})
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	// The agent rewrites .git/config between the check and the push.
	setSeam(t, func() { testutil.Git(t, repo.Dir, "remote", "set-url", "origin", other) })
	if err := repo.PushFastForward(ctx, "fugaro/x", one); err != nil {
		t.Fatal(err)
	}
	if got := remoteBranch(t, remote); got != one {
		t.Fatalf("checked remote at %q, want %s", got, one)
	}
	if got := remoteBranch(t, other); got != "" {
		t.Fatalf("pushed to the URL set after the check: %s", got)
	}
}

func TestPushFastForwardFailedPushIsAnError(t *testing.T) {
	repo, remote := setup(t)
	rejectingRemote(t, remote, "no pushes today")
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	err := repo.PushFastForward(ctx, "fugaro/x", one)
	if err == nil {
		t.Fatal("a refused push returned nil")
	}
	if errors.Is(err, ErrNotFastForward) {
		t.Fatalf("err = %v, a refusal is not a non-fast-forward", err)
	}
	if strings.Contains(err.Error(), remote) || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("err = %q, want origin named, not its URL", err)
	}
	if got := remoteBranch(t, remote); got != "" {
		t.Fatalf("remote at %s after a refused push", got)
	}
}

func TestPushFastForwardPushesTheSHAItWasGiven(t *testing.T) {
	repo, remote, one := pushOne(t)
	if err := repo.CommitEmpty(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	read, _ := repo.HeadSHA(ctx)
	// The agent commits again after the poll read the tip.
	if err := repo.CommitEmpty(ctx, "three"); err != nil {
		t.Fatal(err)
	}
	if err := repo.PushFastForward(ctx, "fugaro/x", read); err != nil {
		t.Fatal(err)
	}
	if got := remoteBranch(t, remote); got != read {
		t.Fatalf("remote at %s, want the sha read %s (one was %s)", got, read, one)
	}
}

func TestPushFastForwardNeverForcesWhenTheRemoteMoves(t *testing.T) {
	repo, remote, one := pushOne(t)
	if err := repo.CommitEmpty(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	two, _ := repo.HeadSHA(ctx)
	// Someone pushes a commit the checkout never had, after the
	// ancestry check passed and before the push.
	var theirs string
	setSeam(t, func() {
		tree := testutil.Git(t, remote, "rev-parse", one+"^{tree}")
		theirs = testutil.Git(t, remote, "commit-tree", tree, "-p", one, "-m", "theirs")
		testutil.Git(t, remote, "update-ref", "refs/heads/fugaro/x", theirs)
	})
	if err := repo.PushFastForward(ctx, "fugaro/x", two); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("err = %v, want ErrNotFastForward", err)
	}
	if got := remoteBranch(t, remote); got != theirs {
		t.Fatalf("their commit was replaced: remote at %s, want %s", got, theirs)
	}
}

func TestPushFastForwardSkipsWhenOriginIsAhead(t *testing.T) {
	repo, remote, one := pushOne(t)
	if err := repo.CommitEmpty(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	two, _ := repo.HeadSHA(ctx)
	testutil.Git(t, repo.Dir, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x") // the agent pushed by itself
	setSeam(t, func() { t.Error("reached the push") })
	if err := repo.PushFastForward(ctx, "fugaro/x", one); err != nil {
		t.Fatalf("err = %v, want nil: origin already has %s", err, one)
	}
	if got := remoteBranch(t, remote); got != two {
		t.Fatalf("remote at %s, want %s", got, two)
	}
}

func TestIsAncestorPassesACancelThrough(t *testing.T) {
	repo, _, one := pushOne(t)
	if err := repo.CommitEmpty(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	two, _ := repo.HeadSHA(ctx)
	if ok, err := repo.isAncestor(ctx, one, two); err != nil || !ok {
		t.Fatalf("isAncestor(one, two) = %v, %v", ok, err)
	}
	if ok, err := repo.isAncestor(ctx, two, one); err != nil || ok {
		t.Fatalf("isAncestor(two, one) = %v, %v", ok, err)
	}
	if ok, err := repo.isAncestor(ctx, strings.Repeat("ab", 20), two); err != nil || ok {
		t.Fatalf("isAncestor(unknown, two) = %v, %v; want false, nil", ok, err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.isAncestor(cctx, one, two); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: err = %v, want context.Canceled", err)
	}
	// And so PushFastForward never calls a cancel a rewritten history.
	if err := repo.PushFastForward(cctx, "fugaro/x", two); err == nil || errors.Is(err, ErrNotFastForward) {
		t.Fatalf("cancelled push: err = %v", err)
	}
}

func TestCheckpointTipOnlyWhenSettled(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	head, _ := repo.HeadSHA(ctx)
	if got, err := repo.CheckpointTip(ctx, "fugaro/x"); err != nil || got != head {
		t.Fatalf("CheckpointTip = %q, %v; want %s", got, err, head)
	}
	if _, err := repo.CheckpointTip(ctx, "fugaro/y"); !errors.Is(err, ErrGitBusy) {
		t.Fatalf("another branch: err = %v, want ErrGitBusy", err)
	}
	gitDir := filepath.Join(repo.Dir, ".git")
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "rebase-merge", "rebase-apply"} {
		p := filepath.Join(gitDir, name)
		if err := os.WriteFile(p, []byte(head+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CheckpointTip(ctx, "fugaro/x"); !errors.Is(err, ErrGitBusy) {
			t.Errorf("%s present: err = %v, want ErrGitBusy", name, err)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "--detach")
	if _, err := repo.CheckpointTip(ctx, "fugaro/x"); !errors.Is(err, ErrGitBusy) {
		t.Fatalf("detached HEAD: err = %v, want ErrGitBusy", err)
	}
}

func TestDefaultBranch(t *testing.T) {
	ctx := context.Background()
	testutil.IsolateGit(t)
	open := func(remote string) *Repo {
		dir := filepath.Join(t.TempDir(), "w")
		testutil.Git(t, filepath.Dir(dir), "init", "-q", dir)
		testutil.Git(t, dir, "remote", "add", "origin", remote)
		return &Repo{Dir: dir, Env: IdentityEnv()}
	}
	// A normal remote: its HEAD names main, whatever the files say.
	remote := testutil.NewRemote(t, map[string]string{"fugaro.yaml": "git: { base_branch: other }\n"})
	if got, err := open(remote).DefaultBranch(ctx); err != nil || got != "main" {
		t.Fatalf("DefaultBranch = %q, %v", got, err)
	}
	// HEAD on another branch is that branch, slashes included.
	testutil.Git(t, remote, "branch", "release/1", "main")
	testutil.Git(t, remote, "symbolic-ref", "HEAD", "refs/heads/release/1")
	if got, err := open(remote).DefaultBranch(ctx); err != nil || got != "release/1" {
		t.Fatalf("DefaultBranch = %q, %v", got, err)
	}
	// An empty remote has an unborn HEAD and no default to report.
	empty := filepath.Join(t.TempDir(), "empty.git")
	testutil.Git(t, filepath.Dir(empty), "init", "-q", "--bare", "-b", "main", empty)
	if got, err := open(empty).DefaultBranch(ctx); err == nil {
		t.Fatalf("an empty remote gave %q", got)
	}
	// A remote that can't be reached is an error too.
	if _, err := open(filepath.Join(t.TempDir(), "gone.git")).DefaultBranch(ctx); err == nil {
		t.Fatal("a missing remote gave a branch")
	}
}

func TestPushFastForwardForeignTipTheCheckoutNeverHad(t *testing.T) {
	repo, remote, _ := pushOne(t)
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", remote, other)
	testutil.Git(t, other, "checkout", "--quiet", "fugaro/x")
	testutil.Git(t, other, "-c", "user.name=o", "-c", "user.email=o@x", "commit", "--quiet", "--allow-empty", "-m", "foreign")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	if err := repo.CommitEmpty(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	two, _ := repo.HeadSHA(ctx)
	setSeam(t, func() { t.Error("reached the push") })
	if err := repo.PushFastForward(ctx, "fugaro/x", two); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("err = %v, want ErrNotFastForward", err)
	}
}

func TestPushFastForwardRefusesTransportHelpersAndSchemes(t *testing.T) {
	repo, remote, _ := pushOne(t)
	if err := repo.CommitEmpty(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	two, _ := repo.HeadSHA(ctx)
	marker := filepath.Join(t.TempDir(), "ran")
	setSeam(t, func() { t.Error("reached the push") })
	testutil.Git(t, repo.Dir, "config", "protocol.ext.allow", "always")
	for _, url := range []string{
		"ext::sh -c 'touch " + marker + "'",
		"fd::17",
		"foo+bar::addr",
		"ftp://example.com/x.git",
		"rsync://example.com/x.git",
	} {
		testutil.Git(t, repo.Dir, "config", "remote.origin.url", url)
		if err := repo.PushFastForward(ctx, "fugaro/x", two); !errors.Is(err, errUnsafeURL) {
			t.Errorf("url %q: err = %v, want errUnsafeURL", url, err)
		}
	}
	// Even past the check, git itself does not run ext:: from the runner.
	if _, err := repo.remoteTipAt(ctx, "ext::sh -c 'touch "+marker+"'", "refs/heads/fugaro/x"); err == nil {
		t.Error("ls-remote accepted ext::")
	}
	if exists(marker) {
		t.Fatal("a command from origin's URL ran")
	}
	if got := remoteBranch(t, remote); got == two {
		t.Fatal("the branch was pushed")
	}
}

func TestHideURL(t *testing.T) {
	base := errors.New("fatal: unable to access 'https://user:tok@example.com/org/repo.git/': boom; also https://example.com/org/repo and example.com/org/repo")
	got := hideURL(base, "https://user:tok@example.com/org/repo").Error()
	for _, leak := range []string{"example.com", "tok", "org/repo"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaks in %q", leak, got)
		}
	}
	short := errors.New("fatal: a/b is not a repository")
	if got := hideURL(short, "a/b").Error(); got != short.Error() {
		t.Errorf("a short URL mangled the text: %q", got)
	}
	if got := hideURL(short, "a/b"); got != short {
		t.Errorf("short URL wrapped the error")
	}
}
