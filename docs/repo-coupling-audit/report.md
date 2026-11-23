# Repo-coupling inventory

A static audit of `origin/main` at `3de72b780`, 2026-11-22: five read-only code surveys, one per
group of surfaces. Each cited symbol was then re-checked by hand. Locations name a path and a
symbol, never a line, because lines drift. The live columns (data layout on a real project,
runtime trace, IAM a member really holds) are **static** until owner Checks 890–892 run
([implementation-plan.md](implementation-plan.md)). Where [reconciliation.md](reconciliation.md)
and this report disagree, the reconciliation wins.

**Legend**

- **Class.** `Infra` (infra-bound), `Benign` (benign override candidate, Bn in §3), `Identity`
  (identity only), `Dead` (dead or vestigial).
- **Surface.** Each section heading names the surface; the rows below it share it.
- **Perms today.** `TF-admin` (an admin in the grants node `config/admin/access`, from which
  `update` derives `terraform.admins`: apply Terraform), `DB-admin` (`roles/firebasedatabase.admin`: the GCP project's owners and editors, and the budget
  admins),
  `operator` (start runs; also submits a repository's builds and acts as its job and build service
  accounts, by design: `modules/repo/build.tf` `operator_build`), `repo-write` (push to the default
  branch), `local` (the owner's machine), `none`.
- **Update.** `blocks`: an admin's single `fugaro update` cannot do it, so a per-checkout run is
  needed. `varies`: the result depends on the working directory. `none`: no effect.
- **Effort and risk.** `S`, `M`, `L`.
- **Home.** The proposed project-level home. "Owned: X" means an approved plan already lifts it.

## 1. Summary

| Surface | Infra | Benign | Identity | Dead | Total |
| --- | --- | --- | --- | --- | --- |
| 1 CLI commands | 3 | 1 | 7 | 1 | 12 |
| 2 Repo-local config | 3 | 17 | 2 | 5 | 27 |
| 3–4 Data model, GCS | 6 | 2 | 11 | 1 | 20 |
| 5 Terraform | 5 | 0 | 1 | 2 | 8 |
| 6 Secrets | 3 | 0 | 0 | 1 | 4 |
| 7–8 Images, jobs | 7 | 0 | 0 | 0 | 7 |
| 10 Console, mobile, MCP | 0 | 1 | 7 | 0 | 8 |
| 11 Update and writers | 2 | 0 | 2 | 0 | 4 |
| 12 Skills and docs | 7 | 0 | 0 | 0 | 7 |
| **Total** | **36** | **21** | **30** | **10** | **97** |

Benign counts 21 rows: the 19 candidates B1–B19 of §3, plus two writers of repo-level config
(RC9, RC80) that follow whatever the owner keeps. RC40 and RC41 are admin-written yet Benign;
reconciliation A5 says why.

Scope item 9 (caps, rigor, recipes, routing) has no rows of its own: its rows sit under surfaces 2
and 3, where the setting lives.

## 2. Inventory

### Surface 1: CLI commands and flags

| ID | Location | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC1 | `internal/cli/retired.go` `newRetiredInitCmd` | `fugaro init` tombstone | Dead | none | none | none (already retired) | none | S | S |
| RC2 | `internal/cli/cloud.go` `originRepo`, `checkoutRoot`; `repo_arg.go` `repoArg` | the cwd's origin is the default target of ~30 commands, among them `run` (job launch), `runs ls` and the recipe commands (`recipes.go` `localRoot` adds a checkout's recipes); each also takes `--repo` | Identity | none | none | keep | none | S | S |
| RC3 | `internal/cli/project.go` `resolveCheckout`, `selectedProjectErr` | the checkout's `fugaro.yaml` `project:` picks the project when no `--project` is given | Identity | none | none | keep; see F10 for monorepos | none | S | M |
| RC4 | `internal/cli/review.go` (calls `originRepo`) | `review` needs a checkout without `--repo` | Identity | operator | none | keep | none | S | S |
| RC5 | `internal/cli/orchestrator_import.go` | `orchestrator import` needs a checkout | Identity | operator | none | keep | none | S | S |
| RC6 | `internal/cli/image_build_catalog.go`, `image_refresh.go` | `image build <entry>` runs as the cwd checkout's build SA; `image refresh --repo` must match the checkout | Infra | TF-admin or operator | blocks | project build identity (D3232) | build SA per repo retired | M | M |
| RC7 | `internal/cli/project_enroll_converge.go` `runProjectReposEnroll` | `project repos add` needs a checkout unless `--allow-list-only`, then runs stages Policy, Installation, Repository | Infra | TF-admin (admin gate) | none (a separate command) | an allow-list write only, once M2 lands | none | M | M |
| RC8 | `internal/cli/init.go` `initRun.repoEngine` | `setup\|update --repo [PATH]`: typed authorization, per-repo root apply, first builds | Infra | TF-admin | blocks | Owned: shared-jobs M2 and M7 (a shared repo already skips it) | retire roots (M2) | L | M |
| RC9 | `internal/cli/settings_set.go` `settingsRepoFile`; `rigor` `--layer repo` | writes a repo's `fugaro.yaml`, possibly through a remembered checkout | Benign (writer) | repo-write | none | keep for approved Bn only | refuse keys not approved (D3235) | S | S |
| RC10 | `internal/cli/plan_status.go` | best-effort `originRepo` | Identity | none | none | keep | none | S | S |
| RC11 | `internal/cli/budget.go` repo scope | `budget set --repo` writes `config/caps/repos/<slug>` | Identity (writer of RC40) | DB-admin | none | see RC40 | see RC40 | S | S |
| RC12 | `internal/cli/update_check.go` `runUpdateCheck` | `update --check [PATH]` reads the checkout's files | Identity | none | varies | read project data only (L5) | none | S | S |

