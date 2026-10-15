package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

func TestCheckRecipeImage(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "mine", Source: "repo"}}
	check := func(baseRef string) (string, error) {
		var warn bytes.Buffer
		if baseRef != "-" {
			writeBuildRecord(t, f, appSlug, "web", baseRef)
		}
		err := checkRecipeImage(context.Background(), env, appSlug, spec, "base", &warn)
		return warn.String(), err
	}
	for _, tc := range []struct{ baseRef, wantErr, wantWarn string }{
		{"-", "has no build record", ""},
		{"", "does not say which base image", ""},
		{"ghcr.io/dimipaun/fugaro-base:0.4.1", "built from base image ghcr.io/dimipaun/fugaro-base:0.4.1, release 0.4.1", ""},
		{"ghcr.io/dimipaun/fugaro-base:0.5.0", "", ""},
		{"ghcr.io/dimipaun/fugaro-base:0.10.0", "", ""},
		{"ghcr.io/dimipaun/fugaro-base:0.4.10", "release 0.4.10", ""},
		{"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.6.2@sha256:" + strings.Repeat("a", 64), "", ""},
		{"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", "", "not a release base image"},
	} {
		warn, err := check(tc.baseRef)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%q: %v", tc.baseRef, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr) || ExitCode(err) != ExitUserError):
			t.Errorf("%q: err = %v, want %q", tc.baseRef, err, tc.wantErr)
		case !strings.Contains(warn, tc.wantWarn):
			t.Errorf("%q: warning %q, want %q", tc.baseRef, warn, tc.wantWarn)
		}
		if tc.wantErr != "" && err != nil {
			for _, want := range []string{"recipe mine needs a job image whose runner knows recipes (fugaro 0.5.0 or later)",
				"Run fugaro image refresh --repo acme/app --workflow web in its checkout, in your own terminal window, or with --yes from CI or a script (never in a coding agent's session", "this release's base base image", "--recipe default"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%q: %v lacks %q", tc.baseRef, err, want)
				}
			}
		}
	}
	// No recipe in the task, but the checkout sets agent.recipe.
	spec.Recipe = nil
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.4.1")
	err := checkRecipeImage(context.Background(), env, appSlug, spec, "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "fugaro.yaml's agent.recipe needs a job image") || strings.Contains(err.Error(), "--recipe default") ||
		!strings.Contains(err.Error(), "it verifies this release's base image and pins it by digest through the installation's remote repository, points") {
		t.Fatalf("agent.recipe: %v", err)
	}
}

// TestCheckCloneImageSince (review finding 1, HIGH): checkCloneImageSince
// judges base_images.base's own release directly, since a checkout: clone
// workflow's job has no derived image and so no build record to judge. One
// row per threshold it backs (recipesSince, layeredSince, each refusing an
// image that predates it), plus a dev/custom ref under one of those other
// thresholds (warned about, not refused: its release is unknown — this
// behaviour is unchanged by the 0.7.0 safety fix). cloneSince itself gets
// its own wording, pinned separately in TestCheckCloneImageSinceRefusedUntilCloneSince,
// since the gate always refuses today (no release ships plan Task 17 yet,
// and that includes a dev/custom ref too): folding it into this generic
// loop would require asserting two different message shapes here.
func TestCheckCloneImageSince(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	check := func(since, baseImage string) (string, error) {
		var warn bytes.Buffer
		lc := *env.lc
		lc.BaseImages = map[string]string{}
		if baseImage != "-" {
			lc.BaseImages["base"] = baseImage
		}
		err := checkCloneImageSince(&lc, spec, since, "the project layer", "the project layer", "", &warn)
		return warn.String(), err
	}
	for _, tc := range []struct{ name, since, baseImage, wantErr, wantWarn string }{
		{"no base image at all", layeredSince, "-", "is not set (the local config has no base_images.base)", ""},
		{"below recipesSince", recipesSince, "ghcr.io/dimipaun/fugaro-base:0.4.0", "is ghcr.io/dimipaun/fugaro-base:0.4.0, release 0.4.0", ""},
		{"below layeredSince", layeredSince, "ghcr.io/dimipaun/fugaro-base:0.5.0", "is ghcr.io/dimipaun/fugaro-base:0.5.0, release 0.5.0", ""},
		{"a dev ref warns, not refused (not cloneSince)", layeredSince, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", "", "not a release base image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warn, err := check(tc.since, tc.baseImage)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("%v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr) || ExitCode(err) != ExitUserError):
				t.Errorf("err = %v, want %q", err, tc.wantErr)
			case !strings.Contains(warn, tc.wantWarn):
				t.Errorf("warning %q, want %q", warn, tc.wantWarn)
			}
			if tc.wantErr != "" && err != nil {
				for _, want := range []string{
					"the project layer needs a job image whose runner knows the project layer (fugaro " + tc.since + " or later)",
					"checkout: clone job image of acme/app workflow web", "fugaro init --base base",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("%v lacks %q", err, want)
					}
				}
			}
		})
	}
}

