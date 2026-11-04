# Workspaces: an optional grouping of projects

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** The owner's spec of 2026-10-24 is carried in [spec.md](spec.md) §0 as this run recorded
it; [reconciliation.md](reconciliation.md) §4 puts sixteen decisions (**D1290–D1305**) in front of
the owner, each with the recommendation first and the cost of the alternative, and **the owner
answered all sixteen on 2026-11-04 — every one as recommended**, so no decision here is marked *not
the recommendation*. **If the implementation happens,
[implementation-plan.md](implementation-plan.md) §5 recommends 0.20.0 or later**: the design is
large and security-heavy, and §5 says why.

**Asked by.** The owner, on 2026-10-24: an optional grouping of Fugaro projects, with single sign-on
through a workspace Firebase token exchange, credentials shared across the group's projects, and a
cap over the group's spend. The owner approved a design PR on 2026-10-31 and **answered the sixteen
decisions on 2026-11-04**.

**Reconciled against.** `origin/main` at `c2ca0aee`, merged into this branch and read on 2026-11-04,
and the pull requests open at the first reading — **#1487** (`docs/mcp-server`, D920–D934, the
authorization server this work's sign-in half depends on: it **landed on `main`** on 2026-11-04, with
none of its code written), #1491 (`docs/binary-allow-rule`, **D981–D1010** — the block this design's
old numbers collided with), #1676 (the repo-image removal design, D1190–D1219) and #1678
(D1260–D1289). Every citation in [reconciliation.md](reconciliation.md) is a path and a line at the
current base. The first reading was taken on 2026-11-01 at `3d6ed04d`; **what main has overtaken
since is listed in [reconciliation.md](reconciliation.md) §2f**, which is the section to read before
trusting any finding above it.

**SECURITY-SENSITIVE.** Two things about this folder are not ordinary design text:

- **[spec.md](spec.md) §8 is a threat model**, and it is the section the owner's brief asked for by
  name: what a stolen workspace token reaches that the person did not already have, what a member's
  operator can reach of the shared pool, why the workspace cap's accounting is only as good as each
  member's own image, and what the new cross-project read exposes. That last one is stated at its
  real size: the service's IAM grant on a member is **whole-database read and whole-database write**
  (Realtime Database has no path-level scope and IAM bypasses the rules), so a compromised service
  can read a member's whole database and **lift a member's cap** — rule 1 is a property of the
  service's code, not of its grant — and a Firebase Auth admin of the workspace's project can mint a
  verified token for any listed address, in every member. **The member's own pin is what bounds that
  last one**: §2 puts the workspace's Firebase project id, the issuer its exchange requires and the
  service account its two grants are bound to in the **member's** config, so neither a member's
  exchange nor a member's grant is ever aimed by a value the workspace's record supplies. **An
  independent SECURITY review of that
  section is mandatory before this design merges**, and **the coordinator runs it** — it is not a
  task of the runs that implement anything, and no task in
  [implementation-plan.md](implementation-plan.md) starts before its findings are answered. (The
  owner's brief first put that review before the *decisions*; the owner answered the decisions on
  2026-11-04, so the gate it holds now is the merge.)