### Surface 2: repo-local config files

`fugaro.yaml` types: `internal/config` `Config`. Per-key scope: `internal/config/scope.go`
`Scopes`. Schema: `schemas/fugaro.schema.json`.

| ID | Field or file | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC13 | `version`, `project`, `gcp_project`, `profile` | anchor the repo to its project; `update` writes the `gcp_project` anchor per checkout (`repoEngine`) | Identity | repo-write | blocks (anchor edit) | keep `project`/`profile`; the anchor edit moves to `repos add` (L5) | none | S | S |
| RC14 | `git.provider` | part of the storage slug | Identity | repo-write | none | keep | none | S | S |
| RC15 | `git.base_branch` (+ `localcfg repos.<r>.base_branch`) | overrides the project's; read by builds in `infra/spec.go` | Benign B1 | repo-write / local | none | project default, repo override if approved | migrate localcfg value into the layer | S | S |
| RC16 | `git.author`, `git.pr.reviewers`, `git.pr.labels`, `early_draft`, `checkpoints` | PR shape | Benign B2 | repo-write | none | keep if approved | none | S | S |
| RC17 | `agent.auth`, `agent.oauth_token`, `agent.crafts.*.access` | credential type per repo; drives mounts, job variant, Vertex, `creds/repoauth` (F5) | Infra | repo-write, then TF-admin to deploy | blocks | Owned: model-credentials-routing MG1–MG6 | migrate once, then reject (MG) | L | L |
| RC18 | `agent.model`, `agent.models.*`, `agent.crafts.*.model` | overlay; the repo replaces the project | Benign B3 | repo-write | none | project routing table; repo pick if approved | none | S | M |
| RC19 | `agent.levels.*`, `review_rounds`, `max_budget_usd`, `first_line_*`, `adversarial_rounds`, `review_stop_fraction`, `workspace_beat`, `recipe` | overlay: **can loosen** | Benign B4 | repo-write | none | project; repo tighten-only if approved | none | M | M |
| RC20 | `model_grades.*` (repo entry) | dropped at merge (`policy.tightenGrades`) | Dead | repo-write | none | project only | reject the key | S | S |
| RC21 | `agent.max_output_tokens.*`, `max_run_tokens` | tighten-only | Benign B5 | repo-write | none | keep if approved | none | S | S |
| RC22 | `agent.compression` | every value refused | Dead | repo-write | none | none | already rejected | S | S |
| RC23 | `agent.triage`, `agent.review_against_task` | tighten through `policy.Merge` | Benign B6 | repo-write | none | keep if approved | none | S | S |
| RC24 | `budget.*` | tighten-only; `mode: off` refused, removal offered (`update_budget_off.go`) | Benign B7 | repo-write | none | keep if approved | live already | S | S |
| RC25 | `governance.*` | repo tightens | Benign B8 | repo-write | none | keep if approved | none | S | S |
| RC26 | `ab.rate` | may only be lowered to 0 (`validateRepoAB`) | Benign B9 | repo-write | none | keep if approved | none | S | S |
| RC27 | `followup.trusted`, `followup.allow_public` | who may follow up a PR | Benign B10 | repo-write | none | keep if approved (security-relevant) | none | S | M |
| RC28 | `rigor.default`, `rigor.areas` | precedence: **can lower** the project level (`rigor.Resolve`) | Benign B11 | repo-write | none | project `rigor.repos`; repo tighten-only if approved | none | M | M |
| RC29 | `planning.decoupling` | the repo replaces the project | Benign B12 | repo-write | none | keep if approved | none | S | S |
| RC30 | `security.patterns` | union with the project | Benign B13 | repo-write | none | keep if approved | none | S | S |
| RC31 | `workflows.*.job_profile`, `needs`, `ab.reviewer_arm` | picks among admin-made profiles | Benign B14 | repo-write | none | keep if approved | none | S | M |
| RC32 | `workflows.*.dockerfile`, `image.*`, `rebuild.*`; `.fugaro/<wf>.Dockerfile` | the repo's derived image | Dead, but live in `update` for a no-catalog project (F4) | repo-write, TF-admin | blocks | kept by D1900; L9 | none | M | M |
| RC33 | `workflows.*.commands.*`, `cache`, `inputs.*`, `checkout` | how the repo builds and tests | Benign B15 | repo-write | none | keep if approved | none | S | S |
| RC34 | `workflows.*.resources`, `timeouts.*` | the per-repo job's CPU, memory and `TaskTimeoutS` (`infra/spec.go`). On a shared job (F18): resources do not reach the container, but the runner prices compute and gates outputs memory by them; `timeouts.*` reach the runner, clamped down to the profile's `timeout_class` (`clampTotalToJob`) | Infra | repo-write, TF-admin | blocks | job profile | Owned: M2 | M | M |
| RC35 | `workflows.*.secrets` | Secret Manager mounts; accepted and ignored (`RepoSecretsOff`, D2313) | Dead | repo-write | none | project secrets | Owned: shared-jobs M1 | S | S |
| RC36 | `.fugaro/recipes/<name>.yaml` (`readRepoRecipe`) | wins over the project recipe | Benign B16 | repo-write | none | keep if approved | none | S | M |
| RC37 | `agent.instructions`, `agent.review` files | prompts per repo | Benign B17 | repo-write | none | keep if approved | none | S | S |
| RC38 | `.claude/settings.json` plugin pin (`pluginwire.PlanRemoval`) | retired pin, removed only from a checkout | Dead | repo-write | blocks (removal per checkout) | none | offered as a diff today; move to a doctor notice (L5) | S | S |
| RC39 | `localcfg` `Config.Repos`: `workflows`, `vertex`, `credentials_service_ready`, `provider_keys_served`, `stage_account_served`, `github_app_id` | per-repo infra facts on the owner's machine | Infra | local, TF-admin | blocks | project layer and installation state | Owned: M2 for `workflows`; the rest in L5 | M | M |

