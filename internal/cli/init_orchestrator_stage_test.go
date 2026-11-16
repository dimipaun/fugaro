package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// The setup door's half of the login unit's step (O9). Everything here runs
// against a temporary home and the faked service manager
// (orchestrator_install_test.go), and every run is driven through the loop, so
// what is asserted is the run's own plan view, the loop's confirmation rules and
// the file the step leaves behind — not a stage method called by hand.

// runUnitStage drives one door's stage through the loop, which is how a run
// reaches it: the plan view it prints is the line the person reads, and the
// loop's own rules decide whether the step applies at all.
func runUnitStage(t *testing.T, e *initEngine, s initflow.Stage) (*initflow.Result, string) {
	t.Helper()
	res, err := initflow.Run(t.Context(), []initflow.Stage{s}, e.options())
	if err != nil {
		t.Fatalf("the loop refused the step: %v", err)
	}
	if res.Failed != nil {
		t.Fatalf("the step failed: %+v", res.Failed)
	}
	return res, e.r.w.(*syncBuf).String()
}

// askedSomething is whether a run printed a question: the harness's own reading
// of one (noprompt_harness_test.go), which a step of this class may never trip.
func askedSomething(out string) bool { return closedStdinQuestion.MatchString(out) }

func TestSetupInstallsTheUnitAsAPlanStep(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := stageEngine(t, "", &initOptions{door: setupDoor.name})
	res, out := runUnitStage(t, e, newOrchestratorUnitStage(e))
	u := currentUnit(t)
	if got := stateOf(res, initflow.Unit); got != initflow.Changed {
		t.Fatalf("the step is %s, want changed:\n%+v", got, res.Stages)
	}
	// The plan the person read names the unit and where it is written: the plan
	// view's row is the stage's own check line, printed before anything was
	// applied — which is what "the plan line is the whole of what it shows"
	// means for a step with no confirmation of its own.
	if !strings.Contains(out, "installs the login unit at "+u.Path) {
		t.Errorf("the run's plan does not name the unit and its path:\n%s", out)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "loaded it with") {
		t.Errorf("the stage's own line after the write is %q", d)
	}
	// The same installer `fugaro orchestrator install` calls: the file this
	// release renders, written under the person's home, and the one reload.
	data, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatalf("the unit was not written: %v", err)
	}
	if string(data) != u.Text {
		t.Errorf("%s is not what this release renders:\n--- on disk\n%s\n--- rendered\n%s", u.Path, data, u.Text)
	}
	if len(f.reloads) != 1 || f.reloads[0].Path != u.Path {
		t.Errorf("the service manager was reloaded with %v, want one call for %s", f.reloads, u.Path)
	}
}

// TestSetupInstallsInAManagedProjectWithoutAPrompt: a managed project's setup
// reaches the step with no confirmation of its own. The gate is the rigs' own
// shape for a managed run, and what the test pins is the answer the ruling asks
// for — the step asks nothing anywhere, the plan line is the whole of what the
// person reads, and the write happens.
func TestSetupInstallsInAManagedProjectWithoutAPrompt(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := stageEngine(t, "", &initOptions{door: setupDoor.name})
	e.r.gate = &initGate{managed: true}
	res, out := runUnitStage(t, e, newOrchestratorUnitStage(e))
	u := currentUnit(t)
	if got := stateOf(res, initflow.Unit); got != initflow.Changed {
		t.Fatalf("the step is %s, want changed:\n%+v", got, res.Stages)
	}
	if askedSomething(out) {
		t.Errorf("the step asked a question in a managed project:\n%s", out)
	}
	if _, err := os.Stat(u.Path); err != nil {
		t.Errorf("the unit was not written: %v", err)
	}
	if len(f.reloads) != 1 {
		t.Errorf("the unit was reloaded %d times, want once", len(f.reloads))
	}
}

// TestSetupNeverInstallsUnderAnAgentMarker: an agent session's setup runs to
// completion and writes no unit, and the step is the one that says why — the
// marker's own sentence, which is what an agent reads and passes on.
func TestSetupNeverInstallsUnderAnAgentMarker(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	e, _ := stageEngine(t, "", &initOptions{door: setupDoor.name})
	t.Setenv("CLAUDECODE", "1")
	res, out := runUnitStage(t, e, newOrchestratorUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
		t.Fatalf("the step is %s, want skipped:\n%+v", got, res.Stages)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "CLAUDECODE") {
		t.Errorf("the skip does not carry the marker's own sentence: %q", d)
	}
	if askedSomething(out) {
		t.Errorf("an agent's run asked a question:\n%s", out)
	}
	u := currentUnit(t)
	if _, err := os.Stat(u.Path); !os.IsNotExist(err) {
		t.Errorf("an agent's run wrote %s (stat: %v)", u.Path, err)
	}
	if len(f.reloads) != 0 || len(f.removes) != 0 {
		t.Errorf("an agent's run reached the service manager: %v %v", f.reloads, f.removes)
	}
}

// TestSetupWritesNoUnitWithNobodyAtTheTerminal is the other half of the same
// rule: a run nobody is at — a pipe, --json, --non-interactive — writes no unit
// either, and says where the command that does is.
func TestSetupWritesNoUnitWithNobodyAtTheTerminal(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	noTerminal(t)
	e, _ := stageEngine(t, "", &initOptions{door: setupDoor.name, yes: true, asJSON: true})
	res, _ := runUnitStage(t, e, newOrchestratorUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Skipped {
		t.Fatalf("the step is %s, want skipped:\n%+v", got, res.Stages)
	}
	if d := res.Stages[0].Detail; !strings.Contains(d, "nobody at it") {
		t.Errorf("the skip does not say why: %q", d)
	}
	if len(res.Left) != 0 {
		t.Errorf("the step left something for the user: %v", res.Left)
	}
	if _, err := os.Stat(currentUnit(t).Path); !os.IsNotExist(err) {
		t.Errorf("a run nobody was at wrote a unit (stat: %v)", err)
	}
	if len(f.reloads) != 0 {
		t.Errorf("a run nobody was at reloaded the service: %v", f.reloads)
	}
}

// TestSetupRerendersAUnitFromAnOlderTemplate: the step is the same installer
// `fugaro orchestrator install` calls, so a machine that already has a unit
// written by an older release converges the same way the command does.
func TestSetupRerendersAUnitFromAnOlderTemplate(t *testing.T) {
	unitHome(t)
	f := fakeUnitService(t)
	typed(t)
	u := currentUnit(t)
	if err := os.MkdirAll(filepath.Dir(u.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.Path, []byte("[Unit]\nDescription=Fugaro orchestrator (0.20.0)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := stageEngine(t, "", &initOptions{door: setupDoor.name})
	res, out := runUnitStage(t, e, newOrchestratorUnitStage(e))
	if got := stateOf(res, initflow.Unit); got != initflow.Changed {
		t.Fatalf("the step is %s, want changed:\n%+v", got, res.Stages)
	}
	data, err := os.ReadFile(u.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != u.Text {
		t.Errorf("the older unit was not re-rendered:\n--- on disk\n%s\n--- rendered\n%s", data, u.Text)
	}
	if !strings.Contains(out, "changed") {
		t.Errorf("the run does not say why it re-rendered:\n%s", out)
	}
	if len(f.reloads) != 1 {
		t.Errorf("the service was reloaded %d times, want once", len(f.reloads))
	}
}
