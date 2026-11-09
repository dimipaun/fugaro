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

// TestStopReadsTheRoundTheRunStoppedAtAndNotTheLevelItNeverReached is item 6b
// at the moment it bites. A run whose first line is on and whose clock crosses
// the threshold inside the cheap loop stops with level 1's round recorded and
// no level 2 round at all — the stop starts no round, so the level the plan
// would have run next holds none by construction (decidingReview) — and the
// round the stop has to read is the one that ran. The pair below is one run
// with level 2 on and the same run with it off, and the ruling's own sentence
// is that asking for a level must not change the mode's rigor (the owner's
// ruling of 2026-11-08): read off the senior round alone, the level-2 run ends
// a draft on findings no field had kept, while the level-2-off run — whose
// round the cheap loop records because it decides there — is ready.
func TestStopReadsTheRoundTheRunStoppedAtAndNotTheLevelItNeverReached(t *testing.T) {
	cfg := func() string {
		return modeCfg(t, "startup", 1, "  first_line_review: on\n  review_stop_fraction: 0.05\n")
	}
	// Above Startup's floor and below the override: a finding the mode buys a
	// fix round for while it has one, and one its stop accepts. It is above the
	// floor so the round is not one the ordinary rule tolerates either — what
	// makes the run ready below is the stop and nothing else.
	blocking := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop in the fetch path", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	})
	spec := func(l2Off bool) *task.Spec {
		s := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
		if l2Off {
			s.ReviewLevels = &config.ReviewLevels{L2: &config.LevelSetting{Off: true}}
		}
		return s
	}
	for _, off := range []bool{true, false} {
		name := "level 1 is the only level, and the stop reads its round"
		if !off {
			name = "level 2 is enabled and never ran, and the stop reads the same round"
		}
		t.Run(name, func(t *testing.T) {
			clk := newClock()
			h := newHarness(t, cfg(), spec(off))
			h.deps.Now = clk.Now
			rec, err := h.run(t, afterStep(clk, implement("feature")), afterStep(clk, blocking))
			if err != nil {
				t.Fatal(err)
			}
			// The one round that ran is the cheap loop's, and the stop cut the
			// loop before the level above it could start.
			if got := stageNames(rec); !eq(got, "implement", "review_first") {
				t.Fatalf("stages = %v, want the cheap round the stop ended the loop on", got)
			}
			if rec.Outcome != runstore.OutcomeReady {
				t.Fatalf("outcome = %s (reason %q), want ready: the stop accepts this round's finding", rec.Outcome, rec.Reason)
			}
			if want := "review round 1 left 1 finding, accepted at startup (time threshold)"; rec.Reason != want {
				t.Errorf("reason = %q, want %q", rec.Reason, want)
			}
			if ac := rec.Accepted; ac == nil || ac.Round != 1 || ac.At != runstore.StopTimeThreshold {
				t.Fatalf("the record's accepted block = %+v, want the cheap loop's round and the half that ended the run", ac)
			}
		})
	}
}

// TestStopFilesTheCheapRoundsAcceptance is the filing half of the ruling (item
// 3) at the one stop both surfaces used to drop: a run whose stop reads the
// cheap loop's round, because the time threshold crossed before the level above
// it could start (firstLineStop, item 6b). The pull request's status line and
// the report's table are the two readers the ruling names, and neither asked
// the stop's own question — the body skipped every first-line round as one that
// decides nothing, and the report compared against the last round an outer
// level left, which here is nil. A run the ruling merges as-is filed nothing at
// either, on the very round the record had already accepted.
//
// Mutation: the body half alone (prflow.go toleratedPart's tier check without
// acceptsAtStop) leaves the line saying "1 finding" and no more, and the report
// half alone (report.go toleratedSection reading seniorReview) drops the
// section; each assertion below fails on its own.
func TestStopFilesTheCheapRoundsAcceptance(t *testing.T) {
	cfg := modeCfg(t, "startup", 1, "  first_line_review: on\n  review_stop_fraction: 0.05\n")
	// Above Startup's floor and below the override, as the 6b test's finding:
	// what makes the run ready is the stop and nothing else.
	blocking := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop in the fetch path", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	})
	clk := newClock()
	s := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
	h := newHarness(t, cfg, s)
	h.deps.Now = clk.Now
	rec, err := h.run(t, afterStep(clk, implement("feature")), afterStep(clk, blocking))
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first") {
		t.Fatalf("stages = %v, want the cheap round the stop ended the loop on", got)
	}
	if ac := rec.Accepted; ac == nil || ac.Round != 1 || ac.At != runstore.StopTimeThreshold {
		t.Fatalf("the record's accepted block = %+v, want the cheap loop's round and the half that ended the run", ac)
	}
	pr := onlyPR(t, h.provider)
	if !strings.Contains(pr.Body, ", accepted at startup (time threshold) (medium)") {
		t.Errorf("the status section does not file the round the stop read as accepted:\n%s", pr.Body)
	}
	raw, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"report.md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"#### Accepted at the stop",
		"The run's review ended at the stop rule's time threshold and the mode startup accepts these findings at the stop: they are left for a follow-up rather than fixed.",
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

