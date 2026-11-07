# No-prompt policy

**Status: plan only; nothing is implemented; no task approved.**

**Spec owner:** the owner (Dimi Paun), 2026-11-07. **Numbers reserved:** D1540–D1559, Checks 380–389.
D1560–D1579 and Checks 390–399 belong to the sibling spec `docs/composite-commands/`, which refers to this one.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received |
| [reconciliation.md](reconciliation.md) | Findings against the code, decisions D1540–D1555 (D1540–D1551 answered 2026-11-07, D1552–D1554 answered 2026-11-07), open tensions |
| [implementation-plan.md](implementation-plan.md) | Slices T1–T15 (rounds 1 and 2 of the independent review applied), levels, critical path, shared-files table |
| [billing-price-spike.md](billing-price-spike.md) | The spike T8 was held for: which API and credential give a Cloud Build machine-minute price, why no IAM grant is needed, the failure modes, and the recommendation |

Three answers are not the recommendation: D1544 (keep `--ask`), D1545 (the managed gate becomes
audit-only) and D1550 (the build rate is queried from Cloud Billing). They open tensions T1–T5 for the owner.
