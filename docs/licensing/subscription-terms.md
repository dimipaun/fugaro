DRAFT FOR COUNSEL REVIEW: not reviewed by counsel; not in force; commercial terms in [BRACKETS] are the owner's to fill

# Fugaro Subscription Terms

**Status.** This is a first draft, written 2026-10-27 for counsel to review. It is not an offer,
and nobody has agreed to it. These are the "Fugaro Subscription Terms" that the business licence
in [`ee/LICENSE`](../../ee/LICENSE), [`console/LICENSE.md`](../../console/LICENSE.md) and
[`mobile/LICENSE.md`](../../mobile/LICENSE.md) (counsel's text of 2026-10-27, one wording in all
three files) refers to as published "at [URL TO BE PUBLISHED]". The licence files are unchanged
by this draft: the placeholder URL is replaced in a separate step, after counsel approves this
text and the owner publishes it ([implementation plan, L4.3 and L4.4](implementation-plan.md#l43--publish-the-subscription-terms-owner-run-after-counsel)).

**How to read it.** Sections 1 to 24 are the agreement text, in plain language for counsel to
tighten. Text in `[BRACKETS]` is a value or choice the owner fills. Paragraphs marked **Note for
counsel** or **Owner choice** are drafting notes, not agreement text, and are removed before
publication. The two question lists after the agreement are outside it.

**Words kept identical to the licence.** "Agreement", "Licensor", "production", "Subscription
Terms", "user seat" and "you" are defined in the licence files; this draft uses them with the same
meaning and does not redefine them in a different way. Where this draft adds a definition the
licence leaves to the Subscription Terms (the Licensor's identity, what a user seat is "as further
described"), it says so.

---

## 1. Parties and acceptance

1.1 These Subscription Terms are an agreement between **[LICENSOR LEGAL NAME]**, [a corporation
incorporated under the laws of [JURISDICTION]], with its address at [ADDRESS] (the **"Licensor"**,
which is the entity "identified as licensor in the Subscription Terms" that the licence names),
and the business entity that accepts them (the **"Customer"**, the "you" of the licence when it
acts for that entity).

> **Owner choice / Note for counsel.** The copyright line of the root `LICENSE` and of the three
> licence files names Dimi Paun personally. Whether the Licensor is that person or an entity he
> controls (and, if an entity, whether the copyright is assigned or licensed to it) is open; the
> licence defines the Licensor only through these Terms.

1.2 The Customer accepts these Terms by signing an Order Form that refers to them, by accepting
them online when it buys a Subscription, or by using the Software in production. The individual
who accepts them for the Customer confirms that they are authorised to bind it.

1.3 **Businesses only.** The Software is offered to businesses and other organisations, for use
in their trade, business or profession, and not to consumers. The Customer confirms that it is
not acquiring the Subscription as a consumer.

## 2. Definitions

Capitalised words have these meanings. Words defined in the licence keep their licence meaning.

- **"Affiliate"**: an entity that controls, is controlled by, or is under common control with a
  party, where "control" means owning more than 50% of the voting interests or otherwise having
  the power to direct its management, for as long as that control lasts.
- **"Agreement"**: as in the licence: these Terms, or another written agreement with the Licensor
  governing the use of the Software. Where the Customer has signed such another agreement, it
  governs instead of these Terms to the extent it says so.
- **"Contractor"**: a person or entity the Customer engages to work for it, acting on the
  Customer's behalf.
- **"Core"**: the parts of the Fugaro repository outside `ee/`, `console/` and `mobile/`, which
  are under the MIT licence in the repository's root `LICENSE` (with third-party code under its
  own licence, as [`LICENSING.md`](../../LICENSING.md) states).
- **"Customer Data"**: everything the Customer or its Users put into or produce with the Software
  or the Core: its repositories, source code, tasks, prompts, run records and logs, model output,
  credentials, cloud and billing data, and any personal information in any of them.
- **"Documentation"**: the documentation the Licensor publishes for the Software, including the
  repository's `README.md`, `SECURITY.md` and the documents under `docs/`, as updated from time
  to time.
- **"Fees"**: the amounts payable for a Subscription under an Order Form.
- **"Licence"**: the licence text in `ee/LICENSE`, `console/LICENSE.md` and `mobile/LICENSE.md`.
- **"License Key"**: a key the Licensor issues that enables the parts of the Software that require
  one (section 8).
- **"Order Form"**: an order, quote or online checkout page, accepted by both parties, that names
  the plan, the number of User Seats, the Subscription Term and the Fees.
- **"Production Use"**: use of the Software in production, which the Licence defines as any
  environment other than a development or testing environment. Using the Software on the
  Customer's real repositories, with real tasks whose output is meant to be merged or shipped, is
  Production Use. **[Note for counsel: the Licence's definition is environment-based; whether to
  add examples (for example, "a project that holds the Customer's real code is production even on
  a developer's laptop") or keep the Licence's wording alone.]**
- **"Software"**: as in the Licence: every file under the `ee/`, `console/` and `mobile/`
  directories of the Fugaro repository, in source form and every compiled form built from it,
  including code from those directories compiled into a released binary or application. Today
  that is the desktop console (from console 0.113.0) and the mobile app (from its first release);
  `ee/` holds no code yet. The Core is not part of the Software.
- **"Subscription"**: the Customer's right, for the Subscription Term, to make Production Use of
  the Software for the number of User Seats on its Order Form; in the Licence's words, a valid
  Fugaro Enterprise Edition subscription for the correct number of user seats.
- **"Subscription Term"**: the initial term and every renewal term (section 6).
- **"User"** and **"User Seat"**: a User is one person who uses the Software; one User Seat
  covers one User. Section 5 describes, as the Licence says these Terms do, how seats are counted.

## 3. What the Subscription grants

3.1 **Production Use.** During the Subscription Term, and subject to these Terms and payment of the
Fees, the Licensor grants the Customer a non-exclusive, non-transferable (except under
section 21), non-sublicensable licence to make Production Use of the Software, for its internal
business purposes, by no more Users than the User Seats on its Order Form.

3.2 **Development and testing stay free.** Nothing in these Terms restricts what the Licence
already allows without a subscription: using, copying and modifying the Software for development
and testing purposes. Those uses need no Subscription and no seat.

3.3 **The Core is not part of the Subscription.** The Core is under the MIT licence and stays
under it. These Terms neither grant nor restrict any right in the Core, and no Fee is charged for
it. Software releases and source published under the MIT licence before the business licence
applied to them (every console release up to and including 0.112.0, and the source of each of the
three directories as published before the change) stay under the MIT licence, as `LICENSING.md`
states, and these Terms do not reach them.

3.4 **Affiliates and Contractors.** As clause 4.2 of the Licence allows, the Customer may let its
Affiliates and Contractors use the Software for the Customer's own internal use. Each such person
who uses the Software is a User and takes a User Seat. The Customer is responsible for their
compliance with these Terms and the Licence. **[Owner choice: whether an Affiliate may instead buy
under the Customer's Order Form for its own internal use.]**

3.5 **Modifications.** The Customer may modify the Software as the Licence allows. Under clause 2.1
of the Licence, ownership of modifications and patches a subscriber makes is governed by these
Terms: **as between the parties, the Licensor owns all rights in modifications of and patches to
the Software made by or for the Customer, and the Customer assigns to the Licensor any rights it
has in them, to the extent they are derivative works of the Software** [Note for counsel: the
owner's ruling D311 is that the licensor keeps the rights in modifications; whether an assignment
is the right instrument, or an exclusive licence back, and how it treats code that is the
Customer's own and merely calls the Software]. The Customer may use its modifications under its
Subscription (clause 2.2 of the Licence). A modification submitted for inclusion in the Fugaro
repository goes through the Fugaro contributor licence agreement, as clause 2.1 of the Licence
says, and not through these Terms.

3.6 **Patents.** **[Owner choice / Note for counsel: clause 10.1 of the Licence grants patent rights
only as the Agreement sets out. Option A: no patent licence (the Licence's default). Option B: a
licence under the Licensor's patents that the Software, unmodified, necessarily infringes, for the
Customer's Production Use during the Subscription Term, ending if the Customer brings a patent
claim against the Licensor over the Software.]**

3.7 **What is not granted.** No right is granted except as stated in these Terms and the Licence.
In particular, the Customer may not: sell, resell, rent or sublicense the Software or access to
it; offer the Software to third parties as a hosted or managed service; remove or work around the
License Key check or any seat limit; or remove licence or copyright notices. No right is granted
in the Fugaro name or logo.

## 4. Plans and Order Forms

4.1 Each Subscription is bought through an Order Form, which states:

| Item | Value |
| --- | --- |
| Plan or tier | [TIERS: for example, Team / Business / Enterprise, and what each includes] |
| Price | [PRICE PER USER SEAT] per [BILLING PERIOD] |
| Currency | [CURRENCY] |
| Billing period | [BILLING PERIOD: monthly or annual] |
| Minimum seats | [MINIMUM, if any] |
| Initial term | [INITIAL TERM] |
| Renewal | [AUTO-RENEWAL: yes or no; renewal term length] |
| Support | [SUPPORT LEVEL, or "standard" per section 10] |
| Trial | [TRIAL OR FREE TIER, if any, and its limits] |

4.2 The Order Form may add terms for one Customer (for example, a negotiated price, a support
level or an SLA). Section 23 says which document wins if they conflict.

## 5. User Seats: counting, changes, true-up and audit

5.1 **What a seat is.** A User Seat is one named person who uses the Software. A person "uses the
Software" when they run or operate any part of it in Production Use: for example, opening the
desktop console against one of the Customer's production installations, signing in to the mobile
app against one, or using a feature that needs the License Key. **[Note for counsel and owner:
whether a person who only receives the result of a run (a reviewer of the resulting pull request)
without operating the Software uses it. The draft's reading is that they do not.]**

5.2 **Seats are for one person at a time.** A seat may not be shared between people or used by a
shared or generic account. The Customer may move a seat from one person to another when the first
person no longer needs it (for example, they leave or change roles), **[not more than once per
[PERIOD] for the same seat]**.

5.3 **Adding seats.** The Customer may add seats at any time through an Order Form. Added seats
are charged **[pro rata for the rest of the current billing period / from the next billing
period]** and end with the Subscription Term.

5.4 **Reducing seats.** The Customer may reduce the number of seats only with effect from the next
renewal, by notice before the renewal date (section 6.3).

5.5 **True-up.** If the Customer's Users exceed its User Seats, the Customer buys the missing seats
for the period of the excess, at the price on its Order Form, and the Licensor invoices them. The
Customer reports its seat usage **[once a year, within [30] days of the anniversary of its
Subscription / on request, not more than once a year]**.

5.6 **Light audit.** Not more than once in any twelve-month period, on at least **[30]** days'
written notice, the Licensor may ask the Customer to certify in writing, signed by an officer, the
number of its Users in the preceding period, with reasonable supporting records. Only if that
certification is not given, or the Licensor has reasonable grounds to believe it is materially
wrong, may an independent auditor bound by confidentiality review the Customer's relevant records,
during business hours and without unreasonable disruption. The Licensor pays for the review,
unless it shows an under-count of more than **[5%]**, in which case the Customer pays its
reasonable cost as well as the missing seats. The Software has no telemetry the Licensor could use
to count Users (section 9); seat counting rests on the Customer's own records.

## 6. Term and renewal

6.1 A Subscription starts on the date on the Order Form and lasts for the **[INITIAL TERM]**.

6.2 **[AUTO-RENEWAL: Option A: it renews automatically for successive terms of [RENEWAL TERM]
unless either party gives notice of non-renewal at least [NOTICE PERIOD] before the end of the
current term. Option B: it ends at the end of its term unless renewed by a new Order Form.]**

6.3 The Licensor tells the Customer of any price change for a renewal at least **[NOTICE PERIOD]**
before the renewal date; the new price applies from the renewal.

6.4 These Terms last as long as any Subscription under them.

## 7. Fees, payment and taxes

7.1 The Customer pays the Fees in **[CURRENCY]**, **[in advance for each billing period]**, within
**[PAYMENT TERMS: for example, 30 days of the invoice date]**, or by the payment method used at
checkout.

7.2 Late amounts **[may bear interest at [RATE] per month, or the highest rate allowed by law if
lower]**, after the Licensor gives notice that the amount is overdue.

7.3 **Taxes.** Fees exclude taxes. The Customer pays all sales, use, value-added, goods and
services and similar taxes on the Fees, except taxes on the Licensor's income. **[Note for counsel:
the tax regime is open. The Licensor is expected to be in Ontario, Canada, so GST/HST registration
and its application to Canadian and non-resident customers, place-of-supply rules for software
licences, withholding tax on cross-border licence payments, and any foreign VAT/GST registration
are open items.]**

7.4 **Refunds.** **[REFUND POLICY: for example, Fees are non-refundable except as section 13.3 or
19.2 says.]**

7.5 Fees are not contingent on any future feature or on any statement about the roadmap.

## 8. License Key and offline activation

8.1 Parts of the Software may need a License Key to turn on. The Licensor issues the License Key
for the Customer's Subscription, and it reflects the plan, the seats and the end of the
Subscription Term.

8.2 **Design intent, not implemented.** The intended model is described here so the Customer knows
what to expect; it is not built, and the Licensor may change it before it is
([licensing plan task L3.1](implementation-plan.md#l31--the-licence-key-design-note) and decision
D308 in [the reconciliation](reconciliation.md)):

- The License Key is a signed statement (who it is for, which features, when it expires) that the
  Software checks **offline**, against a public key built into the Software.
- The check makes **no network call**: the Software does not contact the Licensor to activate,
  validate or count anything ("no phone-home").
- After the key expires, a **grace period** of **[GRACE PERIOD]** applies before the features it
  enables turn off.
- Without a valid key, those features are off; the Core, which needs no key, keeps working.

8.3 The License Key is the Customer's Confidential Information and the Licensor's property. The
Customer may not share it outside its Users, alter it, or circumvent the check. Because the check
is offline, the Licensor relies on these Terms, and not on any technical control, for seat limits.

## 9. Customer Data and privacy

9.1 **The Software runs in the Customer's environment.** Fugaro is not a hosted service. The Core
runs each task as a job in the Customer's own cloud project (today, a Google Cloud project) and
against the Customer's own repositories; the desktop console is an application on the Customer's
machines that drives the Core's command-line tool; the mobile app reads the Customer's own Firebase
database; the optional credentials service and the coordinator are designed to run in the
Customer's own cloud project (both are part of the Core today, not of the Software). The Licensor
operates no server that these components report to.

9.2 **What the Licensor does not receive.** Through the Software, the Licensor does not receive the
Customer's repositories, source code, tasks, prompts, model output, credentials, run records,
logs or cloud data. Customer Data stays in systems the Customer controls and with the third
parties the Customer chooses (section 9.4).

9.3 **Telemetry.** As read from the repository on 2026-10-27:

- **Desktop console.** No telemetry, no analytics and no crash reporting; its outbound requests are
  the command-line tool's own, to the Customer's cloud (the console design, `docs/design/desktop-console.md`
  §7.4). That design also allows an update check that carries no identifier; **UNVERIFIED**: no
  updater was found in the console's main-process code on that date.
- **Mobile app.** Its declared dependencies include no analytics, crash-reporting or push library
  (`mobile/package.json`). **UNVERIFIED for a released build**: no mobile release exists yet, and
  crash reporting for the app has been discussed; if any is added it is described in the
  Documentation and the store disclosures before it ships.
- **License Key.** No phone-home (section 8.2).
- **The command-line tool (Core, outside these Terms).** Checks at most once a day for a newer
  release with a plain request to the public release endpoint of the code host, carrying no
  identifying data (`internal/updatecheck`); the Licensor does not receive it.

**Commitment [Owner choice]:** if the Licensor adds any telemetry to the Software, it is off unless
the Customer turns it on (opt-in), and what it sends is described in the Documentation before the
release that adds it.

9.4 **Third parties the Customer chooses.** The Software and the Core send the Customer's code and
prompts to the AI model providers, code hosts and cloud providers the Customer configures, under the
Customer's own accounts and those providers' terms. The Licensor is not a party to those terms and
does not process that data. Choosing which providers may see which repositories (for example, the
Core's `allow_data_to` setting) is the Customer's decision.

9.5 **Data the Licensor does handle.** The Licensor holds the business contact, billing and
order information needed to sell and support the Subscription (names, business email addresses,
seat counts, invoices), and anything the Customer chooses to send it for support (section 10.3). It
handles that information under its privacy notice at **[PRIVACY NOTICE URL]** and applicable law,
and uses it only to provide the Subscription, support and billing, and to meet legal duties.
**[Note for counsel: PIPEDA applies to that information; whether GDPR or other laws apply depends
on where customers are; a data processing addendum should be needed only if the Licensor receives
personal information in support data.]**

## 10. Support and updates

10.1 **Support.** The Licensor provides **[SUPPORT LEVEL: for example, email support on business
days, with a target first response of [TIME]]**. There is no service-level agreement unless an
Order Form includes one.

10.2 **Updates.** During the Subscription Term the Customer may use every new version of the
Software the Licensor releases, on the same terms. The Licensor may change or remove features of
the Software between versions. The Licensor supports **[the latest release only, as `SECURITY.md`
says for the Core today / the latest [N] releases]**.

10.3 **Support data.** If the Customer sends logs, configuration or code to get support, it sends
only what is needed, removes secrets first, and the Licensor treats it as Confidential Information
and deletes it **[within [PERIOD] after the issue is closed]**.

10.4 **After the Subscription ends.** **[Owner choice.]** Option A (the Licence's default, clause
11.1): the right to Production Use ends when the Subscription ends, for every version. Option B: a
version released while the Subscription was active keeps working in production after it ends,
without updates or support (a "perpetual fallback"); this needs an express grant here, because
clause 11.1 of the Licence otherwise ends production use, and clause 12.1 lets the Agreement
prevail. Either way, development and testing use stays free.

## 11. Intellectual property

11.1 **The Customer's.** The Customer keeps all rights in Customer Data and in its own code,
including code that AI agents write in its repositories through the Software or the Core. The
Licensor claims no rights in them. **[Note for counsel: whether to say anything about the
copyright status of AI-generated code; the draft makes no promise either way.]**

11.2 **The Licensor's.** The Licensor and its licensors keep all rights in the Software, the License
Keys and the Documentation, subject only to the rights these Terms and the Licence grant.

11.3 **Feedback.** If the Customer gives the Licensor suggestions or feedback about the Software,
the Licensor may use them freely, without obligation. Code contributions to the repository go
through the contributor licence agreement, not this clause.

## 12. Customer responsibilities

12.1 The Customer is responsible for: its Users and their compliance; configuring and securing its
own cloud project, repositories, credentials and model-provider accounts as the Documentation
describes, including the trust model and known limits in `SECURITY.md`; keeping the spend limits
it needs with its cloud and model providers; and the content of the tasks it runs.

12.2 **Third-party charges.** Cloud, model-provider and code-host charges incurred by running the
Software or the Core are the Customer's, under its own accounts. The Core's budget caps are a tool
against mistakes and are not a guarantee that spend stays under a cap (`SECURITY.md`, "Money").

## 13. Warranties and disclaimers

13.1 **Mutual.** Each party confirms it has the authority to enter into these Terms.

13.2 **AI output.** The Software drives AI coding agents that run without permission prompts in the
Customer's cloud. **Their output can be wrong, insecure, incomplete or infringing**, and every run
ends in a pull request precisely so that a person reviews it. The Customer is responsible for
reviewing and testing all output before merging, deploying or relying on it.

13.3 **[Owner choice: a limited warranty, for example that the Software materially conforms to the
Documentation for [30] days after purchase, with the remedy of a fix or, failing that, a refund of
the Fees for the non-conforming part.]**

13.4 **Disclaimer.** Except as section 13 says, and to the maximum extent the law allows, the
Software, the License Keys and the Documentation are provided **"AS IS"**, without warranty of any
kind, express or implied, including merchantability, fitness for a particular purpose,
non-infringement, or that the Software is error-free or uninterrupted. This matches clause 13 of
the Licence; for a subscriber, clause 13.2 of the Licence lets this section prevail over it.

## 14. Limitation of liability

14.1 **Cap.** Each party's total liability arising out of or relating to these Terms is limited to
**[CAP: the Fees the Customer paid in the 12 months before the event giving rise to the claim]**.

14.2 **Excluded damages.** Neither party is liable for indirect, incidental, special, consequential
or punitive damages, or for lost profits, revenue, data or goodwill, or for the cost of cloud,
model-provider or other third-party charges, even if advised of the possibility.

14.3 **Exceptions.** Sections 14.1 and 14.2 do not limit: the Customer's duty to pay Fees; a party's
liability for breach of section 16 (confidentiality); the Customer's liability for use beyond the
licence granted (including seats over its Subscription); **[indemnity obligations under section
15]**; or liability that cannot be limited by law.

## 15. Indemnification

**[Placeholder.]** **Note for counsel:** the draft deliberately offers no indemnity. Options: none;
a narrow IP indemnity by the Licensor against third-party claims that the unmodified Software
infringes, excluding claims arising from AI output, the Core, third-party components, modifications
or combinations, with the usual remedies (modify, replace, or terminate and refund); and a
reciprocal indemnity by the Customer for Customer Data and its use in breach of these Terms. A
small licensor's insurance position matters here.

## 16. Confidentiality

16.1 "Confidential Information" is non-public information one party discloses to the other that is
marked confidential or that a reasonable person would understand to be confidential, including
License Keys, non-public pricing and support data. It excludes information that is public through
no fault of the recipient, was already known to it, is independently developed, or is lawfully
received from a third party. The Software's source code, which is published, is not Confidential
Information.

16.2 The recipient uses Confidential Information only for these Terms, protects it with reasonable
care, and shares it only with people who need it and are bound to keep it confidential. It may
disclose it when the law requires, with notice where lawful. These duties last **[3]** years after
the Terms end, and for as long as the information is a trade secret.

## 17. Suspension and termination

17.1 **Non-payment.** If an undisputed amount is more than **[30]** days overdue after notice, the
Licensor may suspend the Subscription (and decline to issue or renew License Keys) until it is
paid.

17.2 **Breach.** Either party may terminate a Subscription if the other materially breaches these
Terms and does not cure the breach within **[30]** days of written notice.

17.3 **Insolvency.** Either party may terminate if the other becomes insolvent, makes an assignment
for the benefit of creditors, or is subject to bankruptcy or receivership proceedings not dismissed
within **[60]** days.

17.4 **Effect.** When a Subscription ends: the right to Production Use ends (subject to section 10.4
Option B, if chosen); the Customer stops Production Use and, as clause 11.3 of the Licence requires
when it applies, destroys copies of the Software, **except** that it may keep copies for
development and testing (which the Licence allows without a subscription) and records it needs for
legal purposes; and unpaid Fees for the period up to the end fall due. **[Owner choice: whether
the Customer gets a pro-rata refund of prepaid Fees when it terminates for the Licensor's
breach.]**

17.5 **Survival.** Sections 2, 3.5, 5.5 and 5.6 (for the period of the Subscription), 7 (amounts
due), 11, 13.4, 14, 15, 16, 17.4, 17.5 and 19 to 24 survive.

## 18. Export control and sanctions

Each party complies with the export control and sanctions laws that apply to it, including those
of Canada and, where they apply, the United States. The Customer will not use or allow access to
the Software in, or by persons in, a country or by a person subject to comprehensive sanctions
under those laws.

## 19. Changes to these Terms

19.1 The Licensor may update these Terms by publishing a new version at **[URL TO BE PUBLISHED]**
with its date. A new version applies to a Subscription from its next renewal (or new Order Form),
not during a term already paid for, unless the Customer agrees.

19.2 The Licensor gives at least **[NOTICE PERIOD]** notice of a material change before the renewal
it applies to. If the Customer does not accept it, it may decline to renew.

## 20. Publicity

Neither party uses the other's name or logo in marketing without the other's prior written
consent, **[except that the Licensor may list the Customer's name as a customer, which the Customer
may refuse by notice]**.

## 21. Assignment

Neither party may assign these Terms without the other's written consent, except to a successor in
a merger, acquisition or sale of all or substantially all of its business or assets related to
these Terms, on notice. An assignment in breach of this section is void.

## 22. Notices

Notices under these Terms are in writing, to the addresses on the Order Form (for the Licensor,
**[NOTICE EMAIL AND ADDRESS]**), and take effect on receipt. Email is sufficient for routine notices;
notices of breach or termination also go **[by courier or registered mail]**.

## 23. Order of precedence and entire agreement

23.1 If documents conflict, this order applies: (1) a signed Order Form, for that Order Form's
Subscription; (2) these Terms; (3) the Documentation. Between these Terms and the Licence, the
Licence's clause 12.1 already says the Agreement prevails for the party that entered into it.

23.2 These Terms, the Order Forms and the Licence are the whole agreement between the parties about
the Software and replace any earlier proposals or understandings about it. Terms in the Customer's
purchase order or other paperwork do not apply even if accepted. An amendment is valid only in
writing signed by both parties, except an update under section 19.

23.3 **General.** If a provision is unenforceable, the rest stands and the provision is enforced as
far as it can be. A failure to enforce a right is not a waiver. The parties are independent
contractors. Neither party is liable for a delay caused by events beyond its reasonable control.
**[Note for counsel: whether to exclude the UN Convention on Contracts for the International Sale
of Goods, and whether a language clause is needed for customers in Quebec.]**

## 24. Governing law and venue

These Terms are governed by the laws of the Province of Ontario and the federal laws of Canada
applicable in it, without regard to conflict-of-laws rules. The courts of Ontario have exclusive
jurisdiction over any dispute arising out of or relating to these Terms, and each party submits to
that jurisdiction. This matches clause 14.1 of the Licence.

---

## Questions for the owner (not part of the agreement)

Every commercial decision the draft leaves in brackets:

1. **Pricing model and tiers**: which plans exist and what each includes.
2. **Metric**: per User Seat (as the Licence says today), or per installation or project; a change
   of metric also needs the Licence's seat wording to change.
3. **Price per seat** and **currency** (CAD, USD or both).
4. **Billing period**: monthly, annual, or both (with an annual discount?).
5. **Initial term, auto-renewal** and the non-renewal **notice period**.
6. **Free tier or trial**: none (development and testing are already free), a time-limited
   production trial, or a small free seat count.
7. **After lapse** (section 10.4): production use stops (the Licence's default) or a perpetual
   fallback for versions released during the term.
8. **Support and SLA**: one support level or tiers; whether any plan carries an SLA; how many
   releases are supported.
9. **Contracting entity** (section 1.1): the owner personally or a company, and if a company, how
   the copyright gets to it.
10. **Negotiated agreements**: whether enterprise customers may sign their own agreement (the
    Licence already allows "another written agreement").
11. **Refund policy** (section 7.4) and whether there is a limited warranty (section 13.3).
12. **Audit strength** (section 5.6): self-certification only, or the auditor fallback; the
    under-count threshold; the true-up cadence.
13. **Free use for academic, non-profit or open-source projects**: whether such production use is
    free, and how it is granted (a free Order Form, or a clause here).
14. **Seat reassignment limit**, **minimum seats**, and whether a pull-request reviewer who never
    opens the Software needs a seat (section 5.1).
15. **Affiliates** buying under one Order Form (section 3.4), and **customer-name publicity**
    (section 20).
16. **Patent licence** (section 3.6, option A or B), and the **opt-in telemetry commitment**
    (section 9.3).
17. **Licence-key grace period** (section 8.2) and the late-payment interest rate.

## Questions for counsel (not part of the agreement)

1. **Enforceability** of a seat-based subscription over source-available software across
   jurisdictions, where the source is public, development and testing are free, and the only
   technical control is an offline key.
2. **Consumers vs businesses**: the draft is B2B only (section 1.3); whether that is enough to keep
   consumer-protection law out, and how online acceptance should be shown.
3. **Data protection**: PIPEDA for the Licensor's own customer and support information; whether
   GDPR or other laws reach it; whether section 9's statements about the Software are adequate
   (and safe) given that the Software processes Customer Data only in the Customer's environment;
   and the separate privacy notice the website already needs for its consent-gated analytics.
4. **AI-output disclaimers** (section 13.2) and liability for AI output, including infringing
   output and the copyright status of AI-written code (section 11.1).
5. **Export control and sanctions** (section 18): whether the Software's cryptography or anything
   else needs classification or reporting in Canada or the United States.
6. **Interaction with the Licence and the CLA**: that the defined terms match the Licence; the
   modification-ownership clause (section 3.5) against the Licence's clause 2.1 and the D311
   ruling; the patent clause against the Licence's clause 10.1; the destruction-on-termination
   carve-outs against clause 11.3; and the boundary with the contributor licence agreement being
   drafted separately.
7. **Tax treatment** (section 7.3): GST/HST and place of supply, withholding on cross-border
   payments, foreign VAT/GST registration thresholds.
8. **Source-code escrow**: whether to offer it at all, given that the source is published.
9. **Indemnity** (section 15): whether to offer one and how narrow.
10. **Licensor identity**: the Licence defines the Licensor only through these Terms while its
    copyright line names an individual; whether that is sound until an entity is chosen.
11. **Unilateral changes** (section 19): whether renewal-only effect with notice is enforceable as
    drafted.
