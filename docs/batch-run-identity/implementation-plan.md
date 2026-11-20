# A run on Cloud Batch gets its identity: implementation plan

**Status: plan only. Nothing is implemented. The owner approved C1, BRS, BR0 and BR6 by name on
2026-11-20, and confirmed BR6 after it changed; none is launched, and every other task needs his explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it, all of 2026-11-20 (D3100 to D3104 as recommended): the backend is finished, not
removed (D3100); the carrier is the VM-attested release (D3101); the release lives in the
credentials service (D3102); Batch learns shared project jobs (D3103); a Batch run's credentials
come from the credentials service alone (D3104). Review round 1 added two: launchers get `actAs`
on shared project job accounts where Batch is provisioned (D3105), and the reaper ends a sealed
run that queues past its token's life (D3106). D3101 was asked again with the fact that a
launcher can already mint (F24), and stands. D3102 was amended after that review. Review round 2 added two more: D3104 supersedes the owner's earlier D2075
(D3107), and the launcher registers the job so that a re-created job receives nothing (D3108).
D3102's second read is decided after Check 830. After the pull request was opened the owner
answered the two questions round 3 left: the registration is for people on the launcher roster
only (D3109), and `fugaro update` applies the compute root **only where Batch is selected**
(D3110, not the recommendation). The review he then asked for showed that a per-machine trigger
cannot work (F32); asked again, he chose a published opt-in fact, which keeps his intent. He
then asked for a way to opt out (D3111, not the recommendation), and confirmed BR6's approval
as the task now reads.

It is 26 runs: one contract, one contract change that always runs, 23 tasks and one integration. Every one is a security path.

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
| A blob for one job releases nothing to another job's VM, to a VM whose job has ended, to a job created outside the bound, to a job re-created under the same name, to a VM outside the installation's project, or to a VM that is not a Batch job's (R1, D3108, O8) | BR2, BR3, BR17 |
| Only the launcher identity that registered a blob's digest may name its job, and no token holder can write the registration (D3108) | BR17 |
| The route admits the per-repository and shared job classes only (R1) | BR2 |
| The release route writes exactly one audit line per refusal once the caller is verified, and none carries the key or the blob | BR2 |
| A job carrying both the plain marker and the wrapped one stops the run (R5) | BR4 |
| The service unreachable stops the run after a bounded retry; it never proceeds without a session (F23) | BR4 |
| Where the block names no key, the launch is refused with `fugaro update` as its fix; no launch reads the cloud to decide (R3) | BR1, BR7 |
| `fugaro update` on an installation at the previous version converges, with a Batch run in flight keeping everything it holds (R9) | BR6, I1 |
| The reaper's role gains nothing. Launchers gain `actAs` on shared project job accounts where Batch is provisioned (D3105) and nothing on the key | BR5, BR6 |
| The credentials service account gains, only where Batch is provisioned: decrypt on one key, `batch.jobs.get` conditioned on the Fugaro job-name prefix, and the one read Check 830 names for the link (D3102 as amended, O1) | BR5 |
| The block's key fact is written by `fugaro update` alone, and a machine whose fact differs from the published one fails doctor (R13) | BR6, BR7 |
| `fugaro update` touches the compute root only where the published block carries the opt-in fact, whatever the updating machine selects; a block alone gets no key ring; a non-operator's update skips the stage with one line (D3110 as amended, R2) | BR6 |
| Publishing a block never drops the key fact or the opt-in fact, and a withdrawal is a written value (R15) | BR19 |
| An opt-out is refused while a Batch job is queued or running; after it the update removes the grants, keeps the key, and says so in one line (D3111, R16) | BR20, BR21 |
| Selecting the backend is not refused for any check only the update can satisfy, and every launch is refused with `fugaro update` as the fix until it has run (R14) | BR7, BR18, BR1 |
| A caller outside the launcher class cannot register a job, and such a launch on Batch is refused with its reason before anything is created (D3109) | BR17, BR1 |
| A sealed run does not wait in the queue past its token's life less the reaper's interval; one that still starts late stops at its session (D3106, F31) | BR14 |
| No CLI release is cut from `main` between the merge of BR1, BR6, BR16 or BR18 and I1 | the launcher of the plan |
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
  inside the encrypted plaintext; the classes the route admits; the block's opt-in fact with its three states (D3110, D3111); the registration's two calls, its fields on
  the run's budget record and the `Registry` interface the release reads (D3108); the link's permission, stated as "named by Check 830" and not guessed; the settings
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
- Commit: `batch: the key-release contract (C1, D3101, R1, R5)`.
- Reviewer tier: opus, mandatory independent review (`fugaro review --pr N`) before any side is
  written against it.

No other task's Files names a file of C1. A task that finds the contract wrong raises a contract change (section 7).

## 4. Slices and tasks

**Slice 0, inert:** C1, BRS, BR0. Nothing a user can meet changes.
**Slice 1, the sides:** BR1 to BR9, BR11 to BR14, BR16, BR17. They are not all inert: BR6 and
BR16 change what `fugaro update` applies, and BR1 with BR16 turns the refusal into a launch that
cannot yet be released. So no CLI release is cut from `main` between the merge of BR1, BR6, BR16
or BR18 and I1.
**Slice 2:** BR10, BR15, BR18, BR19, BR20, BR21.
**Slice 3:** I1. After it, an updated installation on Batch launches.

### BRS: the job definition, split along its seams (`job-environment`)
- Acceptance: `jobDefinition` builds byte for byte the job it builds today, from four helpers in
  four files, `Options` carries the fields the sides will fill (unused, zero), and the
  deployed-job variable has its one name (R6).
