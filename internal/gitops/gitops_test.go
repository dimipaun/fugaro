package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// TestTrackedPathsAnswersForTheIndexNotEmptyTheDirectory: the question the
// runner asks of a directory a run writes into is "did the repository put
// anything here?", and the working tree cannot answer it — an untracked or
// ignored file in that directory is not the repository's. Only the index
// knows, and a directory the index has nothing under is not an error.
//
// Mutation (answer from the working tree instead, e.g. walk dir): stales.txt
// and build/x.js come back as tracked, and this test fails on both.
func TestTrackedPathsAnswersForTheIndexNotEmptyTheDirectory(t *testing.T) {
	repo, _ := setup(t)
	if got, err := repo.TrackedPaths(ctx, "inputs"); err != nil || len(got) != 0 {
		t.Fatalf("TrackedPaths(inputs) = %v, %v, want none and no error", got, err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"inputs/committed.txt": "the repository's own\n",
		"inputs/stales.txt":    "left by a run, never committed\n",
		"build/x.js":           "ignored\n",
		"inputs/sub/deep.txt":  "nested\n",
	})
	testutil.Git(t, repo.Dir, "add", "inputs/committed.txt", "inputs/sub/deep.txt")
	testutil.Git(t, repo.Dir, "commit", "-qm", "inputs")
	got, err := repo.TrackedPaths(ctx, "inputs")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"inputs/committed.txt", "inputs/sub/deep.txt"}; len(got) != len(want) {
		t.Fatalf("TrackedPaths(inputs) = %v, want %v", got, want)
	}
	for _, p := range got {
		if !strings.HasPrefix(p, "inputs/") {
			t.Errorf("TrackedPaths returned %q, want repository-root-relative paths", p)
		}
	}
	if _, err := repo.TrackedPaths(ctx, "../etc"); err == nil {
		t.Error("a path with .. was accepted")
	}
}

// TestCheckoutDetachedKeepsIgnored: review mode's single-pull-request
// checkout (design generic-tool §3.6, G12). It fetches by SHA alone (no
// branch name, as a PR's commit may belong to another repository's fork)
// and lands with a detached HEAD at exactly that commit, the same ignored-
// file and untracked-file behaviour as CheckoutNewBranch.
//
// Mutation (drop --detach from the checkout args): this still passes on a
// bare rev, since git then creates a local branch instead, UNLESS asked to
// check out a commit with no branch pointing at it (a second commit after
// the fetch, below): then "checkout" alone resolves FETCH_HEAD to that
// branch's current tip, not the fetched commit, and the HEAD assertion
// below fails.
func TestCheckoutDetachedKeepsIgnored(t *testing.T) {
	repo, remote := setup(t)
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"node_modules/dep/index.js": "warm cache\n",
		"stray.txt":                 "untracked\n",
	})
	sha := testutil.Git(t, remote, "rev-parse", "refs/heads/main")
	if err := repo.CheckoutDetached(ctx, sha); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, repo.Dir, "rev-parse", "HEAD"); got != sha {
		t.Fatalf("HEAD = %s, want %s", got, sha)
	}
	if got := testutil.Git(t, repo.Dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Fatalf("branch = %q, want a detached HEAD", got)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "stray.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file survived the checkout")
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "node_modules", "dep", "index.js")); err != nil {
		t.Fatal("ignored warm cache was wiped:", err)
	}
}

// TestCheckoutDetachedRefusesAnythingButAFullHash: review mode's SHA comes
// straight from a provider's own PRHead.FetchPRHead (gitprov), reaching
// this function's rev argument unvalidated (checkReviewPRs' own check is
// the first line of defense, not the only one). git itself accepts an
// option in the position a positional revision goes, so a rev of
// "--upload-pack=touch /tmp/x;git-upload-pack" is executed as a shell
// command by `git fetch` — reproduced independently before this fix (a
// throwaway test, not kept): the injected command's marker file existed
// afterward. An abbreviated hash (Bitbucket's own pull-request API gives a
// 12-hex one) is refused too, with a clear error, rather than reaching
// `git fetch` to fail there with "couldn't find remote ref".
//
// Mutation (drop the `if !ValidCommitSHA(rev)` check from CheckoutDetached):
// this test fails for both cases: the option-injection case, by actually
// running the injected command (the marker file would exist); the
// abbreviated-hash case, by reaching git's own, less clear failure instead
// of refusing up front.
func TestCheckoutDetachedRefusesAnythingButAFullHash(t *testing.T) {
	repo, _ := setup(t)
	marker := filepath.Join(t.TempDir(), "injected")
	injection := "--upload-pack=touch " + marker + ";git-upload-pack"
	if err := repo.CheckoutDetached(ctx, injection); err == nil {
		t.Fatal("want an error for an option-injection rev, got none")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the injected command ran: the marker file exists")
	}
	if err := repo.CheckoutDetached(ctx, "abc1234"); err == nil {
		t.Fatal("want an error for an abbreviated hash, got none")
	}
}

// TestWorktreeAddsDetachedCopy: review mode's pick case, one directory per
// pull request it compares (design generic-tool §3.6, G12). The main
// checkout is left exactly where it was: only the new worktree moves.
func TestWorktreeAddsDetachedCopy(t *testing.T) {
	repo, remote := setup(t)
	before := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	testutil.Git(t, remote, "update-ref", "refs/heads/other", "refs/heads/main")
	seed := filepath.Join(t.TempDir(), "seed2")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{"other.txt": "from another attempt\n"})
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "other attempt")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/other")
	sha := testutil.Git(t, remote, "rev-parse", "refs/heads/other")

	dir := filepath.Join(t.TempDir(), "review", "9")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := repo.Worktree(ctx, sha, dir); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, dir, "rev-parse", "HEAD"); got != sha {
		t.Fatalf("worktree HEAD = %s, want %s", got, sha)
	}
	if _, err := os.Stat(filepath.Join(dir, "other.txt")); err != nil {
		t.Fatal("worktree is missing the other attempt's file:", err)
	}
	if got := testutil.Git(t, repo.Dir, "rev-parse", "HEAD"); got != before {
		t.Fatalf("main checkout moved to %s, want %s", got, before)
	}
}

