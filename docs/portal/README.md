# Fugaro portal

**Status as of 2026-11-12: plan only; nothing is implemented; no task approved.**

The signed-in web app behind the marketing site: accounts, organisations, workspaces, keys, sales
records and reports for customers, resellers and Fugaro's operations team. It extends the Go key service
in `ee/keyservice` and the v1 licence key of [licence-key-v2](../licence-key-v2/README.md), and adds a
React app in `ee/portal/`.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim |
| [reconciliation.md](reconciliation.md) | Findings F1–F24 against `origin/main`, decisions D2180–D2195 with the owner's answers (2026-11-12), open items, UNVERIFIED. It wins over the spec |
| [implementation-plan.md](implementation-plan.md) | Slices P0–P9, parallel levels, critical path, shared-files table, Checks 570–575 |

Spec owner: the repository owner. Numbers taken: **D2180–D2229** (D2180–D2195 used), **Checks 570–579**
(570–575 used), findings F1–F24. No spec typo was fixed.

Five answers are not the recommendation: D2180 (all Go), D2181 (keep the v2 bundle), D2182 (hash only,
rotate), D2183 (merge #2457 and #2458 as planned), D2194 (everything in `fugaro-prod`).
