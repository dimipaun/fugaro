package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
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

// proxySince is the first fugaro release whose runner serves a repository's
// Bitbucket access through the run's own loopback forwarder and the
// installation's credentials service (B1.7, the plan's S1; D495): such a run's
// job holds no Bitbucket token at all (G1 — the flip takes the mount off it),
// so its git environment is rewritten to the forwarder and the credential it
// pushes with is the service's. A runner older than this release knows none of
// that, so it would start on a job that has no token and no forwarder and die
// at bootstrap over a repository it cannot even clone.
//
// The launch gate (proxyGate, launch_budget.go) refuses such a launch, judged
// the way every other *Since gate is (checkImageSince, or
// checkCloneImageSince for a checkout: clone workflow): a release older than
// this one refuses, a base with no release tag (a dev or hand-pushed image) is
// only warned about. It carries no shipped flag: refusing every image that
// predates it is exactly the direction the gate wants, since a proxied run on
// an older runner cannot work at all.
//
// It names 0.22.0, the CLI minor after the 0.21.0 tag, and NOT the plan's own
// S1 number 0.15.0: that slot was spent on other work and 0.15.0 is tagged, so
// a gate naming it would refuse nothing — every image in the field is at or
// past it. It named 0.21.0 until 2026-11-08, while that release was not yet
// tagged, and the tag was then cut without the runner half: its tree has no
// proxySince, nothing of the proxy in internal/runner, and no proxied path in
// internal/gitprov's providers, while internal/infra's resolveBitbucketProxied
// — the flip that takes the mount off the job — is already in it and carries no
// image-version condition of its own. 0.21.0 is therefore the one release this
// gate must refuse, and the base image `fugaro update` installs today: a value
// naming it admits exactly the launch the gate exists to refuse, which is the
// "refuses too late" failure the number exists to avoid. The number is the
// release that carries the runner half, not the one the work was first slotted
// for. docs/release.md's Before-you-tag list verifies it against the release
// actually cut, as it does for the other *Since constants. A wrong value
// refuses too late (a proxied run reaches a runner that cannot do it) or too
// early (an image that would have worked stays blocked).
const proxySince = "0.22.0"

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

// imageCatalogSince names the release that publishes — and reads — the
// project policy node's image_catalog key (D1140, D1156): the release whose
// CLI models the key, and therefore the first whose launcher does not refuse
// a node that carries one (refuseUnknownPolicyKeys). A launcher on an older
// CLI meets such a node as an unknown key and refuses it, which is why an
// installation that names a catalog upgrades every launcher before it
// launches again; a runner tolerates the key and logs it.
//
// It is spelled apart from catalogSince/catalogShipped on purpose (F14):
// those two are the *price* catalog's — a route's require_zdr and
// price_policy — and the two catalogs ship on their own schedules, so one
// name for both would judge an image document by the price catalog's
// knowledge and the other way round. docs/release.md's ("Before you tag")
// checklist verifies it equals the release that ships the key, the same way
// it does for catalogSince.
const imageCatalogSince = "0.17.0"

// imageCatalogShipped is true as of 0.18.0, the first release that ships the
// flip. 0.17.0 published and read the node's image_catalog key but was tagged
// with the gate still false, so its readiness held back every job a catalog
// entry gives its image to. infra's imageCatalogShipped (readiness.go) is the
// same fact and moves with this one.
const imageCatalogShipped = true

// imageContextShipped is infra's imageContextShipped (readiness.go) at this
// release, the same fact the pair above keeps for the catalog key itself:
// false, because no released runner reads a catalog source's context member
// yet. The two gates are one question asked of two readers — readiness holds
// back the job whose image comes from such an entry, and policyContextRefusal
// refuses to publish the node that carries it — and they move together, in the
// commit that bumps this constant to the release that reads the member: the
// member is not optional the way the catalog key is, so an older runner
// refuses the whole image_catalog block rather than skipping it (D1403).
const imageContextShipped = false

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
// 0.16.0, the release L4.2 ships in: 0.15.0 shipped with the step parsing
// but no runner running it (a 0.15.0 run stopped at planning), so L4.2
// missed that tag and the constant moved with it — no image ever accepts an
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
const adversarialSince = "0.16.0"

// escalationSince is the first fugaro release whose runner holds the outer
// loop's own recipe constructs: a review step's hand_over (it ends the run at
// that review with the verdict recorded and no fix) and the rebase step (rung
// 0's whole run: put the pull request's branch back on its base, resolve the
// conflicts, re-run the tests and push). A runner before it refuses both
// outright — hand_over is an unknown review key ("is not a recipe key") and
// rebase is not a kind in its version 1 step list ("is not a step type") — so
// the whole recipe dies at bootstrap, with the launch already paid for. This
// gate turns that into a refusal at the launcher's terminal, naming fugaro
// image refresh, exactly as adversarialSince does for its own step.
//
// Like artifactsSince it carries no shipped flag: refusing every image that
// predates the constructs is exactly the direction this gate wants, and an
// image with no release tag (a dev build, a hand-pushed one) only warns, the
// same as for every other *Since constant here — safe either way, since a
// runner that cannot run the step refuses the recipe at bootstrap rather than
// skipping it. It names 0.23.0 ("Tapir", the CLI minor after the tagged
// 0.22.0), which is not tagged yet: docs/release.md's ("Before you tag")
// checklist verifies the constant against the tag actually cut, the way
// digestSince's and adversarialSince's bullets record.
const escalationSince = "0.23.0"

// evaluateSince is the first fugaro release whose runner reads a task's mode:
// evaluate: its task validation accepts the value, its recipe parser knows the
// mode and the registered step kind the recipe holds, and the run is that one
// step and nothing else (A5.2, U6). It is judged from the task's own field and
// not from a recipe's constructs, exactly as artifactsSince is: mode: evaluate
// is carried by the launcher (chooseRecipe writes the spec's mode for an
// artifact and an evaluate recipe alike), so every launch that could start a
// judge run is judged, whatever its recipe's source — an evaluate recipe's text
// trips none of the parse-time gates (the mode is a 0.7.0 key and the registered
// step kind reads as any other registered kind), so without this arm the launch
// would be admitted at recipesSince.
//
// A runner before it refuses the task outright: mode is in the spec's own
// vocabulary (task.Spec.Validate), and "evaluate" is a value it does not know,
// so the run dies at bootstrap with the launch already paid for and nothing in
// the bucket judged. Like artifactsSince it carries no shipped flag: refusing
// every image that predates the mode is exactly the direction this gate wants,
// and an image with no release tag (a dev build, a hand-pushed one) only warns,
// the same as for every other *Since constant here.
//
// It names 0.24.0, the CLI minor after 0.23.0 ("Tapir"), which main already
// carries the release notes of and which is not tagged yet: docs/release.md's
// ("Before you tag") checklist verifies the constant against the tag actually
// cut, the way digestSince's, adversarialSince's and slotSince's bullets
// record. A wrong value refuses too late (a launch reaches a runner that
// refuses its task) or too early (an image that can judge stays blocked).
const evaluateSince = "0.24.0"

// escalationRecipes are the catalog recipe names whose runner half ships in
// escalationSince's release: `escalate` (one coder pass, one review, the
// verdict recorded) and `rebase`, which that release adds, and `merge-judge`,
// the merge judge's own recipe, which E6 adds with the same engine — its name
// is listed here from the start so the three the escalation ladder launches
// (E11) are held to the one release in one place, rather than each arrival
// moving this gate. A recipe of the same name from the repository or the
// project is that layer's own file and is judged by the constructs it
// carries, like any other: this list is the catalog's names, not the names.
var escalationRecipes = []string{"escalate", "rebase", "merge-judge"}

// escalationRecipeNamed reports whether the task's recipe is one of the
// catalog's escalation recipes: a name the launcher resolved from the catalog
// itself, never a repository's or the project's own file that happens to
// share it (resolveRecipe reads those first, and they win). It is the floor
// under the construct gates: a recipe whose text carries no hand_over and no
// rebase step this launcher can see — merge-judge, whose file the catalog
// does not hold yet, and any of the three whose text a later release edits to
// use fewer constructs — is still held to the release whose runner runs the
// ladder's recipes, so an image that cannot take the launch is refused at the
// terminal rather than at bootstrap.
func escalationRecipeNamed(spec *task.Spec) bool {
	return spec.Recipe != nil && spec.Recipe.Source == string(recipe.SourceCatalog) && slices.Contains(escalationRecipes, spec.Recipe.Name)
}

// reviewLevelsSince is the first fugaro release whose runner acts on review
// levels — the task's review_levels block and fugaro.yaml's agent.levels
// (L4.2, D720-D727): it inserts the step of an enabled level the recipe lacks
// (O7), runs the adversarial step, resolves each level's own model and hands
// the levels to readiness. A runner before it — 0.15.0, the release the field
// and the keys shipped in — decodes both and acts on neither, so a launch it
// accepted would run with the levels dropped in silence: no `none`, no level
// 3, no level's own model or effort, no owner's floor.
//
// Like artifactsSince it carries no shipped flag: refusing every image that
// predates the release is exactly the direction this gate wants, and an image
// with no release tag (a dev build, a hand-pushed one) only warns, the same
// as for every other *Since constant here — a refusal there would name a
// release nothing can compare it with, and the levels are the launcher's to
// keep off such an image. It names 0.16.0, the same number adversarialSince
// names and for the same reason: level 3 is the step that made the runner act
// on levels, so the two ship together, and docs/release.md's checklist
// verifies it against the tag actually cut, the way digestSince's and
// adversarialSince's bullets record.
const reviewLevelsSince = "0.16.0"

// reviewLevelsKnows names, for the gate's messages, what the job image's
// runner has to know: the field and the configuration keys a runner that acts
// on levels reads.
const reviewLevelsKnows = "the review levels (review_levels, agent.levels)"

// noneModelSince is the first fugaro release whose runner reads a level's
// `model: none` as the level's own switch (config.NoneModel) rather than as a
// model ID: LevelSetting.validate folds the literal into `none: true`, so the
// level is skipped and the string is never resolved as a model name. Every
// release that acts on levels knows the keys — one older than
// reviewLevelsSince drops the whole block in silence, which is the gate beside
// this one — so between reviewLevelsSince and this release a runner decodes
// the block and resolves the level on the model `none`: a run whose
// allowed_models refuses that ID dies at bootstrap, and one with no ceiling
// runs the level the owner switched off, on a model which is not one. Neither
// skips the level, which is the silence this gate exists for.
//
// Like artifactsSince it carries no shipped flag: refusing every image that
// predates the release is exactly the direction this gate wants, and an image
// with no release tag (a dev build, a hand-pushed one) only warns, the same as
// for every other *Since constant here — a refusal there would name a release
// nothing can compare it with, and the spelling is the file's own to keep off
// such an image. It names 0.21.0, the release this work is slotted for, which
// is not tagged yet: docs/release.md's Before-you-tag list verifies the number
// against the tag actually cut, the way reviewLevelsSince's bullet records.
const noneModelSince = "0.21.0"

// noneModelKnows names, for this gate's messages, what the job image's runner
// has to know: a level whose model is the literal none, which a runner at
// noneModelSince or later reads as the level's own switch. The message reads
// "needs a job image whose runner knows <this>".
const noneModelKnows = "a level's model named none as the level's own switch"

// reviewJobSince is the first fugaro release whose runner can serve a review
// launch, which is two things and not one: it deploys the review-only job
// variant (design docs/cloud-senior-review, CSR7 — the second job per
// (profile, provider, account), with its own service account and no secret at
// all, which is what a senior review runs on) and it carries the review run's
// own model path (CSR13, PR #1777, first tagged in 0.20.0), which is how a
// container holding no token reaches a model at all. The variant shipped in
// 0.19.0 and the model path in 0.20.0, so an image older than this one fails
// in one of two ways: with no variant beside its coding job, a launch naming
// one resolves a job the installation does not hold; on a 0.19.0 image the
// variant is there and the launch starts, pays for the container and dies on
// the runner's own missing-token refusal.
//
// It named 0.19.0 until CSR11's flip of 2026-11-08, because reviewJobAdmitted
// was false and refused every launch before this gate was ever reached: the
// value only decides anything once a launch is admitted, and the commit that
// admits it is the one that moves the value.
//
// It is declared here, by the task that ships the variant, and read by the
// tasks that gate on it — the launch refusal (CSR2: an image older than this
// one would otherwise be accepted in silence) and the release's own checklist
// entry (CSR6) — which is what the plan means by declaring the constant to
// that task. docs/release.md's ("Before you tag") list verifies the number
// against the release whose runner can serve the launch, the way
// digestSince's and reviewLevelsSince's bullets record.
const reviewJobSince = "0.20.0"

