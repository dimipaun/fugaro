package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runner"
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

// TestCheckRecipeImageDigestSince: a recipe holding the digest step needs a
// job image whose runner executes it, which is a release of its own
// (digestSince) and not the 0.7.0 one the recipe's use_when would otherwise
// choose. A release image older than it is refused with the ordinary
// "upgrade your base image" refusal; a base image that is not a release
// cannot be dated, so it only warns, and the runner refuses the recipe
// itself there (right after resolveRecipe, before any stage).
func TestCheckRecipeImageDigestSince(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	text, ok := recipe.CatalogText("run-digest")
	if !ok {
		t.Fatal("no catalog recipe run-digest")
	}
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "run-digest", Source: string(recipe.SourceCatalog), YAML: string(text)}}
	check := func(baseRef string) (string, error) {
		var warn bytes.Buffer
		writeBuildRecord(t, f, appSlug, "web", baseRef)
		err := checkRecipeImage(context.Background(), env, appSlug, spec, "base", &warn)
		return warn.String(), err
	}
	// The newest release tagged when the step was added: it knows every
	// 0.7.0 construct the recipe uses and still has no runner that executes
	// the digest step.
	warn, err := check("ghcr.io/dimipaun/fugaro-base:0.12.0")
	if err == nil || !strings.Contains(err.Error(), "recipe run-digest needs a job image whose runner knows recipes (fugaro 0.13.0 or later)") || ExitCode(err) != ExitUserError {
		t.Fatalf("0.12.0: warn = %q, err = %v", warn, err)
	}
	if warn != "" {
		t.Errorf("0.12.0: warning %q, want none", warn)
	}
	warn, err = check("us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c")
	if err != nil {
		t.Fatalf("dev base: %v", err)
	}
	if !strings.Contains(warn, "not a release base image") {
		t.Errorf("dev base: warning %q, want the not-a-release note", warn)
	}
	// A repository-sourced recipe carries its name alone, so its text is not
	// in the task to judge: what says the run executes the digest step is the
	// manifest the task carries, and it is judged at the same constant. The
	// gate is the field's, so the same recipe with no manifest is judged by
	// recipesSince instead (a repository with no runs to read writes none),
	// and the older runner refuses the recipe at bootstrap there: it knows the
	// step kind and does not execute it.
	spec.Recipe = &task.Recipe{Name: "report", Source: string(recipe.SourceRepo)}
	spec.DigestRuns = []string{"20261026-090000-1234"}
	warn, err = check("ghcr.io/dimipaun/fugaro-base:0.12.0")
	if err == nil || !strings.Contains(err.Error(), "recipe report needs a job image whose runner knows recipes (fugaro 0.13.0 or later)") || ExitCode(err) != ExitUserError {
		t.Fatalf("repo recipe with a manifest: warn = %q, err = %v", warn, err)
	}
	spec.DigestRuns = nil
	if _, err := check("ghcr.io/dimipaun/fugaro-base:0.12.0"); err != nil {
		t.Fatalf("repo recipe with no manifest: %v", err)
	}
}