// TestStopAtAnOuterLoopsTopReadsTheCheapRoundsRound is the same reading
// (TestStopReadsTheRoundTheRunStoppedAtAndNotTheLevelItNeverReached) at the
// other place the time half is asked. There the stop fired inside the cheap
// loop, which is the one loop that hands its round on; here the clock crosses
// one stage later — during the fix a `changes` round bought — so the stop is
// asked at the top of the level above, the level that never started a round.
// The half that ends the run is the same half either way, and the round the
// stop reads is the one that ran: the reason below is built from level 1's
// round and from the stop's own split of it, which a run read off the level
// above alone cannot say — that policy holds no round at all.
//
// The run is a draft, and for the reason of its own that the fix it bought
// earns it: the commit that ships is not the one that round read, so the stop's
// acceptance — an answer about the round — reaches nothing on it (UnreadFix,
// BF1m). What the pair pins is both halves at once: asking for a level changes
// nothing about the finding the cheap round left, and what nobody read is the
// fix, named by the round that bought it.
func TestStopAtAnOuterLoopsTopReadsTheCheapRoundsRound(t *testing.T) {
	// 0.2 of the fixture's 5m is 60s and every stage advances the harness's
	// clock by 25: the cheap round runs (50s) and the fix it asked for takes
	// the run to 75s, so the stop is asked at the top of level 2's loop — the
	// level the plan would run next and which starts no round. The level-2-off
	// run asks it where the cheap loop's own stop sites are instead.
	cfg := func() string {
		return modeCfg(t, "startup", 1, "  first_line_review: on\n  review_stop_fraction: 0.2\n")
	}
	// The same finding as the pair above: above Startup's floor, so the run is
	// a draft at every other moment and the stop's acceptance is the only
	// thing that makes it ready.
	blocking := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop in the fetch path", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	})
	spec := func(l2Off bool) *task.Spec {
		s := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
		if l2Off {
			s.ReviewLevels = &config.ReviewLevels{L2: &config.LevelSetting{Off: true}}
		}
		return s
	}
	for _, off := range []bool{true, false} {
		name := "level 1 is the only level, and the cheap loop asks the stop itself"
		if !off {
			name = "level 2 is enabled and never started, and the stop reads the same round"
		}
		t.Run(name, func(t *testing.T) {
			clk := newClock()
			h := newHarness(t, cfg(), spec(off))
			h.deps.Now = clk.Now
			rec, err := h.run(t,
				afterStep(clk, implement("feature")),
				afterStep(clk, blocking), // review_first: changes, so a fix is bought
				afterStep(clk, fixStep),  // the fix, and the clock crosses during it
			)
			if err != nil {
				t.Fatal(err)
			}
			// One round ran, and no round of the level above it did: the level
			// with no round is what the stop has to read past.
			if got := stageNames(rec); !eq(got, "implement", "review_first", "fix") {
				t.Fatalf("stages = %v, want the cheap round and its fix and no outer round", got)
			}
			if rec.Outcome != runstore.OutcomeDraft {
				t.Fatalf("outcome = %s (reason %q), want a draft: the fix this round bought is on the branch and no review round read it", rec.Outcome, rec.Reason)
			}
			if want := "the fix after review round 1 was not reviewed"; rec.Reason != want {
				t.Errorf("reason = %q, want %q: the round whose fix nobody read is the cheap loop's, which is the round the stop read", rec.Reason, want)
			}
			// The mismatch itself, asserted rather than assumed: the round the
			// record's last entry read is not the commit the run publishes, and
			// that is the whole of what keeps this run a draft.
			if last := rec.Reviews[len(rec.Reviews)-1]; last.Head == "" || last.Head == rec.HeadSHA {
				t.Fatalf("the record's last round read %q and the run shipped %q, want two commits: this is the case the rule is for", last.Head, rec.HeadSHA)
			}
			if ac := rec.Accepted; ac != nil {
				t.Errorf("the record's accepted block = %+v, want none: the stop accepts a round, and this round did not read what ships", ac)
			}
			if pr := onlyPR(t, h.provider); !pr.Draft {
				t.Errorf("the pull request is not a draft: %+v", pr)
			}
		})
	}
}

