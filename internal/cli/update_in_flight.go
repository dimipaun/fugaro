package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
// Three steps can still reach one, and the line names every one of them that
// this run can take — naming a step and why, rather than either claiming a
// safety that is not there or dropping the claim: the budget mode's raise to
// enforce, which a session re-reads on every lease and every report while the
// policy node is read once (`Session.readConfig`); the policy node itself, which
// a project routed through the credentials cascade re-reads on EVERY model call
// (`internal/credsvc`), so a run in flight is held to the node this update
// publishes from its next call; and a retirement, which deletes an orphaned
// Cloud Run job and takes the executions running on it with it
// (`deleteOrphanedJob`, init_retire.go). None of the three is the update
// stopping a run, and none of them is asked to wait.

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
	if steps := e.inFlightSteps(ctx, db); len(steps) > 0 {
		fmt.Fprintf(r.w, "%s%d run(s) are in flight and they are unaffected, except by %s\n",
			o.programPrefix(), n, strings.Join(steps, "; "))
		return
	}
	fmt.Fprintf(r.w, "%s%d run(s) are in flight and they are unaffected: fugaro update stops no run and rewrites nothing a run holds, so no run has to finish first\n",
		o.programPrefix(), n)
}

// inFlightSteps is the steps of THIS update that can reach a run in flight, one
// clause each: the step and why, in the words a person reads. An empty result is
// the update that leaves every run in flight exactly where it is, and it is the
// ordinary case.
//
// Every clause is deliberately the question the step itself is gated on rather
// than the whole of its decision (see budgetModeBindsRuns): the line errs
// towards warning a person about a bound that may not come instead of claiming a
// run is unaffected when it is not.
func (e *initEngine) inFlightSteps(ctx context.Context, db *rtdb.Client) []string {
	var steps []string
	managed := e.managedProjectRead(ctx)
	if e.budgetModeBindsRuns(ctx, db, managed) {
		steps = append(steps, fmt.Sprintf("the budget mode's raise to %s, which a run in flight meets at its next lease and which is your own caps taking effect", budget.ModeEnforce))
	}
	if e.policyReadsLive(ctx, db) {
		steps = append(steps, fmt.Sprintf("the policy this update publishes, which every model call of this project's runs re-reads (%s: %s), so a run in flight is held to the new node from its next call", "routing.claude.via", projectpolicy.RouteCascade))
	}
	if e.retireTakesExecutions(managed) {
		steps = append(steps, "the retirement this update runs, which deletes an orphaned job and takes the executions still running on it")
	}
	return steps
}

// managedProjectRead is r.gateUnderway's answer, or the read the gate's own line
// makes for a door that reached this before it — and no answer at all without
// the clients it reads through, which is the same "not managed" every caller
// treats as silence.
func (e *initEngine) managedProjectRead(ctx context.Context) bool {
	r := e.r
	if r.gate != nil {
		return r.gateUnderway()
	}
	if e.c == nil {
		return false
	}
	managed, err := r.managedProject(ctx, e.c)
	return err == nil && managed
}

// budgetModeBindsRuns is whether this run's budget-mode step can put the
// project-wide mode over the runs just counted: the one step of the update the
// audit found able to bound or halt a run in flight, in the mode node a session
// re-reads live (Session.readConfig) where the policy node is read once.
//
// It asks the two facts raiseBudgetMode's own raise is gated on — the project
// being managed, and a mode that is neither enforce nor the owner's own recorded
// choice — and deliberately not the whole of its decision: whether the database
// is this installation's and whether the caps the raise is refused without are
// set are the step's own reads, and both of them print a warning naming what
// they left alone (update_budget_mode.go). The alternative — the step's whole
// decision read a second time here — would be one fact read twice, which is the
// drift this package keeps collapsing into one reading.
func (e *initEngine) budgetModeBindsRuns(ctx context.Context, db *rtdb.Client, managed bool) bool {
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

// policyReadsLive is whether this project's runs re-read the published policy
// node while they run: the switch that turns the credentials cascade on
// (`routing.claude.via: cascade`) sends every model call through the credentials
// service, which reads the node per call rather than memoizing it at bootstrap
// the way the runner does. A project without the switch keeps the ceiling it
// bootstrapped with, and the node this update writes never reaches it.
//
// A read that failed or a node that is not there is false: with no node there is
// no switch, and an unreadable one is the silence every other read in this file
// answers with.
func (e *initEngine) policyReadsLive(ctx context.Context, db *rtdb.Client) bool {
	p, found, err := projectpolicy.Read(ctx, db)
	if err != nil || !found || p == nil || p.Routing == nil || p.Routing.Claude == nil {
		return false
	}
	return p.Routing.Claude.Via == projectpolicy.RouteCascade
}

// retireTakesExecutions is whether this run can retire at all: the two ways one
// is reached, `--retire` and a managed project's own retirement (retireManaged,
// issue #1794), which a plain `fugaro update` runs with no flag at all and
// behind the gate rather than behind a typed confirmation.
//
// The second half is asked of the project's mark and not of the retirement the
// repository stage has yet to read: whether this config change stops producing
// an address is a fact of the stage's plan, which has not run when the door
// speaks, so a managed update names the retirement whether or not this one will
// delete anything. A run executing on a job the config no longer produces is
// exactly the run this clause is for, and the alternative — silence over a
// deletion that then happens — is the direction that leaves a person surprised.
func (e *initEngine) retireTakesExecutions(managed bool) bool {
	o := e.r.o
	if o.retire {
		return true
	}
	return managed
}
