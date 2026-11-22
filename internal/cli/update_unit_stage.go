package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The login unit's step in `fugaro update` (O9, T7's ruling of 2026-11-05, and
// D2700's install of a missing unit).
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
// It installs the unit a machine is missing, and that is D2700 (the owner's
// answer of 2026-11-16 in docs/orchestrator-graph-estimates/reconciliation.md,
// as corrected by F48): a machine without one — a teammate's machine, one that
// skipped `setup`, one whose unit was removed — gets it here, so the one command
// the user runs turns the daemon on everywhere the project's people work, with
// the per-project lease keeping one of those daemons live and the others on
// standby (F2). The write asks nothing (the no-prompt policy needs no
// confirmation where nothing is asked) and it is fenced by a person at the
// terminal, the same fence `setup`'s unit stage already uses: a run with no
// terminal, a coding agent's session and --plan-only write no unit and say so in
// one line, and the person's own next terminal `fugaro update` installs it. A
// machine with no launchd and no systemd user unit gets one line too, and the
// update goes on (unitStepRead's UnsupportedError arm).
//
// D2700 supersedes the stateless-orchestrator plan's T7/Q2 ruling that this door
// "never installs" a unit (F36 of that reconciliation); R2 of the
// orchestrator-graph-estimates plan amends T7's text and the tests that pin it,
// and the sentences that repeated the old ruling here, in `fugaro orchestrator
// install`'s help and in setup's unit stage went with it.
//
// The step's class is the strictest one — a unit is code that runs at login — so
// --yes never reaches it, a coding agent's session writes nothing (its sentence
// is the skip, and the drift stays visible through `fugaro doctor` until the
// person runs the command themselves), and a run with nobody at it writes
// nothing either: a re-render the loop cannot make is left for the user, with
// the left-for-you line naming the command to run where a person is, and the
// first unit on a machine the run could install is skipped in one line that says
// so (unitStepTodo). What it does NOT do is ask:
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
	_, st, err := unitStepRead(s.e, unitDoorUpdate)
	return st, err
}

func (s *updateUnitStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	_, st, err := unitStepRead(s.e, unitDoorUpdate)
	if err != nil {
		return initflow.Plan{}, err
	}
	return initflow.Plan{Detail: st.Detail, NothingToDo: st.State != initflow.Todo}, nil
}

func (s *updateUnitStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	u, st, err := unitStepRead(s.e, unitDoorUpdate)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if st.State != initflow.Todo {
		// Nothing to write: either the file is already this release's, or the
		// read skipped the write (no person at the terminal, a platform with no
		// unit, a coding agent's session, a build that must not name a login
		// unit).
		return initflow.Outcome{Detail: st.Detail}, nil
	}
	if !initflow.PersonAtTheTerminal(s.e.r.conditions()) {
		// A run nobody is at writes no unit: the loop leaves the step for the
		// user before it reaches here (a step --yes does not cover), and this
		// is the same answer for the modes the loop's own line does not catch.
		return initflow.Outcome{}, &initflow.NeedsYouError{Left: s.Left()}
	}
	_, installed, err := unitTextOnDisk(u)
	if err != nil {
		return initflow.Outcome{}, err
	}
	m, err := orchestratorService()
	if err != nil {
		return initflow.Outcome{}, userErr("%v", err)
	}
	fmt.Fprintf(s.e.r.w, "%s\n", st.Detail)
	// No name: `fugaro update` writes the project's own orchestrator's unit and
	// never touches a named one's (D3206).
	if err := installUnit(ctx, m, u, ""); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(s.e.r.w, "  no confirmation of its own: the unit is one line of the plan this update showed you, and it stays a file of your own under your home\n")
	if !installed {
		return initflow.Outcome{Changed: true, Detail: "installed " + safetext.Field(u.Path) + " and loaded it with " + u.Platform}, nil
	}
	return initflow.Outcome{Changed: true, Detail: "re-rendered " + safetext.Field(u.Path) + " and reloaded it with " + u.Platform}, nil
}

func (s *updateUnitStage) Verify(ctx context.Context) error { return verifyUnitOnDisk() }
