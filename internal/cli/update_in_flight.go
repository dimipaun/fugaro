package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
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
// Eight steps can still reach one, and the note names every one of them this run
// can take — naming a step, rather than either claiming a safety that is not
// there or dropping the claim: the budget mode's raise to enforce, which a
// session re-reads on every lease and every report while the policy node is read
// once (`Session.readConfig`); the policy node itself, which a project routed
// through the credentials cascade re-reads on EVERY model call
// (`internal/credsvc`), so a run in flight is held to the node this update
// publishes from its next call; the Bitbucket tokens that node's write carries,
// which a proxied call re-reads per request, so the call after the publish may
// be served by a different token than the call before it; the Bitbucket proxy
// switch the same stage turns on in the local config (S6), which no run in
// flight reads — its runner decided proxiedness once, at bootstrap — and which
// makes every LATER launch of the repository fetch its git credential from the
// service (bitbucketProxySwitchTurns); the window between that write and the
// credentials service's own replacement, which is the release before this one's
// service meeting a node it cannot read for the length of one apply; the
// workspace's own credentials service, replaced by this update where the
// workspace's recorded image is behind, whose old revision drains as the new one
// takes over (R11); the creation fact, which a run of the release behind
// re-reads per command and refuses once it is rewritten, so that run keeps the
// typed confirmation it would have applied without; the workspace credentials
// node that stage's neighbour publishes, which the workspace's own service
// re-reads as it serves (R13), so a member this run takes out of the table, or
// an entry it unshares, stops being served at that service's next read; and a
// retirement, which deletes an orphaned Cloud Run job and takes the executions
// running on it with it (`deleteOrphanedJob`, init_retire.go). None of the nine
// is the update stopping a run, and none of them is asked to wait.
//
// The note is the count's own line and then ONE SHORT LINE PER STEP, because the
// owner met it with fourteen runs in flight and could not find what they were
// after in the paragraph it used to be (the feedback of 2026-11-15): the whole
// of what each step does to a run in flight is its clause here, its row in the
// audit (docs/design/update-in-flight.md) and the gate function the line reads.
// The count's own read is a step of the run, so the wait it can be is named
// while it happens.
//
// Three of the lines under the count are not steps, and they are the facts the
// count's line cannot carry: R5's, that a run which started with the budget off
// holds its key already and finishes; MG3's, that this release keeps writing the
// old access record, and that so many of the runs in flight are the runs it is
// written for; and PR6's, how many of the count are paused runs. None is a reach
// and none is gated on a step's own question, which is why they are printed
// beside the steps rather than among them: a note saying no step reaches a run in
// flight is about the steps alone, and these facts stand outside that list.
//
// The paused runs are the count's own fact and not a step's, and they are a line
// of their own rather than a parenthesis inside the count (PR6): a run that
// suspended itself has ended its execution and released its lease (R12), so the
// registry the count otherwise reads holds no entry of it, and the pause's node
// under paused/ plus the run's own record are what say it is in flight (C3, R26;
// D3195). The count's sentence is the one the live checks and the owner's note
// read, and nothing is inserted into it (`docs/gcp-live-checklist.md`, Check
// 632) — so the paused figure is said under it, where the update also says that
// it rewrites nothing such a run holds (its record, its node, its saved session,
// its chain's ledger).

