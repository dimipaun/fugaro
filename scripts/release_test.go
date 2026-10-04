// Package scripts_test exercises scripts/release.sh end to end against a
// local bare repository standing in for origin, and a fake `gh` on PATH
// that performs a real squash merge against that repo so step 4's
// verification (fetching the merge commit, checking the plugin bump and
// the release gate) runs for real too.
package scripts_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// releaseRepo is a bare "origin" seeded with this checkout's scripts/ and
// plugin/ trees, and a clone of it (on main) to run release.sh from.
type releaseRepo struct {
	bare  string
	local string
}

func newReleaseRepo(t *testing.T) *releaseRepo {
	t.Helper()
	testutil.IsolateGit(t)
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	testutil.Git(t, root, "init", "--quiet", "--bare", "-b", "main", bare)
	testutil.Git(t, bare, "config", "receive.autogc", "false")
	testutil.Git(t, bare, "config", "maintenance.auto", "false")

	seed := filepath.Join(root, "seed")
	testutil.Git(t, root, "clone", "--quiet", bare, seed)
	moduleRoot := testutil.ModuleRoot()
	for _, name := range []string{"scripts", "plugin"} {
		if err := os.CopyFS(filepath.Join(seed, name), os.DirFS(filepath.Join(moduleRoot, name))); err != nil {
			t.Fatalf("copying %s: %v", name, err)
		}
	}
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "seed")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")

	local := filepath.Join(root, "work")
	testutil.Git(t, root, "clone", "--quiet", bare, local)
	return &releaseRepo{bare: bare, local: local}
}

// ghState is the directory backing scripts/testdata/fake-gh.sh's state
// across the several `gh` invocations one release.sh run makes.
type ghState struct {
	dir string
	env []string
}

func newGHState(t *testing.T, r *releaseRepo, checks map[string]string) *ghState {
	t.Helper()
	dir := t.TempDir()
	env := []string{
		"FAKE_GH_DIR=" + dir,
		"FAKE_GH_ORIGIN=" + r.bare,
		"FAKE_GH_REPO=" + repoSlug(r.bare),
	}
	for name, concl := range checks {
		env = append(env, "FAKE_GH_CHECK_"+strings.ToUpper(name)+"="+concl)
	}
	return &ghState{dir: dir, env: env}
}

func (g *ghState) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (g *ghState) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(g.dir, name))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (g *ghState) log(t *testing.T) string { return g.read(t, "log") }

