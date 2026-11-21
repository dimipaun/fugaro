# Managed flag ergonomics

**Status: plan only; nothing is implemented; no task approved.** The spec's open decisions were answered on 2026-11-21. The current state is in [docs/status.md](../status.md).

**Spec owner:** Dimi Paun, 2026-11-13. **Reconciled and planned:** 2026-11-21, against `origin/main` at d49063235.
**Numbers:** D2210–D2219 (the spec's own block; D2210–D2216 used, D2217–D2219 reserved and unused); Checks 860–869 (Check 860 used).

This spec predates the folder layout. Its documents lived in `docs/design/` until 2026-11-21, when they moved here unchanged except for their links.

| File | What it is | Was |
| --- | --- | --- |
| [spec.md](spec.md) | The spec, as received (no typo fixed) | `docs/design/managed-flag-ergonomics.md` |
| [reconciliation.md](reconciliation.md) | Findings F1–F12 against the code, the answers to D2211–D2216, tension T1 (ruled), resolutions R1–R4, Check 860, UNVERIFIED. **Where it disagrees with spec.md, it wins** | new |
| [implementation-plan.md](implementation-plan.md) | Tasks T1, T2a, T2b, T2c, T3, T4, T5 and T6 in four levels, critical path, shared files. Nothing in it is approved | new |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it | new |

Two answers are not the recommendation: **D2212** (the amount defaults to 100 everywhere) and **T1** (every `fugaro update` marks an unmanaged installation). Both are followed through in the reconciliation and the plan.
