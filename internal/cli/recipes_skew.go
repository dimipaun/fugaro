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

	"github.com/dimipaun/fugaro/internal/backend/gcp"
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

// sharedSince is the first fugaro release whose runner clones for a
// checkout: shared job (design forge-authorship-images/shared-images.md §6,
// route A; config.CheckoutShared). Such a job runs a shared
// dependency-layer image, which holds no repository's source (§12), so its
// runner has to clone the source at launch exactly as a checkout: clone
// job's does — FUGARO_CHECKOUT=shared is the mark, and a runner that does
// not read it starts on an empty work directory with nothing to run.
// checkSharedImageSince below gates a launch on it: a shared image built
// from a release older than this one is refused, with the two commands that
// fix it, while one built from a base with no release tag (a dev or
// hand-pushed image) only warns, the way every other *Since constant here
// is judged. Unlike cloneSince it carries no shipped flag: refusing every
// image that predates the release is exactly the direction this gate wants,
// and a runner that does not clone for shared cannot run such a job at all.
//
// It names 0.14.0, the release this slice ships in, which is not tagged
// yet: the release that tags it holds this number to the tag it cuts, as it
// does for planTaskSince's 0.11.2 and digestSince's 0.13.0.
const sharedSince = "0.14.0"

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

// managedDeployerShipped is true as of 0.10.0, the release the owner's ruling
// of 2026-10-22 ships the managed deployer in: managed projects ship enabled.
// Until then it was false, because the deployer's scoped half could not be
// created — first as the 0.8.6 shape GCP rejected as written (one role, 17
// logical operators where IAM allows 12, resource.name.contains(), which is
// not a supported function, and resource.name on resource types that do not
// support it), and then because the redesign that replaced it had not been
// measured live. 0.8.6, 0.8.7, 0.9.0 and 0.9.5 shipped with the scoped half
// gated off; the ruling of 2026-10-18 ("Gate it off by default; redesign for
// 0.9.0") is why it was ever closed, and the rulings of 2026-10-19 and
// 2026-10-20 moved the redesign off 0.9.0 and off 0.9.5 (the latter placing it
// in 0.9.6 and then renumbering that release to 0.10.0 — one placement under
// two numbers, not two placements; cascadeSince's comment in this file and
// docs/release.md's bullet for it record the same renumbering, and 0.9.5 is
// the last release tagged, so no 0.9.6 was ever cut).
//
// WHAT THE FLIP DOES NOT SETTLE, and what a reader of this constant must not
// take it as: the fence has not been measured. The deployer's scoped half is
// seven per-service custom roles, each bound exactly once under its own
// condition (deploy/terraform/gcp/modules/installation/deployer.tf,
// docs/design/managed-deployer-fence.md), and no test can ask Google whether
// IAM accepts those seven bindings or honours resource.name per type — that is
// what Check 68 (docs/gcp-live-checklist.md) measures, and it is owner-run
// AFTER this release, by the same ruling. So 0.10.0 ships a fence it cannot
// yet prove: if IAM rejects a binding, the installation apply that creates it
// fails partway through; if IAM accepts one and does not honour resource.name,
// that role grants nothing (fail-closed, and no fence). The caveat is stated
// in the release note's "Known limits" and in docs/release.md's bullet for
// this constant, and Check 68's pass is what settles it.
//
// The installation module creates those roles and their bindings only with
// managed_deployer_scoped, and an installation that uses managed projects sets
// it: installRoot reads the project's creation fact — the same read init's
// guard refusal makes, and the one that survives a run that did not create the
// project — and writes the module's variable from it
// (infra.InstallOptions.ManagedDeployerScoped). So a run that creates a
// managed project (--create-project --managed) writes the scoped half, and
// every later run that reaches installRoot — `fugaro setup` and `fugaro
// update`, the two doors that plan and apply the installation root — writes it
// again from the same fact, so the variable cannot be dropped by a later run
// against the project. An installation that never asked for managed writes the
// same tfvars as before the variable existed.
//
// The owner's `fugaro managed flag` is deliberately not in that list: it
// writes the fact itself (the marker object and the project label) and applies
// no Terraform, so a project flagged after its installation was stood up gets
// the fence at the next `fugaro setup` or `fugaro update` of that
// installation, not in the flagging run.
//
// There is deliberately no managedDeployerOpen, no managedDisabled and no
// refuseManagedNotShipped beside it: an open gate nothing closes and a refusal
// nothing can reach are names a later reader re-opens, which is exactly what
// the pair existed to prevent (batchProvisionShipped below went the same way in
// 0.9.5).
const managedDeployerShipped = true

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
// exactly what the pair existed to prevent. The managed gate above kept its
// pair until 0.10.0, and 0.10.0 took it the same way: both gates are open now,
// and neither has a refusal left to name.
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

// digestSince is the first fugaro release whose runner executes the digest
// step: it writes the run reports the repository is missing and commits
// them, with no model call (recipe.UsesDigest). It gates two things, and a
// runner before it refuses both: the step, which the recipe itself has
// carried since 0.12.1 (T3.1) — a 0.12.1 runner knows the step kind and
// refuses the recipe at bootstrap rather than skipping the step ("does not
// execute yet") — and the digest_runs manifest a report run carries, which
// no runner before it knows at all: the task decode is strict, so the field
// is an unknown one it rejects. A digest launch against an older job image
// is refused at launch, by this gate, instead of failing the run at
// bootstrap — and, in the window between this recipe's release and the
// step's, an image gate is the only refusal a launcher can make: the step is
// the runner's own work, so no other release can run it.
//
// Like artifactsSince it carries no shipped flag: refusing every image that
// predates the step is exactly the direction this gate wants, and an image
// with no release tag (a dev build, a hand-pushed one) only warns, the same
// as for every other *Since constant here — safe either way, since a runner
// that does not execute the step refuses the recipe at bootstrap, and one
// that does runs the report run (internal/runner/digest.go), so a warning
// cannot let a digest run through as an ordinary implement run and open a
// pull request the recipe never asked for. It names 0.13.0, the release the
// report run ships in, which is not tagged yet: docs/release.md's checklist
// verifies the constant against the tag actually cut, the way
// planTaskSince's bullet records.
const digestSince = "0.13.0"

// adversarialSince is the first fugaro release whose runner acts on the
// adversarial step, review level 3 (D720): it runs the step after the senior
// review, a rejection bounces back to the first-line loop, and the level
// takes part in readiness (L4.2). A runner before it refuses the recipe
// outright, at parse: its version 1 step list has no adversarial kind, so the
// whole recipe is "is not a step type" (recipe.Parse, recipe.UsesAdversarial)
// and the run dies at bootstrap with the bytes already in the bucket. This
// gate is what turns that into a refusal at the launcher's terminal, naming
// fugaro image refresh, so an image that cannot run the step never accepts
// the launch at all.
//
// Like artifactsSince it carries no shipped flag: refusing every image that
// predates the step is exactly the direction this gate wants, and an image
// with no release tag (a dev build, a hand-pushed one) only warns, the same
// as for every other *Since constant here — safe either way, since a runner
// that cannot run the step refuses the recipe at bootstrap rather than
// skipping it and opening an ordinary pull request in its place. It names
// 0.15.0, the release L4.2 ships in: if L4.2 does not make 0.15.0 the
// constant moves with it before the tag, so no image ever accepts an
// adversarial launch and then refuses the recipe, and docs/release.md's
// checklist verifies it against the tag actually cut, the way digestSince's
// bullet records.
//
// A repository-sourced recipe is judged by this constant too, the step having
// no field of its own in the task: the task carries the recipe's name alone,
// so recipeSinceFor judges the file this checkout holds (repoRecipeForGate,
// the read recipeForLaunch shares) on the baked path and on the clone one
// alike — checkCloneRecipeImage takes the context that read needs. A launch
// whose file cannot be read here — no checkout, an unreadable or invalid one —
// stays on recipesSince, with the runner's parse refusal as the backstop,
// exactly as it is for a name this launcher cannot resolve at all.
const adversarialSince = "0.15.0"

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

