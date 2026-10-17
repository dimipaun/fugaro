# Fugaro mobile app — the documents

**Status (2026-10-17, later): partly implemented — P0.1, P0.2, P0.3, P1.2, P1.3 and P1.4 are merged on `main` (`shared/`); the owner approved Phase 2 (P2.1, P2.2) on 2026-10-17 and P2.1 is draft PR #532; everything else is plan only, and no task beyond the approved ones is approved.** The owner's functional spec v0.1 is added and reconciled ([functional-spec-reconciliation.md](functional-spec-reconciliation.md)): it opens decisions **D160–D172** and tensions **T1–T5**, and records the owner's **amendment of D128 (2026-10-17)** — cancel and approve/merge allowed on the phone with a spoken read-back and an explicit "confirm". Its tasks are **Phase 6 of the plan, proposed and NOT APPROVED**; this changes no approval.

*The status as first written, kept:* **Status: plan only; nothing is implemented. The owner answered decisions D110–D129 and resolved the three tensions between them on 2026-10-17; no task is approved.** No line of an Expo app, no mailbox, no rules change and no shared module exists anywhere in this repository or in any other. This directory holds the owner's spec, the reconciliation of it against the code, and the plan — in the shape the earlier reconciliations use.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-16. **Functional spec v0.1:** the same owner, draft of October 2026, reconciled 2026-10-17.

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, **verbatim**, committed alone in its own first commit. Nothing in it was changed, and nothing in it is a statement about this repository. |
| [reconciliation.md](reconciliation.md) | Findings **F1–F37** against the code (each classified **Wrong / Exists / Gap / Rule / Open**, each citing a file and a symbol, each stating its **consequence** for the design), then **decisions D110–D129** (twenty-two rows, D113b and D113c among them) for the owner — every row with a recommendation, the alternative and its cost, and the "Owner's answer" column, filled with the owner's answers of 2026-10-17 — then **Open for the owner** (the three tensions those answers left, each now marked resolved 2026-10-17), then the **UNVERIFIED** table. |
| [implementation-plan.md](implementation-plan.md) | The plan: ordered parts, each independently mergeable, each with its **verification**, the **files touched** and a **reviewer tier**, plus a **Parallel execution** section of levels and waves with the critical path. |
| [functional-spec-v0.1.md](functional-spec-v0.1.md) | The owner's "Fugaro Mobile App — Functional Spec (v0.1)" (draft, October 2026), **verbatim**, committed alone in its own first commit. No typo needed fixing. |
| [functional-spec-reconciliation.md](functional-spec-reconciliation.md) | The functional spec against `docs/mobile/` and the code at `49712fed`: a concept table, findings **G1–G22** (Wrong / Exists / Gap / Rule / Open), the owner's D128 amendment, every spec §6 question mapped to an existing answer or to new decisions **D160–D172** (recommendation, alternative and cost, empty answer column), open tensions **T1–T5**, open items O1–O3 and UNVERIFIED. |

**Every decision in `reconciliation.md` was answered by the owner on 2026-10-17** — D112 against the recommendation (direct RTDB reads) and D113c as option (ii) (a new `fugaro` command) — **and no task in the plan may start before the owner passes the plan's approval gate**, which has not happened. Answering a decision is not approving a task; the plan says so in its own gate. The three tensions between the answers were **resolved by the owner on 2026-10-17** (reconciliation §4: direct reads under the feed's reader rules, a four-family read grant with no run titles, the per-pairing MAC key confirmed, and the command named `fugaro mailbox`); that approves no task.

## The numbers this work holds

The owner's allocation of 2026-10-16, applied rather than inferred:

