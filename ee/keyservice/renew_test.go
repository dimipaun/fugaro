// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The renewal and status endpoints (§7, §8, D2135, D2140): the two routes a
// project's key reaches the service by. Every test here drives them through the
// handler, because what these routes owe is an answer to a caller and not a
// function's return value — the uniform answer, the absence of a key and the
// refusal to a revoked key are all facts about a response.
//
// The secret is generated as the test runs and never written as a literal
// (.claude/rules/ee.md, R5), and every key a test holds was minted through the
// fixture's own minter and read back through ee/licence's verifier.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// unknownKeyID is a key the service never issued. A key id is not a secret (it
// is what a support ticket quotes), and this one is obviously not real.
const unknownKeyID = "lk_test_never_issued"

// v1Spy is the minter's v1 half as a test sees it: the fixture's own minter —
// so a key it answers is one the released verifier accepts — with what it was
// asked and what it answered kept aside.
type v1Spy struct {
	inner   MintsV1
	asked   []RequestV1
	keys    []string
	secrets []string
	err     error
}

func (v *v1Spy) MintV1(ctx context.Context, req RequestV1) (MintedV1, error) {
	v.asked = append(v.asked, req)
	if v.err != nil {
		return MintedV1{}, v.err
	}
	minted, err := v.inner.MintV1(ctx, req)
	if err != nil {
		return MintedV1{}, err
	}
	v.keys = append(v.keys, minted.Key)
	v.secrets = append(v.secrets, minted.RenewalSecret)
	return minted, nil
}

// minted is how many keys the minter was asked for, which is how a test says
// "nothing was minted" without reading the store.
func (v *v1Spy) minted() int {
	return len(v.keys)
}

// renewing is the fixture with a v1 minter wired into the intake: the renewal
// endpoint's one door to a key (Check 174), and the same minter the fixture
// signs with.
func renewing(t *testing.T) (*fixture, *v1Spy) {
	t.Helper()
	return renewingWithin(t, Limits{Requests: 50, Window: time.Hour})
}

// renewingWithin is the same fixture with the intake's rate the test's own,
// which is what a test about the throttle needs and no other test ever meets.
func renewingWithin(t *testing.T, limits Limits) (*fixture, *v1Spy) {
	t.Helper()
	spy := &v1Spy{}
	f := newFixtureWith(t, Production, func(f *fixture) {
		spy.inner = Minter{Signer: f.signer, Keys: f.keys, Environment: f.env, Now: f.clock}
		f.deps.MintsV1 = spy
		f.cfg.Throttle = limits
	})
	return f, spy
}

// freshSecret is a renewal secret generated as the test runs: 256 bits from the
// system's CSPRNG, which is what every secret in these tests is (.claude/rules/
// ee.md, R5 — a fixture that looked like a secret would be one).
func freshSecret(t *testing.T) string {
	t.Helper()
	secret, err := newSecret()
	if err != nil {
		t.Fatalf("generating a renewal secret: %v", err)
	}
	return secret
}

// heldKey is a key an issue path has already delivered: minted through the
// minter's own service, with the record and the state a renewal reads. It is
// written here because the issue paths that will do this are later tasks in the
// plan (the purchase email and the account page are K2.5's and K2.4's), and a
// renewal test needs a holder rather than a purchase.
func heldKey(t *testing.T, f *fixture, policy string) (keyID, secret, key string) {
	t.Helper()
	return heldKeyIn(t, f, policy, workspaceID)
}

// heldKeyIn is the same key issued into the workspace named, which is what a
// renewal is checked against: the successor goes into the key's own workspace,
// and one that names another is a licence that lives somewhere else.
func heldKeyIn(t *testing.T, f *fixture, policy, workspace string) (keyID, secret, key string) {
	t.Helper()
	ctx := context.Background()
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: f.env, Now: f.clock}
	minted, err := minter.MintV1(ctx, RequestV1{
		Environment:  f.env,
		Policy:       policy,
		WorkspaceID:  workspace,
		Organisation: "acme.example",
		Seats:        3,
	})
	if err != nil {
		t.Fatalf("minting the key the caller holds: %v", err)
	}
	row, known := DefaultPolicy(policy)
	if !known {
		t.Fatalf("the fixture holds no row named %q", policy)
	}
	record := minted.Record
	record.Source = "purchase:test:" + record.KeyID
	record.Email = buyerAddress("acme.example")
	// The key is one the checkout sold, so its record names the subscription it
	// was minted for and the store holds it: a renewal reads it before it mints
	// (§7), and a key with no subscription is the one that cannot renew at all
	// (TestAKeyWithNoSubscriptionIsRefused).
	record.Subscription = subscriptionFor(record.KeyID)
	if err := f.store.SaveIssuance(ctx, record); err != nil {
		t.Fatalf("writing the key's record: %v", err)
	}
	// The subscription is paid through the term the successor will cover, which is
	// the state the merchant's own renewal leaves behind: the trigger runs inside the
	// key's window and moves the end (merchant.Renew), and `/renew` hands out a term
	// that end already reaches and never extends it — a subscription ending where the
	// key in hand ends has bought no successor, and a renewal against one mints
	// nothing (TestARenewalMintsOnlyIntoATermTheSubscriptionCovers).
	if err := f.store.SaveSubscription(ctx, Subscription{
		ID:           record.Subscription,
		Organisation: record.Organisation,
		WorkspaceID:  workspace,
		Seats:        record.Seats,
		TermDays:     int(row.Term / (24 * time.Hour)),
		Email:        record.Email,
		At:           f.now,
		Expires:      covered(record, row, f.now),
	}); err != nil {
		t.Fatalf("writing the key's subscription: %v", err)
	}
	if err := f.store.SaveKeyState(ctx, KeyState{
		KeyID:             record.KeyID,
		WorkspaceID:       workspace,
		RenewalSecretHash: secretHash(minted.RenewalSecret),
		LastSeen:          f.now,
	}); err != nil {
		t.Fatalf("writing the key's state: %v", err)
	}
	return record.KeyID, minted.RenewalSecret, minted.Key
}

// issuance is the record the store holds of one key, which is what a renewal
// reads its term and its window from.
func (f *fixture) issuance(t *testing.T, keyID string) Record {
	t.Helper()
	for _, record := range f.records() {
		if record.KeyID == keyID {
			return record
		}
	}
	f.t.Fatalf("the store holds no record of the key %s", keyID)
	return Record{}
}

// inTheRenewalWindow moves the clock to where a renewal is asked for and answers
// that instant: a day inside the lead the key's own row names before its
// `paid_through`, which is where the job asks (§7, T26) and the only place the
// service mints a successor from (renewable). A key issued a moment ago is a
// whole term away from its window, so every test that renews starts here.
func (f *fixture) inTheRenewalWindow(t *testing.T, keyID string) time.Time {
	t.Helper()
	record := f.issuance(t, keyID)
	policy, known := DefaultPolicy(record.Plan)
	if !known {
		t.Fatalf("the key was minted from the row %q, which is not in the policy table", record.Plan)
	}
	opens := record.Expires.Add(-time.Duration(policy.RenewBeforeDays) * 24 * time.Hour)
	if asked := opens.Add(24 * time.Hour); asked.After(f.now) {
		f.advance(asked.Sub(f.now))
	}
	return f.now
}

// subscriptionFor is the subscription id the fixture's key was sold under. It is
// derived from the key id so that a test can name the subscription of a key it
// has just minted without the fixture handing it back, and the id is not a
// secret: it is the merchant's own, and it is what a support ticket quotes.
func subscriptionFor(keyID string) string {
	return "sub-" + keyID
}

// setSubscription writes a subscription id into one key's record where it lies,
// which is what the records a licensor mints by hand have and the sold ones are
// given. The record is the store's and a source is only ever written
// once, so editing it is the only way to reach the two states a test is about:
// a key whose record names a subscription the merchant has cancelled or ended,
// and one whose record names none at all.
func setSubscription(t *testing.T, f *fixture, keyID, id string) {
	t.Helper()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	for i, record := range f.store.issuances {
		if record.KeyID == keyID {
			f.store.issuances[i].Subscription = id
			return
		}
	}
	t.Fatalf("the store holds no record of the key %s", keyID)
}

// withoutASubscription takes the subscription out of one key's record, which is
// the record of a key the licensor minted by hand — the admin page's, the mint
// tool's, a trial's — rather than sold.
func withoutASubscription(t *testing.T, f *fixture, keyID string) {
	t.Helper()
	setSubscription(t, f, keyID, "")
}

// withoutAWorkspace is the same key's state with its workspace taken away, which
// is what an issue path that never learned one leaves behind.
func withoutAWorkspace(t *testing.T, f *fixture, keyID string) {
	t.Helper()
	ctx := context.Background()
	state, held, err := f.store.KeyState(ctx, keyID)
	if err != nil || !held {
		t.Fatalf("reading the key's state back: held=%v err=%v", held, err)
	}
	state.WorkspaceID = ""
	if err := f.store.SaveKeyState(ctx, state); err != nil {
		t.Fatalf("writing the key's state back: %v", err)
	}
}

// call posts one renewal or status request the way the customer's job does: a
// JSON body over the intake's own origin.
func (f *fixture) call(route string, call RenewalCall) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(call)
	if err != nil {
		f.t.Fatalf("encoding the request: %v", err)
	}
	return f.raw(route, string(body))
}

// raw posts one route's body as it stands, which is how a test sends the bodies
// the service must refuse.
func (f *fixture) raw(route, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.rawFrom(applicantIP, route, body)
}

// rawFrom posts the same body from an address of the test's own, which is what
// a test about the throttle needs: the address is one of its two buckets.
func (f *fixture) rawFrom(ip, route, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+route, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// callFrom is one request the job makes, from an address of the test's own.
func (f *fixture) callFrom(ip, route string, call RenewalCall) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(call)
	if err != nil {
		f.t.Fatalf("encoding the request: %v", err)
	}
	return f.rawFrom(ip, route, string(body))
}

// handovers is how many rotations' answers the store is holding, which is what
// a test about the window's sweep reads rather than what a route answers.
func (f *fixture) handovers() int {
	f.t.Helper()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	return len(f.store.handovers)
}

// answerOf is the answer one route wrote, as the job reads it.
func answerOf(t *testing.T, rec *httptest.ResponseRecorder) Answer {
	t.Helper()
	var answer Answer
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the answer %q is not the shape this service writes: %v", rec.Body.String(), err)
	}
	return answer
}

// TestRenewalAnswersAreUniform: F7 and §7.1 — the pair reveals nothing to a
// caller that has not proved the secret. An unknown key id, a secret that hashes
// to something else, a body this service does not read and a request that named
// nothing are one status and one sentence, on both routes: any difference
// between them would let a stranger ask whether a key exists, which is the
// lookup these routes must not be.
func TestRenewalAnswersAreUniform(t *testing.T) {
	f, spy := renewing(t)
	keyID, _, _ := heldKey(t, f, PolicyTeamMonthly)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"an unknown key id", `{"key_id":"` + unknownKeyID + `","workspace_id":"` + workspaceID + `","renewal_secret":"` + freshSecret(t) + `"}`},
		{"a wrong secret", `{"key_id":"` + keyID + `","workspace_id":"` + workspaceID + `","renewal_secret":"` + freshSecret(t) + `"}`},
		{"a rename or a field the service does not read", `{"key_id":"` + keyID + `","workspace_id":"` + workspaceID + `","renewal_secret":"x","seats":3}`},
		{"a body that is not JSON at all", `key_id=` + keyID},
		{"a request that named nothing", `{}`},
	} {
		for _, route := range []string{RouteRenew, RouteStatus} {
			rec := f.raw(route, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s answered %d for %s, want %d: a caller that has not proved the secret is answered alike whatever was wrong (F7)", route, rec.Code, tc.name, http.StatusForbidden)
			}
			if body := rec.Body.String(); body != renewalRefusal+"\n" {
				t.Errorf("%s answered %q for %s, want the one refusal %q: the wording must not say what was wrong", route, body, tc.name, renewalRefusal)
			}
		}
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) by callers that proved nothing, want none", got)
	}
	// The control: the holder is served, so what the loop above read is the
	// uniform refusal and not a route that refuses everybody.
	held, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	if rec := f.call(RouteStatus, RenewalCall{KeyID: held, WorkspaceID: workspaceID, RenewalSecret: secret}); rec.Code != http.StatusOK {
		t.Errorf("the holder's status call was answered %d, want %d: a refusal that is uniform because nobody is served is not the answer F7 asks for", rec.Code, http.StatusOK)
	}
}

