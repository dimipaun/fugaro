package runner

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// scopeRun is a first run of the shape the scope comparison is read on:
// what narrowReviewRange needs (a checkout, a record and a ref), and
// nothing else. The run's diff base is main, never the integration branch
// it is cut from.
func scopeRun(repo *gitops.Repo) *run {
	return &run{
		d:    Deps{Log: slog.New(slog.DiscardHandler)},
		spec: &task.Spec{Ref: "main"},
		cfg:  &config.Config{Git: config.Git{BaseBranch: "main"}},
		rec:  &runstore.Record{Branch: "fugaro/r1"},
		repo: repo,
	}
}

// TestReviewScopeRecordsOutsideFiles: the range was kept by design — the
// run's own commits and the recorded range hold the same number of commits
// once the branch merged main in — so the range the reviewer reads covers
// what the integration branch contributed, which implement never wrote. The
// comparison is recorded (report only) with applicable false: nothing halts
// on a range that was kept deliberately (D332).
//
// Mutation (record applicable true whenever the range was read): this test
// fails on Applicable.
func TestReviewScopeRecordsOutsideFiles(t *testing.T) {
	ctx := context.Background()
	repo, remote, _ := ownRangeRepo(t, 2)
	// Main moves on too, so the run's branch merging it in leaves the two
	// ranges the same size and nothing is narrowed.
	seed := filepath.Join(t.TempDir(), "main-seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	commitFiles(t, seed, "m1.txt", "m2.txt")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "+refs/heads/*:refs/remotes/origin/*")
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
	commitFiles(t, repo.Dir, "run.txt")
	testutil.Git(t, repo.Dir, "merge", "--quiet", "--no-edit", "origin/main")

	r := scopeRun(repo)
	r.narrowReviewRange(ctx)
	if r.narrowBase != "" {
		t.Fatalf("narrowBase = %q, want the range kept as it was recorded", r.narrowBase)
	}
	sc := r.rec.ReviewScope
	if sc == nil {
		t.Fatal("the record carries no review_scope")
	}
	if sc.Applicable || sc.WhyNot != runstore.ReviewScopeNoNarrowerRange {
		t.Errorf("scope = applicable %v, why_not %q, want false and %q", sc.Applicable, sc.WhyNot, runstore.ReviewScopeNoNarrowerRange)
	}
	if want := []string{"m1.txt", "m2.txt", "run.txt"}; !slices.Equal(sc.ImplementFiles, want) {
		t.Errorf("implement_files = %v, want %v", sc.ImplementFiles, want)
	}
	if want := []string{"integration.txt", "run.txt"}; !slices.Equal(sc.ReviewFiles, want) {
		t.Errorf("review_files = %v, want %v", sc.ReviewFiles, want)
	}
	if want := []string{"integration.txt"}; !slices.Equal(sc.Outside, want) {
		t.Errorf("outside = %v, want %v", sc.Outside, want)
	}
	if sc.Truncated {
		t.Error("a comparison of three paths is recorded as truncated")
	}
	if r.narrowed() != "" {
		t.Errorf("narrowed() = %q, want the run's range untouched", r.narrowed())
	}
}

// TestReviewScopeEqualRecordsNothingOutside: where the review range IS the
// run's own commits — narrowed to them, or equal to them by construction
// because the run's branch was cut from the base it recorded — the two sets
// are the same and the comparison records nothing outside. The range
// narrowed is still applicable, and the one that equals the run's own
// commits by construction is too (D332's check is on what review read).
//
// Mutation (compare against the recorded base instead of the base the run's
// own commits start after): the narrowed case lists the integration branch's
// file as outside and fails.
func TestReviewScopeEqualRecordsNothingOutside(t *testing.T) {
	ctx := context.Background()

	t.Run("narrowed to the run's own commits", func(t *testing.T) {
		repo, _, tip := ownRangeRepo(t, 3)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
		commitFiles(t, repo.Dir, "run.txt", "second.txt")
		r := scopeRun(repo)
		r.narrowReviewRange(ctx)
		if r.narrowBase != tip {
			t.Fatalf("narrowBase = %q, want the integration tip %s", r.narrowBase, tip)
		}
		sc := r.rec.ReviewScope
		if sc == nil {
			t.Fatal("the record carries no review_scope")
		}
		if !sc.Applicable || sc.WhyNot != "" {
			t.Errorf("scope = applicable %v, why_not %q, want true and no reason", sc.Applicable, sc.WhyNot)
		}
		want := []string{"run.txt", "second.txt"}
		if !slices.Equal(sc.ImplementFiles, want) || !slices.Equal(sc.ReviewFiles, want) {
			t.Errorf("implement_files = %v, review_files = %v, want %v for both", sc.ImplementFiles, sc.ReviewFiles, want)
		}
		if len(sc.Outside) != 0 {
			t.Errorf("outside = %v, want nothing outside a range narrowed to the run's own commits", sc.Outside)
		}
	})

	t.Run("the range equals the run's own commits by construction", func(t *testing.T) {
		repo, _, _ := ownRangeRepo(t, 2)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/main")
		commitFiles(t, repo.Dir, "run.txt")
		r := scopeRun(repo)
		r.narrowReviewRange(ctx)
		if r.narrowBase != "" {
			t.Fatalf("narrowBase = %q, want the range untouched", r.narrowBase)
		}
		sc := r.rec.ReviewScope
		if sc == nil {
			t.Fatal("the record carries no review_scope")
		}
		if !sc.Applicable || sc.WhyNot != "" {
			t.Errorf("scope = applicable %v, why_not %q, want true and no reason", sc.Applicable, sc.WhyNot)
		}
		if want := []string{"run.txt"}; !slices.Equal(sc.ImplementFiles, want) || !slices.Equal(sc.ReviewFiles, want) {
			t.Errorf("implement_files = %v, review_files = %v, want %v for both", sc.ImplementFiles, sc.ReviewFiles, want)
		}
		if len(sc.Outside) != 0 {
			t.Errorf("outside = %v, want nothing outside", sc.Outside)
		}
	})
}

// TestReviewScopeNotApplicableWhenRangeKeptWide: the two ways the wide range
// is kept before anything about the run's own commits is known — a git that
// cannot read origin's branches without fetching them, and commits whose
// first one has no parent — are recorded as not applicable, with the reason
// and no comparison at all: there is no implement set to compare with, and
// an absent set is never an empty one (G5).
//
// Mutation (record an empty comparison, or applicable true, on either
// path): this test fails.
func TestReviewScopeNotApplicableWhenRangeKeptWide(t *testing.T) {
	ctx := context.Background()

	t.Run("git older than 2.44", func(t *testing.T) {
		repo, _, _ := ownRangeRepo(t, 3)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
		commitFiles(t, repo.Dir, "run.txt")
		r := scopeRun(repo)
		testutil.RecordGit(t, "2.43.0")
		r.narrowReviewRange(ctx)
		if r.narrowBase != "" {
			t.Fatalf("narrowBase = %q under git 2.43", r.narrowBase)
		}
		sc := r.rec.ReviewScope
		if sc == nil {
			t.Fatal("the record carries no review_scope")
		}
		if sc.Applicable || sc.WhyNot != runstore.ReviewScopeGitTooOld {
			t.Errorf("scope = applicable %v, why_not %q, want false and %q", sc.Applicable, sc.WhyNot, runstore.ReviewScopeGitTooOld)
		}
		if len(sc.ImplementFiles) != 0 || len(sc.ReviewFiles) != 0 || len(sc.Outside) != 0 {
			t.Errorf("scope compares %v against %v (outside %v), want no comparison at all", sc.ImplementFiles, sc.ReviewFiles, sc.Outside)
		}
	})

	t.Run("a first own commit with no parent", func(t *testing.T) {
		repo, _, _ := ownRangeRepo(t, 2)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "--orphan", "work")
		commitFiles(t, repo.Dir, "run.txt")
		r := scopeRun(repo)
		r.narrowReviewRange(ctx)
		if r.narrowBase != "" {
			t.Fatalf("narrowBase = %q, want the range kept", r.narrowBase)
		}
		sc := r.rec.ReviewScope
		if sc == nil {
			t.Fatal("the record carries no review_scope")
		}
		if sc.Applicable || sc.WhyNot != runstore.ReviewScopeOwnCommitsUnreadable {
			t.Errorf("scope = applicable %v, why_not %q, want false and %q", sc.Applicable, sc.WhyNot, runstore.ReviewScopeOwnCommitsUnreadable)
		}
		if len(sc.ImplementFiles) != 0 || len(sc.ReviewFiles) != 0 || len(sc.Outside) != 0 {
			t.Errorf("scope compares %v against %v (outside %v), want no comparison at all", sc.ImplementFiles, sc.ReviewFiles, sc.Outside)
		}
	})
}

// TestReviewScopeNotApplicableToFollowUpReviewAndAutofix: three runs record a
// reason instead of a comparison — a follow-up's range is the pull request's,
// which holds whoever else contributed to it, a review run has no implement
// stage, and a run whose check gate's commands.fix committed skipped
// implement entirely — so the comparison is recorded as not applicable and
// never as a mismatch. The first two read nothing at all: the range is the
// pull request's, so there is no narrowing to make a decision about.
//
// Mutation (leave Applicable true, or drop a reason, for any of them): this
// test fails. Mutation (return a reason for the autofix gate before the range
// is decided): the autofix subtest fails on narrowBase.
func TestReviewScopeNotApplicableToFollowUpReviewAndAutofix(t *testing.T) {
	ctx := context.Background()
	newRun := func() *run {
		return &run{d: Deps{Log: slog.New(slog.DiscardHandler)}, rec: &runstore.Record{Branch: "fugaro/r1"}}
	}
	for _, tc := range []struct {
		name, why string
		mutate    func(*run)
	}{
		{"a follow-up", runstore.ReviewScopeFollowUp, func(r *run) {
			r.follow = &followState{pr: gitprov.PRInfo{Base: "integration"}}
		}},
		{"a review run", runstore.ReviewScopeReviewRun, func(r *run) { r.review = true }},
	} {
		r := newRun()
		tc.mutate(r)
		r.narrowReviewRange(ctx)
		sc := r.rec.ReviewScope
		if sc == nil {
			t.Fatalf("%s: the record carries no review_scope", tc.name)
		}
		if sc.Applicable || sc.WhyNot != tc.why {
			t.Errorf("%s: scope = applicable %v, why_not %q, want false and %q", tc.name, sc.Applicable, sc.WhyNot, tc.why)
		}
		if len(sc.ImplementFiles) != 0 || len(sc.ReviewFiles) != 0 || len(sc.Outside) != 0 {
			t.Errorf("%s: scope compares %v against %v, want no comparison", tc.name, sc.ImplementFiles, sc.ReviewFiles)
		}
	}

	t.Run("an autofix gate that skipped implement", func(t *testing.T) {
		repo, _, tip := ownRangeRepo(t, 3)
		testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
		commitFiles(t, repo.Dir, "run.txt")
		r := scopeRun(repo)
		r.rec.Gate = "fixed"
		r.narrowReviewRange(ctx)
		if r.narrowBase != tip || r.narrowed() != tip {
			t.Fatalf("narrowBase = %q, narrowed() = %q, want the autofix run's range narrowed to %s", r.narrowBase, r.narrowed(), tip)
		}
		sc := r.rec.ReviewScope
		if sc == nil {
			t.Fatal("the record carries no review_scope")
		}
		if sc.Applicable || sc.WhyNot != runstore.ReviewScopeAutofix {
			t.Errorf("scope = applicable %v, why_not %q, want false and %q", sc.Applicable, sc.WhyNot, runstore.ReviewScopeAutofix)
		}
		if len(sc.ImplementFiles) != 0 || len(sc.ReviewFiles) != 0 || len(sc.Outside) != 0 {
			t.Errorf("scope compares %v against %v, want no comparison", sc.ImplementFiles, sc.ReviewFiles)
		}
	})
}
