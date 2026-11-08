// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
)

// The fixtures every test in this package builds from. They are names and
// markers and never a real value: no key material is written down anywhere (the
// keys are generated per test and never leave memory), and the strings the
// refusals must not repeat are markers, so a refusal that echoed one is a
// refusal a test can see.
const (
	testWorkspace  = "workspace-alpha"
	testProject    = "workspace-alpha-project"
	testWorkspace2 = "workspace-beta"
	testProject2   = "workspace-beta-project"
	testMemberA    = "member-a.example.com"
	testMemberB    = "member-b.example.com"
	testEmail      = "person-1@example.com"
	// testAssertionMarker stands in for the assertion's bytes in the one test
	// that proves a refusal never repeats them.
	testAssertionMarker = "marker-workspace-assertion"
)

// memberLimits is a member's OWN limits, the ones its token is minted with:
// a spend cap, a scope and nothing else. Every test that reads a Session reads
// this value back, which is how "the assertion supplied no limit" is checked.
var memberLimits = Limits{
	SpendUSD: 12,
	Scopes:   []string{"tasks:read"},
}

// testKey is a fresh RSA key per test: a fixture written down would be key
// material in a file, which no rule here allows, and one shared key would make
// "signed by a key this member does not pin" untestable.
func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a test key: %v", err)
	}
	return key
}

