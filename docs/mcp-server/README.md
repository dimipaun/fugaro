# The hosted MCP server

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** The owner's spec of 2026-10-24 is copied verbatim into [spec.md](spec.md) §0 and is
reconciled against the code here; [reconciliation.md](reconciliation.md) §4 puts fifteen decisions
(D920–D934) in front of the owner, each with the recommendation first and the cost of the
alternative — and **all fifteen were answered on 2026-11-04**, **every one of them the
recommendation**, with the owner's own detail written into the design (D922's was restated the same
day: the first answer opened the subtree and the restatement closes it). **A sixteenth answer arrived
on 2026-11-05 (D935), from the independent review of PR #1715's M3**: the `mcpauth` records leave the
project's shared Realtime Database for a store whose IAM names only the front door's own account
`mcp_server`, because the database's own grants are project-level and no rule can keep a launcher out
([reconciliation.md](reconciliation.md) §3 F32, §4 D935) — and **which store, the one thing D935
carried over open, was answered by the owner the same day: the bucket the plan's §8 recommends**.
**If the implementation happens, [implementation-plan.md](implementation-plan.md) §5 recommends
0.21.0 "Rhino" or later** — 0.15.0,
0.16.0, 0.17.0, 0.18.0, 0.19.0 and 0.20.0 have all shipped since this design was written, and §5 says
why the work is big and security-heavy enough to want a window of its own.

**Amended 2026-11-04, after the mandatory independent security review.** The design merged as
**#1487** *before* the SECURITY review of [spec.md](spec.md) §8 that this folder requires; that
review ran on **2026-11-04**, **the coordinator ran it**, and the owner answered its findings the
same day. **Every answer is recorded in [reconciliation.md](reconciliation.md) §4a as a dated
amendment note beside the decision it names** — the original answer stays and **no number is reused
for a new meaning** — and the one finding that is a decision of its own took the next free number in
this folder's block, **D936** (§4b); the other, **H3**, is the same defect the independent review of
PR #1715's M3 found from the other end, and its answer is **D935** above. **One amendment is not the
recommendation**: **H1**, where the owner **accepted and documented** the paired-device residual risk
instead of fixing it. The findings and what each changed, in one line each:

