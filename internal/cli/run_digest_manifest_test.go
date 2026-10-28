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
// needs: the digest step is 0.13.0's (digestSince), so an older build
// record is refused before the launcher reads a manifest at all.
const digestLaunchImage = "ghcr.io/dimipaun/fugaro-base:0.13.0"

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

// finishManifestRun gives the run a record in a terminal status.
func finishManifestRun(t *testing.T, f *cloudFixture, slug, id string) {
	t.Helper()
	at := time.Now()
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Status: runstore.StatusSucceeded,
		StartedAt: at.Add(-time.Minute), FinishedAt: &at}
	if err := runstore.Open(statsBucket(t, f), slug, id).WriteRecord(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
}

// launchWith launches a run of acme/app through the CLI with the recipe name
// given, and returns the task the launcher stored for it.
func launchWith(t *testing.T, f *cloudFixture, name, runID string) *task.Spec {
	t.Helper()
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--recipe", name, "--run-id", runID, "Periodic run review"); err != nil {
		t.Fatal(err)
	}
	spec, err := runstore.Open(statsBucket(t, f), appSlug, runID).ReadTask(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// TestDigestManifestThisRepositoryOnly is the headline: the manifest a
// report run carries names the runs of this repository and no other. The
// digest step reads the records it is named and never lists -- a bucket
// listing is a call the job's own grant cannot hold -- so this list alone
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
// last 40 days -- the month the reports cover plus the seven days the digest
// step catches up -- newest first and at most task.MaxDigestRuns of them.
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
		name string
		cap  int
		want []string
	}{
		{"the 40 day window", 0, recent},
		{"the cap", 3, recent[:3]},
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
			spec := launchWith(t, f, "run-digest", "20261028-120000-abcd")
			if !slices.Equal(spec.DigestRuns, tc.want) {
				t.Fatalf("digest_runs = %v, want %v", spec.DigestRuns, tc.want)
			}
		})
	}
}

// TestDigestLaunchRefusedWhileOneRuns: a repository has one report run at a
// time, so a launch while one of its report runs is still going is refused
// before any cloud work, naming the run that holds it. A report run that has
// finished, and an ordinary run that is still going, do not hold it: the
// first reads no records any more, the second never reads any.
func TestDigestLaunchRefusedWhileOneRuns(t *testing.T) {
	f := digestFixture(t)
	now := time.Now()
	running := manifestRunID(now.Add(-30 * time.Minute))
	seedManifestRun(t, f, "acme/app", appSlug, running, manifestRunID(now.Add(-2*time.Hour)))
	seedManifestRun(t, f, "acme/app", appSlug, manifestRunID(now.Add(-45*time.Minute)))

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

	finishManifestRun(t, f, appSlug, running)
	spec := launchWith(t, f, "run-digest", "20261028-130000-abcd")
	// The run that finished is still a record of this repository, so the
	// manifest of the launch that follows it names it.
	if !slices.Contains(spec.DigestRuns, running) {
		t.Fatalf("digest_runs = %v, want it to name %s", spec.DigestRuns, running)
	}
}

// TestDigestManifestRepoRecipe: the report recipe need not be the catalog's.
// A repository's own recipe is resolved from the checkout, and its file is
// where the launcher reads that the first step is the digest step -- the
// task carries the recipe's name alone, so nothing in the task says whether
// the run is a report run.
func TestDigestManifestRepoRecipe(t *testing.T) {
	f := digestFixture(t)
	mine := []string{manifestRunID(time.Now().Add(-time.Hour))}
	seedManifestRun(t, f, "acme/app", appSlug, mine[0])
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	recipes := filepath.Join(dir, ".fugaro", "recipes")
	if err := os.MkdirAll(recipes, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "version: 1\nname: report\ndescription: This repository's own run report recipe.\n" +
		"use_when: A scheduled run review only, launched by the repository's own routine.\n" +
		"steps:\n  - digest: {}\n  - review:\n      max_rounds: 1\n"
	if err := os.WriteFile(filepath.Join(recipes, "report.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	spec := launchWith(t, f, "report", "20261028-120000-abcd")
	if spec.Recipe == nil || spec.Recipe.Name != "report" || spec.Recipe.Source != string(recipe.SourceRepo) {
		t.Fatalf("recipe = %+v, want the repository's own report recipe", spec.Recipe)
	}
	if !slices.Equal(spec.DigestRuns, mine) {
		t.Fatalf("digest_runs = %v, want %v", spec.DigestRuns, mine)
	}
}

// TestDigestManifestOnlyForReportRuns: every other launch stores no manifest,
// whatever records the repository holds. The decoder refuses digest_runs
// under a recipe that is not a report recipe (task.Validate), so a launcher
// that wrote one would refuse its own task at launch.
func TestDigestManifestOnlyForReportRuns(t *testing.T) {
	f := digestFixture(t)
	now := time.Now()
	seedManifestRun(t, f, "acme/app", appSlug, manifestRunID(now.Add(-time.Hour)), manifestRunID(now.Add(-2*time.Hour)))

	spec := launchWith(t, f, "default", "20261028-120000-abcd")
	if spec.DigestRuns != nil {
		t.Fatalf("digest_runs = %v, want none", spec.DigestRuns)
	}
}
