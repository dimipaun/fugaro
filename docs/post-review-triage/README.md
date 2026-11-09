# Post-review triage

**Status: plan only; nothing is implemented; no task approved.**

**Spec owner:** Dimi Paun
**Written:** 2026-11-08
**Base:** `main` at `2232815b0` (re-merged with `main` through round 3)

A triage step between review and fix: the reviewer still reports everything, a triage agent decides per finding whether
a person in the mode's persona would hold the merge for it, and only the blocking findings go to the fixer. The rest are
deferred and become follow-up tasks.

| Document | What it is |
| --- | --- |
| [spec.md](spec.md) | the owner's spec, verbatim, in its own commit (no typo was fixed) |
| [reconciliation.md](reconciliation.md) | F1-F32 findings against the code, decisions D1840-D1856 with the owner's answers, open tensions, UNVERIFIED; where it differs from the spec, it wins |
| [implementation-plan.md](implementation-plan.md) | four slices, thirteen tasks with their files, tests, red steps, levels, critical path and shared-files table |

## In one paragraph

The repository already decides "which findings block" in code (`rigor.Blocks`, `rigor.Split`), has the five modes, per-mode
review caps and a record of what a mode tolerated. It has no second judge, no fixer that sees only blockers, no way to turn
a deferral into a task and no merge (Fugaro opens pull requests and never merges, D13). So the plan layers the spec's judge
*inside* the existing floor (it can only defer among findings the table blocks; security, data loss, impact 5 and
`needs_owner` are never deferred, with data loss and impact 5 outside Hotfix), runs it first in shadow so its verdicts can be read before they steer the loop, records
deferrals in the result record, and adds one CLI command that launches them as tasks once the parent is merged. "Merges right
away" reads "is ready" here.

## Numbers this work holds

Decisions **D1840-D1869** (D1840-D1857 used) and checks **490-499** (490-492 used). The external prerequisite is BF1m (#1999),
which edits the files most runner and config tasks write.

## Notes on the spec

Two of its lines are amended by the reconciliation: in Hotfix the judge can defer nothing (security is floor and the table
blocks nothing else, so Hotfix is not triaged, D1851), and Critical is not triaged (it tolerates nothing, D7). Its acceptance 1 reads "ready after one review".

## Numbering

The block was renumbered twice after the owner's answers of 2026-11-08, because other folders reserved overlapping ranges
(first #2046's D1780-D1809 and checks 470-479, then #2050's D1810-D1839 and checks 480-489). The decisions the owner answered as
D1800-D1816 are D1840-D1856 here (+40), and D1857 is the later deferral-bound ruling (the text says which). Checks 490-492 here.
