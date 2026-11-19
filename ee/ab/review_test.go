// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"math"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// reviewerArm is one arm of a reviewer pair as a run writes it (B1.2, B1.3): a
// run whose own round at the slot's level is what the pair is scored on. The
// round ships with two findings on a reviewer named after the level, at one
// effort, and the level's stage carries one price.
func reviewerArm(runID, championRun, slot string, armTask task.ABArm) runstore.Record {
	rec := pairArm(runID, "pair-1", armTask)
	rec.AB.Slot = task.ABSlot(slot)
	if armTask == task.ABArmChallenger {
		rec.AB.ChampionRun = championRun
	}
	rec.Reviews = []runstore.ReviewSummary{{
		Round: 1, Tier: reviewerTier(task.ABSlot(slot)), Verdict: verdictShip, Findings: 2,
		Model: "reviewer-" + slot, Effort: "high",
	}}
	rec.Stages = []runstore.StageTiming{{Name: reviewerStage[task.ABSlot(slot)], ModelUSD: 0.50}}
	return rec
}

// reviewerTier is the tier a level's round is filed under (runstore's review
// tiers): level 1's first line, level 2's senior review, level 3's adversarial
// one.
func reviewerTier(slot task.ABSlot) string {
	switch slot {
	case task.ABSlotL1:
		return runstore.TierFirst
	case task.ABSlotL2:
		return runstore.TierSenior
	case task.ABSlotL3:
		return runstore.TierAdversarial
	}
	return runstore.TierSenior
}

// reviewerPair is the pair two arms make, through Pairs, so a test that moves a
// validity fact sees no pair at all rather than a scored one.
func reviewerPair(t *testing.T, champion, challenger runstore.Record) Pair {
	t.Helper()
	pairs := Pairs([]runstore.Record{champion, challenger})
	if len(pairs) != 1 {
		t.Fatalf("Pairs over one reviewer pair's two arms returned %d pairs, want 1", len(pairs))
	}
	return pairs[0]
}