// TestTheKeyAloneCannotRenew: D2135 as amended — a key carries the hash of its
// renewal secret, and a reader of the runs bucket holds the key, so the hash is
// not a credential. A caller presenting the key itself, or the hash out of its
// payload, is refused: the secret is what authenticates, and only its holder
// has it.
func TestTheKeyAloneCannotRenew(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, key := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	claims := v1Verified(t, f, key)
	if claims.RenewalSecretHash == "" {
		t.Fatal("the key carries no renewal secret hash, so this test cannot tell the hash from the secret")
	}

	for _, tc := range []struct {
		name   string
		secret string
	}{
		{"the key's own text", key},
		{"the hash out of the key's payload", claims.RenewalSecretHash},
		{"the key's payload", string(v1Payload(t, key))},
	} {
		rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: tc.secret})
		if rec.Code != http.StatusForbidden {
			t.Errorf("a caller holding %s was answered %d, want %d: the key is readable by every reader of the runs bucket, and the hash in it must not renew (D2135 as amended)", tc.name, rec.Code, http.StatusForbidden)
		}
	}
	// The secret itself does renew, so what was refused above was the key and
	// not the route.
	rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("the holder was answered %d, want the next key", rec.Code)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one renewal: a refusal must reach no signer", got)
	}
}

// TestRenewalRefusesAWrongSecret: the digest decides and nothing else does. A
// wrong secret is refused with the uniform answer, no key is minted, and the
// key's own state is left as it stands — a wrong guess must not spend or move
// anything, or a caller could end a holder's renewal by knocking.
func TestRenewalRefusesAWrongSecret(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	before, held, err := f.store.KeyState(context.Background(), keyID)
	if err != nil || !held {
		t.Fatalf("reading the key's state: held=%v err=%v", held, err)
	}

	for _, wrong := range []string{freshSecret(t), secret + "x", digest(secret)} {
		rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: wrong})
		if rec.Code != http.StatusForbidden {
			t.Errorf("a wrong secret was answered %d, want %d", rec.Code, http.StatusForbidden)
		}
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) by wrong secrets, want none", got)
	}
	after, held, err := f.store.KeyState(context.Background(), keyID)
	if err != nil || !held {
		t.Fatalf("reading the key's state back: held=%v err=%v", held, err)
	}
	if after.RenewalSecretHash != before.RenewalSecretHash || !after.LastSeen.Equal(before.LastSeen) {
		t.Errorf("a refused renewal changed the key's state to %+v from %+v, want it untouched", after, before)
	}
}

// TestARenewalMustNameTheWorkspace: §7 has the job send the workspace it is in,
// and the successor has to bind to one — the verifier refuses a key without it
// (TN2), so a renewal that named none is answered with the uniform refusal and
// nothing is minted. The status route is the contrast: it mints nothing and
// reads no workspace, so the scheduled check answers for a key whatever the
// project's own config says.
func TestARenewalMustNameTheWorkspace(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	for _, blank := range []string{"", "   "} {
		rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: blank, RenewalSecret: secret})
		if rec.Code != http.StatusForbidden {
			t.Errorf("a renewal naming %q as its workspace was answered %d, want %d: a successor with no workspace is one every verifier refuses", blank, rec.Code, http.StatusForbidden)
		}
		if body := rec.Body.String(); body != renewalRefusal+"\n" {
			t.Errorf("a renewal naming %q was answered %q, want the one refusal %q", blank, body, renewalRefusal)
		}
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) by renewals that named no workspace, want none", got)
	}
	if rec := f.call(RouteStatus, RenewalCall{KeyID: keyID, RenewalSecret: secret}); rec.Code != http.StatusOK {
		t.Errorf("the status call was answered %d with no workspace named, want %d: a check answers for the key and not for the project's config", rec.Code, http.StatusOK)
	}
}

// TestARenewalStaysInTheKeysOwnWorkspace: D2134 as amended by TN2 — a key is
// tied to one workspace and moving it is a reissue, so a renewal mints the
// successor into the workspace the key is bound to and not into the one the
// request names. Without that, whoever held a key and its secret could bind the
// next term to a workspace of their choosing, and the claim the verifier checks
// (K4.1) would be the thief's word rather than the licence's.
func TestARenewalStaysInTheKeysOwnWorkspace(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	elsewhere := RenewalCall{KeyID: keyID, WorkspaceID: "someone-elses-project", RenewalSecret: secret}

	if rec := f.call(RouteRenew, elsewhere); rec.Code != http.StatusForbidden {
		t.Errorf("a renewal naming another workspace was answered %d, want %d: a key is tied to one workspace and moving it is a reissue", rec.Code, http.StatusForbidden)
	} else if body := rec.Body.String(); body != workspaceRefusal+"\n" {
		t.Errorf("a renewal naming another workspace was answered %q, want %q", body, workspaceRefusal)
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) by a renewal bound elsewhere, want none", got)
	}

	mine := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}
	answer := answerOf(t, f.call(RouteRenew, mine))
	if answer.Key == "" {
		t.Fatal("the holder's own renewal answered no key")
	}
	claims := v1Verified(t, f, answer.Key)
	if claims.WorkspaceID != workspaceID {
		t.Errorf("the successor is bound to %q, want the key's own workspace %q", claims.WorkspaceID, workspaceID)
	}
	state, held, err := f.store.KeyState(context.Background(), claims.KeyID)
	if err != nil || !held {
		t.Fatalf("reading the successor's state back: held=%v err=%v", held, err)
	}
	if state.WorkspaceID != workspaceID {
		t.Errorf("the successor's state names %q, want %q: a chain that lost its workspace could never renew again", state.WorkspaceID, workspaceID)
	}

	// A key whose state names no workspace is an issue path's bug and not the
	// caller's: it is a failure rather than a refusal, and one that mints.
	orphan, orphanSecret, _ := heldKey(t, f, PolicyTeamAnnual)
	withoutAWorkspace(t, f, orphan)
	rec := f.call(RouteRenew, RenewalCall{KeyID: orphan, WorkspaceID: workspaceID, RenewalSecret: orphanSecret})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a key bound to no workspace was answered %d, want %d: nothing here can mint into a workspace it does not know", rec.Code, http.StatusInternalServerError)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the holder's one renewal: a key with no workspace mints nothing", got)
	}
}

