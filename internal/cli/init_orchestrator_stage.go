package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/orchestrator/service"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The login unit's step in `fugaro setup` (O9, the owner's Q2 answer of
// 2026-11-04) and the read `fugaro update`'s step shares with it (D2700 of
// docs/orchestrator-graph-estimates, the owner's answer of 2026-11-16 as
// corrected).
//
// `fugaro setup` is where the first unit belongs: the daemon the unit starts
// drives the project this run is installing, and the machine that has just been
// set up is the machine that wants one. `fugaro update` installs the unit a
// machine is missing too — a machine that skipped `setup`, a teammate's machine,
// or one whose unit was removed — so that one command turns the daemon on
// everywhere the project's people work, and the per-project lease keeps exactly
// one of those daemons live (F2). The step renders the plan line every stage
// renders — the unit it writes and the path it writes it at — and calls the very
// same installer `fugaro orchestrator install` calls, so there is one renderer,
// one writer and one reload (orchestrator_install.go).
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
// step ends there, and the command that writes the unit by hand is named so a
// reader who wants the daemon started — or a unit already installed re-rendered —
// knows where to type. The update door names the person's own next update
// instead, because on that door it is the command that installs a missing unit
// (D2700's own words: the owner's next terminal `update` installs it).
func unitStepNoPerson(door unitDoor) string {
	by := selfCommand() + " orchestrator install writes one by hand"
	if door == unitDoorUpdate {
		by = "your next " + selfCommand() + " update at your own terminal installs it"
	}
	return "the login unit is written by a run you are watching at your own terminal: this one has nobody at it, so nothing is written (" + by + ")"
}

func (s *orchestratorUnitStage) Check(context.Context) (initflow.Status, error) {
	_, st, err := unitStepRead(s.e, unitDoorSetup)
	return st, err
}

// Plan has nothing of its own to add: the read is the plan — whether there is a
// unit, whether it is this release's, and where it goes — and the loop prints
// the check's line as the stage's detail.
func (s *orchestratorUnitStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	_, st, err := unitStepRead(s.e, unitDoorSetup)
	if err != nil {
		return initflow.Plan{}, err
	}
	return initflow.Plan{Detail: st.Detail, NothingToDo: st.State != initflow.Todo}, nil
}

func (s *orchestratorUnitStage) Apply(ctx context.Context, _ initflow.Env) (initflow.Outcome, error) {
	u, st, err := unitStepRead(s.e, unitDoorSetup)
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
	// No name: `fugaro setup` writes the project's own orchestrator's unit, and
	// never a named one (D3206).
	if err := installUnit(ctx, m, u, ""); err != nil {
		return initflow.Outcome{}, err
	}
	fmt.Fprintf(s.e.r.w, "  no confirmation of its own: the login unit is one line of the plan you confirmed with this run\n")
	return initflow.Outcome{Changed: true, Detail: "installed " + safetext.Field(u.Path) + " and loaded it with " + u.Platform}, nil
}

func (s *orchestratorUnitStage) Verify(ctx context.Context) error { return verifyUnitOnDisk() }

// unitDoor is which door's step is reading this machine: `fugaro setup`, which
// writes the machine's first unit (Q2, the owner's answer of 2026-11-04), and
// `fugaro update`, which installs a missing one and re-renders an installed one
// (D2700 of docs/orchestrator-graph-estimates, the owner's answer of 2026-11-16
// as corrected, which supersedes the stateless-orchestrator plan's T7 ruling
// that this door "never installs" one; R2 of that plan amends T7's text).
//
// The door decides two things and nothing else: the command a run with nobody at
// it leaves the person (unitStepNoPerson), and whether a write may name the
// program this run is (unitMayNameThisBinary, which only matters as a difference
// on the update door). Everything else about the read is the same on both.
type unitDoor uint8

const (
	unitDoorSetup unitDoor = iota
	unitDoorUpdate
)