// TestABScoreReviewerSlot is B1.3's reviewer reading (D728): a pair whose slot
// is a review level is scored on the two rounds at that level — the reviewer
// that ran, its effort, the word it answered — and never on the coder signals
// score.go reads a pair of whole runs with.
//
// The red step is the first subtest: before this task, `Score` was the only
// reading of a pair, and it answers a reviewer pair with facts about the
// challenger arm's own coding — the champion's head and tests, which a replay
// has none of — so the pair was scored with coder signals. The build failed to
// compile first (`undefined: ScoreReviewer`), and this test is what makes the
// two readings' disagreement visible rather than the two functions' names.
func TestABScoreReviewerSlot(t *testing.T) {
	t.Run("a reviewer pair's signals are its rounds, not the coder's", func(t *testing.T) {
		champion := reviewerArm("run-champion", "", "l2", task.ABArmChampion)
		challenger := reviewerArm("run-challenger", "run-champion", "l2", task.ABArmChallenger)
		// The champion's own run is the one that shipped nothing: no verified
		// test and no head of its own, which is what a coder reading of the pair
		// decides on — and a reviewer pair is two readings of one change
		// (B1.2), so both arms read the champion's patch and neither fact says
		// anything about the two reviewers.
		champion.HeadSHA = ""
		champion.Verify = nil

		p := reviewerPair(t, champion, challenger)
		if got := Score(p, rigor.Pro, Verdict{}).Winner; got != WinChallenger {
			t.Fatalf("the coder reading answered %q, want %q: this subtest is about the two readings disagreeing, so the fixture no longer shows what it is for", got, WinChallenger)
		}
		res := ScoreReviewer(p, task.ABSlotL2, Verdict{})
		if res.Winner != WinTie {
			t.Errorf("winner = %q, want %q: both rounds answered the same way and neither arm halted, so nothing measured says which read the change better", res.Winner, WinTie)
		}
		if res.Champion.Findings != 2 || res.Challenger.Findings != 2 {
			t.Errorf("findings = %d/%d, want 2/2: the signals are the rounds' counts", res.Champion.Findings, res.Challenger.Findings)
		}
		if res.Champion.Model != "reviewer-l2" || res.Champion.Effort != "high" {
			t.Errorf("champion reviewer = %q at %q, want %q at %q: the model and effort are the round's own", res.Champion.Model, res.Champion.Effort, "reviewer-l2", "high")
		}
		if !res.Champion.Answered || !res.Challenger.Answered {
			t.Errorf("answered = %v/%v, want true/true: a round that shipped is a review that answered", res.Champion.Answered, res.Challenger.Answered)
		}
	})

	t.Run("the round read is the one at the pair's level", func(t *testing.T) {
		// One record carrying a round at every level, each with its own count
		// and reviewer: the slot decides which of them is the arm's signal.
		rec := reviewerArm("run-champion", "", "l2", task.ABArmChampion)
		rec.Reviews = []runstore.ReviewSummary{
			{Round: 1, Tier: runstore.TierFirst, Verdict: verdictShip, Findings: 1, Model: "level-1"},
			{Round: 1, Tier: runstore.TierSenior, Verdict: verdictChanges, Findings: 2, Model: "level-2"},
			{Round: 1, Tier: runstore.TierAdversarial, Verdict: verdictShip, Findings: 3, Model: "level-3"},
		}
		for _, tc := range []struct {
			slot    task.ABSlot
			finding int
			model   string
		}{
			{task.ABSlotL1, 1, "level-1"},
			{task.ABSlotL2, 2, "level-2"},
			{task.ABSlotL3, 3, "level-3"},
		} {
			got := reviewerSignals(rec, tc.slot)
			if got.Findings != tc.finding || got.Model != tc.model {
				t.Errorf("%s: signals = %d findings on %q, want %d on %q", tc.slot, got.Findings, got.Model, tc.finding, tc.model)
			}
		}
		if got := reviewerSignals(rec, task.ABSlotCoder); got != (ReviewerSignals{}) {
			t.Errorf("the coder slot answered %+v, want no signals: the coder slot has no review round of its own", got)
		}
	})

	t.Run("the level runs twice and the last round is the level's", func(t *testing.T) {
		// A bounce: the level ran again after the fix, and the round that
		// answered last is the one that decided the level.
		rec := reviewerArm("run-champion", "", "l2", task.ABArmChampion)
		rec.Reviews = append(rec.Reviews, runstore.ReviewSummary{
			Round: 2, Tier: runstore.TierSenior, Verdict: verdictChanges, Findings: 4, Model: "reviewer-l2",
		})
		if got := reviewerSignals(rec, task.ABSlotL2); got.Findings != 4 || got.Verdict != verdictChanges {
			t.Errorf("signals = %d findings answering %q, want 4 answering %q: the second round of the level is the last word", got.Findings, got.Verdict, verdictChanges)
		}
	})

	t.Run("an arm whose round answered nothing loses", func(t *testing.T) {
		champion := reviewerArm("run-champion", "", "l3", task.ABArmChampion)
		challenger := reviewerArm("run-challenger", "run-champion", "l3", task.ABArmChallenger)
		// The word the runner writes for a round whose call died or whose reply
		// could not be read after its retry — never the empty string, which no
		// written review carries (runner.go's seniorLoop, abreplay.go).
		challenger.Reviews[0].Verdict = "none"

		p := reviewerPair(t, champion, challenger)
		if got := ScoreReviewer(p, task.ABSlotL3, Verdict{}).Winner; got != WinChampion {
			t.Errorf("winner = %q, want %q: the challenger's round answered nothing, so it has no finding set in the pair", got, WinChampion)
		}
		if got := reviewerSignals(challenger, task.ABSlotL3); got.Answered {
			t.Errorf("a round recorded as %q reads Answered = true: that word is not a verdict about the change at all", got.Verdict)
		}

		champion.Reviews[0].Verdict = "none"
		if got := ScoreReviewer(p, task.ABSlotL3, Verdict{}).Winner; got != WinTie {
			t.Errorf("winner = %q, want %q: two rounds that answered nothing draw, as two arms that both halted do", got, WinTie)
		}
	})

	t.Run("a halt decides before the rounds do", func(t *testing.T) {
		champion := reviewerArm("run-champion", "", "l2", task.ABArmChampion)
		challenger := reviewerArm("run-challenger", "run-champion", "l2", task.ABArmChallenger)
		champion.Halt = &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run"}

		p := reviewerPair(t, champion, challenger)
		res := ScoreReviewer(p, task.ABSlotL2, Verdict{})
		if res.Winner != WinChallenger {
			t.Errorf("winner = %q, want %q: a halt is already a loss (D567), whatever the rounds say", res.Winner, WinChallenger)
		}
		if !res.Champion.Halted || res.Challenger.Halted {
			t.Errorf("halted = %v/%v, want true/false: the halt is the arm's own record", res.Champion.Halted, res.Challenger.Halted)
		}
	})

	t.Run("the judge's marking sits beside the signals", func(t *testing.T) {
		champion := reviewerArm("run-champion", "", "l2", task.ABArmChampion)
		challenger := reviewerArm("run-challenger", "run-champion", "l2", task.ABArmChallenger)
		marked := Verdict{Winner: WinChallenger, JudgeModel: "judge-model", Matched: true, Misses: 2, FalseAlarms: 1}

		res := ScoreReviewer(reviewerPair(t, champion, challenger), task.ABSlotL2, marked)
		if res.Winner != WinTie {
			t.Errorf("winner = %q, want %q: the judge's answer is shown and never weighed, as it is outside Critical for a coder pair", res.Winner, WinTie)
		}
		if res.Judge.Winner != WinChallenger {
			t.Errorf("the judge's answer reads %q, want %q: it is carried beside the signals", res.Judge.Winner, WinChallenger)
		}
		if !res.Matched || res.Misses != 2 || res.FalseAlarms != 1 {
			t.Errorf("marking = %v %d/%d, want true 2/1: the counts a judge matched are carried as they were read", res.Matched, res.Misses, res.FalseAlarms)
		}
		if got := ScoreReviewer(reviewerPair(t, champion, challenger), task.ABSlotL2, Verdict{}); got.Matched {
			t.Errorf("a pair no judge has matched reads Matched = true: absent is not a marking of zero")
		}
		// A marking never decides a pair: two rounds that drew still draw with
		// a judge that marked the challenger's review.
		res = ScoreReviewer(reviewerPair(t, champion, challenger), task.ABSlotL2, Verdict{Matched: true, Misses: 1})
		if res.Winner != WinTie {
			t.Errorf("winner = %q, want %q: the misses and false alarms do not rank while U4 is unmeasured", res.Winner, WinTie)
		}
	})

	t.Run("the cost is the level's own review", func(t *testing.T) {
		champion := reviewerArm("run-champion", "", "l2", task.ABArmChampion)
		challenger := reviewerArm("run-challenger", "run-champion", "l2", task.ABArmChallenger)
		// The runs' whole cost is the coder's work and not the level's: a pair
		// whose coder cost ten times as much still reports the review it varies.
		champion.Stages = append(champion.Stages, runstore.StageTiming{Name: ImplementStage, ModelUSD: 10})
		challenger.Stages = append(challenger.Stages, runstore.StageTiming{Name: ImplementStage, ModelUSD: 100})

		p := reviewerPair(t, champion, challenger)
		if got := ReviewerCostRatio(p, task.ABSlotL2); got != 1 {
			t.Errorf("cost ratio = %v, want 1: both rounds cost the one price the fixture gave them", got)
		}
		challenger.Stages[0].ModelUSD = 0.75
		if got := ReviewerCostRatio(p, task.ABSlotL2); got != 1.5 {
			t.Errorf("cost ratio = %v, want 1.5: the level's own stage prices the pair", got)
		}
		// A Pair holds the records it was made from, so every move of a stage is
		// followed by taking the pair again rather than by asking the pair made
		// before it.
		challenger.Stages = challenger.Stages[1:]
		p = reviewerPair(t, champion, challenger)
		if got := ReviewerCostRatio(p, task.ABSlotL2); !math.IsNaN(got) {
			t.Errorf("cost ratio = %v, want NaN: an arm with no stage at the level has no price", got)
		}
		challenger.Stages = []runstore.StageTiming{{Name: reviewerStage[task.ABSlotL2], ModelUSD: 0.75}}
		champion.Stages = []runstore.StageTiming{{Name: reviewerStage[task.ABSlotL2], ModelUSD: 0}, {Name: ImplementStage, ModelUSD: 10}}
		p = reviewerPair(t, champion, challenger)
		if got := ReviewerCostRatio(p, task.ABSlotL2); !math.IsNaN(got) {
			t.Errorf("cost ratio = %v, want NaN: a champion whose review cost nothing is no denominator, whatever its coder cost", got)
		}
		if got := ReviewerCostRatio(p, task.ABSlotCoder); !math.IsNaN(got) {
			t.Errorf("cost ratio = %v, want NaN: the coder slot has no review stage of its own", got)
		}
	})
}

