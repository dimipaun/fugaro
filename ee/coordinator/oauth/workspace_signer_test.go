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
// own reading (the drift test at the end of this file is what holds it to the
// front door's).
const testKid = "kid-1"

// fakeKeys is the published key set the ID tokens are verified against, as a
// test's own: the signer reads a KeySource the way the published JWKS is read,
// and the concrete one is handed in by whatever wires the signer.
type fakeKeys map[string]*rsa.PublicKey

func (f fakeKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	key, ok := f[kid]
	if !ok {
		return nil, ErrUnknownKeyID
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
	signer, err := NewSigner(SignerConfig{
		FirebaseProject: project,
		AssertionKeyPEM: assertionKeyPEM(t, assertionKey),
		Keys:            fakeKeys{testKid: &idKey.PublicKey},
	}, opts...)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return &testSigner{Signer: signer, idKey: idKey, ascKey: assertionKey}
}

// assertionKeyPEM is a signing key as the workspace's own configuration carries
// it: the private half, in the PEM shape NewSigner reads.
func assertionKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling a test signing key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// idTokenFields is one ID token's claims as a test writes them. The zero value
// is not usable: defaultIDToken fills in the token every test shares.
type idTokenFields struct {
	Audience string
	Issuer   string
	Subject  string
	// UserID is the `user_id` claim, which Firebase writes beside `sub` and which
	// both verifiers require to agree with it. Empty means f.Subject.
	UserID string
	// Alg is what the header claims the token is signed with. Empty means RS256.
	// The signature is always a real RS256 signature under the key, whatever this
	// says: the token under test is one whose header lies about it.
	Alg           string
	Email         string
	EmailVerified any
	Provider      string
	IssuedAt      time.Time
	ExpiresAt     time.Time
	// NotBefore is the token's `nbf`, absent unless a test sets it: a token
	// Firebase mints for a person carries none, and the case that matters is the
	// token that carries one in the future.
	NotBefore *time.Time
	KeyID     string
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
	uid := f.UserID
	if uid == "" {
		uid = f.Subject
	}
	claims := map[string]any{
		"aud":     f.Audience,
		"iss":     f.Issuer,
		"sub":     f.Subject,
		"user_id": uid,
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
	if f.NotBefore != nil {
		claims["nbf"] = f.NotBefore.Unix()
	}
	alg := f.Alg
	if alg == "" {
		alg = "RS256"
	}
	head, err := json.Marshal(map[string]string{"alg": alg, "kid": f.KeyID})
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

// TestNewSignerRefusesAConfigurationThatCouldNotSign: every argument the signer
// needs is required at construction rather than at somebody's sign-in, because a
// signer built without one refuses the person's own token and reports a
// deployment mistake as a refusal of a stranger. The key set is the one worth
// naming: with the guard gone nothing here fails until Mint reads through the
// nil interface and panics, which is a crash in a request handler rather than a
// refusal an operator can act on.
func TestNewSignerRefusesAConfigurationThatCouldNotSign(t *testing.T) {
	key := testKey(t)
	whole := SignerConfig{
		FirebaseProject: testProject,
		AssertionKeyPEM: assertionKeyPEM(t, key),
		Keys:            fakeKeys{testKid: &key.PublicKey},
	}
	if _, err := NewSigner(whole); err != nil {
		t.Fatalf("NewSigner refused a configuration that has everything it needs: %v", err)
	}

	for what, edit := range map[string]func(*SignerConfig){
		"no project":        func(c *SignerConfig) { c.FirebaseProject = "" },
		"no published keys": func(c *SignerConfig) { c.Keys = nil },
		"no assertion key":  func(c *SignerConfig) { c.AssertionKeyPEM = "" },
		"a TTL longer than the longest a member accepts": func(c *SignerConfig) {
			c.TTL = maxAssertionLifetime + time.Minute
		},
		"a negative TTL": func(c *SignerConfig) { c.TTL = -time.Minute },
	} {
		t.Run(what, func(t *testing.T) {
			cfg := whole
			edit(&cfg)
			if s, err := NewSigner(cfg); err == nil {
				t.Fatalf("NewSigner accepted %s and answered %+v, which would fail at somebody's sign-in", what, s)
			}
		})
	}
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
// anything a caller sent — the token is verified against the workspace's own
// project, whose audience is the bare project id and whose issuer is built from
// it — and the drift test at the end of this file holds that reading to the
// front door's verifier.
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
			if !errors.Is(err, ErrUnverifiedIDToken) {
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
		"http://" + testMemberA + AuthorizePath,
		"https://person-1@" + testMemberA + AuthorizePath,
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

// frontDoorKeys is the same published set as fakeKeys, read through the front
// door's own interface: each package reads a kid through its own, and the drift
// test below needs one of each.
type frontDoorKeys map[string]*rsa.PublicKey

func (f frontDoorKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	key, ok := f[kid]
	if !ok {
		return nil, mcpauth.ErrUnknownKeyID
	}
	return key, nil
}

// TestSignerVerifiesWhatTheFrontDoorVerifies is what holds this package's second
// copy of the Firebase ID token rules to the first. The front door's verifier
// (ee/coordinator/mcpauth, M6) and this signer's are handed the SAME tokens and
// must answer the same verdict for every one of them, so a rule that moves on
// one side fails here rather than at somebody's sign-in.
//
// A _test.go file may name the front door where a non-test one may not: the
// guard that refuses the name is about what a binary links, and no test file is
// linked into one.
//
// One difference is deliberate, is not a drift, and is why the verdicts are
// compared rather than the answers: the ADDRESS rule. The front door folds a
// printable ASCII address to lower case; this package reads the address through
// mobileauth.NormalizeEmail, the member's own one rule for addresses (W5's lists
// are written and compared with it), which folds the domain and leaves the local
// part as the person wrote it. Both refuse an address neither can read, both
// admit every address below, and the address the assertion carries is normalized
// again by the member before any comparison — so the two readings answer the
// same verdict on every token, which is what a member's trust rests on.
func TestSignerVerifiesWhatTheFrontDoorVerifies(t *testing.T) {
	signer := newTestSigner(t, testProject)
	idKey := signer.idKey
	front, err := mcpauth.NewVerifier(testProject, frontDoorKeys{testKid: &idKey.PublicKey})
	if err != nil {
		t.Fatalf("building the front door's verifier: %v", err)
	}

	honest := firebaseIDToken(t, idKey, defaultIDToken(testProject))
	// The signature's first byte, with one bit flipped, and the token re-encoded:
	// the payload is untouched, so the only thing that can refuse this token is
	// the signature. It is done on the DECODED bytes rather than on the base64url
	// text on purpose — the last character of a 342-character encoding of a
	// 256-byte signature carries only two significant bits, so a different last
	// character can decode to the very same bytes and leave the token verifiable.
	parts := strings.Split(honest, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decoding a test ID token's signature: %v", err)
	}
	sig[0] ^= 1
	tampered := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig)

	cases := map[string]struct {
		token     string
		admitted  bool
		sameEmail bool
	}{}
	add := func(what string, f idTokenFields) {
		cases[what] = struct {
			token     string
			admitted  bool
			sameEmail bool
		}{firebaseIDToken(t, idKey, f), true, true}
	}
	add("the token this project's Firebase mints", defaultIDToken(testProject))

	for what, edit := range map[string]func(*idTokenFields){
		"another project's audience": func(f *idTokenFields) { f.Audience = testProject2 },
		"another project's issuer":   func(f *idTokenFields) { f.Issuer = WorkspaceIssuer(testProject2) },
		"an expired token":           func(f *idTokenFields) { f.ExpiresAt = time.Now().UTC().Add(-time.Minute) },
		"a token from the future":    func(f *idTokenFields) { f.IssuedAt = time.Now().UTC().Add(time.Hour) },
		// The row that holds the signer's own `nbf` refusal to the front door's:
		// without it this deliberate copy could lose the rule and still pass here.
		"a token that is not valid yet": func(f *idTokenFields) {
			nbf := time.Now().UTC().Add(time.Hour)
			f.NotBefore = &nbf
		},
		"an unverified address":      func(f *idTokenFields) { f.EmailVerified = false },
		"no verified claim at all":   func(f *idTokenFields) { f.EmailVerified = nil },
		"no uid":                     func(f *idTokenFields) { f.Subject = "" },
		"an address nobody can read": func(f *idTokenFields) { f.Email = "not an address" },
		"an unpublished key id":      func(f *idTokenFields) { f.KeyID = "kid-2" },
		// The header's own algorithm, and the uid's two copies: the signer's twin
		// of these rules is applied at the member (workspace_assertion.go), and
		// they are rows here so that losing either on this side fails against the
		// front door instead of passing quietly. The token is SIGNED with the
		// published key in every one of them, so the signature is no help to
		// either verifier.
		"a header that names the HMAC family": func(f *idTokenFields) { f.Alg = "HS256" },
		"a header that names no algorithm":    func(f *idTokenFields) { f.Alg = "none" },
		"a user_id that is not the sub":       func(f *idTokenFields) { f.UserID = "uid-2" },
	} {
		f := defaultIDToken(testProject)
		edit(&f)
		cases[what] = struct {
			token     string
			admitted  bool
			sameEmail bool
		}{firebaseIDToken(t, idKey, f), false, false}
	}
	cases["a signature that was edited"] = struct {
		token     string
		admitted  bool
		sameEmail bool
	}{tampered, false, false}

	for what, c := range cases {
		t.Run(what, func(t *testing.T) {
			mine, errMine := signer.verifyIDToken(context.Background(), c.token)
			theirs, errTheirs := front.VerifyIDToken(context.Background(), c.token)
			if (errMine == nil) != (errTheirs == nil) {
				t.Fatalf("this signer answered %v and the front door answered %v for %s: the two readings of one token disagree", errMine, errTheirs, what)
			}
			if c.admitted && errMine != nil {
				t.Fatalf("both verifiers refused %s: %v", what, errMine)
			}
			if !c.admitted {
				if !errors.Is(errMine, ErrUnverifiedIDToken) {
					t.Fatalf("the refusal of %s is %v, and every one of them is ErrUnverifiedIDToken", what, errMine)
				}
				return
			}
			if c.sameEmail && mine.Email != theirs.Email {
				t.Fatalf("this signer reads the address as %q and the front door as %q", mine.Email, theirs.Email)
			}
			if mine.Provider != "google.com" {
				t.Fatalf("this signer read the provider as %q, and the token names google.com", mine.Provider)
			}
		})
	}
}
