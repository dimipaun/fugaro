package creds

import (
	"context"
	"time"

	"github.com/dimipaun/fugaro/internal/routing"
)

// The grant's chain (C2): the sources a slip covers and the pool its launch
// named, and the seam the service picks inside one pool through.
//
// # What the slip gains
//
// A slip records one pool and the secrets a run may be served (Pool, Allowed,
// DS3.2). The table makes a call a walk over the sources its model's chain names
// (R4, D3171), so a grant has to record which of those sources this run's
// launcher was granted and which pool `--pool` named (D3176). Both are NEW
// members, and the names are new, because a value never changes meaning:
//
//   - `sources` are the sources the grant covers, as a list of GrantSource;
//   - `named_pool` is the pool the launch asked for, "" when it asked for none.
//
// `Pool` is left alone and is not reused for either of them: it keeps its
// meaning, the pool the granted credentials belong to (the one whose owner and
// allowed users the grant checked), which is not the same fact as the pool a
// launch named — a launch with no `--pool` has no named pool and still has a
// pool if its launcher designated one (PL4, D3180).
//
// They are members of ONE embedded shape rather than two loose fields on the
// slip, because the record's own file (slips.go) is CS14's to write them into
// and the names must be declared once: CS14 embeds SlipChain into Slip, which
// is what flattens these two into the record the service writes and reads, and
// no second place spells `sources` or `named_pool`. The file is strict-decoded
// like the rest of the slip (decodeStrict), so a member this release does not
// know is refused rather than carried along — which is why the two are additive
// and why an older grant request, which names no sources, is served as today:
// an absent `sources` is a grant that covers nothing new, and CS14 decides what
// that means for a release-behind launcher.
//
// # What the grant covers, and what it does not
//
// A source is covered by its KIND and, where the kind names one, its POOL: the
// table's `launcher-pool` and `pool` sources carry a pool's name, and the kinds
// that carry no pool (`anthropic`, `provider`, `vertex`) are covered by the kind
// alone. The provider, the model and the spelling a call is made in are the
// table's at each call and are not properties of a grant: a grant is one run's,
// and a run's models are fixed at launch, but what each of them resolves to is
// read again per call (CS1). C1's own `Source` type is where the table's shape
// lives; this is the grant's narrower reading of it, and CS14 — which owns both
// halves at once — is where one becomes the other.
//
// # The picker
//
// PickInPool is the signature CS1 calls inside one pool (PL3 implements it,
// PL8 extends it): the pool's own two settings travel in the reference the
// lookup answered (routing.PoolRef.Rule, .Threshold), and the members are the
// ones the walk gathered for that pool.
//
// It is an interface and not a package-level function, and it lives in THIS
// file rather than beside the rule that implements it, for one reason worth
// writing down: the stub the sides are written against until PL3 lands
// (poolrule_stub.go) is a file I1 DELETES, so nothing a side keeps may be
// declared in it — a function or variable declared there would have to be moved
// by the deletion rather than deleted. An interface and a stub TYPE are the two
// halves that can live in different files: the type goes with the deletion, the
// signature stays. It is the same arrangement the package already has for the
// route record (RouteReader in route.go, StubRoute in route_stub.go) and for a
// member's switch (PersonSwitchReader in person.go, StubPersonSwitch in
// person_stub.go).
//
// The two settings being in the reference rather than in the members is what
// keeps the members the same Credential the selection path already carries
// (select.go's own type, which no task of this plan edits), and what keeps a
// member's own figures — the utilisation a threshold moves off — where the
// service records state: the mark (marks.go, PL8).

// GrantSource is one source a grant covers: what kind of source it is (the
// table's own kinds, C1) and, where the kind names one, the pool it is.
type GrantSource struct {
	// Kind is one of the table's source kinds: `launcher-pool`, `pool`,
	// `anthropic`, `provider` or `vertex`.
	Kind string `json:"kind"`
	// Pool is the pool's name for the two kinds that name one, and "" for the
	// kinds that do not. It is a name and never a value (F19).
	Pool string `json:"pool"`
}

// SlipChain is the grant's own additions to a slip: the sources it covers and
// the pool its launch named. It is embedded into Slip (CS14), so these two read
// as the slip's own members, `sources` and `named_pool`.
type SlipChain struct {
	// Sources are the sources this grant covers, in the order the launch read
	// them off the table. An empty list is a grant that covers no source of the
	// new kinds — what a release-behind launcher asks for — and never a grant
	// that covers everything.
	Sources []GrantSource `json:"sources"`
	// NamedPool is the pool the launch asked for with `--pool`, "" when it named
	// none (D3176). It is what the run's chain resolves "the launcher's pool" to
	// for this run (PL4), and it is not Slip.Pool: the grant above records which
	// pool the granted credentials belong to, which a launch that named no pool
	// still has.
	NamedPool string `json:"named_pool"`
}

// PoolPicker picks the member of one pool to serve a call from, by the pool's
// rule (R5, D3177): `ordered` takes the first listed member that is usable,
// `round-robin` turns through them, `failover` takes the one exhausted longest
// ago. It is the signature CS1 calls inside one pool and PL3 implements.
//
// It answers the member to serve from and how long the call may hold it, the
// same pair the selection path answers (Selector.Select), and it answers
// ErrNoCandidates when the pool holds no member that is usable right now — the
// walk treats that as an exhausted source and moves on (D3180), it is never
// "no pool here".
type PoolPicker interface {
	// PickInPool answers the member of this pool to serve one call from. ref is
	// the pool the lookup resolved, so the rule and the threshold are the ones
	// that pool was designated with; run is the run the call is for, which the
	// rules that need one place to turn through need; members are the pool's
	// candidates in the order its owner listed them.
	PickInPool(ctx context.Context, ref routing.PoolRef, run Run, members []Credential) (Credential, time.Duration, error)
}
