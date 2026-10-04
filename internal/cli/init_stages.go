package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

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

// converge runs fugaro init's default and --firebase modes through the
// loop and maps its result to what init returns.
func (e *initEngine) converge(ctx context.Context) error {
	r, o := e.r, e.r.o
	stages := []initflow.Stage{&preflightStage{e}, &installationStage{e}, &firebaseStage{e}}
	res, err := initflow.Run(ctx, stages, initflow.Options{
		Yes: o.yes, NonInteractive: o.nonInteractive, PlanOnly: o.planOnly,
		Terminal: stdinIsTerminal(r.cmd.InOrStdin()), Out: r.w,
		Project: r.projectName, GCPProject: r.gcpProject, Region: e.lc.Region,
	})
	if err != nil {
		return userErr("%v", err)
	}
	r.res.Stages, r.res.LeftForYou = res.Stages, res.Left
	if e.envErr != nil {
		return e.envErr // as init always refused: the problem and its fix
	}
	if res.Failed != nil {
		return remote(res.Cause)
	}
	if err := r.printResult(); err != nil {
		return err
	}
	if len(res.Left) > 0 && !o.planOnly {
		return userErr("fugaro init is not finished: %d step(s) left for you, listed above", len(res.Left))
	}
	return nil
}

// planCounts is the one-line summary of the installation plan the engine
// just showed, if it showed one.
func (e *initEngine) planned() initflow.Plan {
	if c := e.r.res.Changes; c != nil && *c != (infra.PlanCounts{}) {
		return initflow.Plan{Detail: c.String()}
	}
	if e.r.res.Changes != nil {
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
		lf := initflow.Left{Stage: initflow.Preflight, Kind: initflow.LeftCommand, Text: err.Error()}
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
	return initflow.Left{Stage: initflow.Preflight, Kind: initflow.LeftCommand, Text: "fix the environment problem named above and rerun fugaro init"}
}

// installationStage is stage 3: init's installation engine. It cannot know
// without planning whether anything is missing, so its check says it plans
// when applied; an apply that finds nothing is reported "No changes". With
// --firebase the installation is the Firebase engine's first apply, so the
// stage is skipped.
type installationStage struct{ e *initEngine }

func (installationStage) Name() string    { return initflow.Installation }
func (installationStage) SelfConfirming() {}
func (s installationStage) Check(context.Context) (initflow.Status, error) {
	if s.e.r.o.firebase != "" {
		return initflow.Status{State: initflow.Skipped, Detail: "applied as the first step of the firebase backend"}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
}
func (s installationStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	o := s.e.r.o
	was := o.planOnly
	o.planOnly = true // the engine's own plan-only: its plan is shown, nothing applied
	defer func() { o.planOnly = was }()
	if err := s.run(ctx); err != nil {
		return initflow.Plan{}, err
	}
	return s.e.planned(), nil
}
func (s installationStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	if err := s.run(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	return initflow.Outcome{Changed: s.e.r.res.Applied, Detail: noChanges(s.e.r)}, nil
}
func (s installationStage) run(ctx context.Context) error {
	e := s.e
	if err := e.setup(ctx); err != nil {
		return err
	}
	return e.r.install(ctx, e.c, e.t, e.wd, e.lc, e.spec, e.path, e.old)
}

// Verify: the engine applies the plan it showed and reads the outputs back
// (a failure there is the apply's own error), so there is nothing more to
// read.
func (installationStage) Verify(context.Context) error { return nil }
func (installationStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Installation, Kind: initflow.LeftPrompt, Text: "run fugaro init in your own terminal and type the project's name to apply"}
}

// firebaseStage is stage 4: init --firebase's engine, which is three
// confirmed applies (the installation's history account, the Firebase root,
// the installation again for the history job) and the database steps.
type firebaseStage struct{ e *initEngine }

func (firebaseStage) Name() string    { return initflow.Firebase }
func (firebaseStage) SelfConfirming() {}
func (s firebaseStage) Check(context.Context) (initflow.Status, error) {
	if s.e.r.o.firebase == "" {
		return initflow.Status{State: initflow.Skipped, Detail: "no --firebase given"}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "plans when applied"}, nil
}
func (s firebaseStage) Plan(ctx context.Context, _ initflow.Env) (initflow.Plan, error) {
	o := s.e.r.o
	was := o.planOnly
	o.planOnly = true
	defer func() { o.planOnly = was }()
	if err := s.run(ctx); err != nil {
		return initflow.Plan{}, err
	}
	return s.e.planned(), nil
}
func (s firebaseStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	if err := s.run(ctx); err != nil {
		return initflow.Outcome{}, err
	}
	return initflow.Outcome{Changed: s.e.r.res.Applied, Detail: noChanges(s.e.r)}, nil
}
func (s firebaseStage) run(ctx context.Context) error {
	e := s.e
	if err := e.setup(ctx); err != nil {
		return err
	}
	return e.r.initFirebase(ctx, e.c, e.t, e.wd, e.bin, e.lc, e.spec, e.path, e.old)
}
func (firebaseStage) Verify(context.Context) error { return nil }
func (firebaseStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Firebase, Kind: initflow.LeftPrompt, Text: "run fugaro init --firebase <firebase-project-id> in your own terminal and type the project's name at each apply"}
}

func noChanges(r *initRun) string {
	if r.res.Applied {
		return fmt.Sprintf("applied: %s", func() string {
			if r.res.Changes != nil {
				return r.res.Changes.String()
			}
			return "done"
		}())
	}
	return "No changes"
}