// testPublicPEM is the public half in the shape a member's pin carries.
func testPublicPEM(t *testing.T, key *rsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("marshalling a test public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// testPin is one member's pin for a workspace whose assertion key is key.
func testPin(t *testing.T, workspace, project string, key *rsa.PublicKey) Pin {
	t.Helper()
	return Pin{
		Workspace:       workspace,
		FirebaseProject: project,
		Issuer:          WorkspaceIssuer(project),
		AssertionKey:    testPublicPEM(t, key),
	}
}

// testMember is one member of a workspace: its own pin, its own coordinator URL
// and the exchange built from them.
type testMember struct {
	name     string
	url      string
	consumed *MemoryConsumedJTI
	ex       *Exchange
}

// newTestMember builds a member whose coordinator answers on host. limit is the
// member's own limits, which the test's mint is built with.
func newTestMember(t *testing.T, host string, pin Pin, opts ...ExchangeOption) *testMember {
	t.Helper()
	consumed := NewMemoryConsumedJTI(nil)
	ex, err := NewExchange(ExchangeConfig{
		Pin:               pin,
		CoordinatorIssuer: "https://" + host,
		Limits:            memberLimits,
		Consumed:          consumed,
	}, opts...)
	if err != nil {
		t.Fatalf("NewExchange for %s: %v", host, err)
	}
	return &testMember{
		name:     host,
		url:      "https://" + host + AuthorizePath,
		consumed: consumed,
		ex:       ex,
	}
}

// testAssertion is the assertion a workspace mints for a person: the window and
// the identity every test starts from, so a test that edits one claim edits
// exactly one thing. It is separate from assertionFor because a test that must
// hold a rule about the ENCODING rather than about a claim needs the same claims
// under a header of its own, and building them here rather than decoding them out
// of a minted token keeps them the values an honest signer writes.
func testAssertion(project, audience string) assertion {
	now := time.Now().UTC()
	return assertion{
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
}

// assertionFor mints an assertion the way the workspace's signer does, with the
// window and the identity every test shares unless it says otherwise.
func assertionFor(t *testing.T, key *rsa.PrivateKey, project, audience string, edit func(*assertion)) string {
	t.Helper()
	a := testAssertion(project, audience)
	if edit != nil {
		edit(&a)
	}
	raw, err := mintAssertion(key, a)
	if err != nil {
		t.Fatalf("minting a test assertion: %v", err)
	}
	return raw
}

// jtisSpent is the hashes the member's store is holding, in no order: what a
// test reads to see the shape the record is kept in.
func jtisSpent(m *testMember) []string {
	m.consumed.mu.Lock()
	defer m.consumed.mu.Unlock()
	out := make([]string, 0, len(m.consumed.spent))
	for hash := range m.consumed.spent {
		out = append(out, hash)
	}
	return out
}

func refuse(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Admit admitted %s", what)
	}
	if !errors.Is(err, ErrUnverifiedAssertion) {
		t.Fatalf("Admit refused %s with %v, and every refusal of an assertion is ErrUnverifiedAssertion", what, err)
	}
}

// TestExchangeRefusesAWorkspaceAssertionMintedForAnotherMember is the test this
// task exists for (finding 1). The whole security claim of the SSO half is that
// what a member accepts is bound to THAT one member and to one use, so a
// verifier that checked the signature without the `aud` and the `jti` would
// honour an assertion minted for another member — which is exactly what the
// red step of this task shows.
func TestExchangeRefusesAWorkspaceAssertionMintedForAnotherMember(t *testing.T) {
	key := testKey(t)
	memberA := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	// The same workspace, the same signer key, the same person — and an
	// audience that is the member next door's exchange URL.
	raw := assertionFor(t, key, testProject, "https://"+testMemberB+AuthorizePath, nil)

	sess, err := memberA.ex.Admit(context.Background(), raw)
	if err == nil {
		t.Fatalf("the exchange admitted an assertion minted for another member's exchange URL: %+v", sess)
	}
	if !errors.Is(err, ErrUnverifiedAssertion) {
		t.Fatalf("the refusal is %v, and every refusal of an assertion is ErrUnverifiedAssertion", err)
	}
	if strings.Contains(err.Error(), testMemberB) {
		t.Fatalf("the refusal names the member the assertion was minted for: %q", err)
	}
}

// TestExchangeRefusesAReplayedAssertion is finding 1's single-use rule: the
// assertion the member has already spent is refused for the assertion's
// lifetime, so a stolen one is worth one sign-in rather than a bearer.
func TestExchangeRefusesAReplayedAssertion(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	raw := assertionFor(t, key, testProject, member.url, nil)

	if _, err := member.ex.Admit(context.Background(), raw); err != nil {
		t.Fatalf("the first Admit refused an assertion this member minted for itself: %v", err)
	}
	_, err := member.ex.Admit(context.Background(), raw)
	refuse(t, err, "the same assertion a second time")
	if !strings.Contains(err.Error(), "spent") {
		t.Fatalf("the refusal is %q, and it is the replay that must be named", err)
	}
}

// TestExchangeRefusesAnAssertionReplayedAtAnotherMember is the replay a
// per-member set alone would not catch: each member's consumed-jti set is its
// own, so a second member has no record of the assertion at all — what refuses
// it there is the audience, and what the first member's own set still holds is
// the assertion it spent.
func TestExchangeRefusesAnAssertionReplayedAtAnotherMember(t *testing.T) {
	key := testKey(t)
	pin := testPin(t, testWorkspace, testProject, &key.PublicKey)
	memberA := newTestMember(t, testMemberA, pin)
	memberB := newTestMember(t, testMemberB, pin)

	raw := assertionFor(t, key, testProject, memberA.url, nil)
	if _, err := memberA.ex.Admit(context.Background(), raw); err != nil {
		t.Fatalf("the first Admit refused the assertion at its own member: %v", err)
	}

	refuse(t, mustErr(memberB.ex.Admit(context.Background(), raw)), "at a second member")
	if len(jtisSpent(memberB)) != 0 {
		t.Fatalf("the second member recorded %d spent ids, and its set is its own", len(jtisSpent(memberB)))
	}

	// And the first member's entry is still the first member's: the assertion
	// is spent where it was spent, not wherever it was later presented.
	refuse(t, mustErr(memberA.ex.Admit(context.Background(), raw)), "at the member that already spent it")
}

// TestExchangeRefusesAnExpiredAssertion reads the window against the member's
// own clock: an assertion whose lifetime has run out is refused, and so is one
// whose whole life is longer than a "short-lived" assertion may be — a
// workspace's key can mint anything, and a pin bounds what a key may be used
// for rather than how careful its holder is.
func TestExchangeRefusesAnExpiredAssertion(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	expired := assertionFor(t, key, testProject, member.url, func(a *assertion) {
		now := time.Now().UTC()
		a.IssuedAt = now.Add(-10 * time.Minute).Unix()
		a.ExpiresAt = now.Add(-time.Minute).Unix()
	})
	refuse(t, mustErr(member.ex.Admit(context.Background(), expired)), "an expired assertion")

	long := assertionFor(t, key, testProject, member.url, func(a *assertion) {
		now := time.Now().UTC()
		a.IssuedAt = now.Add(-3 * time.Hour).Unix()
		a.ExpiresAt = now.Add(3 * time.Hour).Unix()
	})
	refuse(t, mustErr(member.ex.Admit(context.Background(), long)), "an assertion with a lifetime of hours")
}

// TestExchangeRefusesAnAssertionWhoseAddressIsNotVerified is the member's own
// re-check of a fact the signer states (spec.md §3 step 2, and the package's own
// claim that this end re-checks rather than takes the workspace's word): in the
// hostile-workspace model the signer never runs at all, so an assertion that
// says its address is unverified arrives from anybody holding the pinned key,
// and it is refused here or nowhere. The token is signed by hand rather than
// minted, because the signer refuses to write such an assertion at all — that is
// a fact about the signer's own field checks, and it is not this member's.
func TestExchangeRefusesAnAssertionWhoseAddressIsNotVerified(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	claims := testAssertion(testProject, member.url).claims()
	claims["email_verified"] = false
	raw := signClaims(t, key, claims)

	refuse(t, mustErr(member.ex.Admit(context.Background(), raw)), "an assertion whose address is not marked verified")
	if len(jtisSpent(member)) != 0 {
		t.Fatalf("the refused assertion spent %d ids, and a refusal must write none", len(jtisSpent(member)))
	}
}

// TestExchangeHoldsTheAddressToTheMembersOwnRule: the address a session carries
// is this member's own spelling of it (mobileauth.NormalizeEmail, the one rule
// W5's lists are written and compared with), and never the string the workspace
// sent. An address this member cannot read is a refusal rather than a value that
// travels on, and two spellings of one address are one identity here.
func TestExchangeHoldsTheAddressToTheMembersOwnRule(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	unreadable := assertionFor(t, key, testProject, member.url, func(a *assertion) {
		a.Email = "not an address"
	})
	refuse(t, mustErr(member.ex.Admit(context.Background(), unreadable)), "an assertion carrying an address nobody can read")

	varianced := assertionFor(t, key, testProject, member.url, func(a *assertion) {
		a.Email = "Person-1@EXAMPLE.com"
	})
	sess, err := member.ex.Admit(context.Background(), varianced)
	if err != nil {
		t.Fatalf("Admit refused an address this member can read: %v", err)
	}
	if sess.Identity.Email != "Person-1@example.com" {
		t.Fatalf("the session answers the address %q, and this member's rule answers %q", sess.Identity.Email, "Person-1@example.com")
	}
}

// TestExchangeRefusesAnAssertionIssuedInTheFuture reads the window against this
// member's own clock in both directions. A future iat is how an assertion
// outlives the bound the lifetime rule puts on it: the window is short either
// way, but a clock years ahead is a document this member has no reason to
// honour, and the spent-jti entry the exchange writes is held until the
// assertion's OWN expiry — so one crafted assertion would park a replay entry
// for that whole span.
func TestExchangeRefusesAnAssertionIssuedInTheFuture(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	for what, ahead := range map[string]time.Duration{
		"minutes ahead": 10 * time.Minute,
		"years ahead":   200 * 365 * 24 * time.Hour,
	} {
		raw := assertionFor(t, key, testProject, member.url, func(a *assertion) {
			now := time.Now().UTC()
			a.IssuedAt = now.Add(ahead).Unix()
			a.ExpiresAt = now.Add(ahead + time.Minute).Unix()
		})
		refuse(t, mustErr(member.ex.Admit(context.Background(), raw)), "an assertion issued "+what)
	}
	if len(jtisSpent(member)) != 0 {
		t.Fatalf("the refused assertions parked %d spent ids, and a refusal must write none", len(jtisSpent(member)))
	}
}

// TestExchangeNeverEchoesTheAssertion is the credsvc rule applied to this
// endpoint: a refusal reaches a log line and an answer body, and neither is a
// place for the document a stranger sent.
func TestExchangeNeverEchoesTheAssertion(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	cases := map[string]string{
		"an assertion for another member":   assertionFor(t, key, testProject, "https://"+testMemberB+AuthorizePath, nil),
		"a marker that is not an assertion": testAssertionMarker,
		"an expired assertion": assertionFor(t, key, testProject, member.url, func(a *assertion) {
			now := time.Now().UTC()
			a.IssuedAt = now.Add(-10 * time.Minute).Unix()
			a.ExpiresAt = now.Add(-time.Minute).Unix()
		}),
	}
	for what, raw := range cases {
		t.Run(what, func(t *testing.T) {
			err := mustErr(member.ex.Admit(context.Background(), raw))
			if err == nil {
				t.Fatalf("Admit admitted %s", what)
			}
			msg := err.Error()
			if strings.Contains(msg, raw) {
				t.Fatalf("the refusal repeats the assertion: %q", msg)
			}
			if strings.Contains(msg, testEmail) || strings.Contains(msg, "jti-1") {
				t.Fatalf("the refusal repeats a value out of the assertion: %q", msg)
			}
		})
	}
}

// TestExchangeVerifiesTheMembersPinnedKeyNotTheRecords. The key an assertion is
// verified against is the one THIS member pins — a workspace's record names no
// key at all (§2) — so an assertion signed by another workspace's key is
// refused even though everything else about it is in order. The second half is
// the signer's: it refuses a Firebase token that is not this project's, so a
// workspace cannot be talked into minting for a project it is not either.
func TestExchangeVerifiesTheMembersPinnedKeyNotTheRecords(t *testing.T) {
	pinned, other := testKey(t), testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &pinned.PublicKey))

	stranger := assertionFor(t, other, testProject, member.url, nil)
	refuse(t, mustErr(member.ex.Admit(context.Background(), stranger)), "an assertion signed by a key this member does not pin")

	signer := newTestSigner(t, testProject)
	token := firebaseIDToken(t, signer.idKey, defaultIDToken(testProject2))
	if _, err := signer.Mint(context.Background(), token, member.url); !errors.Is(err, ErrUnverifiedIDToken) {
		t.Fatalf("the signer answered %v for a token of another project, and it must refuse it as an unverified ID token", err)
	}

	// And nothing in this package reads the workspace's published record: the
	// record is not an input anywhere, which is what makes "the member's pin
	// and never a value from the workspace's record" a fact about the code and
	// not a promise about the caller.
	assertPackageReadsNoWorkspaceRecord(t)
}

