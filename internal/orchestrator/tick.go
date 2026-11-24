// The reconcile tick: one cycle of the daemon's loop (O7).
//
// A tick is level-triggered (D1229). It reads the node, reads what the runs
// behind the node's intents reported, folds the finished ones into the items,
// plans (O6), carries the plan out (O6's starter, intent first), triages the
// runs the ladder's threshold has passed (O14b) and commits. There is no
// cursor and no replay: a tick derives its whole answer from the node as it
// now stands, so a tick that ran twice over one node is the same as one that
// ran once (TestTickIsIdempotentOnRepeat), and a daemon that crashed mid-cycle
// resumes by running the next tick.
//
// Nothing here calls a model, a cloud or a person (D1221): the state is the
// store's (O3), the runs are the reader's, a launch is the launcher's, the
// clock is the database server's (D1230), and the one thing a tick says to
// somebody goes through a Notifier. That is what makes the loop a unit test
// rather than a cloud experiment.

package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// Tick is one daemon's cycle and everything it acts through. It is a value
// rather than a function because the loop the plan names — `Tick(ctx, Store,
// RunViewReader, Launcher, Notifier)` — has to carry four things a cycle reads
// and must not reach for: the caps and the kill switch a plan is bounded by,
// the mint that names a run, and the server clock every stamp comes from. O8's
// loop holds one of these, wakes it, and re-arms the breaker it was given.
//
// Nothing here sleeps, waits, retries or reads a clock of its own: one call to
// Step is one cycle, and the waiting between cycles is the loop's.
type Tick struct {
	// Store is the state node the cycle reads and writes.
	Store Store
	// Nodes are the nodes beside the state node that this cycle works: one per
	// imported plan and the standing backlog (D2735), each a Store of its own
	// (NewNode with PlanShard or ShardBacklog). The cycle loads every one of
	// them, folds each one's finished runs into its items and plans launches
	// from each, under the caps counted over all of them at once: max_running
	// and the spawn caps are one project's allowance and never one per node
	// (D2743) — and under the daemon's pause, which any of them carrying an
	// unanswered question puts on all of them.
	//
	// A nil or empty slice is the state node alone, which is the daemon this
	// package had before there were nodes beside it: nothing about that daemon
	// reads differently. Who builds them is the daemon's, and a plan imported
	// after a tick was built is one the daemon hands the next cycle's tick.
	Nodes []Store
	// Runs reads what the runs behind the intents reported. It is asked about
	// the run ids the state recorded, and about no others.
	Runs RunViewReader
	// Launcher starts one run for one attempt, under the run id the state has
	// already recorded.
	Launcher Launcher
	// Claims is the project's claims (claim.go, D3209): what a cycle takes on
	// an item before its work starts, re-stamps while it holds it and settles
	// once the item is finished — a plan task's claim marked finished and left
	// standing, a work item's released — so that two orchestrators of one
	// project never start a run for one plan task or one backlog item. Nil is
	// a cycle that claims nothing — a process that makes no write to this
	// subtree — and then every item is this daemon's as it was before claims.
	Claims ClaimGuard
	// Mint mints that run id. It is the daemon's (internal/task in O8): this
	// package knows no run ids of its own.
	Mint MintFunc
	// Caps are the guardrails the cycle plans under. KillSwitch comes from
	// whoever can read the budget (O8) and is on whenever it could not be
	// read; a Tick built with the zero Caps starts nothing, which is the
	// direction a guard that was never set fails in.
	Caps Caps
	// Breaker counts the cycles in a row that ended with nothing done, and the
	// cycle that reaches the threshold asks a person and leaves the question
	// on the node for the loop to re-arm it over (breaker.go). A nil one
	// counts nothing and asks nothing.
	Breaker *Breaker
	// Notifier is where a finished run and a question for a person go, or nil
	// for a daemon that tells nobody.
	Notifier Notifier
	// Triage is what this cycle acts on the long-run rule through (O14b): the
	// readings, the two actions an outcome can ask for that are not a state
	// write, and the audit log every outcome leaves a line in. Left unset the
	// cycle triages nothing at all, which is the direction a daemon that was
	// given no host, no run storage and no log fails in.
	Triage TriageSeams
	// Ladder is what this cycle walks the escalation ladder through (E10a):
	// the readings the decision's facts are gathered from, the launcher a rung
	// starts its run through, the comment a move posts and the log every move
	// leaves a line in. Left unset the cycle escalates nothing at all, which is
	// the direction a daemon that was given no run store, no provider and no
	// log fails in.
	Ladder LadderSeams
	// Decide is what this cycle reaches the decision agent through (O13): the
	// agent itself and the log the fence is read from. Left unset the cycle
	// judges nothing, which is where a daemon that was not started with
	// `--decide` stands: the flag is off by default, and a daemon without it
	// makes no model call however many judgement cases its node carries.
	Decide DecideSeams
	// Audit is O3's audit log as the trip writes it (O19): one line per run a
	// workspace's refusal stopped, under the id its subject and transition
	// derive, so the person the question waits on can read which run was
	// refused and when. It is the log itself and not an interface of its own —
	// the trip only appends, and every daemon that has a node to write has a
	// log to append to. A nil one writes no line, which is the direction a log
	// that was never wired fails in: the pause and the question are the node's
	// and neither depends on the line (audit.go, "the log is evidence, not the
	// fence").
	Audit *Audit
	// Labels is what this cycle keeps the pull request labels of its own runs
	// through (D2432): the host of each repository, the pull request behind
	// each run, and the log every failure leaves a line in. Left unset the
	// cycle labels nothing at all, which is where a daemon with no provider or
	// no path to the credentials service stands — and nothing a label does
	// fails a cycle (docs/forge-labels, "best-effort").
	Labels LabelSeams
	// Merge is what this cycle reads the pull requests of the items waiting on
	// a merge through (G3, D2705): the reader of each repository's pull request
	// state and the log a read that failed leaves a line in. Left unset the
	// cycle reads nothing at all, which is the same direction Labels fails in —
	// an item whose pull request is never read stays in `review`, and nothing
	// about a merge fails a cycle.
	Merge MergeWaitSeams
	// Issues is what this cycle ingests the project's open issues through
	// (B4, D2718–D2720, D2728, D2742): the repositories `issue_sync` names,
	// read at most once per cadence, as the items the standing backlog's node
	// takes. Left unset the cycle ingests nothing at all, which is the same
	// direction a project that configures no adapter and a daemon whose path to
	// the credentials service could not be built both fail in: nothing about an
	// issue fails a cycle, and the backlog holds what people filed.
	Issues IssueSyncSeams
	// Footprints is what the cycle knows of the items' expected paths, keyed by
	// the item's own id: the plan graph's footprint for the task an item stands
	// for, which is what a run's entry naming a path is matched against when the
	// fold reads what that run reported about the graph (E3, D2709, D2710).
	// Left unset the fold matches no path at all — and every daemon of this
	// release leaves it unset, because a footprint is written into the plan's
	// own graph in the operator's checkout and no node of the orchestrator
	// subtree carries one (F33): the part of the fold that names an item by its
	// plan task lands either way, and the wiring that reads a graph and hands
	// its footprints here is the daemon's own.
	Footprints Footprints
	// Now is the database server's clock (rtdb.ServerNow). Every stamp a cycle
	// writes comes from it, and a cycle that cannot read it refuses rather
	// than stamping anything with this machine's clock (D1230).
	Now ServerNow
}

// Report is what one cycle did, for the loop's log and for the tests.
type Report struct {
	// Outcome is how the cycle ended, in the breaker's vocabulary.
	Outcome CycleOutcome
	// Folded is how many finished runs the cycle wrote into the items.
	Folded int
	// Merged is how many items the cycle's merge read moved out of `review`:
	// done for one whose pull request merged, failed for one that closed
	// without merging (D2705). An item left in `review` because its pull
	// request is still open, or because the read did not happen, is not
	// counted.
	Merged int
	// Carried is how many of the plan's actions the starter carried out — a
	// launch, a retry, a re-issue or an abandonment.
	Carried int
	// Skipped is how many of the plan's actions the cycle skipped because the
	// item is another orchestrator's: the claim on it is held elsewhere
	// (ErrItemClaimed, D3209), so the item is that daemon's work and this cycle
	// carries nothing for it and fails nothing over it. It spends no cap room
	// either: the plan leaves the items this cycle has learned are another
	// orchestrator's out before the caps are read, and a skip hands its slot
	// back, so the room goes to the work that is this daemon's own (carry).
	//
	// It is not a move and it is not a cycle that did nothing. Two orchestrators
	// of one project are the deployment the claim exists for (D3206, D3207), and
	// there the loser of a claim skips every item the winner holds: read as a
	// cycle that achieved nothing, three of those in a row would reach the
	// breaker's threshold and pause the node — refusing this daemon's own
	// unclaimed items too, over a daemon doing exactly what the deployment asks
	// of it (D1228).
	Skipped int
	// Triaged is how many runs the cycle judged past the threshold and acted
	// on. A run the rule was read about but did not have an outcome for — one
	// under the threshold — is not counted, and neither is one whose line the
	// log already carries.
	Triaged int
	// Escalated is how many moves the cycle's ladder made: a rung a run was
	// started for, and a rung recorded for the hook the rungs past rung 2
	// attach through. The floor, rung 1, is not a move — the item's own loop
	// is what resumes it — and neither is an item the ladder asked a person
	// about, nor a wait a hook retried (Waited).
	Escalated int
	// Waited is how many waits the cycle retried: a rung 3 hook that read a
	// reading still moving and left the item where it stands for the next cycle
	// to read again (LadderWaiting, MergeOutcome.Retry). It is not a move and
	// it is not a question — nothing was launched, landed or asked — and it is
	// what keeps such a cycle from being read as one that did nothing: the
	// daemon is waiting on something the reading says is going, and three of
	// those in a row would reach the breaker's threshold and pause the node over
	// a wait the item's own question ends (D1228, mergeRetryWindow).
	Waited int
	// Decided is how many judgements the cycle made: the cases it handed the
	// decision agent and had answered. It is one a cycle at most (the decide
	// step is one call per cycle by design).
	Decided int
	// DecideOutcome is how the call this cycle made reads for the breaker —
	// O12's own reading of it (G10) — and empty for a cycle that made none.
	DecideOutcome CycleOutcome
	// DecideErr is that call's own failure. It never fails the cycle: the fold,
	// the carry and the triage that came before it stand, and what a judgement
	// the agent could not make costs is the cycle's own outcome, which the
	// breaker counts.
	DecideErr error
	// NotifyErr is a notification a sink would not take. It never fails the
	// cycle: what a cycle must not lose is the write behind the notification.
	NotifyErr error
	// LadderErr is a ladder comment a host would not take, which never fails
	// the cycle for the same reason: the comment is a channel and the ledger
	// is the record.
	LadderErr error
}

