# Credentials, secrets and security service — the documents

**Status: plan only; nothing is implemented; decisions answered and O1/O2 ruled 2026-10-17; no task approved.** The owner answered D130–D154 on 2026-10-17 (all as recommended except **D132**, answered as an accepted owner risk) and ruled the same day on **O1 ("skip Slice 1; go straight to the service")** and **O2** (a `cascade` workflow without the project policy's switch is refused). The implementation plan is written from those answers and rulings. **The product work waits for 0.9.0**: the service is now the first product slice, and it needs the budget-token seal, both halves (#417 and its launcher half), first (D134). Two items stay open (O3, a choice; O4, a reading to confirm). Answering a decision is not approving a task: every task needs the owner's explicit go, and no code, Terraform, rules or live action follows from these documents.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-17.

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, "Fugaro — Credentials, Secrets and Security Service" (Oct 17, 2026), **verbatim**, committed alone in its own first commit. No typo needed fixing, so nothing in it was changed. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief, findings **F1–F37** against `origin/main` (`aa3adc51`), each classified **Wrong / Exists / Gap / Rule / Open** with a path and symbol and its consequence; what the parked mixed-credentials design still contributes and what this spec supersedes; **decisions D130–D154**, each with a recommendation, the alternative and its cost, and the **owner's answer of 2026-10-17**; the open items O1–O4 (O1 and O2 ruled by the owner on 2026-10-17, O3 and O4 open); the **UNVERIFIED** table. Where it disagrees with `spec.md`, it wins. |
| [implementation-plan.md](implementation-plan.md) | The plan: Slice 0 (owner-run Checks 90 and 91; can run before 0.9.0), Slice 1 (the service and the cascade, the only writer of the cascade's state; after the seal, 0.9.0), Slice 2 (GitHub App tokens through the service), Slice 3 (later, unscheduled); the in-runner cascade, declined by O1, kept as a short note; each task with files, tests, a verification command, dependencies, reviewer tier and a commit message; the parallel levels and the critical path. **No task is approved.** |

## The numbers this work holds

| Block | State |
|---|---|
| **D130–D159** | allocated to this work; D130–D154 are written and answered (2026-10-17); D155–D159 unused and reserved |
| **Checks 90–99** | reserved for this work's owner-run checks; the plan assigns each number (90 header swap, 91 Cloud Run streaming, 92 and 94–99 later tasks; 93, the declined in-runner cascade's check, reserved and unused); no check section is written by this pull request (Task C0.3 and later tasks write them into `docs/gcp-live-checklist.md`) |

A reserved number is never reused and never renumbered.

## Related

- [docs/design/mixed-credentials.md](../design/mixed-credentials.md) and the held [docs/plans/2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md): the parked design this spec answers (D130).
- [docs/design/project-centric.md](../design/project-centric.md) §7 (phase 5, the OAuth pool; D131).
- [docs/design/m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §11 (D2, the external gateway this service is) and [docs/design/budget-token-channel.md](../design/budget-token-channel.md) (the seal, D134).
- [SECURITY.md](../../SECURITY.md) (the claims this service would change, D148).
