# Repo-coupling audit: reconciliation

Checked against `origin/main` at `3de72b780` on 2026-11-22; F18 and F19 (task V1) against
`8316578f6` on 2026-11-23; F20 against `1d4b59344` on 2026-11-23; F21 (task R1) against
`d8f1ae007` on 2026-11-23, with the owner's runs of Checks 890–892 on `fugaro` that day. **Where this document disagrees with
[spec.md](spec.md), this document wins.** The inventory itself is [report.md](report.md); this
document records what the spec assumed, what the code says instead, and the owner's answers.

## 1. Assumptions

- A1. "All Fugaro repos" means this repository, `ee/` included. The console, mobile app, shared
  packages, plugin and website all live in it, and no other Fugaro source repository exists.
- A2. "Project" is a Fugaro project: one installation in one GCP project, with its own runs bucket,
  RTDB and Firestore (memory: project terminology). "Admin" is a person the grants node
  `config/admin/access` (`internal/access/store.go` `PathNode`) holds as admin. `fugaro update`
  derives `terraform.admins` from that node (`internal/cli/update_access.go` `syncAccess`; a run's
  own `--admin` is folded into the node by `foldFlagLists`), so the local list may be one admin's
  stale copy. The Firebase grant does not follow the grants node: `roles/firebasedatabase.admin`
  goes to the GCP project's `roles/owner` and `roles/editor` members (`init_firebase.go`, from
  `infra.ProjectAdmins`) plus the budget admins (`modules/firebase/iam.tf`,
  `admins = concat(var.admins, var.budget_admins)`). A project owner who is not in the grants node
  is an implicit admin (D1592, `internal/access/store.go`).
- A3. "Member" is anyone the project has placed who is not an admin: an operator (`RoleOperator`)
  or a viewer (`RoleViewer`) in `internal/access/access.go`.
- A4. "Repo level" means three places: a repository's own `fugaro.yaml` and `.fugaro/` files, the
  `repos.<owner/name>` map in an owner's local config (`internal/localcfg` `Config.Repos`), and every
  cloud node or object whose key carries the repository's slug.
- A5. A setting keyed by repository but written only by an admin into a project-owned store, such
  as `config/policy/repos/<slug>` or the project layer's `rigor.repos`, is a project setting about a
  repository. It is classified **Identity**, not a repo-level override. Two exceptions: the RTDB
  repo cap and kill nodes (report RC40, RC41) are admin-written, but each overrides a project
  control for one repository (a cap above the default, a stop for one repository). They are listed
  as benign candidates so the owner rules on them.
- A6. The report is a static audit plus the owner-run live checks D3231 names. No agent touches a
  live project.

## 2. What the spec names and what the repository has

