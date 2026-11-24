package credsvc

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
)

// TestStubPersonalReaderOffersNothing is C2's red step and its whole test. The
// stub reader exists so that a side written against this contract in parallel
// with WC5 — WC4's scope, and every test either of them runs — compiles and
// runs before the real reader exists, and the one thing it must never do is let
// one of them pass on a candidate nothing selected. So every assertion below is
// an assertion that it decided nothing: it offers no candidate, it refuses no
// call, and it answers the same thing whatever the slip is.
//
// The two readers it asks are the two ways a side gets one: the exported
// StubPersonalReader and the constructor's body, which is what NewPersonalReader
// answers with until WC5 assigns its own. The constructor is asserted to be
// non-nil rather than to be the stub, because WC5's assignment is what must
// stand once WC5 lands (the stub's init yields to it), and a test that held the
// constructor to the stub would be a test WC5 could not pass.
//
// Mutation (run, restore): make stubPersonalReader.Personal answer found=true
// with a credential. A candidate the stub answered is one the offering would put
// first (R7) — a credential that nothing read, on a call that was served the
// shared entries before this half existed — and this fails on it.
func TestStubPersonalReaderOffersNothing(t *testing.T) {
	ctx := context.Background()
	if NewPersonalReader == nil {
		t.Fatal("NewPersonalReader answered nothing: the constructor exists so that WC4 builds its scope before WC5's reader is written, and a nil one is a call that panics instead of a reader that offers nothing")
	}
	built, err := stubNewPersonalReader(PersonScopeConfig{})
	if err != nil {
		t.Fatalf("the stub constructor refused a configuration: %v — a configuration error is the real constructor's to name, and being built with none is the state a workspace that serves no grant is in (R8)", err)
	}
	if built == nil {
		t.Fatal("the stub constructor answered no reader: the sides compile against the constructor's answer, so it must be a reader and not a nil")
	}
	slips := []creds.Slip{
		// A slip as the workspace's own grant would write one: an owner, the
		// run it is for, and one personal name in its allowance — names and
		// never values (G9), and an id in the shape the service mints.
		{
			ID:      strings.Repeat("ab", 16),
			Owner:   "104729113355",
			Slug:    "acme/site",
			Run:     "run-1",
			Allowed: []string{"fugaro_u_104729113355_anthropic-api-key"},
		},
		// And the zero slip, which names no owner and no credential at all: the
		// answer must not depend on what the slip holds.
		{},
	}
	for _, reader := range []PersonalReader{StubPersonalReader(), built} {
		for _, sl := range slips {
			c, found, err := reader.Personal(ctx, sl)
			if err != nil {
				t.Fatalf("Personal(%+v) refused the call: %v — the stub holds no reader of the roster, the switch, the policy or a container, so it has no read to fail", sl, err)
			}
			if found {
				t.Fatalf("Personal(%+v) offered the candidate %+v: a candidate the stub answered is one the offering would put first on a call that was served the shared entries before this half existed (R7, R8)", sl, c)
			}
			if c != (creds.Credential{}) {
				t.Fatalf("Personal(%+v) answered found=false with the credential %+v: an absence carries no candidate at all", sl, c)
			}
		}
	}
}
