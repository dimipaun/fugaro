// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The manual queue (subscriptions plan S3.4; the design
// docs/key-signing-service/spec.md §4.5, §6 and Check 173). It is the surface
// the Terms give the requests the checks cannot decide, so what is held here is
// that a queued request reaches the owner, and that the notification leads to the
// portal's own page for it (P5.1) rather than to a page of this service's.
//
// The queue's decider is the portal's and not this service's since P9.3: the
// admin page retired with its routes, and the screens are the SPA's.

import (
	"net/http"
	"strings"
	"testing"
)

// TestQueueNotifiesTheOwner: §4.5 — a queued request notifies the owner, so the
// queue is seen. The open-source ask is the route that queues since P9.3 (the
// website's three forms retired with their routes, and the free seat with them:
// D2199 grants it as an entitlement from a policy row rather than from an
// application this service reads).
//
// The notification is a seam of its own and not the applicant's mail path. That
// is what makes it compatible with §7.2's rule rather than an exception to it:
// the ask mails the applicant nothing at all
// (TestApplicantFieldsCannotInjectMailHeaders), and what it does is tell the
// owner, whose address is not the applicant's.
func TestQueueNotifiesTheOwner(t *testing.T) {
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.askOpenSource(openSourceAsk(t, repositoryURL)); rec.Code != http.StatusNoContent {
		t.Fatalf("the ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}

	// The link is the portal's own page for the queue, built from the
	// deployment's origin and never from a page this service serves: the screens
	// are the SPA's (P5.1, queue.go's routeOps).
	origin, err := portalOrigin(f.cfg.URL)
	if err != nil {
		t.Fatalf("reading the portal's origin from the deployment's own: %v", err)
	}
	alerts := f.notifier.alerts
	if len(alerts) != 1 {
		t.Fatalf("the queued request notified the owner %d time(s), want one: a queue nobody is told about is a queue nobody reads (§4.5)", len(alerts))
	}
	alert := alerts[0]
	if alert.Subject != queueSubject {
		t.Errorf("the notification's subject is %q, want the service's own (%q)", alert.Subject, queueSubject)
	}
	if want := origin + routeOps; !strings.Contains(alert.Body, want) {
		t.Errorf("the notification does not say where the queue is (%s): %q", want, alert.Body)
	}
	if !strings.Contains(alert.Body, danaEmail) {
		t.Errorf("the notification does not name the address the request is about: %q", alert.Body)
	}
	if !strings.Contains(alert.Body, repositoryURL) {
		t.Errorf("the notification does not name the repository the request covers: %q", alert.Body)
	}

	// Nothing was mailed to the applicant: a queued request is not an
	// application that failed, and the screen's own sentence about a request that
	// is waiting is the whole of what it is told (§7.1, api_opensource.go).
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("the queued request mailed the applicant %d message(s), want none", got)
	}
}

// TestOpenSourceAskStoresNothingWhenItCannotNotify: a deployment whose intake
// origin is not `keys.` plus the site's canonical host has no portal origin to
// read (portalOrigin), and the owner's notification is built from it — so the
// ask cannot be finished. What it must not do is leave the request behind: the
// entry is stored for a person to answer and the person is told where it is, and
// an entry written before that origin is read is one nobody is ever told about
// (queue.go's enqueue). Each retry would then add another, which is a queue
// growing by the requests nobody was told about rather than by the ones waiting
// to be decided (§4.5).
//
// The deployment is the fixture's with one field changed, and the ask is the
// ordinary one every other case drives: what is held is the order of two steps
// inside enqueue, not a route a caller reaches differently.
func TestOpenSourceAskStoresNothingWhenItCannotNotify(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		// An intake that is not `keys.<site>`: the portal's own origin cannot be
		// read from it, and portalOrigin refuses rather than guessing at one.
		f.cfg.URL = "https://intake.example"
	})
	f.actsAs(danaUID, danaEmail, signInGoogle)

	// Twice, because the failure the finding names is cumulative: the first
	// attempt stores one entry and the second stores another, so a case that
	// asked once could not tell a queue that grows from one that never did.
	for attempt := 1; attempt <= 2; attempt++ {
		rec := f.askOpenSource(openSourceAsk(t, repositoryURL))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf(
				"attempt %d answered %d (%s), want 500: the ask cannot be told to the owner, and a failure is what that is",
				attempt, rec.Code, rec.Body.String(),
			)
		}
		if got := len(f.queued()); got != 0 {
			t.Errorf(
				"attempt %d left %d entr(ies) in the queue, want none: a request stored before the owner's notification can be built is one nobody is ever told about, and every retry adds another (§4.5)",
				attempt, got,
			)
		}
		if got := len(f.notifier.alerts); got != 0 {
			t.Errorf("attempt %d notified the owner %d time(s), want none: there is no queue page to lead them to", attempt, got)
		}
	}
}
