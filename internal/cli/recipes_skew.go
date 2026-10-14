package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
)

// recipesSince is the first fugaro release whose runner reads a task's
// recipe and fugaro.yaml's agent.recipe; an older runner refuses both. It
// must equal the release that ships recipes: docs/release.md ("Before you
// tag") has the release checklist verify it.
const recipesSince = "0.5.0"

// layeredSince is the first fugaro release whose runner reads a task's
// project layer and fugaro.yaml's profile keys, and whose base image's
// fugaro renders with --layer-bucket; an older one refuses all of them.
// docs/release.md ("Before you tag") has the checklist verify it.
const layeredSince = "0.6.0"

// cloneSince is the release that will ship plan Task 17's runner bootstrap
// (FUGARO_CHECKOUT and the blobless clone it reads at start): Task 17 has
// not landed yet (no code under internal/ reads FUGARO_CHECKOUT), so no
// fugaro release knows it, and this gate refuses every checkout: clone
// launch on any job image's release, including the current one, until it
// does. checkCloneImageSince below gates it unconditionally, whether or
// not the workflow also uses a recipe or a project layer: nothing else here
// checks that base_images.base is actually this recent (fugaro-base could
// first be published from an older release, or --image-source could mirror
// one). docs/release.md's ("Before you tag") checklist line for it must
// stay unchecked, and this constant must equal the release that ships Task
// 17's runner bootstrap, until that task lands.
const cloneSince = "0.7.1"

// recipesV07Since is the first fugaro release whose runner knows the 0.7.0
// recipe format: use_when, mode, roles.coder, a check step and
// review.bounce; a parser before it refuses every one of those keys as
// unknown (recipe.UsesV07). docs/release.md's checklist verifies it equals
// the release that ships them.
const recipesV07Since = "0.7.0"

// namedTokensSince is the first fugaro release whose runner reads a task's
// oauth_token field (docs/design/named-oauth-tokens.md); an older runner
// refuses it, since task.Spec's decoder disallows unknown fields. It must
// equal the release that ships task.Spec.OAuthToken: docs/release.md
// ("Before you tag") has the release checklist verify it. internal/cli/run.go
// gates a launch that would set OAuthToken to a non-default name against an
// older build record, the same way checkImageSince already gates
// recipesSince and layeredSince.
const namedTokensSince = "0.6.5"

// needsRecipeImage reports whether a launch needs a runner that knows
// recipes: the task carries one, or the checkout's fugaro.yaml sets
// agent.recipe (an unknown key to an older runner).
func needsRecipeImage(spec *task.Spec, checkoutRecipe string) bool {
	return spec.Recipe != nil || checkoutRecipe != ""
}

// embeddedRecipeOf is the recipe spec.Recipe's exact YAML parses to, or nil
// when there is none to parse: a repository-sourced recipe carries only its
// name (the runner re-reads the file at the ref), and a bare agent.recipe
// name is not resolved here either. Both still get the plain recipesSince
// gate below; whether either uses a 0.7.0 construct is for the run itself to
// find out, the same way an older runner already refuses an unknown key.
func embeddedRecipeOf(spec *task.Spec) *recipe.Recipe {
	if spec.Recipe == nil || spec.Recipe.YAML == "" {
		return nil
	}
	r, ps := recipe.Parse([]byte(spec.Recipe.YAML))
	if len(ps) > 0 {
		return nil
	}
	return r
}

// checkRecipeImage refuses a launch whose job image runs a fugaro older than
// recipesSince (decision D2), or than recipesV07Since when the embedded
// recipe uses a 0.7.0 construct (design generic-tool.md §3.1). The judge is
// the build record's base_ref: the image runs the binary of the base it was
// built FROM. A non-release base is not judged (one warning); no record, or
// one without base_ref, is refused. kind is the workflow's base kind, ""
// when unknown.
func checkRecipeImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	subject, alt := "fugaro.yaml's agent.recipe", ""
	if spec.Recipe != nil {
		subject = "recipe " + spec.Recipe.Name
		if spec.Recipe.Name != recipe.DefaultName { // a carried default cannot be the way out
			alt = "; or launch with --recipe default to run today's loop"
		}
	}
	since := recipesSince
	if r := embeddedRecipeOf(spec); r != nil && recipe.UsesV07(r) {
		since = recipesV07Since
	}
	return checkImageSince(ctx, env, slug, spec, kind, since, subject, "recipes", alt, warn)
}

