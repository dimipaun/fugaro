// Package initflow is the converge loop behind the installation commands
// (design doc m11-setup-and-skills.md §3.1): an ordered list of stages, each
// with a read-only check and plan, a confirmed apply and a verify; a loop
// that applies the first stage that is not done, stops at the first stage
// that needs the user or fails, and runs nothing after it save where the row
// the stage sits on names that stop (pastStops, RunsPastStop), a stage that
// splits its own work on one being told which they are (PastStops); and the
// plan view and the JSON result that report where the run stands.
//
// The package knows nothing about Google, Terraform or the CLI: the stages
// that exist (and the fakes the tests use) plug in through Stage. It does not
// know which command called it either — the lines it prints name the caller's
// Options.Program, never a verb of its own. What a stage must never be able to
// do is enforced here, not in the stage: --yes does not reach a stage that
// creates a project, links billing or takes a secret, no stage runs before
// the ones ahead of it are done or skipped unless its own row says it runs
// past that stop (pastStops, RunsPastStop), and a run that cannot prompt
// never prompts.
package initflow

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/report"
)

// State is where a stage stands.
type State string

const (
	Done     State = "done"      // nothing to do
	Changed  State = "changed"   // applied and verified in this run
	Blocked  State = "blocked"   // cannot be evaluated until something else is done
	NeedsYou State = "needs-you" // only the user can do the next step (see Left)
	Skipped  State = "skipped"   // does not apply to this run (Detail says why)
	Failed   State = "failed"    // the stage's apply, verify or check failed
	Todo     State = "will-do"   // something to apply
	// BillingPending is a stage that did its part and stopped at the billing
	// link, which the person running setup may not be allowed to make: not a
	// failure, a pause (BillingPausedError). WaitingForBilling is every later
	// stage that needs billing (NeedsBilling), which waits for it rather than
	// failing; a stage that needs none still runs.
	BillingPending    State = "billing-pending"
	WaitingForBilling State = "waiting-for-billing"
)

// NeedsBilling is whether a stage needs the GCP project to have billing: every
// stage but the ones before the project's own and the ones that write local
// files only — the plugin stage, which wires the checkout's settings and
// installs the plugin in the user's own Claude Code, and the checkout stage.
// The checkout's cloud step is the part of that stage that needs billing, and
// it is the step that says so: waiting for billing on the stage would leave the
// plugin's own writes unmoved over a build that is asked for after them.
func NeedsBilling(name string) bool {
	switch name {
	case Preflight, Project, Plugin, Checkout, Unit:
		return false
	}
	return true
}

// MachineLocal is whether a stage is only this machine's: it reads no cloud and
// writes nothing outside the machine it runs on. Such a stage is not stopped by
// a cloud stage that stopped in front of it (blocker), which is what lets the
// one documented command do the local half for a reader who holds no owner's
// role (D954, U8 of docs/plugin-user-scope/implementation-plan.md; spec §11).
//
// The marker is name-level for NeedsBilling's reason: the loop is handed
// stages and knows them by name, and the invariant it carries — no cloud read,
// no write outside this machine — is a property of the stage's own code, kept
// by hand where that code is (Plugin, internal/cli/init_plugin_stage.go: the
// plugin installed in the user's own Claude Code through the claude CLI, and
// the checkout's own .claude/settings.json only where a pin an older fugaro
// left is there to take out — a write a coding agent's session refuses, so a
// marked session's own writes are the machine's). A stage whose local half sits
// beside a cloud write is not one of these, whatever it also writes: Checkout
// is the case that says so — its cloud step is a cloud write, and the loop
// cannot tell the two halves apart.
//
// The tool that runs such a stage after a stop reads the managed gate before it
// writes and refuses on the gate's zero value (the plugin stage's removal): a
// step that now runs inside a managed project's agent session is a writing step
// of a gated command, and the refusal is the direction that keeps it.
func MachineLocal(name string) bool { return name == Plugin }

