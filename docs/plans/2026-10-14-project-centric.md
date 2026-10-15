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

**Verified today:** `internal/budget/model.go:209-224` — `PerRun` returns `ok == false` when the repository and the defaults have no per-run cap, and its comment says *"ok is false when either side is unset, because the rules deny when either comparison reads null"*. `internal/budget/rules/rules.go:310-314` (`REPOPERRUN`, `GLOBALPERRUN` macros) and `:343-346` (`fallback(own, def)` returns null when both are absent). `internal/budget/rules/rules.json.tmpl:28` (the `reserved` validator comparing against both macros). `docs/design/m9-budget-and-dashboard.md:272` says the global cap *is* the default for every repository — which is what the code and rules do not do, and D6 makes it true.

**Interfaces.** Consumes: nothing new. Produces: `Caps.PerRun()`'s new contract (the global cap substitutes for an absent repository/default cap) and the same substitution inside the rules' `fallback()`.

1. `internal/budget/model.go`: `PerRun()` returns `(*c.Global.PerRunMicros, true)` when neither the repository's own cap nor the defaults set one, and keeps `min(repo, *c.Global.PerRunMicros)` when they do. Rewrite the comment: it now says the global cap is the fallback by owner decision D6 and that the rules mirror it. **A repository cap and the defaults still only tighten** — the global cap is a floor of *applicability*, never a loosening: an absent global cap stays `ok == false`.
2. `internal/budget/rules/rules.go:343`: `fallback(own, def)` gains the global cap as its last resort, as one expression, because the rules language has no ternary. The shape the emulator must confirm: the *comparison* gains the fallback, `(X <= REPOPERRUN || (REPOPERRUN == null && X <= GLOBALPERRUN))`, where a null read compares false (so an absent global cap still denies) and `== null` is the existence test. Emit it from one helper used at both sites (`rules.json.tmpl:28`'s `reserved`, and the `released` validator at `:36`, which compares against `reserved` and so needs no change) so the two macros cannot drift apart. **If the emulator shows a null read comparing true or `== null` behaving otherwise, the task stops and reports** rather than emitting a rule that swallows the case: the whole point is that an absent cap still denies.
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

1. **Setup, hard-coded and guarded.** The block that follows the shape of `docs/gcp-live-checklist.md:1158-1177`: `P=fugaro-dev` (hard-coded, not `gcloud config get-value project`), `L=<region>`, and a first line that stops unless `P` is non-empty and equals the installation `fugaro doctor` reports (`${P:?re-run the setup block}`). Save it to a file and run it with `bash`, not an interactive `zsh` (the checklist's own note, `:1190`). **Record the state to restore**: `fugaro budget show --json` (the caps, defaults and mode), and the deployed rules' byte count from `init --firebase`'s own report.
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
package projectpolicy // imports internal/rtdb only, never internal/budget or internal/runner

const Version = 1

// Paths (P2; the caps' own helpers are internal/budget/model.go:44-80 and stay there)
func PathShared() string            { return "config/policy/shared" }
func PathRepo(slug string) string   { return "config/policy/repos/" + seg(slug) } // the $slug node
func PathAdmin() string             { return "config/admin" }

// Policy is the readable node, decoded tolerantly: an unknown key is ignored
// (so a newer policy does not break an older runner on a field it does not
// know) and Version is the only thing that can refuse it (P8).
type Policy struct {
	Version   int       `json:"version"`
	Budget    Budget    `json:"budget"`
	AllowRepos []string `json:"allow_repos"`
	Providers map[string]Provider `json:"providers,omitempty"`
	Prices    map[string]Price    `json:"prices,omitempty"`   // model_prices, the owner's overrides
	Routing   *Routing  `json:"routing,omitempty"`            // phase 1b writes it; nil = direct (D1b)
}
type Budget struct { Mode string; PerRunUSD float64; AllowedModels []string; MaxRunTokens int64 }

// MatchRepo matches a canonical "<provider>/<owner>/<name>" against an
// allow-list pattern (P5). One implementation, used by the runner and the CLI.
func MatchRepo(pattern, canonical string) bool
func Canonical(provider, repo string) (string, error)

// Decode returns (p, found, err): found=false is the absent node (today's
// behaviour), err is a node that exists and cannot be used (fail closed).
func Decode(raw []byte) (*Policy, bool, error)
```

1. `paths.go`: the three path helpers, with `seg`-style escaping copied from `internal/budget/model.go:44` (a slug is escaped the same way `/config/caps/repos/$slug` escapes it, so the two agree).
2. `policy.go`: the type above plus `Decode`, which returns `found=false` for an empty body and an error for a body that does not parse, carries a non-integer or unknown `version`, or whose `allow_repos` holds a pattern `Canonical`/`MatchRepo` rejects. **Unknown JSON keys are ignored** (unlike the version-gated env vars the price catalog's P4 describes): a policy field a newer CLI writes must not brick an older runner mid-flight, and `version` is the one thing that may.
3. `match.go`: `Canonical(provider, repo)` produces `"github/acme/app"` from a provider kind and an `owner/name`, and `MatchRepo` matches `*` within one segment only (`github/acme/*` matches `github/acme/app`, not `github/acme/a/b`; `*/acme/*` is legal, a bare `acme/*` is not — P5).
4. `version.go`: `Version`, and the two skew messages as constants (the runner's refusal and the CLI's refusal), so Task 1.3 and Task 1.6 print the same text.

**Tests:** `TestPathsEscapeASlug`; `TestDecodeAbsentIsNotFound`; `TestDecodeIgnoresUnknownKeys`; `TestDecodeRefusesBadVersion`; `TestDecodeRefusesBadAllowRepoPattern`; `TestMatchRepo` (a table: the wildcard cases, the cross-provider case, the two-`fugarosandbox` case of P5); `TestCanonical`.

**Command:** `GOFLAGS=-p=2 go test ./internal/projectpolicy/... -count=1`. **Commit:** "projectpolicy: the policy object, its paths, its version and its matcher".

### Task 1.2: the read rules — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/rules.go`, `internal/budget/rules/testdata/rules.golden.json`, `internal/budget/rules/{rules_test.go,parity_test.go,emulator_test.go}`.

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
2. Extend `TestRulesParity` (`internal/budget/rules/parity_test.go:175`): a run token can read `config/policy/shared`; it **can** read `config/policy/repos/<its own slug>`; it **cannot** read another slug's node; it **cannot** read `config/policy` (the parent); it cannot write any of them. Each of those is a case in the emulator test too (`internal/budget/rules/emulator_test.go`, tag `firebase`), because the parity test is a table over the generated JSON and the emulator is the real evaluator.
3. Regenerate `testdata/rules.golden.json` in this commit, and add the assertion that the template's policy paths equal `projectpolicy.PathShared()`/`PathRepo("x")` — a task renaming a path updates both or fails.
4. **The owner-run deploy of these rules is Task 1.7 step 2**, in the order the design fixes: **rules first, then the runner image, then `policy publish`** (§5). Until the owner deploys, the rules on the database deny the read, and the runner's Task 1.3 path never fires because no node exists yet — that is why the order is safe rather than merely conventional.

**Command:** `GOFLAGS=-p=2 go test ./internal/budget/... -count=1` then `go test -tags firebase ./internal/budget/rules/... -count=1`. **Commit:** "rules: config/policy is readable per child, config/admin is not readable by a run token".

### Task 1.3: the runner reads the policy and fails closed

**Files:** `internal/runner/projectpolicy.go` (new), `internal/runner/policy.go` (+ `policy_test.go`), `internal/runner/budget_session.go` if the read needs the client the session builds.

**Verified today:** `internal/runner/policy.go:63-120` — `resolvePolicy` builds the ceiling from `r.d.Spend` (the job env), merges the default branch's file layer and the run's own, and **treats the ceiling as optional** (`:66-72`: `Mode` only when non-empty, `PerRunUSD` only when `> 0`); `:108-113` falls back to `pricing.Embedded()` when the mode is on and no prices were loaded. `internal/runner/budget_session.go:25-52` is where the RTDB URL and the Firebase key come from, and `:128`/`:180` are where the slug keys the session.

**Interfaces.** Consumes: `projectpolicy.Decode`/`PathShared` (1.1), the RTDB URL the job env carries (1.4), the run's own token minted by the launcher. Produces: `Spend` and `policy.Effective` computed from the policy instead of the env when the node exists; `runstore.PolicyRecord.Sources` gains `policy` as a source name.

1. `projectpolicy.go`: `func (r *run) projectPolicy(ctx context.Context) (*projectpolicy.Policy, bool, error)` — one `GET` of `config/policy/shared` through `internal/rtdb`, using the session's client when the mode is on and a client built from `FUGARO_RTDB_URL` + the run's token otherwise. `found == false` ⇔ the node is absent ⇔ today's behaviour (P3). Any other error, or a `Decode` error, **halts the run** with a message naming the node (`internal/projectpolicy`'s version.go text for the version case) — never a fall through to the env ceiling.
2. `resolvePolicy`: when the policy exists, the **ceiling** is built from it instead of from `r.d.Spend`:

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
3. `runtimeVersion` skew (P8): a policy whose `version` is not `projectpolicy.Version` halts with the shared message — the runner never guesses.
4. The policy's `allow_repos` is stashed on the run for Task 1.5 (one read, one decode: `projectPolicy` is memoized like `defaultBranchFile` at `internal/runner/policy.go:30-51`).
5. `recordPolicy` (`internal/runner/policy.go:167`) names the source: `policy.sources` carries `policy=config/policy/shared` for the moved keys, so a run's `result.json` says where its ceiling came from. `fugaro diagnose` and `report` read that field unchanged.

**Tests:** `TestRunnerReadsPolicyWhenTheNodeExists`, `TestRunnerUsesEnvCeilingWhenTheNodeIsAbsent`, `TestPolicyReadErrorHaltsTheRun`, `TestPolicyParseErrorHaltsTheRun`, `TestPolicyVersionMismatchHaltsTheRun`, `TestPolicyCannotLoosenTheBranchLayer` (a branch `fugaro.yaml` asking for a *higher* cap is still ignored, `policy.Ignored` records it), `TestPolicyPricesReplaceTheEnvOverrides`. The runner's existing fakes serve the node (`internal/gcpfake` has no RTDB; the budget tests' `internal/rtdb` test server is the pattern to reuse — if it is not reachable from `internal/runner`'s tests, put the policy read behind a `Deps` field the test sets).

**Command:** `GOFLAGS=-p=2 go test ./internal/runner/... -count=1`. **Commit:** "runner: the project policy replaces the ceiling, and a policy that cannot be read halts the run".

### Task 1.4: the RTDB URL reaches every job (D11)

**Files:** `internal/infra/spec.go` (+ `spec_test.go`), `internal/infra/readiness.go` (the Firebase requirement's message).

**Verified today:** `internal/infra/spec.go:755-768` — `FUGARO_RTDB_URL` and `FUGARO_BUDGET_MODE` are written **only when the deploy-time budget mode is not off**, and the mode is read from the operator's local config (so the design's warning is exact: a job deployed with the mode off can never read the policy).

**Interfaces.** Consumes: the installation's Firebase endpoints (`lc.Endpoints`). Produces: a job env that carries `FUGARO_RTDB_URL` whenever Firebase is configured, whatever the mode.

1. `budgetEnv` (`internal/infra/spec.go:732`): write `FUGARO_RTDB_URL` whenever the installation has a Firebase URL, and keep `FUGARO_BUDGET_MODE` conditional on the mode (the mode is no longer only the deploy-time mode once a policy exists, and Task 1.3 reads the policy's mode; the env stays as the fallback for the absent-node case, unchanged).
2. The readiness gate (`internal/infra/readiness.go`): **Firebase becomes mandatory in project-centric mode** (D11). The finding is a `warning` until a policy exists (an installation that has not adopted the model is not blocked) and *blocking* once `config/policy/shared` exists — read through the operator's client at init time, so the message is "the project has a policy: this job must carry `FUGARO_RTDB_URL`, run `fugaro init --repo`". **The plan does not try to detect "project-centric mode" any other way**, and it does not lift the mode-off code path: an installation that wants the budget off stays on the per-repository path (design §5(1)), permanently, which `fugaro doctor` states.
3. `docs/`: the mode-off caveat in `docs/multi-model.md` and `docs/project-layer.md` gains one sentence: the budget mode off is not available in project-centric mode.

**Tests:** `TestBudgetEnvWritesRTDBURLWhenFirebaseIsConfigured` (mode off included), `TestBudgetEnvKeepsModeOff`, `TestReadinessBlocksAJobThatCannotReadAPublishedPolicy`.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/... -count=1`. **Depends on:** nothing from this phase but Task 1.1's path constant (for the message); it is independently revertible. **Commit:** "spec: the job carries the RTDB URL whenever Firebase is configured".

### Task 1.5: `allow_repos` and the slug binding's first half — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/runner/{runner.go,project.go}` (+ tests), `internal/cli/run.go` (+ `run_layer_test.go` or a new `run_allowlist_test.go`).

**Verified today:** the runner's bootstrap clones at `internal/runner/runner.go:789-794` and reads `task.json` at `:797-803`; the git token is minted for `spec.Repo` at `internal/runner/runner.go:586` (`openProvider`); the slug comes from `FUGARO_RUN` (`internal/cli/exec.go:157`,`:168`, `runstore.ParseRef` at `internal/runstore/runstore.go:409`) and keys the locks (`internal/runner/lockcache.go:76`), the cache (`:188`) and the budget session (`internal/runner/budget_session.go:128`,`:180`); `task.Slug(provider, repo)` is `internal/task/task.go:179`. **Nothing checks that `task.Slug(provider, spec.Repo)` equals the slug** — today the per-repository job's bucket condition is what makes a mismatch fail (design §2.2), and the provider for the check cannot come from an overridable env var (12a item 2), so it comes from the job's own provider (`config.Git.Provider`) or the policy.

**Interfaces.** Consumes: Task 1.1's `MatchRepo`/`Canonical`, Task 1.3's memoized policy. Produces: `func (r *run) admissionCheck(ctx context.Context) error`, and the CLI's advisory twin `func allowListRefusal(p *projectpolicy.Policy, provider, repo string) error` in `internal/cli`.

1. In `bootstrap`, **before** `openProvider` mints a token (`internal/runner/runner.go:586`) and before the budget session takes a lease (`internal/runner/budget_session.go:180`), against the (provider, repository) `task.json` names and the job's own provider:
   - `task.Slug(provider, spec.Repo) == <the slug FUGARO_RUN carries>` — refuse otherwise, naming both, with the sentence that the per-repository job's bucket condition used to be the only thing standing in for it.
   - `MatchRepo(p.AllowRepos, Canonical(provider, spec.Repo))` — refuse otherwise, naming the pattern list and `fugaro project add-repo`.
   - When the policy is **absent** (P3), only the slug binding is checked: an installation that has not published a policy has no allow-list, and inventing "empty means deny" there would break every installation on upgrade. **Empty `allow_repos` *inside* a published policy is deny-all**, and `policy publish` seeds it (P4), so the two cases cannot be confused.
2. `fugaro run` (the CLI, `internal/cli/run.go`) checks the same thing before launching and refuses with the same message, reading the policy with the operator's credentials. **It is advisory and the docs say so** (design §4.2/§4.3): a launcher can call `jobs.run` directly, which is why the runner's check is the one that counts.
3. `SECURITY.md`: the launcher-override boundary lands here (D10, accepted): one paragraph saying that a launcher can override a job's environment, that the budget token deliberately does not travel that way, and that after Task 3.0's probe the text says exactly what an execution's overrides expose — no more.

**Tests:** `TestRunnerRefusesSlugMismatch`, `TestRunnerRefusesSlugNotInAllowList`, `TestRunnerChecksBeforeTheTokenAndTheLease` (the fake asserts no credential request and no reserve write), `TestRunnerWithoutAPolicyChecksOnlyTheBinding`, `TestEmptyAllowListInAPublishedPolicyDeniesEverything`, `TestRunRefusesBeforeLaunch` (CLI).

**Command:** `GOFLAGS=-p=2 go test ./internal/runner/... ./internal/cli/... -count=1`. **Commit:** "runner: bind the slug and admit the repository before a token or a lease".

### Task 1.6: `fugaro project`, the doctor checks and the drift note

**Files:** `internal/cli/project.go` (+ a new `project_policy_test.go`), `internal/cli/root.go`, `internal/cli/doctor.go` (+ tests), `internal/cli/upgrade_cloud.go` (+ tests), `plugin/skills/setup/**`, `plugin/skills/working/**`, `docs/project-layer.md`, `docs/multi-model.md`, `docs/checkout.md`, `SECURITY.md`.

**Verified today:** **there is no `fugaro project` command group**; `internal/cli/project.go` is project *selection* (its header comment), and the root registers every command at `internal/cli/root.go:75-78`. `upgrade`'s drift note is `internal/cli/upgrade_cloud.go:162-177` (`jobConfigCopyNote`), and `doctor`'s severities are `internal/cli/doctor.go:39-46`.

**Interfaces.** Consumes: Task 1.1's `Policy`/`Decode`/paths, the RTDB client `fugaro budget set` uses (`internal/cli/budget.go:953` `runBudgetSet`, `openBudget` at `:160`), the operator's local config (`providers`, `claude_accounts`, the budget ceiling and `model_prices`) and the bucket config reader. Produces: `newProjectCmd()` and three subcommands.

1. `newProjectCmd()` (`Use: "project"`, registered in `internal/cli/root.go:75-78`): `policy publish`, `policy show`, `show`, `add-repo`, `remove-repo`. The plan puts `add-repo` here, in phase 1, not phase 4 (P4 and 12a item 3): between phases 1 and 4 a new repository would otherwise be unenrolled, and this one-liner is the interim path. **`sync`, `repos` and the discovery surface stay in phase 4** (Task 4.1).
2. `policy publish`: builds the policy from the operator's file — the M9a budget (`mode`, `per_run_usd`, `allowed_models`, `max_run_tokens`), providers (name, base URL, auth style, **the secret's name, never a value**, models, `allow_data_to`, `route_fee_pct`, and from the price catalog `require_zdr`/`price_policy` when those have merged), `model_prices` overrides, `allow_repos`, and (1b.1) `routing` — writes it with `PutIfMatch` (the CAS `runBudgetSet` already uses, `internal/cli/budget.go:1011`) and refuses to write a `version` above its own (P8). It prints the diff and the seeded allow-list, and it **seeds only when the node is absent or `--seed-repos` is passed** (P4), seeding from the operator's `repos:` **and** the repositories the installation has seen (`runs/<slug>/` in the bucket), each converted with `Canonical`.
3. `policy show` / `project show`: the effective project — caps (global, defaults, per-repository), the policy, the allow-list, the repositories seen, each with its source; `--json` for `watch`-style consumers. It prints the allow-list minus the seen repositories (unenrolled-but-running) and the reverse (allowed but never seen), which is the pair an operator needs.
4. `fugaro doctor`: new checks — `policy-published` (info: whether a policy exists and its version), `policy-newer-than-binary` (warning, P8), `policy-vs-jobs` (warning when a policy is published and the installation's jobs still carry an old runner image: the trap of 12a item 6, detected with the image digests `image refresh` already compares), `allow-list-empty` (warning when a policy exists whose `allow_repos` is empty).
5. `upgrade` (`internal/cli/upgrade_cloud.go:162-177`): the drift note gains the policy sentence — after a policy is published, `upgrade` says that a job's **env copy** of the ceiling is now inert for the keys the policy governs, so the drift it reports is no longer a reason to re-run `init --repo`, and what *does* still need one (mounts) is listed.
6. The skills: `plugin/skills/setup/**` (the setup skill describes `init --repo`'s policy questions and must now say they are answered once per project) and `plugin/skills/working/**` (the working skill's `fugaro.yaml` policy section). Both keep their security text; `fugaro update-skills` distributes them.
7. Docs: `docs/project-layer.md` (the layer is now what tightens the policy), `docs/multi-model.md` (the prices come from the policy when published), `docs/checkout.md` (unchanged, but the phase-3 note is added there in Task 3.6), and SECURITY.md's Task 1.5 paragraph.

**Tests:** `TestProjectPolicyPublishWritesTheNodeAndSeedsTheAllowList`, `TestProjectPolicyPublishDoesNotReseedARemovedRepo`, `TestProjectPolicyPublishRefusesAVersionItCannotWrite`, `TestProjectShowPrintsEnrolledAndSeen`, `TestDoctorPolicyChecks` (all four, including the severity), `TestUpgradeNoteSaysTheEnvCopyIsInert`, and the docs tests for the skill and doc edits.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli/... -count=1` and `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`.

**Depends on:** Tasks 1.1, 1.2, 1.3 (the shape, the rules and the reader it writes for). **Commit:** "cli: fugaro project policy publish, show and add-repo; doctor and upgrade say what the policy changes".

### Task 1.7: Check 49, the rules deploy and the adoption order — OWNER-RUN

**Files:** `docs/gcp-live-checklist.md`.

Append `## Check 49: policy as data, end to end (installation fugaro only, never belong; run by you; NOT RUN)` and its row, with **the order the design fixes: rules deploy, runner rollout, `policy publish`** (design §5), and exactly these steps:

1. **Setup and guard**, hard-coded `P=fugaro-dev` and the two-line guard of Check 48 (Task 0.4 step 1), run from a file. **Record the restore**: `fugaro budget show --json`, `fugaro project show --json` (before: nothing), and the sandbox's `fugaro.yaml` `project:` and pins.
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