// TestCheckCloneRecipeImageDigestSince is TestCheckRecipeImageDigestSince for
// a checkout: clone workflow, where there is no derived image and so no build
// record: the same digest step raises checkCloneImageSince's threshold on
// base_images.base itself, so a release image older than digestSince is
// refused, while a base image that is not a release only warns (a runner
// old enough to lack the step refuses the recipe there).
func TestCheckCloneRecipeImageDigestSince(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	text, ok := recipe.CatalogText("run-digest")
	if !ok {
		t.Fatal("no catalog recipe run-digest")
	}
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "run-digest", Source: string(recipe.SourceCatalog), YAML: string(text)}}
	lc := *env.lc
	lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.12.0"}
	var warn bytes.Buffer
	err := checkCloneRecipeImage(context.Background(), &lc, spec, &warn)
	if err == nil || !strings.Contains(err.Error(), "recipe run-digest needs a job image whose runner knows recipes (fugaro 0.13.0 or later)") ||
		!strings.Contains(err.Error(), "release 0.12.0") || ExitCode(err) != ExitUserError {
		t.Fatalf("0.12.0: warn = %q, err = %v", warn.String(), err)
	}
	if warn.Len() != 0 {
		t.Errorf("0.12.0: warning %q, want none", warn.String())
	}
	lc.BaseImages["base"] = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c"
	warn.Reset()
	if err := checkCloneRecipeImage(context.Background(), &lc, spec, &warn); err != nil {
		t.Fatalf("dev base: %v", err)
	}
	if !strings.Contains(warn.String(), "not a release base image") {
		t.Errorf("dev base: warning %q, want the not-a-release note", warn.String())
	}
	// The same arm as TestCheckRecipeImageDigestSince's: a repository recipe
	// whose task carries the manifest is judged at digestSince by the
	// manifest, and by recipesSince when it carries none.
	spec.Recipe = &task.Recipe{Name: "report", Source: string(recipe.SourceRepo)}
	spec.DigestRuns = []string{"20261026-090000-1234"}
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.12.0"
	warn.Reset()
	if err := checkCloneRecipeImage(context.Background(), &lc, spec, &warn); err == nil || !strings.Contains(err.Error(), "fugaro 0.13.0 or later") {
		t.Fatalf("repo recipe with a manifest: warn = %q, err = %v", warn.String(), err)
	}
	spec.DigestRuns = nil
	if err := checkCloneRecipeImage(context.Background(), &lc, spec, &warn); err != nil {
		t.Fatalf("repo recipe with no manifest: %v", err)
	}
}

// TestRecipesSkewRefusesAdversarialOnOldImage: a recipe holding the
// adversarial step (review level 3) needs a job image whose runner acts on
// it, which is a release of its own (adversarialSince) and newer than every
// other recipe construct — 0.13.0 knows the digest step and still has no
// adversarial kind in its version 1 step list, so it refuses the whole recipe
// at parse. The gate turns that bootstrap refusal into one at the launcher's
// terminal, for a derived image (checkRecipeImage) and for a checkout: clone
// workflow's base image (checkCloneRecipeImage) alike, since both judge the
// recipe's own text. A base image that is not a release cannot be dated, so
// it only warns, and the runner refuses the recipe there.
func TestRecipesSkewRefusesAdversarialOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	const text = "version: 1\nname: mine\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - adversarial: { max_rounds: 2, bounce: first_line }\n"
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "mine", Source: string(recipe.SourceProject), YAML: text}}
	check := func(baseRef string) (string, error) {
		var warn bytes.Buffer
		writeBuildRecord(t, f, appSlug, "web", baseRef)
		err := checkRecipeImage(context.Background(), env, appSlug, spec, "base", &warn)
		return warn.String(), err
	}
	// The newest release that knows the digest step, and still has no
	// adversarial one: the gate is 0.15.0, not 0.13.0.
	for _, baseRef := range []string{"ghcr.io/dimipaun/fugaro-base:0.13.0", "ghcr.io/dimipaun/fugaro-base:0.14.0"} {
		warn, err := check(baseRef)
		if err == nil || !strings.Contains(err.Error(), "recipe mine needs a job image whose runner knows recipes (fugaro 0.15.0 or later)") || ExitCode(err) != ExitUserError {
			t.Fatalf("%s: warn = %q, err = %v", baseRef, warn, err)
		}
		if warn != "" {
			t.Errorf("%s: warning %q, want none", baseRef, warn)
		}
	}
	warn, err := check("us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c")
	if err != nil {
		t.Fatalf("dev base: %v", err)
	}
	if !strings.Contains(warn, "not a release base image") {
		t.Errorf("dev base: warning %q, want the not-a-release note", warn)
	}
	// The same recipe on a checkout: clone workflow, whose job has no derived
	// image and so no build record: the gate raises checkCloneImageSince's
	// threshold on base_images.base itself.
	lc := *env.lc
	lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.14.0"}
	var cloneWarn bytes.Buffer
	err = checkCloneRecipeImage(context.Background(), &lc, spec, &cloneWarn)
	if err == nil || !strings.Contains(err.Error(), "recipe mine needs a job image whose runner knows recipes (fugaro 0.15.0 or later)") ||
		!strings.Contains(err.Error(), "release 0.14.0") || ExitCode(err) != ExitUserError {
		t.Fatalf("clone 0.14.0: warn = %q, err = %v", cloneWarn.String(), err)
	}
	lc.BaseImages["base"] = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c"
	cloneWarn.Reset()
	if err := checkCloneRecipeImage(context.Background(), &lc, spec, &cloneWarn); err != nil {
		t.Fatalf("clone dev base: %v", err)
	}
	if !strings.Contains(cloneWarn.String(), "not a release base image") {
		t.Errorf("clone dev base: warning %q, want the not-a-release note", cloneWarn.String())
	}
	// A recipe without the step is not raised to adversarialSince: the
	// constructs it does carry decide, as before.
	spec.Recipe.YAML = "version: 1\nname: mine\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n"
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.13.0")
	if err := checkRecipeImage(context.Background(), env, appSlug, spec, "base", &bytes.Buffer{}); err != nil {
		t.Fatalf("no adversarial step: %v", err)
	}
}

