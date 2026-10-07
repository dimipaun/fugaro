package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// recipeSpec is the default first-run task carrying r.
func recipeSpec(r *task.Recipe) *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature", Recipe: r}
}

// catalogTask is catalog recipe name as the CLI embeds it.
func catalogTask(t *testing.T, name string) *task.Recipe {
	t.Helper()
	text, ok := recipe.CatalogText(name)
	if !ok {
		t.Fatalf("no catalog recipe %s", name)
	}
	return &task.Recipe{Name: name, Source: string(recipe.SourceCatalog), SHA256: recipe.Sum(text), YAML: string(text)}
}

const twoRounds = "version: 1\nname: two-rounds\nsteps:\n  - review: { max_rounds: 2 }\n"

func TestRecipeDefaultRecorded(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 2, "  first_line_review: off\n"), nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := recipe.CatalogText(recipe.DefaultName)
	if r := rec.Recipe; r == nil || *r != (runstore.RecipeRecord{Name: "default", Source: "catalog", SHA256: recipe.Sum(text)}) {
		t.Fatalf("recipe = %+v", rec.Recipe)
	}
}

func TestRecipeEmbeddedCatalog(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 3, "  first_line_review: off\n"), recipeSpec(catalogTask(t, "cheap-loop-senior")))
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("changes", 1), fixStep, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	// first_line_review: off does not switch off the recipe's first line;
	// the senior review gets the recipe's one round, not review_rounds: 3.
	if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review_first", "fix", "review") {
		t.Fatalf("stages = %v", got)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Recipe.Name != "cheap-loop-senior" || rec.Recipe.Source != "catalog" {
		t.Fatalf("rec = %+v", rec)
	}
}

func TestRecipeRepoLayerWins(t *testing.T) {
	// The task embeds the catalog's cheap-loop-senior; the repository has its
	// own file of that name, which wins.
	own := "version: 1\nname: cheap-loop-senior\nsteps:\n  - review: { max_rounds: 1 }\n"
	h := newHarnessFiles(t, firstLineCfg(t, 2, ""), recipeSpec(catalogTask(t, "cheap-loop-senior")),
		map[string]string{".fugaro/recipes/cheap-loop-senior.yaml": own})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review") {
		t.Fatalf("stages = %v", got)
	}
	if r := rec.Recipe; r.Source != "repo" || r.SHA256 != recipe.Sum([]byte(own)) {
		t.Fatalf("recipe = %+v", r)
	}
}

func TestRecipeAgentRecipeOnFirstRun(t *testing.T) {
	h := newHarnessFiles(t, firstLineCfg(t, 3, "  recipe: two-rounds\n"), nil,
		map[string]string{".fugaro/recipes/two-rounds.yaml": twoRounds})
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") || rec.Recipe.Name != "two-rounds" {
		t.Fatalf("stages = %v, recipe %+v", got, rec.Recipe)
	}
}

// Review Focus 2.
func TestRecipeRepoSourceMissingAtRefFails(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 2, ""), recipeSpec(&task.Recipe{Name: "two-rounds", Source: "repo"}))
	rec, err := h.run(t, implement("feature"))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "commit and push it") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("%d agent calls, want none", len(h.agent.calls))
	}
}

func TestRecipeRepoInvalidFailsNoFallback(t *testing.T) {
	h := newHarnessFiles(t, firstLineCfg(t, 2, "  recipe: broken\n"), nil,
		map[string]string{".fugaro/recipes/broken.yaml": "version: 1\nname: broken\nsteps:\n  - first_line: {}\n"})
	rec, err := h.run(t, implement("feature"))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, ".fugaro/recipes/broken.yaml") ||
		!strings.Contains(rec.Reason, "must end with a review step") || len(h.agent.calls) != 0 {
		t.Fatalf("rec = %+v, err = %v, calls %d", rec, err, len(h.agent.calls))
	}
}

