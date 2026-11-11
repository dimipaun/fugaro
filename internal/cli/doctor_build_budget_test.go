package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// TestDoctorWarnsObserveWithUnattendedBuilds: T4's doctor line about the one
// installation where an over-cap image build is recorded and never refused —
// observe, chosen by the owner, which no later `fugaro update` raises.
//
// It exists because the prompt that used to stand in front of a build is gone
// (T4): on an installation whose mode nobody will raise, the lease counts the
// money and stops nothing, and a build started by CI, a script or a coding
// agent's session runs with nobody at the terminal to read the reservation.
// doctor is then the only reader who can say so.
//
// The line is scoped to exactly that state, and the scoping is half of what this
// test pins: it says nothing once the installation enforces, and nothing for a
// seeded observe — that one is doctor_budget_mode.go's own check, with the
// update that raises it, and one fact printed twice is a reader who cannot tell
// which line to act on. A read that did not answer is an info line with the
// unread marker, never a pass: "unknown" is not "fine".
//
// Mutation (run, restore): drop the `set != budget.ChosenByOwner` branch and
// this test fails on the seeded-observe subtest, whose line this check would
// then report as well; drop the enforce branch and it fails on the enforcing
// one, warned about an installation that refuses nothing.
func TestDoctorWarnsObserveWithUnattendedBuilds(t *testing.T) {
	t.Run("an observe mode the owner chose is named with the command that raises it", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.Set(budget.PathMode, budget.ModeObserve)
		db.Set(budget.PathModeSet, budget.ChosenByOwner)
		c, ok := doctorChecksByID(t)[buildBudgetCheckID]
		if !ok {
			t.Fatalf("doctor printed no %s check for an observe mode its owner chose", buildBudgetCheckID)
		}
		if c.OK || c.Severity != "warning" {
			t.Errorf("%s = ok %v, severity %q, want a warning", buildBudgetCheckID, c.OK, c.Severity)
		}
		// What the reader has to hear: the mode, what the lease does (records and
		// submits anyway), who the build that runs is, that the per-build
		// confirmation is not there, what is left that does bound it, and the one
		// command that lifts it.
		for _, want := range []string{
			budget.ModeObserve,
			"recorded and submitted anyway",
			"takes a lease",
			"nothing refuses it",
			"CI", "script", "coding agent",
			budget.PathLimits + "/maxReserveMicros",
			"fugaro budget set --global --mode " + budget.ModeEnforce,
		} {
			if !strings.Contains(c.Problem+c.Fix, want) {
				t.Errorf("the line does not name %q: %+v", want, c)
			}
		}
		// The flags the repair names are flags `fugaro budget set` really has,
		// the check image_cost_test.go makes of the refusal's own command.
		set, _, err := NewRootCmd().Find([]string{"budget", "set"})
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range flagsNamed(c.Fix) {
			if set.Flags().Lookup(flag) == nil {
				t.Errorf("the line names --%s, which `fugaro budget set` does not have: %q", flag, c.Fix)
			}
		}
	})
	t.Run("an absent mode node is named as absent", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		// No config/mode at all: observe (D1548), and it is worth saying that the
		// node is not there, because the repair is a write to it.
		db.Set(budget.PathModeSet, budget.ChosenByOwner)
		c, ok := doctorChecksByID(t)[buildBudgetCheckID]
		if !ok {
			t.Fatalf("doctor printed no %s check for an owner-chosen mode with no node", buildBudgetCheckID)
		}
		if c.OK || c.Severity != "warning" {
			t.Errorf("%s = ok %v, severity %q, want a warning", buildBudgetCheckID, c.OK, c.Severity)
		}
		if !strings.Contains(c.Problem, budget.PathMode+" is absent") {
			t.Errorf("the line does not say the node is absent, which is the repair: %+v", c)
		}
	})
	t.Run("a seeded observe is the other line's to report", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.Set(budget.PathMode, budget.ModeObserve)
		db.Set(budget.PathModeSet, budget.ChosenByUpdate)
		if c, ok := doctorChecksByID(t)[buildBudgetCheckID]; ok && !c.OK {
			t.Errorf("this check reports %+v on a seeded observe, which doctor_budget_mode.go already names", c)
		}
	})
	t.Run("an enforcing installation has nothing to say", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.Set(budget.PathMode, budget.ModeEnforce)
		db.Set(budget.PathModeSet, budget.ChosenByOwner)
		if c, ok := doctorChecksByID(t)[buildBudgetCheckID]; ok && !c.OK {
			t.Errorf("this check reports %+v on an installation that enforces", c)
		}
	})
	t.Run("a mode that cannot be read is a note, not a pass", func(t *testing.T) {
		_, db := doctorPolicyRig(t, nil, "")
		db.DenyRead(budget.PathMode)
		c, ok := doctorChecksByID(t)[buildBudgetCheckID]
		if !ok {
			t.Fatalf("doctor printed no %s check for a database it could not read", buildBudgetCheckID)
		}
		if c.Severity != "info" {
			t.Errorf("%s = severity %q, want an info line: a read that did not answer is not a finding", buildBudgetCheckID, c.Severity)
		}
		if !strings.Contains(c.Problem, "could not be read") {
			t.Errorf("the line does not say which read failed: %+v", c)
		}
	})
}
