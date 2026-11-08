# `fugaro issue mine`: what happened to my issues

**Status: plan only; nothing is implemented; no task approved.** Spec owner: Dimi Paun. Written 2026-11-08.

- [spec.md](spec.md): the spec as received.
- [reconciliation.md](reconciliation.md): findings F1–F18, decisions D1780–D1784 (all answered 2026-11-08; D1784 is not the recommendation), open items O1–O5 (answered 2026-11-08; O4 is not the recommendation) (answered 2026-11-08; O4 is not the recommendation), UNVERIFIED. Wins over the spec.
- [implementation-plan.md](implementation-plan.md): tasks T-IM1–T-IM7 in three slices, levels, critical path, shared-files table, Checks 470–472.

**Numbers reserved:** D1780–D1809, Checks 470–479. **Typo fixes to the spec:** none. **Builds on:** [`docs/troubleshoot-issue/`](../troubleshoot-issue/README.md) (the issue record, stores and `fugaro issue`) and [`docs/issues-resolved-on-update/`](../issues-resolved-on-update/README.md) (`resolved`, `resolved_in`, the installation record); neither is built, so every task depends on parts of them that are not merged yet. **Amends:** troubleshoot-issue T-IS10's occurrence key layout (a reporter index, hit-time names), T-IS10/T-IS11's reporter source (the verified credential), T-IS11's sync (skips another installation's local issues), T-IS1's local header (`installation`) and the record's three date fields, and update-plan T-RU3's `Reporters`, before any is built. **Drops** the spec's GitHub refresh lines (D1784).
