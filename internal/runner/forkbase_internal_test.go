package runner

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestPickForkBaseTies: the nearest wins; at equal distance the run's own
// ref wins, else the first name (NearestBranches sorts by distance, then by
// name); no candidate is no choice.
//
// Mutation (drop the fallback preference, or return the last tip): a case
// below fails.
func TestPickForkBaseTies(t *testing.T) {
	tip := func(name string, ahead int) gitops.BranchTip { return gitops.BranchTip{Name: name, Ahead: ahead} }
	for _, c := range []struct {
		name     string
		tips     []gitops.BranchTip
		fallback string
		want     string
	}{
		{"nearest wins", []gitops.BranchTip{tip("integration", 2), tip("main", 9)}, "main", "integration"},
		{"a tie goes to the run's ref", []gitops.BranchTip{tip("alpha", 2), tip("main", 2)}, "main", "main"},
		{"a tie without the ref goes to the first name", []gitops.BranchTip{tip("alpha", 2), tip("beta", 2), tip("main", 5)}, "main", "alpha"},
	} {
		got, ok := pickForkBase(c.tips, c.fallback)
		if !ok || got.Name != c.want {
			t.Errorf("%s: pickForkBase = %+v, %v; want %s", c.name, got, ok, c.want)
		}
	}
	if got, ok := pickForkBase(nil, "main"); ok {
		t.Errorf("pickForkBase of nothing = %+v, want no choice", got)
	}
}

// forkRig is a checkout of a remote holding main and an integration branch
// cut from it with a commit of its own, and a first run cut from main —
// after main moved on past the integration branch's fork point, when
// mainMoved says so.
type forkRig struct {
	r            *run
	remote, work string
	integ        string
}

