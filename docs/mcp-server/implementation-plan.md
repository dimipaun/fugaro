# The hosted MCP server: implementation plan

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** The design is [spec.md](spec.md), the findings and the fifteen decisions are
[reconciliation.md](reconciliation.md). **All fifteen were answered on 2026-11-04** — with the owner's
own detail written into M3, M5, M6 and M9 — and this plan is written to those answers: the "Answered"
column below is what the tasks build, and the last column is what a later answer the other way would
move. **The design merged as #1487 before its mandatory security review ran; the review ran on
2026-11-04, the owner answered it the same day, and this plan is amended to those answers** — the
amendments are [reconciliation.md](reconciliation.md) §4a, the two findings that are decisions of
their own are **D935** and **D936** (§4b), and §1 below records each one against the decision it
names. **No task here is approved.**

**Two gates stand before the first line of any task.** The **independent SECURITY review of
[spec.md](spec.md) §8** — **run by the coordinator**, per the owner's brief, because this folder's
authors cannot review their own threat model — **has run** (2026-11-04): the gate from here on is
per task, and **a task starts only when the finding that names it is answered in the task it
builds**, which is what §3 says of each. Its scope was §8, every **SEC**-marked task below (the four
whose marker says **mandatory** are **M3, M4, M5 and M7**, with **M9 keeping the marker in the
deferred slice** and M2, M6, M8 and M10 carrying the marker without that word) and the one new
privilege this design asks for (M9's launch grant, **also deferred**). **D921's licence** is the
second, and it is smaller than it was: the review **confirmed Apache-2.0**, so what remains is
**counsel's clearance of the transitive dependencies** and **the M5 gate file the owner is to
create** ([reconciliation.md](reconciliation.md) §6, the first owner check). It still gates **M5**
specifically, because M5 is the task that takes the dependency, and a licence counsel refuses sends
D921 back to its alternatives before any code exists rather than after.

## 1. What the owner answered on 2026-11-04, and what each answer moves

| D | The question | Answered 2026-11-04, and written to | If the owner later answers the other way |
| --- | --- | --- | --- |
| D920 | Public endpoint, or P8's IAM invoker | **Public**, bearer token is the gate | **M5, M6, M8, M9, M10 are void, and M4 and M7 shrink.** An IAM-invoked endpoint has no metadata documents, no registration and no token endpoint; what survives is M1–M3 and a tool registry reached by a Google identity. The plan is then a different, much smaller plan. |
| D921 | The OAuth library | **`zitadel/oidc`**, whose licence the owner or counsel confirms before any build: **M5 does not start until that is settled** | **M5's shape changes** (the endpoints stay; who implements them does not), and `go.mod` gains a different dependency. |
| D922 | Where the token records live | **The separate `mcpauth` subtree, `.read: false` and `.write: false`, written by the coordinator's own account only** (**restated the same day**: the first answer made it readable by any signed-in person) | **M3 changes** by taking `coordinator/`'s `.read: "USER"` for the new node, and its red step inverts from the denial to the admission. |
| D923 | How the person signs in | **The installation's own custom-token flow, with a PAIRED DEVICE approving and the coordinator never minting a token** — mechanism named; the ID token is consumed at `/oauth/authorize` only, and only exact `user:` list entries are read | **M6 changes** (the verifier moves to the approve step), **M7 gains the `user:`-only rule**, and a decision already taken once (`docs/mobile-auth` D221) is reopened. |
| D924 | How the spend limit is enforced | **A variable of its own, tighter-of-two, counted across the whole token family** | **M9 loses its core change** and gains the launcher-side arithmetic instead, and **M2 gains the family counter** that makes rotation a lifetime mechanism rather than a budget one. |
| D925, D926 | Hotfix as a flag; the window's shape | **Flag beside the window; an empty window refused at mint** | **M1 changes**, and M9's refusal message changes. |
| D927 | The v1 scopes | **`tasks:read`, `tasks:write` only** | **M7 and M10 gain a scope**, and the console's blocked write (F30) gains a route. |
| D928 | Who owns the redirect allowlist | **The project's configuration**, with registration refusing a non-loopback URI off the allowlist, the consent page naming the redirect host, and the flow CSRF-bound — tightened | **M5 and M9 gain a configuration field** and the project-format version question that comes with it, and M5's registration, consent and authorize tests go with them. |
| D929 | What an unattended caller is | **A pre-registered confidential client** | **M5 and M9 gain a key-exchange endpoint**, and the owner's own non-goal is reopened. |
| D930 | Lifetimes and rotation | **Short-lived access tokens, rotating refresh, one-delete revocation** | **M2 and M10 change**; a long-lived-token design deletes most of M2. |
| D931 | How a run is started, and the coordinator's own tools | **`internal/backend`, one new grant, both tool sets on one endpoint, and a single launch path**: the tool is `spawn_task`, and concurrency, rate, idempotency and the kill switch all go through `fugaro run`'s own pre-launch code — tightened | **M9 changes** (the sweep instead of a direct launch), and the tool-naming decision moves into M7. |
| D932 | Where `stream_logs` reads | **The run's own artefacts** | **M8 and M4 change**: one more grant, able to read every log line in the project. |
| D933 | Who a person is | **The project's launcher and operator lists**, read for exact `user:` entries only | **M7 and M10 change**, and a permission model that does not exist yet has to be designed. |
| D934 | Whether the front door is on by default | **OFF until the owner turns it on** | **M4 gains the switch** and the live checklist gains a state; the other reading puts a public write endpoint on installations that asked only for a read-only view. |

### The amendment of 2026-11-04, decision by decision

**Every answer above stands; the note below is what the review moved beside it, and the note is
dated because the answer is not.** The full note for each decision, with the code it cites, is
[reconciliation.md](reconciliation.md) §4a beside that decision; what follows is which task carries
it:

- **D920 — M-e.** A public service is public **per route, not per feature**: every tool maps to a
  scope and an unmapped tool is refused (M7), `max-instances` and a request-body cap are set (M4),
  and an unauthenticated refusal is logged and **never written to the database** (M7).
- **D921 — M-c.** The library's defaults contradict the design (PKCE optional, an empty challenge
  method meaning `plain`, unknown scopes silently dropped, extra grants, a signing key with no home,
  discovery at `openid-configuration` rather than RFC 8414's path). M5 refuses a missing or
  non-`S256` challenge and an unknown scope **before the library sees them**, allows only the code
  and refresh grants, keeps its signing material in Secret Manager or uses opaque tokens, and serves
  both metadata paths from one table. **Licence: Apache-2.0, confirmed by the review**; counsel
  still clears the transitive dependencies, and the **M5 gate file is the owner's to create**.
- **D922 — H3.** Keyed hashes and MACs, no database admin on the public service, and the records
  outside the main database (**D936**). M2, M3 and M4 carry it.
- **D923 — H1 and M-a.** **H1 is the one amendment that is not the recommendation**: a device token
  is a custom token any launcher can mint, so the approval proves nothing against a launcher, and the
  owner **accepted and documented** the residual risk instead of closing it — **it is in
  [spec.md](spec.md) §3 and §8 and it is built by no task here**. M-a's hardening is built: M5 and M6
  require QR proximity or number matching, show the client marked unverified and the redirect host,
  approve by a **single-use POST**, and never put an ID token in a GET URL.
- **D924 — M-b.** The family spend limit is enforced across concurrent runs by reserving
  `min(job cap, remaining)` **atomically against the family counter** at spawn and settling at
  finalize; for `client_credentials` the family is the client. The store half is M2's, the call site
  is M9's — **and M9 is deferred, so until it lands nothing calls it**.
- **D929 — M-f.** An unattended client is not a `user:` entry, so D933 refuses it, and **no task owned
  pre-registration**: `client_credentials` stays **off until it has an owner**, and pre-registration
  is **refused inside a coding agent's session**. M5 and M7 carry the refusal; **neither builds the
  grant**.
- **D930 — M-d.** Rotation is a check-then-write race: M2 conditions it on **the ETag of the same
  read** with a small grace window, and a replay outside the window still revokes the family.
- **D931 — H2 — see D935.** The original answer is the **shape `spawn_task` must take when it is
  built**; the finding is that it must not be built in the first slice.
- **D932 — M-g.** A recorded transcript can hold a credential the agent printed, so the read tools
  serve the progress record **redacted through `internal/safetext`** (M8).
- **D933 — M-f.** The lists are read from **a source only an operator can write** — the
  installation's own published state, not one machine's local config (M7).
- **D934 — M-e.** **"Off" is the absence of the `allUsers` binding**, and the installer's own cover
  rule refuses `allUsers`, so M4's stage asks its **own typed confirmation** and **M4 must not widen
  `tf.Cover`**.
- **D925, D926, D927 and D928** were not touched by the review.

### The two decisions the amendment added

Both were taken on 2026-11-04, out of this folder's block: **D935** and **D936**, whose full
question-and-answer shapes are [reconciliation.md](reconciliation.md) §4b.

| D | The question | The answer, 2026-11-04 | What it moves here |
| --- | --- | --- | --- |
| **D935** | Is `spawn_task` in the first MCP slice? | **No — deferred out of it.** The first slice is **the authorization server plus the read tools**; `spawn_task` (and `cancel_task`, which writes the runs bucket's marker) is its own **later design item** | **M9 leaves the first slice**, and the M10 rows that depend on it leave with it; §2's order, §4's table and §5's slot are re-cut |
| **D936** | Where do the token records live, now that IAM bypasses the rules? | **Outside the main database**: a named store under an **IAM condition** the coordinator's service account alone satisfies, with the main database keeping only the named-and-denied `mcpauth` node as a **tripwire**; the public service holds **viewer, not admin** | **M2** builds the store, **M3**'s node becomes a tripwire, and **M4** creates the store and the pepper's secret |

**Why `spawn_task` is deferred, in one paragraph.** It needs **three privileges, not one**:
`signJwt` to mint the run's custom token; **write to the runs bucket**, which the coordinator does not
hold (F3 is `roles/storage.objectViewer`); and **`run.jobs.runWithOverrides`**, which can override
**container args** — the base image's entrypoint is `tini --` (`images/base_test.go:59`), so an
override can run **any command beside the job's mounted git and model credentials**. That contradicts
the answer's own "the coordinator mints nothing" (D923) and M6's
`TestTheCoordinatorMintsNoCustomToken`. The first slice keeps that sentence true by not needing
`spawn_task` at all. **What a later `spawn_task` design item has to answer** is written in
[reconciliation.md](reconciliation.md) §4b and in §7 below; the four requirements are: no
`runWithOverrides` (use `run.jobs.run` with per-run input under a bucket prefix only the coordinator
writes); run-token minting moved out of the public service; an argument allowlist (no
`--no-budget-check` at `internal/cli/run.go:301`, no `--token` at `internal/cli/run.go:292`, no
free-form `--run-id`); and `mode = max(requested, the token's minimum)` always.

## 2. The order, and what forces it

```
first slice (D935):

M1 ──▶ M2 ──▶ M5 ──▶ M7 ──▶ M8
        │      ▲      │
M3 ─────┘      │      └──▶ M10 (the read half)
               │
M4 ────────────┘        (M4 and M6 are the two that need a live installation)
M6 ────────────┘

deferred slice (D935):

M7 ──▶ M9 ──▶ M10 (its spawn half)
```

Three dependencies force that shape and nothing else does:

1. **M4 before M5.** The endpoints have nowhere to run until the coordinator has a service and an
   account of its own. M4 is the only task that needs Terraform and the only one that cannot be
   proven hermetically beyond a plan test.
2. **M2 before M5.** The token endpoint's whole job is to mint a record M2 defines, and the record's
   store is the one D936 moved out of the main database.
3. **M1 before everything that reads a limit**, because "only narrows" is the one property every
   other task assumes.

**M3 is independent** of all of them and can land first: it is a rules file and its tests, in a
package that has nothing to do with the coordinator.

**The deferred slice is one task plus one half-task, and it is cut on purpose.** M9 leaves, and with
it the parts of M10 that name a spawned or cancelled run; nothing in the first slice starts a run,
holds the launch grant or changes `internal/runner`. That is D935's whole point: the first slice
**answers strangers and reads** — the authorization server, the tool registry and the four read tools
— and the sentence "the coordinator mints nothing" stays true while it does.

## 3. The tasks

Every task is written to be mergeable on its own. **SEC** marks a task whose diff an independent
review must read before it merges, which the brief requires for the threat model as a whole and which
is asked here for every task that changes who may reach what. **The review has run**; the line each
task carries under **SEC** now says what its own findings were and that they are answered here.

### M1 — The limits vocabulary, and "only narrows"

- **Files (6):** `ee/coordinator/mcpauth/limits.go`, `limits_test.go`, `ee/coordinator/mcpauth/token.go`,
  `token_test.go`, `ee/coordinator/mcpauth/limits_table_test.go`, `ee/coordinator/mcpauth/doc.go`.
- **Interface:** `Limits{MinRigor, MaxRigor rigor.Mode; Hotfix bool; SpendMicroUSD *int64; Scopes []string}`
  and `func (l Limits) Narrow(project Limits) (Limits, error)`. `Narrow` takes the tighter side of
  every field and returns an error naming the field when the result admits nothing. **The spend limit
  is a pointer to an integer in micros and not a `float64`** (the review's low finding): "no limit"
  and "a limit of zero" have to be different values, because the difference is the whole of D924's
  "only narrows" — and a limit of zero, or a negative one, is **refused at mint** rather than minted
  as a token that permits nothing. An unknown rigor-mode string is **refused** rather than read as
  the loosest mode.
- **Red step:** `TestLimitsOnlyNarrow` — a token asking for `critical` under a project policy of
  `pro` resolves to `pro`, and a token whose spend exceeds the run's ceiling resolves to the
  ceiling. It fails to compile before `Narrow` exists, which is the red.
- **Tests:** `TestLimitsOnlyNarrow`, `TestUnsetSpendIsNotZeroSpend` (a token with no spend under a
  project limit of N resolves to N, and a token with `0` is refused), `TestHotfixIsNotOnTheAxis` (a
  `min: pro` token refuses Hotfix with the flag off, and a Hotfix token passes a `min: poc` policy —
  the `Rank()`-0 case of D925), `TestEmptyWindowIsRefusedAtMint`,
  `TestUnknownModeStringIsRefusedNotIgnored`, `TestUnknownScopeIsRefusedNotIgnored`.
- **SEC:** no grant changes; reviewed with the threat model because it is the rule the whole folder
  rests on. **The review's finding was the `SpendUSD` type, and it is answered above**; nothing else
  was asked of this task.
- **Depends on:** nothing. **May continue — started on 2026-11-04**, and it is the one task the
  review's low finding changed.

### M2 — Minting and the store, outside the main database

- **Files (8):** `ee/coordinator/mcpauth/store.go`, `store_test.go`, `mint.go`, `mint_test.go`,
  `ee/coordinator/mcpauth/store_named.go`, `store_named_test.go`,
  `ee/coordinator/mcpauth/fake_test.go`, `ee/coordinator/testdata/mcpauth/README.md`.
- **Interface:** `Store` over the **named store D936 puts outside the main database** with
  `PutClient`, `GetClient`, `PutToken`, `LookupByHash`, `RevokeFamily`, and — for M9, which is
  deferred — `ReserveFamily(family string, micros int64) (Lease, error)` and `SettleFamily(Lease)`.
  `Mint(identity, client, limits) (access, refresh string, err error)`. **H3's four parts are this
  task's core:** every record stores an **HMAC-keyed hash** of the token under a **pepper held in
  Secret Manager**; **each record's identity, limits and revocation state are MAC'd**; the store is
  reached under an **IAM condition** that admits the coordinator's service account and nothing else;
  and **nothing here writes to the main database at all** (§8's token storage is written to this).
- **Red step:** `TestNoRecordHoldsTheTokenOrItsHash` — every value written through the fake store is
  compared against the two strings `Mint` returned **and against their unkeyed SHA-256**, and the
  test fails if any record contains either. Written first, it fails because there is no `Mint`.
- **Tests:** `TestNoRecordHoldsTheTokenOrItsHash`, `TestLookupFindsByThePresentedToken`,
  `TestARecordWithABrokenMACIsRefused` (a record written by an account that is not the coordinator —
  which IAM lets several of them do — is **refused rather than honoured**, which is what makes the
  MAC worth carrying), `TestRotateIsConditionalOnTheETag` (two rotations from one read: exactly one
  wins, because the write is conditioned on **the ETag of the same read**,
  `internal/rtdb/client.go:178`), `TestTheLoserOfARotationRaceGetsTheSamePair` (M-d's grace window:
  a client retrying on a slow link is not logged out by its own retry),
  `TestReplayOutsideTheWindowRevokesTheFamily`, `TestRevokedTokenIsNotFound`,
  `TestReserveIsAtomicAgainstTheFamilyCounter` (M-b: two concurrent spawns of a family whose
  remaining headroom is one job's cap admit **one**, and the loser is refused rather than
  over-spending).
- **SEC:** **yes** — it is the task that decides what is at rest, it is the first file that holds the
  pepper, and it is where H3 and D936 land. **The review's findings were H3, M-b and M-d; all three
  are answered here**, and the store's *location* is the one part of this task the owner may still
  have to change (§6, checks 8 and the UNVERIFIED halves).
- **Depends on:** M1, **and on D936's two UNVERIFIED halves** — whether an IAM condition on a named
  store can express exactly "this service account and no other", and whether `fugaro update` can
  create the store idempotently on an installation that already has the default database. **Settling
  those two is this task's first step**, and the fallback is the store it finds simplest that keeps
  the records out of the main database and out of every viewer's reach
  ([reconciliation.md](reconciliation.md) §6, check 8).
- **Carried but uncalled:** the reserve and settle pair is M9's, and **M9 is deferred** — this task
  lands it with its test and **no production caller**, which is the honest cost of cutting the slice
  where D935 cuts it.

### M3 — The rules name the token records' former home, and hold nothing

- **Files (3):** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`,
  `internal/budget/rules/mcpauth_test.go`.
- **Interface:** a top-level `mcpauth` subtree in the template — **a sibling of `coordinator/`, not a
  child of it** (D922's answer) — with **`.read: false` and `.write: false`**, named in the file so
  that the next person who adds a grant can see it is there, plus the constant the parity test reads
  it through. It deliberately does **not** copy `coordinator/`'s `.read: "USER"`
  (`internal/budget/rules/rules.json.tmpl:10-13`, the claim at `internal/budget/rules/rules.go:517`):
  the node is refused to every client, and **H3 showed that a closed rule is not a closed node** —
  `roles/firebasedatabase.viewer` is an IAM read every launcher and operator holds
  (`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`) and `roles/firebasedatabase.admin` a write
  several accounts hold (`:53-58`, `:81-84`). So the node **holds nothing**: the records live in the
  store **D936** moves outside the database, the public service holds **viewer and no admin**
  (`:138-142` is the grant the amended answer refuses), and what the rule and the name are for is to
  make the *next* record that lands here a **failing test rather than a quiet widening**.
- **Red step:** `TestMCPAuthIsNamedAndDeniedToEveryClient` — it asserts the subtree is **named** in
  the template, and that the emulator refuses a **read** as `USER` and as a run's `TOKEN` and refuses
  a **write** as `USER` and as `TOKEN`. It fails before the template change because the key is
  absent. (The root's blanket denial already refuses the `TOKEN` read, which is exactly why the test
  has to assert the *name*: an unnamed subtree is protected by a rule nobody reading the file can
  see. It asserts the `USER` **refusal** too, because that is the half D922's restatement decides —
  a later change that gave `mcpauth` the `coordinator` node's read rule would be a decision, and this
  test is what makes it a failing one rather than a quiet widening.)
- **Tests:** `TestMCPAuthIsNamedAndDeniedToEveryClient`, and `TestRulesParity` must stay green.
- **SEC:** **yes, mandatory** — and the review ran against it: **its finding was H3**, the answer is
  the tripwire above plus D936, and nothing in this task's diff changes because of it. Its question
  was the reverse of the one an open read rule would raise — not what the exposure accepts, but
  whether anything but the coordinator's account can reach a node the rules refuse — and **the answer
  turned out to be that IAM reads and writes reach straight past the rule**, which is why the records
  moved rather than the rule tightening further.
- **Depends on:** nothing. **Can land first. May continue — started on 2026-11-04**, unchanged.
- **Do not touch:** `internal/budget/rules/rules.go` is shared with every other rules work; the
  change is one constant and one template key, and a conflict there is a rebase, not a redesign.

### M4 — The coordinator's public service, its own account and its grants

**Cancelled on 2026-11-04 and to be rerun from this amended plan**: its draft predates the D934 and
M-e answers — the meaning of "off" and the collision with the installer's own cover rule — and the
D936 store it now has to create.

- **Files (7):** `deploy/terraform/gcp/modules/installation/coordinator.tf`,
  `deploy/terraform/gcp/modules/installation/outputs.tf`,
  `deploy/terraform/gcp/modules/installation/coordinator_service_test.go` (a plan-level test),
  `internal/infra/coordinator_service.go`, `internal/infra/coordinator_service_test.go`,
  `internal/infra/tf/cover.go` (**read, and deliberately not changed** — see below),
  `ee/coordinator/service.go`.
- **Interface:** a `google_cloud_run_v2_service` for the coordinator with its own service account, no
  model credential mounted, a **capped `max-instances`** and a **request-body cap**, and the launch
  role of M9 declared only when M9 is built. **The public invoker binding is not a setting the
  handler reads: "off" is the absence of the `allUsers` binding** (D934's amendment), and turning the
  front door on is what adds it. The account holds **`roles/firebasedatabase.viewer`** for M2's
  pre-check and **no `firebasedatabase.admin`** (H3). The same apply creates **D936's store** — the
  named store, the IAM condition that admits the coordinator's account and nothing else, and the
  **pepper's secret**, which is Secret Manager's and not a variable anyone can read.
- **Red step:** `TestFrontDoorOffIsTheAbsenceOfTheBinding` — the same apply without the flag produces
  **no `allUsers` binding anywhere**, and with the flag produces exactly one, on the coordinator's
  service and on nothing else. It fails against the current Terraform because there is no service.
- **Tests:** `TestFrontDoorOffIsTheAbsenceOfTheBinding`,
  `TestCoordinatorServiceCarriesNoCredential` (no secret whose value is a model credential),
  `TestCoordinatorHasItsOwnAccount`, `TestCoordinatorHoldsNoDatabaseAdmin` (the account's grants
  contain viewer and not admin, which is H3's second half),
  `TestTheCoverRefusesAllUsersAndAsksForItsOwnConfirmation` (**`internal/infra/tf/cover.go:867`
  refuses `allUsers`, so a plan carrying the binding is not covered and its stage asks its own typed
  confirmation at `:865`** — the test expects the confirmation, and **the task must not widen
  `tf.Cover` to make its own plan covered**), and the convergence test an installation-level
  migration requires: an installation at the previous version, updated with the flag, ends with
  exactly the resources above, and one that did not pass it ends with none of them.
- **SEC:** **yes, mandatory.** It creates a public endpoint, a new service account and a new store.
  **The review's findings were H3, M-e and D934's cover collision; all three are answered above** —
  the reviewer's question for this task is no longer "is the handler gated" but "is the binding
  absent when it is off, and is anything but the coordinator's account able to read the store".
- **Depends on:** D920 (**answered**), D934's amendment and H3; and M2's first step, which settles
  D936's two UNVERIFIED halves before this task writes Terraform for the store.
- **Owner check:** the service is applied only by `fugaro update`, and this plan does not run it.

#### For operators

The front door is **OFF on every installation until the owner turns it on**, and turning it on is
`fugaro update` — not a second command, because the repository's rule is that one command is the
whole upgrade. The flag is update's own, and the release notes' `### For operators` section carries
the one line that says so, in the release that ships M4:

```sh
fugaro update --firebase-project <firebase-project-id> --mcp-server
```

That run creates, in the installation's GCP project:

- the coordinator's Cloud Run **service** — the resident service D196 decided and the coordinator's
  own P7 builds, which is not the aggregation **job** P5 adds;
- its own service account, and **the public invoker binding D920 asks for — which is the switch**:
  an installation that did not pass the flag has no binding, and no handler-level default brings it
  back (D934's amendment);
- **D936's store**: the named store the token records live in, the IAM condition that admits the
  coordinator's account and nothing else, and **the pepper's secret in Secret Manager** — the
  records are HMAC-keyed hashes under it, so the store is unreadable without it and the service
  holds **viewer, not admin** on the main database (H3);
- the launch grant on the project's own job (M9), **only when M9 is built**, and it is **not** in the
  first slice (D935);
- the front door's own configuration: the redirect allowlist of D928.

That apply carries the `allUsers` binding, and **the installer's own cover rule refuses `allUsers`**
(`internal/infra/tf/cover.go:867`), so **the update asks its own typed confirmation at that stage**
(`:865`) rather than applying it silently. That is a behaviour the owner sees and the release notes
must say so in one line: the command is still the only command, and it will ask.

An installation that opted into the coordinator for its read-only view acquires **none** of it: the
aggregation job and the front door are separate opt-ins, and a public write endpoint is not something
an upgrade should hand a project that asked for a dashboard. M4 carries the convergence test the
repository requires of a migration: an installation at the previous version, updated with the flag,
ends with exactly the resources above — and an installation that did not pass it ends with none.

### M5 — The authorization server: its two metadata documents and its endpoints

- **Files (9):** `ee/coordinator/mcpauth/server.go`, `server_test.go`, `metadata.go`,
  `metadata_test.go`, `register.go`, `register_test.go`, `authorize.go`, `token.go`,
  `authorize_test.go`.
- **Interface:** `/.well-known/oauth-protected-resource`, `/.well-known/oauth-authorization-server`,
  `POST /oauth/register`, `GET /oauth/authorize` (which redirects to the approval step, and carries
  **no token**), `POST /oauth/authorize/approve` (the single-use approval M-a requires),
  `POST /oauth/token`, `POST /oauth/revoke`, mounted on the service M4 creates. **M-c's five
  constraints are this task's core**, because the library's defaults are not the design's:
  1. **A missing `code_challenge`, or one whose method is not `S256`, is refused before the library
     sees the request** — the library treats an empty challenge method as `plain` and PKCE as
     optional, and D928's flow is neither.
  2. **An unknown scope is refused before the library sees it** — the library silently drops what it
     does not know, which would turn D927's "two scopes only" into a token that quietly carries
     neither.
  3. **Only the `authorization_code` and `refresh_token` grants are allowed** — the library also
     offers jwt-bearer, token-exchange, device-code and implicit, and every one of them is a way into
     a front door whose only sign-in is the paired device.
  4. **Its signing material lives in Secret Manager, or the tokens are opaque** — the library needs a
     `SigningKey` and the design has no home for one otherwise. Opaque tokens, looked up in M2's
     store by their keyed hash, are the smaller design and the one this plan prefers; the task picks
     one and says why in its own section.
  5. **Discovery is served at both paths** — the library's `/.well-known/openid-configuration` and
     RFC 8414's `/.well-known/oauth-authorization-server` — from **one table of endpoints**, with
     `TestTheTwoMetadataDocumentsAgree` asserting they name the same issuer and the same endpoints,
     so the difference is a route rather than a second truth.
  M-a and D928 are the rest of the interface: the approval is a **single-use POST** on a named
  endpoint, **an ID token never travels in a GET URL**, the page shows **the client's chosen name
  marked unverified** and **the redirect host**, and approval requires **QR proximity or number
  matching**. M-f's half is here too: **pre-registration is refused inside a coding agent's session**
  and **`client_credentials` is off** — this task builds the refusal and **not** the grant.
- **Red step:** `TestNoTokenIsA401NamingTheMetadataURL` — the MCP endpoint answers 401 with a
  `WWW-Authenticate` header whose resource-metadata URL resolves, and the test fails because the
  endpoint does not exist.
- **Tests:** `TestNoTokenIsA401NamingTheMetadataURL`, `TestMetadataPointsAtItsOwnServer`,
  `TestTheTwoMetadataDocumentsAgree`, `TestRegisterIsOpenAndGrantsNothing`,
  `TestAMissingChallengeIsRefusedBeforeTheLibrary`, `TestAPlainChallengeIsRefusedBeforeTheLibrary`,
  `TestAnUnknownScopeIsRefusedBeforeTheLibrary`, `TestOnlyCodeAndRefreshGrantsAreAllowed`,
  `TestCodeIsSingleUseAndBoundToTheRedirectURI`, `TestRedirectURIMustMatchExactly`,
  `TestRedirectURIMustBeLoopbackOrAllowlisted`, `TestRefreshTokenIsSingleUse`, and the three D928
  tightenings' own: `TestRegistrationRefusesANonLoopbackURI` (a non-loopback URI that is not on the
  allowlist is refused **at registration**, not at a person's sign-in),
  `TestConsentNamesTheRedirectHostAndTheClientNameIsUnverified` (the page a person consents on
  carries the redirect URI's host, and the client's own name is **marked unverified** — the review's
  low finding), `TestConsentIsCSRFBound` (a consent that does not carry the flow's own binding is
  refused, and the `state` returns on the redirect unaltered), plus
  `TestApprovalIsASingleUsePOSTWithNoTokenInTheURL` and `TestNothingIsPushedWithoutAPerson` (M-a).
- **SEC:** **yes, mandatory** — this is the endpoint the threat model's redirect-URI, CSRF and open-
  registration paragraphs are about. **The review's findings were M-a, M-c, M-f and the unverified
  client name; all four are answered above.**
- **Depends on:** M2, M4, D921, D928 — **and on D921's licence**: the review **confirmed Apache-2.0**
  and **counsel still clears the transitive dependencies**, with **the gate file the owner creates**
  ([reconciliation.md](reconciliation.md) §6, check 1). This is the one task the licence gates
  directly.

### M6 — Firebase at the approve step

**Cancelled on 2026-11-04 and to be rerun from this amended plan**: its draft predates M-a and H1,
and both change what this task must say and where its verifier is called from.

- **Files (4):** `ee/coordinator/mcpauth/firebase.go`, `firebase_test.go`,
  `ee/coordinator/mcpauth/firebase_keys.go`, `ee/coordinator/testdata/mcpauth/jwks.json`.
- **Interface:** `VerifyIDToken(ctx, raw string) (Identity, error)`, sharing the verification rules
  `internal/credsvc` already implements (issuer built from the project id, audience compared to the
  bare project id, keys from the public endpoint) and never echoing a token in a refusal. It is
  called from **the approve step of `POST /oauth/authorize` and from nowhere else** (D923's answer as
  M-a amended it): the ID token is the proof of the sign-in step, **it arrives in the POST's body
  rather than in a URL**, and the token endpoint has no path that accepts one. The task carries the
  other half of that answer too: **the coordinator mints no Firebase custom token and holds no signer
  role**, so there is no minting code here for a reviewer to find, and no way for a compromised
  coordinator to assert an identity the paired device did not approve.
- **H1 is documented here and built nowhere.** A device token is a custom token, and any launcher can
  mint one and sign in as an operator's device — **the review's recommendation was a device-held
  signing key, and the owner accepted and documented the residual risk instead**. So this verifier
  does **not** change: it does not require a live device node, it does not refuse `r~`/`p~` tokens,
  and it does not give a paired device a viewer role. The risk, its three consequences and the change
  that would close it are [spec.md](spec.md) §3 and §8 and
  [reconciliation.md](reconciliation.md) §4a, and **no task in this plan may quietly build a piece of
  it** — an implementer who wants to is asking the owner for a decision, not fixing a finding.
- **Red step:** `TestTokenFromAnotherProjectIsRefused` — a token whose audience is another project's
  id is refused, and the refusal names neither the token nor the audience. It fails because there is
  no verifier.
- **Tests:** `TestTokenFromAnotherProjectIsRefused`, `TestWrongIssuerIsRefused`,
  `TestExpiredTokenIsRefused`, `TestRefusalNeverEchoesTheToken`,
  `TestTheTokenEndpointRefusesAFirebaseIDToken` (the same token that is accepted at the approve step
  is refused where it is not a grant), `TestAnIDTokenInAQueryStringIsRefused` (M-a: the approve step
  reads the body and nothing else) and `TestTheCoordinatorMintsNoCustomToken` (no code path signs
  one — **and the amendment is what keeps this test meaningful**: `spawn_task` would have needed
  `signJwt`, which is exactly why it is out of the first slice).
- **SEC:** **yes** — token verification is where a mistake is silent. **The review's findings were
  M-a and H1; the first is built above and the second is documented rather than built.**
- **Depends on:** M4, M5. **Reuses rather than duplicates** `internal/credsvc`'s verification; the
  review must ask why it is a second implementation, and the answer has to be that `internal/credsvc`
  is a different service's package with a different configuration surface — if the reviewer
  disagrees, the right fix is to extract the verifier, and that is a finding for the owner rather
  than a refactor in this task.

### M7 — The resource server and the tool registry

- **Files (7):** `ee/coordinator/mcpauth/resource.go`, `resource_test.go`,
  `ee/coordinator/mcpauth/tools.go`, `tools_test.go`, `ee/coordinator/mcpauth/scopes.go`,
  `scopes_test.go`, `ee/coordinator/mcpauth/identity.go`.
- **Interface:** one handler that reads the bearer token, validates it (issuer, audience, expiry,
  revocation, scopes), resolves the identity through the project's launcher and operator lists
  (D933), and dispatches JSON-RPC; one registry that answers `tools/list` with **both** the MCP tools
  and the coordinator's own (D931). **M-e's half is this task's core:** **every tool maps to a scope
  and a tool with no mapping is refused** — an endpoint that is public because the service is public
  must not hand an unmapped tool to a token that happens to be valid, and P8's `report_intent` and
  `record_override` are the tools that makes concrete. **M-f's other half is here too:** D933's lists
  are read from **a source only an operator can write** — the installation's own published state,
  reachable through the coordinator's account — and **not** from one machine's local config
  (`internal/cli/user.go:32-34`), which is where they live today. **An unauthenticated refusal is
  logged and is not written to the database**: it is a stranger's request, and a public route that
  wrote one row per attempt would let anyone fill the store.
- **Red step:** `TestAToolWithNoScopeIsRefused` — a tool name absent from the scope map is refused
  rather than dispatched, and the test fails because there is no registry. (It also fails if a later
  tool is registered without a scope, which is the failure this task exists to make loud.)
- **Tests:** `TestAToolWithNoScopeIsRefused`, `TestUnknownScopeIsRefusedNotIgnored`,
  `TestIdentityOffTheListsIsRefused`, `TestTheListsComeFromTheInstallationsOwnState` (a list written
  into one machine's local config admits nobody — D933's amendment),
  `TestOnlyExactUserEntriesAreRead` (D923's answer: a list holding `group:`, `domain:` and
  `serviceAccount:` entries beside its `user:` ones admits the `user:` identity and none of the
  others — the same rule `internal/cli/user.go:266` applies at its own door),
  `TestToolsListNamesBothSetsOnce`, `TestAnExpiredTokenIsRefused`,
  `TestAnUnauthenticatedRefusalIsNotWrittenToTheDatabase` (M-e's last clause),
  `TestAToolAnswerCarriesNoCredential` (a table over every tool, asserting the answer's text
  contains none of the project's credential names).
- **SEC:** **yes, mandatory.** **The review's findings were M-e and M-f; both are answered above.**
- **Depends on:** M2, M5, D933.

### M8 — The read tools

- **Files (5):** `ee/coordinator/mcpauth/tool_status.go`, `tool_status_test.go`,
  `ee/coordinator/mcpauth/tool_logs.go`, `tool_logs_test.go`, `ee/coordinator/mcpauth/tool_list.go`.
- **Interface:** `get_task_status`, `stream_logs` (from the run's own recorded artefacts, D932),
  `get_result`, `list_tasks`, each reading through `internal/runstore` and the runs bucket prefix the
  project already owns. **M-g is this task's core:** the artefacts it reads are the run's own session
  transcript (`internal/runner/session.go:185`), and **a transcript can hold a credential the agent
  printed** — a log line, an echoed environment, a command it ran. So the front door serves **the
  progress record redacted through `internal/safetext`** (`internal/safetext/safetext.go:17`) and
  **never the raw file**, which is the difference between a tool that must not return a credential
  and the one tool that does.
- **Red step:** `TestListTasksNamesOnlyTheProjectsRuns` — a run belonging to another project is
  absent from the answer, and the test fails because the tool does not exist.
- **Tests:** `TestListTasksNamesOnlyTheProjectsRuns`, `TestStatusOfAnUnknownRunIsRefused`,
  `TestLogsComeFromTheRunsOwnArtefacts`,
  `TestLogsAreRedactedAndTheRawTranscriptIsNeverServed` (a marker credential the agent printed comes
  back escaped and never verbatim — M-g), `TestResultNeverQuotesAToken`.
- **SEC:** yes — read reach. **The review's finding was M-g, and it is answered above.**
- **Depends on:** M7.

### M9 — `spawn_task`, `cancel_task`, and the narrowed ceiling — **DEFERRED (D935)**

**Out of the first slice.** The first slice is **the authorization server plus the read tools**
(D935): nothing in it starts a run, holds the launch grant or changes `internal/runner`. This task is
kept whole here because it is still the plan's next piece of work, and it is marked deferred so no
planner picks it up with the others.

- **Files (8):** `ee/coordinator/mcpauth/tool_spawn.go`, `tool_spawn_test.go`,
  `ee/coordinator/mcpauth/tool_cancel.go`, `tool_cancel_test.go`,
  `ee/coordinator/mcpauth/launch.go`, `launch_test.go`, `internal/runner/spend.go`,
  `internal/runner/spend_test.go`.
- **Why it is deferred:** it needs **three privileges, not one** — `signJwt`, write on the runs
  bucket, and **`run.jobs.runWithOverrides`**, which can override container args and so run any
  command beside the job's credentials — and the third contradicts "the coordinator mints nothing"
  (D923). **`cancel_task` goes with it**: it writes the runs bucket's cancellation marker, which is
  the same write the coordinator does not hold. Full reasoning: [reconciliation.md](reconciliation.md)
  §4b, D935.
- **Interface, when it is built.** **`spawn_task`** (D931's answer — the owner's name for the
  `submit_task` of the spec §0) starts the project's own job through `internal/backend` with the
  token's spend limit in a variable of its own on the execution override (D924); the run's ceiling
  becomes the tighter of that and the job's own. **It starts it through one launch path and no
  other**, which is the whole of D931's second half: the same **launch claim** in the runs bucket
  (`internal/runstore/launch.go:112`, `ClaimKey`, `ClaimTTL` at `:117`) so one submit is one run and
  a repeat answers about the run in flight, the same **budget pre-check** of kill switches, caps and
  headroom (`internal/budget/precheck.go:58`, `budget.Precheck`, which
  `internal/cli/launch_budget.go:283` is the CLI's own call to), and the front door's own
  **concurrency and rate** limits enforced at that same point. `launch.go` is the one file that
  reaches the job, and a second route to `internal/backend` in any later task is a finding rather
  than a convenience. **Four things the deferral decided this task must do**, taken from D935:
  **no `runWithOverrides`** (use `run.jobs.run` with per-run input under a bucket prefix only the
  coordinator writes, keyed by the execution); **run-token minting moved out of the public service**;
  **an argument allowlist** — no `--no-budget-check` (`internal/cli/run.go:301`), no `--token`
  (`internal/cli/run.go:292`), no free-form `--run-id`; and **`mode = max(requested, the token's
  minimum)`** always. It also calls M2's `ReserveFamily`/`SettleFamily` for M-b, which is why those
  land in M2 ahead of it.
- **Red step:** `TestTokenCeilingOnlyNarrowsTheRunsOwn` in `internal/runner/spend_test.go` — a run
  whose environment carries a token ceiling **above** the job's own resolves to the job's own, and
  one below resolves to the token's. It fails before the variable exists.
- **Tests:** `TestTokenCeilingOnlyNarrowsTheRunsOwn`,
  `TestSpawnRefusesAModeOutsideTheTokensWindow`, `TestSpawnRefusesHotfixWithoutTheFlag`,
  `TestSpawnedRunCarriesTheTokenCeiling`, `TestSpawnNeverMountsACredential`,
  `TestSpawnTakesTheLaunchClaim` (a second submit of the same task answers about the run already in
  flight rather than starting a second), `TestSpawnRunsTheBudgetPrecheck` (a kill switch that refuses
  `fugaro run` refuses `spawn_task`), `TestSpawnReservesBeforeItLaunches` (M-b: the family counter is
  reserved atomically, and a refused reservation launches nothing),
  `TestCancelOfAnotherProjectsRunIsRefused`.
- **SEC:** **yes, mandatory** — it adds the launch grant, and it is the one task that spends money.
  **The review's findings were H2 and M-b; H2 is what defers it and M-b is what it will build.**
- **Depends on:** M2 (the store and its reserve pair), M4 (the grant), M7, D924.
- **Hand-off:** `internal/runner/spend.go` is a core file and **every release's** runner work has
  other writers on it — the 0.15.0 window that made this a hand-off has shipped, and the property it
  named has not gone away. This task belongs to a release where the file has no other writer, and its
  planner must say so before it starts (§4, §5). **The deferral makes that easier rather than
  harder**: the first slice touches no file under `internal/runner` at all.

### M10 — The audit record, revocation, and what an operator sees

- **Files (6):** `ee/coordinator/mcpauth/audit.go`, `audit_test.go`,
  `ee/coordinator/mcpauth/tool_revoke.go`, `tool_revoke_test.go`,
  `ee/coordinator/mcpauth/audit_record.go`, `audit_record_test.go`.
- **Interface:** one structured line per call and per refusal, naming the identity, the tool, the
  scopes and the outcome and never a value, on `internal/credsvc/audit.go`'s pattern; a bounded
  per-identity record an operator can read; and revocation reachable from the API and from a
  person's own surface. **The record lives in the store D936 moved out of the main database** — not
  in M3's node, which is a tripwire and holds nothing — and "an operator can read it" is the **tool
  surface's** rule alone (D933): an operator reads the record through a tool, a launcher does not
  read it at all, and no caller of any kind reaches the raw record
  ([spec.md](spec.md) §4, §6 and §8). **The review's low finding is here:** the record is bounded by
  design, so **a caller can flush it by spamming**, and the answer is that the **Cloud Logging line
  is the durable copy** and the per-identity record is **the readable view** — a caller who fills the
  view has not erased the evidence.
- **The spawn half is deferred.** The rows naming a spawned, refused or cancelled run are M9's tools
  and leave the first slice with it (D935); what lands here is the machinery, the read tools' own
  lines and the revocation surface.
- **Red step:** `TestEveryRefusalWritesOneLineNamingNoValue` — the audit line for a refusal whose
  token is a recognizable marker string contains neither the marker nor its first eight characters.
  It fails because there is no audit writer.
- **Tests:** `TestEveryRefusalWritesOneLineNamingNoValue`, `TestAuditRecordIsBounded`,
  `TestTheLoggingLineIsTheDurableCopy` (the line reaches the logging sink the record does not),
  `TestRevokedFamilyIsRefusedOnTheNextCall`,
  `TestOperatorSeesTheRecordAndALauncherDoesNot`.
- **SEC:** yes. **The review's finding was the low one about the bounded record, and it is answered
  above.**
- **Depends on:** M5, M7 (the first slice); its spawn half depends on M9, which is deferred.

## 4. The shared-files table

Files more than one task touches. A task that finds one of these already changed reads the change
before writing, and a planner who finds two tasks in the same row in one release serialises them.
**The deferred slice changes this table rather than adding to it**: M9 is in rows it shares with M2,
M4, M7 and M10, and a planner who takes the first slice alone takes none of them.

| File | Tasks | What each does to it |
| --- | --- | --- |
| `ee/coordinator/mcpauth/limits.go` | M1, M9 | M1 defines `Limits` and `Narrow` (with the spend in micros); M9 reads them at submit |
| `ee/coordinator/mcpauth/token.go` | M1, M2 | M1 defines the token's shape; M2 mints it |
| `ee/coordinator/mcpauth/store.go` | M2, M5, M10 | M2 defines the store; M5 writes through it; M10 revokes through it |
| `ee/coordinator/mcpauth/server.go` | M5, M6, M7 | M5 mounts the endpoints; M6 adds the approve step's verifier; M7 adds the resource handler |
| `ee/coordinator/mcpauth/authorize.go` | M5, M6 | M5 defines the authorize and approve steps; M6 verifies the ID token the approve step carries |
| `ee/coordinator/mcpauth/tools.go` | M7, M8, M10 — **and M9 when it is built** | the registry: each tool task registers into it, and each registration carries its scope (M-e) |
| `ee/coordinator/service.go` | M4, M5, M7 | M4 creates the service's entry point; M5 and M7 mount handlers on it |
| `deploy/terraform/gcp/modules/installation/coordinator.tf` | M2, M4, M5 — **and M9 when it is built** | M4 creates the service and the account; **M2's store, its IAM condition and the pepper's secret are created here too**; M5's signing material is a secret of the same apply; M9 adds the launch grant |
| `deploy/terraform/gcp/modules/installation/outputs.tf` | M4, M9 | the service's URL, then the grant |
| `internal/infra/tf/cover.go` | **no task — this row is a prohibition** | the cover rule refuses `allUsers` (`:867`) and a plan carrying it asks its own typed confirmation (`:865`); **M4 must not widen this file** to make its own plan covered |
| `internal/runner/spend.go` | M9 alone, but **shared with every other runner task in its release** | one variable and its merge; see §5 — which is why the release slot is chosen for the file's other writers, not only for this work. **Nothing in the first slice touches it** |
| `SECURITY.md` | M4, M9, M10 | a public endpoint, a launch grant, the store and the pepper, and what the audit does not record — **the repository's standing rule is that a security-relevant change updates it in the same PR** |

## 5. The release slot

**The implementation targets 0.21.0 "Rhino" or later.** The design's own window is long spent:
**0.15.0, 0.16.0, 0.17.0 and 0.18.0 have all shipped** since this folder was written, the CLI stream
is at `current: "0.18.0"` with **Panda** next (`docs/release-streams.yaml`), so 0.19.0 is Panda,
0.20.0 is Quokka and **0.21.0 is Rhino**. This is a re-slot, not a preference: the reasons the first
version of this section gave were right about *why* the work needs a window of its own and wrong only
about *which* — and the amendment moves it again, for a reason of its own.

Why the whole work is **0.21.0 or later**, and not the next minor:

1. **The coordinator's own front door is still not built.** P7 and P8 still wait on this design
   (`docs/coordinator/README.md`:26), so the service M4 creates and the account M5 runs on do not
   exist yet. A release cannot carry an MCP endpoint on a service the same release is still
   designing.
2. **The work is big and security-heavy, and the review has now proved it rather than argued it.**
   Ten tasks, **eight of them SEC-marked and four of those mandatory**, a public endpoint, a new
   service account, a **new store with its own IAM condition and a secret**, and — in the deferred
   slice — one new privilege (M9's launch grant). The independent review found **three high findings
   and seven more**, and the answers to them added a store, a pepper and a set of library
   constraints that did not exist when this plan was first written. A window that is not already
   carrying another feature of this shape is the only one it fits in whole.
3. **The update has to carry more than it did, and the update is gated.** The repository's standing
   rule is that a release ships no operator step beyond `fugaro update`, and the apply now creates
   **three** things the first plan did not have: the service, **D936's store** and **the pepper's
   secret** — plus a stage that asks the owner a **typed confirmation** because the binding the front
   door needs is one the installer's own cover rule refuses. Since this folder was written,
   **managed mode landed** and the gate covers `fugaro update` itself (`internal/cli/managed_gate.go`,
   `docs/managed-agent-converge/README.md`:20), so that heavier migration runs through the gate like
   every other managed step. It belongs in a release that is not already carrying the same work for
   three other features.
4. **M9 is a core change to a contention-prone file.** `internal/runner/spend.go` is core, and every
   runner task in whatever release carries M9 shares it (§4). Landing it in a full window means
   either a declared single writer is overridden or two changes are serialised by hand; neither is
   worth it for a feature whose first dependency is unbuilt.

**What the deferral buys, and what it does not.** **The first slice is smaller than the whole**: it
starts no run, asks for **no new privilege**, changes **nothing under `internal/runner`**, and adds
no grant beyond the store's own condition — so if P7 and P8 land, the first slice is a candidate for
**0.20.0 "Quokka"**, and M9 follows in a window of its own. That is a real option and this section
does not choose it for the owner: the slot above is the whole work's, and a planner who wants the
earlier window has to say in the release notes that **the front door is read-only until M9 ships**,
which is a sentence the owner should read before it is written.

**What would move it earlier:** if the owner answers D920 the other way (P8's IAM invoker), M5's
OAuth half disappears, and M1–M3 plus a small M7 fit into whatever release is next. Nothing else
here is small enough to pull forward.

## 6. Test rules for whoever implements this

- **Hermetic, always.** No live project, no real Firebase, no real Cloud Logging, **no real Secret
  Manager**: `internal/gcpfake`, `internal/testutil`, `t.TempDir()`. The rules tests use the
  package's own emulator harness, and **D936's store needs its own fake** — M2's first step is to
  show it is testable that way, because a store whose tests need a live project is a store this
  repository cannot merge. The one thing this plan cannot prove hermetically is the public
  reachability of M4's service, which is [reconciliation.md](reconciliation.md) §6's first owner
  check.
- **Named tests, never whole packages.** `internal/cli` and `internal/runner` are never run whole;
  every command is `GOFLAGS=-p=2 go test ./<package>/ -run 'Name|Name' -count=1`.
- **A test that reads a changed file runs.** For every file a task touches, the implementing run
  greps the test tree for its base name and runs each hit — the wider pin gate this repository
  applies to every change.
- **No secret-shaped literal, in a test or a fixture.** A token in a test is a marker string that
  cannot be mistaken for a real one, and no fixture contains key material — **the pepper included:
  the tests carry a marker pepper and never a value shaped like a real one.**
- **A citation added by the amendment is a citation to `main`.** Every path and line this folder
  cites is checked by `internal/cli`'s docs tests, and the amendment's new ones were verified at the
  commit the README names; a task that finds a line moved re-verifies it rather than moving the
  number.

## 7. What this plan does not own

- **The coordinator's P7 and P8.** D196 already put the front door's home in the coordinator's own
  service; this plan builds the MCP half of it and nothing else. P7's ID-token minting, its per-person
  invoker grants and its IAM checks are the coordinator work's, and M4 must not silently take them
  over — where they overlap, the coordinator's plan wins and this one adapts.
- **The later `spawn_task` design item.** D935 defers it out of the first slice, and §3's M9 keeps
  the shape and the four requirements the deferral decided; **the design of how a run is started from
  outside the CLI is not this plan's to write**, and it is not a task here until it is designed and
  the owner approves it. Anything that starts a run from the front door before then is a finding.
- **The console's graph write.** D927 keeps `roadmap:write` out of v1; GV3.1 and GV3.2 stay blocked
  and that work comes back to ask.
- **The central-orchestrator plan.** D196 answered that the coordinator's service is its own; the
  orchestrator reaches it as a client like any other, and this plan adds nothing to it.
- **The workspaces exchange.** `docs/workspaces` (a parallel run's amendment) requires a
  **member-bound assertion and an exchange at `/oauth/authorize` that keeps the paired-device
  approval**, so its exchange and M5's authorize step meet at one endpoint; **this plan does not own
  that decision and does not edit that folder** — [spec.md](spec.md) §10 states the dependency and
  the **identity mismatch** (a workspace identity is a verified email; this front door's is a device
  uid read back to an email), and it stays a dependency until the workspaces work lands.
- **The stateless-orchestrator spec** (open as **#1682**, not on `main` when this was written). Its
  **state tool group** is hosted here: D931's registry serves the coordinator's own tools on the
  same endpoint under their existing names, so the state group reaches MCP clients through this
  front door and this plan builds **no second surface** for it. Which names that group carries, and
  whether any of them needs a scope of its own, is that design's to decide — D927 keeps v1 to two
  scopes, and a tool the two do not cover brings the scope question back as D927's — and **M-e's rule
  applies to it unchanged: every one of those tools arrives with a scope, or it is refused**. M7 is
  the task the registry lives in, so it is the one that changes if the group's names arrive after
  this plan.
- **The managed gate's decisions.** The gate landed while this design was open
  (`internal/cli/managed_gate.go`, `docs/managed-agent-converge/`), and M4's operator path runs
  through it: `fugaro update` is one of the commands the gate covers, so the front door's switch is
  admitted or refused by the gate's own rules and this plan adds **no row** to it. A new command that
  the gate should cover is a managed-mode change, not this work's — and M4's `#### For operators`
  block already routes the switch through `fugaro update` rather than through a second command, which
  is the same rule the gate exists to enforce.
- **A local stdio MCP server.** The owner's non-goal, and this plan does not creep into it.
- **Any change to `SECURITY.md`, `README.md`, `website/**`, `plugin/**` or `.github/workflows/**`
  made by the run that writes *this* document** — those belong to other works, and the run that
  wrote this folder touched `docs/` and its own test and nothing else. The implementation tasks of §3
  may touch `SECURITY.md` when they run, because by then the change is theirs and the repository's
  standing rule is that a security-relevant change updates it in the same PR (§4).