// Step runs one cycle.
//
// An error is a cycle that did not finish — a node it could not read, a write
// that was refused, a launcher that would not start — and what it leaves
// behind is what it committed before that. The intent of a launch that did not
// happen stands, which is the record the next tick re-issues (D1231); a node
// another writer moved is not written over at all. An error is a failed cycle
// whatever the report's outcome says.
func (t *Tick) Step(ctx context.Context) (Report, error) {
	if err := t.ready(); err != nil {
		return Report{Outcome: CycleFailed}, err
	}
	c := &cycle{tick: t}
	err := c.run(ctx)
	if err != nil {
		c.rep.Outcome = CycleFailed
	}
	if t.Breaker != nil && t.Breaker.Observe(c.rep.Outcome) {
		if rerr := c.ask(ctx); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}
	return c.rep, err
}

func (t *Tick) ready() error {
	switch {
	case t.Store == nil:
		return errors.New("orchestrator: a tick needs the store its state node is read and written through")
	case slices.Contains(t.Nodes, nil):
		// A node with no store is a node nothing can be read or written
		// through, and a cycle that skipped it would count its caps over a
		// record it does not have (D2743).
		return errors.New("orchestrator: a tick needs the store of every node it works, and one of the nodes it was handed has none")
	case t.Runs == nil:
		return errors.New("orchestrator: a tick needs a run view, and one it cannot read is not an empty one")
	case t.Mint == nil:
		return errors.New("orchestrator: a tick needs a mint, and a run id this package invents is a run nobody recorded")
	case t.Now == nil:
		return errors.New("orchestrator: a tick needs the database server's clock, and a stamp from this machine is not one (D1230)")
	}
	return nil
}

// IssueSyncer is the project's issue sync as one cycle reaches it (B4, D2718):
// the repositories `issue_sync` names, each read through the credentials
// service's issue route at most once per cadence (D2742), as the one patch the
// standing backlog's node takes this cycle.
//
// It is a seam and not an import because the sync lives in internal/issuesync
// and this package knows nothing of GitHub, of a route or of a config: what a
// cycle hands over is the backlog node as it stands, the nodes beside it — the
// external id an item is keyed on is unique across all of them (D2728) — and
// the cycle's own reading of the server's clock, and what it takes back is a
// patch it applies through its own fence like any other.
type IssueSyncer interface {
	Sync(ctx context.Context, backlog State, others []State, now time.Time) (Patch, error)
}

// IssueSyncLog is where the issue sync's failures go: one line per repository
// per cadence. Its shape is the merge read's (MergeWaitLog) and it is an
// interface of its own for the reason that one is — a daemon wires the two
// separately, and a line about an issue list in the merge read's log would be
// read as the merge read's own.
type IssueSyncLog interface {
	Warn(ctx context.Context, line string)
}

// IssueSyncSeams is what one cycle reaches the issue sync through: the sync
// itself, the store of the node every op it answers with lands on, and the log
// a repository that could not be read leaves a line in. All three or none — a
// Tick built with fewer reads no issues at all, which is what a daemon whose
// `issue_sync` is unconfigured or whose path to the credentials service could
// not be built is: the backlog then holds what people file and no more.
type IssueSyncSeams struct {
	// Sync answers one cycle's turn at the ingest.
	Sync IssueSyncer
	// Backlog is the store of the standing backlog's node (D2735): the node the
	// ops land on, and the node identity that tells this cycle which of the
	// nodes it works is that one. It is a Store and not an index because a
	// daemon builds the node once — the same one it may also hand to the tick's
	// Nodes — and an index would be a second list to keep in step with it.
	//
	// The ops reach it through this store either way: where the node is one this
	// cycle works, the step reads and commits it as any other step does, and
	// where it is not — a daemon working the state node alone — the step reads
	// and writes it here, once, under the ETag it read it at (see syncIssues).
	Backlog Store
	// Log is where a repository that could not be read leaves its one line.
	Log IssueSyncLog
}

// Set reports whether every seam is there.
func (s IssueSyncSeams) Set() bool { return s.Sync != nil && s.Backlog != nil && s.Log != nil }

// node is one node of the project's orchestrator subtree as one cycle works it:
// the store it is read and written through, the state this cycle read, the ETag
// its next write has to carry, and whether it is still this cycle's to write
// (D2735).
type node struct {
	store Store
	state State
	etag  ETag
	fresh bool
	// refused is which of the workspace's refusals a run of this node's own came
	// back with, and "" when none did: the cycle asks a person about it at once
	// rather than after BreakerThreshold more attempts into the same refusal
	// (O19, D2022 (a)).
	refused string
	// refusedRun is the run that refusal came from, which is the audit line's
	// subject: what the log records is which run the workspace stopped, and the
	// reason alone does not name it.
	refusedRun string
}

// cycle is one tick's work over the nodes of one project: every node it works,
// the one it is working on at the moment with the state as it last read or
// wrote it, and the report being built.
//
// A cycle works one node at a time, and the loop in run moves it from node to
// node: state, etag, fresh and the refusal below are always the node at `at`,
// which is what every step reads and commits to. What is not one node's is what
// the whole project is planned under: the caps are counted over all of them and
// the launches of all of them spend one allowance (counts and planned, D2743),
// and the pause that stops the daemon is the one any of them carries (paused).
type cycle struct {
	tick *Tick
	rep  Report
	// nodes is every node this cycle works, the state node first and the plan
	// and backlog nodes after it in the order the tick carries them; at is which
	// of them the four fields below belong to.
	nodes []node
	at    int
	// state, etag, fresh, refused and refusedRun are the node at `at`: the state
	// as this cycle last read or wrote it, the ETag the next write has to carry,
	// whether that node is still this cycle's to write, and the workspace refusal
	// a run of its own came back with.
	state      State
	etag       ETag
	fresh      bool
	refused    string
	refusedRun string
	now        time.Time
	// counts is what the caps are read against: the intents of every node as
	// this cycle's folds and merge left them. It is read once and not per node,
	// so that a launch this cycle has already decided is counted by the tally
	// (planned) and not a second time by a count that now sees its own write
	// (D2743).
	counts Counts
	// planned is how many launches this cycle has decided, counted over every
	// node it works: one project's allowance, not one per node (D2743).
	planned int
	// paused is the daemon's pause as the cycle plans: the question a tripped
	// breaker or a workspace's refusal raised and nobody has answered yet. It
	// is read over every node the cycle works for the reason the counts are —
	// the question is written to the state node whatever node the run behind it
	// belonged to, so a plan node or the backlog reading its own state would
	// find no pause and start work into the refusal the pause was raised for
	// (O19, D2022 (a)).
	paused bool
	// held is the claim of every item another orchestrator holds, by claim path
	// (claimKey), as this cycle has found it out: an action the starter refused
	// for ErrItemClaimed (D3209). The plan leaves those items out, so a skip
	// spends no room and the node is planned again for the room it leaves
	// (carry). It is one cycle's own reading and nothing is kept from it, so an
	// item whose claim was settled between two cycles is that cycle's work
	// again.
	held map[string]bool
}

// bind makes the node at i the one this cycle is working on.
func (c *cycle) bind(i int) {
	c.at = i
	n := c.nodes[i]
	c.state, c.etag, c.fresh = n.state, n.etag, n.fresh
	c.refused, c.refusedRun = n.refused, n.refusedRun
}

// settle writes the node the cycle has been working on back into the list, so
// that what reads every node — the counts above all — sees what this cycle has
// done to it.
func (c *cycle) settle() {
	c.nodes[c.at] = node{
		store: c.nodes[c.at].store, state: c.state, etag: c.etag, fresh: c.fresh,
		refused: c.refused, refusedRun: c.refusedRun,
	}
}

// settled writes the node back and hands the step's error on, so that a step
// that ended the cycle leaves the node as far as it got.
func (c *cycle) settled(err error) error {
	c.settle()
	return err
}

// states is what every node reads as, the one being worked on included: what
// the caps are counted over (D2743).
func (c *cycle) states() []State {
	c.settle()
	out := make([]State, len(c.nodes))
	for i, n := range c.nodes {
		out[i] = n.state
	}
	return out
}

// anyNode reports whether any node this cycle works is one f holds for.
func (c *cycle) anyNode(f func(State) bool) bool {
	for _, s := range c.states() {
		if f(s) {
			return true
		}
	}
	return false
}

