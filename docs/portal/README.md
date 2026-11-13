# Fugaro portal

**Status as of 2026-11-12: plan only; nothing is implemented; no task approved.**

The signed-in web app behind the marketing site: accounts, organisations, workspaces, entitlements and
keys, sales records and reports for customers, resellers and Fugaro's operations team. It extends the Go
key service in `ee/keyservice` and the v1 licence key of [licence-key-v2](../licence-key-v2/README.md),
and adds a React app in `ee/portal/`. A purchase (or any other grant) is an entitlement that the buyer
binds to their workspace's Firebase project once `fugaro setup` has created it, so paying never waits on
setup.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim |
| [reconciliation.md](reconciliation.md) | Findings F1–F42 against `origin/main` (#2457 and #2458 merged), decisions D2180–D2207 with the owner's answers (2026-11-12), open items, UNVERIFIED. It wins over the spec |
| [implementation-plan.md](implementation-plan.md) | Slices P0–P9, parallel levels, critical path, shared-files table, Checks 570–575 |

Spec owner: the repository owner. Numbers taken: **D2180–D2229** (D2180–D2207 used), **Checks 570–579**
(570–575 used), findings F1–F42. No spec typo was fixed.

Five decisions were answered against the recommendation: D2180 (all Go), D2181 (keep the v2 bundle), D2182 (hash only,
rotate), D2183 (merge #2457 and #2458 as planned), D2194 (everything in `fugaro-prod`, kept after round 1 with the risk accepted); D2194 was asked twice and answered against it both times. Amended after review: D2181 (by D2200) and D2182
(the handover is encrypted), both re-asked and answered. D2184 is amended by the owner's voucher ruling
(D2196–D2199). Tension TN-P1 (D2201 against D2204) was answered against the recommendation: a project
id is unique across organisations and operations transfers it. D2207 for operations (Google only) is the owner's own answer, not the
recommendation.
