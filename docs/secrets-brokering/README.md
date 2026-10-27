# Secrets and credential brokering

**Status: draft reconciliation; nothing is implemented; no task approved.** Decisions D490–D506
are proposed and not answered. No implementation plan is written yet: it follows the owner's
answers.

**Spec owner:** the Fugaro owner. **Date:** 2026-10-27. **Base:** `main` at `db4c5f390`.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, "Fugaro Secrets and Credential Brokering" (2026-10-27), verbatim. The customer it names is replaced by a placeholder |
| [reconciliation.md](reconciliation.md) | What exists (§2); who reads Secret Manager today (§3); findings F1–F27 (§4); proposed decisions D490–D506 and resolutions R1–R4 (§5); what 0.12.1 does meanwhile (§6); tensions T1–T6 (§7); UNVERIFIED U1–U7 (§9). **Where it disagrees with `spec.md`, it wins** |

## Typos fixed in the spec

None.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D490–D519** | D490–D506 proposed; D507–D519 reserved and unused. Above the Bitbucket workspace-token work's D460–D489, on `main` and in every open PR |
| **Checks 190–199** | 190 and 191 proposed, not written; 192–199 reserved and unused. Above Checks 180–189 |

## Related

- [../credentials-service/](../credentials-service/README.md): the service this spec extends
  (D130–D154, Q6).
- [../bitbucket-workspace-token/](../bitbucket-workspace-token/README.md): the 2026-10-27 rulings
  D460–D477, which the proxy partly supersedes (reconciliation F22).
- [../bitbucket-credentials/](../bitbucket-credentials/README.md): D363, whose parked option (d),
  a push and pull-request proxy, is this spec's Bitbucket half.
