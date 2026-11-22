# Managed flag ergonomics

**Status: plan only; nothing is implemented; no task approved.** The spec's open decisions were answered on 2026-11-21. The current state is in [docs/status.md](../status.md).

**Spec owner:** Dimi Paun, 2026-11-13. **Reconciled and planned:** 2026-11-21, against `origin/main` at d49063235.
**Numbers:**
- Decisions D2210–D2219, the spec's own block, all used.
- Decisions D3160–D3169, a fresh block: D3160 used, D3161–D3169 reserved and unused.
- Checks 860–869: Checks 860 and 861 used.

This spec predates the folder layout. Its documents lived in `docs/design/` until 2026-11-21, when they moved here unchanged except for their links.

| File | What it is | Was |
| --- | --- | --- |
| [spec.md](spec.md) | The spec, as received (no typo fixed) | `docs/design/managed-flag-ergonomics.md` |
| [reconciliation.md](reconciliation.md) | Findings F1–F19, the answers to D2211–D2219 and D3160 (D3160 confirmed by the owner 2026-11-21, amended after review), tension TN1 (ruled), resolutions R1–R10, Checks 860–861, UNVERIFIED. **Where it disagrees with spec.md, it wins** | new |
| [implementation-plan.md](implementation-plan.md) | Twelve tasks (T0, T1, T2a–c, T3, T4, T5, T6, T7a–c) in six levels, critical path, shared files. Nothing in it is approved | new |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it | new |

**The owner's principle, 2026-11-21:** Fugaro deals only with managed projects. Every command that writes to a project stops on an unmanaged one and names `fugaro managed flag` (D2217, TN1).

Not the recommendation, and followed through: D2212 (the amount defaults to 100 everywhere), D2218 (a failed managed read warns and proceeds) and D3160 (`offboard` is exempt).
