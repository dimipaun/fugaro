// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The root key and the signed key set (plan A4; spec "Assertion signing keys";
// D2792 as amended by O7, O15 and O18).
//
// **What the root key is for.** A verifier accepts a fetched key set only
// inside an envelope the root key signed (P1), so whoever can change what the
// keys URL serves — a deployer of the identity service, a changed bucket object
// — still cannot publish keys of their own. The root key is what makes the
// pinned public keys in a build more than a snapshot: a rotation is a new KMS
// key version and one signed set, and no client release is needed.
//
// **Where it lives, and who can use it.** In Cloud KMS, in a project of its own
// that the owner creates and owns, as an ASYMMETRIC_SIGN Ed25519 key whose
// private half is generated inside the key store and read out by nothing
// (ee/identitysvc/rootkey/terraform, Check 737). Signing is granted to exactly
// one member, a separate owner identity given as a variable, and to no service
// account: no `fugaro` command, no service and no agent holds the key, and this
// file reaches it only through the signing API (.claude/rules/ee.md R5).
//
// **The envelope is C1's and not this file's.** The signing input is the JWS
// header and the payload exactly as internal/mobilesession's structs serialise
// them — field order as declared, no whitespace — and
// shared/mobile-session/vectors.json is the one document that fixes that form.
// keyset_test.go replays the vector's own bytes through SignKeySet, so a change
// here that moved a field or added a space is the vector's test failing rather
// than a key set every client refuses months later.
//
// **The two fences this file holds.** The command that signs refuses inside a
// coding agent's session (O15, RefuseAgentSession), and a set whose `seq` is
// not above the last signed one is refused before the root key is asked for a
// signature at all (O7): a verifier compares that number against the floor its
// build pinned and the highest one it has stored, so a lower set served by a
// cache or a stale write walks a project back onto older keys.

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"time"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/mobilesession"
)

const (
	// KeySetLifetime is how long a signed key set stays valid: 180 days on from
	// the instant it was signed (O18). A verifier refuses a set past its `exp`
	// (P1) and A3's root alerts 30 days before it, so a set that lapses while
	// nobody is looking is every project refusing new pairings and restores.
	KeySetLifetime = 180 * 24 * time.Hour

	// MaxKeySetBytes bounds a key-set document on either side of this file: the
	// JWKS the command is given and the set it writes. It is the bound A2's
	// reader and the service hold (cmd/identitysvc), so a document one accepts
	// is one the other reads.
	MaxKeySetBytes = 64 << 10

	// maxRootKeySignBytes bounds one payload handed to the root key. A key set
	// is a small JSON document and Cloud KMS refuses a large one itself; the
	// bound is here so that a request this command should never have built is
	// refused before it is sent rather than read as a key ring that failed.
	maxRootKeySignBytes = 64 << 10
)

// rootKeyCRC is the checksum Cloud KMS counts in, and it is Castagnoli's and
// not the IEEE one: the field is named crc32c on both sides of the call.
var rootKeyCRC = crc32.MakeTable(crc32.Castagnoli)

// RootKeyRing is the whole of what signing a key set needs of Cloud KMS: sign
// bytes with one key version, and read the public half the envelope's `kid`
// names. *kms.KeyManagementClient is it as it stands.
//
// Two methods and no more, so a test holds the signing to a key generated in
// its own process rather than to a service — and so a reader of this file sees
// that nothing here can create a key, read a private half or export one.
type RootKeyRing interface {
	AsymmetricSign(ctx context.Context, req *kmspb.AsymmetricSignRequest, opts ...gax.CallOption) (*kmspb.AsymmetricSignResponse, error)
	GetPublicKey(ctx context.Context, req *kmspb.GetPublicKeyRequest, opts ...gax.CallOption) (*kmspb.PublicKey, error)
}

// RootKey is one root key version, reached through Cloud KMS's own APIs.
type RootKey struct {
	ring    RootKeyRing
	version string
}

