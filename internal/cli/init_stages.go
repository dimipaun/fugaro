package cli

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The converge (internal/initflow) over the engines the converge already
// has. The stages here only call them: what init, update --firebase-project and the
// rest do, their flags, confirmations and messages, are the engines' own.
// Stages that have no engine yet (project, services, images, the history
// job's second pass, secrets, plugin wiring, repository) are registered
// when the tasks that write them land.

// initEngine is what the engines need, built in the order the converge has
// always built it: the environment check, then the Terraform workdir, the
// Google clients and the Cloud Resource Manager readiness.
type initEngine struct {
	r    *initRun
	lc   *localcfg.Config
	spec infra.InstallationSpec
	path string
	old  []byte

	bin    string
	envErr error // the environment check's refusal, kept as the error init always returned
	envRan bool

	ready   bool
	rmReady bool // the Resource Manager readiness, done once (setupCloud)
	c       *infra.Clients
	t       *tf.TF
	wd      *infra.Workdir

	authorized map[string]bool // repositories the user typed in this run (see init_repo_gate.go)
	declined   map[string]bool // repositories the user was asked about and did not type
	admins     []string        // the GCP project's owners and editors, read once for the review (init_review.go)
	adminsRead bool
	fpChecked  string // "", "yes" or "no": the Firebase project's verification, once
	defaulted  string // the member a new config's launchers and operators defaulted to (init_members.go)
	newMachine string // set when the installation stage wrote a new machine's config from an existing installation instead of applying

	// What the record owed as this run found it, read before the first stage
	// plans (readOwedBefore): the update door clears only the entries of the
	// steps it answered that are still these (clearAnsweredSteps), and a step
	// another terminal queued while the run was going stays owed.
	owedBefore    *apply.Pending
	owedBeforeErr error

	// The one confirmation's review screen is built from the preview's stages
	// and the two stages it asks about (init_review.go).
	previewed []initflow.StageResult
	images    *imagesStage
	repo      *repositoryStage
	policy    *policyMigrateStage

	// The lift stage and the layer stage after it: the migration of what a
	// repository's own file declares into the project holds the first, and the
	// second publishes the layer file it edited. The engine keeps both because
	// the two are one write from the outside — the lift stage forgets the layer
	// stage's cached read of the file it just changed, and the records it
	// migrated are written after the loop, once the publish has landed
	// (update_lift.go).
	lift  *liftStage
	layer *layerStage

	// The credentials service's image as this run resolved it
	// (credentialsResolve): the policy stage asks for it before it raises the
	// published node's version and the credentials-service stage pins the
	// answer after that write, and one resolve is what makes the two the same
	// read.
	pinnedCredentials string
	credentialsRead   bool
	credentialsErr    error

	// checkoutBehind: this run found the checkout behind this release, which
	// the plugin stage is where to read (the pin it wires is this release's tag,
	// and the plugin in Claude Code is this release's too). The checkout stage
	// runs the checkout's cloud half for this checkout, and this is the
	// fact it decides on: the cloud step's own check reads fugaro.yaml and the
	// local config and never a build record (cloudCheck, U9), and the images
	// stage has already pinned this release's base in that config by the time
	// the checkout stage is checked — so a checkout whose job images are still
	// on the previous release's base reads as a finished cloud half there, while
	// this run, the one that moves the checkout to this release, is exactly the
	// run those images move with.
	//
	// Set and never cleared: the pin it was read from is written by this same
	// run, so the answer is about the run rather than about the file as it
	// stands now (init_plugin_stage.go's Check, init_checkout_upgrade.go's).
	checkoutBehind bool

	redact, held []string // the loop's redaction list, and the slots at its end a stage fills with a value it holds

	// accessDB is the grants node's database as the roles step found it
	// (update_access.go): set when this run has a node to read at all, and nil
	// when it has none — no budget backend, or a database this installation
	// never adopted. The installation root re-reads the node through it for the
	// lists its own apply carries (F29a, deriveAccessAgain), which is why the
	// client outlives the step that made it.
	accessDB *rtdb.Client

	// accessSeed is the migration's node as the roles step computed it, waiting
	// for the installation root to write it (update_access.go, landSeed): the
	// step runs before the loop's first stage, the run's confirmation and the
	// guard, so the write is the root's, past all three. It is nil when this run
	// migrates nothing — the node is there, --plan-only, a first run with no
	// list to hold, outputs that could not be read — and it is cleared by the
	// write, so a root that runs twice lands it once.
	accessSeed *accesslib.Node

	// accessMark is the migration's other landing (update_access.go): the roles
	// step read the installation's own lists and they hold no grant the node can
	// rank — a grant names a person, and a group:, a domain: and a
	// serviceAccount: are not addresses the CLI can match a caller against (F25)
	// — so there is no node to write and the marker alone is what lands. It is
	// what tells such an installation, which HAS run this update, from one whose
	// lists the node has never held: the two are one state otherwise, and RS6c's
	// gates would then never run on it. Cleared by the landing, as accessSeed is.
	accessMark bool

	// reposState is the run's per-repository state: the enrolled list every
	// stage reads (memoized here, so one command reads the repositories once)
	// and the lines each stage and step reported about them
	// (update_repos.go).
	reposState reposState

	// inStages is whether the stage loop is running: a stage reports its
	// per-repository lines while it does and a step after the loop reports
	// them outside it, and only a step's are the run's notices
	// (reportRepos, runStages).
	inStages bool

	// flagFactVersion is the version the self-flag step's own read of the
	// creation fact answered with, and 0 where that step read nothing
	// (update_selfflag.go). The marker step asks it beside the gate's version
	// for the one installation whose fact the gate never saw: the half-written
	// flag, whose object write succeeded and whose label write then failed. A
	// run that finishes that flag has read the object — the step could not
	// complete the object otherwise — so the version the migration needs is
	// already in hand, and taking a read of its own for it would be the second
	// read of an object this run has answered once.
	flagFactVersion int
}

// environment runs the environment check once (it prints the local
// config's warnings when it passes).
func (e *initEngine) environment() error {
	if !e.envRan {
		e.envRan = true
		e.bin, e.envErr = e.r.checkEnv(e.lc)
	}
	return e.envErr
}

// setup is the workdir, the clients and the Resource Manager readiness
// every installation engine needs, done once. The cloud half is setupCloud,
// which the rollback's refusal takes on its own before the workdir exists.
func (e *initEngine) setup(ctx context.Context) error {
	if e.ready {
		return nil
	}
	if err := e.environment(); err != nil {
		return err
	}
	dir, err := infra.InstallationWorkdir(os.Getenv, e.lc.GCPProject)
	if err != nil {
		return userErr("%v", err)
	}
	e.wd, e.t, err = e.r.terraform(e.bin, dir, "installation")
	if err != nil {
		return err
	}
	e.r.res.Workdir = e.wd.Dir
	if err := e.setupCloud(ctx); err != nil {
		return err
	}
	e.ready = true
	return nil
}

// setupCloud is the half of setup that reaches the cloud and touches no
// workdir: the environment, the clients, and the Resource Manager readiness
// every read of the project is made after. setup calls it, and so does the
// rollback's own refusal, which is decided before the workdir is prepared (see
// runInit's --forget door: the preparation rebuilds the tree, so a refused run
// may not reach it). Both callers share it so the work is done once:
// the engine's fields are the guard, and the environment's check prints its
// warnings once for the same reason.
func (e *initEngine) setupCloud(ctx context.Context) error {
	if err := e.environment(); err != nil {
		return err
	}
	if e.c == nil {
		c, err := newInitClients(ctx, e.lc)
		if err != nil {
			return err
		}
		e.c = c
	}
	if e.rmReady {
		return nil
	}
	if err := e.r.resourceManager(ctx, e.c); err != nil {
		return err
	}
	e.rmReady = true
	return nil
}

// secretsHeld lists values the process holds from the start that must never
// reach an output; none. The values the secrets stage prompts for are held
// later, in the slots options adds to the list (see initEngine.hold). Tests
// replace it.
var secretsHeld = func() []string { return nil }

// slots builds the run's redaction list once: the values the process held from
// the start (secretsHeld), then one slot per value a stage prompts for
// (initRun.hold). The loop and the log share it, and the log reads it at each
// line, so the slots are the same list as the run grows into it.
func (e *initEngine) slots() {
	if e.redact == nil {
		base := secretsHeld()
		e.redact = append(slices.Clone(base), make([]string, maxHeldSecrets)...)
		e.held = e.redact[len(base):]
	}
}

// redaction is the list a run's log is redacted with, read at each line: what
// slots built, as the stage that holds a value fills it in (F6).
func (e *initEngine) redaction() func() []string {
	e.slots()
	return func() []string { return e.redact }
}

// options is the loop's configuration from the flags.
func (e *initEngine) options() initflow.Options {
	r, o := e.r, e.r.o
	e.slots() // built once: the loop, the stage and the log share the slots
	opts := initflow.Options{
		Yes: o.yes, NonInteractive: o.nonInteractive, PlanOnly: o.planOnly,
		Terminal: r.stdinTerminal(), JSON: o.asJSON, Agent: agentMarker(os.Getenv, r.stdinTerminal()), Out: r.w,
		Program: o.loopProgram(),
		// The run's account: one line per stage and the closing line, in place
		// of the result header and the plan view (CL5b). A run that has no
		// reporter — --print-vars, --forget, --config-only, or a test's
		// hand-built engine — prints exactly what it printed before.
		Report:  r.rep,
		Detail:  r.accountWriter(),
		Project: r.projectName, GCPProject: r.gcpProject, Region: e.lc.Region,
		Redact: e.redact, OnPreview: func(rs []initflow.StageResult) { e.previewed = rs },
		// The per-repository lines a stage reported go to the run's own
		// account, hint first (U13): a stage that does per-repository work
		// says one line per repository under its header, whatever door the run
		// came in by.
		OnRepos: e.printRepoLines,
		// The managed gate is `fugaro update`'s (D1016, update_managed.go):
		// managedStage answers which door and which stages, and the loop asks
		// it only in a coding agent's session, so a person's run never reads a
		// mark on account of this line.
		ManagedGate: e.managedStage,
	}
	// The event feed (setup_events.go): a run told to write one hands the loop
	// the three moments a consumer renders — a stage reached, a stage settled,
	// and a step left for the user — and a run without --events hands it
	// nothing at all, which leaves the hooks nil and is exactly the run the CLI
	// had before the flag existed.
	if r.events != nil {
		opts.OnStep, opts.OnStage, opts.OnLeft = r.events.step, r.events.stage, r.events.left
	}
	return opts
}

// initStages is the stages converge() runs, in the canonical order, built
// here rather than inside converge so that the registration itself is one
// named thing: which stages a run has, and which flag a stage is behind, is
// the confirmation matrix's rows (setup_refusals_test.go, which calls this
// with an engine and reads the list back). The engines' own behaviour is
// theirs; this function is only who is in the run.
//
// Both front doors run this one list, and the door only removes: `update` is
// everything `setup` registers, minus the project stage's creation path (a
// project it never creates) and minus the Batch backend's (the stage is
// behind --provision-batch, which update refuses). So update's stages are
// preflight, installation, Firebase, images, installation a second time,
// secrets, plugin, the repository of the checkout it is run in (W5.1), the
// deployed security rules generation, the published policy node itself
// (migrated and published from the local config) and the checkout's own
// steps: fugaro
// upgrade's pin, plugin and cloud steps for that checkout
// (init_checkout_upgrade.go, the owner's rule of 2026-10-30). There is no
// services stage in either
// list: initflow.Services is a slot in the canonical order nothing registers.
func initStages(e *initEngine) []initflow.Stage {
	e.images, e.repo = newImagesStage(e), newRepositoryStage(e)
	stages := []initflow.Stage{&preflightStage{e}, newInstallationStage(e), newFirebaseStage(e), e.images, newInstallation2Stage(e)}
	// The policy stage last: it writes the node every run of the project is
	// admitted by — migrated in place when it is an older node, built from the
	// local config when it does not carry what the config states — and the
	// stages before it are what those runs are launched against (see the
	// stage's own file for why it is a migration and never a delete and a
	// republish). Its write and the Bitbucket proxy switch S6 turns on in the
	// local config are ONE step for the reason the switch's file gives: the node
	// and the entry have to agree for every reader of this same update, and the
	// repository stage, which is the reader that refuses a disagreement, is
	// earlier in this list.
	stages = append(stages, newSecretsStage(e), newPluginStage(e), e.repo)
	// The rules stage before the policy stage: the deployed security rules
	// generation is what a published policy node is read under, and the policy
	// write is gated on it — a run that migrated the node first would refuse
	// its own write and leave the upgrade for a second command
	// (init_rules_stage.go, the owner's rule of 2026-10-30).
	stages = append(stages, newRulesStage(e))
	// The owner's own cache-price keys, renamed in place, before the policy
	// stage as the plan puts it (SA4.2) and before the writes made from this
	// config later in the run: the rename is applied to the config the run
	// holds, so the policy node and the run's own write of the file are made
	// from the renamed copy. The node is the same either way — its prices are
	// the folded multipliers, not key names (update_model_prices.go).
	stages = append(stages, newModelPricesStage(e))
	e.policy = newPolicyMigrateStage(e)
	// The run's own local-config write drops the read this stage's plan is made
	// from (initRun.writeConfig), so the stage is reachable from there too.
	e.r.policyStage = e.policy
	stages = append(stages, e.policy)
	// The credentials service's own stage, immediately after the policy one and
	// with nothing between them: the service reads the published node on every
	// call and refuses a node its own binary does not understand, so the node
	// has to be this release's before the process that reads it is replaced
	// (initflow.CredentialsService has the order and the window the order
	// leaves; the owner's ruling of 2026-11-10).
	stages = append(stages, newCredentialsStage(e))
	// The copy of this installation's model keys into that service's bundle,
	// immediately after the stage that deploys it and with nothing between
	// them: what it writes a value into is the document that service reads, the
	// guard it goes through reads the release of that service (so a service too
	// old for the entry refuses the write rather than landing it), and the
	// position is what leaves that refusal a state this same run has already
	// fixed (initflow.CredentialsCopy has the window). It is registered for
	// both doors, like the service's own stage: an installation that has not
	// turned the service on skips it with one line.
	stages = append(stages, newCredentialsCopyStage(e))
	// The required-CLI stamp, registered for both doors and immediately after
	// the credentials copy: it is the last stage of the run that writes
	// something a deployed reader sees, and it raises the node that refuses
	// every fugaro below this release (update_version_stamp.go, D3260, R1). It
	// is registered unconditionally because the stage's own Check is what
	// decides whether this installation has a stamp to raise — a project whose
	// database is not this installation's, or that records no shared backend at
	// all, skips it with that reason rather than with a registration it does
	// not have.
	stages = append(stages, newVersionStampStage(e))
	// The workspace's own project's root BEFORE the record, and both after the
	// policy one: this stage creates the identity the workspace's own service
	// runs as — its own service account and the one grant that reaches the
	// workspace's database — and the record stage right after it publishes the
	// node that identity reads (workspace_create.go, W3). An installation that
	// declares no workspace is not a workspace's own and skips it without
	// opening a workdir.
	stages = append(stages, newWorkspaceProjectStage(e))
	// The workspace record after the policy one: both are documents a run of
	// `fugaro update` publishes from the local config, and this one is the
	// workspace OWNER's — the member list and the cap the workspace's own
	// service rolls up and admits against (update_workspace.go, D1292). A
	// member's run of the same command registers the stage and skips it without
	// opening a database: a member holds no grant on the workspace's project at
	// all.
	workspace := newWorkspaceStage(e)
	// The run's own local-config write drops the read this stage's plan is made
	// from (initRun.writeConfig), so the stage is reachable from there too: the
	// write is where a run that first names a budget backend gives this stage a
	// database to publish the record in.
	e.r.workspaceStage = workspace
	stages = append(stages, workspace)
	// The credential route right after the workspace record and before the
	// layer: it is the MEMBER's own published document, derived from the policy
	// node the run has just published (which accounts and forge credentials the
	// member's own policy admits) and from the membership key and pin the
	// workspace stage's own subjects declare (update_route.go, WS9). A project
	// in no workspace registers it and publishes nothing, removing whatever a
	// former membership left behind — which is what makes leaving a workspace
	// converge with no step of its own.
	stages = append(stages, newCredentialsRouteStage(e))
	// The layer stage after the policy one: the project layer this run would
	// publish is read under the node the policy stage just moved in place, so a
	// layer whose claims the older node could not carry is judged by the same
	// installation it is published for (init_layer_stage.go).
	//
	// The lift stage comes immediately before it, and the order between these
	// two is the whole of the migration's promise: the lift stage moves what a
	// repository's own file declares into this same layer file, so the document
	// the layer stage publishes is the one the migration just wrote, and the
	// records it keeps wait for that publish (update_lift.go, L8, D3305).
	e.lift = newLiftStage(e)
	stages = append(stages, e.lift)
	e.layer = newLayerStage(e)
	stages = append(stages, e.layer)
	// The checkout's own steps (the cloud half; the pin and the plugin are
	// are a stage of this run too: fugaro update is the whole upgrade, so the
	// one command the user runs may not leave the checkout it runs in behind
	// (init_checkout_upgrade.go, the owner's rule of 2026-10-30).
	stages = append(stages, newCheckoutStage(e))
	// The login unit that runs the orchestrator daemon: `fugaro setup` writes
	// the first one (init_orchestrator_stage.go, the owner's Q2 answer of
	// 2026-11-04) and `fugaro update` installs one where a machine has none
	// and re-renders one already installed (update_unit_stage.go, T7's ruling
	// of 2026-11-05 as amended by D2700 of 2026-11-16).
	// The two are one stage — the loop takes one stage per name, and the
	// canonical order holds one slot for it — and the door is what decides
	// which of them this run gets, the way the door decides the project
	// stage's creation path above. It comes after the checkout's own steps
	// because the daemon it starts drives the project they leave behind.
	if e.r.o.door == updateDoor.name {
		stages = append(stages, newUpdateUnitStage(e))
	} else {
		stages = append(stages, newOrchestratorUnitStage(e))
	}
	// The Batch backend's provisioning is the owner's step and nothing else
	// reaches it: the stage is registered only under its own flag, so an
	// installation reaches it by asking for it and never as part of an update
	// (design compute-backend §8.1 tasks 6 and 9).
	if e.r.o.provisionBatch {
		stages = append(stages, newComputeStage(e))
	}
	// The project stage, which is the one stage that may create the GCP
	// project. `setup` registers it on every first run: a first run makes its
	// project Fugaro's (settleCreation sets the field the flag sets), and the
	// stage creates it, adopts one that is already there and holds nothing of
	// Fugaro's, and refuses one that holds anything of Fugaro's unless an
	// earlier run of setup made it Fugaro's. `update` never registers it,
	// and neither does a `setup` whose installation is already there: e.old is
	// the project config's file, and a run that has one converges as update
	// does (D11, reconciliation F24).
	if e.r.o.createProject || (e.r.o.door == setupDoor.name && e.firstRun()) {
		stages = append(stages, newProjectStage(e))
	}
	return stages
}