// syncIssues is one cycle's turn at the issue sync (B4, D2718–D2720, D2728,
// D2742): the backlog node's own patch of ingested issues, applied through this
// cycle's fence like every other write.
//
// The sync answers for one node of the project's — the standing backlog's
// (D2735) — and it reaches it through the seam's own store, which is the node
// the ops land on (IssueSyncSeams.Backlog). Where the tick carries that node
// among the nodes this cycle works, the step reads and writes it exactly as the
// other steps do; where it does not — a daemon working the state node alone,
// which is every daemon this release starts — the node is read and written
// through that same store here, so that the ingest runs for the project rather
// than for a node nothing carries. The two paths are one rule: the document is
// read, patched and written once, under the ETag it was read at.
//
// The daemon's pause does not stop it: what it writes are queued, unapproved
// items, so it starts nothing and is the person's view of the work rather than
// a launch into a refusal (O19). The read is the sync's own cadence, per
// repository (D2742), and it is not read here.
//
// Four conditions leave the node exactly as it stands: a node this cycle works
// and another writer has moved (fresh is false, so a patch built from this
// cycle's reading would be refused anyway), one the seam's store will not answer
// for at all, one already over its size target (OverCap — the ingest reads the
// room it has from the state, and a node past its target has none), and a patch
// its own fence refuses. Each of them is the same rule: this cycle does not
// write a document it cannot derive from.
//
// What this step reads and writes it reads and writes through the node's own
// entry, and it moves the cycle onto it only for the write it makes: the cycle
// is on the state node when this step is reached and the steps after it — the
// counts, the launches, the decisions and the labels — read and write that node,
// so a turn that writes nothing has to leave the cycle where it found it.
func (c *cycle) syncIssues(ctx context.Context) error {
	seams := &c.tick.Issues
	if !seams.Set() {
		return nil
	}
	i := c.backlogIndex(seams.Backlog)
	backlog, etag := State{}, ETag("")
	switch {
	case i >= 0:
		if !c.nodes[i].fresh {
			return nil
		}
		backlog, etag = c.nodes[i].state, c.nodes[i].etag
	default:
		read, got, err := seams.Backlog.Load(ctx)
		if err != nil {
			// One line and nothing else: a node that could not be read is a
			// wait until the next cycle, not a cycle that failed — and a write
			// this step cannot derive from the document is one it does not make.
			warnIssues(ctx, seams, fmt.Sprintf("the standing backlog could not be read: %s", safetext.Field(err.Error())))
			return nil
		}
		backlog, etag = read, got
	}
	if OverCap(backlog) {
		return nil
	}
	// Every node but this one: the external id is unique across the project's
	// items (D2728), and the backlog's own items are the node the sync was
	// handed. A node this cycle does not work is not one of them, and its items
	// are not seen by this turn.
	others := make([]State, 0, len(c.nodes))
	for j, n := range c.nodes {
		if j != i {
			others = append(others, n.state)
		}
	}
	patch, err := seams.Sync.Sync(ctx, backlog, others, c.now)
	if err != nil {
		// One line and nothing else: a repository that could not be read is a
		// wait until the next cadence, not a cycle that failed (D2742). The
		// sync answers with no ops at all alongside an error, so nothing of
		// that repository's turn is written.
		warnIssues(ctx, seams, fmt.Sprintf("the issues of the backlog could not be read: %s", safetext.Field(err.Error())))
		return nil
	}
	if len(patch.Ops) == 0 {
		return nil
	}
	next, err := Apply(backlog, Patch{BaseRev: backlog.Rev, Ops: patch.Ops}, Actor{Class: Daemon, Now: c.now})
	if err != nil {
		// A patch this build's own seam built and this release's fence refuses
		// is a bug and not an outage: it is refused rather than written, the
		// node keeps what it had, and the line says so.
		warnIssues(ctx, seams, fmt.Sprintf("the patch the issue sync answered with was refused: %s", safetext.Field(err.Error())))
		return nil
	}
	if i < 0 {
		// The cycle does not work this node: the write is the store's own and
		// the cycle's own nodes are untouched — the node the ops landed on is
		// not one of them, so nothing this cycle reads afterwards has to see it.
		_, err := saveNode(ctx, seams.Backlog, next, etag)
		return err
	}
	c.bind(i)
	if err := c.commit(ctx, next); err != nil {
		return err
	}
	c.settle()
	c.bind(0)
	return nil
}

// warnIssues leaves the issue sync's one line in its log.
func warnIssues(ctx context.Context, seams *IssueSyncSeams, line string) {
	seams.Log.Warn(ctx, line)
}

// backlogIndex is which of the nodes this cycle works is the backlog node, or
// -1 where none of them is.
//
// The two stores are one node when they are the same store, compared by path
// where the store is one of this package's nodes and by identity otherwise: the
// daemon builds the backlog's node once and hands it to both the tick and the
// seam, and a store this cycle cannot place is left alone rather than guessed
// at — writing the wrong node's document is the one outcome nothing here may
// have.
func (c *cycle) backlogIndex(backlog Store) int {
	for i, n := range c.nodes {
		if sameStore(n.store, backlog) {
			return i
		}
	}
	return -1
}