// countLog returns how many logged gh invocations start with prefix (one
// per line, as fake-gh.sh writes "$*").
func countLog(t *testing.T, g *ghState, prefix string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(g.log(t), "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// fakeBinDir returns a directory on PATH ahead of the real one, holding the
// fake gh (and nothing else: git, bash, sed, awk, sort, date stay real).
func fakeBinDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join(testutil.ModuleRoot(), "scripts", "testdata", "fake-gh.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// repoSlug is the owner/name release.sh derives from origin's URL: the last
// two path components, minus ".git" (origin here is a local path).
func repoSlug(bare string) string {
	return filepath.Base(filepath.Dir(bare)) + "/" + strings.TrimSuffix(filepath.Base(bare), ".git")
}

type runResult struct {
	out string
	err error
}

func (r runResult) requireSuccess(t *testing.T) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("release.sh failed: %v\n%s", r.err, r.out)
	}
}

func (r runResult) requireFailureContaining(t *testing.T, substr string) {
	t.Helper()
	if r.err == nil {
		t.Fatalf("release.sh unexpectedly succeeded:\n%s", r.out)
	}
	if !strings.Contains(r.out, substr) {
		t.Fatalf("release.sh output does not contain %q:\n%s", substr, r.out)
	}
}

// runRelease runs scripts/release.sh from r.local with the fake gh on PATH,
// g's state, stdin, and extra arguments.
func runRelease(t *testing.T, r *releaseRepo, g *ghState, stdin string, args ...string) runResult {
	t.Helper()
	bin := fakeBinDir(t)
	cmd := exec.Command("bash", append([]string{filepath.Join(r.local, "scripts", "release.sh")}, args...)...)
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1")
	cmd.Env = append(cmd.Env, g.env...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return runResult{out: out.String(), err: err}
}

func greenChecks() map[string]string {
	return map[string]string{"test": "success", "terraform": "success", "rules": "success"}
}

// TestPreconditionFailures checks every precondition stops the script with
// a clear message before anything is created, in the order docs/release.md
// (via the task) specifies.
func TestPreconditionFailures(t *testing.T) {
	cases := []struct {
		name    string
		version string
		mutate  func(t *testing.T, r *releaseRepo, g *ghState)
		want    string
	}{
		{
			name:    "not strict semver (leading v)",
			version: "v1.0.0",
			mutate:  func(t *testing.T, r *releaseRepo, g *ghState) {},
			want:    "not strict SemVer",
		},
		{
			name:    "not strict semver (pre-release)",
			version: "1.0.0-rc1",
			mutate:  func(t *testing.T, r *releaseRepo, g *ghState) {},
			want:    "not strict SemVer",
		},
		{
			name:    "not strict semver (leading zero)",
			version: "01.2.3",
			mutate:  func(t *testing.T, r *releaseRepo, g *ghState) {},
			want:    "not strict SemVer",
		},
		{
			name:    "dirty working tree",
			version: "1.0.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				if err := os.WriteFile(filepath.Join(r.local, "dirty.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "working tree is not clean",
		},
		{
			name:    "not on main",
			version: "1.0.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				testutil.Git(t, r.local, "switch", "--quiet", "-c", "other")
			},
			want: "not main",
		},
		{
			name:    "main diverged from origin",
			version: "1.0.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				testutil.Git(t, r.local, "commit", "--quiet", "--allow-empty", "-m", "local only")
			},
			want: "is not origin/main",
		},
		{
			name:    "gh not authenticated",
			version: "1.0.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				g.env = append(g.env, "FAKE_GH_UNAUTH=1")
			},
			want: "gh is not authenticated",
		},
		{
			name:    "tag already exists",
			version: "1.0.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				testutil.Git(t, r.local, "tag", "-a", "v1.0.0", "-m", "x")
			},
			want: "already exists",
		},
		{
			name:    "version not greater than latest tag",
			version: "1.0.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				testutil.Git(t, r.local, "tag", "-a", "v2.0.0", "-m", "x")
			},
			want: "is not greater than",
		},
		{
			name:    "red check",
			version: "1.0.0",
			mutate: func(t *testing.T, r *releaseRepo, g *ghState) {
				g.env = append(g.env, "FAKE_GH_CHECK_TEST=failed")
			},
			want: "required check 'test' is failed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			c.mutate(t, r, g)
			res := runRelease(t, r, g, "", c.version)
			res.requireFailureContaining(t, c.want)
			if countLog(t, g, "pr create") != 0 {
				t.Errorf("a precondition failure must not create a pull request:\n%s", res.out)
			}
		})
	}
}

func TestGHNotInstalled(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "1.0.0")
	cmd.Dir = r.local
	// A PATH with no gh at all (just enough for git, bash's builtins, etc).
	realPath := os.Getenv("PATH")
	cmd.Env = append(os.Environ(), "PATH="+realPath)
	cmd.Env = append(cmd.Env, g.env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// Only meaningful if gh truly isn't already on PATH; skip otherwise.
	if _, err := exec.LookPath("gh"); err == nil {
		t.Skip("gh is installed on this machine's real PATH")
	}
	err := cmd.Run()
	if err == nil || !strings.Contains(out.String(), "gh is not installed") {
		t.Fatalf("want 'gh is not installed', got err=%v out=%s", err, out.String())
	}
}