### Surfaces 3–4: Firebase and GCS

| ID | Path | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC40 | RTDB `config/caps/repos/<slug>` (`budget.PathCapsRepo`) | repo caps; **can exceed** the default cap | Benign B18 | DB-admin | none | project caps; repo override if approved | none | S | M |
| RC41 | RTDB `config/kill/repos/<slug>` | a kill switch per repo | Benign B19 | DB-admin | none | keep if approved | none | S | S |
| RC42 | RTDB `runs/<slug>/<run>`, `spend/<day>/runs/…`, `agents/…`, `outcomes/…` | run identity | Identity | run token | none | keep | none | S | S |
| RC43 | RTDB `spend/<day>/repos/<slug>` | spend per repo | Identity | run token | none | keep | none | S | S |
| RC44 | RTDB `buildleases/<slug>/<id>` | build leases per repo | Infra | TF-admin | none | per catalog entry (L3) | follows L3 | S | S |
| RC45 | RTDB `config/policy/repos/<slug>` | admission (project-owned) | Identity | DB-admin | none | keep | none | S | S |
| RC46 | RTDB `config/policy/voice/repos/<slug>` | voice policy (project-owned) | Identity | DB-admin | none | keep | none | S | S |
| RC47 | RTDB `config/policy/route/<slug>` (`creds/route.go`) | credential route per repo | Infra | DB-admin | none | Owned: model-credentials-routing | per MG | M | L |
| RC48 | RTDB `creds/repoauth/<slug>`, `creds/repoaccess/<slug>` (`writeRepoAuth`) | written by `update` from the checkout's auth | Infra | TF-admin | blocks | Owned: MCR MG3, X4 | bridge one release, then delete | M | L |
| RC49 | RTDB `coordinator/repos/<slug>` | coordinator facts | Identity | service | none | keep | none | S | S |
| RC50 | Firestore `spendDaily/<day>_<slug>`, `runIndex/<run_id>` (`slug` field) | rollups | Identity | service | none | keep | none | S | S |
| RC51 | Policy `image_map` (`projectpolicy.ImageMap`) | keyed by owner/name **without provider**: collides (F10) | Identity | DB-admin | none | key by canonical `provider/owner/name` (L11) | rekey once | S | M |
| RC52 | GCS `runs/<slug>/<run>/…`, `runs/<slug>/pr-index/…` | run records | Identity | run token | none | keep | none | S | S |
| RC53 | GCS `cache/<slug>/…`, `locks/<slug>/…`, `tokens/<slug>/…`, `reports/<slug>/…` | per-repo working data | Identity | run token | none | keep | none | S | S |
| RC54 | GCS `builds/<slug>/<wf>/image.json`, `check.json` | derived image records | Dead (D1900) | TF-admin | none | none | none | S | S |
| RC55 | GCS `builds/<slug>/catalog/<entry>/…` (`gcp.CatalogRecordKey`) | catalog records keyed by the building repo | Infra | TF-admin | blocks (with RC72) | `builds/catalog/<entry>/` (L3) | read both for one release | S | M |
| RC56 | GCS `builds/<slug>/project-layer.yaml` (`config.LayerCopyKey`) | a per-repo copy of the project layer (F16) | Infra | TF-admin | none | goes with per-repo builds | none | S | S |
| RC57 | IAM `gcp.BucketCondition`, `TokenBucketCondition` | repo jobs scoped to their slug's prefixes | Infra | TF-admin | blocks | `ProjectBucketCondition` (shared job) | Owned: M2 | S | M |
| RC58 | Policy `allow_repos`, `accounts.*.allow_repos`, `job_profiles.*.allow_repos`, `clone_urls`, `issue_sync.repos` | project lists naming repos | Identity | DB-admin | none | keep | none | S | S |
| RC59 | Project layer `rigor.repos` | project-set level per repo | Identity | TF-admin (layer publish) | none | keep | none | S | S |

