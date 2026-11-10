// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The free keys, the queue, and the identity the open-source check reads
// (subscriptions plan S3.2; the design docs/design/key-signing-service.md §4 and
// §7). Every test here drives the intake over the routes the site's own forms
// post to, because the routes and the field names are the two halves' contract
// (S3.3, website/src/pages/keys.astro).

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestFreeMailDomainGoesToTheQueue: an address at a free email provider is not
// refused and not minted — it goes to the queue, because an individual is an
// organisation of one for the free seat (T15) and §4.1 is where that decision
// belongs.
func TestFreeMailDomainGoesToTheQueue(t *testing.T) {
	f := newFixture(t)
	rec := f.apply("maintainer@gmail.example")

	answer := answerBody(t, rec)
	if got := f.signer.calls; got != 0 {
		t.Errorf("a free-mail application signed %d key(s), want none: it is the queue's, not the minter's", got)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("a free-mail application mailed %d message(s), want none: nothing is verified that nothing mints", len(f.mail.sent))
	}
	queued := f.queued()
	if len(queued) != 1 {
		t.Fatalf("the queue holds %d application(s), want one", len(queued))
	}
	if queued[0].Domain != "gmail.example" || queued[0].Email != "maintainer@gmail.example" {
		t.Errorf("the queued application is %+v, want the applicant's own address and domain", queued[0])
	}
	if queued[0].Plan != freePlanName {
		t.Errorf("the queued application names plan %q, want %q", queued[0].Plan, freePlanName)
	}
	if len(answer) == 0 {
		t.Error("the free-mail application was answered with nothing")
	}
}

// TestSecondFreeKeyForADomainGoesToTheQueue: one free seat per organisation,
// proxied by the email domain (§4.2, T15). The second application is not
// silently refused — it is seen, which is what the queue is for.
func TestSecondFreeKeyForADomainGoesToTheQueue(t *testing.T) {
	f := newFixture(t)
	f.apply(applicantAddress)
	f.verify(f.mailedToken())
	if len(f.records()) != 1 {
		t.Fatalf("the first free seat minted %d key(s), want one", len(f.records()))
	}
	mailed := len(f.mail.sent)

	f.apply("other@acme.example")

	if got := len(f.records()); got != 1 {
		t.Errorf("a second application for a domain that holds a free key minted %d key(s), want the one it holds: %d", got, got)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the second application mailed %d more message(s), want none", got-mailed)
	}
	queued := f.queued()
	if len(queued) != 1 {
		t.Fatalf("the queue holds %d application(s), want the second one alone", len(queued))
	}
	if queued[0].Email != "other@acme.example" {
		t.Errorf("the queued application is %q, want the second applicant", queued[0].Email)
	}
}

// TestDomainWithAPaidSubscriptionGetsNoFreeKey: §10.2(a) — a domain the
// service's own event store knows holds a paid subscription is refused, and
// refused rather than queued (§4.3). The knowledge is the merchant's events,
// so the test drives a purchase through the webhook first.
func TestDomainWithAPaidSubscriptionGetsNoFreeKey(t *testing.T) {
	f := newFixture(t)
	bought := f.purchase("evt-buy", "buyer.example", 5, 365)
	if bought.Organisation != "buyer.example" {
		t.Fatalf("the purchase was recorded for %q, want the buying organisation", bought.Organisation)
	}
	minted := f.signer.calls
	mailed := len(f.mail.sent)

	answer := answerBody(t, f.apply("dev@buyer.example"))

	if got := f.signer.calls; got != minted {
		t.Errorf("the buyer's domain was minted %d more key(s), want none", got-minted)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the buyer's domain was mailed %d message(s), want none", got-mailed)
	}
	if queued := f.queued(); len(queued) != 0 {
		t.Errorf("the buyer's domain was queued (%+v), want a refusal: a domain with a paid subscription has no free seat", queued)
	}
	if len(answer) == 0 {
		t.Error("the refusal was answered with nothing")
	}
}

