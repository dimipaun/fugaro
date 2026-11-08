# Subscriptions: the counsel addendum

**Status.** This is the addendum [subscriptions S0.2](implementation-plan.md#s02-the-counsel-addendum)
writes. It goes to counsel with the Subscription Terms
([`docs/licensing/subscription-terms.md`](../licensing/subscription-terms.md)) in the round
[S8.4](implementation-plan.md#slice-s8-the-owners-counsels-and-the-accountants-steps) records, and it
asks **only what the Terms' own list for counsel does not already ask**. The draft counsel was given
carried eighteen numbered questions (#1146), of which 12–18 were added with the owner's answers;
counsel's restructured text answers most of those and ends with its own list of eight items for the
owner and counsel, and none of the questions below repeats one of the eight. Nothing here is legal
advice, and nothing here decides anything: every question is counsel's, and every ruling it cites is
the owner's. The tensions T10 to T27 are in
[reconciliation.md](reconciliation.md#rulings-on-the-tensions-2026-10-27), with T27's consequence
[ruled 2026-10-29](reconciliation.md#a-consequence-the-rulings-create-ruled-2026-10-29), and O4 and
the key binding are ruled in the same file; crash reporting (2026-10-31, counsel approved) is the
Terms' own list for counsel, item 5, and the coordinator (2026-10-29) is item 7, recorded there as
the owner's resolution and not as counsel's — see the S0.4 list below.

**It carries no figure (D430, T22).** An amount, a rate and a discount are named by the clause they
sit in and never by their value — the two figures the owner ruled into the Terms (the liability cap
in section 10.5 and the late-payment rate in section 12.2) are referred to the same way. Prices,
discounts, fee rates and the Enterprise minimum are in the owner's private business repository, not
here. [Subscriptions S0.1](implementation-plan.md#s01-a-docs-test-that-no-price-figure-enters-the-repository)
writes the scan that holds this over the folder, and the plan makes it this document's gate; S0.1
has not landed yet (the census of 2026-11-07 lists it NOT STARTED), so until it does this document
holds the rule by construction and the Docs suite is its only test.

**What has moved since the questions were written.** The Terms have changed since. The owner's ruling
D531 (2026-10-29) took the coordinator out of the Core: §9.1 of the draft, now §7.1, stopped calling
it part of the Core at once, with counsel not consulted — the one edit made before counsel answered,
and the reason for the first item of the S0.4 list below. Counsel restructured the text on
2026-10-28 into Parts A–G and sections 1 to 34 (they were the draft's sections 1 to 24), and the
tree took that text on 2026-10-29, when the owner's rulings the restructure did not carry were
applied, with the two figures once held back and the provisions counsel's document had marked (the
Terms' own list, items 1, 3 and 4). The owner filled in the Licensor's name, jurisdiction, address,
privacy notice URL and notice address on 2026-10-30. §7.3 was revised on 2026-10-31 so that crash
reporting is opt-in and off by default (item 5). And on 2026-11-07 §7.1 stopped calling the
credentials service optional — it now says plainly that the service is part of the Core and not of
the Software, because `fugaro update` deploys it on every installation (the owner's ruling of
2026-11-05); the same day's other change to that file was the reading note above the agreement,
which is not part of it and now records that the two figures go in as counsel wrote them. The Terms'
own Status and list record all of these but the 2026-11-07 change. So this packet opens with what is
already settled, and asks the rest.

## How the clauses are numbered

The plan and this packet's brief cite the draft's numbering; counsel works on the restructured text.
The clauses named below:

| The draft (#1146) | Counsel's text | Subject |
| --- | --- | --- |
| §4.3(a) | §10.2(a) | the free seat, one per legal person |
| §4.3(b) | §10.2(b) | open-source teams |
| §5.1 | §5.1 | the seat rule |
| §5.3 | §5.4 | one individual per seat, and the reassignment period |
| §6.2 | §14 | the annual renewal's cancellation notice |
| §7.2 | §12.2 | the late-payment rate |
| §7.4 | §15 | the refund window |
| §8.1 | §6.1 | the License Key and what it states |
| §8.2 | §6.2 | verification and the grace period |
| §8.3 | §6.3 | issue of License Keys |
| §8.4 | §7.5(b) | the personal data at sign-up |
| §8.5 | §6.4 | protection of License Keys |
| §10.1, §10.2 | §8.1, §8.2 | support and supported versions |
| §10.4 | §9.1 | read-only use after the grace period |
| §14 | §24 | limitation of liability: the cap, the excluded damages and the exceptions |
| §17.4 | §27.4 | termination and the destruction duty |
| — | §10.5 | the Community, Free Plans and Beta Features limits, new in counsel's text |

## Settled since the questions were asked

These are settled and this packet asks none of them again. All but T24 are the Terms' own list for
counsel, items 4 and 5, applied with counsel's approval; T24 is the owner's ruling in
[reconciliation.md](reconciliation.md#rulings-on-the-tensions-2026-10-27), which left the clauses as
they stood, the draft having already printed those figures.

- **T12 — read-only use keeps cancel.** After the grace period a User may still cancel a Job and use
  the kill switch, and may not initiate or modify anything else (§9.1); §27.4(a) is where the
  production right ends and §27.4(b) is the destruction duty that goes with it. That is ruling T12,
  in place of the draft's "cannot launch, cancel or change anything".
- **The key binding — one key per organisation.** One License Key per Customer for its organisation,
  stating its User Seats, replaces the draft's key per User (§§2, 5.3, 6.1, 6.3, 6.4, 7.5, 9.1 — the
  Terms' own list, item 4, in counsel's numbering; §5.3 there is Roles, which carries the key).
- **O4 — expiry only.** The covered-versions date is gone (§§6.1, 8.2).
- **T15, first half — the free seat per legal person.** §10.2(a) holds one free User Seat for a legal
  person with no Paid Plan, and a legal person and its Affiliates together hold no more than one
  (§§6.3, 7.5, 10.2(a)).
- **T24 — the contract figures stay in the Terms.** The support targets and hours (§8.1), the annual
  renewal's cancellation notice (§14), the refund window (§15) and the seat reassignment period
  (§5.4) are printed, as the owner ruled.
- **Crash reporting** is opt-in, per User and off by default (§7.3; the owner's ruling of
  2026-10-31, counsel approved).

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
3. D448 makes counsel's sign-off of D432, D433 and D436 the gate on console 0.113.0, which has
   shipped, and on the app's first release, which has not. That sign-off is the plan's
   [S8.4](implementation-plan.md#slice-s8-the-owners-counsels-and-the-accountants-steps): counsel
   words D433 there and confirms D432, D436, T15, T17's honour-based release and T21. What counsel
   approved on 2026-10-29 is narrower — the S0.4 items as applied to the Terms (the Terms' own
   list, item 4), which carry the clauses D432 and D436 produced (§9.1, §10.2(a)) and not the
   rulings themselves — while D433's wording is one of the questions that round answers (counsel
   question 13). Does counsel regard the clause approval as the sign-off, so that the app's first
   release may ship on it, or must that release wait for S8.4?

### A2 — one binary, a mixed licence, and the MIT commands inside it (T21)

The release that carries the business-licensed parts is built with the `ee` tag: one binary carries
the MIT core and the business-licensed parts ([`LICENSING.md`](../../LICENSING.md), "`ee/`, the
Subscription Edition"), and every licence label says so. The licence's "Software" is every file
under `ee/`, `console/` and `mobile/` "including code from those directories compiled into a
released binary", and clause 1.1 conditions production use of the Software on a subscription for the
correct number of user seats. Ruling T21 chose the single binary over a second core-only download
precisely so this could be answered: its option (a) was one binary "with counsel confirming that MIT
commands need no seat".

Counsel's text answers the launcher half of that. §5.1(a) requires a User Seat for each individual
who "initiates a Job by any means, including through the desktop console, the mobile app, the
Command-line tool, or any AI agent or automated process directed by that individual", so the person
who runs `fugaro run` is counted like any other launcher; and §5.2 states that the Command-line tool
is licensed under the MIT licence and is not part of the Software, that §5.1 sets only the number of
User Seats and therefore the Fees, and that §5.1 limits no right granted under the MIT licence.

What the text does not answer is the mixed binary itself. The Terms' §2 and the licence define the
Software the same way — every file under `ee/`, `console/` and `mobile/`, "including code from those
directories compiled into a released binary or application" — so the binary a person downloads is
the Software even when they run only a command that initiates no Job, and nothing in the text says
whether clause 1.1's condition reaches them. The draft carried a note to §3.3, and it listed the
moves only — the coordinator, the spend and cost reports, the central orchestrators and the hosted
MCP server — and said nothing about a mixed-licence binary; counsel's text carries no such note
either, and [reconciliation.md](reconciliation.md) records the gap in as many words: "the released
binary carrying `ee/` code under one key-gated build is not addressed".

1. In the released binary, does clause 1.1 reach a person who runs only a command that initiates no
   Job — `fugaro runs digest`, `fugaro diagnose`, `fugaro watch` — given that the binary carries the
   Software compiled into it while §5.2 says the Command-line tool is not part of the Software?
   (`fugaro run` launches a Job, so §5.1(a) counts whoever runs it whatever the answer here.)
2. If it does not reach them, should the Terms say so, and where — §2's "Software" definition, §5.1's
   seat rule, §5.2, or §9.2, which already carves the Command-line tool out of the lapse ("Nothing in
   this section limits the use of the Command-line tool under the MIT licence, including to initiate
   and cancel Jobs")?
3. If it does reach them, should §5.2 say that its "not part of the Software" holds for the released
   binary too, so a Customer reading §5.2 does not take it for a carve-out of the binary?

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
(§14), the refund window (§15), the seat reassignment period (§5.4), and the two figures the owner
ruled in on 2026-10-29 (the liability cap in §10.5, the late-payment rate in §12.2). Prices,
discounts, fee rates and the Enterprise minimum stay on the Order Form and out of the repository
(D430, T22), so the published text carries the contract terms and not the commercial ones.

1. Is that split the right one for a text published at a URL the licence pins — do the support
   targets, the renewal notice, the refund window and the reassignment period belong in the Terms,
   or should any of them move to the Order Form?
2. §5.4's reassignment period is the one of them that is a product rule rather than a commercial
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
applied: T12 (§9.1), the key binding (§§2, 5.3, 6.1, 6.3, 6.4, 7.5, 9.1), O4 (§§6.1, 8.2) and
T15's first half (§§6.3, 7.5, 10.2(a)) with counsel's approval, and the kept contract figures
(§§8.1, 14, 15 and 5.4) and the two figures the owner ruled in after they had been held back
(§§10.5, 12.2) on the owner's ruling.
What is still open, and what A1–A6 feed:

- **§7.1 and the coordinator (D531)** — the one edit the owner made before counsel answered, and
  not one of A1–A6. On 2026-10-29 the owner ruled that the coordinator moves under `ee/` and had
  §7.1 changed at once to say the coordinator is part of the Software, with counsel not consulted:
  the plan's S0.4 records it as its one exception to "the Terms are not revised before counsel
  answers" and says in as many words "counsel not yet consulted: tell counsel in the next revision
  round" — this round, and the reason the item is here. The Terms' own list, item 7, records the
  owner's resolution and no counsel approval, so the settled list above does not carry it. The move
  has since landed (`LICENSING.md` has the coordinator in `ee/coordinator/`), so §7.1 now matches
  the tree; counsel's text had classified the coordinator as Core, and counsel has still not been
  asked. What S0.4 applies: counsel's word on §7.1 as it stands, and its answer on where the
  coordinator's released MIT versions leave the licence's coverage sentence (T10, F38 — the draft's
  question 18, part of the 12–18 this packet leaves to S8.4's round).
- **§6.1** — whether the Software's key requirement is stated as behaviour or as the Customer's
  obligation, while no release enforces one (A1).
- **§6.3** — T26's renewal 30 days before expiry, T13's hand minting, and the paid-key path (A6).
- **§1.3** — T15's second half: an individual as an organisation of one (A3).
- **§15 or §6.4** — the refund leakage (A4).
- **The mixed-licence binary** — the draft's §3.3 note covered the moves only, and neither it nor
  counsel's text says whether the commands that initiate no Job need a seat in the binary that
  carries `ee/` code, which §5.2's "not part of the Software" does not settle on its own (A2).
- **T24's split** — the contract figures in the Terms and the commercial ones on the Order Form
  (A5).

Two things this packet does not carry, so that counsel's round is not read as more than it is. It
does not re-open a settled clause, and it asks for no figure: the amounts it refers to are the
owner's, in the private business repository. And it is not the whole of S8.4 — that round also
answers the questions 12–18 the draft already put, words D433, confirms D432, D436 and T12's cancel,
T15, T17's honour-based release and T21, and reviews the privacy notice
[subscriptions S7.3](implementation-plan.md#s73-the-privacy-notice-for-key-sign-up) writes, which
does not exist yet. Its list names T12's cancel, which the Terms' own list, item 4, already records
as applied and counsel-approved on 2026-10-29 — the reason the settled section above does not re-ask
it, and what A1's third question asks counsel to resolve for the release gate.
