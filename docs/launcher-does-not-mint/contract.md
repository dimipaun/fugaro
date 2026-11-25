# The identity wire contract

This is the document of **C1**: the wire contract the credentials service's five identity routes
are built on, declared once in `internal/identitywire/wire.go` and read by every side that speaks
it — the service's handlers (LM1 to LM4), the launcher (LM6), the runner (LM7), the resident
orchestrator's ask (LM24) and the rules test that pins where the record lives (LM5). It is
written against [reconciliation.md](reconciliation.md) R2, R3, R5 to R8 (which wins over
[spec.md](spec.md) where the two disagree) and against D3121, D3122, D3125, D3126, D3136 and
D3137.

It is a package of its own, depending on no service code: `internal/credsvc` and `internal/cli`
import it and not the other way round, so the sides that speak it cannot drift on the spelling of
a field. Refusals are still the service's own shape (`internal/credsvc`'s `WireError`, read
through `credsvc.ReadWireError`), because both callers already import that package; this contract
adds no second refusal type.

## The five routes

| Path | Admits | Body | Answer |
| --- | --- | --- | --- |
| `/v1/identity/ticket` | person, roster service account, orchestrator | `TicketRequest` | `TicketResponse` |
| `/v1/identity/claim` | job account | `ClaimRequest` | `ClaimResponse` |
| `/v1/identity/pairing` | person | `PairingRequest` (`{}`) | `PairingResponse` |
| `/v1/identity/device` | person | `DeviceRequest` (`{}`) | `DeviceResponse` |
| `/v1/identity/probe` | person, **operators only** | `ProbeRequest` (`{}`) | `ProbeResponse` |

The ticket is asked for before the run exists and the claim is the run's first call, so every one
of the five is answered **ahead of the shared request parse** — the caller holds no run identity
yet — as the merge mint is for `VerifyOrchestrator`. `identitywire.Routes()` is the one spelling
of the table, `RouteFor(path)` tells a request of this contract from any other route of the
service, and `Route.AdmitsClass` is what a handler branches on.

`OperatorsOnly` is a fact of its own and not a second spelling of `Admits`: the person class is
one list and the roster's operators are the owner's subset of it that `update` may be run by. The
probe admits that subset alone.

The person class is the roster's `user:` entries, whose Google ID token the service verifies as
it already verifies a launcher's (`VerifyLauncher`). A `group:` entry is not a class: a group
holds no token to present, and `update` names such entries before it moves the grant (D3123,
D3126). A roster service account is admitted unscoped, as it is today (D3137). The orchestrator
is a class of its own because what it may be issued is bound to its record and judged by no
launch scope (D3125 as amended by D3136).

## What a caller may name, and what it may not

No **request** of this contract has a field for a uid or for a free claim set. The run and the
repository are the launcher's to name; everything derived from them is the service's — the uid
comes from the record's own path (`token.UID`), the project claim is the installation's own, and
the claims are read off the record when the service signs (R3). A body that names a uid or a
claim set anyway is refused **by name** as an unknown field: every body is read with
`DecodeStrict`, which is a `json.Decoder` with `DisallowUnknownFields` and a trailing-data check
(`Token()` must be EOF). That is what makes "the wire has no field for a uid or a free claim set"
a refusal rather than a byte quietly ignored, and what `TestIdentityWireHasNoCallerChosenIdentity`
pins — by reflection over each request type's fields, and by asking the decoder a body that names
one.

A **response** may report an id the service itself derived: the pairing's and the device's. Those
are values the service draws from `crypto/rand`, reported under the name of the thing they
identify; they are not identities, no route reads one back from a caller, and the CLI needs the
handle of a thing it did not mint.

`TicketRequest` names the ingredients of the claims and never the claim set: `slug`, `run`,
`account` (the job account the launch will run as), `launcher` (the self-asserted `rb`, R11),
`branch` (`fb`), `pull_requests` (`fpr`), `context_repos` (`fcr`), `max_runtime_seconds` and, for
the orchestrator alone, `attempt` or `resumes` — one of the two and never both. The service
derives the expiry, refuses a runtime beyond its configured maximum rather than clamping it,
refuses an `account` that is not this repository's job's account, and judges each context
repository for the view action and against the policy's allow-list before the list is signed
(R4, R16, F31).

`ClaimRequest` carries the ticket and the nonce — and, where the job is on Batch, one field more —
and nothing else: the uid, the claims and the account are the record's, and the caller is the
job's own Google ID token, of which nothing but the account is read. **The extra field is BR4 of
[batch-run-identity](../batch-run-identity/README.md)** (its R1): `attestation`, the run's own VM
identity token in the metadata server's full format, which the service serves the identity to a
job's own VM by. It is empty for a Cloud Run launch, whose job's token already names the one
execution, and a service of the release behind refuses the field it does not know rather than
serving an identity on the job's token alone.

## The claim's nonce (R2)

