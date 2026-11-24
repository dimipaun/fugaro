# Image secrets through the credentials service: reconciliation

**Checked against:** `main` at `2772b8879` (2026-11-23). **Spec:** [spec.md](spec.md), the owner's
"Route Image Secrets Through the Credential Service", verbatim. **Where this document disagrees with
`spec.md`, this document wins.**

**Headline.** The spec was written blind to the repository and reads as if the rule were unbuilt. Most
of it is built. Images declare no secrets; **workflows** do (`workflows.<w>.secrets` in `fugaro.yaml`),
and the parent plan's slice S3 (flip B3.8, shipped in 0.27.0) already serves them through the
credentials service with no mount and no job accessor, fetched by the runner at bootstrap, fail
closed, audited per call; and the owner's own D2313 already switched repository-level secrets off, so
every run reads the project copy alone. The Cloud Batch (VM) backend mounts nothing (D3104) and there is no gen-1
Cloud Run job. What this plan does is close what is left: workflow secrets of vertex repositories, the catalog
build's git credential, the Terraform that a new secret name still needs, file exposure, and a guard
that keeps a workflow-secret mount from coming back. The owner turned the rest of the spec into
decisions D3320–D3333 below.

## 1. Assumptions

- **A1.** "Image" in the spec means the thing a job runs whose configuration names secrets. In this
  repository that is a **workflow** (which names an image through `image_name`/`needs`), not the image.
  Contradicted in part by F1, so the plan speaks of workflow secrets throughout.
- **A2.** "Job" means a job that runs a task or a build: per-repository workflow jobs, shared project
  jobs, Cloud Batch jobs, Cloud Build requests and the daily image-check job. Installation services
  (licence renewal, the resident orchestrator, voice, the workspace service, the coordinator, the
  intake) are not jobs in the spec's sense. Made a decision: D3321.
- **A3.** "The Fugaro Secrets Audit" is the parent plan's tested audit,
  [../secret-readers.md](../secret-readers.md) (task B0.1), and its guard (B0.2). No other audit
  document exists on `main`.
- **A4.** "Policy" in §4.4 is whatever the service reads to decide a fetch, wherever it is stored; it
  need not be the published project policy node.
- **A5.** The spec's rollback paragraph (§5) is advice, not a requirement for a feature flag; pre-1.0,
  a rollback is a release that restores the old code.

## 2. What the repository already has

| Spec concept | What `main` already has |
| --- | --- |
| Image declares secret names (§4.1) | A workflow declares them: `config.Workflow.Secrets` (`config.Secret{Name, Env}`), and a shared profile names them (`config.JobProfile.Secrets`). Images declare none (F1) |
| Job creation passes names, not mappings (§4.1) | Since B3.8, for a served repository: `modules/workflow/sa.tf` `local.vault_secret_names` keeps the names off `local.job_mounts` and off the job's accessor list; `modules/workflow/job.tf` mounts only `local.job_mounts` |
| Job identity (§4.2) | The verified job account (`credsvc.JobAccount`) plus the run's own token (C1.10), checked on every call; per-run identity tickets are launcher-does-not-mint's work (F4) |
| Bootstrap fetch (§4.3) | `internal/runner/workflowsecrets.go` `fetchWorkflowSecrets`: per declared variable the job does not carry, fail closed, value registered with the redactor before it is written, 90 s budget (`workflowSecretsBudget`); launch gate `vaultSince` (`internal/cli/recipes_skew.go`) |
| `POST /v1/secrets:resolve` (§4.3, §4.4) | `credsvc.WorkflowSecretsPath` = `/v1/workflow-secrets`, one variable per call (`credsvc.WorkflowSecretsRequest{Env}`), served from the record `creds/workflowsecrets/<slug>` (`internal/creds/workflowsecrets.go`) |
| Policy: image → allowed names (§4.4) | That record, written by `fugaro update` from the repository's `secrets:` and the operator-local `workflow_secrets` allow-list (`localcfg.Config.WorkflowSecrets`, D1260) |
| Audit log per resolve (§4.4) | `internal/credsvc/audit.go`: one line per act, naming one container, never a value (G18) |
| Job accounts invoke the service (§4.5) | `modules/workflow/sa.tf` `google_cloud_run_v2_service_iam_member.credentials`: `roles/run.invoker`, the job's own account, on that one service |
| Only the service reads Secret Manager (§4.5) | For served repositories' workflow secrets, yes; the full reader list is [../secret-readers.md](../secret-readers.md) E1–E33 |
| A grant that covers names not yet created (D3323) | `modules/installation/credentials.tf` `google_project_iam_member.credentials_prefix_accessor`: the service's `secretAccessor`, conditioned on the `fugaro_u_`/`fugaro_p_` grammar (`internal/secretname`, E25, Check 510) |
| Store a value without Terraform | `fugaro secrets shared set NAME` (`internal/cli/secrets_intake.go`) through the intake, which creates the container with its own account (E22, E26) |
| CI guard (§4.6 bullet 2) | `deploy/terraform/secret_readers_test.go` (B0.2): every Secret Manager reader in Terraform and Go is a listed row of `secret-readers.md`, and every row still has a hit |
| Periodic sweep (§4.6 bullet 3) | `fugaro doctor`'s Secret Manager sweep (`internal/cli/doctor_secret_readers.go`, B0.3) flags an unknown accessor; it does not yet list job mounts (F12) |
| Audit (§5 step 1) | [../secret-readers.md](../secret-readers.md) |

