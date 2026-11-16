# Subscription Terms: proposed next version (redline)

**Status: a proposal for counsel's approval. Not legal advice, not approved, not published.** Nothing in this
file is part of the Terms. The Terms in force are [subscription-terms.md](subscription-terms.md), published at
https://fugaro.dev/legal/subscription-terms (live 2026-11-11), and the generator
(`internal/clidoc`) reads only that file, so this file changes no published text.

**Source.** The first answers to the counsel brief of 2026-11-11 (FugaroBusiness
`docs/pricing-and-partner-program/counsel-brief.md`, items 2, 5, 10, 12 and 15). Those answers were prepared by an
AI model briefed as Canadian counsel, **not by a licensed lawyer**, so real counsel confirms or corrects every
clause below before it is used. Section numbers refer to the current Terms.

## How a new version takes effect (section 29)

- **29.1:** the Licensor amends the Terms "by publishing a new dated version" at the Terms URL. A new version
  applies to a Subscription from its **next renewal or new Order Form**, and not during a term already paid for,
  unless the Customer agrees otherwise.
- **29.2:** no notice is required before a version applies. A Customer with a Paid Plan that does not accept a new
  version may terminate under section 12.5 (Termination after renewal).
- **So:** the required steps are to publish the dated version and update the page, and the effective date for a new
  Order Form (including a Paddle checkout) is the publication date. The five changes below affect only new Order
  Forms and renewals, and none changes a term already paid for. No notice is required, but a short courtesy email to
  Paid Plan Customers before an Enterprise renewal is advisable, because the seat certification (change 5) is new
  to them. Publication also needs the owner's go (L4.3 in [implementation-plan.md](implementation-plan.md)) and the
  version needs its own date and a changelog line on the page.

## Change 1. Referral and promotional discount (section 13.1; counsel-brief item 5)

**Current 13.1:** "The Customer purchases a Team Subscription online from the Merchant of Record, which acts as
seller of record. Section 11.5 (Resellers) applies to that purchase. The Fees are the Licensor's published prices,
and section 15 (Refunds) determines whether a refund is due."

**Proposed:** "... The Fees are the Licensor's published prices, **less any referral or promotional discount the
Licensor offers and the checkout or Order Form shows**, and section 15 (Refunds) determines whether a refund is
due."

**Why.** Today a referral discount is neither a published price nor a section 17 discount. The discount works
now only because a Paddle checkout page is an "Order Form" (section 2) that may carry a negotiated price (11.2) and
outranks the Terms (33.1). The carve-out removes that dependence. The discount is the Licensor's, applied by the
Merchant of Record. It must be disclosed as first year only, renewing at the then-current list price (14.1-14.2),
with no stacking with section 17 discounts. A "percent off" claim is measured against the real published price (Competition
Act s.74.01(2)-(3)).

## Change 2. False eligibility for a Free Plan (section 10.2; counsel-brief item 10)

**Current 10.2, first two sentences:** "The Licensor grants each Free Plan through a License Key or an Order Form.
If the Customer no longer meets the conditions of a Free Plan, the Licensor may end that Free Plan at the end of
the term of its License Key."

**Proposed:** add after them: "**The Customer represents that the information it gives to obtain a Free Plan is
true and complete. If it is not, the Licensor may end the Free Plan immediately by notice, and no Fee, credit or
other reward is earned on it.**"

**Why.** 10.2 covers a Customer that "no longer meets" the conditions, at the end of the key's term. It says
nothing about a claim that was false from the start, and keys verify offline (6.2), so without this the Licensor
cannot end a farmed evaluation seat or fake open-source claim early. The last clause supports the rule in the
referral and advocate agreements that Free Plans earn nothing; those agreements still say so themselves.

## Change 3. Enterprise reseller route (sections 2, 3.1, 3.4, 18.1; counsel-brief item 2)

Section 2 "Enterprise" and 3.1 say an Enterprise Subscription is "signed with, and invoiced directly by, the
Licensor", so a reseller-invoiced deal would be on no Paid Plan, and Part F (19 and 20) would arguably not apply.
These changes make it a Paid Plan. They take effect only when a reseller appointment exists (partner-program.md,
D1957); until then Enterprise stays directly invoiced.