// TestRecipesSkewRefusesRepositoryAdversarialOnOldImage: a repository recipe
// carries its name alone in the task — the runner reads the file at the ref —
// so what the launcher judges is its own read of that file, and the
// adversarial step it carries is judged like an embedded one
// (repoRecipeForGate, recipeSinceFor). Judged by the name alone the launch
// would pass at 0.13.0 and the run would die at bootstrap, which is what this
// closes; a repository recipe without the step keeps the release its own
// constructs need.
func TestRecipesSkewRefusesRepositoryAdversarialOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	recipeCheckout(t, f, "", map[string]string{
		".fugaro/recipes/mine.yaml":  "version: 1\nname: mine\nsteps:\n  - first_line: {}\n  - review: {}\n  - adversarial: {}\n",
		".fugaro/recipes/plain.yaml": "version: 1\nname: plain\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n",
	})
	other := mustSlug("github", "acme/other")
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/other", Ref: "main", Workflow: "svc", Task: "x",
		Recipe: &task.Recipe{Name: "mine", Source: string(recipe.SourceRepo)}}
	writeBuildRecord(t, f, other, "svc", "ghcr.io/dimipaun/fugaro-base:0.14.0")
	var warn bytes.Buffer
	err := checkRecipeImage(context.Background(), env, other, spec, "base", &warn)
	if err == nil || !strings.Contains(err.Error(), "recipe mine needs a job image whose runner knows recipes (fugaro 0.15.0 or later)") || ExitCode(err) != ExitUserError {
		t.Fatalf("warn = %q, err = %v", warn.String(), err)
	}
	if warn.Len() > 0 {
		t.Errorf("warning %q, want none on a release base image", warn.String())
	}
	lc := *env.lc
	lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.14.0"}
	var cloneWarn bytes.Buffer
	if err := checkCloneRecipeImage(context.Background(), &lc, spec, &cloneWarn); err == nil || !strings.Contains(err.Error(), "fugaro 0.15.0 or later") {
		t.Fatalf("clone: warn = %q, err = %v", cloneWarn.String(), err)
	}
	// The control: a repository recipe that carries no adversarial step is
	// judged by the constructs it does carry, and 0.13.0 knows those.
	spec.Recipe.Name = "plain"
	writeBuildRecord(t, f, other, "svc", "ghcr.io/dimipaun/fugaro-base:0.13.0")
	if err := checkRecipeImage(context.Background(), env, other, spec, "base", &bytes.Buffer{}); err != nil {
		t.Fatalf("a repository recipe with no adversarial step: %v", err)
	}
	if err := checkCloneRecipeImage(context.Background(), &lc, spec, &bytes.Buffer{}); err != nil {
		t.Fatalf("a repository recipe with no adversarial step, clone: %v", err)
	}
}

// TestAdversarialSinceHasAReleaseBullet: adversarialSince names 0.15.0, which
// is not tagged, so docs/release.md's Before-you-tag list is what holds the
// constant to the release actually cut — the job every other *Since constant's
// bullet does (TestSharedSinceHasAReleaseBullet). Nothing else ties the number
// the gate accepts to the release L4.2 ships in, and a release that landed
// level 3 elsewhere would otherwise keep refusing images that can run it.
func TestAdversarialSinceHasAReleaseBullet(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.HasPrefix(line, "- **Verify `adversarialSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no adversarialSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+adversarialSince+"\"`") {
		t.Errorf("docs/release.md's adversarialSince bullet does not carry the constant's release (%q)", adversarialSince)
	}
	for _, want := range []string{"adversarial", "not tagged yet"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("docs/release.md's adversarialSince bullet does not say %q", want)
		}
	}
}