## 3. Findings

- **F1 (Wrong).** No image declares secrets. Base, derived and catalog images carry no secret list;
  `config.Workflow.Secrets` and `config.JobProfile.Secrets` are the declarations. The spec's suspected
  path, "image secrets become job secret mappings", is the workflow path: `internal/infra/spec.go`'s
  `SecretMounts` into `modules/workflow/job.tf`'s `secret_key_ref`. *Design consequence:* the plan
  closes workflow secrets; no image metadata is added.
- **F2 (Exists).** §4.1, §4.3 and most of §4.4 are the parent plan's S3 (B3.1–B3.8): for a served
  repository the job mounts no workflow secret, its account holds no accessor on one, and the runner
  fetches each at bootstrap through `/v1/workflow-secrets`. *Consequence:* no new bootstrap stage and
  no new route (D3329).
- **F3 (Wrong).** There is no gen-1 Cloud Run job: every job module sets
  `execution_environment = "EXECUTION_ENVIRONMENT_GEN2"`. The "VM backend" is Cloud Batch, and a
  Batch job already mounts nothing (`internal/backend/batch/secrets.go`, D3104; the launcher refuses a
  launch the service does not serve, `internal/cli/launch_batch.go`). *Consequence:* no backend task
  beyond Cloud Run and Cloud Build.
- **F4 (Exists).** The spec's per-task token (§4.2, open question 1) exists: a call is verified by the
  job account and the run's own token (C1.10), and launcher-does-not-mint makes the run's identity a
  one-time ticket the run claims. *Consequence:* resolution R1, no task.
- **F5 (Gap).** What still mounts in a run job on `main`, by class:
  (a) every workflow secret, the git credential and model credentials of a repository the switch holds
  off — `infra.CredentialsServiceServes` answers false for `agent.auth: vertex`, for a budget mode of
  `off` (always-a-budget removes that mode, D3009) and for a root not re-applied;
  (b) provider keys `FUGARO_PROVIDER_KEY_*` (R6, D1634), owned by keys-through-service;
  (c) the shared jobs' GitHub App key (C2.3, credentials-service, held on Check 96) and provider keys
  (KS16);
  (d) the catalog build identity's `availableSecrets` and its per-repository accessor
  (`modules/installation/build_identity.tf`
  `google_secret_manager_secret_iam_member.build_identity_git_secret`, E3, E6).
  *Consequence:* (a) is D3326, (d) is D3330, (b) and (c) stay with their plans (D3320, T1).
- **F6 (Rule).** The spec's "nothing else may read Secret Manager" contradicts recorded decisions that
  keep installation services reading with their own accessor: the coordinator's App key (E4, D499),
  the orchestrator's pairing keys (E29), licence renewal (E30), the voice hop (E31), the workspace
  service (E32), and every operator row (E8–E21, E23, E24, E27, E33; a project Owner can always read,
  E12). *Consequence:* D3321 scopes the rule to run jobs.
- **F7 (Wrong).** §5 step 3 says the credential-service value wins when both exist. The code says a
  mounted value always wins (`fetchWorkflowSecrets` fetches only a variable the job does not carry).
  Moot once nothing mounts; the plan keeps the code's rule during the migration.
