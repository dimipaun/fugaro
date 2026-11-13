package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
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

	// The one confirmation's review screen is built from the preview's stages
	// and the two stages it asks about (init_review.go).
	previewed []initflow.StageResult
	images    *imagesStage
	repo      *repositoryStage
	policy    *policyMigrateStage

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
	return initflow.Options{
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
		// The managed gate is `fugaro update`'s (D1016, update_managed.go):
		// managedStage answers which door and which stages, and the loop asks
		// it only in a coding agent's session, so a person's run never reads a
		// mark on account of this line.
		ManagedGate: e.managedStage,
	}
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
	// republish).
	stages = append(stages, newSecretsStage(e), newPluginStage(e), e.repo)
	// The rules stage before the policy stage: the deployed security rules
	// generation is what a published policy node is read under, and the policy
	// write is gated on it — a run that migrated the node first would refuse
	// its own write and leave the upgrade for a second command
	// (init_rules_stage.go, the owner's rule of 2026-10-30).
	stages = append(stages, newRulesStage(e))
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
	// The layer stage after the policy one: the project layer this run would
	// publish is read under the node the policy stage just moved in place, so a
	// layer whose claims the older node could not carry is judged by the same
	// installation it is published for (init_layer_stage.go).
	stages = append(stages, newLayerStage(e))
	// The checkout's own steps (the cloud half; the pin and the plugin are
	// are a stage of this run too: fugaro update is the whole upgrade, so the
	// one command the user runs may not leave the checkout it runs in behind
	// (init_checkout_upgrade.go, the owner's rule of 2026-10-30).
	stages = append(stages, newCheckoutStage(e))
	// The login unit that runs the orchestrator daemon: `fugaro setup` writes
	// the first one (init_orchestrator_stage.go, the owner's Q2 answer of
	// 2026-11-04) and `fugaro update` re-renders one already installed and
	// never installs one (update_unit_stage.go, T7's ruling of 2026-11-05).
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
	// project. `setup` registers it on every first run: Fugaro only starts
	// fresh, so a first run creates its project (settleCreation sets the
	// field the flag sets), and the stage refuses an ID that already exists
	// unless an earlier run of setup created it. `update` never registers it,
	// and neither does a `setup` whose installation is already there: e.old is
	// the project config's file, and a run that has one converges as update
	// does (D11, reconciliation F24).
	if e.r.o.createProject || (e.r.o.door == setupDoor.name && e.firstRun()) {
		stages = append(stages, newProjectStage(e))
	}
	return stages
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
	e.announceAlreadySetUp()
	stages := initStages(e)
	e.r.review = &runReview{cover: e.cover, firebase: e.firebaseVerified, owner: e.owner, screen: e.reviewScreen}
	e.gateEarly(ctx)
	// The repository this run is about to onboard joins the local config
	// before the first stage plans, so the installation stage's own apply
	// names its jobs in the credentials service's table (the same run grants
	// them roles/run.invoker on that service: makeOnboardingRepoEntry).
	e.makeOnboardingRepoEntry()
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
	// The door's one line about the runs that are running while it converges,
	// and the last thing before the first stage writes anything: a person
	// deciding whether it is safe to update now reads it before the update
	// starts rather than after. It asks nothing and it refuses nothing, and it
	// is silent when there is nothing running (update_in_flight.go).
	e.announceRunsInFlight(ctx)
	res, err := initflow.Run(ctx, stages, e.options())
	if err == nil && res != nil && e.r.o.planOnly && res.Failed == nil {
		e.planOnlyReview(ctx, res.Stages)
	}
	return e.outcome(ctx, res, err)
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
		_ = r.printResult()
		return res.Cause
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
	// And T-RU5's, the fifth and last: what this release fixes about the
	// project's filed issues, which is a step of the run and not of the door
	// (update_resolved.go) because --json's document carries it and is printed
	// on the next line. It is reached on the same path the four above are —
	// the converge applied everything it had — and it is the one of the five
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
	if len(res.Left) > 0 && !o.planOnly {
		return userErr("%s is not finished: %d step(s) left for you, listed above", o.doorProgram(), len(res.Left))
	}
	return nil
}

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
	run  func(ctx context.Context) error
	left initflow.Left
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

func (s *engineStage) Check(context.Context) (initflow.Status, error) {
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
	_, err = e.writeCarriedCredentials(ctx, doc)
	return err
}

