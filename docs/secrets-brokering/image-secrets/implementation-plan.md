# Image secrets through the credentials service: implementation plan

**Status: a plan. Nothing is implemented, and no task is approved.** Each task needs the owner's own
explicit go; an answered decision approves nothing. The plan builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md): decisions D3320–D3335 as the owner answered them on
2026-11-23, all as recommended (D3323 and D3324 after the owner reframed the question; D3324 and
D3326 amended after review round 1 and answered again; D3334 withdrawn after review), and tensions
T1 (amended by T3) and T3 (T2 withdrawn).

What shaped it, in one paragraph: the parent plan's S3 already serves workflow secrets through the
service for served repositories (F2), and D2313 already makes the project copy the only one a run
reads (F16), so this is a **closure plan** (D3320). The project copies move into the `fugaro_p_<name>`
grammar (D3324) under the grant that already exists (F9, D3323), with a per-secret list of
repositories (D3328) that only the owner writes and that the service checks against the run's own
request by logical name (D3333). `fugaro update` copies the values over (D3325). Vertex repositories
are served too (D3326). A secret may be exposed as a file outside review stages (D3327, F19). The
catalog build stops mounting its git credential (D3330). B0.2's guard gains a mount rule for the
workflow-secret class (D3331, T3), and doctor lists any deployed job's workflow-secret mount.
Shared-job runs are out of scope (D3335).

## Conventions every task follows

- `CLAUDE.md`, and the rules files the paths load: `.claude/rules/terraform.md` (`deploy/terraform/**`,
  `internal/infra/**`) and `.claude/rules/credentials-service.md` (`internal/creds/**`,
  `internal/credsvc/**`, `SECURITY.md`, `website/content/security.md`).