// TestStopLetsTheStopThroughOnlyWhereAReviewReadTheCommit is the pair BF1m's
// rule is read by: the same run twice, with the cheap loop given one round and
// two.
//
// One round, and the fix that round's `changes` verdict bought is the last thing
// that touched the branch: no review round read it, so the stop's acceptance —
// an answer about the round, and the round is about the commit before the fix —
// reaches nothing, and the pull request ships as a draft naming the round.
// Two, and round 2 reviews that fix before the stop is asked: the commit that
// ships is the one round 2 read, the acceptance is an answer about it, and the
// run merges as the ruling of 2026-11-08 has it merge. The distinction is what
// keeps the rule from reading every stopped run as a draft: it is the commit
// that shipped that has to have been read, and not the round the levels happen
// to read readiness off.
func TestStopLetsTheStopThroughOnlyWhereAReviewReadTheCommit(t *testing.T) {
	// Above Startup's floor and below its override: a finding the mode's own row
	// blocks on and its stop accepts, so the stop's acceptance is the only thing
	// that can make either run ready.
	blocking := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop in the fetch path", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	})
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}

	// 0.2 of the fixture's 5m is 60s and a stage advances the harness's clock by
	// 25: the cheap round runs (50s) and its fix takes the run to 75s, so the
	// stop is asked at the top of level 2's loop, past the fix and before any
	// round of that level.
	t.Run("the fix nobody read keeps the draft", func(t *testing.T) {
		clk := newClock()
		h := newHarness(t, modeCfg(t, "startup", 1, "  first_line_review: on\n  review_stop_fraction: 0.2\n"), spec)
		h.deps.Now = clk.Now
		rec, err := h.run(t,
			afterStep(clk, implement("feature")),
			afterStep(clk, blocking), // review_first: changes, so a fix is bought
			afterStep(clk, fixStep),  // the fix, and the clock crosses during it
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review_first", "fix") {
			t.Fatalf("stages = %v, want the cheap round, its fix and no review of it", got)
		}
		if rec.Outcome != runstore.OutcomeDraft {
			t.Fatalf("outcome = %s (reason %q), want a draft", rec.Outcome, rec.Reason)
		}
		if want := "the fix after review round 1 was not reviewed"; rec.Reason != want {
			t.Errorf("reason = %q, want %q", rec.Reason, want)
		}
		if ac := rec.Accepted; ac != nil {
			t.Errorf("the record's accepted block = %+v, want none", ac)
		}
		if pr := onlyPR(t, h.provider); !pr.Draft {
			t.Errorf("the pull request is not a draft: %+v", pr)
		}
	})

	// The same run with the cheap loop given a second round: it reviews the fix
	// (100s), and 0.3 of the budget is 90s, so the stop is asked before the fix
	// round 2 would have bought. The round the stop reads is round 2, and the
	// commit it read is the one the run publishes.
	t.Run("a round that read the fix lets the stop through", func(t *testing.T) {
		clk := newClock()
		h := newHarness(t, modeCfg(t, "startup", 1, "  first_line_review: on\n  first_line_rounds: 2\n  review_stop_fraction: 0.3\n"), spec)
		h.deps.Now = clk.Now
		rec, err := h.run(t,
			afterStep(clk, implement("feature")),
			afterStep(clk, blocking), // review_first, round 1: changes, so a fix is bought
			afterStep(clk, fixStep),  // the fix round 2 reviews
			afterStep(clk, blocking), // review_first, round 2: changes, and the stop is asked before its fix
		)
		if err != nil {
			t.Fatal(err)
		}
		if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review_first") {
			t.Fatalf("stages = %v, want the round that read the fix and no fix after it", got)
		}
		if last := rec.Reviews[len(rec.Reviews)-1]; last.Head != rec.HeadSHA {
			t.Fatalf("the record's last round read %q and the run shipped %q, want the same commit", last.Head, rec.HeadSHA)
		}
		if rec.Outcome != runstore.OutcomeReady {
			t.Fatalf("outcome = %s (reason %q), want ready: a review round read the commit that ships", rec.Outcome, rec.Reason)
		}
		if want := "review round 2 left 1 finding, accepted at startup (time threshold)"; rec.Reason != want {
			t.Errorf("reason = %q, want %q", rec.Reason, want)
		}
		if ac := rec.Accepted; ac == nil || ac.Round != 2 || ac.At != runstore.StopTimeThreshold || ac.Total != 1 {
			t.Fatalf("the record's accepted block = %+v, want the round that read the fix", ac)
		}
		if pr := onlyPR(t, h.provider); pr.Draft {
			t.Errorf("the pull request is a draft: %+v", pr)
		}
	})
}

