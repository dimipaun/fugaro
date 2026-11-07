# User roles and scopes

**Status: plan only; nothing is implemented; no task approved.** Spec owner: Dimi Paun (the owner).
Date: 2026-11-07. Checked against `origin/main` at `1ad2d3143`. **Round 1 of an independent security review is applied (reconciliation §7); no second round has run.**

The spec gives Fugaro three roles (viewer, operator, admin), each granted with a scope (the project, all
repos, or specific repos, with `*` patterns), resolved by "the highest role wins". It extends
[the existing roles design](../design/roles.md), which maps the roles onto the installation's
project-wide IAM lists.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as received |
| [reconciliation.md](reconciliation.md) | The amended spec: findings F1–F28, decisions D1580–D1591, open tensions, UNVERIFIED. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks RS1–RS10 (RS12 later), levels, critical path, shared-files table, Checks 400–402 |

**Numbers reserved:** decisions D1580–D1599, checks 400–409. Siblings: `docs/no-prompt-policy`
(D1540–D1559), `docs/composite-commands` (D1560–D1579).

**Owner answers of 2026-11-07:** D1580–D1582 as recommended; D1583 as recommended in words and **amended
after review** (canonical stored spelling); D1585's location amended (`config/admin/`); D1584 and D1586–D1590 recorded as the recommendation (owner delegated reading to the PR); D1591 as recommended (option c); the D1583 and D1585 amendments confirmed. No decision is open.
