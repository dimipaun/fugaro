# Image secrets through the credentials service: implementation plan

**Status: a plan. Nothing is implemented, and no task is approved.** Each task needs the owner's own
explicit go; an answered decision approves nothing. The plan builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), whose decisions D3320–D3334 the owner answered on 2026-11-23,
all as recommended (D3323 and D3324 after the owner reframed the question), plus tensions T1 and T2.

What shaped it, in one paragraph: the parent plan's S3 already serves workflow secrets through the
service for served repositories (F2), so this is a **closure plan** (D3320). Workflow secrets become
**project secrets** in the `fugaro_p_<name>` grammar (D3324), read under the grant that already
exists (F9, D3323), with a per-secret list of repositories (D3328) that the owner's commands write
and that the service checks against the run's own request by logical name (D3333). `fugaro update`
copies existing values over and retires the old containers (D3325), stopping only a name two
repositories hold differently (D3334), which stays on S3's version-1 fetch and is never mounted (T2).
Repositories the model switch holds off are served too (D3326). A secret may be exposed as a file
(D3327). The catalog build stops mounting its git credential (D3330). B0.2's guard gains a mount rule
with two named until-rows (D3331, T1), and doctor lists any deployed job's mount.

## Conventions every task follows

- `CLAUDE.md`, and the rules files the paths load: `.claude/rules/terraform.md` (`deploy/terraform/**`,
  `internal/infra/**`) and `.claude/rules/credentials-service.md` (`internal/creds/**`,
  `internal/credsvc/**`, `SECURITY.md`, `website/content/security.md`).
