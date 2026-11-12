// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package main

// The mint itself: one claim set in, one key text out. The command in main.go
// and the KMS signer in kms.go are the two ends of it, and the package's
// comment there carries the tool's whole why.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// Signer is the tool's one route to a signature: a KMS key's signing API. What
// it answers is the raw signature the key text carries, and nothing on this
// side holds a private key to sign with instead (.claude/rules/ee.md, R5).
type Signer interface {
	Sign(ctx context.Context, payload []byte) ([]byte, error)
}

// Verifier is the check a minted key is held to before the tool answers it:
// S2.3's verifier, over the ring the caller names rather than the compiled one,
// which is empty until S2.9 publishes the owner's production half.
type Verifier func(raw []byte, now time.Time) (licence.Claims, licence.State, error)

// Mint signs one claim set through sign and answers the key text, or refuses.
//
// The claim set is marshalled once, and those bytes are both what sign is asked
// to sign and what the key text carries as its payload, so the signature covers
// exactly what a verifier reads (docs/licensing/licence-key.md, "Raw bytes
// before claims"). A signature that is not the raw one -- a wrapped or
// re-encoded answer -- fails the check below instead of printing a key nothing
// accepts.
//
// The check is made at the claim set's own `issued`, the instant the command
// takes from its clock: acceptance does not depend on the clock, only the state
// a holder would then see does, and where a claim set came from is the caller's
// business -- `issued` is the minter's to choose, the hole S2.1 states and the
// design leaves open for hand minting.
//
// The state is held and not only the error, because a claim set whose `expires`
// is before its `issued` is not a refusal: it is a key that has already ended,
// which verifies as expired, and the verifier's bound -- a term longer than the
// tier's maximum -- does not fire on a term shorter than nothing. So the check
// above the signature is not the whole guard: what this answers is a key that is
// good at the instant it was minted, and a caller that hands over a claim set
// `issued` in the future gets one, which is the accepted hole and not this one.
//
// The claim set is signed before it is checked. That one signature is spent on
// a claim set the tool then refuses, and it is the price of keeping S2.1's
// maxima in one place: the rule lives in the verifier, this tool keeps no copy
// of it, and a hand-minted key and the service's stay one format.
func Mint(ctx context.Context, keyID string, claims licence.Claims, sign Signer, verify Verifier) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("the claim set as the payload a verifier reads: %w", err)
	}
	signature, err := sign.Sign(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("signing the payload through the key's API: %w", err)
	}
	key := keyText(keyID, payload, signature)
	_, state, err := verify([]byte(key), time.Unix(claims.Issued, 0))
	if err != nil {
		return "", fmt.Errorf("the key this claim set makes: %w", err)
	}
	if state != licence.Valid {
		return "", fmt.Errorf("the key this claim set makes reads %s at its own issued instant, and this tool mints keys that are good then: a term that ends before it starts is not one anyone means to mint", state)
	}
	return key, nil
}

// keyText is the key text the design writes: the key id, the payload and the
// signature, all three unpadded base64url, in that order (docs/licensing/
// licence-key.md, "The key"). The id is the one part nothing decodes, so it is
// carried as the text it was given, which is how a verifier places the key by
// it.
func keyText(keyID string, payload, signature []byte) string {
	return keyID + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

// VerifierV1 is the check a minted v1 key is held to before the tool answers
// it: K1.1's verifier, over the ring the caller names rather than the compiled
// one, which is empty until S2.9 publishes the owner's production half.
type VerifierV1 func(raw []byte, now time.Time) (licence.ClaimsV1, licence.State, error)

// MintV1 signs one v1 claim set through sign and answers the key text, or
// refuses. It is Mint's counterpart for the `fgk_` format (K1.1's, D2130) and
// the format the service of K2 will write too, so a hand-minted key and a
// bought one stay one shape to every verifier.
//
// Everything Mint states about the signing holds here as well, because it is
// the format's rule rather than a tool's: the claim set is marshalled once and
// those bytes are both what sign is asked to sign and what the key text
// carries, so the signature covers exactly what a verifier reads. The signature
// is spent before the key is checked, which is the price of keeping the rules
// in the verifier instead of restating them here.
//
// Two things differ from Mint's check, both of them the v1 format's:
//
//   - The instant the key is read at is the claim set's own `issued_at`, which
//     the format writes as a stamp rather than as Unix seconds.
//   - The state a good key reads there is `active` and not `valid`: `active`,
//     `grace` and `expired` are the v1 names for the same three states
//     (ee/licence/keyv1.go).
func MintV1(ctx context.Context, claims licence.ClaimsV1, sign Signer, verify VerifierV1) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("the claim set as the payload a verifier reads: %w", err)
	}
	signature, err := sign.Sign(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("signing the payload through the key's API: %w", err)
	}
	key := keyTextV1(payload, signature)
	_, state, err := verify([]byte(key), claims.IssuedAt)
	if err != nil {
		return "", fmt.Errorf("the key this claim set makes: %w", err)
	}
	if state != licence.Active {
		return "", fmt.Errorf("the key this claim set makes reads %s at its own issue instant, and this tool mints keys that are good then: a paid-through that is not after its own issued-at is not one anyone means to mint", state)
	}
	return key, nil
}

// keyTextV1 is the v1 key text the format writes: the `fgk_` prefix, then the
// payload and the signature, both unpadded base64url (docs/licensing/
// licence-key.md, "The key"). The prefix is spelled here rather than read from
// the licence package because that package exports no encoder: nothing but a
// minter writes a key text, and the two minters of the format -- this tool and
// the key service (K2.1) -- spell it from their own copy for that reason.
func keyTextV1(payload, signature []byte) string {
	return "fgk_" + base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}
