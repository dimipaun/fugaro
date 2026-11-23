# Repo-coupling inventory

A static audit of `origin/main` at `3de72b780`, 2026-11-22: five read-only code surveys, one per
group of surfaces. Each cited symbol was then re-checked by hand. Rows RC98–RC105, which the
check scripts' reviews found, were checked against `1d4b59344` on 2026-11-23 (reconciliation F20).
Rows RC106 and RC107, which the live checks found, were checked against `d8f1ae007` on 2026-11-23
(reconciliation F21). Locations name a path and a symbol, never a line, because lines drift. The live
columns (data layout on a real project, runtime trace, IAM a member really holds) come from the
owner's runs of Checks 890–892 on the project `fugaro` on 2026-11-23; §1's "Live result" says what
each check saw. Where [reconciliation.md](reconciliation.md) and this report disagree, the
reconciliation wins.

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
| 3–4 Data model, GCS | 7 | 2 | 16 | 1 | 26 |
| 5 Terraform | 6 | 0 | 1 | 2 | 9 |
| 6 Secrets | 4 | 0 | 0 | 1 | 5 |
| 7–8 Images, jobs | 7 | 0 | 0 | 0 | 7 |
| 10 Console, mobile, MCP | 0 | 1 | 7 | 0 | 8 |
| 11 Update and writers | 2 | 0 | 2 | 2 | 6 |
| 12 Skills and docs | 7 | 0 | 0 | 0 | 7 |
| **Total** | **39** | **21** | **35** | **12** | **107** |

Benign counts 21 rows: the 19 candidates B1–B19 of §3, plus two writers of repo-level config
(RC9, RC80) that follow whatever the owner keeps. RC40 and RC41 are admin-written yet Benign;
reconciliation A5 says why.

Scope item 9 (caps, rigor, recipes, routing) has no rows of its own: its rows sit under surfaces 2
and 3, where the setting lives.

### Live result (2026-11-23, project `fugaro`)

The owner ran Checks 890–892 on `fugaro` (GCP `fugaro-dev`). Each read only. They print keys,
stage lines, job names and IAM members, never a value, and so does this section.

**Check 890, the data layout: FAIL on one path, now a row.** Every repo-keyed path it listed is a
row of this report except `creds/current/<slug>/<run>`, which is RC106 now (#3700 named it in the
script). What was seen and what was not:

- **Seen live**: RC40 (one slug), RC41 (one), RC42 (`runs/`, `outcomes/<day>`,
  `spend/<day>/runs`), RC43, RC44 (two), RC45 (four), RC48 (`creds/repoauth` only), RC49, RC50
  (`spendDaily` only), RC52 (run records and `pr-index`), RC53 (`cache`, `locks`, `tokens`), RC54,
  RC55, RC56 (three slugs), RC99 (four), RC100 (one), RC106 (one).
- **Not seen (no data on `fugaro`)**: RC42 `agents/`, RC46, RC47, RC48 `creds/repoaccess`, RC50
  `runIndex` (Firestore holds only `meta` and `spendDaily`), RC53 `reports/`, RC101, RC102 and
  RC103 (`creds/` holds only `current`, `leases`, `repoauth`, `tokens` and `workflowsecrets`).
- **Retired paths still hold data.** RC54's derived image records (`builds/<slug>/go`, `web`), a
  Dead row, are there, and so are RC56's layer copies. A lift that retires them has data to
  remove.
- **The catalog's two homes both exist.** The copied definitions sit at the project-level
  `builds/catalog/<entry>/` (`gcp.CatalogBucketPrefix`); the records still sit under the building
  repo's slug (RC55). L3 moves only the records.
- **Stale per-repo data.** Only two slugs have runs (`runs/` in RTDB and GCS). `creds/repoauth`
  and `creds/workflowsecrets` hold four slugs each, among them `dimi-bmb-billmybanktests-…`, which
  has no runs, no builds and no checkout. `config/policy/repos` holds four slugs, among them two
  different slugs for `dimipaun/fugarosandbox` and one for `dimipaun/fugaro` that its runs do not
  use. `task.Slug` hashes the provider with the name, so two slugs for one name are two providers
  (or two spellings): the case RC51 says `image_map` cannot tell apart. `project repos rm` leaves
  the `creds/` records behind (reconciliation F21).