// reviewAdmissionSince is the first fugaro release whose credentials service
// admits the review job's own caller, which is E7's second half and the half
// an INSTALLATION carries rather than a binary: reviewJobAdmitted says this
// launcher has the class compiled in, and this says which release's service
// answers it. The service reads its review class out of
// FUGARO_REVIEW_JOB_ACCOUNTS (CSRW1, PR #1932) and the installation module
// renders that variable (CSRW2, PR #1934), so an installation whose service
// predates this release refuses the review job's account — the launch starts,
// pays for the container and dies on the service's answer, which is the one
// failure a launch refusal exists to keep in front of the spend.
//
// The judge is the installation's own record of that service: the installation
// root's terraform.tfvars.json, whose credentials_service.image is the image
// `fugaro update` pinned (installationServiceRelease). It is the one record of
// it there is — the launcher can ask the service nothing (no admin route to its
// configuration, D143, and its admission decision is invisible without a call
// that mints a credential, reviewJobAdmitted's comment), and localcfg's
// CredentialsService holds the service's URL and no version. It is also a
// record every update writes: the pin is resolved and applied on every
// installation, whether the service is being deployed or one already deployed
// is moved onto this release's image (init_stages.go's moveCredentialsImage),
// where `base_images.base` moves only in an update that handles that base kind
// at all — outside a checkout with no --base and no catalog build,
// imagesStage.kinds names none, and a gate on the base image would then refuse
// an installation an update has already converged. docs/release.md's ("Before
// you tag") list verifies the number against the release whose service admits
// the class.
const reviewAdmissionSince = "0.21.0"

// providerEntrySince is the first fugaro release whose credentials service
// reads the bundle's provider entry (config.ProviderKeySecret, AK6b of
// docs/api-key-review): the project's provider credential, whose value is the
// mapping of the key, the provider's base URL and how the key is sent
// (credsvc's providerValue). The loader is strict about the document's KEYS —
// an entry it does not know refuses the WHOLE document (ErrBundleUnknownKey),
// which the service answers as a service-wide refusal and not as a refused
// credential — so a bundle written with this entry by a CLI newer than the
// service that reads it halts every caller of that service, coding runs
// included, until `fugaro update` moves the service onto the image that reads
// it.
//
// `fugaro creds set` is the bundle's only writer, so the guard is at that
// write (creds.go, AK10 of the same folder), and it judges the release that
// SERVES the document rather than the installation's pin: a pin ahead of a
// service whose apply was declined or failed is exactly the state the guard
// exists for. Unlike every other gate here, a record it cannot read refuses
// the write instead of warning about it — the alternative is a write that
// halts every caller of the service.
//
// It names 0.22.0, the release AK6b's own commit (`26e72426`, #2172) went out
// in and therefore the first release whose credentials service was built with
// the entry's reader: the constant is the tag that shipped it and not the
// release this guard is written in, and a release that raised it would refuse
// the write on installations whose deployed service already reads the entry —
// which is every installation on that release, and `fugaro update` would be no
// remedy for them. docs/release.md's ("Before you tag") list carries that
// reading.
const providerEntrySince = "0.22.0"

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

// launchLimitsSince is the release whose runner knows the four chain limits a
// launch may write into task.json's overrides (max_usd, max_tokens, max_time,
// max_review_rounds; docs/run-limit-carry-over §2, D1041). task.Parse decodes
// with DisallowUnknownFields, so a runner older than this refuses the whole
// task: a launch that carries one is gated here rather than started on a job
// that dies at bootstrap.
const launchLimitsSince = "0.22.0"

// chainLimitsKnows is what such a runner knows, as the gate names it.
const chainLimitsKnows = "the chain's limits its task carries (max_usd, max_tokens, max_time, max_review_rounds)"

// chainLimitsSubject is what the gate says the launch carries: the four flags
// that write the four fields. A launch that reuses a stored task — --retry, or
// a repeated --run-id of one that never launched — carries limits no flag of
// this call named, which is why the subject names the fields as well as the
// flags.
const chainLimitsSubject = "a chain limit (--max-usd, --max-tokens, --max-time, --max-review-rounds)"

// chainLimitsAlt is the gate's other cure: the launch can drop the limits,
// where the image cannot be brought forward.
const chainLimitsAlt = "; or drop the chain limits from the launch, which needs a job image whose runner knows them before it can carry one"

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

// securitySince is the first fugaro release whose config reader knows
// fugaro.yaml's security: block (SC7 of
// docs/security-classification/implementation-plan.md: the repository's own
// list of what its runs treat as security-sensitive). The file is decoded with
// KnownFields, so a runner built from an older base image refuses the key at
// bootstrap, before the agent starts and after the launch was paid for — and it
// refuses the key rather than the rows: an empty block (`security: {}`) and an
// explicit null are lines that decoder refuses exactly like a full list, which
// is why every gate below judges RepoSecuritySet (the key being written) and
// never the resolved block.
//
// Like rigorSince the judge is the image that runs the run: the installation's
// base image for a checkout: clone workflow (its job runs no derived image of
// its own), the base a workflow's build record was built FROM for a derived
// one. The gate is fugaro update --repo's (refuseSecuritySkew, judged on the
// installation's base image before any cloud effect), the launch's
// (checkSecurityImage, checkCloneSecurityImage), which runs only where the file
// the run will read can be read here at all (launchFileConfig), and validate's
// warning (securityImageWarnings) — the three halves rigorSince has.
//
// Both halves of the key are judged, the way rigorSince's gates carry both of
// rigor's: the repository's own block (RepoSecuritySet) and the project layer's
// top-level security: block (config.ProjectLayer.SetsSecurity, D1501). The
// layer's half reaches further than the repository's, which is why it needs the
// same three gates: the layer is decoded strictly by every release, so an image
// older than this constant refuses the whole layer file at bootstrap, whichever
// repository of the project read it.
//
// It names 0.22.0, the release this work is slotted for (the release jobsSince
// names too), which is not tagged yet: the release that tags it holds this
// number to the tag it cuts, as it does for sharedSince's 0.14.0 and
// reviewJobSince's 0.20.0. docs/release.md ("Before you tag") has the checklist
// verify it.
const securitySince = "0.22.0"

// securityRequestSince is the first fugaro release whose runner reads the
// task's own security field: the request `fugaro run --security` writes, and
// the same request a hand-written task makes with `security: true` (SC10 of
// docs/security-classification/implementation-plan.md, D1517). It is the run
// asking for the strongest review there is, whatever paths it changed.
//
// It is not securitySince's constant and cannot be: that one is 0.22.0, the
// release whose runner reads fugaro.yaml's and the project layer's security:
// block, and a runner built from it knows the block and refuses the task field
// — the field is the newer half of the same control, and it is the task that
// carries it rather than the configuration. task.Parse decodes with
// DisallowUnknownFields, so a job image older than this release refuses the
// whole task at bootstrap, after the launch was paid for and with the run left
// as bytes in the bucket under an id that never starts, which is what the
// launch's gate refuses first (checkSecurityRequestImage and
// checkCloneSecurityRequestImage, judged from the image exactly as the sibling
// field gates are).
//
// The project layer's always_level_3 is the other half of the same request
// (D1503) and the same constant judges it: a key of the layer's security:
// block, which a 0.22.0 release decodes and never acts on, so the switch is
// inert until the job image is on this constant — the silent non-application
// noneModelSince and reviewLevelsSince exist for. Both homes are read off the
// same configuration (securityRequestSources), and the layer's half needs the
// constant for the behaviour rather than for the key.
//
// It names 0.23.0 ("Tapir", the CLI minor after the tagged 0.22.0), which is
// **not tagged yet**: the release that tags it holds this number to the tag it
// cuts, and it moves if this work lands in another release. docs/release.md
// ("Before you tag") has the checklist verify it.
const securityRequestSince = "0.23.0"

// securityRequestSubject is what the securityRequestSince gate calls the task
// field it judges: the task's own, not the flag's, as the title's and the plan
// task's are — a --retry carries no --security (taskFlags) and still carries
// the field whoever launched that run wrote.
const securityRequestSubject = "the task's security request (--security, kept by --retry)"

// securityRequestKnows names what the job image's runner has to know for that
// gate's message.
const securityRequestKnows = "the security field of the task it runs"

// securitySwitchSubject and securitySwitchKnows are that pair for the request's
// other home, the project layer's always_level_3: the layer's own key, which no
// repository writes and only the layer's operator can remove.
const (
	securitySwitchSubject = "the project layer's security.always_level_3"
	securitySwitchKnows   = "the project layer's security.always_level_3 switch"
)

// securityRequestSources reports which homes of the run's request for the
// strongest review this launch carries: the task's own security field
// (securityRequestSubject), and the project layer's always_level_3
// (securitySwitchSubject), which the run resolves at bootstrap too (the
// runner's securityRequested). Both are judged from the configuration the run
// will read — launchLevelsConfig, the same text the levels gate resolves — so a
// launch and the run it starts agree on whether the request is on.
func securityRequestSources(spec *task.Spec, cfg *config.Config) (fromTask, fromSwitch bool) {
	return spec.Security, cfg != nil && cfg.Layer != nil && cfg.Layer.Security != nil && cfg.Layer.Security.AlwaysLevel3
}

// securityRequestNames is the subject and the "knows" of the gate's message for
// the homes the launch carries, joined as securitySubject joins the two halves
// of the security: block.
func securityRequestNames(fromTask, fromSwitch bool) (subject, knows string) {
	switch {
	case fromTask && fromSwitch:
		return securityRequestSubject + " and " + securitySwitchSubject,
			securityRequestKnows + " and " + securitySwitchKnows
	case fromSwitch:
		return securitySwitchSubject, securitySwitchKnows
	}
	return securityRequestSubject, securityRequestKnows
}

// securityRequestRaiseOnly is the configuration and the task block with review
// level 3's own off-switch cleared: the launcher's half of the rule the runner
// applies to the same request (internal/runner/plan.go's
// forcedLevelsAreRaiseOnly). The request only turns the level on (D1517), so a
// layer that wrote `l3: none` does not defeat it, and a launcher that resolved
// the switch as written would refuse a launch the run it starts runs anyway —
// D722's refusal of a floored level a layer turned off, raised where the run
// honours the request instead.
func securityRequestRaiseOnly(cfg *config.Config, task *config.ReviewLevels) (*config.Config, *config.ReviewLevels) {
	clear := func(l *config.LevelSetting) *config.LevelSetting {
		if l == nil || (!l.Off && l.Model != config.NoneModel) {
			return l
		}
		out := *l
		out.Off = false
		if out.Model == config.NoneModel {
			out.Model = ""
		}
		return &out
	}
	cfgOut := *cfg
	cfgOut.Agent.Levels.L3 = clear(cfgOut.Agent.Levels.L3)
	if task == nil {
		return &cfgOut, nil
	}
	block := *task
	block.L3 = clear(block.L3)
	return &cfgOut, &block
}

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

