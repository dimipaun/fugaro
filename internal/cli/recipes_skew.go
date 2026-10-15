package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

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

// cloneSince names the release whose runner reads FUGARO_CHECKOUT and makes
// a blobless clone at start (plan Task 17). Today it names a release that
// does not exist yet, on purpose (see cloneShipped): until that release is
// tagged, this constant is a placeholder for "the release that will ship
// it", not
// a verified fact the way recipesSince/layeredSince/namedTokensSince above
// are. checkCloneImageSince below gates it unconditionally, whether or not
// the workflow also uses a recipe or a project layer: nothing else here
// checks that base_images.base is actually this recent (fugaro-base could
// first be published from an older release, or --image-source could mirror
// one). Once Task 17 ships, this constant's job becomes the same as the
// others': docs/release.md's ("Before you tag") checklist verifies it
// equals the release that ships it.
const cloneSince = "0.7.5"

// cloneShipped is false until the release named by cloneSince is actually
// tagged: Task 17's runner side (the runner's own FUGARO_CHECKOUT support,
// and the refusal of any deployed checkout: clone job before it) is in the
// tree, but no tagged fugaro release carries it yet, so no released binary
// can run one. While it is false:
//   - checkCloneImageSince's cloneSince-specific branches refuse every
//     checkout: clone launch on any job image, known release or not,
//     including the current one, with guidance that the runner side is
//     not released yet (not the ordinary "upgrade your base image" line,
//     which would not fix anything here);
//   - refuseClonesNotShipped refuses fugaro init --repo and fugaro image
//     refresh before either deploys or moves a checkout: clone workflow's
//     job, so a clone job can never come to exist in the first place —
//     closing the gap checkCloneImageSince's launch-time check alone
//     leaves open (a plain `fugaro run` from outside the checkout, or a
//     stale checkout, decides from the deployed job, never reaching that
//     check at all).
//
// Flip this to true in its own tiny PR — never in a release commit, and
// never before live Check 38 (docs/gcp-live-checklist.md) has passed: the
// check needs a build with this gate open, and no release may ship
// checkout: clone before the check has run. That one edit is what turns
// both of the above back into their ordinary behavior (string equality
// against cloneSince alone cannot tell "not released yet" from "this image
// or job just predates a release that now exists"); update docs/release.md's
// "Verify cloneSince" bullet in that PR too, since that bullet is what says
// the flip has not happened yet. Bump cloneSince in that same PR too, if
// Check 38 found the runner side needs a fix: the release that fix ships in
// is the release cloneSince then has to name. If the check needed no fix,
// cloneSince stays as it is — it already names the release that ships
// Task 17.
const cloneShipped = false

// recipesV07Since is the first fugaro release whose runner knows the 0.7.0
// recipe format: use_when, mode, roles.coder, a check step and
// review.bounce; a parser before it refuses every one of those keys as
// unknown (recipe.UsesV07). docs/release.md's checklist verifies it equals
// the release that ships them.
const recipesV07Since = "0.7.0"

