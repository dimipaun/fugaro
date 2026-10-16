# Fugaro mobile app — the documents

**Status: plan only; nothing is implemented.** No line of an Expo app, no mailbox, no rules change and no shared module exists anywhere in this repository or in any other. This directory holds the owner's spec, the reconciliation of it against the code, and the plan — in the shape the earlier reconciliations use.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-16.

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, **verbatim**, committed alone in its own first commit. Nothing in it was changed, and nothing in it is a statement about this repository. |
| [reconciliation.md](reconciliation.md) | Findings **F1–F37** against the code (each classified **Wrong / Exists / Gap / Rule / Open**, each citing a file and a symbol, each stating its **consequence** for the design), then **decisions D110–D129** for the owner — every row with a recommendation, the alternative and its cost, and an **empty** "Owner's answer" column — then **Open for the owner**, then the **UNVERIFIED** table. |
| [implementation-plan.md](implementation-plan.md) | The plan: ordered parts, each independently mergeable, each with its **verification**, the **files touched** and a **reviewer tier**, plus a **Parallel execution** section of levels and waves with the critical path. |

**Every decision in `reconciliation.md` is unanswered, and no task in the plan may start before the owner answers the ones that gate it and then passes the plan's approval gate.** Answering a decision is not approving a task; the plan says so in its own gate.

## The numbers this work holds

The owner's allocation of 2026-10-16, applied rather than inferred:

| Block | Holder | State |
|---|---|---|
| **D1–D20** | the desktop console ([desktop-console.md](../design/desktop-console.md)), all answered 2026-10-15 | answered, and this work does not reopen them |
| **D21–D38** | the console v2 pair ([desktop-console-v2.md](../design/desktop-console-v2.md)), all answered 2026-10-15/16 | answered |
| **D39–D54, plus D54b** | the voice work ([voice.md](../design/voice.md)), all answered 2026-10-16 | answered |
| **D55–D78** | managed projects ([managed-projects.md](../design/managed-projects.md)) | answered |
| **D90–D109** | the website work | allocated, and not this work's to read or restate |
| **D110–D129** | **this work** | **all twenty open — none is answered, and this work answers none of them itself** |
| **Checks 62–63** | the console v2 pair | reserved |
| **Checks 64–66** | the voice work | reserved; 64 is the one owner-run check that work writes |
| **Checks 67–68** | managed projects | reserved |
| **Checks 69–79** | the website work | allocated (the allocation read "69 onward"; **80–89 is carved out of that range** for this work) |
| **Checks 80–89** | **this work** | **reserved here, and no row or section is written by this pull request** |

**A reserved number is never reused and never renumbered**, which is why this work takes **D110–D129** and **80–89** as allocated rather than deriving either from a grep of `main`. The two blocks do not overlap: the website work holds **69–79** and this work holds **80–89**, which is how the allocation reads once the mobile block is taken out of the "69 onward" it was given — and this document neither renumbers nor narrows anything the website work holds.

## What this work conflicts with, in one paragraph

The spec collides with rulings this repository has already recorded, and **the reconciliation puts each collision in front of the owner as a numbered decision rather than resolving it**. Four are load-bearing: the spec's **inbox/outbox mailbox is exactly the mobile relay Voice D49 ruled "not built and not scheduled"**, because it is a realtime-database write path with no claim, no TTL and no ack (**D110**); a phone that signs in with Firebase Auth and reads the database directly **holds a user credential and bypasses the CLI data plane**, which the console's D1, D2 and D14 and Voice D42 forbid (**D111**, **D112**); the spec's "RTDB rules restrict reads/writes to authorised users" describes a rules world that **does not exist** — the deployed rules grant nothing to a user, every grant is a run token, and `/outcomes/**` has no `.read` at all (**D113**); and "the project's existing Firebase project" is **one Firebase project per installation**, so "multiple accounts" is multiple *installations*, `belong` among them, which no agent may touch (**D114**). **No ruling is silently reversed anywhere in these documents**: where the spec departs from one, the departure is a decision row with an empty answer.

## Related

[voice.md](../design/voice.md) and its plan [2026-10-16-voice.md](../plans/2026-10-16-voice.md) (the voice module the app reuses; D39–D54b), [desktop-console.md](../design/desktop-console.md) and its plan [2026-10-15-desktop-console.md](../plans/2026-10-15-desktop-console.md) (the console's security model, inherited here), [desktop-console-v2.md](../design/desktop-console-v2.md) (the command-channel finding this work's mailbox meets), [managed-projects.md](../design/managed-projects.md) (the D55–D78 gate an always-on orchestrator would have to sit behind), [project-centric.md](../design/project-centric.md) (the installation model), [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §6.2 and §6.4 (the database tree), [SECURITY.md](../../SECURITY.md), [console/docs/json-contract.md](../../console/docs/json-contract.md) (the feed contract the job list reuses), [docs/gcp-live-checklist.md](../gcp-live-checklist.md) (the check register).
