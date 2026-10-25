# Task governance and periodic run reviews

**Status: DRAFT. Plan only; nothing is implemented; no task approved.** The reconciliation is a
draft whose decisions are not yet answered, and the implementation plan is not written yet.

**Spec owner:** the Fugaro owner. **Dates:** governance spec 2026-10-22; periodic-reviews request
2026-10-25. **Base:** `main` at `d5898f9b8` (`release: v0.11.4`).

Two owner specs are reconciled together here because they read the same data (a run's cost,
outcome, review rounds and halt) and overlap the same existing commands and skill sections.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The task governance spec (2026-10-22), **verbatim** |
| [spec-periodic-reviews.md](spec-periodic-reviews.md) | The periodic run-review request (2026-10-25, dictated), **verbatim** |
| [reconciliation.md](reconciliation.md) | DRAFT. Findings F1–F34 against the code, resolutions R1–R8, decisions D320–D333 (unanswered), open items, UNVERIFIED. **Where it disagrees with either spec, it wins** |
| `implementation-plan.md` | Not written: it follows the owner's answers |

## Typos fixed in the specs

None. Both are byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D320–D359** | this work's decision block. **D320–D333** are written in the draft reconciliation; **D334–D359** are reserved and unused. Taken above the highest decision on `main` (D311, inside the licensing block D300–D319, which is reserved through D319) and in every open PR's docs (D260). D272–D299 stay unused as the licensing work left them |
| **Checks 135–144** | this work's check block, reserved and unused so far. The highest check on `main` is 134 (licensing's reserved 130–134); open PRs reach 124 |

The owner's ruling of 2026-10-25 on the shape of the periodic reviews is recorded in the
reconciliation (§1): a user's reports land in their own repository, redacted; central submission
is opt-in and approved by a person, never automatic; users schedule the reviews themselves; Fugaro
runs no service for it.
