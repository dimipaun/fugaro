# Bitbucket support through the credentials service

**Status: plan only; nothing is implemented; no task approved.** The owner answered decisions
D360–D367 on 2026-10-26, and D360, D362, D363 and D367 went against the recommendation. Those
answers approve the reconciliation. Each task of the plan still needs the owner's own explicit go.

**Amended 2026-10-26, after #1104:**

- **O1 is answered: D363 is reversed.** Runs keep their per-repository access tokens. The run-token
  slice **S3 is parked** until a push and pull-request proxy exists. It is deferred, not cancelled.
- **D360 is narrowed back** to coordinator reads only, which is the recommendation.
- **O2 is answered:** two consumers. Only the read consumer is built now.
- **O7 is answered:** review-only and artifact runs get no write token. That is the rule for when
  S3 returns.
- **T1 is moot** until S3 returns.
- The active plan is **S0–S2**: the key-free stored-view read, and Bitbucket in the coordinator
  read through the service's read consumer.
- **Still waiting on the owner:**
  - whether the read half of Check 148 (the read consumer's rotation drill) should come back into
    S2.
- **O4, answered 2026-11-01 (B3):** Check 145 is a precondition of the first read mint. **O6,
  answered 2026-11-01 (B4):** the read mint's custody is accepted and documented. O5 is a record,
  and O3 is parked with S3.

**Amended 2026-10-27 (the workspace-token plan, T1.1): D462 reverses D363 for operators who opt in.**
The owner ruled that an operator who holds one **workspace or project access token** may store it once
for many repositories through the explicit `project_secrets.bitbucket-token` allow-list, so an
allow-listed run's agent holds a credential that reaches every repository the token covers — the whole
workspace for a workspace token, one Bitbucket project for a project token, which is the narrower of
the two. That is the reach D363's
reversal (O1) was written to avoid, and it is now the operator's own choice rather than the
installation's default. What does not move: the default is still a repository's own repository access
token, the allow-list is the only way a Bitbucket token becomes project-wide, and S3 stays parked —
the service mints no Bitbucket run token, and the read consumer of S0–S2 is unchanged. The reach is
stated in `SECURITY.md` and `docs/git-providers.md`, and the path is not yet live-checked (Check 180,
D468).

**Spec owner:** the Fugaro owner. **Date:** 2026-10-26. **Base:** `main` at `ce991d88d`.

**Amended 2026-11-01: the owner answered B1–B6, and S2 is retargeted.** S2's first consumer is no
longer the coordinator job: it is **`coordinator show`** (console-coordinator-v2 C3), a person's
one-pull-request read, which refuses Bitbucket today with `coordinator.ErrBitbucket` exactly as
`coordinator status` does. The read consumer the service holds is minted to a **person** through a
**person caller class** on the read mint that follows the review-identity design's verifier class
(PR #1514, its C3.9f), and the CLI never reads the bundle for it. The slice's tasks are renamed
**BB1–BB5**; T2.6 and T2.11 stay, retargeted in place; S3 stays parked. The owner's answers:

- **B1 = (a).** The token comes from the S2 read consumer, the read-scoped Bitbucket OAuth consumer
  the credentials service holds. Never the runs' write-capable tokens.
- **B2 = (b), through a service route.** The person caller class above, coupled to C3.9's classes:
  the review class (C3.9a) and the verifier class (C3.9f). "`show` for Bitbucket" therefore depends
  on the C3.9 chain.
- **B3 = yes.** Check 145, owner-run, is a precondition before the first read mint (O4).
- **B4 = accept and document** the read consumer's custody (O6).
- **B5 = yes.** After `show`, `coordinator status` and the sweep also gain Bitbucket, through the
  read consumer, for single-workspace projects. A repository in another workspace stays refused,
  with a named reason. Q12 (A) is amended to (B).
- **B6 = release 0.17.0.**

**The plan is retargeted, not re-approved.** Nothing in it is approved, and the retarget changes
which task comes first, not whether any of them may start.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's request (2026-10-26) and the context it was relayed with, **verbatim** |
| [reconciliation.md](reconciliation.md) | What the repository already has (§2); Bitbucket Cloud's credential types, verified against Atlassian's own pages (§3); findings F1–F34 (§4); decisions D360–D367 with the owner's answers of 2026-10-26 (§5); the answers followed through, including the threat model (§6); tensions T1–T3 and open items O1–O7 (§7); UNVERIFIED U1–U16 (§9). **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Active slices S0–S2, and S3 parked (2026-10-26). S2 was **retargeted 2026-11-01** at the owner's B1–B6 answers: its tasks are now BB1–BB5, its first consumer is `coordinator show`, and "What the retarget moved" maps every old number. Slices S0–S3 and tasks T0.1–T3.8 (with BB1–BB5, T2.6, T2.11 and T3.0), each with its files, tests, red step and reviewer tier. It also gives five parallel levels, the critical path, the shared-files table and owner-run Checks 145–149. **Nothing in it is approved** |

## Typos fixed in the spec

None. `spec.md` is byte for byte the text this work received. That text is itself the owner's words relayed "near verbatim", as its header says.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D360–D389** | This work's decision block. **D360–D367** are written and answered (2026-10-26); **D368–D389** are reserved and unused. The block sits above the highest decision on `main` (task governance's D320–D359) and in every open PR's docs, none of which reaches D334 |
| **Checks 145–154** | This work's check block. **145–149** are proposed by the plan (owner-run, NOT RUN); **150–154** are reserved and unused. The block sits above the highest check block on `main` (task governance's Checks 135–144); no open PR writes Check 135 or above |
