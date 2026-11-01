package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// digestLaunchImage is the base image a launch with --recipe run-digest
// needs: the digest step is 0.13.0's (digestSince), so an older build record
// is refused at launch — after the manifest is listed, which comes first,
// but before any execution starts.
const digestLaunchImage = "ghcr.io/dimipaun/fugaro-base:0.13.0"

// digestOldImage is the newest release that predates the digest step: a
// report launch against a job image built from it is refused (digestSince).
const digestOldImage = "ghcr.io/dimipaun/fugaro-base:0.12.0"

// digestRepoRecipe is a repository's own report recipe: the digest step
// first, which is all digestRecipe reads to call a launch a report run.
const digestRepoRecipe = "version: 1\nname: report\ndescription: This repository's own run report recipe.\n" +
	"use_when: A scheduled run review only, launched by the repository's own routine.\n" +
	"steps:\n  - digest: {}\n  - review:\n      max_rounds: 1\n"

// reportRecipeCheckout makes the working directory a checkout of acme/app
// carrying digestRepoRecipe as .fugaro/recipes/report.yaml, the file a
// launch of that repository recipe is resolved from.
func reportRecipeCheckout(t *testing.T) {
	t.Helper()
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	recipes := filepath.Join(dir, ".fugaro", "recipes")
	if err := os.MkdirAll(recipes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipes, "report.yaml"), []byte(digestRepoRecipe), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// manifestRunID is a run ID of the one shape, minted at.
func manifestRunID(at time.Time) string { return at.UTC().Format("20060102-150405") + "-abcd" }

// digestFixture is a fixture whose acme/app image can run the digest step.
func digestFixture(t *testing.T) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", digestLaunchImage)
	return f
}

// seedManifestRun stores a run of slug whose task.json is what the launcher
// would have written for it: a report run (runs non-empty) carries the
// catalog run-digest recipe, which is the recipe the runner reads the digest
// step from, and an ordinary run carries none.
func seedManifestRun(t *testing.T, f *cloudFixture, repo, slug, id string, runs ...string) {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: id, Repo: repo, Ref: "main", Workflow: "web", Task: "Periodic run review", DigestRuns: runs}
	if len(runs) > 0 {
		text, ok := recipe.CatalogText("run-digest")
		if !ok {
			t.Fatal("no catalog recipe run-digest")
		}
		spec.Recipe = &task.Recipe{Name: "run-digest", Source: string(recipe.SourceCatalog), SHA256: recipe.Sum(text), YAML: string(text)}
	}
	if err := runstore.Open(statsBucket(t, f), slug, id).CreateTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
}

