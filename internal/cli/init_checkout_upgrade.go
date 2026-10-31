package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// checkoutUpgradeStage runs `fugaro upgrade`'s checkout steps inside `fugaro
// update`: the plugin pin in the checkout's .claude/settings.json, the plugin
// in Claude Code, and the repository's job images. It calls the steps
// themselves — pinStep, pluginStep and cloudStep, the very functions `fugaro
// upgrade` runs — rather than a copy of them, so the two front doors of one
// upgrade cannot drift into two upgrades.
//
// It exists for the owner's rule of 2026-10-30: after `brew upgrade`,
// `fugaro update` alone brings the installation AND the repository to the new
// version, so the pin, the installed plugin and the job images may no longer be
// left to a second command. Outside a Fugaro checkout there is nothing here to
// bring up, and the stage is skipped with one line saying so.
//
// Every refusal the steps have stays theirs: the pin is never committed and
// never moves down, a coding agent's session applies no cloud change, and the
// cloud step's billable build keeps its own confirmation at a real terminal
// (or --yes). Nothing here weakens any of them.
type checkoutUpgradeStage struct {
	e     *initEngine
	loc   pluginwire.Location
	found bool
	skip  string
	ran   []stepResult
}

func newCheckoutUpgradeStage(e *initEngine) *checkoutUpgradeStage {
	return &checkoutUpgradeStage{e: e}
}

func (checkoutUpgradeStage) Name() string    { return initflow.Checkout }
func (checkoutUpgradeStage) SelfConfirming() {}

// Left is what the user does when this stage cannot finish where it is: the
// same three steps, run from the checkout itself.
func (checkoutUpgradeStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Checkout, Kind: initflow.LeftCommand, Text: selfCommand() + " upgrade"}
}

// checkoutUpgradeSteps are the three of the five upgrade steps that are the
// checkout's and not the installation's. The fourth, policy, is this converge's
// own Policy stage, and running it here would take it twice.
var checkoutUpgradeSteps = []upgradeStep{{"pin", pinStep}, {"plugin", pluginStep}, {"cloud", cloudStep}}

// locate resolves the checkout once, by the rule `fugaro upgrade` uses for a
// path: a git checkout Fugaro is set up in, or is being set up in. skip is the
// one line that says why there is nothing to do here when there is not.
func (s *checkoutUpgradeStage) locate() bool {
	if s.found {
		return true
	}
	if releaseVersion() == "" {
		s.skip = "a development build has no release to pin, install or refresh to"
		return false
	}
	loc, ok := pluginwire.Locate(".")
	if !ok || !fugaroCheckout(loc) {
		s.skip = "not in a git checkout Fugaro is set up in, so this checkout has no plugin pin, no plugin install and no job image to bring up (fugaro upgrade runs the same steps in a checkout of yours)"
		return false
	}
	s.loc, s.found = loc, true
	return true
}

func (s *checkoutUpgradeStage) Check(context.Context) (initflow.Status, error) {
	if !s.locate() {
		return initflow.Status{State: initflow.Skipped, Detail: s.skip}, nil
	}
	return initflow.Status{State: initflow.Todo, Detail: "the checkout's plugin pin, the installed plugin and the repository's job images"}, nil
}

// Plan names what the stage is about to do, the billable build included: the
// build takes its own confirmation inside the cloud step, and the review screen
// says so here rather than promising a run that then stops to ask.
func (s *checkoutUpgradeStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	if !s.locate() {
		return initflow.Plan{NothingToDo: true, Detail: s.skip}, nil
	}
	return initflow.Plan{Detail: "pins the plugin in the checkout, installs or updates it in Claude Code, and refreshes the repository's job images (a billable build asks its own confirmation)"}, nil
}

// Apply runs the three steps in `fugaro upgrade`'s order, stopping at the
// first that fails, so the checkout is left exactly where `fugaro upgrade`
// would leave it. The loop's env is what confirms: --yes is the run's, and the
// cloud step's own confirmations are asked where they are taken.
func (s *checkoutUpgradeStage) Apply(ctx context.Context, env initflow.Env) (initflow.Outcome, error) {
	if !s.locate() {
		return initflow.Outcome{Detail: s.skip}, nil
	}
	u := s.upgradeCtx(env)
	s.ran = nil
	var done []string
	for _, st := range checkoutUpgradeSteps {
		r := st.run(ctx, u)
		r.name = st.name
		s.ran = append(s.ran, r)
		printStep(s.e.r.w, r)
		if r.state == stepFailed {
			return initflow.Outcome{}, &initflow.StageError{
				Err: fmt.Errorf("the %s step stopped: %s", r.name, r.reason),
				Fix: "fix what the line above names, then rerun " + selfCommand() + " update",
			}
		}
		if r.state == stepDone {
			done = append(done, r.name)
		}
	}
	if len(done) == 0 {
		return initflow.Outcome{Detail: "No changes"}, nil
	}
	return initflow.Outcome{Changed: true, Detail: "brought up: " + strings.Join(done, ", ")}, nil
}

// Verify reads back what the pin step claims: a settings file the run wrote and
// did not land would be rewritten by every later update.
func (s *checkoutUpgradeStage) Verify(context.Context) error {
	for _, r := range s.ran {
		if r.name == "pin" && r.state == stepDone {
			if _, err := pluginwire.Tag(Version); err != nil {
				return err
			}
			if st := pluginwire.Status(s.loc.Settings, Version, ""); st.Pin != pluginwire.OK {
				return errors.New("the checkout's plugin pin was written and does not read back at this release")
			}
		}
	}
	return nil
}

// upgradeCtx is the run the steps see: the flags the two commands share, and
// the environment marker, which is what keeps a coding agent's session from
// applying a cloud change here exactly as it does under `fugaro upgrade`.
func (s *checkoutUpgradeStage) upgradeCtx(env initflow.Env) *upgradeCtx {
	o := s.e.r.o
	return &upgradeCtx{
		cmd: s.e.r.cmd, w: s.e.r.w,
		o:     upgradeOptions{yes: env.Yes, allowFork: o.allowFork, refresh: refreshOptions{cloud: o.cloud}},
		loc:   s.loc,
		agent: agentMarker(os.Getenv),
	}
}