| Finding | What the review found | The owner's answer | Landed in |
| --- | --- | --- | --- |
| **H1** | A device token is a custom token, and **any launcher can mint one** and approve a sign-in as an operator's device — so the approval proves nothing against a launcher | **ACCEPT AND DOCUMENT, not the recommendation**: the residual risk stands with its consequences (operator scope, forged audit attribution, a rotating refresh family that outlives the ID token's hour), and the change that would close it is named and **not built** | spec.md §3 step 5, §8, §11; D923's amendment |
| **H2** | `spawn_task` needs **three** privileges, not one — `signJwt`, write on the runs bucket, and `run.jobs.runWithOverrides`, which can override container args and so run any command beside the job's credentials | **DEFER `spawn_task` out of the first slice.** The first slice is the authorization server plus the **read tools**; what a later design item must answer is written down | **D936**; spec.md §5, §8; the plan's order, scope and release slot |
| **H3** | **IAM bypasses the database rules**: launchers and operators can read the token records, admins can write them, and the public service would hold `firebasedatabase.admin` over the whole database | **Keyed hashes and MACs** where the records live now, **no database admin on the public service**, and the records **out of the main database** — **D935** states where they live and who may reach them | **D935** (the answer of 2026-11-05, F32); D922's amendment; spec.md §6, §8 |
| **M-a** | The "loopback is the attacker's machine" argument **fails when approval happens on a phone** — cross-device phishing | QR proximity or number matching; the phone shows the client (**marked unverified**) and the redirect host; **a single-use POST**, an ID token never in a GET URL, nothing pushed unstarted | D923's amendment; spec.md §3, §8; M5, M6 |
| **M-b** | The family spend limit was not enforced across **concurrent** runs | Reserve `min(job cap, remaining)` **atomically against the family counter** at spawn, settle at finalize; for `client_credentials` the family is the client | D924's amendment; spec.md §6, §8; M2, M9 (deferred) |
| **M-c** | The library's defaults contradict the design (PKCE optional, empty challenge = `plain`, unknown scopes dropped, extra grants, a `SigningKey` with no home, non-RFC-8414 discovery) | Refuse a missing or non-`S256` challenge and an unknown scope **before the library**; only the code and refresh grants; opaque tokens or the key in Secret Manager; **the M5 gate file is the owner's to create** | D921's amendment; spec.md §1, §3, §6; M5 |
| **M-d** | Refresh rotation is a **check-then-write race** | Condition the rotation on the **ETag of the same read**, with a small grace window | D930's amendment; spec.md §6; M2 |
| **M-e** | `allUsers` makes **every route** public, including P8's `report_intent`/`record_override` | Every tool maps to a scope and an unmapped tool is refused; **"off" means no `allUsers` binding**; cap `max-instances` and the body size; log unauthenticated refusals and never write them to the database; **M4 must not widen `tf.Cover`** | D920's and D934's amendments; spec.md §2, §5, §8; M4, M7 |
| **M-f** | Where D933's lists are read from was unspecified, and **nothing owned client pre-registration** | Read a source **only an operator can write**; **refuse pre-registration inside an agent session**; keep `client_credentials` **off until it has an owner** | D929's and D933's amendments; spec.md §4, §7; M5, M7 |
| **M-g** | `stream_logs`/`get_result` read raw `session/*.jsonl`, which can hold a credential the agent printed | Serve the progress record **redacted through `internal/safetext`** | D932's amendment; spec.md §8, §9; M8 |
| **Low** | M1's `SpendUSD float64` cannot tell unset from zero — and `+Inf` passes the same test, which the review of 2026-11-05 found at `ee/coordinator/mcpauth/limits.go:298` | Optional **integer micros** and unknown modes refused; the client shown **unverified**; **Cloud Logging is the durable copy** | M1 (as written) and **M9** for the ceiling (deferred); M5, M10 |
| **Workspaces** | `docs/workspaces` (a parallel run's amendment) now requires a member-bound assertion and an exchange at `/oauth/authorize` that keeps the paired-device approval | The dependency and the **identity mismatch** (a verified email against a device uid read back to an email) are noted; **this folder does not edit `docs/workspaces`** | spec.md §10; [reconciliation.md](reconciliation.md) §6 check 9 |

**Two tasks were cancelled by the amendment and will be rerun from the amended plan**: **M4** (its
draft predates the D934/M-e answers) and **M6** (its predates M-a and H1). **M1 and M3 may
continue** — M1's spend-type finding is **M9's to close**, which is where the review of 2026-11-05
found the same defect (`ee/coordinator/mcpauth/limits.go:298`) — and **nothing else started**.

**Asked by.** The owner, on 2026-10-24: a hosted MCP server inside the coordinator, with OAuth 2.1
and PKCE and the project's own Firebase as the identity provider, so that MCP clients can submit and
manage tasks without the CLI. The owner approved a design PR on 2026-10-31 and **answered the fifteen
decisions on 2026-11-04** — D920's public endpoint, D923's paired-device sign-in and its `user:`-only
lists, D930's rotating tokens, D931's single launch path, D934's off-by-default front door, D921's
library **with its licence still UNCONFIRMED**, and D922's separate **`mcpauth`** subtree, closed to
every client. **And answered one more on 2026-11-05 (D935)**, on the independent review of PR
#1715's M3: those records do not live in the project's shared Realtime Database at all, but in a
store whose IAM names only the front door's own account `mcp_server` — the bucket of the plan's §8,
which is what the owner's answer of the same day picked out of the three candidates.

**Reconciled against.** `origin/main` at `3d6ed04d`, read on 2026-11-01, **re-read on 2026-11-04 at
`b41709ea`**, and **re-verified again on 2026-11-04 at `5998d67c`** for the amendment round — every
citation the review's findings touched was checked against that commit, and the two owner-check and
amendment citations the review supplied (the `zitadel/oidc` paths, and the parallel `docs/workspaces`
amendment) are the ones marked **UNVERIFIED** because this container cannot read them. The first
reading was taken with the seventeen pull requests open then — most closely #1472
(`docs/cloud-senior-review`, D880–D899), #1481 (`docs/run-without-checkout`, D901–D919) and #1483
(`internal/rigor/rounds.go`). Every citation in [reconciliation.md](reconciliation.md) is a path and a
line at the latest commit, and its §7 records the eight things `main` had overtaken in between:
**managed mode, the person-class reads, `settings`/`rigor`/`accounts`, the project image catalog and
#1676, `fugaro upgrade`, the CLI stream moving to 0.20.3, `stray.go`, and the paired device** (`fugaro pair` and the
device allowlist, which is what D923's answer signs a person in with).

**SECURITY-SENSITIVE.** Two things about this folder are not ordinary design text:

- **[spec.md](spec.md) §8 is a threat model**, and it is the section the owner asked for by name: who
  can call the front door, what a stolen access token, a stolen refresh token and a stolen Firebase
  token each reach, token storage as hashes, redirect-URI abuse, open Dynamic Client Registration,
  and an unattended agent as a caller. **An independent SECURITY review of that section is
  mandatory** — and **the coordinator runs it**, not this folder's authors, exactly as it runs every
  other security-sensitive review in this repository. **That review ran on 2026-11-04**, after this
  design had merged; **its findings and the owner's answers are
  [reconciliation.md](reconciliation.md) §4a**, every document here is written to them, and **no task
  in [implementation-plan.md](implementation-plan.md) starts before the finding that names it is
  answered in the task it builds**. Its scope was §8, every **SEC**-marked task of §3 — the five
  whose marker says **mandatory** are M3, M4, M5, M7 and M9, **with M9 keeping the marker in the
  deferred slice**, and M2, M6, M8 and M10 carry the marker without that word — and the two new
  privileges this design asks for (M9's launch grant, **deferred with M9**, and M4's single binding on
  the store D935 moves the records into). **That review has already found something**: F32 in
  [reconciliation.md](reconciliation.md) §3, the answer to it recorded as D935 on 2026-11-05, and it
  is the reason the records leave the project's database. **One of its findings was accepted and
  documented rather than fixed**: **H1**, the paired-device residual risk ([spec.md](spec.md) §8).
- **The folder keeps one rule unconditionally**: **no agent handles a credential.** The coordinator
  never hands a model credential or a forge credential to an MCP caller — not as a tool result, not
  in an error, not in a log line. A run's credential reaches the run the way it reaches every run
  today: the platform mounts it into the job. [spec.md](spec.md) §9 states it as a prohibition.

## What it is

One document set, four files:

| File | What it holds |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, copied **verbatim**, as §0 — and then the design written to the owner's answers of 2026-11-04 and to the amendments that followed the security review: the front door and its transport (§2), the authorization server and its flow (§3), Firebase as the identity provider (§4), the tools (§5, with **`spawn_task` deferred** out of the first slice), tokens, rigor windows and spend limits (§6), unattended agents (§7), **the threat model (§8)**, the coordinator's prohibitions (§9), **the two other works this design is wired to (§10)**, and what the design leaves open (§11) |
| [reconciliation.md](reconciliation.md) | Each claim of §0 against the code at the base: **what exists** (F1–F15, F19), **what is missing** (F16–F18, F20–F22) and **what conflicts** (F23–F32), each with a path and a line; then the **fifteen decisions, D920–D934** (§4) with the owner's answers of 2026-11-04 under them **and the answer of 2026-11-05, D935** (the records leave the project's database); **§4a**, the independent security review of 2026-11-04 and the owner's answers to it, written as a dated amendment note beside each decision it names; **§4b**, the one finding that is a decision of its own, **D936**; the number block and how it was read (§5), the owner checks that no hermetic test can stand in for (§6), and what `main` had overtaken (§7) |
| [implementation-plan.md](implementation-plan.md) | What the owner was asked and answered (§1), the order and what forces it (§2), ten tasks **M1–M10** each with its files, its interface, its **red step**, its named tests and its SEC flag (§3) — M4 carrying its **For operators** block, **M9 deferred** out of the first slice and **M4 and M6 cancelled and to be rerun** — the **shared-files table** (§4), **the release slot** (§5), the test rules (§6), what the plan does not own (§7) and **the store the records move to (§8, D935)**, with the three candidates compared and **the answer of 2026-11-05** that picked the bucket |
| `README.md` | This file: the status, the reading of the number block, and what a reader should not take from here |

