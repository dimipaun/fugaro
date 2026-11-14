# Bitbucket token types in the credential proxy

**Status (2026-11-14): one task of the plan below is written — C1, the token-store contract, the four files its Files line
names (`internal/bbtokens/bbtokens.go`, `stub.go`, `bbtokens_test.go` and `stub_test.go`) with `internal/projectpolicy/tokens_shape.go`
and `docs/bitbucket-token-types/contract.md` besides, on the branch `fugaro/20261113-201001-7a01`, with its red step run; the
plan is otherwise plan only, and every other task of it is written by the runs launched beside this one.** The owner approved
every task of this plan (2026-11-13, PR #2586), and C1 is the contract T3, T4, T8, T13 and T16 are written against. Owner: the
project owner (the spec names none). Date: 2026-11-14. Checked against `origin/main` at `a7921b57`.

*The status as first written, kept:* **Status: plan only; nothing is implemented; no task approved.** Owner: the project
owner (the spec names none). Date: 2026-11-13. Checked against `origin/main` at `abd0a60af`.

The spec: the credential service's Bitbucket proxy accepts any mix of repository, project and workspace access tokens,
records each token's coverage, picks the narrowest covering token per request, checks a token when it is added, and
backs off on rate limits.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received, byte for byte (no typo was fixed). **Where [reconciliation.md](reconciliation.md) disagrees with it, the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | The spec against the code and against `docs/bitbucket-credentials`, `docs/bitbucket-workspace-token` and `docs/secrets-brokering`: assumptions, findings F1–F23, decisions D2350–D2356 with the owner's answers, resolutions R1–R14, open items O1–O7, UNVERIFIED U1–U8 |
| [implementation-plan.md](implementation-plan.md) | Five slices, 19 tasks (one contract, one integration task) and one anticipated change request, levels, critical path, shared-files table. The owner approved every task of it (2026-11-13, PR #2586) |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads |
| [contract.md](contract.md) | The token-store contract C1 states: what each side owes, the record, the status, the snapshot, the refusals and the node's key |

## Number blocks taken

Decisions **D2350–D2379** (D2350–D2356 used), checks **590–599** (590 and 591 used), findings, resolutions, tasks local to this folder
(F1–F23, R1–R14, O1–O7, U1–U8; C1, T1–T16, I1, CK; change request CR1). Highest in use on `main` when taken: D2314 and Check 582
(`docs/shared-jobs-default` holds D2300–D2349 and 580–589); open pull requests' documents reach D2207.

## What a reader should not take from here

- The spec's "secret in Secret Manager, the record a reference" is not followed: tokens are keys of the credentials bundle (D2350, as D506 ruled for the single token).
- The spec's coverage does not replace `allow_data_to`: a call needs both (D2354).
- The mounted Secret Manager copy of a Bitbucket token is on its way out (owner, 2026-11-13) and is not designed for here (F18).
- This work does not unblock Bitbucket shared jobs by itself: they still wait on B5.1 (#2545), B5.3 and `docs/shared-jobs-default` (F19).
- The decisions D2350–D2356 were all answered as recommended; O1–O7 are proposals the plan builds with unless the owner changes them.

**Update 2026-11-13:** the owner confirmed O1 to O3 (the proposals), O5 (the doubled-dash key spelling) and O7 (a 403 from `GET /2.0/user` is an access token with no identity). No task is approved; the owner asked for none to launch yet.