// stagesNamed is the named subset of the registered stages: the stages a
// command runs for the change it made, in place of the whole list `fugaro
// update` runs (spec §2.5 rule 2, D1561). accountsAddStages names its three
// through it, and `fugaro apply` runs the stages a pending entry names.
//
// The answer is in initflow's order and not the caller's — initflow.Names,
// which is the linear extension of initflow.Needs — so a stage of the subset is
// never handed to the loop before one it needs that is also in the subset.
// Getting that from the caller's own slice is the mistake this exists to make
// impossible: a subset is not a shorter update, it is a slice of one, and
// initflow.Run's Validate sorts and checks it either way, but a caller that
// built its own slice could hand the loop one it cannot order.
//
// unanswered is the names this door registered no stage under, and they come
// back rather than being dropped, for the reason apply.Order keeps the names it
// cannot place: a run that quietly did fewer steps than it was asked for is the
// one thing neither may do. Two kinds of name land there and they are one fact
// to a caller — a stage of initflow's this door did not register (the services
// slot, which no door fills; the Batch backend's compute and the project stage,
// which a door registers behind a flag or on the first run of setup) and a name
// that is no stage at all (apply.CatalogBuild, which is a step of the change
// table and not one of initflow's). A caller that names stages it knows are
// registered ignores it; a caller whose names come from a file it did not write
// reads it and says what it could not run. A name leaves `want` only when a
// stage really answered it, so "not a stage of this door" and "not a stage"
// are both answered here, and neither is lost.
func stagesNamed(e *initEngine, names ...string) (stages []initflow.Stage, unanswered []string) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	byName := map[string]initflow.Stage{}
	for _, st := range initStages(e) {
		byName[st.Name()] = st
	}
	for _, name := range initflow.Names() {
		if !want[name] {
			continue
		}
		st, ok := byName[name]
		if !ok {
			continue
		}
		delete(want, name)
		stages = append(stages, st)
	}
	return stages, slices.Sorted(maps.Keys(want))
}

// runStages runs the stage loop with the options a caller built, with the loop
// marked as running while it does: a stage reports its per-repository lines
// through the same entry point a step does, and this is what tells the two
// apart (reportRepos). Every loop this engine drives goes through here,
// `apply`, `project enroll` and the accounts included.
func (e *initEngine) runStages(ctx context.Context, stages []initflow.Stage, o initflow.Options) (*initflow.Result, error) {
	prev := e.inStages
	e.inStages = true
	defer func() { e.inStages = prev }()
	return initflow.Run(ctx, stages, o)
}

// converge runs the converge's default and --firebase-project modes through the
// loop and maps its result to what init returns.
//
// This is the apply phase (W3.5, reconciliation F12): the wizard's questions
// were the input phase, one run of the loop over wizard_stage.go's stage, and
// they are all answered by the time runInit gets here. What this run's order
// decides is what is applied and in what order — the canonical one
// (initflow.Names), not the order the questions were asked in.
func (e *initEngine) converge(ctx context.Context) error {
	// The owner's own local config, read before anything else this run does and
	// before the first stage plans (update_catalog_guard.go): every image
	// catalog entry this release will refuse is named here, and the one entry
	// whose fix is unambiguous — a pin to a branch the repository no longer has
	// whose file the default branch also has — is fixed in the file itself. It
	// touches nothing in the installation's cloud and it can fail nothing, so it
	// is safe with runs in flight and never a step the one command the upgrade
	// is can be stopped at.
	e.guardCatalogEntries(ctx)
	e.announceAlreadySetUp()
	stages := initStages(e)
	e.r.review = &runReview{cover: e.cover, firebase: e.firebaseVerified, owner: e.owner, screen: e.reviewScreen}
	e.gateEarly(ctx)
	// The repository this run is about to onboard joins the local config
	// before the first stage plans, so the installation stage's own apply
	// names its jobs in the credentials service's table (the same run grants
	// them roles/run.invoker on that service: makeOnboardingRepoEntry).
	e.makeOnboardingRepoEntry()
	// The default job profile of every provider this project's repositories
	// use joins the local config here, before the first stage plans, so that
	// the installation stage — the one that turns profiles into project jobs —
	// deploys their jobs in this same update (S5). It is after the onboarding
	// entry above so that the repository this run onboards is one of the
	// providers in use, and before the loop for the reason syncAccess is
	// below it: the config every stage of this run reads is the one this run
	// writes.
	e.createDefaultProfiles(ctx)
	// The managed project's read, once for the whole run: it is what the run's
	// one confirmation and the steps the gate does not judge ask about
	// (update_managed.go's gateRun), and it is made before the first stage so
	// that a run whose gated stages all have nothing to do still has the
	// answer. Its error is the --ask refusal (a managed project's prompts asked
	// for where nobody can answer them): it stops the run here, before the
	// first stage, and it is never a fallback to the verdict.
	if err := e.gateRun(ctx); err != nil {
		return err
	}
	// The roles step, once for the whole run and before the first stage plans
	// (update_access.go, RS3): the installation's member lists and the grants
	// node are reconciled here, and the spec every stage below builds carries
	// the lists the node says. It is before the loop rather than a stage of its
	// own because it changes what the run's OWN applies carry — the
	// installation stage is the first of them, and a step that ran beside it
	// would leave that apply on the lists the file remembered.
	if err := e.syncAccess(ctx); err != nil {
		return err
	}
	// What this release stops, before anything of it is applied (R8): the
	// repository's three lines and the installation's two, printed here so that
	// they are on screen before the run's own confirmation and before any stage
	// writes. It refuses nothing, asks nothing and cannot fail the update.
	e.announceBudgetStops(ctx)
	// And the word that is not a budget mode any more leaves this machine's own
	// config here, in place and said out loud (D3022, F22): the loader lets the
	// file that says it load, so this write is where the word goes, and it is
	// before the first stage for the reason the catalog guard above is — every
	// stage of this run reads the config this run writes.
	e.rewriteLocalBudgetMode()
	// The door's note about the runs that are running while it converges, and
	// the last thing the run prints before its own confirmation and before any
	// stage writes: a person deciding whether it is safe to update now reads it
	// before the update starts rather than after. It refuses nothing, asks
	// nothing, and asks no run to finish first, and it is silent when there is
	// nothing running — the step before this one computes its node and leaves it
	// to the installation root's apply rather than writing it (syncAccess).
	if err := e.announceRunsInFlight(ctx); err != nil {
		return err
	}
	// The record as this door finds it, read before the first stage: what
	// clearAnsweredSteps drops when the run ends are the entries still
	// identical to these, so a step queued while the run was going — which no
	// stage of this run published — stays owed (D1563).
	e.readOwedBefore()
	// The run's own heading, and the section its steps are read under: a first
	// run's header went out with its first question (printRunHeader prints it
	// once either way), and everything the converge says on its own way here —
	// the roles step's warnings, the budget stops, the runs in flight — is
	// above the heading in the block it belongs to, so what a reader sees under
	// it is the steps themselves.
	e.r.printRunHeader(e.lc.Name, "")
	fmt.Fprintln(e.r.w, e.r.qs.Header(convergeSection(e.r.o)))
	res, err := e.runStages(ctx, stages, e.options())
	// The steps this run answered are steps nothing owes any more: `update`
	// runs every stage, and the entries of the stages it ran successfully are
	// cleared here and nowhere else (D1567). It is after the loop because the
	// answer is per stage — a stage that failed or that was blocked behind one
	// leaves its step owed — and it never touches a catalog build, which
	// `update` does not run (D1577).
	e.clearAnsweredSteps(res)
	// The migration records this run may write, and they are written HERE
	// rather than by the stage that planned them: a record claims that a
	// repository's migration has landed, and the layer half of it lands in the
	// layer stage's publish of the document the lift stage edited, which runs
	// after it — so a publish that was refused, skipped or never reached writes
	// no record and the next update migrates the repository again
	// (update_lift.go, D3305).
	e.writeLiftRecords(ctx, res)
	if err == nil && res != nil && e.r.o.planOnly && res.Failed == nil {
		e.planOnlyReview(ctx, res.Stages)
	}
	return e.outcome(ctx, res, err)
}

// convergeSection is the heading the converge's own steps are read under, and
// it follows what the run does: a run that applies says so, and --plan-only,
// which shows what would be applied and applies nothing, does not.
func convergeSection(o *initOptions) string {
	if o.planOnly {
		return "Planning"
	}
	return "Applying"
}

