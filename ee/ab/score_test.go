// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The pair every test moves one fact of: one start commit (D571), one resolved
// mode, tier and recipe, a passing test on a clean tree at the final head and
// one shipped senior round. A test that changes one of them is a test about
// that one.
const (
	pairStartSHA = "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f"
	pairHeadSHA  = "1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e"
	pairRecipe   = "default"
)

// pairArm is one arm's record as a run writes it.
func pairArm(runID, pairID string, arm task.ABArm) runstore.Record {
	return runstore.Record{
		RunID:    runID,
		StartSHA: pairStartSHA,
		HeadSHA:  pairHeadSHA,
		AB:       &task.AB{PairID: pairID, Arm: arm},
		Rigor:    &runstore.RigorRecord{Mode: rigor.Pro},
		Tier:     &runstore.TierRecord{Tier: tier.Small},
		Recipe:   &runstore.RecipeRecord{Name: pairRecipe, SHA256: strings.Repeat("ab", 32)},
		Verify:   []verify.Record{{Kind: verify.KindTest, HeadSHA: pairHeadSHA, CleanTree: true, Passed: true}},
		Reviews:  []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierSenior, Verdict: verdictShip}},
	}
}

// onePair scores the pair the two arms make, through Pairs, so a test that
// moves a validity fact sees no pair at all rather than a scored one.
//
// A judge has read the pair: every test here is about how the hard signals
// rank, and at Critical a pair no judge has decided is not scored at all
// (D561), so a test that wants the signals read hands over a verdict — a tie,
// which is a judgement like any other and never a signal (TestCriticalNeedsVerdict
// and TestVerdictShownBesideSignals are the two that move the verdict itself).
func onePair(t *testing.T, mode rigor.Mode, champion, challenger runstore.Record) Result {
	t.Helper()
	return onePairJudged(t, mode, judgedVerdict(WinTie), champion, challenger)
}

// judgedVerdict is a verdict as a judge run writes one (A5.2): the arm it took,
// a tie for the judge that could not tell the two arms apart, and the model
// that answered (D553).
func judgedVerdict(w Winner) Verdict {
	return Verdict{Winner: w, JudgeModel: "judge-model"}
}

// onePairJudged is onePair with the judge's own answer, the zero Verdict for a
// pair no judge has decided.
func onePairJudged(t *testing.T, mode rigor.Mode, v Verdict, champion, challenger runstore.Record) Result {
	t.Helper()
	pairs := Pairs([]runstore.Record{champion, challenger})
	if len(pairs) != 1 {
		t.Fatalf("Pairs over one pair's two arms returned %d pairs, want 1", len(pairs))
	}
	return Score(pairs[0], mode, v)
}

// rounds is a record's senior review rounds: one round per findings count,
// numbered in order, each answering with the same verdict.
func rounds(verdict string, findings ...int) []runstore.ReviewSummary {
	out := make([]runstore.ReviewSummary, 0, len(findings))
	for i, n := range findings {
		out = append(out, runstore.ReviewSummary{Round: i + 1, Tier: runstore.TierSenior, Verdict: verdict, Findings: n})
	}
	return out
}

// tolerated is n tolerated findings as the record keeps them.
func tolerated(n int) *runstore.ToleratedFindings {
	findings := make([]runstore.ToleratedFinding, 0, n)
	for i := 0; i < n; i++ {
		findings = append(findings, runstore.ToleratedFinding{
			Title: "a small thing the mode lets through", Mode: rigor.Pro,
			Likelihood: 1, Impact: 1, FixCost: 1, Class: rigor.Cosmetic,
		})
	}
	return runstore.NewTolerated(findings)
}

// TestHaltIsLoss is D561's first signal: an arm that halted or reached no
// final head loses, whatever the signals below it say, and it is asked first
// in every mode — the per-run cap both arms share is why an arm that halts
// more often is the arm that does not work within what a run may cost (D567).
func TestHaltIsLoss(t *testing.T) {
	t.Run("a halted arm loses a pair it is better on", func(t *testing.T) {
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Verify = nil
		champion.Reviews = rounds(verdictChanges, 3)
		champion.Tolerated = nil

		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		challenger.Halt = &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run"}

		for _, mode := range []rigor.Mode{rigor.POC, rigor.Startup, rigor.Pro, rigor.Critical} {
			if got := onePair(t, mode, champion, challenger).Winner; got != WinChampion {
				t.Errorf("%s: winner = %q, want %q", mode, got, WinChampion)
			}
		}
	})

	t.Run("an arm with no final head loses", func(t *testing.T) {
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Verify = nil
		champion.Reviews = rounds(verdictChanges, 3)

		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		challenger.HeadSHA = ""
		challenger.Verify = nil
		challenger.Reviews = nil

		if got := onePair(t, rigor.Pro, champion, challenger).Winner; got != WinChampion {
			t.Errorf("winner = %q, want %q", got, WinChampion)
		}
	})
}

