package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
// a blobless clone at start (plan Task 17): 0.7.5, built and tagged, so it
// is a verified fact the way recipesSince/layeredSince/namedTokensSince above
// are. checkCloneImageSince below gates it unconditionally, whether or not
// the workflow also uses a recipe or a project layer: nothing else here
// checks that base_images.base is actually this recent (fugaro-base could
// first be published from an older release, or --image-source could mirror
// one).
const cloneSince = "0.7.5"

// cloneShipped is true — since 0.9.0, after live Check 38
// (docs/gcp-live-checklist.md) passed on 2026-10-17: the release named by
// cloneSince is tagged, its runner reads FUGARO_CHECKOUT and makes the
// blobless clone, and the check proved a checkout: clone workflow end to end
// on the installation's own release base image. It is what makes the two
// disabled-state behaviors below inert:
//   - checkCloneImageSince's cloneSince-specific branches (the
//     "notShipped" wording) no longer fire: a checkout: clone launch is
//     judged by cloneSince alone, exactly like every other *Since gate —
//     an image older than 0.7.5 is refused with the ordinary "upgrade your
//     base image" line, and a ref with no release tag (a dev build, a
//     hand-pushed image, an --image-source mirror) is only warned about;
//   - refuseClonesNotShipped returns nil, so fugaro update --repo and fugaro
//     image refresh deploy and move a checkout: clone workflow's job the
//     way they do any other.
//
// Both stay in the tree, behind this constant, for the same reason every
// other shipped flag here keeps its gate: the flip is one boolean, and a
// test that pins the shipped behavior fails the moment it is flipped back
// (TestCheckCloneImageSinceAtCloneSince, TestPlanRepoAcceptsCloneWorkflow,
// TestRefreshMovesCloneJob, TestDocsCheckoutSaysWhetherCloneIsShipped).
// Nothing here should ever flip it back: 0.9.0 ships checkout: clone, and a
// later release that had to un-ship it would be a different decision, made
// in its own PR with the check's failure as its evidence.
const cloneShipped = true

// policySince is the first fugaro release whose launcher and runner read the
// project's policy (the release phase 1 of the project-centric model ships
// in, R4). Like cloneSince and unlike the policy's own integer `version` or
// the rules marker's RulesVersion, it is a *release*, and it is bumped by the
// release that ships the model and by nothing else: docs/release.md's ("Before
// you tag") checklist verifies it equals that release, the same way it does for
// recipesSince, layeredSince and namedTokensSince.
//
// The launch gate (launchPolicyGates, run_allowlist.go) and doctor's checks
// quote it where the subject is the model: the rules a policy is read under,
// the marker that records them, and the release a node *behind* this binary was
// written by. They never quote it where the subject is a policy version above
// what this binary understands — that answer is the row of
// projectpolicy.VersionSince for that version, which stopped being this
// constant the moment the version was bumped past 1.
const policySince = "0.8.0"

// policyTooNewFix is doctor's fix line for a policy node ahead of this binary:
// the upgrade, naming the release that introduced the version on the node when
// projectpolicy.VersionSince knows it, and the plain upgrade when it does not —
// the same table, and the same rule, as every too-new refusal
// (projectpolicy.SkewSentence). A version no row names came from a
// release newer than any this binary has heard of, and borrowing a row's
// release for it would name one that is already installed, which is the fault
// the table exists to fix.
func policyTooNewFix(version int) string {
	if rel, ok := projectpolicy.VersionRelease(version); ok {
		return fmt.Sprintf("upgrade Fugaro to fugaro %s or later, the release that introduced policy version %d", rel, version)
	}
	return fmt.Sprintf("upgrade Fugaro to a release that understands version %d", version)
}

// catalogSince names the release whose runner reads a route's require_zdr
// and price_policy (price-catalog plan Task 4, design D11): a runner before
// it decodes FUGARO_MODEL_PROVIDERS with DisallowUnknownFields
// (internal/runner/providers.go), so a job whose value carries either key
// fails at bootstrap with an error naming a field nobody typed. Today it
// names a release that does not exist yet, on purpose (see catalogShipped).
// Once the release that ships them is tagged, this constant's job becomes
// the same as recipesSince's above: docs/release.md's ("Before you tag")
// checklist verifies it equals the release that ships them.
const catalogSince = "0.7.5"

