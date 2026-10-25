package gitops

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestOwnCommitsAreTheOnesNoOtherOriginBranchHolds: a run whose work starts
// on another branch than the one it was launched from (the agent fetched an
// integration branch and cut its own from it) made exactly the commits HEAD
// has and no branch on origin has — except the run's own branch, which its
// checkpoint pushes fill with those very commits.
//
// Mutation (drop the --exclude of the run's own branch): the pushed case
// below finds no commit of the run's own.
func TestOwnCommitsAreTheOnesNoOtherOriginBranchHolds(t *testing.T) {
	repo, remote := setup(t)
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	for _, f := range []string{"one.txt", "two.txt"} {
		testutil.WriteFiles(t, seed, map[string]string{f: f + "\n"})
		testutil.Git(t, seed, "add", "-A")
		testutil.Git(t, seed, "commit", "--quiet", "-m", "integration's "+f)
	}
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/integration")
	integrationTip := testutil.Git(t, seed, "rev-parse", "HEAD")

	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "+refs/heads/integration:refs/remotes/origin/integration")
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/integration")
	var mine []string
	for _, m := range []string{"a", "b", "c"} {
		if err := repo.CommitEmpty(ctx, "the run's "+m); err != nil {
			t.Fatal(err)
		}
		sha, err := repo.HeadSHA(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mine = append(mine, sha)
	}

	got, err := repo.OwnCommits(ctx, "fugaro/x")
	if err != nil || !slices.Equal(got, mine) {
		t.Fatalf("OwnCommits = %v, %v; want the run's three, oldest first: %v", got, err, mine)
	}
	// The run pushed its branch: origin/fugaro/x holds every commit, and
	// they are still the run's own.
	testutil.Git(t, repo.Dir, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "+refs/heads/fugaro/x:refs/remotes/origin/fugaro/x")
	if got, err := repo.OwnCommits(ctx, "fugaro/x"); err != nil || !slices.Equal(got, mine) {
		t.Fatalf("OwnCommits after the run's own push = %v, %v; want %v", got, err, mine)
	}
	// Another branch on origin holding the first two makes them its own,
	// not the run's: what a remote-tracking ref holds is never counted.
	testutil.Git(t, repo.Dir, "update-ref", "refs/remotes/origin/other", mine[1])
	if got, err := repo.OwnCommits(ctx, "fugaro/x"); err != nil || !slices.Equal(got, mine[2:]) {
		t.Fatalf("OwnCommits with another branch at the second commit = %v, %v; want %v", got, err, mine[2:])
	}

	if p, err := repo.FirstParent(ctx, mine[0]); err != nil || p != integrationTip {
		t.Fatalf("FirstParent of the first own commit = %q, %v; want the integration tip %s", p, err, integrationTip)
	}
	root := testutil.Git(t, repo.Dir, "rev-list", "--max-parents=0", "HEAD")
	if p, err := repo.FirstParent(ctx, root); err != nil || p != "" {
		t.Fatalf("FirstParent of a root commit = %q, %v; want none", p, err)
	}
	if _, err := repo.OwnCommits(ctx, "fugaro/*"); err == nil {
		t.Fatal("OwnCommits accepted a run branch that is a pattern")
	}
	if _, err := repo.FirstParent(ctx, "HEAD"); err == nil {
		t.Fatal("FirstParent accepted a ref name, want a full commit SHA")
	}
}
