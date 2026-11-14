# The review job (option C): `fugaro review --pr N` works out of the box

**Status: pieces 1 and 3 in runs or merged; see tasks.** Piece 1 (RJ1) and the two Bitbucket service and runner halves
(RJ6, RJ7) are merged; piece 3 (RJ3) is live; pieces 2 (RJ2) and the Bitbucket implicit job (RJ8) are approved (owner word 2026-11-14); piece 4 and the owner's checks are not
started and **not approved**. [docs/status.md](../status.md) carries the current count.

Owner: the project owner. Date: 2026-11-14. Checked against `origin/main` at `2988c2d64`. The owner's rulings of
2026-11-13 are the spec's source; the owner could not be asked while this folder was written, D2383 to D2388 were answered on 2026-11-14 (reconciliation, Decisions); only T1 and T2 remain open.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | the owner's ruling and the problem as received, each fact tagged with its source |
| [reconciliation.md](reconciliation.md) | the spec against the code: findings F1 to F11, decisions D2380 to D2388, tensions T1 and T2, UNVERIFIED |
| [implementation-plan.md](implementation-plan.md) | tasks RJ1 to RJ10, levels, edges, shared files |
| [implementation-plan.graph.json](implementation-plan.graph.json) | the graph `fugaro plan graph --plan docs/review-job/implementation-plan.md` reads |

**Number blocks taken** (read on 2026-11-14 from `origin/main` and every open pull request's documents; the highest
decision number in use was D2379, the token-types plan's block, and checks 590 and 591 and 600 were taken): decisions
**D2380 to D2399** (D2380 to D2388 used), checks **610 to 619** (610 and 611 used). No typo in the spec was fixed (it is
assembled, not copied).
