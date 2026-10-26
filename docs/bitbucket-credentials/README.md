# Bitbucket support through the credentials service

**Status: design only; nothing is implemented; no task approved.** Decisions D360–D367 are
drafted and none of them has an owner's answer yet. The implementation plan is not written: it
comes after the owner answers.

**Spec owner:** the Fugaro owner. **Date:** 2026-10-26. **Base:** `main` at `ce991d88d`.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's request (2026-10-26) and the context it was relayed with, **verbatim** |
| [reconciliation.md](reconciliation.md) | The draft reconciliation: what the repository already has (§2); Bitbucket Cloud's credential types, verified against Atlassian's own pages (§3); findings F1–F18 (§4); decisions D360–D367, each with its recommendation first and no answer yet (§5); the smallest first slice, migration and threat model (§6); tensions T1–T3 (§7); UNVERIFIED U1–U9 (§9). **Where it disagrees with the spec, it wins** |

## Typos fixed in the spec

None. The spec is byte for byte as received.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D360–D389** | this work's decision block. **D360–D367** are written and open; **D368–D389** are reserved and unused. Taken above the highest decision on `main` (task governance's D320–D359) and in every open PR's docs, none of which reaches D334 |
| **Checks 145–154** | this work's check block, reserved and unused. Taken above the highest check block on `main` (task governance's Checks 135–144); no open PR writes Check 135 or above |
