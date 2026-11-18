package voicesvc

// The verifier's tests. Every one of them is a property the task names, and the
// two that matter most are that a run's identity is refused as a device and
// that an unreadable key set is not a refusal of the caller.

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// TestVerifyDeviceAdmitsAPairedDevice is the base case: a well-formed ID token
// for a device's uid verifies, and the identity is that uid and nothing else.
func TestVerifyDeviceAdmitsAPairedDevice(t *testing.T) {
	k := newTestKey(t)
	a := testAuthenticator(t, k)

	id, err := a.VerifyDevice(context.Background(), k.token(t, k.claims()))
	if err != nil {
		t.Fatalf("VerifyDevice: %v", err)
	}
	if id.UID != testUID {
		t.Fatalf("UID = %q, want %q", id.UID, testUID)
	}
}

// TestVerifyDeviceRefusesARunIdentity is the check the hop exists for: a run's
// identity verifies as a token and is refused as a DEVICE, by reason. A run
// holds a real Firebase token of this installation, so nothing about its
// signature, issuer or audience is wrong — only its uid's family is, and that
// is exactly what this service must not admit.
func TestVerifyDeviceRefusesARunIdentity(t *testing.T) {
	k := newTestKey(t)
	a := testAuthenticator(t, k)

	claims := k.claims()
	claims["sub"], claims["user_id"], claims["mu"] = testRunUID, testRunUID, testRunUID
	_, err := a.VerifyDevice(context.Background(), k.token(t, claims))
	if err == nil {
		t.Fatal("a run identity was admitted as a device")
	}
	if got := ReasonOf(err); got != ReasonNotADevice {
		t.Fatalf("ReasonOf = %q, want %q", got, ReasonNotADevice)
	}
	if !errors.Is(err, ErrUnverified) {
		t.Fatalf("a run identity is not ErrUnverified: %v", err)
	}
	if !strings.Contains(err.Error(), "m~") {
		t.Fatalf("the refusal does not name the family it wants: %v", err)
	}
}

// TestIsDeviceUIDIsTheShapeAPairedDeviceHas pins the shape itself, including
// the lengths a prefix-only check would let through: the uid becomes a database
// path, so a string that is not of this shape must never build one.
func TestIsDeviceUIDIsTheShapeAPairedDeviceHas(t *testing.T) {
	if !IsDeviceUID(testUID) {
		t.Fatalf("IsDeviceUID(%q) = false: the test's own uid is not the shape it claims", testUID)
	}
	for _, uid := range []string{
		"",
		"m~",
		"m~" + strings.Repeat("A", 21),
		"m~" + strings.Repeat("A", 23),
		"m~" + strings.Repeat("A", 21) + "/",
		"m~" + strings.Repeat("A", 21) + ".",
		"m~" + strings.Repeat("A", 21) + "=",
		"m~" + strings.Repeat("A", 21) + " ",
		"M~" + strings.Repeat("A", 22),
		"m" + strings.Repeat("A", 22),
		testRunUID,
		"p~" + strings.Repeat("A", 43),
		"someone@example.com",
		"uid-of-an-account",
	} {
		if IsDeviceUID(uid) {
			t.Errorf("IsDeviceUID(%q) = true, want false", uid)
		}
	}
}

// TestDeviceShapeIsTheMintersOwn holds this package's spelling of a device uid
// to internal/budget/token's, which is the code that mints one. The two
// constants are duplicated (the minter's are unexported) and a test is what
// keeps them one answer — the argument projectpolicy makes for its own
// duplicated rules.
//
// The signer is an echo: what is being read is the uid the minter DREW, not the
// token it wrapped around it.
func TestDeviceShapeIsTheMintersOwn(t *testing.T) {
	for i := 0; i < 32; i++ {
		dt, err := token.MintDevice(context.Background(), echoSigner{}, testNow)
		if err != nil {
			t.Fatalf("MintDevice: %v", err)
		}
		if !IsDeviceUID(dt.UID) {
			t.Fatalf("a uid the minter produced is not one this service admits: %q", dt.UID)
		}
	}
}

// echoSigner is a token.Signer that returns the payload it was handed as a
// JWT: enough for MintDevice's own read-back, and no key of any kind.
type echoSigner struct{}

func (echoSigner) Email() string { return "signer@example.invalid" }

func (echoSigner) SignJWT(ctx context.Context, payload []byte) (string, error) {
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature", nil
}