// TestExchangeVerifiesTheMembersPinnedIssuerNotTheRecords is the other half of
// the pin: the issuer and the project are the member's own, so an assertion
// naming another workspace's issuer or project is refused however well it is
// signed, and the signer refuses a Firebase token whose iss is not the
// workspace's pinned issuer.
func TestExchangeVerifiesTheMembersPinnedIssuerNotTheRecords(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	otherIssuer := assertionFor(t, key, testProject, member.url, func(a *assertion) {
		a.Issuer = WorkspaceIssuer(testProject2)
	})
	refuse(t, mustErr(member.ex.Admit(context.Background(), otherIssuer)), "an assertion naming another workspace's issuer")

	otherProject := assertionFor(t, key, testProject2, member.url, nil)
	refuse(t, mustErr(member.ex.Admit(context.Background(), otherProject)), "an assertion naming another workspace's project")

	// The pin's two halves are compared separately and neither holds the other
	// up: an assertion that states the issuer this member pins — the workspace it
	// signed for — with some other project is refused by the project's own line,
	// so a session can never be marked with a workspace project the pin never
	// named.
	otherSubject := assertionFor(t, key, testProject, member.url, func(a *assertion) {
		a.Subject = testProject2
	})
	refuse(t, mustErr(member.ex.Admit(context.Background(), otherSubject)), "an assertion naming a workspace project this member does not pin")

	signer := newTestSigner(t, testProject)
	fields := defaultIDToken(testProject)
	fields.Issuer = WorkspaceIssuer(testProject2)
	if _, err := signer.Mint(context.Background(), firebaseIDToken(t, signer.idKey, fields), member.url); !errors.Is(err, ErrUnverifiedIDToken) {
		t.Fatalf("the signer answered %v for a token whose iss is another project's, and it must refuse it as an unverified ID token", err)
	}
}