// TestStopInABouncedCheapLoopFilesTheDecidingRoundsAcceptance is the pull
// request's own reader of the round the policy was built from. After a bounce
// the record's last rounds are the cheap loop's, while the round readiness read
// — and the split the accepted block is written from — is the outer round's
// behind them; the levels number their rounds from 1 of their own, so the two
// can carry the same number while being different rounds. reviewsPart builds
// its line from the record alone, so a line keyed on the record's last entry
// asks toleratedPart about a split the round it prints was never judged by:
// with the counts differing the acceptance is dropped from the body entirely,
// and where the counts coincide it is filed as the cheap round's.
//
// The record and the report are the two readers that were right, and both are
// asserted here beside the body, so the three agree on which round the run
// accepted.
func TestStopInABouncedCheapLoopFilesTheDecidingRoundsAcceptance(t *testing.T) {
	// 0.4 of the fixture's 5m is 120s and every stage advances the clock by 25:
	// level 2's round runs at 75s and its fix takes the run to 100s, still
	// under the threshold, so the bounce's cheap round runs — and the stop is
	// asked after it, at 125s. The round the run therefore ends on is the
	// bounced cheap one, and the round readiness read is level 2's round 1.
	cfg := modeCfg(t, "startup", 2, "  first_line_review: on\n  recipe: standard-bounce\n  review_stop_fraction: 0.4\n")
	finding := map[string]any{
		"summary": "unbounded retry loop in the fetch path", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	}
	clk := newClock()
	h := newHarnessFiles(t, cfg, nil, bounceFiles)
	h.deps.Now = clk.Now
	rec, err := h.run(t,
		afterStep(clk, implement("feature")),
		afterStep(clk, review("ship", 0)),                         // review_first: ships, so level 2 reviews the head
		afterStep(clk, reviewScored("changes", finding)),          // review: level 2's round 1, the deciding round
		afterStep(clk, fixStep),                                   // the fix it asked for
		afterStep(clk, reviewScored("changes", finding, finding)), // review_first, the bounce: the stop is asked after it
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "fix", "review_first") {
		t.Fatalf("stages = %v, want the bounced cheap round to be the round the stop ended on", got)
	}
	// The round the stop read is level 2's — the cheap round is the one the
	// stop was asked after, not the one it read.
	if ac := rec.Accepted; ac == nil || ac.Round != 1 || ac.At != runstore.StopTimeThreshold || ac.Total != 1 {
		t.Fatalf("the record's accepted block = %+v, want level 2's round and the half that ended the run", ac)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s (reason %q), want ready", rec.Outcome, rec.Reason)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft {
		t.Errorf("the pull request is a draft: %+v", pr)
	}
	// The line is level 2's round and carries the acceptance; the cheap round,
	// which left two findings, is the round the stop was asked after and not
	// the round the policy was built from.
	if want := "senior review round 1: 1 finding, accepted at startup (time threshold) (medium)"; !strings.Contains(pr.Body, want) {
		t.Errorf("the status section does not file the stop's round as accepted:\n%s", pr.Body)
	}
	if bad := "2 findings, accepted at"; strings.Contains(pr.Body, bad) {
		t.Errorf("the status section files the acceptance on the round the stop was asked after, which the policy was not built from:\n%s", pr.Body)
	}
	raw, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"report.md")
	if err != nil {
		t.Fatal(err)
	}
	// The report's own readers of the same record: the round it names is level
	// 2's one finding, the accepted row is the same finding, and the bounced
	// cheap round stands beside both under its own count.
	got := string(raw)
	for _, want := range []string{
		"senior round 1: changes (1 finding)",
		"first round 1: changes (2 findings)",
		"#### Accepted at the stop",
		"| medium | 3 | 3 | 2 | correctness | startup | `unbounded retry loop in the fetch path` |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the run's report lacks %q:\n%s", want, got)
		}
	}
}

