package endpoint

import (
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// otherHex is the digit a test writes over one of the token's own, so a case
// changes the value whatever the random token happened to be: a literal
// replacement would be a case that is a no-op whenever crypto/rand draws the
// character the test wrote.
func otherHex(c byte) byte {
	if c == '0' {
		return '1'
	}
	return '0'
}

// TestConstantTimeTokenCompare holds the half of D1359's constant-time rule a
// test can hold: that the whole value is compared, so a credential that differs
// anywhere -- the first byte, the last, its length -- is refused.
//
// The other half is not measurable here. A wall clock in a test measures the
// machine it runs on rather than the comparison, so what says "constant time"
// is the body of sameToken (one call to crypto/subtle), and what a test can pin
// is that the function is that one and not a re-implementation that happens to
// agree on a handful of rows.
func TestConstantTimeTokenCompare(t *testing.T) {
	token, err := mint()
	if err != nil {
		t.Fatalf("minting a token: %v", err)
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("the token is not hex: %v", err)
	}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"the token against itself", token, token, true},
		{"two empty values", "", "", true},
		{"the first byte changed", string(otherHex(token[0])) + token[1:], token, false},
		{"the last byte changed", token[:len(token)-1] + string(otherHex(token[len(token)-1])), token, false},
		{"a prefix of the token", token[:len(token)-1], token, false},
		{"the token with a byte appended", token + "0", token, false},
		{"an empty credential", "", token, false},
		{"another value of the same length", strings.Repeat("a", len(token)), token, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameToken(c.a, c.b); got != c.want {
				t.Errorf("sameToken over %d-byte values = %v, want %v", len(c.a), got, c.want)
			}
			if want := subtle.ConstantTimeCompare([]byte(c.a), []byte(c.b)) == 1; sameToken(c.a, c.b) != want {
				t.Errorf("sameToken disagrees with crypto/subtle over %d-byte values", len(c.a))
			}
		})
	}
	// Every position of the value decides it, which is what "the whole value"
	// means: a comparison that stopped after a byte, a length or a checksum
	// would accept one of these.
	for i := range token {
		changed := []byte(token)
		changed[i] = otherHex(changed[i])
		if sameToken(string(changed), token) {
			t.Fatalf("a token changed at position %d is accepted", i)
		}
	}
}

// TestMintIsA256BitRandomToken pins what a token is, because every rule built
// on one assumes exactly this: tokenBytes drawn from crypto/rand, written as
// lower-case hex, and never the same value twice.
func TestMintIsA256BitRandomToken(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		token, err := mint()
		if err != nil {
			t.Fatalf("minting a token: %v", err)
		}
		if len(token) != 2*tokenBytes {
			t.Fatalf("the token is %d characters, want %d", len(token), 2*tokenBytes)
		}
		raw, err := hex.DecodeString(token)
		if err != nil || len(raw) != tokenBytes {
			t.Fatalf("the token is %d characters and decodes to %d bytes, want %d of hex", len(token), len(raw), tokenBytes)
		}
		if token != strings.ToLower(token) {
			t.Fatalf("the token is not lower-case hex")
		}
		if seen[token] {
			t.Fatalf("mint returned the same token twice")
		}
		seen[token] = true
	}
}

// TestBearerTokenReadsOneHeader pins the one place the credential is read:
// exactly one Authorization header, carrying the bearer scheme, quoted whole
// the way the registry file quotes it.
func TestBearerTokenReadsOneHeader(t *testing.T) {
	token, err := mint()
	if err != nil {
		t.Fatalf("minting a token: %v", err)
	}
	cases := []struct {
		name    string
		headers []string
		want    string
		ok      bool
	}{
		{"the bearer header", []string{"Bearer " + token}, token, true},
		{"the scheme in another case", []string{"bearer " + token}, token, true},
		{"no header at all", nil, "", false},
		{"another scheme", []string{"Token " + token}, "", false},
		{"basic auth", []string{"Basic " + token}, "", false},
		{"the scheme alone", []string{"Bearer"}, "", false},
		{"an empty value", []string{""}, "", false},
		{"the value with no scheme", []string{token}, "", false},
		{"two headers carrying it", []string{"Bearer " + token, "Bearer " + token}, "", false},
		// A second space is not the header's to normalise: the credential it
		// hands over carries the space, and a token with one in front of it is
		// not the token.
		{"two spaces after the scheme", []string{"Bearer  " + token}, " " + token, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/state", nil)
			for _, h := range c.headers {
				r.Header.Add(authorizationHeader, h)
			}
			got, ok := bearerToken(r)
			if ok != c.ok || got != c.want {
				t.Errorf("bearerToken = (%d characters, %v), want (%d characters, %v)", len(got), ok, len(c.want), c.ok)
			}
		})
	}
}
