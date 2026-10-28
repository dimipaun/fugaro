package runstore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/tier"
)

// TestTierRecorded pins the record's tier block by exact string: the tier the
// run was dispatched at and the expectation that tier resolved, which is what
// `fugaro runs ls`, `fugaro diagnose` and the console read through the same
// struct (docs/task-governance/implementation-plan.md T8.1, D330).
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
// figures are clamped to the bounds a real run's could fall in, so a tampered
// record cannot put a number of its own choosing in a report.
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
	over := &TierRecord{Tier: tier.Large, ExpectedUSD: MaxTierExpectedUSD + 1, ExpectedMinutes: MaxTierExpectedMinutes + 1}
	if got := over.Bounded(); got == nil || got.ExpectedUSD != MaxTierExpectedUSD || got.ExpectedMinutes != MaxTierExpectedMinutes {
		t.Errorf("an over-large block bounds to %+v, want both figures at their bounds", got)
	}
	under := &TierRecord{Tier: tier.Small, ExpectedUSD: -1, ExpectedMinutes: -1}
	if got := under.Bounded(); got == nil || got.ExpectedUSD != 0 || got.ExpectedMinutes != 0 {
		t.Errorf("a negative block bounds to %+v, want both figures at zero", got)
	}
}