// TestPOCUsesTestsOnly is D561's per-mode set: a POC pair is decided on the
// halt and the tests and on nothing else, so two arms that both passed their
// tests tie however far apart their reviews are.
func TestPOCUsesTestsOnly(t *testing.T) {
	champion := pairArm("run-champion", "pair-1", task.ABArmChampion)

	challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
	challenger.Reviews = rounds(verdictChanges, 1, 1, 1, 1)
	challenger.Tolerated = nil
	challenger.Stages = []runstore.StageTiming{{Name: fixStage}, {Name: fixStage}}

	t.Run("a POC pair ties where the tests agree", func(t *testing.T) {
		if got := onePair(t, rigor.POC, champion, challenger).Winner; got != WinTie {
			t.Errorf("winner = %q, want %q", got, WinTie)
		}
	})

	t.Run("Startup reads the verdict", func(t *testing.T) {
		if got := onePair(t, rigor.Startup, champion, challenger).Winner; got != WinChampion {
			t.Errorf("winner = %q, want %q", got, WinChampion)
		}
	})

	t.Run("a POC pair is still decided on the tests", func(t *testing.T) {
		unverified := challenger
		unverified.Verify = nil
		if got := onePair(t, rigor.POC, champion, unverified).Winner; got != WinChampion {
			t.Errorf("winner = %q, want %q", got, WinChampion)
		}
	})
}