// TestRevokedKeyIsNeverRenewed: §8 — the revocation list is read before
// anything is minted, and a key the licensor has withdrawn is never handed a
// successor. The holder is told `revoked` (its own secret proved it is the
// holder) and is given no key, and no signer is reached.
func TestRevokedKeyIsNeverRenewed(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	// Inside its window, so what turns the renewal away below is the revocation
	// and not the timing: a key a whole term from its window is refused before
	// the list is ever read.
	f.inTheRenewalWindow(t, keyID)
	if err := f.store.Revoke(context.Background(), Revocation{KeyID: keyID, Reason: "refund", At: f.now}); err != nil {
		t.Fatalf("revoking the key: %v", err)
	}
	call := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}

	rec := f.call(RouteRenew, call)
	if rec.Code != http.StatusOK {
		t.Fatalf("the revoked key's renewal was answered %d, want the holder's own answer", rec.Code)
	}
	answer := answerOf(t, rec)
	if answer.Status != StatusRevoked {
		t.Errorf("the revoked key's renewal answered %q, want %q", answer.Status, StatusRevoked)
	}
	if answer.Key != "" || answer.RenewalSecret != "" {
		t.Error("a revoked key was handed a key and a secret, want neither: a key the licensor has withdrawn never renews (§8)")
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) for a revoked key, want none", got)
	}

	if got := answerOf(t, f.call(RouteStatus, call)).Status; got != StatusRevoked {
		t.Errorf("the status endpoint answered %q for a revoked key, want %q", got, StatusRevoked)
	}
	// Another holder's secret is not the revocation's business: a stranger
	// asking about a revoked key is answered like a stranger asking about any
	// other key.
	if rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: freshSecret(t)}); rec.Code != http.StatusForbidden {
		t.Errorf("a wrong secret for a revoked key was answered %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// mailedSecret reads the renewal secret the purchase mailed below the key text,
// which is where the delivery carries it (D2135). A test that renews a purchased
// key has to take the secret from there rather than from the minter: the merchant
// path mints through the fixture's recording minter, and what the customer holds
// — the mail — is the only copy of it a test can read.
func mailedSecret(t *testing.T, f *fixture, key string) string {
	t.Helper()
	for _, sent := range f.mail.sent {
		lines := strings.Split(sent.Body, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == key && i+1 < len(lines) {
				return strings.TrimSpace(lines[i+1])
			}
		}
	}
	t.Fatalf("no mailed message carries the key %q, so the renewal secret it delivered is nowhere", key)
	return ""
}

// renewThroughTheEndpoint renews one key the way the customer's job does and
// answers the successor's record and the secret it was mailed with: a renewal is
// a new key id and a new secret (D2135), and a test that walks a chain needs both.
func renewThroughTheEndpoint(t *testing.T, f *fixture, keyID, secret string) (Record, string) {
	t.Helper()
	f.inTheRenewalWindow(t, keyID)
	rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("the renewal of %s was answered %d, want the holder's own answer", keyID, rec.Code)
	}
	answer := answerOf(t, rec)
	if answer.Key == "" {
		t.Fatalf("the renewal of %s answered no key", keyID)
	}
	for _, record := range f.records() {
		if record.Source == rotationSource(keyID) {
			return record, answer.RenewalSecret
		}
	}
	t.Fatalf("the store holds no rotation of the key %s, so the answer names a key nothing recorded", keyID)
	return Record{}, ""
}

// theMerchantRenews runs the merchant's own renewal of the licence a key belongs
// to, the way the merchant's trigger does when the customer's money buys the next
// term (merchant.Renew, Service.Renew): it advances the clock to where the
// trigger's own window opens and fails when nothing was minted. It is the only
// thing that moves a subscription's end, and so the only thing that buys the term
// `/renew` later hands out
// (TestARenewalMintsOnlyIntoATermTheSubscriptionCovers).
func theMerchantRenews(t *testing.T, f *fixture, key Record) {
	t.Helper()
	f.inTheMerchantRenewalWindow(t, key)
	if _, renewed, err := f.svc.Renew(context.Background(), key.Subscription); err != nil || !renewed {
		t.Fatalf("the merchant's renewal of the subscription %s minted nothing (renewed=%v err=%v)", key.Subscription, renewed, err)
	}
}

// inTheMerchantRenewalWindow moves the clock to where the merchant's own renewal
// is asked for and answers that instant: a day inside the lead the trigger opens
// its window by before the *subscription's* own end — T26's lead, bounded by half
// the term for a row sold for less than it, which is the rule merchant.Renew
// reads for itself.
//
// The subscription's window is not the key's, and D2202 is what separates them: a
// connected row's key runs thirty days while the subscription runs the period the
// customer bought, so the trigger of an annual licence fires some eleven months
// after the key in hand was minted and never inside that key's own last week. A
// test that ran the trigger in the key's window instead would be asking it for a
// term the customer has not been billed for, and `Renew` answers that with
// nothing minted.
func (f *fixture) inTheMerchantRenewalWindow(t *testing.T, key Record) time.Time {
	t.Helper()
	sub, held, err := f.store.Subscription(context.Background(), key.Subscription)
	if err != nil || !held {
		t.Fatalf("the store holds no subscription %q for the key %s (held=%v err=%v)", key.Subscription, key.KeyID, held, err)
	}
	lead := RenewalLead
	if half := time.Duration(sub.TermDays) * 12 * time.Hour; sub.TermDays > 0 && half < lead {
		lead = half
	}
	if asked := sub.Expires.Add(-lead).Add(24 * time.Hour); asked.After(f.now) {
		f.advance(asked.Sub(f.now))
	}
	return f.now
}

// theSubscriptionCovers writes the subscription a key belongs to as paid through
// the term its successor will cover, which is the state the merchant's own
// renewal leaves behind when the money has bought that term (theMerchantRenews).
// A test that walks a chain of rotations without a merchant event needs it,
// because `/renew` hands out only a term the subscription's end already reaches
// and never moves that end itself
// (TestARenewalMintsOnlyIntoATermTheSubscriptionCovers).
func theSubscriptionCovers(t *testing.T, f *fixture, keyID string) {
	t.Helper()
	record := f.issuance(t, keyID)
	policy, known := DefaultPolicy(record.Plan)
	if !known {
		t.Fatalf("the key was minted from the row %q, which is not in the policy table", record.Plan)
	}
	sub, held, err := f.store.Subscription(context.Background(), record.Subscription)
	if err != nil || !held {
		t.Fatalf("the store holds no subscription %q for the key %s (held=%v err=%v)", record.Subscription, keyID, held, err)
	}
	sub.Expires = covered(record, policy, f.now)
	if err := f.store.SaveSubscription(context.Background(), sub); err != nil {
		t.Fatalf("writing the subscription %q back: %v", sub.ID, err)
	}
}

// TestAPurchasedKeyRenewsAndItsSuccessorInheritsItsWorkspaceAndSubscription:
// the pair that left every purchased key's renewal answering 500. The state a
// purchase writes names the workspace the checkout bound — a state without one
// is a key no renewal can serve (K2.3) — and the record names the subscription
// it was sold for, which the successor inherits, because a successor whose
// record named none could never renew again.
func TestAPurchasedKeyRenewsAndItsSuccessorInheritsItsWorkspaceAndSubscription(t *testing.T) {
	f, _ := renewing(t)
	bought := f.purchase("evt-renew-1", "acme.example", 5, 365)
	if got := f.mintedKeys(); len(got) != 1 {
		t.Fatalf("the purchase minted %d key(s), want one", len(got))
	}
	if bought.Subscription == "" {
		t.Fatal("the purchased key's record names no subscription, want the one the event carried")
	}
	state, held, err := f.store.KeyState(context.Background(), bought.KeyID)
	if err != nil || !held {
		t.Fatalf("reading the purchased key's state back: held=%v err=%v", held, err)
	}
	if state.WorkspaceID != workspaceID {
		t.Fatalf("the purchased key's state names the workspace %q, want %q: a state without one is a key no renewal can serve", state.WorkspaceID, workspaceID)
	}

	// What the successor will cover is one the customer has bought, and what says
	// so is the merchant's own renewal of the subscription: the trigger runs inside
	// its own window, mints the key for the term it bills and moves the
	// subscription's end to it (merchant.Renew). The endpoint below hands that term
	// out and reads the end rather than moving it, so a purchase the merchant has
	// not renewed is a licence with no term to hand out
	// (TestARenewalMintsOnlyIntoATermTheSubscriptionCovers).
	secret := mailedSecret(t, f, f.mintedKeys()[0])
	theMerchantRenews(t, f, bought)
	paid, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back: held=%v err=%v", held, err)
	}

	successor, successorSecret := renewThroughTheEndpoint(t, f, bought.KeyID, secret)
	if successor.Subscription != bought.Subscription {
		t.Errorf("the successor's record names the subscription %q, want %q: a successor that inherited none could never renew again", successor.Subscription, bought.Subscription)
	}
	next, held, err := f.store.KeyState(context.Background(), successor.KeyID)
	if err != nil || !held {
		t.Fatalf("reading the successor's state back: held=%v err=%v", held, err)
	}
	if next.WorkspaceID != workspaceID {
		t.Errorf("the successor's state names the workspace %q, want %q: a renewal keeps the key in its own workspace", next.WorkspaceID, workspaceID)
	}
	// The successor covers the term the merchant's renewal paid for, and the
	// rotation left the subscription's own end where the merchant did. A rotation
	// that wrote it would be the endpoint crediting a term, which is what its gate
	// exists to refuse (D2148).
	//
	// Under D2202 those are two different instants: what the merchant's renewal
	// bought is the entitlement's term — a year past the subscription's end it
	// moved — and what the holder is handed out of it is the thirty days a
	// connected key runs from the day it was asked for. What the renewal must
	// never do is reach past the money, and that is the mint's own cap rather than
	// a coincidence of the row's period.
	sub, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back: held=%v err=%v", held, err)
	}
	if run := successor.Expires.Sub(f.now); run != connectedTerm {
		t.Errorf("the successor covers %s from the renewal, want D2202's thirty days: the merchant's renewal buys the entitlement's term and this endpoint hands out a key of it", run)
	}
	if successor.Expires.After(sub.Expires) {
		t.Errorf("the successor covers to %s and the subscription ends at %s, want the successor inside what was paid for", successor.Expires, sub.Expires)
	}
	if !sub.Expires.Equal(paid.Expires) {
		t.Errorf("the rotation moved the subscription's end from %s, where the merchant's own renewal left it, to %s, want it left alone: this route hands out a term the customer has paid for and never credits one", paid.Expires, sub.Expires)
	}
	if sub.Cancelled {
		t.Error("the renewal cancelled the subscription, want it as it was: a renewal is not an event on the merchant's timeline")
	}

	// And the chain keeps going, which is the whole point of inheriting both: the
	// merchant's next renewal credits the following term, and the successor's own
	// window is where the holder is handed the key for it.
	theMerchantRenews(t, f, successor)
	second, secondSecret := renewThroughTheEndpoint(t, f, successor.KeyID, successorSecret)
	if second.Subscription != bought.Subscription {
		t.Errorf("the second successor names the subscription %q, want %q: a chain that lost it stops at one term", second.Subscription, bought.Subscription)
	}
	if secondSecret == successorSecret {
		t.Error("a rotation answered the same secret it replaced, want a new one: the old secret is what a leak costs the holder")
	}
}

// TestARotationLeavesTheSubscriptionsEndWhereTheMerchantLeftIt: the endpoint
// does not write a subscription's end at all, in either direction. Its gate
// reads that end as the merchant's own record of what the customer has paid for
// (D2148), and a route that wrote it would be answering a question it had just
// asked itself: writing it forward is a term credited to a caller whose money
// never followed, which the gate's own test pins, and writing it back is the
// store's record of the paid terms truncated. The key a holder rotates is not
// always the one the merchant's last renewal minted — the job may be installed
// for a key the subscription has already moved past, and that key's own record
// ends where its own term did — so a rotation that stamped its successor's end
// would open the merchant's renewal window early and refuse a renewal of that
// successor for a term the customer has paid for.
func TestARotationLeavesTheSubscriptionsEndWhereTheMerchantLeftIt(t *testing.T) {
	f, _ := renewing(t)
	bought := f.purchase("evt-1", "acme.example", 5, 30)
	// The merchant renews the licence twice, so the subscription's record of the
	// paid term is two terms past the key the customer still holds.
	f.advance(16 * day)
	if _, renewed, err := f.svc.Renew(context.Background(), bought.Subscription); err != nil || !renewed {
		t.Fatalf("the merchant's first renewal minted nothing (renewed=%v, err=%v)", renewed, err)
	}
	f.advance(30 * day)
	if _, renewed, err := f.svc.Renew(context.Background(), bought.Subscription); err != nil || !renewed {
		t.Fatalf("the merchant's second renewal minted nothing (renewed=%v, err=%v)", renewed, err)
	}
	paid, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back: held=%v err=%v", held, err)
	}
	if !paid.Expires.After(bought.Expires) {
		t.Fatalf("the subscription ends at %s and the purchase's key at %s, want the subscription ahead of it: the case this pins is a key the subscription has moved past", paid.Expires, bought.Expires)
	}

	successor, _ := renewThroughTheEndpoint(t, f, bought.KeyID, mailedSecret(t, f, f.mintedKeys()[0]))
	if !successor.Expires.Before(paid.Expires) {
		t.Fatalf("the successor covers to %s and the subscription ends at %s, want the successor behind it: otherwise this key's term would be the subscription's own end", successor.Expires, paid.Expires)
	}
	after, _, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil {
		t.Fatalf("reading the subscription back after the rotation: %v", err)
	}
	if !after.Expires.Equal(paid.Expires) {
		t.Errorf("the rotation of %s moved the subscription's end from %s to %s, and the successor it minted covers only to %s: the terms the merchant has been paid for are not this key's to give back",
			bought.KeyID, paid.Expires, after.Expires, successor.Expires)
	}
}

// TestACancelledSubscriptionsKeyIsRefused: a key whose subscription the merchant
// has cancelled is handed no successor, because the term after this one is one
// nobody is paying for. The key itself is left alone and its holder is told its
// own state (§5.3: a cancellation stops the reissue, the key the customer holds
// runs to its end), and no signer is reached.
func TestACancelledSubscriptionsKeyIsRefused(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-cancel-1", "acme.example", 5, 365)
	secret := mailedSecret(t, f, f.mintedKeys()[0])
	f.inTheRenewalWindow(t, bought.KeyID)
	f.deliver(Event{
		ID: "evt-cancel-2", Type: EventCancellation, Mode: ModeFor(f.env), OccurredAt: f.now,
		Subscription: bought.Subscription, Organisation: "acme.example",
	})
	sub, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held || !sub.Cancelled {
		t.Fatalf("the cancellation did not reach the subscription (held=%v cancelled=%v err=%v)", held, sub.Cancelled, err)
	}

	rec := f.call(RouteRenew, RenewalCall{KeyID: bought.KeyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("a cancelled subscription's key was answered %d, want the holder's own answer", rec.Code)
	}
	answer := answerOf(t, rec)
	if answer.Key != "" || answer.RenewalSecret != "" {
		t.Error("a cancelled subscription's key was handed a successor, want none: the term after this one is unpaid")
	}
	if answer.Status != StatusOK {
		t.Errorf("the answer reads %q, want %q: the key the customer holds runs to its end (§5.3)", answer.Status, StatusOK)
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) for a cancelled subscription, want none", got)
	}
	if _, held, _ := f.store.Revocation(context.Background(), bought.KeyID); held {
		t.Error("the renewal revoked the key, want it left alone: withdrawing is §8's and it is the refund's and the chargeback's (D2131)")
	}
}

