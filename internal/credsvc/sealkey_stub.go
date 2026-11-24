package credsvc

// This file is the one part of the seal-key wire that goes away: the three
// stubs C1 ships, which answer every call the way that can never release a key.
// I1 deletes this file AND sealkey_stub_test.go, its own wiring replaces them,
// and the deletion is a deletion rather than an edit: every test that is about
// the wire's rules rather than about the stub lives in sealkey_wire_test.go,
// which stays.
//
// What they are for is the contract arriving before its implementations. BR2's
// route, BR3's link, BR4's fetch and BR17's registration are written against
// these signatures, and the signatures have to answer before any of them
// exists. The answers they give in the meantime are the ones that can never
// serve a caller by accident: no VM is its job's, no ciphertext opens, and no
// digest is registered — so a side written against C1 runs its own tests
// against a real signature and no side can pass on a key that nothing released.
//
// **They decide nothing.** They read no token, no KMS key, no record and no
// cloud: the arguments are not looked at, and two calls with two different VMs
// get the same answer. That is the stub rule this tree follows
// (provider_wire_stub.go, internal/cli's after_write_stub.go).

import (
	"context"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// stubLink is the link until BR3 supplies the real one: no VM is a VM of any
// job. It is a type and not a bare function because JobLink is an interface —
// the sides implement it, they do not replace it.
//
// It answers false rather than an error, which is this wire's fail-closed
// direction read correctly: "this VM is not that job's" is an ANSWER about a
// VM, and the route turns it into ErrSealKeyNotReleased — the refusal every
// launch of a stub build must meet. The error arm is for a read that FAILED
// (an unreadable token, an unreachable API), and a stub that used it would make
// every side invent a sentence for a failure that is really a verdict.
type stubLink struct{}

func (stubLink) VMsJob(context.Context, VMIdentity, string, time.Time) (JobVM, bool, error) {
	return JobVM{}, false, nil
}

// stubOpener is the opener until BR2 supplies the real one: nothing opens.
//
// It has no false arm — Open's signature has two results and not three — so its
// refusal is the error, and it is the service's own unreadable-state refusal
// rather than ErrSealKeyNotReleased: the route answers a caller
// ErrSealKeyNotReleased either way, and a stub that claimed KMS had refused a
// ciphertext would be claiming a measurement nothing made.
type stubOpener struct{}

func (stubOpener) Open(context.Context, []byte) ([]byte, error) { return nil, ErrUnavailable }

// stubRegistrations is the registration reader until BR17 supplies the real
// one: nothing is registered, so nothing releases.
//
// It answers false rather than an error, for stubJobLink's reason: an empty
// registration is the state every build with no registration route is in, and
// it is the answer that admits no one.
type stubRegistrations struct{}

func (stubRegistrations) Entry(context.Context, string, [token.WrappedDigestSize]byte) (Registration, bool, error) {
	return Registration{}, false, nil
}

// The assignments stand only while nothing else has made one.
//
// The guard is not decoration, and this is the case it exists for: the files
// that assign the real sides — sealkey.go (BR2), joblink.go (BR3) and
// sealkey_register.go (BR17) — all sort BEFORE this one, and the toolchain
// presents a package's files in name order, so an unguarded init here would run
// LAST and put the stubs back over the real sides. Asking whether each variable
// is still nil makes the stub what it is meant to be whatever the other files
// are called: the answer that stands until a real one exists, and never one
// that stands over a real one.
func init() {
	if SealKeyJobLink == nil {
		SealKeyJobLink = stubLink{}
	}
	if SealKeyOpener == nil {
		SealKeyOpener = stubOpener{}
	}
	if SealKeyRegistry == nil {
		SealKeyRegistry = stubRegistrations{}
	}
}
