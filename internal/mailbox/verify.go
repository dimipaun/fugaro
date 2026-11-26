package mailbox

import (
	"crypto/ed25519"
	"fmt"
)

// The device signature (O6, which amends D171): the tag a phone paired the new
// way writes, and the gate checks, where the old way had one HMAC key per
// pairing.
//
// **Why a signature and not a shared key.** The MAC key was one secret in three
// homes on the owner's machine — the laptop's keychain, a Secret Manager copy
// and the project config — so every one of them was a place it could be read,
// and a phone that carried it could not prove anything a reader of any of the
// three could not also produce. A device that signs with its own key proves
// possession of it, and the only thing the consumer needs is the public half,
// which is not a secret at all (O6, O12, F33).
//
// **The node chooses the scheme, and that decision is not here.** Which of the
// two tags a record must carry is the device node's answer, applied by the gate
// (`mac.go`): a node that holds a public key accepts signatures only and
// refuses a MAC-tagged message, and a node that holds none accepts a MAC only.
// Nothing in this file reads a node, a store or a clock — the public key
// arrives as an argument, exactly as the MAC key does — so there is one place
// that decides a scheme and one place that checks a signature.
//
// **Sign and verify are one function over one canonical form**, as they are for
// the MAC: the covered bytes are CanonicalBytes(record, direction), which drops
// whichever tag the record carries. What is *not* here is a Go-side signer for
// a device key, and that is deliberate: the consumer holds no device private
// key and signs nothing with one. The phone's half is C2's `signMessage`
// (`shared/mobile-session/port.js`), and the consumer's own signatures — the
// replies and the heartbeat — are the KMS signer's (`kmssigner.go`, J6, O17).

// VerifySignature decides whether record carries the signature its own bytes
// produce under public, in the direction the reader read it from.
//
// It answers `false` for everything that cannot carry a signature — a record
// with no `sig`, a `sig` that is not a string, a signature of the wrong length
// or a non-canonical spelling — and an error only for a record CanonicalBytes
// refuses, which is a record no signature could have been made over. That is
// `VerifyMAC`'s answer shape, for its reason: a caller branches on "verified",
// and a record that cannot be checked is not one.
//
// The comparison is `ed25519.Verify`'s, which is not constant-time in the
// message and does not need to be: the signature is public, the message is what
// the reader already holds, and the only secret in the exchange is a private
// key this side never has.
func VerifySignature(record Record, public ed25519.PublicKey, direction string) (bool, error) {
	canonical, err := CanonicalBytes(record, direction)
	if err != nil {
		return false, err
	}
	got, ok := record["sig"].(string)
	if !ok {
		// No signature, or one that is not a string: nothing to check. The
		// schema refuses both shapes before a caller reaches here.
		return false, nil
	}
	// The one spelling, strictly decoded, and the same value
	// DecodeConsumerPublicKey uses: the protocol has a single unpadded
	// base64url rule for a key and for a signature, and a second value here
	// would be a second spelling to keep in step. A non-canonical string is
	// refused rather than decoded — the strict decoder's trailing-bits check —
	// because three other strings decode to the same 64 bytes, and a reader
	// that accepted them would be reading a value no signer wrote.
	signature, err := consumerKeyEncoding.DecodeString(got)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return false, nil
	}
	if len(public) != ed25519.PublicKeySize {
		// A key that is not a key verifies nothing. The gate's own constructor
		// refuses one before a record ever reaches this point, and a caller
		// that built a key by hand is answered `false` rather than panicking.
		return false, nil
	}
	return ed25519.Verify(public, canonical, signature), nil
}

// NewSignatureGate is O6's gate: the one a device node holding a public key is
// served by. The key is the node's `public_key` as the node carries it —
// unpadded base64url of the 32 bytes an Ed25519 public key is (C1's
// `DevicePublicKey`, `credsvc.MobileDeviceNode`) — and it is read here, in the
// constructor, rather than by the caller, so that a node whose key cannot be
// read is a refusal to build the gate and never a gate that verifies against
// something that is not a key.
//
// The error is not a fallback: a caller that cannot build this gate has a
// device whose messages it cannot attribute at all, and building the MAC gate
// instead would accept a tag the phone no longer writes (O6's whole point).
func NewSignatureGate(publicKey string, o GateOptions) (*Gate, error) {
	key, err := consumerKeyEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("mailbox: a device public key must be one unpadded base64url string of %d characters decoding to %d bytes", consumerKeyEncoding.EncodedLen(ed25519.PublicKeySize), ed25519.PublicKeySize)
	}
	g := &Gate{public: ed25519.PublicKey(key)}
	o.apply(g)
	return g, nil
}
