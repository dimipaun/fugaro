# Secrets and credential brokering

**Status: plan only; nothing is implemented; no task approved.** The owner answered D490–D506 on
2026-10-27: all as recommended except **D501** (the service keeps scaling to zero: not the recommendation;
D502, staying opt-in, was relayed with it), and **D494**, which follows the recommendation and
**departs from the owner's spec** (no RTDB token store). **D497a is amended after review** (the
claim also names the run's pull requests) and waits for the owner's confirmation (O5). Open items
O1–O6 wait for the owner. Each task needs the
owner's own explicit go.

**Spec owner:** the Fugaro owner. **Date:** 2026-10-27. **Base:** `main` at `db4c5f390`, merged
forward before the PR.

| Release | What |
| --- | --- |
| **0.12.1** | Nothing of this work except one pointer sentence in docs/bitbucket-workspace-token's approved T1.1 |
| **0.13.0** | **S0:** the secret-reader audit as a tested doc, the CI guard, doctor's Secret Manager sweep and deployer read, Check 190 (owner-run) |
| **0.14.0** | **S1:** the Bitbucket proxy for per-repository jobs of opted-in installations, the re-planned Bitbucket S2 tasks, the flip, Checks 191–192 (owner-run) |
| later | S2 model modes, S3 vault fetch, S4 builds, S5 shared jobs |

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, "Fugaro Secrets and Credential Brokering" (2026-10-27), verbatim. The customer it names is replaced by a placeholder |
| [reconciliation.md](reconciliation.md) | What exists (§2); who reads Secret Manager today (§3); findings F1–F28 (§4); decisions D490–D506 with the owner's answers and resolutions R1–R4 (§5); 0.12.1 meanwhile (§6); tensions T1–T6 as the rulings handle them and open items O1–O6 (§7); UNVERIFIED U1–U8 (§9). **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices S0–S5; tasks B0.1–B0.5 and B1.1–B1.14 with files, tests, red steps, commands and reviewer tiers; the re-planned Bitbucket S2; levels, critical path, shared files; Checks 190–192. **Nothing in it is approved** |

## Typos fixed in the spec

None.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D490–D519** | D490–D506 written and answered 2026-10-27; D507–D519 reserved and unused. Above the Bitbucket workspace-token work's D460–D489, on `main` and in every open PR |
| **Checks 190–199** | 190–192 assigned by the plan (owner-run, not written); 193–199 reserved and unused. Above Checks 180–189 |

## Related

- [../credentials-service/](../credentials-service/README.md): the service this spec extends
  (D130–D154, Q6).
- [../bitbucket-workspace-token/](../bitbucket-workspace-token/README.md): D460–D477; its S2 is
  re-planned here (D493).
- [../bitbucket-credentials/](../bitbucket-credentials/README.md): D363, whose parked option (d),
  a push and pull-request proxy, is this spec's Bitbucket half.