### Surface 5: Terraform

| ID | Resource | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC60 | `deploy/terraform/gcp/roots/repo` (state at `infra.RepoStatePrefix(slug)`, workdir `infra.RepoWorkdir`) | one state per repo | Infra | TF-admin | blocks | Owned: shared-jobs M2 | `update` retires the roots | L | L |
| RC61 | `modules/repo` `google_service_account.build` and its grants | per-repo build identity | Infra | TF-admin | blocks | project build SA (D3232, L3) | retire with the root | M | M |
| RC62 | `modules/repo` `google_artifact_registry_repository.images` (`prevent_destroy`) | per-repo registry | Dead (D1900 keeps it) | TF-admin | none | shared registry | none (D3234) | M | M |
| RC63 | `modules/repo/check.tf` check job and Scheduler (paused) | per-repo daily check | Dead (D1900) | TF-admin | none | catalog check | none (D3234) | S | S |
| RC64 | `modules/workflow` job, job SA, launcher IAM, bucket and secret IAM | per-repo Cloud Run jobs | Infra | TF-admin | blocks | `modules/project` shared jobs | Owned: M2 | L | L |
| RC65 | `modules/installation/catalogcheck.tf`, `build_identity.tf` (`for_each` by slug) | catalog entries assigned to repos | Infra | TF-admin | blocks | one project build identity (L3) | re-key per entry | M | M |
| RC66 | `modules/installation/coordinator.tf` `var.coordinator.repos` | a list in the job env | Identity | TF-admin | none | keep | none | S | S |
| RC67 | `modules/project/iam.tf` repo grant ("until the per-repository jobs are dropped") | a transitional grant | Infra | TF-admin | none | remove after M2 | Owned: M2 follow-up | S | S |

No `google_cloudbuild_trigger` exists. The only per-repo Scheduler is the check job in RC63.

### Surface 6: secrets and credentials

