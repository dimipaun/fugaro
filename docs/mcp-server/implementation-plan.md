# The hosted MCP server: implementation plan

**Status: design answered by the owner 2026-11-04; M1, M2, M3, M6 and M4's first half are built and
merged on `main`; M5 is built in this pull request; the rest is not started; this folder approves no
task.** The design is
[spec.md](spec.md), the findings and the fifteen decisions are
[reconciliation.md](reconciliation.md). **All fifteen were answered on 2026-11-04** — **every one of
them the recommendation**, with the owner's own detail written into M3, M5, M6 and M9 — and this
plan is written to those answers: the "Answered" column below is what the tasks build, and the last
column is what a later answer the other way would move. **A sixteenth answer arrived on 2026-11-05
(D935), from the independent review of PR #1715's M3**: the `mcpauth` records — the client list, the
redirect URIs, the token hashes, the tool-call audit and the revocation state — **do not live in the
project's shared Realtime Database**, because every launcher and operator holds
`roles/firebasedatabase.viewer` on the whole project and the budget admins and the history account
hold `roles/firebasedatabase.admin`, so the rules cannot keep them out. They move to a store whose
IAM names only the front door's own account (`mcp_server`), and §1's last subsection records the
answer, M2, M3, M4, M5 and M10 are written to it, and §8 is the three candidates for that store —
**and the answer of the same day that picked the bucket**, which closes the Open item that section
carried: **nothing waits on it any more, and no task names a store it is not written to.** **This plan
approves no task, and two things gate the whole plan:** the
independent SECURITY review of [spec.md](spec.md) §8 (which the coordinator runs), and D921's
**unconfirmed licence** ([reconciliation.md](reconciliation.md) §6, checks 1 and 7). **The owner
confirmed the library's own licence on 2026-11-06** — `zitadel/oidc` is Apache 2.0, read from the
module cache the dependency was fetched into — **which closes the library half of that second gate;
the transitive dependencies' clearance and the M5 gate file are the halves it still holds.**

## 1. What the owner answered on 2026-11-04, and what each answer moves

| D | The question | Answered 2026-11-04, and written to | If the owner later answers the other way |
| --- | --- | --- | --- |
| D920 | Public endpoint, or P8's IAM invoker | **Public**, bearer token is the gate — **the recommendation** | **M5, M6, M8, M9, M10 are void, and M4 and M7 shrink.** An IAM-invoked endpoint has no metadata documents, no registration and no token endpoint; what survives is M1–M3 and a tool registry reached by a Google identity. The plan is then a different, much smaller plan. |
| D921 | The OAuth library | **`zitadel/oidc` — the recommendation, with the licence UNCONFIRMED**, so the owner or counsel confirms it before any build: **M5 does not start until that is settled**. **The owner confirmed the library's own licence on 2026-11-06 — Apache 2.0 — so M5 takes the dependency; the transitive set and the gate file of §6's first check are the owner's still** | **M5's shape changes** (the endpoints stay; who implements them does not), and `go.mod` gains a different dependency. |
| D922 | Where the token records live | **The separate `mcpauth` subtree, `.read: false` and `.write: false`, written by the coordinator's own account only** — the recommendation (**restated the same day**: the first answer made it readable by any signed-in person) | **M3 changes** by taking `coordinator/`'s `.read: "USER"` for the new node, and its red step inverts from the denial to the admission. |
| D923 | How the person signs in | **The installation's own custom-token flow, with a PAIRED DEVICE approving and the coordinator never minting a token** — the recommendation, mechanism named; the ID token is consumed at `/oauth/authorize` only, and only exact `user:` list entries are read | **M6 changes** (the verifier moves to the authorize endpoint), **M7 gains the `user:`-only rule**, and a decision already taken once (`docs/mobile-auth` D221) is reopened. |
| D924 | How the spend limit is enforced | **A variable of its own, tighter-of-two, counted across the whole token family** — the recommendation | **M9 loses its core change** and gains the launcher-side arithmetic instead, and **M2 gains the family counter** that makes rotation a lifetime mechanism rather than a budget one. |
| D925, D926 | Hotfix as a flag; the window's shape | **Flag beside the window; an empty window refused at mint** — the recommendation | **M1 changes**, and M9's refusal message changes. |
| D927 | The v1 scopes | **`tasks:read`, `tasks:write` only** — the recommendation | **M7 and M10 gain a scope**, and the console's blocked write (F30) gains a route. |
| D928 | Who owns the redirect allowlist | **The project's configuration**, with registration refusing a non-loopback URI off the allowlist, the consent page naming the redirect host, and the flow CSRF-bound — the recommendation, tightened | **M5 and M9 gain a configuration field** and the project-format version question that comes with it, and M5's registration, consent and authorize tests go with them. |
| D929 | What an unattended caller is | **A pre-registered confidential client** — the recommendation | **M5 and M9 gain a key-exchange endpoint**, and the owner's own non-goal is reopened. |
| D930 | Lifetimes and rotation | **Short-lived access tokens, rotating refresh, one-delete revocation** — the recommendation | **M2 and M10 change**; a long-lived-token design deletes most of M2. |
| D931 | How a run is started, and the coordinator's own tools | **`internal/backend`, one new grant, both tool sets on one endpoint, and a single launch path**: the tool is `spawn_task`, and concurrency, rate, idempotency and the kill switch all go through `fugaro run`'s own pre-launch code — the recommendation, tightened | **M9 changes** (the sweep instead of a direct launch), and the tool-naming decision moves into M7. |
| D932 | Where `stream_logs` reads | **The run's own artefacts** — the recommendation | **M8 and M4 change**: one more grant, able to read every log line in the project. |
| D933 | Who a person is | **The project's launcher and operator lists**, read for exact `user:` entries only — the recommendation | **M7 and M10 change**, and a permission model that does not exist yet has to be designed. |
| D934 | Whether the front door is on by default | **OFF until the owner turns it on** — the recommendation | **M4 gains the switch** and the live checklist gains a state; the other reading puts a public write endpoint on installations that asked only for a read-only view. |

### The answer of 2026-11-05: D935, and what it moves

**One question was answered after that day, and it moves where the records live rather than what they
are.** The independent review of PR #1715's M3 (the rules change) found that the closed `mcpauth` node
could not do the job D922 gave it, and the reason is a fact about the installation rather than about
the rule: the database grants are **project-level**, so every instance and every node in the project
inherits them and the rules do not bind their holders. `deploy/terraform/gcp/modules/firebase/iam.tf:44`
gives `roles/firebasedatabase.viewer` to every launcher and operator, `:53` gives
`roles/firebasedatabase.admin` to every admin and budget admin, and `:81` gives it to the history
account. `docs/design/roles.md:104` states the consequence in one line: **"[a]ny confidential thing
placed in RTDB is readable by everyone on either list."** So spec.md §8's "read by the coordinator's
account over IAM and by nobody else" does not hold in the project's database, whoever writes the
rules, and [reconciliation.md](reconciliation.md) §3 carries it as **F32**.

| D | The question | Answered 2026-11-05, and written to | If the owner later answers the other way |
| --- | --- | --- | --- |
| **D935** | Do the `mcpauth` records stay in the project's shared Realtime Database, behind the closed node D922 gave them? | **They move to a store of their own, whose IAM names the front door's own account (`mcp_server`) and no other member** — and the `mcpauth` node **stays in the rules as a defence in depth**, with `.read: false` and `.write: false`. **Which** store that is was left open when this row was first written and **was answered the same day: the bucket §8 recommends** (§8, the answered Open item) | **M2, M3, M4, M5 and M10 change**: M2 goes back to a database store, M3 becomes the only barrier rather than the second one, and M4 drops the store and its one grant. §8's table is where the cost of that is written down, and the cost is the whole reason for the answer. |

**The one-line why**, in the owner's terms: **the rules bind client tokens and the installation's
database grants are project-level, so no rule can keep a launcher, an operator, a budget admin or the
history account out of the project's database** — the records have to leave it.

**Which separate store was not decided by this answer, and has been since.** A second Realtime
Database instance, Firestore and a bucket prefix are all "a store of their own"; §8 compares the
three and recommends the bucket, and it carried the choice as an **Open item** — **answered by the
owner the same day, 2026-11-05, again as recommended: the bucket of §8.** The one-line why is the
owner's own ruling read onto the candidates: D935's answer was "a store of their own", and the bucket
is the only one of the three whose IAM can name **exactly one account** (the instance inherits the
project-level database grants until four sets of people are re-scoped, and Firestore's data roles
cannot be narrowed to a database or a collection at all). **§8 records the answer, M2 and M4 name the
bucket and no longer wait on anything, and no other task moves** — every one of them builds against
M2's `Store` interface, which never named a store. Nothing else about the design moves: D922's rule —
hashes only, shown once at mint — stands, D930's rotation and one-delete revocation stand, and D933's
operator read of the audit record stands, because it is served by a tool and never by a node.

**The store answer is carried here and in [reconciliation.md](reconciliation.md) §4, and this plan
wins where the two files part.** [spec.md](spec.md) §6 and §8 carry the answers of 2026-11-04 with
the amendment of the same day written **beside** them as dated notes rather than over them, so the
paragraphs a reader meets first still say the records live in the `mcpauth` subtree of the project's
database, and the note under each is where they leave it. Where the two part — the store the records
are at rest in, and whether the closed node is the barrier or the second barrier — **this document
is the one the tasks build**, and the difference is recorded in
[reconciliation.md](reconciliation.md) §3 (F32) and §4 (the answer) so that a reader of either file
cannot meet it on their own.

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
  both metadata paths from one table. **The licence is still UNCONFIRMED** — nothing in that review
  could settle it — so **the owner or counsel confirms it, and counsel's clearance of the transitive
  dependencies** is part of that, with **the M5 gate file the owner is to create**. **The owner
  confirmed the library's own licence on 2026-11-06** — `zitadel/oidc` is Apache 2.0 — **which is
  what M5's dependency is taken against; the transitive set and the gate file are the part of that
  sentence still standing.**
- **D922 — H3.** Keyed hashes and MACs, no database admin on the public service, and the records
  outside the main database (**D935**, the store of §8). M2, M3 and M4 carry it.
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
- **D930 — M-d.** Rotation is a check-then-write race: M2 conditions it on **the generation of the
  same read** — the generation-matched replace `internal/blobx` gives the bucket D935 moved the
  records to (`internal/blobx/blobx.go:1`) — with a small grace window, and a replay outside the
  window still revokes the family.
- **D931 — H2 — see D936.** The original answer is the **shape `spawn_task` must take when it is
  built**; the finding is that it must not be built in the first slice.
- **D932 — M-g.** A recorded transcript can hold a credential the agent printed, so the read tools
  serve the progress record **redacted through `internal/safetext`** (M8).
- **D933 — M-f.** The lists are read from **a source only an operator can write** — the
  installation's own published state, not one machine's local config (M7).
- **D934 — M-e.** **"Off" is the absence of the `allUsers` binding**, and the installer's own cover
  rule refuses `allUsers`, so M4's stage asks its **own typed confirmation** and **M4 must not widen
  `tf.Cover`**.
