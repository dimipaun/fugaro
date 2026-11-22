// identity_sign.go is the signing half of the identity routes (LM2): the runs'
// signer the service itself holds, and the probe that proves it can sign (R8,
// F33).
//
// The claim signs from here too — identitySignerFor is the one place this
// service builds a signer, so a run's identity (identity_claim.go) and the
// probe's payload are signed by the same account through the same
// construction, and a deployment that could sign one and not the other is not
// a state this code has.

package credsvc

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// identitySignerScope is the scope the service's own credential is fetched
// with: cloud-platform, which is what IAM Credentials signJwt is reached with.
// The account the service runs as holds the minter role on the runs' signer
// (D3121: "its account gains the minter role on the runs' signer"), and it
// holds nothing else of this contract's — the signature is asked for as the
// signer and never with an access token of its own.
const identitySignerScope = "https://www.googleapis.com/auth/cloud-platform"

// identitySignerFor builds the runs' signer: the account the installation's
// row names (IdentityConfig.Signer), signed through token.IAMSigner with the
// credential the service itself runs as.
//
// It is a variable so that a test can hand the routes a signer over a fake IAM
// Credentials endpoint, the seam internal/cli's impersonationEndpoint already
// is: what a test replaces is where the credential comes from, and everything
// above it — the claims' checks, the token's own re-read (token.MintAt) — is
// the real code either way.
var identitySignerFor = func(ctx context.Context, email string) (token.Signer, error) {
	ts, err := google.DefaultTokenSource(ctx, identitySignerScope)
	if err != nil {
		return nil, err
	}
	return token.NewIAMSigner(email, ts)
}

