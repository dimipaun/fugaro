package mailbox

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// The consumer's signing key: the key the laptop signs its mailbox replies and
// heartbeats with once a phone is paired the new way (O12), held in Cloud KMS
// so that no private key material exists outside it (O17). This file is plan
// task J6 of docs/mobile-identity-pairing/implementation-plan.md, and it is two
// halves of one key:
//
//   - the **signer** — `KMSSigner`, a `Signer`'s sibling that signs a value's
//     own canonical bytes with an Ed25519 key version in Cloud KMS;
//   - the **published contract** — where the public half lives in the database
//     and in what spelling (`ConsumerKeyNodePath`, `ConsumerKeyNode`), so that
//     the one writer (`fugaro update`, UP1) and the one reader (`fugaro pair`,
//     P2) cannot drift into a fingerprint that pins nothing.
//
// **What this file is, and what it is not.** J6 asks for the key, the signer
// and the node contract, and that is all this is: the contract is a shape, not
// a write and not a read. Nothing in this binary publishes
// `config/admin/mailbox_key` — the writer is the `update` step that applies the
// key and reads its public half back (UP1, which the plan's own task list names
// for exactly that), and it lands with the key it publishes — and the pairing
// ENDPOINT's read is not here either: the service's pair response (C1's
// `SessionResponse.ConsumerPublicKey`, O16) comes with the endpoint. What reads
// the node today is `fugaro pair` in `internal/cli` (P2), which pins the
// fingerprint into the QR. So the two sides of this contract are held together
// by `TestConsumerKeyNodeIsThePairReader` between the shape here and that
// reader, and the writer and the endpoint's read are recorded as deferred in
// the pull request that carried this file.
//
// **Why a signature and not the MAC.** `mac.go`'s tag is an HMAC: the process
// that signs holds the same secret the phone verifies with, so a phone that
// holds the key can forge the laptop's replies, and the key has to be carried
// into the installation's project (the third copy in
// `internal/mailbox/keychain/cloud.go`). A device signature removes both
// (J5b for the phone's writes, J5c for the laptop's): the verifier holds a
// public key, and the signer holds a private one that never leaves KMS. The
// covered bytes are exactly the MAC's — `CanonicalBytes(value, direction)`,
// every field but `mac` and `acked` plus the direction (§8.2.1, D171) — so the
// two schemes are one covered field set and differ in what the bytes are signed
// with. The record field is `sig` and not `mac` (J5r): the rules accept exactly
// one of the two, and which one a device must use is the gate's (J5b).
//
// **Why Cloud KMS (O17).** The key is created by Terraform in the installation
// project
// (deploy/terraform/gcp/modules/installation/mailbox_key.tf), is
// non-exportable, and is granted to the mailbox consumer's service account
// alone. Nothing in this repository, in the consumer's image or in a
// deployment's disk holds the private half: it is not a value this file could
// load if it wanted to, and `crypto/ed25519`'s own `GenerateKey` appearing
// nowhere outside tests is what makes that true of the code.

// The key's own names, which the Terraform module carries as literals: Go
// cannot read a `.tf` file, so the two spellings are held to each other by
// `TestMailboxKeyNamesAreTheGoSpellings` (internal/infra/mailbox_key_test.go),
// the way the schema's numbers are held to the modules' (M9). A `.tf` literal
// that drifted from these would be a plan whose key no consumer can name, and
// therefore a signer built against a key ring that does not exist.
const (
	// ConsumerKeyRingName is the key ring's name in the installation project.
	ConsumerKeyRingName = "fugaro-mailbox"
	// ConsumerCryptoKeyName is the signing key's name in that ring.
	ConsumerCryptoKeyName = "fugaro-mailbox-consumer"
	// ConsumerKeyAlgorithm is the algorithm the module pins, as Cloud KMS
	// spells it: Ed25519, and software because Cloud KMS offers Ed25519 in no
	// other protection level. It is here so that the algorithm the deployment
	// creates and the algorithm the phone verifies with are one word.
	ConsumerKeyAlgorithm = "EC_SIGN_ED25519"
	// ConsumerKeyVersionName is the key version the signer signs under. Cloud
	// KMS creates version 1 of an asymmetric key with the key itself, and the
	// module creates no version of its own, so this is the version every
	// installation's key has — and a signer naming no version would sign
	// nothing, since an asymmetric key has no primary version to fall back to.
	ConsumerKeyVersionName = "1"
)

