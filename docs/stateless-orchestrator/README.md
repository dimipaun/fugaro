# The stateless orchestrator

**Status: plan only; nothing is implemented; no task approved.**

**Spec owner: the owner (Dimi Paun). Date: 2026-11-04.** Checked against `origin/main` at `5998d67c7`.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The owner's spec verbatim: the stateless orchestrator loop and its daemon addendum. Not edited |
| [reconciliation.md](reconciliation.md) | The spec against the code (F1-F23), the hard questions, the decisions **D1220-D1240** with the owner's answers of 2026-11-04, the UNVERIFIED list (U1-U8) and the open tensions (T1-T5) |
| [implementation-plan.md](implementation-plan.md) | Tasks O0-O19 in three slices, the parallel levels, the shared-files table, the owner-run Checks 300-305 and four owner questions |

## The owner's answers

All of D1220-D1240 were answered on 2026-11-04 as recommended, **except D1235 (not the recommendation): the owner chose to cut over at once**,
with no shadow week. Reconciliation section 6 lists what that leaves open: a bad launch rule has no comparison period, and whether a one-command
rollback to the old scripts is wanted is the owner's to rule.

## The number block

**D1220-D1249**, confirmed by the owner. D1220-D1240 are written; **D1241-D1249 are reserved and unused.** Checks **300-309** are reserved for the
plan (300-305 used, 306-309 reserved), verified free on 2026-11-04.

## The spec's typo and numbering fixes

None. The spec was only renamed from `fugaro-stateless-orchestrator-spec.md`; its two references to "fugaro-coordinator-spec" and
"fugaro-mcp-server-spec" are `docs/coordinator/spec.md` and `docs/mcp-server/spec.md`.

## Amended-by notes

`docs/coordinator/README.md` and `docs/central-orchestrator/README.md` each carry a one-line note that this folder amends them (D1236); both
folders exist on `main`, and their owner-verbatim specs are unchanged.

## What a reader should not take from here

Not an approval to build: each task needs the owner's go by name. Not a plan for the hosted MCP state group before the MCP server exists
(T2-T4). Not a shadow-mode migration: the owner chose to cut over at once.
