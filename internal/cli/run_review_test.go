package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestRunRecipeReviewModeWritesReviewPRs: --recipe review-only --pr 7
// writes review_prs: [7] in task.json, not branch/pr/previous_run — a
// first-run-shaped task, never a follow-up (design generic-tool §3.6,
// G12).
//
// Mutation (make reviewMode's dispatch in runRun fall through to prSpec
// instead of reviewSpec): this test fails, finding Branch set and
// ReviewPRs empty.
func TestRunRecipeReviewModeWritesReviewPRs(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "review-only", "--pr", "7"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261007-100000-abcd")
	if len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != 7 {
		t.Fatalf("review_prs = %v, want [7]", spec.ReviewPRs)
	}
	if spec.Branch != "" || spec.PR != 0 || spec.PreviousRun != "" {
		t.Fatalf("spec = %+v, want no branch/pr/previous_run", spec)
	}
	if spec.Recipe == nil || spec.Recipe.Name != "review-only" {
		t.Fatalf("recipe = %+v", spec.Recipe)
	}
}

// TestRunRecipeReviewModeIgnoresAnyExistingChain: --recipe review-only --pr
// N reviews PR N directly, even when N already has a Fugaro run chain of
// its own (seedRoot): review mode is not a follow-up, and never consults
// the PR's run history the way continuing it would (resolvePR).
//
// Mutation (route a request with both --recipe and --pr through prSpec
// whenever a chain exists for that PR): this test fails: the chain's own
// recipe or ref would end up embedded instead of review-only's.
func TestRunRecipeReviewModeIgnoresAnyExistingChain(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--recipe", "review-only"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, fuID)
	if spec.Branch != "" || spec.PreviousRun != "" {
		t.Fatalf("spec = %+v, want no branch/previous_run (not a follow-up)", spec)
	}
	if len(spec.ReviewPRs) != 1 || spec.ReviewPRs[0] != 7 {
		t.Fatalf("review_prs = %v, want [7]", spec.ReviewPRs)
	}
}

// TestRunSeveralPRNeedsReviewRecipe: several --pr without a review-mode
// recipe is refused: there is no launch flow for them otherwise (a
// follow-up's --pr is a single pull request).
//
// Mutation (drop the `len(o.pr) > 1` check in runRun's o.recipe == ""
// case): this test fails, finding no error (or a different one) for
// --pr 7 --pr 8 with no --recipe.
func TestRunSeveralPRNeedsReviewRecipe(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "several --pr need a review-mode recipe such as pick", "run", "--repo", "acme/app", "--pr", "7", "--pr", "8")
}

// TestRunSeveralPRWithNonReviewRecipeNeedsReviewRecipe: the same refusal
// applies when --recipe names an ordinary (non-review) recipe, not only
// when --recipe is absent.
func TestRunSeveralPRWithNonReviewRecipeNeedsReviewRecipe(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	wantRefused(t, f, "several --pr need a review-mode recipe such as pick", "run", "--repo", "acme/app", "--recipe", "solo", "--pr", "7", "--pr", "8")
}

// TestRunAtMostFourPR: five --pr is refused outright, before any cloud
// work or recipe resolution.
//
// Mutation (drop the `len(o.pr) > 4` check): this test fails, finding no
// error.
func TestRunAtMostFourPR(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "at most 4", "run", "--repo", "acme/app", "--pr", "1", "--pr", "2", "--pr", "3", "--pr", "4", "--pr", "5")
}

// TestRunPickWritesSeveralReviewPRs: --recipe pick --pr A --pr B writes
// both, in order.
func TestRunPickWritesSeveralReviewPRs(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "pick", "--pr", "7", "--pr", "9"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261007-100000-abcd")
	if len(spec.ReviewPRs) != 2 || spec.ReviewPRs[0] != 7 || spec.ReviewPRs[1] != 9 {
		t.Fatalf("review_prs = %v, want [7 9]", spec.ReviewPRs)
	}
}

// TestRunReviewOnlyRefusesSeveralPR: review-only reviews exactly one pull
// request (it has no "keep" verdict to give between several); unlike a
// bare --recipe-less several --pr, naming review-only explicitly must
// still be refused rather than silently reviewing only the first or
// somehow comparing them.
//
// Mutation (drop the `recipe.ReviewPRRange` check in runRun's reviewRecipe
// branch): this test fails, finding no error for --recipe review-only with
// two --pr.
func TestRunReviewOnlyRefusesSeveralPR(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	wantRefused(t, f, "review-only needs exactly 1 --pr, got 2", "run", "--repo", "acme/app", "--recipe", "review-only", "--pr", "7", "--pr", "9")
}

// TestRunPickRefusesSinglePR: pick compares several pull requests; one
// --pr has nothing to compare it against, so it is refused rather than
// run as a review-only-shaped review with no "keep" semantics.
//
// Mutation (same as TestRunReviewOnlyRefusesSeveralPR): this test fails,
// finding no error for --recipe pick with one --pr.
func TestRunPickRefusesSinglePR(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	wantRefused(t, f, "pick needs 2 to 4 --pr, got 1", "run", "--repo", "acme/app", "--recipe", "pick", "--pr", "7")
}