// writeManifestRecord gives the run a record in status.
func writeManifestRecord(t *testing.T, f *cloudFixture, repo, slug, id string, status runstore.Status) {
	t.Helper()
	at := time.Now()
	rec := &runstore.Record{Version: 1, RunID: id, Repo: repo, Workflow: "web", Status: status, StartedAt: at.Add(-time.Minute)}
	if status != runstore.StatusRunning {
		rec.FinishedAt = &at
	}
	if err := runstore.Open(statsBucket(t, f), slug, id).WriteRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

// writeManifestLaunch gives the run a launch.json and no record: where a
// report run sits between its launch and its first status update.
func writeManifestLaunch(t *testing.T, f *cloudFixture, slug, id string) {
	t.Helper()
	l := &runstore.Launch{Version: 1, RunID: id, Backend: "cloud-run", Job: "fugaro-app-web", LaunchedAt: time.Now()}
	if err := runstore.Open(statsBucket(t, f), slug, id).WriteLaunch(context.Background(), l); err != nil {
		t.Fatal(err)
	}
}

// launchWith launches a run of acme/app through the CLI with the recipe name
// given, and returns the task the launcher stored for it.
func launchWith(t *testing.T, f *cloudFixture, name, runID string) *task.Spec {
	t.Helper()
	spec, _ := launchWithWarn(t, f, name, runID)
	return spec
}

// launchWithWarn is launchWith, with the launch's stderr.
func launchWithWarn(t *testing.T, f *cloudFixture, name, runID string) (*task.Spec, string) {
	t.Helper()
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--recipe", name, "--run-id", runID, "Periodic run review")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := runstore.Open(statsBucket(t, f), appSlug, runID).ReadTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return spec, errOut
}

// TestDigestManifestThisRepositoryOnly is the headline: the manifest a
// report run carries names the runs of this repository and no other. The
// digest step reads the records it is named and never lists — a bucket
// listing is a call the job's own grant cannot hold — so this list alone
// decides which records a run reads, and one naming another repository's run
// would put that repository's figures into this repository's committed
// report.
//
// Mutation: listing runs/ instead of runs/<slug>/ in digestManifest puts the
// other repository's newer run at the head of the manifest, and this test
// fails on the comparison.
func TestDigestManifestThisRepositoryOnly(t *testing.T) {
	f := digestFixture(t)
	now := time.Now()
	mine := []string{
		manifestRunID(now.Add(-1 * time.Hour)),
		manifestRunID(now.Add(-2 * time.Hour)),
		manifestRunID(now.Add(-3 * time.Hour)),
	}
	for _, id := range mine {
		seedManifestRun(t, f, "acme/app", appSlug, id)
	}
	// The other repository's runs sit on both sides of this one's newest, so
	// a listing that lost the slug is caught whichever end it starts at.
	other := mustSlug("github", "acme/other")
	seedManifestRun(t, f, "acme/other", other, manifestRunID(now.Add(-30*time.Minute)))
	seedManifestRun(t, f, "acme/other", other, manifestRunID(now.Add(-4*time.Hour)))

	spec := launchWith(t, f, "run-digest", "20261028-120000-abcd")
	if !slices.Equal(spec.DigestRuns, mine) {
		t.Fatalf("digest_runs = %v, want %v", spec.DigestRuns, mine)
	}
}

// TestDigestManifestBounded: the manifest is this repository's runs of the
// last 40 days — the month the reports cover plus the seven days the digest
// step catches up — newest first and at most task.MaxDigestRuns of them. A
// repository with more runs than the cap has the cut said on stderr rather
// than made in silence: the records left out are records this month's report
// will not carry, and nothing else in the launch says so.
func TestDigestManifestBounded(t *testing.T) {
	now := time.Now()
	recent := []string{
		manifestRunID(now.Add(-1 * time.Hour)),
		manifestRunID(now.Add(-2 * time.Hour)),
		manifestRunID(now.Add(-3 * time.Hour)),
		manifestRunID(now.Add(-4 * time.Hour)),
		manifestRunID(now.Add(-5 * time.Hour)),
	}
	old := manifestRunID(now.Add(-41 * 24 * time.Hour))
	for _, tc := range []struct {
		name     string
		cap      int
		want     []string
		wantWarn string
	}{
		{"the 40 day window", 0, recent, ""},
		{"the cap", 3, recent[:3], "more than a report run reads at once; its report covers the newest 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := digestFixture(t)
			for _, id := range recent {
				seedManifestRun(t, f, "acme/app", appSlug, id)
			}
			seedManifestRun(t, f, "acme/app", appSlug, old)
			if tc.cap > 0 {
				defer func(c int) { digestManifestMax = c }(digestManifestMax)
				digestManifestMax = tc.cap
			}
			spec, errOut := launchWithWarn(t, f, "run-digest", "20261028-120000-abcd")
			if !slices.Equal(spec.DigestRuns, tc.want) {
				t.Fatalf("digest_runs = %v, want %v", spec.DigestRuns, tc.want)
			}
			if !strings.Contains(errOut, tc.wantWarn) {
				t.Fatalf("stderr = %q, want %q", errOut, tc.wantWarn)
			}
			if tc.wantWarn == "" && strings.Contains(errOut, "reads at once") {
				t.Fatalf("stderr = %q, want no note about a cut manifest", errOut)
			}
		})
	}
}

