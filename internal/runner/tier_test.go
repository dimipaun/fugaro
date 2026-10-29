package runner_test

import "testing"

// The tier tests that read the shipped expectations moved to ee/governance
// with the policy that now carries them (plan task E2.3). What stays is the
// record's own shape, which is the core's whatever the edition resolves.

// TestRunRecordWithoutATier: a run that carried no tier records no block — the
// field is absent, never a block with an empty tier — so a reader can tell a
// run nobody dispatched an expectation for from one that carries a tier.
func TestRunRecordWithoutATier(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Tier != nil {
		t.Fatalf("the record of an untiered run carries %+v", rec.Tier)
	}
}
