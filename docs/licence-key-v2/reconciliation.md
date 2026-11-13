# Licence key v2: reconciliation of the owner's spec against the code

Checked against `origin/main` at `c13dbda34`, 2026-11-11. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It numbers **D2130–D2179** and **Checks 560–569** (verified free:
no `D213x`–`D217x` and no `Check 56x` appears under `docs/` on `main` or in any open pull request).

The spec was written without the repo, so it reads greenfield. It is not: a licence key is **ruled
(D308, D436, D438, D440, T25, T26, the key binding) and mostly built**. This spec is the one the
owner said would take over licensing plan task **L3.1**, and in practice it **replaces the key
design** of [docs/licensing/licence-key.md](../licensing/licence-key.md) (subscriptions S2.1). The
reconciliation is therefore mostly a list of where the spec reverses a built or ruled thing, so the
owner rules on each reversal rather than finding it in a diff.

## What the repo already has

| Spec concept | What exists on `main` |
| --- | --- |
| Signed offline key, Ed25519 (§1, §3.1) | `ee/licence/key.go`: `<key id>.<payload>.<signature>`, signature over the raw payload bytes, keyring by key id, refused before decode when the id is unknown. **`productionKeyring` is empty** (`ee/licence/key.go`): no production key has been signed or shipped, so no key exists in the field |
| Claim set, policy (§3.2) | `licence.Claims`: `tier`, `features`, `seats`, `organisation`, `issued`, `expires`. No `v`, no `kind`, no grace, renewal, check or warning fields; decoded **strictly** (unknown field → `ErrClaims`) |
| Key store (§11.2) | `ee/licence/store.go`: one file `licence.key` in the CLI's config directory, at most two keys, plus the one `FUGARO_LICENCE_KEY` variable the console hands a child (read once and `Unsetenv`'d). **No project-level store; no `fugaro licence` command** |
| Verifiers elsewhere | console: `console/src/main/licence.ts`, `licence-store.ts`, `licence-state.ts`, `spawn.ts` (`LICENCE_KEY_VAR`). App: none |
| Signing in KMS (§5) | `ee/licence/mint/kms.go` (the owner's hand-minting tool, signs only through a KMS signer); `ee/keyservice/terraform/keyring` |
| Licence server (§12) | `ee/keyservice` (`service.go` intake, `minter.go`, `merchant.go`): routes `/free-seat`, `/open-source`, `/request`, `/verify`, `/webhook`; Paddle webhook, idempotent, test-mode pair; email verification, one per domain, free-mail list, manual queue. **Not deployed; no accounts, no sign-in, no renewal or status endpoint, no revocation list, no admin handler** |
| Grace and expiry (§6, §9) | `Grace` = 30 days (B17); `docs/design/lapse-read-only.md`; `ee/register/lapse.go` (`access.SetSubscriptionLimit`): after grace the console and app go **read-only** (read and cancel kept) |
| Seats (§10) | `claims.seats` compared and shown, **never enforced** (D439); `internal/cli/seats.go` is the advisory estimate; roles `viewer`/`operator`/`admin` in `internal/access/access.go` |
| Scheduler jobs in the customer's project (§7) | Terraform `google_cloud_scheduler_job`: `preload_rebuild`, `reaper_sweep` (`modules/compute`), `catalog_check`, `coordinator_sweep`, `history_sweep`, `history_rollover` (`modules/installation`); the deployer's IAM names `cloudscheduler.jobs.*` |
| Paddle (§2, §12) | `ee/keyservice/merchant.go` (Team checkout, D442); Enterprise is invoiced directly |
| Trials (§14) | D440's plans in `ee/keyservice`: commercial free seat (3 months, one per domain), open source (1 year). No 14-day Team trial |

## Findings

**F1 (Exists, Rule): the spec replaces a ruled design.** The key's format, claims, binding, grace and
lapse, seat treatment and the "no revocation" limit are all owner rulings (D308, D436, D438, D439,
T25, T26) recorded in `docs/licensing/licence-key.md` and `docs/subscriptions/reconciliation.md`.
Each reversal below is a decision (D2130–D2148) with the owner's answer; none was assumed.

**F2 (Wrong): "no hidden phone-home" is true but the built design goes further.** D308's "no network
call" and "no revocation" (T25: a refunded or charged-back key works to expiry) are the opposite of
§8. The spec's job is visible and key-driven, which is why the owner could overturn it (D2131), but
**three docs and two tests state the old limit** and move with it: `licence-key.md` "Limits,
accepted", `TestLicencePackageImportsNoNetwork` (the verifier still makes no call; the job is a
separate command), and the Terms' refund text (counsel, K0.1).

