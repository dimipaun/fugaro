# Bitbucket support through the credentials service

**Status as of 2026-11-10: in progress, 9/12 merged; see [docs/status.md](../status.md) for the current table.** The owner answered decisions
D360–D367 on 2026-10-26, and D360, D362, D363 and D367 went against the recommendation. Those
answers approve the reconciliation. Each task of the plan still needs the owner's own explicit go.

**Amended 2026-11-10 (owner ruling): S3 is superseded by the secrets-brokering proxy chain.** The run-token
slice S3 was parked "until a push and pull-request proxy exists"; that proxy is the B1 chain of
[docs/secrets-brokering](../secrets-brokering/), under which no run holds a Bitbucket token at all, so a
service-minted run token is no longer wanted. S3 (T3.0 to T3.8, Checks 147 and 148, the run consumer) stays in
the plan as history and is not built. **Kept:** S0 to S2, the coordinator's read path through the service's
read consumer, with T2.6 (the runbook), T2.11 (the SECURITY.md text) and owner Checks 145 and 149.

**The same ruling, read over the whole plan (2026-11-10).** [reconciliation-secrets-brokering.md](reconciliation-secrets-brokering.md)
takes the reading over every task of this plan — merged and unmerged — and names, for each, the
secrets-brokering task or the merged code that already builds it, verified on `origin/main`. Its
verdicts: **every S3 task is superseded** — T3.0 by the published node's `proxy` switch (BW-T2.1)
with B1.7, T3.1 by B1.4 and B1.5, T3.2 by B1.6, B1.7 and B1.8, T3.4 by B1.10, T3.6 by B1.8's flip,
and T3.5 with T3.7 and Check 147 — with two exceptions: **T3.3**, whose comment-identity half goes
with the run consumer while its cross-repository trust paths stay owed, and **T3.8**, whose read
half is the read consumer's stop, rotation and ownership-transfer drill. **S2 is untouched, and the
chain builds none of it**: T2.6 (the runbook) and T2.11 (`SECURITY.md`) are still owed, and T1.3
(Check 149) is still the owner's own run. **What falls out of the reading are D1960–D1962**, each
with a recommendation and an answer cell the owner has not filled.

**One precision on the ruling above.** "No run holds a Bitbucket token" is true of the run's *git
traffic*, and the proxy that gives it that is live in the released 0.22.0 — a node that publishes
`proxy: true` proxies its runs today. The **job** still holds the token until the flip: the empty
`config.ProxiedGitSecretNames` is the mount and accessor drop, so a proxied job keeps its
`bitbucket-token` secret, one read of the runner's `/proc` away.

**Amended 2026-10-26, after #1104:**

- **O1 is answered: D363 is reversed.** Runs keep their per-repository access tokens. The run-token
  slice **S3 is parked** until a push and pull-request proxy exists. It is deferred, not cancelled.
- **D360 is narrowed back** to coordinator reads only, which is the recommendation.
- **O2 is answered:** two consumers. Only the read consumer is built now.
- **O7 is answered:** review-only and artifact runs get no write token. That is the rule for when
  S3 returns.
- **T1 is moot** until S3 returns.
- The active plan is **S0–S2**: the key-free stored-view read, and Bitbucket in the coordinator's
  reads through the service's read consumer.
- **Still waiting on the owner:**
  - whether the read half of Check 148 (the read consumer's rotation drill) should come back into
    S2. **It is D1961 now**, with a recommendation and an empty answer cell, in
    [reconciliation-secrets-brokering.md](reconciliation-secrets-brokering.md) §3.
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
| [reconciliation-secrets-brokering.md](reconciliation-secrets-brokering.md) | The plan read against the secrets-brokering proxy chain (2026-11-10): the winning code and where it lives (§1); every task with its verdict — Done, Superseded or Kept (§2); decisions D1960–D1962, with recommendations and empty answer cells (§3); the numbers this reading holds (§4); UNVERIFIED (§5) |
| [implementation-plan.md](implementation-plan.md) | Active slices S0–S2, and S3 parked (2026-10-26). S2 was **retargeted 2026-11-01** at the owner's B1–B6 answers: its tasks are now BB1–BB5, its first consumer is `coordinator show`, and "What the retarget moved" maps every old number. Slices S0–S3 and tasks T0.1–T3.8 (with BB1–BB5, T2.6, T2.11 and T3.0), each with its files, tests, red step and reviewer tier. It also gives five parallel levels, the critical path, the shared-files table and owner-run Checks 145–149. **Nothing in it is approved** |

## Typos fixed in the spec

None. `spec.md` is byte for byte the text this work received. That text is itself the owner's words relayed "near verbatim", as its header says.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D360–D389** | This work's decision block. **D360–D367** are written and answered (2026-10-26); **D368–D389** are reserved and unused. The block sits above the highest decision on `main` (task governance's D320–D359) and in every open PR's docs, none of which reaches D334 |
| **Checks 145–154** | This work's check block. **145–149** are proposed by the plan (owner-run, NOT RUN); **150–154** are reserved and unused. The block sits above the highest check block on `main` (task governance's Checks 135–144); no open PR writes Check 135 or above |
| **D1960–D1979** | **The block [reconciliation-secrets-brokering.md](reconciliation-secrets-brokering.md) takes** (2026-11-10), because D1959 is the highest decision on `main` and no open PR writes D1960 or above. **D1960–D1962** are written there with recommendations and empty answer cells; **D1963–D1979** are reserved and unused. It sits above this work's own D360–D389 and below the workspace-token folder's D1980–D1999, which the same reconciliation takes |
