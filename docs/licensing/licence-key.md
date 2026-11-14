# The licence key (v1; D308 as amended by D2130–D2146)

**Status 2026-11-11: a design.** This is the **v1** text of the key design: the one `fgk_` format and
the claim set of [the licence-key-v2 plan](../licence-key-v2/implementation-plan.md) and its
[reconciliation](../licence-key-v2/reconciliation.md) — decisions D2130–D2146, findings F1–F18 and
tensions TN1–TN5 — and the text the tasks that build it are written against: the Go verifier (K1.1,
`ee/licence/key.go`), the mint tool (K1.3), the console's verifier (K1.4), the project store (K3.1),
the commands (K3.2–K3.4), the renewal job (K4.1, K4.2) and the key service (K2). The production keyring
is empty, so no key exists in the field and the replacement orphans nothing (D2130). The Go verifier of
the old `<key id>.<payload>.<signature>` format went with K7.3, and the console's and the app's readers
of that format remain, with `ee/licence/testdata/vectors.json`, until the tasks that remove those
readers remove the file too.

It **supersedes** licensing task L3.1 and subscriptions **S2.1/S2.3** (the format) and **amends**
S2.4, S2.5, S2.7–S2.10, S3.1–S3.4 and S6.1 where they name the old claims (K7.2 writes the notes for
the plans and the key-signing-service design; S6.1's own note,
[the read-only state after a lapse](../design/lapse-read-only.md), carries this note's key states and
claim names from 2026-11-11). The rules of the old note that the amended decisions keep are carried
here: the signature over the raw bytes before the claims, the offline check at the registration point,
the read-only lapse, the overlap, the tier's maximum lifetime, the staging key's refusal, and where each
product keeps the key.

## The key

One **Ed25519** signature over a JSON claim set, base64url, checked **offline**: verifying a key makes
no network call and no lookup, and nothing about the check phones home. Contact with the licence server
is only ever by an act the user can see — the scheduled job of "Renewal, status and revocation" below,
or a command they run (`fugaro licence refresh`, `fugaro licence login`). The key's text is the spec's
`fgk_` form:

```text
fgk_<payload>.<signature>
```

`payload` is the base64url of the claim set's JSON, and `signature` the base64url of the Ed25519
signature over the payload's **raw bytes**. The `fgk_` prefix is what makes a key recognisable in a
log, in a secret scanner and in a support ticket.

The `kid` claim names the signing key id the signature was made with, and the verifier's keyring is a
map from `kid` to public key. That map is what makes a compiled-in public key **rotate**: a new `kid` is
added and the old one stays until the last key it signed has **passed its grace**, `paid_through +
grace_days` and not bare `paid_through`, because the grace is a state of the **key** rather than of the
build — an id dropped at `paid_through` would refuse, under a build that rotated after that key was
minted, a key the state table below reads as `grace`. An id the build does not hold is refused, and a
production keyring holds production ids only (see "Test-mode keys"). **Open:** the plan does not say how
a verifier reads `kid`, which sits inside the signed payload, before the signature verifies, nor whether
an unknown `kid` is refused before or after the signature check.

The payload is the claim set, and its claims are the licence's own terms:

| Claim | What it holds |
| --- | --- |
| `v` | the policy version, `1` today; see "Policy versioning" |
| `kid` | the signing key id whose public half verifies the signature |
| `key_id` | the key's own id, for revocation and support |
| `workspace_id` | **required**: the Firebase project id of the workspace the key is bound to |
| `organisation` | what the key was issued to: a domain, an individual as an organisation of one, or an Order Form id (the key-binding ruling) |
| `tier` | `community`, `team` or `enterprise` |
| `seats` | the operator and admin seats the organisation bought; compared and shown, never enforced until enforcement ships (D439, D2132, TN1) |
| `issued_at` | when the key was minted |
| `paid_through` | the end of the paid period |
| `grace_days` | the grace after `paid_through`; 30 unless the key says otherwise (D2133) |
| `renewal_mode` | `auto` (the job fetches the next key), `manual` (the user runs `licence refresh`), `none` (offline; the key runs to expiry) |
| `renew_before_days` | how early the job starts trying to fetch the next key |
| `check_interval` | an ISO 8601 duration, `P<n>D` or `P<n>W`, or `null` for none (F16) |
| `renewal_secret_hash` | the hash of the key's renewal secret; the secret itself is never in the key (D2135) |
| `warnings` | who is warned and from when: a role list and `grace_start` or `grace_start+<n>d` |
| `kind` | `paid`, `trial` or `comped` |
| `note` | free text, shown to admins only (optional) |

