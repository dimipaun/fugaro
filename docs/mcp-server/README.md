# The hosted MCP server

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** The owner's spec of 2026-10-24 is copied verbatim into [spec.md](spec.md) §0 and is
reconciled against the code here; [reconciliation.md](reconciliation.md) §4 puts fifteen decisions
(D920–D934) in front of the owner, each with the recommendation first and the cost of the
alternative — and **all fifteen were answered on 2026-11-04**, **every one of them the
recommendation**, with the owner's own detail written into the design (D922's was restated the same
day: the first answer opened the subtree and the restatement closes it). **If the implementation
happens,
[implementation-plan.md](implementation-plan.md) §5 recommends 0.20.0 "Quokka" or later** — 0.15.0,
0.16.0 and 0.17.0 have all shipped since this design was written, and §5 says why the work is big and
security-heavy enough to want a window of its own.

**Asked by.** The owner, on 2026-10-24: a hosted MCP server inside the coordinator, with OAuth 2.1
and PKCE and the project's own Firebase as the identity provider, so that MCP clients can submit and
manage tasks without the CLI. The owner approved a design PR on 2026-10-31 and **answered the fifteen
decisions on 2026-11-04** — D920's public endpoint, D923's paired-device sign-in and its `user:`-only
lists, D930's rotating tokens, D931's single launch path, D934's off-by-default front door, D921's
library **with its licence still UNCONFIRMED**, and D922's separate **`mcpauth`** subtree, closed to
every client.

**Reconciled against.** `origin/main` at `3d6ed04d`, read on 2026-11-01, and **re-read on 2026-11-04
at `b41709ea`**, with the seventeen pull requests open at the first reading — most closely #1472
(`docs/cloud-senior-review`, D880–D899), #1481 (`docs/run-without-checkout`, D901–D919) and #1483
(`internal/rigor/rounds.go`). Every citation in [reconciliation.md](reconciliation.md) is a path and a
line at the later commit, and its §7 records the eight things `main` had overtaken in between:
**managed mode, the person-class reads, `settings`/`rigor`/`accounts`, the project image catalog and
#1676, `fugaro upgrade`, 0.17.0 shipped, `stray.go`, and the paired device** (`fugaro pair` and the
device allowlist, which is what D923's answer signs a person in with).

**SECURITY-SENSITIVE.** Two things about this folder are not ordinary design text:

