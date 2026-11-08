package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestStopTimeThresholdEndsTheReviewBeforeAFix is the stop's time half read
// through the loop and not only through Decide (the owner's ruling of
// 2026-11-08, item 2): the stop is the earlier of the mode's rounds being spent
// and the configured share of the run's time budget, and the loop asks it
// between rounds — a `changes` round whose rounds are not spent is followed by
// the fix it asked for, unless the clock is already past the threshold, and then
// the run finalizes on the round it stopped at.
//
// The clock is the harness's own — each stage advances it by 25 seconds — and
// the fraction here is 0.05 of the fixture's 5m, so the implement stage alone
// puts the run past the threshold and the review round after it still runs:
// a run buys one round whatever the clock says (stopNow's guard), and the stop
// is what ends the loop at the round after that. The finding is one Startup's
// own row blocks on (risk 9, below the override), so this round is a draft at
// every other moment: what makes the run ready is the mode's stop row and
// nothing about the finding.
func TestStopTimeThresholdEndsTheReviewBeforeAFix(t *testing.T) {
	clk := newClock()
	h := newHarness(t, modeCfg(t, "startup", 2, "  review_stop_fraction: 0.05\n"), nil)
	h.deps.Now = clk.Now
	blocking := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop in the fetch path", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	})
	rec, err := h.run(t, afterStep(clk, implement("feature")), afterStep(clk, blocking))
	if err != nil {
		t.Fatal(err)
	}
	if n := countStage(rec, "review"); n != 1 {
		t.Fatalf("review rounds = %d, want 1: the run buys one round past the threshold and no more", n)
	}
	if n := countStage(rec, "fix"); n != 0 {
		t.Fatalf("fix rounds = %d, want none: past the stop the loop starts no round, and the fix the round asked for is one", n)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s (reason %q), want ready: the run stopped, and startup accepts what its stop left", rec.Outcome, rec.Reason)
	}
	if want := "review round 1 left 1 finding, accepted at startup (time threshold)"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	ac := rec.Accepted
	if ac == nil || ac.Mode != rigor.Startup || ac.Round != 1 || ac.At != runstore.StopTimeThreshold || ac.Total != 1 || len(ac.Findings) != 1 {
		t.Fatalf("the record's accepted findings = %+v, want the round the time threshold cut", ac)
	}
	if got := ac.Findings[0]; got.Title != "unbounded retry loop in the fetch path" || got.Label != rigor.Medium {
		t.Errorf("accepted finding = %+v, want the round's own finding with the label its score reads", got)
	}
	// The tolerated block is written beside it, because it is the same split:
	// a reader that only reads Tolerated sees the finding the run did not fix,
	// and the new block says why it was not fixed (acceptedRecord).
	if rec.Tolerated == nil || rec.Tolerated.Total != 1 {
		t.Errorf("the record's tolerated findings = %+v, want the same round", rec.Tolerated)
	}
}

