package tier

import (
	"reflect"
	"testing"
)

// TestVocabulary: the four tiers, in order, and the closed vocabulary's own
// two answers. The list is what the task spec, the governance configuration
// and the CLI share, so a name that leaves Tiers has to fail somewhere: Known
// is where the writers ask.
func TestVocabulary(t *testing.T) {
	want := []Tier{"trivial", "small", "medium", "large"}
	if !reflect.DeepEqual(Tiers, want) {
		t.Fatalf("Tiers = %v, want %v", Tiers, want)
	}
	if got := List(); got != "trivial, small, medium, large" {
		t.Errorf("List() = %q", got)
	}
	for _, tt := range want {
		if !Known(tt) {
			t.Errorf("Known(%q) = false", tt)
		}
	}
	for _, tt := range []Tier{"", "Trivial", "SMALL", "small ", "huge"} {
		if Known(tt) {
			t.Errorf("Known(%q) = true: the vocabulary is closed", tt)
		}
	}
}
