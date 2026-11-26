package credsvc

import (
	"context"
	"errors"
	"net/http"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// identity_mount.go is I1: the door the identity contract is answered at
// (C1, R2), bound to the handlers the contract's own tasks wrote.
//
// server.go answers the six contracted paths HERE and ahead of the shared parse
// (identitywire.RouteFor, ServeHTTP), because their callers hold no run
// identity: a ticket is asked for before the run exists and a claim is the
// run's first call, so RequestFromHTTP would refuse both for the missing
// identity headers before any class ever read its token. What is left for this
// file is the three things a door owns and no handler can:
//
//   - the ONE answer of an installation with no issuer. `fugaro update` gives a
//     deployment its signer (LM11, LM12) and every installation without one
//     keeps the bridge R7 leaves it: the contract's "not served", on all six
//     paths, before anything of the call is read and so with no audit line
//     (audit.go's rule starts once a caller IS verified). It is asked here
//     rather than in each handler because it is a property of the INSTALLATION
//     and not of a route, and because a handler reached with a caller the door
//     had already verified owes that caller a line — which is the state an
//     installation with no issuer must never be put in.
//   - the caller's own class, for the two routes whose caller is a launcher
//     (LM3): the ticket ask and the registration it is the first half of.
//   - the runs' signer, for the two routes that mint a phone's token (LM4).
//
// Nothing of the contract is decided here. Every refusal past the caller's
// verification is the handler's (refuseTicket, refuseRegister), which is what
// keeps "exactly one audit line per refusal" a property of one function per
// route rather than of the door.
func (s *Server) identityRoute(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s.cfg.Identity.Signer == "" {
		s.refuseIdentityNotServed(w)
		return
	}
	switch r.URL.Path {
	case identitywire.ClaimPath:
		s.identityClaim(ctx, w, r)
	case identitywire.ProbePath:
		s.identityProbe(ctx, w, r)
	case identitywire.PairingPath:
		s.identityMintRoute(ctx, w, r, s.identityPairing)
	case identitywire.DevicePath:
		s.identityMintRoute(ctx, w, r, s.identityDevice)
	case identitywire.TicketPath, identitywire.JobPath:
		// The two routes a launcher calls, and one caller for both: the
		// registration is the ticket ask's second half and admits the same
		// classes (identitywire.Routes), so the two may not be verified
		// differently.
		caller, ok := s.identityLauncherCaller(ctx, w, r)
		if !ok {
			return
		}
		if r.URL.Path == identitywire.TicketPath {
			s.identityTicket(ctx, w, caller, r)
			return
		}
		s.identityJobRegister(ctx, w, caller, r)
	}
}

// identityMintRoute builds the runs' signer one of the two phone routes is
// answered with (LM4). The signer is built ONCE per call and before the
// handler reads anything: a person's pairing token and a device token are
// signed as the runs' signer and by nobody else (D3126, D3121), so a service
// whose own credential cannot reach that account has nothing to answer with —
// and that is this service's own failure, never the contract's "not served",
// which a launcher would read as the bridge and sign a run's identity locally
// against an installation that has already moved the grant (R7).
//
// The failure is answered here rather than handed to the handler as a nil
// signer, because identityPerson reads a nil signer as "this installation has
// no issuer" and says so — which would be a lie about a deployment that has
// one and cannot use it.
func (s *Server) identityMintRoute(ctx context.Context, w http.ResponseWriter, r *http.Request, h func(context.Context, http.ResponseWriter, *http.Request, token.Signer)) {
	signer, err := identitySignerFor(ctx, s.cfg.Identity.Signer)
	if err != nil {
		// The error is the credential's own and is logged, never answered: a
		// refusal names the field and never what the call sent (the service's
		// own rule), and this one carries the service's credential.
		s.log.Error("credsvc: the runs' signer could not be built", "signer", s.cfg.Identity.Signer, "error", err)
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable,
			"no token was issued: the service could not reach the account the runs' identities are signed as")
		return
	}
	h(ctx, w, r, signer)
}

// identityLauncherCaller verifies the caller of the two routes a launcher calls
// (LM3, R2) and answers the class it was admitted as.
//
// Every refusal here is made BEFORE any caller is verified, which is why none
// of them writes an audit line: the token is missing, the caller's own Google
// ID token is not one of the three classes, or the service cannot read the key
// set it would verify one with — and an audit line names an identity this
// service verified (audit.go). A caller this function DID verify is handed to
// the handler, which owns every line from there on.
func (s *Server) identityLauncherCaller(ctx context.Context, w http.ResponseWriter, r *http.Request) (IdentityCaller, bool) {
	// The caller's classes are the authenticator's three entry points, read as
	// an interface here rather than as a new configuration field: server.go is
	// the contract's and carries no place to hold a fourth reader of the same
	// authenticator, and the value that answers these three IS the
	// authenticator a deployment wires. A service whose authenticator answers
	// none of them is a deployment failure — never "not served", which a
	// launcher reads as the bridge (R7) — and it is answered as one.
	classes, ok := s.cfg.Auth.(CallerClasses)
	if !ok {
		s.log.Error("credsvc: the configured authenticator does not answer the ticket route's classes")
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable,
			"this service cannot verify the caller of a ticket ask or a registration: the authenticator it was deployed with answers none of the three classes those routes admit")
		return IdentityCaller{}, false
	}
	callers, err := NewIdentityCallers(classes)
	if err != nil {
		s.log.Error("credsvc: the ticket route's caller classes could not be read", "error", err)
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, "this service cannot verify the caller of a ticket ask or a registration")
		return IdentityCaller{}, false
	}
	// personBearerFromHTTP and not bearerFromHTTP: the launcher class already
	// reads this header this way (slipGrant's caller), and the two are one
	// class set — a call that reaches the service without a front end in
	// between reads Authorization, and one behind the front end reads the
	// header it forwards the person's own token in.
	tok, err := personBearerFromHTTP(r)
	if err != nil {
		s.refuse(w, StatusUnverified, identitywire.RefusalCallerNotAdmitted, err.Error())
		return IdentityCaller{}, false
	}
	caller, err := callers.TicketCaller(ctx, tok)
	switch {
	case err == nil:
		return caller, true
	case errors.Is(err, ErrCallerNotAdmitted):
		// The class's own refusal, with the contract's code for it: the four
		// classes a caller can be told apart by are the wire's (identitywire's
		// refusal codes), and a launcher branches on the code and not on the
		// sentence. The sentence names no address the caller did not send.
		s.refuse(w, StatusUnverified, identitywire.RefusalCallerNotAdmitted, err.Error())
		return IdentityCaller{}, false
	default:
		// A key set that could not be read is this service's own state and not
		// a verdict about the caller: a caller told otherwise would be told its
		// token is bad when the service could not tell.
		s.refuseIdentityCaller(w, "a ticket is asked for by a person on this installation's roster, a service account on it, or the resident orchestrator: this caller is not one of them, so it is issued no run's identity", err)
		return IdentityCaller{}, false
	}
}
