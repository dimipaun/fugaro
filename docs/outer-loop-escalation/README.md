# Outer-loop escalation for non-converging PRs

**Status as of 2026-11-10: plan only; nothing is implemented; no task approved.**

**Spec owner:** Dimi Paun
**Written:** 2026-11-10
**Base:** `main` at `d19e2b1cf`

A ladder of rungs R0-R4 in the orchestrator daemon: rebase and re-test, resume, one stronger single pass, merge with
follow-ups under the existing gate, and split as the last resort. It sits on top of the inner recipe loops and the
post-review triage, and it is built on BF2/O14 of `docs/stateless-orchestrator`, not beside it.

| Document | What it is |
| --- | --- |
| [spec.md](spec.md) | the owner's spec, verbatim, in its own commit (no typo was fixed) |
| [reconciliation.md](reconciliation.md) | F1-F22 findings against the code, decisions D2030-D2041 with the owner's answers, open tensions, UNVERIFIED; where it differs from the spec, it wins |
| [implementation-plan.md](implementation-plan.md) | four slices, seventeen tasks with files, tests, red steps, levels, critical path and shared-files table |

## In one paragraph

The spec reads greenfield and is not. The orchestrator daemon exists (10 of 19 tasks built); BF2/O14 already designs a
deterministic "merge as-is and file a follow-up" rule; Fugaro never merges except under D1245's narrow gate; "tier" already
means trivial/small/medium/large; reports have one project-level writer. So the ladder reuses O14's gate, a model judge may
only veto, the names become rungs and model grades, thresholds are local-only keys that replace `orchestrator.triage_after`,
and the digest gains an Escalation section. The owner chose, against the recommendation, to let the daemon ready a stalled
draft and merge it (D2041), behind a local switch that ships off.

## Numbers this work holds

Decisions **D2030-D2069** (D2030-D2041 used) and checks **540-549** (540-541 used, both owner-run).

## Notes on the spec

Amended by the reconciliation: its "merge" is "is merge-eligible under D1245's gate and the daemon merges"; Critical always waits
for a person; its thresholds are local keys, not project-layer keys; "model tier" keys are `model_grades.*`; "regressions traced
to merges" is measured as a revert within seven days; the console dashboard is out of this plan.

No obvious typo in the spec was fixed.
