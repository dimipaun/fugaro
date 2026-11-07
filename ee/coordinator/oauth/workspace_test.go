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
		Limits:            mcpauth.Limits{SpendUSD: 12},
		Consumed:          consumed,
	}, opts...)
	if err != nil {
		t.Fatalf("NewExchange for %s: %v", host, err)
	}
	return &testMember{
		name:     host,
		url:      "https://" + host + mcpauth.AuthorizePath,
		consumed: consumed,
		ex:       ex,
	}
}

// assertionFor mints an assertion the way the workspace's signer does, with the
// window and the identity every test shares unless it says otherwise.
func assertionFor(t *testing.T, key *rsa.PrivateKey, project, audience string, edit func(*assertion)) string {
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
	raw := assertionFor(t, key, testProject, "https://"+testMemberB+mcpauth.AuthorizePath, nil)

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
