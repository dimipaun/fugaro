package runner

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
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
// must not. Mutation (keep the run's own branch among gitops.OriginHeads):
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

// TestNarrowedIsAFirstRunsOnly: a follow-up's and a review run's range is
// the pull request's base, read from the provider, and their prefetch
// fetched for it at bootstrap; a narrowed commit left on the run must never
// move it, in the review range or the guard (both read narrowed).
//
// Mutation (drop the follow/review condition from narrowed): this test
// fails.
func TestNarrowedIsAFirstRunsOnly(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	newRun := func() *run {
		return &run{
			d:          Deps{Log: slog.New(slog.DiscardHandler)},
			spec:       &task.Spec{Ref: "main"},
			cfg:        &config.Config{Git: config.Git{BaseBranch: "main"}},
			narrowBase: sha,
		}
	}
	if got := newRun().narrowed(); got != sha {
		t.Errorf("a first run's narrowed commit is %q, want %s", got, sha)
	}
	f := newRun()
	f.follow = &followState{pr: gitprov.PRInfo{Base: "integration"}}
	if got := f.narrowed(); got != "" {
		t.Errorf("a follow-up is narrowed to %q", got)
	}
	if got := f.reviewRev(context.Background()); got != "origin/integration" {
		t.Errorf("a follow-up's range starts at %q, want origin/integration", got)
	}
	rv := newRun()
	rv.review = true
	if got := rv.narrowed(); got != "" {
		t.Errorf("a review run is narrowed to %q", got)
	}
	if got := rv.reviewRev(context.Background()); got != "origin/main" {
		t.Errorf("a review run's range starts at %q, want origin/main", got)
	}
}

// TestReviewRevFallsBackWhenAFixRoundRebasesAway: the narrowed commit is
// used while it leads to HEAD. A fix round that rebases the run's commits
// onto origin/main leaves it behind, and `<it>...HEAD` would start at the
// integration branch's fork point — larger than the recorded range, which
// is just the run's commits once they sit on main. That round reads the
// recorded range.
//
// Mutation (drop the ancestry check in reviewRev): this test fails.
func TestReviewRevFallsBackWhenAFixRoundRebasesAway(t *testing.T) {
	ctx := context.Background()
	repo, _, tip := ownRangeRepo(t, 4)
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
	commitFiles(t, repo.Dir, "a.txt", "b.txt")
	r := &run{
		d:    Deps{Log: slog.New(slog.DiscardHandler)},
		spec: &task.Spec{Ref: "main"},
		cfg:  &config.Config{Git: config.Git{BaseBranch: "main"}},
		repo: repo,
	}
	base, why := ownCommitsBase(ctx, repo, "fugaro/r1", "origin/main")
	if base != tip {
		t.Fatalf("base = %q (%s), want the integration tip %s", base, why, tip)
	}
	r.narrowBase = base
	if got := r.reviewRev(ctx); got != tip {
		t.Fatalf("reviewRev = %q, want the narrowed %s", got, tip)
	}
	testutil.Git(t, repo.Dir, "rebase", "--quiet", "--onto", "origin/main", tip)
	if got := r.reviewRev(ctx); got != "origin/main" {
		t.Fatalf("after a rebase away from the narrowed commit reviewRev = %q, want origin/main", got)
	}
	if n := testutil.Git(t, repo.Dir, "rev-list", "--count", "origin/main..HEAD"); n != "2" {
		t.Fatalf("the recorded range holds %s commits after the rebase, want the run's 2", n)
	}
}

// TestGuardSubtractsTheNarrowedCommitOnlyWhileOriginHoldsIt: defense in
// depth behind gitops.OwnCommits — whatever set the narrowed commit, the
// guard subtracts it only when a branch on origin, as origin names it now,
// holds it. A commit of the run's own (here: one the checkout alone has) is
// never taken out of the guard.
//
// Mutation (subtract narrowed() in guardBounds without onOrigin): this test
// fails.
func TestGuardSubtractsTheNarrowedCommitOnlyWhileOriginHoldsIt(t *testing.T) {
	ctx := context.Background()
	repo, _, tip := ownRangeRepo(t, 2)
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
	commitFiles(t, repo.Dir, "w.txt", "c.txt")
	own := testutil.Git(t, repo.Dir, "rev-parse", "HEAD~1")
	r := &run{
		d:        Deps{Log: slog.New(slog.DiscardHandler)},
		spec:     &task.Spec{Ref: "main"},
		cfg:      &config.Config{Git: config.Git{BaseBranch: "main"}},
		rec:      &runstore.Record{Branch: "fugaro/r1"},
		repo:     repo,
		startSHA: testutil.Git(t, repo.Dir, "rev-parse", "origin/main"),
	}
	r.narrowBase = tip
	if _, except := r.guardBounds(ctx); !slices.Contains(except, tip) {
		t.Fatalf("guardBounds = %v, want the integration tip %s that origin holds", except, tip)
	}
	r.narrowBase = own
	if _, except := r.guardBounds(ctx); slices.Contains(except, own) {
		t.Fatalf("guardBounds = %v subtracts %s, a commit no branch on origin holds", except, own)
	}
}

