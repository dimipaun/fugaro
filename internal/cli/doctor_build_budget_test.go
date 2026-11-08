package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// buildBudgetCheck is the D1548 check as the wired emitter produces it —
// doctorPolicyChecks is what doctor --json prints — driven directly so the
// test can see the unread marker, which --json deliberately leaves out.
func buildBudgetCheck(t *testing.T, cfgPath string) (doctorCheck, bool) {
	t.Helper()
	lc, err := localcfg.Load(cfgPath)
	if err != nil {
		t.Fatalf("the config this test writes is not one an installation could have: %v", err)
	}
	for _, c := range classifyDoctorChecks(doctorPolicyChecks(context.Background(), lc, doctorRoutingLayer{})) {
		if c.ID == doctorBuildBudgetID {
			return c, true
		}
	}
	return doctorCheck{}, false
}

// TestDoctorWarnsObserveWithUnattendedBuilds is D1548's line: image builds are
// capped only when the mode enforces, and an absent mode is observe, so the
// state a default installation starts in leaves a runaway build recorded and
// never refused — while this installation's builds run unattended (the daily
// image check job and the project's catalog check job submit Cloud Builds with
// nobody at the terminal). doctor names that state, with the one command that
// ends it, and says nothing at all when the mode is enforce.
//
// Mutation (verified, run and restored): treat an absent mode as enforce
// (`if !found || mode == budget.ModeEnforce` where doctorBuildBudgetCheck
// returns nil) — "no mode is set" fails, doctor reporting nothing about the
// state every installation is seeded into (internal/infra/firebasedb.go seeds
// config/mode = observe).
func TestDoctorWarnsObserveWithUnattendedBuilds(t *testing.T) {
	cases := []struct {
		name string
		seed func(db *gcpfake.RTDB)
		want string // what the problem must name; "" means doctor says nothing
	}{
		{
			name: "no mode is set",
			seed: func(db *gcpfake.RTDB) {},
			want: "is not set, and an unset mode behaves as observe",
		},
		{
			name: "the mode is observe",
			seed: func(db *gcpfake.RTDB) { db.Set(budget.PathMode, budget.ModeObserve) },
			want: "is observe",
		},
		{
			// Only the string enforce enforces (internal/budget/model.go):
			// a node holding anything else is the observe state, and the value
			// itself is what the reader has to see to fix it.
			name: "the mode is a value nothing enforces",
			seed: func(db *gcpfake.RTDB) { db.Set(budget.PathMode, "enforcement") },
			want: "enforcement",
		},
		{
			name: "the mode is enforce",
			seed: func(db *gcpfake.RTDB) { db.Set(budget.PathMode, budget.ModeEnforce) },
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, db := doctorPolicyRig(t, doctorPolicy(nil), "")
			tc.seed(db)
			c, ok := buildBudgetCheck(t, r.cfgPath)
			if tc.want == "" {
				if ok {
					t.Fatalf("doctor reported %+v with the mode enforcing: a build that meets a cap is refused there, so there is nothing to warn about", c)
				}
				return
			}
			if !ok {
				t.Fatalf("doctor reported nothing for %s: the caps are advisory and the builds are unattended", tc.name)
			}
			if c.Severity != "warning" {
				t.Errorf("severity = %q, want warning (an instruction to run one command, not a failure of the installation)", c.Severity)
			}
			// The line is not a measurement: it has to print in the default
			// view and fail --strict, or the state it names is invisible.
			if c.Class != doctorClassActionable || !doctorIsFinding(c) {
				t.Errorf("the check is class %q finding=%v: a warning that is not actionable prints nowhere a reader looks", c.Class, doctorIsFinding(c))
			}
			for _, want := range []string{
				tc.want,
				"unattended",
				"daily image check",
				"catalog check",
				"kill switches",
				"maxReserve",
			} {
				if !strings.Contains(c.Problem, want) {
					t.Errorf("the warning does not name %q: %q", want, c.Problem)
				}
			}
			for _, want := range []string{"fugaro budget set --global", "--mode enforce"} {
				if !strings.Contains(c.Fix, want) {
					t.Errorf("the fix does not name %q: %q", want, c.Fix)
				}
			}
			// Every flag the fix names is a flag `fugaro budget set` really
			// has: a fix naming a flag the command does not take is work the
			// reader cannot do.
			set, _, err := NewRootCmd().Find([]string{"budget", "set"})
			if err != nil {
				t.Fatal(err)
			}
			for _, flag := range flagsNamed(c.Fix) {
				if set.Flags().Lookup(flag) == nil {
					t.Errorf("the fix names --%s, which `fugaro budget set` does not have: %q", flag, c.Fix)
				}
			}
		})
	}

	// A mode node doctor cannot read is a fact unknown, not a state: the note
	// doctor gives every read that did not answer, never silence and never the
	// claim that the mode is observe.
	t.Run("the mode cannot be read", func(t *testing.T) {
		r, db := doctorPolicyRig(t, doctorPolicy(nil), "")
		db.DenyRead(budget.PathMode)
		c, ok := buildBudgetCheck(t, r.cfgPath)
		if !ok {
			t.Fatal("doctor reported nothing for a mode it could not read: an unknown counted as a measurement is the silent pass")
		}
		if c.Severity != "info" || !c.unread {
			t.Errorf("the check is severity %q unread=%v, want an info note carrying the unread marker", c.Severity, c.unread)
		}
		if !strings.Contains(c.Problem, "could not be checked") || c.Fix == "" {
			t.Errorf("the note is %q / %q, want what could not be read and the remedy", c.Problem, c.Fix)
		}
		if strings.Contains(c.Problem, "unattended") {
			t.Errorf("a mode that could not be read is reported as the observe state: %q", c.Problem)
		}
	})
}
