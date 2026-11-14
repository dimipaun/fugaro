package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// ClaimTTL is how long an attempt whose run has reported nothing is left alone
// before recovery acts on it: the same ten minutes internal/runstore gives a
// launch claim, and the window O8's launcher takes a stale claim over after.
// The package carries its own number because it imports no launch machinery —
// a plan is as pure as the state it is made of.
const ClaimTTL = 10 * time.Minute

// Spec is what a launch starts from: the declarative half of a work item,
// which is the half a person approves.
type Spec struct {
	ItemID    string
	Title     string
	Repo      string
	Recipe    string
	RigorTier string
	Brief     string
}

// SpecOf is the launch spec of a work item. It reads the item's own fields and
// nothing beside them, and SpecDigest reads the item, so what a launcher is
// asked to start is the spec that was approved or nothing at all.
func SpecOf(w WorkItem) Spec {
	return Spec{
		ItemID:    w.ID,
		Title:     w.Title,
		Repo:      w.Repo,
		Recipe:    w.Recipe,
		RigorTier: w.RigorTier,
		Brief:     w.Brief,
	}
}

// Launcher starts one run for one attempt. It is handed the run id the state
// has already recorded and returns the run id it started; a launcher that
// comes back with another one is refused, because a second run id is a run the
// state knows nothing about (O8 implements this over `fugaro run`; the state
// package imports no command).
type Launcher interface {
	Launch(ctx context.Context, spec Spec, runID string) (string, error)
}

// Run is what a tick's read-only view of the runs knows about one run id.
type Run struct {
	RunID   string
	Started bool
}

// RunView is what a plan reads that the node does not itself hold: the runs
// behind the intents, the follow-up each item was filed from, and what the
// item's own runs have cost. A run that is not in it is one that never started,
// which is the answer recovery acts on; an item it says nothing about is not a
// follow-up, and one it reads no spend for has spent nothing.
//
// It is the daemon that fills it — the run records are its to read (O8) — and
// nothing a run writes is read through it as a decision about an item: a
// follow-up's importance sorts the work and approves none of it (G6, F21), and
// a cost is a figure a person set a ceiling over (G9).
type RunView struct {
	Runs []Run
	// FollowUps is what the daemon read about the follow-up each item settles,
	// by item id (E4, D2040). An item that is not in it is not a follow-up at
	// all — a new work item filed from nothing — and is not one the launcher
	// sorts by findings it never had.
	FollowUps map[string]FollowUp
	// Spend is what each item's own runs have cost and what they may cost, by
	// item id. The ceiling is escalation.cost_ceiling (E3), one figure for the
	// whole installation, and it travels per item because the spend does.
	Spend map[string]LadderSpend
}

// FollowUp is what a plan reads about the follow-up an item was filed from
// (task.Spec.FollowUpOf, E4): how much the findings it settles matter (D2040).
// It is a sorting key and nothing else — an important follow-up is ordered
// ahead of new work and ahead of a minor one, and it approves nothing (G6,
// F21): what a launch starts from is a person's approval over the spec they
// read, and an item nobody approved is one the plan does not hold however
// important it is.
type FollowUp struct {
	// Importance is what the merge judge wrote (D2040), or "" for a follow-up
	// nobody classified. An empty one is not minor: it is no answer, and a
	// reader that has to sort one asks its own rule rather than reading a mark
	// nobody wrote (task.Importance).
	Importance task.Importance
	// Scores are the findings this follow-up settles, as the run that filed it
	// scored them: the rule below reads them where no judge classified the
	// follow-up. They are scores and not finding keys — a key is text, and the
	// rule reads a class and an impact (D2040, G8).
	Scores []rigor.Score
}

// Important reports whether this follow-up is one the launcher orders ahead of
// new work: the important mark, or — for a follow-up nobody classified —
// D2040's deterministic rule over the findings it settles.
func (f FollowUp) Important() bool {
	switch f.Importance {
	case task.ImportanceImportant:
		return true
	case task.ImportanceMinor:
		return false
	}
	if len(f.Scores) == 0 {
		// Nothing to read and no mark: the rule cannot place it, and the strict
		// answer is the one that puts it in front of a person rather than behind
		// new work. It launches nothing either way (G6).
		return true
	}
	return slices.ContainsFunc(f.Scores, importantFinding)
}

