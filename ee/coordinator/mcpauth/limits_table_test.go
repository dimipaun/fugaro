// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// axis is the four modes a window reads, and the whole of what these cases
// vary. It is written out here rather than taken from rigor.Modes' head so
// that a mode added to the axis is a case this file has to name.
var axis = []rigor.Mode{rigor.POC, rigor.Startup, rigor.Pro, rigor.Critical}

// TestWindowTableAdmitsExactlyItsRange is the table D926 describes, every
// pair of the axis, both halves of it: the window a pair leaves admits
// exactly the modes between its floor and its ceiling, and a pair whose
// ceiling ends below its floor admits nothing and is refused where it is
// written.
//
// It is exhaustive on purpose. "Only narrows" is a property of the four
// modes against each other, and a window read with the wrong comparison
// (>= against >, or Rank on the wrong side) is a window that is off by one
// mode in a direction nothing else notices.
func TestWindowTableAdmitsExactlyItsRange(t *testing.T) {
	for _, min := range axis {
		for _, max := range axis {
			lim := mcpauth.Limits{
				MinRigor: min,
				MaxRigor: max,
				Scopes:   []string{mcpauth.ScopeTasksRead},
			}
			err := lim.Validate()
			if min.Rank() > max.Rank() {
				if !errors.Is(err, mcpauth.ErrEmptyWindow) {
					t.Errorf("min %q, max %q: Validate() = %v; want ErrEmptyWindow", min, max, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("min %q, max %q: Validate() = %v; want ok", min, max, err)
				continue
			}
			for _, m := range axis {
				want := m.Rank() >= min.Rank() && m.Rank() <= max.Rank()
				if got := lim.Admits(m) == nil; got != want {
					t.Errorf("min %q, max %q: Admits(%q) = %v; want %v", min, max, m, got, want)
				}
			}
			// The flag is off in every case here, so no window admits
			// Hotfix whatever its bounds are.
			if lim.Admits(rigor.Hotfix) == nil {
				t.Errorf("min %q, max %q: Admits(hotfix) = nil; want refused with the flag off", min, max)
			}
		}
	}
}

// TestHotfixIsNotOnTheAxis pins D925, the case rigor's own Rank makes
// dangerous: Hotfix ranks 0 — below every on-axis mode — precisely so that a
// Hotfix rule can never win a comparison against a Pro or Critical one. A
// window that read it would therefore treat `min: pro` as admitting Hotfix,
// which is the opposite of "a separate opt-in flag, off by default".
//
// So the flag answers Hotfix by itself and the window does not read it at
// all, in both directions: a `min: pro` token refuses Hotfix with the flag
// off, and a token whose policy floor is `poc` admits Hotfix with the flag
// on — and refuses it with the flag off, however low its floor is.
func TestHotfixIsNotOnTheAxis(t *testing.T) {
	pro := mcpauth.Limits{
		MinRigor: rigor.Pro,
		MaxRigor: rigor.Critical,
		Scopes:   []string{mcpauth.ScopeTasksRead},
	}

	if err := pro.Admits(rigor.POC); !errors.Is(err, mcpauth.ErrModeRefused) {
		t.Errorf("Admits(poc) under min pro = %v; want refused", err)
	}
	err := pro.Admits(rigor.Hotfix)
	if !errors.Is(err, mcpauth.ErrModeRefused) {
		t.Fatalf("Admits(hotfix) with the flag off = %v; want refused", err)
	}
	if !strings.Contains(err.Error(), "hotfix") {
		t.Errorf("Admits(hotfix) = %q; want it to name hotfix", err)
	}

	// The floor is the axis's, and the flag is not: a token that allows
	// Hotfix allows it at every floor, because Hotfix is not read against
	// the floor at all.
	hotfix := pro
	hotfix.Hotfix = true
	if err := hotfix.Admits(rigor.Hotfix); err != nil {
		t.Errorf("Admits(hotfix) with the flag on and min pro = %v; want admitted", err)
	}

	// The plan's own case: a `min: poc` policy admits a Hotfix token.
	poc := mcpauth.Limits{MinRigor: rigor.POC, Scopes: []string{mcpauth.ScopeTasksRead}}
	if err := poc.Admits(rigor.Hotfix); !errors.Is(err, mcpauth.ErrModeRefused) {
		t.Errorf("Admits(hotfix) under min poc with the flag off = %v; want refused", err)
	}
	poc.Hotfix = true
	if err := poc.Admits(rigor.Hotfix); err != nil {
		t.Errorf("Admits(hotfix) under min poc with the flag on = %v; want admitted", err)
	}

	// A bound that names Hotfix is not a window: it is refused where the
	// limit is written rather than ranked inside a comparison it would lose
	// (or, folded the other way, win).
	for _, lim := range []mcpauth.Limits{
		{MinRigor: rigor.Hotfix, Scopes: []string{mcpauth.ScopeTasksRead}},
		{MaxRigor: rigor.Hotfix, Scopes: []string{mcpauth.ScopeTasksRead}},
		{MinRigor: "fast", Scopes: []string{mcpauth.ScopeTasksRead}},
	} {
		if err := lim.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil; want the bound refused", lim)
		}
	}

	// And a mode outside the vocabulary is refused as unknown rather than
	// ranked as 0, which is what rigor.Rank does with it.
	if err := poc.Admits("fast"); !errors.Is(err, mcpauth.ErrUnknownMode) {
		t.Errorf("Admits(fast) = %v; want ErrUnknownMode", err)
	}
}