- **D925, D926, D927 and D928** were not touched by the review.

**What the amendment does to the tasks that were already running.** **M1 and M3 may continue** — M3
is unchanged, and **M1's spend-type finding is M9's to close**, which is where the review of
2026-11-05 found the same defect from the other end
(`ee/coordinator/mcpauth/limits.go:298`) — and **M4 and M6 were cancelled** and were to be rerun from
this amended plan, because M4's draft predates the D934 and M-e answers and M6's predates M-a and H1.
**Where the tree has gone since**: **M1 (#1713), M2 (#1767), M3 (#1715), M6 (#1714) and M4's first
half (#1716) are merged on `main`** — M4's half being the account, the front door service and its
public invoker binding (`deploy/terraform/gcp/modules/installation/coordinator.tf:320`, `:382`,
`:527`) — **and M4's store half, M5 and M7–M10 are not started**. **What each built task still owes is
named in its own section below**
([reconciliation.md](reconciliation.md) §4a, "What has happened since").

### The decision the amendment added

It was taken on 2026-11-04, out of this folder's block: **D936**, whose full question-and-answer
shape is [reconciliation.md](reconciliation.md) §4b. **The other decision-shaped finding, H3, is
D935** — the answer of 2026-11-05 recorded in §1 above — and the store it names is the one M2 and M4
build.

| D | The question | The answer | What it moves here |
| --- | --- | --- | --- |
| **D936** | Is `spawn_task` in the first MCP slice? | **No — deferred out of it.** The first slice is **the authorization server plus the read tools**; `spawn_task` (and `cancel_task`, whose marker is an object in the runs bucket — the second privilege the coordinator does not hold) is its own **later design item** | **M9 leaves the first slice**, and the M10 rows that depend on it leave with it; §2's order, §4's table and §5's slot are re-cut |
| **D935** | Where do the token records live, now that IAM bypasses the rules? | **Outside the main database** — the store §1's answer of 2026-11-05 picked: the bucket `fugaro-mcpauth-<gcp-project>`, whose only member is the front door's own account `mcp_server`, with the `mcpauth` node a **tripwire** in the rules and **no database admin** on the public service | **M2** builds the store, **M3**'s node becomes a tripwire, and **M4** creates the bucket and its one grant |

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
`--no-budget-check` at `internal/cli/run.go:325`, no `--token` at `internal/cli/run.go:313`, no
free-form `--run-id`); and `mode = max(requested, the token's minimum)` always.

## 2. The edges, the levels and the critical path

**Twelve task-to-task edges**, and nothing else forces the shape: "A ← B" reads "A waits for B", and
the gates that are not tasks — the SECURITY review, D928, D921's licence and
check 0's unbuilt fence — are named beside the task each one holds. **§8's store answer was one of
them and is not any more**: the owner gave it on 2026-11-05, so M2 and M4 wait on nothing but the
tasks and the review below.

**M1** ← nothing. **M2** ← M1. **M3** ← nothing: it is a rules file and
its tests, and it can land first. **M4** ← the SECURITY review of [spec.md](spec.md) §8. **M5** ← M2, M4, D928, and D921's licence confirmed. **M6** ← M4, M5 (it wires a
verifier into the server M5 mounts). **M7** ← M2, M5. **M8** ← M7. **M9** ← M4, M7, and the
launch-override fence of [reconciliation.md](reconciliation.md) §6 (check 0), which is NOT BUILT.
**M10** ← M5, M7.

Three dependencies force that shape and nothing else does:

1. **M4 before M5.** The endpoints have nowhere to run until the coordinator has a service and an
   account of its own — and, since D935, nowhere to keep a record until the store and its one grant
   exist. M4 is the only task that needs Terraform and the only one that cannot be proven
   hermetically beyond a plan test.
2. **M2 before M5.** The token endpoint's whole job is to mint a record M2 defines.
3. **M1 before everything that reads a limit**, because "only narrows" is the one property every
   other task assumes.

### Levels

A level is a set of tasks that may run at the same time. **No level has two writers of one file** —
that is the rule §4's shared-files table exists to check, and the reason four tasks that share one
registry end up four levels apart.

| Level | Tasks | What holds a task out of an earlier level |
| --- | --- | --- |
| **L0** | M1, M3, M4 | nothing. M4's one remaining gate (the review) stands in front of the level rather than in it |
| **L1** | M2 | `token.go` is M1's file, and D935's answer decides `store_bucket.go` |
| **L2** | M5 | M2's store, M4's service, and D921's licence |
| **L3** | M7 | `server.go` and `service.go` are M5's |
| **L4** | M6, M8 | M6: `server.go`, which M5 and M7 both write; M8: `tools.go`, M7's |
| **L5** | M9 | `tools.go` is M8's, `limits.go` is M1's, and `spend.go` needs a release window of its own (§5) |
| **L6** | M10 | `tools.go` is M9's |

**Critical path:** the review → **M4 → M5 → M7 → M8 → M9 → M10**, six levels to the
last task, with **M1 → M2 → M5** joining it at M5. **Off it:** M3 entirely (it can land at any point,
and it is the one task that shares no file with any other), and M6, which takes the last of
`server.go`'s three slots so that M7 does not have to.

**Sequential by necessity**, and not by preference: **M7 → M8 → M9 → M10** are four writers of one
registry (`ee/coordinator/mcpauth/tools.go`), which is the file that decides what a client can call —
a planner who wants them in parallel is asking for four conflicts in the one file the whole front door
answers through. **M5 → M7 → M6** are the three writers of `server.go`, and M6 is off the critical
path, which is why it takes the third slot rather than the second. **M4 → M5 → M7** are the three
writers of the service's own entry point (`ee/coordinator/service.go`). **M9** waits for a release
window in which `internal/runner/spend.go` has no other writer (§4), which no level can schedule.

**The two slices, drawn (D936).**

```
first slice:

M1 ──▶ M2 ──▶ M5 ──▶ M7 ──▶ M8
        │      ▲      │
M3 ─────┘      │      └──▶ M10 (the read half)
               │
M4 ────────────┘        (M4 and M6 are the two that need a live installation)
M6 ────────────┘

deferred slice:

M7 ──▶ M9 ──▶ M10 (its spawn half)
```

**The deferred slice is one task plus one half-task, and it is cut on purpose.** **M9 leaves the
first slice**, and with it the parts of M10 that name a spawned or cancelled run; nothing in the
first slice starts a run, holds the launch grant or changes `internal/runner`. That is D936's whole
point: the first slice **answers strangers and reads** — the authorization server, the tool registry
and the four read tools — and the sentence "the coordinator mints nothing" stays true while it does.
**Of §2's levels, L5 is the deferred slice's and L6 loses its spawn half; every other row and every
other edge above stands as written.**


## 3. The tasks

Every task is written to be mergeable on its own. **SEC** marks a task whose diff an independent
review must read before it merges, which the brief requires for the threat model as a whole and which
is asked here for every task that changes who may reach what.

**Two gates stand before the first line of any task.** The **independent SECURITY review of
[spec.md](spec.md) §8** — **run by the coordinator**, per the owner's brief — comes first: no task
starts until it has been read and its findings answered, and its scope is §8, every **SEC**-marked
task below (the five whose marker says **mandatory** are M3, M4, M5, M7 and M9) and the two new
privileges this design asks for (M9's launch grant, and M4's single binding on the store the
`mcpauth` records moved to). **D921's unconfirmed licence** is the second
([reconciliation.md](reconciliation.md) §6, the first owner check): it gates **M5** specifically,
because M5 is the task that takes the dependency, and a licence that turns out to be unusable sends
D921 back to its alternatives before any code exists rather than after. **The owner confirmed the
library's own licence on 2026-11-06** — `zitadel/oidc` is Apache 2.0 — **so M5 takes the dependency
against that confirmation, and the transitive dependencies' clearance and the gate file are what the
second gate still holds.**

**That review has run, on 2026-11-04, and the gate from here on is per task**: its findings and the
owner's answers to them are [reconciliation.md](reconciliation.md) §4a, every document in this folder
is written to them, and **a task starts only when the finding that names it is answered in the task
it builds** — which is what §3 says of each task below. Its scope was §8, every SEC-marked task (the
five whose marker says **mandatory** are **M3, M4, M5, M7 and M9**, **with M9 keeping the marker in
the deferred slice**), and the two new privileges the design asks for. **D921's licence** is the half
of the second gate that no review could settle: it is **UNCONFIRMED**, so what stands is **the owner
or counsel confirming it — counsel's clearance of the transitive dependencies included — before any
build**, with **the M5 gate file the owner is to create** ([reconciliation.md](reconciliation.md) §6,
check 1). **The owner confirmed the library's own licence on 2026-11-06 — `zitadel/oidc` is Apache
2.0, read from the module cache — which is the half of this gate M5 is built against; the transitive
set and the file are the halves it still holds.**
**A third gate stood in front of M2 and M4, and it has been answered rather than left standing.**
D935 moved the records out of the project's database and left **which** separate store to the owner
(§8), so M2's backend file and M4's Terraform were both written against an answer nobody had given
yet, and both named §8's recommendation and waited. **The owner answered on 2026-11-05, again as
recommended: the bucket of §8 — `fugaro-mcpauth-<gcp-project>`, a bucket of its own whose one member
is the front door's own account `mcp_server`** — so **M2 and M4 are written to it and wait on
nothing**, and §8 records the answer in the place the Open item stood. **No task was ever gated
twice over by it**: M3, M5, M6, M7, M8, M9 and M10 are written to M2's `Store` interface, which does
not name a store, so an answer the other way would still move one implementation file and one
Terraform file rather than any interface any other task builds against.

### M1 — The limits vocabulary, and "only narrows"

- **Files (6):** `ee/coordinator/mcpauth/limits.go`, `limits_test.go`, `ee/coordinator/mcpauth/token.go`,
  `token_test.go`, `ee/coordinator/mcpauth/limits_table_test.go`, `ee/coordinator/mcpauth/doc.go`.
- **Interface:** `Limits{MinRigor, MaxRigor rigor.Mode; Hotfix bool; SpendUSD float64; Scopes []string}`
  and `func (l Limits) Narrow(project Limits) (Limits, error)`. `Narrow` takes the tighter side of
  every field and returns an error naming the field when the result admits nothing.
- **Red step:** `TestLimitsOnlyNarrow` — a token asking for `critical` under a project policy of
  `pro` resolves to `pro`, and a token whose spend exceeds the run's ceiling resolves to the
  ceiling. It fails to compile before `Narrow` exists, which is the red.
- **Tests:** `TestLimitsOnlyNarrow`, `TestHotfixIsNotOnTheAxis` (a `min: pro` token refuses Hotfix
  with the flag off, and a Hotfix token passes a `min: poc` policy — the `Rank()`-0 case of D925),
  `TestEmptyWindowIsRefusedAtMint`, `TestUnknownScopeIsRefusedNotIgnored`.
- **SEC:** no grant changes; reviewed with the threat model because it is the rule the whole folder
  rests on.
