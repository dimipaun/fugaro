package credsvc

import (
	"context"

	"github.com/dimipaun/fugaro/internal/creds"
)

// This file is the scaffolding of C2, and it is the one part of this contract
// that goes away: I1 wires the sides and deletes this file and its test
// (scope_person_stub_test.go), which is why everything here is a stub and
// nothing a side keeps is.
//
// What it is for is the two sides of the contract being written at once. WC4
// builds the workspace's scope through NewPersonalReader while WC5 — which owns
// that constructor's body, in personal_workspace.go — is still being written, so
// the constructor has to answer before WC5 exists; and the answer it must give
// in the meantime is the one that changes nothing about how the service behaves
// today. So the stub reader offers no personal candidate at all, which is what
// every call is served until the personal half lands (R8: the shared entries the
// call is admitted to), and it reads no roster, no switch, no policy, no slip
// and no container.
//
// **A stub returns the shape it is named for and decides nothing**, which is
// the rule the other scaffolding in this tree follows (C1's scope_stub.go,
// internal/cli's after_write_stub.go, internal/mobilesession's stub.go). The one
// place it departs from C1's stub is the direction of its answer, and the reason
// is the contract's own rule rather than a preference: C1's stub READERS refuse
// because a reader that answered a value would let a caller pass on a decision
// nothing made, while a personal candidate's absence is not a decision at all —
// it is the state every call is in today, and the offering's own rule for a call
// with no personal candidate is to serve what the call was admitted to (R8).
// Refusing here would stop every member's run the moment the reader was wired,
// over a credential the service was never asked to serve.

// StubPersonalReader answers the reader this contract ships until WC5 lands: one
// that offers nothing, for every call, whatever the slip is.
func StubPersonalReader() PersonalReader { return stubPersonalReader{} }

// stubPersonalReader is the reader that offers nothing: it holds no roster, no
// launcher verifier, no slip store and no clock, and it answers the zero
// credential with found=false for every slip it is handed.
type stubPersonalReader struct{}

// Personal answers "no personal candidate", for every call, with no error: it
// is the one answer that leaves the service behaving exactly as it does today
// (see the file's own comment). It reads no field of the slip, so two calls
// with two different owners and two different allowances get the same answer —
// which is what makes it a stub and not a policy.
func (stubPersonalReader) Personal(context.Context, creds.Slip) (creds.Credential, bool, error) {
	return creds.Credential{}, false, nil
}

// stubNewPersonalReader is the constructor's body until WC5 supplies the real
// one. It refuses no configuration, and that is deliberate: a configuration
// error is the real constructor's to name, and this function exists so that a
// process built through the constructor serves no personal candidate rather
// than failing to start — a failure to start would be a worse answer to a
// workspace whose personal half is not written yet.
func stubNewPersonalReader(PersonScopeConfig) (PersonalReader, error) {
	return StubPersonalReader(), nil
}

// The assignment is the whole of how the constructor answers before WC5 exists,
// and WC5's own file replaces it.
//
// **The guard is not decoration, and it is the file-name order that makes it
// necessary.** The toolchain presents a package's files in name order, so
// personal_workspace.go (WC5) is initialized BEFORE scope_person_stub.go, and an
// unconditional assignment here would be the one that stands — WC5's reader
// would be built and then never reached. C1's stub can assign unconditionally
// because its side's file (scope_workspace.go) sorts after it; this one cannot,
// for the same reason read the other way round. So the stub yields to an
// assignment another file already made, and what it answers is the state before
// WC5: a constructor that is nil is answered, and a real one is left alone.
// Deleting this file (I1) is therefore a deletion and not an edit.
func init() {
	if NewPersonalReader == nil {
		NewPersonalReader = stubNewPersonalReader
	}
}
