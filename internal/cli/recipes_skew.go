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
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runner"
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

// serviceAuthSince is the first fugaro release whose runner serves a switched
// `api-key` or `oauth` repository's MODEL calls through the installation's
// credentials service (B2.2, F30; D2071, D2072): such a run's job mounts
// neither the Anthropic key nor the Claude OAuth token from B2.7's flip on
// (config.ServiceServedAuths), so its model credential comes from the service
// and from nothing in its own environment. A runner older than this release reads the
// mounted key it has always read, and on a job that no longer mounts one it
// would have no credential at all: the gateway would open its upstream with an
// empty key and every stage would fail at the provider.
//
// The launch gate (serviceAuthGate, launch_budget.go) refuses such a launch,
// judged the way every other *Since gate is (checkImageSince, or
// checkCloneImageSince for a checkout: clone workflow). It carries no shipped
// flag, on proxySince's reasoning: refusing every image that predates it is
// exactly the direction the gate wants, since a switched run on an older runner
// cannot work at all.
//
// It names 0.25.0, the release that carries the RUNNER half (B2.2's
// gatewayUpstream and ServiceSwitchedAuth), and NOT the release the flip
// itself ships in — the two are one release apart here, and that is D2072's
// second step: B2.3 hands a switched repository's job the service's address in
// the same release as the flip, so from that release on the address and the
// dropped mount arrive together and an image of the release BETWEEN them
// (0.25.0) is the oldest one that can carry the pair. A number naming the
// flip's own release would refuse a 0.25.0 image that works; a number below
// 0.25.0 would admit one whose runner knows nothing of the service and dies at
// its first model call. docs/release.md's Before-you-tag list verifies it
// against the release actually cut, as it does for the other *Since constants,
// and proxySince's own comment above is the cautionary tale for a value that
// names the wrong release.
const serviceAuthSince = "0.25.0"

// identitySince is the first fugaro release whose runner claims its run's
// identity from the installation's credentials service (LM7; D3122): such a
// run's execution carries a one-time ticket in the contract's own variable
// (identitywire.TicketEnv, FUGARO_IDENTITY_TICKET), and the runner presents it
// to the service with the job's own Google ID token and a nonce it keeps for
// its retries, and exchanges the answer for the identity it runs under. From
// this release on the launcher signs nothing itself and writes no sealed
// object (R7, LM6): the ticket is the whole of what a run's identity travels
// on, so a runner older than this release reads neither the ticket nor the
// claim. It looks for the sealed token under tokens/ that its launcher no
// longer writes, finds nothing, and stops at bootstrap with no identity at
// all — with the launch already paid for.
//
// The launch gate (identityGate below) refuses such a launch, judged the way
// every other *Since gate is (checkImageSince, or checkCloneImageSince for a
// checkout: clone workflow): a release older than identitySince is refused, a
// base with no release tag (a dev build, a hand-pushed image) is only warned
// about — the run's own bootstrap refuses there, since a runner that cannot
// claim has nothing to fall back on. It carries no shipped flag, on
// proxySince's reasoning: refusing every image that predates it is exactly the
// direction the gate wants, since a run holding a ticket on an older runner
// cannot work at all.
//
// It is asked for a launch that holds a ticket and NOT for one on the bridge
// (R7, LM6's call site): a launch the service does not serve signs locally as
// before and its execution carries the old seal marker, which a runner of an
// older release still opens, so nothing about it is judged here.
//
// It names 0.29.0, the earliest release that can carry the claim, and not
// either of the two numbers a reader of this tree might expect instead: 0.27.0
// "Xerus" is the release docs/release-streams.yaml makes next and
// keys-through-service's release A is not in it (docs/status.md's gate list
// names no keys-through-service task, and the release's own notes say a
// provider's key is not taken until that plan's flip), while release A's own
// slot is the minor after that. D3128 puts this plan's tasks after release A
// and D3124 ships the work in one release, so the claim arrives in the release
// after release A's. Neither is tagged yet, so docs/release.md's Before-you-tag
// list is what holds this number to the tag actually cut, as it does for every
// other *Since constant here, and TestIdentitySinceHasAReleaseBullet holds that
// bullet to this value. A value below the release that really claims is the
// direction to fear: such a launch is admitted, paid for, and stops at
// bootstrap with no identity at all.
const identitySince = "0.29.0"

// identitySubject and identityKnows are this gate's two names in the family's
// sentence (checkImageSince): what is refused (a launch whose run claims its
// identity from the service) and what its job image's runner has to know (the
// claim the ticket stands for). The refusal carries no alt: the ticket is the
// whole of what a run's identity travels on, so there is no other way for an
// older image to be given one.
const (
	identitySubject = "a launch that holds a ticket for its run's identity (the launcher asks the installation's credentials service, and the run itself claims the identity the ticket stands for)"
	identityKnows   = "the identity claim (the ticket its execution carries, presented to the service with the job's own ID token and exchanged for the run's identity)"
)

// identityGate refuses a launch that holds a ticket for its run's identity on
// a job image whose runner cannot claim it (identitySince): such a run's
// execution carries the ticket and no sealed object, so a runner that does not
// know the claim stops at bootstrap with no identity at all.
//
// It is judged beside the seal's, the proxy's, the model credential's and the
// workflow secrets' gates, from the same build record and the same local
// config — but by its caller and not by a condition of its own: LM6 asks it
// where the launch really holds a ticket, and never where the service answered
// "not served" and the launch fell back to signing locally (R7). A launch on
// the bridge is judged by every gate it always was and nothing here.
func (e *cloudEnv) identityGate(ctx context.Context, slug string, spec *task.Spec, kind string, clone bool, warn io.Writer) error {
	if clone {
		return checkCloneImageSince(e.lc, spec, identitySince, identitySubject, identityKnows, "", warn)
	}
	return checkImageSince(ctx, e, slug, spec, kind, identitySince, identitySubject, identityKnows, "", warn)
}

// vaultSince is the first fugaro release whose runner fetches a workflow's own
// secrets from the installation's credentials service when its job does not
// mount them (B3.4, S3; D491, D1267, D2073-D2075): B3.8's flip
// (config.VaultFetchedSecretClasses) takes the mount of every workflow secret
// a switched repository's job held away, and from that fetch on the value
// arrives at bootstrap, in the runner's own environment, before the agent
// starts. A runner older than this release reads the mounted value it has
// always read, and on a job that no longer mounts one it would have nothing at
// all: agent.BuildEnv would refuse the run over a declared secret that is not
// set, which is a run that stops before any model call.
//
// The launch gate (vaultGate, launch_budget.go) refuses such a launch, judged
// the way every other *Since gate is (checkImageSince, or checkCloneImageSince
// for a checkout: clone workflow). It carries no shipped flag, on
// serviceAuthSince's reasoning: refusing every image that predates it is
// exactly the direction the gate wants, since a run whose mounts are gone on
// an older runner cannot work at all.
//
// It names 0.26.0, the release that carries the RUNNER half (B3.4's
// fetchWorkflowSecrets and the workflow-secrets route B3.2 answers it on), and
// NOT the release the flip itself ships in — the two are one release apart
// here, as they are for serviceAuthSince: the job reaches the service through
// the address and the grant the release BEFORE the flip already put in place
// (B2.3's URL, B3.3's per-container grant), so an image of that release is the
// oldest one that carries both halves of the pair, and the flip takes a mount
// away only where the serving is already there (G17). A number naming the
// flip's own release would refuse a 0.26.0 image that works; a number below
// 0.26.0 would admit one whose runner knows nothing of the fetch and dies at
// bootstrap over a secret it no longer mounts. docs/release.md's Before-you-tag
// list verifies it against the release actually cut, as it does for the other
// *Since constants.
const vaultSince = "0.26.0"

// vaultNameSince is the first fugaro release whose runner asks the credentials
// service for a workflow secret BY NAME — version 2 of the route, answered from
// the project record out of the fugaro_p_<name> grammar (IS2, C1, D3324,
// D3333) — and it is the second constant the flip's own gate is judged on.
//
// It is one release after vaultSince and not a second reading of it: an image
// at vaultSince knows the fetch but asks the version-1 way, naming the variable
// and reading the container the per-repository record maps it to. That
// container is the project copy IS6 stops deriving and IS13 retires and
// deletes, so a job of this release — which mounts none of its workflow's
// secrets — leaves such a runner with nothing to read and it stops at bootstrap
// over a declared secret (the same failure vaultSince refuses one release
// earlier). It names 0.30.0, the release this plan's S2 and S3 ship in
// together, the same number l8Since carries for that reason, and
// docs/release.md's Before-you-tag list is what holds it to the release
// actually cut — TestVaultNameSinceHasAReleaseBullet is the test that reads
// that bullet. The direction to fear is the same one: a value below the release
// that carries IS2 admits a launch whose runner cannot fetch by name.
const vaultNameSince = "0.30.0"

// vaultNameSubject and vaultNameKnows are this gate's two names in the
// family's sentence (checkImageSince): what is refused (a launch whose run
// fetches its workflow secrets by name) and what its job image's runner has to
// know (the by-name fetch, version 2 of the route). The refusal carries no alt:
// the name is the whole of what the fetch travels on, so there is no other way
// for an older image to be given one.
const (
	vaultNameSubject = "a run of a repository whose workflow secrets are brokered by name (the job mounts none of them, and the run asks the installation's credentials service for each declared secret's own name rather than for the container a record maps its variable to)"
	vaultNameKnows   = "the by-name fetch (version 2 of the credentials service's workflow-secrets route, answered from the project record out of the fugaro_p_ grammar)"
)

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
// fails at bootstrap with an error naming a field nobody typed. It names
// 0.7.5, a release that is built and tagged and whose runner reads both keys
// (internal/runner/providers_test.go decodes them), so this constant's job is
// the same as recipesSince's above: docs/release.md's ("Before you tag")
// checklist verifies it equals the release that ships them.
const catalogSince = "0.7.5"

// catalogShipped is true: the release named by catalogSince exists and reads
// a route's require_zdr and price_policy, so a job whose
// FUGARO_MODEL_PROVIDERS value carries either key can be run by a runner that
// was built from it. It was flipped after 0.7.5 was tagged rather than in that
// release, which is why docs/release.md's checklist line for catalogSince is
// the one that records it had been left false — and why the two things the
// flip makes inert are stated here rather than in that release's notes:
//   - catalogSwitchSkew's unshipped branch (the "no fugaro release reads those
//     route keys yet" refusal) no longer fires. A route that sets either key
//     is judged by the installation's base image the way checkCloneImageSince
//     judges a checkout: an image older than catalogSince is refused, naming
//     the release and `fugaro update --base base`, a ref with no release tag
//     (a dev build, a hand-pushed image, an --image-source mirror) is only
//     warned about, and an installation with no base image at all passes
//     because infra.Repo then builds the job from this release's own image;
//   - the key really is deployable end to end: fugaro update --repo and
//     fugaro setup --repo carry the value into the job's FUGARO_MODEL_PROVIDERS.
//
// Both stay in the tree, behind this constant, for the same reason every other
// shipped flag here keeps its gate: the flip is one boolean, and a test that
// pins the shipped behavior (TestRefuseCatalogSwitches, TestGateCatalogSwitches,
// TestInitRepoDeploysCatalogSwitches) fails the moment it is flipped back.
// Nothing here should ever flip it back: 0.7.5 ships a runner that reads both
// keys, and a later release that had to un-ship them would be a different
// decision, made in its own PR.
const catalogShipped = true

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
// release, the same fact the pair above keeps for the catalog key itself, and
// it is true as of 0.26.0, the release that first writes — and reads — a
// catalog source's context member. The two gates are one question asked of two
// readers — readiness holds back the job whose image comes from such an entry,
// and policyContextRefusal refuses to publish the node that carries it — and
// they move together, in the commit that bumps this constant to the release
// that reads the member: the member is not optional the way the catalog key
// is, so an older runner refuses the whole image_catalog block rather than
// skipping it (D1403).
const imageContextShipped = true

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
// docs/managed-deployer-fence/spec.md), and no test can ask Google whether
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
// managed project (a creation) writes the scoped half, and
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

// reportsSince is the first fugaro release whose CLI answers fugaro runs
// reports --job: the command the project's reports job executes, which writes
// every repository's closed periods into reports/ of the runs bucket. A build
// before it knows no such command, so a firing that reaches such a job image
// fails with "unknown command" and writes no report at all.
//
// It is the release's own *Since constant beside digestSince because the two
// are the two halves of one series and neither implies the other: digestSince
// is the release whose runner writes a report run into a checkout's docs tree
// (T3.1's step, which a recipe asks for), and reportsSince is the release
// whose CLI writes the same reports into the project's bucket with no recipe
// and no model call. A skill or a recipe that names fugaro runs reports is
// gated at this value, and the schedule that runs it is the installation's own
// setting (jobs.reports.paused), which R7 deploys stopped: a schedule an
// installation turns on is on a CLI of this release or, since fugaro update
// upgrades launchers and the job image together, of a later one.
//
// docs/release.md's ("Before you tag") checklist verifies it equals the
// release that ships the command.
const reportsSince = "0.26.0"

// commitReportsSince is the first fugaro release whose runner reads the
// reports a task hands in (R9): the commit_reports step downloads every object
// task.Spec.Reports names — runs/<slug>/<run>/reports/<name>, uploaded by the
// launcher that handed them in — checks each one against the size and the
// sha256 the task carries, and commits it into the mirror folder.
//
// The member is what makes this gate necessary rather than a warning: a runner
// before it has no such field, and task.Parse decodes with
// DisallowUnknownFields, so the WHOLE task is refused at bootstrap — after the
// launcher read the project's stored reports, uploaded them and started a job
// — with nothing the run can do about it. So a --commit-reports launch against
// an older job image is refused at the person's terminal, by this gate, before
// the first byte leaves the machine.
//
// Like artifactsSince and digestSince it carries no shipped flag: refusing
// every image that predates the member is exactly the direction this gate
// wants, and an image with no release tag (a dev build, a hand-pushed one)
// only warns, the same as for every other *Since constant here — the runner
// fails closed on the unknown field anyway, so a warning cannot let a commit
// run through as an ordinary one.
//
// docs/release.md's ("Before you tag") checklist verifies it equals the
// release that ships the member.
const commitReportsSince = "0.27.0"

// commitReportsSubject and commitReportsKnows are this gate's two names in the
// family's sentence (checkImageSince): what is refused (the launch carrying
// --commit-reports) and what its job image's runner has to know (the stored
// reports a task hands in, which is R9's step reading the member). The refusal
// carries no alt: the member is the whole of what the flag does, so there is
// no other way to hand reports in that an older image could take.
const (
	commitReportsSubject = "--commit-reports"
	commitReportsKnows   = "the stored reports a task hands in"
)

// checkCommitReportsImage is checkImageSince's arm for a --commit-reports
// launch: a launch whose job image's runner cannot read the reports member is
// refused here, at the person's terminal, rather than starting a job that
// refuses the whole task at bootstrap.
func checkCommitReportsImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	return checkImageSince(ctx, env, slug, spec, kind, commitReportsSince, commitReportsSubject, commitReportsKnows, "", warn)
}

// checkCloneCommitReportsImage is checkCommitReportsImage for a checkout:
// clone workflow: see checkCloneImageSince.
func checkCloneCommitReportsImage(lc *localcfg.Config, spec *task.Spec, warn io.Writer) error {
	return checkCloneImageSince(lc, spec, commitReportsSince, commitReportsSubject, commitReportsKnows, "", warn)
}

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