- **Volume.** One repo holds 4,715 run prefixes in GCS, 144 token prefixes and 23 locks. The other
  holds 78 runs.

**Check 891, where `update` reads the working directory: PASS.** It planned from an empty
directory and from three checkouts (`dimipaun/fugaro`, `edgeappinc/fugarosandbox`,
`dimipaun/fugarosandbox`). Sixteen of nineteen stages were the same everywhere. Three differed, and
each is a §4 blocker:

- `secrets` (RC98): skipped outside a checkout; inside, done, with a different detail per
  checkout. Two name a per-repo command for the model provider's key
  (`fugaro secrets set … --repo <repo>`, one entry per repository); the third says its repo-level
  secret is ignored (D2313).
- `repository` (RC8, RC13, RC17, RC39, RC48, RC60, RC64, RC70, RC71, RC77): skipped outside a
  checkout; inside, "onboards `<repo>`; plans when applied" in each. That is the stage's plain
  to-do line (`init_repo_stage.go` `Check`): `--plan-only` never plans the per-repo root, so the
  check cannot say whether a root has drift.
- `checkout` (RC73, RC74): skipped outside; "up to date: cloud" inside.
- `images` and `policy` (RC72, RC75) were the same everywhere on `fugaro`: nothing was stale, and
  the policy was already published. Their "varies" stays from the code. So does `plugin` (RC38):
  none of the three checkouts holds a pin.

Both repos' newest launches went to the repo's own job (RC76): the one named for `dimipaun/fugaro`'s `go` workflow and
`fugaro-edgeappinc-fugarosandbox-web-…`. None went to a shared `fugaro-p-…` job. **Shared jobs are
not in use on `fugaro`**, so every L2 blocker is live there today.

**Check 892, the IAM: PASS.** The grants node holds one grant: one human principal, an admin.
There is no operator and no viewer, so no member's IAM could be read.

- The admin maps as the legend says. It is a node admin and the local `terraform.admins` names
  exactly it (TF-admin). It holds `roles/firebasedatabase.admin` and `roles/owner` (DB-admin). It
  holds the launcher, build-submitter and `batch.jobsEditor` roles, and `iam.serviceAccountUser` on
  all ten per-repo build and job accounts (operator).
- Ten per-repo accounts exist: four build accounts and six job accounts. Three of the ten are for
  `dimi-bmb-billmybanktests`, the repo with no runs.
- The project policy still binds `roles/batch.agentReporter` and `roles/logging.logWriter` to a
  **deleted** per-repo job account (`deleted:serviceaccount:fugaro-dimipaun-fugar-70110b74…`). Three
  live job accounts hold the same pair and three do not. The pair comes from the Batch compute root,
  which only `setup --provision-batch` applies. That is RC107.
- The services' own grants match the service rows: `fugaro-credentials` holds the custom
  `fugaroCredentialsDatabase` role (the writer of RC100 and RC106).

