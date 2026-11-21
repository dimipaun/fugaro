package workspace

// C2's stubs: one value per interface of the member-operations contract, so that
// the shapes in memberops.go are implementable and a caller written before the
// concrete sides exist has something to hold. I1 deletes this file -- and
// memberops_stub_test.go, which is its test and nothing else's -- in the change
// that adapts WU2, WU4, WU9, WU12, WU15, WU20 and WU22 onto the contract, so no
// release carries a stub and none is ever wired to a live member.
//
// **A stub answers the empty truth and never a made-up one.** Every method
// returns the zero value of every type it declares: nothing is recorded, nothing
// is written, nothing is held, nothing was measured. That is the honest answer of
// a value nothing is behind -- and it is deliberately not a refusal, because a
// refusal is a behaviour (a message, a code, a branch in a caller) that a
// consumer could come to depend on, and a shape test would then be pinning a
// decision the contract never made. What it is NOT is a claim that the truth was
// checked: a caller that reads a report out of a stub is reading "nobody has
// looked", which is why a task's own tests use fakes declared in its own
// `_test.go` files (the plan's conventions) and why this file does not survive
// I1.

import (
	"context"
	"time"
)

// StubMemberOps implements MemberOps with the empty truth: no grants, no mirror,
// no bindings, nothing pending, nothing swept, no entry filed.
type StubMemberOps struct{}

var _ MemberOps = StubMemberOps{}

func (StubMemberOps) ReadGrants(context.Context, MemberTarget) (MemberDoc, error) {
	return MemberDoc{}, nil
}

func (StubMemberOps) WriteGrants(context.Context, MemberTarget, MemberDoc, []byte) error {
	return nil
}

func (StubMemberOps) ReadMirror(context.Context, MemberTarget) (MemberDoc, error) {
	return MemberDoc{}, nil
}

func (StubMemberOps) WriteMirror(context.Context, MemberTarget, MemberDoc, []byte) error {
	return nil
}

func (StubMemberOps) Bindings(context.Context, MemberTarget) ([]MemberBinding, error) {
	return nil, nil
}

func (StubMemberOps) PendingApply(context.Context, MemberTarget, ApplyWant) (Pending, error) {
	return Pending{}, nil
}

func (StubMemberOps) Offboard(context.Context, MemberTarget, string) error { return nil }

func (StubMemberOps) Audit(context.Context, MemberTarget, string, []byte) error { return nil }

// StubAuthority implements Authority with the empty truth: no mechanism, no link
// made or dropped, nothing held, no admin bound.
type StubAuthority struct{}

var _ Authority = StubAuthority{}

// Kind is the empty kind, which is what the contract declares for an authority
// with no mechanism: a workspace whose `init` has not run has none, and the
// contract names no value for that state because every method here is a no-op
// anyway. A command that reads a kind off its authority must therefore handle
// the empty one rather than assume the two constants are the whole set.
func (StubAuthority) Kind() AuthorityKind { return "" }

func (StubAuthority) Link(context.Context, MemberTarget) error   { return nil }
func (StubAuthority) Unlink(context.Context, MemberTarget) error { return nil }

func (StubAuthority) Holds(context.Context, MemberTarget) (bool, error) { return false, nil }

func (StubAuthority) AddAdmin(context.Context, string) error    { return nil }
func (StubAuthority) RemoveAdmin(context.Context, string) error { return nil }

func (StubAuthority) AdminBindings(context.Context) ([]string, error) { return nil, nil }

// StubStateWriter implements StateWriter with the empty truth: nothing saved and
// nothing ever saved before.
type StubStateWriter struct{}

var _ StateWriter = StubStateWriter{}

func (StubStateWriter) Save(context.Context, Snapshot) (SnapshotWritten, error) {
	return SnapshotWritten{}, nil
}

func (StubStateWriter) LastSaved(context.Context, Snapshot) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

// StubCredentialProbe implements CredentialProbe with the empty truth: no
// credential read, and so nothing to report about any of them.
type StubCredentialProbe struct{}

var _ CredentialProbe = StubCredentialProbe{}

func (StubCredentialProbe) Probe(context.Context, MemberTarget) ([]CredentialResult, error) {
	return nil, nil
}

// StubDrift is Drift's stub: the empty report, which says nothing differs
// because nothing was measured. It is a function and not a struct, because Drift
// is a function type; the assertion below is what holds its signature to the
// contract's, which is the whole of what a stub of a function type can check.
func StubDrift(context.Context, MemberTarget) (DriftReport, error) {
	return DriftReport{}, nil
}

var _ Drift = StubDrift