// TestStopStartupWithLevel3IsNoStricterThanWithoutIt is item 6a of the ruling:
// asking for level 3 must not change the mode's rigor, so a level-2 round whose
// every finding the mode takes counts as shipped and a level-2 round that left a
// finding the mode blocks on is the reason — whichever level it is, and read
// through the same findings policy the deciding round is read through.
//
// Without this the run below is a draft twice over: level 3 shipped, and
// heldBackLevel read level 2's round against the mode's own rule as if the round
// had been a level 2 run's last (D727) — which for Startup is a round the mode
// would have merged as-is once its rounds were gone.
func TestStopStartupWithLevel3IsNoStricterThanWithoutIt(t *testing.T) {
	// Level 3 is asked for by the task, which every level below reads as a
	// request for its own rounds and never as a change of mode (item 6).
	spec := func() *task.Spec {
		return &task.Spec{
			Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature",
			ReviewLevels: &config.ReviewLevels{L3: &config.LevelSetting{Model: "adversarial-model"}},
		}
	}
	cfg := func() string {
		return modeCfg(t, "startup", 1, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 2\n")
	}
	// Startup's floor is 8 and its stop accepts everything the override does
	// not cover: this finding below the floor is tolerated in a round and this
	// one above it is what the mode buys rounds for.
	tolerated := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop", "likelihood": 2, "impact": 2, "fix_cost": 1, "class": "correctness",
	})
	blocking := reviewScored("changes", map[string]any{
		"summary": "no bound on the retry loop", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	})

	t.Run("a level 2 round the mode takes counts as shipped", func(t *testing.T) {
		h := newHarnessFiles(t, cfg(), spec(), levelsFiles)
		rec, err := h.run(t,
			implement("feature"),
			review("ship", 0), // review_first: level 1
			tolerated,         // review: level 2's round, the level the mode does not block on
			review("ship", 0), // adversarial: level 3 ships, and it is the deciding level
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := tiers(rec); !eq(got, "first:ship", "senior:changes", "adversarial:ship") {
			t.Fatalf("reviews = %v, want all three levels to have run", got)
		}
		if rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
			t.Fatalf("outcome = %s (reason %q), want ready: level 3 shipped and level 2's round left nothing the mode blocks on", rec.Outcome, rec.Reason)
		}
	})
	t.Run("a level 2 round the mode blocks on is the reason", func(t *testing.T) {
		h := newHarnessFiles(t, cfg(), spec(), levelsFiles)
		rec, err := h.run(t,
			implement("feature"),
			review("ship", 0), // review_first
			blocking,          // review: level 2's round, above the mode's floor
			review("ship", 0), // adversarial: level 3 ships
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := tiers(rec); !eq(got, "first:ship", "senior:changes", "adversarial:ship") {
			t.Fatalf("reviews = %v, want all three levels to have run", got)
		}
		if rec.Outcome != runstore.OutcomeDraft {
			t.Fatalf("outcome = %s, want draft: level 3 shipped, and level 2 left a finding the mode blocks on", rec.Outcome)
		}
		for _, want := range []string{"review level 2 last shipped nothing", "round 1 left changes", "until every enabled review level ships"} {
			if !strings.Contains(rec.Reason, want) {
				t.Errorf("reason = %q, want it to name the level and say %q", rec.Reason, want)
			}
		}
	})
	t.Run("pro reads the two rounds through its own row", func(t *testing.T) {
		// The rule is the mode's own and not Startup's: Pro blocks from a risk
		// of 4 where Startup's floor is 8, so the round below is one Pro buys a
		// fix round for and Startup would have let through — and it is the
		// ready answer below that shows which row read it.
		proCfg := func() string {
			return modeCfg(t, "pro", 1, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 2\n")
		}
		proTolerated := reviewScored("changes", map[string]any{
			"summary": "noise in the log line", "likelihood": 1, "impact": 3, "fix_cost": 1, "class": "cosmetic",
		})
		proBlocking := reviewScored("changes", map[string]any{
			"summary": "noise in the log line", "likelihood": 1, "impact": 5, "fix_cost": 1, "class": "cosmetic",
		})
		for _, tc := range []struct {
			name  string
			round step
			ready bool
		}{
			{"a level 2 round Pro takes counts as shipped", proTolerated, true},
			{"a level 2 round Pro blocks on is the reason", proBlocking, false},
		} {
			h := newHarnessFiles(t, proCfg(), spec(), levelsFiles)
			rec, err := h.run(t,
				implement("feature"),
				review("ship", 0), // review_first
				tc.round,          // review: level 2's round, read through Pro's row
				review("ship", 0), // adversarial
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := tiers(rec); !eq(got, "first:ship", "senior:changes", "adversarial:ship") {
				t.Fatalf("%s: reviews = %v, want all three levels to have run", tc.name, got)
			}
			if ready := rec.Outcome == runstore.OutcomeReady; ready != tc.ready {
				t.Fatalf("%s: outcome = %s (reason %q), want ready %v", tc.name, rec.Outcome, rec.Reason, tc.ready)
			}
		}
	})
}

// TestStopAcceptanceIsFiledOnThePullRequestAndTheReport is the filing half of
// the ruling (item 3) at a real run's own output: the findings a stop accepted
// are filed exactly as a tolerated round's are — the pull request's status line
// and the report's table, each with the mode and the finding's own score — under
// the words that say which of the two they are. The record's accepted block is
// what both readers key on, so a run that stopped is never read as a run whose
// review waved its findings through.
func TestStopAcceptanceIsFiledOnThePullRequestAndTheReport(t *testing.T) {
	h := newHarness(t, modeCfg(t, "startup", 1, ""), nil)
	// Above Startup's floor and below the override: a finding the mode buys a
	// fix round for while it has one, and one its stop still accepts. The round
	// is the run's last, so no fix is bought and finalize decides on what this
	// round left.
	blocking := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop in the fetch path", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	})
	rec, err := h.run(t, implement("feature"), blocking)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s (reason %q), want ready: startup's stop accepts this finding", rec.Outcome, rec.Reason)
	}
	if want := "review round 1 left 1 finding, accepted at startup (rounds spent)"; rec.Reason != want {
		t.Errorf("reason = %q, want %q", rec.Reason, want)
	}
	if ac := rec.Accepted; ac == nil || ac.Mode != rigor.Startup || ac.Round != 1 || ac.At != runstore.StopRoundsSpent {
		t.Fatalf("the record's accepted block = %+v, want the round the stop ended on and the half that ended it", ac)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft {
		t.Errorf("the pull request is a draft: %+v", pr)
	}
	// The status line is the tolerated line's own words with the stop's in
	// place of the tolerance's: the mode, the half, and the labels the round's
	// findings scored.
	if !strings.Contains(pr.Body, ", accepted at startup (rounds spent) (medium)") {
		t.Errorf("the status section does not file the stop's round as accepted:\n%s", pr.Body)
	}
	raw, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"report.md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"#### Accepted at the stop",
		"The run's review ended at the stop rule's rounds spent and the mode startup accepts these findings at the stop: they are left for a follow-up rather than fixed.",
		"| medium | 3 | 3 | 2 | correctness | startup | `unbounded retry loop in the fetch path` |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the run's report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "#### Tolerated") {
		t.Errorf("the run's report files the stop's findings under the tolerance heading, which says a review let them go:\n%s", got)
	}
}
