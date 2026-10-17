package pricing

import (
	"slices"
	"strings"
	"testing"

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
	for _, r := range claudeRoutes {
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
		id, ok := RouteIDFor(r.Canonical, "")
		if !ok || id != r.RouteID {
			t.Errorf("RouteIDFor(%q, \"\") = (%q, %v), want (%q, true)", r.Canonical, id, ok, r.RouteID)
		}
		// The caller that already holds the claim: is this the pair?
		id, ok = RouteIDFor(r.Canonical, r.RouteID)
		if !ok || id != r.RouteID {
			t.Errorf("RouteIDFor(%q, %q) = (%q, %v), want (%q, true)", r.Canonical, r.RouteID, id, ok, r.RouteID)
		}
		if c, ok := CanonicalFor(r.RouteID); !ok || c != r.Canonical {
			t.Errorf("CanonicalFor(%q) = (%q, %v), want (%q, true): the inverse must name the model a person pinned", r.RouteID, c, ok, r.Canonical)
		}
		// A spelling that is not the pair is not translated, whatever it
		// looks like: the map is the authority, not a case fold.
		variant := strings.ReplaceAll(r.RouteID, ".", "-")
		if variant != r.RouteID {
			if id, ok := RouteIDFor(r.Canonical, variant); ok {
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
	for _, r := range claudeRoutes {
		if r.RouteID != "" {
			continue
		}
		unmapped++
		if id, ok := RouteIDFor(r.Canonical, ""); ok {
			t.Errorf("RouteIDFor(%q, \"\") = (%q, true), want no answer: the row is unmapped", r.Canonical, id)
		}
		if _, ok := CanonicalFor("anthropic/" + r.Canonical); ok {
			t.Errorf("CanonicalFor(%q) resolved, want no answer: no route ID was measured for %s", "anthropic/"+r.Canonical, r.Canonical)
		}
		why, ok := UnmappedReason(r.Canonical)
		if !ok || why == "" {
			t.Errorf("UnmappedReason(%q) = (%q, %v), want the map's reason", r.Canonical, why, ok)
		}
	}
	if unmapped == 0 {
		t.Error("every row of the map is mapped: the explicit-unmapped half is untested")
	}
	if id, ok := RouteIDFor("claude-nova-9", ""); ok {
		t.Errorf("RouteIDFor(%q, \"\") = (%q, true), want no answer: an ID the map does not carry is not translated", "claude-nova-9", id)
	}
	if _, ok := UnmappedReason("claude-nova-9"); ok {
		t.Error("UnmappedReason resolved for an ID the map does not carry: absence and an explicit unmapped row are different states")
	}
}

// TestCanonicalMapIsExhaustive: every canonical Claude ID this build prices has
// a row — the route's ID or the reason it has none — and so does every
// canonical ID a shipped catalog row claims. A model the table prices but the
// map skips is where the switch would have to guess, which is exactly what
// D1a's explicit list and this map exist to prevent.
//
// Hand mutation this fails on: adding a Claude row to Embedded (or a
// canonical_id to a catalog row) without a map row for it.
func TestCanonicalMapIsExhaustive(t *testing.T) {
	has := map[string]bool{}
	for _, r := range claudeRoutes {
		has[r.Canonical] = true
	}
	var want []string
	for _, m := range Embedded().Models {
		if strings.HasPrefix(m.ID, "claude-") || strings.HasPrefix(m.ID, "anthropic/") {
			want = append(want, m.ID)
		}
	}
	f, err := catalog.Load()
	if err != nil {
		t.Fatalf("loading the shipped catalog: %v", err)
	}
	for id, e := range f.Models {
		if e.CanonicalID == "" {
			continue
		}
		want = append(want, e.CanonicalID)
		// The row's key is the route's ID, so canonical_id is the direct
		// spelling a pin carries (claude-sonnet-5-5) — the generator refuses a
		// route ID there, and so does the loader: a row carrying one would be
		// the pair written backwards.
		if strings.HasPrefix(e.CanonicalID, "anthropic/") {
			t.Errorf("%s: canonical_id %q is a route ID, not the direct spelling a pin carries", id, e.CanonicalID)
		}
		// And the two must be the map's own pair: the row's key is what the
		// route is sent and charged, canonical_id is what a pin names, and a
		// catalog row that pairs them differently from this map is a second
		// answer to where the call goes and what it costs (Task 1b.2 step 2).
		if _, ok := RouteIDFor(e.CanonicalID, id); !ok {
			t.Errorf("%s: the catalog pairs it with canonical_id %q, and the canonical-to-route map does not: add the pair to ClaudeRoutes, with the route's ID measured", id, e.CanonicalID)
		}
	}
	for _, id := range want {
		if !has[id] {
			t.Errorf("%s is priced by this build but has no row in claudeRoutes: add the route's ID, or the explicit reason it has none", id)
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
		if id, ok := RouteIDFor("claude-sonnet-5-5", in); ok {
			t.Errorf("RouteIDFor(%q, %q) = (%q, true), want no answer: only the checked-in pair is translated", "claude-sonnet-5-5", in, id)
		}
	}
	if _, ok := CanonicalFor("anthropic/claude-sonnet-5-5"); ok {
		t.Error("CanonicalFor resolved a spelling the map does not hold")
	}
}

// The map's content, written out here rather than read from the table it
// checks. TestCanonicalMapRoundTrip only compares the map with itself, and the
// round trip is symmetric: swapping the sonnet and opus rows' route IDs
// round-trips perfectly, so that test stays green while every routed sonnet
// call is sent as opus and charged at opus's rate (the review finding on this
// change). These are the pairs the design's D8 measured and the Claude IDs this
// build prices but has no measured route for; when a route is measured for one
// of the latter, it moves up into expectedPairs here and in claudeRoutes, and
// that edit is the decision.
var (
	expectedPairs = map[string]string{
		"claude-fable-5-1":  "anthropic/claude-fable-5.1",
		"claude-opus-5-5":   "anthropic/claude-opus-5.5",
		"claude-sonnet-5-5": "anthropic/claude-sonnet-5.5",
	}
	expectedUnmapped = []string{"claude-haiku-4-5", "claude-opus-5", "claude-sonnet-5"}
)

// TestCanonicalMapHoldsTheMeasuredPairs asserts the map against that fixture,
// both ways: every pair the fixture names resolves, and the map holds no pair
// the fixture does not. A row that got a pair wrong, a row that was swapped
// with another's, and a spelling invented for a model nobody measured all fail
// here — and the second direction is what makes the first three the map's whole
// content rather than a subset of it.
//
// Hand mutation this fails on: swapping claude-sonnet-5-5's and
// claude-opus-5-5's RouteIDs (each row then holds the other's route, and the
// fixture's own pairs do not resolve).
func TestCanonicalMapHoldsTheMeasuredPairs(t *testing.T) {
	for canonical, routeID := range expectedPairs {
		if id, ok := RouteIDFor(canonical, ""); !ok || id != routeID {
			t.Errorf("RouteIDFor(%q, \"\") = (%q, %v), want (%q, true)", canonical, id, ok, routeID)
		}
		if id, ok := RouteIDFor(canonical, routeID); !ok || id != routeID {
			t.Errorf("RouteIDFor(%q, %q) = (%q, %v), want (%q, true): a caller holding this claim must be told it is the pair", canonical, routeID, id, ok, routeID)
		}
		if c, ok := CanonicalFor(routeID); !ok || c != canonical {
			t.Errorf("CanonicalFor(%q) = (%q, %v), want (%q, true)", routeID, c, ok, canonical)
		}
	}
	for _, canonical := range expectedUnmapped {
		why, ok := UnmappedReason(canonical)
		if !ok || why == "" {
			t.Errorf("UnmappedReason(%q) = (%q, %v), want the reason it has no route", canonical, why, ok)
		}
		if id, ok := RouteIDFor(canonical, ""); ok {
			t.Errorf("RouteIDFor(%q, \"\") = (%q, true), want no answer: no route ID was measured for it", canonical, id)
		}
	}
	// The map is exactly this fixture: a row the fixture does not name is a
	// pair somebody added without deciding the route, and one the fixture names
	// but the map lacks is a translation that went missing.
	mapped := map[string]string{}
	var unmapped []string
	for _, r := range claudeRoutes {
		if r.RouteID == "" {
			unmapped = append(unmapped, r.Canonical)
			continue
		}
		mapped[r.Canonical] = r.RouteID
	}
	for canonical, routeID := range expectedPairs {
		if got, ok := mapped[canonical]; !ok || got != routeID {
			t.Errorf("the map pairs %s with (%q, %v), want %q", canonical, got, ok, routeID)
		}
	}
	for canonical, routeID := range mapped {
		if want, ok := expectedPairs[canonical]; !ok {
			t.Errorf("the map pairs %s with %s, which this fixture does not name: a route ID for a model nobody measured is exactly the guess the map exists to remove", canonical, routeID)
		} else if want != routeID {
			t.Errorf("the map pairs %s with %s, want %s", canonical, routeID, want)
		}
	}
	if len(unmapped) != len(expectedUnmapped) {
		t.Fatalf("the map leaves %v unmapped, want %v", unmapped, expectedUnmapped)
	}
	for _, canonical := range expectedUnmapped {
		if !slices.Contains(unmapped, canonical) {
			t.Errorf("the map does not carry %s as explicitly unmapped: %v", canonical, unmapped)
		}
	}
}