// outcome maps a loop result to init's error, keeping the exit codes the
// engines always had: an engine's own error is returned as it is (its
// ExitError code, else 1), a refusal before anything ran is a user error
// (1), and a run that is unfinished only because the user has to act is 1
// too. Only an engine's coded remote failures are 2. With --json the
// result is printed first, whatever the outcome, so CI gets the account.
//
// The creation fact is recorded here, at the top, and not after the loop
// returned: markCreated only ever warns (an unmarked project is the
// fail-closed direction), and a warning is appended to the result the print
// encodes — so a marker warning recorded after it would reach stderr alone,
// while a --json consumer updating its account read an empty warnings array
// (TestMarkCreatedWarningsAreInThePrintedResult). It is still after the loop's
// own stages, so the project stage has set r.res.Created by the time it runs,
// and it still never changes the outcome.
func (e *initEngine) outcome(ctx context.Context, res *initflow.Result, runErr error) error {
	r, o := e.r, e.r.o
	if res != nil {
		r.res.Stages, r.res.LeftForYou, r.res.Failed, r.res.Note = res.Stages, res.Left, res.Failed, res.Note
	}
	e.markCreated(ctx)
	switch {
	case e.envErr != nil:
		_ = r.printResult()
		return e.envErr // as init always refused: the problem and its fix
	case runErr != nil:
		_ = r.printResult()
		return userErr("%v", runErr)
	case res.Failed != nil:
		// Only this one is the error the account has just printed (its step
		// line, the log's path, the command that resumes the run), so only this
		// one is marked as read. A run that failed and then stopped for
		// something else returns that something else above, and it is the only
		// statement of how to unblock the run — a refusal's "needs a real
		// terminal … or pass --yes" — so it reaches the screen like any other
		// command's error (TestARefusalAfterAFailureStillSaysHowToUnblockIt).
		//
		// A run that only planned is neither: the loop prints its stages as rows
		// and opens no step at all (record answers while it is executing, and
		// only converge sets that), so its failure has no step line, no reason,
		// no log's path and no command that resumes the run anywhere on the
		// screen. Nothing printed it, so main's line is the only place it is
		// said (TestFailureLineIsPrintedOnlyUnderALog/a_failed_plan-only_run).
		_ = r.printResult()
		if res.PlanOnly {
			return res.Cause
		}
		return r.failureShown(res.Cause)
	}
	// D1553's self-flag, and it is here rather than beside markCreated above
	// because of what each of the two marks means: markCreated records what a
	// run created, which is true whatever the run did afterwards, while this
	// one takes the installation's typed confirmation away, and a run that
	// failed above applied nothing to be confirmed. So it is reached only by a
	// run whose stages all succeeded, and its own test drives that path
	// (update_selfflag.go, TestUpdateOnAnUnmanagedInstallation...). It is
	// still before printResult, so what it says and the warnings it records are
	// in the result the print encodes — the reason markCreated is where it is.
	if err := e.selfFlagUnmanaged(ctx); err != nil {
		_ = r.printResult()
		return err
	}
	// And D1548's, on the same path and for the same reason: the converge has
	// applied everything it had, and the mode a managed project's runs read is
	// raised here or on no run at all (update_budget_mode.go). It returns no
	// error — every state that leaves the mode as it is is a warning or
	// silence, because nothing may fail an update the HARD RULE makes the
	// whole upgrade — so it is a statement and not a branch, and it is before
	// printResult for the reason the two above are: what it printed and
	// recorded belongs to this run's result.
	e.raiseBudgetMode(ctx)
	// And the checkout's own file, which is the one door of the two that is a
	// committed file somebody reviews: the removal of the budget.mode line is
	// offered as a diff, written with --yes or by a person at the terminal and
	// printed otherwise, exactly as the anchor's line is (AB8b, F12, D3022). It
	// is here rather than before the loop because the repository stage reads
	// that file and acts only while the working tree holds the default branch's
	// bytes (resolveRepoTarget), so an edit before it would turn this run's own
	// repository step into "not the default branch's file". It returns no error
	// for the reason the raise above returns none.
	e.offerBudgetModeRemoval(ctx)
	// And D1543's: the creation fact a release behind left at version 3 carries
	// the image build count this one no longer bounds a build by, and a fact is
	// migrated where it was written (update_marker_version.go). It is after the
	// self-flag above for the reason that step has: a run which flags an
	// installation writes the version this build writes, so the two together
	// converge any installation the update reaches, and the step itself does
	// nothing where the fact is already this release's. Like the two above it
	// returns no error — a fact left behind is one this build still reads — and
	// it is before printResult for the same reason.
	e.convergeMarkerVersion(ctx)
	// And D1940's: the roster's project-scoped admins, published for the intake
	// that admits a caller to the shared prefix and may hold no database role
	// (update.go's writeAdminList). It is here for the reason above — the
	// converge has applied everything it had, so the roster it reads is the one
	// this run leaves behind — and it returns no error either: the list is a derived
	// object, and failing an upgrade over one is what the HARD RULE forbids.
	e.writeAdminList(ctx)
	// And D3087's: where the installation's located things are, read from the
	// cloud and written down in this machine's own config (LP14 of
	// docs/location-preference/, docs/location-preference/record.md). It is
	// here for the reason the seven above are — the converge has applied
	// everything it had, so what this reads is the installation this run
	// leaves behind — and it is receptive rather than authoritative: nothing
	// reads the block back as an input (R5), so a record that could not be
	// written or read is a warning and never a failed upgrade (the HARD RULE),
	// and it never asks a question. It is the one step here whose writer is
	// both doors — `fugaro setup` creates the installation whose record this
	// is, and `fugaro update` is the command that keeps it true.
	e.recordLocations(ctx)
	// And D2965's, the seventh: the runs that opened, followed or reviewed a
	// pull request before this release wrote an entry as it came to be on one,
	// so `runs ls --pr N` finds them in the index instead of scanning the
	// repository
	// (update_prindex.go). It is here for the reason the six above are — the
	// converge applied everything it had, so the runs it walks are the ones this
	// run leaves behind — and it is beside writeAdminList because it is the
	// other object of this run's that a later read of the installation takes:
	// both are derived from what the installation holds and neither fails an
	// update (the HARD RULE).
	e.backfillPRIndex(ctx)
	// And D2966's: the run summaries the runs before this release have none of,
	// created in place so `runs ls` reads two objects a row instead of three
	// (update_summary.go). It is here for the reason above — the converge
	// applied everything it had, so the runs it walks are the ones this run
	// leaves behind — and it returns no error either: a summary is a derived
	// hint, and failing an upgrade over one is what the HARD RULE forbids.
	e.backfillRunSummary(ctx)
	// And D2716's: the checkout's docs/backlog/ folder, folded into the
	// project's standing backlog once (update_backlog.go). It is here for the
	// reason the four above are — the converge has applied everything it had, so
	// the installation it writes into is the one this run leaves behind — and it
	// returns no error either: the folder is history the moment it is folded, and
	// a checkout whose folder could not be folded is a warning on a run that
	// otherwise converged, never a reason to fail one (the HARD RULE). It is the
	// one step here that reads the checkout this run was started in, and the one
	// that does nothing at all outside one.
	e.foldBacklog(ctx)
	// And RI5's: the run index an installation of an earlier release has never
	// built, marked for a rebuild and started once (update_runindex.go). It is
	// here for the reason the six above are -- the converge has applied
	// everything it had, so the installation this run leaves behind is the one
	// whose index is walked -- and it returns no error either: the index is a
	// derived object, the mark it leaves is what every later sweep continues on
	// its own, and failing an upgrade over one is what the HARD RULE forbids.
	e.reindexRunIndex(ctx)
	// And the owner's rule of 2026-11-24's: every repository this config lists
	// has its deployed jobs checked against what its workflows resolve to now,
	// and moved where they differ (update_jobimages.go). It is here for the
	// reason the seven above are — the converge has applied everything it had,
	// so the map and the catalog it reads are the ones this run leaves behind —
	// and it returns no error either: a repository nobody could read, a job the
	// API refuses to move, is a line and a step the next update takes again,
	// never a failed upgrade (the HARD RULE).
	e.reconcileRepoJobs(ctx)
	// And T-RU5's, the sixth and last: what this release fixes about the
	// project's filed issues, which is a step of the run and not of the door
	// (update_resolved.go) because --json's document carries it and is printed
	// on the next line. It is reached on the same path the five above are —
	// the converge applied everything it had — and it is the one of the six
	// that no mode has to exclude twice: what it writes to the shared store is
	// the store's own guarded write, and what it prints is a line nobody has
	// to act on.
	r.updateResolved(ctx)
	if err := r.printResult(); err != nil {
		return err
	}
	// The run's last line, on the door whose job it is to say it: what filing
	// an issue on GitHub needs (`gh`), and that nothing else about issues does.
	e.announceGhHint()
	// And the other end-of-run line, for the installation set up outside any
	// checkout: how a repository is enrolled, which only a run that converged
	// says (announceEnrolNote). It is here rather than above the steps because
	// a run that fails was asked for no repository, and a line telling its
	// reader to enrol one belongs to no failure.
	e.announceEnrolNote()
	if len(res.Left) > 0 && !o.planOnly {
		return &notFinished{userErr("%s is not finished: %d step(s) left for you, listed above", o.doorProgram(), len(res.Left))}
	}
	return nil
}

// recordLocations is D3087's step: the converge reads where the installation's
// located things are and writes the answer into this machine's own project
// config as the `locations` block (docs/location-preference/record.md).
//
// It is a record and never an input. Every plan asks the cloud itself for the
// thing it is about (R5), so nothing here decides anything: a record that is
// wrong, stale or hand-edited changes no plan, no import and no apply, and a
// member the cloud could not be read for is left absent rather than guessed at.
// The one reader is `fugaro doctor` (LP12), which prints where the data is.
//
// Nothing here may fail the run, for the reason update.go's writeAdminList may
// not: this is a step of the upgrade, and a record that could not be written is
// a record the next `fugaro update` writes. Every state is therefore a warning
// or silence, and which of the two is the state's own answer. The states a
// recording never started in — no local config file of this machine's own (a
// shared selection), no cloud clients at all (a run that never opened the
// installation), no Firebase project, no Firestore client to read with — are
// silence: nothing was read and nothing failed, and a config that cannot reach
// its own project's cloud is the config the record would say nothing about. A
// read that WAS made and came back an error, and a file that refused the write,
// are warnings, because there the record is missing and a person is owed the
// reason. It asks nothing (docs/no-prompt-policy/): a run has already answered
// every step it was going to by the time the converge applies, and a question
// here would be asked at the one moment nobody is looking.
func (e *initEngine) recordLocations(ctx context.Context) {
	r, o := e.r, e.r.o
	// A run that planned wrote nothing at all, and one whose stages left
	// something for the person has not converged the installation: the record
	// is written by the run that applied everything (as writeAdminList's left
	// guard says), and the leftovers are named above so the next run is the one
	// that writes it.
	if o.planOnly || len(r.res.LeftForYou) > 0 {
		return
	}
	// The file this machine's own config is in, as the converge wrote it
	// (writeLocalConfig) — the path of the file the write below reads again,
	// and not the converge's copy of what is in it. A shared selection has
	// none, and an installation reached through one has no record of its own
	// to keep.
	path := r.res.Config
	if path == "" || e.lc == nil || e.c == nil {
		return
	}
	fp := e.firebaseProject()
	if fp == "" {
		return
	}
	// The Firestore client is the CLI's own REST client rather than one of
	// Clients' (internal/infra's ObserveInputs), so it is built the way the
	// Firebase stage builds it — one construction, so a fake endpoint and a
	// real credential resolve here exactly as they do there.
	//
	// Not building it is silence and not a warning, for the reason the guard
	// above is: this step's whole input is a read of the installation's cloud,
	// and a client Fugaro cannot build at all is a config with no Firestore to
	// read (a machine whose config carries no endpoint for it, or no
	// credential) — a run with nothing to say about where anything is rather
	// than a run whose recording failed. A converge that could not reach
	// Google's APIs fails long before this step, and doctor's own row says this
	// machine has no record yet, which is what is true of it.
	fs, err := r.firestoreStep(ctx, e.lc, fp)
	if err != nil {
		return
	}
	obs, err := infra.ObserveLocations(ctx, infra.ObserveInputs{Clients: e.c, Firestore: fs.FS, Spec: e.spec, FirebaseProject: fp})
	if err != nil {
		r.warn(fmt.Sprintf("the locations record was not written: the installation's located things could not be read (%s), so the config keeps the record it has", safetext.FieldErr(err)))
		return
	}
	record := recordedLocations(obs)
	changed, err := r.writeRecordedLocations(record, path)
	if err != nil {
		r.warn(fmt.Sprintf("the locations record was not written: %s", safetext.FieldErr(err)))
		return
	}
	// The line is the reason the file a person may open afterwards changed, so
	// it is said exactly then: a converge that read the same installation again
	// writes the same record, and repeating it every update would be a line
	// about nothing happening. It goes to the run's own writer, as the admin
	// list's line beside it does: the steps of the converge have all ended by
	// here, so a stage's detail writer is no longer a place a person reads.
	if changed {
		if line := recordLine(record); line != "" {
			fmt.Fprintf(r.w, "%s\n", line)
		}
	}
}

// writeRecordedLocations writes the record the converge just read off the cloud
// into this machine's config, through the one writer every local-config write
// goes through, and only when it changes the file. It says whether it did, which
// is what the caller's line about the record is owed to.
//
// The document is built from the file as it is at this moment and not from the
// engine's own copy of the config, for the reason init_compute.go's batch block
// is (the same read, the same parse, the same one member set): the file is
// written by more than one engine of a run, and the other writers write from
// copies that never reach this one. The repository stage resolves a checkout's
// entry and writes it from the config IT read (init.go's repoEngine,
// loadRepoConfig and writeRepoConfig — base_branch, the checkout's workflows,
// the flags an earlier run of the entry's own), and refreshLocalOptIn writes
// the batch block from a read of its own. initEngine's e.lc was loaded before
// any of them, so a record written from it would put the whole entry back to
// what the run found — silently undoing the repository stage's own write in the
// same command. Reading here is what makes the step a writer of one member.
//
// The unchanged check is the writer's own, made here so that the ordinary case
// is silent: an installation whose record this run rewrote with what it read is
// an installation whose file does not change, and a converge that printed a
// diff of the file twice for nothing would be noise on every update. It is the
// same comparison writeLocalConfig makes, for the same reason.
func (r *initRun) writeRecordedLocations(record *localcfg.Locations, path string) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	cur, err := localcfg.Parse(old)
	if err != nil {
		return false, err
	}
	next := *cur
	next.Locations = record
	data, err := next.Marshal()
	if err != nil {
		return false, err
	}
	if bytes.Equal(old, data) {
		return false, nil
	}
	if err := r.writeLocalConfig(&next, path, old, true); err != nil {
		return false, err
	}
	return true, nil
}

// firebaseProject is the Firebase project the installation's Firestore database
// and default Realtime Database live in: the one the config records, else the
// one this run was pointed at (--firebase-project), else the installation's own
// GCP project, which is the pair setup makes one project (D7). It is empty only
// for a config that names no project at all, and a run with no Firebase project
// is a run with no located thing to read.
func (e *initEngine) firebaseProject() string {
	if e.lc == nil {
		return ""
	}
	var recorded string
	if e.lc.Budget != nil {
		recorded = e.lc.Budget.FirebaseProject
	}
	return cmp.Or(recorded, e.r.o.firebaseProject(), e.lc.GCPProject)
}

// recordedLocations is one observation as the record's block. Every member the
// cloud answered for is written, and a thing that does not exist, a thing the
// read could not be made for and a thing this installation has none of all come
// out the same way: absent, which is what the block says for "not known yet".
func recordedLocations(obs infra.Observed) *localcfg.Locations {
	l := &localcfg.Locations{
		Firestore: obs.Firestore.Location,
		RTDB:      obs.Database.Location,
		LogBucket: obs.LogBucket.Location,
	}
	// The three answers the observation gives for a set of containers are the
	// two words the block spells and a list of regions (localcfg
	// .SecretsReplication, whose writer and reader hold the same three).
	switch obs.Secrets.Replication {
	case infra.ReplicationAutomatic:
		l.Secrets.Kind = localcfg.ReplicationAutomatic
	case infra.ReplicationMixed:
		l.Secrets.Kind = localcfg.ReplicationMixed
	default:
		l.Secrets.Regions = obs.Secrets.Regions
	}
	if l.IsZero() {
		// Nothing was read: the record says nothing, which is the same
		// statement the block absent from the file makes. A nil block is what
		// omitempty drops, so an installation this run could read nothing of
		// gets the file it had rather than a written `locations: {}`.
		return nil
	}
	return l
}

// recordLine is the record as the one line the run says it wrote, or "" when
// there is no record. A person who sees their config change at the end of an
// update is owed the sentence that says why.
func recordLine(l *localcfg.Locations) string {
	if l.IsZero() {
		return ""
	}
	var parts []string
	for _, m := range []struct{ name, at string }{
		{"firestore", l.Firestore}, {"rtdb", l.RTDB},
		{"secrets", l.Secrets.String()}, {"log bucket", l.LogBucket},
	} {
		if m.at != "" {
			parts = append(parts, m.name+" "+m.at)
		}
	}
	return "recorded where the installation's data is: " + strings.Join(parts, ", ")
}

