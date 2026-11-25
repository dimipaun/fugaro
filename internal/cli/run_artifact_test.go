package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
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
	// releaseSeal, not release080: both launches here must succeed, so the
	// record has to clear the seal's gate, which every minting launch meets (AB4).
	writeBuildRecord(t, f, appSlug, "web", releaseSeal)
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
	// env's bucket is down, so the entry stands in with a note. It is the
	// repository's own copy that is cached, because that is the name a launch of
	// acme/app reads first (recipe.LookupOrder).
	copied := recipe.CopyName("sizzle", "github", "acme/app")
	stale := localcfg.SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234",
		CheckedAt: time.Now().Add(-48 * time.Hour), YAML: strings.Replace(projectSizzle, "name: sizzle", "name: "+copied, 1)}
	if err := localcfg.SaveRecipeCache(os.Getenv, "aurora", copied, stale); err != nil {
		t.Fatal(err)
	}
	down := unreachableEnv(t, env)
	_, _, err := chooseRecipe(ctx, down, "acme/app", "main", "sizzle", noCheckoutConfig, io.Discard, false)
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cached project recipe sizzle") ||
		!strings.Contains(err.Error(), "the launch is refused") {
		t.Fatalf("err = %v", err)
	}
	// The same resolution with a readable bucket carries no note and launches
	// with its mode, so the refusal is about the unread recipe and not about
	// the recipe being mode: artifact.
	if err := localcfg.DropRecipeCache(os.Getenv, "aurora", copied); err != nil {
		t.Fatal(err)
	}
	_, mode, err := chooseRecipe(ctx, env, "acme/app", "main", "sizzle", noCheckoutConfig, io.Discard, false)
	if err != nil || mode != recipe.ModeArtifact {
		t.Fatalf("mode = %q, err = %v", mode, err)
	}
}

// TestAnArtifactLaunchGatesOnTheImageThatKnowsMode: a spec carrying mode is
// refused outright by a runner older than artifactsSince (task.Parse decodes
// with DisallowUnknownFields), so an artifact launch must not reach such a job
// at all. It fires for an artifact recipe launched with nothing attached just
// as much as for one launched with --attach (Task 6's launch of the same
// recipe). Both subtests are judged by checkRecipeImage, on the text the task
// carries — against artifactsSince, since a mode: artifact recipe is a 0.8.0
// construct — and differ in the name that text reached the task under: a
// project recipe by its own name, a repository's own recipe as the copy the
// project store holds for it (D3303, R7).
//
//   - a recipe the launcher resolves itself (a project or catalog one) is
//     embedded in the task, so checkRecipeImage judges it — against
//     artifactsSince, since a mode: artifact recipe is a 0.8.0 construct;
//   - a repository's own recipe is the project's copy of it (D3303), so it is
//     embedded the same way and judged by the same gate: after D3303 no
//     launcher path resolves a recipe the task does not carry, which is why
//     the spec.Mode gate beside this one has no launch of its own to refuse
//     (PR7 removes the branch, and the launcher's other readers of a
//     repository-sourced task are what remains of it).
//
// Mutation (revert recipeSinceFor's UsesV08 arm to recipesV07Since): both
// subtests are refused by a gate naming an older release than artifactsSince,
// and fail on the message.
func TestAnArtifactLaunchGatesOnTheImageThatKnowsMode(t *testing.T) {
	t.Run("a recipe the launcher embeds", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
		writeBuildRecord(t, f, appSlug, "web", release070) // knows 0.7.0, not 0.8.0
		wantRefused(t, f, "recipe sizzle needs a job image whose runner knows recipes (fugaro "+artifactsSince+" or later)",
			"run", "--repo", "acme/app", "--recipe", "sizzle", "x")
	})
	t.Run("a repository's own recipe, as its project copy", func(t *testing.T) {
		f := newCloudFixture(t)
		recipeCheckout(t, f, "  recipe: sizzle\n", nil)
		copy := repoRecipeCopy(t, f, "acme/other", "sizzle", projectSizzle)
		writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", release070)
		wantRefused(t, f, "recipe "+copy+" needs a job image whose runner knows recipes (fugaro "+artifactsSince+" or later)",
			"run", "--run-id", "20261015-100000-abcd", "x")
	})
}

// release075 is the first job image whose runner's clone side exists
// (cloneSince): past the checkout: clone floor, but before artifactsSince.
const release075 = "ghcr.io/dimipaun/fugaro-base:0.7.5"

