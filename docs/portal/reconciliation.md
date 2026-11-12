# Fugaro portal: reconciliation of the owner's spec against the code

**This file amends [spec.md](spec.md); where the two disagree, this file wins.** Checked against
`origin/main` at `a07e14262` and the open pull requests on 2026-11-12. It numbers **D2180–D2229**
(D2180–D2195 used) and **Checks 570–579**, both verified free on `main` and in every open pull
request's diff. Findings are `F1`…`F24`.

## What the repo already has

The spec was written without the repo. About a third of it is designed, and part of it is built, under
the names below: the key service of the subscriptions plan (S3) and the licence key v2 plan
([docs/licence-key-v2](../licence-key-v2/README.md), 8 of 26 tasks merged, K2.3 in #2457 and K2.5 in
#2458).

| Spec concept | What exists on `main` |
| --- | --- |
| Backend with a licence-refresh endpoint | `ee/keyservice` (Go): `service.go` the public intake, `minter.go` the signer half, `merchant.go`, `admin.go`, `checks.go`, `queue.go`, `policy.go`, `store.go`. Not deployed. `/renew` and `/status` are K2.3 (#2457, open) |
| Key, licence, refresh policy in the key | `ee/licence/keyv1.go`: `fgk_` v1 key, `ClaimsV1` with `key_id`, `workspace_id` (required, the workspace's Firebase project id), `organisation`, `tier`, `seats`, `paid_through`, `grace_days`, `renewal_mode`, `renew_before_days`, `check_interval`, `warnings`, `kind`; `VerifyV1` |
| Signing with a key in Cloud KMS | `ee/keyservice/terraform/keyring`: `EC_SIGN_ED25519`, one ring with key ids `fugaro-licence-production` and `fugaro-licence-staging`, default project `fugaro-licence`; `ee/licence/mint/kms.go` (the owner's hand-mint tool) |
| Staging licences never validate in production | The key id names the environment; `TestStagingKeyIDIsRefused` |
| Paddle webhooks: signature, idempotency | `merchant.go`: `VerifySignature` (`Paddle-Signature: ts=…;h1=…`), the idempotency guard in `apply`, `ModeFor` (test mode against the staging pair). Events are stored in a **canonical shape, not as received** |
| Operations key minting, audit | `admin.go` (S3.4, #2382): the owner's account only, a second factor per mint (**D455**), per-tier caps (**D454**), one audit line per mint (**D456**), and **a key is mailed and never stored or rendered** (design §7.3) |
| Free seat | D440/D432: one free evaluation seat per organisation, 3 months, one per email domain, through the `/free-seat` form on `website/src/pages/keys.astro` and email verification (`checks.go`) |
| Trials, open source | `policy.go` `PolicyTrial` (D2136, 14 days, grace 0); `/open-source` form with proof of control (D452) |
| Revocation | `store.go` `Revocation`, `KeyState`, `Trial` (K2.2); revocation at the job's next check (D2131), never on silence (D2140) |
| Seats | `seats` compared and shown; enforcement is K3.4, **held** until the owner says enforcement ships (TN1 of licence-key-v2); roles `viewer`/`operator`/`admin` in `internal/access` |
| `fugaro license refresh` | K3.2 plans `fugaro licence set | status | refresh` with `license` as a hidden alias (D2145) |
| Email interface | `ee/keyservice/mail.go`: `Mailer` with one `Send`; no provider wired |
| Firebase Hosting, design tokens | `firebase.json` (one hosting site, `website/dist`); `website/src/styles/tokens.css` (custom properties, **no Tailwind**); `fugaro-prod` is the site's project (D91) |
| Analytics consent | `website/src/components/ConsentBanner.astro`, `public/scripts/site.js`: nothing loads from the analytics origin before consent (D93) |
| Firebase Auth in `fugaro-prod` | TN3 of licence-key-v2: the licence site signs in through Firebase Auth in `fugaro-prod`; K2.4 plans the ID-token verifier reusing `ee/coordinator/mcpauth/firebase.go` |

## Findings

**F1 (Exists, Rule): the backend exists in Go.** The spec's "TypeScript on Node" backend would replace
`ee/keyservice`, whose intake/minter split is an identity boundary the design rules (§3, Check 174).
Resolved by **D2180: all Go** (not the recommendation). The portal API extends `ee/keyservice`; the
minter stays the only identity that can sign. The spec's "types shared with the frontend" becomes JSON
Schemas that both sides test against (F20).

**F2 (Wrong): "the key identifies the licence and is exchanged for a fresh signed licence"** describes
a two-object model; licence key v2 has one: the key *is* the signed licence, and a renewal secret
(D2135) fetches the next one. Resolved by **D2181: keep the v2 bundle** (not the recommendation). What
the spec wants still holds: a seat change reissues a successor key (K2.5) and the installation's
`fugaro licence refresh` fetches it with the secret it holds, so nobody pastes a new key after a
purchase or a seat change. "The refresh policy embedded in the key" is v1's `check_interval` and
`renew_before_days`, already in `ClaimsV1`.

**F3 (Rule): "keys are only shown in the portal, so customers can always return to recover them"**
contradicts design §7.3 (a key is mailed once, never stored or rendered). Resolved by **D2182: hash
only, rotate**. "Reveal key" issues a fresh bundle (new secret, its hash in a newly signed key text);
the previous secret still fetches the successor for 24 hours (K2.3), so a running renewal job picks it
up at its daily check. Following the spec, **no key is ever mailed**: the purchase email of K2.5 and the
admin page's mail carry a link to the portal, not the key. That removes the mailbox exposure the
licence-key-v2 threat model (5) accepted.

**F4 (Gap): reveal semantics for the first view.** "After payment the user lands on the workspace page
with the key ready to copy": the first reveal is the first issue. The webhook mints the key's record;
the bundle's secret is generated at the first reveal, so nothing retrievable is stored at any time.

**F5 (Gap): who may reveal.** Revealing rotates a live key, so it is limited to the customer
organisation's owners and admins. Resellers and operations see key **metadata** (key id, seats, tier,
renewal date, last refresh), never a secret: a reseller's reveal would rotate the customer's key under
them. This is the reading of the spec's "resellers see those customers' keys".

**F6 (Exists, Wrong): the free seat.** The spec mints a permanent one-seat key per organisation at
sign-up. Every key needs `workspace_id` (TN2), and a new organisation has none. Resolved by **D2184**:
minted when the organisation adds its first workspace, Team tier, one seat, `kind: free`, renewed
indefinitely while the organisation exists. It replaces D440's form-based seat (3 months, one per
domain, email verification); the abuse control becomes the terms, as the spec says.

**F7 (Gap): trials and open source are not in the spec.** Resolved by **D2185**: both stay, in the
portal. A trial button per workspace (K2.7's rule: one per workspace and account), and an open-source
request that operations reviews in the portal's queue (D452's proof check reused). The three website
forms (`/free-seat`, `/open-source`, `/request`) retire with their routes once the portal flows ship.

**F8 (Rule): "no assignment of people to seats"** contradicts K3.4's `licence seats assign`. Resolved
by **D2186**: admins always hold a seat; operators fill the remaining seats in the order they were
granted; later ones act as viewers. `access.Grant` has no grant time (`internal/access/access.go`),
so **the plan adds a `since` field to the grant node** (an added field, allowed); grants written
before it rank after every dated one, by member address. Still **held** with K3.4 under TN1: nothing
is enforced until the owner says enforcement ships.

**F9 (Rule): operations minting.** The spec lets "members of the operations organization" mint any key;
the built admin page is the owner's account only with a second factor per mint (D455). Resolved by
**D2187**: operations owners and admins mint, extend, change seats and revoke; operations members only
read. Each of those actions needs a sign-in no older than 5 minutes (the ID token's `auth_time`), with
Google or Microsoft. D454's per-tier caps stay. D456's audit applies to every operations action, reads
included (the spec's "every operations action is audit-logged"). Invitations to the operations
organisation are limited to an allowed email-domain list the operations owner sets. **This amends D455**:
the factor is the identity provider's, which Fugaro cannot see.

**F10 (Gap, security): `sign_in_provider` alone.** Google and Microsoft both accept consumer accounts;
the spec's "SSO" check proves the provider, not the employer. With F9's domain list and an invitation
bound to its address, an operations seat needs both. Resolution (no decision needed): **every**
invitation, of any organisation type, is accepted only by a token whose verified `email` equals the
invited address (case-folded as `access.Member` folds it), so a forwarded link is useless.

**F11 (Gap): where the provider is checked.** Resolved by **D2188: API only**. Firestore rules deny every
client read and write, which satisfies the spec's "rules check the provider" trivially; the API checks
the provider, the role and the organisation type on every request.

**F12 (Gap): reseller money.** "Prorated by days, invoiced monthly, credit limit" leaves when a charge
accrues. Resolved by **D2189: daily accrual**. Each day a reseller-minted key is active accrues seats
times the reseller's daily wholesale rate; the month's statement sums it; minting or a seat increase is
refused when unpaid statements, plus this month's accrual, plus the new key's cost to the end of the
month exceed the credit limit. Operations records payment; the portal collects nothing.

**F13 (Wrong): "amounts owed to resellers".** The reseller section has resellers paying Fugaro.
Resolved by **D2190**: the report shows each reseller's receivable to Fugaro; there are no commissions.

**F14 (Gap): how a reseller is linked to a customer.** Resolved by **D2191**: the reseller creates the
customer organisation and invites its owner; the link is recorded at creation; one reseller per
customer organisation; operations can relink. Reseller organisations are created by operations only.

**F15 (Gap): Paddle granularity.** Resolved by **D2192**: one Paddle subscription per paid workspace,
`custom_data` carrying the organisation id and the workspace id, seats as the quantity. It matches
`merchant.go`'s one key per subscription; #2458 (K2.5) maps the event, and the portal task adds the
workspace id to the custom data it reads.

**F16 (Gap): "store each event exactly as received".** `merchant.go` keeps a canonical shape. The plan
stores the raw body and the signature header **before** processing, keyed by Paddle's event id, and
derives the ledger from the stored events.

**F17 (Gap): deletion.** Resolved by **D2195**: an account cannot be deleted while it is the only
owner of an organisation; deleting an organisation cancels its Paddle subscriptions and revokes its
keys (effective at their next check); sales records stay, with personal fields removed. How long they
stay is D457 (counsel).

**F18 (Rule): projects.** The key service's Terraform defaults to a dedicated project, `fugaro-licence`
(design §3.4). Resolved by **D2194: everything in `fugaro-prod`** (not the recommendation): Hosting,
Auth, Firestore (a named database), the API, the minter and the key ring; staging is `fugaro-staging`
with its own ring and only the staging key id. What carries the isolation now is IAM inside one
project, so the plan adds: the key ring's signer grant to the minter's service account only, no
project-level role that includes `cloudkms.cryptoKeyVersions.useToSign` held by anyone but the owner,
default service accounts not used by any service, and a check (Check 571) that measures it. The
website's CI deployer keeps Hosting roles only.

**F19 (Wrong): Tailwind.** The website has no Tailwind; its tokens are CSS custom properties in
`website/src/styles/tokens.css`. Resolution: the portal imports that file; no Tailwind is added (the
spec says "e.g.").

**F20 (Gap): shared types with a Go backend.** Resolution: the API's request and response shapes are
JSON Schemas under `ee/portal/schemas/`; Go decodes strictly and tests fixtures against them; the SPA's
TypeScript types are hand-written beside them and a unit test validates the same fixtures. No code
generator is added. Adding a field is allowed; changing one's meaning is not (CLAUDE.md).

**F21 (Gap): one hosting config.** `firebase.json` holds one site and `scripts/deploy-website.sh` runs
`firebase deploy --only hosting`, which would deploy every site. The plan splits it into two hosting
targets (`website`, `portal`) and the website script names its target. The portal's CSP admits Paddle's
checkout origins and Firebase Auth's; the website's `form-action` loses `keys.fugaro.dev` when the forms
retire.

**F22 (Rule): no agent deploys or operates the service** (`.claude/rules/ee.md`, R5 as amended by
D440). Every deploy, project setup, provider account, key creation and the operations organisation's
seed are OWNER-RUN; the plan writes scripts for them.

**F23 (Rule): new workflows.** A portal CI job is staged under `docs/pending-workflows/`; an agent never
edits `.github/workflows/`.

**F24 (Rule): new dependencies.** The spec is the owner's ask for React, Vite, Firebase JS, Sentry,
Paddle.js and Resend; the Go side adds `cloud.google.com/go/firestore`. The plan lists each with the task
that adds it. Sentry and analytics follow the site's consent rule: analytics after consent only; Sentry
with personal data off and no session replay; the privacy notice changes with counsel (K0.1, S7.3).

**Threat model of what the portal adds.**
1. *The API can ask the minter to sign.* It is now the internet-facing door to signing. The minter
   keeps refusing a claim beyond the tier's cap (D454) and logs every signature; the API holds no
   signer.
2. *An operations account* can mint for any organisation: fresh sign-in, owner or admin role, domain
   list, and every action in the audit log (F9).
3. *A reseller account* can mint for its own customers only, bounded by its credit limit; it never
   holds a customer's secret (F5).
4. *Key reveal* is a rotation, so a stolen session can take over a key's renewal. Reveal needs a fresh
   sign-in (5 minutes), is logged, and notifies the organisation's other owners by email.
5. *Paddle webhook* forged or replayed: signature over the raw body, timestamp window, idempotent by
   event id, stored raw first.
6. *One project* (D2194): a project-level grant in `fugaro-prod` can reach the signer; Check 571
   measures that no one holds one.

## Decisions

| # | Question | Recommendation (why) | Alternative (cost) | Owner's answer |
| --- | --- | --- | --- | --- |
| D2180 | Backend language and split (F1) | A TypeScript API plus the Go minter (types shared with the SPA; the signer stays Go) | All Go (no native type sharing); all TS (the signer in two languages) | **2026-11-12: all Go. Not the recommendation.** Extend `ee/keyservice`; the built, tested code stays |
| D2181 | Key and licence model (F2) | Key = an opaque stable credential; licence = the v1 signed text fetched with it | Keep the v2 bundle (a seat change reissues a key) | **2026-11-12: keep the v2 bundle. Not the recommendation.** The refresh fetches the successor, so the spec's flow holds |
| D2182 | Recovering a key (F3) | Store it encrypted and show it again, audited | Hash only; a reveal rotates (the installation's job picks the successor up within 24h) | **2026-11-12: hash only, rotate. Not the recommendation.** Keeps design §7.3 |
| D2183 | #2457 and #2458 in flight | Hold both until this plan is approved | Merge as planned | **2026-11-12: merge as planned. Not the recommendation.** Consistent with D2181 |
| D2184 | When the free seat is minted (F6) | At the first workspace, one per organisation | At sign-up, unbound (an exception to TN2) | **2026-11-12: the recommendation.** |
| D2185 | Trials and open source (F7) | Both stay, in the portal | Drop the trial; drop both | **2026-11-12: the recommendation.** |
| D2186 | Who is over the seat limit (F8) | Admins first, then operators by grant order | Keep explicit assignment | **2026-11-12: the recommendation.** Held with K3.4 (TN1) |
| D2187 | Operations powers and step-up (F9) | Owner and admin, sign-in within 5 minutes, D454 caps, domain list | Keep D455 owner-only with TOTP; any member | **2026-11-12: the recommendation.** Amends D455 |
| D2188 | How the SPA reaches data (F11) | API only; rules deny every client access | Direct reads plus rules | **2026-11-12: the recommendation.** |
| D2189 | Reseller accrual and credit (F12) | Daily accrual, statement monthly, limit over unpaid + accrued + new | Term up front | **2026-11-12: the recommendation.** |
| D2190 | "Owed to resellers" (F13) | Resellers owe Fugaro; no commissions | Commissions on attributed direct sales | **2026-11-12: the recommendation.** |
| D2191 | Reseller–customer link (F14) | The reseller creates the customer organisation | The customer accepts a link | **2026-11-12: the recommendation.** |
| D2192 | Paddle subscriptions (F15) | One per paid workspace | One per organisation | **2026-11-12: the recommendation.** |
| D2193 | Where the SPA lives | `ee/portal/` under the Subscription licence | `portal/` MIT; a private repository | **2026-11-12: the recommendation.** |
| D2194 | Projects (F18) | API in `fugaro-prod`, minter and ring in `fugaro-licence` | Everything in `fugaro-prod` | **2026-11-12: everything in `fugaro-prod`. Not the recommendation.** The owner asked why not; told: defence in depth against project-level grants, at the cost of one project. Isolation is carried by IAM and Check 571 |
| D2195 | Deletion (F17) | Refuse a sole owner; deleting an organisation cancels and revokes | Cascade | **2026-11-12: the recommendation.** |

What this changes in licence key v2 (its plan is amended by this plan's task P9.1, not edited here):
K2.6 (the admin page) is superseded by P5.1; K5.1 (licence pages on the Astro site) is superseded by the
portal's screens, and K2.4's authorize and device pages are served by the SPA; K3.4 follows D2186; K2.5's
purchase email carries a portal link, not a key (F3).

## Open tensions and open items for the owner

No two answers conflict. Open items, all the owner's:

- **TN1 of licence-key-v2 still holds.** Seat enforcement (D2186) ships only when the owner says
  enforcement ships.
- **Counsel.** Fugaro invoicing resellers directly (outside Paddle as merchant of record) brings
  invoicing and sales-tax duties; the terms of service and the licence terms the portal records
  acceptance of; the privacy notice for the portal (Firestore personal data, analytics, Sentry); D457
  retention. Stated neutrally: these are the owner's and counsel's to confirm.
- **Accounts and consoles (OWNER-RUN, P0):** the Firebase providers (Apple needs a Services ID;
  Microsoft an app registration), the `app.fugaro.dev` DNS, Paddle live and sandbox, Resend and its DNS
  records, the Sentry project and DSN, the analytics property, and the signing keys in `fugaro-prod`
  and `fugaro-staging`.
- **TOTP for email sign-in** is "later" in the spec and is not planned.

## UNVERIFIED

- The value of `firebase.sign_in_provider` for an email-link sign-in (`password` or `emailLink`). The
  API allows `google.com` and `microsoft.com` only, so either value is refused; P1.3's test pins both.
- Whether Identity Platform's TOTP second factor works with email-link sign-in (the spec's own
  question). Not needed by any task.
- Whether the `fugaro-licence` project or its key ring was ever created (S3.8 is owner-run). If it was,
  the ring cannot be deleted; its key versions are destroyed and the ring left (P0.4).
- Whether `fugaro-prod` sits under an organisation that disables automatic Editor grants to default
  service accounts. Check 571 measures the outcome either way.
- Paddle's customer-portal session API and its current field names, and Paddle.js's CSP origins:
  P4.3 and P4.1 confirm them against Paddle's documentation at build time.
- Resend's free-tier limits.