// planTaskSince is the first fugaro release whose runner reads a task's
// plan_task field (task.Spec.PlanTask, fugaro run --plan-task): an older
// runner refuses the field outright, since task.Parse decodes with
// DisallowUnknownFields, and the run would fail at bootstrap on a task
// nobody can read. It must equal the release that ships
// task.Spec.PlanTask: docs/release.md ("Before you tag") has the release
// checklist verify it, and the same bullet records the operator order the
// field needs for the release note's "For operators" section (every job
// image a --plan-task launch can reach is rebuilt or repointed at this
// release first). internal/cli/run.go gates a launch that would set one
// against an older build record, exactly as it does for a title
// (titlesSince) and for a named Claude account (namedTokensSince); a
// launch with no --plan-task writes no such field and is never gated.
//
// It names 0.11.2 ("Camel two"), the release after 0.11.1 and the first
// that ships the field -- a number ahead of the tag, like cascadeSince's
// 0.10.0, and the checklist bullet above is what holds it to the version
// actually tagged.
const planTaskSince = "0.11.2"

// compressionSince is the first fugaro release whose runner reads a task's
// compression_off field (task.Spec.CompressionOff, fugaro run
// --no-compression): an older runner refuses the field outright, since
// task.Parse decodes with DisallowUnknownFields, and the run would fail at
// bootstrap on a task nobody can read. It must equal the release that ships
// task.Spec.CompressionOff, and docs/release.md ("Before you tag") is where
// that is verified: its bullet for this constant does not exist yet, because
// it is written with the release that cuts the feature -- Task 9 of
// docs/plans/2026-10-12-headroom-compression.md, whose Task 6 added this
// constant and this gate, the same way planTaskSince's bullet arrived with the
// release that shipped the plan task. internal/cli/run.go gates a launch
// against an older build record, exactly as it does for a plan task
// (planTaskSince) and for a title (titlesSince).
//
// It names 0.12.0 ("Dolphin": docs/release-streams.yaml's `next` CLI name),
// the release after 0.11.6 and the first that ships the field's runner half
// (the sidecar Task 4 merged) -- a number ahead of the tag, like
// planTaskSince's 0.11.2 ("Camel two"), and the checklist bullet is what will
// hold it to the version actually tagged.
const compressionSince = "0.12.0"

// compressionForTest, for tests only, stands in for the resolved
// agent.compression of the configuration a launch reads. It is the CLI's
// counterpart of the runner's Deps.CompressionForTest: config.Validate
// refuses agent.compression while the key is reserved (the plan's P11), so a
// committed fugaro.yaml cannot carry it and no flag sets it, and a test that
// means to exercise the launch-time gate has to stand the resolution up
// itself. Empty (the default, and every real binary) means no test is
// running. Nothing a repository or a launcher controls reaches it: it is not
// read from any file, environment variable or flag.
var compressionForTest string

// compressionRequested reports whether the run this launch is about turns
// context compression on: one half of what the launch's compressionSince gate
// is judged on (the task's own CompressionOff is the other, since a follow-up
// and a retry inherit it) and what --no-compression decides against. known is
// whether the launcher read the configuration at all: on is false both when
// the file was read and leaves compression off and when there is no file to
// read, and the two are not the same thing to the launch — the first is a
// --no-compression with nothing to turn off, the second one the launch cannot
// honour at all (finishLaunch).
//
// The configuration is read as rigorLaunchSources reads it -- at the ref, out
// of this checkout's object database, so a launch is judged on the file the
// run checks out rather than on the launcher's working tree, which the run
// never reads -- with the checkout's own document standing in when the ref
// cannot be read. A file the launcher cannot read at all is never gated on the
// key: the run's own bootstrap, which reads the file this launcher could not,
// is what refuses the key then. agent.compression is repository-scope and no
// project layer or profile may set it (docs/design/layered-config.md §5), so
// the resolved value is this file's own.
func compressionRequested(ctx context.Context, env *cloudEnv, spec *task.Spec) (on, known bool) {
	if compressionForTest != "" {
		return true, true
	}
	cfg := launchFileConfig(ctx, env, spec)
	if cfg == nil {
		return false, false
	}
	return cfg.Agent.Compression != "", true
}

// cascadeSince is the first fugaro release whose config reader accepts
// fugaro.yaml's agent.auth: cascade (docs/credentials-service, D133): before
// it the value is not one of config.AgentAuths, so a job built from an older
// base image refuses the file at bootstrap ("agent.auth: must be one of
// vertex, api-key, oauth") and the run dies before the agent starts.
//
// The judge is the installation's base image — the ref a job of the
// repository is built from (lc.BaseImage, config.BaseKind) — because that is
// where the reader lives: unlike the recipe and artifact gates there is no
// per-workflow build record for a checkout: clone or a derived image that
// could answer differently, and this value is read by the same config reader
// either way (refuseCascadeSkew). docs/release.md ("Before you tag") has the
// checklist verify it equals the release that ships the value.
//
// It names 0.10.0, which is not tagged yet, and that is the ruling rather than
// a slip: the owner's ruling of 2026-10-20 places the whole credentials
// service — this value included — in the next release, which is 0.10.0 (the
// line named 0.9.6 while the release was expected to ship as 0.9.6, and the
// owner renumbered it), so 0.10.0 is the release this value ships in and the
// one whose config reader accepts it. An installation whose base image is
// 0.9.5 or older is therefore refused, which is right: that
// runner's config.Validate does not know the value, so a cascade repository
// deployed against it would die at bootstrap, and `fugaro update --base base`
// — run by a binary of the release that carries the value — is the fix that
// clears the refusal. No `cascadeShipped`-style boolean belongs beside this
// constant, the shape catalogSince has: that one gates a value read by a
// release *later* than the binary that writes it, so no base image of any age
// could read it and the age check would be vacuous, while here the reader is
// this release's own config reader and the age check is the whole rule.
//
// TestCascadeSinceIsTheReleaseTheDocsAndSchemasName holds it to the number
// docs/release.md's bullet and the two schemas' agent.auth descriptions state,
// and TestCascadeSinceIsTheReleaseTheCredentialsServiceShipsIn holds it to the
// release the service ships in, so the constant cannot move without them.
const cascadeSince = "0.10.0"

// baseBranchSince is the first fugaro release whose runner reads a task's
// base_branch (the project's default base branch a launch resolves): an older
// runner refuses the whole task at bootstrap, since task.Parse decodes with
// DisallowUnknownFields. Unlike every other *Since gate here the launch is
// never refused over it — baseBranchSkew drops the field and says so — because
// what the field carries is a default: the run it would have targeted the
// project's base branch on runs on the installation's base instead, and a
// launch that works today must keep working. It must equal the release that
// ships the field; docs/release.md ("Before you tag") has the checklist
// verify it.
const baseBranchSince = "0.12.1"

