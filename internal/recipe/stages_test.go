package recipe

import "testing"

// n3Default is the adversarial default these tests hand StageBound for a step
// that writes none. The number is the caller's, not this package's (StageBound
// takes it so that internal/rigor, which this package's own test imports, can
// be its source), so it moves a bound only for a recipe carrying the step, and
// two is the figure the plan quotes its arithmetic with.
const n3Default = 2

// TestStageBoundWithAdversarial pins the two figures the plan quotes (F12,
// D726): today's `standard` at the knobs' defaults bounds a run at 11 model
// stages, and the same recipe with review level 3 added — adversarial with
// two rounds bouncing to first_line, at f = 1 and n = 2 — bounds it at 19.
// The step's term is n3*(2 + 2*f), so a round that bounces costs the
// adversarial review, its fix and the cheap loop, and a step that writes no
// bounce costs n3*2: the bound must never come in under either.
//
// n3 is the caller's number where the step writes no max_rounds, and it is one
// source with the rigor modes: a caller that resolved a mode passes
// rigor.AdversarialDefault(mode), the senior row, and a caller with no mode
// passes the strictest row (5, Critical's senior rounds). Here the step's own
// rounds and the caller's default are both two, so one bound covers both.
func TestStageBoundWithAdversarial(t *testing.T) {
	std, ps := Parse([]byte("version: 1\nname: s\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n"))
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if got := StageBound(std, 1, 2, n3Default); got != 11 {
		t.Fatalf("standard bound = %d, want 11", got)
	}
	for name, steps := range map[string]string{
		"explicit rounds": "  - adversarial: { max_rounds: 2, bounce: first_line }\n",
		"default rounds":  "  - adversarial: { bounce: first_line }\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, ps := Parse([]byte("version: 1\nname: s\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n" + steps))
			if len(ps) != 0 {
				t.Fatalf("problems: %s", ProblemsText(ps))
			}
			if got := StageBound(r, 1, 2, n3Default); got != 19 {
				t.Fatalf("bound = %d, want 19", got)
			}
		})
	}
	// The default a caller with no mode passes: the strictest row is five, so
	// the same step two rounds shorter at two is three rounds longer at five.
	r, ps := Parse([]byte("version: 1\nname: s\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - adversarial: { bounce: first_line }\n"))
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if got := StageBound(r, 1, 2, 5); got != 31 {
		t.Fatalf("strictest-row bound = %d, want 31", got)
	}
	// No bounce: each of the n3 rounds is the adversarial review and its fix.
	r, ps = Parse([]byte("version: 1\nname: s\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n  - adversarial: { max_rounds: 3 }\n"))
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if got := StageBound(r, 1, 2, n3Default); got != 11+3*2 {
		t.Fatalf("no-bounce bound = %d, want 17", got)
	}
	// The step's own max_rounds and the first-line step's own max_rounds both
	// bind: f is the step's when it writes one, exactly as the review term
	// reads it.
	r, ps = Parse([]byte("version: 1\nname: s\nsteps:\n  - first_line: { max_rounds: 3 }\n  - review: { bounce: first_line }\n  - adversarial: { max_rounds: 2, bounce: first_line }\n"))
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if got := StageBound(r, 1, 2, n3Default); got != 1+2*3+2*(2+2*3)+2*(2+2*3) {
		t.Fatalf("bound = %d, want %d", got, 1+2*3+2*(2+2*3)+2*(2+2*3))
	}
}

// TestStageBoundIsFinite: the bound the cost preview and the docs quote, for
// every catalog recipe shape at the knobs' largest values.
func TestStageBoundIsFinite(t *testing.T) {
	std, _ := Parse([]byte("version: 1\nname: s\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n"))
	if got := StageBound(std, 3, 10, n3Default); got != 1+2*3+10*(2+2*3) { // 87
		t.Fatalf("standard bound = %d", got)
	}
	solo, _ := Parse([]byte("version: 1\nname: s\nroles: { reviewer: coder }\nsteps:\n  - review: {}\n"))
	if got := StageBound(solo, 3, 2, n3Default); got != 1+2*2 { // implement, then review+fix per round
		t.Fatalf("solo bound = %d", got)
	}
	ro, _ := Parse([]byte("version: 1\nname: r\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n"))
	if got := StageBound(ro, 3, 10, n3Default); got != 1 {
		t.Fatalf("review-only bound = %d", got)
	}
}

