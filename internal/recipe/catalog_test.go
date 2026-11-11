package recipe

import (
	"slices"
	"testing"
)

// TestCatalogIsTheStarterSet: the catalog holds exactly the spec's starter
// set (design generic-tool.md §3.5) plus the two the escalation plan adds
// (E5), each one parses clean, every recipe but default has a use_when, and
// no recipe pins max_rounds outside mode: review (a catalog recipe takes the
// agent.* knobs, G6) other than the one-round cap mode: review itself
// requires, a digest recipe's own single review round, which is the report
// run's shape rather than a knob, or escalate's single round, which is the
// escalation's own shape: one pass and the verdict, with no round after it.
func TestCatalogIsTheStarterSet(t *testing.T) {
	want := []string{"default", "escalate", "lint-fix", "pick", "premium", "rebase", "review-only", "run-digest", "solo", "standard", "test-and-fix"}
	if got := CatalogNames(); !slices.Equal(got, want) {
		t.Fatalf("catalog = %v", got)
	}
	onePass := map[string]bool{"escalate": true}
	for _, n := range want {
		data, _ := CatalogText(n)
		r, ps := Parse(data)
		if len(ps) != 0 {
			t.Fatalf("%s: %s", n, ProblemsText(ps))
		}
		if n != DefaultName && r.UseWhen == "" {
			t.Errorf("%s has no use_when", n)
		}
		digest := len(r.Steps) > 0 && r.Steps[0].Kind == StepDigest
		for _, s := range r.Steps {
			if s.MaxRounds != 0 && r.Mode != ModeReview && !digest && !onePass[n] {
				t.Errorf("%s pins max_rounds: catalog recipes take the knobs (G6)", n)
			}
		}
	}
	if _, ok := CatalogText("nope"); ok {
		t.Fatal("an unknown name has text")
	}
	if _, ok := CatalogText("../catalog/default"); ok {
		t.Fatal("a path is a catalog name")
	}
}

// TestRecipeEscalateOnePass: the catalog's escalate is one coder pass and
// one review, and the review hands the run over (E5): its findings are
// recorded, never fixed, whatever the agent.* knobs say. The step's own
// max_rounds is 1 and hand_over is what holds the bound there, so a run of it
// costs two model stages — the implement stage and the review — at every
// knob, and not the loop's 1 + n*(2 + ...) a review step otherwise buys.
func TestRecipeEscalateOnePass(t *testing.T) {
	data, ok := CatalogText("escalate")
	if !ok {
		t.Fatal("escalate is not in the catalog")
	}
	r, ps := Parse(data)
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if !UsesHandOver(r) {
		t.Fatal("escalate's review step does not hand the run over")
	}
	if len(r.Steps) != 1 || r.Steps[0].Kind != StepReview || r.Steps[0].MaxRounds != 1 || !r.Steps[0].HandOver {
		t.Fatalf("steps = %+v, want one review step with max_rounds 1 and hand_over", r.Steps)
	}
	// The knobs cannot buy a fix round: the step's own max_rounds is 1, and
	// even a step that wrote none would not, since hand_over refuses the
	// round after the review.
	if got := StageBound(r, 3, 10, n3Default); got != 2 {
		t.Fatalf("escalate bound = %d, want 2 (the implement stage and the one review)", got)
	}
	// hand_over removes the fix, not the rounds: a review step that writes
	// no max_rounds still runs the knobs' number of reviews, each of them
	// alone (the recipe a run wants here is the catalog's, which pins 1).
	loose, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - review: { hand_over: true }\n"))
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if got := StageBound(loose, 3, 10, n3Default); got != 11 {
		t.Fatalf("hand_over bound = %d, want 11 (1 + 10 bare reviews at the knobs' largest values)", got)
	}
}

// TestRecipeRebaseStagesOrder: the catalog's rebase is the rebase step
// alone, and the parser keeps it that way (E5): the step is the whole run —
// the branch is put on its base, the conflicts are the one stage, the tests
// are run again and the branch is pushed — so it is not a step of the loop
// and needs no review step after it. Every recipe that puts it beside
// another step, or in another mode, is refused.
func TestRecipeRebaseStagesOrder(t *testing.T) {
	data, ok := CatalogText("rebase")
	if !ok {
		t.Fatal("rebase is not in the catalog")
	}
	r, ps := Parse(data)
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if !UsesRebase(r) || len(r.Steps) != 1 || r.Steps[0].Kind != StepRebase {
		t.Fatalf("steps = %+v, want the rebase step alone", r.Steps)
	}
	// One model stage at most: the conflicts, and nothing else.
	if got := StageBound(r, 3, 10, n3Default); got != 1 {
		t.Fatalf("rebase bound = %d, want 1", got)
	}
	for name, src := range map[string]string{
		"with a review after it": "version: 1\nname: x\nsteps:\n  - rebase: {}\n  - review: {}\n",
		"with an implement loop": "version: 1\nname: x\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - rebase: {}\n",
		"twice":                  "version: 1\nname: x\nsteps:\n  - rebase: {}\n  - rebase: {}\n",
		"in mode: review":        "version: 1\nname: x\nmode: review\nsteps:\n  - rebase: {}\n",
		"in mode: artifact":      "version: 1\nname: x\nmode: artifact\noutputs:\n  globs: [\"out/*.png\"]\nsteps:\n  - rebase: {}\n",
		"with options":           "version: 1\nname: x\nsteps:\n  - rebase: { max_rounds: 2 }\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ps := Parse([]byte(src)); len(ps) == 0 {
				t.Fatalf("%s parses clean, want a refusal", name)
			}
		})
	}
}

// TestCatalogRenamedNamesAreGone: the two 0.6 catalog recipes the 0.7.0
// catalog retired are not in the new catalog (they moved to recipe.Renamed).
func TestCatalogRenamedNamesAreGone(t *testing.T) {
	for old := range Renamed {
		if _, ok := CatalogText(old); ok {
			t.Errorf("%s is still in the catalog; it was renamed", old)
		}
	}
}

// TestCatalogTextIsACopy: a caller that edits the bytes cannot change the
// catalog for the next caller.
func TestCatalogTextIsACopy(t *testing.T) {
	a, _ := CatalogText("default")
	want := string(a)
	for i := range a {
		a[i] = 'X'
	}
	b, _ := CatalogText("default")
	if string(b) != want {
		t.Fatalf("second call = %q, want %q", b, want)
	}
}