// Names of the stages, in the order the design runs them (§3.1). A stage
// is registered under exactly one of them.
const (
	Preflight     = "preflight"
	Project       = "project"
	Services      = "services"
	Installation  = "installation"
	Firebase      = "firebase"
	Images        = "images"
	Installation2 = "installation-2"
	Secrets       = "secrets"
	Plugin        = "plugin"
	Repository    = "repository"
	// Rules is the deployed security rules generation
	// (internal/cli/init_rules_stage.go): the document a project policy is
	// read under, and its marker. It comes after the repository stage and
	// BEFORE the policy stage, because the policy stage's write is gated on
	// that marker — a policy written under rules that predate the policy
	// model is a policy no run reads — so a run that migrates the node
	// without deploying the rules first refuses its own work and leaves the
	// whole upgrade for a second command (the owner's rule of 2026-10-30:
	// fugaro update is the whole upgrade).
	Rules = "rules"
	// ModelPrices is the owner's own config rewritten in place: the old
	// cache-price key names (cache_read, cache_write_5m, cache_write_1h) become
	// the ones this release documents (docs/spend-accuracy SA4.2, D2646). It is
	// the owner's file and this machine's alone, so it asks nothing and runs in
	// an agent's session too (LocalUnderAgent), and the plan places it before
	// the policy stage (SA4.2): the rename is applied to the config the run
	// holds, so the node that stage publishes and the run's own write of the
	// file are made from the renamed copy rather than from the bytes read at
	// boot. The node has no spelling for the order to fix — it carries each
	// price as the folded multiplier, with no key name on it (policyPrices).
	ModelPrices = "model-prices"
	// Policy is the published policy node's migration in place
	// (internal/cli/init_policy_migrate.go, the owner's rule of 2026-10-30:
	// fugaro update is the whole upgrade). It comes after the repository
	// stage because the node it reads and rewrites is what those jobs'
	// runs are admitted by, and it is the one stage whose change is a
	// document rather than an installation.
	Policy = "policy"
	// CredentialsService is the credentials service's own stage
	// (internal/cli/init_stages.go's newCredentialsStage): the installation
	// root once more, with the service's image moved onto this release's. It
	// comes immediately AFTER the policy stage, and that position is the whole
	// of it (the owner's ruling of 2026-11-10): the service reads the published
	// node per call and refuses a node its own binary does not understand, so a
	// release whose node version moves has to have the node moved before the
	// process that reads it is replaced — a service replaced first refuses
	// every cascade and Bitbucket-proxied call until the node catches up. The
	// reverse direction is the gap between this stage and the one before it,
	// and it is as short as the order allows: an older service meeting the new
	// node refuses a call for the length of this stage's own plan and apply,
	// and no shorter gap exists, because the node is one database write and the
	// service's replacement is a Terraform apply of the installation root.
	CredentialsService = "credentials-service"
	// CredentialsCopy is the copy of this installation's model keys into the
	// credentials service's bundle (internal/cli/update_keys.go, KS12 of
	// docs/keys-through-service, D3001, R4): for each model key the
	// installation holds outside the bundle, the newest version of its
	// container is read in memory and written under C1's name for the
	// repository it is for, with the container and the version it came from
	// recorded on the entry.
	//
	// It comes immediately AFTER the credentials service's own stage, and
	// that position is the whole of it: what it writes a value INTO is the
	// service's bundle, and a document holding an entry the deployed service
	// does not know is a document that service refuses WHOLE (the loader's
	// strictness, F22/F26) — halting every caller of it, coding runs included.
	// The copy goes through the same guard as every other writer of the
	// family, so a service too old for the entry refuses the write rather than
	// landing it; the stage's position is what makes the guard's answer a
	// state the same run has already fixed.
	CredentialsCopy = "credentials-copy"
	// ImageMapRetire is the stage that takes the legacy repo-to-image member
	// off the published policy node (internal/cli/repo_images_retire.go, U1c of
	// docs/image-map-by-provider, D3376 as amended): image_map is the map a
	// release from before L11 reads, repo_images the one this release's
	// credentials service reads, and the old member stays on the node until the
	// service that reads it has been replaced — the release behind finds no map
	// at all on a node carrying only repo_images.
	//
	// It sits after the credentials service's own stage for exactly that
	// reason, and the write is one conditional PUT of the node with the member
	// removed.
	ImageMapRetire = "image-map-retire"
	// VersionStamp is the required-CLI stamp's stage
	// (internal/cli/update_version_stamp.go, D3260, R1): the node
	// config/policy/required_cli, which says the oldest fugaro release that may
	// act on the project, raised to this release once the installation has
	// converged on it. Nothing else writes that node, and it is never lowered
	// (D3262).
	//
	// It sits immediately AFTER the credentials copy, and with nothing between
	// them that the stamp's own fence would refuse: the stage raises the node
	// only once every other stage of the run — the policy and rules it needs
	// among them — has converged, and the image-map retire, which follows it, is
	// the last of the stages that writes a document a deployed service reads
	// (R1). A stamp
	// raised before one of them left an installation whose own `fugaro update`
	// could not finish: an older binary is refused by this node, so the run
	// holding the half-finished installation has to be this release's.
	VersionStamp = "version-stamp"
	// Workspace is the workspace OWNER's published record
	// (internal/cli/update_workspace.go, D1292): `workspaces/<name>` in the
	// workspace's own database, built from the record this installation
	// declares (workspace_members and workspace_cap) and migrated in place
	// when an older release wrote the node. It comes after the policy stage
	// and before the layer's: the two are the run's published documents, and
	// this one decides who is in the group the workspace's own service rolls
	// up and admits against. A MEMBER's run of the same command registers the
	// stage and skips it — a member holds no grant on the workspace's project
	// at all — so it is the owner's installation that publishes.
	Workspace = "workspace"
	// CredentialsRoute is the MEMBER's published record of where each of its
	// repositories' credentials is served from (internal/cli/update_route.go,
	// WS9, D2973/D2982): `config/policy/route/<slug>` in the member's own
	// database, one record per repository, carrying the workspace's service
	// address from the member's own pin and, per kind, whether the project's
	// own copy serves it or the workspace's service does. A run reads it once
	// at bootstrap and keeps the answer, so this stage is what writes the only
	// leaves a run's reads are made of.
	//
	// It comes after the policy and the workspace stages: what it publishes is
	// derived from the policy node the same run publishes (which credentials
	// the member's own policy admits) and from the membership key and pin the
	// workspace stage's own subjects declare — recording a route before the
	// node it is derived from would publish a decision no policy backs. It
	// comes BEFORE the layer's, and it needs neither the layer nor the
	// checkout: the record is a database write and nothing else.
	CredentialsRoute = "credentials-route"
	// WorkspaceProject is the workspace's OWN project's root
	// (internal/cli/workspace_create.go, W3): the workspace's own service
	// account and the one grant that reaches the workspace's database, in the
	// project the workspace is named after. It comes after the installation's
	// own stages — the project, its Firebase and its database are what the
	// account is created in and what the grant is made on — and BEFORE the
	// record's publish, which is the other half of the same thing: this stage
	// builds the identity the workspace's own service runs as, and the record
	// stage publishes what that service reads. An installation that declares no
	// workspace registers it and skips it, so a member's project and a plain one
	// never carry the account.
	WorkspaceProject = "workspace-project"
	// Lift is the L8 migration of the repositories' own values into the
	// project (internal/cli/update_lift.go, docs/repo-override-rulings D3305,
	// D3306): the stage runs every registered planner over the repositories
	// update can list, prints every line of every change before any write,
	// and writes what the planners decided — the edits into the local layer
	// file, the recipe objects into the runs bucket, the cap nodes into the
	// budget database — each behind the same confirmation every other
	// writer of this door passes, and records each repository it migrated in
	// the runs bucket once that record is due.
	//
	// It comes BEFORE the layer stage, and the order is the whole of the
	// hand-off: the layer FILE is this stage's write and the published
	// OBJECT is the layer stage's (D830/D831), so a stage that edits the
	// file has to run before the one that publishes it, and the record of a
	// repository whose edits the layer stage never published is not written
	// at all (the stage forgets its read so the layer stage plans again
	// after this one's write).
	Lift = "lift"
	// Layer is the published project layer and the one local layer file the
	// owner edits (internal/cli/init_layer_stage.go, D830/D831): the stage
	// seeds the file from the published object when it is absent, publishes
	// the file when it differs, and records the generation the file was
	// written from (D854). It comes after the policy stage and BEFORE the
	// checkout stage, and Checkout needs it: the checkout stage refreshes
	// the checkout's images off the project layer, so a layer that changed
	// commands or image settings has to be published before that refresh, or
	// the run builds from the layer the owner just replaced.
	Layer = "layer"
	// Checkout is what `fugaro update` does for the checkout it is run in, as
	// a stage of the converge itself (internal/cli/init_checkout_upgrade.go,
	// the owner's rule of 2026-10-30: fugaro update is the whole upgrade, and
	// one command, so a checkout's half may not be a second command).
	// It comes after the repository stage, whose onboarding is what the
	// images it refreshes belong to, and before compute, which stays last.
	Checkout = "checkout"
	// Unit is the login unit that runs the orchestrator daemon
	// (internal/cli/init_orchestrator_stage.go for `setup`, which installs the
	// first one, and internal/cli/update_unit_stage.go for `update`, which
	// installs one where a machine has none and re-renders one already
	// installed). The two are
	// one stage because the loop takes one stage per name and the door decides
	// which of them it is (initStages): the owner's ruling Q2 of 2026-11-04
	// gives the first install to `setup`, T7's of 2026-11-05 makes
	// `fugaro update` re-render an installed unit so that one command stays
	// the whole upgrade, and D2700 of 2026-11-16 as corrected (F48) has that
	// same command install the unit a machine is missing, at a person's own
	// terminal and with nothing asked.
	Unit = "unit"
	// Compute is the Batch backend's installation-level provisioning
	// (design docs/compute-backend/spec.md §8.1 task 6). It is last
	// because it is the only stage that may not run before everything else
	// is done: what it provisions is for a backend no run can select yet.
	Compute = "compute"
)

// slot is a stage's place in the canonical order: what it needs ahead of
// it, and whether --yes may reach it.
type slot struct {
	name  string
	needs []string
	// noYes marks the stages --yes never covers: the project's creation and
	// billing linkage (own flags and typed confirmations) and a secret (a
	// hidden prompt, never a flag).
	noYes bool
	// pastStops names the stages whose needs-you stop this stage still runs
	// past (RunsPastStop): the stages that stopped with something only the
	// user can give, and only over a subject this stage does not read at all.
	// Nothing else is one of them — a failure left a state nothing is known
	// about, a block a stage's own Check reported is not the user's to clear,
	// and a stage that writes installation state is not one of them whatever
	// the stop is about.
	pastStops []string
}

// RunsPastStop reports whether the stage name still runs when the stage
// stopped stopped with a step only the user can give (NeedsYou) and the loop
// has nothing else to go on. It is the one place that answers it, and the
// answer is a property of the stage: every row of order is empty but the
// checkout's, whose one remaining step reads no repository state of its own — a
// reader of the run is owed its own line about the images whatever the
// repository stage did, and the plugin step that must be reached past that stop
// is the plugin stage's, ahead of the repository stage (U1) — because the
// user's fix for a stopped stage can be weeks away and the door the run's
// lines name is `fugaro update` (the owner's rule of 2026-10-30: one fugaro
// update is the whole upgrade) — a door that must not be left unable to clear
// the one line it is the remedy for (#1836). Which steps of a tolerant row may
// not run past the stop is the row's own business, and the stage is told what
// it is running past for it (PastStops).
func RunsPastStop(name, stopped string) bool {
	for _, s := range order {
		if s.name == name {
			return slices.Contains(s.pastStops, stopped)
		}
	}
	return false
}

