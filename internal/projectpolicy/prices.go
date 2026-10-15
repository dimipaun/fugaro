package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// UnmarshalJSON reads the block off a node: one member per model, unescaped
// back into the model ID. Each entry is decoded with DisallowUnknownFields, as
// the block always was — a misspelt rate is an error rather than a price that
// quietly takes the list default — and the entry's model ID is in the message,
// so a refusal names the model rather than only the field.
func (ps *Prices) UnmarshalJSON(raw []byte) error {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	out := make(Prices, len(entries))
	for key, entry := range entries {
		id, err := unescKey(key)
		if err != nil {
			return fmt.Errorf("%q: %w", key, err)
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

// CatalogJSON renders the block under the catalogue's own spelling — the real
// model IDs — which is what pricing.ParseOverrides reads and what a reader
// outside RTDB expects. A nil block is an empty object.
func (ps Prices) CatalogJSON() ([]byte, error) {
	return json.Marshal(map[string]Price(ps))
}
