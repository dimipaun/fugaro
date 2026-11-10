// scripts/verify-release.sh, the post-tag verification the release rule in
// docs/release.md asks for: a release is not done until every workflow on its
// tag is green, the images it published can be pulled without a login, and --
// for a patch tag -- every fix it carries is on main. The fixture tags v0.12.1
// on release/0.12 the way scripts/release.sh --hotfix leaves it, and the fake
// gh answers the runs a tag push starts (FAKE_GH_RUN_*), while the fake docker
// answers images/verify-public.sh's three calls (FAKE_DOCKER_FAIL).
package scripts_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// newVerifyFixture is a hotfixRepo whose v0.12.1 is already tagged on
// release/0.12, exactly as scripts/release.sh --hotfix leaves it: the branch
// cut from the line's tag, the fix and the bump cherry-picked with -x, and the
// release's own text committed on top.
func newVerifyFixture(t *testing.T) *hotfixRepo {
	t.Helper()
	r := newHotfixRepo(t)
	git := func(args ...string) string { return strings.TrimSpace(testutil.Git(t, r.local, args...)) }
	git("checkout", "--quiet", "-b", "release/0.12", "v0.12.0")
	git("cherry-pick", "-x", r.fix)
	git("cherry-pick", "-x", r.bump)
	writeHighlights(t, r.local, "0.12.1", "- A fix.\n")
	writeSummaries(t, r.local, map[string]string{"0.12.1": "A fix."})
	setStreamCurrent(t, r.local, "0.12.1")
	git("add", "-A")
	git("commit", "--quiet", "-m", "release: v0.12.1")
	git("push", "--quiet", "origin", "release/0.12")
	git("tag", "-a", "v0.12.1", "-m", "Fugaro v0.12.1")
	git("push", "--quiet", "origin", "refs/tags/v0.12.1")
	git("checkout", "--quiet", "main")
	return r
}

// verifyEnv is what scripts/verify-release.sh needs on top of the process
// environment: the fakes on PATH, and a wait bounded in seconds rather than in
// the flag's whole minutes.
func verifyEnv(t *testing.T, r *releaseRepo, g *ghState, seconds int) []string {
	t.Helper()
	bin := fakeBinDir(t)
	env := []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"VERIFY_RELEASE_POLL_SECONDS=1",
		fmt.Sprintf("VERIFY_RELEASE_TIMEOUT_SECONDS=%d", seconds),
		"RELEASE_RETRY_SECONDS=0",
		"GITHUB_REPOSITORY=" + repoSlug(r.bare),
	}
	return append(env, g.env...)
}

// runVerify runs scripts/verify-release.sh from the fixture checkout, with the
// fake gh and docker answering and its wait bounded to seconds.
func runVerify(t *testing.T, r *releaseRepo, g *ghState, seconds int, args ...string) runResult {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(r.local, "scripts", "verify-release.sh")}, args...)...)
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(), verifyEnv(t, r, g, seconds)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return runResult{out: out.String(), err: err}
}

// verify runs verify-release.sh from a hotfix fixture's checkout, which is the
// release checkout runVerify takes.
func (r *hotfixRepo) verify(t *testing.T, g *ghState, seconds int, args ...string) runResult {
	t.Helper()
	return runVerify(t, r.releaseRepo, g, seconds, args...)
}

func (r runResult) requireVerifySuccess(t *testing.T) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("verify-release.sh failed: %v\n%s", r.err, r.out)
	}
}

func (r runResult) requireVerifyFailureContaining(t *testing.T, substr string) {
	t.Helper()
	if r.err == nil {
		t.Fatalf("verify-release.sh unexpectedly succeeded:\n%s", r.out)
	}
	if !strings.Contains(r.out, substr) {
		t.Fatalf("verify-release.sh output does not contain %q:\n%s", substr, r.out)
	}
}

