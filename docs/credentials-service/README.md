# Credentials, secrets and security service — the documents

**Status: Slice 0 is built; Slice 1 and later are plan only; decisions answered and O1–O4 settled 2026-10-17; the owner approved Slice 0 on 2026-10-18, and C0.2, C0.3 and C0.6 are built; no other task approved.** The owner answered D130–D154 on 2026-10-17 (all as recommended except **D132**, answered as an accepted owner risk) and ruled the same day on **O1 ("skip Slice 1; go straight to the service")** and **O2** (a `cascade` workflow without the project policy's switch is refused), answered **O3** (the switch is spelled `routing.claude: {via: cascade}`, which needs no provider route while tier C is off and leaves `via: auto` as it is) and confirmed **O4** (the service learns each repository's admitted accounts from what `fugaro project policy publish` publishes). **On 2026-10-18 the owner approved Slice 0, and it is built**: C0.2's supersession notes, C0.3's Checks 90 and 91 written into `docs/gcp-live-checklist.md` (their runs, C0.4 and C0.5, are the owner's), and C0.6's `creds/` denial pinned against today's rules. The implementation plan is written from those answers and rulings. **The product work waits for 0.9.0**: the service is now the first product slice, and it needs the budget-token seal, both halves, on `main` first (D134): #417 (the runner half, merged into `next-0.9` on 2026-10-17) and #523 (the launcher half, an open draft on `next-0.9`). No item is open. Answering a decision or an item is not approving a task: every task needs the owner's explicit go, and no code, Terraform, rules or live action follows from these documents.

**Owner:** the Fugaro owner (`dimipaun`). **Date:** 2026-10-17.

| File | What it is |
|---|---|
| [spec.md](spec.md) | The owner's spec, "Fugaro — Credentials, Secrets and Security Service" (Oct 17, 2026), **verbatim**, committed alone in its own first commit. No typo needed fixing, so nothing in it was changed. |
| [reconciliation.md](reconciliation.md) | Corrections to the brief, findings **F1–F37** against `origin/main` (`021728c1`), each classified **Wrong / Exists / Gap / Rule / Open** with a path and symbol and its consequence; what the parked mixed-credentials design still contributes and what this spec supersedes; **decisions D130–D154**, each with a recommendation, the alternative and its cost, and the **owner's answer of 2026-10-17**; the items O1–O4 found while planning (O1 and O2 ruled, O3 answered `via: cascade`, O4 confirmed, all on 2026-10-17; none open); the **UNVERIFIED** table. Where it disagrees with `spec.md`, it wins. |
| [implementation-plan.md](implementation-plan.md) | The plan: Slice 0 (owner-run Checks 90 and 91; can run before 0.9.0), Slice 1 (the service and the cascade, the only writer of the cascade's state; after the seal, 0.9.0), Slice 2 (GitHub App tokens through the service), Slice 3 (later, unscheduled); the in-runner cascade, declined by O1, kept as a short note; each task with files, tests, a verification command, dependencies, reviewer tier and a commit message; the parallel levels and the critical path. **No Slice 1, 2 or 3 task is approved: the owner's approval of Slice 0 (2026-10-18) is the only one this work records — see the status line at the top and the plan's own.** |

## The numbers this work holds

| Block | State |
|---|---|
| **D130–D159** | the owner allocated **D130–D154**, which are written and answered (2026-10-17). **D155–D159 stand only in this work's own text: this document reserved them by itself, the owner has given no word for them, and no task here uses one.** They are left free rather than claimed — this work's last written decision is D154 — and nothing in the register's 93-style "reserved and unused" sense should be read into them: 93 is a number this work holds and declines to use, whereas D155–D159 are a range this text wrote down as belonging to it. The plan's C0 tasks name no D155–D159, and no later task here will take one without the owner's word |
| **Checks 90–99** | reserved for this work's owner-run checks; the plan assigns each number (90 header swap, 91 Cloud Run streaming, 92 and 94–99 later tasks; 93, the declined in-runner cascade's check, reserved and unused); the block is carved out of the website work's 69-and-up range, the way mobile's 80–89 is, and above 99 the register's standing rule applies (a number is chosen at the moment of merge). Task C0.3 wrote Checks 90 and 91 into `docs/gcp-live-checklist.md`; 92 and 94–99 are written by later tasks |

A reserved number is never reused and never renumbered.

## Related

- [docs/design/mixed-credentials.md](../design/mixed-credentials.md): the parked design this spec answers (D130) — Path B's mechanism is superseded, while its Paths A and C are this work's tiers B and C.
- [docs/plans/2026-10-14-oauth-reviewer-stage.md](../plans/2026-10-14-oauth-reviewer-stage.md): **superseded by D130** (2026-10-17), as its own status banner says; Check 47 stays reserved and unused, and what carries over (the trusted-repository opt-in, the skew constant, the per-stage notional record) is named in the plan's C0.2.
- [docs/design/project-centric.md](../design/project-centric.md) §7 (phase 5, the OAuth pool; D131).
- [docs/design/m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §11 (D2, the external gateway this service is) and [docs/design/budget-token-channel.md](../design/budget-token-channel.md) (the seal, D134).
- [SECURITY.md](../../SECURITY.md) (the claims this service would change, D148).
