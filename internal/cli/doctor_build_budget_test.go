package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// doctor_build_budget.go's tests (T4, D1548): the one check that says what
// bounds a billable image build now that the recorded count is gone (D1543) and
// no step of `image build` or `image refresh` asks a person anything.
//
// The check is a warning, not a failure, so these tests read it through the
// whole command (doctorChecksByID) rather than calling the function: what has to
// hold is that a run of `fugaro doctor` says it at all, with the severity a
// mode an owner may have chosen deserves, and stops saying it where the mode is
// enforce.

// The state the check exists for, and the state an installation nobody has
// raised is in: the project's database records no mode, and an absent mode is
// observe (D1548), so a build whose estimate the caps have no room for is
// recorded and submitted anyway.
//
// The rig's own local config says `mode: enforce`, which is the half that does
// not decide this: localcfg's mode says whether this CLI engages the budget
// backend at all (buildLeases), and the comparison a build's lease makes follows
// the database's node. A check that read the local config would call this state
// enforce and say nothing.
//
// Mutation (run, restore): delete the doctorBuildBudget call from doctor.go,
// and the first assertion fails — no check of that id is reported.
func TestDoctorWarnsObserveWithUnattendedBuilds(t *testing.T) {
	_, db := doctorPolicyRig(t, doctorPolicy(nil), "")

	c, ok := doctorChecksByID(t)["build-budget-mode"]
	if !ok {
		t.Fatal("doctor says nothing about what refuses an over-cap image build")
	}
	if c.Severity != "warning" || c.OK {
		t.Errorf("severity = %q, ok = %v, want a warning: the mode is one an owner may have chosen on purpose", c.Severity, c.OK)
	}
	if c.Class != doctorClassActionable {
		t.Errorf("class = %q, want %q: the line names the one command that changes it", c.Class, doctorClassActionable)
	}
	// What a person reads: which project, that the node is not set (and so
	// observe), that the build is submitted anyway, and the two things that are
	// left bounding it.
	for _, want := range []string{
		"proj-1234", "not set", "observe", "recorded and submitted anyway",
		"maxReserveMicros", "kill switch", "nothing bounds how many run",
	} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("the finding does not carry %q:\n%s", want, c.Problem)
		}
	}
	// And the remedy is a command this program has, the one that raises the mode
	// together with the caps it needs.
	for _, want := range []string{"fugaro budget set --global", "--mode enforce", "fugaro budget kill"} {
		if !strings.Contains(c.Fix, want) {
			t.Errorf("the remedy does not carry %q:\n%s", want, c.Fix)
		}
	}

	// The explicit observe an owner chose reads the same way, because it is the
	// same state for a build.
	db.Set(budget.PathMode, budget.ModeObserve)
	if c := doctorChecksByID(t)["build-budget-mode"]; !strings.Contains(c.Problem, budget.ModeObserve) {
		t.Errorf("an explicit observe mode is not named as such:\n%s", c.Problem)
	}

	// In enforce the lease stops an over-cap build, and there is nothing left to
	// warn about: the check reads as done.
	db.Set(budget.PathMode, budget.ModeEnforce)
	c = doctorChecksByID(t)["build-budget-mode"]
	if !c.OK || c.Severity != "" || c.Problem != "" {
		t.Errorf("a project in enforce still warns: %+v", c)
	}
}
