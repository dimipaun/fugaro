# Image secrets through the credentials service: reconciliation

**Checked against:** `main` at `2772b8879` (2026-11-23). **Spec:** [spec.md](spec.md), the owner's
"Route Image Secrets Through the Credential Service", verbatim. **Where this document disagrees with
`spec.md`, this document wins.**

**Headline.** The spec was written blind to the repository and reads as if the rule were unbuilt. Most
of it is built. Images declare no secrets; **workflows** do (`workflows.<w>.secrets` in `fugaro.yaml`),
and the parent plan's slice S3 (flip B3.8, shipped in 0.27.0) already serves them through the
credentials service with no mount and no job accessor, fetched by the runner at bootstrap, fail
closed, audited per call. The Cloud Batch (VM) backend mounts nothing (D3104) and there is no gen-1
Cloud Run job. What this plan does is close what is left: workflow secrets of repositories the switch
holds off, the catalog build's git credential, the Terraform that a new secret name still needs, file
exposure, and a guard that keeps a mount from coming back. The owner turned the rest of the spec into
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
- **F8 (Gap).** Adding a workflow secret needs Terraform today: `modules/repo/secrets.tf` creates each
  repository's container (`google_secret_manager_secret.this`), and the service's per-container
  accessor is `modules/workflow/sa.tf` `google_secret_manager_secret_iam_member.service` (D2074).
  Changing a value needs nothing. *Consequence:* D3323, D3324, D3325.
- **F9 (Exists).** The grant D3323 asks for is on `main`: `credentials_prefix_accessor` covers the
  `fugaro_p_` prefix, and `config.SecretNameRE` (`^[a-z0-9][a-z0-9-]{0,62}$`) fits the grammar's name
  rule (`internal/secretname` `nameRE`). *Consequence:* no new IAM for the service. The header comment
  of `internal/credsvc/dynamicsecrets.go` still says the conditioned accessor is not on the tree; that
  is stale and the task that touches the file corrects it.
- **F10 (Gap).** `fugaro_p_<name>` is also the dynamic-delivery grammar for project-shared
  credentials (model keys and the like). A workflow secret named like one of those would share its
  container. `config.ReservedSecrets` names are already refused as workflow-secret names (D1260's
  note on `localcfg.Config.WorkflowSecrets`); the plan pins that disjointness in a test (global
  constraint G2).
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

## 4. Decisions

Numbers D3320–D3339 (README); D3322 was drafted, folded into D3326 before it was asked, and is not reused. Every answer is the owner's, given 2026-11-23 in the session that wrote
this document.

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D3320 | What does this spec become, given F2? | A closure plan: the leftover classes no other plan owns; KS16 and C2.3 by reference | **As recommended** (2026-11-23): no duplicate machinery |
| D3321 | Scope of "nothing else reads Secret Manager" | Run jobs only (per-repository, shared, Batch, build, image check); installation services stay listed exceptions under B0.2 | **As recommended** (2026-11-23) |
| D3323 | Adding, removing or changing a workflow secret needs no Terraform: which grant? | The prefix-conditioned grant (one, at install), over per-container grants or a project-wide accessor | **As recommended, after the owner asked why a grant is needed at all** (2026-11-23). First asked as "keep per-container grants (D2074)"; the owner rejected that framing. Reopens D2074 for workflow secrets |
| D3324 | Per-repository or project secrets? | Project only: every workflow secret is `fugaro_p_<name>`; the repository copy and D1262's repo-then-project order go | **As recommended, on the owner's own question** (2026-11-23). First asked as a naming question with a repository prefix recommended; the owner asked whether per-repository should exist at all |
| D3325 | How does `update` converge existing containers? | Copy each latest enabled version into its `fugaro_p_` container as the operator, rewrite the record, then drop the old grants and containers | **As recommended** (2026-11-23) |
| D3326 | Serve workflow secrets to repositories the switch holds off? | Yes: serving workflow secrets stops depending on the model-auth switch | **As recommended** (2026-11-23). Rests on U1 |
| D3327 | Add file exposure? | Yes: `file: true` on a secret entry; the runner writes a `0400` tmpfs file and sets the variable to its path | **As recommended** (2026-11-23) |
| D3328 | Which repositories may fetch a project secret by default? | An explicit list per secret (`--repo`, or `--all-repos`); unlisted is refused with the fix | **As recommended** (2026-11-23) |
| D3329 | A batch `secrets:resolve` route? | No: keep `/v1/workflow-secrets`, one variable per call | **As recommended** (2026-11-23) |
| D3330 | Close the catalog build's git-credential mount here? | Yes: the catalog build fetches from the service and the identity's accessors go | **As recommended** (2026-11-23) |
| D3331 | What regression guard to add? | A mount rule in B0.2 (no `secret_key_ref`/`availableSecrets` in run-job modules and Go job builders) plus a doctor check listing any deployed job with a `value_source` | **As recommended** (2026-11-23) |
| D3332 | Refetch during long runs? | No: fetch once at bootstrap | **As recommended** (2026-11-23) |
| D3333 | How does the service learn what a run may fetch? | The run names the logical secret from its own `fugaro.yaml` (a new request field, format version bumped); the service checks the verified job's repository against the secret's allow-list | **As recommended** (2026-11-23). The unit of access becomes the repository, not the workflow |
| D3334 | Two repositories hold different values under one name when `update` copies them into `fugaro_p_<name>` (D3325): what does update do? | Stop that name only: converge every other name, leave the colliding name on its current path, print one line naming both repositories and the fix (rename one in `fugaro.yaml`, store it with `fugaro secrets shared set`); the next update finishes it | **As recommended** (2026-11-23). Asked during planning, after the reconciliation was written |