// Gating is linear: a stage runs only when every registered stage before it
// is done or skipped, save for the tolerance pastStops states (RunsPastStop),
// which is the stages whose needs-you stop this one still runs past. needs
// records why the order is what it is, and the tests check it against the
// order; it does not gate anything itself.
//
// order is the dependency order the dogfooding run discovered by hand:
// installation, Firebase, images, the history job, secrets, repository.
var order = []slot{
	{name: Preflight},
	{name: Project, needs: []string{Preflight}, noYes: true},
	{name: Services, needs: []string{Project}},
	{name: Installation, needs: []string{Services}},
	{name: Firebase, needs: []string{Installation}},
	{name: Images, needs: []string{Installation, Firebase}},
	{name: Installation2, needs: []string{Firebase, Images}},
	{name: Secrets, needs: []string{Installation2}, noYes: true},
	{name: Plugin, needs: []string{Preflight}},
	{name: Repository, needs: []string{Installation2, Secrets}},
	// The rules generation is what the policy node is READ under, so it is
	// deployed before the node that depends on it: the policy stage's write
	// holds the publisher's gate, which refuses a node under rules that
	// predate the policy model, and a run whose rules are stale would refuse
	// its own migration and leave the upgrade unfinished.
	{name: Rules, needs: []string{Repository}},
	// The owner's own cache-price keys are renamed before the node that is
	// built from that config is published, so the published copy and the file
	// say the same thing about the same price.
	{name: ModelPrices, needs: []string{Repository}},
	// The policy node is what every run of the project is admitted by, and
	// the repository stage is the last writer of the installation those runs
	// read it against.
	{name: Policy, needs: []string{Repository, Rules}},
	// The credentials service's image moves after the node does, and nothing
	// else in the run is between them: the service refuses a node its own
	// binary does not understand, so the node has to be this release's before
	// the process that reads it is replaced (CredentialsService's own comment,
	// the owner's ruling of 2026-11-10).
	{name: CredentialsService, needs: []string{Policy}},
	// The copy of the installation's model keys, immediately after the service
	// whose bundle it writes them into and with nothing between them: the
	// document it leaves must be one the service that reads it can load, and
	// the stage before this one is the run's one move of that service's image
	// (CredentialsCopy's own comment has the window).
	{name: CredentialsCopy, needs: []string{CredentialsService}},
	// The legacy repo-to-image member leaves the node after the process that
	// reads it has been replaced (ImageMapRetire's own comment, U1c, D3376 as
	// amended): the release behind's credentials service reads image_map and
	// finds no map on a node that carries only repo_images, so the two moves
	// are this order and no other.
	{name: ImageMapRetire, needs: []string{CredentialsService}},
	// The required-CLI stamp is raised last of the stages that write anything
	// a deployed reader sees (R1, D3267): it names this release as the oldest
	// that may act on the project, so a stamp raised before the run had
	// converged would refuse that same run's own remaining work — an older
	// binary is what the node stops, but a run holding a half-finished
	// installation is one the person would have to finish by hand. It needs
	// the policy node and the rules it is read under, which are the two the
	// stage names as its own reason for waiting (needs records why, and F2's
	// gating is linear: these do not gate anything themselves).
	{name: VersionStamp, needs: []string{Policy, Rules}},
	// The workspace's own project's root, before the record it serves: what it
	// creates is the identity the workspace's own service runs as, and the
	// record stage publishes the node that service reads. It needs the
	// installation's own stages (Installation2's rows are what carried the
	// database and the state bucket it writes its state into), and --yes never
	// covers it: what it applies is an access GRANT, and the cover admits no
	// grant (spec §6).
	{name: WorkspaceProject, needs: []string{Installation2}, noYes: true},
	// The workspace record is the owner's own declaration, and the mark that
	// lets this stage write into that database is the installation's
	// (budgetDBUnadopted): it comes after the installation's own stages, and
	// after the policy one because the two are the run's published documents
	// and the policy node is what every run of the project is admitted by.
	{name: Workspace, needs: []string{Installation2, Policy}},
	// The route is the member's own record of where each repository's
	// credentials are served (internal/cli/update_route.go). It needs the
	// policy stage, because which kind the member's own copy serves is a
	// question asked of the node that stage publishes, and it needs the
	// installation's own stages for the database it writes into — the same
	// two the workspace record needs and for the same reason.
	{name: CredentialsRoute, needs: []string{Installation2, Policy}},
	// The L8 migration edits the local layer file, and the layer stage is
	// what publishes it: the two are one hand-off in one order, so the
	// migration comes first and a repository's record is written only once
	// that publish has landed (docs/repo-override-rulings D3305, M2). It
	// reads the published policy's admits for the repositories it can name,
	// so it waits for the policy stage like the two documents beside it do.
	{name: Lift, needs: []string{Repository, Policy}},
	// The layer is what the checkout's images are built from, so it is
	// published before the refresh that reads it: the checkout stage's cloud
	// step builds this checkout's images off the layer that applies, and a
	// layer published after it would leave the run building from the one the
	// owner just replaced.
	{name: Layer, needs: []string{Repository}},
	// The checkout's own step (the cloud step) runs after the
	// repository stage: the images it refreshes are that repository's, and the
	// onboarding the repository stage does is what the refresh builds for. The
	// pin and the plugin are the plugin stage's since U1 (D941,
	// docs/plugin-user-scope/implementation-plan.md), which is what puts the
	// step that clears the staleness line every command prints ahead of the
	// repository stage rather than behind it.
	//
	// pastStops: the one row that has it, and the tolerance is what the
	// stage's remaining step is dropped under rather than run. The cloud step
	// needs the repository the stop is about — its job images are built from
	// it — so a run running past that stop drops the step, says which it
	// dropped in the stage's own line, and the loop tells the stage the stops
	// it is running past for exactly that (PastStops). The three stages before
	// it publish the installation every run of the project is admitted by,
	// which is why none of them is here: a run with a stage left for the user
	// does not write that (accounts_add_converge_test.go is the door that says
	// so).
	{name: Checkout, needs: []string{Repository, Layer}, pastStops: []string{Repository}},
	// The login unit that runs the daemon. It comes after the checkout's own
	// steps and before compute: the daemon it starts drives the project the
	// repository stage published and re-reads the policy node every stage
	// before it wrote, so a run that installed a unit ahead of those would
	// start a daemon that cannot read what it drives. Like Plugin and
	// Checkout it writes only this machine's own files — one file under the
	// person's home — so billing does not gate it (NeedsBilling).
	//
	// noYes: --yes never covers it. A unit is code that runs at every login, so
	// neither door may take a flag for it (the owner's Q2 answer of 2026-11-04
	// and T7's ruling of 2026-11-05). Neither door asks a question of its own
	// either: Q2's first install has no confirmation beside the plan line the
	// person already read, and T7's re-render has none either — the owner's
	// rule of 2026-11-10 is that `fugaro update` carries no extra prompt — so
	// the step the run cannot make is a step --yes does not cover, which the
	// loop leaves for the user (the update door) or skips with the sentence
	// that says nobody was at the terminal (the setup door). What fences the
	// write is the session, not a question: a coding agent's run writes no
	// unit at all, and the drift stays visible through `fugaro doctor`.
	{name: Unit, needs: []string{Repository, Policy}, noYes: true},
	// The compute backend's provisioning is money, permanent and owner-run,
	// so --yes never covers it; it comes last, after every stage that
	// builds the identity, the registry and the installation it reads.
	{name: Compute, needs: []string{Repository}, noYes: true},
}

// runCovered are the stages whose ordinary steps the run's one confirmation
// covers (design m11 §9, "one confirmation"): the Terraform applies of the
// installation, the Firebase root, the history job and the repository, and
// what goes with them (the state bucket, the Viewer-read removal, the
// Firestore marks and rules, the deployed security rules generation, the image
// copies that add tags, the local config). Everything else is never covered by
// it: the project's creation and billing, a secret, and the plugin wiring (its
// own y/N).
//
// The map answers for WHOLE STAGES, and that is the whole of what it says. A
// step finer than its stage — a billable image build inside Images, replacing a
// registry tag and onboarding an unlisted repository inside Repository — is not
// a row here and cannot be one: whether such a step is covered is the
// confirmation of the caller that takes it, decided where the step is taken,
// which is also the only place that knows which front door is running and so
// which steps that door owns.
var runCovered = map[string]bool{
	Installation: true, Firebase: true, Images: true, Installation2: true, Repository: true, Rules: true,
	CredentialsService: true,
}

// RunCovers reports whether the run's one confirmation may cover the stage's
// ordinary steps. A stage --yes does not cover (noYes) is never covered.
func RunCovers(name string) bool { return runCovered[name] && YesCovers(name) }