| ID | Location | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC68 | `gcp.SecretID(slug, logical)`; `modules/repo` `google_secret_manager_secret.this` | per-repo secret containers (ignored since D2313) | Dead | TF-admin | none | project secrets | Owned: shared-jobs M1, M2 | S | S |
| RC69 | `config.PerRepositorySecret` (Bitbucket only) | Bitbucket token per repo | Infra | TF-admin | blocks | project `bitbucket_tokens` | Owned: bitbucket-token-types | M | M |
| RC70 | `internal/cli/update_workflowsecrets.go` `writeWorkflowSecrets` | per-repo records written by `update` from the checkout | Infra | TF-admin | blocks | project workflow secrets (L6) | write from the published layer | M | M |
| RC71 | `localcfg repos.<r>.github_app_id`, `github-app-key` container | stored per repo; the per-repo job and build read it per repo, while the shared job, coordinator and Firebase stage take one ID and refuse two (`recordedGitHubAppID`, F19) | Infra | TF-admin | blocks | the project's App | one project-level App ID and key (L6) | M | M |

### Surfaces 7–8: images, Cloud Build and jobs

| ID | Location | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC72 | `internal/cli/init_images_catalog.go` `imagesStage.staleCatalog` (`here` and `elsewhere` via `sameRepo`) | builds only the entries the cwd checkout owns | Infra | TF-admin or operator | blocks | project build identity (D3232, L3) | none | M | M |
| RC73 | `internal/cli/image_refresh_steps.go` `refreshBuilds` (run by `update`) | derived image for a no-catalog project | Infra | TF-admin or operator | blocks | L9 | none | M | M |
| RC74 | `internal/cli/init_checkout_upgrade.go` `checkoutSteps` (`cloudStep`) | the checkout's job images refresh | Infra | TF-admin | blocks | goes with RC73 | none | S | S |
| RC75 | `internal/cli/init_images.go` `imagesStage.kinds`; `init_policy_migrate.go` `checkoutWorkflows` | base kinds and the default entry read the cwd's workflows | Infra | TF-admin | varies | read every enrolled repo's default branch, or the published layer (L5) | none | M | M |
| RC76 | `internal/backend/gcp/run.go` `jobFor`, `TaskTimeout` | falls back to the per-repo `JobPath(slug, wf)` | Infra | operator | none | shared job only | Owned: M2 | S | M |
| RC77 | `internal/infra/projectjobs.go`, `infra/tfvars.go` `CredentialsJobAccounts` | per-repo job accounts from `localcfg repos.<r>.workflows` | Infra | TF-admin | blocks | profiles | Owned: M2 | M | M |
| RC78 | `internal/cli/init_retire.go`; `project_repos_rm.go` (`infra.Orphans`) | label-checked deletes of a repo's jobs; the root state is kept | Infra | TF-admin | none | `update` retirement | Owned: M2 | M | M |

### Surface 10: console, mobile, coordinator, MCP and watch

| ID | Location | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC79 | `console/src/main/spawn.ts` `SPAWN_COMMANDS` `--repo` rows; `launchrules.ts` | filter and launch target | Identity | operator | none | keep | none | S | S |
| RC80 | `console/src/renderer/screens/projects.ts` `buildProjectsSettings` → `settings.edit` | a repo-layer write lands in the child's cwd without `--repo` (F17) | Benign (writer) | repo-write | none | pass `--repo`, or refuse outside a checkout (L12) | none | S | M |
| RC81 | `console/src/renderer/screens/nocheckout.ts` | the Graph needs a checkout | Identity | none | none | keep | none | S | S |
| RC82 | `console/src/renderer/manage/kinds/repos.ts` | enrolment copy names a per-repo `image build` | Identity | none | none | copy follows L3 | none | S | S |
| RC83 | `mobile/src/feed/paths.js`, `reader.js` | reads caps, kill and spend per repo | Identity | none | none | follows RC40/RC41 | none | S | S |
| RC84 | `ee/coordinator/mcpauth` `tool_status.go`, `tool_list.go` | the repo list is the fence | Identity | service | none | keep | none | S | S |
| RC85 | `internal/cli/watch.go` `--repo`; `watch_queued.go` `scanRepo` | filter | Identity | none | none | keep | none | S | S |
| RC86 | `internal/orchestrator/state.go` `Escalation.Repo` | per-repo digest | Identity | none | none | keep | none | S | S |

### Surface 11: `fugaro update` and the writers

Rows RC8, RC13, RC17, RC38, RC48, RC70–RC75 are update steps too; they are filed where the
setting lives.