// gitSecretIDSince is the first fugaro release whose runner reads
// CheckJobSpec's git_secret_id (internal/infra/spec.go): the daily image
// check job's own rebuild names the secret ID Readiness resolved, so a
// project-wide provider credential resolves for the rebuild exactly as it
// does for the job's clone. A runner before it ignores the field (plain
// json.Unmarshal) and falls back to the repository's own ID, which mounts a
// secret with no version once that name has moved to project scope (design
// docs/design/project-secrets.md §5, §6 step 4a).
//
// It is a genuine, narrow exception to P6's "no *Since constant per feature":
// unlike every other gate here, it is not judged against a build record
// (there is none to judge: the check job always runs the base image itself),
// but against ready.Check.Image, the check job's own base ref, in planRepo --
// see the gate there. docs/release.md ("Before you tag") has the checklist
// verify it equals the release that ships the field.
const gitSecretIDSince = "0.7.5"

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
// without a release tag) only warns, same as checkImageSince, UNLESS since
// is cloneSince itself and cloneShipped is still false: no build of any
// release or otherwise reads FUGARO_CHECKOUT yet, so a dev or hand-pushed
// image cannot make a checkout: clone launch work either, and letting it
// through on a warning (review finding, 0.7.0 safety fix round 2) would
// fail the run at bootstrap exactly like an old release would. A
// repository with no base image at all is refused outright (there is
// nothing for the job to run). Once cloneShipped flips to true, both of
// cloneSince's special cases above stop firing and this threshold gets the
// same "upgrade your base image" refusal (or warning) as any other.
func checkCloneImageSince(lc *localcfg.Config, spec *task.Spec, since, subject, knows, alt string, warn io.Writer) error {
	ref := lc.BaseImage(config.BaseKind)
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the checkout: clone job image of %s workflow %s %s. "+
			"Run fugaro init --base base in your own terminal window to move the installation's base image to a current release%s",
			subject, knows, since, spec.Repo, spec.Workflow, why, alt)
	}
	// notShipped is cloneSince's own refusal, used whether the image
	// predates cloneSince (a known, too-old release) or its release is not
	// known at all (a dev build, a hand-pushed image, or an --image-source
	// mirror without a release tag): cloneShipped being false means no
	// image, of any release or none, can run checkout: clone, so
	// "fugaro init --base base" (refuse's own fix line) would not help
	// either way. Say so plainly, and name checkout: baked, the only
	// checkout that actually runs today.
	notShipped := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later); checkout: clone's own runner side is not released yet "+
			"(generic-tool plan Task 17 has not shipped), so the checkout: clone job image of %s workflow %s (%s) is refused "+
			"regardless of its release. Use checkout: baked instead to run it today.",
			subject, knows, since, spec.Repo, spec.Workflow, why)
	}
	if ref == "" {
		return refuse("is not set (the local config has no base_images.base)")
	}
	switch rel, ok := releaseOf(ref); {
	case !ok && since == cloneSince && !cloneShipped:
		return notShipped(pluginwire.Printable(ref) + ", which is not a release base image")
	case !ok:
		fmt.Fprintf(warn, "warning: the checkout: clone job image of %s workflow %s is %s, which is not a release base image; whether its runner knows %s is not checked\n",
			spec.Repo, spec.Workflow, pluginwire.Printable(ref), knows)
	case imagePredates(rel, since) && since == cloneSince && !cloneShipped:
		return notShipped(fmt.Sprintf("%s, release %s", pluginwire.Printable(ref), rel))
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

// refuseClonesNotShipped refuses, before any cloud effect, deploying or
// moving a checkout: clone workflow's job while cloneShipped is false (0.7.0
// safety fix round 2): checkCloneImageSince's launch-time refusal alone
// leaves a gap it cannot close — a plain `fugaro run --repo X --workflow W`
// launched from outside the checkout, or from a stale checkout whose local
// fugaro.yaml no longer says clone, decides only from the DEPLOYED job
// (run.go's checkoutConfig reads the checkout, not the job), so it never
// reaches that gate at all; if the deployed job happens to be a clone job,
// the run fails at bootstrap instead. Making it impossible for a clone job
// to exist while cloneShipped is false closes that gap at the source:
// fugaro init --repo's planRepo calls this before its Apply, and fugaro
// image refresh's refreshCloneJobs calls it before moving any job, each
// naming only the clone workflow(s) it was about to act on — a baked
// sibling in the same repository is unaffected and unmentioned. cmd names
// the command refusing (for the message alone); verb is what it was about
// to do ("deploy", "move"). A nil names, or cloneShipped true, is a no-op.
func refuseClonesNotShipped(repo, cmd, verb string, names []string) error {
	if cloneShipped || len(names) == 0 {
		return nil
	}
	return userErr("%s refuses to %s %s workflow %s: checkout: clone's own runner side is not released yet "+
		"(generic-tool plan Task 17 has not shipped), so no release can run its job yet. Use checkout: baked "+
		"instead to run it today; checkout: clone starts working at fugaro %s.",
		cmd, verb, repo, strings.Join(names, ", "), cloneSince)
}
