// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"math"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file reads the reviewer slots (plan task B1.3, D728): a pair whose
// ab.slot is a review level, scored and reported on what its two rounds hold,
// beside the coder signals score.go reads a coder pair with.
//
// The two arms of such a pair are two readings of one change rather than of two
// runs' code (B1.2): the champion arm is the champion run's own round at that
// level, and the challenger is a shadow replay of the same level, on another
// reviewer model, over the champion's recorded patch. So the signals a coder
// pair is ranked on say nothing here — a replay has no implement stage of its
// own, verifies nothing and reaches no head — and a report that read one
// through them would draw a figure about the task instead of about the
// reviewers.
//
// What a reviewer pair has instead is what each of its rounds carries: the
// findings the review raised, the verdict it answered, the reviewer model and
// effort it ran at, and what the level's stage cost. The misses and false
// alarms D728 names are a judge's reading of two finding sets, and whether that
// reading can be relied on is U4, whose check (Check 211) has not been run —
// B1.3's first commit says so — so both are carried here as the judge marked
// them and neither decides a pair.

// reviewerStage is the stage name each slot's round is filed under in the
// record, in the runner's own vocabulary (internal/runner's reviewStages and
// the names it records its stage timings under): the level 1 review runs under
// "review_first" — the stage is the first_line recipe step's, under the name
// the review-level reading gave it, so recipe.StepFirstLine's own spelling is
// deliberately not used here — level 2 under "review" and level 3 under
// "adversarial". The map is what makes a slot's cost readable from a record,
// and it is a map rather than a switch so that a slot with no review stage of
// its own — the coder — is a miss and not a default.
var reviewerStage = map[task.ABSlot]string{
	task.ABSlotL1: "review_first",
	task.ABSlotL2: string(recipe.StepReview),
	task.ABSlotL3: string(recipe.StepAdversarial),
}

// SlotOf is the slot a record's pair varies: task.AB.EffectiveSlot, so the
// empty spelling reads as the coder slot rather than as a hole (a record
// written before the field existed is the coder pair it was), and a record
// carrying no ab block at all is no arm of a sampled launch and reads as the
// coder slot too — nothing here asks whether the record is an arm.
func SlotOf(rec runstore.Record) task.ABSlot {
	if rec.AB == nil {
		return task.ABSlotCoder
	}
	return rec.AB.EffectiveSlot()
}

// ReviewerRun is the reviewer one arm ran at the slot's level: the model and
// reasoning effort of that arm's own round, and whether the record carries such
// a round at all. A record that ran no round at the level — the slot is the
// coder's, or the level was off in that run, or the round is one of the reviews
// written before these fields existed — answers "", "", false.
//
// The model and effort are read from the round and not from the config or the
// task, because the round states the reviewer that ran: it is the level's own
// model when a layer named it one, and it is the effort that reached the stage
// (runstore.ReviewSummary.Effort), which an empty value states as "none" rather
// than leaving it unknown.
func ReviewerRun(rec runstore.Record, slot task.ABSlot) (model, effort string, ok bool) {
	round := slotRound(rec, slot)
	if round == nil {
		return "", "", false
	}
	return round.Model, round.Effort, true
}

// slotRound is the arm's own round at the slot's level: the last round the
// record filed under it, which is the round that decided the level (the first
// line and the senior review each run at most once per round of the loop, so a
// level that ran twice — a bounce — is read at the round that answered last).
// A record carries a round's tier, never the slot it was launched for, so the
// slot is matched through reviewRank: level 1's tier is TierFirst, level 2's is
// TierSenior (and the empty tier, the senior review of a run that had no first
// line), level 3's is TierAdversarial.
func slotRound(rec runstore.Record, slot task.ABSlot) *runstore.ReviewSummary {
	rank, ok := slotRank(slot)
	if !ok {
		return nil
	}
	for i := len(rec.Reviews) - 1; i >= 0; i-- {
		if reviewRank(rec.Reviews[i].Tier) == rank {
			return &rec.Reviews[i]
		}
	}
	return nil
}

