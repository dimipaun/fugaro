# Troubleshoot findings to `fugaro issue`

**Status (2026-11-09, later): one task of the plan below is written — T-IS2, the issue redaction gate (`internal/redact/redact.go` and `redact_test.go`, `internal/issues/redact.go` and `redact_test.go`, on the branch `fugaro/20261108-174015-f89c`, where its red step has run) — and T-IS1, T-IS3, T-IS5, T-IS10, T-IS13, T-IS14 and T-IS15 have merged on `main` while that branch was open (T-IS1 and T-IS10 already run the gate, over the record's header and over the text the shared store holds, which is not the post); everything else the folder holds is plan only, and no other task is written.** Spec owner: Dimi Paun. Written 2026-11-08. Two independent review rounds ran; see the pull request.

*The status as first written, kept:* **Status: plan only; nothing is implemented; no task approved.** Spec owner: Dimi Paun. Written 2026-11-08. Two independent review rounds ran; see the pull request.

- [spec.md](spec.md): the spec as received.
- [reconciliation.md](reconciliation.md): findings F1–F27, decisions D1720–D1736 (the writer of `resolved_in` is another spec) (all answered 2026-11-08, all as recommended; D1720, D1721 and D1605 (D1728) amended after review), open items, UNVERIFIED. Wins over the spec.
- [implementation-plan.md](implementation-plan.md): tasks T-IS1–T-IS17 and T-IS19 in three slices (local, shared store, terminal report), levels, critical path, shared-files table, Checks 450–453.

**Numbers reserved:** D1720–D1749, Checks 450–459. **Typo fixes to the spec:** none. **Builds on:** `docs/diagnose-agent/` (the `fugaro troubleshoot` command is its T-DG8, not built yet). Amends D1605 of that plan (D1728). Untouched: D327 (`runs digest --submit-draft` files nothing).

**Amended by [`docs/issues-resolved-on-update/`](../issues-resolved-on-update/README.md)** (the `update` spec this plan assumed): the status enum gains `resolved` and the record `closed_as` (F25), a submitted record may move to `resolved` or `dismissed` once (T-IS10), T-IS19's wording and the retry command's source (F28, D1755).

**Amended by [`docs/issue-mine/`](../issue-mine/README.md)** (the `fugaro issue mine` spec): a reporter index and hit-time occurrence names in T-IS10's store, the reporter set from the verified credential at upload (T-IS10/T-IS11), T-IS11's sync skipping another installation's local issues, an `installation` header field (T-IS1), the date fields `submitted_at`, `resolved_at`, `dismissed_at`, and `Reporters` (T-RU3) reading the new layout.