// displayNames is what each stage is called on the line a person reads: the
// stage's own name above is an internal ID, and the step line says the work in
// plain words instead while the ID stays in the log and in --json (D3435, spec
// §4). Every stage of order has a row here, and a stage a later task adds
// without one prints its ID rather than a blank line.
var displayNames = map[string]string{
	Preflight:          "Checking the environment",
	Project:            "Creating the Google Cloud project",
	Services:           "Enabling the Google APIs",
	Installation:       "Applying infrastructure changes",
	Firebase:           "Setting up the Firebase project",
	Images:             "Pinning the release images",
	Installation2:      "Deploying the scheduled jobs",
	Secrets:            "Collecting the secrets",
	Plugin:             "Installing the plugin",
	Repository:         "Onboarding the repositories",
	Rules:              "Deploying the security rules",
	ModelPrices:        "Renaming the config keys",
	Policy:             "Publishing the policy",
	CredentialsService: "Updating the credentials service",
	CredentialsCopy:    "Copying the model keys",
	ImageMapRetire:     "Retiring the old image map",
	VersionStamp:       "Raising the required CLI version",
	Workspace:          "Publishing the workspace record",
	CredentialsRoute:   "Publishing the credential routes",
	WorkspaceProject:   "Creating the workspace project",
	Lift:               "Migrating the repository settings",
	Layer:              "Publishing the project layer",
	Checkout:           "Updating the checkout",
	Unit:               "Installing the login unit",
	Compute:            "Provisioning the compute backend",
}

// DisplayName is the plain-words name a stage prints on its step line. The ID
// itself is the answer for a name this table does not carry.
func DisplayName(name string) string {
	if d, ok := displayNames[name]; ok {
		return d
	}
	return name
}

// Names is the canonical order of every stage name.
func Names() []string {
	out := make([]string, len(order))
	for i, s := range order {
		out[i] = s.name
	}
	return out
}

// Needs is the stages name must come after.
func Needs(name string) []string {
	for _, s := range order {
		if s.name == name {
			return append([]string(nil), s.needs...)
		}
	}
	return nil
}

// YesCovers reports whether --yes may confirm the stage.
func YesCovers(name string) bool {
	for _, s := range order {
		if s.name == name {
			return !s.noYes
		}
	}
	return false
}

// LeftKind is how the user does what a stage leaves them.
type LeftKind string

const (
	LeftCommand LeftKind = "command" // one line to run
	LeftPrompt  LeftKind = "prompt"  // a prompt fugaro shows in a terminal
	LeftConsole LeftKind = "console" // something done in a web console
	// LeftRefused is a step the managed gate refused (D1012): nothing was
	// written, and nothing the reader types or runs writes it. It is a kind of
	// its own rather than a command or a prompt because that is the whole of
	// what the reader has to be told, and because the command a stage's own
	// Left carries beside it would ask for exactly what the gate has just
	// made impossible.
	LeftRefused LeftKind = "refused"
)

// Left is one thing only the user can do: a single line of text, never a
// continued or multi-line command and never a secret's value. When there are
// commands to paste, each is one complete line of its own in Commands, run in
// order, never joined by && or ;, and Text is the sentence that introduces
// them. A LeftCommand with no Commands is itself the one command — unless its
// Text is a refusal rather than a command, which is a step whose command would
// refuse where it stands (internal/cli's catalog step, an entry whose own
// definition its build refused): the run prints what refused it and nothing
// the reader can run, because there is nothing to run until the refusal is
// acted on.
type Left struct {
	Stage    string   `json:"stage"`
	Kind     LeftKind `json:"kind"`
	Text     string   `json:"text"`
	Commands []string `json:"commands,omitempty"`
	// Why is the reason this step was not done, one line, in the refusal's own
	// words: what the run reads under the step it leaves, and the fact a reader
	// who was not at the terminal has no other way to get. The loop fills it
	// from the stage's own result when the stage set none and the stage
	// stopped before applying — a stage that only said "run this in your own
	// terminal" has said what it owes and not why — so a stage that knows why
	// it stopped says so once, for the log and for --json, and not only in the
	// line it printed in flight.
	//
	// It is empty when there is no reason beside the step's own Text. A stage
	// that leaves a step by returning NeedsYouError carries that sentence and
	// nothing else — the error type has no detail of its own, where
	// BillingPausedError does; a reason equal to Text is dropped by one rather
	// than printed twice; and a stage that applied work and left a step sets
	// this itself or not at all, because its detail is a success line ("No
	// changes", "installed v0.26.0") and a success line is not a reason.
	Why string `json:"why,omitempty"`
	// Brief is a refusal's account as the terminal prints it, one part per
	// line: what was refused, why in a clause, what to do (the owner's
	// feedback of 2026-11-09). A refusal's Text argues the case at paragraph
	// length — the gate's own sentence, quoted, among it — and Text is what
	// the log holds and the only thing a reader outside this binary sees, so
	// this never reaches the wire (json:"-"): the two fields say the same
	// thing at two lengths and not two things.
	Brief []string `json:"-"`
}

// one makes the step's one-line fields one line wherever they are printed and
// takes the run's redactions over them: a value the process holds must not
// reach the screen through a stage's own sentence or through the reason it
// gives for the step, and the two are normalised the same way for that reason.
// A reason that turns out to be the step's own sentence is dropped here and
// not printed twice: the input phase's wizard is the stage that answers a stop
// with its own question as both the line and the reason (wizardStage.Check),
// and one line is all the step owes.
func (lf *Left) one(redact func(string) string, redactAll func([]string) []string) {
	lf.Text = redact(oneLine(lf.Text))
	lf.Why = redact(oneLine(lf.Why))
	if lf.Why == lf.Text {
		lf.Why = ""
	}
	if len(lf.Commands) > 0 {
		lf.Commands = redactAll(lf.Commands)
		for i, c := range lf.Commands {
			lf.Commands[i] = oneLine(c)
		}
	}
}

// PrintedCommands is every command line the Left asks the user to run.
func (l Left) PrintedCommands() []string {
	if len(l.Commands) > 0 {
		return l.Commands
	}
	if l.Kind == LeftCommand && l.Text != "" {
		return []string{l.Text}
	}
	return nil
}

// NoTerminalAdvice is what to do when a typed confirmation or a hidden prompt
// has no real terminal: the one sentence every refusal ends with.
const NoTerminalAdvice = "run it in your own terminal window, not through a coding agent or a pipe"

// NeedsTerminal is the one refusal for a step that has to be typed at a real
// terminal; what names the step.
func NeedsTerminal(what string) string {
	return what + " needs a real terminal: " + NoTerminalAdvice
}

// Status is a stage's read-only check.
type Status struct {
	State  State
	Detail string // one line: what is missing, or why skipped or blocked
	Left   *Left  // for NeedsYou; else the stage's own Left() is used
	// Undone marks a Skipped answer that left the stage's own work undone —
	// the bucket it publishes into could not be read, the node it migrates is
	// held or is another installation's — as against one that means the stage
	// found nothing to do. Both are one word in State because both are true of
	// the run: neither is a failure, neither stops the run, and the plan view
	// prints the same line for the two. What reads this is a caller settling a
	// record of the steps a machine still owes (internal/cli's applyAnswered):
	// a step whose stage skipped without doing its work is a step still owed
	// (D1567, D1575), and one whose stage found nothing to do is answered.
	Undone bool
}

// Env is what a stage's Plan and Apply are told.
type Env struct {
	// Yes is whether the stage may take --yes as its confirmation. It is
	// never true for a stage --yes does not cover, whatever the flag says.
	Yes bool
	// Interactive is whether the stage may prompt: a terminal, and not
	// --non-interactive.
	Interactive bool
}

// Plan is what a stage's real plan found.
type Plan struct {
	Detail      string // one line, such as "14 to create, 0 to change, 0 to delete"
	NothingToDo bool   // the plan is empty: the stage is done ("No changes")
}

// Outcome is what an apply did.
type Outcome struct {
	Changed bool   // false: the stage found nothing to do ("No changes")
	Detail  string // one line
	// Left is a step the stage applied what it could without: something of
	// its work this session may not do (the checkout's cloud half in a coding
	// agent's session, where every write must be on this machine —
	// LocalUnderAgent). The loop records it as left-for-you beside the
	// stage's line, so the run reports the step and exits 1 rather than
	// reporting a stage that skipped half of itself as done.
	Left *Left
}

