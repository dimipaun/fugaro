package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
)

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
			true, []string{"budget backend", "3 minutes", "reachable again"}},
		{"token expired", runstore.Halt{Reason: runstore.HaltBudgetTokenExpired, Scope: "run", Detail: "launch it again"},
			true, []string{"budget token", "start the run again"}},
		{"no cap in the database", runstore.Halt{Reason: runstore.HaltNoCap, Scope: "repo", Detail: "no daily cap is set for this repository"},
			true, []string{"fugaro budget set"}},
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
