# Fugaro Subscription Terms

**Status.** This is counsel's restructured text, written 2026-10-28 (the date of the document
counsel returned, which carries no date or version number in its own text). It replaces the draft
of 2026-10-27 that followed the owner's answers, and reorganises the agreement by plan: Parts A to
G, sections 1 to 34. Counsel has approved the text, including section 7.3 (Crash reports and
diagnostics) as revised on 2026-10-31 to the owner's ruling that crash reporting is opt-in
and off by default. It is not an offer, and nobody has agreed to it: the Licensor's name, jurisdiction, address, privacy notice URL
and notice address were filled in by the owner on 2026-10-30. These are the "Fugaro Subscription Terms" that the
business licence in [`ee/LICENSE`](../../ee/LICENSE), [`console/LICENSE.md`](../../console/LICENSE.md)
and [`mobile/LICENSE.md`](../../mobile/LICENSE.md) refers to as published "at https://fugaro.dev/legal/subscription-terms";
the text is in force only once the owner publishes it
([implementation plan, L4.3](implementation-plan.md#l43--publish-the-subscription-terms-owner-run-after-counsel)).

**How to read it.** Parts A to G are the agreement text, as counsel wrote it, with Markdown for
counsel's formatting. Two figures in counsel's text are not published in this repository and stand
as bracketed items instead: the late-payment interest rate (section 12.2) and the liability cap for
the Community Plan, Free Plans and Beta Features (section 10.5); the owner decides whether they
are published. Counsel's table in section 3.5 is reproduced as a Markdown table. The list after the
agreement is outside it.

---

# Part A. General

## 1. Parties and acceptance

1.1 These Terms are an agreement between **DSSD SOFTWARE INC**, a corporation incorporated
under the laws of Ontario, of 1621 Maple Ridge Dr., Mississauga, Ontario, L4W 3R6, Canada (the **"Licensor"**), and the business that
accepts them (the **"Customer"**).

1.2 **Acceptance.** The Customer accepts these Terms when it first does any of the following: (a)
signs an Order Form that refers to them; (b) accepts them online when it buys or obtains a
Subscription, including a Free Plan under section 10.2 (Free Plans); or (c) makes Production Use of
the Software. The individual who accepts these Terms on the Customer's behalf represents that they
are authorised to bind the Customer.

1.3 **Businesses only.** The Licensor offers the Software only to businesses and other
organisations, for use in their trade, business or profession, and not to consumers. The Customer
represents that it is not acquiring the Subscription as a consumer for personal use.

## 2. Definitions

Capitalised words have these meanings.

- **"Affiliate"**: an entity that controls, is controlled by, or is under common control with a
  party, where "control" means owning more than 50% of the voting interests or otherwise having
  the power to direct its management, for as long as that control lasts.
- **"Agreement"**: these Terms, or another written agreement with the Licensor governing the use of
  the Software. Where the Customer has signed such another agreement, it governs instead of these
  Terms to the extent it says so.
- **"Beta Feature"**: a feature or version of the Software identified as beta, preview,
  experimental or similar (section 10.6 (Beta Features)).
- **"Command-line tool"**: the `fugaro` command-line tool, which is part of the Core.
- **"Community"** or **"Community Plan"**: the plan with no Fee, including the Free Plans
  (section 10 (Community and Free Plans)).
- **"Contractor"**: a person or entity the Customer engages to work for it, acting on the
  Customer's behalf.
- **"Core"**: the parts of the Fugaro Repository outside `ee/`, `console/` and `mobile/`, which are
  under the MIT licence in the Fugaro Repository's root `LICENSE` (with third-party code under its
  own licence, as [`LICENSING.md`](../../LICENSING.md) states).
- **"Customer Data"**: everything the Customer or its Users put into or produce with the Software
  or the Core: its repositories, source code, tasks, prompts, run records and logs, model output,
  credentials, cloud and billing data, and any personal information in any of them.
- **"Documentation"**: the documentation the Licensor publishes for the Software, including the
  Fugaro Repository's `README.md`, `SECURITY.md` and the documents under `docs/`, as updated from
  time to time.
- **"Effective Date"**: for a Customer that signs an Order Form for an Enterprise Subscription, the
  date that Order Form is signed by both parties; for every other Customer, the earlier of the date
  it accepts these Terms and the date it first makes Production Use of the Software.
- **"Enterprise"**, **"Enterprise plan"** or **"Enterprise Subscription"**: a Paid Plan bought
  through an Order Form signed with, and invoiced directly by, the Licensor (Part F).
- **"Fees"**: the amounts payable for a Subscription under an Order Form.
- **"Free Plan"**: a Subscription for which no Fee is charged, under section 10.2 (Free Plans).
- **"Fugaro Repository"**: the collection of source code files, with their full history of
  changes, in which the Licensor publishes and maintains Fugaro, on GitHub or a similar
  code-hosting service, at the location the Licensor designates.
- **"Job"**: a single task that the Core runs in the Customer's cloud project.
- **"Key Service"**: the hosted service the Licensor operates to issue License Keys (section 6.3
  (Issue of License Keys)).
- **"License Key"**: a signed key the Licensor issues to a Customer for its organisation that
  enables the Software for the Customer's Users and states the number of User Seats (section 6
  (License Keys and offline activation)).
- **"Modifications"**: modifications of and patches to the Software made by or for the Customer.
- **"Merchant of Record"**: the Reseller that the Licensor appoints to sell Team Subscriptions
  online as seller of record (section 13.1 (Merchant of Record)).
