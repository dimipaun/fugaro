# Project-centric Fugaro

Status: draft for the owner's review, 2026-10-15. Refines the owner's spec (`fugaro-project-centric-spec.md`, October 2026) against the code on `main` (b744aeca), resolves its five open questions, and sets up an implementation plan. Nothing here is built. Citations are `file:line` on `main`, from two read-only code investigations; every one is to be re-checked by the plan task that touches it. Items marked UNVERIFIED were not settled from the code. Related: [layered-config.md](layered-config.md), [shared-config.md](shared-config.md), [project-secrets.md](project-secrets.md), [named-oauth-tokens.md](named-oauth-tokens.md), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md), [mixed-credentials.md](mixed-credentials.md), [config-transitions.md](config-transitions.md), [generic-tool.md](generic-tool.md).

## 1. Summary and recommendation

The spec asks that the **project**, not the repository, be the unit of configuration, policy, credentials and setup, and that adding a repository be lightweight: no Terraform, no confirmations. The code agrees that this is possible, and shows that **most of the weight is one mechanism**: `fugaro init --repo` copies project policy into each repository's Cloud Run job and builds per-repository cloud resources around it, so every policy change and every new teammate means re-running per-repository Terraform. The budget counters, the launch path and the runner are already more project-centric than the provisioning around them.

Recommendation, in the order that delivers value soonest:

1. **Take policy out of the job definition and into one project policy object** read by the runner at start (RTDB `config/policy`, admin-write only). This alone ends "re-run `init --repo` in every repository after a policy change", before any job is shared (§5, phase 1).
2. **Make the Anthropic key optional, and decouple model choice from key routing** with one explicit project switch that counts as the opt-in the mixed-credentials ruling requires (§6, phase 0 and 1).
3. **Project-scope keys by default**: project secrets (#343) become the normal path, and storing a key at project scope is itself the opt-in (§6, phase 2).
4. **Project-level jobs** for `checkout: clone` workflows: one Cloud Run job per (profile, account) owned by the installation root, serving every repository of the project, with the repository passed in `task.json` as it is today (§4, phase 3). Cloud Run overrides cover only env, args, task count and timeout, so a job cannot be per-repository without being per-repository infrastructure; sharing it is what removes the per-repository Terraform.
5. **Enrollment as data, not infrastructure**: an operator-written project allow-list plus first-use registration (§4.3, phase 4).
6. **Person-owned OAuth tokens** shared by reference, with failover in the CLI (§7, phase 5).
7. **A backing Git repository** fed by `fugaro project export`, with secrets excluded (§8, phase 6).

Everything except phase 3's job sharing is data and CLI work. Baked-image workflows (a repository's checkout inside its image) are inherently per repository and are the one thing the spec's hard requirement forces a ruling on (§4.4).

## 2. What the code does today (verified by investigation)

### 2.1 The weight is in `init --repo`