// TestOpenSourceCheckNeedsProofOfControl: visibility and licence prove nothing
// about who is asking (F47), so the check mints nothing until proof of control
// is given — and what counts as proof is D452, which is why the check is an
// interface and this fixture's answer is the only one in the tree.
func TestOpenSourceCheckNeedsProofOfControl(t *testing.T) {
	f := newFixture(t)
	f.proofs.proved = false

	answerBody(t, f.applyOpenSource(applicantAddress, repositoryURL))
	if got := f.proofs.asked; got != 1 {
		t.Errorf("the check asked for proof %d time(s), want once", got)
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("an open-source application with no proof of control signed %d key(s), want none", got)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("an open-source application with no proof of control mailed %d message(s), want none", len(f.mail.sent))
	}

	// The proof given, the same application reaches the verification mail, and
	// the token mints the open-source key.
	f.proofs.proved = true
	answerBody(t, f.applyOpenSource(applicantAddress, repositoryURL))
	if len(f.mail.sent) != 1 {
		t.Fatalf("an open-source application with proof mailed %d message(s), want the verification one", len(f.mail.sent))
	}
	f.verify(f.mailedToken())
	records := f.records()
	if len(records) != 1 {
		t.Fatalf("the verified open-source application minted %d key(s), want one", len(records))
	}
	if records[0].Plan != openPlanName {
		t.Errorf("the minted key is of plan %q, want %q", records[0].Plan, openPlanName)
	}
	if records[0].Organisation != openSourceOrg {
		t.Errorf("the minted key binds to %q, want what the proof showed control of (%q)", records[0].Organisation, openSourceOrg)
	}
}

// TestOpenSourceCheckRefusesAPrivateOrNonOSIRepository: §6.3 and §10.2(b) —
// every repository the applicant names has to be public under a licence the
// Open Source Initiative approves (§4.4).
func TestOpenSourceCheckRefusesAPrivateOrNonOSIRepository(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{name: "a private repository", url: privateURL},
		{name: "a repository under a licence the OSI does not approve", url: unlicensedURL},
		// A licence that reads as permissive and is not approved: the host's
		// metadata carries the id, the table has no business admitting it.
		{name: "a repository under a permissive licence the OSI does not approve", url: unapprovedURL},
		{name: "a URL the code host cannot answer for", url: "https://elsewhere.example/acme/widget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			answerBody(t, f.applyOpenSource(applicantAddress, tc.url))

			if got := f.signer.calls; got != 0 {
				t.Errorf("%s was signed %d key(s), want none", tc.name, got)
			}
			if len(f.mail.sent) != 0 {
				t.Errorf("%s was mailed %d message(s), want none: the check failed before anything was verified", tc.name, len(f.mail.sent))
			}
			if queued := f.queued(); len(queued) != 0 {
				t.Errorf("%s was queued (%+v), want a refusal: the queue is not the checks' fallback (§4.5)", tc.name, queued)
			}
		})
	}
}

// TestTheLicenceTableIsTheOSIsApprovedList holds §4.4's data to the design's
// rule — the licences the OSI approves are the ones admitted — with the ids a
// code host's metadata actually carries on both sides of it.
//
// It is a test rather than a comment because the rule is a data set with an
// update path, and the failure it guards is a permissive-looking id being added
// because it looks permissive: `0BSD`, `BSD-3-Clause-Clear`, `OpenSSL` and `X11`
// are the ones that were here and are not approved. SPDX's own `isOsiApproved`
// field is the comparison the list should be held to, and it is not in this tree
// and not reachable from a hermetic run — so the four are pinned by name and the
// owner's update is what re-checks the rest.
func TestTheLicenceTableIsTheOSIsApprovedList(t *testing.T) {
	for _, id := range []string{"MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "GPL-3.0-only", "LGPL-2.1-or-later", "MPL-2.0", "ISC", "Unlicense", "PostgreSQL", "OFL-1.1", "EUPL-1.2"} {
		if !osiApproved[id] {
			t.Errorf("%s is not admitted, want it in the table: it is on the OSI's approved list, and a refusal here is an honest maintainer refused", id)
		}
	}
	for _, id := range []string{
		// Permissive and not approved: what the table exists to keep out.
		"0BSD",
		"BSD-3-Clause-Clear",
		"OpenSSL",
		"X11",
		// And what is not an id at all, which a host's metadata can still say.
		"",
		"NOASSERTION",
		"LicenseRef-Proprietary",
		"MIT OR Apache-2.0",
	} {
		if osiApproved[id] {
			t.Errorf("%q is admitted, want a refusal: it is not a licence the OSI approves, and §4.4 refuses a repository the check cannot place", id)
		}
	}
}

