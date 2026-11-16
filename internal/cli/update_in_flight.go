package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra/managed"
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
// Five steps can still reach one, and the line names every one of them that
// this run can take — naming a step and why, rather than either claiming a
// safety that is not there or dropping the claim: the budget mode's raise to
// enforce, which a session re-reads on every lease and every report while the
// policy node is read once (`Session.readConfig`); the policy node itself, which
// a project routed through the credentials cascade re-reads on EVERY model call
// (`internal/credsvc`), so a run in flight is held to the node this update
// publishes from its next call; the Bitbucket tokens that node's write carries,
// which a proxied call re-reads per request, so the call after the publish may
// be served by a different token than the call before it; the window between
// that write and the credentials service's own replacement, which is the release
// before this one's service meeting a node it cannot read for the length of one
// apply; and a retirement, which deletes an orphaned Cloud Run job and takes the
// executions running on it with it (`deleteOrphanedJob`, init_retire.go). None
// of the five is the update stopping a run, and none of them is asked to wait.

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
	lead := fmt.Sprintf("%s%s in flight: update waits for none and changes nothing they hold", o.programPrefix(), runsPhrase(n))
	if steps := e.inFlightSteps(ctx, db); len(steps) > 0 {
		// The screen says only that a step can; the steps and the clause that
		// says why each one can are the run's own account (stageDetail: the log always,
		// the terminal under -v). A run with no log has nowhere else to put
		// them, so there the whole clause stands (G5).
		if r.repLog == nil {
			fmt.Fprintf(r.w, "%s, but can still reach them: %s\n", lead, inFlightWhy(steps))
			return
		}
		fmt.Fprintf(r.stageDetail(), "%s, but can still reach them: %s\n", lead, inFlightWhy(steps))
		fmt.Fprintf(r.w, "%s, but can still reach them\n", lead)
		return
	}
	fmt.Fprintf(r.w, "%s, and no step reaches them\n", lead)
}

func runsPhrase(n int) string {
	if n == 1 {
		return "1 run"
	}
	return fmt.Sprintf("%d runs", n)
}

// inFlightStep is one step of this update that can reach a run in flight: the
// clause that names it and says why, in the words a person reads, which is what
// the log gets whole.
type inFlightStep struct {
	why string
}

// inFlightWhy is the steps as the one clause each that explains them, for the
// line that has room for the whole account.
func inFlightWhy(steps []inFlightStep) string {
	whys := make([]string, len(steps))
	for i, s := range steps {
		whys[i] = s.why
	}
	return strings.Join(whys, "; ")
}

// inFlightSteps is the steps of THIS update that can reach a run in flight, one
// entry each: the clause that names it and says why,
// in the words a person reads. An empty result is the update that leaves every
// run in flight exactly where it is, and it is the ordinary case.
//
// Every clause is deliberately the question the step itself is gated on rather
// than the whole of its decision (see budgetModeBindsRuns): the line errs
// towards naming a step that may end up doing nothing instead of claiming the
// update cannot reach a run when it can.
func (e *initEngine) inFlightSteps(ctx context.Context, db *rtdb.Client) []inFlightStep {
	var steps []inFlightStep
	// isManaged rather than managed: the package of the same name is read below,
	// for the two versions the creation fact's step names.
	isManaged := e.managedProjectRead(ctx)
	if e.budgetModeBindsRuns(ctx, db, isManaged) {
		steps = append(steps, inFlightStep{
			why: fmt.Sprintf("the budget mode's raise to %s, which a run in flight meets at its next lease and which is your own caps taking effect", budget.ModeEnforce)})
	}
	if e.policyReadsLive(ctx, db) {
		steps = append(steps, inFlightStep{
			why: fmt.Sprintf("the policy this update publishes, which every model call of this project's runs re-reads (%s: %s), so a run in flight is held to the new node from its next call", "routing.claude.via", projectpolicy.RouteCascade)})
	}
	if e.credentialsWindowOpens(ctx, db) {
		steps = append(steps, inFlightStep{
			why: "the credentials service this update replaces, which reads the published node on every call it serves: this run raises the node's version in one stage and moves the service in the next, and a call landing between the two meets the previous release's service with a node its own binary does not understand"})
	}
	if e.tokensPublishReachesRuns(ctx, db) {
		steps = append(steps, inFlightStep{
			why: "the Bitbucket tokens this update publishes, which a proxied call re-reads on every request (bitbucketproxy.go): the call after the publish may be served by a different token than the call before it, and it is the operator's own records taking effect"})
	}
	if e.markerConverges(ctx) {
		steps = append(steps, inFlightStep{
			why: fmt.Sprintf("the creation fact this update rewrites from version %d to version %d, which a run in flight re-reads per command: that run is the release behind, whose reader refuses a version it does not know, so its own apply keeps the typed confirmation instead of applying under the gate — nothing of the run is rewritten, and no record, ledger or counter of it is touched", managed.MarkerVersionBuilds, managed.MarkerVersionNoBuilds)})
	}
	if e.retireTakesExecutions(isManaged) {
		steps = append(steps, inFlightStep{
			why: "the retirement this update runs, which deletes an orphaned job and takes the executions still running on it"})
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

// tokensPublishReachesRuns is whether this update's policy stage can publish a
// node whose bitbucket_tokens key a run in flight then meets: the local config
// names typed Bitbucket token records (so the stage derives the key and the
// drift comparison finds it) and the published node turns the credentials
// service's Bitbucket route on (`bitbucket_token.proxy`, the operator's
// per-installation switch), which is what sends a run's Bitbucket calls through
// the service rather than to the provider with the job's own mounted token.
//
// The route re-reads the node on every request it serves — the selection is made
// from the node's coverage, and the record it builds is derived from the node
// alone (`internal/credsvc/bitbucketproxy.go`) — where the runner's own read is
// memoized at bootstrap. So the first call after this update's write may be
// served by a different token than the call before it: a record the operator has
// just added takes over a repository it covers, and one they narrowed stops
// serving it. That is the operator's own records taking effect on a run already
// running, which is why it is named rather than passed over.
//
// A run of the release behind sees the key too, and neither half of the gate is
// about that: its runner tolerates an unknown top-level key and falls back to
// its single `GET /user` identity (R5), and the row for this step in
// docs/design/update-in-flight.md names it. What this asks is the pair the
// per-call re-read is gated on, and it errs towards naming the step, as every
// clause here: whether the publish turns out to change the key at all (the
// records may be exactly what the node already carries) is the stage's own
// decision, and so is whether this run gets as far as writing it.
//
// Both reads answer silence the way the rest of this file does: a node that is
// not there is a project with no proxy switch to read, and a read that failed is
// the answer every other clause treats as "no". The node is read through
// ReadCarriedForward for policyReadsLive's reason — the node every installation
// holds while this update runs may be written in the version behind, and Read
// would refuse it and answer "no proxy" for the project that has one.
func (e *initEngine) tokensPublishReachesRuns(ctx context.Context, db *rtdb.Client) bool {
	if e.lc == nil || len(e.lc.BitbucketTokens) == 0 {
		return false
	}
	p, found, err := projectpolicy.ReadCarriedForward(ctx, db)
	if err != nil || !found || p == nil || p.BitbucketToken == nil {
		return false
	}
	return p.BitbucketToken.Proxy
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
