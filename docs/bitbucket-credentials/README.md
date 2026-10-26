# Bitbucket support through the credentials service

**Status: plan only; nothing is implemented; no task approved.** The owner answered decisions
D360–D367 on 2026-10-26. D360, D362, D363 and D367 went against the recommendation. Those answers
approve the reconciliation. Each task of the plan still needs the owner's own explicit go.

Items O1, O2, O4, O6 and O7 wait on the owner, O3 is the owner's per-repository flip, and O5 is a
record (reconciliation §7):

- **O1** holds tasks T3.1, T3.2, T3.3 and T3.6.
- **O2** holds task T2.2.
- **O4** makes Check 145 a precondition of every mint.

**Spec owner:** the Fugaro owner. **Date:** 2026-10-26. **Base:** `main` at `ce991d88d`.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's request (2026-10-26) and the context it was relayed with, **verbatim** |
| [reconciliation.md](reconciliation.md) | What the repository already has (§2); Bitbucket Cloud's credential types, verified against Atlassian's own pages (§3); findings F1–F34 (§4); decisions D360–D367 with the owner's answers of 2026-10-26 (§5); the answers followed through, including the threat model (§6); tensions T1–T3 and open items O1–O7 (§7); UNVERIFIED U1–U16 (§9). **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices S0–S3 and tasks T0.1–T3.8 (with T2.11 and T3.0), each with its files, tests, red step and reviewer tier. It also gives six parallel levels, the critical path, the shared-files table and owner-run Checks 145–149. **Nothing in it is approved** |

## Typos fixed in the spec

None. `spec.md` is byte for byte the text this work received. That text is itself the owner's words relayed "near verbatim", as its header says.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D360–D389** | This work's decision block. **D360–D367** are written and answered (2026-10-26); **D368–D389** are reserved and unused. The block sits above the highest decision on `main` (task governance's D320–D359) and in every open PR's docs, none of which reaches D334 |
| **Checks 145–154** | This work's check block. **145–149** are proposed by the plan (owner-run, NOT RUN); **150–154** are reserved and unused. The block sits above the highest check block on `main` (task governance's Checks 135–144); no open PR writes Check 135 or above |