- **F8 (Gap; amended after review).** Adding a workflow secret needs Terraform today. The copy a run
  reads is the project copy (F16), created by `modules/installation/secrets.tf`
  `google_secret_manager_secret.project` (with `prevent_destroy`, so it can only gain keys), named by
  `gcp.ProjectSecretID`, and admitted per repository by the operator-local `workflow_secrets`
  allow-list; the service's per-container accessor is `modules/workflow/sa.tf`
  `google_secret_manager_secret_iam_member.service` (D2074). `modules/repo/secrets.tf`'s repository
  containers still exist but are dormant under D2313. Changing a value needs nothing.
  *Consequence:* D3323, D3324, D3325; retiring a project copy needs a state removal before the
  container is deleted (U5).
- **F9 (Exists).** The grant D3323 asks for is on `main`: `credentials_prefix_accessor` covers the
  `fugaro_p_` prefix, and `config.SecretNameRE` (`^[a-z0-9][a-z0-9-]{0,62}$`) fits the grammar's name
  rule (`internal/secretname` `nameRE`). *Consequence:* no new IAM for the service. The header comment
  of `internal/credsvc/dynamicsecrets.go` still says the conditioned accessor is not on the tree; that
  is stale and the task that touches the file corrects it.
- **F10 (Gap).** `fugaro_p_<name>` is also the dynamic-delivery grammar for project-shared
  credentials (model keys and the like). A workflow secret named like one of those would share its
  container. `config.ReservedSecrets` names are already refused as workflow-secret names (D1260's
  note on `localcfg.Config.WorkflowSecrets`); since review round 2 the separation is the marker rule of F18
  and G2 rather than a name list, so no task tests name disjointness on its own.
- **F11 (Gap).** No secret is exposed as a file: `config.Secret` has `Name` and `Env` only, and the
  runner writes environment variables only. *Consequence:* D3327.
- **F12 (Gap).** Nothing reports a deployed job's mounts live. Doctor's sweep reads IAM, not job
  templates. *Consequence:* D3331.
- **F13 (Gap).** The build route `/v1/build/credentials` (`internal/credsvc/build.go`) serves "the
  build secrets of the workflow the call names out of the record", and `internal/infra/spec.go` builds
  `WorkflowSpec.BuildSecrets` from `w.Secrets`. A change to the record's shape (D3333) reaches the
  build route too. *Consequence:* the contract task covers both routes.
- **F14 (Rule).** "Fugaro asks for nothing" (CLAUDE.md, `docs/no-prompt-policy/`) and the owner-only
  fence on `fugaro secrets allow`/`deny` (D1552) hold for the new commands: storing and allow-listing
  a secret is the owner's own step, refused in an agent's session, with no prompt.
- **F15 (Rule).** "`fugaro update` is the whole upgrade" and "safe while runs are in flight"
  (CLAUDE.md; `docs/design/update-in-flight.md`): the migration of existing containers (D3325) runs
  inside `update` with a convergence test, and changes nothing a running execution holds.
- **F16 (Rule; found in review round 1).** D2313 (docs/shared-jobs-default) already disables
  repository-level secrets: `config.RepoSecretsOff = true` (`internal/config/secrets_disabled.go`),
  readiness mounts no repository-level name, `fugaro secrets set` answers
  `config.RepoSecretsDisabledNotice` for one, and the record names only the project copy
  (`internal/cli/update_workflowsecrets.go` `workflowSecretMountID`). The ruling keeps the dormant code
  so the feature can return. *Consequence:* D3324 and D3334 rested on a wrong fact and are amended
  below; this plan leaves D2313's dormant code alone.
- **F17 (Gap; review round 1).** An address is a model switch: `internal/runner/gateway.go`
  `serviceSwitched` is true for `api-key`/`oauth` whenever the job carries the service's address, so
  handing the address to a repository held off by budget mode `off` or a not-ready root would move its
  model calls. *Consequence:* D3326 amended to vertex only.