// TestDigestLaunchRefusedWhileOneRuns: a repository has one report run at a
// time, so a launch while one of its report runs is still going is refused
// before anything is launched, naming the run that holds it. Every shape a
// report run can be in while it is going holds the slot -- no record yet,
// a record still running, a launch.json with no record -- and a run that has
// finished does not, nor does an ordinary run of the same repository, which
// never reads a record at all, nor one cancelled before it launched, which
// will never write anything.
func TestDigestLaunchRefusedWhileOneRuns(t *testing.T) {
	now := time.Now()
	ordinary := manifestRunID(now.Add(-45 * time.Minute))
	for _, tc := range []struct {
		name string
		// holds is whether the shape is a report run still going, which the
		// launch below must be refused for; a shape that does not hold it
		// launches, and the manifest names the run.
		holds bool
		seed  func(t *testing.T, f *cloudFixture, id string)
	}{
		{"no record yet", true, func(t *testing.T, f *cloudFixture, id string) {}},
		{"a record still running", true, func(t *testing.T, f *cloudFixture, id string) {
			writeManifestRecord(t, f, "acme/app", appSlug, id, runstore.StatusRunning)
		}},
		{"a launch with no record", true, func(t *testing.T, f *cloudFixture, id string) {
			writeManifestLaunch(t, f, appSlug, id)
		}},
		{"a cancel marker and nothing else", false, func(t *testing.T, f *cloudFixture, id string) {
			if err := runstore.Open(statsBucket(t, f), appSlug, id).RequestCancel(context.Background()); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := digestFixture(t)
			running := manifestRunID(now.Add(-30 * time.Minute))
			seedManifestRun(t, f, "acme/app", appSlug, running, manifestRunID(now.Add(-2*time.Hour)))
			tc.seed(t, f, running)
			seedManifestRun(t, f, "acme/app", appSlug, ordinary)

			if !tc.holds {
				spec := launchWith(t, f, "run-digest", "20261028-120000-abcd")
				// The cancelled run's task is still a run of this repository,
				// so the manifest of the launch that follows it names it.
				if !slices.Contains(spec.DigestRuns, running) {
					t.Fatalf("digest_runs = %v, want it to name %s", spec.DigestRuns, running)
				}
				return
			}
			_, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "run-digest", "--run-id", "20261028-120000-abcd", "Periodic run review")
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), running) || !strings.Contains(err.Error(), "one report run at a time") {
				t.Fatalf("second launch: err = %v, want a refusal naming %s", err, running)
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Fatalf("%d executions started, want none", n)
			}
			if _, err := runstore.Open(statsBucket(t, f), appSlug, "20261028-120000-abcd").ReadTask(context.Background()); err == nil {
				t.Fatal("the refused launch stored a task")
			}

			writeManifestRecord(t, f, "acme/app", appSlug, running, runstore.StatusSucceeded)
			spec := launchWith(t, f, "run-digest", "20261028-130000-abcd")
			// The run that finished is still a record of this repository, so
			// the manifest of the launch that follows it names it.
			if !slices.Contains(spec.DigestRuns, running) {
				t.Fatalf("digest_runs = %v, want it to name %s", spec.DigestRuns, running)
			}
		})
	}
}

// TestDigestAttemptsRefused: --attempts launches its attempts together, all
// of them carrying the manifest the plan resolved once, so a report recipe's
// batch would write the same reports into several pull requests at once --
// what the one-report-run-at-a-time refusal above exists to prevent, and
// which that refusal cannot see, since a batch's attempts are stored only as
// they launch. The batch is refused where the recipe is known (chooseRecipe),
// before the manifest is even listed.
func TestDigestAttemptsRefused(t *testing.T) {
	f := digestFixture(t)
	_, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "run-digest", "--attempts", "2", "Periodic run review")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "one report run at a time") {
		t.Fatalf("--attempts: err = %v, want the report recipe's refusal", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions started, want none", n)
	}
	// An ordinary recipe's batch is untouched by it.
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "A task"); err != nil {
		t.Fatalf("--attempts with an ordinary recipe: %v", err)
	}
}