// sameStore reports whether two stores are one node.
func sameStore(a, b Store) bool {
	if a == nil || b == nil {
		return false
	}
	if p, ok := a.(interface{ Path() string }); ok {
		if q, ok := b.(interface{ Path() string }); ok {
			return p.Path() == q.Path()
		}
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	return ta == tb && ta.Comparable() && a == b
}

// load reads every node this cycle works — the state node and the plan and
// backlog nodes beside it — each under its own ETag, so each is written back
// under its own precondition (D2735). A node of any of them that cannot be read
// ends the cycle: this release does not launch from a document it could not
// read, and the caps cannot be counted over a node nobody read (D2743).
func (c *cycle) load(ctx context.Context) error {
	stores := make([]Store, 0, 1+len(c.tick.Nodes))
	stores = append(append(stores, c.tick.Store), c.tick.Nodes...)
	c.nodes = make([]node, 0, len(stores))
	for _, st := range stores {
		s, etag, err := st.Load(ctx)
		if err != nil {
			return err
		}
		c.nodes = append(c.nodes, node{store: st, state: s, etag: etag, fresh: true})
	}
	// The state node is the one the cycle starts on, and the one a step that
	// reads every node — the run ids and the counts — settles first.
	c.bind(0)
	return nil
}

// openRunIDs is every node's attempt that has not reported. One read answers
// for all of them: a run belongs to exactly one node, the one whose intent
// records it, so a node's ids are nobody else's.
func (c *cycle) openRunIDs() []string {
	var ids []string
	for _, s := range c.states() {
		ids = append(ids, openRunIDs(s)...)
	}
	return ids
}

func (c *cycle) run(ctx context.Context) error {
	if err := c.load(ctx); err != nil {
		return err
	}
	now, ok := c.tick.Now()
	if !ok {
		// A cycle that cannot read the server's clock writes nothing at all
		// (D1230): the intents' stamps are what the caps count over, and a
		// reading from this machine is a count nothing derived can be trusted
		// with.
		return ErrNoServerTime
	}
	c.now = stamp(now)

	reports, err := c.tick.Runs.Runs(ctx, c.openRunIDs())
	if err != nil {
		return err
	}
	view := viewOf(reports)

	// Every node is folded before any of them is planned: a run that ended in a
	// plan's node or the backlog's is what frees the room this cycle has, and
	// the caps are counted over what the nodes say once the folds have landed
	// (D2743). The state node is folded first, and its fold is the one that
	// reads a workspace's refusal out of every node's reports and asks the
	// person about it: the question is the daemon's own node's, as it was when
	// the daemon had one, and so is the audit line beside it.
	c.bind(0)
	if err := c.fold(ctx, reports); err != nil {
		return c.settled(err)
	}
	// A run the workspace refused stops this cycle before it plans anything: the
	// fold has just put the question on the node in the same patch as the
	// outcome, so the plan this cycle derives already refuses over the pause
	// (O19, D2022 (a)) and the next item is not started into the same refusal.
	// What is left is the line the log keeps of which run it was. Every other
	// question is the streak's and comes after the cycle has ended, where Step
	// asks it (Observe).
	if c.refused != "" {
		if err := c.auditRefusal(ctx); err != nil {
			return c.settled(err)
		}
	}
	c.settle()
	for i := 1; i < len(c.nodes); i++ {
		c.bind(i)
		if err := c.foldNode(ctx, reports); err != nil {
			return c.settled(err)
		}
		c.settle()
	}

	// The merge read comes after the fold and before the plan, and it reaches
	// every node: the fold has just left every item whose run succeeded with a
	// pull request in `review` (D2705), wherever it is, and an item whose pull
	// request has since merged is `done` before the plan is made, so the work
	// that depends on it starts in this cycle rather than the next one (D2735).
	// The state node's own turn follows, in the order it has always had: what it
	// launches, then the ladder, the long-run rule, the decision agent and the
	// labels.
	c.bind(0)
	if err := c.merge(ctx); err != nil {
		return c.settled(err)
	}
	// The issue sync follows the merge and reaches the backlog node alone: the
	// merge read has just said which items are done, and the ingest is what
	// turns the project's open issues into items of the backlog beside them
	// (D2720). It lands before the plan is made, so the room the fold and the
	// merge freed is room the ingest may use in this same cycle — and the items
	// it writes are queued and unapproved, so nothing here starts a run.
	// Nothing about it fails a cycle: a repository that could not be read is a
	// line in the log and a wait until the next cadence (D2742).
	if err := c.syncIssues(ctx); err != nil {
		return c.settled(err)
	}
	// The counts are read once, here, and held for every node the cycle plans —
	// the state node and the plan and backlog nodes after it. Reading them again
	// per node would count the launches this cycle has itself committed, which
	// are the tally's, and the nodes planned last would spend the allowance
	// twice (D2743). Every fold has landed by now, so a run that ended in this
	// cycle is what this reading leaves out.
	c.counts = CountsOfAll(c.states(), c.now)
	// And the pause is read with them, over the same nodes: the question that
	// stops the daemon is on the state node whatever node the run behind it
	// belonged to, so a plan node or the backlog that read the pause off its own
	// state would keep launching into the refusal it was raised for (O19,
	// D2022 (a)) — and a workspace's refusal folded above is one this same cycle
	// has to stop on, which is why the reading is here and not at the load.
	c.paused = c.anyNode(func(s State) bool { return BreakerOpen(s, c.now) })
	if err := c.carry(ctx, view); err != nil {
		return c.settled(err)
	}
	if err := c.escalate(ctx); err != nil {
		return c.settled(err)
	}
	if err := c.triage(ctx); err != nil {
		return c.settled(err)
	}
	if err := c.decide(ctx, view); err != nil {
		return c.settled(err)
	}
	c.label(ctx)
	c.settle()

	// The nodes beside it: the launches their own items are ready for, under the
	// same allowance and the tally this cycle has already spent (D2743). The
	// state node is planned first, so a project short of room spends it on the
	// work a person queued there before the imported plans and the backlog.
	for i := 1; i < len(c.nodes); i++ {
		c.bind(i)
		if err := c.carry(ctx, view); err != nil {
			return c.settled(err)
		}
		c.settle()
	}

	// The claims of the finished items are settled last, once every write that
	// finished them has landed (D3209): an item the fold's own reading of a run,
	// the merge read or a split moved to done or cancelled is work nobody is on
	// any more. What settling it means is the item's identity (claim.go): a plan
	// task's claim is marked finished and stays, because a rival whose own node
	// still carries the task approved would take a freed node as unclaimed work
	// and start a second run of it, while a work item's claim comes off, its id
	// being this one node's own. It is last because it is about what landed —
	// and it is every finished item the nodes carry and not only what moved in
	// this cycle, which is what settles a claim this daemon's earlier cycle
	// failed to settle or died before settling.
	if err := c.releaseClaims(ctx); err != nil {
		return err
	}
	c.rep.Outcome = c.outcome()
	return nil
}

// releaseClaims settles this orchestrator's claim on every item a node of this
// project has finished, over the nodes as they now stand, and re-stamps the
// claims it still holds (Claim.Refresh, D3209). Every finished item and not
// only the ones this cycle moved, so that a settlement that failed, and one a
// cycle that died after the item's own write had landed never made, are made by
// the next cycle that reads the node. A claim that cannot be settled fails the
// cycle: the item is finished and its claim is stale, so the next cycle reads
// the same nodes and settles it then, and a daemon that swallowed the refusal
// would leave the item blocked for every other orchestrator of the project with
// nothing said.
func (c *cycle) releaseClaims(ctx context.Context) error {
	if c.tick.Claims == nil {
		return nil
	}
	settled, err := settledClaimPaths(c.states())
	if err != nil {
		return err
	}
	return c.tick.Claims.Refresh(ctx, settled)
}

// fold writes what the finished runs mean into the items, and says so once the
// write has landed. It is also where a run the workspace's credentials service
// refused is noticed: the marker is the run's own record's, and the seam that
// tells a refusal the workspace answered from an ordinary outage — a rule over
// the marker and the halt beside it — is ee/'s (WorkspaceRefused, O19). A
// refusal this build's seam claims is remembered on the cycle, which asks a
// person at once rather than spending the attempts the streak would take.
//
// The question that refusal raises goes into this same patch (O19). The outcome
// that spends the attempt and the pause that stops the next item are one write,
// so a write that does not land leaves the intent still unreported and the next
// cycle reads the same report, folds the same refusal and asks then. A second
// write is the pause's whole risk: an ETag a person's or a session's write
// moved, a database that refused it, or the daemon dying between the two would
// leave the outcome landed and the refusal never folded again — openRunIDs
// skips an intent that has reported — so the queue would carry on into the
// workspace's refusal with no question and no line.
//
// What it asks about is the reports the reader answered for the intents that
// have not reported, which is every report the fold looks at: an attempt that
// has already reported is history, and a refusal folded in an earlier cycle is
// one this cycle cannot see again, so a question already asked is never asked
// twice by a fold.
func (c *cycle) fold(ctx context.Context, reports []RunReport) error {
	ops, notes, err := foldOps(c.state, reports)
	if err != nil {
		return err
	}
	folded := len(ops)
	// What the runs reported about the graph goes in the patch that ends their
	// attempts, and in the room that patch has left: the state is what the
	// daemon's own recovery and the caps read, and a run's entries are read
	// once — the attempt reports here, and no later tick asks about it again
	// (runview.go's openRunIDs) — so an entry the patch had no room for is one
	// nobody folds.
	graph := foldDiscovered(c.state, reports, c.tick.Footprints, MaxOps-len(ops), c.now)
	ops = append(ops, graph.ops...)
	for _, r := range reports {
		if r.Finished && WorkspaceRefused(r) {
			c.refused, c.refusedRun = r.WorkspaceRefusal, r.RunID
			break
		}
	}
	base := c.state
	var asked *DecisionOp
	if c.refused != "" && c.tick.Breaker != nil {
		// Against the state before this patch: the fold's ops write items and
		// intents, and whether the node already carries the breaker's question
		// is a read of the decisions it leaves alone.
		if op, ok := c.tick.Breaker.WorkspaceDecision(base, c.now, c.refused); ok {
			// The questions whose window has passed go out with the one this
			// patch raises: they are inert and nothing else retires them, so
			// the document would carry one per window until it refused every
			// write (dropLapsedWorkspaceQuestions).
			base = dropLapsedWorkspaceQuestions(base, c.now)
			ops = append(ops, op)
			asked = op.Decision
		}
	}
	if err := c.writeFold(ctx, base, folded, ops, notes, asked); err != nil {
		return err
	}
	// The lines the graph's refusals left go after the write that carries the
	// rest: they are evidence of decisions that stand, and none of them is what
	// the cycle did.
	return c.auditFold(ctx, graph.lines)
}

// foldNode writes what one sibling node's finished runs mean into its items and
// commits them. It is the fold without the two things that belong to the
// daemon's own node: the question a workspace's refusal raises and the line the
// audit log keeps of it. The state node's fold is the one that reads a refusal
// — out of every node's reports, because one call answers for every node's
// intents — and it is the state node whose question a person answers.
//
// A sibling over its size cap is skipped, and this is that node's own affair as
// much as the plan it is not given is: Apply validates the state a patch is made
// from as well as the one it makes, so the fold of an over-cap node is a patch
// the database refuses — and that refusal is this step's error, which ends the
// whole cycle before the state node's merge and its launches (X4: a node over
// its cap stops that node alone). The runs the skipped node holds are left
// unfolded and unreported, which is the same place the node is in for the plan:
// nothing this release writes can land on a document ValidateState refuses both
// ways, so there is no fold to make and no launch to hold back.
func (c *cycle) foldNode(ctx context.Context, reports []RunReport) error {
	if OverCap(c.state) {
		return nil
	}
	ops, notes, err := foldOps(c.state, reports)
	if err != nil {
		return err
	}
	folded := len(ops)
	// A node beside the state node folds the graph its own runs reported the
	// same way, into its own items and no other node's: a dependency never
	// crosses nodes (F44), and the fold of one node's report reads that node.
	graph := foldDiscovered(c.state, reports, c.tick.Footprints, MaxOps-len(ops), c.now)
	ops = append(ops, graph.ops...)
	if err := c.writeFold(ctx, c.state, folded, ops, notes, nil); err != nil {
		return err
	}
	return c.auditFold(ctx, graph.lines)
}

// writeFold commits one fold: the patch its ops make and the notifications that
// go with it. folded is how many of the ops are folded runs — the question a
// refusal raises rides in the same patch and is not one — and it is added to
// the report rather than set, because a cycle that works several nodes folds
// several times. A fold that asks for no op writes nothing at all.
func (c *cycle) writeFold(ctx context.Context, base State, folded int, ops []Op, notes []Notification, asked *DecisionOp) error {
	if len(ops) == 0 {
		return nil
	}
	next, err := Apply(base, Patch{BaseRev: base.Rev, Ops: ops}, Actor{Class: Daemon, Now: c.now})
	if err != nil {
		return err
	}
	if err := c.commit(ctx, next); err != nil {
		return err
	}
	c.rep.Folded += folded
	for _, n := range notes {
		c.say(ctx, n)
	}
	if asked != nil {
		c.say(ctx, Notification{Kind: NotifyDecisionNeeded, Subject: asked.ID, Text: asked.Question})
	}
	return nil
}

// carry runs one node's plan: each action in the order the plan decided them,
// each one through the starter, whose commit is this cycle's write to that node.
// The caps a node plans under are counted over every node of the project and
// the tally is the cycle's own, so the nodes of one project spend one allowance
// between them (D2743) — the state node is planned first, and the plan and
// backlog nodes get what it left. The counts and the tally meet exactly once:
// what the nodes held when the cycle read them (counts) and what this cycle has
// decided since (planned), never the same launch in both. The pause is the
// daemon's rather than the node's (paused), so a node whose own state carries
// no question still plans nothing while one stands anywhere in the project.
// The first action that cannot be carried out ends the cycle — what it left is
// what the starter committed before it, and the next tick re-derives the rest.
//
// An action whose item another orchestrator holds the claim on is not one of
// those: it is skipped, because the item is that orchestrator's work and not
// this daemon's (ErrItemClaimed, D3209), and nothing about it is this cycle's to
// carry or to fail over. The skip is counted on the report all the same
// (Skipped), because a cycle that skipped every action it planned is a cycle
// waiting on another orchestrator and not one that did nothing (outcome).
//
// What a skip must not do is spend the cycle's room (claimKey): the item was
// never started, so the slot the plan gave it goes back — the tally comes down
// — the item is remembered as another orchestrator's and the node is planned
// again for whatever the room it used leaves. That is what makes a project
// usable when the items that come first in a node's order are held elsewhere:
// finished plan-task claims stand for good and a rival's own copy of those tasks
// reads approved for good, so without this the daemon would spend its whole
// allowance on skips and never plan the work that is its own. Each pass either
// carries everything it planned or leaves at least one more item out, so it ends.
func (c *cycle) carry(ctx context.Context, view RunView) error {
	starter := Starter{Launcher: c.tick.Launcher, Commit: c.commit, Mint: c.tick.Mint, Claims: c.tick.Claims}
	if c.held == nil {
		c.held = map[string]bool{}
	}
	for {
		actions, planned := planNode(c.state, view, c.tick.Caps, c.now, c.counts, c.planned, c.paused, c.held)
		c.planned = planned
		if len(actions) == 0 {
			return nil
		}
		skipped := false
		for _, a := range actions {
			next, err := starter.Start(ctx, c.state, a, c.now)
			if errors.Is(err, ErrItemClaimed) {
				c.rep.Skipped++
				// The slot the plan gave the action goes back to the cycle: the
				// item was never started, so it must not hold room a launch
				// would use. Only a launch and a retry are counted by the plan —
				// a re-issue spends nothing a cap is there to bound (planNode) —
				// so only those have a slot to hand back.
				if a.Kind == ActionLaunch || a.Kind == ActionRetry {
					c.planned--
				}
				if w, ok := c.state.Item(a.ItemID); ok {
					c.held[claimKey(c.state.AgentID, w)] = true
				}
				skipped = true
				continue
			}
			if err != nil {
				return err
			}
			c.state = next
			c.rep.Carried++
		}
		if !skipped {
			return nil
		}
	}
}

// outcome is how the cycle reads itself for the breaker: a cycle that folded,
// carried or merged something moved the work, a cycle with an attempt that has
// not reported is waiting on a run — never a failure, however many ticks of one
// run go by (D1228) — and anything else did nothing at all.
//
// An item waiting for its pull request to merge is that same reading (G3): the
// attempt has reported, so an unreported intent is not what says the daemon is
// waiting, and a cycle spent waiting is not a cycle that did nothing — three of
// them would raise the pause that refuses the very launch the merge is for
// (D2705).
//
// A rung 3 hook that retried a wait is that same reading again (Waited): the
// checks it read had not concluded, or the run it read had not ended, and the
// cycle's news is something still going rather than nothing. The wait is not
// left to the breaker to end — the item's own question is what a wait that never
// ends owes a person, and the breaker names no item (boundRetry,
// mergeRetryWindow).
//
// An action the cycle skipped because another orchestrator holds the item
// (Skipped) is that same reading once more: the work is going, on the daemon
// that holds it, and this one has nothing to do about it and nothing to ask
// anyone. Two orchestrators of one project is the deployment the claim is for
// (D3206, D3207), and the loser of a claim skips what the winner holds — read as
// a cycle that did nothing, three of those stop a daemon whose own unclaimed
// items are still there for it (D1228).
//
// A judgement counts as work when it landed — Decided is the case the agent
// answered and the patch it landed is committed — and a cycle whose only news
// is a call is read by what the call came to: a call that failed or answered
// nothing is what the breaker counts (G10), and a cycle that also folded a run
// is progress whatever the call beside it came to.
func (c *cycle) outcome() CycleOutcome {
	switch {
	case c.rep.Folded > 0 || c.rep.Carried > 0 || c.rep.Triaged > 0 || c.rep.Escalated > 0 || c.rep.Decided > 0 || c.rep.Merged > 0:
		return CycleProgress
	case c.rep.DecideOutcome != "":
		return c.rep.DecideOutcome
	case c.anyNode(func(s State) bool { return hasOpenIntent(s) || hasReviewWait(s) }) || c.rep.Waited > 0 || c.rep.Skipped > 0:
		return CycleInFlight
	}
	return CycleEmpty
}

// commit writes a state this cycle derived — to the node the cycle is working
// on, which is the one the state was read from — and takes the ETag that write
// left.
//
// One conditional write per state, under the ETag the node was last read at —
// the store's own rule (O3): a body derived from the document read at that ETag,
// written once, and never pushed again under a fresher one. A cycle writes
// several times (the fold, then the intent and the outcome of each action), and
// the ETag each write needs is the one the previous write left; the store's
// Save answers nothing, so the only way to know it is to read the node back,
// and that read is also the check that the node is still the one this cycle
// wrote.
func (c *cycle) commit(ctx context.Context, next State) error {
	etag, err := saveNode(ctx, c.nodes[c.at].store, next, c.etag)
	if err != nil {
		// Whatever the reason, this cycle no longer holds the document its
		// next write would be derived from.
		c.fresh = false
		return err
	}
	c.state, c.etag = next, etag
	return nil
}

// saveNode is commit's write for a store the cycle may not be working on: the
// standing backlog's node, which the issue sync reaches through its own seam
// whether or not the tick carries it among the nodes the cycle works (D2735).
//
// The rule is the same one write under the ETag the document was read at, and
// the read-back is the ETag the next write has to carry. The caller keeps what
// the read-back says about freshness for itself, because a node the cycle is not
// working on has no freshness of its own to lose.
func saveNode(ctx context.Context, store Store, next State, etag ETag) (ETag, error) {
	if err := store.Save(ctx, next, etag); err != nil {
		return "", err
	}
	back, left, err := store.Load(ctx)
	if err != nil {
		return "", err
	}
	if back.Rev != next.Rev {
		return "", fmt.Errorf("%w: the node reads back at revision %d after the write of revision %d",
			ErrStateMoved, back.Rev, next.Rev)
	}
	return left, nil
}

// reread takes the node as it now stands after a write this cycle did not make.
//
// The rung 3 hook lands its moves through its own seam (BeyondSeams.Node), with
// the store's own conditional write, and the ETag that leaves is not the one
// this cycle holds: the state it derived and the ETag it read are both older
// than the document the hook left, so the next write it makes in the same Step
// — another item's question, the triage, the decision — would be refused under
// a revision that is already gone. Reading the node back is what gives the
// cycle the document its next write derives from, and it is the same read the
// seam could not make for it: the hook is handed the store and not the cycle.
//
// A read that fails leaves the cycle with nothing it may write rather than with
// a state it can no longer place (fresh), which is the direction the breaker's
// own question and the triage already decline in.
func (c *cycle) reread(ctx context.Context) error {
	back, etag, err := c.tick.Store.Load(ctx)
	if err != nil {
		c.fresh = false
		return err
	}
	c.state, c.etag = back, etag
	return nil
}

// ask commits the question a tripped breaker raises, and says so. The breaker
// asks at most one question per trip (Decision and WorkspaceDecision each
// refuse one the node already carries), so a daemon that restarted into an
// unanswered question does not leave a second one behind — and a cycle whose
// node is not its own to write asks nothing at all.
//
// The question goes on the state node whatever node the cycle was working on
// when it ended: the streak is the daemon's and the state node is where its
// question has always landed, beside the workspace refusals its own fold read.
// A cycle that read no node at all — the load failed on the first one, which is
// the one thing that can leave the cycle with none — asks nothing: the state
// node is not among the nodes it holds, and a question is a write.
func (c *cycle) ask(ctx context.Context) error {
	if c.tick.Breaker == nil || len(c.nodes) == 0 {
		return nil
	}
	c.bind(0)
	if !c.fresh {
		return nil
	}
	op, ok := c.question()
	if !ok {
		return nil
	}
	next, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: []Op{op}}, Actor{Class: Daemon, Now: c.now})
	if err != nil {
		return err
	}
	if err := c.commit(ctx, next); err != nil {
		return err
	}
	c.say(ctx, Notification{Kind: NotifyDecisionNeeded, Subject: op.Decision.ID, Text: op.Decision.Question})
	return nil
}