// announceRunsInFlight prints the update door's note about the runs in flight —
// the count's line and the steps under it. It prints nothing at all when no run
// is running (an installation with an empty registry gets the update's ordinary
// transcript).
//
// It refuses nothing, it asks nothing, it asks no run to finish first, and it
// never makes the update wait for one: what it claims is that the update waits
// for none and changes nothing a run holds, and the steps that can still reach a
// run are named under it rather than denied. A person who wants to stop instead
// has the update's own confirmation, and this note is the fact they decide it
// on: it is printed before that confirmation and before any stage writes.
//
// It says nothing with --json (stdout is one object) or --print-vars (stdout is
// the variables alone), the two modes whose stdout is a document rather than a
// transcript, and nothing with --plan-only, which applies nothing and so has no
// run to leave running. Every door but the update's is silent too: `fugaro
// setup` shows a plan, and the runs in flight are the update's fact.
//
// A count that could not be read is silence and not an error. The note is
// information, and the writes it is about are guarded where they are made: an
// update that could not reach the budget database says so in the stages that
// need it, and a door that failed here over a line would invent a new way for
// the one command the upgrade is to stop.
func (e *initEngine) announceRunsInFlight(ctx context.Context) error {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name || o.asJSON || o.printVars || o.planOnly {
		return nil
	}
	if e.lc == nil {
		return nil
	}
	lc, db, err := budgetDBFor(ctx, e.lc)
	if err != nil {
		return nil
	}
	// The registry read is the door's slow one on an installation with many
	// runs, and it is the reason this door has a step of its own before the
	// loop: the step names the wait while it happens (the owner's feedback of
	// 2026-11-15 — with fourteen runs running, the command sat there).
	var step *report.Step
	if r.rep != nil {
		step = r.rep.Step("Checking for runs in flight")
	}
	agents, err := inFlightRegistry(ctx, db, lc)
	// The paused runs are counted beside the registry, and for the same read's
	// sake: a suspended run's execution has ended (R12), so the registry holds
	// no entry of it, and the node the credentials service wrote is the one
	// place its chain's in-flightness is written (C3, R26).
	paused := 0
	if err == nil {
		paused = e.pausedRuns(ctx, db)
	}
	n := runsCount(agents) + paused
	if step != nil {
		if err != nil {
			// A count that could not be read is silence: a step abandoned
			// prints nothing and counts nothing, so the run's account is what
			// it was.
			step.Abandon()
		} else {
			step.Done("")
		}
	}
	if err != nil || n == 0 {
		return nil
	}
	note := fmt.Sprintf("%s%s in flight: update waits for none and changes nothing they hold", o.programPrefix(), runsPhrase(n))
	// The screen and the log read the same note, because it is one note: the
	// count's line, then one short line per step (loggedOut, D1352 — and not the
	// reporter's stream, which is the terminal under --verbose as well as the
	// log, and would print the note twice there).
	w := loggedOut(r.w, r.repLog)
	if steps := e.inFlightSteps(ctx, db); len(steps) > 0 {
		fmt.Fprintf(w, "%s, and these steps can still reach them:\n", note)
		for _, s := range steps {
			fmt.Fprintf(w, "  %s\n", s.line)
		}
	} else {
		fmt.Fprintf(w, "%s, and no step reaches them\n", note)
	}
	// PR6's own line, and the first of the facts that are not steps: the paused
	// runs of the count. A paused chain is in flight the way a running one is
	// (PR12, D3195), so it is in the count above, and what a person cannot read
	// off the count is that some of these runs are held by a pause rather than
	// working. The line says what the count read and no more: a pause that
	// stands, which is what the node and the run's own record say — the contract
	// marks no node resumed (C3), so a chain the orchestrator has already brought
	// back is counted here too, and the line cannot say otherwise (pausedRuns).
	// Nothing is inserted into the count's own sentence for it: that sentence is
	// pinned whole (`docs/gcp-live-checklist.md`, Check 632).
	if paused > 0 {
		fmt.Fprintf(w, "  paused: %s among them, held by a pause that still stands\n", runsPhrase(paused))
	}
	// R5's own line, which is another fact that is not a step: a run that started
	// with the budget off has no session and holds its key already, so nothing
	// this update does reaches it and it finishes. It is printed while this
	// machine's own config still says the
	// word — the state in which such a run can exist, and the state this very
	// update is about to end — and it is this release that owes it, because
	// this is the release that removes the word.
	if e.budgetOffRunFinishes() {
		fmt.Fprintf(w, "  a run that started with the budget off holds its key and finishes\n")
	}
	// MG3's own line, and like R5's it is not a step: this release keeps writing
	// the repository's old access record beside the routing table, and that
	// record is what a run whose image predates the table resolves from (D3196,
	// R25). The count is those runs, read out of the runs' own records, and the
	// line is printed only when there is one: a note over a machine whose runs
	// all carry the table has nothing to say about the record, and a read this
	// door could not complete says nothing at all rather than naming a number
	// nobody read (the count above it, and this note's own rule about a read
	// that failed).
	if old, err := e.runsOnTheOldRecord(ctx, agents); err == nil && old > 0 {
		fmt.Fprintf(w, "  old access record: still written, so %d of these runs resolve from it\n", old)
	}
	return nil
}

