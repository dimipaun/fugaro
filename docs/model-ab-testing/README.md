# Model A/B testing

**Status: plan only; nothing is implemented; no task approved.** Decisions D550–D573 were answered by the owner on
2026-10-29: the recommendation in every row except D570 (a new dedicated command, not the
recommendation). The answers are not task approvals. Open: the new command's name and whether the
digest section stays (reconciliation.md section 4, T1 and T2).

**Spec owner:** the Fugaro owner. **Spec received:** 2026-10-28. **Reconciled against** `origin/main`
at `c192e8766` and the docs of the 22 PRs open on 2026-10-29.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's "Fugaro Model A/B Testing Spec", **verbatim** |
| [reconciliation.md](reconciliation.md) | The headline table, findings F1–F22 against the code, resolutions R1–R5, decisions D550–D573 (answered 2026-10-29), open tensions T1–T3, open items O1–O3, UNVERIFIED U1–U6. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices A1–A6, tasks A1.1–A6.2 (with A3.0), levels L0–L5, the critical path, the shared-files table, Checks 210–212. **Nothing in it is approved** |

## Headline

- The orchestrator agent the spec names as the judge does not exist (F9); the "what it would have
  cost on model X" reports the spec calls today's are not built (F3).
- What does exist and is reused: rigor modes and `report --by mode`, the tier, `--attempts` with its
  all-at-once budget pre-check, the task's unused `overrides.model`, the run record's hard signals,
  `stats.MinRuns`.
- What has to be built: a run that pushes and posts nothing (the shadow arm, F8), the sampler and
  pair launch, pair scoring and paired statistics, and, later, an anonymiser and a blind judge run.
- Post-merge rework is observable for one arm only, so it cannot enter a paired comparison (F12).
- Where each part lives (MIT or `ee/`) follows the 2026-10-29 re-org (D550).

## Typos fixed in the spec

None. It is byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D550–D579** | this work's block: **D550–D573** written and answered; **D574–D579** reserved and unused. Taken above the highest decision on `main` and in every open PR's docs (D537 written; D549 the top of ee-reorg's reserved block D520–D549) |
| **Checks 210–219** | this work's block: **210–212** proposed by the plan (written by A2.2, A5.3, A6.2; run by the owner; NOT RUN); **213–219** reserved and unused. Above ee-reorg's Checks 200–209 |