- **Always:** a red test before the change; targeted tests only (`go test <pkg> -run 'TestX|TestY'
  -count=1`), `gofmt -l .` empty, `go vet ./...`; `terraform fmt -check -recursive deploy/terraform`
  and `go test -tags terraform ./internal/infra/...` for Terraform; `go test ./internal/cli/ -run
  Docs` for any doc. Hermetic tests only (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`).
- **Ask first (raise, don't build):** any new wire field beyond C1's; any change to the git
  credential's or model credentials' paths; any grant beyond the ones a task names; anything that
  touches D2313's dormant repository-level code.
- **Never:** a secret-shaped literal in a test, doc or fixture; a value in a log, error, run record or
  audit line (G18); a prompt or confirmation (`docs/no-prompt-policy/`); a live cloud action by an
  agent; an edit under `.github/workflows/`; a grant derived from a repository's own `fugaro.yaml`
  (F22).
- **Reviewer tier.** Every task here touches credentials, a write guard or IAM except IS10, IS11 and
  IS12: those run at the repository's mode (`startup`); every other task is reviewed at **opus with an
  independent review** (`fugaro review --pr N`). C1, a contract over credentials and a wire format, is
  reviewed that way **before IS1 or IS2 is launched** — a launch hold stated on each, because the
  graph cannot carry IS1's wait on C1 (see "Graph read").

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | A request's name is the caller's text; the service decides from the verified job account and the project record alone; an unlisted repository is refused, typed, with the fix | IS1 |
| G2 | A workflow secret is stored only into a container that carries the workflow-secret marker or does not exist yet; the service serves only names the record holds whose container carries the marker (F18) | IS4, IS5, IS1 |
| G3 | A file-exposed value is written `0400` under a tmpfs path, registered with the redactor first; no file exists while a review stage runs, none in a review run, and it is removed at run end (F19) | IS2 |
| G4 | The migration is safe with runs in flight: a run that bootstrapped before the update still fetches, a job template that still names an old container keeps it until no deployed template does, and the update waits for none | IS5, IS6 |
| G5 | Version 2 refuses every caller class but a per-repository coding job: review, shared job, build identity, coordinator, a person (F21, D3335) | IS1 |
| G6 | After the flip, no run-job module or Go job builder mounts a workflow or build secret, and the catalog build identity holds no accessor on one; other classes stay their `secret-readers.md` rows (T3) | IS7 |
| G7 | Model credentials and the git credential keep their current paths: only vertex repositories gain the address (D3326, F17) | IS9 |
| G8 | Only the owner's commands write the allow-list, through the same compare-and-swap, backup and widening diff `fugaro secrets allow` already uses; update merges into it and never overwrites a newer entry | IS3, IS5 |

## Slices

- **S1, inert pieces:** C1, IS3, IS4, IS10, IS12. IS3 writes the project record **and** the
  operator-local block, so version 1 keeps working; nothing a run does changes.
- **S2, the served path:** IS1, IS2, IS5, I1, IS9. The service and runner speak version 2, update
  copies the values, vertex repositories get the address; mounts and old containers are still there,
  and a mounted value still wins (F7).
- **S3, the flip:** IS6, then IS7 and IS11. Workflow-secret mounts, grants and project-copy
  containers go; the guard and the docs follow.
- **Apart:** IS8 (catalog build) ships whenever it is ready.

S2 and S3 ship in **one release**: `fugaro update` converges an installation from the release before
in one run (CLAUDE.md, "`fugaro update` is the whole upgrade").

**External gate.** IS6 merges only after always-a-budget's removal of budget mode `off` (D3009) is on
`main`: a repository on mode `off` has no run identity, so `fetchWorkflowSecrets` refuses it once its
mount goes (`internal/runner/workflowsecrets.go`, the `sess == nil` refusal).

## Tasks

### C1: the version-2 request and the file field (contract)
- Acceptance: `credsvc.WorkflowSecretsRequest` gains `name` and `version` (2); `env` keeps its
  meaning and is not read by version 2; the response echoes `name`; `config.Secret` gains `file`
  (bool), validated (a `file` secret still names its `env`, which receives the path) and in the
  schema; the contract document says what each side may rely on, including the refusal codes version 2
  answers; a stub handler answers version 2 from an in-memory map.
- Files: `internal/credsvc/wire.go`, `internal/config/config.go`, `internal/config/validate.go`,
  `schemas/fugaro.schema.json`, `docs/secrets-brokering/image-secrets/contract.md` (new), stub
  `internal/credsvc/workflowsecrets_stub.go` (new).
- Interfaces: produces the version-2 request and response, the refusal codes, `config.Secret.File`,
  and the stub.
- Tests: `TestWorkflowSecretsRequestV2Shape`, `TestSecretFileRequiresEnv`, `TestSchemaSecretFile`.
- Command: `go test ./internal/credsvc/ ./internal/config/ -run 'TestWorkflowSecretsRequestV2|TestSecretFile|TestSchemaSecretFile' -count=1`
- Red step: `TestWorkflowSecretsRequestV2Shape` fails to compile until the `name` field exists.
- Depends on: none.
- Commit: `credsvc, config: the version-2 workflow-secret request and the file field (C1)`
- Reviewer tier: opus, independent review before IS1 or IS2 launch.

### IS1: the service serves by name from the project record
- Acceptance: a version-2 call is answered the latest version of `fugaro_p_<name>` only when the name
  is in the project record, its container carries the workflow-secret marker, the caller is a
  per-repository coding job and its verified repository matches the name's list; otherwise a typed
  refusal with the fix (`fugaro secrets allow NAME owner/repo`), no value, the caller's bytes echoed
  only through `pluginwire.Printable`; review, shared-job, build-identity, coordinator and person
  callers are each refused (G5); `/v1/build/credentials` answers a repository build's secrets the
  same way (F13); version 1 is answered as today; one audit line per call naming one container; the
  stale header of `dynamicsecrets.go` is corrected (F9).
- Files: `internal/credsvc/workflowsecrets.go`, `internal/credsvc/workflowsecrets_test.go`,
  `internal/credsvc/build.go`, `internal/credsvc/build_test.go`, `internal/credsvc/dynamicsecrets.go`.
- Interfaces: consumes C1 (request) and IS3's record reader; produces the version-2 route.
- Tests: `TestWorkflowSecretsV2ServesAListedRepository`, `TestWorkflowSecretsV2RefusesAnUnlistedRepository`,
  `TestWorkflowSecretsV2RefusesAnUnmarkedContainer`, `TestWorkflowSecretsV2RefusesEachOtherClass`,
  `TestBuildCredentialsV2`, `TestWorkflowSecretsAuditNamesOneContainer`.
- Command: `go test ./internal/credsvc/ -run 'TestWorkflowSecretsV2|TestBuildCredentialsV2|TestWorkflowSecretsAudit' -count=1`
- Red step: `TestWorkflowSecretsV2ServesAListedRepository` gets a refusal from the version-1 route.
- Depends on: IS3 (it reads the record through IS3's codec — a contract for the codec would be nearly
  the codec itself, so the edge is kept). **Launch hold:** not before C1 has merged after its
  independent review.
- Commit: `credsvc: serve workflow and build secrets by name from the project record (IS1, D3324, D3328, D3333)`
- Reviewer tier: opus, independent review.

### IS2: the runner fetches by name and exposes files
- Acceptance: the runner sends version 2 with each declared secret's name; a `file: true` secret is
  written to a `0400` file under a tmpfs directory and its variable is set to the path, the value
  registered with the redactor before it is written; the file is removed before every review stage
  and rewritten after it, never written in a review run, and removed at run end (G3); a mounted value
  still wins (F7).
- Files: `internal/runner/workflowsecrets.go`, `internal/runner/workflowsecrets_test.go`,
  `internal/runner/secretfile.go` (new), `internal/runner/secretfile_test.go` (new),
  `internal/agent/env.go`.
- Interfaces: consumes C1 (request, `config.Secret.File`), tested against C1's stub.
- Tests: `TestFetchWorkflowSecretsSendsName`, `TestSecretFileIsMode0400OnTmpfs`,
  `TestSecretFileRedactedBeforeWrite`, `TestSecretFileAbsentDuringReviewStage`,
  `TestSecretFileRemovedAtRunEnd`, `TestReviewRunGetsNoSecretFile`.
- Command: `go test ./internal/runner/ ./internal/agent/ -run 'TestFetchWorkflowSecretsSendsName|TestSecretFile|TestReviewRunGetsNoSecretFile' -count=1`
- Red step: `TestSecretFileIsMode0400OnTmpfs` finds the value in the environment instead of a path.
- Depends on: C1 (request shape and the stub). **Launch hold:** not before C1 has merged after its
  independent review.
- Commit: `runner: fetch workflow secrets by name, and expose a secret as a 0400 file outside review stages (IS2, D3327, D3333)`
- Reviewer tier: opus, independent review.

### IS3: the project secret record, and the owner's allow-list writes it
- Acceptance: `internal/creds` encodes and decodes the project record at `creds/projectsecrets`
  (version 1 of that record: for each workflow-secret name, its allowed repository patterns),
  refusing unknown fields; `fugaro secrets allow NAME PATTERN...` and `deny` write the name's list
  into the record **and** the operator-local `workflow_secrets` block, through the compare-and-swap,
  backup and widening diff the family already uses (G8); `--all-repos` writes `*`, every repository
  enrolled in the project whatever its owner; refused in an agent's session with the command to run
  (D1552); no prompt; nothing reads or prints a value.
- Files: `internal/creds/projectsecrets.go` (new), `internal/creds/projectsecrets_test.go` (new),
  `internal/cli/secrets_allow.go`, `internal/cli/secrets_allow_test.go`,
  `internal/cli/secrets_policy_workflow_test.go`.
- Interfaces: produces `creds.ProjectSecrets`, `creds.ReadProjectSecrets`, `creds.WriteProjectSecrets`
  (compare-and-swap).
- Tests: `TestProjectSecretsRoundTrip`, `TestProjectSecretsRefusesUnknownFields`,
  `TestSecretsAllowWritesProjectRecordAndLocalBlock`, `TestSecretsAllowAllRepos`,
  `TestSecretsAllowLosesNoConcurrentWrite`, `TestSecretsDenyNarrows`, `TestSecretsAllowRefusedInAgentSession`.
- Command: `go test ./internal/creds/ ./internal/cli/ -run 'TestProjectSecrets|TestSecretsAllow|TestSecretsDeny' -count=1`
- Red step: `TestSecretsAllowWritesProjectRecordAndLocalBlock` finds the record absent.
- Depends on: none.
- Commit: `creds, cli: the project secret record; secrets allow and deny write it (IS3, D3328)`
- Reviewer tier: opus, independent review.

### IS4: storing a workflow secret goes through the intake
- Acceptance: `fugaro secrets set NAME` for a name a workflow declares stores it as
  `fugaro_p_<name>` through the intake (the act `fugaro secrets shared set` performs), labelled with
  the workflow-secret marker, and says so in one line; a store into an existing `fugaro_p_`
  container without the marker is refused with the fix (choose another name) (G2); the intake accepts
  workflow-secret names (U2). D2313's repository-level notice is unchanged.
- Files: `internal/cli/secrets.go`, `internal/cli/secrets_test.go`, `internal/intake/handlers.go`,
  `internal/intake/gcp.go`, `internal/intake/server_test.go`.
- Interfaces: none from C1; the grammar is `internal/secretname`'s.
- Tests: `TestSecretsSetWorkflowNameUsesIntake`, `TestIntakeAcceptsWorkflowSecretName`,
  `TestIntakeMarksWorkflowSecret`, `TestIntakeRefusesUnmarkedExistingContainer`.
- Command: `go test ./internal/cli/ ./internal/intake/ -run 'TestSecretsSetWorkflowNameUsesIntake|TestIntakeAcceptsWorkflowSecretName|TestIntakeMarksWorkflowSecret|TestIntakeRefusesUnmarkedExistingContainer' -count=1`
- Red step: `TestSecretsSetWorkflowNameUsesIntake` sees a `gcp.ProjectSecretID` container written.
- Depends on: none.
- Commit: `cli, intake: a workflow secret is stored once for the project through the intake (IS4, D3323, D3324)`
- Reviewer tier: opus, independent review.

### IS5: `fugaro update` copies workflow secrets into the grammar
- Acceptance: update copies the latest enabled version of each workflow-secret **project copy**
  (`gcp.ProjectSecretID`, the only copy a run reads under D2313) into `fugaro_p_<name>` with the
  marker, as the operator, comparing digests and never printing a value; it skips a name whose
  `fugaro_p_` container exists without the marker and prints the fix (G2); it seeds each name's list
  only from the operator's `workflow_secrets` `allow_data_to`, never from a repository's declarations
  (F22), merging into the record without overwriting a newer entry (G8); an operator with no read on
  the project copy is stopped with the fix (run it as the project's Owner); in a coding agent's
  session the copy step is refused like every other cloud write, with the command to run; it says in
  one line that runs in flight are untouched; an installation at the previous release converges in
  one run.
- Files: `internal/cli/update_workflowsecrets.go`, `internal/cli/update_workflowsecrets_test.go`,
  `internal/backend/gcp/secrets.go`, `internal/cli/update_in_flight_test.go`.
- Interfaces: consumes IS3's record writer.
- Tests: `TestUpdateCopiesProjectCopiesToGrammar`, `TestUpdateSkipsAnUnmarkedContainer`,
  `TestUpdateSeedsListOnlyFromAllowDataTo`, `TestUpdateMergesIntoANewerRecord`,
  `TestUpdateStopsANonOwnerWithTheFix`, `TestUpdateFromPreviousReleaseConvergesSecrets`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateCopiesProjectCopiesToGrammar|TestUpdateSkipsAnUnmarkedContainer|TestUpdateSeedsListOnlyFromAllowDataTo|TestUpdateMergesIntoANewerRecord|TestUpdateStopsANonOwnerWithTheFix|TestUpdateFromPreviousReleaseConvergesSecrets|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestUpdateCopiesProjectCopiesToGrammar` finds no `fugaro_p_` container after update.
- Depends on: IS3 (it writes the record through IS3's codec).
- Commit: `update: copy workflow secrets into the fugaro_p_ grammar (IS5, D3325)`
- Reviewer tier: opus, independent review.

### I1: wire the version-2 path end to end (integration)
- Acceptance: deletes `internal/credsvc/workflowsecrets_stub.go`; an end-to-end hermetic test runs
  the owner's allow (IS3), the migration's copy and record (IS5), the real route (IS1) and the
  runner's fetch with a file-exposed secret (IS2), and a refused unlisted repository; IS2's tests use
  the real route.
- Files: `internal/credsvc/workflowsecrets_stub.go` (deleted), `internal/e2e/projectsecrets_test.go`
  (new), `internal/runner/workflowsecrets_test.go`.
- Interfaces: consumes C1, IS1, IS2, IS3, IS5.
- Tests: `TestProjectSecretFetchedEndToEnd`, `TestUnlistedRepositoryRefusedEndToEnd`.
- Command: `go test ./internal/e2e/ ./internal/runner/ -run 'TestProjectSecretFetchedEndToEnd|TestUnlistedRepositoryRefusedEndToEnd|TestFetchWorkflowSecretsSendsName' -count=1`
- Red step: the end-to-end test fails while the runner still talks to the stub.
- Depends on: IS1 and IS2 (the sides C1 separated); IS5 (the test reads the migration's record).
- Commit: `e2e: the project secret path end to end, stub removed (I1)`
- Reviewer tier: opus, independent review.

### IS9: vertex repositories get the address
- Acceptance: whenever the installation has a service, a vertex repository's root grants its job
  accounts `roles/run.invoker` on it and the launcher puts the address on the execution; no other
  held-off repository gains it (F17); model mounts and accessors are unchanged (G7); the root and the
  launcher read one function for the answer; U1 is settled by a red test that a vertex run holds a
  run identity.
- Files: `internal/infra/spec.go`, `internal/infra/spec_test.go`, `internal/cli/run.go`,
  `deploy/terraform/gcp/modules/workflow/sa.tf`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`.
- Interfaces: none from C1.
- Tests: `TestWorkflowSecretsServedForVertex`, `TestModeOffRepoGetsNoAddress`, `TestVertexRepoKeepsModelPath`,
  `TestLaunchPutsAddressForVertexRepo`, `TestVertexRunHasRunIdentity`, the repo root's tftest run.
- Command: `go test ./internal/infra/ ./internal/cli/ -run 'TestWorkflowSecretsServedForVertex|TestModeOffRepoGetsNoAddress|TestVertexRepoKeepsModelPath|TestLaunchPutsAddressForVertexRepo|TestVertexRunHasRunIdentity' -count=1 && go test -tags terraform ./internal/infra/... -run Repo -count=1`
- Red step: `TestWorkflowSecretsServedForVertex` finds no invoker grant.
- Depends on: none.
- Commit: `infra: serve workflow secrets to vertex repositories (IS9, D3326)`
- Reviewer tier: opus, independent review.

### IS6: the flip (no workflow-secret mount, grant or project copy)
- Acceptance: `modules/installation/secrets.tf` creates no project copy for a workflow-secret name,
  each such instance removed from state before the apply (`prevent_destroy` is on the resource, U5);
  `modules/workflow/sa.tf` grants neither the job nor the service on one; no job mounts a workflow
  secret; a repository build's request names none; update deletes an old project copy only on a run
  where no deployed job template names it, and labels it retired until then (G4); a launch on a job
  image below the release that carries IS2 is refused (a new constant beside `vaultSince` in
  `internal/cli/recipes_skew.go`); `TestServiceWorkflowSecretGrantsMatchTheJobsMounts` is rewritten
  to the new sets.
- Files: `deploy/terraform/gcp/modules/installation/secrets.tf`,
  `deploy/terraform/gcp/modules/workflow/sa.tf`, `internal/infra/spec.go`, `internal/infra/retire.go`,
  `internal/cli/recipes_skew.go`, `deploy/terraform/terraform_test.go`.
- Interfaces: consumes IS1, IS2, IS5 and IS9's behaviour.
- Tests: `TestNoWorkflowSecretMount`, `TestProjectCopyLeavesStateBeforeApply`,
  `TestRetiredCopyDeletedOnlyWhenUnreferenced`, `TestLaunchRefusesImageBelowNameFetch`,
  `TestServiceWorkflowSecretGrantsMatchTheJobsMounts`, the repo and installation roots' tftest runs.
- Command: `go test ./internal/infra/ ./internal/cli/ ./deploy/terraform/ -run 'TestNoWorkflowSecretMount|TestProjectCopyLeavesStateBeforeApply|TestRetiredCopyDeletedOnlyWhenUnreferenced|TestLaunchRefusesImageBelowNameFetch|TestServiceWorkflowSecretGrantsMatchTheJobsMounts' -count=1 && go test -tags terraform ./internal/infra/... -run 'Repo|Installation' -count=1`
- Red step: `TestNoWorkflowSecretMount` finds `secret_key_ref` on a vertex repository's job.
- Depends on: I1 (the version-2 path works end to end before anything is taken away); IS5 (the
  values are in `fugaro_p_` containers before the old ones go); IS9 (vertex repositories fetch before
  their mounts go; both edit `spec.go` and `sa.tf`). External gate: always-a-budget's removal of mode
  `off` on `main`.
- Commit: `terraform, infra: workflow secrets leave every job spec (IS6, D3323, D3324, D3326)`
- Reviewer tier: opus, independent review.

### IS7: the guard's mount rule for the workflow-secret class
- Acceptance: `deploy/terraform/secret_readers_test.go` fails on a workflow or build secret mounted
  in a run-job module (`modules/workflow`, `modules/project`, `modules/repo`) or named by Go's job and
  build builders, and on any accessor of the catalog build identity on one; the shared jobs' two
  until-rows of T1 stay; every other class remains its own row (T3); `secret-readers.md` rows E1, E3,
  E6 and E7 are rewritten to the new state; a deliberately bad fixture is shown to fail (spec §6).
- Files: `deploy/terraform/secret_readers_test.go`, `docs/secrets-brokering/secret-readers.md`.
- Interfaces: reads the tree IS6 and IS8 leave.
- Tests: `TestRunJobsMountNoWorkflowSecret`, `TestCatalogIdentityHoldsNoSecretAccessor`,
  `TestMountRuleCatchesAFixture`, `TestSecretReadersDocMatchesTheTable`.
- Command: `go test ./deploy/terraform/ -run 'TestRunJobsMountNoWorkflowSecret|TestCatalogIdentityHoldsNoSecretAccessor|TestMountRule|TestSecretReaders' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestMountRuleCatchesAFixture` passes a fixture with a workflow-secret `secret_key_ref`
  before the rule exists.
- Depends on: IS6 and IS8 (the rule is true only on the tree they leave).
- Commit: `terraform: no run job mounts a workflow secret (IS7, D3331, T3)`
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

### IS10: doctor lists a deployed job's workflow-secret mounts
- Acceptance: `fugaro doctor` reports, by job and variable name (never a value), any deployed run job
  whose template mounts a workflow secret (T3's class); before the flip that lists the vertex and
  unserved repositories' mounts, which is what it is for; `--json` carries the same rows; no prompt.
- Files: `internal/cli/doctor_job_mounts.go` (new), `internal/cli/doctor_job_mounts_test.go` (new),
  `internal/cli/doctor.go`, `internal/gcpfake/run.go`.
- Interfaces: none.
- Tests: `TestDoctorJobMountsFlagsAWorkflowSecret`, `TestDoctorJobMountsIgnoresOtherClasses`,
  `TestDoctorJobMountsJSON`.
- Command: `go test ./internal/cli/ -run 'TestDoctorJobMounts' -count=1`
- Red step: `TestDoctorJobMountsFlagsAWorkflowSecret` finds no row.
- Depends on: none.
- Commit: `doctor: list any deployed job that mounts a workflow secret (IS10, D3331)`
- Reviewer tier: standard (`startup`).

### IS11: the docs and the release note
- Acceptance: `SECURITY.md` and `website/content/security.md` say workflow secrets are project
  secrets served by the service, readable only by allow-listed repositories' coding jobs; the
  `fugaro.yaml` reference documents `file:` and that a file is absent during review stages; the
  commands reference documents `secrets allow --all-repos` and the store path; the release notes'
  `### For operators` is "run `fugaro update`" (as the project's Owner, for the copy) and nothing
  else; the plugin skills name only commands and flags that exist; shared-job runs are named as not
  carrying workflow secrets (D3335).
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
- Acceptance: `scripts/live-checks/check920.sh` with `--dry-run` (calls nothing), the run (no run job
  of the installation mounts a workflow secret; a run on a vertex repository fetches one; a
  `file: true` secret arrives as a `0400` file and is absent during the run's review stage; an
  unlisted repository is refused; the service's audit log holds one line per fetch), and `--catalog`
  for Check 921 (a catalog build succeeds with no accessor on the build identity); the steps file;
  the register's sections.
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
| IS6 needs IS5 | data: the values must be in `fugaro_p_` containers before the old ones go |
| IS6 needs IS9 | behaviour and files: vertex repositories must fetch before their mounts go; both edit `spec.go` and `sa.tf` |
| IS7 needs IS6, IS8 | behaviour: the mount rule is true only on the tree they leave |
| IS11 needs IS6, IS4 | behaviour: it documents what they do, and the plugin tests check the flags exist |
| I1 needs IS1, IS2 | the integration joins the sides C1 separated |

### Levels

| Level | Tasks |
| --- | --- |
| 1 | C1, IS3, IS4, IS8, IS9, IS10, IS12 |
| 2 | IS1, IS2, IS5 (IS1 and IS2 held until C1 has merged after review) |
| 3 | I1 |
| 4 | IS6 (and the external gate) |
| 5 | IS7, IS11 |

**Critical path:** C1 → IS2 → I1 → IS6 → IS11, with IS3 → IS1 → I1 → IS6 → IS7 the same length. The
graph read is quoted under "Graph read" below.

### Shared files

| File | Writers (level) |
| --- | --- |
| `internal/infra/spec.go` | IS9 (1), IS6 (4) |
| `deploy/terraform/gcp/modules/workflow/sa.tf` | IS9 (1), IS6 (4) |
| `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl` | IS9 (1); IS6 runs that root's tests without editing the file unless its assertions move |
| `internal/runner/workflowsecrets_test.go` | IS2 (2), I1 (3) |
| `internal/credsvc/workflowsecrets_stub.go` | C1 (1, creates), I1 (3, deletes) |
| `internal/config/validate.go` | C1 (1) |
| `internal/backend/gcp/secrets.go` | IS5 (2) |
| `internal/infra/retire.go` | IS6 (4) |
| `internal/cli/*` | IS4 `secrets.go` (1), IS10 `doctor*.go` (1), IS9 `run.go` (1), IS3 `secrets_allow*.go` and `secrets_policy_workflow_test.go` (1), IS5 `update_*.go` (2), IS6 `recipes_skew.go` (4) — no file has two writers in one level |
| `internal/intake/*` | IS4 (1) |
| `docs/gcp-live-checklist.md` | IS12 (1) |
| `docs/secrets-brokering/secret-readers.md` | IS7 (5) |

No level holds two writers of one file. C1's files (`internal/credsvc/wire.go`,
`internal/config/config.go`'s `Secret` and `internal/config/validate.go`'s secret check,
`schemas/fugaro.schema.json`'s secret entry, `contract.md`, the stub) belong to C1 and its change
requests alone.

### Contract changes anticipated

- **R-request** (raised by IS1; IS2 would raise the same change): if version 2's refusal codes cannot
  tell the runner "unlisted" from "unmarked" well enough to print the right fix. Reaches IS1, IS2, I1.

## Graph read

`implementation-plan.graph.json` beside this file, read with `fugaro plan graph --plan
docs/secrets-brokering/image-secrets/implementation-plan.md` on 2026-11-23. It reports 5 levels,
`critical path (5 tasks): C1 -> IS2 -> I1 -> IS6 -> IS11` (IS3 → IS1's chain is the same length),
`bottleneck: C1`, and `gain: 1 broken, 8 kept, 3 added; before 5 tasks, weight 13; after 5 tasks,
weight 11; improves`.

The estimate, as the read printed it:

`ready in 287–1257 min, no history yet for 7 tasks (15 tasks; tiers for 8, seeds for 7; from the cached history of 2026-11-22 07:25 UTC)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, and it moves as runs finish. The graph counts 15 nodes: the 14 tasks above and the
anticipated contract change R-request, which is not a run unless IS1 raises it.

**Where the graph and this text differ.** The text has IS1 and I1 waiting on C1; the graph does not
carry those two edges, because `fugaro plan graph` admits a wait on a contract only from the consumer
side of the edge it broke (IS2). I1 still starts after C1 (it waits on IS2, which waits on C1). IS1
does not: the graph makes it ready once IS3 merges, so IS1 carries an explicit launch hold — not
before C1 has merged after its independent review — and so does IS2.

## Checks

| Check | Measures | Who |
| --- | --- | --- |
| 920 | No run job mounts a workflow secret; a vertex repository's run fetches; a file secret arrives `0400` and is absent during review; an unlisted repository is refused; one audit line per fetch (spec §6 criteria 1 and 3 for the workflow-secret class, U1, U4) | the owner, after the release |
| 921 | A catalog build succeeds with no accessor on the build identity (D3330) | the owner, after the release |

Checks 922–929 are reserved and unused. Spec §6 criteria 1 and 2 as written ("zero secret references
on every Fugaro job", "no job account has any Secret Manager permission") are not this plan's to
meet: T3 scopes the rule to the workflow-secret class, and the other classes keep their rows and the
plans that end them.

## After deploy

The release note's `### For operators` is "run `fugaro update`" as the project's Owner. No other
step.

## After this plan

- Removing version 1 of `/v1/workflow-secrets` and the per-repository record `creds/workflowsecrets/<slug>`
  once no supported job image speaks version 1: a follow-up task.
- C2.3 and KS16 each delete their until-row from the guard in their own pull requests (T1).
- Workflow secrets for shared-job runs: their own spec (D3335).

## Considered and declined

- A batch `secrets:resolve` route (D3329), image-level secret metadata (F1), a gen-1 or VM backend
  task (F3), refetching during a run (D3332), a per-repository container prefix (D3324), moving the
  installation services' reads behind the service (D3321), collision handling in the migration
  (D3334, withdrawn: F16), deleting D2313's dormant repository-level code (D3324 as amended), an
  absolute mount rule over every class (T3).