// levelRolesSince is the first fugaro release whose recipe reader knows the
// by_level: key (LD9): the recipe's one conditional, the roles mapping a
// recipe resolves at a rigor level. A runner before it does not drop the key
// — it refuses the whole recipe ("by_level is not a recipe key"), so a launch
// carrying one dies at bootstrap with the launch already paid for. This gate
// turns that into a refusal at the launcher's terminal, naming fugaro image
// refresh, exactly as adversarialSince does for its own construct.
//
// What the gate protects is the parse and not a behaviour: no run applies the
// block yet (recipe.RolesFor has no caller, and docs/recipes.md says so where
// it describes the key), so the launch an older image would take and then
// refuse at bootstrap is one it would refuse whatever its entries hold —
// which is exactly why the judge below is the key's presence.
//
// The key's presence is the whole of what the gate judges: recipe.UsesLevelRoles
// decides by the key and never by what its entries hold, because an older
// parser refuses by_level: {} too, and the recipe that survives is still one
// the run cannot read.
//
// It carries no shipped flag, like artifactsSince and escalationSince:
// refusing every image that predates the key is exactly the direction this
// gate wants, and an image with no release tag (a dev build, a hand-pushed
// one) only warns, the same as for every other *Since constant here — safe
// either way, since the runner fails closed on the unknown key anyway.
//
// It names 0.27.0, the release this work is slotted for
// (docs/release-streams.yaml has 0.26.0 current and Xerus 0.27.0 next), which
// is not tagged yet: docs/release.md's ("Before you tag") checklist verifies
// the constant against the tag actually cut, the way rigorSpellingSince's
// bullet records, and moves it if the key lands in another release.
const levelRolesSince = "0.27.0"

// mergeJudgeSince is the first fugaro release whose runner runs the merge
// judge (E6, D2031): its recipe parser knows `merge_judge` as a kind, and
// mode: evaluate's own step list carries it, so the judge's recipe — one step,
// and that step — is a run; and its configuration reader knows the judge's
// craft, `agent.crafts.merge-judge`, which the five-craft block adds (its
// `Crafts` struct carries a field per craft, so the decoder refuses the field
// and not the block).
//
// **It is not evaluateSince, and the two are a release apart.** 0.24.0 reads
// mode: evaluate, which is the mode the judge's task carries, so that gate
// admits a launch against a 0.24.0 job image and cannot tell this run from any
// other evaluate one; its parser then refuses `merge_judge` outright ("is not a
// step type") and its decoder refuses the craft key ("field merge-judge not
// found in type config.Crafts"), so the run dies at bootstrap with the launch
// already paid for and nothing judged. What judges a launch is therefore the
// recipe's own constructs (recipe.UsesMergeJudge, read from the embedded text a
// catalog launch carries) and the catalog's own name, for the task that names
// it and carries no text — the two arms of recipeSinceFor, not the mode's —
// and, for the craft key, the two files the configuration writes it in
// (mergeJudgeCraftLaunchSources, checkMergeJudgeCraftImage).
//
// The two halves are one constant because they are one release and one
// feature, but they are judged apart and a launch may carry either without the
// other: a repository's file may set the judge's craft with no judge run ever
// launched from it (the ladder launches the judge, and its task is the
// ladder's), and a judge run may be launched where no layer sets the craft,
// the judge then following the coder.
//
// It names 0.25.0, the release this work lands in, which is not tagged yet:
// docs/release.md's ("Before you tag") checklist verifies the constant against
// the tag actually cut, the way digestSince's, adversarialSince's and
// evaluateSince's bullets record. A wrong value refuses too late (a launch
// reaches a runner that refuses its recipe or its configuration) or too early
// (an image that can judge stays blocked).
const mergeJudgeSince = "0.25.0"

// escalationRecipes are the catalog recipe names whose runner half ships in
// escalationSince's release: `escalate` (one coder pass, one review, the
// verdict recorded) and `rebase`, which that release adds. A recipe of the
// same name from the repository or the project is that layer's own file and is
// judged by the constructs it carries, like any other: this list is the
// catalog's names, not the names. `merge-judge`, the third of the ladder's
// recipes (E6), is deliberately not here: its runner half ships a release
// later, and mergeJudgeRecipeNamed holds it to that one.
var escalationRecipes = []string{"escalate", "rebase"}

// escalationRecipeNamed reports whether the task's recipe is one of the
// catalog's escalation recipes: a name the launcher resolved from the catalog
// itself, never a repository's or the project's own file that happens to
// share it (resolveRecipe reads those first, and they win). It is the floor
// under the construct gates: a recipe whose text carries no hand_over and no
// rebase step this launcher can see — or either of the two whose text a later
// release edits to use fewer constructs — is still held to the release whose
// runner runs the ladder's recipes, so an image that cannot take the launch is
// refused at the terminal rather than at bootstrap.
func escalationRecipeNamed(spec *task.Spec) bool {
	return spec.Recipe != nil && spec.Recipe.Source == string(recipe.SourceCatalog) && slices.Contains(escalationRecipes, spec.Recipe.Name)
}

// mergeJudgeRecipe is the catalog recipe the judge run is launched by, and
// mergeJudgeRecipeNamed reports whether the task's recipe is that catalog
// entry: a name the launcher resolved from the catalog itself, never a
// repository's or the project's own file that happens to share it.
//
// It is the name arm mergeJudgeSince needs beside the construct one: a catalog
// launch carries the recipe's text, which recipe.UsesMergeJudge reads, but a
// task naming the catalog entry and carrying no text (a hand-written task.json,
// or a later release's launcher) has no construct to read, and the name is then
// the only thing that says which run this is.
const mergeJudgeRecipe = "merge-judge"

