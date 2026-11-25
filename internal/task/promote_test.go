package task

import (
	"encoding/json"
	"strings"
	"testing"
)

// promoteSpec is the smallest spec that carries a promote block: what
// fugaro run --promote <area> --mode <level> writes (LV7.1), and nothing else.
func promoteSpec() *Spec {
	return &Spec{Version: 1, RunID: "20261122-120000-a1b2", Repo: "acme/app", Ref: "main", Task: "Raise the payments area to pro.", Rigor: "pro", Paths: []string{"internal/payments/**"}, Promote: &Promote{Area: "payments", Paths: []string{"internal/payments/**"}}}
}

// TestPromoteProblem is the block's own shape: an area is one line with a
// name in it, and the paths are the declared ones a spec carries. Every
// refusal here is the fail-closed direction — a promote run that cannot name
// the area it raises, or whose paths are not a list, is a run that would close
// items of an area it never resolved.
func TestPromoteProblem(t *testing.T) {
	long := strings.Repeat("a", MaxPromoteAreaBytes+1)
	paths := make([]string, MaxDeclaredPaths+1)
	for i := range paths {
		paths[i] = "internal/x/y.go"
	}
	for _, tc := range []struct {
		name  string
		block Promote
		want  string
	}{
		{"valid", Promote{Area: "payments", Paths: []string{"internal/payments/**"}}, ""},
		{"no paths at all", Promote{Area: "payments"}, ""},
		{"empty", Promote{}, "must name the area it promotes"},
		{"spaces", Promote{Area: "   "}, "must name the area it promotes"},
		{"too long", Promote{Area: long}, "longer than 80 bytes"},
		{"newline", Promote{Area: "payments\nextra"}, "one line"},
		{"nul", Promote{Area: "payments\x00"}, "one line"},
		{"control character", Promote{Area: "payments\x07"}, "control character"},
		{"too many paths", Promote{Area: "payments", Paths: paths}, "more than 100 paths"},
		{"blank path", Promote{Area: "payments", Paths: []string{" "}}, "empty path at paths[0]"},
		{"long path", Promote{Area: "payments", Paths: []string{strings.Repeat("a", MaxDeclaredPathBytes+1)}}, "longer than"},
	} {
		if got := tc.block.Problem(); got != tc.want && !strings.Contains(got, tc.want) {
			t.Errorf("%s: Problem() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSpecValidateHoldsThePromoteBlock: the two clauses the block needs beside
// its own shape — a promote run names the level it raises the area to, and it
// is not a review — and the positive case, so the rules are not vacuously
// refused.
func TestSpecValidateHoldsThePromoteBlock(t *testing.T) {
	if err := promoteSpec().Validate(); err != nil {
		t.Fatalf("a promote spec with a mode is valid: %v", err)
	}
	s := promoteSpec()
	s.Rigor = ""
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "names no rigor level") {
		t.Errorf("a promote spec with no mode: err = %v, want the level named", err)
	}
	s = promoteSpec()
	s.ReviewPRs = []int{7}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "don't go together") {
		t.Errorf("a promote spec that is also a review: err = %v, want the pair refused", err)
	}
	s = promoteSpec()
	s.Promote = &Promote{Area: ""}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "task spec: promote") {
		t.Errorf("a promote spec with no area: err = %v, want the block refused", err)
	}
}

// TestPromoteRoundTripsThroughTheTask: the block is a wire shape, so a task
// stored by this release carries it byte for byte, and a spec with no block
// stays exactly what the previous release wrote.
func TestPromoteRoundTripsThroughTheTask(t *testing.T) {
	data, err := json.Marshal(promoteSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"promote":{"area":"payments","paths":["internal/payments/**"]}`) {
		t.Errorf("marshalled task carries %s", data)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Promote == nil || back.Promote.Area != "payments" || len(back.Promote.Paths) != 1 {
		t.Errorf("parsed promote = %+v", back.Promote)
	}
	plain, err := json.Marshal(&Spec{Version: 1, RunID: "20261122-120000-a1b2", Repo: "acme/app", Ref: "main", Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "promote") {
		t.Errorf("a spec with no promote block carries one: %s", plain)
	}
}