// carriedDefaultAlt is the way out a launch that carries another recipe has:
// the catalog's own default. It is not a way out of an old image either — a
// carried default is judged by the constructs it carries, and it carries the
// adversarial step since adversarialSince — so the sentence names that release
// rather than sending the user to a second refusal.
var carriedDefaultAlt = "; or launch with --recipe default, which is the catalog's own loop and needs a " + adversarialSince + " job image itself (it carries the adversarial step)"

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
			alt = carriedDefaultAlt
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
// The adversarial step and the escalation constructs are judged from rp too,
// and they are the newest a recipe can carry (escalationSince, then
// adversarialSince), so their cases come first. Unlike the manifest and mode:
// artifact — a repository recipe's digest run and its mode both reach the
// task as fields, which carry their own gates above — neither the step nor
// the two escalation constructs has anything in the task behind it, so the
// launcher's read of the file the recipe names is the only place the gate can
// see it; one this launcher cannot read keeps recipesSince, with the runner's
// parse refusal as the backstop. Every older construct keeps the gate it
// always had, judged from the embedded text alone: a repository recipe trips
// none of them here, which is what its own tests pin.
//
// The catalog's escalation recipes are held to escalationSince by name as
// well (escalationRecipeNamed), which is the floor under the constructs: the
// name is what the ladder's launches and a person's terminal carry, and the
// gate must not move back if a later release edits one of those files to use
// fewer of them.
func recipeSinceFor(spec *task.Spec, rp *recipe.Recipe) string {
	r := embeddedRecipeOf(spec)
	if len(spec.DigestRuns) == 0 && r == nil && rp != nil {
		switch {
		case recipe.UsesRebase(rp) || recipe.UsesHandOver(rp):
			return escalationSince
		case recipe.UsesAdversarial(rp):
			return adversarialSince
		}
	}
	since := ""
	switch {
	case len(spec.DigestRuns) > 0:
		since = digestSince
	case r == nil:
		since = recipesSince
	case recipe.UsesRebase(r) || recipe.UsesHandOver(r):
		since = escalationSince
	case recipe.UsesAdversarial(r):
		since = adversarialSince
	case recipe.UsesDigest(r):
		// The digest step is 0.13.0's, newer than every other construct
		// here, so it is the binding gate; no mode: artifact construct can
		// come with it, since the step is only legal in mode: implement.
		since = digestSince
	case recipe.UsesV08(r):
		// UsesV08 implies UsesV07 (a 0.8.0 construct always comes with a
		// 0.7.0 one: mode is one), and artifactsSince is the newer of the
		// two, so it is the binding gate.
		since = artifactsSince
	case recipe.UsesV07(r):
		since = recipesV07Since
	default:
		since = recipesSince
	}
	if imagePredates(since, escalationSince) && escalationRecipeNamed(spec) {
		return escalationSince
	}
	return since
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
			"Run fugaro update in its checkout, in your own terminal window, or with --yes from CI or a script (never in a coding agent's session; its images stage verifies %s, pins it by digest through the installation's remote repository and rebuilds the workflow's image)%s",
			subject, knows, since, spec.Repo, spec.Workflow, why, base, alt)
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
// checkout mode, nil when the launch read no workflow at all — a repository no
// shared job serves, a project that published no profile, a ref whose tree
// carries no document, or a launch for another repository from a checkout that
// is not its own. Neither judge is then known to be the job's, so both are
// asked and
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
		fmt.Fprintf(warn, "the job image predates %s and ignores the project's base branch %s; fugaro update in the checkout moves it onto this release's base image\n", baseBranchSince, b)
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

// abSince is the first fugaro release whose runner gives a task's ab block the
// challenger's half of the pair: the release A2.1's shadow behaviour landed in,
// where the arm publishes nothing and pushes nothing (D554). A1.1's wire is one
// release older, and that is what makes the release before this one the worse
// of the two to write a pair onto rather than the better: a 0.13.0 runner reads
// the block, has no challenger rule, and runs the arm handed to it as an
// ordinary run — one that publishes, taking the developer's own PR and the
// shadow's whole point with it. A runner older than the wire knows no ab field
// at all, and task.Parse decodes with DisallowUnknownFields, so it refuses the
// whole task at bootstrap: the champion's arm included, which is the
// developer's own run.
const abSince = "0.14.0"

// pairImageWhy is why this launch samples no pair for the job image it will
// run on (pairOldImage), or "" when the image is one a pair may be written
// for. It is asked after the planner has answered a pair and before anything
// of it is written: a pair is dropped here, and never the launch (D554).
//
// Every other image gate here judges a field the task carries, and each of
// them refuses the launch or drops the field, which are the two directions
// that leave the developer's run intact. This one is asked by the *pair* —
// a second run this build adds on top of that run — so it answers one closed
// reason and the champion goes on alone, unmarked: a pair dropped costs a
// sample, while a pair written onto an image whose runner predates abSince
// costs the champion the run it was launched for either way: the arm publishes
// as an ordinary run, or the whole task is refused at bootstrap.
//
// The image is judged by both judges baseBranchSkew asks where it read no
// workflow (its own nil arm, whose reasoning this shares): a checkout: clone
// job runs the installation's base image itself and a derived one runs the
// binary of the base its build record was built FROM. This is asked before
// the gates that resolve a workflow, so neither judge is known to be the
// job's and both are asked — either one predating drops the pair.
//
// An image neither judge can show a release for is one no judge here can date
// (a development build, a hand-pushed image), and it is sampled on: dropping
// the pair there would take the feature off every installation built from
// source, and what that would buy back — a development image whose runner
// predates the block — is a build from a checkout older than the release that
// writes pairs.
func pairImageWhy(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) string {
	predates := func(rel string, known bool) bool { return known && imagePredates(rel, abSince) }
	if rel, known := releaseOf(env.lc.BaseImage(config.BaseKind)); predates(rel, known) {
		return pairOldImage
	}
	if rel, known := recordBaseRelease(ctx, env, slug, spec); predates(rel, known) {
		return pairOldImage
	}
	return ""
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
//
// subject and knows are the caller's, the way checkCloneImageSince's are: the
// lock is the one record of which image a shared job runs, and more than one
// key of this release is judged off it (sharedSince's own gate, and the
// published policy node's version in run_allowlist.go's policyImageGate), so
// the sentence names what the launch needs that image to know rather than what
// this function guesses.
func checkSharedImageSince(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, since, subject, knows string, warn io.Writer) error {
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
			alt = carriedDefaultAlt
		}
	}
	return checkCloneImageSince(lc, spec, recipeSinceFor(spec, repoRecipeForGate(ctx, spec)), subject, "recipes", alt, warn)
}

// reviewLevelsAsk is what a launch asks of its job image's runner about
// review levels, in the terms the refusal names, and "" when it asks nothing
// at all — a launch every release runs identically.
//
// The resolved levels (config.ResolveLevels, L3.1) are the half the flags
// cannot carry: a level 3 the mode's own row or the owner's floor turns on,
// and a first-line step the run has to insert in a recipe that lacks one
// (O7), both reach a run with no task field and no configuration key. A
// runner that predates reviewLevelsSince runs neither, silently, so the task
// field alone would let both through.
//
// The second half is the blocks only a runner that acts on levels reads: the
// task's review_levels (L1.1) and the merged configuration's agent.levels
// (L1.3), each named by the level keys it writes. A block that writes nothing
// says nothing (config.writesLevel), and a level that merely runs as it
// always has is not one of these: the model a level resolves to with no
// per-level key is agent.models' own, which every release reads.
//
// The owner's floor (D722, G1) is the third, and it is read from the
// owner-only policy node by a runner at reviewLevelsSince or later alone.
//
// recipeUnknown is whether the launcher could not read the recipe the task
// names, which leaves the insertion question unanswerable rather than
// answered: config.ResolveLevels carries no recipe then and reports Inserted
// false, and a first-line step the run's real recipe turns out to lack would
// be inserted by a runner at reviewLevelsSince and dropped in silence by one
// before it. The conservative direction is the one every other gate here
// takes for a file it cannot read — ask, and let the refusal name the image
// to refresh — and it costs a refusal only on an image older than the
// release whose runner acts on levels.
func reviewLevelsAsk(levels config.Levels, task *config.ReviewLevels, cfg *config.Config, floor []policy.Level, recipeUnknown bool) string {
	var asks []string
	if len(floor) > 0 {
		asks = append(asks, "the owner's review-level floor")
	}
	if levels.L3.On {
		asks = append(asks, "review level 3 (its adversarial step)")
	}
	switch {
	case levels.L1.Inserted:
		asks = append(asks, "review level 1 (a first-line step the run inserts)")
	case levels.L1.On && recipeUnknown:
		asks = append(asks, "review level 1 (a first-line step the run inserts unless its recipe carries one, and this checkout has not got that file)")
	}
	if s := levelsBlockAsk("the task's review_levels", task); s != "" {
		asks = append(asks, s)
	}
	if cfg != nil {
		if s := levelsBlockAsk("fugaro.yaml's agent.levels", &cfg.Agent.Levels); s != "" {
			asks = append(asks, s)
		}
	}
	return strings.Join(asks, " and ")
}

// levelsBlockAsk names the level keys one block writes ("the task's
// review_levels (l1.none, l2.model)"), and "" for a block that writes none:
// an absent block and one whose levels are all nil say nothing about any
// level, the same reading config.writesLevel gives them. A level's block is
// named by its first key alone, because what the refusal is about is the
// level, not the list.
func levelsBlockAsk(name string, l *config.ReviewLevels) string {
	if l == nil {
		return ""
	}
	var keys []string
	for _, lv := range []struct {
		name string
		s    *config.LevelSetting
	}{{"l1", l.L1}, {"l2", l.L2}, {"l3", l.L3}} {
		switch {
		case lv.s == nil:
		case lv.s.Off:
			keys = append(keys, lv.name+".none")
		case lv.s.Model != "":
			keys = append(keys, lv.name+".model")
		case lv.s.Effort != "":
			keys = append(keys, lv.name+".effort")
		case lv.s.Focus != "":
			keys = append(keys, lv.name+".focus")
		}
	}
	if len(keys) == 0 {
		return ""
	}
	return name + " (" + strings.Join(keys, ", ") + ")"
}

// launchReviewLevelsAsk is reviewLevelsAsk for the launch about to be made:
// the run's levels resolved the way the run resolves them (config.ResolveLevels,
// the call L4.2's bootstrap makes), from the file the run will read, the mode
// it resolves and the owner's floor. The second result is that resolution's
// own refusal: L3.2 refuses the launch on it, before the task is written, so a
// configuration the run's bootstrap would refuse (L1 = L2, a level a layer
// turns off under the owner's floor, an effort a provider route would drop)
// costs no job and leaves nothing in the bucket.
//
// A policy node the launcher cannot read is one the launch's own policy gate
// refuses (launchPolicyGates), and inventing a floor off it would refuse a
// launch that works: that read fails towards asking nothing. The file's own
// absence does not — it is not a file the run's bootstrap refuses, it is a
// launch made from outside the repository's checkout — and the configuration
// this reads is launchLevelsConfig's, which resolves over the project layer the
// task carries when there is no file to read, rather than reading none.
//
// The mode is the launcher's approximation of the run's: the ref's file with
// the flag raised over it, as noteRigorRequest reads it, since the
// authoritative resolution is the runner's at bootstrap. The recipe is the
// one this launcher can read (launchRecipeForLevels), and recipeUnknown says
// the one thing that read could not settle: whether that recipe writes a
// first-line step of its own. CatalogDefault is read as false: it decides
// level 1's own default alone, which this gate does not judge.
//
// The run's own request for the strongest review is part of this resolution
// exactly as it is part of the run's (securityRequestSources, the runner's
// securityForcedLevels, SC10/D1517): it forces review level 3, and the levels'
// own off-switch for that level is cleared rather than allowed to defeat it
// (securityRequestRaiseOnly). Mirroring it is what keeps this gate the run's
// own answer.
//
// A mode: review recipe is the one run the force cannot hold — the level has
// no step to run in — and the launch tolerates it exactly as the run does
// (the runner's securityForcedLevels): the force is dropped, the levels
// resolve as they would without the request, and a warning names the request
// and the reason. Refusing here instead would be the tolerance's opposite:
// `fugaro review` resolves review-only (reviewOnlyRecipe) and every launch of
// it comes through this gate, so a project whose layer sets always_level_3
// would refuse every review of every repository of it — including the project
// owner's own, with no flag that lifts it — and the same would go for
// `run --pr --recipe review-only` and for a recipe picked by --recipe pick.
// A request that costs a launch is worse than one a launch cannot honour.
func launchReviewLevelsAsk(ctx context.Context, env *cloudEnv, spec *task.Spec, cfg *config.Config, rcp *recipe.Recipe, recipeUnknown bool, warn io.Writer) (string, error) {
	mode := rigor.Resolve(cfg.RigorConfig(), rigor.Request{Mode: rigor.Mode(spec.Rigor), Paths: spec.Paths}).Mode
	var floor []policy.Level
	if p, found, err := env.policy.read(ctx, env.lc); err == nil && found && p != nil {
		floor = p.ReviewFloor[mode]
	}
	fromTask, fromSwitch := securityRequestSources(spec, cfg)
	if (fromTask || fromSwitch) && rcp != nil && rcp.Mode == recipe.ModeReview {
		subject, _ := securityRequestNames(fromTask, fromSwitch)
		fmt.Fprintf(warn, "warning: the run asks for the strongest review (%s) and its recipe %s is mode: review, which allows exactly one review step and nothing else: review level 3 has no step to run in, so the launch resolves the levels the run would have resolved without it\n", subject, rcp.Name)
		fromTask, fromSwitch = false, false
	}
	in := config.LevelInputs{
		Config: cfg, Mode: mode, Floor: floor, Task: spec.ReviewLevels, Recipe: rcp, Providers: env.lc.Providers,
	}
	if fromTask || fromSwitch {
		// Cloned: ReviewFloor hands out the policy's own slice, and appending
		// to it would write the forced level into the merged policy.
		in.Floor = append(slices.Clone(floor), policy.LevelL3)
		in.Config, in.Task = securityRequestRaiseOnly(cfg, spec.ReviewLevels)
	}
	levels, _, err := config.ResolveLevels(in)
	if err != nil {
		// Nothing is asked of the image and no task is written: the launch is
		// the resolution's to refuse, and a refusal carries no levels to read
		// an ask off (config.ResolveLevels answers with the zero Levels).
		// Refusing here rather than leaving it to the run's bootstrap is what
		// G2 means by "never launches", and the alternative is a job paid for
		// a run whose first act is to refuse the task it was given.
		if fromTask || fromSwitch {
			// The refusal is config's own (a level a layer turns off under the
			// floor, a model the allow-list does not name): what this adds is
			// that the request is why the level was resolved at all, so a
			// reader is not left looking for a floor nobody set.
			subject, _ := securityRequestNames(fromTask, fromSwitch)
			return "", userErr("the run's request for the strongest review (%s) turns review level 3 on, and its review levels do not resolve: %v", subject, err)
		}
		return "", userErr("%v", err)
	}
	return reviewLevelsAsk(levels, spec.ReviewLevels, cfg, floor, recipeUnknown), nil
}