`issued_at` and `paid_through` are spelled as the spec's payload spells them (RFC 3339 timestamps).

`workspace_id` is required on every key and is checked **offline**: the verifier compares it with the
Firebase project id of the project it runs in, read from the project's own config in the same read as
the key (TN2, amending D2134). A project outside a workspace uses its own Firebase project id, so a
`community` key is bound too. A key without the claim is refused, and so is a key whose claim names
another workspace: a copy of the key is therefore refused in another workspace, while inside the same
one it works (one of the limits below). Moving a key to another workspace is a reissue.

`organisation` is D308's `licensee`, ruled to be the organisation rather than the person: a key binds
to one **organisation** and not to a person or a machine (the key-binding ruling), so every installation
of that organisation holds the same key, and there is no key per user. Nothing in the product compares
the key's organisation with the installation, which is one of the limits below rather than a check it
makes.

## Policy versioning

Every key carries `v`, and Fugaro accepts any version it knows — `1` today. A key with a **higher** `v`
is **refused** with *"This licence key requires a newer version of Fugaro. Upgrade Fugaro to use it."*
It never silently falls back to Free: a key the build cannot read is a refusal, not an absence of a key.

**Unknown fields are ignored for a signed key only** (D2146). The payload of a key whose signature
verifies may carry fields this build does not know, and they are dropped rather than refused. That is
the one named exception to the rule that config and wire formats decode strictly: every other format —
`fugaro.yaml`, the run record, the published policy — and every unsigned input still refuses an unknown
field. Anything that changes a known field's meaning bumps `v` rather than reusing
its name (CLAUDE.md, "A field never changes meaning"). The spec's planned v2 — explicit entitlements in
the key instead of a tier — is a later decision and not a gap in this one.

## Expiry only (O4)

`issued_at` and `paid_through`, with the key's `grace_days` after it, are the whole window. There is
**no covered-versions date** and **no release-date stamping**: the date named a version window that only
the perpetual fallback gave a meaning to, and D436 dropped the fallback. Both were D438's — its
`covers_until` claim and the `ReleaseDate` it stamped into every build — and **O4 dropped them,
superseding that part of D438**; L3.1's text never carried either. `v` is the **policy** version and not
a covered-versions date: it says which claim-set shape the key is, never which releases it covers.

## Raw bytes before claims

The signature is verified over the payload's **raw bytes before any JSON is parsed**. The verifier
decodes the payload, verifies the signature over exactly those bytes, and only then unmarshals them.
Two consequences, and both are rules rather than implementation notes:

- a claim set is **signed as it is spelled**. Re-wrapping, re-ordering or re-encoding it is a different
  byte string, so it **fails the signature** instead of verifying as the same claims — which is what the
  non-canonical-signature vector of K1.1 exercises;
- **no claim is read out of a payload whose signature did not verify.** A refusal names the key and the
  part that failed, never a claim, because a claim from an unverified payload is attacker-chosen text.

## The check

The check runs at the **`ee/register`** registration point in the CLI, in the console's main process and
at the app's mailbox client — D308 with D303 as amended: the Go verifier lives in `ee/` (the console's
and the app's are their own modules, K1.4 and the amended S2.5), and **nothing in the core** imports,
reads, stores or knows about a key (D524). An untagged build therefore has no key check at all: the MIT
CLI's own commands never check a key, and a build without the registration launches with no key. The
core reaches the paid code only through `ee/register`: no core package imports `ee/`, and the boundary
is one-directional — `ee/register` itself calls into the core (`internal/cli`), which is what makes the
registration the only route in.

