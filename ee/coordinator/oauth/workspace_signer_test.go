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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
)

// testKid is the key id the fake published set holds the ID-token key under.
// Google's real set has many; this one has exactly one, because what is under
// test is what the signer does with a verified token rather than the key set's
// own reading (mcpauth's own tests carry that).
const testKid = "kid-1"

// fakeKeys is the published key set the ID tokens are verified against, as a
// test's own: the signer reads the same mcpauth.KeySource interface Google's
// JWKS does.
type fakeKeys map[string]*rsa.PublicKey

func (f fakeKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	key, ok := f[kid]
	if !ok {
		return nil, mcpauth.ErrUnknownKeyID
	}
	return key, nil
}

// testSigner is a workspace's signer with the two keys a test needs: the key
// the workspace's Firebase signs ID tokens with (published in the fake set) and
// the assertion key whose public half members pin.
type testSigner struct {
	*Signer
	idKey  *rsa.PrivateKey
	ascKey *rsa.PrivateKey
}

// newTestSigner returns the signer of a workspace whose Firebase is project.
// Both keys are generated per call and never written down: a fixture would be
// key material in a file.
func newTestSigner(t *testing.T, project string, opts ...SignerOption) *testSigner {
	t.Helper()
	idKey, assertionKey := testKey(t), testKey(t)
	der, err := x509.MarshalPKCS8PrivateKey(assertionKey)
	if err != nil {
		t.Fatalf("marshalling a test signing key: %v", err)
	}
	signer, err := NewSigner(SignerConfig{
		FirebaseProject: project,
		AssertionKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		Keys:            fakeKeys{testKid: &idKey.PublicKey},
	}, opts...)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return &testSigner{Signer: signer, idKey: idKey, ascKey: assertionKey}
}

// idTokenFields is one ID token's claims as a test writes them. The zero value
// is not usable: defaultIDToken fills in the token every test shares.
type idTokenFields struct {
	Audience      string
	Issuer        string
	Subject       string
	Email         string
	EmailVerified any
	Provider      string
	IssuedAt      time.Time
	ExpiresAt     time.Time
	KeyID         string
}

// defaultIDToken is the token the workspace's Firebase would mint for the
// person: this project as the audience and the issuer, a verified address, and
// a real provider.
func defaultIDToken(project string) idTokenFields {
	now := time.Now().UTC()
	return idTokenFields{
		Audience:      project,
		Issuer:        WorkspaceIssuer(project),
		Subject:       "uid-1",
		Email:         testEmail,
		EmailVerified: true,
		Provider:      "google.com",
		IssuedAt:      now.Add(-time.Minute),
		ExpiresAt:     now.Add(time.Hour),
		KeyID:         testKid,
	}
}

// firebaseIDToken signs an ID token the way the workspace's Firebase does: the
// claims at the top level, `firebase.sign_in_provider` among them, RS256 under
// the key the published set holds.
func firebaseIDToken(t *testing.T, key *rsa.PrivateKey, f idTokenFields) string {
	t.Helper()
	claims := map[string]any{
		"aud":     f.Audience,
		"iss":     f.Issuer,
		"sub":     f.Subject,
		"user_id": f.Subject,
		"email":   f.Email,
		"iat":     f.IssuedAt.Unix(),
		"exp":     f.ExpiresAt.Unix(),
		"firebase": map[string]any{
			"sign_in_provider": f.Provider,
		},
	}
	if f.EmailVerified != nil {
		claims["email_verified"] = f.EmailVerified
	}
	head, err := json.Marshal(map[string]string{"alg": "RS256", "kid": f.KeyID})
	if err != nil {
		t.Fatalf("encoding a test ID token header: %v", err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encoding test ID token claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing a test ID token: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// TestSignerMintsAnAssertionTheMemberAdmits is the round trip the two halves
// are: the workspace's signer verifies a workspace Firebase token with the
// pinned provider and mints an assertion bound to one member's exchange URL,
// and that member's exchange — which knows only its own pin — admits it.
func TestSignerMintsAnAssertionTheMemberAdmits(t *testing.T) {
	signer := newTestSigner(t, testProject)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &signer.ascKey.PublicKey))

	raw, err := signer.Mint(context.Background(), firebaseIDToken(t, signer.idKey, defaultIDToken(testProject)), member.url)
	if err != nil {
		t.Fatalf("Mint refused a token the workspace's own Firebase signed: %v", err)
	}
	sess, err := member.ex.Admit(context.Background(), raw)
	if err != nil {
		t.Fatalf("the member refused the assertion its own workspace minted: %v", err)
	}
	if sess.Identity.Email != testEmail || sess.Identity.Provider != "google.com" || sess.Identity.WorkspaceProject != testProject {
		t.Fatalf("the session is %+v, and the assertion was for %s at %s", sess.Identity, testEmail, testProject)
	}
	if !sess.Identity.EmailVerified {
		t.Fatal("the session's address is not marked verified, and the signer requires a verified one")
	}
}

// TestSignerMintsOneAssertionPerSignIn: every mint carries a fresh id, so two
// sign-ins are two assertions and neither is a replay of the other.
func TestSignerMintsOneAssertionPerSignIn(t *testing.T) {
	signer := newTestSigner(t, testProject)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &signer.ascKey.PublicKey))
	token := firebaseIDToken(t, signer.idKey, defaultIDToken(testProject))

	for i := 0; i < 2; i++ {
		raw, err := signer.Mint(context.Background(), token, member.url)
		if err != nil {
			t.Fatalf("Mint %d: %v", i, err)
		}
		if _, err := member.ex.Admit(context.Background(), raw); err != nil {
			t.Fatalf("Admit of mint %d: %v", i, err)
		}
	}
	if len(jtisSpent(member)) != 2 {
		t.Fatalf("the member holds %d spent ids, and two sign-ins are two assertions", len(jtisSpent(member)))
	}
}

