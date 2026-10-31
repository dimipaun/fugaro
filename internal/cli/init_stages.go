package cli

import (
	"context"
	"errors"
	"os"
	"slices"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

	redact, held []string // the loop's redaction list, and the slots at its end a stage fills with a value it holds
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

// options is the loop's configuration from the flags.
func (e *initEngine) options() initflow.Options {
	r, o := e.r, e.r.o
	if e.redact == nil { // built once: the loop and the stage share the slots
		base := secretsHeld()
		e.redact = append(slices.Clone(base), make([]string, maxHeldSecrets)...)
		e.held = e.redact[len(base):]
	}
	return initflow.Options{
		Yes: o.yes, NonInteractive: o.nonInteractive, PlanOnly: o.planOnly,
		Terminal: stdinIsTerminal(r.cmd.InOrStdin()), JSON: o.asJSON, Agent: agentMarker(os.Getenv), Out: r.w,
		Program: o.loopProgram(),
		Project: r.projectName, GCPProject: r.gcpProject, Region: e.lc.Region,
		Redact: e.redact, OnPreview: func(rs []initflow.StageResult) { e.previewed = rs },
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
// deployed security rules generation, the published policy node's migration
// and the checkout's own steps: fugaro
// upgrade's pin, plugin and cloud steps for that checkout
// (init_checkout_upgrade.go, the owner's rule of 2026-10-30). There is no
// services stage in either
// list: initflow.Services is a slot in the canonical order nothing registers.
func initStages(e *initEngine) []initflow.Stage {
	e.images, e.repo = newImagesStage(e), newRepositoryStage(e)
	stages := []initflow.Stage{&preflightStage{e}, newInstallationStage(e), newFirebaseStage(e), e.images, newInstallation2Stage(e)}
	// The credentials service is turned on by its own owner-run flag and by
	// nothing else, and the stage comes BEFORE the repository stage: a
	// repository root reads the installation's credentials_service output to
	// decide whether its jobs mount a model credential at all, and grants each
	// job's account an invoker on the service (C1.20).
	if e.r.o.provisionCredentials {
		stages = append(stages, newCredentialsStage(e))
	}
	// The policy stage last: it rewrites the published node every run of the
	// project is admitted by, and the stages before it are what those runs
	// are launched against (see the stage's own file for why it is a
	// migration and never a delete and a republish).
	stages = append(stages, newSecretsStage(e), newPluginStage(e), e.repo)
	// The rules stage before the policy stage: the deployed security rules
	// generation is what a published policy node is read under, and the policy
	// write is gated on it — a run that migrated the node first would refuse
	// its own write and leave the upgrade for a second command
	// (init_rules_stage.go, the owner's rule of 2026-10-30).
	stages = append(stages, newRulesStage(e))
	e.policy = newPolicyMigrateStage(e)
	stages = append(stages, e.policy)
	// The checkout's own steps (fugaro upgrade's pin, plugin and cloud steps)
	// are a stage of this run too: fugaro update is the whole upgrade, so the
	// one command the user runs may not leave the checkout it runs in behind
	// (init_checkout_upgrade.go, the owner's rule of 2026-10-30).
	stages = append(stages, newCheckoutStage(e))
	// The Batch backend's provisioning is the owner's step and nothing else
	// reaches it: the stage is registered only under its own flag, and
	// `backend: cloud-batch` stays refused by config validation until the
	// enable task (design compute-backend §8.1 tasks 6 and 9).
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
	if err := r.printResult(); err != nil {
		return err
	}
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
func (s *engineStage) Left() initflow.Left          { return s.left }

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
			return e.r.install(ctx, e.c, e.t, e.wd, e.lc, e.spec, e.path, e.old)
		},
		left: promptLeft(initflow.Installation, "type the project's name to apply", e.r.o.doorName()),
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
			return e.r.initFirebase(ctx, e.c, e.t, e.wd, e.bin, e.lc, e.spec, e.path, e.old)
		},
		left: promptLeft(initflow.Firebase, "type the project's name at each apply", e.r.o.doorName(), "--firebase-project", e.firebaseProjectWord()),
	}
}

// newInstallation2Stage is stage 6: the installation root once more, which
// deploys the history job with the Firebase outputs now that the images
// stage has put its image in the registry. Only --firebase-project has a history
// job to deploy.
func newInstallation2Stage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Installation2, isolate: true,
		skip: func() string {
			if e.r.o.firebase == "" {
				return "no --firebase-project given: nothing to deploy the history job with"
			}
			return ""
		},
		run: func(ctx context.Context) error {
			if err := e.setup(ctx); err != nil {
				return err
			}
			return e.r.firebaseHistory(ctx, e.c, e.t, e.wd, e.lc, e.spec)
		},
		left: promptLeft(initflow.Installation2, "type the project's name to apply the history job", e.r.o.doorName(), "--firebase-project", e.firebaseProjectWord()),
	}
}
