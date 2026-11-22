package creds

import (
	"context"
	"time"

	"github.com/dimipaun/fugaro/internal/routing"
)

// C2's stub picker: the other side of PoolPicker, before PL3's rule exists. I1
// deletes this file once poolrule.go holds the rule; nothing but this file, a
// test and a side written ahead of PL3 names it, and no production path keeps
// it.
//
// **It answers the one thing a side can be written against without a rule:** a
// pool of one member is served by that member under every rule there is, and a
// pool's first member is what `ordered` answers as soon as it is usable. That is
// today's behaviour stated as a value — a pool served in the order it was
// written, which is what a member list with no rule behind it does — and it is
// what lets CS1 walk a chain and PL8 measure utilisation while the rule itself
// does not exist yet.
//
// What it does NOT do is decide: it reads no mark, no lease, no placement, no
// counter and no utilisation figure, it writes none of them, and it takes no
// lease on the member it answers. So a caller cannot pass on a decision nothing
// made — the member it answers is the one the pool's own order already names,
// and a caller that hands it no members at all gets ErrNoCandidates, the seam's
// refusal of an empty member list, rather than a member invented to keep a call
// going. It never reads a member's state, so it never answers what a real rule
// answers for a pool whose members are all unusable (*ExhaustedUntil or
// ErrNoCredential, the interface's own two).
type StubPoolRule struct{}

var _ PoolPicker = StubPoolRule{}

// PickInPool answers the pool's first member, and ErrNoCandidates when it is
// handed no members at all: see the file's own comment.
func (StubPoolRule) PickInPool(_ context.Context, _ routing.PoolRef, _ Run, members []Credential) (Credential, time.Duration, error) {
	if len(members) == 0 {
		return Credential{}, 0, ErrNoCandidates
	}
	return members[0], 0, nil
}
