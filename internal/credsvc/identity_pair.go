package credsvc

// The pairing and device routes (LM4, R14): the two of the five identity routes
// a PERSON asks, and the two whose answer is a phone's token rather than a
// run's.
//
// They are one act in two halves, and the halves are minutes apart: the
// operator's machine asks for the pairing token before it shows the code, and
// asks for the device token once the phone has written its key and the typed
// code has matched. `fugaro pair` did both itself until this route existed, on
// the operator's own standing grant; the service does them now because the
// runs' signer is the only account that may mint them (D3126, D3121).
//
// What the service derives and no request may name (R14): the pairing uid, the
// device's uid and its claim. PairingRequest and DeviceRequest have no field at
// all, and a body that names one — a uid among them — is refused by the strict
// decode rather than served under a value the caller chose. What the service
// does NOT decide is who may pair in the roles' sense: the class is the
// installation's launcher roster, and which of its people holds the roles the
// act needs stays `pair.go`'s own reading of the cloud's answer, unchanged
// (R14).
//
// # What these routes record
//
// One line per token issued, on the service's own log, and no audit line. That
// is the grant route's own reading of the act it answers — "a StatusOK line
// requires a credential, and a grant issues none" (slip.go) — with a second
// reason on top of it: the audit's purpose set is closed and holds no purpose
// of the identity acts (audit.go). C1 declared this contract and left the
// writer alone, every task of this plan is told not to edit that file, and the
// four mobile purposes arrived in a task of their own (the mobile identity
// plan's P0). A line written under another route's purpose would be a false
// record in the trail an operator greps, which is worse than none; a purpose
// for the identity acts is a task this plan does not have.
//
// No refusal here is one the audit's rule reaches either: the rule starts once
// a caller is verified, and these routes refuse before that, at the class
// itself — which is what refuseLauncher already does and why it writes no line.
// The mint's own failure is the service's failure and not a refusal of a
// caller, and it is recorded on the same log.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// identityPairWait is the wait a pairing token is sized to, and it is the
// contract's own ceiling rather than a field of the call: PairingRequest has no
// field at all (R14), so the operator's own `--timeout` never reaches this
// service, and a lifetime shorter than the ceiling would refuse a wait the CLI
// is allowed to take (its pairMaxTimeout is the ceiling less the exchange's
// slack). PairingLifetime of the ceiling is the ceiling itself.
const identityPairWait = token.PairingMaxLifetime

// identityPairing answers POST /v1/identity/pairing (LM4, R14): a verified
// person on the roster is answered a pairing token, the p~ uid it names and
// when it stops working, all three the service's own.
func (s *Server) identityPairing(ctx context.Context, w http.ResponseWriter, r *http.Request, signer token.Signer) {
	caller, ok := s.identityPerson(ctx, w, r, signer, &identitywire.PairingRequest{})
	if !ok {
		return
	}
	now := s.now()
	tok, uid, err := token.MintPairing(ctx, signer, identityPairWait, now)
	if err != nil {
		s.identityMintFailed(w, err)
		return
	}
	s.log.Info("credsvc: a pairing token was issued", "caller", caller.Account, "pairing", uid)
	answer, err := json.Marshal(identitywire.PairingResponse{
		Token:     tok,
		Pairing:   uid,
		ExpiresAt: now.Add(token.PairingLifetime(identityPairWait)),
	})
	if err != nil {
		s.identityAnswerFailed(w, err)
		return
	}
	writeSlipAnswer(w, answer)
}

// identityDevice answers POST /v1/identity/device (LM4, R14): a verified person
// on the roster is answered the device token of the pairing they completed, the
// m~ uid it names and when it stops working. That the pairing WAS completed —
// the phone's key written, the typed code matched — is checked on the person's
// own side, which is where the code is read (R14); what this route adds is the
// token, and it is the signer's and not the caller's.
func (s *Server) identityDevice(ctx context.Context, w http.ResponseWriter, r *http.Request, signer token.Signer) {
	caller, ok := s.identityPerson(ctx, w, r, signer, &identitywire.DeviceRequest{})
	if !ok {
		return
	}
	now := s.now()
	dev, err := token.MintDevice(ctx, signer, now)
	if err != nil {
		s.identityMintFailed(w, err)
		return
	}
	s.log.Info("credsvc: a device token was issued", "caller", caller.Account, "device", dev.UID)
	answer, err := json.Marshal(identitywire.DeviceResponse{
		Token:     dev.Token,
		Device:    dev.UID,
		ExpiresAt: now.Add(token.Lifetime),
	})
	if err != nil {
		s.identityAnswerFailed(w, err)
		return
	}
	writeSlipAnswer(w, answer)
}

