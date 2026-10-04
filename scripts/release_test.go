// Package scripts_test exercises scripts/release.sh end to end against a
// local bare repository standing in for origin, and a fake `gh` on PATH
// that performs a real squash merge against that repo so step 4's
// verification (fetching the merge commit, checking the plugin bump and
// the release gate) runs for real too.
package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		"FAKE_GH_REPO=o/r",
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

// TestPRClosedUnmerged checks a closed-without-merging PR stops the script
// with a message naming it, rather than hanging or tagging anything.
func TestPRClosedUnmerged(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	g.write(t, "pr_number", "7")
	g.write(t, "pr_state", "CLOSED")

	res := runRelease(t, r, g, "", "1.0.0")
	res.requireFailureContaining(t, "#7")
	res.requireFailureContaining(t, "closed without merging")
	if out := testutil.Git(t, r.bare, "tag", "--list", "v1.0.0"); out != "" {
		t.Errorf("a closed PR must not lead to a tag: %s", out)
	}
}

// TestPRFailedCheckStopsWaiting checks a failing status check on the
// release PR is reported by name rather than waited out until timeout.
func TestPRFailedCheckStopsWaiting(t *testing.T) {
	r := newReleaseRepo(t)
	g := newGHState(t, r, greenChecks())
	g.write(t, "pr_check_fail", "rules")

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