// cloneCheckout is a checkout: clone workflow of acme/other, as
// TestRunClonedWorkflowWithRecipeLaunchesWithNoBuildRecord builds one: its
// job runs the installation's base image base directly (there is no derived
// image, and so no build record), with files written into the working tree.
func cloneCheckout(t *testing.T, f *cloudFixture, base, agentLines string, extra map[string]string) {
	t.Helper()
	f.appendConfig(t, "base_images: {base: "+base+"}\n")
	testutil.IsolateGit(t)
	other := mustSlug("github", "acme/other")
	f.run.AddJob(gcp.JobName(other, "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	files := map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nagent:\n  auth: api-key\n" + agentLines +
		"workflows:\n  svc: { checkout: clone, commands: { build: sh build.sh, test: sh test.sh } }\n"}
	for k, v := range extra {
		files[k] = v
	}
	testutil.WriteFiles(t, dir, files)
	t.Chdir(dir)
}

// TestAnArtifactRecipeOnACloneWorkflowGatesOnTheCloneImage is the clone half
// of TestAnArtifactLaunchGatesOnTheImageThatKnowsMode: a clone workflow has no
// build record, so checkCloneImageSince reads the installation's own base image
// instead. Both subtests reach checkCloneRecipeImage and recipeSinceFor, whose
// UsesV08 arm raises the threshold to artifactsSince — without it the 0.7.5
// image passes and the spec.Mode gate below refuses instead, with the other
// gate's message — and differ in the name the task carries the recipe under: a
// project recipe by its own name, a repository's own recipe as the project's
// copy of it (D3303, R7).
//
//   - a recipe the launcher embeds (here a project one, the same as in the
//     baked test) reaches checkCloneRecipeImage and recipeSinceFor, whose
//     UsesV08 arm raises the threshold to artifactsSince — without it the
//     0.7.5 image passes and the spec.Mode gate below refuses instead, with
//     the other gate's message;
//   - a repository's own recipe, which the launcher resolves as the project's
//     copy of it (D3303), is embedded and judged by that same gate: no
//     launcher path resolves a repository recipe the task does not carry, so
//     the spec.Mode gate's clone arm has nothing left to refuse (PR7 removes
//     it).
//
// Mutation (drop the `UsesV08` arm of recipeSinceFor): both subtests are
// refused by a gate naming an older release than artifactsSince, and fail on
// the message.
func TestAnArtifactRecipeOnACloneWorkflowGatesOnTheCloneImage(t *testing.T) {
	t.Run("a recipe the launcher embeds", func(t *testing.T) {
		f := newCloudFixture(t)
		projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
		cloneCheckout(t, f, release075, "  recipe: sizzle\n", nil)
		wantRefused(t, f, "recipe sizzle needs a job image whose runner knows recipes (fugaro "+artifactsSince+" or later)",
			"run", "--run-id", "20261015-100000-abcd", "Render the nightly bundle")
	})
	t.Run("a repository's own recipe, as its project copy", func(t *testing.T) {
		f := newCloudFixture(t)
		cloneCheckout(t, f, release075, "", nil)
		copy := repoRecipeCopy(t, f, "acme/other", "sizzle", projectSizzle)
		// The whole read-path sentence, not only its "needs a job image"
		// half: the read path judges a BUILD RECORD, and for a clone
		// workflow there is none, so its own refusal says "has no build
		// record" — the shared prefix alone would let this subtest pass
		// against the wrong gate (which is exactly the mutation above).
		wantRefused(t, f, "recipe "+copy+" needs a job image whose runner knows recipes (fugaro "+artifactsSince+" or later), "+
			"but the checkout: clone job image of acme/other workflow svc is "+release075+", release 0.7.5",
			"run", "--run-id", "20261015-100000-abcd", "--recipe", "sizzle", "Render the nightly bundle")
	})
}

// appRepoCheckout is a working directory whose origin is acme/app, with files
// of the caller's own: a launcher to be run from a checkout of that
// repository. It is secrets_test.go's appCheckout with extra files. It is no
// longer where a repository recipe is read — the launch reads the project's
// copy of one (D3237) — so a test that wants a recipe in play publishes it
// (projectRecipesFixture, publishCopy).
func appRepoCheckout(t *testing.T, files map[string]string) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	all := map[string]string{"fugaro.yaml": "version: 1\nproject: aurora\ngit: { provider: github, base_branch: main }\n"}
	for k, v := range files {
		all[k] = v
	}
	testutil.WriteFiles(t, dir, all)
	t.Chdir(dir)
}