**F3 (Wrong, Exists): the key text and payload differ.** Spec `fgk_<payload>.<sig>` with `v`, `kid`,
`key_id`, `workspace_id`, `paid_through`, `grace_days`, `renewal_mode`, `renew_before_days`,
`check_interval`, `warnings`, `kind`, `note`; built `<id>.<payload>.<sig>` with `features` and
`organisation` that the spec lacks. The spec drops `features` (tier decides) and its v2 note says
entitlements come later; the built tests pin `features`. Resolved by D2130 (one format, v1 of the
spec) and the claims table in the plan's K1.1. Because the production keyring is empty (F1) nothing
issued is orphaned.

**F4 (Rule): the spec's "unknown optional fields are ignored" contradicts the strict-decode rule**
(CLAUDE.md "Config and wire formats decode strictly"; built `decodeClaims`). Resolved by D2146 as a
named exception for signed keys only.

**F5 (Rule): `fugaro license set <key>` puts the key in argv.** The built design's F41 ("a key in
argv is a key every local process reads") forbids it. **Resolution (no decision needed):** `set` reads
the key from standard input or `--file`, a positional key is refused with the same message, and
`FUGARO_LICENCE_KEY` stays console-only. The command is spelled `licence`, matching the code, the
variable and every doc; `license` is accepted as a hidden alias so the spec's spelling works.

**F6 (Gap): the core must never read a key (D524).** All of `licence set/status/refresh/renew/login`,
warnings and seat enforcement live in `ee/register`; an untagged build has none of them
(`TestUntaggedBuildStillLaunches`), and the MIT CLI never warns about a licence. The spec's "warn
everyone, developers included" is therefore delivered by the console, the app and the tagged CLI's
ee commands only. Consequence recorded in the plan (K3.5).

**F7 (Gap, security): the renewal request in §7 is unauthenticated.** `key_id` and `workspace_id` are
not secrets (the spec says key_id is for support tickets and logs). Anyone with them could pull the
holder's next key, and the next key is a bearer credential. Resolved by D2135.

**F8 (Gap): `workspace_id` has no referent.** A Fugaro *workspace* (`docs/workspaces`, D1290–D1305) is
a group of projects with its own Firebase project; the spec says "workspace/project". The built key
binds an `organisation` that nothing compares (F47). D2134 keeps the organisation and adds an optional
`workspace_id` (not the recommendation, which was a checked installation id); the plan therefore
pins what the verifier does with the claim and what it leaves unchecked (Open tension TN2).

**F9 (Gap): "stored at project level" has no home** (the CLI file and console store are per machine).
Resolved by D2137: the key text lives in the installation's runs bucket beside the published policy,
read by `ee/` code only; the renewal job's secret is the installation's Secret Manager's. A viewer
reaches the key's *state* through `fugaro licence status --json`; the store has no protection beyond the bucket's IAM.

**F10 (Rule, HARD RULE, D524): a renewal job is a `fugaro update` concern, always applied.** The core reads no key, so `update` cannot apply it only when a key asks; the command no-ops without a key, and on a managed project it applies without a prompt. A Scheduler job in the customer's
project is Terraform's (`modules/installation`), not something `licence set` creates (a direct API call
would drift from state, be refused in an agent session, and break the "`fugaro update` is the whole
upgrade" rule). The spec's "set and login create the renewal job" becomes: they write the key and,
**with the installation's consent where it asks one**, the next `fugaro update` applies the job; `set`
prints that one line. An installation at the previous version must converge in one `update`, safe
with runs in flight (`TestUpdateLeavesARunInFlightRunning`). The job's image is the key-gated
build the coordinator job already uses; `TestRunImagesCarryNoKey` still holds (the key is read from the
bucket and Secret Manager at run time, never baked).

**F11 (Gap): what a "check" returns.** §3.3 says a seat increase takes effect "at the next renewal or
check", but §8's status endpoint only answers `revoked` or not. The plan defines one response shape:
`{status: ok|revoked, newer_key: bool}`; the job then calls the renewal endpoint. Adding a field later
is allowed; reusing one is not (CLAUDE.md "A field never changes meaning").

**F12 (Rule): lapse.** Spec §1/§9 "drops to Free, nothing broken" differs from D436/T12 read-only after
grace. D2142: D436 stands; "Free" means the MIT CLI plus the no-key state. The warning table (§7.1)
counts from grace's start and is unchanged by that.