// identityProbePayload is what the probe has the runs' signer sign: the
// contract's own fixed marker (identitywire.ProbePayload) inside the smallest
// claim set signJwt takes.
//
// It is a serialized JSON OBJECT and not the bare constant, and that is
// signJwt's own rule rather than this file's preference: IAM Credentials
// refuses a payload that is not one ("a serialized JSON object that contains a
// JWT Claims Set"), and internal/gcpfake's fake of that call holds the same
// rule — so handing it identitywire.ProbePayload's plain string would make
// every probe answer 503, which is a probe that proves nothing on the one step
// it exists for (R8, F33: `update` moves the people's minter row only after an
// answer). The constant is the whole of what is in the payload besides the
// signing's own clock, so what the service signs is still a fixed value and
// never a caller's bytes: the route is neither a signing oracle nor an open
// trigger of the signing quota (R2).
//
// iat and exp are the shape every other signing call of this repository has
// (token.MintAt), and no caller reads either: the signature is discarded and
// never answered, and neither is a digest of it.
func identityProbePayload(now time.Time) ([]byte, error) {
	return json.Marshal(struct {
		Iss string `json:"iss"`
		Sub string `json:"sub"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}{
		Iss: identitywire.ProbePayload, Sub: identitywire.ProbePayload,
		Iat: now.Unix(), Exp: now.Add(token.Lifetime).Unix(),
	})
}

// signerWitness wraps the runs' signer to record whether the signing was
// reached at all.
//
// token.MintAt checks the record's claims (R4) and then asks the signer, and
// the two failures are answered differently: a record whose claims are not one
// this service may sign is the caller's refusal, and a signer that could not
// sign is this service's own failure (R16's rule, the StoreError doc's). The
// errors a signer returns are not a closed set this service can hold — token's
// IAM signer answers a refused credential, a missing role and an HTTP status
// with three different errors — so the split is observed where it happens:
// whether SignJWT was called. Email is passed through: MintAt reads it before
// it signs.
type signerWitness struct {
	token.Signer
	reached bool
}

func (w *signerWitness) SignJWT(ctx context.Context, payload []byte) (string, error) {
	w.reached = true
	return w.Signer.SignJWT(ctx, payload)
}

// identityProbe answers the probe (R2, R8, F33): the route `fugaro update`
// calls before it moves the people's minter row to the service, so that an
// answer means the row and the account are right.
//
// It signs the contract's fixed payload (identityProbePayload, built around
// identitywire.ProbePayload) as the runs' signer and DISCARDS the signature:
// the answer carries the signer's own name and when it signed, and no signature
// bytes and no digest of them. That is what keeps the route from being a
// signing oracle and from being an open trigger of the signing quota (R2): a
// caller can make the service sign one fixed payload — nothing of the call
// reaches it — and can learn nothing it signed.
//
// It admits the roster's operators alone. A caller that is not a person on the
// roster is refused before its identity is verified and writes no audit line —
// there is no verified caller for a line to name; a launcher of the roster
// that is not an operator IS verified, so its refusal writes the one line
// every refusal past that point writes.
func (s *Server) identityProbe(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s.cfg.Identity.Signer == "" {
		s.refuseIdentityNotServed(w)
		return
	}
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, identitywire.RefusalCallerNotAdmitted, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyLauncher(ctx, tok)
	if err != nil {
		s.refuseIdentityCaller(w, "this route admits a person on the installation's roster, verified by their own Google ID token: the probe proves the runs' signer for the operators that may run an update, so no other caller is served here. A service account and a run's job account probe nothing — the ticket route is the one that admits them", err)
		return
	}
	if !identityListed(s.cfg.Identity.Operators, caller.Account) {
		// The implicit admins are the scope check's (LM23, F29) and not this
		// route's: what the probe admits is the owner's operators list, which
		// is the list `update` may be run by (R2, A2).
		s.refuseIdentityCall(ctx, w, "", "", StatusUnverified, identitywire.RefusalCallerNotAdmitted,
			"the probe is the roster's operators' alone: this caller is a launcher of this installation and not one of its operators, so it may not ask the service to sign")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refuseIdentityCall(ctx, w, "", "", StatusRequestRefused, identitywire.RefusalIdentityRefused, requestRefused+err.Error())
		return
	}
	// The body has no field, and it is read strictly like every other body of
	// this contract: a caller that names one — a payload among them — is
	// refused rather than served under a value it chose.
	var req identitywire.ProbeRequest
	if err := identitywire.DecodeStrict(cl.body, &req); err != nil {
		s.refuseIdentityCall(ctx, w, "", "", StatusRequestRefused, identitywire.RefusalIdentityRefused, requestRefused+err.Error())
		return
	}
	signer, err := identitySignerFor(ctx, s.cfg.Identity.Signer)
	if err != nil {
		// The error is the credential's own and is logged, never answered: a
		// refusal names the field and never what the call sent (the service's
		// own rule), and this one carries the service's credential.
		s.log.Error("credsvc: the runs' signer could not be built", "signer", s.cfg.Identity.Signer, "error", err)
		s.refuseIdentityCall(ctx, w, "", "", StatusUnavailable, ErrorStateUnavailable,
			"the service could not reach the account the runs' identities are signed as")
		return
	}
	now := s.now()
	payload, err := identityProbePayload(now)
	if err != nil {
		s.log.Error("credsvc: the probe's payload could not be built", "error", err)
		s.refuseIdentityCall(ctx, w, "", "", StatusUnavailable, ErrorStateUnavailable, "the probe's payload could not be built")
		return
	}
	// The signature is discarded here and nowhere else: it is not written to
	// the answer, to a log line or to an audit line, and no digest of it is.
	if _, err := signer.SignJWT(ctx, payload); err != nil {
		s.log.Error("credsvc: the identity probe could not sign", "signer", signer.Email(), "error", err)
		s.refuseIdentityCall(ctx, w, "", "", StatusUnavailable, ErrorStateUnavailable,
			"the service could not sign as the runs' signer: the probe proves the signer's row and its account, and this installation's are not right yet")
		return
	}
	// The probe's own record is the service's log line, as the grant's is: a
	// StatusOK audit line requires a purpose, a run and a job (checked), and
	// this act has none of the three — the signer's name is the fact an
	// operator reads here.
	s.log.Info("credsvc: the identity probe signed", "signer", signer.Email(), "caller", caller.Account)
	body, err := json.Marshal(identitywire.ProbeResponse{Signer: signer.Email(), SignedAt: now})
	if err != nil {
		s.log.Error("credsvc: the probe's answer could not be built", "error", err)
		s.refuseIdentityCall(ctx, w, "", "", StatusUnavailable, ErrorStateUnavailable, "the probe's answer could not be built")
		return
	}
	writeSlipAnswer(w, body)
}
