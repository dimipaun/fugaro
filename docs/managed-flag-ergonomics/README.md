# Managed flag ergonomics

**Status: plan only; nothing is implemented; no task approved.** The spec's open decisions were answered on 2026-11-21. The current state is in [docs/status.md](../status.md).

**Spec owner:** Dimi Paun, 2026-11-13. **Reconciled and planned:** 2026-11-21, against `origin/main` at d49063235.
**Numbers:**
- Decisions D2210–D2219, the spec's own block: D2210–D2217 used, D2218–D2219 reserved and unused.
- Checks 860–869: Checks 860 and 861 used.

This spec predates the folder layout. Its documents lived in `docs/design/` until 2026-11-21, when they moved here unchanged except for their links.

| File | What it is | Was |
| --- | --- | --- |
| [spec.md](spec.md) | The spec, as received (no typo fixed) | `docs/design/managed-flag-ergonomics.md` |
| [reconciliation.md](reconciliation.md) | Findings F1–F15, the answers to D2211–D2217, tension TN1 (ruled), resolutions R1–R5, Checks 860–861, UNVERIFIED. **Where it disagrees with spec.md, it wins** | new |
| [implementation-plan.md](implementation-plan.md) | Tasks T0–T7 (with T2a, T2b and T2c) in five levels, critical path, shared files. Nothing in it is approved | new |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it | new |

**The owner's principle, 2026-11-21:** Fugaro deals only with managed projects. Every command that writes to a project stops on an unmanaged one and names `fugaro managed flag` (D2217, TN1).

D2212 (the amount defaults to 100 everywhere) is not the recommendation, and is followed through.
