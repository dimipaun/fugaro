// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The manual queue (subscriptions plan S3.4; the design
// docs/design/key-signing-service.md §4.5, §6 and Check 173). It is the surface
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

// TestAnUnreadablePortalOriginLeavesNoQueueEntry: the notification is what makes
// a queued request a request — an entry written and never mentioned is one
// nobody will look at (§4.5) — so a deployment whose portal origin cannot be
// read from its intake's refuses the ask rather than writing the entry. The
// applicant's own answer is the uniform failure; what is held here is that
// nothing was left behind for a person to find, and that a second attempt leaves
// nothing either: an entry per retry is a queue that grows unannounced.
func TestAnUnreadablePortalOriginLeavesNoQueueEntry(t *testing.T) {
	// A deployment whose configured origin is not `keys.` plus the site's host,
	// which is the one shape portalOrigin reads the portal's origin from: its
	// owner cannot be sent a link to the portal's own page for the queue (P5.1).
	f := newFixtureWith(t, Production, func(b *fixture) { b.cfg.URL = "https://intake.fugaro.dev" })
	f.actsAs(danaUID, danaEmail, signInGoogle)

	for attempt := 1; attempt <= 2; attempt++ {
		rec := f.askOpenSource(openSourceAsk(t, repositoryURL))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("ask %d from a deployment whose portal origin cannot be read answered %d (%s), want 500",
				attempt, rec.Code, rec.Body)
		}
		if held := f.queued(); len(held) != 0 {
			t.Fatalf("ask %d was written into the queue anyway: the owner is told nothing about it, so it is a request nobody reads (§4.5): %+v",
				attempt, held)
		}
	}

	if len(f.notifier.alerts) != 0 {
		t.Errorf("the refused ask notified the owner %d time(s), want none", len(f.notifier.alerts))
	}
	if len(f.mail.sent) != 0 {
		t.Error("the refused ask mailed the applicant")
	}
	if len(f.records()) != 0 {
		t.Errorf("the refused ask recorded %d issuance(s), want none", len(f.records()))
	}
}
