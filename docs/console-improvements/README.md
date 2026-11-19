# Console improvements: spec, reconciliation and implementation plan

**Status: plan only; nothing is implemented; no task approved.** This folder is documents only: it adds no code,
no schema, no console source, no fixture and no workflow, and it edits no file outside `docs/` (the one row it
adds to [docs/status.md](../status.md) follows the rule that a spec's row moves when its plan is written).

**Spec owner:** the owner, November 2026 (the spec's header gives no name; he asked for this reconciliation on
2026-11-18). The spec is the console's own: the Electron console stays a thin front end over the CLI.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec **verbatim**. No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1 to A5, findings F1 to F31, decisions D2900 to D2909 with the owner's dated answers, resolutions R1 to R10, open items O1 to O6, UNVERIFIED U1 to U9. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Six slices and 46 tasks with levels, the critical path, the shared-files table, the dependencies broken and kept, the estimate line, the three owner-run checks and the after-deploy steps. **Nothing in it is approved** |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads (schema 3) |

## Numbers taken

Decisions **D2900 to D2929** (D2900 to D2909 used), checks **710 to 719** (710 to 712 used). The highest decision on
`main` at `01fe41f30` was D2769 and no open pull request's documents reserve a higher one. Findings, assumptions,
resolutions, open items and UNVERIFIED items are numbered from 1 inside this folder only. Module ids (approved
2026-11-18): `live-pane`, `graph-flow`, `settings-meta`, `settings-ui`, `manage-objects`, `setup-wizard`,
`console-perf`, `console-robust`.

## What waits on something external

The owner's explicit go for every task, and a go of its own for `MW1`, `MW2` and `SW3a`; the owner-run Checks 710,
711 and 712; and a CLI release that carries the new flags before the console floors are set. Open items O1 to O6
were all answered on 2026-11-18 (reconciliation section 6); O4, not the recommendation, added MD1 and MD2
(`--dry-run` on `models add` and `image add`).