// TestDryRunCreatesNothing checks --dry-run stops after the plan, with no
// branch, commit, PR or tag.
func TestDryRunCreatesNothing(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "", "1.0.0", "--dry-run")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "Dry run for v1.0.0") {
		t.Fatalf("expected a dry-run plan, got:\n%s", res.out)
	}
	if countLog(t, g, "pr ") != 0 {
		t.Errorf("dry-run must not touch any pull request; got:\n%s", g.log(t))
	}
	if out := testutil.Git(t, r.bare, "branch", "--list", "release/v1.0.0"); out != "" {
		t.Errorf("dry-run created a branch: %s", out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out != "" {
		t.Errorf("dry-run created a tag: %s", out)
	}
}

// TestHappyPathAnsweringYes drives release.sh through a real bump, PR,
// squash merge and, after answering "y", the tag — checking that exactly
// one branch, one commit and one PR were created, and the tag matches the
// merge commit.
func TestHappyPathAnsweringYes(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	initialMain := testutil.Git(t, r.bare, "rev-parse", "main")

	res := runRelease(t, r, g, "y\n", "1.0.0")
	res.requireSuccess(t)

	if n := countLog(t, g, "pr create"); n != 1 {
		t.Errorf("want exactly one 'pr create', got %d:\n%s", n, g.log(t))
	}
	branchCommits := testutil.Git(t, r.bare, "rev-list", "--count", initialMain+"..refs/heads/release/v1.0.0")
	if branchCommits != "1" {
		t.Errorf("want exactly one commit on the release branch, got %s", branchCommits)
	}
	subject := testutil.Git(t, r.bare, "log", "-1", "--format=%s", "refs/heads/release/v1.0.0")
	if subject != "release: v1.0.0" {
		t.Errorf("unexpected commit subject: %s", subject)
	}
	body := testutil.Git(t, r.bare, "log", "-1", "--format=%b", "refs/heads/release/v1.0.0")
	if !strings.Contains(body, "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>") {
		t.Errorf("commit body is missing the attribution trailer:\n%s", body)
	}

	tagSHA := testutil.Git(t, r.bare, "rev-parse", "v1.0.0^{commit}")
	mainSHA := testutil.Git(t, r.bare, "rev-parse", "main")
	if tagSHA != mainSHA {
		t.Errorf("tag v1.0.0 (%s) does not point at main (%s)", tagSHA, mainSHA)
	}
	if mainSHA == initialMain {
		t.Errorf("main did not advance past the merge")
	}
}

// TestAnsweringNoLeavesNoTag checks the merge still happens (the PR is real
// and auto-merge already landed it) but declining the final prompt leaves
// no tag, since the default answer is no.
func TestAnsweringNoLeavesNoTag(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "n\n", "1.0.0")
	if res.err == nil {
		t.Fatalf("expected a non-zero exit when declining the tag prompt:\n%s", res.out)
	}
	if !strings.Contains(res.out, "aborted") {
		t.Errorf("expected an 'aborted' message, got:\n%s", res.out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out != "" {
		t.Errorf("declining the prompt must not create a tag: %s", out)
	}
	// The merge itself did happen (auto-merge doesn't wait for a human).
	mainSHA := testutil.Git(t, r.bare, "rev-parse", "main")
	subject := testutil.Git(t, r.bare, "log", "-1", "--format=%s", mainSHA)
	if subject != "release: v1.0.0" {
		t.Errorf("expected the release PR to have merged before the prompt, got HEAD subject %q", subject)
	}
}

// TestEmptyAnswerDefaultsToNo checks the bare prompt default (no input) is
// "no", per docs/release.md's [y/N].
func TestEmptyAnswerDefaultsToNo(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "\n", "1.0.0")
	if res.err == nil {
		t.Fatalf("expected a non-zero exit on an empty answer:\n%s", res.out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out != "" {
		t.Errorf("an empty answer must not create a tag: %s", out)
	}
}

// TestYesFlagSkipsPrompt checks --yes tags without reading stdin at all.
func TestYesFlagSkipsPrompt(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireSuccess(t)
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out == "" {
		t.Errorf("--yes should have created the tag; output:\n%s", res.out)
	}
}

// TestResumeAfterMerge simulates a prior run that got as far as the PR
// merging on origin but was interrupted before the tag, and checks a
// fresh run finds that state and continues straight to tagging, creating
// no second pull request.
func TestResumeAfterMerge(t *testing.T) {
	r := newReleaseRepo(t)

	// Stand in for "the PR already merged": bump and commit directly on a
	// clone, then fast-forward origin/main, as the earlier run's squash
	// merge would have.
	clone := filepath.Join(t.TempDir(), "premerge")
	testutil.Git(t, filepath.Dir(clone), "clone", "--quiet", r.bare, clone)
	bumpCmd := exec.Command("bash", filepath.Join(clone, "scripts", "bump-plugin-version.sh"), "1.0.0")
	bumpCmd.Dir = clone
	if out, err := bumpCmd.CombinedOutput(); err != nil {
		t.Fatalf("bump: %v\n%s", err, out)
	}
	testutil.Git(t, clone, "add", "-A")
	testutil.Git(t, clone, "commit", "--quiet", "-m", "release: v1.0.0", "-m", "Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>")
	testutil.Git(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	mergeSHA := testutil.Git(t, clone, "rev-parse", "HEAD")

	// A fresh checkout, as if the user re-ran release.sh after pulling.
	local2 := filepath.Join(t.TempDir(), "work2")
	testutil.Git(t, filepath.Dir(local2), "clone", "--quiet", r.bare, local2)
	r2 := &releaseRepo{bare: r.bare, local: local2}

	g := newGHState(t, r2, greenChecks())
	g.write(t, "pr_number", "1")
	g.write(t, "pr_state", "MERGED")
	g.write(t, "pr_merge_sha", mergeSHA)

	res := runRelease(t, r2, g, "y\n", "1.0.0")
	res.requireSuccess(t)

	if n := countLog(t, g, "pr create"); n != 0 {
		t.Errorf("resuming an already-merged PR must not create a new one, got %d:\n%s", n, g.log(t))
	}
	tagSHA := testutil.Git(t, r.bare, "rev-parse", "v1.0.0^{commit}")
	if tagSHA != mergeSHA {
		t.Errorf("tag v1.0.0 (%s) does not point at the pre-merged commit (%s)", tagSHA, mergeSHA)
	}
}

// TestResumeRebuildsStaleLocalBranch simulates an interrupted run that left
// a local, never-pushed release/vX.Y.Z branch behind, with main having
// since advanced (the user pulled before re-running). A fresh run must
// rebuild that branch on top of the new main rather than basing the PR on
// the stale history it already had checked out.
func TestResumeRebuildsStaleLocalBranch(t *testing.T) {
	r := newReleaseRepo(t)

	testutil.Git(t, r.local, "switch", "--quiet", "-c", "release/v1.0.0")
	testutil.Git(t, r.local, "switch", "--quiet", "main")

	// Main advances independently of the stale local branch above.
	advancer := filepath.Join(t.TempDir(), "advancer")
	testutil.Git(t, filepath.Dir(advancer), "clone", "--quiet", r.bare, advancer)
	testutil.Git(t, advancer, "commit", "--quiet", "--allow-empty", "-m", "unrelated main commit")
	testutil.Git(t, advancer, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	newMain := testutil.Git(t, advancer, "rev-parse", "HEAD")

	// As if the user had pulled main before re-running: local main now
	// matches origin/main, but the stale local release branch does not.
	testutil.Git(t, r.local, "fetch", "--quiet", "origin", "main")
	testutil.Git(t, r.local, "merge", "--quiet", "--ff-only", "origin/main")

	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "y\n", "1.0.0")
	res.requireSuccess(t)

	if !strings.Contains(res.out, "predates main") {
		t.Errorf("expected a message about rebuilding the stale branch, got:\n%s", res.out)
	}
	branchCommits := testutil.Git(t, r.bare, "rev-list", "--count", newMain+"..refs/heads/release/v1.0.0")
	if branchCommits != "1" {
		t.Errorf("want exactly one commit on the rebuilt release branch on top of the new main, got %s", branchCommits)
	}
	if base := testutil.Git(t, r.bare, "merge-base", "v1.0.0", newMain); base != newMain {
		t.Errorf("tag v1.0.0 is not based on the new main (%s); merge-base was %s", newMain, base)
	}
}

// TestPRClosedUnmerged checks a closed-without-merging PR stops the script
// with a message naming it, rather than hanging or tagging anything.
func TestPRClosedUnmerged(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	g.write(t, "pr_number", "7")
	g.write(t, "pr_state", "CLOSED")
	pushReleaseBranch(t, r, "1.0.0")
	g.write(t, "pr_branch", "release/v1.0.0")

	res := runRelease(t, r, g, "", "1.0.0")
	res.requireFailureContaining(t, "#7")
	res.requireFailureContaining(t, "closed without merging")
	res.requireFailureContaining(t, "git push origin --delete release/v1.0.0")
	if countLog(t, g, "pr merge") != 0 {
		t.Errorf("a closed PR must never reach 'gh pr merge':\n%s", g.log(t))
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out != "" {
		t.Errorf("a closed PR must not lead to a tag: %s", out)
	}
}

// TestPRFailedCheckStopsWaiting checks a failing status check on the
// release PR is reported by name rather than waited out until timeout.
func TestPRFailedCheckStopsWaiting(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	g.write(t, "pr_check_fail", "rules FAILURE")

	res := runRelease(t, r, g, "", "1.0.0")
	res.requireFailureContaining(t, "check 'rules' failed")
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out != "" {
		t.Errorf("a failed check must not lead to a tag: %s", out)
	}
}

// TestInterruptPrintsResumeInstructions checks Ctrl-C (SIGINT) is handled
// with a message telling the caller how to continue, rather than leaving
// the terminal on a bare stack trace.
func TestInterruptPrintsResumeInstructions(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	// Never let the fake merge the PR, so the run is still in the poll
	// loop's sleep (not exited already) when the signal arrives.
	g.env = append(g.env, "FAKE_GH_MERGE_DELAY_CALLS=1000000")
	bin := fakeBinDir(t)
	cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "1.0.0", "--yes")
	cmd.Dir = r.local
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1")
	cmd.Env = append(cmd.Env, g.env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !strings.Contains(out.String(), "resume") {
		t.Fatalf("expected resume instructions after SIGINT, got:\n%s", out.String())
	}
}

// TestVersionComparisonIsNumeric checks 1.0.10 is accepted as greater than
// an existing v1.0.9 tag: a lexicographic (not numeric) comparison would
// wrongly block this release, since "1.0.10" < "1.0.9" as strings.
func TestVersionComparisonIsNumeric(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	testutil.Git(t, r.local, "tag", "-a", "v1.0.9", "-m", "x")
	res := runRelease(t, r, g, "", "1.0.10", "--dry-run")
	res.requireSuccess(t)
}

// pushReleaseBranch stands in for an earlier interrupted run: it pushes
// release/vVERSION to origin, already carrying the bump commit.
func pushReleaseBranch(t *testing.T, r *releaseRepo, version string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "earlier")
	testutil.Git(t, filepath.Dir(clone), "clone", "--quiet", r.bare, clone)
	testutil.Git(t, clone, "switch", "--quiet", "-c", "release/v"+version)
	bump := exec.Command("bash", filepath.Join(clone, "scripts", "bump-plugin-version.sh"), version)
	bump.Dir = clone
	if out, err := bump.CombinedOutput(); err != nil {
		t.Fatalf("bump: %v\n%s", err, out)
	}
	testutil.Git(t, clone, "add", "-A")
	testutil.Git(t, clone, "commit", "--quiet", "-m", "release: v"+version)
	testutil.Git(t, clone, "push", "--quiet", "origin", "release/v"+version)
}

// installHook installs a pre-receive hook (a shell script body) on origin.
func installHook(t *testing.T, r *releaseRepo, body string) {
	t.Helper()
	hook := filepath.Join(r.bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func removeHook(t *testing.T, r *releaseRepo) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.bare, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
}

func currentBranch(t *testing.T, r *releaseRepo) string {
	t.Helper()
	return testutil.Git(t, r.local, "rev-parse", "--abbrev-ref", "HEAD")
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func (g *ghState) with(env ...string) *ghState {
	return &ghState{dir: g.dir, env: append(append([]string{}, g.env...), env...)}
}

func requireNoTag(t *testing.T, r *releaseRepo, res runResult) {
	t.Helper()
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out != "" {
		t.Errorf("no tag may exist on origin, got %s\n%s", out, res.out)
	}
}

// TestWaitsForPostMergeChecks: right after the merge, CI has not reported
// on the merge commit yet; the gate must wait rather than fail.
func TestWaitsForPostMergeChecks(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_MISSING_CALLS=2")
	res := runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "still waiting: check 'test' is missing") {
		t.Errorf("expected the pending check to be named:\n%s", res.out)
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out == "" {
		t.Errorf("tag missing after the checks went green:\n%s", res.out)
	}
}

func TestPostMergeCheckFailsFast(t *testing.T) {
	r := newReleaseRepo(t)
	// A long timeout: a conclusive failure must not be waited out.
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_POST_TERRAFORM=failed")
	start := time.Now()
	res := runRelease(t, r, g, "y\n", "1.0.0")
	res.requireFailureContaining(t, "required check 'terraform' is failed")
	if time.Since(start) > 30*time.Second {
		t.Errorf("a red check was waited out")
	}
	requireNoTag(t, r, res)
}

func TestPostMergeCheckStillPendingTimesOut(t *testing.T) {
	for _, state := range []string{"pending", "missing", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks()).with("FAKE_GH_POST_RULES="+state, "RELEASE_SH_TIMEOUT_SECONDS=2")
			res := runRelease(t, r, g, "y\n", "1.0.0")
			res.requireFailureContaining(t, "required check 'rules' is "+state)
			requireNoTag(t, r, res)
		})
	}
}

// TestHeadNotMergeCommit: something else landed on main after the release
// merge; the script must refuse rather than tag a different commit.
func TestHeadNotMergeCommit(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_ADVANCE_AFTER_MERGE=1")
	res := runRelease(t, r, g, "y\n", "1.0.0", "--yes")
	res.requireFailureContaining(t, "is not the release merge commit")
	requireNoTag(t, r, res)
}

func TestBumpCheckFailsAfterMerge(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_CORRUPT_MERGE=1")
	res := runRelease(t, r, g, "y\n", "1.0.0", "--yes")
	res.requireFailureContaining(t, "want 1.0.0")
	requireNoTag(t, r, res)
}

func TestRemoteOnlyExistingTag(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", r.bare, other)
	testutil.Git(t, other, "tag", "-a", "v1.0.0", "-m", "x")
	testutil.Git(t, other, "push", "--quiet", "origin", "refs/tags/v1.0.0")
	res := runRelease(t, r, g, "y\n", "1.0.0")
	res.requireFailureContaining(t, "already exists on origin")
	if countLog(t, g, "pr create") != 0 {
		t.Errorf("no PR may be created:\n%s", res.out)
	}
}

func TestTimeoutMinutesEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no value", []string{"1.0.0", "--timeout-minutes"}},
		{"empty", []string{"1.0.0", "--timeout-minutes", ""}},
		{"zero", []string{"1.0.0", "--timeout-minutes", "0"}},
		{"negative", []string{"1.0.0", "--timeout-minutes", "-5"}},
		{"non-numeric", []string{"1.0.0", "--timeout-minutes", "abc"}},
		{"fraction", []string{"1.0.0", "--timeout-minutes", "1.5"}},
		{"leading zero", []string{"1.0.0", "--timeout-minutes", "05"}},
		{"multi-line", []string{"1.0.0", "--timeout-minutes", "5\nx"}},
		{"swallows the next flag", []string{"--timeout-minutes", "--yes", "1.0.0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			res := runRelease(t, r, g, "", c.args...)
			if exitCode(res.err) != 2 {
				t.Fatalf("want exit 2, got %v:\n%s", res.err, res.out)
			}
			if !strings.Contains(res.out, "--timeout-minutes") {
				t.Errorf("the message must name the flag:\n%s", res.out)
			}
		})
	}
	t.Run("valid", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		runRelease(t, r, g, "", "1.0.0", "--timeout-minutes", "7", "--dry-run").requireSuccess(t)
	})
}

func TestArgumentErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"1.0.0", "--bogus"}, "unknown flag: --bogus"},
		{"unknown short flag", []string{"-y", "1.0.0"}, "unknown flag: -y"},
		{"two versions", []string{"1.0.0", "1.0.1"}, "unexpected argument"},
		{"no version", nil, "usage:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			res := runRelease(t, r, g, "", c.args...)
			if exitCode(res.err) != 2 || !strings.Contains(res.out, c.want) {
				t.Fatalf("want exit 2 and %q, got %v:\n%s", c.want, res.err, res.out)
			}
		})
	}
}

func TestStrictSemVerRejectsMultiLine(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g, "", "1.0.0\nfoo")
	res.requireFailureContaining(t, "not strict SemVer")

	root := testutil.ModuleRoot()
	for _, c := range []struct {
		script string
		args   []string
		code   int
	}{
		{"bump-plugin-version.sh", []string{"1.0.0\n1.0.0"}, 2},
		{"bump-plugin-version.sh", []string{"--check", "1.0.0\nx"}, 2},
		{"bump-plugin-version.sh", []string{"01.0.0"}, 2},
		{"release-gate.sh", []string{"v1.0.0\nx"}, 1},
		{"release-gate.sh", []string{"v01.0.0"}, 1},
	} {
		cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", c.script)}, c.args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if exitCode(err) != c.code {
			t.Errorf("%s %q: want exit %d, got %v\n%s", c.script, c.args, c.code, err, out)
		}
	}
}