// launchLevelsConfig is the configuration the launch's review-level gates read:
// the file the run will check out, resolved over the layer that applies to it
// (launchFileConfig), and — where no file can be read at all, because the
// launcher stands outside the repository's checkout and this run's ref is not
// in one either — the project layer the task carries itself (launchLayerConfig).
//
// One function for both readers is the point: the levels ask
// (launchReviewLevelsAsk) and the gate on a level whose model is the literal
// none (checkNoneModelImage, checkCloneNoneModelImage) judge the same text, and
// in this state the task's layer is all of it — the layer's own agent.levels
// block, and a level in it a runner older than noneModelSince resolves as a
// model ID rather than skipping. A gate reading launchFileConfig alone sees
// nothing there and runs the level the layer switched off.
func launchLevelsConfig(ctx context.Context, env *cloudEnv, spec *task.Spec) *config.Config {
	if cfg := launchFileConfig(ctx, env, spec); cfg != nil {
		return cfg
	}
	return launchLayerConfig(spec)
}

// launchLayerConfig is the configuration a launch from outside the
// repository's checkout resolves: the project layer the task carries
// (spec.ProjectLayer, embedProjectLayer), resolved over a fugaro.yaml that
// is nothing but the anchoring that layer applies to — its own project and
// gcp_project, which is the whole of what the file the launcher could not
// read contributes here. Nothing settles whether that layer really applies
// to the run (the file that would say so is the one that could not be read),
// and the reading taken is the conservative one: a layer the run turns out
// not to use can only make this gate ask for an image that acts on levels,
// where a layer the run does use and this gate ignored is a level the run
// drops in silence.
//
// A layer that does not parse leaves the empty configuration: embedProjectLayer
// parsed the layer before storing it, so that does not happen for a task this
// launcher wrote, and a layer that will not read back is a run's own problem to
// refuse, not a skew this gate names.
//
// A layer that parses but does not resolve over the anchor is kept all the same,
// as a configuration carrying the layer alone. The anchor is what fails there,
// not the layer: it is a stand-in for a file this launcher could not read, it
// carries no workflow and no profile:, and a layer that names no
// default_profile of its own has nothing to resolve over it ("workflows: must
// define at least one workflow, or name a profile with profile:"). The layer is
// still text the run reads — the file at the ref resolves it once the run has
// one — so the gates that read it off the text see it (SetsLevelsNoneModel),
// where an empty configuration reads as a layer that says nothing and runs the
// level the layer switched off in silence.
func launchLayerConfig(spec *task.Spec) *config.Config {
	if spec.ProjectLayer == nil {
		return &config.Config{}
	}
	l, ps := config.ParseProjectLayer([]byte(spec.ProjectLayer.YAML), config.LayerAnchor{})
	if len(ps) > 0 {
		return &config.Config{}
	}
	anchor := []byte(fmt.Sprintf("version: 1\nproject: %s\ngcp_project: %s\n", l.Project, l.GCPProject))
	if c, _, ps := config.Resolve(anchor, l); len(ps) == 0 {
		return c
	}
	return &config.Config{Layer: l}
}

// launchRecipeForLevels is the recipe launchReviewLevelsAsk judges the levels
// on, and whether that read could not settle it.
//
// A project or catalog recipe is not a gap here: the launcher embeds its text
// in the task when it resolves the name (taskRecipeEmbedding), so the task's
// own bytes are the recipe the run will get — the same bytes the runner reads
// out of task.json. The task's text comes first for that reason; a repository
// recipe carries its name alone (the runner reads the file at the ref), so
// this checkout's copy is the second read (repoRecipeForGate, the same one
// recipeForLaunch and the recipe gates make).
//
// Unknown — the second result — is a repository recipe whose file this
// checkout has not got (a directory that is not the repository's, a file
// that lives only at the ref, an unreadable one), and a task whose embedded
// text does not parse, which is a launch the runner's bootstrap refuses. The
// steps of a recipe nobody read are unknown, not absent, and the L1 ask says
// so rather than reading Inserted false as "no first-line step".
func launchRecipeForLevels(ctx context.Context, spec *task.Spec) (*recipe.Recipe, bool) {
	if spec.Recipe == nil {
		// The runner chooses, and only the catalog's own default can come
		// out of that with no text in the task — the recipe this launcher
		// would have embedded had it resolved a name itself. Its file is
		// known here, and it writes a first_line step.
		return nil, false
	}
	if r := embeddedRecipeOf(spec); r != nil {
		return r, false
	}
	if r := repoRecipeForGate(ctx, spec); r != nil {
		return r, false
	}
	return nil, true
}

// reviewLevelsAlt is the refusal's other way out, and the one a user who has
// just moved to this release has: the update rebuilds this repository's job
// images behind its own confirmation, so after a brew upgrade the one command
// they run — the HARD RULE of the update — is already the rebuild the refusal
// asks for, and the refresh above is that same rebuild on its own, for one
// repository, from its checkout.
const reviewLevelsAlt = "; or run fugaro update, which is the whole upgrade: it brings the installation to this release and rebuilds this repository's job images behind its own confirmation"

// checkReviewLevelsImage refuses a launch whose job image runs a fugaro older
// than reviewLevelsSince: subject is what reviewLevelsAsk named, and "" —
// a launch that asks nothing — is never gated. The judge is the build
// record's base_ref, as every checkImageSince gate's is.
func checkReviewLevelsImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind, subject string, warn io.Writer) error {
	if subject == "" {
		return nil
	}
	return checkImageSince(ctx, env, slug, spec, kind, reviewLevelsSince, subject, reviewLevelsKnows, reviewLevelsAlt, warn)
}

// checkCloneReviewLevelsImage is checkReviewLevelsImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneReviewLevelsImage(lc *localcfg.Config, spec *task.Spec, subject string, warn io.Writer) error {
	if subject == "" {
		return nil
	}
	return checkCloneImageSince(lc, spec, reviewLevelsSince, subject, reviewLevelsKnows, reviewLevelsAlt, warn)
}

// noneModelSources reports, for the launch's noneModelSince gate, whether the
// configuration the run will read names a level's model none: the repository's
// fugaro.yaml (fromRepo) or the project layer it resolves over (fromLayer).
// The configuration is launchLevelsConfig's, so a launch from outside the
// repository's checkout — no file read at all — is asked of the layer the task
// carries, which is the same layer that stands in for the levels ask above it.
// Both are read off the text rather than off the resolved agent.levels, which
// the fold leaves without the literal (Config.RepoLevelsNoneModelSet,
// ProjectLayer.SetsLevelsNoneModel).
//
// The task's own review_levels is not one of the sources: the launcher writes
// it, and it writes the level folded into `none: true` (LevelSetting.validate),
// so no runner ever reads the spelling there.
func noneModelSources(cfg *config.Config) (fromRepo, fromLayer bool) {
	if cfg == nil {
		return false, false
	}
	return cfg.RepoLevelsNoneModelSet, cfg.Layer.SetsLevelsNoneModel()
}

// noneModelSubject names, for the gate's messages, which file names a level's
// model none, and the way out that file's author has: the other spelling of
// the same switch in that same file, which every runner that acts on levels
// reads — a level written `none: true` is skipped by 0.16.0 and later, so
// rewriting the level is a way out with no image to move. The distinction is
// rigorSubject's: the layer's literal reaches every repository of the project
// and only its operator can rewrite it.
func noneModelSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const (
		repoAlt  = "write it as none: true in fugaro.yaml"
		layerAlt = "write it as none: true in the project layer's defaults.agent.levels and publish it again"
		same     = ": it is the same switch, and every runner that acts on levels reads it, from " + reviewLevelsSince + " on"
	)
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's agent.levels and the project layer's defaults.agent.levels of " + repo,
			"write them as none: true in both" + same
	case fromLayer:
		return "the project layer's defaults.agent.levels (read for " + repo + ")", layerAlt + same
	}
	return "fugaro.yaml's agent.levels of " + repo, repoAlt + same
}

// checkNoneModelImage refuses a launch whose job image runs a fugaro older
// than noneModelSince while the configuration the run reads names a level's
// model none: a runner before that release reads the literal as a model ID, so
// the level the file switched off is run — or the run dies at bootstrap on an
// allowed_models refusal — rather than skipped. The judge is the build record's
// base_ref, as every checkImageSince gate's is.
func checkNoneModelImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := noneModelSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := noneModelSubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, noneModelSince, subject, noneModelKnows, "; or "+alt, warn)
}

// checkCloneNoneModelImage is checkNoneModelImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneNoneModelImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := noneModelSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := noneModelSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(lc, spec, noneModelSince, subject, noneModelKnows, "; or "+alt, warn)
}

// stopFractionSince is the first fugaro release whose config reader knows
// fugaro.yaml's agent.review_stop_fraction and the project layer's
// defaults.agent.review_stop_fraction (the owner's ruling of 2026-11-08: the
// stop rule's time half, the share of the run's budget after which the review
// loop starts no further round). Both files are decoded with KnownFields, so a
// runner built from an older image refuses either key at bootstrap, before the
// agent starts and after the launch was paid for — and it is the key that is
// refused, whatever it says: a file writing 0 asks for the default and is
// refused all the same, which is why the gate reads the text
// (Config.RepoStopFractionSet, ProjectLayer.SetsStopFraction) and not the
// resolved Agent.ReviewStopFraction.
//
// Like noneModelSince it carries no shipped flag: refusing every image that
// predates the release is exactly the direction this gate wants, and an image
// with no release tag (a dev build, a hand-pushed one) only warns, the same as
// for every other *Since constant here. It names 0.22.0, the release this work
// is slotted for ("Sloth"), which is not tagged yet: docs/release.md's
// Before-you-tag list verifies the number against the tag actually cut, the way
// noneModelSince's bullet records.
const stopFractionSince = "0.22.0"

// stopFractionKnows names, for the image gates' messages, what the job image's
// runner has to know: the key, in whichever of the two files that may write it
// did. The message reads "needs a job image whose runner knows <this>".
func stopFractionKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the stop rule's time half (agent.review_stop_fraction and the project layer's defaults.agent.review_stop_fraction)"
	case fromLayer:
		return "the stop rule's time half (the project layer's defaults.agent.review_stop_fraction)"
	}
	return "the stop rule's time half (agent.review_stop_fraction)"
}

