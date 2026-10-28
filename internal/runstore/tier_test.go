package runstore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/tier"
)

// TestTierRecorded pins the record's tier block by exact string: the tier the
// run was dispatched at and the expectation that tier resolved
// (docs/task-governance/implementation-plan.md T8.1, D330). Nothing reads the
// block yet — no runview.Row key projects it, so `fugaro runs ls`, `fugaro
// diagnose` and the console, which read that projection, show no tier — and
// the exact string is what pins the wire format a reader will be written
// against.
func TestTierRecorded(t *testing.T) {
	rec := Record{Tier: &TierRecord{Tier: tier.Small, ExpectedUSD: 1, ExpectedMinutes: 30}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tier":`) {
		t.Fatalf("the record does not carry the block under %q: %s", "tier", data)
	}
	block, err := json.Marshal(rec.Tier)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"tier":"small","expected_usd":1,"expected_minutes":30}`
	if string(block) != want {
		t.Errorf("the tier block marshals as\n%s\nwant\n%s", block, want)
	}
	// An untiered run records no block at all — never one with an empty tier —
	// so a record from before this field existed is the record an old reader
	// saw, and a reader can tell "no expectation" from "a tier nobody named".
	bare, err := json.Marshal(Record{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bare), "tier") {
		t.Errorf("a record of an untiered run carries a block anyway: %s", bare)
	}
}

// TestTierRecordBoundedRefusesABlockItCannotRead: the record is an object the
// job's service account can write, so the block read back is not trusted to be
// the one a run resolved. A tier outside the vocabulary is not a decision
// anybody made, and nil is the answer the field's absence gives; the two
// figures are floored at zero, which no configuration could have written, and
// left uncapped, because the project layer may loosen a tier past anything
// Fugaro ships and a reader that clamped it would report a figure the run
// never resolved.
func TestTierRecordBoundedRefusesABlockItCannotRead(t *testing.T) {
	var nilBlock *TierRecord
	if got := nilBlock.Bounded(); got != nil {
		t.Errorf("a nil block bounds to %+v, want nil", got)
	}
	for _, tt := range []tier.Tier{"", "Trivial", "SMALL", "small ", "huge"} {
		b := &TierRecord{Tier: tt, ExpectedUSD: 1, ExpectedMinutes: 30}
		if got := b.Bounded(); got != nil {
			t.Errorf("tier %q bounds to %+v, want nil: the vocabulary is closed", tt, got)
		}
	}
	kept := &TierRecord{Tier: tier.Large, ExpectedUSD: 8, ExpectedMinutes: 240}
	if got := kept.Bounded(); got == nil || *got != *kept {
		t.Errorf("a readable block bounds to %+v, want it unchanged (%+v)", got, kept)
	}
	// A figure larger than anything Fugaro ships is a value the project layer
	// may legally write, so it is shown as it is: the block was written by a
	// run that resolved it, and no reader knows better what it resolved.
	big := &TierRecord{Tier: tier.Large, ExpectedUSD: 5000, ExpectedMinutes: 20000}
	if got := big.Bounded(); got == nil || *got != *big {
		t.Errorf("a large but legal block bounds to %+v, want it unchanged (%+v)", got, big)
	}
	under := &TierRecord{Tier: tier.Small, ExpectedUSD: -1, ExpectedMinutes: -1}
	if got := under.Bounded(); got == nil || got.ExpectedUSD != 0 || got.ExpectedMinutes != 0 {
		t.Errorf("a negative block bounds to %+v, want both figures at zero", got)
	}
}
