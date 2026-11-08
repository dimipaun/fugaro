# No-prompt policy

**Status: plan only; nothing is implemented; no task approved.**

**Spec owner:** the owner (Dimi Paun), 2026-11-07. **Numbers reserved:** D1540–D1559, Checks 380–389.
D1560–D1579 and Checks 390–399 belong to the sibling spec `docs/composite-commands/`, which refers to this one.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received |
| [reconciliation.md](reconciliation.md) | Findings against the code, decisions D1540–D1555 (all answered 2026-11-07), the tensions T1–T6 (T1–T4 and T6 ruled by the owner 2026-11-07; T5's spike has reported, with its two live facts verified by the owner's probe of 2026-11-08, and T6's capless case open) |
| [implementation-plan.md](implementation-plan.md) | Slices T1–T16 and the spike T8S (rounds 1 and 2 of the independent review applied), levels, critical path, shared-files table |
| [billing-price-spike.md](billing-price-spike.md) | The spike T8 was held for: which API and credential give a Cloud Build machine-minute price, why no IAM grant is needed — **verified 2026-11-08** by the owner's own probe (§8) — the failure modes, and the recommendation |

Three answers are not the recommendation: D1544 (keep `--ask`), D1545 (the managed gate becomes
audit-only) and D1550 (the build rate is queried from Cloud Billing). They opened tensions T1–T5, and
D1554 against D1548 opened T6. The owner ruled on 2026-11-07: **T1** `--ask` as proposed, **T2** (a)
audit-only with a hardened record and the cover-table rule rewritten as a tripwire, **T3** and **T4**
confirmed, **T6** (b) `fugaro update` raises the budget mode to enforce on a managed project unless its owner
chose observe — except where the installation has no global caps, where the raise cannot land (D6): that one
stays observe and is unbounded once T10 removes the build count, which the reconciliation holds open.
**T5** stays open: the D1550 answer is held until the T8 spike (task T8S) reports — and it has now
reported, with its two live facts verified by the owner's probe of 2026-11-08: **no grant**, so what remains
is the owner's ruling rather than a measurement.