// rigorSince is the first fugaro release whose config reader knows
// fugaro.yaml's rigor: block and the project layer's defaults.rigor
// (docs/rigor-modes). Both files are decoded with KnownFields, so a runner
// built from an older base image refuses either key at bootstrap; the project
// layer's key does it to every repository of the project at once. Like
// cascadeSince the judge is the installation's base image and the gate is
// fugaro update --repo's (refuseRigorSkew); fugaro run gates the launch too
// (checkRigorImage, checkCloneRigorImage), but only from a checkout of the
// repository, where it can read the file the run will, so outside one only
// fugaro update --repo checks. docs/release.md ("Before you tag") has the
// checklist verify it equals the release that ships the key.
const rigorSince = "0.11.0"

// rigorRequestSince is the first fugaro release whose runner reads a task's
// own rigor request (task.Spec.Rigor and task.Spec.Paths, fugaro run --mode
// and --path): an older runner refuses both fields outright, since
// task.Parse decodes with DisallowUnknownFields, and the run would fail at
// bootstrap on a task nobody can read. It must equal the release that ships
// task.Spec.Rigor and task.Spec.Paths: docs/release.md ("Before you tag")
// has the release checklist verify it.
//
// It is not rigorSince, which is older and answers a different question:
// that one names the release whose *config reader* knows fugaro.yaml's
// rigor: block, judged against the installation's base image by fugaro
// update --repo. This one names the release whose *task reader* knows the
// two fields the flags write, and it is judged against the job's own build
// record by the launch, exactly as titlesSince is. The gate is the field's,
// not the flag's: a launch that asks for nothing writes neither field (both
// are omitempty) and its spec is byte-identical to what the previous release
// wrote, so it is never gated — but a follow-up that passed no flag still
// carries a mode when its PR recorded one (T5.2, rigorGateSubject).
const rigorRequestSince = "0.11.1"

// governanceSince is the first fugaro release whose config reader knows
// fugaro.yaml's governance: block and the project layer's governance: block
// (docs/task-governance/spec.md). Both files are decoded with KnownFields, so
// a runner built from an older base image refuses either key at bootstrap; the
// project layer's key does it to every repository of the project at once.
// Like rigorSince the judge is the installation's base image and the gate is
// fugaro update --repo's (refuseGovernanceSkew); fugaro run gates the launch
// too (checkGovernanceImage, checkCloneGovernanceImage), but only from a
// checkout of the repository, where it can read the file the run will, so
// outside one only fugaro update --repo checks. docs/release.md ("Before you
// tag") has the checklist verify it equals the release that ships the key.
//
// It is also the release that ships the expectation tier's task field
// (task.Spec.Tier, fugaro run --tier, plan T8.1): the tier's expectation is
// read from that same governance: block, so the field and the key arrive
// together, and a runner older than it refuses a task carrying a tier the way
// task.Parse's DisallowUnknownFields refuses the key. The launch judges that
// field too (tierGateSubject), on the same image.
const governanceSince = "0.13.0"

// authorSince is the first fugaro release whose runner reads a task's author
// field (task.Spec.Author, the launcher's identity a launch records for the
// repositories that opted in, D604/D606): an older runner refuses the field
// outright, since task.Parse decodes with DisallowUnknownFields, and the run
// would fail at bootstrap on a task nobody can read. It must equal the release
// that ships task.Spec.Author: docs/release.md ("Before you tag") has the
// release checklist verify it.
//
// The launch is never refused over this field: authorSkew drops it and says so
// (baseBranchSkew's shape), because what it carries is an attribution — the run
// it is dropped for does the same work and commits it under the identity it
// would have had before the field existed (D605). What is refused is the key
// the repository opted in with (gitAuthorSince): its holder asked for the
// launcher's identity, and is told when the job image cannot be shown to honor
// the opt-in rather than have it silently ignored.
const authorSince = "0.14.0"

// gitAuthorSince is the first fugaro release whose config reader knows
// fugaro.yaml's git.author (config.GitAuthorLauncher, D606: a repository opts
// in to its launcher's identity). The file is decoded with KnownFields, so a
// runner built from an older base image refuses the key at bootstrap, before
// the agent starts and after the launch was paid for. Like rigorSince and
// governanceSince the judge is the image that runs the run: the installation's
// base image for a checkout: clone workflow (its job runs no derived image of
// its own), the base a workflow's build record was built FROM for a derived
// one. The gate is fugaro update --repo's (refuseGitAuthorSkew, judged on the
// installation's base image before any cloud effect) and the launch's
// (checkGitAuthorImage, checkCloneGitAuthorImage), which runs only where the
// file the run will read can be read here at all (launchFileConfig).
//
// It names the same release as authorSince and is still its own constant:
// the two answer different questions about one release — a config key's reader
// and a task field's — and the message each produces names what that reader
// has to know. rigorSince and rigorRequestSince are the same pair of questions
// answered by two different releases, which is why they carry two numbers.
// docs/release.md ("Before you tag") has the checklist verify both.
const gitAuthorSince = "0.14.0"

// asksForRigor reports whether a launch asks for a rigor mode or declares a
// path: the two task fields a runner older than rigorRequestSince refuses
// outright, and so the condition the launch's own gate is written on. A
// launch that asks for nothing carries neither field, and a follow-up
// inherits a mode rather than asking for one (T5.2): both readings are the
// spec's, which is the only thing the job reads.
func asksForRigor(spec *task.Spec) bool {
	return spec.Rigor != "" || len(spec.Paths) > 0
}

// rigorGateSubject is what the rigorRequestSince gate calls the field it
// judges. A launch that passed --mode or --path is described by them; one that
// passed neither and still carries a mode keeps it from the PR it follows
// (T5.2), and naming flags nobody gave would send its reader looking for a
// mistake they did not make — the request is real all the same, and a runner
// older than rigorRequestSince refuses the whole task for it.
//
// The mode is what makes a request kept rather than asked for, so a follow-up
// that passed no flag and carries paths alone — a stored task, launched again
// by --retry — is described by the flags: it carries no mode to name, and
// "(rigor: )" would report the field's absence as the thing the gate judged.
func rigorGateSubject(o *runOptions, spec *task.Spec) string {
	if o.rigor == "" && len(o.paths) == 0 && spec.IsFollowUp() && spec.Rigor != "" {
		return fmt.Sprintf("the mode a follow-up keeps from its PR (rigor: %s)", spec.Rigor)
	}
	return "the run's own rigor request (--mode, --path)"
}

// tierGateSubject is what the governanceSince gate calls the tier it judges. A
// launch that passed --tier is described by it; one that passed nothing and
// still carries a tier keeps it from the PR it follows, and naming a flag
// nobody gave would send its reader looking for a mistake they did not make —
// the tier is real all the same, and a runner older than governanceSince
// refuses the whole task for it. It is rigorGateSubject's shape for the same
// reason: the check is the field's, not the flag's.
func tierGateSubject(o *runOptions, spec *task.Spec) string {
	if o.tier == "" && spec.IsFollowUp() && spec.Tier != "" {
		return fmt.Sprintf("the tier a follow-up keeps from its PR (tier: %s)", spec.Tier)
	}
	return "the run's own tier request (--tier)"
}

// needsRecipeImage reports whether a launch needs a runner that knows
// recipes: the task carries one, or the checkout's fugaro.yaml sets
// agent.recipe (an unknown key to an older runner).
func needsRecipeImage(spec *task.Spec, checkoutRecipe string) bool {
	return spec.Recipe != nil || checkoutRecipe != ""
}