**Cells confirmed.** Perms: TF-admin, DB-admin and operator as the legend defines them (892, for
the one admin; the operator-only case has no principal on `fugaro`). Update: `varies` for RC98,
and `blocks` for RC8, RC13, RC17, RC39, RC48, RC60, RC64, RC70, RC71, RC73, RC74 and RC77 (891).
Perms `operator` and Update `none` for RC76 (891's launches). No cell was corrected. Rows the checks cannot reach keep their static cells: the `run
token` and `repo-write` rows, `local`, and the two steps of §4 item 4 that are not stages (RC104,
RC105).

**Clean-up, 2026-11-23, at the owner's request.** After the checks, on `fugaro` (GCP `fugaro-dev`):

- The deleted job account's two bindings (`roles/batch.agentReporter` and
  `roles/logging.logWriter` for `deleted:serviceAccount:fugaro-dimipaun-fugar-70110b74@…`) were
  removed from the project's IAM policy, and none remains. That fixes RC107's drift by hand; the
  compute root's snapshot still needs L2's fix (RC107).
- `config/policy/repos/dimipaun-fugaro-c2eb03dd3bfcd195` and
  `config/policy/repos/dimipaun-fugarosandbox-9b0fc60610974836` (the Bitbucket-provider slugs of
  two GitHub repositories, since `task.Slug` hashes the provider: RC51's case) and
  `gs://fugaro-runs-fugaro-dev/builds/dimipaun-fugarosandbox-9b0fc60610974836/` were deleted after
  a backup.
- `creds/repoauth` and `creds/workflowsecrets` for `dimi-bmb-billmybanktests-41aa52e538568a87`
  were **kept** by the owner's ruling, for the Bitbucket workspace-token Checks 180 and 181.

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
| RC9 | `internal/cli/settings_set.go` `settingsRepoFile`; `rigor` `--layer repo` | writes a repo's `fugaro.yaml`, possibly through a remembered checkout | Benign (writer) | repo-write | none | keep for the kept Bn only | refuse the lifted keys (D3237, L8) | S | S |
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
| RC18 | `agent.model`, `agent.models.*`, `agent.crafts.*.model` | overlay; the repo replaces the project | Benign B3 | repo-write | none | project routing table (D3237: rejected) | migrate once, then refuse (L8) | S | M |
| RC19 | `agent.levels.*`, `review_rounds`, `max_budget_usd`, `first_line_*`, `adversarial_rounds`, `review_stop_fraction`, `workspace_beat`, `recipe` | overlay: **can loosen** | Benign B4 | repo-write | none | project; repo tighten-only if approved | none | M | M |
| RC20 | `model_grades.*` (repo entry) | dropped at merge (`policy.tightenGrades`) | Dead | repo-write | none | project only | reject the key | S | S |
| RC21 | `agent.max_output_tokens.*`, `max_run_tokens` | tighten-only | Benign B5 | repo-write | none | keep if approved | none | S | S |
| RC22 | `agent.compression` | every value refused | Dead | repo-write | none | none | already rejected | S | S |
| RC23 | `agent.triage`, `agent.review_against_task` | tighten through `policy.Merge` | Benign B6 | repo-write | none | keep if approved | none | S | S |
| RC24 | `budget.*` | tighten-only; `mode: off` refused, removal offered (`update_budget_off.go`) | Benign B7 | repo-write | none | keep if approved | live already | S | S |
| RC25 | `governance.*` | repo tightens | Benign B8 | repo-write | none | keep if approved | none | S | S |
| RC26 | `ab.rate` | may only be lowered to 0 (`validateRepoAB`) | Benign B9 | repo-write | none | project layer (D3237: lifted) | migrate once, then refuse (L8) | S | S |
| RC27 | `followup.trusted`, `followup.allow_public` | who may follow up a PR | Benign B10 | repo-write | none | keep if approved (security-relevant) | none | S | M |
| RC28 | `rigor.default`, `rigor.areas` | precedence: **can lower** the project level (`rigor.Resolve`) | Benign B11 | repo-write | none | project `rigor.repos` (D3237: lifted) | migrate once, then refuse (L8) | M | M |
| RC29 | `planning.decoupling` | the repo replaces the project | Benign B12 | repo-write | none | keep if approved | none | S | S |
| RC30 | `security.patterns` | union with the project | Benign B13 | repo-write | none | keep if approved | none | S | S |
| RC31 | `workflows.*.job_profile`, `needs`, `ab.reviewer_arm` | picks among admin-made profiles | Benign B14 | repo-write | none | keep if approved | none | S | M |
| RC32 | `workflows.*.dockerfile`, `image.*`, `rebuild.*`; `.fugaro/<wf>.Dockerfile` | the repo's derived image | Dead, but live in `update` for a no-catalog project (F4) | repo-write, TF-admin | blocks | kept by D1900; L9 | none | M | M |
| RC33 | `workflows.*.commands.*`, `cache`, `inputs.*`, `checkout` | how the repo builds and tests | Benign B15 | repo-write | none | keep if approved | none | S | S |
| RC34 | `workflows.*.resources`, `timeouts.*` | the per-repo job's CPU, memory and `TaskTimeoutS` (`infra/spec.go`). On a shared job (F18): resources do not reach the container, but the runner prices compute and gates outputs memory by them; `timeouts.*` reach the runner, clamped down to the profile's `timeout_class` (`clampTotalToJob`) | Infra | repo-write, TF-admin | blocks | job profile | Owned: M2 | M | M |
| RC35 | `workflows.*.secrets` | Secret Manager mounts; accepted and ignored (`RepoSecretsOff`, D2313) | Dead | repo-write | none | project secrets | Owned: shared-jobs M1 | S | S |
| RC36 | `.fugaro/recipes/<name>.yaml` (`readRepoRecipe`) | wins over the project recipe | Benign B16 | repo-write | none | project recipes (D3237: lifted) | migrate once, then refuse (L8) | S | M |
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
| RC99 | RTDB `creds/workflowsecrets/<slug>` (`internal/creds/workflowsecrets.go` `pathWorkflowSecrets`) | one record per repo of its workflows' secrets (variable and container), read by the credentials service; written by `update` from the checkout (RC70). Infra: what it holds is the repo's own job wiring, not a fact about the repo | Infra | TF-admin | blocks (with RC70) | project workflow secrets (L6) | follows RC70 | M | M |
| RC100 | RTDB `creds/leases/<slug>/<run>` (`internal/creds/state.go` `pathLeases`) | one probe lease per run, written by the credentials service. Identity: the slug names the run's repo, as in `runs/<slug>/<run>` (RC42) | Identity | service | none | keep | none | S | S |
| RC101 | RTDB `creds/review-signs/<slug>/<run>` (`internal/credsvc/verdict.go` `pathReviewSigns`) | one run's one-signature mark on its verdict, written by the service. Identity, as RC100 | Identity | service | none | keep | none | S | S |
| RC102 | RTDB `creds/issuance/<slug>/<run>` (`internal/identitywire/wire.go` `pathRecords`) | the record of the ticket the service issued one run. Identity, as RC100 | Identity | service | none | keep | none | S | S |
| RC103 | RTDB `creds/buildpairs/<identity>/<build>` (`internal/creds/buildpairs.go` `pathBuildPairs`) | keyed by build identity, not by slug; each record names the repos the build may fetch, by canonical name. Identity: the names are the fence, and the key follows whatever build identity L3 leaves | Identity | service | none | keep | none (records expire) | S | S |
| RC106 | RTDB `creds/current/<slug>/<run>` (`internal/creds/state.go` `pathCurrent`, `PathCurrent`) | the credential one run is on, written by the credentials service with the run's lease as one patch (`SetPlacement`) and removed with it when the run has ended (`internal/credsvc/server.go` `forgetRun`). Identity, as RC100. Found by Check 890 | Identity | service | none | keep | none | S | S |

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
| RC107 | `roots/compute`, `modules/compute/iam.tf` `run_agent_reporter`, `run_log_writer` (`for_each = var.run_accounts`, from `infra.JobRunAccounts`) | the Batch compute root grants every per-repo job account `roles/batch.agentReporter` and `roles/logging.logWriter` on the project. The list is a snapshot of the job accounts the repo roots had made when `setup --provision-batch` last ran; `update` never runs that stage (`init_stages.go`). Found by Check 892: three of fugaro's six job accounts hold the pair, three do not, and a deleted account's pair remains | Infra | TF-admin | none (a separate command, `setup --provision-batch`) | the shared jobs' accounts (L2) | re-derive with L2 | M | M |

No `google_cloudbuild_trigger` exists. The only per-repo Scheduler is the check job in RC63.

### Surface 6: secrets and credentials

| ID | Location | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC68 | `gcp.SecretID(slug, logical)`; `modules/repo` `google_secret_manager_secret.this` | per-repo secret containers (ignored since D2313) | Dead | TF-admin | none | project secrets | Owned: shared-jobs M1, M2 | S | S |
| RC69 | `config.PerRepositorySecret` (Bitbucket only) | Bitbucket token per repo | Infra | TF-admin | blocks | project `bitbucket_tokens` | Owned: bitbucket-token-types | M | M |
| RC70 | `internal/cli/update_workflowsecrets.go` `writeWorkflowSecrets` | per-repo records written by `update` from the checkout | Infra | TF-admin | blocks | project workflow secrets (L6) | write from the published layer | M | M |
| RC98 | `internal/cli/initsecrets.go` `secretsStage.resolve` (the `update` stage `secrets`) | reads the working directory (`gitRead` ".", `readOrigin`, `defaultBranchConfig`), then creates and takes the checkout's repo secrets in containers named by slug (`gcp.SecretID`), or the project copy where one serves (`projectScope`). From outside a checkout it skips. Infra: it makes per-repo containers | Infra | TF-admin | varies | the target from the enrolled repos, not the cwd (L5) | none | M | M |
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

Rows RC8, RC13, RC17, RC38, RC48, RC70–RC75 and RC98 are update steps too; they are filed where
the setting lives.

| ID | Location | Tied to the repo | Class | Perms | Update | Home | Migration | Eff | Risk |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RC87 | `internal/cli/settings_access.go` (comment); `internal/access` gate | `update` asks no role; the gate is lenient by design | Infra (hidden admin dependency) | IAM only | none | admin-only stages refuse a non-admin by name (L7) | none | M | L |
| RC88 | `internal/cli/write_publish.go` `publishAfterWrite`; `internal/cli/chain.go` `chainApply`; `internal/apply/steps.go` | writers with no role gate (`models add\|rm`, `workspace`, `models provider`, `project binaries`, secrets expiry) trigger Terraform and Cloud Build stages after the write (F7) | Infra (hidden admin dependency) | IAM only | none | the after-write hook runs infra stages for admins only (L7) | none | M | L |
| RC89 | `internal/cli/init_stages.go` `initStages` | update's stage list | Identity | TF-admin | none | the order L2–L6 empties its checkout stages | none | S | S |
| RC104 | `internal/cli/update_budget_off.go` `offerBudgetModeRemoval` (called from `init_stages.go`) | finds the checkout with `gitRead` "." and reads its working-tree `fugaro.yaml`; offers the removal of a `budget.mode: off` line as a diff. Runs under `--plan-only` too, and prints the diff. Not a stage, so Check 891 cannot see it. Dead: it migrates a value that is refused | Dead | repo-write | varies | a refusal or doctor notice that names the line (L5) | none | S | S |
| RC105 | `internal/cli/update_backlog.go` `foldBacklog` (via `pluginwire.Locate` ".", then `docs/backlog/`) | folds the checkout's backlog folder into the project's backlog node, once. Returns early under `--plan-only`; on a real update it differs per working directory. Not a stage, so Check 891 cannot see it. Dead: it migrates a folder format D2716 superseded | Dead | operator | varies | fold from each enrolled repo's default branch, or retire the step (L5) | none | S | S |
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

## 3. Benign override candidates, and the owner's rulings (D3235, D3237)

The owner ruled on each candidate on 2026-11-23, one by one, with the auditor's recommendation
beside it (reconciliation D3237). Three rulings are not the recommendation: B9, B11 and B16 lift to
the project. "Can loosen" means the repo can make the project less strict today. "Lift to project"
means the setting moves to the project layer: a repo's value is migrated once, then refused (the
spec's principle, F15). Lift L8 applies the rulings.

| # | Setting (row) | Why it could differ per repo | Today | Recommendation | Ruling (D3237) |
| --- | --- | --- | --- | --- | --- |
| B1 | `git.base_branch` (RC15) | repos branch differently | repo overrides | keep | keep |
| B2 | `git.author`, `pr.reviewers`, `pr.labels`, `early_draft`, `checkpoints` (RC16) | PR conventions per repo | overlay | keep | keep |
| B3 | `agent.model`, `models.*`, `crafts.*.model` (RC18) | a repo needs a stronger model | **can loosen** cost | reject; the routing table decides | **reject**, as recommended: the routing table decides; migrated once, then refused |
| B4 | levels, rounds, `max_budget_usd`, recipe name (RC19) | review depth per repo | **can loosen** | keep tighten-only | keep, tighten-only |
| B5 | `max_output_tokens`, `max_run_tokens` (RC21) | — | tighten-only | keep | keep |
| B6 | `triage`, `review_against_task` (RC23) | — | tighten-only | keep | keep |
| B7 | `budget.*` (RC24) | a cheaper repo | tighten-only | keep | keep |
| B8 | `governance.*` (RC25) | — | tighten-only | keep | keep |
| B9 | `ab.rate` (RC26) | opt a repo out of A/B | to 0 only | keep | **lift to project** (not the recommendation) |
| B10 | `followup.trusted`, `allow_public` (RC27) | who may follow up | repo-only | keep; security-relevant | keep |
| B11 | `rigor.default`, `rigor.areas` (RC28) | critical paths differ | **can lower** the project level | keep tighten-only; project `rigor.repos` for the rest | **lift to project** (not the recommendation): per-repo levels live in the project's `rigor.repos` |
| B12 | `planning.decoupling` (RC29) | — | replaces | keep | keep |
| B13 | `security.patterns` (RC30) | repo-specific sensitive paths | union | keep | keep |
| B14 | `job_profile`, `needs`, `reviewer_arm` (RC31) | a repo needs docker | picks an admin-made profile | keep | keep |
| B15 | `commands`, `cache`, `inputs`, `checkout` (RC33) | how the repo builds | repo-only | keep | keep |
| B16 | `.fugaro/recipes/` (RC36) | a repo-specific procedure | wins over the project | keep tighten-only, or project-named only | **lift to project** (not the recommendation): repo recipe files migrated once, then refused |
| B17 | `agent.instructions`, `agent.review` files (RC37) | repo context for the agent | repo-only | keep | keep |
| B18 | RTDB repo caps (RC40) | a repo's spend ceiling | **can exceed** the default | keep, capped at the default unless the global allows | keep, capped at the project default unless the global cap allows more |
| B19 | RTDB repo kill switch (RC41) | stop one repo | repo-only | keep | keep |

## 4. What blocks a single admin-run `fugaro update`

The question for each item: run from anywhere by one admin, what does `update` still leave
undone, or do differently depending on the directory it runs in?

1. **The per-repo root of every repository not on a shared job** (RC8, RC60, RC64, RC34, RC77),
   with its per-slug bucket conditions (RC57) and the per-repo infra facts in the owner's local
   config (RC39). Lift L2, owned by shared-jobs M2, and L5 for RC39. The Batch compute root's
   grants to those jobs' accounts (RC107) are not `update`'s at all: only
   `setup --provision-batch` applies them.
2. **Catalog entries owned by another repository** (RC72, RC6, RC55, RC61, RC65). Lift L3.
3. **Credential records written from the checkout's auth** (RC17, RC48). Lift L4, owned by MCR.
4. **Stages that read the working directory**: base kinds and the default catalog entry (RC75),
   the `gcp_project` anchor edit (RC13), the plugin pin removal (RC38), `update --check` (RC12),
   and the secrets stage, which takes the checkout's repo secrets and skips outside one (RC98).
   Two steps that are not stages read it too: the `budget.mode` removal (RC104) and the backlog
   fold (RC105). Check 891 sees stages only, so it cannot see these two. Lift L5, rescoped by
   D3239: the working directory stays an optional hint (the project, and a default focus or
   order), never what decides which work `update` does.
5. **Workflow-secret records from the checkout** (RC70, RC99) and the GitHub App per repo (RC71,
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
| `internal/cli/initsecrets.go` `secretsStage.resolve` (`defaultBranchConfig`) | default branch | which secrets the stage takes, and for which repo (RC98) |
| `internal/cli/update_budget_off.go` `offerBudgetModeRemoval` (`prepareBudgetModeRemoval`) | working tree | nothing; it edits the file (RC104) |

A key the owner rejects or lifts under D3237 must be refused by every one of these readers. A reader that
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

**Waves (D3238, 2026-11-23, amended the same day).** Wave 1 is L1, L5 (as D3239 rescopes it) and
L8, each its own spec; L8 needs L1. Wave 2 is L3 (which still waits on L2), L9 (which waits on L3)
and the small ones L11, L12 and L13. L6 and L7 wait for the owner's next pick: L7 was offered for
wave 1 and not chosen, L6 for wave 2 and not chosen. L2, L4 and L4b stay with the plans that own them. L10's first half is done (D1,
#3693); its rest follows the lifts it describes.

| # | Lift | Rows | Owner | Waits on |
| --- | --- | --- | --- | --- |
| L1 | Required-version stamp: `update` writes the minimum CLI release beside `config/policy/shared`; the root pre-run refuses project commands below it, naming `brew upgrade` and `fugaro update` | §6 | new spec | — (first, because every "migrate once, then reject" below needs old CLIs fenced) |
| L2 | Retire the per-repo roots and jobs | RC7, RC8, RC34, RC60, RC64, RC67, RC76–RC78, RC57, RC107 | shared-jobs-default M2 (RC107 is not in M2's brief: it needs a line in M2's follow-up or its own task) | Check 580 |
| L3 | One project build identity; catalog records and leases keyed by entry | RC6, RC44, RC55, RC56, RC61, RC65, RC72, RC82 | new spec (D3232) | L2 |
| L4 | Credential type out of the repo | RC17, RC47, RC48 | model-credentials-routing MG1–MG6, X4 | its plan edges |
| L4b | Bitbucket tokens at project or workspace level, not per repository | RC69 | bitbucket-token-types | its plan |
| L5 | `update`'s work does not depend on the working directory (rescoped by D3239). The working directory is an optional hint only: inside a checkout, `update` may take the project (so `--project` is not needed there) and a default focus or order from that checkout's `fugaro.yaml`. It never decides what work is done: from any directory, the secrets, repository and checkout stages and RC104's and RC105's steps work over every enrolled repository, reading each from its default branch, not the working tree; kinds and the default entry likewise; the anchor edit in `repos add`; the pin removal as a doctor notice. A value only a person can supply for a repository (the secrets stage) is asked by the repository's name, or skipped with a line that names the repository, never skipped silently as today | RC12, RC13, RC38, RC39, RC75, RC98, RC104, RC105 | new spec | L2 |
| L6 | Workflow-secret records and the GitHub App from project data | RC70, RC71, RC99 | new spec | — (U2 settled, F19) |
| L7 | Admin-only infra stages: `update` and the after-write hook refuse a non-admin by name before any infra stage | RC87, RC88 | new spec | — |
| L8 | Apply the benign rulings (D3237): lift B3 (to the routing table), B9, B11 (to the project's `rigor.repos`) and B16 to the project, each migrated once, then refused in every §5 reader and by RC9's writer; make B4 tighten-only and cap B18 at the project default unless the global cap allows more. The other thirteen are kept as they are | RC9, RC18, RC19, RC26, RC28, RC36, RC40 | new spec | L1 (the rulings are in, D3237) |
| L9 | A no-catalog project gets a default catalog entry, so no derived image is built in `update` (T1) | RC32, RC73, RC74 | new spec (T1, answered 2026-11-22; D1900 stands) | L3, unless the default entry names no source repository |
| L10 | Docs and skills: fix the missing `fugaro upgrade` now (README, `docs/release.md`, the website's install page); rewrite per-checkout steps as each lift lands | RC90–RC94, RC96, RC97 | docs runs (D1 for RC93, RC96, RC97) | D1 none; the rest follow L2–L5 |
| L11 | `image_map` keyed by provider | RC51 | small spec | — |
| L12 | The console's repo-layer write names its target | RC80 | console task | — |
| L13 | Dead repo keys out of the schema: `model_grades` in a repo file and `agent.compression` refused at decode with the fix, and their merge-time plumbing removed. Repo-level image rows stay (D1900); the plugin pin is L5; the per-repo secret containers are M1/M2 | RC20, RC22 | small spec | L1 |