// notFinished is a run's own end with steps left for the user: it applied
// everything it had, the steps that remain are the person's, and that is exit
// 1 and not a failure. It is a type of its own rather than a plain userErr
// because the event feed tells the two apart with it (setup_events.go): a run
// with steps left ends its feed `done` with exit 1 — the hand-off a wizard
// draws — where a refusal ends it `error`, and this error and the refusals
// above it are otherwise the same error with the same code.
type notFinished struct{ error }

func (e *notFinished) Unwrap() error { return e.error }

// planned is the one-line summary of the installation plan the engine
// just showed, if it showed one.
func (e *initEngine) planned(c *infra.PlanCounts) initflow.Plan {
	if c != nil && *c != (infra.PlanCounts{}) {
		return initflow.Plan{Detail: c.String()}
	}
	if c != nil {
		return initflow.Plan{NothingToDo: true, Detail: "No changes"}
	}
	return initflow.Plan{}
}

// preflightStage is the environment check (stage 0): read-only, nothing to
// apply, and a failing check stops the loop with the check's own message,
// problem and fix in one line.
type preflightStage struct{ e *initEngine }

func (preflightStage) Name() string { return initflow.Preflight }
func (s preflightStage) Check(context.Context) (initflow.Status, error) {
	if err := s.e.environment(); err != nil {
		lf := initflow.Left{Stage: initflow.Preflight, Kind: initflow.LeftConsole, Text: err.Error()}
		return initflow.Status{State: initflow.NeedsYou, Detail: "the environment is not ready", Left: &lf}, nil
	}
	return initflow.Status{State: initflow.Done}, nil
}
func (preflightStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	return initflow.Plan{NothingToDo: true}, nil
}
func (preflightStage) Apply(context.Context, initflow.Env) (initflow.Outcome, error) {
	return initflow.Outcome{}, errors.New("the environment check changes nothing")
}
func (preflightStage) Verify(context.Context) error { return nil }
func (s preflightStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Preflight, Kind: initflow.LeftConsole, Text: "fix the environment problem named above, then rerun", Commands: []string{selfCommand() + " " + s.e.r.o.doorName()}}
}

// engineStage is a stage that calls one of init's engines. The engines read
// the run's options (r.o.yes, r.o.planOnly) directly, so the adapter, not
// the engine, is where the loop's decision is enforced: during Plan and
// Apply r.o.yes is what the loop said (env.Yes: never true for a stage
// --yes does not cover, whatever the flag says) and Plan forces the
// engine's own plan-only mode.
//
// It cannot know without planning whether anything is missing, so its
// check says it plans when applied, and an apply that finds nothing is
// reported "No changes". Verify has nothing more to read: the engines
// apply the plan they showed and read the outputs back, and a failure
// there is the apply's own error.
type engineStage struct {
	e    *initEngine
	name string
	// skip is why the stage does not apply to this run ("" when it does).
	skip func() string
	// check, when set, is the stage's Check instead of skip, for a reply that
	// costs a read and so cannot be given without one: the Firebase stage's,
	// whose skip holds only while the Firebase root's state carries the run
	// index's composite indexes (RI2). It answers the same two states skip can
	// answer (skipped, will-do) and never an error for a read that did not
	// answer — a plain update must not fail over one (firebaseSkip).
	check func(ctx context.Context) (initflow.Status, error)
	run   func(ctx context.Context) error
	left  initflow.Left
	// leftFor, when set, is the stage's left-for-you as a function of the run's
	// own arm instead of one fixed line: the installation-2 stage has two, and
	// the command they name differs (--firebase-project's history job, the
	// catalog check job a plain update deploys), so one of them would send the
	// user to a command that does not do what the prompt says.
	leftFor func() initflow.Left
	// isolate makes the stage judge "No changes" and its plan line by what
	// it did itself, not by what the stages before it left in the run's
	// result (the second installation apply, after the Firebase root's).
	// The run's result still accumulates both.
	isolate bool
	// did, when set, is what the engine did besides applying Terraform (the
	// installation stage's new-machine run writes the local config and applies
	// nothing): a non-empty line makes the stage "changed".
	did     func() string
	applied bool
	changes *infra.PlanCounts
	// declined, when set and non-empty, is the one line a stage that did
	// nothing this run says instead of "No changes": why the step is left as it
	// is. It is the Firebase stage's, whose no-flag arm may reach its steps and
	// find that this run may not take them (R2's project owner, billing), and
	// it is a detail on a stage that is DONE: the rest of the run carries on,
	// which is the whole point of the line. A stage with something only the
	// user can give leaves it as a *initflow.NeedsYouError instead, and one
	// that changed something says it with did.
	declined func() string
}

func (s *engineStage) Name() string                 { return s.name }
func (s *engineStage) SelfConfirming()              {}
func (s *engineStage) Verify(context.Context) error { return nil }
func (s *engineStage) Left() initflow.Left {
	if s.leftFor != nil {
		return s.leftFor()
	}
	return s.left
}

