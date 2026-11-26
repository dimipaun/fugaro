package cli

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestNoLauncherPathReadsARepoRecipe: a task naming a repository recipe
// (Source: repo) is one a launcher before release A wrote — no launcher of
// this release writes one, since a repository's recipe reaches a run as the
// project's copy of it (D3303, R7) — and the launcher reads the repository's
// .fugaro/recipes/ for it on no path any more. The two launcher paths that
// judged such a task by that file are here: --retry, which refused a report
// run (digestRecipe), and the review of the run a pull request came from,
// which judged the run's own step for its spec (ownStepRecipe). Both judge
// the text the task carries, so a repository task answers no to both: the
// checkout below holds the file in each arm, and a reader that opened it
// would answer yes.
func TestNoLauncherPathReadsARepoRecipe(t *testing.T) {
	t.Run("--retry of a report recipe's task", func(t *testing.T) {
		f := digestFixture(t)
		reportRecipeCheckout(t)
		id := manifestRunID(time.Now().Add(-time.Hour))
		spec := firstRunSpec(id)
		spec.Recipe = &task.Recipe{Name: "report", Source: string(recipe.SourceRepo)}
		seedSpec(t, f, spec, false)

		if _, _, err := execute(t, "run", "--retry", appSlug+"/"+id); err != nil {
			t.Fatalf("--retry of a task naming a repository recipe: %v — the launcher judges the text the task carries, and this one carries none", err)
		}
	})
	t.Run("a review of the run", func(t *testing.T) {
		f := newCloudFixture(t)
		reportRecipeCheckout(t)
		spec := firstRunSpec(rootID)
		spec.Task = "periodic run review"
		spec.Recipe = &task.Recipe{Name: "report", Source: string(recipe.SourceRepo)}
		seedLaunchRootSpec(t, f, spec, 7, time.Now().Add(-72*time.Hour))

		rs := launchSpecOf(t, f, 7)
		if rs == nil || rs.Source != task.ReviewSpecRun || rs.Text != spec.Task {
			t.Fatalf("review spec = %+v, want the run's own text: the launcher judges the text the task carries, so a task naming a repository recipe is an ordinary run's", rs)
		}
	})
}