// TestARepositoryOffTheDeploysHostIsNeverAskedAbout: §4.4's check reads a
// repository **of the deploy's own code host**, with the deploy's credentials —
// so a line an unauthenticated stranger typed must never become an address this
// service dials. What is admitted is one host, one scheme, one `owner/name`
// path, and the code host is handed the two parts and never the line: a line
// naming an internal address, a port, a deeper path or another host at all is
// refused before the first call, and the code host is asked about nothing.
func TestARepositoryOffTheDeploysHostIsNeverAskedAbout(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{name: "the metadata server", url: "https://169.254.169.254/latest/meta-data/"},
		{name: "an internal name", url: "https://code.internal.example/acme/widget"},
		{name: "the merchant's own host", url: "https://elsewhere.example/acme/widget"},
		{name: "a plain-http address", url: "http://code.example/acme/widget"},
		{name: "no scheme at all", url: "code.example/acme/widget"},
		{name: "an scp-style address", url: "git@code.example:acme/widget"},
		{name: "a port on the deploy's host", url: "https://code.example:8443/acme/widget"},
		{name: "credentials in the URL", url: "https://token@code.example/acme/widget"},
		{name: "a query string", url: "https://code.example/acme/widget?per_page=1"},
		{name: "a fragment", url: "https://code.example/acme/widget#readme"},
		{name: "a path deeper than a repository", url: "https://code.example/acme/widget/issues/1"},
		{name: "a traversal", url: "https://code.example/acme/../secret"},
		{name: "an encoded traversal", url: "https://code.example/acme%2f..%2fsecret"},
		{name: "a colon in the owner", url: "https://code.example/acme:widget/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			answerBody(t, f.applyOpenSource(applicantAddress, tc.url))

			if got := len(f.host.asked); got != 0 {
				t.Errorf("%s asked the code host about %v, want nothing: what an applicant typed is not an address the service dials (§4.4, §7.2)", tc.name, f.host.asked)
			}
			if got := f.signer.calls; got != 0 {
				t.Errorf("%s signed %d key(s), want none", tc.name, got)
			}
			if got := len(f.mail.sent); got != 0 {
				t.Errorf("%s mailed %d message(s), want none", tc.name, got)
			}
		})
	}

	// And the line the check does read is read as two parts: the host is
	// lowercased and a `.git` suffix is the same repository, so what the code
	// host is asked about is the repository the applicant named and not the
	// spelling of it.
	f := newFixture(t)
	answerBody(t, f.applyOpenSource(applicantAddress, "https://Code.Example/acme/widget.git"))
	if got := len(f.host.asked); got != 1 || f.host.asked[0] != "acme/widget" {
		t.Fatalf("the code host was asked %v, want the repository's two parts alone", f.host.asked)
	}
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the application mailed %d message(s), want the verification one", got)
	}
	f.verify(f.mailedToken())
	records := f.records()
	if len(records) != 1 {
		t.Fatalf("the application recorded %d key(s), want one", len(records))
	}
	held := f.store.verifications
	if len(held) != 1 || len(held[0].Repositories) != 1 || held[0].Repositories[0] != repositoryURL {
		t.Errorf("the verification holds %v, want the canonical URL %q: what is stored is the URL on the deploy's own host and never the line", held, repositoryURL)
	}
}

// TestAnApplicationOverTheRepositoryCapIsRefused: the count is the deploy's,
// because each repository it names is a call into the deploy's code-host
// credentials made on the strength of one unauthenticated post — and the form's
// body cap is not a bound on that: 16 KiB is more URLs than the check should
// ever ask the host about. An application past the cap is refused before the
// first call.
func TestAnApplicationOverTheRepositoryCapIsRefused(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) { f.cfg.MaxRepositories = 2 })

	// At the cap: read, and the address verified.
	answerBody(t, f.applyOpenSource(applicantAddress, repositoryURL, repositoryTwoURL))
	if got := len(f.host.asked); got != 2 {
		t.Fatalf("an application naming two repositories asked the code host %d time(s), want its two", got)
	}
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("an application at the cap mailed %d message(s), want the verification one", got)
	}
	asked, mailed := len(f.host.asked), len(f.mail.sent)

	// One past it: nothing is read and nothing is mailed.
	answerBody(t, f.applyOpenSource("dev@other.example", repositoryURL, repositoryTwoURL, repositoryURL+"-three"))
	if got := len(f.host.asked) - asked; got != 0 {
		t.Errorf("an application past the cap asked the code host %d time(s), want none: the count is checked before the first call", got)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("an application past the cap mailed %d message(s) in all, want the verification one alone", got)
	}
	if queued := f.queued(); len(queued) != 0 {
		t.Errorf("an application past the cap was queued (%+v), want a refusal", queued)
	}
}