func (s *engineStage) Check(ctx context.Context) (initflow.Status, error) {
	if s.check != nil {
		return s.check(ctx)
	}
	if why := s.skip(); why != "" {
		return initflow.Status{State: initflow.Skipped, Detail: why}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
}

// with runs the engine with the options the loop's env allows, restoring
// them after.
func (s *engineStage) with(ctx context.Context, env initflow.Env, planOnly bool) error {
	o := s.e.r.o
	yes, plan := o.yes, o.planOnly
	defer func() { o.yes, o.planOnly = yes, plan }()
	o.yes = env.Yes
	o.planOnly = o.planOnly || planOnly
	res := &s.e.r.res
	if !s.isolate {
		err := s.run(ctx)
		s.applied, s.changes = res.Applied, res.Changes
		return err
	}
	prevApplied, prevChanges := res.Applied, res.Changes
	res.Applied, res.Changes = false, nil
	err := s.run(ctx)
	s.applied, s.changes = res.Applied, res.Changes
	res.Applied = res.Applied || prevApplied
	if res.Changes == nil {
		res.Changes = prevChanges
	}
	return err
}

func (s *engineStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	if err := s.with(ctx, env, true); err != nil {
		return initflow.Plan{}, err
	}
	return s.e.planned(s.changes), nil
}

func (s *engineStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if err := s.e.newMachineGuard(s.name); err != nil {
		return initflow.Outcome{}, err
	}
	if err := s.with(ctx, env, false); err != nil {
		return initflow.Outcome{}, err
	}
	switch {
	// A declined stage applied nothing at all — the line is set on the two arms
	// that return before the first apply — so it is read FIRST: the run's own
	// result accumulates what the stages BEFORE this one did (a `setup` from
	// nothing applies the installation root before the Firebase stage is
	// reached), and a declined line that waited for those counters to be empty
	// would never be the one the stage says.
	case s.declined != nil && s.declined() != "":
		return initflow.Outcome{Detail: s.declined()}, nil
	case !s.applied && s.did != nil && s.did() != "":
		return initflow.Outcome{Changed: true, Detail: s.did()}, nil
	case !s.applied:
		return initflow.Outcome{Detail: "No changes"}, nil
	case s.changes != nil:
		return initflow.Outcome{Changed: true, Detail: "applied: " + s.changes.String()}, nil
	}
	return initflow.Outcome{Changed: true, Detail: "applied"}, nil
}

// ensureCredentialsService writes the installation's credentials service into
// the installation root's tfvars, before the stage that plans that root. It is
// the one writer of that variable's EXISTENCE: `fugaro update` deploys the
// service on every installation, with no flag and no typed confirmation of its
// own (the owner's ruling of 2026-11-05, docs/credentials-service/default-on.md),
// so the block is part of the state the installation stage's own confirmed
// apply converges and not an opt-in. The image inside an existing block is not
// this function's: moving it onto this release's is the credentials-service
// stage's own step (moveCredentialsImage), which runs AFTER the policy stage,
// because the service reads the published node per call and refuses a node its
// own binary does not understand (the owner's ruling of 2026-11-10;
// initflow.CredentialsService has the order).
//
// Two jobs meet here, and which one runs is what the tfvars already carry:
//
//   - An installation whose tfvars carry NO block gets one, built from
//     infra.Credentials: the service's account and bundle container, the
//     installation's own database, and the job accounts that may call it. The
//     image is this release's, resolved and pinned by digest — the same
//     resolve, so the very first apply that creates the service already runs
//     the released image. This half has to run where it does and no later: the
//     block is what the repository stage reads to decide whether a job mounts a
//     model credential at all, so an installation that has none gets one before
//     that stage, and the stage that would otherwise deploy it runs after it.
//   - An installation that already HAS one keeps it, byte for byte, with its
//     job tables brought up to date with this machine's config. The block is
//     the record of a service that exists — its URL is the audience every
//     caller's ID token is minted for, and nothing on this side knows it — so
//     it is carried as it is found (init.go's prepare,
//     CarriedCredentialsService) and the apply that follows deploys exactly
//     what the earlier release wrote, image and all. The image's move is the
//     later stage's, and that is what makes an ordinary `fugaro update` the
//     whole upgrade: without it the recorded reference keeps naming the release
//     the service was created with, and the module's own decisions that read
//     the release out of it — credentials.tf's credentials_image_release, which
//     names the Cloud Run container port h2c at and above the release that
//     serves HTTP/2 — stay where they were.
//
// Three things it deliberately does not do:
//
//   - It runs nothing in a coding agent's session. The engine stages are
//     PLANNED there rather than skipped, so a plan-only run would reach cosign
//     and the registry with the operator's ADC and write the block into the
//     workdir's tfvars. The service is the identity that holds every model
//     credential the installation has, so the deploy is the owner's own act:
//     the owner's `fugaro update` writes the block, and an agent's session
//     leaves the installation exactly as it found it.
//   - It makes no database call. The verifier list the block carries is
//     prepare's own member, written from the effective list a few steps later
//     (effectiveVerifiers, which is the tolerant read this writer must not
//     duplicate), so nothing here has to reach the budget backend.
//   - It does not fail a run that cannot deploy the service. An installation
//     with no Firebase backend, or with no repository job for the service to
//     admit, cannot carry it at all (infra.Credentials), and a registry or
//     cosign this run cannot reach is not a reason to stop the rest of the
//     upgrade: the deploy is what is missing, nothing is left stale, and the
//     next run tries again.
func (e *initEngine) ensureCredentialsService(ctx context.Context) error {
	if m := agentMarker(os.Getenv, e.r.stdinTerminal()); m != "" {
		return nil
	}
	if releaseVersion() == "" {
		// A development build resolves no released image (resolveCredentialsImage
		// refuses it), so there is nothing here for it to deploy: the recorded
		// block is left as it stands rather than made an error of.
		return nil
	}
	doc, _, err := e.recordedCredentialsImage()
	if err != nil {
		return err
	}
	if doc == nil { // no credentials_service in the installation's tfvars: this run deploys it
		return e.deployCredentialsService(ctx)
	}
	// The installation's record says the service is deployed, and this run's spec
	// has to say the same: the block in this document is what the run that
	// deployed the service left behind, and the config write that records it in
	// the local config is the last step of that run.
	e.specReviewJobsOfTheService()
	_, err = e.writeCarriedCredentials(ctx, doc)
	return err
}

// writeCarriedCredentials writes the carried block back into the installation
// root's tfvars, with the members this side composes out of the local config —
// the two job tables, and the identity half's three names — brought up to date,
// and returns the document it wrote.
//
// The document goes back on disk BEFORE anything that can fail and return.
// prepare (init.go) is what rewrites the installation's tfvars from this run's
// spec and carries the block forward, and it is steps away: PrepareWorkdir has
// already deleted the file, so a step in between that returned its error — the
// carries below reach for cosign, for the registry and for this machine's
// config, and either of them can fail — would leave an installation whose tfvars
// hold no block at all. The next run reads that as "no service yet", composes a
// fresh one from the local config alone, and applies it: the block's URL — the
// audience every caller's ID token is minted for, which only the tfvars and the
// module's outputs carry — is gone from the desired state, so the revision is
// left admitting nobody until the audience fill puts it back, and any member the
// deployed block carried beyond what this machine's config states is lost with
// it. Putting the document the carry is about to use back closes the window
// PrepareWorkdir opened, and costs one write of a file prepare rewrites a few
// steps later.
//
// A run that fails before that rewrite leaves the file on disk, and that is the
// point rather than a leak to clean up: nothing removes a workdir on the error
// path (the next run's PrepareWorkdir is what replaces the tree), and every
// reader of this file reads it AS the earlier run's document — prepare and
// --print-vars carry forward from it the keys this run's spec never holds, and
// installationFenceRecorded reads the managed deployer's fence out of it. What
// is written is those same bytes, the ones recordedCredentialsImage just read
// (Workdir.PrevVars in an ordinary update), so the file and the snapshot agree;
// deleting it here would put the block back out of reach and repeat the failure
// above.
//
// The refresh is not cosmetic: the deploy is the job tables' only other writer,
// and a repository onboarded after it would hold an invoker grant on a service
// whose table refuses its jobs (infra.RefreshCredentialsServiceJobAccounts).
// The repository this run onboards is in the config before the first stage
// plans (makeOnboardingRepoEntry), so the table names its jobs in the run that
// grants them the invoker: a row only a later run would add leaves this run's
// own repository refused by the service it was just switched onto. A refusal
// there is not a failure of the run: the block is written exactly as the
// workdir held it, which is the fail-closed direction — an installation whose
// config cannot describe the table keeps the table it has — and the same shape
// the deploy path answers a config that cannot carry the service with.
func (e *initEngine) writeCarriedCredentials(ctx context.Context, doc []byte) ([]byte, error) {
	next := doc
	if e.lc != nil {
		refreshed, err := infra.RefreshCredentialsServiceJobAccounts(doc, e.lc)
		if err != nil {
			fmt.Fprintf(e.r.w, "the credentials service's job table was not refreshed: %v\n", err)
		} else {
			next = refreshed
		}
		// And the identity half's three names, which no release before this one
		// wrote: the deploy is their only other writer, and every installation
		// that has the block carries one without them — byte for byte, so
		// nothing else ever adds them. Without this refresh the roster's
		// service accounts and the implicit admins never reach the service at
		// exactly the installations that have it, and the runs' signer never
		// reaches it either, which is the name LM14's probe reads to decide
		// whether the minter grant can move. Refreshed from `next` rather than
		// from the document the job-table refresh was handed, so a refusal
		// there does not also cost this one.
		if named, err := infra.RefreshCredentialsServiceIdentity(next, e.lc); err != nil {
			fmt.Fprintf(e.r.w, "the credentials service's identity names were not refreshed: %v\n", err)
		} else {
			next = named
		}
	}
	// And the intake, which the same apply deploys and which no earlier release
	// wrote: an installation whose tfvars carry the service's block and no
	// intake's is every installation this release upgrades, and the two are
	// deployed by one run because they are one pin of one image — the intake is
	// a subcommand of it (IntakeSpec). It is added here, where the block the
	// run is about to apply is read, and never in an agent's session or a
	// development build: this function's callers have already refused those
	// (ensureCredentialsService, moveCredentialsImage).
	//
	// A document this run cannot read or write leaves the entry where it is,
	// which is the fail-closed direction and never a reason to stop the upgrade
	// (the job-table refresh above answers the same way): an installation whose
	// intake is not written keeps the one it has, and an installation that has
	// none is up to date about everything else.
	if withIntake, err := e.carriedIntake(ctx, next); err != nil {
		fmt.Fprintf(e.r.w, "the secrets intake is not deployed: %v\n", err)
	} else {
		next = withIntake
	}
	// And the voice hop is deliberately NOT added here, which is the whole of
	// its opt-in: the hop is a PUBLIC endpoint (voice.tf's allUsers binding), so
	// an installation that never wrote var.voice_service gets none of it — no
	// service, no account and no invoker binding — and nothing about the hop is
	// planned or deployed. The block is the opt-in itself, and prepare's carry
	// (init.go's CarriedVoiceService) is what keeps an installation that wrote
	// one, which is where the hop's opt-in differs from the
	// intake's above, the intake being a block this run deploys and the hop a
	// block only the installation's own edit turns on.
	if err := e.wd.WriteVars(next); err != nil {
		return nil, userErr("writing the installation's %s: %v", infra.VarsFile, err)
	}
	return next, nil
}

// carriedIntake is doc with the secrets intake's block added when it holds
// none, carrying the image the apply that follows will create the service from:
// this release's pin, when the recorded one is a release this binary is past.
//
// Why the recorded image is not good enough here, and why the pin cannot wait
// for the credentials-service stage a few steps later. This block is written
// before the apply that CREATES the intake's service, and the image in it is
// the binary that service runs: an installation that skipped the release the
// intake shipped in has a recorded image whose binary has no `intake serve`
// subcommand (intake.tf's own args), so the revision the apply creates exits on
// start and the apply fails — with the block already on disk, which is what
// every rerun then repeats. The credentials service's own process is a
// different question and keeps the recorded image until that stage: the node it
// reads per call may not be raised before the process that reads it is replaced
// (the owner's ruling of 2026-11-10), and the intake reads no such node.
//
// The resolve is the run's one (credentialsResolve, memoized): the policy
// stage's precondition and the credentials-service stage's move read this same
// answer, so pinning here costs no second registry read. A resolve that fails
// returns with the block unwritten — an installation keeps no intake rather
// than one whose revision cannot come up — and the failure is still the run's
// where it belongs: the policy stage's precondition refuses a stale recorded
// image before the node it reads moves, and a run whose node is already at this
// version meets the same memoized error at the credentials-service stage, whose
// move returns a resolve it cannot make.
//
// A document that carries no image for the service gets no intake: without one
// there is nothing for the module's container to run, and the block would be a
// revision that never comes up. A run with no release of its own — a
// development build, which this function's callers have already refused a
// deployment for — keeps the recorded image rather than resolving one.
func (e *initEngine) carriedIntake(ctx context.Context, doc []byte) ([]byte, error) {
	if _, have, err := infra.IntakeOf(doc); err != nil || have {
		return doc, err
	}
	image, ok, err := infra.CredentialsServiceImageOf(doc)
	if err != nil || !ok {
		return doc, err
	}
	if ver := releaseVersion(); ver != "" && credentialsImageStale(ver, image) {
		pinned, err := e.credentialsResolve(ctx)
		if err != nil {
			return doc, err
		}
		image = pinned
	}
	return infra.SetIntake(doc, infra.Intake(image))
}

// The two reasons a run deploys no credentials service at all: a coding agent's
// session deploys nothing (the service is the identity that holds every model
// credential the installation has, and the deploy is the owner's own act), and
// a development build resolves no released image to deploy. They are constants
// because two readers say them: the credentials-service stage, which skips on
// them, and the policy stage, which holds the published node's version with
// them (credentialsMoveHeld) — the sentence the node is held with and the
// sentence the stage skips with are one fact about the run.
const (
	credentialsSessionHeld  = "a coding agent's session deploys nothing"
	credentialsDevBuildHeld = "a development build resolves no released image"
)

// credentialsMoveHeld is why a run may not write a node whose version rises:
// the installation's tfvars carry a credentials service, and this run is one
// that replaces nothing — a coding agent's session, or a development build (the
// two constants above).
//
// The service reads the published node on every cascade and Bitbucket-proxied
// call and refuses a node its own binary does not understand, so a node that
// moves while the process reading it stays where it is refuses every such call
// until a later update succeeds. The migration is held instead, and with it the
// whole write of the policy stage — a node written without the version it is
// behind is the same node the config half would rewrite on the next run, so the
// two halves of that write stay one — and the owner's own `fugaro update` does
// the pair together: its policy stage migrates the node, and the
// credentials-service stage immediately after it replaces the service.
//
// Nothing else holds it. An installation whose tfvars carry no block runs no
// service to refuse the node at all, and the other two runs answer "" here:
// the one whose recorded image is already this release's (the apply before the
// policy stage converges that service, and there is no stale process left) and
// the one that is about to move it (credentialsResolveBeforeRaise).
func (e *initEngine) credentialsMoveHeld() string {
	var why string
	switch {
	case agentMarker(os.Getenv, e.r.stdinTerminal()) != "":
		why = credentialsSessionHeld
	case releaseVersion() == "":
		why = credentialsDevBuildHeld
	default:
		return ""
	}
	doc, _, err := e.recordedCredentialsImage()
	if err != nil || doc == nil {
		return ""
	}
	return why
}

// credentialsResolveBeforeRaise resolves the image this run's own move of the
// credentials service will pin, and it is the policy stage's precondition for a
// write whose version rises. It is where the resolve's failure has to land: an
// image this run cannot reach is a move that will not happen, and after the
// write the same failure leaves a node at this version with the release
// behind's service deployed — a service that refuses every cascade and
// Bitbucket-proxied call until a later update succeeds (the owner's ruling of
// 2026-11-10; credentialsMoveHeld is the rule's other half).
//
// A tfvars this run cannot read is no move to resolve: the failure of that read
// is the run's at the stage that applies the file (the installation root, whose
// engines run before this one), and this precondition has nothing to answer
// about a document it cannot read.
//
// It asks servicesImageStale and not the credentials service's own image,
// because the move it is a precondition for is the four services' (the hop's
// and the resident orchestrator's are the two blocks a hand-written or
// once-written image is left behind in): a node this stage raises while a hop or
// a daemon on the release behind stays where it is is the same refusal one
// service over, and the resolve has to fail here rather than after the write.
func (e *initEngine) credentialsResolveBeforeRaise(ctx context.Context) error {
	ver := releaseVersion()
	if ver == "" {
		return nil
	}
	doc, _, err := e.recordedCredentialsImage()
	if err != nil || doc == nil {
		return nil
	}
	stale, err := servicesImageStale(ver, doc)
	if err != nil || !stale {
		return nil
	}
	_, err = e.credentialsResolve(ctx)
	return err
}

// credentialsResolve is the run's one resolve of the credentials service's
// image, memoized: the policy stage asks for it before it writes a node whose
// version moves, and the credentials-service stage pins the answer after that
// write. The failure of a resolve has to land before the node moves — a pinned
// image this run cannot reach is a move that will not happen, and the state it
// may not leave is a node at this version with the release behind's service
// deployed — and one answer is what keeps the two readers of it from spending
// the operator's ADC on the same read twice.
func (e *initEngine) credentialsResolve(ctx context.Context) (string, error) {
	if !e.credentialsRead {
		e.credentialsRead = true
		e.pinnedCredentials, e.credentialsErr = resolveCredentialsImage(ctx, e)
	}
	return e.pinnedCredentials, e.credentialsErr
}

// moveCredentialsImage moves the installation's recorded credentials image
// forward onto this release's, in the installation root's tfvars, and the
// intake's, the hop's and the resident orchestrator's beside it — the four are
// one pin of one image, and what this step has to do is read over all four
// (servicesImageStale), so an installation whose credentials image is already
// this release's is still a move when the hop's block, the one the run never
// writes, or the orchestrator's, the one the run writes once, names an older
// release. It is the credentials-service stage's step (newCredentialsStage):
// it runs after the policy stage, so the node every call of the service is read
// under is this release's before the process that reads it is replaced (the
// owner's ruling of 2026-11-10; initflow.CredentialsService has the order and
// the window it leaves). The resolve behind it is the policy stage's own read
// (credentialsResolve): the write is this stage's, and what it would fail on is
// already answered by the time it runs.
//
// It pins through resolveCredentialsImage: the release's signed "-pinned"
// image, its signature verified and its digest read back through the
// installation's remote repository. A digest nothing verified is never written.
//
// Unlike the deploy, a resolve it cannot make is returned rather than tolerated:
// an installation that already HAS the block has something to report as moved,
// and an update that cannot reach the registry cannot report it. (A run that
// does not apply — a plan-only run, or one whose confirmation the owner
// declines — leaves the tfvars naming this release's image while the deployed
// service is still the previous one. That is prepare's own plan-time write,
// unchanged by this: the tfvars ARE the desired state, and the next run that
// applies converges the service on them.)
func (e *initEngine) moveCredentialsImage(ctx context.Context) error {
	if m := agentMarker(os.Getenv, e.r.stdinTerminal()); m != "" {
		return nil
	}
	ver := releaseVersion()
	if ver == "" {
		return nil
	}
	doc, _, err := e.recordedCredentialsImage()
	if err != nil {
		return err
	}
	if doc == nil { // no block: nothing to move (the stage's own skip says why)
		return nil
	}
	next, err := e.writeCarriedCredentials(ctx, doc)
	if err != nil {
		return err
	}
	// The four images decide, not the credentials service's alone: an
	// installation whose credentials image is already this release's and whose
	// hop's or orchestrator's is not is a move with work to do
	// (servicesImageStale says why).
	stale, err := servicesImageStale(ver, next)
	if err != nil {
		return userErr("reading the installation's %s: %v", infra.VarsFile, err)
	}
	if !stale {
		return nil
	}
	pinned, err := e.credentialsResolve(ctx)
	if err != nil {
		return err
	}
	updated, err := infra.SetCredentialsServiceImage(next, pinned)
	if err != nil {
		return userErr("setting the credentials service's image in the installation's %s: %v", infra.VarsFile, err)
	}
	// The intake moves with the service it shares the pin with, in one write:
	// both run the released image, and a run that replaced the service's
	// process alone would leave the intake serving the release behind's binary
	// — a binary whose own reader is what decides whether this run's published
	// node is one it can serve at all.
	if _, haveIntake, err := infra.IntakeOf(updated); err != nil {
		return userErr("reading the installation's %s: %v", infra.VarsFile, err)
	} else if haveIntake {
		if updated, err = infra.SetIntakeImage(updated, pinned); err != nil {
			return userErr("setting the secrets intake's image in the installation's %s: %v", infra.VarsFile, err)
		}
	}
	// And the voice hop, in the same write and for the intake's reason: it is
	// a subcommand of that pin, and it is the one reader of the
	// published voice policy (internal/voicesvc/policy.go, at
	// config/policy/voice) — a node the policy stage has just raised to this
	// release's version. A revision left on the release behind's binary reads a
	// node its reader may not understand, and internal/voicesvc refuses a node
	// it cannot decode, so what that leaves is an installation whose every
	// device call is refused — the owner's own admit-list not honored — until a
	// later update succeeds. The hop holds no address and no member to correct,
	// so its image is the whole of its move.
	spec, haveVoice, err := infra.VoiceServiceOf(updated)
	if err != nil {
		return userErr("reading the installation's %s: %v", infra.VarsFile, err)
	}
	if haveVoice {
		spec.Image = pinned
		if updated, err = infra.SetVoiceService(updated, spec); err != nil {
			return userErr("setting the voice hop's image in the installation's %s: %v", infra.VarsFile, err)
		}
	}
	// And the resident orchestrator, in the same write: `fugaro orchestrator
	// serve` is the fourth subcommand of that one pin, and the service the
	// installation root deploys runs it. A revision left on the release
	// behind's binary is a daemon that reads the mailbox and the installation's
	// nodes with a process this run's own state may have moved past — the same
	// lag the hop's own move is for, one service over. Nothing else in the
	// block moves: the account, its five bindings and the two names are the
	// installation's state, and an image is the whole of what a release
	// changes about them.
	if _, haveOrchestrator, err := infra.OrchestratorOf(updated); err != nil {
		return userErr("reading the installation's %s: %v", infra.VarsFile, err)
	} else if haveOrchestrator {
		if updated, err = infra.SetOrchestratorImage(updated, pinned); err != nil {
			return userErr("setting the resident orchestrator's image in the installation's %s: %v", infra.VarsFile, err)
		}
	}
	if err := e.wd.WriteVars(updated); err != nil {
		return userErr("writing the installation's %s: %v", infra.VarsFile, err)
	}
	return nil
}

// credentialsImageStale reports whether a recorded credentials-service
// reference is one this release's move replaces, ver being the release this
// binary is.
//
// A reference that names NO release is replaced rather than compared: it is an
// operator's hand-written digest, and there is no release in it to say the
// installation is ahead of this binary. The check is written out because the
// comparison alone answers the wrong way for it — newer splits both sides on
// "." and atoi's each part, so an empty operand reads as one zero part and
// compares equal to a release whose major is 0, which is every release this
// program has cut so far.
//
// The other direction is the rule the base image's own pin follows (newer,
// init_images.go): a CLI older than the release the recorded image names — two
// machines of one installation upgraded at different times — finds the recorded
// release newer than its own and leaves it alone. Re-pinning DOWN would put the
// previous release's process back, and those two machines would flip the
// service on every update.
//
// It is one function because two callers ask it: the move itself, and the
// credentials-service stage's own skip, which may not answer "nothing to do"
// about a block the move would rewrite. And it reads a reference of any of the
// four services this run writes, not only the credentials service's:
// servicesImageStale asks it about the intake's, the hop's and the resident
// orchestrator's images too, the four being one reference shape of one image.
func credentialsImageStale(ver, image string) bool {
	recorded := infra.CredentialsServiceImageRelease(image)
	return recorded == "" || newer(ver, recorded)
}

// servicesImageStale reports whether the installation's tfvars carry an image
// this release's move replaces: the credentials service's, the intake's, the
// voice hop's or the resident orchestrator's — the four services of one pin,
// which moveCredentialsImage re-pins in one write.
//
// It is wider than credentialsImageStale on purpose, and the two blocks the run
// does not write on every apply are why. The hop's block is the installation's
// own opt-in, hand-written into the installation root's tfvars, and the
// orchestrator's is the one the writer writes once (update_orchestrator.go's ensureOrchestratorBlock): either may name the
// release that wrote it while the
// credentials service's own image is already this release's. The hop's revision
// would then run a binary that may have no `voice serve` at all and whose reader
// is a release behind the policy node this run's policy stage has just raised —
// which internal/voicesvc refuses, so every device call of that installation is
// refused — and the daemon's is the same lag on the service that holds signJwt:
// every later update would answer "the recorded image is already this release's"
// and leave it there. The intake is read for the same reason one step weaker:
// the run writes its block, but a document whose intake image was hand-edited or
// left by an interrupted run has the same shape.
//
// The comparison is credentialsImageStale's, image by image: a reference that
// names NO release is replaced, and one that names a release NEWER than this
// binary is left alone, so two machines of one installation upgraded at
// different times do not flip the three services between their two releases on
// every update.
func servicesImageStale(ver string, doc []byte) (bool, error) {
	image, ok, err := infra.CredentialsServiceImageOf(doc)
	if err != nil {
		return false, err
	}
	if ok && credentialsImageStale(ver, image) {
		return true, nil
	}
	if spec, have, err := infra.IntakeOf(doc); err != nil {
		return false, err
	} else if have && credentialsImageStale(ver, spec.Image) {
		return true, nil
	}
	spec, have, err := infra.VoiceServiceOf(doc)
	if err != nil {
		return false, err
	}
	if have && credentialsImageStale(ver, spec.Image) {
		return true, nil
	}
	// And the resident orchestrator, the fourth service of the same pin and the
	// one the run writes only once: an installation that opted in keeps its
	// block with the image of the release that opted it in, so the credentials
	// service's own image being this release's says nothing about the daemon
	// beside it — and a daemon left on the release behind's binary is the same
	// lag the hop's image is, on the service that holds signJwt.
	ospec, haveOrch, err := infra.OrchestratorOf(doc)
	if err != nil {
		return false, err
	}
	return haveOrch && credentialsImageStale(ver, ospec.Image), nil
}

// deployCredentialsService writes the block an installation that has none gets:
// the service's own facts, composed from this machine's local config by
// infra.Credentials, with this release's image resolved and pinned by digest.
//
// The document the block is set in is the newest one the workdir holds — the
// tfvars on disk, else the snapshot PrepareWorkdir took before it rebuilt the
// tree — and never an empty one: the write is a read-modify-write of the ONE
// key (infra.SetCredentialsService), so the keys of the document that is about
// to be carried into prepare's rewrite are there for every reader in between.
// In an ordinary update that is the previous run's document, which for an
// installation at the release before this one carries no credentials_service
// at all: the case this function exists for.
//
// A refusal from infra.Credentials is not a failure of the run. It says the
// installation cannot carry the service — no Firebase backend to read (its
// budget block names no project or database), or no repository job for the
// service to admit, which the service refuses to start without
// (internal/credsvc/auth.go's NewAuthenticator) — and the upgrade converges
// everything else exactly as it did before this task. The same for a resolve
// this run cannot make: nothing is left stale, and the next run tries again.
func (e *initEngine) deployCredentialsService(ctx context.Context) error {
	// The installation's own facts first, and before the resolve: an
	// installation that cannot carry the service at all spends no registry read
	// on its behalf. This is the same question infra.Credentials asks with its
	// own image in hand (CredentialsInstallationFacts), so the two agree.
	if err := infra.CredentialsInstallationFacts(e.lc); err != nil {
		fmt.Fprintf(e.r.w, "the credentials service is not deployed: %v\n", err)
		return nil
	}
	pinned, err := resolveCredentialsImage(ctx, e)
	if err != nil {
		fmt.Fprintf(e.r.w, "the credentials service is not deployed: %v\n", err)
		return nil
	}
	// The installation this run is turning into one that HAS the service, which
	// the local config does not say yet: it records the block from this run's
	// own outputs, at the end of the run (init.go's writeConfig). Both writers
	// below read that fact off the config's block, and this is the one run where
	// it is not there (credentialsServiceOptedIn).
	optedIn := credentialsServiceOptedIn(e.lc)
	// publishedVerifiers is nil on purpose, and it is not "nobody verifies":
	// the member it feeds is prepare's, written from the effective list a few
	// steps later (init.go's effectiveVerifiers through CarriedCredentialsService,
	// the tolerant read of the published node), so passing the node's list here
	// as well would be the same value read twice — and this writer makes no
	// database call (see its own doc comment).
	svc, err := infra.Credentials(optedIn, pinned, nil)
	if err != nil {
		fmt.Fprintf(e.r.w, "the credentials service is not deployed: %v\n", err)
		return nil
	}
	// The address a previous run recorded, when this machine's config carries
	// one: a second machine of one installation holds no tfvars for the service
	// at all, and the block would otherwise name the module's unassigned
	// placeholder — an audience that admits nobody — for the apply that creates
	// the revision, which the audience fill then has to correct.
	if lc := e.lc; lc != nil && lc.CredentialsService != nil {
		svc.URL = lc.CredentialsService.URL
	}
	base, err := e.wd.Vars()
	if err != nil {
		return userErr("reading the installation's tfvars: %v", err)
	}
	if len(base) == 0 {
		base = e.wd.PrevVars
	}
	if len(base) == 0 {
		// A first run on this machine: PrepareWorkdir found no tfvars to
		// snapshot, so the only document the block can be set in is the empty
		// one. That is not a loss — prepare rewrites this file from this run's
		// spec a few steps later and carries the block forward — and the
		// alternative (an empty read passed to the read-modify-write) is the
		// EOF this branch exists for.
		base = []byte("{}")
	}
	doc, err := infra.SetCredentialsService(base, svc)
	if err != nil {
		return userErr("setting the credentials service in the installation's %s: %v", infra.VarsFile, err)
	}
	// And the intake, in the same document and off the same resolve: the run
	// that deploys the service on an installation that has none is the run that
	// deploys the intake beside it, because the intake is a subcommand of the
	// very image just pinned (`fugaro intake serve`). Nothing about the
	// installation has to be read for it: the intake holds no bundle and no
	// database, so the facts CredentialsInstallationFacts refused above are the
	// service's alone.
	doc, err = infra.SetIntake(doc, infra.Intake(pinned))
	if err != nil {
		return userErr("setting the secrets intake in the installation's %s: %v", infra.VarsFile, err)
	}
	// The voice hop is deliberately NOT written here, and that is its opt-in:
	// the run that deploys the credentials service on an installation that has
	// none is the run that deploys the intake beside it, and the hop — a
	// subcommand of the same image — only for an installation that asked
	// for it. The block is the ask (var.voice_service, off unless the
	// installation turns it on), nothing composes it from the local config the
	// way the service's own block is composed, and an installation that never
	// wrote one plans no hop at all: no service, no account and no public
	// invoker binding. An installation that HAS one keeps it through prepare's
	// carry (init.go's CarriedVoiceService) and has its image moved with the two
	// services beside it (moveCredentialsImage).
	if err := e.wd.WriteVars(doc); err != nil {
		return userErr("writing the installation's %s: %v", infra.VarsFile, err)
	}
	// And this run's spec gains the review jobs of that same config, the
	// implicit default job among them (infra's implicitReviewVariant, which the
	// config's block is the opt-in of): the job this run's own apply creates,
	// and what every later stage of the run plans the root from.
	e.specReviewJobsOfTheService()
	return nil
}

// specReviewJobsOfTheService replaces this run's review set with the one the
// installation's own record describes: the set a config that HAS the
// credentials service composes (credentialsServiceOptedIn). Both of its callers
// stand in front of such an installation — the run that is deploying the block
// and the run that found the block already in the installation's tfvars,
// whatever the local config says — and the implicit default review job is the
// member the config's own answer is missing.
//
// The spec is not a local detail: every prepare of the run rewrites the
// installation root's tfvars FROM it (init.go), so a set composed from a config
// that names no service plans that root without the job. Where the state holds
// the job and the config does not — a run that died between the installation
// root's apply and the config write that records the block, the only run that
// creates it (init.go's writeConfig, the last step) — that plan is the job's
// own destruction, RefuseProjectJobDeletes stops the whole update on it, and the
// writer that would have repaired the spec sits downstream of the refusal: every
// later run repeats it, under a message naming a profile that is not what is
// missing. The carried branch is the one that reads the installation's record as
// the fact it is.
//
// A config that cannot describe the set is not a reason to stop a run: the
// review job is what is missing, and the next run composes it from the block it
// finds.
func (e *initEngine) specReviewJobsOfTheService() {
	review, err := infra.ProjectReviewJobs(credentialsServiceOptedIn(e.lc), e.spec.RunsBucket, e.spec.EnableVertex)
	if err != nil {
		fmt.Fprintf(e.r.w, "the review jobs are not deployed: %v\n", err)
		return
	}
	e.spec.ProjectReviewJobs = review
}

// credentialsServiceOptedIn is lc as the installation this run is about to
// deploy the credentials service into reads: the same config with an empty
// credentials_service block when it carries none, and lc itself otherwise.
//
// The block's PRESENCE is the opt-in every reader of it gates on — the
// deployment's own review-job table (infra.Credentials), the implicit default
// review job (infra's implicitReviewVariant) and, at launch time, the checks
// that refuse a review on an installation with no service — and the run that
// writes the block is the one run where the config does not hold it yet. A
// writer that read the config as it stands would compose an installation one
// run behind the deployment: a service that exists with no review job of the
// implicit kind, and a job that appears only on the next `fugaro update`. The
// owner's requirement of 2026-11-13 is that a review works out of the box on
// the installation the service was deployed for, so the two are written by the
// one run.
//
// The URL is deliberately left empty, and it is not a loss: Cloud Run assigns
// it, the audience fill writes it a few steps later in this same run
// (fillCredentialsAudience), and an empty one is the state every variant's
// environment is in until then (reviewVariant adds the address only when the
// config carries one).
func credentialsServiceOptedIn(lc *localcfg.Config) *localcfg.Config {
	if lc == nil || lc.CredentialsService != nil {
		return lc
	}
	next := *lc
	next.CredentialsService = &localcfg.CredentialsService{}
	return &next
}

// installationCredentialsImage is the installation root's tfvars as they stand
// on disk, and the image they carry for the credentials service: nil when they
// carry no block at all.
//
// It is recordedCredentialsImage's read for a caller that holds no workdir —
// the checks' own case: a check runs before the stage that prepares the
// workdir, so the file the last update left is the only document it has.
func installationCredentialsImage(lc *localcfg.Config) ([]byte, string, error) {
	dir, err := infra.InstallationWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return nil, "", userErr("%v", err)
	}
	doc, err := infra.ReadWorkdirVars(dir, "installation")
	if err != nil {
		return nil, "", userErr("%v", err)
	}
	image, ok, err := infra.CredentialsServiceImageOf(doc)
	if err != nil {
		return nil, "", userErr("reading the installation's tfvars: %v", err)
	}
	if !ok {
		return nil, "", nil
	}
	return doc, image, nil
}