// ConsumerCryptoKeyPath is the crypto key's resource name, as Cloud KMS spells
// it and as the module composes it out of the installation's project and
// region: `projects/<project>/locations/<region>/keyRings/fugaro-mailbox/cryptoKeys/fugaro-mailbox-consumer`.
//
// It is a function rather than a constant because both halves of the
// installation are the deployment's: this is the name `update` reads the public
// half under (UP1) and the name the key's own IAM grant must carry (the module,
// `mailboxSignerGrant` in internal/infra/tf/cover.go).
func ConsumerCryptoKeyPath(project, region string) string {
	return "projects/" + project + "/locations/" + region +
		"/keyRings/" + ConsumerKeyRingName + "/cryptoKeys/" + ConsumerCryptoKeyName
}

// ConsumerCryptoKeyVersionPath is the key version's resource name: the signing
// call's `name`, and the name `update` reads the public half of the version
// under. It is ConsumerCryptoKeyPath plus the version the key is created with.
func ConsumerCryptoKeyVersionPath(project, region string) string {
	return ConsumerCryptoKeyPath(project, region) + "/cryptoKeyVersions/" + ConsumerKeyVersionName
}

// The published half. `fugaro update` will write the consumer's public key to
// one node under `config/admin` — the writer is UP1's step, deferred with the
// key it publishes and not written by this file — and the database rules deny
// every client
// outright (`config/admin/**` carries `.read: false`, `.write: false`, and the
// children inherit both) — so the node is reachable with IAM alone, exactly as
// the roster and the invites beside it are, and a phone reads the key from the
// service's own answer and never from the database (O16, D1).
const (
	// ConsumerKeyNodePath is where the public half is published.
	ConsumerKeyNodePath = "config/admin/mailbox_key"
	// ConsumerKeyNodeVersion is the published node's own version. A reader pins
	// the node it reads (P2 refuses anything but this one), so a shape change
	// here is a bump the two sides make together.
	ConsumerKeyNodeVersion = 1
	// ConsumerPublicKeySize is an Ed25519 public key: 32 raw bytes.
	ConsumerPublicKeySize = ed25519.PublicKeySize
	// ConsumerSignatureSize is an Ed25519 signature: 64 raw bytes, and always
	// 64 — the length is a property of the algorithm and not of the key, which
	// is what makes a short answer from the key ring a refusal here rather than
	// a signature a phone rejects with no reason (J5r pins the same fixed
	// length in the rules).
	ConsumerSignatureSize = ed25519.SignatureSize
)

// consumerKeyEncoding is the one spelling of a published key: unpadded
// base64url, strictly decoded, which is C1's `ConsumerPublicKey` spelling and
// the QR's (internal/mobilesession/contract.go, internal/cli's pair_invite.go).
// 32 bytes is three whole base64 groups plus two characters, so the last
// character carries four significant bits and two this encoding discards —
// three other characters decode to the same 32 bytes — and Strict() is what
// refuses them, so one key has one spelling and one fingerprint.
var consumerKeyEncoding = base64.RawURLEncoding.Strict()

// ConsumerKeyNode is what `update` publishes at ConsumerKeyNodePath: the
// mailbox consumer's public half, one unpadded base64url string of the 32 bytes
// an Ed25519 public key is. It carries the key alone — the key version it came
// from, the algorithm and the key's own resource name are the deployment's and
// are read from the installation, not from the database — so a phone cannot be
// handed a claim about which key signs that a compromised writer could forge
// past the fingerprint check (O16).
type ConsumerKeyNode struct {
	V         int    `json:"v"`
	PublicKey string `json:"public_key"`
}

