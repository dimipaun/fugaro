package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// A manifest builder is the seam A5.2 registers under a catalog recipe's name:
// the objects a mode: evaluate launch leaves under its run's prefix before the
// run's task exists, for the recipe's own registered step to read back. These
// tests hold the core half of it — when the builder is asked, what the launch
// does with its answer, and what a registration refuses — on the `solo` recipe,
// whose launch is an ordinary one: nothing about the seam is specific to the
// judge's recipe, which is what makes it the core's plumbing (D550).

// installManifest installs b as the build's manifest builder for a catalog
// recipe, for one test.
func installManifest(t *testing.T, name string, b RecipeManifest) {
	t.Helper()
	extendState(t)
	if err := RegisterRecipeManifest(name, b); err != nil {
		t.Fatalf("registering the manifest builder for %s: %v", name, err)
	}
}

// manifestOf is what a run's prefix holds at ab/manifest.json, "" when the
// launch wrote none.
func manifestOf(t *testing.T, f *cloudFixture, run string) string {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	data, err := runstore.Open(b, appSlug, run).ReadFile(context.Background(), runstore.ManifestName)
	if err != nil {
		return ""
	}
	return string(data)
}

// taskBytes is a run's task.json as the bucket holds it, "" when there is
// none: a refused launch is held to having written nothing at all.
func taskBytes(t *testing.T, f *cloudFixture, run string) string {
	t.Helper()
	b, err := blob.OpenBucket(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	data, err := runstore.Open(b, appSlug, run).ReadFile(context.Background(), "task.json")
	if err != nil {
		return ""
	}
	return string(data)
}

// TestManifestBuilderRegistered: the builder is asked once, before the first
// run's task exists, with the launch's own resolved facts, and what it answers
// is under the run's own prefix at ab/manifest.json — the one leaf the recipe's
// step reads back, spelled once in runstore. The registrations that are not a
// builder — a nil, a name no catalog recipe holds, a second builder on one
// recipe — are refused, so a misspelled name is loud rather than a manifest
// nobody writes.
func TestManifestBuilderRegistered(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	var (
		calls int
		atRun PlannedRun
		there bool
	)
	installManifest(t, "solo", func(ctx context.Context, run PlannedRun, warn io.Writer) ([]byte, error) {
		calls++
		atRun = run
		b, err := blob.OpenBucket(ctx, f.bucket)
		if err != nil {
			t.Errorf("opening the runs bucket: %v", err)
			return nil, nil
		}
		defer b.Close()
		if _, err := runstore.Open(b, appSlug, budgetRun).ReadTask(ctx); err == nil {
			there = true
		}
		return []byte(`{"version":1,"pairs":[]}`), nil
	})
	if _, _, err := runBudget(t, "--recipe", "solo"); err != nil {
		t.Fatalf("the launch failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("%d builder calls, want 1", calls)
	}
	if there {
		t.Error("the builder was asked after the task was written")
	}
	if atRun.Cloud == nil || atRun.Cloud.env == nil {
		t.Error("the builder was handed no cloud handle")
	}
	if atRun.Slug != appSlug || atRun.Repo != "acme/app" {
		t.Errorf("planned %q of %q, want %q of acme/app", atRun.Slug, atRun.Repo, appSlug)
	}
	if atRun.Spec == nil || atRun.Spec.RunID != budgetRun {
		t.Errorf("planned task = %+v, want the launch's own run", atRun.Spec)
	}
	if atRun.Config == nil {
		t.Error("the builder was handed no configuration")
	}
	if got, want := manifestOf(t, f.cloudFixture, budgetRun), `{"version":1,"pairs":[]}`; got != want {
		t.Errorf("the manifest = %q, want %q", got, want)
	}
	if spec := readSpec(t, f.cloudFixture, budgetRun); spec.Recipe.Name != "solo" {
		t.Errorf("the stored task's recipe = %q, want solo", spec.Recipe.Name)
	}
}

// TestNoManifestBuilderLaunchUnchanged: the core default is no builder, so a
// launch writes no manifest and asks nothing — D550's "the plumbing is inert
// in a build that registers none" — and a builder registered for one recipe is
// not the answer for another, which is why it is keyed by the recipe's name:
// a judge recipe's builder must not be asked about a recipe with no judge step
// in it.
func TestNoManifestBuilderLaunchUnchanged(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	installManifest(t, "solo", func(context.Context, PlannedRun, io.Writer) ([]byte, error) {
		t.Error("a builder registered for solo was asked about another recipe")
		return nil, nil
	})
	if _, _, err := runBudget(t, "--recipe", "premium"); err != nil {
		t.Fatalf("the launch failed: %v", err)
	}
	if got := manifestOf(t, f.cloudFixture, budgetRun); got != "" {
		t.Errorf("a launch wrote a manifest with no builder for its recipe: %q", got)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// TestManifestBuilderErrorRefusesLaunch: a builder that refuses is the
// launch's refusal — the launch stores nothing and starts nothing, so a judge
// run never starts over a manifest its own step would not find (the rule
// --attach's own refusals follow, design §4 step 1 of the run).
func TestManifestBuilderErrorRefusesLaunch(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	installManifest(t, "solo", func(context.Context, PlannedRun, io.Writer) ([]byte, error) {
		return nil, userErr("no pair is complete and unjudged")
	})
	_, _, err := runBudget(t, "--recipe", "solo")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no pair is complete and unjudged") {
		t.Fatalf("err = %v, want the builder's own refusal", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none: a refused launch starts nothing", n)
	}
	if got := manifestOf(t, f.cloudFixture, budgetRun); got != "" {
		t.Errorf("a refused launch left a manifest: %q", got)
	}
	if got := taskBytes(t, f.cloudFixture, budgetRun); got != "" {
		t.Errorf("a refused launch left a task: %s", got)
	}
}

// TestRegisterRecipeManifestRefusals: the three names a registration may not
// take. A name no catalog recipe holds would be a builder nothing ever asks —
// the launch looks the builder up by the recipe it resolved — so a typo in
// ee/register's init would be silent; nil is not a builder that writes
// nothing; and a second builder on one recipe is refused rather than quietly
// replacing what a tagged build installed (§5.5).
func TestRegisterRecipeManifestRefusals(t *testing.T) {
	extendState(t)
	ok := func(context.Context, PlannedRun, io.Writer) ([]byte, error) { return nil, nil }
	if err := RegisterRecipeManifest("no-such-recipe", ok); err == nil {
		t.Error("a builder was registered under a name no catalog recipe holds")
	}
	if err := RegisterRecipeManifest("solo", nil); err == nil {
		t.Error("a nil builder was registered")
	}
	if err := RegisterRecipeManifest("solo", ok); err != nil {
		t.Fatalf("registering the builder: %v", err)
	}
	if err := RegisterRecipeManifest("solo", ok); err == nil {
		t.Error("a second builder was registered for one recipe")
	}
}

// TestManifestBuilderErrorIsNotASkip: a builder that fails is not the
// planner's soft failure. The planner degrades to the champion alone (D554),
// while a manifest that could not be built leaves the run with nothing to read
// — so the launch is refused, loudly, rather than started over an empty bucket.
func TestManifestBuilderErrorIsNotASkip(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	installManifest(t, "solo", func(context.Context, PlannedRun, io.Writer) ([]byte, error) {
		return nil, errors.New("the runs bucket could not be listed")
	})
	_, _, err := runBudget(t, "--recipe", "solo")
	if err == nil {
		t.Fatal("a builder that failed launched the run")
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("%d executions, want none", n)
	}
}