// NewRootKey builds the signer over one root key version's resource name.
//
// The version is required and is not a default: an asymmetric key has no primary
// version for the API to fall back to, so a signer built with none would sign
// nothing at all — and the failure would arrive at the ceremony rather than at
// the start, which is the shape an owner-run step is most expensive in.
func NewRootKey(ring RootKeyRing, keyVersion string) (*RootKey, error) {
	if ring == nil {
		return nil, errors.New("identitysvc: a root key signer has no client, so it can sign nothing")
	}
	version := strings.TrimSpace(keyVersion)
	if version == "" {
		return nil, errors.New("identitysvc: a root key signer names no key version, and an asymmetric key has no primary version to fall back to")
	}
	return &RootKey{ring: ring, version: version}, nil
}

// Sign hands the signing input to the key version's signing API and answers the
// signature it made.
//
// The payload travels as the request's `data` and not as a digest, because the
// root key is Ed25519 (O7) and a managed Ed25519 key signs the payload itself: a
// digest would be signed as opaque bytes and what answered would be a signature
// no verifier of this format accepts.
//
// The checks are ee/keyservice/kmssigner.go's, copied the way
// cmd/identitysvc's signer copies them: Cloud KMS answers a CRC32C of the
// signature beside it and checks one sent with the payload, so both directions
// are compared. Neither check is a cryptographic one — the project's verifier is
// that — but they are what tells an answer that arrived damaged or in part from
// a whole one before a key set is assembled around it, which is the one failure
// this whole task exists to keep out of the served document.
func (k *RootKey) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("identitysvc: a signature over no bytes is one every empty payload shares, so there is no key set to sign")
	}
	if len(payload) > maxRootKeySignBytes {
		return nil, fmt.Errorf("identitysvc: the signing input is %d bytes, over the %d a signing call reads", len(payload), maxRootKeySignBytes)
	}
	sum := int64(crc32.Checksum(payload, rootKeyCRC))
	answer, err := k.ring.AsymmetricSign(ctx, &kmspb.AsymmetricSignRequest{
		Name:       k.version,
		Data:       payload,
		DataCrc32C: wrapperspb.Int64(sum),
	})
	if err != nil {
		return nil, fmt.Errorf("identitysvc: the root key's signing API: %w", err)
	}
	if !answer.GetVerifiedDataCrc32C() {
		return nil, fmt.Errorf("identitysvc: the root key did not check the %d byte(s) it was asked to sign, so what it signed is not the signing input this command built", len(payload))
	}
	// The answer names the key version that signed, and it is compared rather
	// than read: a signature under a version this command did not ask for is a
	// key set that verifies against a root nobody pinned.
	if signed := answer.GetName(); signed != "" && signed != k.version {
		return nil, fmt.Errorf("identitysvc: the root key signed with %s, and this command signs under %s", signed, k.version)
	}
	signature := answer.GetSignature()
	if len(signature) == 0 {
		return nil, errors.New("identitysvc: the root key answered an empty signature")
	}
	if counted := answer.GetSignatureCrc32C(); counted == nil || counted.Value != int64(crc32.Checksum(signature, rootKeyCRC)) {
		return nil, fmt.Errorf("identitysvc: the root key answered a %d-byte signature whose bytes are not the ones it counted, so the signature is not the one it made", len(signature))
	}
	return signature, nil
}

