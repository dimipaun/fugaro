package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The checkout stage of the converge (initflow.Checkout): what `fugaro
// upgrade` does for the checkout, run inside `fugaro update` — the owner's
// rule of 2026-10-30, one fugaro update is the whole upgrade. The user is busy
// and is not expected to run a second command, so the pin in the checkout's
// .claude/settings.json, the plugin in Claude Code and the repository's job
// images move here, with the version the binary just became.
//
// It calls upgrade's own steps and never a copy of them, because the two front
// doors of one upgrade may not drift: a refusal (a pin newer than this binary,
// a fork's marketplace, a cloud write in a coding agent's session, a billable
// build not confirmed) is the step's own, in its own words. The policy step is
// not among them: the policy stage (init_policy_migrate.go) is that migration,
// registered for the same checkout, and running it twice would migrate one
// node twice.
//
// The stage is skipped, not failed, where there is no checkout to work on: a
// `fugaro update` from a directory that is not one, or a checkout Fugaro is
// not set up in, has no pin to move.

// checkoutSteps are fugaro upgrade's steps for the checkout, in upgrade's own
// order, minus the policy step the converge already runs as its own stage.
var checkoutSteps = []upgradeStep{{"pin", pinStep}, {"plugin", pluginStep}, {"cloud", cloudStep}}

type checkoutStage struct{ e *initEngine }

func newCheckoutStage(e *initEngine) *checkoutStage { return &checkoutStage{e: e} }

func (*checkoutStage) Name() string { return initflow.Checkout }

// SelfConfirming: the steps keep their own confirmations — the cloud step's
// per-step and per-build ones, and the pin's diff — exactly as they do under
// fugaro upgrade, which is what lets the stage run where the loop would
// otherwise ask for the run's own typed name.
func (*checkoutStage) SelfConfirming() {}

// LocalUnderAgent: in a coding agent's session the loop applies this stage
// instead of leaving it for the user (initflow.LocalUnderAgent), because every
// write it makes is on this machine — the checkout's .claude/settings.json and
// the plugin in the user's own Claude Code — and those are the two writes the
// user's own session makes with fugaro upgrade, on files it reads anyway. What
// reaches outside the machine the stage refuses itself: under a marker its plan
// drops the cloud step, with that step's own sentence (cloudAgentRefusal), and
// the step refuses the marker again if it is ever reached another way. So the
// pin and the plugin move in an agent's session, --yes or not, and nothing else
// does.
func (*checkoutStage) LocalUnderAgent() {}

func (s *checkoutStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Checkout, Kind: initflow.LeftCommand, Text: selfCommand() + " upgrade"}
}

// checkout is the checkout this stage works on, and the one line saying why
// it has none.
func (s *checkoutStage) checkout() (pluginwire.Location, string) {
	loc, ok := pluginwire.Locate(".")
	if !ok {
		return pluginwire.Location{}, "not in a checkout: the checkout's own steps (the plugin pin, the plugin in Claude Code, the job images) run where the checkout is"
	}
	if releaseVersion() == "" {
		return loc, "a development build has no release to pin, install or refresh to"
	}
	if !fugaroCheckout(loc) {
		return loc, "not a Fugaro checkout: it has no fugaro.yaml and its .claude/settings.json does not wire the Fugaro plugin (set it up with fugaro setup and /fugaro:setup first)"
	}
	return loc, ""
}

// plannedStep is one of the checkout's steps in this run: the step, and the
// line saying why it is skipped here when it is.
type plannedStep struct {
	step upgradeStep
	skip string
}

