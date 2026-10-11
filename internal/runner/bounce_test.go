package runner_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// standardBounceRecipe is a custom repo recipe shaped like the design's
// "standard" catalog recipe (docs/design/generic-tool.md §3.5): first_line
// then review: {bounce: first_line}, with no max_rounds of its own, so
// agent.first_line_rounds and agent.review_rounds (the knobs, G6) decide.
const standardBounceRecipe = "version: 1\nname: standard-bounce\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n"

// bounceFiles is the repo file map for standardBounceRecipe.
var bounceFiles = map[string]string{".fugaro/recipes/standard-bounce.yaml": standardBounceRecipe}

// countStage is how many stages named name appear in rec.
func countStage(rec *runstore.Record, name string) int {
	n := 0
	for _, s := range stageNames(rec) {
		if s == name {
			n++
		}
	}
	return n
}

// TestBounceRunsTheCheapLoopAfterARejection: standard with first_line_rounds
// 1 (the default) and review_rounds 2: implement, first-line(changes)+fix,
// senior(changes), fix, first-line(ship), senior(ship). The cheap loop that
// follows the senior rejection is recorded with Bounce 1.
func TestBounceRunsTheCheapLoopAfterARejection(t *testing.T) {
	cfg := firstLineCfg(t, 2, "  first_line_review: on\n  recipe: standard-bounce\n")
	h := newHarnessFiles(t, cfg, nil, bounceFiles)
	rec, err := h.run(t,
		implement("feature"),
		review("changes", 1), // review_first round 1
		fixStep,
		review("changes", 1), // review (senior) round 1
		fixStep,
		review("ship", 0), // review_first, the bounce-back
		review("ship", 0), // review (senior) round 2
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review", "fix", "review_first", "review") {
		t.Fatalf("stages = %v", got)
	}
	if got := tiers(rec); !eq(got, "first:changes", "senior:changes", "first:ship", "senior:ship") {
		t.Fatalf("reviews = %v", got)
	}
	if rs := rec.Reviews; rs[2].Bounce != 1 || rs[2].Tier != runstore.TierFirst {
		t.Fatalf("the bounced first-line review = %+v, want Bounce 1, Tier first", rs[2])
	}
	// Only the bounced first-line review carries a non-zero Bounce.
	for i, r := range rec.Reviews {
		if i != 2 && r.Bounce != 0 {
			t.Errorf("review %d = %+v, want Bounce 0", i, r)
		}
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s, want ready (reason %q)", rec.Outcome, rec.Reason)
	}
}

// TestBounceStopsAtReviewRounds: with review_rounds 2 a second senior
// rejection ends the loop: no third cheap loop, and the PR is a draft.
func TestBounceStopsAtReviewRounds(t *testing.T) {
	cfg := firstLineCfg(t, 2, "  first_line_review: on\n  recipe: standard-bounce\n")
	h := newHarnessFiles(t, cfg, nil, bounceFiles)
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0), // review_first round 1: no findings, no fix
		review("changes", 1),
		fixStep,
		review("ship", 0), // review_first, the bounce-back
		review("changes", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	// The full order, not just the counts: the senior round's own fix runs
	// before the bounce-back first-line round, never after it.
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "fix", "review_first", "review") {
		t.Fatalf("stages = %v", got)
	}
	if n := countStage(rec, "review"); n != 2 {
		t.Fatalf("senior reviews = %d, want 2", n)
	}
	if n := countStage(rec, "review_first"); n != 2 {
		t.Fatalf("first-line reviews = %d, want 2 (one before, one bounce)", n)
	}
	if rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("outcome = %s, want draft (reason %q)", rec.Outcome, rec.Reason)
	}
}

// TestBounceNeverFiresWithoutBounceFlag: a recipe whose review step has no
// bounce still runs the cheap loop once, up front, and never again after a
// senior rejection: the existing (pre-bounce) behaviour is unchanged.
func TestBounceNeverFiresWithoutBounceFlag(t *testing.T) {
	h := newHarness(t, firstLineCfg(t, 2, "  first_line_review: on\n"), nil)
	rec, err := h.run(t, implement("feature"), review("changes", 1), fixStep, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "fix", "review") {
		t.Fatalf("stages = %v (no bounce: the senior review runs once, after the one cheap loop)", got)
	}
	if n := countStage(rec, "review_first"); n != 1 {
		t.Fatalf("first-line reviews = %d, want 1", n)
	}
	for _, r := range rec.Reviews {
		if r.Bounce != 0 {
			t.Errorf("review %+v has a non-zero Bounce with no bounce: first_line step", r)
		}
	}
}