// stopFractionSubject names, for the gates' messages, which file wrote the key,
// and the way out that file's author has: the key is a knob, not a behaviour
// the run depends on — a runner that does not read it stops the loop only when
// the whole budget is spent, which is what every release before this one does —
// so removing it is a way out with no image to move. The distinction between
// the two files is rigorSubject's: the layer's key reaches every repository of
// the project and only its operator can remove it.
func stopFractionSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const (
		repoAlt  = "remove agent.review_stop_fraction from fugaro.yaml: a runner that does not read it starts no round past the whole budget, which is what every release before this one does"
		layerAlt = "remove defaults.agent.review_stop_fraction from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way, and a runner that does not read the key starts no round past the whole budget"
	)
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's agent.review_stop_fraction and the project layer's defaults.agent.review_stop_fraction of " + repo,
			"remove the key from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's defaults.agent.review_stop_fraction (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's agent.review_stop_fraction of " + repo, repoAlt
}

// stopFractionSources reports, for the launch's stopFractionSince gate, whether
// the configuration the run will read writes the key: the repository's
// fugaro.yaml (fromRepo) or the project layer it resolves over (fromLayer,
// which the task's own layer stands in for when the launcher can read no file:
// launchLevelsConfig).
//
// The layer's half is SetsStopFraction and not SetsDefault, which answers false
// for a key written as null: an older runner's KnownFields decoder refuses the
// key whatever it holds, so a layer writing it null is a layer every repository
// of the project dies at bootstrap on, and a gate reading the resolved value
// would deploy it.
func stopFractionSources(cfg *config.Config) (fromRepo, fromLayer bool) {
	if cfg == nil {
		return false, false
	}
	return cfg.RepoStopFractionSet, cfg.Layer.SetsStopFraction()
}