// TestVerifyDeviceRefusesABadToken walks the checks in order. Each row is a
// token this service must refuse, and the reason it must answer with: the
// caller's own refusal everywhere but the key set, which is not the caller's.
func TestVerifyDeviceRefusesABadToken(t *testing.T) {
	k := newTestKey(t)
	a := testAuthenticator(t, k)

	other := newTestKey(t)
	foreign := k.claims()
	foreign["aud"] = "another-project"
	foreignISS := k.claims()
	foreignISS["iss"] = firebaseIssuer("another-project")
	expired := k.claims()
	expired["exp"] = testNow.Add(-time.Second).Unix()
	future := k.claims()
	future["iat"] = testNow.Add(2 * clockSkew).Unix()
	noSub := k.claims()
	delete(noSub, "sub")
	noUserID := k.claims()
	delete(noUserID, "user_id")
	twoUIDs := k.claims()
	twoUIDs["user_id"] = testRunUID
	noMu := k.claims()
	delete(noMu, "mu")
	wrongMu := k.claims()
	wrongMu["mu"] = testRunUID

	for _, tc := range []struct {
		name   string
		token  string
		reason string
	}{
		{"empty", "", ReasonTokenUnverified},
		{"not a JWT", "not-a-token", ReasonTokenUnverified},
		{"two segments", "aaa.bbb", ReasonTokenUnverified},
		{"header is not JSON", "!!!." + strings.Split(k.token(t, k.claims()), ".")[1] + ".sig", ReasonTokenUnverified},
		{"claims are not JSON", strings.Split(k.token(t, k.claims()), ".")[0] + ".!!!.sig", ReasonTokenUnverified},
		{"alg is none", k.signed(t, map[string]any{"alg": "none", "kid": k.kid}, k.claims()), ReasonTokenUnverified},
		{"alg is HS256", k.signed(t, map[string]any{"alg": "HS256", "kid": k.kid}, k.claims()), ReasonTokenUnverified},
		{"no alg", k.signed(t, map[string]any{"kid": k.kid}, k.claims()), ReasonTokenUnverified},
		{"unknown kid", k.signed(t, map[string]any{"alg": "RS256", "kid": "no-such-key"}, k.claims()), ReasonTokenUnverified},
		{"no kid", k.signed(t, map[string]any{"alg": "RS256"}, k.claims()), ReasonTokenUnverified},
		{"another key's signature", other.token(t, k.claims()), ReasonTokenUnverified},
		{"tampered payload", tamper(t, k.token(t, k.claims())), ReasonTokenUnverified},
		{"another project's audience", k.token(t, foreign), ReasonTokenUnverified},
		{"another project's issuer", k.token(t, foreignISS), ReasonTokenUnverified},
		{"expired", k.token(t, expired), ReasonTokenUnverified},
		{"no exp", k.token(t, without(k.claims(), "exp")), ReasonTokenUnverified},
		{"issued in the future", k.token(t, future), ReasonTokenUnverified},
		{"no sub", k.token(t, noSub), ReasonTokenUnverified},
		{"no user_id", k.token(t, noUserID), ReasonTokenUnverified},
		{"two different uids", k.token(t, twoUIDs), ReasonTokenUnverified},
		{"a run's uid", k.token(t, runClaims(k)), ReasonNotADevice},
		{"a pairing session's uid", k.token(t, uidClaims(k, "p~"+strings.Repeat("B", 43))), ReasonNotADevice},
		{"an account's own uid", k.token(t, uidClaims(k, "owner@example.com")), ReasonNotADevice},
		{"a uid of the wrong length", k.token(t, uidClaims(k, "m~"+strings.Repeat("B", 21))), ReasonNotADevice},
		{"no mu", k.token(t, noMu), ReasonNotADevice},
		{"mu names another uid", k.token(t, wrongMu), ReasonNotADevice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.VerifyDevice(context.Background(), tc.token)
			if err == nil {
				t.Fatal("the token was admitted")
			}
			if got := ReasonOf(err); got != tc.reason {
				t.Fatalf("ReasonOf = %q, want %q (%v)", got, tc.reason, err)
			}
			if tc.reason == ReasonTokenUnverified && !errors.Is(err, ErrUnverified) {
				t.Fatalf("a token refusal is not ErrUnverified: %v", err)
			}
		})
	}
}

// TestVerifyDeviceRefusesAnOversizedToken: the bound is checked before
// anything is decoded, so a caller cannot hand this process an arbitrarily
// large string to parse.
func TestVerifyDeviceRefusesAnOversizedToken(t *testing.T) {
	k := newTestKey(t)
	a := testAuthenticator(t, k)

	_, err := a.VerifyDevice(context.Background(), strings.Repeat("x", maxTokenBytes+1))
	if err == nil {
		t.Fatal("an oversized token was admitted")
	}
	if got := ReasonOf(err); got != ReasonTokenUnverified {
		t.Fatalf("ReasonOf = %q, want %q", got, ReasonTokenUnverified)
	}
	if k.lookups != 0 {
		t.Fatalf("the key set was consulted %d times for a token that is not a JWT", k.lookups)
	}
}

