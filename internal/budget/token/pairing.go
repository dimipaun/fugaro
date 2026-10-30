package token

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The pairing token (D165; plan §4.1, §4.4, §7.1 step 3): the one-shot
// credential a phone holds while it pairs, minted beside the run token and
// deliberately not shaped like one. Its uid begins p~ — never r~ (a run's,
// UID's) and never m~ (a device's) — and its one custom claim is pu, which is
// all the deployed document reads of it (rules.go's pairingClaim). The rules
// hand the binding to the operator's own comparison of the typed fingerprint
// (M3), so a token that reaches past its two nodes would be authority nothing
// asked for.
const (
	// PairingMaxLifetime is D165's five-minute ceiling on the pairing token.
	PairingMaxLifetime = 5 * time.Minute
	// PairingSlack is the exchange's slack. The phone trades the pairing
	// token for an ID token after the operator has typed the code, so the
	// token must outlive the wait by enough for that exchange to land.
	PairingSlack = 30 * time.Second
	// PairingPrefix is what a pairing uid begins with.
	PairingPrefix = "p~"
)

// PairingLifetime is the pairing token's exp - iat for a wait of timeout: the
// smaller of PairingMaxLifetime and the wait plus PairingSlack. It is what the
// caller reports as expires_at, and it is here, not at the call site, so the
// reported expiry and the minted one cannot drift apart.
func PairingLifetime(timeout time.Duration) time.Duration {
	if lifetime := timeout + PairingSlack; lifetime < PairingMaxLifetime {
		return lifetime
	}
	return PairingMaxLifetime
}

// MintPairing mints the one-shot pairing token for a phone that may wait
// timeout, and returns it with the pairing uid it names.
//
// exp is iat + PairingLifetime(timeout), so the credential is sized to the
// wait rather than the other way round: a wait longer than D165's ceiling
// would leave the phone exchanging a token that had already died, which
// surfaces as an opaque token error instead of the caller's own refusal —
// which is why the caller refuses a --timeout above the ceiling less the
// slack (§4.1).
func MintPairing(ctx context.Context, signer Signer, timeout time.Duration, now time.Time) (string, string, error) {
	if timeout <= 0 {
		return "", "", errors.New("budget token: the pairing wait must be positive")
	}
	email := signer.Email()
	if email == "" {
		return "", "", errors.New("budget token: no signer")
	}
	uid, err := pairingUID()
	if err != nil {
		return "", "", err
	}
	payload, err := json.Marshal(map[string]any{
		"iss": email, "sub": email, "aud": Audience,
		"iat": now.Unix(), "exp": now.Add(PairingLifetime(timeout)).Unix(),
		"uid": uid,
		"claims": map[string]any{
			"pu": uid,
		},
	})
	if err != nil {
		return "", "", err
	}
	tok, err := signer.SignJWT(ctx, payload)
	if err != nil {
		return "", "", err
	}
	// The signer is trusted, but a token that does not name this pairing must
	// never leave this function (mint.go's rule for a run's token).
	got, err := parseJWT(tok)
	if err != nil {
		return "", "", errors.New("budget token: the signer returned something that is not a JWT")
	}
	extra, _ := got["claims"].(map[string]any)
	if claimString(got, "uid") != uid || claimString(extra, "pu") != uid {
		return "", "", fmt.Errorf("%w: the signer returned a token for another uid", ErrTokenMismatch)
	}
	return tok, uid, nil
}

// pairingUID returns a fresh pairing uid: PairingPrefix followed by 32 random
// bytes in the one spelling the deployed PUBKEY alphabet admits (unpadded
// base64url, §2 (e)), bounded by maxUIDBytes. A failure of crypto/rand is
// returned rather than replaced by a weaker source: the uid is the whole of
// what the pairing token names, and a guessable one would be a guessable
// pairing.
func pairingUID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("budget token: no randomness for a pairing uid")
	}
	uid := PairingPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	if len(uid) > maxUIDBytes {
		return "", errors.New("budget token: the pairing uid is longer than 128 bytes")
	}
	return uid, nil
}
