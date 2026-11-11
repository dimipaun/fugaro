// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The manual queue (subscriptions plan S3.4; the design
// docs/design/key-signing-service.md §4.5, §6 and Check 173). It is the surface
// the Terms give the requests the checks cannot decide, so what is held here is
// that a queued request reaches the owner, that the owner's own text is data on
// the page's render, and that one click mints once however many times it is
// submitted.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestQueueNotifiesTheOwner: §4.5 — a queued request notifies the owner, so the
// queue is seen. Every route that queues notifies: the manual form, the
// free-mail domain and the second key for a domain all leave a request a person
// has to answer.
//
// The notification is a seam of its own and not the applicant's mail path. That
// is what makes it compatible with §7.2's rule rather than an exception to it:
// the manual form mails the applicant nothing at all
// (TestApplicantFieldsCannotInjectMailHeaders), and what it does is tell the
// owner, whose address is not the applicant's.
func TestQueueNotifiesTheOwner(t *testing.T) {
	f := newFixture(t)
	hostile := "a seminar <script>alert(1)</script>\r\nBcc: victim@elsewhere.example"
	f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {hostile}})

	// The free-mail route queues too, and for the same reason: an address at a
	// provider is a request a person looks at and not a refusal (§4.1).
	f.apply("maintainer@gmail.example")

	alerts := f.notifier.alerts
	if len(alerts) != 2 {
		t.Fatalf("two queued requests notified the owner %d time(s), want two", len(alerts))
	}
	for i, alert := range alerts {
		if alert.Subject != queueSubject {
			t.Errorf("notification %d's subject is %q, want the service's own (%q)", i, alert.Subject, queueSubject)
		}
		// Nothing an applicant typed is a header (§7.2 read at the owner's door
		// as well as the applicant's): the subject is the service's constant and
		// the text is body text.
		if strings.Contains(alert.Subject, "seminar") || strings.Contains(alert.Subject, "Bcc") {
			t.Errorf("notification %d carries the applicant's text in its subject: %q", i, alert.Subject)
		}
		if !strings.Contains(alert.Body, RouteAdmin) {
			t.Errorf("notification %d does not say where the queue is: %q", i, alert.Body)
		}
	}
	if !strings.Contains(alerts[0].Body, hostile) {
		t.Errorf("the notification does not carry what the applicant wrote: %q", alerts[0].Body)
	}
	if !strings.Contains(alerts[1].Body, "maintainer@gmail.example") {
		t.Errorf("the free-mail queue's notification does not name the address: %q", alerts[1].Body)
	}

	// Nothing was mailed to either applicant: a queued request is not an
	// application that failed, and the uniform answer it already has is the
	// whole of what it is told (§7.1).
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("the two queued requests mailed the applicant %d message(s), want none", got)
	}
}

// TestApprovalMintsOnce: §4.5 and Check 173 step 3 — the owner approves a
// request, and **an approval mints once**: the idempotency of §5.2 applied to a
// person's click. The replay is answered as the first submission was, and what
// it must not do is mint a second key or write a second audit line.
func TestApprovalMintsOnce(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial for a small team"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}
	id := open[0].ID

	form := f.mintForm(url.Values{
		"application":  {id},
		"plan":         {paidPlanName},
		"seats":        {"5"},
		"term_days":    {"365"},
		"organisation": {"acme.example"},
	})
	if rec := f.adminPost(RouteAdminMint, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("the approval answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 1 {
		t.Fatalf("the approval minted %d key(s), want one", got)
	}
	// The key went to the applicant the request was about, and nowhere else.
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the approval sent %d message(s), want the key's one", got)
	}
	if f.mail.sent[0].To != applicantAddress {
		t.Errorf("the key went to %q, want the address the request was about", f.mail.sent[0].To)
	}

	// The replay: the same click, submitted again.
	if rec := f.adminPost(RouteAdminMint, form); rec.Code != http.StatusSeeOther {
		t.Errorf("the replay answered %d, want the same 303 the first submission got: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 1 {
		t.Errorf("the replay minted %d key(s) in all, want the first one alone", got)
	}
	if got := len(f.auditor.lines); got != 1 {
		t.Errorf("the replay wrote %d audit line(s) in all, want one", got)
	}
	if got := len(f.mail.sent); got != 1 {
		t.Errorf("the replay sent %d message(s) in all, want the key's one", got)
	}
	records := f.records()
	if len(records) != 1 {
		t.Fatalf("the store holds %d record(s), want one", len(records))
	}
	if want := sourceApproval + id; records[0].Source != want {
		t.Errorf("the record's source is %q, want %q: the approval's identity is the request it answered", records[0].Source, want)
	}
	if records[0].Seats != 5 || records[0].Organisation != "acme.example" {
		t.Errorf("the record is %d seat(s) for %q, want the five the owner minted for acme.example", records[0].Seats, records[0].Organisation)
	}
	// The approval also wrote the decision, so the request is out of the queue
	// and off the page.
	if got := len(f.open()); got != 0 {
		t.Errorf("the approved request is still in the queue: %+v", f.open())
	}
	if body := f.adminGet().Body.String(); !strings.Contains(body, "Nothing is waiting.") {
		t.Error("the page still shows a request in the queue after the approval")
	}
}

// TestQueueShowsWhatTheOwnerDecided: the queue is the requests nobody has
// answered, and declining one takes it out of the queue without minting
// anything — a decline is not a failed mint, and nothing about it reaches the
// applicant, whose answer is the uniform one it already had (§7.1).
func TestQueueShowsWhatTheOwnerDecided(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"an academic licence"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}
	id := open[0].ID
	if rec := f.adminPost(RouteAdminDecide, f.decideForm(id)); rec.Code != http.StatusSeeOther {
		t.Fatalf("the decline answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 0 {
		t.Errorf("declining minted %d key(s), want none", got)
	}
	if got := len(f.auditor.lines); got != 0 {
		t.Errorf("declining wrote %d audit line(s), want none: nothing was minted", got)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("declining mailed %d message(s), want none", got)
	}
	if got := len(f.open()); got != 0 {
		t.Errorf("the declined request is still in the queue: %+v", f.open())
	}
	// And it does not come back through the mint form: a request the owner has
	// answered is not one a second click, or a replayed one, can mint.
	form := f.mintForm(url.Values{"application": {id}, "plan": {paidPlanName}, "seats": {"1"}, "term_days": {"30"}, "organisation": {"acme.example"}})
	if rec := f.adminPost(RouteAdminMint, form); rec.Code == http.StatusSeeOther {
		t.Errorf("the declined request was minted for after all: %s", rec.Body.String())
	}
	if got := len(f.recording.keys); got != 0 {
		t.Errorf("minting a declined request minted %d key(s), want none", got)
	}
}