// TestARenewedKeysSuccessorIsRefusedOnceTheSubscriptionIsCancelled: the same
// guard, reached the way a paying customer reaches it — a renewal while the
// subscription runs, and the cancellation after it. The successor the rotation
// minted is a holder in its own right, and it is the key such a customer's job
// holds when the merchant's cancellation lands: the guard has to be read for it
// too, or a cancellation that arrives between two renewals is answered with
// another term, and the month after that with another (§5.3).
//
// What the successor is answered is its own state and no key, and it is left
// where it is: the key runs to its end, and the job's next call is what tells the
// installation the term after it is not coming.
func TestARenewedKeysSuccessorIsRefusedOnceTheSubscriptionIsCancelled(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-cancel-renew-1", "acme.example", 5, 30)
	theMerchantRenews(t, f, bought)
	successor, successorSecret := renewThroughTheEndpoint(t, f, bought.KeyID, mailedSecret(t, f, f.mintedKeys()[0]))
	if successor.Subscription != bought.Subscription {
		t.Fatalf("the successor names the subscription %q, want the purchase's %q: the key this test cancels under is the one a holder's job carries", successor.Subscription, bought.Subscription)
	}

	f.deliver(Event{
		ID: "evt-cancel-renew-2", Type: EventCancellation, Mode: ModeFor(f.env), OccurredAt: f.now,
		Subscription: bought.Subscription, Organisation: "acme.example",
	})
	sub, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held || !sub.Cancelled {
		t.Fatalf("the cancellation did not reach the subscription (held=%v cancelled=%v err=%v)", held, sub.Cancelled, err)
	}

	// The successor's own window is opened first, so what refuses this call is the
	// cancellation and not the calendar — and the window would otherwise be far
	// out: the successor was minted a month before its own last week.
	f.inTheRenewalWindow(t, successor.KeyID)
	rec := f.call(RouteRenew, RenewalCall{KeyID: successor.KeyID, WorkspaceID: workspaceID, RenewalSecret: successorSecret})
	if rec.Code != http.StatusOK {
		t.Fatalf("the successor of a cancelled subscription was answered %d, want the holder's own answer", rec.Code)
	}
	answer := answerOf(t, rec)
	if answer.Key != "" || answer.RenewalSecret != "" || answer.NewerKey {
		t.Errorf("the successor of a cancelled subscription was answered %+v, want its own state and no key: the term after it is one nobody is paying for", answer)
	}
	if answer.Status != StatusOK {
		t.Errorf("the answer reads %q, want %q: the key the customer holds runs to its end (§5.3)", answer.Status, StatusOK)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one the renewal earned before the cancellation and nothing after it", got)
	}
	if _, held, _ := f.store.Revocation(context.Background(), successor.KeyID); held {
		t.Error("the refused renewal revoked the successor, want it left alone: withdrawing is §8's and it is the refund's and the chargeback's (D2131)")
	}
}

// TestAnExpiredSubscriptionsKeyIsRefused: the other half of the same rule — a
// subscription whose end has passed renews nothing, whatever the key's own
// window says. A key past its `paid_through` is inside its window trivially, so
// without this gate it is exactly the key that mints a term nobody paid for: the
// customer's subscription lapsed and the key they still hold walks on.
//
// The lapse is the subscription's own and not the key's, and D2202 is why they
// are two dates: the year the customer bought is where the money ends, while the
// key the purchase mailed ran thirty days of it, so a clock past the key's end is
// a clock months inside the term that was paid for and a renewal there is the one
// the licence is owed (TestAPurchasedKeyRenewsThroughTheRenewEndpoint).
func TestAnExpiredSubscriptionsKeyIsRefused(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-lapsed-1", "acme.example", 5, 365)
	secret := mailedSecret(t, f, f.mintedKeys()[0])
	lapsed, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back: held=%v err=%v", held, err)
	}
	// Past the end of the term the purchase paid for, which is past the end of the
	// subscription it was sold under — and long past the end of the key in hand,
	// which is the holder whose job lapsed and still asks.
	f.advance(lapsed.Expires.Sub(f.now) + time.Hour)
	if !f.now.After(bought.Expires) {
		t.Fatalf("the clock reads %s and the key ends at %s, want the key past its end", f.now, bought.Expires)
	}
	if !f.now.After(lapsed.Expires) {
		t.Fatalf("the clock reads %s and the subscription ends at %s, want the subscription past its end", f.now, lapsed.Expires)
	}

	rec := f.call(RouteRenew, RenewalCall{KeyID: bought.KeyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("an expired subscription's key was answered %d, want the holder's own answer", rec.Code)
	}
	answer := answerOf(t, rec)
	if answer.Key != "" || answer.RenewalSecret != "" {
		t.Error("an expired subscription's key was handed a successor, want none: the term it would sign is unpaid")
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) for an expired subscription, want none", got)
	}
}

// TestARenewalMintsOnlyIntoATermTheSubscriptionCovers: the other half of the
// money gate, and the half that keeps the keys and the money together. What says
// the customer has bought the time after the key in hand is the subscription's
// own end being past it: the merchant's trigger is what moves that end
// (merchant.Renew) and this route reads it. A purchase the merchant has not
// renewed is a subscription ending where its key ends — the monthly row's own
// state, and the one term that was bought — so there is no term beyond it to hand
// out and the call is answered the key's own state, however often it is asked.
//
// Without the gate the route credits itself the term it is about to mint: every
// call is answered a fresh key id and a fresh secret, so a caller that paid once
// walks a chain of keys a term at a time forever, each key a bucket of its own
// for the throttle, while the subscription's end follows it and the merchant's
// own renewal — the money the term is for — finds its window already moved past.
func TestARenewalMintsOnlyIntoATermTheSubscriptionCovers(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-term-1", "acme.example", 5, 30)
	secret := mailedSecret(t, f, f.mintedKeys()[0])
	paid, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back: held=%v err=%v", held, err)
	}
	if !paid.Expires.Equal(bought.Expires) {
		t.Fatalf("the subscription ends at %s and the purchase's key at %s, want the same instant: one term bought, and no merchant renewal behind it", paid.Expires, bought.Expires)
	}

	f.inTheRenewalWindow(t, bought.KeyID)
	call := RenewalCall{KeyID: bought.KeyID, WorkspaceID: workspaceID, RenewalSecret: secret}
	for _, ask := range []string{"the first", "a second"} {
		answer := answerOf(t, f.call(RouteRenew, call))
		if answer.Key != "" || answer.RenewalSecret != "" || answer.NewerKey {
			t.Errorf("%s call inside the window was answered %+v, want the key's own state and no key: the term after this key is one nobody has paid for", ask, answer)
		}
		if answer.Status != StatusOK {
			t.Errorf("%s call's answer reads %q, want %q: the caller proved its secret and the key it holds runs to its end (§5.3)", ask, answer.Status, StatusOK)
		}
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) against a subscription paid through one term, want none", got)
	}
	after, _, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil {
		t.Fatalf("reading the subscription back after the calls: %v", err)
	}
	if !after.Expires.Equal(paid.Expires) {
		t.Errorf("the refused renewals moved the subscription's end from %s to %s, want it left where the customer's money put it", paid.Expires, after.Expires)
	}

	// The merchant's renewal is the money arriving for the next term, and it is
	// what buys the successor: the same call then hands it over, because what was
	// missing was the paid term and never the key.
	theMerchantRenews(t, f, bought)
	answer := answerOf(t, f.call(RouteRenew, call))
	if answer.Key == "" {
		t.Fatalf("the renewal was refused after the subscription was paid through the next term: %+v", answer)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s) once the term was paid for, want the one successor", got)
	}

	// What the successor's own renewal buys is the days the subscription has left
	// and never a term of its own: the merchant paid for one term, and this route
	// hands that term out across however many keys the holder walks through on the
	// way to its end. So the successor ends where the chain's own thirty days put
	// it, the key it is handed ends where the subscription does — the cap on the
	// mint and not a coincidence of the calendar — and that last key buys nothing
	// at all. Every window is opened before its call, so what refuses the last one
	// is the money and not the timing.
	moved, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back after the merchant's renewal: held=%v err=%v", held, err)
	}
	successor := v1Verified(t, f, answer.Key)
	f.inTheRenewalWindow(t, successor.KeyID)
	next := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: successor.KeyID, WorkspaceID: workspaceID, RenewalSecret: answer.RenewalSecret}))
	if next.Key == "" {
		t.Fatalf("the successor was refused with days of the paid term still ahead of it: %+v", next)
	}
	last := v1Verified(t, f, next.Key)
	if !last.PaidThrough.Equal(moved.Expires) {
		t.Errorf("the successor's own renewal ends at %s, want the subscription's own end (%s): a renewal covers the days that are left of the term, not a period of its own (D2202)", last.PaidThrough, moved.Expires)
	}
	f.inTheRenewalWindow(t, last.KeyID)
	refused := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: last.KeyID, WorkspaceID: workspaceID, RenewalSecret: next.RenewalSecret}))
	if refused.Key != "" || refused.NewerKey {
		t.Errorf("a key ending where the subscription ends was answered %+v, want its own state and no key: the term after this one is one nobody has paid for", refused)
	}
	if got := spy.minted(); got != 2 {
		t.Errorf("the minter was asked for %d key(s) in all, want the two the paid term covers and nothing after it", got)
	}
}

// TestAMerchantRenewalsKeyRenewsOffTheTermGrid: the key the merchant's own
// renewal mints is the one a paying customer's installation actually holds, and
// D2202 leaves it off every grid the row draws — it runs the thirty days a
// connected key runs from the day the trigger fired, while the subscription's own
// end has moved a whole period on from the term that renewal billed. A gate that
// asked the subscription for a term measured out from this key's own end refuses
// a key whose renewal the customer has paid for, and the endpoint's answer is the
// successor that carries the licence to the end the money reaches.
func TestAMerchantRenewalsKeyRenewsOffTheTermGrid(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-grid-1", "acme.example", 5, 30)
	theMerchantRenews(t, f, bought)
	if got := len(f.mintedKeys()); got != 2 {
		t.Fatalf("the purchase and the merchant's renewal minted %d key(s), want the merchant's key beside the purchase's", got)
	}
	keyID := v1Verified(t, f, f.mintedKeys()[1]).KeyID
	secret := mailedSecret(t, f, f.mintedKeys()[1])
	record := f.issuance(t, keyID)
	sub, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back: held=%v err=%v", held, err)
	}
	policy, known := DefaultPolicy(record.Plan)
	if !known {
		t.Fatalf("the key was minted from the row %q, which is not in the policy table", record.Plan)
	}
	// The case this pins: the row's own period measured from this key's end reaches
	// past the subscription's, so nothing the grid says would hand this key a term
	// — while the money says there is one, to the subscription's own end.
	if !record.Expires.Add(policy.Term).After(sub.Expires) {
		t.Fatalf("a %s term past the key's end %s is %s, inside the subscription's own end %s, want it past: this is the off-grid key the gate is read for", policy.Term, record.Expires, record.Expires.Add(policy.Term), sub.Expires)
	}

	f.inTheRenewalWindow(t, keyID)
	answer := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
	if answer.Key == "" {
		t.Fatalf("the merchant's own key was answered %+v, want a successor: the money reaches past its end and this key is the one the holder runs", answer)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one successor", got)
	}
	claims := v1Verified(t, f, answer.Key)
	if !claims.PaidThrough.After(record.Expires) {
		t.Errorf("the successor ends at %s, want past the end of the key it replaces (%s)", claims.PaidThrough, record.Expires)
	}
	if claims.PaidThrough.After(sub.Expires) {
		t.Errorf("the successor ends at %s, past the subscription's own end %s: what a renewal covers is the money and never a period of its own", claims.PaidThrough, sub.Expires)
	}
}

