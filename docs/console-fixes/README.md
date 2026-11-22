# Console fixes: performance, the coordinator tab, redaction

**Status (2026-11-21): plan only; nothing is implemented; no task approved.** Every decision is answered by the owner (D3200–D3214) or stated as a resolution: D3215–D3222 were approved at the gate, and D3223–D3224 were added after the round-1 review. D3223, D3224, the amendments to D3215, D3217, D3218 and D3220, tension T1 and the reindex IAM question wait on the owner (O3, O4, O6, O7). Each task still needs the owner's explicit go. The current state is in [docs/status.md](../status.md).

**Spec owner: the owner (Dimi Paun). Date: 2026-11-21.** Checked against `origin/main` at `dfe066618`.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as received |
| [reconciliation.md](reconciliation.md) | The capability map, assumptions A1–A7, findings F1–F40, decisions D3200–D3224 with the answers (D3215, D3217, D3218 and D3220 amended after review), open tension T1, open items O1–O7, UNVERIFIED U1–U5. Wins where it disagrees with the spec |
| [implementation-plan.md](implementation-plan.md) | 37 tasks in four modules and six levels, two anticipated contract changes, owner Checks 870 and 871. Nothing in it is approved |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it |

## In one paragraph

The Graph tab's 90 s timeout is the console's spawn deadline killing `plan status --json --all`, which reads 90 days of run objects from GCS and asks `gh` about merges. `runs ls` reads its whole window even when paged. The fix is a Firestore index of runs, written only by the history job's sweep and kept fresh by a bounded RTDB overlay. `fugaro update` builds it, and `fugaro runs reindex` rebuilds it. Every list reads it through one Go reader the console reaches by spawning the CLI. The Coordinator tab becomes a graph of active orchestrators, which the owner chose to make many and named per project: cross-orchestrator dependencies (solid edges), collisions (dashed edges), task claims and a sweep heartbeat. The console's non-secret masks go: they live in its display sanitizer (`console/src/shared/sanitize.ts`) and in one refusal path of its main process (`boundReason`). Only secrets stay masked.

## Modules

`run-index` → `indexed-reads`; `coordinator-graph` and `identity-display` are independent (D3200).

## Numbers

Decisions **D3200–D3224** are taken and **D3225–D3229** are reserved. **Checks 870 and 871** are taken and **872–879** are reserved. Findings (`F`), assumptions (`A`), open items (`O`) and unverified items (`U`) are numbered within this folder.

## Typos fixed in spec.md

None.