- **F18 (Gap; review rounds 1 and 2).** `fugaro_p_` holds admin-chosen credential names: the intake
  stores any grammar-conforming name, and `config.ReservedSecrets` is a closed list of four. A
  closed-list check cannot keep a workflow secret off a shared credential's container.
  *Consequence:* a workflow secret's container carries a marker label written when it is created
  (neither the intake nor the service holds `secretmanager.secrets.update`, so it can only be set at
  create); a workflow store refuses an existing container without it; `fugaro secrets shared set`
  and the cascade's per-secret read (`internal/credsvc/dynamicsecrets.go`) refuse one with it. The
  marker is **not a fence on its own**: `fugaroDeployer` holds `secretmanager.secrets.create`
  unconditionally (`modules/installation/deployer.tf`), so a managed apply can pre-create a marked
  container, the case `internal/intake/handlers.go` `checkShape` exists for. The fence is the
  owner-only record; the marker plus a shape check (no TTL, expiry, topic, CMEK key or rotation, the
  intake's own annotations only) keeps the two classes apart. The service reads the label through its
  `fugaroSecretLister` role (`secrets.list`, filtered by name); no new grant (G2).
- **F19 (Gap; review rounds 1 and 2).** The agent runs as the runner's own uid (no `SysProcAttr` in
  `internal/agent` or `internal/runner`), so a `0400` file does not keep an in-job review stage from
  reading it — no more than it keeps that stage from the environment variable a secret is exposed as
  today (the stages share the run's environment). *Consequence:* a file is treated exactly as an
  environment secret is: present for the coding run's stages, never written in an independent review
  run (whose agent today gets no secret), removed at run end (G3). The round-1 "remove before every
  review stage" step is dropped: it protected nothing the environment does not already expose.
- **F20 (Gap; review round 1).** Shared project jobs, the default for most runs, carry no workflow
  secret: `infra.projectJobSecrets` admits only reserved names and provider keys. *Consequence:* D3335.
- **F21 (Gap; review round 1).** S3's route binds a variable to one workflow, and its review-class
  refusal rests on that (`internal/runner/workflowsecrets.go` header). D3333 makes the repository the
  unit, so the refusal of every caller class that is not a per-repository coding job (review, shared,
  build identity, coordinator, a person) has to be restated and tested on version 2.
- **F22 (Gap; review round 1).** A repository's `fugaro.yaml` is repository-controlled (an agent can
  edit it). A declaration must never become a grant: the allow-list is the owner's alone, seeded only
  from the operator's `workflow_secrets` `allow_data_to`.
- **F23 (Gap; review round 2).** Each operator's `workflow_secrets` block is local and carries no
  timestamp, so seeding from it on every update would undo another operator's `deny`. *Consequence:*
  update seeds a name only when the record holds no entry for it; a `deny` leaves an explicit,
  possibly empty, entry; update never widens an existing entry (G8).
- **F24 (Gap; review round 2).** Version 2 assumes D2313: with `config.RepoSecretsOff` false, a
  repository's own copy would be shadowed by the project value. *Consequence:* the service and the
  runner refuse version 2 while the door is open, so reversing D2313 is a visible change.

## 4. Decisions