// Stage is one step of the converge. Check, Plan and Verify only read;
// Apply is called only after the loop has decided a confirmation can be
// had, and keeps its own confirmations (typed project name, billable build):
// the loop never confirms for a stage.
type Stage interface {
	Name() string
	// Check says what is missing or different, without changing anything.
	Check(ctx context.Context) (Status, error)
	// Plan shows what Apply would do (the stage's real plan, where there is
	// one) and changes nothing. Its one-line summary joins the plan view.
	Plan(ctx context.Context, env Env) (Plan, error)
	// Apply makes the stage's change after its own confirmation. A stage
	// that finds nothing to do returns Outcome{Changed: false}.
	Apply(ctx context.Context, env Env) (Outcome, error)
	// Verify re-reads what Apply claims to have done; an error fails the
	// stage and nothing after it runs.
	Verify(ctx context.Context) error
	// Left is what the user does when the stage needs them (a prompt the
	// loop cannot let it show, a command to run).
	Left() Left
}

// InputStage is a Stage that needs inputs only flags can give without a
// terminal. Missing names each flag (with its purpose) still absent.
type InputStage interface {
	Missing() []string
}

// SelfConfirming marks a stage that shows its own plan and takes its own
// confirmation (the typed project name, or --yes), and itself refuses, with
// a one-line instruction, to go on without a terminal and without --yes. The
// existing init engines are such stages: a run with neither still shows
// their plan before they refuse. The loop lets them run with
// Env.Interactive false; any other stage is not run at all in that case. A
// stage --yes does not cover can never be one.
type SelfConfirming interface {
	SelfConfirming()
}

// LocalUnderAgent marks a stage whose writes are all on this machine: in a
// coding agent's session it still applies, and it refuses there whatever
// reaches outside the machine. Nothing else checks that for it, so the refusal
// is the stage's own, and it must have one: a stage that implements this and
// then writes to the cloud would do it.
//
// Two stages of fugaro update are such stages, and both are written on this
// machine's files: the plugin stage (internal/cli/init_plugin_stage.go), whose
// writes are the checkout's .claude/settings.json and the plugin in the user's
// own Claude Code; and the checkout stage (internal/cli/
// init_checkout_upgrade.go), whose one step left is the cloud half, dropped
// under a marker with that step's own sentence and left for the user as a
// named step. A marker stops a run from doing what only a person should
// confirm: the pin and the plugin are not that — they are the writes the
// user's own session makes with fugaro update, on files it reads anyway — and
// what reaches outside the machine keeps its refusal, in the step that makes
// it.
type LocalUnderAgent interface{ LocalUnderAgent() }

// PastStops is implemented by a stage whose own steps are not all alike about
// the stop its row tolerates (pastStops, RunsPastStop): the loop hands it the
// stops it is running past before it checks, plans and applies it. They are
// the run's own stops and not a reading of the world — a stage that asked the
// world instead would be answering with its own guess at a verdict the loop
// already has, and would be wrong about it wherever the state moved after the
// stopped stage answered (the run's own writes move it: the repository stage's
// anchor edits the checkout's fugaro.yaml in the run that onboarded it).
//
// It exists for one stage: the checkout's inside fugaro update (internal/cli/
// init_checkout_upgrade.go). The steps that read no repository file — the pin
// and the plugin, which the plugin stage owns since U1 — are ahead of the
// repository stage and need no tolerance; what is left in the checkout is its
// cloud step, which builds the job images from the very repository that stop is
// about, and it is dropped rather than run past the stop. The stage is told
// which stops it is running past so that the reader gets that step's own line
// out of a run the stop would otherwise end at "waits for repository", and it
// is every needs-you stop of that stage the cloud step is dropped for — the
// repository stage's are not all about its file (#1836).
type PastStops interface{ PastStops(stops []string) }

// StageError is a failure that carries the one-line fix.
type StageError struct {
	Err error
	Fix string
}

func (e *StageError) Error() string { return e.Err.Error() }
func (e *StageError) Unwrap() error { return e.Err }

// NeedsYouError is returned by Apply when it cannot proceed without the
// user (a typed confirmation not given): the loop stops with Left, which is
// not a failure.
type NeedsYouError struct{ Left Left }

func (e *NeedsYouError) Error() string { return e.Left.Text }

// BillingPausedError is returned by Apply (or wrapped by Check's status, see
// BillingPending) when the stage's own work is done but the project has no
// billing yet: the loop records it as BillingPending with Left, lets the
// stages that need no billing run, and marks the rest WaitingForBilling.
type BillingPausedError struct {
	Left   Left
	Detail string
}

func (e *BillingPausedError) Error() string { return e.Left.Text }

// MissingInputsError is a run that never prompts and has a terminal neither:
// its single refusal, listing every flag every stage still needs to be given
// on the command line instead.
type MissingInputsError struct{ Flags []string }

func (e *MissingInputsError) Error() string {
	return "this run never prompts and has no terminal, so a flag is needed for every question it cannot ask: missing " + strings.Join(e.Flags, "; ")
}

// NoTerminalError is the refusal to prompt without a terminal: the step is the
// owner's own and there is nobody here to ask, so the refusal names the
// terminal it needs and no flag stands in for it (T11 of
// docs/no-prompt-policy). With an agent's marker set it is the agent's own
// sentence instead: a coding agent does not get to confirm anything.
type NoTerminalError struct {
	Stage string
	Agent string // the coding agent's environment marker, "" for none
}

func (e *NoTerminalError) Error() string {
	if e.Agent != "" {
		return AgentRefusal(e.Agent)
	}
	return NeedsTerminal("typing the confirmation for " + e.Stage)
}

// AgentAdvice is the one sentence a refusal for a coding agent's session
// says. The check is a mitigation, not a barrier (an agent can unset its own
// environment): it stops the accident, not the adversary.
const AgentAdvice = "run this in your own terminal window, not through a coding agent"

// IDEMarker is the one marker the Claude Code IDE extension also sets in a
// person's own integrated terminal (VS Code, JetBrains).
const IDEMarker = "CLAUDE_CODE_SSE_PORT"

// IDEHint is the sentence for a person whose own IDE terminal tripped the
// check, and only for IDEMarker: for any other marker it is "", because a
// recipe for getting past the check has no business in text an agent reads.
// It is worded for the person, and says to do it themselves.
func IDEHint(marker string) string {
	if marker != IDEMarker {
		return ""
	}
	return "if this is your own IDE terminal and not a coding agent's, you can unset " + IDEMarker + " there yourself"
}

// AgentRefusal is why a run in a coding agent's session applies nothing; it
// names the marker, never offers --yes, and carries the IDE hint only for the
// marker an IDE extension also sets.
func AgentRefusal(marker string) string {
	msg := "a coding agent's session is present (" + marker + " is set), so nothing is applied: " + AgentAdvice
	if h := IDEHint(marker); h != "" {
		msg += " (" + h + ")"
	}
	return msg
}

// AgentError is the refusal of an applying step in a coding agent's session:
// the loop reports it as the user's to do, not as a failure.
type AgentError struct{ Marker string }

func (e *AgentError) Error() string { return AgentRefusal(e.Marker) }

// GatedError is a step the managed project's gate did not admit (D1012,
// D1023): the loop reports it as the user's to do, in the gate's own sentence
// rather than in the session's, because what refused the step is the document
// the gate read and not the marker the session carries. A session that read
// AgentRefusal here would be told to leave the run to someone else; a session
// that reads this one is told what about the document the gate would not
// admit, which is the account its reader acts on.
//
// Reason is the gate's sentence as the gate wrote it (managed.NotConverged and
// the rules that fired, rendered by internal/managed), and Err is that refusal
// as a value, so a caller can match on it (managed.ErrNotConverged) without
// reading English.
//
// Refusal is what that answer leaves the reader, and it is set where the
// refusal is built rather than read off Err here: only that caller knows
// whether the gate judged a document at all. The zero value is the plan
// refusal, which is the answer that must never hand the reader a confirmation
// to type (CV1); Left turns the four into the left-for-you.
//
// Owner is the left-for-you of the one refusal whose remedy is a command the
// reader already has (GateRefusedDelete): the flag that admits a refused
// removal, the address the gate refused and the identity of the object it
// takes away are the producer's to name — the door, the flag and the plan are
// all its — so it is built whole there and carried here. Left hands it back
// with the stage's name filled in.
type GatedError struct {
	Reason  string
	Err     error
	Refusal GateRefusal
	Owner   Left
	// Reasons is the same refusal as the rules that fired, one entry each, in
	// the producer's own words and with no Terraform address in them: what
	// the plan's three short lines name is the thing the gate refused
	// ("cover: a member the review did not list"), and the address each rule
	// was read at stays in the log beside the sentence (D1352, the owner's
	// feedback of 2026-11-09). Empty is a refusal judged without them — a
	// step whose document the producer refused by its own rules — and the
	// brief then says only that no table of Fugaro's covers it.
	Reasons []string
}