// TestASeatChangesKeyRenewsWhileTheTermRuns: a seat change reissues inside the
// term the customer has already paid for (§5.3), so the key it mints runs its
// thirty days from the change and the subscription's end stays where the purchase
// put it — a year on, with the change nowhere near it. This is the second off-grid
// key the gate is read for, and the one an installation is most likely to hold: a
// seat change is a reissue of the key the customer runs, not a second licence.
func TestASeatChangesKeyRenewsWhileTheTermRuns(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-seats-1", "acme.example", 5, 365)
	f.advance(100 * day)
	before := len(f.records())
	f.event(Event{ID: "evt-seats-2", Type: EventSeatChange, Organisation: "acme.example", Seats: 8, TermDays: 365})
	records := f.records()
	if len(records) != before+1 {
		t.Fatalf("the seat change minted %d key(s), want one", len(records)-before)
	}
	reissued := records[len(records)-1]
	if reissued.Subscription != bought.Subscription {
		t.Fatalf("the reissued key names the subscription %q, want the purchase's %q: a seat change reissues the same licence", reissued.Subscription, bought.Subscription)
	}
	key, secret := mailedBundle(t, f.mail.sent[len(f.mail.sent)-1].Body)
	if got := v1Verified(t, f, key).KeyID; got != reissued.KeyID {
		t.Fatalf("the mail carries the key %s, want the seat change's own %s", got, reissued.KeyID)
	}
	sub, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back: held=%v err=%v", held, err)
	}
	policy, known := DefaultPolicy(reissued.Plan)
	if !known {
		t.Fatalf("the key was minted from the row %q, which is not in the policy table", reissued.Plan)
	}
	if !reissued.Expires.Add(policy.Term).After(sub.Expires) {
		t.Fatalf("a %s term past the reissued key's end %s is %s, inside the subscription's own end %s, want it past: a seat change inside a year lands its key months short of it", policy.Term, reissued.Expires, reissued.Expires.Add(policy.Term), sub.Expires)
	}

	f.inTheRenewalWindow(t, reissued.KeyID)
	answer := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: reissued.KeyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
	if answer.Key == "" {
		t.Fatalf("the seat change's key was answered %+v, want a successor: the renewal the customer paid for is the one that keeps their installation running", answer)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one successor", got)
	}
	claims := v1Verified(t, f, answer.Key)
	if !claims.PaidThrough.After(reissued.Expires) {
		t.Errorf("the successor ends at %s, want past the end of the key it replaces (%s)", claims.PaidThrough, reissued.Expires)
	}
	if claims.PaidThrough.After(sub.Expires) {
		t.Errorf("the successor ends at %s, past the subscription's own end %s: the change moved the seats and never the term (§5.3)", claims.PaidThrough, sub.Expires)
	}
	if claims.Seats != 8 {
		t.Errorf("the successor carries %d seat(s), want the 8 the seat change reissued for", claims.Seats)
	}
}

// TestAMonthBilledInTwentyEightDaysRenewsToTheSubscriptionsEnd: the merchant
// bills a calendar month, so a subscription's own period can be the twenty-eight
// days February makes while the key minted under it runs the thirty D2202 gives a
// connected row — here the key the merchant's own renewal mints, thirty days from
// the trigger, against a subscription moved on by twenty-eight. The month's two
// ends are off one grid, and what the endpoint hands out is the days between
// them: the successor that reaches the subscription's own end stops there rather
// than running a period of its own.
func TestAMonthBilledInTwentyEightDaysRenewsToTheSubscriptionsEnd(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-feb-1", "acme.example", 5, 28)
	sub, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back: held=%v err=%v", held, err)
	}
	if run := sub.Expires.Sub(bought.Issued); run != 28*day {
		t.Fatalf("the subscription runs %s, want the twenty-eight day calendar month the customer was billed for", run)
	}

	theMerchantRenews(t, f, bought)
	moved, held, err := f.store.Subscription(context.Background(), bought.Subscription)
	if err != nil || !held {
		t.Fatalf("reading the subscription back after the merchant's renewal: held=%v err=%v", held, err)
	}
	if got := len(f.mintedKeys()); got != 2 {
		t.Fatalf("the purchase and the merchant's renewal minted %d key(s), want the merchant's key beside the purchase's", got)
	}
	keyID := v1Verified(t, f, f.mintedKeys()[1]).KeyID
	secret := mailedSecret(t, f, f.mintedKeys()[1])
	record := f.issuance(t, keyID)
	if run := record.Expires.Sub(record.Issued); run != connectedTerm {
		t.Fatalf("the renewed key runs %s, want D2202's thirty days against the twenty-eight the customer is billed for", run)
	}
	policy, known := DefaultPolicy(record.Plan)
	if !known {
		t.Fatalf("the key was minted from the row %q, which is not in the policy table", record.Plan)
	}
	if !record.Expires.Add(policy.Term).After(moved.Expires) {
		t.Fatalf("a %s term past the renewed key's end %s is %s, inside the subscription's own end %s, want it past: the calendar's month and the row's key are different lengths", policy.Term, record.Expires, record.Expires.Add(policy.Term), moved.Expires)
	}

	f.inTheRenewalWindow(t, keyID)
	answer := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
	if answer.Key == "" {
		t.Fatalf("the renewed key of a calendar month was answered %+v, want a successor: the money reaches past its end and the holder runs on it", answer)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one successor", got)
	}
	if last := v1Verified(t, f, answer.Key).PaidThrough; !last.Equal(moved.Expires) {
		t.Errorf("the successor ends at %s, want the subscription's own end %s: a renewal covers the days that are left and never a period of its own", last, moved.Expires)
	}
}

// TestAKeyWithNoSubscriptionIsRefused: a key whose record names no subscription
// at all is one nothing here can prove was paid for — a key minted by hand, by
// the admin page or by the mint tool all look the same — so it does not renew,
// and the owner's ruling of 2026-11-13 says so in as many words: it fails
// closed. What it is answered is the uniform refusal and not its own state: the
// state would tell its holder that a renewal is one secret away, and the refusal
// is the one answer §7.1 gives a caller this service will not serve.
func TestAKeyWithNoSubscriptionIsRefused(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	if f.issuance(t, keyID).Subscription == "" {
		t.Fatal("the fixture's key carries no subscription to take away, so this test proves nothing")
	}
	withoutASubscription(t, f, keyID)
	if got := f.issuance(t, keyID).Subscription; got != "" {
		t.Fatalf("the key's record still names the subscription %q, want none", got)
	}

	rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a key with no subscription was answered %d, want %d: a key nothing can prove was paid for does not renew", rec.Code, http.StatusForbidden)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != renewalRefusal {
		t.Errorf("the refusal reads %q, want the uniform sentence %q: a caller is not told which of the ways in it failed", body, renewalRefusal)
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) for a key with no subscription, want none", got)
	}
	// The same key with its subscription back renews, so what refused it above is
	// the missing subscription and not something else about the fixture's key.
	setSubscription(t, f, keyID, subscriptionFor(keyID))
	if got := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})).Key; got == "" {
		t.Error("the same key renewed once its subscription was written back, want a refusal only while it names none")
	}
}

// TestStatusCarriesNoKey: F11 — the status endpoint answers the key's state and
// whether a successor exists, and nothing else. It carries no key and no secret
// in either answer, before or after a renewal: a reader of the job's check must
// not be handed a licence by it.
func TestStatusCarriesNoKey(t *testing.T) {
	f, _ := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)

	rec := f.call(RouteStatus, RenewalCall{KeyID: keyID, RenewalSecret: secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("the holder's status call was answered %d, want its key's state", rec.Code)
	}
	answer := answerOf(t, rec)
	if answer.Status != StatusOK || answer.NewerKey {
		t.Errorf("a fresh key's status is %+v, want ok and no successor yet", answer)
	}
	assertNoKeyIn(t, rec, secret)

	// After a renewal the same call is made with the old secret, which is what a
	// job whose key write lagged would send, and the status endpoint says a
	// successor exists without handing it over.
	renewed := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
	if renewed.Key == "" {
		t.Fatal("the renewal answered no key, so this test cannot tell what a status answer must not carry")
	}
	after := f.call(RouteStatus, RenewalCall{KeyID: keyID, RenewalSecret: secret})
	if !answerOf(t, after).NewerKey {
		t.Error("the status endpoint says there is no newer key after a rotation, want it true (F11)")
	}
	assertNoKeyIn(t, after, secret, renewed.Key, renewed.RenewalSecret)
}

// assertNoKeyIn fails when one of the two routes' answers carries key material:
// the secret it was sent and the bundle a renewal answered with.
func assertNoKeyIn(t *testing.T, rec *httptest.ResponseRecorder, material ...string) {
	t.Helper()
	body := rec.Body.String()
	for _, secret := range material {
		if secret == "" {
			continue
		}
		if strings.Contains(body, secret) {
			t.Errorf("the answer %q carries %q: a status answer carries no key and no secret (F11)", body, secret)
		}
	}
	if strings.Contains(body, keyPrefixV1) {
		t.Errorf("the answer %q carries a key text, and the status endpoint carries no key", body)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatalf("the answer %q is not JSON: %v", body, err)
	}
	for _, field := range []string{"key", "renewal_secret"} {
		if _, held := fields[field]; held {
			t.Errorf("the answer %q carries a %q field, want the state and nothing else", body, field)
		}
	}
}

// TestReplayedRenewalIsIdempotent: §7 — the job's request is redelivered by
// ordinary network failure, and a redelivery must not be a second key. The
// second call is answered with the same key and the same secret and the minter
// is not asked again: the first answer is the only key that rotation minted,
// and a second one would leave the holder with a key nobody was ever told
// about.
func TestReplayedRenewalIsIdempotent(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	call := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}

	first := answerOf(t, f.call(RouteRenew, call))
	second := answerOf(t, f.call(RouteRenew, call))
	if first.Key == "" || first.RenewalSecret == "" {
		t.Fatal("the renewal answered no bundle, so this test cannot read a replay")
	}
	if second.Key != first.Key || second.RenewalSecret != first.RenewalSecret {
		t.Errorf("the replayed renewal answered a different bundle, want the first one: a second mint is a key nobody was told about and a secret the holder never got")
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one renewal and not a second", got)
	}
	if got := answerOf(t, f.call(RouteStatus, call)).NewerKey; !got {
		t.Error("the status endpoint says there is no newer key after the rotation, want it true")
	}
}

// TestAStrangerCannotSpendTheHoldersAllowance: §7.2 — the key's own bucket is
// spent by a request that has proved the secret and not by one that has merely
// named the key, because a key id is not a secret: it is what a support ticket
// quotes and what a log carries, so a bucket charged before the proof would let
// anyone who has read one spend the holder's whole allowance with wrong-secret
// calls and have the holder's own renewal refused with the same uniform answer.
//
// The half that must not change is the other one: a request that did prove the
// secret does spend the key's allowance, and the address's bucket is spent by
// every request, which is what bounds guessing.
func TestAStrangerCannotSpendTheHoldersAllowance(t *testing.T) {
	const (
		stranger  = "198.51.100.9:5000"
		elsewhere = "198.51.100.10:5000"
		fresh     = "198.51.100.11:5000"
	)
	f, spy := renewingWithin(t, Limits{Requests: 3, Window: time.Hour})
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)

	for i := 0; i < 3; i++ {
		rec := f.callFrom(stranger, RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: freshSecret(t)})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("the stranger's call %d was answered %d, want %d", i+1, rec.Code, http.StatusForbidden)
		}
	}
	holder := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}
	holderRec := f.callFrom(applicantIP, RouteRenew, holder)
	if holderRec.Code != http.StatusOK {
		t.Fatalf("the holder's own renewal was answered %d (%q), want the next key: what a stranger spends is its own address's allowance", holderRec.Code, holderRec.Body.String())
	}
	if answer := answerOf(t, holderRec); answer.Key == "" {
		t.Error("the holder's own renewal answered no key")
	}
	if got := spy.minted(); got != 1 {
		t.Fatalf("the minter was asked for %d key(s), want the holder's one renewal", got)
	}

	// Two more calls that proved the secret fill the key's allowance, and the
	// next one is refused by the key's bucket alone: the address it came from is
	// one nothing has asked from.
	status := RenewalCall{KeyID: keyID, RenewalSecret: secret}
	for i := 0; i < 2; i++ {
		if rec := f.callFrom(elsewhere, RouteStatus, status); rec.Code != http.StatusOK {
			t.Fatalf("the holder's status call %d was answered %d, want %d", i+1, rec.Code, http.StatusOK)
		}
	}
	rec := f.callFrom(fresh, RouteStatus, status)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a call past the key's own allowance was answered %d, want %d: a request that proved the secret does spend it", rec.Code, http.StatusForbidden)
	}
	if body := rec.Body.String(); body != renewalRefusal+"\n" {
		t.Errorf("a call past the key's allowance was answered %q, want the one refusal %q: a throttled holder is answered like anyone else", body, renewalRefusal)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the holder's one renewal: a throttled call mints nothing", got)
	}
}

