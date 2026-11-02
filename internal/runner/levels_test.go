package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// levelsRecipe is a repository recipe shaped like the design's "standard"
// catalog recipe with level 3's step after it (docs/review-levels-ab-jobmax
// L4.2): the first line, the senior review that bounces back to it, and the
// adversarial step, which bounces the same way.
const levelsRecipe = "version: 1\nname: levels\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - adversarial: { bounce: first_line }\n"

// levelsFiles is the repository file map for levelsRecipe.
var levelsFiles = map[string]string{".fugaro/recipes/levels.yaml": levelsRecipe}

// criticalCfg is the fixture config with extra agent lines and the
// repository's mode at Critical, which is the one mode whose row runs level 3
// (internal/rigor's defaultLevels).
func criticalCfg(t *testing.T, rounds int, extra string) string {
	t.Helper()
	cfg := firstLineCfg(t, rounds, extra)
	out := strings.Replace(cfg, "agent:\n", "rigor:\n  default: critical\nagent:\n", 1)
	if out == cfg {
		t.Fatal("the fixture has no agent: block to put rigor: before, so the repository would not opt in")
	}
	return out
}

// TestLevelsL3BounceSkipsL2: an L3 rejection runs the fix, then the L1 loop,
// then L3 again — never L2 (D727). The senior review shipped the head it read;
// asking it again about a head it never saw would be a second review of the
// change level 3 already rejected, and the level that decides readiness is
// level 3 from then on.
func TestLevelsL3BounceSkipsL2(t *testing.T) {
	cfg := criticalCfg(t, 2, "  first_line_review: on\n  recipe: levels\n  adversarial_rounds: 2\n")
	h := newHarnessFiles(t, cfg, nil, levelsFiles)
	rec, err := h.run(t,
		implement("feature"),
		review("ship", 0),    // review_first
		review("ship", 0),    // review (senior)
		review("changes", 1), // adversarial round 1
		fixStep,
		review("ship", 0), // review_first, the bounce-back
		review("ship", 0), // adversarial round 2
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := stageNames(rec); !eq(got, "implement", "review_first", "review", "adversarial", "fix", "review_first", "adversarial") {
		t.Fatalf("stages = %v", got)
	}
	if n := countStage(rec, "review"); n != 1 {
		t.Fatalf("senior reviews = %d, want 1: a level 3 rejection never goes back to level 2 (D727)", n)
	}
	if n := countStage(rec, "adversarial"); n != 2 {
		t.Fatalf("adversarial reviews = %d, want 2", n)
	}
	if got := tiers(rec); !eq(got, "first:ship", "senior:ship", "adversarial:changes", "first:ship", "adversarial:ship") {
		t.Fatalf("reviews = %v", got)
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("outcome = %s, want ready (reason %q): every enabled level's last verdict is ship", rec.Outcome, rec.Reason)
	}
}