// embeddedRecipeOf is the recipe spec.Recipe's exact YAML parses to, or nil
// when there is none to parse: a repository-sourced recipe carries only its
// name (the runner re-reads the file at the ref), and a bare agent.recipe
// name is not resolved here either. A repository recipe is judged from the
// launcher's own read of its file (repoRecipeForGate); a bare agent.recipe
// name still gets the plain recipesSince gate, unless the task carries a
// report run's manifest, which is a gate of its own whatever the recipe's
// source (recipeSinceFor); whether that one uses a 0.7.0 construct is for the
// run itself to find out, the same way an older runner already refuses an
// unknown key.
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
// recipesSince (decision D2), or than the newer constant the task's own
// recipe — its embedded text, or the manifest it carries — needs
// (recipeSinceFor). The judge is the build record's base_ref: the image runs
// the binary of the base it was built FROM. A non-release base is not judged
// (one warning); no record, or one without base_ref, is refused. kind is the
// workflow's base kind, "" when unknown.
func checkRecipeImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	subject, alt := "fugaro.yaml's agent.recipe", ""
	if spec.Recipe != nil {
		subject = "recipe " + spec.Recipe.Name
		if spec.Recipe.Name != recipe.DefaultName { // a carried default cannot be the way out
			alt = "; or launch with --recipe default to run today's loop"
		}
	}
	return checkImageSince(ctx, env, slug, spec, kind, recipeSinceFor(spec, repoRecipeForGate(ctx, spec)), subject, "recipes", alt, warn)
}

// repoRecipeForGate is a repository-sourced task's recipe, read from this
// checkout the way recipeForLaunch reads it — the working tree, the
// launcher's own approximation of the ref the runner checks out. Only
// recipeSinceFor's adversarial arm reads it: that step is the one construct
// whose gate the task's own fields cannot carry (see recipeSinceFor). nil for
// every other launch, and for one whose file cannot be read here, which keeps
// the plain recipesSince it always had; the runner's own parse refusal is the
// backstop there, exactly as it is for a name this launcher cannot resolve at
// all.
func repoRecipeForGate(ctx context.Context, spec *task.Spec) *recipe.Recipe {
	if spec.Recipe == nil || spec.Recipe.YAML != "" {
		return nil
	}
	return repoRecipeFrom(spec, checkoutRoot(ctx, spec.Repo))
}

// recipeSinceFor is the release the recipe's own constructs demand, the
// newest of the gates its text trips (design generic-tool.md §3.1): the
// task's embedded text when it carries one, else rp, the launcher's own read
// of a repository recipe's file (repoRecipeForGate). A bare agent.recipe
// name, which no file here resolves, keeps recipesSince, the same way an
// older runner refuses its unknown keys at the run.
//
// A task carrying digest_runs is judged by the manifest itself, whatever its
// recipe's source: the field is what says the run executes the digest step
// (task.Validate accepts it under no other recipe), and for a repository
// recipe it is the only thing in the task that says so — the recipe's own
// text is read at the ref, where this launcher cannot look. Without this the
// launch of a repository's own report recipe would be judged by recipesSince
// and start a job whose runner refuses the whole task at bootstrap, the
// manifest being a field it does not know.
//
// The adversarial step is judged from rp too, and it is the newest construct
// any recipe can carry (adversarialSince), so its case comes first. Unlike
// the manifest and mode: artifact — a repository recipe's digest run and its
// mode both reach the task as fields, which carry their own gates above —
// the step has nothing in the task behind it, so the launcher's read of the
// file the recipe names is the only place the gate can see it; one this
// launcher cannot read keeps recipesSince, with the runner's parse refusal as
// the backstop. Every older construct keeps the gate it always had, judged
// from the embedded text alone: a repository recipe trips none of them here,
// which is what its own tests pin.
func recipeSinceFor(spec *task.Spec, rp *recipe.Recipe) string {
	r := embeddedRecipeOf(spec)
	if len(spec.DigestRuns) == 0 && r == nil && rp != nil && recipe.UsesAdversarial(rp) {
		return adversarialSince
	}
	switch {
	case len(spec.DigestRuns) > 0:
		return digestSince
	case r == nil:
		return recipesSince
	case recipe.UsesAdversarial(r):
		return adversarialSince
	case recipe.UsesDigest(r):
		// The digest step is 0.13.0's, newer than every other construct
		// here, so it is the binding gate; no mode: artifact construct can
		// come with it, since the step is only legal in mode: implement.
		return digestSince
	case recipe.UsesV08(r):
		// UsesV08 implies UsesV07 (a 0.8.0 construct always comes with a
		// 0.7.0 one: mode is one), and artifactsSince is the newer of the
		// two, so it is the binding gate.
		return artifactsSince
	case recipe.UsesV07(r):
		return recipesV07Since
	}
	return recipesSince
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

// baseBranchSkew drops spec.BaseBranch when the job's image runs a fugaro
// older than baseBranchSince, and says so on warn: such a runner refuses the
// whole task at bootstrap (task.Parse decodes with DisallowUnknownFields), so
// carrying the field would turn a launch that works today into a run that dies
// after it was paid for. The launch is never refused — the project's base
// branch is a default, and the run it is dropped for targets the installation's
// base branch instead, which is what every launch did before this field — and
// the field's own absence keeps a launch that resolved no project default (the
// common case) byte-identical to the previous release's.
//
// The judge is the one the other gates use: a checkout: clone job runs the
// installation's base image itself (checkCloneImageSince's judge, the ref
// base_images.base names), and a derived image runs the binary of the base its
// build record was built FROM (checkImageSince's). clone is that workflow's
// checkout mode, nil when the launch could not read it — a launch for another
// repository from a checkout that is not its own, where checkoutConfig answers
// nil. Neither judge is then known to be the job's, so both are asked and
// either one predating drops the field: a clone workflow has no build record
// at all (it is never built, so the other judge would find nothing and keep a
// field the image cannot read), while a derived one is refused by
// checkImageSince before the task is written when its record is missing. An
// image neither judge can show — a base with no release tag, a record without
// a base_ref, a record that cannot be read — is left carrying the field: what
// the image reads cannot be shown, and the alternative, dropping a project
// default on a development image whose runner does read it, is a silent change
// of the branch the run targets, which nothing in the run reports. The read's
// own failure is dropped for the same reason: this gate's only power is to
// omit a field, and a launch is not worth refusing over a database read that
// the launch's other gates make anyway.
func baseBranchSkew(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, clone *bool, warn io.Writer) {
	b := spec.BaseBranch
	if b == "" {
		return
	}
	drop := func() {
		spec.BaseBranch = ""
		fmt.Fprintf(warn, "the job image predates %s and ignores the project's base branch %s; refresh it with fugaro image refresh\n", baseBranchSince, b)
	}
	predates := func(rel string, known bool) bool { return known && imagePredates(rel, baseBranchSince) }
	if clone == nil {
		rel, known := releaseOf(env.lc.BaseImage(config.BaseKind))
		if predates(rel, known) {
			drop()
			return
		}
		if rel, known = recordBaseRelease(ctx, env, slug, spec); predates(rel, known) {
			drop()
		}
		return
	}
	if *clone {
		if rel, known := releaseOf(env.lc.BaseImage(config.BaseKind)); predates(rel, known) {
			drop()
		}
		return
	}
	if rel, known := recordBaseRelease(ctx, env, slug, spec); predates(rel, known) {
		drop()
	}
}

// recordBaseRelease is the release of the base image the build record of slug
// workflow names, and whether a record said so at all.
func recordBaseRelease(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) (string, bool) {
	b, err := env.recordBucket(ctx)
	if err != nil {
		return "", false
	}
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, spec.Workflow))
	if err != nil {
		return "", false
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil || rec.BaseRef == "" {
		return "", false
	}
	return releaseOf(rec.BaseRef)
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

// checkSharedImageSince refuses a launch of a checkout: shared workflow
// whose job runs a shared dependency-layer image built from a base older
// than since: a runner before that release does not clone for shared, and
// the image it would run holds no source (design
// forge-authorship-images/shared-images.md §6, §12). Two remedies, in the
// order they work: the installation's base image moves to a current release
// (fugaro update --base base), and the repository's own refresh then builds
// and places the shared image the new key needs (fugaro image refresh) —
// the base is one of the key's inputs (§2.1), so a newer base is a new key
// and so a new image.
//
// The judge is the lock (§3.1), which names per image the repositories on
// it and each one's job: it is what tells the launcher which image this
// workflow's job points at without computing the key, which only the
// operator's machine does (§2.5, D622). A repository the lock holds no
// entry for is not judged: it has not migrated yet (§10), so its job is
// still on a per-(repository, workflow) image whose runner this gate has
// nothing to say about.
func checkSharedImageSince(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, since string, warn io.Writer) error {
	const subject, knows = "this checkout: shared workflow", "how to clone for a shared image (FUGARO_CHECKOUT)"
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the shared image of %s workflow %s %s. "+
			"Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, then fugaro image refresh --repo %s --workflow %s in its checkout to build the shared image the repository's new dependency key needs and place it on this job",
			subject, knows, since, spec.Repo, spec.Workflow, why, spec.Repo, spec.Workflow)
	}
	b, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	// ReadStrict, not Read: Read folds an HTTP 403 into ErrNotExist, and an
	// absent lock means "this repository has not migrated", which this gate
	// permits — so a read the launcher is not allowed to make would disable
	// the gate silently rather than fail it. The launcher holds read on the
	// runs bucket, so a 403 here means no access, and it is a remote error
	// like any other (imagecheck/lock.go's LockWriter has the same reasoning
	// for the lock's write).
	data, _, err := b.ReadStrict(ctx, imagecheck.LockKey)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil
	case err != nil:
		return remote(fmt.Errorf("reading the image lock for %s workflow %s: %w", spec.Repo, spec.Workflow, err))
	}
	lock, err := imagecheck.ParseLock(data)
	if err != nil {
		// The lock is decoded strictly so that a reader which does not know
		// the object stops and says so (§3.2); a launch is such a reader, and
		// judging nothing would launch onto the wrong binary.
		return refuse("cannot be judged (" + oneLineCLI(err.Error()) + ")")
	}
	// The job a launch starts, not the workflow: a named Claude account gets
	// its own job (jobWorkflow), and the lock records the job names that
	// exist, so matching on the bare workflow would miss every one of them.
	variant, ok := jobWorkflow(spec.Workflow, spec.OAuthToken)
	if !ok {
		return nil
	}
	img, ok := sharedImageOf(lock, spec.Repo, gcp.JobName(slug, variant))
	if !ok {
		return nil
	}
	ref := pluginwire.Printable(img.BaseRef)
	switch rel, ok := releaseOf(img.BaseRef); {
	case !ok:
		fmt.Fprintf(warn, "warning: the shared image of %s workflow %s was built from %s, which is not a release base image; whether its runner knows %s is not checked\n",
			spec.Repo, spec.Workflow, ref, knows)
	case imagePredates(rel, since):
		return refuse(fmt.Sprintf("was built from base image %s, release %s", ref, rel))
	}
	return nil
}