func newForkRig(t *testing.T, mainMoved ...bool) *forkRig {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.Git(t, seed, "commit", "--quiet", "--allow-empty", "-m", "integration's own")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/integration")
	integ := testutil.Git(t, seed, "rev-parse", "HEAD")
	if len(mainMoved) > 0 && mainMoved[0] {
		testutil.Git(t, seed, "reset", "--quiet", "--hard", "origin/main")
		testutil.Git(t, seed, "commit", "--quiet", "--allow-empty", "-m", "main moves on")
		testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	}
	work := filepath.Join(t.TempDir(), "work")
	repo, err := gitops.OpenOrClone(context.Background(), work, remote, gitops.IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(context.Background(), "main", "fugaro/r1"); err != nil {
		t.Fatal(err)
	}
	start, err := repo.HeadSHA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := &run{
		d:        Deps{Log: slog.New(slog.DiscardHandler)},
		spec:     &task.Spec{Ref: "main"},
		cfg:      &config.Config{Git: config.Git{BaseBranch: "main"}},
		rec:      &runstore.Record{Branch: "fugaro/r1"},
		repo:     repo,
		startSHA: start,
	}
	return &forkRig{r: r, remote: remote, work: work, integ: integ}
}

func (g *forkRig) git(t *testing.T, args ...string) string {
	t.Helper()
	return testutil.Git(t, g.work, args...)
}

func (g *forkRig) commit(t *testing.T, msg string) {
	t.Helper()
	g.git(t, "commit", "--quiet", "--allow-empty", "-m", msg)
}

// TestNearestBaseOnAHermeticRepo is the decision itself, case by case, on a
// real checkout: the integration branch the work moved onto is chosen over
// the ref the run was cut from; a run that stayed on its ref keeps it; a
// branch the run merged in is its base only when it holds the commit the
// run was cut from; a branch that is not in the
// run's history, and the run's own pushed branch, are never chosen.
//
// Mutation (drop the start-commit rule, the run-branch filter or the
// ancestor check — gitops.NearestBranches's isAncestor): a case below fails.
func TestNearestBaseOnAHermeticRepo(t *testing.T) {
	ctx := context.Background()

	t.Run("work moved onto the integration branch", func(t *testing.T) {
		g := newForkRig(t)
		g.git(t, "checkout", "--quiet", "-B", "work", "origin/integration")
		g.commit(t, "the run's own")
		if got, _ := g.r.nearestBase(ctx); got != "integration" {
			t.Fatalf("nearestBase = %q, want integration", got)
		}
	})

	t.Run("a run that stayed on its ref keeps it", func(t *testing.T) {
		g := newForkRig(t)
		g.commit(t, "the run's own")
		if got, _ := g.r.nearestBase(ctx); got != "main" {
			t.Fatalf("nearestBase = %q, want main", got)
		}
	})

	t.Run("no commits of its own keeps the ref", func(t *testing.T) {
		g := newForkRig(t)
		g.git(t, "checkout", "--quiet", "-B", "work", "origin/integration")
		g.git(t, "checkout", "--quiet", "fugaro/r1")
		if got, _ := g.r.nearestBase(ctx); got != "main" {
			t.Fatalf("nearestBase = %q, want main", got)
		}
	})

	t.Run("a branch merged in that lacks the run's start is not its base", func(t *testing.T) {
		g := newForkRig(t, true)
		// Integration carries more commits of its own than main has since
		// the fork, so by distance alone it would be the nearer one.
		seed := filepath.Join(t.TempDir(), "more")
		testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", g.remote, seed)
		testutil.Git(t, seed, "checkout", "--quiet", "-B", "x", "origin/integration")
		testutil.Git(t, seed, "commit", "--quiet", "--allow-empty", "-m", "integration's second")
		testutil.Git(t, seed, "commit", "--quiet", "--allow-empty", "-m", "integration's third")
		testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/integration")
		g.git(t, "fetch", "--quiet", "origin")
		g.commit(t, "the run's own")
		g.git(t, "merge", "--quiet", "--no-edit", "origin/integration")
		if got, _ := g.r.nearestBase(ctx); got != "main" {
			t.Fatalf("nearestBase = %q, want main: measured against integration, main's own commit would read as the run's", got)
		}
	})

	t.Run("work moved onto an integration branch main has moved past", func(t *testing.T) {
		g := newForkRig(t, true)
		g.git(t, "checkout", "--quiet", "-B", "work", "origin/integration")
		g.commit(t, "the run's own")
		if got, _ := g.r.nearestBase(ctx); got != "integration" {
			t.Fatalf("nearestBase = %q, want integration", got)
		}
	})

	// An integration branch that holds the run's start commit and was merged
	// in, rather than moved onto, is the base too: the history cannot tell
	// the two apart, and measured against it the diff is still exactly the
	// run's own change (the matrix's cell (d) is this shape on --ref).
	t.Run("an up-to-date branch merged in is the base", func(t *testing.T) {
		g := newForkRig(t)
		g.commit(t, "the run's own")
		g.git(t, "merge", "--quiet", "--no-edit", "origin/integration")
		if got, _ := g.r.nearestBase(ctx); got != "integration" {
			t.Fatalf("nearestBase = %q, want integration", got)
		}
	})

	t.Run("a branch not in the run's history is never chosen", func(t *testing.T) {
		g := newForkRig(t)
		g.git(t, "checkout", "--quiet", "-B", "work", "origin/integration")
		g.commit(t, "the run's own")
		// A branch one commit ahead of integration is "near" by any count
		// that ignores ancestry, and is not where the work came from.
		seed := filepath.Join(t.TempDir(), "ahead")
		testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", g.remote, seed)
		testutil.Git(t, seed, "checkout", "--quiet", "-B", "x", "origin/integration")
		testutil.Git(t, seed, "commit", "--quiet", "--allow-empty", "-m", "someone else's")
		testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/aaa-ahead")
		g.git(t, "fetch", "--quiet", "origin")
		if got, _ := g.r.nearestBase(ctx); got != "integration" {
			t.Fatalf("nearestBase = %q, want integration", got)
		}
	})

	t.Run("the run's own pushed branch is never chosen", func(t *testing.T) {
		g := newForkRig(t)
		g.git(t, "checkout", "--quiet", "-B", "work", "origin/integration")
		g.commit(t, "the run's own")
		g.commit(t, "the run's own, again")
		// A checkpoint pushed the run's branch one commit back: the nearest
		// ancestor of all, and the one that would hide the run's own work.
		g.git(t, "push", "--quiet", "origin", "HEAD~1:refs/heads/fugaro/r1")
		g.git(t, "push", "--quiet", "origin", "HEAD~1:refs/heads/fugaro/other")
		g.git(t, "fetch", "--quiet", "origin")
		if got, _ := g.r.nearestBase(ctx); got != "integration" {
			t.Fatalf("nearestBase = %q, want integration", got)
		}
	})
}

// TestSettledForkBaseIsEveryDiffsBase: once settled, diffBase — the review
// prompt's range, the review input, the follow-up's stat — and the guard's
// base are the same branch, and a later change to the history does not move
// it. A follow-up is never touched: its base is its pull request's.
//
// Mutation (let guardDiffBase recompute after settling, or let
// settleForkBase run for a follow-up): this test fails.
func TestSettledForkBaseIsEveryDiffsBase(t *testing.T) {
	ctx := context.Background()
	g := newForkRig(t)
	g.git(t, "checkout", "--quiet", "-B", "work", "origin/integration")
	g.commit(t, "the run's own")
	if got := g.r.guardDiffBase(ctx); got != "integration" {
		t.Fatalf("before settling, guardDiffBase = %q, want integration", got)
	}
	if got := g.r.diffBase(); got != "main" {
		t.Fatalf("before settling, diffBase = %q, want the run's ref main", got)
	}
	g.r.settleForkBase(ctx)
	g.git(t, "checkout", "--quiet", "fugaro/r1")
	for name, got := range map[string]string{
		"diffBase": g.r.diffBase(), "reviewPromptBase": g.r.reviewPromptBase(), "guardDiffBase": g.r.guardDiffBase(ctx),
	} {
		if got != "integration" {
			t.Errorf("after settling, %s = %q, want integration", name, got)
		}
	}

	f := newForkRig(t)
	f.r.follow = &followState{pr: gitprov.PRInfo{Base: "main"}}
	f.git(t, "checkout", "--quiet", "-B", "work", "origin/integration")
	f.commit(t, "the follow-up's own")
	f.r.settleForkBase(ctx)
	if got := f.r.diffBase(); got != "main" {
		t.Errorf("a follow-up's diffBase = %q, want its pull request's base main", got)
	}
	if got := f.r.guardDiffBase(ctx); got != "main" {
		t.Errorf("a follow-up's guardDiffBase = %q, want its pull request's base main", got)
	}
}