// TestLatestTagIgnoresNonStrictTags: -rc and four-part tags never count as
// "the latest release".
func TestLatestTagIgnoresNonStrictTags(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	for _, tg := range []string{"v9.0.0-rc1", "v9.0.0.1", "v09.0.0", "vfoo"} {
		testutil.Git(t, r.local, "tag", "-a", tg, "-m", "x")
	}
	runRelease(t, r, g, "", "1.0.0", "--dry-run").requireSuccess(t)
}

func TestOriginMustMatchGHRepo(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_REPO=other/thing")
	res := runRelease(t, r, g, "", "1.0.0")
	res.requireFailureContaining(t, "gh targets other/thing")
	if countLog(t, g, "pr create") != 0 {
		t.Errorf("no PR may be created:\n%s", res.out)
	}
}

func TestFailedBranchPushLeavesMainAndResumes(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	installHook(t, r, `while read old new ref; do case "$ref" in refs/heads/release/*) echo "rejected" >&2; exit 1;; esac; done`)
	res := runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireFailureContaining(t, "pushing release/v1.0.0 failed")
	if b := currentBranch(t, r); b != "main" {
		t.Fatalf("checkout left on %q, want main", b)
	}
	removeHook(t, r)
	runRelease(t, r, g, "", "1.0.0", "--yes").requireSuccess(t)
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out == "" {
		t.Errorf("the re-run did not finish the release")
	}
}