// sharedImageOf is the lock's image that repository's job points at, and
// whether the lock holds one: the entry naming both, since a repository may
// be on one shared image per workflow.
func sharedImageOf(lock *imagecheck.Lock, repo, job string) (imagecheck.LockedImage, bool) {
	for _, key := range slices.Sorted(maps.Keys(lock.Images)) {
		img := lock.Images[key]
		if img.Jobs[repo] == job && slices.Contains(img.Repositories, repo) {
			return img, true
		}
	}
	return imagecheck.LockedImage{}, false
}

// checkCloneRecipeImage is checkRecipeImage for a checkout: clone workflow
// (recipesSince, or the newer constant the recipe's constructs need): see
// checkCloneImageSince. It reads a repository recipe's own file from this
// checkout like its derived-image twin, so a checkout: clone launch is
// judged on the recipe the run will read rather than on the name alone.
func checkCloneRecipeImage(ctx context.Context, lc *localcfg.Config, spec *task.Spec, warn io.Writer) error {
	subject, alt := "fugaro.yaml's agent.recipe", ""
	if spec.Recipe != nil {
		subject = "recipe " + spec.Recipe.Name
		if spec.Recipe.Name != recipe.DefaultName { // a carried default cannot be the way out
			alt = "; or launch with --recipe default to run today's loop"
		}
	}
	return checkCloneImageSince(lc, spec, recipeSinceFor(spec, repoRecipeForGate(ctx, spec)), subject, "recipes", alt, warn)
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

// refuseCascadeSkew refuses fugaro update --repo, before any cloud effect, when
// the cascade is in effect and the installation's base image predates the
// release whose config reader accepts the value: the job would be deployed,
// then die at bootstrap on a file the image refuses.
//
// auth is the resolved agent.auth, src the layer it came from
// (config.Resolution.SourceOf("agent.auth"), which is what the subject names:
// see cascadeSubject), and ref is the installation's base image
// (lc.BaseImage(config.BaseKind)); anything but cascade passes — this is the
// one auth the value is new for, and every other one is read by every release.
// A ref that is not a release base image is only warned about (nothing can
// tell what such a runner knows), and no base image at all passes, since the
// job is then built from this release's own image, which reads it — both the
// reading checkCloneImageSince gives the same states.
func refuseCascadeSkew(repo, auth, src, ref string, warn io.Writer) error {
	if auth != config.AuthCascade {
		return nil
	}
	subject := cascadeSubject(repo, src)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the value is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, cascadeSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner refuses the file at bootstrap, because %s is not one of the agent.auth values it knows. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or run this repository with agent.auth: api-key",
			subject, cascadeSince, pluginwire.Printable(ref), rel, config.AuthCascade)
	}
	return nil
}

// cascadeSubject names, for the two messages above, what put the cascade in
// effect: the repository's own fugaro.yaml, or the project layer's defaults.
// The resolved value is not proof that the file asked for it — agent.auth
// resolves like every other key, the repository's value else the workflow's
// profile's else the layer's defaults (config.Resolve) — so a subject that
// always said "fugaro.yaml's" would tell the operator of a layered
// installation to edit a line their file does not have, and the fix differs:
// the layer's default is what has to change, or an agent.auth line the
// repository adds, which wins over it. The same care annotate() takes with a
// problem's path and doctor's oauth-token-allowed check takes with the auth
// it names.
//
// src is Resolution.SourceOf("agent.auth"). A source this cannot name is
// named rather than guessed at, so a value that reaches the config some new
// way is still never attributed to a file that does not carry it.
func cascadeSubject(repo, src string) string {
	switch src {
	case config.SourceRepo:
		return "fugaro.yaml's agent.auth: " + config.AuthCascade + " of " + repo
	case config.SourceProject:
		return "the project layer's defaults.agent.auth: " + config.AuthCascade + " of " + repo
	}
	return repo + "'s agent.auth: " + config.AuthCascade + " (set by " + pluginwire.Printable(src) + ")"
}