// TestTheOldSecretFetchesTheNextKeyForADay: D2135's window and its edge. A
// rotation makes a new secret, and the old one still fetches that same successor
// for 24 hours — the time a lost answer needs to be asked for again — and is
// refused after it. The successor is a holder in its own right: it renews with
// its own secret, under its own key id.
func TestTheOldSecretFetchesTheNextKeyForADay(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	old := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}
	first := answerOf(t, f.call(RouteRenew, old))
	if first.Key == "" || first.RenewalSecret == "" {
		t.Fatal("the renewal answered no bundle, so this test cannot read the window")
	}
	claims := v1Verified(t, f, first.Key)
	if claims.RenewalSecretHash != secretHash(first.RenewalSecret) {
		t.Errorf("the successor carries %q as its secret's hash, want its own new secret's digest: a rotation makes a new secret", claims.RenewalSecretHash)
	}
	if claims.RenewalSecretHash == secretHash(secret) {
		t.Error("the successor carries the old secret's hash, want the new one's: the old secret is spent")
	}
	successor := claims.KeyID

	f.advance(23 * time.Hour)
	again := answerOf(t, f.call(RouteRenew, old))
	if again.Key != first.Key || again.RenewalSecret != first.RenewalSecret {
		t.Error("the old secret inside the 24 hours answered a different bundle, want the successor the rotation minted")
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s) inside the window, want the one renewal", got)
	}

	f.advance(2 * time.Hour)
	if rec := f.call(RouteRenew, old); rec.Code != http.StatusForbidden {
		t.Errorf("the old secret was answered %d more than 24 hours after the rotation, want %d: the window is 24 hours and then it is refused", rec.Code, http.StatusForbidden)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s) after the window, want none: a spent secret mints nothing", got)
	}
	// The successor renews with its own secret, which is what makes the chain a
	// chain rather than a key that can be renewed once: a term later, when its
	// own window opens and the merchant's renewal has credited that term.
	theSubscriptionCovers(t, f, successor)
	f.inTheRenewalWindow(t, successor)
	next := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: successor, WorkspaceID: workspaceID, RenewalSecret: first.RenewalSecret}))
	if next.Key == "" {
		t.Fatal("the successor was not renewed, so the rotation left a key that can never renew again")
	}
	if got := spy.minted(); got != 2 {
		t.Errorf("the minter was asked for %d key(s), want the successor's own renewal beside the first", got)
	}
}

// TestTheNextKeyCoversTheTermAfterThisOne: T26 — a renewal taken inside the
// window takes no days from the customer, so the next key ends one period past
// this one's end rather than one period from now, and a row nothing renews is
// refused. What the request carries is this key's own claims and the workspace
// the caller named, which is what the job verifies before it writes the key
// (K4.1).
func TestTheNextKeyCoversTheTermAfterThisOne(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	policy, known := DefaultPolicy(PolicyTeamMonthly)
	if !known {
		t.Fatal("the monthly row is not in the table")
	}

	at := f.inTheRenewalWindow(t, keyID)
	answer := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
	if answer.Key == "" {
		t.Fatal("the renewal answered no key")
	}
	if len(spy.asked) != 1 {
		t.Fatalf("the minter was asked %d time(s), want one", len(spy.asked))
	}
	asked := spy.asked[0]
	if asked.Policy != policy.Name || asked.WorkspaceID != workspaceID || asked.Organisation != "acme.example" || asked.Seats != 3 || asked.Environment != f.env {
		t.Errorf("the minter was asked %+v, want the row the key was minted from, its organisation, its seats and the workspace the caller named", asked)
	}
	// Six days of the key's own term are left — a day inside the lead its row
	// names — so the successor runs for those six plus a whole period.
	left := f.issuance(t, keyID).Expires.Sub(at)
	if want := left + policy.Term; time.Duration(asked.TermDays)*24*time.Hour != want {
		t.Errorf("the successor was asked for %d day(s), want the %s left of this term plus the row's %s: a renewal inside the window costs the customer nothing", asked.TermDays, left, policy.Term)
	}
	claims := v1Verified(t, f, answer.Key)
	if !claims.PaidThrough.After(f.now) {
		t.Errorf("the successor ends at %s and the renewal was taken at %s, want a key that outlives the one it replaces", claims.PaidThrough, f.now)
	}
	if claims.Tier != licence.TierTeam || claims.Seats != 3 || claims.WorkspaceID != workspaceID {
		t.Errorf("the successor is %+v, want the same licence for a later term", claims)
	}

	// An air-gapped key is the row nothing renews: it contacts no server and
	// there is no successor for it (§7, D2138).
	gapped, gappedSecret, _ := heldKey(t, f, PolicyEnterpriseAirGapped)
	request := RenewalCall{KeyID: gapped, WorkspaceID: workspaceID, RenewalSecret: gappedSecret}
	if rec := f.call(RouteRenew, request); rec.Code != http.StatusForbidden {
		t.Errorf("an air-gapped key's renewal was answered %d, want %d: nothing renews that row", rec.Code, http.StatusForbidden)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one renewal: an air-gapped key is never handed a successor", got)
	}
}

// TestConcurrentRenewalsMintOneKey: renewals of one key arriving together are
// one rotation. Without that, two of them read "no successor yet" and both mint,
// and only one of the two can be the rotation the store records — the other key
// is one the store holds no record of, so nothing would let it renew, and the
// customer who installed it holds a paid key that is dead on arrival. Every
// caller is answered the one successor and the minter is asked once.
func TestConcurrentRenewalsMintOneKey(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	body, err := json.Marshal(RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if err != nil {
		t.Fatalf("encoding the request: %v", err)
	}

	const callers = 4
	answers := make([]*httptest.ResponseRecorder, callers)
	var wg sync.WaitGroup
	for i := range answers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteRenew, strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = applicantIP
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)
			answers[i] = rec
		}(i)
	}
	wg.Wait()

	first := answerOf(t, answers[0])
	if answers[0].Code != http.StatusOK || first.Key == "" {
		t.Fatalf("a renewal was answered %d (%q), want the successor", answers[0].Code, answers[0].Body.String())
	}
	for i, rec := range answers[1:] {
		if rec.Code != http.StatusOK {
			t.Fatalf("renewal %d of the same key was answered %d (%q), want the one successor: they arrive together and one rotation is what the store records", i+2, rec.Code, rec.Body.String())
		}
		if got := answerOf(t, rec); got.Key != first.Key || got.RenewalSecret != first.RenewalSecret {
			t.Errorf("renewal %d answered another bundle, want the one rotation: a second mint is a key the store holds no record of", i+2)
		}
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s) by %d concurrent renewals of one key, want one: only one of them can be the rotation the store records", got, callers)
	}
}

// TestASecondInstanceAnswersTheSameRotation: the 24-hour window is the store's
// and not the process's. A job whose answer was lost asks again hours later, and
// which instance answers is the load balancer's choice — with the window in one
// process's memory, that is a refusal for a holder whose successor exists, and
// the recovery K4.1 plans (retry with the previous secret version) is exactly
// that ask. The second instance here shares the first's store and nothing else:
// its own memory, its own minter, its own limiter.
func TestASecondInstanceAnswersTheSameRotation(t *testing.T) {
	first, _ := renewing(t)
	keyID, secret, _ := heldKey(t, first, PolicyTeamMonthly)
	first.inTheRenewalWindow(t, keyID)
	call := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}
	answer := answerOf(t, first.call(RouteRenew, call))
	if answer.Key == "" {
		t.Fatal("the first instance answered no key, so there is no rotation to replay")
	}

	second, spy := renewing(t)
	second.deps.Store = first.store
	second.start()

	rec := second.call(RouteRenew, call)
	if rec.Code != http.StatusOK {
		t.Fatalf("the second instance answered the replay %d (%q), want the successor: a retry is answered by whichever instance reaches it", rec.Code, rec.Body.String())
	}
	if replayed := answerOf(t, rec); replayed.Key != answer.Key || replayed.RenewalSecret != answer.RenewalSecret {
		t.Error("the second instance answered a different bundle, want the rotation the first one made: the minter would sign a different key with a different secret, and the successor the lost answer carried would be gone")
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the second instance minted %d key(s), want none: the rotation exists and a second one is a key nobody was told about", got)
	}
}

// TestAnExpiredHandoverIsDropped: the store holds a rotation's successor — the
// key's own text and its renewal secret — for the window and no longer. What it
// must not do is keep it until somebody asks about that key again, because the
// key whose answer was lost is the one nobody asks a second time: an entry left
// behind is a live credential in the store past the end of the promise. Every
// read and every write drops what the window has closed on.
func TestAnExpiredHandoverIsDropped(t *testing.T) {
	f, _ := renewing(t)
	ctx := context.Background()
	rotate := func(policy string) string {
		t.Helper()
		keyID, secret, _ := heldKey(t, f, policy)
		f.inTheRenewalWindow(t, keyID)
		if answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})).Key == "" {
			t.Fatalf("the key %s did not renew", keyID)
		}
		return keyID
	}
	first := rotate(PolicyTeamMonthly)
	if _, live, err := f.store.Handover(ctx, first, f.now); err != nil || !live {
		t.Fatalf("the store answered live=%v err=%v for a rotation made now, want the successor it has just kept", live, err)
	}
	second := rotate(PolicyTeamMonthly)

	f.advance(25 * time.Hour)
	rotate(PolicyTeamMonthly)
	if got := f.handovers(); got != 1 {
		t.Errorf("the store holds %d rotation(s)' answers after one past both windows, want the one whose window is open: what is left behind is a key and its secret", got)
	}
	for _, keyID := range []string{first, second} {
		if _, live, err := f.store.Handover(ctx, keyID, f.now); err != nil || live {
			t.Errorf("the store still answers the rotation of %s (live=%v err=%v) past its window, want none", keyID, live, err)
		}
	}
}

