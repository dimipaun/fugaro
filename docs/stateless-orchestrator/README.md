# The stateless orchestrator

**Status: plan only; nothing is implemented; no task approved. Every decision D1220-D1243 and every question the plan raised (Q1-Q4) is answered, and the one thing the owner is still asked to rule is T6** — the offer Q2 puts in `fugaro update`, against the standing no-prompt rule for a managed project (reconciliation section 6).

**Spec owner: the owner (Dimi Paun). Date: 2026-11-04.** Checked against `origin/main` at `5998d67c7`.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's spec verbatim: the stateless orchestrator loop and its daemon addendum. Not edited |
| [reconciliation.md](reconciliation.md) | The spec against the code (F1-F23), the hard questions, the decisions **D1220-D1243** with the owner's answers of 2026-11-04, the UNVERIFIED list (U1-U9) and the open tensions (T1-T6) |
| [implementation-plan.md](implementation-plan.md) | Tasks O0-O19 in three slices, the parallel levels, the shared-files table, the owner-run Checks 300-305 and the owner questions Q1-Q4 |

## The owner's answers

All of D1220-D1243 were answered on 2026-11-04. Most are the recommendation; three rulings are not:

- **D1235: cut over at once**, with no shadow week. The old scripts are kept 14 days and the rollback is a documented, manual one (stop the daemon,
  re-arm the sweep, the watcher and the queue scripts); no rollback command is built.
- **The retry rule: one approval covers the first attempt and at most one automatic retry** of that same approved spec after a first failure, so one
  approval can launch two runs. The spec's "Task failed, first time: Code retries" row stands; a refusal (a failed `Precheck`, the kill switch, a
  spend cap, a validation refusal) is never retried, a second failure needs a person's new approval, and the caps still bound it.
- **Q2: `fugaro update` offers to install the daemon.** That collides with the standing no-prompt rule for a managed project, and reconciliation
  section 6 states both readings as **T6** and asks the owner to rule.

D1241 (the person's own credential writes the `orchestrator/` node), D1242 (the terminal Typed confirmation now, a paired-device channel later) and
D1243 (the session-start print reads a local cache the daemon writes) are all the recommendation, and the gates the plan had put on them are lifted.

## The number block

**D1220-D1249**, confirmed by the owner. D1220-D1243 are written; **D1244-D1249 are reserved and unused.** Checks **300-309** are reserved for the
plan (300-305 used, 306-309 reserved), verified free on 2026-11-04.

## The spec's typo and numbering fixes

None. The spec was only renamed from `fugaro-stateless-orchestrator-spec.md`; its two references to "fugaro-coordinator-spec" and
"fugaro-mcp-server-spec" are `docs/coordinator/spec.md` and `docs/mcp-server/spec.md`.

## Amended-by notes

`docs/coordinator/README.md` and `docs/central-orchestrator/README.md` each carry a one-line note that this folder amends them (D1236); both
folders exist on `main`, and their owner-verbatim specs are unchanged.

## What a reader should not take from here

Not an approval to build: each task needs the owner's go by name. Not a plan for the hosted MCP state group before the MCP server exists
(T2-T4). Not a shadow-mode migration: the owner chose to cut over at once. Not a settled answer to T6: the plan writes Q2's offer in O9 but builds
the managed-project half of it only after the owner rules.