// TestProUsesUntoleratedFindings is D561's fourth signal: where both arms'
// rounds asked for changes, the arm that ended on fewer findings its mode does
// not tolerate wins at Pro, and Startup — which stops at the verdict — ties.
func TestProUsesUntoleratedFindings(t *testing.T) {
	champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
	champion.Reviews = rounds(verdictChanges, 3)
	champion.Tolerated = tolerated(2)

	challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
	challenger.Reviews = rounds(verdictChanges, 3)
	challenger.Tolerated = nil

	t.Run("Startup stops at the verdict", func(t *testing.T) {
		if got := onePair(t, rigor.Startup, champion, challenger).Winner; got != WinTie {
			t.Errorf("winner = %q, want %q", got, WinTie)
		}
	})

	t.Run("Pro reads the findings the mode does not tolerate", func(t *testing.T) {
		res := onePair(t, rigor.Pro, champion, challenger)
		if res.Winner != WinChampion {
			t.Errorf("winner = %q, want %q", res.Winner, WinChampion)
		}
		if res.Champion.Untolerated != 1 || res.Challenger.Untolerated != 3 {
			t.Errorf("untolerated = %d and %d, want 1 and 3", res.Champion.Untolerated, res.Challenger.Untolerated)
		}
	})

	t.Run("a cheap loop the round followed counts as an iteration", func(t *testing.T) {
		// Both arms ended on one finding and let it through, and their
		// deciding round is the same either way: what tells them apart is the
		// first-line round the champion's rejection bought (Bounce), which
		// D561 counts beside the rounds and the fix stages.
		bounced := pairArm("run-bounced", "pair-3", task.ABArmChampion)
		bounced.Reviews = []runstore.ReviewSummary{
			{Round: 1, Tier: runstore.TierSenior, Verdict: verdictChanges, Findings: 1},
			{Round: 2, Tier: runstore.TierFirst, Verdict: verdictChanges, Findings: 1, Bounce: 1},
		}
		bounced.Tolerated = tolerated(1)

		// The first line ran before the senior round here, which is a round no
		// rejection bought: the same two rounds as the champion's, and one
		// iteration fewer.
		steady := pairArm("run-steady", "pair-3", task.ABArmChallenger)
		steady.Reviews = []runstore.ReviewSummary{
			{Round: 1, Tier: runstore.TierFirst, Verdict: verdictShip},
			{Round: 2, Tier: runstore.TierSenior, Verdict: verdictChanges, Findings: 1},
		}
		steady.Tolerated = tolerated(1)

		if got := onePair(t, rigor.Pro, bounced, steady).Winner; got != WinTie {
			t.Errorf("Pro: winner = %q, want %q", got, WinTie)
		}
		if got := onePair(t, rigor.Critical, bounced, steady).Winner; got != WinChallenger {
			t.Errorf("Critical: winner = %q, want %q", got, WinChallenger)
		}
	})

	t.Run("an Accepted block that outruns Tolerated is not read", func(t *testing.T) {
		// The runner writes the stop's let-through entries into both blocks
		// (toleratedRecord, acceptedRecord), so Accepted never holds one
		// Tolerated does not; a block that outruns it is read at Tolerated's
		// smaller figure rather than making the round look cleaner.
		champion := pairArm("run-champion", "pair-3", task.ABArmChampion)
		champion.Reviews = rounds(verdictChanges, 3)
		champion.Tolerated = tolerated(2)
		champion.Accepted = &runstore.AcceptedFindings{
			Findings: tolerated(3).Findings, Total: 3, Mode: rigor.Pro, Round: 1, At: runstore.StopRoundsSpent,
		}

		res := onePair(t, rigor.Pro, champion, pairArm("run-challenger", "pair-3", task.ABArmChallenger))
		if res.Champion.Untolerated != 1 {
			t.Errorf("untolerated = %d, want 1: Tolerated is the let-through count", res.Champion.Untolerated)
		}
	})

	t.Run("Critical reads the iterations after them", func(t *testing.T) {
		// Both arms ended on one finding and let it through, so Pro's list
		// is exhausted; the run that spent three review rounds where the
		// other spent one is the one Critical ranks below.
		slow := pairArm("run-slow", "pair-2", task.ABArmChampion)
		slow.Reviews = rounds(verdictChanges, 1, 1, 1)
		slow.Tolerated = tolerated(1)
		quick := pairArm("run-quick", "pair-2", task.ABArmChallenger)
		quick.Reviews = rounds(verdictChanges, 1)
		quick.Tolerated = tolerated(1)

		if got := onePair(t, rigor.Pro, slow, quick).Winner; got != WinTie {
			t.Errorf("Pro: winner = %q, want %q", got, WinTie)
		}
		if got := onePair(t, rigor.Critical, slow, quick).Winner; got != WinChallenger {
			t.Errorf("Critical: winner = %q, want %q", got, WinChallenger)
		}
	})
}

// TestTieOnAllSignals is D561's last line: a tie on every used signal is a
// tie, and the pair is still a pair — it is the figure A4.2 counts as a tie
// and not a pair either arm took.
func TestTieOnAllSignals(t *testing.T) {
	champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
	challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)

	for _, mode := range []rigor.Mode{rigor.POC, rigor.Startup, rigor.Pro, rigor.Critical} {
		if got := onePair(t, mode, champion, challenger).Winner; got != WinTie {
			t.Errorf("%s: winner = %q, want %q", mode, got, WinTie)
		}
	}

	t.Run("two arms that both lose tie", func(t *testing.T) {
		halted := pairArm("run-halted", "pair-2", task.ABArmChampion)
		halted.Halt = &runstore.Halt{Reason: runstore.HaltTokenCap, Scope: "run"}
		alsoHalted := pairArm("run-also-halted", "pair-2", task.ABArmChallenger)
		alsoHalted.Halt = &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run"}
		if got := onePair(t, rigor.Critical, halted, alsoHalted).Winner; got != WinTie {
			t.Errorf("winner = %q, want %q", got, WinTie)
		}
	})
}