**F13 (Rule): enforcement is deferred.** D523/T27(b) says paid commands warn, not refuse, "until
enforcement ships", and the owner has not said it ships. Seat enforcement (D2132) and revocation's
effect on the console are *enforcement*; the plan marks K3.4 and the effect half of K4.1 as **held
until the owner says enforcement ships**, with the verifier, the store, the commands, the job and the
server all buildable first (Open tension TN1).

**F14 (Gap): the licence site has no sign-in.** §11.3/§12 assume accounts. D2144: Firebase Auth. The
owner asked whether each workspace's Firebase project can be the IDP. Facts: each Fugaro project is
its own Firebase project and a workspace has its own (`docs/workspaces/spec.md`, `docs/mobile-auth`),
but Firebase's signing keys are one global set, so a token from any customer's project verifies; what
it proves is "someone controls that project", not who bought. **Recorded as: Firebase Auth in the
owner's licence-service project (email link plus Google), not the customer's.** Using a workspace's own
Firebase token to prove the `workspace_id` binding at `licence login` is a possible later claim check
(TN3, answered: fugaro-prod).

**F15 (Gap): the admin page and the manual queue.** §13 (reseller, comped, revoke, reissue, override
policy) and the design's §6 exist as a design (`docs/design/key-signing-service.md` §6) but `ee/keyservice`
has no admin route. A comped key is `kind: comped`, expiring, never in revenue reporting; the owner-only
access rule of the design (`TestAdminRefusesAnyoneButTheOwner`) carries over.

**F16 (Gap): ISO 8601 durations.** `check_interval: "P1D"` needs a parser the repo does not have. The plan
accepts only `P<n>D` and `P<n>W`, refuses anything else, and `null` means none.

**F17 (Exists): signing keys, rotation and test mode** (§5): the keyring map holds any number of key
ids, a staging id is refused by a production keyring (`TestStagingKeyIDIsRefused`); "current and next" is
two entries of that map. No change beyond naming.

**F18 (Gap): a clock set back extends a key and a leaked key's `issued`** stay accepted limits; the
revocation check adds a server-side bound only where a key's `check_interval` is non-null.

**F19 (Rule): a key the licensor mints by hand cannot renew, and §12.1 says it can.** `/renew` renews a
key only where the record of its issuance names the subscription the checkout sold it for, and a key
whose record names none is refused with the endpoint's uniform answer — the owner ruled on 2026-11-13
that it fails closed (**D2148**; the alternative, renewing whatever key presents a secret that verifies,
is an unpaid renewal for every hand-minted key). Every route that mints by hand writes neither a
subscription nor a key state: the admin page and the manual queue (§13, K2.6), the free-seat and
open-source intake (K2.2) and the owner's mint tool (K1.3) all reach the one mint in `service.go`. None
of those keys renews, and nothing else renews them either: the merchant's renewal mints for a
subscription when a payment completes, and `/renew` is the only route that mints for a key a holder
presents. §12.1's Enterprise column and `PolicyEnterprise` say `renewal_mode: auto`, while §13 has the
owner name an invoiced key's period — and the minter mints a caller-named term only for a row something
renews, so the two stand or fall together. TN6 holds the two ways out, both the owner's; K2.6, K2.7 and
K4.1 build against this ruling.

The other half of the same ruling is what a renewal is *for*: the term a successor covers is one the
customer has paid for, so `/renew` mints only where the subscription's own end already reaches the end
of that term, and it never moves that end itself. The subscription's end is the merchant's record of
what has been bought and the trigger's renewal is the only writer of it (§5.1, `merchant.Renew`); a
purchase with no renewal behind it is a subscription ending where its key ends, and a renewal against it
is answered the key's own state and no key. The route's own gate would otherwise be asking a question it
had just answered for itself: a write of that end credits the caller the term it is about to mint, so a
customer who paid once walks a chain of keys a term at a time — each with a key id, and so a throttle
bucket, of its own — while the merchant's renewal, which is what the money is for, finds its window
already moved past.

**F20 (Rule): a refund withdraws the licence, and §8's list is keyed by one key id.** A licence a
customer holds is several keys: the purchase, the key a **seat change** reissued inside the term they
already paid for (§5.3 — the term is the subscription's and only the seats are in the key), the key the
merchant's own renewal minted (a renewal is §5.1's ordinary route, so that is the key an installation
usually runs), and the `/renew` rotations of any of those. Every entry of §8's list names one key, and
the key a merchant's refund or chargeback resolves to is the one its own transaction carries — the
purchase, after a seat change or a renewal has replaced it. The owner ruled on 2026-11-13 that such an
event therefore withdraws the whole licence and not the key it names (**D2147**): the licence is matched
by the subscription its records name, and the rotations of each of those keys are followed through
`rotation:<the key it replaces>`. A **cancellation** withdraws nothing: it carries no key id in this
shape, §5 gives §8's list refunds, chargebacks and leaked keys, and design §5.3 has a cancellation stop
the reissue while the key the customer holds runs to its end. What a cancellation does is enforced by
D2148's gate instead; if the owner means it to withdraw keys too, the event needs the key it names and a
ruling on the leakage §5.3 accepted.

