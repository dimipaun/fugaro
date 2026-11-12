# Image setup skill

**Status as of 2026-11-12: the `fugaro:images` skill is in the tree (IS1 to IS12, with IS2 and IS8b still open pull requests), and IS13 lands its release — Check 500 and Check 501 are written and NOT RUN; see [docs/status.md](../status.md) for the current table.** Spec owner: Dimi Paun. Received and reconciled 2026-11-09.

**Status as of 2026-11-10: in progress, 4/14 merged; see [docs/status.md](../status.md) for the current table.** Spec owner: Dimi Paun. Received and reconciled 2026-11-09.

A Fugaro skill that surveys a project's repos, proposes fewer than five project-level images, generates their Dockerfiles and validates them against the runtime contract. Repo-level images are disabled, not removed.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim |
| [reconciliation.md](reconciliation.md) | Findings F1-F22 against the code, decisions D1900-D1906 and the tensions T1, O3, O4 with the owner's answers, the open items O1-O2, UNVERIFIED. Wins over the spec |
| [implementation-plan.md](implementation-plan.md) | Tasks IS1-IS13 (IS6 unused), parallel levels, critical path, shared-files table. No task is approved |

**Numbers held:** D1900-D1929 (D1907-D1929 unused), checks 500-509 (500 and 501 are taken, by IS13; 502-509 are free), findings F1-F22. Read on 2026-11-09 against `origin/main` and the open pull requests.

**Related:** [project-image-catalog](../project-image-catalog/README.md) (what the skill fills), [repo-image-removal](../repo-image-removal/README.md) (R2-R10 parked by D1900), [catalog-build-context](../catalog-build-context/README.md).

No typo was fixed in spec.md.
