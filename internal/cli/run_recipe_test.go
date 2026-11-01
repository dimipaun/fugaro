package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const release050 = "ghcr.io/dimipaun/fugaro-base:0.5.0"

// release070 is a job image new enough for a catalog recipe that uses a
// 0.7.0 construct: every catalog recipe but default carries use_when, so
// launching one needs this, not release050 (recipesV07Since).
const release070 = "ghcr.io/dimipaun/fugaro-base:0.7.0"

// release0150 is a job image new enough for the catalog default itself, whose
// adversarial step needs a runner that acts on it (adversarialSince): 0.7.0
// is past every 0.7.0 construct and still short of that, so a test that
// embeds default needs this one.
const release0150 = "ghcr.io/dimipaun/fugaro-base:0.15.0"

func TestRunRecipeFlagEmbedsCatalog(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	out, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "solo", "--json", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Recipe == nil || *res.Recipe != (launchRecipe{Name: "solo", Source: "catalog"}) {
		t.Fatalf("result = %s, %v", out, err)
	}
	if !strings.Contains(errOut, "recipe: solo (catalog)\n") {
		t.Fatalf("stderr = %q", errOut)
	}
	text, _ := recipe.CatalogText("solo")
	if r := readSpec(t, f, "20261007-100000-abcd").Recipe; r == nil || r.SHA256 != recipe.Sum(text) || r.YAML != string(text) {
		t.Fatalf("task recipe = %+v", r)
	}
}

func TestRunWithoutCheckoutLetsTheRunnerChoose(t *testing.T) {
	newCloudFixture(t)
	out, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--json", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "recipe: chosen by the runner (agent.recipe in fugaro.yaml at main, else default)") || strings.Contains(out, `"recipe"`) {
		t.Fatalf("stdout %s, stderr %q", out, errOut)
	}
}

// With no checkout the runner would read agent.recipe at the ref, so an
// explicit --recipe default is carried (and the image checked).
func TestRunExplicitDefaultWithoutCheckoutEmbeds(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "has no build record", "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "default", "x")
	writeBuildRecord(t, f, appSlug, "web", release050)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "default", "x"); err != nil {
		t.Fatal(err)
	}
	if r := readSpec(t, f, "20261007-100000-abcd").Recipe; r == nil || r.Name != "default" || r.YAML == "" {
		t.Fatalf("task recipe = %+v", r)
	}
}

func TestRunRecipeRefusedOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.4.1")
	wantRefused(t, f, "release 0.4.1", "run", "--repo", "acme/app", "--recipe", "solo", "x")
}

func TestRunUnknownRecipeFailsBeforeCloudWork(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "recipe nope is not in .fugaro/recipes", "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "nope", "x")
	if _, err := os.Stat(filepath.Join(f.dir, "runs", "runs", appSlug, "20261007-100000-abcd", "task.json")); err == nil {
		t.Fatal("a task.json was written for a refused recipe")
	}
}

// TestRunRefusesReviewModeWithoutPR: mode: review only reviews existing
// pull requests (Task 12 of the generic-tool plan), so fugaro run must
// refuse these catalog recipes without --pr, before any cloud work, not
// launch a job that silently falls back to running implement despite
// review-only's and pick's own "No coding" (docs/recipes.md §2). A check
// step is no gap at all: Task 11's runner executes it
// (TestRunAllowsCheckStepRecipe).
//
// Mutation (drop the Mode == ModeReview check from chooseRecipe): this
// test fails, launching a job instead of refusing.
func TestRunRefusesReviewModeWithoutPR(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	for _, name := range []string{"review-only", "pick"} {
		wantRefused(t, f, "recipe "+name+" is mode: review", "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", name, "x")
	}
}

// TestRunAllowsCheckStepRecipe: test-and-fix and lint-fix use a check step,
// no longer refused as not-yet-runnable now that Task 11's runner executes
// it as a gate.
//
// Mutation (restore the StepCheck branch of recipe.NotYetRunnable): this
// test fails with "cannot be launched yet", and no execution starts.
func TestRunAllowsCheckStepRecipe(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	for i, name := range []string{"test-and-fix", "lint-fix"} {
		runID := "2026100" + string(rune('7'+i)) + "-100000-abcd"
		_, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", runID, "--recipe", name, "x")
		if err != nil || !strings.Contains(errOut, "recipe: "+name+" (catalog)") {
			t.Fatalf("%s: err = %v, stderr %q", name, err, errOut)
		}
		if n := len(f.run.Executions()); n != i+1 {
			t.Fatalf("%s: %d executions, want %d", name, n, i+1)
		}
	}
}