- **"Order Form"**: an order, quote or online checkout page, accepted by both parties, that names
  the plan, the number of User Seats, the Subscription Term, the features of the plan and the Fees.
- **"Paid Plan"**: a Team or Enterprise Subscription for which Fees are charged, including at a
  discount.
- **"Reseller"**: a reseller, distributor, cloud marketplace or other third party that the Licensor
  authorises to sell Subscriptions to customers as seller, including the Merchant of Record
  (section 11.5 (Resellers)).
- **"Production Use"**: use of the Software in any environment other than a development or testing
  environment. Using the Software on the Customer's real repositories, with real tasks whose output
  is meant to be merged or shipped, is Production Use.
- **"Software"**: every file under the `ee/`, `console/` and `mobile/` directories of the Fugaro
  Repository, in source form and every compiled form built from it, including code from those
  directories compiled into a released binary or application.
- **"Subscription"**: the Customer's right, for the Subscription Term, to make Production Use of
  the Software for the number of User Seats on its Order Form. A Free Plan is a Subscription.
- **"Subscription Term"**: the initial term, which begins on the Effective Date or, for a
  Subscription purchased after the Effective Date, on the date of that purchase, and every renewal
  term (section 14 (Term and renewal) and section 19 (Enterprise commercial terms)).
- **"Team"**, **"Team plan"** or **"Team Subscription"**: a Paid Plan bought online from the
  Merchant of Record and billed monthly or annually (Part E).
- **"User"** and **"User Seat"**: a User is one person who uses the Software or launches Jobs as
  section 5.1 (Seats required) says; one User Seat covers one User. Section 5 (User Seats)
  describes how seats are counted.

## 3. Plans and the terms that govern each

3.1 **The plans.** The Licensor offers three plans:

- **Community** (no Fee): the Core; development and testing use of the Software; and the Free
  Plans of section 10.2 (Free Plans).
- **Team**: bought online from the Merchant of Record, per User Seat, billed monthly or annually.
- **Enterprise**: an annual contract, per User Seat, signed with and invoiced directly by the
  Licensor.

3.2 **Community.** Parts A, B, C and G govern a Community Customer, including a Customer on a Free
Plan. Parts D, E and F do not apply to it, and no Fees, payment, refunds, true-up or audit apply. A
Free Plan that provides the Team plan's features under section 10.2(b) or (c) (Free Plans) remains
a Community Plan; Part E applies only to a paid Team Subscription.

3.3 **Team.** Parts A, B, D, E and G govern a Team Customer. A Team Subscription is purchased from
the Merchant of Record, a Reseller, under section 11.5 (Resellers) and section 13 (Buying the Team
plan). A Team Customer that receives a nonprofit or startup discount under section 17 (Discounts
and trials) is a paid Team Customer. Section 10.5 (Limits for Community, Free Plans and Beta
Features) and section 10.6 (Beta Features) also apply to any Beta Feature it uses.

3.4 **Enterprise.** Parts A, B, D, F and G govern an Enterprise Customer, through an Order Form
that refers to these Terms. If the Enterprise Customer and the Licensor sign another written
agreement, that agreement governs instead of these Terms to the extent it so provides (section 18
(Contracting)). No Merchant of Record is involved. Section 10.5 (Limits for Community, Free Plans
and Beta Features) and section 10.6 (Beta Features) also apply to any Beta Feature it uses.

3.5 **Summary.** The following table summarises which Parts apply to each plan. If the table
conflicts with section 3.2 (Community), section 3.3 (Team) or section 3.4 (Enterprise), those
sections prevail.

| Part | Community | Team | Enterprise |
| --- | --- | --- | --- |
| A. General (1 to 3) | Yes | Yes | Yes |
| B. Every plan (4 to 9) | Yes | Yes | Yes |
| C. Community and Free Plans (10) | Yes | No | No |
| D. Paid plans (11 and 12) | No | Yes | Yes |
| E. Team (13 to 17) | No | Yes | No |
| F. Enterprise (18 to 20) | No | No | Yes |
| G. Legal terms (21 to 34) | Yes | Yes | Yes |
| Other terms | None | Merchant of Record's buyer terms, for the sale only | A signed agreement, if any, to the extent it says so |

# Part B. Terms for every plan

## 4. What the Subscription grants

4.1 **Production Use.** Subject to these Terms and to payment of any applicable Fees, the Licensor
grants the Customer, for the Subscription Term, a non-exclusive, non-transferable (except under
section 31 (Assignment)), non-sublicensable licence to make Production Use of the Software for its
internal business purposes, by no more Users than the number of User Seats stated in its Order
Form or License Keys.

4.2 **Development and testing.** The Customer may use, copy and modify the Software for development
and testing purposes without a Subscription or a User Seat.

4.3 **Affiliates and Contractors.** The Customer may permit its Affiliates and Contractors to use
the Software solely for the Customer's internal business purposes. Each individual at an Affiliate
or Contractor who requires a User Seat under section 5.1 (Seats required) is a User and shall be
counted against the Customer's User Seats. Under a Paid Plan, an Affiliate may also purchase under
the Customer's Order Form for its own internal business purposes, and its Users shall be counted
against that Order Form's User Seats. The Customer is responsible for its Affiliates' and
Contractors' compliance with these Terms.

4.4 **Modifications.** The Customer may modify the Software and submit a Modification for inclusion
in the Fugaro Repository which is governed by the Fugaro contributor licence agreement.

4.5 **Patents.**

(a) Subject to these Terms, the Licensor grants the Customer a non-exclusive, non-transferable
(except under section 31 (Assignment)), non-sublicensable, royalty-free licence under the Licensor
Patents to make, use and import the Software, solely to the extent of, and for as long as, the
rights granted under section 4.1 (Production Use) and section 4.2 (Development and testing).