- Three Terraform roots exist: installation (`fugaro init`), firebase, and **repo** (`fugaro init --repo`), state at `fugaro/repos/<slug>` (`internal/infra/workdir.go:19-23`, `repo.go:38-40`).
- Per repository the repo root creates: one Secret Manager container per logical secret with `prevent_destroy` and two operator grants each (`modules/repo/secrets.tf:4-42`); an Artifact Registry repository (`registry.tf:10-72`); a **build service account** with registry, secret, bucket and project grants (`build.tf:5-91`); a daily **check job** with an invoker grant and a paused scheduler when any workflow is baked (`check.tf:16-142`); and per workflow variant a **job service account**, bucket conditions on `runs/`, `cache/`, `locks/<slug>/`, secret grants, the **Cloud Run job** itself (deletion protection) and `job_runner` for every launcher (`modules/workflow/sa.tf`, `job.tf`). The golden plan for 2 workflows is **39 resources**. Under oauth there is one variant per (workflow, named account) (`spec.go:808-821`).
- It is re-run for every repository when a launcher or operator is added (grants are per job, secret, service account and registry) and when the budget mode, per-run cap, allowed models, prices or providers change (copied into each job's env: `spec.go:659-673`, `713-752`; `upgrade` only reports the drift, `upgrade_cloud.go:162-174`).
- Confirmations: the unknown-repository gate (type `owner/name`, never satisfied by `--yes`), a prompt per missing secret, one or two Terraform applies each confirmed, `--allow-delete` for deletes and replaces, a typed project name per billable first build, and a refusal inside a coding agent (`init_repo_gate.go`, `init_review.go`). A first onboarding is roughly 15 to 30 minutes and 3 to 5 interactions (estimate).
- `image refresh` and `upgrade` already update jobs **through the Cloud Run API without Terraform** (`image_refresh_steps.go:226-260`), a precedent for API-driven job changes.

### 2.2 What is already project-level

- **Budget caps.** Global caps, `--defaults` and per-repository nodes are RTDB nodes read live (`internal/budget/model.go:149-167`, `188-226`; `rules.go:341-345`). A repository with no node uses the defaults (`m9-budget-and-dashboard.md:273`); nothing copies caps into repositories; there is no per-repository registration. The claim that api-key runs need a per-repository cap is **not true in the database**. What forces per-repository action is the **M9a policy copy**: `per_run_usd`, `mode`, `allowed_models`, `max_run_tokens` are copied into each job (`spec.go:713-737`), and with api-key and enforce but no copied cap the run halts "set budget.per_run_usd ... and run fugaro init --repo" (`runner/gateway.go:322-327`).
- **The launch path.** `fugaro run` writes `task.json` and passes only `FUGARO_RUN=<slug>/<run>` plus a timeout as overrides (`backend/gcp/run.go:103-132`); repository, ref, task, workflow, account and layer come from `task.json` (`runner.go:797-803`); the runner opens the provider for `spec.Repo` and mints a token scoped to that repository (GitHub App: `gitprov/github/apptoken.go:153-176`). Cloud Run allows overrides of env, args, task count and timeout only, not image, service account, CPU/memory, volumes or secrets (Cloud Run v2 API; the budget token deliberately does not travel as an override, `m9-budget-and-dashboard.md:582`).
- **Tying a repository to a project.** `project:` in the repository's default-branch `fugaro.yaml` is required and enforced by the runner (`runner/project.go:20-43`); the slug (name plus 16 hex of a hash) keys every bucket prefix, RTDB node and resource name; the local config's `repos:` map is published into the bucket `fugaro/config.yaml`, which only operators can write since 0.7.0 (`installation/iam.tf:75-123`); launchers are installation-wide (every launcher holds `job_runner` on every job, `job.tf:94-101`).

### 2.3 What is repository-bound and should not be

- **Keys.** Secret containers are per repository (`names.go:140-145`). The **Anthropic key is mandatory for every api-key run**, even when every model routes through OpenRouter, in five places: `spec.go:571-572` (mounted unconditionally, deploy blocked without a version: `readiness.go:100-107`), `agent/env.go:97-103`, `runner/gateway.go:74-76`, `gateway.go:256-259` and `route.go:95-97`. That is the "dummy Anthropic key". `providers` and `claude_accounts` exist **only in the owner's local config** (`localcfg.go:78-84`), are refused in the shared config (`sharedcfg.go:150-155`, a restriction whose stated reason, launchers writing the bucket, stopped holding when 0.7.0 made `fugaro/` operator-only) and are copied into each job.
- **Routing.** The model ID alone decides the route (`providers.go:92-99`, `route.go:142-151`); Claude is forced direct (`ClaudePatterns`, `providers.go:203-219`; `pricing.IsAlias`, `alias.go:30-43`); a Claude model has a different ID on each route (`claude-sonnet-5-5` versus `anthropic/claude-sonnet-5.5`) and nothing translates between them.
- **OAuth tokens.** One secret per (repository, account), one job and service account per (repository, workflow, account), and no notion of a person (`claude_accounts.go:63-85`, `spec.go:800-821`); failover exists only in the skill (`plugin/skills/working/reference/diagnose.md:79-97`). Per-person tokens were ruled out of scope in 0.6.5 (`named-oauth-tokens.md:15`).
- **Policy has no history and one author.** Policy is authoritative in RTDB, the bucket `fugaro/*`, and **a file on one laptop** (`~/.config/fugaro/projects/<name>.yaml`) that holds providers, claude accounts, the budget ceiling, prices and project secrets and is nowhere else.

### 2.4 Two budget bugs to fix regardless

- The global per-run cap is **not** the fallback when a repository or the defaults have none (`model.go:216-222`), although the design says it is (`m9-budget-and-dashboard.md:272`).
- Four refusal messages name flags that do not exist (`evaluate.go:190`, `247`; `session.go:501`, `511`); the real ones are `budget set --defaults --daily` and `--per-run`.

## 3. Principles

1. **The project is the unit.** A repository is a row: a name, an allow-list match and a slug. It carries no cloud resources of its own in the default model.
2. **Policy is data, and it is read, not copied.** The runner reads the project policy at start. No job definition carries policy, so no policy change needs an apply (and a launcher's `runWithOverrides` can no longer override it: the investigation flags that every launcher can override any job's env today, UNVERIFIED as an exploit; policy in env is a weakness this removes).
3. **Overrides survive only if they cost nothing.** A per-repository cap is an RTDB node and survives. Anything that needs per-repository infrastructure is dropped.
4. **Reuse what exists**: the budget counters, #343's project secrets, #344's retirement engine for migration, the price catalog, `report.Build` for the export, `fugaro watch --json` for a console.

## 4. Jobs, enrollment and migration

### 4.1 One job per profile and account (clone workflows)

Cloud Run cannot vary the image, service account, CPU or memory per execution, so the unit that can be shared is "a job with these properties". Define a **profile** at the project level: `{cpu, memory, task timeout class}` on the one Fugaro base image. The installation root creates one job per (profile, account) and one job service account for all of them. A repository's workflow selects a profile by name (`workflows.<name>.profile`), default `standard`. `checkout: clone` (the 0.7.1 mode, gated by `cloneShipped`) is the prerequisite: the job runs `fugaro-base`, the runner clones from the remote derived from `task.json`'s repository (as `spec.go:435-441` derives it today) and installs the tools its `mise.toml` names.

What this changes, honestly:

| Today | Project-centric | What is lost |
|---|---|---|
| Job and job service account per (repo, workflow, account) | Job per (profile, account); one job service account | The per-repository job service account. For GitHub the App key already reaches every installed repository (`SECURITY.md:47-50`), so this is nominal there; for Bitbucket it is real (per-repository tokens, `providers.go:22`), see §4.5. |
| Bucket IAM conditions on `runs/`, `cache/`, `locks/<slug>/` per job | One condition on `runs/`, `cache/`, `locks/` | A run could write another repository's run records, cache or lock. Launchers can already write every `runs/` prefix (`SECURITY.md:36`). Stated in SECURITY.md as an accepted property of one project. |
| Secret containers and grants per repository | Project secrets (#343) granted to the project job service account | Per-repository secret access (see §6). |
| Registry, build service account, check job and scheduler per repository | None for clone repositories (they need no build and no daily check, `spec.go:822-829`); one project-level base-image check | Nothing. Clone repositories get an inert registry today (`checkout.md:81`). |
| Launcher and operator grants per job, secret, registry | Per project job | Nothing: every launcher already holds every job. |
| Policy in each job's env | Policy read at start | Nothing (§5). |

Unchanged and verified safe to share: the history job reads the whole bucket (`history.tf:160-169`); logs are selected by job and execution name (`logs.go:193-194`); `max_parallel` counts `fugaro-` jobs (`run.go:222`); the runner's repository lock and cache key off the slug (`runner/lockcache.go:76`, `187`). `CheckRunExecution` must be rebased from "the job's repository" to the execution's `FUGARO_RUN` (`run.go:86-101`, `389-390`).

### 4.2 Policy never rides in overrides

A shared job cannot carry per-repository values in its env, and launchers hold `runWithOverrides`. The budget token already travels through the bucket for the same reason. Nothing in the project-job design may put policy or secrets in an override; the only per-run override stays `FUGARO_RUN` and the timeout.

### 4.3 Enrollment (open question 2): a project allow-list plus first-use registration

**Decision.** A repository is tied to a project by three things, all data:

1. Its default-branch `fugaro.yaml` names the project (`project:`), **already enforced** by the runner.
2. The project's **allow-list**: `repos:` globs (for example `acme/*`, `acme/service-*`) in the operator-only bucket config (`fugaro/config.yaml`). **Default empty means no repository is enrolled**; `["*"]` is an explicit choice. The runner and `fugaro run` check the allow-list before they mint a git token or take a budget lease.
3. Provider-side access: the GitHub App installed on the repository, or a Bitbucket token that can read it.

**First-use enrollment.** The first `fugaro run` for an allowed repository writes a registration record (RTDB `repos/<slug>/meta`: name, provider, first seen, who launched it, base branch) and nothing else; there is no init step, no Terraform and no confirmation. **Discovery is optional and explicit**: `fugaro project sync` lists the GitHub App's installed repositories and offers to add the missing ones to the allow-list (a data write, confirmed once per batch); `fugaro project add-repo owner/name` adds one. Rejected alternatives: silent discovery from the organization (a repository the project's operators never chose would be able to spend its budget) and explicit registration per repository with an apply (the thing being removed).

Why the allow-list matters even though launchers are the only ones who can start runs: it is what stops a repository whose `fugaro.yaml` claims `project: X` from spending X's budget or reaching X's secrets, and it is the same gate shape as `allow_data_to`.

### 4.4 Baked workflows (the hard requirement) and migration (open question 3)

A **baked** workflow puts the repository's checkout and its warmed dependencies into the job image. It cannot be shared: the image differs per repository. The spec's hard requirement says to drop per-repository overrides rather than keep per-repository Terraform. Recommendation: **phase 3 and 4 support clone workflows only; baked workflows stay on the existing per-repository path, unchanged, until phase 4 ends, and are then either dropped (recommended: the base image already carries every runtime, and `checkout: clone` plus `mise install` costs seconds, measured 10.5 s median cold start, `gcp-live-checklist.md` Check 41) or re-implemented as per-repository jobs created through the Cloud Run API, adopted by labels, with no Terraform (the `image refresh` precedent)**. This is owner decision D3.

**Migration with the no-backward-compat rule.** Until release only the owner's two installations exist (`dimipaun/fugaro`, `edgeappinc/fugarosandbox`, both baked, in installation `fugaro`; never the belong project). Migration is "re-run init under the new model", using the retirement engine already designed in [config-transitions.md](config-transitions.md): deploy the project jobs, switch launches to them, then per repository `init --repo --retire` (state removal plus the label-driven delete of the old jobs, service accounts, grants and registries), keeping and re-homing the secrets as project secrets. #344's design is the migration tool; its rows A, C, D, F become moot for clone repositories, so its implementation should be scoped to the core retirement flow (§10).

### 4.5 Providers

GitHub first: one App, one key, a token minted per repository by the runner, so one job service account loses nothing. Bitbucket needs a per-repository access token today; a project workspace token (UNVERIFIED that it can be scoped acceptably) or a runtime fetch by secret-name prefix with an IAM condition (UNVERIFIED) is needed. **Phase 3 ships for GitHub; Bitbucket projects stay on the per-repository path until a design settles it** (owner decision D5; the sandbox is Bitbucket and stays a per-repository installation until then).

## 5. Policy as data (phases 0 and 1)

**Decision.** One **project policy object**, RTDB `config/policy`, readable by every run token and writable only by admins (RTDB rules grant no client write to `config/*` today: `internal/budget/rules/rules.json.tmpl:9-20`, so the write path is IAM, as for the caps). It carries what is copied into jobs and the local file today: the M9a policy (`per_run_usd`, `mode`, `allowed_models`, `max_run_tokens`), providers (name, base URL, auth style, the **secret name** (never a value), models, `allow_data_to`, `route_fee_pct`, and from the price catalog `require_zdr` and `price_policy`), price overrides, claude-account definitions and routing (§6). The runner reads it at boot through the existing config reader (`session.go:361`), exactly where the caps are read, and enforces it as a ceiling the repository's `fugaro.yaml` can only tighten (the existing rule, `runner/policy.go:63-107`).

`fugaro project policy publish` writes it from the operator's file (and `fugaro project show` prints the effective project: caps, defaults, policy, allow-list, enrolled repositories). The local operator file shrinks to identity, endpoints and caches; policy gets history through the export (§8). The shared-config restriction on `providers` and `claude_accounts` is lifted for the operator-written bucket config, because its stated reason no longer holds since 0.7.0.

Budget changes ride on this phase: the two bugs in §2.4 are fixed first (phase 0), the per-run cap and mode move out of the job env, and the project cap model is the one the spec asks for with the commands that already exist: `budget set --global` (the overall project cap), `--defaults` (the default per-repository cap, applying to every repository including ones never configured), `--repo` (a per-repository override, kept because it is an RTDB node and costs nothing: **open question 1 resolved: the only per-repository override that survives is the cap, plus kill switches**; everything needing per-repository infrastructure is dropped).

## 6. Keys and routing (phases 0, 1 and 2)

### 6.1 Neither key mandatory (spec 4.3)

Mount `anthropic-api-key` only when it exists and the project policy allows a direct Claude route; make the gateway's Anthropic upstream optional and refuse a direct Claude call with a clear message when there is no key; drop the demands at the five sites in §2.3 and the readiness block. This is independent of everything else and ships first (phase 0).

### 6.2 Model choice decoupled from key routing

The model and the route become two settings. A route entry in the project policy lists the models it serves; a stage's pin names a model, and the route is resolved from policy, not from the ID alone. The spec's default (Anthropic models go direct when an Anthropic key exists, otherwise through OpenRouter; or one OpenRouter key for everything) is expressed as one project setting, **`routing.claude: direct | openrouter | auto`, default `auto`**. Two consequences need stating:

- **It restates the mixed-credentials ruling.** [mixed-credentials.md](mixed-credentials.md) (§4, §8) allows a Claude model through a provider only by an explicit opt-in on a route entry, one `anthropic/<id>`, never a pattern, never in a repository's `fugaro.yaml`. `routing.claude: openrouter` (or `auto` resolving to OpenRouter) is a broader opt-in than one entry per model. Owner decision **D1**: the project-level switch counts as the explicit opt-in. The repository still cannot set it. With the §3a rule of #343 (an absent `allow_data_to` means every repository) an automatic fallback would send **every api-key repository's Claude traffic, and its code, to OpenRouter**, so `auto` should resolve to OpenRouter only when the operator has also allowed the provider for those repositories (`allow_data_to`), and `fugaro validate` must print the resolution.
- **IDs need a map.** A canonical Claude ID has a different ID on each route. The gateway never rewrites bodies (`provider-price-catalog.md:14`), so the translation is applied where pins are rendered (`agentenv.go:54-77`), in prices and in records: a small canonical-to-route map carried by the price catalog rows. This is the one new design piece with no existing code. The cache-marker behaviour of Claude through OpenRouter is unmeasured and stays a live-check item ([mixed-credentials.md](mixed-credentials.md) §4).

PR #359's phase 2 (the `anthropic/<id>` claim, `IsAlias(model, claimed)`) is the mechanism; extend it with the switch rather than replacing it.

### 6.3 Project-scope keys by default (phase 2)

#343 (project secrets) is the foundation: an installation-level container, `fugaro secrets set --project-wide`, and a rule that a repository's own copy wins. The spec wants inheritance as the default: **`fugaro secrets set` without `--repo` stores at project scope, and a project-scope key reaches every enrolled repository** (`allow_data_to` absent or `["*"]`), including the reserved names `anthropic-api-key` and the git credential, which the #343 design currently lists as impossible. **Decision D2**: storing a key at project scope is itself the opt-in, and the doc says plainly that every enrolled repository can then use it. The repository-scope copy stays as an escape hatch only on the per-repository path (§4.4) and is dropped with it. `workflows.<n>.secrets` (repository-specific application secrets, for example a private registry token) cannot be shared across a job: they become named project secrets with an allow-list, or stay on the per-repository path.

## 7. OAuth tokens (phase 5)

Today tokens are per (repository, account) with a job per (repository, workflow, account) and failover only in the skill. The spec asks for personal ownership, individual or project registration, never repository level, with a project-level token as a reference to an individual's token, and failover.

**Decision.**

- **Storage.** One Secret Manager secret per (person, account) in the project's GCP project, created from the installation root or the CLI (flat IDs and per-secret grants work regardless of the Terraform root, `project-secrets.md:18`). The owner is granted `secretVersionAdder` and `viewer` on that secret only, never `setIamPolicy`, so an owner cannot widen who reads it. Readers are only the job service accounts that mount it.
- **Reference.** A record `{account, owner, secret ID, allow-list (or all repositories)}` in the project policy, readable by teammates and writable only by operators (§5). It holds no value. `fugaro accounts share <name>` creates the reference after the owner confirms; the owner can retire it, which removes the reference and the mount.
- **Mounting.** A secret env var is fixed in the job definition, so a shared account means one project job per (profile, account): the number of accounts is small and `§4.1` already creates jobs per (profile, account). The alternative (the runner fetches the token by name at boot) gives every job service account read access to every shareable token and is rejected.
- **Failover.** Move the skill's logic into `fugaro run`: when a run ends on a usage limit, retry on the next account, ordered the launcher's own accounts first then shared ones, with the tried-list of the existing design. The launcher's identity is already recorded (`runstore/launch.go:12-29`, the registry's `requestedBy`). The usage-limit text Claude Code prints is UNVERIFIED (U4 of the named-accounts design).
- **No repository level.** The per-repository copies, `secrets set claude-oauth-token-* --repo` and the init prompts go away with the per-repository path; `agent.oauth_token` stays as a preference only.