// slotRank is the level a slot's round answers for, in reviewRank's vocabulary.
// The coder slot has no rank: it runs no review of its own.
func slotRank(slot task.ABSlot) (int, bool) {
	switch slot {
	case task.ABSlotL1:
		return rankFirstLine, true
	case task.ABSlotL2:
		return rankSenior, true
	case task.ABSlotL3:
		return rankAdversary, true
	}
	return 0, false
}

// ReviewerSignals is one reviewer arm's signals (B1.3): the one round it ran at
// the pair's level, as the arm's own record carries it.
type ReviewerSignals struct {
	// Halted is a run a limit stopped (Record.Halt), read exactly as the coder
	// signals read it (D567): a halt is a loss, and a halted arm is one whose
	// review at the level either never ran or is not what the run was doing.
	Halted bool
	// Findings is how many findings that round raised and Verdict the word it
	// answered. Both are the record's own: a finding has no identity (F18), so
	// counting them is all a reader of the record can do, and matching two
	// reviews' findings against each other is a judge's work (D728) and not
	// this struct's.
	//
	// Findings is reported and never ranked on: a review that raises more
	// findings is not a better one, and which of them were real is exactly what
	// misses and false alarms would say — and they are unmeasured (U4).
	Findings int
	Verdict  string
	// Answered reports whether the round answered one of the words a review
	// answers with. A round that answered none — the call died, or its reply
	// could not be read after the retry the loop gives it (B1.2) — left no
	// finding set to compare, and that is the one state this file ranks: an arm
	// no reviewer read is an arm with nothing in the pair, the way an arm with
	// no head has nothing in a coder pair (D561).
	Answered bool
	// Model and Effort are the reviewer that round ran on, which is part of the
	// group a reviewer pair is reported in (B1.3's slot × model × effort):
	// empty for an arm whose record carries no round at that level.
	Model, Effort string
}

// ReviewerResult is one reviewer pair scored at the slot it varies (B1.3).
type ReviewerResult struct {
	// PairID is the pair this result belongs to (Pair.ID).
	PairID string
	// Slot is the level the pair varies, which is never the coder slot: a
	// reviewer pair's slot comes from the records and is what routed it here.
	Slot task.ABSlot
	// Winner is the arm that took the pair on the signals this file reads.
	Winner Winner
	// Champion and Challenger are the two rounds' signals, whether or not the
	// arm won: a reader asking why the shadow lost reads them here.
	Champion   ReviewerSignals
	Challenger ReviewerSignals
	// Judge is the judge's answer for the pair, the zero Verdict for one no
	// judge decided. It sits beside the signals and never among them, as it
	// does outside Critical for a coder pair (Score): where the two disagree
	// both are shown.
	Judge Verdict
	// Matched reports whether the judge matched the pair's two finding sets,
	// and Misses and FalseAlarms are what it matched: the champion's findings
	// the challenger's review lacked, and the challenger's findings it marked
	// as not real (D728). Matched is false for a judgement that matched
	// nothing, which is not the same fact as a marking of zero, and it is what
	// the report reads to print no figure for a group no judge has marked.
	//
	// No judge run built here writes a marking (judge.go): matching two finding
	// sets is a reading of free text, which F18 rules has no identity, and U4 —
	// whether the A5 judge does it reliably — is unmeasured. So these are read
	// from the wire a future judge writes them on and are absent today.
	Matched             bool
	Misses, FalseAlarms int
}