// question is what this cycle asks a person: the question a workspace's refusal
// raises when one landed in this cycle's fold, and the streak's own otherwise.
// The two never meet — a cycle that folded a refusal did work, which clears the
// streak (Observe) — and the node carries at most one of them either way.
func (c *cycle) question() (Op, bool) {
	if c.refused != "" {
		return c.tick.Breaker.WorkspaceDecision(c.state, c.now, c.refused)
	}
	return c.tick.Breaker.Decision(c.state, c.now)
}

// auditRefusal writes the line a run a workspace refused leaves: which run it
// was, which of the workspace's refusals the record named, and what the daemon
// did about it. The id is derived from the run and the transition, so a cycle
// that sees the same refusal again writes the same key rather than a second
// line.
//
// The line is written whether or not the question was raised — a node that
// already carried the breaker's question is a node whose refusal is still worth
// reading about — and it is not what raises it: the question is the fold's
// patch, and the log is evidence of it.
//
// A log this package cannot write fails the cycle with its error, which the
// tick returns and the report calls failed. What it does not do is lift the
// pause: the pause is the node's question and the trip has already landed, so a
// line the daemon could not append costs a reader evidence and a failed cycle,
// and not the stop.
func (c *cycle) auditRefusal(ctx context.Context) error {
	if c.tick.Audit == nil {
		return nil
	}
	_, err := c.tick.Audit.Append(ctx, Entry{
		Subject:    c.refusedRun,
		Transition: refusalTransition,
		Text: fmt.Sprintf("the workspace refused this run (%s); the orchestrator stopped starting work and asked a person",
			safetext.OneLine(c.refused)),
	})
	return err
}

// say hands one notification to the sink, once the write that made it true has
// landed. A sink that will not take it is recorded on the report and the cycle
// goes on: the notification is a channel, and the node is the record.
func (c *cycle) say(ctx context.Context, n Notification) {
	if c.tick.Notifier == nil {
		return
	}
	if err := c.tick.Notifier.Notify(ctx, n); err != nil {
		c.rep.NotifyErr = errors.Join(c.rep.NotifyErr, err)
	}
}

// TriageSeams is what one cycle acts on the long-run rule through (O14b): the
// read that gathers the rule's input, the two things an outcome can ask for
// that are not a state write, and the log every outcome leaves a line in. All
// three or none — a Tick built with none triages nothing, which is where a
// daemon with no host, no run storage or no audit log stands.
type TriageSeams struct {
	Read TriageReader
	Act  TriageActor
	Log  TriageLog
}