// importantFinding is D2040's deterministic rule read at one finding: a
// security or data-loss class, or the top of the impact scale, makes the
// follow-up important. It is the spec's catastrophic finding — rigor's table
// reads the same three clauses as its severity override — stated here because
// the table answers about a rigor mode (rigor.Blocks) and the launcher has
// none: what it reads is what a finding costs if it is never fixed.
//
// A finding the reviewer did not score is important as well. The rule cannot
// place it, and D4's fail-closed reading is the one this repository takes of a
// value it could not read.
func importantFinding(s rigor.Score) bool {
	if s.Class == rigor.Security || s.Class == rigor.DataLoss || s.Impact == 5 {
		return true
	}
	return !s.Scored()
}

// LadderSpend is what one item's ladder runs have cost and what they may cost:
// the sum of the item's own run records, the chain's figure where a chain
// exists (D2038, G9), and escalation.cost_ceiling (E3). Zero for the ceiling is
// no ceiling — a person who set none gets no cost bound — and the ladder's
// counts are what bound it then (D2042).
type LadderSpend struct {
	USD     float64
	Ceiling float64
}

// AtCeiling reports whether the item has spent its ceiling. It is D2042's
// optional extra read at the launch and not only at the decision: a run that
// lands the item at the ceiling stops the next one even where a tick decided
// otherwise on an older read of the records.
func (s LadderSpend) AtCeiling() bool { return s.Ceiling > 0 && s.USD >= s.Ceiling }

// Run returns what the view knows about a run id.
func (v RunView) Run(id string) Run {
	for _, r := range v.Runs {
		if r.RunID == id {
			return r
		}
	}
	return Run{RunID: id}
}

// ActionKind is what a plan decided to do about one item.
type ActionKind string

const (
	// ActionLaunch starts the first attempt under a person's approval.
	ActionLaunch ActionKind = "launch"
	// ActionRetry starts the one further attempt an approval whose attempt
	// failed still covers.
	ActionRetry ActionKind = "retry"
	// ActionLadder starts a run the ladder asked for rather than one a person's
	// approval was waiting on: rung 1's resume of the item's own recipe, which
	// the approval covers whether it still stands or a retry has spent it
	// (D2042, G15). LadderLaunch is what plans one, and the executor that walks
	// the ladder asks for it (E10a): the launcher's own plan holds nothing for
	// such an item, because a person's approval has no attempt left to give it.
	//
	// Nothing starts one in this release. An attempt is declared under a person's
	// standing approval and nothing else (patch.go's commitIntent), which is
	// exactly what a rung 1 resume does not have — a retry spent it, and the item
	// carries the marker in its place — so Starter refuses this kind rather than
	// hand a caller a launch no write can record (LadderLaunch,
	// TestLadderResumeIsRefusedByTheWriteSide).
	ActionLadder ActionKind = "ladder"
	// ActionReissue starts an attempt whose run id is already recorded and
	// whose run never started. It is the same launch, not a second one.
	ActionReissue ActionKind = "reissue"
	// ActionAbandon closes an attempt nothing may start.
	ActionAbandon ActionKind = "abandon"
)

// Action is one thing a plan decided: the item, the attempt and the intent it
// is for, and the spec and digest a launch would carry. RunID is set on a
// re-issue, where the run id is the one the state already recorded.
type Action struct {
	Kind       ActionKind
	ItemID     string
	IntentID   string
	Attempt    int
	RunID      string
	Spec       Spec
	Digest     Digest
	ApprovalAt time.Time
	Reason     string
}