// refuseRigorSkew refuses fugaro update --repo, before any cloud effect, when
// the repository's fugaro.yaml writes a rigor: block (fromRepo) or the project
// layer it resolves over writes defaults.rigor (fromLayer) and the
// installation's base image predates rigorSince. The states of ref are read as
// refuseCascadeSkew reads them: no base image passes, one that is not a
// release only warns.
func refuseRigorSkew(repo string, fromRepo, fromLayer bool, ref string, warn io.Writer) error {
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := rigorSubject(repo, fromRepo, fromLayer)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, rigorSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, rigorSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// rigorSubject names, for refuseRigorSkew's messages, which file wrote the
// key, and the way out that file's author has: the repository's block is the
// repository's to remove, while the layer's is read by every repository of
// the project.
func rigorSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const layerAlt = "remove defaults.rigor from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's rigor: block and the project layer's defaults.rigor of " + repo,
			"remove the rigor: block from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's defaults.rigor (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's rigor: block of " + repo, "remove the rigor: block from fugaro.yaml"
}

// rigorKnows names, for the image gate's messages, the key the job image's
// runner has to know: the message reads "needs a job image whose runner knows
// <this>", so it has to name whichever of the two the config wrote.
func rigorKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the rigor: block and the project layer's defaults.rigor"
	case fromLayer:
		return "the project layer's defaults.rigor"
	}
	return "the rigor: block"
}

// checkRigorImage and checkCloneRigorImage are the launch's own rigorSince
// gate, for a workflow whose job image is a derived one and for a checkout:
// clone workflow respectively: the pair every other per-workflow image gate
// has (checkImageSince and checkCloneImageSince), judged on the key the
// configuration the run will read writes (rigorLaunchSources). The refusal
// names the fix that kind of image has — fugaro image refresh for a built
// image, fugaro update --base base for the installation's own — and the way
// out the file's author has, from rigorSubject.
//
// The image is the judge because that is where the reader is: a runner older
// than rigorSince decodes fugaro.yaml and the project layer with KnownFields,
// so the key alone kills the run at bootstrap, before the agent starts and
// after the launch was paid for.
func checkRigorImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	fromRepo, fromLayer := rigorLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := rigorSubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, rigorSince, subject, rigorKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkCloneRigorImage is checkRigorImage for a checkout: clone workflow: see
// checkCloneImageSince.
func checkCloneRigorImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	fromRepo, fromLayer := rigorLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := rigorSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(env.lc, spec, rigorSince, subject, rigorKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// rigorLaunchSources reports, for the launch's rigorSince gate, whether the
// configuration the run will read writes the key: the repository's fugaro.yaml
// (fromRepo) or the project layer it resolves over (fromLayer).
//
// The file is read as cascadeLaunchRefusal reads it — at the ref, out of this
// checkout's object database, so a launch is judged on the file the run checks
// out rather than on the launcher's working tree, which the run never reads —
// with the checkout's own document standing in when the ref cannot be read (no
// checkout here, or a ref that does not resolve), the fallback the auth read
// has always had. The layer is the one that file resolves over: ResolveConfig
// applies a layer only to a file that names its project and GCP project, so a
// file that cannot be read is a run that reads no defaults.rigor either, and
// such a launch is not gated — its bootstrap, which reads the file this
// launcher could not, is what refuses the key then.
func rigorLaunchSources(ctx context.Context, env *cloudEnv, spec *task.Spec) (fromRepo, fromLayer bool) {
	cfg := launchFileConfig(ctx, env, spec)
	if cfg == nil {
		return false, false
	}
	return cfg.RepoRigorSet, cfg.Layer.SetsRigor()
}

// launchFileConfig is the configuration a launch reads the file the run will
// check out for — the config-key image gates (rigorLaunchSources,
// governanceLaunchSources) and compressionRequested: from cascadeRefConfig,
// with the launcher's own checkout standing in when the ref cannot be read.
func launchFileConfig(ctx context.Context, env *cloudEnv, spec *task.Spec) *config.Config {
	cfg := cascadeRefConfig(ctx, env, spec)
	if cfg == nil {
		cfg = checkoutConfig(ctx, spec.Repo)
	}
	return cfg
}

// rigorImageWarnings warns, once, that a configuration which writes the key —
// the repository's file (RepoRigorSet) or the project layer it resolved over
// (SetsRigor) — needs every job image on rigorSince. It is validate's half of
// checkRigorImage, and a warning rather than a Problem for the reason
// usesProfiles' profile warning is one: the file is valid, and what has to move
// is the installation (the base image, or a workflow's derived one), which is
// why both ways to move an image are named. It says which of the two wrote the
// key, the same distinction rigorSubject draws for the launch gates, because
// the layer's key reaches every repository of the project and only its operator
// can remove it.
func rigorImageWarnings(c *config.Config) []config.Problem {
	fromRepo, fromLayer := c.RepoRigorSet, c.Layer.SetsRigor()
	if !fromRepo && !fromLayer {
		return nil
	}
	subject := "this file's rigor: block"
	switch {
	case fromRepo && fromLayer:
		subject = "this file's rigor: block and the project layer's defaults.rigor"
	case fromLayer:
		subject = "the project layer's defaults.rigor"
	}
	return []config.Problem{{Path: "rigor", Message: subject + " needs fugaro " + rigorSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseGovernanceSkew refuses fugaro update --repo, before any cloud effect,
// when the repository's fugaro.yaml writes a governance: block (fromRepo) or
// the project layer it resolves over writes governance: (fromLayer) and the
// installation's base image predates governanceSince. The states of ref are
// read as refuseRigorSkew reads them: no base image passes, one that is not a
// release only warns.
func refuseGovernanceSkew(repo string, fromRepo, fromLayer bool, ref string, warn io.Writer) error {
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := governanceSubject(repo, fromRepo, fromLayer)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, governanceSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, governanceSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// governanceSubject names, for refuseGovernanceSkew's messages, which file
// wrote the key, and the way out that file's author has: the repository's
// block is the repository's to remove, while the layer's is read by every
// repository of the project. The layer's key is a top-level key of the layer
// file, like the repository's own, rather than a defaults.rigor.
func governanceSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const layerAlt = "remove the governance: block from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's governance: block and the project layer's governance: block of " + repo,
			"remove the governance: block from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's governance: block (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's governance: block of " + repo, "remove the governance: block from fugaro.yaml"
}

// governanceKnows names, for the image gate's messages, the key the job
// image's runner has to know: the message reads "needs a job image whose
// runner knows <this>", so it has to name whichever of the two the config
// wrote.
func governanceKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the governance: block and the project layer's governance: block"
	case fromLayer:
		return "the project layer's governance: block"
	}
	return "the governance: block"
}

// checkGovernanceImage and checkCloneGovernanceImage are the launch's own
// governanceSince gate, for a workflow whose job image is a derived one and
// for a checkout: clone workflow respectively, the pair every other
// per-workflow image gate has (checkImageSince and checkCloneImageSince),
// judged on the key the configuration the run will read writes
// (governanceLaunchSources). The refusal names the fix that kind of image has
// — fugaro image refresh for a built image, fugaro update --base base for the
// installation's own — and the way out the file's author has, from
// governanceSubject.
//
// The image is the judge because that is where the reader is: a runner older
// than governanceSince decodes fugaro.yaml and the project layer with
// KnownFields, so the key alone kills the run at bootstrap, before the agent
// starts and after the launch was paid for.
//
// The same judge answers the second question the same image raises: a runner
// new enough to decode the key may still be a core build, which reads the
// block and enforces nothing in it, and the launch says so (warnCoreJobImage,
// after the refusal above so a refusal is never printed beside a warning).
// That half is judged on the key the configuration writes OR on the run's own
// tier, which a core runner ignores the same way (D522) — the tier is a task
// field and not a config key, which is why it is read here rather than in
// run.go's own tier gate.
func checkGovernanceImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	fromRepo, fromLayer := governanceLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer && spec.Tier == "" {
		return nil
	}
	if fromRepo || fromLayer {
		subject, alt := governanceSubject(spec.Repo, fromRepo, fromLayer)
		if err := checkImageSince(ctx, env, slug, spec, kind, governanceSince, subject, governanceKnows(fromRepo, fromLayer), "; or "+alt, warn); err != nil {
			return err
		}
	}
	warnCoreJobImage(ctx, env, slug, spec, fromRepo, fromLayer, warn)
	return nil
}

// checkCloneGovernanceImage is checkGovernanceImage for a checkout: clone
// workflow: see checkCloneImageSince. It has that gate's skew half and no
// edition half: the edition is a field of the workflow's build record, a clone
// workflow is never built and so has none (checkCloneImageSince's own comment,
// and baseBranchSkew reaches the same conclusion about the same image), and a
// record left under its key by an earlier life as a base-kind workflow
// describes an image this job does not run. What it runs is the installation's
// base image itself, whose edition no record carries — it is built and
// published by the release, not rendered into one — so it is unknown, and an
// unknown image warns about nothing (design ee-boundary-moves §7). That
// image's own build is where the tag lands (§8: images/base/Dockerfile is
// built with -tags ee), which is the answer for this shape of job.
func checkCloneGovernanceImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	fromRepo, fromLayer := governanceLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := governanceSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(env.lc, spec, governanceSince, subject, governanceKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// warnCoreJobImage warns when the build record of slug's workflow reports a
// core build and this launch asks for something such a build ignores: the
// governance: block the configuration writes (fromRepo/fromLayer), or the
// run's own tier. A core runner decodes both and enforces neither (D522), so
// the launch is never refused over this — the sentence says what the image
// does with what it was given, and never that anything is licensed (D523).
//
// Only a record that says core warns: a record with no edition, an empty one,
// or none at all is unknown, and an unknown image gets no warning, which is
// every image built before the field existed and every --image-source mirror
// (design §7). The read is the one checkImageSinceDev makes, of the same
// object and through the same bucket, and a failure to read is dropped: this
// warning's only power is to say something, and every gate that must hold has
// held by the time it is called.
//
// An image the gates refuse is not warned about either: a warning describes
// what a run would do with what it was given, and a refused launch has no run.
// Both this gate's skew half and run.go's own tier gate judge the record's
// base release against governanceSince, and a record that cannot say which
// base it was built from is refused by both, so a record that predates the
// release or names no base is left to the refusal.
func warnCoreJobImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, fromRepo, fromLayer bool, warn io.Writer) {
	if !fromRepo && !fromLayer && spec.Tier == "" {
		return
	}
	b, err := env.recordBucket(ctx)
	if err != nil {
		return
	}
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, spec.Workflow))
	if err != nil {
		return
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil || rec.Edition != EditionCore || rec.BaseRef == "" {
		return
	}
	if rel, known := releaseOf(rec.BaseRef); known && imagePredates(rel, governanceSince) {
		return
	}
	var asks []string
	if fromRepo || fromLayer {
		asks = append(asks, "the governance: block")
	}
	if spec.Tier != "" {
		asks = append(asks, "the run's tier")
	}
	fmt.Fprintf(warn, "warning: the job image of %s workflow %s is a core build of fugaro: it reads %s and ignores it, so nothing it sets is enforced by this run. Run fugaro image refresh --repo %s --workflow %s in its checkout, in your own terminal window, to have it enforced\n",
		spec.Repo, spec.Workflow, strings.Join(asks, " and "), spec.Repo, spec.Workflow)
}

