// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// P7.1's tests: the daily run's two decisions, the record that makes a reminder
// once per term, and the door only the job may call.
//
// The state of a key is a bound entitlement and the state of a term is its end,
// so what these cases stand up is records in the store: they seed entitlements
// the way a grant and a binding leave one (P3.1) rather than binding through
// bindEntitlement, because everything a reminder reads is that record — the run
// never reaches the key, the workspace or the installation, which is what a
// binding's probe is about.
//
// The token the job presents is the fixture's own scheduler seam except where a
// case is about the door itself, and there it is a real OIDC token signed by the
// key P1.3's tests generate (portalauth_test.go): no fixture in this repository
// holds a usable key (.claude/rules/ee.md, R5).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// reminderJobToken is what these cases present as the bearer token to a fixture
// whose scheduler seam is a spy. It is a placeholder and obviously not a token:
// what the spy answers is the account, and the door reads no more of it.
const reminderJobToken = "fake-scheduler-token-0123456789"

// apiAsJob posts one request to a route the way Cloud Scheduler does: JSON, from
// its own address, with the OIDC token in the Authorization header and no body.
func (f *fixture) apiAsJob(path, token string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// runReminders posts one reminder run the way the deployment's job does.
func (f *fixture) runReminders() *httptest.ResponseRecorder {
	f.t.Helper()
	return f.apiAsJob(apiRemindersPath, reminderJobToken)
}

// reminderEntitlement writes one entitlement into the store in the state a case
// names and answers it. Bound is what minting a key puts on the record (P3.1's
// binding): a term with a key an installation is running on, which is the only
// kind a renewal reminder is about.
func (f *fixture) reminderEntitlement(id, organisationID, source, kind string, endsAt time.Time, bound bool) WireEntitlement {
	f.t.Helper()
	held := WireEntitlement{
		ID:             id,
		OrganisationID: organisationID,
		Source:         source,
		Row:            PolicyTeamAnnual,
		Tier:           licence.TierTeam,
		Kind:           kind,
		Seats:          3,
		StartsAt:       endsAt.Add(-yearTerm),
		EndsAt:         endsAt,
		Status:         entitlementActive,
		CreatedAt:      endsAt.Add(-yearTerm),
	}
	if bound {
		at := held.StartsAt
		held.BoundAt = &at
	}
	f.seed(entitlementsCollection, held.ID, held)
	return held
}

// reminderRecords is every reminder the store says was sent, which is what a
// case reads "once per term" from rather than counting the mails alone.
func (f *fixture) reminderRecords() []reminderRecord {
	f.t.Helper()
	held, err := recordsWhere[reminderRecord](context.Background(), f.docs, remindersCollection, Where{})
	if err != nil {
		f.t.Fatalf("reading the reminders: %v", err)
	}
	return held
}

// TestReminderIsSentOncePerTerm is the task's first case: a term nothing renews
// runs out, its owners are mailed before it does, and the run does not mail
// again about the same term however often it fires. The next term is a term
// nobody has been told about yet, and the reminder goes out again.
func TestReminderIsSentOncePerTerm(t *testing.T) {
	f := newFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	held := f.reminderEntitlement("ent-grant", org.ID, entitlementSourceOperations, licence.KindPaid, f.now.Add(3*day), true)

	if got := f.runReminders(); got.Code != http.StatusNoContent {
		t.Fatalf("the reminder run answered %d, want %d: %s", got.Code, http.StatusNoContent, got.Body)
	}
	if len(f.mail.sent) != 1 {
		t.Fatalf("the run mailed %d message(s), want the one renewal reminder: %v", len(f.mail.sent), f.mail.sent)
	}
	sent := f.mail.sent[0]
	if sent.To != danaEmail {
		t.Errorf("the reminder went to %q, want the organisation's owner %q", sent.To, danaEmail)
	}
	if sent.Subject != renewalReminderSubject {
		t.Errorf("the reminder's subject is %q, want the renewal one %q", sent.Subject, renewalReminderSubject)
	}
	if !strings.Contains(sent.Body, held.EndsAt.UTC().Format("2 January 2006")) {
		t.Errorf("the reminder does not say when the term ends:\n%s", sent.Body)
	}
	if !strings.Contains(sent.Body, "https://app.fugaro.example"+routeWorkspaces) {
		t.Errorf("the reminder does not lead to the portal's own screen:\n%s", sent.Body)
	}

	// The job fires again the next day, and a term that has been reminded about
	// is not reminded about again.
	f.advance(day)
	if got := f.runReminders(); got.Code != http.StatusNoContent {
		t.Fatalf("the second run answered %d, want %d", got.Code, http.StatusNoContent)
	}
	if len(f.mail.sent) != 1 {
		t.Errorf("a second run of the same term mailed %d message(s), want none", len(f.mail.sent)-1)
	}
	if len(f.reminderRecords()) != 1 {
		t.Errorf("the store holds %d reminder(s), want the one that was sent", len(f.reminderRecords()))
	}

	// The next term is granted, and it is a term nobody has been told about:
	// the record that stopped the second run names the term that ended.
	next := held
	next.EndsAt = f.now.Add(3 * day)
	f.seed(entitlementsCollection, next.ID, next)
	if got := f.runReminders(); got.Code != http.StatusNoContent {
		t.Fatalf("the run of the next term answered %d, want %d", got.Code, http.StatusNoContent)
	}
	if len(f.mail.sent) != 2 {
		t.Fatalf("the run of the next term mailed %d message(s) in all, want the two", len(f.mail.sent))
	}
	if got := f.mail.sent[1]; !strings.Contains(got.Body, next.EndsAt.UTC().Format("2 January 2006")) {
		t.Errorf("the second term's reminder is about the first term's end:\n%s", got.Body)
	}
}

// TestPaddleRenewedKeysGetNoReminder: a purchase is renewed at the merchant of
// record (P4.1, P4.2), so its key is not one that runs out on its own day and
// nobody has to be told about a date Paddle already handles. A purchase inside
// the lead mails nothing and is not recorded, so a run that never needed to mail
// leaves nothing behind it.
func TestPaddleRenewedKeysGetNoReminder(t *testing.T) {
	f := newFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	f.reminderEntitlement("ent-bought", org.ID, entitlementSourcePurchase, licence.KindPaid, f.now.Add(day), true)

	if got := f.runReminders(); got.Code != http.StatusNoContent {
		t.Fatalf("the reminder run answered %d, want %d: %s", got.Code, http.StatusNoContent, got.Body)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("the run mailed %d message(s) about a Paddle-renewed key, want none: %v", len(f.mail.sent), f.mail.sent)
	}
	if len(f.reminderRecords()) != 0 {
		t.Errorf("the run recorded %d reminder(s) it did not send", len(f.reminderRecords()))
	}
}

// TestUnboundEntitlementIsReminded: a paid entitlement nobody bound is a term
// running down unused (D2197 runs it from payment), and the reminder is the one
// mail an entitlement with no key is sent. It carries no key — the store holds
// none and the mail is a link (§7.3) — and it is sent once.
func TestUnboundEntitlementIsReminded(t *testing.T) {
	f := newFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	held := f.reminderEntitlement("ent-unbound", org.ID, entitlementSourcePurchase, licence.KindPaid, f.now.Add(yearTerm), false)

	if got := f.runReminders(); got.Code != http.StatusNoContent {
		t.Fatalf("the reminder run answered %d, want %d: %s", got.Code, http.StatusNoContent, got.Body)
	}
	if len(f.mail.sent) != 1 {
		t.Fatalf("the run mailed %d message(s), want the one binding reminder: %v", len(f.mail.sent), f.mail.sent)
	}
	sent := f.mail.sent[0]
	if sent.Subject != bindingReminderSubject {
		t.Errorf("the reminder's subject is %q, want the binding one %q", sent.Subject, bindingReminderSubject)
	}
	if !strings.Contains(sent.Body, "not bound") {
		t.Errorf("the reminder does not say the licence is unbound:\n%s", sent.Body)
	}
	if !strings.Contains(sent.Body, held.EndsAt.UTC().Format("2 January 2006")) {
		t.Errorf("the reminder does not say when the paid term ends:\n%s", sent.Body)
	}

	// An unbound entitlement is reminded about once, like a term: the record
	// names it, so tomorrow's run says nothing.
	f.advance(day)
	f.runReminders()
	if len(f.mail.sent) != 1 {
		t.Errorf("a second run mailed %d message(s) about the same unbound licence, want none", len(f.mail.sent)-1)
	}
}

// TestReminderIsMailedToEveryOwner: the reminder is the organisation's owners'
// and every one of them is told, because the owner who buys and the owner who
// binds are not always the same person. An admin who may bind (D2186) is not an
// owner and is not mailed: the plan's word for this mail is owners.
func TestReminderIsMailedToEveryOwner(t *testing.T) {
	f := newFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	f.seed(membershipsCollection, membershipID(org.ID, erinUID), WireMembership{
		OrganisationID: org.ID, UID: erinUID, Email: erinEmail, Role: roleOwner, CreatedAt: f.now,
	})
	f.seed(membershipsCollection, membershipID(org.ID, sellerUID), WireMembership{
		OrganisationID: org.ID, UID: sellerUID, Email: sellerMail, Role: roleAdmin, CreatedAt: f.now,
	})
	f.reminderEntitlement("ent-grant", org.ID, entitlementSourceOperations, licence.KindPaid, f.now.Add(day), true)

	if got := f.runReminders(); got.Code != http.StatusNoContent {
		t.Fatalf("the reminder run answered %d, want %d: %s", got.Code, http.StatusNoContent, got.Body)
	}
	told := make([]string, 0, len(f.mail.sent))
	for _, msg := range f.mail.sent {
		told = append(told, msg.To)
	}
	if len(told) != 2 || !strings.Contains(strings.Join(told, " "), danaEmail) || !strings.Contains(strings.Join(told, " "), erinEmail) {
		t.Errorf("the run mailed %v, want one message to each of %s and %s", told, danaEmail, erinEmail)
	}
	for _, msg := range f.mail.sent {
		if msg.To == sellerMail {
			t.Errorf("the run mailed the organisation's admin, want its owners alone: %v", told)
		}
	}
}

// TestReminderIsNotSentForWhatRenewsItself: the free seat is the one grant that
// needs nobody (D2184 gives it while the organisation exists; D2203 ends it only
// when a paid grant supersedes it there), so a year-old seat inside its lead is
// a licence that is not going anywhere and the run says nothing about it.
func TestReminderIsNotSentForWhatRenewsItself(t *testing.T) {
	f := newFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	f.reminderEntitlement("ent-seat", org.ID, entitlementSourceFreeSeat, licence.KindComped, f.now.Add(day), true)

	if got := f.runReminders(); got.Code != http.StatusNoContent {
		t.Fatalf("the reminder run answered %d, want %d: %s", got.Code, http.StatusNoContent, got.Body)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("the run mailed %d message(s) about the free seat, want none: %v", len(f.mail.sent), f.mail.sent)
	}
}

// refusingMailer is the mail path with one address the provider will not take,
// which is what a suppressed or dead address is from the run's side: the error
// is the shape resend.go builds out of a provider's refusal, and every other
// address goes to the fixture's own spy.
type refusingMailer struct {
	spy     *mailSpy
	refused string
}

func (m refusingMailer) Send(ctx context.Context, msg Message) error {
	if msg.To == m.refused {
		return errors.New("keyservice: the mail provider refused the message with 422 Unprocessable Entity: the recipient's address is not accepted")
	}
	return m.spy.Send(ctx, msg)
}

// TestARefusedAddressRemindsEveryoneElseOnce: one owner the provider will not
// accept is that owner's failure and nobody else's. The owner beside them is
// mailed in the same run, the next organisation's reminder is not stuck behind
// the refused address — the store's order is stable, so "behind it" would be
// every run until somebody fixed that address by hand — and nobody is mailed
// twice however often the job asks again, because each owner is recorded as they
// are mailed and the second day's runs find nothing to send them.
//
// The run still answers a failure, and that is the point of it: a reminder the
// service was asked to send has not gone out, and the retry F33 gives the job is
// what asks for it again. Answering 204 would say that everyone was told.
func TestARefusedAddressRemindsEveryoneElseOnce(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.deps.Mailer = refusingMailer{spy: f.mail, refused: danaEmail}
	})
	first := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	f.seed(membershipsCollection, membershipID(first.ID, erinUID), WireMembership{
		OrganisationID: first.ID, UID: erinUID, Email: erinEmail, Role: roleOwner, CreatedAt: f.now,
	})
	second := f.seededOrganisation("org-seller", "Seller Ltd", orgTypeCustomer, sellerUID, sellerMail, roleOwner)
	f.reminderEntitlement("ent-dana", first.ID, entitlementSourceOperations, licence.KindPaid, f.now.Add(day), true)
	f.reminderEntitlement("ent-seller", second.ID, entitlementSourceOperations, licence.KindPaid, f.now.Add(day), true)

	for run := 1; run <= 3; run++ {
		if got := f.runReminders(); got.Code != http.StatusInternalServerError {
			t.Fatalf("run %d answered %d, want %d: a reminder that did not go out is a run that failed", run, got.Code, http.StatusInternalServerError)
		}
		f.advance(day)
	}

	told := map[string]int{}
	for _, msg := range f.mail.sent {
		told[msg.To]++
	}
	if told[danaEmail] != 0 {
		t.Errorf("the refused address was mailed %d time(s), want none: the provider does not accept it", told[danaEmail])
	}
	if told[erinEmail] != 1 {
		t.Errorf("the owner beside the refused one was mailed %d time(s), want once: her reminder does not depend on the other address", told[erinEmail])
	}
	if told[sellerMail] != 1 {
		t.Errorf("the next organisation's owner was mailed %d time(s), want once: a refused address does not hold up the reminders filed after it", told[sellerMail])
	}
}