// checkImageSince refuses a launch whose job image runs a fugaro older than
// since: subject needs a runner that knows knows. The judge is the build
// record's base_ref: the image runs the binary of the base it was built
// FROM. A non-release base is not judged (one warning); no record, or one
// without base_ref, is refused. kind is the workflow's base kind, "" when
// unknown.
func checkImageSince(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind, since, subject, knows, alt string, warn io.Writer) error {
	base := "this release's base image"
	if kind != "" {
		base = "this release's " + kind + " base image"
	}
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the job image of %s workflow %s %s. "+
			"Run fugaro image refresh --repo %s --workflow %s in its checkout, in your own terminal window, or with --yes from CI or a script (never in a coding agent's session; it copies %s, points the daily image check job at it and rebuilds the image)%s",
			subject, knows, since, spec.Repo, spec.Workflow, why, spec.Repo, spec.Workflow, base, alt)
	}
	b, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, spec.Workflow))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return refuse("has no build record")
	case err != nil:
		return remote(fmt.Errorf("reading the build record of %s workflow %s: %w", spec.Repo, spec.Workflow, err))
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		return refuse("has an unreadable build record (" + oneLineCLI(err.Error()) + ")")
	}
	if rec.BaseRef == "" {
		return refuse("has a build record that does not say which base image it was built from")
	}
	ref := pluginwire.Printable(rec.BaseRef)
	// Only the binary's release matters: the image's base kind may differ from
	// the workflow's (a rebuilt image, a renamed kind) and that is deliberately
	// not judged here; init and image build own that.
	switch rel, ok := releaseOf(rec.BaseRef); {
	case !ok:
		fmt.Fprintf(warn, "warning: the job image of %s workflow %s was built from %s, which is not a release base image; whether its runner knows %s is not checked\n", spec.Repo, spec.Workflow, ref, knows)
	case imagePredates(rel, since):
		return refuse(fmt.Sprintf("was built from base image %s, release %s", ref, rel))
	}
	return nil
}

// releaseOf is ref's release version, through baseRefReleaseRE (a release
// base image of one of config.Bases, <host and path>/fugaro-<kind>:<X.Y.Z>,
// with an optional digest), and whether it matched at all: a dev build, a
// hand-pushed image or one --image-source mirrored without a release tag
// does not, and is never judged, only warned about.
func releaseOf(ref string) (string, bool) {
	m := baseRefReleaseRE.FindStringSubmatch(ref)
	if m == nil {
		return "", false
	}
	return m[2], true
}

// checkCloneImageSince is checkImageSince for a checkout: clone workflow:
// its job has no derived image and so no build record to judge (it is
// never built, only base_images.base's ref, run as is); the judge is that
// ref itself. A non-release ref (dev, hand-pushed, or --image-source
// without a release tag) only warns, same as checkImageSince; a repository
// with none at all is refused outright (there is nothing for the job to
// run). When since is cloneSince itself, an image that predates it is
// refused with guidance of its own (no release knows FUGARO_CHECKOUT yet,
// so "run fugaro init --base base" would not fix anything): see the
// cloneSince-specific branch below.
func checkCloneImageSince(lc *localcfg.Config, spec *task.Spec, since, subject, knows, alt string, warn io.Writer) error {
	ref := lc.BaseImage(config.BaseKind)
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the checkout: clone job image of %s workflow %s %s. "+
			"Run fugaro init --base base in your own terminal window to move the installation's base image to a current release%s",
			subject, knows, since, spec.Repo, spec.Workflow, why, alt)
	}
	if ref == "" {
		return refuse("is not set (the local config has no base_images.base)")
	}
	switch rel, ok := releaseOf(ref); {
	case !ok:
		fmt.Fprintf(warn, "warning: the checkout: clone job image of %s workflow %s is %s, which is not a release base image; whether its runner knows %s is not checked\n",
			spec.Repo, spec.Workflow, pluginwire.Printable(ref), knows)
	case imagePredates(rel, since) && since == cloneSince:
		// cloneSince names a release that does not exist yet (plan Task 17
		// has not shipped its runner side): naming "fugaro init --base base"
		// here would send the user to re-copy the same base image and hit
		// this refusal again. Say so plainly, and name checkout: baked, the
		// only checkout that actually runs today.
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later); checkout: clone's own runner side is not released yet "+
			"(generic-tool plan Task 17 has not shipped), so the checkout: clone job image of %s workflow %s (%s, release %s) is refused "+
			"regardless of its release. Use checkout: baked instead to run it today.",
			subject, knows, since, spec.Repo, spec.Workflow, pluginwire.Printable(ref), rel)
	case imagePredates(rel, since):
		return refuse(fmt.Sprintf("is %s, release %s", pluginwire.Printable(ref), rel))
	}
	return nil
}

// checkCloneRecipeImage is checkRecipeImage for a checkout: clone workflow
// (recipesSince, or recipesV07Since for an embedded recipe that uses a 0.7.0
// construct): see checkCloneImageSince.
func checkCloneRecipeImage(lc *localcfg.Config, spec *task.Spec, warn io.Writer) error {
	subject, alt := "fugaro.yaml's agent.recipe", ""
	if spec.Recipe != nil {
		subject = "recipe " + spec.Recipe.Name
		if spec.Recipe.Name != recipe.DefaultName { // a carried default cannot be the way out
			alt = "; or launch with --recipe default to run today's loop"
		}
	}
	since := recipesSince
	if r := embeddedRecipeOf(spec); r != nil && recipe.UsesV07(r) {
		since = recipesV07Since
	}
	return checkCloneImageSince(lc, spec, since, subject, "recipes", alt, warn)
}