// TestStopWithdrawsItsAcceptanceForALevelTheStopDoesNotTake is the stop's one
// exception at the two readers that are not Decide (the ruling's "every
// remaining finding that is not catastrophic", item 1): the round readiness
// reads is the highest enabled level's, so an enabled level under it whose
// round the stop does not take — here level 2's catastrophic finding, which
// Startup's stop row keeps blocking — is a draft, and the acceptance the mode
// would otherwise have made is one the run never made. Decide itself refuses
// above the acceptance, so the two surfaces this test asserts are the ones a
// reader sees instead: the record's accepted block, which the report and the
// pull request's own status line are built from, and the body they are written
// into. A run that filed the acceptance anyway would print a merge the record
// has no block for and the mode did not make.
func TestStopWithdrawsItsAcceptanceForALevelTheStopDoesNotTake(t *testing.T) {
	// Level 3 is asked for by the task, which is how a run below Critical gets
	// the highest level it enables (item 6).
	spec := &task.Spec{
		Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature",
		ReviewLevels: &config.ReviewLevels{L3: &config.LevelSetting{Model: "adversarial-model"}},
	}
	// One adversarial round, so the round it leaves is the last the level has
	// and the stop ends the run on it rather than on a fix it would have bought.
	cfg := modeCfg(t, "startup", 1, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 1\n")
	// Level 2's round leaves the severity override, which Startup's stop row
	// keeps blocking whatever the axes say; level 3's leaves a finding above the
	// mode's floor — one the mode buys a fix round for and its stop accepts,
	// which is the only shape that reaches the stop's own split at all
	// (findingsPolicyOf). The two rounds differ only in the level they came
	// from, which is what the stop's exception is about.
	catastrophic := reviewScored("changes", map[string]any{
		"summary": "the fetch path drops the error", "likelihood": 1, "impact": 5, "fix_cost": 1, "class": "cosmetic",
	})
	blocking := reviewScored("changes", map[string]any{
		"summary": "unbounded retry loop", "likelihood": 3, "impact": 3, "fix_cost": 2, "class": "correctness",
	})
	h := newHarnessFiles(t, cfg, spec, levelsFiles)
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0), // review_first: level 1, which advises and never decides
		catastrophic,      // review: level 2's round, the level the stop does not take
		blocking,          // adversarial: level 3's round, which the stop accepts
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := tiers(rec); !eq(got, "first:ship", "senior:changes", "adversarial:changes") {
		t.Fatalf("reviews = %v, want all three levels to have run and level 3 to decide", got)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %s (reason %q), want a draft: level 2 left a finding the stop does not take", rec.Outcome, rec.Reason)
	}
	if !strings.Contains(rec.Reason, "review level 2 left a finding the stop does not accept") {
		t.Errorf("reason = %q, want it to name level 2's round as the reason the run is a draft", rec.Reason)
	}
	if ac := rec.Accepted; ac != nil {
		t.Errorf("the record's accepted block = %+v, want none: Decide refused the acceptance, and a record that filed one would report a merge the run did not make", ac)
	}
	pr := onlyPR(t, h.provider)
	if !pr.Draft {
		t.Errorf("the pull request is not a draft: %+v", pr)
	}
	if strings.Contains(pr.Body, "accepted at") {
		t.Errorf("the status section files an acceptance the run never made:\n%s", pr.Body)
	}
	raw, err := h.bucket.ReadAll(context.Background(), h.store.Prefix()+"report.md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, "#### Accepted at the stop") {
		t.Errorf("the report files an acceptance the run never made:\n%s", got)
	}
	if !strings.Contains(got, "first round 1: ship") || !strings.Contains(got, "senior round 1: changes (1 finding)") {
		t.Errorf("the report does not carry the rounds the case is about:\n%s", got)
	}
}