// TestRenamedRecipeNamesTheReplacement: a 0.6 catalog name 0.7.0 retired
// (recipe.Renamed) is refused with its replacement, both at fugaro run
// --recipe and at fugaro validate's agent.recipe warning; a repository or
// project recipe that itself defines the old name still wins (the catalog
// rows of TestResolveRecipeLayers and the repo row of
// TestValidateWarnsRenamedAgentRecipe cover that precedence).
func TestRenamedRecipeNamesTheReplacement(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "claude-solo was renamed in 0.7.0: use solo", "run", "--repo", "acme/app", "--recipe", "claude-solo", "x")
	doc, err := validateDir(t, "  recipe: cheap-loop-senior\n", map[string]string{})
	if err != nil || !has(doc.Warnings, "agent.recipe", "cheap-loop-senior was renamed in 0.7.0: use standard") {
		t.Fatalf("validate: doc = %+v, err = %v", doc, err)
	}
}

// recipeCheckout makes the working directory a checkout of acme/other whose
// fugaro.yaml carries agentLines under agent:, with extra files.
func recipeCheckout(t *testing.T, f *cloudFixture, agentLines string, extra map[string]string) {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	files := map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent:\n  auth: api-key\n" + agentLines +
		"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"}
	for k, v := range extra {
		files[k] = v
	}
	testutil.WriteFiles(t, dir, files)
	t.Chdir(dir)
}

// Review Focus 1.
func TestRunAgentRecipeTriggersImageCheck(t *testing.T) {
	f := newCloudFixture(t)
	recipeCheckout(t, f, "  recipe: solo\n", nil)
	wantRefused(t, f, "has no build record", "run", "--run-id", "20261007-100000-abcd", "A task")
	other := mustSlug("github", "acme/other")
	writeBuildRecord(t, f, other, "svc", release070)
	_, errOut, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "A task")
	if err != nil || !strings.Contains(errOut, "recipe: solo (catalog)") {
		t.Fatalf("err = %v, stderr %q", err, errOut)
	}
}

// A workflow with no base: (the implicit Fugaro base) is named as "base" in
// the recipe-image refusal, not left unnamed: runRun resolves kind through
// w.BaseKind(), not the raw (empty) w.Base field.
//
// Mutation (run, restore): change runRun's `kind = w.BaseKind()` back to
// `kind = w.Base`, and this test fails: the refusal says "it verifies this
// release's base image" (the no-kind-known wording) instead of "it verifies
// this release's base base image" (kind "base" named explicitly).
func TestRunRecipeImageNamesTheImplicitBaseKind(t *testing.T) {
	f := newCloudFixture(t)
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent:\n  auth: api-key\n  recipe: solo\n" +
		"workflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	other := mustSlug("github", "acme/other")
	writeBuildRecord(t, f, other, "svc", "ghcr.io/dimipaun/fugaro-base:0.4.0")
	wantRefused(t, f, "it verifies this release's base base image", "run", "--run-id", "20261007-100000-abcd", "A task")
}

// TestRunClonedWorkflowWithRecipeLaunchesWithNoBuildRecord (review finding
// 1, HIGH): checkRecipeImage judges a workflow's derived-image build
// record, which a checkout: clone workflow never has (it runs the
// installation's base image directly, not a per-repository build).
// Without a skip for it, this launch would be refused "has no build
// record", and the refusal's own fix line (fugaro image refresh) never
// writes one for a clone workflow either — a permanent dead end.
//
// Mutation (run, restore): in runRun, change `clone = w.Checkout ==
// config.CheckoutClone` to `clone = false` — this test then fails,
// refused "has no build record".
func TestRunClonedWorkflowWithRecipeLaunchesWithNoBuildRecord(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "base_images: {base: ghcr.io/dimipaun/fugaro-base:0.7.5}\n")
	testutil.IsolateGit(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent:\n  auth: api-key\n  recipe: solo\n" +
		"workflows:\n  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	if r := readSpecOf(t, f, other, "20261008-100000-abcd").Recipe; r == nil || r.Name != "solo" {
		t.Fatalf("recipe = %+v", r)
	}
}

