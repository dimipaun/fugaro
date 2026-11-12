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
	spy := &v1Spy{}
	f := newFixtureWith(t, Production, func(f *fixture) {
		spy.inner = Minter{Signer: f.signer, Keys: f.keys, Environment: f.env, Now: f.clock}
		f.deps.MintsV1 = spy
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
	ctx := context.Background()
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: f.env, Now: f.clock}
	minted, err := minter.MintV1(ctx, RequestV1{
		Environment:  f.env,
		Policy:       policy,
		WorkspaceID:  workspaceID,
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
		RenewalSecretHash: secretHash(minted.RenewalSecret),
		LastSeen:          f.now,
	}); err != nil {
		t.Fatalf("writing the key's state: %v", err)
	}
	return record.KeyID, minted.RenewalSecret, minted.Key
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
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+route, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
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

// TestRevokedKeyIsNeverRenewed: §8 — the revocation list is read before
// anything is minted, and a key the licensor has withdrawn is never handed a
// successor. The holder is told `revoked` (its own secret proved it is the
// holder) and is given no key, and no signer is reached.
func TestRevokedKeyIsNeverRenewed(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
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

// TestTheOldSecretFetchesTheNextKeyForADay: D2135's window and its edge. A
// rotation makes a new secret, and the old one still fetches that same successor
// for 24 hours — the time a lost answer needs to be asked for again — and is
// refused after it. The successor is a holder in its own right: it renews with
// its own secret, under its own key id.
func TestTheOldSecretFetchesTheNextKeyForADay(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
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
	// chain rather than a key that can be renewed once.
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

	f.advance(20 * 24 * time.Hour)
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
	// Ten days of the key's own term are left, so the successor runs for those
	// ten plus a whole period.
	if want := 10*24*time.Hour + policy.Term; time.Duration(asked.TermDays)*24*time.Hour != want {
		t.Errorf("the successor was asked for %d day(s), want the %s left of this term plus the row's %s: a renewal inside the window costs the customer nothing", asked.TermDays, 10*24*time.Hour, policy.Term)
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

// TestTheRenewalFailsWhenTheMinterDoes: the intake holds no signer (Check 174),
// so a minter that refuses or is not configured leaves the caller with a
// failure and no key — and never with an answer that reads like a refusal of
// the caller's secret, which would tell a holder to stop trying.
func TestTheRenewalFailsWhenTheMinterDoes(t *testing.T) {
	f, spy := renewing(t)
	keyID, secret, _ := heldKey(t, f, PolicyTeamMonthly)
	spy.err = errors.New("the minter is unreachable")
	if rec := f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}); rec.Code != http.StatusInternalServerError {
		t.Errorf("a renewal whose minter failed was answered %d, want %d: a transient failure is not a refusal of the key", rec.Code, http.StatusInternalServerError)
	}
	if _, held, err := f.store.KeyState(context.Background(), keyID); err != nil || !held {
		t.Fatalf("reading the key's state: held=%v err=%v", held, err)
	}
}