// TestDigestFollowUpRefused: the other launch that cannot carry a report
// recipe is a follow-up (--pr). It never lists a manifest — digestManifest is
// the first-run plan's — and it never runs the one-report-run-at-a-time check
// that listing makes, so a report recipe arriving at one (named explicitly,
// inherited from the run it continues as text or as a repository recipe's
// name, or kept from the recipe the runner chose) would write the reports of
// records it never read, beside a report run already going. Refused where the
// recipe is known, before any cloud work: the image gate would refuse some of
// these for a different reason (the recipe needs 0.13.0) and let the rest
// through to the runner, whose own backstop is the run it has already been
// paid to start.
//
// Mutations: drop the refuseFollowUpReportMode calls from newFollowUpSpec and
// carryChosenRecipe and the one from refuseFollowUpRepoRecipe, and every arm
// but the last launches a job and fails wantRefused on the execution count as
// well as on the missing refusal; drop the prChain.PrevReport arm of
// newFollowUpSpec and the last arm does.
func TestDigestFollowUpRefused(t *testing.T) {
	digestRecipeOf := func(t *testing.T) *task.Recipe {
		t.Helper()
		text, ok := recipe.CatalogText("run-digest")
		if !ok {
			t.Fatal("no catalog recipe run-digest")
		}
		return &task.Recipe{Name: "run-digest", Source: string(recipe.SourceCatalog), SHA256: recipe.Sum(text), YAML: string(text)}
	}
	// wantRefusedReport is wantRefused with the two facts every arm shares:
	// the refusal is the report recipe's, and it names the launch that would
	// run one instead (that recipe's own name: a repository recipe is
	// scheduled by its own name).
	wantRefusedReport := func(t *testing.T, f *cloudFixture, args ...string) {
		t.Helper()
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "writes this repository's run reports") ||
			!strings.Contains(err.Error(), "schedule a report run of its own with fugaro run --recipe ") {
			t.Fatalf("%v: err = %v, want the report recipe's refusal", args, err)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%v: %d executions, want none", args, n)
		}
	}
	// seedReportRoot stores root as a report run on PR 7: the recipe is the
	// catalog's, or the repository's own name, and the manifest is one run's.
	seedReportRoot := func(t *testing.T, f *cloudFixture, rcp *task.Recipe) {
		t.Helper()
		root := firstRunSpec(rootID)
		root.Recipe = rcp
		root.DigestRuns = []string{manifestRunID(time.Now().Add(-2 * time.Hour))}
		seedSpec(t, f, root, false)
		rec := prRecord(rootID, "", 7, 1)
		rec.StartedAt = time.Now().Add(-72 * time.Hour)
		writeRecord(t, f, rootID, rec)
	}

	t.Run("named explicitly", func(t *testing.T) {
		f := digestFixture(t)
		seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
		wantRefusedReport(t, f, "run", "--repo", "acme/app", "--pr", "7", "--recipe", "run-digest", "--run-id", fuID, "Have another look")
	})
	t.Run("inherited as text", func(t *testing.T) {
		f := digestFixture(t)
		seedReportRoot(t, f, digestRecipeOf(t))
		wantRefusedReport(t, f, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Have another look")
	})
	t.Run("inherited as a repository recipe's name", func(t *testing.T) {
		f := digestFixture(t)
		reportRecipeCheckout(t)
		seedReportRoot(t, f, &task.Recipe{Name: "report", Source: string(recipe.SourceRepo)})
		wantRefusedReport(t, f, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Have another look")
	})
	// The same follow-up from a directory that is not a checkout of the
	// repository: the recipe's own file is not there to read, and the
	// previous run's task is what says its recipe is a report one.
	t.Run("inherited as a repository recipe's name, outside the checkout", func(t *testing.T) {
		f := digestFixture(t)
		testutil.IsolateGit(t)
		t.Chdir(t.TempDir())
		seedReportRoot(t, f, &task.Recipe{Name: "report", Source: string(recipe.SourceRepo)})
		wantRefusedReport(t, f, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Have another look")
	})
	t.Run("kept from the recipe the runner chose", func(t *testing.T) {
		f := digestFixture(t)
		text, ok := recipe.CatalogText("run-digest")
		if !ok {
			t.Fatal("no catalog recipe run-digest")
		}
		seedChosenRoot(t, f, &runstore.RecipeRecord{Name: "run-digest", Source: string(recipe.SourceCatalog), SHA256: recipe.Sum(text)})
		wantRefusedReport(t, f, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "Have another look")
	})
}

// TestDigestRetryRefused: --retry launches the stored task of a first launch
// as it stands, so a report run's manifest is the one that launch listed —
// stale by now — and none of the checks a fresh report launch makes (the
// in-flight refusal among them) run at all. A second report run would then
// write the same reports beside one already going, and the report itself
// would cover records that have moved on. The task says it is a report run
// two ways, and both are refused: the manifest it carries (digest_runs is
// accepted under no other recipe, task.Validate) and the recipe itself, for a
// report run whose manifest came out empty.
//
// Mutation (drop the retry refusal from retrySpec): both arms launch a job.
func TestDigestRetryRefused(t *testing.T) {
	text, ok := recipe.CatalogText("run-digest")
	if !ok {
		t.Fatal("no catalog recipe run-digest")
	}
	report := &task.Recipe{Name: "run-digest", Source: string(recipe.SourceCatalog), SHA256: recipe.Sum(text), YAML: string(text)}
	for _, tc := range []struct {
		name   string
		runs   []string
		recipe *task.Recipe
	}{
		{"its manifest", []string{manifestRunID(time.Now().Add(-2 * time.Hour))}, report},
		{"its recipe, with a manifest that came out empty", nil, report},
		{"its recipe, a repository one", nil, &task.Recipe{Name: "report", Source: string(recipe.SourceRepo)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := digestFixture(t)
			id := manifestRunID(time.Now().Add(-time.Hour))
			spec := firstRunSpec(id)
			spec.Recipe, spec.DigestRuns = tc.recipe, tc.runs
			seedSpec(t, f, spec, false)
			if tc.recipe.Source == string(recipe.SourceRepo) {
				reportRecipeCheckout(t)
			} else {
				t.Chdir(t.TempDir())
			}
			_, _, err := execute(t, "run", "--retry", appSlug+"/"+id)
			if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is a report run") {
				t.Fatalf("--retry: err = %v, want the report run's refusal", err)
			}
			if n := len(f.run.Executions()); n != 0 {
				t.Fatalf("%d executions, want none", n)
			}
		})
	}
	// A run that is not a report run is retried as before.
	f := digestFixture(t)
	t.Chdir(t.TempDir())
	id := manifestRunID(time.Now().Add(-time.Hour))
	seedSpec(t, f, firstRunSpec(id), false)
	if _, _, err := execute(t, "run", "--retry", appSlug+"/"+id); err != nil {
		t.Fatalf("--retry of an ordinary run: %v", err)
	}
}

// TestDigestManifestRepoRecipe: the report recipe need not be the catalog's.
// A repository's own recipe is resolved from the checkout, and its file is
// where the launcher reads that the first step is the digest step — the
// task carries the recipe's name alone, so nothing in the task says whether
// the run is a report run.
func TestDigestManifestRepoRecipe(t *testing.T) {
	f := digestFixture(t)
	mine := []string{manifestRunID(time.Now().Add(-time.Hour))}
	seedManifestRun(t, f, "acme/app", appSlug, mine[0])
	reportRecipeCheckout(t)

	spec := launchWith(t, f, "report", "20261028-120000-abcd")
	if spec.Recipe == nil || spec.Recipe.Name != "report" || spec.Recipe.Source != string(recipe.SourceRepo) {
		t.Fatalf("recipe = %+v, want the repository's own report recipe", spec.Recipe)
	}
	if !slices.Equal(spec.DigestRuns, mine) {
		t.Fatalf("digest_runs = %v, want %v", spec.DigestRuns, mine)
	}
}

// TestDigestRepoRecipeImageGate: the digest release's own gate judges a
// repository's report recipe by the manifest its task carries, not by the
// recipe's text, which the task does not carry at all (a repository recipe is
// a name, read at the run's own ref). Without that the launch is judged by
// recipesSince, passes, and stores a task whose digest_runs a runner older
// than digestSince refuses at bootstrap — a job started, and paid for, over a
// field nobody can read. The gate is the field's, and a report launch always
// carries one: a launch of the same recipe that lists nothing never reaches
// the gate at all, since digestManifest refuses the empty listing before it
// (TestDigestEmptyListingRefused). The recipe's own arm of recipeSinceFor
// still judges a task that carries the recipe's text and no manifest, which no
// launch of this release writes.
//
// Mutation (drop the `len(spec.DigestRuns) > 0` arm of recipeSinceFor): this
// test launches, and fails on both the missing refusal and the stored task.
func TestDigestRepoRecipeImageGate(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", digestOldImage)
	reportRecipeCheckout(t)
	seedManifestRun(t, f, "acme/app", appSlug, manifestRunID(time.Now().Add(-time.Hour)))
	// Minted from the wall clock the manifest's window reads, not fixed: the
	// launch's own ID is read as a mint time like any other run's.
	runID := manifestRunID(time.Now())

	_, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", "report", "--run-id", runID, "Periodic run review")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro 0.13.0 or later") {
		t.Fatalf("err = %v, want the digest release's own refusal", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions started, want none", n)
	}
	if _, err := runstore.Open(statsBucket(t, f), appSlug, runID).ReadTask(context.Background()); err == nil {
		t.Fatal("the refused launch stored a task")
	}
}

// TestDigestManifestOnlyForReportRuns: every other launch stores no manifest,
// whatever records the repository holds. The decoder refuses digest_runs
// under a recipe that is not a report recipe (task.Validate), so a launcher
// that wrote one would refuse its own task at launch.
func TestDigestManifestOnlyForReportRuns(t *testing.T) {
	f := digestFixture(t)
	// The launch below is the catalog default, which carries the adversarial
	// step since 0.15.0, so the digest fixture's 0.13.0 image is too old for
	// it: the image gate is not what this test is about, and it names its own
	// release when it refuses.
	writeBuildRecord(t, f, appSlug, "web", release0150)
	now := time.Now()
	seedManifestRun(t, f, "acme/app", appSlug, manifestRunID(now.Add(-time.Hour)), manifestRunID(now.Add(-2*time.Hour)))

	spec := launchWith(t, f, "default", "20261028-120000-abcd")
	if spec.DigestRuns != nil {
		t.Fatalf("digest_runs = %v, want none", spec.DigestRuns)
	}
}

// TestDigestEmptyListingRefused: a repository with no runs in the window has
// nothing to report, and the launch is refused with a plain message instead of
// starting a job whose runner refuses the empty manifest it was handed -- a
// refusal that costs a job started and paid for, and whose own message tells
// the person to launch with --recipe run-digest, which is what they did. The
// runner's refusal stays as the backstop for a task written by hand
// (internal/runner's TestDigestStepRefusesNoManifest).
//
// Mutation (drop the empty-listing refusal): both launches start a job, and
// this fails on the execution count and on the stored task.
func TestDigestEmptyListingRefused(t *testing.T) {
	f := digestFixture(t)
	refuse := func(t *testing.T, name, id string) {
		t.Helper()
		_, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", name, "--run-id", id, "Periodic run review")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no runs in the period, nothing to report") {
			t.Fatalf("%s: err = %v, want the empty listing's refusal", name, err)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%s: %d executions started, want none", name, n)
		}
		if _, err := runstore.Open(statsBucket(t, f), appSlug, id).ReadTask(context.Background()); err == nil {
			t.Fatalf("%s: the refused launch stored a task", name)
		}
	}
	refuse(t, "run-digest", "20261028-120000-abcd")
	// A repository's own report recipe is judged the same way: the manifest
	// is the recipe's, not the catalog's.
	reportRecipeCheckout(t)
	refuse(t, "report", "20261028-120000-abcd")
	// The same repository launches as soon as it has one run in the window.
	seedManifestRun(t, f, "acme/app", appSlug, manifestRunID(time.Now().Add(-time.Hour)))
	if spec := launchWith(t, f, "report", "20261028-130000-abcd"); len(spec.DigestRuns) != 1 {
		t.Fatalf("digest_runs = %v, want the one run seeded", spec.DigestRuns)
	}
}