- **The folder keeps two rules unconditionally**, and [spec.md](spec.md) §9 states both as
  prohibitions: **a workspace never widens a member** (no member's policy, cap, allow-list or
  launcher list can be changed by anything a workspace holds), and **no agent handles a
  credential** (a shared credential is proxied by the workspace's service and never disclosed).

## What it is

Four files, the shape the other design folders here use:

| File | What it holds |
| --- | --- |
| [spec.md](spec.md) | The owner's spec as recorded (§0), then the design written to every recommendation: what a workspace is (§1), the record (§2), the identity and the token exchange (§3), shared credentials (§4), the workspace cap (§5), the command surface (§6), the console and the mobile app (§7), **the threat model (§8)**, what a workspace never does (§9), and what this design leaves open (§10) |
| [reconciliation.md](reconciliation.md) | Each claim of §0 against the code at the base: **what exists** (F1, F4, F7–F11, F13–F17, F19, F21), **what is missing** (F2, F5, F12, F18, F20, F22) and **what conflicts** (F3, F6), each with a path and a line; §2f is what main has overtaken since, including the security re-reads F29–F32; then the **sixteen decisions, D1290–D1305** (§4), the number block and how it was read (§5), the owner checks no hermetic test can stand in for (§6), the UNVERIFIED table (§7) and **the two owner decisions this round reported instead of taking (§8)**. **Where it disagrees with [spec.md](spec.md), it wins** |
| [implementation-plan.md](implementation-plan.md) | What the owner must answer first (§1), the order and the three dependencies that force it (§2), ten tasks **W0–W9** each with its files, its interface, its **red step**, its named tests and its **SEC** flag (§3), the **shared-files table** (§4), **the release slot** (§5), the test rules (§6) and what the plan does not own (§7) |
| `README.md` | This file: the status, what was and was not recovered of the owner's spec, the reading of the number block, and what a reader should not take from here |

## The owner's spec, and what was not recovered

**The verbatim text of the owner's spec was not recovered by this run.** [spec.md](spec.md) §0
carries the recorded summary — the four things a workspace is — and the reconciliation and the plan
are written against that summary. A reader who holds the original should treat it as authoritative
and fix §0 to match it, exactly as `docs/central-orchestrator/README.md` says of its own spec copy.

**The original's worked example named a real organisation.** It is a placeholder here —
`workspace-alpha` — and its projects are `owner/repo-a` and `owner/repo-b`, as they are in
[spec.md](spec.md) and [implementation-plan.md](implementation-plan.md). **The workspace is deliberately not `workspace-a`:** that string already means two
other things in this repository — it is a Bitbucket workspace name in the policy examples
(`internal/projectpolicy/bitbucket.go`:33, `:75-76`, `:194` all write `bitbucket/workspace-a/…`, and
the tests in `internal/cli/project_cmd_bitbucket_test.go` carry the same string) and it is a
*project* placeholder in `docs/settings-commands/spec.md`:7 — and a folder about Fugaro workspaces
that reused it would be read as being about either of those.
No real organisation, person or address appears anywhere in it, no currency figure is stated (the
design carries the shape of a cap and no number), and no file contains a credential, a key, a token
or anything shaped like one.

This is core documentation, and no file in it carries a licence notice.

## The owner's answers, 2026-11-04

**All sixteen decisions are answered, and every one as recommended.** So this folder carries no
"not the recommendation" marker anywhere: there is no deviation to record. The answers, by the
number they now carry (the old number in brackets is the one the owner answered under — see *The
number block* for why the block moved):

| Decision | Answer (2026-11-04) | Why the design reads this way |
| --- | --- | --- |
| **D1290** (D980) | **The workspace has its own Firebase project** | A designated member's Firebase would make that member's launchers/operators lists silently the workspace's admission lists — a widening nobody asked for |
| **D1291** (D981) | At most one workspace per project | The membership is one scalar key; two workspaces leave "whose headroom applies" with no unique answer |
| **D1292** (D982) | The record is declared locally and published by `fugaro update` | The HARD RULE: no second command that must be run after a change |
| **D1293** (D983) | **One sign-in at the workspace, one exchange per member** | One stolen token must not be every member at once |
| **D1294** (D984) | **The exchange happens at the member** | The member stays the authority over what the identity may do there; a workspace that minted member tokens would be a credential authority over them |
| **D1295** (D985) | **Option (A): nothing beyond the member's own launcher and operator lists** | The only-tighten rule would not survive a workspace role that maps to a member role |
| **D1296** (D986) | Hashes only | A stored member token is the one value "hashes only" exists to prevent |
| **D1297** (D987) | **One workspace bundle and one service** | N copies are N rotations, N leak sites and no single revocation — and no one hop that sees the total, which the cap needs |
| **D1298** (D988) | A per-project allow-list, intersected with the member's policy | A workspace credential available to every member would be a policy bypass |
| **D1299** (D989) | **The cap is enforced at the workspace service** | A member cannot be the fence over its own spend |
| **D1300** (D990) | **It bounds the credentials the workspace pays for** | Calling it a bound on a member's total spend would be false for a member that holds credentials of its own |
| **D1301** (D991) | A rollup from each member's ledger, `stale` rather than zero | An unreadable member must never look free |
| **D1302** (D992) | `fugaro budget kill` gains a workspace scope | One stop command to remember under pressure, not two |
| **D1303** (D993) | Local writes and reads; `update` carries the cloud | The HARD RULE again |
| **D1304** (D994) | No console or mobile task in this plan | A wire contract fixed before the document it carries exists |
| **D1305** (D995) | **Not a licence unit and not a price** | The subscriptions design already measures the paid unit — a seat — and a workspace is the shape that measurement takes |

**Two dependencies the answers do not remove.** This design's SSO half is written to **#1487's**
authorization server (`docs/mcp-server/`), which is **on `main`** since 2026-11-04 — as a design
folder, with **none of its code written** ([implementation-plan.md](implementation-plan.md) §2 and §5
say what that costs the slot). And **§8's threat model needs an independent SECURITY review before
this design merges — the coordinator runs it**, not this folder's author and not the runs that
implement anything.

## The number block

**This design was renumbered on 2026-11-04.** It was written as **D980–D995** on 2026-11-01; the
next day **#1491** (`docs/binary-allow-rule`) claimed **D981–D1010**, which overlaps it, and main
itself had moved far past it. The whole design therefore moved, **in order**, into the block the
launcher assigned to this pull request:

| Written as | Now | | Written as | Now |
| --- | --- | --- | --- | --- |
| D980 | **D1290** | | D988 | **D1298** |
| D981 | **D1291** | | D989 | **D1299** |
| D982 | **D1292** | | D990 | **D1300** |
| D983 | **D1293** | | D991 | **D1301** |
| D984 | **D1294** | | D992 | **D1302** |
| D985 | **D1295** | | D993 | **D1303** |
| D986 | **D1296** | | D994 | **D1304** |
| D987 | **D1297** | | D995 | **D1305** |

Read on 2026-11-04 at `origin/main` `c2ca0aee`:

| | |
| --- | --- |
| Highest number in `docs/` on `origin/main` (`c2ca0aee`) | **D1189**, the reserved tail of `docs/config-management`'s block (D1160–D1183 written, D1184–D1189 reserved and unused) |
| Taken by work not on `main` | **D1190–D1219** (**#1676**, the repo-image removal design), **D1220–D1259** (the stateless-orchestrator work), **D1260–D1289** (**#1678**) |
| **This work's block** | **D1290–D1309** — the sixteen decisions of [reconciliation.md](reconciliation.md) §4 take **D1290–D1305** |
| Reserved, unused | **D1306–D1309** |

**The block is claimed whole and used from the front**, which is why the design does not straddle
two blocks: a reader who greps `D129x` or `D130x` finds this folder and nothing else.
[reconciliation.md](reconciliation.md) §5 records the reading and the boundary the search needs, and
notes the one false positive `grep -rhoE 'D[0-9]{3,4}' docs` returns on `main`.

## What a reader should not take from here

- **Not an approval to implement.** The decisions are answered; **no task is approved**, and the
  owner approves each task by name. An answered decision is not a merge.
- **Not a plan to start.** W4, W5, W6 and W7 each add a surface [spec.md](spec.md) §8 describes, and
  none of them starts before that section's mandatory independent SECURITY review — which **the
  coordinator runs** — has been read and answered.
- **Not a claim that the sign-in half can be built now.** W4's exchange is the hosted MCP server's
  authorization server: that design is on `main`, and **none of its code is written**
  ([implementation-plan.md](implementation-plan.md) §2 and §5 say what that costs the slot).
- **Not a new kind of project.** A workspace owns no repository, no runs bucket, no job and no
  image; it is a grouping over projects that already exist.
- **Not a second `ee/` boundary.** The workspace's own services are the coordinator's and the
  credentials service's, and the standing rulings put them under `ee/`
  ([reconciliation.md](reconciliation.md) F17). This document adds no licence notice anywhere.
