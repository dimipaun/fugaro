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