// TestGitDirAsksGitForTheGitDirectory: GitDir exists so a caller that
// writes git's own state -- an artifact run appends inputs/ to the exclude
// file (design artifact-runs.md §4 step 5) -- can find the directory without
// assuming it is <Dir>/.git, and a linked worktree is precisely where that
// assumption breaks: there .git is a regular FILE naming a git directory
// somewhere else entirely. The worktree half is the one a .git-join cannot
// pass; the main-checkout half pins that GitDir is at least the same
// directory, so a change that answered for worktrees and not the ordinary
// case does not slip through on the interesting half alone.
//
// Note for the caller, not for this test: what git READS for the exclude is
// $(git rev-parse --git-common-dir)/info/exclude, and in a worktree that is
// the main repository's .git, not the directory GitDir answers with (checked
// by hand: a line written to a worktree's own info/exclude leaves inputs/
// untracked, the same line in the common dir's does not; git does not even
// create info/ under the per-worktree directory). That is harmless here
// because only review mode ever makes a worktree (internal/runner/
// reviewmode.go) and an artifact run never runs in one, so the exclude
// GitDir names is the one git consults -- see excludeInputsFromGit.
//
// Mutation (replace GitDir's body with filepath.Join(r.Dir, ".git")): the
// worktree half fails -- it gets the .git file, which is not a directory.
func TestGitDirAsksGitForTheGitDirectory(t *testing.T) {
	repo, _ := setup(t)
	got, err := repo.GitDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := realPath(t, filepath.Join(repo.Dir, ".git"))
	if realPath(t, got) != want {
		t.Fatalf("GitDir = %s, want the checkout's git directory %s", got, want)
	}
	if st, err := os.Stat(got); err != nil || !st.IsDir() {
		t.Fatalf("GitDir %s: %v (dir=%v), want a directory", got, err, err == nil && st.IsDir())
	}

	sha := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	dir := filepath.Join(t.TempDir(), "review", "9")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := repo.Worktree(ctx, sha, dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Mode().IsRegular() {
		t.Fatalf("a worktree's .git is %s, want a regular file naming the git directory", st.Mode())
	}
	wt, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotWT, err := wt.GitDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gotWT == filepath.Join(dir, ".git") || gotWT == realPath(t, filepath.Join(dir, ".git")) {
		t.Fatalf("GitDir = %s, want the git directory the .git file names, not .git itself", gotWT)
	}
	if gotWT == realPath(t, filepath.Join(repo.Dir, ".git")) {
		t.Fatalf("GitDir = %s, want the worktree's own git directory, not the main repository's", gotWT)
	}
	if st, err := os.Stat(gotWT); err != nil || !st.IsDir() {
		t.Fatalf("GitDir %s: %v, want a directory", gotWT, err)
	}
	if _, err := os.Stat(filepath.Join(gotWT, "HEAD")); err != nil {
		t.Fatalf("GitDir %s holds no HEAD: %v", gotWT, err)
	}
}

// realPath resolves symlinks, so a comparison of two names for one directory
// does not fail on the temporary directories a test runs under (on macOS
// /var is a symlink to /private/var).
func realPath(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWorktreeRefusesAnythingButAFullHash: the same rev-validation
// TestCheckoutDetachedRefusesAnythingButAFullHash proves for the
// single-pull-request case, for pick's per-pull-request worktree instead.
// The injected command's marker lives under t.TempDir(), not a fixed path
// like /tmp/x: under the mutation below, the injected `touch` would
// otherwise run against the real host's /tmp, not a path this test owns.
//
// Mutation (drop the `if !ValidCommitSHA(rev)` check from Worktree): this
// test fails, finding no error for either an option-injection rev or an
// abbreviated hash.
func TestWorktreeRefusesAnythingButAFullHash(t *testing.T) {
	repo, _ := setup(t)
	dir := filepath.Join(t.TempDir(), "review", "9")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "injected")
	injection := "--upload-pack=touch " + marker + ";git-upload-pack"
	if err := repo.Worktree(ctx, injection, dir); err == nil {
		t.Fatal("want an error for an option-injection rev, got none")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the injected command ran: the marker file exists")
	}
	if err := repo.Worktree(ctx, "abc1234", dir); err == nil {
		t.Fatal("want an error for an abbreviated hash, got none")
	}
}

// TestWorktreeReusesExistingDir: a pick run's review/<N> directory is not
// part of the image or clone that gets reset between runs the way
// r.d.WorkDir itself is (reviewDir's own doc), and nothing removes it after
// a run — so a retried execution, or a later pick run comparing the same
// pull request number again, finds the directory (and its worktree
// registration) left over from before. `git worktree add` fails outright
// if the target directory already exists; Worktree must handle this the
// same way CheckoutDetached's force checkout already handles reusing
// r.d.WorkDir itself, not leave it to error out as infra_error.
func TestWorktreeReusesExistingDir(t *testing.T) {
	repo, remote := setup(t)
	testutil.Git(t, remote, "update-ref", "refs/heads/other", "refs/heads/main")
	seed := filepath.Join(t.TempDir(), "seed3")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{"first.txt": "first attempt\n"})
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "first attempt")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/other")
	firstSHA := testutil.Git(t, remote, "rev-parse", "refs/heads/other")

	dir := filepath.Join(t.TempDir(), "review", "9")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := repo.Worktree(ctx, firstSHA, dir); err != nil {
		t.Fatal(err)
	}

	testutil.WriteFiles(t, seed, map[string]string{"first.txt": "second attempt\n"})
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "second attempt")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/other")
	secondSHA := testutil.Git(t, remote, "rev-parse", "refs/heads/other")

	// dir, and the worktree Worktree just registered for it, are both left
	// exactly as the first call made them — nothing removes either, the
	// same as the real runner never does between runs on a warm container.
	if err := repo.Worktree(ctx, secondSHA, dir); err != nil {
		t.Fatalf("second Worktree call on the same dir failed: %v", err)
	}
	if got := testutil.Git(t, dir, "rev-parse", "HEAD"); got != secondSHA {
		t.Fatalf("worktree HEAD = %s, want %s (the second attempt)", got, secondSHA)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "first.txt")); err != nil || string(got) != "second attempt\n" {
		t.Fatalf("first.txt = %q, %v, want the second attempt's content", got, err)
	}
}