// ScoreReviewer reads one reviewer pair at the slot it varies (B1.3, D728).
//
// The pair is ranked on what its two rounds can be compared on with no
// judgement, and on nothing else: D567's halt first — a run a limit stopped is
// one whose review at the level either never ran or was not what the run was
// doing, and a halt is already a loss — and then whether each round answered.
// D728's misses and false alarms are a judge's reading of two finding sets, and
// U4, whether that reading can be relied on, is unmeasured: a pair decided on
// those counts would be decided on a figure nobody has checked, so they are
// reported beside the ranking and never inside it, as a coder pair's verdict is
// outside Critical. The finding counts do not rank for the same reason: a review
// that raised more findings is not a better one, and which of them were real is
// exactly what the two counts would say.
//
// So two arms neither of which halted and both of whose rounds answered draw —
// two reviews of one change, with no measured way to say which read it better —
// and so do two that both halted or both failed to answer, the way a coder
// pair's vector draws on equal signals. Only a pair whose arms differ on the
// halt, and then on whether they answered, has a winner.
//
// The pair's validity is not re-asked here: Pairs is where R2 is read. Every
// valid reviewer pair counts, as a coder pair outside Critical does, and the
// judge is required at neither: a Critical coder pair requires it because the
// hard signals miss design quality there, and a reviewer pair has no hard
// signals of its own for it to weigh against.
func ScoreReviewer(p Pair, slot task.ABSlot, v Verdict) ReviewerResult {
	res := ReviewerResult{
		PairID:      p.ID,
		Slot:        slot,
		Champion:    reviewerSignals(p.Champion, slot),
		Challenger:  reviewerSignals(p.Challenger, slot),
		Judge:       v,
		Matched:     v.Matched,
		Misses:      v.Misses,
		FalseAlarms: v.FalseAlarms,
	}
	switch {
	case res.Champion.Halted != res.Challenger.Halted:
		// D567's rule, which every pair is ranked on first: a halt is already a
		// loss, so the arm a limit stopped is the arm that did not work within
		// what a run is allowed to cost.
		if res.Champion.Halted {
			res.Winner = WinChallenger
		} else {
			res.Winner = WinChampion
		}
	case res.Champion.Answered == res.Challenger.Answered:
		res.Winner = WinTie
	case res.Champion.Answered:
		res.Winner = WinChampion
	default:
		res.Winner = WinChallenger
	}
	return res
}

// reviewerSignals reads one arm's record into its round's signals (B1.3).
// Nothing is inferred from the other arm, and no field of the round is asked
// twice.
func reviewerSignals(rec runstore.Record, slot task.ABSlot) ReviewerSignals {
	// The halt is a fact about the run and is read whether or not the level left
	// a round: a halted arm whose review never ran is a halted arm.
	s := ReviewerSignals{Halted: rec.Halt != nil}
	round := slotRound(rec, slot)
	if round == nil {
		return s
	}
	s.Findings = round.Findings
	s.Verdict = round.Verdict
	// A round answered only when it answered one of the two words a review
	// answers with (verdictShip, verdictChanges). A review whose call died, or
	// whose reply could not be read after the retry the loop gives it, is
	// recorded as Verdict "none" — not an empty string, which no written review
	// carries — and "none" is not a verdict about the change at all (outcome.go
	// reads it as a round nobody read), so it is the answer this file ranks and
	// not an approval.
	s.Answered = round.Verdict == verdictShip || round.Verdict == verdictChanges
	s.Model = round.Model
	s.Effort = round.Effort
	return s
}

// ReviewerCostRatio is one reviewer pair's price (B1.3): what the slot's review
// cost the challenger's arm over what it cost the champion's, read from the
// stage the level ran under (StageTiming.ModelUSD) rather than from the runs'
// whole cost, which is the coder's work and not the level's.
//
// NaN — no figure, which is not a ratio of zero — for a pair either of whose
// arms carries no stage at the level, and for a champion whose review cost
// nothing: a ratio to zero has no value, and the report reads NaN as the empty
// cell pairCostRatio does for a coder pair.
func ReviewerCostRatio(p Pair, slot task.ABSlot) float64 {
	champion, ok := slotCost(p.Champion, slot)
	if !ok || champion <= 0 {
		return math.NaN()
	}
	challenger, ok := slotCost(p.Challenger, slot)
	if !ok {
		return math.NaN()
	}
	return challenger / champion
}

// slotCost is what one arm's reviews at the slot's level cost, summed over the
// stages the level ran under that name (a level that ran twice — a bounce —
// costs what both rounds cost). It answers false for a record that carries no
// such stage and for the coder slot, which has no review stage of its own.
func slotCost(rec runstore.Record, slot task.ABSlot) (float64, bool) {
	name, ok := reviewerStage[slot]
	if !ok {
		return 0, false
	}
	total, found := 0.0, false
	for _, stage := range rec.Stages {
		if stage.Name == name {
			total, found = total+stage.ModelUSD, true
		}
	}
	return total, found
}
