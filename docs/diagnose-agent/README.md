# Diagnose (a local agent over a failed command's log)

**Status (2026-11-07, later): two tasks of the plan below are written — T-DG4, the source cache (`internal/srctree/srctree.go` and `srctree_test.go`, on the branch `fugaro/20261107-212223-8492`, with the independent review rounds its tier requires), and T-DG5, the credential resolver (`internal/diagnoseagent/creds.go` and `creds_test.go`, on the branch `fugaro/20261107-212231-bc83`, with the in-run review round its tier requires); everything else the folder holds is plan only, and no other task is approved.** Spec owner: Dimi Paun. Written 2026-11-07. **Review rounds over the plan documents themselves have not run.**

*The status as first written, kept:* **Status: plan only; nothing is implemented; no task is approved.** Spec owner: Dimi Paun. Written 2026-11-07. **Independent review rounds have not run.**

- [spec.md](spec.md): the spec as received.
- [reconciliation.md](reconciliation.md): findings F1–F23, decisions D1600–D1614 (all answered 2026-11-07, D1605–D1613 as recommended on the owner's delegation; D1600 not the recommendation; D1603 amended after review), tensions, UNVERIFIED. Wins over the spec.
- [implementation-plan.md](implementation-plan.md): T-DG1–T-DG10, levels, critical path, Checks 410–412.

**Numbers reserved:** D1600–D1619, Checks 410–419. **Typo fixes to the spec:** none. **Siblings:** no-prompt (PR #1906), composite commands (PR #1907) and roles (PR #1908); their folders are not on `main` yet, so they are named, not linked.
