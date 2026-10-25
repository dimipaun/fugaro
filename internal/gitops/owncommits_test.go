package gitops

import (
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestOwnCommitsAreTheOnesNoBranchOnOriginHolds: a run whose work starts on
// another branch than the one it was launched from (the agent fetched an
// integration branch to FETCH_HEAD and cut its own from it, as a
// single-branch clone leaves it no other way) made exactly the commits HEAD
// has and no branch on origin has — except the run's own branch, which its
// checkpoint pushes fill with those very commits. "On origin" is origin's
// own answer, never a remote-tracking ref the agent can write.
//
// Mutation (read --remotes=origin instead of ls-remote): the FETCH_HEAD
// case counts integration's commits as the run's, and the forged ref hides
// the run's own. Mutation (keep the run's own branch): the pushed case finds
// no commit of the run's own.
func TestOwnCommitsAreTheOnesNoBranchOnOriginHolds(t *testing.T) {
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

	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "refs/heads/integration")
	testutil.Git(t, repo.Dir, "update-ref", "-d", "refs/remotes/origin/integration")
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "FETCH_HEAD")
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
	// A remote-tracking ref the agent wrote itself, with no push, is not a
	// branch on origin: it hides nothing.
	testutil.Git(t, repo.Dir, "update-ref", "refs/remotes/origin/decoy", mine[1])
	if got, err := repo.OwnCommits(ctx, "fugaro/x"); err != nil || !slices.Equal(got, mine) {
		t.Fatalf("OwnCommits with a forged origin/decoy = %v, %v; want all three: %v", got, err, mine)
	}
	// The run pushed its branch: origin's fugaro/x holds every commit, and
	// they are still the run's own.
	testutil.Git(t, repo.Dir, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/x")
	if got, err := repo.OwnCommits(ctx, "fugaro/x"); err != nil || !slices.Equal(got, mine) {
		t.Fatalf("OwnCommits after the run's own push = %v, %v; want %v", got, err, mine)
	}
	// A real branch on origin holding the first two makes them its own.
	testutil.Git(t, repo.Dir, "push", "--quiet", "origin", mine[1]+":refs/heads/other")
	if got, err := repo.OwnCommits(ctx, "fugaro/x"); err != nil || !slices.Equal(got, mine[2:]) {
		t.Fatalf("OwnCommits with origin's other at the second commit = %v, %v; want %v", got, err, mine[2:])
	}

	if p, err := repo.FirstParent(ctx, mine[0]); err != nil || p != integrationTip {
		t.Fatalf("FirstParent of the first own commit = %q, %v; want the integration tip %s", p, err, integrationTip)
	}
	root := testutil.Git(t, repo.Dir, "rev-list", "--max-parents=0", "HEAD")
	if p, err := repo.FirstParent(ctx, root); err != nil || p != "" {
		t.Fatalf("FirstParent of a root commit = %q, %v; want none", p, err)
	}
	if _, err := repo.OwnCommits(ctx, ""); err == nil {
		t.Fatal("OwnCommits accepted no run branch")
	}
	if _, err := repo.FirstParent(ctx, "HEAD"); err == nil {
		t.Fatal("FirstParent accepted a ref name, want a full commit SHA")
	}
}

// TestOriginHeadsSkipsWhatTheCheckoutLacksAndFailsWithoutOrigin: a branch
// on origin whose tip was never fetched is left out (and not fetched), and
// an origin that cannot be read is an error — the caller then narrows
// nothing — never a quiet fallback to the checkout's own refs.
func TestOriginHeadsSkipsWhatTheCheckoutLacksAndFailsWithoutOrigin(t *testing.T) {
	repo, remote := setup(t)
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	if err := (&Repo{Dir: seed, Env: IdentityEnv()}).CommitEmpty(ctx, "unfetched"); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/unfetched")
	unfetched := testutil.Git(t, seed, "rev-parse", "HEAD")
	main := testutil.Git(t, repo.Dir, "rev-parse", "origin/main")
	heads, err := repo.OriginHeads(ctx, "fugaro/x")
	if err != nil || !slices.Equal(heads, []string{main}) {
		t.Fatalf("OriginHeads = %v, %v; want main's tip alone, not the unfetched %s", heads, err, unfetched)
	}
	if exec.Command("git", "-C", repo.Dir, "cat-file", "-e", unfetched+"^{commit}").Run() == nil {
		t.Fatal("reading origin's heads fetched a commit")
	}
	testutil.Git(t, repo.Dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone"))
	if _, err := repo.OwnCommits(ctx, "fugaro/x"); err == nil {
		t.Fatal("OwnCommits answered with origin unreadable")
	}
}