| Spec concept | What the repository has |
| --- | --- |
| `fugaro init repo` with Terraform and confirmations | `fugaro init` is a tombstone (`internal/cli/retired.go` `newRetiredInitCmd`, exits 1). Onboarding is `fugaro setup\|update --repo [PATH]` (`initRun.repoEngine`) and `fugaro project repos add` (`runProjectReposEnroll`) |
| Shared jobs lifted; "confirm no per-repo job resources remain" | Per-repo jobs remain for every repository not admitted by a shared profile: `deploy/terraform/gcp/roots/repo`, `modules/repo`, `modules/workflow`. A shared repository skips the root (`init.go`, `if spec.Shared { … enrolSharedRepo }`). Retiring the roots is shared-jobs-default **M2**, not merged, waiting on owner Check 580 |
| Shared images lifted; update rebuilds only images whose Dockerfile is in the current repo | The build already clones the entry's source repository inside Cloud Build (catalog-build-context, `internal/backend/gcp/catalog.go`). The tie is the build identity: a catalog build runs as the build service account of the checkout it is submitted from, so `imagesStage.staleCatalog` builds `here` entries and prints the `elsewhere` ones |
| Repo-level images "disabled but still in the codebase" | `repoImagesOff = true` (`internal/cli/image.go`, D1900) closes the commands, but `fugaro update` still runs the machinery in process (`refreshBuilds`) for a repository whose project names no catalog image (the comment cites O4 b) |
| Repo-level API key vs OAuth marking, superseded by routing | `agent.auth`, `agent.oauth_token` and `agent.crafts.*.access` are marked Retired in `internal/config/scope.go` and are still fully honoured. `config.RetiredKeyNotes` has no caller outside `internal/config` and one docs test. Migrating them is model-credentials-routing **MG1–MG6** (approved 2026-11-21, MG1/MG3 in draft) |
| Repo-level spending caps | Two layers: `fugaro.yaml` `budget:` (tighten-only) and the RTDB node `config/caps/repos/<slug>` (`budget.PathCapsRepo`), which can sit above the default cap (`Caps.RepoPerRun`/`RepoDaily`) and is capped only by the global cap |
| Repo-level recipe layer | `.fugaro/recipes/<name>.yaml` (`internal/recipe` `RepoDir`, `internal/runner/recipe.go` `readRepoRecipe`) wins over the project recipe and the catalog |
| Required-version stamp in Firebase | No minimum-version field exists. The nearest is the policy node's schema version (`projectpolicy.Version`, refused through `VersionError`/`CLISkew`). The CLI has one startup hook, the root `PersistentPreRun` (`internal/cli/root.go`), whose comment warns that a subcommand's own hook would replace it |
| Firestore data model "keyed by repo" | Firestore holds `spendDaily/<day>_<slug>` and `runIndex/<run_id>` (with a `slug` field). No index is checked in, and `google_firestore_*` resources are banned by a Terraform test |
| Coordinator and MCP server | No `internal/mcp`. The MCP server is `ee/coordinator/mcpauth`; coordinator facts are at `coordinator/repos/<slug>` (`internal/coordpath`) |

## 3. Findings

- **F1 (Wrong).** `fugaro init repo` no longer exists (`retired.go`). The spec's known item reads
  as "per-repo onboarding with Terraform and confirmations", which is `repoEngine` (typed
  authorization `confirmRepo` or `--onboard-repo`, a per-repo root plan and apply, `offerBuilds`)
  and `project repos add` (one `confirmTyped`, then stages Policy, Installation and Repository).
  Report rows RC7 and RC8.
- **F2 (Wrong).** Per-repo jobs remain (F-table above). Shared-jobs-default M2 owns their
  retirement; the report cites it rather than re-planning it. Report rows RC60, RC61, RC64, RC67 and RC76–RC78.
- **F3 (Wrong, premise of the images question).** The build context is already fetched from git
  inside Cloud Build. What ties a catalog build to a checkout is the per-repo build service account
  (`modules/repo` `google_service_account.build`) and the per-repo assignment of catalog entries
  (`infra.CatalogRepos`, `modules/installation/catalogcheck.tf` `for_each` by slug). The spec's
  option 2 is half built. D3232.
- **F4 (Wrong).** Repo-level images are not dead in `fugaro update`: `refreshBuilds` still builds a
  repository's derived image when its project has no catalog. The image commands are closed.
  Report RC32 and RC73.
- **F5 (Gap).** The retired auth keys are honoured and drive infrastructure: secret mounts, the
  job variant, the Vertex API, `creds/repoauth/<slug>` and the credentials-service switch
  (`internal/infra/spec.go`, `internal/cli/update_repoauth.go` `writeRepoAuth`,
  `internal/credsvc/cascade.go` `narrowToRepoAuth`). The "`fugaro update` migrates this key" text
  in `scope.go` describes code that does not exist yet. The routing plan's MG tasks own the
  migration; the report names them as the lift.
