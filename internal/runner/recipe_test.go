package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// recipeSpec is the default first-run task carrying r.
func recipeSpec(r *task.Recipe) *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature", Recipe: r}
}

// embeddedTask is a task.Recipe the way the CLI embeds a recipe it resolved
// (any source, trusted as given: the runner never re-checks an embedded
// recipe's YAML against its source, resolveRecipe). It is used below for the
// exact shape of the two 0.6 catalog recipes 0.7.0 retired (recipe.Renamed):
// the runner's handling of an embedded recipe does not care that they are no
// longer in the catalog, only the CLI's image gate does (recipes_skew.go).
func embeddedTask(name, yaml string) *task.Recipe {
	return &task.Recipe{Name: name, Source: string(recipe.SourceCatalog), SHA256: recipe.Sum([]byte(yaml)), YAML: yaml}
}

const twoRounds = "version: 1\nname: two-rounds\nsteps:\n  - review: { max_rounds: 2 }\n"

// retiredCheapLoopSenior and retiredClaudeSolo are the exact 0.6 catalog
// texts of the two recipes the 0.7.0 catalog renamed away (solo, standard):
// pinned rounds, no use_when, so they still parse that way under any runner.
const (
	retiredCheapLoopSenior = "version: 1\nname: cheap-loop-senior\ndescription: Cheap first-line review/fix loop by the coder's model (2 rounds), then one senior review\nsteps:\n  - first_line: { max_rounds: 2 }\n  - review: { max_rounds: 1 }\n"
	retiredClaudeSolo      = "version: 1\nname: claude-solo\ndescription: One model plays both parts. The coder's model reviews in a fresh session, one senior round, no first line.\nroles:\n  reviewer: coder\nsteps:\n  - review: { max_rounds: 1 }\n"
)

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
	h := newHarness(t, firstLineCfg(t, 3, "  first_line_review: off\n"), recipeSpec(embeddedTask("cheap-loop-senior", retiredCheapLoopSenior)))
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
	// The task embeds a recipe named cheap-loop-senior; the repository has
	// its own file of that name, which wins.
	own := "version: 1\nname: cheap-loop-senior\nsteps:\n  - review: { max_rounds: 1 }\n"
	h := newHarnessFiles(t, firstLineCfg(t, 2, ""), recipeSpec(embeddedTask("cheap-loop-senior", retiredCheapLoopSenior)),
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
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "commit and push it") || !strings.Contains(rec.Reason, "at its base") {
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

// TestRecipeModeReviewMismatchFailsBeforeImplement: bootstrap cross-checks
// a resolved recipe's Mode against the task (r.review, decided from
// spec.ReviewPRs before the recipe is even known) and refuses a mismatch
// before implement ever runs — the runner's own guard, independent of the
// CLI's: it is what actually protects a repository recipe or a bare
// agent.recipe name, which the CLI never reads the text of before launch.
// This task (a first run, Task: "Add a feature", no ReviewPRs) can never
// carry a mode: review recipe and run it as ordinary implement; Task 12's
// bootstrap refuses it instead of silently running implement regardless (as
// it did before review mode existed at all). A check step is no gap at
// all: Task 11's runner gates the run on it (TestRecipeCheckStepReachesTheGate).
//
// Mutation (run, restore): comment out the Mode-mismatch check in
// runner.go's bootstrap, and this test fails: implement runs and the
// record is not an infra_error.
func TestRecipeModeReviewMismatchFailsBeforeImplement(t *testing.T) {
	const yaml = "version: 1\nname: reviewonly\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n"
	h := newHarness(t, firstLineCfg(t, 2, ""), recipeSpec(embeddedTask("reviewonly", yaml)))
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "the task names no pull request to review") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("%d agent calls, want none (implement must not run)", len(h.agent.calls))
	}
}