// TestCheckCloneImageSince (review finding 1, HIGH): checkCloneImageSince
// judges base_images.base's own release directly, since a checkout: clone
// workflow's job has no derived image and so no build record to judge. One
// row per threshold it backs (recipesSince, layeredSince, each refusing an
// image that predates it), plus a dev/custom ref under one of those other
// thresholds (warned about, not refused: its release is unknown). cloneSince
// itself is pinned separately, in TestCheckCloneImageSinceAtCloneSince, with
// the message shape the cloneShipped flip gave it (the ordinary "upgrade
// your base image" refusal); folding it into this generic loop would mean
// asserting two different shapes here.
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
					"checkout: clone job image of acme/app workflow web", "fugaro update --base base",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("%v lacks %q", err, want)
					}
				}
			}
		})
	}
}

// TestCheckCloneImageSinceAtCloneSince: cloneSince's own threshold, with
// cloneShipped true (0.9.0 shipped the flip) — the case the flip changed.
// Before it, no release ran FUGARO_CHECKOUT at all, so this gate refused a
// checkout: clone launch on every image, dev refs included, with wording
// saying the runner side was not released yet. Now it judges cloneSince the
// way every other *Since gate judges its release: an image older than
// 0.7.5 (the release whose runner reads FUGARO_CHECKOUT and makes the
// blobless clone) is refused with the ordinary "upgrade your base image"
// line, a ref with no release tag is warned about and let through, and an
// image at cloneSince or later passes.
//
// Mutation (run, restore): set cloneShipped back to false in
// recipes_skew.go, and the 0.7.0 row and the dev-ref row both fail — the
// refusal comes back with the not-released-yet wording, and the dev ref is
// refused where this test wants a warning. That is what makes this the
// enabled-state pin for the launch gate.
func TestCheckCloneImageSinceAtCloneSince(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	lc := *env.lc
	lc.BaseImages = map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.7.0"}
	var warn bytes.Buffer
	err := checkCloneImageSince(&lc, spec, cloneSince, "this checkout: clone workflow", "how to provision a blobless clone (FUGARO_CHECKOUT)", "", &warn)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("0.7.0 (below cloneSince): err = %v, want refused", err)
	}
	for _, want := range []string{
		"this checkout: clone workflow needs a job image whose runner knows how to provision a blobless clone (FUGARO_CHECKOUT) (fugaro 0.7.5 or later)",
		"acme/app workflow web",
		"is ghcr.io/dimipaun/fugaro-base:0.7.0, release 0.7.0",
		"fugaro update --base base",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	// The disabled state's wording is gone with the flip: an older image is
	// told to move its base image, not that the runner side is unreleased.
	for _, not := range []string{"is not released yet", "generic-tool plan Task 17", "checkout: baked", "today"} {
		if strings.Contains(err.Error(), not) {
			t.Errorf("a shipped gate still says %q: %v", not, err)
		}
	}
	if warn.Len() != 0 {
		t.Errorf("a release below cloneSince warns as well as refusing: %q", warn.String())
	}
	// A dev ref: judged the way every other *Since gate judges one — warned
	// about, not refused. Nothing can tell what its runner knows.
	lc.BaseImages["base"] = "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c"
	warn.Reset()
	if err := checkCloneImageSince(&lc, spec, cloneSince, "this checkout: clone workflow", "how to provision a blobless clone (FUGARO_CHECKOUT)", "", &warn); err != nil {
		t.Fatalf("dev ref: err = %v, want none (warned about, not refused)", err)
	}
	for _, want := range []string{
		"us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c",
		"not a release base image",
	} {
		if !strings.Contains(warn.String(), want) {
			t.Errorf("dev ref: warning %q lacks %q", warn.String(), want)
		}
	}
	// cloneSince itself, and a release past it, both pass.
	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:0.7.5", "ghcr.io/dimipaun/fugaro-base:0.9.0"} {
		lc.BaseImages["base"] = ref
		warn.Reset()
		if err := checkCloneImageSince(&lc, spec, cloneSince, "this checkout: clone workflow", "how to provision a blobless clone (FUGARO_CHECKOUT)", "", &warn); err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if warn.Len() != 0 {
			t.Errorf("%s: warn = %q, want none", ref, warn.String())
		}
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
	err := checkCloneRecipeImage(context.Background(), &lc, spec, &warn)
	if err == nil {
		t.Fatal("old base image: no error")
	}
	for _, want := range []string{
		"recipe mine needs a job image whose runner knows recipes (fugaro 0.5.0 or later)",
		"is ghcr.io/dimipaun/fugaro-base:0.4.1, release 0.4.1", "--recipe default", "fugaro update --base base",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	// No recipe in the task, but the checkout sets agent.recipe: no "or
	// launch with --recipe default" (a carried default is not a way out).
	spec.Recipe = nil
	err = checkCloneRecipeImage(context.Background(), &lc, spec, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "fugaro.yaml's agent.recipe needs a job image") || strings.Contains(err.Error(), "--recipe default") {
		t.Fatalf("agent.recipe: %v", err)
	}
	// A current release passes.
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.5.0"
	spec.Recipe = &task.Recipe{Name: "mine", Source: "repo"}
	if err := checkCloneRecipeImage(context.Background(), &lc, spec, &bytes.Buffer{}); err != nil {
		t.Fatalf("current release: %v", err)
	}
}

// TestRecipeV07NeedsA07Image: a recipe using a 0.7.0 construct (here the
// catalog's standard, with bounce) is refused on a build record whose
// base_ref predates 0.7.0 even though it is at or after recipesSince
// (0.5.0): the same shape as TestCheckRecipeImage's 0.5.0 gate, one version
// higher. The catalog default carries no 0.7.0 construct and is not held to
// 0.7.0 for one -- it is held to 0.15.0 instead, for the adversarial step it
// carries since that release, which is the gate below.
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
	// default carries no 0.7.0 construct, so 0.7.0 is not what holds it: its
	// adversarial step holds it to 0.15.0, the release whose runner acts on
	// that step (adversarialSince).
	defText, _ := recipe.CatalogText("default")
	defSpec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "default", Source: "catalog", SHA256: recipe.Sum(defText), YAML: string(defText)}}
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.5.0")
	err = checkRecipeImage(context.Background(), env, appSlug, defSpec, "base", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "recipe default needs a job image whose runner knows recipes (fugaro 0.15.0 or later)") ||
		!strings.Contains(err.Error(), "release 0.5.0") || ExitCode(err) != ExitUserError {
		t.Fatalf("default on 0.5.0 base: %v", err)
	}
	writeBuildRecord(t, f, appSlug, "web", "ghcr.io/dimipaun/fugaro-base:0.15.0")
	if err := checkRecipeImage(context.Background(), env, appSlug, defSpec, "base", &bytes.Buffer{}); err != nil {
		t.Fatalf("default on 0.15.0 base: %v", err)
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
// judged against recipesV07Since on base_images.base itself, and the catalog
// default, which carries none, against the 0.15.0 its adversarial step needs.
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
	err := checkCloneRecipeImage(context.Background(), &lc, spec, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "recipe standard needs a job image whose runner knows recipes (fugaro 0.7.0 or later)") ||
		!strings.Contains(err.Error(), "release 0.6.2") || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.7.0"
	if err := checkCloneRecipeImage(context.Background(), &lc, spec, &bytes.Buffer{}); err != nil {
		t.Fatalf("0.7.0 base: %v", err)
	}
	defText, _ := recipe.CatalogText("default")
	defSpec := &task.Spec{Version: 1, RunID: "20261007-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		Recipe: &task.Recipe{Name: "default", Source: "catalog", SHA256: recipe.Sum(defText), YAML: string(defText)}}
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.5.0"
	err = checkCloneRecipeImage(context.Background(), &lc, defSpec, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "recipe default needs a job image whose runner knows recipes (fugaro 0.15.0 or later)") ||
		!strings.Contains(err.Error(), "release 0.5.0") || ExitCode(err) != ExitUserError {
		t.Fatalf("default on 0.5.0 base: %v", err)
	}
	lc.BaseImages["base"] = "ghcr.io/dimipaun/fugaro-base:0.15.0"
	if err := checkCloneRecipeImage(context.Background(), &lc, defSpec, &bytes.Buffer{}); err != nil {
		t.Fatalf("default on 0.15.0 base: %v", err)
	}
}

// TestRefuseCatalogSwitches pins plan P4 and Task 4 step 2 for the route
// switches: FUGARO_MODEL_PROVIDERS is decoded with DisallowUnknownFields
// (internal/runner/providers.go) and a decode failure is a hard bootstrap
// error, so a route that sets require_zdr or price_policy — at any value,
// default or not: the omitempty tags write the key whenever the owner set it
// — cannot be deployed while no release reads it. update --repo refuses it
// before any cloud effect, naming the release that will read it and both ways
// out (leave it unset, or price the model under model_prices); a route that
// sets neither passes, and so does a repository with no provider at all.
//
// The shipped half (the release that reads them has been tagged) judges the
// installation's base image the way the clone gate does: a release that
// predates catalogSince is refused, a non-release ref only warned about, and
// no base image at all means the job is built from this release, which reads
// them.
func TestRefuseCatalogSwitches(t *testing.T) {
	yes, no := true, false
	allows := []string{"acme/app"}
	cases := []struct {
		name string
		p    config.ModelProvider
		set  bool
	}{
		{"neither key", config.ModelProvider{AllowDataTo: allows}, false},
		{"require_zdr: false", config.ModelProvider{AllowDataTo: allows, RequireZDR: &no}, true},
		{"require_zdr: true (the default, but still written)", config.ModelProvider{AllowDataTo: allows, RequireZDR: &yes}, true},
		{"price_policy: highest", config.ModelProvider{AllowDataTo: allows, PricePolicy: config.PricePolicyHighest}, true},
		{"price_policy: cheapest (the default, but still written)", config.ModelProvider{AllowDataTo: allows, PricePolicy: config.PricePolicyCheapest}, true},
		{"both", config.ModelProvider{AllowDataTo: allows, RequireZDR: &no, PricePolicy: config.PricePolicyHighest}, true},
		{"a switch on a route that may not receive this repository", config.ModelProvider{AllowDataTo: []string{"acme/other"}, RequireZDR: &no}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lc := &localcfg.Config{Providers: map[string]config.ModelProvider{"openrouter": c.p}}
			var warn bytes.Buffer
			err := refuseCatalogSwitches("acme/app", lc, &warn)
			if !c.set {
				if err != nil || warn.Len() != 0 {
					t.Fatalf("err = %v, warn = %q, want neither", err, warn.String())
				}
				// The job's own value must not carry the key either: this
				// gate and ProvidersEnv agree on what "set" means.
				v, err := runner.ProvidersEnv(lc.Providers, "acme/app")
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(v, "require_zdr") || strings.Contains(v, "price_policy") {
					t.Fatalf("the gate let through a value the env writes a key for: %s", v)
				}
				return
			}
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want refused", err)
			}
			for _, want := range []string{
				"fugaro update --repo refuses to deploy acme/app's provider openrouter",
				"require_zdr or price_policy",
				"fugaro " + catalogSince,
				"model_prices",
				"require_zdr: true, price_policy: cheapest",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v lacks %q", err, want)
				}
			}
			if warn.Len() != 0 {
				t.Errorf("warn = %q, want none (refused, not warned)", warn.String())
			}
			// The key really is in the value that would be deployed: the
			// refusal is about a field an older runner would refuse.
			v, err := runner.ProvidersEnv(lc.Providers, "acme/app")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(v, "require_zdr") && !strings.Contains(v, "price_policy") {
				t.Fatalf("the gate refused a value that carries no key: %s", v)
			}
		})
	}

	// Several routes: every one that set a key is named, sorted.
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"zroute": {AllowDataTo: allows, PricePolicy: config.PricePolicyHighest},
		"aroute": {AllowDataTo: allows, RequireZDR: &no},
		"plain":  {AllowDataTo: allows},
	}}
	err := refuseCatalogSwitches("acme/app", lc, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "providers aroute, zroute") || strings.Contains(err.Error(), "plain") {
		t.Fatalf("two routes: %v", err)
	}

	// The shipped half: the base image decides. Before catalogSince is
	// refused, at it (and past it) passes, a ref that is not a release only
	// warns, and no base image at all passes (the job is built from this
	// release, which reads the keys).
	names := []string{"openrouter"}
	judge := func(ref string) (string, error) {
		var warn bytes.Buffer
		err := catalogSwitchSkew("acme/app", names, ref, true, &warn)
		return warn.String(), err
	}
	warn, err := judge("ghcr.io/dimipaun/fugaro-base:0.7.0")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("0.7.0: err = %v, want refused", err)
	}
	for _, want := range []string{
		"the provider openrouter of acme/app sets require_zdr or price_policy",
		"fugaro " + catalogSince + " or later",
		"ghcr.io/dimipaun/fugaro-base:0.7.0, release 0.7.0",
		"fugaro update --base base",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if warn != "" {
		t.Errorf("warn = %q, want none", warn)
	}
	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + catalogSince, "ghcr.io/dimipaun/fugaro-base:0.9.0", ""} {
		if warn, err := judge(ref); err != nil || warn != "" {
			t.Errorf("%q: err = %v, warn = %q, want neither", ref, err, warn)
		}
	}
	warn, err = judge("us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c")
	if err != nil || !strings.Contains(warn, "not a release base image") || !strings.Contains(warn, "is not checked") {
		t.Errorf("dev ref: err = %v, warn = %q", err, warn)
	}
}

