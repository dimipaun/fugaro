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
// internal/runner prices a run with (policyOverrides), so the decoded map must be
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
	p, found, err := Decode([]byte(`{"version": 4, "model_prices": {"` + id + `": {"input_per_m": 1, "output_per_m": 2}}}`))
	if err != nil || !found {
		t.Fatalf("a hand-written node was refused: %v", err)
	}
	if _, ok := p.Prices[id]; !ok {
		t.Errorf("Prices = %v, want the ID as it was written", p.Prices)
	}
	// An escaped key is read as what it escapes, and the two spellings of the
	// same model ID land on the same entry rather than on two.
	escaped, _, err := Decode([]byte(`{"version": 4, "model_prices": {"` + escKey("deepseek/deepseek-v4.1-flash") + `": {"input_per_m": 1, "output_per_m": 2}}}`))
	if err != nil {
		t.Fatalf("an escaped hand-written key was refused: %v", err)
	}
	if _, ok := escaped.Prices["deepseek/deepseek-v4.1-flash"]; !ok {
		t.Errorf("Prices = %v, want the escaped key read back as the model ID", escaped.Prices)
	}
	// A bad escape is an error, not a silently different model ID.
	if _, _, err := Decode([]byte(`{"version": 4, "model_prices": {"a%2": {"input_per_m": 1, "output_per_m": 2}}}`)); err == nil {
		t.Error("a truncated escape was accepted")
	}
	if _, _, err := Decode([]byte(`{"version": 4, "model_prices": {"a%zz": {"input_per_m": 1, "output_per_m": 2}}}`)); err == nil {
		t.Error("a bad escape was accepted")
	}
}