// chainIntroduced is when the chain contract reached the integration branch
// (C1, #3460, 2026-11-22 13:05:08 UTC), which is when a job image could first be
// built from a checkout that carries it. A run's gateway is what asks the service
// for the chain — the stage header, and the answer's source and fallback beside
// the body (chain_wire.go) — so a job image built before this holds a gateway that
// sends none of it, and the service has to answer that run out of the old access
// record (MG3). The date is the same kind of fact sealRunnerIntroduced is
// (recipes_skew.go), and this note is its only reader.
var chainIntroduced = time.Date(2026, 11, 22, 13, 5, 8, 0, time.UTC)

// runsOnTheOldRecord counts the runs in flight whose image predates the routing
// table: the runs this release's old access record is written for, and the
// number MG3's line under the note carries.
//
// The image a run is running is the run's own record, which the runner writes at
// bootstrap out of the statement its container carries (runner.ImageInfo, the
// image's own /etc/fugaro/image.json), and never the workflow's current build
// record: an image rebuilt while a run flies leaves that run on the one it
// started with, so the build record would answer about the next launch rather
// than about this one. A record that cannot be read
// and one that names no build time count as predating, which is the direction the
// image gate takes for a job with no build record at all (checkImageSinceDev,
// recipes_skew.go) and the one this note is for: its claim is that the runs in
// flight are not the ones the table strands, and a run this door cannot place on
// this release's image is one it cannot promise that about. A read that failed for
// any other reason is returned, and the note then says nothing about the record.
func (e *initEngine) runsOnTheOldRecord(ctx context.Context, agents map[string]map[string]budget.AgentEntry) (int, error) {
	if e.lc == nil || len(agents) == 0 {
		return 0, nil
	}
	// The same opener, over the same bucket, every other reader of a run's record
	// uses (cloud.go's recordReadURL and openRecordBucket), which is also the pair
	// a test replaces.
	url := recordReadURL(e.lc)
	if url == "" {
		return 0, nil
	}
	b, err := openRecordBucket(ctx, url)
	if err != nil {
		return 0, err
	}
	defer b.Close()
	old := 0
	for slug, runs := range agents {
		for runID := range runs {
			rec, err := runstore.Open(b.Bucket, slug, runID).ReadRecord(ctx)
			if err != nil && !errors.Is(err, runstore.ErrNotFound) {
				return 0, err
			}
			if rec == nil || rec.Image == nil || rec.Image.BuiltAt == nil || rec.Image.BuiltAt.Before(chainIntroduced) {
				old++
			}
		}
	}
	return old, nil
}

// budgetOffRunFinishes is whether this machine's local config says the word that
// is not a budget mode any more, read out of the bytes the run found: a run
// launched under it was launched with no session and with its key in the job
// (R5, D3009), and the word is exactly what tells such a run apart from one the
// note's steps can reach. The loader holds the word as the file writes it
// (localcfg's acceptedBudgetMode), and this asks the same bytes the update's
// rewrite reads, so the two cannot disagree about a config that says it.
func (e *initEngine) budgetOffRunFinishes() bool {
	_, off := localcfg.RemovedBudgetMode(e.old)
	return off
}

func runsPhrase(n int) string {
	if n == 1 {
		return "1 run"
	}
	return fmt.Sprintf("%d runs", n)
}

