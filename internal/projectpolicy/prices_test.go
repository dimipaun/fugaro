package projectpolicy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// TestModelPricesKeysAreEscapedOnTheNode: an RTDB key cannot hold "." or "/",
// and the model IDs the documentation uses carry one or the other — the
// documented deepseek/deepseek-v4-flash a slash (docs/multi-model.md), the
// catalog's own deepseek/deepseek-v4.1-flash a dot. Writing the block under
// the raw IDs is a write RTDB refuses whole (a 400, nothing stored), so the
// node holds escKey(id) and Decode reads it back: this is the round trip, and
// the shape of what is written is asserted against RTDB's own key rule rather
// than against a copy of the escape table.
func TestModelPricesKeysAreEscapedOnTheNode(t *testing.T) {
	const slash, dot = "deepseek/deepseek-v4-flash", "deepseek/deepseek-v4.1-flash"
	p := &Policy{Version: Version, Budget: Budget{Mode: "enforce"}, Prices: Prices{
		slash: {InputPerM: rate(1), OutputPerM: rate(2)},
		dot:   {InputPerM: rate(3), OutputPerM: rate(4), CacheRead: rate(0.5)},
	}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}

	// What the node holds: every member name is one RTDB accepts.
	var doc struct {
		Prices map[string]json.RawMessage `json:"model_prices"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Prices) != 2 {
		t.Fatalf("the node's model_prices has %d members, want 2: %s", len(doc.Prices), raw)
	}
	for key := range doc.Prices {
		if strings.ContainsAny(key, ".$#[]/") {
			t.Errorf("the node's key %q is one RTDB refuses (a key may not contain . $ # [ ] /): the write would answer 400 and store nothing: %s", key, raw)
		}
	}
	if _, ok := doc.Prices[slash]; ok {
		t.Errorf("the node holds the model ID %q as a key: RTDB refuses it", slash)
	}
	if _, ok := doc.Prices[escKey(slash)]; !ok {
		t.Errorf("the node does not hold %q: %s", escKey(slash), raw)
	}

	// And it reads back as the model IDs the operator priced.
	got, found, err := Decode(raw)
	if err != nil || !found {
		t.Fatalf("Decode(%s) = (%v, %v, %v)", raw, got, found, err)
	}
	for _, id := range []string{slash, dot} {
		if _, ok := got.Prices[id]; !ok {
			t.Errorf("the decoded policy has no entry for %q: %v", id, got.Prices)
		}
	}
	if v := got.Prices[dot].CacheRead; v == nil || *v != 0.5 {
		t.Errorf("the entry written for a dot-carrying ID did not come back whole: %+v", got.Prices[dot])
	}
}

// TestModelPricesKeysAreEscapedOnTheNodeChangesNothingElse is the other half:
// the catalogue's own parser reads the decoded block, and it is what
// internal/runner prices a run with (policyPrices), so the decoded map must be
// the document the operator wrote. CatalogJSON is that document.
func TestModelPricesCatalogJSON(t *testing.T) {
	const id = "deepseek/deepseek-v4-flash"
	ps := Prices{id: {InputPerM: rate(1), OutputPerM: rate(2)}}
	raw, err := ps.CatalogJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"`+id+`"`) {
		t.Errorf("CatalogJSON = %s, want the model ID as the key", raw)
	}
	o, err := pricing.ParseOverrides(string(raw))
	if err != nil {
		t.Fatalf("the catalogue's own parser refused the block: %v", err)
	}
	if _, ok := o[id]; !ok {
		t.Errorf("ParseOverrides = %v, want an entry for %q", o, id)
	}
}

// TestModelPricesCatalogJSONOfAnEmptyBlock: a block with no model in it is an
// empty object, as the doc says and as the node's own encoding (MarshalJSON)
// writes it. json.Marshal of a nil map is the literal `null`, and the
// catalogue's own parser refuses that with "it must be a JSON object keyed by
// model ID" — so the two spellings of one block have to agree, or the first
// caller that does not guard the call (both of today's callers do: Decode's
// isNull check and policyOverrides' len == 0) is handed a document the reader
// it is written for cannot read.
//
// Mutation (verified, run and restored): make CatalogJSON return
// json.Marshal(map[string]Price(ps)) again — the nil case fails on "null".
func TestModelPricesCatalogJSONOfAnEmptyBlock(t *testing.T) {
	for name, ps := range map[string]Prices{"nil": nil, "empty": {}} {
		raw, err := ps.CatalogJSON()
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "{}" {
			t.Errorf("%s: CatalogJSON = %s, want {}", name, raw)
		}
		if _, err := pricing.ParseOverrides(string(raw)); err != nil {
			t.Errorf("%s: the catalogue's own parser refused the block: %v", name, err)
		}
		node, err := json.Marshal(ps)
		if err != nil {
			t.Fatal(err)
		}
		if string(node) != string(raw) {
			t.Errorf("%s: the node's own encoding is %s and CatalogJSON is %s, want the same document", name, node, raw)
		}
	}
}

// TestModelPricesReadsAHandWrittenKey: a node written by hand — an
// administrator with IAM credentials can write one, and a person spells a
// model ID the way fugaro.yaml does — stays readable. Only "%" starts an
// escape, so a key with no escape in it is read as the ID it spells, and an
// administrator who writes a slash-free ID by hand is not made to escape it.
func TestModelPricesReadsAHandWrittenKey(t *testing.T) {
	const id = "claude-sonnet-4-5-20250929@20250929" // a model ID with no "." or "/" in it
	p, found, err := Decode([]byte(`{"version": 1, "model_prices": {"` + id + `": {"input_per_m": 1, "output_per_m": 2}}}`))
	if err != nil || !found {
		t.Fatalf("a hand-written node was refused: %v", err)
	}
	if _, ok := p.Prices[id]; !ok {
		t.Errorf("Prices = %v, want the ID as it was written", p.Prices)
	}
	// An escaped key is read as what it escapes, and the two spellings of the
	// same model ID land on the same entry rather than on two.
	escaped, _, err := Decode([]byte(`{"version": 1, "model_prices": {"` + escKey("deepseek/deepseek-v4.1-flash") + `": {"input_per_m": 1, "output_per_m": 2}}}`))
	if err != nil {
		t.Fatalf("an escaped hand-written key was refused: %v", err)
	}
	if _, ok := escaped.Prices["deepseek/deepseek-v4.1-flash"]; !ok {
		t.Errorf("Prices = %v, want the escaped key read back as the model ID", escaped.Prices)
	}
	// A bad escape is an error, not a silently different model ID.
	if _, _, err := Decode([]byte(`{"version": 1, "model_prices": {"a%2": {"input_per_m": 1, "output_per_m": 2}}}`)); err == nil {
		t.Error("a truncated escape was accepted")
	}
	if _, _, err := Decode([]byte(`{"version": 1, "model_prices": {"a%zz": {"input_per_m": 1, "output_per_m": 2}}}`)); err == nil {
		t.Error("a bad escape was accepted")
	}
}

func rate(v float64) *float64 { return &v }
