# The hosted MCP server

**Status: design only, 2026-11-01. Nothing here is approved, nothing here is implemented, and the
implementation is deliberately not approved by this document.** The owner's spec of 2026-10-24 is
copied verbatim into [spec.md](spec.md) §0 and is reconciled against the code for the first time
here; [reconciliation.md](reconciliation.md) §4 puts fifteen decisions (D920–D934) in front of the
owner, each with the recommendation first and the cost of the alternative. **If the implementation
happens, [implementation-plan.md](implementation-plan.md) §5 recommends 0.16.0 "Moose"** — the design
belongs to the 0.15.0 documentation window, the code to the release after it, and §5 says why.

**Asked by.** The owner, on 2026-10-24: a hosted MCP server inside the coordinator, with OAuth 2.1
and PKCE and the project's own Firebase as the identity provider, so that MCP clients can submit and
manage tasks without the CLI. The owner approved a design PR on 2026-10-31.

**Reconciled against.** `origin/main` at `3d6ed04d`, read on 2026-11-01, and the seventeen pull
requests open at that moment — most closely #1472 (`docs/cloud-senior-review`, D880–D899), #1481
(`docs/run-without-checkout`, D901–D919) and #1483 (`internal/rigor/rounds.go`). Every citation in
[reconciliation.md](reconciliation.md) is a path and a line at that commit.

**SECURITY-SENSITIVE.** Two things about this folder are not ordinary design text:

- **[spec.md](spec.md) §8 is a threat model**, and it is the section the owner asked for by name: who
  can call the front door, what a stolen access token, a stolen refresh token and a stolen Firebase
  token each reach, token storage as hashes, redirect-URI abuse, open Dynamic Client Registration,
  and an unattended agent as a caller. **An independent review of that section is mandatory**, by
  the owner's brief, before any decision in §4 of the reconciliation is answered.
- **The folder keeps one rule unconditionally**: **no agent handles a credential.** The coordinator
  never hands a model credential or a forge credential to an MCP caller — not as a tool result, not
  in an error, not in a log line. A run's credential reaches the run the way it reaches every run
  today: the platform mounts it into the job. [spec.md](spec.md) §9 states it as a prohibition.

## What it is

One document set, four files:

| File | What it holds |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, copied **verbatim**, as §0 — and then the design written to every recommendation: the front door and its transport (§2), the authorization server and its flow (§3), Firebase as the identity provider (§4), the six tools (§5), tokens, rigor windows and spend limits (§6), unattended agents (§7), **the threat model (§8)**, the coordinator's prohibitions (§9), and the console's graph write (§10) |
| [reconciliation.md](reconciliation.md) | Each claim of §0 against the code at the base: **what exists** (F1–F15, F19), **what is missing** (F16–F18, F20–F22) and **what conflicts** (F23–F31), each with a path and a line; then the **fifteen decisions, D920–D934** (§4), the number block and how it was read (§5), and the owner checks that no hermetic test can stand in for (§6) |
| [implementation-plan.md](implementation-plan.md) | What the owner must answer first (§1), the order and what forces it (§2), ten tasks **M1–M10** each with its files, its interface, its **red step**, its named tests and its SEC flag (§3) — M4 carrying its **For operators** block —
the **shared-files table** (§4), **the release slot** (§5), the test rules (§6) and what the plan does not own (§7) |
| `README.md` | This file: the status, the reading of the number block, and what a reader should not take from here |

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

Read at launch by hand, not from a neighbour's line:

| | |
| --- | --- |
| Highest number in `docs/` on `origin/main` (`3d6ed04d`) | **D900**, in `docs/rigor-command/README.md`:117 |
| Highest number in the open pull requests' own diffs | **D899** (`docs/cloud-senior-review`, #1472) |
| Also taken, the same morning | **D901–D919**, by `docs/run-without-checkout` (#1481) |
| **This work's block** | **D920–D934** — the fifteen decisions of [reconciliation.md](reconciliation.md) §4 |
| Reserved, unused | **D935–D939** |
| A later task takes | **D940**, and re-checks the facts above rather than this conclusion |

`grep -rhoE 'D[0-9]{3,4}' docs` is not quite enough on its own, for two reasons this folder records
so the next reader does not repeat the search: it returns `D1000` (a package-lock integrity
fragment, not a decision), and a concurrent work's block is not on `main` at all — #1481's README, in
its own diff, is where D901–D919 is written.

## What a reader should not take from here

- **Not an approval.** Every decision in the reconciliation is answered by the owner or not at all;
  the design is written to the recommendations and an answer the other way is a different document.
- **Not a plan to start.** M4 creates a public endpoint and M9 adds a launch grant; neither starts
  before the mandatory independent review of [spec.md](spec.md) §8 has been read.
- **Not the last word on the MCP specification.** §0's open item — re-verify the current revision —
  is an owner check ([reconciliation.md](reconciliation.md) §6), and the design is written to the
  2026-07-28 revision because that is the one the owner named.