// pausedRuns counts the runs of this project a pause still holds: the runs that
// ended `paused` and whose node under runstore.PauseNodeRoot the service has not
// cancelled (C3, R26; D3195, D3197). It is the count of the note's paused line.
//
// A node on its own is not a paused run: the node is the service's record that a
// pause was asked for, and the run's own record is what says the run ended
// `paused`. That is the reading every guard makes of a pause (pauseInFlight,
// runs_prstate.go) and the one a resume itself reads it by (PR4: a node whose run
// has not ended paused is resumed no more than a cancelled one), so a node whose
// run ended any other way — the `/pause` of a run that then failed, one whose run
// ended before the service recorded the pause — is a pause that holds nothing,
// and so is one whose record the bucket no longer has: a run's objects live in
// the bucket for a window and age out together, which is what bounds this figure
// over an installation's history.
//
// It is what the door can read, and not a promise that the orchestrator still has
// this resume to do. The contract puts no resumed mark on the node (C3), and the
// run a pause was resumed with is found in the chain instead — the run whose task
// names `resumes` with the paused run's id (PR5) — which this count does not
// walk. A pause that has already been resumed is therefore still counted while
// its record ends `paused`, and the line under the count says what was read — a
// pause that stands — rather than that one is still due.
//
// A suspended run's execution has ended and released its lease (R12), so the
// registry the count otherwise reads holds no entry of it, and a node a launcher
// cancelled is not in flight at all (D3197). A node this release cannot place
// answers nothing: half a node is not a pause (ErrInvalidPauseNode). Every
// failure here is silence — a read that could not be made says nothing rather
// than naming a number nobody read, the rule this door's count is under.
func (e *initEngine) pausedRuns(ctx context.Context, db *rtdb.Client) int {
	var nodes map[string]map[string]json.RawMessage
	if _, err := db.Get(ctx, runstore.PauseNodeRoot, &nodes); err != nil {
		return 0
	}
	if len(nodes) == 0 {
		return 0
	}
	url := recordReadURL(e.lc)
	if url == "" {
		return 0
	}
	b, err := openRecordBucket(ctx, url)
	if err != nil {
		return 0
	}
	defer b.Close()
	n := 0
	for slugKey, runs := range nodes {
		slug, err := budget.Unkey(slugKey)
		if err != nil {
			continue
		}
		for runKey, raw := range runs {
			node, err := runstore.ParsePauseNode(raw)
			if err != nil || node.Cancelled {
				continue
			}
			runID, err := budget.Unkey(runKey)
			if err != nil {
				continue
			}
			rec, err := runstore.Open(b.Bucket, slug, runID).ReadRecord(ctx)
			if err == nil && rec != nil && rec.Status == runstore.StatusPaused {
				n++
			}
		}
	}
	return n
}

// inFlightStep is one step of this update that can reach a run in flight: the
// one short line the note prints for it, naming the step and what it does to a
// run. The whole of why is the clause that gates it (the functions below), its
// row in docs/design/update-in-flight.md, and the step's own code.
type inFlightStep struct {
	line string
}