// TestResetToRevertsTrackedRemovesUntrackedKeepsIgnored: a tracked file's
// modification is reverted, an untracked file is removed, and an ignored
// file (a warm dependency cache) survives — the same guarantee
// CheckoutNewBranch's own clean -fd gives a fresh checkout, used instead
// when a command's output (gate.go: commands.fix) cannot be trusted.
func TestResetToRevertsTrackedRemovesUntrackedKeepsIgnored(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	head, err := repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"README.md":                 "half-written\n",
		"half.txt":                  "untracked\n",
		"node_modules/dep/index.js": "warm cache\n",
	})
	if err := repo.ResetTo(ctx, head); err != nil {
		t.Fatal(err)
	}
	if clean, err := repo.IsClean(ctx); err != nil || !clean {
		t.Fatalf("IsClean after ResetTo = %v, %v", clean, err)
	}
	if data, err := os.ReadFile(filepath.Join(repo.Dir, "README.md")); err != nil || string(data) != "hello\n" {
		t.Fatalf("README.md = %q, %v, want it reverted to \"hello\\n\"", data, err)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "half.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file survived ResetTo")
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "node_modules", "dep", "index.js")); err != nil {
		t.Fatal("ignored warm cache was wiped:", err)
	}
}

// TestResetToDiscardsACommitMadeAfterRef: ResetTo(ctx, ref) must move HEAD
// back to ref, discarding a commit made on top of it — the exact gap
// gate.go's runAutofix closes: a fix that commits on its own moves HEAD, so
// resetting to the literal string "HEAD" (what the code used to pass)
// would be a no-op that leaves the fix's own commit in place.
func TestResetToDiscardsACommitMadeAfterRef(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	preFixHEAD, err := repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"evil.txt": "evil\n"})
	testutil.Git(t, repo.Dir, "add", "evil.txt")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "-m", "evil")
	if err := repo.ResetTo(ctx, preFixHEAD); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.HeadSHA(ctx); err != nil || got != preFixHEAD {
		t.Fatalf("HEAD = %s, %v, want %s", got, err, preFixHEAD)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("the commit made after ref survived ResetTo")
	}
}

// TestResetToIgnoresAReplaceRef: a refs/replace/<sha> entry -- which the run's
// own agent can write into the checkout's .git, and which git otherwise
// honours when it looks an object up -- must not decide what ResetTo puts the
// working tree at. ResetTo is the runner putting the branch back to a commit
// it made itself (gate.go's runAutofix, digest.go's digestFinalize), and that
// commit's own tree is the whole point of it.
//
// Mutation (run the reset without --no-replace-objects): the working tree is
// the replacement object's, so the branch holds content no commit of this
// run's ever had.
func TestResetToIgnoresAReplaceRef(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	step, err := repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"evil.txt": "evil\n"})
	testutil.Git(t, repo.Dir, "add", "-A")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "-m", "evil")
	testutil.Git(t, repo.Dir, "replace", step, testutil.Git(t, repo.Dir, "rev-parse", "HEAD"))

	if err := repo.ResetTo(ctx, step); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.HeadSHA(ctx); err != nil || got != step {
		t.Fatalf("HEAD = %s, %v, want %s", got, err, step)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("the replacement object's file survived ResetTo: the working tree is not the commit's own")
	}
	if data, err := os.ReadFile(filepath.Join(repo.Dir, "README.md")); err != nil || string(data) != "hello\n" {
		t.Fatalf("README.md = %q, %v, want the commit's own content", data, err)
	}
}

// TestIsCleanIgnoresReplaceRefs: the agent writes the checkout's own .git, so a
// refs/replace/<sha> it plants must not change what git says the tree is. `git
// status` compares the index against the tree of HEAD's commit, and a
// replacement whose tree differs from HEAD's makes a tree with no changes read
// as staged -- which sends CommitAll off to commit nothing.
//
// Mutation (plain `git status`): IsClean is false on a clean tree, and
// CommitAll fails instead of reporting that there was nothing to commit.
func TestIsCleanIgnoresReplaceRefs(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	base := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if changed, err := repo.CommitAll(ctx, "add a"); err != nil || !changed {
		t.Fatalf("CommitAll = %v, %v", changed, err)
	}
	head := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	testutil.Git(t, repo.Dir, "replace", head, base)
	// The plant does bite a plain read -- without this the test would pass on
	// a git that ignored the ref for some other reason.
	if out := testutil.Git(t, repo.Dir, "status", "--porcelain"); out == "" {
		t.Fatal("the planted replacement ref changed nothing about a plain `git status`; the test is not exercising it")
	}
	if clean, err := repo.IsClean(ctx); err != nil || !clean {
		t.Fatalf("IsClean with a planted replacement ref = %v, %v; want a clean tree", clean, err)
	}
	if changed, err := repo.CommitAll(ctx, "nothing"); err != nil || changed {
		t.Fatalf("CommitAll with a planted replacement ref = %v, %v; want nothing to commit", changed, err)
	}
}

