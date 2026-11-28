package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is I3's runner half: the end of the migration, where a run meets
// only what fugaro run embedded in its task. The copy the migration published
// (D3302) is the recipe the run gets — a repository's file is never read, so
// the folder's file at that name decides nothing (D3304) — and it is recorded
// as the project's, with the copy's own name and sum. A task that still leaves
// its recipe to the repository (Source: repo, the shape a launch before the
// release wrote) is refused before any stage, naming the fix (acceptance 7).

// TestEmbeddedCopyRunsAndIsRecorded is I3's acceptance in the runner: the
// copy's two rounds run and are recorded as the project's, while the file the
// repository's default branch still holds — one round, and named the recipe
// rather than its copy — would have given a different run. The rounds are what
// says which text the runner read: the configured three are the copy's two,
// never the file's one.
func TestEmbeddedCopyRunsAndIsRecorded(t *testing.T) {
	copied := recipe.CopyName("review", "github", "acme/app")
	if copied == "" {
		t.Fatal("no copy name for recipe review of acme/app")
	}
	// The migration's copy: the repository's file with its name: line
	// rewritten and nothing else (R14), and one round more than it, so the
	// count of reviews says whose text the run read.
	copyText := "version: 1\nname: " + copied + "\nsteps:\n  - review: { max_rounds: 2 }\n"
	// The file still at .fugaro/recipes/review.yaml, the unmigrated file whose
	// one round a runner that read the folder would run (D3237).
	ownFile := "version: 1\nname: review\nsteps:\n  - review: { max_rounds: 1 }\n"
	folder := map[string]string{".fugaro/recipes/review.yaml": ownFile}

	t.Run("the copy the task carries runs and is recorded as the project's", func(t *testing.T) {
		h := newHarnessFiles(t, firstLineCfg(t, 3, ""), recipeSpec(projectTask(copied, copyText)), folder)
		rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("changes", 1))
		if err != nil {
			t.Fatal(err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") {
			t.Fatalf("stages = %v, want the copy's two rounds (the file's one and the configured three are both wrong)", got)
		}
		r := rec.Recipe
		if r.Name != copied || r.Source != string(recipe.SourceProject) || r.SHA256 != recipe.Sum([]byte(copyText)) {
			t.Fatalf("recipe = %+v, want the project's copy %s", r, copied)
		}
	})

	t.Run("a task naming the repository's recipe is refused", func(t *testing.T) {
		h := newHarnessFiles(t, firstLineCfg(t, 3, ""),
			recipeSpec(&task.Recipe{Name: "review", Source: string(recipe.SourceRepo)}), folder)
		rec, err := h.run(t, implement("feature"))
		if err == nil || rec.Status != runstore.StatusInfraError || rec.FailureClass != failure.ConfigInvalid {
			t.Fatalf("rec = %+v, err = %v, want a config_invalid infra_error", rec, err)
		}
		for _, want := range []string{"recipe review is the repository's own", "brew upgrade dimipaun/tap/fugaro", "fugaro update"} {
			if !strings.Contains(rec.Reason, want) {
				t.Fatalf("the reason is %q, want one naming %q", rec.Reason, want)
			}
		}
		if len(h.agent.calls) != 0 {
			t.Fatalf("%d agent calls, want none", len(h.agent.calls))
		}
	})
}