// recordedCredentialsImage is the installation root's tfvars as they carry the
// credentials service, and the image in them: nil when they carry no block at
// all, which is the state this run deploys the service into.
//
// The document is the one prepare (init.go) reads its carry from — the tfvars
// the workdir holds NOW, else the snapshot PrepareWorkdir took before it
// rebuilt the tree — so the two agree about which entry this run is working
// on. In an ordinary update the workdir is fresh and only the snapshot has it;
// in the run that deploys the block the file on disk is this run's own write,
// and that is what the pin's second pass (the loop plans and then applies)
// reads: the block is there, its image is this release's, and nothing is
// resolved twice.
//
// A run with no workdir yet reads the installation's tfvars on disk instead,
// and that is the checks' own case rather than an edge: the loop checks every
// stage before the first one runs, and the workdir is what the FIRST stage's
// run prepares (setup), so a check that reached for it would take the run down
// before it printed its plan. The file on disk is what prepare is about to
// read and carry, so the two agree there as well — and a stage that has
// prepared its workdir uses it, because by then the file on disk may be the
// run's own newer write (deployCredentialsService) and the workdir is the
// document both the carry and the move are working on.
func (e *initEngine) recordedCredentialsImage() ([]byte, string, error) {
	var docs [][]byte
	switch {
	case e.wd != nil:
		now, err := e.wd.Vars()
		if err != nil {
			return nil, "", userErr("reading the installation's tfvars: %v", err)
		}
		docs = append(docs, now, e.wd.PrevVars)
	case e.lc != nil:
		doc, _, err := installationCredentialsImage(e.lc)
		if err != nil {
			return nil, "", err
		}
		docs = append(docs, doc)
	}
	for _, doc := range docs {
		image, ok, err := infra.CredentialsServiceImageOf(doc)
		if err != nil {
			return nil, "", userErr("reading the installation's tfvars: %v", err)
		}
		if ok {
			return doc, image, nil
		}
	}
	return nil, "", nil
}