// TestCheckCloneImageSinceRefusedUntilCloneSince (0.7.0 safety fix, review
// finding: tagging 0.7.0 with cloneSince = "0.7.0" would let a checkout:
// clone workflow pass this gate and then fail every run at bootstrap,
// since plan Task 17's runner side — the only thing that reads
// FUGARO_CHECKOUT — has not shipped). A checkout: clone launch must be
// refused on any image's release, including today's current release
// (0.7.0), with guidance that says plainly the runner side is not released
// yet and names checkout: baked as the way to run the workflow today; only
// an image built from cloneSince itself (0.7.1, the release that will ship
// Task 17) passes.
//
// A non-release ref (dev, hand-pushed, --image-source) gets the same
// refusal, not just a warning (0.7.0 safety fix round 2, review finding:
// while cloneShipped is false, no build of any kind — release or not —
// reads FUGARO_CHECKOUT, so a dev ref cannot make the launch work either;
// warning and letting it through used to contradict that and fail the run
// at bootstrap just the same).
func TestCheckCloneImageSinceRefusedUntilCloneSince(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	lc := *env.lc
	lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.7.0"}
	var warn bytes.Buffer
	err := checkCloneImageSince(&lc, spec, cloneSince, "this checkout: clone workflow", "how to provision a blobless clone (FUGARO_CHECKOUT)", "", &warn)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("0.7.0 (today's current release): err = %v, want refused", err)
	}
	for _, want := range []string{
		"this checkout: clone workflow needs a job image whose runner knows how to provision a blobless clone (FUGARO_CHECKOUT) (fugaro 0.7.1 or later)",
		"checkout: clone's own runner side is not released yet",
		"generic-tool plan Task 17",
		"acme/app workflow web",
		"ghcr.io/dimipaun/fugaro-base:0.7.0, release 0.7.0",
		"checkout: baked",
		"today",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	// A dev ref: still refused, not merely warned about, while cloneShipped
	// is false.
	lc.BaseImages["base"] = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c"
	warn.Reset()
	err = checkCloneImageSince(&lc, spec, cloneSince, "this checkout: clone workflow", "how to provision a blobless clone (FUGARO_CHECKOUT)", "", &warn)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("dev ref: err = %v, want refused", err)
	}
	if warn.Len() != 0 {
		t.Errorf("dev ref: warn = %q, want none (refused, not warned)", warn.String())
	}
	for _, want := range []string{
		"checkout: clone's own runner side is not released yet",
		"generic-tool plan Task 17",
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c",
		"checkout: baked",
		"today",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("dev ref: %v lacks %q", err, want)
		}
	}
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.7.1"
	warn.Reset()
	if err := checkCloneImageSince(&lc, spec, cloneSince, "this checkout: clone workflow", "how to provision a blobless clone (FUGARO_CHECKOUT)", "", &warn); err != nil {
		t.Fatalf("0.7.1 (ships Task 17): %v", err)
	}
}