Residual risks stated, not solved: a project job that mounts several people's tokens widens the blast radius of the `/proc` and metadata-server exposure ([mixed-credentials.md](mixed-credentials.md) §6) to every repository sharing the job; OAuth spend is notional and no per-person budget exists (`runstore/cost.go:62-70`; a per-person breakdown exists in Firestore, `rollup.go:418-445`). PR #363 (the OAuth reviewer stage) multiplies per-repository variant jobs and should be sequenced after this phase (owner decision D9).

## 8. The backing Git repository (phase 6; open questions 4 and 5)

A project may name a **backing repository**: a Git repository that is a record, not a unit of execution (it is not enrolled and never runs). `fugaro project export [--push]` writes, reusing `report.Build` and the existing readers:

- `policy/`: RTDB `config/*` (caps, defaults, mode, limits, kill switches with who/when/why) and the project policy object;
- `config/`: the bucket's shared config, project layer and recipes verbatim;
- `config/operator.yaml`: the operator's local config without identity and endpoints, secrets by **name** only. This is the most valuable item because it exists nowhere else today;
- `inventory/`: secrets metadata (Secret Manager list; launchers already hold it, `iam.tf:15-19`), enrolled repositories and project jobs;
- `spend/`: Firestore `spendDaily` (final days only, so history is never rewritten) and per-run CSVs from `result.json`.

