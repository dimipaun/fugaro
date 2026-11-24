package assertion

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// PinnedKey is one signing key a build trusts without fetching anything (O7):
// the key itself, and the day it stops being accepted.
//
// The key is pinned so that an assertion signed by it verifies with no read of
// any URL at all, which is what the identity project being unreachable must not
// take away. It ends two ways, and both are in the plan's round-5 resolution:
// at NotAfter, which is the end of the rotation overlap the release that pinned
// it was built for (A2's 90 days with 30 days of overlap), and earlier than
// that when a valid key set at or above the build's sequence floor omits it --
// which is what lets a key be withdrawn between releases, and what makes a lost
// root key recoverable without waiting for the next build to be deployed. The
// verifier refuses a key set that omits it rather than keeping a key nothing
// publishes any more.
type PinnedKey struct {
	// KID is the key id an assertion names in its header.
	KID string
	// Key is the public half, and it is the public half only: this repository
	// carries no private key for the identity project's signer (O8), and a
	// build that did would be a build whose signing key ships to every project.
	Key ed25519.PublicKey
	// NotAfter is the instant the key stops being accepted, and it is the end
	// of the overlap the pin was made for rather than the start of it.
	NotAfter time.Time
}

// Pins is everything a build checks an assertion against without asking anyone:
// the issuer it accepts, where the key set is read from, the root key that
// signs that set, the signing keys it already holds, and the sequence of the
// set the release pinned (P8). It is a build-time value, not configuration: a
// pin a deployment could change is a pin an attacker with a deployment could
// change, and O7's whole point is that the trust anchor travels with the binary.
type Pins struct {
	// Issuer is the identity project's issuer, and an assertion's iss must
	// equal it exactly (F38).
	Issuer string
	// JWKSURL is where a signed key set is read from. It is https and carries
	// no query, fragment or userinfo: a URL the caller can lengthen is a URL
	// whose document a stranger can choose.
	JWKSURL string
	// RootKeyID is the root key's id, and it is the *root's*: the header of a
	// signed set names it, and a reader that verified with whatever the header
	// named would verify with a key the document brought with it.
	RootKeyID string
	// RootKey is the root key's public half, which is what makes a fetched set
	// trustworthy at all.
	RootKey ed25519.PublicKey
	// Keys are the signing keys this build holds itself.
	Keys []PinnedKey
	// SeqFloor is the sequence of the key set this release pinned (P8). A
	// fetched set below it is refused, so a cache serving a document older than
	// the build cannot walk a project back onto keys the build has outlived.
	SeqFloor int64
}

// DefaultPins are the pins this build carries. Until P8 they are placeholders
// from a key pair drawn for the occasion: the public halves below are real
// values of the right shape, the private halves were discarded and exist
// nowhere, and the hosts are `.invalid` -- reserved, and resolvable by nobody --
// so a misconfigured fetch in a test cannot reach a live endpoint.
//
// They therefore verify nothing: no assertion any identity project can sign
// verifies against a key whose private half nobody holds, so a build taken to
// production with these pins refuses every pairing rather than admitting one.
// P8 replaces the values with the identity project's issuer, its keys URL, its
// root key, a signing key and the sequence of the release's key set, and it
// changes nothing about the shape -- so the tests that read these keep passing
// when it lands, and the dates below are past any release that would still
// carry them.
func DefaultPins() Pins {
	return Pins{
		Issuer:    "https://identity.invalid",
		JWKSURL:   "https://identity.invalid/.well-known/fugaro-key-set.jws",
		RootKeyID: "root-placeholder-2026-11",
		RootKey:   placeholderRootKey,
		Keys: []PinnedKey{{
			KID:      "signing-placeholder-2026-11",
			Key:      placeholderSigningKey,
			NotAfter: time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC),
		}},
		SeqFloor: 1,
	}
}

// The placeholder public halves, in unpadded base64url as every other key in
// this contract is spelled. They are written this way rather than as a byte
// literal so that a reader can decode them and see that they are ordinary
// Ed25519 keys, and so that no file in this repository has to carry a private
// key's shape at all.
const (
	placeholderRootKeyB64    = "JRmEDlDxNXOS-_yy9L4dPAteha_jviWQ5pyXp2JTR94"
	placeholderSigningKeyB64 = "T78AEweJf3CjqbtoG0zSABCUuAiLzqwFlyw6x3_0O2A"
)

// The two decoded above, and they are decoded once at start-up rather than per
// assertion: a constant that cannot be decoded is a bug in this file and not
// something an assertion can cause, so it panics where the mistake is.
var (
	placeholderRootKey    = mustDecodeKey(placeholderRootKeyB64)
	placeholderSigningKey = mustDecodeKey(placeholderSigningKeyB64)
)

func mustDecodeKey(encoded string) ed25519.PublicKey {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		panic("assertion: the placeholder pin is not an Ed25519 public key")
	}
	return ed25519.PublicKey(raw)
}

// validatePins refuses pins that cannot verify anything, where they are built.
// A pin set that is unusable fails closed on every sign-in with a message about
// the sign-in, which is the worst place to learn that the issuer is empty -- so
// the verifier is built once and refuses once, like the licence key's reader
// and mcpauth's key reader both do. What it checks is the shape and never the
// values: it cannot tell a production root key from a placeholder, and it is
// not meant to.
func validatePins(p Pins) error {
	if p.Issuer == "" {
		return errors.New("the issuer is empty")
	}
	if err := checkPinnedURL("issuer", p.Issuer); err != nil {
		return err
	}
	if err := checkPinnedURL("key set URL", p.JWKSURL); err != nil {
		return err
	}
	if p.RootKeyID == "" {
		return errors.New("the root key id is empty")
	}
	if len(p.RootKey) != ed25519.PublicKeySize {
		return fmt.Errorf("the root key is %d bytes, want %d", len(p.RootKey), ed25519.PublicKeySize)
	}
	if len(p.Keys) == 0 {
		return errors.New("no signing key is pinned: a build that pins none verifies nothing without a fetch, which is the dependency O7 pins a key to remove")
	}
	seen := make(map[string]bool, len(p.Keys))
	for _, k := range p.Keys {
		if k.KID == "" {
			return errors.New("a pinned signing key has no key id")
		}
		if seen[k.KID] {
			return fmt.Errorf("two pinned signing keys share the key id %s", k.KID)
		}
		seen[k.KID] = true
		if len(k.Key) != ed25519.PublicKeySize {
			return fmt.Errorf("the pinned key %s is %d bytes, want %d", k.KID, len(k.Key), ed25519.PublicKeySize)
		}
		if k.NotAfter.IsZero() {
			return fmt.Errorf("the pinned key %s has no not-after date, so nothing would ever end it", k.KID)
		}
	}
	if p.SeqFloor < 1 {
		return fmt.Errorf("the sequence floor is %d: a key set's sequence starts at one, so a floor below it floors nothing", p.SeqFloor)
	}
	return nil
}

func checkPinnedURL(what, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("the %s is not a URL", what)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("the %s is not an https URL", what)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("the %s carries a query, a fragment or userinfo", what)
	}
	return nil
}
