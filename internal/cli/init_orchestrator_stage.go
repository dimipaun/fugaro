package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/orchestrator/service"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The login unit's step in `fugaro setup` (O9, the owner's Q2 answer of
// 2026-11-04).
//
// `fugaro setup` is where the first unit belongs: the daemon the unit starts
// drives the project this run is installing, and the machine that has just been
// set up is the machine that wants one. The step renders the plan line every
// stage renders — the unit it writes and the path it writes it at — and calls
// the very same installer `fugaro orchestrator install` calls, so there is one
// renderer, one writer and one reload (orchestrator_install.go).
//
// It has no confirmation of its own, and that is the whole of the ruling: the
// plan line is a line of the plan the person read and confirmed at their own
// terminal before the run applied anything, and a unit is not a new privilege
// on that machine — it runs `fugaro orchestrator run`, which drives the project
// the same run just installed. What the step may not do is write a unit nobody
// asked for, so it is a real terminal only: a run with no person at it (a pipe,
// --json, --non-interactive, or a coding agent's session, whose own sentence the
// step skips with) writes no unit, and `fugaro orchestrator install` is the
// command that writes one by hand.

type orchestratorUnitStage struct{ e *initEngine }

func newOrchestratorUnitStage(e *initEngine) *orchestratorUnitStage {
	return &orchestratorUnitStage{e: e}
}

func (*orchestratorUnitStage) Name() string { return initflow.Unit }

func (*orchestratorUnitStage) Left() initflow.Left {
	return initflow.Left{Stage: initflow.Unit, Kind: initflow.LeftCommand, Text: selfCommand() + " orchestrator install"}
}

// unitStepNoPerson is the line a run with nobody at it leaves in the plan: the
// step ends there, and the command that does it by hand is named so a reader who
// wants the daemon started —
// or a unit already installed re-rendered — knows where to type.
func unitStepNoPerson() string {
	return "the login unit is written by a run you are watching at your own terminal: this one has nobody at it, so nothing is written (" +
		selfCommand() + " orchestrator install writes one by hand)"
}

func (s *orchestratorUnitStage) Check(context.Context) (initflow.Status, error) {
	_, st, err := unitStepRead(s.e, unitStepInstalls)
	return st, err
}

// Plan has nothing of its own to add: the read is the plan — whether there is a
// unit, whether it is this release's, and where it goes — and the loop prints
// the check's line as the stage's detail.
func (s *orchestratorUnitStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	_, st, err := unitStepRead(s.e, unitStepInstalls)
	if err != nil {
		return initflow.Plan{}, err
	}
	return initflow.Plan{Detail: st.Detail, NothingToDo: st.State != initflow.Todo}, nil
}

func (s *orchestratorUnitStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	u, st, err := unitStepRead(s.e, unitStepInstalls)
	if err != nil {
		return initflow.Outcome{}, err
	}
	if st.State != initflow.Todo {
		// The read is repeated here, so this is a machine that changed under the
		// run: the file is already this release's, or the platform has no unit.
		return initflow.Outcome{Detail: st.Detail}, nil
	}
	m, err := orchestratorService()
	if err != nil {
		return initflow.Outcome{}, userErr("%v", err)
	}
	fmt.Fprintf(s.e.r.w, "%s\n", st.Detail)
	if err := installUnit(ctx, m, u); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(s.e.r.w, "  no confirmation of its own: the login unit is one line of the plan you confirmed with this run\n")
	return initflow.Outcome{Changed: true, Detail: "installed " + safetext.Field(u.Path) + " and loaded it with " + u.Platform}, nil
}

func (s *orchestratorUnitStage) Verify(ctx context.Context) error { return verifyUnitOnDisk() }

// unitStepInstalls and unitStepRerendersOnly are the two doors' answers to a
// machine with no unit on it: `setup` writes the first one, and `fugaro update`
// never installs one.
const (
	unitStepInstalls      = true
	unitStepRerendersOnly = false
)