Numbers D3320–D3339 (README); D3322 was drafted, folded into D3326 before it was asked, and is not reused. Every answer is the owner's, given 2026-11-23 in the session that wrote
this document.

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D3320 | What does this spec become, given F2? | A closure plan: the leftover classes no other plan owns; KS16 and C2.3 by reference | **As recommended** (2026-11-23): no duplicate machinery |
| D3321 | Scope of "nothing else reads Secret Manager" | Run jobs only (per-repository, shared, Batch, build, image check); installation services stay listed exceptions under B0.2 | **As recommended** (2026-11-23) |
| D3323 | Adding, removing or changing a workflow secret needs no Terraform: which grant? | The prefix-conditioned grant (one, at install), over per-container grants or a project-wide accessor | **As recommended, after the owner asked why a grant is needed at all** (2026-11-23). First asked as "keep per-container grants (D2074)"; the owner rejected that framing. Reopens D2074 for workflow secrets |
| D3324 | Per-repository or project secrets? | Project only: every workflow secret is `fugaro_p_<name>`; the repository copy and D1262's repo-then-project order go | **As recommended, on the owner's own question** (2026-11-23). First asked as a naming question with a repository prefix recommended; the owner asked whether per-repository should exist at all. **Amended after review** (2026-11-23, F16): project-only is already the state under D2313; the plan moves the project copies into the grammar and leaves D2313's dormant repository-level code as that ruling keeps it (the owner's answer to the amendment) |
| D3325 | How does `update` converge existing containers? | Copy each latest enabled version into its `fugaro_p_` container as the operator, rewrite the record, then drop the old grants and containers (as built: the project record gains the copied names; the per-repository version-1 record stays until the follow-up) | **As recommended** (2026-11-23) |
| D3326 | Serve workflow secrets to repositories the switch holds off? | Yes: serving workflow secrets stops depending on the model-auth switch | **As recommended** (2026-11-23). Rests on U1. **Amended after review** (2026-11-23, F17): vertex repositories only; the flip waits for always-a-budget's removal of mode `off`, and a not-ready root converges when update re-applies it (the owner's answer to the amendment) |
| D3327 | Add file exposure? | Yes: `file: true` on a secret entry; the runner writes a `0400` tmpfs file and sets the variable to its path | **As recommended** (2026-11-23) |
| D3328 | Which repositories may fetch a project secret by default? | An explicit list per secret (`--repo`, or `--all-repos`); unlisted is refused with the fix | **As recommended** (2026-11-23) |
| D3329 | A batch `secrets:resolve` route? | No: keep `/v1/workflow-secrets`, one variable per call | **As recommended** (2026-11-23) |
| D3330 | Close the catalog build's git-credential mount here? | Yes: the catalog build fetches from the service and the identity's accessors go | **As recommended** (2026-11-23) |
| D3331 | What regression guard to add? | A mount rule in B0.2 (no `secret_key_ref`/`availableSecrets` in run-job modules and Go job builders) plus a doctor check listing any deployed job with a `value_source` | **As recommended** (2026-11-23) |
| D3332 | Refetch during long runs? | No: fetch once at bootstrap | **As recommended** (2026-11-23) |
| D3333 | How does the service learn what a run may fetch? | The run names the logical secret from its own `fugaro.yaml` (a new request field, format version bumped); the service checks the verified job's repository against the secret's allow-list | **As recommended** (2026-11-23). The unit of access becomes the repository, not the workflow |
| D3334 | *(withdrawn after review, F16: under D2313 one name has one project copy, so no collision can occur)* Two repositories hold different values under one name when `update` copies them into `fugaro_p_<name>` (D3325): what does update do? | Stop that name only: converge every other name, leave the colliding name on its current path, print one line naming both repositories and the fix (rename one in `fugaro.yaml`, store it with `fugaro secrets shared set`); the next update finishes it | **Withdrawn after review** (2026-11-23, F16). Had been answered as recommended during planning |
| D3335 | Serve workflow secrets to shared-job runs (F20)? | No: out of scope, a gap with its own follow-up spec; version 2 refuses a shared-job caller, with a test | **As recommended** (2026-11-23, asked after review round 1) |

### Consequences of the answers, stated once

- **The record changes shape (D3324, D3328, D3333).** Beside `creds/workflowsecrets/<slug>` (one
  record per repository, mapping each workflow's variables to containers, read by version 1 until the
  follow-up removes it), one project record `creds/projectsecrets` holds, for each workflow-secret
  name, the repository patterns allowed to fetch it. The run sends `{name, version: 2}`; the service
  answers the value of `fugaro_p_<name>` when the name is in the record, its container carries the
  workflow-secret marker (F18), the caller is a per-repository coding job (F21) and its verified
  repository matches the list, and otherwise refuses with the fix. The `env` field is not reused for
  another meaning (CLAUDE.md, "a field never changes meaning").
- **Commands (D3323, D3328, F14).** `fugaro secrets shared set NAME` stores the value through the
  intake (exists). `fugaro secrets allow NAME PATTERN...` / `deny` write the allow-list to the
  service's record instead of the operator-local `workflow_secrets` block, behind the same owner-only
  fence. `fugaro secrets set NAME` for a workflow secret's name is redirected to the shared form.
  No command touches Terraform; none prompts.
- **Terraform leaves (D3323, D3324).** `modules/installation/secrets.tf` stops creating a project
  copy for a workflow-secret name (each instance is removed from state first, `prevent_destroy`
  being on the resource, then update deletes the container once its value is in `fugaro_p_<name>`),
  `modules/workflow/sa.tf` stops granting the job and the service on one, and
  `local.vault_secret_names` is no longer needed for that class. `modules/repo/secrets.tf`'s dormant
  containers stay (D2313). The git credential and model credentials keep their own containers and
  paths; this plan does not move them.
- **Vertex repositories (D3326 as amended).** A vertex repository gets the service's address and the
  invoker grant whenever the installation has a service, so its runner can fetch workflow secrets;
  vertex is never model-switched, so its model path does not move (F17).
- **Granularity (D3333).** Any workflow of an allowed repository may fetch the secret, where S3 bound
  a variable to one workflow (G19). Accepted with the answer.


### Resolutions (one defensible answer, stated rather than asked)

- **R1.** Spec open question 1 (per-task token): exists (F4).
- **R2.** Spec open question 2 (where the policy lives): in the service's record, written by the
  owner's commands (D3323, D3328, D3333), not in image metadata and not in Terraform.