// stopFractionImageWarnings warns, once, that a configuration which writes the
// key — the repository's file (RepoStopFractionSet) or the project layer it
// resolved over (SetsStopFraction) — needs every job image on stopFractionSince. It
// is validate's half of checkStopFractionImage, and a warning rather than a
// Problem for rigorImageWarnings' reason: the file is valid, and what has to
// move is the installation (the base image, or a workflow's derived one), which
// is why both ways to move an image are named. It says which of the two files
// wrote the key, the same distinction stopFractionSubject draws for the gates,
// because the layer's key reaches every repository of the project and only its
// operator can remove it.
func stopFractionImageWarnings(c *config.Config) []config.Problem {
	fromRepo, fromLayer := stopFractionSources(c)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject := "this file's agent.review_stop_fraction"
	switch {
	case fromRepo && fromLayer:
		subject = "this file's agent.review_stop_fraction and the project layer's defaults.agent.review_stop_fraction"
	case fromLayer:
		subject = "the project layer's defaults.agent.review_stop_fraction"
	}
	return []config.Problem{{Path: "agent.review_stop_fraction", Message: subject + " needs fugaro " + stopFractionSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseStopFractionSkew refuses fugaro update --repo, before any cloud
// effect, when the configuration the checkout resolves writes
// agent.review_stop_fraction and the installation's base image predates
// stopFractionSince — a runner built from it decodes both files with
// KnownFields and refuses the run at bootstrap on a key it does not know. It is
// the half checkStopFractionImage cannot be: a launch from outside a checkout
// reads no fugaro.yaml at all, so the key the run will find once it has one is
// judged here, by the operator's own converge (refuseRigorSkew's shape and its
// reason). The states of ref are read as refuseRigorSkew reads them: no base
// image passes, one that is not a release only warns.
//
// fromRepo and fromLayer are the key being written (RepoStopFractionSet, the
// layer's SetsStopFraction) and not the value it holds: a file writing 0, which
// is the default and asks for nothing, is the same line to an older runner's
// decoder as one writing 0.5.
func refuseStopFractionSkew(repo string, fromRepo, fromLayer bool, ref string, warn io.Writer) error {
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := stopFractionSubject(repo, fromRepo, fromLayer)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, stopFractionSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, stopFractionSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkStopFractionImage refuses a launch whose job image runs a fugaro older
// than stopFractionSince while the configuration the run reads writes
// agent.review_stop_fraction: that runner decodes fugaro.yaml and the project
// layer with KnownFields, so the key alone kills the run at bootstrap. The
// judge is the build record's base_ref, as every checkImageSince gate's is.
func checkStopFractionImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := stopFractionSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := stopFractionSubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, stopFractionSince, subject, stopFractionKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkCloneStopFractionImage is checkStopFractionImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneStopFractionImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := stopFractionSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := stopFractionSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(lc, spec, stopFractionSince, subject, stopFractionKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// triageSince is the first fugaro release whose config reader knows
// fugaro.yaml's agent.triage — the triage stage's switch (D1854) — and whose
// runner has the stage at all (D1851). fugaro.yaml is decoded with KnownFields,
// so a runner built from an older image refuses the key at bootstrap, before
// the agent starts and after the launch was paid for. It is refused whatever
// the key says, which is why the gate reads the text (Config.RepoTriageSet) and
// not the resolved switch: a file writing `triage: true` asks for the default
// and dies exactly like one writing false.
//
// The key has one file, not two: agent.triage is repo-scope alone
// (config.Scopes), so there is no layer half here the way stopFractionSince
// reads one — a project layer that writes it is refused by the layer's own
// loader long before any image is judged. It carries no shipped flag, for
// noneModelSince's reason: refusing every image that predates the release is
// the direction this gate wants, and an image with no release tag (a dev
// build, a hand-pushed one) only warns. It names 0.23.0, the release this work
// ships in ("Tapir", the CLI minor after the tagged 0.22.0), which is not
// tagged yet: docs/release.md's Before-you-tag list verifies the number against
// the tag actually cut. It is not 0.22.0: that release is tagged (06347d98) and
// its config reader has no agent.triage, so a gate naming it would admit an
// image that refuses the key at bootstrap — the failure this gate exists to
// prevent, arriving after the launch was paid for.
const triageSince = "0.23.0"

// triageKnows names, for the image gates' messages, what the job image's
// runner has to know: the stage itself and the key that turns it off. The
// message reads "needs a job image whose runner knows <this>".
const triageKnows = "the triage stage and its switch (agent.triage)"

// triageSubject names, for the gates' messages, which file wrote the key and
// the way out its author has. The way out is not one sentence for both values
// of a switch: the absent key means the stage is on (Agent.TriageOn), so a file
// that wrote true loses nothing by removing the key — the key's absence asks for
// the answer it wrote — while one that wrote false gives the stage back to its
// default on the first image that reads the file. Offering the second sentence
// to the first file, as "which is what the key turned off asks for", offers its
// author the opposite of what it wrote.
//
// written is the key's own field (Agent.Triage): nil is the key absent, which no
// caller reaches — triageSources gates all three — and reads as false, the
// branch that has a cost to name.
func triageSubject(repo string, written *bool) (subject, alt string) {
	subject = "fugaro.yaml's agent.triage of " + repo
	if written != nil && *written {
		return subject, "remove agent.triage from fugaro.yaml: the absent key means the stage on, which is what this file writes, and a job image that predates the stage runs no triage whatever the file says"
	}
	return subject, "remove agent.triage from fugaro.yaml only if the off-switch may go with it: a job image that predates the stage runs no triage, and the first image that reads the key finds the stage on again"
}

// slotSince is the first fugaro release whose runner acts on the A/B pair's
// slot (docs/review-levels-ab-jobmax, D728 and B1.1; task.AB.Slot and
// task.AB.ChampionRun): a challenger at a review slot replays that level's
// review over the champion's own recorded patch and publishes nothing, so a
// pair at a review slot is one review stage rather than a second coding loop
// (B1.2).
//
// A runner that only decodes the field is not enough, which is why this is not
// the release the field itself ships in: such a runner, handed a challenger at
// a review slot, would run the whole task on the challenger's coder model —
// publish: none keeps it harmless, and it is still a paid run of the entire
// loop where the pair means one review, which is the "refuses too late"
// failure proxySince's comment describes. It names 0.24.0, the CLI minor after
// the untagged 0.23.0, the release the replay is expected in: that release does
// not exist yet, so no image is at or past it, and the gate refuses every
// launch that would need the replay until the release carrying it is cut.
// docs/release.md's Before-you-tag list verifies the number against the release
// that actually carries B1.2, the way it does for the other *Since constants.
//
// The launch that reads it is the reviewer arm's own (B1.4, ee/ab): it writes
// `ab.slot` and `ab.champion_run` onto the challenger once the champion's patch
// exists, and it asks this gate first, exactly as the pair launch (A3.3) asks
// its own gate before writing an `ab` block at all.
const slotSince = "0.24.0"

// slotKnows names, for the image gates' messages, what the job image's runner
// has to know: the pair's slot and the champion run the replay reads.
const slotKnows = "the A/B pair's slot (ab.slot, ab.champion_run)"

// slotAsk is what a launch asks of its job image's runner about the pair's
// slot, in the terms the refusal names, and "" when it asks nothing: a task
// with no `ab` block, and one whose block names no slot at all, are launches
// every release runs identically — the second is the coder pair A1.1 already
// wrote, which B1.1 reads as coder without a key.
//
// The gate is written on the key's presence and not on its value: a slot a
// launcher spells out is written by the release whose runner acts on slots, and
// what a reader needs to know is whether the image's runner reads the key at
// all. A coder pair — the pair A3.3 samples — writes neither key, so this gate
// is never the one that refuses it: that launch is judged by the `ab` field's
// own gate (A3.3).
func slotAsk(spec *task.Spec) string {
	if spec.AB == nil || spec.AB.Slot == "" {
		return ""
	}
	return fmt.Sprintf("the task's ab.slot (%s)", spec.AB.Slot)
}

// checkSlotImage refuses a launch whose job image runs a fugaro older than
// slotSince while the task's pair names a slot: subject is what slotAsk named,
// and "" — a launch that asks nothing — is never gated. The judge is the build
// record's base_ref, as every checkImageSince gate's is.
func checkSlotImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind, subject string, warn io.Writer) error {
	if subject == "" {
		return nil
	}
	return checkImageSince(ctx, env, slug, spec, kind, slotSince, subject, slotKnows, "", warn)
}

// checkCloneSlotImage is checkSlotImage for a checkout: clone workflow: see
// checkCloneImageSince.
func checkCloneSlotImage(lc *localcfg.Config, spec *task.Spec, subject string, warn io.Writer) error {
	if subject == "" {
		return nil
	}
	return checkCloneImageSince(lc, spec, slotSince, subject, slotKnows, "", warn)
}

// triageSources reports, for the launch's triageSince gate, whether the
// configuration the run will read writes the key: the repository's own
// fugaro.yaml and nothing else (Config.RepoTriageSet). There is no layer to
// ask, unlike stopFractionSources' second half — config.Scopes gives
// agent.triage InRepo alone, so a project layer writing it never reaches a
// resolution at all.
func triageSources(cfg *config.Config) bool {
	return cfg != nil && cfg.RepoTriageSet
}

// triageImageWarnings warns, once, that a configuration which writes
// agent.triage (RepoTriageSet) needs every job image on triageSince. It is
// validate's half of checkTriageImage, and a warning rather than a Problem for
// rigorImageWarnings' reason: the file is valid, and what has to move is the
// installation's image, which is why both ways to move one are named.
func triageImageWarnings(c *config.Config) []config.Problem {
	if !triageSources(c) {
		return nil
	}
	return []config.Problem{{Path: "agent.triage", Message: "this file's agent.triage needs fugaro " + triageSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseTriageSkew refuses fugaro update --repo, before any cloud effect, when
// the configuration the checkout resolves writes agent.triage and the
// installation's base image predates triageSince — a runner built from it
// decodes fugaro.yaml with KnownFields and refuses the run at bootstrap on a key
// it does not know. It is the half checkTriageImage cannot be: a launch from
// outside a checkout reads no fugaro.yaml at all, so the key the run will find
// once it has one is judged here, by the operator's own converge
// (refuseRigorSkew's shape and its reason). The states of ref are read as
// refuseRigorSkew reads them: no base image passes, one that is not a release
// only warns. It takes the configuration rather than the one bit gateTriage
// would pass, because the way out its message offers is worded for the answer
// the file wrote (triageSubject, Agent.Triage).
func refuseTriageSkew(repo string, cfg *config.Config, ref string, warn io.Writer) error {
	if !triageSources(cfg) {
		return nil
	}
	subject, alt := triageSubject(repo, cfg.Agent.Triage)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, triageSince):
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, triageKnows, triageSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkTriageImage refuses a launch whose job image runs a fugaro older than
// triageSince while the configuration the run reads writes agent.triage: that
// runner decodes fugaro.yaml with KnownFields, so the key alone kills the run at
// bootstrap. The judge is the build record's base_ref, as every checkImageSince
// gate's is.
func checkTriageImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	if !triageSources(cfg) {
		return nil
	}
	subject, alt := triageSubject(spec.Repo, cfg.Agent.Triage)
	return checkImageSince(ctx, env, slug, spec, kind, triageSince, subject, triageKnows, "; or "+alt, warn)
}

// checkCloneTriageImage is checkTriageImage for a checkout: clone workflow: see
// checkCloneImageSince.
func checkCloneTriageImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	if !triageSources(cfg) {
		return nil
	}
	subject, alt := triageSubject(spec.Repo, cfg.Agent.Triage)
	return checkCloneImageSince(lc, spec, triageSince, subject, triageKnows, "; or "+alt, warn)
}

// followUpSince names the release that carries the two fields `fugaro followups
// --launch` writes into a task: the parent run and the finding keys it settles
// (task.Spec.FollowUpOf) and the debt mark (task.Spec.Debt). A runner built
// before it decodes the task with DisallowUnknownFields and refuses the whole
// spec at bootstrap — bytes in the bucket under a run that never starts, after
// the launch was paid for.
//
// It names 0.22.0, the release whose task reader carries both fields: they
// landed with T-TR4 (#2057), a release before the command that writes them, so
// the constant is the older of the two and not the release this work ships in.
// A constant naming the newer one — 0.23.0, which is not tagged yet — would
// refuse the job image every current installation runs, and `fugaro image
// refresh` rebuilds from the same base and is refused again. docs/release.md's
// Before-you-tag list verifies the number against the tag actually cut and
// against internal/task at that tag, the way it does for every other *Since
// constant.
//
// The gate fires on the fields themselves and not on the command, since the
// bytes are what the older runner refuses: a hand-written or migrated task
// carrying them is refused the same way, and `fugaro run --plan-task` on a
// filed task is judged here too.
const followUpSince = "0.22.0"

// followUpKnows names, for the gates' messages, what the job image's runner has
// to know. The message reads "needs a job image whose runner knows <this>".
const followUpKnows = "the follow-up fields a filed task carries (follow_up_of, debt)"

// followUpSubject names, for the gates' messages, where the fields come from.
const followUpSubject = "a task filed for a run's deferred findings (fugaro followups --launch)"

// followUpAsk reports whether spec carries the fields followUpSince gates: a
// task filed for a run's deferrals. Either field is enough — the older runner
// refuses the spec on the first key it does not know — and the two are written
// together, so the check reads as the one intention it is.
func followUpAsk(spec *task.Spec) bool {
	return spec.FollowUpOf != nil || spec.Debt
}

// checkFollowUpImage refuses a launch whose job image runs a fugaro older than
// followUpSince while the task carries the follow-up fields.
func checkFollowUpImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	if !followUpAsk(spec) {
		return nil
	}
	return checkImageSince(ctx, env, slug, spec, kind, followUpSince, followUpSubject, followUpKnows, "", warn)
}

// checkCloneFollowUpImage is checkFollowUpImage for a checkout: clone workflow:
// see checkCloneImageSince.
func checkCloneFollowUpImage(lc *localcfg.Config, spec *task.Spec, warn io.Writer) error {
	if !followUpAsk(spec) {
		return nil
	}
	return checkCloneImageSince(lc, spec, followUpSince, followUpSubject, followUpKnows, "", warn)
}

// reviewJobKnows names, for this gate's messages, what the job image's runner
// has to know: the review-only variant of the job it is in, the marker a
// launch on it sets, which is the whole of what a review run branches on
// (internal/runner's reviewJob), and the model path the review class serves —
// the last of the three is why an image that has the other two can still be
// refused, since a container holding no token reaches a model only through it.
const reviewJobKnows = "the review-only job variant, the review job's own marker (FUGARO_REVIEW_JOB, mode: review) and the review run's own model path"

// reviewJobAlt is the refusal's other way out, as reviewLevelsAlt's is: the
// update deploys the variants beside the coding jobs and rebuilds this
// repository's job images behind its own confirmation.
const reviewJobAlt = "; or run fugaro update, which is the whole upgrade: it brings the installation to this release, deploys the review-only job beside each coding job and rebuilds this repository's job images behind its own confirmation"

// checkReviewJobImage refuses a launch that carries mode: review (fugaro
// review): the launch starts CSR7's review-only variant and marks the
// execution, and the two releases that satisfy it are not one. An image older
// than the variant's has no such job beside its coding job, so a launch naming
// one resolves a job the installation does not hold, and the review would run
// on the coder's own container with the App key the review container exists to
// be without; a 0.19.0 image holds the variant and no model path for a
// container that carries no token, so the launch starts, bills and dies inside
// the job. reviewJobSince's value is what has to keep this gate ahead of both,
// which is why it names the release whose runner can serve the launch rather
// than the release that first deployed the variant. Judged from the deployed
// job's build record, as every checkImageSince gate's is.
func checkReviewJobImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	return checkImageSince(ctx, env, slug, spec, kind, reviewJobSince, "a review launch", reviewJobKnows, reviewJobAlt, warn)
}

// checkCloneReviewJobImage is checkReviewJobImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneReviewJobImage(lc *localcfg.Config, spec *task.Spec, warn io.Writer) error {
	return checkCloneImageSince(lc, spec, reviewJobSince, "a review launch", reviewJobKnows, reviewJobAlt, warn)
}

// checkReviewAdmission is the review launch's other half, and the half no job
// image can show: a review container mounts no credential of its own, so its
// account comes from the installation's credentials service, and the service
// admits the review job's own caller only where its own release carries the
// class (reviewAdmissionSince). The two halves of a review launch are then
// judged from two different places — the job's build record for the variant,
// the installation for the service — and this is the one the flip of
// reviewJobAdmitted made load-bearing: while the constant was false no launch
// reached the service, and a launch that reaches one too old for the class
// starts, bills and dies on its 401.
//
// The judge is the installation's own record of its service rather than any
// image of the job's: installationServiceRelease, whose comment says why that
// record and no other one — the launcher cannot ask the service anything (D143,
// reviewJobAdmitted's comment), the local config carries the service's URL and
// no release, and the installation's tfvars are what `fugaro update` writes on
// every installation, whatever kind of images that run happened to handle. An
// installation that states no release for its service — no workdir for this
// project, no tfvars, no credentials_service block, or a reference an operator
// wrote by hand — is warned about and admitted: what cannot be read is not a
// refusal, the rule every gate here takes for an image it cannot judge.
//
// This gate is reached by `fugaro review` and not by every spelling of a
// review: `fugaro run --pr N --recipe review-only` builds the same task with no
// mode on it (reviewSpec writes none, and only runReviewLaunch sets one), so it
// enters no `mode: review` block and launches the repository's ordinary coding
// job, whose container carries an account of its own and asks the service for
// no review class. That spelling is the refusal's way out, and it needs no gate
// of this one's.
func checkReviewAdmission(lc *localcfg.Config, warn io.Writer) error {
	if lc == nil {
		return nil
	}
	rel, image, ok := installationServiceRelease(lc)
	if !ok {
		fmt.Fprintf(warn, "warning: %s, so the release the installation's credentials service was deployed from is not known; whether that service admits the review job's own caller is not checked\n", installationServiceUnknown)
		return nil
	}
	if !imagePredates(rel, reviewAdmissionSince) {
		return nil
	}
	return userErr("a review runs on the review-only job, which mounts no credential of its own: its account comes from the installation's credentials service, and a service admits the review job's own caller only from fugaro %s on, the release whose service reads the review class out of FUGARO_REVIEW_JOB_ACCOUNTS. This installation's service is %s — image %s, the pin this installation's own fugaro update wrote — so the run would fetch its account from a service that refuses it and die inside the job it had already paid for. Run fugaro update: that is the whole upgrade, and the command that resolves this release's service image and applies it, beside the review-only job variant. This launch is refused rather than started until that update has run; until then, review the pull request in the run's own job with fugaro run --pr N --recipe %s, which starts the repository's ordinary coding job — that container carries its own account, so its review asks the service for no class at all",
		reviewAdmissionSince, rel, pluginwire.Printable(image), reviewOnlyRecipe)
}

// installationServiceUnknown is what the warning above names when the
// installation states nothing this gate can read as a release: it is one
// sentence per outcome, and this one is shared by every reason, because the
// reason is a fact about a file on this machine rather than something the
// reader can act on differently.
const installationServiceUnknown = "this installation's terraform.tfvars.json names no credentials service image, or names one whose reference carries no release version"

// installationServiceRelease is the release the installation's credentials
// service image names, read from the installation root's terraform.tfvars.json,
// with the image itself for the refusal to print. ok is false when the
// installation states no release there, which is "not known" rather than "old":
// no workdir for this project on this machine, no tfvars in it, no
// credentials_service block, no image, or a reference an operator wrote by hand
// (a digest with no release in it, or one resolved from another registry).
//
// It is the update's DESIRED state and not the applied one: the document is
// written as the stage plans, and the apply that carries it out comes after, so
// a run whose apply the owner declined or which failed at the apply leaves the
// pin ahead of the deployed service and this gate admits a launch the service
// still refuses. That is the direction it can be wrong in, and the other one is
// worse: refusing an installation whose record this machine does not hold — a
// second machine, a config copied to someone else's — is a refusal on a launch
// that works, with a remedy the reader cannot carry out.
func installationServiceRelease(lc *localcfg.Config) (rel, image string, ok bool) {
	image, ok = installationServiceImageOf(lc)
	if !ok {
		return "", "", false
	}
	rel = infra.CredentialsServiceImageRelease(image)
	return rel, image, rel != ""
}

// installationServiceImageOf is the record itself: the credentials service
// image the installation's terraform.tfvars.json names, whether or not the
// reference carries a release. It is what tells "no pin at all" — an
// installation whose workdir this machine does not hold, or one that names no
// service — from a pin this reader cannot read, which are two different
// answers for a caller deciding whether anything is coming: the review gate
// warns about both (installationServiceRelease), while the bundle write
// refuses the second (creds.go's bundleReaderRelease), because an image whose
// release cannot be shown is a service that may not be able to read the
// document.
func installationServiceImageOf(lc *localcfg.Config) (image string, ok bool) {
	dir, err := infra.InstallationWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return "", false
	}
	vars, err := infra.ReadWorkdirVars(dir, "installation")
	if err != nil {
		return "", false
	}
	image, ok, err = infra.CredentialsServiceImageOf(vars)
	if err != nil {
		return "", false
	}
	return image, ok
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
// governanceLaunchSources), compressionRequested, and launchLevelsConfig,
// which resolves the task's own layer over it when this answers nil: from
// cascadeRefConfig, with the launcher's own checkout standing in when the ref
// cannot be read.
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

// planningSince is the first fugaro release whose config reader knows
// fugaro.yaml's planning: block and the project layer's defaults.planning
// (docs/contract-first-decoupling, D1813). Both files are decoded with
// KnownFields, so a runner built from an older base image refuses either key at
// bootstrap; the project layer's key does it to every repository of the project
// at once. Like rigorSince and governanceSince the judge is the installation's
// base image and the gate is fugaro update --repo's (refusePlanningSkew);
// fugaro run gates the launch too (checkPlanningImage, checkClonePlanningImage),
// but only from a checkout of the repository, where it can read the file the
// run will, so outside one only fugaro update --repo checks, and fugaro
// validate warns (planningImageWarnings).
//
// It names 0.22.0, the release the key ships in ("Sloth",
// docs/contract-first-decoupling/implementation-plan.md's CD6, whose gate the
// plan had left to CD7), which is not tagged yet: the release that tags it
// holds this number to the tag it cuts, as it does for jobsSince's 0.22.0 and
// reviewJobSince's 0.20.0. docs/release.md ("Before you tag") has the checklist
// verify it.
const planningSince = "0.22.0"

// planningLaunchSources reports, for the launch's planningSince gate, whether
// the configuration the run will read writes the key: the repository's
// fugaro.yaml (fromRepo) or the project layer it resolves over (fromLayer),
// read the way rigorLaunchSources reads them.
func planningLaunchSources(ctx context.Context, env *cloudEnv, spec *task.Spec) (fromRepo, fromLayer bool) {
	cfg := launchFileConfig(ctx, env, spec)
	if cfg == nil {
		return false, false
	}
	return cfg.RepoPlanningSet, cfg.Layer.SetsPlanning()
}

// planningSubject names, for refusePlanningSkew's messages, which file wrote
// the key, and the way out that file's author has: the repository's block is
// the repository's to remove, while the layer's is read by every repository of
// the project. The layer's key is defaults.planning, the one under defaults:,
// rather than a top-level key of the layer file like governance:'s.
func planningSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const layerAlt = "remove defaults.planning from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's planning: block and the project layer's defaults.planning of " + repo,
			"remove the planning: block from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's defaults.planning (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's planning: block of " + repo, "remove the planning: block from fugaro.yaml"
}

// planningKnows names, for the image gate's messages, the key the job image's
// runner has to know: the message reads "needs a job image whose runner knows
// <this>", so it has to name whichever of the two the config wrote.
func planningKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the planning: block and the project layer's defaults.planning"
	case fromLayer:
		return "the project layer's defaults.planning"
	}
	return "the planning: block"
}

