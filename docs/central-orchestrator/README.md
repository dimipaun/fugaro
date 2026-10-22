# Central orchestrators — the documents

**Status: plan only; no code is implemented.** This directory holds the owner's spec for
**central orchestrators** — Fugaro orchestrators that run permanently in the cloud instead of
on a developer's machine (`spec.md`, verbatim) — the reconciliation of that spec against the
code at `origin/main` **`2f8b4dca`**, the decisions the reconciliation could not answer on its
own (**D1–D26**, all open), and the plan that follows from them
(`implementation-plan.md`). No Go, no TypeScript, no Terraform, no rules change, no
`.github/workflows` change and no live action follows from these documents. **Owner: Dimi
Paun. Date: 2026-10-20. Base: `main` at `2f8b4dca`** — the commit every path and symbol below
was read at, and this branch's own merge base.

**Nothing here is approved.** The owner wrote the spec; the reconciliation and the plan are
this work's. No decision has been answered, no part of the plan has been accepted, and the
plan's owner gate at the end is unpassed. The two siblings this work depends on are also
unbuilt: the always-on orchestrator design is in **open PR #632** (with the rigor modes work
in **open PR #773**), and `docs/credentials-service/` and `docs/coordinator/` are each
*"plan only; nothing is implemented"* in their own words.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | **The owner's spec, verbatim** — *"Fugaro Central Orchestrator Spec"*, Oct 20 2026 · @Dimi, from Purpose through Resolved questions. Committed alone, in its own first commit, before anything else in this directory. Nothing was reworded, reordered, re-headed or re-numbered. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief this work was written from (**C1–C7**); the spec's concepts against what the repository has (§1); the spec's own open item — *"check how much state the orchestrator persists today"* — **answered from the code** (§2: the shepherding state that exists today, in a table, with what a second orchestrator could and could not pick up); findings **#1–#57**, each classified **Wrong / Exists / Gap / Rule / Open**, each citing a path and a symbol, each stating its consequence (§3); the exact edits the mobile functional spec still needs, **listed and not made** (§4); decisions **D1–D26**, each with its recommendation first, the alternative and its cost (§5); the same decisions tabled as **Open for the owner** (§6); the numbers this work would hold, with the check register read **name by name** (§7); items **O1–O4**, found while planning and kept so they are not lost (§8); the **UNVERIFIED** table U1–U13 (§9); the glossary the spec's words need (§10). Where it disagrees with `spec.md`, it wins. |
| [implementation-plan.md](implementation-plan.md) | The plan: ordered **parts P0–P9**, each independently mergeable, each with its files, its red step, its verification, its dependencies, its reviewer tier and its commit message; which parts are **SECURITY-SENSITIVE** and need an independent review; the edges and the critical path; what is deliberately **not** in it; the owner-run live checks **proposed by name and not numbered**; and the **OWNER APPROVAL GATE**, unpassed. |

## Typos fixed in `spec.md`

**None.** The spec is copied byte for byte from the text the owner supplied and it needed no
correction: a scan for doubled words, trailing whitespace, unbalanced emphasis and suspect
spellings found nothing, and every mode name, command name and number in it is written
exactly as the tree or the owner writes it. Nothing was reworded, reordered, re-headed or
re-numbered, and no sentence was added. **If a later reader finds a difference between
`spec.md` and the owner's copy, the owner's copy is right and `spec.md` is the file to fix.**

## What this work holds, in one paragraph