The check is **offline** — no network call and no lookup — and it is the whole of what verifying a key
does: the contact with the licence server is the visible job of the next section, or a command the user
runs.

Against `now`, an installed key is in one of three states:

| State | Condition |
| --- | --- |
| `active` | `now <= paid_through` |
| `grace` | `paid_through < now <= paid_through + grace_days` |
| `expired` | past the grace |

A key outside all three — a bad signature, a `kid` the build does not hold, a higher `v`, a lifetime
over its tier's maximum, a missing or mismatched `workspace_id` — is refused rather than given a state.
Which of several installed keys applies is the store's choice, below.

## The lapse (D436, D2142, T12)

`paid_through` is not the end of work. A **30-day grace** follows it (`grace_days`, 30 by default,
D2133), and only after the grace does the paid side lapse: the console and the app go **read-only**,
which keeps reading, cancel and the kill switch, and loses launching and configuration changes. A
running fleet, a queued job and a hotfix are never stopped by licence state, and the MIT CLI keeps
launching and cancelling throughout. **Nothing is deleted**: settings and data stay where they are, and
installing a new valid key restores everything at once. D2142 keeps this against the spec's "drops to
Free": "Free" is the MIT CLI plus the no-key state, and read-only is what a lapse costs. The console's
and the app's own shape of that state is the design task S6.1's, and what a lapse shows is not this
note's to fix.

The lapse is the design, but its **effect is enforcement**, and the owner has not yet said enforcement
ships (D523/T27(b), TN1): until that day a paid command warns rather than refuses, and K3.4 and
revocation's lapse effect are held.

## Renewal and overlap (T26)

Two keys may be installed at once, and the verifier takes **the latest valid one** — the one with the
later `paid_through` among those that verify and are within their lifetime — so a renewal landing early
changes nothing for the user. The job starts trying `renew_before_days` before `paid_through`, which is
the key's own number, so an issuing outage is **absorbed by the grace period**; the plan's per-tier
policy table gives `renew_before_days` no default, and that is open. Overlap is a property
of the format and not of a special case: nothing in a key says whether another key is installed, so
**two keys are simply two keys the store holds**, and at most two are kept (K3.1). The old design's
fixed lead — the next key minted a fixed 30 days before expiry, and the 60-day outage that followed
from it — is gone: the lead and the grace are the key's own numbers now (`renew_before_days`,
`grace_days`).

## Renewal, status and revocation (D2131)

