package cli

import (
	"context"
	"errors"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// The repository stage (stage 9, design §3.6): onboards the checkout's
// repository by running today's fugaro init --repo engine as the last step
// of the converge. It applies only when the checkout's default branch has a
// valid fugaro.yaml naming this project (/fugaro:setup writes it, and its
// pull request has to be merged first: the runs read fugaro.yaml from the
// default branch, and the first image build clones it); otherwise it is
// skipped with the reason and the next step. Everything the engine does is
// the engine's own, flags and confirmations included (the first image
// build's typed, billable confirmation, --yes, --no-build): the stage only
// asks for the GitHub App's ID first when the engine will need it.

// repositoryStage is stage 9: the init --repo engine, run for the checkout.
type repositoryStage struct {
	*engineStage
	tg *repoTarget
	st initflow.Status
}

func newRepositoryStage(e *initEngine) *repositoryStage {
	s := &repositoryStage{}
	s.engineStage = &engineStage{e: e, name: initflow.Repository, isolate: true,
		skip: func() string { return "" }, // Check decides, below
		run: func(ctx context.Context) error {
			if err := e.environment(); err != nil {
				return err
			}
			return e.r.repoEngine(ctx, ".", e.bin, true)
		},
		left: initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftPrompt, Text: "run fugaro init in your own terminal, in the checkout, and type the project's name at each apply"},
	}
	return s
}

func (s *repositoryStage) resolve(ctx context.Context) {
	if s.tg == nil && s.st == (initflow.Status{}) {
		s.tg, s.st = resolveRepoTarget(ctx, s.e.lc.Name)
	}
}

// Missing is --github-app-id when the engine will need it and the local
// config does not hold it, for --non-interactive's one list of flags.
func (s *repositoryStage) Missing() []string {
	s.resolve(context.Background())
	if s.tg != nil && s.tg.needsAppID(s.e.lc, s.e.r.o) {
		return []string{appIDFlag}
	}
	return nil
}

func (s *repositoryStage) Check(ctx context.Context) (initflow.Status, error) {
	s.resolve(ctx)
	if s.tg == nil {
		return s.st, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "onboards " + s.tg.repo + "; plans when applied"}, nil
}

// askAppID takes the GitHub App's ID before the engine needs it: asked once
// at a prompt, then held in the run's options (so it is not asked again) and
// recorded in the local config by the engine when it succeeds. Without a
// terminal it is the user's to pass.
func (s *repositoryStage) askAppID(env initflow.Env) error {
	e := s.e
	s.resolve(context.Background())
	if s.tg == nil || !s.tg.needsAppID(e.lc, e.r.o) {
		return nil
	}
	if !env.Interactive || !e.r.canAsk() {
		return &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand,
			Text: selfCommand() + " init --github-app-id <id>"}}
	}
	id, err := e.r.prompt("GitHub App ID for "+s.tg.repo+" (a number, not a secret; on the App's settings page)", anyStoredAppID(e.lc), func(v string) error {
		if !appIDRE.MatchString(v) {
			return errors.New("a GitHub App ID is 1 to 20 digits")
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.r.o.githubAppID = id
	return nil
}

func (s *repositoryStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	if err := s.askAppID(env); err != nil {
		return initflow.Plan{}, err
	}
	return s.engineStage.Plan(ctx, env)
}

func (s *repositoryStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if err := s.askAppID(env); err != nil {
		return initflow.Outcome{}, err
	}
	return s.engineStage.Apply(ctx, env)
}