func mergeJudgeRecipeNamed(spec *task.Spec) bool {
	return spec.Recipe != nil && spec.Recipe.Source == string(recipe.SourceCatalog) && spec.Recipe.Name == mergeJudgeRecipe
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

// reviewSpecSince is the first fugaro release whose runner reads a review's
// spec off the task it was handed (task.Spec.ReviewSpec, FromFindings and
// OwnText; docs/review-against-task R2): the task a pull request was written
// for, the follow-up's text beside it, and the person's own words on a
// findings follow-up. task.Parse decodes with DisallowUnknownFields, so an
// older runner refuses the WHOLE spec at bootstrap — bytes in the bucket
// under a run that never starts — which is why the launcher judges the image
// before it writes one.
//
// It is judged twice, and the two are the two halves of D2933:
//
//   - The FIELDS are omitted, never refused: a review whose job image
//     predates this release runs code-only, and the launcher says so in one
//     line naming `fugaro update` (and in `--json`, D2933 as amended). The
//     run is worth more than the check, so the launch proceeds — the
//     direction baseBranchSkew and authorSkew take for their own fields.
//   - The repository's own KEY (agent.review_against_task, S-KNOB) is
//     refused at launch instead, as triageSince's key is: its holder asked
//     for the check, and a job that cannot run it would answer a request
//     nobody could honor. `fugaro validate` warns where a launch would
//     refuse.
//
// It gates the field and never the flag, exactly as contextReposSince does: a
// launch that carries none of the three fields is never judged, and a --retry
// of a task that carries them is judged the way the launch that wrote it was.
// It names 0.27.0, the release this work ships in, which is not tagged yet:
// docs/release.md's ("Before you tag") list verifies the number against the
// tag actually cut, the way digestSince's and reviewLevelsSince's bullets
// record.
const reviewSpecSince = "0.27.0"

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

// bitbucketTokenSince is the first fugaro release whose credentials service
// reads the bundle's typed Bitbucket token family (config.BitbucketTokenPrefix,
// D2350, R13 of docs/bitbucket-token-types): the keys
// bitbucket-token--<name>, each holding one access token the service's proxy
// serves. It is the family's own entry of the same kind as providerEntrySince
// above, and it is a constant of its own rather than that one for the reason
// that entry's comment gives: the loader refuses the WHOLE document for a key
// it does not know, so the release a document is readable by is decided by the
// newest entry it holds and not by one release for the document.
//
// `fugaro creds set` is the bundle's only writer, so the guard is at that write
// too (creds.go's refuseAnEntryTheServiceCannotRead, which judges the release
// that SERVES the document rather than the installation's pin).
//
// It names 0.24.0, the release T1's own commit (`6eda5ddf`, #2591) went out in
// and therefore the first release whose credentials service was built with the
// family's reader. The direction is providerEntrySince's: the constant is the
// tag that shipped the key and not the release this guard is written in, and a
// release that raised it would refuse `fugaro creds set bitbucket-token--<name>`
// on installations whose deployed service already reads the family — which is
// every installation on that release — with `fugaro update` no remedy for them.
// docs/release.md's ("Before you tag") list carries that reading.
const bitbucketTokenSince = "0.24.0"

// keyCredentialSince is the first fugaro release whose credentials service
// reads the bundle's key-credential family (config.CredentialSecret,
// config.CredentialBySecret, D1670 of docs/model-crafts, MC23): the entries
// credential-<name>-key, each holding one declared credential's key — the
// service-served half of the types config.CredentialServingPath answers
// CredentialPathCredsvc for, offered to a craft's calls at the tier of a paid
// API key exactly as the project's own Anthropic key is. It is the family's own
// entry of the same kind as providerEntrySince above, and a constant of its own
// for the reason that entry's comment gives: the loader refuses the WHOLE
// document for a key it does not know, so the release a document is readable by
// is decided by the newest entry it holds and not by one release for the
// document.
//
// `fugaro creds set` is the bundle's only writer, so the guard is at that write
// too (creds.go's refuseAnEntryTheServiceCannotRead, which judges the release
// that SERVES the document rather than the installation's pin). It is the
// guard's third entry and not a second writer: a RUNNER-path key type has been
// refused at that write since KS2 (D1670 reversed by D2975) and its key is
// written into repoCredentialSince's family instead, so the entries of THIS
// family are the service-served credentials' alone and no write of a provider
// key adds one.
//
// It names 0.26.0, the release this work ships in, and it is the first constant
// of this file whose reader has never been in a released image: MC22's
// declaration and MC23's halves are both newer than the `v0.25.0` tag, so the
// release before this one serves a bundle without the family and cannot read a
// document that holds it. That is what makes 0.26.0 the answer and not 0.25.0 —
// a constant naming a released version would clear the write that halts that
// version's service. The test that holds providerEntrySince and
// bitbucketTokenSince to a release at or below docs/release-streams.yaml's
// `current` cannot be written in that form here — no release at or below that
// `current` (the 0.25.0 tag) reads the family — so the value is verified against
// the tag actually cut, as slotSince's and modelGradesSince's bullets record;
// TestKeyCredentialSinceIsNotAheadOfTheReleaseThatShipsIt holds the same bound
// one release looser while the family is unreleased (never above the stream's
// own release or the next one), which becomes the siblings' assertion itself
// from 0.26.0 on, and TestKeyCredentialSinceHasAReleaseBullet holds the
// checklist's bullet.
//
// The direction the siblings' tests protect holds from 0.26.0 on, and it is the
// one to watch at the next release: a value ABOVE the tag that shipped the
// family refuses `fugaro creds set credential-<name>-key` on installations whose
// deployed service already reads it, with `fugaro update` no remedy for them.
// docs/release.md's ("Before you tag") list carries that reading.
const keyCredentialSince = "0.26.0"

// repoCredentialSince is the first fugaro release whose credentials service
// reads the bundle's repository-credential family (credsvc.RepoCredentialPrefix,
// credsvc.RepoCredentialKey, C1 of docs/keys-through-service): the entries
// repo-credential--<slug>--<credential>, one per (repository, provider
// credential) pair, each holding the provider key and the two routing facts the
// service attaches when it forwards a call (credsvc's providerFacts). It is a
// fourth entry of the same kind as providerEntrySince above and a constant of
// its own for the reason that entry's comment gives: the loader refuses the
// WHOLE document for a key it does not know, so the release a document is
// readable by is decided by the newest entry it holds.
//
// The guard is no longer one write's: the family has THREE writers (F27) — the
// key credential's own `fugaro creds set credential-<name>-key`, `fugaro
// accounts add --type openrouter`, and `fugaro secrets set` of a model
// provider's own secret, at repository scope and project-wide — and all three
// go through refuseAnEntryTheServiceCannotRead before the value is read
// (creds.go's providerKeyBundleWrite).
//
// It names 0.27.0, the release this work ships in, and it is the second
// constant of this file whose reader has never been in a released image: C1's
// wire and KS1's loader are newer than the `v0.26.1` tag, so the release before
// this one serves a bundle without the family and cannot read a document that
// holds it. The direction the siblings' tests protect holds from 0.27.0 on: a
// value ABOVE the tag that shipped the family refuses every provider-key write
// on installations whose deployed service already reads it, with `fugaro
// update` no remedy for them. docs/release.md's ("Before you tag") list carries
// that reading, and TestRepoCredentialSinceHasAReleaseBullet holds its bullet.
const repoCredentialSince = "0.27.0"

// gitSecretIDSince is the first fugaro release whose runner reads
// CheckJobSpec's git_secret_id (internal/infra/spec.go): the daily image
// check job's own rebuild names the secret ID Readiness resolved, so a
// project-wide provider credential resolves for the rebuild exactly as it
// does for the job's clone. A runner before it ignores the field (plain
// json.Unmarshal) and falls back to the repository's own ID, which mounts a
// secret with no version once that name has moved to project scope (design
// docs/project-secrets/spec.md §5, §6 step 4a).
//
// It is a genuine, narrow exception to P6's "no *Since constant per feature":
// unlike every other gate here, it is not judged against a build record
// (there is none to judge: the check job always runs the base image itself),
// but against ready.Check.Image, the check job's own base ref, in planRepo --
// see the gate there. docs/release.md ("Before you tag") has the checklist
// verify it equals the release that ships the field.
const gitSecretIDSince = "0.7.5"

// namedTokensSince is the first fugaro release whose runner reads a task's
// oauth_token field (docs/named-oauth-tokens/spec.md); an older runner
// refuses it, since task.Spec's decoder disallows unknown fields. It must
// equal the release that ships task.Spec.OAuthToken: docs/release.md
// ("Before you tag") has the release checklist verify it. internal/cli/run.go
// gates a launch that would set OAuthToken to a non-default name against an
// older build record, the same way checkImageSince already gates
// recipesSince and layeredSince.
const namedTokensSince = "0.6.5"

// sealSince is the first fugaro release whose runner reads a sealed budget
// token (docs/budget-token-channel/spec.md §7.1): the marker's value is the
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
// (artifactsSince). A launch with no title at all is refused before it is
// gated (createTask), and a plain `--pr` follow-up carries its chain's title,
// so the gate judges a follow-up's inherited title like any other.
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

// externalSince is the first fugaro release whose runner reads a task's
// external field (task.Spec.External, the id of the ticket outside Fugaro the
// work item stands for): an older runner refuses the field outright, since
// task.Parse decodes with DisallowUnknownFields, and the run would fail at
// bootstrap on a task nobody can read.
//
// It differs from planTaskSince beside it in what a launch does about that,
// and the difference is the field's rather than the release's: the plan task
// is a refusal (internal/cli/run.go's gate, which the daemon's launch reaches
// too, because an item's plan task is part of what a person approved), while
// the external id is a drop (externalSkew). What is lost by dropping it is
// one `Fixes #<n>` line in the pull request the run opens, and the approved
// attempt that a refused bootstrap would spend is worth more than the link.
//
// It names 0.26.0, the release this work ships in: the whole orchestrator
// plan's daemon half lands there (docs/releases/v0.26.0.md), and the field
// has no operator order to record, because a launch never refuses over it —
// so this constant is verified against the tag actually cut (a number ahead
// of it, like cascadeSince's 0.10.0) by whoever cuts the release and not by
// a "Before you tag" bullet, which contextReposSince and changeSetSince
// beside it do without for the same reason. A wrong value here writes the
// field onto an image whose runner cannot read it (the run dies at bootstrap
// over a task nobody can read) or drops it for one that could (the run opens
// its pull request with no `Fixes` line, which nothing else reports).
const externalSince = "0.26.0"

// compressionSince is the first fugaro release whose runner reads a task's
// compression_off field (task.Spec.CompressionOff, fugaro run
// --no-compression): an older runner refuses the field outright, since
// task.Parse decodes with DisallowUnknownFields, and the run would fail at
// bootstrap on a task nobody can read. It must equal the release that ships
// task.Spec.CompressionOff, and docs/release.md ("Before you tag") is where
// that is verified: its bullet for this constant does not exist yet, because
// it is written with the release that cuts the feature -- Task 9 of
// docs/headroom-compression/implementation-plan.md, whose Task 6 added this
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
// project layer or profile may set it (docs/layered-config/spec.md §5), so
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

// rigorSpellingSince is the first fugaro release whose config reader knows the
// two spellings of the rigor configuration this release adds to the ones
// rigorSince shipped: an area rule's `level:` key, and the scalar shorthand
// `rigor: <level>`, which says what a block carrying only `default` says (D20).
//
// It is not rigorSince, which is older and answers a different question: that
// one names the release whose reader knows the rigor: key at all, and its
// judge booleans (RepoRigorSet, SetsRigor) are spelling-agnostic — a file
// writing either new spelling passes them on every installation newer than
// 0.11.0. What dies then is the run: a runner older than this constant meets
// `level:` as "field level not found in type rigor.Area" and the shorthand as
// "cannot unmarshal !!str into config.Rigor", both at bootstrap, in a file it
// decodes with KnownFields, and the project layer's area key does it to every
// repository of the project at once. Like rigorSince the judge is the
// installation's base image and the gate is fugaro update --repo's
// (refuseRigorSpellingSkew); fugaro run gates the launch too
// (checkRigorSpellingImage, checkCloneRigorSpellingImage), but only from a
// checkout of the repository, where it can read the file the run will, so
// outside one only fugaro update --repo checks.
//
// Both spellings are read from the text and never from the parsed
// configuration, which folds them into the block's own fields (the loader
// drops the spelling once it has read it): Config.RepoRigorLevelSet,
// Config.RepoRigorScalarSet and ProjectLayer.SetsRigorLevel.
//
// It names 0.27.0, the release this work is slotted for (docs/release.md's
// Before-you-tag bullet verifies it against the tag actually cut), which is not
// tagged yet: a wrong value refuses too late (a run on an image whose runner
// cannot read the spelling dies at bootstrap) or too early (an image that knows
// it stays blocked).
const rigorSpellingSince = "0.27.0"

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

// promoteSince is the first fugaro release whose runner reads a task's promote
// block (task.Spec.Promote, fugaro run --promote): an older runner refuses the
// whole spec outright, since task.Parse decodes with DisallowUnknownFields, and
// the run would fail at bootstrap on a task nobody can read — after the launch
// was paid for. It must equal the release that ships task.Spec.Promote:
// docs/release.md ("Before you tag") has the release checklist verify it, and
// TestPromoteSinceHasAReleaseBullet holds the two together.
//
// It is not rigorRequestSince (0.11.1), which gates the two fields a promote
// launch also carries (Spec.Rigor and Spec.Paths, the mode and the area's own
// paths): that constant names a release far older, so gating the block on it
// would pass an image that knows the request and not the promote — the
// [0.11.1, promoteSince) bracket, which is every image today — and the run
// would die at bootstrap with the launch already paid for. A promote launch is
// therefore judged on this constant rather than that one, and the two are
// checked in that order (run.go's finishLaunch): for a promote launch the
// refusal a person can act on names the block they asked for.
//
// It names 0.29.0 "Zander", the release that ships task.Spec.Promote, which is
// **not tagged yet**: verify the constant against the tag actually cut, the way
// keyCredentialSince's bullet records, and move it if the block lands in
// another release. It is not 0.28.0 "Yak", which is tagged and whose runner
// knows no promote block: that value would admit exactly the image this gate
// exists to refuse, and the launch would be paid for and die at bootstrap — the
// mistake a value pinned to the release a reader of this tree expected the work
// to land in makes once that release has shipped without it. A wrong value
// refuses too late (an older runner dies at bootstrap on the stored task) or
// too early (an image that reads the block stays blocked).
const promoteSince = "0.29.0"

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

// contextReposSince is the release whose runner reads a task's read-only
// context repositories (task.Spec.ContextRepos, fugaro run --context, plan
// task C5a). task.Parse decodes with DisallowUnknownFields, so a runner older
// than this refuses the whole task at bootstrap: the run would die over a
// field it cannot read, and the launch is gated here instead of being started
// on such a job.
//
// It gates the field and never the flag, exactly as launchLimitsSince does: a
// launch that names no context repository writes none (the field is omitempty,
// so the spec is byte-identical to what the previous release wrote) and is
// never gated, while a --retry of a task that carries one is gated the same
// way the launch that wrote it was.
const contextReposSince = "0.26.0"

// contextReposKnows is what such a runner knows, as the gate names it.
const contextReposKnows = "the read-only context repositories its task carries (context_repos)"

// contextReposSubject is what the gate says the launch carries: the flag, and
// the stored task a --retry replays the field from.
const contextReposSubject = "read-only context repositories (--context, kept by --retry and by a stored task)"

// contextReposAlt is the gate's other cure: the launch can name none, where
// the image cannot be brought forward.
const contextReposAlt = "; or drop --context, which needs a job image whose runner reads the field before it can carry one"

// changeSetSince is the release whose runner reads a task's change set
// (task.Spec.ChangeSet, fugaro run --change-set, plan task D1). task.Parse
// decodes with DisallowUnknownFields, so a runner older than this refuses the
// whole task at bootstrap: the member run would die over a field it cannot
// read, which is why the launch is gated here rather than started on such a
// job.
//
// It gates the field and never the flag, exactly as contextReposSince does: a
// launch that is in no change set writes none (the field is omitempty, so the
// spec is byte-identical to what the previous release wrote) and is never
// gated, while a --retry of a task that carries one is gated the same way the
// launch that wrote it was.
const changeSetSince = "0.26.0"

// changeSetKnows is what such a runner knows, as the gate names it.
const changeSetKnows = "the change set its task is a member of (change_set)"

// changeSetSubject is what the gate says the launch carries: the flag, and
// the stored task a --retry replays the field from.
const changeSetSubject = "a change set (--change-set, kept by --retry and by a stored task)"

// changeSetAlt is the gate's other cure: the launch can name no change set,
// where the image cannot be brought forward.
const changeSetAlt = "; or drop --change-set, which needs a job image whose runner reads the field before it can carry one"

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

// promoteGateSubject is what the promoteSince gate calls the field it judges:
// the block a promote launch writes, named by the flag that wrote it — the
// fields a runner older than promoteSince refuses are the block's
// (task.Promote), and a refusal that named the launch's mode instead would
// send its reader to a flag whose own gate is older and already passed.
func promoteGateSubject(spec *task.Spec) string {
	return fmt.Sprintf("the promote run's own block (--promote %s, which names the area and its paths)", spec.Promote.Area)
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
// recipeSinceFor's adversarial and level-roles arms read it: those are the
// constructs whose gates the task's own fields cannot carry (see
// recipeSinceFor). nil for
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
// The level's role mapping, the judge's step, the adversarial step and the
// escalation constructs are judged from rp too, and — the level's role
// mapping first (levelRolesSince, the newest), then the judge's step, the
// escalation constructs and the adversarial step — they are the newest a
// recipe can carry, so their
// cases come first. Unlike the manifest and mode: artifact — a repository
// recipe's digest run and its mode both reach the task as fields, which carry
// their own gates above — none of those three has anything in the task behind
// it, so the launcher's read of the file the recipe names is the only place
// the gate can see it; one this launcher cannot read keeps recipesSince, with
// the runner's parse refusal as the backstop. Every older construct keeps the
// gate it always had, judged from the embedded text alone: a repository recipe
// trips none of them here, which is what its own tests pin.
//
// The catalog's escalation recipes are held to escalationSince by name as
// well (escalationRecipeNamed), which is the floor under the constructs: the
// name is what the ladder's launches and a person's terminal carry, and the
// gate must not move back if a later release edits one of those files to use
// fewer of them. The judge's recipe is the third of those names and is held
// the same way, to its own, newer release (mergeJudgeRecipeNamed).
func recipeSinceFor(spec *task.Spec, rp *recipe.Recipe) string {
	r := embeddedRecipeOf(spec)
	if len(spec.DigestRuns) == 0 && r == nil && rp != nil {
		switch {
		case recipe.UsesLevelRoles(rp):
			return levelRolesSince
		case recipe.UsesMergeJudge(rp):
			return mergeJudgeSince
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
	case recipe.UsesLevelRoles(r):
		// The key is 0.27.0's, newer than every construct below, so it is
		// the binding gate whenever it is written: a launch from a 0.24.0
		// image is refused here whatever else the recipe holds.
		since = levelRolesSince
	case recipe.UsesMergeJudge(r):
		since = mergeJudgeSince
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
	if mergeJudgeRecipeNamed(spec) {
		return mergeJudgeSince
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
			"Run fugaro update in its checkout, in your own terminal window (never in a coding agent's session; its images stage verifies %s, pins it by digest through the installation's remote repository and rebuilds the workflow's image)%s%s",
			subject, knows, since, spec.Repo, spec.Workflow, why, base, alt, causeMarkerJobImage)
	}
	b, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	if entry, ok := launchCatalogEntry(ctx, env, slug, spec); ok {
		return checkCatalogEntrySince(ctx, env, b, slug, spec, entry, since, subject, knows, warn)
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

// launchCatalogEntry is the project image catalog entry the job this launch
// starts runs, and whether it runs one at all. A repository whose image is the
// project's catalog entry has no build record of its own to judge: its
// per-workflow record is one `fugaro image build --workflow` and `image
// refresh` refuse to write while repo-level images are disabled, so it stays
// at whatever base it had when the catalog took over, and judging it would
// refuse a launch on an image nothing runs. That is as true for a launch from
// outside the repository's checkout as from inside it: the entry is the
// deployed job's image and the project's map is what names it for this
// repository, neither of which a checkout is needed for
// (deployedCatalogEntry). A failed resolution is not an answer, so the
// repository's own record is judged as before.
func launchCatalogEntry(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) (config.Entry, bool) {
	entry, ok, _, err := deployedCatalogEntry(ctx, env, slug, spec)
	if err != nil || !ok {
		return config.Entry{}, false
	}
	return entry, true
}

// checkCatalogEntrySince is checkImageSinceDev's judge for a job that runs a
// catalog entry: the entry's own build record, under the key a catalog build
// writes it (gcp.CatalogRecordKey), whose base_ref says the base the image was
// built FROM. The cure it names is the entry's build, which no
// `fugaro update` of this checkout does.
func checkCatalogEntrySince(ctx context.Context, env *cloudEnv, b *blobx.Bucket, slug string, spec *task.Spec, entry config.Entry, since, subject, knows string, warn io.Writer) error {
	name := pluginwire.Printable(entry.Name)
	where := "in the checkout of the entry's own repository"
	if entry.Source.Repo != "" {
		where = "in the checkout of " + pluginwire.Printable(entry.Source.Repo)
	}
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the job image of %s workflow %s is the project's catalog entry %s, which %s. "+
			"Run fugaro image build %s %s, in your own terminal window (never in a coding agent's session): it rebuilds the entry from this release's base image and writes the record this launch reads",
			subject, knows, since, spec.Repo, spec.Workflow, name, why, name, where)
	}
	rec, err := catalogEntryRecord(ctx, b, catalogEntrySlugs(ctx, env, slug, entry), entry.Name)
	switch {
	case err != nil:
		return remote(fmt.Errorf("reading the build record of the catalog entry %s: %w", name, err))
	case rec == nil:
		return refuse("has no build record")
	case rec.BaseRef == "":
		return refuse("has a build record that does not say which base image it was built from")
	}
	ref := pluginwire.Printable(rec.BaseRef)
	switch rel, ok := releaseOf(rec.BaseRef); {
	case !ok:
		fmt.Fprintf(warn, "warning: the catalog entry %s, the job image of %s workflow %s, was built from %s, which is not a release base image; whether its runner knows %s is not checked\n", name, spec.Repo, spec.Workflow, ref, knows)
	case imagePredates(rel, since):
		return refuse(fmt.Sprintf("was built from base image %s, release %s", ref, rel))
	}
	return nil
}

// diagnoseSince is the first fugaro release whose runner executes the diagnose
// step, the cloud diagnosis's own stage (docs/troubleshoot-cloud D2402): it
// reads the bundle a launcher left under the run's own prefix and writes the
// two files the troubleshoot recipe's outputs block names. It is the release
// that first carries the step, so an image built from a base older than it has
// no diagnose in its version 1 step list at all and refuses the whole recipe
// as "is not a step type" — at bootstrap, with the launch already paid for —
// which is what the cloud tier's pre-flight exists to turn into a refusal at
// the person's terminal, naming this release and fugaro update (D2401, F18).
// docs/recipes.md's bullet for the step names it, the way the digest bullet
// names digestSince, and the value is verified against the tag actually cut
// the way every *Since here is.
const diagnoseSince = "0.26.0"

// diagnoseSubject and diagnoseKnows are this gate's two names in the family's
// sentence: what is refused (the cloud diagnosis the tier was about to launch)
// and what its job image has to know (the step the recipe it runs holds). The
// refusal carries no alt: the tier has no other way out to offer — the step is
// what a diagnosis is, and the evidence-only path a refused tier leaves is the
// decline's own sentence (troubleshootDeclineCloud), not this gate's.
const (
	diagnoseSubject = "a cloud diagnosis"
	diagnoseKnows   = "the diagnose step (the cloud diagnosis's own stage)"
)

// checkDiagnoseImage refuses a cloud diagnosis whose job image's runner cannot
// execute the diagnose step: the tier's launch runs the catalog recipe
// troubleshoot on the review-only variant, whose runner would refuse the whole
// recipe at bootstrap, after the container was started and the bundle
// uploaded, and no job image is in that state on purpose.
//
// It is asked by name rather than by the launch's own block for the same
// reason the review variant's two gates are (F8, D2401): the block's recipe
// gate judges the recipe text a task carries (recipeSinceFor), and the task a
// diagnosis carries names the catalog entry rather than embedding its text —
// the entry is the CLI's own file, which that gate never reads — so the step's
// release is asked here or not at all. Judged from the deployed job's build
// record, as every checkImageSince gate's is.
func checkDiagnoseImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	return checkImageSince(ctx, env, slug, spec, kind, diagnoseSince, diagnoseSubject, diagnoseKnows, "", warn)
}

// checkCloneDiagnoseImage is checkDiagnoseImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneDiagnoseImage(lc *localcfg.Config, spec *task.Spec, warn io.Writer) error {
	return checkCloneImageSince(lc, spec, diagnoseSince, diagnoseSubject, diagnoseKnows, "", warn)
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

// externalSkew drops a task's external id when the job image it will run on is
// older than the release that reads the field (externalSince), and says so in
// one line. It never refuses the launch: the id is the ticket outside Fugaro
// the item came from, named in the pull request of a run for it so a merge
// closes it (B5, the runner half that reads this field, D2727), and a launch
// that cannot carry it still does the work — where a task an older runner
// refuses is a paid-for attempt that never started.
//
// No command reaches it but the daemon's launcher (orchestrator_launcher.go),
// which is the field's only writer: no flag sets an external id, so `fugaro
// run` has nothing here to drop.
//
// The judge is baseBranchSkew's, for the same two shapes: a `checkout: clone`
// job runs the installation's base image itself, and a derived image runs the
// binary of the base its build record was built FROM. clone is that workflow's
// checkout mode, nil for the daemon's launch, which resolves no checkout at all
// — both judges are then asked and either one predating drops the field. An
// image neither judge can date (a development build, a hand-pushed one) keeps
// the field, as it does for the base branch: the drop is announced, but it does
// change what the run's pull request says, and an installation built from
// source must not lose the link to its own issue because no record dates its
// image — a runner of such an image that predates the field refuses the task at
// bootstrap, and that is the mistake the release's own `fugaro update` fixes.
func externalSkew(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, clone *bool, warn io.Writer) {
	e := spec.External
	if e == "" {
		return
	}
	drop := func() {
		spec.External = ""
		fmt.Fprintf(warn, "the job image predates %s and cannot read the item's external id %s, so this run opens no Fixes line for its issue; fugaro update in the checkout moves it onto this release's base image\n", externalSince, pluginwire.Printable(e))
	}
	predates := func(rel string, known bool) bool { return known && imagePredates(rel, externalSince) }
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
// first-line step of its own. This resolution reads CatalogDefault as false,
// which decides level 1's default on a recipe this launcher has in hand alone;
// the crafts read below takes the run's own answer (launchCatalogDefault),
// because the crafts are judged by the run's write and not by this resolution.
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
//
// The resolution itself is launchReviewLevels', beside this one, which a
// planner reads the levels of the same run through (api.go's
// Cloud.ReviewLevels): a reviewer arm outside the core decides which model to
// run by what the run will run, and an answer resolved a second way there
// would be that planner's reading of these rules rather than the run's own.
func launchReviewLevelsAsk(ctx context.Context, env *cloudEnv, spec *task.Spec, cfg *config.Config, rcp *recipe.Recipe, recipeUnknown bool, warn io.Writer) (string, error) {
	res := launchResolve(ctx, env, spec, cfg, rcp, warn)
	levels, floor, raised, err := res.levels, res.floor, res.raised, res.err
	if err != nil {
		// Nothing is asked of the image and no task is written: the launch is
		// the resolution's to refuse, and a refusal carries no levels to read
		// an ask off (config.ResolveLevels answers with the zero Levels).
		// Refusing here rather than leaving it to the run's bootstrap is what
		// G2 means by "never launches", and the alternative is a job paid for
		// a run whose first act is to refuse the task it was given.
		if raised {
			// The refusal is config's own (a level a layer turns off under the
			// floor, a model the allow-list does not name): what this adds is
			// that the request is why the level was resolved at all, so a
			// reader is not left looking for a floor nobody set.
			subject, _ := securityRequestNames(securityRequestSources(spec, cfg))
			return "", userErr("the run's request for the strongest review (%s) turns review level 3 on, and its review levels do not resolve: %v", subject, err)
		}
		return "", userErr("%v", err)
	}
	if res.craftErr != nil {
		return "", res.craftErr
	}
	return reviewLevelsAsk(levels, spec.ReviewLevels, cfg, floor, recipeUnknown), nil
}

// launchResolution is launchReviewLevels' answer plus the verdict of the model
// half's checks on the crafts the run reads, which only the launch gate acts
// on: a planner asking what the run will run is not a launch to refuse.
type launchResolution struct {
	levels   config.Levels
	floor    []policy.Level
	raised   bool
	err      error
	craftErr error
}

func launchReviewLevels(ctx context.Context, env *cloudEnv, spec *task.Spec, cfg *config.Config, rcp *recipe.Recipe, warn io.Writer) (config.Levels, []policy.Level, bool, error) {
	r := launchResolve(ctx, env, spec, cfg, rcp, warn)
	return r.levels, r.floor, r.raised, r.err
}

// launchResolve is that resolution alone, so the gate and a planner that
// has to know what the run it is planning will run ask one call the same
// question: the levels themselves, the floor they resolved under (the
// published policy's, before any force below), whether the run's request for
// the strongest review is what raised review level 3, and the resolution's own
// refusal. The last of the three is what the gate words its refusal with: only
// a level the request raised may be reported as refused because of it.
//
// A cloud with no project config (CloudOverBucket) has no database to read a
// policy node from and no provider table: it resolves with no floor and no
// providers rather than reaching for the config it does not have.
func launchResolve(ctx context.Context, env *cloudEnv, spec *task.Spec, cfg *config.Config, rcp *recipe.Recipe, warn io.Writer) launchResolution {
	rigorCfg := cfg.RigorConfig()
	req := spec.RigorRequest(spec.Paths)
	mode := rigor.Resolve(rigorCfg, req).Level
	var floor []policy.Level
	// The node is read once for both halves of the model's own gates: its
	// floor, and the crafts it carries, which are the project layer's answer to
	// the model half (internal/runner's craftLayers reads the same node for the
	// run, so the two cannot disagree about which model a craft runs).
	var node *projectpolicy.Policy
	var foundNode bool
	var providers map[string]config.ModelProvider
	if lc := env.lc; lc != nil {
		providers = lc.Providers
		if p, found, err := env.policy.read(ctx, lc); err == nil && found && p != nil {
			node, foundNode = p, true
			// The same two rows the run's own resolution reads: a request that
			// takes the run off the axis leaves the floor of the mode the layers
			// resolved in force beside hotfix's (D8, LF5), and a note resolved
			// against one row would say the launch's levels differ from the run's.
			floor = policy.FloorFor(p.ReviewFloor, mode, rigor.LayersRigor(rigorCfg, req, ""))
		}
	}
	fromTask, fromSwitch := securityRequestSources(spec, cfg)
	if (fromTask || fromSwitch) && rcp != nil && rcp.Mode == recipe.ModeReview {
		subject, _ := securityRequestNames(fromTask, fromSwitch)
		fmt.Fprintf(warn, "warning: the run asks for the strongest review (%s) and its recipe %s is mode: review, which allows exactly one review step and nothing else: review level 3 has no step to run in, so the launch resolves the levels the run would have resolved without it\n", subject, rcp.Name)
		fromTask, fromSwitch = false, false
	}
	// The crafts the run will resolve (MC12's write, MC13's rung): the same two
	// layers internal/runner's craftLayers hands the run — the agent block this
	// launch resolved, where the file at the ref and the project layer file's
	// defaults both arrive, and the published node — so a craft model the run
	// would be refused for is refused here, before a task is written.
	cr := craftsOf(launchCraftLayers(cfg, node, foundNode))
	claimed := providers
	if _, c, _ := layerRoutes(env.lc, cfg.Layer, spec.Repo); c != nil {
		claimed = c
	}
	in := config.LevelInputs{
		Config: cfg, Mode: mode, Floor: floor, Task: spec.ReviewLevels, Recipe: rcp, Providers: providers,
		CraftModels: cr.models,
	}
	// The crafts the run reads, asked before the levels are resolved because the
	// run's own write happens before it resolves them (MC12's write, applyCrafts)
	// and because this is the run's answer, not the one the resolution below
	// gives: the resolution reads the crafts' models (CraftModels) so that its
	// refusals name the craft key that wrote a model, and what the run reads is
	// the set of slots its own write fills.
	read := launchCraftsRead(cr, launchCraftWrites(cfg, cr, in, launchCatalogDefault(spec)))
	if fromTask || fromSwitch {
		// Cloned: ReviewFloor hands out the policy's own slice, and appending
		// to it would write the forced level into the merged policy.
		in.Floor = append(slices.Clone(floor), policy.LevelL3)
		in.Config, in.Task = securityRequestRaiseOnly(cfg, spec.ReviewLevels)
	}
	levels, _, err := config.ResolveLevels(in)
	var craftErr error
	if err == nil {
		// The model half's own checks (MC14, D1665): the allow-list, the route, the
		// price and the access, each at the key of the document that wrote the
		// craft's model. It is the verdict `fugaro validate` prints, from the same
		// function, and the one the run's bootstrap reaches through the slots MC12
		// writes — so a configuration that cannot be served stops the launch here,
		// where the task is not yet written and no job is paid for.
		//
		// The crafts judged are the ones this run reads (launchCraftsRead), not
		// every craft the layers resolve: the sentence below says the run refuses
		// the same configuration, and a craft whose level the run's own layers left
		// off reaches no slot at all, so the run neither runs it nor refuses it.
		//
		// The documents the access half is judged against are the installation's own
		// (env.lc's credentials, pools and accounts) and not the node's, which is
		// where the crafts were resolved from: they are the owner's writes (F12,
		// D1628) and the node is that config published, so a name the node carries
		// is a name this config holds — and this is the read slipCredentialNames
		// makes for the same half, for the same reason.
		if ps := craftModelProblems(cfg, env.lc, spec.Repo, claimed, launchFileSubject, read); len(ps) > 0 {
			lines := make([]string, 0, len(ps))
			for _, p := range ps {
				lines = append(lines, p.String())
			}
			craftErr = userErr("the crafts this run would resolve cannot be served: %s; the run this launch would start refuses the same configuration at bootstrap, so the launch is refused before any execution exists",
				strings.Join(lines, "; "))
		}
	}
	return launchResolution{levels: levels, floor: floor, raised: fromTask || fromSwitch, err: err, craftErr: craftErr}
}

// launchCraftsRead is the crafts whose model the run this launch resolved
// really reads, out of every craft the layers resolve, and the answer is the
// run's own write (MC12, internal/runner/craftmodel.go's applyCrafts): written
// is the set of crafts launchCraftWrites performed below.
//
// The coder's model goes into agent.models.coder and the senior review's into
// agent.models.reviewer whatever the levels are, and the first-line review's
// and the security review's go into their own level's slot alone: applyCrafts
// writes those two only where the level runs and wrote no model of its own
// (F14 — a craft decides a level's model, never whether it runs), and a craft
// written nowhere the run looks is a craft the run charges nothing for and
// refuses nothing for. Judging one at a launch would refuse a launch the run
// serves, with a sentence saying the run refuses the same configuration, and
// the way out would be editing a key the refused run never reads.
//
// The set is the write's own answer rather than a second copy of the rule: the
// level 1 switch is read off the coder's model
// (config.Agent.FirstLineOn, agent.first_line_review: auto), so a craft can turn
// a level on, and a launch resolving the levels off the models the file's own
// slots name answers that switch differently from the run whose slots the
// crafts' models were written into before it resolved them
// (TestLaunchTakesTheCraftsTheRunsBootstrapResolves).
func launchCraftsRead(cr craftResolution, written map[string]bool) []config.CraftModel {
	out := make([]config.CraftModel, 0, len(cr.crafts))
	for _, c := range cr.crafts {
		if written[c.Craft] {
			out = append(out, c)
		}
	}
	return out
}

// launchCraftWrites is the crafts the run this launch resolved writes into a
// slot: MC12's write, performed on a copy of the launch's own merged
// configuration through the functions the run's bootstrap performs it with
// (runner.WriteCraftRoles and runner.WriteCraftLevels), over the same inputs its
// own resolution is given.
//
// It is a copy for two reasons: the launch's own configuration is what every
// gate below judges, and the write allocates a level's block and writes a model
// into one a layer left empty — through the shared block pointers, either would
// land in the launch's.
//
// The role write goes first because level 1's answer is read off the roles
// (WriteCraftRoles), and the levels are asked whether they run on the
// configuration as it stands then, exactly as the run asks them
// (CraftLevelsRunning, which is applyCrafts' own call).
//
// in is the resolution's own inputs with the crafts' models in it: the role and
// level writes do not read CraftModels, and the level answer this needs is the
// one the run takes before it resolves the levels — the resolution below is the
// launch's, with the task's forced levels in it, and the run's write precedes
// every forcing (internal/runner's resolveLevels).
func launchCraftWrites(cfg *config.Config, cr craftResolution, in config.LevelInputs, derived bool) map[string]bool {
	run := *cfg
	run.Agent.Levels = launchLevelsCopy(cfg.Agent.Levels)
	written := runner.WriteCraftRoles(&run, cr.eff, in.Recipe)
	in.Config, in.CatalogDefault = &run, derived
	for name := range runner.WriteCraftLevels(&run, cr.eff, runner.CraftLevelsRunning(in)) {
		written[name] = true
	}
	return written
}

// launchLevelsCopy clones the three level blocks, nil for a level no layer
// wrote: a craft's write is a write into a block, and the blocks of the
// configuration a launch holds are shared with the copy launchCraftWrites hands
// the run's own functions.
func launchLevelsCopy(lv config.ReviewLevels) config.ReviewLevels {
	return config.ReviewLevels{
		L1: launchLevelCopy(lv.L1),
		L2: launchLevelCopy(lv.L2),
		L3: launchLevelCopy(lv.L3),
	}
}

func launchLevelCopy(b *config.LevelSetting) *config.LevelSetting {
	if b == nil {
		return nil
	}
	c := *b
	return &c
}

// launchCatalogDefault is whether the recipe the run will follow is the
// catalog's own `default`, the one whose first_line step runs only when
// agent.first_line_review turns it on (config.LevelInputs.CatalogDefault): the
// run's own r.derived read off the task, since the launcher carries every recipe
// it resolved (recipes_resolve.go's taskRecipeEmbedding) and a task that carries
// none names nothing else — launchRecipeOf's own answer for the recipe of a
// launch, which reads absent-and-not-chosen as this default.
func launchCatalogDefault(spec *task.Spec) bool {
	if spec.Recipe == nil {
		return true
	}
	return spec.Recipe.Name == recipe.DefaultName && spec.Recipe.Source == string(recipe.SourceCatalog)
}

// launchFileSubject is what a launch's own model messages call the file the
// policy merge reads (policy.SourceDefaultBranch): the fugaro.yaml at the ref
// the run checks out, which is the document a run of it resolves and not the
// launcher's working tree — the distinction cascadeRefConfig exists for. A
// message saying "this file" would name the checkout the person typed the
// command in, which is a document the run never reads.
const launchFileSubject = "the fugaro.yaml at the ref"

// launchCraftLayers are the layers a launch resolves the crafts' models over:
// internal/runner's craftLayers, which is the answer the run itself resolves —
// the agent block this launch read, where the file at the ref and the project
// layer file's defaults both arrive, and the published node, which carries the
// project's own crafts, credentials and pools (craftNodeKeyPrefix). That
// function is unexported, since it answers a run's own configuration, so the two
// are kept in step by hand, as craft ls's own three layers are.
//
// A node the launcher could not read is no project layer: the launch then
// resolves what the file and the installation's config alone say, and a craft
// model the node would have named is left to the run's own bootstrap — which
// reads the node and refuses there — rather than invented here. The
// installation's own config is not among these layers because a run has none:
// the job carries what the owner published.
func launchCraftLayers(cfg *config.Config, node *projectpolicy.Policy, found bool) []craft.Layer {
	layers := []craft.Layer{{Name: craft.SourceRepo, Crafts: cfg.Agent.Crafts}}
	if !found || node == nil {
		return layers
	}
	l := craft.Layer{Name: craft.SourceProject, KeyPrefix: craftNodeKeyPrefix,
		Credentials: node.CraftCredentials, Pools: node.CraftPools}
	if node.Crafts != nil {
		l.Crafts = *node.Crafts
	}
	return append(layers, l)
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
// failure proxySince's comment describes. The field's release and the replay's
// are different releases, and 0.24.0 and 0.25.0 are the proof: both are tagged,
// both carry the task's ab.slot and the pair rules that read it
// (task.AB.EffectiveSlot), and neither release's internal/runner has the replay
// at all (B1.2, the replay's own task) — so a gate naming either one admits
// exactly the run this constant exists to refuse.
//
// It names 0.26.0, the release this work ships in (docs/release-streams.yaml
// has 0.25.0 current and Wombat 0.26.0 next) and the release the replay ships
// in when B1.2 lands in it: that one is not tagged yet, so docs/release.md's
// Before-you-tag list verifies the number against the tag actually cut, the way
// it does for the other *Since constants.
//
// The launch that reads it is a task that carries `ab.slot` itself, replayed or
// hand-written (B1.1, B1.4). The workflow's own ask for a reviewer arm
// (workflows.<name>.ab.reviewer_arm, config.WorkflowAB) is a different key in a
// different file, and an older runner dies while decoding that file rather than
// while replaying a slot, so it is gated by workflowABSince and not here.
const slotSince = "0.26.0"

// slotKnows names, for the image gates' messages, what the job image's runner
// has to know: the pair's slot and the champion run the replay reads.
const slotKnows = "the A/B pair's slot (ab.slot, ab.champion_run)"

// slotAsk is what a launch asks of its job image's runner about the pair's
// slot, in the terms the refusal names, and "" when it asks nothing: the task's
// own `ab.slot`, replayed or hand-written, which is what B1.1 declared this gate
// for. A task with no `ab` block, and one whose block names no slot at all, ask
// nothing: the second is the coder pair A1.1 already wrote, which B1.1 reads as
// coder without a key.
//
// The gate is written on the key's presence and not on its value: a slot a
// launcher spells out is written by the release whose runner acts on slots, and
// what a reader needs to know is whether the image's runner reads the key at
// all. A coder pair — the pair A3.3 samples — writes neither key, so this gate
// is never the one that refuses it: that launch is judged by the `ab` field's
// own gate (A3.3).
//
// A workflow's ask for an A/B reviewer arm is not asked here even though the
// champion's launch is where it is judged: the ask is a key of the file the job
// decodes rather than a field of the task, and it is refused by the config
// reader rather than by the runner's slot handling, so it has an ask of its own
// (workflowABSince, checkWorkflowABImage).
func slotAsk(spec *task.Spec) string {
	if spec == nil || spec.AB == nil || spec.AB.Slot == "" {
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

// workflowABSince is the first fugaro release whose config reader knows a
// workflow's own ab: block (fugaro.yaml's workflows.<name>.ab,
// config.WorkflowAB): the key a workflow asks its A/B reviewer arm with (the
// owner's ruling of 2026-11-16, docs/review-levels-ab-jobmax O12). A runner
// built from an older image decodes fugaro.yaml with KnownFields, so the key
// alone refuses the whole file at bootstrap — the champion's own launch, the
// last moment a job image whose runner cannot carry the arm is still refusable
// before the job is paid for, dies there whatever the key says, an explicit
// null included (workflowABNames).
//
// It is a constant of its own and not slotSince, because the two keys are
// different kinds of thing: the ask is the repository's file, read by the job's
// config reader before anything runs, while the slot is the task the launcher
// writes and is read by the runner's review stage. A release can carry one and
// not the other, and 0.25.0 is why the two cannot share a number: it is tagged,
// it carries the task's `ab.slot` (task.AB.Slot) and the pair rules that read
// it, and it has no config.WorkflowAB, so a slotSince gate would admit it and
// the paid job would die at bootstrap on `field ab not found in type
// config.Workflow`. The two constants name the same release today only because
// neither key's behaviour is in a tagged release yet.
//
// It names 0.26.0, the release this work ships in — docs/release-streams.yaml
// has 0.25.0 current and Wombat 0.26.0 next — which is not tagged yet, so
// docs/release.md's Before-you-tag list verifies the number against the tag
// actually cut, the way it does for the other *Since constants.
const workflowABSince = "0.26.0"

// workflowABKnows names, for the image gates' messages, what the job image's
// runner has to know. The message reads "needs a job image whose runner knows
// <this>".
const workflowABKnows = "a workflow's ab: block (workflows.<name>.ab)"

// workflowABNames is the workflows of the configuration the run will read that
// write an ab: block, sorted, and the whole of what the three gates below judge.
//
// Every one of them is named and not only the workflow the task selects:
// fugaro.yaml is decoded whole, so a sibling workflow's ab: block is exactly as
// fatal to an older runner as the selected one's — the file that fails to decode
// is the whole file, and the job dies before it reads which workflow it runs.
//
// It reads the key being written and not the block it holds: the text's own
// answer (Config.RepoWorkflowABSet, setsWorkflowAB) is what a runner older than
// workflowABSince refuses, and an explicit null (`ab:`, which decodes to no
// block at all) is one line to that decoder — the reason setsSecurity is a
// presence read for security:. The decoded block is the second half of the
// union, and not a second question: a configuration this process built in
// memory rather than parsed (launchLayerConfig's empty one, a caller's own) has
// no text to read, and the block it carries is the only answer there is.
func workflowABNames(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	wrote := map[string]bool{}
	for _, name := range cfg.RepoWorkflowABSet {
		wrote[name] = true
	}
	for name, wf := range cfg.Workflows {
		if wf.AB != nil {
			wrote[name] = true
		}
	}
	if len(wrote) == 0 {
		return nil
	}
	names := make([]string, 0, len(wrote))
	for name := range wrote {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// workflowABSubject names, for the gates' messages, the keys that wrote the
// block and the way out their author has. The way out is the block itself rather
// than the image's age, because the block is the repository's own line: removing
// it costs the ask, and a job image that predates the key runs no reviewer arm
// whatever the file says, so a file that asks for none is refused nothing.
func workflowABSubject(repo string, names []string) (subject, alt string) {
	keys := make([]string, 0, len(names))
	for _, name := range names {
		keys = append(keys, "workflows."+name+".ab")
	}
	return "fugaro.yaml's " + strings.Join(keys, ", ") + " of " + repo,
		"remove the ab: block from fugaro.yaml (or from each workflow named): a job image that predates the key runs no reviewer arm at all, so the file that asks for none is refused nothing"
}

// workflowABImageWarnings warns, once per workflow that writes the block, that a
// configuration which asks for an A/B reviewer arm needs every job image on
// workflowABSince. It is validate's half of checkWorkflowABImage, and a warning
// rather than a Problem for triageImageWarnings' reason: the file is valid, and
// what has to move is the installation's image, which is why both ways to move
// one are named. One Problem per key, because each block is a separate line an
// operator removes.
func workflowABImageWarnings(c *config.Config) []config.Problem {
	var ps []config.Problem
	for _, name := range workflowABNames(c) {
		ps = append(ps, config.Problem{Path: "workflows." + name + ".ab", Message: "this file's workflows." + name + ".ab needs fugaro " + workflowABSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"})
	}
	return ps
}

// refuseWorkflowABSkew refuses fugaro update --repo, before any cloud effect,
// when the configuration the checkout resolves writes a workflow's ab: block and
// the installation's base image predates workflowABSince — a runner built from it
// decodes fugaro.yaml with KnownFields and refuses the run at bootstrap on a key
// it does not know. It is the half checkWorkflowABImage cannot be: a launch from
// outside a checkout reads no fugaro.yaml at all, so the key the run will find
// once it has one is judged here, by the operator's own converge
// (refuseTriageSkew's shape and its reason). The states of ref are read as
// refuseRigorSkew reads them: no base image passes, one that is not a release
// only warns.
func refuseWorkflowABSkew(repo string, cfg *config.Config, ref string, warn io.Writer) error {
	names := workflowABNames(cfg)
	if len(names) == 0 {
		return nil
	}
	subject, alt := workflowABSubject(repo, names)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, workflowABSince):
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, workflowABKnows, workflowABSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkWorkflowABImage refuses a launch whose job image runs a fugaro older than
// workflowABSince while the configuration the run will read writes a workflow's
// ab: key, at any value: that runner decodes fugaro.yaml with KnownFields, so the
// key alone kills the run at bootstrap. The judge is the build record's base_ref,
// as every checkImageSince gate's is, and cfg is the file the run will read —
// nil, a launch that could not read one, gated by nothing, as triageSources' nil
// is.
func checkWorkflowABImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	names := workflowABNames(cfg)
	if len(names) == 0 {
		return nil
	}
	subject, alt := workflowABSubject(spec.Repo, names)
	return checkImageSince(ctx, env, slug, spec, kind, workflowABSince, subject, workflowABKnows, "; or "+alt, warn)
}

// checkCloneWorkflowABImage is checkWorkflowABImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneWorkflowABImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	names := workflowABNames(cfg)
	if len(names) == 0 {
		return nil
	}
	subject, alt := workflowABSubject(spec.Repo, names)
	return checkCloneImageSince(lc, spec, workflowABSince, subject, workflowABKnows, "; or "+alt, warn)
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

// reviewAgainstTaskKnows names, for the image gates' messages, what the job
// image's runner has to know: the check itself and the key that turns it off.
// The message reads "needs a job image whose runner knows <this>".
const reviewAgainstTaskKnows = "the review-against-task check and its switch (agent.review_against_task)"

// reviewAgainstTaskSubject is triageSubject for agent.review_against_task, and
// for its reason: the absent key means the check on (Agent.ReviewAgainstTaskOn),
// so a file that wrote true loses nothing by removing the key, while one that
// wrote false gives the check back to its default on the first image that reads
// the file. written is the key's own field (Agent.ReviewAgainstTask): nil is the
// key absent, which no caller reaches — reviewAgainstTaskSources gates all four —
// and reads as false, the branch that has a cost to name.
func reviewAgainstTaskSubject(repo string, written *bool) (subject, alt string) {
	subject = "fugaro.yaml's agent.review_against_task of " + repo
	if written != nil && *written {
		return subject, "remove agent.review_against_task from fugaro.yaml: the absent key means the check on, which is what this file writes, and a job image that predates the check reviews the code alone whatever the file says"
	}
	return subject, "remove agent.review_against_task from fugaro.yaml only if the off-switch may go with it: a job image that predates the check reviews the code alone, and the first image that reads the key finds the check on again"
}

// reviewAgainstTaskSources reports, for the launch's reviewSpecSince gate,
// whether the configuration the run will read writes the key: the repository's
// own fugaro.yaml and nothing else (Config.RepoReviewAgainstTaskSet). There is
// no layer to ask, for triageSources' reason — config.Scopes gives
// agent.review_against_task InRepo alone, so a project layer writing it never
// reaches a resolution at all. The owner's published policy node is the other
// home of the answer (policy.Layer.ReviewAgainstTask) and is not judged here:
// it is not a key any file of this repository's decodes, so an older runner
// does not refuse it at bootstrap.
func reviewAgainstTaskSources(cfg *config.Config) bool {
	return cfg != nil && cfg.RepoReviewAgainstTaskSet
}

// reviewAgainstTaskImageWarnings warns, once, that a configuration which writes
// agent.review_against_task (RepoReviewAgainstTaskSet) needs every job image on
// reviewSpecSince. It is validate's half of checkReviewAgainstTaskImage, and a
// warning rather than a Problem for triageImageWarnings' reason: the file is
// valid, and what has to move is the installation's image, which is why both
// ways to move one are named.
func reviewAgainstTaskImageWarnings(c *config.Config) []config.Problem {
	if !reviewAgainstTaskSources(c) {
		return nil
	}
	return []config.Problem{{Path: "agent.review_against_task", Message: "this file's agent.review_against_task needs fugaro " + reviewSpecSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseReviewAgainstTaskSkew refuses fugaro update --repo, before any cloud
// effect, when the configuration the checkout resolves writes
// agent.review_against_task and the installation's base image predates
// reviewSpecSince — a runner built from it decodes fugaro.yaml with KnownFields
// and refuses the run at bootstrap on a key it does not know. It is the half
// checkReviewAgainstTaskImage cannot be: a launch from outside a checkout reads
// no fugaro.yaml at all, so the key the run will find once it has one is judged
// here, by the operator's own converge (refuseTriageSkew's shape and its
// reason). The states of ref are read as refuseTriageSkew reads them: no base
// image passes, one that is not a release only warns. It takes the
// configuration rather than the one bit a predicate would pass, because the way
// out its message offers is worded for the answer the file wrote
// (reviewAgainstTaskSubject, Agent.ReviewAgainstTask).
func refuseReviewAgainstTaskSkew(repo string, cfg *config.Config, ref string, warn io.Writer) error {
	if !reviewAgainstTaskSources(cfg) {
		return nil
	}
	subject, alt := reviewAgainstTaskSubject(repo, cfg.Agent.ReviewAgainstTask)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, reviewSpecSince):
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, reviewAgainstTaskKnows, reviewSpecSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkReviewAgainstTaskImage refuses a launch whose job image runs a fugaro
// older than reviewSpecSince while the configuration the run reads writes
// agent.review_against_task: that runner decodes fugaro.yaml with KnownFields,
// so the key alone kills the run at bootstrap, whatever it says. The judge is
// the build record's base_ref, as every checkImageSince gate's is, and cfg is
// the file the run will read — nil, a launch that could not read one, gated by
// nothing, as reviewAgainstTaskSources' nil is.
func checkReviewAgainstTaskImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	if !reviewAgainstTaskSources(cfg) {
		return nil
	}
	subject, alt := reviewAgainstTaskSubject(spec.Repo, cfg.Agent.ReviewAgainstTask)
	return checkImageSince(ctx, env, slug, spec, kind, reviewSpecSince, subject, reviewAgainstTaskKnows, "; or "+alt, warn)
}

// checkCloneReviewAgainstTaskImage is checkReviewAgainstTaskImage for a
// checkout: clone workflow: see checkCloneImageSince.
func checkCloneReviewAgainstTaskImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	if !reviewAgainstTaskSources(cfg) {
		return nil
	}
	subject, alt := reviewAgainstTaskSubject(spec.Repo, cfg.Agent.ReviewAgainstTask)
	return checkCloneImageSince(lc, spec, reviewSpecSince, subject, reviewAgainstTaskKnows, "; or "+alt, warn)
}

// workspaceBeatSince is the first fugaro release whose config reader knows
// fugaro.yaml's agent.workspace_beat and the project layer's
// defaults.agent.workspace_beat (WS13 of docs/workspace-service/, R10, D2985):
// the run's own switch for the keep-warm beat, on unless the file says
// otherwise, so a workspace's credentials service is not cold when the next
// call the run pays for arrives. Both files are decoded with KnownFields, so a
// runner built from an older image refuses either key at bootstrap, before the
// agent starts and after the launch was paid for — and it is the key that is
// refused, whatever it says: a file writing `true` asks for the default and is
// refused all the same, which is why the gate reads the text
// (Config.RepoWorkspaceBeatSet, ProjectLayer.SetsWorkspaceBeat) and not the
// resolved Agent.WorkspaceBeat.
//
// Like stopFractionSince it carries no shipped flag: refusing every image that
// predates the release is exactly the direction this gate wants, and an image
// with no release tag (a dev build, a hand-pushed one) only warns, the same as
// for every other *Since constant here. It names 0.27.0, the release this work
// is slotted for, which is not tagged yet: docs/release.md's Before-you-tag
// list verifies the number against the tag actually cut, the way
// stopFractionSince's bullet records.
const workspaceBeatSince = "0.27.0"

// workspaceBeatKnows names, for the image gates' messages, what the job
// image's runner has to know: the key, in whichever of the two files that may
// write it did. The message reads "needs a job image whose runner knows
// <this>".
func workspaceBeatKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the keep-warm beat's switch (agent.workspace_beat and the project layer's defaults.agent.workspace_beat)"
	case fromLayer:
		return "the keep-warm beat's switch (the project layer's defaults.agent.workspace_beat)"
	}
	return "the keep-warm beat's switch (agent.workspace_beat)"
}

// workspaceBeatSubject names, for the gates' messages, which file wrote the
// key, and the way out that file's author has: the beat is an optimisation and
// not a behaviour the run depends on — a runner that does not read the key
// sends no keep-warm request at all, which is what every release before this
// one does, and the calls the run pays for are served either way, at a cold
// start this run's own file was written to avoid — so removing it is a way out
// with no image to move. The distinction between the two files is
// stopFractionSubject's: the layer's key reaches every repository of the
// project and only its operator can remove it.
func workspaceBeatSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const (
		repoAlt  = "remove agent.workspace_beat from fugaro.yaml: a runner that does not read it sends no keep-warm beat, which is what every release before this one does"
		layerAlt = "remove defaults.agent.workspace_beat from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way, and a runner that does not read the key sends no keep-warm beat"
	)
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's agent.workspace_beat and the project layer's defaults.agent.workspace_beat of " + repo,
			"remove the key from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's defaults.agent.workspace_beat (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's agent.workspace_beat of " + repo, repoAlt
}

// workspaceBeatSources reports, for the launch's workspaceBeatSince gate,
// whether the configuration the run will read writes the key: the
// repository's fugaro.yaml (fromRepo) or the project layer it resolves over
// (fromLayer, which the task's own layer stands in for when the launcher can
// read no file: launchLevelsConfig).
//
// The layer's half is SetsWorkspaceBeat and not SetsDefault, which answers
// false for a key written as null: an older runner's KnownFields decoder
// refuses the key whatever it holds, so a layer writing it null is a layer
// every repository of the project dies at bootstrap on, and a gate reading the
// resolved value would deploy it.
func workspaceBeatSources(cfg *config.Config) (fromRepo, fromLayer bool) {
	if cfg == nil {
		return false, false
	}
	return cfg.RepoWorkspaceBeatSet, cfg.Layer.SetsWorkspaceBeat()
}

// workspaceBeatImageWarnings warns, once, that a configuration which writes
// the key — the repository's file (RepoWorkspaceBeatSet) or the project layer
// it resolved over (SetsWorkspaceBeat) — needs every job image on
// workspaceBeatSince. It is validate's half of checkWorkspaceBeatImage, and a
// warning rather than a Problem for stopFractionImageWarnings' reason: the
// file is valid, and what has to move is the installation (the base image, or
// a workflow's derived one), which is why both ways to move an image are
// named. It says which of the two files wrote the key, the same distinction
// workspaceBeatSubject draws for the gates, because the layer's key reaches
// every repository of the project and only its operator can remove it.
func workspaceBeatImageWarnings(c *config.Config) []config.Problem {
	fromRepo, fromLayer := workspaceBeatSources(c)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject := "this file's agent.workspace_beat"
	switch {
	case fromRepo && fromLayer:
		subject = "this file's agent.workspace_beat and the project layer's defaults.agent.workspace_beat"
	case fromLayer:
		subject = "the project layer's defaults.agent.workspace_beat"
	}
	return []config.Problem{{Path: "agent.workspace_beat", Message: subject + " needs fugaro " + workspaceBeatSince + " or later in the job image that runs it, and in the CLI of everyone who reads this file: an older runner decodes the file strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseWorkspaceBeatSkew refuses fugaro update --repo, before any cloud
// effect, when the configuration the checkout resolves writes
// agent.workspace_beat and the installation's base image predates
// workspaceBeatSince — a runner built from it decodes both files with
// KnownFields and refuses the run at bootstrap on a key it does not know. It
// is the half checkWorkspaceBeatImage cannot be: a launch from outside a
// checkout reads no fugaro.yaml at all, so the key the run will find once it
// has one is judged here, by the operator's own converge
// (refuseStopFractionSkew's shape and its reason). The states of ref are read
// as refuseStopFractionSkew reads them: no base image passes, one that is not
// a release only warns.
//
// fromRepo and fromLayer are the key being written (RepoWorkspaceBeatSet, the
// layer's SetsWorkspaceBeat) and not the value it holds: a file writing
// `true`, which is the default and asks for nothing, is the same line to an
// older runner's decoder as one writing `false`.
func refuseWorkspaceBeatSkew(repo string, fromRepo, fromLayer bool, ref string, warn io.Writer) error {
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := workspaceBeatSubject(repo, fromRepo, fromLayer)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, workspaceBeatSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, workspaceBeatSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkWorkspaceBeatImage refuses a launch whose job image runs a fugaro older
// than workspaceBeatSince while the configuration the run reads writes
// agent.workspace_beat: that runner decodes fugaro.yaml and the project layer
// with KnownFields, so the key alone kills the run at bootstrap. The judge is
// the build record's base_ref, as every checkImageSince gate's is.
func checkWorkspaceBeatImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := workspaceBeatSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := workspaceBeatSubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, workspaceBeatSince, subject, workspaceBeatKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkCloneWorkspaceBeatImage is checkWorkspaceBeatImage for a checkout:
// clone workflow: see checkCloneImageSince.
func checkCloneWorkspaceBeatImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := workspaceBeatSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := workspaceBeatSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(lc, spec, workspaceBeatSince, subject, workspaceBeatKnows(fromRepo, fromLayer), "; or "+alt, warn)
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
// highest are written too), and ProvidersEnv (internal/runner/providers.go)
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

// refuseRigorSpellingSkew refuses fugaro update --repo, before any cloud
// effect, when the configuration the repository resolves over writes one of
// the two rigor spellings this release adds — the `level:` key of a rigor area
// rule (fromRepoLevel in the repository's own file, fromLayer in the project
// layer it resolves over) or the scalar shorthand `rigor: <level>`
// (fromRepoScalar) — and the installation's base image predates
// rigorSpellingSince. The states of ref are read as refuseRigorSkew reads them:
// no base image passes, one that is not a release only warns.
func refuseRigorSpellingSkew(repo string, fromRepoLevel, fromRepoScalar, fromLayer bool, ref string, warn io.Writer) error {
	if !fromRepoLevel && !fromRepoScalar && !fromLayer {
		return nil
	}
	subject, alt := rigorSpellingSubject(repo, fromRepoLevel, fromRepoScalar, fromLayer)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the spelling is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, rigorSpellingSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the file strictly and refuses the run at bootstrap on the spelling it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, rigorSpellingSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// rigorSpellingSubject names, for refuseRigorSpellingSkew's messages, which
// file wrote which of the two spellings — a file may write both — and the way
// out that file's author has: the repository's own file is the repository's to
// edit, while the layer's key is read by every repository of the project. The
// cure is the spelling every release reads (`mode:`, and the block form of the
// rigor: key) rather than the release, which the gate cannot move for the file
// an older image reads.
func rigorSpellingSubject(repo string, fromRepoLevel, fromRepoScalar, fromLayer bool) (subject, alt string) {
	const (
		levelWhat  = "rigor area rule `level:` key"
		scalarWhat = "scalar `rigor:` shorthand"
		layerWhat  = "the project layer's rigor area rule `level:` key"
		levelCure  = "write the area rule's level under `mode:`, which every release reads"
		scalarCure = "write the rigor: key as a block carrying `default:` rather than as a scalar"
		layerAlt   = "write the area rule's level under `mode:` in the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
	)
	var what, cure []string
	if fromRepoLevel {
		what = append(what, levelWhat)
		cure = append(cure, levelCure)
	}
	if fromRepoScalar {
		what = append(what, scalarWhat)
		cure = append(cure, scalarCure)
	}
	switch {
	case fromLayer && len(what) > 0:
		return "fugaro.yaml's " + strings.Join(what, " and ") + ", and " + layerWhat + " of " + repo,
			strings.Join(cure, ", and ") + "; and " + layerAlt
	case fromLayer:
		return layerWhat + " (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's " + strings.Join(what, " and ") + " of " + repo,
		strings.Join(cure, ", and ") + " in fugaro.yaml"
}

// rigorSpellingKnows names, for the image gate's messages, the spelling the job
// image's runner has to know: the message reads "needs a job image whose runner
// knows <this>", so it has to name whichever of the two the configuration
// wrote, from whichever file.
func rigorSpellingKnows(fromRepoLevel, fromRepoScalar, fromLayer bool) string {
	var what []string
	if fromRepoLevel || fromLayer {
		what = append(what, "a rigor area rule's `level:` key")
	}
	if fromRepoScalar {
		what = append(what, "the scalar `rigor:` shorthand")
	}
	return strings.Join(what, " and ")
}

// checkRigorSpellingImage and checkCloneRigorSpellingImage are the launch's own
// rigorSpellingSince gate, for a workflow whose job image is a derived one and
// for a checkout: clone workflow respectively: the pair every other
// per-workflow image gate has (checkImageSince and checkCloneImageSince),
// judged on the spelling the configuration the run will read writes
// (rigorSpellingLaunchSources). The refusal names the fix that kind of image
// has — fugaro image refresh for a built image, fugaro update --base base for
// the installation's own — and the way out the file's author has, from
// rigorSpellingSubject.
//
// The image is the judge because that is where the reader is: a runner older
// than rigorSpellingSince decodes fugaro.yaml and the project layer with
// KnownFields, so either spelling alone kills the run at bootstrap, before the
// agent starts and after the launch was paid for.
func checkRigorSpellingImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	fromRepoLevel, fromRepoScalar, fromLayer := rigorSpellingLaunchSources(ctx, env, spec)
	if !fromRepoLevel && !fromRepoScalar && !fromLayer {
		return nil
	}
	subject, alt := rigorSpellingSubject(spec.Repo, fromRepoLevel, fromRepoScalar, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, rigorSpellingSince, subject, rigorSpellingKnows(fromRepoLevel, fromRepoScalar, fromLayer), "; or "+alt, warn)
}

// checkCloneRigorSpellingImage is checkRigorSpellingImage for a checkout:
// clone workflow: see checkCloneImageSince.
func checkCloneRigorSpellingImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	fromRepoLevel, fromRepoScalar, fromLayer := rigorSpellingLaunchSources(ctx, env, spec)
	if !fromRepoLevel && !fromRepoScalar && !fromLayer {
		return nil
	}
	subject, alt := rigorSpellingSubject(spec.Repo, fromRepoLevel, fromRepoScalar, fromLayer)
	return checkCloneImageSince(env.lc, spec, rigorSpellingSince, subject, rigorSpellingKnows(fromRepoLevel, fromRepoScalar, fromLayer), "; or "+alt, warn)
}

// rigorSpellingLaunchSources reports, for the launch's rigorSpellingSince gate,
// whether the configuration the run will read writes either spelling: the
// repository's own file (fromRepoLevel, fromRepoScalar) or the project layer it
// resolves over (fromLayer). The file is read as rigorLaunchSources reads it —
// at the ref, out of this checkout's object database, with the checkout's own
// document standing in when the ref cannot be read — and the layer is the one
// that file resolves over, so a file that cannot be read here is a launch whose
// bootstrap, which reads the file this launcher could not, refuses the spelling
// then.
func rigorSpellingLaunchSources(ctx context.Context, env *cloudEnv, spec *task.Spec) (fromRepoLevel, fromRepoScalar, fromLayer bool) {
	cfg := launchFileConfig(ctx, env, spec)
	if cfg == nil {
		return false, false, false
	}
	return cfg.RepoRigorLevelSet, cfg.RepoRigorScalarSet, cfg.Layer.SetsRigorLevel()
}

// rigorMembersSince is the first fugaro release whose config reader knows what
// LD4 adds to the project layer's rigor block: a `members:` key of one of its
// defaults.rigor.areas rules, and the per-repository defaults.rigor.repos map
// beside them. Both keys are the layer's alone — a repository's own file knows
// no repos: and refuses members: on a rule of its own (rigor.Area.Validate) —
// and the layer is decoded with KnownFields, so a layer that carries either
// makes every repository of the project whose job image is older than this
// release fail at bootstrap on a key its runner does not know, before the agent
// starts and after the launch was paid for.
//
// It is not rigorSpellingSince, which names the release whose reader knows the
// two spellings of that block: that constant's judge booleans are an area
// rule's `level:` key and the scalar shorthand, and a layer writing members or
// repos need write neither (an area rule still reads `mode:`, and the layer
// never carried the shorthand), so the gate it drives passes a layer this one
// refuses. Like rigorSpellingSince the judge is the image that runs the run:
// the installation's base image for a checkout: clone workflow, the base a
// workflow's build record was built FROM for a derived one. fugaro update
// --repo gates it before any cloud effect (gateRigorMembers, beside
// gateRigorSpelling), a launch gates it the way checkJobsImage does
// (checkRigorMembersImage, checkCloneRigorMembersImage) from a checkout of the
// repository, where the layer the run will read can be read here at all, and
// fugaro validate warns (rigorMembersImageWarnings). Outside a checkout only
// fugaro update --repo checks.
//
// It names 0.27.0, the release this work is slotted for (docs/release.md's
// Before-you-tag bullet verifies it against the tag actually cut), which is not
// tagged yet: a wrong value refuses too late (a run on an image whose runner
// cannot read the keys dies at bootstrap) or too early (an image that knows
// them stays blocked).
const rigorMembersSince = "0.27.0"

// rigorMembersLayerSet reports whether the project layer the configuration
// resolves over writes either key, at any value: SetsRigorMembers' question,
// and its reason — `members:` with nothing under it and `repos: {}` are lines
// an older runner's decoder refuses just as a populated one is, and the parsed
// rule cannot tell the two apart. The configuration is launchFileConfig's, so
// the launch gates fire where the sibling gates' do: from a checkout of the
// repository, where the layer the run will read can be read here at all
// (checkoutConfig, or the ref's file resolved over the layer the task carries,
// cascadeRefConfig). Outside one it is nil and nothing is gated, which is the
// shape rigorSpellingSince and jobsSince have: there only fugaro update --repo
// checks.
func rigorMembersLayerSet(cfg *config.Config) bool {
	return cfg != nil && cfg.Layer.SetsRigorMembers()
}

// rigorMembersSubject names, for the gates' messages, what the layer wrote and
// the way out its author has: the layer is the operator's file, and the
// repositories the key breaks each have the same fix — removing it and
// publishing the layer again. One subject names both keys, because one release
// reads both and the operator finds whichever they wrote in the same block.
func rigorMembersSubject(repo string) (subject, alt string) {
	// The subject takes the gates' singular verb ("needs a job image whose
	// runner knows …"), so the two keys are joined by "or" rather than by a
	// comma: the verb then agrees with the nearer one, and the sentence reads
	// for either key an operator wrote.
	return "the project layer's `members:` key or its `defaults.rigor.repos` map (read for " + repo + ")",
		"remove the members: key or the repos: map from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
}

// rigorMembersKnows names, for the image gates' messages, the keys the job
// image's runner has to know: the refusal reads "needs a job image whose runner
// knows <this>", and the two keys are the layer's whole half of LD4.
const rigorMembersKnows = "the project layer's rigor members, an area rule's `members:` key and the `defaults.rigor.repos` map"

// rigorMembersImageWarnings warns, once, that the project layer a configuration
// resolves over writes members: or repos:, which needs every job image on
// rigorMembersSince. It is validate's half of checkRigorMembersImage and a
// warning rather than a Problem for rigorImageWarnings' reason: the layer is
// valid, and what has to move is the installation (the base image, or a
// workflow's derived one), which is why both ways to move an image are named.
func rigorMembersImageWarnings(c *config.Config) []config.Problem {
	if !rigorMembersLayerSet(c) {
		return nil
	}
	return []config.Problem{{Path: "defaults.rigor", Message: rigorMembersKnows + " need fugaro " + rigorMembersSince + " or later in the job image that runs it, and in the CLI of everyone who reads this layer: an older runner decodes the layer strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
}

// refuseRigorMembersSkew refuses fugaro update --repo, before any cloud effect,
// when the project layer the checkout resolves writes members: or repos: and
// the installation's base image predates rigorMembersSince — a runner built
// from it decodes the layer with KnownFields and refuses it at bootstrap. The
// states of ref are read as refuseRigorSkew reads them: no base image passes,
// one that is not a release only warns.
func refuseRigorMembersSkew(repo string, fromLayer bool, ref string, warn io.Writer) error {
	if !fromLayer {
		return nil
	}
	subject, alt := rigorMembersSubject(repo)
	switch rel, ok := releaseOf(ref); {
	case ref == "":
		return nil
	case !ok:
		fmt.Fprintf(warn, "warning: %s cannot be judged: the installation's base image is %s, which is not a release base image, so whether its runner reads the key is not checked\n",
			subject, pluginwire.Printable(ref))
	case imagePredates(rel, rigorMembersSince):
		return userErr("%s needs a job image whose runner reads it (fugaro %s or later), but the installation's base image is %s, release %s: an older runner decodes the layer strictly and refuses the run at bootstrap on a key it does not know. Run fugaro update --base base in your own terminal window to move the installation's base image to a current release, or %s",
			subject, rigorMembersSince, pluginwire.Printable(ref), rel, alt)
	}
	return nil
}

// checkRigorMembersImage and checkCloneRigorMembersImage are the launch's own
// rigorMembersSince gate, for a workflow whose job image is a derived one and
// for a checkout: clone workflow respectively: the pair every other
// per-workflow image gate has (checkImageSince/checkCloneImageSince), with
// checkJobsImage's judge and its one source — the project layer, which is the
// only file that can write either key. A launch whose configuration writes
// neither is never gated.
func checkRigorMembersImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	if !rigorMembersLayerSet(launchFileConfig(ctx, env, spec)) {
		return nil
	}
	subject, alt := rigorMembersSubject(spec.Repo)
	return checkImageSince(ctx, env, slug, spec, kind, rigorMembersSince, subject, rigorMembersKnows, "; or "+alt, warn)
}

// checkCloneRigorMembersImage is checkRigorMembersImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneRigorMembersImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	if !rigorMembersLayerSet(launchFileConfig(ctx, env, spec)) {
		return nil
	}
	subject, alt := rigorMembersSubject(spec.Repo)
	return checkCloneImageSince(env.lc, spec, rigorMembersSince, subject, rigorMembersKnows, "; or "+alt, warn)
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
// escalation ladder's two model grades: the project layer's
// defaults.model_grades (docs/outer-loop-escalation, D2039's E2). The layer is
// decoded with KnownFields, so a runner built from an older base image refuses
// the key at bootstrap. Like planningSince, whose home is the same layer, the
// judge is the installation's base image and the gate is fugaro update --repo's
// (refuseModelGradesSkew); fugaro run gates the launch too
// (checkModelGradesImage, checkCloneModelGradesImage), but only from a checkout
// of the repository, where it can read the layer the run will, so outside one
// only fugaro update --repo checks, and fugaro validate warns
// (modelGradesImageWarnings).
//
// A repository's own fugaro.yaml is not judged: the two grade models left it
// (config.LiftedKeys), so a file that still writes one is named where it is
// written, by the reader that owns it, rather than gated on the image it would
// die on — the line is the repository's own and its fix is a diff.
//
// It names 0.24.0, the release this work ships in ("Uakari": docs/release-streams.yaml
// has 0.23.0 current and Uakari 0.24.0 next), which is not tagged yet: the
// release that tags it holds this number to the tag it cuts, as it does for
// slotSince's 0.26.0 and jobsSince's 0.22.0. docs/release.md ("Before you tag")
// has the checklist verify it.
const modelGradesSince = "0.24.0"

// modelGradesLaunchSources reports, for the launch's modelGradesSince gate,
// whether the configuration the run will read writes the key: the project layer
// it resolves over (fromLayer, SetsModelGrades), read the way
// planningLaunchSources reads it. It is the key being written and not the value
// it holds: an older runner's decoder refuses defaults.model_grades whatever it
// says — an empty mapping and two full grades are one line to it — so judging
// the grades would launch the first to die at bootstrap on a run already paid
// for.
func modelGradesLaunchSources(ctx context.Context, env *cloudEnv, spec *task.Spec) (fromLayer bool) {
	cfg := launchFileConfig(ctx, env, spec)
	if cfg == nil {
		return false
	}
	return cfg.Layer.SetsModelGrades()
}

// modelGradesSubject names, for refuseModelGradesSkew's messages, the file that
// wrote the key, and the way out that file's author has: the layer's block is
// read by every repository of the project, so publishing the layer again is what
// fixes them all. The layer's key is defaults.model_grades, the one under
// defaults:, rather than a top-level key of the layer file like jobs:'s — and it
// is the home the ladder reads, so removing the block leaves the run with no
// grade at all rather than with a default.
func modelGradesSubject(repo string) (subject, alt string) {
	return "the project layer's defaults.model_grades (read for " + repo + ")",
		"remove defaults.model_grades from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way"
}

// modelGradesKnows names, for the image gates' messages, the key the job image's
// runner has to know: the message reads "needs a job image whose runner knows
// <this>", and the layer is the one file the key is still written in.
const modelGradesKnows = "the project layer's defaults.model_grades"

// refuseModelGradesSkew refuses fugaro update --repo, before any cloud effect,
// when the project layer the repository resolves over writes
// defaults.model_grades (fromLayer) and the installation's base image predates
// modelGradesSince — a runner built from it decodes the layer with KnownFields
// and refuses the run at bootstrap on a key it does not know. The states of ref
// are read as refuseRigorSkew reads them: no base image passes, one that is not
// a release only warns.
func refuseModelGradesSkew(repo string, fromLayer bool, ref string, warn io.Writer) error {
	if !fromLayer {
		return nil
	}
	subject, alt := modelGradesSubject(repo)
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
	if !modelGradesLaunchSources(ctx, env, spec) {
		return nil
	}
	subject, alt := modelGradesSubject(spec.Repo)
	return checkImageSince(ctx, env, slug, spec, kind, modelGradesSince, subject, modelGradesKnows, "; or "+alt, warn)
}

// checkCloneModelGradesImage is checkModelGradesImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneModelGradesImage(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	if !modelGradesLaunchSources(ctx, env, spec) {
		return nil
	}
	subject, alt := modelGradesSubject(spec.Repo)
	return checkCloneImageSince(env.lc, spec, modelGradesSince, subject, modelGradesKnows, "; or "+alt, warn)
}

// taskGradeSince is the first fugaro release whose task reader knows a task's
// own model grade (task.Spec.ModelGrade) and the importance a follow-up carries
// (task.Spec.FollowUpOf.Importance) — docs/outer-loop-escalation, E4 (G11) and
// D2040. A task is decoded with DisallowUnknownFields, so a runner built from an
// older job image refuses the whole spec at bootstrap: bytes in the bucket under
// a run that never starts, after the launch was paid for. The gate follows
// followUpSince's shape rather than modelGradesSince's — what is judged is the
// task, not a file, so the fields' own presence is the whole question and no
// checkout of the repository is needed to answer it.
//
// It names 0.24.0, the release this work ships in, which is the number
// modelGradesSince carries and for the same reason: a task that names a grade
// resolves it through the project layer's defaults.model_grades, so the release
// whose configuration reader knows that key is the release whose task reader
// knows this field — an installation older than it can neither set a grade nor
// run a task that names one, and gating the field one release earlier would
// launch runs that die at bootstrap on a grade the project has no way to set.
// docs/release-streams.yaml has 0.23.0 current and Uakari 0.24.0 next, and the
// release that tags this holds the number to the tag it cuts;
// docs/release.md's ("Before you tag") checklist is where it verifies it.
const taskGradeSince = "0.24.0"

// taskGradeAsk reports, for the gates' messages, which of the two fields the
// task carries: the subject ("" for a task that carries neither, which no gate
// ever refuses) and what the job image's runner has to know. The two travel
// apart — a grade rides any task, while an importance rides the follow-up block
// alone — so a refusal names the field the person wrote rather than both.
func taskGradeAsk(spec *task.Spec) (subject, knows string) {
	grade := spec.ModelGrade != ""
	importance := spec.FollowUpOf != nil && spec.FollowUpOf.Importance != ""
	switch {
	case grade && importance:
		return "a task carrying a model grade and a follow-up importance", "a task's model_grade and follow_up_of.importance"
	case importance:
		return "a task carrying a follow-up importance", "a task's follow_up_of.importance"
	case grade:
		return "a task carrying a model grade", "a task's model_grade"
	}
	return "", ""
}

// checkTaskGradeImage refuses a launch whose job image runs a fugaro older than
// taskGradeSince while the task carries either field: a runner built before that
// release decodes the task strictly and refuses the spec at bootstrap, so the
// launch must not reach such a job at all.
func checkTaskGradeImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	subject, knows := taskGradeAsk(spec)
	if subject == "" {
		return nil
	}
	return checkImageSince(ctx, env, slug, spec, kind, taskGradeSince, subject, knows, "", warn)
}

// checkCloneTaskGradeImage is checkTaskGradeImage for a checkout: clone
// workflow: see checkCloneImageSince.
func checkCloneTaskGradeImage(lc *localcfg.Config, spec *task.Spec, warn io.Writer) error {
	subject, knows := taskGradeAsk(spec)
	if subject == "" {
		return nil
	}
	return checkCloneImageSince(lc, spec, taskGradeSince, subject, knows, "", warn)
}

// visionSince is the first fugaro release whose configuration reader knows
// agent.models.vision — the slot a stage runs instead when its own model cannot
// read an image (V1, D2003) — and whose policy reader knows
// model_prices.*.image_tokens, the per-image token ceiling that is the whole of
// what makes a model able to take one (V2, D2001). The two members are one
// release because the slot is unusable without the ceiling: a model whose price
// row carries no image rate is refused the call (internal/gateway's
// imagePriceFix), so the release that lets an operator write the slot is the
// release that lets them price it.
//
// Both files are decoded strictly — fugaro.yaml and the project layer with
// KnownFields, the published policy node member by member — so a runner built
// from an older image refuses the key at bootstrap, before the agent starts and
// after the launch was paid for, and refuses a node carrying the price member
// whole. It is the key that is refused whatever it says: a file writing
// agent.models.vision with nothing under it, and a vision: "" that asks for the
// default, are the same line to that decoder as a model ID, which is why the
// gate reads the text (Config.RepoVisionSet, ProjectLayer.SetsVision) and not
// the resolved Agent.VisionModel.
//
// Like noneModelSince, it carries no shipped flag: refusing every image that
// predates the release is exactly the direction this gate wants, and an image
// with no release tag (a dev build, a hand-pushed one) only warns, the same as
// for every other *Since constant here. It names 0.23.0 ("Tapir"), the release
// the two members shipped in: tag v0.23.0 is the tag that contains V1 (#2305)
// and V2 (#2336), and it is that release's config reader and policy reader that
// know the key and the member, so this is a cut number and not one the work was
// slotted for. A later number fails in the other direction — it refuses a
// 0.23.0 or a 0.24.0 image whose readers do know the key, and the publish half
// would refuse a node for an installation whose own base image can already read
// it, so fugaro update would move that image to a release that still refuses the
// node: a refusal with no way out.
const visionSince = "0.23.0"

// visionLaunchSources reports, for the launch's visionSince gate, whether the
// configuration the run will read writes the key: the repository's fugaro.yaml
// (fromRepo, RepoVisionSet) or the project layer it resolves over (fromLayer,
// SetsVision), the two homes config.Scopes gives the key, read the way
// stopFractionSources reads agent.review_stop_fraction's. Both are the key
// being written and not the model it names: an older runner's decoder refuses
// the key whatever it says — a null leaf, a blank string and a model ID are one
// line to it.
//
// The layer's half is SetsVision and not SetsDefault, which answers false for a
// key written as null: an older runner's KnownFields decoder refuses the key
// whatever it holds, so a layer writing `models: { vision: }` is a layer every
// repository of the project dies at bootstrap on, and a gate reading the
// resolved value would launch those runs.
func visionLaunchSources(cfg *config.Config) (fromRepo, fromLayer bool) {
	if cfg == nil {
		return false, false
	}
	return cfg.RepoVisionSet, cfg.Layer.SetsVision()
}

// visionSubject names, for the gates' messages, which file wrote the key, and
// the way out that file's author has: the slot is a choice of which model a
// stage runs, not a behaviour the run depends on — a runner that does not read
// it runs every stage on the model it already had, which is what every release
// before this one does — so removing the key is a way out with no image to
// move, at the cost of the fallback the operator asked for. The distinction
// between the two files is stopFractionSubject's: the layer's key reaches every
// repository of the project and only its operator can remove it.
func visionSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const (
		repoAlt  = "remove agent.models.vision from fugaro.yaml: a runner that does not read it runs a stage whose own model cannot read an image on that model, which is what every release before this one does"
		layerAlt = "remove defaults.agent.models.vision from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way, and a runner that does not read the key runs a stage whose own model cannot read an image on that model"
	)
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's agent.models.vision and the project layer's defaults.agent.models.vision of " + repo,
			"remove the key from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's defaults.agent.models.vision (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's agent.models.vision of " + repo, repoAlt
}

// visionKnows names, for the image gates' messages, the key the job image's
// runner has to know: the message reads "needs a job image whose runner knows
// <this>", so it has to name whichever of the two files wrote it.
func visionKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the vision slot (agent.models.vision and the project layer's defaults.agent.models.vision)"
	case fromLayer:
		return "the vision slot (the project layer's defaults.agent.models.vision)"
	}
	return "the vision slot (agent.models.vision)"
}

// checkVisionImage and checkCloneVisionImage are the launch's own visionSince
// gate, for a workflow whose job image is a derived one and for a checkout:
// clone workflow respectively: the pair every other per-workflow image gate has
// (checkImageSince and checkCloneImageSince), judged on the key the
// configuration the run will read writes (visionLaunchSources). The refusal
// names the fix that kind of image has — fugaro update in its checkout for a
// built image, fugaro update --base base for the installation's own — and the
// way out the file's author has, from visionSubject.
//
// It is a launch gate and not an update --repo one: the slot is read by the
// runner of the job the run launches, so the image that has to know the key is
// the workflow's own, and the run is refused where that image is known. The
// node's price member, which the same release carries, is the other half of the
// guard (policyImageTokensRefusal, internal/cli's publish door).
func checkVisionImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := visionLaunchSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := visionSubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, visionSince, subject, visionKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkCloneVisionImage is checkVisionImage for a checkout: clone workflow: see
// checkCloneImageSince.
func checkCloneVisionImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := visionLaunchSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := visionSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(lc, spec, visionSince, subject, visionKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// mergeJudgeCraftLaunchSources reports, for the launch's mergeJudgeSince gate
// on the judge's craft, whether the configuration the run will read writes
// agent.crafts.merge-judge: the repository's fugaro.yaml
// (RepoMergeJudgeCraftSet) or the project layer it resolves over
// (SetsMergeJudgeCraft), the two homes config.Scopes gives agent.crafts, read
// the way visionLaunchSources reads the vision slot's.
//
// It is the merge judge's craft key and not the crafts block: the other four
// crafts are 0.24.0's, so a runner older than mergeJudgeSince decodes them, and
// it is this one field that its KnownFields decoder refuses ("field merge-judge
// not found in type config.Crafts"). Both files are read for the reason
// visionLaunchSources reads both: an older runner refuses the key in either,
// and the layer's is the one that reaches every repository of the project.
func mergeJudgeCraftLaunchSources(cfg *config.Config) (fromRepo, fromLayer bool) {
	if cfg == nil {
		return false, false
	}
	return cfg.RepoMergeJudgeCraftSet, cfg.Layer.SetsMergeJudgeCraft()
}

// mergeJudgeCraftSubject names, for the gates' messages, which file wrote the
// key, and the way out that file's author has: the craft is a choice of which
// credential the judge's call goes out on, not a behaviour the run depends on —
// a runner that does not read it resolves the judge's craft from the craft it
// follows (the coder, config.CraftParent), which is what a run that sets no
// judge craft already does — so removing the key is a way out with no image to
// move, at the cost of the credential the operator asked for. The distinction
// between the two files is visionSubject's: the layer's key reaches every
// repository of the project and only its operator can remove it.
func mergeJudgeCraftSubject(repo string, fromRepo, fromLayer bool) (subject, alt string) {
	const (
		repoAlt  = "remove agent.crafts.merge-judge from fugaro.yaml: a runner that does not read it resolves the judge's craft from the coder's, which is what a run that sets no judge craft already does"
		layerAlt = "remove defaults.agent.crafts.merge-judge from the project layer and publish it again: until then every repository of the project whose job image predates it fails the same way, and a runner that does not read the key resolves the judge's craft from the coder's"
	)
	switch {
	case fromRepo && fromLayer:
		return "fugaro.yaml's agent.crafts.merge-judge and the project layer's defaults.agent.crafts.merge-judge of " + repo,
			"remove the key from fugaro.yaml and " + layerAlt
	case fromLayer:
		return "the project layer's defaults.agent.crafts.merge-judge (read for " + repo + ")", layerAlt
	}
	return "fugaro.yaml's agent.crafts.merge-judge of " + repo, repoAlt
}

// mergeJudgeCraftKnows names, for the image gates' messages, the key the job
// image's runner has to know: the message reads "needs a job image whose runner
// knows <this>", so it has to name whichever of the two files wrote it.
func mergeJudgeCraftKnows(fromRepo, fromLayer bool) string {
	switch {
	case fromRepo && fromLayer:
		return "the merge judge's craft (agent.crafts.merge-judge and the project layer's defaults.agent.crafts.merge-judge)"
	case fromLayer:
		return "the merge judge's craft (the project layer's defaults.agent.crafts.merge-judge)"
	}
	return "the merge judge's craft (agent.crafts.merge-judge)"
}

// checkMergeJudgeCraftImage and checkCloneMergeJudgeCraftImage are the launch's
// own mergeJudgeSince gate for the judge's craft, for a workflow whose job
// image is a derived one and for a checkout: clone workflow respectively — the
// pair every other per-workflow image gate has (checkImageSince and
// checkCloneImageSince), judged on the key the configuration the run will read
// writes (mergeJudgeCraftLaunchSources), exactly as checkVisionImage is judged
// on the vision slot's.
//
// It is the second half of the release's gate on the merge judge: the step
// gate (mergeJudgeSince, recipeSinceFor) judges the judge's recipe, and this
// one judges the craft key its configuration writes. A launch may carry either
// without the other — a hand launch of another recipe in a repository whose
// file sets the judge's craft, and a judge run whose file sets none — so the
// two are separate reads of the same constant. A repository's or the project's
// file that writes the key is refused at the terminal, naming the file, the
// release and the image to move, rather than at bootstrap on a runner that
// cannot decode it.
func checkMergeJudgeCraftImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := mergeJudgeCraftLaunchSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := mergeJudgeCraftSubject(spec.Repo, fromRepo, fromLayer)
	return checkImageSince(ctx, env, slug, spec, kind, mergeJudgeSince, subject, mergeJudgeCraftKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// checkCloneMergeJudgeCraftImage is checkMergeJudgeCraftImage for a checkout:
// clone workflow: see checkCloneImageSince.
func checkCloneMergeJudgeCraftImage(lc *localcfg.Config, spec *task.Spec, cfg *config.Config, warn io.Writer) error {
	fromRepo, fromLayer := mergeJudgeCraftLaunchSources(cfg)
	if !fromRepo && !fromLayer {
		return nil
	}
	subject, alt := mergeJudgeCraftSubject(spec.Repo, fromRepo, fromLayer)
	return checkCloneImageSince(lc, spec, mergeJudgeSince, subject, mergeJudgeCraftKnows(fromRepo, fromLayer), "; or "+alt, warn)
}

// modelGradesImageWarnings warns, once, that a configuration which writes the
// key — the project layer the file resolved over (SetsModelGrades) — needs every
// job image on modelGradesSince. It is validate's half of checkModelGradesImage,
// and a warning rather than a Problem for rigorImageWarnings' reason: the file
// is valid, and what has to move is the installation (the base image, or a
// workflow's derived one), which is why both ways to move an image are named.
func modelGradesImageWarnings(c *config.Config) []config.Problem {
	if c == nil || !c.Layer.SetsModelGrades() {
		return nil
	}
	return []config.Problem{{Path: "model_grades", Message: "the project layer's defaults.model_grades needs fugaro " + modelGradesSince + " or later in the job image that runs it, and in the CLI of everyone who reads this layer: an older runner decodes the layer strictly and refuses the run at bootstrap. Run fugaro update --base base in your own terminal window to move the installation's base image, and fugaro image refresh for a workflow's own derived image"}}
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
// keys yet — the state catalogShipped left behind, and the reason the flag is
// passed rather than read, so a test can still ask that half; shipped true,
// which is what catalogShipped is, judges the installation's base image the
// job is built from, the way checkCloneImageSince does — a release that
// predates catalogSince is refused, a ref that is not a release image is only
// warned about (nothing can tell what its runner knows), and no base image at
// all passes (infra.Repo then builds the job from this release's own image,
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

// l8Since is the first fugaro release whose launcher and runner read a
// repository as release A of lift L8 reads it (docs/repo-override-rulings,
// D3237): the keys the project owns are dropped and named rather than honoured
// (config.Resolve, LiftedKeyNotes, L8's S1), a recipe reaches a run from the
// project store rather than from .fugaro/recipes/ (B16), and the two things
// release A adds to the project layer are known — ab.repos (AB1, D3297) and a
// recipe's project copy (PR0, D3302). A job image built before it runs a runner
// that honours a repository's lifted keys, reads the folder the launch no longer
// reads, and refuses the layer's ab.repos or a copy name at bootstrap, after the
// launch was paid for, so a launch that would meet any of the four is refused
// here (checkL8Image, checkCloneL8Image).
//
// It names 0.30.0, the release this work is slotted for: release A is cut at
// least one release after the release that carries L1 (D3300), and that one is
// 0.29.0 (docs/required-version-stamp, identitySince; docs/release-streams.yaml
// has 0.28.0 current). It is not tagged yet: the release that tags release A
// holds this number to the tag it cuts, and docs/release.md ("Before you tag")
// has the checklist verify it.
const l8Since = "0.30.0"

// abReposSince is the first fugaro release whose layer reader knows ab.repos,
// the per-repository sampling rate AB1 adds inside the ab: block (D3297). Both
// the layer and the repository's file are decoded with KnownFields, and the map
// is the layer's alone, so a runner older than this refuses the whole document
// at bootstrap, whatever the map holds — an empty one and an explicit null are
// the same line to that decoder (SetsABRepos) — and every repository of the
// project whose job image predates release A fails the same way. It is release
// A's number: the release that ships the key.
const abReposSince = l8Since

// recipeCopySince is the first fugaro release whose runner parses a project
// recipe's copy name — <name>.<owner-repo>-<8 hex>, D3302's amended spelling,
// PR0's IsCopyName — as a recipe name rather than as a name its grammar refuses
// for holding a dot. A task that embeds one is refused on an older image, whose
// runner's recipe.Parse refuses the whole task at bootstrap: the copy is the
// name release A's launcher resolves for a migrated repository (D3303), so a
// launch the newer CLI would run is one the older runner cannot.
const recipeCopySince = l8Since

// l8Skew is one trigger of a launch's release-A gate: the release the trigger's
// subject needs in the job image, and the two clauses the shared image refusal
// reads (checkImageSince): what the image has to know, and the behaviour that
// subject needs of it.
type l8Skew struct {
	Since   string
	Subject string
	Knows   string
}

// l8LaunchSkew is the release-A launch gate's judge (S5; spec §7, R16, D3297,
// D3302): the first of the four triggers a job image older than release A would
// meet on this launch, and the notes for every lifted key the repository's
// default branch still writes, which it prints to warn as it reads them.
//
// The four triggers, in the order they are judged: a copy name the task embeds,
// which the older runner's recipe parser refuses; a lifted key the repository's
// file writes on its default branch, which the older runner honours rather than
// drops; .fugaro/recipes/ on that branch (R16, F19), which the release-A
// launcher no longer reads the recipe from; and the project layer's ab.repos
// (AB1). The notes are C1's (LiftedKeyNotes decides nothing: release A's keys
// still decode and still validate), and they are printed by the judge so that
// every launch prints them once and a launch refused for any trigger has named
// the keys first.
//
// L13's two grade models are the one exception (DK2, R11): they are neither
// noted nor a trigger here, because a runner older than this release already
// dropped them (G3's merge) rather than honouring them, so no image is owed
// anything for the line — they are refused outright, whatever the image, by
// deadGradeModelsRefusal.
//
// Everything is read from a checkout of the repository, or from the task's own
// fields, and nothing at all otherwise — the pattern every other config-key gate
// follows (checkoutRoot, launchFileConfig): a launch from outside the
// repository, or one whose default branch cannot be listed, is judged on what
// the task carries alone rather than refused for a read this CLI cannot make.
//
// The notes are printed whether or not the launch is refused, and the refusal
// names the first key's path alone: the rest are the lines above it.
func l8LaunchSkew(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) (l8Skew, bool) {
	repo := pluginwire.Printable(spec.Repo)
	root := checkoutRoot(ctx, spec.Repo)
	var db defaultBranchResult
	if root != "" {
		db = defaultBranchParse(ctx, root)
	}
	var notes []config.RetiredKeyNote
	if db.read {
		notes = config.LiftedKeyNotes(db.data)
	}
	notes = slices.DeleteFunc(notes, func(n config.RetiredKeyNote) bool { return deadGradeModelPath(n.Path) })
	for _, n := range notes {
		fmt.Fprintf(warn, "note: the default branch of %s writes a key the project now owns: %s\n", repo, n.Why)
	}
	if spec.Recipe != nil && recipe.IsCopyName(spec.Recipe.Name) {
		return l8Skew{
			Since:   recipeCopySince,
			Subject: "the recipe " + pluginwire.Printable(spec.Recipe.Name) + " (a project recipe's copy)",
			Knows:   "a project recipe's copy name (a recipe name, a dot, the repository and a hash)",
		}, true
	}
	if len(notes) > 0 {
		subject := "fugaro.yaml's " + notes[0].Path + " on the default branch of " + repo
		if len(notes) > 1 {
			subject += fmt.Sprintf(" (and %d more lifted keys)", len(notes)-1)
		}
		return l8Skew{
			Since:   l8Since,
			Subject: subject,
			Knows:   "a repository's fugaro.yaml with the keys the project owns dropped and named",
		}, true
	}
	if paths, known := defaultBranchRecipes(ctx, root); known && len(paths) > 0 {
		return l8Skew{
			Since:   l8Since,
			Subject: recipe.RepoDir + "/ on the default branch of " + repo,
			Knows:   "a recipe the project store holds rather than .fugaro/recipes/",
		}, true
	}
	if launchSetsABRepos(ctx, env, spec) {
		return l8Skew{
			Since:   abReposSince,
			Subject: "the project layer's ab.repos (read for " + repo + ")",
			Knows:   "the project layer's ab.repos, its per-repository sampling rate",
		}, true
	}
	return l8Skew{}, false
}

// deadGradeModelPath is one of L13's two dead keys (DK2, R11): a repository's
// own model_grades.<grade>.model, which the project owns — the ladder's grades
// are a model choice of the project's (G3, D2039), and a file's entry is
// dropped and named where it is written. The two are lifted keys
// (config.LiftedKeys) and the only model_grades entries on that list: a grade's
// `.access` is model-credentials-routing's, retired and still honoured until
// that plan's own release 2 (R11), so the model is named here and the subtree
// is not: a `.access` row added to the lift one day would be that plan's to
// refuse, not this one's.
func deadGradeModelPath(path string) bool {
	return strings.HasPrefix(path, "model_grades.") && strings.HasSuffix(path, ".model")
}

// deadGradeModelsRefusal refuses a launch whose default branch's fugaro.yaml
// still writes one of the ladder's two grade models (DK2, R11), with the
// sentence the key's own row carries — the drop's own note, which is what a
// person reads from `fugaro validate` and from `fugaro update` beside it.
//
// It is a refusal from this release rather than from release B, where every
// other lifted key is refused (XB2): the merge has dropped a repository's grade
// since G3, so a file that writes one decides nothing today and nothing a run
// reads changes by refusing the line — which is why no image is owed anything
// for it either (the note that would have gated the image is not a note here).
//
// It is the default branch's own read, like every other trigger of this gate
// (R16): a launch of a repository the checkout cannot be read for — one from
// outside a checkout — is judged on what the task carries, so this refuses
// nothing there, and the runner drops the key the same way on every ref (R2).
func deadGradeModelsRefusal(ctx context.Context, spec *task.Spec) error {
	root := checkoutRoot(ctx, spec.Repo)
	if root == "" {
		return nil
	}
	db := defaultBranchParse(ctx, root)
	if !db.read {
		return nil
	}
	for _, n := range config.LiftedKeyNotes(db.data) {
		if deadGradeModelPath(n.Path) {
			return userErr("%s (the default branch of %s)", n.Why, pluginwire.Printable(spec.Repo))
		}
	}
	return nil
}

// launchSetsABRepos reports whether the layer this launch resolves over writes
// ab.repos, from the two places the launcher can see it. The first is the layer
// the task itself carries (spec.ProjectLayer, embedProjectLayer), which is the
// layer the run reads and the one every launch has — a launch from outside a
// checkout of the repository included, which is why the trigger cannot be read
// from the configuration alone. The second is the configuration the checkout
// resolves over (launchFileConfig), where a cascade project's own layer is read
// from the ref the run checks out; a launch with neither is judged on the task
// alone, like every other read here.
//
// The layer's own text is the question and not the block it decoded to
// (SetsABRepos, AB1): ab.repos is what a runner older than release A refuses to
// decode at all, so an empty map and an explicit null are the same line to it.
func launchSetsABRepos(ctx context.Context, env *cloudEnv, spec *task.Spec) bool {
	if cfg := launchLayerConfig(spec); cfg.Layer.SetsABRepos() {
		return true
	}
	cfg := launchFileConfig(ctx, env, spec)
	return cfg != nil && cfg.Layer.SetsABRepos()
}

// defaultBranchRecipes is what the checkout's default branch holds under
// .fugaro/recipes/, as repository paths, and whether that could be read at all:
// known is false for a checkout with no default branch and for a git read that
// failed, which is not the same answer as a folder that is not there.
//
// It is the default branch's read and not the task's ref's (R16): the
// release-A launcher resolves a repository's recipe as the project's copy
// (PR1, PR2, D3303) whatever the ref it launches, and the migration that
// publishes those copies is the default branch's own (`fugaro update`), so a
// repository whose default branch still holds a folder is a repository whose
// next run resolves another recipe — or none.
//
// A symbolic link at the directory itself is listed as .fugaro/recipes, which is
// a folder this repository holds as much as a tree is.
func defaultBranchRecipes(ctx context.Context, root string) (paths []string, known bool) {
	if root == "" {
		return nil, false
	}
	ref := defaultBranchRef(ctx, root)
	if ref == "" {
		return nil, false
	}
	out, err := gitCmd(ctx, root, "ls-tree", "-r", "-z", "--name-only", ref, "--", recipe.RepoDir).Output()
	if err != nil {
		return nil, false
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == recipe.RepoDir || strings.HasPrefix(p, recipe.RepoDir+"/") {
			paths = append(paths, p)
		}
	}
	return paths, true
}

// checkL8Image and checkCloneL8Image are the launch's own release-A gate, for a
// workflow whose job image is a derived one and for a checkout: clone workflow
// respectively: the pair every other per-workflow image gate has (checkImageSince
// and checkCloneImageSince), judged by l8LaunchSkew, which prints the notes for
// the lifted keys the default branch writes. The refusal is the shared one, so
// it names the release, the image to move and `fugaro update` as the fix, and it
// carries no second way out: every trigger is a document the repository or its
// project holds, and the migration that empties it is that one command's.
func checkL8Image(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	sk, ok := l8LaunchSkew(ctx, env, spec, warn)
	// L13's dead keys first: they are refused whatever the image (DK2, R11),
	// where every trigger below is about what an older image does with the
	// launch.
	if err := deadGradeModelsRefusal(ctx, spec); err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return checkImageSince(ctx, env, slug, spec, kind, sk.Since, sk.Subject, sk.Knows, "", warn)
}

// checkCloneL8Image is checkL8Image for a checkout: clone workflow: see
// checkCloneImageSince.
func checkCloneL8Image(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	sk, ok := l8LaunchSkew(ctx, env, spec, warn)
	// See checkL8Image: the dead keys are refused whatever the image.
	if err := deadGradeModelsRefusal(ctx, spec); err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return checkCloneImageSince(env.lc, spec, sk.Since, sk.Subject, sk.Knows, "", warn)
}
