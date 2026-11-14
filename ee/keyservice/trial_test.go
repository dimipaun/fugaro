// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The 14-day Team trial (§14, D2136, D2201; K2.7): the door a signed-in account
// asks at and the rule that one installation and one account get one. Every
// test here drives the route through the handler, because what it owes is an
// answer to a caller — the trial it minted, the refusal a second ask meets and
// the sign-in a request without one is met with — and not a function's return
// value.
//
// The project id a test names is the fixture's own `workspaceID` and the account
// is the fixture's own address: neither is real, and the key every test reads
// back was minted through the fixture's throwaway keyring and verified by
// ee/licence's verifier rather than by this package's own ideas about a claim
// set.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// trialSeats is what a test asks a trial for. The number is the caller's (the
// trial row names none, K2.1) and the fixture's own.
const trialSeats = 3

// trialAccount is the account a test signs in as. It is an address at the
// fixture's own domain and obviously not a real one.
const trialAccount = "founder@acme.example"

// trialTicket is the ID token a test's sign-in carries: the verifier behind the
// fixture's seam answers the caller it was built with whatever it is handed, so
// what this string says never matters — that it arrives as the request's bearer
// token is the whole of what the route is asked about it.
const trialTicket = "fake-id-token-0123456789"

// trialAs is the fixture with an account signed in: the seam the route reads to
// learn who asks is swapped for one that proves trialAccount.
func trialAs(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, Production, func(f *fixture) {
		f.signIn.caller = Caller{UID: "uid-trial", Email: trialAccount, Provider: "google.com", AuthTime: f.now}
	})
}

// ask is one trial request, from the applicant's own address: the route's own
// body, carrying the token the sign-in rides in.
func (f *fixture) ask(body string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.askFrom(applicantIP, body)
}

// askFrom is the same ask from an address the test names, which is what tells a
// per-domain bucket from a per-address one.
func (f *fixture) askFrom(ip, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteTrial, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+trialTicket)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// signsInAs turns the fixture's sign-in seam to another account, and it is the
// spy the running service already holds: a test that asks twice as two accounts
// is asking the same intake — the same limiter, the same store, the same key ring
// — rather than a service rebuilt for the second ask, which is what a per-domain
// bucket has to be read on.
func (f *fixture) signsInAs(uid, email string) {
	f.t.Helper()
	f.signIn.caller = Caller{UID: uid, Email: email, Provider: "google.com", AuthTime: f.now}
}

// claimRefuser is the store with a claim that cannot be written: every other
// method is the fixture's own, so what a test reads after such an ask is what
// the route really wrote and not what a stub remembers.
type claimRefuser struct {
	Store
	err error
}

func (c claimRefuser) ClaimTrial(context.Context, Trial) error { return c.err }

// unansweredClaim is the store whose answer to a claim was lost: the claim
// lands — the fixture's store holds the trial — and the error that reaches the
// route is the one a re-run of the deployed store's transaction answers when it
// reads the documents its own first attempt committed (firestore_store.go's
// `ClaimTrial`, which is re-run when its result is lost).
type unansweredClaim struct {
	Store
}

func (c unansweredClaim) ClaimTrial(ctx context.Context, t Trial) error {
	if err := c.Store.ClaimTrial(ctx, t); err != nil {
		return err
	}
	return ErrTrialAlreadyClaimed
}

// blindAfterAClaim is the store that cannot be read once a claim has failed, so
// what it answers leaves the claim's landing unknowable: the one case in which
// the route has nothing but a guess to go on.
type blindAfterAClaim struct {
	Store
	blind bool
}

func (b *blindAfterAClaim) ClaimTrial(context.Context, Trial) error {
	b.blind = true
	return errors.New("the store could not be written")
}

func (b *blindAfterAClaim) Trials(ctx context.Context) ([]Trial, error) {
	if b.blind {
		return nil, errors.New("the store could not be read")
	}
	return b.Store.Trials(ctx)
}

// trialBody is the request one test sends: the workspace's project id and the
// seats the trial is asked for.
func trialBody(workspace string, seats int) string {
	body, err := json.Marshal(TrialCall{WorkspaceID: workspace, Seats: seats})
	if err != nil {
		panic(err)
	}
	return string(body)
}

