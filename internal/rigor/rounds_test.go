package rigor

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// TestDefaultRoundsIsTheModesTable pins the owner's ruling of 2026-10-31,
// which answers O3: the loop counts depend on the rigor mode, and each mode's
// row is its own — POC and Hotfix the single round the question was about,
// Critical the senior's five (docs/rigor-command/reconciliation.md §4).
func TestDefaultRoundsIsTheModesTable(t *testing.T) {
	want := map[Mode]Rounds{
		POC:      {Inner: 1, Senior: 1},
		Startup:  {Inner: 2, Senior: 2},
		Pro:      {Inner: 3, Senior: 3},
		Critical: {Inner: 3, Senior: 5},
		Hotfix:   {Inner: 1, Senior: 1},
	}
	for m, w := range want {
		if got := DefaultRounds(m); got != w {
			t.Errorf("DefaultRounds(%s) = %+v; want %+v", m, got, w)
		}
	}
}

// TestDefaultRoundsHasARowForEveryMode: every mode of the vocabulary has its
// own row, so a mode added to Modes without one fails here rather than
// silently taking the row DefaultRounds answers for a mode it cannot place.
func TestDefaultRoundsHasARowForEveryMode(t *testing.T) {
	for _, m := range Modes {
		if _, ok := roundDefaults[m]; !ok {
			t.Errorf("roundDefaults has no row for %s", m)
		}
	}
	if len(roundDefaults) != len(Modes) {
		t.Errorf("roundDefaults has %d rows; want one per mode, %d", len(roundDefaults), len(Modes))
	}
}

// TestDefaultRoundsRowsAreWithinTheRecipeBounds: no row reaches outside the
// recipe's own bounds, so the number a mode supplies for a step is one the
// recipe could have carried itself (inner 1..MaxFirstLineRounds, senior
// 1..MaxReviewRounds). Asserted over the raw rows rather than through
// DefaultRounds, whose clamp would hide a row the table should not carry.
func TestDefaultRoundsRowsAreWithinTheRecipeBounds(t *testing.T) {
	for m, r := range roundDefaults {
		if r.Inner < 1 || r.Inner > recipe.MaxFirstLineRounds {
			t.Errorf("roundDefaults[%s].Inner = %d; want 1..%d", m, r.Inner, recipe.MaxFirstLineRounds)
		}
		if r.Senior < 1 || r.Senior > recipe.MaxReviewRounds {
			t.Errorf("roundDefaults[%s].Senior = %d; want 1..%d", m, r.Senior, recipe.MaxReviewRounds)
		}
	}
}

// TestDefaultRoundsForAModeItCannotPlace: a mode outside the vocabulary takes
// the strictest row there is, the fail-closed reading table.go gives a mode it
// cannot read — a value this code cannot place must never be the lenient
// answer (the zero Mode, a typo). The resolution path only ever hands over a
// known mode, so this is the guard behind it, not a run's behaviour.
func TestDefaultRoundsForAModeItCannotPlace(t *testing.T) {
	strictest := DefaultRounds(Critical)
	for _, r := range roundDefaults {
		if r.Inner > strictest.Inner || r.Senior > strictest.Senior {
			t.Fatalf("row %+v is above Critical's %+v, so Critical is not the strictest row", r, strictest)
		}
	}
	for _, m := range []Mode{"", "poc ", "Critical", "critical"} {
		if got := DefaultRounds(m); got != strictest {
			t.Errorf("DefaultRounds(%q) = %+v; want the strictest row %+v", m, got, strictest)
		}
	}
}

// TestDefaultRoundsFollowTheAxisAbovePOC: the table sits on the modes' own
// axis (D7b, T3.2) — the stricter the mode, the more rounds, never fewer. It
// is the property a hand-edited table breaks, and the one a reader of the
// table takes for granted.
func TestDefaultRoundsFollowTheAxisAbovePOC(t *testing.T) {
	for i := 1; i < len(Modes)-1; i++ { // the four on-axis modes; Hotfix is off it
		a, b := DefaultRounds(Modes[i-1]), DefaultRounds(Modes[i])
		if b.Inner < a.Inner || b.Senior < a.Senior {
			t.Errorf("%s %+v is below %s %+v; the table follows the axis", Modes[i], b, Modes[i-1], a)
		}
	}
}

// TestAdversarialDefaultIsTheModesSeniorRow: the adversarial pass — review
// level 3 — starts from the mode's own senior number, because the ruling
// gives the modes' rounds two cells and the adversarial step is a review pass
// on the same axis, run after the senior one on the same budget. A step's own
// max_rounds and the layers above it still set another number, as they do for
// the senior loop: this is a default and not a ceiling.
func TestAdversarialDefaultIsTheModesSeniorRow(t *testing.T) {
	for _, m := range Modes {
		if got, want := AdversarialDefault(m), DefaultRounds(m).Senior; got != want {
			t.Errorf("AdversarialDefault(%s) = %d; want the mode's senior rounds %d", m, got, want)
		}
	}
	for _, m := range []Mode{"", "poc "} {
		if got, want := AdversarialDefault(m), DefaultRounds(Critical).Senior; got != want {
			t.Errorf("AdversarialDefault(%q) = %d; want the strictest row's senior rounds %d", m, got, want)
		}
	}
}
