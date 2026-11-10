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
func TestTheChecksRefuseANonAddress(t *testing.T) {
	f := newFixture(t)
	for _, raw := range []string{"", "not-an-address", "two@example.example, three@example.example", "no-domain@localhost"} {
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