// TestStageBoundArtifactIsOneModelStage: a mode: artifact recipe has no
// implement loop and no review loop, so a run of it starts at most one model
// stage -- the optional plan. The render is a runner command with its own
// timeout, exactly like a check step, so it is not a model stage and does
// not count. Without this arm the recipe falls into the implement
// arithmetic and is priced as 1 + 2*rounds stages: five at this
// repository's defaults, none of which a run of it can start (design
// artifact-runs.md §6, plan P8).
func TestStageBoundArtifactIsOneModelStage(t *testing.T) {
	for name, steps := range map[string]string{
		"plan then render": "  - plan: {}\n  - render: {}\n",
		"render alone":     "  - render: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, ps := Parse([]byte(artifactHead + "steps:\n" + steps + artifactOutputs))
			if len(ps) != 0 {
				t.Fatalf("problems: %s", ProblemsText(ps))
			}
			if got := StageBound(r, 3, 10, n3Default); got != 1 {
				t.Fatalf("artifact bound = %d, want 1", got)
			}
		})
	}
}

// TestStageBoundDigestHasNoImplementStage: a digest recipe's first step is
// the runner's own work -- it writes the run reports the repository is
// missing with no model call -- so a run of it starts no implement stage,
// and the review loop after it is all the run can record. Without this arm
// the recipe falls into the implement arithmetic and is priced one stage too
// high, which is the direction a cost ceiling must not err in.
func TestStageBoundDigestHasNoImplementStage(t *testing.T) {
	r, ps := Parse([]byte("version: 1\nname: d\nsteps:\n  - digest: {}\n  - review: { max_rounds: 1 }\n"))
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if got := StageBound(r, 3, 10, n3Default); got != 2 { // the senior review and its fix
		t.Fatalf("digest bound = %d, want 2", got)
	}
}

// TestUsesV07DecidesByKeyPresence: a 0.6 parser refuses every one of these
// keys outright ("is not a recipe key"), even written with a value that
// looks like a no-op, so UsesV07 must decide from presence, never from the
// parsed value. Each row below exercises exactly one 0.7.0 construct (G7 to
// G14), alone, so a mutation that drops one construct's presence-marking
// cannot hide behind another construct in the same recipe still tripping
// UsesV07 (the combined "standard" recipe in TestExtendedFormatParses uses
// both use_when and bounce together, and would not catch that).
func TestUsesV07DecidesByKeyPresence(t *testing.T) {
	for name, src := range map[string]string{
		"use_when, value a no-op": "version: 1\nname: x\nuse_when: \"\"\nsteps:\n  - review: {}\n",
		"mode, value the default": "version: 1\nname: x\nmode: implement\nsteps:\n  - review: {}\n",
		"roles.coder":             "version: 1\nname: x\nroles: { coder: reviewer }\nsteps:\n  - review: {}\n",
		"review.bounce":           "version: 1\nname: x\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n",
		"a check step":            "version: 1\nname: x\nsteps:\n  - check: { command: test }\n  - review: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, ps := Parse([]byte(src))
			if len(ps) != 0 {
				t.Fatalf("problems: %s", ProblemsText(ps))
			}
			if !UsesV07(r) {
				t.Fatal("UsesV07 = false, want true")
			}
		})
	}
	none, ps := Parse([]byte("version: 1\nname: x\nsteps:\n  - first_line: {}\n  - review: {}\n"))
	if len(ps) != 0 {
		t.Fatalf("problems: %s", ProblemsText(ps))
	}
	if UsesV07(none) {
		t.Fatal("UsesV07 = true, want false for a recipe with no 0.7.0 construct")
	}
}
