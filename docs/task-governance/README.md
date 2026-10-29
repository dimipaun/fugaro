# Task governance and periodic run reviews

**Status (2026-10-29): partly implemented, and what remains is born in `ee/`.** On `main`, task by
task: T1.1–T1.4, T2.1–T2.3, T3.1, T3.2, T3.3, T3.5, T3.6, T4.1–T4.3, T5.1, T5.2, T6.1, T6.2,
T7.1, T7.6 and T8.1 (both halves: T8.1a's tier vocabulary and fields, T8.1b's `fugaro run --tier`).
**T3.4 and T4.4 are not started.** **The remaining governance tasks — T7.2–T7.5, T8.2 and T8.3's
calibration half — are re-targeted to `ee/governance` (D527, D530): they are written there rather
than moved into it, behind the core hooks the ee-reorg plan's E1.2 adds (and the gateway
stage-limit hook T7.2 wires first), and none is started.** The wire and the mechanism stay in
`internal/` under MIT, the policy goes to `ee/governance`; T8.3's `ExpectedScope` drift flag stays
in the MIT `internal/digest` (D528), and **D523 defers every licence notice and warning** until the
verifier, the key-minting service and the launch are ready.
[implementation-plan.md](implementation-plan.md) §"Where the remaining tasks live" names the file
and the hook for each. The
rigor plan's T4.2 (a run's report listing the findings its mode tolerated, #1156) is on `main` too,
and is not a task of this plan. Every stage's record carries `calls` (the gateway's stage report,
absent without a gateway), `agents` (the sub-agent tool calls the relay counted on the stage's
own stream, for every auth mode) and `repeats` (the most times one identical shell command ran in
the stage, the relay's count, T1.4); the gateway's count of distinct agent IDs
(`gateway.StageReport.Agents`, T1.2) is never recorded, and nothing reads it yet: T7.2's fan-out
stop is what will. `internal/digest` builds a period's runs as a redacted, deterministic report
(T2.1) and raises the two report-only flags T1.4 feeds — a stage at five repeats or more, and a
review-scope drift. The owner answered decisions D320–D333 on 2026-10-26 (D320 not the
recommendation), which approves the reconciliation; each task of the plan still needs the owner's
own explicit go.

**Spec owner:** the Fugaro owner. **Dates:** governance spec 2026-10-22; periodic-reviews request
2026-10-25. **Base:** `main` at `d5898f9b8` (`release: v0.11.4`).

Two owner specs are reconciled together here because they read the same data (a run's cost,
outcome, review rounds and halt) and overlap the same existing commands and skill sections.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The task governance spec (2026-10-22), **verbatim** |
| [spec-periodic-reviews.md](spec-periodic-reviews.md) | The periodic run-review request (2026-10-25, dictated), **verbatim** |
| [reconciliation.md](reconciliation.md) | Findings F1–F38 against the code, resolutions R1–R8, decisions D320–D333 with the owner's answers of 2026-10-26, D320 followed through (§5.1), tensions T1–T3, items O1–O9, UNVERIFIED U1–U8. **Where it disagrees with either spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Eight slices, 30 tasks, six parallel levels, the critical path, the shared-files table, Checks 135–139, and (2026-10-29) the `ee/` re-target of what remains (D527, D530). Partly implemented; each remaining task still needs the owner's own explicit go |

## Typos fixed in the specs

None. Both are byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D320–D359** | this work's decision block. **D320–D333** are written and answered; **D334–D359** are reserved and unused. Taken above the highest decision on `main` (D311, inside the licensing block D300–D319, which is reserved through D319) and in every open PR's docs (D260). D272–D299 stay unused as the licensing work left them |
| **Checks 135–144** | this work's check block. **135, 136 and 139** are written by T3.5 and on `main` (owner-run, NOT RUN); **137 and 138** are T7.5's and unwritten; **140–144** reserved and unused. The block sits above 134 (licensing's reserved 130–134) and the register's highest written section is 149 |

The owner's ruling of 2026-10-25 on the shape of the periodic reviews is recorded in the
reconciliation (§1): a user's reports land in their own repository, redacted; central submission
is opt-in and approved by a person, never automatic; users schedule the reviews themselves; Fugaro
runs no service for it.