// TestIsCleanSeesPastAReplaceRefThatMatchesTheIndex is the mirror of the test
// above: a replacement whose tree equals the index makes a plain `git status`
// read a tree with staged changes as clean, which would let CommitAll report
// there was nothing to commit while the agent's work sat uncommitted. The
// replacement-blind read still sees the staged change.
func TestIsCleanSeesPastAReplaceRefThatMatchesTheIndex(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	head := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	// A staged change, never committed: the index differs from HEAD's tree.
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	testutil.Git(t, repo.Dir, "add", "-A")
	tree := testutil.Git(t, repo.Dir, "write-tree")
	alt := testutil.Git(t, repo.Dir, "commit-tree", tree, "-p", head, "-m", "the index as a commit")
	testutil.Git(t, repo.Dir, "replace", head, alt)
	// The plant does bite a plain read: with replacements on, the index
	// matches the replacement tree and the staged change reads as clean.
	if out := testutil.Git(t, repo.Dir, "status", "--porcelain"); out != "" {
		t.Fatalf("the planted replacement did not hide the staged change from a plain `git status`: %q", out)
	}
	if clean, err := repo.IsClean(ctx); err != nil || clean {
		t.Fatalf("IsClean with a replacement matching the index = %v, %v; want a dirty tree", clean, err)
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

// TestNewPathsListsWhatACommitWouldCarry: the listing is exactly the set of new
// files a commit would take. A file the agent staged itself is new all the same
// and is in it, an ignored file is not (and so cannot reach a commit either), a
// tracked file is not new however much it changed, and a path with a newline in
// it survives the NUL-separated read.
func TestNewPathsListsWhatACommitWouldCarry(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"a.txt":           "a\n",
		"line\nbreak.txt": "b\n",
		"staged.bin":      "\x7fELF\x00\x00",
		"build/out.bin":   "ignored\n",
		"README.md":       "edited\n",
	})
	testutil.Git(t, repo.Dir, "add", "staged.bin")
	got, err := repo.NewPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	want := []string{"a.txt", "line\nbreak.txt", "staged.bin"}
	if !slices.Equal(got, want) {
		t.Fatalf("NewPaths = %q; want %q", got, want)
	}
}

// TestCommitAllExceptHoldsTheNamedPathsBack: the commit carries everything but
// the named paths, and the named ones stay in the tree untracked, where the
// reader of the run finds them.
func TestCommitAllExceptHoldsTheNamedPathsBack(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"a.txt":          "a\n",
		"test_dup_case2": "\x7fELF\x00\x00",
	})
	changed, err := repo.CommitAllExcept(ctx, "leftover", []string{"test_dup_case2"})
	if err != nil || !changed {
		t.Fatalf("CommitAllExcept = %v, %v", changed, err)
	}
	if carried := testutil.Git(t, repo.Dir, "show", "--name-only", "--format=", "HEAD"); carried != "a.txt" {
		t.Fatalf("the commit carries %q; want only a.txt", carried)
	}
	if got := testutil.Git(t, repo.Dir, "status", "--porcelain"); !strings.Contains(got, "?? test_dup_case2") {
		t.Fatalf("status = %q; want the held-back path still untracked in the tree", got)
	}
	if got := testutil.Git(t, repo.Dir, "log", "-1", "--format=%s"); got != "leftover" {
		t.Fatalf("subject = %q; want the message CommitAllExcept was given", got)
	}
}

// TestCommitAllExceptWithEveryPathExceptedMakesNoCommit: staging everything is
// how the call sees whether anything is left, and a tree whose every change is
// held back leaves nothing — no commit, and the branch where it was.
func TestCommitAllExceptWithEveryPathExceptedMakesNoCommit(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	before := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	testutil.WriteFiles(t, repo.Dir, map[string]string{"test_dup_case2": "\x00\n"})
	changed, err := repo.CommitAllExcept(ctx, "leftover", []string{"test_dup_case2"})
	if err != nil || changed {
		t.Fatalf("CommitAllExcept = %v, %v; want no commit", changed, err)
	}
	if after := testutil.Git(t, repo.Dir, "rev-parse", "HEAD"); after != before {
		t.Fatalf("HEAD moved from %s to %s", before, after)
	}
}

// TestCommitAllExceptReadsItsPathsAsLiterals: a file's name is data. A path
// beginning with a colon reads as a pathspec's magic, so without the literal
// read the reset fails and takes the whole leftover commit with it.
func TestCommitAllExceptReadsItsPathsAsLiterals(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		":magic.txt": "named like a pathspec\n",
		"keep.txt":   "kept\n",
	})
	changed, err := repo.CommitAllExcept(ctx, "leftover", []string{":magic.txt"})
	if err != nil || !changed {
		t.Fatalf("CommitAllExcept = %v, %v", changed, err)
	}
	if carried := testutil.Git(t, repo.Dir, "show", "--name-only", "--format=", "HEAD"); carried != "keep.txt" {
		t.Fatalf("the commit carries %q; want only keep.txt", carried)
	}
}

// TestNewPathsSeesPastAReplaceRefThatMatchesTheIndex: the staged read is what
// sees a file the agent `git add`-ed and never committed, and a replacement
// whose tree equals the index makes a plain `git diff --cached` read that file
// as no change at all -- which would hide a staged artifact from the listing
// finalize refuses by. The replacement-blind read still sees it.
func TestNewPathsSeesPastAReplaceRefThatMatchesTheIndex(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	head := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	// Staged, never committed, and so in the staged read alone: `--others` does
	// not report a file that is in the index.
	testutil.WriteFiles(t, repo.Dir, map[string]string{"test_dup_case2": "\x7fELF\x00\x00"})
	testutil.Git(t, repo.Dir, "add", "test_dup_case2")
	tree := testutil.Git(t, repo.Dir, "write-tree")
	alt := testutil.Git(t, repo.Dir, "commit-tree", tree, "-p", head, "-m", "the index as a commit")
	testutil.Git(t, repo.Dir, "replace", head, alt)
	// The plant does bite a plain read -- without this the test would pass on a
	// git that ignored the ref for some other reason.
	if out := testutil.Git(t, repo.Dir, "diff", "--cached", "--name-only", "--diff-filter=A", "HEAD"); out != "" {
		t.Fatalf("the planted replacement did not hide the staged file from a plain read: %q", out)
	}
	got, err := repo.NewPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"test_dup_case2"}) {
		t.Fatalf("NewPaths with a replacement matching the index = %q; want the staged file", got)
	}
}