- Files: `internal/backend/batch/run.go`, `internal/backend/batch/batch.go`,
  `internal/backend/batch/env.go` (new), `internal/backend/batch/seal.go` (new),
  `internal/backend/batch/account.go` (new), `internal/backend/batch/secrets.go` (new),
  `internal/backend/deployedjob.go` (new), `internal/cli/docs_budget_token_channel_test.go` (it
  reads the refusal's text out of `run.go`, which moves to `seal.go`),
  `deploy/terraform/secret_readers_test.go` (its row E5 is keyed on the file that holds
  `SecretVariables`, which moves to `secrets.go`). Past five; four are moves.
- Interfaces: none consumed; produces the four helpers BR1, BR8, BR10 and BR11 each own one of,
  and the variable's name BR8 writes and BR9 reads.
- Tests: `TestTheSplitBuildsTheSameJob`, plus the package's existing job tests unchanged.
- Command: `go test ./internal/backend/batch/ -run 'TheSplitBuildsTheSameJob|JobDefinition|Launch' -count=1 && go test ./deploy/terraform/ -run SecretReaders -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the golden job differs when a helper is emptied.
- Depends on: none. If AB9 of always-a-budget has an open pull request, BRS starts after it merges
  (R8: both edit `run.go`).
- Commit: `batch: the job definition in four helpers, no behaviour change (BRS)`.
- Reviewer tier: opus, mandatory independent review.

### BR0: Check 830, the probe (`live-proof`)
- Acceptance: one script measures, on a throwaway Batch job in `fugaro-dev`: what the identity
  token in full format carries inside the task's container (U2); which Google-made fact links the
  VM to its job, and whether a second job can forge it through labels or any job field (U1);
  whether a marker variable's value appears in Cloud Logging, instance metadata or the `CreateJob`
  audit record (U3); how soon a deleted job's name can be used again (U8). It prints one `PASS` or `FAIL` per fact and deletes the job it made.
- Files: `scripts/live-checks/check-830.sh` (new), `scripts/live-checks/check-830_test.sh` (new),
  `scripts/live_checks_test.go` (its list of scripts), `docs/gcp-live-checklist.md`.
- Tests: the self-test, against stubbed `gcloud`.
- Command: `bash scripts/live-checks/check-830_test.sh && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the self-test fails when the script omits the forge attempt.
- Depends on: none.
- Commit: `checks: Check 830, what links a Batch VM to its job (BR0, U1, U2, U3)`.
- Reviewer tier: opus, mandatory independent review.
- Owner-run: `CHK830_OWNER_RUN=yes bash scripts/live-checks/check-830.sh`, then paste the result
  block into the checklist. No agent performs any step of it. **BR3 waits on this result (R4).**

### BR1: the backend wraps, or refuses (`key-release`)
- Acceptance: a sealed launch on an installation whose block names the key produces a job whose
  only trace of the key is the wrapped variable, bound to that job's name; where the block names
  no key the launch is refused before anything is created, with `fugaro update` as the fix; the
  plain marker is never a job variable; before the create it registers the blob's digest with the
  service and after it the job's unique id, and a launch whose first call fails, or whose caller
  is refused by the service, creates nothing and names the launcher roster as the likely reason
  and how a person joins it (D3108, D3109, O15); the blob prints redacted and is in no run record (R7);
  the two design documents' pinned sentence and the test that holds it say what the new refusal
  says (F28).
- Files: `internal/budget/token/wrap.go` (new), `internal/budget/token/wrap_test.go` (new),
  `internal/backend/batch/seal.go`, `internal/backend/batch/seal_test.go` (new),
  `internal/backend/batch/register.go` (new), `internal/cli/docs_budget_token_channel_test.go`, `docs/design/budget-token-channel.md`,
  `docs/design/compute-backend.md`. Past five; the last three are one sentence each.
- Interfaces: consumes C1 (envelope, names) and BRS (`seal.go`).
- Tests: `TestASealedBatchLaunchCarriesOnlyTheWrappedKey`, `TestAWrappedKeyNamesItsJob`,
  `TestABatchLaunchWithoutTheKeyFactIsRefusedWithItsFix`, `TestTheRefusalNeverQuotesTheKey`,
  `TestTheWrappedBlobIsRedactedAndNotRecorded`, `TestTheJobIsRegisteredBeforeAndAfterTheCreate`,
  `TestAFailedRegistrationCreatesNothing`, `TestARefusedRegistrationNamesTheRoster`.
- Command: `go test ./internal/budget/token/ -run Wrap -count=1 && go test ./internal/backend/batch/ -run 'SealedBatchLaunch|WrappedKeyNamesItsJob|WithoutTheKeyFact|NeverQuotesTheKey|WrappedBlobIsRedacted|JobIsRegistered|FailedRegistrationCreatesNothing|RefusedRegistrationNamesTheRoster' -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the plain key appears among the job's variables.
- Depends on: C1 (the wire); BRS (kept: one file). `docs/design/budget-token-channel.md` is also
  AB9's file: BR1 starts after AB9 merges if AB9 has an open pull request.
- Commit: `batch: a sealed launch carries the key wrapped, or is refused with its fix (BR1, D3101, R3)`.
- Reviewer tier: opus, mandatory independent review.

### BR2: the release route (`key-release`)
- Acceptance: the route releases the seal key to a verified job account whose VM `JobLink` accepts
  for the job the blob names, and to nobody else: only the per-repository and shared job classes
  are admitted; the token's project must be the installation's and its zone in the installation's
  region; the job's unique id must be the one registered for the blob (read through C1's
  `Registry`, a stub until I1); the creation-time bound allows a stated clock tolerance. Each
  refusal after the caller is verified writes
  exactly one audit line, naming the field and never its content.
- Files: `internal/credsvc/sealkey.go` (new), `internal/credsvc/sealkey_test.go` (new),
  `internal/credsvc/kmsopen.go` (new), `internal/credsvc/server.go`, `internal/credsvc/audit.go`,
  `internal/gcpfake/kms.go` (new).
- Interfaces: consumes C1; uses the stub `JobLink` until I1.
- Tests: `TestTheKeyIsReleasedToTheJobsOwnVM`, `TestABlobForAnotherJobIsRefused`,
  `TestAnAccountOutsideTheJobTableIsRefused`, `TestAStaleBlobIsRefused`,
  `TestTheReviewAndPersonClassesAreRefused`, `TestAVMOutsideTheProjectIsRefused`,
  `TestAnUnregisteredJobIsRefused`, `TestTheBoundAllowsTheStatedClockTolerance`,
  `TestEachReleaseRefusalWritesOneAuditLine`, `TestNoAuditLineCarriesTheKeyOrTheBlob`.
- Command: `go test ./internal/credsvc/ -run 'KeyIsReleased|BlobForAnotherJob|OutsideTheJobTable|StaleBlob|ReviewAndPersonClasses|VMOutsideTheProject|UnregisteredJobIsRefused|BoundAllowsTheStatedClock|ReleaseRefusalWritesOne|CarriesTheKeyOrTheBlob' -count=1`.
- Red step: a blob for another job is released.
- Depends on: C1.
- Commit: `credsvc: the seal key is released to the run's own VM (BR2, D3101, D3102)`.
- Reviewer tier: opus, mandatory independent review.

### BR3: the link from a VM to its job (`key-release`)
- Acceptance: `JobLink` accepts a VM only on the fact Check 830 recorded, for a job that exists, is
  not finished and was created within the bound; it refuses a VM of another job, a VM whose
  launcher-set labels claim the job, and a plain Compute Engine VM that is no Batch job's (O8).
- Files: `internal/credsvc/joblink.go` (new), `internal/credsvc/joblink_test.go` (new),
  `internal/gcpfake/batch.go`, `internal/gcpfake/compute.go`.
- Interfaces: consumes C1 and the recorded result of Check 830.
- Tests: `TestTheLinkAcceptsTheJobsOwnVM`, `TestALabelIsNotALink`,
  `TestAPlainVMIsRefused`, `TestAFinishedJobIsRefused`.
- Command: `go test ./internal/credsvc/ -run 'LinkAccepts|LabelIsNotALink|PlainVMIsRefused|FinishedJob' -count=1`.
- Red step: a VM carrying only a forged label is accepted.
- Depends on: C1; `R-C1-link-permission` (kept, data: it carries the owner's result of Check 830
  and his answer on the second read, R4, O1).
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
  and the one read Check 830 named for the link; launchers get nothing on the key; the reaper's
  role is unchanged; one input turns the grants off and leaves the key and its ring planned as
  they are (R16); `.claude/rules/credentials-service.md` states the account's grants as they
  now are, in this same change.
- Files: `deploy/terraform/gcp/modules/compute/kms.tf` (new), `.../compute/apis.tf`,
  `.../compute/iam.tf`, `.../compute/outputs.tf`, `.../compute/variables.tf`,
  `deploy/terraform/gcp/roots/compute/main.tf`, `.../outputs.tf`, `.../variables.tf`,
  `.../tests/compute.tftest.hcl`, `internal/infra/tf/cover.go`, the golden plans (regenerated by
  `scripts/gen-golden-plans.sh`, never by hand), `deploy/terraform/terraform_test.go` (the
  custom-role snapshot pins the new role), `.claude/rules/credentials-service.md`. Past five; each root file is a pass-through line.
- Interfaces: takes the output and input names from C1's contract document, and the link's
  permission from the recorded result of Check 830.
- Tests: the root's `terraform test`; `TestTheComputeRootsNewTypesAreCovered`; the custom-role
  snapshot test.
- Command: `terraform fmt -check -recursive deploy/terraform && (cd deploy/terraform/gcp/roots/compute && TF_DATA_DIR=$(mktemp -d) terraform init -backend=false -lockfile=readonly && terraform validate && terraform test) && scripts/gen-golden-plans.sh && go test ./internal/infra/tf/ -run 'ComputeRootsNewTypes|Golden' -count=1 && go test -tags terraform ./internal/infra/... && go test ./deploy/terraform/ -run 'Role|Snapshot' -count=1 && tflint --chdir=deploy/terraform/gcp && sh deploy/terraform/scan.sh`.
- Red step: the tftest fails on a role holding a permission beyond the two named.
- Depends on: `R-C1-link-permission` (kept, data: it carries the owner's result of Check 830 and
  his answer on the second read, and it comes after C1, whose names this task uses).
- Commit: `terraform: the Batch seal key, and who may use it (BR5, D3102, R9)`.
- Reviewer tier: opus, mandatory independent review.

### BR6: update applies the compute root (`update-converge`)
- Acceptance: `fugaro update` applies the compute root where the published block carries the
  opt-in fact, whatever the updating machine's own `backend:` says, and leaves alone an
  installation nobody opted in; on a machine without operator rights it skips the stage with one
  line (D3110 as amended, R2);
  it asks nothing, is refused in an agent's session, and does not wait; an apply that changes
  only a key, a role or a grant starts no rebuild of the preload image, and one that changes the
  image's inputs says so in one line with the cost (R12); with a Batch run in flight it says so
  in one line and changes nothing the run holds.
- Files: `internal/cli/init_stages.go`, `internal/cli/init_compute.go`, `internal/cli/update.go`,
  `internal/apply/steps.go`, `internal/cli/update_batch_test.go` (new).
- Interfaces: consumes C1 (the opt-in fact).
- Tests: `TestUpdateAppliesTheComputeRootWhereTheInstallationOptedIn`,
  `TestUpdateLeavesAnInstallationNobodyOptedInAlone`, `TestTheUpdatingMachinesOwnBackendDoesNotDecide`,
  `TestANonOperatorsUpdateSkipsTheStage`, `TestTheUpdateSaysABatchRunKeepsRunning`,
  `TestTheComputeStageUnderUpdateAsksNothing`, `TestTheComputeStageUnderUpdateIsRefusedInAnAgentSession`,
  `TestAGrantOnlyApplyStartsNoPreloadRebuild`.
- Command: `go test ./internal/cli/ -run 'UpdateAppliesTheComputeRoot|NobodyOptedInAlone|OwnBackendDoesNotDecide|NonOperatorsUpdateSkips|ABatchRunKeepsRunning|ComputeStageUnderUpdate|GrantOnlyApplyStartsNoPreload|RefusalMatrix|Docs' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: an update on an opted-in installation leaves the compute root unapplied.
- Depends on: C1. This task was approved on 2026-11-20 as a task with no dependency that applied
  the root wherever a block exists; it changed (D3110), and the owner confirmed the approval for
  this text the same day.
- Commit: `update: the compute root is applied by the one command, where the installation opted in (BR6, R2, R12, D3110)`.
- Reviewer tier: opus, mandatory independent review.

### BR16: update writes the block (`update-converge`)
- Acceptance: after `fugaro update` the block carries the key fact, rewritten from the root's
  output on every run (R13); `batch.secrets` and the single `service_account` are gone from the
  file, in place; the root is fed the project job accounts beside the per-repository ones, which
  is what gives launchers `actAs` on them (D3105); the block is published, and a launcher who is
  not an operator reads the key fact from the published copy; the release notes' `### For
  operators` says "run `fugaro update`", and the feature notes say how Batch is chosen; where the root has no key output
  yet (BR5 not applied), no key fact is written and the launch refusal stays.
- Files: `internal/cli/init_compute.go`, `internal/cli/init_compute_block_test.go`,
  `internal/infra/compute.go`, `internal/infra/compute_test.go`, the next release's notes under
  `docs/releases/`.
- Interfaces: consumes C1 (the block's key fact, the root's names).
- Tests: `TestUpdateRecordsTheSealKeyInTheBlock`, `TestUpdateRewritesAKeyFactThatDiffers`,
  `TestUpdateRemovesBatchSecretsInPlace`, `TestTheRootIsFedTheProjectJobAccounts`,
  `TestALauncherReadsTheKeyFactFromThePublishedBlock`, `TestNoKeyOutputWritesNoKeyFact`.
- Command: `go test ./internal/cli/ -run 'UpdateRecordsTheSealKey|RewritesAKeyFactThatDiffers|UpdateRemovesBatchSecrets|KeyFactFromThePublishedBlock|NoKeyOutputWritesNoKeyFact' -count=1 && go test ./internal/infra/ -run RootIsFedTheProjectJobAccounts -count=1`.
- Red step: after an update the block names no key.
- Depends on: C1; BR6 (kept: one file, `init_compute.go`).
- Commit: `update: the seal key and the accounts reach the block (BR16, R10, R13, D3105)`.
- Reviewer tier: opus, mandatory independent review.

### BR7: doctor and validate say where the carrier stands (`update-converge`)
- Acceptance: on an installation that mints, the seal check passes where the block names the key
  and fails with `fugaro update` as its fix where it does not; `validate` agrees offline; a
  machine whose key fact differs from the published one fails (R13), under an id of its own,
  distinct from the missing key's; each Batch check whose fix is the compute root under `fugaro
  update` carries a mark saying so, which BR18 reads (R14).
- Files: `internal/cli/doctor_batch.go`, `internal/cli/doctor_batch_test.go`,
  `internal/cli/validate_batch.go`, `internal/cli/validate_batch_test.go`.
- Interfaces: consumes C1 (the block's key fact).
- Tests: `TestDoctorPassesAMintingBatchInstallationWithTheKey`,
  `TestDoctorNamesTheUpdateWhereTheKeyIsMissing`, `TestValidateAgreesWithDoctorOnTheSeal`,
  `TestDoctorFailsAKeyFactThatDiffersFromThePublishedOne`, `TestAMissingKeyAndADifferingKeyAreTwoFailures`,
  `TestTheChecksOnlyTheUpdateFixesAreMarked`.
- Command: `go test ./internal/cli/ -run 'MintingBatchInstallationWithTheKey|NamesTheUpdateWhereTheKeyIsMissing|ValidateAgreesWithDoctorOnTheSeal|KeyFactThatDiffers|MissingKeyAndADifferingKey|OnlyTheUpdateFixesAreMarked' -count=1`.
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
- Commit: `batch: a project job's workflow runs on Batch under that job's account (BR10, D3103)`.
- Reviewer tier: opus, mandatory independent review.

### BR11: nothing is mounted on a Batch job (`service-credentials`)
- Acceptance: a Batch job carries no `secretVariables`; a launch on Batch for a repository the
  credentials service does not serve is refused before the mint, with its fix; row E5 leaves the
  secret-readers table and its document together, saying D3107 supersedes D2075.
- Files: `deploy/terraform/secret_readers_test.go`, `docs/secrets-brokering/secret-readers.md`,
  `internal/backend/batch/secrets.go`, `internal/backend/batch/secrets_test.go` (new),
  `internal/cli/launch_batch.go` (new), `internal/cli/launch_batch_test.go` (new),
  `internal/cli/run.go`, `internal/cli/cloud_openers_test.go`. Past five; the first two are one
  row.
- Interfaces: consumes BRS (`secrets.go`).
- Tests: `TestABatchJobMountsNothing`, `TestAnUnservedRepositoryIsRefusedOnBatchWithItsFix`,
  `TestTheRefusalComesBeforeTheMint`.
- Command: `go test ./internal/backend/batch/ -run BatchJobMountsNothing -count=1 && go test ./internal/cli/ -run 'UnservedRepositoryIsRefusedOnBatch|RefusalComesBeforeTheMint|Openers|Docs' -count=1 && go test ./deploy/terraform/ -run SecretReaders -count=1`.
- Red step: a job with `batch.secrets` set still carries `secretVariables`.
- Depends on: BRS (kept: one file).
- Commit: `batch: a run's credentials come from the service alone (BR11, D3104, D3107, R10)`.
- Reviewer tier: opus, mandatory independent review.

### BR12: the documents (`live-proof`)
- Acceptance: `SECURITY.md` says who can and cannot obtain a Batch run's seal key and names the
  service's new grant; the website page is regenerated; `docs/backends.md` says how to reach a
  launching Batch installation; `SECURITY.md` also says that launchers hold `actAs` on shared
  project job accounts where Batch is provisioned (D3105) and that a launcher can mint (F24); its
  sentence that launchers hold the Batch editor role "on the project" is corrected to the
  job-name condition the grant carries; its sentence on the service account's only other grant
  names the grants as they now are.
- Files: `SECURITY.md`, `website/content/security.md` (generated), `docs/backends.md`,
  `internal/cli/docs_security_test.go`.
- Tests: the Docs tests; `website`'s `security.mjs`.
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`.
- Red step: the website's security test fails until the page is regenerated.
- Depends on: C1; `R-C1-link-permission` (kept, data: the page names the service's grants as
  the owner settled them). `docs/backends.md` is also AB9's file: BR12 starts after AB9 merges if
  AB9 has an open pull request.
- Commit: `docs: a Batch run's identity, and who can reach its key (BR12, D3101, D3102, D3105)`.
- Reviewer tier: opus, mandatory independent review.

### BR13: Check 831, the proof (`live-proof`)
- Acceptance: one script launches a sandbox run on Batch, reads that it opened its session and
  was metered, and then, as a second launcher account, fails to obtain the first run's seal key
  four ways: reading the job, replaying the blob into a job of its own, calling the release
  route, and deleting a queued job and re-creating it under the same name (D3108). It ends with the cost and with nothing left to delete.
- Files: `scripts/live-checks/check-831.sh` (new), `scripts/live-checks/check-831_test.sh` (new),
  `scripts/live_checks_test.go` (its list of scripts), `docs/gcp-live-checklist.md`.
- Tests: the self-test.
- Command: `bash scripts/live-checks/check-831_test.sh && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the self-test fails when the replay step is removed.
- Depends on: C1; BR0 (kept: two files, the checklist and the scripts' list).
- Commit: `checks: Check 831, a Batch run's identity end to end (BR13, R11)`.
- Reviewer tier: opus, mandatory independent review.
- Owner-run, after the release and `fugaro update` on `fugaro-dev`:
  `CHK831_OWNER_RUN=yes bash scripts/live-checks/check-831.sh --second-launcher <account>`. No
  agent performs any step of it.

### BR14: the reaper ends a sealed run at its token's life (`job-environment`)
- Acceptance: a job that carries the wrapped marker and has been queued for the token's lifetime
  less the reaper's own interval and a stated start margin is deleted by the reaper, reported
  with that reason, and reads as ended in the queue; the boundary is tested at one tick either
  side (F31); a job with no marker keeps today's queue deadline; nothing else the reaper deletes
  changes.
- Files: `internal/backend/batch/reap.go`, `internal/backend/batch/reap_sealed_test.go` (new).
- Interfaces: consumes C1 (the marker's name) and `token.Lifetime`.
- Tests: `TestASealedJobQueuedPastItsTokenIsDeleted`, `TestTheSealedDeadlineLeavesATickAndAMargin`,
  `TestAnUnsealedJobKeepsTheQueueDeadline`,
  `TestTheReaperNeverReadsTheBlobsValue`.
- Command: `go test ./internal/backend/batch/ -run 'SealedJobQueuedPastItsToken|SealedDeadlineLeavesATick|UnsealedJobKeepsTheQueueDeadline|NeverReadsTheBlobsValue|Reap' -count=1`.
- Red step: a sealed job queued for two hours is left to start.
- Depends on: C1.
- Commit: `reaper: a sealed run does not outlive its token in the queue (BR14, D3106)`.
- Reviewer tier: opus, mandatory independent review.

### BR15: doctor and the readers' table follow (`service-credentials`)
- Acceptance: doctor's secret check for the block and its Batch row in the secret-readers check
  are gone, and with them the readers' table's row for that check (E23); `SECURITY.md` says that
  a Batch run mounts nothing and that D3107 supersedes D2075; the tests that pin those sentences
  agree.
- Files: `internal/cli/doctor_batch.go`, `internal/cli/doctor_batch_test.go`,
  `internal/cli/doctor_secret_readers.go`, `deploy/terraform/secret_readers_test.go`,
  `docs/secrets-brokering/secret-readers.md`, `docs/secrets-brokering/reconciliation.md` (D2075's
  cell, marked superseded), `SECURITY.md`, `website/content/security.md` (generated),
  `internal/cli/docs_security_test.go`. Past five; five are one sentence or one row each.
- Tests: `TestDoctorHasNoBatchSecretsCheck`, the Docs tests.
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run 'DoctorHasNoBatchSecretsCheck|DoctorBatch|SecretReaders|Docs' -count=1 && go test ./deploy/terraform/ -run SecretReaders -count=1 && (cd website && npm run check)`.
- Red step: the readers' table test fails on the row whose check is gone.
- Depends on: BR7 (kept: one file, `doctor_batch.go`); BR12 (kept: one file, `SECURITY.md`).
- Commit: `doctor, docs: a Batch run mounts nothing; D2075 is superseded (BR15, D3107)`.
- Reviewer tier: opus, mandatory independent review.

### BR18: selecting the backend does not wait for the key (`update-converge`)
- Acceptance: `fugaro backends use cloud-batch` is refused for every failing Batch check except
  those BR7 marks as fixed only by the update; with only those failing it selects, an operator's
  selection writes and publishes the opt-in fact, and it prints one line saying that `fugaro
  update` is the next step and that launches are refused until it has run; a person who is not an
  operator selects locally and is told an operator must opt the installation in; a key fact that
  differs from the published one still refuses the selection; `docs/backends.md` says the order
  (R2, R14).
- Files: `internal/cli/backends.go`, `internal/cli/backends_use_test.go`, `docs/backends.md`.
- Interfaces: consumes BR7's check, which says which failure it is.
- Tests: `TestBackendsUseSelectsWhenOnlyTheKeyIsMissing`, `TestBackendsUseSelectsOnASharedJobsInstallationBeforeTheApply`,
  `TestAnOperatorsSelectionPublishesTheOptIn`, `TestANonOperatorsSelectionDoesNotOptIn`,
  `TestBackendsUseNamesTheUpdateAsTheNextStep`, `TestBackendsUseStillRefusesADifferingKeyFact`.
- Command: `go test ./internal/cli/ -run 'BackendsUseSelects|OperatorsSelection|BackendsUseNamesTheUpdate|BackendsUseStillRefusesADiffering|BackendsUse|Docs' -count=1`.
- Red step: on an installation with shared jobs and no apply yet, the selection is refused.
- Depends on: BR7 (kept, behaviour: it reads which failure the check reports); BR12 (kept: one
  file, `docs/backends.md`).
- Commit: `backends: selecting Batch does not wait for the key (BR18, D3110, R14)`.
- Reviewer tier: opus, mandatory independent review.

### BR19: publishing never drops the key or the opt-in (`update-converge`)
- Acceptance: when a machine publishes a `batch:` block that lacks the key fact or the opt-in
  fact, the published copy's values for those two fields are kept; a withdrawn opt-in is a
  value of its own and is kept the same way; every other field merges as it does today (R15).
- Files: `internal/localcfg/shared.go`, `internal/localcfg/shared_test.go`.
- Interfaces: consumes C1 (the two fields).
- Tests: `TestPublishingKeepsThePublishedKeyFact`, `TestPublishingKeepsTheOptIn`,
  `TestPublishingKeepsAWithdrawal`,
  `TestEveryOtherBatchFieldMergesAsBefore`.
- Command: `go test ./internal/localcfg/ -run 'PublishingKeeps|EveryOtherBatchFieldMerges|Shared' -count=1`.
- Red step: a machine with no key fact publishes and the published block loses it.
- Depends on: C1.
- Commit: `localcfg: publishing a batch block keeps the key and the opt-in (BR19, R15)`.
- Reviewer tier: opus, mandatory independent review.

### BR20: an operator opts out (`update-converge`)
- Acceptance: an explicit command withdraws the opt-in fact and publishes it; merely selecting
  `cloud-run` on a machine does not; it asks nothing; it is refused with the reason while a Batch
  job of the installation is queued or running, and for a person who is not an operator; after
  it, a launch on Batch is refused with opting in again as the fix; `docs/backends.md` and the
  CLI reference say so (R16).
- Files: `internal/cli/backends.go`, `internal/cli/backends_optout_test.go` (new),
  `docs/backends.md`, `website/content/cli-reference.md` (generated), the plugin skill that names
  the `backends` commands if the plugin's tests require it.
- Interfaces: consumes C1 (the fact's three states).
- Tests: `TestAnOperatorOptsOut`, `TestSelectingCloudRunDoesNotOptOut`,
  `TestOptOutIsRefusedWhileABatchJobIsLive`, `TestANonOperatorCannotOptOut`,
  `TestALaunchAfterOptOutNamesOptingIn`.
- Command: `go run ./internal/clidoc && go test ./internal/cli/ -run 'OperatorOptsOut|SelectingCloudRunDoesNotOptOut|OptOutIsRefusedWhile|NonOperatorCannotOptOut|LaunchAfterOptOut|CliReference|Docs' -count=1 && go test ./plugin/... -count=1`.
- Red step: selecting `cloud-run` withdraws the fact.
- Depends on: BR18 (kept: two files, `backends.go` and `docs/backends.md`).
- Commit: `backends: an operator can opt the installation out of Batch (BR20, D3111, R16)`.
- Reviewer tier: opus, mandatory independent review.

### BR21: update retires the grants after an opt-out (`update-converge`)
- Acceptance: on an installation whose published fact is withdrawn, `fugaro update` applies the
  compute root with the grants off, so the credentials service's role and decrypt and the
  launchers' `actAs` on the shared project job accounts are gone, and the key, its ring and the
  provisioning are untouched; it prints one line naming what it removes; opting in again
  restores them at the next update under the same key; an installation at the previous version
  that opted out converges (R16, R9).
- Files: `internal/cli/init_compute.go`, `internal/cli/update_batch_optout_test.go` (new),
  `internal/apply/steps.go`.
- Interfaces: consumes C1 (the fact's states) and BR5's input that turns the grants off.
- Tests: `TestUpdateRemovesTheGrantsAfterAnOptOut`, `TestTheKeySurvivesAnOptOut`,
  `TestOptingInAgainRestoresTheGrantsUnderTheSameKey`, `TestTheUpdateNamesWhatAnOptOutRemoves`.
- Command: `go test ./internal/cli/ -run 'RemovesTheGrantsAfterAnOptOut|KeySurvivesAnOptOut|OptingInAgainRestores|NamesWhatAnOptOutRemoves|RefusalMatrix|Docs' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: after an opt-out and an update the service still holds decrypt on the key.
- Depends on: BR16 (kept: one file, `init_compute.go`); BR5 (kept, behaviour: it applies the
  root with the input BR5 adds).
- Commit: `update: an opted-out installation loses the Batch grants and keeps its key (BR21, D3111, R16)`.
- Reviewer tier: opus, mandatory independent review.

### BR17: the registration (`key-release`)
- Acceptance: a launcher's verified identity registers a blob's digest and job name as one entry
  on the run's budget record, and later that entry's job id; entries are one per digest, written
  once and never overwritten, and the job id is written once per entry; a retry's new digest adds
  an entry beside the old; a second identity cannot name the job of another's entry; an entry
  for one digest neither blocks nor opens another; only the launcher class is admitted and every
  other class is refused; no token holder can write an entry, by the database rules; `Registry`
  answers the release from them; each refusal writes one audit line. If the rules' version has
  to move, `fugaro update` carries it and a test shows an installation at the previous rules
  version converging.
- Files: `internal/credsvc/sealkey_register.go` (new), `internal/credsvc/sealkey_register_test.go`
  (new), `internal/credsvc/server.go`, `internal/budget/rules/rules.json.tmpl`,
  `internal/budget/rules/testdata/rules.golden.json`, the rules' own tests beside them.
- Interfaces: consumes C1 (the two calls, the fields, `Registry`).
- Tests: `TestTheWrappingLauncherRegistersTheJob`, `TestAnotherIdentityCannotNameTheJob`,
  `TestADigestRegistersOnce`, `TestAnEntryIsNeverOverwritten`, `TestARetryAddsAnEntry`,
  `TestOnlyTheLauncherClassRegisters`, `TestNoTokenHolderWritesTheRegistration`,
  `TestEachRegistrationRefusalWritesOneAuditLine`.
- Command: `go test ./internal/credsvc/ -run 'WrappingLauncherRegisters|AnotherIdentityCannotName|DigestRegistersOnce|EntryIsNeverOverwritten|RetryAddsAnEntry|OnlyTheLauncherClassRegisters|RegistrationRefusalWritesOne' -count=1 && go test ./internal/budget/... -run 'Rules' -count=1 && go test -tags firebase ./internal/budget/rules/ -run NoTokenHolderWritesTheRegistration -count=1` (the last needs the rules emulator the `firebase` tag's tests already use; no live project).
- Red step: a run's own token writes the job id field.
- Depends on: C1; BR2 (kept: one file, `server.go`).
- Commit: `credsvc: the launcher that wrapped a key names its job (BR17, D3108)`.
- Reviewer tier: opus, mandatory independent review.

### I1: integration (all)
- Acceptance: the stub is deleted and the real link, registry and key opener are wired into the
  service;
  `batchOptions` fills the backend from the block and the configuration; end to end and
  hermetically, a sealed launch on Batch reaches an open session, an installation at the previous
  version converges under `fugaro update`, a Batch run in flight across that update keeps running,
  and no resource was created per run.
- Files: `internal/credsvc/sealkey_stub.go` (deleted), `internal/credsvc/server.go`,
  `internal/cli/cloud.go`, `internal/cli/batch_identity_end_to_end_test.go` (new),
  `internal/cli/update_in_flight_batch_test.go` (new).
- Tests: `TestABatchRunOpensItsSessionEndToEnd`, `TestASecondJobCannotOpenTheFirstRunsKey`,
  `TestARecreatedJobCannotOpenTheFirstRunsKey`,
  `TestUpdateConvergesABatchInstallationAtThePreviousVersion`,
  `TestUpdateLeavesABatchRunInFlightRunning`, `TestNothingIsCreatedPerBatchRun`.
- Command: `go build ./... && go vet ./... && ! git grep -n 'sealkey_stub' -- '*.go' && go test ./internal/cli/ -run 'BatchRunOpensItsSession|SecondJobCannotOpen|RecreatedJobCannotOpen|ConvergesABatchInstallation|LeavesABatchRunInFlight|NothingIsCreatedPerBatchRun' -count=1`.
- Red step: the build fails on the deleted stub's last user.
- Depends on: C1, `R-C1-link-permission` and every task: BRS, BR0 to BR21.
- Commit: `batch: a run on Cloud Batch gets its identity, end to end (I1, D3100)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

**Levels.** Tasks in one level run at once.

| Level | Tasks |
| --- | --- |
| 0 | C1, BRS, BR0 |
| 1 | BR1, BR2, BR4, BR6, BR7, BR8, BR9, BR11, BR13, BR14, BR19; and `R-C1-link-permission` once the owner has run Check 830 and answered O1 |
| 2 | BR10, BR16, BR17; and BR3, BR5, BR12 after `R-C1-link-permission` |
| 3 | BR15, BR18, BR21 |
| 4 | BR20 |
| 5 | I1 |

**Edges broken by C1**, each because the dependent needs a shape and not a behaviour:

| Was | Why the contract is enough |
| --- | --- |
| BR16 after BR5 | the update needs the root's output and input names, not the resources |
| BR1 after BR16 | the backend needs the block's key fact, not the update that writes it |
| BR7 after BR16 | doctor needs the same fact |
| BR2 after BR1 | the service needs the blob's layout, not the code that writes it |
| BR3 after BR2 | the link implements an interface the route calls; it does not need the route |
| BR4 after BR2 | the runner needs the route's request and response, not the route |
| BR12 after BR2 | the documents quote names, not code |
| BR13 after BR4 | the script reads names, not the runner |
| BR14 after BR1 | the reaper needs the marker's name, not the code that writes it |
| BR17 after BR1 | the registration needs the digest's definition, not the wrapper |
| BR6 after BR5 | the update's stage needs the block's opt-in fact and the root's input names, not the root's resources |
| BR19 after BR16 | the merge needs the two fields' shape, not the update that writes them |

**Edges kept:**

| Edge | Why a contract would not do |
| --- | --- |
| BR1, BR8, BR10, BR11 after BRS | one file: each owns a helper BRS creates |
| BR9 after BRS | the one name BRS defines; a contract for one constant would cost more than the edge |
| `R-C1-link-permission` after BR0; BR3, BR5 and BR12 after it | data: the owner's measurement and his answer decide which fact is the link and which permission reads it (R4, O1) |
| BR16 after BR6 | one file, `init_compute.go` |
| BR21 after BR16 and BR5 | one file, `init_compute.go`; and behaviour: it applies the root with the input BR5 adds |
| BR20 after BR18 | two files, `backends.go` and `docs/backends.md` |
| BR17 after BR2 | one file, `server.go` |
| BR15 after BR7 and BR12 | one file each, `doctor_batch.go` and `SECURITY.md` |
| BR18 after BR7 and BR12 | behaviour: it reads which failure BR7's check reports; and one file, `docs/backends.md` |
| BR10 after BR8 | behaviour: a project job's environment is what BR8 builds |
| BR13 after BR0 | two files, the checklist and the scripts' list |
| I1 after all | it wires the real sides and proves them together |

**The gain**, as `fugaro plan graph` reports it: 12 edges broken, 16 kept, 4 added; the critical
path was 8 tasks of weight 17 and is 6 tasks of weight 9.5. **The critical path** is BR0,
`R-C1-link-permission`, BR12, BR18, BR20, I1, and BR0 is the bottleneck: between it and the
contract change stand the owner's run of Check 830 and his answer on the second read. Everything
that does not need that answer runs meanwhile.

**Shared files.** No level holds two writers of one file.

| File | Writers (level) |
| --- | --- |
| `internal/backend/batch/run.go` | BRS (0), BR10 (2) |
| `internal/backend/batch/batch.go` | BRS (0) |
| `internal/backend/batch/seal.go` | BRS (0), BR1 (1) |
| `internal/backend/batch/env.go` | BRS (0), BR8 (1) |
| `internal/backend/batch/account.go` | BRS (0), BR10 (2) |
| `internal/backend/batch/secrets.go` | BRS (0), BR11 (1) |
| `internal/credsvc/server.go` | BR2 (1), BR17 (2), I1 (5) |
| `internal/credsvc/audit.go`, `internal/gcpfake/kms.go` | BR2 (1) |
| `internal/budget/rules/rules.json.tmpl` | BR17 (2) |
| `internal/credsvc/sealkey_stub.go` | C1 (0), I1 (5, deletes) |
| `internal/gcpfake/batch.go`, `internal/gcpfake/compute.go` | BR3 (2) |
| `internal/backend/batch/reap.go` | BR14 (1) |
| `internal/cli/docs_budget_token_channel_test.go` | BRS (0), BR1 (1) |
| `deploy/terraform/secret_readers_test.go` | BRS (0), BR11 (1), BR15 (3) |
| `docs/secrets-brokering/secret-readers.md` | BR11 (1), BR15 (3) |
| `internal/cli/docs_security_test.go` | BR12 (2), BR15 (3) |
| `deploy/terraform/terraform_test.go` | BR5 (2) |
| `docs/design/budget-token-channel.md`, `docs/design/compute-backend.md` | BR1 (1) |
| `.claude/rules/credentials-service.md` | BR5 (2) |
| `internal/infra/spec.go`, `internal/backend/launchenv.go` | BR8 (1) |
| `scripts/live_checks_test.go` | BR0 (0), BR13 (1) |
| `internal/settings/registry.go`, `schemas/settings.schema.json` | C1 (0) |
| `internal/cli/doctor_batch.go`, `internal/cli/doctor_batch_test.go` | BR7 (1), BR15 (3) |
| `internal/cli/doctor_secret_readers.go`, `docs/secrets-brokering/reconciliation.md` | BR15 (3) |
| `internal/cli/validate_batch.go` | BR7 (1) |
| `internal/cli/backends.go` | BR18 (3), BR20 (4) |
| `internal/cli/backends_use_test.go` | BR18 (3) |
| `internal/cli/run.go`, `internal/cli/cloud_openers_test.go` | BR11 (1) |
| `internal/runner/budget_session.go`, `internal/gitops/gitops.go` | BR4 (1) |
| `internal/runner/projectjob.go` | BR9 (1) |
| `internal/cli/cloud.go` | I1 (5) |
| `internal/cli/init_compute.go` | BR6 (1), BR16 (2), BR21 (3) |
| `internal/apply/steps.go` | BR6 (1), BR21 (3) |
| `internal/cli/init_stages.go`, `internal/cli/update.go` | BR6 (1) |
| `internal/infra/compute.go`, the release notes | BR16 (2) |
| `internal/localcfg/shared.go`, `internal/localcfg/shared_test.go` | BR19 (1) |
| `internal/infra/tf/cover.go`, the compute module and root | BR5 (2) |
| `internal/localcfg/localcfg.go` | C1 (0) |
| `internal/backend/deployedjob.go` | BRS (0) |
| `internal/credsvc/sealkey_wire.go`, the contract document | C1 (0), `R-C1-link-permission` (1) |
| `docs/gcp-live-checklist.md` | BR0 (0), BR13 (1) |
| `docs/backends.md` | BR12 (2), BR18 (3), BR20 (4) |
| `SECURITY.md`, `website/content/security.md` | BR12 (2), BR15 (3) |

**Files another plan also writes.** `internal/backend/batch/run.go`,
`internal/cli/doctor_batch.go`, `internal/cli/validate_batch.go`, `docs/backends.md` and
`docs/design/budget-token-channel.md` are AB9's in always-a-budget, which is approved and
launching; `internal/credsvc/server.go`
and `internal/credsvc/audit.go` are touched by keys-through-service. A launcher checks for an
open pull request on each before starting BRS, BR1, BR2, BR7 and BR12.

## 6. The estimate

26 runs. At the repository's own caps the worst case is 26 times one run's cap, plus 26
independent reviews. No run is proposed for launch here: nothing starts until the owner approves
tasks by name.

## 7. Contract changes anticipated

- **`R-C1-link-permission`. It always runs**, and is counted in the 26. Once Check 830 has named
  the link's read and the owner has answered O1, it writes that read into the contract. BR3, BR5
  and BR12 wait on it. If the owner's answer is a release service of its own, it does not run:
  D3102 is reversed and this plan is re-sliced first.
- **`R-C1-project-job-account`.** If `LaunchSpec` cannot name the project job's account (BR10), it
  records the field in the contract document. It reaches BR10.

The second runs only if raised and is not counted.

## 8. Checks the plan takes

From the reserved block 830 to 839:

| Check | Measures | Who | When |
| --- | --- | --- | --- |
| 830 | What links a Batch VM to its job and which permission reads it, whether a launcher can forge it, what the identity token carries, where Batch writes variables, how soon a job's name can be reused (U1, U2, U3, U8) | the owner | after BR0 merges, before `R-C1-link-permission`, BR3, BR5 and BR12 start |
| 831 | The brief's item 6: launch, session, metering, and a second launcher failing four ways (R11, D3108) | the owner | after the release, on an updated `fugaro-dev` |

832 to 839 are reserved and unused. Check 124 stays as it is; 831 does not replace its preemption
and cost parts.

## 9. After-deploy steps

One, written in BR16's own pull request under `### For operators`: run `fugaro update`. It applies
the compute root where an operator has opted the installation in, creates the key, and rewrites
the block. Choosing Batch (`fugaro backends use cloud-batch`, at an operator's own terminal) is
an opt-in to a feature and is described in the feature notes, not as an upgrade step. It
asks nothing. The key ring it creates cannot be deleted from a Google Cloud project, which the
note says.

## 10. Considered and declined

- **Removing the Batch backend**, carrier only, and parking (D3100's other options).
- **K1, `encryptedVariables`**, and **K2, a per-run secret with an expire time** (D3101): a second
  launcher replays either (F4).
- **A separate release service** (D3102).
- **Batch for per-repository jobs only** (D3103), and **mounts on a Batch job** (D3104).
- **Falling back to K1 if Check 830 fails** (R4): the work returns to the owner instead.
- **Letting a queued run fail at start on a dead token**, and leaving it open (D3106).
- **Reversing D3103 to avoid the `actAs` widening** (D3105).
- **A grant on the key for launchers**: the public key reaches them through the block (R13).
- **An opt-in that is never withdrawn** (the recommendation of D3111): the owner chose an
  opt-out.
- **Destroying the key on an opt-out**: Fugaro never destroys a key version (R9), and a ring
  cannot be deleted.
- **Triggering the apply by the updating machine's own `backend:` key** (D3110 as first
  answered): it is never published (F32).
- **Applying the compute root wherever a `batch:` block exists** (the recommendation of D3110):
  the owner chose selection first.
- **Admitting service-account launchers, or dropping the registration** (D3109).
- **Keeping D2075's mount for workflow secrets** (D3107), and **accepting the re-created job**
  (D3108).
- **A record written at first release**: it names whichever job asks first (F30).
- **A per-run service account**: what phase 3 removed, with an account to delete per run.
- **Binding the blob to the job's unique id**: Google assigns it at creation, after the blob is
  wrapped; D3108's registration ties the blob to the id once it exists.