// Set reports whether every seam is there.
func (s TriageSeams) Set() bool { return s.Read != nil && s.Act != nil && s.Log != nil }

// TriageTarget is one run this cycle is due to judge: the item waiting on it,
// the run id its attempt recorded, how long that attempt has been going and
// the threshold it passed.
type TriageTarget struct {
	Item      WorkItem
	RunID     string
	Age       time.Duration
	Threshold time.Duration
}

// TriageReader gathers the rule's input for one run: the record, the pull
// request, the CI state and the findings file, each from where it lives. A
// reading that could not be made is reported inside the input — the rule's own
// unreadable inputs, which it fails closed on — and an error is a read that
// did not happen, which ends the cycle instead of judging a run on nothing.
//
// The age and the threshold are not the reader's: the cycle overwrites them
// from the target, so a run is due by the daemon's clock and the ladder's
// figure and by nothing else.
type TriageReader interface {
	TriageInput(ctx context.Context, target TriageTarget) (TriageInput, error)
}

// TriageActor carries out the two things an outcome can ask for beside a state
// write: landing the pull request the rule judged, and stopping the run it
// judged. A merge is at the head the rule read — a host that refuses it is a
// refusal the cycle reports to a person — and a cancel is the run's own cancel
// path, so the runner it stops still finalizes what it has.
type TriageActor interface {
	Merge(ctx context.Context, target TriageTarget, pr gitprov.PRInfo) error
	Cancel(ctx context.Context, target TriageTarget) error
}

// TriageLog is O3's audit log as one cycle writes it. Triaged answers whether
// this run's line is already there, which is what makes a repeated cycle
// append nothing and act on nothing; Append writes one line under the id the
// entry derives from its subject and transition.
type TriageLog interface {
	Triaged(ctx context.Context, run string) (bool, error)
	Append(ctx context.Context, entry Entry) error
}

// TriageAfter is the age at which the long-run rule is due: the ladder's own
// ceiling for the mode, `escalation.r3_after_hours` — the moment its rung 3,
// this rule's MergeAsIs, applies — and never a key of its own, because the
// local key this slice was once planned around was never built and is retired
// with the ladder's own figure (D2032). Zero when the ladder carries no row
// for the mode, which the rule reads as not due.
//
// A work item carries a rigor *tier* and the ladder is keyed by rigor *mode*,
// and nothing maps one to the other yet (the mode is gathered per run, which
// no release does): every item is therefore judged under the default mode's
// row. The critical-path factor is not applied here either — it scales the
// review rounds a mode gets, not the age at which a long run is judged.
func TriageAfter(mode rigor.Rigor) time.Duration {
	row, ok := escalation.DefaultThresholds().Rows[mode]
	if !ok || row.CeilingHours <= 0 {
		return 0
	}
	return time.Duration(row.CeilingHours * float64(time.Hour))
}

// triage is one cycle's turn at the rule: every item with a run recorded whose
// attempt has passed the threshold, the rule read about each of them, and what
// it answered carried out — unless the log already carries the run's line, in
// which case the cycle leaves it alone, which is the whole of the idempotence.
//
// A cycle that no longer holds the node does not triage at all: the outcomes
// act on the world (a merge, a cancel) and file work, and one read from a node
// this cycle cannot write is not a state to act from.
func (c *cycle) triage(ctx context.Context) error {
	if !c.tick.Triage.Set() || !c.fresh {
		return nil
	}
	for _, target := range triageDue(c.state, c.now, TriageAfter(rigor.Default)) {
		done, err := c.tick.Triage.Log.Triaged(ctx, target.RunID)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		in, err := c.tick.Triage.Read.TriageInput(ctx, target)
		if err != nil {
			return err
		}
		in.Elapsed, in.Threshold = target.Age, target.Threshold
		out := Triage(in)
		if !out.Due() {
			// A run the rule read no outcome for is left undocumented: it is
			// judged again next cycle, which is what makes a threshold that
			// comes due later one the daemon still reaches.
			continue
		}
		if err := c.triaged(ctx, target, in, out); err != nil {
			return err
		}
		c.rep.Triaged++
	}
	return nil
}

// triageDue is the runs a cycle's triage is about: every item with a run
// recorded whose attempt for that run has been going at least the threshold.
// The attempt is found by the run id the item names rather than by whether it
// has reported: the rule has an outcome for a run that has ended (the merge
// gate reads a terminal record) and one for a run still going, so both are
// judged here.
func triageDue(s State, now time.Time, threshold time.Duration) []TriageTarget {
	if threshold <= 0 {
		return nil
	}
	var out []TriageTarget
	for _, w := range s.WorkItems {
		if w.RunID == "" {
			continue
		}
		it, ok := attemptOf(s, w.RunID)
		if !ok {
			continue
		}
		age := now.Sub(it.At)
		if age < threshold {
			continue
		}
		out = append(out, TriageTarget{Item: w, RunID: w.RunID, Age: age, Threshold: threshold})
	}
	return out
}

// attemptOf is the intent recorded for one run id, reported or not.
func attemptOf(s State, runID string) (Intent, bool) {
	for _, it := range s.Intents {
		if it.RunID == runID {
			return it, true
		}
	}
	return Intent{}, false
}

// triaged carries one outcome out and writes the line that fences it.
//
// The order is the rule's: the act first — the merge the rule judged with
// every input read, the cancel that stops the run — then the state the outcome
// demands, then the line. A line is therefore only ever written for something
// that happened, and a cycle that died in the act leaves the run unfenced for
// the next one, which is the direction a lost write has to fail in.
func (c *cycle) triaged(ctx context.Context, target TriageTarget, in TriageInput, out TriageOutcome) error {
	text := triageLine(out)
	var (
		ops  []Op
		told []Notification
	)
	switch out.Kind {
	case MergeAsIs:
		// The one outcome that asks for a merge without a person, and the
		// only merge this package ever asks for: the head is the one the
		// rule judged, and the actor's own implementation is what refuses a
		// head that moved or a diff nothing corroborates.
		//
		// An item the daemon adopted at start-up is the one this rule may not
		// hand the host at all (D2702): adoption writes it for a run in
		// flight with no approval of a person's, and the ladder's rung 3 —
		// the other place in this package that lands work — refuses an
		// adopted item for exactly that reason (adoptedMergeRefused). The
		// rule cannot see this: it reads the run's inputs and never the item,
		// so the refusal is here, in the shape an actor's own refusal is
		// recorded in, and the person the approval belongs to is asked.
		if adoptedMergeRefused(target.Item, escalation.R3) {
			text = triageLine(out) + " " + string(ReasonMergeRefused) + " (" + codeUnapproved + ")"
			if op, question, ok := c.askPerson(target, mergeRefusedQuestion(target, codeUnapproved)); ok {
				ops, told = append(ops, op), append(told, question)
			}
			break
		}
		err := c.tick.Triage.Act.Merge(ctx, target, *in.PR)
		code := mergeRefusalCode(err)
		switch {
		case err == nil:
			text = triageLine(out) + " (merged)"
			if op, ok := c.followUp(target, in); ok {
				ops = append(ops, op)
			}
		case code != "":
			// A refusal is not an outage: the host said no to this merge —
			// the head moved, its merge cannot be bound to one, it holds no
			// merge credential — and what the rule judged is not what lands.
			// That is a person's to read, and the line below fences the run
			// for it. The line carries both: the outcome and its evidence,
			// then the refusal and its closed code, so the log is never read
			// as a merge that landed.
			text = triageLine(out) + " " + string(ReasonMergeRefused) + " (" + code + ")"
			if op, question, ok := c.askPerson(target, mergeRefusedQuestion(target, code)); ok {
				ops, told = append(ops, op), append(told, question)
			}
		default:
			return err
		}
	case Escalate:
		if err := c.tick.Triage.Act.Cancel(ctx, target); err != nil {
			return err
		}
		if op, question, ok := c.askPerson(target, escalateQuestion(target, out)); ok {
			ops, told = append(ops, op), append(told, question)
		}
	case Restart:
		// A run that is going nowhere is stopped here, and the smaller scopes
		// the rule named are filed for a person to approve: the daemon files
		// them unapproved, and only a person's own approval starts them
		// (D1226).
		if err := c.tick.Triage.Act.Cancel(ctx, target); err != nil {
			return err
		}
		ops = append(ops, c.smallerScopes(target, out)...)
	case RecommendOnly:
		// The outcome that raises nothing, files nothing, merges nothing and
		// cancels nothing: what it does is the line below, which names the
		// input nobody could read.
	}
	if len(ops) > 0 {
		next, err := Apply(c.state, Patch{BaseRev: c.state.Rev, Ops: ops}, Actor{Class: Daemon, Now: c.now})
		if err != nil {
			return err
		}
		if err := c.commit(ctx, next); err != nil {
			return err
		}
	}
	if err := c.tick.Triage.Log.Append(ctx, Entry{Subject: target.RunID, Transition: triageTransition, Text: text}); err != nil {
		return err
	}
	for _, n := range told {
		c.say(ctx, n)
	}
	return nil
}

// ErrTriageUncorroborated is how a triage actor refuses a merge the rule
// judged: the paths the merge gate classified are the run's own account of its
// diff, and nothing the actor can read corroborates them at the judged head.
//
// The job writes that record from inside the container it shares with the
// agent (design §6.1), so a run can describe its diff as one of audit-safe
// paths while carrying a sensitive one, and a merge taken on it would land
// work nobody was asked about. The read that lifts this refusal is the host's
// changed files at the head under judgement; until a provider answers one, no
// merge lands unattended, and the outcome becomes a person's question.
var ErrTriageUncorroborated = errors.New("the changed paths are not corroborated at the judged head")

// codeUncorroborated is that refusal's closed code in the log and the question.
const codeUncorroborated = "uncorroborated"