// TestPairInvalidWhenStartSHADiffers is R2 and D571: the launcher pins one
// commit for both arms, so a pair whose records name different ones — or no
// commit at all, which is an arm whose bootstrap never got that far — is
// dropped rather than scored, and every figure drawn from pairs drops with it.
func TestPairInvalidWhenStartSHADiffers(t *testing.T) {
	champion := pairArm("run-champion", "pair-1", task.ABArmChampion)

	t.Run("two commits", func(t *testing.T) {
		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		challenger.StartSHA = "2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d"
		if got := Pairs([]runstore.Record{champion, challenger}); len(got) != 0 {
			t.Errorf("Pairs returned %d pairs, want none", len(got))
		}
	})

	t.Run("an arm that never started", func(t *testing.T) {
		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		challenger.StartSHA = ""
		if got := Pairs([]runstore.Record{champion, challenger}); len(got) != 0 {
			t.Errorf("Pairs returned %d pairs, want none", len(got))
		}
	})

	t.Run("both arms never started", func(t *testing.T) {
		// Two arms that never started agree on every fact R2 compares — both
		// carry no start commit — so only the guard that reads the absence
		// itself keeps them out of every figure.
		neverStarted := pairArm("run-never-started", "pair-1", task.ABArmChampion)
		neverStarted.StartSHA = ""
		alsoNever := pairArm("run-also-never", "pair-1", task.ABArmChallenger)
		alsoNever.StartSHA = ""

		if got := Pairs([]runstore.Record{neverStarted, alsoNever}); len(got) != 0 {
			t.Errorf("Pairs returned %d pairs, want none", len(got))
		}
	})
}

// TestPairInvalidWhenModeDiffers is the rest of R2: the arms' resolved mode,
// tier and recipe must agree, or the pair is not one the comparison may draw a
// figure from.
func TestPairInvalidWhenModeDiffers(t *testing.T) {
	differing := map[string]func(runstore.Record) runstore.Record{
		"mode": func(r runstore.Record) runstore.Record {
			r.Rigor = &runstore.RigorRecord{Mode: rigor.POC}
			return r
		},
		"tier": func(r runstore.Record) runstore.Record {
			r.Tier = &runstore.TierRecord{Tier: tier.Large}
			return r
		},
		"recipe": func(r runstore.Record) runstore.Record {
			r.Recipe = &runstore.RecipeRecord{Name: "hotfix", SHA256: strings.Repeat("ab", 32)}
			return r
		},
		"the recipe's bytes": func(r runstore.Record) runstore.Record {
			r.Recipe = &runstore.RecipeRecord{Name: pairRecipe, SHA256: strings.Repeat("cd", 32)}
			return r
		},
		"a mode one arm never resolved": func(r runstore.Record) runstore.Record {
			r.Rigor = nil
			return r
		},
	}

	champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
	for name, differ := range differing {
		t.Run(name, func(t *testing.T) {
			challenger := differ(pairArm("run-challenger", "pair-1", task.ABArmChallenger))
			if got := Pairs([]runstore.Record{champion, challenger}); len(got) != 0 {
				t.Errorf("Pairs returned %d pairs, want none", len(got))
			}
		})
	}

	t.Run("one arm alone", func(t *testing.T) {
		if got := Pairs([]runstore.Record{champion}); len(got) != 0 {
			t.Errorf("Pairs returned %d pairs, want none", len(got))
		}
	})

	t.Run("a record that is no arm", func(t *testing.T) {
		unsampled := pairArm("run-unsampled", "pair-1", task.ABArmChallenger)
		unsampled.AB = nil
		strange := pairArm("run-strange", "pair-1", task.ABArmChallenger)
		strange.AB.Arm = task.ABArm("neither")
		if got := Pairs([]runstore.Record{champion, unsampled, strange}); len(got) != 0 {
			t.Errorf("Pairs returned %d pairs, want none", len(got))
		}
	})

	t.Run("two records of one arm", func(t *testing.T) {
		// No launcher writes this, and scoring whichever record came first
		// would make a figure depend on the order the records were read in.
		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		for name, extra := range map[string]runstore.Record{
			"two champions":   pairArm("run-champion-2", "pair-1", task.ABArmChampion),
			"two challengers": pairArm("run-challenger-2", "pair-1", task.ABArmChallenger),
		} {
			t.Run(name, func(t *testing.T) {
				if got := Pairs([]runstore.Record{champion, challenger, extra}); len(got) != 0 {
					t.Errorf("Pairs returned %d pairs, want none", len(got))
				}
			})
		}
	})
}