// TestTrialIsTeamTierFourteenDays: §14's trial is a normal Team key issued
// without payment — 14 days, no grace, a daily check — and it is keyed on the
// installation's Firebase project id and the account that asked (D2136, D2201).
// The key is read back through the released verifier, so what this holds is the
// claim set an installation receives and not this package's own record of one.
func TestTrialIsTeamTierFourteenDays(t *testing.T) {
	f := trialAs(t)
	rec := f.ask(trialBody(workspaceID, trialSeats))
	if rec.Code != http.StatusOK {
		t.Fatalf("a trial ask answered %d (%s), want 200", rec.Code, rec.Body.String())
	}

	keys := f.mintedKeys()
	if len(keys) != 1 {
		t.Fatalf("the ask minted %d key(s), want one", len(keys))
	}
	claims, state, err := licence.Verify([]byte(keys[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the trial's key does not verify: %v", err)
	}
	if state != licence.Active {
		t.Errorf("the trial's key reads %s the instant it was minted, want active", state)
	}
	if claims.Tier != licence.TierTeam || claims.Kind != licence.KindTrial {
		t.Errorf("the trial's key is %s/%s, want %s/%s (D2136)", claims.Tier, claims.Kind, licence.TierTeam, licence.KindTrial)
	}
	if claims.GraceDays != 0 {
		t.Errorf("the trial's key carries %d day(s) of grace, and D2138 rules that a trial has none", claims.GraceDays)
	}
	if want := f.now.Add(14 * 24 * time.Hour); !claims.PaidThrough.Equal(want) {
		t.Errorf("the trial runs to %s, want the fourteen days of §14 (%s)", claims.PaidThrough, want)
	}
	if claims.CheckInterval == nil || *claims.CheckInterval != dailyCheck {
		t.Errorf("the trial's check interval is %v, want %s: a trial is revoked by contact like any other connected key (§14)", claims.CheckInterval, dailyCheck)
	}
	if claims.WorkspaceID != workspaceID {
		t.Errorf("the trial's key binds to workspace %q, want the installation's own project id %q (TN2)", claims.WorkspaceID, workspaceID)
	}
	if claims.Seats != trialSeats {
		t.Errorf("the trial's key is minted for %d seat(s), want the %d that were asked for", claims.Seats, trialSeats)
	}

	// The store's trial names the two things §14 counts, and the record names
	// the installation the key was minted into.
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 1 {
		t.Fatalf("the store holds %d trial(s), want one: %+v", len(trials), trials)
	}
	if trials[0].Installation != workspaceID || trials[0].Account != trialAccount {
		t.Errorf("the trial is held for installation %q and account %q, want %q and %q (D2201)",
			trials[0].Installation, trials[0].Account, workspaceID, trialAccount)
	}
	if trials[0].KeyID == "" {
		t.Error("the trial names no key, so an operator reading the list cannot find what it minted")
	}
	records := f.records()
	if len(records) != 1 || records[0].Organisation != "acme.example" {
		t.Errorf("the minted key's record is %+v, want one record for the account's own organisation", records)
	}

	// The bundle leaves with the key: the second line is the renewal secret, and
	// the store holds its hash alone (§7.3, D2135).
	if len(f.mail.sent) != 1 || !strings.Contains(f.mail.sent[0].Body, keys[0]) {
		t.Fatalf("the ask mailed %d message(s) and the key is not in them: %+v", len(f.mail.sent), f.mail.sent)
	}
	if f.mail.sent[0].To != trialAccount {
		t.Errorf("the bundle went to %q, want the address the sign-in proved (%q)", f.mail.sent[0].To, trialAccount)
	}
	held, known, err := f.store.KeyState(t.Context(), records[0].KeyID)
	if err != nil || !known {
		t.Fatalf("the minted key has no state (known=%v err=%v), so nothing can check it in (§8)", known, err)
	}
	if held.WorkspaceID != workspaceID {
		t.Errorf("the key's state names workspace %q, want %q", held.WorkspaceID, workspaceID)
	}
}

// TestSecondTrialIsRefused: §14's rule is one trial per installation or account
// (D2136, D2201). The second ask is refused, and the refusal mints nothing at
// the key ring — a refused ask that had already signed a key would spend the
// licensor's authority on a trial nobody gets — and writes nothing: the store
// still holds the one trial the first ask claimed.
func TestSecondTrialIsRefused(t *testing.T) {
	f := trialAs(t)
	if rec := f.ask(trialBody(workspaceID, trialSeats)); rec.Code != http.StatusOK {
		t.Fatalf("the first ask answered %d (%s), want 200", rec.Code, rec.Body.String())
	}

	// The same account, another installation: the account has had its trial.
	sameAccount := f.ask(trialBody("acme-other", trialSeats))
	if sameAccount.Code != http.StatusConflict {
		t.Errorf("a second ask from the same account answered %d (%s), want 409", sameAccount.Code, sameAccount.Body.String())
	}
	// Another account, the installation the first trial was minted for: the
	// installation has had its trial.
	f.signsInAs("uid-other", "other@acme.example")
	sameInstallation := f.ask(trialBody(workspaceID, trialSeats))
	if sameInstallation.Code != http.StatusConflict {
		t.Errorf("a second ask for the same installation answered %d (%s), want 409", sameInstallation.Code, sameInstallation.Body.String())
	}

	if minted := f.mintedKeys(); len(minted) != 1 {
		t.Errorf("the refused asks minted %d key(s) past the first, want none", len(minted)-1)
	}
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 1 {
		t.Errorf("the store holds %d trial(s) after two refusals, want the first ask's one: %+v", len(trials), trials)
	}
	if revocations := f.revocations(); len(revocations) != 0 {
		t.Errorf("the refusals withdrew %d key(s), want none: a refused ask never reached the key ring (%+v)", len(revocations), revocations)
	}
}

// TestTrialIsRefusedWithoutAVerifiedSignIn: the account is what the rule counts
// and the sign-in is the only thing that names it (K2.4's verifier, P1.3), so a
// request without one, and one whose token the verifier refuses, are both
// answered the same way and mint nothing.
func TestTrialIsRefusedWithoutAVerifiedSignIn(t *testing.T) {
	f := trialAs(t)
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"no token at all", ""},
		{"a token the verifier refuses", "another-token-0123456789"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.deps.SignIn = &signInSpy{err: errors.New("this token was minted for another project")}
			f.start()
			req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteTrial, strings.NewReader(trialBody(workspaceID, trialSeats)))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			req.RemoteAddr = applicantIP
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("the ask answered %d (%s), want 401", rec.Code, rec.Body.String())
			}
			if minted := f.mintedKeys(); len(minted) != 0 {
				t.Errorf("an ask with no verified sign-in minted %d key(s), want none", len(minted))
			}
		})
	}
}

