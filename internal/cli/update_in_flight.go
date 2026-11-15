package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/report"
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
// Five steps can still reach one, and the note names every one of them that
// this run can take — naming the step and what it does to a run, rather than
// either claiming a safety that is not there or dropping the claim: the budget
// mode's raise to enforce, which a session re-reads on every lease and every
// report while the policy node is read once (`Session.readConfig`); the policy
// node itself, which a project routed through the credentials cascade re-reads
// on EVERY model call (`internal/credsvc`), so a run in flight is held to the
// node this update publishes from its next call; the window between that write
// and the credentials service's own replacement, which is the release before
// this one's service meeting a node it cannot read for the length of one apply;
// the installation's creation fact, which this release converges into a version
// the run in flight REFUSES to read, so a command of it keeps the typed
// confirmation instead of applying under the gate (update_marker_version.go);
// and a retirement, which deletes an orphaned Cloud Run job and takes the
// executions running on it with it (`deleteOrphanedJob`, init_retire.go). None
// of the five is the update stopping a run, and none of them is asked to wait.

// announceRunsInFlight counts the runs in flight while the CLI's own progress
// line says what it is doing, prints the door's short note about them, and —
// where the person asked to be asked — lets them stop before the first stage
// writes anything. It answers whether the run must stop, which is the one
// answer that changes nothing.
//
// The progress line is the CLI's own step (report.Step, the spinner and the
// step line every other step of this update is drawn on), because the count is
// a read of the budget database over the network and the owner's feedback of
// 2026-11-15 asked for it to be watched rather than waited on. It is opened and
// ended before the note, so the note is read under a finished step and never
// under a frame, and it is drawn exactly where every other line of this run is:
// a spinner at a terminal with colour, the step line alone on a pipe, and
// nothing at all under --json, --print-vars and --plan-only, which return above
// it. Every door but the update's is silent too: `fugaro setup` shows a plan,
// and the runs in flight are the update's fact.
//
// The note is short (inFlightNote): the count and the two facts that hold
// whatever this update does, then one short line per step that can still reach
// a run. It is never a request to wait, and it is not a question unless --ask
// asks for one.
//
// A count that could not be read is silence and not an error. The note is
// information, and the writes it is about are guarded where they are made: an
// update that could not reach the budget database says so in the stages that
// need it, and a door that failed here over a note would invent a new way for
// the one command the upgrade is to stop.
func (e *initEngine) announceRunsInFlight(ctx context.Context) (bool, error) {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name || o.asJSON || o.printVars || o.planOnly {
		return false, nil
	}
	if e.lc == nil {
		return false, nil
	}
	var check *report.Step
	if r.rep != nil {
		check = r.rep.Step("Checking for runs in flight")
	}
	lc, db, err := budgetDBFor(ctx, e.lc)
	var n int
	var steps []inFlightStep
	if err == nil {
		n, err = runsInFlight(ctx, db, lc)
	}
	if err == nil && n > 0 {
		steps = e.inFlightSteps(ctx, db)
	}
	if check != nil {
		check.Done("")
	}
	if err != nil || n == 0 {
		return false, nil
	}
	w := loggedOut(r.w, r.repLog)
	fmt.Fprintln(w, inFlightNote(o.programPrefix(), n, steps))
	stop, err := r.stopOverRunsInFlight()
	if err != nil {
		return false, err
	}
	if !stop {
		return false, nil
	}
	fmt.Fprintf(w, "%sstopped at your answer: nothing was written\n", o.programPrefix())
	return true, nil
}

// stopOverRunsInFlight is the courtesy the owner asked for on 2026-11-15: the
// person who just read the note may stop the update there, before any stage has
// written anything, and exit 0 with the installation exactly as it was. A run
// that was not asked answers false and goes on.
//
// It is asked only where --ask puts the door's questions back (asksBack: the
// flag, and the gate the flag has something to restore for), which is the
// condition the update's own confirmations are asked under (confirmNow) and the
// reason it does not break docs/no-prompt-policy: a managed project asks nothing
// by default, a coding agent's session, a pipe, --json and --non-interactive are
// answered without a prompt, and the flag is the policy's one sanctioned
// exemption, refused where nobody can answer it (askAtTerminal's own session
// check, which is the package's one read of a y/N — managed_ask.go).
//
// The question is phrased as the STOP and not as the continue, so the default
// an empty line gives is to go on. That is not a detail of wording: an update
// must never refuse over a run in flight (CLAUDE.md's HARD RULE), and a
// courtesy whose Enter key stopped the upgrade would be exactly that refusal
// wearing a prompt's clothes. A yes is the only answer that stops anything.
func (r *initRun) stopOverRunsInFlight() (bool, error) {
	if !r.asksBack() {
		return false, nil
	}
	return askAtTerminal(r.cmd, "stop here, before anything is written?", initflow.AskRefusal)
}

// inFlightNote is the door's short note: the count and the two facts that hold
// whatever this update does, then one short line per step that can still reach
// a run, each naming the step and what it does to a run in flight. An update
// that can reach none says so on the count's own line.
//
// The steps are the whole of what the note says about them, and the note is the
// same on the screen and in the log, so a person reading either reads one line
// per step rather than a paragraph of reasons.
func inFlightNote(prefix string, n int, steps []inFlightStep) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d run(s) are in flight: it asks no run to finish first and rewrites nothing a run holds", prefix, n)
	if len(steps) == 0 {
		b.WriteString(", and no step of it can reach them")
		return b.String()
	}
	b.WriteString(", and these steps can still reach them:")
	for _, s := range steps {
		b.WriteString("\n  " + s.line)
	}
	return b.String()
}