(b) "Licensor Patents" means the patent claims owned or licensable by the Licensor or its
Affiliates, now or in future, that are necessarily infringed by the Software as released by the
Licensor, alone. They exclude claims infringed only by a Modification, or by a combination of the
Software with anything the Licensor did not supply.

(c) Defensive termination. If the Customer or any of its Affiliates asserts in writing, or brings
any claim, counterclaim or proceeding alleging, that the Software or the Core infringes any patent,
the licence under section 4.5(a) ends automatically on the date of that assertion.

(d) No other patent rights. Except as stated in this section 4.5, these Terms grant no patent
licence, whether by implication, estoppel or otherwise. Nothing in this section limits any right
granted under the MIT licence for the Core.

4.6 **Reserved rights and restrictions.** The Licensor reserves all rights not expressly granted in
these Terms. The Customer shall not, and shall not permit any other person to: (a) sell, resell,
rent, lease or sublicense the Software or access to it; (b) provide the Software to third parties
as a hosted or managed service; (c) remove, disable or circumvent the License Key verification or
any User Seat limit; or (d) remove any licence or copyright notice. These Terms grant no right in
the Fugaro name or logo.

## 5. User Seats

5.1 **Seats required.** The Customer must obtain one User Seat for each individual who, on the
Customer's behalf, (a) initiates a Job by any means, including through the desktop console, the
mobile app, the Command-line tool, or any AI agent or automated process directed by that
individual, or (b) otherwise makes Production Use of the Software. Notwithstanding the foregoing,
no User Seat is required for an individual who (i) only views information while holding the
viewer role (section 5.3 (Roles)), or (ii) only reviews pull requests opened by Jobs and does not
otherwise use the Software or initiate Jobs.

5.2 **Command-line tool.** The Command-line tool is licensed under the MIT licence and is not part
of the Software. Section 5.1 (Seats required) determines only the number of User Seats, and
therefore the Fees, payable under the Customer's Subscription, and does not limit any right granted
under the MIT licence.

5.3 **Roles.** The Software provides viewer, operator and administrator roles, enforced across the
whole system, including its cloud components. An individual who holds only the viewer role does
not require a User Seat. Each individual who holds the operator or administrator role is a User and
requires a User Seat under the Customer's License Key (section 6 (License Keys and offline
activation)).

5.4 **One individual per seat.** Each User Seat may be assigned to only one individual at a time,
and shall not be shared between individuals or used by a shared or generic account. The Customer
may reassign a User Seat to another individual no more than once in any 30-day period, except that
it may reassign a User Seat at any time when the individual holding it leaves the Customer's
organisation.

5.5 **Seat estimate.** The Software displays, beside the number of licensed User Seats, an
advisory estimate of the number of individuals who initiate Jobs, calculated on the Customer's
machines from the Customer's own records, without storing the estimate or sending it to the
Licensor. The estimate is provided for convenience only and does not determine the number of User
Seats required. The Customer remains responsible for counting its Users from its own records.

## 6. License Keys and offline activation

6.1 **License Keys.** The Licensor shall provide a License Key, under section 6.3 (Issue of License
Keys), to each Customer under a Paid Plan or a Free Plan, and the Software requires a valid License
Key to operate, subject to section 4.2 (Development and testing). Each License Key states the plan,
the features it enables, the number of User Seats of the Subscription and its expiry date.

6.2 **Verification.** The Software verifies License Keys without contacting the Licensor or the Key
Service, so an outage of the Key Service does not affect any License Key already installed. When a
License Key expires, a grace period of 30 days applies, after which section 9 (After the
Subscription ends) applies.

6.3 **Issue of License Keys.** The Licensor issues License Keys through the Key Service, a
self-serve website, as follows:

- **Open-source teams** (section 10.2(b) (Free Plans)): the applicant provides the URLs of its
  repositories and an email address. The Key Service verifies that every repository is public under
  a licence approved by the Open Source Initiative and, if so, issues a License Key valid for one
  year.
- **Free seat** (section 10.2(a) (Free Plans)): the applicant provides a work email address on the
  organisation's own domain; addresses at free email providers are not accepted. The Key Service
  issues a renewable License Key valid for three months, and issues no more than one free seat per
  legal person, determined by email domain and, where it is known, by the legal person's identity.
- **Paid Subscriptions**: the Licensor issues the Customer a License Key stating the User Seats
  purchased, following purchase.