// TestTrialIsRefusedForABodyItCannotRead: the body is the API's own JSON and it
// is read strictly, so a body with a field this route does not know, a body
// naming no workspace and one naming no seat count are refused before anything
// is minted. The id itself is not checked for shape here: a value that is not a
// project id mints a key no installation accepts, which is the asker's own loss.
func TestTrialIsRefusedForABodyItCannotRead(t *testing.T) {
	f := trialAs(t)
	bodies := map[string]string{
		"a field the route does not know": `{"workspace_id":"acme-widgets","seats":3,"note":"please"}`,
		"no workspace":                    `{"seats":3}`,
		"no seat count":                   `{"workspace_id":"acme-widgets"}`,
		"a seat count of none":            `{"workspace_id":"acme-widgets","seats":0}`,
		"not an object at all":            `"acme-widgets"`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			rec := f.ask(body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("the ask answered %d (%s), want 400", rec.Code, rec.Body.String())
			}
		})
	}
	if minted := f.mintedKeys(); len(minted) != 0 {
		t.Errorf("a body the route cannot read minted %d key(s), want none", len(minted))
	}
}

// TestTrialIsMountedOnlyWithASignInVerifier: the route reads the sign-in
// verifier to learn the account half of §14's rule, and it declares that seam
// where it is registered. It is the declaration and not the handler that keeps
// the service honest: a deployment that mounts this door without a verifier is
// refused when it starts (api.go's `reaching`, `missing`), rather than answering
// every ask with a failure of its own or, with the declaration gone, serving the
// ask with a nil verifier.
func TestTrialIsMountedOnlyWithASignInVerifier(t *testing.T) {
	route, known := apiRoutes.lookup(http.MethodPost, RouteTrial)
	if !known {
		t.Fatalf("no route is mounted at %s %s, so nothing serves a trial ask", http.MethodPost, RouteTrial)
	}
	if !slices.Contains(route.needs, needSignIn) {
		t.Errorf("the route %s %s does not declare the sign-in verifier it reads, so a deployment holding none mounts it and answers every ask with a failure of its own",
			route.method, route.path)
	}
	only := &apiRegistry{routes: []apiRoute{route}, byPath: map[string][]int{}}
	if err := only.missing(Deps{}); err == nil {
		t.Error("a deployment holding no sign-in verifier is not refused the route, want the start refused (P1.0)")
	}
}

