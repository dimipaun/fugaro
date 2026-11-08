# Show resolved issues on `fugaro update`

**Status: plan only; nothing is implemented; no task approved.** Spec owner: Dimi Paun. Written 2026-11-08.

- [spec.md](spec.md): the spec as received.
- [reconciliation.md](reconciliation.md): findings F1–F20, decisions D1750–D1757 (all answered 2026-11-08; D1753, D1754 and D1755 are not the recommendation), open items O1–O4, UNVERIFIED. Wins over the spec.
- [implementation-plan.md](implementation-plan.md): tasks T-RU1–T-RU7 in three slices, levels, critical path, shared-files table, Checks 460–462.

**Numbers reserved:** D1750–D1779, Checks 460–469. **Typo fixes to the spec:** none. **Builds on and amends:** [`docs/troubleshoot-issue/`](../troubleshoot-issue/README.md), whose F25, F27, D1731 and D1736 assume this spec writes `resolved_in`; it amends that plan's F25 (the status enum gains `resolved`), T-IS10's "a submitted record is final", T-IS19's wording and F28 (the retry command; D1755). Every task depends on parts of that plan that are not merged yet.