// TestNarrowingAndTheGuardCostTheSameWhateverTheBranchCount: with 220
// branches on origin, narrowing the range is seven git processes (the git
// version, ls-remote, one cat-file for every head's presence, one rev-list
// for the run's own commits, the first one's parent, and the two counts),
// and the guard's check of the narrowed commit is three (ls-remote, cat-file
// and one rev-list fed the heads on stdin). Before, each was a process per
// branch: a cat-file each, and a merge-base each in the guard.
//
// Mutation (a merge-base per head in onOrigin, or a cat-file -e per head in
// gitops.OriginHeads): the counts below fail.
func TestNarrowingAndTheGuardCostTheSameWhateverTheBranchCount(t *testing.T) {
	ctx := context.Background()
	repo, remote, tip := ownRangeRepo(t, 3)
	testutil.ManyBranches(t, remote, 220)
	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "+refs/heads/*:refs/remotes/origin/*")
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
	commitFiles(t, repo.Dir, "a.txt", "b.txt")
	r := &run{
		d:    Deps{Log: slog.New(slog.DiscardHandler)},
		spec: &task.Spec{Ref: "main"},
		cfg:  &config.Config{Git: config.Git{BaseBranch: "main"}},
		rec:  &runstore.Record{Branch: "fugaro/r1"},
		repo: repo,
	}
	calls := testutil.RecordGit(t, "")
	r.narrowReviewRange(ctx)
	if r.narrowBase != tip {
		t.Fatalf("narrowBase = %q, want the integration tip %s", r.narrowBase, tip)
	}
	lines := testutil.GitCalls(t, calls)
	if n := testutil.GitSubcommands(lines); len(lines) != 7 || n["cat-file"] != 1 || n["ls-remote"] != 1 || n["merge-base"] != 0 {
		t.Fatalf("narrowing over 220 branches ran %d git processes (%v), want 7:\n%s", len(lines), n, strings.Join(lines, "\n"))
	}
	if !r.onOrigin(ctx, tip) {
		t.Fatalf("onOrigin(%s) = false, want the integration branch to hold it", tip)
	}
	lines = testutil.GitCalls(t, calls)
	if n := testutil.GitSubcommands(lines); len(lines) != 3 || n["ls-remote"] != 1 || n["cat-file"] != 1 || n["rev-list"] != 1 {
		t.Fatalf("the guard's check over 220 branches ran %d git processes (%v), want ls-remote, cat-file and rev-list", len(lines), n)
	}
	if own := testutil.Git(t, repo.Dir, "rev-parse", "HEAD"); r.onOrigin(ctx, own) {
		t.Fatalf("onOrigin(%s) = true for the run's own commit", own)
	}
}

// TestNarrowingIsOffUnderAGitOlderThan244: an older git ignores
// GIT_NO_LAZY_FETCH, and reading origin's heads would fetch every missing
// branch's commit in a blobless clone, so the range is the recorded one and
// nothing about origin's branches is read; the run says so once.
//
// Mutation (drop the version check in narrowReviewRange): the range is
// narrowed and ls-remote runs.
func TestNarrowingIsOffUnderAGitOlderThan244(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := ownRangeRepo(t, 3)
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
	commitFiles(t, repo.Dir, "a.txt")
	var logs strings.Builder
	r := &run{
		d:    Deps{Log: slog.New(slog.NewTextHandler(&logs, nil))},
		spec: &task.Spec{Ref: "main"},
		cfg:  &config.Config{Git: config.Git{BaseBranch: "main"}},
		rec:  &runstore.Record{Branch: "fugaro/r1"},
		repo: repo,
	}
	calls := testutil.RecordGit(t, "2.43.0")
	r.narrowReviewRange(ctx)
	if r.narrowBase != "" {
		t.Fatalf("narrowed to %s under git 2.43", r.narrowBase)
	}
	if n := testutil.GitSubcommands(testutil.GitCalls(t, calls)); n["ls-remote"] != 0 || n["cat-file"] != 0 {
		t.Fatalf("origin's branches were read under git 2.43: %v", n)
	}
	if c := strings.Count(logs.String(), "GIT_NO_LAZY_FETCH needs git 2.44"); c != 1 {
		t.Fatalf("the fallback is logged %d times, want once:\n%s", c, logs.String())
	}
	if got := r.reviewRev(ctx); got != "origin/main" {
		t.Fatalf("reviewRev = %q, want the recorded origin/main", got)
	}
}