func TestFailedPRCreateLeavesMainAndResumes(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	res := runRelease(t, r, g.with("FAKE_GH_PR_CREATE_FAIL=1"), "", "1.0.0", "--yes")
	res.requireFailureContaining(t, "'gh pr create' failed")
	if b := currentBranch(t, r); b != "main" {
		t.Fatalf("checkout left on %q, want main", b)
	}
	res = runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "resuming pushed branch") {
		t.Errorf("expected to resume the pushed branch:\n%s", res.out)
	}
}

// TestSignalsLeaveMain: a signal while the release branch is checked out
// (here: during the branch push) ends on main with the resume message.
func TestSignalsLeaveMain(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			r := newReleaseRepo(t)
			g := newGHState(t, r, greenChecks())
			marker := filepath.Join(t.TempDir(), "in-push")
			installHook(t, r, fmt.Sprintf(`touch %s; sleep 2`, marker))
			bin := fakeBinDir(t)
			cmd := exec.Command("bash", filepath.Join(r.local, "scripts", "release.sh"), "1.0.0", "--yes")
			cmd.Dir = r.local
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "RELEASE_SH_POLL_SECONDS=1")
			cmd.Env = append(cmd.Env, g.env...)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(20 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatalf("never reached the push:\n%s", out.String())
				}
				time.Sleep(20 * time.Millisecond)
			}
			if b := currentBranch(t, r); b != "release/v1.0.0" {
				t.Fatalf("expected to be on the release branch mid-push, on %q", b)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			if b := currentBranch(t, r); b != "main" {
				t.Errorf("checkout left on %q, want main", b)
			}
			if !strings.Contains(out.String(), "Re-run") {
				t.Errorf("expected resume instructions:\n%s", out.String())
			}
		})
	}
}