// TestVerifyReleasePassesWhenEveryWorkflowIsGreen: the whole verification of a
// patch tag -- the runs the tag started, the images its tree published, and the
// two patch rules -- and nothing changed on origin.
func TestVerifyReleasePassesWhenEveryWorkflowIsGreen(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks())
	before := testutil.Git(t, r.local, "ls-remote", "origin")
	res := r.verify(t, g, 60, "v0.12.1")
	res.requireVerifySuccess(t)
	for _, want := range []string{
		"waiting for every workflow the v0.12.1 push started",
		"verify-release: v0.12.1",
		"runs:   every workflow the tag started (release images console-windows, and any other run on the tag)",
		"ghcr.io/dimipaun/fugaro-history:0.12.1",
		"ghcr.io/dimipaun/fugaro-base:0.12.1",
		"ghcr.io/dimipaun/fugaro-credentials:0.12.1",
		"patch:  2 cherry-picked commit(s), each on origin/main",
	} {
		if !strings.Contains(res.out, want) {
			t.Errorf("the verification does not say %q:\n%s", want, res.out)
		}
	}
	if after := testutil.Git(t, r.local, "ls-remote", "origin"); after != before {
		t.Errorf("the verification changed origin:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestVerifyReleaseChecksTheMajorTagOnlyAtTheTopOfItsMajor: images.yml moves
// `<major>` when, and only when, the tag is the top of its major, so a backport
// tag publishes (and is verified for) `<version>` alone. Both reads are the
// tagged tree's own, and the fake docker records every reference it is asked
// about.
func TestVerifyReleaseChecksTheMajorTagOnlyAtTheTopOfItsMajor(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks())
	g = g.with("FAKE_DOCKER_LOG=" + filepath.Join(g.dir, "docker_log"))

	// v0.12.1 is the newest release of the line, so it is the top of its major:
	// the moving tags are its own and are checked.
	res := r.verify(t, g, 60, "v0.12.1")
	res.requireVerifySuccess(t)
	checked := g.read(t, "docker_log")
	for _, ref := range []string{"fugaro-history:0\n", "fugaro-history:0.12.1\n", "fugaro-base:0\n", "fugaro-base:0.12.1\n"} {
		if !strings.Contains(checked, ref) {
			t.Errorf("the verification did not check %q, and 0.12.1 is the top of its major:\n%s", strings.TrimSuffix(ref, "\n"), checked)
		}
	}

	// v0.12.2 is tagged on main after it: 0.12.1 is then a backport, and the
	// moving tags belong to the newer release.
	writeHighlights(t, r.local, "0.12.2", "- A later fix.\n")
	testutil.Git(t, r.local, "add", "-A")
	testutil.Git(t, r.local, "commit", "--quiet", "-m", "release: v0.12.2")
	testutil.Git(t, r.local, "tag", "-a", "v0.12.2", "-m", "Fugaro v0.12.2")
	testutil.Git(t, r.local, "push", "--quiet", "origin", "main", "refs/tags/v0.12.2")
	if err := os.Remove(filepath.Join(g.dir, "docker_log")); err != nil {
		t.Fatal(err)
	}
	res = r.verify(t, g, 60, "v0.12.1")
	res.requireVerifySuccess(t)
	checked = g.read(t, "docker_log")
	if strings.Contains(checked, "fugaro-history:0\n") || strings.Contains(checked, "fugaro-base:0\n") {
		t.Errorf("the verification checked the moving major tag for a backport:\n%s", checked)
	}
	if !strings.Contains(checked, "fugaro-history:0.12.1\n") {
		t.Errorf("the verification did not check the backport's own images:\n%s", checked)
	}
}

// TestVerifyReleaseCountsEveryWorkflowOnTheTag: release.yml and images.yml are
// named because a run registers a few seconds after the push; any other
// workflow the tag started is held to the same rule, and the runs of the same
// workflows on main (a schedule, a dispatch) are not this release's.
func TestVerifyReleaseCountsEveryWorkflowOnTheTag(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks()).with("FAKE_GH_RUN_OTHER=console-tests:failure")
	res := r.verify(t, g, 60, "v0.12.1")
	res.requireVerifyFailureContaining(t, "console-tests concluded failure")
}

// TestVerifyReleaseRefusesARedImagesRun: a green release.yml with a red
// images.yml is not a release.
func TestVerifyReleaseRefusesARedImagesRun(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks()).with("FAKE_GH_RUN_IMAGES=failure")
	res := r.verify(t, g, 60, "v0.12.1")
	res.requireVerifyFailureContaining(t, "images concluded failure")
	// The images themselves are never read: a conclusive failure ends it.
	if strings.Contains(res.out, "ghcr.io/") {
		t.Errorf("the verification went on to the images after a red run:\n%s", res.out)
	}
}

