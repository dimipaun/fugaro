# Subscriptions: the counsel addendum

**Status.** This is the addendum [subscriptions S0.2](implementation-plan.md#s02-the-counsel-addendum)
writes. It goes to counsel with the Subscription Terms
([`docs/licensing/subscription-terms.md`](../licensing/subscription-terms.md)) in the round
[S8.4](implementation-plan.md#slice-s8-the-owners-counsels-and-the-accountants-steps) records, and it
asks **only what the Terms' own list for counsel does not already ask**: the draft counsel was given
carried seventeen questions (#1146), of which 12–18 were added with the owner's answers, and that
list is the eight items counsel's restructured text ends with today. Nothing here is legal advice,
and nothing here decides anything: every question is counsel's, and every ruling it cites is the
owner's, already recorded in
[reconciliation.md](reconciliation.md#rulings-on-the-tensions-2026-10-27).

**It carries no figure (D430, T22).** An amount, a rate and a discount are named by the clause they
sit in and never by their value — the two figures the owner ruled into the Terms (the liability cap
in section 10.5 and the late-payment rate in section 12.2) are referred to the same way. Prices,
discounts, fee rates and the Enterprise minimum are in the owner's private business repository, not
here. The scan
[subscriptions S0.1](implementation-plan.md#s01-a-docs-test-that-no-price-figure-enters-the-repository)
writes holds that over this folder, and the plan makes it this document's gate.

**What has moved since the questions were written.** The Terms have changed twice. Counsel
restructured them on 2026-10-28 into Parts A–G and sections 1 to 34 (they were the draft's sections
1 to 24), and the owner's rulings that restructure did not carry were applied on 2026-10-29 and
approved by counsel — the Terms' own list, item 4. So this packet opens with what is already
settled, and asks the rest.

## How the clauses are numbered

The plan and this packet's brief cite the draft's numbering; counsel works on the restructured text.
The clauses named below:

| The draft (#1146) | Counsel's text | Subject |
| --- | --- | --- |
| §4.3(a) | §10.2(a) | the free seat, one per legal person |
| §4.3(b) | §10.2(b) | open-source teams |
| §5.1 | §5.1 | the seat rule |
| §7.2 | §12.2 | the late-payment rate |
| §8.1 | §6.1 | the License Key and what it states |
| §8.2 | §6.2 | verification and the grace period |
| §8.3 | §6.3 | issue of License Keys |
| §8.4 | §7.5(b) | the personal data at sign-up |
| §8.5 | §6.4 | protection of License Keys |
| §10.1, §10.2 | §8.1, §8.2 | support and supported versions |
| §10.4 | §9.1 | read-only use after the grace period |
| §14 | §10.5 | the liability cap for the Community Plan and the Free Plans |
| §17.4 | §27.4 | termination and the destruction duty |

## Settled since the questions were asked

Counsel has applied and approved these, and the Terms' own list for counsel records each one (items
4, 5 and 7), so this packet asks none of them again:

- **T12 — read-only use keeps cancel.** After the grace period a User may still cancel a Job and use
  the kill switch, and may not initiate or modify anything else (§9.1); §27.4(a) refers the
  destruction duty to it. That is ruling T12, in place of the draft's "cannot launch, cancel or
  change anything".
- **The key binding — one key per organisation.** One License Key per Customer for its organisation,
  stating its User Seats, replaces the draft's key per User (§§2, 5.3, 6.1, 6.3, 6.4, 7.5, 9.1).
- **O4 — expiry only.** The covered-versions date is gone (§§6.1, 8.2).
- **T15, first half — the free seat per legal person.** §10.2(a) holds one free User Seat for a legal
  person with no Paid Plan, and a legal person and its Affiliates together hold no more than one
  (§§6.3, 7.5, 10.2(a)).
- **T24 — the contract figures stay in the Terms.** The support targets and hours (§8.1), the annual
  renewal's cancellation notice (§14), the refund window (§15) and the seat reassignment period
  (§5.3) are printed, as the owner ruled.
- **The coordinator** is part of the Software and not of the Core (§7.1; the owner's ruling of
  2026-10-29), and **crash reporting** is opt-in, per User and off by default (§7.3; the owner's
  ruling of 2026-10-31, counsel approved).

Two subjects are with counsel elsewhere and are not repeated: the seat rule's wording as a pricing
rule (counsel question 13), and whether the licence files need a revision matching the Terms' own
words (the Terms' own list, item 8).

## The questions

### A1 — may a licensed release check no key? (T17, D448, S0.7)

Console 0.113.0 is the first console release under [`console/LICENSE.md`](../../console/LICENSE.md)
and it shipped with no key check: its notes say compliance is on the Customer's honour and that key
enforcement arrives in a later release. The mobile app's first release, which has not shipped, is
the other half of the same gate. The Terms lean on themselves for this — §6.4 says the Licensor
relies on these Terms, and not on technical controls, to enforce User Seat limits, and §6.2's
offline verification is a design no release implements. But **§6.1 states the check as the
Software's behaviour**: "the Software requires a valid License Key to operate, subject to section
4.2". No released Software does, and none will until the Key Service exists (A6).

1. Does counsel confirm that clause 1.1 of the licence carries a licensed release whose only
   enforcement is the Agreement — that "you have agreed to, and are in compliance with, the
   Agreement" is satisfied by the Terms and the Customer's acceptance, with no key in the product?
2. If counsel would rather the text say so, should §6.1 be worded as the Customer's obligation
   (a Customer shall hold a valid License Key) rather than as what the Software does, or should it
   carry the release from which the check applies?
3. D448 makes counsel's sign-off of D432, D433 and D436 the gate on console 0.113.0 and the app's
   first release. Counsel applied and approved those rulings on 2026-10-29. Does counsel regard
   that approval as the sign-off, so that the app's first release may ship on it, or is a separate
   written confirmation wanted?

### A2 — one binary, a mixed licence, and the MIT commands inside it (T21)

From 0.13.0 the release is built with the `ee` tag: one binary carries the MIT core and the
business-licensed parts ([`LICENSING.md`](../../LICENSING.md), "`ee/`, the Subscription Edition"),
and every licence label says so. The licence's "Software" is every file under `ee/`, `console/` and
`mobile/` "including code from those directories compiled into a released binary", and clause 1.1
conditions production use of the Software on a subscription for the correct number of user seats.
Ruling T21 chose the single binary over a second core-only download precisely so this could be
answered: its option (a) was one binary "with counsel confirming that MIT commands need no seat".

The draft carried a note to §3.3 saying what moved and that one mixed-licence binary ships. Counsel's
text has no such note — §2's "Core" and "Software" definitions do the work instead — so the Terms
today say nothing about which commands a seat reaches.

1. In the released binary, does clause 1.1 reach a person who runs only a core command in
   production: `fugaro run` to launch a Job, `fugaro runs digest`, `fugaro diagnose`, `fugaro watch`?
   D433 makes a seat anyone who launches Jobs by any means, and those commands launch none.
2. If it does not reach them, should the Terms say so, and where — §3.1's plan table, §5.1's seat
   rule, or §9.2, which already carves the Command-line tool out of the lapse ("Nothing in this
   section limits the use of the Command-line tool under the MIT licence, including to initiate and
   cancel Jobs")?
3. If it does reach them, does §5.1 need to say that the Command-line tool is a launcher like any
   other, so a Customer reading §5.1 alone counts the people who use it?

### A3 — an individual, "businesses only", and the free seat (T15)

§1.3 offers the Software "only to businesses and other organisations, for use in their trade,
business or profession, and not to consumers", and the Customer represents it is not acquiring as a
consumer for personal use. §10.2(a) holds the free seat for "a legal person" with no Paid Plan, and
§10.2(b) frees a team whose projects are all public under an Open Source Initiative licence. Ruling
T15 is that an individual counts as an organisation of one for both, and that business-only must not
exclude them; §1.3 was deliberately left unchanged while that was settled, and §10.2(a) says "legal
person" rather than the draft's "organisation".

1. Does §1.3 exclude an individual who maintains an open-source project, or a sole proprietor, from
   the free seat of §10.2(a) or the open-source plan of §10.2(b) — and if it may, should §1.3 or
   §10.2 carry a sentence that an individual acting other than as a consumer is an organisation of
   one?
2. The Terms use "consumer" without defining it. Should §2 define it, so that §1.3 and the free
   plans are read the same way by a self-serve applicant and by a court?

### A4 — a refunded buyer keeps a key until it expires (T25)

A License Key verifies offline and nothing can call one back: the design has no revocation, so a
key's expiry is the only thing that ends it (decision D308, carried by the key's own tasks S2.1 and
S2.3). §15 lets a Team buyer take a refund within its window. A buyer who does so therefore keeps a
working key until that key expires, and the lapse rule bites only 30 days after that. The owner
accepted the leakage as a risk (T25), against the recommendation, which was a short initial key.

1. Does counsel accept that as drafted, or should §15 or §6.4 say plainly that a refund does not end
   a License Key already issued, so no buyer is surprised by it and no dispute turns on silence?
2. Should the Licensor reserve the express right to decline a renewal, or a seat change, after a
   refund — or does §14 (Term and renewal) already leave that to the Licensor?

### A5 — the contract figures printed in a public Terms text (T24)

The Terms print the support targets and hours (§8.1), the annual renewal's cancellation notice
(§14), the refund window (§15), the seat reassignment period (§5.3), and the two figures the owner
ruled in on 2026-10-29 (the liability cap in §10.5, the late-payment rate in §12.2). Prices,
discounts, fee rates and the Enterprise minimum stay on the Order Form and out of the repository
(D430, T22), so the published text carries the contract terms and not the commercial ones.

1. Is that split the right one for a text published at a URL the licence pins — do the support
   targets, the renewal notice, the refund window and the reassignment period belong in the Terms,
   or should any of them move to the Order Form?
2. §5.3's reassignment period is the one of them that is a product rule rather than a commercial
   term: does it need to be a contract figure at all, or would "at most once per Subscription
   period" carry it without a number?

### A6 — issuance before the Key Service exists, and the paid-key path (T13, T26, F42)

§6.3 says the Licensor "issues License Keys through the Key Service, a self-serve website", and §2
defines the Key Service as "the hosted service the Licensor operates to issue License Keys". Ruling
T13 put the service in a dedicated project the owner operates, built after the entity exists, and
ruled that **until then the owner mints keys by hand**. The Terms say nothing about that interim.
§6.3's paid path is also one sentence — "the Licensor issues the Customer a License Key stating the
User Seats purchased, following purchase" — with no seat change, no renewal and no cancellation,
while T26 ruled that the next key issues 30 days before expiry and the key format allows two valid
keys at once, and F42 records that nothing mints on a purchase, reissues at renewal or stops at
cancellation today.

1. Does §6.3 need a transition sentence — keys issued by the Licensor directly, by hand, until the
   Key Service is live — or a wider "Key Service" definition, so the text is true from the first
   hand-minted key?
2. Should §6.3 write the paid path out: issue after purchase, reissue on a prorated seat change,
   renewal issued 30 days before expiry, and no reissue after cancellation?
3. With the renewal's overlap, a Customer may hold two valid keys at once. Does the licence's clause
   1.1 — a subscription "for the correct number of user seats" — need anything for that, or does it
   read the same for two overlapping keys as for one?

## What S0.4 applies when counsel answers

[Subscriptions S0.4](implementation-plan.md#s04-the-one-terms-revision-after-counsels-feedback) is
the one revision run of the Terms after counsel's feedback. Of its item list, these are already
applied and counsel-approved: T12 (§9.1), the key binding (§§2, 5.3, 6.1, 6.3, 6.4, 7.5, 9.1), O4
(§§6.1, 8.2), T15's first half (§§6.3, 7.5, 10.2(a)) and the kept contract figures (§§10.5, 12.2).
What is still open, and what A1–A6 feed:

- **§6.1** — whether the Software's key requirement is stated as behaviour or as the Customer's
  obligation, while no release enforces one (A1).
- **§6.3** — T26's renewal 30 days before expiry, T13's hand minting, and the paid-key path (A6).
- **§1.3** — T15's second half: an individual as an organisation of one (A3).
- **§15 or §6.4** — the refund leakage (A4).
- **The draft's §3.3 note**, gone from counsel's text with no replacement — whether the Terms say
  which commands need a seat (A2).
- **T24's split** — the contract figures in the Terms and the commercial ones on the Order Form
  (A5).

Two things this packet does not carry, so that counsel's round is not read as more than it is. It
does not re-open a settled clause, and it asks for no figure: the amounts it refers to are the
owner's, in the private business repository. And it is not the whole of S8.4 — that round also
answers the questions 12–18 the draft already put, words D433, confirms the rest of D432, D436 and
T12's cancel, and reviews the privacy notice
[subscriptions S7.3](implementation-plan.md#s73-the-privacy-notice-for-key-sign-up) writes, which
does not exist yet.