// writeCarriedCredentials writes the carried block back into the installation
// root's tfvars, with its two job tables — the coding one and the review-only
// one — brought up to date with this machine's config, and returns the document
// it wrote.
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
func (e *initEngine) credentialsResolveBeforeRaise(ctx context.Context) error {
	ver := releaseVersion()
	if ver == "" {
		return nil
	}
	doc, image, err := e.recordedCredentialsImage()
	if err != nil || doc == nil || !credentialsImageStale(ver, image) {
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
// forward onto this release's, in the installation root's tfvars, and it is the
// credentials-service stage's step (init_stages.go's newCredentialsStage): it
// runs after the policy stage, so the node every call of the service is read
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
	doc, image, err := e.recordedCredentialsImage()
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
	if !credentialsImageStale(ver, image) {
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
// about a block the move would rewrite.
func credentialsImageStale(ver, image string) bool {
	recorded := infra.CredentialsServiceImageRelease(image)
	return recorded == "" || newer(ver, recorded)
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
	// publishedVerifiers is nil on purpose, and it is not "nobody verifies":
	// the member it feeds is prepare's, written from the effective list a few
	// steps later (init.go's effectiveVerifiers through CarriedCredentialsService,
	// the tolerant read of the published node), so passing the node's list here
	// as well would be the same value read twice — and this writer makes no
	// database call (see its own doc comment).
	svc, err := infra.Credentials(e.lc, pinned, nil)
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
	if err := e.wd.WriteVars(doc); err != nil {
		return userErr("writing the installation's %s: %v", infra.VarsFile, err)
	}
	return nil
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
	if current == url && currentIntake == intakeURL {
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
	gated, err := r.gateStep(ctx, e.t, nil, r.coverOf(r.coverReasons), nil)
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

// newFirebaseStage is stage 4: update --firebase-project's engine, which is two
// confirmed applies (the installation's history account, the Firebase root)
// and the database steps. The installation again, for the history job, is
// the installation-2 stage, after the images stage has mirrored the image.
func newFirebaseStage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Firebase,
		skip: func() string {
			if e.r.o.firebase == "" {
				return "no --firebase-project given"
			}
			return ""
		},
		run: func(ctx context.Context) error {
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
			if err := e.r.initFirebase(ctx, e.c, e.t, e.wd, e.bin, e.lc, e.spec, e.path, e.old); err != nil {
				return err
			}
			_, err := e.fillCredentialsAudience(ctx)
			return err
		},
		left: promptLeft(initflow.Firebase, "run the command below: it asks you nothing and each apply goes ahead", e.r.o.doorName(), "--firebase-project", e.firebaseProjectWord()),
	}
}

// newInstallation2Stage is stage 6: the installation root once more, deploying
// what only the images stage's own work makes deployable — the history job,
// whose image that stage mirrors, and the catalog check job, which runs FROM
// the base image it pins (#1843).
func newInstallation2Stage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Installation2, isolate: true,
		skip: func() string {
			switch {
			case e.r.o.firebase != "", e.catalogJobAwaitsItsBase():
				return ""
			case e.catalogBaseIsMissing():
				return "no --firebase-project given, and the project's image catalog builds its entries FROM base_images." + config.BaseKind + ", which this run does not pin: the catalog check job runs FROM that image and nothing else deploys it, so the catalog's entries are checked by nothing until a run that pins it is through"
			case infra.CatalogBuilds(e.lc):
				return "no --firebase-project given, and the first apply already deployed the catalog check job from the recorded base image: this stage has nothing of its own"
			}
			return "no --firebase-project given: nothing to deploy the history job with"
		},
		run: func(ctx context.Context) error {
			if err := e.setup(ctx); err != nil {
				return err
			}
			// The installation's spec is read the run's own way again: it was
			// built before the images stage, and the catalog check job's image
			// is one of the things that stage moves.
			next, err := installOptions(e.r.o, e.lc)
			if err != nil {
				return err
			}
			e.spec.CatalogCheck = next.CatalogCheck
			if e.r.o.firebase != "" {
				return e.r.firebaseHistory(ctx, e.c, e.t, e.wd, e.lc, e.spec)
			}
			return e.r.installCatalogCheck(ctx, e.c, e.t, e.wd, e.lc, e.spec)
		},
		leftFor: func() initflow.Left {
			if e.r.o.firebase != "" {
				return promptLeft(initflow.Installation2, "run the command below: it asks you nothing and applies the history job", e.r.o.doorName(), "--firebase-project", e.firebaseProjectWord())
			}
			return promptLeft(initflow.Installation2, "run the command below: it asks you nothing and applies the catalog check job", e.r.o.doorName())
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
// second time: a run whose recorded image is already this release's, or whose
// tfvars carry no block at all, has nothing to move and answers with the
// sentence rather than an apply. It asks the same predicate the move does
// (credentialsImageStale), so the two can never disagree about whether there is
// anything to do.
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
			case !credentialsImageStale(ver, image):
				return "the recorded image is already this release's"
			}
			return ""
		},
		run: func(ctx context.Context) error {
			if err := e.setup(ctx); err != nil {
				return err
			}
			if err := e.moveCredentialsImage(ctx); err != nil {
				return err
			}
			e.r.installLabel = " (this release's credentials service)"
			_, stop, err := e.r.installRoot(ctx, e.c, e.t, e.wd, e.lc, e.spec)
			if err != nil || stop {
				return err
			}
			return nil
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
// happens (catalogBaseImage), not the one the preview read: a run whose own
// --base-image names base records it between the two applies.
func (e *initEngine) catalogJobAwaitsItsBase() bool {
	if !infra.CatalogBuilds(e.lc) {
		return false
	}
	if e.images != nil && e.images.pinsBase() {
		return true
	}
	img := e.catalogBaseImage()
	return img != "" && (e.spec.CatalogCheck == nil || e.spec.CatalogCheck.Image != img)
}

// catalogBaseImage is the base image the catalog check job runs FROM by the
// time the second apply happens: this run's own --base-image value for base
// when it names one, else what the local config records. The flag is what
// initRun.writeConfig writes into the config after the first apply (the
// flag wins there), so it is the only way a decision made before any apply —
// the preview and the review screen built from it — can see the base image
// that apply is about to give the project.
func (e *initEngine) catalogBaseImage() string {
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
	return infra.CatalogBuilds(e.lc) && e.catalogBaseImage() == ""
}

// installCatalogCheck applies the installation root again with the catalog
// check job the local config now names: the run that pinned the project's base
// image deploys the job from it in the same run, so the catalog's entries are
// built without a second command from the user.
func (r *initRun) installCatalogCheck(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, lc *localcfg.Config, spec infra.InstallationSpec) error {
	r.installLabel = " (the catalog check job and its schedule)"
	_, stop, err := r.installRoot(ctx, c, t, wd, lc, spec)
	if err != nil || stop {
		return err
	}
	return nil
}