### Consequences of the answers, stated once

- **The record changes shape (D3324, D3328, D3333).** `creds/workflowsecrets/<slug>` (one record per
  repository, mapping each workflow's variables to containers) is replaced by one project record:
  for each secret name, the list of repository patterns allowed to fetch it. The service no longer
  needs to know a workflow's variables; the run sends `{name}` and the service answers the value of
  `fugaro_p_<name>` when the verified job's repository matches the list. The response still echoes
  what was asked. This is a new wire version of the request and the record (CLAUDE.md, "a field never
  changes meaning"); the `env` field is not reused.
- **Commands (D3323, D3328, F14).** `fugaro secrets shared set NAME` stores the value through the
  intake (exists). `fugaro secrets allow NAME PATTERN...` / `deny` write the allow-list to the
  service's record instead of the operator-local `workflow_secrets` block, behind the same owner-only
  fence. `fugaro secrets set NAME` for a workflow secret's name is redirected to the shared form.
  No command touches Terraform; none prompts.
- **Terraform leaves (D3323, D3324).** `modules/repo/secrets.tf` stops creating workflow-secret
  containers, `modules/workflow/sa.tf` stops granting the job and the service on them, and
  `local.vault_secret_names` is no longer needed for that class. The git credential and model
  credentials keep their own containers and paths; this plan does not move them.
- **Held-off repositories (D3326).** A repository the model switch holds off still gets the
  service's address and the invoker grant whenever the installation has a service, so its runner can
  fetch workflow secrets. Its model credentials stay as the switch says.
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
- **T2 (answered 2026-11-23).** D3334 leaves a colliding name "on its current path", and for a
  held-off (vertex) repository that path is a mount, which T1's two-row guard does not admit.
  **Answer:** a colliding name stays on S3's per-container fetch (`/v1/workflow-secrets` version 1
  and the service's per-container grant), for held-off repositories too (they gain the address,
  D3326), so no job mounts it and the guard stays at two rows. The version-1 route and its grant stay
  until no installation reports a collision; a follow-up task removes them (plan, "After this plan").
- **O1.** The owner confirms that the new allow-list being repository-wide (D3333) is meant to
  replace the per-workflow binding S3 enforced (G19). Recorded as accepted; listed so a reviewer sees it.

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
  version without an operator step. *Settles it:* the migration task's convergence test.
- **U4.** That no deployed job of the owner's installations still carries a workflow-secret
  `value_source` after the release. *Settles it:* Check 920, owner-run.
