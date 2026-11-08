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

// TestOwnerDecisionReportSendsTheReaderBelow: the halted report writes its To
// continue line before the decisions that line remembers, because the only
// place a decision is named is the Waiting-on-you list (followUpReport writes
// haltLines and then ownerSection, and the Halted line above carries the count
// alone). So the remedy has to send the reader down to that list: it said "the
// decisions above", which named a place in the report where nothing about a
// decision is printed. This renders the whole report, which is what a reader
// sees, and checks the list really is under the line that points at it.
func TestOwnerDecisionReportSendsTheReaderBelow(t *testing.T) {
	at := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	rec := &runstore.Record{
		Version: 1, RunID: "20261002-100000-abcd",
		Status: runstore.StatusHalted, Outcome: runstore.OutcomeDraft,
		Reason: "review is waiting on 2 owner decisions",
		Halt: &runstore.Halt{Reason: runstore.HaltOwnerDecision, Scope: "run", At: at,
			Detail: "review is waiting on 2 owner decisions"},
		OwnerDecisions: runstore.NewOwnerDecisions([]runstore.OwnerDecision{
			{File: "LICENSE.md", Summary: "the licence text needs counsel's clearance"},
			{File: "internal/approval/flow.go", Summary: "the approval flow is a product decision"},
		}),
	}
	got := followUpReport(rec, "", nil, nil, nil)
	const remedy = "To continue: the owner answers 2 decisions below, then run `fugaro run --pr N` with `--owner-ruled <text-or-file>` carrying the ruling."
	line := strings.Index(got, remedy)
	if line < 0 {
		t.Fatalf("the report does not carry the remedy line %q:\n%s", remedy, got)
	}
	list := strings.Index(got, "**Waiting on you**")
	if list < line {
		t.Fatalf("the decisions are not under the line that sends the reader to them:\n%s", got)
	}
	for _, want := range []string{"`LICENSE.md`: the licence text needs counsel's clearance",
		"`internal/approval/flow.go`: the approval flow is a product decision"} {
		if !strings.Contains(got[list:], want) {
			t.Errorf("the list under the remedy does not name %q:\n%s", want, got[list:])
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