Not exported: Terraform state (rebuildable, may hold sensitive values) and transcripts (large, may hold code). Reconstruction replays `init`, `budget set`, `config publish`, `recipes publish` and `secrets set` from the export.

**Cadence (question 4): both.** A manual `fugaro project export` ships first (it needs the operator's local file, which only the operator has, and the operator's git credentials). A scheduled entry on the existing history job (sweep every 15 minutes, rollover daily: `history.tf:174-260`; its service account already has RTDB admin) is a later step, needing a deploy key or App token stored as a project secret (UNVERIFIED that the existing App can push to the backing repository); the manual command or a hook in `init` and `budget set` captures local policy changes.

**Secrets (question 5): excluded, with an inventory.** No value is ever written, and no person holds secret-read today (`secrets.tf:31-42`), another reason not to build a reader. If encryption of secret material is ever wanted: SOPS with GCP KMS (operators decrypt with their own gcloud credentials, no key to distribute) plus an offline age recipient as break-glass, because the KMS key dies with the GCP project. Nothing in the repository uses KMS, SOPS or age yet; this is an opt-in for later, not part of the first slice.

## 9. The desktop console (spec 6, forward-looking)

No work in the implementation plan. `fugaro watch --json` is already a project-wide feed (`cli/watch.go:33`, `watch/json.go:97-115`) that an Electron app can spawn, with `budget kill|resume` and `report --json` for actions. Views still missing, for the console's own design: spend over time, live compute dollars, per-model and per-person breakdowns, a default-versus-override caps table, configuration and recipe views, a secrets and accounts inventory with owners, OAuth quota, logs and transcripts, backing-repository history. The project-centric data model above (policy object, enrollment records, export tree) is what those views would read.