// unitStepRead is the read both doors' stage makes: the unit this release would
// write, what is on disk, and the one status the two make of it.
//
// The order of the questions is the ruling's: a platform with no login unit
// ends the step before anything is read off the machine, and a coding agent's
// session ends it with its own sentence (AgentRefusal) — the drift stays visible
// through `fugaro doctor` and never through a write.
//
// A machine with no file is what both doors install: `setup` writes the first
// unit, and `fugaro update` installs the one a machine is missing (D2700) — a
// machine that skipped `setup`, a teammate's machine, one whose unit was
// removed. Neither asks anything, and both are fenced by a person at the
// terminal: a run with nobody at it writes no unit and says so in one line
// (unitStepTodo, unitStepNoPerson).
//
// A re-render the run cannot make is the one asymmetry left, and it is the
// rulings': the loop leaves it for the user — a step --yes never covers, in the
// shape every other such step has — so an unattended `fugaro update` reports the
// drift instead of quietly skipping it (unitStepTodo).
//
// The other difference is the program the write names. A unit's text is its
// template plus one binary path, so a file that differs from what this release
// renders differs by the template, by that path, or by both — and the path is
// the one this process is running, which is not always a fugaro a release
// installed. A unit that restarts its program forever (the template's
// keep-alive) must not be pointed at a program that will not be there tomorrow,
// so the update door writes a unit only when unitMayNameThisBinary accepts the
// program: that covers the re-point of an installed unit AND the first unit on a
// machine that has none, since an update run from a checkout's `go build`, from
// `go run`'s temporary binary or from a copy in a scratch directory would
// otherwise leave a login unit restarting a program that is already gone. The
// `setup` door keeps both writes whatever build it is: the person is at a
// terminal, the plan line names the path, and the run they confirmed is the run
// that asked for the unit.
func unitStepRead(e *initEngine, door unitDoor) (service.Unit, initflow.Status, error) {
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
	case !ok:
		if door == unitDoorUpdate {
			binary, err := orchestratorBinaryPath()
			if err != nil {
				return service.Unit{}, initflow.Status{}, err
			}
			if !unitMayNameThisBinary(binary) {
				return u, initflow.Status{State: initflow.Skipped, Detail: unitStepLeavesNoUnit(u, binary)}, nil
			}
		}
		return u, unitStepTodo(e, door, true, unitStepInstallDetail(u)), nil
	case was == u.Text:
		return u, initflow.Status{State: initflow.Done, Detail: "No changes: the login unit at " + safetext.Field(u.Path) +
			" is this release's template"}, nil
	}
	binary, err := orchestratorBinaryPath()
	if err != nil {
		return service.Unit{}, initflow.Status{}, err
	}
	if door == unitDoorUpdate && unitRepointsTheProgram(u, binary, was) && !unitMayNameThisBinary(binary) {
		return u, initflow.Status{State: initflow.Skipped, Detail: unitStepLeavesAnotherProgram(u, binary, was)}, nil
	}
	return u, unitStepTodo(e, door, false, unitStepRerenderDetail(u, binary, was)), nil
}

// unitStepInstallDetail is the plan line of the first unit on a machine that has
// none: what is written, where it goes, and what it does from then on.
func unitStepInstallDetail(u service.Unit) string {
	return "installs the login unit at " + safetext.Field(u.Path) + " (" + u.Platform +
		"): it starts the daemon at every login, restarts it when it stops, and the daemon drives this project's orchestrator until you uninstall it"
}

// unitRepointsTheProgram is whether writing this release's unit over the file on
// disk would change the program the unit runs: the one thing about a re-render
// that is not the release's to decide by itself.
func unitRepointsTheProgram(u service.Unit, binary, was string) bool {
	if old, ok := unitOtherProgramPath(u, binary, was); ok {
		return old != binary
	}
	// The file is not this release's template, so the path in it cannot be read
	// back out (below). The unit already runs this program when its text carries
	// the path, and a file that does not is one the write would re-point.
	return !strings.Contains(was, binary)
}

// unitOtherProgramPath is the binary path the file on disk names, when the file
// is this release's template rendered for a path other than binary. A unit's
// text is the template plus that one path, named twice, so the file is this
// release's template with its own path substituted and the path can be read back
// out of it: the head and the tail around the two occurrences are this
// release's, and what stands between them is the path, twice. ok is false for a
// file this release's template cannot account for — an older release's template,
// an edit by hand — where the path in it is not this code's to guess.
func unitOtherProgramPath(u service.Unit, binary, was string) (string, bool) {
	const probe = "\x00unit\x00"
	parts := strings.Split(strings.ReplaceAll(u.Text, binary, probe), probe)
	if len(parts) != 3 {
		return "", false
	}
	head, mid, tail := parts[0], parts[1], parts[2]
	if !strings.HasPrefix(was, head) || !strings.HasSuffix(was, tail) {
		return "", false
	}
	body := was[len(head) : len(was)-len(tail)]
	if len(body) < len(mid) || (len(body)-len(mid))%2 != 0 {
		return "", false
	}
	n := (len(body) - len(mid)) / 2
	if body[n:n+len(mid)] != mid || body[:n] != body[n+len(mid):] {
		return "", false
	}
	return body[:n], true
}

