# Website change log

**Status: plan only; nothing is implemented; no task approved.** The owner answered D1640–D1643 on
2026-11-07 (all as recommended; D1641 amended after review, amendment confirmed); D1644–D1646 and D1648 were answered as recommended the same day. No decision is open.

**Spec owner:** Dimi Paun, received 2026-11-07. **Reserved numbers:** D1640–D1659, Checks 430–439.

The spec asks for a change log page of the last three releases, taken from GitHub's API, with a link to
the full list. The page is already built automatically from `docs/releases/`, and the build is
deliberately network-free, so the owner chose to keep the release files as the source and show the newest
three plus the link. The spec's API fetch, its failure fallback and its rebuild trigger are overridden by
that answer.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | the spec, as received |
| [reconciliation.md](reconciliation.md) | findings F1–F14, decisions D1640–D1648 with the owner's answers, open items |
| [implementation-plan.md](implementation-plan.md) | tasks WC1–WC4, levels, critical path, shared files, Check 430 |

One independent review ran on 2026-11-07 and its findings are applied; no second round has run.
