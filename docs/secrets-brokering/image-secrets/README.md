# Image secrets through the credentials service

**Status: plan only; nothing is implemented; no task is approved.** The owner answered every decision
(D3320–D3321, D3323–D3335) and the tensions (T1–T3) on 2026-11-23, all as recommended: D3323 and D3324
asked again after the owner reframed them; D3324 and D3326 amended after review round 1 (D2313
already disables repository-level secrets; a service address moves an api-key/oauth repository's
model calls) and answered again; D3334 and T2 withdrawn after review; T1 amended by T3. Each task still needs the owner's own explicit
go.

**Spec owner:** Dimi. **Date:** 2026-11-23. **Checked against:** `main` at `2772b8879`.

The spec asks that every secret a job uses come from the credentials service at runtime, suspecting
that secrets declared on an image still become job secret mappings. Read against the code
([reconciliation.md](reconciliation.md)), images declare no secrets; workflows do, and the parent
plan's S3 already serves them for served repositories, while D2313 already makes the project copy the
only one a run reads. This folder plans what is left: the project copies move into the `fugaro_p_`
grammar with a per-secret repository list the owner writes, so adding, removing or changing one needs
no Terraform; `fugaro update` copies existing values; vertex repositories are served too; a secret may be exposed as a file; the catalog
build stops mounting its git credential; and the parent plan's guard gains a mount rule for the workflow-secret class. Shared-job runs are out of scope (D3335).

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, verbatim |
| [reconciliation.md](reconciliation.md) | Assumptions A1–A5, what exists, findings F1–F24, decisions D3320–D3335 with the owner's answers, resolutions R1–R6, tensions T1–T3 (T2 withdrawn), open items O1–O2 (both answered 2026-11-23), UNVERIFIED U1–U5. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | One contract (C1), one integration (I1) and thirteen tasks (IS1–IS13) in five levels, the edges broken and kept, shared files, Checks 920–921. **Nothing in it is approved** |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan as the graph `fugaro plan graph` reads |

## Typos fixed in the spec

None.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D3320–D3339** | Taken 2026-11-23 above D3319, the highest number written or reserved on `main` or in any open pull request that day. D3320, D3321 and D3323–D3335 are written and answered (D3334 since withdrawn); D3322 was drafted, folded into D3326 before it was asked, and is not reused; D3336–D3339 are reserved and unused |
| **Checks 920–929** | Taken 2026-11-23 above Check 913, the highest on `main` or in any open pull request. 920 and 921 are assigned (owner-run, after the release, script written by IS12); 922–929 are reserved and unused |

## Related

- [../README.md](../README.md): the parent spec, whose S3 (B3.1–B3.8) this extends.
- [../secret-readers.md](../secret-readers.md): the audit and the guard's exceptions (B0.1, B0.2).
- [../dynamic-delivery/](../dynamic-delivery/README.md): the `fugaro_u_`/`fugaro_p_` grammar, the
  intake and the prefix-conditioned grant this plan reuses.
- [../../project-workflow-secrets/](../../project-workflow-secrets/README.md): the project copy and
  allow-list (D1260–D1277) that D3324 replaces.