// TestGateCatalogSwitches is repoEngine's guard around the refusal above:
// --forget and --plan-only pass it through (nothing is deployed; the plan can
// still be shown), and --print-vars is judged with its warning on stderr, so
// stdout stays the Terraform variables alone.
func TestGateCatalogSwitches(t *testing.T) {
	no := false
	lc := &localcfg.Config{
		Providers:  map[string]config.ModelProvider{"openrouter": {AllowDataTo: []string{"acme/app"}, RequireZDR: &no}},
		BaseImages: map[string]string{"base": "ghcr.io/dimipaun/fugaro-base:0.7.0"},
	}
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := &initRun{o: &initOptions{}, cmd: cmd, w: &out}
	if err := r.gateCatalogSwitches(r.o, "acme/app", lc); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want refused", err)
	}
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateCatalogSwitches(o, "acme/app", lc); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	// --print-vars: refused the same way, and nothing of it lands on stdout.
	pv := &initOptions{printVars: true}
	r = &initRun{o: pv, cmd: cmd, w: io.Discard}
	if err := r.gateCatalogSwitches(pv, "acme/app", lc); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars: stdout = %q, want empty", out.String())
	}
}

// TestPolicyVersionSinceRowOneIsTheModel: version 1 is the policy model
// itself, so projectpolicy.VersionSince's first row is the release policySince
// names. The two are separate on purpose (that one is the model's release, the
// table is per version, and its own guard is in internal/projectpolicy), and
// this is the one place they must agree.
func TestPolicyVersionSinceRowOneIsTheModel(t *testing.T) {
	if got := projectpolicy.VersionSince[1]; got != policySince {
		t.Errorf("projectpolicy.VersionSince[1] = %q, want policySince (%q)", got, policySince)
	}
}

