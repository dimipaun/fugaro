# Project-centric Fugaro: implementation plan

**Goal:** make the **project**, not the repository, the unit of configuration, policy, credentials and setup — the reconciled design [docs/design/project-centric.md](../design/project-centric.md) (§11 phases 0 to 6), implemented so that **each phase merges alone, leaves `main` green and the system working**, and adds a repository to a project with no Terraform and no confirmations. No code in this plan has been written or run; this is a design-to-code plan, the same status `docs/plans/2026-10-14-*` had before its Task 1 started. **This PR (the one that adds this plan) is docs-only: no Go, Terraform or product-behavior file under this plan's "Files" lists is touched by it.** Unlike the price-catalog and project-secrets plans it edits **nothing else at all** — not even `docs/gcp-live-checklist.md`: the check numbers this plan reserves (48 to 56) are *named* here and their rows and sections are written by each phase's own check task when that phase ships, because a reservation without a written check is exactly the "Reserved number" row `main` still carries for Check 42 and the design says nothing about those numbers. Read §"Check numbers this plan takes" for why nothing below 48 is free. Tasks 0.1 onward are future work, gated on the owner approving the design.

**Spec:** [docs/design/project-centric.md](../design/project-centric.md) (approved 2026-10-14; the owner's decisions D1a to D12 are in its §12, its §12a lists the fourteen review items this plan must carry, and its §13 lists what was UNVERIFIED when it was written) and the owner's verbatim draft [project-centric-spec-source.md](../design/project-centric-spec-source.md). The design's decisions are settled; this plan's own decisions continue as **P1 onward**, each independently vetoable before its phase starts.

**Architecture — what the seven phases build, in one paragraph.** Phase 0 is four small fixes with no shared surface: the budget refusals stop naming flags that do not exist, `anthropic-api-key` stops being mandatory, and the global per-run cap becomes the fallback the RTDB rules already deny for. Phase 1 moves the ceiling out of each job's environment into **one RTDB policy object** (`config/policy`), read by the runner at start, unreadable-by-wrong-slug by rule, and fail-closed: "project-centric mode" is *the existence of `config/policy/shared`*, so an installation that has not published one behaves exactly as today. Phase 1 also ships the enrollment allow-list (`allow_repos`) that a shared job needs from its first day, and the CLI that publishes and shows the policy. Phase 1b decouples the model from the key route with the operator-written `routing.claude` switch and the canonical-to-route ID map, and ships only after #359's phase 2 and the price catalog are merged. Phase 2 makes project scope the default for `fugaro secrets set` (D2, which reverses #343's default-safe rule). Phase 3 replaces the per-(repository, workflow, account) job with **one job and one job service account per (profile, account)**, and with it the runner binds its slug to the repository it clones, admits only repositories its profile allows, and takes its remote from `task.json` and the policy instead of `FUGARO_REMOTE`. Phase 4 turns enrollment into data (`project sync`, `add-repo`, `repos`), retires a repository's whole Terraform root, retires `init --repo` for clone repositories, and drops baked workflows (D3). Phase 5 makes Claude OAuth tokens person-owned and shared by reference, with the CLI (not the skill) doing the failover. Phase 6 writes the project's policy, config, inventory and spend into a backing Git repository, secrets excluded. Nothing in phase *n* is required by phase *n-1*'s merge: each phase is a working system, and the last section of this plan lists every cross-task dependency so a reviewer can check that claim.

**Builds on merged code (what each phase stands on).** Verified on `main` as of this plan's writing (`fd86542`, and the open PRs named in §"Check numbers"):

| Phase | Merged work it builds on |
|---|---|
| 0 | `internal/budget` (M9b/evaluate/session), `internal/budget/rules` (rules template, emulator, `TestRulesParity`, `internal/budget/rules/parity_test.go:175`), `internal/infra/spec.go`'s secret mounts, `internal/gateway`, `internal/agent/env.go`, `docs/design/m9-budget-and-dashboard.md` |
| 1 | `internal/rtdb` (the client `fugaro budget set` already writes caps with), `internal/policy` (the layer merge `resolvePolicy` computes), #343's `ResolvedSecretID`/`ProjectSecretCandidates` (project secrets, merged 47a73b9), #344's retirement engine (`docs/design/config-transitions.md`), the golden-plan generator |
| 1b | **#359's phase 2** (the `anthropic/<id>` claim and `IsAlias(model, claimed)`), **#361/the price catalog** (`require_zdr`, `price_policy`, and the catalog rows the canonical-ID map needs), `internal/runner/agentenv.go`'s `stagePins` |
| 2 | **#343, merged** (project-scope secret containers, `--project-wide`, the repo-wins-else-project precedence in `internal/infra/readiness.go`) |
| 3 | **#342, merged** (`checkout: clone` and the runner's blobless-clone bootstrap), **#360/Task 18 and its owner-run Check 38** (the critical path: `cloneShipped` flips only after Check 38), `image refresh`'s API-driven job patch (`internal/cli/image_refresh_steps.go:226-261`), `internal/backend/gcp`'s `JobName`/`BucketCondition`/`Launch`, `internal/gcpfake` (the fake that must learn project jobs) |
| 4 | #344's design and **#365's core implementation** (the migration/retirement tool), #359/#363's lesson that per-repository variant jobs multiply |
| 5 | `internal/cli/accounts.go`, `agents/` and the OAuth variant machinery, `runstore/launch.go`'s `LaunchedBy`, the skill's failover prose (`plugin/skills/working/reference/diagnose.md:79-97`); **#363 is held until this phase** (D9) and rebases onto it |
| 6 | `internal/report`'s `Build` (`internal/report/report.go:159`, called from `internal/cli/report.go:170,393`), `internal/watch`'s JSON feed (`internal/watch/json.go:148`), the history job and its 15-minute sweep (`deploy/terraform/gcp/modules/installation/history.tf:174-253`) |

## Phase summary

| Phase | What ships | Owner-run steps | Depends on |
|---|---|---|---|
| **0. Small fixes** | the four wrong flag names; `anthropic-api-key` optional end to end; D6: the global per-run cap is the fallback, in the rules template, the emulator, the parity test and `internal/budget/model.go` | **the RTDB rules deploy** (Check 48), plus the live check | nothing |
| **1. Policy as data** | `config/policy` (per-child read rules, emulator + parity tests), the fail-closed runner, `allow_repos` + `project add-repo`, `project policy publish` / `project show`, `FUGARO_RTDB_URL` written whenever Firebase is configured, the policy version and its skew messages | **the RTDB rules deploy** (Check 49), then a sandbox publish and run | phase 0 (its rules deploy lands first) |
| **1b. Routing switch** | `routing.claude` (explicit ID list, D1a; absent = `direct`, D1b), the canonical-to-route ID map, `validate`'s offline resolution line | Check 50: a routed Claude senior and whether its cache markers survive | **#359 phase 2 + #361 merged**; phase 1's policy delivery |
| **2. Project-scope keys** | `secrets set` defaults to project scope (D2), mounted on the repository's job until phase 3, `allow_data_to` unchanged as the data-policy gate | secret entry by the owner; Check 51 | phase 1 (enrolled repositories are what "every enrolled repository" means) |
| **3. Shared project jobs** | profiles; one job + one job SA per (profile, account); the runner's slug binding, remote-from-task, allow-list and profile admission; one bucket condition per prefix; every job-naming caller; `cloneShipped` flipped after Check 38 | **Check 52 first** (the four UNVERIFIED probes: the Bitbucket token, the env-override secret reference, execution overrides, Secret Manager IAM conditions), Terraform applies, then Check 53 | **#342 + #360/Check 38**; phase 1's allow-list; phase 2's project secrets |
| **4. Enrollment and migration** | `project sync`/`add-repo`/`repos`; retire a repository root; `init --repo` retired for clone repositories; baked workflows dropped (D3) | the migration of installation `fugaro` (never `belong`), Check 54 | phase 3 (project jobs are what a repository moves onto); #365's core |
| **5. OAuth pool** | person-owned secrets per (person, account), references in the policy, `accounts share`, CLI failover | the owner stores their own token; Check 55 | phase 3 (a shared account is a job per profile) |
| **6. Backing repository** | `fugaro project export [--push]`, secrets excluded, people omitted by default | the owner's own push target; Check 56 | phases 1 and 3 (what there is to export) |

**Nothing in this table depends on a later row.** Phase 1b may be deferred indefinitely (its two prerequisites are other PRs); phases 2, 3, 4 and 5 do not read the routing switch; phase 6 reads only the policy and the bucket. The last section lists the per-task dependencies.

## Check numbers this plan takes (48 to 56)

`docs/gcp-live-checklist.md` on `main` (as merged by #353 and #354) says *"Check 45 is the next free number"* (line 242). The **open PRs change that**, so this plan takes nothing below 48 and says whose each number is:

| Number | Claimed by | State |
|---|---|---|
| 42 | config-transitions design §6 and plan Task 8, merged as #344 | reserved row on `main`; #365 implements the core |
| 43 (43a-43f) | registry remote repository, merged as #353 | written on `main`, not run |
| 44 | provider price catalog, merged as #354 (plan Task 7) | written on `main`, not run |
| 45, 46 | **open PR #359** (senior reviewer, Phase 1 and Phase 2), whose checklist patch adds both rows and sections | not merged — the claim is readable only in that PR, which is why this plan does not take them |
| 47 | **open PR #363** (OAuth reviewer stage, its Task 8) | same |
| **48** | **this plan, phase 0**: the rules deploy and what the global per-run fallback changes | reserved here |
| **49** | **this plan, phase 1**: policy as data, end to end | reserved here |
| **50** | **this plan, phase 1b**: the routed Claude senior and its cache markers | reserved here |
| **51** | **this plan, phase 2**: project scope by default (D2) | reserved here |
| **52** | **this plan, phase 3, first**: the four UNVERIFIED probes, a go/no-go | reserved here |
| **53** | **this plan, phase 3**: the shared project job round trip | reserved here |
| **54** | **this plan, phase 4**: enrollment, retirement, migration of installation `fugaro` | reserved here |
| **55** | **this plan, phase 5**: the OAuth pool | reserved here |
| **56** | **this plan, phase 6**: the export | reserved here |

**Each check's row and section is written by the phase's own task, not by this PR** — that is the one place this plan differs from its predecessors' habit of reserving a row up front. The reason is #359/#363: their numbers live only in unmerged PRs, and a reservation this plan wrote into the checklist would have to be amended by whichever of the three merges second. Taking 48 as the next free number and writing each row with its phase keeps one writer per row. **All nine are owner-run, on installation `fugaro` and the sandbox repository only, never the `belong` project** (which holds `edgeappinc/edgeserver` and `edgeappinc/edgeweb`: no agent touches it, ever; migrating it is the owner's own work after the model is proven on `fugaro`, D2 and D3). Every check is written with FACT lines, a restore step and a hard-coded project with a guard, the shape Check 43's setup block already uses (`docs/gcp-live-checklist.md:1158-1177`).

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
| `run.go:92-101`, `:107` | `internal/cli/run.go:89-102`; `Launch` is `internal/backend/gcp/run.go:106` | the design's `run.go:107` is the CLI's own wiring |
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
| `recipes_skew.go:43`, `:45-67` | `internal/cli/recipes_skew.go:44-70` | `cloneSince = "0.7.1"` at `:44`, `cloneShipped = false` at `:70` |
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
- **P2. The policy object is one RTDB subtree, `config/policy`, with three children and no other readable node: `shared`, `repos/$slug`, `admin`.** The types, the paths and the version live in one new package, `internal/projectpolicy`; `internal/budget` keeps its own `Path*` helpers (`internal/budget/model.go:44-80`) and neither package imports the other. `internal/projectpolicy` imports `internal/rtdb` only. The rule is per child because **RTDB read grants cascade to children** (design §5): a `TOKEN` read on `config/policy` itself would make every child readable by every run token, which is the narrowing the design exists to keep. **`config/admin`, not `config/policy/admin`**: the design's §5 wording is "an admin-only `config/admin`", and 12a item 7 adds the reason not to put anything secret-adjacent there — launchers hold the RTDB *viewer* role and IAM reads bypass the rules, so launchers read it. *Veto alternative:* one flat `config/policy` node readable by run tokens — rejected outright: a prompt-injected agent can take the run token through `/proc` and would then read the project's whole policy, including who owns which account.
- **P3. "Project-centric mode" is the existence of `config/policy/shared`, tested by the runner with one read, and an unreadable-or-unparseable existing node halts the run.** Not a flag in the job env (a launcher can clear it, D10), not a version probe on the parent (denied under per-child rules). Absent ⇒ today's behaviour exactly (the env ceiling, the per-repository path): an installation that has not published a policy cannot be changed by this phase, which is what makes phase 1 merge alone. *Veto alternative:* a `FUGARO_POLICY_URL`/`FUGARO_POLICY=1` env flag — rejected: it is exactly the override surface D10 documents, and it would let one launcher override turn the project policy off for one run.
- **P4. `allow_repos` is seeded once, from the local config's `repos:` map, and a later publish does not silently re-add a name an operator removed (12a item 4).** `project policy publish` seeds **only** when the node does not exist yet, or when the operator passes `--seed-repos`; the seeded source is the operator's own file *and* the repositories the installation has already run (`runs/<slug>/` in the bucket), so seeding does not depend on one laptop when the laptop is not the one publishing. A name removed from the allow-list stays removed until an operator adds it, and `project show` prints the difference between the allow-list and the seen repositories. *Veto alternative:* seed on every publish — rejected: it makes `fugaro project remove-repo` impossible to keep, which is a security-relevant action (it is how a repository is un-enrolled).
- **P5. `allow_repos` patterns are canonical `provider/owner/name` globs (`github/acme/*`, `bitbucket/edgeappinc/service-*`, `*/acme/*`), matched against the same canonical form `task.Slug` builds (12a item 5).** The pattern's provider segment is required (a bare `acme/*` is refused with a message naming the canonical form) so a GitHub and a Bitbucket repository of the same name cannot be enrolled by one glob. Matching lives in one function, `projectpolicy.MatchRepo(pattern, canonical string) bool`, used by the runner's admission check and by the CLI's advisory check, so the two cannot disagree. *Veto alternative:* match the bare `owner/name` — rejected: `dimipaun/fugarosandbox` and `edgeappinc/fugarosandbox` already exist in the owner's installations (design §4.4), so a bare-name allow-list would enroll the wrong one.
- **P6. Phase 1 enforces `allow_repos` only; profile admission ships in phase 3, where profiles arrive (12a item 8).** The runner's phase-1 check is therefore "the slug matches the allow-list and equals `task.Slug(provider, spec.Repo)`" against the job's own provider — the binding's *first half*, which is enforceable before shared jobs exist and is what a shared job will need on its first day. Nothing in phase 1 creates a profile.
- **P7. Phase 1b is separately shippable and separately deferrable; nothing in phases 2 to 6 reads the routing switch.** Its gate is real: #359's phase 2 (the `anthropic/<id>` claim, `IsAlias(model, claimed)`) and the price catalog (#361) must be merged, because the switch is delivered *through* the claim and the canonical-to-route ID map is carried *by* the catalog rows. If both are merged when phase 1 ends, 1b runs next; if not, the plan proceeds to phase 2 and 1b runs whenever they land. This is the only prerequisite in the plan that is another team's PR, and it is stated here so a reviewer does not read the phase order as a hidden dependency.
- **P8. Version skew is handled by one integer, `version`, in `config/policy/shared`, and by three messages, not by a capability table.** The runner refuses a `version` it does not know, naming both numbers and the fix (`fugaro upgrade`, then refresh the job images); `project policy publish` refuses to write a version above the release the binary is, so a beta cannot publish a shape `main` cannot read; `fugaro doctor` reports a published policy newer than the binary and a policy published while the installation's jobs still carry the old image (12a item 6 — an **old baked runner image silently ignores the published policy until refreshed**, which is the trap this message exists for). *Veto alternative:* no version field, tolerate unknown keys — rejected: the design's §11 names version skew as a cross-cutting item, and this is the cheapest mechanism that answers it.
- **P9. A profile is `{cpu, memory, timeout class, secrets, allow_repos}` in the policy, and the installation root creates one job and one job service account per (profile, account) in phase 3.** `workflows.<name>.profile` selects it, defaulting to `standard`. A shared account (phase 5) is a new job *per profile* and is created by Terraform through the installation root — **the plan picks Terraform, not the API**, for the phase-3 job set, because the installation root already owns every project-level resource and the `image refresh` precedent (`internal/cli/image_refresh_steps.go:226-261`) is a *patch*, not a create. Task 3.2 says what that costs (one apply per profile added) and Task 5.2 revisits it for OAuth accounts once the phase-3 pattern exists.
- **P10. The export omits people by default and never writes a secret value; the choice is a flag, and the flag's help text says what it publishes.** `fugaro project export` writes no `byPerson` spend and no kill-switch author, and `--with-people` includes them as the operator's own choice ("they are committed to a git repository", design §8). **Nothing else in the export is optional**, and a test asserts that a byte-for-byte scan of the export for every value Secret Manager holds finds none (Task 6.1). *Veto alternative:* hash the emails and always write them — rejected by the design's own reasoning (a hash is reversible against a dictionary of the team's addresses) and because an export that always writes a derivative of a person's address is not the "omitted by default" the design promised.

## Global Constraints

From the design:
- **A phase merges alone.** Each phase's tasks are complete without any later phase: no task may read a file, node, flag or Terraform output that a later task creates. Where a phase *wants* something a later phase brings (phase 1's profile admission, phase 3's project jobs), it says so and ships the narrower thing (P6).
- **No repository is ever in a state where a shared job serves it without an allow-list match** (design §4.3 "Ordering"): the allow-list and the runner's checks ship in phase 1, and phase 3's shared jobs come after.
- **Project-centric mode requires Firebase and the budget backend** (D11). An installation that wants the budget mode off stays on the per-repository path; that is a permanent second code path and the design accepts it (§5).
- **Launchers stay trusted** (D10, accepted): the runner cannot remove the launcher's ability to override a job's env, and no phase claims to. `FUGARO_RUN` itself is set as a `ContainerOverride` (`internal/backend/gcp/run.go:110`). SECURITY.md states it (phase 1) after the live probe (Task 3.0 step 3) confirms what an execution's overrides expose.
- **`fugaro validate` is offline** (design §11): it cannot see the routing resolution or the policy unless it is given a project config that can read them. What it prints when it cannot — "unresolved offline" — is a *requirement*, not a wording choice (Task 1b.3).
- **The RTDB policy replaces only the ceiling; `fugaro.yaml`'s layers still only tighten it** (12a item 13). `internal/runner/policy.go`'s merge (`:90`) is not inverted, and no phase makes a repository able to loosen the project policy.
- **Every owner-run step in this plan is marked `OWNER-RUN` and no agent performs it**: rules deploys, Terraform applies, `fugaro init --repo`, secret entry and every live check. Live checks run on installation `fugaro` and a sandbox repository only, **never the `belong` project**.
- **Every check's shell steps hard-code the project and refuse an empty variable** (`P=${P:?...}`), the shape `docs/gcp-live-checklist.md:1158-1177` already uses, so a guard cannot fall back to `gcloud`'s ambient default project.

Project rules (carried over from this repository's existing plans):
- Every task's own test run is in the foreground, `-count=1`, and `-race` where the package's existing tests use it.
- Fakes only in tests: no live RTDB, GCP or Cloud Run call from a test. The RTDB emulator (`internal/budget/rules/emulator/`) is the fake the rules tests use, and `internal/gcpfake` is the fake the Cloud Run paths use.
- **Security-sensitive tasks are flagged `SECURITY-SENSITIVE — independent opus review`** and are not merged on a self-review: phase 0's D6 rules change (Task 0.3), phase 1's rules and policy node and the runner's fail-closed path (Tasks 1.2, 1.3, 1.5), phase 2's project-scope keys (Task 2.1), phase 3's shared-job IAM and bucket conditions (Task 3.2) and the runner's slug binding, admission and fail-closed checks (Task 3.3), phase 5's OAuth pool (Tasks 5.1, 5.2), phase 6's export (Task 6.1).
- Docs must match behavior: `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1` stays green throughout. A docs test that reads the changed text is updated in the same commit as the text.
- `fugaro verify build` / `fugaro verify test` are the only build and test entry points for this repository's own conventions; a task runs them before it is considered done.
- **No task may edit `.github/workflows/`** (a Fugaro-run constraint: GitHub refuses Fugaro's push of workflow changes). A phase that wants a CI change describes it in its PR description instead.

## Review Focus (mutation-proof notes)

1. **The global per-run cap becomes a fallback in the code and not in the rules, or the reverse.** Pinned by `TestPerRunFallsBackToGlobal` (`internal/budget/model_test.go`) **and** by the emulator test that writes a reserve for a repository with only a global cap **and** by `TestRulesParity` (`internal/budget/rules/parity_test.go:175`) — the three fail together or the change is not done. The mutation that matters: `fallback()` (`internal/budget/rules/rules.go:343`) keeps returning null for a missing repository cap, so the rules deny while `model.go` computes a fallback — a run that halts with a message about a cap the owner did set.
2. **A refusal message names a flag that does not exist.** Pinned by `TestRefusalMessagesNameRealFlags`, which runs every `ReasonNoCap` detail string through the real flag set (`internal/cli/budget.go:687-691`'s flags, as the command registers them) and fails on any `--x` in the text that the command does not define. The current four (`internal/budget/evaluate.go:190`, `:247`, `internal/budget/session.go:501`, `:511`) fail it before Task 0.1 and pass after.
3. **An api-key run is refused for a key nobody needs.** Pinned by `TestApiKeyRunWithoutAnthropicKeyStarts` (`internal/agent`) and `TestGatewayStartsWithoutAnthropicUpstream` (`internal/gateway`), plus `TestReadinessDoesNotDemandTheAnthropicKey` (`internal/infra`): a workflow whose pins all route through a provider must reach the agent with no `anthropic-api-key` version at all. The mutation: leaving `internal/infra/spec.go:591`'s mount unconditional in the *readiness* path (`internal/infra/readiness.go:100-158`) so the deploy is still blocked.
4. **A published policy is silently ignored.** Pinned by `TestRunnerReadsPolicyWhenTheNodeExists` and `TestRunnerUsesEnvCeilingWhenTheNodeIsAbsent` (`internal/runner`), which assert the *effective* per-run cap and the recorded `policy.sources`, not merely that a read happened. The mutation: reading `config/policy` (the parent) instead of `.../shared`, which succeeds under an admin client and fails under a run token's rules — the test runs the runner with a token-scoped fake.
5. **A missing or unparseable policy falls back to an empty ceiling.** The design's §5(3) fail-closed requirement. Pinned by `TestPolicyReadErrorHaltsTheRun` and `TestPolicyParseErrorHaltsTheRun`: the run ends with a message naming the node, and `spend.Mode` is never left at the *less* constrained value. The mutation: treating any read error as "absent".
6. **A repository runs on a job it is not allowed on.** Pinned by `TestRunnerRefusesSlugNotInAllowList` and `TestRunnerRefusesSlugMismatch` (phase 3) — both assert the refusal happens **before** a token is minted or a lease taken, which is checked by asserting the fake saw no token request and no reserve write. The mutation: doing the check after `openProvider` (`internal/runner/runner.go:586`).
7. **The bucket conditions stop covering what they covered.** Pinned by the golden-plan test (`internal/infra/tf/testdata/golden/`, regenerated by `scripts/gen-golden-plans.sh`) plus a runner-level test that a job service account's condition still names `runs/`, `cache/` and `locks/`. The plan does **not** pretend this is not a regression: the test asserts the broadened condition (one condition on each prefix), and SECURITY.md says so (D4).
8. **A project-wide secret reaches a repository the owner did not opt in.** This is the ruling D2 reverses; what must not break with it is the **data-policy gate**: pinned by `TestAllowDataToStillGatesProviderRoutes` (`internal/gateway`), which asserts a provider route is refused for a repository whose canonical name is not matched by the route's written `allow_data_to`, whatever the key's scope is.
9. **The routing switch becomes a pattern.** D1a: `routing.claude` carries an explicit ID list. Pinned by `TestRoutingClaudeRefusesAGlob` (phase 1b) and by `TestRepoCannotWriteRouting` — the policy's `shared` node is operator-written and a repository's `fugaro.yaml` has no path to it.
10. **The export writes a secret value or a person's address.** Pinned by `TestExportContainsNoSecretValue` (the export is scanned for every value the fake Secret Manager holds) and `TestExportOmitsPeopleByDefault` (no `byPerson`, no kill-switch author without `--with-people`).
11. **The canonical-to-route ID map translates a model nobody pinned, or fails to translate one that was.** Pinned by `TestCanonicalMapRoundTrip` (phase 1b): for each catalog row with a Claude canonical ID, the rendered pin, the charged price and the recorded `serving_model`/`priced_as` all name the same model, and a Claude pin with no route resolves to the direct ID exactly as today.
12. **`cloneShipped` is flipped before Check 38 passes.** Pinned by the existing gate tests (`docs_checkout_test.go:123-132`, `image_refresh_steps_test.go:662`, `init_repo_clone_test.go:19`), which fail if the flip and the docs disagree — a task flipping it must land Check 38's evidence first, and Task 3.6 says where that evidence is recorded.
13. **An owner-run step is run by an agent, or a check's guard falls back to an ambient project.** Every check task's steps carry a hard-coded project and a `${P:?...}` guard; a reviewer reads each `gcloud` line for a bare ambient project, and `TestChecklistChecksAreGuarded` (phase 3) is a docs test over `docs/gcp-live-checklist.md` asserting every `## Check 48`-onward section contains a `:?` guard and names installation `fugaro`.

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
| `internal/cli/{project.go,root.go,doctor.go}` (+ tests) | `newProjectCmd()`, `policy publish`, `show`, `add-repo`; the doctor checks | 1.6 |
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
| `deploy/terraform/gcp/modules/project/` (new: `job.tf`, `sa.tf`, `iam.tf`), `.../modules/installation/`, `.../modules/installation/history.tf`, `scripts/gen-golden-plans.sh`, `internal/infra/tf/testdata/golden/` | one job + one job SA per (profile, account); the broadened bucket IAM; the golden plans | 3.2 |
| `internal/runner/{projectjob.go (new),runner.go,provision.go,project.go}`, `internal/backend/backend.go` (+ tests) | the profile/account from `CLOUD_RUN_JOB`, the slug binding, the remote from `task.json` + policy, admission, before any token or lease | 3.3 |
| `internal/backend/gcp/{run.go,names.go,logs.go}`, `internal/cli/{run.go,runs.go,logs.go,cancel.go}`, `internal/gcpfake/` (+ tests) | every job-naming caller on project jobs | 3.4 |
| `internal/cli/init_repo_clone_test.go`, `docs/checkout.md`, `internal/cli/recipes_skew.go` | `cloneShipped` flipped after Check 38 | 3.6 |
| `docs/gcp-live-checklist.md` | **Check 53** | 3.7 |
| `internal/cli/project.go` (`sync`, `add-repo`, `repos`), `internal/infra/discover*.go` | enrollment as data (D-spec §4.3) | 4.1 |
| `internal/cli/init_repo_stage.go`, `internal/infra/forget.go`, `internal/cli/init.go`, the config-transitions retirement engine | retire a repository's whole root | 4.2 |
| `internal/cli/init_repo_target.go`, `internal/cli/init.go`, `docs/checkout.md` | `init --repo` retired for clone repositories | 4.3 |
| `internal/cli/init_repo_stage.go`, `docs/`, `SECURITY.md` | baked workflows dropped (D3) | 4.4 |
| `docs/gcp-live-checklist.md` | **Check 54** (the owner's own migration of installation `fugaro`, never `belong`) | 4.5 |
| `internal/cli/accounts.go`, `internal/projectpolicy/`, `internal/infra/` | person-owned secrets, references, `accounts share` | 5.1 |
| `deploy/terraform/gcp/modules/project/`, `internal/backend/gcp/names.go` | one job per (profile, shared account) | 5.2 |
| `internal/cli/{run.go,run_oauth*.go}`, `internal/runner/`, `plugin/skills/working/reference/diagnose.md` | failover moves from the skill into `fugaro run` | 5.3 |
| `docs/gcp-live-checklist.md` | **Check 55** | 5.5 |
| `internal/cli/project_export.go` (new), `internal/report/report.go`, `internal/watch/json.go` | `project export [--push]` | 6.1 |
| `docs/gcp-live-checklist.md` | **Check 56** | 6.4 |

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
| **M** | 3.6, 3.7 | `cloneShipped` after Check 38; Check 53 |
| **N** | 4.1, 4.2, 4.3, 4.4 | the retirement of a repository root, and that nothing retires a resource another repository needs |
| **O** | 4.5 | **OWNER-RUN** — the owner's migration, Check 54 |
| **P** | 5.1, 5.2, 5.3 | **SECURITY-SENSITIVE (OAuth pool)** — a person's token, its grants and its blast radius |
| **Q** | 5.4, 5.5 | #363 rebased, Check 55 |
| **R** | 6.1, 6.2, 6.3, 6.4 | **SECURITY-SENSITIVE (export)** — no value, no person by default |
