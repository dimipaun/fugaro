// Rung 4's split (E10b): the item the ladder cannot merge is stopped, and the
// work continues as a scope a person approves.
//
// Rung 4 is the ladder's last rung and the one that stops trying. The judge
// blocked the merge on a fundamental issue (D2031) and the same specification
// has already been through the rungs below, so what is left is not another
// attempt — D1246's rule is that a split never relaunches the spec the ladder
// has just decided about, and it is enforced by what is not written here as much
// as by what is: newItemOp copies the repo, the recipe and the tier and hands on
// no specification, so the item it files cannot be the run that failed.
//
// Three things are kept and one is not. The branch and the pull request stay
// exactly as they are: they are the work's, a person may still read them, and
// nothing in this file touches the host at all. The item is cancelled, because
// what continues is the scope and not the item. And the scope itself is the
// judge's own named blocker, clipped and fenced into the new item's brief —
// rung 4 invents no scope of its own, and a blocker that cannot be read is a
// question for a person (G5) rather than a split this code made up.
//
// It is built here rather than in the merge file because it shares nothing with
// rung 3 but the seams: it launches nothing, merges nothing, asks no host, and
// reads exactly one field of the verdict.
package orchestrator

import (
	"context"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// split is rung 4 for one item: read the verdict, stop the item, and file the
// scope the blocker names — or ask a person where there is no blocker to file.
//
// The order is the reverse of rung 3's in one place and for one reason: the item
// is cancelled whatever comes of the scope, because rung 4 is a rung the item has
// left, and the split's own item is filed beside it. A cycle that re-runs after
// the write landed therefore finds the item cancelled — the ladder reads no
// further rungs for it — and still files nothing twice, because the split item's
// name is derived from the item's id (G4).
func (s RungSeams) split(ctx context.Context, item WorkItem, step escalation.Step, reason escalation.Reason, read LadderReading) (bool, error) {
	now, ok := s.Now()
	if !ok {
		return false, ErrNoServerTime
	}
	st, err := s.Node.State(ctx)
	if err != nil {
		return false, err
	}
	if cur, ok := st.Item(item.ID); ok {
		item = cur
	}
	mr, err := s.Read.MergeAt(ctx, item, read.HeadSHA)
	if err != nil {
		return false, err
	}
	out := splitOutcome(mr)
	moved := ladderReached(item) != step.Rung
	var ops []Op
	if op, ok := mergeLedgerOp(item, step, reason, read.HeadSHA, item.RunID, false, moved, now); ok {
		ops = append(ops, op)
	}
	ops = append(ops, cancelItemOp(item.ID))
	var told *Notification
	if scope := splitScope(mr); scope == "" {
		if op, n, ok := mergeQuestionOp(st, item, splitQuestion(item)); ok {
			ops, told = append(ops, op), &n
		}
	} else if _, ok := st.Item(splitID(item.ID)); !ok {
		ops = append(ops, newItemOp(splitID(item.ID), splitTitle(item), splitBrief(item, scope), item))
	}
	if err := s.Node.Apply(ctx, ops); err != nil {
		return false, err
	}
	line := "escalation: " + ladderMove(step, reason, item.RunID) + " " + mergeLine(out)
	entry := Entry{Subject: item.ID, Transition: mergeTransition(step.Rung, out.Kind, read.HeadSHA), Text: line}
	if err := s.Log.Append(ctx, entry); err != nil {
		return false, err
	}
	if moved {
		// The rung starts no run, so the comment names the item's own run:
		// ladderComment's marker is for the pull request's reader, and a marker
		// around no run id is one no reader can place.
		if err := s.Comment.LadderComment(ctx, item, ladderComment(step, ladderMove(step, reason, item.RunID), item.RunID)); err != nil {
			s.warn(err)
		}
	}
	if told != nil {
		s.say(ctx, *told)
	}
	// Rung 4 always moves: the item is stopped and the scope is filed or asked
	// for, which is the whole of what the rung is, and the item a cycle reads
	// after it is one the ladder reads no further rungs for.
	return true, nil
}

// splitOutcome is rung 4's outcome: the same kind either way — the item is
// stopped and a scope is filed or asked for — with the code that says whether
// the judge named one.
func splitOutcome(mr MergeReading) MergeOutcome {
	fact := GateUnscoped
	if splitScope(mr) != "" {
		fact = GateScoped
	}
	return MergeOutcome{Kind: MergeSplit, Facts: []GateReason{fact}, Verdict: mr.Verdict}
}

// splitScope is the judge's named blocker as rung 4's scope, and "" where there
// is none to scope a split to.
//
// The verdict is rung 4's whole input (D2031): a blocker is what D1290's split is
// scoped to, and the two ways there is none — no readable verdict at all, and a
// block that named no fundamental issue — both end the same way, in a person's
// question rather than a scope this code invented (G5). A verdict that is
// readable and blocked but whose blocker is empty is already read as unreadable
// by the record's own reader (runstore.MergeJudge.Unreadable), so the two cases
// are one check here.
//
// The text is a record's own field: it is made safe for a terminal, clipped to
// the bound the record itself uses, and fenced where it is written into a brief
// (splitBrief), because a blocker is the judge's words and not this program's.
func splitScope(mr MergeReading) string {
	v := mr.Verdict
	if !mr.Read || v.Unreadable || v.Verdict != runstore.MergeJudgeBlocked {
		return ""
	}
	return safetext.Clip(safetext.MultiLine(v.Blocker), runstore.MaxMergeJudgeBlocker)
}

// splitID is the name the scope rung 4 files is written under.
func splitID(itemID string) string { return mergeName(itemID, "-split") }

// splitTitle is what the new item is called: the same shape the triage's smaller
// scopes use, because it is the same kind of work — a piece of the item's that a
// person approves before anything runs.
func splitTitle(item WorkItem) string { return "a smaller scope: " + item.Title }

// splitBrief is what the person approving the scope reads: the judge's blocker,
// fenced, and the two things they need to know about it — that the specification
// they already read is not what this item runs (D1246), and that the branch and
// the pull request are untouched.
func splitBrief(item WorkItem, scope string) string {
	return "The escalation ladder split " + item.ID + ": the merge judge blocked this change on a fundamental issue, " +
		"and what continues is a scope a person approves. The judge named it:\n\n" +
		fenced(scope, maxScopeFenceRunes) +
		"\n\nThis item is that scope and not the specification the ladder decided about: nothing of the original brief is relaunched, " +
		"and the branch and the pull request stay as they are. Approve the item when the scope is the right work."
}

// splitQuestion is what a person is asked when rung 4 has nothing to scope a
// split to: the item is stopped either way, and the scope is theirs to name.
func splitQuestion(item WorkItem) string {
	return "The escalation ladder split " + item.ID + ": rung 4 files the work as a scope a person approves, and the merge judge named no blocker to scope it to — " +
		"no readable verdict stands for this head, or the verdict that blocked named no fundamental issue. Read the item and decide what the split should be."
}

// cancelItemOp stops the item. Rung 4 keeps the branch and the pull request —
// they are the work's and a person may still merge them by hand — and takes the
// item out of the queue, because what continues is the scope and never another
// attempt at the same specification (D1246).
func cancelItemOp(id string) Op {
	phase := PhaseCancelled
	return Op{Op: OpWorkItemUpsert, WorkItem: &WorkItemOp{ID: id, Phase: &phase}}
}

const (
	// maxScopeFenceRunes caps the blocker as the brief carries it. The record
	// holds the same text at runstore.MaxMergeJudgeBlocker, and the room left
	// for it here is what keeps the whole brief inside MaxBriefRunes — the
	// scope is what a person reads before approving, and a brief the state
	// clipped would be one whose fence was cut in half.
	maxScopeFenceRunes = runstore.MaxMergeJudgeBlocker
)
