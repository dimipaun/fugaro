// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The key material the identity service publishes, and the two documents a
// verifier reads it from (plan A2; spec "Resolved decisions", "Assertion
// signing keys"; D2792 as amended by O7).
//
// **There are two documents and they are not the same one.** The pinned URL
// answers the root-signed key set A4's signing command wrote — the document a
// build pins a root key and fetches, and the only one a verifier accepts keys
// from, because it is the only one the root key signed (P1). The plain JWKS
// sits beside it: the same keys, unsigned, in RFC 7517's form, for a reader
// that wants to look at them and not to verify with them. Nothing verifies
// against the plain set, and nothing should: whoever can answer this service's
// request can write it.
//
// **The signed document is served and never read.** This half holds no root
// key, cannot sign one and does not verify one (O7): the signature is a
// consumer's check (P1), and what is served here is the byte string the owner's
// command produced, so that what a verifier checks is what the owner signed.
// The one member read out of it is the expiry — the figure A3's alert fires on
// 30 days before it (O18).
//
// **No private half is reachable from here.** A JWK is built from a SigningKey,
// which carries a public half and nothing else (rotation.go), and the type this
// file marshals has no member a private key could travel in
// (.claude/rules/ee.md R5; jwks_test.go's TestJWKSCarriesNoPrivateMaterial).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// RouteKeySet is the pinned URL a verifier fetches: the root-signed key-set
	// document, served byte for byte (O7). A build pins this path into the
	// verifier (P8) and the installation publishes the route that answers it
	// (A3), so a change here is a change in both.
	RouteKeySet = "/.well-known/keyset.jws"
	// RouteJWKS is the plain JWKS beside it.
	RouteJWKS = "/.well-known/jwks.json"
)

// JWK is one public key in RFC 7517's form, for the one key type this service
// publishes: an Ed25519 public key is OKP with the crv Ed25519, and x is its
// raw bytes in base64url without padding.
//
// No `alg` and no `use` are carried. The contract accepts EdDSA and refuses
// every other algorithm (P1), so an `alg` here would advertise a choice where
// there is none, and a reader that took it as one is the confusion the
// algorithm list exists to prevent.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
}

// JWKS is the published set. Keys is never null: a service whose first key has
// not started answers an empty list, so a reader that iterates what it fetched
// iterates nothing rather than reading a null as a broken document.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// SignedKeySet is where the root-signed key-set document is read from: the
// object A4's signing command writes and the owner puts in the bucket this
// service reads (A3). It is an interface because that read is the deployment's
// — a bucket in production, a file on a disk in a test — and because this half
// must be able to answer without holding anything that could write one.
type SignedKeySet interface {
	Document(ctx context.Context) ([]byte, error)
}

// FileKeySet reads the document from a path on disk.
type FileKeySet struct {
	path string
}

// NewFileKeySet returns the file-backed source over path.
func NewFileKeySet(path string) (*FileKeySet, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("identitysvc: a file-backed key set names no path")
	}
	return &FileKeySet{path: path}, nil
}

// Document returns the document as it stands, and refuses an empty one: a file
// that is there and holds nothing is not a key set, and serving it would answer
// a verifier with a document no root key signed.
func (f *FileKeySet) Document(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	document, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("identitysvc: the signed key set at %s: %w", f.path, err)
	}
	if len(document) == 0 {
		return nil, fmt.Errorf("identitysvc: the signed key set at %s is empty", f.path)
	}
	return document, nil
}

// KeySet serves the identity service's published key material.
type KeySet struct {
	rotation *Rotation
	signed   SignedKeySet
	now      func() time.Time
}

// KeySetOption configures a KeySet.
type KeySetOption func(*KeySet)

// WithKeySetClock sets the clock the published set is chosen with (tests).
func WithKeySetClock(now func() time.Time) KeySetOption {
	return func(k *KeySet) { k.now = now }
}