// TestNewPathsIgnoresReplaceRefs is the mirror: a replacement whose tree differs
// from the index makes a plain `git diff --cached` call a committed file a newly
// staged one, and the listing finalize acts on must name what this checkout has
// staged rather than what a ref the agent planted says it has.
func TestNewPathsIgnoresReplaceRefs(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	base := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	testutil.WriteFiles(t, repo.Dir, map[string]string{"staged.bin": "\x7fELF\x00\x00"})
	if changed, err := repo.CommitAll(ctx, "add it"); err != nil || !changed {
		t.Fatalf("CommitAll = %v, %v", changed, err)
	}
	head := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	testutil.Git(t, repo.Dir, "replace", head, base)
	// The plant does bite a plain read: base's tree has no staged.bin, so the
	// committed file reads as an addition the index holds.
	if out := testutil.Git(t, repo.Dir, "diff", "--cached", "--name-only", "--diff-filter=A", "HEAD"); out == "" {
		t.Fatal("the planted replacement changed nothing about a plain read; the test is not exercising it")
	}
	got, err := repo.NewPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("NewPaths with a planted replacement ref = %q; want nothing", got)
	}
}

// TestCommitAllExceptSeesPastAReplaceRefThatMatchesTheIndex: the call commits
// the run's leftover work, and a replacement whose tree equals the index makes
// a plain `git diff --cached` read that work as no change at all -- which would
// have the call report there was nothing to commit and drop the work silently.
func TestCommitAllExceptSeesPastAReplaceRefThatMatchesTheIndex(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	base := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"feature.txt":    "the run's work\n",
		"test_dup_case2": "\x7fELF\x00\x00",
	})
	testutil.Git(t, repo.Dir, "add", "-A")
	testutil.Git(t, repo.Dir, "reset", "-q", "--", "test_dup_case2")
	tree := testutil.Git(t, repo.Dir, "write-tree")
	alt := testutil.Git(t, repo.Dir, "commit-tree", tree, "-p", base, "-m", "the index as a commit")
	testutil.Git(t, repo.Dir, "replace", base, alt)
	// The plant does bite a plain read -- without this the test would pass on a
	// git that ignored the ref for some other reason.
	if out := testutil.Git(t, repo.Dir, "diff", "--cached", "--name-only"); out != "" {
		t.Fatalf("the planted replacement did not hide the staged work from a plain read: %q", out)
	}
	changed, err := repo.CommitAllExcept(ctx, "leftover", []string{"test_dup_case2"})
	if err != nil || !changed {
		t.Fatalf("CommitAllExcept with a replacement matching the index = %v, %v; want the work committed", changed, err)
	}
	testutil.Git(t, repo.Dir, "replace", "-d", base)
	if carried := testutil.Git(t, repo.Dir, "show", "--name-only", "--format=", "HEAD"); carried != "feature.txt" {
		t.Fatalf("the commit carries %q; want only feature.txt", carried)
	}
	if got := testutil.Git(t, repo.Dir, "status", "--porcelain"); !strings.Contains(got, "?? test_dup_case2") {
		t.Fatalf("status = %q; want the held-back path still untracked in the tree", got)
	}
}