// inFlightSteps is the steps of THIS update that can reach a run in flight, one
// entry each: the short line the note prints for it, naming the step and what it
// does to a run. An empty result is the update that leaves every run in flight
// exactly where it is, and it is the ordinary case.
//
// Every line is deliberately the question the step itself is gated on rather
// than the whole of its decision (see budgetModeBindsRuns): the note errs
// towards naming a step that may end up doing nothing instead of claiming the
// update cannot reach a run when it can.
func (e *initEngine) inFlightSteps(ctx context.Context, db *rtdb.Client) []inFlightStep {
	var steps []inFlightStep
	// isManaged rather than managed: the package of the same name is read below,
	// for the two versions the creation fact's step names.
	isManaged := e.managedProjectRead(ctx)
	if e.budgetModeBindsRuns(ctx, db, isManaged) {
		steps = append(steps, inFlightStep{
			line: fmt.Sprintf("budget mode: a run meets the raise to %s at its next lease", budget.ModeEnforce)})
	}
	if e.policyReadsLive(ctx, db) {
		steps = append(steps, inFlightStep{
			line: "policy: a cascade project's runs pick up the new policy at their next model call"})
	}
	if e.credentialsWindowOpens(ctx, db) {
		steps = append(steps, inFlightStep{
			line: "credentials service: a call between the node's raise and its move is refused"})
	}
	if e.orchestratorToWrite() {
		steps = append(steps, inFlightStep{
			line: "resident orchestrator: the credentials service's new revision names its account; a call is served by either revision"})
	}
	if e.tokensPublishReachesRuns(ctx, db) {
		steps = append(steps, inFlightStep{
			line: "bitbucket tokens: a proxied call after the publish may use a different token"})
	}
	if e.workspaceServiceRevisionStale() {
		steps = append(steps, inFlightStep{
			line: "workspace service: a new revision drains the one a run started on"})
	}
	if e.bitbucketProxySwitchTurns() {
		steps = append(steps, inFlightStep{
			line: "bitbucket proxy switch: the address is set; the job's mount drops next update"})
	}
	if e.workspaceDropsReach(ctx, db) {
		steps = append(steps, inFlightStep{
			line: "workspace: a removed member or an unshared entry stops being served by the service"})
	}
	if e.markerConverges(ctx) {
		steps = append(steps, inFlightStep{
			line: "creation fact: a command of a run in flight stops taking the gate"})
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
//
// The first half is the node's STATED version, and the chain carrying it: the
// policy stage migrates what projectpolicy.Migratable admits and refuses the
// rest with the republish remedy, so a node it cannot carry is one the stage
// does not raise. That is the same pair init_policy_migrate.go's own write
// reads, which is why the version comes off the document's bytes
// (projectpolicy.ReadStatedVersion) rather than from a decoded node: from the
// step that is not shape-free, the node every installation holds while the
// upgrade runs is one Read refuses and ReadCarriedForward answers for with this
// release's own version, and neither reports whether the policy stage will
// raise it.
func (e *initEngine) credentialsWindowOpens(ctx context.Context, db *rtdb.Client) bool {
	// The version the node STATES, not the one a decoded node reports: the
	// question is whether this run's policy stage raises it, and from the step
	// that is not shape-free no reader of the node answers that — Read refuses
	// the node every installation holds while the upgrade runs, and
	// ReadCarriedForward returns this release's own version.
	v, found, err := projectpolicy.ReadStatedVersion(ctx, db)
	if err != nil || !found || v >= projectpolicy.Version || !projectpolicy.Migratable(v) {
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

// bitbucketProxySwitchTurns is whether this update turns the credentials
// service's Bitbucket proxy on for the project's local config and publishes the
// block it implies (S6, D2306, R7): the cheap half of the flip
// (bitbucketProxyFlip), which the policy stage's plan asks again as the whole of
// its decision.
//
// A run in flight is not proxied by this write. Its job's execution carries the
// address the flip needs — and a repository whose job does not carry one is not
// proxied however the node reads — and its runner computed proxiedness ONCE, at
// bootstrap, from the node it memoized (proxiedBitbucket, internal/runner), so
// the run going on now keeps the credential it started with, mounted token and
// all. What the flip sets is the ADDRESS the repository's jobs are admitted by
// (the node's bitbucket_token block, derived from the entry the same run writes)
// and not the job's own shape: the mount comes off at the NEXT update's
// repository stage (the drop, update_proxied.go), so the launch that fetches its
// credential from the service is the one after that update and not this step's
// neighbour. The line says what this run leaves behind — the address, and the
// mount still on the job — because that is the state a person watching a project
// converge has to read correctly: nothing of the run in flight changes, and a
// launch under the mount it still holds is a run that goes on mounting its own
// token.
//
// It asks the cheap half rather than the whole flip, as every clause here asks
// the question its step is gated on and not the whole of its decision: whether
// the credentials service's bundle holds a token to forward (R7) and whether
// the config's entry can be published as it stands are the stage's own reads,
// and both leave the switch off with a line of their own. Naming the step over
// an update whose flip is then held back is the direction this note errs in —
// and the other direction is the one it must never take, since a project that
// does turn the switch on has every launch after it proxied.
func (e *initEngine) bitbucketProxySwitchTurns() bool {
	_, turn := e.bitbucketProxyFlip()
	return turn
}

// workspaceServiceRevisionStale is whether this update replaces the workspace's
// own credentials service with a revision of this release's image — R11's second
// half, "a new revision of the service, which drains the old one" — read over
// the workspace's OWN root, which is another root of this installation and the
// one whose tfvars carry the block (workspace_create.go's serviceBlock).
//
// The step is the drain and nothing else. A run in flight keeps the environment
// and the route it started with (R11's first sentence): the service serves a
// member's calls over the same routes on either revision, and what a replacement
// does to a call in flight is drain the connection it holds when the old
// revision is taken out of service. That is Cloud Run's own drain and not a
// rewrite of anything a run holds — no record, ledger or counter is touched —
// which is why the line says "drains" and names the revision, rather than
// claiming the run is stopped or unaffected.
//
// Three states answer false, and each is a different fact:
//
//   - an installation that is not a workspace's own, and a split one, deploy no
//     service at all (workspaceSplitFirebase: the skip's own reason says so) —
//     the two predicates the stage itself runs on, read here from the same two
//     functions rather than re-derived;
//   - a session or a development build deploys nothing (serviceBlock's own two
//     constants): the plan a session reads names a move it may not make;
//   - a workspace whose root holds no block yet. This update DEPLOYS the service
//     for the first time, and a revision nobody was calling drains nothing: the
//     line would be a drain the operator could not find in anything the update
//     does.
//
// What is left is the recorded image, compared as the deploy compares it
// (credentialsImageStale): a block whose image is this release's — or a NEWER
// release's, on a second machine of one installation upgraded later — is no
// revision to move, and one that names no release at all, or one behind, is the
// revision this run replaces. A file the door cannot read answers false: this
// note is information, and a fact it cannot read is not permission to name a
// step.
func (e *initEngine) workspaceServiceRevisionStale() bool {
	lc := e.lc
	if lc == nil || !workspaceDeclared(lc) || workspaceSplitFirebase(lc) != "" {
		return false
	}
	if agentMarker(os.Getenv, e.r.stdinTerminal()) != "" || releaseVersion() == "" {
		return false
	}
	dir, err := infra.WorkspaceWorkdir(os.Getenv, lc.GCPProject)
	if err != nil {
		return false
	}
	vars, err := infra.ReadWorkdirVars(dir, "workspace")
	if err != nil {
		return false
	}
	svc := recordedWorkspaceService(vars)
	if svc == nil {
		return false
	}
	return credentialsImageStale(releaseVersion(), svc.Image)
}

// workspaceDropsReach is whether this run's workspace write can take a member, or
// a pool entry, out of the published credentials node that a run in flight is
// served by (R11): a member row the local table no longer declares, or an entry
// the local shared list no longer holds.
//
// The workspace's own service re-reads that node as it serves a call (R13), so
// the run of a member it no longer resolves — or a run whose repository the pool
// offers no credential to any more — is refused at the service's next call, with
// the refusal that names the workspace owner's `fugaro update`. That is the one
// reach this stage has on a run in flight, and it is why the line is about who
// is served rather than about the run being untouched.
//
// What it asks is the pair of names, and not the stage's own decision: the node
// as it stands, read here before any stage of this run has written, against the
// member table and the shared list of this machine's config. Whether the stage
// gets as far as publishing at all, whether the removal is a whole row or one of
// its accounts, and what else the same write states are the stage's own reads,
// and the diff it prints names every one of them. A node that is not there —
// every member's installation, and every owner who has not published yet — and a
// read that failed are silence, as every other read in this file answers.
func (e *initEngine) workspaceDropsReach(ctx context.Context, db *rtdb.Client) bool {
	if e.lc == nil || e.lc.Name == "" {
		return false
	}
	var raw json.RawMessage
	found, err := db.Get(ctx, projectpolicy.PathWorkspaceCredentials(e.lc.Name), &raw)
	if err != nil || !found {
		return false
	}
	node, _, err := projectpolicy.DecodeWorkspaceCredentials(raw)
	if err != nil || node == nil {
		return false
	}
	members := map[string]bool{}
	if e.lc.WorkspaceMembers != nil {
		for name := range *e.lc.WorkspaceMembers {
			members[name] = true
		}
	}
	for _, row := range node.Members {
		if !members[row.Project] {
			return true
		}
	}
	shared := map[string]bool{}
	if e.lc.WorkspaceShared != nil {
		for name := range *e.lc.WorkspaceShared {
			shared[name] = true
		}
	}
	for _, s := range node.Shared {
		if !shared[s.Name] {
			return true
		}
	}
	return false
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
