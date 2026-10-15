package pricing_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// TestCanonicalMapRoundTrip is Review Focus 11's pin (docs/plans/
// 2026-10-14-project-centric.md): for every row of the map, the canonical ID, the
// route's own ID and the inverse all name one model, and nothing else is
// translated. A map that got a pair wrong sends a Claude call to a route that
// does not serve it, and charges it at a price nobody quoted.
//
// Hand mutations this fails on: swapping two rows' RouteIDs (the round trip
// still works, so each pair is checked in both directions); deleting a row
// (the canonical ID then resolves to nothing, where the pin renderer would
// have to guess); deriving the ID instead of reading it (a case-or-separator
// variant of the route's ID would resolve).
func TestCanonicalMapRoundTrip(t *testing.T) {
	mapped := 0
	for _, r := range pricing.ClaudeRoutes {
		if r.RouteID == "" {
			if r.Unmapped == "" {
				t.Errorf("%s: neither a route ID nor an unmapped reason", r.Canonical)
			}
			continue
		}
		mapped++
		if r.Unmapped != "" {
			t.Errorf("%s: both a route ID (%s) and an unmapped reason", r.Canonical, r.RouteID)
		}
		if !strings.HasPrefix(r.RouteID, "anthropic/") {
			t.Errorf("%s: route ID %q is not an anthropic/<id> claim", r.Canonical, r.RouteID)
		}
		// The pins renderer's question: what is sent on this route?
		id, ok := pricing.RouteIDFor(r.Canonical, "")
		if !ok || id != r.RouteID {
			t.Errorf("RouteIDFor(%q, \"\") = (%q, %v), want (%q, true)", r.Canonical, id, ok, r.RouteID)
		}
		// The caller that already holds the claim: is this the pair?
		id, ok = pricing.RouteIDFor(r.Canonical, r.RouteID)
		if !ok || id != r.RouteID {
			t.Errorf("RouteIDFor(%q, %q) = (%q, %v), want (%q, true)", r.Canonical, r.RouteID, id, ok, r.RouteID)
		}
		if c, ok := pricing.CanonicalFor(r.RouteID); !ok || c != r.Canonical {
			t.Errorf("CanonicalFor(%q) = (%q, %v), want (%q, true): the inverse must name the model a person pinned", r.RouteID, c, ok, r.Canonical)
		}
		// A spelling that is not the pair is not translated, whatever it
		// looks like: the map is the authority, not a case fold.
		variant := strings.ReplaceAll(r.RouteID, ".", "-")
		if variant != r.RouteID {
			if id, ok := pricing.RouteIDFor(r.Canonical, variant); ok {
				t.Errorf("RouteIDFor(%q, %q) = (%q, true), want no answer: %q is not the map's pair, and a near miss is a wrong route", r.Canonical, variant, id, variant)
			}
		}
	}
	if mapped == 0 {
		t.Fatal("no Claude ID is mapped: the switch could never route anything")
	}
}

// TestCanonicalMapRefusesAnUnmappedModel: a canonical ID with no measured route
// ID is refused loudly, with the map's own reason — never guessed, and never
// quietly resolved to a near miss.
func TestCanonicalMapRefusesAnUnmappedModel(t *testing.T) {
	unmapped := 0
	for _, r := range pricing.ClaudeRoutes {
		if r.RouteID != "" {
			continue
		}
		unmapped++
		if id, ok := pricing.RouteIDFor(r.Canonical, ""); ok {
			t.Errorf("RouteIDFor(%q, \"\") = (%q, true), want no answer: the row is unmapped", r.Canonical, id)
		}
		if _, ok := pricing.CanonicalFor("anthropic/" + r.Canonical); ok {
			t.Errorf("CanonicalFor(%q) resolved, want no answer: no route ID was measured for %s", "anthropic/"+r.Canonical, r.Canonical)
		}
		why, ok := pricing.UnmappedReason(r.Canonical)
		if !ok || why == "" {
			t.Errorf("UnmappedReason(%q) = (%q, %v), want the map's reason", r.Canonical, why, ok)
		}
	}
	if unmapped == 0 {
		t.Error("every row of the map is mapped: the explicit-unmapped half is untested")
	}
	if id, ok := pricing.RouteIDFor("claude-nova-9", ""); ok {
		t.Errorf("RouteIDFor(%q, \"\") = (%q, true), want no answer: an ID the map does not carry is not translated", "claude-nova-9", id)
	}
	if _, ok := pricing.UnmappedReason("claude-nova-9"); ok {
		t.Error("UnmappedReason resolved for an ID the map does not carry: absence and an explicit unmapped row are different states")
	}
}

// TestCanonicalMapIsExhaustive: every canonical Claude ID this build prices has
// a row — the route's ID or the reason it has none — and so does every
// canonical ID a shipped catalog row claims. A model the table prices but the
// map skips is where the switch would have to guess, which is exactly what
// D1a's explicit list and this map exist to prevent.
//
// Hand mutation this fails on: adding a Claude row to pricing.Embedded (or a
// canonical_id to a catalog row) without a map row for it.
func TestCanonicalMapIsExhaustive(t *testing.T) {
	has := map[string]bool{}
	for _, r := range pricing.ClaudeRoutes {
		has[r.Canonical] = true
	}
	var want []string
	for _, m := range pricing.Embedded().Models {
		if strings.HasPrefix(m.ID, "claude-") || strings.HasPrefix(m.ID, "anthropic/") {
			want = append(want, m.ID)
		}
	}
	f, err := catalog.Load()
	if err != nil {
		t.Fatalf("loading the shipped catalog: %v", err)
	}
	for id, e := range f.Models {
		if e.CanonicalID != "" {
			want = append(want, e.CanonicalID)
			if !strings.HasPrefix(e.CanonicalID, "anthropic/") {
				// The row's own key is the route's ID, so the canonical one
				// must be the direct spelling: anything else is a row the map
				// and the pins cannot agree on.
				t.Errorf("%s: canonical_id %q is not the direct spelling", id, e.CanonicalID)
			}
		}
	}
	for _, id := range want {
		if !has[id] {
			t.Errorf("%s is priced by this build but has no row in pricing.ClaudeRoutes: add the route's ID, or the explicit reason it has none", id)
		}
	}
}

// TestRouteIDForNeverDerivesASpelling: the map's own pair is the only thing it
// translates. The two spellings of one model differ in case and in the
// separator that holds the version, so anything that folded one into the other
// would answer a routing question with a string operation — and the route entry
// the operator wrote is what is sent, so a derived spelling is a claim no route
// made.
func TestRouteIDForNeverDerivesASpelling(t *testing.T) {
	for _, in := range []string{"anthropic/claude-sonnet-5-5", "ANTHROPIC/CLAUDE-SONNET-5.5", "anthropic/claude-sonnet-5.5:online", "anthropic/claude-sonnet-5.5[1m]"} {
		if id, ok := pricing.RouteIDFor("claude-sonnet-5-5", in); ok {
			t.Errorf("RouteIDFor(%q, %q) = (%q, true), want no answer: only the checked-in pair is translated", "claude-sonnet-5-5", in, id)
		}
	}
	if _, ok := pricing.CanonicalFor("anthropic/claude-sonnet-5-5"); ok {
		t.Error("CanonicalFor resolved a spelling the map does not hold")
	}
}