// planFor is the steps this checkout's run has, and the steps it does not:
// three reasons drop the cloud step, and none of them is a skip over a
// failure. A coding agent's session leaves the cloud out (U7: the pin and the
// plugin move there, the images do not — what fugaro upgrade --local does in
// the same session), the step never having been reached to say it itself
// because the loop applied this stage for its local half. A checkout with no
// origin remote has no repository for the refresh to resolve — a skip here,
// not the failure fugaro upgrade makes of that read, because there the user
// asked for the refresh by name and here the step is one of a run. A checkout
// whose fugaro.yaml names another project is not this run's to touch.
//
// Everything else is the steps' own: every other reason the refresh skips (no
// fugaro.yaml, a teammate checkout, one not onboarded, a development build, a
// custom base) is cloudCheck's verdict and cloudStep says it, and every other
// reason it fails — an origin that is not owner/name, a checkout git cannot
// read — is a failure here too, not a skip.
func (s *checkoutStage) planFor(ctx context.Context, env initflow.Env, loc pluginwire.Location) ([]plannedStep, error) {
	u := s.upgradeCtx(env, loc)
	pinSkip, cloudSkip := "", ""
	switch other, err := s.namesAnotherProject(loc); {
	case err != nil:
		return nil, err
	case other != "":
		pinSkip, cloudSkip = other, other
	case u.agent != "":
		cloudSkip = cloudAgentRefusal(u)
	default:
		origin, err := hasOriginRemote(ctx, loc.Root)
		if err != nil {
			return nil, fmt.Errorf("reading the checkout's remotes: %w", err)
		}
		if !origin {
			cloudSkip = "this checkout has no origin repository to refresh"
		}
	}
	steps := make([]plannedStep, 0, len(checkoutSteps))
	for _, step := range checkoutSteps {
		p := plannedStep{step: step}
		switch step.name {
		case "pin":
			p.skip = pinSkip
		case "cloud":
			p.skip = cloudSkip
		}
		steps = append(steps, p)
	}
	return steps, nil
}