// TestRecipeNonReviewModeMismatchFailsBeforeImplement: the mirror of
// TestRecipeModeReviewMismatchFailsBeforeImplement's own mismatch check —
// a task naming pull requests to review (r.review, decided from
// spec.ReviewPRs alone) but an ordinary, non-"mode: review" recipe, must
// be refused before implement, the same as the opposite mismatch is.
//
// Mutation (drop the `r.review && rcp.Mode != recipe.ModeReview` case from
// runner.go's bootstrap switch): this test fails, finding implement runs
// against the named pull request's branch instead of refusing up front.
func TestRecipeNonReviewModeMismatchFailsBeforeImplement(t *testing.T) {
	spec := recipeSpec(embeddedTask("two-rounds", twoRounds))
	spec.ReviewPRs = []int{7}
	h := newHarness(t, firstLineCfg(t, 2, ""), spec)
	rec, err := h.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "is not mode: review, but the task names pull requests to review") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("%d agent calls, want none (implement must not run)", len(h.agent.calls))
	}
}

// TestRecipeDigestStepFailsBeforeImplement: the digest step is the runner's
// own work — it writes the run reports the repository is missing and commits
// them, with no model call — and planOf has no arm for it. A run of such a
// recipe stops at the step, before any implement stage: this one stops
// because its task carries no digest_runs manifest, which is the one input
// the step reads (T3.6). What the test pins is that no implement stage runs
// and no ordinary pull request is opened in the recipe's name — the opposite
// of what run-digest asks for, and the reason the step takes implement's
// place in agentLoop rather than being dropped from the plan.
//
// Mutation (drop the r.digest dispatch from runner.go's agentLoop): this test
// fails, finding implement runs and the run ends with a pull request rather
// than an infra_error.
func TestRecipeDigestStepFailsBeforeImplement(t *testing.T) {
	const yaml = "version: 1\nname: rundigest\nsteps:\n  - digest: {}\n  - review: { max_rounds: 1 }\n"
	h := newHarness(t, firstLineCfg(t, 2, ""), recipeSpec(embeddedTask("rundigest", yaml)))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	// The step's own refusal ends the run (the gate's failures end one the
	// same way), so it is the record that carries it, not Run's error.
	if rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "digest_runs") {
		t.Fatalf("rec = %+v", rec)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("%d agent calls, want none (implement must not run)", len(h.agent.calls))
	}
}

// TestRecipeCheckStepReachesTheGate: an embedded recipe with a check step —
// the same resolution path recipe.NotYetRunnable used to refuse before
// Task 11's runner gated the run on it — now runs the gate instead: a clean
// check ends the run at once, with no implement stage and no pull request.
// (internal/runner/gate_test.go covers the gate's own behaviour in full;
// this test is only that the *embedded-task* resolution path, which
// TestRecipeNotYetRunnableFailsBeforeImplement above also uses, really
// reaches it — the repo-recipe path gate_test.go exercises is a different
// branch of resolveRecipe.)
//
// Mutation (restore the StepCheck branch of recipe.NotYetRunnable): this
// test fails, back to infra_error "cannot be run yet", zero agent calls.
func TestRecipeCheckStepReachesTheGate(t *testing.T) {
	const yaml = "version: 1\nname: gated\nsteps:\n  - check: { command: test }\n  - review: {}\n"
	h := newHarness(t, firstLineCfg(t, 2, ""), recipeSpec(embeddedTask("gated", yaml)))
	rec, err := h.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone || rec.Gate != "clean" {
		t.Fatalf("rec = %+v", rec)
	}
	if len(h.agent.calls) != 0 {
		t.Fatalf("%d agent calls, want none (a clean gate never reaches implement)", len(h.agent.calls))
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
	h := newHarness(t, firstLineCfg(t, 3, "  first_line_review: off\n"), recipeSpec(embeddedTask("claude-solo", retiredClaudeSolo)))
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
			testutil.WriteFiles(t, dir, map[string]string{".fugaro/recipes/big.yaml": "# " + strings.Repeat("x", 2<<20)})
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
	defer runner.FailOnShowRepoFile(func() { t.Error("the base file was read before its size was checked") })()
	rec, err := h.run(t, implement("tidy"))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "16 KiB") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}
