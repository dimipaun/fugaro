# Project-level shared jobs as the default

**Status: plan only; nothing is implemented; no task approved.** Owner: the project owner (the spec names none).
Date: 2026-11-13. Checked against `origin/main` at `a2e77dc3a`.

The spec: every project, GitHub or Bitbucket, runs on shared project jobs; images are project-level caches; access is
decided per job by the credential service; `fugaro update` migrates existing projects and retires the per-repository jobs.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received, byte for byte (no typo was fixed). **Where [reconciliation.md](reconciliation.md) disagrees with it, the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | The spec against the code: capability map, assumptions, findings F1–F23, decisions D2300–D2313 with the owner's answers, open items, UNVERIFIED |
| [implementation-plan.md](implementation-plan.md) | Four slices, 32 tasks (including two contracts and two integration tasks) and two anticipated change requests, levels, critical path, shared-files table. Nothing in it is approved |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads |

## Number blocks taken

Decisions **D2300–D2349** (D2300–D2312 used; D2307 unused), checks **580–589** (580, 581 and 582 used), findings and tasks local to this
folder (F1–F23; C1, C2, S1–S10, B1–B6, B8, B9, I1, I2, N1–N3, M1–M7; change requests CR1, CR2). Highest in use on `main` when taken: D2229 and Check 574; open
pull requests' documents reach D2207.

## What a reader should not take from here

- The spec says shared jobs are "disabled as untested"; they are not behind a switch, they are limited to GitHub by a provider list.
- The spec's "Fugaro-issued token" for Bitbucket is dropped (D2302): the job holds no token, and the proxy pairs the verified run identity to the profile's repositories.
- Open items 6, 7 and 8 were answered as D2310 to D2312, and D2312 was amended by the owner (no repository-level Terraform at all). D2313 first moved repository secrets to project secrets and was then reversed: repository-level secrets are disabled for now, accepted and ignored with a warning, like repository-level images.
- D2303 (no retire gate) and D2309 (a run may read the project's other baked repositories) are **not the recommendation**; D2303 departs from the spec's own "after one successful run".
- Check 191, the spec's release blocker, is the owner's own run and is a prerequisite here, not a task.
