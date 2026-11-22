package credsvc

import (
	"context"

	"github.com/dimipaun/fugaro/internal/routing"
)

// C2's stub lookup: the other side of routing.LauncherPool, before PL4 resolves
// a launcher's pool at all. I1 deletes this file once launcherpool.go holds the
// lookup; nothing but this file, a test and a side written ahead of PL4 names
// it, and no production path keeps it.
//
// **It answers the one thing a side can be written against without a record:**
// no pool is designated, for every principal and every named pool. That is not a
// placeholder invented to fill a signature — it is what a project nothing has
// designated yet resolves to, and it is the answer D3180 fixes for it (the walk
// moves to the chain's next source with the step flagged a fallback) — which is
// what lets CS1 walk a chain and CS12 carry a name on a launch while the store
// that holds designations does not exist yet.
//
// What it does NOT do is decide: it reads no designation, no pool, no policy and
// no entitlement, so a named pool is answered "none" for a launcher that owns it
// and for one that does not alike, and no principal is ever resolved to another
// principal's pool. It answers an ERROR for nothing either — "none" is the whole
// of its answer — so a caller that met it cannot tell "the stub" from "nobody has
// designated a pool", which is exactly the state a project that has not migrated
// is in (PL3's own red step: a pool that is inert today stays inert).
type StubLauncherPool struct{}

var _ routing.LauncherPool = StubLauncherPool{}

// LauncherPool answers found false — no pool designated — and no error, for
// every principal and every named pool: see the file's own comment.
func (StubLauncherPool) LauncherPool(context.Context, routing.Principal, string) (routing.PoolRef, bool, error) {
	return routing.PoolRef{}, false, nil
}
