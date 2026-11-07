// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package oauth

// The member-bound assertion itself: the claims it carries, the key it is
// verified against, and the one mint that produces it (docs/workspaces/spec.md
// §3, "the member-bound assertion").
//
// **The claims are a CLOSED set and that is load-bearing.** An assertion is the
// only value a member accepts from a workspace, so a member that read the
// claims it knows and ignored the rest would accept an assertion carrying
// anything else a workspace chose to put beside them — a scope, a role, a
// limit. Every read here therefore refuses a payload carrying one key this
// type does not model, which is the same reading every config and wire decoder
// in this program gives an unknown field (and the reason a "helpful" new claim
// is a change both halves ship in one release).
//
// **What the assertion does not carry is as load-bearing as what it does**: no
// member token, no scope, no limit and no credential (spec.md §3). The
// member's own limits are the member's own, they are nowhere in this document,
// and the exchange's answer is built from the member's configuration rather
// than from anything a workspace sent.
//
// The encoding is the JWS compact form the MCP design already uses for its ID
// tokens, and the reading is deliberately the same strict one mcpauth's
// decodeSegment applies: base64url, exactly one JSON object, and nothing after
// it. A payload that is one claim set and then another is not a payload whose
// claims are the ones that were signed.

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// The assertion's header. RS256 is the only algorithm this package mints and
// the only one it verifies: "none" is a signature nobody signed, and an
// HMAC-family alg would verify with the key that also mints, which would turn
// the member's pinned public key into a shared secret.
const (
	assertionAlg = "RS256"
	assertionTyp = "JWT"
	// maxAssertionBytes bounds a presented assertion before it is parsed. It is
	// one form field or one header value, and one past the bound is refused at
	// the door rather than decoded.
	maxAssertionBytes = 8 << 10
)

// assertion is an assertion's claims. Every field is minted by the workspace's
// signer (workspace_signer.go) and read by the member's exchange
// (workspace.go), and no field is a grant: the identity fields say who signed
// in, and the member's own lists are what decide whether that person may do
// anything at all (W5).
type assertion struct {
	// Issuer is the workspace's own securetoken issuer — the value the member's
	// pin carries as Issuer, and the same pair with the project id that the
	// workspace's Firebase ID token was verified by at the signer. It is how a
	// member tells one workspace's assertion from another's without reading any
	// per-project key: Google publishes one global Firebase key set, so the
	// aud/iss pair is the whole of what tells two projects apart.
	Issuer string
	// Audience is the member's own exchange URL: the member's `/oauth/authorize`
	// on its own coordinator. It is the binding that makes an assertion minted
	// for one member worthless at another (finding 1).
	Audience string
	// Subject is the workspace's Firebase project id, the same value the issuer
	// ends with. It is carried so that a refusal can name what the assertion was
	// for without parsing the issuer, and the exchange requires it to agree with
	// the pin's project.
	Subject string
	// IssuedAt and ExpiresAt bound the assertion's life, in unix seconds.
	IssuedAt  int64
	ExpiresAt int64
	// JTI is the assertion's own id, and it is what makes the assertion
	// single-use: the member records every jti it has spent, as a hash, for the
	// assertion's lifetime, and a second presentation of the same assertion is
	// refused (finding 1).
	JTI string
	// Email is the address the workspace's Firebase attested, folded to lower
	// case as ASCII. It is checked against the member's own launcher and
	// operator lists and it is never itself a permission.
	Email string
	// EmailVerified is the signer's statement that the address was verified. The
	// member re-checks it rather than taking the signer's word (spec.md §3 step
	// 2): an assertion that says anything but true is refused, because an
	// unverified address is one nobody proved they own.
	EmailVerified bool
	// Provider is the `firebase.sign_in_provider` the sign-in used. The member
	// re-checks it against the pinned set (finding 7) rather than trusting the
	// signer's word, for the same reason: anyone who can mint a custom token for
	// the workspace's project can set the address and its verified flag, and an
	// anonymous sign-in proves no address at all.
	Provider string
}