// TestVerifyReleaseWaitsForARunThatHasNotRegistered: a run registers a few
// seconds after the push, so a missing one is waited for, bounded by the
// timeout, and never read as "nothing failed".
func TestVerifyReleaseWaitsForARunThatHasNotRegistered(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks()).with("FAKE_GH_RUN_IMAGES=missing")
	res := r.verify(t, g, 4, "v0.12.1")
	res.requireVerifyFailureContaining(t, "is not released: images has no run on v0.12.1 yet")
	if !strings.Contains(res.out, "still waiting: images has no run") {
		t.Errorf("the missing run was not waited for:\n%s", res.out)
	}
}

// TestVerifyReleaseWaitsForTheWindowsRunToo: the console's Windows pack,
// console-windows.yml, carries the same `on: push: tags:` filter as release.yml
// and images.yml, so a strict vX.Y.Z tag starts it too and it is one of the
// runs the wait names. A verification that only waited for the other two would
// print "is verified" while the Windows run was still registering -- or never
// started -- which is the race the named list exists to close.
func TestVerifyReleaseWaitsForTheWindowsRunToo(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks()).with("FAKE_GH_RUN_WINDOWS=missing")
	res := r.verify(t, g, 4, "v0.12.1")
	res.requireVerifyFailureContaining(t, "is not released: console-windows has no run on v0.12.1 yet")
	if !strings.Contains(res.out, "still waiting: console-windows has no run") {
		t.Errorf("the missing Windows run was not waited for:\n%s", res.out)
	}
}

// TestVerifyReleaseRefusesAnUnpublishedImage: the tag's tree names an image
// only a login can read, which is what a package left private looks like.
func TestVerifyReleaseRefusesAnUnpublishedImage(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks()).with("FAKE_DOCKER_FAIL=fugaro-history")
	res := r.verify(t, g, 60, "v0.12.1")
	res.requireVerifyFailureContaining(t, "ghcr.io/dimipaun/fugaro-history:0.12.1 cannot be read without a login")
}

// TestVerifyReleaseRefusesAFixThatIsNotOnMain: the trailers the branch's
// cherry-picks carry are what the release is held to, so a fix main does not
// have -- a branch commit picked from a branch that never merged -- is refused.
func TestVerifyReleaseRefusesAFixThatIsNotOnMain(t *testing.T) {
	r := newVerifyFixture(t)
	// main loses the fix: the shape of a fix that only the branch carries.
	seed := testutil.Git(t, r.local, "rev-parse", "v0.12.0^{commit}")
	testutil.Git(t, r.local, "push", "--force", "--quiet", "origin", seed+":refs/heads/main")
	g := newGHState(t, r.releaseRepo, greenChecks())
	res := r.verify(t, g, 60, "v0.12.1")
	res.requireVerifyFailureContaining(t, "carries a commit that is not on origin/main")
	// The refusal names the commit it read the trailer of: main is missing both
	// the fix and the bump cherry-picked from it, and the trailers are read in
	// sha order, so either may be the one reported.
	if !strings.Contains(res.out, r.fix) && !strings.Contains(res.out, r.bump) {
		t.Errorf("the refusal names neither of the branch's cherry-picks (%s, %s):\n%s", r.fix, r.bump, res.out)
	}
}

