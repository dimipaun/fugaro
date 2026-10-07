package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func writeRecipeFile(t *testing.T, dir, rel, text string) string {
	t.Helper()
	testutil.WriteFiles(t, dir, map[string]string{rel: text})
	return filepath.Join(dir, filepath.FromSlash(rel))
}

func TestRecipesValidate(t *testing.T) {
	dir := t.TempDir()
	good := writeRecipeFile(t, dir, "good.yaml", projectTeam)
	out, _, err := execute(t, "recipes", "validate", good)
	if err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("good: %s, %v", out, err)
	}
	bad := writeRecipeFile(t, dir, "bad.yaml", "version: 1\nname: bad\nmodel: claude-opus-5\nsteps:\n  - review: {}\n")
	out, _, err = execute(t, "recipes", "validate", "--json", bad)
	var doc struct {
		Valid    bool             `json:"valid"`
		Problems []recipe.Problem `json:"problems"`
	}
	if ExitCode(err) != ExitUserError || json.Unmarshal([]byte(out), &doc) != nil || doc.Valid || len(doc.Problems) != 1 ||
		!strings.Contains(doc.Problems[0].Message, "never names a model") {
		t.Fatalf("bad: %s, %v", out, err)
	}
}

// Review Focus 4.
func TestRecipesValidateNameMismatch(t *testing.T) {
	path := writeRecipeFile(t, t.TempDir(), ".fugaro/recipes/mine.yaml", "version: 1\nname: claude-solo\nsteps:\n  - review: {}\n")
	_, _, err := execute(t, "recipes", "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
	out, _, _ := execute(t, "recipes", "validate", path)
	if !strings.Contains(out, "the file name and the name must agree") {
		t.Fatalf("out = %s", out)
	}
}

func TestRecipesPublishRefusedInAgentSession(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "your own terminal") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("team")))); err == nil {
		t.Fatal("published from an agent session")
	}
}

func TestRecipesPublishWritesObject(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "claude-solo.yaml", projectSolo)
	out, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil || !strings.Contains(out, "published claude-solo to gs://fugaro-runs-proj-1234/fugaro/recipes/claude-solo.yaml") ||
		!strings.Contains(errOut, "replaces the catalog's claude-solo") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
	got, err := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("claude-solo"))))
	if err != nil || string(got) != projectSolo {
		t.Fatalf("object = %q, %v", got, err)
	}
}

func TestRecipesPublishShowsWhatItReplaces(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"team": projectTeam})
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", repoTeam)
	_, errOut, err := execute(t, "recipes", "publish", path)
	if err != nil || !strings.Contains(errOut, "replacing the project's team") ||
		!strings.Contains(errOut, "- ") || !strings.Contains(errOut, "+ ") || !strings.Contains(errOut, "max_rounds: 3") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}

func TestRecipesPublishNeedsFileNamedAfterRecipe(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, nil)
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "mine.yaml", projectSolo)
	_, _, err := execute(t, "recipes", "publish", path)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "runs", filepath.FromSlash(recipe.ObjectKey("claude-solo")))); err == nil {
		t.Fatal("published under a mismatched file name")
	}
}

func TestRecipesPublishRefusesInvalidAndCustomBucket(t *testing.T) {
	f := newCloudFixture(t) // custom bucket name
	noAgentSession(t)
	path := writeRecipeFile(t, t.TempDir(), "team.yaml", projectTeam)
	if _, _, err := execute(t, "recipes", "publish", path); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("custom bucket: %v", err)
	}
	projectRecipesFixture(t, f, nil)
	bad := writeRecipeFile(t, t.TempDir(), "bad.yaml", "version: 1\nname: bad\nsteps: []\n")
	if _, _, err := execute(t, "recipes", "publish", bad); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") {
		t.Fatalf("invalid: %v", err)
	}
}

func TestRecipesLsLayers(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"claude-solo": projectSolo, "team": projectTeam})
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	writeRecipeFile(t, dir, ".fugaro/recipes/team.yaml", repoTeam)
	t.Chdir(dir)
	out, _, err := execute(t, "recipes", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Recipes []recipeRow `json:"recipes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	want := []recipeRow{
		{Name: "team", Source: "repo"},
		{Name: "claude-solo", Source: "project"},
		{Name: "team", Source: "project", ShadowedBy: "repo"},
		{Name: "cheap-loop-senior", Source: "catalog"},
		{Name: "claude-solo", Source: "catalog", ShadowedBy: "project"},
		{Name: "default", Source: "catalog"},
	}
	if len(doc.Recipes) != len(want) {
		t.Fatalf("rows = %+v", doc.Recipes)
	}
	for i, w := range want {
		g := doc.Recipes[i]
		if g.Name != w.Name || g.Source != w.Source || g.ShadowedBy != w.ShadowedBy {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestRecipesShowSource(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"claude-solo": projectSolo})
	out, _, err := execute(t, "recipes", "show", "claude-solo")
	if err != nil || !strings.HasPrefix(out, "recipe claude-solo from project (gs://fugaro-runs-proj-1234/fugaro/recipes/claude-solo.yaml)\nsha256 "+recipe.Sum([]byte(projectSolo))+"\n\n") ||
		!strings.HasSuffix(out, projectSolo) {
		t.Fatalf("out = %q, %v", out, err)
	}
}

func TestRecipesShowPrintsNote(t *testing.T) {
	newCloudFixture(t) // custom runs bucket: project recipes are not read
	out, errOut, err := execute(t, "recipes", "show", "default")
	if err != nil || !strings.HasPrefix(out, "recipe default from catalog") || !strings.Contains(errOut, "note: project recipes need the default runs bucket name") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}
