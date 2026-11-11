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
func onePair(t *testing.T, mode rigor.Mode, champion, challenger runstore.Record) Result {
	t.Helper()
	pairs := Pairs([]runstore.Record{champion, challenger})
	if len(pairs) != 1 {
		t.Fatalf("Pairs over one pair's two arms returned %d pairs, want 1", len(pairs))
	}
	return Score(pairs[0], mode)
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
	if got := Score(pairs[0], rigor.Critical).Winner; got != WinTie {
		t.Errorf("winner = %q, want %q: the follow-up count is context", got, WinTie)
	}
}
