# Bitbucket workspace and project tokens: reconciliation against the secrets-brokering proxy chain

**What this is.** This plan's S2 was re-planned against the credentials-service proxy on
2026-10-27 (its D490, D493, D505, recorded in [README.md](README.md) and in
[../secrets-brokering/implementation-plan.md](../secrets-brokering/implementation-plan.md), "The
re-planned Bitbucket S2"). That table was written from the proxy's **design**; the chain has moved
a long way since. This document is the same reading taken against the **merged tree**: for every
task of this plan, what the proxy chain or other merged code already builds it, and whether the
task is **Done**, **Superseded** (naming the task that wins) or **Kept** (with why).

**The owner's question, answered here.** [status.md](../status.md) records that "the proxy chain
probably makes T2.2 and T2.9 moot". Half of that holds, and the two are not alike: **T2.9 is
moot** — the service's rules are the boundary now, and a runner-side check binds nothing an agent
cannot bypass — while **T2.2 is not**: the proxy takes the token out of the *job*, and it does not
make the service admit a *shared* job, which is B5.1 and has not started. §2 and §3 say so with the
file and the symbol behind each claim.

**It adds to [reconciliation.md](reconciliation.md); it does not replace it.** Nothing there is
revised, no historical text anywhere is rewritten, and this document approves no task and
renumbers none.

**Method.** Every claim was read on `origin/main` at `256106ec`, with `git grep`, `git ls-tree`
and `git log` against the merged tree — never from a commit title, from this folder's status line,
or from [status-2026-11-07.md](status-2026-11-07.md), whose rows are stale in two places that
matter here: **T1.1's remainder and T1.3 both merged after it was read**, in `3bcbd1dd` (#1880).

## 1. What the proxy chain changed here

| What it is | Where it lives | Task |
| --- | --- | --- |
| The node's `bitbucket_token` block, its `proxy` switch, `bitbucket_api_roots`, policy version 4, and the profile rules | `internal/config/project_secrets.go`'s `Proxy`, `internal/projectpolicy/bitbucket.go` (`BitbucketToken`, `DeriveBitbucketToken`, `SharedBitbucketProfile`, `ValidBitbucketAPIRoots`), `internal/projectpolicy/match.go`'s `MatchRepo` | BW-T2.1, merged `ca9659b4` (#1329) and `d23d364b` (#1336) |
| The block is derived whether or not a profile mounts the token, and publish never drops it | the publish path in `internal/cli` | BW-T2.3, merged `cf65a8fc` (#1340) |
| A cloud run sends its Bitbucket token only to Bitbucket's own API or an owner-listed root | `internal/runner/runner.go`'s `checkBitbucketAPIRoot`, called from `openProvider` for every Bitbucket provider | BW-T2.8, merged `7b965bc6` (#1344) |
| The runner on the proxy, whose git traffic carries no token | `internal/gitprov/providers/providers.go`'s `Proxied(env []string, forwarder string, hc *http.Client, warn func(string))`, installed by `internal/runner/runner.go`'s `startProxiedGit`; `internal/gateway/gitforward.go` forwards to the service's route | B1.6 (`1031563d`, #1943) and B1.7 |
| The empty flip list, and the mount and accessor a filled one drops | `internal/config/providers.go`'s `ProxiedGitSecretNames`, `internal/infra/spec.go`'s `RepoSpec.BitbucketProxied`, `deploy/terraform/gcp/modules/workflow/sa.tf` | B1.8, merged `0c973faa` (#1338) and `1bc8a846` (#1349) |
| The operator-facing text and the owner-run checks | `SECURITY.md`'s "The Bitbucket git proxy", `docs/git-providers.md`, `docs/credentials-service/runbook.md` §11; `scripts/live-checks/check191.sh` and the register's rows 191 and 192 | B1.10 and B1.11 |

**Two things this plan's S2 depends on are still not in the tree**, and both are why T2.2 waits:
`internal/infra/projectjobs.go`'s `SharedJobProvider` still admits `github` alone (so
`TestProjectJobsSkipBitbucketProfiles` still passes), and `internal/projectpolicy/bitbucket.go`'s
`SharedBitbucketProfile` — T2.1's own interface, and the predicate T2.2 was to build on — has no
production caller at all. And `internal/config/providers.go`'s `ProxiedGitSecretNames` is still
`[]string{}`, so a proxied job keeps its `bitbucket-token` mount and accessor: the flip is B1.13
part two, held on Check 191 in draft #2166.

**The proxy itself is live in the released 0.22.0, and that list does not hold it back.** What makes
a run proxied is the **published node** alone — `internal/runner/project.go`'s `proxiedBitbucket`
reads the repository's `proxy: true` and the job's credentials-service URL — so a node that
publishes `proxy: true` proxies its runs today, and the service's own route
(`internal/credsvc/bitbucketproxy.go`) landed a release earlier, in 0.21.0. The empty list holds
back the **mount** drop alone: while it is empty the token stays in the job, one read of the
runner's `/proc` away, even though the run's own git traffic carries none.

## 2. Every task, and what builds it

| Task | Verdict | What builds it, and what is left |
| --- | --- | --- |
| **T1.1** docs: one token for many repositories | **Done** | Merged, **including the one pointer sentence D490 added to it**: `docs/git-providers.md` carries it ("A proxy that keeps the token out of the run's own git traffic is designed in docs/secrets-brokering and **built**"), and `internal/cli/docs_project_wide_git_test.go`'s `TestDocsStateTheBitbucketProjectTokenReach` pins it. Both landed in `3bcbd1dd` (#1880). The census's "merged except for the one pointer sentence" is stale. |
| **T1.2** CLI text names the workspace or project token | **Done** | Merged; unchanged by the chain. |
| **T1.3** Check 180 written into the register | **Done** | `docs/gcp-live-checklist.md` has both halves — its row 180 and its `## Check 180:` section — with `scripts/live-checks/check180.sh` and `check180_test.sh` beside them, held by `internal/cli/docs_checks_180_181_test.go`. Landed in `3bcbd1dd` (#1880). The census's "not started" is stale. |
| **T1.4** Check 180, OWNER-RUN | **Kept** | Nothing changes it. The register's row reads **USER-RUN, NOT RUN**, and no Premium sandbox workspace is known (U6). No agent runs any step of it. |
| **T2.1** the node's block, `bitbucket_api_roots`, the profile rules, policy v4 | **Done** | Built by **BW-T2.1**, amended: the block is derived whether or not a profile mounts the token, it carries `proxy`, and `VersionSince[4]` is 0.14.0's. |
| **T2.2** deploy and launch Bitbucket shared jobs | **Kept — waiting on B5.1** | **Not moot.** The proxy takes the token off the run's git traffic (the job's own mount stays until the flip); it does not make the service admit a shared job. `internal/infra/projectjobs.go`'s `SharedJobProvider` still admits `github` alone and `TestProjectJobsSkipBitbucketProfiles` still passes, and `projectpolicy.SharedBitbucketProfile` has no production caller. B5.1 — shared project jobs at the service, then Bitbucket shared jobs — is not started. D1980. |
| **T2.3** policy publish derives the block and runs the check | **Done** | Built by **BW-T2.3**: publish derives the amended block and never drops it. |
| **T2.4** `fugaro doctor` warns about an own copy a shared job ignores | **Kept — waiting on B5.1** | Its subject is a **shared** job's use of the project token, which the proxy does not change until B5.1 admits shared jobs. The secrets-brokering plan's "moot for proxied repositories" reads a per-repository proxied job, which T2.4 was never about: on such a repository the runner spends no token, though the job still mounts one until the flip. D1982. |
| **T2.5** folded into T2.9 | **Superseded by B1.4 and B1.5** | It went with T2.9, and T2.9 is replaced. |
| **T2.6** docs: phase 3 serves Bitbucket too | **Kept in part — one sliver is owed now** | The phase-3 half waits for S5 with T2.2. Three corrections do not: the `FUGARO_BITBUCKET_API_URL` variables-table row does not name `bitbucket_api_roots`; the section still says the root lock "is 0.13.0's (D474, T2.8)" when BW-T2.8 shipped it in 0.14.0; and "Bitbucket stays on the per-repository path" says each Bitbucket repository "keeps its own job and its own `bitbucket-token`", which the flip makes false. D1983. |
| **T2.7** Check 181, OWNER-RUN | **Kept** | The register has no row 181 and no `## Check 181:` section; `internal/cli/docs_checks_180_181_test.go` says 181 joins them with T2.6's section. It waits for T2.6 and S5. |
| **T2.8** a cloud run pins the Bitbucket API root | **Done** | Built by **BW-T2.8**: `internal/runner/runner.go`'s `checkBitbucketAPIRoot`, called from `openProvider` for every Bitbucket provider, proxied or not. On a proxied repository the runner has no token to send, so the runs it still protects are the non-proxied ones, whose runner spends the job's token. Its operator step is missing from the shipped notes — D1984. |
| **T2.9** the runner enforces the block on a shared job | **Superseded by B1.4 and B1.5** | Verified absent: `internal/runner/projectjob.go` has `checkProjectJobAccount` and no Bitbucket check, and its test file is not in the tree. The boundary is now `internal/gitproxy/rules.go`'s `Decide` and `CheckPRWrite`, behind `internal/credsvc/bitbucketproxy.go` — a place a runner-side check cannot be, because a check in the job binds nothing the agent cannot bypass (F5). D1981. |
| **T2.10** OFF (D476) | **off** | Unchanged, and the proxy does not revive it: the subject was a review-only run refusing a pull request its own token opened, and a proxied run's git traffic carries no token. |
| **T2.11** the 0.13.0 lockstep upgrade notes | **Superseded by B1.13 part one — one step is missing** | `docs/secrets-brokering/release-0.14.0-operators.md` carries the v4 lockstep, the image refresh, the republish order and the profile rules. It does **not** carry T2.11's step 5: an installation that sets `FUGARO_BITBUCKET_API_URL` must list that root in `bitbucket_api_roots` and republish before it upgrades its runners. `docs/releases/` names neither the variable nor the list, and the pin is live in every runner built from 0.14.0 on. The shared-job step waits for S5. D1984. |
| **T3.1** the `allow_data_to` wildcard | **Superseded by run dc73** | Unchanged (D464), as the README already records. |

**One consequence worth stating plainly.** The owner's D460 ruling — that Bitbucket repositories
also run in shared project jobs — is not what the proxy chain delivers. The chain delivers the
opposite for the per-repository job: the job keeps its own job and loses its token. A shared
Bitbucket job still needs B5.1, and until then T2.2, T2.4, T2.6's phase-3 half and T2.7 stay where
they are.

## 3. Decisions

**D1980–D1999 is this reconciliation's block**, taken fresh because D1959 is the highest decision
on `main` and no open pull request writes D1960 or above. D1980–D1984 are written below, each with
a recommendation and an **empty answer cell**; D1985–D1999 are reserved and unused. It sits above
D460–D489, which is this folder's own, and above D1960–D1979, which
[../bitbucket-credentials/reconciliation-secrets-brokering.md](../bitbucket-credentials/reconciliation-secrets-brokering.md)
takes in the same reading.

| Decision | Recommendation | Answer |
| --- | --- | --- |
| **D1980** T2.2 (deploy and launch Bitbucket shared jobs) — moot under the proxy chain, or still owed? | **Still owed, and it waits for B5.1.** `internal/infra/projectjobs.go`'s `SharedJobProvider` still admits `github` alone, `TestProjectJobsSkipBitbucketProfiles` still passes, and `projectpolicy.SharedBitbucketProfile` has no production caller. The proxy removes the credential from the job; it does not make the service admit a shared job, and a shared job whose repository is proxied still has to be deployed, launched and admitted. Nothing in T2.2's own text is contradicted by the chain — its subset check reads the block BW-T2.1 amended. | |
| **D1981** T2.9 (the runner enforces the block on a shared job) — replaced by the service, or kept as a second gate? | **Replaced, and the number closes with T2.5.** The check is not in the tree, and it should not be: `internal/gitproxy/rules.go`'s `Decide` and `CheckPRWrite` decide the same question where the agent cannot reach it, and a second gate in the job would be a gate the agent bypasses by not using the runner's git path at all (F5). | |
| **D1982** T2.4 (doctor warns that a shared job ignores a repository's own token) — keep it for S5, or drop it as moot? | **Keep it for S5.** Its subject is a shared job's use of the project token, which B5.1 changes and the proxy does not. The one sentence it needs and does not have is that the warning is about shared jobs only: on a proxied per-repository job the run spends no token of the job's, so there is none to be shadowed. | |
| **D1983** Three corrections in `docs/git-providers.md` are owed by shipped code rather than by S5: the `FUGARO_BITBUCKET_API_URL` variables-table row does not name `bitbucket_api_roots`; the section says the root lock "is 0.13.0's (D474, T2.8)" when BW-T2.8 shipped it in 0.14.0; and "Bitbucket stays on the per-repository path" says each Bitbucket repository "keeps its own job and its own `bitbucket-token`", which the flip (B1.13 part two) makes false. Land them now, or with S5? | **Land them now, as one docs-only change.** The first two describe what 0.14.0 already does, and the row is what an operator reads when they set the variable; the third is a dated amendment to a 2026-10-15 owner ruling that the chain has overtaken, and a ruling a reader cannot tell has moved is worse than a correction. The rest of T2.6 still waits for S5. | |
| **D1984** T2.11's step 5 — an installation that sets `FUGARO_BITBUCKET_API_URL` lists that root in `bitbucket_api_roots` and republishes before upgrading its runners — is absent from `docs/secrets-brokering/release-0.14.0-operators.md`, which shipped, and from `docs/releases/` entirely. Add it now as a dated amendment, or carry it in the next release's notes? | **Add it now, as a dated amendment to the shipped draft.** The pin is live in every runner built from 0.14.0 on (`internal/runner/runner.go`'s `checkBitbucketAPIRoot`, unconditional for a Bitbucket provider outside a local invocation), so an installation with a non-default root that refreshed its images has runs refused today, and the note that would have told it why never shipped. A dated note is how this repository amends a shipped release record. | |

## 4. Numbers this work holds

| Block | Use |
| --- | --- |
| **D460–D489** | Unchanged: **D460–D477** written (all ruled 2026-10-27; D469–D472 are resolutions); **D478–D489** reserved and unused. |
| **Checks 180–189** | Unchanged: **180** is written and registered (T1.3); **181** is proposed and waits for T2.6 and S5; **182–189** reserved and unused. |
| **D1980–D1999** | **This reconciliation's block**: D1980–D1984 are written above, D1985–D1999 reserved and unused. It sits above D1959, the highest decision on `main`, and above the credentials folder's D1960–D1979. |

## 5. UNVERIFIED

- **Nothing here was checked live.** Every row is a reading of `origin/main`; no run was launched,
  no installation was touched and no credential was read. Checks 180 and 181 stay the owner's, and
  neither has been run.
- **The flip is not done** (`ProxiedGitSecretNames` is empty), so a proxied job keeps its mount and
  `docs/git-providers.md`'s statements about the job's mount are still literally true. The **proxy**
  is not held back by that: it is in the released 0.22.0, so what this document says about a proxied
  run is a reading of code a released installation runs — a node that publishes `proxy: true`
  proxies its runs today.
- **B5.1 is not started and unscheduled**, so every "waiting on B5.1" above is a wait on the
  owner's own scope, not on a task in flight.
- **`../secrets-brokering/status-2026-11-07.md` is stale** on B1.7, B1.10 and B1.11, which have all
  merged since it was read; it was not used as evidence here.