- **R3.** Spec open question 3 (VM identity): Batch jobs mount nothing and launch only when served
  (F3); their identity is the launcher-issued run identity, as on Cloud Run.
- **R4.** Spec open question 4: D3332.
- **R5.** §4.3 step 4's "child process only": the runner already keeps the values out of logs through
  the redactor and hands them to the agent through `agent.BuildEnv`; the review arm hands the agent
  none. The plan keeps that and adds the file mode (D3327) under the same rule.
- **R6.** §5 step 3 (dual release): the dual period already happened in S3 (mounted value wins).
  The new migration (D3325) converges inside one `fugaro update`.

## 5. Open tensions and open items

- **T1 (answered 2026-11-23).** D3331's absolute mount rule against D3320's leaving C2.3 and KS16 to
  their plans. **Answer:** the guard allows exactly two named "until" rows, the shared jobs' App key
  (until C2.3) and provider keys (until KS16); the owning task's pull request deletes its row, and
  nothing else may be added.
- **T3 (answered 2026-11-23, after review round 1).** T1's absolute rule cannot pass: vertex
  repositories keep git and model mounts (G7), the image-check job and unserved builds keep the git
  credential, and per-repository provider keys still mount. **Answer:** the mount rule is scoped to
  the workflow-secret class (no workflow or build secret mounted in a run job, no accessor on one for
  the catalog build identity); every other class stays its existing `secret-readers.md` row with the
  plan that ends it. This amends T1's "exactly two": the two until-rows remain for the shared jobs'
  classes C2.3 and KS16 own, inside a rule that no longer claims the other classes.
- **T2 (withdrawn after review, F16, with D3334).** Its premise, a colliding name, cannot occur under
  D2313; the version-1 route's removal is now the follow-up in the plan's "After this plan" (once no
  supported job image speaks version 1). Recorded as first answered: D3334 leaves a colliding name "on its current path", and for a
  held-off (vertex) repository that path is a mount, which T1's two-row guard does not admit.
  **Answer:** a colliding name stays on S3's per-container fetch (`/v1/workflow-secrets` version 1
  and the service's per-container grant), for held-off repositories too (they gain the address,
  D3326), so no job mounts it and the guard stays at two rows. The version-1 route and its grant stay
  until no installation reports a collision; a follow-up task removes them (plan, "After this plan").
- **O2 (review round 3).** D3328's `--all-repos` reaches every repository enrolled in the project
  whatever its owner, which the operator-local block's owner-literal rule (`internal/config/project_secrets.go`)
  calls "the one widening this list is never meant to make". The plan keeps that rule for the local
  block and stores all-repos as its own member of the project record only. The owner confirms the
  cross-owner reach, or narrows `--all-repos` to one owner.
  **Answered 2026-11-23: every repository, any owner** — not the recommendation (one owner only).
  The record-only `all_repos` member stands as planned; the local block keeps its owner-literal rule.
- **O1.** The owner confirms that the new allow-list being repository-wide (D3333) is meant to
  replace the per-workflow binding S3 enforced (G19). Recorded as accepted; listed so a reviewer sees it.
  **Answered 2026-11-23: accepted, as recommended.**

## 6. UNVERIFIED

- **U1.** Whether a run of a repository on `agent.auth: vertex` holds a run identity (`r.sess`) that
  the service verifies. `fetchWorkflowSecrets` refuses without one. always-a-budget's "every run has
  an identity" says yes once its tasks land (10/19 merged). *Settles it:* the D3326 task's own red
  test against `internal/runner`, and Check 920 on a vertex repository. *Who:* the task's run, then
  the owner.
- **U2.** Whether the intake accepts any grammar-conforming name for `fugaro secrets shared set` or
  only credential names. *Settles it:* reading `internal/intake` in the commands task; if it filters,
  that task widens the filter to workflow-secret names and pins G2.
- **U3.** Whether `internal/infra/retire.go` can retire a workflow-secret container that has a live
  version without an operator step. *Settles it:* IS13's convergence test (the retirement task).
- **U4.** That no deployed job of the owner's installations still carries a workflow-secret
  `value_source` after the release. *Settles it:* Check 920, owner-run.
- **U5.** Whether Terraform 1.9's `removed` block can forget one `for_each` instance of
  `google_secret_manager_secret.project`, or whether `internal/infra` must run `terraform state rm`
  per instance before the apply. *Settles it:* IS13's `TestProjectCopyLeavesStateBeforeApply`. *Who:* that task's run.