// TestAFollowUpLaunchRefusesAModeItInheritsByRepoName: a follow-up does not
// choose its recipe, it keeps the previous run's, and a repository recipe is
// inherited as a NAME — the previous task's task.Recipe, or the record of the
// recipe the runner chose, both of which the follow-up resolves and embeds
// (D3303, R7). The resolution is the launcher's, so the refusal judges the copy
// the project holds rather than leaving the mode to the runner: the runner
// refuses it too (internal/runner/runner.go), but not before a job starts and
// clones. The two modes a follow-up cannot run are mode: artifact (nothing to
// continue) and mode: review (a review run never continues one either). Three
// arms: the two inheritance paths that inherit a name — the previous task's own
// recipe, and the recipe the runner chose for it (carryChosenRecipe) — and the
// second refusal, which shares the path.
//
// Mutation (drop the `else if rcp != nil` branch of newFollowUpSpec): the
// first arm launches a job; mutation (drop the followUpRecipeOfName call from
// carryChosenRecipe): the second arm launches one. Both fail wantRefused on the
// execution count as well as the missing refusal.
func TestAFollowUpLaunchRefusesAModeItInheritsByRepoName(t *testing.T) {
	t.Run("the previous task's recipe", func(t *testing.T) {
		f := newCloudFixture(t)
		appRepoCheckout(t, nil)
		repoRecipeCopy(t, f, "acme/app", "sizzle", projectSizzle)
		writeBuildRecord(t, f, appSlug, "web", release080)
		root := firstRunSpec(rootID)
		root.Recipe = &task.Recipe{Name: "sizzle", Source: string(recipe.SourceRepo)}
		seedSpec(t, f, root, false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-72 * time.Hour)
		writeRecord(t, f, rootID, rec)
		wantRefused(t, f, "it takes no --pr", "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Render the bundle")
	})
	t.Run("the recipe the runner chose", func(t *testing.T) {
		f := newCloudFixture(t)
		appRepoCheckout(t, nil)
		repoRecipeCopy(t, f, "acme/app", "sizzle", projectSizzle)
		seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "sizzle", Source: string(recipe.SourceRepo)})
		wantRefused(t, f, "it takes no --pr", "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Render the bundle")
	})
	t.Run("a mode: review recipe", func(t *testing.T) {
		f := newCloudFixture(t)
		appRepoCheckout(t, nil)
		repoRecipeCopy(t, f, "acme/app", "peek", "version: 1\nname: peek\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n")
		writeBuildRecord(t, f, appSlug, "web", release080)
		root := firstRunSpec(rootID)
		root.Recipe = &task.Recipe{Name: "peek", Source: string(recipe.SourceRepo)}
		seedSpec(t, f, root, false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-72 * time.Hour)
		writeRecord(t, f, rootID, rec)
		wantRefused(t, f, "which only reviews an existing pull request and cannot continue one",
			"run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Have another look")
	})
}

// TestAFollowUpLaunchRefusesAModeItInheritsByText is the other half of
// TestAFollowUpLaunchRefusesAModeItInheritsByRepoName: the previous run's
// recipe can also arrive as TEXT — an embedded project or catalog recipe in
// the previous task.json (taskRecipe, recipes_resolve.go) — and that text is
// parsed again at the launch (newFollowUpSpec), so both follow-up refusals
// judge it before any cloud work starts. Without the artifact refusal there,
// the launch names no mode of its own (a follow-up's spec carries none), the
// runner refuses the pair at bootstrap instead, and a job has already started
// and cloned by then.
//
// Mutation (drop the `refuseFollowUpArtifactMode(inherited, rcp.Name)` call
// from newFollowUpSpec's `rcp.YAML != ""` branch): this test starts a job and
// fails wantRefused on the execution count as well as on the missing refusal.
func TestAFollowUpLaunchRefusesAModeItInheritsByText(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", release080)
	root := firstRunSpec(rootID)
	root.Recipe = &task.Recipe{Name: "sizzle", Source: string(recipe.SourceProject),
		SHA256: recipe.Sum([]byte(projectSizzle)), YAML: projectSizzle}
	seedSpec(t, f, root, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.StartedAt = time.Now().Add(-72 * time.Hour)
	writeRecord(t, f, rootID, rec)
	wantRefused(t, f, "it takes no --pr", "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Render the bundle")
}

// TestAFollowUpLaunchRefusesAModeItCarriedFromAChosenProjectRecipe: a
// follow-up with no inherited recipe keeps the one the RUNNER chose for the
// previous run (carryChosenRecipe) — a record, not a task recipe, naming a
// recipe by name and source. When that is a project or catalog recipe the
// launcher can resolve it here (unlike a repository recipe, whose text lives
// at the ref), so the same two follow-up refusals judge it at the launch
// rather than leaving the whole cost of the refusal to the runner.
//
// Mutation (drop the `refuseFollowUpArtifactMode(rr.Recipe, rr.Name)` call
// from carryChosenRecipe): this test starts a job and fails wantRefused on
// the execution count as well as on the missing refusal.
func TestAFollowUpLaunchRefusesAModeItCarriedFromAChosenProjectRecipe(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	projectRecipesFixture(t, f, map[string]string{"sizzle": projectSizzle})
	seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "sizzle", Source: string(recipe.SourceProject),
		SHA256: recipe.Sum([]byte(projectSizzle))})
	wantRefused(t, f, "it takes no --pr", "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Render the bundle")
}
