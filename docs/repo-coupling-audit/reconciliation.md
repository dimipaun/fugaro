# Repo-coupling audit: reconciliation

Checked against `origin/main` at `3de72b780` on 2026-11-22. **Where this document disagrees with
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
  stale copy. The Firebase grant follows the derived list
  (`deploy/terraform/gcp/modules/firebase/iam.tf`, `admins = concat(var.admins, var.budget_admins)`).
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
  retirement; the report cites it rather than re-planning it. Report rows RC60–RC67, RC76–RC78.
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
  - **Gated writers that also run stages**: `settings set|unset` (`settingsAccessWrite`), the
    `accounts` cascade, craft and pool writers and `models use` (`accessGateProject`), and
    `image add` (`accessGate`).

  `rigor`, `workflow add|rm`, `project repos set`, `project profile` and `project clone-url` write
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

  Report §5 lists each one with what it can override.
- **F10 (Gap, monorepos and shared repos).** A repository belongs to one project at a time: the
  runner refuses a repository whose default-branch `project:` is not the job's project
  (`internal/runner/project.go` `sameProject`). The check is skipped for follow-up runs, and with
  `FUGARO_PROJECT` unset only for a local run: a cloud job without it fails (`checkProject`,
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

Resolutions (one defensible answer, stated rather than asked):

- R1. Items an approved plan already owns (F13) are cited, not re-planned.
- R2. The version stamp's store is an RTDB node beside `config/policy/shared`, written by
  `fugaro update` with admin IAM and `.write: false` for clients. RTDB already holds every config
  node the CLI reads at start; Firestore holds only rollups. The spec for L1 (report §7) settles
  the details.
- R3. The report's "permissions needed today" column names the IAM set from the code:
  - `TF-admin` (an admin in the grants node `config/admin/access`, from which `update` derives
    `terraform.admins`)
  - `DB-admin` (`roles/firebasedatabase.admin`: admins and budget admins)
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
- O1. The benign-candidate rulings (D3235) are owed after merge. Until then no candidate is kept
  or rejected.
- O2. Checks 890–892's output fills the live columns. Until then those columns read "static".
- O3. `docs/status.md` gets this spec's row when the plan merges, as for every spec folder before
  it; this pull request does not touch it.

## 6. UNVERIFIED

- U1. Under shared jobs, whether `workflows.*.resources` and `timeouts.*` still reach the shared
  job or only the per-repo one. `infra/spec.go` writes `TaskTimeoutS` for the per-repo job; the
  shared job takes the profile's `timeout_class`. Check 891 or a reading of
  `internal/sharedjob` settles it.
- U2. Whether `localcfg repos.<r>.github_app_id` still mints per repository, or the project's App
  covers every repository. Settled from the code by task V1 (leads: `internal/cli/catalog_provider.go`
  `repoGitHubAppID`, `internal/backend/gcp/build.go`).
- U3. The real IAM a member holds on `belong`. Settled by Check 892.
- U4. Whether any member-run command triggers Terraform on `belong` today. F7 says the code allows
  it under IAM; Check 891's trace settles what actually happens.