// TestUnverifiedEmailMintsNothing: nothing is minted before the address is
// verified (§4.1) — the free seat's application mails a single-use token and
// mints only when it comes back.
func TestUnverifiedEmailMintsNothing(t *testing.T) {
	f := newFixture(t)
	f.apply(applicantAddress)

	if got := f.signer.calls; got != 0 {
		t.Errorf("an application with an unverified address signed %d key(s), want none", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("an application with an unverified address recorded %d key(s), want none", got)
	}
	if len(f.mail.sent) != 1 {
		t.Fatalf("the application mailed %d message(s), want the verification one", len(f.mail.sent))
	}
	if got := f.mail.sent[0].To; got != applicantAddress {
		t.Errorf("the verification mail went to %q, want the address the applicant typed (%q)", got, applicantAddress)
	}
	if got := f.mail.sent[0].Subject; !strings.Contains(got, verificationSubject) {
		t.Errorf("the verification mail's subject is %q, want the service's own (%q)", got, verificationSubject)
	}

	f.verify(f.mailedToken())

	if got := f.signer.calls; got != 1 {
		t.Fatalf("the verified application signed %d key(s), want one", got)
	}
	records := f.records()
	if len(records) != 1 {
		t.Fatalf("the verified application recorded %d key(s), want one", len(records))
	}
	if records[0].Plan != freePlanName || records[0].Organisation != "acme.example" {
		t.Errorf("the minted key is %+v, want the free seat's plan bound to the applicant's domain", records[0])
	}
	if len(f.mail.sent) != 2 {
		t.Errorf("the verified application mailed %d message(s) in all, want the key's too", len(f.mail.sent))
	}
}

// TestVerificationTokenSingleUse: a leaked link is not a key forever (§7.2) —
// the token is spent by the first request that carries it.
func TestVerificationTokenSingleUse(t *testing.T) {
	f := newFixture(t)
	f.apply(applicantAddress)
	token := f.mailedToken()

	f.verify(token)
	if got := f.signer.calls; got != 1 {
		t.Fatalf("the first use signed %d key(s), want one", got)
	}
	f.verify(token)
	if got := f.signer.calls; got != 1 {
		t.Errorf("the second use of one token signed %d key(s) in all, want the first use's one", got)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the record holds %d key(s), want one", got)
	}

	// The open-source programme's token too, where nothing else stops a second
	// mint: the free seat's second use is refused by §4.2's domain check whether
	// or not the token is spent, and this half is the one that holds the spending
	// itself.
	open := newFixture(t)
	open.applyOpenSource(applicantAddress, repositoryURL)
	openToken := open.mailedToken()
	open.verify(openToken)
	open.verify(openToken)
	if got := open.signer.calls; got != 1 {
		t.Errorf("the open-source link followed twice signed %d key(s), want the first use's one", got)
	}
}

// TestAnswersDoNotRevealAnExistingKey: §7.1 — every answer reads the same,
// whether the application was minted, queued or refused, so the form is not a
// lookup for someone else's licence state.
func TestAnswersDoNotRevealAnExistingKey(t *testing.T) {
	f := newFixture(t)
	f.apply(applicantAddress)
	f.verify(f.mailedToken())
	mailed := len(f.mail.sent)

	held := answerBody(t, f.apply("other@acme.example"))   // this domain holds one
	free := answerBody(t, f.apply("person@gmail.example")) // a free-mail domain
	fresh := answerBody(t, f.apply("dev@fresh.example"))   // nothing known about it

	if held != free || held != fresh {
		t.Errorf("the answers differ, so the form says which domains hold a key:\n held: %q\nfree: %q\nfresh: %q", held, free, fresh)
	}
	if got := len(f.mail.sent); got != mailed+1 {
		t.Errorf("the three applications mailed %d message(s) in all, want one more: the unknown domain is the only one that verifies, and the answers must hide the difference", got)
	}
}

// TestTheChecksRefuseANonAddress keeps the one field the three forms share to
// an address: a value that is not one is answered uniformly and reaches neither
// the mail path nor the store.
//
// The list is the specials RFC 5322 allows *around* an address and not in one,
// most of them with no whitespace anywhere to catch them: an address the intake
// reads whole is one it can mail to and count by, and `maintainer@gmail.example(x)`
// is the case that costs three checks at once — §4.1's free-mail list, §4.2's
// one-per-domain count and §4.3's paid-domain refusal all compare the domain,
// while net/mail delivers that address to gmail.example.
func TestTheChecksRefuseANonAddress(t *testing.T) {
	f := newFixture(t)
	for _, raw := range []string{
		"",
		"not-an-address",
		"two@example.example, three@example.example",
		"no-domain@localhost",
		"a@b.example,victim@c.example",
		"a@b.example;victim@c.example",
		"maintainer@gmail.example(x)",
		"\"dev\"@acme.example",
		"dev@acme.example>",
		"dev\\v@acme.example",
	} {
		rec := f.post(RouteFreeSeat, url.Values{"email": {raw}})
		answerBody(t, rec)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("a refused address mailed %d message(s), want none", len(f.mail.sent))
	}
	if len(f.queued()) != 0 {
		t.Errorf("a refused address queued %d application(s), want none", len(f.queued()))
	}
}

// TestAddressReadsEachHalfByItsOwnGrammar is the same rule one layer down, where
// the two halves can be read apart: the local part is RFC 5322's dot-atom, which
// is what refuses a header injection's punctuation, and the domain is a name of
// labels, which is what the three eligibility checks compare. Both are pinned
// here, because through the routes every refusal is the one answer page (§7.1)
// and a whole table can go green for the wrong reason.
func TestAddressReadsEachHalfByItsOwnGrammar(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "a plain address", raw: "dev@acme.example", want: "dev@acme.example"},
		{name: "a domain of any case", raw: "dev@ACME.Example", want: "dev@acme.example"},
		{name: "atext and dots in the local part", raw: "first.last+tag_1@sub.acme.example", want: "first.last+tag_1@sub.acme.example"},

		// What the mail header cannot carry: a control character, whatever the
		// local part's grammar says about the rest of it.
		{name: "a header line", raw: "dev@acme.example\r\nBcc: victim@elsewhere.example"},
		{name: "a space", raw: "dev@acme.example (a comment)"},

		// The specials, none of which is atext or a label.
		{name: "a comma", raw: "a@b.example,victim@c.example"},
		{name: "a semicolon", raw: "a@b.example;victim@c.example"},
		{name: "a comment in the domain", raw: "maintainer@gmail.example(x)"},
		{name: "a quoted local part", raw: "\"dev\"@acme.example"},
		{name: "an angle bracket", raw: "dev@acme.example>"},
		{name: "a backslash", raw: "dev\\v@acme.example"},
		{name: "an address literal", raw: "dev@[192.0.2.1]"},
		{name: "a second at sign", raw: "dev@acme.example@other.example"},
		{name: "a character outside ASCII", raw: "dév@acme.example"},

		// Dots, which are a separator and not a character of either half.
		{name: "a leading dot", raw: ".dev@acme.example"},
		{name: "a doubled dot", raw: "de..v@acme.example"},
		{name: "a trailing dot", raw: "dev.@acme.example"},
		{name: "a leading dot in the domain", raw: "dev@.acme.example"},
		{name: "an empty domain label", raw: "dev@acme..example"},

		// Labels of a name, and the shape of a name at all.
		{name: "a label opening with a hyphen", raw: "dev@-acme.example"},
		{name: "a label closing with a hyphen", raw: "dev@acme.example-"},
		{name: "one label", raw: "dev@acme"},
		{name: "no at sign", raw: "dev.acme.example"},
	} {
		got, ok := address(tc.raw)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: address(%q) read %q, want a refusal", tc.name, tc.raw, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s: address(%q) = %q, %v, want %q", tc.name, tc.raw, got, ok, tc.want)
		}
	}
}

