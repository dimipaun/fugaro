package gitops

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// TestOwnCommitsCostOneProcessPerStepWhateverTheBranchCount: with 250
// branches on origin, reading the run's own commits is one ls-remote, one
// cat-file for every head's presence and one rev-list fed the heads on
// stdin — never a process per branch, and never the heads on the command
// line, where ARG_MAX caps them — and asking whether origin holds a commit
// is one rev-list more.
//
// Mutation (go back to a cat-file -e per head, or a merge-base per head in
// HeldBy): the counts below fail.
func TestOwnCommitsCostOneProcessPerStepWhateverTheBranchCount(t *testing.T) {
	repo, remote := setup(t)
	main := testutil.ManyBranches(t, remote, 250)
	testutil.Git(t, repo.Dir, "fetch", "--quiet", "origin", "+refs/heads/*:refs/remotes/origin/*")
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "-b", "work", "origin/b007")
	b007 := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	var mine []string
	for _, m := range []string{"a", "b"} {
		if err := repo.CommitEmpty(ctx, "the run's "+m); err != nil {
			t.Fatal(err)
		}
		mine = append(mine, testutil.Git(t, repo.Dir, "rev-parse", "HEAD"))
	}

	calls := testutil.RecordGit(t, "")
	got, err := repo.OwnCommits(ctx, "fugaro/x")
	if err != nil || !slices.Equal(got, mine) {
		t.Fatalf("OwnCommits = %v, %v; want the run's two: %v", got, err, mine)
	}
	lines := testutil.GitCalls(t, calls)
	if n := testutil.GitSubcommands(lines); len(lines) != 3 || n["ls-remote"] != 1 || n["cat-file"] != 1 || n["rev-list"] != 1 {
		t.Fatalf("OwnCommits over 250 branches ran %d git processes (%v), want one ls-remote, one cat-file and one rev-list", len(lines), n)
	}

	heads, err := repo.OriginHeads(ctx, "fugaro/x")
	if err != nil || len(heads) != 251 {
		t.Fatalf("OriginHeads = %d heads, %v; want 250 branches and main", len(heads), err)
	}
	testutil.GitCalls(t, calls)
	for _, c := range []struct {
		commit string
		want   bool
	}{{b007, true}, {main, true}, {mine[0], false}, {mine[1], false}} {
		held, err := repo.HeldBy(ctx, c.commit, heads)
		if err != nil || held != c.want {
			t.Errorf("HeldBy(%s) = %v, %v; want %v", c.commit, held, err, c.want)
		}
		if lines := testutil.GitCalls(t, calls); len(lines) != 1 || testutil.GitSubcommands(lines)["rev-list"] != 1 {
			t.Errorf("HeldBy over 251 heads ran %v, want one rev-list", lines)
		}
	}
	if held, err := repo.HeldBy(ctx, b007, nil); err != nil || held {
		t.Errorf("HeldBy with no heads = %v, %v; want false", held, err)
	}
	if _, err := repo.HeldBy(ctx, "HEAD", heads); err == nil {
		t.Error("HeldBy accepted a ref name, want a full commit SHA")
	}
	// A full SHA that names no commit is never held: rev-list walks no
	// commit from a tree or a blob and counts 0, which reads as "held".
	tree := testutil.Git(t, repo.Dir, "rev-parse", "HEAD^{tree}")
	blob := testutil.Git(t, repo.Dir, "rev-parse", "HEAD:README.md")
	for _, c := range []string{tree, blob, strings.Repeat("e", 40)} {
		if held, err := repo.HeldBy(ctx, c, heads); err == nil || held {
			t.Errorf("HeldBy(%s, not a commit here) = %v, %v; want an error", c, held, err)
		}
	}
}

// TestNoLazyFetchNeedsGit244: GIT_NO_LAZY_FETCH, which keeps OriginHeads
// from fetching every missing branch's commit, exists from git 2.44 on; an
// older git ignores it.
func TestNoLazyFetchNeedsGit244(t *testing.T) {
	for v, want := range map[string]bool{
		"git version 2.44.0":                 true,
		"git version 2.47.3":                 true,
		"git version 3.0.0":                  true,
		"git version 2.55.0.windows.1":       true,
		"git version 2.43.0":                 false,
		"git version 2.39.5 (Apple Git-154)": false,
		"git version 1.99.9":                 false,
		"":                                   false,
		"not git":                            false,
		"git version two":                    false,
	} {
		if got := NoLazyFetch(v); got != want {
			t.Errorf("NoLazyFetch(%q) = %v, want %v", v, got, want)
		}
	}
	repo, _ := setup(t)
	testutil.RecordGit(t, "2.43.0")
	if v, err := repo.GitVersion(ctx); err != nil || v != "git version 2.43.0" || NoLazyFetch(v) {
		t.Errorf("GitVersion under a 2.43 git = %q, %v", v, err)
	}
}
