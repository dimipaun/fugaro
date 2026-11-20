# A run on Cloud Batch gets its identity: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's
explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it, all as recommended, all of 2026-11-20: the backend is finished, not
removed (D3060); the carrier is the VM-attested release (D3061); the release lives in the
credentials service (D3062); Batch learns shared project jobs (D3063); a Batch run's credentials
come from the credentials service alone (D3064). Review round 1 added two: launchers get `actAs`
on shared project job accounts where Batch is provisioned (D3065), and the reaper ends a sealed
run that queues past its token's life (D3066). D3061 was asked again with the fact that a
launcher can already mint (F24), and stands. D3062 is amended after review and awaits
confirmation (O1).

It is 18 runs: one contract, 16 tasks and one integration. Every one is a security path.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/credentials-service.md` (BR2, BR3, BR5, BR12),
`.claude/rules/terraform.md` (BR5, BR6), `docs/no-prompt-policy/`,
`docs/design/update-in-flight.md`.

- **Always.** Targeted tests, never a whole package. Hermetic: `internal/gcpfake`,
  `internal/testutil`, `t.TempDir()`. A task that makes a pinned sentence false changes the
  sentence and its test in the same change, and runs `go test ./internal/cli/ -run Docs -count=1`.
  A task names the `BR#` and `D#` it implements in its pull request.
- **Ask first.** A new dependency; a permission or role beyond the ones this plan names; any
  change to a file the contract owns (raise a contract change instead); anything that would make a command
  wait for input.
- **Never.** A live cloud call from an agent or a test. A secret-shaped literal in a test, a
  document, a log or a fixture: keys in tests are generated in the test. The seal key or the
  wrapped blob in a log line, an error or an audit line. An edit under `.github/workflows/`. A
  deletion of a secret, a secret version or a KMS key version.
- **Review.** Every task is reviewed independently on the strongest model before it merges, and
  never merges on an in-loop verdict alone. Every task carries at least one mutation check in its
  review.
- **Live checks** are scripts with a hermetic self-test; the owner runs them, on `fugaro-dev`
  only, never `belong`.

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The plain seal key is never a variable of a Batch job, in any field (F3) | BR1 |
| A blob for one job releases nothing to another job's VM, to a VM whose job has ended, to a job created outside the bound, to a second job under the same name, or to a VM outside the installation's project (R1) | BR2, BR3 |
| The route admits the per-repository and shared job classes only (R1) | BR2 |
| The release route writes exactly one audit line per refusal once the caller is verified, and none carries the key or the blob | BR2 |
| A job carrying both the plain marker and the wrapped one stops the run (R5) | BR4 |
| The service unreachable stops the run after a bounded retry; it never proceeds without a session (F23) | BR4 |
| Where the block names no key, the launch is refused with `fugaro update` as its fix; no launch reads the cloud to decide (R3) | BR1, BR7 |
| `fugaro update` on an installation at the previous version converges, with a Batch run in flight keeping everything it holds (R9) | BR6, I1 |
| The reaper's role gains nothing. Launchers gain `actAs` on shared project job accounts where Batch is provisioned (D3065) and nothing on the key | BR5, BR6 |
| The credentials service account gains, only where Batch is provisioned: decrypt on one key, `batch.jobs.get` conditioned on the Fugaro job-name prefix, and the one read Check 810 names for the link (D3062 as amended, O1) | BR5 |
| The block's key fact is written by `fugaro update` alone, and a machine whose fact differs from the published one fails doctor (R13) | BR6, BR7 |
| A sealed run does not start on a dead token (D3066) | BR14 |
| Nothing is created per run (F18) | I1 |

## 3. The contract

`planning.decoupling` is `true` for this repository, and the repository's rigor default is
`startup`. This contract is over credentials and a wire format, so it is written in full and
reviewed at the strictest tier before any side starts, whatever the default says.

### C1: the key-release wire (`key-release`)
- Acceptance: the shapes below compile, the stub refuses every release, and
  `contract-key-release.md` says what each side may rely on.
- Holds: the wrapped blob's envelope (version byte, the fields R1 names, the padding and hash the
  encryption uses, the size bound) with its encode and decode; the route's path, request and
  response; the interfaces `JobLink` (is this VM a VM of this job, and is the job live and created
  within the bound) and `KeyOpener` (decrypt with the installation's key); the names
  `FUGARO_BUDGET_TOKEN_WRAPPED` (R5); the rule that the job's digest and the wrap time sit
  inside the encrypted plaintext; the classes the route admits; the record that makes a release
  once-only; the link's permission, stated as "named by Check 810" and not guessed; the settings
  registry's entry for the key fact as a key no settings command writes (R13); the `batch:`
  block's key fact (the key version's name and its public key); the compute root's new output and input names.
