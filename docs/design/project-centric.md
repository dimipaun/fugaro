# Project-centric Fugaro

Status: the owner's decisions D1 to D12 are recorded (2026-10-14); revised after three independent opus review rounds. Awaiting the owner's approval of the reconciliation before the implementation plan is written. Refines the owner's spec ([project-centric-spec-source.md](project-centric-spec-source.md)) against the code on `main`, resolves its five open questions, and sets up an implementation plan. Nothing here is built. Citations are `file:line` on `main` (b744aeca), from two read-only code investigations and the review that checked them; every one is to be re-checked by the plan task that touches it (line numbers shift as PRs merge: #343 moved `spec.go` by about 19 lines). Items marked UNVERIFIED were not settled from the code. Related: [layered-config.md](layered-config.md), [shared-config.md](shared-config.md), [project-secrets.md](project-secrets.md), [named-oauth-tokens.md](named-oauth-tokens.md), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md), [mixed-credentials.md](mixed-credentials.md), [config-transitions.md](config-transitions.md), [generic-tool.md](generic-tool.md), [bucket-iam.md](bucket-iam.md).

## 1. Summary and recommendation

The spec asks that the **project**, not the repository, be the unit of configuration, policy, credentials and setup, and that adding a repository be lightweight: no Terraform, no confirmations. The code agrees that this is possible, and shows that **most of the weight is one mechanism**: `fugaro init --repo` copies project policy into each repository's Cloud Run job and builds per-repository cloud resources around it, so every policy change and every new teammate means re-running per-repository Terraform. The budget counters, the launch path and most of the runner are already more project-centric than the provisioning around them. Two things the first draft got wrong are corrected here and drive the shape: **sharing a job is not free of runner changes** (the runner takes its repository, slug and remote from the job's environment, not from `task.json`, §4.1) and **moving policy to the database needs a rules change, a fail-closed rule and an explicit Firebase requirement** (§5).

Recommendation, in the order that delivers value soonest:

1. **Small independent fixes first**: the four wrong flag names in budget refusals and the Anthropic key made optional (§6.1, phase 0).
2. **Take policy out of the job definition** into one project policy object read by the runner at start, with a fail-closed rule, an RTDB rules change (an owner-run rules deploy) and an enrollment allow-list that ships with it (§5, §4.3, phase 1). This ends re-running `init --repo` for changes to policy **values** (mode, caps, allowed models, prices, routing among keys already mounted). Changes to what a job **mounts** (a secret, a provider key, a named account, a launcher or operator grant) still need per-repository Terraform until phase 3 (§11).
3. **Decouple model choice from key routing** with one explicit project switch whose default stays `direct` (§6.2, phase 1).
4. **Project-scope keys by default**, a decision that reverses a ruling of the project-secrets design and so is the owner's (§6.3, phase 2).
5. **Shared project jobs** for `checkout: clone` workflows on GitHub: one Cloud Run job and one job service account per (profile, account), the runner binding its slug to the repository it clones (§4, phase 3).
6. **Enrollment as data**, migration of the owner's two installations (§4.3, §4.4, phase 4).
7. **Person-owned OAuth tokens** shared by reference, with failover in the CLI (§7, phase 5).
8. **A backing Git repository** fed by `fugaro project export`, secrets excluded (§8, phase 6).