// ConsumerKeyFingerprint is the value the phone pins: the SHA-256 of the
// public key's decoded bytes, unpadded base64url (O16). It is over the bytes
// and never over a spelling, so the two spellings of one key are one
// fingerprint, and it is 43 characters rather than `token.Fingerprint`'s eight
// human-typed ones: this hash is what makes a forged key infeasible, and 40
// bits is a code for a person to compare and not a hash to check.
//
// `internal/cli`'s `pairConsumerKeyFingerprint` computes the same value for
// the QR, and `TestConsumerKeyNodeIsThePairReader` holds the two to each other,
// so a change on one side is a failing test rather than a QR whose fingerprint
// no phone could ever match.
func ConsumerKeyFingerprint(key []byte) string {
	sum := sha256.Sum256(key)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// EncodeConsumerPublicKey is the published spelling of a public key: unpadded
// base64url. It is what `update` writes into ConsumerKeyNode.
func EncodeConsumerPublicKey(key []byte) string {
	return base64.RawURLEncoding.EncodeToString(key)
}

// DecodeConsumerPublicKey reads a public key from that one spelling, and
// refuses everything else. Padding, the standard alphabet, a wrong length and a
// non-canonical trailing character are each refused rather than repaired, for
// the reason `pair_invite.go` gives: a repaired key is a key whose fingerprint
// the phone computes over other bytes.
//
// It is deliberately not `token.DecodeDeviceKey`: that decoder enforces
// X25519's discarded top bit, which is a rule about a key-agreement key and
// says nothing about a signing key — an Ed25519 public key with its top bit set
// is one a phone verifies against happily.
func DecodeConsumerPublicKey(s string) ([]byte, error) {
	if len(s) != consumerKeyEncoding.EncodedLen(ConsumerPublicKeySize) {
		return nil, fmt.Errorf("mailbox: a consumer public key must be one unpadded base64url string of %d characters decoding to %d bytes", consumerKeyEncoding.EncodedLen(ConsumerPublicKeySize), ConsumerPublicKeySize)
	}
	key, err := consumerKeyEncoding.DecodeString(s)
	if err != nil || len(key) != ConsumerPublicKeySize {
		return nil, fmt.Errorf("mailbox: a consumer public key must be one unpadded base64url string of %d characters decoding to %d bytes", consumerKeyEncoding.EncodedLen(ConsumerPublicKeySize), ConsumerPublicKeySize)
	}
	return key, nil
}

// AsymmetricSigner is the whole of what this file needs of Cloud KMS: sign
// payload with one asymmetric key version, and answer the signature. It is the
// KMS asymmetric-sign call with the request and response shapes reduced to what
// a signature is — the deployment's adapter is the thin one
// `*kms.KeyManagementClient` needs (the request's `name`, `data` and the
// CRC32C both ways, ee/keyservice/kmssigner.go's call), and the test's is a key
// generated in the test's own process.
//
// The version is an argument and not a field, so that one client serves every
// key of an installation — which is the API Cloud KMS actually has, and the
// reason `KMSSigner` holds the version instead.
type AsymmetricSigner interface {
	SignAsymmetric(ctx context.Context, keyVersion string, payload []byte) ([]byte, error)
}

// maxSignBytes bounds one payload. A mailbox value is a small JSON document
// (`CanonicalBytes`), and the bound is here so that a record this package
// should never have built is refused before it reaches a key ring rather than
// read as a key that failed.
const maxSignBytes = 64 << 10

// KMSSigner is §8.2's signer over the consumer's Ed25519 key in Cloud KMS: the
// value's canonical bytes go to the key version's signing call, and the
// signature that answers is the value's `sig` (J5r).
//
// **The direction is fixed by the constructor and not passed per call**, as it
// is for the MAC `Signer`: the two families are the outbox reply and the
// heartbeat (§8.2.3), the direction is in the signed bytes (a tag made for one
// node must not verify in another, §8.1), and so the node a value is signed for
// is a property of the signer and a caller cannot get it wrong per value. A
// constructor that named neither is refused, because a signer whose direction
// is empty would sign bytes no reader ever verifies.
//
// **There is no key here to redact.** The private half is in KMS and this holds
// a resource name; `KeyVersion` is the deployment's own name for the key, which
// is not a secret (a public key's version is readable by anyone who may read
// the key ring). A KMSSigner is read-only after construction and may be used
// from anywhere.
type KMSSigner struct {
	client     AsymmetricSigner
	keyVersion string
	direction  string
}

// NewKMSSigner builds the signer a paired-the-new-way reply or heartbeat is
// signed with.
//
// The client, the key version and the direction are all required, and each
// refusal names what is missing rather than defaulting: a signer with no client
// signs nothing, a signer naming no key version names no key KMS will sign with
// (an asymmetric key has no primary version to fall back to), and a signer
// signing for a node that is neither the outbox nor the heartbeat produces
// signatures the phone's verifier — which is handed the node it read the value
// from — rejects.
func NewKMSSigner(client AsymmetricSigner, keyVersion, direction string) (*KMSSigner, error) {
	if client == nil {
		return nil, errors.New("mailbox: a KMS signer has no client, so it can sign nothing")
	}
	version := strings.TrimSpace(keyVersion)
	if version == "" {
		return nil, errors.New("mailbox: a KMS signer names no key version, and an asymmetric key has no primary version to fall back to")
	}
	switch direction {
	case directionOutbox, directionHeartbeat:
	default:
		return nil, fmt.Errorf("mailbox: a KMS signer signs for the %s or the %s node, and %q is neither: a value signed for another node is one no reader verifies, with no error to say so", directionOutbox, directionHeartbeat, direction)
	}
	return &KMSSigner{client: client, keyVersion: version, direction: direction}, nil
}

// Direction is the node this signer signs for. It is safe to print: the
// direction is in the signed bytes and is not a secret.
func (s *KMSSigner) Direction() string {
	if s == nil {
		return ""
	}
	return s.direction
}

// KeyVersion is the KMS key version this signer signs under, as the deployment
// names it. It is safe to print.
func (s *KMSSigner) KeyVersion() string {
	if s == nil {
		return ""
	}
	return s.keyVersion
}

// String is what a log line about the signer says: what it is and the key
// version it signs under, which is a resource name and never key material.
func (s *KMSSigner) String() string {
	if s == nil {
		return "mailbox.KMSSigner(nil)"
	}
	return fmt.Sprintf("mailbox.KMSSigner{direction: %s, key version: %s}", s.direction, s.keyVersion)
}

// Signature is the value's signature: the Ed25519 signature over
// CanonicalBytes(value, this signer's direction), which is the bytes `MacOf`
// hashes and therefore the bytes the phone's verifier rebuilds from the node it
// read (J5c). It answers the unpadded-base64url spelling `sig` carries, and it
// is over the value the caller built — the value is built first, signed second
// and `sig` added last, so what the signature covers is exactly what lands
// (§8.2.1).
//
// A value the canonical serialisation refuses is an error rather than a
// signature, exactly as it is for the MAC signer, and for the same reason:
// signing bytes a reader will not see is the failure mode the canonical form
// exists to prevent. Every error here is ErrUnsignable's — M4's per-write
// refusal, the one that writes nothing for that value and owes the reply again
// on the next pass — so a caller never has to guess which of M4's three rows it
// is looking at.
func (s *KMSSigner) Signature(ctx context.Context, value Record) (string, error) {
	if s == nil || s.client == nil {
		return "", fmt.Errorf("%w: there is no signer", ErrUnsignable)
	}
	payload, err := CanonicalBytes(value, s.direction)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnsignable, err)
	}
	signature, err := s.sign(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnsignable, err)
	}
	return base64.RawURLEncoding.EncodeToString(signature), nil
}