// TestCheckCloneRecipeImage mirrors TestCheckRecipeImage for a checkout:
// clone workflow: the same subject/alt wording (recipe name, or
// fugaro.yaml's agent.recipe, with --recipe default named unless the
// carried recipe already is one), judged against base_images.base instead
// of a build record.
func TestCheckCloneRecipeImage(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "mine", Source: "repo"}}
	lc := *env.lc
	lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.4.1"}
	var warn bytes.Buffer
	err := checkCloneRecipeImage(&lc, spec, &warn)
	if err == nil {
		t.Fatal("old base image: no error")
	}
	for _, want := range []string{
		"recipe mine needs a job image whose runner knows recipes (fugaro 0.5.0 or later)",
		"is ghcr.io/dimipaun/fugaro-base:0.4.1, release 0.4.1", "--recipe default", "fugaro init --base base",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	// No recipe in the task, but the checkout sets agent.recipe: no "or
	// launch with --recipe default" (a carried default is not a way out).
	spec.Recipe = nil
	err = checkCloneRecipeImage(&lc, spec, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "fugaro.yaml's agent.recipe needs a job image") || strings.Contains(err.Error(), "--recipe default") {
		t.Fatalf("agent.recipe: %v", err)
	}
	// A current release passes.
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.5.0"
	spec.Recipe = &task.Recipe{Name: "mine", Source: "repo"}
	if err := checkCloneRecipeImage(&lc, spec, &bytes.Buffer{}); err != nil {
		t.Fatalf("current release: %v", err)
	}
}

// TestRecipeV07NeedsA07Image: a recipe using a 0.7.0 construct (here the
// catalog's standard, with bounce) is refused on a build record whose
// base_ref predates 0.7.0 even though it is at or after recipesSince
// (0.5.0): the same shape as TestCheckRecipeImage's 0.5.0 gate, one version
// higher. A recipe with no 0.7.0 construct (default) is not held to it.
func TestRecipeV07NeedsA07Image(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	text, ok := recipe.CatalogText("standard")
	if !ok {
		t.Fatal("no catalog recipe standard")
	}
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "standard", Source: "catalog", SHA256: recipe.Sum(text), YAML: string(text)}}
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.6.2")
	err := checkRecipeImage(context.Background(), env, appSlug, spec, "base", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "recipe standard needs a job image whose runner knows recipes (fugaro 0.7.0 or later)") ||
		!strings.Contains(err.Error(), "release 0.6.2") || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.7.0")
	if err := checkRecipeImage(context.Background(), env, appSlug, spec, "base", &bytes.Buffer{}); err != nil {
		t.Fatalf("0.7.0 base: %v", err)
	}
	// default carries no 0.7.0 construct, so 0.5.0 still suffices for it.
	defText, _ := recipe.CatalogText("default")
	defSpec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "default", Source: "catalog", SHA256: recipe.Sum(defText), YAML: string(defText)}}
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.5.0")
	if err := checkRecipeImage(context.Background(), env, appSlug, defSpec, "base", &bytes.Buffer{}); err != nil {
		t.Fatalf("default on 0.5.0 base: %v", err)
	}
}

func TestNeedsRecipeImage(t *testing.T) {
	s := &task.Spec{}
	if needsRecipeImage(s, "") || !needsRecipeImage(s, "claude-solo") || !needsRecipeImage(&task.Spec{Recipe: &task.Recipe{Name: "x", Source: "repo"}}, "") {
		t.Fatal("needsRecipeImage")
	}
}

// TestCloneRecipeV07NeedsA07Image is TestRecipeV07NeedsA07Image for a
// checkout: clone workflow: an embedded recipe using a 0.7.0 construct is
// judged against recipesV07Since on base_images.base itself, and one with
// none (default) only against recipesSince.
func TestCloneRecipeV07NeedsA07Image(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	text, ok := recipe.CatalogText("standard")
	if !ok {
		t.Fatal("no catalog recipe standard")
	}
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "standard", Source: "catalog", SHA256: recipe.Sum(text), YAML: string(text)}}
	lc := *env.lc
	lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.6.2"}
	err := checkCloneRecipeImage(&lc, spec, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "recipe standard needs a job image whose runner knows recipes (fugaro 0.7.0 or later)") ||
		!strings.Contains(err.Error(), "release 0.6.2") || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.7.0"
	if err := checkCloneRecipeImage(&lc, spec, &bytes.Buffer{}); err != nil {
		t.Fatalf("0.7.0 base: %v", err)
	}
	defText, _ := recipe.CatalogText("default")
	defSpec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "default", Source: "catalog", SHA256: recipe.Sum(defText), YAML: string(defText)}}
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.5.0"
	if err := checkCloneRecipeImage(&lc, defSpec, &bytes.Buffer{}); err != nil {
		t.Fatalf("default on 0.5.0 base: %v", err)
	}
}