// assertionClaimKeys is the closed key set of an assertion payload, in the
// order a refusal names them. A payload carrying any other top-level key is
// refused rather than read (the file comment above).
var assertionClaimKeys = []string{
	"iss", "aud", "sub", "iat", "exp", "jti", "email", "email_verified", "provider",
}

// mintAssertion signs an assertion with the workspace's private key. It is the
// signer endpoint's whole answer (workspace_signer.go), and it is a function of
// its arguments alone: no clock, no store and no configuration is read here, so
// a caller that wants a particular window writes it into the claims.
//
// The header carries no kid: a member pins one key, so there is nothing for a
// key id to select between, and a kid the member did not pin would invite a
// reader to look one up.
func mintAssertion(key *rsa.PrivateKey, a assertion) (string, error) {
	if key == nil {
		return "", errors.New("oauth: the signer holds no assertion key")
	}
	if err := a.check(); err != nil {
		return "", err
	}
	head, err := json.Marshal(map[string]string{"alg": assertionAlg, "typ": assertionTyp})
	if err != nil {
		return "", fmt.Errorf("oauth: encoding the assertion header: %w", err)
	}
	body, err := json.Marshal(a.claims())
	if err != nil {
		return "", fmt.Errorf("oauth: encoding the assertion claims: %w", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("oauth: signing the assertion: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// claims is the assertion as the wire carries it. It is a map and not the
// struct: the struct is this package's reading, and the keys are the wire
// contract both halves are held to.
func (a assertion) claims() map[string]any {
	return map[string]any{
		"iss":            a.Issuer,
		"aud":            a.Audience,
		"sub":            a.Subject,
		"iat":            a.IssuedAt,
		"exp":            a.ExpiresAt,
		"jti":            a.JTI,
		"email":          a.Email,
		"email_verified": a.EmailVerified,
		"provider":       a.Provider,
	}
}

// check holds what an assertion must say to be mintable at all: the values the
// signer put in are the signer's own, so anything missing here is a bug in the
// signer rather than a refusal of a caller.
func (a assertion) check() error {
	for _, f := range []struct{ name, value string }{
		{"iss", a.Issuer}, {"aud", a.Audience}, {"sub", a.Subject},
		{"jti", a.JTI}, {"email", a.Email}, {"provider", a.Provider},
	} {
		if f.value == "" {
			return fmt.Errorf("oauth: the assertion has no %s", f.name)
		}
	}
	if a.ExpiresAt <= a.IssuedAt {
		return errors.New("oauth: the assertion's expiry is not after its issue")
	}
	if !a.EmailVerified {
		return errors.New("oauth: the assertion would state an unverified address")
	}
	return nil
}

// verifyAssertion parses a compact assertion and checks its RS256 signature
// against key. What it answers is the claims as they were signed, and NOTHING
// about them is judged here: which issuer, which audience, which window and
// which jti are admissible is the exchange's question (workspace.go), because
// those are the member's own pin and its own consumed-jti set rather than
// properties of the encoding.
//
// Every failure is ErrUnverifiedAssertion with the rule named and no value
// repeated: an assertion is a document a stranger sent, and a refusal reaches a
// log line and an answer body.
func verifyAssertion(key *rsa.PublicKey, raw string) (assertion, error) {
	if key == nil {
		return assertion{}, fmt.Errorf("%w: this member pins no assertion key", ErrUnverifiedAssertion)
	}
	head, claims, sig, err := splitAssertion(raw)
	if err != nil {
		return assertion{}, refusef("%v", err)
	}
	if alg, _ := head["alg"].(string); alg != assertionAlg {
		return assertion{}, refusef("the assertion is not signed with %s", assertionAlg)
	}
	sum := sha256.Sum256([]byte(raw[:len(raw)-len(sig)-1]))
	decoded, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return assertion{}, refusef("the signature is not base64url")
	}
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], decoded); err != nil {
		return assertion{}, refusef("the signature does not verify against the key this member pins")
	}
	return readAssertionClaims(claims)
}

// splitAssertion breaks a compact JWT into its header, its claims and its
// signature segment, refusing anything that is not exactly three non-empty
// segments. The claims come back as a decoded object so that the caller reads
// them once, strictly.
func splitAssertion(raw string) (head, claims map[string]any, sig string, err error) {
	if raw == "" || len(raw) > maxAssertionBytes {
		return nil, nil, "", errors.New("not a JWT of a usable size")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, nil, "", errors.New("not a compact JWT")
	}
	if head, err = decodeSegment(parts[0]); err != nil {
		return nil, nil, "", err
	}
	if claims, err = decodeSegment(parts[1]); err != nil {
		return nil, nil, "", err
	}
	return head, claims, parts[2], nil
}

// readAssertionClaims reads the payload of an assertion whose signature has
// already been verified. Every key is read at its own type and the key set is
// closed, so a claim that is absent, of the wrong type, or one this package
// does not model is a refusal rather than a zero value.
func readAssertionClaims(claims map[string]any) (assertion, error) {
	for _, k := range slices.Sorted(maps.Keys(claims)) {
		if !slices.Contains(assertionClaimKeys, k) {
			return assertion{}, refusef("the assertion carries a claim this member does not read")
		}
	}
	var a assertion
	var err error
	if a.Issuer, err = claimString(claims, "iss"); err != nil {
		return assertion{}, err
	}
	if a.Audience, err = claimString(claims, "aud"); err != nil {
		return assertion{}, err
	}
	if a.Subject, err = claimString(claims, "sub"); err != nil {
		return assertion{}, err
	}
	if a.JTI, err = claimString(claims, "jti"); err != nil {
		return assertion{}, err
	}
	if a.Email, err = claimString(claims, "email"); err != nil {
		return assertion{}, err
	}
	if a.Provider, err = claimString(claims, "provider"); err != nil {
		return assertion{}, err
	}
	if a.IssuedAt, err = claimInt(claims, "iat"); err != nil {
		return assertion{}, err
	}
	if a.ExpiresAt, err = claimInt(claims, "exp"); err != nil {
		return assertion{}, err
	}
	v, ok := claims["email_verified"].(bool)
	if !ok {
		return assertion{}, refusef("the assertion's address is not marked verified")
	}
	a.EmailVerified = v
	return a, nil
}

// refusef builds a refusal of an assertion: every one of them is
// ErrUnverifiedAssertion with the rule that refused it, and none of them
// repeats the assertion's bytes.
func refusef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnverifiedAssertion, fmt.Sprintf(format, args...))
}

