package recipe

import "testing"

// TestStageBoundIsFinite: the bound the cost preview and the docs quote, for
// every catalog recipe shape at the knobs' largest values.
func TestStageBoundIsFinite(t *testing.T) {
	std, _ := Parse([]byte("version: 1\nname: s\nsteps:\n  - first_line: {}\n  - review: { bounce: first_line }\n"))
	if got := StageBound(std, 3, 10); got != 1+2*3+10*(2+2*3) { // 87
		t.Fatalf("standard bound = %d", got)
	}
	solo, _ := Parse([]byte("version: 1\nname: s\nroles: { reviewer: coder }\nsteps:\n  - review: {}\n"))
	if got := StageBound(solo, 3, 2); got != 1+2*2 { // implement, then review+fix per round
		t.Fatalf("solo bound = %d", got)
	}
	ro, _ := Parse([]byte("version: 1\nname: r\nmode: review\nsteps:\n  - review: { max_rounds: 1 }\n"))
	if got := StageBound(ro, 3, 10); got != 1 {
		t.Fatalf("review-only bound = %d", got)
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