// TestCommitAllExceptDoesNotRestoreAHeldBackPathFromAReplaceRef: the call stages
// everything and then takes the held-back paths out of the index, and what it
// takes out comes from HEAD's tree. A replacement whose tree holds such a path
// puts it back, and the commit made afterwards carries the very file the call
// was told to leave alone.
func TestCommitAllExceptDoesNotRestoreAHeldBackPathFromAReplaceRef(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	base := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"feature.txt":    "the run's work\n",
		"test_dup_case2": "\x7fELF\x00\x00",
	})
	testutil.Git(t, repo.Dir, "add", "-A")
	tree := testutil.Git(t, repo.Dir, "write-tree")
	testutil.Git(t, repo.Dir, "reset", "-q", "--", "test_dup_case2")
	alt := testutil.Git(t, repo.Dir, "commit-tree", tree, "-p", base, "-m", "the tree with the held-back file")
	testutil.Git(t, repo.Dir, "replace", base, alt)
	// The plant does bite a plain read: with replacements on, HEAD's tree is the
	// one holding the path the call is about to hold back.
	if out := testutil.Git(t, repo.Dir, "ls-tree", "--name-only", "HEAD"); !strings.Contains(out, "test_dup_case2") {
		t.Fatalf("the planted replacement does not show the held-back file; the test is not exercising it: %q", out)
	}
	changed, err := repo.CommitAllExcept(ctx, "leftover", []string{"test_dup_case2"})
	if err != nil || !changed {
		t.Fatalf("CommitAllExcept = %v, %v", changed, err)
	}
	testutil.Git(t, repo.Dir, "replace", "-d", base)
	if carried := testutil.Git(t, repo.Dir, "show", "--name-only", "--format=", "HEAD"); carried != "feature.txt" {
		t.Fatalf("the commit carries %q; want only feature.txt", carried)
	}
	if got := testutil.Git(t, repo.Dir, "status", "--porcelain"); !strings.Contains(got, "?? test_dup_case2") {
		t.Fatalf("status = %q; want the held-back path still untracked in the tree", got)
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

// The budget marker (FUGARO_BUDGET_TOKEN) is the run's own secret: once the
// launcher seals, its value is the launch's seal key, and an agent-written
// .git/config command is exactly the way to read a git call's environment. It
// never reaches git or a command git runs, even for a Repo whose StripEnv is
// empty — StripEnv is the caller's list and this is not the caller's to
// remember (the runner's own BudgetTokenEnv is this constant).
func TestGitEnvStripsTheBudgetMarker(t *testing.T) {
	repo, _ := setup(t)
	// A fixture value, never a key: this test is about the variable's name and
	// value reaching a hook, not about the seal.
	const value = "marker-fixture-never-a-real-key"
	t.Setenv(BudgetTokenEnv, value)
	t.Setenv("KEEP_ME", "kept")
	dump := filepath.Join(t.TempDir(), "env.txt")
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nenv > "+dump+"\nprintf '\\0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "config", "core.fsmonitor", hook)
	repo.Env = append(repo.Env, BudgetTokenEnv+"="+value)
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
	if strings.Contains(string(b), BudgetTokenEnv) || strings.Contains(string(b), value) {
		t.Errorf("the hook's env holds the budget marker:\n%s", b)
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
	// A decoy pushurl: git pushes to a remote's pushurl when it is given the
	// remote's name, so the push below landing on remote is what shows it went
	// to origin's URL — the one the call read — and not to the name.
	decoy := testutil.NewRemote(t, map[string]string{"README.md": "decoy\n"})
	testutil.Git(t, repo.Dir, "config", "remote.origin.pushurl", decoy)
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
	// Pushed to origin's URL, not to the remote's name: a push to the name
	// would have followed remote.origin.pushurl to the decoy instead. (A URL
	// push no longer means no remote-tracking ref in the checkout; see
	// PushFastForward.)
	if got := testutil.Git(t, decoy, "for-each-ref", "refs/heads/fugaro/"); got != "" {
		t.Fatalf("the push went to origin's pushurl: %q", got)
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

func TestCommitOfAndIsAncestor(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	start, err := repo.CommitOf(ctx, "refs/heads/fugaro/x")
	if head, _ := repo.HeadSHA(ctx); err != nil || start != head {
		t.Fatalf("CommitOf = %q, %v; want %s", start, err, head)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	for _, c := range []struct {
		a, b string
		want bool
	}{{start, one, true}, {one, start, false}, {start, start, true}} {
		if got, err := repo.IsAncestor(ctx, c.a, c.b); err != nil || got != c.want {
			t.Errorf("IsAncestor(%s, %s) = %v, %v; want %v", c.a[:7], c.b[:7], got, err, c.want)
		}
	}
	for _, bad := range invalidSHAs(one) {
		if _, err := repo.IsAncestor(ctx, bad, start); err == nil {
			t.Errorf("IsAncestor(%q) took a non-SHA", bad)
		}
	}
	for _, ref := range []string{"HEAD~1", "--output=/tmp/x", "a..b", "refs/heads/nope"} {
		if sha, err := repo.CommitOf(ctx, ref); err == nil {
			t.Errorf("CommitOf(%q) = %q, want an error", ref, sha)
		}
	}
}

// TestCheckoutDetachedNeverRunsLFSFilters: a reviewed pull request's own
// .gitattributes can mark any path filter=lfs; wherever git-lfs (or any
// other command registered under that name) is already configured in
// this git config — locally or, in a real environment, globally, from an
// earlier `git lfs install` — checking out that content would otherwise
// run it. CheckoutDetached must never do that (design generic-tool §3.6,
// G12, reviewmode.go's stripClaudeConfig and the fork threat model it
// guards against): review mode never builds or tests the checkout, so
// there is no reason to run an externally-configured filter command at
// all, let alone one a fork's own .gitattributes pointed at.
//
// Mutation (drop withLFSFiltersOff from the checkout call, leaving the
// fetch's own in place): this test fails — the checkout itself errors
// (filter.lfs.process's long-running protocol speaks pkt-lines, and
// "touch" doesn't, so git reports a broken pipe as "the remote end hung
// up unexpectedly"), which the test treats as a failure the same as
// finding the marker would, since neither one is the clean, filter-free
// checkout this is pinning.
func TestCheckoutDetachedNeverRunsLFSFilters(t *testing.T) {
	repo, remote := setup(t)
	marker := filepath.Join(t.TempDir(), "smudge-ran")
	testutil.Git(t, repo.Dir, "config", "filter.lfs.smudge", "touch "+marker)
	testutil.Git(t, repo.Dir, "config", "filter.lfs.process", "touch "+marker)
	testutil.Git(t, repo.Dir, "config", "filter.lfs.required", "true")

	seed := filepath.Join(t.TempDir(), "seed-lfs")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{
		".gitattributes": "secret.bin filter=lfs -text\n",
		"secret.bin":     "pretend-lfs-pointer\n",
	})
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "lfs file")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/lfs")
	sha := testutil.Git(t, remote, "rev-parse", "refs/heads/lfs")

	if err := repo.CheckoutDetached(ctx, sha); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, repo.Dir, "rev-parse", "HEAD"); got != sha {
		t.Fatalf("HEAD = %s, want %s", got, sha)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the lfs filter ran during the checkout")
	}
}

// remoteWithCommits is a bare remote whose main branch holds n commits (the
// seeded one included), for the clone tests below. It returns the remote's
// `file://` URL, not the path testutil.NewRemote hands back: over a local
// path git clones with a local transport, which takes neither --filter nor
// --depth into account (it just hardlinks what it finds), so a filter or a
// shallowness bug would be invisible to these tests — review M4, where
// exactly that left a `--depth 1` mutation green. Over file:// git uses the
// upload-pack protocol and honours both, and uploadpack.allowFilter=true is
// what lets it serve the partial clone at all.
func remoteWithCommits(t *testing.T, n int) string {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "hello\n"})
	testutil.Git(t, remote, "config", "uploadpack.allowFilter", "true")
	if n > 1 {
		work := filepath.Join(t.TempDir(), "seed")
		testutil.Git(t, filepath.Dir(work), "clone", "--quiet", remote, work)
		for i := 1; i < n; i++ {
			name := fmt.Sprintf("f%d.txt", i)
			// Distinct contents: two files holding the same bytes are one
			// blob, and the blobless assertions below count blobs.
			testutil.WriteFiles(t, work, map[string]string{name: fmt.Sprintf("x%d\n", i)})
			testutil.Git(t, work, "add", "-A")
			testutil.Git(t, work, "commit", "--quiet", "-m", "commit "+name)
		}
		testutil.Git(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	}
	return "file://" + remote
}

// missingObjects is how many objects `git rev-list --objects
// --missing=print` reports as absent from the clone: git prints each as a
// "?"-prefixed line. It answers without fetching anything (which is the
// point: a lazy fetch would hide exactly what the test is asking about), so
// a blobless, unchecked-out clone counts the blobs it never downloaded, and
// a clone that has everything counts none.
func missingObjects(t *testing.T, dir string) int {
	t.Helper()
	var n int
	for _, line := range strings.Split(testutil.Git(t, dir, "rev-list", "--objects", "--missing=print", "HEAD"), "\n") {
		if strings.HasPrefix(line, "?") {
			n++
		}
	}
	return n
}

// TestCloneBloblessKeepsHistory (plan Task 17, G4): the clone is blobless,
// single-branch and checked out by nobody yet, but never shallow —
// checkpoints, follow-ups and merge-base need the whole history.
//
// Mutation (run, restore): add "--depth", "1" to CloneBlobless's args — the
// history and .git/shallow assertions fail; drop "--no-checkout" — the empty
// worktree and missing-blob assertions fail; drop "--filter=blob:none" — the
// missing-blob assertion fails.
func TestCloneBloblessKeepsHistory(t *testing.T) {
	remote := remoteWithCommits(t, 3)
	dir := filepath.Join(t.TempDir(), "repo")
	r, err := CloneBlobless(ctx, dir, remote, "main", IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if r.Dir != dir {
		t.Fatalf("Dir = %q, want %q", r.Dir, dir)
	}
	if n := testutil.Git(t, dir, "rev-list", "--count", "HEAD"); n != "3" {
		t.Fatalf("history = %s commits, want 3 (not shallow)", n)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "shallow")); !os.IsNotExist(err) {
		t.Fatalf(".git/shallow exists (err = %v): a shallow clone breaks checkpoints and merge-base", err)
	}
	if v := testutil.Git(t, dir, "config", "remote.origin.partialclonefilter"); v != "blob:none" {
		t.Fatalf("filter = %q, want blob:none", v)
	}
	if b := testutil.Git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); b != "main" {
		t.Fatalf("branch = %q, want main", b)
	}
	if got := testutil.Git(t, dir, "config", "remote.origin.fetch"); got != "+refs/heads/main:refs/remotes/origin/main" {
		t.Fatalf("fetch refspec = %q, want the single branch", got)
	}
	// --no-checkout: the clone brings refs and trees, no working tree, and
	// the file contents are not fetched until a checkout wants them.
	if entries, err := os.ReadDir(dir); err != nil {
		t.Fatal(err)
	} else {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if len(names) != 1 || names[0] != ".git" {
			t.Fatalf("the clone's directory holds %v, want only .git (--no-checkout)", names)
		}
	}
	if n := missingObjects(t, dir); n == 0 {
		t.Fatal("every object is already in the clone: it is not blobless")
	} else if n < 3 {
		t.Fatalf("%d objects are missing, want the one blob per commit (3)", n)
	}
	// The file contents are fetched on demand, and a checkout still works:
	// this is what the runner's checkout of the task's ref does.
	if err := r.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/x" {
		t.Fatalf("checked-out branch = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("the checkout has no README.md: %v", err)
	}
	if n := missingObjects(t, dir); n != 0 {
		t.Fatalf("%d objects are still missing after the checkout, want none: the checkout fetched what it needs", n)
	}
}