// splitSignInProvider reads `firebase.sign_in_provider` out of a Firebase ID
// token, and the ID token itself is not verified here.
//
// It is called with the bytes mcpauth's Verifier has JUST admitted
// (workspace_signer.go), and it is sound for exactly that reason: an RS256
// signature covers the header and the payload, so a token that verifier
// admitted has an authentic payload, and this decode is the same strict one
// that verifier used — base64url, exactly one JSON object and nothing after it
// — so it cannot read a claim the verifier did not read. A caller that reached
// here with unverified bytes would be reading a stranger's text, which is why
// this function's only caller verifies first and says so.
//
// The nested object is refused unless it is an object with a string in it: the
// claim is present in every Firebase ID token, and a token whose firebase
// object is missing or shaped otherwise is one this exchange will not read a
// provider out of.
func splitSignInProvider(idToken string) (string, error) {
	_, claims, _, err := splitAssertion(idToken)
	if err != nil {
		return "", err
	}
	fb, ok := claims["firebase"].(map[string]any)
	if !ok {
		return "", errors.New("oauth: the ID token carries no firebase claim")
	}
	provider, ok := fb["sign_in_provider"].(string)
	if !ok || provider == "" {
		return "", errors.New("oauth: the ID token's firebase claim names no sign-in provider")
	}
	return provider, nil
}

