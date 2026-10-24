# Licensing (open core)

**Status: plan only; nothing is implemented; no task approved.** Every decision was answered by the
owner on 2026-10-24; each task in the plan still needs the owner's own go.

Spec owner: the repository owner. Spec received 2026-10-24; reconciled against `origin/main` at
`e924481cd` the same day.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim (no typo fixes) |
| [reconciliation.md](reconciliation.md) | Findings F1–F27 against the code and the published licence texts, resolutions R1–R8, decisions D300–D310 with the owner's answers, open tension T1 and items O1–O3, UNVERIFIED U1–U8. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices L0–L3, tasks with their tests and dependencies, levels, the critical path, the shared-files table. Nothing in it is approved |

## The numbers this work holds

| Numbers | State |
| --- | --- |
| **D300–D319** | this work's block. **D300–D310** written and answered 2026-10-24 (D305 not the recommendation); **D311–D319** reserved and unused. Taken above the highest decision written under `docs/` on `main` (D271) and in open PRs (D260); **D272–D299** are left unused so that reconciliations started the same day cannot collide with this block |
| **Checks 130–134** | this work's block. **130** written by plan task L1.10 and run by the owner (L1.11); **131–134** reserved and unused. Taken above the highest check on `main` (123) and in open PRs (124) |

## Headline

The core stays MIT and the repository reserves `ee/` for net-new organisation features under their
own licence. What ships with CLI 0.11.0 is the skeleton only: the root scope statement, a README
Licensing section, the website card, the vendored skills' licence, the Go boundary tests, the
contribution terms, and a staged workflow. The `ee/LICENSE` text waits on counsel and on open
tension T1; without it, `ee/` ships reserved.