// Plan is one cycle's decision: the launches and retries the state, the view
// of the runs and the caps allow, in the order planOrder gives the items — the
// important follow-ups first, then everything else as the state declares it
// (D2040). It reads and changes nothing — a plan is a proposal, and the tick
// that carries it out commits each intent before anything starts.
//
// It hesitates in the only safe direction. Nothing is planned with the kill
// switch on, nothing while the breaker's question stands unanswered on the node
// (a pause a restart may not lift), nothing while the node is over its size cap
// (the intents a count is read against would no longer be the whole record),
// nothing for an item with an attempt that has not reported, nothing for an
// item nobody approved,
// nothing whose spec has changed since the approval, and nothing while a
// dependency is unfinished. What is left is a first attempt under a person's
// approval, or the one retry that approval still covers. None of it is what the
// ladder launches on top (LadderLaunch): a rung's runs are the executor's to
// ask for, and its own bound is not this plan's caps.
func Plan(s State, view RunView, caps Caps, now time.Time) []Action {
	if caps.KillSwitch || OverCap(s) || BreakerOpen(s) {
		return nil
	}
	counts := CountsOf(s, now)
	var actions []Action
	planned := 0
	for _, w := range planOrder(s, view) {
		if it, ok := openIntentOf(s, w.ID); ok {
			// A run is in flight or never started, and what is left to decide
			// is that attempt's own recovery. It is not capped: a re-issue
			// starts the attempt that was already committed and counted, so it
			// spends nothing a cap is there to bound.
			if a, ok := recovery(s, view, w, it, now); ok {
				actions = append(actions, a)
			}
			continue
		}
		a, ok := nextAttempt(s, w)
		if !ok {
			continue
		}
		if !counts.Room(caps, planned) {
			// The caps bound the cycle's whole list, so an item that does not
			// fit is passed over rather than being planned past them — and the
			// items after it still get their own recovery.
			continue
		}
		actions = append(actions, a)
		planned++
	}
	return actions
}

// planOrder is the items a plan considers, in the order it considers them: the
// important follow-ups first, then everything else, each group in the order the
// state declares its items. Importance is the one thing that reorders work
// (D2040), and with the cycle's caps binding, the room a plan has goes to the
// work that matters most rather than to whichever item the state lists first.
// Nothing else about a reading of importance reaches a plan: it is a sorting
// key, and it approves nothing (G6).
func planOrder(s State, view RunView) []WorkItem {
	items := slices.Clone(s.WorkItems)
	slices.SortStableFunc(items, func(a, b WorkItem) int {
		return importanceRank(view, a) - importanceRank(view, b)
	})
	return items
}

// importanceRank is an item's place in that order: 0 for an important
// follow-up, 1 for everything else — new work, a minor follow-up, and an item
// the view says nothing about. A rank rather than a comparison, so the sort
// stays stable and two items of one rank keep the order the state declares
// them in, which is a person's own.
//
// An item the view does not carry is not a follow-up at all, and its reading is
// never asked: the zero FollowUp a missing entry would hand back is the value
// an unclassified follow-up carries, so reading it would make every new work
// item an important finding nobody filed (E4).
func importanceRank(view RunView, w WorkItem) int {
	f, ok := view.FollowUps[w.ID]
	if ok && f.Important() {
		return 0
	}
	return 1
}

// Queue is the work waiting for a person: the items no approval covers and
// whose phase an attempt starts from — the items nothing may start for want of
// somebody's go — in the order a launcher takes work, the important follow-ups
// first. It is what a message to a person, or a list they read, is built from,
// and it decides nothing: what comes back is an order to put in front of them.
//
// No item in it is one a plan of this package may start, and that is not the
// same as saying no daemon may start it (G6, F21 still holds, and nothing here
// is approved by being listed): an item whose approval is spent and which the
// ladder has walked to rung 1 is one the ladder's executor resumes under the
// approval that covered it (LadderLaunch). It is listed all the same, because
// what a person decides about the ladder's floor — a new approval over the item
// as it now stands, or a cancellation — is theirs to make, and a queue that
// dropped the items the ladder is carrying would be a list that hides work from
// the one reader who can call it off.
func Queue(s State, view RunView) []WorkItem {
	var out []WorkItem
	for _, w := range planOrder(s, view) {
		if w.Approved == nil && startable(w.Phase) {
			out = append(out, w)
		}
	}
	return out
}