func (e *GatedError) Error() string { return e.Reason }
func (e *GatedError) Unwrap() error { return e.Err }

// GateRefusal is what the gate's answer about a step was, as far as the person
// who reads the refusal is concerned.
type GateRefusal uint8

const (
	// GateRefusedPlan is the gate reading the document and not admitting it:
	// the cover or the cost list, with no command of the reader's own that
	// admits what it refused. The step is nobody's from here — the plan is
	// outside what Fugaro's own tables cover, so what fixes it is a release
	// and not a confirmation — and the reader is told exactly that instead of
	// being handed the stage's prompt to type the project's name, which is the
	// deadlock CV1 is about (D1012: no typed confirmation reaches past a
	// refusal).
	GateRefusedPlan GateRefusal = iota
	// GateRefusedUnjudged is a step no plan gate may judge at all, whatever the
	// document looks like (D1026: the policy publish that would loosen the
	// published node, set an account-choice key or write the project's first
	// node; a workspace publish that admits a member). The step IS the
	// owner's, at their own terminal, so the stage's own Left — the one
	// command they run themselves (spec §4.3) — is kept, and an agent's
	// report to its owner stays actionable.
	GateRefusedUnjudged
	// GateRefusedUnread is there being no verdict to act on: a document that
	// could not be produced or read, a mark that could not be read a second
	// time immediately before the write. Nothing is known about the plan,
	// which is what the reader is told rather than that a cover refused a
	// shape it never saw.
	GateRefusedUnread
	// GateRefusedDelete is the plan removing a resource the reader's own
	// --allow-delete would admit (tf.Listable): a delete or a replace is their
	// decision and not a session's, and the command that carries it already
	// exists (spec §4.3's "For the owner" sentence, which promises that
	// command and the identity the plan's before view carries). So this
	// refusal is neither the plan's "file an issue" — nothing is missing from
	// Fugaro's tables, the owner simply has not approved the removal — nor the
	// stage's prompt, which asks for a typing the gate never reaches: what it
	// leaves is Owner, the producer's own command line. The plan's own
	// section for it is §4.1's delete rule 1.
	GateRefusedDelete
)

// Left is the left-for-you of this refusal: the stage's own for the step that
// is the owner's to run themselves, the producer's command for the removal
// their own flag admits, and the refusal's own commandless text for every
// other answer.
func (e *GatedError) Left(stage Left) Left {
	switch e.Refusal {
	case GateRefusedUnjudged:
		return stage
	case GateRefusedDelete:
		owner := e.Owner
		if owner.Kind == "" {
			// A producer that classified the refusal without building the
			// command it promises: the plan's own text, never the empty line
			// an empty Left would print.
			return Left{Kind: LeftRefused, Text: gateOutsideCover(e.Reason), Brief: coverBrief(e.Reasons)}
		}
		owner.Stage = stage.Stage
		return owner
	case GateRefusedUnread:
		return Left{Kind: LeftRefused, Text: gateUnjudged(e.Reason), Brief: unjudgedBrief()}
	default:
		return Left{Kind: LeftRefused, Text: gateOutsideCover(e.Reason), Brief: coverBrief(e.Reasons)}
	}
}

// coverBrief and unjudgedBrief are the two refusals as the terminal prints
// them: three short lines, no longer than a step's own line, saying what was
// refused, why in a clause and what to do. They are separate from the
// sentences above and not a cut of them: a refusal's full text is an argument
// — the gate's own sentence, naming the resource, is quoted in it — and what
// the person at the terminal needs is the fact, the cause and the door, with
// the argument in the log the step's line prints the path of (D1352).
func coverBrief(reasons []string) []string {
	why := "what it would write is not one of the shapes Fugaro's own tables cover"
	if len(reasons) > 0 {
		why = "refused by " + report.Short(strings.Join(reasons, "; "))
	}
	return []string{
		"the managed gate refused this step and nothing was written",
		why,
		"a person files an issue with this line; the whole sentence is in the log",
	}
}

func unjudgedBrief() []string {
	return []string{
		"the managed gate did not judge this step and nothing was written",
		"no verdict was reached: a failure of the run, not of your plan",
		"the whole sentence is in the log",
	}
}

// gateOutsideCover is the refusal of a plan the gate's own rules would not
// admit. It says the four things this reader needs and names nothing they can
// act on in this terminal: the step was refused and nothing was written, the
// gate's sentence for why (quoted — it names the resource or the member the
// cover would not admit, which is what an issue needs), that there is nothing
// to type and no command that helps, and that the cause is a plan outside what
// those rules cover, which is a person's issue to file and a Fugaro release to
// fix.
func gateOutsideCover(reason string) string {
	return "the managed gate refused this step and nothing was written: " + reason +
		". Nothing here is yours to type or to run, because no confirmation reaches past a refusal (D1012): what the gate would not admit is a plan its own tables do not cover, which is fixed in Fugaro and not in your project — a person files an issue with the sentence above, and the next release reads it"
}

// gateUnjudged is the refusal of a step the gate reached no verdict on, and it
// is a different sentence on purpose: there is no plan to blame and no shape
// missing from Fugaro's tables, so blaming the cover here would be the same
// mistake as handing the reader a prompt, in the other direction. What is left
// is the run's own failure, named as one.
func gateUnjudged(reason string) string {
	return "the managed gate did not judge this step and nothing was written: " + reason +
		". Nothing here is yours to type or to run: the gate reached no verdict it could act on, so nothing was applied (D1012), and what the sentence above names is a failure of the run rather than of your plan"
}

// AgentProceeded is the other half of AgentRefusal: the sentence for the one
// case in which a step in a coding agent's session runs all the same — D55's
// answer, the managed project whose plan the gate admitted (Managed.Removes).
// It sits beside AgentRefusal because both are about the same marker, and
// because the reason is decided here: a caller that printed its own sentence
// would be a second copy of this rule.
//
// It names the marker, says what the step is proceeding on, and names no
// marker, variable or flag a reader could unset to get here — the same rule
// IDEHint follows, and for the same reason: a recipe for getting past the
// check has no business in text an agent reads. What it does NOT say is that
// the step is safe: the three things that replace the refusal (the gate, the
// owner's delete list, the marker) are named where they are decided, and the
// session that reads this is the one that could go around all three.
func AgentProceeded(marker string) string {
	return "a coding agent's session (" + marker + " is set) on a managed project: the gate admitted the plan, so nothing is confirmed here"
}

// GateProceeded is the sentence for the same step in the OWNER'S OWN session: a
// managed project, a document the gate admitted, and no confirmation asked for
// (the ruling of 2026-11-01). It is a second sentence rather than
// AgentProceeded's with an empty marker, because the two are addressed to two
// readers: this one is read by the person the prompt would have asked, and the
// one thing it has to say to them is why nothing is being asked.
//
// It follows AgentProceeded's own rule about what it may name — no flag, no
// variable, no marker a reader could set to reach past the gate — and it names
// where the verdict was written instead, because an owner who wants to check
// afterwards is not trying to get past anything.
//
// No surface prints it any more (the owner's run of 2026-11-27/28): a step that
// needs no answer prints one plain line saying what it did, and the sentence
// accounting for the confirmation the verdict replaced is what made a finished
// step read as a question. It stays as the owner's half of AgentProceeded and
// as the design's own vocabulary; cli's gateSentence is where that choice is
// made, and it answers "" for the owner's own terminal.
func GateProceeded() string {
	return "this project is managed and the gate admitted the plan: it recorded the verdict, so nothing is typed here"
}

// GateOrdinary is the sentence for an ordinary step of the same run that the
// gate does NOT judge document by document — the Terraform state bucket, an
// image copy that only adds a tag, the local config — and which the run's own
// review announced instead. It says what is true of it and no more: the plan
// the step prints is the plan it applies, the marks were read, and the
// confirmation a managed project does not ask for is the one the person at the
// terminal asked not to see — or, where nobody is at the session, the one no
// person could have given (check 292). Saying GateProceeded's words here would
// claim a verdict for a document no rule read, which is the direction every
// other sentence in this file refuses to take.
func GateOrdinary() string {
	return "this project is managed and the run announced this step: the marks were read, so nothing is confirmed here"
}