// codeUnapproved is the closed code of the other merge this file refuses: the
// item the rule judged was adopted from a run the daemon did not start, and no
// person has approved it (D2702). It is not an error an actor answers with — the
// item is not the actor's to see — so it is written where the refusal is made
// rather than mapped from one, and it is a closed code of the same vocabulary
// for the same reason: the line and the question carry no untrusted text.
const codeUnapproved = "unapproved"

// mergeRefusalCode is the closed code of a merge the actor refused, or ""
// when the error is not a refusal at all. A refusal is a person's: the head
// moved under the call, the provider's merge cannot be bound to an expected
// head at all, it holds no merge credential, the pull request is no longer
// open, or the diff under judgement is nobody's but the run's own word.
// Everything else is an outage — a host that did not answer — which the next
// cycle retries.
//
// The codes are this file's own vocabulary and never the provider's message:
// O3's line carries closed codes and no untrusted text, and two providers
// spell one refusal two ways.
func mergeRefusalCode(err error) string {
	for code, sentinel := range map[string]error{
		codeUncorroborated:      ErrTriageUncorroborated,
		"head_moved":            gitprov.ErrHeadMoved,
		"no_head_precondition":  gitprov.ErrMergeNoHeadPrecondition,
		"no_merge_mint":         gitprov.ErrNoMergeMint,
		"pull_request_not_open": gitprov.ErrPRNotOpen,
	} {
		if errors.Is(err, sentinel) {
			return code
		}
	}
	return ""
}

// askPerson is the question an outcome raises for a person: one decision per
// run, under the run's own id, and nothing when the node already carries it — a
// decision_add on an id that is there is refused, so a cycle that re-runs
// after a write that landed asks once.
func (c *cycle) askPerson(target TriageTarget, question string) (Op, Notification, bool) {
	id := target.RunID + "-" + triageTransition
	if _, ok := c.state.Decision(id); ok {
		return Op{}, Notification{}, false
	}
	op := Op{Op: OpDecisionAdd, Decision: &DecisionOp{ID: id, Question: question, WaitingOn: OnPerson}}
	return op, Notification{Kind: NotifyDecisionNeeded, Subject: id, Text: question}, true
}

// smallerScopes is the work a restart files: one item per reason the rule gave,
// each waiting for a person's approval — never approved here, because a daemon
// that approved its own work items would be the standing approval D1226
// refuses. An item whose name the node already carries is not filed again, so a
// cycle that re-runs after a committed write adds nothing.
func (c *cycle) smallerScopes(target TriageTarget, out TriageOutcome) []Op {
	var ops []Op
	for _, reason := range out.Reasons {
		id := target.RunID + "-" + string(reason)
		if _, ok := c.state.Item(id); ok {
			continue
		}
		ops = append(ops, newItemOp(id,
			"a smaller scope: "+target.Item.Title,
			"The triage restarted run "+target.RunID+" ("+string(reason)+"): the run is over or going nowhere, and what it was asked to do is not one attempt's work. Split it into a scope a person can approve; this item waits for that approval.",
			target.Item))
	}
	return ops
}

// followUp is the work a merge leaves behind: the findings the run wrote that
// did not block the landing are still open, and a person decides what to do
// about them. A merge with nothing open leaves nothing to file.
func (c *cycle) followUp(target TriageTarget, in TriageInput) (Op, bool) {
	if len(in.Findings.List) == 0 || in.Record == nil {
		return Op{}, false
	}
	id := target.RunID + "-follow-up"
	if _, ok := c.state.Item(id); ok {
		return Op{}, false
	}
	lines := make([]string, 0, len(in.Findings.List))
	for _, f := range in.Findings.List {
		lines = append(lines, string(f.Severity))
	}
	return newItemOp(id,
		"the findings run "+target.RunID+" left open",
		"Run "+target.RunID+" was merged with these findings still open: "+strings.Join(lines, ", ")+". Decide each one; this item waits for a person's approval.",
		target.Item), true
}

// newItemOp is one work item a cycle files: the repo, the recipe and the tier
// of the item the run was for, the phase work starts in, and no approval —
// only a person's own work_item_approve is one (D1226). The title and the
// brief are the store's to clip to their bounds, which is why this never
// builds one longer than they allow.
func newItemOp(id, title, brief string, of WorkItem) Op {
	phase := PhaseQueued
	repo, recipe, tier := of.Repo, of.Recipe, of.RigorTier
	return Op{Op: OpWorkItemUpsert, WorkItem: &WorkItemOp{
		ID: id, Title: &title, Repo: &repo, Recipe: &recipe, RigorTier: &tier, Brief: &brief, Phase: &phase,
	}}
}

// triageLine is what the audit log carries for one outcome: the outcome and
// the reasons it was taken for, in the rule's own words, so a person reading a
// long run's ending reads the evidence and not a story about it.
func triageLine(out TriageOutcome) string {
	return string(out.Kind) + ": " + triageCodes(out)
}

// triageCodes is an outcome's reasons as one line of closed codes.
func triageCodes(out TriageOutcome) string {
	codes := make([]string, 0, len(out.Reasons))
	for _, r := range out.Reasons {
		codes = append(codes, string(r))
	}
	return strings.Join(codes, " ")
}

// escalateQuestion is what a person is asked about a run the triage escalated:
// the codes the rule read it for, and the run they were read in.
func escalateQuestion(target TriageTarget, out TriageOutcome) string {
	return "The triage escalated run " + target.RunID + " (" + triageCodes(out) + "). The run was stopped; read the finding and decide what happens to it."
}

// mergeRefusedQuestion is what a person is asked about a merge that was
// refused: the refusal is theirs to act on — a moved head is a review, an
// unbound merge and a missing credential are configuration, an uncorroborated
// diff is the one the daemon will not land by itself, and an item nobody
// approved is the one no daemon may land at all — and the run keeps its own
// record either way.
func mergeRefusedQuestion(target TriageTarget, code string) string {
	if code == codeUncorroborated {
		return "The triage will not land the pull request of run " + target.RunID + " by itself: the changed paths it judged are the run's own account of its diff, and no host read of the judged head corroborates them (" + code + "). Read the diff and land it yourself, or close it."
	}
	if code == codeUnapproved {
		return "The triage will not land the pull request of run " + target.RunID + " by itself: the item stands for a run the daemon adopted at start-up and no person has approved it (" + code + "). Approve the item, or read the diff and land the pull request yourself."
	}
	return "The triage could not land the pull request of run " + target.RunID + " (" + code + "). Decide what happens to it."
}

// DecideSeams is what one cycle reaches the decision agent through (O13): the
// agent itself and the log the fence is read from. Both or neither — a Tick
// built with neither judges nothing, which is where a daemon that was not
// started with `--decide` stands.
//
// The agent is an interface here and an implementation in internal/cli, the way
// the triage's actor is: the package that makes a decision call (O12's
// internal/orchestrator/decide) imports this one, so the seam has to be named
// on this side of that import.
type DecideSeams struct {
	Agent Decider
	Log   DecideLog
}

// Set reports whether every seam is there.
func (s DecideSeams) Set() bool { return s.Agent != nil && s.Log != nil }

// WakeReason is why a cycle woke the decision agent: one of the three cases the
// spec's own table hands the LLM rather than code ("When to wake the LLM",
// spec.md). Every other row of that table is code and never reaches this file's
// list of cases — a heartbeat and a progress ping are the run view's own
// readings, a completed task is a fold, and a task that failed for the first
// time is the retry a person's approval already covers (O6) — which is the
// whole of what keeps a daemon from re-creating the sweep's cost at a smaller
// constant (D1221).
type WakeReason string

const (
	// WakeRetriesExhausted is the spec's "task failed, retries exhausted": an
	// item whose phase is failed, whose approval is spent, and whose newest
	// attempt under that approval ended failed. Nothing in this package moves
	// such an item — another attempt needs a person's approval (D1226), and
	// recovery has nothing to re-issue because the attempt reported.
	WakeRetriesExhausted WakeReason = "retries-exhausted"
	// WakeStuck is the spec's "task stuck past timeout": an attempt that has
	// not reported, whose run never started, older than the window its claim is
	// good for (ClaimTTL). Recovery re-issues it, which is the deterministic
	// half (D1231); the judgement is what is left for an attempt that is still
	// not going.
	//
	// A run that started and has not reported is the other row of that table —
	// a heartbeat, which is code's, however long it goes — and a run past the
	// ladder's own ceiling is the long-run triage's (O14b, code by D1244).
	WakeStuck WakeReason = "stuck"
	// WakeAmbiguousResult is the spec's "ambiguous review result": an item in a
	// phase an attempt is still expected from, whose newest attempt reported an
	// outcome that phase has no move for (state.go's CanMoveTo), so the fold
	// recorded the ending and left the phase where it stood (foldOps). The
	// run's result is on the node, and nothing reads it as a phase of the item.
	WakeAmbiguousResult WakeReason = "ambiguous-result"
)

// DecideCase is one judgement a cycle hands the decision agent: the item the
// spec's table names a wake for, why code will not decide it, and the attempt
// the case is about. Every case is about one attempt, so the id its fence
// derives is that attempt's own and a later attempt is a case of its own.
type DecideCase struct {
	ItemID  string
	Reason  WakeReason
	Attempt Intent
}

// Decider is the decision agent as one cycle reaches it (O12). It is handed the
// state the cycle read — never a state to write — and answers what to commit.
type Decider interface {
	// Decide makes one judgement about one case.
	//
	// The error is the call's own failure, and a caller reports it on the cycle
	// rather than raising it out of one: the fold, the carry and the triage that
	// came before this step stand, and what a judgement the agent could not make
	// costs is the cycle's outcome, which the breaker counts (G10, D1228).
	Decide(ctx context.Context, state State, c DecideCase) (DecideResult, error)
}