// LadderRuns is how many launches the ladder has spent on one item, counted
// apart from ApprovalAttempts (G15, D2042). The two records the daemon keeps
// describe different runs, so the count is their sum: the ledger carries the
// runs of another recipe the executor launched — rung 0's rebases, the one rung
// 2, the judge, the review of a rebased head (Escalation.OuterAttempts, E7) —
// and the intents carry rung 1's resumes, which are runs of the item's own
// recipe and are the ladder's past the ones a person's approval covers.
//
// A run the executor writes into both records — an outer run recorded as an
// intent under the approval beside the ledger's own count — is counted twice
// here, and that is deliberate: the two records carry no key that joins them,
// and a count that cannot place a run gives the answer that spends less (D4).
// The executor that records them (E10a) writes a ladder run into one of the two
// and not both, which is what makes the sum the count rather than a bound on it.
func LadderRuns(s State, w WorkItem) int {
	n := 0
	if w.Escalation != nil {
		n = w.Escalation.OuterAttempts
	}
	for _, at := range approvalAts(s, w.ID) {
		attempts, covered, ok := approvalAttempts(s, w.ID, at)
		if !ok {
			continue
		}
		normal := 0
		for _, it := range attempts {
			if it.SpecDigest != covered {
				n++
				continue
			}
			normal++
			if normal > ApprovalAttempts {
				n++
			}
		}
	}
	return n
}

// LadderLaunch is the one launch the ladder may make for an item on top of what
// a person's approval plans for it: rung 1's resume, a run of the item's own
// recipe (D2042). Rung 1 is the ladder's floor and today's behaviour — the item
// goes back to its normal recipe loop — and the approval covers it: the one
// standing on the item, or the one a retry spent, whose At the spent marker
// names. What bounds it is the ladder's own count (LadderRuns, G15) and, where
// a person set one, escalation.cost_ceiling (E3).
//
// It is the ladder's executor that asks for this (E10a), for an item a plan
// holds nothing for: wherever the person's own approval still has an attempt to
// give, that attempt is the loop's — started, retried, or declining to be
// retried — and this is what is left once the approval has no attempt left. It
// answers false for an item whose own loop still holds work — an attempt under
// the approval's allowance, or one committed and not reported — because a
// launch beside either of those is the loop's and not the ladder's, and false
// for an item the loop did not fail at: the resume runs the item's own recipe
// again, and a refusal or a cancellation is an answer the second run would
// reach again (failedUnder).
//
// The caps are read here the way Plan reads them, and what comes back approves
// nothing: the approval covers the run, and the ladder has no approval of its
// own (G6, F21). What it starts would be intent-first like any other launch —
// the run id is minted and the intent committed before anything starts (D1231)
// — and that is what does not exist yet: an intent is declared under a person's
// standing approval and nothing else (patch.go's commitIntent), and the item a
// rung 1 resume starts from is one whose approval a retry has already spent, so
// the state would refuse the declaration, and nothing carries this action out
// either (Starter.Start). The write side is E7's file and the rule is reported
// rather than relaxed here, so what the ladder plans for such an item is a
// launch nothing can start (TestLadderResumeIsRefusedByTheWriteSide) — the gap
// that has to close before the executor can walk rung 1 (E10a).
//
// Nothing else on the ladder is this. Every other rung launches a run of
// another recipe — a rebase, the one stronger pass, the judge, a merge, a split
// — and a rung the item is past is a rung whose answer is not another normal
// run of its recipe.
func LadderLaunch(s State, w WorkItem, view RunView, caps Caps) (Action, bool) {
	if caps.KillSwitch || OverCap(s) || BreakerOpen(s) {
		// The three questions Plan asks before it launches anything. They are not
		// the ladder's bound — that is LadderRuns and, where a person set one, the
		// cost ceiling (D2042) — they are whether this node launches at all: a
		// start while the budget switch holds spends what a person stopped, one on
		// a node over its size cap acts on a record that may no longer be the whole
		// one, and one while the breaker's question stands is the pause a restart
		// may not lift.
		return Action{}, false
	}
	if w.Escalation == nil || w.Escalation.Rung != escalation.R1 {
		return Action{}, false
	}
	if !startable(w.Phase) || !dependenciesDone(s, w) {
		return Action{}, false
	}
	if _, open := openIntentOf(s, w.ID); open {
		// An attempt is in flight or never started, and the tick's business with
		// it is its own recovery. A launch beside it would be that attempt's run
		// id asked for twice.
		return Action{}, false
	}
	if a := w.Approved; a != nil && normalRunsUnder(s, w, *a) < a.Attempts {
		// The approval still has an attempt to give the item, and rung 1 is where
		// the item goes back to its own recipe loop: what the loop does with that
		// attempt is its own answer (nextAttempt), and the ladder launches nothing
		// beside it. A run the loop will not retry — a refusal, a cancellation —
		// is not one the ladder overrides either: the resume is the run past the
		// two an approval covers, and a person's own attempt is not the ladder's
		// to spend on work a run already declined.
		return Action{}, false
	}
	at, ok := ladderCoveredAt(w)
	if !ok {
		return Action{}, false
	}
	covered, ok := ladderCoveredDigest(s, w, at)
	if !ok || SpecDigest(w) != covered {
		// The approval covers the spec it was given over, and an edit since is
		// another spec: what the ladder resumes is the run the person approved,
		// or nothing at all.
		return Action{}, false
	}
	if !failedUnder(s, w.ID, at, covered) {
		// A resume is the item's own recipe run again, so it is work the loop
		// failed at and nothing else. What the loop answered instead is its
		// answer to the same spec: a refusal is the run's own word on the work
		// — the thing that already stops the retry (nextAttempt) — and a
		// cancellation is a person stopping it. Neither is a run the ladder
		// makes again, whoever paid for the attempt, and a run the ladder
		// started over after a refusal is one it starts over after every
		// further one, up to MaxOuterAttempts.
		return Action{}, false
	}
	if LadderRuns(s, w) >= MaxOuterAttempts {
		return Action{}, false
	}
	if view.Spend[w.ID].AtCeiling() {
		return Action{}, false
	}
	attempt := attemptsCommitted(s, w.ID) + 1
	return Action{
		Kind:       ActionLadder,
		ItemID:     w.ID,
		IntentID:   IntentID(w.ID, attempt),
		Attempt:    attempt,
		Spec:       SpecOf(w),
		Digest:     SpecDigest(w),
		ApprovalAt: at,
	}, true
}