// TestFollowUpCountIsContextOnly is D562: only the arm that reaches the
// developer can be followed up — the shadow opens no pull request — so the
// kept arm's follow-up count is carried beside the pair as context and decides
// nothing about who took it.
func TestFollowUpCountIsContextOnly(t *testing.T) {
	champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
	challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)

	followUp := func(runID, previous string) runstore.Record {
		return runstore.Record{
			RunID:    runID,
			StartSHA: pairStartSHA,
			AB:       nil,
			FollowUp: &runstore.FollowUp{PR: 12, PreviousRun: previous},
		}
	}

	records := []runstore.Record{
		champion,
		challenger,
		followUp("run-follow-1", "run-champion"),
		followUp("run-follow-2", "run-follow-1"),
		followUp("run-shadow-follow", "run-challenger"),
	}
	pairs := Pairs(records)
	if len(pairs) != 1 {
		t.Fatalf("Pairs returned %d pairs, want 1", len(pairs))
	}
	if pairs[0].ChampionFollowUps != 2 {
		t.Errorf("ChampionFollowUps = %d, want 2", pairs[0].ChampionFollowUps)
	}
	if got := Score(pairs[0], rigor.Critical, judgedVerdict(WinTie)).Winner; got != WinTie {
		t.Errorf("winner = %q, want %q: the follow-up count is context", got, WinTie)
	}
}