| ID | Location | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC87 | `internal/cli/settings_access.go` (comment); `internal/access` gate | `update` asks no role; the gate is lenient by design | Infra (hidden admin dependency) | IAM only | none | admin-only stages refuse a non-admin by name (L7) | none | M | L |
| RC88 | `internal/cli/write_publish.go` `publishAfterWrite`; `internal/cli/chain.go` `chainApply`; `internal/apply/steps.go` | writers with no role gate (`models add\|rm`, `workspace`, `models provider`, `project binaries`, secrets expiry) trigger Terraform and Cloud Build stages after the write (F7) | Infra (hidden admin dependency) | IAM only | none | the after-write hook runs infra stages for admins only (L7) | none | M | L |
| RC89 | `internal/cli/init_stages.go` `initStages` | update's stage list | Identity | TF-admin | none | the order L2–L6 empties its checkout stages | none | S | S |
| RC95 | `internal/access/scope.go` `ScopeRepos`; `internal/cli/access_gate.go` `accessGateRepo`; node `config/admin/access` | a role can be scoped to a list of repository patterns | Identity (project-owned grants scoped by repo) | DB-admin | none | keep | none | S | S |

### Surface 12: skills, docs and templates

| ID | Location | Instruction | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC90 | `plugin/skills/update/SKILL.md` ("Update a checkout", "in each checkout") | run update per checkout | Infra (docs) | — | blocks | one admin run (after L2–L6) | rewrite | S | S |
| RC91 | `plugin/skills/setup/SKILL.md` §9 | "run setup from the merged checkout" adds the repo's job | Infra (docs) | — | blocks | `repos add` | rewrite after M7 | S | S |
| RC92 | `plugin/skills/images/SKILL.md` §1, §7 | doctor from a checkout; `update --repo --plan-only` | Infra (docs) | — | varies | project-wide | rewrite after L3 | S | S |
| RC93 | `README.md` Getting started, Upgrading and Providers step 6 | `fugaro upgrade` (no such command), "in each checkout", commit the pin (retired), the stale `/fugaro:upgrade` skill name (F12); `fugaro update --repo` in each listed repository | Infra (docs, wrong) | — | blocks | `brew upgrade` then one `fugaro update` | fix now (L10) | S | M |
| RC94 | `docs/how-to/budgets.md`, `registry-cleanup.md`, `retiring-a-workflow.md`, `offboarding.md`, `teammates.md` | `fugaro update --repo` in each checkout | Infra (docs) | — | blocks | follows L2, L5 | rewrite | S | S |
| RC96 | `docs/release.md` (the users' step 3, and the plugin paragraph) | "For each repository, in its checkout … `fugaro upgrade`" (no such command) | Infra (docs, wrong) | — | blocks | one `fugaro update` | fix with D1 | S | S |
| RC97 | `website/data/install.yaml` | `fugaro upgrade --local` (no such command) | Infra (docs, wrong) | — | blocks | `fugaro update` | fix with D1 | S | S |

## 3. Benign override candidates, for the owner's review (D3235)

The owner rules after this PR merges. None is kept or rejected until then. The recommendation is
the auditor's. "Can loosen" means the repo can make the project less strict today.

| # | Setting (row) | Why it could differ per repo | Today | Recommendation |
| --- | --- | --- | --- | --- |
| B1 | `git.base_branch` (RC15) | repos branch differently | repo overrides | keep |
| B2 | `git.author`, `pr.reviewers`, `pr.labels`, `early_draft`, `checkpoints` (RC16) | PR conventions per repo | overlay | keep |
| B3 | `agent.model`, `models.*`, `crafts.*.model` (RC18) | a repo needs a stronger model | **can loosen** cost | reject; the routing table decides |
| B4 | levels, rounds, `max_budget_usd`, recipe name (RC19) | review depth per repo | **can loosen** | keep tighten-only |
| B5 | `max_output_tokens`, `max_run_tokens` (RC21) | — | tighten-only | keep |
| B6 | `triage`, `review_against_task` (RC23) | — | tighten-only | keep |
| B7 | `budget.*` (RC24) | a cheaper repo | tighten-only | keep |
| B8 | `governance.*` (RC25) | — | tighten-only | keep |
| B9 | `ab.rate` (RC26) | opt a repo out of A/B | to 0 only | keep |
| B10 | `followup.trusted`, `allow_public` (RC27) | who may follow up | repo-only | keep; security-relevant |
| B11 | `rigor.default`, `rigor.areas` (RC28) | critical paths differ | **can lower** the project level | keep tighten-only; project `rigor.repos` for the rest |
| B12 | `planning.decoupling` (RC29) | — | replaces | keep |
| B13 | `security.patterns` (RC30) | repo-specific sensitive paths | union | keep |
| B14 | `job_profile`, `needs`, `reviewer_arm` (RC31) | a repo needs docker | picks an admin-made profile | keep |
| B15 | `commands`, `cache`, `inputs`, `checkout` (RC33) | how the repo builds | repo-only | keep |
| B16 | `.fugaro/recipes/` (RC36) | a repo-specific procedure | wins over the project | keep tighten-only, or project-named only |
| B17 | `agent.instructions`, `agent.review` files (RC37) | repo context for the agent | repo-only | keep |
| B18 | RTDB repo caps (RC40) | a repo's spend ceiling | **can exceed** the default | keep, capped at the default unless the global allows |
| B19 | RTDB repo kill switch (RC41) | stop one repo | repo-only | keep |

## 4. What blocks a single admin-run `fugaro update`

The question for each item: run from anywhere by one admin, what does `update` still leave
undone, or do differently depending on the directory it runs in?

1. **The per-repo root of every repository not on a shared job** (RC8, RC60, RC64, RC34, RC77),
   with its per-slug bucket conditions (RC57) and the per-repo infra facts in the owner's local
   config (RC39). Lift L2, owned by shared-jobs M2, and L5 for RC39.
2. **Catalog entries owned by another repository** (RC72, RC6, RC55, RC61, RC65). Lift L3.
3. **Credential records written from the checkout's auth** (RC17, RC48). Lift L4, owned by MCR.
4. **Stages that read the working directory**: base kinds and the default catalog entry (RC75),
   the `gcp_project` anchor edit (RC13), the plugin pin removal (RC38), `update --check` (RC12).
   Lift L5.
5. **Workflow-secret records from the checkout** (RC70) and the GitHub App per repo (RC71,
   F19). Lift L6. The per-repo Bitbucket token (RC69) is lift L4b, owned by
   bitbucket-token-types.
6. **Derived images of a no-catalog project** (RC73, RC74, RC32). These are kept by D1900; L9
   offers the alternative.
7. **The docs that tell a person to run update per checkout** (RC90–RC94, RC96, RC97). Lift
   L10, after the lifts they describe.

## 5. Stale local config: every reader of a repo's file

| Reader | What it reads | What it can override |
| --- | --- | --- |
| `internal/cli/cloud.go` `checkoutParse` | working tree | the project layer cache, workflows |
| `internal/cli/run_layer.go` `embedProjectLayer` | working tree | the layer a run embeds |
| `internal/cli/init_credentials.go` `deploymentConfig` | working tree (auth from the default branch) | workflows, secrets, base branch deployed by Terraform |
| `internal/cli/craft.go` `checkoutFugaroYAML` | working tree | `craft ls` answers |
| `internal/cli/run.go` `cascadeRefConfig` | the ref, else the checkout | launch credentials |
| `internal/cli/run.go` `defaultBranchPolicyLayer` | default branch | the policy layer at launch |
| `internal/cli/recipes_skew.go` `launchLevelsConfig` | checkout | review levels at launch |
| `internal/cli/settings_set.go` `settingsRepoFile` | a remembered checkout | where a write lands |
| `config.EffectiveBaseBranch` | file, then `localcfg repos.<r>.base_branch` | the base branch |
| `internal/runner/policy.go`, `rigor.go`, `followup.go` | default and run branch | policy, rigor, followup trust |
| `internal/cli/security.go` `securityBaseFile` | the default branch (`readFugaroYAMLAtRef`) | security patterns |
| `internal/cli/project_job.go` `checkoutFugaroYAMLAtRef` | a ref of the checkout | what `accounts rm` keeps |
| `internal/cli/config_show.go` `configLayerAnchor` | checkout | which layer `config show` reads |
| `internal/cli/cloud.go` `refuseSharedCheckout` | checkout | refuses a shared checkout |
| `internal/cli/init_images.go` `imagesStage.kinds`; `init_policy_migrate.go` `checkoutWorkflows` | working tree | base kinds and the default catalog entry (RC75) |

A key the owner rejects under D3235 must be refused by every one of these readers. A reader that
silently ignores a rejected key re-creates the stale-config problem.

## 6. Corner cases

- **Mixed CLI versions.** An older CLI writes any repo key and the repo cap node unchecked. Only
  the policy schema version refuses (`VersionError`). The fence is lift L1.
- **Monorepos and shared repos.** One project per repo is enforced by `runner` `sameProject`,
  except a local run with `FUGARO_PROJECT` unset (a cloud job without it fails); a follow-up run is
  checked against its pull request's base instead. A monorepo shares one slug, so B-rows
  apply to all its services. `image_map` collides across providers (RC51).
- **Hidden admin dependencies.** RC87 and RC88. `fugaro run` itself triggers no Terraform or
  Cloud Build.
- **Required-version entry points (for L1).** The CLI's one startup hook is the root
  `PersistentPreRun` in `internal/cli/root.go`, which already resolves the selected project read-only
  (`updateCheckDisabled`, then `selectedProjectConfigReadOnly`). It is a `PersistentPreRun`,
  which cannot return an error, so a refusal needs the root's hook to become `PersistentPreRunE`
  or the check to sit in `cmd/fugaro/main.go` before `ExecuteContextC`; L1's spec picks one. Exempt
  from refusal: `update` and `setup` themselves, `version` and the `--version` flag, `help`,
  `doctor`, the retired tombstones, and the console's read-only paths (`consoleReadPaths` in
  `internal/cli/project.go`), which must not fail on a stamp they only display. A post-run hook is
  skipped when `RunE` fails, which is why `WarnUpdateAvailable` lives in `cmd/fugaro/main.go`. An
  RTDB read at every command start costs latency and fails offline. L1's spec settles a cache
  (the `internal/updatecheck` cache is the pattern) and what an unreadable stamp does.

