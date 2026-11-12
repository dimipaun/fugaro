// The token end of the local endpoint: the one secret it mints, the one header
// it reads it from, and the one comparison it makes with it (CG13a of
// docs/cli-output-console-graph/implementation-plan-graph.md).
//
// It is minted here rather than passed in because the token's whole meaning is
// "the endpoint this process is serving, this time": a value that outlived the
// listener would still be honoured by the next one, so the daemon's start-up
// mints one with the endpoint and writes it where only its own user can read it
// (CG13b's registry file, D1359).
//
// Nothing in this file ever puts the value into a string a caller could print
// by accident: the mint returns it, sameToken compares two of them, and the
// one place a header is read hands back the credential alone -- an error text
// or a log line built from any of these is the bug the whole design is written
// against (F23).

package endpoint

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// tokenBytes is what D1359 fixes the token at, and the number is the point: 32
// bytes of crypto/rand is 256 bits, so guessing one is not a strategy, and
// hex-encoding it makes the value a header-safe ASCII string of 64 characters,
// which is what the registry file carries and what a reader sends back.
const tokenBytes = 32

// authorizationHeader is the one header the token travels in. It is read on
// every request including the stream, and there is no second spelling: a query
// string is refused before it is looked at (server.go's ServeHTTP), so a token
// in a URL cannot be served even while it is being rejected.
const authorizationHeader = "Authorization"

// bearerScheme is the scheme half of the header, compared case-insensitively
// because RFC 7235 makes the scheme case-insensitive and a reader that refused
// "bearer" would refuse a correct credential for a spelling it does not own.
const bearerScheme = "bearer"

// mint returns a fresh token: tokenBytes of crypto/rand as lower-case hex.
//
// A failure here is fatal to the caller's start-up and never a token that is
// weaker than the one D1359 asks for: a fallback drawn from anything but the
// system's random source -- a clock, a pid, a counter -- would be a token an
// offline guess reaches, and the endpoint is reachable by every process of
// every user on the machine (F23).
func mint() (string, error) {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("endpoint: minting a token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// bearerToken returns the credential the request carries in its one
// Authorization header, and whether there was exactly one of them to read.
//
// Exactly one, because two headers are two answers to one question and a
// server that read the first would be a server whose answer depends on a
// proxy's order; and the bearer scheme, because the header is quoted in the
// registry file as a whole (`Authorization: Bearer <token>`, CG13b) rather
// than as a bare value.
func bearerToken(r *http.Request) (string, bool) {
	got := r.Header.Values(authorizationHeader)
	if len(got) != 1 {
		return "", false
	}
	scheme, credential, ok := strings.Cut(got[0], " ")
	if !ok || !strings.EqualFold(scheme, bearerScheme) {
		return "", false
	}
	return credential, true
}

// sameToken compares a credential against the endpoint's own token in constant
// time (D1359), over the raw bytes of the two strings.
//
// The constant time is about the value's length and its prefix rather than its
// entropy: 256 random bits are not guessable either way, but a comparison that
// stopped at the first byte that differed would let a local process narrow the
// token one byte at a time and read the answer off its own clock (F23).
//
// No run can measure that -- a wall clock in a test measures the machine -- so
// token_test.go holds two things it can: that the whole value decides the answer
// (TestConstantTimeTokenCompare's rows, and every position of a token), and that
// this body is one call to crypto/subtle.ConstantTimeCompare over the two values
// and nothing around it, read off this file's syntax tree. Replacing it with a
// comparison that stops at the first byte that differs fails there, which is
// what makes the claim this comment makes a fact a reader can check.
func sameToken(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