- **Other requests** (for example, from an individual without an email address on their
  organisation's domain, or for a multi-seat trial or an academic or nonprofit application): the
  applicant may submit the manual request form. The Licensor may approve or decline any such
  request at its discretion.

6.4 **Protection of License Keys.** Each License Key is the Licensor's property and the Licensor's
Confidential Information. The Customer shall not share a License Key with anyone other than its
Users, alter it, or circumvent its verification. The Customer acknowledges that, because
verification is offline, the Licensor relies on these Terms, and not on technical controls, to
enforce User Seat limits.

## 7. Customer Data and Privacy

7.1 **Customer environment.** The Software is not a hosted service. The Core runs each Job in the
Customer's own cloud project and against the Customer's own repositories; the desktop console runs
on the Customer's machines and operates the Command-line tool; the mobile app reads the Customer's
own Firebase database; and the optional credentials service, which is part of the Core and not of the
Software, and the coordinator, which is part of the Software, run in the Customer's own cloud project. Subject to section 7.3
(Crash reports and diagnostics), none of these components reports to a server operated by the
Licensor, and the Software does not contact the Key Service.

7.2 **Customer Data.** Except as described in section 7.3 (Crash reports and diagnostics), the
Licensor does not receive, through the Software, the Customer's repositories, source code, tasks,
prompts, model output, credentials, run records, logs or cloud data. Customer Data remains in
systems the Customer controls and with the third parties the Customer chooses under section 7.4
(Third parties the Customer chooses).

7.3 **Crash reports and diagnostics.** The Software does not send crash reports or other
diagnostic data unless a User turns crash reporting on. It is off by default, and each User can
turn it on or off at any time in the desktop console's settings. When it is on, the Software sends only the
type of an error and the source locations inside the Software's own code, to the Licensor's
crash-reporting processor (Sentry, hosted in the European Union). It does not send error messages,
file paths, user names, environment values or host names, and it never sends the Customer's
repository contents, credentials or task text.

7.4 **Third parties the Customer chooses.** The Software and the Core send the Customer's code and
prompts to the AI model providers, code hosts and cloud providers that the Customer configures,
under the Customer's own accounts and subject to those providers' terms. The Licensor is not a
party to those terms and does not process that data. The Customer is solely responsible for
deciding which providers may access which repositories, for example through the Core's
`allow_data_to` setting.

7.5 **Information the Licensor handles.** The Licensor collects and processes: (a) the business
contact, billing and order information it needs to sell and support the Subscription, such as
names, business email addresses, User Seat counts and invoices; (b) the information the Key Service
collects to issue a License Key, namely the email address of the individual signing up, the
organisation's domain and, for open-source teams, the repository URLs; (c) the crash reports and
diagnostic data described in section 7.3 (Crash reports and diagnostics); and (d) any information
the Customer chooses to send it for support under section 8.3 (Support data). The Licensor shall
use that information only to provide the Subscription, License Keys, support and billing, to verify
eligibility for a Free Plan and enforce the limit of one free seat per legal person, to contact
the Customer and its Users about the Customer's License Key, and to comply with its legal
obligations. The Licensor shall handle all of the information it collects and processes in
accordance with its privacy notice at **https://fugaro.dev/privacy** and applicable law.

## 8. Support and updates

8.1 **Support.** The Licensor shall provide support according to the Customer's plan:

- **Community**, including Free Plans unless their Order Form provides otherwise: support through
  public GitHub issues, with no response target.
- **Team**: support by email, with a target first response by the next business day, during
  business hours in Toronto, Canada.
- **Enterprise**: target first responses, during business hours, within four business hours for a
  P1 issue (the Software or the Core is unusable in production and no workaround exists), within
  one business day for a P2 issue (a major function is impaired), and within two business days for
  a P3 issue (any other issue).

The Licensor does not provide round-the-clock support. Response times are targets and not
guarantees. No service-level commitment applies.

8.2 **Updates.** During the Subscription Term, the Customer may use, on these Terms, each new
version of the Software that the Licensor releases during the Subscription Term. The Licensor may change or remove features of the Software between versions. The
Licensor shall support the current minor release of the Software and the minor release immediately
before it.

8.3 **Support data.** If the Customer sends logs, configuration or code to obtain support, it shall
send only what is necessary and shall first remove any secrets. The Licensor shall treat that
material as Confidential Information.

## 9. After the Subscription ends

9.1 **End of Production Use.** The Customer's right to make Production Use of the Software, for
every version of the Software, ends when its Subscription ends, and no perpetual licence survives.
However, the Licensor grants the following rights after that time:

- **Grace period.** For 30 days after the Customer's License Key expires, its Users may continue
  to use the Software as that License Key permitted.
- **Read-only use.** After the grace period, each User may continue to use the desktop console and
  the mobile app in read-only mode, to view the Customer's installations, Jobs and their records.
  In read-only mode, the User may not initiate or modify anything through them, except that the
  User may cancel a Job and use the kill switch, and no paid feature is available.

9.2 **Unaffected uses.** Nothing in this section limits the use of the Command-line tool under the
MIT licence, including to initiate and cancel Jobs, or the use of the Software for development and
testing under section 4.2 (Development and testing).

# Part C. Community and Free Plans

## 10. Community and Free Plans

10.1 **Community.** The Community Plan carries no Fees. It comprises the Core, use of the Software
for development and testing under section 4.2 (Development and testing), and the Free Plans
described in section 10.2 (Free Plans). Support is provided through public GitHub issues under
section 8.1 (Support).

10.2 **Free Plans.** The Licensor grants each Free Plan through a License Key or an Order Form. If
the Customer no longer meets the conditions of a Free Plan, the Licensor may end that Free Plan at
the end of the term of its License Key. The Free Plans are:

- **(a) One free seat per legal person.** A legal person that has no Paid Plan may hold one free
  User Seat, and a legal person and its Affiliates together hold no more than one, intended for
  proof of concept and evaluation, under a License Key issued as section 6.3 (Issue of License
  Keys) provides. A Customer that has a Paid Plan has no free seat and shall
  pay for every User.
- **(b) Open-source teams.** A team whose projects are all public under a licence approved by the
  Open Source Initiative receives the Team plan's features without charge. The Key Service
  verifies eligibility from the repository URLs provided under section 6.3 (Issue of License Keys)
  and issues a License Key valid for one year.
- **(c) Academic institutions.** An academic institution receives the Team plan's features without
  charge, on a short application renewed every year.

10.3 **Trial.** The renewable three-month License Key of the free seat under section 10.2(a) (Free
Plans) serves as the self-serve, single-seat trial. The Licensor may grant a multi-seat trial of
the Team plan on request under section 17 (Discounts and trials).

10.4 **No Fees or refunds.** No Fees or refunds apply to Free Plans, and Parts D, E and F of the
Terms do not apply to them.

10.5 **Limits for Community, Free Plans and Beta Features.** Notwithstanding anything else in these
Terms, and to the maximum extent permitted by law, the Licensor provides no warranty, indemnity,
service-level commitment or support, other than public GitHub issues under section 8.1 (Support),
for the Community Plan, any Free Plan or any Beta Feature, and the Licensor's aggregate liability
arising out of or relating to them shall not exceed
US$100.

10.6 **Beta Features.** The Licensor may make available features or versions of the Software
identified as beta, preview, experimental or similar ("Beta Features"). The Customer may choose
whether to use them. Beta Features may be incomplete or contain errors, and the Licensor may
change, suspend or discontinue any Beta Feature at any time without notice. Section 10.5 (Limits
for Community, Free Plans and Beta Features) applies to Beta Features under every plan, including
a Paid Plan.

# Part D. Paid plans (Team and Enterprise)

## 11. Order Forms and seat changes

11.1 **Order Forms.** Each Paid Plan is purchased through an Order Form.

11.2 **Order Form terms.** An Order Form may add terms that apply to one Customer only, such as a
negotiated price or a support level. Section 33 (Order of precedence and entire agreement)
determines which document prevails if they conflict.

11.3 **Adding User Seats.** The Customer may add User Seats at any time. The Licensor shall charge
for added User Seats pro rata from the day they are added to the end of the current billing period.
Added User Seats end with the Subscription Term.

11.4 **Reducing User Seats.** The Customer may reduce the number of User Seats only with effect
from the next renewal, by notice given before the renewal date under section 14 (Term and renewal)
or section 19 (Enterprise commercial terms). A reduction does not reduce the Fees of an Enterprise
Subscription below its minimum annual fee under section 19 (Enterprise commercial terms).

11.5 **Resellers.** If the Customer purchases a Paid Plan through a Reseller: (a) these Terms
govern the Subscription and the use of the Software, and the Reseller's terms govern only the sale,
including price, payment, invoicing, and the charging and remitting of taxes on its sale; (b) the
Reseller is not the Licensor's agent, and no statement, promise or term of the Reseller binds the
Licensor unless the Licensor agrees to it in writing; (c) the Customer shall pay the Reseller as
the Reseller's terms provide; (d) if the Licensor does not receive payment for the Customer's
Subscription from the Reseller, the Licensor may suspend the Subscription under section 12.6
(Suspension for non-payment) as if the amount were due from the Customer, after notice to the
Customer; and (e) any refund is a matter between the Customer and the Reseller, except that, for a
purchase through the Merchant of Record, these Terms determine whether a refund is due (section 15
(Refunds)).

## 12. Fees, payment and taxes

12.1 **Payment.** The Customer shall pay the Fees in the currency stated in its Order Form, in
advance for each billing period, as section 13.2 (Payment), for a Team Subscription, or section 19
(Enterprise commercial terms), for an Enterprise Subscription, provides.

12.2 **Late payment.** Overdue amounts bear interest at 12% per year, or at the
highest rate permitted by law if lower, from the date on which the Licensor or the Merchant of
Record gives notice that the amount is overdue.

12.3 **Taxes.** Fees exclude taxes. The Customer shall pay all sales, use, value-added, goods and
services and similar taxes on the Fees, other than taxes on the Licensor's income. Those taxes are
charged as follows: (a) for a Subscription purchased through the Merchant of Record or another
Reseller, the Reseller charges and remits the taxes on its sale; and (b) for a Subscription
purchased directly from the Licensor, including an Enterprise Subscription, the Licensor shall
include the applicable taxes on its invoices.

12.4 **Price changes.** The Licensor may change its prices with effect from any renewal of a
Subscription, without prior notice. The new price applies from that renewal, subject to section
12.5 (Termination after renewal).

12.5 **Termination after renewal.** The Customer may terminate a Paid Plan Subscription, for any
reason, by notice given within 30 days after the start of any renewal term. The Licensor shall then
refund, or procure that the Merchant of Record or Reseller refunds, the prepaid Fees for that
renewal term pro rata for the unused part of the term, calculated from the date the termination
takes effect.

12.6 **Suspension for non-payment.** If any undisputed amount remains unpaid more than 30 days
after the Licensor gives notice that it is overdue, the Licensor may suspend the Subscription, and
decline to issue or renew License Keys, until the amount is paid.

# Part E. Team

## 13. Buying the Team plan

13.1 **Merchant of Record.** The Customer purchases a Team Subscription online from the Merchant of
Record, which acts as seller of record. Section 11.5 (Resellers) applies to that purchase. The Fees
are the Licensor's published prices, and section 15 (Refunds) determines whether a refund is due.

13.2 **Payment.** The Customer shall pay the Fees using the payment method provided at checkout.

13.3 **No minimum.** No minimum number of User Seats applies to a Team Subscription.

## 14. Term and renewal

14.1 **Monthly.** A monthly Team Subscription begins on purchase and renews automatically each
month. The Customer may cancel it at any time, with effect at the end of the current month.

14.2 **Annual.** An annual Team Subscription begins on purchase and renews automatically for
successive one-year terms, unless the Customer cancels it at least 30 days before the end of the
current term.

## 15. Refunds

No refund is payable for a monthly Team Subscription, except under section 12.5 (Termination after
renewal). For an annual Team Subscription, the Customer is entitled to a full refund of the Fees if
it cancels within 30 days after the initial purchase. A refund after a renewal is governed by
section 12.5 (Termination after renewal). No other refund is payable.

## 16. True-up

At each renewal of an annual Team Subscription, the Customer shall certify the number of its Users
during the term that is ending and shall purchase, at the price stated in its Order Form, any User
Seats it lacked, for the period during which it lacked them.

## 17. Discounts and trials

A nonprofit organisation may apply for a discount on the Team plan, renewable each year. A startup
may apply for a time-limited discount on the Team plan, which ends automatically at the end of its
period. The amount of any discount, and for a startup its period, are as stated in the Order Form.
Any Customer may request a multi-seat trial of the Team plan. Each application or request shall be
made through the manual request form of the Key Service under section 6.3 (Issue of License Keys),
and the Licensor may approve or decline it at its discretion and, for a trial, shall specify its
period.

# Part F. Enterprise

## 18. Contracting

18.1 **Direct contract.** The Licensor contracts directly with each Enterprise Customer and
invoices it directly, through an Order Form that refers to these Terms.

18.2 **Separate agreement.** An Enterprise Customer and the Licensor may instead sign a separate
written agreement. That agreement is then the Agreement (section 2 (Definitions)) and governs
instead of these Terms to the extent it so provides.

## 19. Enterprise commercial terms

The minimum annual fee, payment terms, Subscription Term, renewal and any refunds of an Enterprise
Subscription are as stated in its Order Form, except that section 12.5 (Termination after renewal)
applies in every case. The Fees shall be no less than the minimum annual fee stated in the Order
Form, whatever the number of User Seats.

## 20. Audit

The Licensor may, no more than once in any twelve-month period and on at least 30 days' written
notice, appoint an independent auditor bound by confidentiality obligations to review the
Enterprise Customer's records relevant to its number of Users. The Customer shall give the auditor
reasonable access to those records. The audit shall take place during business hours and shall not
unreasonably disrupt the Customer's business. The Licensor shall bear the cost of the audit, unless
the audit reveals an underpayment of more than 5%, in which case the Customer shall reimburse the
reasonable cost of the audit and shall pay for the unpaid User Seats.

# Part G. Legal terms for every plan

## 21. Intellectual property

21.1 **Customer's rights.** As between the parties, the Customer retains all rights in Customer
Data and in its own code, including code that AI agents write in its repositories through the
Software or the Core. The Licensor claims no rights in them.

21.2 **Licensor's rights.** The Licensor and its licensors retain all rights in the Software, the
License Keys and the Documentation, subject only to the rights expressly granted in these Terms.

21.3 **Feedback.** If the Customer provides the Licensor with suggestions or feedback about the
Software, the Licensor may use them freely and without obligation to the Customer. Code
contributions to the Fugaro Repository are governed by the contributor licence agreement and not
by this section.

## 22. Customer responsibilities

22.1 **General.** The Customer is responsible for: (a) its Users and their compliance with these
Terms; (b) configuring and securing its own cloud project, repositories, credentials and
model-provider accounts as the Documentation describes, including the trust model and known
limitations described in `SECURITY.md`; (c) maintaining any spending limits it requires with its
cloud and model providers; and (d) the content of the tasks it runs.

22.2 **Third-party charges.** The Customer is responsible for all cloud, model-provider and
code-host charges incurred by running the Software or the Core, under its own accounts. The Core's
budget caps are a safeguard against mistakes and do not guarantee that spending remains below any
cap (`SECURITY.md`, "Money").

## 23. Warranties and disclaimers

23.1 **Mutual.** Each party represents that it has the authority to enter into these Terms.

23.2 **AI output.** The Customer acknowledges that the Software operates AI coding agents that run
without permission prompts in the Customer's cloud environment, that **their output may be wrong,
insecure, incomplete or infringing**, and that every run ends in a pull request so that a person
can review it. The Customer is solely responsible for reviewing and testing all output before
merging, deploying or relying on it.

23.3 **No other warranty.** The Licensor makes no warranty other than under section 23.1 (Mutual),
under any plan, paid or free. The refunds under section 15 (Refunds) and section 19 (Enterprise
commercial terms) are not a warranty.

23.4 **Disclaimer.** Except as expressly provided in section 23 (Warranties and disclaimers), and
to the maximum extent permitted by law, the Software, the License Keys and the Documentation are
provided **"AS IS"**, without warranty of any kind, express or implied, including any warranty of
merchantability, fitness for a particular purpose or non-infringement, or that the Software will be
error-free or uninterrupted.

## 24. Limitation of liability

24.1 **Cap.** Each party's total liability arising out of or relating to these Terms shall not
exceed the Fees the Customer paid in the 12 months before the event giving rise to the claim.

24.2 **Excluded damages.** Neither party shall be liable for any indirect, incidental, special,
consequential or punitive damages, for any loss of profits, revenue, data or goodwill, or for any
cloud, model-provider or other third-party charges, even if advised of their possibility.

24.3 **Exceptions.** Section 24.1 (Cap) and section 24.2 (Excluded damages) do not limit: (a) the
Customer's obligation to pay Fees; (b) either party's liability for breach of section 26
(Confidentiality); (c) the Customer's liability for use beyond the licence granted, including use
by more Users than its User Seats; (d) indemnity obligations under section 25 (Indemnification); or
(e) any liability that cannot be limited by law.

## 25. Indemnification

25.1 **Licensor's indemnity.** This section applies only to a Customer with a Paid Plan that is in
effect, and paid up, when the claim arises. The Licensor shall defend the Customer against any
third-party claim alleging that the Software, when used by the Customer as these Terms authorise,
infringes that third party's copyright (a "Claim"), and shall pay any damages and costs finally
awarded against the Customer, or agreed in a settlement the Licensor approves, resulting from the
Claim.

25.2 **Exclusions.** Section 25.1 (Licensor's indemnity) does not apply to a Claim to the extent it
arises from: (a) modifications of the Software not made by the Licensor; (b) the combination of the
Software with any product, service, code or data not supplied by the Licensor, where the Claim
would not have arisen without the combination; (c) the Core, or any open-source or other
third-party component; (d) Customer Data, or any output of an AI model, including code written by
AI agents that the Software operates; (e) use of the Software in breach of these Terms, or after
the Licensor has made a non-infringing version available to the Customer; or (f) the Customer's
continued use after it knew, or should reasonably have known, of the infringement.

25.3 **Remedies.** If the Software is, or in the Licensor's opinion is likely to be, subject to a
Claim, the Licensor may, at its option and expense: (a) obtain the right for the Customer to
continue using it; (b) modify or replace it so that it no longer infringes, without materially
reducing its functionality; or (c) if neither is commercially reasonable, terminate the affected
Subscription and refund the prepaid Fees for the unused part of the Subscription Term.

25.4 **Customer's indemnity.** The Customer shall defend the Licensor against any third-party claim
arising from Customer Data, from the output of the AI models and agents the Customer uses through
the Software or the Core, or from the Customer's use of the Software in breach of these Terms, and
shall pay any damages and costs finally awarded against the Licensor, or agreed in a settlement the
Customer approves, resulting from that claim.

25.5 **Procedure.** The indemnified party shall: (a) notify the indemnifying party promptly in
writing of the claim; (b) give the indemnifying party sole control of the defence and settlement,
except that no settlement may impose an obligation or admission on the indemnified party without
its consent, not to be unreasonably withheld; and (c) give reasonable cooperation, at the
indemnifying party's expense.

25.6 **Exclusive remedy.** This section 25 (Indemnification) states the Licensor's entire
liability, and the Customer's exclusive remedy, for any claim that the Software infringes a third
party's intellectual property rights.

## 26. Confidentiality

26.1 **Definition.** "Confidential Information" means non-public information that one party (the
"Discloser") discloses to the other (the "Recipient") and that is marked confidential or that a
reasonable person would understand to be confidential, including License Keys, non-public pricing
and support data. It excludes information that (a) is or becomes public through no fault of the
Recipient, (b) was already known to the Recipient, (c) is independently developed by the
Recipient, or (d) is lawfully received from a third party. The source code of the Software, which
is published, is not Confidential Information.

26.2 **Obligations.** The Recipient shall use Confidential Information only for the purposes of
these Terms, shall protect it with at least reasonable care, and shall disclose it only to persons
who need it for those purposes and are bound by confidentiality obligations. The Recipient may
disclose Confidential Information when required by law, after giving the Discloser notice where
lawful. These obligations continue for 3 years after these Terms end and, for any trade secret, for
as long as it remains a trade secret.

## 27. Duration and termination

27.1 **Duration.** These Terms remain in effect for as long as any Subscription under them is in
effect.

27.2 **Breach.** Either party may terminate a Subscription by written notice if the other party
materially breaches these Terms and fails to cure the breach within 30 days after receiving written
notice of it.

27.3 **Insolvency.** Either party may terminate a Subscription by written notice if the other party
becomes insolvent, makes an assignment for the benefit of its creditors, or becomes subject to
bankruptcy or receivership proceedings that are not dismissed within 60 days.

27.4 **Effect.** When a Subscription ends: (a) the Customer's right to make Production Use of the
Software ends, except for the grace period and read-only use under section 9 (After the
Subscription ends); (b) the Customer shall cease all other Production Use and shall destroy all
copies of the Software, **except** copies used for development and testing under section 4.2
(Development and testing), copies needed for read-only use under section 9 (After the Subscription
ends), and records it needs for legal purposes; and (c) all unpaid Fees for the period up to the
end of the Subscription become due.

27.5 **Survival.** The following survive the end of these Terms: section 2 (Definitions); section
4.4 (Modifications); section 4.5(d) (No other patent rights); section 9 (After the Subscription
ends); section 12 (Fees, payment and taxes) and section 19 (Enterprise commercial terms), as to
amounts due; section 16 (True-up) and section 20 (Audit), for the period of the Subscription;
section 21 (Intellectual property); section 23.4 (Disclaimer); section 24 (Limitation of
liability); section 25 (Indemnification); section 26 (Confidentiality); section 27.4 (Effect);
section 27.5 (Survival); section 29 (Changes to these Terms); section 30 (Publicity); section 31
(Assignment); section 32 (Notices); section 33 (Order of precedence and entire agreement); and
section 34 (Governing law, venue and language).

## 28. Export control and sanctions

Each party shall comply with the export control and sanctions laws that apply to it, including
those of Canada and, where applicable, the United States. The Customer shall not use the Software,
or permit access to it, in any country, or by any person, subject to comprehensive sanctions under
those laws.

## 29. Changes to these Terms

29.1 **Amendments.** The Licensor may amend these Terms by publishing a new dated version at **https://fugaro.dev/legal/subscription-terms**.
A new version applies to a Subscription from its next renewal or new Order Form, and not during a
term already paid for, unless the Customer agrees otherwise.

29.2 **No notice required.** The Licensor is not required to give notice of a new version before
it applies. A Customer with a Paid Plan that does not accept a new version may terminate its
Subscription under section 12.5 (Termination after renewal).

## 30. Publicity

30.1 **Customer name and logo.** The Licensor may use the Customer's name and logo in its marketing
materials, including its website, solely to identify the Customer as a customer of the Licensor,
unless an Enterprise Order Form states that the Licensor may not do so. Any other use, such as a
case study, testimonial or press release, requires the Customer's prior written consent. The
Licensor shall follow any reasonable trademark guidelines the Customer provides.

30.2 **Licensor name and logo.** The Customer shall not use the Licensor's or Fugaro's name or logo
in marketing or as a reference without the Licensor's prior written consent.

## 31. Assignment

Neither party may assign these Terms without the other party's prior written consent, except to an
Affiliate or to a successor in a merger, acquisition or sale of all or substantially all of its
business or assets related to these Terms, on notice to the other party. Any assignment in breach
of this section is void.

## 32. Notices

All notices under these Terms shall be in writing and sent to the addresses stated in the Order
Form (for the Licensor, **dimi@fugaro.dev, and 1621 Maple Ridge Dr., Mississauga, Ontario, L4W 3R6, Canada**). Notices take effect on receipt. Email is
sufficient for notices.

## 33. Order of precedence and entire agreement

33.1 **Order of precedence.** If documents conflict, the following order of precedence applies: (1)
an Order Form, for that Order Form's Subscription; (2) these Terms; and (3) the Documentation. The
Merchant of Record's buyer terms govern only the sale, under section 3.3 (Team) and section 13.1
(Merchant of Record).

33.2 **Entire agreement.** These Terms and the Order Forms constitute the entire agreement between
the parties concerning the Software and supersede all prior proposals and understandings
concerning it. Terms in any purchase order or other document of the Customer do not apply, even if
accepted. No amendment is valid unless in writing and signed by both parties, except an amendment
under section 29 (Changes to these Terms).

33.3 **General.** If any provision of these Terms is held unenforceable, the remaining provisions
remain in effect and that provision shall be enforced to the maximum extent permitted. No failure
or delay in enforcing a right is a waiver of it. The parties are independent contractors. Neither
party is liable for any delay or failure to perform caused by events beyond its reasonable control,
except that this does not excuse any obligation to pay Fees or other amounts due.

## 34. Governing law, venue and language

34.1 **Governing law.** These Terms are governed by the laws of the Province of Ontario and the
federal laws of Canada applicable in it, without regard to conflict-of-laws principles. The courts
of Ontario have exclusive jurisdiction over any dispute arising out of or relating to these Terms,
and each party irrevocably submits to that jurisdiction.

34.2 **Language.** If the Customer is located in Quebec, the Licensor has made a French version of
these Terms available to it before it accepted them, and the Customer, having received that
version, has expressly chosen to be bound by these Terms and all related documents in English. Si
le Client est situé au Québec, le Concédant lui a remis une version française des présentes
Conditions avant leur acceptation, et le Client, après en avoir pris connaissance, a expressément
choisi d'être lié par les présentes Conditions et tous les documents qui s'y rattachent en anglais.

---

## Questions for the owner and counsel (not part of the agreement)

Counsel's text answers most of the earlier draft's questions (an indemnity, a patent licence, a
Quebec language clause, the late-payment rate, resellers and the merchant of record). What remains:

1. **The two figures once held back** (sections 10.5 and 12.2): counsel's text states a liability
   cap for the Community Plan, Free Plans and Beta Features and a late-payment interest rate; the
   owner ruled on 2026-10-29 that both go in as counsel wrote them, and they are now in the text.
2. **Counsel's open comment on section 4.4** (Modifications): whether the clause should rest on the
   contributor licence agreement alone. The earlier draft's assignment of rights in Modifications
   to the Licensor is not in counsel's text.
3. **Provisions counsel's document marks** (highlighted in the source): section 5.1(i) (no seat for
   a viewer), section 5.3 (Roles) and section 5.5 (Seat estimate), which counsel's text wrote as
   features to come. Applied: present tense, owner 2026-10-29; the roles and the seat estimate must
   be released before the Terms go public.
4. **The owner's rulings counsel's text did not carry** (subscriptions plan task S0.4): applied
   (counsel approved 2026-10-29). Read-only use after the grace period can still cancel a Job and
   use the kill switch (section 9.1, ruling T12); one License Key per Customer for its
   organisation, stating its User Seats, instead of one per User (sections 2, 5.3, 6.1, 6.3, 6.4,
   7.5 and 9.1); no covered-versions date (sections 6.1 and 8.2, ruling O4); the free seat applied
   per legal person and its Affiliates (sections 6.3, 7.5 and 10.2(a), ruling T15).
5. **Crash reports** (section 7.3): counsel's first text let the Software send crash reports and
   diagnostics that the Customer cannot turn off. Resolved by the owner on 2026-10-31: crash
   reporting is opt-in, per User, and off by default, and counsel approved the revised section.
   The console and the mobile app send none today.
6. **Publicity** (section 30.1): the Licensor may name a Customer as a customer unless an
   Enterprise Order Form says otherwise; the earlier draft required consent both ways.
7. **The coordinator** (section 7.1): counsel's text called the credentials service and the
   coordinator "part of the Core and not of the Software". Resolved by the owner on 2026-10-29:
   the coordinator is part of the Software (the business-licensed Subscription Edition), and
   section 7.1 is corrected; the credentials service stays part of the Core.
8. **The licence's wording**: counsel's text no longer cites the licence's clauses (the "Licence"
   definition is gone, and the Subscription is not described in the licence's words); whether the
   licence files need a matching revision is counsel's.
