# Always a budget: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's
explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it: budget mode off is removed (D3009), `observe` stays and the migration
lands on it (D3010); a model with no price is refused in every mode (D3020, not the
recommendation); every Cloud Batch launch is refused with a stated reason (D3021); a repository's
`fugaro.yaml` that says off is rejected at once (D3022, not the recommendation); one release, with
the stop it brings accepted (D3023, not the recommendation); a database's own mode stands (D3024);
the policy node keeps its version (D3025, not the recommendation); no rule is written in terms of
a repository's auth, and retiring that setting is a spec of its own (D3026, the owner's own
answer); the budget database is written before the Firestore location's note (D3027); a managed
project creates its database under the managed gate (D3028, not the recommendation); AB7 waits on a location-preference spec, so no database is
created in a place the installation would not have chosen (D3029). **AB6 is dropped** (F28); its number is not reused.

**It ships in one release** (R1). No CLI release, a hotfix included, is cut from `main` between the
merge of AB7 and I1.

**What a user meets after the upgrade**, each with its fix in the refusal: an installation with no
budget database launches nothing until a project owner runs `fugaro update`; a second machine
launches nothing until its own `fugaro update`; a repository whose job predates the database
launches nothing until `fugaro update` is run for it.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/terraform.md` (AB5, AB7, AB15),
`.claude/rules/credentials-service.md` (AB15), `docs/no-prompt-policy/`,
`docs/design/update-in-flight.md`.

- **Always.** Targeted tests, never a whole package. Hermetic: `internal/gcpfake`,
  `t.TempDir()`. A task that makes a pinned sentence false changes the sentence and its test in
  the same change. A new update stage or refusal is added to the censuses that count it: the stage
  refusal table (`TestRefusalMatrix*`), `internal/apply/steps.go` and the in-flight lines.
- **Until I1.** Every task leaves `policy.ModeOff` and its aliases compiling. A task stops reading
  the mode; it does not delete the constant. I1 deletes it, and fails to build if a reader is left.
- **Existing tests.** A task changes every existing test its change makes false, in whatever
  package, and they count as its files. It finds them by running, beside its own filter,
  `go test ./internal/... ./ee/... -run 'Off|Budget|Mode|Gateway|Mint|CliReference' -count=1`.
  The ones already known are named in the task. AB1a, AB4 and AB7 change what every run, launch
  and update does, so each also runs its whole package once before it is done, which `CLAUDE.md`
  allows for a broad change (F33).
- **Ask first.** A new dependency; a change to the contract's files (§7).
- **Never.** A confirmation step; an edit under `.github/workflows/`; a secret-shaped literal; a
  live cloud call from a test or an agent; a second database where one exists.
- **Before launching** a task that edits a file R10 lists: check that no task of the keys plan
  that edits the same file is live.

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No run puts an API key in the agent's own environment for want of a gateway. An `oauth` run the credentials service does not serve is unchanged (D3026, R12) | AB1b `TestNoAPIKeyRunPutsTheKeyInTheAgentsEnvironment`, `TestAnOAuthRunIsUnchanged` |
| Unset means observe in the local config, the job and the run | AB2 `TestUnsetModeIsObserve`; AB5 `TestTheJobAlwaysNamesItsMode`; AB1a `TestTheRunReadsUnsetAsObserve` |
| `off` is accepted nowhere, and each refusal names the fix | AB2 `TestOffIsRefusedWithTheFix`; AB3 `TestBudgetModeOffFlagIsRefused` |
| A location left for a person never holds back the budget database | AB7 `TestTheBudgetDatabaseIsWrittenBeforeTheLocationNote` |
| An update never builds a second database, and never adopts one outside the installation's own project | AB7 `TestADatabaseUnknownHereIsAdopted`, `TestAStateNamingAForeignProjectIsNotAdopted` |
| An update that cannot create the database leaves the cloud as it was and says who must run it | AB7 `TestAnUpdateThatCannotCreateTheDatabaseSaysWhoMust` |
| A launch by this release's CLI that cannot have an identity is refused by the launcher, with the fix, never by a runner | AB4 `TestALaunchWithNoDatabaseIsRefusedWithTheFix`, `TestALaunchOnAJobThatPredatesTheDatabaseIsRefused` |
| A local config or a policy that says off still loads for the update that rewrites it | AB8a `TestAnOffNodeStillLoadsForTheMigration`; AB8b `TestAnOffLocalConfigStillLoadsForTheMigration` |
| The update names what will stop before it changes anything | AB8b `TestTheUpdateNamesWhatWillStopFirst` |
| A run that started with the budget off finishes across the update | I1 `TestUpdateLeavesARunStartedWithoutABudgetRunning` |
| An installation at the previous release, with no database and mode off, converges through `fugaro update` alone | I1 `TestUpdateConvergesAnInstallationThatHadNoBudget` |
| Old records that say off still read | AB11 `TestAnOldRecordWithModeOffStillValidates` |
| The policy node is migrated in place and stays readable by a version 10 reader | AB8a `TestThePolicyMigrationRewritesOffInPlace`, `TestAMigratedNodeIsAValidVersion10Node` |

## 3. The contract

### C1: the mode vocabulary
- Acceptance: one place answers what a mode string means from now on: `Effective` (unset is
  observe), `Removed` (true for the old word), and the one refusal text with its fix that every
  refusal of the old word uses. `ValidMode` is untouched here. The contract's document lists every
  reader that must move to it.
- Files: `internal/policy/mode.go` (new), `internal/policy/mode_test.go` (new),
  `docs/always-a-budget/contract-mode.md` (new).
- Interfaces: produces the three names; consumed by AB1a, AB3, AB4, AB5, AB8a, AB8b, AB10. AB2
  builds the validation behind it.
- Tests: `TestEffectiveModeOfUnsetIsObserve`, `TestTheRemovedModeIsNamed`.
- Command: `go test ./internal/policy/ -run 'EffectiveModeOfUnset|TheRemovedModeIsNamed' -count=1`.
- Red step: `Effective("")` answers the old word.
- Depends on: none.
- Commit: `policy: the mode vocabulary without off (C1, D3009, D3010)`.
- Reviewer tier: standard.

## 4. Slices and tasks

**Slice 0, the tests:** AB0. **Slice 1, the database everywhere:** AB7. **Slice 2, the vocabulary:** AB2, AB3, AB11.
**Slice 3, every run has a gateway:** AB1a, AB1b, AB4, AB5, AB15. **Slice 4, the migration:** AB8a,
AB8b. **Slice 5, the edges:** AB9, AB10. **Documents and checks:** AB12, AB13, AB14.
**Integration:** I1.

### AB0: the test harnesses have a budget
- Acceptance: the hermetic harnesses the runner's, the CLI's and the end-to-end tests build a run,
  a launch and an installation from give each a fake budget database by default, with a way to ask
  for none; the fake project API answers a permission test; no test's outcome changes at this
  task's merge (F33, U7).
- Files: `internal/testutil/budget.go` (new), `internal/gcpfake/crm.go`, the harness files of
  `internal/runner`, `internal/cli` and `internal/e2e` that build a run's environment and a local
  config (named by this task; U7), `internal/testutil/budget_test.go` (new).
- Interfaces: produces the default the later tasks' tests stand on.
- Tests: `TestTheHarnessGivesARunABudgetDatabase`, `TestTheHarnessCanAskForNone`,
  `TestTheFakeProjectAnswersAPermissionTest`.
- Command: `go test ./internal/testutil/ ./internal/gcpfake/ -run 'TheHarnessGives|TheHarnessCanAsk|TheFakeProjectAnswers' -count=1 && go test -timeout 40m ./internal/runner/... ./internal/cli/... ./internal/e2e/... -count=1`.
- Red step: a harness-built run has no database address.
- Depends on: none.
- Commit: `testutil: hermetic runs and launches have a budget database by default (AB0, F33)`.
- Reviewer tier: standard.

### AB1a: the run always opens its session
- Acceptance: `spendFromEnv` and `resolvePolicy` read an unset mode as observe through C1;
  `startBudgetBackend` opens the session for every run; a run whose job carries no database
  address is refused at its start, and `strayTokenNoBackend` names the fix (`fugaro update` run
  for that repository); observe and enforce behave as today.
- Files: `internal/runner/spend.go`, `internal/runner/policy.go`,
  `internal/runner/budget_session.go`, `internal/runner/projectpolicy.go`,
  `internal/runner/budget_always_test.go` (new), and the end-to-end tests that run the container's
  entry point with no database on purpose (`internal/e2e/cloud_test.go` among them).
- Interfaces: consumes C1.
- Tests: `TestTheRunReadsUnsetAsObserve`, `TestEveryRunOpensItsSession`,
  `TestARunWithNoDatabaseIsRefusedWithTheFix`.
- Command: `go test ./internal/runner/ -run 'TheRunReadsUnset|EveryRunOpensItsSession|ARunWithNoDatabase' -count=1 && go test -timeout 40m ./internal/runner/... ./internal/e2e/... -count=1`.
- Red step: a run with no mode in its environment opens no session.
- Depends on: C1; AB0 (kept, behaviour: the other tests need a database once a run without one is
  refused).
- Commit: `runner: every run opens its budget session (AB1a, R3, F7)`.
- Reviewer tier: opus, mandatory independent review.

### AB1b: the gateway always runs
- Acceptance: `runner.GatewayOn` ignores its mode argument and keeps its signature, so its callers
  in the CLI still build (I1 drops the argument); every cloud run that has a gateway under observe
  today has one always; `agent.BuildEnv` has no arm that puts an API key in the
  agent's environment for want of a gateway; its `oauth` arm and `OAuthStageEnv` are unchanged
  (D3026, R12);
  `serviceSwitchModeRefusal`, the review-class refusal and the compression refusal for a missing
  gateway are deleted (F15); `VertexBudgetRefusal` names observe only (F11).
- Files: `internal/runner/routing.go`, `internal/runner/gateway.go`, `internal/runner/runner.go`,
  `internal/runner/compression.go`, `internal/runner/triage_stage.go`, `internal/agent/env.go`,
  `internal/runner/agentenv.go`, `internal/runner/gateway_always_test.go` (new),
  `internal/agent/env_always_test.go` (new), `internal/runner/routing_internal_test.go` (the
  review-class refusal's tests go with it), and the two CLI tests that pin the Vertex refusal's
  text (`internal/cli/init_test.go`, `validate_test.go`). It is past five files because one
  predicate has that many readers.
- Interfaces: none new.
- Tests: `TestTheGatewayNoLongerReadsTheMode`, `TestTheVertexRefusalNamesObserveOnly` (the
  runner's new file); `TestNoAPIKeyRunPutsTheKeyInTheAgentsEnvironment`, `TestAnOAuthRunIsUnchanged`
  (the agent's new file).
- Command: `go test ./internal/runner/ -run 'TheGatewayNoLongerReadsTheMode|TheVertexRefusalNames|Routing' -count=1 && go test ./internal/agent/ -run 'NoAPIKeyRunPutsTheKey|AnOAuthRunIsUnchanged|Env' -count=1 && go test ./internal/cli/ -run Vertex -count=1`.
- Red step: a run with an unset mode puts the key in the agent's environment.
- Depends on: AB1a (kept, behaviour: the gateway needs the session).
- Commit: `runner: the gateway always runs (AB1b, D3009, F15)`.
- Reviewer tier: opus, mandatory independent review.

### AB2: off is no longer a mode
- Acceptance: `policy.ValidMode` refuses the old word with C1's text; the local config's
  `BudgetMode` answers observe when unset; `modeRank`, `config.modeName`, the config's own
  validation and `CheckProviderGateway` stop treating an empty mode as off;
  `schemas/fugaro.schema.json` lists two modes, so a repository's file that says off is rejected
  with the fix (D3022).
- Files: `internal/policy/policy.go`, `internal/policy/merge.go`, `internal/localcfg/localcfg.go`,
  `internal/config/validate.go`, `internal/config/stage_accounts.go`,
  `internal/config/providers.go`, `schemas/fugaro.schema.json`. It is past five files because the
  vocabulary has that many homes.
- Interfaces: builds C1's other side (the validation).
- Tests: `TestOffIsRefusedWithTheFix`, `TestUnsetModeIsObserve`,
  `TestTheRepositorySchemaListsTwoModes`, `TestAnEmptyModeIsNotOffForTheProviderCheck`.
- Command: `go test ./internal/policy/ ./internal/localcfg/ ./internal/config/ -run 'OffIsRefusedWithTheFix|UnsetModeIsObserve|TheRepositorySchemaLists|AnEmptyModeIsNotOff|Schema' -count=1 && go test ./schemas/ -count=1 && go test ./internal/projectpolicy/ -run AnOffNodeStillLoads -count=1 && go test ./internal/cli/ -run AnOffLocalConfigStillLoads -count=1`. The last two were written by AB8a and AB8b while off still validated; this task is where they would go red, and it runs them. Known tests in other packages that use off as a valid mode, and are this task's to change: in `internal/cli`, `project_policy_test.go`, `launch_budget_test.go` and `init_firebase_test.go`; in `internal/runner`, `policy_record_test.go`. From this merge on, an `api-key` or `oauth` repository that was in off passes the credentials switch where its bundle is ready (F26).
- Red step: `ValidMode("off")` is true.
- Depends on: AB8a and AB8b (kept, behaviour: validation may refuse the old word only once the
  update can load and rewrite a config or a node that says it, F22). It starts after C1 merges.
- Commit: `policy: off is no longer a budget mode (AB2, D3009, D3022)`.
- Reviewer tier: opus, mandatory independent review.

### AB3: the CLI's readers of the mode
- Acceptance: `validate` applies the price rules in every mode and names `model_prices` as the fix (D3020); `doctor`
  prints the lines of AB8b's helper (R8), and names a published policy that says off (R4); `fugaro budget` and
  the `--budget-mode` flag refuse the old word with C1's text; the ceiling layer, the brokering
  doctor, `checkTestHooks` and the cloud settings stop special-casing off.
- Files: `internal/cli/validate.go`,
  `internal/cli/doctor.go`, `internal/cli/doctor_brokering.go`, `internal/cli/budget.go`,
  `internal/cli/init.go`, `internal/cli/exec.go`, `internal/cli/settings_cloud.go`,
  `website/content/cli-reference.md` (the flag's help changes),
  `internal/cli/budget_always_cli_test.go` (new). It is past five files because the mode has that
  many readers in the CLI.
- Interfaces: consumes C1.
- Tests: `TestValidatePricesEveryMode`, `TestDoctorNamesWhatAlwaysABudgetStops`,
  `TestBudgetCommandRefusesTheOldWord`, `TestBudgetModeOffFlagIsRefused`,
  `TestDoctorNamesAPublishedPolicyThatSaysOff`.
- Command: `go test ./internal/cli/ -run 'ValidatePricesEveryMode|DoctorNamesWhatAlwaysABudget|BudgetCommandRefusesTheOldWord|BudgetModeOffFlag|DoctorNamesAPublishedPolicy|CliReferenceNames' -count=1 && go test ./ee/clidoc/ -run CliReferenceIsCurrent -count=1`.
- Red step: `validate` skips the price rules for an unset mode.
- Depends on: C1; AB7 (kept: both edit `init.go`); AB8b (kept: `doctor` prints the helper AB8b
  writes).
- Commit: `cli: validate and doctor without budget mode off (AB3, D3020, R8)`.
- Reviewer tier: standard.

### AB4: the launcher always mints
- Acceptance: `budgetBackendOn` and `budgetMintOn` no longer read the mode: a launch on an
  installation with a database always mints; a launch on one without is refused with the fix, one
  wording for who runs it (F5); a launch on a repository whose job was applied before the database
  existed is refused with its fix, `fugaro update` run for that repository (R6, U6); the chain's ledger reader, the orchestrator and the craft's
  "priced" flag stop reading off.
- Files: `internal/cli/launch_budget.go`, `internal/cli/chain_continues.go`,
  `internal/cli/orchestrator_serve.go`, `internal/cli/craft.go`,
  `internal/cli/launch_budget_always_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestEveryLaunchMints`, `TestALaunchWithNoDatabaseIsRefusedWithTheFix`,
  `TestALaunchOnAJobThatPredatesTheDatabaseIsRefused`.
- Command: `go test ./internal/cli/ -run 'EveryLaunchMints|ALaunchWithNoDatabaseIsRefused|ALaunchOnAJobThatPredates|Mint' -count=1 && go test -timeout 40m ./internal/cli/ -count=1`.
- Red step: a launch with an unset mode mints nothing.
- Depends on: C1; AB7 (kept, behaviour: the refusal's fix must be able to build the database); AB0
  (kept, behaviour: the other tests need a database once a launch without one is refused).
- Commit: `cli: every launch mints the run's token (AB4, F7)`.
- Reviewer tier: opus, mandatory independent review.

