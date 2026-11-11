package gitops

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// advanceBase adds a commit to the remote's main branch from a checkout of
// its own, so the run's own checkout has a base it has not got: the state
// every rebase run starts in. What it writes is the caller's, because the
// conflict a test wants comes from the two sides touching the same lines.
func advanceBase(t *testing.T, remote string, files map[string]string) string {
	t.Helper()
	other, err := OpenOrClone(ctx, filepath.Join(t.TempDir(), "other"), remote, IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, other.Dir, files)
	testutil.Git(t, other.Dir, "add", "-A")
	testutil.Git(t, other.Dir, "commit", "--quiet", "-m", "the base moved on")
	testutil.Git(t, other.Dir, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return testutil.Git(t, other.Dir, "rev-parse", "HEAD")
}

// theBranch is a run branch cut from main with one commit of its own, pushed,
// and the base moved on afterwards: ready for a rebase that conflicts.
func theBranch(t *testing.T, files map[string]string, baseFiles map[string]string) (repo *Repo, remote, start, base string) {
	t.Helper()
	repo, remote = setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, files)
	if _, err := repo.CommitAll(ctx, "the run's own work"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if start, _ = repo.HeadSHA(ctx); start == "" {
		t.Fatal("no start commit")
	}
	base = advanceBase(t, remote, baseFiles)
	if err := repo.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	return repo, remote, start, base
}

// TestGitopsRebaseConflictRefuses: a branch whose own change and whose base's
// change touch the same lines cannot be put on its base. The call says so
// with ErrRebaseConflict, the checkout is left mid-rebase (which is what
// RebaseInProgress reports, and what the conflict-resolution stage needs), the
// branch is still where it was, and the abort the refusal path calls puts the
// checkout back exactly as the rebase found it — nothing of a rebase nobody
// resolved reaches the branch.
func TestGitopsRebaseConflictRefuses(t *testing.T) {
	repo, _, start, _ := theBranch(t,
		map[string]string{"README.md": "the branch's side\n"},
		map[string]string{"README.md": "the base's side\n"})
	if repo.RebaseInProgress(ctx) {
		t.Fatal("a rebase is in progress before one was started")
	}
	err := repo.Rebase(ctx, "main")
	if !errors.Is(err, ErrRebaseConflict) || !repo.RebaseInProgress(ctx) {
		t.Fatalf("Rebase err = %v, in progress = %v, want ErrRebaseConflict and a rebase left to resolve", err, repo.RebaseInProgress(ctx))
	}
	// The branch ref, not HEAD: a conflicted rebase leaves HEAD detached on
	// the commit it was replaying, and what must not have moved is the branch
	// a push would carry.
	if got := testutil.Git(t, repo.Dir, "rev-parse", "refs/heads/fugaro/x"); got != start {
		t.Fatalf("the branch is at %s, want %s: it moved before anyone resolved the conflict", got, start)
	}
	if err := repo.RebaseAbort(ctx); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, repo.Dir, "rev-parse", "refs/heads/fugaro/x"); got != start || repo.RebaseInProgress(ctx) {
		t.Fatalf("the branch after the abort is at %s, want %s, in progress = %v", got, start, repo.RebaseInProgress(ctx))
	}
	if body, err := repo.ShowFile(ctx, "HEAD", "README.md"); err != nil || string(body) != "the branch's side\n" {
		t.Fatalf("README.md after the abort = %q, %v, want the branch's own side", body, err)
	}
}

// TestGitopsRebaseResolvesAndContinues: the conflict-resolution stage's half
// of the same call. A resolution that rewrites the conflicted file and lets
// the rebase go on leaves the branch on its base — the base's own commit an
// ancestor of the new head, which is what a resolved rebase is — and the
// resolution's content is what the file holds and what the replayed commit
// carries.
func TestGitopsRebaseResolvesAndContinues(t *testing.T) {
	repo, _, start, base := theBranch(t,
		map[string]string{"README.md": "the branch's side\n"},
		map[string]string{"README.md": "the base's side\n"})
	if err := repo.Rebase(ctx, "main"); !errors.Is(err, ErrRebaseConflict) {
		t.Fatalf("Rebase err = %v, want ErrRebaseConflict", err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"README.md": "both sides\n"})
	if err := repo.RebaseContinue(ctx); err != nil {
		t.Fatal(err)
	}
	if repo.RebaseInProgress(ctx) {
		t.Fatal("the rebase is still in progress after the continue")
	}
	head, _ := repo.HeadSHA(ctx)
	if head == start {
		t.Fatal("the branch did not move: the rebase replayed nothing")
	}
	if on, err := repo.IsAncestor(ctx, base, head); err != nil || !on {
		t.Fatalf("the base %s is an ancestor of %s = %v, %v, want the rebased branch to sit on its base", base, head, on, err)
	}
	if body, err := repo.ShowFile(ctx, "HEAD", "README.md"); err != nil || string(body) != "both sides\n" {
		t.Fatalf("README.md = %q, %v, want the resolution", body, err)
	}
}

// TestGitopsRebaseUsesForceWithLease: a rebase rewrites the branch, so the
// push that carries it is the force-with-lease one and not a fast-forward one
// — the new head is not a descendant of the head the remote has — while the
// lease still holds the run to the branch it started from: a tip that is
// somebody else's is refused with ErrForeignTip and left alone, exactly as
// the follow-up push refuses one.
func TestGitopsRebaseUsesForceWithLease(t *testing.T) {
	repo, remote, start, _ := theBranch(t,
		map[string]string{"work.txt": "the run's work\n"},
		map[string]string{"other.txt": "the base's own file\n"})
	if err := repo.Rebase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	head, _ := repo.HeadSHA(ctx)
	if forward, err := repo.IsAncestor(ctx, start, head); err != nil || forward {
		t.Fatalf("the rebased head %s is a descendant of %s = %v, %v: this test proves nothing unless the rebase rewrote the branch", head, start, forward, err)
	}
	if err := repo.PushExisting(ctx, "fugaro/x", start); err != nil {
		t.Fatalf("pushing the rebased branch: %v", err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != head {
		t.Fatalf("remote branch = %s, want the rebased head %s", got, head)
	}

	stranger := filepath.Join(t.TempDir(), "stranger")
	if _, err := OpenOrClone(ctx, stranger, remote, IdentityEnv()); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, stranger, "checkout", "--quiet", "-B", "fugaro/x", "origin/fugaro/x")
	testutil.WriteFiles(t, stranger, map[string]string{"stranger.txt": "someone else's work\n"})
	testutil.Git(t, stranger, "add", "-A")
	testutil.Git(t, stranger, "commit", "--quiet", "-m", "someone else's commit")
	testutil.Git(t, stranger, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	err := repo.PushExisting(ctx, "fugaro/x", head)
	if !errors.Is(err, ErrForeignTip) {
		t.Fatalf("PushExisting err = %v, want ErrForeignTip", err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got == head {
		t.Fatal("the branch was overwritten: the lease did not hold")
	}
}