// CommitFunc persists a state a tick has changed. It is the node's write and
// the whole of the atomicity here: a commit that failed wrote nothing, and
// what the starter does after one is nothing.
type CommitFunc func(ctx context.Context, s State) error

// MintFunc mints the run id for one attempt. It is a function because the
// state package knows no run ids (internal/task mints them in the daemon), and
// what it hands back has to be a fresh one: the same id twice is the same run.
type MintFunc func(now time.Time) (string, error)

// Starter carries out the actions a plan called for, intent first: it mints
// the run id, commits the intent that records it, and asks the launcher to
// start it only then. A crash between any two of those leaves a state that
// says which run id belongs to which attempt, so the next tick re-issues that
// same launch instead of minting a second one — which is the whole of what
// makes a daemon that dies mid-cycle spend once (D1231).
type Starter struct {
	Launcher Launcher
	Commit   CommitFunc
	Mint     MintFunc
}

// Start carries out one action and returns the state the node now holds. A
// refusal — a commit that did not land, a launcher that would not start, an
// action built on a state somebody else has since changed — is an error, and
// what it leaves behind is exactly what was committed: an intent for a launch
// that did not happen is the record recovery re-issues. A launch and a retry
// record their attempt before anything starts and start the spec the state then
// holds, and a re-issue is checked against the state in hand before it starts,
// so nothing here starts a run the state does not carry — neither a run id the
// state never recorded nor a spec it does not hold.
//
// A ladder launch is not one of these (ActionLadder): the write that records an
// attempt is a standing approval's to allow, and the run the ladder asks for is
// one that approval has already spent. Refusing the kind here is what keeps the
// starter from claiming a launch nothing recorded.
func (d Starter) Start(ctx context.Context, s State, a Action, now time.Time) (State, error) {
	switch a.Kind {
	case ActionLaunch, ActionRetry:
		return d.begin(ctx, s, a, now)
	case ActionReissue:
		w, err := reissuable(s, a)
		if err != nil {
			return s, err
		}
		return s, d.start(ctx, SpecOf(w), a.RunID)
	case ActionAbandon:
		return d.abandon(ctx, s, a, now)
	}
	return s, fmt.Errorf("%w: %q is not an action this release carries out", ErrInvalid, safetext.OneLine(string(a.Kind)))
}

