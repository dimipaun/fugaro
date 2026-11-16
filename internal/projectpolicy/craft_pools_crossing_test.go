package projectpolicy

// The crossing at the node's own read (plan MC20, reconciliation D1668,
// D1673), and the reason the refusal is NOT here yet: `Decode` runs
// `config.ValidatePublishedPools`, so a rule of that function is a rule of the
// runner's `Read`, of the credentials service's per-call re-read, of `doctor`
// and of `update` itself — every reader of the node at once. A node written by
// 0.25.0 may already carry a crossing pool, because that release's `accounts
// pool add` wrote one and its `accounts pool ls` read it as valid, so a reader
// that refused it would refuse a node no step of this release can repair: a
// broken installation, which CLAUDE.md's HARD RULE forbids, and `fugaro update`
// is one of those readers.
//
// MC21 is that step — a version bump of the policy chain whose migration splits
// `craft_pools` on the node, one pool per half, and repoints the crafts whose
// access named the split pool — and it moves the refusal into
// `ValidatePublishedPools` in the same change that lands it, in this refusal's
// own release (the activation plan's row 43). Until then the rule is the door a
// NEW pool is written through (`config.ValidatePoolPaths`), and this test is
// what fails if it is moved into the reader first: it reads a node at THIS
// binary's version that carries a crossing, which is the node an installation
// at 0.25.0 published.

import (
	"fmt"
	"testing"
)

// TestTheNodeReadsACrossPathPoolUntilItsSplitLands pins the staging between the
// refusal and the migration: a node carrying a pool that names a member of each
// half is read, and the members are carried through as the node wrote them.
//
// The version is read from Version rather than spelled, because what the test is
// about is the reader's own rule and not a step of the chain: MC21's step is the
// one that converges such a node, and this test is replaced by its own the day
// the rule moves into ValidatePublishedPools.
func TestTheNodeReadsACrossPathPoolUntilItsSplitLands(t *testing.T) {
	const creds = `"craft_credentials":[{"name":"openrouter-x","type":"openrouter"},{"name":"anthropic-y","type":"anthropic"}],`
	raw := fmt.Sprintf(`{"version":%d,`+creds+`"craft_pools":[{"name":"mixed","owner":"owner@example.com","members":["openrouter-x","anthropic-y"]}]}`, Version)

	p, found, err := Decode([]byte(raw))
	if err != nil || !found || p == nil {
		t.Fatalf("a node written by an earlier release and carrying a cross-path pool was refused: (%+v, %v, %v) — the split that converges it is MC21's, and it is not in this change", p, found, err)
	}
	if len(p.CraftPools) != 1 || len(p.CraftPools[0].Members) != 2 {
		t.Fatalf("the node's pools came back as %+v, want the one pool with its two members", p.CraftPools)
	}
	// The members keep the order the node wrote: nothing here reorders or drops
	// one, which is what the migration's own step is held to (MC21).
	for i, want := range []string{"openrouter-x", "anthropic-y"} {
		if p.CraftPools[0].Members[i] != want {
			t.Errorf("member %d is %q, want %q: the reader carries the node's order through", i, p.CraftPools[0].Members[i], want)
		}
	}
}