The runner draws a nonce and keeps it in memory for as long as it retries. The same nonce again
gets the same answer, so a retry after a lost response is **not** a second claim; another nonce
after a success is refused. The record keeps the nonce's **digest** (`Claim.NonceHash`), never
the nonce; the nonce is written to no log line and no audit line; and the same-nonce answer holds
only for the ticket's own life. Stated: a ticket, its nonce and the job's identity token together
are a token for that long — of the three only the nonce is confined to the run's own process.

## The probe (R2, R8, F33)

`fugaro update` calls the probe before it moves the grant. The service signs the fixed payload
`identitywire.ProbePayload` as the runs' signer and **discards** it, and answers `ProbeResponse`
with the account it signed as and when. The payload is a constant of this contract so that the
route is neither a signing oracle nor an open trigger of the signing quota; the answer carries
**no signature bytes and no digest of them**, because what the probe needs to prove is that the
row and the account are right, which the signer's own name proves. The probe admits the roster's
operators only and writes one audit line per refusal.

## The attempt reference (D3136)

An orchestrator's ask carries `Attempt`: the id of the orchestrator's own intent for that attempt,
which is the item's id, a colon and the attempt's number, exactly as `internal/orchestrator`'s
`IntentID` writes it and `IntentItem` and `IntentAttempt` read it back. `AttemptID.Valid` mirrors
that spelling: a non-empty item id, a colon, and a non-zero attempt number of at most nine digits
read one by one, so that `wi-1:1x` is not attempt 1 and two references that mean one attempt
cannot both be looked up. The issuer looks the reference up on the orchestrator's own record and
serves only a recorded, unstarted attempt; no other class may name one, and an orchestrator's ask
that names none is refused.

## The resume reference (D3189)

An orchestrator's ask may carry `Resumes` instead of `Attempt`: the id of the paused run it
continues — the same reference a resumed run's task names (`runstore.PauseMemberResumes`), so that
the ask and the task that run is launched with call the paused run the same thing. It is the
orchestrator's own, exactly as `Attempt` is, and an ask names one of the two and never both: a run
either re-issues an attempt of the orchestrator's own record or continues a pause the credentials
service recorded.

The two are looked up in different places, and that is the whole of the case D3189 adds to D3136.
An attempt is judged against the orchestrator's own state node — written over IAM by the database
admins, so a launcher with a modified CLI cannot write it. A resume is judged against **the
service's own record of the pauses it wrote**: the node at `runstore.PauseNodePath(slug, run)`,
under the `paused` subtree of the database, which the service alone writes (R19). A run's own
word about a pause it suspended is the record in its own bucket (`pause.json`), which is a hostile
agent's (R23) — a resume derived from that one would be a resume any agent could ask for (F52) —
so the issuer never reads it. The record cannot carry the resume either: the run that continues a
paused one is a new run of the chain, so no intent of that node names it.

What this route does not judge is the grant's own rules — that the node's resume time has passed,
that it carries no cancellation, that the paused run really ended `paused`. Those are the slip's,
where a resume's launcher is judged (contract-pause, "Resuming"). What the route answers is the
one question it is the door for: is this ask one this service's own record carries at all?

## The refusal codes

The codes are this contract's own, in the shape the service's other routes already write.

| Code | When |
| --- | --- |
| `not_served` | the identity half is not configured: the routes are mounted, no signer is (I1) |
| `caller_not_admitted` | a verified caller the route does not admit: off the roster, a job account at the ticket, a non-operator at the probe |
| `no_launch_scope` | the caller is admitted and this repository is not one it may launch (LM23) |
| `identity_refused` | the ask itself: an account that is not the record's job account, a run on another caller's record, a ticket already claimed or beside an outcome, a runtime beyond the maximum, an unknown or expired ticket, a nonce that is not the claim's own, an orchestrator's attempt the record does not carry, an orchestrator's resume this service recorded no pause for (LM1, LM2, LM24, D3189) |

`identity_refused` is one code because the CLI's own branch is the same for all of them — fail
with the reason and the fix — and because a code a caller may act on differently would have to be
a decision.

`not_served` is **not** the service's existing `refuseNotServed` (which writes
`unverified_job_identity` and refuses a route a workspace scope does not serve); it is a code of
this contract's own (`F27`). It is answered with **503** and deliberately **not** with the 404 a
path the service does not know is answered with, because R7 keys the bridge on three things and
those two must stay told apart.

## The bridge's three triggers (R7)

The CLI of this release asks the issuer, and where the service does not serve the route it signs
locally as before — which works exactly as long as the people's row is still there. The bridge
keys on **three things only**:

1. the contract's **`not_served`** answer, which the service also gives while it has no signer
   configured;
2. the answer a **release-behind service** gives a path it does not know — the **404** (or 405)
   that `internal/cli`'s `slipRouteAbsent` already reads as an absent route (`F27`);
3. **no service configured**.

It is no downgrade: local signing needs the row, and once LM14 has removed it the bridge has
nothing to sign with; there the launch refuses with the fix (run `fugaro update`), not with IAM's
error. `TestUnknownPathAnswerIsPinned` pins trigger 2 hermetically: the service of this release
serves the five paths by POST alone, so the same paths asked with another method are answered the
one 404 a path nobody knows is answered with, byte for byte.

