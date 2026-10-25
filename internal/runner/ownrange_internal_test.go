package runner

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// ownRangeRepo is a checkout of a remote with main and an integration branch
// cut from it with n commits of its own, everything fetched; it returns the
// checkout, the remote and the integration tip.
func ownRangeRepo(t *testing.T, n int) (*gitops.Repo, string, string) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	for i := range n {
		testutil.WriteFiles(t, seed, map[string]string{"integration.txt": string(rune('a'+i)) + "\n"})
		testutil.Git(t, seed, "add", "-A")
		testutil.Git(t, seed, "commit", "--quiet", "-m", "integration's own")
	}
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/integration")
	tip := testutil.Git(t, seed, "rev-parse", "HEAD")
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "work"), remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "+refs/heads/*:refs/remotes/origin/*")
	return repo, remote, tip
}

func commitFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		testutil.WriteFiles(t, dir, map[string]string{n: n + "\n"})
		testutil.Git(t, dir, "add", "-A")
		testutil.Git(t, dir, "commit", "--quiet", "-m", "the run's "+n)
	}
}

// TestOwnCommitsBase is the decision the review range is narrowed by
// (diffbase.go, ownCommitsBase): a run launched without --ref (its recorded
// base is main) whose agent cut its branch from an integration branch is
// reviewed over its own commits, and every other shape keeps today's range.
//
// Mutation (return the first own commit's parent without comparing the two
// counts): the run-on-main and merge-of-a-feature cases narrow when they
// must not. Mutation (drop the run branch's --exclude in gitops.OwnCommits):
// the pushed case reads no own commits and keeps origin/main.
func TestOwnCommitsBase(t *testing.T) {
	ctx := context.Background()
	const runBranch = "fugaro/r1"

	t.Run("a run cut from integration while its ref is main", func(t *testing.T) {
		repo, _, tip := ownRangeRepo(t, 5)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
		commitFiles(t, repo.Dir, "a.txt", "b.txt", "c.txt")
		base, why := ownCommitsBase(ctx, repo, runBranch, "origin/main")
		if base != tip {
			t.Fatalf("base = %q (%s), want the integration tip %s", base, why, tip)
		}
		if n := testutil.Git(t, repo.Dir, "rev-list", "--count", base+"..HEAD"); n != "3" {
			t.Fatalf("the narrowed range holds %s commits, want the run's 3", n)
		}
		// The run pushed its branch after each commit: still its own.
		testutil.Git(t, repo.Dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+runBranch)
		testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "+refs/heads/*:refs/remotes/origin/*")
		if again, why := ownCommitsBase(ctx, repo, runBranch, "origin/main"); again != tip {
			t.Fatalf("after the run's own push base = %q (%s), want %s", again, why, tip)
		}
	})

	t.Run("a run on main", func(t *testing.T) {
		repo, _, _ := ownRangeRepo(t, 2)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/main")
		commitFiles(t, repo.Dir, "a.txt", "b.txt")
		if base, why := ownCommitsBase(ctx, repo, runBranch, "origin/main"); base != "" {
			t.Fatalf("a run on main narrowed to %s (%s), want its range unchanged", base, why)
		}
	})

	t.Run("no commits of its own yet", func(t *testing.T) {
		repo, _, _ := ownRangeRepo(t, 4)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
		if base, why := ownCommitsBase(ctx, repo, runBranch, "origin/main"); base != "" {
			t.Fatalf("a run with no commits narrowed to %s (%s), want its range unchanged", base, why)
		}
	})

	t.Run("a merge commit among its own", func(t *testing.T) {
		repo, remote, tip := ownRangeRepo(t, 4)
		// main moves on, and the run merges it into its integration-based
		// branch: the merge is the run's, main's commit is not.
		seed := filepath.Join(t.TempDir(), "main-seed")
		testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
		commitFiles(t, seed, "person.txt")
		testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
		testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "+refs/heads/*:refs/remotes/origin/*")
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
		commitFiles(t, repo.Dir, "a.txt")
		testutil.Git(t, repo.Dir, "merge", "--quiet", "--no-edit", "origin/main")
		commitFiles(t, repo.Dir, "b.txt")
		base, why := ownCommitsBase(ctx, repo, runBranch, "origin/main")
		if base != tip {
			t.Fatalf("base = %q (%s), want the integration tip %s", base, why, tip)
		}
	})

	t.Run("a merge of another branch into a run on main", func(t *testing.T) {
		repo, _, _ := ownRangeRepo(t, 3)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/main")
		commitFiles(t, repo.Dir, "a.txt")
		testutil.Git(t, repo.Dir, "merge", "--quiet", "--no-edit", "origin/integration")
		if base, why := ownCommitsBase(ctx, repo, runBranch, "origin/main"); base != "" {
			t.Fatalf("narrowed to %s (%s): a range that holds what the run merged must not be cut", base, why)
		}
	})

	t.Run("a base that does not resolve", func(t *testing.T) {
		repo, _, _ := ownRangeRepo(t, 3)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
		commitFiles(t, repo.Dir, "a.txt")
		if base, why := ownCommitsBase(ctx, repo, runBranch, "origin/gone"); base != "" {
			t.Fatalf("narrowed to %s (%s) against a base the checkout lacks, want today's range", base, why)
		}
	})
}

// TestReviewRevIsNarrowedForAFirstRunOnly: a follow-up's and a review run's
// range is the pull request's base, read from the provider, and their
// prefetch fetched for it at bootstrap; a narrowed commit left behind on the
// run must never move it. narrowReviewRange does not set one there either.
//
// Mutation (drop the follow/review condition from reviewRev): this test
// fails.
func TestReviewRevIsNarrowedForAFirstRunOnly(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	newRun := func() *run {
		return &run{
			d:          Deps{Log: slog.New(slog.DiscardHandler)},
			spec:       &task.Spec{Ref: "main"},
			cfg:        &config.Config{Git: config.Git{BaseBranch: "main"}},
			narrowBase: sha,
		}
	}
	if got := newRun().reviewRev(); got != sha {
		t.Errorf("a first run's narrowed range starts at %q, want %s", got, sha)
	}
	f := newRun()
	f.follow = &followState{pr: gitprov.PRInfo{Base: "integration"}}
	if got := f.reviewRev(); got != "origin/integration" {
		t.Errorf("a follow-up's range starts at %q, want origin/integration", got)
	}
	rv := newRun()
	rv.review = true
	if got := rv.reviewRev(); got != "origin/main" {
		t.Errorf("a review run's range starts at %q, want origin/main", got)
	}
	plain := newRun()
	plain.narrowBase = ""
	if got := plain.reviewRev(); got != "origin/main" {
		t.Errorf("an unnarrowed first run's range starts at %q, want origin/main", got)
	}
}
