# Workspaces: an optional grouping of projects

**Status: design only, 2026-11-01. Nothing here is approved, nothing here is implemented, and the
implementation is deliberately not approved by this document.** The owner's spec of 2026-10-24 is
carried in [spec.md](spec.md) §0 as this run recorded it and is reconciled against the code for the
first time here; [reconciliation.md](reconciliation.md) §4 puts sixteen decisions (**D980–D995**) in
front of the owner, each with the recommendation first and the cost of the alternative. **If the
implementation happens, [implementation-plan.md](implementation-plan.md) §5 recommends 0.17.0** —
the design belongs to the 0.15.0 documentation window, the code to the release after the hosted MCP
server, and §5 says why.

**Asked by.** The owner, on 2026-10-24: an optional grouping of Fugaro projects, with single sign-on
through a workspace Firebase token exchange, credentials shared across the group's projects, and a
cap over the group's spend. The owner approved a design PR on 2026-10-31.

**Reconciled against.** `origin/main` at `3d6ed04d`, read on 2026-11-01, and the 24 pull requests
open at that moment — most closely #1487 (`docs/mcp-server`, D920–D934, the design this work's
sign-in half builds on), #1490 (`docs/periodic-reviews`, D960–D971), #1488 (`docs/plugin-user-scope`,
D940–D953), #1472 (`docs/cloud-senior-review`, D880–D899), #1481 (`docs/run-without-checkout`,
D901–D919) and #1485 (`docs/run-limit-carry-over`, D901–D920). Every citation in
[reconciliation.md](reconciliation.md) is a path and a line at that commit.

**SECURITY-SENSITIVE.** Two things about this folder are not ordinary design text:

- **[spec.md](spec.md) §8 is a threat model**, and it is the section the owner's brief asked for by
  name: what a stolen workspace token reaches that the person did not already have, what a member's
  operator can reach of the shared pool, why the workspace cap's accounting is only as good as each
  member's own image, and what the new cross-project read exposes. **An independent review of that
  section is mandatory**, by the owner's brief, before any decision in §4 of the reconciliation is
  answered.
- **The folder keeps two rules unconditionally**, and [spec.md](spec.md) §9 states both as
  prohibitions: **a workspace never widens a member** (no member's policy, cap, allow-list or
  launcher list can be changed by anything a workspace holds), and **no agent handles a
  credential** (a shared credential is proxied by the workspace's service and never disclosed).

## What it is

Four files, the shape the other design folders here use:

| File | What it holds |
| --- | --- |
| [spec.md](spec.md) | The owner's spec as recorded (§0), then the design written to every recommendation: what a workspace is (§1), the record (§2), the identity and the token exchange (§3), shared credentials (§4), the workspace cap (§5), the command surface (§6), the console and the mobile app (§7), **the threat model (§8)**, what a workspace never does (§9), and what this design leaves open (§10) |
| [reconciliation.md](reconciliation.md) | Each claim of §0 against the code at the base: **what exists** (F1, F4, F7–F11, F13–F17, F19, F21), **what is missing** (F2, F5, F12, F18, F20, F22) and **what conflicts** (F3, F6), each with a path and a line; then the **sixteen decisions, D980–D995** (§4), the number block and how it was read (§5), the owner checks no hermetic test can stand in for (§6) and the UNVERIFIED table (§7). **Where it disagrees with [spec.md](spec.md), it wins** |
| [implementation-plan.md](implementation-plan.md) | What the owner must answer first (§1), the order and the three dependencies that force it (§2), ten tasks **W0–W9** each with its files, its interface, its **red step**, its named tests and its **SEC** flag (§3), the **shared-files table** (§4), **the release slot** (§5), the test rules (§6) and what the plan does not own (§7) |
| `README.md` | This file: the status, what was and was not recovered of the owner's spec, the reading of the number block, and what a reader should not take from here |

## The owner's spec, and what was not recovered

**The verbatim text of the owner's spec was not recovered by this run.** [spec.md](spec.md) §0
carries the recorded summary — the four things a workspace is — and the reconciliation and the plan
are written against that summary. A reader who holds the original should treat it as authoritative
and fix §0 to match it, exactly as `docs/central-orchestrator/README.md` says of its own spec copy.

**The original's worked example named a real organisation.** It is a placeholder here —
`workspace-alpha` — and its projects are `owner/repo-a` and `owner/repo-b`, as they are throughout
this folder. **The workspace is deliberately not `workspace-a`:** that string already means two
other things in this repository — it is a Bitbucket workspace name in the policy examples
(`internal/projectpolicy/bitbucket.go`:33, `:75-76`, `:194` all write `bitbucket/workspace-a/…`, and
the tests in `internal/cli/project_cmd_bitbucket_test.go` carry the same string) and it is a
*project* placeholder in `docs/settings-commands/spec.md`:7 — and a folder about Fugaro workspaces
that reused it would be read as being about either of those.
No real organisation, person or address appears anywhere in it, no currency figure is stated (the
design carries the shape of a cap and no number), and no file contains a credential, a key, a token
or anything shaped like one.

This is core documentation, and no file in it carries a licence notice.

## The number block

Read at launch by hand, not from a neighbour's line:

| | |
| --- | --- |
| Highest number in `docs/` on `origin/main` (`3d6ed04d`) | **D900**, in `docs/rigor-command/README.md`:117, and answered |
| Highest number in the open pull requests' own diffs | **D979**, reserved by `docs/periodic-reviews` (**#1490**) |
| Also taken, the same morning | **D920–D934** (`docs/mcp-server`, **#1487**, reserving D935–D939), **D940–D953** (`docs/plugin-user-scope`, **#1488**, reserving D954–D959), **D901–D919** (**#1481**) and **D901–D920** (**#1485**) |
| **This work's block** | **D980–D995** — the sixteen decisions of [reconciliation.md](reconciliation.md) §4 |
| Reserved, unused | **D996–D999** |
| A later task takes | **D1000**, and re-checks the facts above rather than this conclusion |

`grep -rhoE 'D[0-9]{3,4}' docs` returns `D1000` on `main`, and it is not a decision: it is the test
name `TestSelftestManagedSettingsDirUID1000` in
`docs/plans/2026-09-30-m9a-gateway-and-project-identity.md`. A concurrent work's block is not on
`main` at all, which is why the open pull requests' own diffs are read as well as the tree.
[reconciliation.md](reconciliation.md) §5 states both, and the boundary the search needs.

## What a reader should not take from here

- **Not an approval.** Every decision in the reconciliation is answered by the owner or not at all;
  the design is written to the recommendations, and an answer the other way is a different document.
- **Not a plan to start.** W4, W5, W6 and W7 each add a surface [spec.md](spec.md) §8 describes, and
  none of them starts before that section's mandatory independent review has been read.
- **Not a claim that the sign-in half can be built now.** W4's exchange is the hosted MCP server's
  authorization server, which is an open pull request today
  ([implementation-plan.md](implementation-plan.md) §2 and §5 say what that costs the slot).
- **Not a new kind of project.** A workspace owns no repository, no runs bucket, no job and no
  image; it is a grouping over projects that already exist.
- **Not a second `ee/` boundary.** The workspace's own services are the coordinator's and the
  credentials service's, and the standing rulings put them under `ee/`
  ([reconciliation.md](reconciliation.md) F17). This document adds no licence notice anywhere.