- **[spec.md](spec.md) §8 is a threat model**, and it is the section the owner asked for by name: who
  can call the front door, what a stolen access token, a stolen refresh token and a stolen Firebase
  token each reach, token storage as hashes, redirect-URI abuse, open Dynamic Client Registration,
  and an unattended agent as a caller. **An independent SECURITY review of that section is
  mandatory** — and **the coordinator runs it**, not this folder's authors, exactly as it runs every
  other security-sensitive review in this repository. **The review is a precondition of this pull
  request merging**, and no task in [implementation-plan.md](implementation-plan.md) starts before it
  has been read and its findings answered. Its scope is §8, every **SEC**-marked task of §3 — the
  five whose marker says **mandatory** are M3, M4, M5, M7 and M9, and M2, M6, M8 and M10 carry the
  marker without that word — and the one new privilege this design asks for (M9's launch grant).
- **The folder keeps one rule unconditionally**: **no agent handles a credential.** The coordinator
  never hands a model credential or a forge credential to an MCP caller — not as a tool result, not
  in an error, not in a log line. A run's credential reaches the run the way it reaches every run
  today: the platform mounts it into the job. [spec.md](spec.md) §9 states it as a prohibition.

## What it is

One document set, four files:

| File | What it holds |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, copied **verbatim**, as §0 — and then the design written to the owner's answers of 2026-11-04: the front door and its transport (§2), the authorization server and its flow (§3), Firebase as the identity provider (§4), the six tools (§5), tokens, rigor windows and spend limits (§6), unattended agents (§7), **the threat model (§8)**, the coordinator's prohibitions (§9), and the console's graph write (§10) |
| [reconciliation.md](reconciliation.md) | Each claim of §0 against the code at the base: **what exists** (F1–F15, F19), **what is missing** (F16–F18, F20–F22) and **what conflicts** (F23–F31), each with a path and a line; then the **fifteen decisions, D920–D934** (§4) with the owner's answers of 2026-11-04 under them, the number block and how it was read (§5), the owner checks that no hermetic test can stand in for (§6), and what `main` had overtaken (§7) |
| [implementation-plan.md](implementation-plan.md) | What the owner was asked and answered (§1), the order and what forces it (§2), ten tasks **M1–M10** each with its files, its interface, its **red step**, its named tests and its SEC flag (§3) — M4 carrying its **For operators** block — the **shared-files table** (§4), **the release slot** (§5), the test rules (§6) and what the plan does not own (§7) |
| `README.md` | This file: the status, the reading of the number block, and what a reader should not take from here |

**Two works this folder is wired to.** The **stateless-orchestrator spec** (open as **#1682**, not on
`main` when this was written) gives the coordinator a **state tool group**, and **this MCP server is
what hosts it** — D931 already serves the coordinator's own tools on the same endpoint under their
existing names, so the state group reaches MCP clients through the front door rather than through a
second surface ([spec.md](spec.md) §11 says which part stays that design's to decide). The
**coordinator's own P7/P8** is the other: [reconciliation.md](reconciliation.md) F1, F24 and the plan's
§7 keep the front door's home in the coordinator's service, and this folder builds the MCP half of it
and nothing else.

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
| Reserved, unused | **D935–D939** |
| A later task takes | **D1310**, the first number above every allocation above, and re-checks the facts rather than this conclusion |

`grep -rhoE 'D[0-9]{3,4}' docs` is not quite enough on its own, for two reasons this folder records
so the next reader does not repeat the search: it returns `D1000` (a package-lock integrity
fragment, not a decision), and a concurrent work's block is not on `main` at all — another branch's
README, in its own diff, is where the block above D1189 is written.

## What a reader should not take from here

- **Not an approval of any task.** Every decision was answered on 2026-11-04, and **an answer is not
  an approval**: no task in [implementation-plan.md](implementation-plan.md) is approved, and the
  owner approves them one at a time.
- **Not a plan to start.** M4 creates a public endpoint and M9 adds a launch grant; **neither starts
  before the independent SECURITY review of [spec.md](spec.md) §8 has been read and its findings
  answered**, and **the coordinator runs that review**.
- **Not a design anyone may build against yet.** D921's library was chosen with **its licence
  UNCONFIRMED**: the owner or counsel confirms it before any build
  ([reconciliation.md](reconciliation.md) §6, the first owner check), and a licence that turns out to
  be unusable sends that decision back to its alternatives.
- **Not the last word on the MCP specification.** §0's open item — re-verify the current revision —
  is still an owner check ([reconciliation.md](reconciliation.md) §6): **this container cannot reach
  the public specification**, which is the limitation `docs/coordinator/reconciliation.md`:794
  recorded as U1, so the design stays written to the **2026-07-28** revision because that is the one
  the owner named.
- **Not a design with a second reader to argue about.** D922's answer closes the token records'
  subtree to every client (`.read: false`, `.write: false`, the coordinator's own account the only
  writer), so [spec.md](spec.md) §8's token-storage paragraph states a **refusal** rather than an
  accepted exposure — and the `mcpauth` node is the one place in this design where nothing but the
  coordinator's database admin grant reaches the data at all. The review reads that paragraph
  beside the rest of §8 anyway: the paragraph a review should check hardest is the one that says
  nothing can get in.
- **Not settled on what a later version might admit.** D923 refuses `domain:`, `group:` and
  `serviceAccount:` entries in v1, the same refusal `fugaro user` makes at its own door; whether a
  later version admits a whole domain, and what would have to be true of the signer's role first, is
  deliberately left open rather than answered by omission.