// TestPairReadinessFollowsTheLevelThatDecided is D561's third signal read the
// way the runner decides readiness: the last round of the level the run decided
// on, whatever it answered (D723). Readiness is never read backwards to a round
// that advises, and never to a level below the one that decides, so an arm
// whose decider left no round — or left one that answered nothing parseable —
// is not ready.
//
// The level is the highest of the three readings the record allows: the mode's
// own defaults, the rounds the record shows (a round above the default is a
// level a layer turned on, and the round it wrote is the round it decided on),
// and level 3 where the security escalation is on the record (D1508).
func TestPairReadinessFollowsTheLevelThatDecided(t *testing.T) {
	firstLine := []runstore.ReviewSummary{{Round: 1, Tier: runstore.TierFirst, Verdict: verdictShip}}

	t.Run("a deciding round that answered nothing ends the arm unreviewed", func(t *testing.T) {
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Reviews = []runstore.ReviewSummary{
			{Round: 1, Tier: runstore.TierSenior, Verdict: verdictShip},
			{Round: 2, Tier: runstore.TierSenior, Verdict: "none"},
		}

		res := onePair(t, rigor.Startup, champion, pairArm("run-challenger", "pair-1", task.ABArmChallenger))
		if res.Champion.ReviewReady {
			t.Error("champion ready = true, want false: its last deciding round answered nothing")
		}
		if res.Winner != WinChallenger {
			t.Errorf("winner = %q, want %q", res.Winner, WinChallenger)
		}
	})

	t.Run("a first-line round decides under POC", func(t *testing.T) {
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Reviews = firstLine

		res := onePair(t, rigor.POC, champion, pairArm("run-challenger", "pair-1", task.ABArmChallenger))
		if !res.Champion.ReviewReady {
			t.Error("champion ready = false, want true: POC runs level 1 alone")
		}
		if res.Winner != WinTie {
			t.Errorf("winner = %q, want %q: POC reads no verdict", res.Winner, WinTie)
		}
	})

	t.Run("a first-line round does not decide where a level 2 runs", func(t *testing.T) {
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Reviews = firstLine

		res := onePair(t, rigor.Startup, champion, pairArm("run-challenger", "pair-1", task.ABArmChallenger))
		if res.Champion.ReviewReady {
			t.Error("champion ready = true, want false: its level 2 left no round")
		}
		if res.Winner != WinChallenger {
			t.Errorf("winner = %q, want %q", res.Winner, WinChallenger)
		}
	})

	t.Run("a senior round does not decide under Critical", func(t *testing.T) {
		// Critical runs level 3 as well, so its adversarial round is the one
		// readiness is read from: an arm whose senior level shipped and whose
		// adversarial pass never ran is a run Decide leaves a draft, and the
		// arm that ran it is the one that takes the pair.
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)

		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		challenger.Reviews = append(rounds(verdictShip), runstore.ReviewSummary{
			Round: 2, Tier: runstore.TierAdversarial, Verdict: verdictShip,
		})

		res := onePair(t, rigor.Critical, champion, challenger)
		if res.Champion.ReviewReady {
			t.Error("champion ready = true, want false: its adversarial level left no round")
		}
		if !res.Challenger.ReviewReady {
			t.Error("challenger ready = false, want true: its adversarial round shipped")
		}
		if res.Winner != WinChallenger {
			t.Errorf("winner = %q, want %q", res.Winner, WinChallenger)
		}
	})

	t.Run("a round with no tier is the senior level", func(t *testing.T) {
		// The record's own reading of the field: empty is the senior review of
		// a run that had no first line, which is level 2's round.
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Reviews = []runstore.ReviewSummary{{Round: 1, Verdict: verdictShip}}

		res := onePair(t, rigor.Startup, champion, pairArm("run-challenger", "pair-1", task.ABArmChallenger))
		if !res.Champion.ReviewReady {
			t.Error("champion ready = false, want true: an empty tier is level 2's round")
		}
		if res.Winner != WinTie {
			t.Errorf("winner = %q, want %q", res.Winner, WinTie)
		}
	})

	t.Run("a mode outside the vocabulary reads the strictest level", func(t *testing.T) {
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)

		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		challenger.Reviews = append(rounds(verdictShip), runstore.ReviewSummary{
			Round: 2, Tier: runstore.TierAdversarial, Verdict: verdictShip,
		})

		res := onePair(t, rigor.Mode("paranoid"), champion, challenger)
		if res.Champion.ReviewReady {
			t.Error("champion ready = true, want false: level 3 left no round")
		}
		if !res.Challenger.ReviewReady {
			t.Error("challenger ready = false, want true")
		}
	})

	t.Run("the security escalation decides at level 3", func(t *testing.T) {
		// The escalation turns level 3 on for the run and deliberately leaves the
		// rigor mode where it was (D1508), so a Pro run whose diff matched a
		// sensitivity row decided on its adversarial round: the arm whose senior
		// level shipped and whose adversarial pass never ran is the draft the
		// runner keeps, whatever the mode's own defaults say.
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Security = &runstore.SecurityEscalation{At: runstore.SecurityAtImplement, Pattern: "credential paths"}

		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		challenger.Security = champion.Security
		challenger.Reviews = append(rounds(verdictShip), runstore.ReviewSummary{
			Round: 2, Tier: runstore.TierAdversarial, Verdict: verdictShip,
		})

		res := onePair(t, rigor.Pro, champion, challenger)
		if res.Champion.ReviewReady {
			t.Error("champion ready = true, want false: its escalated level 3 left no round")
		}
		if !res.Challenger.ReviewReady {
			t.Error("challenger ready = false, want true: its escalated level 3 shipped")
		}
		if res.Winner != WinChallenger {
			t.Errorf("winner = %q, want %q", res.Winner, WinChallenger)
		}
	})

	t.Run("a refused escalation is not a level anything was decided on", func(t *testing.T) {
		// A refusal means the forced level was not taken and level 3 never ran
		// (D1509): the levels stayed where they were, so the record reads as the
		// run it is — level 2's round decides.
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Security = &runstore.SecurityEscalation{
			At: runstore.SecurityAtFinalize, Pattern: "credential paths", Refused: true,
		}

		res := onePair(t, rigor.Pro, champion, pairArm("run-challenger", "pair-1", task.ABArmChallenger))
		if !res.Champion.ReviewReady {
			t.Error("champion ready = false, want true: a refused escalation left the levels where they were")
		}
		if res.Winner != WinTie {
			t.Errorf("winner = %q, want %q", res.Winner, WinTie)
		}
	})

	t.Run("a level a layer turned on decides on its own round", func(t *testing.T) {
		// The record carries no resolved levels, so a level above the mode's own
		// default is read from the round it wrote: a Pro run the owner's floor
		// raised to level 3 decided there, and an arm whose adversarial round
		// asked for changes is not ready beside one whose round shipped.
		champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
		champion.Reviews = append(rounds(verdictShip), runstore.ReviewSummary{
			Round: 2, Tier: runstore.TierAdversarial, Verdict: verdictChanges,
		})

		challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
		challenger.Reviews = append(rounds(verdictShip), runstore.ReviewSummary{
			Round: 2, Tier: runstore.TierAdversarial, Verdict: verdictShip,
		})

		res := onePair(t, rigor.Pro, champion, challenger)
		if res.Champion.ReviewReady {
			t.Error("champion ready = true, want false: its adversarial round asked for changes")
		}
		if res.Winner != WinChallenger {
			t.Errorf("winner = %q, want %q: the level above the default is the one that decided", res.Winner, WinChallenger)
		}
	})
}