- **Depends on:** nothing.
- **The amendment's low finding, and where its fix lands (2026-11-04).** The field the review found
  is `SpendUSD float64`, which cannot tell an unset limit from a limit of zero — and the review of
  2026-11-05 found the same defect from the other end, where `tighterSpend`
  (`ee/coordinator/mcpauth/limits.go:298`) treats `+Inf` as a value. **Refusing that ceiling is
  M9's** ([reconciliation.md](reconciliation.md) §4a, "the low findings"), and the **optional
  integer in micros** the finding asked for is that fix's own decision rather than a second one. An
  unknown rigor-mode string is **refused** rather than read as the loosest mode. **Nothing above
  changes, and this task may continue.**

### M2 — Minting and the store: hashes only, in a store of their own (D935)

- **Files (8):** `ee/coordinator/mcpauth/store.go`, `store_test.go`, `store_bucket.go`,
  `store_bucket_test.go`, `mint.go`, `mint_test.go`, `ee/coordinator/mcpauth/fake_test.go`,
  `ee/coordinator/testdata/mcpauth/README.md`.
- **Interface:** `Store` — `PutClient`, `GetClient`, `PutToken`, `LookupByHash`, `RevokeFamily` — over
  **the store of D935's answer, and never the project's shared database**;
  `Mint(identity, client, limits) (access, refresh string, err error)` writes through it. **`store.go`
  is the interface, the key layout and the record shapes; `store_bucket.go` is the one concrete
  backend**, so an owner who had answered §8 with a different store would change that file and its own
  test and nothing any other task builds against. The backend is §8's answer of 2026-11-05 — **the
  `mcpauth/` prefix of a bucket of its own, `fugaro-mcpauth-<gcp-project>`, whose only member is the
  front door's own account `mcp_server` — the front door's dedicated account of M4, **not** the
  sweep's (`google_service_account.coordinator`) — and whose name is outside every prefix a conditioned
  role in this installation is fenced to (M4, and the correction of 2026-11-05)**. **M2 never names,
  creates or adopts a bucket: it opens the one name Terraform writes into the installation's outputs
  (`deploy/terraform/gcp/modules/installation/outputs.tf`, M4's own row of §4), and a bucket that
  already exists when M4 applies is M4's failure rather than something this backend picks up** (§8,
  "the name is squatting-shaped") —
  written with `internal/blobx` (`internal/blobx/blobx.go:1`): create-if-absent with the new
  generation, generation-matched replace and delete, atomic on GCS. That is what makes rotation and
  revocation compare-and-swaps rather than read-modify-writes, and it is why a bucket can carry a
  store this design otherwise wants a transaction for.
- **Red step:** `TestMintStoresOnlyTheHash` — every value written through the store is compared
  against the two strings `Mint` returned, and the test fails if any record contains either or any
  prefix of either. Written first, it fails because there is no `Mint`.
- **Tests:** `TestMintStoresOnlyTheHash`, `TestLookupFindsByThePresentedToken`,
  `TestRefreshRotatesAndReplayRevokesTheFamily`, `TestRevokedTokenIsNotFound`,
  `TestARotationUnderAConcurrentRevokeLosesNeither` (the generation-matched write a bucket-backed
  store needs — a database transaction used to give it away, and a bucket does not),
  `TestTheStoreRefusesTheDatabaseAndTheRunsBucket` (the store opens one URL from the coordinator's own
  configuration and refuses anything else at construction, so a later edit that pointed it back at
  `mcpauth` in the database, at the runs bucket, **or at a bucket under the managed prefix `fmgd-` —
  the reach the correction of 2026-11-05 removed** — fails rather than works),
  `TestTheStoreOpensTheBucketTerraformOutputs` (the one name the backend opens is the store bucket M4
  writes into the installation's outputs, and **the backend neither creates nor adopts a bucket**: a
  name it is handed that already exists, or one that is not the output, is refused rather than
  opened — the squatting half of §8, which is M4's refusal to make and M2's not to undo).
- **SEC:** yes — it is the task that decides what is at rest **and where**, and D935's answer is only
  worth anything if the store is the store and not the database under another name.
- **Depends on:** M1. **Nothing else** — §8's open item was answered on 2026-11-05 (the bucket), and
  this task is written to that answer rather than waiting on it.
- **The amendment's marks, which this task carries too (2026-11-04, H3).** Beyond "hashes only":
  **the hash is keyed** — an HMAC under a **pepper held in Secret Manager**, which is not in the
  store — and **every record's identity, its limits and its revocation state are MAC'd**, so a reader
  who is not the store's one member learns nothing and a record written by anybody else is **refused
  rather than honoured**. `TestAKeyedRecordTheStoreDidNotWriteIsRefused` is that row.

### M3 — The rules name the token records' old home, and keep it closed (D935)