// TestVerifyReleaseRefusesAPatchWithNoCherryPickTrailer: a patch tag whose own
// commits carry no "(cherry picked from commit ...)" trailer has nothing to
// hold it to main, and -x is what makes the check possible.
func TestVerifyReleaseRefusesAPatchWithNoCherryPickTrailer(t *testing.T) {
	r := newHotfixRepo(t)
	// A patch tag on a branch whose commits are all written by hand: nothing on
	// it says where the fix came from, so there is nothing to check it by.
	testutil.Git(t, r.local, "checkout", "--quiet", "-b", "release/0.12", "v0.12.0")
	if err := os.WriteFile(filepath.Join(r.local, "hand-edited.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, r.local, "add", "-A")
	testutil.Git(t, r.local, "commit", "--quiet", "-m", "a fix committed straight on the branch")
	testutil.Git(t, r.local, "push", "--quiet", "origin", "release/0.12")
	testutil.Git(t, r.local, "tag", "-a", "v0.12.2", "-m", "Fugaro v0.12.2")
	testutil.Git(t, r.local, "push", "--quiet", "origin", "refs/tags/v0.12.2")
	testutil.Git(t, r.local, "checkout", "--quiet", "main")
	g := newGHState(t, r.releaseRepo, greenChecks())
	res := r.verify(t, g, 60, "v0.12.2")
	res.requireVerifyFailureContaining(t, "none of its own commits carries a \"(cherry picked from commit <sha>)\" trailer")
}

// TestVerifyReleaseRefusesWhenMainIsBehindTheTag: the branch is never merged
// back, so a plugin version main does not carry leaves main serving a plugin
// older than the published tag (scripts/release-gate.sh refuses it too).
func TestVerifyReleaseRefusesWhenMainIsBehindTheTag(t *testing.T) {
	// A patch whose bump never landed on main: the fix it carries is on main,
	// and main's plugin is still the one the line shipped with. The branch is
	// never merged back, so main would serve a plugin older than the tag.
	r := newReleaseRepo(t)
	git := func(args ...string) string { return strings.TrimSpace(testutil.Git(t, r.local, args...)) }
	git("tag", "-a", "v0.12.0", "-m", "Fugaro v0.12.0")
	git("push", "--quiet", "origin", "refs/tags/v0.12.0")
	if err := os.WriteFile(filepath.Join(r.local, "fix.txt"), []byte("the fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "--quiet", "-m", "fix: the thing that was wrong")
	git("push", "--quiet", "origin", "main")
	fix := git("rev-parse", "HEAD")
	git("checkout", "--quiet", "-b", "release/0.12", "v0.12.0")
	git("cherry-pick", "-x", fix)
	git("push", "--quiet", "origin", "release/0.12")
	git("tag", "-a", "v0.12.1", "-m", "Fugaro v0.12.1")
	git("push", "--quiet", "origin", "refs/tags/v0.12.1")
	git("checkout", "--quiet", "main")

	g := newGHState(t, r, greenChecks())
	res := runVerify(t, r, g, 60, "v0.12.1")
	res.requireVerifyFailureContaining(t, "main's plugin/.claude-plugin/plugin.json says '0.11.4' and v0.12.1 is 0.12.1")
}

// TestVerifyReleaseRefusesATagThatIsNotOnOrigin and the two shape refusals
// below are the reads before any of the above: the tag has to be a CLI release
// tag, and it has to be on origin at the commit this checkout holds.
func TestVerifyReleaseRefusesATagThatIsNotOnOrigin(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks())
	res := r.verify(t, g, 60, "v0.12.9")
	res.requireVerifyFailureContaining(t, "tag v0.12.9 is not on origin")
	if n := countLog(t, g, "api"); n != 0 {
		t.Errorf("a tag origin does not have reached the runs api (%d call(s))", n)
	}
}

func TestVerifyReleaseRefusesAStreamsTag(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks())
	for _, tag := range []string{"console-v1.2.3", "mobile-v1.2.3", "1.2.3", "v1.2"} {
		res := r.verify(t, g, 60, tag)
		res.requireVerifyFailureContaining(t, "is not a strict vX.Y.Z")
	}
}

func TestVerifyReleaseRefusesArgumentsItCannotRead(t *testing.T) {
	r := newVerifyFixture(t)
	g := newGHState(t, r.releaseRepo, greenChecks())
	r.verify(t, g, 60).requireVerifyFailureContaining(t, "usage: ")
	r.verify(t, g, 60, "v0.12.1", "v0.12.2").requireVerifyFailureContaining(t, "unexpected argument")
	r.verify(t, g, 60, "v0.12.1", "--timeout-minutes", "0").requireVerifyFailureContaining(t, "whole number of minutes")
	r.verify(t, g, 60, "v0.12.1", "--nope").requireVerifyFailureContaining(t, "unknown flag")
}