// TestHandoverWithNoKeyIsRefused: a rotation's answer is read by the key that
// rotated, so an entry filed under no key at all is the one entry that answers
// for every key nobody named — and what this record answers with is a key and a
// renewal secret. The write that could carry no key_id is the service's own
// (renew.go keeps the successor under the id it rotated), and the store refuses
// it for the reason it refuses an unnamed revocation rather than keeping it
// where the sweep would serve it.
func TestHandoverWithNoKeyIsRefused(t *testing.T) {
	f, _ := renewing(t)
	ctx := context.Background()
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	if answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})).Key == "" {
		t.Fatalf("the key %s did not renew", keyID)
	}
	// The refused record is a real rotation's answer with its key id taken off,
	// so what the store is asked to refuse is the only unnamed thing about it:
	// its digest, its key, its secret and its window are the ones the service
	// wrote moments ago.
	kept, live, err := f.store.Handover(ctx, keyID, f.now)
	if err != nil || !live {
		t.Fatalf("the store answered live=%v err=%v for the rotation just made, want the successor it kept", live, err)
	}
	kept.KeyID = ""
	if err := f.store.SaveHandover(ctx, kept, f.now); !errors.Is(err, ErrHandoverUnnamed) {
		t.Errorf("a handover that names no key answered %v, want %v", err, ErrHandoverUnnamed)
	}
	if _, live, err := f.store.Handover(ctx, "", f.now); err != nil || live {
		t.Errorf("the refused handover was written anyway: live=%v err=%v", live, err)
	}
}

// TestARotationInsideItsWindowWithoutItsAnswerIsRetried: the rotation and the
// answer beside it are two writes, so there is an instant — and, if the second
// write fails, a longer stretch — in which the rotation exists and its answer
// does not. Inside the window that is a service unable to answer yet, and the
// holder is told to ask again; a refusal there would tell a customer whose
// successor is minutes old that its key is finished and send it to a reissue.
// Past the window the answer is gone for good, and the refusal is the honest
// answer then.
func TestARotationInsideItsWindowWithoutItsAnswerIsRetried(t *testing.T) {
	f, _ := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	call := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}
	if answerOf(t, f.call(RouteRenew, call)).Key == "" {
		t.Fatal("the key did not renew, so there is no rotation to lose the answer of")
	}
	f.store.mu.Lock()
	delete(f.store.handovers, keyID)
	f.store.mu.Unlock()

	if rec := f.call(RouteRenew, call); rec.Code != http.StatusInternalServerError {
		t.Errorf("a replay inside the window whose answer the store does not hold was answered %d (%q), want %d: a retry is what asks again", rec.Code, rec.Body.String(), http.StatusInternalServerError)
	}
	f.advance(renewalHandover + time.Hour)
	if rec := f.call(RouteRenew, call); rec.Code != http.StatusForbidden {
		t.Errorf("a replay past the window whose answer the store does not hold was answered %d, want %d: the window is over and the secret is spent", rec.Code, http.StatusForbidden)
	}
}

// unrecordedRotations is the store of an instance that lost a rotation to
// another one: the rotation's record comes back as a source the store already
// holds, which is the instant between the record and the handover beside it.
type unrecordedRotations struct {
	*Memory
	refuse bool
}

func (u *unrecordedRotations) SaveIssuance(ctx context.Context, r Record) error {
	if u.refuse && strings.HasPrefix(r.Source, "rotation:") {
		return ErrAlreadyRecorded
	}
	return u.Memory.SaveIssuance(ctx, r)
}

// TestARotationTheStoreDidNotRecordIsNeverAnswered: the record is what makes a
// successor a key the store can renew, so a renewal whose rotation another
// instance recorded is answered with *that* rotation and never with the key
// minted here — the store holds no record of it, so every later renewal would
// refuse it and the customer would install a paid key that is dead on arrival.
// While the other instance's answer is not in the store yet, the answer is a
// failure to retry and not a key.
func TestARotationTheStoreDidNotRecordIsNeverAnswered(t *testing.T) {
	spy := &v1Spy{}
	store := &unrecordedRotations{}
	f := newFixtureWith(t, Production, func(f *fixture) {
		spy.inner = Minter{Signer: f.signer, Keys: f.keys, Environment: f.env, Now: f.clock}
		f.deps.MintsV1 = spy
		store.Memory = f.store
		f.deps.Store = store
	})
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	store.refuse = true

	rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a renewal whose rotation was recorded elsewhere was answered %d (%q), want %d: what this instance minted is not the rotation, and a key the store holds no record of is one nothing would let renew", rec.Code, rec.Body.String(), http.StatusInternalServerError)
	}
	if body := rec.Body.String(); strings.Contains(body, keyPrefixV1) {
		t.Errorf("the answer %q carries a key text, want none: the store holds no record of the key this instance minted", body)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one this instance minted before it lost the rotation", got)
	}
}

// TestTheRenewalFailsWhenTheMinterDoes: the intake holds no signer (Check 174),
// so a minter that refuses or is not configured leaves the caller with a
// failure and no key — and never with an answer that reads like a refusal of
// the caller's secret, which would tell a holder to stop trying.
func TestTheRenewalFailsWhenTheMinterDoes(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	spy.err = errors.New("the minter is unreachable")
	if rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}); rec.Code != http.StatusInternalServerError {
		t.Errorf("a renewal whose minter failed was answered %d, want %d: a transient failure is not a refusal of the key", rec.Code, http.StatusInternalServerError)
	}
	if _, held, err := f.store.KeyState(context.Background(), keyID); err != nil || !held {
		t.Fatalf("reading the key's state: held=%v err=%v", held, err)
	}
}

// TestARenewalOutsideItsWindowMintsNothing: a renewal is the key for the term
// *after* the one the customer is in, and the service mints it inside the lead
// the key's own row names before `paid_through` (§7, T26) and not before. A key
// issued a moment ago is a whole term away from that, and what it is answered is
// its own state — `ok` and no successor, which is what the status route says
// about it — rather than a refusal that would read as the licence being over.
func TestARenewalOutsideItsWindowMintsNothing(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	call := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}

	rec := f.call(RouteRenew, call)
	if rec.Code != http.StatusOK {
		t.Fatalf("a renewal asked for a term early was answered %d (%q), want the key's own state", rec.Code, rec.Body.String())
	}
	answer := answerOf(t, rec)
	if answer.Status != StatusOK || answer.NewerKey {
		t.Errorf("the answer is %+v, want the key's state and no successor", answer)
	}
	assertNoKeyIn(t, rec, secret)
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) outside the renewal window, want none: a term minted before the one the customer is in is a term nobody paid for", got)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the store holds %d record(s) after a renewal that minted nothing, want the key's own and no other", got)
	}

	// The control: the same call inside the window is served, so what the window
	// turned away above is the timing and not the route.
	f.inTheRenewalWindow(t, keyID)
	if answerOf(t, f.call(RouteRenew, call)).Key == "" {
		t.Error("the same renewal inside the window answered no key, want the successor")
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one renewal the open window allowed", got)
	}
}

// TestChainedRenewalsStopAtOneTerm: the probe a review of this endpoint ran, as
// a test. A successor is a new key id with a new secret and therefore a throttle
// bucket of its own, so a holder that renews the key it was just handed — twelve
// times, all at one instant — takes twelve terms for the price of one and ends
// up with a licence that outlives everything it paid for. The window is what
// stops the chain: the successor's own window opens a term after the renewal
// that minted it, so one call buys one term, and the latest `paid_through` the
// store holds is the day that one renewal was asked for plus the thirty days a
// connected row's key runs (D2202).
func TestChainedRenewalsStopAtOneTerm(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	asked := f.now
	paidThrough := f.issuance(t, keyID).Expires

	held := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}
	first := answerOf(t, f.call(RouteRenew, held))
	if first.Key == "" {
		t.Fatal("the renewal inside the window answered no key, so there is no chain to climb")
	}
	// Every later call renews the key the one before it was handed, which is
	// what a holder climbing the ladder does.
	for i := 0; i < 11; i++ {
		claims := v1Verified(t, f, first.Key)
		held = RenewalCall{KeyID: claims.KeyID, WorkspaceID: workspaceID, RenewalSecret: first.RenewalSecret}
		answer := answerOf(t, f.call(RouteRenew, held))
		if answer.Key != "" {
			t.Fatalf("renewal %d of the chain was handed a key, want the key's own state: a successor at one instant buys a term per call, and the licence outlives what was paid for", i+2)
		}
		if answer.Status != StatusOK {
			t.Errorf("renewal %d of the chain was answered %q, want the successor's own state", i+2, answer.Status)
		}
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s) by twelve renewals at one instant, want the one the open window allowed", got)
	}
	furthest := paidThrough
	for _, record := range f.records() {
		if record.Expires.After(furthest) {
			furthest = record.Expires
		}
	}
	if want := asked.Add(connectedTerm); !furthest.Equal(want) {
		t.Errorf("the latest term in the store ends at %s, want %s — the one renewal the window allowed and the thirty days a connected key runs from the day it was asked for, and not a period per call (T26, D2202)", furthest, want)
	}
}

// TestARenewalOffTheDayGridRunsItsCappedThirtyDays: the fixture's clock is whole
// days, which is what hides this from the chain test above. The term the
// endpoint names is whole days (`RequestV1.TermDays`) and the minter dates the
// successor from its own clock and never from a date this side sends (§2), so a
// renewal asked at an hour the key was not issued at is dated from that hour.
// What that hour costs is the asker's own lateness inside the window: the
// successor runs the thirty days D2202 gives a connected key from the minute it
// was asked for and not from the minute the window opened, so a job that runs at
// the minute its predecessor's round ended lands its successor a whole number of
// days on, and the renewal after that one measures from its own asking again —
// the day the round costs is paid once and never handed out a period at a time
// (T26).
func TestARenewalOffTheDayGridRunsItsCappedThirtyDays(t *testing.T) {
	f, _ := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)
	// Seven hours and thirteen minutes later than the minute the window opened
	// on, which is where a daily job's run lands for a key issued at noon.
	f.advance(7*time.Hour + 13*time.Minute)
	asked := f.now

	answer := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
	if answer.Key == "" {
		t.Fatalf("the renewal inside the window answered no key: %+v", answer)
	}
	claims := v1Verified(t, f, answer.Key)
	if want := asked.Add(connectedTerm); !claims.PaidThrough.Equal(want) {
		t.Errorf("the successor is paid through %s, want %s: it runs the thirty days D2202 gives a connected key from the minute it was asked for, and not from the minute the window opened", claims.PaidThrough, want)
	}
	if rest := claims.PaidThrough.Sub(asked) % (24 * time.Hour); rest != 0 {
		t.Errorf("the successor is paid through %s, which is %s from the asking and not a whole number of days: the minter dates a key in whole days from its own clock", claims.PaidThrough, rest)
	}

	// The renewal after it, asked from the minute the successor's end is on,
	// lands on the term's end itself — once the merchant's own renewal has
	// credited the term, which is what a term bought means here.
	theSubscriptionCovers(t, f, claims.KeyID)
	f.inTheRenewalWindow(t, claims.KeyID)
	asked = f.now
	next := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: claims.KeyID, WorkspaceID: workspaceID, RenewalSecret: answer.RenewalSecret}))
	if next.Key == "" {
		t.Fatalf("the successor did not renew: %+v", next)
	}
	if got, want := v1Verified(t, f, next.Key).PaidThrough, asked.Add(connectedTerm); !got.Equal(want) {
		t.Errorf("the renewal after an off-grid one is paid through %s, want %s: every key in the chain ends its own thirty days from the day it was asked for (T26, D2202)", got, want)
	}
}

// losingHandovers is the store of an instance whose handover write fails while
// the two writes before it land: the rotation's state and its record are in the
// store and the answer kept beside them is not.
type losingHandovers struct{ *Memory }

func (l *losingHandovers) SaveHandover(context.Context, Handover, time.Time) error {
	return errors.New("the store refused the write")
}