// An empty branch clones the remote's own default branch; a remote that
// does not exist is an error, not a checkout.
func TestCloneBloblessDefaultBranchAndErrors(t *testing.T) {
	remote := remoteWithCommits(t, 2)
	dir := filepath.Join(t.TempDir(), "sub", "repo")
	if _, err := CloneBlobless(ctx, dir, remote, "", IdentityEnv()); err != nil {
		t.Fatal(err)
	}
	if n := testutil.Git(t, dir, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("history = %s commits, want 2", n)
	}
	if _, err := CloneBlobless(ctx, filepath.Join(t.TempDir(), "x"), "", "main", nil); err == nil {
		t.Fatal("cloned with no remote")
	}
	if _, err := CloneBlobless(ctx, filepath.Join(t.TempDir(), "y"), filepath.Join(t.TempDir(), "nope"), "main", nil); err == nil {
		t.Fatal("cloned a remote that does not exist")
	}
}

// CloneBlobless strips the model credential from the clone's own git calls,
// as OpenOrClone does (its hook and filter commands must never see one).
func TestCloneBloblessStripsFromTheStart(t *testing.T) {
	remote := remoteWithCommits(t, 1)
	env := append(IdentityEnv(), "ANTHROPIC_API_KEY=test-key")
	r, err := CloneBlobless(ctx, filepath.Join(t.TempDir(), "repo"), remote, "main", env, ModelCredentialVars...)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.StripEnv) != len(ModelCredentialVars) {
		t.Fatalf("StripEnv = %v", r.StripEnv)
	}
	for _, kv := range r.Environ() {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Fatal("the clone's environment holds the model credential")
		}
	}
}