## 10. Effect on work in flight

| Item | Effect | Recommendation |
|---|---|---|
| #343 project secrets | The foundation for §6.3; its repository-root grant wiring and ExtraSecrets union are partly superseded once repositories stop having roots. It still defaults to deny. | Merge as is (it works for the per-repository path used until phase 4); rulings D2. |
| #361 price catalog | Neutral or helpful; its `require_zdr` and `price_policy` become project-policy provider fields (§5); `anthropic/*` rows wait on the opt-in; needs the canonical-ID map (§6.2). | Continue. |
| #362 registry remote repository | Installation-level; no conflict. | Continue. |
| #359 OpenRouter senior reviewer | Its phase 2 is the mechanism for §6.2; its live-check layer path ties to §5. | Continue, extend with the routing switch. |
| #363 OAuth reviewer stage | Multiplies per-repository variant jobs; widens token blast radius under sharing. | **Hold** until phase 5 (D9). |
| config transitions (#344) | Its inventory is the churn this design removes; its retirement engine is the migration tool. | Implement only the core (addresses, retirements, the retire flow: plan Tasks 1 to 5) and re-scope the rest (D8). |
| mono-repo and many-repos designs | Superseded by this design. | Dropped; PRs #355 and #356 were already closed. |
| `checkout: clone` (#342, Task 18) | The prerequisite for phase 3. | Task 18 and its owner-run Check 38 stay on the critical path; the flip PR follows Check 38. |

## 11. Phases

Each phase ships independently and leaves the system working.

0. **Small fixes (no design risk).** The two budget bugs (§2.4); the Anthropic key optional (§6.1).
1. **Policy as data.** `config/policy` in RTDB, the runner reads it, `fugaro project policy publish`, `fugaro project show`, the local file shrinks; the routing switch and the canonical-ID map (§6.2); policy leaves the job env (§5).
2. **Project-scope keys.** #343 merged, `secrets set` defaults to project scope, the D2 ruling implemented.
3. **Project jobs for clone workflows (GitHub).** Profiles, jobs per (profile, account), one job service account, broadened bucket IAM, SECURITY.md updated, `fugaro run` and `CheckRunExecution` on project jobs, `cloneShipped` flipped after Check 38.
4. **Enrollment.** The allow-list, first-use registration, `fugaro project sync` and `add-repo`, `init --repo` retired for clone repositories, migration of the owner's two installations through the retirement engine, the fate of baked workflows (D3).
5. **OAuth pool.** Person-owned secrets, references, `accounts share`, CLI failover; #363 rebased.
6. **Backing repository.** `fugaro project export`; the scheduled export later.

Each phase's live check is owner-run on installation `fugaro` and the sandbox only, never the belong project.

## 12. Open questions, resolved, and decisions for the owner

| Spec question | Resolution |
|---|---|
| 1. Repo-level overrides kept? | Only the per-repository **cap** (an RTDB node, no infrastructure) and kill switches. Everything needing per-repository infrastructure is dropped. |
| 2. How is a repo tied to a project? | Allow-list plus first-use registration, optional `project sync` discovery (§4.3). |
| 3. Migration | Re-run init under the new model with the retirement engine; two installations only; clone repositories first (§4.4). |
| 4. Export cadence | Both: manual first, scheduled later (§8). |
| 5. Encryption of secrets in the backing repo | Exclude secrets; inventory by name; SOPS plus KMS (with an age break-glass) only if ever wanted (§8). |

Decisions for the owner (each with the recommendation above):

- **D1.** `routing.claude: direct | openrouter | auto` at project level counts as the explicit opt-in of the mixed-credentials ruling (§6.2). Default `auto`.
- **D2.** Storing a key at project scope is itself the opt-in, including the reserved names, and reaches every enrolled repository (§6.3).
- **D3.** Baked workflows: dropped after phase 4 (recommended) or re-implemented as per-repository jobs created through the Cloud Run API (§4.4).
- **D4.** Accept the loss of per-repository bucket isolation between repositories of one project, stated in SECURITY.md (§4.1).
- **D5.** GitHub first; Bitbucket projects stay per-repository until a design for per-repository tokens in a shared job exists (§4.5).
- **D6.** The OAuth ownership model of §7 (a secret per person and account, owner as version-adder only, references in policy).
- **D7.** Whether the scheduled export is wanted at all (§8).
- **D8.** Config transitions is implemented only for its core (§10).
- **D9.** #363 is held until phase 5.

## 13. UNVERIFIED, to settle in the plan's first tasks

Whether a Cloud Run secret reference works inside an env override; Secret Manager IAM conditions on a name prefix; whether a repository module can reference a secret it does not create; the project-wide Bitbucket token scope; whether the GitHub App can push to the backing repository; Claude Code's usage-limit text; the runs bucket's IAM policy size limit at scale and Google's default service-account quota (about 100 per project: the current model exceeds it at about 60 repositories, the project-job model does not); how the Firestore database is created; whether the exploitability of launcher `runWithOverrides` on every job is real.
