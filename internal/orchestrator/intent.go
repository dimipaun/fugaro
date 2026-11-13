package orchestrator

import (
	"fmt"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// abandonEditedSpec is why an attempt recovery refused to re-issue was closed.
// It is a sentence rather than a code because the daemon leaves it on the item
// as the note a person reads.
const abandonEditedSpec = "the attempt was abandoned: the item's spec is not the one the intent was committed for, so nothing was started under its run id and nothing will be"

// abandonReplacedApproval is why an attempt is closed when the item still is
// the spec the intent was committed for but the approval behind it has been
// replaced: a person's new go is another approval, and the attempt under the
// old one is not one to start. The spec is untouched in that case, so the note
// says what actually closed the attempt — it is the sentence a person reads.
const abandonReplacedApproval = "the attempt was abandoned: a person approved the item again since, so nothing was started under its run id and nothing will be"

// abandonFinishedItem is the other reason: the item is done or cancelled, and a
// finished item never leaves its phase, so an attempt of it that never started
// is closed rather than launched. A cancellation is a person stopping the work,
// and starting a run for it is the one thing that must not happen.
const abandonFinishedItem = "the attempt was abandoned: the item is finished, so nothing was started under its run id and nothing will be"

// openIntentOf is the attempt of an item that has not reported, and false when
// every attempt of it has. It is openIntent with the intent itself, which is
// what planning and recovery read: the run id it recorded and the approval it
// spent.
func openIntentOf(s State, itemID string) (Intent, bool) {
	id := openIntent(s.Intents, itemID)
	if id == "" {
		return Intent{}, false
	}
	for _, it := range s.Intents {
		if it.ID == id {
			return it, true
		}
	}
	return Intent{}, false
}

// startable reports whether a plan may start an attempt at an item in this
// phase. It is the phases an attempt may be declared from: queued itself and
// the two a person's new approval re-opens, which reach queued in the same
// write that declares the attempt. An item a run is moving through, and one
// that is finished, are not ones to start.
func startable(p Phase) bool {
	return p == PhaseQueued || p == PhaseFailed || p == PhaseBlocked
}

// dependenciesDone reports whether every item this one depends on is done. A
// dependency the list does not carry holds the item rather than being read as
// done: that is a thing for a person to look at, and holding back is the
// direction that starts nothing.
func dependenciesDone(s State, w WorkItem) bool {
	for _, id := range w.DependsOn {
		dep, ok := s.Item(id)
		if !ok || !dep.Phase.Done() {
			return false
		}
	}
	return true
}

// approvalAttempts is the item's attempts committed under one approval, oldest
// first, and the digest that approval covers.
//
// The digest is read off the oldest of them: an intent is declared only when
// its digest is the approval's own (commitIntent), so the first attempt under
// an approval is a run of the spec it was given over. That is the only copy of
// it left once the approval is spent — the write that spends it clears Approved
// and the marker carries the At alone — which is what lets a rung 1 resume be
// checked against the spec the person approved.
func approvalAttempts(s State, itemID string, at time.Time) ([]Intent, Digest, bool) {
	var attempts []Intent
	for _, it := range s.Intents {
		if IntentItem(it.ID) == itemID && it.ApprovalAt.Equal(at) {
			attempts = append(attempts, it)
		}
	}
	if len(attempts) == 0 {
		return nil, "", false
	}
	slices.SortStableFunc(attempts, func(a, b Intent) int { return IntentAttempt(a.ID) - IntentAttempt(b.ID) })
	return attempts, attempts[0].SpecDigest, true
}

// approvalAts is the approvals an item's intents name, each one once, in the
// order the state carries them.
func approvalAts(s State, itemID string) []time.Time {
	var ats []time.Time
	for _, it := range s.Intents {
		if IntentItem(it.ID) != itemID || slices.ContainsFunc(ats, it.ApprovalAt.Equal) {
			continue
		}
		ats = append(ats, it.ApprovalAt)
	}
	return ats
}

// normalRunsUnder is how many of an item's attempts under one approval are runs
// of the spec that approval covers, which is the count ApprovalAttempts bounds
// (G15, D2042). The person's approval pays for the item's own recipe; a run of
// another recipe the ladder launched under the same approval — a rebase, the
// one stronger pass — is the ladder's own, is counted by LadderRuns, and spends
// nothing of theirs.
func normalRunsUnder(s State, w WorkItem, approval Approval) int {
	n := 0
	for _, it := range s.Intents {
		if IntentItem(it.ID) == w.ID && it.ApprovalAt.Equal(approval.At) && it.SpecDigest == approval.SpecDigest {
			n++
		}
	}
	return n
}

// failedUnder reports whether the newest attempt the approval covers failed,
// which is the only thing a retry answers. A refusal, a cancellation and a
// success are not failures, and an attempt that has not reported is not one to
// retry either. An attempt under the same approval that the approval does not
// cover — a run of another recipe the ladder launched — is not the item's own
// last attempt, so it neither answers a retry nor stops one.
func failedUnder(s State, w WorkItem, approval Approval) bool {
	newest, found := Intent{}, false
	for _, it := range s.Intents {
		if IntentItem(it.ID) != w.ID || !it.ApprovalAt.Equal(approval.At) || it.SpecDigest != approval.SpecDigest {
			continue
		}
		if !found || IntentAttempt(it.ID) > IntentAttempt(newest.ID) {
			newest, found = it, true
		}
	}
	return found && newest.Outcome == OutcomeFailed
}

// approvalCovers reports whether the standing approval is the one an intent
// spent: recovery re-issues only what a person has not replaced.
//
// A ladder intent is covered here like any other attempt of the item's own
// recipe: a rung 1 resume carries the At of the approval that covers it, which
// is why a resume a person's approval still stands over is re-issued by
// approvalCovers and one under an approval a retry spent is re-issued by
// spentBy. Neither of them reads the ladder at all: what a crash left is an
// attempt of an item the approval covers (D2042), and a daemon that re-issued
// only the approval's own two would leave every rung 1 resume abandoned.
func approvalCovers(w WorkItem, it Intent) bool {
	a := w.Approved
	return a != nil && a.At.Equal(it.ApprovalAt) && a.SpecDigest == it.SpecDigest
}

// spentBy reports whether the item's spent marker names the approval this
// intent spent. Committing a retry's intent clears Approved in the same write,
// so "the approval still stands" alone would abandon every retry that crashed
// before its task existed — and every rung 1 resume, which is committed under
// an approval that is spent already (D2042, G15).
func spentBy(w WorkItem, it Intent) bool {
	return w.SpentApprovalAt != nil && w.SpentApprovalAt.Equal(it.ApprovalAt)
}

// ladderCoveredAt is the At of the approval that covers the ladder's launch on
// an item: the approval standing on it, or the one a retry spent, which the
// item's marker names (D2042). It is false for an item with no approval to
// spend — one nobody has approved, or one a finish cleared — and the ladder
// launches nothing under it: its runs are the approval's to spend and it has
// none of its own (G6, F21).
func ladderCoveredAt(w WorkItem) (time.Time, bool) {
	if a := w.Approved; a != nil {
		return a.At, true
	}
	if at := w.SpentApprovalAt; at != nil {
		return *at, true
	}
	return time.Time{}, false
}

// ladderCoveredDigest is the digest that approval covers, read for the launch
// the ladder is about to make: the one the approval carries while it stands,
// and, once it is spent, the one the item's attempts under it ran.
func ladderCoveredDigest(s State, w WorkItem, at time.Time) (Digest, bool) {
	if a := w.Approved; a != nil {
		return a.SpecDigest, true
	}
	_, covered, ok := approvalAttempts(s, w.ID, at)
	return covered, ok
}

// nextAttempt is the attempt a plan may start for an item, or false when there
// is none: a person's standing approval, the item still being the spec that
// approval covers, every dependency done, and an attempt left under it. The
// first of those attempts needs nothing else, and the one after it is a retry,
// which needs the newest attempt the approval covers to have failed.
//
// What the allowance counts is the approval's own runs and nothing beside them
// (G15): a run of another recipe the ladder launched under the same At — a
// rebase, the one stronger pass — is the ladder's, and a person's two attempts
// are still theirs to spend. A rung 1 resume is not planned here at all
// (LadderLaunch): the ladder's executor asks for that one, and what this
// function reads is what a person's approval plans by itself.
//
// The write that records the attempt does not count that way yet, so a retry
// this plans over a ladder run recorded under the approval is one it refuses
// (TestRetryOverALadderRunIsRefusedByTheWriteSide, and the gap this task
// reports): patch.go's commitIntent spends the allowance by every attempt
// recorded under it, whatever recipe ran it (AttemptsUnder). What the write
// side owes is this count, and until it holds it the item waits — the direction
// that starts nothing rather than the one that spends a person's attempt on a
// run the ladder launched.
func nextAttempt(s State, w WorkItem) (Action, bool) {
	approval := w.Approved
	if approval == nil || !startable(w.Phase) {
		return Action{}, false
	}
	digest := SpecDigest(w)
	if digest != approval.SpecDigest {
		// The person approved the spec they were shown. An edit since is
		// another spec, so there is nothing here to start under their go.
		return Action{}, false
	}
	if !dependenciesDone(s, w) {
		return Action{}, false
	}
	under := normalRunsUnder(s, w, *approval)
	if under >= approval.Attempts {
		// The allowance is spent. What opens another one is a person, and the
		// daemon cannot approve anything on their behalf.
		return Action{}, false
	}
	attempt := attemptsCommitted(s, w.ID) + 1
	action := Action{
		ItemID:     w.ID,
		IntentID:   IntentID(w.ID, attempt),
		Attempt:    attempt,
		Spec:       SpecOf(w),
		Digest:     digest,
		ApprovalAt: approval.At,
	}
	if under == 0 {
		action.Kind = ActionLaunch
		return action, true
	}
	if !failedUnder(s, w, *approval) {
		// The allowance is not spent, but only a failure is retried: a refusal
		// is not one, and neither is a run a person stopped.
		return Action{}, false
	}
	action.Kind = ActionRetry
	return action, true
}

// recovery is what is left for an item whose attempt has not reported: a run
// that started is in flight, an attempt still inside its claim window is left
// alone — the platform may be starting it — and past that the attempt is
// re-issued under the run id the state recorded, which is the same launch and
// never a second one. An item that is no longer the spec its intent was
// committed for is abandoned instead: nothing may start under that intent, and
// the alternative to closing it is an attempt nothing will ever report on. So
// is an item whose approval has been replaced since the intent was committed
// under it, and so is a finished item, whatever its spec and whichever approval
// the attempt spent. Each leaves the note that says which of the three it was.
func recovery(s State, view RunView, w WorkItem, it Intent, now time.Time) (Action, bool) {
	if view.Run(it.RunID).Started {
		return Action{}, false
	}
	if now.Sub(it.At) < ClaimTTL {
		return Action{}, false
	}
	a := Action{
		ItemID:     w.ID,
		IntentID:   it.ID,
		Attempt:    IntentAttempt(it.ID),
		RunID:      it.RunID,
		Digest:     it.SpecDigest,
		ApprovalAt: it.ApprovalAt,
	}
	if w.Phase.Done() {
		// A run is going for an item a person stopped, or a retry that spent
		// its approval never started and the item was cancelled under it. No
		// window makes either of those a launch: a cancelled item is one
		// somebody called the work off, and its phase is one it never leaves.
		a.Kind = ActionAbandon
		a.Reason = abandonFinishedItem
		return a, true
	}
	if SpecDigest(w) != it.SpecDigest {
		a.Kind = ActionAbandon
		a.Reason = abandonEditedSpec
		return a, true
	}
	if !approvalCovers(w, it) && !spentBy(w, it) {
		a.Kind = ActionAbandon
		a.Reason = abandonReplacedApproval
		return a, true
	}
	a.Kind = ActionReissue
	a.Spec = SpecOf(w)
	return a, true
}

// reissuable checks a re-issue against the state it is carried out on, and
// hands back the item that state carries. A plan is made from one read of the
// node and a tick carries it out against another, so what a re-issue starts is
// the attempt the state still holds: the open intent the action names, under
// the run id that intent recorded, for the spec its digest pins, under the
// approval that stands or the one it spent, at an item that is not finished.
// The spec it starts from is read off that item rather than taken from the
// action, so what the launcher is handed is the state's own spec.
func reissuable(s State, a Action) (WorkItem, error) {
	w, ok := s.Item(a.ItemID)
	if !ok {
		return WorkItem{}, fmt.Errorf("%w: no work item %q", ErrNotFound, safetext.OneLine(a.ItemID))
	}
	it, open := openIntentOf(s, a.ItemID)
	if !open || it.ID != a.IntentID || it.RunID != a.RunID {
		return WorkItem{}, fmt.Errorf("%w: %q carries no attempt recorded as %q under run %q", ErrInvalid, safetext.OneLine(a.ItemID), safetext.OneLine(a.IntentID), safetext.OneLine(a.RunID))
	}
	if w.Phase.Done() || SpecDigest(w) != it.SpecDigest || it.SpecDigest != a.Digest || !(approvalCovers(w, it) || spentBy(w, it)) {
		return WorkItem{}, fmt.Errorf("%w: the attempt %q of %q is not one to start", ErrInvalid, safetext.OneLine(it.ID), safetext.OneLine(a.ItemID))
	}
	return w, nil
}