## 7. Lift order

Ordered so the fence comes first and then each step removes one blocker of §4. Each item is its
own spec when the owner picks it (D3236); "owned" items need none.

| # | Lift | Rows | Owner | Waits on |
| --- | --- | --- | --- | --- |
| L1 | Required-version stamp: `update` writes the minimum CLI release beside `config/policy/shared`; the root pre-run refuses project commands below it, naming `brew upgrade` and `fugaro update` | §6 | new spec | — (first, because every "migrate once, then reject" below needs old CLIs fenced) |
| L2 | Retire the per-repo roots and jobs | RC7, RC8, RC34, RC60, RC64, RC67, RC76–RC78, RC57 | shared-jobs-default M2 | Check 580 |
| L3 | One project build identity; catalog records and leases keyed by entry | RC6, RC44, RC55, RC56, RC61, RC65, RC72, RC82 | new spec (D3232) | L2 |
| L4 | Credential type out of the repo | RC17, RC47, RC48 | model-credentials-routing MG1–MG6, X4 | its plan edges |
| L4b | Bitbucket tokens at project or workspace level, not per repository | RC69 | bitbucket-token-types | its plan |
| L5 | `update` reads no working directory: kinds and default entry from every enrolled repo's default branch; the anchor edit in `repos add`; the pin removal as a doctor notice | RC12, RC13, RC38, RC39, RC75 | new spec | L2 |
| L6 | Workflow-secret records and the GitHub App from project data | RC70, RC71 | new spec | — (U2 settled, F19) |
| L7 | Admin-only infra stages: `update` and the after-write hook refuse a non-admin by name before any infra stage | RC87, RC88 | new spec | — |
| L8 | Apply the benign rulings: migrate once, then reject the keys not kept, in every §5 reader | RC9, B1–B19 | new spec | D3235 rulings, L1 |
| L9 | A no-catalog project gets a default catalog entry, so no derived image is built in `update` (T1) | RC32, RC73, RC74 | new spec (T1, answered 2026-11-22; D1900 stands) | L3, unless the default entry names no source repository |
| L10 | Docs and skills: fix the missing `fugaro upgrade` now (README, `docs/release.md`, the website's install page); rewrite per-checkout steps as each lift lands | RC90–RC94, RC96, RC97 | docs runs (D1 for RC93, RC96, RC97) | D1 none; the rest follow L2–L5 |
| L11 | `image_map` keyed by provider | RC51 | small spec | — |
| L12 | The console's repo-layer write names its target | RC80 | console task | — |
| L13 | Dead repo keys out of the schema: `model_grades` in a repo file and `agent.compression` refused at decode with the fix, and their merge-time plumbing removed. Repo-level image rows stay (D1900); the plugin pin is L5; the per-repo secret containers are M1/M2 | RC20, RC22 | small spec | L1 |