// TestExchangeRefusesWhenTheMemberPinsNoWorkspace is the member's own
// `workspace rm` (W1) as the exchange sees it: the membership key AND the pin
// are gone, so there is no key, no project and no issuer left to verify
// against, and the exchange refuses whatever the workspace's record still says
// about this member. It is the half of a removal only the member can complete
// (spec.md §3), and it is a refusal of its own rather than a bad-assertion one.
func TestExchangeRefusesWhenTheMemberPinsNoWorkspace(t *testing.T) {
	key := testKey(t)
	unpinned := newTestMember(t, testMemberA, Pin{})
	raw := assertionFor(t, key, testProject, unpinned.url, nil)

	_, err := unpinned.ex.Admit(context.Background(), raw)
	if !errors.Is(err, ErrNoWorkspacePinned) {
		t.Fatalf("Admit answered %v, and a member that pins no workspace refuses with ErrNoWorkspacePinned", err)
	}
	if errors.Is(err, ErrUnverifiedAssertion) {
		t.Fatal("the refusal reads as a bad assertion, and there is no assertion this member could accept")
	}

	// A pin that is there is not this state: the same call admits with a pin.
	pinned := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	if _, err := pinned.ex.Admit(context.Background(), assertionFor(t, key, testProject, pinned.url, nil)); err != nil {
		t.Fatalf("a member with a pin refused its own assertion: %v", err)
	}
}

