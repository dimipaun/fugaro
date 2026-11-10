# Bitbucket support through the credentials service: reconciliation against the secrets-brokering proxy chain

**What this is.** On 2026-11-10 the owner ruled that **S3 of this folder is superseded by the proxy
chain** of [../secrets-brokering/](../secrets-brokering/README.md): the service's Bitbucket git and
REST route puts the credential on the wire itself, so a run that the published node admits with
`proxy: true` holds no Bitbucket token at all — and a service-minted run token is therefore no
longer wanted. [README.md](README.md) records that ruling. This document is the same reading taken
over **every** task of this plan, not only S3: for each task, what in the proxy chain or in merged
code already builds it, and whether it is **Done**, **Superseded** (naming the task that wins) or
**Kept** (with why).

**It adds to [reconciliation.md](reconciliation.md); it does not replace it.** That document
reconciles the owner's spec against the repository and against Atlassian's own documentation, and
it wins where it disagrees with the spec. This one reconciles *the plan* against work that landed
after it was written: the B1 chain of the secrets-brokering plan, and the tasks of
[../bitbucket-workspace-token/](../bitbucket-workspace-token/README.md) that chain re-planned.
Nothing in [reconciliation.md](reconciliation.md) is revised here, and no historical text anywhere
is rewritten: what changes is the plan, and it changes by dated note.

**It approves nothing and renumbers nothing.** A task marked Done was built by work that merged
under another task's number, and its own number stays where the plan wrote it.

**Method.** Every claim below was read on `origin/main` at `256106ec`, with `git grep`, `git
ls-tree` and `git log` against the merged tree — never from a commit title, from this folder's own
status line, or from `../secrets-brokering/status-2026-11-07.md`, whose rows for B1.7, B1.10 and
B1.11 were written before those three merged and are stale on `main` today. A task counts as Done
only when the file and the named test (or, where the plan names none, the behaviour it describes)
are in the tree.

## 1. The winning code, as it stands on `main`

