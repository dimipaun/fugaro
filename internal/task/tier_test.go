package task

import (
	"strings"
	"testing"
)

// TestTaskTierValidated: the expectation tier and the expected scope travel in
// the task (docs/task-governance spec §3, plan T8.1). The tier's vocabulary is
// closed — a name outside it is a launcher's bug, not a run's opinion about its
// own size — and an untiered task is a real case: it is every spec written
// before this release and every launch that attaches no expectation.
//
// The scope is paths, not prose: each entry is bounded and non-blank the way
// Spec.Paths is, because a hand-written task.json is what can make the list
// arbitrarily long.
func TestTaskTierValidated(t *testing.T) {
	for _, tier := range []string{"", "trivial", "small", "medium", "large"} {
		in := specHead + `,"tier":"` + tier + `"}`
		if _, err := Parse([]byte(in)); err != nil {
			t.Errorf("tier %q: %v", tier, err)
		}
	}
	for _, tier := range []string{"Trivial", "SMALL", "small ", " small", "huge", "none"} {
		in := specHead + `,"tier":"` + tier + `"}`
		if _, err := Parse([]byte(in)); err == nil || !strings.Contains(err.Error(), "tier") {
			t.Errorf("tier %q: err = %v, want a refusal naming tier", tier, err)
		}
	}

	scope := specHead + `,"tier":"small","expected_scope":["internal/payments/x.go","docs/**"]}`
	if _, err := Parse([]byte(scope)); err != nil {
		t.Errorf("an expected scope: %v", err)
	}
	blank := specHead + `,"expected_scope":["internal/x.go","   "]}`
	if _, err := Parse([]byte(blank)); err == nil || !strings.Contains(err.Error(), "expected_scope[1]") {
		t.Errorf("a blank expected scope entry: err = %v, want a refusal naming it", err)
	}
	over := make([]string, MaxDeclaredPaths+1)
	for i := range over {
		over[i] = "internal/x.go"
	}
	if _, err := Parse([]byte(specHead + `,"expected_scope":["` + strings.Join(over, `","`) + `"]}`)); err == nil ||
		!strings.Contains(err.Error(), "expected_scope") {
		t.Errorf("%d expected scope entries: err = %v, want a refusal", len(over), err)
	}
	long := specHead + `,"expected_scope":["` + strings.Repeat("a", MaxDeclaredPathBytes+1) + `"]}`
	if _, err := Parse([]byte(long)); err == nil || !strings.Contains(err.Error(), "expected_scope[0]") {
		t.Errorf("an over-long expected scope entry: err = %v, want a refusal naming it", err)
	}
}