// checkPlanningImage and checkClonePlanningImage are the launch's own
// planningSince gate, for a workflow whose job image is a derived one and for a
// checkout: clone workflow respectively: the pair every other per-workflow
// image gate has (checkImageSince and checkCloneImageSince), judged on the key
// the configuration the run will read writes (planningLaunchSources). The
// refusal names the fix that kind of image has — fugaro image refresh for a
// built image, fugaro update --base base for the installation's own — and the
// way out the file's author has, from planningSubject.
//
// The judge is the key being written and not the value it holds: the file is
// decoded with KnownFields, so a runner older than planningSince refuses
// planning: whatever it says — planning.decoupling: false and an explicit null
// are lines its decoder refuses exactly like planning.decoupling: true — and
// judging the value would launch those to die at bootstrap on a run already
// paid for.
func checkPlanningImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	fromRepo, fromLayer := planningLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := planningSubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, planningSince, subject, planningKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkClonePlanningImage is checkPlanningImage for a checkout: clone workflow:
// see checkCloneImageSince.
func checkClonePlanningImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	fromRepo, fromLayer := planningLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := planningSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(env.lc, spec, planningSince, subject, planningKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// planningImageWarnings warns, once, that a configuration which writes the key
// — the repository's file (RepoPlanningSet) or the project layer it resolved
// over (SetsPlanning) — needs every job image on planningSince. It is validate's
// half of checkPlanningImage, and a warning rather than a Problem for
// rigorImageWarnings' reason: the file is valid, and what has to move is the
// installation (the base image, or a workflow's derived one), which is why both
// ways to move an image are named. It says which of the two wrote the key, the
// same distinction planningSubject draws for the launch gates, because the
// layer's key reaches every repository of the project and only its operator can
// remove it.
func planningImageWarnings(c *config.Config) []config.Problem {
	fromRepo, fromLayer := c.RepoPlanningSet, c.Layer.SetsPlanning()
	if !fromRepo && !fromLayer {
		return nil
	}
	subject := "this file's planning: block"
	switch {
	case fromRepo && fromLayer:
		subject = "this file's planning: block and the project layer's defaults.planning"
	case fromLayer:
		subject = "the project layer's defaults.planning"
	}
	return []config.Problem{{Path: "planning", Message: subject + " needs fugaro " + planningSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refusePlanningSkew refuses fugaro update --repo, before any cloud effect,
// when the repository's fugaro.yaml writes a planning: block (fromRepo) or the
// project layer it resolves over writes defaults.planning (fromLayer) and the
// installation's base image predates planningSince — a runner built from it
// decodes the file with KnownFields and refuses the run at bootstrap on a key
// it does not know. The states of ref are read as refuseRigorSkew reads them:
// no base image passes, one that is not a release only warns.
func refusePlanningSkew(repo string, fromRepo, fromLayer bool, ref string, warn io.Writer) error {
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := planningSubject(repo, fromRepo, fromLayer)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, planningSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, planningSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// jobsSince is the first fugaro release whose config reader knows the project
// layer's jobs: block (R13: the installation's two scheduled jobs and whether
// each is stopped). The key is the layer's alone — a repository's fugaro.yaml
// has no jobs: key and refuses an unknown one — and the layer is decoded with
// KnownFields, so a layer that carries it makes every repository of the project
// whose job image is older than this release fail at bootstrap on a key its
// runner does not know, before the agent starts and after the launch was paid
// for. Like governanceSince the judge is the image that runs the run: the
// installation's base image for a checkout: clone workflow, the base a
// workflow's build record was built FROM for a derived one. fugaro update
// --repo gates it before any cloud effect (gateJobs, beside gateGovernance),
// a launch gates it the way checkGovernanceImage does (checkJobsImage,
// checkCloneJobsImage) from a checkout of the repository, where the layer the
// run will read can be read here at all, and fugaro validate warns
// (jobsImageWarnings).
//
// It names 0.22.0, the release the key ships in ("Sloth",
// docs/periodic-reviews/implementation-plan.md §P7), which is not tagged yet:
// the release that tags it holds this number to the tag it cuts, as it does for
// sharedSince's 0.14.0 and reviewJobSince's 0.20.0. docs/release.md ("Before
// you tag") has the checklist verify it.
const jobsSince = "0.22.0"

// jobsLayerSet reports whether the project layer the configuration resolves
// over writes jobs:, at any value: SetsJobs' question, and its reason — an
// empty block (`jobs:`) is a line an older runner's decoder refuses just as a
// populated one is, and Jobs' zero value cannot tell the two apart. The
// configuration is launchFileConfig's, so the launch gates fire where the
// sibling gates' do: from a checkout of the repository, where the layer the run
// will read can be read here at all (checkoutConfig, or the ref's file resolved
// over the layer the task carries, cascadeRefConfig). Outside one it is nil and
// nothing is gated, which is the shape governanceSince and rigorSince have:
// there only fugaro update --repo checks.
func jobsLayerSet(cfg *config.Config) bool {
	return cfg != nil && cfg.Layer.SetsJobs()
}

// jobsSubject names, for the gates' messages, the block and the way out its
// author has: the layer is the operator's file, and the repositories the block
// breaks each have the same fix — removing it and publishing the layer again.
func jobsSubject(repo string) (subject, alt string) {
	return "the project layer's jobs: block (read for " + repo + ")",
		"remove the jobs: block from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
}

// jobsKnows names, for the image gates' messages, the key the job image's
// runner has to know: the refusal reads "needs a job image whose runner knows
// <this>", and the block is one key of the layer file whatever it holds.
const jobsKnows = "the project layer's jobs: block"

// jobsImageWarnings warns, once, that the project layer a configuration
// resolves over writes jobs:, which needs every job image on jobsSince. It is
// validate's half of checkJobsImage and a warning rather than a Problem for
// rigorImageWarnings' reason: the layer is valid, and what has to move is the
// installation (the base image, or a workflow's derived one), which is why both
// ways to move an image are named.
func jobsImageWarnings(c *config.Config) []config.Problem {
	if !jobsLayerSet(c) {
		return nil
	}
	return []config.Problem{{Path: "jobs", Message: "the project layer's jobs: block needs fugaro " + jobsSince + " or later in the job image that runs it, and in the CLI of everyone who reads this layer: an older runner decodes the layer strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseJobsSkew refuses fugaro update --repo, before any cloud effect, when
// the project layer the checkout resolves writes jobs: and the installation's
// base image predates jobsSince — a runner built from it decodes the layer with
// KnownFields and refuses it at bootstrap. The states of ref are read as
// refuseRigorSkew reads them: no base image passes, one that is not a release
// only warns.
func refuseJobsSkew(repo string, fromLayer bool, ref string, warn io.Writer) error {
	if !fromLayer {
		return nil
	}
	subject, alt := jobsSubject(repo)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, jobsSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the layer strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, jobsSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkJobsImage and checkCloneJobsImage are the launch's own jobsSince gate,
// for a workflow whose job image is a derived one and for a checkout: clone
// workflow respectively: the pair every other per-workflow image gate has
// (checkImageSince/checkCloneImageSince), with checkGovernanceImage's judge and
// its one source — the project layer, which is the only file that can write the
// key. A launch whose configuration writes no jobs: is never gated.
func checkJobsImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	if !jobsLayerSet(launchFileConfig(ctx, env, spec)) {
		return nil
	}
	subject, alt := jobsSubject(spec.Repo)
	return checkImageSince(ctx, env, slug, spec, kind, jobsSince, subject, jobsKnows, "; or "+alt, warn)
}

// checkCloneJobsImage is checkJobsImage for a checkout: clone workflow: see
// checkCloneImageSince.
func checkCloneJobsImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	if !jobsLayerSet(launchFileConfig(ctx, env, spec)) {
		return nil
	}
	subject, alt := jobsSubject(spec.Repo)
	return checkCloneImageSince(env.lc, spec, jobsSince, subject, jobsKnows, "; or "+alt, warn)
}

// modelGradesSince is the first fugaro release whose config reader knows the
// escalation ladder's two model grades: fugaro.yaml's top-level model_grades:
// block (docs/outer-loop-escalation, D2039's E2) and the project layer's
// defaults.model_grades. Both files are decoded with KnownFields, so a runner
// built from an older base image refuses either key at bootstrap. Like
// planningSince, whose two homes are the same two, the judge is the
// installation's base image and the gate is fugaro update --repo's
// (refuseModelGradesSkew); fugaro run gates the launch too
// (checkModelGradesImage, checkCloneModelGradesImage), but only from a checkout
// of the repository, where it can read the file the run will, so outside one
// only fugaro update --repo checks, and fugaro validate warns
// (modelGradesImageWarnings).
//
// The repository's half is not a second opinion on the grade: a grade a
// repository file writes is dropped and recorded by the merge (G3, E2's note),
// so nothing a run reads changes. It is gated all the same, because what the
// older runner refuses is the key itself: the line an operator added to their
// fugaro.yaml is what dies at bootstrap, whoever was going to ignore it.
//
// It names 0.25.0, the release this work ships in ("Vole": docs/release-streams.yaml
// has 0.23.0 current and Uakari 0.24.0 next), which is not tagged yet: the
// release that tags it holds this number to the tag it cuts, as it does for
// slotSince's 0.24.0 and jobsSince's 0.22.0. docs/release.md ("Before you tag")
// has the checklist verify it.
const modelGradesSince = "0.25.0"

// modelGradesLaunchSources reports, for the launch's modelGradesSince gate,
// whether the configuration the run will read writes the key: the repository's
// fugaro.yaml (fromRepo, RepoModelGradesSet) or the project layer it resolves
// over (fromLayer, SetsModelGrades), read the way planningLaunchSources reads
// them. Both are the key being written and not the value it holds: an older
// runner's decoder refuses model_grades whatever it says — an explicit null,
// model_grades: {} and two full grades are one line to it — so judging the
// grades would launch the first two to die at bootstrap on a run already paid
// for.
func modelGradesLaunchSources(ctx context.Context, env *cloudEnv, spec *task.Spec) (fromRepo, fromLayer bool) {
	cfg := launchFileConfig(ctx, env, spec)
	if cfg == nil {
		return false, false
	}
	return cfg.RepoModelGradesSet, cfg.Layer.SetsModelGrades()
}

// modelGradesSubject names, for refuseModelGradesSkew's messages, which file
// wrote the key, and the way out that file's author has: the repository's block
// is the repository's to remove, while the layer's is read by every repository
// of the project. The layer's key is defaults.model_grades, the one under
// defaults:, rather than a top-level key of the layer file like jobs:'s — and
// it is the home the ladder reads, so removing the repository's block leaves
// the run with no grade at all rather than with a default.
func modelGradesSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const layerAlt = "remove defaults.model_grades from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's model_grades: block and the project layer's defaults.model_grades of " + repo,
			"remove the model_grades: block from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's defaults.model_grades (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's model_grades: block of " + repo, "remove the model_grades: block from fugaro.yaml"
}

// modelGradesKnows names, for the image gates' messages, the key the job
// image's runner has to know: the message reads "needs a job image whose runner
// knows <this>", so it has to name whichever of the two the config wrote.
func modelGradesKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the model_grades: block and the project layer's defaults.model_grades"
	case fromLayer:
		return "the project layer's defaults.model_grades"
	}
	return "the model_grades: block"
}

// refuseModelGradesSkew refuses fugaro update --repo, before any cloud effect,
// when the repository's fugaro.yaml writes a model_grades: block (fromRepo) or
// the project layer it resolves over writes defaults.model_grades (fromLayer)
// and the installation's base image predates modelGradesSince — a runner built
// from it decodes the file with KnownFields and refuses the run at bootstrap on
// a key it does not know. The states of ref are read as refuseRigorSkew reads
// them: no base image passes, one that is not a release only warns.
func refuseModelGradesSkew(repo string, fromRepo, fromLayer bool, ref string, warn io.Writer) error {
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := modelGradesSubject(repo, fromRepo, fromLayer)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, modelGradesSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, modelGradesSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkModelGradesImage and checkCloneModelGradesImage are the launch's own
// modelGradesSince gate, for a workflow whose job image is a derived one and
// for a checkout: clone workflow respectively: the pair every other
// per-workflow image gate has (checkImageSince and checkCloneImageSince),
// judged on the key the configuration the run will read writes
// (modelGradesLaunchSources). The refusal names the fix that kind of image has
// — fugaro image refresh for a built image, fugaro update --base base for the
// installation's own — and the way out the file's author has, from
// modelGradesSubject.
func checkModelGradesImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	fromRepo, fromLayer := modelGradesLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := modelGradesSubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, modelGradesSince, subject, modelGradesKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkCloneModelGradesImage is checkModelGradesImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneModelGradesImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	fromRepo, fromLayer := modelGradesLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := modelGradesSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(env.lc, spec, modelGradesSince, subject, modelGradesKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// modelGradesImageWarnings warns, once, that a configuration which writes the
// key — the repository's file (RepoModelGradesSet) or the project layer it
// resolved over (SetsModelGrades) — needs every job image on modelGradesSince.
// It is validate's half of checkModelGradesImage, and a warning rather than a
// Problem for rigorImageWarnings' reason: the file is valid, and what has to
// move is the installation (the base image, or a workflow's derived one), which
// is why both ways to move an image are named. It says which of the two wrote
// the key, planningImageWarnings' distinction for the same reason.
func modelGradesImageWarnings(c *config.Config) []config.Problem {
	if c == nil {
		return nil
	}
	fromRepo, fromLayer := c.RepoModelGradesSet, c.Layer.SetsModelGrades()
	if !fromRepo && !fromLayer {
		return nil
	}
	subject := "this file's model_grades: block"
	switch {
	case fromRepo && fromLayer:
		subject = "this file's model_grades: block and the project layer's defaults.model_grades"
	case fromLayer:
		subject = "the project layer's defaults.model_grades"
	}
	return []config.Problem{{Path: "model_grades", Message: subject + " needs fugaro " + modelGradesSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// securityLaunchSources reports, for the launch's securitySince gate, whether
// the configuration the run will read writes the key: the repository's
// fugaro.yaml (fromRepo, RepoSecuritySet) or the project layer it resolves over
// (fromLayer, the layer's top-level security: block), read the way
// rigorLaunchSources reads them.
//
// The key is the question and not the rows it holds: a runner older than
// securitySince decodes the file with KnownFields and refuses the key whatever
// it holds, so an empty block (`security: {}`) and a full list are the same line
// to it, and judging the rows would launch the first to die at bootstrap on a
// run already paid for.
//
// The configuration is launchFileConfig's, so the launch gates fire where the
// sibling gates' do: from a checkout of the repository, where the file the run
// will read can be read here at all (checkoutConfig, or the ref's file resolved
// over the layer the task carries, cascadeRefConfig). Outside one it is nil and
// nothing is gated, which is the shape rigorSince's gates have: there only
// fugaro update --repo checks, and the run's own bootstrap is what meets the
// key then.
func securityLaunchSources(ctx context.Context, env *cloudEnv, spec *task.Spec) (fromRepo, fromLayer bool) {
	cfg := launchFileConfig(ctx, env, spec)
	if cfg == nil {
		return false, false
	}
	return cfg.RepoSecuritySet, cfg.Layer.SetsSecurity()
}

// securitySubject names, for the gates' messages, which file wrote the key, and
// the way out that file's author has: the repository's block is the
// repository's to remove, while the layer's is read by every repository of the
// project. The layer's key is a top-level key of the layer file, like jobs:
// rather than a defaults.rigor.
func securitySubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const layerAlt = "remove the security: block from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's security: block and the project layer's security: block of " + repo,
			"remove the security: block from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's security: block (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's security: block of " + repo,
		"remove the security: block from fugaro.yaml to run under the engine's floor until every job image is current"
}

// securityKnows names, for the image gates' messages, the key the job image's
// runner has to know: the message reads "needs a job image whose runner knows
// <this>", so it has to name whichever of the two the config wrote.
func securityKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the security: block and the project layer's security: block"
	case fromLayer:
		return "the project layer's security: block"
	}
	return "the security: block"
}

// securityImageWarnings warns, once, that a configuration which writes the
// security: block — the repository's file (RepoSecuritySet) or the project
// layer it resolved over (SetsSecurity) — needs every job image on
// securitySince. It is validate's half of checkSecurityImage, and a warning
// rather than a Problem for rigorImageWarnings' reason: the file is valid, and
// what has to move is the installation (the base image, or a workflow's derived
// one), which is why both ways to move an image are named. It says which of the
// two wrote the key, the same distinction securitySubject draws for the launch
// gates, because the layer's key reaches every repository of the project and
// only its operator can remove it.
func securityImageWarnings(c *config.Config) []config.Problem {
	fromRepo, fromLayer := c.RepoSecuritySet, c.Layer.SetsSecurity()
	if !fromRepo && !fromLayer {
		return nil
	}
	subject := "this file's security: block"
	switch {
	case fromRepo && fromLayer:
		subject = "this file's security: block and the project layer's security: block"
	case fromLayer:
		subject = "the project layer's security: block"
	}
	return []config.Problem{{Path: "security", Message: subject + " needs fugaro " + securitySince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseSecuritySkew refuses fugaro update --repo, before any cloud effect,
// when the repository's fugaro.yaml writes the security: block (fromRepo) or
// the project layer it resolves over writes security: (fromLayer) and the
// installation's base image predates securitySince — a runner built from it
// decodes the file with KnownFields and refuses the run at bootstrap on a key
// it does not know. The states of ref are read as refuseRigorSkew reads them:
// no base image passes, one that is not a release only warns.
//
// Each half is the key being written and not the rows it holds: security: {}
// and an explicit null are the same line to an older runner's decoder as a full
// list, so judging the rows would deploy a job that dies at bootstrap on a line
// the operator did write.
func refuseSecuritySkew(repo string, fromRepo, fromLayer bool, ref string, warn io.Writer) error {
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := securitySubject(repo, fromRepo, fromLayer)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, securitySince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, securitySince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkSecurityImage and checkCloneSecurityImage are the launch's own
// securitySince gate, for a workflow whose job image is a derived one and for a
// checkout: clone workflow respectively: the pair every other config-key image
// gate has (checkRigorImage and checkCloneRigorImage), judged on the key the
// configuration the run will read writes (securityLaunchSources). The refusal
// names the fix that kind of image has — fugaro image refresh for a built
// image, fugaro update --base base for the installation's own — and the way
// out the file's author has, from securitySubject.
//
// The image is the judge because that is where the reader is: a runner older
// than securitySince decodes fugaro.yaml and the project layer with
// KnownFields, so the key alone kills the run at bootstrap, before the agent
// starts and after the launch was paid for.
func checkSecurityImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	fromRepo, fromLayer := securityLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := securitySubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, securitySince, subject, securityKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkCloneSecurityImage is checkSecurityImage for a checkout: clone workflow:
// see checkCloneImageSince.
func checkCloneSecurityImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	fromRepo, fromLayer := securityLaunchSources(ctx, env, spec)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := securitySubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(env.lc, spec, securitySince, subject, securityKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkSecurityRequestImage and checkCloneSecurityRequestImage are the launch's
// securityRequestSince gate, for a workflow whose job image is a derived one
// and for a checkout: clone workflow respectively: the pair the sibling task
// fields have (checkFollowUpImage, and run.go's own --title and --tier gates).
//
// The condition is the request's and not the flag's: a --retry takes no
// --security (taskFlags) and still carries the task's field when the run it
// replays was launched with one, a task written by hand carries it too, and the
// project layer's always_level_3 is the same request in the other home. A
// launch that carries none of the three is never gated, so the request is what
// decides and never the release the CLI building a task happens to be. cfg is
// the configuration the run will read (launchLevelsConfig), which is where the
// layer's half is judged.
//
// It is not checkSecurityImage's gate: that one judges the configuration the
// run will read (the repository's and the layer's security: blocks) against
// securitySince, and this one judges what reads it against the release whose
// runner acts on it. A launch can trip either, both, or neither.
func checkSecurityRequestImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, cfg *config.Config, kind string, warn io.Writer) error {
	fromTask, fromSwitch := securityRequestSources(spec, cfg)
	if !fromTask && !fromSwitch {
		return nil
	}
	subject, knows := securityRequestNames(fromTask, fromSwitch)
	return checkImageSince(ctx, env, slug, spec, kind, securityRequestSince, subject, knows, "", warn)
}

// checkCloneSecurityRequestImage is checkSecurityRequestImage for a checkout:
// clone workflow: see checkCloneImageSince.
func checkCloneSecurityRequestImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	fromTask, fromSwitch := securityRequestSources(spec, cfg)
	if !fromTask && !fromSwitch {
		return nil
	}
	subject, knows := securityRequestNames(fromTask, fromSwitch)
	return checkCloneImageSince(lc, spec, securityRequestSince, subject, knows, "", warn)
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
	fmt.Fprintf(warn, "warning: the job image of %s workflow %s is a core build of fugaro: it reads %s and ignores it, so nothing it sets is enforced by this run. Run fugaro update in its checkout, in your own terminal window, to have it enforced\n",
		spec.Repo, spec.Workflow, strings.Join(asks, " and "))
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
			warnings = append(warnings, config.Problem{Path: "workflow " + name, Message: "is a core build of fugaro: the job image of workflow " + name + " reads the governance: block and ignores it, so nothing it sets is enforced by a run of this repository. Run fugaro update in that workflow's checkout to have it enforced"})
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

// imageCatalogSkew is the image family's own gate for the node's
// image_catalog key (D1156, F14), the shape catalogSwitchSkew has for the
// price catalog's switches: it judges the installation's base image, the
// one the job a catalog image replaces is built from, because a job whose
// runner predates imageCatalogSince reads a node carrying the key as an
// unknown document. It is spelled with imageCatalogSince/imageCatalogShipped
// and never with catalogSince/catalogShipped, which are the price catalog's
// (F14): the two documents ship on their own schedules.
//
// shipped is the constant, passed rather than read so a test can ask both
// halves of the gate, exactly as catalogSwitchSkew's tests do. While no
// release reads the key, only a job image this release builds itself is
// safe: an image that is a release, or one nothing can date, is refused,
// because no release can be named that reads the key and an operator told
// otherwise would be sent to a CLI that does not exist. Once it ships, a
// release older than imageCatalogSince is refused and an undated ref is
// only warned about, the way checkCloneImageSince judges one.
//
// where names what the gate is being asked about, and ref is the
// installation's base image ("" when the config names none, which passes:
// infra.Repo then builds the job from this release's own image, which reads
// the key).
func imageCatalogSkew(where, ref string, shipped bool, warn io.Writer) error {
	if ref == "" {
		return nil
	}
	rel, ok := releaseOf(ref)
	switch {
	case !ok:
		if !shipped {
			return userErr("%s needs a job image whose fugaro reads the project policy's image_catalog key, but no fugaro release reads it yet (it ships at fugaro %s) and the installation's base image %s is not a release image, so nothing can be told about it. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, and fugaro update, before a catalog entry is named",
				where, imageCatalogSince, pluginwire.Printable(ref))
		}
		fmt.Fprintf(warn, "warning: %s needs a job image whose fugaro reads the project policy's image_catalog key, but the installation's base image is %s, which is not a release base image; whether it does is not checked\n",
			where, pluginwire.Printable(ref))
	case imagePredates(rel, imageCatalogSince):
		return userErr("%s needs a job image whose fugaro reads the project policy's image_catalog key (fugaro %s or later), but the installation's base image is %s, release %s. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, then fugaro update",
			where, imageCatalogSince, pluginwire.Printable(ref), rel)
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