// catalogShipped is false until the release named by catalogSince is
// actually tagged: no fugaro release reads a route's require_zdr or
// price_policy yet, so no runner that can exist today can run a job whose
// value carries either key. While it is false, catalogSwitchSkew refuses
// fugaro update --repo outright when any provider that may receive the
// repository's code sets one — before any cloud effect, since the job it
// would deploy carries FUGARO_MODEL_PROVIDERS — rather than judging the
// base image (no release, of any base image, would read the key, so
// "upgrade your base image" would not fix anything).
//
// Flip this to true in the same commit that bumps catalogSince to the
// release that ships them: that one edit turns the refusal back into the
// ordinary base-image age check (the installation's base image predating
// catalogSince), the same shape as checkCloneImageSince's; update
// docs/release.md's checklist line for catalogSince in that commit too.
const catalogShipped = false

// managedDeployerShipped is false until the release that ships a managed
// deployer IAM can actually create: 0.9.6, by the owner's ruling of
// 2026-10-20, which moved the redesign off 0.9.5 (the ruling of 2026-10-19 had
// moved it off 0.9.0, where the ruling of 2026-10-18 put it: "Gate it off by
// default; redesign for 0.9.0"). 0.9.0 and 0.9.5 therefore ship with the
// scoped role still gated off, as 0.8.6 and 0.8.7 do. The deployer's
// scoped role is bound under an fmgd- prefix condition that GCP rejects as
// written (17 logical operators where IAM allows 12, resource.name.contains()
// is not a supported function, and resource.name is not supported for most of
// the resource types it names), so the installation module creates that role
// and its binding only with managed_deployer_scoped, which nothing sets
// (deploy/terraform/gcp/modules/installation/deployer.tf). While it is false,
// every command that would create a managed project or act as its deployer
// (fugaro setup --create-project --managed, fugaro managed apply and fugaro
// restore) refuses with managedDisabled before any cloud call, rather than
// failing later at the apply or the impersonation.
//
// Flip it to true only in the release whose deployer fence has been
// redesigned and measured live, and in that same change set
// infra.InstallOptions.ManagedDeployerScoped for the installations that opt
// in; docs/release.md's "Before you tag" checklist verifies it.
const managedDeployerShipped = false

// managedDeployerOpen is managedDeployerShipped. It is a variable only so the
// tests of the managed paths themselves can run them with the gate open
// (TestMain opens it); the refusal tests reset it to the constant, and no
// production code assigns it.
var managedDeployerOpen = managedDeployerShipped

// managedDisabled is the one refusal every managed entry point gives while
// managedDeployerShipped is false.
const managedDisabled = "managed projects are disabled in this release: the managed deployer's scoped role and its binding are not created yet (docs/design/managed-projects.md); a project created without --managed is unaffected"

// refuseManagedNotShipped is managedDisabled as an error, nil once the
// managed deployer ships.
func refuseManagedNotShipped() error {
	if managedDeployerOpen {
		return nil
	}
	return userErr("%s", managedDisabled)
}

// batchProvisionShipped is true as of 0.9.5, the release that fixed the
// compute module the stage behind it applies. Until then it was false because
// the module's reaper role named batch.jobs.cancel -- not a permission GCP
// has, so the role could not be created and the apply could not complete --
// and init refused --provision-batch before any cloud call. 0.9.5 is the
// release whose reaper role is the five permissions GCP defines and whose
// queue deadline deletes rather than cancels (internal/backend/batch/reap.go),
// so the role is creatable and the stage completes; the flag's own tests are
// where the stage is exercised.
//
// There is deliberately no batchProvisionOpen, no batchProvisionDisabled and
// no refuseBatchProvisionNotShipped beside it: an open gate nothing closes and
// a refusal nothing can reach are names a later reader re-opens, which is
// exactly what the pair existed to prevent. The managed gate above keeps its
// pair because it is still closed.
const batchProvisionShipped = true

