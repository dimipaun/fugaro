package credsvc

import (
	"context"
	"net/http"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// identityRoute is the one door the five identity routes are answered at (C1,
// R2). ServeHTTP mounts it and the ahead-of-the-shared-parse predicate names
// the five paths, because their caller holds no run identity: a ticket is asked
// for before the run exists and the claim is the run's first call, so
// RequestFromHTTP would refuse the call for the missing identity headers before
// any class read its token. The route's own class and its own body are read by
// the handler behind this door, which is why the door is one and not five.
//
// THIS FILE IS THE STUB, and I1 deletes it: until the issuer is wired in, every
// one of the five answers the contract's "not served" (R7) whatever the
// configuration holds, because there is no signer to issue with and no store to
// write one in. It is what each release from this one on gives an installation
// whose roots carry no issuer row yet, and it is what the red step of I1 fails
// against.
//
// The refusal writes no audit line, and that is the rule and not an omission:
// no caller's identity was verified here — there is nothing to verify one
// against, since no issuer is configured — and the audit is written once a
// caller IS verified (audit.go).
func (s *Server) identityRoute(_ context.Context, w http.ResponseWriter, _ *http.Request) {
	s.refuse(w, StatusUnavailable, identitywire.RefusalNotServed,
		"this service does not issue a run's identity: no issuer is configured on this installation, so the identity routes answer 'not served'. A launcher of this release signs the run's identity locally, as it did before an issuer existed; a launcher on an installation that has already moved the grant cannot, and refuses with the fix (run fugaro update)")
}