| What it is | Where it lives | Task |
| --- | --- | --- |
| The service's Bitbucket git and REST route | `internal/credsvc/bitbucketproxy.go`, served under `BitbucketProxyPrefix` and wired into the service's own handler | B1.5, merged `3a72fd28` (#1881) |
| The proxy's rules: what a run may fetch, push and read over REST | `internal/gitproxy/rules.go` (`Classify`, `Decide`, `CheckPRWrite`) and `internal/gitproxy/pktline.go` (`ReadUpdateRequest`) | B1.4, merged `a5f28490` (#1341); B1.3, merged `6492f403` (#1307) |
| The runner's loopback forwarder, which holds nothing and signs every request fresh | `internal/gateway/gitforward.go` | B1.6, merged `1031563d` (#1943) |
| The runner on the proxy | `internal/gitprov/providers/providers.go`'s `Proxied(env []string, forwarder string, hc *http.Client, warn func(string))`, installed by `internal/runner/runner.go`'s `startProxiedGit` | B1.7 |
| The node's `bitbucket_token` block, its `proxy` switch, `bitbucket_api_roots`, policy version 4 | `internal/config/project_secrets.go`'s `Proxy`, `internal/projectpolicy/bitbucket.go` (`BitbucketToken`, `DeriveBitbucketToken`, `SharedBitbucketProfile`, `ValidBitbucketAPIRoots`), `internal/projectpolicy/match.go`'s `MatchRepo` | BW-T2.1, merged `ca9659b4` (#1329) and `d23d364b` (#1336) |
| Publish derives the block and never drops it | the publish path in `internal/cli` | BW-T2.3, merged `cf65a8fc` (#1340) |
| The API-root pin a cloud run keeps | `internal/runner/runner.go`'s `checkBitbucketAPIRoot` | BW-T2.8, merged `7b965bc6` (#1344) |
| The empty flip list, and the mount and accessor a filled one drops | `internal/config/providers.go`'s `ProxiedGitSecretNames`, `internal/infra/spec.go`'s `RepoSpec.BitbucketProxied`, `deploy/terraform/gcp/modules/workflow/sa.tf` | B1.8, merged `0c973faa` (#1338) and `1bc8a846` (#1349) |
| The operator-facing text and the proxy's own owner checks | `SECURITY.md`'s "The Bitbucket git proxy", `docs/git-providers.md`, `docs/credentials-service/runbook.md` §11; `scripts/live-checks/check191.sh` and the register's rows 191 and 192 | B1.10 and B1.11 |

Two things are **not** on `main`, and both matter below: `internal/config/providers.go`'s
`ProxiedGitSecretNames` is still `[]string{}`, so no installation is proxied yet and the job's
mount stays (the flip is B1.13 part two, held on Check 191 in draft #2166); and this folder's own
S2 read path — `ee/coordinator/cli/coordinator_show.go`, `internal/credsvc/bitbucket.go`'s read
mint, `internal/credsvc/auth.go`'s person class — is untouched by the chain, because it reads
through a **person's** credential rather than a run's.

## 2. Every task, and what builds it

**Merged, for the record.** T0.1 and T0.2 (this folder's guard
`internal/cli/docs_bitbucket_credentials_test.go`, and the register's rows and sections for Checks
145, 146 and 149, held by `internal/cli/docs_checks_145_149_test.go`); T1.1 and T1.2
(`ee/coordinator/cli/coordinator_view.go` and the console's P6); **BB1** (`internal/gitprov/bitbucket/oauth.go`);
**BB2** (`internal/credsvc/bundle.go`'s `KindBitbucketReadConsumer` and `BitbucketReadConsumerSecret`);
**BB3** (`scripts/live-checks/check145.sh`); **BB4** (`internal/credsvc/bitbucket.go`,
`internal/credsvc/auth.go`'s `PersonAccounts`, `ee/coordinator/cli/coordinator_show.go`); **BB5**
(`scripts/live-checks/check146.sh`). The proxy chain does not touch any of them: none holds a
Bitbucket token for a run.

| Task | Verdict | What builds it, and what is left |
| --- | --- | --- |
| **T1.3** Check 149 (OWNER-RUN) | **Kept** | Nothing changes it. The read it confirms is merged and the register's `\| 149 \|` row reads **USER-RUN, NOT RUN**: it is the owner's, and no agent runs any step of it. |
| **T2.6** the read consumer's runbook | **Kept — owed** | Nothing builds it. `docs/credentials-service/runbook.md` has no sentence about the read consumer (its §11 is the proxy's, a different holder), and there is no runbook pin in `internal/cli`. The code it documents is merged: `internal/credsvc/bundle.go`'s `BitbucketReadConsumerSecret`, `internal/credsvc/auth.go`'s person class, `ee/coordinator/cli/coordinator_show.go`. |
| **T2.11** `SECURITY.md` for S2 | **Kept — owed** | Nothing builds it. `SECURITY.md` carries B1.10's "The Bitbucket git proxy", which is about the **run's** git credential; the read consumer, the read mint's two caller classes and the read mint's custody (O6) are still unwritten, and `SECURITY.md` has no sentence naming either. |
| **T3.0** the per-repository `bitbucket_via_service` switch, rendered into the job | **Superseded by BW-T2.1 and B1.7** | The switch is now the **published node's**: `internal/config/project_secrets.go`'s `Proxy`, derived into `internal/projectpolicy/bitbucket.go`'s `BitbucketToken.Proxy`, and read by `internal/gitprov/providers/providers.go`'s `Proxied`. That is stronger than the design T3.0 rejected the shared config for: the switch is the operator's own published node rather than a job environment variable Terraform writes, and a launcher's local config cannot override it. |
| **T3.1** the run mint, with an allow-list and an audit line | **Superseded by B1.4 and B1.5** | Nothing is minted to a run any more. `internal/gitproxy/rules.go`'s `Decide` and `CheckPRWrite`, behind `internal/credsvc/bitbucketproxy.go`, admit a run's traffic to the repositories the node's block names, and the service puts the credential on the wire. There is no run token to allow-list, no audit line for a mint, and no `POST /v1/bitbucket/access-token`. |
| **T3.2** the runner gets its Bitbucket token from the service | **Superseded by B1.6, B1.7 and B1.8** | The runner holds nothing: `internal/gateway/gitforward.go` forwards to the service's route and `internal/gitprov/providers/providers.go`'s `Proxied` is what the runner installs, through `internal/runner/runner.go`'s `startProxiedGit`. T3.2's "never falls back to the mounted token" is B1.8's drop, whose tests are merged, and whose flip — the mount actually leaving the job — is B1.13 part two. |
| **T3.3** comment identity and the cross-repository trust paths | **Superseded in part, Kept in part** | The identity half is superseded: there is no run consumer, so no consumer-owned person identity to detect, and what a run may comment is B1.4's REST allow-list (`CallPRComment`, `CallCommentList`) behind B1.5's route. The trust-path half is **Kept** and still owed: `internal/gitprov/bitbucket/comments.go`'s `selfUser` and `Comments` and `internal/followup/select.go`'s `MarkersFromAnyone` are in the tree from other work, but T3.3's four tests are absent, and the proxy makes the question live again rather than moot — a proxied run's comments are written with the operator's own token. |
| **T3.4** `SECURITY.md` and the docs say it plainly | **Superseded by B1.10 and B1.13 part one** | Its subject was the reach of a **minted run token** (workspace-wide for the consumer's owner, for the life of the run plus 1–2 hours). No token is minted. B1.10 wrote the proxy's own section and B1.13 part one the operator notes; the sentences T3.4 was to replace are still true, because a run on a non-proxied repository still holds its own per-repository token. |
| **T3.5** the minimum run scopes, enforced where possible, and their check | **Superseded — and Check 147 with it** | Nothing is minted, so there is no run scope set to enforce: the service spends the credential the operator stored, whose scopes are the operator's own. Check 147 has no script and its subject is gone (D1960). |
| **T3.6** the per-repository drop of the job's own `bitbucket-token` | **Superseded by B1.8** | `internal/config/providers.go`'s `ProxiedGitSecretNames` with `internal/infra/spec.go`'s `RepoSpec.BitbucketProxied` and `deploy/terraform/gcp/modules/workflow/sa.tf` is the same drop, keyed on the published node instead of on a per-repository local switch. Its tests are merged; only the **flip** is owed (B1.13 part two, held on Check 191 in draft #2166). |
| **T3.7** Check 147 (OWNER-RUN) | **Superseded by B1.11 and B1.12** | The check is dropped with T3.5, and the proxy's own owner-run check takes its place: `scripts/live-checks/check191.sh` and the register's `\| 191 \|` row, with 192 for the flip. |
| **T3.8** Check 148: the stop, rotation and ownership-transfer drill | **Superseded in part, Kept in part** | The run-mint half goes with S3. The **read** half — the read consumer's stop, rotation and ownership transfer, with U13 — is Kept and is D1961: the read consumer is merged and shipped, and nothing else exercises its rotation. |

**One consequence worth stating plainly.** S3 was parked "until a push and pull-request proxy
exists". That proxy now exists, and it does not want S3: the chain keeps the credential in the
service instead of minting one to the run, so S3's premise (a run that holds a service-minted
token) is the thing the chain removes. The two remaining live halves of S3 are T3.3's trust paths
and T3.8's read drill, and neither is a run mint.

## 3. Decisions

**D1960–D1979 is this reconciliation's block**, taken fresh because D1959 is the highest decision
on `main` and no open pull request writes D1960 or above. D1960–D1962 are written below, each with
a recommendation and an **empty answer cell**; D1963–D1979 are reserved and unused. The block sits
above D360–D389, which is this folder's own, and beside
[../bitbucket-workspace-token/](../bitbucket-workspace-token/README.md)'s D1980–D1999, which this
same reconciliation takes.

| Decision | Recommendation | Answer |
| --- | --- | --- |
| **D1960** Check 147 has no script, and its subject — a sandbox run on a service-minted token, with exactly the minimum scopes — is gone with T3.5. Retire the number as reserved and unused, or hold it for a proxy check? | **Retire it.** The register has no `\| 147 \|` row and no `## Check 147:` section, and none is owed: the proxy's owner-run checks are 191 and 192, written and registered by B1.11. A check number the plan still names as "written by T3.5" points a reader at a task that will never be built. The folder's checks table then reads 145, 146, 148 (restored, D1961) and 149. | |
| **D1961** The **read** half of Check 148 — the read consumer's stop, rotation and ownership-transfer drill, with U13 — is the half the plan parked with S3 (the run-mint half is superseded). Bring it back into S2, or leave it parked? | **Bring it back into S2**, as an owner-run check written beside T2.6, which documents the same four operations. The read consumer is merged and shipped (BB2, BB4, BB5) and is the only credential in this folder that a person owns and can lose: D367 asked for rotation and ownership transfer to be a task **and** a check, and T2.6 alone is the task. | |
| **D1962** T2.6 is blocked on Check 145, which is NOT RUN and needs a Premium sandbox workspace that is not known (U6, D468); U14 (whether creating a consumer needs workspace-admin rights) and U15 (whether the consumer must be private) are two of the clauses it would state. Write the runbook now with those clauses marked **UNVERIFIED**, or hold the task until Check 145 runs? | **Write it now with the clauses marked UNVERIFIED.** The rest of the procedure — the read scopes, the stop switch, rotation, ownership transfer, who the consumer is minted to, the two caller classes — is settled by merged code and by the owner's own B1–B6 answers, and a runbook an operator cannot read until a sandbox exists is a runbook nobody has. The two clauses are the ones Check 145 settles, and the folder already treats UNVERIFIED as a first-class state. | |

## 4. Numbers this work holds

| Block | Use |
| --- | --- |
| **D360–D389** | Unchanged: **D360–D367** written and answered (2026-10-26); **D368–D389** reserved and unused. |
| **Checks 145–154** | **145, 146, 148 and 149** are the plan's; **147** is retired by D1960 and **150–154** stay reserved. |
| **D1960–D1979** | **This reconciliation's block**: D1960–D1962 are written above, D1963–D1979 reserved and unused. It sits above D1959, the highest decision on `main`, and beside the workspace-token folder's D1980–D1999. |

## 5. UNVERIFIED

- **Nothing here was checked live.** Every row is a reading of `origin/main`; no run was launched,
  no service was called and no credential was read. Checks 145, 146, 148 and 149 stay the owner's,
  and none of them has been run.
- **The flip is not done** (`ProxiedGitSecretNames` is empty), so no installation is proxied today
  and the reach `SECURITY.md` states is still the live one. Everything this document says about a
  proxied run is a reading of code that no released installation runs yet.
- **B1.13 part two** is held in draft #2166 on Check 191, which is unrun. Nothing here presumes its
  outcome.
