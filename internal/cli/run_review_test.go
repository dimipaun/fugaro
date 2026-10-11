package cli

import (
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

// TestRunReviewModeWarnsAllowForksEnabled: a review-mode launch against a
// local checkout whose fugaro.yaml sets review.allow_forks: true gets a
// one-line stderr reminder naming the actual exposure (the GitHub App's
// private key), not just fugaro validate's own warning when the file is
// edited (TestValidateWarnsAllowForks, validate_test.go) — a launcher may
// never have run validate on this exact file.
//
// Mutation (drop the `if c := checkout(); c != nil && c.Review.AllowForks`
// block from reviewSpec): this test fails, finding no note on stderr.
func TestRunReviewModeWarnsAllowForksEnabled(t *testing.T) {
	f := newCloudFixture(t)
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent:\n  auth: api-key\n" +
		"review:\n  allow_forks: true\n" +
		"workflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	other := mustSlug("github", "acme/other")
	writeBuildRecord(t, f, other, "svc", release070)
	_, errOut, err := execute(t, "run", "--recipe", "review-only", "--pr", "7", "--run-id", "20261007-100000-abcd")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "note:") || !strings.Contains(errOut, "GitHub App's private key") {
		t.Fatalf("errOut = %q", errOut)
	}
}