// identityPerson is the two routes' shared admission, in the order they answer
// it: the bearer, the call's shape, then the caller. It answers the refusal
// itself and reports whether the call may go on.
//
// The order is why each step is where it is:
//
//   - the bearer first, and only read: the caller's own token is what the body
//     is read with, so that nothing below this line can echo it back (the
//     call-scoped scrub, readCall).
//   - the call's shape next, and before any identity is read: a body that names
//     a field, a body this service will not hold, and a call presenting a run
//     identity are shapes these routes do not have, and refusing one reads no
//     caller at all.
//   - the caller last, which is the whole of the admission: the person class of
//     these routes is the installation's launcher roster (VerifyLauncher), and
//     an account on no roster, a service account and a run's job account are
//     refused by it. A refusal here is the class's own and names no audit line,
//     as the grant route's class refusal does: nothing was verified.
func (s *Server) identityPerson(ctx context.Context, w http.ResponseWriter, r *http.Request, signer token.Signer, body any) (LauncherCaller, bool) {
	// The issuer's own answer first (R7): a service whose identity half is not
	// configured has nothing to verify a caller FOR, and the contract's "not
	// served" is what the CLI bridges on and signs locally against. No caller is
	// read here, so no line is written.
	if s.cfg.Identity.Signer == "" || signer == nil {
		s.refuse(w, StatusUnavailable, identitywire.RefusalNotServed,
			"this service holds no issuer: no signer account is configured on this installation, so the identity routes answer 'not served'. A launcher of this release signs a run's identity locally as before; an installation that has already moved the grant cannot, and the fix is fugaro update")
		return LauncherCaller{}, false
	}
	tok, err := personBearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on these routes with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return LauncherCaller{}, false
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it issues a phone's token to a person, and a caller presenting a run is a call of a shape it does not have")
		return LauncherCaller{}, false
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it issues a phone's token to a person, and a caller naming a job is a call of a shape it does not have")
		return LauncherCaller{}, false
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		// A body this service will not hold is a refused REQUEST and not an
		// unavailable service, which is the reading the grant route gives the
		// same error.
		s.refuse(w, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return LauncherCaller{}, false
	}
	if err := identitywire.DecodeStrict(cl.body, body); err != nil {
		// The refusal names the field and never what the call sent: the
		// unknown field's NAME is all json's error carries, and a uid among the
		// names is exactly the caller-chosen identity this contract refuses.
		s.refuse(w, StatusRequestRefused, ErrorRequestRefused,
			requestRefused+"this route's body is an empty JSON object and names no field, so no uid and no claim of a caller's reaches the token: "+err.Error())
		return LauncherCaller{}, false
	}
	caller, err := s.cfg.Auth.VerifyLauncher(ctx, tok)
	if err != nil {
		s.refuseIdentityCaller(w, "this route admits a person on the installation's roster, verified by their own Google ID token: this caller is not one of them, so no token is issued. A service account and a run's job account pair nothing — the ticket route is the one that admits them", err)
		return LauncherCaller{}, false
	}
	return caller, true
}

// refuseIdentityCaller answers the class's own refusal of a caller, in the
// words of the route that made it. It is refuseLauncher's shape with this
// contract's code: the caller-side refusals (an account on no roster, a token
// that is not a person's, an ID token this class will not carry) are the
// contract's caller_not_admitted, and a key set this service could not read is
// its own state and not a refusal of the caller — a caller told otherwise would
// be told its token is bad when the service could not tell.
//
// admitted is the route's own sentence and not this helper's, because the
// routes that refuse a caller this way admit different classes — the pairing's
// persons, the claim's job accounts, the probe's operators — and one sentence
// would tell two of them about a class their route does not serve.
func (s *Server) refuseIdentityCaller(w http.ResponseWriter, admitted string, err error) {
	switch {
	case errors.Is(err, ErrUnverifiedJob), errors.Is(err, ErrUnverified):
		s.refuse(w, StatusUnverified, identitywire.RefusalCallerNotAdmitted, admitted)
	default:
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, err.Error())
	}
}

// identityMintFailed answers a mint the signer would not perform. It is this
// service's own failure and not a refusal of the call: nothing was issued, no
// credential of the cascade was read, and the operator reads it on the
// service's log (this file's note on the audit says why it is not a line).
func (s *Server) identityMintFailed(w http.ResponseWriter, err error) {
	s.log.Error("credsvc: an identity token could not be signed", "error", err)
	s.refuse(w, StatusUnavailable, ErrorStateUnavailable,
		"no token was issued: the service's signer did not answer, so this installation cannot mint one at all (the issuer's row and its account are what `fugaro doctor` reads)")
}

// identityAnswerFailed answers a minted token whose answer could not be built.
// The token is not in any answer and never reaches a caller, and the operator
// reads the encoding's own error on the service's log.
func (s *Server) identityAnswerFailed(w http.ResponseWriter, err error) {
	s.log.Error("credsvc: the identity answer could not be built", "error", err)
	s.refuse(w, StatusUnavailable, ErrorStateUnavailable, "the answer could not be built, so no token was issued")
}
