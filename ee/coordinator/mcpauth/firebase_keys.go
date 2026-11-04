// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

// The published key set the ID tokens are checked against.
//
// Google publishes the keys that sign a project's Firebase ID tokens as a JWKS
// document — the owner's spec §0 names exactly that — and the set is the only
// proof a token has: the kid in a token's header is a hint about which key to
// look for and never evidence, which is why nothing here reads a claim before
// the signature has been checked against something Google published.
//
// The reading is a cached one, in internal/credsvc's shape for the same
// endpoint's x509 twin: one fetch per cache life, the lock held across it so
// two cold calls wait for one fetch rather than racing two, and one extra fetch
// when a token names a kid the cached set does not hold — a rotation would
// otherwise refuse every person until the cache ran out, which is up to a day.
// That extra fetch is rate-limited, so a caller naming random kids cannot make
// this front door read Google's endpoint in a loop.

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// SecureTokenJWKSURL is where Google publishes the keys that sign Firebase
	// ID tokens.
	SecureTokenJWKSURL = "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"
	// maxKeysBytes bounds the published document before it is parsed.
	maxKeysBytes = 1 << 20
	// maxModulusBytes bounds one published modulus: 16384 bits, four times the
	// longest key Google publishes, and a document past it is refused as
	// unreadable rather than turned into a very slow verification.
	maxModulusBytes = 2048
	// keyCacheDefault, and the bounds around Google's Cache-Control: the set
	// may be cached for as long as Google says, clamped to these.
	keyCacheDefault = time.Hour
	keyCacheMin     = time.Minute
	keyCacheMax     = 24 * time.Hour
)

var (
	// ErrUnknownKeyID is a token whose key id the published set does not hold.
	// It is a refusal of THAT token and not a failure of this service, which is
	// the distinction VerifyIDToken keeps when it wraps it in
	// ErrUnverifiedIDToken.
	ErrUnknownKeyID = errors.New("mcpauth: no published key for the token's key id")
	// ErrUnavailable is this service being unable to read the key set at all.
	// It is the answer to a key endpoint that timed out, answered an error, or
	// published something this code cannot read — and it is deliberately not a
	// refusal of the token, because an operator who read it as one would look
	// for a bad token where the fault is the key set.
	ErrUnavailable = errors.New("mcpauth: the published key set could not be read")
)

// KeySource returns the RSA public key a token's kid names. A kid that is not
// in the set is ErrUnknownKeyID; a set that could not be read is ErrUnavailable.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// SecureTokenKeys is the KeySource over Google's published JWKS for the
// securetoken signer.
type SecureTokenKeys struct {
	url     string
	hc      *http.Client
	now     func() time.Time
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
	fetched bool
	// forced is when this source last read the set outside the cache's life,
	// because a token named a kid the cached set did not hold. It is
	// rate-limited by keyCacheMin.
	forced time.Time
}

// KeysOption configures SecureTokenKeys.
type KeysOption func(*SecureTokenKeys)

// WithKeysEndpoint points the key source at another URL (tests).
func WithKeysEndpoint(u string) KeysOption { return func(g *SecureTokenKeys) { g.url = u } }

// WithKeysHTTPClient sets the HTTP client (tests).
func WithKeysHTTPClient(c *http.Client) KeysOption { return func(g *SecureTokenKeys) { g.hc = c } }

// WithKeysClock sets the clock the cache's life is measured with (tests).
func WithKeysClock(now func() time.Time) KeysOption {
	return func(g *SecureTokenKeys) { g.now = now }
}