- **Files (3):** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`,
  `internal/budget/rules/mcpauth_test.go`.
- **Interface:** a top-level `mcpauth` subtree in the template — **a sibling of `coordinator/`, not a
  child of it** (D922's answer) — with **`.read: false` and `.write: false`**, named in the file so
  that the next person who adds a grant can see it is there, plus the constant the parity test reads
  it through. It deliberately does **not** copy `coordinator/`'s `.read: "USER"`
  (`internal/budget/rules/rules.json.tmpl:10-13`, the claim at `internal/budget/rules/rules.go:517`):
  the node is refused to every client, and the one account that could write it — the coordinator's
  own — reaches it through `roles/firebasedatabase.admin`
  (`deploy/terraform/gcp/modules/firebase/iam.tf:138`) and not through a rule. **That last sentence
  states the rules' half only, and the IAM half is what the Open item below raised; D935 is the
  owner's answer to it, recorded in the bullet after this one.**
- **What the node is now, and what its comment must say (D935, 2026-11-05).** **The records do not
  live here any more**: they live in the store M2 defines, because this installation's database grants
  are project-level and no rule can keep a launcher, an operator, a budget admin or the history
  account out of this database (`deploy/terraform/gcp/modules/firebase/iam.tf:44`, `:53`, `:81`;
  `docs/design/roles.md:104`, and [reconciliation.md](reconciliation.md) F32). **What #1715 added
  stays as a defence in depth**, and the comment above the node must say exactly that, in these terms:
  *that the node holds nothing* (nothing writes it any more, and the records are elsewhere);
  *why it is closed anyway* (a value written here by mistake — an older build, a hand-run script, a
  later task that reads this design's first revision — is then unreadable by every client, which costs
  one rule and buys the one mistake nobody would notice); *where the records went* (the store of
  D935, whose only member is the front door's own account `mcp_server`, over IAM); and *what the node must
  not become* (a node the coordinator reaches through its `roles/firebasedatabase.admin` grant,
  `deploy/terraform/gcp/modules/firebase/iam.tf:138` — that grant is for the coordinator's own
  `coordinator/` subtree, and it is precisely the fact that it reaches the whole database that made
  the records leave). The constant stays for the same reason it was added: a parity test over the
  template is what says the node is still named, and a comment is not.
- **Red step:** `TestMCPAuthIsNamedAndDeniedToEveryClient` — it asserts the subtree is **named** in
  the template, and that the emulator refuses a **read** as `USER` and as a run's `TOKEN` and refuses
  a **write** as `USER` and as `TOKEN`. It fails before the template change because the key is
  absent. (The root's blanket denial already refuses the `TOKEN` read, which is exactly why the test
  has to assert the *name*: an unnamed subtree is protected by a rule nobody reading the file can
  see. It asserts the `USER` **refusal** too, because that is the half D922's restatement decides —
  a later change that gave `mcpauth` the `coordinator` node's read rule would be a decision, and this
  test is what makes it a failing one rather than a quiet widening.) **D935 adds one assertion**: the
  comment above the node must name the store the records actually live in, so that a reader who finds
  the node cannot come away believing the token material is still at rest in the database — the
  failure mode D935's answer creates, and the only new way this task can be wrong.
- **Tests:** `TestMCPAuthIsNamedAndDeniedToEveryClient`, and `TestRulesParity` must stay green.
- **SEC:** **yes, mandatory** — it is still a rules change naming the token records, and the review's
  question is now the reverse of the one the first version of this task asked. Not what the exposure
  accepts, but whether a closed node beside records that have moved misleads its reader into thinking
  the project's database is where the token material lives, and whether anything but the coordinator's
  own account can reach a node the rules refuse and the database admin grant reaches past. M4 is the task
  that decides who reaches the store they moved to.
- **The open item this task raised, and the answer it got (raised 2026-11-05, answered the same day).**
  [spec.md](spec.md) §8 says the subtree is "read by the coordinator's account
  over IAM and by nobody else" (`docs/mcp-server/spec.md:513`) and calls the coordinator's account
  its only writer, and the Interface bullet above repeated that; **neither held.** A rule binds a
  client token and binds no IAM identity, and the installation's RTDB roles are granted
  project-wide, with no path scope and no condition: `roles/firebasedatabase.viewer` to every
  launcher and every operator (`deploy/terraform/gcp/modules/firebase/iam.tf:44`), who read the
  whole database past the rules, and `roles/firebasedatabase.admin` to the admins and budget admins
  (`deploy/terraform/gcp/modules/firebase/iam.tf:53`), to the history service account
  (`deploy/terraform/gcp/modules/firebase/iam.tf:81`) and to the coordinator's own writer
  (`deploy/terraform/gcp/modules/firebase/iam.tf:141`), who read and write it. So `mcpauth` is
  refused to every client the rules bind — the whole of what a rule can do, and what M3's red step
  proves — and **not** refused to the installation's own IAM identities: the token records, the
  client list, the redirect URIs, the revocation state and the whole tool-call audit were inside that
  exposure. The rules package already records this fact for `config/admin`
  (`internal/budget/rules/rules.go:59`), [roles.md](../design/roles.md) states it for the database as
  a whole (`docs/design/roles.md:104` — "Any confidential thing placed in RTDB is readable by
  everyone on either list"), and M3's own SEC bullet above is the question it answers: something
  other than the coordinator's account **can** reach this node. **The options it put to the owner,
  none of them decided in it:** a store the installation's RTDB roles do not reach (a separate
  database instance, or a different store altogether); narrower IAM, which RTDB's project-wide
  `firebasedatabase.admin` and `firebasedatabase.viewer` do not offer at path level; or the exposure
  accepted and §8 and this section corrected to say what is true. **The owner took the first on
  2026-11-05 — D935, the store of the first kind — and this open item is answered rather than
  standing**: the records leave this database for a store whose IAM names the front door's own account
  `mcp_server` and no other member — the account M4 creates (`coordinator.tf:320`), never the
  coordinator's own (`coordinator.tf:71`, the sweep's) — §8's sentence is corrected by the plan's own
  §8, and the node stays closed as a defence in depth. What M5 writes is therefore M2's store and not
  `mcpauth`, and the answer to this item is what decides that.
- **Depends on:** nothing. **Can land first**, and it is the one task that shares no file with any
  other.
- **Do not touch:** `internal/budget/rules/rules.go` is shared with every other rules work; the
  change is one constant and one template key, and a conflict there is a rebase, not a redesign.
- **The amendment's finding, and what this task does with it (2026-11-04, H3).** **Its finding was
  H3** — a closed rule is not a closed node, because `roles/firebasedatabase.viewer`
  (`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`) is an IAM read every launcher and operator
  holds and `roles/firebasedatabase.admin` (`:53-58`, `:81-84`) is a write several accounts hold —
  and the answer is D935's (the records leave) with the marks D922's amendment adds. **Nothing in
  this task's diff changes because of it**: the node, the constant and the red step stay as written,
  and the comment above the node must say it holds nothing. **May continue**, unchanged.

### M4 — The coordinator's public service, its own account, its grants and the store (D935)
**Cancelled on 2026-11-04 and to be rerun from this amended plan**: its draft predates the D934 and
M-e answers — the meaning of "off" and the collision with the installer's own cover rule — and the
store D935 moves the records into, which this task creates as a bucket of its own. **Its first half
landed as #1716** — the account, the front door service and its public invoker binding
(`deploy/terraform/gcp/modules/installation/coordinator.tf:320`, `:382`, `:527`) — and **its store
half, `mcpauth_store.tf` with the bucket and its one grant, is not started.**

- **The amendment's findings (2026-11-04). The review's findings were H3, M-e and D934's cover
  collision; all three are answered above**: the account holds no `firebasedatabase.admin` over
  anything (H3), **"off" is the absence of the `allUsers` binding** and the stage that adds it asks
  its **own typed confirmation** (`internal/infra/tf/cover.go:911`), and **M4 must not widen
  `tf.Cover`** to make its own plan covered (`:913` refuses `allUsers`).

- **Files (8):** `deploy/terraform/gcp/modules/installation/coordinator.tf`,
  `deploy/terraform/gcp/modules/installation/mcpauth_store.tf` (new: the store and its one grant),
  `deploy/terraform/gcp/modules/installation/outputs.tf`,
  `deploy/terraform/gcp/modules/installation/coordinator_service_test.go` (a plan-level test),
  `internal/infra/tf/cover.go` (the store's two types, `google_storage_bucket` and
  `google_storage_bucket_iam_member`, are already covered at
  `internal/infra/tf/cover.go:64` — **unless this task takes the authoritative-policy route to strip
  the convenience bindings the floor bullet above describes, in which case
  `google_storage_bucket_iam_policy` is a type F2's "one does not appear" did not anticipate and
  belongs in `allowedTypes` on purpose, with the reason written beside it**),
  `internal/infra/coordinator_service.go`, `internal/infra/coordinator_service_test.go`,
  `ee/coordinator/service.go`.
- **Interface:** a `google_cloud_run_v2_service` for the coordinator with `allUsers` invoker (D920),
  its own service account, no model credential mounted, and the launch role of M9 declared but not
  yet used. **And the store of D935, which is this task's because this is the task that writes IAM.**
  The store is **a bucket of its own — `fugaro-mcpauth-<gcp-project>`, a function of the
  project alone, the convention the runs bucket and both state buckets already follow
  (`internal/managed/statebucket.go:75`)** — with uniform bucket-level access and public access
  prevention enforced, because the installer's plan review refuses a bucket without both
  (`internal/infra/tf/cover.go:2199`). **Its name is deliberately outside every conditioned prefix,
  and that is the correction of 2026-11-05: `fmgd-` is the managed deployer's fence, not this
  store's.** **Six things about its IAM and its name are deliberate:**
  - **One binding, one member, and the member is the front door's own account.** The member is
    `google_service_account.mcp_server` — the account this task creates for the service, in
    `coordinator.tf`, and the one #1716 shipped — and the store's member is named that way in every
    document that carries the design **and nowhere as "the coordinator"**, because the coordinator's
    account and this one are two different accounts with two different reaches (M2, §1's D935 row and
    §8's table used to say "the coordinator's own service account"; they say `mcp_server` now, and
    M2's store is written to that name). Every storage grant in this installation that is not the
    deployer's conditioned one is bucket-scoped
    (`deploy/terraform/gcp/modules/installation/iam.tf:207`, `:222`, `:238`;
    `coordinator.tf:92`), and the deployer's conditioned role is fenced to names this store does not
    carry, so a bucket with one **binding** has exactly one **member**. **A member is not the same
    thing as a reader**, which the fifth bullet is about: the bindings this task writes are not the
    only ones a new bucket answers to, and a bucket whose IAM is left to the defaults is read by
    people this plan's member list never names. That is
    the whole of D935: the IAM names the front door's own account and nobody else *that the
    installation grants*, where the database's own grants name four sets of people.
  - **`roles/storage.objectAdmin`, and not `objectUser`.** A bucket-scoped `objectAdmin` carries no
    IAM permission and no `storage.buckets.delete`, so the account reads and writes objects in this
    one bucket and can neither widen it nor remove it. It is also the narrowest role the plan review
    accepts here with no condition (`internal/infra/tf/cover.go:1700` refuses an unconditioned
    `objectUser`), and the same role operators already hold on the runs bucket — one bucket, one
    account, one role, nothing new in shape.
  - **The sweep's account is not the member, and the front door's own account is.** The member is the
    front door's dedicated account `mcp_server` — never the coordinator's own
    (`google_service_account.coordinator`), which is the sweep's. The sweep (P5) reads runs and never
    tokens, and the operator's read of the audit
    record is served by a tool (M10) rather than by a credential — so the second the store gains a
    second member is the second this design has widened, and the test below is what makes that fail
    rather than happen.
  - **The name is outside the managed prefix, and that is what the first version of this plan got
    wrong (corrected 2026-11-05).** The earlier bullet here said the opposite — that the name should
    carry `fmgd-` so the managed deployer's storage role would reach it, and that "no project-level
    storage binding exists anywhere". **The second half is false and the first half was the bug.**
    The deployer holds a **project-level** binding of `fugaroDeployerStorage`
    (`deploy/terraform/gcp/modules/installation/deployer.tf:379`, the role at `:237`), and the role is
    conditioned on `projects/_/buckets/fmgd-` (`deployer.tf:142`) while carrying
    `storage.objects.get/list/create/update/delete` and `storage.buckets.setIamPolicy/update/delete`:
    a store named `fmgd-...` would be readable **and rewritable** — the token hashes, the redirect
    URIs, the revocation state and the whole audit — by every identity that can act as the deployer,
    and re-grantable to anyone the deployer may bind. Every operator is such an identity, because the
    installation gives each of them `roles/iam.serviceAccountTokenCreator` on the deployer's own
    account (`deployer.tf:501`, `:513`), and every managed apply is another. **That is F32 again, one
    resource over.** Naming the bucket outside `fmgd-` puts it out of that role's reach entirely, and
    the deployer's reach is not needed: **`fugaro update` runs with the owner's own rights**, which
    create every bucket in the project. Nothing in a managed plan can take the name either — the
    deployer's only unconditioned storage permission is `storage.buckets.create`
    (`deployer.tf:219`), and creating a bucket that already exists fails.
  - **The floor under the store is wider than "the project Owner", and this task owns the part of it
    that can be narrowed (corrected 2026-11-05).** The earlier text here said the project Owner was
    "the only other identity that reaches it"; **that is too narrow, and `SECURITY.md` says so in
    its own words for a bucket this installation owns**: "Project Owners, Editors and anyone with a
    project-level storage role bypass the bucket's own policy regardless, hardened or not"
    (`SECURITY.md:88`, over [docs/design/bucket-iam.md](../design/bucket-iam.md) §9). And a new
    bucket answers to more than the policy this task writes: **GCS attaches its convenience bindings
    to every bucket it creates** — `projectOwner:` and `projectEditor:` as
    `roles/storage.legacyBucketOwner`, and `projectViewer:` as `roles/storage.legacyBucketReader`
    plus `roles/storage.legacyObjectReader` (`internal/gcpfake/gcs.go:250`,
    `docs/design/bucket-iam.md:47`) — so **a basic project Viewer reads the
    store unless those bindings are taken off it**, and that is the reading check 8 gains. Four
    classes therefore reach a bucket this task has just made, and they are not one:
    - **Project Owners and Editors**, through their project-level roles, which no bucket policy
      narrows. Nothing in this task can take that away, it is the floor under every option of §8 (a
      second database instance and Firestore are reached by them too), and it is stated rather than
      mitigated. **UNVERIFIED here**: the exact permission each carries is the platform's, and this
      container has no project to read it from.
    - **Anyone else holding a project-level storage role**, for the same reason and with the same
      floor.
    - **A basic project Viewer**, through the convenience read bindings — and this one **is** the
      task's to close.
    - **A member the installation grants on the bucket**, which is exactly the one binding above.
    **M4 therefore owns the member list and not only the binding it writes**: the store's bucket must
    end with **no legacy or convenience binding and no second member** — uniform bucket-level access
    does not remove them and neither does writing an additive member — so this task **strips them, on
    the pattern `fugaro setup` already uses on the state bucket (`internal/infra/statebucket.go:140`,
    `RemoveProjectViewers`), or refuses to ship the bucket until they are gone**, and whichever it
    does is a plan-level assertion, not prose. What D935 buys is that the store names nobody **else** —
    no launcher, no operator, no budget admin and not the history account — which is exactly what the
    database it leaves cannot do; **what it cannot buy is a reader the project's own IAM gives
    everywhere, and this task says which of those it narrowed and which it could not.**
  - **The name is squatting-shaped, so this task creates it and never adopts it (2026-11-05).**
    `fugaro-mcpauth-<gcp-project>` is a **function of the project alone** — the same shape the runs
    bucket and both state buckets have — which makes it **predictable**, and bucket names are
    **global**: anyone on the internet can hold that name in another project. The deployer holds
    `storage.buckets.create` **unconditioned** (`deployer.tf:219`), so nothing in a managed plan can
    be relied on to have taken it either. **Terraform is the answer and it is a fail-closed one**: the
    bucket is created by `fugaro update` running with the owner's own rights, and a create against a
    name that already exists **fails the apply** rather than importing it — **never adopt a bucket
    this installation did not create**, and never read the store out of one. M4 therefore asserts
    the create is a create (no `terraform import`, no data source, no "use what is there"), and
    **M2 opens only the name this task writes into the installation's outputs** (§3, M2) — so a
    squatted name is an installation that fails to update rather than an installation that stores
    token hashes in somebody else's bucket.
- **Red step:** `TestCoordinatorServiceIsPublicAndCarriesNoCredential` — it fails against the current
  Terraform because there is no service, and it asserts the two things that matter (the invoker
  binding is `allUsers`, and the container carries no secret whose value is a model credential).
- **Tests:** `TestCoordinatorServiceIsPublicAndCarriesNoCredential`,
  `TestCoordinatorServiceHasItsOwnAccount`,
  `TestTheStoreHasExactlyOneMemberAndItIsTheMCPAccount` (the plan's bindings on the store's bucket
  name one member, that member is `google_service_account.mcp_server` — the front door's own account
  and **not** `google_service_account.coordinator`, whose comment is what says why — no other row of
  the plan names that bucket, and **the bucket's name outside every prefix a conditioned role in this
  installation is fenced to** — the `fmgd-` assertion that is the correction of 2026-11-05 kept as a
  test rather than only written as prose),
  `TestTheStoreBucketCarriesNoConvenienceBinding` (the floor bullet's half that can be narrowed: the
  plan leaves no `legacyBucket*` role, no `projectOwner:`/`projectEditor:`/`projectViewer:` member and
  no second member on the store's bucket — **the assertion that fails the day the convenience bindings
  GCS attaches to a new bucket are left on it, which is the one class of reader a basic project Viewer
  is**), and
  `TestTheStoreBucketIsCreatedAndNeverAdopted` (the name is predictable and global and this task
  creates it with the owner's rights: no `import`, no data source, no re-use of a bucket that is
  already there — a refusal at apply rather than a store in somebody else's bucket), and the
  existing `cover.go` tests must stay green.
- **SEC:** **yes, mandatory.** It creates a public endpoint, a new service account **and the store
  the token records live in** — the one new store this design adds and the one new grant that keeps it
  private. The review's question is the one D935 asks: whether a plan-level test can be trusted to
  keep that grant's member list at one, **whether the store is out of reach of every identity the
  installation already lets act — the point the first version of this task got wrong, where the
  managed deployer's conditioned role was the way in rather than the reason the name was chosen** —
  and **which readers of the bucket this task could not remove at all** (the floor bullet: the
  project Owner's and an Editor's project-level roles are not this task's to take away, and the
  correction of 2026-11-05 is that the plan says so instead of saying "the Owner" and stopping). Its
  second question is whether the store's name and its IAM are the only route to it.
- **Depends on:** D920 answered on 2026-11-04 (**it was, the recommendation**), and the SECURITY
  review of [spec.md](spec.md) §8 read. **§8's open item is answered** (2026-11-05: the bucket), so
  the store this task creates is the one §8's answer names and nothing here waits on the owner.
- **Owner check:** the service and the store are applied only by `fugaro update`, and this plan does
  not run it.

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
- its own service account, and the public invoker binding D920 asks for;
- **the store the `mcpauth` records live in and its one grant** (D935): the bucket §8's answer of
  2026-11-05 names, `fugaro-mcpauth-<gcp-project>`, and the single `objectAdmin` binding for
  `mcp_server`, the service's own account. **It adds no operator step of its
  own** — no second command, no `gcloud` line, no `terraform apply` by hand: the bucket and its
  binding are Terraform this same `update` applies, and both names are functions of the project
  alone. **The store has no flag and no opt-in of its own either**: it arrives with the front door's
  opt-in, because it is the service's own state, and an installation that upgrades without the flag
  acquires neither the store nor the grant. **And no operator action repairs it**: a bucket that
  already exists under that name fails the apply rather than being adopted (§3, M4), which is the
  refusal the operator reads and not a name to edit by hand;
- the launch grant on the project's own job (M9), the one new privilege this design asks for;
- the front door's own configuration: the redirect allowlist of D928 and the switch of D934.

An installation that opted into the coordinator for its read-only view acquires **none** of it: the
aggregation job and the front door are separate opt-ins, and a public write endpoint is not something
an upgrade should hand a project that asked for a dashboard. M4 carries the convergence test the
repository requires of a migration: an installation at the previous version, updated with the flag,
ends with exactly the resources above — **the store and its one grant included** — and an installation
that did not pass it ends with none. **One line joins the release notes' `### For operators` section
with D935**, and it is the only operator-facing sentence this section adds: the store is created and
granted by the same `fugaro update` that creates the service, and the owner confirms it with the
hand-check of [reconciliation.md](reconciliation.md) §6 (check 8) rather than with a step.