// A price entry's member names are held to the documented spelling, like the
// budget block's: DisallowUnknownFields refuses a rate written "input_perm",
// and encoding/json matches "INPUT_PER_M" to the same field as input_per_m, so
// the rate a run is priced by would otherwise depend on the order the keys were
// written in. The long_context tier is an object of its own and reads the same
// way.
func TestModelPricesRefusesCaseFoldedMembers(t *testing.T) {
	for name, raw := range map[string]string{
		"a case-folded rate beside the real one": `{"version": 4, "model_prices": {"deepseek/deepseek-v4-flash": {"input_per_m": 5, "INPUT_PER_M": 1000, "output_per_m": 1}}}`,
		"a rate written twice":                   `{"version": 4, "model_prices": {"deepseek/deepseek-v4-flash": {"input_per_m": 5, "input_per_m": 1000, "output_per_m": 1}}}`,
		"a case-folded tier member":              `{"version": 4, "model_prices": {"deepseek/deepseek-v4-flash": {"input_per_m": 5, "output_per_m": 1, "long_context": {"above_input_tokens": 100000, "input_per_m": 2, "INPUT_PER_M": 3, "output_per_m": 4}}}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want a refusal", name, raw, p, found, err)
			continue
		}
		if !strings.Contains(err.Error(), "model_prices") {
			t.Errorf("%s: err = %v, want it to name the block", name, err)
		}
	}
	// The documented spelling, tier and all, is read as before.
	p, found, err := Decode([]byte(`{"version": 4, "model_prices": {"deepseek/deepseek-v4-flash": {"input_per_m": 5, "output_per_m": 1, "long_context": {"above_input_tokens": 100000, "input_per_m": 2, "output_per_m": 4}}}}`))
	if err != nil || !found || p == nil {
		t.Fatalf("a correctly spelled price entry was refused: (%v, %v, %v)", p, found, err)
	}
	price, ok := p.Prices["deepseek/deepseek-v4-flash"]
	if !ok || price.InputPerM == nil || *price.InputPerM != 5 || price.LongContext == nil || *price.LongContext.InputPerM != 2 {
		t.Errorf("Prices = %+v, want the entry as written", p.Prices)
	}
}

// TestModelPricesRefusesACaseFoldedTierMemberAlone: the long_context tier is
// held to the same name check as the entry around it, and that check's own
// rule — a name this Fugaro models, spelled in another case, is refused — only
// fires for a name the list it is handed holds. priceTierMembers carried
// input_per_m and output_per_m but not above_input_tokens, which is the tier's
// third field (projectpolicy.PriceTier), so a lone "ABOVE_INPUT_TOKENS" went
// through: not the fail-open the two-spelling case is (the decoder still
// resolves the field and a second spelling beside it is caught by the fold
// check), but the invariant the function's comment states did not hold, and
// the next writer of that list would have no reason to notice. Listed, the
// spelling is refused like any other.
func TestModelPricesRefusesACaseFoldedTierMemberAlone(t *testing.T) {
	const raw = `{"version": 4, "model_prices": {"deepseek/deepseek-v4-flash": {"input_per_m": 5, "output_per_m": 1, ` +
		`"long_context": {"ABOVE_INPUT_TOKENS": 100000, "input_per_m": 2, "output_per_m": 4}}}}`
	p, found, err := Decode([]byte(raw))
	if err == nil || p != nil || !found {
		t.Fatalf("Decode(%s) = (%v, %v, %v), want a refusal", raw, p, found, err)
	}
	for _, w := range []string{"model_prices.long_context", "above_input_tokens", `"ABOVE_INPUT_TOKENS"`} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("err = %v, want it to name %s", err, w)
		}
	}
}

// TestModelPricesRefusesOneModelPricedTwice: the block is keyed by model ID,
// and the ID is the *unescaped* key, so one model has two spellings on the
// node — the escaped one this package writes and the literal one a person
// writes by hand. A block that carries both is a block whose two prices land
// on one map entry, and which of the two survives is the order the members
// were read in: the run's accounting and the per_run_usd comparison would move
// run to run, from a document nobody edited between them. An exact repeat of
// one key is the same loss one step earlier (the map keeps the last of the
// two), so both are refused and both name what was written.
func TestModelPricesRefusesOneModelPricedTwice(t *testing.T) {
	const id = "deepseek/deepseek-v4.1-flash"
	for name, raw := range map[string]string{
		"the escaped spelling beside the literal one": `{"version": 4, "model_prices": {"` + escKey(id) + `": {"input_per_m": 1, "output_per_m": 2}, "` + id + `": {"input_per_m": 99, "output_per_m": 2}}}`,
		"the literal spelling first":                  `{"version": 4, "model_prices": {"` + id + `": {"input_per_m": 99, "output_per_m": 2}, "` + escKey(id) + `": {"input_per_m": 1, "output_per_m": 2}}}`,
		"the same key twice":                          `{"version": 4, "model_prices": {"deepseek/*": {"input_per_m": 1, "output_per_m": 2}, "deepseek/*": {"input_per_m": 99, "output_per_m": 2}}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("%s: Decode(%s) = (%v, %v, %v), want a refusal", name, raw, p, found, err)
			continue
		}
		if !strings.Contains(err.Error(), "model_prices") {
			t.Errorf("%s: err = %v, want it to name the block", name, err)
		}
	}
	// The same two keys under two different models are two entries and are
	// read: the refusal is the collision, not the spelling.
	raw := []byte(`{"version": 4, "model_prices": {"` + escKey(id) + `": {"input_per_m": 1, "output_per_m": 2}, "deepseek/other": {"input_per_m": 99, "output_per_m": 2}}}`)
	p, found, err := Decode(raw)
	if err != nil || !found || p == nil || len(p.Prices) != 2 {
		t.Fatalf("Decode(%s) = (%v, %v, %v), want both entries", raw, p, found, err)
	}
	// And one model written once is priced at what it says, whichever spelling
	// was used: the escaped form is what this package writes, the literal one
	// is what a person writes.
	for _, key := range []string{escKey(id), id} {
		raw := []byte(`{"version": 4, "model_prices": {"` + key + `": {"input_per_m": 7, "output_per_m": 2}}}`)
		p, found, err := Decode(raw)
		if err != nil || !found || p == nil {
			t.Fatalf("Decode(%s) = (%v, %v, %v), want it read", raw, p, found, err)
		}
		if price, ok := p.Prices[id]; !ok || price.InputPerM == nil || *price.InputPerM != 7 {
			t.Errorf("Prices = %+v, want %s priced at 7 under the catalogue's spelling", p.Prices, id)
		}
	}
}

func rate(v float64) *float64 { return &v }
