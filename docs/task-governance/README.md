# Task governance and periodic run reviews

**Status (2026-10-26): partly implemented — T1.1, T1.2, T1.3 and T2.1 are on `main`; no other
task has merged.** Every stage's record carries `calls` (the gateway's stage report, absent without
a gateway) and `agents` (the sub-agent tool calls the relay counted on the stage's own stream, for
every auth mode); the gateway's count of distinct agent IDs (`gateway.StageReport.Agents`, T1.2) is
never recorded, and nothing reads it yet: T7.2's fan-out stop is what will. `internal/digest`
builds a period's runs as a redacted, deterministic report (T2.1). The owner answered decisions
D320–D333 on 2026-10-26 (D320 not the recommendation), which approves the reconciliation; each task
of the plan still needs the owner's own explicit go.

**Spec owner:** the Fugaro owner. **Dates:** governance spec 2026-10-22; periodic-reviews request
2026-10-25. **Base:** `main` at `d5898f9b8` (`release: v0.11.4`).

Two owner specs are reconciled together here because they read the same data (a run's cost,
outcome, review rounds and halt) and overlap the same existing commands and skill sections.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The task governance spec (2026-10-22), **verbatim** |
| [spec-periodic-reviews.md](spec-periodic-reviews.md) | The periodic run-review request (2026-10-25, dictated), **verbatim** |
| [reconciliation.md](reconciliation.md) | Findings F1–F38 against the code, resolutions R1–R8, decisions D320–D333 with the owner's answers of 2026-10-26, D320 followed through (§5.1), tensions T1–T3, items O1–O8, UNVERIFIED U1–U8. **Where it disagrees with either spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Eight slices, 30 tasks, six parallel levels, the critical path, the shared-files table, Checks 135–139. Nothing in it is approved |

## Typos fixed in the specs

None. Both are byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D320–D359** | this work's decision block. **D320–D333** are written and answered; **D334–D359** are reserved and unused. Taken above the highest decision on `main` (D311, inside the licensing block D300–D319, which is reserved through D319) and in every open PR's docs (D260). D272–D299 stay unused as the licensing work left them |
| **Checks 135–144** | this work's check block. **135–139** are proposed by the plan (written by tasks T3.5 and T7.5, run by the owner, NOT RUN); **140–144** reserved and unused. The highest check on `main` is 134 (licensing's reserved 130–134); open PRs reach 124 |

The owner's ruling of 2026-10-25 on the shape of the periodic reviews is recorded in the
reconciliation (§1): a user's reports land in their own repository, redacted; central submission
is opt-in and approved by a person, never automatic; users schedule the reviews themselves; Fugaro
runs no service for it.
