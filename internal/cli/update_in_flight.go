package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// update_in_flight.go is what the update door says, and does not do, about the
// runs that are running while it converges: the owner's requirement of
// 2026-11-09, whose step-by-step audit is docs/design/update-in-flight.md.
//
// Why the door and not the step that writes the project's policy: that stage
// REFUSED the write while a run was in flight and told the operator to wait for
// them (`refuseRunsInFlight`), which is exactly what the requirement forbids — a
// team of ten or twenty always has runs in flight, so a command that waits for
// them is a command nobody can run, and a refusal here would be the update
// asking a person to stop work for it. The audit found the refusal was never
// needed: the runner reads the project policy node and the rules marker once, at
// bootstrap, and memoizes both (`policyRead`, internal/runner/projectpolicy.go),
// so a run mid-flight keeps the ceiling it started with; the rules generator may
// only add to the document a run's own writes are granted by
// (`TestRulesPreexistingPathsUnchanged` and `TestRunTokenWritesSurviveThePolicyEdit`,
// internal/budget/rules); and no step of the update writes a run's registry
// entry, its ledger, its day counters or its record.
//
// One step can still bind a run in flight — the budget mode's raise to enforce,
// which a session re-reads on every lease and every report while the policy node
// is read once (`Session.readConfig`) — and the line names it where it can
// happen rather than claiming a safety that is not there. It stays, and it is
// the operator's own caps taking effect rather than the update stopping a run.

// announceRunsInFlight prints the update door's one line about the runs in
// flight, and nothing at all when there are none (an installation with an empty
// registry gets the update's ordinary transcript).
//
// It is one line, and it is not a question: no prompt, no refusal, and never a
// request to wait. It says nothing with --json (stdout is one object) or
// --print-vars (stdout is the variables alone), the two modes whose stdout is a
// document rather than a transcript, and nothing with --plan-only, which applies
// nothing and so has no run to leave running. Every door but the update's is
// silent too: `fugaro setup` shows a plan, and the runs in flight are the
// update's fact.
//
// A count that could not be read is silence and not an error. The line is
// information, and the writes it is about are guarded where they are made: an
// update that could not reach the budget database says so in the stages that
// need it, and a door that failed here over a line would invent a new way for
// the one command the upgrade is to stop.
func (e *initEngine) announceRunsInFlight(ctx context.Context) {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name || o.asJSON || o.printVars || o.planOnly {
		return
	}
	if e.lc == nil {
		return
	}
	lc, db, err := budgetDBFor(ctx, e.lc)
	if err != nil {
		return
	}
	n, err := runsInFlight(ctx, db, lc)
	if err != nil || n == 0 {
		return
	}
	if e.budgetModeBindsRuns(ctx, db) {
		fmt.Fprintf(r.w, "%s%d run(s) are in flight and they are unaffected, except by the budget mode's raise to %s: it is the one step that can bind one, from its next lease, and it is your own caps taking effect\n",
			o.programPrefix(), n, budget.ModeEnforce)
		return
	}
	fmt.Fprintf(r.w, "%s%d run(s) are in flight and they are unaffected: fugaro update stops no run and rewrites nothing a run holds, so no run has to finish first\n",
		o.programPrefix(), n)
}

// budgetModeBindsRuns is whether this run's budget-mode step can put the
// project-wide mode over the runs just counted: the one step of the update the
// audit found able to bound or halt a run in flight, in the mode node a session
// re-reads live (Session.readConfig) where the policy node is read once.
//
// It asks the two facts raiseBudgetMode's own raise is gated on first — the
// project being managed, and a mode that is neither enforce nor the owner's own
// recorded choice — and deliberately not the whole of its decision: whether the
// database is this installation's and whether the caps the raise is refused
// without are set are the step's own reads, and both of them print a warning
// naming what they left alone (update_budget_mode.go). So the line names the
// step where it can fire rather than promising it, which is the direction that
// errs towards warning a person about a bound that may not come instead of
// claiming a run is unaffected when it is not. The alternative — the step's
// whole decision read a second time here — would be one fact read twice, which
// is the drift this package keeps collapsing into one reading.
func (e *initEngine) budgetModeBindsRuns(ctx context.Context, db *rtdb.Client) bool {
	r := e.r
	managed := r.gateUnderway()
	if r.gate == nil {
		// The read the gate's own line would have made, for a door that reached
		// this before it — and no answer at all without the clients it reads
		// through, which is silence rather than a bound claimed or denied.
		if e.c == nil {
			return false
		}
		var err error
		if managed, err = r.managedProject(ctx, e.c); err != nil {
			return false
		}
	}
	if !managed {
		return false
	}
	// The absence of the node is observe (D1548), exactly as the raise reads it:
	// an installation whose mode node is not there is one the raise is for as
	// much as a seeded observe is.
	var mode string
	found, err := db.Get(ctx, budget.PathMode, &mode)
	if err != nil {
		return false
	}
	if !found {
		mode = budget.ModeObserve
	}
	if mode == budget.ModeEnforce {
		return false
	}
	var set string
	found, err = db.Get(ctx, budget.PathModeSet, &set)
	if err != nil {
		return false
	}
	return !(found && set == budget.ChosenByOwner)
}