// TestPolicyTooNewFixNamesTheRelease is doctor's fix line for a node ahead of
// this binary: the upgrade, naming the release that introduced the version on
// the node when the table knows it. It is pinned directly because the branch
// is not reachable through a launch on this release — this binary understands
// version 2, so every too-new node it meets is version 3 or above, and the
// table's rows end at 2 — while a release that reads an older line (an 0.8.1
// patch carrying this fix) meets version 2 exactly, which is the case below.
func TestPolicyTooNewFixNamesTheRelease(t *testing.T) {
	fix := policyTooNewFix(2)
	for _, want := range []string{"upgrade Fugaro", "fugaro 0.8.5", "version 2"} {
		if !strings.Contains(fix, want) {
			t.Errorf("policyTooNewFix(2) = %q, want %q", fix, want)
		}
	}
	if strings.Contains(fix, policySince) {
		t.Errorf("policyTooNewFix(2) = %q: it names policySince, the release the model ships in, which the operator of a version-1 binary already runs", fix)
	}
	// A version no row names: the upgrade alone, and no release borrowed from
	// the table's other rows.
	fix = policyTooNewFix(99)
	if !strings.Contains(fix, "upgrade Fugaro") || !strings.Contains(fix, "version 99") {
		t.Errorf("policyTooNewFix(99) = %q, want the plain upgrade naming the version", fix)
	}
	for _, rel := range projectpolicy.VersionSince {
		if strings.Contains(fix, rel) {
			t.Errorf("policyTooNewFix(99) = %q: it names %s, a release the table knows for another version", fix, rel)
		}
	}
}
