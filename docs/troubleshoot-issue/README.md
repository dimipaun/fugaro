# Troubleshoot findings to `fugaro issue`

**Status (2026-11-08, later): one task of the plan below is written — T-IS2, the issue redaction gate (`internal/redact/redact.go` and `redact_test.go`, `internal/issues/redact.go` and `redact_test.go`, on the branch `fugaro/20261108-174015-f89c`, where its red step has run); everything else the folder holds is plan only, and no other task is approved.** Spec owner: Dimi Paun. Written 2026-11-08. Two independent review rounds ran; see the pull request.

*The status as first written, kept:* **Status: plan only; nothing is implemented; no task approved.** Spec owner: Dimi Paun. Written 2026-11-08. Two independent review rounds ran; see the pull request.

- [spec.md](spec.md): the spec as received.
- [reconciliation.md](reconciliation.md): findings F1–F27, decisions D1720–D1736 (the writer of `resolved_in` is another spec) (all answered 2026-11-08, all as recommended; D1720, D1721 and D1605 (D1728) amended after review), open items, UNVERIFIED. Wins over the spec.
- [implementation-plan.md](implementation-plan.md): tasks T-IS1–T-IS17 and T-IS19 in three slices (local, shared store, terminal report), levels, critical path, shared-files table, Checks 450–453.

**Numbers reserved:** D1720–D1749, Checks 450–459. **Typo fixes to the spec:** none. **Builds on:** `docs/diagnose-agent/` (the `fugaro troubleshoot` command is its T-DG8, not built yet). Amends D1605 of that plan (D1728). Untouched: D327 (`runs digest --submit-draft` files nothing).
