package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// noCheckoutConfig is chooseRecipe's checkout accessor for a launch made away
// from any repository: the recipe is resolved by name alone, as
// `fugaro run --repo` does in the cloud.
func noCheckoutConfig() *config.Config { return nil }

// release080 is a job image new enough for a mode: artifact recipe and a task
// carrying mode: the first release whose runner implements it (artifactsSince).
const release080 = "ghcr.io/dimipaun/fugaro-base:0.8.0"

// projectSizzle is a project recipe of the third mode: one render step and a
// declared deliverable.
const projectSizzle = "version: 1\nname: sizzle\nmode: artifact\nsteps:\n  - render: {}\noutputs: { globs: [out/*.mp4] }\n"

// TestArtifactLaunchWritesTheModeIntoTheTask is the launcher's side of
// Spec.Mode: the field the runner selects the run's shape from before it can
// resolve the recipe itself. The launcher writes it from the recipe it
// resolved, and writes NOTHING for a recipe of either other mode, so every
// spec an older release would have written is byte-identical on the wire.
//
// Mutation (drop `Mode: plan.mode` from buildAttemptSpec): the artifact task
// carries no mode, and this test fails on the first assertion; mutation (set
// Mode unconditionally, from the recipe, whatever its mode): the implement
// launch's task grows a "mode" key it never had, and the second assertion
// fails.
func TestArtifactLaunchWritesTheModeIntoTheTask(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
	writeBuildRecord(t, f, appSlug, "web", release080)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261015-100000-abcd", "--recipe", "sizzle", "Render the nightly bundle")
	if err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, "20261015-100000-abcd").Mode; got != recipe.ModeArtifact {
		t.Fatalf("task mode = %q, want %q", got, recipe.ModeArtifact)
	}
	// An implement recipe writes nothing: the field is omitted entirely, and
	// every spec the previous release wrote stays byte-identical.
	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20261015-110000-abcd", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, "20261015-110000-abcd").Mode; got != "" {
		t.Fatalf("an implement launch wrote mode %q, want the field omitted", got)
	}
}

// TestArtifactLaunchRefusesPRAndAttempts: the two launch shapes an artifact
// run cannot have, refused by name before anything is written. It opens no
// pull request, so it takes no --pr (and so no follow-up, which exists to
// resolve a pull request's chain) and no --attempts (a best-of-N batch is
// defined by the pull requests its runs open). --retry and --run-id are
// deliberately allowed and are not exercised here.
//
// Mutation (drop refuseFollowUpArtifactMode from followup.go's three sites):
// the --pr launch reaches the follow-up machinery and launches a job; mutation
// (drop the ModeArtifact arm of chooseRecipe's forAttempts refusal): --attempts
// launches a batch. Either way wantRefused fails on the execution count too.
func TestArtifactLaunchRefusesPRAndAttempts(t *testing.T) {
	f := newCloudFixture(t)
	projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
	writeBuildRecord(t, f, appSlug, "web", release080)
	// A pull request for the follow-up path to resolve: PR 7, opened by a run
	// of this repository. Without it the launch is refused for the missing
	// pull request rather than for the recipe, and the test would pass for
	// the wrong reason.
	root := firstRunSpec(rootID)
	seedSpec(t, f, root, false)
	writeRecord(t, f, rootID, prRecord(rootID, "", 7, 1))
	wantRefused(t, f, "it takes no --pr", "run", "--repo", "acme/app", "--recipe", "sizzle", "--pr", "7", "--run-id", fuID, "x")
	wantRefused(t, f, "cannot use it", "run", "--repo", "acme/app", "--recipe", "sizzle", "--attempts", "2", "x")
}

