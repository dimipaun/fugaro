# Making `fugaro runs ls` fast

**Status (2026-11-19): plan only; nothing is implemented; no task approved.** The shape was agreed in outline by the owner; D2960–D2967 are recommendations awaiting confirmation. The current state is in [docs/status.md](../status.md).

**Spec owner: the owner (Dimi Paun). Date: 2026-11-19.** Checked against `origin/main` at `b8858034b`.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as written from the owner's outline, with the measured times |
| [reconciliation.md](reconciliation.md) | The spec against the code (F1–F9), the decisions D2960–D2967 with their recommendations, UNVERIFIED (U1–U2). Wins where it disagrees with the spec |
| [implementation-plan.md](implementation-plan.md) | Tasks RL1, RL2, RL2W, RL2B, RL3, RL4 (optional, gated), RL5 and RL6 in three levels, and the owner-run Check 760 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it |
| [briefs/](briefs/) | One ready task brief per task, `RL1.md` to `RL6.md`; the first line of each is its `# Task:` title |

## In one paragraph

`runs ls` reads every run in its window and filters afterwards, so `--state running` takes 20 s for no rows and `--pr N` 23 s for one. The fix has three parts: the live states read the backend's live executions and the launch claims (RL1); a per-run object under `runs/<slug>/pr-index/<N>/` names the runs on a pull request, written by the launcher and backfilled in place by `fugaro update` (RL2, RL2B; RL2W's runner write retired with D3204's IR5a, and the two that remain stop with its IR5b); and `--pr N` reads it as a hint, verifying each run's own records, unioned with the last 24 hours and falling back to the scan when empty (RL3). A per-run summary object (RL4) is built only if the default listing is still slow afterwards.

## Numbers

Decisions **D2960–D2967** are taken as recommendations and **D2968–D2969** are reserved. **Check 760** is taken and **761–769** are reserved. Findings (`F`) and unverified items (`U`) are numbered within this folder.

## Typos fixed in spec.md

None.
