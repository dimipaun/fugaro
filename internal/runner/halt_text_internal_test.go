package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestHaltTextNamesEachGovernanceStop: every governance reason has words of
// its own, and they are the runner's own vocabulary rather than the reason
// constant echoed back — the checkpoint's why_over is this text, so a value
// that fell through to the default would put "stage_budget" in front of a
// person as the explanation of their stopped run. One reason has two texts:
// stage_budget stops a stage that passed its own dollars and one that passed
// its own timeout (T7.6), and the detail is the runner's own word for which.
func TestHaltTextNamesEachGovernanceStop(t *testing.T) {
	for _, tc := range []struct {
		reason runstore.HaltReason
		detail string
		want   string
	}{
		{runstore.HaltStageBudget, "", "a stage passed its own budget"},
		{runstore.HaltStageBudget, "time", "a stage passed its own time bound"},
		{runstore.HaltReviewRatio, "", "review spent more calls than the ratio allows for the implementation it read"},
		{runstore.HaltFanout, "", "a stage used more sub-agents than the maximum"},
		{runstore.HaltReviewScope, "", "the review range covers files this run never changed"},
		{runstore.HaltExpectedExceeded, "", "the run passed the cost its tier expected of it"},
	} {
		got := haltReasonText(runstore.Halt{Reason: tc.reason, Scope: "run", Detail: tc.detail})
		if got != tc.want {
			t.Errorf("haltReasonText(%s) = %q, want %q", tc.reason, got, tc.want)
		}
		if got == string(tc.reason) {
			t.Errorf("%s falls through to the reason constant", tc.reason)
		}
	}
}

// TestHaltedReportNamesCauseAndRemedy: each halt of the shared budget says
// what stopped the run and what to do next, in the report people read.
func TestHaltedReportNamesCauseAndRemedy(t *testing.T) {
	at := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		halt   runstore.Halt
		budget bool
		want   []string
	}{
		{"global kill", runstore.Halt{Reason: runstore.HaltKillSwitch, Scope: "global", Detail: "the global kill switch is on (admin@example.invalid: runaway spend)"},
			true, []string{"project's kill switch", "admin@example.invalid: runaway spend", "fugaro budget resume"}},
		{"repo kill", runstore.Halt{Reason: runstore.HaltKillSwitch, Scope: "repo", Detail: "this repository's kill switch is on"},
			true, []string{"repository's kill switch", "fugaro budget resume"}},
		{"repo day cap", runstore.Halt{Reason: runstore.HaltRepoDailyCap, Scope: "repo", Detail: "this repository's daily cap $60.00 reached"},
			true, []string{"daily dollar cap", "fugaro budget set", "UTC day"}},
		{"committed day cap", runstore.Halt{Reason: runstore.HaltRepoDailyCap, Scope: "repo", Detail: "per_day_usd $1.00 of fugaro.yaml reached"},
			true, []string{"per_day_usd", "fugaro.yaml"}},
		{"global day cap", runstore.Halt{Reason: runstore.HaltGlobalDailyCap, Scope: "global", Detail: "the project's daily cap $150.00 reached"},
			true, []string{"project's daily dollar cap", "fugaro budget set"}},
		{"unavailable", runstore.Halt{Reason: runstore.HaltBudgetUnavailable, Scope: "run", Detail: "the budget backend could not be reached for 3m0s (lease)"},
			true, []string{"budget backend", "whole grace period", "reachable again"}},
		{"token expired", runstore.Halt{Reason: runstore.HaltBudgetTokenExpired, Scope: "run", Detail: "launch it again"},
			true, []string{"budget token", "start the run again"}},
		{"no cap in the database", runstore.Halt{Reason: runstore.HaltNoCap, Scope: "repo", Detail: "no daily cap is set for this repository"},
			true, []string{"fugaro budget set"}},
		{"no credential", runstore.Halt{Reason: runstore.HaltNoCredential, Scope: "run", Detail: "the earliest reset is 2026-10-02T11:00:00Z, and this job must be finished by 2026-10-02T10:30:00Z"},
			true, []string{"no credential became usable before the job's own timeout", "fugaro accounts ls", "`credentials`", "start the run again"}},
		{"creds rules denied", runstore.Halt{Reason: runstore.HaltCredsRulesDenied, Scope: "run", Detail: "the database's rules refuse the cascade's fields"},
			true, []string{"the database's rules refused this run's credential state", "fugaro update --firebase-project", "start the run again"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := c.halt
			h.At = at
			rec := &runstore.Record{Version: 1, RunID: "20261002-100000-abcd", Status: runstore.StatusHalted, Outcome: runstore.OutcomeNone, Halt: &h, CostUSD: 0.5}
			if c.budget {
				rec.Budget = &runstore.BudgetRecord{Mode: "enforce", Backend: "rtdb"}
			}
			got := haltLines(rec)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("the halted report lacks %q:\n%s", w, got)
				}
			}
		})
	}
}