| Where | Current | Proposed |
| --- | --- | --- |
| Section 2, "Enterprise" | "a Paid Plan bought through an Order Form signed with, and invoiced directly by, the Licensor (Part F)." | "a Paid Plan bought through an Order Form signed with the Licensor, or with a Reseller that the Licensor has authorised in writing to resell Enterprise Subscriptions, and invoiced by the Licensor or by that Reseller (Part F)." |
| Section 3.1, Enterprise bullet | "an annual contract, per User Seat, signed with and invoiced directly by the Licensor." | "an annual contract, per User Seat, signed with the Licensor, or with a Reseller the Licensor has authorised in writing to resell Enterprise Subscriptions, and invoiced by the Licensor or that Reseller." |
| Section 3.4, after "No Merchant of Record is involved." | (none) | add "Where the Enterprise Customer buys through a Reseller, section 11.5 (Resellers) applies." |
| Section 18.1 | "The Licensor contracts directly with each Enterprise Customer and invoices it directly, through an Order Form that refers to these Terms." | "Except where the Customer buys through a Reseller under section 11.5 (Resellers), the Licensor contracts directly with each Enterprise Customer and invoices it directly, through an Order Form that refers to these Terms." |

**Why.** With a reseller the licence is still between the Licensor and the end customer, who accepts these Terms
directly (1.2(a)/(b)); the reseller's terms cover only price, payment, invoicing and tax (11.5(a)). **To check with
counsel:** the 3.5 table of which Parts apply to each plan, section 18.2 (separate agreement), and whether
"authorised in writing" should refer to a defined "Reseller Agreement". The "Reseller" definition already requires
the Licensor to authorise the third party.

## Change 4. Order Form contents for Enterprise (section 11.2 or 19; counsel-brief item 12)

**Proposed:** add to section 19, after the first paragraph: "**An Enterprise Order Form shall state the plan, the
number of User Seats, the Subscription Term, the features, the per-User Seat fee, the minimum annual fee and the
currency, the payment terms, renewal and notice, and, where the Customer buys through a Reseller, the Reseller's
name and that price and payment are as the Reseller's terms provide. A reduction in User Seats under section 11.4
cannot take the Fees below the minimum annual fee.**"

**Why.** Section 19 leaves the minimum annual fee and payment terms to the Order Form without saying what the Order
Form must contain. Section 12.5 applies "in every case", so a minimum is firm for the first term only (an
Enterprise Customer can terminate within 30 days after a renewal for a pro-rata refund); public copy must not
describe it as a multi-year commitment. With a reseller, the minimum binds the reseller at wholesale, not the end
customer's price.

## Change 5. Annual Enterprise User certification (new text in section 19 or 20; counsel-brief item 15)

Section 16 (True-up) certifies Users for Team only. Section 20 (Audit) is the Licensor's only control on Enterprise
seats, and keys do not enforce seats (6.4), so the contract is the control. It reaches only "the Enterprise
Customer's records", once a year, and shifts cost only above its underpayment threshold.

**Proposed new paragraph, to follow section 20:** "**Annual certification.** At each anniversary of the start of
its Subscription Term, and at each renewal, an Enterprise Customer shall certify in writing the number of its Users
during the period that is ending. It shall buy, at the price stated in its Order Form, any User Seats it lacked, for
the period during which it lacked them. Where the Customer buys through a Reseller, the Customer gives the
certification to the Licensor and the Reseller, and shall buy any shortfall through the Reseller or, if the Reseller
fails to supply it, directly from the Licensor at the Licensor's then-current price. Section 20 (Audit) applies
whether or not the certification has been given.**"

**Why.** It mirrors section 16 for Enterprise and gives the Licensor a recovery route when a reseller under-reports.
It keeps the existing uncapped liability for overuse (24.3(c)), and should be read with section 20, whose
"shall pay for the unpaid User Seats" does not say to whom (the proposal names the payee). **Not in the Terms and
needed anyway:** audit, reporting and record-keeping duties on the reseller itself, which the Terms cannot impose
because the reseller is not a party; they belong in the reseller appointment.

## Not changed here

- Section 11.5 and the other reseller mechanics are unchanged (they already cover the Merchant of Record and any
  authorised Reseller).
- No change to Part G, to 30.2, or to section 28; the partner trademark licence, the sanctions covenants and the
  referral agreement sit in separate partner agreements, not in the Terms.
- The Quebec French-language clause is unchanged; a French version of the Terms is not part of this proposal.
