// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
	"time"
)

// mustErr drops the session of a refused Admit, so a test can assert on the
// refusal alone.
func mustErr(_ Session, err error) error { return err }

// signClaims signs a payload with key, the way mintAssertion does and without
// mintAssertion's own field checks: it is how a test builds the payloads an
// honest signer would never write — one with an extra claim, one whose claims
// were edited after signing.
func signClaims(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	return signToken(t, key, map[string]string{"alg": assertionAlg, "typ": assertionTyp}, claims)
}

// signToken signs claims under an arbitrary header with key. It exists so that a
// test about a header rule can present a token whose signature VERIFIES: a token
// built by swapping the header over the bytes of an honest token is refused by
// the signature check, so it never reaches the rule the test means to hold.
func signToken(t *testing.T, key *rsa.PrivateKey, header map[string]string, claims map[string]any) string {
	t.Helper()
	head, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("encoding a test header: %v", err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encoding test claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing test claims: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// assertionWithClaim mints an otherwise ordinary assertion carrying one claim
// more than this member reads.
func assertionWithClaim(t *testing.T, key *rsa.PrivateKey, project, audience, name string, value any) string {
	t.Helper()
	now := time.Now().UTC()
	a := assertion{
		Issuer:        WorkspaceIssuer(project),
		Audience:      audience,
		Subject:       project,
		IssuedAt:      now.Add(-time.Minute).Unix(),
		ExpiresAt:     now.Add(4 * time.Minute).Unix(),
		JTI:           "jti-1",
		Email:         testEmail,
		EmailVerified: true,
		Provider:      "google.com",
	}
	claims := a.claims()
	claims[name] = value
	return signClaims(t, key, claims)
}

// TestAssertionRefusesAClaimThisMemberDoesNotRead. The claim set is closed, and
// that is what makes "the workspace mints nothing a member's API accepts" true
// at the wire: a payload carrying a scope, a limit or a role is refused rather
// than read past, so there is no claim a workspace could put beside the
// identity that a later reader might start honouring.
func TestAssertionRefusesAClaimThisMemberDoesNotRead(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	for _, c := range []struct {
		name  string
		value any
	}{
		{"scopes", []string{"tasks:write"}},
		{"limits", map[string]any{"spend_usd": 1000}},
		{"role", "operator"},
		{"email_verified_by", "workspace"},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := assertionWithClaim(t, key, testProject, member.url, c.name, c.value)
			refuse(t, mustErr(member.ex.Admit(context.Background(), raw)), "an assertion carrying a claim this member does not read")
		})
	}
}

// TestAssertionRefusesAnotherAlgorithm: RS256 is the only algorithm this
// exchange verifies, so a token whose header says `none` — or an HMAC family
// whose "key" would be the value this member publishes — is refused before any
// key is used.
//
// Each hostile token is SIGNED with the key this member pins, under a header
// that names the other algorithm, so its signature verifies and the algorithm
// guard is the only rule left that can refuse it. That is what makes this test
// hold the guard: a token built by putting a new header over the bytes of an
// honest one — which is what this test used to do — is refused by the signature
// check instead, several lines below the guard, and deleting the guard would
// leave the package green.
func TestAssertionRefusesAnotherAlgorithm(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	claims := testAssertion(testProject, member.url).claims()

	for _, alg := range []string{"none", "HS256", "RS512"} {
		raw := signToken(t, key, map[string]string{"alg": alg, "typ": assertionTyp}, claims)
		refuse(t, mustErr(member.ex.Admit(context.Background(), raw)), "an assertion signed with "+alg)
	}
}

// TestAssertionRefusesAnEditedPayload: the signature covers the payload, so a
// claim edited after signing — here the address, which is the one an attacker
// would want to change — is refused as a signature that does not verify.
func TestAssertionRefusesAnEditedPayload(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	honest := assertionFor(t, key, testProject, member.url, nil)
	parts := strings.Split(honest, ".")

	body, err := json.Marshal(map[string]any{
		"iss": WorkspaceIssuer(testProject), "aud": member.url, "sub": testProject,
		"iat": time.Now().UTC().Add(-time.Minute).Unix(), "exp": time.Now().UTC().Add(4 * time.Minute).Unix(),
		"jti": "jti-1", "email": "person-2@example.com", "email_verified": true, "provider": "google.com",
	})
	if err != nil {
		t.Fatalf("encoding an edited payload: %v", err)
	}
	raw := parts[0] + "." + base64.RawURLEncoding.EncodeToString(body) + "." + parts[2]
	refuse(t, mustErr(member.ex.Admit(context.Background(), raw)), "an assertion whose payload was edited after signing")
}