// artifactsSince is the first fugaro release whose runner implements a
// mode: artifact recipe: it reads the task's mode field
// (internal/task/task.go), renders through commands.render and writes the
// run record's render object. A runner before it refuses the task outright
// (task.Parse decodes with DisallowUnknownFields), so an artifact launch
// against an older job image is refused at launch, by this gate, through
// the same seam every other spec-field skew uses.
//
// Unlike cloneSince it carries no shipped flag: refusing every image that
// predates the mode is exactly the direction this gate wants, and an image
// with no release tag (a dev build, a hand-pushed one) only warns, the same
// as for every other *Since constant here — the runner fails closed on the
// mode and the field anyway, so a warning cannot let an artifact run through
// as an implement run.
//
// docs/release.md ("Before you tag") has the checklist verify it equals the
// release that ships the mode.
const artifactsSince = "0.8.0"

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

// sealSince is the first fugaro release whose runner reads a sealed budget
// token (docs/design/budget-token-channel.md §7.1): the marker's value is the
// launch's seal key, parsed strictly and never echoed, the key is registered
// with the run's redactor before anything can print it, and the token object
// is read from tokens/, never from runs/ and never as plaintext. A runner
// before it does none of that — an image built before the seal's runner half
// formats a marker value it cannot parse into its bootstrap error, so the
// launch's key would land in the run's record (§7.4, D19) — so a launch that
// mints a budget token is refused against a job image older than this, before
// the claim, the mint or the object (sealGate). It is D19's "runner and image
// first, launcher second" as a gate rather than as an ordering of
// deployments, which the owner accepted as the alternative. It names the
// release the seal ships in (0.9.0, the design's "Which release" paragraph);
// docs/release.md's ("Before you tag") checklist verifies it the way it does
// for the other *Since constants.
const sealSince = "0.9.0"

// sealRunnerIntroduced is when the seal's runner half (#417, Slice A) reached
// the integration branch (its merge into next-0.9, 2026-10-17 17:45:33 UTC).
// A job image whose base is not a release has no version to compare with
// sealSince. Its build record is rendered by the base's own fugaro, which
// says so when it reads the sealed token (Record.SealedBudgetToken, a pass);
// failing that, the record's built_at is the job image's build time, and a
// job image built before this cannot hold a runner that reads the sealed
// token, so the seal gate refuses it (the owner's answer of 2026-10-17: a
// development image that predates the seal is refused, one that cannot be
// shown either way is warned about). A job image built after it, with no
// such statement, may have been built FROM an older base, which the record
// cannot show (built_at is not the base's build time): that one is only
// warned about.
var sealRunnerIntroduced = time.Date(2026, 10, 17, 17, 45, 33, 0, time.UTC)

// titlesSince is the first fugaro release whose runner reads a task's title
// (task.Spec.Title, fugaro run --title): an older runner refuses the field
// outright, since task.Parse decodes with DisallowUnknownFields, and the
// run would fail at bootstrap on a task nobody can read. It must equal the
// release that ships task.Spec.Title: docs/release.md ("Before you tag")
// has the release checklist verify it. internal/cli/run.go gates a launch
// that would set a title against an older build record, exactly as it does
// for a named Claude account (namedTokensSince) and for mode: artifact
// (artifactsSince); a launch with no --title writes no such field and is
// never gated.
const titlesSince = "0.8.5"

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
	if r := embeddedRecipeOf(spec); r != nil && recipe.UsesV08(r) {
		// UsesV08 implies UsesV07 (a 0.8.0 construct always comes with a
		// 0.7.0 one: mode is one), and artifactsSince is the newer of the
		// two, so it is the binding gate.
		since = artifactsSince
	} else if r := embeddedRecipeOf(spec); r != nil && recipe.UsesV07(r) {
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
	return checkImageSinceDev(ctx, env, slug, spec, kind, since, subject, knows, alt, nil, warn)
}