// TestTrialIsThrottledPerDomain: §14's trial is a speed bump and the throttle it
// is documented with is the **domain's** and not the address's — "the trial path
// is throttled per domain" (docs/licensing/licence-key.md, Limits, accepted) —
// because the account a caller asks from is one they may open another of while
// the organisation behind it is not. The fixture's rate is one ask per window, so
// the second account at the first account's domain meets the bucket from a
// different address (which a per-address rule would have let through), and an
// account at another domain still asks.
func TestTrialIsThrottledPerDomain(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Throttle = Limits{Requests: 1, Window: time.Hour}
		f.signIn.caller = Caller{UID: "uid-trial", Email: trialAccount, Provider: "google.com", AuthTime: f.now}
	})
	if rec := f.askFrom(applicantIP, trialBody(workspaceID, trialSeats)); rec.Code != http.StatusOK {
		t.Fatalf("the first ask answered %d (%s), want 200", rec.Code, rec.Body.String())
	}
	// The same organisation, another account, another address: this is the ask a
	// per-address bucket would have served.
	f.signsInAs("uid-two", "colleague@acme.example")
	second := f.askFrom("198.51.100.44:4040", trialBody("acme-other", trialSeats))
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("a second account at the same domain answered %d (%s), want 429: §14's trial is throttled per domain and not per address", second.Code, second.Body.String())
	}
	// Another organisation asks and is served: what the bucket counts is the
	// domain and not the trial route as a whole.
	f.signsInAs("uid-three", "founder@widgets.example")
	third := f.askFrom("198.51.100.45:4040", trialBody("widgets-workspace", trialSeats))
	if third.Code != http.StatusOK {
		t.Errorf("an ask from another domain answered %d (%s), want 200: the bucket is the domain's and not the route's", third.Code, third.Body.String())
	}
	if minted := f.mintedKeys(); len(minted) != 2 {
		t.Errorf("the asks minted %d key(s), want the two that were served", len(minted))
	}
}