// TestAssertionRefusesAClaimOfTheWrongType: a claim of the wrong type is a
// refusal and never a zero value, so `email_verified: "true"` or a fractional
// `exp` cannot be read as the value a reader hoped for.
func TestAssertionRefusesAClaimOfTheWrongType(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	now := time.Now().UTC()

	cases := map[string]func(map[string]any){
		"a string where a bool belongs": func(c map[string]any) { c["email_verified"] = "true" },
		"a fractional expiry":           func(c map[string]any) { c["exp"] = float64(now.Add(4*time.Minute).Unix()) + 0.5 },
		"a missing expiry":              func(c map[string]any) { delete(c, "exp") },
		"a numeric address":             func(c map[string]any) { c["email"] = 42 },
		"an empty id":                   func(c map[string]any) { c["jti"] = "" },
	}
	for what, edit := range cases {
		t.Run(what, func(t *testing.T) {
			a := assertion{
				Issuer: WorkspaceIssuer(testProject), Audience: member.url, Subject: testProject,
				IssuedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(4 * time.Minute).Unix(),
				JTI: "jti-1", Email: testEmail, EmailVerified: true, Provider: "google.com",
			}
			claims := a.claims()
			edit(claims)
			refuse(t, mustErr(member.ex.Admit(context.Background(), signClaims(t, key, claims))), what)
		})
	}
}

// TestAssertionKeyIsTheMembersPinnedPublicKey: the pin carries a public key and
// nothing else, so a pin holding a private key, another block type or a value
// that is not PEM at all is refused where the exchange is built — a member
// whose pin cannot verify is a member that would otherwise fail closed on every
// sign-in with a message about the sign-in.
func TestAssertionKeyIsTheMembersPinnedPublicKey(t *testing.T) {
	private := testKey(t)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatalf("marshalling a private key: %v", err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	for _, c := range []struct {
		what string
		key  string
	}{
		{"a private key", privatePEM},
		{"not PEM at all", testAssertionMarker},
		{"an empty pin value", ""},
		{"two blocks", testPublicPEM(t, &private.PublicKey) + testPublicPEM(t, &private.PublicKey)},
	} {
		t.Run(c.what, func(t *testing.T) {
			pin := testPin(t, testWorkspace, testProject, &private.PublicKey)
			pin.AssertionKey = c.key
			_, err := NewExchange(ExchangeConfig{
				Pin:               pin,
				CoordinatorIssuer: "https://" + testMemberA,
				Consumed:          NewMemoryConsumedJTI(nil),
			})
			if err == nil {
				t.Fatalf("NewExchange accepted a pin whose assertion key is %s", c.what)
			}
			if !strings.Contains(err.Error(), "key") {
				t.Fatalf("the refusal is %q, and it is about the pinned key", err)
			}
		})
	}
}

// TestExchangeRefusesACoordinatorURLThatIsNotOne: the audience an assertion must
// name is derived from the member's own coordinator URL, and a member whose
// coordinator is not an https origin would bind assertions to an address no
// browser reaches.
func TestExchangeRefusesACoordinatorURLThatIsNotOne(t *testing.T) {
	key := testKey(t)
	for _, url := range []string{"", "http://" + testMemberA, "https://" + testMemberA + "/path", "https://person-1@" + testMemberA, "not a url"} {
		if _, err := NewExchange(ExchangeConfig{
			Pin:               testPin(t, testWorkspace, testProject, &key.PublicKey),
			CoordinatorIssuer: url,
			Consumed:          NewMemoryConsumedJTI(nil),
		}); err == nil {
			t.Fatalf("NewExchange accepted the coordinator URL %q", url)
		}
	}
	if _, err := NewExchange(ExchangeConfig{
		Pin:               testPin(t, testWorkspace, testProject, &key.PublicKey),
		CoordinatorIssuer: "https://" + testMemberA,
	}); err == nil {
		t.Fatal("NewExchange accepted an exchange with nowhere to record a spent id, and every assertion would be admitted twice")
	}
}

// TestExchangeURLIsTheMembersOwnAuthorizeEndpoint: the value an assertion's aud
// must be is the member's own coordinator joined with the MCP design's
// authorize path — the same path the front door serves, so the binding and the
// endpoint cannot drift apart.
func TestExchangeURLIsTheMembersOwnAuthorizeEndpoint(t *testing.T) {
	got, err := ExchangeURL("https://" + testMemberA)
	if err != nil {
		t.Fatalf("ExchangeURL: %v", err)
	}
	if want := "https://" + testMemberA + "/oauth/authorize"; got != want {
		t.Fatalf("ExchangeURL = %q, want %q", got, want)
	}
}

// assertPackageReadsNoWorkspaceRecord is "the member's pin and never a value
// from the workspace's record" as a fact about this code rather than a promise
// about its callers: no file of this package reads the published workspace
// record, so a "helpful" refactor that reached for it has nothing to reach
// with. The record's own package and its policy reader are what a reader would
// import, and neither appears here.
func assertPackageReadsNoWorkspaceRecord(t *testing.T) {
	t.Helper()
	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing this package's sources: %v", err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, forbidden := range []string{"internal/projectpolicy", "internal/workspace"} {
					if strings.HasSuffix(path, forbidden) {
						t.Fatalf("%s imports %s: the exchange verifies the member's own pin, and the workspace's record is not an input to it", name, path)
					}
				}
			}
		}
	}
}