**Threat model of what the spec adds.** (1) *The renewal and status endpoints* are an internet-facing path
that mints credentials: authenticated by the per-key secret (D2135), throttled, uniform answers, and the
status endpoint carries no key. (2) *Revocation availability*: it can never revoke on silence (D2140); a
network block therefore turns revocation off, which is why 30 days of failure warn admins. (3) *The
loopback listener* in `licence login`: bind `127.0.0.1` only, one request, `state` and PKCE verifier, a
short deadline, never log the code. (4) *Device code*: short user code, rate-limited and expiring, the
poll answers the same for unknown and expired codes. (5) *Email delivery of keys* (§12) puts a bearer
credential in mail; the mail carries the key text only to the verified purchaser and the account page
re-issues it, so a mailbox leak is the same exposure the built key service already accepts (T25). (6) *The
project-level store* is a licence-grade object readable by every identity that reads the runs bucket;
a viewer **can** copy the key; with the required `workspace_id` (D2134 as amended by TN2) the copy is refused in any other workspace, so the limit is the same workspace only (the plan's "Limits this plan accepts"). It cannot renew: the key holds only the secret's hash and the renewal call sends the secret itself. Round 1 amended F7's fix, because an HMAC keyed by that hash would have been forgeable by any bucket reader.

## Decisions

| # | Question | Recommendation (cost of the alternative) | Owner's answer |
| --- | --- | --- | --- |
| D2130 | One key format: replace the built `<id>.<payload>.<sig>` with the spec's `fgk_` v1 (F3) | Replace (alternatives: two formats forever; keep old, lose in-key policy) | **2026-11-11: the recommendation.** Supersede; the empty production keyring means nothing is orphaned |
| D2131 | Revocation via a visible job, overturning D308/T25 for revocable keys (F2) | Yes, only where `check_interval` is set; offline keys never check (alternatives: keep T25; defer) | **2026-11-11: the recommendation.** |
| D2132 | Seat enforcement, overturning D439 (F13) | Enforce in `ee` surfaces only; the MIT CLI unaffected (alternatives: advisory; core check, which D524 forbids) | **2026-11-11: the recommendation**, subject to TN1 (the owner's enforcement go) |
| D2133 | Grace | `grace_days` carried by the key, server default **30** (B17, T26) (alternatives: 14; fixed 30 outside the key) | **2026-11-11: the recommendation.** The spec's 14 becomes 30 |
| D2134 | What `workspace_id` names | Recommended: a checked installation id | **2026-11-11: NOT the recommendation.** Keep `organisation`, add an optional `workspace_id`. **Amended by the owner the same day (TN2): `workspace_id` is REQUIRED on every key and is checked offline against the Firebase project id of the workspace** (a project outside a workspace: its own Firebase project id) |
| D2135 | Renewal authentication (F7) | A per-key renewal secret, hash in the key, secret sent over TLS and held in the installation's Secret Manager (alternatives: the scheduler SA's identity; none) | **2026-11-11: the recommendation.** **Amended after review:** the call sends the secret and the server compares its hash, with no HMAC (a hash in the key is readable by every bucket reader). **Confirmed by the owner 2026-11-11.** |
| D2136 | Trials | Add the 14-day Team trial beside D440's plans (alternatives: replace D440; none) | **2026-11-11: the recommendation.** One per installation |
| D2137 | Where the key lives per project (F9) | The runs bucket beside the published policy (alternatives: Secret Manager; per machine) | **2026-11-11: the recommendation.** |
| D2138 | Grace of trial and air-gapped keys | Trial 0, air-gapped 30 | **2026-11-11: the recommendation.** |
| D2139 | Seat holders | Count in the key, holders in the project (the spec's assumption) | **2026-11-11: the recommendation.** |
| D2140 | Revocation check unreachable | Valid; admins warned after 30 days of failures | **2026-11-11: the recommendation.** |
| D2141 | Paddle scope | Team via Paddle, Enterprise direct (D442 stands) | **2026-11-11: the recommendation.** |
| D2142 | Lapse behaviour (F12) | D436 read-only stands | **2026-11-11: the recommendation.** |
| D2143 | A project with no key | No key is Free; community keys stay optional in the new format | **2026-11-11: the recommendation.** |
| D2144 | Licence site sign-in (F14) | Recommended: email magic link | **2026-11-11: NOT the recommendation.** Firebase Auth, as the owner asked ("why not use the firebase IDP"); read as the **owner's licence-service project**, TN3 asks the owner to confirm that reading |
| D2145 | Command spelling (F5) | `licence`, with `license` a hidden alias | **(default)**: stated as a resolution, not asked of the owner |
| D2146 | Unknown fields in a signed key (F4) | Ignore them, signed keys only | **2026-11-11: the recommendation.** |
| D2147 | What a refund, a chargeback or a cancellation withdraws (F20) | Every key of the licence the event's key belongs to, matched by the subscription its records name, plus the rotations of each of them (alternatives: the key the event names alone, which leaves the key an installation runs; the rotation chain only, which leaves the key a renewal or a seat change minted) | **2026-11-13: the owner ruled it.** The named key and every other key of that licence, not the named one alone. Read against §5 as: a refund and a chargeback withdraw; a **cancellation** names no key and is D2148's gate instead |
| D2148 | What `/renew` renews (F19) | Only a key whose issuance record names the subscription the checkout sold it for, and only into a term that subscription's own end already reaches: the merchant's renewal is what moves that end and this route never writes it (alternatives: renew whatever key presents a secret that verifies, which is an unpaid renewal for every hand-minted key; refuse only where the record is missing *and* the mint route was a checkout one, which the record cannot tell) | **2026-11-13: the owner ruled it.** It fails closed, and a term is handed out only where the money for it has arrived. §12.1's Enterprise `renewal_mode` is left saying `auto` and TN6 carries the disagreement |

## Open tensions and open items for the owner

- **TN1: seat enforcement and revocation's effect against D523/T27(b).** The owner ruled paid commands warn
  and nothing refuses until enforcement ships, which has not been ruled. D2131 and D2132 are the
  enforcement. Options: (a) build everything now, hold the *effect* (K3.4, K4.1's drop-to-lapse) until
  the owner says enforcement ships (the plan's reading); (b) ship the effect with the key. (a) holds
  back revocation from the first key; (b) reverses T27(b).
- **TN1: answered 2026-11-11 (the recommendation).** Build everything, hold the effect (K3.4 and revocation's lapse effect) until the owner says enforcement ships.
- **TN2: answered 2026-11-11, amends D2134.** `workspace_id` is required on every key and names the Firebase project id of the workspace; the verifier compares it with the project's own Firebase project id, read from the same place as the key. Consequences the plan carries: a free-seat or open-source key's web form asks for the project's Firebase project id; a key is tied to one workspace (moving it is a reissue); `community` keys are bound too. Nothing else about the binding changes (`organisation` stays, unchecked).
- **TN3: answered 2026-11-11.** Firebase Auth in the owner's own project, `fugaro-prod`; a customer's workspace Firebase token is not trusted by the server.
- **TN5: answered 2026-11-11 (the recommendation).** An installation without a coordinator renews by hand (`licence refresh`) and gets no automatic revocation check.
- **TN4: answered 2026-11-11 (the recommendation).** The owner confirmed D2135's amendment.
- **TN6: the Enterprise column's `renewal_mode: auto` (F19) — the owner's.** Nothing serves it: an
  invoiced key is minted by hand and never renews (D2148), so the claim is a promise the service does not
  keep — while §13's "issue an Enterprise key for a workspace, seats and period" needs the row to stay
  one something renews, because the minter refuses a caller-named term for a row nothing renews. Options:
  (a) read the column as `none`, which makes the claim true and takes the named period away from §13 (the
  admin page then mints at the row's own term); (b) give an invoiced licence a renewal route of its own —
  a subscription-shaped record for a direct sale, which D2141 keeps out of Paddle; (c) leave both as they
  are, and K2.6, K2.7 and K4.1 build knowing `/renew` serves checkout keys only.
- **Pending the owner's go:** no task is approved. K0.1 (counsel: the Terms' refund and the privacy text
  for the renewal contact) and K0.2 (production signing keys, S3.8) are owner-run.

## UNVERIFIED

- Whether any key was ever hand-minted with the S2.10 tool against a real KMS key (the keyring is empty in
  code; the owner's private records are not read here). D2130 assumes none.
- That the key-gated coordinator image can run an `ee` command other than the coordinator's (K4.2 checks).
- Whether the website's CSP `form-action` allowance (the owner's 2026-11-07 ruling) covers the new
  authorize and device pages (K5.1 checks).