// jobVerifier is the adapter between P1.3's SchedulerVerifier and the seam a
// route reaches: the verifier answers whether a token is the configured job's,
// and the seam answers which account that job runs as, which is the account the
// verifier was built with — so the adapter states it beside the verifier rather
// than reading it back out of a token it has just checked. A deployment builds
// this pair from two config values (P1.7) and nothing in this tree does, which
// is why the case below builds it here.
type jobVerifier struct {
	verifier *SchedulerVerifier
	account  string
}

func (v jobVerifier) Verify(ctx context.Context, bearer string) (string, error) {
	if err := v.verifier.Verify(ctx, bearer); err != nil {
		return "", err
	}
	return v.account, nil
}

// TestReminderEndpointRefusesOtherCallers: the door reads the Scheduler's own
// token and nothing else (F33). The job's token is accepted, and every other
// caller is refused with the API's own refusal: no token at all, a token minted
// for another audience, a token signed for another account, and the SPA's own
// sign-in — which is a token this route never puts to the sign-in verifier,
// because a customer's door and a job's are not one door.
func TestReminderEndpointRefusesOtherCallers(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		// The real verifier, over the key P1.3's tests generate: what is refused
		// below is refused by the check a deployment runs, and not by a spy.
		f.deps.Scheduler = jobVerifier{verifier: schedulerVerifier(t), account: portalSchedulerAccount}
	})
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	f.reminderEntitlement("ent-grant", org.ID, entitlementSourceOperations, licence.KindPaid, f.now.Add(day), true)

	otherAudience := schedulerClaims()
	otherAudience["aud"] = portalOtherAudience
	otherAccount := schedulerClaims()
	otherAccount["email"] = portalOtherAccount

	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{name: "the job's own token", token: signPortalToken(t, portalKey(), portalKid, "RS256", schedulerClaims()), want: http.StatusNoContent},
		{name: "no token at all", token: "", want: http.StatusUnauthorized},
		{name: "a token minted for another audience", token: signPortalToken(t, portalKey(), portalKid, "RS256", otherAudience), want: http.StatusUnauthorized},
		{name: "a token signed for another account", token: signPortalToken(t, portalKey(), portalKid, "RS256", otherAccount), want: http.StatusUnauthorized},
		{name: "the SPA's own sign-in", token: portalIDToken, want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.mail.sent)
			got := f.apiAsJob(apiRemindersPath, tc.token)
			if got.Code != tc.want {
				t.Fatalf("the reminder route answered %d with %s, want %d: %s", got.Code, tc.name, tc.want, got.Body)
			}
			if tc.want == http.StatusUnauthorized {
				if code := refusal(f, got); code != "unauthenticated" {
					t.Errorf("the refusal's code is %q, want %q", code, "unauthenticated")
				}
				if len(f.mail.sent) != before {
					t.Errorf("%s mailed %d message(s), want none: a caller this route refuses does not run it", tc.name, len(f.mail.sent)-before)
				}
			}
		})
	}
	if len(f.signIn.tokens) != 0 {
		t.Errorf("the route put %d token(s) to the sign-in verifier, want none: a job's door reads the Scheduler's document and not a customer's sign-in", len(f.signIn.tokens))
	}
}