// NewSecureTokenKeys returns the key source over Google's published set.
func NewSecureTokenKeys(opts ...KeysOption) *SecureTokenKeys {
	g := &SecureTokenKeys{url: SecureTokenJWKSURL, hc: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Key returns the public key for kid, reading the published set when it is not
// cached. No error here names the kid: it comes out of a token this service was
// handed, so it may be any length and any bytes, and a refusal that repeated it
// would put a stranger's text in a log line.
func (g *SecureTokenKeys) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if kid == "" {
		return nil, fmt.Errorf("%w: the token names no key id", ErrUnknownKeyID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.fetched || !g.now().Before(g.expires) {
		if err := g.fetch(ctx); err != nil {
			return nil, err
		}
	}
	if key, ok := g.keys[kid]; ok {
		return key, nil
	}
	if g.now().Sub(g.forced) >= keyCacheMin {
		g.forced = g.now()
		if err := g.fetch(ctx); err != nil {
			return nil, err
		}
		if key, ok := g.keys[kid]; ok {
			return key, nil
		}
	}
	return nil, fmt.Errorf("%w: the token names a key id Google does not publish", ErrUnknownKeyID)
}

// fetch reads and parses the published set, and installs it. Nothing is
// installed on error: a failed fetch leaves the previous set and its expiry
// alone.
func (g *SecureTokenKeys) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url, nil)
	if err != nil {
		return fmt.Errorf("%w: the key endpoint is not a usable URL", ErrUnavailable)
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: reading the published keys failed", ErrUnavailable)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeysBytes))
	if err != nil {
		return fmt.Errorf("%w: reading the published keys failed", ErrUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: the key endpoint answered %d", ErrUnavailable, resp.StatusCode)
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	g.keys, g.fetched = keys, true
	g.expires = g.now().Add(cacheLife(resp.Header.Get("Cache-Control")))
	return nil
}

// jwksDocument is the published set: the JWKS the endpoint serves, of which
// only the RSA signing keys are read.
type jwksDocument struct {
	Keys []jwksKey `json:"keys"`
}

type jwksKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	// N and E are the modulus and the exponent, base64url of their big-endian
	// bytes: the only two fields a signature is checked with.
	N string `json:"n"`
	E string `json:"e"`
}

// parseJWKS reads the document the endpoint publishes into a key set.
//
// An entry of another kind, another use or another algorithm is skipped rather
// than refused: a set that grows an encryption key must not stop this front
// door from reading the signing keys it already held. An entry this code cannot
// read at all is a different matter — the document is then not the set it
// claims to be, and the whole read is ErrUnavailable rather than a set quietly
// missing a key.
func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("the published key set is not a JWKS document")
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		if k.Kid == "" {
			return nil, errors.New("a published key carries no key id")
		}
		key, err := rsaKeyFromJWK(k)
		if err != nil {
			return nil, err
		}
		keys[k.Kid] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("the published set holds no signing key")
	}
	return keys, nil
}

// rsaKeyFromJWK reads one published entry. Every refusal here describes the
// document rather than the value that failed it, for the reason the package
// keeps no-echo.
func rsaKeyFromJWK(k jwksKey) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.N, "="))
	if err != nil || len(n) == 0 || len(n) > maxModulusBytes {
		return nil, errors.New("a published key's modulus is not a usable base64url value")
	}
	e, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.E, "="))
	if err != nil || len(e) == 0 || len(e) > 4 {
		return nil, errors.New("a published key's exponent is not a usable base64url value")
	}
	exp := new(big.Int).SetBytes(e)
	// The exponent is held to the range rsa can verify with: a value outside it
	// is a document this code cannot read rather than one it verifies slowly.
	if !exp.IsInt64() || exp.Int64() < 3 || exp.Int64() > math.MaxInt32 {
		return nil, errors.New("a published key's exponent is outside the range a signature is verified with")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exp.Int64())}, nil
}

// cacheLife is how long the published set may be cached: its own max-age,
// clamped to a minute and a day, an hour when it says nothing.
//
// A directive that says not to store the set at all — no-store, no-cache, or a
// max-age of zero — is not a licence to fall back to the hour: it means the
// shortest life this code allows, which is the floor. A max-age this code
// cannot read is the one thing ignored, because there is nothing to take at its
// word. The directives are read in the order the header spells them, so
// `no-store, max-age=3600` is still `no-store`.
func cacheLife(cacheControl string) time.Duration {
	life := keyCacheDefault
	mayKeep := true
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.TrimSpace(part)
		if part == "no-store" || part == "no-cache" {
			mayKeep = false
			continue
		}
		value, ok := strings.CutPrefix(part, "max-age=")
		if !ok {
			continue
		}
		secs, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		life = time.Duration(secs) * time.Second
	}
	if !mayKeep {
		life = 0
	}
	return min(max(life, keyCacheMin), keyCacheMax)
}