// remoteWithABranch seeds a remote whose main holds a file, a branch "feat"
// that changes it twice, and a main that then moves on: the shape a pull
// request has once the base has moved past the merge base. Three revisions
// of app.txt exist and none of them is the branch tip's — the merge base's,
// the branch's first commit's, and the base tip's — which is exactly what a
// blobless clone's checkout of the branch does not bring in. It returns the
// remote's path and its file:// URL.
func remoteWithABranch(t *testing.T) (path, url string) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"app.txt": "one\n", "other.txt": "other\n"})
	testutil.Git(t, remote, "config", "uploadpack.allowFilter", "true")
	work := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(work), "clone", "--quiet", remote, work)
	testutil.WriteFiles(t, work, map[string]string{"app.txt": "one\ntwo\n"})
	testutil.Git(t, work, "add", "-A")
	testutil.Git(t, work, "commit", "--quiet", "-m", "change app.txt")
	testutil.WriteFiles(t, work, map[string]string{"app.txt": "one\ntwo\nthree\n"})
	testutil.Git(t, work, "add", "-A")
	testutil.Git(t, work, "commit", "--quiet", "-m", "change app.txt again")
	testutil.Git(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/feat")
	// Back to the merge base, then main moves on: the base tip's version of
	// app.txt is then neither the merge base's nor the branch's.
	testutil.Git(t, work, "reset", "--hard", "--quiet", "HEAD~2")
	testutil.WriteFiles(t, work, map[string]string{"app.txt": "one\nbase moved\n"})
	testutil.Git(t, work, "add", "-A")
	testutil.Git(t, work, "commit", "--quiet", "-m", "base moved")
	testutil.Git(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	return remote, "file://" + remote
}

// gitOut runs git with args in dir, with the process's own environment — no
// credentials of the runner's — and returns what git said. It is what the
// agent's own git does.
func gitOut(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String() + stderr.String(), err
}

// moveRemoteAway renames the remote out of the way and returns a func that
// puts it back: the review's own stand-in for the agent having no
// credentials, under which any lazy fetch from it fails.
func moveRemoteAway(t *testing.T, remote string) (back func()) {
	t.Helper()
	away := remote + ".away"
	if err := os.Rename(remote, away); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.Rename(away, remote); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPrefetchDiffFetchesTheBaseSide (review N1): in a blobless clone the
// checkout brings in the checked-out revision's file contents and no
// others, so `git diff origin/main...HEAD` needs a lazy fetch from origin —
// and on a private repository the agent's own git has no credentials to
// make it with. PrefetchDiff runs that diff on the runner's side, whose git
// does carry them, and the objects it fetches are what the agent's later
// diff reads.
//
// The remote is moved away for the agent's attempts, the review's own
// stand-in: with no credentials, any lazy fetch from it fails. The
// prefetch's own call happens while it is still there.
//
// Mutation (run, restore): make PrefetchDiff a no-op (or drop the diff it
// runs) — the second diff fails with "could not fetch <oid> from promisor
// remote", which is the bug.
func TestPrefetchDiffFetchesTheBaseSide(t *testing.T) {
	remote, url := remoteWithABranch(t)
	dir := filepath.Join(t.TempDir(), "repo")
	r, err := CloneBlobless(ctx, dir, url, "", IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	// The runner's checkout of the pull request's branch (CheckoutNewBranch
	// fetches it by its full name), then the base.
	if err := r.CheckoutNewBranch(ctx, "refs/heads/feat", "feat"); err != nil {
		t.Fatal(err)
	}
	if err := r.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	// Moving the remote away stands in for the agent having no credentials:
	// a lazy fetch from it fails either way, and the runner's own prefetch
	// below happens while it is back in place.
	back := moveRemoteAway(t, remote)
	defer back()
	if out, err := gitOut(t, dir, "diff", "origin/main...HEAD"); err == nil {
		t.Fatalf("the agent's diff already works with no remote: %s", out)
	}
	back()
	if err := r.PrefetchDiff(ctx, "origin/main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	moveRemoteAway(t, remote)
	out, err := gitOut(t, dir, "diff", "origin/main...HEAD")
	if err != nil {
		t.Fatalf("the agent's diff still fails after the prefetch: %v\n%s", err, out)
	}
	if !strings.Contains(out, "+two") {
		t.Fatalf("the agent's diff says nothing about the change:\n%s", out)
	}
}

// TestPrefetchFetchesTheBranchsOwnCommits (review M2): the three-dot diff
// covers the two endpoints of the branch's diff and no more, so reading a
// pull request commit by commit — `git show <a commit of the branch>`,
// `git log -p origin/main..HEAD`, `git diff HEAD~1` — and a two-dot
// `git diff origin/main` against a base that has moved on each need file
// contents the endpoints' diff never fetched. On a private repository,
// where the agent's git has no credentials, each fails with "could not
// fetch <oid> from promisor remote" until the runner prefetches them.
//
// Mutation (run, restore): drop either PrefetchCommits or PrefetchBaseDiff
// from the two below and the commands it serves fail here.
func TestPrefetchFetchesTheBranchsOwnCommits(t *testing.T) {
	remote, url := remoteWithABranch(t)
	dir := filepath.Join(t.TempDir(), "repo")
	r, err := CloneBlobless(ctx, dir, url, "", IdentityEnv())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CheckoutNewBranch(ctx, "refs/heads/feat", "feat"); err != nil {
		t.Fatal(err)
	}
	if err := r.FetchBase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	// What a reviewer reads a pull request with, and what it needs of
	// app.txt that the checkout of the branch tip did not bring in: the
	// branch's first commit's version; the branch's second; the base tip's.
	reads := [][]string{
		{"show", "HEAD~1"},
		{"log", "-p", "origin/main..HEAD"},
		{"diff", "origin/main"},
	}
	back := moveRemoteAway(t, remote)
	defer back()
	for _, args := range reads {
		if out, err := gitOut(t, dir, args...); err == nil {
			t.Fatalf("git %s already works with no remote: %s", strings.Join(args, " "), out)
		}
	}
	back()
	if err := r.PrefetchCommits(ctx, "origin/main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := r.PrefetchBaseDiff(ctx, "origin/main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	moveRemoteAway(t, remote)
	for _, args := range reads {
		out, err := gitOut(t, dir, args...)
		if err != nil {
			t.Fatalf("git %s still fails after the prefetch: %v\n%s", strings.Join(args, " "), err, out)
		}
		if !strings.Contains(out, "app.txt") {
			t.Fatalf("git %s says nothing about the file the branch changed:\n%s", strings.Join(args, " "), out)
		}
	}
}

// TestNewPathsSeesAnIntentToAddFile: `git add -N` puts a path in the index
// without its content. It is absent from `ls-files --others` (it is in the
// index) and `diff --cached` hides it, so a listing built from those two names
// neither, while `git add -A` at finalize would commit the file.
func TestNewPathsSeesAnIntentToAddFile(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"x.bin": "\x7fELF\x00\x00"})
	testutil.Git(t, repo.Dir, "add", "-N", "x.bin")
	got, err := repo.NewPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "x.bin") {
		t.Fatalf("NewPaths = %q; want the intent-to-add x.bin listed", got)
	}
}

// TestNewPathsSeesANewFileThatLooksLikeARenameOfATrackedOne: delete a tracked
// file and stage a new one with nearly the same bytes, and git reports a rename,
// not an addition, so a listing filtered to additions would drop the new path.
// The new file is new all the same and is what a commit would carry.
func TestNewPathsSeesANewFileThatLooksLikeARenameOfATrackedOne(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	body := "\x7fELF\x00\x00" + strings.Repeat("same bytes\n", 40)
	testutil.WriteFiles(t, repo.Dir, map[string]string{"tracked.bin": body})
	if _, err := repo.CommitAll(ctx, "base"); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "rm", "-q", "tracked.bin")
	testutil.WriteFiles(t, repo.Dir, map[string]string{"out.o": body + "one more line\n"})
	testutil.Git(t, repo.Dir, "add", "out.o")
	got, err := repo.NewPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "out.o") {
		t.Fatalf("NewPaths = %q; want out.o listed although git sees a rename", got)
	}
}
