package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
		err := checkRecipeImage(context.Background(), env, appSlug, spec, "web-node", &warn)
		return warn.String(), err
	}
	for _, tc := range []struct{ baseRef, wantErr, wantWarn string }{
		{"-", "has no build record", ""},
		{"", "does not say which base image", ""},
		{"ghcr.io/dimipaun/fugaro-web-node:0.4.1", "built from base image ghcr.io/dimipaun/fugaro-web-node:0.4.1, release 0.4.1", ""},
		{"ghcr.io/dimipaun/fugaro-web-node:0.5.0", "", ""},
		{"ghcr.io/dimipaun/fugaro-web-node:0.10.0", "", ""},
		{"ghcr.io/dimipaun/fugaro-web-node:0.4.10", "release 0.4.10", ""},
		{"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:0.6.2@sha256:" + strings.Repeat("a", 64), "", ""},
		{"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-1a2b3c", "", "not a release base image"},
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
				"Run fugaro image refresh --repo acme/app --workflow web in its checkout, in your own terminal window, or with --yes from CI or a script (never in a coding agent's session", "this release's web-node base image", "--recipe default"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%q: %v lacks %q", tc.baseRef, err, want)
				}
			}
		}
	}
	// No recipe in the task, but the checkout sets agent.recipe.
	spec.Recipe = nil
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-web-node:0.4.1")
	err := checkRecipeImage(context.Background(), env, appSlug, spec, "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "fugaro.yaml's agent.recipe needs a job image") || strings.Contains(err.Error(), "--recipe default") ||
		!strings.Contains(err.Error(), "it copies this release's base image, points") {
		t.Fatalf("agent.recipe: %v", err)
	}
}

// TestCheckCloneImageSince (review finding 1, HIGH): checkCloneImageSince
// judges base_images.base's own release directly, since a checkout: clone
// workflow's job has no derived image and so no build record to judge. One
// row per threshold it backs (recipesSince, layeredSince, cloneSince, each
// refusing an image that predates it), plus a dev/custom ref (warned about,
// not refused: its release is unknown) and a current release (passes
// clean).
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
		{"below cloneSince", cloneSince, "ghcr.io/dimipaun/fugaro-base:0.6.0", "is ghcr.io/dimipaun/fugaro-base:0.6.0, release 0.6.0", ""},
		{"a dev ref warns, not refused", cloneSince, "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c", "", "not a release base image"},
		{"a current release passes", cloneSince, "ghcr.io/dimipaun/fugaro-base:0.7.0", "", ""},
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

func TestNeedsRecipeImage(t *testing.T) {
	s := &task.Spec{}
	if needsRecipeImage(s, "") || !needsRecipeImage(s, "claude-solo") || !needsRecipeImage(&task.Spec{Recipe: &task.Recipe{Name: "x", Source: "repo"}}, "") {
		t.Fatal("needsRecipeImage")
	}
}