// TestSignerRefusesAnIDTokenOfAnotherProject is the signer's own audience and
// issuer check, both against the workspace's project and neither against
// anything a caller sent — the token is verified with mcpauth's Verifier, whose
// audience is the bare project id and whose issuer is built from it.
func TestSignerRefusesAnIDTokenOfAnotherProject(t *testing.T) {
	signer := newTestSigner(t, testProject)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &signer.ascKey.PublicKey))

	cases := map[string]idTokenFields{}
	f := defaultIDToken(testProject)
	f.Audience = testProject2
	cases["another project's audience"] = f
	f = defaultIDToken(testProject)
	f.Issuer = WorkspaceIssuer(testProject2)
	cases["another project's issuer"] = f
	f = defaultIDToken(testProject)
	f.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	cases["an expired token"] = f
	f = defaultIDToken(testProject)
	f.EmailVerified = false
	cases["an unverified address"] = f
	f = defaultIDToken(testProject)
	f.EmailVerified = nil
	cases["no verified claim at all"] = f

	for what, fields := range cases {
		t.Run(what, func(t *testing.T) {
			raw, err := signer.Mint(context.Background(), firebaseIDToken(t, signer.idKey, fields), member.url)
			if !errors.Is(err, mcpauth.ErrUnverifiedIDToken) {
				t.Fatalf("Mint answered %q and %v for %s, and it must refuse it as an unverified ID token", raw, err, what)
			}
		})
	}
}

// TestSignerRefusesAnUnpinnedProvider is finding 7 at the signer: `custom` and
// `anonymous` are outside the pinned set, so the endpoint refuses to mint for
// them at all — a member that re-checked the provider would be the last line
// rather than the only one.
func TestSignerRefusesAnUnpinnedProvider(t *testing.T) {
	signer := newTestSigner(t, testProject)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &signer.ascKey.PublicKey))

	for provider, want := range map[string]bool{
		"google.com": true,
		"password":   true,
		"custom":     false,
		"anonymous":  false,
	} {
		t.Run(provider, func(t *testing.T) {
			fields := defaultIDToken(testProject)
			fields.Provider = provider
			raw, err := signer.Mint(context.Background(), firebaseIDToken(t, signer.idKey, fields), member.url)
			switch {
			case want && err != nil:
				t.Fatalf("Mint refused the pinned provider %q: %v", provider, err)
			case !want && !errors.Is(err, ErrProviderNotPinned):
				t.Fatalf("Mint answered %q and %v for %q, and it must refuse it as an unpinned provider", raw, err, provider)
			}
		})
	}
}

// TestSignerRefusesATokenWithNoProviderClaim: the claim is required rather than
// defaulted, because a token whose provider this code cannot read is a token
// whose admission would rest on a value nobody checked.
func TestSignerRefusesATokenWithNoProviderClaim(t *testing.T) {
	signer := newTestSigner(t, testProject)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &signer.ascKey.PublicKey))

	fields := defaultIDToken(testProject)
	fields.Provider = ""
	raw, err := signer.Mint(context.Background(), firebaseIDToken(t, signer.idKey, fields), member.url)
	if err == nil || !errors.Is(err, ErrProviderNotPinned) {
		t.Fatalf("Mint answered %q and %v for a token naming no provider", raw, err)
	}
}

// TestSignerRefusesAnExchangeURLOfNoMember: the audience is the one member's
// exchange URL, and a value that is not an https URL with a host is refused
// before a key is used — an assertion minted for an URL no browser reaches is
// an assertion nobody can present, and one whose audience carries userinfo is
// not the member's own address.
func TestSignerRefusesAnExchangeURLOfNoMember(t *testing.T) {
	signer := newTestSigner(t, testProject)
	token := firebaseIDToken(t, signer.idKey, defaultIDToken(testProject))

	for _, url := range []string{
		"",
		"http://" + testMemberA + mcpauth.AuthorizePath,
		"https://person-1@" + testMemberA + mcpauth.AuthorizePath,
		"not a url",
		"/oauth/authorize",
	} {
		raw, err := signer.Mint(context.Background(), token, url)
		if !errors.Is(err, ErrUnusableExchangeURL) {
			t.Fatalf("Mint answered %q and %v for the exchange URL %q", raw, err, url)
		}
	}
}

// TestSignerNeverEchoesTheIDToken: a workspace Firebase token is a bearer, and
// the signer answers strangers on the public internet (§8's second surface), so
// no refusal of its may repeat the token, the person's address or the key id.
func TestSignerNeverEchoesTheIDToken(t *testing.T) {
	signer := newTestSigner(t, testProject)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &signer.ascKey.PublicKey))

	fields := defaultIDToken(testProject)
	fields.Provider = "custom"
	token := firebaseIDToken(t, signer.idKey, fields)
	_, err := signer.Mint(context.Background(), token, member.url)
	if err == nil {
		t.Fatal("Mint minted an assertion for an unpinned provider")
	}
	for _, secret := range []string{token, testEmail, testKid} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the refusal repeats %q: %q", secret, err)
		}
	}

	_, err = signer.Mint(context.Background(), testAssertionMarker, member.url)
	if err == nil {
		t.Fatal("Mint admitted a marker that is not an ID token")
	}
	if strings.Contains(err.Error(), testAssertionMarker) {
		t.Fatalf("the refusal repeats the presented value: %q", err)
	}
}