// NewKeySet returns the published key material over a rotation and a source of
// the signed document.
//
// Both are required and neither is a default: a set with no rotation publishes
// nothing, and one with no document answers the pinned URL with nothing — the
// two failures a verifier cannot tell from a service that is simply down.
func NewKeySet(rotation *Rotation, signed SignedKeySet, opts ...KeySetOption) (*KeySet, error) {
	if rotation == nil {
		return nil, errors.New("identitysvc: a key set over no rotation publishes nothing")
	}
	if signed == nil {
		return nil, errors.New("identitysvc: a key set with no signed key set to serve answers the pinned URL with nothing")
	}
	k := &KeySet{rotation: rotation, signed: signed, now: time.Now}
	for _, o := range opts {
		o(k)
	}
	return k, nil
}

// JWKS returns the plain set this service publishes at the instant its clock
// reads: the keys of the moment, newest first (rotation.go).
func (k *KeySet) JWKS() JWKS {
	published := k.rotation.Published(k.now())
	keys := make([]JWK, 0, len(published))
	for _, key := range published {
		keys = append(keys, JWK{
			Kty: "OKP",
			Crv: "Ed25519",
			Kid: key.KID,
			X:   base64.RawURLEncoding.EncodeToString(key.Public),
		})
	}
	return JWKS{Keys: keys}
}

// DaysLeft reports the whole days between now and the newest signed key set's
// expiry, floored: the figure A3's alert fires on 30 days before it, and a set
// past its expiry is a figure below zero rather than one that reads as valid.
func (k *KeySet) DaysLeft(ctx context.Context) (int, error) {
	document, err := k.signed.Document(ctx)
	if err != nil {
		return 0, err
	}
	expiry, err := keySetExpiry(document)
	if err != nil {
		return 0, err
	}
	return int(math.Floor(expiry.Sub(k.now()).Hours() / 24)), nil
}

// Mount registers the two documents on mux. The method is part of the route, so
// a write to either is answered by the mux's own refusal rather than by a
// handler's.
func (k *KeySet) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+RouteKeySet, k.serveSignedKeySet)
	mux.HandleFunc("GET "+RouteJWKS, k.serveJWKS)
}

// serveSignedKeySet answers the pinned URL with A4's document, unchanged.
func (k *KeySet) serveSignedKeySet(w http.ResponseWriter, r *http.Request) {
	document, err := k.signed.Document(r.Context())
	if err != nil {
		// A failure here is this service's dependency and not the caller's
		// request: a verifier that reads it as a refusal of an assertion looks
		// for a bad assertion where the fault is an unpublished key set, and a
		// 503 is the one answer that says come back.
		log.Printf("%v", err)
		http.Error(w, "The key set is not published just now. Please try again.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/jose")
	_, _ = w.Write(document)
}

// serveJWKS answers the plain JWKS.
func (k *KeySet) serveJWKS(w http.ResponseWriter, _ *http.Request) {
	document, err := json.Marshal(k.JWKS())
	if err != nil {
		log.Printf("identitysvc: the published key set could not be written: %v", err)
		http.Error(w, "The key set could not be served just now. Please try again.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/jwk-set+json")
	_, _ = w.Write(document)
}

// keySetExpiry reads the `exp` a signed key-set document carries.
//
// The document is the JWS compact form C1's contract fixes — internal/mobilesession's
// KeySet, and the key_set section of shared/mobile-session/vectors.json, which
// jwks_test.go's TestKeySetExpiryReadsC1sVector replays — three base64url
// segments, and the claims the root key signed in the payload's `exp`, `iat`
// and `seq`. The payload is read for its expiry and left otherwise alone — the
// envelope's whole shape is the contract's and A4's to write, and a reader that
// refused a member it does not model would refuse a set the moment the contract
// grew one. The signature is not read: verifying it is the consumer's check
// (P1), and this service holds no root key and cannot sign one (O7).
func keySetExpiry(document []byte) (time.Time, error) {
	segments := strings.Split(string(document), ".")
	if len(segments) != 3 {
		return time.Time{}, fmt.Errorf("identitysvc: the signed key set has %d segment(s), and a JWS compact serialization has three", len(segments))
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("identitysvc: the signed key set's payload is not base64url: %w", err)
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("identitysvc: the signed key set's payload is not a claim set: %w", err)
	}
	if claims.ExpiresAt == 0 {
		return time.Time{}, errors.New("identitysvc: the signed key set names no expiry, so nothing says when it stops being valid")
	}
	return time.Unix(claims.ExpiresAt, 0).UTC(), nil
}
