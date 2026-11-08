# Post-review triage

**Status: plan only; nothing is implemented; no task approved.**

**Spec owner:** Dimi Paun
**Written:** 2026-11-08
**Base:** `main` at `2232815b0`

A triage step between review and fix: the reviewer still reports everything, a triage agent decides per finding whether
a person in the mode's persona would hold the merge for it, and only the blocking findings go to the fixer. The rest are
deferred and become follow-up tasks.

| Document | What it is |
| --- | --- |
| [spec.md](spec.md) | the owner's spec, verbatim, in its own commit (no typo was fixed) |
| [reconciliation.md](reconciliation.md) | F1-F20 findings against the code, decisions D1800-D1816 with the owner's answers, open tensions, UNVERIFIED; where it differs from the spec, it wins |
| [implementation-plan.md](implementation-plan.md) | four slices, thirteen tasks with their files, tests, red steps, levels, critical path and shared-files table |

## In one paragraph

The repository already decides "which findings block" in code (`rigor.Blocks`, `rigor.Split`), has the five modes, per-mode
review caps and a record of what a mode tolerated. It has no second judge, no fixer that sees only blockers, no way to turn
a deferral into a task and no merge (Fugaro opens pull requests and never merges, D13). So the plan layers the spec's judge
*inside* the existing floor (it can only defer among findings the table blocks; security, data loss, impact 5 and
`needs_owner` are never deferred), runs it first in shadow so its verdicts can be read before they steer the loop, records
deferrals in the result record, and adds one CLI command that launches them as tasks once the parent is merged. "Merges right
away" reads "is ready" here.

## Numbers this work holds

Decisions **D1800-D1829** (D1800-D1816 used) and checks **470-479** (470-472 used). The external prerequisite is BF1m (#1999),
which edits the files most runner and config tasks write.

## Notes on the spec

Two of its lines are amended by the reconciliation: Hotfix defers everything except what the floor keeps (security still
blocks), and Critical is not triaged (it tolerates nothing, D7). Its acceptance 1 reads "ready after one review".