// Public reads the key version's public half from Cloud KMS.
//
// It is read rather than taken from the caller because the value is what P8 pins
// into a build and what every client verifies a set against: a public key typed
// beside the command would be a value nothing connects to the key that signed,
// and the worst reading this ceremony can produce is a pinned key that is not
// the signing one. The read is of the same version the signature came from, so
// the two are the same key by construction.
//
// The algorithm is checked as well: a key version that is not the Ed25519 one
// O7 fixes would sign a document no verifier accepts, and an owner who pointed
// the command at another key of the ring finds that out here rather than after
// the set is published.
func (k *RootKey) Public(ctx context.Context) (ed25519.PublicKey, error) {
	answer, err := k.ring.GetPublicKey(ctx, &kmspb.GetPublicKeyRequest{Name: k.version})
	if err != nil {
		return nil, fmt.Errorf("identitysvc: reading the root key's public half: %w", err)
	}
	if got := answer.GetAlgorithm(); got != kmspb.CryptoKeyVersion_EC_SIGN_ED25519 {
		return nil, fmt.Errorf("identitysvc: the key version is a %s key, and the root key O7 fixes is EC_SIGN_ED25519", got)
	}
	document := answer.GetPem()
	if len(document) == 0 {
		return nil, errors.New("identitysvc: the root key answered no public half")
	}
	// The PEM travels with a CRC32C of its bytes, and it is compared for the
	// same reason the signature's is: a public key that arrived damaged is a
	// value P8 would pin into every build.
	if counted := answer.GetPemCrc32C(); counted != nil && counted.Value != int64(crc32.Checksum([]byte(document), rootKeyCRC)) {
		return nil, errors.New("identitysvc: the root key answered a public half whose bytes are not the ones it counted, so the key is not the one it holds")
	}
	block, _ := pem.Decode([]byte(document))
	if block == nil {
		return nil, errors.New("identitysvc: the root key's public half is not a PEM document")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identitysvc: the root key's public half is not a public key: %w", err)
	}
	public, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("identitysvc: the root key's public half is a %T, and the root key is Ed25519", parsed)
	}
	if len(public) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("identitysvc: the root key's public half is %d bytes, and an Ed25519 one is %d", len(public), ed25519.PublicKeySize)
	}
	return public, nil
}

// KeySetRequest is one signed key set to build: the keys to publish, the
// sequence number that orders this set against the ones before it, the root
// key's own id, and the document being replaced.
type KeySetRequest struct {
	// JWKS is the identity service's published keys, as C1's contract holds
	// them (mobilesession.JWKS).
	JWKS mobilesession.JWKS
	// Seq orders this set against every other. A verifier refuses a set whose
	// `seq` is below the floor its build pinned or the highest one it has
	// stored, so this number only ever goes up (O7).
	Seq int64
	// RootKID is the root key's own id, which the envelope's header carries. It
	// is the *root's*: the keys in the payload have ids of their own, and a
	// reader that mixed the two up would verify an envelope with a key the
	// envelope brought (C1, mobilesession.KeySetHeader).
	RootKID string
	// Previous is the signed document this one replaces — the set A2 serves
	// just now, as it stands — or nil for the identity project's first. A `seq`
	// not above it is refused.
	Previous []byte
}

