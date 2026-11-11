package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// rebaseTask is the task.Recipe a follow-up inherits for a rebase run: the
// catalog's own text, which is what the launcher embeds once it resolves the
// name (a follow-up never chooses a recipe of its own, §5).
func rebaseTask(t *testing.T) *task.Recipe {
	t.Helper()
	text, ok := recipe.CatalogText("rebase")
	if !ok {
		t.Fatal("rebase is not in the catalog")
	}
	return &task.Recipe{Name: "rebase", Source: string(recipe.SourceCatalog), YAML: string(text), SHA256: recipe.Sum(text)}
}

// leaveTheConflict is a conflict-resolution stage that resolves nothing: the
// tree keeps the markers git wrote, which is what every stage of a run that
// cannot resolve a conflict finds.
func leaveTheConflict(_ *testing.T, _ context.Context, _ agent.Request) (agent.Result, error) {
	return agent.Result{}, nil
}

// resolveTheConflict is a conflict-resolution stage that does the work: it
// writes the file both sides added with both sides' content, which is what a
// resolution is.
func resolveTheConflict(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	t.Helper()
	shell(t, req, "printf 'the base side\\nthe branch side\\n' > feature.txt")
	return agent.Result{}, nil
}

// TestRecipeRebaseResolvesAndPushes: a rebase run that resolves its conflict
// puts the pull request's branch on its base and pushes it — the one thing
// the recipe asks for. The replayed commit carries the resolution, the tests
// are run again before the push, and the record's head is the rebased one,
// which is the head rung 1 is launched at (G13): the push is the force
// with lease a rewritten branch needs, so the branch it replaces is the one
// this run read at its start.
func TestRecipeRebaseResolvesAndPushes(t *testing.T) {
	h := followUpHarness(t, "", nil, commitOnly("feature"), review("ship", 0))
	commitToRemote(t, h.harness, func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"feature.txt": "the base's side\n"})
	})
	pushes := countPushes(t, h.harness)
	h.followUpRecipe(t, followID, runID, "Put it back on main.", rebaseTask(t))
	rec, err := h.run(t, resolveTheConflict)
	if err != nil || rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v, err = %v; want succeeded/none", rec, err)
	}
	if rec.PushedHead == "" || rec.PushedHead != rec.HeadSHA {
		t.Fatalf("the record's head = %q and pushed head = %q, want the rebased commit they both are", rec.HeadSHA, rec.PushedHead)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.PushedHead {
		t.Fatalf("the branch on origin is %s, want the pushed head %s", got, rec.PushedHead)
	}
	if n := pushes(); n != 1 {
		t.Fatalf("%d pushes, want the rebased branch pushed once", n)
	}
	// The branch sits on its base: the commit the base moved to is an
	// ancestor of what was pushed, which is what the rebase was for.
	base := testutil.Git(t, h.remote, "rev-parse", "refs/heads/main")
	testutil.Git(t, h.remote, "merge-base", "--is-ancestor", base, rec.PushedHead)
	if body := testutil.Git(t, h.remote, "show", rec.PushedHead+":feature.txt"); body != "the base side\nthe branch side" {
		t.Fatalf("feature.txt = %q, want the resolution the replay carries", body)
	}
	if len(h.agent.calls) != 1 {
		t.Fatalf("%d conflict-resolution stages, want one for the one conflict", len(h.agent.calls))
	}
}

// TestRecipeRebaseRefusesOnUnresolvedConflict: a rebase run whose conflicts
// no stage resolves ends refused, and nothing of it reaches the branch. The
// step is the whole run (recipe.Parse keeps the rebase step alone), so what
// it leaves is a record that says why and a branch exactly where it was: no
// commit carrying git's conflict markers, no push, no pull request touched.
//
// Mutation (drop the marker check from gitops.RebaseContinue): the staged
// markers are committed as content and the run pushes a branch whose file
// holds them, so the record is a success rather than this refusal.
func TestRecipeRebaseRefusesOnUnresolvedConflict(t *testing.T) {
	h := followUpHarness(t, "", nil, commitOnly("feature"), review("ship", 0))
	// The base moves on with its own version of the file the branch added:
	// the same path, different content, which is a conflict for every commit
	// the rebase replays.
	commitToRemote(t, h.harness, func(dir string) {
		testutil.WriteFiles(t, dir, map[string]string{"feature.txt": "the base's side\n"})
	})
	pushes := countPushes(t, h.harness)
	h.followUpRecipe(t, followID, runID, "Put it back on main.", rebaseTask(t))
	rec, err := h.run(t, leaveTheConflict, leaveTheConflict, leaveTheConflict)
	if err != nil || rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v, err = %v; want failed/none", rec, err)
	}
	if !strings.Contains(rec.Reason, "conflict") || !strings.Contains(rec.Reason, "feature.txt") {
		t.Fatalf("the reason does not name the conflict it could not resolve: %q", rec.Reason)
	}
	if len(h.agent.calls) != 3 {
		t.Fatalf("%d conflict-resolution stages, want 3: the step's own bound, spent and then refused", len(h.agent.calls))
	}
	if n := pushes(); n != 0 {
		t.Fatalf("%d pushes, want none: an unresolved conflict must not reach the branch", n)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != h.first.PushedHead {
		t.Fatalf("the branch is at %s, want %s: it moved under a refusal", got, h.first.PushedHead)
	}
}