// Review Focus 4.
func TestRecipeRepoNameMismatch(t *testing.T) {
	h := newHarnessFiles(t, firstLineCfg(t, 2, "  recipe: mine\n"), nil,
		map[string]string{".fugaro/recipes/mine.yaml": strings.Replace(twoRounds, "name: two-rounds", "name: claude-solo", 1)})
	rec, err := h.run(t, implement("feature"))
	if err == nil || !strings.Contains(rec.Reason, "file name and the name must agree") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestRecipeUnknownName(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 2, "  recipe: nowhere\n"), nil)
	rec, err := h.run(t, implement("feature"))
	if err == nil || !strings.Contains(rec.Reason, "recipe nowhere is not in .fugaro/recipes") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestRecipeRepoSymlinkRefused(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "x.yaml")
	if err := os.WriteFile(outside, []byte(twoRounds), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, firstLineCfg(t, 2, "  recipe: two-rounds\n"), nil)
	commitToRemote(t, h, func(dir string) {
		if err := os.MkdirAll(filepath.Join(dir, ".fugaro", "recipes"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, ".fugaro", "recipes", "two-rounds.yaml")); err != nil {
			t.Fatal(err)
		}
	})
	rec, err := h.run(t, implement("feature"))
	if err == nil || !strings.Contains(rec.Reason, "not a regular file") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// A follow-up reads the repository layer from its base, never from the PR's
// branch (which an earlier agent could have changed).
func TestRecipeFollowUpReadsBase(t *testing.T) {
	h := followUpHarness(t, "", func(h *harness) {
		commitToRemote(t, h, func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{".fugaro/recipes/two-rounds.yaml": twoRounds})
		})
	}, then(commitOnly("feature"), func(t *testing.T, req agent.Request) {
		testutil.WriteFiles(t, req.Dir, map[string]string{".fugaro/recipes/two-rounds.yaml": strings.Replace(twoRounds, "max_rounds: 2", "max_rounds: 9", 1)})
		shell(t, req, "git add -A && git commit -qm 'Loosen the recipe'")
	}), review("ship", 0))
	h.followUp(t, followID, runID, "Tidy up.")
	spec, err := h.store.ReadTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spec.Recipe = &task.Recipe{Name: "two-rounds", Source: "repo"}
	if err := h.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("tidy"), review("changes", 1), fixStep, review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") || rec.Recipe.SHA256 != recipe.Sum([]byte(twoRounds)) {
		t.Fatalf("stages = %v, recipe %+v (the base's two rounds, not the branch's nine)", got, rec.Recipe)
	}
}

// The loop's rounds come from the plan, not from agent.review_rounds: a
// recipe of one review round on a config saying 3 runs one senior review.
func TestRecipeRoundsComeFromThePlan(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 3, "  first_line_review: off\n"), recipeSpec(catalogTask(t, "claude-solo")))
	rec, err := h.run(t, implement("feature"), review("changes", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review") {
		t.Fatalf("stages = %v", got)
	}
}

// A follow-up's recipe is the task's or default, never agent.recipe (§5).
func TestRecipeFollowUpIgnoresAgentRecipe(t *testing.T) {
	cfg := strings.Replace(followUpYAML(t, ""), "  review_rounds: 2\n", "  review_rounds: 2\n  recipe: two-rounds\n", 1)
	h := followUpHarness(t, cfg, func(h *harness) {
		commitToRemote(t, h, func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{".fugaro/recipes/two-rounds.yaml": twoRounds})
		})
	})
	if h.first.Recipe == nil || h.first.Recipe.Name != "two-rounds" {
		t.Fatalf("first run recipe = %+v", h.first.Recipe)
	}
	h.followUp(t, followID, runID, "Tidy up.")
	rec, err := h.run(t, implement("tidy"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Recipe == nil || rec.Recipe.Name != "default" || rec.Recipe.Source != "catalog" {
		t.Fatalf("recipe = %+v, want default", rec.Recipe)
	}
}

func TestRecipeFollowUpOversizeBaseFileFails(t *testing.T) {
	h := followUpHarness(t, "", func(h *harness) {
		commitToRemote(t, h, func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{".fugaro/recipes/big.yaml": "# " + strings.Repeat("x", recipe.MaxBytes)})
		})
	})
	h.followUp(t, followID, runID, "Tidy up.")
	spec, err := h.store.ReadTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spec.Recipe = &task.Recipe{Name: "big", Source: "repo"}
	if err := h.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("tidy"))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "16 KiB") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}