// TestAModeArtifactLaunchRefusesWhenTheRecipeCannotBeRead is the launcher's
// fail-closed half: resolveRecipe has paths where what it returns is NOT the
// recipe the run will get — here, a cached project recipe standing in for an
// unreachable bucket. On those the mode would stay empty for a recipe that is
// mode: artifact, the spec would be indistinguishable from an implement one,
// and the image gate would not fire: a silent pull request where the owner
// asked for a file. So the launch is refused with the resolution's own note as
// its reason, before anything is written.
//
// Mutation (drop the rr.Note refusal from chooseRecipe): the launch proceeds
// and a job starts on an empty mode.
func TestAModeArtifactLaunchRefusesWhenTheRecipeCannotBeRead(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
	env := fileEnv(t, f)
	ctx := context.Background()
	// A stale cache entry of this project's bucket, as a successful read two
	// days ago would have left: the entry is too old to answer on its own
	// (decision D9), so a readable bucket would be read strictly — and this
	// env's bucket is down, so the entry stands in with a note.
	stale := localcfg.SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now().Add(-48 * time.Hour), YAML: projectSizzle}
	if err := localcfg.SaveRecipeCache(os.Getenv, "aurora", "sizzle", stale); err != nil {
		t.Fatal(err)
	}
	down := unreachableEnv(t, env)
	_, _, err := chooseRecipe(ctx, down, "sizzle", noCheckoutConfig, "", io.Discard, false)
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cached project recipe sizzle") ||
		!strings.Contains(err.Error(), "the launch is refused") {
		t.Fatalf("err = %v", err)
	}
	// The same resolution with a readable bucket carries no note and launches
	// with its mode, so the refusal is about the unread recipe and not about
	// the recipe being mode: artifact.
	if err := localcfg.DropRecipeCache(os.Getenv, "aurora", "sizzle"); err != nil {
		t.Fatal(err)
	}
	_, mode, err := chooseRecipe(ctx, env, "sizzle", noCheckoutConfig, "", io.Discard, false)
	if err != nil || mode != recipe.ModeArtifact {
		t.Fatalf("mode = %q, err = %v", mode, err)
	}
}

// TestAnArtifactLaunchGatesOnTheImageThatKnowsMode: a spec carrying mode is
// refused outright by a runner older than artifactsSince (task.Parse decodes
// with DisallowUnknownFields), so an artifact launch must not reach such a job
// at all. It fires for an artifact recipe launched with nothing attached just
// as much as for one launched with --attach (Task 6's launch of the same
// recipe). Two gates can carry it, and both are asserted because which one
// fires depends on what the launcher could embed:
//
//   - a recipe the launcher resolves itself (a project or catalog one) is
//     embedded in the task, so checkRecipeImage judges it — against
//     artifactsSince, since a mode: artifact recipe is a 0.8.0 construct;
//   - a repository recipe is NOT embedded (the runner re-reads the file at
//     the ref), so checkRecipeImage judges it at the plain recipesSince and
//     the spec.Mode gate beside the project layer's and the named token's is
//     what refuses the launch.
//
// Mutation (drop the spec.Mode == ModeArtifact block from runRun's gates): the
// repository-recipe subtest is not refused and a job starts on an image whose
// runner cannot parse its task; mutation (revert checkRecipeImage's UsesV08
// arm to recipesV07Since): the project-recipe subtest passes the 0.7.0 image
// and starts that job.
func TestAnArtifactLaunchGatesOnTheImageThatKnowsMode(t *testing.T) {
	t.Run("a recipe the launcher embeds", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
		writeBuildRecord(t, f, appSlug, "web", release070) // knows 0.7.0, not 0.8.0
		wantRefused(t, f, "recipe sizzle needs a job image whose runner knows recipes (fugaro "+artifactsSince+" or later)",
			"run", "--repo", "acme/app", "--recipe", "sizzle", "x")
	})
	t.Run("a repository recipe", func(t *testing.T) {
		f := newCloudFixture(t)
		recipeCheckout(t, f, "  recipe: sizzle\n", map[string]string{recipe.RepoPath("sizzle"): projectSizzle})
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", release070)
		wantRefused(t, f, "needs a job image whose runner knows mode: artifact (fugaro "+artifactsSince+" or later)",
			"run", "--run-id", "20261015-100000-abcd", "x")
	})
}