// TestAReviewRoundSaysWhenAFixMovedTheBranchOffTheNarrowedCommit: the
// prompt's scope line is built once and names the narrowed commit; a round
// after a fix that rebased away from it reads the recorded range, and its
// prompt says so rather than leaving the reviewer two ranges to choose from.
// A round still on the narrowed commit carries no such line.
//
// Mutation (drop the note in reviewPromptWithInput): this test fails.
func TestAReviewRoundSaysWhenAFixMovedTheBranchOffTheNarrowedCommit(t *testing.T) {
	ctx := context.Background()
	repo, _, tip := ownRangeRepo(t, 4)
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
	commitFiles(t, repo.Dir, "a.txt", "b.txt")
	r := &run{
		d:          Deps{Log: slog.New(slog.DiscardHandler)},
		spec:       &task.Spec{Ref: "main"},
		cfg:        &config.Config{Git: config.Git{BaseBranch: "main"}},
		repo:       repo,
		narrowBase: tip,
	}
	prompt := ReviewPrompt("", "", r.reviewRev(ctx), DiffSize{}, false)
	if got := r.reviewPromptWithInput(ctx, prompt); strings.Contains(got, "moved this branch off") {
		t.Fatalf("a round on the narrowed range says it moved:\n%s", got)
	}
	testutil.Git(t, repo.Dir, "rebase", "--quiet", "--onto", "origin/main", tip)
	got := r.reviewPromptWithInput(ctx, prompt)
	want := "A fix round moved this branch off " + tip + ", so the range named above no longer leads to HEAD: this round reviews origin/main...HEAD instead"
	if !strings.Contains(got, want) {
		t.Fatalf("the round after the rebase does not say which range it reads:\n%s", got)
	}
	if !strings.Contains(got, "git diff origin/main...HEAD") {
		t.Fatalf("the input is not built over the recorded range:\n%s", got)
	}
}

// TestNarrowSummary is the line the report and the status section carry: the
// commit the review started after and the run's own count, then what else
// the pull request carries against its base; a run moved off the narrowed
// commit says which range its later rounds read, and a run never narrowed
// says nothing.
//
// Mutation (drop the "also carries" part, or count against diffBase's
// narrowed commit for both): this test fails.
func TestNarrowSummary(t *testing.T) {
	ctx := context.Background()
	repo, _, tip := ownRangeRepo(t, 5)
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
	commitFiles(t, repo.Dir, "a.txt", "b.txt", "c.txt")
	r := &run{
		d:    Deps{Log: slog.New(slog.DiscardHandler)},
		spec: &task.Spec{Ref: "main"},
		cfg:  &config.Config{Git: config.Git{BaseBranch: "main"}},
		repo: repo,
	}
	if got := r.narrowSummary(ctx); got != "" {
		t.Fatalf("a run not narrowed says %q", got)
	}
	r.narrowBase = tip
	want := "review covered commits after `" + tip[:7] + "` (3 commits); this PR also carries 5 commits from other branches"
	if got := r.narrowSummary(ctx); got != want {
		t.Fatalf("narrowSummary = %q, want %q", got, want)
	}
	testutil.Git(t, repo.Dir, "rebase", "--quiet", "--onto", "origin/main", tip)
	want = "review covered commits after `" + tip[:7] + "` until a fix round moved the branch off it; later rounds covered origin/main...HEAD"
	if got := r.narrowSummary(ctx); got != want {
		t.Fatalf("after a rebase away narrowSummary = %q, want %q", got, want)
	}
}
