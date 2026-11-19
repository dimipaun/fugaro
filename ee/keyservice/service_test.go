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
// Each path is driven with both methods — the one it used to serve and the one
// it did not, because a path this table no longer holds answers neither — from
// an applicant's own address and with a body the live route would have acted on,
// and then the seams are read: an answer of "there is no such route" that mailed
// the address, told the owner, queued an application, asked the minter to sign
// or recorded an issuance is not a retired route.
//
// The answer must be the mux's own 404, and a 405 is a failure rather than the
// other refusal that fits. On a path no pattern names, every method is answered
// by the mux's not-found, so a 405 means a pattern for this path does exist —
// for some other method — and the door is back. Accepting it would leave the
// guard blind to a re-registration on any verb these two do not drive: a
// `PUT /admin` answers both of them with 405, and the retired path would pass.
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
		what string
		path string
		form url.Values
	}{
		// S3.3's free-seat form: one address, and the seat was granted on the
		// strength of it.
		{
			what: "the free-seat form",
			path: "/free-seat",
			form: url.Values{"email": {"applicant@acme.example"}},
		},
		// §4.4's open-source form: the maintainer's address and the repositories
		// the check reads through the deploy's own code-host credentials.
		{
			what: "the open-source form",
			path: "/open-source",
			form: url.Values{
				"email":        {"maintainer@acme.example"},
				"repositories": {"https://code.example/acme/widget"},
			},
		},
		// §4.5's manual request: what an applicant asks for, in their own words.
		{
			what: "the manual request form",
			path: "/request",
			form: url.Values{"email": {"prof@academic.example"}, "request": {"a multi-seat trial"}},
		},
		// §4.1's verification link, followed from the mail that carried it.
		{
			what: "the verification link",
			path: "/verify",
			form: url.Values{"token": {"the-token-the-mail-would-have-carried"}},
		},
		// S3.4's admin page: the surface itself, one path of the subtree under
		// it (its handler matched the prefix and refused inside itself, so both
		// halves answered), the mint and the decision.
		{what: "the admin page", path: "/admin"},
		{what: "a path under the admin page", path: "/admin/queue"},
		{
			what: "the admin mint door",
			path: "/admin/mint",
			form: url.Values{"code": {"000000"}},
		},
		{
			what: "the admin decision door",
			path: "/admin/decide",
			form: url.Values{"code": {"000000"}, "decision": {"approved"}},
		},
	} {
		// Both methods, and not only the one the route used to serve: what
		// retired is the path, so no method answers it, and a case that drove
		// the retired method alone would leave the other free for a later task
		// to register — a `GET /free-seat` is as much a door as the `POST` was,
		// and a body is read the same way whichever verb carried it.
		//
		// The two are a probe and not the list of verbs a door could come back
		// on: the 404 below is what covers the rest, because a path registered
		// for a method neither of these drives still refuses both of them with a
		// 405.
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			was := count()
			var rec *httptest.ResponseRecorder
			if method == http.MethodGet {
				rec = f.get(tc.path, tc.form)
			} else {
				rec = f.post(tc.path, tc.form)
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf(
					"%s (%s %s) answered %d: the route went with the site's forms and the admin page (portal plan P9.3), so this path serves no door — the mux's own 404 is the answer, and anything else is a door a stranger reaches. A 405 is one too: it is what a pattern registered for another method answers, so it means this path is back in the table",
					tc.what, method, tc.path, rec.Code,
				)
			}
			if now := count(); now != was {
				t.Errorf(
					"%s (%s %s) answered %d and moved a seam: %+v became %+v — a retired route that still mails, notifies, queues, signs, dials or records has not retired",
					tc.what, method, tc.path, rec.Code, was, now,
				)
			}
		}
	}
}