// failedTagPush runs a release whose final tag push is rejected by origin.
func failedTagPush(t *testing.T) (*releaseRepo, *ghState) {
	t.Helper()
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	installHook(t, r, `while read old new ref; do case "$ref" in refs/tags/*) echo "rejected" >&2; exit 1;; esac; done`)
	res := runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireFailureContaining(t, "git push origin refs/tags/v1.0.0")
	if b := currentBranch(t, r); b != "main" {
		t.Fatalf("checkout left on %q, want main", b)
	}
	if strings.Contains(res.out, "tag v1.0.0 pushed") {
		t.Errorf("must not claim the tag was pushed:\n%s", res.out)
	}
	if out := testutil.Git(t, r.local, "tag", "--list", "v1.0.0"); out == "" {
		t.Errorf("the local tag should remain for a retry")
	}
	removeHook(t, r)
	return r, g
}

func TestFailedTagPushThenRerunPushesLocalTag(t *testing.T) {
	r, g := failedTagPush(t)
	res := runRelease(t, r, g, "y\n", "1.0.0")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "exists locally at this commit but is not on origin") {
		t.Errorf("expected the local-tag prompt:\n%s", res.out)
	}
	merge := testutil.Git(t, r.bare, "rev-parse", "main")
	if tag := testutil.Git(t, r.bare, "rev-parse", "v1.0.0^{commit}"); tag != merge {
		t.Errorf("origin's tag is at %s, want the merge commit %s", tag, merge)
	}
	if countLog(t, g, "pr create") != 1 {
		t.Errorf("the re-run must not open another PR:\n%s", g.log(t))
	}
}