**Three works this folder is wired to.** The **stateless-orchestrator spec** (open as **#1682**, not
on `main` when this was written) gives the coordinator a **state tool group**, and **this MCP server
is what hosts it** — D931 already serves the coordinator's own tools on the same endpoint under their
existing names, so the state group reaches MCP clients through the front door rather than through a
second surface ([spec.md](spec.md) §11 says which part stays that design's to decide). The
**coordinator's own P7/P8** is the second: [reconciliation.md](reconciliation.md) F1, F24 and the
plan's
§7 keep the front door's home in the coordinator's service, and this folder builds the MCP half of it
and nothing else. The **workspaces work** (`docs/workspaces`, **amended in a parallel run**) is the
third, and it is the newest: its **member-bound assertion and its exchange at `/oauth/authorize`**,
which keeps the paired-device approval, meet this front door's authorize step at the same endpoint —
[spec.md](spec.md) §10 states the dependency and the **identity mismatch** (a workspace identity is a
verified email; this one is a device uid read back to an email), and **this folder does not edit that
one**.

## Names and figures

The owner's spec is copied **verbatim**. It names no person and no customer organisation: the only
proper nouns in it are the products and libraries it is about — the project it is written for, the
identity provider, the MCP specification's revision, and the three candidate OAuth libraries — and
those are load-bearing, so they are kept. It also contains the owner's own illustrative budget
amounts in the token-limits section; those two figures are the owner's hypothetical example and are
the **only currency figures in this folder**. The design states no price of anything.

Every repository, person and workspace this folder's own text invents is a placeholder:
`owner/repo-a`, `owner/repo-b`, `person-1`, `workspace-a`. No file here contains a credential, a key,
a token, or anything shaped like one; the tests the plan names use marker strings by rule
([implementation-plan.md](implementation-plan.md) §6).

This is core documentation, and no file in it carries a licence notice.

## The number block

Read at launch (2026-11-01) by hand and not from a neighbour's line, and **re-read on 2026-11-04 at
`b41709ea`** — the tree moved a long way in three days, and a block that was free at launch is not
free by default:

| | |
| --- | --- |
| Highest number written on `origin/main`, at launch (`3d6ed04d`) | **D900**, in `docs/rigor-command/README.md`:117 — **superseded, see below** |
| Highest number written on `origin/main`, re-read (`b41709ea`) | **D1189** — the block **D1160–D1189** is `docs/config-management/`'s (`docs/config-management/README.md`:72) |
| In the open pull requests at launch | **D899** (`docs/cloud-senior-review`, #1472); **D901–D919** by `docs/run-without-checkout` (#1481) |
| Taken above D1189 now | **D1190–D1219** #1676 (repository-image removal), **D1220–D1259** the stateless-orchestrator work, **D1260–D1289** the #1678 design, **D1290–D1309** #1492's new block |
| **This work's block** | **D920–D934** — the fifteen decisions of [reconciliation.md](reconciliation.md) §4, **kept because they do not collide**: "D920" and "D934" appear on `main` only as other works' inventories of this pull request's block (`docs/managed-agent-converge/reconciliation.md`:248, `docs/run-limit-carry-over/README.md`:54) |
| **Used on 2026-11-04** | **D936** (the first MCP slice, and `spawn_task` deferred — finding H2), from [reconciliation.md](reconciliation.md) §4b. **An amendment takes no number**: a decision's answer is amended in place, with a dated note beside it, so the number keeps one meaning |
| Taken from this work's own reservation | **D935** — the answer of 2026-11-05, written here and not on `main` before it, so the same non-collision holds |
| Reserved, unused | **D937–D939** |
| A later task takes | **D1310**, the first number above every allocation above, and re-checks the facts rather than this conclusion |

`grep -rhoE 'D[0-9]{3,4}' docs` is not quite enough on its own, for two reasons this folder records
so the next reader does not repeat the search: it returns `D1000` (a package-lock integrity
fragment, not a decision), and a concurrent work's block is not on `main` at all — another branch's
README, in its own diff, is where the block above D1189 is written.

## What a reader should not take from here

- **Not an approval of any task.** Every decision was answered on 2026-11-04, and **an answer is not
  an approval**: no task in [implementation-plan.md](implementation-plan.md) is approved, and the
  owner approves them one at a time.
- **Not a plan to start.** The independent SECURITY review of [spec.md](spec.md) §8 **has run** (the
  coordinator ran it on 2026-11-04) and its findings are answered in
  [reconciliation.md](reconciliation.md) §4a; **M4 and M6 were cancelled by those findings** and will
  be **rerun from the amended plan**, and **M1 and M3 may continue**. Something else still gates every
  task: **the M5 gate file is the owner's to create** (§6, the first owner check), and the two
  **UNVERIFIED** half of D935.
- **Not a design anyone may build against yet.** D921's library was chosen with **its licence
  UNCONFIRMED**, and the review of 2026-11-04 **could not settle it** — the Apache 2.0 claim is the
  library's own — so **the owner or counsel confirms it, the transitive dependencies included**, and
  **the gate file is the owner's to create**
  ([reconciliation.md](reconciliation.md) §6, the first owner check). A licence that turns out to be
  unusable still sends that decision back to its alternatives.
- **Not the last word on the MCP specification.** §0's open item — re-verify the current revision —
  is still an owner check ([reconciliation.md](reconciliation.md) §6): **this container cannot reach
  the public specification**, which is the limitation `docs/coordinator/reconciliation.md`:794
  recorded as U1, so the design stays written to the **2026-07-28** revision because that is the one
  the owner named.
- **Not a design with a second reader to argue about — and the review proved the point.** D922's
  answer closes the token records' subtree to every client (`.read: false`, `.write: false`, with the
  coordinator's database-admin account the one IAM identity a rule cannot bind), so
  [spec.md](spec.md) §8's token-storage paragraph states a **refusal** rather than an accepted
  exposure. **But the rules are not the whole of who reaches a node**, and **finding H3** is where
  this folder's own review of 2026-11-04 said so: IAM reads and writes bypass the rules, so launchers
  could read the node, several accounts could write it, and the public service would have held
  `firebasedatabase.admin` over the whole database. **D935 is the answer, and it is the same defect
  the review of PR #1715's M3 found from the other end (F32)**: the subtree sits in the project's
  shared database, whose `viewer` and `admin` grants are project-level and therefore cover every node
  in it, so the rules keep out every client and nobody else — a launcher, a budget admin and the
  history account all reach it. The records now live in the store §8 answers for — the bucket
  `fugaro-mcpauth-<gcp-project>`, whose IAM names one account, the front door's own `mcp_server` and
  never the coordinator's (M2, M4) — as **keyed hashes with a MAC over each record's identity, limits
  and revocation state**, and the public service holds **no database admin over anything at all**
  (H3).
  **The `mcpauth` node the rules still name and deny is a tripwire, not a store**: nothing writes it
  any more, and it stays closed so that a value written there by mistake is unreadable by every
  client. **Nothing in §8 changes**: the node still says `.read: false` and `.write: false` and still
  holds nothing, and the review reads that paragraph beside the rest of §8 anyway — the paragraph a
  review should check hardest is the one that says nothing can get in, which is exactly the paragraph
  that turned out to be wrong once.
- **Not settled on what a later version might admit.** D923 refuses `domain:`, `group:` and
  `serviceAccount:` entries in v1, the same refusal `fugaro user` makes at its own door; whether a
  later version admits a whole domain, and what would have to be true of the signer's role first, is
  deliberately left open rather than answered by omission.
