# No-prompt policy

**Status: plan only; nothing is implemented; no task approved.**

**Spec owner:** the owner (Dimi Paun), 2026-11-07. **Numbers reserved:** D1540–D1559, Checks 380–389.
D1560–D1579 and Checks 390–399 belong to the sibling spec `docs/composite-commands/`, which refers to this one.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received |
| [reconciliation.md](reconciliation.md) | Findings against the code, decisions D1540–D1554 (D1540–D1551 answered 2026-11-07, D1552–D1554 answered 2026-11-07), open tensions |
| [implementation-plan.md](implementation-plan.md) | Slices T1–T14 (round 1 of the independent review applied), levels, critical path, shared-files table |

Three answers are not the recommendation: D1544 (keep `--ask`), D1545 (the managed gate becomes
audit-only) and D1550 (the build rate is queried from Cloud Billing). They open tensions T1–T5 for the owner.