The CLI branches on these codes for one thing only, and R7 says it is the whole of what it may
branch on: `not_served` means sign locally. Every other code is a refusal with the fix, and none
of them may be read as a bridge.

## What of the slip the routes reuse (F1)

The slip (`internal/credsvc/slip.go`) is the same mechanism in small. The identity routes reuse
four things of it rather than inventing them:

- the **roster class**: the caller of the ticket, the pairing and the probe is a verifier's Google
  ID token on the roster, read from the `Authorization` bearer and verified by `VerifyLauncher`;
- the **once-only fence**: a record is claimed once and the second claim is refused (`Claim`,
  `ErrClaimed`, R5);
- a **stored hash of a secret**: the ticket is kept as its digest and never in the clear
  (`Record.TicketHash`), and so is the claim's nonce (`Claim.NonceHash`), exactly as the slip
  keeps `creds.ClaimSecretHash`;
- the **bridge**: a CLI of this release signs locally where the service does not serve the route.

Three things are not the slip's and are this contract's own: the ticket is presented **once** and
then dies into a signed token, where the slip's claim secret is presented on every later call; the
record holds the claims the service will sign, because the identity is signed long before the run
can use it (R3); and the claim is answered ahead of the shared parse (R2).

## The ticket's execution variable (R7)

`identitywire.TicketEnv` is `FUGARO_IDENTITY_TICKET`: the launcher sets it on the execution it
starts (LM6) and the runner reads its claim out of it (LM7). It is a new name and not
`runner.BudgetTokenEnv`'s, because that one keeps meaning "the seal key" until the release after
this one deletes it (LM18): the two markers stand for two different mechanisms, and a runner of
this release holds both — it claims where it finds a ticket and opens the sealed object where it
finds the old marker.

## The issuance record (R3)

The issuer writes, **once per slug and run**, when it issues a ticket, and reads it again before
it signs:

| Field | What it is |
| --- | --- |
| `caller`, `class` | the verified address and class of who asked |
| `at` | when the record was written |
| `claims` | the claims the service will sign (`token.Claims`, the signer's own type) |
| `expected_account` | the job account the claim must come from (R4, F12) |
| `ticket_hash` | the digest of the ticket (`Digest`) |
| `claimed` | `account`, `at` and the digest of the nonce the claim carried (`nonce_hash`) |
| `attempt` | the reference an orchestrator's ask carried (D3136), empty for every other class |

The ticket itself is never stored, and neither is the nonce: both are kept as their digests alone.
The slug and the run are the record's **path** and not fields of it, exactly as a slip's id is its
path. `identitywire.PathRecord(slug, run)` is the place:

```
creds/issuance/<escaped slug>/<escaped run>
```

under the database's `creds` subtree, which the rules already close to every signed-in identity
(F26, R3), so this contract adds no rule — LM5 pins the path against the generator and generates
nothing. Both names are escaped as every other database key of the installation is
(`budget.Key`), so that two pairs cannot collapse onto one node; an empty segment is a panic and
not a path, because neither ever arrives from a verified ask.

`Digest` is the SHA-256 of a value, lower-hex, with no salt and no stretching — the right choice
for exactly this input and the slip's own: the ticket is drawn from `crypto/rand`, so there is no
dictionary to run and nothing a work factor would slow down.

`Record.Claims` is the signer's own `token.Claims` and not a second spelling of the claim set:
one type means the record cannot hold a set the signer would refuse, and the wire names a token
ends up carrying (`fs`, `fr`, `fx`, `fp`, `rb`, `fb`, `fpr`, `fcr`) are built from it at the
signing, by `token`'s own `claimSet`.

## The service's configuration and the stub (C1's mount)

`identitywire` declares the contract; `internal/credsvc` serves it. `IdentityConfig` is the
service's identity configuration: the signer, the roster's service accounts and operators, the
implicit admins, the maximum runtime, the issuance store (`IssuanceStore`) and the launch-scope
check (`ScopeCheck`). Its **zero value is an installation that has not been given an issuer yet**,
and that is a state of its own and not an error: from this release on the five routes are mounted
in every service, and one with no signer answers `not_served`.

Until I1 wires the real handlers, the five routes are served by `identity_stub.go`, which reads
nothing and answers every path `not_served` (and writes no audit line, since no caller was
verified). I1 replaces that file with the mount. The symbols other tasks build against — the
interfaces, `StubIdentityStore` and `StubIdentityScope` — live in `identity_config.go`, which I1
does not delete. Both stubs decide nothing: the store refuses every call and the scope check is
passed explicitly, so nothing in the service silently falls back to an admitting default.

The two mount points are one line each in `internal/credsvc/server.go` and
`internal/credsvc/scope_installation.go`: the five paths are added to `servedRoute`'s POST-only
list, to the front door's 404 sentence (which `TestServedRoutesCensus` holds equal to the list of
served routes), and to `aheadOfTheSharedParse`, so that a caller holding no run identity is
resolved early rather than refused by the shared parse.