// TestPairVerifiedNeedsACleanTree is D561's second signal read the way the
// runner reads a commit before publishing one: each kind's last run on a clean
// tree at the final head passed, and a test is among them. A head whose only
// test ran on a dirty tree, a head whose build failed last, and a head no test
// ran on are each an arm without the signal — and a run on a dirty tree after a
// clean one is skipped rather than taken as the last word, because a tree the
// agent had left dirty says nothing about the commit.
func TestPairVerifiedNeedsACleanTree(t *testing.T) {
	at := func(kind verify.Kind, clean, passed bool) verify.Record {
		return verify.Record{Kind: kind, HeadSHA: pairHeadSHA, CleanTree: clean, Passed: passed}
	}

	cases := []struct {
		name   string
		verify []verify.Record
		want   bool
	}{
		{"a test that passed on a clean tree", []verify.Record{at(verify.KindTest, true, true)}, true},
		{"a test that passed with the tree dirty", []verify.Record{at(verify.KindTest, false, true)}, false},
		{"a build whose last run at the head failed", []verify.Record{
			at(verify.KindBuild, true, true), at(verify.KindTest, true, true), at(verify.KindBuild, true, false),
		}, false},
		{"build and lint with no test at the head", []verify.Record{
			at(verify.KindBuild, true, true), at(verify.KindLint, true, true),
		}, false},
		{"a test that failed and then passed", []verify.Record{
			at(verify.KindTest, true, false), at(verify.KindTest, true, true),
		}, true},
		{"a test that passed and then failed on a clean tree", []verify.Record{
			at(verify.KindTest, true, true), at(verify.KindTest, true, false),
		}, false},
		{"a dirty run after a clean one at the head", []verify.Record{
			at(verify.KindTest, true, true), at(verify.KindBuild, true, true), at(verify.KindTest, false, true),
		}, true},
		{"a dirty run that failed after a clean pass", []verify.Record{
			at(verify.KindTest, true, true), at(verify.KindTest, false, false),
		}, true},
		{"a test on a head the arm did not end at", []verify.Record{
			{Kind: verify.KindTest, HeadSHA: strings.Repeat("2d", 20), CleanTree: true, Passed: true},
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
			champion.Verify = tc.verify

			res := onePair(t, rigor.POC, champion, pairArm("run-challenger", "pair-1", task.ABArmChallenger))
			if res.Champion.Verified != tc.want {
				t.Errorf("champion verified = %v, want %v", res.Champion.Verified, tc.want)
			}
			want := WinTie
			if !tc.want {
				want = WinChallenger
			}
			if res.Winner != want {
				t.Errorf("winner = %q, want %q", res.Winner, want)
			}
		})
	}
}