// governanceImageWarnings is validate's half of checkGovernanceImage, and has
// that gate's two halves. The first warns, once, that a configuration which
// writes the key — the repository's file (RepoGovernanceSet) or the project
// layer it resolved over (SetsGovernance) — needs every job image on
// governanceSince. It is a warning rather than a Problem for the reason
// rigorImageWarnings is one: the file is valid, and what has to move is the
// installation (the base image, or a workflow's derived one), which is why both
// ways to move an image are named. The subject says which of the two wrote the
// key, and names the layer's block the way governanceSubject does — a top-level
// governance:, not a defaults.governance — because the layer's key reaches
// every repository of the project and only its operator can remove it.
//
// The second warns for each of the file's workflows whose job image's build
// record says it is a core build: such a runner reads the block and enforces
// nothing in it (D522), so the file is valid and the run it is launched with
// would simply ignore what the operator set. editionOf answers a workflow's job
// image edition and whether a record said so at all — an image no record speaks
// for is unknown and warns about nothing (design ee-boundary-moves §7) — and
// note is the sentence to print when there is no editionOf to ask ("" when
// there is one). A caller that could not ask gets a note rather than silence,
// so a file whose block needs enforcing is not left looking fine by a command
// that never looked; the first half prints either way, since it is a statement
// about the file. Only the workflows a record can speak for are asked at all:
// governanceEditionWorkflows.
func governanceImageWarnings(c *config.Config, editionOf func(workflow string) (string, bool), note string) (warnings, notes []config.Problem) {
	fromRepo, fromLayer := c.RepoGovernanceSet, c.Layer.SetsGovernance()
	if !fromRepo && !fromLayer {
		return nil, nil
	}
	subject := "this file's governance: block"
	switch {
	case fromRepo && fromLayer:
		subject = "this file's governance: block and the project layer's governance: block"
	case fromLayer:
		subject = "the project layer's governance: block"
	}
	warnings = append(warnings, config.Problem{Path: "governance", Message: subject + " needs fugaro " + governanceSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"})
	if editionOf == nil {
		if note != "" {
			notes = append(notes, config.Problem{Path: "governance", Message: note})
		}
		return warnings, notes
	}
	for _, name := range governanceEditionWorkflows(c) {
		if edition, known := editionOf(name); known && edition == EditionCore {
			warnings = append(warnings, config.Problem{Path: "workflow " + name, Message: "is a core build of fugaro: the job image of workflow " + name + " reads the governance: block and ignores it, so nothing it sets is enforced by a run of this repository. Run fugaro image refresh in that workflow's checkout to have it enforced"})
		}
	}
	return warnings, notes
}

// governanceEditionWorkflows are the file's workflows whose job image a build
// record can speak for. A checkout: clone workflow is not one of them: it is
// never built, so it has no record of its own, and what its job runs is the
// installation's base image, which no record describes (design
// ee-boundary-moves §7). A record left under its key by an earlier life as a
// base-kind workflow describes an image the job does not run, so reading it
// would warn about the wrong image rather than about none.
func governanceEditionWorkflows(c *config.Config) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(c.Workflows)) {
		if c.Workflows[name].Checkout != config.CheckoutClone {
			out = append(out, name)
		}
	}
	return out
}

