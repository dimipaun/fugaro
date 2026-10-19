# The coordinator agent

**Status: reconciliation only; nothing is implemented; the plan waits for the owner's answers.** This directory holds the owner's spec for a **coordinator agent**, the
reconciliation of that spec against the code at `origin/main` `c6009b5f` (`release:
v0.8.6 (#637)`), and the questions the reconciliation could not answer on its own. No Go,
no TypeScript, no Terraform, no rules change and no live action follows from these
documents. **Owner: Dimi Paun. Date: 2026-10-19.** The reconciliation was run against
`c6009b5f`, and every path and symbol in it was read at that commit.

A coordinator is a **per-project service** that aggregates what every local orchestrator is
doing, owns project-level state, detects conflicts early and suggests sequencing — advisory,
never authoritative. The spec is the owner's own text; the reconciliation is what the
repository actually has against it.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | **The owner's spec, verbatim** — *"Fugaro Coordinator Agent — Design Spec"*, sections 1–11, exactly as received. Committed alone, in its own commit, before anything else in this directory. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief this work was written from (§0); the spec's concepts against what the repository has (§1); findings **#1–#30**, each classified **Wrong / Exists / Gap / Rule / Open**, each citing a path and a symbol and stating its consequence (§2); **Q1–Q16** for the owner, each with two or three options, the recommendation first and what it changes in the spec (§3); the **UNVERIFIED** table (§4); and the glossary the spec's words need (§5). |

## Typos fixed in `spec.md`

**None.** The spec is copied byte for byte from the text the owner supplied, and it needed
no correction. Nothing was reworded, reordered, re-headed or re-numbered, and no sentence
was added. If a later reader finds a difference between `spec.md` and the owner's copy, the
owner's copy is right and `spec.md` is the file to fix.

## The numbers this work holds

| Numbers | Whose | State |
| --- | --- | --- |
| **D1–D192** | the console (D1–D38), voice (D39–D54b), managed projects (D55–D78), the website (D90–D109), mobile (D110–D129), the credentials service (D130–D159), the mobile functional spec (D160–D179), the website redesign (D180–D192) | **taken**; the highest number written anywhere in `docs/` at `c6009b5f` is **D192** |
| **D193 onward** | — | **free.** This work takes **no D-number**: its open items are **Q1–Q16**, and they stay questions until the owner answers them. The spec is the owner's, and the reconciliation is a reconciliation — neither allocates a decision. |
| **Checks 1–109** | the live checklist, up to the mobile functional spec's reserved **105–109** | **taken or reserved**; the highest check *section written* is **70c** |
| **Check 110** | **claimed by the always-on orchestrator design** (`docs/design/always-on-orchestrator.md`, PR #632, its §7 and Q5) | **claimed, not merged.** This work takes **no check number**, and it must not take 110 while that claim is open: the repository's own rule is that whichever of two open claims merges *second* renumbers itself (`docs/gcp-live-checklist.md`) |

A reserved number is never reused and never renumbered. Neither block above is derived by
grep alone: the D-block is read from each work's own README, and the check numbers from
`docs/gcp-live-checklist.md`'s allocation paragraph.

## What this work collides with, in one paragraph

The spec is a good idea whose deployment section describes a surface Fugaro has never had.
**There is no Cloud Run service anywhere in this repository** — every workload is a Cloud
Run *job*, and the type is not even in `fugaro init`'s plan allowlist, so adding one is a
decision the code enforces rather than an accident. The spec's *"whoever can reach the
project's coordinator is in the project; no extra permission model in v1"* is the opposite
of the standing design, which refuses `allUsers`, `allAuthenticatedUsers` and `domain:`
members everywhere it can and puts the trust boundary at the repository and the task source.
Its *"reuses the project's existing key management and security service"* names a service
that does not exist — the credentials service is a document, not code — and its *"Firestore
transactions"* and *"Firestore's real-time listeners drive live updates in the console"* are
both unavailable: the Firestore client is a hand-written REST client with no transactions
and no listeners, and the console renderer's CSP is `connect-src 'none'`, so it never talks
to the network at all. Its *"daily token-spend cap that counts toward the project's existing
spend caps"* cannot be satisfied by configuration, because every existing cap is keyed to a
**run's** Firebase identity and a long-lived service has none. And its LLM half would decide
the mobile design's deferred phase-2 question (D115) implicitly — which is exactly what
that deferral was written to prevent. What the spec gets right is the part that needs no new
surface: the project-wide view, the PR records, the roadmap and the override log are all
greenfield, all deterministic, and all buildable from the two stores and the one GitHub
credential the repository already has.

## Related

- [docs/design/v1.md](../design/v1.md) §1 — what a **Fugaro project** and an
  **installation** are (the spec's unit of deployment).
- [docs/design/project-centric.md](../design/project-centric.md) — the project as the unit
  of configuration, policy and credentials; the `project` Terraform module.
- `docs/design/always-on-orchestrator.md` — the close sibling: one resident service per
  installation, no model credential in it. **It is in open PR #632 and not on `main`**, so
  there is no link to it here. Reconciliation §2 #29 and **Q10**.
- [docs/credentials-service/](../credentials-service/README.md) — the "security service"
  the spec's §8 names. Plan only; D130–D154 answered 2026-10-17; nothing implemented.
- [docs/mobile/](../mobile/README.md) — the orchestrator, the phone, and D115 (phase 2
  deferred).
- [docs/design/desktop-console-v2.md](../design/desktop-console-v2.md) — the console's
  project-wide "Overview" (unbuilt) and D22, the one write (launch).
- [SECURITY.md](../../SECURITY.md) — the trust assumption the spec's §4 contradicts.