- **F6 (Gap).** `fugaro update` from outside a checkout runs every project-wide stage and skips
  three: Repository (`init_repo_stage.go`), Checkout (`init_checkout_upgrade.go` `checkoutSteps`,
  the image refresh) and the pin-removal half of Plugin. Two more stages read the working
  directory: Images (`imagesStage.kinds` reads the checkout's workflows) and Policy
  (`checkoutWorkflows` in `init_policy_migrate.go`). So the result of one admin-run update depends
  on where it is run. This is the "blocks a single admin-run update" list in the report (§4).
- **F7 (Gap, hidden admin dependency).** `fugaro update` asks no role (`settings_access.go`
  comment), and `publishAfterWrite` (`write_publish.go`, mapping in `internal/apply/steps.go`) runs
  converge stages, Terraform and Cloud Build included, in-process after a write; `chainApply`
  (`internal/cli/chain.go`) does the same for a chain of changes. Two lists, re-derived from the
  call sites:
  - **Writers that run converge stages and call no role gate**: `models add|rm` and the
    `workspace` writers (`chainApply`), `models provider` (`models_provider.go`, providers and
    prices), `project binaries` (`project_binaries.go`) and the secrets-expiry writer
    (`secrets_expiry.go`), all through `runAfterWrite`.
  - **Gated writers that also run stages**: `settings set|unset` and `models use`
    (`settingsAccessWrite`); the `accounts` writers (`add`, `rm`, `switch`, `alias`, cascade, craft
    and pool; `accessGateProject`); `image add|rm` (`accessGateProject`); and `project repos add`
    (RC7).

  `rigor` (with `rigor floor` and `rigor security`), `workflow add|rm`, `project repos set`,
  `project routing set`, `project profile` and `project clone-url` write
  through `applySettingsEdit` (`settings_write.go`), call no role gate and run no stage: they print
  the next `fugaro update`. The comment in `settings_access.go` that lists `accounts` and `secrets`
  as ungated is stale. Cloud IAM is the only stop for the ungated stage-running writers. The
  role gate is lenient by design: no roles means not judged, an unplaced caller is an implicit
  admin, an unreadable database is not judged. Report RC87 and RC88.
- **F8 (Gap).** A repo can loosen the project:
  - **Rigor** resolves by precedence, not tightening (`internal/rigor/resolve.go` `Resolve`:
    project default, project areas, `ProjectRepos`, then the repo's `rigor.default` and areas).
  - **The `overlay` merge** (models, levels, review rounds, `max_budget_usd`, recipe, workflow
    keys) replaces the project value in either direction.
  - **The repo cap node** can exceed the default cap.

  Only `internal/policy` `Merge` keys are tighten-only. These are the benign-candidate rows flagged
  "can loosen".
- **F9 (Gap, stale local config).** Ten readers still take a checkout's working-tree or
  default-branch `fugaro.yaml`, and the working-tree ones see uncommitted edits:
  - `checkoutParse` (`cloud.go`)
  - `embedProjectLayer` (`run_layer.go`)
  - `deploymentConfig` (`init_credentials.go`): workflows, secrets and base branch come from the
    working tree; the repository stage refuses a tree that differs from the default branch first
  - `checkoutFugaroYAML` (`craft.go`)
  - `cascadeRefConfig` and `defaultBranchPolicyLayer` (`run.go`)
  - `launchLevelsConfig` (`recipes_skew.go`)
  - `settingsRepoFile` (`settings_set.go`)
  - `config.EffectiveBaseBranch`, which also reads `localcfg repos.<r>.base_branch`
  - the runner's default-branch and run-branch reads (`internal/runner/policy.go`, `rigor.go`,
    `followup.go`)

  Report §5 lists each one with what it can override, and the five readers round 3 of the review
  added (`security.go` `securityBaseFile`, `project_job.go` `checkoutFugaroYAMLAtRef`,
  `config_show.go` `configLayerAnchor`, `cloud.go` `refuseSharedCheckout`, and the images stage's
  `kinds` with the policy stage's `checkoutWorkflows`).
- **F10 (Gap, monorepos and shared repos).** A repository belongs to one project at a time: the
  runner refuses a repository whose default-branch `project:` is not the job's project
  (`internal/runner/project.go` `sameProject`). A follow-up run is checked against the pull
  request's base instead (`readBaseConfig`), and with `FUGARO_PROJECT` unset the check is skipped
  only for a local run: a cloud job without it fails (`checkProject`,
  "job environment lacks FUGARO_PROJECT", unless `r.d.Local`). Two projects never share a store (per-project bucket, RTDB marker
  `/fugaro/project`, bucket marker `fugaro/project.json`). Within one project, `image_map` keys
  drop the provider (`projectpolicy.ImageMap`), so GitHub `acme/app` and Bitbucket `acme/app`
  collide. A monorepo has one slug, so every repo-keyed setting applies to all its services.
- **F11 (Gap, mixed CLI versions).** Nothing stops an older CLI from writing a repository's
  `fugaro.yaml` or the repo cap node after an admin upgrades. The only skew refusal is the policy
  node's schema version. The version stamp (D3233) is the proposed fence.
- **F12 (Wrong, docs).** `README.md`'s Upgrading section says
  `brew upgrade … && fugaro upgrade`, "in each checkout", and says it pins the plugin in
  `.claude/settings.json`. No `upgrade` command exists, and the pin is retired: `pluginwire` only
  removes it. Getting started also tells the user to commit the pin and run
  `fugaro upgrade --local`. `docs/release.md` (the users' step 3) and `website/data/install.yaml`
  name the same missing command. Report RC93, RC96 and RC97.
- **F13 (Exists).** Several lifts are already owned by approved plans:
  - Credential type: model-credentials-routing MG1–MG6, plus MG3/X4 for `creds/repoauth`.
  - Per-repo job and root retirement: shared-jobs-default M2.
  - Bitbucket tokens beyond one repository: bitbucket-token-types.
  - Repo-level secrets accepted and ignored: shared-jobs M1, D2313 (`RepoSecretsOff`).

  The report cites the owner and adds nothing for these.
- **F14 (Rule).** The spec's "dead or vestigial: recommend removal" meets D1900 (2026-11-09),
  under which the per-repo image code is kept and repo-image-removal R2–R10 is parked. D3234.
- **F15 (Rule).** The spec's principle "old repo-level configuration is migrated once, then
  rejected" matches model-credentials-routing's spec ("auto-migrated once into the project routing
  table. After that, the old fields are rejected"). The report's migration column follows it.
- **F16 (Gap).** `builds/<slug>/project-layer.yaml` (`config.LayerCopyKey`) is a per-repository
  copy of a project-owned layer, read by the repo's image builds and daily check
  (`doctor_layer.go`, `image.go`). It goes when per-repo builds go.
- **F17 (Gap).** The console's Project-tab settings write passes no `--repo`, and with a read
  whose `target_layer` is `repo` it writes the `fugaro.yaml` in the child's working directory, the
  installation's recorded checkout, without checking it is one. `console/src/main/ipc.ts` records
  this as an open question.
- **F18 (Gap, settles U1).** On a shared job a repository's `workflows.*.resources` does not reach
  the container, and its `timeouts.*` reach the runner but cannot lengthen the job:
  - **Resources.** The shared job's CPU and memory are the profile's (`infra/projectjobs.go`
    `ProjectJobs`, `CPU: pr.CPU`, `Memory: pr.Memory`; `modules/project/job.tf`). The launch
    overrides only the environment and the timeout (`internal/backend/gcp/run.go` `Launch`). Only
    the per-repo job takes the repository's numbers (`infra/spec.go` `repoCtx.workflow`). The
    runner still reads the repository's numbers: it prices compute with them
    (`internal/runner/cost.go` `updateCost`) and judges the outputs memory gate by them
    (`internal/runner/gate.go` `outputsMemoryProblem`). So on a shared job the cost record and
    that gate use a size the container does not have.
  - **Timeouts.** The shared job's task timeout is the profile's `timeout_class` plus the slack
    (`ProjectJobs`, `TaskTimeoutS`). The runner reads `timeouts.*` from the repository's config,
    then holds `timeouts.total` to the class and cuts the stage, verify, render and reserve caps
    with it (`internal/runner/projectjob.go` `clampTotalToJob`, called from `runner.go`). A
    repository can shorten its run, never lengthen it. Only a task's `--total-timeout`
    (`internal/cli/run.go` `launchTimeoutOf`, not a `fugaro.yaml` key) becomes the execution's
    own timeout, and it skips the clamp.
- **F19 (Gap, settles U2).** `localcfg repos.<r>.github_app_id` is stored per repository
  (`localcfg.Repo.GitHubAppID`) but means one App per project wherever a shared job is involved.
  The per-repo path reads each repository's own ID: the job environment (`infra/spec.go`
  `resolve`, then `repoCtx.workflow` sets `EnvGitHubAppID`), the build's `_GITHUB_APP_ID`
  (`internal/backend/gcp/build.go` `BuildRequest`, from `infra.RepoSpec.GitHubAppID` through
  `internal/cli/image.go` `cloudBuildSpec`), and
  `repoGitHubAppID` (`internal/cli/catalog_provider.go`) for catalog builds and `followups`. The
  shared job, the coordinator and the Firebase stage read one ID for the whole config
  (`infra/projectjobs.go` `recordedGitHubAppID`), and refuse a config that records two. So two
  Apps work only while every GitHub repository keeps a per-repo job. The key is resolved per
  repository, from the repo copy or the project-scoped one (`catalogAppKeySecretID` →
  `resolveGitSecretID`). The home is one project-level App ID and key.
- **F20 (Gap).** The check scripts' reviews found eight coupling points the report missed. They
  are rows RC98–RC105, and the totals move from 97 rows to 105:
  - **RC98 (Infra).** The `update` stage `secrets` (`initsecrets.go` `secretsStage.resolve`)
    reads the working directory and takes the checkout's repo secrets into per-repo containers.
    From outside a checkout it skips, so F6's three skipped stages are four. It is §4 item 4
    and a §5 reader. Its lift is L5, not L6:
    what ties it to the cwd is the choice of target, as for RC75, while L6 is about records
    derived from project data and the values here come from a person's prompt.
  - **RC99 (Infra).** `creds/workflowsecrets/<slug>`, the node RC70 writes from the checkout.
    Lift L6, with RC70.
  - **RC100–RC102 (Identity).** `creds/leases`, `creds/review-signs` and `creds/issuance`, each
    keyed by slug and run, written by the credentials service. The slug names the run's repo.
  - **RC103 (Identity).** `creds/buildpairs/<identity>/<build>`: keyed by build identity, and
    each record names the repos a build may fetch. The names are the fence.
  - **RC104 (Dead).** `offerBudgetModeRemoval` reads the checkout's `fugaro.yaml` and offers to
    remove a refused `budget.mode: off`, under `--plan-only` too. It is a second §5 reader.
  - **RC105 (Dead).** `foldBacklog` folds the checkout's `docs/backlog/` once. It does nothing
    under `--plan-only`.

  RC104 and RC105 are steps after the stage loop, not stages, so Check 891, which compares
  stages, cannot see them. Both are §4 item 4 and lift L5.
- **F21 (Gap, from the live checks).** The owner's runs of Checks 890–892 on `fugaro`
  (2026-11-23) found two rows the report lacked and three facts that matter for the lift. The
  totals move from 105 rows to 107. Report §1, "Live result", has the detail.
  - **RC106 (Identity).** `creds/current/<slug>/<run>` (`internal/creds/state.go` `pathCurrent`),
    the credential a run is on. The service writes it with the lease (`SetPlacement`) and removes
    both when the run has ended (`internal/credsvc/server.go` `forgetRun`). Check 890 named it.
  - **RC107 (Infra).** The Batch compute root (`modules/compute/iam.tf`, `for_each =
    var.run_accounts`) grants each per-repo job account two project roles, from a snapshot
    (`infra.JobRunAccounts`) taken when `setup --provision-batch` last ran. `update` never runs
    that stage (`init_stages.go`). Check 892 shows the drift: a deleted job account still holds
    the pair, and three of six live job accounts lack it. It goes with the per-repo jobs (L2), but
    M2's brief does not name it.
  - **Shared jobs are unused on `fugaro`.** Both repos' newest launches went to their own job
    (RC76), so every blocker of report §4 item 1 is live there, not a corner case.
  - **Per-repo records outlive their repo.** `creds/repoauth`, `creds/workflowsecrets` and
    `config/policy/repos` hold slugs no run uses, among them a repo with no runs, no builds and no
    checkout, and two slugs for one owner/name. `task.Slug` hashes the provider with the name, so
    the second is RC51's collision case in real data. `project repos rm` (RC78) narrows
    `config/policy/repos` but leaves `creds/repoauth` and `creds/workflowsecrets` behind
    (`project_repos_rm.go`), and nothing removes a slug's records when its repo moves provider.
    The lifts that retire these nodes (L4 for RC48, L6 for RC99) have stale slugs to clear too.
  - **A per-repo credential the report has no row for.** The `secrets` stage's detail sends the
    owner to `fugaro secrets set <provider key> --repo <repo>` once per repository: a provider key
    is a bundle entry per repository (`internal/credsvc/provider_wire.go` `RepoCredentialKey`,
    D3008). It is a credential, so its lift is L4's (model-credentials-routing) to name; it is not
    a row here because no check listed the bundle. Open item O5.
  - **Clean-up (2026-11-23, at the owner's request).** The deleted account's two bindings
    (`roles/batch.agentReporter`, `roles/logging.logWriter` for
    `deleted:serviceAccount:fugaro-dimipaun-fugar-70110b74@…`) were removed from `fugaro-dev`'s IAM
    policy, and none remains: RC107's drift fixed by hand, while the compute root's snapshot still
    needs L2's fix. `config/policy/repos/dimipaun-fugaro-c2eb03dd3bfcd195`,
    `config/policy/repos/dimipaun-fugarosandbox-9b0fc60610974836` (the Bitbucket-provider slugs of
    two GitHub repositories: RC51's case) and
    `gs://fugaro-runs-fugaro-dev/builds/dimipaun-fugarosandbox-9b0fc60610974836/` were deleted
    after a backup. `creds/repoauth` and `creds/workflowsecrets` for
    `dimi-bmb-billmybanktests-41aa52e538568a87` were kept by the owner's ruling, for the Bitbucket
    workspace-token Checks 180 and 181.

## 4. Decisions

| # | Question | Recommendation | Answer (2026-11-22) |
| --- | --- | --- | --- |
| D3230 | Where is the inventory written? | In this PR as `report.md`, so the plan carries only live checks | **As recommended.** The static inventory is verified now; a re-derivation would cost about six runs |
| D3231 | How are the live Method steps (data export, runtime trace, IAM per point) done? | Owner-run read-only scripts, Checks 890–892 | **As recommended.** Agents never touch live cloud |
| D3232 | Images spread across repos | A project build identity: one project-level build service account that can read every catalog entry's git secret, so `update` builds every entry from anywhere. The git fetch already exists (F3) | **As recommended.** A documented procedure breaks "update once"; a state repo is a new concept that moves teams' Dockerfiles |
| D3233 | How far does the plan take the version stamp? | A lift item specced later; the report names the hook and the store | **As recommended.** The spec says audit only |
| D3234 | Dead per-repo image code: recommend removal? | Recommend unparking repo-image-removal R2–R10 after M2 | **Keep D1900. Not the recommendation.** The report classifies it Dead, records that D1900 keeps it, and the lift list recommends nothing for it |
| D3235 | When does the owner rule on the benign candidates? | After this PR merges, in a follow-up session | **As recommended.** The report holds the list with a recommendation per row |
| D3236 | Shape of the lift work | An ordered list; each item cites its owning plan or becomes its own spec when picked | **As recommended** |
| D3237 | The owner's ruling on each benign candidate B1–B19 (D3235; asked one by one on 2026-11-23, a recommendation each) | Per row, report §3 | **Answered 2026-11-23.** Keep, as recommended: B1, B2, B5, B6, B7, B8, B10, B12, B13, B14, B15, B17, B19. B3 rejected, as recommended: the routing table decides; `agent.model`, `models.*` and `crafts.*.model` are migrated once, then refused. B4 kept tighten-only, as recommended. B18 kept, capped at the project default unless the global cap allows more, as recommended. **Not the recommendation:** B9 `ab.rate` lifts to the project (keep was recommended); B11 `rigor.default` and `rigor.areas` lift to the project, with per-repo levels in the project's `rigor.repos` (keep tighten-only was recommended); B16 `.fugaro/recipes/` lifts to the project, the repo recipe files migrated once, then refused (keep tighten-only was recommended). Report §3 has the column; L8 applies them |
| D3238 | Which lifts go first (D3236)? | Report §7's order | **Answered 2026-11-23; amended 2026-11-23 (L5 added by the owner).** Wave 1: L1 (the version stamp), L5 (as D3239 rescopes it) and L8 (apply D3237; needs L1), each its own spec. Wave 2: L3 (the project build identity; still waits on L2), L9 (the default catalog entry; waits on L3), and L11, L12 and L13. L6 and L7 wait for the owner's next pick (L7 was offered for wave 1 and not chosen, L6 for wave 2 and not chosen). L2, L4 and L4b stay with the plans that own them; L10's first half is done (D1, #3693) |
| D3239 | L5's scope (the owner's own, 2026-11-23, amending L5 as report §7 first stated it) | — (raised by the owner) | **The working directory is an optional hint, never what decides the work.** Inside a checkout, `update` may take the project (so `--project` is not needed there) and a default focus or order from that checkout's `fugaro.yaml`. From any directory, the secrets, repository and checkout stages, and RC104's and RC105's steps, work over every enrolled repository, reading each from its default branch, not the working tree. A value only a person can supply for a repository (the secrets stage) is asked by the repository's name, or skipped with a line that names the repository, never skipped silently as today. Report §4 item 4 and §7's L5 row follow it |

Resolutions (one defensible answer, stated rather than asked):

- R1. Items an approved plan already owns (F13) are cited, not re-planned.
- R2. The version stamp's store is an RTDB node beside `config/policy/shared`, written by
  `fugaro update` with admin IAM and `.write: false` for clients. RTDB already holds every config
  node the CLI reads at start; Firestore holds only rollups. The spec for L1 (report §7) settles
  the details.
- R3. The report's "permissions needed today" column names the IAM set from the code:
  - `TF-admin` (an admin in the grants node `config/admin/access`, from which `update` derives
    `terraform.admins`)
  - `DB-admin` (`roles/firebasedatabase.admin`: the GCP project's owners and editors, and the
    budget admins; see A2)
  - `operator` (`terraform.operators`)
  - `repo-write` (push to the default branch)
  - `local` (the owner's machine only)

  Check 892 confirms them live.

## 5. Open tensions and open items

- **T1. A dead path that update still runs (D3234 against F4). Settled 2026-11-22.** D1900 keeps
  the per-repo image code, and that code is still live in `update` for a project with no catalog,
  so such a project keeps a per-checkout step. **Answer (as recommended): lift item L9.** Recommend
  giving a no-catalog project a default catalog entry, so `update` never builds a derived image.
  The code stays, as D1900 rules.
- O1. **Closed 2026-11-23.** The owner ruled on B1–B19 (D3237) and on the lift order (D3238, amended), and rescoped L5 (D3239).
- O2. **Closed 2026-11-23.** The owner ran Checks 890–892 on `fugaro`; report §1's "Live result"
  records them, and F21 what they found.
- O3. `docs/status.md` gets this spec's row when the plan merges, as for every spec folder before
  it; this pull request does not touch it.
- O4. `config.RepoCraftsNodePrefix` (`config/policy/repocrafts/<slug>`) is a constant with no
  writer on `main` yet. Model-credentials-routing CM2 adds one; the node becomes a row then.
- O5. The per-repository provider-key entry in the credentials bundle (`RepoCredentialKey`,
  D3008; F21) has no row. Whether it is a row here or a line in L4's plan is the owner's call.

## 6. UNVERIFIED

- U1. **Settled by V1 (F18).** Resources do not reach a shared job; timeouts reach the runner,
  which holds the total to the profile's `timeout_class`.
- U2. **Settled by V1 (F19).** Stored per repository; one App per project wherever a shared job
  is involved, several Apps only on per-repo jobs.
- U3. **Settled by Check 892 (2026-11-23).** `fugaro` has one principal in the grants node, an
  admin, and no member. The admin holds what the legend says TF-admin, DB-admin and operator mean.
  No member's IAM exists to read; a project with an operator needs the check run again.
- U4. **Restated after Checks 891 and 892 (2026-11-23).** On `fugaro` no member exists, so no
  member-run command can trigger Terraform there today. Check 891 ran as the admin and showed
  that only three stages change with the directory, each a blocker report §4 names. Whether a
  member's writer runs Terraform on a project that has members is still F7's answer from the
  code: the IAM allows it.
  It stays open until a project with an operator runs Check 891 as that operator.
