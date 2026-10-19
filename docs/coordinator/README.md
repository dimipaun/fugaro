# The coordinator agent

**Status: reconciliation only; nothing is implemented and no plan is approved.** This directory holds the owner's spec for a **coordinator agent**, the
reconciliation of that spec against the code at `origin/main` `c6009b5f` (`release:
v0.8.6 (#637)`), the questions the reconciliation could not answer on its own, and the
owner's answers to them. No Go,
no TypeScript, no Terraform, no rules change and no live action follows from these
documents. **Owner: Dimi Paun. Date: 2026-10-19.** The reconciliation was run against
`c6009b5f`, and every path and symbol in it was read at that commit; the citation check that
backs that claim, and the shape of citation it does not cover, are stated in
`internal/cli/docs_coordinator_test.go`'s own comment rather than left implied.
Citations into `docs/design/` and `docs/mobile/` name the document and the phrase the
citation turns on rather than a line number — those two trees gain sections, rows and
paragraphs while this work sits in review, and a line number into one of them moves under
the sentence it was written for without anything looking wrong.
`TestDocsCoordinatorFrequentCitationsAreAnchoredByContent` holds every one of them to the
single line of that document which carries the phrase.

**All sixteen questions are answered, and none is approved.** The owner answered Q1–Q16 on
**2026-10-19**, each by taking **option (A)**, the recommended one; each answer is recorded
under its question in the reconciliation's §3, with what it means for the spec.
**An answer is not an approval**: nothing in this directory has been accepted, approved or given a decision number, no plan exists, and the plan that follows is a separate document with its own gate.
The options that were not taken are left in place, unedited, so the answer can be
read against what it was choosing between. Two things the answers do not settle: **U2 stays
an owner check** (whether the GitHub App already has `pull_requests: read`, which Q3's answer
asks it for), and **U6/U7 are still drafts** in open pull requests.

A coordinator is a **per-project service** that aggregates what every local orchestrator is
doing, owns project-level state, detects conflicts early and suggests sequencing — advisory,
never authoritative. The spec is the owner's own text; the reconciliation is what the
repository actually has against it.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | **The owner's spec, verbatim** — *"Fugaro Coordinator Agent — Design Spec"*, sections 1–11, exactly as received. Committed alone, in its own commit, before anything else in this directory. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief this work was written from (§0); the spec's concepts against what the repository has (§1); findings **#1–#30**, each classified **Wrong / Exists / Gap / Rule / Open**, each citing a path and a symbol and stating its consequence (§2); **Q1–Q16**, each with two or three options, the recommendation first, what it changes in the spec, and **the owner's answer of 2026-10-19** (§3); the **UNVERIFIED** table (§4); and the glossary the spec's words need (§5). |

## Typos fixed in `spec.md`

**None.** The spec is copied byte for byte from the text the owner supplied, and it needed
no correction. Nothing was reworded, reordered, re-headed or re-numbered, and no sentence
was added. If a later reader finds a difference between `spec.md` and the owner's copy, the
owner's copy is right and `spec.md` is the file to fix.

## The numbers this work holds

