# Model crafts and credentials

**Status: plan only; nothing is implemented; no task approved.** Spec owner: Dimi Paun. Date: 2026-11-07.

The spec replaces the fixed "OpenRouter coder, Opus reviewer" setup with **credentials**, **pools** and **crafts** (the owner's word for the spec's "roles", which collide with user roles). Most of it exists under other names (named Claude accounts, per-stage accounts, a dormant ordered failover, model routes, review levels); the plan generalises those in place and migrates inside `fugaro update`.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received (verbatim) |
| [reconciliation.md](reconciliation.md) | Findings F1–F25, decisions D1620–D1637 (D1638–D1639 reserved), open tensions, UNVERIFIED |
| [implementation-plan.md](implementation-plan.md) | Slices, tasks MC1–MC10, levels, critical path, shared-files table, Checks 420–422 (423–429 reserved) |

**Numbers:** D1620–D1639, Checks 420–429. Siblings: no-prompt (#1906), composite commands (#1907), roles (#1908), diagnose (#1909). **Independent review has not run.**