// sign is the one call out, with the checks a signature has to pass before it
// is one: the payload is something to sign and inside the bound, and what comes
// back is the 64 bytes an Ed25519 signature is.
//
// **Why the answer is checked and not just used.** A signature is what makes a
// reply attributable to this installation, so an answer that arrived truncated
// would be a value written as signed that no phone accepts — and, worse, a
// signature that fires the M4 ledger as if it were one. The length is the whole
// of what this side can check: whether the signature verifies under the public
// key is the phone's and the shared vectors' (M9), and there is no secret here
// to check anything against.
func (s *KMSSigner) sign(ctx context.Context, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("mailbox: a signature over no bytes is one every empty payload shares, so there is nothing to sign")
	}
	if len(payload) > maxSignBytes {
		return nil, fmt.Errorf("mailbox: the value is %d bytes, over the %d a signing call reads", len(payload), maxSignBytes)
	}
	signature, err := s.client.SignAsymmetric(ctx, s.keyVersion, payload)
	if err != nil {
		return nil, fmt.Errorf("mailbox: the key ring's signing call: %w", err)
	}
	if len(signature) != ConsumerSignatureSize {
		return nil, fmt.Errorf("mailbox: the key ring answered a %d-byte signature, and an Ed25519 signature is %d bytes, so what came back is not one the phone could verify", len(signature), ConsumerSignatureSize)
	}
	return signature, nil
}
