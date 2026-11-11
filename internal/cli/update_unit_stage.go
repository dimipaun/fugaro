package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The login unit's step in `fugaro update` (O9, T7's ruling of 2026-11-05).
//
// It is what makes the HARD RULE literally true: `fugaro update` is the whole
// upgrade, so a release that changes the unit's template re-renders the unit
// already installed on this machine rather than asking for a second command. It
// detects the unit at the platform's own login-unit path, compares what it would
// render now with the file on disk through the same renderer `install` uses (no
// second template, no stamp to drift), writes the file when the two differ and
// leaves a current one byte-for-byte alone, and reloads the login service so the
// daemon picks it up — `installUnit`, the one installer, called exactly as
// `fugaro orchestrator install` calls it.
//
// It never installs a unit: a machine without one is a project that did not
// choose the daemon or a person who skipped `setup`, and the read ends the step
// there with the command that writes one.
//
// The step's class is the strictest one — a unit is code that runs at login — so
// --yes never reaches it, a coding agent's session writes nothing (its sentence
// is the skip, and the drift stays visible through `fugaro doctor` until the
// person runs the command themselves), and a run with nobody at it writes
// nothing either: the loop leaves the step for the user, and the left-for-you
// line names the command to run where a person is. What it does NOT do is ask:
// the plan line naming the file it re-renders and where it lives is a line of
// the plan this update showed at the person's own terminal before it applied
// anything, and re-rendering a unit the person installed themselves — from the
// same template, naming the same stable binary — is not a new write of code
// into their login. An extra confirmation here would be exactly the operator
// step T7 exists to remove.
//
// That last sentence is not this file's reading: it is the owner's ruling of
// 2026-11-10 (T8 — `fugaro update` stays the whole upgrade with no extra
// confirmation prompt), recorded with its date in
// docs/stateless-orchestrator/reconciliation.md, and it supersedes the typed
// ask T7's own paragraph had put on this step. The pin is
// TestDocsStatelessOrchestratorUnitRerenderAsksNothing, the behavior is
// TestUpdateRerendersAnInstalledUnitWhenTheTemplateChanged, and the reversal is
// one place — Apply, just below.
type updateUnitStage struct{ e *initEngine }

func newUpdateUnitStage(e *initEngine) *updateUnitStage { return &updateUnitStage{e: e} }

func (*updateUnitStage) Name() string { return initflow.Unit }

func (*updateUnitStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Unit, Kind: initflow.LeftCommand,
		Text: selfCommand() + " update"}
}

func (s *updateUnitStage) Check(context.Context) (initflow.Status, error) {
	_, st, err := unitStepRead(s.e, unitStepRerendersOnly)
	return st, err
}

func (s *updateUnitStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	_, st, err := unitStepRead(s.e, unitStepRerendersOnly)
	if err != nil {
		return initflow.Plan{}, err
	}
	return initflow.Plan{Detail: st.Detail, NothingToDo: st.State != initflow.Todo}, nil
}

func (s *updateUnitStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	u, st, err := unitStepRead(s.e, unitStepRerendersOnly)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if st.State != initflow.Todo {
		// Nothing to re-render: either the file is already this release's, or
		// there is no unit on this machine at all.
		return initflow.Outcome{Detail: st.Detail}, nil
	}
	if !initflow.PersonAtTheTerminal(s.e.r.conditions()) {
		// A run nobody is at writes no unit: the loop leaves the step for the
		// user before it reaches here (a step --yes does not cover), and this
		// is the same answer for the modes the loop's own line does not catch.
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	m, err := orchestratorService()
	if err != nil {
		return initflow.Outcome{}, userErr("%v", err)
	}
	fmt.Fprintf(s.e.r.w, "%s\n", st.Detail)
	if err := installUnit(ctx, m, u); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(s.e.r.w, "  no confirmation of its own: the unit is one line of the plan this update showed you, and it stays the unit you installed\n")
	return initflow.Outcome{Changed: true, Detail: "re-rendered " + safetext.Field(u.Path) + " and reloaded it with " + u.Platform}, nil
}

func (s *updateUnitStage) Verify(ctx context.Context) error { return verifyUnitOnDisk() }