// governanceLaunchSources reports, for the launch's governanceSince gate,
// whether the configuration the run will read writes the key: the
// repository's fugaro.yaml (fromRepo) or the project layer it resolves over
// (fromLayer), read the way rigorLaunchSources reads them.
func governanceLaunchSources(ctx context.Context, env *cloudEnv, spec *task.Spec) (fromRepo, fromLayer bool) {
	cfg := launchFileConfig(ctx, env, spec)
	if cfg == nil {
		return false, false
	}
	return cfg.RepoGovernanceSet, cfg.Layer.SetsGovernance()
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

// checkGitAuthorImage and checkCloneGitAuthorImage are the launch's own
// gitAuthorSince gate, for a workflow whose job image is a derived one and for
// a checkout: clone workflow respectively: the pair every other config-key
// image gate has (checkRigorImage and checkCloneRigorImage), judged on the key
// the configuration the run will read writes (gitAuthorSet, which authorGates
// resolves once for both gates). The refusal names the fix that kind of image
// has — fugaro image refresh for a built image, fugaro update --base base for
// the installation's own — and the way out the file's author has, from
// gitAuthorSubject.
//
// The judge is the key being written and not the value it holds: the file is
// decoded with KnownFields, so a runner older than gitAuthorSince refuses
// git.author whatever it says — git.author: fugaro and an explicit null are
// lines its decoder refuses exactly like git.author: launcher — and judging
// the value would launch those to die at bootstrap on a run already paid for.
func checkGitAuthorImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	if !gitAuthorSet(cfg) {
		return nil
	}
	subject, alt := gitAuthorSubject(spec.Repo)
	return checkImageSince(ctx, env, slug, spec, kind, gitAuthorSince, subject, "the git.author key", "; or "+alt, warn)
}

// checkCloneGitAuthorImage is checkGitAuthorImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneGitAuthorImage(ctx context.Context, env *cloudEnv, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	if !gitAuthorSet(cfg) {
		return nil
	}
	subject, alt := gitAuthorSubject(spec.Repo)
	return checkCloneImageSince(env.lc, spec, gitAuthorSince, subject, "the git.author key", "; or "+alt, warn)
}

// gitAuthorSet reports whether the configuration the run will read writes the
// git.author key at all, at any value: the question the gitAuthorSince gates
// ask, and RepoGitAuthorSet's reason. Like rigorLaunchSources it is the file
// the run checks out and not the launcher's working tree (launchFileConfig), so
// a launch whose file cannot be read here — one for another repository from a
// checkout that is not its own — is gated on nothing, and its bootstrap, which
// reads the file this launcher could not, is what meets the key then.
func gitAuthorSet(cfg *config.Config) bool {
	return cfg != nil && cfg.RepoGitAuthorSet
}

// gitAuthorLauncher reports whether the configuration opted in to the
// launcher's identity — the *value* question, which decides whether a run
// records an author at all (D606), where gitAuthorSet's key question decides
// which images may run the file.
func gitAuthorLauncher(cfg *config.Config) bool {
	return cfg != nil && cfg.Git.Author == config.GitAuthorLauncher
}

// gitAuthorSubject names, for the gates' messages, the key a repository opted
// in with and the way out its author has. git.author is InRepo (config.Scopes),
// so unlike rigor: and governance: there is no project-layer half to name.
func gitAuthorSubject(repo string) (subject, alt string) {
	return "fugaro.yaml's git.author of " + repo,
		"remove the git.author key from fugaro.yaml to run under the run's own identity until every job image is current"
}

// gitAuthorImageWarnings warns, once, that a configuration which writes the
// git.author key needs every job image on gitAuthorSince. It is validate's half
// of checkGitAuthorImage, and a warning rather than a Problem for
// rigorImageWarnings' reason: the file is valid, and what has to move is the
// installation (the base image, or a workflow's derived one), which is why both
// ways to move an image are named. Unlike rigor: and governance: there is no
// project layer's half to name — config.Scopes gives git.author InRepo alone —
// so the sentence is the repository's own.
func gitAuthorImageWarnings(c *config.Config) []config.Problem {
	if !gitAuthorSet(c) {
		return nil
	}
	return []config.Problem{{Path: "git.author", Message: "this file's git.author needs fugaro " + gitAuthorSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseGitAuthorSkew refuses fugaro update --repo, before any cloud effect,
// when the repository's fugaro.yaml writes the git.author key and the
// installation's base image predates gitAuthorSince — a runner built from it
// decodes the file with KnownFields and refuses the run at bootstrap on a key
// it does not know. The states of ref are read as refuseRigorSkew reads them:
// no base image passes, one that is not a release only warns.
//
// fromRepo is the key being written (RepoGitAuthorSet) and not the value it
// holds, the distinction gateRigor draws for rigor:: git.author: fugaro, an
// explicit null and git.author: launcher are the same line to an older runner's
// decoder, so judging the value would deploy a job that dies at bootstrap on a
// line the operator did write.
func refuseGitAuthorSkew(repo string, fromRepo bool, ref string, warn io.Writer) error {
	if !fromRepo {
		return nil
	}
	subject, alt := gitAuthorSubject(repo)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, gitAuthorSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, gitAuthorSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// authorSkew drops spec.Author, and says so on warn, unless the job image is
// shown to run a fugaro at authorSince or later: an older runner refuses the
// whole task at bootstrap (task.Parse decodes with DisallowUnknownFields), so
// carrying the field would turn a launch that works today into a run that dies
// after it was paid for, over an attribution alone — which is why this gate
// omits the field where the key's gate refuses the launch.
//
// "Shown to" is stricter than the other gates' "not shown to predate": an image
// whose judge answers nothing — a base image with no release tag (a dev build,
// a hand-pushed one, a mirror without one), a build record that cannot be read
// — drops the field too. The field here is not the branch baseBranchSkew
// carries, where the unknown case keeps it because dropping it silently changes
// what the run does: nothing the run does changes here, the warning names the
// image and both ways to move it, and the two mistakes are not symmetric (a
// dropped attribution is announced and recoverable on the next launch; a task
// an older runner refuses is a paid-for run that never started).
//
// The judge is baseBranchSkew's, for the same two shapes and the same reason: a
// checkout: clone job runs the installation's base image itself, a derived image
// the base its build record was built FROM. clone is that workflow's checkout
// mode, nil when the launch could not read it; both judges are then asked, and
// either one not being shown to know the field drops it.
func authorSkew(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, clone *bool, warn io.Writer) {
	a := spec.Author
	if a == nil {
		return
	}
	drop := func() {
		spec.Author = nil
		fmt.Fprintf(warn, "the job image is not known to run fugaro %s or later, which is the release that reads a task's author: the launch records none, so this run's commits stay the run's own identity. Run fugaro update --base base for the installation's base image, or fugaro image refresh --repo %s --workflow %s for a workflow's own, to record %s <%s> again\n",
			authorSince, spec.Repo, spec.Workflow, pluginwire.Printable(a.Name), pluginwire.Printable(a.Email))
	}
	shown := func(rel string, known bool) bool { return known && !imagePredates(rel, authorSince) }
	if clone == nil {
		if rel, known := releaseOf(env.lc.BaseImage(config.BaseKind)); !shown(rel, known) {
			drop()
			return
		}
		if rel, known := recordBaseRelease(ctx, env, slug, spec); !shown(rel, known) {
			drop()
		}
		return
	}
	if *clone {
		if rel, known := releaseOf(env.lc.BaseImage(config.BaseKind)); !shown(rel, known) {
			drop()
		}
		return
	}
	if rel, known := recordBaseRelease(ctx, env, slug, spec); !shown(rel, known) {
		drop()
	}
}