### M5 — The authorization server: its two metadata documents and its four endpoints

- **Files (9):** `ee/coordinator/mcpauth/server.go`, `server_test.go`, `metadata.go`,
  `metadata_test.go`, `register.go`, `register_test.go`, `authorize.go`, `token.go`,
  `authorize_test.go`.
- **Interface:** `/.well-known/oauth-protected-resource`, `/.well-known/oauth-authorization-server`,
  `POST /oauth/register`, `GET /oauth/authorize`, `POST /oauth/token`, `POST /oauth/revoke`, mounted
  on the service M4 creates. **Every record it writes — a registered client, a code, an access token,
  a refresh token, a revocation — goes through M2's `Store`, and nothing here opens a database or a
  bucket of its own** (D935): the store is the one place a token record is at rest, and the endpoints
  reach it the way every other task does, through the interface rather than through a client. Nothing
  in this task names the bucket, and nothing in it names the project's database.
- **Red step:** `TestNoTokenIsA401NamingTheMetadataURL` — the MCP endpoint answers 401 with a
  `WWW-Authenticate` header whose resource-metadata URL resolves, and the test fails because the
  endpoint does not exist.
- **Tests:** `TestNoTokenIsA401NamingTheMetadataURL`, `TestMetadataPointsAtItsOwnServer`,
  `TestRegisterIsOpenAndGrantsNothing`, `TestPKCEIsRequiredAndPlainIsRefused`,
  `TestCodeIsSingleUseAndBoundToTheRedirectURI`, `TestRedirectURIMustMatchExactly`,
  `TestRedirectURIMustBeLoopbackOrAllowlisted`, `TestRefreshTokenIsSingleUse`,
  `TestNoRecordIsWrittenOutsideTheStore` (a store handed to the whole server: every write the five
  endpoints make arrives through the `Store` interface, and none of them opens a second client — the
  test that keeps D935's "the store is the only place" true as the endpoints are added), and the three
  D928 tightenings' own: `TestRegistrationRefusesANonLoopbackURI` (a non-loopback URI that is not on
  the allowlist is refused **at registration**, not at a person's sign-in),
  `TestConsentNamesTheRedirectHost` (the page a person consents on carries the redirect URI's host),
  `TestConsentIsCSRFBound` (a consent that does not carry the flow's own binding is refused, and the
  `state` returns on the redirect unaltered).
- **SEC:** **yes, mandatory** — this is the endpoint the threat model's redirect-URI, CSRF and open-
  registration paragraphs are about.
- **Depends on:** M2, M4, D921, D928 — **and on D921's licence being confirmed**, by the owner or by
  counsel, before the dependency is added ([reconciliation.md](reconciliation.md) §6, check 1). This
  is the one task the licence gates directly.
- **The amendment's findings, and what this task carries (2026-11-04).** **M-a, M-c, M-f and the
  unverified client name**, all answered here: **a missing challenge, a challenge that is not `S256`
  and an unknown scope are refused before the library sees them**; only the code and refresh grants
  are allowed; the signing material lives in **Secret Manager** or the tokens are **opaque**; both
  metadata paths are served from **one table**; the approval is a **single-use POST** on a named
  endpoint with **no ID token in a GET URL**, showing the client **marked unverified** and the
  redirect host, and requiring **QR proximity or number matching**; and **pre-registration is refused
  inside a coding agent's session** with **`client_credentials` off** — this task builds the refusal
  and **not** the grant. Its tests carry the rows for each of those.
- **What the review settles about the library, and what it does not.** The review found the library's
  defaults are the opposite of this design's rules in five named ways, and that half is settled by
  the constraints above; **the licence is not, and stays UNCONFIRMED** — until **the owner confirmed
  the library's own on 2026-11-06: `zitadel/oidc` is Apache 2.0, which is what this task's `go.mod`
  entry is taken against, with the transitive dependencies' clearance and the gate file still the
  owner's** (the paragraph in §3 above, [reconciliation.md](reconciliation.md) §6 check 1).

### M6 — Firebase at the authorize endpoint
**Cancelled on 2026-11-04 and to be rerun from this amended plan**: its draft predates M-a and H1,
and both change what this task must say and where its verifier is called from. **What merged as
#1714 is that draft**: the verifier is in the tree and the token endpoint refuses an ID token, while
**M-a's change is still to be built** — the approve step's single-use POST and
`TestAnIDTokenInAQueryStringIsRefused` — because nothing calls the verifier from an approve step yet
and M5, which defines that step, is not started.

- **M-a and H1 (2026-11-04).** The verifier is called from **the approve step** — a **single-use
  POST** that carries the ID token in its body and never in a URL — and
  `TestAnIDTokenInAQueryStringIsRefused` is that row. **H1 is documented here and built nowhere**: a
  device token is a custom token **any launcher can mint**, the review's recommendation was a
  device-held signing key, and the owner **accepted and documented the residual risk instead**. So
  this verifier does **not** require a live device node, does **not** refuse `r~`/`p~` tokens and does
  **not** give a paired device a viewer role; the risk, its three consequences and the change that
  would close it are [spec.md](spec.md) §3 and §8 and
  [reconciliation.md](reconciliation.md) §4a, and **no task in this plan may quietly build a piece of
  it**.

- **Files (4):** `ee/coordinator/mcpauth/firebase.go`, `firebase_test.go`,
  `ee/coordinator/mcpauth/firebase_keys.go`, `ee/coordinator/testdata/mcpauth/jwks.json`.
- **Interface:** `VerifyIDToken(ctx, raw string) (Identity, error)`, sharing the verification rules
  `internal/credsvc` already implements (issuer built from the project id, audience compared to the
  bare project id, keys from the public endpoint) and never echoing a token in a refusal. It is
  called from **`GET /oauth/authorize` and from nowhere else** (D923's answer of 2026-11-04): the ID
  token is the proof of the sign-in step, and the token endpoint has no path that accepts one. The
  task carries the other half of that answer too: **the coordinator mints no Firebase custom token
  and holds no signer role**, so there is no minting code here for a reviewer to find, and no way for
  a compromised coordinator to assert an identity the paired device did not approve.
- **Red step:** `TestTokenFromAnotherProjectIsRefused` — a token whose audience is another project's
  id is refused, and the refusal names neither the token nor the audience. It fails because there is
  no verifier.
- **Tests:** `TestTokenFromAnotherProjectIsRefused`, `TestWrongIssuerIsRefused`,
  `TestExpiredTokenIsRefused`, `TestRefusalNeverEchoesTheToken`,
  `TestTheTokenEndpointRefusesAFirebaseIDToken` (the same token that is accepted at authorize is
  refused where it is not a grant) and `TestTheCoordinatorMintsNoCustomToken` (no code path signs
  one).
- **SEC:** **yes** — token verification is where a mistake is silent.
- **Depends on:** M4. **Reuses rather than duplicates** `internal/credsvc`'s verification; the review
  must ask why it is a second implementation, and the answer has to be that `internal/credsvc` is a
  different service's package with a different configuration surface — if the reviewer disagrees, the
  right fix is to extract the verifier, and that is a finding for the owner rather than a refactor in
  this task.

### M7 — The resource server and the tool registry

- **Files (7):** `ee/coordinator/mcpauth/resource.go`, `resource_test.go`,
  `ee/coordinator/mcpauth/tools.go`, `tools_test.go`, `ee/coordinator/mcpauth/scopes.go`,
  `scopes_test.go`, `ee/coordinator/mcpauth/identity.go`.
- **Interface:** one handler that reads the bearer token, validates it (issuer, audience, expiry,
  revocation, scopes), resolves the identity through the project's launcher and operator lists
  (D933), and dispatches JSON-RPC; one registry that answers `tools/list` with **both** the MCP
  tools and the coordinator's own (D931).
- **Red step:** `TestUnknownScopeIsRefusedNotIgnored` — a token carrying a scope the server does not
  know is refused rather than treated as holding no scopes. It fails because there is no registry.
- **Tests:** `TestUnknownScopeIsRefusedNotIgnored`, `TestIdentityOffTheListsIsRefused`,
  `TestOnlyExactUserEntriesAreRead` (D923's answer: a list holding `group:`, `domain:` and
  `serviceAccount:` entries beside its `user:` ones admits the `user:` identity and none of the
  others — the same rule `internal/cli/user.go:266` applies at its own door),
  `TestToolsListNamesBothSetsOnce`, `TestAnExpiredTokenIsRefused`,
  `TestAToolAnswerCarriesNoCredential` (a table over every tool, asserting the answer's text
  contains none of the project's credential names).
- **Note — one escalation this task does not close, and where it is ruled to be read (2026-11-05).**
  The identity M6 verifies at the authorize endpoint is a Firebase ID token, and **Firebase accepts a
  custom sign-in token signed by ANY service account of the project, not only by the account that was
  meant to sign it** (`internal/infra/tokensigners.go:15`, the package's own comment: a token signed
  by `fugaro-scheduler` was accepted by a live project, Check 26 step 7). So the boundary behind a
  verified ID token is **who can sign as some service account of the Firebase project**, and this
  repo already carries principals that can (`internal/infra/tokensigners.go` lists them read-only
  from the IAM policy). D923's answer is that the coordinator mints no custom token of its own and
  that only exact `user:` entries are read, which is what this task's middle tests hold — **but the
  escalation itself is not a task's to fix here**: **the owner has ruled that it folds into the
  independent SECURITY review of [spec.md](spec.md) §8**, which is already the gate on this plan, so
  it is written down as a question the review reads with §8's "what a stolen Firebase token reaches"
  paragraph rather than as a finding one task closes. **UNVERIFIED here**: which identities of a
  given installation can actually sign as one of its service accounts is a fact about that project's
  IAM policy, and no hermetic test can stand in for the reading `tokensigners.go` performs live.
- **SEC:** **yes, mandatory.**
- **Depends on:** M2, M5, D933.
- **The amendment's findings, and what this task carries (2026-11-04).** **M-e and M-f**: **every tool
  maps to a scope and a tool with no mapping is refused** — the coordinator's own `report_intent` and
  `record_override` are what makes that concrete — and D933's lists are read from **a source only an
  operator can write** (the installation's own published state), never from one machine's local
  config (`internal/cli/user.go:32-34`), with **an unauthenticated refusal logged and never written
  to the database**. Its tests carry `TestAToolWithNoScopeIsRefused`,
  `TestTheListsComeFromTheInstallationsOwnState` and
  `TestAnUnauthenticatedRefusalIsNotWrittenToTheDatabase`.

### M8 — The read tools

- **Files (5):** `ee/coordinator/mcpauth/tool_status.go`, `tool_status_test.go`,
  `ee/coordinator/mcpauth/tool_logs.go`, `tool_logs_test.go`, `ee/coordinator/mcpauth/tool_list.go`.
- **Interface:** `get_task_status`, `stream_logs` (from the run's own recorded artefacts, D932),
  `get_result`, `list_tasks`, each reading through `internal/runstore` and the runs bucket prefix the
  project already owns.
- **Red step:** `TestListTasksNamesOnlyTheProjectsRuns` — a run belonging to another project is
  absent from the answer, and the test fails because the tool does not exist.
- **Tests:** `TestListTasksNamesOnlyTheProjectsRuns`, `TestStatusOfAnUnknownRunIsRefused`,
  `TestLogsComeFromTheRunsOwnArtefacts`, `TestResultNeverQuotesAToken`.
- **SEC:** yes — read reach.
- **Depends on:** M7.
- **The amendment's finding (2026-11-04, M-g).** The artefacts are the run's own session transcript
  (`internal/runner/session.go:185`), and **a transcript can hold a credential the agent printed** —
  a log line, an echoed environment, a command it ran. The front door serves **the progress record
  redacted through `internal/safetext`** (`internal/safetext/safetext.go:17`) and **never the raw
  file**: `TestLogsAreRedactedAndTheRawTranscriptIsNeverServed`.

### M9 — `spawn_task`, `cancel_task`, and the narrowed ceiling — **DEFERRED (D936)**
**Out of the first slice (D936).** The first slice is **the authorization server plus the read
tools**, and **M9 leaves the first slice**: it is the task that starts a run from the front door, and
it needs three privileges — `signJwt`, write on the runs bucket, and
`run.jobs.runWithOverrides`, which can run any command beside the job's mounted credentials. **What a
later `spawn_task` design item has to answer is written in §1 and §7 below**, and **nothing in the
first slice starts a run, holds the launch grant or changes `internal/runner`**. The task is written
here, unchanged, as the plan for when it is designed and the owner approves it.

- **Files (8):** `ee/coordinator/mcpauth/tool_spawn.go`, `tool_spawn_test.go`,
  `ee/coordinator/mcpauth/tool_cancel.go`, `tool_cancel_test.go`,
  `ee/coordinator/mcpauth/launch.go`, `launch_test.go`, `internal/runner/spend.go`,
  `internal/runner/spend_test.go`.
- **Interface:** **`spawn_task`** (D931's answer of 2026-11-04 — the owner's name for the
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
  than a convenience. `cancel_task` writes the marker `fugaro cancel` writes.
- **Red step:** `TestTokenCeilingOnlyNarrowsTheRunsOwn` in `internal/runner/spend_test.go` — a run
  whose environment carries a token ceiling **above** the job's own resolves to the job's own, and
  one below resolves to the token's. It fails before the variable exists.
- **Tests:** `TestTokenCeilingOnlyNarrowsTheRunsOwn`,
  `TestSpawnRefusesAModeOutsideTheTokensWindow`, `TestSpawnRefusesHotfixWithoutTheFlag`,
  `TestSpawnedRunCarriesTheTokenCeiling`, `TestSpawnNeverMountsACredential`,
  `TestSpawnTakesTheLaunchClaim` (a second submit of the same task answers about the run already in
  flight rather than starting a second), `TestSpawnRunsTheBudgetPrecheck` (a kill switch that refuses
  `fugaro run` refuses `spawn_task`), `TestCancelOfAnotherProjectsRunIsRefused`.
- **A finding this task must close, and it is not optional for the task that spends money (2026-11-05).**
  **`tighterSpend` in `ee/coordinator/mcpauth/limits.go` treats `+Inf` as a value and not as a
  non-number** (`ee/coordinator/mcpauth/limits.go:298`): the helper reads a spend limit as unset when
  `!(v > 0)`, which catches 0, a negative and NaN — and `+Inf`, which passes `> 0`, is returned as
  the smaller of the two sides whenever the other side is unset, because an unset side passes the
  other through. So a token minted with `spend_usd: +Inf` against a project policy that names no cap
  resolves to a ceiling of `+Inf`, and M9 is the task that turns that ceiling into the run's own —
  **which makes this finding M9's to close rather than M1's to have avoided**: the mint-time check
  (`Limits.Validate`) refuses no spend value outside the three the model can be asked for, and D924's
  "only narrows" holds arithmetically while admitting a limit that is not a limit. M9 closes it at
  the door it owns — a spend limit that is not a finite, non-negative number is refused where a limit
  is read, or where the run's ceiling is resolved, and `TestTokenCeilingOnlyNarrowsTheRunsOwn` gains
  the row that says so: **a `+Inf` token ceiling is a named refusal, never the run's ceiling.** An
  earlier task that fixes it in `limits.go` takes M9's finding with it, and either way the fix lands
  **with a test**, because an unbounded ceiling is the one failure this design cannot see in a log.
- **SEC:** **yes, mandatory.** It adds the launch grant, and it is the one task that spends money.
- **Depends on:** M4 (the grant **and the store it writes its records into**), M7, D924, and the
  launch-override fence of [reconciliation.md](reconciliation.md) §6 (check 0), which is NOT BUILT —
  the privilege this task asks for is a launcher's, and §8 of [spec.md](spec.md) says what an unfenced
  environment means until the fence exists.
- **Hand-off:** `internal/runner/spend.go` is a core file and **every release's** runner work has
  other writers on it — the 0.15.0 window that made this a hand-off has shipped, and the property it
  named has not gone away. This task belongs to a release where the file has no other writer, and its
  planner must say so before it starts (§4, §5).

### M10 — The audit record, revocation, and what an operator sees

- **Files (6):** `ee/coordinator/mcpauth/audit.go`, `audit_test.go`,
  `ee/coordinator/mcpauth/tool_revoke.go`, `tool_revoke_test.go`,
  `ee/coordinator/mcpauth/audit_record.go`, `audit_record_test.go`.
- **Interface:** one structured line per call and per refusal, naming the identity, the tool, the
  scopes and the outcome and never a value, on `internal/credsvc/audit.go`'s pattern; a bounded
  per-identity record an operator can read; and revocation reachable from the API and from a
  person's own surface. **The record is written into the store of D935, through M2's `Store`** — the
  store of D935 is the only place it is at rest, and M3's node is the second barrier and not the
  home. Writing it into that node instead would put the audit back behind the database admin grant
  D935 exists to get away from, and would leave it readable by everyone the ruling found the rules
  cannot exclude. So "an operator can read it" is the **tool surface's** rule alone (D933): an
  operator reads the record through a tool, a launcher does not read it at all, and no caller of any
  kind reaches the raw objects — the store's IAM and the tool's rule agree here rather than parting
  ([spec.md](spec.md) §4, §6 and §8). **Revocation is still one delete** (D930): one generation-
  matched delete in the store, and the next call with the revoked token is refused — the shape a
  bucket keeps and a transaction used to give for free.
- **Red step:** `TestEveryRefusalWritesOneLineNamingNoValue` — the audit line for a refusal whose
  token is a recognizable marker string contains neither the marker nor its first eight characters.
  It fails because there is no audit writer.
- **Tests:** `TestEveryRefusalWritesOneLineNamingNoValue`, `TestAuditRecordIsBounded`,
  `TestRevokedFamilyIsRefusedOnTheNextCall`, `TestOperatorSeesTheRecordAndALauncherDoesNot`,
  `TestTheRecordIsNotInTheProjectsDatabase` (the record the tools serve comes out of the store, and
  the same read against the `mcpauth` node returns nothing — the assertion that fails the day a
  later task decides to write the audit where it used to live).
- **SEC:** yes — it decides who can read the audit record and what revocation destroys, and both are
  now decided by an IAM binding rather than by a rule.
- **Depends on:** M5, M7.
- **The amendment's findings (2026-11-04).** The record is written **into the store of D935** through
  M2's `Store` and **not** into the `mcpauth` node, which is the tripwire; a client's chosen name is
  shown **marked unverified** (M5); and the bounded per-identity record **can be flushed by spam**,
  so **the Cloud Logging line is the durable copy** and the per-identity record is the readable
  view — `TestTheLoggingLineIsTheDurableCopy` is the row that says so.

## 4. The shared-files table

Files more than one task touches, with the level each writer is in (§2). **No level has two writers of
one file** — that is what the level column is for, and it is why four tasks that share one registry sit
four levels apart. A task that finds one of these already changed reads the change before writing, and
a planner who finds two tasks in the same row in one release serialises them.

**The deferred slice changes this table rather than adding to it (D936)**: M9 is in rows it shares
with M1, M2, M7 and M10, so a planner who takes the first slice alone takes none of them, and what
leaves the first slice is M9's own work and the half of M10's that names a spawned or cancelled run.

| File | Writers (level) | What each does to it |
| --- | --- | --- |
| `ee/coordinator/mcpauth/limits.go` | M1 (L0), M9 (L5) | M1 defines `Limits` and `Narrow`; M9 reads them at submit |
| `ee/coordinator/mcpauth/token.go` | M1 (L0), M2 (L1) | M1 defines the token's shape; M2 mints it |
| `ee/coordinator/mcpauth/store.go` | M2 (L1), M5 (L2), M10 (L6) | M2 defines the store; M5 writes through it; M10 revokes through it |
| `ee/coordinator/mcpauth/server.go` | M5 (L2), M7 (L3), M6 (L4) | M5 mounts the five endpoints; M7 adds the resource handler; M6 adds the authorize endpoint's verifier, off the critical path and therefore in the last of the three slots |
| `ee/coordinator/mcpauth/tools.go` | M7 (L3), M8 (L4), M9 (L5), M10 (L6) | the registry: each of the three tool tasks registers into it |
| `ee/coordinator/service.go` | M4 (L0), M5 (L2), M7 (L3) | M4 creates the service's entry point; M5 and M7 mount handlers on it |
| `deploy/terraform/gcp/modules/installation/coordinator.tf` | M4 (L0), M9 (L5) | M4 creates the service and the account; M9 adds the launch grant |
| `deploy/terraform/gcp/modules/installation/outputs.tf` | M4 (L0), M9 (L5) | the service's URL and the store's bucket, then the grant |
| `internal/runner/spend.go` | M9 (L5) alone, but **shared with every other runner task in its release** | one variable and its merge; see §5 — which is why the release slot is chosen for the file's other writers, not only for this work |
| `SECURITY.md` | M4 (L0), M9 (L5), M10 (L6) | a public endpoint, the store and the single grant that keeps it private, a launch grant, and what the audit does not record — **the repository's standing rule is that a security-relevant change updates it in the same PR** |

| `ee/coordinator/mcpauth/authorize.go` | M5 (L2), M6 (L4) | M5 defines the authorize and approve steps; M6 verifies the ID token the approve step carries |

**Files deliberately not in this table**, because one writer is not a shared file and a row for one
would read as a conflict: `deploy/terraform/gcp/modules/installation/mcpauth_store.tf` and
`internal/infra/tf/cover.go` are M4's alone, `ee/coordinator/mcpauth/store_bucket.go` is M2's alone,
and `internal/budget/rules/*` is M3's alone — the last one being the reason M3 can land at any point
in the schedule rather than at a level.

## 5. The release slot

**The implementation targets 0.21.0 "Rhino" or later.** The design's own window is long spent:
**0.15.0, 0.16.0, 0.17.0, 0.18.0, 0.19.0 and 0.20.0 have all shipped** since this folder was
written (`docs/release-streams.yaml`'s cli stream: `current: "0.20.3"`, `next: Rhino`, so **0.21.0
is Rhino**). This is a re-slot, not a preference: the reasons the first version of this section gave
were right about *why* the work needs a window of its own and wrong only about *which* — and the
amendment's own cut moves it again, for a reason of its own.

Why the implementation is **0.21.0 or later**, and not the next minor:

1. **The coordinator's own front door is still not built.** P7 and P8 still wait on this design
   (`docs/coordinator/README.md`:26), so the service M4 creates and the account M5 runs on do not
   exist yet. A release cannot carry an MCP endpoint on a service the same release is still
   designing.
2. **The work is big and security-heavy, and the owner's own instruction of 2026-11-04 is that it
   takes a slot of its own for that reason.** Ten tasks, **nine of them SEC-marked and five of those
   mandatory**, a public endpoint, a new service account, **a new store with one IAM binding on it**
   (M4, D935) and **one new privilege** (M9's launch grant), behind an independent review whose first
   finding (F32) has already moved where the token records live. A window that is not already carrying
   another feature of this shape is the only one it fits in whole.
3. **M9 is a core change to a contention-prone file.** `internal/runner/spend.go` is core, and every
   runner task in whatever release carries M9 shares it (§4). Landing it in a full window means
   either a declared single writer is overridden or two changes are serialised by hand; neither is
   worth it for a feature whose first dependency is unbuilt.
4. **The update has to carry it, and the update is now gated.** The repository's standing rule is
   that a release ships no operator step beyond `fugaro update`, and M4's service and M9's grant are
   both Terraform that the update applies. Since this folder was written, **managed mode landed** and
   the gate covers `fugaro update` itself (`internal/cli/managed_gate.go`,
   `docs/managed-agent-converge/README.md`:20), so the migration this task carries — with the
   convergence test that an installation at the previous version ends with exactly M4's resources,
   **the store and its one grant included** — runs through the gate like every other managed step.
   That is a heavier migration than it was, and it belongs in a release that is not already carrying
   the same work for three other features.

**What the deferral buys, and what it does not (D936).** **The first slice is smaller than the
whole**: it starts no run, asks for **no new privilege**, changes **nothing under
`internal/runner`**, and adds no grant beyond the store's own one — so it is a candidate for an
earlier window than the one above, and this section does not choose that for the owner. A planner who
takes it into an earlier release has to say in the release notes that **the front door is read-only
until M9 ships**, which is a sentence the owner should read before it is written.

**What would move it earlier:** if the owner answers D920 the other way (P8's IAM invoker), M5's
OAuth half disappears, and M1–M3 plus a small M7 fit into whatever release is next. Nothing else
here is small enough to pull forward.

## 6. Test rules for whoever implements this

- **Hermetic, always.** No live project, no real Firebase, no real Cloud Logging: `internal/gcpfake`,
  `internal/testutil`, `t.TempDir()`. The rules tests use the package's own emulator harness; the
  store's own tests use the GCS fake, whose conditional writes are the generation-matched ones a
  bucket-backed store stands on (`internal/gcpfake/gcs.go:34`), so M2 is provable without a bucket.
  **Two things this plan cannot prove hermetically, and both are owner checks in
  [reconciliation.md](reconciliation.md) §6:** the public reachability of M4's service (check 2), and
  **who the store's IAM actually admits (check 8)** — a plan-level test reads the plan, and the plan
  is not the project's policy. **The second is the one the correction of 2026-11-05 turned on**: a
  name and a binding can be read off the plan, and whether *another* identity — an operator acting as
  the deployer — reaches the store is a fact about the project's IAM and can only be read there.
- **The amendment's own rule (2026-11-04).** **No real Secret Manager either**: the pepper D922's
  amendment puts in Secret Manager is a **marker pepper** in every test and fixture, so a store whose
  tests need a live project, or a fixture carrying key material, is a store this repository cannot
  merge. **A citation the amendment adds is a citation to `main`**: every path and line this folder
  cites is checked by `internal/cli`'s docs tests, and the amendment's new ones were verified at the
  commit the README names; a task that finds a line moved re-verifies it rather than moving the
  number.
- **Named tests, never whole packages.** `internal/cli` and `internal/runner` are never run whole;
  every command is `GOFLAGS=-p=2 go test ./<package>/ -run 'Name|Name' -count=1`.
- **A test that reads a changed file runs.** For every file a task touches, the implementing run
  greps the test tree for its base name and runs each hit — the wider pin gate this repository
  applies to every change.
- **No secret-shaped literal, in a test or a fixture.** A token in a test is a marker string that
  cannot be mistaken for a real one, and no fixture contains key material.

## 7. What this plan does not own

- **The coordinator's P7 and P8.** D196 already put the front door's home in the coordinator's own
  service; this plan builds the MCP half of it and nothing else. P7's ID-token minting, its per-person
  invoker grants and its IAM checks are the coordinator work's, and M4 must not silently take them
  over — where they overlap, the coordinator's plan wins and this one adapts.
- **The console's graph write.** D927 keeps `roadmap:write` out of v1; GV3.1 and GV3.2 stay blocked
  and that work comes back to ask.
- **The central-orchestrator plan.** D196 answered that the coordinator's service is its own; the
  orchestrator reaches it as a client like any other, and this plan adds nothing to it.
- **The stateless-orchestrator spec** (open as **#1682**, not on `main` when this was written). Its
  **state tool group** is hosted here: D931's registry serves the coordinator's own tools on the
  same endpoint under their existing names, so the state group reaches MCP clients through this
  front door and this plan builds **no second surface** for it. Which names that group carries, and
  whether any of them needs a scope of its own, is that design's to decide — D927 keeps v1 to two
  scopes, and a tool the two do not cover brings the scope question back as D927's. M7 is the task
  the registry lives in, so it is the one that changes if the group's names arrive after this plan.
- **The managed gate's decisions.** The gate landed while this design was open
  (`internal/cli/managed_gate.go`, `docs/managed-agent-converge/`), and M4's operator path runs
  through it: `fugaro update` is one of the commands the gate covers, so the front door's switch is
  admitted or refused by the gate's own rules and this plan adds **no row** to it. A new command that
  the gate should cover is a managed-mode change, not this work's — and M4's `#### For operators`
  block already routes the switch through `fugaro update` rather than through a second command, which
  is the same rule the gate exists to enforce.
- **The later `spawn_task` design item.** **D936** defers it out of the first slice, and §3's M9
  keeps the shape and the four requirements the deferral decided; **how a run is started from outside
  the CLI is not this plan's to write**, and it is not a task here until it is designed and the owner
  approves it. Anything that starts a run from the front door before then is a finding.
- **The workspaces exchange.** `docs/workspaces` (a parallel run's amendment, merged as **#1711**)
  requires a **member-bound assertion and an exchange at `/oauth/authorize` that keeps the
  paired-device approval**, so its exchange and M5's authorize step meet at one endpoint; **this plan
  does not own
  that decision and does not edit that folder** — [spec.md](spec.md) §10 states the dependency and
  the **identity mismatch** (a workspace identity is a verified email; this front door's is a device
  uid read back to an email), and it stays a dependency until the workspaces work lands.

- **A local stdio MCP server.** The owner's non-goal, and this plan does not creep into it.
- **Any change to `SECURITY.md`, `README.md`, `website/**`, `plugin/**` or `.github/workflows/**`
  made by the run that writes *this* document** — those belong to other works, and the run that
  wrote this folder touched `docs/` and its own test and nothing else. The implementation tasks of §3
  may touch `SECURITY.md` when they run, because by then the change is theirs and the repository's
  standing rule is that a security-relevant change updates it in the same PR (§4).

## 8. The store the records move to (D935): the three candidates, and the answer

**This is the one thing D935 left open, and the owner answered it on 2026-11-05: the third
candidate, the bucket of its own — `fugaro-mcpauth-<gcp-project>`, `mcpauth/` inside it, one IAM
binding for the front door's own account `mcp_server` and nobody else.** **The answer is the
recommendation**, and the one-line why is D935's own ruling read onto the three rows below: D935
answered **"a store of their own"**, and the bucket is the only candidate whose IAM can name
**exactly one account** — the second Realtime Database instance inherits the project-level database
grants until four sets of people are re-scoped, and Firestore's data roles cannot be narrowed to a
database or a collection at all, so those two are the finding again one resource over rather than
narrower stores. **M2 and M4 are written to this answer and wait on nothing** (§3). All three
candidates are
"a store of their own whose IAM names the front door's own account"; they differ in **what that IAM can
name**, which is the whole question, and in what the store costs to write against. The record shapes,
the rule that only hashes are stored, D930's rotation-and-replay revocation and D930's one-delete
revocation are identical in all three. **No price appears anywhere in this section**, because none appears
anywhere in this folder ([README.md](README.md), "Names and figures"): "what it costs" is work and
risk, and never money.

| | A second Realtime Database instance | Firestore | **A bucket of its own, prefix `mcpauth/`** — **the answer of 2026-11-05** |
| --- | --- | --- | --- |
| **What it is** | a new `google_firebasedatabase_instance`, its own rules publish in the same `update`, its URL in the coordinator's configuration | a `mcpauth` collection in the project's existing database, or a named database of its own | a new bucket (`fugaro-mcpauth-<gcp-project>`, **outside every conditioned prefix**) with every record under `mcpauth/` |
| **What it costs** | **the most, and it does not work on its own.** The project-level bindings are inherited by every instance, so the new one is readable by every launcher and operator and writable by every admin, budget admin and the history account **until those three bindings are re-scoped** from the project to the default instance alone (`deploy/terraform/gcp/modules/firebase/iam.tf:44`, `:53`, `:81`) — a migration of the access every launcher already uses for `fugaro watch` and `budget show` (`:43`), in the release that also ships a public endpoint. The store code is the closest to what M2 first planned (the database client, key paths), which is the one thing in its favour | **the smallest to add and the one that cannot be made private at all.** `roles/datastore.viewer` is granted project-wide to `local.people ∪ local.admins` (`deploy/terraform/gcp/modules/firebase/iam.tf:65`) and the history account holds `roles/datastore.user` (`:96`); Firestore's data roles have **no per-database and no per-collection narrowing** to re-scope to, so a named database is reached by the same project-level binding as the default one. Getting out would mean withdrawing `datastore.viewer` from the people lists — which takes the spend history's own read with it — and the spend history is not this work's to break | **no transactions and no query language.** The store is objects, written with `internal/blobx`'s generation-matched primitives (`internal/blobx/blobx.go:1`), so the audit record is a prefix listing rather than a query, and each of rotate, replay-detect and revoke is a compare-and-swap that a test has to prove rather than a transaction that gives it. Everything else is a seam the repository already has, and the GCS fake implements the one precondition the store needs (`internal/gcpfake/gcs.go:34`) |
| **The new Terraform** | one `google_firebasedatabase_instance`, plus the rules publish to it, plus **the re-scoping of the three project-level bindings above**, which is the part that touches what exists | one `google_firestore_database` for a named database, and **none at all** for a collection in the existing one | one `google_storage_bucket` — uniform bucket-level access and public access prevention enforced, because the installer's plan review refuses a bucket without both (`internal/infra/tf/cover.go:2199`) — and one `google_storage_bucket_iam_member`. Both types are already covered (`internal/infra/tf/cover.go:64`), and `fugaro update` applies them with the owner's own rights, so **no new operator permission and no second command**. **The name must not carry `fmgd-`**: the managed deployer's storage role is a project-level binding conditioned on that prefix (`deploy/terraform/gcp/modules/installation/deployer.tf:379`, `:142`) and every operator can act as the deployer (`:501`), so a prefixed name would put the store inside exactly the reach this option exists to leave — the correction of 2026-11-05, and the reason M4's test asserts the name. **And the name is squatting-shaped**: bucket names are global and this one is a function of the project alone, while the deployer holds an **unconditioned** `storage.buckets.create` (`deployer.tf:219`), so M4 **creates** the bucket and a create against a name that already exists **fails the apply** — never adopted, never imported, never read (M2 opens only the name M4 writes into the installation's outputs) |
| **The IAM binding it needs** | **none that is narrow.** There is no per-instance binding in the shape the rest of this installation uses: the only way to keep the four sets of people out is to stop granting them project-wide, which is the re-scoping above — and that changes what a launcher has today | **not available.** The binding cannot be narrowed to the records, so the store is readable by everyone `datastore.viewer` names and writable by the history account. This is the row that rules Firestore out rather than pricing it | **one binding, one member:** `roles/storage.objectAdmin` for **the front door's own account `mcp_server`** on that bucket, and nothing else — **not the sweep's account** (`google_service_account.coordinator`), which reads runs and never tokens. Every storage grant in this installation **but one** is bucket-scoped (`deploy/terraform/gcp/modules/installation/iam.tf:207`, `:222`, `:238`; `coordinator.tf:92`) — the exception is the managed deployer's conditioned `fugaroDeployerStorage` (`deploy/terraform/gcp/modules/installation/deployer.tf:379`), which is why the store's name stays outside `fmgd-` and out of its reach — so a bucket so named has exactly one **member**. **What is wider than the member list is the floor, and the plan no longer claims otherwise (corrected 2026-11-05):** the project's **Owner and its Editors reach every bucket through their project-level roles** — `SECURITY.md:88` says it of this installation's own buckets, "Project Owners, Editors and anyone with a project-level storage role bypass the bucket's own policy regardless, hardened or not" — and **GCS's convenience bindings give a basic project Viewer the read** until they are taken off the bucket (`internal/gcpfake/gcs.go:250`). Uniform bucket-level access removes neither, so **M4 owns stripping them or refusing the bucket** (M4's fifth bullet, and the reading check 8 adds). What the floor is not: it is not the four sets of people the database grants name — no launcher, no operator, no budget admin and not the history account is admitted by a bucket claim — and that is the whole distance D935 bought. **A prefix of the runs bucket is not this option:** every launcher reads that whole bucket (`deploy/terraform/gcp/modules/installation/iam.tf:222`) and every operator holds `objectAdmin` on it (`:207`), so the records would be readable by both lists — the finding F32 is, one resource over |
| **The owner-run live check** | after the re-scoping: read the new instance's `mcpauth` node as a launcher and as a budget admin and expect a refusal — **and then confirm `fugaro watch` and `fugaro report` still work**, which is the half of this option that can break an installation | the same read, which **fails**: the launcher's own credentials reach the documents. The check is what turns this row from a preference into a fact | with a launcher's own credentials and with a budget admin's, read and list the store's prefix and expect a refusal; **impersonate the deployer as an operator and expect a refusal too** — the reading the correction of 2026-11-05 adds; **read and list the prefix as a basic project Viewer and expect a refusal as well** — the reading the floor correction of the same day adds, and the only one that shows the convenience bindings were actually stripped rather than only written away; with the front door's own account `mcp_server`, read one record and expect it to work; and confirm the store's bucket is not the runs bucket (check 8, [reconciliation.md](reconciliation.md) §6) |

**Why the recommendation is the bucket.** The first candidate's IAM cannot be narrowed without
re-scoping grants four sets of people already hold, which is a bigger change to a working
installation than the store itself; the second's cannot be narrowed at all, so it is not a candidate
— it is the finding again, one resource over. The third is the only one whose IAM **can be exactly one
account**, which is the sentence D935 answered with, and the one thing the ruling actually requires.
**Getting there cost the first version of this section a mistake worth recording**: it named the
bucket under the managed prefix `fmgd-` so the deployer's conditioned role would reach it, which put
the store inside the reach of every operator (through the deployer's own account) and of every
managed apply — F32 again, one resource over, and found by this work's own review. A name outside
that prefix, with the owner's `update` doing the creating, is what makes the sentence true rather
than nearly true. What it gives up is transactions, and the repository already has the primitives for
the two places a
transaction would otherwise be doing the work (`internal/blobx`, and the GCS fake's generation
precondition), which is a smaller risk than either of the other two rows' IAM.

**The Open item, ANSWERED 2026-11-05 — and this is the record of the answer rather than a question
left for a reader to chase.** **The owner answered which of the three: the third, the bucket of its
own**, which is the recommendation (§3, and [reconciliation.md](reconciliation.md) §4's D935 row and
its §6 check 8). **The one-line why**: D935's ruling was **"a store of their own"**, and among the
three candidates the bucket is the only one whose IAM can name **exactly one account** — the second
instance inherits the project-level database grants and Firestore's data roles cannot be narrowed to
a database or a collection at all. **What the answer closes**: M2 and M4 no longer wait on anything
(they were the only two that did), §2's level table and critical path no longer carry the gate, and
**nothing else in this plan ever waited on it**, because every other task builds against M2's
`Store` interface and not against a store. **What the answer does not close**, and this section is
where a reader finds it: **the floor under the bucket** — the Owner's and an Editor's project-level
roles, which no bucket policy narrows — and **the convenience bindings GCS attaches to a new
bucket**, which M4 owns stripping (M4's fifth bullet).

**What the store adds to the live check** ([reconciliation.md](reconciliation.md) §6, check 8, which
is owner-run like the rest): readings no hermetic test can stand in for — a launcher refused, a
budget admin refused, **an operator acting as the deployer refused** (the reading the correction of
2026-11-05 adds, and the one that proves the name is out of the conditioned role's reach rather than
only written to be), **a basic project Viewer refused** (the reading the floor correction of the same
day adds, and the one that proves the convenience bindings were stripped rather than only written
away), and the front door's own account `mcp_server` admitted — plus the confirmation that the
store's bucket is not the runs bucket. It is check 2's shape for a second resource: the claim is about
who reaches something, and only the project can answer it.

**What the store adds to `### For operators`.** One resource and one binding, in the same
`fugaro update` that already creates the service, its account and its invoker binding — **no second
command, no `gcloud` line, no per-store flag, and no opt-in of its own** (§3, M4). That is the HARD
RULE read at this section: the store's whole operator story is the one line the front door already
carries, and an owner who never passes `--mcp-server` never acquires either the store or its grant.

**What this section does not decide.** Nothing else. The record shapes belong to M2, the rule that
only hashes are at rest belongs to D922, rotation and revocation belong to D930, and who may read the
audit record belongs to D933 — none of them moves with the store, and a question any of them raises is
a question for its own decision rather than a fourth candidate here.