// begin is the intent-first start of a new attempt: mint the run id, commit
// the intent that records it, and start it only once the write has landed. What
// it starts is the spec of the item that write left, not the one the action
// named: the state is the record of what a person approved, and an action is a
// proposal built from an earlier read of it.
func (d Starter) begin(ctx context.Context, s State, a Action, now time.Time) (State, error) {
	if d.Mint == nil || d.Commit == nil || d.Launcher == nil {
		return s, errors.New("orchestrator: the starter carries no launcher, no commit or no mint, and a start it cannot record is one it must not make")
	}
	runID, err := d.Mint(now)
	if err != nil {
		return s, err
	}
	op := WorkItemOp{
		ID:     a.ItemID,
		RunID:  &runID,
		Intent: &IntentOp{ID: a.IntentID, RunID: &runID, SpecDigest: &a.Digest, ApprovalAt: &a.ApprovalAt},
	}
	if w, ok := s.Item(a.ItemID); ok && w.Phase != PhaseQueued {
		// failed and blocked reach queued on their way to an attempt, and the
		// phase table has no direct edge from either: the same write that
		// declares the attempt moves the item first.
		queued := PhaseQueued
		op.Phase = &queued
	}
	next, err := Apply(s, Patch{BaseRev: s.Rev, Ops: []Op{{Op: OpWorkItemUpsert, WorkItem: &op}}}, Actor{Class: Daemon, Now: now})
	if err != nil {
		return s, err
	}
	if err := d.Commit(ctx, next); err != nil {
		return s, err
	}
	w, ok := next.Item(a.ItemID)
	if !ok {
		return next, fmt.Errorf("%w: the attempt of %q was committed for no item the state carries, and a start whose spec cannot be read is one it must not make", ErrInvalid, safetext.OneLine(a.ItemID))
	}
	return next, d.start(ctx, SpecOf(w), runID)
}

// start asks the launcher for one run id and refuses an answer that is not the
// id the state recorded.
func (d Starter) start(ctx context.Context, spec Spec, runID string) error {
	if d.Launcher == nil {
		return errors.New("orchestrator: the starter carries no launcher, and a start it cannot make is one it must not claim")
	}
	got, err := d.Launcher.Launch(ctx, spec, runID)
	if err != nil {
		return err
	}
	if got != runID {
		return fmt.Errorf("%w: the launcher started %q for the launch recorded as %q, and a second run id is a run the state does not carry", ErrInvalid, safetext.OneLine(got), safetext.OneLine(runID))
	}
	return nil
}

// close abandons an attempt nothing may start. The intent records that nothing
// ran under its run id, and an item a run is moving through is left blocked
// with the reason on it, where a person can see it: an item left running with
// no run behind it is one no tick could ever pick up again. An item already
// failed or blocked is left where it stands — both are states a person reads,
// and the phase table has no edge from either to blocked, so writing one there
// would be a refusal for every attempt of a failed item rather than a move.
func (d Starter) abandon(ctx context.Context, s State, a Action, now time.Time) (State, error) {
	if d.Commit == nil {
		return s, errors.New("orchestrator: the starter carries no commit, and a change it cannot record is one it must not make")
	}
	outcome := OutcomeAbandoned
	op := WorkItemOp{ID: a.ItemID, Intent: &IntentOp{ID: a.IntentID, Outcome: &outcome}}
	if w, ok := s.Item(a.ItemID); ok && !w.Phase.Done() && w.Phase != PhaseBlocked && w.Phase != PhaseFailed {
		blocked := PhaseBlocked
		op.Phase = &blocked
	}
	if a.Reason != "" {
		note := a.Reason
		op.Note = &note
	}
	next, err := Apply(s, Patch{BaseRev: s.Rev, Ops: []Op{{Op: OpWorkItemUpsert, WorkItem: &op}}}, Actor{Class: Daemon, Now: now})
	if err != nil {
		return s, err
	}
	if err := d.Commit(ctx, next); err != nil {
		return s, err
	}
	return next, nil
}