// TestRunClonedWorkflowRefusedBelowCloneSince (review finding 1, HIGH; 0.7.0
// safety fix, review finding): a checkout: clone workflow is refused on a
// base image that predates cloneSince even when it uses neither a recipe
// nor a project layer. The floor plan Task 17's runner support needs
// applies unconditionally, because nothing else here checks that
// base_images.base is actually that recent: fugaro-base could first be
// published from an older release, or --image-source could mirror one.
// cloneSince is 0.7.5, and 0.6.0 here is simply some release below it: the
// refusal must name the release the runner side needs and send the reader to
// `fugaro update --base base`, the ordinary *Since wording. It is the
// enabled-state pin for this gate — with cloneShipped false the same launch
// is refused with the not-released-yet wording instead, and this test fails.
func TestRunClonedWorkflowRefusedBelowCloneSince(t *testing.T) {
	f := newCloudFixture(t)
	f.appendConfig(t, "base_images: {base: ghcr.io/dimipaun/fugaro-base:0.6.0}\n")
	testutil.IsolateGit(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n" +
		"  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"})
	t.Chdir(dir)
	_, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "how to provision a blobless clone (FUGARO_CHECKOUT) (fugaro 0.7.5 or later)") ||
		!strings.Contains(err.Error(), "is ghcr.io/dimipaun/fugaro-base:0.6.0, release 0.6.0") ||
		!strings.Contains(err.Error(), "fugaro update --base base") {
		t.Fatalf("err = %v", err)
	}
	// The disabled state's guidance is gone with the flip: an older image is
	// told to move its base image, not that the runner side is unreleased.
	if strings.Contains(err.Error(), "not released yet") || strings.Contains(err.Error(), "checkout: baked") {
		t.Fatalf("a shipped gate still refuses with the unreleased wording: %v", err)
	}
}

func TestRunRepoRecipeFromCheckout(t *testing.T) {
	f := newCloudFixture(t)
	recipeCheckout(t, f, "", map[string]string{".fugaro/recipes/mine.yaml": "version: 1\nname: mine\nsteps:\n  - review: { max_rounds: 1 }\n"})
	other := mustSlug("github", "acme/other")
	writeBuildRecord(t, f, other, "svc", release050)
	if _, _, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "--recipe", "mine", "A task"); err != nil {
		t.Fatal(err)
	}
	b, err := blob.OpenBucket(t.Context(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, other, "20261007-100000-abcd").ReadTask(t.Context())
	if err != nil || spec.Recipe == nil || *spec.Recipe != (task.Recipe{Name: "mine", Source: "repo"}) {
		t.Fatalf("task = %+v, %v", spec, err)
	}
}

// Review Focus 5: the follow-up keeps the previous run's embedded text even
// though the project recipe has since been deleted.
func TestRunPRKeepsEmbeddedRecipeAfterProjectDelete(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil) // default bucket name; no team recipe published any more
	writeBuildRecord(t, f, appSlug, "web", release050)
	team := "version: 1\nname: team\nsteps:\n  - review: {}\n"
	root := firstRunSpec(rootID)
	root.Recipe = &task.Recipe{Name: "team", Source: "project", SHA256: recipe.Sum([]byte(team)), YAML: team}
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x")
	if err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).Recipe; got == nil || *got != *root.Recipe {
		t.Fatalf("follow-up recipe = %+v", got)
	}
	if !strings.Contains(errOut, "recipe: team (project)") {
		t.Fatalf("stderr = %q", errOut)
	}
}

