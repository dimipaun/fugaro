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