**The spec is a good idea whose deployment section describes a surface Fugaro has never
had, and whose credential section depends on work that does not exist.** There is no Cloud
Run **service** anywhere in this repository — every workload is a Cloud Run *job*, and the
type is not only absent but **refused**: `internal/infra/tf/cover.go`'s `allowedTypes` admits
only `google_cloud_run_v2_job` and `google_cloud_run_v2_job_iam_member`, and
`Cover.NotCovered` makes any other type a typed confirmation at `fugaro init` (#1). There is
no WebSocket in either direction (#3), no lease record of any kind (#6), no merge path that a
service could use (#22), no public sign-in provider — `internal/cli/init_firebase.go`'s
`refusePublicSignUp` refuses every one of them (#32) — no notification sender (#34), no alert
ingest (#41) and no Firestore TTL (#49). Its *"credentials … from the security service"* names
a plan document, not a service (C3, #21), and its *"one Cloud Run service"* collides with the
always-on orchestrator design in open PR #632, which deliberately keeps the agent **out** of
the resident process and holds no model credential (§3 #52, D20). What the spec gets right is
what it says about **shepherding**: one job, one shepherd is a real invariant, the takeover
protocols to build on already exist in two places in generation-matched compare-and-set form
(`internal/cli/run.go`'s `takeClaim`, `internal/lock/lock.go`'s `Takeover`), the RTDB client
already knows the **database's** clock (`internal/rtdb/client.go`'s `ServerNow`) so a lease
need not trust any machine's `time.Now()`, and the budget's sealed per-run identity
(`internal/budget/token/`, on `main`) is a working model for what a service's identity would
have to become.

## The numbers this work holds

| Numbers | Whose | State |
| --- | --- | --- |
| **D1–D26** | **this work's decisions**, folder-local and distinct by folder, as `docs/windows-cli/` (its D1–D16) and `docs/rigor-modes/` (its D1–D19) each chose and each recorded. Every one is **open**: the owner has answered none, and the reconciliation's §6 tables them with the recommendation first. Answering a decision is not approving a task | **open.** The register is [docs/coordinator/README.md](../coordinator/README.md) §"The numbers this work holds": **D1–D192 are taken** (the highest written anywhere in `docs/` is **D192**), **D79–D89 is deliberately unused** — *"an unused number is not a free number"* — and **D193 onward is free**, which is the block this work would take if the owner prefers one sequence. **Recommendation: keep D1–D26 local and distinct by folder** (D23) |
| **No check number** | this work proposes **none** | **not claimed and not written.** The register's own state is unsettled where this work would sit: **Check 110** is claimed **twice** — by `docs/mobile/design/voice-evaluation-switch.md` (P5.3) and by the always-on orchestrator design in **PR #632** (§7) — and written by neither, so the register's rule *"whichever of two open claims merges second renumbers itself"* applies; **111** is written by [docs/setup-wizard/](../setup-wizard/); **112** is proposed by [docs/windows-cli/](../windows-cli/); **113–118** are taken by [docs/mobile/voice-session/](../mobile/voice-session/); **119** is taken by `docs/rigor-modes/` in open PR #773. The owner-run checks this work needs are therefore **named and not numbered** (implementation-plan §7), and the part that writes one takes the next free number at merge time (D24) |

Check numbers are allocated, never reused and never renumbered once taken — the live
checklist's own numbering ruling (`docs/gcp-live-checklist.md`). **Decision numbers follow the
same rule and state it in their own document**: *"a decision number, once allocated, is never
reused or renumbered either"*, with the allocation made by the owner in advance to a line of
work rather than to whoever answers first (`docs/design/desktop-console-v2.md`).

## Related

- `docs/mobile/design/always-on-orchestrator.md` — **the close sibling**: one resident Cloud
  Run service per installation, `min-instances = 0`, a Cloud Scheduler tick, *"no model
  credential of its own, ever"*, and *"the agent does not run in the resident service"*. **It
  is in open PR #632 and not on `main`**, so there is no link to it here. Its Q1–Q13 were
  answered 2026-10-19. Reconciliation §3 #52 and **D20**, which is the one place the two
  designs cannot both be built.
- [docs/mobile/reconciliation.md](../mobile/reconciliation.md) — **D115**: phase 2 deferred
  until after phase 1 ships and after managed-projects' gate, with the conditions the
  orchestrator would have to meet (a credential in the installation's own secret manager, a
  dedicated identity, and **behind the same launch claim and the same budget caps**). The
  central orchestrator *is* that question (§3 #53).
- `docs/rigor-modes/` (**open PR #773**, not on `main`, so no link here) — the five modes this
  spec's escalation table names (`POC`, `Startup`, `Pro`, `Critical`, `Hotfix`), and the one place
  the spec's *"Escalation timeouts (resolved)"* has no home yet: that work builds no
  scheduler and never mentions a timeout (C6, §3 #39, **D8**).
- [docs/credentials-service/](../credentials-service/README.md) — *the* "security service"
  the spec's Interaction channel and Submission auth name. Plan only; D130–D154 answered
  2026-10-17; nothing implemented (C3, §3 #21).
- [docs/coordinator/](../coordinator/README.md) — the sibling document set, and the register
  of every D-block and check range. The coordinator is *"advisory, not authoritative … It
  never blocks or reorders merges"*, and unbuilt; the spec's *"every orchestrator … reports to
  the coordinator"* therefore describes a path with no reader (§3 #51, **D14**). **Added
  2026-10-21:** the coordinator is **not** a role this work's service plays — the owner answered
  that work's D196 **(B)**, so it gets **its own** resident service and every orchestrator,
  this one included, reaches it **as a client**; this plan's P4/P5 no longer block it.
- [docs/design/v1.md](../design/v1.md) §2 — the non-goal the spec's *"carries PRs through to
  merge"* contradicts: *"Auto-merge. Fugaro never merges."* (§3 #22, **D18**).
- [SECURITY.md](../../SECURITY.md) — the trust assumptions every credential question here
  inherits: the launcher's reach, that a run is the untrusted party, and the accepted
  blast-radius paragraphs of `docs/design/project-centric.md` §4.1 (§3 #20, #46, **D4**).
- [docs/design/desktop-console.md](../design/desktop-console.md) §5.1 and
  [docs/design/desktop-console-v2.md](../design/desktop-console-v2.md) — the console's rules
  that bound what a client of this service may be: the renderer never talks to the network
  (`console/src/main/protocol.ts`'s `connect-src 'none'`), and the IPC surface is a fixed list
  (§3 #29, #30, **D5**).
- [docs/mobile/functional-spec-v0.1.md](../mobile/functional-spec-v0.1.md) — the file the
  spec's Follow-up bullet means by `fugaro-mobile-app-spec.md`, which does not exist. §4 of the
  reconciliation lists the six (plus one) edits it still needs; **this work makes none of
  them** (C5, §4, **D22**).
- [docs/gcp-live-checklist.md](../gcp-live-checklist.md) — the register whose numbering this
  work reads but does not take from (§7, **D24**).
- [docs/setup-wizard/](../setup-wizard/), [docs/windows-cli/](../windows-cli/) and
  [docs/credentials-service/](../credentials-service/) — the folder shape this one follows:
  a README index with a status line, `spec.md` verbatim and committed alone,
  `reconciliation.md` with numbered findings and decisions, and `implementation-plan.md` with
  ordered parts, red steps and an owner gate.