The key's own contact with the licence server is a **visible Cloud Scheduler job in the customer's own
project**, beside the image rebuild jobs: it is documented, it is the customer's to see and to delete,
and nothing hidden phones home. The design's other contacts are commands a user runs: `fugaro
licence refresh` (K3.2) asks the server once, and `fugaro licence login` (K3.3) is the browser sign-in,
whose package is the one place under `ee/licence/` allowed a network. `fugaro update` applies the job
(K4.2, the "`fugaro update` is the whole upgrade" rule), so the spec's "`set` and `login` create the
job" becomes: they write the key, and the next `fugaro update` applies the job. An installation without the coordinator's job has none: it renews
by hand (`fugaro licence refresh`) and gets no automatic revocation check (TN5).

`check_interval` is separate from expiry. At that interval the job calls the server's `/status`
endpoint and reads `{status: ok|revoked, newer_key: bool}` (F11); from `renew_before_days` it calls
`/renew`. A key whose `check_interval` is `null` is never checked and simply runs to expiry.

**An unreachable server never revokes** (D2140): a failed, timed-out or errored check leaves the key
valid — only an explicit `revoked` answer revokes — and after 30 days of failures the job sets a
"cannot verify revocation" warning for admins.

The renewal call carries `key_id` and the key's **renewal secret** over TLS, and the server compares the
secret's hash with the one it holds. There is no HMAC: the hash is in the key, which every reader of the
runs bucket holds, so it must not be a signing key (D2135 as amended). Answers are uniform — a caller
without the secret learns nothing, and an unknown `key_id` and a wrong secret answer identically — the
status endpoint carries no key, a revoked or unknown key is never renewed, and a replayed request is
idempotent. A renewal returns the next key and its new secret, reissued under a new `key_id`, which
rotates it.

The server keeps a **revocation list** for refunds, chargebacks and leaked keys. A refund or a
chargeback revokes the key (D2131 overturns T25 for revocable keys), and a leaked key is reissued under
a new `key_id`, the old one revoked and never renewed. How far any of that reaches is one of the limits
below.

## Limits, accepted

These are stated as limits rather than defended against; the Terms are the enforcement, not the code.

- **A maximum lifetime per tier.** The verifier refuses a key whose `paid_through - issued_at` is longer
  than its tier allows, which is the ruled condition: no key is given a term longer than its tier's, and
  a tier's term is stated here so a tier can be granted a longer one **without a change to the key
  format**. **A tier's maximum is its term plus the issue lead its renewal uses**, because the renewal
  above makes that the longest key the design mints: a renewal is minted `renew_before_days` before the
  key it follows expires, and its `issued_at` is the day it is minted while its `paid_through` is the
  end of the term it covers — a ceiling of the bare term would refuse the very renewal the overlap
  exists for. The lead is the key's own `renew_before_days`, so the number a ceiling has to clear is the
  **longest lead that tier's policy may mint**, not a fixed one. **The longest term is the ruled
  issuance's** — section 10.2(b)'s open-source key is "valid for one year", and the Team and Enterprise
  annual terms run for a year (B4; a monthly Team term is shorter and sits under the same ceiling) —
  with section 10.3's three-month commercial evaluation key (D440) well under it. **The per-tier maxima,
  and the lead each assumes, are open**: the plan's per-tier policy table gives `renew_before_days` no
  default, so this note states the rule and not the number, and a lead above the one a ceiling was
  written against would have the server mint keys the verifier refuses — the two move together, in one
  decision, here. K1.1's `TestKeyBeyondTheTierLifetimeIsRefused` is what enforces the bound: a tier's
  maximum moves here, in a decision, and **never in the verifier alone**.
  **It bounds one key's length from that key's own `issued_at`, which is not the same as bounding a
  leaked signing key**: `issued_at` is the minter's to choose, so a key minted with an `issued_at` far
  in the future passes the length check and then reads `active` under the state table for as long as its
  minter wants. Narrowing it would take a rule this note does not have — a refusal of an `issued_at`
  after the check's own `now` — and that rule would still compare against the machine clock the limit
  below says is the user's: a holder moves their clock to meet a key's `issued_at`, which is the same
  move as the clock set back, in the other direction. So the hole is listed rather than closed.
- **Revocation is advisory.** The customer's own operators can write the runs bucket and delete the
  job, so a determined holder keeps a revoked key until it expires, and a key whose `check_interval` is
  `null` is never checked at all: a refunded offline key keeps working to its expiry, which is T25's
  limit surviving where the key asks for no check. That is the price of a check that never phones home
  by itself.
- **The project-level store is licence-grade, not secret.** The key is readable by every identity that
  reads the runs bucket, and a viewer can copy it; with the required `workspace_id` the copy is refused
  in another workspace but works inside the same one, and it cannot renew, since the key holds only the
  secret's hash (D2135). The store has no protection beyond the bucket's IAM (D2137, F9).
- **A clock set back extends `paid_through`** (D308). The check reads the machine's clock, so an offline
  expiry is beatable this way; it is a known limit, not a bug to fix later.
- **The source is visible**, so the check is a switch and can be removed by a determined user.
- **Nothing compares the organisation with the installation**, so a key can be shared beyond its
  organisation (F47). Seats are compared and shown, never enforced until enforcement ships (D439,
  D2132, TN1).
- **One trial per installation is a speed bump.** Uniqueness is keyed by the organisation and the
  workspace's Firebase project id (D2201), because a project id is a caller's own string and proves
  nothing (F37); an address at a free mail provider is refused a trial rather than counted as an
  organisation (D453), since everyone who holds an address there would share one; the self-serve trial
  is one seat and the ask cannot name another (§10.3, B6 — a multi-seat trial is the request form's,
  §17); and the trial path is throttled per domain (K2.7). Another account, or another Firebase
  project, can still ask again.
- **The hand-off reaches one child only where the build has the check.** The removal the storage
  section states is the registration point's, so an **untagged build** — no store, no key check and
  therefore no removal — passes `FUGARO_LICENCE_KEY` on to every child it starts, the same four the
  storage section names; and the console hands the variable to whichever `fugaro` rule 4 resolved, which
  is the tagged build in a Subscription Edition installation and can be the MIT one elsewhere. Closing
  it takes one of two things, neither of them this note's: an edition check in the console before the
  hand-off — the `version` line the handshake already reads **carries no edition today**, so that half
  is a CLI change as well — or the removal in the core for **every** build, which would put one variable
  *name* into the core, where this design puts none. Until one of the two is ruled, the limit stands as
  written.

## Test-mode keys

The `kid` names the environment its signing key belongs to: the owner's signing keys are the
**production** ones and the **staging key id** is a separate one, minted for tests and for the sandbox
installation. A production verifier's keyring holds production ids only, and a `kid` it does not hold is
refused — so **a staging key is refused** by every production build rather than half-accepted, and **a
test key can never license a customer**. The two keyrings are one lookup table each in the three
verifiers, and K1.1's `TestStagingKeyIDIsRefused` holds the production half. Publishing the production
public half in every verifier is K0.2's publication task, after the owner creates the keys.

## Storage and hand-off (F41)

Each product keeps the key where its own platform keeps a credential, and the project keeps it where
every member reads it. None of them ever passes it in a command line: **a key in argv is a key every
local process reads.**

| Product | Where the key is kept | How it arrives |
| --- | --- | --- |
| the project | the key object in the installation's **runs bucket**, beside the published policy, read by `ee/` code only (D2137) | `fugaro licence set` (standard input or `--file`), browser sign-in, or the setup wizard |
| the CLI | one file in the CLI's own config directory (`licence.key`, the directory `internal/localcfg` already returns), one key per line, at most two, mode 0600 | the operator writing the file, `licence set`, or a console that hands the key to the child it spawns |
| the console | the console's own store on the machine, one per installation (S2.7) | pasted into the console's key screen (S2.7) |
| the mobile app | **not the app's two-item secret store**: the app's platform secure storage is `mobile/src/accounts/secure-store.js` (the platform itself, behind `mobile/src/accounts/expo-secure-store.js`), it holds what `secret.js`'s `SECRET_ITEMS` allows and nothing else — `idToken` and `macKey`, a list D232 keeps at two — and the key's own place is S2.8's decision (S2.8) | pasted into the app's settings screen (S2.8) |
| the renewal job | the **Secret Manager** secret of the installation holds the renewal secret; the key itself is read from the runs bucket at run time, and **no image carries a key** | `fugaro update` creates the empty secret (K4.2); `licence set` writes the secret, which is never typed into a command line |

Where the per-machine `licence.key` and the project key differ, **the project key wins** and `status`
says so (K3.1).

**The app's row is deliberately not the Keychain**, and it cannot become one without a ruling this note
does not make. `SECRET_ITEMS` (`mobile/src/accounts/secret.js`) is the whole of what the app may hold: a
closed, frozen list of exactly two names — the Firebase Auth ID token and the per-pairing MAC key — that
`isSecretItem` refuses every other name against, that `secure-store.js` and the accounts store enforce,
that `secret.test.js` pins at two, and that `website/test/privacy.mjs` fails unless it is exactly
`idToken,macKey`. **The owner's D232 ruling of 2026-10-29 keeps it at two**, against the telemetry
plan's four-item list, and D111's exception is written "in these words and no wider ones". So this note
does not add a licence key to it: where the app keeps its key is **S2.8's** decision, in the app's own
storage, and putting it in platform secure storage would take an owner ruling that widens D111 — S2.8's
review is where that would be raised, not here. What the key is, is why the ordinary place is the
admissible one: it binds to an **organisation** rather than to a person, every installation of that
organisation holds the same one, and nothing compares it with the installation (one of the limits
above), so the app's copy is a licence rather than an account credential.

The console spawns the CLI, and the one route to a spawned child is its **environment**: the console
hands the key to the CLI it spawns in a single environment variable (`FUGARO_LICENCE_KEY`), never in
argv, and the CLI prefers the environment over its own file so the console's installation wins. The
console's child environment is an exact allow-list (`CHILD_ENV_KEYS` in `console/src/main/spawn.ts`), so
the hand-off is one new entry in it with its own tests and its own independent review — that is S2.7's
security-sensitive half, not an incidental one.

**The variable reaches one child and stops there where the build that receives it has the check**, and
it is never written to a file, a log or an error message. The console's allow-list is only one half of
that, because the CLI starts children of its own — git, the plugin install, Terraform, the runner in
`fugaro exec` — and each of them inherits the environment as it stands at the moment it is started: a
CLI that only read the variable would hand the organisation's key to every one of them, which is F41's
point in the other direction. The other half is therefore a rule of this design, not an implementation
detail: **the CLI reads `FUGARO_LICENCE_KEY` once, at the registration point and before any command
runs, and takes it out of its own environment in the same step** (`os.Unsetenv`), so no child and no
grandchild inherits a key it has no business holding. The read belongs there rather than in the core for
the reason the section above gives: the store is the CLI's own keeper of the key, and nothing in the
core reads one. `TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts` (`ee/licence/key_test.go`, which
K7.3 removes with the old format) holds it today; **the plan names no v1 test that carries the rule
forward, and that is open**. **A build with no registration point has no such step**, which is one of
the limits above rather than a hole this paragraph closes.

**Hand minting is OWNER-RUN** (R8): the **private signing key** never enters this repository, a
document, a test or a log, it lives off every machine an agent reaches, and no agent mints, holds or
transports a key. What is public — a public key and a signed vector — is what K1.1's test data may
carry, and the first implementing task runs the image secret scan over a public-key literal (licensing
U5, `images/trivy-secret.yaml`).

## States and minting

- **No key.** An installation of a console or an app with **no key** installed is a real state and not
  an error: a console 0.112.0 upgraded to 0.113.0 has none, a fresh install has none, and a free
  evaluation that never asked for one has none (F43). The verifier itself has nothing to say about it —
  no key is **the store being empty rather than a fourth state** — and what each product shows and what
  it still allows is S6.1's design. A project with no key is Free (D2143), and `community` keys stay
  optional in the new format.
- **Hand minting now (T13).** Until the owner-operated service is built, keys are **minted by hand**:
  the owner mints them one at a time with the tool of K1.3, against the same claim set this note fixes,
  so the keys of the interim and the keys of the service are the same format. Minting and the production
  signing key are **owner-run** (K0.2, K1.3's "OWNER-RUN to use"): no agent mints, and the production
  keyring stays empty until the owner publishes the public half. The record of what was issued lives in
  the service's store and the private business repository (R5), never here.
- **The service later (T13).** The owner-operated service — a dedicated Fugaro GCP project, Cloud Run, a
  non-exportable KMS signing key, intake split from signing (K2) — issues the same keys, with the
  purchase events of the merchant of record feeding it. It is **not deployed by any task of the v1
  plan**; its uptime is what the overlap exists to absorb.
- **An expiring community key is not a special case.** Free keys expire too (F39): a Community key that
  is not renewed reaches the same grace and the same read-only state, and the MIT CLI is unaffected as
  it is for a paid key.