// OwnerProceeded is the sentence for the same reader as GateOrdinary — the
// person whose prompt the step would have been — at the steps the gate has no
// document for and no verdict about: a policy whose next generation loosens
// what the project may spend, a project layer this run republishes, the local
// config. Nothing about them is judged plan by plan, so this sentence claims
// no verdict; what it says is the owner's ruling of 2026-11-06, that a person
// at their own terminal on a managed project is not asked to type a
// confirmation for a step that loses no data and takes no secret value, and
// that the line above is the note of what changed.
//
// It follows GateOrdinary's own rule about what it may name — no flag, no
// variable, no marker a reader could set — and it is a third sentence rather
// than that one's words because the reader of this one is owed the difference:
// GateOrdinary speaks for a step the run announced and the gate let through,
// this one for a step no rule judged at all.
func OwnerProceeded() string {
	return "this project is managed and this is the owner's own terminal: the marks were read, so nothing is typed here"
}

// AskRestored is the line a managed project's own terminal reads when --ask
// was given: the flag's whole promise, said once, at the point where the gate
// would otherwise have taken the confirmations away. It says the two things a
// reader has to be able to check — the prompts are asked here, and the gate's
// own judgement is not weakened by asking for them (a step whose document the
// gate refuses is still refused, before any prompt) — because the one mistake
// this flag could be read as is "the gate is off for this run".
func AskRestored() string {
	return "--ask: on this managed project the confirmations the gate replaces are asked here, and the gate is unchanged: still judged, still refused"
}

// AskUnmanaged is the one line --ask prints where the marks did not answer
// "managed" — a project that is not managed, and a read that could not be made
// or did not answer (the two are one answer to every caller, which is
// Managed's own contract). It says why the flag does nothing here and that it
// is not an error, because the alternative a caller might reach for — refusing
// the flag — would fail a command that behaves exactly as it always has.
func AskUnmanaged() string {
	return "--ask: this project's marks do not answer \"managed\", so this run asks the confirmations it always asks; that is not an error"
}

// AskValues is the line --ask prints on `fugaro update` where its value entry
// turns on and a value is really asked for at the owner's own terminal (D3347,
// R18). The secrets stage prints it, at the point where it asks, and never the
// gate: whether there is a value to ask for — one the stage finds missing, for
// a checkout it can ask about — is known there and nowhere earlier, and a line
// the gate printed ahead of that would claim an ask that does not happen (a
// project whose every name is stored, a list of many repositories the stage
// leaves to `fugaro secrets set`). It is not AskUnmanaged's words: that line
// promises a run that behaves exactly as it always has, and here the flag does
// something new.
func AskValues() string {
	return "--ask: a missing secret value is asked for at this terminal"
}

// AskNoGate is the line --ask prints in a run the managed gate never covers:
// --plan-only applies nothing by its own definition, and a run whose own
// arguments shape the installation is one the gate must never judge (the
// confirmations it keeps are whole). Neither has a step whose confirmation the
// gate stands in for, so there is nothing for --ask to restore and no mark is
// read for it.
func AskNoGate() string {
	return "--ask: this run applies nothing the managed gate stands in for, so no confirmation was replaced and there is nothing to restore"
}

// AskRefusal is the refusal of --ask where there is no prompt to answer it: a
// coding agent's session, --json, or stdin that is not a
// terminal. A prompt nobody can answer is a refusal and never a silent
// proceed, and the run stops before it writes anything rather than falling
// back to the gate's verdict — which is the one thing the flag could not mean,
// since the verdict is exactly what the owner asked to be asked instead.
//
// It names no flag, no variable and no marker a reader could set to reach the
// step (the rule AgentRefusal and AgentProceeded follow), and here that rule
// has teeth: the move it must not name is "drop the flag that asked for these
// confirmations", which would hand the step back to the gate's verdict — the
// write the owner asked to be asked about, made without them. So the sentence
// says what was asked for and who is not there, and not how to get past it.
// The marker's own value is the one name it carries, because it is the session
// the reader is in and not a thing to set (AgentRefusal's rule).
func AskRefusal(c Conditions) string {
	return "the confirmations of this managed project were asked for and " + askNobody(c) + ": a confirmation nobody can answer is a refusal here and never a silent proceed, so nothing is applied and the step is not taken on the gate's verdict instead — " + AgentAdvice
}

// askNobody names why no confirmation can be answered in this session.
func askNobody(c Conditions) string {
	switch {
	case c.Agent != "":
		return "a coding agent's session is present (" + c.Agent + " is set)"
	case c.NonInteractive:
		return "this run never prompts"
	case c.JSON:
		return "--json never prompts"
	default:
		return "this run has no terminal on its input"
	}
}

// AskDecision is the whole of what --ask does once the marks have been read,
// as one rule rather than a copy per command: the line the run prints, or the
// refusal. managed is what the read answered (both marks there and readable,
// the same value every caller decides a step under). values is whether this
// run's --ask also turns on value entry — the update door alone (D3347, R18) —
// so that a project the marks did not answer "managed" for is not answered with
// the no-op line every other door's --ask prints there.
//
// An empty line and an empty refusal is "the flag prints and refuses nothing
// here" — the flag was not passed, or the door's value entry answers nothing of
// its own: the gate knows no value is missing there and no list can be asked
// about, so AskValues is the secrets stage's line, printed where it asks (the
// stage leaves its own `fugaro secrets set` lines where it cannot, D1540).
// Otherwise exactly one of the two is answered, and the order is the rule's: a
// project the marks answered "managed" for restores the gate's confirmations
// and refuses where no prompt can be answered, and one the marks did not answer
// "managed" for is the no-op line unless --ask turns value entry on there.
func AskDecision(c Conditions, managed, values bool) (line, refusal string) {
	canPrompt := c.Agent == "" && !c.NonInteractive && !c.JSON && c.Terminal
	switch {
	case managed && canPrompt:
		return AskRestored(), ""
	case managed:
		return "", AskRefusal(c)
	case !values:
		return AskUnmanaged(), ""
	default:
		return "", ""
	}
}

// Conditions are what a run says about who is there.
type Conditions struct {
	// Yes is a run carrying its own answer for the steps that answer reaches,
	// and NonInteractive one that never prompts. Neither is a flag a command
	// has any more (T11 of docs/no-prompt-policy): what carries them is the
	// run Fugaro makes of itself — accounts add's converge, which sets both —
	// and a run a test builds for the shape they stand for. Every rule here
	// reads them where they take a confirmation away.
	Yes, NonInteractive, JSON, Terminal bool
	Agent                               string // a coding agent's environment marker, "" for none
	// Ask is the owner's --ask in a project the marks say is managed: the
	// prompts the gate would have stood in for are asked here instead. It never
	// widens anything: the gate's own admission is decided before this field is
	// read and is unchanged by it (GateStandsIn answers false under it, so a
	// step falls back to exactly the answer it would have had if the project
	// were not managed). A caller that could not prompt at all never sets it:
	// --ask is refused there (AskRefusal), because a prompt nobody can answer
	// is a refusal and never a silent proceed.
	Ask bool
}