// TestExchangeRefusalIsIndistinguishableForAnUnknownMember. spec.md §8 forbids
// member enumeration: a member the person is not in is refused with a refusal
// identical in shape to one for a member that does not exist. An assertion
// minted for a member that is nowhere is refused exactly as one minted for the
// member next door, and neither refusal names a member.
func TestExchangeRefusalIsIndistinguishableForAnUnknownMember(t *testing.T) {
	key := testKey(t)
	memberA := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))

	nextDoor := "https://" + testMemberB + AuthorizePath
	unknown := "https://" + testMemberA + "-that-is-not-there.example.com" + AuthorizePath
	errNext := mustErr(memberA.ex.Admit(context.Background(), assertionFor(t, key, testProject, nextDoor, nil)))
	errNone := mustErr(memberA.ex.Admit(context.Background(), assertionFor(t, key, testProject, unknown, nil)))
	if errNext == nil || errNone == nil {
		t.Fatal("a member admitted an assertion minted for an exchange URL that is not its own")
	}
	if errNext.Error() != errNone.Error() {
		t.Fatalf("the refusals differ: a member that exists answers %q and one that does not answers %q", errNext, errNone)
	}
	for _, name := range []string{testMemberB, "-that-is-not-there"} {
		if strings.Contains(errNext.Error(), name) {
			t.Fatalf("the refusal names the member it was minted for: %q", errNext)
		}
	}
}