// TestARenewalIsAnsweredWhenTheHandoverCannotBeKept: the state, the record and
// the handover are three writes, and a store that refuses the third has already
// taken the first two — the successor is signed and the store holds its state
// and its record, which is everything a key needs to renew. Answering the holder
// with a failure there throws a paid term away and leaves the key unable to
// rotate again: a retry is asked inside the window the handover was for and is
// answered the same failure until it closes, and past it the old secret is spent
// for good. What the holder loses is the retry, so the store's refusal is logged
// and the key is answered.
func TestARenewalIsAnsweredWhenTheHandoverCannotBeKept(t *testing.T) {
	spy := &v1Spy{}
	store := &losingHandovers{}
	f := newFixtureWith(t, Production, func(f *fixture) {
		spy.inner = Minter{Signer: f.signer, Keys: f.keys, Environment: f.env, Now: f.clock}
		f.deps.MintsV1 = spy
		store.Memory = f.store
		f.deps.Store = store
	})
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)

	rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("a renewal whose handover could not be kept was answered %d (%q), want the successor: the key is minted and the store holds its state and its record", rec.Code, rec.Body.String())
	}
	answer := answerOf(t, rec)
	if answer.Key == "" || answer.RenewalSecret == "" {
		t.Fatal("the answer carries no bundle, want the key and the secret that were minted")
	}
	if _, live, err := f.store.Handover(context.Background(), keyID, f.now); err != nil || live {
		t.Errorf("the store answers live=%v err=%v for the handover it refused to keep, want none", live, err)
	}

	// The successor is a holder in its own right, and it renews a term later:
	// what the handover carries is the retry and not the key.
	successor := v1Verified(t, f, answer.Key).KeyID
	if _, held, err := f.store.KeyState(context.Background(), successor); err != nil || !held {
		t.Fatalf("the successor has no state in the store (held=%v err=%v), want the write that makes it a key", held, err)
	}
	theSubscriptionCovers(t, f, successor)
	f.inTheRenewalWindow(t, successor)
	if answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: successor, WorkspaceID: workspaceID, RenewalSecret: answer.RenewalSecret})).Key == "" {
		t.Error("the successor did not renew, want the chain to go on")
	}
	if got := spy.minted(); got != 2 {
		t.Errorf("the minter was asked for %d key(s), want the successor's own renewal beside the first", got)
	}
}

// TestARequestSpendsItsAddressesAllowanceWhateverItsShape: the address's bucket
// is what bounds guessing at a secret (§7.2), so it is spent by the request
// before anything of it is read: a body this service will not parse, a key id it
// has never issued and a wrong secret each cost the address exactly what a good
// call costs. A bucket that only counted the requests that reached a key id
// would serve a flood of bodies nobody parses for free, and the limit that
// bounds guessing would be the one thing a caller can step around.
func TestARequestSpendsItsAddressesAllowanceWhateverItsShape(t *testing.T) {
	f, _ := renewingWithin(t, Limits{Requests: 2, Window: time.Hour})
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	f.inTheRenewalWindow(t, keyID)

	for _, body := range []string{
		`key_id=` + keyID,
		`{"key_id":"` + unknownKeyID + `","workspace_id":"` + workspaceID + `","renewal_secret":"` + freshSecret(t) + `"}`,
	} {
		if rec := f.raw(RouteRenew, body); rec.Code != http.StatusForbidden {
			t.Fatalf("a request the service refuses was answered %d, want %d", rec.Code, http.StatusForbidden)
		}
	}
	// The address's allowance is spent, so the holder's own call from it is
	// refused like anyone else's: what the bucket counts is the request.
	rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret})
	if rec.Code != http.StatusForbidden {
		t.Errorf("the holder's renewal, from an address whose allowance two requests spent, was answered %d (%q), want %d", rec.Code, rec.Body.String(), http.StatusForbidden)
	}
	if body := rec.Body.String(); body != renewalRefusal+"\n" {
		t.Errorf("a request past the address's allowance was answered %q, want the one refusal %q", body, renewalRefusal)
	}
}

// TestAnAnnualRowsKeyRenewsInItsOwnLastWeek: D2202 gives every connected row's
// key thirty days, so the window a renewal opens in is the key's own and never
// the year behind it — which is what the row's seven-day lead measures back
// from. On an annual row that is the key's twenty-third day rather than the
// three hundred and fifty-eighth of the term the customer bought: the licence
// in hand lapses a month in, and the year the subscription holds (S3.5) is not
// a term this key can run.
func TestAnAnnualRowsKeyRenewsInItsOwnLastWeek(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamAnnual)
	record := f.issuance(t, keyID)
	if run := record.Expires.Sub(record.Issued); run != connectedTerm {
		t.Fatalf("the annual row's key runs %s, want D2202's thirty days", run)
	}
	call := RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}

	f.advance(23*day - time.Hour)
	if answer := answerOf(t, f.call(RouteRenew, call)); answer.Key != "" {
		t.Errorf("a renewal asked an hour before the window opened was handed a key, want the key's own state: the window is the key's last week (T26)")
	}
	if got := spy.minted(); got != 0 {
		t.Errorf("the minter was asked for %d key(s) before the window opened, want none", got)
	}

	f.advance(time.Hour)
	answer := answerOf(t, f.call(RouteRenew, call))
	if answer.Key == "" {
		t.Fatalf("the renewal on the day the window opens answered no key (%+v), want the successor: the customer's year is renewed a month at a time", answer)
	}
	claims := v1Verified(t, f, answer.Key)
	if run := claims.PaidThrough.Sub(claims.IssuedAt); run != connectedTerm {
		t.Errorf("the successor runs %s, want D2202's thirty days again: what renews is the key, and the entitlement is what the subscription holds", run)
	}
}

// TestAPurchasedKeyRenewsThroughTheRenewEndpoint: the key a purchase mails is
// the key D2202 makes a month of, and the endpoint the plan names for carrying an
// entitlement from one of those to the next is `/renew` — "keys of connected
// entitlements carry at most 30 days to `paid_through` and renew through
// `/renew` while the entitlement runs" (docs/portal/reconciliation.md). The
// state the paid lane writes is the whole of that endpoint's input, and this
// reads both halves of it: the key's own renewal hash, and the workspace the
// successor is minted into — a state that names none is refused there rather than
// renewed (TN2, K2.3), which leaves the customer dark from the end of the key in
// hand until the merchant's own renewal.
func TestAPurchasedKeyRenewsThroughTheRenewEndpoint(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-1", "acme.example", 5, 365)
	if len(f.mail.sent) != 1 {
		t.Fatalf("the purchase mailed %d message(s), want the one carrying the key", len(f.mail.sent))
	}
	key, secret := mailedBundle(t, f.mail.sent[0].Body)
	if claims := v1Verified(t, f, key); claims.KeyID != bought.KeyID {
		t.Fatalf("the mail carries the key %s, want the purchased one %s", claims.KeyID, bought.KeyID)
	}

	state, held, err := f.store.KeyState(context.Background(), bought.KeyID)
	if err != nil || !held {
		t.Fatalf("the purchase wrote no key state for %s (held=%v, err=%v), and without one there is no renewal at all", bought.KeyID, held, err)
	}
	if state.RenewalSecretHash != secretHash(secret) {
		t.Errorf("the purchase's key state carries the hash %q, want the hash of the secret it mailed: a state hashed from anything else files the key under a secret its holder does not have", state.RenewalSecretHash)
	}
	if state.WorkspaceID != workspaceID {
		t.Fatalf("the purchase's key state names the workspace %q, want the one the purchase bound (%q): this endpoint mints the successor into the state's own workspace and refuses a state that names none", state.WorkspaceID, workspaceID)
	}

	f.advance(24 * day)
	answer := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: bought.KeyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
	if answer.Key == "" {
		t.Fatalf("the purchased key was answered %+v, want a successor: a paid key that cannot renew is a licence that ends at D2202's thirty days", answer)
	}
	if got := spy.minted(); got != 1 {
		t.Errorf("the minter was asked for %d key(s), want the one successor", got)
	}
	if run := v1Verified(t, f, answer.Key).PaidThrough.Sub(f.now); run != connectedTerm {
		t.Errorf("the successor runs %s, want D2202's thirty days from the day it was asked for", run)
	}
}

// TestAnAnnualPurchasesLicenceIsLiveAllYear: D2202 gives every connected key
// thirty days, so what an annual purchase buys is a chain of them, and this
// endpoint is what mints each successor. It is the whole of what keeps the
// customer licensed in between: the merchant's own renewal opens its window
// RenewalLead before the subscription's own end — day 335 of a year — so the
// installation is dark from the end of its first key, plus the row's grace, until
// then. The chain is walked the way a holder's job walks it: each renewal is
// asked for on the day the key in hand says the job should ask, and every
// successor covers past the end of the key it replaces, so no instant inside the
// year is one with nothing live.
func TestAnAnnualPurchasesLicenceIsLiveAllYear(t *testing.T) {
	f, spy := renewing(t)
	bought := f.purchase("evt-1", "acme.example", 5, 365)
	if len(f.mail.sent) != 1 {
		t.Fatalf("the purchase mailed %d message(s), want the one carrying the key", len(f.mail.sent))
	}
	key, secret := mailedBundle(t, f.mail.sent[0].Body)
	claims := v1Verified(t, f, key)
	if run := claims.PaidThrough.Sub(claims.IssuedAt); run != connectedTerm {
		t.Fatalf("the purchased key runs %s, want D2202's thirty days: the subscription carries the year and the key is a month of it", run)
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the subscription is %v, held=%v, want the purchase's own", err, held)
	}
	if want := bought.Issued.Add(365 * day); !sub.Expires.Equal(want) {
		t.Fatalf("the subscription ends at %s, want the end of the year the customer bought (%s)", sub.Expires, want)
	}

	// Each call nets the chain the lead short of a full term: the successor is
	// capped at thirty days from its own issue (D2202, `maxKeyTerm`) and is asked
	// for renewalLeadDays before the end of the key it replaces, so it covers
	// twenty-three days of new ground and overlaps the last week of the old one.
	// A year is fifteen of those; the guard is well past that, so a chain that
	// stops covering fails on the year rather than on the count.
	renewals := 0
	for covered := claims.PaidThrough; covered.Before(sub.Expires); renewals++ {
		if renewals > 20 {
			t.Fatalf("%d renewals did not cover the year to %s, so this is not one D2202 term per call", renewals, sub.Expires)
		}
		// The day the key in hand says its job should ask: its own row's lead
		// before the end it carries, which is where the window opens.
		f.advance(claims.PaidThrough.Add(-renewalLeadDays * day).Sub(f.now))
		answer := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: claims.KeyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
		if answer.Key == "" {
			t.Fatalf("renewal %d was answered %+v, want a successor: this endpoint is what carries an annual purchase from one thirty-day key to the next, and nothing else mints for it until the merchant's own renewal on day 335", renewals+1, answer)
		}
		next := v1Verified(t, f, answer.Key)
		if !next.PaidThrough.After(claims.PaidThrough) {
			t.Errorf("renewal %d ends at %s, want past the end of the key it replaces (%s): a successor short of it leaves the installation dark between the two", renewals+1, next.PaidThrough, claims.PaidThrough)
		}
		if !f.now.Before(claims.PaidThrough) {
			t.Errorf("renewal %d was asked for at %s, at or after the end of the key it replaces (%s): the window is the key's own last week, so a job that runs inside it never leaves a gap", renewals+1, f.now, claims.PaidThrough)
		}
		claims, secret, covered = next, answer.RenewalSecret, next.PaidThrough
	}
	if got := spy.minted(); got != renewals {
		t.Errorf("the minter was asked for %d key(s) across %d renewals, want one successor each", got, renewals)
	}
	if renewals != 15 {
		t.Errorf("the year took %d renewals, want the fifteen a thirty-day key asked for a week early costs", renewals)
	}
}
