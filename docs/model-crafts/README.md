# Model crafts and credentials

**Status as of 2026-11-10: in progress, 4/10 merged; see [docs/status.md](../status.md) for the current table.** Spec owner: Dimi Paun. Date: 2026-11-07.

The spec replaces the fixed "OpenRouter coder, Opus reviewer" setup with **credentials**, **pools** and **crafts** (the owner's word for the spec's "roles", which collide with user roles). Most of it exists under other names (named Claude accounts, per-stage accounts, a dormant ordered failover, model routes, review levels); the plan generalises those in place and migrates inside `fugaro update`.

**2026-11-10 (F26, D1638): the api-key review path is not superseded.** `docs/api-key-review`'s AK6a (merged), AK6b (in review) and AK7 (not started) are the API-key credential type's runner and service halves; **MC5 and MC6 extend them and never replace them**, and both are **held until AK6b and AK7 have merged**. That folder's AK9 and AK10, written and unapproved, are folded into MC6.

**2026-11-15: the activation plan.** Model crafts shipped in 0.24.0 (Uakari) with four limits named in that release's `### Known limits`, and the owner's order of 2026-11-15 is to close them in the order he gave: **the model half of a craft** (a craft's model is written, validated and shown, and not yet what a run runs), **the publish** (nothing writes the crafts, credentials and pools from the owner's config into the published node), **pool crossing** (one pool cannot span the two halves a run is served by) and **key credentials** (`accounts add --type` serves `claude-oauth` alone). [activation-plan.md](activation-plan.md) is that work: tasks MC11–MC26, no task approved, and each waiting on the owner's explicit go.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received (verbatim) |
| [reconciliation.md](reconciliation.md) | Findings F1–F26 and F27–F30, decisions D1620–D1638 (D1639 reserved) and, from the activation plan, D1660–D1672 (D1673–D1679 reserved), open tensions, UNVERIFIED |
| [implementation-plan.md](implementation-plan.md) | Slices, tasks MC1–MC10, levels, critical path, shared-files table, Checks 420–422 (423–429 reserved) |
| [activation-plan.md](activation-plan.md) | The four halves 0.24.0's known limits name, in the owner's order of 2026-11-15: slices, tasks MC11–MC26, levels, critical path, shared-files table, the rollout order, Checks 628–632 (633–637 reserved) |

**Numbers:** D1620–D1639 and D1660–D1679, Checks 420–429 and 628–637. Siblings: no-prompt (#1906), composite commands (#1907), roles (#1908), diagnose (#1909). **Independent review has not run.**