// TestExchangeAcceptsAProviderThatIsPinnedAndRefusesOneThatIsNot is finding 7
// at the member: `google.com` and `password` are the providers the workspace's
// Firebase carries, `custom` and `anonymous` are refused — and the claim is
// re-checked here rather than taken from the signer's word, which is why this
// test mints the assertion itself rather than going through the signer.
func TestExchangeAcceptsAProviderThatIsPinnedAndRefusesOneThatIsNot(t *testing.T) {
	key := testKey(t)
	for provider, want := range map[string]bool{
		"google.com": true,
		"password":   true,
		"custom":     false,
		"anonymous":  false,
		"oidc.other": false,
	} {
		t.Run(provider, func(t *testing.T) {
			member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
			raw := assertionFor(t, key, testProject, member.url, func(a *assertion) { a.Provider = provider })
			sess, err := member.ex.Admit(context.Background(), raw)
			if !want {
				refuse(t, err, "an assertion whose provider is not pinned")
				return
			}
			if err != nil {
				t.Fatalf("Admit refused the pinned provider %q: %v", provider, err)
			}
			if sess.Identity.Provider != provider {
				t.Fatalf("the session names the provider %q, and the assertion named %q", sess.Identity.Provider, provider)
			}
		})
	}
}

// TestExchangeMintsAMemberTokenWithTheMembersOwnLimits. The exchange's answer is
// what the member's own mint is built from, and the limits in it are the
// member's own configuration's — carried unchanged, never read from the
// assertion. A workspace cannot widen or narrow what its people may do at this
// member by anything it puts in an assertion, and the payload's closed claim
// set is what refuses the attempt rather than trusting it to be harmless.
func TestExchangeMintsAMemberTokenWithTheMembersOwnLimits(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	sess, err := member.ex.Admit(context.Background(), assertionFor(t, key, testProject, member.url, nil))
	if err != nil {
		t.Fatalf("Admit refused the member's own assertion: %v", err)
	}
	if !slices.Equal(sess.Limits.Scopes, memberLimits.Scopes) || sess.Limits.SpendUSD != memberLimits.SpendUSD ||
		sess.Limits.MinRigor != memberLimits.MinRigor || sess.Limits.MaxRigor != memberLimits.MaxRigor || sess.Limits.Hotfix != memberLimits.Hotfix {
		t.Fatalf("the session's limits are %+v, and this member's own are %+v", sess.Limits, memberLimits)
	}

	// The member's mint, over the session: the limits it validates are the
	// member's own, so a token minted from this sign-in carries exactly what a
	// token minted from a sign-in that started at the member carries.
	token := mcpauth.Token{
		Identity:  "user:" + sess.Identity.Email,
		ClientID:  "client-1",
		Limits:    mcpauth.Limits(sess.Limits),
		ExpiresAt: time.Now().UTC().Add(mcpauth.AccessTokenTTL),
	}
	if err := token.Validate(); err != nil {
		t.Fatalf("the member's own mint refuses the session's limits: %v", err)
	}

	// And the assertion cannot carry limits of its own: a payload with a claim
	// this member does not model is refused rather than read past.
	widened := assertionWithClaim(t, key, testProject, member.url, "limits", map[string]any{"spend_usd": 1000})
	refuse(t, mustErr(member.ex.Admit(context.Background(), widened)), "an assertion carrying a limits claim")
}

// TestExchangeStoresOnlyHashes is D1296: the one thing the member stores about a
// sign-in is the hash of the assertion's id, in a subtree no client of its own
// database may read. Nothing at rest can be presented, so a reader of that
// subtree holds nothing it could sign in with.
func TestExchangeStoresOnlyHashes(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	raw := assertionFor(t, key, testProject, member.url, func(a *assertion) { a.JTI = "jti-single-use" })

	if _, err := member.ex.Admit(context.Background(), raw); err != nil {
		t.Fatalf("Admit refused the member's own assertion: %v", err)
	}
	spent := jtisSpent(member)
	if len(spent) != 1 {
		t.Fatalf("the store holds %d entries, and one assertion is one spent id", len(spent))
	}
	if spent[0] != TokenHash("jti-single-use") {
		t.Fatalf("the store holds %q, and a spent id is kept as its hash", spent[0])
	}
	if spent[0] == "jti-single-use" {
		t.Fatal("the store holds the jti itself")
	}
	if !strings.HasPrefix(PathConsumedJTI(spent[0]), ConsumedJTIPrefix) || strings.Contains(PathConsumedJTI(spent[0]), "jti-single-use") {
		t.Fatalf("PathConsumedJTI(%q) = %q, and the path carries the hash rather than the id", spent[0], PathConsumedJTI(spent[0]))
	}
}