func TestLocalTagDeclinedAndWrongCommit(t *testing.T) {
	r, g := failedTagPush(t)
	res := runRelease(t, r, g, "n\n", "1.0.0")
	res.requireFailureContaining(t, "aborted")
	requireNoTag(t, r, res)

	// A local tag on some other commit is refused, never pushed.
	testutil.Git(t, r.local, "tag", "-d", "v1.0.0")
	testutil.Git(t, r.local, "tag", "-a", "v1.0.0", "-m", "x", "HEAD~1")
	res = runRelease(t, r, g, "y\n", "1.0.0")
	res.requireFailureContaining(t, "not the verified merge commit")
	requireNoTag(t, r, res)
}

func TestResumeOpenPR(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	pushReleaseBranch(t, r, "1.0.0")
	g.write(t, "pr_number", "4")
	g.write(t, "pr_state", "OPEN")
	g.write(t, "pr_branch", "release/v1.0.0")
	res := runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireSuccess(t)
	if countLog(t, g, "pr create") != 0 || countLog(t, g, "pr merge --auto --squash 4") != 1 {
		t.Errorf("want auto-merge on the open PR #4 and no new PR:\n%s", g.log(t))
	}
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out == "" {
		t.Errorf("tag missing:\n%s", res.out)
	}
}

func TestExistingRemoteBranchWithoutPR(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	initial := testutil.Git(t, r.bare, "rev-parse", "main")
	pushReleaseBranch(t, r, "1.0.0")
	res := runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireSuccess(t)
	if !strings.Contains(res.out, "resuming pushed branch release/v1.0.0") {
		t.Errorf("expected the pushed branch to be reused:\n%s", res.out)
	}
	if n := testutil.Git(t, r.bare, "rev-list", "--count", initial+"..refs/heads/release/v1.0.0"); n != "1" {
		t.Errorf("want exactly one commit on the branch, got %s", n)
	}
	if countLog(t, g, "pr create") != 1 {
		t.Errorf("want one PR:\n%s", g.log(t))
	}
}

func TestClosedPRWithBranchDeletedOpensNewOne(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	g.write(t, "pr_number", "7")
	g.write(t, "pr_state", "CLOSED")
	res := runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireSuccess(t)
	if countLog(t, g, "pr create") != 1 {
		t.Errorf("the documented recovery (branch deleted) must open a fresh PR:\n%s", g.log(t))
	}
}

func TestAutoMergeUnavailable(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks()).with("FAKE_GH_AUTOMERGE_FAIL=1")
	res := runRelease(t, r, g, "", "1.0.0", "--yes")
	res.requireFailureContaining(t, "could not enable auto-merge")
	res.requireFailureContaining(t, "Merge the pull request yourself, then re-run")
	requireNoTag(t, r, res)
	if b := currentBranch(t, r); b != "main" {
		t.Errorf("checkout left on %q", b)
	}
}

func TestCancelledCheckIsRetriedOnce(t *testing.T) {
	t.Run("transient", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		g.write(t, "pr_check_fail", "rules CANCELLED")
		g.write(t, "pr_check_once", "")
		res := runRelease(t, r, g, "", "1.0.0", "--yes")
		res.requireSuccess(t)
		if !strings.Contains(res.out, "was cancelled") {
			t.Errorf("expected a retry notice:\n%s", res.out)
		}
	})
	t.Run("persistent", func(t *testing.T) {
		r := newReleaseRepo(t)
		g := newGHState(t, r, greenChecks())
		g.write(t, "pr_check_fail", "rules CANCELLED")
		res := runRelease(t, r, g, "", "1.0.0", "--yes")
		res.requireFailureContaining(t, "check 'rules' failed (CANCELLED)")
		if countLog(t, g, "pr checks") < 2 {
			t.Errorf("a cancelled check must be given one more poll:\n%s", g.log(t))
		}
	})
}