// TestRunReviewModeKeepsRefWorkflowFlags: unlike an ordinary --pr
// follow-up, review mode takes --ref and --workflow (it inherits nothing
// from a pull request's own history).
func TestRunReviewModeKeepsRefWorkflowFlags(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "review-only", "--pr", "7", "--ref", "main", "--workflow", "web")
	if err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261007-100000-abcd")
	if spec.Ref != "main" || spec.Workflow != "web" {
		t.Fatalf("spec = %+v", spec)
	}
}

// TestRunReviewModeWithoutPR covers the fourth CLI case of Task 12's plan:
// --recipe review-only without --pr is refused. (The same check also
// covers it in TestRunRefusesReviewModeWithoutPR, run_recipe_test.go; this
// pins the exact invocation the plan names.)
func TestRunReviewModeWithoutPR(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "review-only", "x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "mode: review") {
		t.Fatalf("err = %v", err)
	}
}

// TestRunReviewModeRefusesLeftoverAllowForks (owner ruling 2026-10-12):
// review.allow_forks is removed outright; a local checkout whose
// fugaro.yaml still sets it fails to launch at all (the strict parser
// refuses the unknown key), rather than merely losing the reminder.
func TestRunReviewModeRefusesLeftoverAllowForks(t *testing.T) {
	f := newCloudFixture(t)
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent:\n  auth: api-key\n" +
		"review:\n  allow_forks: true\n" +
		"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	other := mustSlug("github", "acme/other")
	writeBuildRecord(t, f, other, "svc", release070)
	if _, _, err := execute(t, "run", "--recipe", "review-only", "--pr", "7", "--run-id", "20261007-100000-abcd"); err == nil {
		t.Fatal("want an error for the removed review.allow_forks key, got none")
	}
}

// release0160 is a job image whose runner acts on review levels
// (reviewLevelsSince, L4.2): the first image a launch that resolves level 3,
// a none, a level's own model or effort, or the owner's floor may run on. A
// test that wants a critical mode's own level 3 to reach a run unchanged needs
// this image, not release0111 — the gate refuses anything older
// (TestRecipesSkewRefusesResolvedL3OnOldImage).
const release0160 = "ghcr.io/dimipaun/fugaro-base:0.16.0"

// TestRunReviewLevelsFlagWritesTaskField: --review-levels decodes the
// document it names strictly into the block the task stores (L1.2, D724), so
// the levels a launcher asks for reach the run as the one field the resolver
// reads. The image is at the release that acts on levels (reviewLevelsSince),
// because a launch that asks for them on an older one is refused — which is
// TestRecipesSkewRefusesReviewLevelsOnOldImage's.
func TestRunReviewLevelsFlagWritesTaskField(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release0160)
	dir := t.TempDir()
	good := filepath.Join(dir, "levels.yaml")
	if err := os.WriteFile(good, []byte("l2:\n  model: deepseek/deepseek-v4-pro\n  effort: high\n  focus: the diff's error handling\nl3:\n  none: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--review-levels", good, "do the thing"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261007-100000-abcd")
	if spec.ReviewLevels == nil || spec.ReviewLevels.L2 == nil || spec.ReviewLevels.L3 == nil || spec.ReviewLevels.L1 != nil {
		t.Fatalf("review_levels = %+v, want l2 and l3 and no l1", spec.ReviewLevels)
	}
	if l2 := spec.ReviewLevels.L2; l2.Model != "deepseek/deepseek-v4-pro" || l2.Effort != "high" || l2.Focus != "the diff's error handling" || l2.Off {
		t.Errorf("review_levels.l2 = %+v", l2)
	}
	if l3 := spec.ReviewLevels.L3; !l3.Off || l3.Model != "" || l3.Effort != "" || l3.Focus != "" {
		t.Errorf("review_levels.l3 = %+v, want none: true alone", l3)
	}
	// A launch without the flag writes no field at all: the run resolves its
	// levels from its own configuration, as it did before the flag existed.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abce", "do the thing"); err != nil {
		t.Fatal(err)
	}
	if spec := readSpec(t, f, "20261007-100000-abce"); spec.ReviewLevels != nil {
		t.Fatalf("review_levels = %+v, want none without the flag", spec.ReviewLevels)
	}
	// A document this launcher refuses is refused before any cloud call: an
	// effort outside the closed set is the launcher's mistake, and a runner
	// would never refuse it (L1.1).
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("l2:\n  effort: veryhigh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(f.run.Executions())
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcf", "--review-levels", bad, "do the thing")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "effort") {
		t.Fatalf("err = %v, want a refused effort", err)
	}
	if n := len(f.run.Executions()); n != before {
		t.Fatalf("the refused launch started %d executions, want the %d it ran before", n, before)
	}
}

