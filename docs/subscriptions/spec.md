# Fugaro Subscriptions Spec

Oct 27, 2026 · Dimi Paun

> **Excerpt.** The owner's subscriptions spec of 2026-10-27 holds commercial material that stays
> outside this repository (D403, D430): the price row of the tier table, the support row and the
> "SLA and support" section's hours, the billing lines of "Billing and trial", the "Contracting
> entity" and "Refunds" sections, the Enterprise audit line's figures and the nonprofit program's
> row live in the owner's private business repository. Everything below is the owner's text,
> unedited except one name substitution the folder README lists; only those lines are left out,
> and this note is the excerpt's own.

## Context and principles

Fugaro sells software, not a service: it runs entirely in the customer's own GCP/Firebase project, and they pay their own model and infra costs. So there is no hosted chokepoint for metering or enforcement, and the model leans into that.

- **Open-core, split by buyer.** Everything one developer needs stays free and MIT. What only organisations need is paid.
- **Never paywall safety.** Spend caps, the kill switch, the credentials security service and rigor modes are always in the free core.
- **Never block work.** No licence state, lapse or failed check may stop a running fleet or a job, hotfixes included.
- **No Fugaro-hosted dependency.** Avoid licence servers or relays; once one exists, Fugaro owes an uptime SLA.
- **Honour-based enforcement.** Visible usage, soft warnings and annual true-ups, not hard locks.

## Tiers and features

Three tiers: Community (free forever), Team (self-serve, per seat) and Enterprise (annual, invoiced). Price points are placeholders to validate with early customers.

| Feature | Community | Team | Enterprise |
| --- | --- | --- | --- |
| CLI, cloud jobs, recipes, harnesses | Yes | Yes | Yes |
| Spend caps, kill switch, credentials security service | Yes | Yes | Yes |
| Rigor modes, including Hotfix | Yes | Yes | Yes |
| Fugaro Watch dashboard | Yes | Yes | Yes |
| Console and mobile app, single user | Yes | Yes | Yes |
| Console and mobile app, multi-user with viewer/operator/admin roles | No | Yes | Yes |
| Per-project coordinator agent | No | Yes | Yes |
| Shared central orchestrators | No | Yes | Yes |
| Hosted MCP server with policy-narrowing tokens | No | Yes | Yes |
| Workspaces: shared identity, credentials, workspace-wide cap | No | No | Yes |
| Audit and spend-history exports | No | No | Yes |
| Contract terms, security review, invoicing | No | No | Yes |

The line is: if a feature only matters once a second person or an organisation-wide view is involved, it is paid.

## Licensing of paid components

Paid features ship under the Functional Source License (FSL), converting to MIT two years after each release; the core stays MIT in dimipaun/fugaro.

- MIT code can be forked freely, so paid features cannot live under MIT in the same repo.
- FSL keeps source visible, forbids competing commercial use, and its conversion to MIT pairs naturally with perpetual fallback.
- Alternative: closed-source binaries for paid components. Simpler to enforce, worse for trust and contributions.
- Repo layout: paid components in a separate directory or repo with their own LICENSE, so the MIT boundary is unambiguous.

## Pricing metric

Paid tiers are priced per seat. A seat is a person who launches jobs or signs in to the console or mobile app as operator or admin; viewers are free.

Seats are measurable without phoning home: Firebase Auth and the per-project allowlists already know who signs in.

| Metric considered | Decision | Reason |
| --- | --- | --- |
| Per seat | Chosen | Matches who gets value; already measured by Firebase Auth |
| Per installation or project | Rejected | Fugaro-managed throwaway projects make counts meaningless; penalises sensible project splits |
| Percentage of model spend | Rejected | Taxes the customer's own costs; conflicts with the spend controls Fugaro sells |
| Per job or per run | Rejected | Discourages the wide parallelism Fugaro is built for |

## Billing and trial

- Trial: 30 days of Team features, no card required, so a lead can try the coordinator and multi-user roles with real colleagues.
- At trial end without purchase, Team features go read-only (see lapse behaviour); nothing stops running.

## Lapse behaviour

Perpetual fallback: any version released while a subscription was active keeps working forever; a lapse only stops access to newer versions.

- A running fleet, a queued job or a hotfix is never stopped by licence state.
- On a lapse, paid features on newer versions go read-only with a renewal reminder in the console.
- Licence checks are local (signed key, no network call), so an outage on Fugaro's side can never cause a lapse.
- With FSL, each release also becomes MIT after two years, which reinforces the promise.

## Licence enforcement and audit

Enforcement is light and visible: the licence key encodes the seat count, the console shows seats used against it, and overage triggers warnings, never blocks.

- The key is a signed token verified locally; no phone-home.
- Annual true-up: the customer self-certifies the seat count at renewal and pays the difference.
- Optional, opt-in telemetry may report seat counts; it is never required.
- Rationale: aggressive auditing hurts adoption of developer tools more than under-licensing costs.

## Academic, open-source and nonprofit programs

Team is free for open-source maintainers and for academic research and teaching; nonprofits get a discount.

| Program | Eligibility | Benefit | Process |
| --- | --- | --- | --- |
| Open source | A Fugaro project whose repos are all public and under an OSI-approved licence | Team free | Short application form, renewed yearly |
| Academic | Research or teaching at an accredited institution, non-commercial use | Team free | Application with institutional email |

Open-source eligibility is judged per project, so a company cannot qualify just by owning one public repo.

## Open questions

- [ ] Final Team price point, and Enterprise minimum commitment
- [ ] FSL versus closed binaries for paid components
- [ ] Are the console and mobile app free for a single user, or paid from seat one?
- [ ] New Fugaro entity now, or start under [the owner's existing company] and assign later?
- [ ] Which merchant of record?
- [ ] Is a startup discount worth adding alongside the nonprofit one?