// Managed is the managed-project state a step's confirmation is decided
// under: the two facts about the plan that is about to be applied, which a
// caller has read and passes in. It exists so D55's answer — the coding
// agent's refusal is removed in a managed project — is one case of one rule
// rather than a second copy of it, and so a caller that has read nothing
// passes the zero value and keeps today's behaviour exactly.
//
// It is not a field of Conditions, and that is deliberate: Conditions is what
// a RUN says about who is there — its four fields are enumerated exactly by
// internal/cli's completeness test, and the property test runs over the whole
// product of them — while these two facts are about a plan ONE step is about
// to apply. A run has one Conditions, and one Managed per applying step.
//
// The zero value is "not managed", so every caller that does not know about
// managed projects — init's embedded roots, the plugin stage — refuses under a
// coding agent's marker as it did before this type existed, and the only
// caller that can reach the managed case is the managed apply itself.
type Managed struct {
	// Managed is whether the creation-fact read answered managed: true only
	// with a nil error (infra/managed.IsManaged's contract). A marker that is
	// absent, unreadable, of another project or of a version this build does
	// not know is false, and false is the answer that keeps the refusal.
	Managed bool
	// Admitted is whether the gate's verdict admitted THE PLAN OF THE STEP
	// BEING CONFIRMED (managed.Verdict.OK, Tasks 1.0-1.3). It is about this
	// step's plan and not about the project: a verdict for another plan, or a
	// refusal, or a gate that was never run, is false.
	Admitted bool
	// Fallback is whether the whole-project inventory (managed.Inventory,
	// design D59, wired in by the plan's Task 4.5) decided that this run keeps
	// its confirmations: a resource the installation's own state does not
	// account for, or a read that could not be made at all. It is the THIRD
	// fact of the managed case, and it is a field here rather than a caller's
	// decision for the reason this type exists: D59's fallback is exactly "the
	// confirmations this project's plan would otherwise not need", and the
	// removal of the coding agent's refusal IS that confirmation. So a
	// fallback suppresses Removes and nothing else — the plan may still be
	// applied, by a person who is asked, and never unattended beside a project
	// nobody looked at.
	//
	// The zero value is "no fallback", which keeps every caller that does not
	// know about the inventory exactly where it was; the caller that knows is
	// the managed apply, and it passes a value that falls back when the read
	// was not made (managed.InventoryCheck's own contract), so a caller that
	// forgets to thread the read through falls back rather than proceeds.
	Fallback bool
}

// Removes reports whether the managed case removes this step's confirmation:
// every condition, held at once — the project is managed, the gate admitted
// this step's plan, and the whole-project inventory did not fall back. Any
// other combination keeps the step exactly where it was, which is the direction
// D59 and D55 both end in: what a coding agent's session cannot give is kept
// wherever the inventory could not clear the project. A step no plan gate may
// judge at all never reaches this answer with a verdict — its Converge refuses
// (managed.ConvergeUnjudged, D1026), so Admitted is false for it.
//
// It says nothing about WHO is at the session: Removes may be true in a run
// nobody is at, which is why GateStandsIn asks for the session beside it and a
// refusal is the answer everywhere else.
func (m Managed) Removes() bool {
	return m.Managed && m.Admitted && !m.Fallback
}

// GateStandsIn is the whole of "the gate's verdict IS this step's
// confirmation", in both sessions it is asked in: Removes under a coding
// agent's marker, and the owner at their own real terminal on a managed
// project for a document the gate admitted. A caller asks it when it is about
// to decide what the step proceeds on, and prints AgentProceeded's or
// GateProceeded's sentence from the answer.
//
// The session nobody is at — a pipe with its input closed, a script, a cron,
// check 292 (the owner's, 2026-11-26) — is deliberately NOT one of them, and
// the two arms below are the whole of the reason: this predicate is what the
// step's own confirmation is put to where a person or an agent is present, and
// a step whose answer is instead the run's one confirmation (internal/cli's
// runCovers, which asks NobodyIsAtTheSession beside the marks) reads
// GateOrdinary's sentence rather than this one. Widening it here would reach
// every caller at once, `fugaro apply` included, whose own question a person
// must be there to answer (internal/managed's Request.confirm) — and a
// predicate that answered a pipe would send that apply to a question with
// nobody to give it.
//
// The order is the rule's: a session with a marker is the agent's case whatever
// else is true of it, and the terminal case cannot have a marker
// (PersonAtTheTerminal says so itself). --ask is asked FIRST, because it is the
// owner taking this question off the table: the gate still judges the document
// — that is the caller's own admit, made before this predicate is reached, and
// this predicate changes nothing about it — and what the flag restores is the
// confirmation the verdict would have replaced.
func GateStandsIn(c Conditions, m Managed) bool {
	if c.Ask {
		// The owner asked for the prompts back: whatever the verdict was, this
		// step is the one it would have been in a project that is not managed.
		// It narrows and never widens: what it takes off the table is the
		// branch that admits WITHOUT a prompt.
		return false
	}
	if c.Agent != "" {
		return m.Removes()
	}
	return PersonAtTheTerminal(c) && m.Removes()
}

// NobodyIsAtTheSession reports whether the conditions are a session no person
// is at and no confirmation can be put to: no coding agent's marker, no
// terminal, and neither of the two flags that say what the run does with a
// question (--json, --non-interactive) nor --ask, which is the owner asking for
// the question back. It is a pipe with its input closed, a script, a cron — the
// session check 292 and check 821 drive `fugaro update` in.
//
// It is the counterpart of PersonAtTheTerminal, and the two are the sessions a
// managed project's run answers a step's confirmation in: the gate's own read
// of the marks is what stands in for it in either of them (internal/cli's
// runCovers, and the ruling of 2026-11-01 the managed case carries). A caller
// asks it BESIDE its own read of the marks, never instead of one, and it is
// never a licence to apply a step no verdict admitted: Removes is asked beside
// it wherever a step is decided, and the plan's own cover is still what decides
// whether a step is one the run may take silently (a delete, a replace or an
// IAM binding asks, and a session nobody is at refuses it).
func NobodyIsAtTheSession(c Conditions) bool {
	return c.Agent == "" && !c.Terminal && !c.NonInteractive && !c.JSON && !c.Ask
}

// PersonAtTheTerminal reports whether the conditions put a person at the
// project's own terminal, there to read a document and answer for it: the
// owner's own session (no marker), a real terminal, and none of the three
// things that say nobody can answer a prompt — --ask (the owner asking to be
// asked again is a question to put, never a licence to skip one), a run that
// never prompts and --json.
//
// It is GateStandsIn's condition half, named once so that a caller asking the
// same question about a document the gate's verdict does not cover — the policy
// node a managed project rewrites, whose widening is admitted by no plan gate;
// the ordinary steps of the converge the run's one confirmation covered, the
// owner's ruling of 2026-11-06 — asks it the same way. A caller asks this
// beside its own read of the marks (the gate's own read, never a second one)
// and prints OwnerProceeded's sentence; it is not a verdict about any plan, and
// it admits nothing on its own. NobodyIsAtTheSession is its counterpart, for
// the other session a managed project's run answers in.
//
// The terminal is the load-bearing half and the one a caller writing this out
// again would drop: a pipe carries no marker, so a session nobody is at reads
// exactly like the owner's own, and a coding agent's session is at a terminal
// too. Whether the run carries an answer of its own is deliberately not part
// of it: this asks who is there to read the document, and a run's own answer
// reaches other steps and never this one (CanConfirmRun).
func PersonAtTheTerminal(c Conditions) bool {
	return !c.Ask && c.Agent == "" && c.Terminal && !c.NonInteractive && !c.JSON
}

// CanConfirmRun reports whether the run's one confirmation (the step the run
// announces and then applies, at the person's own answer) can be taken under
// the conditions: a person at a real terminal. --json and a pipe never take it,
// a run that carries an answer of its own already gave it (it covers the
// ordinary steps with no review, and a step that needs the person's own answer
// is not one of them), a run that never prompts has nobody to take it, and
// under a coding agent's marker nothing applies at all. Plan-only runs show
// the review and never take it (the caller's own; nothing is applied there).
func CanConfirmRun(c Conditions) bool {
	return c.Agent == "" && !c.Yes && !c.NonInteractive && c.Terminal && !c.JSON
}

// Validate checks stages are registered once each, under a known name, and
// returns them in the canonical order. A stage's predecessors that are not
// registered are not required (the stages later tasks provide may be
// absent), but every one that is registered is ahead of it.
func Validate(stages []Stage) ([]Stage, error) {
	pos := map[string]int{}
	for i, s := range order {
		pos[s.name] = i
	}
	byName := map[string]Stage{}
	for _, s := range stages {
		if s == nil {
			return nil, fmt.Errorf("initflow: a nil stage")
		}
		if _, ok := pos[s.Name()]; !ok {
			return nil, fmt.Errorf("initflow: unknown stage %q", s.Name())
		}
		if _, dup := byName[s.Name()]; dup {
			return nil, fmt.Errorf("initflow: stage %q registered twice", s.Name())
		}
		byName[s.Name()] = s
	}
	out := make([]Stage, 0, len(stages))
	for _, s := range order {
		if st, ok := byName[s.name]; ok {
			out = append(out, st)
		}
	}
	return out, nil
}