// TestRunPRRefusesInheritedReviewMode covers the two follow-up paths that
// inherit a recipe from the PR's own history rather than naming one
// explicitly (the inherited-YAML re-parse and carryChosenRecipe): neither
// may let mode: review through just because the launch is a follow-up
// rather than a first run — a follow-up continues an existing Fugaro pull
// request, which mode: review never does (it only reviews one, and never
// pushes). An explicit --recipe naming a mode: review recipe alongside
// --pr is a different launch entirely now (review mode itself, TestRunPRReviewMode
// in run_review_test.go): it bypasses this follow-up machinery rather than
// being refused by it.
//
// Mutation (drop the Mode == ModeReview check from refuseFollowUpReviewMode):
// both subtests fail, launching a job instead of refusing.
func TestRunPRRefusesInheritedReviewMode(t *testing.T) {
	t.Run("inherited recipe", func(t *testing.T) {
		f := newCloudFixture(t)
		writeBuildRecord(t, f, appSlug, "web", release070)
		text, _ := recipe.CatalogText("review-only")
		root := firstRunSpec(rootID)
		root.Recipe = &task.Recipe{Name: "review-only", Source: "catalog", SHA256: recipe.Sum(text), YAML: string(text)}
		seedSpec(t, f, root, false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-72 * time.Hour)
		writeRecord(t, f, rootID, rec)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "also rename x")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "recipe review-only is mode: review") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("runner-chosen recipe", func(t *testing.T) {
		f := newCloudFixture(t)
		seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "review-only", Source: "catalog", SHA256: strings.Repeat("a", 64)})
		_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "ran (review-only) cannot be used") ||
			!strings.Contains(err.Error(), "recipe review-only is mode: review") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRunPRRecipeFlagWins(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	root := firstRunSpec(rootID)
	root.Recipe = &task.Recipe{Name: "mine", Source: "repo"}
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--recipe", "solo"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).Recipe; got == nil || got.Name != "solo" || got.Source != "catalog" {
		t.Fatalf("follow-up recipe = %+v", got)
	}
}

func TestRunRetryTakesNoRecipe(t *testing.T) {
	newCloudFixture(t)
	_, _, err := execute(t, "run", "--retry", "20261007-100000-abcd", "--recipe", "solo")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "it takes no --recipe") {
		t.Fatalf("err = %v", err)
	}
}

// The recipe's Note (here the custom runs bucket reason) reaches the user on
// stderr, escaped like other bucket-derived text.
func TestRunPrintsRecipeNote(t *testing.T) {
	f := newCloudFixture(t) // its runs bucket is not the default name: the note applies
	writeBuildRecord(t, f, appSlug, "web", release070)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "solo", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "note: project recipes need the default runs bucket name fugaro-runs-proj-1234") {
		t.Fatalf("stderr = %q", errOut)
	}
	if got := noteLine("bad\x1b[31m name\n"); strings.ContainsAny(got[:len(got)-1], "\x1b\n") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("noteLine = %q", got)
	}
}

// An explicit --recipe default must beat agent.recipe: the catalog default
// is then embedded, the preview and --json say so, and the image check
// applies without offering --recipe default as the way out.
func TestRunExplicitDefaultOverridesAgentRecipe(t *testing.T) {
	f := newCloudFixture(t)
	recipeCheckout(t, f, "  recipe: solo\n", nil)
	other := mustSlug("github", "acme/other")
	writeBuildRecord(t, f, other, "svc", release0150)
	out, errOut, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "--recipe", "default", "--json", "A task")
	if err != nil {
		t.Fatal(err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Recipe == nil || *res.Recipe != (launchRecipe{Name: "default", Source: "catalog"}) {
		t.Fatalf("result = %s, %v", out, err)
	}
	if !strings.Contains(errOut, "recipe: default (catalog)\n") {
		t.Fatalf("stderr = %q", errOut)
	}
	b, err := blob.OpenBucket(t.Context(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, other, "20261007-100000-abcd").ReadTask(t.Context())
	text, _ := recipe.CatalogText("default")
	if err != nil || spec.Recipe == nil || spec.Recipe.Name != "default" || spec.Recipe.Source != "catalog" ||
		spec.Recipe.YAML != string(text) || spec.Recipe.SHA256 != recipe.Sum(text) {
		t.Fatalf("task recipe = %+v, %v", spec.Recipe, err)
	}
}

func TestRunExplicitDefaultOverRefusedOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	recipeCheckout(t, f, "  recipe: solo\n", nil)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:0.4.1")
	_, _, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "--recipe", "default", "A task")
	if err == nil || !strings.Contains(err.Error(), "release 0.4.1") || strings.Contains(err.Error(), "--recipe default") {
		t.Fatalf("err = %v", err)
	}
}

// With no agent.recipe in the checkout, --recipe default and no flag alike
// carry no recipe, and the preview and --json say the catalog default.
func TestRunCheckoutDefaultCarriesNoRecipe(t *testing.T) {
	for _, args := range [][]string{{}, {"--recipe", "default"}} {
		f := newCloudFixture(t)
		recipeCheckout(t, f, "", nil)
		out, errOut, err := execute(t, append(append([]string{"run", "--run-id", "20261007-100000-abcd", "--json"}, args...), "A task")...)
		if err != nil {
			t.Fatal(err)
		}
		var res launchResult
		if err := json.Unmarshal([]byte(out), &res); err != nil || res.Recipe == nil || *res.Recipe != (launchRecipe{Name: "default", Source: "catalog"}) {
			t.Fatalf("%v: result = %s, %v", args, out, err)
		}
		if !strings.Contains(errOut, "recipe: default (catalog)\n") || strings.Contains(errOut, "note:") {
			t.Fatalf("%v: stderr = %q", args, errOut)
		}
		if r := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261007-100000-abcd").Recipe; r != nil {
			t.Fatalf("%v: task recipe = %+v", args, r)
		}
	}
}