// namesAnotherProject is why this checkout is not the one this run is for: its
// fugaro.yaml names a project that is not this run's, or none at all. The
// plugin stage gates on exactly this (init_plugin_stage.go), for the same
// reason: a checkout of another project is not touched — its pin and its job
// images least of all. A fugaro.yaml that cannot be read is not a pass but a
// failure: unknown is not the same as matching, and it could name any project.
func (s *checkoutStage) namesAnotherProject(loc pluginwire.Location) (string, error) {
	data, err := readFugaroYAML(filepath.Join(loc.Root, "fugaro.yaml"))
	switch {
	case err == nil:
		if p, perr := config.ProjectOf(data); perr != nil || p != s.e.lc.Name {
			return "this checkout's fugaro.yaml names another project (or none), not " + pluginwire.Printable(s.e.lc.Name) + ": its plugin pin and its job images are not moved here", nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("this checkout's fugaro.yaml cannot be read, so which project it names is unknown and its plugin pin and its job images are not moved: %s", oneLine(err.Error()))
	}
	return "", nil
}

// hasOriginRemote: whether the checkout has an origin remote at all. It is the
// one case where the cloud step is dropped here: the refresh resolves the
// repository from the origin, and a checkout with none has nothing to resolve.
// A git read that fails is not that case and is an error, as it is under
// fugaro upgrade: the step is dropped only for a checkout that answers, and
// answers that it has no origin.
func hasOriginRemote(ctx context.Context, root string) (bool, error) {
	out, err := gitCmd(ctx, root, "remote").Output()
	if err != nil {
		return false, err
	}
	return slices.Contains(strings.Fields(string(out)), "origin"), nil
}

// detail is the stage's one line, in both the read-only view and the plan: the
// steps it runs, and the one it does not.
func (s *checkoutStage) detail(steps []plannedStep, loc pluginwire.Location) string {
	names, skipped := make([]string, 0, len(steps)), make([]string, 0, len(steps))
	for _, p := range steps {
		if p.skip != "" {
			skipped = append(skipped, p.step.name+" ("+p.skip+")")
			continue
		}
		names = append(names, p.step.name)
	}
	line := fmt.Sprintf("runs fugaro upgrade's own steps for %s: %s", pluginwire.Printable(loc.Root), strings.Join(names, ", "))
	if len(skipped) > 0 {
		line += "; skipped here: " + strings.Join(skipped, "; ")
	}
	return line
}

// Check answers whether this checkout has anything to do, by running the steps
// in their check mode: the same mode fugaro upgrade --check uses (u.o.check),
// which reads and decides and never writes, never asks and never calls the
// cloud. Every step at this release is a checkout already at it, and a second
// fugaro update then writes nothing, builds nothing and needs no terminal.
// Anything else is something for the apply, which reports it in the step's own
// words — and a step that fails in its check mode fails the stage here, at the
// same point the apply would.
func (s *checkoutStage) Check(ctx context.Context) (initflow.Status, error) {
	loc, why := s.checkout()
	if why != "" {
		return initflow.Status{State: initflow.Skipped, Detail: why}, nil
	}
	steps, err := s.planFor(ctx, initflow.Env{}, loc)
	if err != nil {
		return initflow.Status{}, err
	}
	var current []string
	todo := false
	for _, p := range steps {
		if p.skip != "" {
			// Not this checkout's step at all: nothing to do about it here.
			continue
		}
		// Every step is checked, not just up to the first with something to
		// do: a step that fails here fails the run before any of them
		// writes, where the pin would otherwise have moved before the cloud
		// step was read.
		switch r := s.checkStep(ctx, p.step, loc); r.state {
		case stepFailed:
			return initflow.Status{}, errors.New(r.reason)
		case stepCurrent:
			current = append(current, p.step.name)
		default:
			todo = true
		}
	}
	if todo || len(current) == 0 {
		return initflow.Status{State: initflow.Todo, Detail: s.detail(steps, loc)}, nil
	}
	return initflow.Status{State: initflow.Done, Detail: "up to date: " + strings.Join(current, ", ")}, nil
}

// checkStep is one step of the sequence in its check mode: read-only, no
// prompt, and quiet. The notes and warnings a check prints — the fork warning,
// the caveat on the deployed job's config copy — belong to the apply that
// really runs the step, not to a look at whether there is anything to do.
func (s *checkoutStage) checkStep(ctx context.Context, step upgradeStep, loc pluginwire.Location) stepResult {
	u := s.upgradeCtx(initflow.Env{}, loc)
	u.o.check = true
	u.w = io.Discard
	c := *s.e.r.cmd // the run's command, its output silenced: the steps' side notes go there
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetContext(ctx)
	u.cmd = &c
	return step.run(ctx, u)
}

func (s *checkoutStage) Plan(ctx context.Context, env initflow.Env) (initflow.Plan, error) {
	loc, why := s.checkout()
	if why != "" {
		return initflow.Plan{NothingToDo: true, Detail: why}, nil
	}
	steps, err := s.planFor(ctx, env, loc)
	if err != nil {
		return initflow.Plan{}, err
	}
	return initflow.Plan{Detail: s.detail(steps, loc)}, nil
}

// Apply runs the steps in upgrade's order and stops at the first that fails,
// as upgrade stops that checkout. Its outcome is the steps' own: something
// changed, or everything was already current or skipped.
func (s *checkoutStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	loc, why := s.checkout()
	if why != "" {
		return initflow.Outcome{Detail: why}, nil
	}
	steps, err := s.planFor(ctx, env, loc)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if !env.Yes && !env.Interactive {
		// The cloud step asks before every change, each billable build
		// included, and there is no terminal here to ask at; the stage
		// leaves the whole checkout for the user, which is what fugaro
		// upgrade itself does without a terminal.
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	// The refresh reads its context off the command (image_refresh.go), and the
	// loop's own context is the run's: the steps stop when it does. The command
	// belongs to the run, not to this stage, so the context it came with goes
	// back when the stage is done: a later stage reads what it would have read
	// had this one never run.
	prev := s.e.r.cmd.Context()
	s.e.r.cmd.SetContext(ctx)
	defer s.e.r.cmd.SetContext(prev)
	u := s.upgradeCtx(env, loc)
	var changed []string
	for _, p := range steps {
		r := stepResult{name: p.step.name, state: stepSkipped, reason: p.skip}
		if p.skip == "" {
			r = p.step.run(ctx, u)
			r.name = p.step.name
		}
		printStep(s.e.r.w, r)
		if r.state == stepFailed {
			return initflow.Outcome{}, errors.New(r.reason)
		}
		if r.state == stepDone {
			changed = append(changed, r.name)
		}
	}
	if len(changed) == 0 {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	return initflow.Outcome{Changed: true, Detail: "ran fugaro upgrade's " + strings.Join(changed, ", ") + " for this checkout"}, nil
}

func (*checkoutStage) Verify(context.Context) error { return nil }

// upgradeCtx is what upgrade's steps see for this checkout: the converge's own
// command and output, the flags the converge carries that upgrade has too, and
// the marker of the coding agent's session this run is in — the run's own
// options().Agent, and not a second read of the environment, so that the
// stage and the loop that drives it answer the marker the same way. Under a
// marker the cloud step refuses before it reads anything, so the pin and the
// plugin move there and the images do not: what `fugaro upgrade --local` does
// in the same session.
func (s *checkoutStage) upgradeCtx(env initflow.Env, loc pluginwire.Location) *upgradeCtx {
	o := s.e.r.o
	return &upgradeCtx{
		cmd: s.e.r.cmd,
		w:   s.e.r.w,
		o: upgradeOptions{yes: env.Yes, allowFork: o.allowFork, refresh: refreshOptions{
			imageSource: o.imageSource, expectDigests: o.expectDigests, cloud: o.cloud, in: s.e.r.in}},
		loc:   loc,
		agent: s.e.options().Agent,
	}
}
