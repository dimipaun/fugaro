package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// detail is the stage's one line, in both the read-only view and the plan.
func (s *checkoutStage) detail(loc pluginwire.Location) string {
	return fmt.Sprintf("runs fugaro upgrade's own steps for %s: %s", pluginwire.Printable(loc.Root), checkoutStepNames())
}

func checkoutStepNames() string {
	names := make([]string, 0, len(checkoutSteps))
	for _, s := range checkoutSteps {
		names = append(names, s.name)
	}
	return strings.Join(names, ", ")
}

func (s *checkoutStage) Check(context.Context) (initflow.Status, error) {
	loc, why := s.checkout()
	if why != "" {
		return initflow.Status{State: initflow.Skipped, Detail: why}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: s.detail(loc)}, nil
}

func (s *checkoutStage) Plan(_ context.Context, _ initflow.Env) (initflow.Plan, error) {
	loc, why := s.checkout()
	if why != "" {
		return initflow.Plan{NothingToDo: true, Detail: why}, nil
	}
	return initflow.Plan{Detail: s.detail(loc)}, nil
}

// Apply runs the steps in upgrade's order and stops at the first that fails,
// as upgrade stops that checkout. Its outcome is the steps' own: something
// changed, or everything was already current or skipped.
func (s *checkoutStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	loc, why := s.checkout()
	if why != "" {
		return initflow.Outcome{Detail: why}, nil
	}
	if !env.Yes && !env.Interactive {
		// The cloud step asks before every change, each billable build
		// included, and there is no terminal here to ask at; the stage
		// leaves the whole checkout for the user, which is what fugaro
		// upgrade itself does without a terminal.
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	u := s.upgradeCtx(ctx, env, loc)
	var changed []string
	for _, step := range checkoutSteps {
		r := step.run(ctx, u)
		r.name = step.name
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
func (s *checkoutStage) upgradeCtx(ctx context.Context, env initflow.Env, loc pluginwire.Location) *upgradeCtx {
	o := s.e.r.o
	// The refresh reads its context off the command (image_refresh.go), and
	// the loop's own context is the run's: the steps stop when it does.
	s.e.r.cmd.SetContext(ctx)
	return &upgradeCtx{
		cmd: s.e.r.cmd,
		w:   s.e.r.w,
		o: upgradeOptions{yes: env.Yes, allowFork: o.allowFork, refresh: refreshOptions{
			imageSource: o.imageSource, expectDigests: o.expectDigests, cloud: o.cloud}},
		loc:   loc,
		agent: s.e.options().Agent,
	}
}
