package gitops

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// pushBranch commits one empty commit on top of from in a scratch clone of
// remote and pushes it as name.
func pushBranch(t *testing.T, remote, from, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", remote, dir)
	testutil.Git(t, dir, "checkout", "--quiet", "-B", "seed", "origin/"+from)
	testutil.Git(t, dir, "commit", "--quiet", "--allow-empty", "-m", "a commit on "+name)
	testutil.Git(t, dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+name)
	return testutil.Git(t, dir, "rev-parse", "HEAD")
}

// TestNearestBranches: of origin's branches whose remote-tracking tip is in
// HEAD's history, only the nearest are returned — a branch whose tip another
// candidate's tip contains is never nearer — each with the commits HEAD has
// beyond it; a branch whose tip is not an ancestor never is one, nor one the
// caller's filter refuses, nor a tracking ref whose branch origin has deleted.
//
// Mutation (drop the ancestor check, the independence pruning, the filter or
// the ls-remote check): the matching assertion below fails.
func TestNearestBranches(t *testing.T) {
	repo, remote := setup(t)
	integ := pushBranch(t, remote, "main", "integration")
	pushBranch(t, remote, "integration", "unrelated")               // ahead of integration: not an ancestor
	testutil.Git(t, remote, "update-ref", "refs/heads/twin", integ) // a tie at integration's tip
	testutil.Git(t, remote, "update-ref", "refs/heads/fugaro/r1", integ)
	testutil.Git(t, remote, "update-ref", "refs/heads/gone", integ) // deleted below: a stale tracking ref
	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin")
	testutil.Git(t, remote, "update-ref", "-d", "refs/heads/gone")
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-B", "work", "origin/integration")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "--allow-empty", "-m", "the run's own")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "--allow-empty", "-m", "the run's own, again")
	head, err := repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keep := func(name string) bool { return name != "fugaro/r1" }
	got, err := repo.NearestBranches(ctx, head, keep)
	if err != nil {
		t.Fatal(err)
	}
	want := []BranchTip{{Name: "integration", SHA: integ, Ahead: 2}, {Name: "twin", SHA: integ, Ahead: 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("NearestBranches = %+v, want %+v", got, want)
	}

	// Nothing that is an ancestor: none at all, and no error.
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "--orphan", "lonely")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "--allow-empty", "-m", "no history in common")
	lonely, _ := repo.HeadSHA(ctx)
	if got, err := repo.NearestBranches(ctx, lonely, nil); err != nil || len(got) != 0 {
		t.Fatalf("NearestBranches of an unrelated head = %+v, %v; want none", got, err)
	}
	if _, err := repo.NearestBranches(ctx, "HEAD", nil); err == nil {
		t.Error("NearestBranches took a head that is not a full SHA")
	}
}