// TestUnreadableKeySetIsStateAndNotARefusal is the distinction the app's
// behaviour hangs on: "your token is bad" makes it drop a token that is fine,
// while "this service cannot tell" makes it serve locally and lets an operator
// look. So an unreadable key set is state_unavailable and is deliberately NOT
// ErrUnverified.
func TestUnreadableKeySetIsStateAndNotARefusal(t *testing.T) {
	k := newTestKey(t)
	k.keyErr = errBoom
	a := testAuthenticator(t, k)

	_, err := a.VerifyDevice(context.Background(), k.token(t, k.claims()))
	if err == nil {
		t.Fatal("a token was admitted with no readable key set")
	}
	if got := ReasonOf(err); got != ReasonStateUnavailable {
		t.Fatalf("ReasonOf = %q, want %q", got, ReasonStateUnavailable)
	}
	if errors.Is(err, ErrUnverified) {
		t.Fatal("an unreadable key set is ErrUnverified, so the app would drop a good token")
	}
}

// TestUnknownKeyIDIsARefusalAndNotState is the other side of the same line: the
// key set WAS read and does not hold the key id, which is a fact about the
// token.
func TestUnknownKeyIDIsARefusalAndNotState(t *testing.T) {
	k := newTestKey(t)
	a := testAuthenticator(t, k)

	_, err := a.VerifyDevice(context.Background(), k.signed(t, map[string]any{"alg": "RS256", "kid": "gone"}, k.claims()))
	if got := ReasonOf(err); got != ReasonTokenUnverified {
		t.Fatalf("ReasonOf = %q, want %q", got, ReasonTokenUnverified)
	}
}

// TestARefusalNeverEchoesWhatTheCallerSent: every refusal of an identity is
// built from this package's own words. The key id is the one that matters — it
// is a caller-chosen string of any length — so a token whose kid is a marker is
// refused with a message that does not contain it.
func TestARefusalNeverEchoesWhatTheCallerSent(t *testing.T) {
	const marker = "MARKER-the-caller-wrote-this"
	k := newTestKey(t)
	a := testAuthenticator(t, k)

	_, err := a.VerifyDevice(context.Background(), k.signed(t, map[string]any{"alg": "RS256", "kid": marker}, k.claims()))
	if err == nil {
		t.Fatal("a token with an unknown kid was admitted")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("the refusal echoes the caller's key id: %v", err)
	}
	// The same for the token itself: no refusal may quote it, because a token
	// is a credential.
	tok := k.token(t, k.claims())
	_, err = a.VerifyDevice(context.Background(), tamper(t, tok))
	if strings.Contains(err.Error(), tok) {
		t.Fatalf("the refusal echoes the token: %v", err)
	}
}

// TestNewAuthenticatorRefusesAnEmptyConfiguration: a verifier that could not
// verify anything must not be built, because the failure it would cause is an
// admission rather than an error.
func TestNewAuthenticatorRefusesAnEmptyConfiguration(t *testing.T) {
	k := newTestKey(t)
	if _, err := NewAuthenticator(AuthConfig{Keys: k}); err == nil {
		t.Fatal("an empty Firebase project was accepted: every project's tokens would verify")
	}
	if _, err := NewAuthenticator(AuthConfig{FirebaseProject: testProject}); err == nil {
		t.Fatal("a missing key source was accepted")
	}
}

// tamper rewrites a token's payload so its signature no longer matches, leaving
// everything else a valid token has.
func tamper(t *testing.T, tok string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("tamper: %q is not a JWS", tok)
	}
	return parts[0] + "." + base64Segment(t, `{"sub":"`+testUID+`","user_id":"`+testUID+`"}`) + "." + parts[2]
}

// runClaims is a run's identity in a token: the `r~` family a job's token
// carries.
func runClaims(k *testKey) map[string]any {
	return uidClaims(k, testRunUID)
}

// uidClaims is the base claims with every uid-carrying member set to uid.
func uidClaims(k *testKey, uid string) map[string]any {
	c := k.claims()
	c["sub"], c["user_id"], c["mu"] = uid, uid, uid
	return c
}

// without is the base claims minus one member.
func without(c map[string]any, key string) map[string]any {
	out := map[string]any{}
	for k, v := range c {
		if k != key {
			out[k] = v
		}
	}
	return out
}
