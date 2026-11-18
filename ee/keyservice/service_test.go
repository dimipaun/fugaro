// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The routes P9.3 retired, held to their absence. "Gone" is a claim about
// Handler's table rather than about the files that were deleted: a later task
// that registered one of these paths again — the admin page served again, a free
// seat minted from a stranger's POST — would put a door back that no other case
// in this package reads, because every other case drives a door that is supposed
// to answer.
//
// Each path is driven with the method it used to serve, from an applicant's own
// address and with a body the live route would have acted on, and then the seams
// are read: an answer of "there is no such route" that mailed the address, told
// the owner, queued an application, asked the minter to sign or recorded an
// issuance is not a retired route.
//
// The paths are written out rather than named by a constant. The constants went
// with the routes, and what this file holds is the addresses themselves — the
// ones a cached form or a bookmarked link can still reach — rather than a name
// that may or may not exist beside them.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRetiredRoutesAreGone(t *testing.T) {
	f := newFixture(t)

	// What a live route left behind, counted so a case can read that a request
	// moved none of it. The issuance record is read beside the four the task
	// names, because a key issued is the outcome the others exist to produce, and
	// the code host is read because §4.4's check dialled it on the applicant's
	// own line before anything else about the application happened.
	type seams struct {
		mail, notified, queued, signed, issued, dialled int
	}
	count := func() seams {
		return seams{
			mail:     len(f.mail.sent),
			notified: len(f.notifier.alerts),
			queued:   len(f.queued()),
			signed:   f.signer.calls,
			issued:   len(f.records()),
			dialled:  len(f.host.asked),
		}
	}

	for _, tc := range []struct {
		what   string
		method string
		path   string
		form   url.Values
	}{
		// S3.3's free-seat form: one address, and the seat was granted on the
		// strength of it.
		{
			what:   "the free-seat form",
			method: http.MethodPost,
			path:   "/free-seat",
			form:   url.Values{"email": {"applicant@acme.example"}},
		},
		// §4.4's open-source form: the maintainer's address and the repositories
		// the check reads through the deploy's own code-host credentials.
		{
			what:   "the open-source form",
			method: http.MethodPost,
			path:   "/open-source",
			form: url.Values{
				"email":        {"maintainer@acme.example"},
				"repositories": {"https://code.example/acme/widget"},
			},
		},
		// §4.5's manual request: what an applicant asks for, in their own words.
		{
			what:   "the manual request form",
			method: http.MethodPost,
			path:   "/request",
			form:   url.Values{"email": {"prof@academic.example"}, "request": {"a multi-seat trial"}},
		},
		// §4.1's verification link, followed from the mail that carried it.
		{
			what:   "the verification link",
			method: http.MethodGet,
			path:   "/verify",
			form:   url.Values{"token": {"the-token-the-mail-would-have-carried"}},
		},
		// S3.4's admin page: the surface itself, one path of the subtree under
		// it (its handler matched the prefix and refused inside itself, so both
		// halves answered), the mint and the decision.
		{what: "the admin page", method: http.MethodGet, path: "/admin"},
		{what: "a path under the admin page", method: http.MethodGet, path: "/admin/queue"},
		{
			what:   "the admin mint door",
			method: http.MethodPost,
			path:   "/admin/mint",
			form:   url.Values{"code": {"000000"}},
		},
		{
			what:   "the admin decision door",
			method: http.MethodPost,
			path:   "/admin/decide",
			form:   url.Values{"code": {"000000"}, "decision": {"approved"}},
		},
	} {
		was := count()
		var rec *httptest.ResponseRecorder
		if tc.method == http.MethodGet {
			rec = f.get(tc.path, tc.form)
		} else {
			rec = f.post(tc.path, tc.form)
		}
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf(
				"%s (%s %s) answered %d: the route went with the site's forms and the admin page (portal plan P9.3), so this path serves no door — the mux's own 404, or its 405 where a pattern matches another method, is the answer, and anything else is a door a stranger reaches",
				tc.what, tc.method, tc.path, rec.Code,
			)
		}
		if now := count(); now != was {
			t.Errorf(
				"%s (%s %s) answered %d and moved a seam: %+v became %+v — a retired route that still mails, notifies, queues, signs, dials or records has not retired",
				tc.what, tc.method, tc.path, rec.Code, was, now,
			)
		}
	}
}