### AB5: the job always names its mode
- Acceptance: `budgetEnv` writes `FUGARO_BUDGET_MODE` and the grace for every job, observe when
  unset (F13); `projectJobEnv` writes the same entry for a shared project job, which had none
  (F20); the readiness
  note about a mode with no backend becomes the refusal's fix.
- Files: `internal/infra/spec.go`, `internal/infra/readiness.go`,
  `internal/infra/projectjobs.go`, `internal/infra/spec_budget_env_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestTheJobAlwaysNamesItsMode`, `TestASharedJobNamesItsMode`,
  `TestAnOlderImageSeesObserve`.
- Command: `go test ./internal/infra/ -run 'TheJobAlwaysNamesItsMode|ASharedJobNamesItsMode|AnOlderImageSeesObserve' -count=1`.
- Red step: a job planned with an unset mode has no mode entry.
- Depends on: C1.
- Commit: `infra: every job names its budget mode (AB5, R3, R6)`.
- Reviewer tier: opus, mandatory independent review.

### AB7: `fugaro update` creates the database
- Acceptance: the Firebase stage can run with no flag, on `setup` and `update`, in the
  installation's own project, and acts only to adopt or to create: where the database is known on
  this machine it is skipped as today, so an operator's update of such an installation does what
  it did (F34; amended by console-fixes RI2: the skip is decided on the Firebase root's state, and
  an installation whose state does not carry the run index's composite indexes yet runs the stage,
  because nothing but an apply of that root creates them — unless the credential may not make the
  project's IAM changes that apply makes, which is the operator's own update, and that run keeps
  F34's skip and converges); otherwise it first probes for a database that exists and is unknown on this machine, by the
  Firebase state and never by the history account (F23), and adopts it only if it is in the
  installation's own project (F24); where there is none it probes whether the person can make the
  project's IAM changes; if so it creates the database in observe and records it; if not, or the
  project has no billing, or nothing answers the stage's ordinary confirmation (F4), the stage is
  skipped with one line saying who must run `fugaro update` or that billing must be linked, and
  the rest of the update carries on (R2); the budget database is written before the Firestore
  location's note is reached, so a location left for a person holds back only the run history
  (D3027); on a managed project the stage runs under the managed gate and creates the database
  there, with no confirmation (D3028, R11, U5); before it creates, it lists the project's databases, so a
  deleted state never leads to a second one (F32); the stages that were skipped with the flag run
  with the database: the second installation stage, which deploys the history job and the shared
  project jobs, the history image's mirror and the budget admins' review (F29); the variable
  `enable_budget` is set once the database exists, as today; `--firebase-project` keeps its one
  accepted value; the stage asks nothing new.
- Files: `internal/cli/init_stages.go`, `internal/cli/init_firebase.go`, `internal/cli/init.go`,
  `internal/cli/init_new_machine.go`, `internal/cli/setup.go`, `internal/cli/init_images.go`,
  `internal/cli/init_review.go`, `internal/infra/firebase.go`,
  `internal/cli/setup_refusals_test.go`, `internal/cli/init_firebase_always_test.go` (new),
  `website/content/cli-reference.md`. It is past five files for the stage's censuses.
- Interfaces: none new.
- Tests: `TestUpdateCreatesTheDatabaseWithNoFlag`, `TestADatabaseUnknownHereIsAdopted`,
  `TestAStateNamingAForeignProjectIsNotAdopted`,
  `TestAnUpdateThatCannotCreateTheDatabaseSaysWhoMust`, `TestAManagedProjectCreatesItsDatabaseUnderTheGate`,
  `TestTheNewDatabaseStartsInObserve`, `TestADeletedStateDoesNotLeadToASecondDatabase`,
  `TestTheStagesBehindTheFlagRunWithTheDatabase`, `TestAKnownDatabaseSkipsTheStageAsToday`,
  `TestWithNothingToAnswerTheStageIsSkippedWithItsLine`,
  `TestTheBudgetDatabaseIsWrittenBeforeTheLocationNote`.
- Command: `go test ./internal/cli/ -run 'UpdateCreatesTheDatabase|ADatabaseUnknownHere|AStateNamingAForeignProject|AnUpdateThatCannotCreate|AManagedProjectCreatesItsDatabase|TheBudgetDatabaseIsWrittenBefore|TheNewDatabaseStarts|ADeletedStateDoesNotLead|TheStagesBehindTheFlag|AKnownDatabaseSkips|WithNothingToAnswerTheStage|RefusalMatrix|CliReferenceNames' -count=1 && go test ./ee/clidoc/ -run CliReferenceIsCurrent -count=1 && go test -timeout 40m ./internal/cli/ -count=1`.
- Red step: an update with no flag skips the stage with "no --firebase-project given".
- Depends on: AB0 (kept, behaviour: its probe needs the fake project's permission test, and the
  other update tests need the harness's database). **Not launched before the location-preference
  spec's plan has merged the part that says where a new budget database is created (D3029).**
- Commit: `update: the budget database is created on every installation (AB7, R2, F3)`.
- Reviewer tier: opus, mandatory independent review.

### AB8a: the published policy is migrated
- Acceptance: `fugaro update` rewrites a published mode of off to observe in place and prints one
  line, in a same-version step added to the policy migration, which today acts only on a version
  change (F22); the step reads the node's mode before validation, so a node that says off still
  loads for it and for nothing else; the node keeps its version, and a migrated node validates as
  version 10 (R4, D3025); a publish of the old word is refused with C1's text;
  `refuseModeOffWhileTheServiceServes` is deleted (F15).
- Files: `internal/projectpolicy/migrate.go`, `internal/projectpolicy/policy.go`,
  `internal/cli/init_policy_migrate.go`, `internal/cli/project_cmd.go`,
  `internal/projectpolicy/migrate_off_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestThePolicyMigrationRewritesOffInPlace`, `TestAMigratedNodeIsAValidVersion10Node`,
  `TestAnOffNodeStillLoadsForTheMigration`, `TestPublishingTheOldWordIsRefused`.
- Command: `go test ./internal/projectpolicy/ -run 'ThePolicyMigrationRewritesOff|AMigratedNodeIsAValid|AnOffNodeStillLoads' -count=1 && go test ./internal/cli/ -run 'PublishingTheOldWord|PolicyMigrat' -count=1`.
- Red step: after an update the published node still says off.
- Depends on: C1 (it starts after C1 merges).
- Commit: `update: a published budget mode of off becomes observe (AB8a, R4, D3010)`.
- Reviewer tier: opus, mandatory independent review.

### AB8b: the local config, the repository's file and what will stop
- Acceptance: one helper answers R8's lines, and before it changes anything `fugaro update`
  prints them:
  for the repository it is run for, a pinned model with no price, a `fugaro.yaml` that says off
  and a model key that will move to the service; for the installation, the database's mode and
  caps where a local off is being removed (D3024) and a Batch backend; it reads the local
  config's mode before validation, so a config that says off still loads for the rewrite (F22),
  rewrites off to observe in place and says so; it offers the removal of the line from the checkout's `fugaro.yaml` as a diff, written
  with `--yes` or by a person at the terminal and printed otherwise, as the anchor is (F12); when
  runs are running it prints the line they are owed (R5).
- Files: `internal/cli/budget_stops.go` (new, the helper), `internal/cli/update_budget_off.go`
  (new), `internal/cli/update_budget_off_test.go` (new),
  `internal/localcfg/load_removed_mode.go` (new), `internal/cli/init_fugaroyaml.go`,
  `internal/cli/init_stages.go`,
  `internal/cli/update_in_flight.go` with `docs/design/update-in-flight.md`,
  `internal/apply/steps.go`. It is past five files for the stage's censuses.
- Interfaces: consumes C1.
- Tests: `TestTheUpdateNamesWhatWillStopFirst`, `TestAnOffLocalConfigStillLoadsForTheMigration`,
  `TestTheUpdateNamesTheDatabasesModeAndCaps`, `TestTheLocalConfigIsRewrittenInPlace`,
  `TestTheRepositoryFileRemovalIsOfferedAsADiff`, `TestTheUpdateSaysABudgetOffRunKeepsRunning`.
- Command: `go test ./internal/cli/ -run 'TheUpdateNamesWhatWillStop|AnOffLocalConfigStillLoads|TheUpdateNamesTheDatabasesMode|TheLocalConfigIsRewritten|TheRepositoryFileRemoval|TheUpdateSaysABudgetOffRun|UpdateInFlight|RefusalMatrix|Docs' -count=1 && go test ./internal/apply/ -run StepsMatches -count=1`.
- Red step: after an update the local config still says off.
- Depends on: C1 (it starts after C1 merges); AB7 (kept: both edit `init_stages.go`).
- Commit: `update: off leaves the local config and the repository's file (AB8b, R5, R8, D3022)`.
- Reviewer tier: opus, mandatory independent review.

### AB9: Cloud Batch refuses every launch
- Acceptance: a launch on the Batch backend is refused with one stated reason that names the
  token's missing carrier; `doctor` and `validate` say so for an installation on Batch;
  `docs/backends.md` says so, and `docs/budget-token-channel/spec.md` loses its stale line about
  unbuilt wiring (D3021). The carrier is planned in
  [batch-run-identity](../batch-run-identity/README.md), whose BR1 replaces this refusal.
- Files: `internal/backend/batch/run.go`, `internal/cli/doctor_batch.go`,
  `internal/cli/validate_batch.go`, `docs/backends.md`, `docs/budget-token-channel/spec.md`.
- Interfaces: none.
- Tests: `TestEveryBatchLaunchIsRefusedWithOneReason`, `TestDoctorSaysBatchCannotLaunch`.
- Command: `go test ./internal/backend/batch/ -run EveryBatchLaunchIsRefused -count=1 && go test ./internal/cli/ -run 'DoctorSaysBatchCannotLaunch|OnCloudBatch|Docs' -count=1`.
- Red step: an unsealed Batch launch is accepted.
- Depends on: AB4 (kept, behaviour: Batch may refuse every launch only once every launch is
  sealed; until then an unsealed one still works).
- Commit: `batch: every launch is refused until the token has a carrier (AB9, D3021)`.
- Reviewer tier: standard.

### AB10: the enterprise code
- Acceptance: `ee/ab/admit.go` reads an empty mode through C1, and
  `ee/coordinator/cli/declare.go` no longer skips declaring for a mode of off.
- Files: `ee/ab/admit.go`, `ee/ab/admit_test.go`, `ee/coordinator/cli/declare.go`,
  `ee/coordinator/cli/declare_test.go` (new; the package has no tests today).
- Interfaces: consumes C1.
- Tests: `TestAdmitReadsAnEmptyModeAsObserve`, `TestDeclareNoLongerSkipsOnOff`.
- Command: `go test ./ee/ab/ -run AdmitReadsAnEmptyMode -count=1 && go test ./ee/coordinator/cli/ -run DeclareNoLongerSkips -count=1`.
- Red step: an empty mode fails the provider check as off.
- Depends on: C1.
- Commit: `ee: the budget mode is never off (AB10, F19)`.
- Reviewer tier: standard.

### AB11: the settings registry and the record's schema
- Acceptance: the settings registry and its schema keep saying unset is observe, now under a test
  that reads the run; `schemas/result.schema.json` keeps the old word in `policy.effective.mode`
  for old records and says nothing writes it (R7).
- Files: `internal/settings/registry.go`, `schemas/settings.schema.json`,
  `schemas/result.schema.json`, `schemas/result_off_test.go` (new, beside the schemas' own tests).
- Interfaces: none.
- Tests: `TestAnOldRecordWithModeOffStillValidates`, `TestTheRegistryAndTheRunAgreeOnUnset`.
- Command: `go test ./schemas/ -count=1 && go test ./internal/settings/ -run TheRegistryAndTheRunAgree -count=1`.
- Red step: the registry's test reads the run's default and finds the old word.
- Depends on: AB1a (kept, behaviour: the test reads the run's default).
- Commit: `schemas: unset is observe, and old records keep the old word (AB11, R7, F2)`.
- Reviewer tier: standard.

### AB12: the documents and the operator notes
- Acceptance: `README.md`, `docs/how-to/budgets.md`, `docs/m9-budget-and-dashboard/spec.md`,
  `docs/design/v1.md`, `docs/multi-model.md` and every row of the live checklist that presents off
  (Check 47's budget-off variant among them) no longer present it as a mode or as a rollback; the release notes' `### For operators` is "run `fugaro update`" and what it will
  say: that it creates the budget database (about two dollars a month at the design's estimate,
  billing required), which repositories will stop and why, that a project owner must run it where
  an operator cannot, and that Cloud Batch can launch nothing.
- Files: `README.md`, `docs/how-to/budgets.md`, `docs/m9-budget-and-dashboard/spec.md`,
  `docs/design/v1.md`, `docs/multi-model.md`, `docs/gcp-live-checklist.md`, the release's notes
  file. It is past five files because the mode is described in that many places.
- Interfaces: none.
- Tests: the Docs filter.
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: `git grep -n 'budget-mode off' -- README.md docs/how-to` still finds the rollback.
- Depends on: AB2 (kept: the statements become true only then); AB14 (kept: both edit
  `gcp-live-checklist.md`).
- Commit: `docs: there is always a budget (AB12, F17, F18)`.
- Reviewer tier: standard.

### AB13: the plugin and the website
- Acceptance: `plugin/skills/setup/reference/decisions.md` no longer offers off, and the setup
  skill says the budget database is built by setup; `SECURITY.md` and
  `website/content/security.md` no longer say that budget mode defaults to off or describe a run
  with the budget off, and say which runs still have no gateway (R12); the two sentences
  `docs_security_test.go` pins move with their pins, and `docs/go-to-market/security-check.md`,
  which quotes one, follows; the plugin's and the website's own tests pass.
- Files: `plugin/skills/setup/reference/decisions.md`, `plugin/skills/setup/SKILL.md`,
  `website/content/security.md`, `SECURITY.md`, `internal/cli/docs_security_test.go`,
  `docs/go-to-market/security-check.md`. It is past five files for the pinned sentences.
- Interfaces: none.
- Tests: the plugin's tests; `npm run check` in `website/`; `go test ./internal/cli/ -run SecurityMd`.
- Command: `go test ./plugin/... -count=1 && go test ./internal/cli/ -run 'SecurityMd|Docs' -count=1 && (cd website && npm run check)`.
- Red step: `git grep -n -i 'budget off\|is `off`' -- plugin/skills/setup` still finds the two
  sentences that offer off.
- Depends on: AB3 (kept, behaviour: the skill may stop naming `--budget-mode off` only once the
  CLI refuses it); AB1b (kept, behaviour: the security statements become true only then).
- Commit: `plugin, website: there is always a budget (AB13, F17)`.
- Reviewer tier: opus, mandatory independent review (a plugin skill and `SECURITY.md`).

### AB14: the live checks, as scripts
- Acceptance: Checks 790 and 791 exist as one command each under `scripts/live-checks/`, each with
  its hermetic self-test, its entry in `scripts/live_checks_test.go` and its row in
  `docs/gcp-live-checklist.md`; the five existing scripts that assert a fact about a published
  mode of off no longer do (F23); none is run by an agent.
- Files: `scripts/live-checks/check790.sh`, `check791.sh` (new) with their `_test.sh`,
  `scripts/live-checks/check-580.sh`, `check193.sh`, `check197.sh`, `check610.sh`, `check611.sh`
  with the self-tests that pin those facts,
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: the two `_test.sh`, `TestLiveCheckScripts`.
- Command: `for n in 790 791 193; do bash scripts/live-checks/check${n}_test.sh; done && go test ./scripts/ -run 'TestLiveCheckScripts|TestLiveCheckCIShard' -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: each self-test fails on a missing script.
- Depends on: none.
- Commit: `checks: 790 and 791 as scripts (AB14)`.
- Reviewer tier: standard.
- Owner-run: `bash scripts/live-checks/check790.sh` and `check791.sh`. No agent performs any step.

### AB15: the switch loses its mode argument
- Acceptance: `infra.CredentialsServiceServes` no longer takes the budget mode, and its callers
  follow; the record of the switch comes out the same for every repository that was in observe or
  enforce; a repository that was in off now passes the switch where its bundle is ready, so its
  next apply moves its model key to the service, as for every other repository (F26), and this
  task adds that line to R8's helper.
- Files: `internal/infra/readiness.go`, `internal/infra/spec.go`, `internal/infra/spec_test.go`,
  `internal/cli/init_credentials.go`, `internal/cli/doctor_brokering.go`, `internal/cli/run.go`,
  `internal/cli/budget_stops.go`, `internal/infra/readiness_switch_test.go` (new). It is past five
  files because the function has that many callers.
- Interfaces: none.
- Tests: `TestTheSwitchNoLongerReadsTheMode`, `TestTheSwitchRecordIsUnchangedForObserveAndEnforce`,
  `TestTheUpdateNamesAKeyThatWillMoveToTheService`;
  the existing `TestServesNoAPIKeyOrOAuthWithBudgetOff` is removed with the case it held.
- Command: `go build ./... && go test ./internal/infra/ -run 'TheSwitchNoLongerReadsTheMode|Serves' -count=1 && go test ./internal/cli/ -run 'TheSwitchRecordIsUnchanged|TheUpdateNamesAKeyThatWillMove' -count=1`.
- Red step: the function still has four parameters.
- Depends on: AB5 (kept: both edit `infra/spec.go` and `readiness.go`); AB3 (kept: both edit
  `doctor_brokering.go`); AB8b (kept: both edit `budget_stops.go`); AB1b (kept, behaviour: the switch may ignore the mode only once every
  run has the gateway).
- Commit: `infra: the credentials switch no longer reads the budget mode (AB15, F15)`.
- Reviewer tier: opus, mandatory independent review.

### I1: integration
- Acceptance: `policy.ModeOff`, `config.BudgetOff` and `localcfg.BudgetOff` are deleted, and
  `policy.Removed` is the only place that knows the old word; `runner.GatewayOn` loses its mode
  argument and its callers follow; the three warnings about a mode of off in `init_firebase.go`
  go; the tree builds; one hermetic test
  takes an installation at the previous release, with no database, mode off, a published policy
  that says off and a run in flight, through `fugaro update` alone: the database exists, the
  policy and the local config say observe, the run in flight finishes, and a new run has a gateway
  and no key in the agent's environment.
- Files: `internal/policy/policy.go`, `internal/config/config.go`, `internal/localcfg/localcfg.go`,
  `internal/policy/craft.go` (a comment that names the constant),
  `internal/policy/mode_removed_test.go` (new), `internal/runner/routing.go`,
  `internal/cli/validate.go`, `internal/cli/doctor_brokering.go`, `internal/cli/init_firebase.go`,
  `internal/cli/budget_always_end_to_end_test.go` (new), and every test file that still names a
  deleted constant as a word (11 at this reading), each moved to the literal or deleted with the
  case it held. It is past five files, and every such change is one line.
- Interfaces: consumes every side of C1.
- Tests: `TestUpdateConvergesAnInstallationThatHadNoBudget`, `TestUpdateLeavesARunStartedWithoutABudgetRunning`,
  `TestNoCodeReadsTheRemovedMode`.
- Command: `go build ./... && go vet ./... && ! git grep -n -w -E 'ModeOff|BudgetOff' -- '*.go' && go test ./internal/cli/ -run 'UpdateConvergesAnInstallationThatHadNoBudget|UpdateLeavesARunStartedWithoutABudget' -count=1 && go test ./internal/policy/ -run NoCodeReadsTheRemovedMode -count=1`.
- Red step: the build fails on a reader of the deleted constant.
- Depends on: C1 and every side of an edge it broke: AB2, AB1a, AB3, AB4, AB5, AB10; AB1b, AB7,
  AB8a, AB8b, AB9, AB11 and AB15 (kept, behaviour: the end-to-end test needs them all); AB12, AB13
  and AB14 (kept: R1's window closes at I1, so the notes, the skill and the checks merge first).
- Commit: `budget: off is gone end to end (I1, D3009)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository).

**Levels.** Tasks in one level run at once. **A launcher follows this table.**

| Level | Tasks |
| --- | --- |
| 0 | C1, AB0, AB14 |
| 1 | AB1a, AB5, AB7 (after the location-preference plan, D3029), AB8a, AB10 |
| 2 | AB1b, AB4, AB8b, AB11 |
| 3 | AB2, AB3, AB9 |
| 4 | AB12, AB13, AB15 |
| 5 | I1 |

**Critical path.** The graph read reports `critical path (6 tasks): AB0 -> AB7 -> AB8b -> AB2 -> AB12 -> I1`, and `gain: 5 broken, 20 kept, 3 added; before 7 tasks, weight 17; after 6 tasks, weight 14; improves`.

**Where the graph and this table differ.** AB8a and AB8b read C1's names, and AB2 builds its other
side. The graph tool refuses an edge to a contract from a task that has no decoupling entry, and
none of the three has one: AB8a and AB8b never waited on AB2 (AB2 waits on them). So the graph
shows AB8a as ready at once and AB2 and AB8b as waiting only on other tasks. All three start after
C1 merges.

**Edges broken by C1 and rejoined in I1.** AB1a, AB3, AB4, AB5 and AB10 after AB2: each needs to
know what a mode string means, not the validation's code.

**Edges kept.** Each is stated in its task: behaviour (AB1b after AB1a; AB2 after AB8a and AB8b;
AB1a, AB4 and AB7 after AB0; AB3 after AB8b, whose helper it prints; AB4 after AB7; AB9 after AB4; AB11 after AB1a; AB12 after
AB2; AB13 after AB3 and AB1b; AB15 after AB1b; I1 after the rest) and a shared file (AB3 and AB8b
after AB7; AB12 after AB14; AB15 after AB3, AB5 and AB8b).

**Shared files.** No level holds two writers of one file.

| File | Writers, by level |
| --- | --- |
| `internal/policy/policy.go` | AB2 (3), I1 (5) |
| `internal/localcfg/localcfg.go` | AB2 (3), I1 (5) |
| `internal/config/config.go` | I1 (5) |
| `internal/config/validate.go`, `stage_accounts.go`, `providers.go`, `internal/policy/merge.go`, `schemas/fugaro.schema.json` | AB2 (3) |
| `internal/runner/spend.go`, `policy.go`, `budget_session.go`, `projectpolicy.go` | AB1a (1) |
| `internal/runner/routing.go`, `gateway.go`, `runner.go`, `compression.go`, `triage_stage.go`, `agentenv.go`, `internal/agent/env.go`, `internal/runner/routing_internal_test.go` | AB1b (2); I1 (5) for `routing.go` |
| `internal/cli/init.go` | AB7 (1), AB3 (3) |
| `internal/cli/validate.go`, `doctor.go`, `budget.go`, `exec.go`, `settings_cloud.go` | AB3 (3), I1 (5) for `validate.go` |
| `internal/cli/doctor_brokering.go` | AB3 (3), AB15 (4), I1 (5) |
| `internal/cli/launch_budget.go`, `chain_continues.go`, `orchestrator_serve.go`, `craft.go` | AB4 (2) |
| `internal/infra/spec.go`, `readiness.go` | AB5 (1), AB15 (4) |
| `internal/infra/projectjobs.go` | AB5 (1) |
| `internal/cli/init_stages.go` | AB7 (1), AB8b (2) |
| `internal/cli/init_firebase.go`, `init_new_machine.go`, `setup.go`, `init_images.go`, `init_review.go`, `setup_refusals_test.go`, `internal/infra/firebase.go` | AB7 (1); I1 (5) for `init_firebase.go` |
| `website/content/cli-reference.md` | AB7 (1), AB3 (3) |
| `internal/cli/budget_stops.go` | AB8b (2), AB15 (4) |
| the test harnesses, `internal/gcpfake/crm.go` | AB0 (0) |
| `internal/e2e/cloud_test.go` | AB1a (1) |
| `internal/projectpolicy/migrate.go`, `policy.go`, `internal/cli/init_policy_migrate.go`, `project_cmd.go` | AB8a (1) |
| `internal/cli/init_fugaroyaml.go`, `update_in_flight.go`, `docs/design/update-in-flight.md`, `internal/apply/steps.go` | AB8b (2) |
| `internal/cli/init_credentials.go`, `run.go`, `internal/infra/spec_test.go` | AB15 (4) |
| `internal/backend/batch/run.go`, `internal/cli/doctor_batch.go`, `validate_batch.go`, `docs/backends.md`, `docs/budget-token-channel/spec.md` | AB9 (3) |
| `ee/ab/admit.go`, `ee/coordinator/cli/declare.go` | AB10 (1) |
| `internal/settings/registry.go`, `schemas/settings.schema.json`, `schemas/result.schema.json` | AB11 (2) |
| `docs/gcp-live-checklist.md` | AB14 (0), AB12 (4) |
| `website/content/security.md`, `SECURITY.md`, `internal/cli/docs_security_test.go`, `docs/go-to-market/security-check.md`, the setup skill | AB13 (4) |
| test files that name the deleted constants | their package's task, then I1 (5) |

R10 lists the files the keys plan also writes.

## 6. The estimate

As `fugaro plan graph --plan docs/always-a-budget/implementation-plan.md` printed it:

`ready in 420–1680 min, no history yet for 20 tasks (20 tasks; seeds for 20; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, and it moves as runs finish. The graph counts one node more than this plan's 19 runs: the
contract change of §7, which runs only if raised.

## 7. Contract changes anticipated

- **R-mode-text** (C1, raised by AB3): if the one refusal text cannot name the right fix for both a
  flag and a file. Reaches AB2, AB3, AB8a.

## 8. Checks the plan takes

From the reserved block 790–799. Both owner-run (AB14), after the release.

| Check | What it measures |
| --- | --- |
| **790** | On a real installation at the previous release with no budget database and a run in flight: a project owner's `fugaro update` creates the database, the run in flight finishes, a new run has a gateway and holds no key in the agent's environment, and a run on a job image built before the release runs as observe; an operator's update on a second such installation changes nothing and says who must run it (U1, U2, U4) |
| **791** | A run whose model has no price is refused with the `model_prices` fix under observe, and a run whose committed `fugaro.yaml` says off is refused with its fix |

## 9. After-deploy steps

**`fugaro update`, run by a project owner, once for the installation and once for each
repository, as today.** It names what will change, creates the budget database where there is
none, rewrites off to observe in the local config and the published policy, and re-applies the
repository it is run for so its job names its mode and its database. An operator's update on an
installation with no database skips that stage and says a project owner must run it; until then
that installation launches nothing (D3023).

**What a person may still have to do, and the update says so:** enter a `model_prices` entry for a
model with no price; commit the removal of `budget.mode: off` from a repository's `fugaro.yaml`.

AB12's release notes say what the update prints.

## 10. Considered and declined

- **Removing `observe` as well** (D3010's other option).
- **Running an unpriced model under observe and marking its spend** (the recommendation of D3020):
  the owner chose the refusal.
- **Reading a repository file's off as unset for one release** (the recommendation of D3022): the
  owner chose rejection at once.
- **A carrier for the token on Cloud Batch, or removing the Batch backend** (D3021's other
  options).
- **Dropping the Firestore location's confirmation, or leaving the stage's order as it is**
  (D3027's other options).
- **Adopting under the managed gate and creating outside it** (the recommendation of D3028): the
  owner chose creation under the gate.
- **Keeping budget mode off** (D3009's other option, the plan as it stood before the owner's
  answer).
- **Removing the Terraform switch for the budget backend** (AB6, dropped): it would create project
  IAM on every apply of the installation root, outside the update's probe, and move the addresses
  of resources that exist (F28). The switch is set once the database exists, as today.
- **A rule for `oauth` repositories the credentials service does not serve** (both options of
  D3026): the owner wants no rule in terms of a repository's auth, and that setting retired under
  its own spec (R12).
- **Two releases, the database first** (the recommendation of D3023): the owner chose one release
  and accepted the stop.
- **Lowering a database's mode to observe where a local config said off** (D3024's other option).
- **A new version of the policy node** (the recommendation of D3025): the owner kept version 10.
- **Stopping the whole update where the database cannot be created** (the first draft of R2): the
  stage is skipped with its line, and the launch is what refuses.
- **Letting an observe run continue when the database is unreachable** (F8): the run's identity is
  the database's, and the credentials service serves no run without it.
- **A database in a project other than the installation's own**: refused today, by the owner's
  ruling of 2026-10-23.