// unitStepRerenderDetail is the plan line of a re-render. It names what changed
// — the template, or the program the unit runs, both paths when it is the
// program — and the one property of the write a reader has to know: it stays
// under their own home.
func unitStepRerenderDetail(u service.Unit, binary, was string) string {
	what := "the template changed"
	if old, ok := unitOtherProgramPath(u, binary, was); ok {
		what = "it named " + safetext.Field(old) + " and now names " + safetext.Field(binary) + ", the fugaro this run is"
	}
	return "re-renders the login unit at " + safetext.Field(u.Path) + ": " + what +
		", and it is one file under your home — no system path, no root, no sudo and no network"
}

// unitStepLeavesAnotherProgram is the plan line of a re-render the update door
// will not make, because the write would point the login unit at the binary this
// run is and that is not a fugaro a release installed. Nothing is written, the
// two paths are named so the difference is not a mystery, and the command that
// points the unit at another program deliberately is named with them.
func unitStepLeavesAnotherProgram(u service.Unit, binary, was string) string {
	names := " names another program than this run"
	if old, ok := unitOtherProgramPath(u, binary, was); ok {
		names = " names " + safetext.Field(old)
	}
	return "leaves the login unit at " + safetext.Field(u.Path) + names + ", and this run is " + safetext.Field(binary) +
		" — a development build or a binary in a temporary directory, not a fugaro a release installed: " +
		"nothing was re-rendered here, and `" + selfCommand() + " orchestrator install` is the command that points the unit at another program"
}

// unitStepLeavesNoUnit is the plan line of a machine with NO unit, when the run
// is not a fugaro a release installed: the write the update door would make is
// the same one unitStepLeavesAnotherProgram refuses, one step earlier — the unit
// it would write would restart this program forever. Nothing is written, the
// binary this run is and the command that writes the first unit deliberately are
// both named, and the machine is left exactly as it was for a released fugaro's
// own update to install from.
func unitStepLeavesNoUnit(u service.Unit, binary string) string {
	return "leaves this machine without the login unit at " + safetext.Field(u.Path) + ": this run is " + safetext.Field(binary) +
		" — a development build or a binary in a temporary directory, not a fugaro a release installed, and a login unit restarts its program at every login: " +
		"nothing was written here, and `" + selfCommand() + " orchestrator install` writes the first one deliberately"
}

// unitMayNameThisBinary is whether a login unit may be pointed at the binary
// this process is running: a fugaro a release installed, in a place that is not
// the system's temporary directory. A unit runs its program at every login and
// restarts it whenever it stops (the template's keep-alive), so one pointed at a
// checkout's `go build`, at `go run`'s temporary binary, or at a copy in a
// scratch directory would restart a program that is not there tomorrow — and
// both doors compare the file's text, so nothing would repair it afterwards
// either.
func unitMayNameThisBinary(binary string) bool {
	temp := filepath.Clean(os.TempDir()) + string(filepath.Separator)
	return Version != "dev" && !strings.HasPrefix(filepath.Clean(binary), temp)
}

// unitStepTodo is the read's answer when the step has something to do, under the
// one rule that keeps a unit out of a login nobody asked for: the write happens
// only in a run a person is at. The first unit on a machine — `setup`'s and
// `fugaro update`'s alike — ends there when nobody is (Skipped, in one line
// naming the command that writes it where a person is), and a re-render the loop
// can leave for the user is a Todo — the loop turns it into needs-you, which is
// what a stage --yes does not cover looks like in every other run.
func unitStepTodo(e *initEngine, door unitDoor, needsPerson bool, detail string) initflow.Status {
	if needsPerson && !initflow.PersonAtTheTerminal(e.r.conditions()) {
		return initflow.Status{State: initflow.Skipped, Detail: unitStepNoPerson(door)}
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