// parseAssertionKey reads the public key a member pins. What a pin carries is
// the workspace's public key in PEM — the `PUBLIC KEY` block an
// `openssl rsa -pubout` writes, or the `CERTIFICATE` block a workspace hands
// out from its own certificate — and nothing else: a private key, a
// certificate request or a block whose body is not a key is refused here rather
// than at the first assertion, because a pin that cannot verify anything is a
// membership that would otherwise fail closed on every sign-in with a message
// about the sign-in.
func parseAssertionKey(pemText string) (*rsa.PublicKey, error) {
	block, rest := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("%w: it is not a PEM block", ErrAssertionKeyUnreadable)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("%w: it carries more than one PEM block, and a pin names one key", ErrAssertionKeyUnreadable)
	}
	switch block.Type {
	case "PUBLIC KEY":
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAssertionKeyUnreadable, err)
		}
		key, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: it is a %T, and an assertion is signed with RS256", ErrAssertionKeyUnreadable, parsed)
		}
		return key, nil
	case "CERTIFICATE":
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAssertionKeyUnreadable, err)
		}
		if key, ok := parsed.PublicKey.(*rsa.PublicKey); ok {
			return key, nil
		}
		return nil, fmt.Errorf("%w: the certificate's key is a %T, and an assertion is signed with RS256", ErrAssertionKeyUnreadable, parsed.PublicKey)
	case "RSA PRIVATE KEY", "PRIVATE KEY":
		return nil, fmt.Errorf("%w: it is a PRIVATE key, and the member's pin is the public half the workspace signs with", ErrAssertionKeyUnreadable)
	default:
		return nil, fmt.Errorf("%w: it is a %s block, and a pin carries PUBLIC KEY or CERTIFICATE", ErrAssertionKeyUnreadable, block.Type)
	}
}

// parseSigningKey reads the private half the workspace's own signer holds.
func parseSigningKey(pemText string) (*rsa.PrivateKey, error) {
	block, rest := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("oauth: the assertion key is not a PEM block")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("oauth: the assertion key carries more than one PEM block")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("oauth: the assertion key is a %T, and an assertion is signed with RS256", parsed)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("oauth: the assertion key is a %s block, and the signer holds RSA PRIVATE KEY or PRIVATE KEY", block.Type)
	}
}

// decodeSegment decodes one JWT segment: base64url, then exactly one JSON
// object. A segment carrying anything after the object is refused rather than
// half-read, because a payload that is one claim set and then another is not a
// payload whose claims are the ones that were signed (mcpauth's decodeSegment,
// which this package cannot call: its helpers are unexported and this is its
// own package).
func decodeSegment(s string) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, errors.New("a JWT segment is not base64url")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, errors.New("a JWT segment is not a JSON object")
	}
	// Token, not More: More reports whether the next non-space byte is not a
	// closing bracket, so a segment ending in `}` would read as nothing left.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("a JWT segment carries more than one JSON value")
	}
	return out, nil
}

// claimString reads a claim that must be a non-empty string.
func claimString(m map[string]any, k string) (string, error) {
	s, ok := m[k].(string)
	if !ok || s == "" {
		return "", refusef("the assertion's %s is not a string", k)
	}
	return s, nil
}

// claimInt reads a numeric claim as the exact integer the signer wrote, and
// refuses anything else — including a fractional or out-of-range value, which a
// conversion to int64 would silently narrow into a window nobody wrote.
func claimInt(m map[string]any, k string) (int64, error) {
	n, ok := m[k].(json.Number)
	if !ok {
		return 0, refusef("the assertion's %s is not a number", k)
	}
	i, err := n.Int64()
	if err != nil {
		return 0, refusef("the assertion's %s is not an integer", k)
	}
	return i, nil
}
