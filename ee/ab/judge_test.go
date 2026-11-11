// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// The judge run's recipe (A5.2): the shape the core parser is taught by this
// package's own registration, and the invariants the run's own mode carries.
func TestJudgeRecipeParses(t *testing.T) {
	rcp, problems := recipe.Parse(RecipeText())
	if len(problems) > 0 {
		t.Fatalf("the judge recipe is refused: %s", recipe.ProblemsText(problems))
	}
	if rcp.Name != RecipeName {
		t.Errorf("the recipe is named %q, want %q", rcp.Name, RecipeName)
	}
	if rcp.Mode != recipe.ModeEvaluate {
		t.Errorf("mode = %q, want %q", rcp.Mode, recipe.ModeEvaluate)
	}
	if len(rcp.Steps) != 1 || rcp.Steps[0].Kind != stepJudge {
		t.Errorf("steps = %+v, want the one %s step", rcp.Steps, stepJudge)
	}
}

// TestJudgeRecipeRunsNoRepositoryCommand pins the mode's whole point: a judge
// run holds no checkout of the repository's own steps, so a recipe that asked
// for one — a test command, an implement loop, a review — would be a judge run
// running code instead of reading two diffs.
func TestJudgeRecipeRunsNoRepositoryCommand(t *testing.T) {
	for _, kind := range []recipe.StepKind{
		recipe.StepCheck, recipe.StepFirstLine, recipe.StepReview, recipe.StepRender,
	} {
		text := "version: 1\nname: ab-judge\nmode: evaluate\nsteps:\n  - " + string(kind) + ": {}\n"
		_, problems := recipe.Parse([]byte(text))
		if len(problems) == 0 {
			t.Errorf("a mode: evaluate recipe holding %s parses", kind)
			continue
		}
		if !strings.Contains(recipe.ProblemsText(problems), "mode: evaluate") {
			t.Errorf("%s: problems = %s, want one naming mode: evaluate", kind, recipe.ProblemsText(problems))
		}
	}
}
