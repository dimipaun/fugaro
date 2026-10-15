# Project-centric Fugaro: implementation plan

**Goal:** make the **project**, not the repository, the unit of configuration, policy, credentials and setup — the reconciled design [docs/design/project-centric.md](../design/project-centric.md) (§11 phases 0 to 6), implemented so that **each phase merges alone, leaves `main` green and the system working**, and adds a repository to a project with no Terraform and no confirmations. No code in this plan has been written or run; this is a design-to-code plan, the same status `docs/plans/2026-10-14-*` had before its Task 1 started. **This PR (the one that adds this plan) is docs-only: no Go, Terraform or product-behavior file under this plan's "Files" lists is touched by it.** Unlike the price-catalog and project-secrets plans it edits **nothing else at all** — not even `docs/gcp-live-checklist.md`: the check numbers this plan reserves (48 to 56) are *named* here and their rows and sections are written by each phase's own check task when that phase ships, because a reservation without a written check is exactly the "Reserved number" row `main` still carries for Check 42 and the design says nothing about those numbers. Read §"Check numbers this plan takes" for why nothing below 48 is free. Tasks 0.1 onward are future work, gated on the owner approving the design.

**Spec:** [docs/design/project-centric.md](../design/project-centric.md) (approved 2026-10-14; the owner's decisions D1a to D12 are in its §12, its §12a lists the fourteen review items this plan must carry, and its §13 lists what was UNVERIFIED when it was written) and the owner's verbatim draft [project-centric-spec-source.md](../design/project-centric-spec-source.md). The design's decisions are settled; this plan's own decisions continue as **P1 onward**, each independently vetoable before its phase starts.

**Architecture — what the seven phases build, in one paragraph.** Phase 0 is three small fixes with no shared surface: the budget refusals stop naming flags that do not exist, `anthropic-api-key` stops being mandatory, and the global per-run cap becomes the fallback the RTDB rules already deny for. Phase 1 moves the ceiling out of each job's environment into **one RTDB policy object** (`config/policy`), read by the runner at start, unreadable-by-wrong-slug by rule, and fail-closed: "project-centric mode" is *the rules-version marker `config/limits/policyRules` being at least `RulesVersion` **and** `config/policy/shared` existing* (R3; P3), so an installation that has not published one behaves exactly as today. Phase 1 also ships the enrollment allow-list (`allow_repos`) that a shared job needs from its first day, and the CLI that publishes and shows the policy. Phase 1b decouples the model from the key route with the operator-written `routing.claude` switch and the canonical-to-route ID map, and ships only after #359's phase 2 and the price catalog are merged. Phase 2 makes project scope the default for `fugaro secrets set` (D2, which reverses #343's default-safe rule). Phase 3 replaces the per-(repository, workflow, account) job with **one job and one job service account per (profile, provider, account)**, and with it the runner binds its slug to the repository it clones, admits only repositories its profile allows, and takes its remote from `task.json` and the policy instead of `FUGARO_REMOTE`. Phase 4 turns enrollment into data (`project sync`, `project repos ls\|add\|rm`), retires a repository's whole Terraform root, retires `init --repo` for clone repositories, and drops baked workflows (D3). Phase 5 makes Claude OAuth tokens person-owned and shared by reference, with the CLI (not the skill) doing the failover. Phase 6 writes the project's policy, config, inventory and spend into a backing Git repository, secrets excluded. Nothing in phase *n* is required by phase *n-1*'s merge: each phase is a working system, and the last section of this plan lists every cross-task dependency so a reviewer can check that claim.

**Builds on merged code (what each phase stands on).** Verified on `main` as of this plan's writing (`fd86542`, and the open PRs named in §"Check numbers"):

| Phase | Merged work it builds on |
|---|---|
| 0 | `internal/budget` (M9b/evaluate/session), `internal/budget/rules` (rules template, emulator, `TestRulesParity`, `internal/budget/rules/parity_test.go:175`), `internal/infra/spec.go`'s secret mounts, `internal/gateway`, `internal/agent/env.go`, `docs/design/m9-budget-and-dashboard.md` |
| 1 | `internal/rtdb` (the client `fugaro budget set` already writes caps with), `internal/policy` (the layer merge `resolvePolicy` computes), #343's `ResolvedSecretID`/`ProjectSecretCandidates` (project secrets, merged 47a73b9), the golden-plan generator. **#372** (open, `next`) already implements Tasks 1.1 and 1.2 from an earlier draft — **without the rules marker** (R2-7), so a follow-up lands it |
| 1b | **#359's phase 2** (the `anthropic/<id>` claim and `IsAlias(model, claimed)`), **#361/the price catalog** (`require_zdr`, `price_policy`, and the catalog rows the canonical-ID map needs), `internal/runner/agentenv.go`'s `stagePins` |
| 2 | **#343, merged** (project-scope secret containers, `--project-wide`, the repo-wins-else-project precedence in `internal/infra/readiness.go`) |
| 3 | **#342, merged** (`checkout: clone` and the runner's blobless-clone bootstrap), **#360/Task 18 and its owner-run Check 38** (the critical path: `cloneShipped` flips only after Check 38), `image refresh`'s API-driven job patch (`internal/cli/image_refresh_steps.go:226-261`), `internal/backend/gcp`'s `JobName`/`BucketCondition`/`Launch`, `internal/gcpfake` (the fake that must learn project jobs) |
| 4 | **the full config-transitions plan** (`docs/plans/2026-10-14-config-transitions.md`, Tasks 1 to 9), which ships on `main` in **0.7.5** (its core is PR #365, in flight); **D8 is superseded** (R2) — phase 4 builds on the whole engine, not on a core subset, and `init --repo --retire` is that plan's own flag (so Task 4.2 picks a different one, finding 18). #359's lesson that per-repository variant jobs multiply |
| 5 | `internal/cli/accounts.go`, `agents/` and the OAuth variant machinery, `runstore/launch.go`'s `LaunchedBy`, the skill's failover prose (`plugin/skills/working/reference/diagnose.md:79-97`); **#363 merged on `main` as a held plan** (D9; Check 47 is written, `docs/gcp-live-checklist.md:1685`) and is rebased onto this phase in Task 5.4 |
| 6 | `internal/report`'s `Build` (`internal/report/report.go:159`, called from `internal/cli/report.go:170,393`), `internal/watch`'s JSON feed (`internal/watch/json.go:148`), the history job and its 15-minute sweep (`deploy/terraform/gcp/modules/installation/history.tf:174-253`) |

**Releases (R4, owner ruling 2026-10-15).** The next release on `main` is **0.7.5** (it carries the config-transitions work, R2, and the rename of `cloneSince` from `0.7.1` to `0.7.5`, #370 — already merged on `main`). The redesign ships as **0.8.0 on the `next` branch**: **phase 0 lands on `main`** (its three fixes are independent and D6's rules deploy is the owner's own step there), and **phases 1 to 6 land on `next`**, released together as 0.8.0. Every task below that names a release — the `cloneSince` value, the `policySince` launch gate (finding 16), the `--retire` spelling (finding 18) — is written against that: 0.7.5 is the last release on `main` before the model, and 0.8.0 is the release of the model.

## Phase summary

| Phase | What ships | Owner-run steps | Depends on |
|---|---|---|---|
| **0. Small fixes** | the four wrong flag names; `anthropic-api-key` optional end to end; D6: the global per-run cap is the fallback, in the rules template, the emulator, the parity test and `internal/budget/model.go` | **the RTDB rules deploy** (Check 48), plus the live check | nothing |
| **1. Policy as data** | `config/policy` (per-child read rules, emulator + parity tests), the rules-version marker, the fail-closed runner, `allow_repos` + `project repos add`, `project policy publish` / `project show`, `FUGARO_RTDB_URL` and the API key written whenever Firebase is configured, the policy version and its skew messages | **the RTDB rules deploy** (Check 49), then a sandbox publish and run | phase 0 (its rules deploy lands first) |
| **1b. Routing switch** | `routing.claude` (explicit ID list, D1a; absent = `direct`, D1b), the canonical-to-route ID map, `validate`'s offline resolution line | Check 50: a routed Claude senior and whether its cache markers survive | **#359 phase 2 + #361 merged**; phase 1's policy delivery |
| **2. Project-scope keys** | `secrets set` defaults to project scope (D2), mounted on the repository's job until phase 3, `allow_data_to` unchanged as the data-policy gate | secret entry by the owner; Check 51 | phase 1 (enrolled repositories are what "every enrolled repository" means) |
| **3. Shared project jobs** | profiles (each pinning one provider); one job + one job SA per (profile, provider, account); the runner's slug binding, remote-from-task, allow-list and profile admission; one bucket condition per prefix; the D13 token exposure documented; every job-naming caller; `cloneShipped` flipped after Check 38; **3b: the token channel designed** | **Check 52 first** (the four UNVERIFIED probes: the Bitbucket token, the env-override secret reference, execution overrides, Secret Manager IAM conditions), Terraform applies, then Check 53 | **#342 + #360/Check 38**; phase 1's allow-list; phase 2's project secrets |
| **4. Enrollment and migration** | `project repos ls/add/rm`/`sync`; retire a repository root; `init --repo` retired for clone repositories; baked workflows dropped for new onboarding (D3) | the migration of installation `fugaro` (never `belong`), Check 54 | phase 3 (project jobs are what a repository moves onto); **the full config-transitions plan, on `main` in 0.7.5** (R2) |
| **5. OAuth pool** | person-owned secrets per (person, account), references in the policy, `accounts share`, CLI failover | the owner stores their own token; Check 55 | phase 3 (a shared account is a job per profile) |
| **6. Backing repository** | `fugaro project export [--push]`, secrets excluded, people omitted by default | the owner's own push target; Check 56 | phases 1 and 3 (what there is to export) |

**Nothing in this table depends on a later row.** Phase 1b may be deferred indefinitely (its two prerequisites are other PRs); phases 2, 3, 4 and 5 do not read the routing switch; phase 6 reads only the policy and the bucket. The last section lists the per-task dependencies.

## Check numbers this plan takes (48 to 56)

`docs/gcp-live-checklist.md` on `main` (as merged by #353 and #354) says *"Check 45 is the next free number"* (line 242). The **open PRs change that**, so this plan takes nothing below 48 and says whose each number is:

| Number | Claimed by | State |
|---|---|---|
| 42 | config-transitions design §6 and plan Task 8, merged as #344 | reserved row on `main`; the full plan (and its core, #365) implements it |
| 43 (43a-43f) | registry remote repository, merged as #353 | written on `main`, not run |
| 44 | provider price catalog, merged as #354 (plan Task 7) | written on `main`, not run |
| 45, 46 | **open PR #359** (senior reviewer, Phase 1 and Phase 2), whose checklist patch adds both rows and sections | not merged — the claim is readable only in that PR, which is why this plan does not take them |
| 47 | OAuth reviewer stage, merged as **#363** (its Task 8) | **merged as a held plan, written on `main`** (`docs/gcp-live-checklist.md:1685`), not run |
| **48** | **this plan, phase 0**: the rules deploy and what the global per-run fallback changes | reserved here |
| **49** | **this plan, phase 1**: policy as data, end to end | reserved here |
| **50** | **this plan, phase 1b**: the routed Claude senior and its cache markers | reserved here |
| **51** | **this plan, phase 2**: project scope by default (D2) | reserved here |
| **52** | **this plan, phase 3, first**: the four UNVERIFIED probes, a go/no-go | reserved here |
| **53** | **this plan, phase 3**: the shared project job round trip | reserved here |
| **54** | **this plan, phase 4**: enrollment, retirement, migration of installation `fugaro` | reserved here |
| **55** | **this plan, phase 5**: the OAuth pool | reserved here |
| **56** | **this plan, phase 6**: the export | reserved here |
| 57 | **PR #375**, the compute-instance backend (the generic-tool Task 28 design, in progress) | not merged; **that work is separate from this plan** (R5): it is a different execution backend, it claims 57, and nothing here shares a job, a name or a Terraform module with it |

**Checked, not assumed (R5):** `main`'s checklist ends at 47; #359's patch claims 45 and 46; #375 claims 57; and **#360, #361, #362, #365, #369, #371, #372 and #373 add no number at all** — so **48 to 56 are free and stay this plan's**. **#374** (the compute-instance backend's own plan, generic-tool Task 28) is separate for the same reason 57 is: one line here so a reader does not confuse its "project job" with phase 3's.

**Each check's row and section is written by the phase's own task, not by this PR** — that is the one place this plan differs from its predecessors' habit of reserving a row up front. The reason is #359: its numbers live only in an unmerged PR, and a reservation this plan wrote into the checklist would have to be amended by whichever of the two merges second. Taking 48 as the next free number and writing each row with its phase keeps one writer per row. **All nine are owner-run, on installation `fugaro` and the sandbox repository only, never the `belong` project** (which holds `edgeappinc/edgeserver` and `edgeappinc/edgeweb`: no agent touches it, ever; migrating it is the owner's own work after the model is proven on `fugaro`, D2 and D3). Every check is written with FACT lines, a restore step **and Check 43's full guard, spelled once in Task 0.4 step 1 and required verbatim in every check 49 to 56** (`docs/gcp-live-checklist.md:1158-1192` on `main`: the three hard stops, `--project fugaro` on every `fugaro` command and `--project "${P:?}"` on every `gcloud` command, re-run from a fresh shell before each step — R2-6).

## Citation drift: where the design's `file:line` are now

The design's citations are `file:line` on `main` at `b744aeca`, and it warns they drift. **Re-verified one by one for this plan, on `fd86542`.** Every task below cites the *current* location; this table is what a later reader uses to read the design's prose against the code. Two systematic differences first:

- **The design quotes bare filenames** (`spec.go`, `names.go`, `evaluate.go`, `run-gen.go`, `task.go`, `readiness.go`). Their packages are not always the ones a reader guesses: the job spec builder is **`internal/infra/spec.go`**, not `internal/cli/spec.go`; the four wrong flag names are in **`internal/budget/{evaluate,session}.go`**, not `internal/cli/`; `task.json`'s type is **`internal/task/task.go`**; the readiness gate is **`internal/infra/readiness.go`**; the per-repository secret ID is **`internal/backend/gcp/names.go`**. Terraform paths resolve under **`deploy/terraform/gcp/`** (`modules/repo/...`, `modules/workflow/...`, `modules/installation/...`, `modules/firebase/...`).
- **`internal/cli/run-gen.go` does not exist** on this branch and neither does a `run.jobs.runWithOverrides` merge function by that name; the override is the request body `internal/backend/gcp/run.go:106-115` builds, and the API's merge behaviour is the design's citation of `run-gen.go:769-778` (generated client code, not in this repository). **This is the one citation the plan could not re-verify from the repository** and is why the D10 live check (Task 3.0 step 3) is a *probe*: what the API does with a `ContainerOverride` that repeats a job's own env var is read from the API's behaviour, not from a file here.

| Design citation (at `b744aeca`) | Now, on `fd86542` | Note |
|---|---|---|
| `workdir.go:19-23`, `repo.go:38-40` | `internal/infra/workdir.go:19-23`, `internal/infra/repo.go:38-40` | unchanged |
| `spec.go:435-441` (`infra.repoURL`) | `internal/infra/spec.go:454-481` | the function grew; the remote is computed at deploy time |
| `spec.go:650` (`FUGARO_REMOTE`) | `internal/infra/spec.go:669` | inside the `checkout: clone` branch |
| `spec.go:112-113`, `:656-657` (GitHub App ID) | `internal/infra/spec.go:112-113`, `:675-677` | unchanged shape |
| `spec.go:571-572` (the Anthropic mount "unconditionally") | `internal/infra/spec.go:585-591` | **not unconditional**: it is the `case "api-key"` branch of `SecretMounts`; the mounted secret is `anthropic-api-key` at `:591` |
| `spec.go:659-673`, `713-752` (`budgetEnv`) | `internal/infra/spec.go:732-771` | caps/tokens at `:734-739`, prices at `:744-754` |
| `spec.go:736-750` (`FUGARO_RTDB_URL` only when the mode is not off) | `internal/infra/spec.go:755-768` | unchanged behaviour: the mode is read from the operator's local config |
| `spec.go:800-821` (one variant per (workflow, account)) | `internal/infra/spec.go:818-840` | `gcp.WorkflowVariant(name, token)` at `:836` |
| `spec.go:822-829` (clone repositories need no check) | `internal/infra/spec.go:846` | the `Checkout != config.CheckoutClone` gate |
| `readiness.go:100-107` | `internal/infra/readiness.go:100-158` | package moved `internal/runner` → `internal/infra`; the secret-version gate is general, not Anthropic-specific |
| `runner.go:789-794`, `797-803`, `586` | `internal/runner/runner.go:789-794`, `797-803`, `586` | unchanged: the clone, the task read, the token minted for `spec.Repo` |
| `provision.go:30-44` | `internal/runner/provision.go:30-44` | `RemoteEnv = "FUGARO_REMOTE"` |
| `exec.go:67`, `:157`, `:168` | `internal/cli/exec.go:67`, `:157`, `:168` | unchanged; `runstore.ParseRef` is `internal/runstore/runstore.go:409` |
| `lockcache.go:76`, `:188` | `internal/runner/lockcache.go:76`, `:188` | unchanged |
| `budget_session.go:128`, `:180`, `:27-52` | `internal/runner/budget_session.go:128`, `:180`, `:25-52` | env consts start at `:25` |
| `project.go:20-43` | `internal/runner/project.go:20-43` | unchanged: `project:` enforcement |
| `gateway.go:74-76` (Anthropic key demand) | `internal/runner/gateway.go:74-76` | the `api-key` upstream takes `ANTHROPIC_API_KEY` |
| `gateway.go:322-327` (the no-cap halt) | `internal/runner/gateway.go:322-327`; the **message** is `internal/runner/gateway.go:429-433` | the quoted text ("run `fugaro init --repo`") is `noCapDetail` |
| `gateway/gateway.go:256-259`, `route.go:95-97`, `:142-151` | `internal/gateway/gateway.go:255-259`, `internal/gateway/route.go:92-96`, `:142-151` | unchanged |
| `agent/env.go:97-103` | `internal/agent/env.go:91-104` | the ≥4-byte redaction demand |
| `agentenv.go:54-57`, `:54-77` | `internal/runner/agentenv.go:54-57`, `:54-77` | `stagePins` returns nil when the gateway is off |
| `localcfg.go:78-84`, `:96`, `:378-395` | `internal/localcfg/localcfg.go:78-84`, `:96`, `:385-397` | the `repos:` map field itself is `:104` |
| `sharedcfg.go:146-155` | `internal/cli/sharedcfg.go:146-155` | `sharedForbidden` |
| `providers.go:92-99`, `203-219` (Claude forced direct) | `internal/runner/providers.go:92-101`; **`ClaudePatterns` is `internal/config/providers.go:331-344`** | the design's second citation is the wrong file on this branch |
| `pricing/alias.go:30-43` | `internal/pricing/alias.go:30-44` | `IsAlias` |
| `claude_accounts.go:63-85` | `internal/cli/accounts.go:60-94` | the file was renamed |
| `evaluate.go:190`, `:247`; `session.go:501`, `:511` | `internal/budget/evaluate.go:190`, `:247`; `internal/budget/session.go:501`, `:511` | **present and still wrong** (`--repo-daily`, `--repo-per-run`); the real flags are `internal/cli/budget.go:687-691` |
| `upgrade_cloud.go:162-174` | `internal/cli/upgrade_cloud.go:162-177` | `jobConfigCopyNote`: drift reported, never fixed |
| `image_refresh_steps.go:226-260` | `internal/cli/image_refresh_steps.go:226-261` | `refreshCloneJobs`: job patched through the API |
| `run.go:92-101`, `:107` | **the CLI caller of `CheckRunExecution` is `internal/cli/cloud.go:285`**; `internal/cli/run.go:89-102` is `launchHooks`, a different function (finding 20). `Launch` is `internal/backend/gcp/run.go:106`, `CheckRunExecution` is `:92-103`, and the execution's own env read is `:425-430` | the design's `run.go:107` is the CLI's own wiring |
| `run.go:222` (`max_parallel` counts `fugaro-` jobs) | `internal/cli/run.go:1084-1112` | `checkMaxParallel`/`checkMaxParallelFor` |
| `run.go:385-390`, `:389-390` (the execution's own env) | **`internal/backend/gcp/run.go:420-430`** | `ev.Name == "FUGARO_RUN"` read from the execution's template; `FUGARO_RUN` is *written* as a `ContainerOverride` at `internal/backend/gcp/run.go:110` |
| `logs.go:193-194` | `internal/backend/gcp/logs.go` (job/execution names); the CLI reader is `internal/cli/logs.go` | the design's range is a different function now |
| `names.go:140-145`, `:363` | `internal/backend/gcp/names.go:143-158` (`SecretID`, `ProjectSecretID`), `:374` (`JobBucketPrefixes`) | `:383` `BucketCondition`, `:92`/`:106` in `run.go` |
| `run-gen.go:769-778` | **not in this repository** | the Cloud Run v2 override merge is the API's; see Task 3.0 step 3 |
| `launch.go:12-29` ("requestedBy") | `internal/runstore/launch.go:12-21` (`Launch`, field `LaunchedBy` at `:19`), `:24-29` (`WriteLaunch`) | there is no `requestedBy` symbol; `RequestedBy` is `internal/task/task.go:57` |
| `task.go:47-70` (no provider in `task.json`) | `internal/task/task.go:45-79` | confirmed: no provider field |
| `budget/model.go:149-167`, `188-226`, `210-215` | `internal/budget/model.go:149-167`, `:188-226`; **`PerRun` is `:209-224`** | and its comment still states the old rule ("ok is false when either side is unset") — Task 0.3 rewrites it |
| `rules.go:310-311`, `:341-345` | `internal/budget/rules/rules.go:310-314` (`REPOPERRUN`, `GLOBALPERRUN`), `:343-346` (`fallback`) | the package is `internal/budget/rules`, not `internal/budget` |
| `rules.json.tmpl:3-21`, `:28` | `internal/budget/rules/rules.json.tmpl:3-21`, `:28` | the `reserved` validator is `:28` |
| `config/task.go` | `internal/task/task.go` | |
| `config/providers.go:56`, `:68` (`PatternMatches`, `Matches`) | `internal/config/providers.go:185` (`PatternMatches`), `:118` (`Matches`) | the design's `:56`/`:68` are other fields |
| `config/providers.go:158`, `205-220`, `212-220` | `internal/config/providers.go:164` (`OpenToEveryRepoReason`), `:205-216`, `:212-220`, and `ClaudePatterns` at `:331-344` | |
| `config/pins.go:27`, `:29-32` | `internal/config/pins.go:27-32` | inside `CheckPins` (`:15`) |
| `config/config.go:115` | `internal/config/config.go:115` | `PRSettings.EarlyDraftOn` |
| `runner/policy.go:63-107`, `:108-113`, `:112` | `internal/runner/policy.go:63-120`, `:108-113`, `:112` | `resolvePolicy` treats the ceiling as optional — confirmed, and that is what Task 1.3 changes |
| `runner/spend.go:122` | `internal/runner/spend.go:122` | `pricing.Embedded().With(o)` |
| `pricing/...` (`table.go:64,79-87`, `pricing.go:36-41,240,252-266`, `overrides.go:20-25,51`) | unchanged | as the price-catalog plan records |
| `cli/budget.go:41`, `:1193`; `cli/validate.go:133`, `:168`; `cli/diagnose.go:273`, `:277` | unchanged | the pricing surfaces the catalog plan also touches |
| `recipes_skew.go:43`, `:45-67` | `internal/cli/recipes_skew.go:44-70` | **`cloneSince = "0.7.5"`** at `:44` (renamed by #370, merged on `main`), `cloneShipped = false` at `:70`. Task 3.6 flips `cloneShipped` **only if the release process has not already done it** (finding 21: `docs/release.md` ties the flip to the commit that bumps `cloneSince`) |
| `init_fugaroyaml.go:133` | `internal/cli/init_fugaroyaml.go:133` | `imagePredates` |
| `doctor.go:39-45` | `internal/cli/doctor.go:39-46` | a `warning` fails only with `--strict` |
| `m9-budget-and-dashboard.md:271` (the global per-run cap "is the fallback") | `docs/design/m9-budget-and-dashboard.md:272` | one line off; the document is **wrong about the code** and D6 changes the code so it becomes true |
| `m9-budget-and-dashboard.md:315`, `:582` | unchanged | the history job reads `FUGARO_MODEL_PRICES`; the budget token is not an env override |
| `named-oauth-tokens.md:15`, U4 | unchanged | **U4 is the expired/revoked-token text**, not the usage-limit text (that is §12) — Task 5.3 says which is which |
| `checkout.md:81` (the inert registry) | `docs/checkout.md:81` | unchanged |
| `SECURITY.md` (the 0.7.0 property, the mode default, one App per repository) | `SECURITY.md:36`, `:34`, `:48` | phases 1, 2, 3 and 5 amend it |
| `installation/iam.tf:26-31`, `:15-19`; `firebase/iam.tf:43-49`; `modules/workflow/job.tf:93-101`; `modules/repo/{secrets,registry,build,check}.tf`; `history.tf:160-169`, `:174-260` | `deploy/terraform/gcp/modules/installation/iam.tf:26-31`, `:15-19`; `.../modules/firebase/iam.tf:43-49`; `.../modules/workflow/job.tf:93-101`; `.../modules/repo/*.tf`; `.../modules/installation/history.tf:163-169`, `:174-253` | the last range's `:260` is past the end of the file (253 lines) |
| `watch/json.go:97-115` | `internal/watch/json.go:148` (`BuildJSON`) | |
| `project-centric.md` §12a item 1 ("`CLOUD_RUN_JOB`, which the runner already reads") | **the runner reads it through `backend.ExecutionFromEnv`** (`internal/backend/backend.go:96-104`, wired at `internal/cli/exec.go:134`); there is no direct `CLOUD_RUN_JOB` read in `internal/runner` | Task 3.3 makes the derivation explicit rather than assuming it |
| `project-centric.md` §4.1 "`fugaro project ...`" commands | **no `fugaro project` command group exists** | `internal/cli/project.go` is project *selection*, used by every cloud command; Task 1.6 adds `newProjectCmd()` and registers it in `internal/cli/root.go:75-78` |

**If the merged code differs when a task starts, that task's implementer adapts its edits to the then-current file, not the other way round** (the rule `docs/plans/2026-10-08-upgrade.md` and `docs/plans/2026-10-13-project-secrets.md` use). A task that finds a cited symbol gone stops and reports rather than inventing a replacement.

## Decisions (veto any before its phase starts)

- **P1. The D6 rules change ships alone, in phase 0, and the rules, the code, the tests and the m9 document change in one commit.** Making the global per-run cap the fallback is a **fail-open-direction change to security rules** (design §2.5): shipped as one commit it is reviewable as one thing, and shipped as two it leaves a window in which the code enforces a fallback the rules deny (every write refused) or the rules allow one the code does not compute. *Veto alternative:* fold it into phase 1's policy work — rejected: it would make phase 1's rules deploy carry two unrelated changes, and the design's own phase 0 names it.
- **P2. The policy object is one RTDB subtree, `config/policy`, with two readable children — `shared` and `repos/$slug` — and one node outside it, `config/admin`, which no run token may read.** The types, the paths and the version live in one new package, `internal/projectpolicy`; `internal/budget` keeps its own `Path*` helpers (`internal/budget/model.go:44-80`) and neither package imports the other. `internal/projectpolicy` imports `internal/rtdb` and `internal/task` only (Task 1.1's own header says the same — finding 21: the two sentences disagreed), and never `internal/budget`, `internal/runner` or `internal/cli`. The rule is per child because **RTDB read grants cascade to children** (design §5): a `TOKEN` read on `config/policy` itself would make every child readable by every run token, which is the narrowing the design exists to keep. **`config/admin`, not `config/policy/admin`**: the design's §5 wording is "an admin-only `config/admin`", and 12a item 7 adds the reason not to put anything secret-adjacent there — launchers hold the RTDB *viewer* role and IAM reads bypass the rules, so launchers read it. *Veto alternative:* one flat `config/policy` node readable by run tokens — rejected outright: a prompt-injected agent can take the run token through `/proc` and would then read the project's whole policy, including who owns which account.
- **P3. "Project-centric mode" is the rules-version marker `config/limits/policyRules` being at least `RulesVersion` **and** `config/policy/shared` existing (owner ruling R3, 2026-10-15), and an unreadable-or-unparseable existing node halts the run.** The marker is the first half for a reason the round-2 review found (R2-4): a policy published without the rules deployed cannot exist — `policy publish` refuses without the marker (Task 1.6) — and the marker is readable under both the pre-phase-1 and the phase-1 rules, so "marker absent" is provably "this installation's rules predate the model", never "a policy is hiding behind a denied read". Not a flag in the job env (a launcher can clear it, D10), not a version probe on the parent (denied under per-child rules). **The pointer to the node is not protected either** (finding 27): a launcher that clears `FUGARO_RTDB_URL`, or points it at another database, sends the run down the env-ceiling path — D10-accepted, and SECURITY.md says so in Task 1.5 step 5, so no reader of P3 should conclude the mode cannot be turned off by a launcher. Absent ⇒ today's behaviour exactly (the env ceiling, the per-repository path): an installation that has not published a policy cannot be changed by this phase, which is what makes phase 1 merge alone. **The halt applies to a run that has the budget credential** (round-2 review): without `FUGARO_RTDB_URL`/`FUGARO_FIREBASE_API_KEY` there is no RTDB client and the marker cannot be read at all, so that case is the mode-off path and the launcher's readiness gate is the refusal (Task 1.3 step 1, Task 1.4 step 2). *Veto alternative:* a `FUGARO_POLICY_URL`/`FUGARO_POLICY=1` env flag — rejected: it is exactly the override surface D10 documents, and it would let one launcher override turn the project policy off for one run.
- **P4. `allow_repos` is seeded once, from the local config's `repos:` map, and a later publish does not silently re-add a name an operator removed (12a item 4).** `project policy publish` seeds **only** when the node does not exist yet, or when the operator passes `--seed-repos`; the seeded source is **the operator's own `repos:` map and the shared config's repository list** — never the bucket's `runs/<slug>/` prefixes, because **a slug cannot be turned back into a name** (finding 19): it is a readable prefix plus a truncated hash of `(provider, canonical repo)` (`internal/task/task.go:183-196`), and `task.json` carries no provider (`internal/task/task.go:45-79`). Recovering a canonical name from a prefix means reading that slug's `task.json` repository and testing `task.Slug(kind, repo)` against the slug for each known provider kind — which is exactly what Task 4.1's *seen* list does, under a different question ("what has run", not "what to enroll"). Seeding from it would enroll a repository the operator never named, which is the thing this decision exists to prevent. A name removed from the allow-list stays removed until an operator adds it, and `project show` prints the difference between the allow-list and the seen repositories. *Veto alternative:* seed on every publish — rejected: it makes `fugaro project remove-repo` impossible to keep, which is a security-relevant action (it is how a repository is un-enrolled).
- **P5. `allow_repos` patterns are canonical `provider/owner/name` globs (`github/acme/*`, `bitbucket/edgeappinc/service-*`, `*/acme/*`), matched against `projectpolicy.Canonical(provider, repo)` — the provider kind plus `task.CanonicalRepo`'s lower-cased `owner/name` (`internal/task/task.go:127-138`), never `task.Slug` (12a item 5).** **The distinction matters and is easy to get wrong**: `task.Slug(provider, repo)` is the *storage* identity, a readable prefix plus a truncated SHA-256 of exactly that (provider, canonical name) pair (`internal/task/task.go:179-196`) — matching globs against it would compare patterns to `acme-app-3f9c…`, which matches nothing and carries no provider segment at all. The two agree because they hash the same pair: the allow-list is checked against the canonical string, and the slug binding of Task 1.5 asserts `slug == task.Slug(provider, spec.Repo)`, so a policy that enrolls `github/acme/app` admits exactly the runs whose slug is that repository's. The pattern's provider segment is required (a bare `acme/*` is refused with a message naming the canonical form) so a GitHub and a Bitbucket repository of the same name cannot be enrolled by one glob. Matching lives in one function, `projectpolicy.MatchRepo(pattern, canonical string) bool`, used by the runner's admission check and by the CLI's advisory check, so the two cannot disagree; `Canonical` itself is the single place the two segments are joined, so no caller can build a third spelling. *Veto alternative:* match the bare `owner/name` — rejected: `dimipaun/fugarosandbox` and `edgeappinc/fugarosandbox` already exist in the owner's installations (design §4.4), so a bare-name allow-list would enroll the wrong one.
- **P6. Phase 1 enforces `allow_repos` only; profile admission ships in phase 3, where profiles arrive (12a item 8).** The runner's phase-1 check is therefore "the slug matches the allow-list and equals `task.Slug(provider, spec.Repo)`", with the provider **derived from the slug and `task.json`'s repository** (`providerForSlug`, Task 1.5 step 2 — the round-1 text said "the job's own provider", which phase 1 does not have: a phase-1 job carries none, R2-12) — the binding's *first half*, which is enforceable before shared jobs exist and is what a shared job will need on its first day. Nothing in phase 1 creates a profile.
- **P7. Phase 1b is separately shippable and separately deferrable; nothing in phases 2 to 6 reads the routing switch.** Its gate is real: #359's phase 2 (the `anthropic/<id>` claim, `IsAlias(model, claimed)`) and the price catalog (#361) must be merged, because the switch is delivered *through* the claim and the canonical-to-route ID map is carried *by* the catalog rows. If both are merged when phase 1 ends, 1b runs next; if not, the plan proceeds to phase 2 and 1b runs whenever they land. This is the only prerequisite in the plan that is another team's PR, and it is stated here so a reviewer does not read the phase order as a hidden dependency.
- **P8. Version skew is handled by one integer, `version`, in `config/policy/shared`, and by three messages, not by a capability table.** The runner refuses a `version` it does not know, naming both numbers and the fix (`fugaro upgrade`, then refresh the job images); `project policy publish` refuses to write a version above the release the binary is, so a beta cannot publish a shape `main` cannot read; `fugaro doctor` reports a published policy newer than the binary and a policy published while the installation's jobs still carry the old image (12a item 6 — an **old baked runner image silently ignores the published policy until refreshed**, which is the trap this message exists for). *Veto alternative:* no version field, tolerate unknown keys — rejected: the design's §11 names version skew as a cross-cutting item, and this is the cheapest mechanism that answers it.
- **P9. A profile is `{provider, cpu, memory, timeout class, secrets, allow_repos}` in the policy — **the provider is required** — and the installation root creates **one job and one job service account per (profile, provider, account)**, which is one job per (profile, account) *within* the single provider the profile pins.** **One provider per profile, settled (R2-11):** a profile names exactly one provider ('github' or 'bitbucket', validated with `task.ValidProvider`, which #372 exports at `internal/task/task.go:169`), so the triple is really (profile, account) with the provider determined by the profile — but the plan writes the **triple everywhere**, because it is the triple that names a job, and "per (profile, account) alone" is what let the two cardinalities drift apart in the earlier draft. A project with repositories on both providers defines two profiles (`standard` for GitHub, `standard-bitbucket` for Bitbucket); the default `standard` names one provider and a GitHub-only project never notices. `workflows.<name>.profile` selects it, defaulting to `standard`. **The provider is a field of the profile, not of the job env** (finding 4, a design amendment): one job cannot serve a GitHub and a Bitbucket repository at once, and the provider cannot come from `$FUGARO_GIT_PROVIDER` because a launcher can override it (12a item 2). It is encoded in the job's own name, which Cloud Run sets — the one identifier a launcher cannot write. A shared account (phase 5) is a new job *per profile* and is created by Terraform through the installation root — **the plan picks Terraform, not the API**, for the phase-3 job set, because the installation root already owns every project-level resource and the `image refresh` precedent (`internal/cli/image_refresh_steps.go:226-261`) is a *patch*, not a create. Task 3.2 says what that costs (one apply per profile added) and Task 5.2 revisits it for OAuth accounts once the phase-3 pattern exists.
- **P10. The export omits people by default and never writes a secret value; the choice is a flag, and the flag's help text says what it publishes.** `fugaro project export` writes no `byPerson` spend and no kill-switch author, and `--with-people` includes them as the operator's own choice ("they are committed to a git repository", design §8). **The only other switch is `--with-admin`, and it is not an exception to "nothing else is optional"** (finding 28): the design's §5/§7 put the owner's identity in `config/admin` **and in the export**, so `--with-admin` is the *content* switch for admin **metadata** (which is always written in some form), while `--with-people` and its default are the *privacy* switch — Task 6.1's layout says which file each lands in and how the two interact. A test asserts that a byte-for-byte scan of the export for every value Secret Manager holds finds none (Task 6.1). *Veto alternative:* hash the emails and always write them — rejected by the design's own reasoning (a hash is reversible against a dictionary of the team's addresses) and because an export that always writes a derivative of a person's address is not the "omitted by default" the design promised.

## Review notes (where this plan disagrees with, or defers, a review finding)

**Read this first, because it is the round-2 review's own blocking finding about these notes (R2-1).** The round-1 notes that stood here said that everything not listed "was confirmed against the code and fixed as the review asked", and that claim was **false**: only the six blocking findings and one later one had been applied, and the round-1 status the round-2 review recorded was **22 findings open and 4 partly resolved**. This revision applies **every one of them**, and the follow-up account on the PR lists the edit behind each. The notes below are therefore only what this plan **does not** adopt as written — one line each, no claim that unlisted work was done.

- **Finding 2, partly.** The launcher's mint decision now follows the policy (Task 1.3 step 2). But the plan keeps the local mode as a **fallback when no policy exists**: an installation that has deliberately turned the budget off (design §5(1)) stays off. That is the design's own promise, not an oversight.
- **Finding 6 / D13.** Superseded by the owner's ruling (2026-10-15): the plan no longer recommends and no longer leaves anything open — see **D13 (ruled)** below. The review's implied preference for per-profile isolation became the *mitigation* the ruling names rather than a separate option to choose.
- **Findings 8, 9, 10 and R2-7 (the D6 shape).** The plan **changes, and #373 does not**: what actually landed is a single `PERRUNOK` boolean macro emitted by `rules.go`'s `perRunOK` that **deletes** `REPOPERRUN` and `GLOBALPERRUN`, so Task 0.3 now describes that rather than prescribing the `||` spelling. The review's reading ("one boolean, because a comparison against an unset side must not deny on its own") is adopted; its proposed spelling (`own.exists() ? own : (defaults.exists() ? defaults : global)`) is **not**, because that is a *cap selection*, while the implementation compares each side that is set — which is the semantics `Caps.PerRun` computes. One of the two had to change before #373 is reviewed against the plan; the plan is the one that changed.
- **Finding 16, partly.** The plan adopts a `policySince` launch gate **and** keeps `doctor`'s checks: the gate is a launch-time refusal, `doctor` is what tells an owner *before* a run fails. The review reads them as alternatives; they are complements, and 12a item 6 asks for the message, not only the gate.
- **Finding 17.** Adopted as a **sequenced, owner-run** migration rather than a flag a repository could set: `init --repo` refuses baked workflows **only for new onboarding**, the owner migrates `belong` after `fugaro`, and the task says the refusal is sequenced and that no agent touches `belong`.
- **Finding 18.** Adopted: Task 4.2 does **not** reuse `init --repo --retire` (that flag is the config-transitions plan's own, R2) — it uses `--offboard` and #365's `Retirements`/`RetireSet`/`Orphans` helpers.
- **Findings 29 and 30 (testability).** Adopted in full: the refusal strings are exported from `internal/budget` so the CLI test can actually fail when one of them names a flag that does not exist, and every check's probe step names its `gcloud` line.
- **Finding 33, naming.** Adopted: `fugaro project repos ls|add|rm|sync`, and **one file** for the group (`internal/cli/project_cmd.go`).
- **R2-8(a) (the no-token case).** Adopted as the review asks — the `policySince` launch gate is what closes it — but the plan **disagrees with one word**: it is not a *bypass* to be refused at the runner (a run with no token object has no RTDB client at all), so the refusal is the launcher's, and SECURITY.md states it as a D10-class launcher choice. That is the same shape P3 already takes for the credential-less run.
- **R2-14 (the Notes' own finding-6 line).** The review is right that it read as if the ruling were still the plan's to make; it is replaced by the D13 ruling above.

Nothing else is deferred: every other round-1 finding (7, 9 partly, 11 to 15, 19 to 28, 30 to 33) and every deferrable of round 2 (R2-8 to R2-16) has a task edit in this revision.

## Design amendments (what the plan changes in `docs/design/project-centric.md`)

The design is the authority for this plan, and **eight** of the plan's fixes are **changes to it**, not implementations of it. They are listed here, exhaustively, so the owner's assistant can apply them to the design in one pass and no implementer has to guess which document wins. The plan follows the amended text below; where a task cites the design, it cites it as amended.

1. **A D13 row in §12's decisions table** (§12, next to D4; Task 3.2 step 2, Task 3b.1). The owner ruled on **2026-10-15**: under the broadened `runs/` grant, **accept** the budget-token exposure, **document it in SECURITY.md beside D4's cache- and lock-poisoning regression**, **design a token channel in phase 3b** (a channel that does not live under a prefix every run can list), and **use separate profiles for repositories that must be kept apart** meanwhile. The design's decisions table gains the row with that date; the design's D4 paragraph gains the sentence that the token path is inside the grant; and §4.1's table gains the row that a profile is the isolation boundary for budget identity too. **The plan no longer treats this as open anywhere** — see **D13 (ruled)** below.
2. **D8 is superseded** (§12 D8, §4.4, §11 phase 4). The design's D8 kept the retirement work to a "core"; **the full config-transitions plan (`docs/plans/2026-10-14-config-transitions.md`, Tasks 1 to 9) ships on `main` in 0.7.5** (its core is PR #365, in flight), so phase 4 builds on the **whole** engine, including that plan's own Task 5 `--retire`. The design's D8 row becomes "superseded: the full plan ships".
3. **§5's definition of project-centric mode** (§5, §5(3); P3). The design defines the mode as the existence of `config/policy/shared`. **It is now: the rules-version marker `config/limits/policyRules` is at least `RulesVersion` **and** `config/policy/shared` exists** (R3), and a run with **no budget credential** takes the mode-off path — the launcher's readiness gate is its refusal, because the run cannot read the marker at all without the credential. The design must say both, or the design and the plan disagree about what the mode is.
4. **A profile carries its provider, and it is required — jobs are per (profile, provider, account)** (§4.1, §4.1(1); Task 3.1 step 2, Task 3.2 step 1). The design says "one job and one job service account per (profile, account)". That is only well-defined once a profile pins **one** provider: the provider is part of the slug (`task.Slug(provider, repo)` hashes the provider and the canonical name, `internal/task/task.go:187`), part of the job's identity and part of the git credential the job holds, so a job cannot serve repositories of two providers. **One provider per profile, and the job set is one job per (profile, provider, account)** (R2-11 settles the cardinality the design's §4.1 leaves ambiguous).
5. **The provider is not a job environment variable** (§4.1(3), §12a item 2; Task 3.2 step 1, Task 3.3 steps 1 and 2). The design's item 1 has the runner derive its profile and account "from `CLOUD_RUN_JOB`, which the runner already reads" — a derivation the plan makes real — and item 2 forbids taking the provider from an overridable env var. Together they mean **the provider must come from the job name too**, and the design's "which the runner already reads" is itself wrong (it reads it through `backend.ExecutionFromEnv`, §12a item 1's own drift note).
6. **Admission and the profile halt apply to project jobs only; a legacy job keeps the phase-1 contract** (§4.1, §4.4; Task 3.3 step 1). The design's phase 3 assumes every repository has moved onto a project job. The plan's phases are ordered so they have not — phase 4 migrates, and D3 keeps baked workflows per-repository until it does — so the runner must tell the two job shapes apart and apply the phase-1 rules to the legacy one. **A project job's name encodes the profile, provider and account** (§4.1; Task 3.2 step 1), and the design names neither the spelling nor how a run recognises one; the plan fixes both (a match against the names **computed** from the policy's set, never a parse).
7. **The rules-version marker** (§5, §11, §12a item 6; Tasks 0.4, 1.1, 1.2, 1.6, 1.7). The design has no rules-version marker and relies on `doctor` alone for version skew, which is a fail-open. The design gains: the marker `config/limits/policyRules`, **written after `PutRules` succeeds** in `fugaro init --firebase`; "a published policy implies the rules are deployed" as a property of `policy publish`; a `policySince` **launch gate** in `fugaro run` for the release skew; and the halt message for an **older CLI's `init --firebase`** that redeploys pre-phase-1 rules while the marker stays.
8. **D3 and the `belong` note** (§12 D3, §4.4, §11 phase 4; Tasks 4.4, 4.5, 2.3, 4.6). The design's D3 drops baked workflows after phase 4. As written that breaks the owner's own production installation on upgrade: `belong` holds `edgeappinc/edgeserver` and `edgeappinc/edgeweb`, both baked, and it is **not** this plan's to migrate. The design says instead: the refusal is **for new onboarding**, the owner migrates `belong` **after** the model is proven on `fugaro`, and **no agent ever touches `belong`**. The design also carries **D2's release note for `belong`** (the `secrets set` default changes there too) — and, with §11, the release plan: **phase 0 on `main` in 0.7.5's line, phases 1 to 6 on `next`, released together as 0.8.0** (R4).

**What the plan does not change, deliberately**: the design's decisions D1a to D12 stand except where the eight amendments above rewrite them, its phases keep their order, and the plan adds no phase (3b is a slot the owner schedules, see below). This plan's own decisions (P1 to P10) are the ones a reviewer vetoes; the amendments above are the ones the *design* needs.

## D13 (ruled 2026-10-15): the budget token under the broadened `runs/` grant

**The owner ruled on 2026-10-15. The ruling is option 1 of the three below, plus a phase 3b task that designs option 3.** It is recorded here in the ruling's own terms, because it overrides what this section said while the decision was open.

**The finding.** Phase 3 (Task 3.2) replaces the per-slug bucket condition with one condition per prefix, because one job service account now serves every repository of a profile (design §4.1). D4 accepts the consequence the design listed: within a profile a run could poison another repository's cache, forge or steal a lock, or rewrite a queued `task.json`. **But `runs/` is not only the queue**: the launcher writes each run's budget token to `runs/<slug>/<run>/budget-token` (`internal/budget/token/object.go:15`), and that token *is* the run's RTDB identity (`token.Claims` carries `fs` — the slug — and `fr` — the run id). A run that can list and read `runs/**` can therefore **take a queued run's token and act as that run in the database**: read `config/policy/repos/<that slug>`, read that slug's caps and kill switches, and write that run's ledger, reserve and spend. That is a different class of harm from cache poisoning — a **budget-identity theft across repositories** — and it is not in D4's accepted list. It also interacts with `CheckRunExecution` (`internal/backend/gcp/run.go:88-91`): the same grant lets a run rewrite another run's `launch.json`/`result.json`, which Task 3.4 and SECURITY.md state, and which is *not* part of the open question.

**The ruling, in full:**

1. **Accept and document.** Phase 3.2 ships the broadened `runs/` grant with the token exposure accepted. **SECURITY.md states it immediately after D4's cache- and lock-poisoning regression**, in these words: within a profile, a run can read and take another *queued* run's budget token and act as that run until the token's own expiry (about an hour, `token.ExpiryFX`), so **profiles are the isolation boundary for budget identity too**, and **repositories that must not share a budget identity use separate profiles**. `docs/design/bucket-iam.md`'s prefix table gains the token path and why it is inside the grant, and the release note repeats it.
2. **Design the channel in phase 3b — Task 3b.1**, a new task in this plan (see below), whose subject is a token channel that **does not live under a prefix every run of a shared job can list**. It is design work with its own security review, not code in phase 3.
3. **Separate profiles meanwhile.** Until 3b lands, the mitigation is the one the paragraph names: two repositories that must not share a budget identity are put on different profiles, because a profile is a job and a job is a service-account boundary.

**What this removes from the plan:** the gate. Task 3.2 **no longer waits for a ruling and no longer describes its step 2 as provisional** — the paragraph above is the final wording SECURITY.md gets. The phase-3 introduction, the audit's 3.2 row, the security-sensitive table and P9 all say "D13 ruled 2026-10-15" instead of "open", and **no task in phases 0 to 6 is held by D13**. Task 3b.1 is scheduled by the owner, not by phase 3: it blocks nothing before it.

## Phase 3b: the budget-token channel (one task, scheduled by the owner)

**Phase 3b is a slot the owner schedules, not part of the 0-to-6 sequence**: it is where a D13 option-3 token channel is designed, and it blocks nothing in phases 0 to 6 (the ruling above is what lets phase 3 ship without it). It is written here so the channel has a home and a task, rather than being a sentence in a decisions table.

### Task 3b.1: the token channel design — SECURITY-SENSITIVE, independent opus review

**Files:** `docs/design/budget-token-channel.md` (new), `SECURITY.md` (a pointer once it lands).

1. **The subject, exactly:** a channel by which a run learns its budget token that is **not** an object under a prefix every run of the same shared job can list. The candidates, each priced: a signed URL minted by the launcher and handed to the run as an override; a Secret Manager secret created by the launcher, read only by the run's own service account (which needs the IAM conditions Task 3.0 step 4 probes — **if that probe failed, this candidate is out and the document says so**); a `tokens/<run-hash>` prefix whose IAM condition names one run; or taking the token off the object store entirely and having the run exchange its own Cloud Run execution identity for an RTDB one.
2. **What it must not do:** put the credential back into a launcher-overridable env var (D10, 12a item 2), or widen any service account's grants beyond the prefix it needs.
3. **Its output is a design document with the options compared, a recommendation, and the migration of the token object out of `runs/`** — and it is reviewed by opus on its own (the security-sensitive flag below), because it changes the same boundary D13 accepted.
4. **What it changes when it lands (recorded now, not designed now):** `internal/cli/launch_budget.go` (the mint), `internal/budget/token/object.go` (the object key and its `TakeObject`), the Terraform in `deploy/terraform/gcp/modules/project/`, the runner's bootstrap and the SECURITY.md paragraph D13 wrote.

**Tests:** none of its own (it is a design document; its *implementation* is a later plan). **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` (docs only). **Depends on:** 3.2 (the grant it narrows) and 3.0 step 4 (the Secret Manager IAM answer, which kills or keeps one candidate). **Commit:** "design: the budget-token channel outside the runs prefix". It is **SECURITY-SENSITIVE — independent opus review**, and the owner schedules it: nothing in this plan waits for it.

## Global Constraints

From the design:
- **A phase merges alone.** Each phase's tasks are complete without any later phase: no task may read a file, node, flag or Terraform output that a later task creates. Where a phase *wants* something a later phase brings (phase 1's profile admission, phase 3's project jobs), it says so and ships the narrower thing (P6).
- **No repository is ever in a state where a shared job serves it without an allow-list match** (design §4.3 "Ordering"): the allow-list and the runner's checks ship in phase 1, and phase 3's shared jobs come after.
- **Project-centric mode requires Firebase and the budget backend** (D11). An installation that wants the budget mode off stays on the per-repository path; that is a permanent second code path and the design accepts it (§5).
- **Launchers stay trusted** (D10, accepted): the runner cannot remove the launcher's ability to override a job's env, and no phase claims to. `FUGARO_RUN` itself is set as a `ContainerOverride` (`internal/backend/gcp/run.go:110`). SECURITY.md states it **in phase 1**, from the phase-1 evidence in **Check 49 step 6** (a live `FUGARO_RUN` override on a real job — finding 14: the paragraph does **not** wait for Task 3.0's phase-3 probe, which was a forward edge and is gone), and Task 3.0 step 3's U2 probe then decides only what the *phase-3* text may add.
- **`fugaro validate` is offline** (design §11): it cannot see the routing resolution or the policy unless it is given a project config that can read them. What it prints when it cannot — "unresolved offline" — is a *requirement*, not a wording choice (Task 1b.3).
- **The RTDB policy replaces only the ceiling; `fugaro.yaml`'s layers still only tighten it** (12a item 13). `internal/runner/policy.go`'s merge (`:90`) is not inverted, and no phase makes a repository able to loosen the project policy.
- **Every owner-run step in this plan is marked `OWNER-RUN` and no agent performs it**: rules deploys, Terraform applies, `fugaro init --repo`, secret entry and every live check. Live checks run on installation `fugaro` and a sandbox repository only, **never the `belong` project**.
- **Every check's shell steps hard-code the project and refuse an empty variable**, and they carry **Check 43's full guard** (`docs/gcp-live-checklist.md:1158-1192` on `main`): three hard stops — `P` is exactly `fugaro-dev` with the variables non-empty, `fugaro doctor --project fugaro` reports that project, and the checkout's origin is `edgeappinc/fugarosandbox` — plus `--project fugaro` on **every** `fugaro` command and `--project "${P:?}"` on **every** `gcloud` command, re-run from a fresh shell before each step. Task 0.4 step 1 spells the block once; every check task 49 to 56 requires it **verbatim**, not "like Check 48" (R2-6, round-1 findings 31 and 30).

Project rules (carried over from this repository's existing plans):
- Every task's own test run is in the foreground, `-count=1`, and `-race` where the package's existing tests use it.
- Fakes only in tests: no live RTDB, GCP or Cloud Run call from a test. The RTDB emulator (`internal/budget/rules/emulator/`) is the fake the rules tests use, and `internal/gcpfake` is the fake the Cloud Run paths use.
- **Security-sensitive tasks are flagged `SECURITY-SENSITIVE — independent opus review`** in their own heading, and are not merged on a self-review: phase 0's D6 rules change (**0.3**), phase 1's rules and policy node (**1.2**), the runner's fail-closed read (**1.3**), **the RTDB URL and API key that decide whether a published policy can be read at all (1.4)**, the slug binding's first half (**1.5**), **`policy publish`'s marker refusal and the allow-list seed (1.6)**, phase 2's project-scope keys (**2.1**), phase 3's shared-job IAM and bucket conditions (**3.2**) and the runner's slug binding, admission and fail-closed checks (**3.3**), **the token-channel design (3b.1)**, phase 5's OAuth pool (**5.1, 5.2**), phase 6's export (**6.1**). R2-13: 1.3's heading, 1.4's absence from this list, and 1.6's and 3b.1's absence were the gaps.
- **Every task has a red step.** Each task states the test (or the command) it expects to **FAIL** before its change and to pass after, as `- [ ] Step N … **Expected: FAIL**`, the shape `docs/plans/2026-10-13-project-secrets.md` uses (finding 32, R2-16). A task whose change cannot be made to fail first is written as one whose *test* can, or it says why not — an implementer is never told only which tests will exist.
- Docs must match behavior: `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` stays green throughout. A docs test that reads the changed text is updated in the same commit as the text.
- `fugaro verify build` / `fugaro verify test` are the only build and test entry points for this repository's own conventions; a task runs them before it is considered done.
- **No task may edit `.github/workflows/`** (a Fugaro-run constraint: GitHub refuses Fugaro's push of workflow changes). A phase that wants a CI change describes it in its PR description instead.

## Review Focus (mutation-proof notes)

1. **The global per-run cap becomes a fallback in the code and not in the rules, or the reverse.** Pinned by `TestPerRunFallsBackToGlobal` (`internal/budget/model_test.go`), by the emulator suite's `TestNullCapDenies` **re-pointed** (it asserts today that "no repository per-run cap" denies; under D6 with a global cap it must **allow**, and a per-run-only variant is the case that still denies — finding 8, and #373 has the Go mirror), and by `TestRulesParity`'s **generator**: `parity_test.go:175` is a randomized Evaluate-against-rules generator, **not** a case table, so the D6 case is added to `genCase` (a missing repository and defaults per-run cap with a global one set) rather than appended to a list (finding 8). The mutation that matters: `fallback()` (`internal/budget/rules/rules.go:341-346`) keeps returning null for a missing repository cap, so the rules deny while `model.go` computes a fallback — a run that halts with a message about a cap the owner did set.
2. **A refusal message names a flag that does not exist.** Pinned by `TestRefusalMessagesNameRealFlags`, which runs every `ReasonNoCap` detail string through the real flag set (`internal/cli/budget.go:687-691`'s flags, as the command registers them) and fails on any `--x` in the text that the command does not define. The current four (`internal/budget/evaluate.go:190`, `:247`, `internal/budget/session.go:501`, `:511`) fail it before Task 0.1 and pass after.
3. **An api-key run is refused for a key nobody needs.** Pinned by `TestApiKeyRunWithoutAnthropicKeyStarts` (`internal/agent`) and `TestGatewayStartsWithoutAnthropicUpstream` (`internal/gateway`), plus `TestReadinessDoesNotDemandTheAnthropicKey` (`internal/infra`): a workflow whose pins all route through a provider must reach the agent with no `anthropic-api-key` version at all. The mutation: leaving `internal/infra/spec.go:591`'s mount unconditional in the *readiness* path (`internal/infra/readiness.go:100-158`) so the deploy is still blocked.
4. **A published policy is silently ignored.** Pinned by `TestRunnerReadsPolicyWhenTheNodeExists` and `TestRunnerUsesEnvCeilingWhenTheNodeIsAbsent` (`internal/runner`), which assert the *effective* per-run cap and the recorded `policy.sources`, not merely that a read happened. The mutation: reading `config/policy` (the parent) instead of `.../shared`, which succeeds under an admin client and fails under a run token's rules — the test runs the runner with a token-scoped fake.
5. **A missing or unparseable policy falls back to an empty ceiling.** The design's §5(3) fail-closed requirement. Pinned by `TestPolicyReadErrorHaltsTheRun` and `TestPolicyParseErrorHaltsTheRun`: the run ends with a message naming the node, and `spend.Mode` is never left at the *less* constrained value. The mutation: treating any read error as "absent".
6. **A repository runs on a job it is not allowed on.** Pinned by `TestRunnerRefusesSlugNotInAllowList` and `TestRunnerRefusesSlugMismatch` (phase 3) — both assert the refusal happens **before** a token is minted or a lease taken, which is checked by asserting the fake saw no token request and no reserve write. The mutation: doing the check after `openProvider` — whose **calls** are `internal/runner/runner.go:838` and `:848`; `:586` is that function's definition (R2-12).
7. **The bucket conditions stop covering what they covered.** Pinned by the golden-plan test (`internal/infra/tf/testdata/golden/`, regenerated by `scripts/gen-golden-plans.sh`) plus a runner-level test that a job service account's condition still names `runs/`, `cache/` and `locks/`. The plan does **not** pretend this is not a regression: the test asserts the broadened condition (one condition on each prefix), and SECURITY.md says so (D4).
8. **A project-wide secret reaches a repository the owner did not opt in.** This is the ruling D2 reverses; what must not break with it is the **data-policy gate**: pinned by `TestAllowDataToStillGatesProviderRoutes` (`internal/gateway`), which asserts a provider route is refused for a repository whose canonical name is not matched by the route's written `allow_data_to`, whatever the key's scope is.
9. **The routing switch becomes a pattern.** D1a: `routing.claude` carries an explicit ID list. Pinned by `TestRoutingClaudeRefusesAGlob` (phase 1b) and by `TestRepoCannotWriteRouting` — the policy's `shared` node is operator-written and a repository's `fugaro.yaml` has no path to it.
10. **The export writes a secret value or a person's address.** Pinned by `TestExportContainsNoSecretValue` (the export is scanned for every value the fake Secret Manager holds) and `TestExportOmitsPeopleByDefault` (no `byPerson`, no kill-switch author without `--with-people`).
11. **The canonical-to-route ID map translates a model nobody pinned, or fails to translate one that was.** Pinned by `TestCanonicalMapRoundTrip` (phase 1b): for each catalog row with a Claude canonical ID, the rendered pin, the charged price and the recorded `serving_model`/`priced_as` all name the same model, and a Claude pin with no route resolves to the direct ID exactly as today.
12. **`cloneShipped` is flipped before Check 38 passes.** Pinned by the existing gate tests (`docs_checkout_test.go:123-132`, `image_refresh_steps_test.go:662`, `init_repo_clone_test.go:19`), which fail if the flip and the docs disagree — a task flipping it must land Check 38's evidence first, and Task 3.6 says where that evidence is recorded.
13. **An owner-run step is run by an agent, or a check's guard falls back to an ambient project.** Every check task's steps carry **Check 43's block verbatim** (Task 0.4 step 1), and every check 49 to 56 names only the sandbox or a scratch repository, never a `belong` one; a reviewer reads each `gcloud` line for a bare ambient project, and `TestChecklistChecksAreGuarded` is a docs test over `docs/gcp-live-checklist.md` asserting that every `## Check 48`-onward section carries **Check 43's three hard stops and its project pinning, not merely a `:?` guard**: `P` is exactly `fugaro-dev` with the other variables non-empty, `fugaro doctor --project fugaro` is called and its `project:` line grepped, the checkout's origin is grepped against `edgeappinc/fugarosandbox`, every `fugaro` command in the section carries `--project fugaro` and every `gcloud` command `--project "${P:?}"`. **Owned by Task 6.3** (round-2 review: it was named here as "(phase 3)" and no task created it — it can only be written once all of 48 to 56 exist, which is why its home is the last phase's docs task, and the review focus now points there).

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/budget/evaluate.go`, `session.go` (+ tests) | the four refusal messages name real flags | 0.1 |
| `internal/infra/spec.go`, `internal/infra/readiness.go`, `internal/agent/env.go`, `internal/runner/gateway.go`, `internal/gateway/{gateway,route}.go` (+ tests) | `anthropic-api-key` optional end to end; the direct-Claude-with-no-key refusal | 0.2 |
| `internal/budget/model.go`, `internal/budget/rules/rules.go`, `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/{parity_test.go,testdata/rules.golden.json}`, `internal/budget/rules/emulator/` (+ tests), `docs/design/m9-budget-and-dashboard.md` | D6: the global per-run cap is the fallback, in rules and code | 0.3 |
| `internal/projectpolicy/{policy.go,paths.go,match.go,version.go}` (new) + tests | the policy object, its paths, its version, `MatchRepo` (P2, P4, P5, P8) | 1.1 |
| `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/{rules.go,parity_test.go,emulator/}` | `config/policy/{shared,repos/$slug}` read for a run token; `config/admin` admin-only | 1.2 |
| `internal/runner/{projectpolicy.go (new),policy.go}` (+ tests) | read the policy, fail closed, replace the ceiling, version refusal | 1.3 |
| `internal/infra/spec.go` (+ tests) | `FUGARO_RTDB_URL` whenever Firebase is configured; `FUGARO_PROJECT` already there | 1.4 |
| `internal/runner/{runner.go,project.go}` (+ tests), `internal/cli/run.go` | `allow_repos` and the slug binding's first half, before the token and the lease | 1.5 |
| `internal/cli/{project.go,root.go,doctor.go}` (+ tests) | `newProjectCmd()`, `policy publish`, `show`, `project repos ls\|add\|rm`; the doctor checks | 1.6 |
| `docs/gcp-live-checklist.md` | **Check 49**'s row and section | 1.7 |
| `internal/config/{providers.go,config.go}` (the operator's `routing`), `internal/projectpolicy/` (+ tests) | the `routing.claude` object, validated (explicit list only), delivered through the policy | 1b.1 |
| `internal/pricing/catalog*`, `internal/config/pricetable.go`, `internal/runner/agentenv.go`, `internal/runner/{spend.go,gateway.go}` (+ tests) | the canonical-to-route ID map in prices, pins and records | 1b.2 |
| `internal/cli/{validate.go,doctor.go}`, `docs/multi-model.md` | the resolution printed when readable, "unresolved offline" otherwise | 1b.3 |
| `docs/gcp-live-checklist.md` | **Check 50** | 1b.4 |
| `internal/cli/secrets.go`, `internal/cli/initsecrets.go` (+ tests) | `secrets set` without `--repo` stores project-wide (D2) | 2.1 |
| `internal/infra/spec.go`, `internal/infra/readiness.go` (+ tests) | a project-scope key mounts on the repository's job until phase 3 | 2.2 |
| `SECURITY.md`, `docs/project-layer.md`, `docs/multi-model.md`, `internal/cli/doctor.go`, `internal/cli/upgrade_cloud.go` | the D2 default, the data-policy gate's unchanged role, the drift note | 2.3 |
| `docs/gcp-live-checklist.md` | **Check 51** | 2.4 |
| `docs/gcp-live-checklist.md` | **Check 52**, the four probes, written and run **before** any phase 3 code (go/no-go) | 3.0 |
| `internal/projectpolicy/` (profiles), `internal/config/` (`workflows.<n>.profile`), `internal/infra/spec.go` | profiles in the policy, the workflow's selection, the default `standard` | 3.1 |
| `deploy/terraform/gcp/modules/project/` (new: `job.tf`, `sa.tf`, `iam.tf`), `.../modules/installation/`, `.../modules/installation/history.tf`, `scripts/gen-golden-plans.sh`, `internal/infra/tf/testdata/golden/` | one job + one job SA per (profile, provider, account); the broadened bucket IAM; the golden plans | 3.2 |
| `internal/runner/{projectjob.go (new),runner.go,provision.go,project.go}`, `internal/backend/backend.go` (+ tests) | the (profile, provider, account) from `CLOUD_RUN_JOB`, the slug binding, the remote from `task.json` + policy, admission, before any token or lease | 3.3 |
| `internal/backend/gcp/{run.go,names.go,logs.go}`, `internal/cli/{run.go,runs.go,logs.go,cancel.go}`, `internal/gcpfake/` (+ tests) | every job-naming caller on project jobs | 3.4 |
| `internal/cli/init_repo_clone_test.go`, `docs/checkout.md`, `internal/cli/recipes_skew.go` | `cloneShipped` flipped after Check 38 | 3.6 |
| `docs/gcp-live-checklist.md` | **Check 53** | 3.7 |
| `internal/cli/project_enroll.go` (+ tests) (`sync`, `repos ls\|add\|rm`), `internal/infra/discover*.go` | enrollment as data (D-spec §4.3) | 4.1 |
| `internal/cli/init_repo_stage.go`, `internal/infra/forget.go`, `internal/cli/init.go`, the config-transitions retirement engine | retire a repository's whole root | 4.2 |
| `internal/cli/init_repo_target.go`, `internal/cli/init.go`, `docs/checkout.md` | `init --repo` retired for clone repositories | 4.3 |
| `internal/cli/init_repo_stage.go`, `docs/`, `SECURITY.md` | baked workflows dropped (D3) | 4.4 |
| `docs/gcp-live-checklist.md` | **Check 54** (the owner's own migration of installation `fugaro`, never `belong`) | 4.5 |
| `internal/cli/accounts.go`, `internal/projectpolicy/`, `internal/infra/` | person-owned secrets, references, `accounts share` | 5.1 |
| `deploy/terraform/gcp/modules/project/`, `internal/backend/gcp/names.go` | one job per (profile, provider, shared account) | 5.2 |
| `internal/cli/{run.go,run_oauth*.go}`, `internal/runner/`, `plugin/skills/working/reference/diagnose.md` | failover moves from the skill into `fugaro run` | 5.3 |
| `docs/gcp-live-checklist.md` | **Check 55** | 5.5 |
| `internal/cli/project_export.go` (new), `internal/report/report.go`, `internal/watch/json.go` | `project export [--push]` | 6.1 |
| `docs/gcp-live-checklist.md` | **Check 56** | 6.4 |
| `docs/design/budget-token-channel.md` (new) | the D13 channel's design, an owner-scheduled design task (docs only) | 3b.1 |

## PR groups

Each phase is its own PR (or its own small group of PRs where the table below splits it), and each group's PR is mergeable and green on its own:

| Group | Tasks | What a reviewer reads hardest |
|---|---|---|
| **A** | 0.1, 0.2 | the two fixes; nothing shared |
| **B** | 0.3, 0.4 | **SECURITY-SENSITIVE (rules)** — the rules template, the emulator, the parity test and `model.go` in one commit; then the owner-run deploy |
| **C** | 1.1 | the policy object's shape and the version (no rules change yet: it is dead code until 1.3) |
| **D** | 1.2 | **SECURITY-SENSITIVE (rules)** — the per-child read rules, the emulator and the parity test |
| **E** | 1.3, 1.4, 1.5 | **SECURITY-SENSITIVE (fail-closed, admission)** — the read path, the halt, `budgetEnv` |
| **F** | 1.6, 1.7 | the CLI surface, the doctor checks, Check 49 |
| **G** | 1b.1, 1b.2, 1b.3, 1b.4 | the switch's validation (D1a/D1b) and the ID map |
| **H** | 2.1 | **SECURITY-SENSITIVE (keys)** — the default scope and what it reaches |
| **I** | 2.2, 2.3, 2.4 | the mounting path and SECURITY.md |
| **J** | 3.0 | **the go/no-go probes** — nothing in phase 3 starts before these are answered |
| **K** | 3.1, 3.2 | **SECURITY-SENSITIVE (IAM, bucket conditions)** — profiles, the project module, the golden plans |
| **L** | 3.3, 3.4 | **SECURITY-SENSITIVE (binding, admission, fail-closed)** — the runner and every caller |
| **M** | 3.5, 3.6, 3.7 | the launcher/operator grants and the dashboard; `cloneShipped` after Check 38; Check 53 |
| **N** | 4.1, 4.2, 4.3, 4.4 | the retirement of a repository root, and that nothing retires a resource another repository needs |
| **O** | 4.5, 4.6 | **OWNER-RUN (Check 54)** — the owner's migration; then the phase's docs and checks sweep |
| **P** | 5.1, 5.2, 5.3 | **SECURITY-SENSITIVE (OAuth pool)** — a person's token, its grants and its blast radius |
| **Q** | 5.4, 5.5 | #363 rebased, Check 55 |
| **R** | 6.1, 6.2, 6.3, 6.4 | **SECURITY-SENSITIVE (export)** — no value, no person by default |
| **S** | 3b.1 | **SECURITY-SENSITIVE (budget token)** — docs only, and its output is a design with its own review; it can land any time after 3.2 (D13 ruled 2026-10-15) |

## Phase 0 — small fixes (three tasks, plus the owner's deploy)

The design's phase 0 (§11). Nothing here shares a surface with anything else in the plan, and each of the three tasks is independently vetoable: the four flag names, the optional Anthropic key, and D6.

### Task 0.1: the refusal messages name flags that exist

**Files:** `internal/budget/evaluate.go` (+ `evaluate_test.go`), `internal/budget/session.go` (+ `session_test.go`), `internal/cli/budget_refusal_test.go` (new).

**Files it touches, verified today:** `internal/budget/evaluate.go:190` and `:247`, `internal/budget/session.go:501` and `:511`. The four strings are `fugaro budget set --defaults --repo-daily` and `--defaults --repo-per-run`; the flags the command actually registers are `--global`, `--defaults`, `--repo`, `--daily`, `--per-run` (`internal/cli/budget.go:687-691`), so the two messages must become `fugaro budget set --defaults --daily` and `fugaro budget set --defaults --per-run`.

**Interfaces.** Consumes: nothing new. Produces: no symbol — the four strings, and one test that keeps them honest.

1. Fix `internal/budget/evaluate.go:190` (`no daily cap is set for this repository and there is no default (fugaro budget set --defaults --daily)`) and `:247` (`... there is no default (fugaro budget set --defaults --per-run)`).
2. Fix the same two strings in `internal/budget/session.go:501` and `:511`. The second one (`:511`) also names `budget.per_run_usd`; keep that and the `--global --per-run` half, which is a real flag.
3. `TestRefusalMessagesNameRealFlags` (new file, `package cli`): the strings live in `internal/budget`, but the *flags* live in this package, so the test is here. It builds the real command (`newBudgetCmd()`), walks its `--`-prefixed tokens out of every `ReasonNoCap` and `Mode`-refusal detail the package can produce (a table of the four strings plus the two the gateway emits, `internal/runner/gateway.go:429-433`), and fails on any token the command does not define — after normalizing `budget.per_run_usd` (a config key, not a flag) out. It fails on the four before step 1 and passes after.

```go
// internal/cli/budget_refusal_test.go
func TestRefusalMessagesNameRealFlags(t *testing.T) {
	defined := map[string]bool{}
	newBudgetCmd().Flags().VisitAll(func(f *flag.Flag) { defined["--"+f.Name] = true })
	// a nested subcommand's flags (`budget set --per-run`) are on the child
	for _, c := range newBudgetCmd().Commands() {
		c.Flags().VisitAll(func(f *flag.Flag) { defined["--"+f.Name] = true })
	}
	for _, msg := range refusalMessages() { // the table, one entry per site
		for _, tok := range strings.Fields(msg) {
			tok = strings.Trim(tok, "(),;")
			if !strings.HasPrefix(tok, "--") || defined[tok] || configKey(tok) {
				continue
			}
			t.Errorf("%q names %s, which budget does not define", msg, tok)
		}
	}
}
```

**Command:** `GOFLAGS=-p=2 go test ./internal/budget/... ./internal/cli/... -count=1`.

**Commit:** one commit — "budget: the four refusal messages name flags that exist".

### Task 0.2: `anthropic-api-key` is not mandatory

**Files:** `internal/infra/spec.go` (+ `spec_test.go`, `secretmounts_test.go`), `internal/infra/readiness.go` (+ `readiness_test.go`), `internal/agent/env.go` (+ `env_test.go`), `internal/runner/gateway.go` (+ `gateway_test.go`), `internal/gateway/{gateway.go,route.go}` (+ tests), `docs/multi-model.md`.

**The five sites, re-verified:** `internal/infra/spec.go:585-591` (the mount, in the `case "api-key"` branch of `SecretMounts`), the readiness gate `internal/infra/readiness.go:100-158` (the secret-version probe — the design's "deploy blocked without a version"), `internal/agent/env.go:91-104` (the ≥4-byte demand for redaction), `internal/runner/gateway.go:74-76` (`up.APIKey = envLookup(r.d.Env, "ANTHROPIC_API_KEY")`), `internal/gateway/gateway.go:255-259` (`the anthropic upstream has no API key`), `internal/gateway/route.go:92-96` (`routes need the anthropic upstream, not %q`).

**Interfaces.** Consumes: the version probe `readiness.go` already runs (`hasVersion`, `internal/infra/readiness.go:100`, and the per-logical-name results it already collects). Produces: `Inputs.MountedSecrets map[string]bool` — logical name → "an enabled version exists", filled by `init` before the spec is built, and read by `SecretMounts`. Nothing else changes shape.

1. `SecretMounts` (`internal/infra/spec.go:585`): mount `anthropic-api-key` **only when `Inputs.MountedSecrets["anthropic-api-key"]`**. The accidental coupling the design notes goes away: the mount is a fact about data at deploy time, not about the mode or the pins, so flipping the routing switch later never needs Terraform (design §6.1).
2. The readiness gate (`internal/infra/readiness.go`): a missing `anthropic-api-key` is no longer a *blocking* finding when the run's pins name no direct Claude model. Keep the finding at `warning` severity with text naming the case it would break (`routing.claude` or a direct Claude pin), so an owner routing everything through OpenRouter is not nagged and one who pins Claude direct is told before the run.
3. `internal/agent/env.go:91-104`: the ≥4-byte demand applies only when the gateway holds a key. With the gateway present and no key, the agent's environment is built without `ANTHROPIC_API_KEY` rather than failing — the runner still registers the placeholder for redaction if it exists, but its absence is not an error.
4. `internal/gateway/gateway.go:255-259` and `internal/gateway/route.go:92-96`: the Anthropic upstream becomes optional; **a direct Claude call with no key is refused with a clear message naming the fix**, and the refusal is a *per-call* error, not a startup error. The route-level message (`routes need the anthropic upstream`) becomes "this route needs the anthropic upstream, which has no key: set `anthropic-api-key`, or route this model through a provider (`routing.claude`)". **The refusal must not become a silent route change**: a direct-Claude call with no key never falls through to a provider (that is phase 1b's opt-in and D1b's `direct` default).
5. `docs/multi-model.md`: the placeholder-key paragraph goes, and the "a key is needed only for the models that use it" rule is stated once.

**Tests:** `TestSecretMountsOmitsAbsentAnthropicKey` and `TestSecretMountsMountsPresentAnthropicKey` (`internal/infra`, table-driven over `MountedSecrets`); `TestApiKeyRunWithoutAnthropicKeyStarts` (`internal/agent`); `TestGatewayStartsWithoutAnthropicUpstream` and `TestDirectClaudeCallWithoutKeyIsRefusedByName` (`internal/gateway`); `TestReadinessWarnsInsteadOfBlocking` (`internal/infra`); `TestRunnerStartsWithNoAnthropicKeyWhenPinsAreRouted` (`internal/runner`). The last one is the end-to-end assertion: a run whose pins all name provider models reaches the agent, and one whose pins name `claude-*` fails at the first call with the message above.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/... ./internal/agent/... ./internal/gateway/... ./internal/runner/... -count=1`.

**Commit:** one commit — "keys: neither the Anthropic key nor a provider key is mandatory".

### Task 0.3: the global per-run cap is the fallback (D6) — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/budget/model.go` (+ `model_test.go`), `internal/budget/rules/rules.go`, `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/testdata/rules.golden.json`, `internal/budget/rules/{parity_test.go,rules_test.go}`, `internal/budget/rules/emulator_test.go` (tag `firebase`), `docs/design/m9-budget-and-dashboard.md`.

**Verified today:** `internal/budget/model.go:209-224` — `PerRun` returns `ok == false` when the repository and the defaults have no per-run cap, and its comment says *"ok is false when either side is unset, because the rules deny when either comparison reads null"*. `internal/budget/rules/rules.go:310-314` (`REPOPERRUN`, `GLOBALPERRUN` macros) and `:343-346` (`fallback(own, def)` returns null when both are absent). `internal/budget/rules/rules.json.tmpl:28` (the `reserved` validator comparing against both macros — the only site that does; `:32` is `released` and `:36` is `spent`, both comparing against `reserved`). `docs/design/m9-budget-and-dashboard.md:272` says the global cap *is* the default for every repository — which is what the code and rules do not do, and D6 makes it true.

**Interfaces.** Consumes: nothing new. Produces: `Caps.PerRun()`'s new contract (the global cap substitutes for an absent repository/default cap) and the same substitution inside the rules' `fallback()`.

1. `internal/budget/model.go`: `PerRun()` returns `(*c.Global.PerRunMicros, true)` when neither the repository's own cap nor the defaults set one, and keeps `min(repo, *c.Global.PerRunMicros)` when they do. Rewrite the comment: it now says the global cap is the fallback by owner decision D6 and that the rules mirror it. **A repository cap and the defaults still only tighten** — the global cap is a floor of *applicability*, never a loosening: an absent global cap stays `ok == false`.
2. `internal/budget/rules/rules.go:343`: `fallback(own, def)` gains the global cap as its last resort. **The earlier draft of this step justified the shape with "the rules language has no ternary", and that is false** (round-2 review): `fallback` itself already emits one, `(snap.exists() ? snap.val() : …)` (`internal/budget/rules/rules.go:341-346`), and so does `val` (`:244`). The real reason is the null semantics the existing helper is built around: a missing node reads **null**, and a comparison against null is **false**, so `X <= REPOPERRUN` cannot fall through to the global cap when `REPOPERRUN` is null — it denies, which is exactly today's behaviour and exactly what D6 must change. The fallback therefore has to be an explicit branch, and the plan keeps the `||` shape because it reuses the two macros the validator already names without adding a branch to `fallback` (which `REPODAILY` shares, where D6 does not apply): the *comparison* gains the fallback, `(X <= REPOPERRUN || (REPOPERRUN == null && X <= GLOBALPERRUN))`, where `== null` is the existence test and an absent global cap still denies. An equivalent nested `? :` emitted by `fallback` is available if the emulator rejects the `||` spelling — but the emulator is the decider either way (step 5), and the behaviour, not the spelling, is what step 4's parity case pins. Emit it from one helper used at the one site that compares against the macros (`rules.json.tmpl:28`'s `reserved` validator; the `released` validator at `:32` and the `spent` one at `:36` compare against `reserved`'s own balance and so need no change) so the two macros cannot drift apart. **If the emulator shows a null read comparing true or `== null` behaving otherwise, the task stops and reports** rather than emitting a rule that swallows the case: the whole point is that an absent cap still denies.
3. `internal/budget/rules/rules.json.tmpl:28`: both macros keep their meaning (repository/default, then global); the `reserved` validator is otherwise unchanged. Regenerate `testdata/rules.golden.json` (`rules_test.go:15`) in this commit.
4. `internal/budget/rules/parity_test.go` (`TestRulesParity`, `:175`): add the case that pins the new agreement — a project with a global per-run cap, no defaults cap and no repository node: `model.PerRun()` returns the global cap and the rules **allow** a reserve of exactly that size and **deny** one micro more.
5. The emulator test (tag `firebase`, `internal/budget/rules/emulator_test.go`): a run token writes `reserved` for a slug with no `config/caps/repos/<slug>` and no defaults, and the write succeeds when `global/perRunMicros` is set — the fail-open direction, exercised against the real rules.
6. `docs/design/m9-budget-and-dashboard.md:272` and its §6.4: the document now says what the code does. Add one sentence naming D6 and the date, so the change of direction is recorded in the document that was wrong.

**Tests:** the three above, plus `TestPerRunNoGlobalCapStillUnset` (an absent global cap is still `ok == false`, so this cannot become "no cap at all"), and `TestPerRunRepoCapStillTightens` (a repository cap below the global one still wins).

**Command:** `GOFLAGS=-p=2 go test ./internal/budget/... -count=1`, then the emulator: `go test -tags firebase ./internal/budget/rules/... -count=1` (CONTRIBUTING.md:21; it *fails* rather than skips when the emulator is missing, so a task without Java/Node must run it in CI before merging — and this task does not merge without it).

**Commit:** one commit — "budget: the global per-run cap is the fallback (D6), in the rules and the code". Nothing else rides along: the rules template, the golden file, the code and the m9 document are one reviewable change (P1).

### Task 0.4: Check 48, and the rules deploy — OWNER-RUN, no agent performs any step

**Files:** `docs/gcp-live-checklist.md`.

The design's phase 0 ends with an owner-run rules deploy (§2.5, D6). **An agent never deploys rules**: the security rules of a live database are the owner's to publish, and the deploy here changes what a run token may write.

Append `## Check 48: the global per-run cap as the fallback, and the rules deploy it needs (installation fugaro only, never belong; run by you; NOT RUN)` plus its row in the summary table, with exactly these steps in this order:

1. **Setup, hard-coded and guarded — and this is the block every check 49 to 56 copies verbatim** (R2-6; round-1 findings 30 and 31). The earlier draft's "the two-line guard of Check 48" was weaker than what Check 43 already does, and it is now **Check 43's own block**, three hard stops, spelled once here and required *word for word* in every later check — not "guarded like Check 48". The shape, from `docs/gcp-live-checklist.md:1158-1187` on `main`:

```bash
# === Check NN: setup + guard. RE-RUN THE WHOLE BLOCK at the start of every
# step: it is idempotent, and nothing below the three hard stops runs. ===
P=fugaro-dev                 # HARD-CODED, never `gcloud config get-value project`
L=<the installation's region>
# ${VAR:?} everywhere: an empty variable is a hard error, never an empty
# argument gcloud resolves from its ambient configuration.
[ "$P" = fugaro-dev ] && [ -n "$L" ] \
  || { echo "Check NN stopped: re-run the whole setup block (P=$P must be fugaro-dev; L=$L not empty)" >&2; exit 1; }
fugaro doctor --project fugaro | grep -q '^project: .*(GCP fugaro-dev,' \
  || { echo "Check NN stopped: this is not the fugaro-dev installation" >&2; exit 1; }
git remote get-url origin | grep -Eq '[:/]edgeappinc/fugarosandbox(\.git)?$' \
  || { echo "Check NN stopped: this checkout's origin is not the sandbox" >&2; exit 1; }
```

   The three stops, and why each is an assertion rather than a print: **(1)** `P` is *exactly* `fugaro-dev` and every variable the check uses is non-empty — a skipped, partially pasted or hand-edited block stops there instead of reaching `gcloud` with an empty argument, which is the N1 trap (`gcloud` then falls back to the owner's ambient `*-prod` project); **(2)** `fugaro doctor --project fugaro` reports that project — the second stop is also the fourth, because `--project` disagreeing with the checkout's `project:` is **refused** (`internal/localcfg/select.go:163`), and a checkout that is not the sandbox cannot pass it; **(3)** the checkout's origin is `edgeappinc/fugarosandbox`, never EdgeWeb's or EdgeServer's. **Then**: every `fugaro` command that takes the flag gets `--project fugaro` (the installation's *name*; the project is `fugaro-dev`) and every `gcloud` command gets `--project "${P:?}"` (plus `${L:?}`/`${R:?}` where used). `fugaro version` takes no flags and is the one exception. **Re-run the whole block from a fresh shell at the start of every step** — the later steps are minutes or days apart, and the variables are gone by then. Paste it into a file and run it with `bash`, not an interactive `zsh` (the checklist's own note, `:1190`): an interactive zsh does not honour the `#` comments unless `setopt interactive_comments` is set. **The `belong` stop is the point of stops 2 and 3 together**: this plan touches only installation `fugaro` and the sandbox checkout, `belong` holds edgeserver and edgeweb, **no agent runs any live step at all**, and the migration of `belong` is the owner's own work after the model is proven (Task 4.5, and the note in Global Constraints). **Record the state to restore**: `fugaro budget show --json` (the caps, defaults and mode), and the deployed rules' byte count from `init --firebase`'s own report.
2. **Owner-run deploy.** `fugaro init --firebase` on installation `fugaro` — the deploy of the generated rules (`internal/infra/firebasedb.go:206-219`, `PutRules`), the same step M9b's design names at `docs/design/m9-budget-and-dashboard.md:702`. **FACT:** the reported rules byte count, and that `init` read the deployed rules back (`internal/infra/firebasedb.go:215`).
3. **The new fallback, live.** With a **sandbox repository slug** that has no `config/caps/repos/<slug>` node and no `config/caps/defaults`: `fugaro budget set --global --per-run 2` (a real write), then one small sandbox run. **FACT:** the run's `policy` log line and `result.json`'s effective per-run cap, and that the run reserves rather than halting with `ReasonNoCap`. This is the mutation `TestRulesParity` cannot see: the emulator is a fake database.
4. **The refusal still refuses.** Remove the global per-run cap (`fugaro budget set --global --per-run 0`, or the value the restore records) and run the same sandbox task once more. **FACT:** the halt message verbatim — it must name `budget.per_run_usd` or `fugaro budget set --defaults --per-run`, and it must be a *refusal*, not a silent no-cap run. A run that succeeds here is the fail-open direction going too far and the deploy is rolled back (step 6).
5. **The dashboard and `watch`.** `fugaro budget show` and `fugaro watch --json` on the same project. **FACT:** the per-run line for a repository that has only the global cap — it must show the global value as effective, not `—`.
6. **Restore.** Put back the caps step 1 recorded (`fugaro budget set` with the recorded values), confirm `fugaro budget show --json` matches step 1's output, and run one ordinary sandbox task to confirm the sandbox still runs.

End with: "**Paste back:** every FACT line above, never a secret value and never a key value. Never run this against `belong`." **If the rules cannot express the existence-guarded fallback (Task 0.3 step 2), this check is not written and phase 0 stops**: the owner is told, and D6 is re-decided rather than half-shipped.

**Tests:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`.

**Depends on:** Task 0.3 (its own change is what this check deploys). Nothing else in the plan reads it.

## Phase 1 — policy as data (seven tasks)

The design's §5 and §4.3. **What this phase does and does not remove** (design §11): a change to a policy **value** (mode, per-run ceiling, allowed models, prices, `allow_repos`, and 1b's routing) stops needing `init --repo`; a change to what a job **mounts** (a secret, a provider key, a named account, a launcher or operator grant) still needs per-repository Terraform until phase 3. An installation that never publishes a policy is untouched, which is what makes this phase mergeable alone.

### Task 1.1: the policy object, its paths and its version

**Files:** `internal/projectpolicy/{policy.go,paths.go,match.go,version.go}` (new), `internal/projectpolicy/*_test.go` (new). Nothing reads it yet — this task is dead code plus tests, which is deliberate: it is the shape a reviewer vetoes cheapest before Task 1.2 changes the rules.

**Interfaces.** Produces (consumed by Tasks 1.2, 1.3, 1.4, 1.5, 1.6, 1b.1, 3.1, 5.1, 6.1):

```go
package projectpolicy // imports internal/rtdb and internal/task only: never internal/budget, internal/runner or internal/cli

const Version = 1

// Paths (P2; the caps' own helpers are internal/budget/model.go:44-80 and stay there)
func PathShared() string            { return "config/policy/shared" }
func PathRepo(slug string) string   { return "config/policy/repos/" + seg(slug) } // the $slug node
func PathAdmin() string             { return "config/admin" }

// PathRulesMarker is the rules-deployment marker. fugaro init --firebase
// writes it AFTER PutRules returns, never before it and never beside the
// other node writes — the rules are the deploy's last action
// (internal/infra/firebasedb.go:206-221), so a marker written earlier
// would survive a failed rules deploy and halt every budget-on run. It is
// readable by a run token under BOTH the old rules and the new ones, because it lives under
// config/limits — whose TOKEN read the old template already grants
// (internal/budget/rules/rules.json.tmpl:15). It is what lets the runner
// tell "the node is absent" (legacy path) from "the rules deny me" (halt)
// before the owner has deployed phase 1's rules: see Task 1.2 step 4 and
// Task 1.3 step 1. RulesVersion is the value init writes.
func PathRulesMarker() string { return "config/limits/policyRules" }

const RulesVersion = 1

// Policy is the readable node, decoded tolerantly: an unknown key is ignored
// (so a newer policy does not break an older runner on a field it does not
// know) and Version is the only thing that can refuse it (P8). Every field
// carries its json tag, and `budget` is all-or-nothing: see Decode.
type Policy struct {
	Version   int       `json:"version"`
	Budget    Budget    `json:"budget"`
	AllowRepos []string `json:"allow_repos"`
	Providers map[string]Provider `json:"providers,omitempty"`
	Prices    map[string]Price    `json:"prices,omitempty"`   // model_prices, the owner's overrides
	Routing   *Routing  `json:"routing,omitempty"`            // phase 1b writes it; nil = direct (D1b)
}
type Budget struct {
	Mode          string   `json:"mode"`
	PerRunUSD     float64  `json:"per_run_usd"`
	AllowedModels []string `json:"allowed_models"`
	MaxRunTokens  int64    `json:"max_run_tokens"`
}

// MatchRepo matches a canonical "<provider>/<owner>/<name>" against an
// allow-list pattern (P5). One implementation, used by the runner and the CLI.
func MatchRepo(pattern, canonical string) bool
func Canonical(provider, repo string) (string, error)

// Decode returns (p, found, err): found=false is the absent node (today's
// behaviour), err is a node that exists and cannot be used (fail closed).
func Decode(raw []byte) (*Policy, bool, error)
```

1. `paths.go`: the three path helpers plus `PathRulesMarker`/`RulesVersion`, with `seg`-style escaping copied from `internal/budget/model.go:44` (a slug is escaped the same way `/config/caps/repos/$slug` escapes it, so the two agree). The marker's path must stay under `config/limits`: **it is the one node a run token can read under the rules that predate this phase**, which is the whole point of it (Task 1.2 step 4).
2. `policy.go`: the type above plus `Decode`, which returns `found=false` for an empty body and an error for a body that does not parse, carries a non-integer or unknown `version`, or whose `allow_repos` holds a pattern `Canonical`/`MatchRepo` rejects. **Unknown JSON keys are ignored** (unlike the version-gated env vars the price catalog's P4 describes): a policy field a newer CLI writes must not brick an older runner mid-flight, and `version` is the one thing that may. **That tolerance is bounded by two rules that keep a typo from becoming a silent zero ceiling**: every field of `Policy` and `Budget` carries its explicit `json` tag spelled as the documented key (`per_run_usd`, `allowed_models`, `max_run_tokens`, `mode`, `version`, `allow_repos`), so the documented spellings are the only ones that decode; and a `budget` object that **has keys but none of the four documented ones** is an error, not an all-zero `Budget` — the round-2 review caught that the earlier wording ("carries none of its four documented keys") made `"budget": {}` an error, which is the opposite of what the tolerant path must do and what `TestDecodeAcceptsAPartialBudgetAndAnEmptyOne` asserts. The rule is therefore: an **empty** `budget` object is "the policy sets no budget" (all-zero, valid, indistinguishable from the key being absent), and a **non-empty** one that misses all four documented keys is `TestDecodeRefusesABudgetWhoseKeysAllMiss`'s error. Without the first, a hand-authored or cross-version policy written in camelCase decodes to a zero per-run cap and a zero token ceiling — a ceiling that would be enforced; without the second, the same file is indistinguishable from `"budget": {}`, which is what the tolerant path must still read as "the policy sets no budget". A budget that carries *some* of the keys stays valid: absent keys are zero, exactly as `fugaro.yaml`'s own `budget:` block behaves.
3. `match.go`: `Canonical(provider, repo)` produces `"github/acme/app"` from a provider kind and an `owner/name` — by **calling `task.CanonicalRepo` and validating the provider kind with `task`'s own `providerRE`** (`internal/task/task.go:179-182`) rather than re-implementing the lower-casing, the `.git` trim or the provider check, so the allow-list and the slug identity agree by construction and there is one spelling of a repository in the codebase (P5). `MatchRepo` matches `*` within one segment only (`github/acme/*` matches `github/acme/app`, not `github/acme/a/b`; `*/acme/*` is legal, a bare `acme/*` is not — P5).
4. `version.go`: `Version`, and the two skew messages as constants (the runner's refusal and the CLI's refusal), so Task 1.3 and Task 1.6 print the same text.

**Tests:** `TestPathsEscapeASlug`; `TestRulesMarkerPathIsUnderConfigLimits` (the marker's path stays readable under the pre-phase-1 template, asserted by reading the template's own tree in the test — if it ever moves out of `config/limits`, the whole safe-ordering argument in 1.2/1.3 breaks and this test says so); `TestDecodeAbsentIsNotFound`; `TestDecodeIgnoresUnknownKeys`; `TestDecodeRefusesBadVersion`; `TestDecodeRefusesBadAllowRepoPattern`; `TestDecodeReadsTheDocumentedSnakeCaseKeys` (the four budget keys as the docs spell them, asserted field by field — the test that would have caught the missing tags); `TestDecodeRefusesABudgetWhoseKeysAllMiss` (`"budget": {"perRunUsd": 5}` is an error, not a zero ceiling) and `TestDecodeAcceptsAPartialBudgetAndAnEmptyOne` (`{"mode":"observe"}` and `{}` are both valid); `TestMatchRepo` (a table: the wildcard cases, the cross-provider case, the two-`fugarosandbox` case of P5); `TestCanonical`; `TestCanonicalAgreesWithTaskSlug` (for a mixed-case `Owner/Name.git`, `Canonical`'s output and the pair `task.Slug` hashes are the same string, and `MatchRepo("github/acme/*", canonical)` admits the repository whose slug is `task.Slug("github", repo)` — the property P5 exists to guarantee).

**Command:** `GOFLAGS=-p=2 go test ./internal/projectpolicy/... -count=1`. **Commit:** "projectpolicy: the policy object, its paths, its version and its matcher".

### Task 1.2: the read rules — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`, `internal/budget/rules/testdata/rules.golden.json`, `internal/budget/rules/{rules_test.go,parity_test.go,emulator_test.go}`, **`internal/infra/firebasedb.go`** (+ its test, step 7 — the marker write lives with the rules deploy).

**Verified today:** the template's `config` block is `internal/budget/rules/rules.json.tmpl:9-21` — `caps/global` and `caps/defaults` `.read: "TOKEN"`, `caps/repos/$slug` and `kill/repos/$slug` `.read: "TOKEN && auth.token.fs == $slug"`, and no `.write` anywhere under `config` (the root `.write` is `false`, `:3-4`; admin writes are IAM-only, `deploy/terraform/gcp/modules/firebase/iam.tf:52-58`). The `TOKEN` macro is `internal/budget/rules/rules.go:292-293`; the slug claim is `auth.token.fs`.

**Interfaces.** Consumes: Task 1.1's paths (as literals in the template, and as a `rules.go` helper that **must agree with them** — the parity test asserts the template's paths equal `projectpolicy.PathShared()` etc., so the two cannot drift). Produces: `TestRulesParity`'s new cases and the generated rules.

1. Add to `rules.json.tmpl`'s `config`, beside `caps`:

```json
"policy": {
  "shared": { ".read": "TOKEN", ".write": false },
  "repos": { "$slug": { ".read": "TOKEN && auth.token.fs == $slug", ".write": false } }
},
"admin": { ".read": false, ".write": false }
```

**Per child, never on `config/policy` itself** (design §5(2)): a `TOKEN` read on the parent cascades to every child and would make `repos/$slug` and the admin node readable by every run token. `config/admin` carries `.read: false` in the *rules*, and **the rules are not what keeps it private**: launchers hold `roles/firebasedatabase.viewer` (`deploy/terraform/gcp/modules/firebase/iam.tf:43-49`) and an IAM read bypasses the rules, so 12a item 7 applies — **no secret-adjacent value in `config/admin`**, ever, and the section says so.
2. **Where the tests go matters here, and the earlier draft got it wrong.** `TestRulesParity` (`internal/budget/rules/parity_test.go:175`) is **not** a case table: it is a randomized Evaluate-against-rules generator behind `//go:build firebase`, and it covers lease and release **writes** only — it cannot carry a read-path case. The read-path rows belong in the emulator's own tables: `TestAllowedPaths` (`internal/budget/rules/emulator_test.go:143`) and `TestDenialTable` (`:338`), under tag `firebase`, asserting that a run token **can** read `config/policy/shared`, **can** read `config/policy/repos/<its own slug>`, **cannot** read another slug's node, **cannot** read `config/policy` (the parent), **cannot** read `config/admin`, and can read the marker `config/limits/policyRules` **under both the old and the new template** (a second table run against the previous golden — the row that keeps the safe ordering true). The non-emulator golden test is `rules_test.go`'s `TestRulesGolden`. **Without `-tags firebase` none of the read-path assertions run**, so the command below says `-tags firebase` explicitly and the task does not merge without it.
3. Regenerate `testdata/rules.golden.json` in this commit, and add the assertion that the template's policy paths equal `projectpolicy.PathShared()`/`PathRepo("x")` — a task renaming a path updates both or fails.
4. **The ordering, and the window it has to close** (this is the finding that shapes the task; the earlier draft of this plan got it wrong and said so). Until the owner deploys these rules, a run token's `GET` of `config/policy/shared` is **permission-denied**, not absent: the template's root is `".read": false` (`internal/budget/rules/rules.json.tmpl:3`) and nothing under `config` grants `policy` a read. So a runner that treated "I can't read it" as "the node is absent, carry on" would be defeated by the rules it cannot read, and a runner that treated every non-absent error as a halt (Task 1.3's first draft) would **halt every budget-on run on every installation whose rules had not been redeployed** — including `belong`, whose deploy is the owner's alone and whose lifetime is not this phase's to schedule. A release rolls out everywhere at once; a rules deploy does not. The plan therefore does three things, in this order:
   - **The marker makes the two states distinguishable.** `fugaro init --firebase` writes `config/limits/policyRules = RulesVersion` (Task 1.1) **in the same deploy that puts these rules in place, and only after `PutRules` has succeeded** (`internal/infra/firebasedb.go:206-221`; the rules deploy is the deploy's last act and the marker's write is the act after it — Task 1.2 step 7 states the failure direction). It needs **no new rule**: it sits under `config/limits`, whose TOKEN read the *old* template already grants (`internal/budget/rules/rules.json.tmpl:15`; `:13` is `config/caps/repos/$slug`), so it is readable by a run token before and after this phase's deploy, and unreadable only on an installation that never deployed rules at all. A worker's Task 1.4 test asserts the marker's path stays there.
   - **The reader is gated on the marker, not on the deploy order.** Task 1.3 step 1 reads the marker first: marker absent or lower than `RulesVersion` ⇒ this installation's rules predate this phase ⇒ the policy cannot be readable **and cannot have been published** (step 3 below) ⇒ the run takes the legacy path and a permission-denied read is not an error; marker present ⇒ permission-denied on `config/policy/shared` is a genuine anomaly ⇒ **halt**. That is the fail-closed direction without a window: an old installation runs as today, a new one cannot be silently denied.
   - **`policy publish` refuses to publish before the deploy.** `fugaro project policy publish` (Task 1.6) reads the marker with the operator's IAM credentials — IAM reads bypass the rules — and refuses when it is absent or older than `RulesVersion`, with the fix in the message (`fugaro init --firebase`). This is what makes "a published policy implies the rules are deployed" true **by construction** rather than by luck, and it is why the runner may halt on a denied read at all. The design's order (rules deploy, runner rollout, publish, §5) is per installation; this makes the *release* safe on an installation that has not run any of the three.
   - **The per-installation order is still the owner's** (Task 1.7): rules, then the runner image, then the publish. Nothing in this task or 1.3 requires that order for safety any more, but it is the order that keeps the failure messages good.
5. **D6's smaller window, stated the same way** (Task 0.3): a new runner computes the global fallback while old rules deny the reserve it needs. That is still a **refusal**, not a bypass, so it is safe but noisy — the owner deploys the phase-0 rules before rolling out the runner that computes them (Task 0.4 step 2 says so), and this plan does not claim otherwise.
6. **The run token is not only a reader** (12a item 12 — the design's §4.3 lists what a run token writes, and this change must not disturb it): it writes `runs/$slug/$run/...` (`:22-55`), `spend/$day/global/{counted,spent,notional}` (`:57-70`), `agents/$slug/$run` (`:145-172`) and `outcomes/$day/$slug/$run` (`:175-188`), the outcome node being write-once (`!data.exists()`). The policy edit is **additive and confined to `config`**, and the emulator's read and write tables gain one row per existing write above asserting it is still granted (Task 1.2 step 2's placement note) — so a later edit to the `config` block cannot quietly take away a run's ability to record its own outcome or its own spend. No existing row is renumbered or removed, and `TestWritesOnlyAtNumericLeaves` (`internal/budget/rules/rules_test.go:178`) must accept the new `.write: false` nodes rather than counting them as leaves.
7. `fugaro init --firebase`: write `config/limits/policyRules` (`projectpolicy.PathRulesMarker()`, value `RulesVersion`) **as the action after `PutRules` succeeds** (R2-4). `internal/infra/firebasedb.go:206-221` plans the node writes first and **queues the rules deploy last** — its own comment says so ("The rules: last, so they go live on a database that is marked and configured") — and the acts run in order. The marker is therefore **not** one more `d.put` beside the others and **not** "in the same step as `PutRules`": the natural reading of that earlier wording would leave a renamed, failed or declined rules deploy with the marker already present over the **old** rules, and the runner would then read the marker, be denied `config/policy/shared`, and **halt every budget-on run** on an installation with no policy published — finding 1's failure, reintroduced by the fix for it. The marker is appended to `d.acts` **after** the rules act, and its `do` runs only once `PutRules` has returned nil; on any rules-deploy failure the marker is not written at all. The failure direction is deliberate and stated in the code's comment: **rules without a marker costs a publish (recoverable, safe), a marker without rules costs every run (unrecoverable in the field)**. `:215` is the **pre-deploy read of the live rules** (`d.c.Rules`, feeding the `sameJSON` comparison) — it is not a read-back of what was deployed, and Check 48 step 2 and this step must not describe it as one (deferrable, R2-4). **Tests:** `TestInitFirebaseWritesTheMarkerAfterTheRules` (the acts are ordered: the rules act precedes the marker act), `TestInitFirebaseLeavesNoMarkerWhenTheRulesDeployFails` (a `PutRules` that returns an error ⇒ no marker put, asserted on the planned acts and on the executed calls), and `TestInitFirebaseSkipsTheMarkerWhenTheRulesAreAlreadyRight` — with `sameJSON(live, gen)` true there is no rules act at all, and the marker is written only when it differs from `RulesVersion` (an already-marked installation re-runs `init --firebase` and writes nothing).

**Command:** `GOFLAGS=-p=2 go test ./internal/budget/... -count=1` (this runs the golden and unit tests only — **none** of the read-path rows) **and** `go test -tags firebase ./internal/budget/rules/... -count=1` (the emulator, which is where they live; it fails rather than skips without Java/Node, CONTRIBUTING.md:21). **Commit:** "rules: config/policy is readable per child, config/admin is not readable by a run token".

### Task 1.3: the runner reads the policy and fails closed

**Files:** `internal/runner/projectpolicy.go` (new), `internal/runner/policy.go` (+ `policy_test.go`), `internal/runner/budget_session.go` (the client, the marker read and **the single token take** — finding 2), `internal/cli/launch_budget.go` (+ tests: the launcher's mode decision follows the policy), `internal/infra/spec.go` (the API key, with Task 1.4).

**Verified today:** `internal/runner/policy.go:63-120` — `resolvePolicy` builds the ceiling from `r.d.Spend` (the job env), merges the default branch's file layer and the run's own, and **treats the ceiling as optional** (`:66-72`: `Mode` only when non-empty, `PerRunUSD` only when `> 0`); `:108-113` falls back to `pricing.Embedded()` when the mode is on and no prices were loaded. `internal/runner/budget_session.go:25-52` is where the RTDB URL and the Firebase key come from (`RTDBURLEnv`, `FirebaseAPIKeyEnv` at `:29-31`), and `:128`/`:180` are where the slug keys the session. **Three facts the earlier draft of this task missed, all verified:** the launcher mints and stores a run token only when the *local* budget mode is not off (`internal/cli/launch_budget.go:38-40` — `budgetBackendOn` returns false for `BudgetOff` or no URL, and the mint is at `:174-188`); the token exchange needs `FUGARO_FIREBASE_API_KEY` (`budget_session.go:66-71`), not just the URL; and `token.TakeObject` **deletes** the token object as it reads it (`internal/budget/token/object.go:76`, and its own comment says a token left behind could be replayed), so a policy read that takes the token and a session that takes it again cannot both succeed.

**Interfaces.** Consumes: `projectpolicy.Decode`/`PathShared`/`PathRulesMarker`/`RulesVersion` (1.1), the RTDB URL **and API key** the job env carries (1.4), the run's own token minted by the launcher (and the one take Task 1.3 shares with the session). Produces: `Spend` and `policy.Effective` computed from the policy instead of the env when the node exists; `runstore.PolicyRecord.Sources` gains `policy` as a source name; and a `bootstrapBudget`-owned token take that both the early read and the later session use.

1. `projectpolicy.go`: `func (r *run) projectPolicy(ctx context.Context) (*projectpolicy.Policy, bool, error)` — **three outcomes, not two**, because the rules that make the node readable may not be deployed yet (Task 1.2 step 4):
   - **Marker absent, or below `projectpolicy.RulesVersion`** ⇒ this installation's rules predate phase 1 ⇒ no policy can have been published here (Task 1.6 refuses to publish without the marker) ⇒ **`found=false`, the legacy path, and a permission-denied or missing `config/policy/shared` read is not an error**. This is what keeps a release from halting `belong`, `fugaro` and every other installation whose rules deploy is the owner's own step.
   - **Marker present and `config/policy/shared` absent (empty body)** ⇒ installed, not adopted ⇒ `found=false`, today's behaviour exactly (P3).
   - **Marker present and the node exists but cannot be read** (permission denied, network, or `Decode`'s error: unparseable, bad `version`, bad pattern) ⇒ **halt** with a message naming the node, the marker and the fix. **The message must name the one cause that is not a tampering case** (R2-8(b), deferrable): an **older CLI's `fugaro init --firebase`** redeploys the pre-phase-1 rules (its `sameJSON` differs from the new template, so it *does* redeploy) and leaves the marker in place, so the policy that was published is now unreadable and every budget-on run halts. The text is `the deployed rules are older than the marker config/limits/policyRules: re-run 'fugaro init --firebase' with fugaro >= <version>` — with the version substituted from `RulesVersion`'s release, and a `docs/project-layer.md` line saying the same, because an owner who reads the halt must be able to fix it without the plan in hand. `fugaro doctor` reports the same skew before a run fails (Task 1.6 step 4's checklist), and the `policySince` launch gate (finding 16, Task 1.6 step 3) refuses the *launch* rather than the run in the version-skew case the gate covers. A denied read here is a genuine anomaly: the marker is written by the same deploy that grants the read, so "denied" means the rules were changed out from under a published policy, which is exactly the case that must not fall back to a wider ceiling.
   The client is the one `internal/rtdb` builds from `RTDBURLEnv` + `FirebaseAPIKeyEnv` + the run's token. **The three outcomes above are reachable only when that client exists, and the earlier draft of this step stated a rule that could never fire** (round-2 review, high): the credential the marker read needs is the very credential whose absence the rule was about. `internal/budget/session.go:175-199` is the proof — `token.Exchange` is the first thing `Open` does (`:188`, the exchange at `:66-71` of `budget_session.go` is what needs `FUGARO_FIREBASE_API_KEY`) and `rtdb.New` (`:214`, `:50` in the listing) builds the client from the resulting token, so **a run with no URL or no key has no RTDB client and cannot see the marker**; "marker present ⇒ halt" is dead code for exactly the run it was meant to catch. The rule is restated on what the run can observe:
   - **The credential is present and the session opens** ⇒ the marker read above runs in full: marker absent/older ⇒ legacy; marker present + node absent ⇒ today's behaviour; marker present + node exists but denied or unparseable ⇒ **halt**. This is the fail-closed direction, and it is the only one the runner can implement.
   - **The mode-off path is exactly three absences, and nothing else** (R2-3): a missing `FUGARO_RTDB_URL`, a missing `FUGARO_FIREBASE_API_KEY`, or a missing token object (`token.TakeObject` returning `token.ErrNoToken`) ⇒ the run is on the **mode-off path** (P3), the same path as an installation that never turned the budget on, and the runner does not halt. The gate for that case is the **launcher**, which holds the operator's credentials and is trusted (design §2.2's threat model: the run is the untrusted party): Task 1.4's readiness gate is **blocking** once `config/policy/shared` exists and refuses the job that cannot read it, and Task 1.3 step 2 makes the launcher mint a token whenever a policy exists, mode off included. What the runner must never do — and what the marker still prevents — is treat **a denial it can see** as an absent node.
   - **An exchange or read failure halts. It is never the mode-off path** (R2-3 — **the earlier draft said the opposite and that was a fail-open**, the round-2 review's high finding). Today a budget-on run whose exchange fails stops: `startBudgetBackend` returns "the budget backend refused the run's identity" (`internal/runner/budget_session.go:205-213`, the `budget.Open` switch) or halts `HaltBudgetTokenExpired`/`HaltBudgetUnavailable` (`:200-207`). Under "the exchange fails ⇒ mode off", a network blip or an expired token during the policy read would drop the run to the env ceiling — possibly mode off, with no gateway and no cap — on an installation that **has published a policy**. That is a regression of a shipped property, not a simplification. The policy read therefore reuses the session's own failure classification, in one place: the marker read, the policy read and `budget.Open` share the single token take of step 2 and map their errors exactly as `startBudgetBackend` does — `token.ErrTokenExpired` ⇒ `bootstrapHalt(HaltBudgetTokenExpired, …)`, `budget.ErrGraceExpired` ⇒ `bootstrapHalt(HaltBudgetUnavailable, …)`, anything else ⇒ the refusal `the budget backend refused the run's identity: …`. A permission-denied or unparseable `config/policy/shared` read taken while the credential works is in the same class (step 1's third outcome): **halt**. The rule the implementation states in one sentence: *the mode-off path is entered by an absence the run can see before it has a credential, never by a failure it sees after one.*
2. **One token take, shared** (finding 2). `internal/cli/launch_budget.go`'s decision changes (`budgetBackendOn`, `:38-40`): a token is minted when Firebase is configured **and either the local mode is not off or a policy exists** — the launcher can see the policy with its own IAM credentials in the same call it already makes for the advisory check (Task 1.5 step 2), so the mode decision follows the policy rather than the operator's local mode. On the runner side, the read moves into the session's own bootstrap: `TakeObject` is called **once** and its result handed to both the policy read and the budget session (`budget_session.go`'s `:128`/`:180` path takes the object today). The task states the invariant in the code's comment: **`TakeObject` is called exactly once per run**, because a second call finds nothing and a token kept in memory is the same token.
3. `resolvePolicy`: when the policy exists, the **ceiling** is built from it instead of from `r.d.Spend`:

```go
ceiling := policy.Layer{MaxRunTokens: c.MaxRunTokens, AllowedModels: c.AllowedModels}
if pp != nil {
	ceiling = policy.Layer{Mode: pp.Budget.Mode, PerRunUSD: pp.Budget.PerRunUSD,
		MaxRunTokens: pp.Budget.MaxRunTokens, AllowedModels: pp.Budget.AllowedModels}
	c.Prices = pp.Prices          // the owner's overrides, replacing FUGARO_MODEL_PRICES
	c.Cap = /* µ$ from PerRunUSD */
}
```
   **Only the ceiling moves** (12a item 13): the default branch's `fugaro.yaml` layer and the run's own `cfg` are still merged at `:90` and can still only tighten. `s.Cap = c.Cap` at `:100` keeps working because the policy's cap becomes `c.Cap` too.
4. `runtimeVersion` skew (P8): a policy whose `version` is not `projectpolicy.Version` halts with the shared message — the runner never guesses. (The marker's own version, `RulesVersion`, is separate and answers a different question: *can I read the policy at all*, step 1.)
5. The policy's `allow_repos` is stashed on the run for Task 1.5 (one read, one decode: `projectPolicy` is memoized like `defaultBranchFile` at `internal/runner/policy.go:30-51`).
6. `recordPolicy` (`internal/runner/policy.go:167`) names the source: `policy.sources` carries `policy=config/policy/shared` for the moved keys, so a run's `result.json` says where its ceiling came from. `fugaro diagnose` and `report` read that field unchanged.

**Tests:** `TestRunnerReadsPolicyWhenTheNodeExists`, `TestRunnerUsesEnvCeilingWhenTheNodeIsAbsent`, `TestRunnerWithoutTheMarkerTreatsADeniedReadAsLegacy` (the finding-1 regression test: marker absent + permission denied ⇒ the run proceeds on the env ceiling), `TestRunnerHaltsOnADeniedReadWhenTheMarkerExists`, `TestRunnerWithNoCredentialTakesTheModeOffPath` (no URL / no key ⇒ no session, no marker read, the env ceiling — the unimplementable "halt without the API key" assertion the round-2 review caught is replaced by this one, and the refusal is asserted where it can be: `internal/infra`'s readiness gate, `TestReadinessBlocksAJobThatCannotReadAPublishedPolicy` in Task 1.4), **`TestRunnerHaltsWhenTheExchangeFails`** (R2-3's sibling, and the red step of this task: with the URL, the key and a token object all present, the exchange server answers 500 — or the token is past its hour — and the run must **halt** (`HaltBudgetTokenExpired` for the expired token, the "refused the run's identity" refusal for the 500), *not* proceed on the env ceiling; the test asserts the halt reason and, on the regression the earlier draft described, fails with "the run fell back to the env ceiling after a failed exchange"), `TestRunnerHaltsWhenTheMarkerReadFails`, `TestPolicyReadErrorHaltsTheRun`, `TestPolicyParseErrorHaltsTheRun`, `TestPolicyVersionMismatchHaltsTheRun`, `TestTokenIsTakenOnce` (the policy read and the session share one take; the fake asserts exactly one `TakeObject`), `TestLauncherMintsATokenWhenAPolicyExistsAndTheLocalModeIsOff` (in `internal/cli`, finding 2), `TestPolicyCannotLoosenTheBranchLayer` (a branch `fugaro.yaml` asking for a *higher* cap is still ignored, `policy.Ignored` records it), `TestPolicyPricesReplaceTheEnvOverrides`. The runner's existing fakes serve the node (`internal/gcpfake` has no RTDB; the budget tests' `internal/rtdb` test server is the pattern to reuse — if it is not reachable from `internal/runner`'s tests, put the policy read behind a `Deps` field the test sets).

**Command:** `GOFLAGS=-p=2 go test ./internal/runner/... -count=1`. **Commit:** "runner: the project policy replaces the ceiling, and a policy that cannot be read halts the run".

### Task 1.4: the RTDB URL **and API key** reach every job (D11)

**Files:** `internal/infra/spec.go` (+ `spec_test.go`), `internal/infra/readiness.go` (the Firebase requirement's message), `internal/cli/launch_budget.go` (with Task 1.3 step 2: the mint decision).

**Verified today:** `internal/infra/spec.go:755-768` — `FUGARO_RTDB_URL`, `FUGARO_FIREBASE_API_KEY` and `FUGARO_BUDGET_MODE` are written **only when the deploy-time budget mode is not off**, and the mode is read from the operator's local config (so the design's warning is exact: a job deployed with the mode off can never read the policy). **The earlier draft of this task wrote only the URL**, which is not enough: the token exchange needs the API key as well (`internal/runner/budget_session.go:66-71`, `FirebaseAPIKeyEnv` at `:30`), and a policy read that has a URL and no key fails exactly where it must not — at the hallt in Task 1.3 step 1.

**Interfaces.** Consumes: the installation's Firebase endpoints (`lc.Endpoints`), the project's Firebase API key. Produces: a job env that carries **both** whenever Firebase is configured, whatever the mode.

1. `budgetEnv` (`internal/infra/spec.go:732`): write `FUGARO_RTDB_URL` **and** `FUGARO_FIREBASE_API_KEY` whenever the installation has them, and keep `FUGARO_BUDGET_MODE` conditional on the mode (the mode is no longer only the deploy-time mode once a policy exists, and Task 1.3 reads the policy's mode; the env stays as the fallback for the absent-node case, unchanged).
2. The readiness gate (`internal/infra/readiness.go`): **Firebase becomes mandatory in project-centric mode** (D11). The finding is a `warning` until a policy exists (an installation that has not adopted the model is not blocked) and *blocking* once `config/policy/shared` exists — read through the operator's client at init time, so the message is "the project has a policy: this job must carry `FUGARO_RTDB_URL` and `FUGARO_FIREBASE_API_KEY`, run `fugaro init --repo`". **The plan does not try to detect "project-centric mode" any other way**, and it does not lift the mode-off code path: an installation that wants the budget off stays on the per-repository path (design §5(1)), permanently, which `fugaro doctor` states.
3. **This is a security-relevant task, not a plumbing one** (the review says so and it is right): it decides whether a published policy can be read at all. Round-2 review restated what the failure looks like, because the runner cannot detect it: a job that carries no key while a policy exists **runs on the environment ceiling** (Task 1.3 step 1's mode-off path — the runner has no way to read the marker without the credential), so this task's **readiness gate is the refusal** (`init --repo`, `doctor`: blocking once `config/policy/shared` exists) and the launcher's mint (Task 1.3 step 2) is what makes the job able to read the policy at all. It is listed for independent opus review with Task 1.3.
4. `docs/`: the mode-off caveat in `docs/multi-model.md` and `docs/project-layer.md` gains one sentence: the budget mode off is not available in project-centric mode.

**Tests:** `TestBudgetEnvWritesRTDBURLAndAPIKeyWhenFirebaseIsConfigured` (both variables, mode off included), `TestBudgetEnvKeepsModeOff`, `TestReadinessBlocksAJobThatCannotReadAPublishedPolicy`, `TestReadinessWarnsBeforeAPolicyExists`.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/... -count=1`. **Depends on:** Task 1.1's path constant (for the readiness message) and nothing else in this phase. The round-2 review is right that the earlier "it is independently revertible" line contradicted the audit, which recorded a dependency on 1.3; the two are reconciled like this. The *dependency* runs the other way — Task 1.3's reader consumes the two variables this task writes — but the two tasks **share one file**, `internal/cli/launch_budget.go` (this task's Task-1.3-step-2 half, the mint decision), so neither merges alone: they are PR group E together. The audit rows for 1.3 and 1.4 now say exactly that instead of a backward edge. **Commit:** "spec: the job carries the RTDB URL whenever Firebase is configured".

### Task 1.5: `allow_repos` and the slug binding's first half — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/runner/{runner.go,project.go}` (+ tests), `internal/cli/run.go` (+ `run_layer_test.go` or a new `run_allowlist_test.go`).

**Verified today (and the earlier draft of this task was wrong about two of these; the review caught it and the code confirms it).** `bootstrap` reads `task.json` at `internal/runner/runner.go:797-803`, **then** calls `openProvider` at **`:833-849`** — `:586` is that function's *definition*, not the call — then clones at `:851-855`, reads `fugaro.yaml` at `:1015-1019` and resolves the policy at `:993`. So "before `openProvider`" is a real place *after* `task.json` is known (`:803`) and *before* any git credential exists (`:833`), and it is where this phase's checks have to live: `resolvePolicy` (`:993`) is far too late, because the clone — and the token it needed — has already happened. **The provider before the clone is not available from `fugaro.yaml`**: `config.Git.Provider` is read at `:1015`, after it. What exists at `:833-849` is `r.d.ProviderKind` (the `--provider` flag, defaulting to `$FUGARO_GIT_PROVIDER`, `internal/cli/exec.go:69`) or `gitprov.KindForURL(origin)` — both env-derived and launcher-overridable, which is exactly what 12a item 2 forbids. The slug comes from `FUGARO_RUN` (`internal/cli/exec.go:157`,`:168`, `runstore.ParseRef` at `internal/runstore/runstore.go:409`) and keys the locks (`internal/runner/lockcache.go:76`), the cache (`:188`) and the budget session (`internal/runner/budget_session.go:128`,`:180`); `task.Slug(provider, repo)` is `internal/task/task.go:179`. **Nothing checks that `task.Slug(provider, spec.Repo)` equals the slug**: today the per-repository job's bucket condition is what makes a mismatch fail (design §2.2).

**Interfaces.** Consumes: Task 1.1's `MatchRepo`/`Canonical`, Task 1.3's memoized policy. Produces: `func (r *run) admissionCheck(ctx context.Context) error`, and the CLI's advisory twin `func allowListRefusal(p *projectpolicy.Policy, provider, repo string) error` in `internal/cli`.

1. **Placement, in `bootstrap`'s own order**: `task.json` (`:797-803`) → **the marker and policy read (Task 1.3)** → **this check** → `openProvider` (`:833-849`) → the clone. Stated as an ordered list because the order *is* the security property: the check must run before a credential exists and before the clone, and the policy must be readable before the check can use it. `resolvePolicy` keeps its later place — it merges the policy as the ceiling once `fugaro.yaml` has been read (`:993`), and it must not be the place the policy is first read.
2. **The provider, derived without asking any overridable source (12a item 2, finding 3).** The check needs a provider to compute `task.Slug(provider, spec.Repo)`; phase 1 does **not** have one in the job env it can trust. It derives it from the pair it *can* trust — the full slug the run was given (`FUGARO_RUN`, which is also what the budget token's `fs` claim and the job's bucket condition bind to) and the repository `task.json` names:

```go
// providerForSlug is the provider kind whose Slug of repo is slug. One job
// serves one provider, and the slug is a hash of (kind, canonical repo)
// (internal/task/task.go:187), so exactly one kind can match. It is
// derived, never read from an env var a launcher can override (12a item 2).
func providerForSlug(slug, repo string) (kind string, ok bool) {
	var found []string
	for _, k := range jobProviderKinds { // github, bitbucket — never "fake"
		if s, err := task.Slug(k, repo); err == nil && s == slug {
			found = append(found, k)
		}
	}
	return cmp.Or(found...), len(found) == 1
}
```
   Zero matches ⇒ refuse ("`FUGARO_RUN` names a slug that is not this repository under any provider kind"). Two matches ⇒ refuse (a hash collision, or a new kind added without a domain separator — `task.Slug` already separates with `"\x00"`, so this is a programming error made visible instead of invisible). A `--provider`/`FUGARO_GIT_PROVIDER` value that disagrees with the derived kind is also refused — the flag may only tighten. **Phase 3 does this differently and better** (the job name carries the profile and the provider, Task 3.2/3.3); this is the phase-1 form, and it is what makes phase 1 safe to merge before any project job exists.
3. Then the two refusals, both before `openProvider`:
   - `task.Slug(providerKind, spec.Repo) == <the slug FUGARO_RUN carries>` — implied by the derivation above and re-checked explicitly, naming both, with the sentence that the per-repository job's bucket condition used to be the only thing standing in for it.
   - `MatchRepo(p.AllowRepos, Canonical(providerKind, spec.Repo))` — refuse otherwise, naming the pattern list and `fugaro project repos add`.
   - When the policy is **absent** (no marker, or a marker and no node — Task 1.3 step 1), only the slug binding and the provider derivation are checked: an installation that has not published a policy has no allow-list, and inventing "empty means deny" there would break every installation on upgrade. **Empty `allow_repos` *inside* a published policy is deny-all**, and `policy publish` seeds it (P4), so the two cases cannot be confused.
4. `fugaro run` (the CLI, `internal/cli/run.go`) checks the same thing before launching and refuses with the same message, reading the policy with the operator's credentials. **It is advisory and the docs say so** (design §4.2/§4.3): a launcher can call `jobs.run` directly, which is why the runner's check is the one that counts.
5. `SECURITY.md`: the launcher-override boundary lands here (D10, accepted): one paragraph saying that a launcher can override a job's environment, that the budget token deliberately does not travel that way, and **that the pointer is overridable too** — clearing `FUGARO_RTDB_URL` (or pointing it at another database) sends the run down the env-ceiling path, because the mode *is* the node and the pointer to the node is a launcher's to clear (finding 27; design §4.2 lists the pointer among the overridable settings). The paragraph says exactly what an execution's overrides expose **from the phase-1 evidence in Check 49 step 6** (a live `FUGARO_RUN` override on a real job), not from a phase-3 probe — the earlier draft made this paragraph wait for Task 3.0's probe, which was a forward edge into phase 3 and is gone (finding 14).

**Tests:** `TestProviderForSlugDerivesEachKind`, `TestProviderForSlugRefusesNoMatchAndAmbiguity`, `TestRunnerIgnoresAnOverriddenProviderEnvAmongAgreeingKinds` (a `FUGARO_GIT_PROVIDER` naming the *other* kind is refused, not obeyed), `TestRunnerRefusesSlugMismatch`, `TestRunnerRefusesSlugNotInAllowList`, `TestRunnerChecksBeforeTheTokenAndTheLease` (the fake asserts **no** credential request and **no** reserve write, and that the refusal happens before the call at `internal/runner/runner.go:833`), `TestRunnerWithoutAPolicyChecksOnlyTheBinding`, `TestEmptyAllowListInAPublishedPolicyDeniesEverything`, `TestRunRefusesBeforeLaunch` (CLI).

**Command:** `GOFLAGS=-p=2 go test ./internal/runner/... ./internal/cli/... -count=1`. **Commit:** "runner: bind the slug and admit the repository before a token or a lease".

### Task 1.6: `fugaro project`, the doctor checks and the drift note

**Files:** `internal/cli/project_cmd.go` (new) (+ `project_policy_test.go`), `internal/cli/root.go`, `internal/cli/doctor.go` (+ tests), `internal/cli/upgrade_cloud.go` (+ tests), `plugin/skills/setup/**`, `plugin/skills/working/**`, `docs/project-layer.md`, `docs/multi-model.md`, `docs/checkout.md`, `SECURITY.md`.

**Verified today:** **there is no `fugaro project` command group**; `internal/cli/project.go` already exists and is project *selection* (`checkoutProject` at `:30`), so the group gets its own file, and the root registers every command at `internal/cli/root.go:75-78`. `upgrade`'s drift note is `internal/cli/upgrade_cloud.go:162-177` (`jobConfigCopyNote`), and `doctor`'s severities are `internal/cli/doctor.go:39-46`.

**Interfaces.** Consumes: Task 1.1's `Policy`/`Decode`/paths, the RTDB client `fugaro budget set` uses (`internal/cli/budget.go:953` `runBudgetSet`, `openBudget` at `:160`), the operator's local config (`providers`, `claude_accounts`, the budget ceiling and `model_prices`) and the bucket config reader. Produces: `newProjectCmd()` and three subcommands.

1. `newProjectCmd()` (`Use: "project"`, registered in `internal/cli/root.go:75-78`): `policy publish`, `policy show`, `show`, and a `repos` subcommand with `ls`, `add` and `rm`. The plan puts `repos add` here, in phase 1, not phase 4 (P4 and 12a item 3): between phases 1 and 4 a new repository would otherwise be unenrolled, and this one-liner is the interim path. **`sync`, `repos ls` beyond the allow-list and the discovery surface stay in phase 4** (Task 4.1).
2. `policy publish`: builds the policy from the operator's file — the M9a budget (`mode`, `per_run_usd`, `allowed_models`, `max_run_tokens`), providers (name, base URL, auth style, **the secret's name, never a value**, models, `allow_data_to`, `route_fee_pct`, and from the price catalog `require_zdr`/`price_policy` when those have merged), `model_prices` overrides, `allow_repos`, and (1b.1) `routing` — writes it with `PutIfMatch` (the CAS `runBudgetSet` already uses, `internal/cli/budget.go:1011`) and refuses to write a `version` above its own (P8). It prints the diff and the seeded allow-list, and it **seeds only when the node is absent or `--seed-repos` is passed** (P4), seeding from the operator's `repos:` **and** the repositories the installation has seen (`runs/<slug>/` in the bucket), each converted with `Canonical`. **It writes names, never values**: every secret-shaped field is a Secret Manager name — a provider's key, the git credentials, and (Task 3.3) the GitHub App key — read from the operator's config as names, with the values never loaded on this path at all, which `TestPolicyCarriesNoSecretValue` asserts by building the node from a config whose fake Secret Manager holds known bytes and scanning the published body for them.
3. `policy show` / `project show`: the effective project — caps (global, defaults, per-repository), the policy, the allow-list, the repositories seen, each with its source; `--json` for `watch`-style consumers. It prints the allow-list minus the seen repositories (unenrolled-but-running) and the reverse (allowed but never seen), which is the pair an operator needs.
4. `fugaro doctor`: new checks — `policy-published` (info: whether a policy exists and its version), `policy-newer-than-binary` (warning, P8), `policy-vs-jobs` (warning when a policy is published and the installation's jobs still carry an old runner image: the trap of 12a item 6, detected with the image digests `image refresh` already compares), `allow-list-empty` (warning when a policy exists whose `allow_repos` is empty).
5. `upgrade` (`internal/cli/upgrade_cloud.go:162-177`): the drift note gains the policy sentence — after a policy is published, `upgrade` says that a job's **env copy** of the ceiling is now inert for the keys the policy governs, so the drift it reports is no longer a reason to re-run `init --repo`, and what *does* still need one (mounts) is listed.
6. The skills: `plugin/skills/setup/**` (the setup skill describes `init --repo`'s policy questions and must now say they are answered once per project) and `plugin/skills/working/**` (the working skill's `fugaro.yaml` policy section). Both keep their security text; `fugaro update-skills` distributes them.
7. Docs: `docs/project-layer.md` (the layer is now what tightens the policy), `docs/multi-model.md` (the prices come from the policy when published), `docs/checkout.md` (unchanged, but the phase-3 note is added there in Task 3.6), and SECURITY.md's Task 1.5 paragraph.

**Tests:** `TestProjectPolicyPublishWritesTheNodeAndSeedsTheAllowList`, `TestProjectPolicyPublishDoesNotReseedARemovedRepo`, `TestProjectPolicyPublishRefusesAVersionItCannotWrite`, `TestPolicyCarriesNoSecretValue` (no value of any secret the fake Secret Manager holds appears in the published bytes — the rule 12a item 7 and Task 3.3 depend on), `TestProjectShowPrintsEnrolledAndSeen`, `TestDoctorPolicyChecks` (all four, including the severity), `TestUpgradeNoteSaysTheEnvCopyIsInert`, and the docs tests for the skill and doc edits.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1` and `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`.

**Depends on:** Tasks 1.1, 1.2, 1.3 (the shape, the rules and the reader it writes for). **Commit:** "cli: fugaro project policy publish, show and project repos add; doctor and upgrade say what the policy changes".

### Task 1.7: Check 49, the rules deploy and the adoption order — OWNER-RUN

**Files:** `docs/gcp-live-checklist.md`.

Append `## Check 49: policy as data, end to end (installation fugaro only, never belong; run by you; NOT RUN)` and its row, with **the order the design fixes: rules deploy, runner rollout, `policy publish`** (design §5), and exactly these steps:

1. **Setup and guard** — **Check 43's block verbatim** (Task 0.4 step 1: the three hard stops, `--project fugaro` on every `fugaro` command, `--project "${P:?}"` on every `gcloud` command, re-run from a fresh shell at the start of every step), run from a file. **Record the restore**: `fugaro budget show --json`, `fugaro project show --json` (before: nothing), and the sandbox's `fugaro.yaml` `project:` and pins.
2. **The rules deploy.** `fugaro init --firebase` on installation `fugaro`. **FACT:** the rules byte count, that init read them back, and — the new part — that no `config/policy/shared` node exists yet (`fugaro project show` says "no policy").
3. **The runner rollout.** Release a runner image that carries Task 1.3, then roll it onto the sandbox repository's jobs (`fugaro upgrade --cloud`, or `fugaro image refresh`). **FACT:** one ordinary sandbox run's `policy.sources` — with no policy published it must name the **ceiling from the env**, exactly as before, and the run must succeed. This is the "nothing changes until you publish" half of P3, and it is the step that fails if Task 1.4's `FUGARO_RTDB_URL` regressed the mode-off path.
4. **Publish.** `fugaro project policy publish` on installation `fugaro`, with a per-run ceiling the sandbox run can notice (for instance `per_run_usd` different from the job env's). **FACT:** the printed diff, the seeded `allow_repos` (and that it names `edgeappinc/fugarosandbox`'s canonical form, not a bare name — P5), and the node's `version`.
5. **The policy governs.** One sandbox run. **FACT:** `result.json`'s effective `policy` and its `sources` (they must name `config/policy/shared`), and the run's per-call charge agreeing with the published ceiling rather than the job env's — the mutation Task 1.3's unit test cannot see, because this is a real token against real rules.
6. **It fails closed.** Point the sandbox's `fugaro.yaml` at a repository the allow-list does not name (a temporary edit on the sandbox branch, reverted in step 8), and run. **FACT:** the refusal verbatim, and that **no git token was minted and no lease was taken** — visible as the absence of a `credential` line and of a reserve in `fugaro budget show`. Then, on the same run, confirm the **slug-mismatch** refusal by launching with a `FUGARO_RUN` override naming another slug (a launcher can: `internal/backend/gcp/run.go:110`): the run must refuse rather than run.
7. **The CLI is advisory and says so.** `fugaro run` on the same unenrolled repository prints its own refusal before launching; and the docs read (`SECURITY.md`, `docs/project-layer.md`) names the launcher-override boundary rather than claiming a fix.
8. **Restore**: revert the sandbox `fugaro.yaml` edit, `fugaro budget set` with step 1's recorded values, confirm `fugaro project show --json`'s caps match, and run one ordinary sandbox task to confirm the sandbox still runs. **Leave the policy published only if the owner intends to keep the model**; otherwise `fugaro project policy publish --delete` (the removal path Task 1.6 must therefore ship) and re-run one task to confirm the env ceiling governs again.

End with: "**Paste back:** every FACT line above, never a secret value and never a key value. Never run this against `belong`."

**Tests:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`.

**Depends on:** Tasks 1.2 (the rules it deploys), 1.3, 1.4, 1.5, 1.6. **Owns the phase's one owner-run step.**

## Phase 1b — the routing switch (four tasks; gated on two other PRs)

The design's §6.2. **The gate is real and is not this phase's to control**: #359's phase 2 (the `anthropic/<id>` claim and `IsAlias(model, claimed)`) and the price catalog (#361) must be merged. **If they are not merged when phase 1 ends, this phase waits and phases 2 to 6 proceed** — nothing in them reads the routing switch (P7). The switch is *delivered* by the policy (Task 1.1's `Routing` field) but *written* only by the operator, in the local operator config or the project layer, and a repository can never write it (D1a).

### Task 1b.1: `routing.claude` — the object, its validation and its delivery

**Files:** `internal/config/config.go` (+ the operator's section), `internal/localcfg/localcfg.go` (+ tests), `internal/config/{providers.go,validate.go}` (+ tests), `internal/projectpolicy/policy.go` (+ test), `internal/cli/project.go` (publish).

**Verified today:** `ClaudePatterns` forces Claude direct and `CheckModelPattern` refuses a provider pattern overlapping it (`internal/config/providers.go:331-344`, `:205-220`); `pricing.IsAlias` refuses the `anthropic/` prefix and `~` (`internal/pricing/alias.go:30-44`), and `CheckPins` refuses the pin (`internal/config/pins.go:27-32`); the provider's `openness` is `AllowDataTo` (`internal/config/providers.go:164` `OpenToEveryRepoReason` — an absent `allow_data_to` means every repository, which is exactly why D1b's safeguard must require it to be **written**).

**Interfaces.** Consumes: Task 1.1's `projectpolicy.Policy.Routing`; #359's claim mechanism. Produces: `config.RoutingClaude{Via string; Models []string}` and `localcfg.Config.Routing`, plus `RoutingClaudeFor(pins, providers) (route string, model string, ok bool)` — one resolver used by the pins renderer, the price lookup and `validate`.

1. `config.RoutingClaude` with `Via` in `direct|openrouter|auto` (empty ⇒ `direct`, D1b) and an **explicit list of canonical Claude IDs** in `Models` — never a pattern: a glob, a bare prefix or an empty list with `via: openrouter` is refused by validation with the message naming D1a and the merged ruling it comes from.
2. Validation: `openrouter` and `auto` resolve to a provider **only when that provider's `allow_data_to` is written**, as a list or the explicit `["*"]` (design §6.2). An absent `allow_data_to` with `via: openrouter` is refused at config load, naming `allow_data_to` and the provider.
3. Definition sites: the operator's local config and the project layer, and **nowhere else** — a repository's `fugaro.yaml` has no `routing` key, which the existing `TestRepoCannotAddProvider`-style test asserts for this key too.
4. `policy publish` copies `Routing` into `config/policy/shared`; `project show` prints the objects and the IDs.

**Tests:** `TestRoutingAbsentIsDirect`; `TestRoutingRefusesAGlob`; `TestRoutingRefusesOpenrouterWithoutAllowDataTo`; `TestRepoCannotWriteRouting`; `TestPolicyCarriesRouting`.

**Command:** `GOFLAGS=-p=2 go test ./internal/config/... ./internal/localcfg/... ./internal/projectpolicy/... -count=1`. **Commit:** "config: routing.claude, an explicit ID list, absent means direct".

### Task 1b.2: the canonical-to-route ID map

**Files:** `internal/pricing/catalog/gencatalog/main.go` (+ `main_test.go`), `internal/pricing/catalog/catalog.json` (regenerated), `internal/pricing/{catalog_apply.go,pricing.go}` (+ tests), `internal/config/pricetable.go`, `internal/runner/{agentenv.go,spend.go,gateway.go}` (+ tests), `scripts/gen-price-catalog.sh`.

**Verified today:** the gateway never rewrites a body, so a Claude model has a different ID per route (`claude-sonnet-5-5` versus `anthropic/claude-sonnet-5.5`) and nothing translates between them (design §2.4); `stagePins` returns nil when the gateway is off (`internal/runner/agentenv.go:54-57`) and renders the pins at `:54-77`; the price catalog's rows are keyed by the provider's model ID (`internal/pricing/catalog`).

**Interfaces.** Consumes: the catalog's rows and the switch (1b.1). Produces: a `canonical` field on a catalog row and one resolver, `pricing.RouteIDFor(canonical string, route string) (string, bool)`, used by the pin renderer, the price lookup and the recorded `serving_model`/`priced_as`.

1. The generator: the allowlist gains the Claude rows **only under the opt-in the catalog plan's D8 defers to this phase**, and each Claude row carries `canonical_id` (the direct ID, `claude-sonnet-5-5`) beside its own key (the route's ID, `anthropic/claude-sonnet-5.5`). A row whose canonical ID is missing, or two rows claiming one canonical ID on one route, is a hard error — the same class of refusal the catalog's Task 1 already has.
2. `RouteIDFor` and its inverse: given a canonical ID and a resolved route, the ID to send and the ID to price. **The same function is what `serving_model`/`priced_as` are recorded with**, so a bill, a charge and a record name the same model — the design's requirement (§6.2) and Review Focus 11's mutation.
3. `stagePins` (`internal/runner/agentenv.go:54-77`): with the gateway on and `routing.claude.via` naming this Claude ID, the pin renders the route's ID; otherwise the canonical ID, exactly as today. **With the gateway off nothing changes**: `stagePins` still returns nil (routing exists only with the gateway, D11 makes that the project-centric mode).
4. The price lookup and the gateway's route resolution use the same resolved ID, so a Claude pin routed through OpenRouter is priced from the catalog's row for that route (the catalog plan's P5 builder) and never from the Anthropic table.

**Tests:** `TestCanonicalMapRoundTrip` (Review Focus 11), `TestGeneratorRefusesDuplicateCanonicalID`, `TestStagePinsUseTheRouteIDOnlyWhenOptedIn`, `TestClaudePinWithoutOptInIsUnchanged`, `TestRoutedClaudeIsPricedFromTheCatalogRow`.

**Command:** `GOFLAGS=-p=2 go test ./internal/pricing/... ./internal/config/... ./internal/runner/... -count=1`. **Depends on:** 1b.1 and the merged catalog. **Commit:** "pricing: the canonical-to-route ID map, one resolver for pins, prices and records".

### Task 1b.3: what the owner sees, offline included

**Files:** `internal/cli/{validate.go,doctor.go,diagnose.go}` (+ tests), `docs/multi-model.md`, `docs/design/mixed-credentials.md` (a pointer only).

1. `validate`: prints the routing resolution **when it can read the policy** (an operator's config and credentials), and **"unresolved offline"** when it cannot — this is a requirement, not a wording choice (design §11): `fugaro validate` is offline, and a resolution it cannot see must not be printed as if it had been. A project with **no Anthropic key and a Claude pin** fails validation with a message naming `routing.claude: openrouter` (design §6.2's safeguard).
2. `doctor`: a `routing-resolution` check (info) printing the same resolution, or that it is unresolved, and a `routing-cache-markers` info naming that the cost of a routed Claude senior is unmeasured until Check 50 runs.
3. `docs/multi-model.md`: the owner-facing paragraph — the object, the explicit list, the absent-means-direct default, the `allow_data_to` requirement, and the one sentence about cache markers being a live question with a number attached (Check 50).

**Tests:** `TestValidatePrintsUnresolvedOffline` and `TestValidateRefusesAClaudePinWithNoKeyAndNoSwitch` are the two that matter; plus `TestDoctorRoutingCheckNamesTheLiveQuestion`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1` and `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`. **Commit:** "cli: the routing resolution, printed when readable and said unresolved when not".

### Task 1b.4: Check 50 — OWNER-RUN

Append `## Check 50: a Claude senior through a provider route, and whether its cache markers survive (installation fugaro only, never belong; run by you; NOT RUN)` and its row. Steps, guarded with **Check 43's block verbatim** (Task 0.4 step 1), with the restore:

1. Setup, guard, and the recorded state (the sandbox's pins, `routing.claude`, the job env's prices, the key's credit).
2. The refusal, unclaimed: with no `routing.claude`, a `anthropic/<id>` pin is refused at every surface. **FACT:** the refusal text.
3. The claim: `routing.claude: {via: openrouter, models: [claude-sonnet-5-5]}`, published. **FACT:** `fugaro validate`'s resolution line, and that a repository cannot write it (an attempt to add `routing:` to the sandbox's `fugaro.yaml` is refused).
4. Two runs of the same small task, one direct (no switch) and one routed. **FACT:** the reviewer's own `model call` lines — `serving_model`, `priced_as`, `cache_read`/`cache_write_5m` token counts and `charged_micros` — plus `cost.model_usd` and `cost.reported_usd` for both, and the bill for the day.
5. **The conclusion that is the point of the check:** whether the routed run reports cache **reads** at all. A routed Claude senior that never reports one is charged at the full input rate, which is the difference between list price and several times list price (design §6.2). **FACT:** both token counts, and one sentence on the cost difference.
6. The `auto` case: `via: auto` with and without an Anthropic key version. **FACT:** which route each run took, and that with no key the run does not fall through silently.
7. Restore: the switch removed (`policy publish` without it), the pins put back, one ordinary sandbox task confirming the sandbox still runs.

**Paste back:** every FACT line, never a key or a token. **If step 5 shows the markers do not survive, the design's conclusion does not change but the docs do** — Task 1b.3's paragraph gains the measured number, and the switch's help text says what it costs. **Depends on:** 1b.1 to 1b.3.

## Phase 2 — project-scope keys by default (four tasks)

The design's §6.3 and D2. **This phase changes a security default** and says so: #343's rule was "nothing shared until the owner says so" (`docs/design/project-secrets.md` §3(b), and its *Veto alternatives* (b) at `:57`); D2 makes storing at project scope *be* the opt-in. What it does **not** change is the data-policy gate: a provider's `allow_data_to` still decides which repositories' code the gateway may route to that provider, at runtime, and it still does **not** keep the key out of a shared job's environment (design §6.3).

### Task 2.1: `secrets set` stores at project scope — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/cli/secrets.go` (+ `secrets_test.go`), `internal/cli/initsecrets.go` (+ tests), `internal/infra/readiness.go` (messages), `SECURITY.md`.

**Verified today:** `secrets set` resolves a repository by default (`resolveSecretRepo`) and stores `gcp.SecretID(r.slug, name)` (`internal/cli/secrets.go:214-286`); `--project-wide` is the opt-in (`:76`, `checkScope` at `:80-92`) and its eligibility runs through `projectSecretRefusal` (`:289-296`: `allow_data_to` must list at least one repository) and `repoMayUseProject` (`:497`). `gcp.ProjectSecretID(fugaroProject, logical)` is `internal/backend/gcp/names.go:154`.

**Interfaces.** Consumes: #343's machinery unchanged (the containers, the precedence, `ResolvedSecretID`). Produces: a new default in `secretsOptions` (`scope` derived from the flags), and the same store/read paths.

1. **Scope resolution becomes explicit and defaults to project.** `fugaro secrets set <name>` with neither flag stores the **project** copy; `--repo` (or `--repo <slug>`) stores the repository copy and is kept until phase 3, where a repository copy loses its job to mount it. `--project-wide` stays as an accepted synonym of the default so existing scripts and the skills do not break, and `--repo` + `--project-wide` stay mutually exclusive (`:87`).
2. **The reserved names are swept in, the OAuth tokens are not** (D2): `anthropic-api-key`, the git credentials (`config.ReservedSecrets`' names except the Claude OAuth namespace) and provider keys are eligible project-wide **without the per-name `allow_data_to` list** — `projectSecretRefusal` no longer fires for them. `claude-oauth-token-*` keeps its refusal: it is person-owned and phase 5 replaces it (`claudeOAuthAccountRefusal`, `internal/cli/secrets.go:196`).
3. What "reaches every enrolled repository" means is written in the code's message and in SECURITY.md, in one sentence: **inside a shared job a project secret has no IAM boundary between repositories — the profile is the boundary** (design §4.1, §6.3), and `workflows.<n>.secrets` cannot be shared across a job's mounts (they become named project secrets on a profile, or stay on the per-repository path).
4. `init --repo`'s secrets stage (`internal/cli/initsecrets.go`): a name the project already covers stops being asked for, which extends #343's 40i to the new default. **The prompt text says the value will be stored project-wide** unless `--repo` is passed, because the prompt is where a person learns the blast radius.
5. `SECURITY.md`: the D2 paragraph — the default, what it reverses, the profile as the boundary, and the data-policy gate's unchanged role.

**Tests:** `TestSecretsSetDefaultsToProjectScope`; `TestSecretsSetRepoFlagStoresTheRepoCopy`; `TestProjectWideIsStillAccepted`; `TestAnthropicKeyNeedsNoAllowDataToList`; `TestOAuthTokenStillRefusesProjectScope`; `TestInitRepoDoesNotAskForAProjectCoveredSecret`; `TestAllowDataToStillGatesProviderRoutes` (Review Focus 8, in `internal/gateway`).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... ./internal/gateway/... -count=1`. **Commit:** "secrets: project scope is the default (D2)".

### Task 2.2: a project-scope key mounts on the repository's job until phase 3

**Files:** `internal/infra/spec.go` (+ `secretmounts_test.go`), `internal/infra/readiness.go` (+ tests).

**Verified today:** `SecretMounts` (`internal/infra/spec.go:585-600`) takes the logical name and resolves the secret ID elsewhere (`Inputs.Secrets`, `:273`, and `ProjectSecrets`/`ProjectSecretCandidates` from #343, `:287-291`); `SecretID`/`ProjectSecretID` are `internal/backend/gcp/names.go:143-154`.

1. A name that resolves to the **project** copy mounts the project's secret ID on the repository's job (`gcp.ProjectSecretID`), which #343 already supports; this task makes it the *default* resolution rather than the opted-in one, and pins it with a test that the mounted ID is the project's when no repository copy exists.
2. Readiness: a project-covered name is never reported missing (`internal/infra/readiness.go`'s `resolveProject`, `:100-158`), and the fix line names `fugaro secrets set <name>` (now project scope) rather than `--repo`.
3. The phase-3 hand-off, stated in the file's comment: when the repository stops having its own job (phase 3), the same resolution feeds a **profile's** mount list instead, and the repository copy's precedence survives only on the per-repository path.

**Tests:** `TestProjectSecretMountsOnTheRepoJobWithoutARepoCopy`, `TestRepoCopyStillWins`, `TestReadinessNamesTheProjectScopeFix`.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/... -count=1`. **Depends on:** 2.1. **Commit:** "spec: a project-scope key mounts on the repository's job".

### Task 2.3: docs, doctor and the drift note

**Files:** `internal/cli/doctor.go` (+ tests), `internal/cli/upgrade_cloud.go` (+ tests), `docs/project-layer.md`, `docs/multi-model.md`, `docs/dogfooding.md` (the review list already names secrets), `plugin/skills/setup/**`.

1. `doctor`: a `secret-scope` check (info) listing which names are project-scope and which are repository copies, and a `warning` when a repository copy shadows a project name (the footgun `secrets.go:275-286` already warns about at set time). This is the "launcher grants"-adjacent surface the cross-cutting list asks for: what a job mounts, and from where.
2. `upgrade`'s drift note (`internal/cli/upgrade_cloud.go:162-177`): after D2, a name stored at project scope changes what a job **mounts** when the project secret is created or deleted — so the note says when a re-run of `init --repo` is still needed for a mount (phase 3 removes this) and when it is not.
3. Docs and skills as in Task 1.6 step 6, extended with the D2 paragraph and, in the setup skill, the changed prompt wording.

**Tests:** `TestDoctorSecretScopeCheck`, `TestDoctorWarnsOnAShadowedProjectName`, `TestUpgradeNoteNamesTheMountCase`, plus the docs tests for the edited text.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1` and `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`. **Commit:** "doctor, upgrade and the docs: what project scope changes".

### Task 2.4: Check 51 — OWNER-RUN

Append `## Check 51: project scope by default, and what it reaches (installation fugaro only, never belong; run by you; NOT RUN)` and its row. **Check 40 stays as it is** — it verifies #343's opted-in path, which is still reachable through `--project-wide` and through a repository copy; this check verifies the new default. Steps, guarded with **Check 43's block verbatim** (Task 0.4 step 1), with the restore:

1. Setup and guard; record `fugaro secrets ls --json`, the sandbox job's mounts and the sandbox key's scope.
2. The default: `fugaro secrets set anthropic-api-key` in the sandbox checkout **with no flag**, and paste the scope line alone. **FACT:** the scope, and that the sandbox's job mounts the **project** secret ID after the next `init --repo` (the mount is the phase's remaining Terraform step, D2's 12a item 10).
3. The reach, and the boundary: a **second sandbox repository the owner creates for this step** (say `dimipaun/fugarosandbox-scope`, empty and under the App — **never a `belong` repository, never edgeserver or edgeweb**, R2-6), or the same sandbox repository with a repository copy removed, runs with the project key. **FACT:** the run starts, and one sentence on what the profile boundary is until phase 3 (each repository still has its own job). The scratch repository is deleted in the restore.
4. The gate is unchanged: a provider route whose `allow_data_to` does not list the sandbox repository is still refused at runtime even though the key is project-scope. **FACT:** the gateway's refusal verbatim.
5. A repository copy still wins: `secrets set --repo <name>` then run. **FACT:** which secret version the job mounted (the IDs visible in `fugaro secrets ls --json`), and that removing the copy falls back to the project's.
6. Nothing leaks: `fugaro secrets ls`, `fugaro diagnose`, the run's logs and a `terraform plan` of the sandbox root printed to a file. **FACT:** the absence of any value (bytes count, never the value).
7. Restore: put back the scope and copies step 1 recorded, one ordinary sandbox task.

**Paste back:** every FACT line, never a secret value. **Depends on:** 2.1 to 2.3.

## Phase 3 — shared project jobs for clone workflows (eight tasks)

The design's §4 and §4.1. **This is the phase that removes per-repository provisioning for clone workflows**, and it is the phase that must not break the ones that have not moved yet: a legacy per-repository job keeps the phase-1 contract (Task 3.3 step 1), and **D13 was ruled on 2026-10-15 (R1)**: the budget token's exposure under the broadened `runs/` grant is **accepted and documented in SECURITY.md beside D4**, Task 3b.1 designs the replacement channel, and repositories that must stay apart use separate profiles meanwhile — there is no gate left here. **It is also the phase that states a regression**: one job service account per (profile, provider, account) means the bucket conditions can no longer name one slug, and a run — an untrusted agent, unlike a launcher — could poison another repository's cache, forge or steal a lock, or rewrite another repository's queued `task.json` (design §4.1's table; `SECURITY.md:36` lists exactly those among the properties 0.7.0 gained). D4 accepts it and documents it. Terraform today lives at `deploy/terraform/gcp/{roots,modules}` — `roots/{installation,firebase,repo}` and `modules/{installation,firebase,repo,workflow}` — and a repository's job, its service account and its bucket conditions come from `modules/workflow/{job.tf,sa.tf}`, with the launcher's `run.jobs.runWithOverrides` role in `modules/installation/iam.tf:26-31` and `modules/workflow/job.tf:93-102`.

### Task 3.0: the four UNVERIFIED probes, written and answered first — OWNER-RUN, and a go/no-go — Check 52

**Files:** `docs/gcp-live-checklist.md`.

Design §13 lists what was never settled from the code, and this phase is the one that needs four of them. **They are run before any phase 3 code is written**, because two of them decide whether the phase is shaped the way the design says: the Bitbucket token decides whether Bitbucket is first-class here at all (D5), and the Secret Manager IAM question decides whether a runtime fetch is even a fallback. Append `## Check 52: the four probes phase 3 depends on (installation fugaro, sandbox only, never belong; run by you; NOT RUN)` and its row, with **Check 43's block verbatim** (Task 0.4 step 1) and these steps:

**What no step of this check may touch, stated before the steps** (R2-6; round-1 findings 22 and 31, escalated — **this is the `belong` safety rule of the whole plan, and it is not negotiable**): **no agent creates a credential, no agent runs a live step, and no step of this check creates a token for the `edgeappinc` workspace or probes any repository that is not a scratch repository created for the probe.** `edgeappinc` is the owner's production workspace: it holds `edgeserver` and `edgeweb`, which the `belong` project runs, and **no agent and no probe ever touches them** — the migration of `belong` is the owner's own work after the model is proven on `fugaro` (Task 4.5). The earlier draft's "a Bitbucket **workspace** access token for the sandbox workspace … plus `git ls-remote` against a repository **outside** the intended scope" is exactly the reach that must not happen: a workspace token in that workspace is a token over production repositories, and asking what it can reach asks by using it. The scope is therefore split, and the split is the point:

- **GCP-side probes run in installation `fugaro`'s own project** (`fugaro-dev`, the guard's hard-coded `P`), because that is the project the model is about and the answers must transfer to it (the service-account quota, the bucket policy, the IAM condition, the secret reference). They act on the sandbox's **own** job, service account, bucket and secrets — never on another installation's.
- **Credential- and repository-scope probes run on scratch repositories created for this check**, in a **dedicated scope that holds no production repository**: either a Bitbucket **project** access token on a Bitbucket project the owner creates for the probe (Bitbucket's project-scoped tokens are the whole question U1 asks), or a **scratch workspace** holding only probe repositories. If neither can be created cheaply, U1 is answered "cannot be scoped" and Bitbucket stays on the per-repository path — the safe answer is available without ever pointing a token at production.

1. **Setup and guard** — Check 43's block verbatim (Task 0.4 step 1), plus the recorded state (the sandbox job's env, its service account and its bucket condition; **the kind of any credential the owner already holds**, named by kind only). The guard's three stops are what make "sandbox checkout, `fugaro-dev`, nothing else" true before a single `gcloud` line runs.
2. **U1 — can a Bitbucket credential be scoped to one project (the first task of the design's phase 3, §4.5)?** The owner creates, **in Bitbucket's UI — no agent creates a credential** — a **project access token on a dedicated Bitbucket project** that holds **two scratch repositories created for this probe** (`fugaro-probe-<n>`, empty, no production code). Then, in a scratch checkout: `git ls-remote https://x-token-auth:<token>@bitbucket.org/<probe-workspace>/fugaro-probe-1.git`, one push to a scratch branch of `fugaro-probe-1`, and `git ls-remote` against the **other probe repository in a different Bitbucket project** (the out-of-scope target — a scratch repository too, **never** a production one, and never anything under `edgeappinc`). **FACT:** whether the token reads and pushes to exactly the project's repositories, whether it is refused by the other project's probe repository, and the token's own scopes as Bitbucket reports them. **If the scope cannot be a Bitbucket project's repositories, Bitbucket projects stay on the per-repository path** (design §4.5) — **the owner is told before phase 3 starts**, D5 is re-decided, and Tasks 3.1 to 3.7 proceed for GitHub only. The fallback the design names — a runtime fetch by secret-name prefix with an IAM condition — is itself probe U4 below and **reintroduces the shared-service-account exposure of §4.1**, so it is a decision, not a default.
3. **U2 — does a Cloud Run **secret reference** work inside a container env override?** The API type has a `ValueSource`. One `gcloud run jobs execute`/`jobs update` attempt on the sandbox job with a `ContainerOverride` env var whose value is a secret reference, and one plain run. **FACT:** whether the override is accepted and whether the value arrives; **and the D10 counterpart — whether an execution's own overrides are visible to the container** (the runner already reads its execution's template for `FUGARO_RUN`, `internal/backend/gcp/run.go:420-430`; what is unverified is whether the *override* is what the template reports). **This step's answer is what SECURITY.md's launcher-override paragraph is allowed to claim** (Task 1.5 step 3): if the override is visible, the text says so; if it is not, the text says only that the API merges overrides and the runner cannot see them.
4. **U3 — Secret Manager IAM conditions on a name prefix.** `gcloud secrets add-iam-policy-binding` on one sandbox secret with an `expression` naming a prefix, then a read attempt with the sandbox job's service account. **FACT:** whether the condition is honoured for `secretmanager.versions.access` and what the denial looks like. If it works, it is a *possibility* for U1's fallback and for phase 5's per-person grants; if not, both designs say so.
5. **U4 — the quota and bucket-policy ceilings** (design §13): Google's default service-account quota (about 100 per project) and the runs bucket's IAM policy size at scale. **FACT:** the sandbox project's current service-account count against its quota, and the sandbox bucket's policy binding count. The current model exceeds the SA quota at about 60 repositories and the project-job model does not — this step is what records that the sandbox still has headroom.
6. **Restore**: the scratch branch and the **two scratch probe repositories** deleted, the Bitbucket **project** token revoked **by the owner in Bitbucket** (record only that it was revoked, never the token), the job's env put back, the IAM binding removed, and one ordinary sandbox task confirming the sandbox still runs.

End with: "**Paste back:** every FACT line above, never a token, key or secret value. **Never run this against `belong`, never create a `edgeappinc` workspace token, and never point a probe at edgeserver or edgeweb** — every repository this check names is a scratch repository created for it." **A step that creates a credential outside the scratch scope, or probes a repository this check did not create, stops the check and is reported to the owner** — not fixed in place. **A step that fails does not stop the phase silently**: it is reported to the owner and the affected task is re-scoped in the same PR, in writing.

### Task 3.1: profiles — the policy's shape and the workflow's selection

**Files:** `internal/projectpolicy/{policy.go,profiles.go}` (+ tests), `internal/config/{config.go,validate.go}` (+ tests), `internal/infra/spec.go` (the workflow's `profile`), `internal/cli/project.go` (`policy publish`).

**Interfaces.** Consumes: Task 1.1's `Policy`. Produces: `projectpolicy.Profile{Provider, CPU, Memory, TimeoutClass string, Secrets []string, AllowRepos []string}` and `Policy.Profiles map[string]Profile`, plus `config.Workflow.Profile string` defaulting to `standard`.

1. `profiles` in the policy: the shareable unit is "a job with these properties" (design §4.1). `standard` must exist; a policy that names a profile in `workflows.<n>.profile` and does not define it is refused **at publish time** and at runner start (fail closed, the Task 1.3 path).
2. **A profile carries its provider, and it is required** (finding 4; a **design amendment**, see that section). One Cloud Run job cannot serve a GitHub repository and a Bitbucket repository at once, because the provider is part of the slug (`task.Slug(provider, repo)`, `internal/task/task.go:187`), part of the job's identity and part of the git credential it holds — so "one job per (profile, account)" only means anything once a profile pins which provider it serves. The job key is therefore **(profile, provider, account)**, and because the provider is a field *of* the profile the key can be written either way; the plan writes it as one job per (profile, account) **per provider the profile names**, and Task 3.2 encodes the provider in the job's own name. A project with repositories on both providers defines two profiles (`standard`, `standard-bitbucket`) or two sets; the default `standard` names one provider, and a GitHub-only project never notices. The workflow's `profile` selects the profile, and a repository whose provider the profile does not serve is refused at admission (Task 3.3) with a message naming both.
3. `allow_repos` on a profile is the admission list the runner enforces in Task 3.3, and the *project's* `allow_repos` stays the enrollment list of phase 1: an empty profile list means "the project's list applies", never "everything". A profile's `allow_repos` may only name repositories of its own provider — a pattern whose provider segment disagrees with the profile's is refused at publish time, because it could never match.
4. `cpu`/`memory`/`timeout` are validated against Cloud Run's accepted forms (the same shapes Check 4 already exercised: `internal/backend/gcp/limits.go`), so a policy cannot name a job the API will refuse.
5. `policy publish` writes the profiles; `project show` prints them with the jobs each maps to.

**Tests:** `TestPublishRefusesAnUndefinedProfile`; `TestPublishRefusesAProfileWithoutAProvider`; `TestPublishRefusesAnAllowRepoPatternOfAnotherProvider`; `TestProfileDefaultsToStandard`; `TestProfileAdmissionFallsBackToTheProjectList`; `TestProfileRefusesAnInvalidResourceForm`.

**Command:** `GOFLAGS=-p=2 go test ./internal/projectpolicy/... ./internal/config/... -count=1`. **Depends on:** 1.1, 1.6. **Commit:** "projectpolicy: profiles, and a workflow's selection of one".

### Task 3.2: the project job module and the broadened bucket IAM — SECURITY-SENSITIVE, independent opus review

**Files:** `deploy/terraform/gcp/modules/project/{job.tf,sa.tf,iam.tf,variables.tf,outputs.tf}` (new), `deploy/terraform/gcp/roots/installation/main.tf` (+ `variables.tf`), `deploy/terraform/gcp/modules/installation/{iam.tf,bucket.tf,history.tf,outputs.tf}`, `deploy/terraform/gcp/modules/installation/variables.tf`, `internal/backend/gcp/names.go` (+ `names_test.go`: `ProjectJobName`/`ParseProjectJobName`), `internal/infra/tf/testdata/golden/*.plan.json`, `scripts/gen-golden-plans.sh`, `internal/infra/generated_root_test.go`, `SECURITY.md`, `docs/design/bucket-iam.md`.

**The token exposure is ruled, not open (finding 6; D13, ruled 2026-10-15).** This task changes the `runs/` bucket grant, and that grant also exposes `runs/<slug>/<run>/budget-token` (`internal/budget/token/object.go:15`) — another queued run's RTDB identity. **The owner accepted that on 2026-10-15 (R1)**: the module ships the grant with the exposure documented in SECURITY.md beside D4, Task 3b.1 designs the channel that removes it, and repositories that must not share a budget identity use separate profiles meanwhile. There is **no gate**: this task's steps 2 and 5 are the accepted shape, and the per-profile-isolation alternative the review raised is the mitigation the ruling names, not a shape to wait for. The task still names the decision explicitly, so the reader sees why the condition cannot be narrowed to a profile prefix yet.

**Verified today:** a workflow's job service account, its per-slug bucket grant and its secret grants are `deploy/terraform/gcp/modules/workflow/sa.tf:5,24-33,36-43`; the job is `.../workflow/job.tf:15` (deletion protection) with the launcher's `job_runner` grant at `:94-102`; the launcher role's permissions are `.../modules/installation/iam.tf:26-31` (`run.jobs.run`, `run.jobs.runWithOverrides`); the history job's whole-bucket read is `.../modules/installation/history.tf:163-169`; the bucket prefixes and the condition builder are `internal/backend/gcp/names.go:374` (`JobBucketPrefixes`) and `:383` (`BucketCondition`), with the launcher's own condition at `:424` (`LauncherBucketCondition`).

1. The new module creates, per (profile, provider, account) — the provider being the one the profile pins (Task 3.1 step 2) — **one Cloud Run job** (the base image, the profile's cpu/memory/timeout, the profile's secret mounts, deletion protection as today) and **one job service account** with the bucket grant, the secret grants for the profile's mounts and (under Vertex) the same optional grant as today. The job's env carries what phase 1 left in env and nothing per-repository: `FUGARO_RUN` (an override at launch), the timeout, `FUGARO_RTDB_URL`, `FUGARO_FIREBASE_API_KEY`, `FUGARO_PROJECT`, `FUGARO_BUCKET`. **The provider is not an env var** (finding 4; the earlier draft put it there and that was wrong — `$FUGARO_GIT_PROVIDER` is exactly the launcher-overridable source 12a item 2 forbids, `internal/cli/exec.go:69`, and a launcher can override any env var through `run.jobs.runWithOverrides`, `deploy/terraform/gcp/modules/installation/iam.tf:30`, `internal/backend/gcp/run.go:108-112`). It is **encoded in the job's own name**, which Cloud Run sets and no launcher writes: `gcp.ProjectJobName(profile, provider, account)` → a stable, readable, hashed name of the shape `fugaro-p-<profile>-<provider>-<account>-<hash>`, built with `derive` like `JobName` (`internal/backend/gcp/names.go:130`) so the 63-character limit and the collision behaviour are the same. **There is no `ParseProjectJobName`, and the earlier draft's "the parse is the inverse" was not implementable** (R2-5): `derive` truncates the readable part (`:101-112`), profile names hold hyphens, an api-key job's account segment is empty, and `fugaro-p-` is also the legacy stem of an owner named `p` (`stem`, `:122-124`) — so the name is written, never read back. The runner holds the (profile, provider, account) set the policy defines, **computes every name and matches** `CLOUD_RUN_JOB` exactly (Task 3.3 step 1); the tests here assert the name function's own three properties: it is **stable** (same triple ⇒ same name), it is **distinct** over a table of triples that a shape test would confuse (a hyphenated profile, an empty account, a long profile, a triple whose readable part is truncated), and a legacy `fugaro-<slug>-<wf>` name is **never equal to** any computed project name for the fixture policy. The name's length limit (`internal/backend/gcp/names.go:130`'s `derive` hashing) is what the hash segment is for.
2. **The bucket condition loses the slug**: one condition per prefix (`runs/`, `cache/`, `locks/`), built by a new `ProjectBucketCondition(bucket, prefixes)` beside `BucketCondition` (`internal/backend/gcp/names.go:383`) so the old per-slug builder stays for the per-repository path until Task 4.4 drops it. **SECURITY.md and `docs/design/bucket-iam.md` state the regression in the words D4 chose**: it is a loss of a documented 0.7.0 property, a run could poison another repository's cache (cross-repository code execution on its next run), forge or steal a lock, or rewrite a queued `task.json`, and **repositories that must stay apart use different profiles** (a profile is a job, so a profile boundary is a job-service-account boundary). **Two things the D4 paragraph must not omit because they are not in D4's accepted list** (finding 6): the same prefix exposes **another queued run's budget token** (`runs/<slug>/<run>/budget-token`, `internal/budget/token/object.go:15`) — which is an RTDB identity, not a cache entry — and it lets a run **rewrite another run's `launch.json`/`result.json`**, which is the reason `CheckRunExecution` exists at all (`internal/backend/gcp/run.go:88-91`, Task 3.4). The first was an owner decision, **D13, ruled on 2026-10-15 (R1): accepted, documented in SECURITY.md beside D4, with Task 3b.1 designing the channel that replaces it and separate profiles as the interim boundary** — this step therefore states it as an accepted regression, not as provisional text, and there is no gate. The second is handled by Task 3.4 and stated in SECURITY.md here.
3. Launcher and operator grants move from per (repository, workflow) to per project job (`modules/project/iam.tf`), with the same `job_runner` role and the same `run.jobs.runWithOverrides` permission — nothing widens: every launcher already holds every job (design §4.1's table, "Nothing: every launcher already holds every job").
4. The history job and its sweep (`.../modules/installation/history.tf:174-253`) are unchanged (it already reads the whole bucket), and the per-project base-image check is unchanged: clone repositories need no check (`internal/infra/spec.go:846`), the inert per-repository registry stays until Task 4.2.
5. **Golden plans** (`internal/infra/tf/testdata/golden/`): regenerate with `scripts/gen-golden-plans.sh` (it runs `terraform show -json` offline with a fake token and a dead proxy, and derives the `*-adopt` variants with `jq`) and add the new root's plan. **The test that underpins the whole phase is a golden test asserting the project root's resource set**: one job and one SA per (profile, provider, account), three bucket conditions, no per-slug condition. A reviewer reads the golden diff, not the HCL.
6. **The size claim the design makes** (design §13: the current model exceeds the default service-account quota at about 60 repositories, the project-job model does not) gets a test: the golden plan's SA count is asserted to be a function of the profiles and accounts, not of the repository count.

**Command:** the repository's Terraform checks through `fugaro verify test` (the golden-plan test is a Go test: `GOFLAGS=-p=2 go test ./internal/infra/... -count=1`), plus `terraform validate`/`plan` for the new module **offline** (no apply, no credentials — the generator's own `env -i` pattern). **Depends on:** 3.0 (a probe failure can re-scope this), 3.1, 1.6. **Commit:** "tf: one project job and job service account per (profile, provider, account)".

### Task 3.3: the runner's binding, the remote and the admission — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/runner/{projectjob.go (new),runner.go,provision.go,project.go}` (+ tests), `internal/backend/backend.go` (the accessor the runner uses), `internal/runner/{lockcache.go,budget_session.go}` (the ordering comment).

**Verified today:** the runner clones from `r.d.Remote` (`internal/runner/runner.go:789-794`; `internal/runner/provision.go:30-44` is `RemoteEnv = "FUGARO_REMOTE"`, set at `internal/infra/spec.go:669` only for `checkout: clone`); the slug arrives in `FUGARO_RUN` (`internal/cli/exec.go:157`,`:168`); `task.json` carries repository, ref, task, workflow, account and layer but **no provider** (`internal/task/task.go:45-79`); the token is minted for `spec.Repo` (`internal/runner/runner.go:586`); `CLOUD_RUN_JOB` is read by `backend.ExecutionFromEnv` (`internal/backend/backend.go:96-104`), wired at `internal/cli/exec.go:134` — **the design's §12a item 1 is wrong that the runner reads it directly today**; this task makes the derivation real.

**Interfaces.** Consumes: 3.1's profiles, 1.1's `MatchRepo`, `task.Slug` (`internal/task/task.go:179`), `gcp.ProjectJobName` and `gcp.JobName` (`internal/backend/gcp/names.go:130`). Produces: `func (r *run) jobIdentity() (profile, provider, account string, err error)` — the **triple**, not the pair (R2-11) — and the extended `admissionCheck` of Task 1.5.

1. **The identity is computed and matched, never parsed** (R2-5 — the earlier draft's `gcp.ParseProjectJobName`, "the inverse" of the name, cannot be implemented and is gone). A name like `fugaro-p-<profile>-<provider>-<account>-<hash>` is not invertible: `derive` truncates the readable part to Cloud Run's 63-character limit (`internal/backend/gcp/names.go:101-112`), which can cut the account or the hash off; profile names contain hyphens (the plan's own example is `standard-bitbucket`); the account segment is empty for an api-key job; and `fugaro-p-` is *also* the legacy stem of any repository whose owner is `p` (`stem` = `fugaro-<owner>-<name>-<wf>`, `names.go:122-124`), so a shape test would misclassify a legacy job as a project job and halt it. `jobIdentity` therefore **computes the candidate names and matches `CLOUD_RUN_JOB` against them exactly** (12a item 1, item 2, finding 4 — the job name is still the strongest identifier available: Cloud Run sets it, a launcher cannot write it, and it is what the execution's own `job` field reports, `internal/backend/gcp/run.go:420-430`). Three outcomes, in this order:
   - **A project job.** `CLOUD_RUN_JOB` equals `gcp.ProjectJobName(profile, provider, account)` for some (profile, provider, account) in the **set the published policy defines** — every profile's own provider with its default account, plus the non-default accounts of phase 5. The match *is* the identity: the triple the name was computed from is what the run adopts, so the provider is the one the profile pins (Task 3.1 step 2) and the account is the one whose service account the job runs as. The profile must exist in the policy (**halt** with a message naming the job and the policy if it does not — a job created by an older module run against a newer policy would otherwise run with no admission at all).
   - **A legacy job.** No project job matched, **and** `CLOUD_RUN_JOB` equals `gcp.JobName(slug, workflow)` recomputed from the slug the run has bound (Task 1.5) and the workflow in the run's own `task.json` ⇒ **the phase-1 path, unchanged**: the slug binding and `allow_repos` (Task 1.5), **no profile admission, no halt**, whether or not a policy is published. This is the correction that matters most: phase 3's runner goes out to every installation at once, while repositories migrate one at a time (phase 4) and baked workflows stay per-repository until then (D3). A runner that halted "a job name that maps to no profile" would halt every per-repository job on every installation that has published a policy — including `belong` — and would make phase 3 **unmergeable**, contradicting nothing less than the audit's own claim that per-repository jobs keep working. A legacy job keeps working *because the recomputed name matches*, and `doctor` (Task 1.6 step 4) is what tells the owner which jobs are still legacy.
   - **Neither ⇒ halt.** A name that matches no computed project job and is not this run's legacy `JobName` is a job the runner cannot place: it may be another installation's, a stale name from a module the policy no longer describes, or a hand-created job — none of which the run may adopt an identity from. The message names `CLOUD_RUN_JOB`, the bound slug and the workflow, and the fix is `fugaro doctor`/`fugaro init --firebase` with the current version. **This is the branch that makes the match total**: an empty `CLOUD_RUN_JOB` (a `docker run`, a local invocation) lands here too, which is correct — the identity that admission and the bucket grant rest on cannot be absent on a job that has a policy.
   The set is computed **once**, from one policy read (memoized, Task 1.3 step 5), and its size is the plan's own cost note: a project with N profiles and M accounts computes N×M names and compares strings — no regex, no prefix test, no ambiguity between `p` the owner and `p-` the project marker.
2. **The slug is bound, and the provider comes from the job, not the env** (finding 4). On a project job the provider is the one the matched candidate carries — that is, the provider the profile pins (step 1) — `FUGARO_GIT_PROVIDER` is never consulted, and a `--provider` flag that disagrees is refused, because both are launcher-writable. On a legacy job, phase 1's derivation stands (Task 1.5 step 2: the provider kind whose `task.Slug` of the repository equals the slug). Then `slug == task.Slug(provider, spec.Repo)`, and the check runs **before the call to `openProvider` at `internal/runner/runner.go:833-849`** — not "before `:586`", which is that function's definition — and before the budget session takes a lease (`internal/runner/budget_session.go:180`). The phase-1 ordering check (Task 1.5 step 1) already put this before the token; this task extends it to the profile case and keeps the same place.
3. **The remote comes from data, not from the job env**: the clone URL is computed from `task.json`'s repository, the job's provider and the policy (a custom clone URL lives in the policy, design §4.1(1)), reusing `internal/infra/spec.go:454`'s `repoURL` logic moved to a shared helper so the deploy-time and run-time answers cannot differ. `FUGARO_REMOTE` stops being read on a project job; `provision.go:30-44`'s constant stays for the per-repository path until Task 4.4.
4. **Admission** (design §4.1), **on project jobs**: the runner refuses a repository the profile's `allow_repos` does not admit, for the (profile, provider, account) it is actually running on — so the repository's `fugaro.yaml` selection of a profile cannot widen its own access, and the launch check stays advisory (§4.2). One function, one message, shared with the CLI's advisory twin (`internal/cli/run.go`, Task 1.5 step 4). A repository whose provider the profile does not serve is refused here with a message naming both (Task 3.1 step 2).
5. `task.json`'s repository is *not* trusted to name the slug: it is the input the binding compares, and the comparison is what makes the bucket condition's absence survivable.
6. **The GitHub App becomes the project's, not the repository's — and its key never becomes part of the policy node** (design §4.1(3), §4.5): the project names one App, and the runner mints a token per repository from it, so a project is tied to one App and a project with several Apps cannot share jobs (SECURITY.md says so; `SECURITY.md:48` already tells users to use one App per repository if that matters). **The private key stays a Secret Manager secret**: `github-app-key` (`internal/config/config.go:387`) mounted through `internal/infra/spec.go:555`, and on a shared job it is granted to the project job service accounts by Task 3.2's module exactly as the per-repository path grants it today. **It is never a field of `config/policy/shared`**: every run token can read that node (P2) and 12a item 7 is precisely about not putting secret-adjacent data where a launcher's IAM read reaches it — a key in the policy would hand a prompt-injected agent the App's credentials for every enrolled repository. What the policy carries is the App's **ID and the secret's name**, the rule Task 1.6 step 2 already applies to provider keys ("the secret's name, never a value"); the ID is not secret-adjacent, and the runner reads it from the policy rather than from an env var, for the same reason Task 3.2 step 1 keeps the provider out of the env (finding 4) — an overridden App ID is not a privilege escalation, but it is a confusing failure the policy can simply prevent. The per-repository path keeps `github-app-key` as it is until Task 4.4, and `TestPolicyCarriesNoSecretValue` (Task 1.6) covers the App key's bytes like any other secret.

**Tests**, all written against the computed-and-matched rule (R2-5): `TestJobIdentityMatchesAComputedProjectJobName` (the triple is the one `ProjectJobName` was called with — **the round-trip the earlier draft tried to get by parsing**, now asserted by computing both sides), **`TestJobIdentityIsNotAParser`** (a table of names a shape test would misread: a two-hyphen profile `standard-bitbucket`, an api-key job with an empty account segment, a name truncated at the 63-character limit, and a **legacy job of a repository whose owner is `p`** — `fugaro-p-…` as `stem` builds it (`names.go:122-124`) — must **not** be taken for a project job; the same four names as genuine project jobs **must** match), `TestJobIdentityRefusesAnUnknownProjectJob` (a computed shape naming a profile the policy does not define ⇒ halt), `TestLegacyJobNameTakesThePhaseOnePath` (the recomputed `gcp.JobName(slug, wf)` with a policy published: binding and `allow_repos` only, **no halt** — the finding-5 regression test), `TestJobIdentityRefusesANameItCannotPlace` (neither a computed project job nor this run's legacy name, and an empty `CLOUD_RUN_JOB`), `TestProjectJobOfAnotherProviderIsRefused`, `TestRunnerRefusesSlugNotInProfileAllowList`, `TestRunnerChecksBeforeTheTokenAndTheLease` (extended; asserts the refusal precedes the call at `internal/runner/runner.go:838`/`:848` and that no credential request and no reserve write happened), `TestRemoteFromTaskAndPolicy` (including a custom clone URL), `TestProjectJobIgnoresFUGARORemote`, `TestProjectJobIgnoresTheProviderEnvVar`. Each refusal test asserts the *absence* of a token request and a reserve write in the fake, which is the mutation that matters. **The tests are written first and fail**: before the change, `jobIdentity` does not exist, `gcp.ProjectJobName` does not exist, and `TestLegacyJobNameTakesThePhaseOnePath` cannot compile — so the red state is real rather than asserted.

**Command:** `GOFLAGS=-p=2 go test ./internal/runner/... ./internal/backend/... -count=1`. **Depends on:** 3.1, 3.2 (the job names it parses), 3.0 step 3. **Commit:** "runner: bind the slug to the job's own repository and admit the profile's repositories".

### Task 3.4: every job-naming caller

**Files:** `internal/backend/gcp/{run.go,names.go,logs.go}`, `internal/cli/{run.go,runs.go,logs.go,cancel.go}`, `internal/gcpfake/` (+ tests).

**Verified today:** `CheckRunExecution` is `internal/backend/gcp/run.go:92-100`, `Launch` is `:106-140`, the job path `:30-45`, execution parsing `:47-80`, and the execution's own env is read at `:420-430`; the CLI's `max_parallel` counts `fugaro-` jobs at `internal/cli/run.go:1084-1112`; the job names are `internal/backend/gcp/names.go:130` (`JobName`).

1. `Launch` launches the project job for the (profile, provider, account) the workflow selects, and sets `FUGARO_RUN` as it does today (the override, `:110`).
2. **`CheckRunExecution` compares the execution's own `FUGARO_RUN`** (already read from the execution, `:420-430`) with the run it belongs to, and with the repository the caller asked for — "which must compare the execution's own `FUGARO_RUN`, not the job's name" (design §4.1). This is what keeps `logs`, `cancel` and `ls` honest now that several repositories share a job.
3. `fugaro ls`/`runs`/`logs`/`cancel`: selection stays by execution name and by the `FUGARO_RUN` the execution reports, and `max_parallel` (`internal/cli/run.go:1084-1112`) keeps counting `fugaro-` jobs at the same horizon — but its per-job assumption is restated: with shared jobs, one job's executions are several repositories', so the count is by execution, not by job.
4. `internal/gcpfake`: the fake learns project jobs (a name → (profile, provider, account) mapping, and the override merge), because every runner and CLI test in this phase runs against it.
5. `docs/`: the `logs`/`ls` user-facing notes that named a per-repository job.

**Tests:** `TestListAndLogsAcrossRepositorySharedJobs`, `TestCancelRefusesAnotherRunsExecution`, `TestMaxParallelCountsExecutionsNotJobs`, and the existing golden-adjacent CLI tests updated to project job names.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... ./internal/backend/... ./internal/gcpfake/... -count=1`. **Depends on:** 3.2, 3.3. **Commit:** "cli and backend: every job-naming caller on project jobs".

### Task 3.5: the launcher and operator grants, and the dashboard

**Files:** `deploy/terraform/gcp/modules/{installation,project}/*.tf`, `internal/watch/*` (+ tests), `internal/cli/doctor.go` (+ tests), `SECURITY.md`.

1. The cross-cutting list's "launcher grants": the launcher role's permissions are unchanged (`run.jobs.run`, `run.jobs.runWithOverrides`, the secret metadata list — `modules/installation/iam.tf:15-31`), and what changes is *how many* jobs and secrets they attach to. `fugaro doctor` reports the grant count it sees and the jobs a launcher can start, which is the surface the owner checks after a migration.
2. `fugaro watch`/`watch --json`: a run's job is now a (profile, provider, account) name, so the JSON gains `profile`, `provider` and `account` beside the job; the TUI's grouping stays by repository (from `FUGARO_RUN`), which is what a person reads.
3. SECURITY.md's D4 paragraphs and the launcher-override paragraph (Task 1.5) are in place before this task closes the phase, and the phase's own `docs/design/bucket-iam.md` table of what each service account may do is regenerated.

**Tests:** `TestWatchJSONCarriesProfileAndAccount`, `TestDoctorReportsLauncherReach`. **Command:** `GOFLAGS=-p=2 go test ./internal/watch/... ./internal/cli/... -count=1`. **Commit:** "watch, doctor and the grants: what a project job changed".

### Task 3.6: `cloneShipped` flipped, and the docs that gate on it

**Files:** `internal/cli/recipes_skew.go`, `internal/cli/{docs_checkout_test.go,image_refresh_steps_test.go,init_repo_clone_test.go}`, `docs/checkout.md`, `docs/git-providers.md`.

**Verified today:** `cloneSince = "0.7.5"` at `internal/cli/recipes_skew.go:44` (the rename from `0.7.1` is #370, merged on `main` with the 0.7.5 release, R4) and `cloneShipped = false` at `:70`, with three tests pinning the docs/gate agreement (`internal/cli/docs_checkout_test.go:123-132`, `image_refresh_steps_test.go:662`, `init_repo_clone_test.go:19`).

1. **The flip is gated on Check 38** — #360's Task 18, the owner-run check that proves `checkout: clone` end to end; it is on this phase's critical path and the design says the flip PR follows Check 38 (`docs/design/project-centric.md` §10). This task records the evidence (the check's run date and its FACT lines) in the PR description, not in the code.
2. Flip `cloneShipped` to `true`, update `docs/checkout.md` and `docs/git-providers.md` to say clone workflows are shipped and what they need, and let the three gating tests pass — they fail if the flip and the docs disagree, which is the point of Review Focus 12.
3. `docs/git-providers.md`: the Bitbucket section gains the D5 outcome from Task 3.0 step 2 — either a shared project-wide token and its scope, or the statement that Bitbucket stays per-repository and why.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1` and `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`. **Depends on:** #360/Check 38 (**outside this repository's control**), 3.2, 3.3, 3.4. **Commit:** "checkout: clone is shipped (cloneShipped), and the docs say what it needs".

### Task 3.7: Check 53 — OWNER-RUN

Append `## Check 53: a shared project job, end to end (installation fugaro and the sandbox only, never belong; run by you; NOT RUN)` and its row — the design's phase 3 live check (§11): *"Live check on the Bitbucket sandbox, plus a GitHub repository for the GitHub-specific parts (one has to be created; `dimipaun/fugarosandbox` is Bitbucket)"*. Steps, guarded with **Check 43's block verbatim** (Task 0.4 step 1):

1. Setup and guard; record the restore (the sandbox's workflow/job names, its service account's bucket condition, its launcher grants, the workflow's `profile`, and whether `cloneShipped` is true in the installed binary).
2. **The GitHub half needs a repository that does not exist yet** (design §4.4): the owner creates a GitHub sandbox repository under the App, adds it to `allow_repos`, and everything below runs once on it and once on the Bitbucket sandbox. If no GitHub repository is created, the check runs the Bitbucket half and **says so**; the GitHub-specific claims (the App token minted per repository) stay open.
3. **The project root applies** (OWNER-RUN Terraform): `fugaro init`'s installation root with the new `modules/project`, on installation `fugaro`. **FACT:** the resource counts (jobs, service accounts, bucket conditions) and that the per-repository root is **not** applied.
4. **A clone workflow runs on the shared job**: `fugaro run` on the sandbox, `checkout: clone`. **FACT:** the job name, the execution's own `FUGARO_RUN`, the clone URL the runner used and where it came from, the profile it ran on, and the run's outcome.
5. **The binding refuses a mismatch**: launch with a `FUGARO_RUN` override naming another slug, and (a second run) with a repository the profile's `allow_repos` does not admit. **FACT:** both refusals verbatim, and that no token was minted and no lease taken (the absence of the credential and reserve lines).
6. **The regression, measured**: from the sandbox job's service account, `gcloud storage cat` of **another** slug's `cache/` object succeeds (the property D4 accepted). **FACT:** the object read, and one sentence confirming SECURITY.md's wording matches it. This step is deliberately written as a test of the documented regression rather than a test that it does not happen.
7. **`ls`, `logs` and `cancel` on a shared job**: **two enrolled repositories the check names — the Bitbucket sandbox and the GitHub scratch repository step 2 creates, both non-production and neither in `belong`** (R2-6) — run on the same job, then `fugaro ls`, `fugaro logs <run>` and a `fugaro cancel` of the wrong execution. **FACT:** each command's selection, and that cancel refuses an execution that is not the run it was asked for.
8. Restore: the sandbox back to its per-repository path if the owner wants it (the per-repository root is still applied in phase 3), the launcher grants and bucket conditions as recorded, one ordinary sandbox task.

**Paste back:** every FACT line, never a token or key. **Depends on:** 3.0 to 3.6.

## Phase 4 — enrollment and migration (six tasks)

The design's §4.3, §4.4 and D3. **This phase is where the owner's own installations move**, and where the two things the design says the plan must add on top of #344 land: the enrollment commands and **"retire a repository root"** (design §4.4: config-transitions' engine covers a changed `fugaro.yaml`, not the retirement of a whole repository root with `prevent_destroy` secret containers and a registry — its plan Tasks 1 to 5 do not cover full offboarding; D8 keeps that work to the core, which #365 implements).

### Task 4.1: `project sync` and `project repos` — enrollment as data

**Files:** `internal/cli/project_enroll.go` (new) + tests, `internal/cli/project.go` (the existing checkout/project helpers it sits beside, `checkoutProject` at `:30`), `internal/infra/discover*.go` (the App's installed repositories), `docs/project-layer.md`.

**Interfaces.** Consumes: 1.6's `newProjectCmd()` and the published policy, the bucket's `runs/<slug>/` prefixes (`internal/backend/gcp/names.go:374`), Firestore's `spendDaily` (the durable sources the design names, §4.3), `internal/gitprov/github`'s App authentication. Produces: `sync`, the fuller `repos ls`, and the `repos add`/`repos rm` pair 1.6 already shipped.

1. `project repos`: lists **enrolled** (the allow-list, from the policy) against **seen** (the bucket's `runs/<slug>/` prefixes and Firestore `spendDaily`), each with its canonical name and provider. **No registration record is written** (design §4.3): the set of repositories that have run is derivable, and the RTDB ledger is pruned after about 8 days, so the bucket and Firestore are the sources.
2. `project sync`: lists the GitHub App's installed repositories (the App API, with the project's App credentials) and offers the ones the allow-list does not name, **one confirmed batch**, writing the allow-list through the CAS path 1.6 uses. It never removes anything.
3. `project repos add <provider>/<owner>/<name>` is a one-line data write (shipped in 1.6 for the interim path of 12a item 3) and `project repos rm` its inverse; both print the resulting allow-list.
4. The interim path is closed: after this task a new repository needs **no Terraform** and no `init --repo` for a clone workflow, and `docs/project-layer.md` says the three things that tie a repository to a project (its `project:`, the allow-list, the App's installation) in one paragraph (design §4.3).

**Tests:** `TestProjectReposListsEnrolledAndSeen` (the seen set from a bucket fake and a Firestore fake), `TestProjectSyncAddsOnlyTheMissingOnesAndConfirmsOnce`, `TestProjectRemoveRepoStopsAdmittingTheRepo` (the runner refuses on the next run, via the 1.5/3.3 check).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... ./internal/infra/... -count=1`. **Depends on:** 1.6, 3.3. **Commit:** "project: enrollment as data, and the repositories the installation has seen".

### Task 4.2: retire a repository root

**Files:** `internal/cli/init_repo_stage.go`, `internal/cli/init.go` (+ tests), `internal/infra/forget.go`, `internal/infra/{repo.go,workdir.go}`, the config-transitions retirement engine (`internal/infra/retire*.go`, per #365's core), `docs/design/config-transitions.md` (a pointer), `docs/checkout.md`, `docs/design/bucket-iam.md`.

**Verified today:** the repository root's state is `fugaro/repos/<slug>` (`internal/infra/workdir.go:19-23`, `internal/infra/repo.go:38-40`); what it owns is a secret container per logical secret with `prevent_destroy` and two operator grants (`deploy/terraform/gcp/modules/repo/secrets.tf:4-42`), an Artifact Registry repository (`.../repo/registry.tf:10-72`), a build service account (`.../repo/build.tf:5-93`), optionally a check job and scheduler (`.../repo/check.tf:16-142`), and per workflow variant a job service account, the Cloud Run job and its grants (`.../modules/workflow/{sa.tf,job.tf}`).

1. A new command (`fugaro init --repo --retire`, following config-transitions' own Task 5 spelling) retires a repository **root**: it computes the retirement set from the state and the labels Fugaro writes (`gcp.LabelManaged`, `gcp.LabelRepo`), removes the module from the root's state, then deletes the labelled jobs, service accounts, IAM bindings and registries **through the API, not through a `destroy`** — because `prevent_destroy` on the secret containers means a destroy cannot run, and the design says plainly that **secret values cannot be carried over**: no person can read them (`.../repo/secrets.tf:31-42`), so a project-scope secret is re-entered (Task 4.5 step 3).
2. **A safety property the plan adds**: the command refuses to retire a repository whose slugs another repository's resources still reference (a shared profile job, a launcher grant, an operator grant), and it prints the allow-list entry it would remove — **retiring a root never un-enrolls the repository by itself** (that is `project repos rm`, an explicit act). The ordering it enforces: un-enroll → retire the root → the repository's runs stop (design §4.4's "re-run init under the new model": deploy the project jobs, switch launches, then retire each repository's root).
3. `internal/infra/forget.go`'s existing forget path is reused for the state half rather than duplicated; the labels half is the new code.
4. `docs/checkout.md:81`'s inert per-repository registry paragraph is replaced by "a clone repository has no root at all" once 4.3 lands.
5. **A test that no retirement deletes a resource another repository needs**: a golden-plan test over the retirement set, plus `TestRetireRefusesASharedProfileJob` and `TestForgetKeepsTheStateOfUnrelatedRepos`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... ./internal/infra/... -count=1`. **Depends on:** #365's core (merged or merged into this branch first), 3.2. **Commit:** "init --repo --retire: retire a repository's whole root, state removal then labelled deletes".

### Task 4.3: `init --repo` retired for clone repositories

**Files:** `internal/cli/{init_repo_target.go,init.go,init_repo_stage.go}` (+ tests), `internal/infra/spec.go`, `docs/checkout.md`, `docs/project-layer.md`.

1. For a `checkout: clone` repository, `init --repo` becomes: the local `repos:` entry (unchanged) **plus** an allow-list line — it applies **no Terraform** and asks **no confirmation** beyond the one write, which is the spec's "adding a repo must be lightweight: no Terraform, no confirmations" (spec §4.2). Where it used to create a registry, a build service account, secret containers and a job, it now does nothing of the kind.
2. **This is where the design supersedes #343's repository-root grant wiring and the `ExtraSecrets` union** (design §10): with no repository root for a clone repository, a project secret's grant is an installation-root grant, and the union the repository root computed is no longer needed on this path. #343's machinery stays for the per-repository path (baked workflows, until 4.4) — the code is not deleted, the path is.
3. `fugaro init --repo` on a **baked** workflow keeps today's behaviour until 4.4 (Task 4.4's ordering), and prints one line saying so.
4. `docs/checkout.md`: a clone repository's onboarding is now "commit `project:` and `checkout: clone`, run `fugaro init --repo` once, run `fugaro project repos add`" — with the note that only the App's installation remains as a provider-side step.

**Tests:** `TestInitRepoCloneWritesNoTerraform` (the fake Terraform runner sees no apply and no plan), `TestInitRepoCloneAddsTheAllowListEntry`, `TestInitRepoBakedStillPlans`, plus the existing `init_repo_clone_test.go` gates.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1`. **Depends on:** 4.1, 4.2. **Commit:** "init --repo: a clone repository needs no Terraform at all".

### Task 4.4: baked workflows, dropped after phase 4 (D3)

**Files:** `internal/cli/{init_repo_stage.go,init.go}` (+ tests), `docs/checkout.md`, `docs/base-image.md`, `SECURITY.md` (a pointer), `plugin/skills/setup/**`.

**The design's ruling:** "phases 3 and 4 support clone workflows only; baked workflows stay on the existing per-repository path, unchanged, until phase 4 ends, and are then **dropped** … or **re-implemented as per-repository jobs created through the Cloud Run API**, adopted by labels, with no Terraform (the `image refresh` precedent)" — owner decision **D3: dropped after phase 4**.

1. `fugaro init --repo` refuses a baked workflow **with the way out in the message**: `checkout: clone` (measured at a 10.5 s median cold start, Check 41, design §4.4) or a recipe, since "the base image carries every runtime".
2. `fugaro doctor`: a `baked-workflows` check that lists any workflow still baked in a repository the installation knows, as a `warning` naming the migration, so an owner finds them before a run fails.
3. The per-repository code path stays in the tree but is unreachable for new deployments; **the API-created-job alternative is explicitly deferred, not built** — if an owner needs a baked workflow after this phase, that is a new design (§4.4's second option), and the plan says so rather than leaving it implied.
4. `docs/base-image.md` and `docs/checkout.md`: what "baked" was, why it is gone, and what replaces it.

**Tests:** `TestInitRepoRefusesABakedWorkflow` (message names `checkout: clone` and the base image), `TestDoctorListsBakedWorkflows`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1`. **Depends on:** 4.2, 4.3. **Commit:** "baked workflows are dropped; clone and recipes are the way".

### Task 4.5: the owner's migration of installation `fugaro` — OWNER-RUN, Check 54

**Files:** `docs/gcp-live-checklist.md` (and, if the migration exposes a gap, a follow-up task in the same PR — never a silently-skipped step).

Installation `fugaro` holds `dimipaun/fugaro` (GitHub, baked, with a check job), `edgeappinc/fugarosandbox` (Bitbucket, baked, no check job) and `dimipaun/fugarosandbox` (Bitbucket, listed in the local config with no workdir on this machine: stale or onboarded elsewhere, UNVERIFIED — design §4.4). **No agent touches `belong`** (which holds `edgeappinc/edgeserver` and `edgeappinc/edgeweb`): moving it is the owner's own work after the model is proven on `fugaro`.

Append `## Check 54: migrate installation fugaro to project jobs (installation fugaro, sandbox first, never belong; run by you; NOT RUN)` and its row:

1. Setup and guard — **Check 43's block verbatim** (Task 0.4 step 1), and this check's own name list is a `never belong` list: every repository it names is the sandbox or a scratch repository the owner creates for it. Record **everything** to restore: the local config file, each repository's `fugaro.yaml`, the caps, the policy, the secrets' names and scopes (**never their values**), each job's env, and the project jobs' names.
2. **Sandbox first**: `edgeappinc/fugarosandbox` converted to `checkout: clone` on a branch, `fugaro init --repo` run once, added to the allow-list, run once on a project job. **FACT:** the job it ran on, its profile, and the outcome.
3. **Secrets are re-entered, not copied** (design §4.4): the owner types the project-scope values (nothing can read the old ones). **FACT:** the names and scopes only, that `fugaro secrets ls` shows a version, and that no value appears in any log, plan or state print — never a value.
4. `dimipaun/fugarosandbox` (`UNVERIFIED`: stale or onboarded elsewhere): the owner decides from `fugaro project repos` — if it never ran, `project repos rm` and retire its root if one exists; if it is live elsewhere, it stays untouched and the FACT records the decision.
5. `dimipaun/fugarosandbox`'s sibling, `dimipaun/fugaro`: `dimipaun/fugaro` is **baked**, so it is the phase's proof that D3's refusal is survivable: convert its workflow to `checkout: clone` (or a recipe), run it once on a project job. **FACT:** the workflow's before/after `checkout`, the run's outcome, and the cold-start time against Check 41's 10.5 s median. If a workflow genuinely cannot be converted, **stop and report** — that is the signal for §4.4's deferred API path, and it is the owner's decision, not the agent's.
6. **Retire the roots** (OWNER-RUN Terraform, one at a time, sandbox first): `fugaro init --repo --retire`, then confirm no labelled job, service account, registry or secret container remains and that `gcp`'s service-account count dropped. **FACT:** the resource counts before and after, and that the runs bucket's objects were left alone.
7. Restore: put back whatever was changed, run one ordinary task on the sandbox, and confirm `fugaro project repos`, `fugaro doctor` and `fugaro ls` agree with what is deployed. **`belong` is not touched at any step** — the check's last FACT line says so explicitly.

**Paste back:** every FACT line, never a secret value. **Depends on:** 4.1 to 4.4, and 3.7 (the shared job proven first).

### Task 4.6: the phase's cross-cutting sweep

**Files:** `SECURITY.md`, `docs/{project-layer.md,checkout.md,git-providers.md,multi-model.md}`, `docs/design/bucket-iam.md`, `plugin/skills/setup/**`, `plugin/skills/working/**`, `internal/cli/{doctor.go,upgrade_cloud.go}` (+ tests), `scripts/gen-golden-plans.sh`, `internal/infra/tf/testdata/golden/`.

The cross-cutting list of design §11, carried explicitly by this task as it is by each phase's own last task: **SECURITY.md** (what a repository can now do without a root, and D4's regression restated), **user docs** (the five above), **the plugin skills** (setup asks the policy questions once per project now; working's layer section points at the policy), **`fugaro doctor`** (the checks added in 4.2 to 4.4), **`upgrade`'s drift note** (a re-run of `init --repo` is no longer how policy or enrollment changes; what remains is a mount, and after phase 3 nothing does), **the dashboard** (`watch`/`report` read a repository's job from `FUGARO_RUN`, not from the job name — Task 3.5's change, restated for the post-retirement state), **launcher grants** (the count, per Task 3.5), **version skew** (the policy version's messages, Task 1.6 step 4, and the job-image message of 12a item 6), **`fugaro validate` is offline** (its "unresolved offline" line, Task 1b.3), **`scripts/gen-golden-plans.sh` and the goldens** (regenerated for the retired roots), **`docs/multi-model.md`**, **`docs/checkout.md`** and **`docs/git-providers.md`**.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` and `fugaro verify test`. **Commit:** "docs, skills and checks: the project-centric path after phase 4".

## Phase 5 — the OAuth pool (five tasks)

The design's §7. **What changes**: a Claude OAuth token stops being a repository's secret and becomes **a person's**, referenced by the project. Residual risks the design states and this phase's docs repeat: a project job that mounts several people's tokens widens the `/proc` and metadata-server exposure of `mixed-credentials.md` §6 to every repository sharing that job, and **OAuth spend is notional and no per-person budget exists** (`internal/runstore/cost.go:62-71`). **#363 is held until this phase** (D9) and is rebased in Task 5.4.

### Task 5.1: person-owned secrets and their references — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/projectpolicy/policy.go` (+ tests, the `accounts` records), `internal/cli/accounts.go` (+ `accounts_test.go`), `internal/infra/{secretmounts_test.go,readiness.go}`, `internal/backend/gcp/names.go` (the per-person secret ID), `SECURITY.md`.

**Verified today:** tokens are per (repository, account) — `internal/cli/accounts.go:60-94` lists them, `config.ClaudeAccountSecret` derives the ID (`internal/config/claude_accounts.go:80`), and `internal/infra/spec.go:589` mounts the (workflow, account) variant's secret; `claude-oauth-token-*` is refused project scope by `claudeOAuthAccountAllowed`/`claudeOAuthAccountRefusal` (`internal/cli/secrets.go:174-204`) and is **not** swept in by D2. A secret's ID is a flat, project-wide namespace and a `secret_id` IAM grant works identically whichever root created it (`docs/design/project-secrets.md:18`), which is what makes this task Go and IAM rather than a new resource type.

1. **Storage**: one Secret Manager secret per **(person, account)** in the project's GCP project, with a new ID helper beside `gcp.SecretID`/`ProjectSecretID` (`internal/backend/gcp/names.go:143-158`) — flat IDs and per-secret grants work regardless of which root owns them (`docs/design/project-secrets.md:18`).
2. **Who creates it**: **the person** (12a item 11), with `fugaro accounts add <account>` storing their own token at person scope; the operator accepts the reference. The owner of the secret is granted `secretVersionAdder` and `viewer` on that secret only, **never `setIamPolicy`** (design §7).
3. **The grant is an operator step, and the plan says why**: granting the project's job service account `secretAccessor` on a person's secret needs `setIamPolicy`, so it happens when the reference is accepted — through the installation root (Task 5.2), not by the person.
4. **Reference record** in `config/policy/shared`: `{account, secret ID, allowed profiles}` — **no value and no identity**; the owner's identity lives in the admin-only `config/admin` node and in the export (design §5(2), §7). `fugaro accounts share <name>` proposes it after the owner confirms; an operator accepts; the owner can retire it.
5. `fugaro accounts ls` (and `--json`) shows, per account: the person, the reference's state, which profiles mount it, and — for the owner — whether their token is used by anyone else. **The run-readable node never carries an email**, and a test asserts the published `shared` node holds no `@` (Review Focus: the design's "nothing personal" rule, §5(2)).
6. SECURITY.md: the per-person model, the operator's grant step, the widening risk in the design's own words, and the notional-spend caveat.

**Tests:** `TestPersonSecretIDIsFlatAndPerAccount`; `TestShareRecordCarriesNoIdentity` (`@` scan of the published node); `TestOwnerCanRetireAReference`; `TestPersonCannotSetIamPolicy` (the CLI never calls it from a person's flow); `TestAccountsShareProposesBeforeWriting`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... ./internal/projectpolicy/... ./internal/backend/... -count=1`. **Depends on:** 3.2 (a job per (profile, provider, account)), 1.6. **Commit:** "accounts: a person's OAuth token, referenced by the project".

### Task 5.2: one job per (profile, shared account)

**Files:** `deploy/terraform/gcp/modules/project/*.tf`, `deploy/terraform/gcp/roots/installation/`, `internal/infra/tf/testdata/golden/project*.plan.json`, `scripts/gen-golden-plans.sh`, `internal/cli/project.go`.

**Verified today:** a secret env var is fixed in the job definition, so a shared account means a job per profile (design §7, "Mounting"); the `image refresh` precedent patches a job rather than creating one (`internal/cli/image_refresh_steps.go:226-261`), which is why P9 picks Terraform.

1. The phase-3 module already creates one job and one job SA per (profile, provider, account): this task adds the **non-default accounts** — one job per profile and provider for each shared account, whose service account alone may read that person's secret. The alternative the design rejects (one shared job SA fetching any token by name at boot) stays rejected for the §4.1 reason, and the code comment says so where someone would be tempted.
2. `policy publish` writes the reference; the installation root's variables gain the accounts to create jobs for (the union of the policy's referenced accounts and the operator's own), so the order is: accept the reference → apply (OWNER-RUN) → the account's runs use its job.
3. **A new shared account costs one apply per profile** — stated in the docs and in `doctor` (`oauth-pool-jobs` check: which referenced accounts have no job yet). The design's "no Terraform per addition" goal meets its one exception here, and the plan names it rather than hiding it.

**Tests:** the golden plan's job count as a function of (profiles × accounts), and `TestDoctorNamesAccountsWithoutAJob`.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/... ./internal/cli/... -count=1`. **Depends on:** 5.1, 3.2. **Commit:** "tf: one project job per profile for each shared OAuth account".

### Task 5.3: failover moves from the skill into `fugaro run`

**Files:** `internal/cli/{run.go,run_oauth*.go}` (+ tests), `internal/runner/` (the usage-limit detection point), `plugin/skills/working/reference/diagnose.md`, `internal/runstore/launch.go` (read-only), `docs/design/named-oauth-tokens.md` (a pointer).

**Verified today:** failover exists only as prose in the skill (`plugin/skills/working/reference/diagnose.md:79-97`: a loose case-insensitive match of "usage limit" in `row.reason`/`log_tail`/`agent_message`, then steps to switch accounts); the launcher's identity is recorded (`internal/runstore/launch.go:12-21`, field `LaunchedBy` at `:19`); **the design's citation "`requestedBy`" is not a symbol** — `RequestedBy` is `internal/task/task.go:57`. Claude Code's usage-limit text is UNVERIFIED as an **expired/revoked** token (that is U4 of `named-oauth-tokens.md:113`) and separately as the **usage-limit** wording (that document's §12, `:150-163`), and the design's phrase "U4" refers to the second.

1. `fugaro run` retries, on a usage limit, with the **next account**: the launcher's own accounts first, then the shared ones, with the tried-list the existing design names, and it records the switch in the run's record so `fugaro ls`/`report` show it.
2. The detection reuses the skill's own rule (the loose "usage limit" match), **and the plan adds a live check for it** rather than trusting the prose: Check 55 step 4.
3. `diagnose.md` becomes a pointer: the skill explains what a usage limit is and how to read the run's record, and says the switching is `fugaro run`'s (so the two cannot drift into two implementations).
4. `agent.oauth_token` stays as a preference (design §7, "No repository level"): it can name an account, and the per-repository copies plus `secrets set claude-oauth-token-* --repo` go away with the per-repository path.

**Tests:** `TestRunFailsOverToTheNextAccount`, `TestFailoverOrderIsLaunchersFirstThenShared`, `TestFailoverRecordsTheSwitch`, `TestNoFailoverWithoutAUsageLimit` (a failure that mentions a limit-shaped string in an unrelated place must not switch — the false-positive guard).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1`. **Depends on:** 5.1, 5.2. **Commit:** "run: the OAuth failover moves out of the skill".

### Task 5.4: #363 rebased (D9)

**Files:** whatever the rebase touches; `docs/plans/2026-10-14-oauth-reviewer-stage.md` (#363's own plan, amended in that PR or in a follow-up — never silently).

**#363 is held until this phase** (D9): the OAuth reviewer stage multiplies per-repository variant jobs, which phase 3 removes. The rebase is its own PR, after 5.1 to 5.3 land, and its plan's assumptions about per-repository variants are re-read against project jobs — the reviewer stage's own job is a (profile, provider, account) job, and its Check 47 runs after this phase, not before. **This task is a pointer with a rule**: it does not merge #363's code into this phase, and nothing in phases 0 to 4 waits for it.

**Depends on:** 5.3. **No test of its own** (the rebase carries its own).

### Task 5.5: Check 55, and the phase's docs — OWNER-RUN for the check

**Files:** `docs/gcp-live-checklist.md`, `SECURITY.md`, `docs/multi-model.md`, `plugin/skills/setup/**`, `internal/cli/{doctor.go,report.go}`, `internal/watch/*`.

Append `## Check 55: the OAuth pool (installation fugaro and the sandbox only, never belong; run by you; NOT RUN)` and its row:

1. Setup and guard — **Check 43's block verbatim** (Task 0.4 step 1); record the state (the accounts, their secrets' scopes, the runs' `credential`, the chosen workflow).
2. **The owner types their own token** (`fugaro accounts add`); no agent sees it. **FACT:** the secret's ID and that a version exists — never the value.
3. Reference and grant: `fugaro accounts share`, the operator's acceptance, the apply, then one sandbox run using the shared account. **FACT:** the job it ran on, the profile, that the job's SA read only that person's secret (the IAM policy read back, secret IDs only), and the run's outcome.
4. **The failover, for real**: a run whose account hits a usage limit. **FACT:** the detector's own line (what matched), the account it switched to, and whether the second account's run succeeded — the design's U4 question, answered.
5. The notional caveat: the run's `cost` record for an OAuth stage. **FACT:** that the spend is recorded as notional with `credential: oauth` and that no per-person budget exists.
6. Restore: the reference retired (the owner's choice), the account's token left in place or disabled by the owner, one ordinary sandbox task.

**Paste back:** every FACT line, never a token. Plus the phase's docs sweep, as Task 4.6's list, with SECURITY.md's OAuth section and the dashboard's notional column.

**Depends on:** 5.1 to 5.3.

## Phase 6 — the backing Git repository (four tasks)

The design's §8. **`fugaro project export` writes a record, not a unit of execution**: the backing repository is never enrolled and never runs. What it lets one reconstruct is the project's configuration and policy and an account of spend over time — **not** the project end to end: secret values must be re-entered, and Firestore history, run records, the GitHub App and the Firebase project itself are not in it (design §8, stated in the doc and in `doctor`).

### Task 6.1: `fugaro project export` — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/cli/project_export.go` (new) + tests, `internal/cli/project.go`, `internal/report/report.go` (read-only reuse), `internal/watch/json.go` (a reader), `internal/budget/model.go` (the paths it reads), `docs/project-layer.md`.

**Verified today:** `report.Build` is `internal/report/report.go:159` and is called from `internal/cli/report.go:170,393`; the `watch` JSON document is `internal/watch/json.go:148` (`BuildJSON`); the RTDB cap and spend paths are `internal/budget/model.go:51-80` (`PathCapsRepo`, `PathSpendGlobal`, `PathSpendRepo`, `PathSpendRun`); the shared bucket config is read through `internal/blobx`/`internal/cli/sharedcfg.go`.

**Interfaces.** Consumes: the policy (1.1), the caps' paths, the bucket's `fugaro/` objects, Secret Manager's metadata list, `report.Build`, the local operator config. Produces: a directory tree and a `--push`-able commit.

```go
// Layout (design §8). Nothing here holds a secret value (P10).
policy/          config/* as JSON: caps (global, defaults, per-repo), mode, limits, kill switches with when and why
policy/shared.json  the policy object, admin nodes included only under --with-admin
config/          the bucket's shared config, project layer and recipes, verbatim bytes
config/operator.yaml  the operator's local config without identity and endpoints; secrets by NAME only
inventory/       secrets metadata (names, scopes, versions' create times), enrolled repositories, project jobs
spend/           Firestore spendDaily (final days only, so history is never rewritten) and per-run CSVs from result.json
```

1. **Final days only** for `spendDaily` (design §8): the current day is still being written, so an export never rewrites a number that later changes.
2. **People are omitted by default** (P10): no `byPerson` spend, no kill-switch author. `--with-people` includes them and its help text says they are committed to a Git repository.
3. **No value, ever**: the secret inventory reads metadata only (`secretmanager.secrets.list`/`versions.list` — the launchers already hold it, `deploy/terraform/gcp/modules/installation/iam.tf:15-19`), and `TestExportContainsNoSecretValue` scans every byte of the output for every value the fake Secret Manager holds plus the run's own token.
4. `--json` prints the tree's manifest, so a caller can diff two exports without reading files; the human output prints per-file counts and the export's own `checked_at`.
5. Idempotent and deterministic: same input → byte-identical output, including ordering (sorted keys and sorted rows), so a diff shows a policy change and not a map iteration.

**Tests:** `TestExportContainsNoSecretValue`; `TestExportOmitsPeopleByDefault` and `TestExportWithPeopleIncludesThem`; `TestExportDeterministic` (two runs, byte compare); `TestExportSpendSkipsTheCurrentDay`; `TestExportOperatorYamlHasNoIdentityOrEndpoints`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1`. **Depends on:** 1.6, 3.5 (the inventory's job list). **Commit:** "project export: the project's record, secrets excluded".

### Task 6.2: `--push`, and the cadence the design defers

**Files:** `internal/cli/project_export.go` (+ tests), `docs/project-layer.md`.

1. `--push` commits to the backing repository the policy names (a new `backing_repo` policy field, written by the operator): a clone into a scratch dir, a commit whose message names the export's `checked_at`, and a push through the same credential path the runner's git secret uses. **It refuses to push a non-fast-forward** and reports the divergence rather than forcing anything.
2. **The scheduled export and the `init`/`budget set` hooks are deferred** (D7: "manual first, scheduled later"). The design's later shape is recorded where the code will be: a history-job entry (`deploy/terraform/gcp/modules/installation/history.tf:174-253`, whose service account already has RTDB admin) needs a deploy key or App token as a project secret, and whether the existing App can push to the backing repository is **UNVERIFIED** (design §8) — a probe for that phase's own check, not this one.
3. `docs/project-layer.md`: the manual cadence, what to put in the repository, and the sentence that the export does not make the project reconstructable end to end.

**Tests:** `TestExportPushRefusesNonFastForward`, `TestExportPushUsesTheConfiguredBackingRepo`, `TestExportWithoutBackingRepoRefusesPush`.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1`. **Depends on:** 6.1. **Commit:** "project export --push: the manual cadence, and nothing scheduled yet".

### Task 6.3: the docs, the skills and `doctor`

**Files:** `docs/project-layer.md`, `docs/multi-model.md`, `plugin/skills/setup/**`, `internal/cli/doctor.go` (+ tests), `internal/cli/docs_project_centric_test.go` (new: the checklist guard test), `SECURITY.md`.

1. The user-facing story: what a backing repository is for (history and audit; survival of the Firebase project), what it is not (a unit of execution, a backup of secrets), and how to read it.
2. `doctor`: a `backing-repo` check (info/warning) — configured or not, whether the last export is stale against the policy's own `checked_at` (a `warning` past 30 days, the same threshold family the price catalog's P7 uses), and that a divergence on `--push` is reported rather than forced.
3. The setup skill: the backing repository's one question, and the export command.
4. SECURITY.md: the export's security posture in one paragraph — metadata only, no values, people's addresses are a deliberate opt-in.
5. **`TestChecklistChecksAreGuarded`** (Review Focus 13, `internal/cli/docs_project_centric_test.go`, in the `docs_*_test.go` convention Task 1.6 and 2.3 use): a docs test over `docs/gcp-live-checklist.md` asserting that **every** `## Check 48`-onward section carries **Check 43's three hard stops and its project pinning, not merely a `${P:?...}`** (R2-6, round-1 finding 13): that each section (a) asserts `P` equals `fugaro-dev` with the other variables non-empty, (b) calls `fugaro doctor --project fugaro` and greps its `project:` line, (c) greps the checkout's origin against `edgeappinc/fugarosandbox`, and (d) uses `--project fugaro` on every `fugaro` command that takes the flag and `--project "${P:?}"` on every `gcloud` command — the last two asserted by scanning the section's own lines for `fugaro`/`gcloud` commands that lack the flag, so a hand edit that drops one fails the test. It also asserts the three names the plan fixes: **no section may mention `edgeappinc/edgeserver`, `edgeappinc/edgeweb` or a `edgeappinc` workspace token** except in a "never" sentence. It lands here, in the last phase, because checks 48 to 56 do not all exist before it — and it is the test that fails if a later hand edit weakens a guard.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` and `GOFLAGS=-p=2 go test ./internal/cli/... -count=1`. **Commit:** "docs, skills and doctor: the backing repository".

### Task 6.4: Check 56 — OWNER-RUN

**Files:** `docs/gcp-live-checklist.md`.

Append `## Check 56: the export, and what it does not contain (installation fugaro only, never belong; run by you; NOT RUN)` and its row:

1. Setup and guard — **Check 43's block verbatim** (Task 0.4 step 1); record the project's caps, policy and the sandbox's state.
2. `fugaro project export --out <dir>` on installation `fugaro`. **FACT:** the file tree with per-file byte counts, and the manifest's `checked_at`.
3. **The scan that matters**: grep the whole export for a value the owner knows is in Secret Manager (the owner chooses one and greps for it **without pasting it**). **FACT:** zero matches, and the counts for the `inventory/` files that name the secret.
4. **The people check**: `grep -r '@' <dir>` (before any `--with-people`). **FACT:** the count, and which files matched (a commit message or a URL is fine; a `byPerson` key is not).
5. Determinism: export twice into two directories and `diff -r` them. **FACT:** empty diff (or the one field that legitimately differs, named).
6. `--push` to the backing repository if one is configured: **FACT:** the commit's subject, the file count, that the divergence case was reported rather than forced (a second push attempt after an out-of-band commit), and that nothing in the pushed tree is a secret value.
7. Restore: none needed (the export writes outside the installation) — but confirm one ordinary sandbox task still runs and, if `--push` was used, that the backing repository's `main` is where the owner expects.

**Paste back:** every FACT line, never a secret value. **Depends on:** 6.1 to 6.3.

## Cross-task dependency audit

The owner's first requirement is that **each phase, and where practical each task, merges alone and leaves `main` green**, with no task depending on a later one. This section is that check, done explicitly rather than assumed: every task's "Depends on" line from its own section, in one table, with the direction and the answer to "what if the dependency is not merged yet".

**Direction.** Every edge below points **backwards** — to an earlier task in the same phase, to an earlier phase, or to an already-open PR. **There is no forward edge in this plan**, and no task is written as "do this after the next one": that is the property the table exists to demonstrate.

| Task | Depends on | If it is not there yet |
|---|---|---|
| 0.1 | — | merges alone |
| 0.2 | — | merges alone |
| 0.3 (D6) | — | merges alone; the deploy is 0.4 |
| 0.4 (Check 48) | 0.3 | the check deploys what 0.3 changed; nothing else in the plan reads it |
| 1.1 | — | merges alone (a new package, no callers) |
| 1.2 | 1.1 (the path literals it asserts parity with) | needs 1.1's exported paths; same phase, previous task |
| 1.3 | 1.1, 1.2, 1.4 (the two variables it reads) | reads the policy 1.1 defines and 1.2 makes readable; **the 1.3 ⇄ 1.4 edge is one file in both directions** (`internal/cli/launch_budget.go`), so the two are PR group E together and neither merges alone |
| 1.4 (D11) | 1.1 (the path constants), 1.3 (one shared file, both directions) | **1.3 consumes 1.4's two variables and the two edit the same `internal/cli/launch_budget.go`**, so they are PR group E together; the earlier row's "1.3 (the shared token take and the launcher's mint decision)" was right about the file and wrong about the direction (round-2 review) |
| 1.5 | 1.1, 1.3 | the allow-list check extends the reader 1.3 builds |
| 1.6 | 1.1–1.3 | the CLI and doctor surface for what they built |
| 1.7 (Check 49) | 1.2–1.6 | the phase's one owner-run step; nothing after it in phase 1 |
| 1b.1 | 1.1, 1.6 **and** #359's phase 2 + the price catalog (#361) | the whole sub-phase is **deferrable**: it is gated on two other PRs by P7, and phase 2 does not need it |
| 1b.2 | 1b.1 | same sub-phase |
| 1b.3 | 1b.1, 1b.2 | same sub-phase |
| 1b.4 (Check 50) | 1b.1–1b.3 | same sub-phase |
| 2.1 (D2) | — (nothing from phase 1: the project secret ID is `gcp.ProjectSecretID`, `internal/backend/gcp/names.go:154`, merged with #343) | merges alone: the default changes, the machinery is #343's, already merged. The earlier row named "1.1 (the project secret ID)", which was wrong twice — 1.1 is `internal/projectpolicy` and nothing in 2.1 consumes it, and the ID function was never phase 1's (round-2 review) |
| 2.2 | 2.1 | mounts what 2.1 stores at the new default |
| 2.3 | 2.1 | documents it |
| 2.4 (Check 51) | 2.1–2.3 | owner-run |
| 3.0 (Check 52) | — | **runs first by design**, before any phase-3 code; a go/no-go, not a dependency on code |
| 3.1 | 1.1, 1.6 | the policy shape and the CLI that publishes it |
| 3.2 | 3.0, 3.1, 1.6 | a probe failure re-scopes it in the same PR, in writing; **D13 is ruled (2026-10-15), so nothing here waits on it** — the token exposure is accepted and documented, and Task 3b.1 designs the channel (R1) |
| 3.3 | 3.1, 3.2, 3.0 | the job names it parses come from 3.2 |
| 3.4 | 3.2, 3.3 | renames the callers for the model 3.3 introduces |
| 3.5 | 3.2, 3.3 (phase order; the grants and the naming it reads) | none of it is read by an earlier task |
| 3.6 | **#360 / Check 38** (external, open) and 3.2–3.5 | `cloneShipped` is flipped only after Check 38's answer; until then the flag stays where it is |
| 3.7 (Check 53) | 3.0–3.6 | owner-run |
| 4.1 | 1.6, 3.3 | the seen-repositories read wants the project jobs to exist to be meaningful; the commands themselves need only 1.6, so it is the softest edge in the plan and the task says so |
| 4.2 | **the full config-transitions plan** (#365; R2: it ships on `main` in 0.7.5, Tasks 1 to 9, not "core only") and 3.2 | the retirement engine is #365's; the job/SA/registry set it deletes is 3.2's. Phase 4 builds on the **full** engine, so this edge is a version requirement (0.7.5 or later), not a partial merge |
| 4.3 | 4.1, 4.2 | the allow-list write and the retirement path |
| 4.4 | 4.2, 4.3 | the refusal lands after the paths it refuses |
| 4.5 (Check 54) | 4.1–4.4, 3.7 | owner-run; the last task in the phase |
| 4.6 | 4.1–4.5 | the phase's sweeping commit; nothing after it depends on it |
| 5.1 | 3.2, 1.6 | a job per (profile, provider, account) is what a person's secret is referenced by |
| 5.2 | 5.1, 3.2 | the references it creates jobs for |
| 5.3 | 5.1, 5.2 | the accounts it fails over between |
| 5.4 (#363) | 5.3 | **a pointer with a rule**: it merges in its own PR, after this phase; nothing in phases 0–4 waits for it |
| 5.5 (Check 55) | 5.1–5.3 | owner-run |
| 6.1 | 1.6, 3.5 | the policy it exports and the job inventory it lists |
| 6.2 | 6.1 | the export it pushes |
| 6.3 | 6.1, 6.2 | documents them |
| 6.4 (Check 56) | 6.1–6.3 | owner-run |

**The cross-phase edges that are not just ordering**, spelled out because they are the ones a reviewer will question:

1. **`#365` before 4.2, `#360` before 3.6, `#359`+`#361` before 1b, `#343` superseded by 4.3.** Four external edges, all open PRs, all backward — the plan builds on them or waits for them, and never the reverse. D9's `#363` is deliberately **after** phase 5, not before it.
2. **Phase 1 without phase 2 or 3**: an installation that publishes a policy gets enforcement, and one that does not is untouched (P3), so phase 1 merges into a working system on its own.
3. **Phase 2 without phase 3**: the default scope changes; a repository keeps its own job and mounts the project's secret on it (2.2), which is #343's supported path. Nothing needs a shared job.
4. **Phase 3 without phase 4**: shared jobs work; a repository's root still exists and is simply unused for clone workflows. Nothing is deleted until 4.2, which is exactly why 4.2 is safe.
5. **Phase 4 retires what phases 0–3 left behind, and nothing earlier depends on phase 4.** 4.2's refusal to retire a root whose resources another repository references is what makes it mergable into an installation that has not finished migrating.
6. **Phases 5 and 6 are independent of each other** (5 needs 1.6 and 3.2; 6 needs 1.6 and 3.5), so either can ship first, and neither is needed by 0–4.

**What each phase leaves on `main` when it lands**, which is the "system still works" half of the requirement: after 0, the rules and the messages are fixed and nothing else changed; after 1, an installation with no policy behaves exactly as before and one with a policy is enforced, with `fugaro run` still working in both; after 1b, routing is data and the unset case falls back to today's behaviour; after 2, `secrets set` stores project-wide and every existing path still mounts it; after 3, clone workflows run on shared jobs and per-repository jobs still exist for anyone not switched over — **enforced by Task 3.3 step 1, which routes a legacy job name down the phase-1 path instead of halting it** (finding 5; without that the phase could not merge on an installation that has published a policy); after 4, the old roots are retired one at a time, by the owner, each after its replacement is proven; after 5, OAuth accounts are people's, with the per-repository copies still readable until the repository path is gone; after 6, a record can be exported, and the installation is unaffected by the export having run.

## The design's §12a items, by owning task

| §12a item | Owning task | What carries it |
|---|---|---|
| 1. profile and account from `CLOUD_RUN_JOB`, not an overridable env var | 3.3 | the derivation is explicit (the runner reads it through `backend.ExecutionFromEnv`), and the corrector was in the drift table: the runner does not read `CLOUD_RUN_JOB` directly on this branch |
| 2. no provider from an overridable env var; the job or the policy supplies it | 3.3 (binding), 1.5 (the first half) | the job's provider and the policy's canonical names |
| 3. the interim path between phases 1 and 4 | 1.6 step 1 (`fugaro project repos add`, the one-line data write shipped early for exactly this gap), 4.1 (closes it) | a new repository is unenrolled between them; the interim is one allow-list write, not a Terraform run |
| 4. seed `allow_repos` without one laptop, and no silent re-add | P4, 1.6 (the seed), 1.3 (the reader) | seeded once from every checkout the operator's machine knows; a later publish never re-adds a name the operator removed |
| 5. matching includes the provider, canonical names | 1.1 (`Canonical`, `MatchRepo`), 1.5 | the glob rules are `provider/owner/name` |
| 6. old baked images ignore a published policy | 1.6 | `doctor` and `upgrade` say so, and 12a item 6's message is in the drift note |
| 7. `config/admin` is readable by launchers; no secret-adjacent data | 1.2 (rules), 1.1 (shape) | the section states it and the rules carry `.read: false` for a run token |
| 8. profile admission is a phase 3 item | 3.3 | phase 1 enforces `allow_repos` only (P6) |
| 9. the routing switch is an object, effective on publish | 1b.1 | an edit takes effect when an operator publishes |
| 10. D2's reach on per-repository jobs still needs `init --repo` until phase 3 | 2.2, 2.4 step 2, 3.2 | stated as the phase's remaining Terraform step |
| 11. who creates a person's secret | 5.1 | the person, with the operator accepting the reference |
| 12. run-token writes include `outcomes/` and the global spend node | **1.2 step 5** | the policy edit is additive and confined to `config`, and the parity table gains a row per existing write |
| 13. the RTDB policy replaces only the ceiling; `fugaro.yaml` still only tightens | 1.3 (the merge) | `policy.Merge`'s ceiling becomes the published policy, and the file layers keep their place |
| 14. the cross-cutting list names `scripts/gen-golden-plans.sh`, `docs/multi-model.md`, `docs/checkout.md`, `docs/git-providers.md` | 3.2, 4.6, 5.5, 6.3 | each phase's sweeping task, named there |

## The design's §13 UNVERIFIED items, by owning task

| §13 item | Owning task | The probe, and what happens if it fails |
|---|---|---|
| Cloud Run secret reference inside an env override | 3.0 step 3 (Check 52) | if it works, it is a narrower option for the account mount; the design's option D stays rejected for the IAM reason the same step checks |
| whether an execution's overrides are visible to the container | 3.0 step 3, then 1.5 step 3's SECURITY.md text | what SECURITY.md claims is limited to the answer |
| Secret Manager IAM conditions on a name prefix | 3.0 step 4 | if honoured, it is a *possibility* for phase 5's per-person grants; if not, phase 5 uses one secret per person per account, as designed |
| whether a repository module can reference a secret it does not create | **settled by reading, no probe**: `docs/design/project-secrets.md:18` (a `secret_id` IAM grant works identically whichever root created the secret), and Task 2.2's test | confirmed live in Check 51 step 2 (the sandbox job mounts the project secret ID) and Check 53 |
| the project-wide Bitbucket token scope | 3.0 step 2 (Check 52), **a go/no-go** | Bitbucket projects stay on the per-repository path, the owner is told before phase 3 starts, D5 is re-decided, and 3.1–3.7 proceed for GitHub only |
| whether the GitHub App can push to the backing repository | 6.2 (recorded as that phase's own probe when its check runs) | `--push` needs a deploy key or App token as a project secret, and 6.2 says which |
| Claude Code's usage-limit text | 5.3 (the detector), 5.5 step 4 (the live answer) | a miss is the safe direction (no switch) and is what the skill already documents |
| runs bucket IAM policy size and the service-account quota | 3.0 step 5 (Check 52), 3.2 step 6 (the golden test asserts the count is a function of profiles × accounts, not of repositories) | a sandbox with no headroom stops the phase and is reported |
| how the Firestore database is created | **settled by reading, no probe**: `fugaro init --firebase`'s `firestoreStep`/`deployDatabase` (`internal/cli/init_firebase.go:549`, `:465`), with `firestore.googleapis.com` enabled by the Firebase root (`deploy/terraform/gcp/modules/firebase/apis.tf:15`) and the history account's `roles/datastore.user` (`.../modules/firebase/iam.tf:94`) | phase 6's export and the history job both assume it exists; the export reads it, never creates it |
| whether `dimipaun/fugarosandbox` is usable as the GitHub sandbox | 4.5 step 4 (Check 54) and 3.7 step 2 (Check 53) | if not, a GitHub sandbox repository is created and the FACT line says which |
| whether the launcher override gap is exploitable in practice | 3.0 step 3's D10 counterpart, with SECURITY.md written in 1.5 step 3 and 3.5 | the code says yes; a live check runs before SECURITY.md states it, and no phase claims to close it (D10 accepted) |

## Security-sensitive tasks, for independent opus review

Each of the owner's named areas maps to a task, and each of those tasks stands alone in its own PR group (the PR groups section) so a review sees exactly one of them:

| Area the owner named | Task | Why it is security-sensitive |
|---|---|---|
| the RTDB rules and the policy node | 1.2, 0.3 (D6) | the rules decide who can read the policy and the kill switches; D6 changes the per-run cap's direction (a fail-open direction) |
| the runner's slug binding, admission and fail-closed checks | 1.3 (fail closed), 1.4 (**the RTDB URL and API key**, which decide whether a published policy can be read or bypassed), 1.5 (the binding's first half), 3.3 (the full binding and profile admission) | a wrong slug lets one repository's run act as another's; fail-open would let an unpublished policy be bypassed |
| the shared-job IAM and bucket conditions | 3.2 | one job SA per (profile, provider, account) broadens the bucket conditions — D4's accepted regression, stated in SECURITY.md; **and the same grant reaches other runs' budget tokens, which is D13, ruled 2026-10-15: accepted and documented beside D4, with Task 3b.1 designing the channel and separate profiles as the interim boundary (R1)** |
| project-scope keys | 2.1 | D2 reverses a default: a key reaches every enrolled repository unless `--repo` is passed |
| the OAuth pool | 5.1 | a person's token, an operator's grant, and the widening the design accepts |
| the export | 6.1 | a file tree that must contain no secret value and, by default, no person |
| the launcher's override surface | 3.0 (the probe), SECURITY.md in 1.5/3.5 | the boundary is documented, not closed (D10); the probe decides what may be claimed |

## Self-review

Written last, against the owner's instructions, checking the plan rather than its prose:

1. **Does every phase merge alone?** Yes — the audit above is that check, and it found one edge to soften and one item to add. Phase 1b is deferrable by P7 (it waits on two open PRs); 4.2 waits on the **full** config-transitions plan, which ships in 0.7.5 (R2); 3.6 waits on #360's Check 38. Nothing in the plan waits on a later task. Phase 0 to 6 each leave `main` green and the system working, as stated per phase in the audit's last paragraph. **The round-1 review found one place where that was claimed but not true** — phase 3's runner would have halted every unmigrated per-repository job once a policy existed — and the fix is a rule in the plan (Task 3.3 step 1: the job's own name decides which contract applies), not a promise. **The round-2 review found the phase-1 counterpart of the same class of mistake**: the fail-closed rule was stated for a run that cannot see the marker at all (finding 1 above), so it is now stated on the run that has the credential, with the credential-less case owned by the launcher's readiness gate.
2. **Was every cited `file:line` re-verified?** Every one that the plan asserts, yes, against this branch (`fd86542`), and the drift table at the top records what moved, what the design names as a bare filename, and the three citations that could not be re-verified from the repository (the generated `run-gen.go`, the `requestedBy` symbol, and the "the runner already reads `CLOUD_RUN_JOB`" premise). Three corrections came out of writing the later phases and are in this commit rather than the drift table: `claudeOAuthAccountRefusal` is `internal/cli/secrets.go:196`, the spend and cap paths are `internal/budget/model.go:51-80` (not `:44`), and `internal/cli/project.go` already exists and holds the checkout/project helpers, so the enrollment commands get their own file. The `build.tf` range is `:5-93`; the other module ranges cited (`secrets.tf:4-42` with `prevent_destroy` at `:22`, `registry.tf:10-72` with `prevent_destroy` at `:59`, `check.tf:16-142`, `history.tf:174-253`) were read, not inferred.
3. **Is every security-sensitive area flagged for opus review?** Yes — the five the owner named, plus D10's override surface and D6, in seven tasks, each with its own PR group.
4. **Is every owner-run step marked, and none done by an agent?** Rules deploys (0.4, 1.7), Terraform applies (3.7, 4.5, 5.5), `init --repo` (4.5), secret entry (4.5, 5.5), and the live checks (48 to 56) — each marked **OWNER-RUN**, each with a guard, a hard-coded project, a restore, and a "paste back FACT lines only, never a value" line. **No step touches `belong`**: every check 48 to 56 says so explicitly, its guard's stops 2 and 3 enforce it, and no step in the plan creates a credential in the `edgeappinc` workspace or probes edgeserver or edgeweb (R2-6).
5. **Are the Check numbers free?** 45 and 46 are claimed by open PR #359, 47 by #363 (read with `gh pr diff`), 44 by #361, 43 by #362, 38 by #360; `docs/gcp-live-checklist.md:242` says Check 45 is the next free number on `main`. This plan takes **48 to 56**, and says so at the top and in each phase's check task.
6. **Is every §12a item and §13 item carried?** Yes — the two maps above. Writing them found one gap, 12a item 12 (the run token's non-read writes), which was added to Task 1.2 as step 5, and one item that was a probe in the design and is settled by reading here (how the Firestore database is created), now cited to `init_firebase.go:549`.
7. **Is every cross-cutting item carried?** The §11 list — SECURITY.md, the user docs, the plugin skills, `fugaro doctor`, `upgrade`'s drift note, the dashboard, launcher grants, version skew, `validate` offline, `scripts/gen-golden-plans.sh`, `docs/multi-model.md`, `docs/checkout.md`, `docs/git-providers.md` — is carried by each phase's sweeping task (1.6, 1b.3, 2.3, 3.2/3.5/3.6, 4.6, 5.5, 6.3), and the phases that change nothing in one of them say so instead of dropping it silently.
8. **What this plan does not do.** It does not implement anything (docs only), it does not decide D5, D3's deferred API alternative, D7's scheduled export or D9's rebase — each is marked where it would land and by whom. It does not claim the launcher override surface is closed, and it does not claim the export makes a project reconstructable end to end.
9. **The weakest part of the plan**, said plainly so a reviewer can attack it: Task 4.1's dependency on 3.3 (the seen-repositories list is only meaningful once project jobs exist) and Task 3.0's go/no-go on the Bitbucket token, which can re-scope a whole phase for GitHub-only. Both are the honest place for those questions to sit, but both are places where a phase's shape is decided after the plan is written, and the plan says what happens then rather than leaving it open.
10. **The review round, and what it caught — kept here because it is evidence about the plan's own method, not a footnote.** Seven findings, all in this document and all corrected in one commit: `Budget`'s fields had no `json` tags, so the documented snake_case keys would never have decoded and — with unknown keys ignored — a camelCase policy would have become a silent zero ceiling (Task 1.1 now carries the tags, refuses a `budget` whose documented keys all miss, and has two tests for it); P5 said the allow-list matched "the same canonical form `task.Slug` builds", when `task.Slug` is the hashed storage identity (`acme-app-3f9c…`) and the compared value is `projectpolicy.Canonical` (P5 now distinguishes the two and says why they agree by construction, with `TestCanonicalAgreesWithTaskSlug`); Task 3.3 said "the GitHub App ID and key become project policy", which would have put `github-app-key` in a node every run token can read (the key now stays a Secret Manager secret granted to the project job SAs, and only its name reaches the policy — the rule 12a item 7 and Task 1.6's `TestPolicyCarriesNoSecretValue` exist for); Task 0.3 mislabelled `rules.json.tmpl:36` as `released` (it is `spent`; `released` is `:32`); P2's first sentence called `config/admin` the third child of `config/policy` and then corrected itself; the §12a map named the interim enrollment path as `--project-wide` when Task 1.6 ships `project repos add`; and the header said phase 0 was four fixes while listing three. Two of the seven are the same failure mode — a claim that was right in one place and sloppy in another (P2, the phase count) — and one, the missing tags, is the reason Task 1.1 now states its decoding rules as a contract rather than as a comment.