// TestCriticalNeedsVerdict is D561's own line: at Critical the hard signals are
// not enough on their own — they miss design quality on work that passes its
// tests — so a pair no judge has decided does not count. It is taken by no arm
// and enters no figure at all, and the count of such pairs is the report's to
// show beside the row (A5.4). The requirement is Critical's: every other mode is
// decided on the signals whether or not a judge has reached the pair.
func TestCriticalNeedsVerdict(t *testing.T) {
	champion := pairArm("run-champion", "pair-1", task.ABArmChampion)

	// The champion's signals take the pair, so a test reading a winner knows
	// whether the signals were read at all.
	challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
	challenger.Halt = &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run"}

	t.Run("an unjudged pair does not count", func(t *testing.T) {
		res := onePairJudged(t, rigor.Critical, Verdict{}, champion, challenger)
		if res.Counted {
			t.Errorf("an unjudged Critical pair counts: %+v", res)
		}
		if res.Winner != "" {
			t.Errorf("winner = %q, want none: a pair that does not count is taken by no arm", res.Winner)
		}
		// The signals are read anyway: the pair is short by a judge and by
		// nothing about the two runs, and a reader sees what it would have been
		// decided on.
		if res.Champion.Halted || !res.Challenger.Halted {
			t.Errorf("halts read as champion %v, challenger %v, want false and true: the signals are read for a pair that does not count", res.Champion.Halted, res.Challenger.Halted)
		}
		if res.Verdict.Judged() {
			t.Errorf("the zero verdict reads as judged: %+v", res.Verdict)
		}
	})

	t.Run("the same pair counts at Pro", func(t *testing.T) {
		res := onePairJudged(t, rigor.Pro, Verdict{}, champion, challenger)
		if !res.Counted || res.Winner != WinChampion {
			t.Errorf("the pair counts as %v and is taken by %q at Pro, want true and %q: the judge is required at Critical and at no other mode (D561)", res.Counted, res.Winner, WinChampion)
		}
	})

	t.Run("a mode outside the vocabulary does not ask for one", func(t *testing.T) {
		res := onePairJudged(t, rigor.Mode("paranoid"), Verdict{}, champion, challenger)
		if !res.Counted {
			t.Error("a pair at a mode the vocabulary does not hold does not count: the requirement is Critical's own (D561), and no other mode's reading asks a judge")
		}
	})

	t.Run("a judged pair counts, tie or not", func(t *testing.T) {
		// A judge that could not tell the arms apart decided the pair: what D561
		// requires is a verdict, and a tie is one. No answer moves the winner,
		// which the signals set.
		for name, v := range map[string]Verdict{
			"a judge that took the challenger":       judgedVerdict(WinChallenger),
			"a judge that took the champion":         judgedVerdict(WinChampion),
			"a judge that could not tell them apart": judgedVerdict(WinTie),
		} {
			t.Run(name, func(t *testing.T) {
				res := onePairJudged(t, rigor.Critical, v, champion, challenger)
				if !res.Counted {
					t.Fatalf("a judged Critical pair does not count: %+v", res)
				}
				if res.Winner != WinChampion {
					t.Errorf("winner = %q, want %q: the verdict is shown beside the signals, which are what rank the pair", res.Winner, WinChampion)
				}
			})
		}
	})

	t.Run("a skipped verdict is no judgement", func(t *testing.T) {
		// A5.2 writes a skip for every pair a judge run left alone: the pair is
		// one no judge has read, whatever the reason it was passed over.
		for _, skip := range []string{skipIdentified, skipTruncated, skipTooLarge, skipUnreadable, skipCallFailed} {
			res := onePairJudged(t, rigor.Critical, Verdict{Skipped: skip, JudgeModel: "judge-model"}, champion, challenger)
			if res.Counted || res.Winner != "" {
				t.Errorf("a pair skipped as %q counts as %q: a skipped pair is one no judge decided", skip, res.Winner)
			}
		}
	})
}

// TestVerdictShownBesideSignals is the other half of D561: the verdict is shown
// beside the hard signals and never weighed with them. The signals rank the
// pair — they are what a mode reads — and the judge's answer is carried on the
// same Result, whole and unchanged, so a reader sees both rather than one folded
// into the other by a weight nobody chose.
func TestVerdictShownBesideSignals(t *testing.T) {
	champion := pairArm("run-champion", "pair-1", task.ABArmChampion)
	challenger := pairArm("run-challenger", "pair-1", task.ABArmChallenger)
	challenger.Halt = &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run"}

	t.Run("a verdict that disagrees is carried as the judge wrote it", func(t *testing.T) {
		v := Verdict{Winner: WinChallenger, Reason: "clarity", JudgeModel: "judge-model"}
		for _, mode := range []rigor.Mode{rigor.POC, rigor.Startup, rigor.Pro, rigor.Critical} {
			res := onePairJudged(t, mode, v, champion, challenger)
			if res.Winner != WinChampion {
				t.Errorf("%s: winner = %q, want %q: the halted arm loses on the signals whatever a judge answered", mode, res.Winner, WinChampion)
			}
			if res.Verdict != v {
				t.Errorf("%s: the verdict reads %+v, want %+v: it is shown beside the signals and never rewritten by them", mode, res.Verdict, v)
			}
		}
	})

	t.Run("a judge that gave no reason is carried that way too", func(t *testing.T) {
		v := judgedVerdict(WinTie)
		if res := onePairJudged(t, rigor.Pro, v, champion, challenger); res.Verdict != v {
			t.Errorf("the verdict reads %+v, want %+v", res.Verdict, v)
		}
	})

	t.Run("a pair no judge decided carries the zero verdict and still counts", func(t *testing.T) {
		res := onePairJudged(t, rigor.Pro, Verdict{}, champion, challenger)
		if res.Verdict != (Verdict{}) || res.Verdict.Judged() {
			t.Errorf("the verdict reads %+v, want nothing: no judge has reached this pair", res.Verdict)
		}
		if !res.Counted || res.Winner != WinChampion {
			t.Errorf("the pair counts as %v and is taken by %q at Pro, want true and %q: outside Critical the signals decide a pair no judge has read (D561)", res.Counted, res.Winner, WinChampion)
		}
	})
}