// inFlightStep is one step of this update that can reach a run in flight, as
// the one short line the note carries it on: the step's own name, and what it
// does to a run that is running. An empty list is the update that leaves every
// run in flight exactly where it is, and it is the ordinary case.
type inFlightStep struct {
	line string
}

// inFlightSteps is the steps of THIS update that can reach a run in flight, one
// short line each. The line names the step rather than denying it, which is
// what CLAUDE.md's HARD RULE asks the note for, and it is short enough to be
// read while the update goes past: the account of WHY each step can reach a run
// is the step's own, in docs/design/update-in-flight.md and in the tests that
// turn each one on alone.
//
// Every clause is deliberately the question the step itself is gated on rather
// than the whole of its decision (see budgetModeBindsRuns): the line errs
// towards naming a step that may end up doing nothing instead of claiming the
// update cannot reach a run when it can.
func (e *initEngine) inFlightSteps(ctx context.Context, db *rtdb.Client) []inFlightStep {
	var steps []inFlightStep
	isManaged := e.managedProjectRead(ctx)
	if e.budgetModeBindsRuns(ctx, db, isManaged) {
		steps = append(steps, inFlightStep{line: fmt.Sprintf(
			"budget mode: a run meets the raise to %s at its next lease", budget.ModeEnforce)})
	}
	if e.policyReadsLive(ctx, db) {
		steps = append(steps, inFlightStep{
			line: "policy: runs in flight pick up the new policy at their next model call"})
	}
	if e.credentialsWindowOpens(ctx, db) {
		steps = append(steps, inFlightStep{
			line: "credentials service: a call landing mid-update meets a service that cannot read the node"})
	}
	if e.markerConverges(ctx) {
		steps = append(steps, inFlightStep{
			line: "creation fact: a run of the release behind keeps its typed confirmation"})
	}
	if e.retireTakesExecutions(isManaged) {
		steps = append(steps, inFlightStep{
			line: "retirement: deletes an orphaned job and the executions on it"})
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
//
// It reads through ReadCarriedForward and not Read, and that is the whole of
// this line's correctness on the upgrade that bumps the policy version: the
// node every installation holds while this update runs is written in the
// version behind, so Read would refuse it and answer "no cascade" for the
// project that has one — and the line would say "no step of it can reach them"
// about the policy stage, which rewrites the node the credentials service
// re-reads on every model call of a run in flight. What is asked here is a
// question about the node rather than a decision by it, which is the difference
// Read and ReadCarriedForward are two functions for.
func (e *initEngine) policyReadsLive(ctx context.Context, db *rtdb.Client) bool {
	p, found, err := projectpolicy.ReadCarriedForward(ctx, db)
	if err != nil || !found || p == nil || p.Routing == nil || p.Routing.Claude == nil {
		return false
	}
	return p.Routing.Claude.Via == projectpolicy.RouteCascade
}

// credentialsWindowOpens is whether this update opens the window the credentials
// service's own replacement leaves: the run raises the published node's version
// (the node is behind this binary's, so the policy stage's write moves it) and
// then replaces the service that reads that node per call, one stage later
// (init_stages.go's credentialsResolveBeforeRaise is the same pair seen from the
// writing side). The service is read by a cascade project's model calls and by
// every Bitbucket-proxied call alike, so the clause is named for any project
// whose installation carries the service — which is why this asks the two facts
// the writing run asks and not the project's routing.
//
// Both halves are the questions the two stages are gated on rather than the
// whole of their decisions, as every clause here is: a node whose version is
// behind is one the policy stage may refuse to write for another reason (the
// gate, a ceiling this run may not loosen), and an image that is not this
// release's is one the move may be held back from pinning (credentialsMoveHeld).
// Naming the window over a run that turns out not to open it is the direction
// this line errs in.
func (e *initEngine) credentialsWindowOpens(ctx context.Context, db *rtdb.Client) bool {
	p, found, err := projectpolicy.Read(ctx, db)
	if err != nil || !found || p == nil || p.Version >= projectpolicy.Version {
		return false
	}
	doc, image, err := e.recordedCredentialsImage()
	if err != nil || doc == nil {
		return false
	}
	return credentialsImageStale(releaseVersion(), image)
}

// markerConverges is whether this update rewrites the installation's creation
// fact into a version the run in flight does not know: D1543's step converges a
// version-3 fact to the version this build writes (update_marker_version.go,
// managed.ConvergeMarker), and the run in flight runs the release behind — the
// reader that REFUSES the version it is rewritten to rather than reading it as
// the version-3 shape it resembles. So the clause is the one step of this
// update that changes what a run in flight makes of the marks: a command of that
// run that reads the mark between this update's write and its own end answers
// "not managed" and keeps the typed confirmation it would have applied without.
// That is the fail-closed direction the reconciliation's F12 asks for, and it is
// why this is named rather than passed over: nothing of the run is rewritten —
// its records, its ledger and its counters are untouched, and its every write
// still lands — but a person who sees a run stop taking the gate deserves to
// know which step of the update did it.
//
// What it asks is the question the step itself is gated on — the installation's
// fact being version 3 and believed — and not the whole of its decision, as
// every clause here: whether the bucket the read looks in is the one this run
// would write to, whether the project's number can be read, and whether the
// read can be made at all are the step's own, and it warns naming what it left
// alone. A read that failed, a fact of any other version, and an installation
// that is not managed are all silence.
func (e *initEngine) markerConverges(ctx context.Context) bool {
	if e.c == nil {
		return false
	}
	f, err := managed.ReadMarkFacts(ctx, e.c.Storage, e.c.CRM, e.r.projectName, e.r.gcpProject)
	return err == nil && f.Managed && f.Version == managed.MarkerVersionBuilds
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