// TestExchangeTakesTheFrontDoorsShapes is half of the guard that keeps the
// second implementation honest (workspace_signer.go's file comment says why
// there is one). Three things this package shares a MEANING with the member's
// front door — the authorize path an assertion's audience ends in, the hash a
// spent id is kept as, and the limits the member's own token is minted with —
// and each is spelled here rather than imported, because no non-test file of
// this package may name that package at all. They are held to each other here:
// a rename or a retype on either side fails this test, and a test file may name
// the front door because the guard is about what a binary links.
//
// The limits comparison is field by field rather than a conversion, because a
// conversion is exactly what the wiring does (mcpauth.Limits(session.Limits)) and
// a struct literal that compiled but dropped a field is the failure this has to
// catch.
func TestExchangeTakesTheFrontDoorsShapes(t *testing.T) {
	if AuthorizePath != mcpauth.AuthorizePath {
		t.Errorf("this package's authorize path is %q and the front door's is %q: an assertion's audience ends in one of them, and two spellings of one endpoint refuse every sign-in", AuthorizePath, mcpauth.AuthorizePath)
	}
	if got, want := TokenHash("marker-assertion-id"), mcpauth.TokenHash("marker-assertion-id"); got != want {
		t.Errorf("a spent id is kept as %q here and as %q by the front door, and the two sit in the same project's database", got, want)
	}
	mine, theirs := reflect.TypeOf(Limits{}), reflect.TypeOf(mcpauth.Limits{})
	if mine.NumField() != theirs.NumField() {
		t.Fatalf("this package's limits carry %d fields and the front door's carry %d, so the member's own mint would read one the exchange never wrote", mine.NumField(), theirs.NumField())
	}
	for i := 0; i < mine.NumField(); i++ {
		if mine.Field(i).Name != theirs.Field(i).Name || mine.Field(i).Type != theirs.Field(i).Type {
			t.Errorf("limits field %d is %s %s here and %s %s at the front door", i, mine.Field(i).Name, mine.Field(i).Type, theirs.Field(i).Name, theirs.Field(i).Type)
		}
	}
}

// TestExchangeRefusesAWindowThatWouldOverflowTheLifetimeBound is the hostile
// signer at the one piece of arithmetic the window check does. A workspace holds
// the assertion key, so it chooses both claims, and the bound on the assertion's
// lifetime — the member's own protection, since a pin says what a key may be used
// for and not that its holder is careful — is a subtraction of two values that
// hostile. Computed in int64 it wraps for a pair of extremes, and an assertion
// whose lifetime is "negative" is one that never expires: it would be admitted
// here, its jti would be held until an expiry far beyond any reader, and a stolen
// copy would be a bearer for as long as the member's database exists.
func TestExchangeRefusesAWindowThatWouldOverflowTheLifetimeBound(t *testing.T) {
	key := testKey(t)
	member := newTestMember(t, testMemberA, testPin(t, testWorkspace, testProject, &key.PublicKey))
	now := time.Now().UTC()

	for what, window := range map[string]struct{ iat, exp int64 }{
		// The pair that wraps: a lifetime of five minutes minus two to the
		// sixty-third is a negative number, and every other check passes.
		"an iat at the bottom of int64":       {math.MinInt64, now.Add(5 * time.Minute).Unix()},
		"a window from the bottom to the top": {math.MinInt64, math.MaxInt64},
		// Not an overflow, and refused by the same bound: an assertion that really
		// does live for centuries.
		"an exp at the top of int64": {now.Add(-time.Minute).Unix(), math.MaxInt64},
	} {
		t.Run(what, func(t *testing.T) {
			raw := assertionFor(t, key, testProject, member.url, func(a *assertion) {
				a.IssuedAt, a.ExpiresAt = window.iat, window.exp
			})
			refuse(t, mustErr(member.ex.Admit(context.Background(), raw)), what)
			if len(jtisSpent(member)) != 0 {
				t.Fatalf("the refused assertion spent a jti, and a refusal must not burn an id")
			}
		})
	}
}
