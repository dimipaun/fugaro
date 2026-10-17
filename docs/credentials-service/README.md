# Credentials, secrets and security service — the documents

**Status: plan only; nothing is implemented; decisions answered 2026-10-17; no task approved.** The owner answered D130–D154 on 2026-10-17 (all as recommended except **D132**, answered as an accepted owner risk), and the implementation plan is written from those answers. Answering a decision is not approving a task: every task needs the owner's explicit go, and no code, Terraform, rules or live action follows from these documents.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-17.

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, "Fugaro — Credentials, Secrets and Security Service" (Oct 17, 2026), **verbatim**, committed alone in its own first commit. No typo needed fixing, so nothing in it was changed. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief, findings **F1–F37** against `origin/main` (`aa3adc51`), each classified **Wrong / Exists / Gap / Rule / Open** with a path and symbol and its consequence; what the parked mixed-credentials design still contributes and what this spec supersedes; **decisions D130–D154**, each with a recommendation, the alternative and its cost, and the **owner's answer of 2026-10-17**; two new open items (O1, O2) where two answers meet; the **UNVERIFIED** table. Where it disagrees with `spec.md`, it wins. |
| [implementation-plan.md](implementation-plan.md) | The plan: Slice 0 (owner-run Checks 90 and 91), Slice 1 (the in-runner cascade), Slice 2 (the service), Slice 3 (GitHub App tokens through the service), Slice 4 (later, unscheduled); each task with files, tests, a verification command, dependencies, reviewer tier and a commit message; the parallel levels and the critical path. **No task is approved.** |

## The numbers this work holds

| Block | State |
|---|---|
| **D130–D159** | allocated to this work; D130–D154 are written and answered (2026-10-17); D155–D159 unused and reserved |
| **Checks 90–99** | reserved for this work's owner-run checks; the plan assigns each number (90 header swap, 91 Cloud Run streaming, 92–98 later slices, 99 unused); no check section is written by this pull request (Task C0.3 and later tasks write them into `docs/gcp-live-checklist.md`) |

A reserved number is never reused and never renumbered.

## Related

- [docs/design/mixed-credentials.md](../design/mixed-credentials.md) and the held [docs/plans/2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md): the parked design this spec answers (D130).
- [docs/design/project-centric.md](../design/project-centric.md) §7 (phase 5, the OAuth pool; D131).
- [docs/design/m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §11 (D2, the external gateway this service is) and [docs/design/budget-token-channel.md](../design/budget-token-channel.md) (the seal, D134).
- [SECURITY.md](../../SECURITY.md) (the claims this service would change, D148).
