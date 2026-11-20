package policy_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// TestEffectiveModeOfUnsetIsObserve pins the one reading this vocabulary exists
// for: a mode nobody wrote is observe, and so is every string that names no
// live mode — the old word included. The answer is never off, which is what an
// unset mode was read as at the run, the launcher and the local config while
// the settings registry and the console were reading it as observe (F2, R3).
func TestEffectiveModeOfUnsetIsObserve(t *testing.T) {
	for _, tc := range []struct {
		mode policy.Mode
		want string
	}{
		{"", policy.ModeObserve},
		// The old word is written as the literal and never through the
		// constant, so this file needs no edit when I1 deletes it
		// (docs/always-a-budget/contract-mode.md §3).
		{"off", policy.ModeObserve},
		{"no such mode", policy.ModeObserve},
		{policy.ModeObserve, policy.ModeObserve},
		{policy.ModeEnforce, policy.ModeEnforce},
	} {
		if got := tc.mode.Effective(); got != tc.want {
			t.Errorf("Mode(%q).Effective() = %q, want %q", string(tc.mode), got, tc.want)
		}
	}
}

// TestTheRemovedModeIsNamed: the word that was a mode is recognised by one
// predicate and named by one text, so no reader has to spell it and no two
// refusals of it can say different things (D3009).
func TestTheRemovedModeIsNamed(t *testing.T) {
	if !policy.Mode("off").Removed() {
		t.Errorf("Mode(%q).Removed() = false, want true", "off")
	}
	for _, m := range []policy.Mode{"", "off ", "OFF", "no such mode", policy.ModeObserve, policy.ModeEnforce} {
		if m.Removed() {
			t.Errorf("Mode(%q).Removed() = true, want false: only the one word was ever a mode", string(m))
		}
	}
	// The one refusal says what the word is now and what to write instead: it
	// names the word it refuses and both live modes it offers as the fix.
	for _, want := range []string{"off", policy.ModeObserve, policy.ModeEnforce} {
		if !strings.Contains(policy.ModeOffRefusal, want) {
			t.Errorf("ModeOffRefusal does not name %q: %q", want, policy.ModeOffRefusal)
		}
	}
}