- Files: `internal/budget/token/wrap_contract.go` (new), `internal/credsvc/sealkey_wire.go` (new),
  `internal/credsvc/sealkey_stub.go` (new, the stub), `internal/localcfg/localcfg.go` (the one field on `Batch`), `internal/localcfg/batch_sealkey.go`
  (new), `internal/settings/registry.go`, `schemas/settings.schema.json`,
  `internal/localcfg/testdata/shared.golden.yaml`,
  `docs/batch-run-identity/contract-key-release.md` (new), and the contract's own tests. Past five;
  the three before the document are one entry each.
- Tests: `TestWrappedEnvelopeRoundTrips`, `TestWrappedEnvelopeRefusesAForeignVersion`,
  `TestTheStubReleasesNothing`, `TestABatchBlockWithAKeyFactLoads`.
- Command: `go test ./internal/budget/token/ -run WrappedEnvelope -count=1 && go test ./internal/credsvc/ -run StubReleasesNothing -count=1 && go test ./internal/localcfg/ -run 'BatchBlockWithAKeyFact|Shared' -count=1 && go test ./internal/settings/ -run RegistryCoversEveryConfigField -count=1`.
- Red step: the envelope test fails to compile.
- Depends on: none.
- Commit: `batch: the key-release contract (C1, D3061, R1, R5)`.
- Reviewer tier: opus, mandatory independent review (`fugaro review --pr N`) before any side is
  written against it.

No other task's Files names a file of C1. A task that finds the contract wrong raises a contract change (section 7).

## 4. Slices and tasks

**Slice 0, inert:** C1, BRS, BR0. Nothing a user can meet changes.
**Slice 1, the sides:** BR1 to BR9, BR12, BR13. Each is inert until I1 wires it: the block names
no key until BR6 runs inside an update, so the refusal stands.
**Slice 2:** BR10, BR11.
**Slice 3:** I1. After it, an updated installation on Batch launches.

### BRS: the job definition, split along its seams (`job-environment`)
- Acceptance: `jobDefinition` builds byte for byte the job it builds today, from four helpers in
  four files, `Options` carries the fields the sides will fill (unused, zero), and the
  deployed-job variable has its one name (R6).