- **Always:** a red test before the change; targeted tests only (`go test <pkg> -run 'TestX|TestY'
  -count=1`), `gofmt -l .` empty, `go vet ./...`; `terraform fmt -check -recursive deploy/terraform`
  and `go test -tags terraform ./internal/infra/...` for Terraform; `go test ./internal/cli/ -run
  Docs` for any doc. Hermetic tests only (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`).
- **Ask first (raise, don't build):** any new wire field beyond C1's; any change to the git
  credential's or model credentials' paths; any grant beyond the ones a task names.
- **Never:** a secret-shaped literal in a test, doc or fixture; a value in a log, error, run record or
  audit line (G18); a prompt or confirmation (`docs/no-prompt-policy/`); a live cloud action by an
  agent; an edit under `.github/workflows/`.
- **Reviewer tier.** Every task here touches credentials, a write guard or IAM except IS10, IS11 and
  IS12: those run at the repository's mode (`startup`); every other task is reviewed at **opus with an
  independent review** (`fugaro review --pr N`), and C1, a contract over credentials and a wire
  format, is reviewed that way **before** any task that consumes it is launched.

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | A request's name and env are the caller's text; the service decides from the verified job account and the project record alone, and an unlisted repository is refused with a typed refusal | IS1 |
| G2 | A workflow-secret name never equals a reserved or credential name in the `fugaro_p_` namespace (F10) | IS4 |
| G3 | A file-exposed value is written `0400` under a tmpfs path, registered with the redactor first, removed at run end, and never handed to a review run's agent | IS2 |
| G4 | The migration is safe with runs in flight: a run that bootstrapped before the update still fetches (the service reads the record at call time), and the update waits for none | IS5 |
| G5 | A colliding name (D3334) is never mounted and stays served by version 1 (T2) | IS5, IS6 |
| G6 | After the flip, no run-job module or Go job builder emits a `secret_key_ref` or `availableSecrets`, except the two until-rows of T1 | IS7 |
| G7 | Model credentials and the git credential keep their current paths: the held-off address (D3326) serves workflow secrets only | IS9 |

## Slices

- **S1, inert pieces:** C1, IS3, IS4, IS10, IS12. They ship without changing what any run does.
- **S2, the served path:** IS1, IS2, IS5, I1, IS9. The service and runner speak version 2, update
  migrates, held-off repositories get the address; mounts and old containers are still there, and a
  mounted value still wins (F7).
- **S3, the flip:** IS6, then IS7 and IS11. Old containers, grants and mounts go; the guard and the
  docs follow.
- **Apart:** IS8 (catalog build) ships whenever it is ready.

S2 and S3 ship in **one release**: `fugaro update` converges an installation from the release before
in one run (CLAUDE.md, "`fugaro update` is the whole upgrade").

## Tasks

### C1: the version-2 request and the file field (contract)
- Acceptance: `credsvc.WorkflowSecretsRequest` gains
  `name` and `version` (2), keeping `env` (used only for the version-1 answer of T2);
  `config.Secret` gains `file` (bool), validated and in the schema; the contract document says what
  each side may rely on; a stub handler answers version 2 from an in-memory map.
- Files: `internal/credsvc/wire.go`, `internal/config/config.go`, `schemas/fugaro.schema.json`,
  `docs/secrets-brokering/image-secrets/contract.md` (new), stub
  `internal/credsvc/workflowsecrets_stub.go` (new).
- Interfaces: produces the version-2 request and response, `config.Secret.File`, and the stub.
- Tests: `TestWorkflowSecretsRequestV2Shape`, `TestSecretFileRequiresEnv`, `TestSchemaSecretFile`.
- Command: `go test ./internal/credsvc/ ./internal/config/ -run 'TestWorkflowSecretsRequestV2|TestSecretFile|TestSchemaSecretFile' -count=1`
- Red step: `TestWorkflowSecretsRequestV2Shape` fails to compile until the `name` field exists.
- Depends on: none.
- Commit: `credsvc, config: the version-2 workflow-secret request and the file field (C1)`
- Reviewer tier: opus, independent review before IS1 or IS2 launch.

### IS1: the service serves by name from the project record
- Acceptance: a version-2 call for a name whose list matches the verified job's repository is
  answered the latest version of `fugaro_p_<name>`; an unlisted repository is refused (typed, no
  value, no name echo beyond the caller's own bytes through `pluginwire.Printable`); a name the
  record marks colliding for this repository is answered from the version-1 record by `env` (T2);
  `/v1/build/credentials` answers build secrets the same way (F13); one audit line per call naming
  one container; the stale header of `dynamicsecrets.go` is corrected (F9).
- Files: `internal/credsvc/workflowsecrets.go`, `internal/credsvc/workflowsecrets_test.go`,
  `internal/credsvc/build.go`, `internal/credsvc/build_test.go`, `internal/credsvc/dynamicsecrets.go`.
- Interfaces: consumes C1 (request) and IS3's record reader; produces the version-2 route.
- Tests: `TestWorkflowSecretsV2ServesAListedRepository`, `TestWorkflowSecretsV2RefusesAnUnlistedRepository`,
  `TestWorkflowSecretsV2CollidedNameFallsBackToV1`, `TestBuildCredentialsV2`, `TestWorkflowSecretsAuditNamesOneContainer`.
- Command: `go test ./internal/credsvc/ -run 'TestWorkflowSecretsV2|TestBuildCredentialsV2|TestWorkflowSecretsAudit' -count=1`
- Red step: `TestWorkflowSecretsV2ServesAListedRepository` gets a refusal from the version-1 route.
- Depends on: C1 (request shape); IS3 (it reads the record through IS3's codec — a contract for the
  codec would be nearly the codec itself, so the edge is kept).
- Commit: `credsvc: serve workflow and build secrets by name from the project record (IS1, D3324, D3328, D3333)`
- Reviewer tier: opus, independent review.

### IS2: the runner fetches by name and exposes files
- Acceptance: the runner sends version 2 with each declared secret's name and env; a `file: true`
  secret is written to a `0400` file under a tmpfs directory and its variable is set to the path; the
  value is registered with the redactor before it is written; the file is removed when the run ends;
  a review run's agent receives neither the value nor the path; a mounted value still wins (F7).
- Files: `internal/runner/workflowsecrets.go`, `internal/runner/workflowsecrets_test.go`,
  `internal/runner/secretfile.go` (new), `internal/runner/secretfile_test.go` (new),
  `internal/agent/env.go`.
- Interfaces: consumes C1 (request, `config.Secret.File`), tested against C1's stub.
- Tests: `TestFetchWorkflowSecretsSendsName`, `TestSecretFileIsMode0400OnTmpfs`,
  `TestSecretFileRedactedBeforeWrite`, `TestSecretFileRemovedAtRunEnd`, `TestReviewRunGetsNoSecretFile`.
- Command: `go test ./internal/runner/ ./internal/agent/ -run 'TestFetchWorkflowSecretsSendsName|TestSecretFile|TestReviewRunGetsNoSecretFile' -count=1`
- Red step: `TestSecretFileIsMode0400OnTmpfs` finds the value in the environment instead of a path.
- Depends on: C1 (request shape and the stub).
- Commit: `runner: fetch workflow secrets by name, and expose a secret as a 0400 file (IS2, D3327, D3333)`
- Reviewer tier: opus, independent review.

### IS3: the project secret record, and the owner's allow-list writes it
- Acceptance: `internal/creds` encodes and decodes the project record at `creds/projectsecrets`
  (version 1 of that record: for each secret name, its allowed repository patterns, and the names
  update marked colliding with the repositories involved), refusing unknown fields; `fugaro secrets allow NAME PATTERN...` and `deny` write the secret's list into
  `creds/projectsecrets` for a workflow-secret name (no longer the operator-local `workflow_secrets`
  block); `--all-repos` writes `*`-for-the-owner form; refused in an agent's session with the command
  to run (D1552); no prompt; nothing reads or prints a value.
- Files: `internal/creds/projectsecrets.go` (new), `internal/creds/projectsecrets_test.go` (new),
  `internal/cli/secrets_allow.go`, `internal/cli/secrets_allow_test.go`,
  `internal/cli/secrets_policy_workflow_test.go`.
- Interfaces: produces `creds.ProjectSecrets`, `creds.ReadProjectSecrets`, `creds.WriteProjectSecrets`.
- Tests: `TestProjectSecretsRoundTrip`, `TestProjectSecretsRefusesUnknownFields`, `TestSecretsAllowWritesProjectRecord`, `TestSecretsAllowAllRepos`, `TestSecretsDenyNarrows`,
  `TestSecretsAllowRefusedInAgentSession`.
- Command: `go test ./internal/creds/ ./internal/cli/ -run 'TestProjectSecrets|TestSecretsAllow|TestSecretsDeny' -count=1`
- Red step: `TestSecretsAllowWritesProjectRecord` finds the local config changed and the record absent.
- Depends on: none.
- Commit: `creds, cli: the project secret record; secrets allow and deny write the project record for workflow secrets (IS3, D3328)`
- Reviewer tier: opus, independent review.

### IS4: storing a workflow secret goes through the intake
- Acceptance: `fugaro secrets set NAME` for a name a workflow declares stores it as `fugaro_p_<name>`
  through the intake (the act `fugaro secrets shared set` performs) and says so in one line; the
  intake accepts workflow-secret names (U2); a workflow-secret name equal to a reserved or credential
  name is refused at validate (G2).
- Files: `internal/cli/secrets.go`, `internal/cli/secrets_test.go`, `internal/intake/handlers.go`,
  `internal/intake/server_test.go`, `internal/config/validate.go`.
- Interfaces: none from C1; the grammar is `internal/secretname`'s.
- Tests: `TestSecretsSetWorkflowNameUsesIntake`, `TestIntakeAcceptsWorkflowSecretName`,
  `TestWorkflowSecretNameDisjointFromReserved`.
- Command: `go test ./internal/cli/ ./internal/intake/ ./internal/config/ -run 'TestSecretsSetWorkflowNameUsesIntake|TestIntakeAcceptsWorkflowSecretName|TestWorkflowSecretNameDisjointFromReserved' -count=1`
- Red step: `TestSecretsSetWorkflowNameUsesIntake` sees a repository-scoped container created.
- Depends on: none.
- Commit: `cli, intake: a workflow secret is stored once for the project through the intake (IS4, D3323, D3324)`
- Reviewer tier: opus, independent review.

### IS5: `fugaro update` migrates workflow secrets to the project
- Acceptance: update copies each repository's (and each project copy's) latest enabled version into
  `fugaro_p_<name>` as the operator; writes the project record from each repository's declarations
  and the existing `workflow_secrets` allow-lists; for a name whose values differ between
  repositories (compared by digest, never printed) it writes nothing for that name, marks it colliding
  in the record and prints one line naming the repositories and the fix (D3334); it retires an old
  container only once its value is in the project container; it says in one line that runs in flight
  are untouched; an installation at the previous release converges in one run.
- Files: `internal/cli/update_workflowsecrets.go`, `internal/cli/update_workflowsecrets_test.go`,
  `internal/infra/retire.go`, `internal/backend/gcp/secrets.go`, `internal/cli/update_in_flight_test.go`.
- Interfaces: consumes IS3's record writer.
- Tests: `TestUpdateCopiesWorkflowSecretsToProject`, `TestUpdateStopsACollidingName`,
  `TestUpdateRetiresOnlyCopiedContainers`, `TestUpdateFromPreviousReleaseConvergesSecrets`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ ./internal/infra/ -run 'TestUpdateCopiesWorkflowSecretsToProject|TestUpdateStopsACollidingName|TestUpdateRetiresOnlyCopiedContainers|TestUpdateFromPreviousReleaseConvergesSecrets|TestUpdateLeavesARunInFlightRunning|TestRetire' -count=1`
- Red step: `TestUpdateCopiesWorkflowSecretsToProject` finds no `fugaro_p_` container after update.
- Depends on: IS3 (it writes the record through IS3's codec).
- Commit: `update: copy workflow secrets into project containers and retire the old ones (IS5, D3325, D3334)`
- Reviewer tier: opus, independent review.

### I1: wire the version-2 path end to end (integration)
- Acceptance: deletes `internal/credsvc/workflowsecrets_stub.go`; an end-to-end hermetic test runs
  the owner's allow (IS3's writer), the migration's record (IS5), the real route (IS1) and the
  runner's fetch with a file-exposed secret (IS2); IS2's tests use the real route.
- Files: `internal/credsvc/workflowsecrets_stub.go` (deleted), `internal/e2e/projectsecrets_test.go`
  (new), `internal/runner/workflowsecrets_test.go`.
- Interfaces: consumes C1, IS1, IS2, IS3, IS5.
- Tests: `TestProjectSecretFetchedEndToEnd`, `TestCollidedSecretFetchedThroughV1EndToEnd`.
- Command: `go test ./internal/e2e/ ./internal/runner/ -run 'TestProjectSecretFetchedEndToEnd|TestCollidedSecretFetchedThroughV1EndToEnd|TestFetchWorkflowSecretsSendsName' -count=1`
- Red step: the end-to-end test fails while the runner still talks to the stub.
- Depends on: C1, IS1 and IS2 (the sides it joins); IS5 (the end-to-end test reads the migration's
  record).
- Commit: `e2e: the project secret path end to end, stub removed (I1)`
- Reviewer tier: opus, independent review.

### IS9: repositories the model switch holds off get the address
- Acceptance: whenever the installation has a service, a repository root grants its job accounts
  `roles/run.invoker` on it and the launcher puts the address on the execution, whatever
  `infra.CredentialsServiceServes` answers; model mounts and their accessors are unchanged for those
  repositories (G7); the root and the launcher read one function for the new answer; U1 is settled by
  a red test that a vertex run holds a run identity.
- Files: `internal/infra/spec.go`, `internal/infra/spec_test.go`, `internal/cli/run.go`,
  `deploy/terraform/gcp/modules/workflow/sa.tf`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`.
- Interfaces: none from C1.
- Tests: `TestWorkflowSecretsServedForVertex`, `TestHeldOffRepoKeepsModelMounts`,
  `TestLaunchPutsAddressForHeldOffRepo`, `TestVertexRunHasRunIdentity`, the repo root's tftest run.
- Command: `go test ./internal/infra/ ./internal/cli/ -run 'TestWorkflowSecretsServedForVertex|TestHeldOffRepoKeepsModelMounts|TestLaunchPutsAddressForHeldOffRepo|TestVertexRunHasRunIdentity' -count=1 && go test -tags terraform ./internal/infra/... -run Repo -count=1`
- Red step: `TestWorkflowSecretsServedForVertex` finds no invoker grant.
- Depends on: none.
- Commit: `infra: serve workflow secrets to repositories the model switch holds off (IS9, D3326)`
- Reviewer tier: opus, independent review.

### IS6: the flip (no workflow-secret mount, container or per-container grant)
- Acceptance: `modules/repo/secrets.tf` creates no workflow-secret container; `modules/workflow/sa.tf`
  grants neither the job nor the service on one, except the service's version-1 grant on a colliding
  name's container (T2); no job mounts a workflow secret (G5); the build request names none; a launch
  on a job image below the release that carries IS2 is refused (a new constant beside `vaultSince` in
  `internal/cli/recipes_skew.go`); `TestServiceWorkflowSecretGrantsMatchTheJobsMounts` is rewritten to
  the new sets.
- Files: `deploy/terraform/gcp/modules/repo/secrets.tf`, `deploy/terraform/gcp/modules/workflow/sa.tf`,
  `internal/infra/spec.go`, `internal/cli/recipes_skew.go`,
  `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`, `deploy/terraform/terraform_test.go`.
- Interfaces: consumes IS1, IS2, IS5 and IS9's behaviour.
- Tests: `TestNoWorkflowSecretMount`, `TestCollidedNameKeepsV1GrantOnly`, `TestLaunchRefusesImageBelowNameFetch`,
  `TestServiceWorkflowSecretGrantsMatchTheJobsMounts`, the repo root's tftest run.
- Command: `go test ./internal/infra/ ./internal/cli/ ./deploy/terraform/ -run 'TestNoWorkflowSecretMount|TestCollidedNameKeepsV1GrantOnly|TestLaunchRefusesImageBelowNameFetch|TestServiceWorkflowSecretGrantsMatchTheJobsMounts' -count=1 && go test -tags terraform ./internal/infra/... -run Repo -count=1`
- Red step: `TestNoWorkflowSecretMount` finds `secret_key_ref` on a held-off repository's job.
- Depends on: I1 (the version-2 path works end to end before anything is taken away); IS5 (the
  values are in the project containers before the old ones go); IS9 (held-off repositories fetch
  before their mounts go; both edit `spec.go`, `sa.tf` and `repo.tftest.hcl`).
- Commit: `terraform, infra: workflow secrets leave every job spec (IS6, D3323, D3324, D3326)`
- Reviewer tier: opus, independent review.

### IS7: the guard's mount rule
- Acceptance: `deploy/terraform/secret_readers_test.go` fails on any `secret_key_ref`,
  `value_source` or `availableSecrets` in a run-job module (`modules/workflow`, `modules/project`,
  `modules/repo`) or in Go's job and build builders, except the two until-rows of T1 (shared jobs'
  App key until C2.3, provider keys until KS16); `secret-readers.md` rows E1, E3, E6 and E7 are
  rewritten to the new state; a deliberately bad fixture is shown to fail (spec §6).
- Files: `deploy/terraform/secret_readers_test.go`, `docs/secrets-brokering/secret-readers.md`.
- Interfaces: reads the tree IS6 and IS8 leave.
- Tests: `TestRunJobsMountNoSecret`, `TestMountRuleUntilRowsAreExactlyTwo`, `TestMountRuleCatchesAFixture`,
  `TestSecretReadersDocMatchesTheTable`.
- Command: `go test ./deploy/terraform/ -run 'TestRunJobsMountNoSecret|TestMountRule|TestSecretReaders' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestMountRuleCatchesAFixture` passes a fixture with a `secret_key_ref` before the rule exists.
- Depends on: IS6 and IS8 (the rule is true only on the tree they leave).
- Commit: `terraform: no run job mounts a secret, two named until-rows (IS7, D3331, T1)`
- Reviewer tier: opus, independent review.

### IS8: the catalog build fetches its git credential
- Acceptance: a catalog entry's build request carries no `availableSecrets` and its credential step
  reads the git credential from the service (`/v1/build/token`, the route the catalog identity already
  calls); `modules/installation/build_identity.tf` keeps no
  `google_secret_manager_secret_iam_member.build_identity_git_secret`; an installation at the
  previous release converges.
- Files: `internal/backend/gcp/catalog.go`, `internal/backend/gcp/catalog_test.go`,
  `deploy/terraform/gcp/modules/installation/build_identity.tf`,
  `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`, `internal/cli/gitcred.go`.
- Interfaces: none from C1.
- Tests: `TestCatalogBuildRequestNamesNoSecret`, `TestCatalogGitCredentialFromService`, the
  installation root's tftest run.
- Command: `go test ./internal/backend/gcp/ ./internal/cli/ -run 'TestCatalogBuildRequestNamesNoSecret|TestCatalogGitCredentialFromService' -count=1 && go test -tags terraform ./internal/infra/... -run Installation -count=1`
- Red step: `TestCatalogBuildRequestNamesNoSecret` finds `availableSecrets`.
- Depends on: none.
- Commit: `catalog: the build reads its git credential from the service, no accessor (IS8, D3330)`
- Reviewer tier: opus, independent review.

### IS10: doctor lists a deployed job's mounts
- Acceptance: `fugaro doctor` reports, by job and variable name (never a value), any deployed run job
  whose template carries a `value_source`, except the two T1 classes; `--json` carries the same rows;
  no prompt.
- Files: `internal/cli/doctor_job_mounts.go` (new), `internal/cli/doctor_job_mounts_test.go` (new),
  `internal/cli/doctor.go`, `internal/gcpfake/run.go`.
- Interfaces: none.
- Tests: `TestDoctorJobMountsFlagsAWorkflowJob`, `TestDoctorJobMountsAllowsTheTwoUntilClasses`,
  `TestDoctorJobMountsJSON`.
- Command: `go test ./internal/cli/ -run 'TestDoctorJobMounts' -count=1`
- Red step: `TestDoctorJobMountsFlagsAWorkflowJob` finds no row.
- Depends on: none.
- Commit: `doctor: list any deployed job that mounts a secret (IS10, D3331)`
- Reviewer tier: standard (`startup`).

### IS11: the docs and the release note
- Acceptance: `SECURITY.md` and `website/content/security.md` say workflow secrets are project
  secrets served by the service; the `fugaro.yaml` reference documents `file:`; the commands
  reference documents `secrets allow --all-repos` and the store path; the release notes' `### For
  operators` is "run `fugaro update`" plus the one line a collision prints (D3334); the plugin skills
  name only commands and flags that exist.
- Files: `SECURITY.md`, `website/content/security.md`, `website/content/cli-reference.md`,
  `docs/releases/` (the next release's notes), `plugin/skills/working/SKILL.md`.
- Interfaces: describes IS2, IS3, IS4, IS6.
- Tests: `go test ./internal/cli/ -run Docs`, `go test ./plugin/...`, `npm run check` in `website/`.
- Command: `go test ./internal/cli/ -run Docs -count=1 && go test ./plugin/... -count=1 && (cd website && npm run check)`
- Red step: the plugin's command test fails on `--all-repos` until IS3 has merged.
- Depends on: IS6 (the behaviour it describes); IS4 (the store path it documents).
- Commit: `docs: workflow secrets are project secrets served by the credentials service (IS11)`
- Reviewer tier: opus, independent review (touches `SECURITY.md` and a plugin skill).

### IS12: Checks 920 and 921, the owner's script
- Acceptance: `scripts/live-checks/check920.sh` with `--dry-run` (calls nothing), the run (every run
  job of the installation has no `value_source` outside the two T1 classes; a run on a vertex
  repository fetches a workflow secret; a `file: true` secret arrives as a `0400` file; the
  service's audit log holds one line per fetch), and `--catalog` for Check 921 (a catalog build
  succeeds with no accessor on the build identity); the steps file; the register's sections.
- Files: `scripts/live-checks/check920.sh` (new), `scripts/live-checks/check920_test.sh` (new),
  `docs/secrets-brokering/image-secrets/check920-steps.md` (new), `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: `scripts/live-checks/check920_test.sh`, `go test ./internal/cli/ -run Docs`.
- Command: `bash scripts/live-checks/check920_test.sh && go test ./internal/cli/ -run Docs -count=1`
- Red step: `check920_test.sh` fails while `--dry-run` would call `gcloud`.
- Depends on: none.
- Commit: `live-checks: Checks 920 and 921 for image secrets (IS12)`
- Reviewer tier: standard (`startup`).
- Owner-run (after the release, not a node): `scripts/live-checks/check920.sh --dry-run`, then
  `scripts/live-checks/check920.sh`, then `scripts/live-checks/check920.sh --catalog`, pasting each
  block into the register. No agent runs any step of it.

## Parallel execution

### Edges broken by C1

| Was | Why the interface was enough | Integration |
| --- | --- | --- |
| IS2 needs IS1 | the runner needs the version-2 request and answer, not the route's code; it tests against C1's stub | I1 |

### Edges kept

| Edge | Reason |
| --- | --- |
| IS1 needs IS3, IS5 needs IS3 | interface in principle, but the record's codec is the whole of what a contract would hold: a contract nearly the task it unblocks is a loss, and breaking it would not shorten the path (IS3 is at level 1 either way) |
| I1 needs IS5 | data: the end-to-end test reads the record the migration writes |
| IS6 needs I1 | behaviour: nothing is taken away until the version-2 path works end to end |
| IS6 needs IS5 | data: the values must be in the project containers before the old ones go |
| IS6 needs IS9 | behaviour and files: held-off repositories must fetch before their mounts go; both edit `spec.go`, `sa.tf`, `repo.tftest.hcl` |
| IS7 needs IS6, IS8 | behaviour: the mount rule is true only on the tree they leave (T1's two rows, no more) |
| IS11 needs IS6, IS4 | behaviour: it documents what they do, and the plugin tests check the flags exist |
| I1 needs C1, IS1, IS2 | the integration joins the sides C1 separated |

### Levels

| Level | Tasks |
| --- | --- |
| 1 | C1, IS3, IS4, IS8, IS9, IS10, IS12 |
| 2 | IS1, IS2, IS5 |
| 3 | I1 |
| 4 | IS6 |
| 5 | IS7, IS11 |

**Critical path:** C1 → IS2 → I1 → IS6 → IS11, with C1 or IS3 → IS1 → I1 → IS6 → IS7 the same length. The graph read is quoted under "Graph read" below.

### Shared files

| File | Writers (level) |
| --- | --- |
| `internal/infra/spec.go` | IS9 (1), IS6 (4) |
| `deploy/terraform/gcp/modules/workflow/sa.tf` | IS9 (1), IS6 (4) |
| `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl` | IS9 (1), IS6 (4) |
| `internal/runner/workflowsecrets_test.go` | IS2 (2), I1 (3) |
| `internal/credsvc/workflowsecrets_stub.go` | C1 (1, creates), I1 (3, deletes) |
| `internal/cli/*` | IS4 `secrets.go` (1), IS10 `doctor*.go` (1), IS9 `run.go` (1), IS3 `secrets_allow*.go` and `secrets_policy_workflow_test.go` (1), IS5 `update_*.go` (2), IS6 `recipes_skew.go` (4) — no file has two writers in one level |
| `docs/gcp-live-checklist.md` | IS12 (1) |
| `docs/secrets-brokering/secret-readers.md` | IS7 (5) |

No level holds two writers of one file. C1's files (`internal/credsvc/wire.go`, `internal/config/config.go`'s `Secret`, `schemas/fugaro.schema.json`'s
secret entry, `contract.md`, the stub) belong to C1 and its change requests alone.

### Contract changes anticipated

- **R-request** (raised by IS1 or IS2): if the version-1 fallback for a colliding name (T2) cannot be
  answered from `env` alone. Reaches IS1, IS2, I1.

## Graph read

`implementation-plan.graph.json` beside this file, read with `fugaro plan graph --plan
docs/secrets-brokering/image-secrets/implementation-plan.md` on 2026-11-23. It reports 5 levels,
`critical path (5 tasks): C1 -> IS2 -> I1 -> IS6 -> IS11` (C1 → IS1's chain through IS3 is the same
length), `bottleneck: C1`, and `gain: 1 broken, 8 kept, 3 added; before 5 tasks, weight 13; after 5
tasks, weight 11; improves`.

The estimate, as the read printed it:

`ready in 287–1257 min, no history yet for 7 tasks (15 tasks; tiers for 8, seeds for 7; from the cached history of 2026-11-22 07:25 UTC)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, and it moves as runs finish. The graph counts 15 nodes: the 14 tasks above and the
anticipated contract change R-request, which is not a run unless IS1 or IS2 raises it.

**Where the graph and this text differ.** The text has IS1 and I1 waiting on C1; the graph does not
carry those two edges, because `fugaro plan graph` admits a wait on a contract only from the consumer
side of the edge it broke (IS2). Neither changes a level: IS1 waits on IS3 and I1 on IS1 and IS2, so
both start after C1 has merged.

## Checks

| Check | Measures | Who |
| --- | --- | --- |
| 920 | No run job has a `value_source` outside T1's two classes; a vertex repository's run fetches; a file secret arrives `0400`; one audit line per fetch (spec §6 criteria 1–3, U1, U4) | the owner, after the release |
| 921 | A catalog build succeeds with no accessor on the build identity (D3330) | the owner, after the release |

Checks 922–929 are reserved and unused.

## After deploy

The release note's `### For operators` is "run `fugaro update`", plus what it prints when two
repositories hold one name differently (D3334). No other step.

## After this plan

- Removing the version-1 route and its per-container grant once no installation reports a colliding
  name (T2): a follow-up task, filed when doctor and update both report none.
- C2.3 and KS16 each delete their until-row from the guard in their own pull requests (T1).

## Considered and declined

- A batch `secrets:resolve` route (D3329), image-level secret metadata (F1), a gen-1 or VM backend
  task (F3), refetching during a run (D3332), a per-repository container prefix (D3324), moving the
  installation services' reads behind the service (D3321).