func readSpecOf(t *testing.T, f *cloudFixture, slug, id string) *task.Spec {
	t.Helper()
	b, err := blob.OpenBucket(t.Context(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec, err := runstore.Open(b, slug, id).ReadTask(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// The default-named runs bucket, no project default: the catalog default,
// no embedded recipe, no image check (no build record here), no note.
func TestRunPlainWithDefaultNamedBucket(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	out, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	if r := readSpec(t, f, "20261007-100000-abcd").Recipe; r != nil {
		t.Fatalf("task recipe = %+v", r)
	}
	if strings.Contains(errOut, "note:") || strings.Contains(out, "recipe") {
		t.Fatalf("stdout %q, stderr %q", out, errOut)
	}
}

// The custom-bucket note is for a name the user chose that fell through to
// the catalog, not for every launch.
func TestRunNoteOnlyForExplicitNonDefault(t *testing.T) {
	f := newCloudFixture(t) // custom runs bucket
	writeBuildRecord(t, f, appSlug, "web", release050)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "x")
	if err != nil || strings.Contains(errOut, "note:") {
		t.Fatalf("implicit default: %v, stderr %q", err, errOut)
	}
	_, errOut, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100001-abcd", "--recipe", "default", "x")
	if err != nil || strings.Contains(errOut, "note:") {
		t.Fatalf("explicit default: %v, stderr %q", err, errOut)
	}
	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100002-abcd", "--recipe", "nope", "x")
	if err == nil || !strings.Contains(err.Error(), "recipe nope is not in") || !strings.Contains(err.Error(), "project recipes need the default runs bucket name") {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRunRepoRecipeRefusedOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	recipeCheckout(t, f, "", map[string]string{".fugaro/recipes/mine.yaml": "version: 1\nname: mine\nsteps:\n  - review: { max_rounds: 1 }\n"})
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-base:0.4.1")
	_, _, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "--recipe", "mine", "A task")
	if err == nil || !strings.Contains(err.Error(), "release 0.4.1") {
		t.Fatalf("err = %v", err)
	}
}

func seedRecipeRoot(t *testing.T, f *cloudFixture, rcp *task.Recipe, record string) {
	t.Helper()
	writeBuildRecord(t, f, appSlug, "web", record)
	root := firstRunSpec(rootID)
	root.Recipe = rcp
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
}

func TestRunPRInheritedRecipeRefusedOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	team := "version: 1\nname: team\nsteps:\n  - review: {}\n"
	seedRecipeRoot(t, f, &task.Recipe{Name: "team", Source: "project", SHA256: recipe.Sum([]byte(team)), YAML: team}, "ghcr.io/dimipaun/fugaro-base:0.4.1")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
	if err == nil || !strings.Contains(err.Error(), "recipe team needs a job image") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunPRInheritedBadRecipeFailsBeforeLaunch(t *testing.T) {
	f := newCloudFixture(t)
	bad := "version: 1\nname: team\nsteps: nope\n"
	seedRecipeRoot(t, f, &task.Recipe{Name: "team", Source: "project", SHA256: recipe.Sum([]byte(bad)), YAML: bad}, release050)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
	if err == nil || !strings.Contains(err.Error(), "team") || len(f.run.Executions()) != 0 {
		t.Fatalf("err = %v", err)
	}
}

// A repeated --run-id with another --recipe is a different task.
func TestRunPRRepeatRunIDWithAnotherRecipe(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release070)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	args := []string{"run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID}
	if _, _, err := execute(t, append(args, "--recipe", "solo")...); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, append(args, "--recipe", "solo")...); err != nil {
		t.Fatalf("same recipe: %v", err)
	}
	_, _, err := execute(t, append(args, "--recipe", "standard")...)
	if err == nil || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("err = %v", err)
	}
}

// seedChosenRoot seeds a first run whose task names no recipe (the runner
// chose) and whose record says chosen; nil chosen leaves the record without.
func seedChosenRoot(t *testing.T, f *cloudFixture, chosen *runstore.RecipeRecord) {
	t.Helper()
	writeBuildRecord(t, f, appSlug, "web", release070)
	seedSpec(t, f, firstRunSpec(rootID), false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	rec.Recipe = chosen
	writeRecord(t, f, rootID, rec)
}

func TestRunPRKeepsRecipeTheRunnerChose(t *testing.T) {
	f := newCloudFixture(t)
	text, _ := recipe.CatalogText("solo")
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "solo", Source: "catalog", SHA256: recipe.Sum(text)})
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
	if err != nil {
		t.Fatal(err)
	}
	got := readSpec(t, f, fuID).Recipe
	if got == nil || got.Name != "solo" || got.Source != "catalog" || got.YAML != string(text) {
		t.Fatalf("follow-up recipe = %+v", got)
	}
	if !strings.Contains(errOut, "recipe: solo (catalog)") || !strings.Contains(errOut, "keeping recipe solo, which the runner chose for run") ||
		strings.Contains(errOut, "has changed since") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestRunPRKeepsChosenRecipeAndSaysItChanged(t *testing.T) {
	f := newCloudFixture(t)
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "solo", Source: "catalog", SHA256: strings.Repeat("a", 64)})
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
	if err != nil || !strings.Contains(errOut, "has changed since run") {
		t.Fatalf("err = %v, stderr %q", err, errOut)
	}
}

