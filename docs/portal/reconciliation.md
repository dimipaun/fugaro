# Fugaro portal: reconciliation of the owner's spec against the code

**This file amends [spec.md](spec.md); where the two disagree, this file wins.** Checked against
`origin/main` at `a07e14262` and #2457 (K2.3) and #2458 (K2.5), both merged on 2026-11-12. It
numbers **D2180–D2229** (D2180–D2207 used) and **Checks 570–579**, both verified free on `main` and in
every open pull request's diff. Findings are `F1`…`F42`. Review rounds corrected several findings; those
are marked **(round 1)**, **(round 2)** or **(round 3)**.

## What the repo already has

The spec was written without the repo. About a third of it is designed, and part of it is built, under
the names below: the key service of the subscriptions plan (S3) and the licence key v2 plan
([docs/licence-key-v2](../licence-key-v2/README.md); K2.3 (#2457) and K2.5 (#2458) merged on 2026-11-12).

| Spec concept | What exists on `main` (#2457 and #2458 included) |
| --- | --- |
| Backend with a licence-refresh endpoint | `ee/keyservice` (Go): `service.go` the public intake, `minter.go` the signer half, `merchant.go`, `admin.go`, `checks.go`, `queue.go`, `policy.go`, `store.go`. **No `main`, no KMS-backed signer, no image: nothing is deployable** (the package comment says so). `/renew` and `/status` are #2457 |
| Key, licence, refresh policy in the key | `ee/licence/key.go`: `fgk_` v1 key, `Claims` with `key_id`, `workspace_id` (required: the workspace's Firebase project id), `organisation`, `tier`, `seats`, `paid_through`, `grace_days`, `renewal_mode`, `renew_before_days`, `check_interval`, `warnings`, `kind` (`knownKind`: `paid`, `trial`, `comped` only); `Verify`. Shipped in v0.23.0 |
| Signing with a key in Cloud KMS | `ee/keyservice/terraform/keyring`: `EC_SIGN_ED25519`, one ring (default name `fugaro-licence`) holding key ids `fugaro-licence-production` and `fugaro-licence-staging` (D459(a)); the `project` variable has no default. `ee/licence/mint/kms.go` is the owner's hand-mint tool (package `main`, through `gcloud`) |
| Staging licences never validate in production | The key id names the environment; `TestStagingKeyIDIsRefused` |
| Paddle webhooks: signature, idempotency | `merchant.go`: `VerifySignature` (parses `ts`, **never compares it with now**), the idempotency guard in `apply`, `ModeFor`. `webhook` decodes a **canonical `Event`**, not Paddle's notification body, and answers 200 when decoding fails |
| Operations key minting, audit | `admin.go` (S3.4, #2382): the owner's account only, a second factor per mint (**D455**), **D454**'s per-tier rate per window that **queues** the excess (`MintCaps`), one audit line per mint (**D456**), and a key is mailed, never stored or rendered (design §7.3) |
| Policy chosen by the minter | `MintV1` takes the renewal and check policy from the row only ("a caller that could name them could mint a key that never checks"); it draws a new `key_id` and `issued_at` on every mint, generates the renewal secret itself (D2135) whose hash is a signed claim (`RenewalSecretHash`), and takes the caller's `TermDays` capped at the tier's maximum |
| Renewal handover | #2457: `Handover{Key, RenewalSecret, Until}` keeps a rotated successor's key and **plaintext** secret for 24 hours; `/renew` sees a successor only through a `rotation:<key_id>` record; it renews any key whose mode is not `none`, with no check on what was paid |
| Free seat | D440/D432: one free seat per organisation, 3 months, one per email domain, through the `/free-seat` form on `website/src/pages/keys.astro` and email verification (`checks.go`) |
| Trials, open source | `policy.go` `PolicyTrial` (D2136, 14 days, grace 0); `/open-source` form with proof of control (D452) |
| Revocation | `store.go` `Revocation`, `KeyState`, `Trial` (K2.2); revocation at the job's next check (D2131), never on silence (D2140) |
| Seats and roles | `seats` compared and shown; enforcement is K3.4, **held** (TN1 of licence-key-v2). Roles `viewer`/`operator`/`admin` in `internal/access`; the grants node is an ordered array decoded with `DisallowUnknownFields` |
| `fugaro license refresh` | K3.2 plans `fugaro licence set | status | refresh` with `license` as a hidden alias (D2145) |
| Email interface | `ee/keyservice/mail.go`: `Mailer` with one `Send`; no provider wired |
| Firebase sign-in verifier | `ee/coordinator/mcpauth/firebase.go`: `Identity{UID, Email}` (email lower-cased whole); its comment pins its one caller (D923) |
| Firebase Hosting, design tokens | root `firebase.json` (one site, `website/dist`), pinned by `website-preview.yml`'s gate, `website/test/brand.mjs`, `consent.mjs`, `lib/html.mjs` and `scripts/deploy-website_test.sh`; `website/src/styles/tokens.css` (custom properties, **no Tailwind**); `fugaro-prod` is the site's project (D91) |
| Analytics consent | `website/src/components/ConsentBanner.astro`, `public/scripts/site.js` (D93) |
| Firebase Auth in `fugaro-prod` | TN3 of licence-key-v2 |

## Findings

**F1 (Exists, Rule): the backend exists in Go.** Resolved by **D2180: all Go** (not the recommendation).
The portal API extends `ee/keyservice`; the minter stays the only identity that can sign. The spec's
"types shared with the frontend" becomes JSON Schemas both sides test against (F20).

**F2 (Wrong, round 1): a seat change or a renewal is not visible to `licence refresh`.** D2181 (keep the
v2 bundle) was answered on the premise that a reissue is a successor the installation's refresh fetches.
As built (#2458), a seat change mints through `paid()` with a fresh key and writes no handover, and #2457's
`/renew` sees a successor only through a rotation record. **D2181 is amended after review by D2200**:
a reissue for seats or a renewal keeps the entitlement's renewal-secret hash, and `/renew` answers with the
entitlement's newest key; only a reveal rotates the secret. **D2200 amends D2135** ("the secret is minted once"): the minter
accepts an existing secret hash for a reissue under the same entitlement (P1.8), and the renewal job (K4.1)
keeps its secret when `/renew` answers a key without a new one (**round 2**). With that, the spec's "run `fugaro licence
refresh` after a purchase or seat change" holds.

**F3 (Rule, round 1): "keys are always recoverable in the portal"** against design §7.3. **D2182** (hash
only, rotate) rested on "nothing secret is stored". #2457's 24-hour handover stores the successor's
plaintext key and secret. **D2182 is amended after review, confirmed by the owner 2026-11-12**: the
handover is kept and its bundle is encrypted under a KMS key that only the API's account can decrypt; a
workspace allows one reveal per 24 hours. Following the spec, **no key is ever mailed**: the purchase
email and every notice carry a portal link.

**F4 (Gap): reveal semantics.** The first reveal is the first issue of a bound entitlement (F25). A
reveal mints with a term that ends on the predecessor's `paid_through` (F30), never a fresh period.

**F5 (Gap): who may reveal.** Revealing rotates a live key, so it is limited to the customer
organisation's owners and admins. Resellers and operations see key **metadata** only (key id, seats,
tier, renewal date, last refresh).

**F6 (Exists, Wrong, round 1): the free seat.** The spec mints a permanent one-seat key at sign-up; every
key needs `workspace_id`; and the verifier knows only `paid`, `trial` and `comped` (shipped in v0.23.0,
so a new kind would be refused by released CLIs). Resolved by **D2184 as amended by D2199**: sign-up
creates a free-seat **entitlement** on the organisation; it is minted when bound (F25), Team tier, one
seat, **`kind: comped`** (never revenue), a yearly term renewed while the organisation exists. It
replaces D440's form-based seat; the abuse control is the terms, plus a limit on organisations created
per account per day (F35).

**F7 (Gap): trials and open source are not in the spec.** Resolved by **D2185**: both stay, in the
portal, as entitlements (D2199). The three website forms retire with their routes.

**F8 (Rule, round 1): "no assignment of people to seats".** Resolved by **D2186**: admins always hold a
seat; operators fill the remaining seats **in the order of the grants node's array**, which is already
the order they were granted; later ones act as viewers. No field is added: `internal/access/store.go`
decodes the node with `DisallowUnknownFields`, so a new field would make every installed reader refuse
it. Held with K3.4 under TN1.

**F9 (Rule, round 1): operations minting.** Resolved by **D2187**: operations owners and admins mint,
extend, change seats, comp and revoke; members only read. Each of those actions needs a sign-in no older
than 5 minutes (`auth_time`) with Google (D2207). **D454 is a rate per tier per window that queues
the excess** (`admin.go` `MintCaps`), not a claim cap; P5.1 carries it over. D456's audit covers every
operations action, reads included. Invitations to the operations organisation are limited to an allowed
email-domain list the operations owner sets. D454 and D455 are **open** in design §12; D2187 decides
them (D455 as the identity provider's factor, not Fugaro's) (**round 3**).

**F10 (Gap, security, round 1): the SSO rule and email claims.** The rule follows the **actor's**
membership: a person who belongs to an operations or reseller organisation must have signed in with
Google or Microsoft for any request that uses that membership, whatever organisation it targets. Every
invitation is accepted only by a token whose verified `email` equals the invited address, both folded
by `access.Member`. For Microsoft, an address is a tenant's claim and Firebase's token likely carries no Microsoft tenant
id (**round 3**). Resolved by **D2207**: the operations organisation accepts Google only; a reseller
accepts Google, or Microsoft only with a pinned tenant id if P1.3 finds one in the token.

**F11 (Gap): where the provider is checked.** Resolved by **D2188: API only**. Firestore rules deny every
client read and write.

**F12 (Gap): reseller money.** Resolved by **D2189: daily accrual**; operations records payment.

**F13 (Wrong): "amounts owed to resellers".** Resolved by **D2190**: resellers owe Fugaro; no commissions.

**F14 (Gap): reseller–customer link.** Resolved by **D2191**: the reseller creates the customer
organisation; operations creates reseller organisations and can relink.

**F15 (Gap, security, round 1): Paddle granularity and `custom_data`.** Resolved by **D2192**: one Paddle
subscription per portal workspace (a workspace may be unbound, F25). `custom_data` set in the browser is
the buyer's to forge (they could name a victim's workspace, then refund). **Resolution:** the API creates
the Paddle transaction server-side with `custom_data` and hands the overlay only the transaction id. An
event is admitted when its transaction, or the subscription it belongs to, began with a transaction the
API created (Paddle creates renewals and prorations itself, **round 2**); an event with neither is parked
for operations; a second active subscription for
one workspace is refused.

**F16 (Gap, round 1): Paddle events.** `webhook` decodes a canonical shape, so a real Paddle notification
is dropped with a 200; refunds need a `key_id` Paddle never sends; the route is `/webhook`; the signature's
timestamp is not checked, which design §5.2 chose deliberately (idempotency handles replay); the window
below **reverses** it, and only if Paddle signs every retry with a fresh `ts` (**round 3**). **Resolution:** an adapter from Paddle's notification body to the canonical
event, a refund or chargeback resolved through the subscription or transaction to its entitlement; the
signature and a 5-minute timestamp window verified **first**, then the verified body and its signature
header stored as received **before** processing, keyed by event id (an undecodable verified body by its
digest), so a stranger cannot pre-store a forged body under a real event id (**round 2**); a body that does not decode is stored and answered 400 so Paddle retries and operations sees it;
the route moves to `/api/paddle/webhook`.

**F17 (Gap): deletion.** Resolved by **D2195**: a sole owner cannot delete their account; deleting an
organisation cancels its subscriptions and revokes its entitlements' keys. The Firebase Auth user is
deleted with the account. Raw Paddle events are sales records: how long they are kept and whether their
buyer fields are redacted is counsel's (D457), an open item.

**F18 (Rule, round 1): projects.** **D2194: everything in `fugaro-prod`** (not the recommendation). It supersedes the settled ruling
**T13** (2026-10-27, "a dedicated Fugaro GCP project", design §12 and §9.1 row 5) (**round 3**). The
key service's Terraform has no default project (the ring's default *name* is `fugaro-licence`); this plan
sets the project per apply and puts one key id per ring, which **decides the open D459** toward one ring per environment, not (a)'s one ring for both (**round 3**): production's ring holds only the production key, staging's (`fugaro-staging`) only the
staging key. **Round 1 found that Firebase Hosting's IAM is project-wide**: the website's CI deployer
(`roles/firebasehosting.admin`, runnable by `workflow_dispatch`) can deploy the portal's JavaScript, and
with it take a fresh operations sign-in and mint; project-level `datastore.user`, `run.services.update` on
the minter, or `iam.serviceAccounts.actAs` on the minter's account reach signing too. **Re-asked on
2026-11-12 with these facts; the owner kept `fugaro-prod` and accepted the risk.** The plan still sets the
minter's ingress to internal and its invoker to the API's account only, and Check 571 lists every
project-level grant that reaches signing so the owner sees the exposure.

**F19 (Wrong): Tailwind.** The website has none. The portal imports `website/src/styles/tokens.css`.

**F20 (Gap): shared types with a Go backend.** The API's shapes are JSON Schemas in `ee/portal/schemas/`
(one file per area); Go decodes strictly and tests fixtures against them; hand-written TypeScript types
beside them are tested on the same fixtures. No code generator.

**F21 (Gap, round 1): one hosting config.** The root `firebase.json`'s shape (one hosting object, `public`
`website/dist`) is pinned by the preview workflow's gate, by three website tests and by the deploy
script's test. **Resolution:**
the portal has its own `ee/portal/firebase.json`, deployed with `firebase deploy --config`; the root file
changes only to drop `keys.fugaro.dev` from `form-action` when the forms retire. The portal's CSP admits
Paddle's, Firebase Auth's, Sentry's and the analytics origins.

**F22 (Rule): no agent deploys or operates the service** (`.claude/rules/ee.md`). Every deploy, image
build, project setup, provider account, signing key and the operations organisation's seed are OWNER-RUN.

**F23 (Rule): new workflows** are staged under `docs/pending-workflows/`.

**F24 (Rule): new dependencies.** The spec is the ask for React, Vite, Firebase JS, Sentry, Paddle.js and
Resend; the Go side adds `cloud.google.com/go/firestore`, `cloud.google.com/go/kms` and
`github.com/getsentry/sentry-go`. The portal's lockfile joins the copyleft audit
(`internal/eeboundary`'s licence test reads the console's and mobile's).

**F25 (Gap, owner 2026-11-12): pay before the project exists.** The key needs the workspace's Firebase
project id, which exists only after `fugaro setup`, and a buyer may pay first. #2458 refuses a purchase
event with no `workspace_id`. Resolved by **D2196–D2199**: every grant (purchase, free seat, trial,
operations, reseller, open source) is an **entitlement** held in the portal; an owner or admin binds it to
a Firebase project id in the portal or through `fugaro licence login` (the CLI names its project id; nothing proves it, F37);
the key is minted at binding. The paid term runs from payment (D2197). An owner or admin may rebind once
per 30 days, revoking the old key (D2198).

**F26 (Gap, round 1): nothing is deployable.** No `main`, no KMS signer, no config loader, no image. The
plan adds the two programs, a KMS signer and their image builds (P1.7); the owner builds and deploys.

**F27 (Gap, round 1): `/renew` checks nothing paid.** It renews any key whose mode is not `none`, so a
reseller, comped or open-source key would renew for free forever. **Resolution:** renewal follows the
entitlement: it issues the next key only while the entitlement runs (an active subscription, an unexpired
operations or reseller term, the organisation alive for the free seat).

**F28 (Gap, round 1): operations choosing a refresh policy.** The spec lets operations mint "with any …
refresh policy"; `MintV1` refuses a caller-named policy so no caller can mint a key that never checks.
**Resolution:** operations picks a row, seats and an end date, and may set `check_interval` within
`P1D`–`P30D`; only the air-gapped row has none. The minter enforces the bounds.

**F29 (Gap, round 1): revocation across rotations.** A refund revoking the original `key_id` would leave a
revealed successor valid. **Resolution:** revocation is by entitlement: every key minted for it is revoked.

**F30 (Gap, rounds 1 and 2): reveal must not extend a term.** `MintV1` takes the caller's `TermDays`
capped at the tier's maximum, but it is stateless and cannot know a predecessor's `paid_through`. The API
passes the days remaining; "reveal never extends" is held by the API, not the minter.

**F31 (Gap, round 1): the sign-in verifier.** `mcpauth.Identity` has only `UID` and `Email` and is pinned
to one caller (D923). **Resolution:** it gains `Provider`, `AuthTime` and `TenantID` (added fields), its
comment names the portal as a second caller, and email comparison goes through `access.Member` on both
sides.

**F32 (Gap, round 1): two reveals in a day strand an installation**, and a leaked old secret still fetches
the new bundle for 24 hours. **Resolution:** one reveal per workspace per 24 hours; the other owners are
mailed (a link, no key) at each reveal; operations can cut a handover short.

**F33 (Gap, round 1): Scheduler endpoints.** The API checks the OIDC token's account **and audience**.

**F34 (Gap, round 1, UNVERIFIED): client address behind Hosting.** The throttle's `clientIP` may read the
Hosting edge's address; P1.0 tests which `X-Forwarded-For` entry is the caller's.

**F35 (Gap, round 1): rate limits on the new doors.** Organisations created per account per day, reveals
per workspace per day (F32), trials (one per workspace and account).

**F36 (Gap): workspace screens for `licence login`.** K2.4's authorize and device pages are served by the
SPA, and they list unbound entitlements to bind (D2196).

**F37 (Gap, security, round 2): binding has no proof.** A portal or `licence login` binding takes a
project id nobody proves (TN3: a workspace's own token is not trusted). Resolved by **D2201** (no proof,
limit the harm): trials and every one-per rule key on organisation and project id, so another organisation
naming your id never burns your trial; a phished binding is undone by the owner's rebind. **D2204**
makes a project id unique across organisations (first binder wins; operations transfers). The two meet in
**TN-P1**, answered: global uniqueness stands, so a squatter can block a binding until operations
transfers it.

**F38 (Gap, security, round 2): rebinding multiplies installations.** With rebinding (D2198) and no
revocation on silence (D2140), a full-term key left on an egress-blocked installation runs to `paid_through`
plus grace. Resolved by **D2202**: keys of connected entitlements carry at most 30 days to `paid_through`
and renew through `/renew` while the entitlement runs; air-gapped keys keep their term.

**F39 (Rule, round 2): the free seat for a paying customer.** The terms' §10.2(a) (design §4.2): "a
Customer that has a Paid Plan has no free seat". Resolved by **D2203**: the first paid entitlement ends
the organisation's free seat at its key's next renewal; it does not come back.

**F40 (Gap, round 2): smaller resolutions.**
- One active entitlement per workspace (D2204): a purchase supersedes a trial or free seat there, so the
  spec's "one key per workspace" holds.
- A reseller is never a member of its customers' organisations; it invites the customer's first owner.
- The operations seed is an invitation, and membership binds to the uid at first acceptance.
- Operations accepts Google only; a reseller's Microsoft sign-in needs a pinned tenant, and an empty
  list refuses every `microsoft.com` sign-in (D2207).
- Operations can cut a handover short and transfer a project id.
- Operations sets each reseller's credit limit and wholesale rates.
- Deletion removes memberships and invitations.
- The website preview workflow's gate does not pin `.hosting.site`, so a branch's `firebase.json` could
  target the portal's site: the concrete form of D2194's risk. A staged workflow change pins it.

**F41 (Gap, owner 2026-11-12): the project must exist.** Resolved by **D2205**: before binding, the API
reads `https://<project>-default-rtdb.firebaseio.com/.json` without credentials. Every Fugaro installation
creates that database (`deploy/terraform/gcp/modules/firebase/firebase.tf`); its locked rules answer
"permission denied", while a project with no installation answers "no such database". It catches typos
and invented ids and proves an installation exists, not who controls it (D2201 stands). The id's shape
is checked before it goes into a host name, so the probe cannot be aimed elsewhere.

**F42 (Gap, security, round 3): air-gapped copies.** An air-gapped key has no secret and is never
revoked, so reveal and rebind would each mint a permanent extra copy. Resolved by **D2206**: issued once
at binding, as a file; the same signed text on every download; no reveal or rebind except by operations.

**Threat model of what the portal adds.**
1. *The API asks the minter to sign.* The minter is internal-only, invokable by the API's account only,
   enforces the row, the tier's maximum term and the policy bounds (F28), and logs every signature. It is
   stateless: the predecessor's end (F30) and the entitlement's limits are the API's to hold, so a
   compromised API mints any row for any workspace (accepted since S3, widened by D2194).
2. *An operations account* can mint for any organisation: fresh sign-in, owner or admin role, domain and
   tenant lists, D454's queue, every action audited.
3. *A reseller account* mints for its own customers only, within its credit limit, and never holds a secret.
4. *Reveal* rotates: fresh sign-in, one per day, other owners told, handover encrypted.
5. *Paddle*: server-created transactions, signature with a timestamp window, idempotent by event id,
   stored raw first.
6. *One project (accepted, D2194)*: the website's deployer and any project-level Firestore, Run or
   service-account grant reaches signing; Check 571 lists them.

## Decisions

| # | Question | Recommendation (why) | Alternative (cost) | Owner's answer |
| --- | --- | --- | --- | --- |
| D2180 | Backend language and split (F1) | A TypeScript API plus the Go minter | All Go; all TS | **2026-11-12: all Go. Not the recommendation.** The built code stays |
| D2181 | Key and licence model (F2) | An opaque stable credential plus the v1 text fetched with it | Keep the v2 bundle | **2026-11-12: keep the v2 bundle. Not the recommendation.** **Amended after review** by D2200: the premise "a reissue is fetched by refresh" was false in code |
| D2182 | Recovering a key (F3) | Store it encrypted, show it again | Hash only; a reveal rotates | **2026-11-12: hash only, rotate. Not the recommendation.** **Amended after review**: the 24-hour handover stores the successor, so it is encrypted under KMS and reveals are one per day. **Confirmed by the owner 2026-11-12** (the handover-encrypted option, the recommendation of the re-ask) |
| D2183 | #2457 and #2458 in flight | Hold both | Merge as planned | **2026-11-12: merge as planned. Not the recommendation.** Follow-ups P3.6 and P4.2 change their code |
| D2184 | When the free seat is minted (F6) | At the first workspace | At sign-up, unbound | **2026-11-12: the recommendation.** **Amended by D2199 the same day**: an entitlement at sign-up, minted when bound; `kind: comped` (round 1: the verifier knows no `free`) |
| D2185 | Trials and open source (F7) | Both stay, in the portal | Drop one or both | **2026-11-12: the recommendation.** |
| D2186 | Who is over the seat limit (F8) | Admins first, then operators by grant order | Explicit assignment | **2026-11-12: the recommendation.** Grant order is the array's (round 1: no `since` field). Held with K3.4 (TN1) |
| D2187 | Operations powers and step-up (F9) | Owner and admin, sign-in within 5 minutes, D454, domain list | D455 owner-only; any member | **2026-11-12: the recommendation.** Amends D455; D454 restated as the queueing rate it is (round 1) |
| D2188 | How the SPA reaches data (F11) | API only | Direct reads plus rules | **2026-11-12: the recommendation.** |
| D2189 | Reseller accrual and credit (F12) | Daily accrual | Term up front | **2026-11-12: the recommendation.** |
| D2190 | "Owed to resellers" (F13) | Resellers owe Fugaro | Commissions | **2026-11-12: the recommendation.** |
| D2191 | Reseller–customer link (F14) | The reseller creates the customer | The customer accepts a link | **2026-11-12: the recommendation.** |
| D2192 | Paddle subscriptions (F15) | One per paid workspace | One per organisation | **2026-11-12: the recommendation.** |
| D2193 | Where the SPA lives | `ee/portal/` | `portal/` MIT; private repository | **2026-11-12: the recommendation.** |
| D2194 | Projects (F18) | API in `fugaro-prod`, minter and ring apart | Everything in `fugaro-prod` | **2026-11-12: everything in `fugaro-prod`. Not the recommendation.** Re-asked after round 1 (Hosting IAM is project-wide, so the website's deployer reaches signing): **the owner kept `fugaro-prod` and accepted the risk** (not the recommendation, which was a project of its own) |
| D2195 | Deletion (F17) | Refuse a sole owner; deletion cancels and revokes | Cascade | **2026-11-12: the recommendation.** |
| D2196 | The voucher's form (F25) | An entitlement in the portal, bound by an owner or admin in the portal or by `licence login` | A bearer code; both | **2026-11-12: the recommendation.** |
| D2197 | When a paid term starts (F25) | At payment | At binding | **2026-11-12: the recommendation.** |
| D2198 | Rebinding (F25) | Self-serve, once per 30 days, old key revoked | Operations only; unlimited | **2026-11-12: the recommendation.** |
| D2199 | Which grants are entitlements (F25) | Every source | Paddle purchases only | **2026-11-12: the recommendation.** Amends D2184 |
| D2200 | Successors after a seat change or renewal (F2) | Keep the secret's hash; `/renew` returns the entitlement's newest key | Each reissue rotates through the handover | **2026-11-12: the recommendation.** Amends D2181 and D2135 |
| D2201 | Proof of a binding (F37) | No proof; trials and one-per rules key on org and project | Proof through the installation | **2026-11-12: the recommendation.** Its "never blocks" holds for trials only (TN-P1) |
| D2202 | Rebind multiplier (F38) | Connected keys at most 30 days, renewed while the entitlement runs | Accept | **2026-11-12: the recommendation.** |
| D2203 | Free seat after a purchase (F39) | Ends at the next renewal | Keep it (needs counsel) | **2026-11-12: the recommendation.** |
| D2205 | Binding to a project that does not exist (F41) | (owner's own ruling) | — | **2026-11-12, the owner:** "check that the Firebase project ID exists at the very least"; the unauthenticated database probe is the plan's mechanism |
| D2206 | Air-gapped copies (F42) | Issued once; no reveal or rebind | Cap at two per term | **2026-11-12: the recommendation.** |
| D2207 | Microsoft for operations and resellers (F10) | Pin the tenant or refuse | Separate app; accept | **2026-11-12: "Operations should be Google only"; resellers: Google, or Microsoft with a pinned tenant (the recommendation of the follow-up).** Not the recommendation for operations |
| D2204 | Workspace, entitlements, project id (F40) | One active entitlement per workspace; project id unique across organisations | Several; shared ids | **2026-11-12: the recommendation.** TN-P1: global uniqueness kept, operations transfers |

What this changes in licence key v2 (amended by this plan's task P9.1, not edited here): K2.4 uses P1.3's
verifier, registers its routes through P1.0's registry, and its pages are the SPA's (P3.5); K2.6 is
superseded by P5.1; K2.7 drops its edge on K2.6 and registers through the registry; K3.3 sends the CLI's
Firebase project id and binds an entitlement; K3.4 follows D2186 with the array order; K5.1 is superseded
by the portal's screens; K2.5's mail carries a link; K2.7 keys trials on organisation and project id (D2201); K4.1 keeps its secret
when `/renew` answers without a new one, and connected keys renew monthly (D2200, D2202).

## Open tensions and open items for the owner

- **TN-P1 (answered 2026-11-12):** D2201 meant another organisation never blocks you; D2204's global
  uniqueness lets a squatter bind your project id first. Options were uniqueness per organisation
  (recommended) or global with an operations transfer. **The owner chose global, operations transfers
  (not the recommendation).**

Open items, all the owner's:

- **Confirm the amended decisions**: D2181 (by D2200) and D2182 (encrypted handover) were re-asked and
  answered; D2184's amendment by D2199 follows the owner's own voucher ruling. D2194's accepted risk is
  recorded in the plan's "Limits".
- **TN1 of licence-key-v2 still holds.** Seat enforcement ships only when the owner says so.
- **Counsel.** Reseller invoicing and sales tax; the terms of service and licence terms the portal records;
  the privacy notice (Firestore personal data, analytics, Sentry); D457 retention, including how long raw
  Paddle events are kept and whether their buyer fields are redacted on deletion (F17).
- **Accounts (OWNER-RUN, P0):** Firebase providers (Apple Services ID, Microsoft app registration),
  `app.fugaro.dev` DNS, Paddle live and sandbox, Resend and its DNS, Sentry, the analytics property, the
  signing keys, the image builds and deploys.
- **TOTP for email sign-in** is "later" in the spec and is not planned.

## UNVERIFIED

- The value of `firebase.sign_in_provider` for an email-link sign-in (`password` or `emailLink`); the API
  allows only `google.com` and `microsoft.com`, and P1.3's test pins both values as refused.
- Whether Firebase's ID token carries a Microsoft tenant id at all (P1.3; without one, resellers are
  Google-only, D2207).
- The exact answers of the database probe (D2205) for a locked database and for a missing one (P3.1
  confirms them; a missing project may answer differently from a project without the database).
- Whether Firebase Hosting has any per-site IAM (the plan assumes none, as round 1 read it).
- Whether a `fugaro-licence` ring was ever created (S3.8 is owner-run).
- Which `X-Forwarded-For` entry is the caller's behind Hosting's rewrite to Cloud Run (F34).
- Whether Identity Platform's TOTP works with email-link sign-in (the spec's own question; no task needs it).
- Paddle's server-side transaction API, customer-portal session API and Paddle.js's CSP origins (P4.1,
  P4.3 confirm at build time); Resend's free-tier limits.