// fillCredentialsAudience writes the service's own URL into the installation's
// tfvars from the module's output and applies it, when the two differ. It
// reports whether it applied anything.
//
// This is the second half of the audience (C1.14), and it is a second apply
// because the first one cannot do it: Cloud Run assigns the URL, Terraform
// refuses an expression that reads google_cloud_run_v2_service.credentials[0]
// .uri from inside that resource (a self-referential block), and the URL
// carries the project NUMBER, which nothing in the module learns offline. So
// the apply that CREATES the service writes the module's unassigned
// placeholder (credentials.tf's local) and its revision admits nobody; the URL
// exists the moment that apply returns, the module's own output carries it
// back (outputs.tf), and this is the apply that puts it on the revision —
// inside the same stage, so an installation this run reports as converged is
// one that serves.
//
// It needs no confirmation of its own. What the owner confirmed is the
// installation's own apply, which is what creates the service and the revision
// this fills in; this changes ONE environment value on a service that is
// already theirs and already unreachable, so asking again would be asking them
// to re-approve the same step. A run whose tfvars already carry the output's
// URL applies nothing at all — that is every run after the first, because
// prepare carries the URL forward.
//
// The intake's SECOND class list rides the same apply (renderIntakePeople,
// WC3/R5): a workspace's own project renders the roster's people into the
// intake's block here, and that alone is a reason to apply — a person the
// roster gained between two updates changes no URL, and an apply gated on the
// audience would leave them refused by the intake until the next release. On
// every other installation there is nothing of that list to write and this
// function is what it was.
//
// An output with no URL is refused rather than skipped: the module's output
// reads the service's own uri, so an empty one means the service in the state
// is not the one this run's apply made, and a revision whose audience is not
// its own URL admits no caller however healthy it looks. So is a document that
// claims a service the outputs know nothing about: that is the misspelling
// this write is the only reader of.
//
// The tfvars are read BEFORE the outputs, because they answer whether this
// function has anything to do at all: an installation whose tfvars carry no
// credentials_service has no service whose audience could be missing, and a
// terraform call to be told so is one the run does not make.
//
// Two runs it does nothing in, and both are the same rule:
//
//   - A coding agent's session, where the service is not deployed at all (see
//     ensureCredentialsService): the agent's apply is a plan the owner has not
//     confirmed, and this one has no confirmation to keep it out.
//   - --plan-only, which applies nothing by its own definition.
func (e *initEngine) fillCredentialsAudience(ctx context.Context) (bool, error) {
	if agentMarker(os.Getenv, e.r.stdinTerminal()) != "" {
		return false, nil
	}
	if e.r.o.planOnly {
		return false, nil
	}
	r := e.r
	// The installation's own tfvars answer the question first, and they are
	// read first on purpose: a run whose tfvars carry no credentials service
	// has no service to fill the audience of, and the outputs of one are not
	// worth a terraform call — which is every installation that cannot carry
	// the service at all, and every repository's own run.
	vars, err := e.wd.Vars()
	if err != nil {
		return false, userErr("reading the installation's tfvars: %v", err)
	}
	_, haveBlock, err := infra.CredentialsServiceImageOf(vars)
	if err != nil {
		return false, userErr("reading the installation's tfvars: %v", err)
	}
	if !haveBlock {
		return false, nil
	}
	raw, err := e.t.Output(ctx)
	if err != nil {
		return false, remote(err)
	}
	outs, err := infra.DecodeOutputs(raw)
	if err != nil {
		return false, remote(err)
	}
	if outs.CredentialsService == nil {
		return false, userErr("the installation's tfvars carry a credentials service and its outputs carry none: the tfvars key is not the variable the installation root reads, so nothing was deployed, and the repository roots read this output to decide whether their jobs mount a model credential at all — a run must not be told the service is there")
	}
	url := outs.CredentialsService.URL
	if url == "" {
		return false, userErr("the installation's outputs carry the credentials service %s with no URL: the module's output reads the service's own uri, so an empty one means the state holds something other than the service this run's apply created, and a revision whose audience is not its own URL admits no caller", outs.CredentialsService.Name)
	}
	current, err := infra.CredentialsServiceURLOf(vars)
	if err != nil {
		return false, userErr("reading the installation's tfvars: %v", err)
	}
	// The intake's audience is the same write, on the same apply, and for the
	// same reason: it is a second Cloud Run service with its own URL, its own
	// unassigned placeholder on the apply that creates it and its own callers
	// whose ID tokens are minted for that URL — so a run that filled only the
	// service's would leave an intake that is healthy, reachable by IAM and
	// admits nobody.
	//
	// An installation whose tfvars carry no intake is left alone: the block is
	// that installation's record of not having the feature, and this function
	// may not turn it on by inventing one (SetIntakeURL refuses to).
	intakeURL := ""
	if _, haveIntake, err := infra.IntakeOf(vars); err != nil {
		return false, userErr("reading the installation's tfvars: %v", err)
	} else if haveIntake {
		if outs.Intake == nil {
			return false, userErr("the installation's tfvars carry a secrets intake and its outputs carry none: the tfvars key is not the variable the installation root reads, so nothing was deployed, and every caller of the intake mints an ID token for a URL that does not exist")
		}
		if intakeURL = outs.Intake.URL; intakeURL == "" {
			return false, userErr("the installation's outputs carry the secrets intake %s with no URL: the module's output reads the service's own uri, so an empty one means the state holds something other than the service this run's apply created, and a revision whose audience is not its own URL admits no caller", outs.Intake.Name)
		}
	}
	currentIntake, err := infra.IntakeURLOf(vars)
	if err != nil {
		return false, userErr("reading the installation's tfvars: %v", err)
	}
	// And the intake's second class list, which is the same write for the same
	// reason one step over (WC3, R5, D3143): the people of the workspace's
	// roster, rendered from the roster itself and written into the intake's own
	// block. It rides this apply because this is the apply that carries the
	// install's own block onto the revision — the block the installation stage
	// wrote a few steps before — and because a person the roster gains between
	// two runs changes no URL at all: a step gated on the audience alone would
	// leave them refused until the next release.
	vars, peopleChanged, err := e.renderIntakePeople(ctx, vars)
	if err != nil {
		return false, err
	}
	if current == url && currentIntake == intakeURL && !peopleChanged {
		return false, nil
	}
	updated, err := infra.SetCredentialsServiceURL(vars, url)
	if err != nil {
		return false, userErr("setting the credentials service's URL in the installation's %s: %v", infra.VarsFile, err)
	}
	if intakeURL != "" {
		if updated, err = infra.SetIntakeURL(updated, intakeURL); err != nil {
			return false, userErr("setting the secrets intake's URL in the installation's %s: %v", infra.VarsFile, err)
		}
	}
	if err := e.wd.WriteVars(updated); err != nil {
		return false, userErr("writing the installation's %s: %v", infra.VarsFile, err)
	}
	changed, err := e.t.Plan(ctx, infra.PlanFile)
	if err != nil {
		return false, remote(err)
	}
	if !changed {
		return false, userErr("the installation plans nothing with credentials_service.url set to the service's own URL: the module does not carry that value onto the revision, so no revision would ever carry the audience its callers' ID tokens are minted for (the secrets intake's url is written in the same apply, and one that plans nothing is just as wrong for it)")
	}
	plan, err := e.t.Show(ctx, infra.PlanFile)
	if err != nil {
		return false, remote(err)
	}
	r.planBlock(plan)
	// The managed gate judges this plan exactly as it judges the installation
	// root's own: it is a second apply inside the same stage, and a session the
	// gate let through may not make one it never read. It is handed no imports
	// because this plan has none — every resource in it is in the state already,
	// and the cover consults that list only for a change that carries an import
	// (tf.Cover.NotCovered).
	gated, err := r.gateStep(ctx, e.t, nil, r.coverOf(r.coverReasons), nil, nil)
	if err != nil {
		return false, err
	}
	if !gated {
		if err := r.guard(ctx, e.c, plan, r.o.allowDelete); err != nil {
			return false, err
		}
	}
	if err := e.t.Apply(ctx, infra.PlanFile); err != nil {
		return false, remote(err)
	}
	r.res.Applied = true
	fmt.Fprintf(r.w, "the credentials service's audience is its own URL: %s\n", url)
	if intakeURL != "" {
		fmt.Fprintf(r.w, "the secrets intake's audience is its own URL: %s\n", intakeURL)
	}
	return true, nil
}

// renderIntakePeople is vars with the intake's second class list rendered from
// the workspace's roster, and whether it changed (WC3, R5).
//
// WHO IT IS FOR. The roster's people are the workspace's, and their tokens live
// in the workspace project's own intake (D3143): a person the roster names is
// admitted to that installation's `fugaro_u_` half under their own sub, and to
// nothing else of it. So this runs on ONE installation — a workspace's own
// project, which is the installation whose config declares the owner's half of
// the membership (workspaceDeclared) — and every other installation, every
// member's included, writes nothing here. A member holds no grant on the
// workspace's project at all (spec §5), so its run must not reach for the
// workspace's database: the predicate is asked before any database is opened,
// which is the workspace record stage's own rule (update_workspace.go).
//
// A read that fails leaves the list where it is and does not stop the upgrade,
// which is the fail-closed direction and this file's own rule for the intake's
// block (writeCarriedCredentials): an installation whose roster cannot be read
// keeps the people it admits, and the next run tries again. A roster that is
// NOT THERE is a different answer and an empty list: the node is what
// `fugaro workspace init` writes first (WU24), a workspace without one names
// nobody, and keeping the grants of a roster that is gone would be the widening
// nothing asks for.
//
// The write is omitted when the CLASS the module reads already says this: the
// block's people list is only half of it, because the module composes the class
// the revision is handed from the launchers and this list together
// (infra.IntakeClass, intake.tf's local.intake_accounts). So a roster that
// gained or lost a person the launchers already carry changes the list and not
// the class, and writing the block for one would leave the apply with no plan to
// make — which is the state the fill's own refusal reads as the module not
// carrying the class onto the revision at all, and blames the audience for. An
// ordinary update of a workspace's project writes nothing and applies nothing
// here, and so does one whose roster moved only over a launcher.
func (e *initEngine) renderIntakePeople(ctx context.Context, vars []byte) ([]byte, bool, error) {
	if e.lc == nil || !workspaceDeclared(e.lc) {
		return vars, false, nil
	}
	spec, have, err := infra.IntakeOf(vars)
	if err != nil || !have {
		return vars, false, nil
	}
	people, err := e.rosterPeople(ctx)
	if err != nil {
		fmt.Fprintf(e.r.w, "the secrets intake's people were not read from the workspace's roster: %v\n", err)
		return vars, false, nil
	}
	launchers, err := infra.LauncherAccountsOf(vars)
	if err != nil {
		return vars, false, nil
	}
	if slices.Equal(infra.IntakeClass(launchers, people), infra.IntakeClass(launchers, spec.People)) {
		return vars, false, nil
	}
	next, err := infra.SetIntakePeople(vars, people)
	if err != nil {
		return vars, false, userErr("setting the secrets intake's people in the installation's %s: %v", infra.VarsFile, err)
	}
	return next, true, nil
}

// rosterPeople is the workspace's roster as the intake's class list: the
// canonical addresses of its humans (infra.IntakePeople), read from the
// workspace's own database.
//
// The database is this installation's own budget backend, which for a
// workspace's own project IS the workspace's (the workspace is named after that
// project, F21/D1290): the roster is the node at workspaceRosters/<name> in it
// (workspace.PathRoster), and the name is the config's own (workspaceNameOf).
// The node is read rather than resolved through workspaceRosterAt, which
// REFUSES an absent one: a workspace whose roster is not written yet is a
// workspace with no people, and an update is not the command that fails over it.
func (e *initEngine) rosterPeople(ctx context.Context) ([]string, error) {
	lc, db, err := budgetDBFor(ctx, e.lc)
	if err != nil {
		return nil, err
	}
	store, err := workspace.NewRosterStore(db, workspaceNameOf(lc))
	if err != nil {
		return nil, err
	}
	node, found, err := store.Read(ctx)
	if err != nil {
		return nil, dbErr(lc, err, false)
	}
	if !found {
		return []string{}, nil
	}
	roster, err := workspace.DecodeRoster(node.Raw)
	if err != nil {
		return nil, userErr("%s: %v", store.Path(), err)
	}
	return infra.IntakePeople(roster), nil
}

// newInstallationStage is stage 3: init's installation engine. With
// --firebase-project the installation is the Firebase engine's first apply, so the
// stage is skipped.
func newInstallationStage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Installation, did: func() string { return e.newMachine },
		skip: func() string {
			if e.r.o.firebase != "" {
				return "applied as the first step of the firebase backend"
			}
			return ""
		},
		run: func(ctx context.Context) error {
			if err := e.setup(ctx); err != nil {
				return err
			}
			if e.firstRun() { // no local config yet (or one a paused setup wrote): is the installation already there (a new machine's update)?
				switch ok, err := e.r.installationExists(ctx, e.c, e.spec); {
				case err != nil:
					return err
				case ok:
					return e.r.newMachineConfig(ctx, e)
				}
				if err := e.defaultMembers(ctx); err != nil {
					return err
				}
			}
			if err := e.ensureCredentialsService(ctx); err != nil {
				return err
			}
			if err := e.r.install(ctx, e.c, e.t, e.wd, e.lc, e.spec, e.path, e.old); err != nil {
				return err
			}
			_, err := e.fillCredentialsAudience(ctx)
			return err
		},
		left: promptLeft(initflow.Installation, "run the command below: it asks you nothing and the apply goes ahead", e.r.o.doorName()),
	}
}

// firebaseProjectWord is --firebase-project's value in a printed command: the
// installation's own GCP project id, the only value it takes.
func (e *initEngine) firebaseProjectWord() string {
	if e.lc == nil {
		return firebaseProjectIDWord("")
	}
	return firebaseProjectIDWord(e.lc.GCPProject)
}

// firebaseProjectIDWord is --firebase-project's value in a command printed
// anywhere else. The flag is a string flag with no default, so a message that
// stopped at the flag prints a command nobody can run; the id is what it
// takes, and a caller with none to read prints the placeholder to type.
func firebaseProjectIDWord(id string) string {
	if id == "" {
		return "<gcp-project-id>"
	}
	return quoteWord(id)
}

// historyJobOfThisRun reports whether the history job's own apply belongs to
// this run's second installation stage. It is true where this run has read the
// Firebase root's outputs — r.fb, which the flag's path sets and the no-flag
// path sets when it adopted or created the database (AB7, R2) — and where the
// flag asked for the history job, whose apply is the one that reads them.
//
// What it is NOT is "the installation has a budget backend": an installation
// whose config already records the database reaches the Firebase root in a
// plain update only while that root's state is still missing the run index's
// composite indexes (RI2, firebaseSkip), and the history job for every other
// such run is composed by the FIRST apply from the recorded backend
// (installOptionsCarrying) and deployed there.
func (e *initEngine) historyJobOfThisRun() bool {
	return e.r.fb != nil || e.r.o.firebase != ""
}

// budgetDatabaseKnown reports whether this machine's local config already
// records the installation's budget database: the realtime database's address,
// which is the whole of what "the database exists and it is ours" means on a
// machine (F6 — the address is what a run's session is opened against, and the
// Firebase project alone is what a config that has never seen one names). The
// stage that adopts or creates a database is skipped where this answers true
// (F34), which is every converged installation — except for the one thing such
// an installation can still owe, the Firebase root's run index composite
// indexes, which firebaseSkip decides on the state because nothing else can
// create them (RI2).
func (e *initEngine) budgetDatabaseKnown() bool {
	return e.lc.Budget != nil && e.lc.Budget.RTDBURL != ""
}

// budgetBackendOn reports whether the installation has a budget backend at all:
// the switch the installation root's `enable_budget` is set from — this run's
// --firebase-project, or a config that records the backend, by its address or by
// the project it lives in. It is one door earlier than budgetDatabaseKnown: this
// machine may know only the project, while the installation has the database.
//
// It is what the budget admins' review asks (init_review.go), and it is
// installOptionsCarrying's own expression (init.go), respelled: the two must
// answer the same question, because one decides whether the history account is
// applied and the other who is shown as able to change a cap on it.
func (e *initEngine) budgetBackendOn() bool {
	return e.r.o.firebase != "" || e.lc.Terraform.BudgetBackend ||
		(e.lc.Budget != nil && e.lc.Budget.FirebaseProject != "")
}

