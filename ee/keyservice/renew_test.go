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
	record := minted.Record
	record.Source = "purchase:test:" + record.KeyID
	record.Email = buyerAddress("acme.example")
	if err := f.store.SaveIssuance(ctx, record); err != nil {
		t.Fatalf("writing the key's record: %v", err)
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
	// own window opens.
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
// store holds is this key's own end plus that single period.
func TestChainedRenewalsStopAtOneTerm(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	policy, known := DefaultPolicy(PolicyTeamMonthly)
	if !known {
		t.Fatal("the monthly row is not in the table")
	}
	f.inTheRenewalWindow(t, keyID)
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
	if want := paidThrough.Add(policy.Term); !furthest.Equal(want) {
		t.Errorf("the latest term in the store ends at %s, want %s — the term this key is in plus the one renewal it was entitled to, and not a period per call (T26)", furthest, want)
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
