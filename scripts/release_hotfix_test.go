// --hotfix, scripts/release.sh's other path: a patch release of a line that
// shipped from main, cut from its maintenance branch release/X.Y. The fixture
// here is the release one with a line that has shipped -- v0.12.0 tagging main,
// a fix merged to main after it and main's plugin bumped to 0.12.1 -- which is
// the state docs/release.md's "Hotfix releases" starts from.
package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// hotfixRepo is a releaseRepo whose line 0.12 has shipped: the tag it was cut
// from, the fix --fix names, and main's plugin bump for the patch.
type hotfixRepo struct {
	*releaseRepo
	first string // v0.12.0's commit on main: the line's floor
	fix   string // a fix commit on main
	bump  string // the commit on main that set the plugin to 0.12.1
}

func newHotfixRepo(t *testing.T) *hotfixRepo {
	t.Helper()
	r := newReleaseRepo(t)
	git := func(args ...string) string { return strings.TrimSpace(testutil.Git(t, r.local, args...)) }

	// The line shipped from main: v0.12.0 tags the seed, and main's plugin is
	// still the fixture's 0.11.4 there -- the bump is the hotfix's own commit.
	git("tag", "-a", "v0.12.0", "-m", "Fugaro v0.12.0")
	git("push", "--quiet", "origin", "refs/tags/v0.12.0")
	first := git("rev-parse", "v0.12.0^{commit}")

	// The fix, merged to main first through a PR as usual: what --fix names, and
	// what the gate's ancestry rule and verify-release.sh's trailer check read
	// main for.
	if err := os.WriteFile(filepath.Join(r.local, "fix.txt"), []byte("the fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "fix: the thing that was wrong")
	git("push", "--quiet", "origin", "main")
	fix := git("rev-parse", "HEAD")

	// The bump PR, landed on main before the hotfix (docs/release.md): release.yml
	// checks the tag against this version, so --hotfix cherry-picks the commit
	// that set it onto the branch.
	bump := exec.Command("bash", filepath.Join(r.local, "scripts", "bump-plugin-version.sh"), "0.12.1")
	bump.Dir = r.local
	if out, err := bump.CombinedOutput(); err != nil {
		t.Fatalf("bumping the fixture plugin to 0.12.1: %v\n%s", err, out)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "plugin: 0.12.1")
	git("push", "--quiet", "origin", "main")
	return &hotfixRepo{releaseRepo: r, first: first, fix: fix, bump: git("rev-parse", "HEAD")}
}

// git runs git in the fixture checkout and returns its trimmed output.
func (r *hotfixRepo) git(t *testing.T, args ...string) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, r.local, args...))
}

// state is the fake gh's state for this fixture, every required check green.
// The tests that turn one red build their own.
func (r *hotfixRepo) state(t *testing.T) *ghState {
	t.Helper()
	return newGHState(t, r.releaseRepo, greenChecks())
}

// runHotfix runs scripts/release.sh X.Y.Z --hotfix with the extra arguments.
func runHotfix(t *testing.T, r *hotfixRepo, g *ghState, version string, extra ...string) runResult {
	t.Helper()
	return runRelease(t, r.releaseRepo, g, "", append([]string{version, "--hotfix"}, extra...)...)
}