// DecideResult is what one wake came to.
type DecideResult struct {
	// Next is the state to commit — O12's record of the call and the patch it
	// judged — or the zero State for a call that was never made: the caps
	// refused it, the state carried no such item, or the call's own record had
	// no room in it. Nothing else here is read from a zero one.
	Next State
	// Outcome is how the cycle reads the wake, in the breaker's vocabulary,
	// and empty for a call that was never made. A refusal is not a cycle that
	// achieved nothing, and counting one would stop the loop over a bound that
	// is doing its job (O12's own reading).
	Outcome CycleOutcome
	// Answered reports whether the agent answered the case: a call was made and
	// an answer was read, whatever that answer asked for — an answer that asks
	// for no change is a judgement, "there is nothing to do about this item"
	// being the answer to the question.
	//
	// An answered case is fenced: the log's line is written and it is never
	// judged again. A case the agent did not answer is left for the next cycle,
	// which is what lets a call that keeps failing reach the caps and the
	// breaker instead of being asked once and forgotten.
	Answered bool
	// Text is the judgement for the log's line: the class and the model it ran
	// as with what the model reasoned.
	Text string
}

// Called reports whether the wake made a call at all: whether there is a state
// to commit — O12's record of it — and whether an answer could have been read.
// The runner answers the zero State for a call it never made, and no state this
// package validates carries an empty agent id (validate.go).
func (r DecideResult) Called() bool { return r.Next.AgentID != "" }

// DecideLog is O3's audit log as the decide step writes it: Judged answers
// whether a case's line is already there, which is the whole of the
// idempotence — a repeated cycle judges one attempt once, and the line it wrote
// is the evidence of what was judged — and Append writes one line under the id
// the case derives.
//
// The line is evidence and not a lock (audit.go). What it fences is the wake,
// which is money and a model call; what it does not fence is the answer, whose
// ops go through Apply under the DecisionAgent class and are refused there
// however an answer words them (G1, F20).
type DecideLog interface {
	Judged(ctx context.Context, id string) (bool, error)
	Append(ctx context.Context, entry Entry) error
}

// decideTransition is the transition of the line a case's fence is read by: the
// reason and the attempt's own number, so one attempt is judged once and a
// later attempt of the same item is a case of its own. It is spelled in the
// log's own alphabet — a transition is a lowercase name (audit.go) — and the
// reasons above are lowercase names joined with hyphens already.
func decideTransition(c DecideCase) string {
	return "decide-" + string(c.Reason) + "-" + strconv.Itoa(IntentAttempt(c.Attempt.ID))
}

// DecideAuditID is the derived id of a case's own line (EntryID): what the
// cycle reads before it wakes the agent, and the write that fences the case.
func DecideAuditID(c DecideCase) string { return EntryID(c.ItemID, decideTransition(c)) }

// judgementCases is the cycle's list of the judgements the spec's table hands
// the LLM, in the order that table states them and never in the order the node
// happens to carry its items: the cases are tried in the order below, so the
// one call a cycle may make goes to the case the table puts first.
//
// A case is one attempt of one item, and the three readers below are disjoint:
// a failed item with nothing left has no open attempt and no active phase, an
// attempt that never started has not reported, and an attempt that reported is
// not one the claim window is about.
func judgementCases(s State, view RunView, now time.Time) []DecideCase {
	var out []DecideCase
	for _, w := range s.WorkItems {
		if it, ok := retriesExhausted(s, w); ok {
			out = append(out, DecideCase{ItemID: w.ID, Reason: WakeRetriesExhausted, Attempt: it})
		}
	}
	for _, w := range s.WorkItems {
		if it, ok := stuckAttempt(s, view, w, now); ok {
			out = append(out, DecideCase{ItemID: w.ID, Reason: WakeStuck, Attempt: it})
		}
	}
	for _, w := range s.WorkItems {
		if it, ok := ambiguousResult(s, w); ok {
			out = append(out, DecideCase{ItemID: w.ID, Reason: WakeAmbiguousResult, Attempt: it})
		}
	}
	return out
}

// retriesExhausted is the spec's "task failed, retries exhausted" read at this
// release's state: the item's phase is failed, no approval stands over it, and
// the newest attempt the approval it spent ended failed.
//
// The spent marker is what makes the case the daemon's own: an item a person or
// a session moved to failed writes none, and the daemon asks no model about a
// judgement somebody already made about the item.
func retriesExhausted(s State, w WorkItem) (Intent, bool) {
	if w.Phase != PhaseFailed || w.Approved != nil || w.SpentApprovalAt == nil {
		return Intent{}, false
	}
	_, covered, ok := approvalAttempts(s, w.ID, *w.SpentApprovalAt)
	if !ok {
		return Intent{}, false
	}
	newest, ok := newestUnder(s, w.ID, *w.SpentApprovalAt, covered)
	if !ok || newest.Outcome != OutcomeFailed {
		return Intent{}, false
	}
	return newest, true
}

// stuckAttempt is the spec's "task stuck past timeout" read at this release's
// state: an attempt that has not reported, whose run never started, older than
// the window a claim is good for (ClaimTTL) — this release's timeout for a task
// that was on its way and has not arrived.
//
// The run id is read in the view the cycle already has and never asked for
// again: a run the view says nothing about is one that never started, which is
// the same answer recovery acts on.
func stuckAttempt(s State, view RunView, w WorkItem, now time.Time) (Intent, bool) {
	it, ok := openIntentOf(s, w.ID)
	if !ok || view.Run(it.RunID).Started {
		return Intent{}, false
	}
	if now.Sub(it.At) < ClaimTTL {
		return Intent{}, false
	}
	return it, true
}

// ambiguousResult is the spec's "ambiguous review result" read at this
// release's state: an item an attempt of which reported an outcome the item's
// own phase table has no move for, so the fold recorded the ending and left the
// phase where it stood — the run's result is on the node and nothing reads it
// as a phase.
//
// Only the item's newest attempt is read, whatever approval or recipe it ran
// under: it is the item's last word. And only while the item stands in a phase
// an attempt is still expected from (queued, running, review): the four phases
// outside those are a person's or a session's own word about the item — done,
// failed, blocked, cancelled — and the daemon asks no model to overrule one.
func ambiguousResult(s State, w WorkItem) (Intent, bool) {
	if !w.Phase.active() {
		return Intent{}, false
	}
	newest, ok := newestAttempt(s, w.ID)
	if !ok || newest.Outcome == "" {
		return Intent{}, false
	}
	// The pull request is not read here: the question is whether the phase
	// table has a move for this ending at all, and for a succeeded attempt the
	// two readings of it agree — from a phase an attempt is still expected from
	// (queued, running, review) both `review` and `done` are reachable or
	// neither is.
	phase, known := foldPhase(newest.Outcome, false)
	if !known || w.Phase.CanMoveTo(phase) {
		return Intent{}, false
	}
	return newest, true
}

// newestAttempt is the newest attempt recorded for one item, whatever approval
// or recipe it ran under, and false when the item has none. It is the reader
// for a question about the item itself; newestUnder is the narrower one a plan
// reads, because what an approval covers is not everything an item ran.
func newestAttempt(s State, itemID string) (Intent, bool) {
	newest, found := Intent{}, false
	for _, it := range s.Intents {
		if IntentItem(it.ID) != itemID {
			continue
		}
		if !found || IntentAttempt(it.ID) > IntentAttempt(newest.ID) {
			newest, found = it, true
		}
	}
	return newest, found
}

// decide is one cycle's turn at the decision agent (O13): the judgement cases
// the spec's table names, and at most one call a cycle.
//
// One call and not one per case, because every call is a fresh child on the
// person's own login: a node with six stuck items is six judgements, and the
// caps would let one tick spend the hour's whole allowance. The cases are tried
// in the table's own order (judgementCases), the first one the log does not
// already carry is the one this cycle spends on, and the rest are left for the
// cycles after it — each of which re-derives them from the node it reads
// (D1229).
//
// A paused daemon judges nothing: the breaker's question stands unanswered on
// the node and the pause is a person's (D1228). A daemon that may not spend does
// not judge either — the kill switch is the person's stop, and a model call is
// money — and neither does one whose node is no longer this cycle's to write,
// because the ops an answer asks for are committed against that node.
func (c *cycle) decide(ctx context.Context, view RunView) error {
	if !c.tick.Decide.Set() || !c.fresh || c.tick.Caps.KillSwitch || BreakerOpen(c.state, c.now) {
		return nil
	}
	for _, k := range judgementCases(c.state, view, c.now) {
		judged, err := c.tick.Decide.Log.Judged(ctx, DecideAuditID(k))
		if err != nil {
			return err
		}
		if judged {
			continue
		}
		return c.judge(ctx, k)
	}
	return nil
}

// judge hands one case to the agent, commits what it answered and writes the
// line that fences it.
//
// The order is the rule's, and it is the triage's own: the state first — O12's
// record of the call is on the node before the answer is read, which is what
// makes the caps count a call whose answer was refused — and the line last, so
// a cycle that died in the call leaves the case unfenced and the next one
// reaches it again. A line is therefore only ever written for a judgement that
// happened, and a lost write fails in the direction of judging again rather
// than of never judging at all.
func (c *cycle) judge(ctx context.Context, k DecideCase) error {
	res, err := c.tick.Decide.Agent.Decide(ctx, c.state, k)
	if err != nil {
		c.rep.DecideErr = errors.Join(c.rep.DecideErr, err)
	}
	if res.Outcome != "" {
		c.rep.DecideOutcome = res.Outcome
	}
	if res.Called() {
		// The call's own record is committed whatever became of the answer: the
		// call was made and the money is spent, and the caps count the state's
		// decisions rather than the model's replies (O12).
		if err := c.commit(ctx, res.Next); err != nil {
			return err
		}
	}
	if !res.Answered {
		return nil
	}
	if err := c.tick.Decide.Log.Append(ctx, Entry{
		Subject:    k.ItemID,
		Transition: decideTransition(k),
		Text:       res.Text,
	}); err != nil {
		return err
	}
	c.rep.Decided++
	return nil
}