- Files: `internal/backend/batch/run.go`, `internal/backend/batch/batch.go`,
  `internal/backend/batch/env.go` (new), `internal/backend/batch/seal.go` (new),
  `internal/backend/batch/account.go` (new), `internal/backend/batch/secrets.go` (new),
  `internal/backend/deployedjob.go` (new), `internal/cli/docs_budget_token_channel_test.go` (it
  reads the refusal's text out of `run.go`, which moves to `seal.go`). Past five; four are moves.
- Interfaces: none consumed; produces the four helpers BR1, BR8, BR10 and BR11 each own one of,
  and the variable's name BR8 writes and BR9 reads.
- Tests: `TestTheSplitBuildsTheSameJob`, plus the package's existing job tests unchanged.
- Command: `go test ./internal/backend/batch/ -run 'TheSplitBuildsTheSameJob|JobDefinition|Launch' -count=1`.
- Red step: the golden job differs when a helper is emptied.
- Depends on: none. If AB9 of always-a-budget has an open pull request, BRS starts after it merges
  (R8: both edit `run.go`).
- Commit: `batch: the job definition in four helpers, no behaviour change (BRS)`.
- Reviewer tier: opus, mandatory independent review.

### BR0: Check 810, the probe (`live-proof`)
- Acceptance: one script measures, on a throwaway Batch job in `fugaro-dev`: what the identity
  token in full format carries inside the task's container (U2); which Google-made fact links the
  VM to its job, and whether a second job can forge it through labels or any job field (U1);
  whether a marker variable's value appears in Cloud Logging, instance metadata or the `CreateJob`
  audit record (U3); how soon a deleted job's name can be used again (U8). It prints one `PASS` or `FAIL` per fact and deletes the job it made.
- Files: `scripts/live-checks/check-810.sh` (new), `scripts/live-checks/check-810_test.sh` (new),
  `scripts/live_checks_test.go` (its list of scripts), `docs/gcp-live-checklist.md`.
- Tests: the self-test, against stubbed `gcloud`.
- Command: `bash scripts/live-checks/check-810_test.sh && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the self-test fails when the script omits the forge attempt.
- Depends on: none.
- Commit: `checks: Check 810, what links a Batch VM to its job (BR0, U1, U2, U3)`.
- Reviewer tier: opus, mandatory independent review.
- Owner-run: `CHK810_OWNER_RUN=yes bash scripts/live-checks/check-810.sh`, then paste the result
  block into the checklist. No agent performs any step of it. **BR3 waits on this result (R4).**

### BR1: the backend wraps, or refuses (`key-release`)
- Acceptance: a sealed launch on an installation whose block names the key produces a job whose
  only trace of the key is the wrapped variable, bound to that job's name; where the block names
  no key the launch is refused before anything is created, with `fugaro update` as the fix; the
  plain marker is never a job variable; the blob prints redacted and is in no run record (R7);
  the two design documents' pinned sentence and the test that holds it say what the new refusal
  says (F28).
- Files: `internal/budget/token/wrap.go` (new), `internal/budget/token/wrap_test.go` (new),
  `internal/backend/batch/seal.go`, `internal/backend/batch/seal_test.go` (new),
  `internal/cli/docs_budget_token_channel_test.go`, `docs/design/budget-token-channel.md`,
  `docs/design/compute-backend.md`. Past five; the last three are one sentence each.
- Interfaces: consumes C1 (envelope, names) and BRS (`seal.go`).
- Tests: `TestASealedBatchLaunchCarriesOnlyTheWrappedKey`, `TestAWrappedKeyNamesItsJob`,
  `TestABatchLaunchWithoutTheKeyFactIsRefusedWithItsFix`, `TestTheRefusalNeverQuotesTheKey`,
  `TestTheWrappedBlobIsRedactedAndNotRecorded`.
- Command: `go test ./internal/budget/token/ -run Wrap -count=1 && go test ./internal/backend/batch/ -run 'SealedBatchLaunch|WrappedKeyNamesItsJob|WithoutTheKeyFact|NeverQuotesTheKey|WrappedBlobIsRedacted' -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the plain key appears among the job's variables.
- Depends on: C1 (the wire); BRS (kept: one file). `docs/design/budget-token-channel.md` is also
  AB9's file: BR1 starts after AB9 merges if AB9 has an open pull request.
- Commit: `batch: a sealed launch carries the key wrapped, or is refused with its fix (BR1, D3061, R3)`.
- Reviewer tier: opus, mandatory independent review.

### BR2: the release route (`key-release`)
- Acceptance: the route releases the seal key to a verified job account whose VM `JobLink` accepts
  for the job the blob names, and to nobody else: only the per-repository and shared job classes
  are admitted; the token's project and zone must be the installation's; and a blob already
  released to one job's unique id is refused for any other. Each refusal after the caller is verified writes
  exactly one audit line, naming the field and never its content.
- Files: `internal/credsvc/sealkey.go` (new), `internal/credsvc/sealkey_test.go` (new),
  `internal/credsvc/kmsopen.go` (new), `internal/credsvc/server.go`, `internal/credsvc/audit.go`,
  `internal/gcpfake/kms.go` (new).
- Interfaces: consumes C1; uses the stub `JobLink` until I1.
- Tests: `TestTheKeyIsReleasedToTheJobsOwnVM`, `TestABlobForAnotherJobIsRefused`,
  `TestAnAccountOutsideTheJobTableIsRefused`, `TestAStaleBlobIsRefused`,
  `TestTheReviewAndPersonClassesAreRefused`, `TestAVMOutsideTheProjectIsRefused`,
  `TestABlobIsReleasedToOneJobOnly`,
  `TestEachReleaseRefusalWritesOneAuditLine`, `TestNoAuditLineCarriesTheKeyOrTheBlob`.
- Command: `go test ./internal/credsvc/ -run 'KeyIsReleased|BlobForAnotherJob|OutsideTheJobTable|StaleBlob|ReviewAndPersonClasses|VMOutsideTheProject|ReleasedToOneJobOnly|ReleaseRefusalWritesOne|CarriesTheKeyOrTheBlob' -count=1`.
- Red step: a blob for another job is released.
- Depends on: C1.
- Commit: `credsvc: the seal key is released to the run's own VM (BR2, D3061, D3062)`.
- Reviewer tier: opus, mandatory independent review.

### BR3: the link from a VM to its job (`key-release`)
- Acceptance: `JobLink` accepts a VM only on the fact Check 810 recorded, for a job that exists, is
  not finished and was created within the bound; it refuses a VM of another job, a VM whose
  launcher-set labels claim the job, and a job re-created under the same name later.
- Files: `internal/credsvc/joblink.go` (new), `internal/credsvc/joblink_test.go` (new),
  `internal/gcpfake/batch.go`.
- Interfaces: consumes C1 and the recorded result of Check 810.
- Tests: `TestTheLinkAcceptsTheJobsOwnVM`, `TestALabelIsNotALink`,
  `TestARecreatedJobIsRefused`, `TestAFinishedJobIsRefused`.
- Command: `go test ./internal/credsvc/ -run 'LinkAccepts|LabelIsNotALink|RecreatedJob|FinishedJob' -count=1`.
- Red step: a VM carrying only a forged label is accepted.
- Depends on: C1; BR0 (kept, data: the owner's result of Check 810 decides the fact, R4).
- Commit: `credsvc: a VM is its job's by what Google says, not by a label (BR3, R1, R4)`.
- Reviewer tier: opus, mandatory independent review.

### BR4: the runner fetches its key (`key-release`)
- Acceptance: a runner whose environment carries the wrapped marker obtains the seal key from the
  service with the VM's identity token, registers it with the redactor before anything can print
  it, and then takes its token as today; both markers together, or no answer within the bound,
  stop the run; the wrapped marker is stripped from every git call.
- Files: `internal/runner/budget_session.go`, `internal/runner/sealkey_fetch.go` (new),
  `internal/runner/sealkey_fetch_test.go` (new), `internal/gitops/gitops.go`.
- Interfaces: consumes C1 (route, names).
- Tests: `TestAWrappedMarkerIsExchangedForTheKey`, `TestBothMarkersStopTheRun`,
  `TestAnUnreachableServiceStopsTheRunAfterTheBound`, `TestTheFetchedKeyIsRedacted`,
  `TestGitNeverSeesTheWrappedMarker`.
- Command: `go test ./internal/runner/ -run 'WrappedMarkerIsExchanged|BothMarkersStop|UnreachableServiceStops|FetchedKeyIsRedacted' -count=1 && go test ./internal/gitops/ -run WrappedMarker -count=1`.
- Red step: the runner starts with no session when the service is down.
- Depends on: C1.
- Commit: `runner: a Batch run exchanges its wrapped marker for the seal key (BR4, R1, R5)`.
- Reviewer tier: opus, mandatory independent review.

### BR5: the key and its grants (`update-converge`)
- Acceptance: the compute root enables the KMS API and plans one key ring and one asymmetric
  decryption key that Terraform may not destroy; the credentials service account gets decrypt on
  that key and a custom role holding `batch.jobs.get`, conditioned on the Fugaro job-name prefix,
  and the one read Check 810 named for the link; launchers get nothing on the key; the reaper's
  role is unchanged; `.claude/rules/credentials-service.md` states the account's grants as they
  now are, in this same change.
- Files: `deploy/terraform/gcp/modules/compute/kms.tf` (new), `.../compute/apis.tf`,
  `.../compute/iam.tf`, `.../compute/outputs.tf`, `.../compute/variables.tf`,
  `deploy/terraform/gcp/roots/compute/main.tf`, `.../outputs.tf`, `.../variables.tf`,
  `.../tests/compute.tftest.hcl`, `internal/infra/tf/cover.go`, the golden plans (regenerated by
  `scripts/gen-golden-plans.sh`, never by hand), `.claude/rules/credentials-service.md`. Past
  five; each root file is a pass-through line.
- Interfaces: takes the output and input names from C1's contract document, and the link's
  permission from the recorded result of Check 810.
- Tests: the root's `terraform test`; `TestTheComputeRootsNewTypesAreCovered`.
- Command: `terraform fmt -check -recursive deploy/terraform && (cd deploy/terraform/gcp/roots/compute && TF_DATA_DIR=$(mktemp -d) terraform init -backend=false -lockfile=readonly && terraform validate && terraform test) && scripts/gen-golden-plans.sh && go test ./internal/infra/tf/ -run 'ComputeRootsNewTypes|Golden' -count=1 && go test -tags terraform ./internal/infra/... && tflint --chdir=deploy/terraform/gcp && sh deploy/terraform/scan.sh`.
- Red step: the tftest fails on a role holding a permission beyond the two named.
- Depends on: BR0 (kept, data: the owner's run of Check 810 and his confirmation of O1 decide the
  role). It also starts after C1 merges, for the names; the graph records the BR0 edge only,
  because a contract edge there would record a break this task does not have.
- Commit: `terraform: the Batch seal key, and who may use it (BR5, D3062, R9)`.
- Reviewer tier: opus, mandatory independent review.

### BR6: update applies the compute root (`update-converge`)
- Acceptance: `fugaro update` on an installation with a `batch:` block applies the compute root,
  records the key fact in the block, removes `batch.secrets` and the single `service_account`
  from it, feeds the root the project job accounts beside the per-repository ones (which is what
  gives launchers `actAs` on them, D3065), and publishes the block; the key fact is rewritten from
  the root's output on every run (R13); it asks nothing, is refused in an agent's session, and
  prints one line naming what it applies and that a changed apply rebuilds the preload image, with
  the cost (R12); with a Batch run in flight it says so in one line and changes
  nothing the run holds; an installation with no block is untouched; the release notes' `### For
  operators` says "run `fugaro update`".
- Files: `internal/cli/init_stages.go`, `internal/cli/init_compute.go`, `internal/cli/update.go`,
  `internal/infra/compute.go`, `internal/apply/steps.go`, `internal/cli/update_batch_test.go`
  (new), the next release's notes under `docs/releases/`. Past five; the last two are one entry
  each.
- Interfaces: consumes C1 (the block's key fact, the root's names).
- Tests: `TestUpdateAppliesTheComputeRootWhereThereIsABlock`,
  `TestUpdateRecordsTheSealKeyInTheBlock`, `TestUpdateLeavesAnInstallationWithoutABlockAlone`,
  `TestUpdateRemovesBatchSecretsInPlace`, `TestTheUpdateSaysABatchRunKeepsRunning`,
  `TestTheComputeStageUnderUpdateAsksNothing`, `TestTheComputeStageUnderUpdateIsRefusedInAnAgentSession`,
  `TestTheComputeStageNamesThePreloadRebuild`.
- Command: `go test ./internal/cli/ -run 'UpdateAppliesTheComputeRoot|UpdateRecordsTheSealKey|WithoutABlockAlone|UpdateRemovesBatchSecrets|ABatchRunKeepsRunning|ComputeStageUnderUpdate|ComputeStageNamesThePreload|RefusalMatrix|Docs' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: after an update the block names no key.
- Depends on: C1.
- Commit: `update: the compute root, the seal key and the block, in the one command (BR6, R2, R9, R10, R12, R13, D3065)`.
- Reviewer tier: opus, mandatory independent review.

### BR7: doctor and validate say where the carrier stands (`update-converge`)
- Acceptance: on an installation that mints, the seal check passes where the block names the key
  and fails with `fugaro update` as its fix where it does not; `validate` agrees offline;
  `fugaro backends use cloud-batch`, which reads the same check, follows it; a machine whose key
  fact differs from the published one fails (R13).
- Files: `internal/cli/doctor_batch.go`, `internal/cli/doctor_batch_test.go`,
  `internal/cli/validate_batch.go`, `internal/cli/validate_batch_test.go`,
  `internal/cli/backends_use_test.go`.
- Interfaces: consumes C1 (the block's key fact).
- Tests: `TestDoctorPassesAMintingBatchInstallationWithTheKey`,
  `TestDoctorNamesTheUpdateWhereTheKeyIsMissing`, `TestValidateAgreesWithDoctorOnTheSeal`,
  `TestBackendsUseFollowsTheSealCheck`, `TestDoctorFailsAKeyFactThatDiffersFromThePublishedOne`.
- Command: `go test ./internal/cli/ -run 'MintingBatchInstallationWithTheKey|NamesTheUpdateWhereTheKeyIsMissing|ValidateAgreesWithDoctorOnTheSeal|BackendsUseFollowsTheSealCheck|KeyFactThatDiffers' -count=1`.
- Red step: doctor still fails a minting installation that has the key.
- Depends on: C1. Both files are also AB9's: BR7 starts after AB9 merges if AB9 has an open pull
  request.
- Commit: `doctor: a Batch installation that mints is fine once it has the key (BR7, R3)`.
- Reviewer tier: opus, mandatory independent review.

### BR8: a Batch job carries what a deployed job carries (`job-environment`)
- Acceptance: the job's variables are every variable the deployed job's template would carry for
  the same workflow (the bucket, the budget backend, the credentials service's URL, the price
  tables, the caps and the rest, F8), built from the same values the template is built from, plus
  the name of the deployed job the run stands in for; each is reserved against a launch entry; a
  test fails when the template gains a variable the Batch job lacks; the stale comments about
  unbuilt wiring are gone.
- Files: `internal/backend/batch/env.go`, `internal/backend/batch/env_test.go` (new),
  `internal/backend/launchenv.go`, `internal/infra/spec.go` (the template's variables, exported
  as one list), `internal/infra/spec_batch_test.go` (new).
- Interfaces: consumes BRS (`env.go`, the deployed-job name).
- Tests: `TestABatchJobNamesItsBucketAndBudgetBackend`, `TestABatchJobNamesItsDeployedJob`,
  `TestABatchJobCarriesEveryVariableADeployedJobDoes`,
  `TestALaunchEntryCannotSetThem`.
- Command: `go test ./internal/backend/batch/ -run 'NamesItsBucketAndBudget|NamesItsDeployedJob|LaunchEntryCannotSet' -count=1 && go test ./internal/backend/ -run 'LaunchEnv|Reserved' -count=1 && go test ./internal/infra/ -run BatchJobCarriesEveryVariable -count=1`.
- Red step: the job has no bucket variable.
- Depends on: BRS (kept: one file, and the name it defines).
- Commit: `batch: the job's environment is a deployed job's (BR8, F8, R6)`.
- Reviewer tier: opus, mandatory independent review.

### BR9: the runner places a Batch run (`job-environment`)
- Acceptance: on Batch the runner places the run by the deployed-job variable, exactly as it
  places a Cloud Run run by its job name, and sends that name to the service; a Batch run with no
  such variable, or one that matches no candidate, stops as an unplaceable run does today.
- Files: `internal/runner/projectjob.go`, `internal/runner/projectjob_batch_test.go` (new).
- Interfaces: consumes BRS (the name).
- Tests: `TestABatchRunIsPlacedByItsDeployedJob`, `TestABatchRunWithNoDeployedJobStops`.
- Command: `go test ./internal/runner/ -run 'BatchRunIsPlaced|BatchRunWithNoDeployedJob' -count=1`.
- Red step: a Batch run ends at `cannotPlaceJob`.
- Depends on: BRS (kept: the variable's name is a constant BRS adds; no contract is needed for one name).
- Commit: `runner: a Batch run is placed by the job it stands in for (BR9, F9, R6)`.
- Reviewer tier: opus, mandatory independent review.

### BR10: Batch runs a project job's workflow (`project-jobs`)
- Acceptance: a launch whose workflow belongs to a shared project job creates a Batch job under
  that project job's account with that job's environment; a per-repository workflow runs under
  its own job's account; the block's single account is no longer read.
- Files: `internal/backend/batch/account.go`, `internal/backend/batch/account_test.go` (new),
  `internal/backend/batch/run.go`.
- Interfaces: consumes BRS (`account.go`), BR8's environment and `LaunchSpec.Job`. If the account
  cannot be derived from what `LaunchSpec` carries, it raises `R-C1-project-job-account`.
- Tests: `TestAProjectJobWorkflowRunsUnderThatJobsAccount`,
  `TestAPerRepositoryWorkflowRunsUnderItsOwnAccount`, `TestTheBlocksAccountIsNotRead`.
- Command: `go test ./internal/backend/batch/ -run 'ProjectJobWorkflowRuns|PerRepositoryWorkflowRuns|BlocksAccountIsNotRead' -count=1`.
- Red step: the launch is refused with "no project jobs yet".
- Depends on: BRS (kept: one file); BR8 (kept, behaviour: the project job's environment is built
  by BR8's helper).
- Commit: `batch: a project job's workflow runs on Batch under that job's account (BR10, D3063)`.
- Reviewer tier: opus, mandatory independent review.

### BR11: nothing is mounted on a Batch job (`service-credentials`)
- Acceptance: a Batch job carries no `secretVariables`; a launch on Batch for a repository the
  credentials service does not serve is refused before the mint, with its fix; doctor's secret
  check for the block is gone.
- Files: `internal/backend/batch/secrets.go`, `internal/cli/launch_batch.go` (new),
  `internal/cli/launch_batch_test.go` (new), `internal/cli/run.go`,
  `internal/cli/doctor_batch.go`.
- Interfaces: consumes BRS (`secrets.go`).
- Tests: `TestABatchJobMountsNothing`, `TestAnUnservedRepositoryIsRefusedOnBatchWithItsFix`,
  `TestTheRefusalComesBeforeTheMint`.
- Command: `go test ./internal/backend/batch/ -run BatchJobMountsNothing -count=1 && go test ./internal/cli/ -run 'UnservedRepositoryIsRefusedOnBatch|RefusalComesBeforeTheMint|DoctorBatch' -count=1`.
- Red step: a job with `batch.secrets` set still carries `secretVariables`.
- Depends on: BRS (kept: one file); BR7 (kept: one file, `doctor_batch.go`).
- Commit: `batch: a run's credentials come from the service alone (BR11, D3064, R10)`.
- Reviewer tier: opus, mandatory independent review.

### BR12: the documents (`live-proof`)
- Acceptance: `SECURITY.md` says who can and cannot obtain a Batch run's seal key and names the
  service's new grant; the website page is regenerated; `docs/backends.md` says how to reach a
  launching Batch installation; `SECURITY.md` also says that launchers hold `actAs` on shared
  project job accounts where Batch is provisioned (D3065) and that a launcher can mint (F24).
- Files: `SECURITY.md`, `website/content/security.md` (generated), `docs/backends.md`.
- Tests: the Docs tests; `website`'s `security.mjs`.
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`.
- Red step: the website's security test fails until the page is regenerated.
- Depends on: C1. `docs/backends.md` is also AB9's file: BR12 starts after AB9 merges if AB9 has
  an open pull request.
- Commit: `docs: a Batch run's identity, and who can reach its key (BR12, D3061, D3062, D3065)`.
- Reviewer tier: opus, mandatory independent review.

### BR13: Check 811, the proof (`live-proof`)
- Acceptance: one script launches a sandbox run on Batch, reads that it opened its session and
  was metered, and then, as a second launcher account, fails to obtain the first run's seal key
  three ways: reading the job, replaying the blob into a job of its own, and calling the release
  route. It ends with the cost and with nothing left to delete.
- Files: `scripts/live-checks/check-811.sh` (new), `scripts/live-checks/check-811_test.sh` (new),
  `scripts/live_checks_test.go` (its list of scripts), `docs/gcp-live-checklist.md`.
- Tests: the self-test.
- Command: `bash scripts/live-checks/check-811_test.sh && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the self-test fails when the replay step is removed.
- Depends on: C1; BR0 (kept: two files, the checklist and the scripts' list).
- Commit: `checks: Check 811, a Batch run's identity end to end (BR13, R11)`.
- Reviewer tier: opus, mandatory independent review.
- Owner-run, after the release and `fugaro update` on `fugaro-dev`:
  `CHK811_OWNER_RUN=yes bash scripts/live-checks/check-811.sh --second-launcher <account>`. No
  agent performs any step of it.

### BR14: the reaper ends a sealed run at its token's life (`job-environment`)
- Acceptance: a job that carries the wrapped marker and is still queued when its token's
  lifetime has passed is deleted by the reaper, reported with that reason, and reads as ended in
  the queue; a job with no marker keeps today's queue deadline; nothing else the reaper deletes
  changes.
- Files: `internal/backend/batch/reap.go`, `internal/backend/batch/reap_sealed_test.go` (new).
- Interfaces: consumes C1 (the marker's name) and `token.Lifetime`.
- Tests: `TestASealedJobQueuedPastItsTokenIsDeleted`, `TestAnUnsealedJobKeepsTheQueueDeadline`,
  `TestTheReaperNeverReadsTheBlobsValue`.
- Command: `go test ./internal/backend/batch/ -run 'SealedJobQueuedPastItsToken|UnsealedJobKeepsTheQueueDeadline|NeverReadsTheBlobsValue|Reap' -count=1`.
- Red step: a sealed job queued for two hours is left to start.
- Depends on: C1.
- Commit: `reaper: a sealed run does not outlive its token in the queue (BR14, D3066)`.
- Reviewer tier: opus, mandatory independent review.

### I1: integration (all)
- Acceptance: the stub is deleted and the real link and key opener are wired into the service;
  `batchOptions` fills the backend from the block and the configuration; end to end and
  hermetically, a sealed launch on Batch reaches an open session, an installation at the previous
  version converges under `fugaro update`, a Batch run in flight across that update keeps running,
  and no resource was created per run.
- Files: `internal/credsvc/sealkey_stub.go` (deleted), `internal/credsvc/server.go`,
  `internal/cli/cloud.go`, `internal/cli/batch_identity_end_to_end_test.go` (new),
  `internal/cli/update_in_flight_batch_test.go` (new).
- Tests: `TestABatchRunOpensItsSessionEndToEnd`, `TestASecondJobCannotOpenTheFirstRunsKey`,
  `TestUpdateConvergesABatchInstallationAtThePreviousVersion`,
  `TestUpdateLeavesABatchRunInFlightRunning`, `TestNothingIsCreatedPerBatchRun`.
- Command: `go build ./... && go vet ./... && ! git grep -n 'sealkey_stub' -- '*.go' && go test ./internal/cli/ -run 'BatchRunOpensItsSession|SecondJobCannotOpen|ConvergesABatchInstallation|LeavesABatchRunInFlight|NothingIsCreatedPerBatchRun' -count=1`.
- Red step: the build fails on the deleted stub's last user.
- Depends on: C1 and every task: BRS, BR0 to BR14.
- Commit: `batch: a run on Cloud Batch gets its identity, end to end (I1, D3060)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

**Levels.** Tasks in one level run at once.

| Level | Tasks |
| --- | --- |
| 0 | C1, BRS, BR0 |
| 1 | BR1, BR2, BR4, BR6, BR7, BR8, BR9, BR12, BR13, BR14; and BR3 and BR5 once the owner has run Check 810 |
| 2 | BR10, BR11 |
| 3 | I1 |

**Edges broken by C1**, each because the dependent needs a shape and not a behaviour:

| Was | Why the contract is enough |
| --- | --- |
| BR6 after BR5 | the update needs the root's output and input names, not the resources |
| BR1 after BR6 | the backend needs the block's key fact, not the update that writes it |
| BR7 after BR6 | doctor needs the same fact |
| BR2 after BR1 | the service needs the blob's layout, not the code that writes it |
| BR3 after BR2 | the link implements an interface the route calls; it does not need the route |
| BR4 after BR2 | the runner needs the route's request and response, not the route |
| BR12 after BR2 | the documents quote names, not code |
| BR13 after BR4 | the script reads names, not the runner |
| BR14 after BR1 | the reaper needs the marker's name, not the code that writes it |

**Edges kept:**

| Edge | Why a contract would not do |
| --- | --- |
| BR1, BR8, BR10, BR11 after BRS | one file: each owns a helper BRS creates |
| BR9 after BRS | the one name BRS defines; a contract for one constant would cost more than the edge |
| BR3 and BR5 after BR0 | data: the owner's measurement decides which fact is the link and which permission reads it (R4, O1) |
| BR10 after BR8 | behaviour: a project job's environment is what BR8 builds |
| BR11 after BR7 | one file, `doctor_batch.go` |
| BR13 after BR0 | two files, the checklist and the scripts' list |
| I1 after all | it wires the real sides and proves them together |

**The gain**, as `fugaro plan graph` reports it: 9 edges broken, 10 kept, 4 added; the critical
path was 7 tasks of weight 17 and is 4 tasks of weight 7. **The critical path** is BRS, BR8, BR10,
I1, and BRS is the bottleneck. By the calendar the longest wait is not a task: it is the owner's
run of Check 810 between BR0 and BR3.

**Shared files.** No level holds two writers of one file.

| File | Writers (level) |
| --- | --- |
| `internal/backend/batch/run.go` | BRS (0), BR10 (2) |
| `internal/backend/batch/batch.go` | BRS (0) |
| `internal/backend/batch/seal.go` | BRS (0), BR1 (1) |
| `internal/backend/batch/env.go` | BRS (0), BR8 (1) |
| `internal/backend/batch/account.go` | BRS (0), BR10 (2) |
| `internal/backend/batch/secrets.go` | BRS (0), BR11 (2) |
| `internal/credsvc/server.go` | BR2 (1), I1 (3) |
| `internal/credsvc/sealkey_stub.go` | C1 (0), I1 (3, deletes) |
| `internal/gcpfake/batch.go` | BR3 (1) |
| `internal/backend/batch/reap.go` | BR14 (1) |
| `internal/cli/docs_budget_token_channel_test.go` | BRS (0), BR1 (1) |
| `docs/design/budget-token-channel.md`, `docs/design/compute-backend.md` | BR1 (1) |
| `.claude/rules/credentials-service.md` | BR5 (1) |
| `internal/infra/spec.go` | BR8 (1) |
| `scripts/live_checks_test.go` | BR0 (0), BR13 (1) |
| `internal/settings/registry.go`, `schemas/settings.schema.json` | C1 (0) |
| `internal/cli/doctor_batch.go` | BR7 (1), BR11 (2) |
| `internal/cli/backends_use_test.go`, `internal/cli/validate_batch.go` | BR7 (1) |
| `internal/cli/run.go` | BR11 (2) |
| `internal/cli/cloud.go` | I1 (3) |
| `internal/infra/compute.go`, `internal/cli/init_compute.go`, `init_stages.go`, `update.go` | BR6 (1) |
| `internal/infra/tf/cover.go`, the compute module and root | BR5 (1) |
| `internal/localcfg/localcfg.go` | C1 (0) |
| `internal/backend/deployedjob.go` | BRS (0) |
| `internal/credsvc/sealkey_wire.go` | C1 (0) |
| `docs/gcp-live-checklist.md` | BR0 (0), BR13 (1) |
| `docs/backends.md`, `SECURITY.md` | BR12 (1) |

**Files another plan also writes.** `internal/backend/batch/run.go`,
`internal/cli/doctor_batch.go`, `internal/cli/validate_batch.go`, `docs/backends.md` and
`docs/design/budget-token-channel.md` are AB9's in always-a-budget; `internal/credsvc/server.go`
and `internal/credsvc/audit.go` are touched by keys-through-service. A launcher checks for an
open pull request on each before starting BRS, BR1, BR2, BR7 and BR12.

## 6. The estimate

18 runs. At the repository's own caps the worst case is 18 times one run's cap, plus 18
independent reviews. No run is proposed for launch here: nothing starts until the owner approves
tasks by name.

## 7. Contract changes anticipated

- **`R-C1-link-permission`.** Once Check 810 has named the link's read and the owner has
  confirmed O1, it writes that permission into the contract. It reaches BR3 and BR5.
- **`R-C1-project-job-account`.** If `LaunchSpec` cannot name the project job's account (BR10), it
  records the field in the contract document. It reaches BR10.

Neither runs unless raised; they are not counted in the 18.

## 8. Checks the plan takes

From the reserved block 810 to 819:

| Check | Measures | Who | When |
| --- | --- | --- | --- |
| 810 | What links a Batch VM to its job and which permission reads it, whether a launcher can forge it, what the identity token carries, where Batch writes variables, how soon a job's name can be reused (U1, U2, U3, U8) | the owner | after BR0 merges, before BR3 and BR5 start |
| 811 | The brief's item 6: launch, session, metering, and a second launcher failing three ways (R11) | the owner | after the release, on an updated `fugaro-dev` |

812 to 819 are reserved and unused. Check 124 stays as it is; 811 does not replace its preemption
and cost parts.

## 9. After-deploy steps

One, written in BR6's own pull request under `### For operators`: run `fugaro update`. It applies
the compute root where there is a `batch:` block, creates the key, and rewrites the block. It
asks nothing. The key ring it creates cannot be deleted from a Google Cloud project, which the
note says.

## 10. Considered and declined

- **Removing the Batch backend**, carrier only, and parking (D3060's other options).
- **K1, `encryptedVariables`**, and **K2, a per-run secret with an expire time** (D3061): a second
  launcher replays either (F4).
- **A separate release service** (D3062).
- **Batch for per-repository jobs only** (D3063), and **mounts on a Batch job** (D3064).
- **Falling back to K1 if Check 810 fails** (R4): the work returns to the owner instead.
- **Letting a queued run fail at start on a dead token**, and leaving it open (D3066).
- **Reversing D3063 to avoid the `actAs` widening** (D3065).
- **A grant on the key for launchers**: the public key reaches them through the block (R13).
- **A per-run service account**: what phase 3 removed, with an account to delete per run.
- **Binding the blob to the job's unique id**: Google assigns it at creation, after the blob is
  wrapped; the creation-time bound in R1 does its work.