// refExists reports whether ref resolves in dir, without failing the test when
// it does not: for these tests a missing ref is an answer.
func refExists(t *testing.T, dir, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--quiet", "--verify", ref)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// isAncestor reports whether a is reachable from b in the fixture checkout.
func isAncestor(t *testing.T, dir, a, b string) bool {
	t.Helper()
	cmd := exec.Command("git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// worktrees is how many worktrees the fixture checkout has: more than one means
// a scratch worktree was left behind.
func worktrees(t *testing.T, dir string) int {
	t.Helper()
	out := testutil.Git(t, dir, "worktree", "list")
	return len(strings.Split(out, "\n"))
}

// TestHotfixRefusesAMinorsFirstRelease: X.Y.0 belongs to main, and the gate
// refuses a vX.Y.0 anywhere else.
func TestHotfixRefusesAMinorsFirstRelease(t *testing.T) {
	r := newHotfixRepo(t)
	res := runHotfix(t, r, r.state(t), "0.12.0")
	res.requireFailureContaining(t, "X.Y.0 is a minor's first release")
	if refExists(t, r.bare, "refs/heads/release/0.12") {
		t.Error("a refused run created the maintenance branch")
	}
}

// TestHotfixRefusesFixWithoutHotfix: --fix names a commit --hotfix cherry-picks;
// the ordinary release takes no outside commit.
func TestHotfixRefusesFixWithoutHotfix(t *testing.T) {
	r := newHotfixRepo(t)
	g := r.state(t)
	res := runRelease(t, r.releaseRepo, g, "", "0.12.1", "--fix", r.fix)
	res.requireFailureContaining(t, "--fix names a commit --hotfix cherry-picks")
	if n := countLog(t, g, "repo view"); n != 0 {
		t.Errorf("the run got as far as gh (%d call(s)): --fix without --hotfix is refused at the arguments", n)
	}
}

// TestHotfixRefusesAFixWithNoValue: --fix takes a sha.
func TestHotfixRefusesAFixWithNoValue(t *testing.T) {
	r := newHotfixRepo(t)
	res := runHotfix(t, r, r.state(t), "0.12.1", "--fix")
	res.requireFailureContaining(t, "--fix needs a commit")
}

// TestHotfixRefusesWithoutTheLinesFirstTag: a patch of a line that never
// shipped is not a patch of anything.
func TestHotfixRefusesWithoutTheLinesFirstTag(t *testing.T) {
	r := newReleaseRepo(t)
	res := runRelease(t, r, newGHState(t, r, greenChecks()), "", "0.12.1", "--hotfix")
	res.requireFailureContaining(t, "v0.12.0 does not exist")
}

// TestHotfixRefusesALineThatNeverWentThroughMain: vX.Y.0 may exist on some
// other branch, but the gate only accepts a patch of a line that was released
// from main.
func TestHotfixRefusesALineThatNeverWentThroughMain(t *testing.T) {
	r := newReleaseRepo(t)
	git := func(args ...string) string { return strings.TrimSpace(testutil.Git(t, r.local, args...)) }
	git("checkout", "--quiet", "-b", "line-that-never-shipped")
	if err := os.WriteFile(filepath.Join(r.local, "line.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "a line that never went through main")
	git("tag", "-a", "v0.12.0", "-m", "v0.12.0")
	git("push", "--quiet", "origin", "refs/tags/v0.12.0")
	git("checkout", "--quiet", "main")
	res := runRelease(t, r, newGHState(t, r, greenChecks()), "", "0.12.1", "--hotfix")
	res.requireFailureContaining(t, "v0.12.0 is not on origin/main")
}

// TestHotfixRefusesAnOlderLine: only the newest line may be patched from a
// branch, or the patch is published as the latest release.
func TestHotfixRefusesAnOlderLine(t *testing.T) {
	r := newHotfixRepo(t)
	r.git(t, "tag", "-a", "v0.13.0", "-m", "v0.13.0")
	r.git(t, "push", "--quiet", "origin", "refs/tags/v0.13.0")
	res := runHotfix(t, r, r.state(t), "0.12.1", "--fix", r.fix)
	res.requireFailureContaining(t, "a newer line exists (v0.13.0)")
}

// TestHotfixRefusesWhenMainIsBehindTheVersion: the bump PR has to land on main
// first (docs/release.md), because the branch is never merged back.
func TestHotfixRefusesWhenMainIsBehindTheVersion(t *testing.T) {
	r := newReleaseRepo(t)
	testutil.Git(t, r.local, "tag", "-a", "v0.12.0", "-m", "v0.12.0")
	testutil.Git(t, r.local, "push", "--quiet", "origin", "refs/tags/v0.12.0")
	res := runRelease(t, r, newGHState(t, r, greenChecks()), "", "0.12.1", "--hotfix")
	res.requireFailureContaining(t, "main's plugin/.claude-plugin/plugin.json says '0.11.4'")
}

// TestHotfixRefusesWhenNoCommitOnMainSetsTheVersion: main's plugin can be newer
// than the patch and still carry no commit that sets it to the patch's version
// -- the 0.21.1 shape, where the bump never went through main -- and then there
// is nothing to cherry-pick onto the branch.
func TestHotfixRefusesWhenNoCommitOnMainSetsTheVersion(t *testing.T) {
	r := newReleaseRepo(t)
	git := func(args ...string) string { return strings.TrimSpace(testutil.Git(t, r.local, args...)) }
	git("tag", "-a", "v0.12.0", "-m", "v0.12.0")
	git("push", "--quiet", "origin", "refs/tags/v0.12.0")
	if err := os.WriteFile(filepath.Join(r.local, "fix.txt"), []byte("the fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "fix: the thing that was wrong")
	git("push", "--quiet", "origin", "main")
	fix := git("rev-parse", "HEAD")
	bump := exec.Command("bash", filepath.Join(r.local, "scripts", "bump-plugin-version.sh"), "0.13.0")
	bump.Dir = r.local
	if out, err := bump.CombinedOutput(); err != nil {
		t.Fatalf("bumping the fixture plugin to 0.13.0: %v\n%s", err, out)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "plugin: 0.13.0")
	git("push", "--quiet", "origin", "main")
	res := runRelease(t, r, newGHState(t, r, greenChecks()), "", "0.12.1", "--hotfix", "--fix", fix)
	res.requireFailureContaining(t, "no commit on origin/main sets it to 0.12.1")
}

// TestHotfixRefusesACommitThatIsNotOnMain: a fix is merged to main first, and
// the branch carries a copy of it, never the only copy.
func TestHotfixRefusesACommitThatIsNotOnMain(t *testing.T) {
	r := newHotfixRepo(t)
	r.git(t, "checkout", "--quiet", "-b", "unmerged-fix")
	if err := os.WriteFile(filepath.Join(r.local, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(t, "add", "-A")
	r.git(t, "commit", "--quiet", "-m", "a fix that never merged")
	unmerged := r.git(t, "rev-parse", "HEAD")
	r.git(t, "checkout", "--quiet", "main")
	res := runHotfix(t, r, r.state(t), "0.12.1", "--fix", unmerged)
	res.requireFailureContaining(t, "--fix "+unmerged+" is not on origin/main")
}

// TestHotfixRefusesAShaThatIsNotACommit covers the other half of the same
// precondition: a sha this checkout does not have.
func TestHotfixRefusesAShaThatIsNotACommit(t *testing.T) {
	r := newHotfixRepo(t)
	missing := strings.Repeat("0", 40)
	res := runHotfix(t, r, r.state(t), "0.12.1", "--fix", missing)
	res.requireFailureContaining(t, "--fix "+missing+" is not a commit in this checkout")
}

// TestHotfixRefusesAnExistingTag: a release is never re-tagged.
func TestHotfixRefusesAnExistingTag(t *testing.T) {
	r := newHotfixRepo(t)
	r.git(t, "tag", "-a", "v0.12.1", "-m", "v0.12.1")
	r.git(t, "push", "--quiet", "origin", "refs/tags/v0.12.1")
	res := runHotfix(t, r, r.state(t), "0.12.1", "--fix", r.fix)
	res.requireFailureContaining(t, "tag v0.12.1 already exists on origin")
}

// TestHotfixRefusesALocalTag covers a leftover tag this checkout holds and
// origin does not: the tag would otherwise be created a second time.
func TestHotfixRefusesALocalTag(t *testing.T) {
	r := newHotfixRepo(t)
	r.git(t, "tag", "-a", "v0.12.1", "-m", "v0.12.1")
	res := runHotfix(t, r, r.state(t), "0.12.1", "--fix", r.fix)
	res.requireFailureContaining(t, "tag v0.12.1 exists locally")
}

// TestHotfixRefusesAVersionTheLineAlreadyHas: the patch is released after the
// tags the line already carries, never between them.
func TestHotfixRefusesAVersionTheLineAlreadyHas(t *testing.T) {
	r := newHotfixRepo(t)
	r.git(t, "tag", "-a", "v0.12.2", "-m", "v0.12.2")
	r.git(t, "push", "--quiet", "origin", "refs/tags/v0.12.2")
	res := runHotfix(t, r, r.state(t), "0.12.1", "--fix", r.fix)
	res.requireFailureContaining(t, "not greater than the line's latest tag v0.12.2")
}

// TestHotfixRefusesABranchThatIsNotThisLines: origin's release/X.Y is reused,
// but only when it descends from vX.Y.0 -- the ancestry the gate reads -- so a
// branch that merely shares the name cannot be tagged.
func TestHotfixRefusesABranchThatIsNotThisLines(t *testing.T) {
	r := newReleaseRepo(t)
	git := func(args ...string) string { return strings.TrimSpace(testutil.Git(t, r.local, args...)) }
	seed := git("rev-parse", "HEAD")
	// v0.12.0 tags main later than the branch points: release/0.12 would then be
	// a branch of the line's name that does not carry the line's release.
	if err := os.WriteFile(filepath.Join(r.local, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "later work on main")
	git("tag", "-a", "v0.12.0", "-m", "v0.12.0")
	bump := exec.Command("bash", filepath.Join(r.local, "scripts", "bump-plugin-version.sh"), "0.12.1")
	bump.Dir = r.local
	if out, err := bump.CombinedOutput(); err != nil {
		t.Fatalf("bumping the fixture plugin to 0.12.1: %v\n%s", err, out)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "plugin: 0.12.1")
	git("push", "--quiet", "origin", "main", "refs/tags/v0.12.0")
	git("checkout", "--quiet", "-b", "release/0.12", seed)
	git("push", "--quiet", "origin", "release/0.12")
	git("checkout", "--quiet", "main")
	res := runRelease(t, r, newGHState(t, r, greenChecks()), "", "0.12.1", "--hotfix", "--fix", git("rev-parse", "main"))
	res.requireFailureContaining(t, "origin's release/0.12 does not descend from v0.12.0")
	if refExists(t, r.bare, "refs/tags/v0.12.1") {
		t.Error("the refused run tagged the branch")
	}
}

// TestHotfixRefusesADirtyTree: the same precondition the ordinary release has.
func TestHotfixRefusesADirtyTree(t *testing.T) {
	r := newHotfixRepo(t)
	if err := os.WriteFile(filepath.Join(r.local, "uncommitted.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runHotfix(t, r, r.state(t), "0.12.1", "--fix", r.fix)
	res.requireFailureContaining(t, "the working tree is not clean")
}

// TestHotfixDryRunChangesNothing: the plan is printed for a release the text of
// which is not on the branch yet, and nothing is pushed, tagged or left behind.
func TestHotfixDryRunChangesNothing(t *testing.T) {
	r := newHotfixRepo(t)
	g := r.state(t)
	before := r.git(t, "rev-parse", "HEAD")
	res := runHotfix(t, r, g, "0.12.1", "--fix", r.fix, "--dry-run")
	res.requireSuccess(t)
	for _, want := range []string{
		"Dry run for v0.12.1 (release/0.12)",
		"docs/releases/v0.12.1.md",
		"website/data/release-summaries.yaml",
		"gh workflow run ci --ref release/0.12",
		"scripts/verify-release.sh v0.12.1",
	} {
		if !strings.Contains(res.out, want) {
			t.Errorf("the dry run does not say %q:\n%s", want, res.out)
		}
	}
	if refExists(t, r.bare, "refs/heads/release/0.12") {
		t.Error("the dry run pushed the maintenance branch")
	}
	if refExists(t, r.bare, "refs/tags/v0.12.1") {
		t.Error("the dry run pushed the tag")
	}
	if n := countLog(t, g, "workflow run"); n != 0 {
		t.Errorf("the dry run dispatched CI %d time(s)", n)
	}
	if after := r.git(t, "rev-parse", "HEAD"); after != before {
		t.Errorf("the dry run moved the checkout from %s to %s", before, after)
	}
	if n := worktrees(t, r.local); n != 1 {
		t.Errorf("the dry run left %d worktrees behind", n-1)
	}
}

// TestHotfixHappyPath is the whole path, in the order docs/release.md's
// "Hotfix releases" runs it: the branch created from the line's tag with the
// fix and the bump cherry-picked with -x, the release's own text refused on the
// first run (the hand flow commits it on the branch), then CI dispatched on the
// branch, the tag and its verification. The user's checkout never moves.
func TestHotfixHappyPath(t *testing.T) {
	r := newHotfixRepo(t)
	g := r.state(t)
	before := r.git(t, "rev-parse", "HEAD")

	// The first run: the branch is built and pushed, and then refused, because
	// the release's own text is not on it and this script writes none of it.
	first := runHotfix(t, r, g, "0.12.1", "--fix", r.fix)
	first.requireFailureContaining(t, "docs/releases/v0.12.1.md")
	first.requireFailureContaining(t, "release/0.12 is pushed")

	firstTip := testutil.Git(t, r.bare, "rev-parse", "release/0.12")
	if !isAncestor(t, r.bare, r.first, firstTip) {
		t.Errorf("release/0.12 (%s) does not descend from v0.12.0 (%s)", firstTip, r.first)
	}
	// The branch carries the fix with its content, and the bump release.yml
	// checks the tag against, each cherry-picked with -x.
	body := testutil.Git(t, r.bare, "log", "--format=%B", "release/0.12")
	for _, sha := range []string{r.fix, r.bump} {
		if !strings.Contains(body, "(cherry picked from commit "+sha+")") {
			t.Errorf("release/0.12 does not carry %s as a cherry-pick:\n%s", sha, body)
		}
	}
	if n := strings.Count(body, "(cherry picked from commit"); n != 2 {
		t.Errorf("release/0.12 carries %d cherry-picks, want the fix and the bump:\n%s", n, body)
	}
	if out := testutil.Git(t, r.bare, "show", "release/0.12:fix.txt"); out != "the fix" {
		t.Errorf("the fix's content is not on the branch: %q", out)
	}
	if refExists(t, r.bare, "refs/tags/v0.12.1") {
		t.Error("the first run tagged the branch: the release's text is not on it yet")
	}
	if n := worktrees(t, r.local); n != 1 {
		t.Errorf("the refused run left %d worktrees behind", n-1)
	}
	if after := r.git(t, "rev-parse", "HEAD"); after != before {
		t.Errorf("the refused run moved the checkout from %s to %s", before, after)
	}

	// The hand flow's step 3, committed on the branch: the Highlights, the
	// summary line the changelog page prints, and the stream's current.
	commitHotfixText(t, r, "0.12.1")

	// The second run resumes: the fix and the bump are already there (their
	// trailers say so), the text is there, so CI is dispatched, the gate is
	// waited for, the tag goes on the branch tip and verify-release.sh runs.
	second := runHotfix(t, r, g, "0.12.1", "--fix", r.fix)
	second.requireSuccess(t)
	if !strings.Contains(second.out, "dispatched CI on release/0.12") {
		t.Errorf("the run did not dispatch CI on the branch:\n%s", second.out)
	}
	if !strings.Contains(second.out, "verify-release: v0.12.1") {
		t.Errorf("the run did not verify the tag it pushed:\n%s", second.out)
	}
	if runs := g.read(t, "workflow_run"); !strings.Contains(runs, "workflow run ci --ref release/0.12") {
		t.Errorf("CI was not dispatched on release/0.12; the fake recorded:\n%s", runs)
	}
	// The tag is on the branch tip, and the branch is the one the first run
	// pushed with the hand-written text committed on top -- never rebuilt.
	tip := testutil.Git(t, r.bare, "rev-parse", "release/0.12")
	if tagged := r.git(t, "rev-parse", "refs/tags/v0.12.1^{commit}"); tagged != tip {
		t.Errorf("tag v0.12.1 (%s) is not on the branch tip (%s)", tagged, tip)
	}
	if !isAncestor(t, r.bare, firstTip, tip) {
		t.Errorf("the re-run rebuilt the branch: origin has %s, which does not carry the first run's %s", tip, firstTip)
	}
	if n := strings.Count(testutil.Git(t, r.bare, "log", "--format=%B", "release/0.12"), "(cherry picked from commit"); n != 2 {
		t.Errorf("the re-run picked the fix or the bump a second time (%d cherry-picks)", n)
	}
	if branchOn := r.git(t, "rev-parse", "--abbrev-ref", "HEAD"); branchOn != "main" {
		t.Errorf("the checkout ended on %q, want main (--hotfix never moves it)", branchOn)
	}
	if n := worktrees(t, r.local); n != 1 {
		t.Errorf("the run left %d worktrees behind", n-1)
	}
}

// commitHotfixText commits the three pieces of a hotfix's own text on the
// maintenance branch, in a clone of origin: what the hand flow does at step 3,
// and what --hotfix refuses to invent.
func commitHotfixText(t *testing.T, r *hotfixRepo, version string) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "branch")
	testutil.Git(t, filepath.Dir(work), "clone", "--quiet", r.bare, work)
	testutil.Git(t, work, "checkout", "--quiet", "release/0.12")
	writeHighlights(t, work, version, "- A user-facing change.\n")
	writeSummaries(t, work, map[string]string{version: "A user-facing change."})
	setStreamCurrent(t, work, version)
	testutil.Git(t, work, "add", "-A")
	testutil.Git(t, work, "commit", "--quiet", "-m", "release: v"+version)
	testutil.Git(t, work, "push", "--quiet", "origin", "release/0.12")
}

// setStreamCurrent records the CLI stream's current through the catalogue's own
// writer, internal/streams/cmd/streams, which is what a release does.
func setStreamCurrent(t *testing.T, dir, version string) {
	t.Helper()
	cmd := exec.Command(streamsHelper(t), "set", "cli", version)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("streams set cli %s: %v\n%s", version, err, out)
	}
}
