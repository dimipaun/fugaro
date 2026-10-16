package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Prices is the model_prices block of a policy: the owner's price overrides,
// keyed by model ID.
//
// It is a map in Go and an object on the node, but its *keys* are not model
// IDs on the node. RTDB refuses a key containing "." or "/" — the write is
// rejected whole, with a 400 and nothing stored — and the model IDs the
// documentation uses carry one or the other: the documented
// deepseek/deepseek-v4-flash (docs/multi-model.md) has a slash, and the
// catalog's own deepseek/deepseek-v4.1-flash a dot. A policy node is written
// by the CLI and read by a run, so the block is stored under escKey(id) — the
// same escape a slug gets in config/policy/repos/$slug (paths.go) — and
// unescKey reads it back. The decoded map, which is what every reader in Go
// sees, keeps the catalogue's spelling.
//
// The escape lives in MarshalJSON and UnmarshalJSON rather than in a step a
// writer has to remember: every encoding of a Policy is then a document RTDB
// accepts, and the two directions cannot drift apart. CatalogJSON is the other
// spelling, for pricing.ParseOverrides (the catalogue's own parser, which
// Decode holds this document to) and for anything that shows the block to a
// person.
//
// A key with no escape in it is read as it is, so a node written by hand —
// where a person spells a model ID the way fugaro.yaml does — stays readable;
// the escaped form is only what this package writes.
type Prices map[string]Price

// MarshalJSON writes the block as the node holds it: one member per model,
// under escKey(id). It is a value receiver, so a nil Prices marshals to an
// empty object exactly as a nil map does.
func (ps Prices) MarshalJSON() ([]byte, error) {
	out := make(map[string]Price, len(ps))
	for id, price := range ps {
		out[escKey(id)] = price
	}
	return json.Marshal(out)
}

// priceMembers and priceTierMembers are the member names a price entry and its
// long_context tier are documented with, in the spelling their json tags write:
// the names checkMemberNames holds a written entry to, beside the
// DisallowUnknownFields decode, because that decoder refuses only the names no
// field answers to at all — a rate written INPUT_PER_M answers to input_per_m
// and would otherwise be priced by whichever spelling the document wrote last.
//
// Each list has to carry every field of the struct it describes (Price,
// PriceTier), because the case-variant half of that check fires only for a name
// the list holds: above_input_tokens was missing from the tier's list, so a
// lone ABOVE_INPUT_TOKENS in a tier passed a check whose whole point is that it
// does not (code review, low).
var (
	priceMembers     = []string{"cache_read", "cache_write_1h", "cache_write_5m", "input_per_m", "long_context", "output_per_m", "web_search_per_1k"}
	priceTierMembers = []string{"above_input_tokens", "input_per_m", "output_per_m"}
)

// UnmarshalJSON reads the block off a node: one member per model, unescaped
// back into the model ID. Each entry is decoded with DisallowUnknownFields, as
// the block always was — a misspelt rate is an error rather than a price that
// quietly takes the list default — and the entry's model ID is in the message,
// so a refusal names the model rather than only the field. The member *names*
// are checked first (checkMemberNames, against priceMembers): the decoder
// folds case, so "input_per_m" written twice, or "INPUT_PER_M" beside it,
// is one field to it and the rate this run is priced by would depend on the
// order the keys were written in. The long_context tier is held to the same
// check, for the same reason. The block's own keys are held to a rule of their
// own: one model may be named once, whether it is repeated under one key or
// written under two spellings of its ID (checkNoDuplicateNames, then the
// unescaped IDs), because a model priced twice is priced by whichever of the
// two members the read happened to see last.
func (ps *Prices) UnmarshalJSON(raw []byte) error {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	// The block's own key names, as the document writes them: the map above
	// keeps the last of a repeated key, and one model can also be written
	// under two *different* keys — the escaped spelling this package writes
	// and the literal model ID a person writes by hand both unescape to the
	// same ID. Either way only one of the two prices can survive, so which one
	// this run is billed by (and the ceiling it is compared against) would
	// depend on the order the keys were read in, and one of them was written
	// by someone who meant it.
	names, err := objectMemberNames(raw)
	if err != nil {
		return err
	}
	if err := checkNoDuplicateNames("model_prices", names); err != nil {
		return fmt.Errorf("projectpolicy: %w", err)
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys) // a stable order, so the refusal below names the same pair every time
	out := make(Prices, len(entries))
	spelled := make(map[string]string, len(entries)) // model ID -> the key it was written under
	for _, key := range keys {
		entry := entries[key]
		id, err := unescKey(key)
		if err != nil {
			return fmt.Errorf("%q: %w", key, err)
		}
		if first, dup := spelled[id]; dup {
			return fmt.Errorf("%q and %q are both the model ID %q, and a model may be named once: the price this run is billed by would otherwise depend on the order the block's keys are in", first, key, id)
		}
		spelled[id] = key
		if err := checkPriceMemberNames(entry); err != nil {
			return fmt.Errorf("%q: %w", id, err)
		}
		dec := json.NewDecoder(bytes.NewReader(entry))
		dec.DisallowUnknownFields()
		var price Price
		if err := dec.Decode(&price); err != nil {
			return fmt.Errorf("%q: %w", id, err)
		}
		out[id] = price
	}
	*ps = out
	return nil
}

// checkPriceMemberNames applies checkMemberNames to one price entry and to its
// long_context tier, which is an object of its own and reads the same way.
func checkPriceMemberNames(entry json.RawMessage) error {
	names, err := objectMemberNames(entry)
	if err != nil {
		return err
	}
	if err := checkMemberNames("model_prices", names, priceMembers); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(entry, &members); err != nil {
		return err
	}
	tier, ok := members["long_context"]
	if !ok || IsNull(tier) {
		return nil
	}
	tnames, err := objectMemberNames(tier)
	if err != nil {
		return err
	}
	return checkMemberNames("model_prices.long_context", tnames, priceTierMembers)
}

// CatalogJSON renders the block under the catalogue's own spelling — the real
// model IDs — which is what pricing.ParseOverrides reads and what a reader
// outside RTDB expects. A nil block is an empty object, exactly as MarshalJSON
// writes the node: json.Marshal of a nil map is the literal `null`, which
// pricing.ParseOverrides refuses ("it must be a JSON object keyed by model
// ID"), so a caller that did not guard the call would be handed a document the
// reader it is written for cannot read.
func (ps Prices) CatalogJSON() ([]byte, error) {
	out := make(map[string]Price, len(ps))
	for id, price := range ps {
		out[id] = price
	}
	return json.Marshal(out)
}