func TestRunPRKeepsChosenRepoRecipeByName(t *testing.T) {
	f := newCloudFixture(t)
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "mine", Source: "repo", SHA256: strings.Repeat("a", 64)})
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).Recipe; got == nil || *got != (task.Recipe{Name: "mine", Source: "repo"}) {
		t.Fatalf("follow-up recipe = %+v", got)
	}
}

func TestRunPRChosenRecipeNothingToKeep(t *testing.T) {
	for name, chosen := range map[string]*runstore.RecipeRecord{
		"default": {Name: "default", Source: "catalog", SHA256: strings.Repeat("a", 64)},
		"absent":  nil,
	} {
		f := newCloudFixture(t)
		seedChosenRoot(t, f, chosen)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := readSpec(t, f, fuID).Recipe; got != nil {
			t.Errorf("%s: follow-up recipe = %+v", name, got)
		}
	}
}

func TestRunPRChosenRecipeRefusedOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "standard", Source: "catalog", SHA256: strings.Repeat("a", 64)})
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.4.1")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
	if err == nil || !strings.Contains(err.Error(), "recipe standard needs a job image") {
		t.Fatalf("err = %v", err)
	}
}

func failRecordRead(t *testing.T, err error) {
	t.Helper()
	old := readRunRecord
	readRunRecord = func(context.Context, *runstore.Store) (*runstore.Record, error) { return nil, err }
	t.Cleanup(func() { readRunRecord = old })
}

// A record that can't be read must never turn into a silent default.
func TestRunPRChosenRecipeRecordReadFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"transient": {errors.New("storage: connection reset"), "nothing was launched"},
		"corrupt":   {&json.SyntaxError{}, "result.json is unreadable"},
	} {
		f := newCloudFixture(t)
		seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "standard", Source: "catalog", SHA256: strings.Repeat("a", 64)})
		failRecordRead(t, tc.err)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Errorf("%s: %d executions", name, n)
		}
		if _, err := runstore.Open(fileEnv(t, f).bucket.Bucket, appSlug, fuID).ReadFile(t.Context(), "task.json"); !errors.Is(err, runstore.ErrNotFound) {
			t.Errorf("%s: a task.json was created: %v", name, err)
		}
	}
}

func TestRunPRChosenRecipeRecordNotFoundCarriesNothing(t *testing.T) {
	f := newCloudFixture(t)
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "standard", Source: "catalog", SHA256: strings.Repeat("a", 64)})
	failRecordRead(t, runstore.ErrNotFound)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).Recipe; got != nil {
		t.Fatalf("recipe = %+v", got)
	}
}

func TestRunPRChosenRecipeNoLongerResolves(t *testing.T) {
	f := newCloudFixture(t)
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "gone", Source: "project", SHA256: strings.Repeat("a", 64)})
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
	if err == nil || !strings.Contains(err.Error(), "ran (gone) cannot be used") || !strings.Contains(err.Error(), "pass --recipe NAME") ||
		ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
}
