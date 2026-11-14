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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		f.deps.SignIn = &signInSpy{caller: Caller{
			UID: "uid-trial", Email: trialAccount, Provider: "google.com", AuthTime: f.now,
		}}
	})
}

// ask is one trial request: the route's own body, carrying the token the
// sign-in rides in.
func (f *fixture) ask(body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteTrial, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+trialTicket)
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
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
	f.deps.SignIn = &signInSpy{caller: Caller{UID: "uid-other", Email: "other@acme.example", Provider: "google.com", AuthTime: f.now}}
	f.start()
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
