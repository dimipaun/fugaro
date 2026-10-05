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

// The converge (internal/initflow) over the engines fugaro init already
// has. The stages here only call them: what init, init --firebase and the
// rest do, their flags, confirmations and messages, are the engines' own.
// Stages that have no engine yet (project, services, images, the history
// job's second pass, secrets, plugin wiring, repository) are registered
// when the tasks that write them land.

// initEngine is what the engines need, built in the order fugaro init has
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

	ready bool
	c     *infra.Clients
	t     *tf.TF
	wd    *infra.Workdir

	authorized map[string]bool // repositories the user typed in this run (see init_repo_gate.go)
	declined   map[string]bool // repositories the user was asked about and did not type
	admins     []string        // the GCP project's owners and editors, read once for the review (init_review.go)
	adminsRead bool
	fpChecked  string // "", "yes" or "no": the Firebase project's verification, once
	defaulted  string // the member a new config's launchers and operators defaulted to (init_members.go)
	adopted    string // set when the installation stage adopted an existing installation instead of applying

	// The one confirmation's review screen is built from the preview's stages
	// and the two stages it asks about (init_review.go).
	previewed []initflow.StageResult
	images    *imagesStage
	repo      *repositoryStage

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
// every installation engine needs, done once.
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
	if e.c, err = newInitClients(ctx, e.lc); err != nil {
		return err
	}
	if err := e.r.resourceManager(ctx, e.c); err != nil {
		return err
	}
	e.ready = true
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
		Project: r.projectName, GCPProject: r.gcpProject, Region: e.lc.Region,
		Redact: e.redact, OnPreview: func(rs []initflow.StageResult) { e.previewed = rs },
	}
}

// converge runs fugaro init's default and --firebase modes through the
// loop and maps its result to what init returns.
func (e *initEngine) converge(ctx context.Context) error {
	e.images, e.repo = newImagesStage(e), newRepositoryStage(e)
	stages := []initflow.Stage{&preflightStage{e}, newInstallationStage(e), newFirebaseStage(e), e.images, newInstallation2Stage(e)}
	stages = append(stages, newSecretsStage(e), newPluginStage(e), e.repo)
	e.r.review = &runReview{cover: e.cover, firebase: e.firebaseVerified, owner: e.owner, screen: e.reviewScreen}
	if e.r.o.createProject {
		stages = append(stages, newProjectStage(e))
	}
	e.gateEarly(ctx)
	res, err := initflow.Run(ctx, stages, e.options())
	if err == nil && res != nil && e.r.o.planOnly && res.Failed == nil {
		e.planOnlyReview(ctx, res.Stages)
	}
	return e.outcome(res, err)
}

// outcome maps a loop result to init's error, keeping the exit codes the
// engines always had: an engine's own error is returned as it is (its
// ExitError code, else 1), a refusal before anything ran is a user error
// (1), and a run that is unfinished only because the user has to act is 1
// too. Only an engine's coded remote failures are 2. With --json the
// result is printed first, whatever the outcome, so CI gets the account.
func (e *initEngine) outcome(res *initflow.Result, runErr error) error {
	r, o := e.r, e.r.o
	if res != nil {
		r.res.Stages, r.res.LeftForYou, r.res.Failed, r.res.Note = res.Stages, res.Left, res.Failed, res.Note
	}
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
		return userErr("fugaro init is not finished: %d step(s) left for you, listed above", len(res.Left))
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
func (preflightStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Preflight, Kind: initflow.LeftConsole, Text: "fix the environment problem named above, then rerun", Commands: []string{selfCommand() + " init"}}
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
	// installation stage's adoption writes the local config and applies
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
	if err := s.e.adoptGuard(s.name); err != nil {
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
// --firebase the installation is the Firebase engine's first apply, so the
// stage is skipped.
func newInstallationStage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Installation, did: func() string { return e.adopted },
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
			if e.old == nil { // no local config yet: is this an installation someone else made?
				switch ok, err := e.r.installationExists(ctx, e.c, e.spec); {
				case err != nil:
					return err
				case ok:
					return e.r.adopt(ctx, e)
				}
				if err := e.defaultMembers(ctx); err != nil {
					return err
				}
			}
			return e.r.install(ctx, e.c, e.t, e.wd, e.lc, e.spec, e.path, e.old)
		},
		left: promptLeft(initflow.Installation, "type the project's name to apply", "init"),
	}
}

// newFirebaseStage is stage 4: init --firebase's engine, which is two
// confirmed applies (the installation's history account, the Firebase root)
// and the database steps. The installation again, for the history job, is
// the installation-2 stage, after the images stage has mirrored the image.
func newFirebaseStage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Firebase,
		skip: func() string {
			if e.r.o.firebase == "" {
				return "no --firebase given"
			}
			return ""
		},
		run: func(ctx context.Context) error {
			if err := e.setup(ctx); err != nil {
				return err
			}
			if e.old == nil { // a first run with --firebase: the same default, unless the installation exists
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
		left: promptLeft(initflow.Firebase, "type the project's name at each apply", "init", "--firebase", "<firebase-project-id>"),
	}
}

// newInstallation2Stage is stage 6: the installation root once more, which
// deploys the history job with the Firebase outputs now that the images
// stage has put its image in the registry. Only --firebase has a history
// job to deploy.
func newInstallation2Stage(e *initEngine) *engineStage {
	return &engineStage{e: e, name: initflow.Installation2, isolate: true,
		skip: func() string {
			if e.r.o.firebase == "" {
				return "no --firebase given: nothing to deploy the history job with"
			}
			return ""
		},
		run: func(ctx context.Context) error {
			if err := e.setup(ctx); err != nil {
				return err
			}
			return e.r.firebaseHistory(ctx, e.c, e.t, e.wd, e.lc, e.spec)
		},
		left: promptLeft(initflow.Installation2, "type the project's name to apply the history job", "init", "--firebase", "<firebase-project-id>"),
	}
}