| Block | Holder | State |
|---|---|---|
| **D1–D20** | the desktop console ([desktop-console.md](../design/desktop-console.md)), all answered 2026-10-15 | answered, and this work does not reopen them |
| **D21–D38** | the console v2 pair ([desktop-console-v2.md](../design/desktop-console-v2.md)), all answered 2026-10-15/16 | answered |
| **D39–D54, plus D54b** | the voice work ([voice.md](../design/voice.md)), all answered 2026-10-16 | answered |
| **D55–D78** | managed projects ([managed-projects.md](../design/managed-projects.md)) | answered |
| **D90–D109** | the website work | allocated, and not this work's to read or restate |
| **D110–D129** | **this work** | **all answered by the owner on 2026-10-17** (this work answered none of them itself). The reconciliation carries **twenty-two rows**: the twenty allocated, plus **D113b** and **D113c** — sub-numbers added the way this repository's own **D54b** was, because the writer of the outbox and the heartbeat, and where the consumer lives, had no rows of their own and D130 is outside the allocation |
| **Checks 62–63** | the console v2 pair | reserved |
| **Checks 64–66** | the voice work | reserved; 64 is the one owner-run check that work writes |
| **Checks 67–68** | managed projects | reserved |
| **Checks 69a–69c** | the website work | allocated, written as 69a–69c as the website plan on `main` does (the allocation read "69 onward"; **80–89 is carved out of that range** for this work) |
| **Checks 80–89** | **this work** | **reserved here, and no row or section is written by this pull request** |
| **D130–D159**, **checks 90–99** | the credentials service ([credentials-service](../credentials-service/README.md)) | answered; D155–D159 reserved |
| **D160–D179** | **the functional spec v0.1** (this directory) | **D160–D172 open** for the owner; **D173–D179 reserved and unused**. Taken after reading the highest numbers on `main` (`49712fed`) and in every open pull request |
| **Checks 100–109** | **the functional spec v0.1** | **100–104 proposed** (Phase 6, P6.11 writes them); **105–109 reserved and unused**. Checks 70–79 are unused and deliberately not taken |

**A reserved number is never reused and never renumbered**, which is why this work takes **D110–D129** and **80–89** as allocated rather than deriving either from a grep of `main`. The two blocks do not overlap: the website work holds **69a–69c** and this work holds **80–89**, which is how the allocation reads once the mobile block is taken out of the "69 onward" it was given — and this document neither renumbers nor narrows anything the website work holds.

## What this work conflicts with, in one paragraph

The spec collides with rulings this repository has already recorded, and **the reconciliation puts each collision in front of the owner as a numbered decision rather than resolving it**. Four are load-bearing: the spec's **inbox/outbox mailbox is exactly the mobile relay Voice D49 ruled "not built and not scheduled"**, because it is a realtime-database write path with no claim, no TTL and no ack (**D110**); a phone that signs in with Firebase Auth and reads the database directly **holds a user credential and bypasses the CLI data plane**, which the console's D1, D2 and D14 and Voice D42 forbid (**D111**, **D112**); the spec's "RTDB rules restrict reads/writes to authorised users" describes a rules world that **does not exist** — the deployed rules grant nothing to a user, every grant is a run token, and `/outcomes/**` has no `.read` at all (**D113**); and "the project's existing Firebase project" is **one Firebase project per installation**, so "multiple accounts" is multiple *installations*, `belong` among them, which no agent may touch (**D114**). **No ruling is silently reversed anywhere in these documents**: where the spec departs from one, the departure is a decision row, and the owner's answers of 2026-10-17 are recorded in those rows — with the amendments they owe to [voice.md](../design/voice.md) (D40, D42, D49, D50) and to the console designs (D112) made in the same docs-only change, each keeping the original text visible (D129).

## Related

[voice.md](../design/voice.md) and its plan [2026-10-16-voice.md](../plans/2026-10-16-voice.md) (the voice module the app reuses; D39–D54b), [desktop-console.md](../design/desktop-console.md) and its plan [2026-10-15-desktop-console.md](../plans/2026-10-15-desktop-console.md) (the console's security model, inherited here), [desktop-console-v2.md](../design/desktop-console-v2.md) (the command-channel finding this work's mailbox meets), [managed-projects.md](../design/managed-projects.md) (the D55–D78 gate an always-on orchestrator would have to sit behind), [project-centric.md](../design/project-centric.md) (the installation model), [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §6.2 and §6.4 (the database tree), [SECURITY.md](../../SECURITY.md), [console/docs/json-contract.md](../../console/docs/json-contract.md) (the feed contract the job list reuses), [docs/gcp-live-checklist.md](../gcp-live-checklist.md) (the check register).