// newFirebaseStage is stage 4: update --firebase-project's engine, which is two
// confirmed applies (the installation's history account, the Firebase root)
// and the database steps. The installation again, for the history job, is
// the installation-2 stage, after the images stage has mirrored the image.
//
// With no flag it is the same engine with one decision in front of it (AB7,
// R2, init_firebase.go's noFlagBudgetDatabase): the stage acts only to adopt the
// database this installation already has or to create one where there is none,
// in the installation's own project, and where this run may not create one it
// changes nothing and says so in one line (r.declined, which the stage reports
// as what it has to say). It is skipped where this machine already records the
// database (F34) and where nothing would answer the confirmation a creation
// needs (F4, cannotCreateTheDatabase), and the flag's own path — the one value
// it could always take, the installation's own project — is untouched.
// The skip is the one that reads (Check and not skip): the run index's
// composite indexes are the Firebase root's and only its apply creates them
// (RI2, F10), and so is the workspace's read of a member's database (WS1,
// D2988), so a converged installation is still this stage's while the root's
// state does not carry them — and while the credential may make the IAM
// changes that root's apply makes, which is F34's own operator case. The
// second question is what makes a member's plain `fugaro update` the whole of
// its step: the pin `fugaro workspace add` wrote is not in any state yet, so
// the very next update is one this stage runs, with no flag and no second
// command.
//
// firebaseSkip answers it, and everything it cannot read leaves F34's line
// standing, because the update of a converged installation must not fail over
// a read the stage does not need to run: the indexes it does not create this
// time are created by the update that can read them.
func (e *initEngine) firebaseSkip(ctx context.Context) string {
	if e.r.o.firebase != "" {
		return ""
	}
	if e.budgetDatabaseKnown() {
		const known = "the project config already records the budget database: this stage with no --firebase-project only adopts or creates one, and there is one"
		// setup's own failure is the run's to report and not this check's: every
		// stage that, unlike this one, has to run calls it too.
		if err := e.setup(ctx); err != nil {
			return known
		}
		wait, why := e.r.firebaseRootWait(ctx, e.c, e.bin, e.spec.StateBucket, cmp.Or(e.r.o.firebaseProject(), e.lc.GCPProject), e.lc)
		if !wait {
			return known + why
		}
		return ""
	}
	// F4/R2's answer is asked HERE and not in the stage's own arm,
	// because the arm is only reached by a run that can answer: the
	// loop leaves a stage whose steps nothing can confirm to the user,
	// which is the stop R2 replaces with one line and a run that
	// carries on.
	if why, yes := e.r.cannotCreateTheDatabase(); yes {
		return why
	}
	return ""
}

func newFirebaseStage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Firebase,
		check: func(ctx context.Context) (initflow.Status, error) {
			if why := e.firebaseSkip(ctx); why != "" {
				return initflow.Status{State: initflow.Skipped, Detail: why}, nil
			}
			return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
		},
		run: func(ctx context.Context) error {
			// This run's own answer, cleared before the stage speaks: the plan
			// pass and the apply pass both run the engine, and a line left over
			// from the plan would be reported for an apply that did not say it.
			e.r.declined = ""
			if err := e.setup(ctx); err != nil {
				return err
			}
			if e.firstRun() { // a first run with --firebase-project: the same default, unless the installation exists
				if ok, err := e.r.installationExists(ctx, e.c, e.spec); err != nil {
					return err
				} else if !ok {
					if err := e.defaultMembers(ctx); err != nil {
						return err
					}
				}
			}
			if err := e.ensureCredentialsService(ctx); err != nil {
				return err
			}
			// &e.spec and not e.spec: the stage sets the `enable_budget` switch
			// on the run's own spec once the database exists, so the stages
			// behind this one carry it too (initFirebase).
			if err := e.r.initFirebase(ctx, e.c, e.t, e.wd, e.bin, e.lc, &e.spec, e.path, e.old); err != nil {
				return err
			}
			_, err := e.fillCredentialsAudience(ctx)
			return err
		},
		declined: func() string { return e.r.declined },
		left:     promptLeft(initflow.Firebase, "run the command below: it asks you nothing and each apply goes ahead", e.r.o.doorName(), "--firebase-project", e.firebaseProjectWord()),
	}
}

// newInstallation2Stage is stage 6: the installation root once more, deploying
// what only the images stage's own work makes deployable — the history job,
// whose image that stage mirrors, the installation's two scheduled jobs, which
// run FROM the base image it pins: the catalog check job (#1843) and the
// reports job (R7) — and the shared project jobs, whose image is the project's
// catalog default and so the same base image (S10, D2305).
func newInstallation2Stage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Installation2, isolate: true,
		skip: func() string {
			switch {
			// F29: what deploys the history job is this run having the Firebase
			// root's outputs, not the flag that used to be the only way to get
			// them — the no-flag update that just adopted or created the
			// database reaches this stage with them.
			case e.historyJobOfThisRun(), e.catalogJobAwaitsItsBase(), e.reportsJobAwaitsItsBase():
				return ""
			case e.catalogBaseIsMissing():
				return "the project's image catalog builds its entries FROM base_images." + config.BaseKind + ", which this run does not pin: the catalog check job runs FROM that image and nothing else deploys it, so the catalog's entries are checked by nothing until a run that pins it is through"
			case infra.CatalogBuilds(e.lc):
				return "the first apply already deployed the scheduled jobs from the recorded base image: this stage has nothing of its own"
			}
			if e.budgetBackendOn() {
				return "the first apply deployed the history job from the budget backend the project config records: this stage has nothing of its own"
			}
			return "the installation has no budget backend, so there is no history job to deploy: run fugaro update once a project owner has created one"
		},
		run: func(ctx context.Context) error {
			if err := e.setup(ctx); err != nil {
				return err
			}
			// The installation's spec is read the run's own way again: it was
			// built before the images stage, and the base image both scheduled
			// jobs run FROM is one of the things that stage moves — a job this
			// apply left at the image the run started with would lag an entire
			// `fugaro update` behind. installSpec and not installOptions, for
			// the reason the repository stage reads it that way too: a config
			// this run cannot compose the default profile's job from must not
			// stop a stage of a converging update.
			//
			// The shared project jobs are read again with them (S10): their
			// image is the project's catalog default, and the images stage is
			// what moves that — the entry is written on the base image the run
			// started with, before the stage that pins this release's, so the
			// first apply's jobs carry the image the run started with. The same
			// lag, and the same apply to close it, as the scheduled jobs beside
			// them.
			next, err := installSpec(e.r.o, e.lc, e.r.withheld)
			if err != nil {
				return err
			}
			e.spec.CatalogCheck = next.CatalogCheck
			e.spec.Reports = next.Reports
			e.spec.ProjectJobs = next.ProjectJobs
			// F29: the history job is this stage's own apply where this run has
			// the Firebase root's outputs, whether the flag asked for them or
			// the no-flag arm adopted or created the database.
			if e.historyJobOfThisRun() {
				return e.r.firebaseHistory(ctx, e.c, e.t, e.wd, e.lc, e.spec)
			}
			return e.r.installCatalogCheck(ctx, e.c, e.t, e.wd, e.lc, e.spec)
		},
		leftFor: func() initflow.Left {
			if e.historyJobOfThisRun() {
				return promptLeft(initflow.Installation2, "run the command below: it asks you nothing and applies the history job", e.r.o.doorName(), "--firebase-project", e.firebaseProjectWord())
			}
			return promptLeft(initflow.Installation2, "run the command below: it asks you nothing and applies the scheduled jobs", e.r.o.doorName())
		},
	}
}

// newCredentialsStage is the stage immediately after the policy one: the
// installation root once more, with the credentials service's image moved onto
// this release's.
//
// It is here and nowhere else because the service reads the published node on
// every call and refuses a node its own binary does not understand, so a
// release whose node version moves has to move the node before the process
// that reads it is replaced (initflow.CredentialsService has the order and the
// window the order leaves; the owner's ruling of 2026-11-10). The block's
// EXISTENCE is still the installation stage's own work (ensureCredentialsService):
// this stage moves an image that is already there, and the two are one step
// apart in the tfvars — the block, then the image inside it.
//
// Its skip is what keeps a converged installation from planning the root a
// second time: a run whose four recorded images are already this release's, or
// whose tfvars carry no block at all, has nothing to move and answers with the
// sentence rather than an apply. It asks the same predicate the move does
// (servicesImageStale, which is credentialsImageStale over all four services),
// so the two can never disagree about whether there is anything to do — the
// disagreement that would leave a hop hand-written onto an older release where
// it is while the credentials service beside it is current.
//
// The image is not the only thing this stage settles. The shared project jobs'
// switch is read from the local config's per-repository records, and two
// writers move them inside a run that applies the installation root: the
// upgrade backfill (init.go's install, after that root's first apply) and the
// repository stage's own record write. A run whose records have moved past the
// jobs its spec holds has a grant to make that no later apply of this run would
// otherwise make, and a launcher that reads the records live has already been
// told to use it (ProjectJobsCredentialsServiceStale) — so that run answers with
// an apply rather than the sentence, and the root is planned once more with the
// switch re-derived (installRoot).
func newCredentialsStage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.CredentialsService, isolate: true,
		skip: func() string {
			if m := agentMarker(os.Getenv, e.r.stdinTerminal()); m != "" {
				return credentialsSessionHeld
			}
			ver := releaseVersion()
			if ver == "" {
				return credentialsDevBuildHeld
			}
			doc, image, err := e.recordedCredentialsImage()
			switch {
			case err != nil:
				// The run reports the read's own failure: a stage that could not
				// read the tfvars may not answer "nothing to move" about them.
				return ""
			case doc == nil:
				return "the installation's tfvars carry no credentials service"
			}
			stale, err := servicesImageStale(ver, doc)
			switch {
			case err != nil:
				// A document whose blocks the predicate cannot read is one this
				// stage may not answer "nothing to move" about either.
				return ""
			case stale:
				return ""
			case infra.ProjectJobsCredentialsServiceStale(e.spec.ProjectJobs, e.lc):
				// The image is this release's and the run still has an apply to
				// make: the records the shared project jobs' switch is read from
				// moved after the last apply of this root — the upgrade backfill
				// writes them at the end of the installation stage, and the
				// repository stage writes the checkout it converges — so a
				// profile the launcher now hands the service's address has no
				// roles/run.invoker grant on it yet, and the run it is handed to
				// halts at the front door (D143). This stage is the run's only
				// further apply of the installation root, so the grant lands
				// here or on a second `fugaro update`.
				return ""
			}
			if write, _ := e.orchestratorVerdict(doc, image); write {
				// The block is this run's to write: the resident orchestrator
				// (update_orchestrator.go), which every installation that can carry
				// one gets from this one command.
				return ""
			}
			return "the recorded image is already this release's" + e.orchestratorLeftOff(doc, image)
		},
		run: func(ctx context.Context) error {
			if err := e.setup(ctx); err != nil {
				return err
			}
			if err := e.moveCredentialsImage(ctx); err != nil {
				return err
			}
			_, wrote, err := e.ensureOrchestratorBlock()
			if err != nil {
				return err
			}
			e.r.rootPlanEmpty, e.r.rootApplied = false, false
			e.r.installLabel = orchestratorInstallLabel(wrote)
			_, _, err = e.r.installRoot(ctx, e.c, e.t, e.wd, e.lc, e.spec)
			return e.orchestratorApplied(wrote, err)
		},
		left: promptLeft(initflow.CredentialsService, "run the command below: it asks you nothing and moves the credentials service onto this release's image", e.r.o.doorName()),
	}
}

// catalogJobAwaitsItsBase reports whether the catalog check job is what this
// stage's apply is for: the images stage is about to move the base image that
// job runs FROM, or this run has moved it already and the installation root
// the run planned carries no job from it. The catalog has to be one this
// installation builds either way — with nothing to check there is no job.
//
// The first arm is the images stage's own decision (pinsBase) and not a
// reading of the local config, which is the whole of #1843: the config a run's
// preview reads still holds the pin the run started with, so on a release
// upgrade — not only on the first pin — only the images stage can say whether
// base_images.base is about to move. The preview, the review screen built from
// it and the converge all ask this one function, so the second apply the user's
// confirmation covers is the one the preview said would happen. The second arm
// reads the base image the config will hold by the time this stage's apply
// happens (installBaseImage), not the one the preview read: a run whose own
// --base-image names base records it between the two applies.
func (e *initEngine) catalogJobAwaitsItsBase() bool {
	if !infra.CatalogBuilds(e.lc) {
		return false
	}
	if e.images != nil && e.images.pinsBase() {
		return true
	}
	img := e.installBaseImage()
	return img != "" && (e.spec.CatalogCheck == nil || e.spec.CatalogCheck.Image != img)
}

// reportsJobAwaitsItsBase reports whether the reports job is what this stage's
// apply is for: the images stage is about to move the base image the job runs
// FROM, or this run has moved it already and the installation root the run
// planned carries no job from it — or one whose image is not that base, which
// is the whole of the lag R7 exists to avoid. It is catalogJobAwaitsItsBase's
// question without the catalog, the reports job running FROM the base image and
// from nothing else, and the two are asked apart because either job can be the
// only reason this stage has anything to do.
func (e *initEngine) reportsJobAwaitsItsBase() bool {
	if e.images != nil && e.images.pinsBase() {
		return true
	}
	img := e.installBaseImage()
	return img != "" && (e.spec.Reports == nil || e.spec.Reports.Image != img)
}

// installBaseImage is the base image the installation's two scheduled jobs run
// FROM by the time the second apply happens: this run's own --base-image value
// for base when it names one, else what the local config records. The flag is
// what initRun.writeConfig writes into the config after the first apply (the
// flag wins there), so it is the only way a decision made before any apply —
// the preview and the review screen built from it — can see the base image
// that apply is about to give the project.
func (e *initEngine) installBaseImage() string {
	for _, v := range e.r.o.baseImages {
		if kind, ref, err := localcfg.ParseBaseImageFlag(v); err == nil && kind == config.BaseKind {
			return ref
		}
	}
	return e.lc.BaseImage(config.BaseKind)
}

// catalogBaseIsMissing reports the state no stage of this run can fix: the
// project's catalog is one this installation builds and no base image is
// recorded for its check job to run FROM, because this run pins none and
// names none — a development build pins nothing, and neither does a run that
// names the kinds it handles. Nothing deploys the job, so nothing checks the
// catalog's entries until a run that pins the base image is through, and the
// stage that would deploy it says so rather than reporting the run converged.
func (e *initEngine) catalogBaseIsMissing() bool {
	return infra.CatalogBuilds(e.lc) && e.installBaseImage() == ""
}

// installCatalogCheck applies the installation root again with the scheduled
// jobs the local config now names: the run that pinned the project's base
// image deploys them from it in the same run, so the catalog's entries are
// built — and the project's reports written — without a second command from
// the user.
func (r *initRun) installCatalogCheck(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec) error {
	// The apply writes the whole installation root, and what that root holds is
	// what the label names: the catalog check job's own is named next to the
	// reports job's wherever the spec carries both (R7), and the confirmation a
	// run puts to the user is about the apply it is asking for. The catalog
	// check's job is the one that may be absent — a catalog built by nothing has
	// no job — and a label promising one the apply does not write is a
	// confirmation about another apply.
	r.installLabel = " (the reports job and its schedule)"
	if spec.CatalogCheck != nil {
		r.installLabel = " (the scheduled jobs and their schedules)"
	}
	_, stop, err := r.installRoot(ctx, c, t, wd, lc, spec)
	if err != nil || stop {
		return err
	}
	return nil
}
