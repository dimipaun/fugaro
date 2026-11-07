# The licence key (D308, as ruled)

**Status 2026-11-07: a design, not built.** `ee/licence/` does not exist, no build reads a key, no
release mounts one, and nothing refuses without one. This note is the text the verifier tasks are
written against — the Go one (`ee/licence/key.go` and `store.go`, subscriptions plan S2.3), the
console's (S2.4), the app's (S2.5) — with the two entry paths (S2.7, S2.8) and the coordinator job's
key (T10, S1.9's secret, deferred with enforcement: no image carries one — the ee-reorg plan's E1.4
builds the tagged image, and `TestRunImagesCarryNoKey` holds it to carrying none). It **supersedes
licensing plan task L3.1** ([implementation-plan.md](implementation-plan.md), "L3.1 — The
licence-key design note"): that is this note before the owner's rulings of 2026-10-27, and its three
design rules (the raw bytes before the JSON, the key id, the clock set back) are carried here, while
the covered-versions date and the release-date stamping it still lists, and O4 dropped, are named
under "Expiry only" below.

## The key

One **Ed25519** signature over a JSON claim set, base64url, checked **offline**: no network call, no
lookup, no phone-home (D308). The key's text is three base64url parts, in this order:

```text
<key id>.<payload>.<signature>
```

The **key id** leads, in the clear, because the verifier has to choose the public key before it can
verify anything: the keyring's keys are named by it, and an id the build does not hold is refused
before either other part is decoded. That is what makes a compiled-in public key **rotate** — a new
key id is added to the keyring and the old one stays until the last key it signed has expired — and
it is what `TestVerifyUnknownKeyID` holds.

The payload is the claim set, and its claims are the licence's own terms:

| Claim | What it holds |
| --- | --- |
| `tier` | `community`, `team` or `enterprise` |
| `features` | the paid features this key turns on |
| `seats` | the user seats the organisation bought; compared and shown, never enforced (D439) |
| `organisation` | what the key was issued to: a domain, an individual as an organisation of one, or an Order Form id (the key-binding ruling, F35, F47) |
| `issued` | when the key was minted, as Unix seconds |
| `expires` | the end of its term, as Unix seconds |

`organisation` is D308's `licensee`, ruled to be the organisation rather than the person: a key binds
to one **organisation** and not to a person or a machine (the key-binding ruling), so every console
and app installation of that organisation holds the same key, and there is no key per user. Nothing
in the product compares the key's organisation with the installation, which is one of the limits
below rather than a check it makes.

## Expiry only (O4)

`issued` and `expires` are the whole window. There is **no covered-versions date** and **no
release-date stamping**: the date named a version window that only the perpetual fallback gave a
meaning to, and D436 dropped the fallback. L3.1's earlier text — the `covers_until` claim and the
`ReleaseDate` stamped into every build — is withdrawn with it, and a version field is a later
decision rather than a gap in this one.

## Raw bytes before claims

The signature is verified over the payload's **raw bytes before any JSON is parsed**. The verifier
decodes the payload, verifies the signature over exactly those bytes, and only then unmarshals them.
Two consequences, and both are rules rather than implementation notes:

- a claim set is signed as it is spelled. Re-wrapping, re-ordering or re-encoding it is a different
  byte string, so it fails the signature instead of verifying as the same claims — which is what the
  non-canonical-signature vector of S2.3 exercises;
- **no claim is read out of a payload whose signature did not verify.** A refusal names the key and
  the part that failed, never a claim, because a claim from an unverified payload is attacker-chosen
  text.

## The check

The check runs at the **`ee/register`** registration point in the CLI, in the console's main process
and at the app's mailbox client — D308 with D303 as amended: the verifier lives in `ee/`, and
**nothing in the core** imports, reads, stores or knows about a key. An untagged build therefore has
no key check at all: the MIT CLI's own commands never check a key, and a build without the
registration launches with no key (`TestUntaggedBuildStillLaunches`). The core reaches the paid code
only through `ee/register`, and never the other way round.

Against `now`, an installed key is in one of three states:

| State | Condition |
| --- | --- |
| `valid` | `now <= expires` |
| `grace` | `expires < now <= expires + 30 days` |
| `expired` | past the grace |

A key outside all three — a bad signature, an unknown key id, a lifetime over its tier's maximum —
is refused rather than given a state. Which of several installed keys applies is the store's
choice, below.

## The lapse (D436, T12)

`expires` is not the end of work. A **30-day grace** follows it (B17), and only after the grace does
the paid side lapse: the console and the app go **read-only**, which keeps reading, cancel and the
kill switch, and loses launching and configuration changes. A running fleet, a queued job
and a hotfix are never stopped by licence state, and the MIT CLI keeps launching and cancelling
throughout. The console's and the app's own shape of that state is the design task S6.1's, and what
a lapse shows is not this note's to fix.

## Renewal and overlap (T26)

The next key issues **30 days before expiry**, and **two keys** may be installed at once. The
verifier takes **the latest valid one** — the one with the later `expires` among those that verify
and are within their lifetime — so a renewal landing early changes nothing for the user, and an
issuing outage only bites once it lasts past 60 days. Overlap is a property of the format and not of
a special case: nothing in a key says whether another key is installed, so two keys are simply two
keys the store holds.

## Limits, accepted

These are stated as limits rather than defended against; the Terms are the enforcement, not the
code.

- **A maximum lifetime per tier.** The verifier refuses a key whose `expires - issued` is longer
  than its tier allows, so **a leaked signing key cannot mint keys old releases accept for ever**:
  the most it can mint is a key that dies within the tier's maximum. **The maxima are this note's
  number and not the Terms'**: one year for every tier today, taken from the longest term the ruled
  issuance already gives a key — section 10.2(b)'s open-source key is "valid for one year", and the
  Team and Enterprise subscriptions renew by the year — with section 10.3's three-month commercial
  evaluation key (D440) well under it. They are stated per tier so a tier can be granted a longer
  term without a change to the key format, and S2.3's `TestKeyBeyondTheTierLifetimeIsRefused` is what
  enforces the bound: a tier's maximum moves here, in a decision, and never in the verifier alone.
- **No revocation.** Nothing asks a server whether a key is still good, so a key works until it
  expires; that is the price of a check that never phones home.
- **Refund leakage, accepted (T25).** A refunded buyer keeps a working key until it expires.
- **A clock set back extends `expires`** (D308). The check reads the machine's clock, so an offline
  expiry is beatable this way; it is a known limit, not a bug to fix later.
- **The source is visible**, so the check is a switch and can be removed by a determined user.
- **Nothing compares the organisation with the installation**, so a key can be shared beyond its
  organisation (F47). Seats are compared and shown, never enforced (D439).

## Test-mode keys

The key id names the environment its signing key belongs to: the owner's signing keys are the
**production** ones and the **staging key id** is a separate one, minted for tests and for the
sandbox installation. A production verifier's keyring holds production ids only, and an id it does
not hold is refused before the payload is decoded — so **a staging key is refused** by every
production build rather than half-accepted, and a test key can never license a customer. The two
keyrings are one lookup table each in the three verifiers, and S2.3's
`TestStagingKeyIDIsRefused` and `TestTestKeyIDIsNotInTheProductionKeyring` hold the two halves.
Publishing the production public half in every verifier is task S2.9, after the owner creates the
keys (S3.8).

## Storage and hand-off (F41)

Each product keeps the key where its own platform keeps a credential, and none of them ever passes
it in a command line: **a key in argv is a key every local process reads.**

| Product | Where the key is kept | How it arrives |
| --- | --- | --- |
| the CLI | one file in the CLI's own config directory (`licence.key`, the directory `internal/localcfg` already returns), one key per line, at most two, mode 0600 | the operator writing the file, or a console that hands the key to the child it spawns |
| the console | the console's own store on the machine, one per installation (S2.7) | pasted into the console's key screen (S2.7) |
| the mobile app | the platform **secure store**, through `mobile/src/accounts/expo-secure-store.js`, the module every credential the app keeps goes through (S2.8) | pasted into the app's settings screen (S2.8) |
| the coordinator job | a **Secret Manager** secret of the installation, mounted into the job and never typed by a person (S1.9, T10) | `fugaro update`, which stores it per installation |

The console spawns the CLI, and the one route to a spawned child is its **environment**: the console
hands the key to the CLI it spawns in a single environment variable (`FUGARO_LICENCE_KEY`), never in
argv, and the CLI prefers the environment over its own file so the console's installation wins. The
console's child environment is an exact allow-list (`CHILD_ENV_KEYS` in `console/src/main/spawn.ts`),
so the hand-off is one new entry in it with its own tests and its own independent review — that is
S2.7's security-sensitive half, not an incidental one. The variable reaches exactly one child and is
never written to a file, a log or an error message.

**Hand minting is OWNER-RUN** (R8): the **private signing key** never enters this repository, a
document, a test or a log, it lives off every machine an agent reaches, and no agent mints, holds or
transports a key. What is public — a public key and a signed vector — is what S2.3's test data may
carry, and the first implementing task runs the image secret scan over a public-key literal
(licensing U5, `images/trivy-secret.yaml`).

## States and minting

- **No key.** An installation of a console or an app with **no key** installed is a real state and
  not an error: a console 0.112.0 upgraded to 0.113.0 has none, a fresh install has none, and a free
  evaluation that never asked for one has none (F43). The verifier itself has nothing to say about
  it — no key is the store being empty rather than a fourth state — and what each product shows and
  what it still allows is S6.1's design.
- **Hand minting now (T13).** Until the entity exists and the owner-operated service is built, keys
  are **minted by hand**: the owner mints them one at a time with the tool of S2.10, against the same
  claim set this note fixes, so the keys of the interim and the keys of the service are the same
  format. The record of what was issued lives in the service's store and the private business
  repository (R5), never here.
- **The service later (T13).** The owner-operated service — a dedicated Fugaro GCP project, Cloud
  Run, a non-exportable KMS signing key, intake split from signing — issues the same keys, with the
  purchase events of the merchant of record feeding it. Its uptime is what the 30-day overlap exists
  to absorb.
- **The coordinator job's key (T10).** The coordinator runs where no person types anything, so its
  copy of the organisation's key is the installation's Secret Manager secret, mounted into the job
  (S1.9; E1.4 of the ee-reorg plan is the image half, and no image carries a key). Nothing is
  mounted yet: enforcement is deferred, and D524 keeps the tagged build and the key apart until it
  lands.
- **An expiring free key is not a special case.** Free keys expire too (F39): a Community key that
  is not renewed reaches the same grace and the same read-only state, and the MIT CLI is unaffected
  as it is for a paid key.
