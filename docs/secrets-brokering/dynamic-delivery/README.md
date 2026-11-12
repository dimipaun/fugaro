# Dynamic secret delivery

**Status: plan only; nothing is implemented; no task approved.** The owner answered D1930–D1946 on 2026-11-09, all as recommended (D1938–D1940 after review round 1, D1941–D1943 after round 2, D1944–D1946 after round 3). No item is open.

**Spec owner:** the Fugaro owner. **Date:** 2026-11-09. **Base:** `main` at `934bb23f8`.

The addendum replaces Terraform-mounted secrets with runtime delivery through the credential service: an
intake function writes per-user and shared secrets, the service is the only reader, a job gets a slip, and
the LLM proxy stays behind the gateway. It merges with [../spec.md](../spec.md) (D490–D506) and
[../../credentials-service/](../../credentials-service/README.md) (D130–D154, Q6) through the unified design
in the reconciliation.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The addendum, verbatim |
| [reconciliation.md](reconciliation.md) | The unified design (§4), conflicts C1–C10 (§3), findings F1–F37, decisions D1930–D1946, resolutions R1–R12, UNVERIFIED. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices DS0–DS4, tasks, levels, critical path, shared-files table, Checks 510–511. **Nothing in it is approved** |
| [check510-steps.md](check510-steps.md) | Check 510's owner steps (U3, F30: the name-prefix IAM condition, the grants IAM may not be able to make conditioned, the secret ID length). The owner ran it on 2026-11-11 (its conditioned create was ADMITTED, contradicting F30's create half) and again on 2026-11-12 (a create outside the prefixes was REFUSED, so create is fenced by name and DS2.2 conditions it) |
| [check511-steps.md](check511-steps.md) | Check 511's owner steps (U4: a request every N seconds keeps one service instance up). The owner ran it on 2026-11-12: a request every 15 s kept one instance up for the whole 300 s window (20 of 20 answered, no cold start), with `min_instance_count` 0 |

## Typos fixed in the spec

None.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D1930–D1959** | D1930–D1946 written and answered 2026-11-09; D1947–D1959 reserved and unused |
| **Checks 510–519** | 510 and 511 assigned (owner-run); 512–519 reserved and unused |