// TestRunReviewLevelsFlagRefusesHostileDocuments: the document is the
// launcher's to read strictly and refuse, before any cloud call, because the
// runner's bootstrap is where a near miss would otherwise be found after the
// launch was paid for (config.ParseReviewLevelsYAML). An unknown key, `none:
// true` beside another key, a document with nothing in it, a file that is not
// there, and - beside --task-file - (one stdin, two readers) are each refused
// with a message naming what is wrong, and each leaves the runs bucket empty
// and starts no execution.
func TestRunReviewLevelsFlagRefusesHostileDocuments(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release0160)
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name, id, stdin, want string
		args                  []string
	}{
		{"an unknown key", "20261007-100000-abcd", "", "l4",
			[]string{"--review-levels", write("unknown.yaml", "l4:\n  model: deepseek/deepseek-v4-pro\n")}},
		{"none beside another key", "20261007-100000-abce", "", "none: true beside another key",
			[]string{"--review-levels", write("none.yaml", "l2:\n  none: true\n  model: deepseek/deepseek-v4-pro\n")}},
		{"a document with nothing in it", "20261007-100000-abcf", "", "the document is empty",
			[]string{"--review-levels", write("empty.yaml", "")}},
		{"an empty flag value", "20261007-100000-ac00", "", "pass a file, or - for stdin",
			[]string{"--review-levels", ""}},
		{"a file that is not there", "20261007-100000-ac01", "", "reading the review levels",
			[]string{"--review-levels", filepath.Join(dir, "missing.yaml")}},
		// The task comes from stdin too here, so this one carries text that way
		// — with no text at all the empty task is refused first, and the
		// both-read-stdin refusal is never reached.
		{"- beside --task-file -", "20261007-100000-ac02", "do the thing", "both read stdin",
			[]string{"--review-levels", "-", "--task-file", "-"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := len(f.run.Executions())
			args := append([]string{"run", "--repo", "acme/app", "--run-id", c.id}, c.args...)
			if c.stdin == "" {
				args = append(args, "do the thing")
			}
			_, _, err := executeStdin(t, c.stdin, args...)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want exit 1 naming %q", err, c.want)
			}
			if n := len(f.run.Executions()); n != before {
				t.Fatalf("the refused launch started %d executions, want the %d it ran before", n, before)
			}
		})
	}
}

// TestRunRefusesLevelsBelowFloor pins G1 at the launcher (L3.2, D722): a task
// that writes none for a level the owner's floor requires is refused before
// the task is written, so no job starts and nothing is left in the runs bucket
// under a run id that never launched. The floor is read from the owner-only
// policy node, which no launcher can write, and the run's own bootstrap
// refuses the same task a second time (L4.2) — a task written straight to the
// bucket meets the same rule.
//
// Mutation (drop the refusal from launchReviewLevelsAsk, answering only the
// ask): this test fails, finding the launch accepted and a job started.
func TestRunRefusesLevelsBelowFloor(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, `{"version":4,"allow_repos":["github/acme/*"],"review_floor":{"pro":["l2"]}}`)
	writeBuildRecord(t, f.cloudFixture, appSlug, "web", release0160)
	dir := t.TempDir()
	levels := filepath.Join(dir, "levels.yaml")
	if err := os.WriteFile(levels, []byte("l2:\n  none: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, f.cloudFixture, "l2: none is refused: the owner's floor for mode pro requires this level (D722)",
		"run", "--repo", "acme/app", "--mode", "pro", "--run-id", budgetRun, "--review-levels", levels, "Fix it.")
	if storedTask(t, f.cloudFixture) {
		t.Fatal("the refused launch wrote a task")
	}
}

// TestRunGatesResolvedLevelsOnImage: what the launch asks of the job image is
// the run's resolved levels, not the task's review_levels field (L3.2, L1.2).
// At critical the mode's own row turns level 3 on with nothing in the task and
// nothing in fugaro.yaml (L2.2), and a runner older than reviewLevelsSince
// would run the loop without the adversarial step in silence — so the launch
// is refused, and the same launch stands on the image that acts on levels.
//
// Mutation (build the ask from spec.ReviewLevels alone, dropping the
// resolution): the 0.15.0 launch below succeeds.
func TestRunGatesResolvedLevelsOnImage(t *testing.T) {
	f := newCloudFixture(t)
	args := func(run string) []string {
		return []string{"run", "--repo", "acme/app", "--mode", "critical", "--run-id", run, "Fix it."}
	}
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.15.0")
	_, _, err := execute(t, args("20261007-100000-abcd")...)
	if err == nil || !strings.Contains(err.Error(), "review level 3 (its adversarial step)") || ExitCode(err) != ExitUserError {
		t.Fatalf("0.15.0: err = %v, want the level 3 refusal", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("the refused launch started %d executions, want none", n)
	}
	writeBuildRecord(t, f, appSlug, "web", release0160)
	if _, _, err := execute(t, args("20261007-100000-abce")...); err != nil {
		t.Fatalf("0.16.0: %v", err)
	}
	// The task itself carries no level field: what was judged is the level the
	// mode resolved, which only the resolution knows.
	if spec := readSpec(t, f, "20261007-100000-abce"); spec.ReviewLevels != nil {
		t.Fatalf("review_levels = %+v, want none", spec.ReviewLevels)
	}
}
