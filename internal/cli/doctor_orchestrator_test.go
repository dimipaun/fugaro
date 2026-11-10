package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The login unit on doctor's report (O9): the drift the update itself repairs is
// a check of the update class, so the person is told to update rather than
// handed a second command, and doctor measures — it never writes the file it
// reads.
func TestDoctorReportsAnOutOfDateUnitTemplate(t *testing.T) {
	unitHome(t)
	u := currentUnit(t)

	// A machine with no unit: nothing to report. A project that did not choose
	// the daemon is not a machine with something wrong on it.
	if c := doctorOrchestratorUnit(); c.ID != "orchestrator-unit" || !c.OK || c.Problem != "" || c.Fix != "" {
		t.Errorf("a machine with no login unit: %+v, want an ok check and no line", c)
	}

	// A unit this release rendered: current, so nothing to report either.
	path := unitFileAt(t, u.Text, time.Now())
	if c := doctorOrchestratorUnit(); !c.OK {
		t.Errorf("a unit this release rendered: %+v, want ok", c)
	}

	// The previous release's template: the drift, in the update class, with the
	// file named and nothing written.
	unitFileAt(t, previousTemplate, time.Now())
	c := doctorOrchestratorUnit()
	if c.OK || c.Severity != "warning" {
		t.Fatalf("an older template: %+v, want a warning", c)
	}
	if !strings.Contains(c.Problem, path) {
		t.Errorf("the check does not name the file: %q", c.Problem)
	}
	if got := doctorClassOf(c); got != doctorClassUpdate {
		t.Errorf("the check is classed %q, want %q: `fugaro update` re-renders the unit in place", got, doctorClassUpdate)
	}
	if line := doctorItemLine(t, c); !strings.HasSuffix(line, "run `fugaro update`") {
		t.Errorf("the line the default view prints is %q, want the class's one command", line)
	}
	if !strings.Contains(doctorShortProblem(c), "older template") {
		t.Errorf("the short problem is %q, want the one sentence about the template", doctorShortProblem(c))
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != previousTemplate {
		t.Errorf("doctor wrote the file it reads (err %v):\n%s", err, data)
	}

	// A platform with no login unit at all: nothing to report, and nothing
	// installed either (the same answer `fugaro orchestrator install` gives).
	old := orchestratorGOOS
	orchestratorGOOS = "windows"
	t.Cleanup(func() { orchestratorGOOS = old })
	if c := doctorOrchestratorUnit(); !c.OK {
		t.Errorf("a platform with no login unit: %+v, want ok", c)
	}
	orchestratorGOOS = old

	// A file that cannot be read is an unknown and never an ok: it is marked
	// unread, it is counted as a finding, and its own fix is what prints — a
	// command the update would fail at too is not the class's line.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	c = doctorOrchestratorUnit()
	if c.OK || !c.unread || c.Severity != "warning" {
		t.Fatalf("an unreadable unit: %+v, want a warning marked unread", c)
	}
	if !doctorIsFinding(c) {
		t.Error("an unreadable unit is not counted as a finding: an unknown beside \"everything checked is ok\" is the silent pass")
	}
	if line := doctorItemLine(t, c); !strings.Contains(line, "orchestrator install") {
		t.Errorf("the unread state's line is %q, want its own fix rather than the class's command", line)
	}
}