// TestTheRequestFormQueuesWhatAPersonReads: §4.5 — the third form is the
// requests the checks cannot decide, and every one of them is a person's.
func TestTheRequestFormQueuesWhatAPersonReads(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{
		"email":   {"prof@academic.example"},
		"request": {"a multi-seat trial for a seminar"},
	}))

	queued := f.queued()
	if len(queued) != 1 {
		t.Fatalf("the manual request queued %d application(s), want one", len(queued))
	}
	if queued[0].Detail != "a multi-seat trial for a seminar" {
		t.Errorf("the queued request's text is %q, want what the applicant wrote", queued[0].Detail)
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("a manual request signed %d key(s), want none: it is the owner's to answer (§4.5)", got)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("a manual request mailed %d message(s), want none", len(f.mail.sent))
	}
}

// TestTheTokenExpires: §8's sibling — a token's life is bounded, so a link in a
// mailbox nobody reads is not a key (D457's period is the owner's and this is
// the rule it feeds).
func TestTheTokenExpires(t *testing.T) {
	f := newFixture(t)
	f.apply(applicantAddress)
	token := f.mailedToken()

	f.advance(time.Duration(f.svc.cfg.VerificationTTL) + time.Minute)
	answerBody(t, f.verify(token))

	if got := f.signer.calls; got != 0 {
		t.Errorf("an expired token signed %d key(s), want none", got)
	}
}
