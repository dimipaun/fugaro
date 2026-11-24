package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// This file is L8's release A in the runner (D3304): the repository's
// .fugaro/recipes/ is no longer a layer a run reads. What these tests pin is
// the refusal that replaces it — a task that leaves its recipe to the folder
// is refused at bootstrap, naming the fix — and the one shape that is left
// running, a task that embeds nothing for a name only the catalog holds.

// unembeddedSpec is a task that carries no recipe at all: the shape a launch
// before B16 wrote, whose name then comes from agent.recipe or from default.
func unembeddedSpec() *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
}

// TestRunnerNeverReadsTheFolder is PR3's red step: a run resolves the recipe
// the task carries, and .fugaro/recipes/ — which the runner read first, at
// the task's ref, before this — is not consulted at all. Every case leaves a
// file at the embedded name that the OLD runner would have preferred: an
// invalid one it could not have parsed, one with a different number of
// rounds, and one naming itself something else. A runner that still read the
// folder ends each of them its own way; this one runs the task's two rounds
// every time.
func TestRunnerNeverReadsTheFolder(t *testing.T) {
	own := "version: 1\nname: two-rounds\nsteps:\n  - review: { max_rounds: 1 }\n"
	cases := []struct {
		name string
		file string
	}{
		{"an invalid file at the embedded name", "version: [\n"},
		{"a file with one round at the embedded name", own},
		{"a file naming itself something else", strings.Replace(own, "name: two-rounds", "name: claude-solo", 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarnessFiles(t, firstLineCfg(t, 2, ""), recipeSpec(projectTask("two-rounds", twoRounds)),
				map[string]string{".fugaro/recipes/two-rounds.yaml": c.file})
			rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("changes", 1))
			if err != nil {
				t.Fatal(err)
			}
			if got := stageNames(rec); !eq(got, "implement", "review", "fix", "review") {
				t.Fatalf("stages = %v, want the embedded recipe's two rounds", got)
			}
			if r := rec.Recipe; r.Name != "two-rounds" || r.Source != string(recipe.SourceProject) || r.SHA256 != recipe.Sum([]byte(twoRounds)) {
				t.Fatalf("recipe = %+v", r)
			}
		})
	}
}

// TestRepoSourcedTaskIsRefused is D3304 and R8's first shape: a task naming a
// repository recipe (Source: repo, the name alone) is refused before any
// stage, whether or not the file is still at the ref — the runner has no
// repository layer left to read it from — and the reason carries the fix.
func TestRepoSourcedTaskIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"the file is still at the ref", map[string]string{".fugaro/recipes/two-rounds.yaml": twoRounds}},
		{"the file is already gone", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarnessFiles(t, firstLineCfg(t, 2, ""),
				recipeSpec(&task.Recipe{Name: "two-rounds", Source: "repo"}), c.files)
			rec, err := h.run(t, implement("feature"))
			if err == nil || rec.Status != runstore.StatusInfraError || rec.FailureClass != failure.ConfigInvalid {
				t.Fatalf("rec = %+v, err = %v, want a config_invalid infra_error", rec, err)
			}
			for _, want := range []string{"recipe two-rounds is the repository's own", ".fugaro/recipes", "brew upgrade dimipaun/tap/fugaro", "fugaro update"} {
				if !strings.Contains(rec.Reason, want) {
					t.Fatalf("the reason is %q, want one naming %q", rec.Reason, want)
				}
			}
			if len(h.agent.calls) != 0 {
				t.Fatalf("%d agent calls, want none", len(h.agent.calls))
			}
		})
	}
}

// TestUnembeddedTaskWithTheFileStillThereIsRefused is D3304's second shape: a
// task that embeds no recipe, for a name the ref still holds a file for, is
// refused the same way. Both refs a run reads are walked — the first run's
// own ref, and a follow-up's origin/<ref> — and the file is never opened,
// which its size says: a follow-up meeting a multi-megabyte one is refused
// by the rule, not by a limit the runner used to check before reading.
//
// The third case is the residue D3307 (open item O5) accepts: the folder is
// already gone and only the catalog has the name, so the catalog recipe runs
// — nothing at the ref contradicts it.
func TestUnembeddedTaskWithTheFileStillThereIsRefused(t *testing.T) {
	t.Run("a first run whose ref holds the file", func(t *testing.T) {
		h := newHarnessFiles(t, firstLineCfg(t, 2, "  recipe: two-rounds\n"), unembeddedSpec(),
			map[string]string{".fugaro/recipes/two-rounds.yaml": twoRounds})
		rec, err := h.run(t, implement("feature"))
		if err == nil || rec.Status != runstore.StatusInfraError || rec.FailureClass != failure.ConfigInvalid {
			t.Fatalf("rec = %+v, err = %v, want a config_invalid infra_error", rec, err)
		}
		if !strings.Contains(rec.Reason, ".fugaro/recipes/two-rounds.yaml") {
			t.Fatalf("the reason does not name the file: %q", rec.Reason)
		}
		if len(h.agent.calls) != 0 {
			t.Fatalf("%d agent calls, want none", len(h.agent.calls))
		}
	})

	t.Run("a follow-up whose base holds the file", func(t *testing.T) {
		h := followUpHarness(t, "", nil)
		// Committed after the first run, so the run that opened the pull
		// request ran on a base without it: this is the unmigrated state a
		// follow-up meets, and a follow-up resolves its recipe off
		// origin/<ref> (the base), not off the pull request's branch.
		commitToRemote(t, h.harness, func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{".fugaro/recipes/default.yaml": "# " + strings.Repeat("x", 2<<20)})
		})
		h.followUp(t, followID, runID, "Tidy up.")
		rec, err := h.run(t, implement("tidy"))
		if err == nil || rec.Status != runstore.StatusInfraError || rec.FailureClass != failure.ConfigInvalid {
			t.Fatalf("rec = %+v, err = %v, want a config_invalid infra_error", rec, err)
		}
		if !strings.Contains(rec.Reason, ".fugaro/recipes/default.yaml") || strings.Contains(rec.Reason, "16 KiB") {
			t.Fatalf("the reason is %q, want the rule and no size limit (the file is never read)", rec.Reason)
		}
	})

	t.Run("a ref whose folder is gone, for a catalog name", func(t *testing.T) {
		h := newHarnessFiles(t, firstLineCfg(t, 2, "  recipe: solo\n"), unembeddedSpec(), nil)
		rec, err := h.run(t, implement("feature"), review("ship", 0))
		if err != nil {
			t.Fatal(err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review") {
			t.Fatalf("stages = %v, want the catalog solo's one round", got)
		}
		if r := rec.Recipe; r.Name != "solo" || r.Source != string(recipe.SourceCatalog) {
			t.Fatalf("recipe = %+v, want the catalog's solo", r)
		}
	})
}
