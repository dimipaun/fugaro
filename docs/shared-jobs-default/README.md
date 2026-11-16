# Project-level shared jobs as the default

**Status: plan only; nothing is implemented; no task approved.** Owner: the project owner (the spec names none).
Date: 2026-11-13. Checked against `origin/main` at `a2e77dc3a`.

The spec: every project, GitHub or Bitbucket, runs on shared project jobs; images are project-level caches; access is
decided per job by the credential service; `fugaro update` migrates existing projects and retires the per-repository jobs.

**Amended 2026-11-13 (the token-types plan, T15): R7 and F15 are read as "any mix within one workspace".**
[docs/bitbucket-token-types](../bitbucket-token-types/README.md) rules — its D2350 and D2355 answered 2026-11-13 as
recommended — that the credentials bundle holds any mix of repository, project and workspace Bitbucket tokens, several
at once, and that the proxy serves each request with the narrowest covering one. R7's "the one workspace token in the
credentials bundle" and F15's "one workspace token per project" therefore read as one workspace, any mix of tokens
within it, and S6's step — `update` asking once for the one workspace token — now points at
`fugaro creds set bitbucket-token--<name>`. D2314's one-workspace-per-project limit is not lifted, and nothing here is
approved by that plan: S2 and S6 read the token store through the interface its contract C1 defines.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received, byte for byte (no typo was fixed). **Where [reconciliation.md](reconciliation.md) disagrees with it, the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | The spec against the code: capability map, assumptions, findings F1–F23, decisions D2300–D2313 with the owner's answers, open items, UNVERIFIED |
| [implementation-plan.md](implementation-plan.md) | Four slices, 32 tasks (including two contracts and two integration tasks) and two anticipated change requests, levels, critical path, shared-files table. Nothing in it is approved |
| [contract.md](contract.md) | C1's prose half: the shapes `internal/sharedjob` declares, what each side of the contract may rely on, what none of them may, and what amending C1 costs |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads |

## Number blocks taken

Decisions **D2300–D2349** (D2300–D2312 used; D2307 unused), checks **580–589** (580, 581 and 582 used), findings and tasks local to this
folder (F1–F23; C1, C2, S1–S10, B1–B6, B8, B9, I1, I2, N1–N3, M1–M7; change requests CR1, CR2). Highest in use on `main` when taken: D2229 and Check 574; open
pull requests' documents reach D2207.

## What a reader should not take from here

- The spec says shared jobs are "disabled as untested"; they are not behind a switch, they are limited to GitHub by a provider list.
- The spec's "Fugaro-issued token" for Bitbucket is dropped (D2302): the job holds no token, and the proxy pairs the verified run identity to the profile's repositories.
- Open items 6, 7 and 8 were answered as D2310 to D2312, and D2312 was amended by the owner (no repository-level Terraform at all). D2313 first moved repository secrets to project secrets and was then reversed: repository-level secrets are disabled for now, accepted and ignored with a warning, like repository-level images.
- D2303 (no retire gate) is **not the recommendation** and departs from the spec's own "after one successful run". D2309 first was too, and was amended to the recommendation.
- Check 191, the spec's release blocker, is the owner's own run and is a prerequisite here, not a task.