| Numbers | Whose | State |
| --- | --- | --- |
| **D1–D192** | the owner's allocation, block by block as the one table in this repository that lists every work's block reads it. **Every block is cited by content and not by line**: it is read from the one line of the document named after it that carries its label, written as that document writes it — the voice block's own label is **D39–D54, plus D54b** there. A line number here went stale the moment another work inserted a row above the table it pointed into, which is why these are content anchors now: **D1–D20** the console (`docs/mobile/README.md`), **D21–D38** console v2 (`docs/mobile/README.md`), **D39–D54b** voice, written there as **D39–D54, plus D54b** (`docs/mobile/README.md`), **D55–D78** managed projects (`docs/mobile/README.md`), **D90–D109** the website (`docs/mobile/README.md`), **D110–D129** mobile (`docs/mobile/README.md`), **D130–D159** the credentials service (`docs/mobile/README.md`), **D160–D179** the mobile functional spec (`docs/mobile/README.md`) and **D180–D192** the website redesign, proposed (`docs/website/redesign/README.md`) | **taken**, with two exceptions the allocation's own documents record: **D79–D89** is **deliberately unused** — *"an unused number is not a free number"* (`docs/website/redesign/implementation-plan.md`) — and **D180–D192** is **proposed, not yet confirmed by the owner**. The highest number written anywhere in `docs/` at `c6009b5f` is **D192** |
| **D193 onward** | — | **free.** This work takes **no D-number**: its items were **Q1–Q16**, and the owner answered them on 2026-10-19 without a decision number. An answer picks an option; it does not allocate, and it does not approve. The spec is the owner's, and the reconciliation is a reconciliation — neither allocates a decision. |
| **Checks 1–109** | the live checklist (`docs/gcp-live-checklist.md`), block by block as its own register reads it, cited by content the same way — the register writes its own sections (`## Check 70a`), and the two blocks it does not reach are written **Checks 90–99** and **Checks 100–109** on the lines named beside them below: **48–56** reserved by the project-centric plan (`docs/gcp-live-checklist.md`), **59a/59b** the desktop console's (`docs/gcp-live-checklist.md`), **60** the budget-token channel's (`docs/gcp-live-checklist.md`), **61** project-centric phase 6's (`docs/gcp-live-checklist.md`), **62–63** console v2, **64–66** voice and **67–68** managed projects, each reserved (`docs/gcp-live-checklist.md`), **69a–69c** the website's (`docs/gcp-live-checklist.md`), **70a–70c** the website redesign's, proposed (`docs/gcp-live-checklist.md`), **71–79** inside the website work's held range, with no check written in them (`docs/gcp-live-checklist.md`), **80–89** mobile's, carved out of that range (`docs/gcp-live-checklist.md`), **90–99** the credentials service, written **Checks 90–99** (`docs/credentials-service/README.md`) and **100–109** the mobile functional spec, written **Checks 100–109** (`docs/mobile/README.md`), the two the checklist's register does not reach | **taken or reserved**, each range's own state being the register's: **71–79** are **held but unused** — the website work holds *"69 and up"* (`docs/gcp-live-checklist.md`), and no document writes a check in them; **100–104** are **proposed, not approved** (`docs/mobile/README.md`); **105–109** are **reserved and unused** (`docs/mobile/README.md`). The highest check *section written* is **70c** |
| **Check 110** | **claimed by the always-on orchestrator design** (`docs/design/always-on-orchestrator.md`, PR #632, its §7 and Q5) | **claimed, not merged.** This work takes **no check number**, and it must not take 110 while that claim is open: the repository's own rule is that whichever of two open claims merges *second* renumbers itself (`docs/gcp-live-checklist.md`) |

A reserved number is never reused and never renumbered, and that is the register's own rule and
not this work's — the numbering ruling of 2026-10-13, on the register's own line
(`docs/gcp-live-checklist.md`). Neither block above is derived by grep alone: the D-block is read
from the one table in this repository that lists every work's block, and the redesign's proposed
block and the unused gap beside it from that work's own documents; the check numbers from the
paragraphs that allocate them — `docs/gcp-live-checklist.md` for every range up to **70a–70c**
and for mobile's **80–89**, and `docs/mobile/README.md`, `docs/mobile/implementation-plan.md` and
`docs/credentials-service/README.md` for **90–99** and **100–109**, which the checklist's
register does not reach. Every block either row states names the document it is read from, and
both rows are held to the line that document carries it on by
`internal/cli/docs_coordinator_test.go` rather than trusted. One range is read differently by two
documents: **70–79**. `docs/mobile/README.md` records them as unused and not taken — the line
reading *"Checks 70–79 are unused and deliberately not taken"* — which is what that work did: it
took **80–89** instead. The register reads **70** as the website redesign's and **71–79** as
inside the website work's held range, and the row above follows the register, which is the
document that allocates.

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