// TestTrialIsServedWhenItsOwnClaimWentUnanswered: an error is not a claim that
// did not land, and the store's answer alone cannot tell the two apart — the
// deployed store re-runs its transaction when its result is lost and then reads
// its own committed documents, so it answers "already claimed" about the ask
// that claimed it. Withdrawing there destroys the ask's own trial: the store
// holds a trial naming a revoked key, the account is mailed nothing, and the
// account and the installation are barred for good, because no method of the
// Store interface repairs one. The key is what tells them apart, and the ask is
// served: the key is mailed, and the trial, the issuance and the state are the
// ones the served key reads back as.
func TestTrialIsServedWhenItsOwnClaimWentUnanswered(t *testing.T) {
	f := trialAs(t)
	f.deps.Store = unansweredClaim{Store: f.store}
	f.start()
	rec := f.ask(trialBody(workspaceID, trialSeats))
	if rec.Code != http.StatusOK {
		t.Fatalf("the ask answered %d (%s), want 200: its own claim landed and only its answer was lost", rec.Code, rec.Body.String())
	}
	minted := f.mintedKeys()
	if len(minted) != 1 {
		t.Fatalf("the ask minted %d key(s), want one", len(minted))
	}
	claims, _, err := licence.Verify([]byte(minted[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the served key does not verify: %v", err)
	}
	if len(f.mail.sent) != 1 || !strings.Contains(f.mail.sent[0].Body, minted[0]) {
		t.Fatalf("the ask mailed %d message(s) and the key is not in them, so the account that claimed the trial was handed nothing: %+v", len(f.mail.sent), f.mail.sent)
	}
	trials, err := f.store.Trials(t.Context())
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 1 || trials[0].KeyID != claims.KeyID {
		t.Fatalf("the store holds %+v, want the one trial naming the key this ask minted (%s)", trials, claims.KeyID)
	}
	if withdrawn := f.revocations(); len(withdrawn) != 0 {
		t.Errorf("the ask withdrew %d key(s), want none: the trial it landed names one of them, so the revocation would bar the account it was minted for (%+v)", len(withdrawn), withdrawn)
	}
	if records := f.records(); len(records) != 1 || records[0].KeyID != claims.KeyID {
		t.Errorf("the store recorded %+v, want the issuance of the key it served", records)
	}
	if held, known, err := f.store.KeyState(t.Context(), claims.KeyID); err != nil || !known {
		t.Errorf("the served key has no state (known=%v err=%v), so nothing can check it in (§8)", known, err)
	} else if held.WorkspaceID != workspaceID {
		t.Errorf("the key's state names workspace %q, want %q", held.WorkspaceID, workspaceID)
	}
}

// TestTrialIsNotWithdrawnWhenTheStoreCannotSay: when the claim fails and the
// store cannot be read either, nothing says whether the claim landed — and the
// two ways of being wrong are not equal. A key left where the mint put it is
// held by nobody (the mail never went out) and is what an operator reads off the
// trial if one landed; a trial withdrawn on a guess is destroyed, and the
// account and the installation are barred with no way back. Nothing is
// withdrawn, and the ask is answered a failure.
func TestTrialIsNotWithdrawnWhenTheStoreCannotSay(t *testing.T) {
	f := trialAs(t)
	f.deps.Store = &blindAfterAClaim{Store: f.store}
	f.start()
	rec := f.ask(trialBody(workspaceID, trialSeats))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("the ask answered %d (%s), want 500: the claim failed and the trials could not be read", rec.Code, rec.Body.String())
	}
	if minted := f.mintedKeys(); len(minted) != 1 {
		t.Fatalf("the ask minted %d key(s), want the one whose fate the store could not say", len(minted))
	}
	if withdrawn := f.revocations(); len(withdrawn) != 0 {
		t.Errorf("the ask withdrew %d key(s) on a claim it could not read, want none: withdrawing a trial that landed bars the account and the installation for good (%+v)", len(withdrawn), withdrawn)
	}
	if trials, err := f.store.Trials(t.Context()); err != nil || len(trials) != 0 {
		t.Errorf("the store holds %+v (err=%v), want no trial: this claim did not land", trials, err)
	}
}

// TestTrialIsWithdrawnWhenTheClaimCannotBeWritten: §14 counts a trial in the
// store's check-and-write, and a mint stands before it. A claim the store
// refuses — the lost race, or a store that could not be written — leaves the key
// the mint answered recorded nowhere, and the store says so here: the trials
// read back name no such key, so the claim did not land, and the key is
// withdrawn rather than left live, which is what puts a key nobody holds into
// §8's list where the licensor can see it (K2.2).
func TestTrialIsWithdrawnWhenTheClaimCannotBeWritten(t *testing.T) {
	f := trialAs(t)
	f.deps.Store = claimRefuser{Store: f.store, err: errors.New("the store could not be written")}
	f.start()
	rec := f.ask(trialBody(workspaceID, trialSeats))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("the ask answered %d (%s), want 500: the claim could not be written", rec.Code, rec.Body.String())
	}
	minted := f.mintedKeys()
	if len(minted) != 1 {
		t.Fatalf("the ask minted %d key(s), want the one it then had to withdraw", len(minted))
	}
	claims, _, err := licence.Verify([]byte(minted[0]), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the minted key does not verify: %v", err)
	}
	withdrawn := f.revocations()
	if len(withdrawn) != 1 {
		t.Fatalf("the store holds %d revocation(s), want the one key this ask minted and could not record: %+v", len(withdrawn), withdrawn)
	}
	if withdrawn[0].KeyID != claims.KeyID {
		t.Errorf("the withdrawn key is %s, want the key this ask minted (%s)", withdrawn[0].KeyID, claims.KeyID)
	}
	if withdrawn[0].Reason != ReasonUnrecorded {
		t.Errorf("the key was withdrawn as %q, want %q: nothing holds it and nothing recorded it", withdrawn[0].Reason, ReasonUnrecorded)
	}
}
