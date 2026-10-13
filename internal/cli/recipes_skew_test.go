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
		!strings.Contains(err.Error(), "it copies this release's base image, points") {
		t.Fatalf("agent.recipe: %v", err)
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
