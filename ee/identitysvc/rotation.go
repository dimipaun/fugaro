// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// Which signing keys the identity service publishes at a moment.
//
// The assertion's signing keys rotate (spec "Resolved decisions", "Assertion
// signing keys"; plan A2, with the plan's own figures: a 90-day period and a
// 30-day overlap). A new key starts signing every RotationPeriod, and the key
// it replaced stays published for RotationOverlap more, so an assertion minted
// a moment before a rotation is still verifiable a moment after it — the
// alternative is a verifier that refuses every live assertion until it fetches
// the set again, which is the one failure a rotation must not cause.
//
// **What this file models is a kid, a public half and an instant.** No private
// half is here, none is reachable from here, and none is in the document the
// service serves (.claude/rules/ee.md R5): the private half of every signing
// key is a Cloud KMS key version, and the owner creates each one and signs the
// set that contains it before the signer uses it (Check 737).
//
// **The set is the current key plus the one before it, and never a third.** An
// assertion lives two minutes (plan O4), so a key two rotations back is one no
// assertion inside its own life can name; publishing it would widen what a
// stolen key set can be spent on for nothing.

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	// RotationPeriod is how long a signing key is the current one: the plan's
	// 90 days, the figure the owner accepted (O4).
	RotationPeriod = 90 * 24 * time.Hour
	// RotationOverlap is how long the key a rotation replaced stays published
	// beside the new one: the plan's 30 days (O4). An assertion is live for two
	// minutes, so the overlap is three orders of magnitude past what the
	// assertion's own life needs, and what it covers is a verifier that has not
	// fetched the set since the rotation.
	RotationOverlap = 30 * 24 * time.Hour
)

// SigningKey is one signing key as the published set carries it: the key id a
// verifier looks an assertion's header up by, the public half it verifies with,
// and the instant the key started signing.
type SigningKey struct {
	KID       string
	Public    ed25519.PublicKey
	NotBefore time.Time
}

// Rotation is the set of signing keys this service knows about, in the order
// they started signing.
type Rotation struct {
	keys []SigningKey
}

// NewRotation returns the rotation over keys, oldest first.
//
// Everything it refuses is refused here rather than at a fetch: a set this
// service cannot publish is a set a verifier reads, finds nothing usable in and
// refuses every assertion against, and the moment to find that out is the
// moment the service is built, not the moment someone tries to sign in.
//
// Two keys that started signing at one instant are refused, and not ordered by
// a rule of this file's own: the order between them is then the store's, and
// Current would sign with whichever of the two the store happened to list
// first, so the same set would sign differently depending on who read it. A
// rotation puts one key after another in time, and two keys at one instant are
// a set nobody meant to publish.
func NewRotation(keys []SigningKey) (*Rotation, error) {
	if len(keys) == 0 {
		return nil, errors.New("identitysvc: a rotation with no key publishes nothing and signs nothing")
	}
	ordered := slices.Clone(keys)
	slices.SortStableFunc(ordered, func(a, b SigningKey) int { return a.NotBefore.Compare(b.NotBefore) })
	seen := make(map[string]bool, len(ordered))
	for i, key := range ordered {
		if key.KID == "" {
			return nil, errors.New("identitysvc: a signing key names no key id, so a verifier has nothing to look its assertions up by")
		}
		if seen[key.KID] {
			return nil, fmt.Errorf("identitysvc: two signing keys carry the key id %q, and a verifier reading that id cannot tell which key signed", key.KID)
		}
		seen[key.KID] = true
		if len(key.Public) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("identitysvc: the signing key %q carries a %d byte public half, and an Ed25519 one is %d bytes", key.KID, len(key.Public), ed25519.PublicKeySize)
		}
		if key.NotBefore.IsZero() {
			return nil, fmt.Errorf("identitysvc: the signing key %q names no instant it started signing, so there is no order to publish it in", key.KID)
		}
		if i > 0 && key.NotBefore.Equal(ordered[i-1].NotBefore) {
			return nil, fmt.Errorf("identitysvc: the signing keys %q and %q both started signing at %s, and the order between two keys at one instant is then the store's and not the set's", ordered[i-1].KID, key.KID, key.NotBefore.UTC().Format(time.RFC3339))
		}
	}
	return &Rotation{keys: ordered}, nil
}

// Published returns the keys a verifier may see at at, newest first: the key
// signing now, and the one it replaced while the overlap lasts.
//
// A key that has not started signing is not published — an assertion cannot
// name it — and a key past its own overlap is not either. The current key is
// published however long it has been current: a rotation the owner has not run
// yet must not empty the set, because a set with no key is every assertion
// refused.
func (r *Rotation) Published(at time.Time) []SigningKey {
	current, ok := r.Current(at)
	if !ok {
		return []SigningKey{}
	}
	published := []SigningKey{current}
	index := slices.IndexFunc(r.keys, func(k SigningKey) bool { return k.KID == current.KID })
	if index > 0 && at.Before(current.NotBefore.Add(RotationOverlap)) {
		published = append(published, r.keys[index-1])
	}
	return published
}

// Current returns the key that signs at at: the newest one that has started,
// and false when none has.
func (r *Rotation) Current(at time.Time) (SigningKey, bool) {
	index := -1
	for i, key := range r.keys {
		if at.Before(key.NotBefore) {
			break
		}
		index = i
	}
	if index < 0 {
		return SigningKey{}, false
	}
	return r.keys[index], true
}