// SignKeySet builds C1's envelope over the request's keys, refuses a `seq` that
// is not above the previous document's, has the root key sign the signing
// input, and answers the compact JWS and the root key's public half.
//
// The refusal comes before the signing call and not after: a document the
// command will not publish is one the root key must not have signed at all, and
// keyset_test.go holds that by counting the signatures its fake key ring was
// asked for.
func SignKeySet(ctx context.Context, root *RootKey, req KeySetRequest, now time.Time) ([]byte, mobilesession.JWK, error) {
	var none mobilesession.JWK
	if root == nil {
		return nil, none, errors.New("identitysvc: a key set is signed by a root key, and none was given")
	}
	if err := checkKeySetSeq(req.Previous, req.Seq); err != nil {
		return nil, none, err
	}
	if err := checkKeySetID(req.RootKID); err != nil {
		return nil, none, err
	}
	jwks, err := normaliseKeySet(req.JWKS)
	if err != nil {
		return nil, none, err
	}
	header, err := json.Marshal(mobilesession.KeySetHeader{
		Alg: mobilesession.KeySetAlg,
		Kid: req.RootKID,
		Typ: mobilesession.KeySetTyp,
	})
	if err != nil {
		return nil, none, fmt.Errorf("identitysvc: the key set's header: %w", err)
	}
	payload, err := json.Marshal(mobilesession.KeySet{
		Format: mobilesession.KeySetFormat,
		Seq:    req.Seq,
		Iat:    now.Unix(),
		Exp:    now.Add(KeySetLifetime).Unix(),
		JWKS:   jwks,
	})
	if err != nil {
		return nil, none, fmt.Errorf("identitysvc: the key set's payload: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature, err := root.Sign(ctx, []byte(signingInput))
	if err != nil {
		return nil, none, err
	}
	public, err := root.Public(ctx)
	if err != nil {
		return nil, none, err
	}
	rootJWK := mobilesession.JWK{
		Kty: "OKP",
		Crv: "Ed25519",
		X:   base64.RawURLEncoding.EncodeToString(public),
		Kid: req.RootKID,
		Alg: mobilesession.KeySetAlg,
		Use: "sig",
	}
	document := signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
	if len(document) > MaxKeySetBytes {
		return nil, none, fmt.Errorf("identitysvc: the signed key set is %d bytes, over the %d a reader takes", len(document), MaxKeySetBytes)
	}
	return []byte(document), rootJWK, nil
}

// ParseKeySetJWKS reads the JWKS the signing command is given: the public halves
// to publish, in the form A2 serves at RouteJWKS. A key the payload cannot
// verify with is refused here, before the root key is asked to sign anything
// over it.
//
// The decode is strict: a field this shape does not carry is a refusal naming
// it, because the alternative is a document that reads as a key set and holds
// something else — and the `alg` and `use` a set may carry are members of C1's
// JWK (mobilesession), which normaliseKeySet fills in rather than reads.
func ParseKeySetJWKS(document []byte) (mobilesession.JWKS, error) {
	decoder := json.NewDecoder(strings.NewReader(string(document)))
	decoder.DisallowUnknownFields()
	var jwks mobilesession.JWKS
	if err := decoder.Decode(&jwks); err != nil {
		return mobilesession.JWKS{}, fmt.Errorf("identitysvc: the JWKS to sign is not a key set: %w", err)
	}
	return normaliseKeySet(jwks)
}

// normaliseKeySet holds the keys a signed set may carry to the one shape every
// verifier accepts: OKP, Ed25519, a kid, and the fixed algorithm and use. The
// two members the plain JWKS omits are filled in here, so the document A2 serves
// and the document the root key signs carry the same key either way.
func normaliseKeySet(jwks mobilesession.JWKS) (mobilesession.JWKS, error) {
	if len(jwks.Keys) == 0 {
		return mobilesession.JWKS{}, errors.New("identitysvc: a key set with no keys is one nothing verifies against, so there is nothing to sign")
	}
	keys := make([]mobilesession.JWK, 0, len(jwks.Keys))
	seen := map[string]bool{}
	for _, key := range jwks.Keys {
		if key.Kty != "OKP" || key.Crv != "Ed25519" {
			return mobilesession.JWKS{}, fmt.Errorf("identitysvc: the key %q is a %s/%s key, and the identity service publishes OKP/Ed25519 and nothing else", key.Kid, key.Kty, key.Crv)
		}
		if strings.TrimSpace(key.Kid) == "" {
			return mobilesession.JWKS{}, errors.New("identitysvc: a key in the set names no key id, so an assertion naming it could not be verified")
		}
		if key.Alg != "" && key.Alg != mobilesession.KeySetAlg {
			return mobilesession.JWKS{}, fmt.Errorf("identitysvc: the key %q carries the algorithm %q, and %s is the only one the contract holds", key.Kid, key.Alg, mobilesession.KeySetAlg)
		}
		if key.Use != "" && key.Use != "sig" {
			return mobilesession.JWKS{}, fmt.Errorf("identitysvc: the key %q carries the use %q, and a signing key's use is \"sig\"", key.Kid, key.Use)
		}
		raw, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil {
			return mobilesession.JWKS{}, fmt.Errorf("identitysvc: the key %q's public half is not base64url: %w", key.Kid, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return mobilesession.JWKS{}, fmt.Errorf("identitysvc: the key %q's public half is %d bytes, and an Ed25519 one is %d", key.Kid, len(raw), ed25519.PublicKeySize)
		}
		if seen[key.Kid] {
			return mobilesession.JWKS{}, fmt.Errorf("identitysvc: two keys carry the key id %q, and a verifier reading that id cannot tell which one signed", key.Kid)
		}
		seen[key.Kid] = true
		key.Alg = mobilesession.KeySetAlg
		key.Use = "sig"
		keys = append(keys, key)
	}
	return mobilesession.JWKS{Keys: keys}, nil
}

// checkKeySetSeq refuses a set that is not above the one it replaces.
//
// "The last signed one" is the document at the destination — the set A2 serves
// just now — and not a number kept anywhere else: it is the only value that
// describes what a verifier can already have fetched, and a second store of it
// would be a second answer to the same question. A previous document whose
// `seq` cannot be read is a refusal rather than a first publish, because this
// command cannot then tell whether the new number is above it.
func checkKeySetSeq(previous []byte, seq int64) error {
	if seq <= 0 {
		return fmt.Errorf("identitysvc: a key set's seq is a positive number, and %d was given: a verifier refuses a set below the floor its build pinned", seq)
	}
	if len(previous) == 0 {
		return nil
	}
	last, err := keySetSeq(previous)
	if err != nil {
		return fmt.Errorf("identitysvc: refusing to sign over a key set whose seq cannot be read: %w", err)
	}
	if seq <= last {
		return fmt.Errorf("identitysvc: the seq %d is not above the last signed set's %d, and a verifier refuses a set below the highest one it has stored (O7): a set that goes backwards walks a project onto keys it has already rotated out", seq, last)
	}
	return nil
}

// checkKeySetID holds the envelope's `kid` to something a reader can name.
func checkKeySetID(kid string) error {
	if strings.TrimSpace(kid) == "" {
		return errors.New("identitysvc: a key set's header names the root key's kid, and none was given: the keys in the set have ids of their own and a reader cannot tell the two apart")
	}
	if kid != strings.TrimSpace(kid) {
		return fmt.Errorf("identitysvc: the root key's kid %q has whitespace around it, and a key id is written as it is", kid)
	}
	return nil
}

// keySetSeq reads the sequence number a signed key-set document carries.
func keySetSeq(document []byte) (int64, error) {
	segments := strings.Split(string(document), ".")
	if len(segments) != 3 {
		return 0, fmt.Errorf("the signed key set has %d segment(s), and a JWS compact serialization has three", len(segments))
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return 0, fmt.Errorf("the signed key set's payload is not base64url: %w", err)
	}
	var claims struct {
		Seq int64 `json:"seq"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, fmt.Errorf("the signed key set's payload is not a claim set: %w", err)
	}
	if claims.Seq <= 0 {
		return 0, fmt.Errorf("the signed key set carries the seq %d, and a set's seq is a positive number", claims.Seq)
	}
	return claims.Seq, nil
}

// RefuseAgentSession is the fence O15 puts around the root key: signing a key
// set is refused in a coding agent's session, the way `fugaro user` refuses a
// write that changes who may do what.
//
// It is a mitigation and not a barrier — an agent that unsets its own variables
// is not stopped, and IAM is the control, since signing is granted to one owner
// identity that an agent's session does not hold — but it stops the accident,
// and it is what makes an agent's session unable to run the ceremony at all.
// The classification is internal/agentenv's: an agent-set marker refuses
// whatever the terminal, and the IDE extension's marker alone refuses only
// without a real terminal on the run's input, because a person typing in their
// own IDE terminal is not an agent.
func RefuseAgentSession(getenv func(string) string, terminal bool) error {
	if marker := agentenv.Session(getenv, terminal); marker != "" {
		return fmt.Errorf("identitysvc: refusing to sign a key set in a coding agent's session (%s is set): the root key's signing command is run by a person at their own terminal, as the separate owner identity the root key project grants (O15). Nothing was signed", marker)
	}
	return nil
}