// TestABReviewerSlotIsAPairInput: a pair whose two arms vary different slots,
// and a reviewer pair whose challenger read another run's patch, are no pair at
// all (R2, B1.3). Scoring either as one would read a reviewer against a coder's
// signals, or two reviewers of two different changes against each other.
func TestABReviewerSlotIsAPairInput(t *testing.T) {
	t.Run("two slots are not one pair", func(t *testing.T) {
		champion := reviewerArm("run-champion", "", "l2", task.ABArmChampion)
		challenger := reviewerArm("run-challenger", "run-champion", "l3", task.ABArmChallenger)
		if pairs := Pairs([]runstore.Record{champion, challenger}); len(pairs) != 0 {
			t.Errorf("Pairs returned %d pairs, want 0: a slot the two arms disagree on is two experiments, not one pair", len(pairs))
		}
	})

	t.Run("a reviewer challenger reads the champion's own run", func(t *testing.T) {
		champion := reviewerArm("run-champion", "", "l1", task.ABArmChampion)
		challenger := reviewerArm("run-challenger", "run-other", "l1", task.ABArmChallenger)
		if pairs := Pairs([]runstore.Record{champion, challenger}); len(pairs) != 0 {
			t.Errorf("Pairs returned %d pairs, want 0: the challenger names another run's patch, so the two arms are not two readings of one change", len(pairs))
		}
		challenger.AB.ChampionRun = champion.RunID
		if pairs := Pairs([]runstore.Record{champion, challenger}); len(pairs) != 1 {
			t.Errorf("Pairs returned %d pairs, want 1: the same pair with the champion's own run named is a pair", len(pairs))
		}
	})
}
