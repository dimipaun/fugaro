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

func TestRunDefaultCarriesNoRecipe(t *testing.T) {
	f := newCloudFixture(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261007-100000-abcd", "--recipe", "default", "x"); err != nil {
		t.Fatal(err) // no build record needed: a default run carries no recipe
	}
	if r := readSpec(t, f, "20261007-100000-abcd").Recipe; r != nil {
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
