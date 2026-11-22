package creds

// The pools contract (C2), pinned on the half of it this package declares: the
// grant's two new members (SlipChain, CS14) and the picker's signature
// (PoolPicker, PL3).
//
// It is the other half of `TestPoolShapesAreStable` in
// internal/routing/pools_test.go, and it carries the same name on purpose: the
// contract's shapes are pinned by one test in each package that declares them.
// The routing half holds the pool's own names, this one the grant's, because
// the two members are declared HERE (slip_chain.go) and a file outside this
// package may not reach them: importing internal/creds is the deliberate
// allow-list internal/creds/boundary_test.go keeps, and internal/routing is not
// on it — nor could be in a non-test file, since this package imports that one.
// So the pin lives where the names do, exactly as the chain wire's names are
// pinned in internal/credsvc/chain_wire_test.go.
//
// Each of them is a name a reader outside this binary reads: `sources` and
// `named_pool` are members of the slip the service writes and reads, and the
// picker is the seam CS1 calls inside one pool. A value never changes meaning:
// a member added is fine, a member removed or retyped is a new version of the
// shape that carries it, and this test is what makes that an edit somebody
// sees.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/routing"
)

// TestPoolShapesAreStable pins the grant's two wire names and the picker's
// shape. The pool's own names — the rule, the principal, the threshold, the
// designation, the reference and the listing — are the same test in
// internal/routing/pools_test.go.
func TestPoolShapesAreStable(t *testing.T) {
	t.Run("the grant's two members are the contract's", func(t *testing.T) {
		// The two new members travel as ONE embedded shape (CS14 embeds
		// SlipChain into Slip), so the names are declared once and read as the
		// slip's own members: a rename here is a name the service's reader and a
		// release-behind launcher would have met under the old one, and the pin
		// is the whole object rather than its key set, so a member that joined
		// them is an edit this test sees too.
		//
		// It is deliberately NOT a member called `pool`: the grant records which
		// pool the granted credentials belong to, under that name, and which pool
		// a launch NAMED, under `named_pool` — a launch that named no pool still
		// has a pool, so the two are separate facts (D3176).
		raw, err := json.Marshal(SlipChain{
			Sources:   []GrantSource{{Kind: "launcher-pool"}, {Kind: "pool", Pool: "team"}},
			NamedPool: "team",
		})
		if err != nil {
			t.Fatalf("a grant's chain does not marshal: %v", err)
		}
		const want = `{"sources":[{"kind":"launcher-pool","pool":""},{"kind":"pool","pool":"team"}],"named_pool":"team"}`
		if string(raw) != want {
			t.Errorf("the grant's chain is %s, want %s", raw, want)
		}
	})

	t.Run("the picker is the shape CS1 calls", func(t *testing.T) {
		// The signature is pinned at compile time by a function shaped as the
		// interface: a rule whose answer moved — a second return, a member where
		// a wait belongs — fails to satisfy PoolPicker here rather than being
		// found by a caller at run time. The Duration is the WAIT until a member
		// is usable and never a hold time, which is what the interface's own
		// comment says and why it is not a lease's TTL.
		var _ PoolPicker = poolPickerFunc(nil)
	})
}

// poolPickerFunc is a function shaped as the contract's picker: it takes the
// reference the lookup answered (rule and threshold already resolved), the run
// the call is for and the pool's members in the order its owner listed them,
// and answers the member to serve the call from with the wait until one is
// usable.
type poolPickerFunc func(context.Context, routing.PoolRef, Run, []Credential) (Credential, time.Duration, error)

func (f poolPickerFunc) PickInPool(ctx context.Context, ref routing.PoolRef, run Run, members []Credential) (Credential, time.Duration, error) {
	return f(ctx, ref, run, members)
}
