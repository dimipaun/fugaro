package cli

import (
	"encoding/json"
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

const release050 = "ghcr.io/dimipaun/fugaro-web-node:0.5.0"

func TestRunRecipeFlagEmbedsCatalog(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release050)
	out, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "claude-solo", "--json", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Recipe == nil || *res.Recipe != (launchRecipe{Name: "claude-solo", Source: "catalog"}) {
		t.Fatalf("result = %s, %v", out, err)
	}
	if !strings.Contains(errOut, "recipe: claude-solo (catalog)\n") {
		t.Fatalf("stderr = %q", errOut)
	}
	text, _ := recipe.CatalogText("claude-solo")
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
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-web-node:0.4.1")
	wantRefused(t, f, "release 0.4.1", "run", "--repo", "acme/app", "--recipe", "claude-solo", "x")
}

func TestRunUnknownRecipeFailsBeforeCloudWork(t *testing.T) {
	f := newCloudFixture(t)
	wantRefused(t, f, "recipe nope is not in .fugaro/recipes", "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "nope", "x")
	if _, err := os.Stat(filepath.Join(f.dir, "runs", "runs", appSlug, "20261007-100000-abcd", "task.json")); err == nil {
		t.Fatal("a task.json was written for a refused recipe")
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
		"workflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"}
	for k, v := range extra {
		files[k] = v
	}
	testutil.WriteFiles(t, dir, files)
	t.Chdir(dir)
}

// Review Focus 1.
func TestRunAgentRecipeTriggersImageCheck(t *testing.T) {
	f := newCloudFixture(t)
	recipeCheckout(t, f, "  recipe: claude-solo\n", nil)
	wantRefused(t, f, "has no build record", "run", "--run-id", "20261007-100000-abcd", "A task")
	other := mustSlug("github", "acme/other")
	writeBuildRecord(t, f, other, "svc", release050)
	_, errOut, err := execute(t, "run", "--run-id", "20261007-100000-abcd", "A task")
	if err != nil || !strings.Contains(errOut, "recipe: claude-solo (catalog)") {
		t.Fatalf("err = %v, stderr %q", err, errOut)
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

func TestRunPRRecipeFlagWins(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release050)
	root := firstRunSpec(rootID)
	root.Recipe = &task.Recipe{Name: "mine", Source: "repo"}
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--recipe", "cheap-loop-senior"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID).Recipe; got == nil || got.Name != "cheap-loop-senior" || got.Source != "catalog" {
		t.Fatalf("follow-up recipe = %+v", got)
	}
}

func TestRunRetryTakesNoRecipe(t *testing.T) {
	newCloudFixture(t)
	_, _, err := execute(t, "run", "--retry", "20261007-100000-abcd", "--recipe", "claude-solo")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "it takes no --recipe") {
		t.Fatalf("err = %v", err)
	}
}

// The recipe's Note (here the custom runs bucket reason) reaches the user on
// stderr, escaped like other bucket-derived text.
func TestRunPrintsRecipeNote(t *testing.T) {
	f := newCloudFixture(t) // its runs bucket is not the default name: the note applies
	writeBuildRecord(t, f, appSlug, "web", release050)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "claude-solo", "x")
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
	recipeCheckout(t, f, "  recipe: claude-solo\n", nil)
	other := mustSlug("github", "acme/other")
	writeBuildRecord(t, f, other, "svc", release050)
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
	recipeCheckout(t, f, "  recipe: claude-solo\n", nil)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-web-node:0.4.1")
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
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", "ghcr.io/dimipaun/fugaro-web-node:0.4.1")
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
	seedRecipeRoot(t, f, &task.Recipe{Name: "team", Source: "project", SHA256: recipe.Sum([]byte(team)), YAML: team}, "ghcr.io/dimipaun/fugaro-web-node:0.4.1")
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
	writeBuildRecord(t, f, appSlug, "web", release050)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	args := []string{"run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID}
	if _, _, err := execute(t, append(args, "--recipe", "claude-solo")...); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, append(args, "--recipe", "claude-solo")...); err != nil {
		t.Fatalf("same recipe: %v", err)
	}
	_, _, err := execute(t, append(args, "--recipe", "cheap-loop-senior")...)
	if err == nil || !strings.Contains(err.Error(), "already holds a different task") {
		t.Fatalf("err = %v", err)
	}
}

// seedChosenRoot seeds a first run whose task names no recipe (the runner
// chose) and whose record says chosen; nil chosen leaves the record without.
func seedChosenRoot(t *testing.T, f *cloudFixture, chosen *runstore.RecipeRecord) {
	t.Helper()
	writeBuildRecord(t, f, appSlug, "web", release050)
	seedSpec(t, f, firstRunSpec(rootID), false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	rec.Recipe = chosen
	writeRecord(t, f, rootID, rec)
}

func TestRunPRKeepsRecipeTheRunnerChose(t *testing.T) {
	f := newCloudFixture(t)
	text, _ := recipe.CatalogText("cheap-loop-senior")
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "cheap-loop-senior", Source: "catalog", SHA256: recipe.Sum(text)})
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
	if err != nil {
		t.Fatal(err)
	}
	got := readSpec(t, f, fuID).Recipe
	if got == nil || got.Name != "cheap-loop-senior" || got.Source != "catalog" || got.YAML != string(text) {
		t.Fatalf("follow-up recipe = %+v", got)
	}
	if !strings.Contains(errOut, "recipe: cheap-loop-senior (catalog)") || !strings.Contains(errOut, "keeping recipe cheap-loop-senior, which the runner chose for run") ||
		strings.Contains(errOut, "has changed since") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestRunPRKeepsChosenRecipeAndSaysItChanged(t *testing.T) {
	f := newCloudFixture(t)
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "cheap-loop-senior", Source: "catalog", SHA256: strings.Repeat("a", 64)})
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
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "cheap-loop-senior", Source: "catalog", SHA256: strings.Repeat("a", 64)})
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-web-node:0.4.1")
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "x")
	if err == nil || !strings.Contains(err.Error(), "recipe cheap-loop-senior needs a job image") {
		t.Fatalf("err = %v", err)
	}
}