// unitStepRead is the read both doors' stage makes: the unit this release would
// write, what is on disk, and the one status the two make of it.
//
// The order of the questions is the ruling's: a platform with no login unit
// ends the step before anything is read off the machine, and a coding agent's
// session ends it with its own sentence (AgentRefusal) — the drift stays visible
// through `fugaro doctor` and never through a write.
//
// The two doors differ in what a write is there: `setup` writes the first unit
// and `fugaro update` never installs one, so a machine with no file ends the
// step either way and only one of them has anything to do. They differ again in
// what a run with nobody at it does, which is the one asymmetry here and it is
// the rulings': the first install is a real terminal only (a `setup` a pipe
// started writes no unit, and says so), while a re-render the run cannot make is
// left for the user by the loop — a step --yes never covers, in the shape every
// other such step has — so an unattended `fugaro update` reports the drift
// instead of quietly skipping it (unitStepTodo).
func unitStepRead(e *initEngine, installs bool) (service.Unit, initflow.Status, error) {
	u, err := orchestratorUnit()
	if err != nil {
		var un *service.UnsupportedError
		if errors.As(err, &un) {
			return service.Unit{}, initflow.Status{State: initflow.Skipped, Detail: oneLineCLI(un.Error())}, nil
		}
		return service.Unit{}, initflow.Status{}, err
	}
	if m := agentMarker(os.Getenv, e.r.stdinTerminal()); m != "" {
		return u, initflow.Status{State: initflow.Skipped, Detail: initflow.AgentRefusal(m)}, nil
	}
	was, ok, err := unitTextOnDisk(u)
	if err != nil {
		return service.Unit{}, initflow.Status{}, err
	}
	switch {
	case !ok && !installs:
		return u, initflow.Status{State: initflow.Skipped, Detail: "no login unit is installed at " + safetext.Field(u.Path) +
			": " + selfCommand() + " update re-renders the daemon's unit on a machine that chose it and never installs one (" +
			selfCommand() + " orchestrator install writes the first one)"}, nil
	case !ok:
		return u, unitStepTodo(e, installs, "installs the login unit at "+safetext.Field(u.Path)+" ("+u.Platform+
			"): it starts the daemon at every login, restarts it when it stops, and the daemon drives this project's orchestrator until you uninstall it"), nil
	case was == u.Text:
		return u, initflow.Status{State: initflow.Done, Detail: "No changes: the login unit at " + safetext.Field(u.Path) +
			" is this release's template"}, nil
	}
	return u, unitStepTodo(e, installs, "re-renders the login unit at "+safetext.Field(u.Path)+
		": the template or the binary path changed, and it is one file under your home — no system path, no root, no sudo and no network"), nil
}

// unitStepTodo is the read's answer when the step has something to do, under the
// one rule that keeps a unit out of a login nobody asked for: the write happens
// only in a run a person is at. `setup`'s first install ends there when nobody
// is (Skipped, with the sentence that says so), and a re-render the loop can
// leave for the user is a Todo — the loop turns it into needs-you, which is what
// a stage --yes does not cover looks like in every other run.
func unitStepTodo(e *initEngine, installs bool, detail string) initflow.Status {
	if installs && !initflow.PersonAtTheTerminal(e.r.conditions()) {
		return initflow.Status{State: initflow.Skipped, Detail: unitStepNoPerson()}
	}
	return initflow.Status{State: initflow.Todo, Detail: detail}
}

// verifyUnitOnDisk is the loop's read-back after either door's write: the file
// on disk is what this release renders, which is also what the next run's
// comparison reads.
func verifyUnitOnDisk() error {
	u, err := orchestratorUnit()
	if err != nil {
		return err
	}
	was, ok, err := unitTextOnDisk(u)
	if err != nil {
		return err
	}
	if !ok || was != u.Text {
		return fmt.Errorf("the login unit at %s is not what this release renders after the write", safetext.Field(u.Path))
	}
	return nil
}
