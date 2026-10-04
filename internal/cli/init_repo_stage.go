package cli

import (
	"context"
	"errors"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
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
	tg     *repoTarget
	st     initflow.Status
	warned bool
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
		if s.tg == nil && s.e.r.o.githubAppID != "" && !s.warned {
			s.warned = true
			s.e.r.warn("--github-app-id is not used: the repository stage is " + string(s.st.State) + " (" + oneLineCLI(s.st.Detail) + ")")
		}
	}
}

// Missing is --github-app-id when the engine will need it and the local
// config does not hold it, for --non-interactive's one list of flags. A
// repository that is not the project's yet is never asked about here: its
// stage is needs-you, with the --onboard-repo line.
func (s *repositoryStage) Missing() []string {
	s.resolve(context.Background())
	if s.tg == nil || !s.tg.needsAppID(s.e.lc, s.e.r.o) {
		return nil
	}
	if st, err := s.e.authState(s.tg.repo); err != nil || st != authOK {
		return nil
	}
	return []string{appIDFlag}
}

func (s *repositoryStage) Check(ctx context.Context) (initflow.Status, error) {
	s.resolve(ctx)
	if s.tg == nil {
		return s.st, nil
	}
	repo := pluginwire.Printable(s.tg.repo)
	switch a, err := s.e.authState(s.tg.repo); {
	case err != nil:
		return initflow.Status{}, err
	case a == authAsk:
		return initflow.Status{State: initflow.Todo, Detail: "onboards " + repo + ", which is not the project's yet: asks you to type its name first"}, nil
	case a == authNeeded:
		lf := onboardLeft(s.tg.repo)
		return initflow.Status{State: initflow.NeedsYou, Detail: unknownDetail(s.e.lc.Name, s.tg.repo), Left: &lf}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "onboards " + repo + "; plans when applied"}, nil
}

// authorize is the gate and then the App ID, before the engine's first
// cloud call: a repository the project does not list is typed in (or was
// opted in by --onboard-repo); the GitHub App's ID is asked once, then held
// in the run's options and recorded in the local config by the engine when it
// succeeds. Without a terminal either is the user's to do.
func (s *repositoryStage) authorize(ctx context.Context, env initflow.Env) error {
	s.resolve(ctx)
	e := s.e
	if s.tg == nil {
		return nil
	}
	switch a, err := e.authState(s.tg.repo); {
	case err != nil:
		return err
	case a == authNeeded || (a == authAsk && !env.Interactive):
		return &initflow.NeedsYouError{Left: onboardLeft(s.tg.repo)}
	case a == authAsk:
		ok, err := e.confirmRepo(s.tg.root, s.tg.repo)
		if err != nil {
			return err
		}
		if !ok {
			return &initflow.NeedsYouError{Left: onboardLeft(s.tg.repo)}
		}
	}
	if !s.tg.needsAppID(e.lc, e.r.o) {
		return nil
	}
	if !env.Interactive || !e.r.canAsk() {
		return &initflow.NeedsYouError{Left: initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand,
			Text: selfCommand() + " init --github-app-id <id>"}}
	}
	id, err := e.r.prompt("GitHub App ID for "+pluginwire.Printable(s.tg.repo)+" (a number, not a secret; on the App's settings page)", anyStoredAppID(e.lc), func(v string) error {
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
	if err := s.authorize(ctx, env); err != nil {
		return initflow.Plan{}, err
	}
	return s.engineStage.Plan(ctx, env)
}

func (s *repositoryStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if err := s.authorize(ctx, env); err != nil {
		return initflow.Outcome{}, err
	}
	return s.engineStage.Apply(ctx, env)
}
