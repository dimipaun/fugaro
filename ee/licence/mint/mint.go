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
	if _, _, err := verify([]byte(key), time.Unix(claims.Issued, 0)); err != nil {
		return "", fmt.Errorf("the key this claim set makes: %w", err)
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
