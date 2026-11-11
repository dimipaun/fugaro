package projectpolicy

import (
	"slices"
	"testing"
)

// TestABinariesNodeIsNotReadYet is the state this release ships: `binaries` is
// a member the node's decoder does not know, so a node that writes one has it
// listed in UnknownKeys and carries no rule. That is the strict end — the run
// admits no binary — and it is what keeps a block in force from being one
// nothing checked: a node like the one below names `/etc/passwd`, `..`, an
// empty entry and `**`, and a cap of -5, none of which D982's pattern checks,
// D983's cap or the count and length bounds would let through.
//
// The version bump to 5 landed with the pools' owner marker (the 0.22.0
// release), ahead of this block, so the node below is written at the current
// version — and the tripwire below pins that version: 5 is taken, so AR0's own
// bump is 5 → 6, the step it adds to migrate.go beside migrateV4ToV5, and the
// row it adds to VersionSince. The decode and its validation are the plan's AR0
// and land with the key, beside the member-name check every written block in
// this package has (a node spelling `Allow` beside `allow` is one
// encoding/json folds onto the other, so the last of the two would be the rule
// read). When that lands, this test goes: it is the wrong direction of AR0's
// own TestPolicyUnknownKeysStillListsBinariesForAnOlderReader, where the reader
// that lists the key is a version-5 binary and the node is a version-6 one.
func TestABinariesNodeIsNotReadYet(t *testing.T) {
	if Version != 5 {
		t.Fatalf("the policy Version is %d: this test is about the release before the block is read — AR0 lands the decode, its validation and the bump to version 6, with the 5 → 6 step in migrate.go, and deletes this test with them", Version)
	}
	p, found, err := Decode([]byte(`{"version": 5, "binaries": {"allow": ["/etc/passwd", "..", "", "**"], "max_bytes": -5}}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the node read with the key listed", p, found, err)
	}
	if p.Binaries != nil {
		t.Errorf("Binaries = %+v, want none: nothing validates a block here, so nothing may read one", p.Binaries)
	}
	if want := []string{"binaries"}; !slices.Equal(p.UnknownKeys, want) {
		t.Errorf("UnknownKeys = %v, want %v: a member this reader does not know is listed, not dropped (P8)", p.UnknownKeys, want)
	}
}
