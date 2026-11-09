# Dynamic secret delivery

**Status: plan only; nothing is implemented; no task approved.** The owner answered D1930–D1943 on 2026-11-09, all as recommended (D1938–D1940 after review round 1, D1941–D1943 after round 2). **One item, O1 (replay of a gcloud-audience token at the grant route), awaits the owner.**

**Spec owner:** the Fugaro owner. **Date:** 2026-11-09. **Base:** `main` at `934bb23f8`.

The addendum replaces Terraform-mounted secrets with runtime delivery through the credential service: an
intake function writes per-user and shared secrets, the service is the only reader, a job gets a slip, and
the LLM proxy stays behind the gateway. It merges with [../spec.md](../spec.md) (D490–D506) and
[../../credentials-service/](../../credentials-service/README.md) (D130–D154, Q6) through the unified design
in the reconciliation.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The addendum, verbatim |
| [reconciliation.md](reconciliation.md) | The unified design (§4), conflicts C1–C10 (§3), findings F1–F37, decisions D1930–D1943, resolutions R1–R12, open item O1, UNVERIFIED. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices DS0–DS4, tasks, levels, critical path, shared-files table, Checks 510–511. **Nothing in it is approved** |

## Typos fixed in the spec

None.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D1930–D1959** | D1930–D1943 written and answered 2026-11-09; D1944–D1959 reserved and unused |
| **Checks 510–519** | 510 and 511 assigned (owner-run); 512–519 reserved and unused |