Baked-image workflows (a repository's checkout inside its image) are inherently per repository and are the one thing the spec's hard requirement forces a ruling on (§4.4, D3). Bitbucket is first-class in phase 3 (D5): the owner's production repositories are Bitbucket, so shared jobs use a project-wide access token, after a verification task (§4.5).

## 2. What the code does today (verified)

### 2.1 The weight is in `init --repo`

- Three Terraform roots exist: installation (`fugaro init`), firebase, and **repo** (`fugaro init --repo`), state at `fugaro/repos/<slug>` (`internal/infra/workdir.go:19-23`, `repo.go:38-40`).
- Per repository the repo root creates: one Secret Manager container per logical secret with `prevent_destroy` and two operator grants each (`modules/repo/secrets.tf:4-42`); an Artifact Registry repository (`registry.tf:10-72`); a **build service account** with registry, secret, bucket and project grants (`build.tf:5-91`); a daily **check job** with an invoker grant and a paused scheduler when any workflow is baked (`check.tf:16-142`); and per workflow variant a **job service account**, bucket conditions on `runs/`, `cache/` and `locks/` **per slug**, secret grants, the **Cloud Run job** (deletion protection) and `job_runner` for every launcher (`modules/workflow/sa.tf`, `job.tf`). The golden plan for 2 workflows is **39 resources** (confirmed). Under oauth there is one variant per (workflow, named account) (`spec.go:808-821`).
- It is re-run for every repository when a launcher or operator is added (grants are per job, secret, service account and registry) and when the budget mode, per-run cap, allowed models, prices or providers change, because they are copied into each job's env (`budgetEnv`, `spec.go:659-673`, `713-752`; `upgrade` only reports the drift, `upgrade_cloud.go:162-174`).
- Confirmations: the unknown-repository gate (type `owner/name`, never satisfied by `--yes`), a prompt per missing secret, one or two Terraform applies each confirmed, `--allow-delete` for deletes and replaces, a typed project name per billable first build, and a refusal inside a coding agent (`init_repo_gate.go`, `init_review.go`). A first onboarding is roughly 15 to 30 minutes and 3 to 5 interactions (estimate).
- `image refresh` and `upgrade` already update jobs **through the Cloud Run API without Terraform** (`image_refresh_steps.go:226-260`), a precedent for API-driven job changes.

### 2.2 What the runner takes from where (the part that decides sharing)

- **Everything the repository-specific job env carries today:** `FUGARO_REMOTE` (the clone URL, computed at **deploy time** by `infra.repoURL`, `spec.go:435-441`, written to the env at `spec.go:650`), `FUGARO_GITHUB_APP_ID` (a per-repository `github_app_id`, `localcfg.go` `Repo.GitHubAppID`, `spec.go:112-113`, `656-657`), the git-secret mount, `FUGARO_MODEL_PROVIDERS` (cut down per repository, `runner/providers.go`), `FUGARO_SECRET_ENVS`, and the budget and policy variables. The runner clones from `r.d.Remote` (`cli/exec.go:67`, `runner.go:789-794`, `provision.go:30-44`).
- **The slug comes from the `FUGARO_RUN` override** (`exec.go:157`, `168`, `runstore.ParseRef`), not from `task.json`. Locks (`lockcache.go:76`), cache (`lockcache.go:188`) and the budget session (`budget_session.go:128`, `180`) key off that slug. **Nothing checks that `task.Slug(provider, spec.Repo)` equals it.** Today the per-repository job's bucket condition is what makes a mismatch fail: the job cannot read another slug's `task.json`. Repository, ref, task, workflow, account and layer do come from `task.json` (`runner.go:797-803`), and the git token is minted for `spec.Repo` (`runner.go:586`; GitHub App tokens are scoped to that repository, `gitprov/github/apptoken.go:153-176`).
- **Launchers can override any job's env today.** Every launcher holds `run.jobs.runWithOverrides` on every job (`installation/iam.tf:26-31`, `modules/workflow/job.tf:93-101`); the Cloud Run v2 API merges override env into the job's (`run-gen.go:769-778`; `args` replace, and `delayExecution` and `taskCount` are also overridable) and the runner reads nothing about the execution's overrides. By the code a launcher can launch with `FUGARO_BUDGET_MODE=off` today (no gateway, no cap). This is a **current gap**, not an inference; it is a property of the launcher role, which is a trusted role (§4.2).

### 2.3 What is already project-level

- **Budget caps.** Global caps, `--defaults` and per-repository nodes are RTDB nodes read live (`internal/budget/model.go:149-167`, `188-226`; `rules.go:341-345`). A repository with no node uses the defaults; nothing copies caps into repositories; there is no per-repository registration. The M9a policy (`per_run_usd`, `mode`, `allowed_models`, `max_run_tokens`) is the per-repository snapshot (`spec.go:713-737`); with api-key and enforce but no copied cap the run halts "set budget.per_run_usd ... and run fugaro init --repo" (`runner/gateway.go:322-327`).
- **The RTDB is optional.** `FUGARO_RTDB_URL` and `FUGARO_FIREBASE_API_KEY` are set in the job env only when the budget mode is not off (`budgetEnv`, `spec.go:713-752`; `budget_session.go:27-52`: without them "the runner is M9a's"); SECURITY.md says the mode defaults to off.
- **Tying a repository to a project.** `project:` in the default-branch `fugaro.yaml` is required and enforced by the runner (`runner/project.go:20-43`). The local config's `repos:` is a **map keyed by `owner/name`** with per-repository fields (`provider`, `base_branch`, `workflows`, `github_app_id`, `vertex`: `localcfg.go:96`, `378-395`), published into the operator-only bucket `fugaro/config.yaml`. **Job service accounts have no access to `fugaro/`** (`bucket-iam.md` §3; `names.go:363`: job prefixes are `runs`, `cache`, `locks`). Launchers are RTDB **viewers** only (`firebase/iam.tf:43-49`).

### 2.4 What is repository-bound and should not be

- **Keys.** Secret containers are per repository (`names.go:140-145`). The **Anthropic key is mandatory for every api-key run**, even when every model routes through OpenRouter, in five places: `spec.go:571-572` (mounted unconditionally; deploy blocked without a version, `readiness.go:100-107`), `agent/env.go:97-103`, `runner/gateway.go:74-76`, `internal/gateway/gateway.go:256-259` and `internal/gateway/route.go:95-97`. That is the "dummy Anthropic key". `providers` and `claude_accounts` exist **only in the owner's local config** (`localcfg.go:78-84`), are refused in the shared config (`sharedcfg.go:146-155`, for the stated reason that launchers could write the bucket) and are copied into each job.
- **Routing.** The model ID alone decides the route (`providers.go:92-99`, `route.go:142-151`); Claude is forced direct (`ClaudePatterns`, `providers.go:203-219`; `pricing.IsAlias`, `alias.go:30-43`); a Claude model has a different ID per route (`claude-sonnet-5-5` versus `anthropic/claude-sonnet-5.5`) and nothing translates between them. Routing exists only with the gateway: `stagePins` returns nil when it is off (`agentenv.go:54-57`).
- **OAuth tokens.** One secret per (repository, account), one job and service account per (repository, workflow, account), no notion of a person (`claude_accounts.go:63-85`, `spec.go:800-821`); failover only in the skill (`plugin/skills/working/reference/diagnose.md:79-97`). Per-person tokens were ruled out of scope in 0.6.5 (`named-oauth-tokens.md:15`).
- **Policy has no history and one author.** It is authoritative in RTDB, the bucket `fugaro/*`, and **a file on one laptop** (`~/.config/fugaro/projects/<name>.yaml`) holding providers, claude accounts, the budget ceiling, prices and project secrets.

### 2.5 Two budget findings

- Four refusal messages name flags that do not exist (`evaluate.go:190`, `247`; `session.go:501`, `511`: `--repo-daily`, `--repo-per-run`); the real ones are `budget set --defaults --daily` and `--defaults --per-run` (`cli/budget.go:687-691`). A plain fix.
- The global per-run cap is **not** the fallback when a repository and the defaults have none (`model.go:210-215`), although `m9-budget-and-dashboard.md:271` says it is. This is **deliberate and mirrored in the RTDB rules** (`rules.go:341-345` `fallback()` returns null so every comparison is false and the rules deny; `REPOPERRUN` at `rules.go:310-311`; the `reserved` validator, `rules.json.tmpl:28`). Changing only `model.go` breaks the rules parity test and still leaves the rules denying; making the global cap the fallback is a **fail-open-direction change to security rules** (template, emulator tests, parity test, an owner-run rules deploy). The conservative reading is that the m9 document is wrong. **Owner decision D6 (2026-10-14): change the rules and the code** so the global per-run cap is the fallback. That is a fail-open-direction change to security rules and ships as an owner-run step in phase 0: the rules template, the emulator and parity tests, then a rules deploy; the m9 design doc then says what the code does.

## 3. Principles

1. **The project is the unit.** A repository is a row: a name, an allow-list match and a slug. In the default model it carries no cloud resources of its own.
2. **Policy is data, read at start, and fails closed.** The runner reads the project policy from the database. It does **not** remove the launcher's ability to override the job env (§2.2): launchers remain trusted principals (§4.2), and the design states that plainly instead of claiming a fix.
3. **Overrides survive only if they cost nothing.** A per-repository cap is an RTDB node and survives. Anything needing per-repository infrastructure is dropped.
4. **Reuse what exists**: the budget counters, #343's project secrets, #344's retirement engine for migration, the price catalog, `report.Build` for the export, `fugaro watch --json` for a console.
5. **State the isolation actually kept.** Sharing a job removes boundaries; each is named in §4.1 and in SECURITY.md rather than argued away.

## 4. Jobs, enrollment and migration

### 4.1 One job and one job service account per (profile, account), for clone workflows

Cloud Run cannot vary the image, service account, CPU or memory per execution, so the shareable unit is "a job with these properties". A **profile** is defined at the project level: `{cpu, memory, task timeout class, secrets, allowed repositories}` on the one Fugaro base image. The installation root creates one job **and one job service account** per (profile, account). A repository's workflow selects a profile by name (`workflows.<name>.profile`, default `standard`).

**Isolation granularity is the profile.** Inside one project job every run can read every secret that job mounts, through the environment, `/proc` or the metadata server. There is **no IAM boundary between repositories sharing a job**, and no allow-list can create one: an allow-list on a project secret is only meaningful as "which profiles mount it" and "which repositories the launch check lets onto those profiles". A secret that must stay away from some repositories lives on a profile those repositories are not allowed on. **The runner enforces profile admission, not only the launch check**: before it clones or mints a token the runner reads the profile's `allow_repos` from the policy and refuses to run a repository the profile does not admit, for the (profile, account) it is actually running on (the repository picks its profile in `fugaro.yaml`, and the launch check is advisory, §4.3). The same admission is what gives §7's per-reference repository lists their effect. (This is also why §7 gives each account its own job service account: runtime fetching of any shareable token by one shared service account is rejected for the same reason.)

**`checkout: clone` is the prerequisite** (the 0.7.1 mode, gated by `cloneShipped`; the job runs `fugaro-base`, the runner clones and installs the tools `mise.toml` names). **New runner behavior is required**, not just a shared job:

1. The runner computes the clone URL from `task.json`'s repository, the provider and the project policy (a custom clone URL lives in the policy), instead of `FUGARO_REMOTE` from the job env. The provider comes from `task.json`'s repository reference, the way `task.Slug(provider, repo)` takes it (the plan confirms where it is carried).
2. **The slug is bound:** the runner refuses to start unless `slug == task.Slug(provider, spec.Repo)` and the repository is enrolled (§4.3). The check runs **before** it mints a git token, takes the budget lease or reads anything. Today only the per-repository bucket condition stands in for this.
3. The GitHub App ID and key become project policy (one App per project, §4.5); `FUGARO_MODEL_PROVIDERS` and `FUGARO_SECRET_ENVS` per repository disappear in favor of the policy and the profile's mounts; the job's `workflows.<n>.secrets` per repository (§6.3) are not supported on shared jobs.

What this changes, honestly:

| Today | Project-centric | What is lost |
|---|---|---|
| Job and job service account per (repo, workflow, account) | Job and service account per (profile, account) | Per-repository service accounts and per-repository GitHub Apps: a project with several Apps cannot share jobs (SECURITY.md tells users to use one App per repository if that matters; D5). |
| Bucket conditions per slug on `runs/`, `cache/`, `locks/` | One condition on each prefix | **A regression of a documented 0.7.0 property.** SECURITY.md lists "poison `cache/`; forge or steal `locks/`" among what 0.7.0 removed. A run, which is an untrusted agent unlike a launcher, could poison another repository's cache (cross-repository code execution on its next run), forge or steal locks, and rewrite another repository's queued `task.json`. With one GitHub App this is not worse than the App key's reach, but it is not "launchers can already write `runs/`": launchers are people, runs are not (D4). |
| Secret containers and grants per repository | Project secrets granted to the project's job service accounts | Per-repository secret access (§6.3). |
| Registry, build service account, check job and scheduler per repository | None for clone repositories (`spec.go:822-829`: clone repositories need no check); one project-level base-image check | Nothing. Clone repositories get an inert registry today (`checkout.md:81`). |
| Launcher and operator grants per job, secret and registry | Per project job | Nothing: every launcher already holds every job. |
| Policy in each job's env | Policy read at start | The env-copy snapshot; see §5 for what replaces it. |

Unchanged and safe to share: the history job reads the whole bucket (`history.tf:160-169`), logs are selected by job and execution name (`logs.go:193-194`), `max_parallel` counts `fugaro-` jobs (`run.go:222`). **Every caller of the job naming changes**: `Launch` (`run.go:107`), logs, cancel and `CheckRunExecution` (`run.go:92-101`), which must compare the execution's own `FUGARO_RUN` (already read from the execution at `run.go:389-390`) with the run.

### 4.2 Launchers are trusted; the override surface

A shared job cannot carry per-repository policy in its env, and **launchers can override any job's env** (§2.2). The design does not claim to close that. It states the boundary: a launcher is a trusted principal who can already start any run and spend the budget; the money and data limits that matter are enforced in the gateway and the rules against the run's own token. The budget token deliberately does not travel as an override (`m9-budget-and-dashboard.md:582`). **What stays in env after §5**: `FUGARO_RUN`, the timeout, and the pointer to the policy (`FUGARO_RTDB_URL`, `FUGARO_PROJECT`, `FUGARO_BUCKET`), all overridable by a launcher. Owner decision **D10**: (a) accept it and document it in SECURITY.md (recommended for now), or (b) narrow the launcher role to `run.jobs.run` and carry `FUGARO_RUN` through a claim object found by execution name, plus a runner check of the execution's own overrides (a larger change, UNVERIFIED: whether Cloud Run exposes an execution's overrides to the container; `run.go:385-390` already reads the execution's own environment to find `FUGARO_RUN`, and whether that exposes the overrides is part of the check). One live check should confirm the current gap before SECURITY.md states it.

### 4.3 Enrollment (open question 2): an allow-list in the policy, checked by the runner

**Decision.** A repository is tied to a project by three things, all data:

1. Its default-branch `fugaro.yaml` names the project (`project:`), already enforced by the runner.
2. The project's **allow-list**, a new policy key `allow_repos` (globs such as `acme/*`, `acme/service-*`; **not** the existing `repos:` map, which is keyed by exact name with per-repository fields). It lives in the policy object the runner can read (§5), **not** in the bucket (job accounts cannot read `fugaro/`). **Default empty means no repository is enrolled**; `["*"]` is an explicit choice. It is checked by the runner before it mints a token or takes a lease (§4.1) and by `fugaro run` before launch; the CLI check is **advisory**, because a launcher can call `jobs.run` directly (§4.2).
3. Provider-side access: the GitHub App installed on the repository.

**No registration record.** The first draft proposed an RTDB registration written at first use; no principal can write it (launchers are viewers, run tokens write only their own `runs/`, `spend/` and `agents/` nodes). The set of repositories that have run is already derivable from the nodes the runner writes (the bucket's `runs/<slug>/` prefix and Firestore `spendDaily` are the durable sources; the RTDB run ledger, `agents/` registry and counters are pruned after about 8 days), so **`fugaro project repos` lists them from there** with the allow-list. **Discovery is optional and explicit**: `fugaro project sync` lists the GitHub App's installed repositories and offers to add the missing ones to the allow-list (a data write by an operator, confirmed once per batch); `fugaro project add-repo owner/name` adds one.

**Ordering.** The allow-list and the runner's checks ship in **phase 1** with the policy object and are required by phase 3's shared jobs from the first day. There is never a state in which a shared job serves a repository that has not been allowed.

### 4.4 Baked workflows (the hard requirement) and migration (open question 3)

A **baked** workflow puts the repository's checkout and warmed dependencies into the job image and cannot be shared. The spec's hard requirement says to drop per-repository overrides rather than keep per-repository Terraform. Recommendation: phases 3 and 4 support clone workflows only; baked workflows stay on the existing per-repository path, unchanged, until phase 4 ends, and are then **dropped** (the base image carries every runtime, and `checkout: clone` plus `mise install` costs seconds: Check 41 measured a 10.5 s median cold start) or **re-implemented as per-repository jobs created through the Cloud Run API**, adopted by labels, with no Terraform (the `image refresh` precedent). Owner decision D3.

**Migration with the no-backward-compat rule.** Until release only the owner's **two installations** exist: `fugaro` and `belong`. Installation `fugaro` holds `dimipaun/fugaro` (GitHub, baked, with a check job), `edgeappinc/fugarosandbox` (Bitbucket, baked, no check job) and `dimipaun/fugarosandbox` (Bitbucket, listed in the local config with no workdir on this machine: stale or onboarded elsewhere, UNVERIFIED). Installation `belong` holds the production repositories `edgeappinc/edgeserver` and `edgeappinc/edgeweb` (both Bitbucket, baked). **No agent ever touches `belong`**: every live step of this design is owner-run on `fugaro` and a sandbox repository, and moving `belong` is the owner's own work after the model is proven on `fugaro` (D2 changes `secrets set` there too, and D3 drops baked workflows, which both production repositories use). Migration is "re-run init under the new model": deploy the project jobs, switch launches, then retire each repository's root. [config-transitions.md](config-transitions.md)'s retirement engine covers a changed `fugaro.yaml`, **not the retirement of a whole repository root** with `prevent_destroy` secret containers and a registry (its plan Tasks 1 to 5 do not cover full offboarding); the plan adds a "retire a repository root" task on top of it (state removal, then the label-driven delete of the old jobs, service accounts, grants and registries). **Secret values cannot be carried over**: no person can read them (`secrets.tf:31-42`), so the project-scope secrets are re-entered. With Bitbucket first-class in phase 3 (D5), the Bitbucket sandbox (`edgeappinc/fugarosandbox`) is the live-check repository for phases 3 and 4; a GitHub-specific check also needs a GitHub repository, and `dimipaun/fugarosandbox` is Bitbucket, so one would have to be created.

### 4.5 Providers

**GitHub:** one App, one App ID and key as project policy, a token minted per repository by the runner. A project that uses several Apps cannot share jobs. **Bitbucket (D5: in phase 3):** today each repository has its own access token (`bitbucket-token`, shared across repositories by #343's project scope where the owner chooses). Shared jobs need a project-wide credential: a Bitbucket workspace or project access token, whose scoping is UNVERIFIED and is the **first task of phase 3** (verify it can read and push to exactly the project's repositories, and what it can be used for besides). A runtime fetch by secret-name prefix with an IAM condition (UNVERIFIED) is the fallback and reintroduces the shared-service-account exposure of §4.1. If the verification fails, Bitbucket projects stay on the per-repository path and the owner is told before phase 3 starts.

## 5. Policy as data (phase 1)

**Decision.** One **project policy object** in RTDB, `config/policy`, readable by run tokens and writable only by admins (IAM-only writes, as for the caps: the rules grant no client write to `config/*`, `rules.json.tmpl:3-21`, `firebase/iam.tf`). Three corrections to the first draft:

1. **Firebase becomes mandatory in project-centric mode** (D11). Today the runner talks to RTDB only when the budget mode is not off (§2.3), and the mode is itself part of the policy being moved, which is circular. In project-centric mode every installation has Firebase and the budget backend on; an installation that wants budget mode off stays on the per-repository path, which makes **a permanent second code path** for budget-off installations. This is a decision, not a detail.
2. **The RTDB rules need a read rule**: `config/policy` is not readable by a run token under the current rules (child reads exist only for `caps/global`, `caps/defaults`, `caps/repos/$slug`, `limits`, `mode`, `kill/*`). **RTDB read grants cascade to children**, so a `TOKEN` read on `config/policy` itself would make every child readable by every run token and defeat the narrowing below; the rule must be per child, the way `config/caps` works: `config/policy/shared` readable by a run token, `config/policy/repos/$slug` readable only for the token's own slug, and an admin-only `config/admin`. The new nodes need emulator and parity tests and an **owner-run rules deploy**. Because every run token can read it (and a prompt-injected agent can take the token through `/proc`), **the readable node holds only what the runner needs and nothing personal**: no emails, no account owners; repository-specific parts sit under `$slug` nodes the way `caps/repos/$slug` does. Admin-only metadata (owners, who set what) lives in an admin-only `config/admin` node and in the export.
3. **The runner fails closed.** `resolvePolicy` (`runner/policy.go:63-107`) treats the ceiling as optional today: an empty mode, nil `AllowedModels` and no cap constrain nothing. In project-centric mode a **missing or unreadable policy halts the run** with a message that says which node it could not read; it never falls back to an empty ceiling (which would be less constrained than today's env copy).

**What "project-centric mode" is, and how existing runs keep working.** The mode is **the existence of `config/policy/shared`** (`fugaro project policy publish` creates it; the runner tests that child, because a read of the parent is denied under the per-child rules), not a flag in the job env that a launcher could clear. **The RTDB URL must reach every job**: `budgetEnv` (`spec.go:736-750`) writes `FUGARO_RTDB_URL` only when the **deploy-time** budget mode is not off, so a job deployed with the mode off never reads the policy. Phase 1 changes `budgetEnv` to write the RTDB URL whenever Firebase is configured; adopting it costs one `init --repo` per repository plus a runner image rollout, in the order **rules deploy, runner rollout, `policy publish`**. This is D11's mechanism. When RTDB is configured and the node is **absent**, the runner behaves exactly as today (policy from the job env, the per-repository path), so nothing changes for an installation that has not published a policy; when the node **exists**, it governs, and a node that exists but cannot be read or parsed halts the run. `policy publish` **seeds `allow_repos` from the local config's `repos:` map (exact names) by default**, so publishing a policy never un-enrolls a repository that runs today; globs are added explicitly.

**Content.** What is copied into jobs and the local file today: the M9a policy (`mode`, `per_run_usd`, `allowed_models`, `max_run_tokens`), providers (name, base URL, auth style, the **secret name, never a value**, models, `allow_data_to`, `route_fee_pct`, and from the price catalog `require_zdr` and `price_policy`), price overrides (**the history job reads them from env today and moves with them**, `m9-budget-and-dashboard.md:315`), routing (§6.2), the claude-account definitions without owner identity, profiles (§4.1) and `allow_repos` (§4.3). **Two per-run caps exist** (the M9a `per_run_usd` and the RTDB `config/caps/*` per-run); the policy keeps `per_run_usd` as a ceiling the caps can only tighten, and the plan consolidates the messages so a user sees one. The shared-config restriction on `providers` and `claude_accounts` (§2.4) is **not lifted**: policy goes to RTDB, and a second carrier would drift; that restriction's current validity after 0.7.0 is for the owner to revisit separately.

`fugaro project policy publish` writes the policy from the operator's file (and `fugaro project show` prints the effective project: caps, defaults, policy, allow-list, repositories seen); the local operator file shrinks toward identity, endpoints and caches. Policy gets history through the export (§8).

**Budget, as the spec asks, with the commands that already exist.** `budget set --global` is the overall project cap; `--defaults` is the default per-repository cap, applying to every repository including ones never configured; `--repo` is a per-repository override, kept because it is an RTDB node and costs nothing. **Open question 1 resolved: the only per-repository overrides that survive are the cap and the kill switches**; everything needing per-repository infrastructure is dropped.

## 6. Keys and routing

### 6.1 Neither key mandatory (phase 0)

Mount `anthropic-api-key` whenever the secret has a version (a fact at init time about data, not about policy, so flipping the routing switch never needs Terraform); make the gateway's Anthropic upstream optional and refuse a direct Claude call with a clear message when there is no key; drop the demands at the five sites in §2.4 and the readiness block. Independent of everything else.

### 6.2 Model choice decoupled from key routing (phase 1)

The model and the route become two settings. A route entry in the project policy lists the models it serves, and a stage's pin names a model; the route is resolved from policy, not from the ID alone. The spec's default (Anthropic models go direct when an Anthropic key exists, else through OpenRouter; or one OpenRouter key for everything) is expressed as a project setting **`routing.claude: direct | openrouter | auto`**. Two decisions, split because the first draft hid the bigger one inside the smaller:

- **D1a.** The merged ruling ([mixed-credentials.md](mixed-credentials.md) §4, §8) requires **one explicit `anthropic/<id>` per route entry, never a pattern**, in the local operator config or the shared project layer, never in a repository's `fugaro.yaml`. A switch that covers every Claude model is pattern-like, and delivering it through the RTDB policy changes where it lives. This design keeps both properties: the switch carries an **explicit list of Claude IDs** (`routing.claude: {via: openrouter, models: [claude-sonnet-5-5, ...]}`), written by the operator in the local operator config or the project layer and only **delivered** to the runner through the policy; a repository can never write it. Do you accept the policy object as the delivery path and the explicit list as the opt-in? Recommendation: yes.
- **D1b.** What does an **absent** switch mean? Recommendation: **`direct`, today's behavior**; `openrouter` and `auto` must be written. The spec's "sensible default" (key-presence driven) conflicts with the merged ruling that "with no such entry ... everything is refused"; the owner rules once. Safeguard that is real, not nominal: `openrouter` and `auto` resolve to a provider **only when its `allow_data_to` is written**, as a list or the explicit `["*"]`. Under #343's rule an absent `allow_data_to` already means every repository, so the safeguard must require it to be written; `fugaro validate` prints the resolution (or says it cannot resolve it offline, §11), and a project with no Anthropic key and Claude pinned fails validation with a message that names `routing.claude: openrouter`.

Routing exists only with the gateway (`agentenv.go:54-57`), so the switch means nothing with the budget off; D11 makes that the mode of project-centric installations. **IDs need a map**: a canonical Claude ID has a different ID per route, the gateway never rewrites bodies (`provider-price-catalog.md:14`), so the translation is applied where pins are rendered (`agentenv.go:54-77`), in prices and in records: a canonical-to-route map carried by the price catalog rows. This is the one new piece with no existing code, and the cache-marker behavior of Claude through OpenRouter is unmeasured and stays a live-check item. PR #359's phase 2 (the `anthropic/<id>` claim, `IsAlias(model, claimed)`) is the mechanism; extend it with the switch rather than replace it.

### 6.3 Project-scope keys (phase 2)

[project-secrets.md](project-secrets.md) (PR #343) is the foundation: an installation-level container, `fugaro secrets set --project-wide`, a repository copy that wins over the project copy, and the §3a rule for provider keys. What #343 allows today: the four reserved names (`anthropic-api-key` and the git credential among them) can be project-scoped through `project_secrets:` **with an explicit, non-empty `allow_data_to`**; what it forbids is a reserved secret **reaching a repository the owner did not opt in** ("the DEFAULT must be safe: nothing shared until the owner says so", §3(b)).

**Decision D2 reverses that ruling, and says so**: the spec wants inheritance by default, so `fugaro secrets set` without `--repo` stores at project scope and a project-scope key reaches every enrolled repository on the profiles that mount it, including the reserved names, with no per-name allow-list. The owner stated the default-safe rule; D2 is a change of it, not an extension. With §4.1 in mind the doc says plainly: **inside a shared job a project secret has no IAM boundary between repositories** (the profile is the boundary, §4.1), and `workflows.<n>.secrets` (repository-specific application secrets) cannot be shared across a job's mounts: they become named project secrets mounted on a profile, or stay on the per-repository path. The data-policy gate for providers (`allow_data_to`, enforced by the gateway at runtime) is unaffected and stays a real control: it decides which repositories' code the gateway may route to a provider, and it does **not** keep the key out of a shared job's environment. D2 covers API keys and the git credential; `claude-oauth-token` secrets are person-owned (§7) and are not swept into project scope.

## 7. OAuth tokens (phase 5)

Today tokens are per (repository, account) with a job per (repository, workflow, account) and failover only in the skill. The spec asks for personal ownership, individual or project registration, never repository level, with a project-level token as a reference to an individual's token, and failover.

**Decision.**

- **Storage.** One Secret Manager secret per (person, account) in the project's GCP project (flat IDs and per-secret grants work regardless of the Terraform root, `project-secrets.md:18`). The owner is granted `secretVersionAdder` and `viewer` on that secret only, never `setIamPolicy`. **Granting the project's job service account `secretAccessor` on a person's secret needs `setIamPolicy`, so it is an operator step**, performed when the reference is accepted.
- **Reference.** A record `{account, owner, secret ID, allowed profiles/repositories}` in the policy, writable only by operators; the owner's identity sits in the admin-only node, not the run-readable one (§5). It holds no value. `fugaro accounts share <name>` proposes it after the owner confirms; an operator accepts it; the owner can retire it.
- **Mounting.** A secret env var is fixed in the job definition, so a shared account means one project job and **one job service account per (profile, account)**, which keeps each person's token readable only by the runs that use that account. A new shared account therefore means a new job per profile: an installation apply, or an API-created job (the `image refresh` precedent); the plan picks one given the "no Terraform per addition" goal. The alternative (the runner fetches the token by name at boot with one shared service account) is rejected for the §4.1 reason.
- **Failover.** Move the skill's logic into `fugaro run`: when a run ends on a usage limit, retry on the next account, the launcher's own accounts first then shared ones, with the tried-list of the existing design. The launcher's identity is already recorded (`runstore/launch.go:12-29`, the registry's `requestedBy`). Claude Code's usage-limit text is UNVERIFIED (U4 of the named-accounts design).
- **Scope of "individual level".** The spec may mean tokens an individual registers once across projects; this design scopes a person's secret to one GCP project. Cross-project sharing is out of scope and flagged for the owner (D12).
- **No repository level.** The per-repository copies, `secrets set claude-oauth-token-* --repo` and the init prompts go away with the per-repository path; `agent.oauth_token` stays as a preference.

Residual risks stated: a project job that mounts several people's tokens widens the `/proc` and metadata-server exposure of [mixed-credentials.md](mixed-credentials.md) §6 to every repository sharing that job; OAuth spend is notional and no per-person budget exists (`runstore/cost.go:62-70`). PR #363 (the OAuth reviewer stage) multiplies per-repository variant jobs and should wait for this phase (D9).

## 8. The backing Git repository (phase 6; open questions 4 and 5)

A project may name a **backing repository**: a Git repository that is a record, not a unit of execution (it is not enrolled and never runs). `fugaro project export [--push]` writes, reusing `report.Build` and the existing readers:

- `policy/`: RTDB `config/*` (caps, defaults, mode, limits, kill switches with when and why) and the policy object;
- `config/`: the bucket's shared config, project layer and recipes verbatim;
- `config/operator.yaml`: the operator's local config without identity and endpoints, secrets by **name** only. The most valuable item, because it exists nowhere else today;
- `inventory/`: secrets metadata (Secret Manager list; launchers already hold it, `iam.tf:15-19`), enrolled repositories and project jobs;
- `spend/`: Firestore `spendDaily` (final days only, so history is never rewritten) and per-run CSVs from `result.json`. **People's emails** (`byPerson` spend, who set a kill switch) are committed to a git repository: they are omitted by default (a hash is reversible with a dictionary of the team's addresses), with an opt-in to keep them.

Not exported: Terraform state (rebuildable, may hold sensitive values) and transcripts (large, may hold code). **What the export lets you reconstruct** is the project's configuration and policy, and an account of spend over time; it does **not** make the project reconstructable end to end: secret values must be re-entered (no person can read them), and Firestore history, run records, the GitHub App and the Firebase project itself are not in it.

**Cadence (question 4): both.** A manual `fugaro project export` ships first (it needs the operator's local file and git credentials). A scheduled entry on the history job (sweep every 15 minutes, rollover daily: `history.tf:174-260`; its service account already has RTDB admin) is later, needing a deploy key or App token stored as a project secret (UNVERIFIED that the existing App can push to the backing repository); a hook in `init` and `budget set` captures local policy changes.

**Secrets (question 5): excluded, with an inventory.** No value is ever written, and no person holds secret-read today (`secrets.tf:31-42`). If encryption is ever wanted: SOPS with GCP KMS (operators decrypt with their own gcloud credentials) plus an offline age recipient as break-glass, because the KMS key dies with the GCP project. Nothing in the repository uses KMS, SOPS or age yet; an opt-in for later.

## 9. The desktop console (spec 6, forward-looking)

No work in the plan. `fugaro watch --json` is already a project-wide feed (`watch/json.go:97-115`) that an Electron app can spawn, with `budget kill|resume` and `report --json` for actions. Views still missing, for the console's own design: spend over time, live compute dollars, per-model and per-person breakdowns, a default-versus-override caps table, configuration and recipe views, a secrets and accounts inventory with owners, OAuth quota, logs and transcripts, backing-repository history.

## 10. Effect on work in flight

| Item | Effect | Recommendation |
|---|---|---|
| #343 project secrets | The foundation for §6.3. **Merged** (47a73b9f, Tasks 1 to 11); its repository-root grant wiring and the ExtraSecrets union are the parts this design supersedes once repositories stop having roots. It defaults to deny; D2 changes that. | Done; the owner rules D2 separately. |
| #361 price catalog | Neutral or helpful; `require_zdr` and `price_policy` become policy provider fields (§5); `anthropic/*` rows wait on the opt-in; needs the canonical-ID map (§6.2). | Continue. |
| #362 registry remote repository | Installation-level; no conflict. | Continue. |
| #359 OpenRouter senior reviewer | Its phase 2 is the mechanism for §6.2. | Continue, extend with the routing switch. |
| #363 OAuth reviewer stage | Multiplies per-repository variant jobs; widens token blast radius under sharing. | **Hold** until phase 5 (owner, D9). |
| config transitions (#344, merged design) | Its inventory is churn this design removes; its retirement engine is the migration tool. | Implement only the core (plan Tasks 1 to 5, started) and re-scope the rest (D8). |
| `budget preview` (#351, #357), cost per stage (#348) | Project-level budget preview reads the same caps; the per-stage cost data is unaffected. | Continue. |
| `checkout: clone` (#342 merged, Task 18 in review) | The prerequisite for phase 3. | Task 18 and the owner-run Check 38 stay on the critical path; the flip PR follows Check 38. |
| #360 Task 18 (Check 38, setup-skill offer) | On the phase 3 critical path; it does not flip `cloneShipped`. | Continue. |
| mono-repo and many-repos designs | Superseded by this design. | Dropped (PRs #355 and #356 were closed). |

## 11. Phases

Each phase ships independently and leaves the system working. **Cross-cutting items every phase carries**, which the first draft omitted: SECURITY.md updates (phases 1, 2, 3 and 5), user docs, the plugin skills (setup and working), `fugaro doctor` checks for the new state, `upgrade`'s drift note, the dashboard where it reads the changed state, launcher grants, **version skew** (an old CLI against a new policy: the runner and the CLI must agree on a policy version and refuse a mismatch with a clear message), and the fact that `fugaro validate` is offline and **cannot see the routing resolution** (it prints the resolution when it can read the policy and says "unresolved offline" otherwise).

0. **Small fixes.** The four wrong flag names (§2.5); the Anthropic key made optional (§6.1); the global per-run cap as the fallback (D6): the rules template, emulator and parity tests, the code, the m9 document, then an owner-run rules deploy.
1. **Policy as data.** `config/policy` with per-child read rules (an owner-run rules deploy), the fail-closed runner (mode = the node exists), `allow_repos` seeded from `repos:` and the runner's and CLI's allow-list and profile-admission checks, `fugaro project policy publish` and `fugaro project show`, the local file shrinking; Firebase mandatory in project-centric mode (D11). **Phase 1b**, after #359's phase 2 and the price catalog (#361) have merged: the routing switch and the canonical-ID map. What phase 1 does and does not remove: policy **values** no longer need `init --repo`; anything that changes a **mount** still does until phase 3.
2. **Project-scope keys.** #343 is merged; `secrets set` default scope (D2), mounted on the repository's job until phase 3 and on profiles after; SECURITY.md.
3. **Shared project jobs for clone workflows (GitHub and Bitbucket, D5).** First task: the Bitbucket project-wide token verification (§4.5). Profiles, jobs and service accounts per (profile, account), the runner binding (`slug == task.Slug(...)`, remote from task and policy), broadened bucket IAM with SECURITY.md stating the 0.7.0 regression (D4), `fugaro run` and `CheckRunExecution` and every job-naming caller on project jobs, `cloneShipped` flipped after Check 38. Live check on the Bitbucket sandbox, plus a GitHub repository for the GitHub-specific parts (one has to be created; `dimipaun/fugarosandbox` is Bitbucket).
4. **Enrollment and migration.** `project sync`, `add-repo`, `project repos`, `init --repo` retired for clone repositories, the "retire a repository root" task, migration of the owner's installations, the fate of baked workflows (D3).
5. **OAuth pool.** Person-owned secrets, references, `accounts share`, CLI failover; #363 rebased.
6. **Backing repository.** `fugaro project export`; the scheduled export later.

Each phase's live check is owner-run, on installation `fugaro` and a sandbox repository only, never the belong project, with no agent-run apply.

## 12. Open questions, resolved, and decisions for the owner

| Spec question | Resolution |
|---|---|
| 1. Repo-level overrides kept? | Only the per-repository **cap** (an RTDB node) and kill switches. Everything needing per-repository infrastructure is dropped (§5). |
| 2. How is a repo tied to a project? | An allow-list in the policy, checked by the runner and the CLI; no init step; optional `project sync` (§4.3). |
| 3. Migration | Re-run init under the new model with the retirement engine plus a "retire a repository root" task; clone repositories first; secrets re-entered (§4.4). |
| 4. Export cadence | Both: manual first, scheduled later (§8). |
| 5. Encryption of secrets in the backing repo | Exclude secrets; inventory by name; SOPS plus KMS (with an age break-glass) only if ever wanted (§8). |

Decisions, made by the owner on 2026-10-14 (the question tool, recommended option first; D6 is the one answer that is not the recommendation):

| # | Decision | Answer |
|---|---|---|
| D1a | How the Claude-through-OpenRouter opt-in is expressed | **An explicit ID list**, operator-written in the local config or project layer (`routing.claude: {via: openrouter, models: [...]}`), delivered through the policy; a repository can never write it. |
| D1b | What an absent routing setting means | **`direct`**, today's behavior; `openrouter` and `auto` must be written, and resolve to a provider only when its `allow_data_to` is written. |
| D2 | Project-scope keys by default | **Yes**: storing at project scope is the opt-in; it reaches every enrolled repository on the profiles that mount it. Covers `anthropic-api-key` and the git credentials, not `claude-oauth-token` (person-owned, §7). It reverses #343's default-safe rule. |
| D3 | Baked workflows | **Dropped after phase 4.** |
| D4 | Loss of per-repository bucket isolation | **Accepted and documented** in SECURITY.md as a regression of a 0.7.0 property; repositories that must stay apart use different profiles. |
| D5 | Bitbucket | **First-class in phase 3**, via a project-wide Bitbucket access token after a verification task (§4.5). |
| D6 | Global per-run cap as the fallback | **Change the rules and the code** (not the recommendation): an owner-run rules deploy in phase 0 (§2.5). |
| D7 | Scheduled export | **Manual first, scheduled later** (resolved by default: no objection; §8). |
| D8 | Config transitions | **Core only** (plan Tasks 1 to 5, already running). |
| D9 | #363 OAuth reviewer stage | **Held until phase 5.** |
| D10 | Launcher environment overrides | **Accepted and documented** now, after one live check; narrowing the role later. |
| D11 | Firebase and the budget backend mandatory in project-centric mode | **Yes** (§5). |
| D12 | "Individual level" tokens | **One GCP project**; cross-project sharing is a later design. |

## 12a. Items carried into the plan (from the final review round; none changes the design)

1. Derive the runner's profile and account from `CLOUD_RUN_JOB`, which the runner already reads, not from an env var a launcher can override.
2. `task.json` carries no provider (`task.go:47-70`): the slug-binding check must not take the provider from an overridable env var; the per-job provider (a job serves one provider) or a value in the policy supplies it.
3. Between phases 1 and 4 a new repository is unenrolled (`init --repo` writes only the local `repos:`, and `add-repo` arrives in phase 4): the plan decides the interim path.
4. Seed `allow_repos` from a source that does not depend on one laptop, and say whether a later publish re-adds a name the operator removed.
5. `allow_repos` matching includes the provider and uses canonical names.
6. Old baked runner images silently ignore a published policy until refreshed: `upgrade` and `doctor` say so.
7. `config/admin` is readable by launchers (they hold the RTDB viewer role and IAM reads bypass the rules): put no secret-adjacent data there.
8. Profile admission is a phase 3 item (profiles arrive in phase 3), not phase 1; phase 1 enforces `allow_repos` only.
9. The routing switch is an object (D1a), not a scalar; an edit to the project layer takes effect only when an operator publishes.
10. D2's reach on per-repository jobs still needs `init --repo` until phase 3.
11. Who creates a person's secret (§7): the person, with the operator accepting the reference.
12. Run-token writes in §4.3 include `outcomes/` and the global spend node.
13. The RTDB policy replaces only the ceiling; the `fugaro.yaml` layers still only tighten it.
14. The cross-cutting list names `scripts/gen-golden-plans.sh`, `docs/multi-model.md`, `docs/checkout.md` and `docs/git-providers.md`.

## 13. UNVERIFIED, to settle in the plan's first tasks

Whether a Cloud Run secret reference works inside an env override (the API type has a `ValueSource`); whether an execution's overrides are visible to the container; Secret Manager IAM conditions on a name prefix; whether a repository module can reference a secret it does not create; the project-wide Bitbucket token scope; whether the GitHub App can push to the backing repository; Claude Code's usage-limit text; the runs bucket's IAM policy size limit at scale and Google's default service-account quota (about 100 per project: the current model exceeds it at about 60 repositories, the project-job model does not); how the Firestore database is created; whether `dimipaun/fugarosandbox` is usable as the GitHub sandbox; whether the launcher override gap is exploitable in practice (the code says yes: one live check before SECURITY.md states it).