// checkImageSinceDev is checkImageSince with a judge for an image whose base
// is not a release: devJudge returns the refusal of such an image when its
// record shows it cannot know knows, supported when the record shows it does
// (no warning then), and neither when that cannot be shown (the warning
// stands, with note appended when the judge gives one: what is at stake and
// the cure). A nil devJudge only warns, as checkImageSince does.
func checkImageSinceDev(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind, since, subject, knows, alt string, devJudge func(rec *imagecheck.Record, ref string) (supported bool, note string, err error), warn io.Writer) error {
	base := "this release's base image"
	if kind != "" {
		base = "this release's " + kind + " base image"
	}
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the job image of %s workflow %s %s. "+
			"Run fugaro image refresh --repo %s --workflow %s in its checkout, in your own terminal window, or with --yes from CI or a script (never in a coding agent's session; it verifies %s and pins it by digest through the installation's remote repository, points the daily image check job at it and rebuilds the image)%s",
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
		note := ""
		if devJudge != nil {
			supported, n, err := devJudge(rec, ref)
			if err != nil {
				return err
			}
			if supported {
				return nil
			}
			if n != "" {
				note = ": " + n
			}
		}
		fmt.Fprintf(warn, "warning: the job image of %s workflow %s was built from %s, which is not a release base image; whether its runner knows %s is not checked%s\n", spec.Repo, spec.Workflow, ref, knows, note)
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
// is cloneSince itself and cloneShipped is false: no build of any release
// or otherwise reads FUGARO_CHECKOUT then, so a dev or hand-pushed image
// could not make a checkout: clone launch work either, and letting it
// through on a warning (review finding, 0.7.0 safety fix round 2) would
// fail the run at bootstrap exactly like an old release would. A
// repository with no base image at all is refused outright (there is
// nothing for the job to run). cloneShipped is true since 0.9.0, so both
// of cloneSince's special cases are inert and this threshold gets the same
// "upgrade your base image" refusal (or warning) as any other — the
// branches stay for the flip-back the constant's comment describes.
func checkCloneImageSince(lc *localcfg.Config, spec *task.Spec, since, subject, knows, alt string, warn io.Writer) error {
	ref := lc.BaseImage(config.BaseKind)
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the checkout: clone job image of %s workflow %s %s. "+
			"Run fugaro update --base base in your own terminal window to move the installation's base image to a current release%s",
			subject, knows, since, spec.Repo, spec.Workflow, why, alt)
	}
	// notShipped is cloneSince's own refusal, used whether the image
	// predates cloneSince (a known, too-old release) or its release is not
	// known at all (a dev build, a hand-pushed image, or an --image-source
	// mirror without a release tag): cloneShipped being false means no
	// runner a release can build today reads FUGARO_CHECKOUT, so
	// "fugaro update --base base" (refuse's own fix line) would not help
	// either way. Say so plainly, and name checkout: baked, the only
	// checkout that runs then. (Images at cloneSince or later do not reach
	// this closure at all — see the constant's comment.) cloneShipped is
	// true since 0.9.0, so neither switch arm below calls it today.
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
	if r := embeddedRecipeOf(spec); r != nil && recipe.UsesV08(r) {
		// UsesV08 implies UsesV07 (a 0.8.0 construct always comes with a
		// 0.7.0 one: mode is one), and artifactsSince is the newer of the
		// two, so it is the binding gate.
		since = artifactsSince
	} else if r := embeddedRecipeOf(spec); r != nil && recipe.UsesV07(r) {
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
// fugaro update --repo's planRepo calls this before its Apply, and fugaro
// image refresh's refreshCloneJobs calls it before moving any job, each
// naming only the clone workflow(s) it was about to act on — a baked
// sibling in the same repository is unaffected and unmentioned. cmd names
// the command refusing (for the message alone); verb is what it was about
// to do ("deploy", "move"). A nil names, or cloneShipped true, is a no-op —
// and cloneShipped has been true since 0.9.0 shipped the flip, so every
// call is one today: the two call sites, the refusal and its wording stay
// for the flip-back the constant's comment describes, and this is the
// function that PR would make live again.
func refuseClonesNotShipped(repo, cmd, verb string, names []string) error {
	if cloneShipped || len(names) == 0 {
		return nil
	}
	return userErr("%s refuses to %s %s workflow %s: checkout: clone's own runner side is not released yet "+
		"(generic-tool plan Task 17 has not shipped), so no release can run its job yet. Use checkout: baked "+
		"instead to run it today; checkout: clone starts working at fugaro %s.",
		cmd, verb, repo, strings.Join(names, ", "), cloneSince)
}

// switchProviders lists, sorted, the providers that set require_zdr or
// price_policy — at any value — and may receive repo's code. "Set" is the
// test, not "different from the default": the omitempty tags write the key
// whenever the owner wrote it (require_zdr: true and price_policy:
// cheapest are written too), and ProvidersEnv (internal/runner/providers.go)
// writes exactly this distinction, so an explicit default breaks an older
// runner just like the other value does. A provider whose allow_data_to
// does not cover repo is not in the job's value at all, so it cannot break
// it and is not this command's business.
func switchProviders(providers map[string]config.ModelProvider, repo string) []string {
	var names []string
	for _, name := range slices.Sorted(maps.Keys(providers)) {
		if p := providers[name]; p.AllowsData(repo) && (p.RequireZDR != nil || p.PricePolicy != "") {
			names = append(names, name)
		}
	}
	return names
}

// refuseCatalogSwitches refuses fugaro update --repo, before any cloud effect,
// when a provider that may receive the repository's code sets require_zdr or
// price_policy and the runner that would read it cannot be known to exist:
// see catalogSince/catalogShipped above and catalogSwitchSkew below.
func refuseCatalogSwitches(repo string, lc *localcfg.Config, warn io.Writer) error {
	names := switchProviders(lc.Providers, repo)
	if len(names) == 0 {
		return nil
	}
	return catalogSwitchSkew(repo, names, lc.BaseImage(config.BaseKind), catalogShipped, warn)
}

// catalogSwitchSkew is the refusal itself: shipped false refuses every
// provider in names, whatever the base image, because no release reads the
// keys yet; shipped true judges the installation's base image the job is
// built from, the way checkCloneImageSince does — a release that predates
// catalogSince is refused, a ref that is not a release image is only warned
// about (nothing can tell what its runner knows), and no base image at all
// passes (infra.Repo then builds the job from this release's own image,
// which reads them). names must not be empty and is already sorted.
func catalogSwitchSkew(repo string, names []string, ref string, shipped bool, warn io.Writer) error {
	where := "provider " + names[0]
	if len(names) > 1 {
		where = "providers " + strings.Join(names, ", ")
	}
	if !shipped {
		return userErr("fugaro update --repo refuses to deploy %s's %s with require_zdr or price_policy set: no fugaro release reads "+
			"those route keys yet (they ship at fugaro %s), and FUGARO_MODEL_PROVIDERS is decoded strictly, so an older runner fails "+
			"the job at bootstrap on a field nobody typed. Leave both unset for now (their defaults are require_zdr: true, "+
			"price_policy: cheapest), or price the model under model_prices, which replaces the catalog's row and both switches with it",
			repo, where, catalogSince)
	}
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: the %s of %s sets require_zdr or price_policy, but the installation's base image is %s, which is not a release base image; whether its runner reads them is not checked\n",
			where, repo, pluginwire.Printable(ref))
	case imagePredates(rel, catalogSince):
		return userErr("the %s of %s sets require_zdr or price_policy, which needs a job image whose runner reads them (fugaro %s or later), "+
			"but the installation's base image is %s, release %s. Run fugaro update --base base in your own terminal window to move the "+
			"installation's base image to a current release, or leave both unset for now",
			where, repo, catalogSince, pluginwire.Printable(ref), rel)
	}
	return nil
}
