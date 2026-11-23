# Required-version stamp: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's
explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it, all 2026-11-23:
- `update` writes its own release (D3260).
- CLIs released before L1 stay unfenced, and nothing here bumps `projectpolicy.Version` (D3261, not
  the recommendation).
- There is no way to lower the stamp (D3262).
- A cached stamp is trusted for 15 minutes (D3263).
- Read failures fail open, except a cached stamp above the binary and a malformed stamp, which
  refuse (D3264, bounded by D3268).
- Console read paths are exempt and print one stderr line (D3265).
- An `update` from a binary below the stamp refuses before its first write (D3266).
- A coding agent's `update` raises the stamp under the managed gate, like `rules`, but only once the
  installation has converged (D3267, amended after review and confirmed by the owner).
  - The stage holds while `credentialsMoveHeld` holds the policy node, or while
    `servicesImageStale` says the credentials service is behind.
  - In a session, `credentialsMoveHeld` holds on every installation with a credentials block.
  - So in practice the owner's own `update` raises the stamp; an agent session raises it only when
    nothing is held.
- The stamp is bounded (D3268). A cached value above the binary refuses on its own for only 15
  minutes. After that, only a re-read that confirms it refuses.
- `update` writes only a published release (D3269). It checks live that the tag `v<Version>` is a
  published release that is neither a draft nor a pre-release, whatever the update-check switches
  say. If the lookup fails, the stamp stays unchanged and a line says so.
- `fugaro budget kill` is exempt from the fence in all its forms (D3270).

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md` (the HARD RULE on `update`, safety with runs in flight, the
no-prompt policy, "a field never changes meaning", tested docs), `docs/no-prompt-policy/`,
`docs/design/update-in-flight.md`, `scripts/live-checks/README.md` (K1).

- **Always.**
  - Run targeted tests, never a whole package.
  - Keep tests hermetic: `internal/gcpfake` for the database, `t.TempDir()` with `XDG_CACHE_HOME`
    pointed into it for the cache, a fake HTTP server for the releases API, and no home directory.
  - A doc change runs `go test ./internal/cli/ -run Docs -count=1`.
  - A task whose tests add a literal repository-path read regenerates
    `docs/ci-affected-tests/read-map.json` with `go run ./scripts/ci-readmap`, which `TestReadMap…`
    (`internal/ciselect`) checks. In this plan, T1 (the schema) and K2 (the check scripts) do, at
    different levels. Every other task reads its fixtures from `t.TempDir()`. A task that finds it
    needs a new literal read raises it, rather than writing the map in a level that already has a
    writer.
  - The refusal sentence comes from `internal/versionstamp` and nowhere else.
  - Every exemption is keyed by command path, never by name (R6).
- **Ask first.** A new dependency. A change to the node's shape once T1 has merged: that is a
  follow-up on T1's package before any consumer launches.
- **Never.**
  - A prompt, an override flag or an environment switch for the stamp (A5).
  - Lowering the stamp (D3262).
  - Reading the stamp inside a job.
  - A `projectpolicy.Version` bump for this purpose (D3261).
  - A secret-shaped literal.
  - A live cloud or network call from a test or an agent. The owner alone runs Check 900.
  - Moving `docs/design/update-in-flight.md:62` (F13).

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A command not in the exempt table is fenced, including one added tomorrow | T3 `TestEveryCommandIsFencedOrExempt` (walks the whole tree, `ee/` extensions included) |
| The fence checks the project the command acts on, including a member on the shared config | T3 `TestFenceReadsTheCommandsOwnProjectFlag`, `TestFenceCoversTheSharedConfigFallback` |
| Stopping spend always works | T3 `TestBudgetKillPassesBelowTheStamp` (every form, `--workspace` included) |
| Read failures fail open; a cached value above the binary or a malformed stamp refuses | T1 `TestUnreachableDatabasePasses`, `TestCachedStampAboveBinaryRefusesWithoutARead`, `TestMalformedStampRefuses` |
| A stale or future-dated cache is re-read before it refuses (D3268) | T1 `TestStaleCachedStampAboveBinaryNeedsAReRead`, `TestFutureCacheTimestampIsStale` |
| The stamp never falls | T2a `TestOlderUpdateNeverLowersTheStamp`, `TestLostRaceWritesNothingAndSaysSo` |
| A malformed stamp refuses wherever it is read, and names its fix | T1 `TestMalformedSentenceNamesItsFix`, T2a `TestStampStageRefusesAMalformedStamp`, T4 `TestUpdateRefusesAMalformedStamp` |
| A stage that stops or fails before it leaves the stamp unchanged | T2a `TestFailedPolicyStageLeavesTheStampUnchanged` |
| The stamp rises only once the installation has converged (D3267) | T2a `TestStampHoldsWhilePolicyIsHeld`, `TestStampHoldsWhileCredentialsServiceIsBehind` |
| Only a published release is written (D3269) | T2c `TestPublishedIgnoresTheUpdateCheckSwitches`, `TestOlderLineHotfixIsPublished`; T2b `TestStampStaysWhenTheReleaseLookupFails` |
| An agent session's raise is judged and audited | T2b `TestAgentSessionStampRaiseWritesTheAuditRecord` |
| A database that is not this installation's is never stamped | T2a `TestStampStageSkipsAnUnadoptedDatabase` |
| An older `update` writes nothing at all, in any mode | T4 `TestUpdateBelowTheStampRefusesBeforeAnyWrite`, `TestForgetBelowTheStampRefuses`, `TestConfigOnlyBelowTheStampRefuses` |
| A run in flight keeps running | T2b `TestUpdateLeavesARunInFlightRunning` with the stamp raised mid-run |
| The unit's daemon never crash-loops on the stamp | T6 `TestDaemonBelowTheStampHoldsUnderAUnit` |
| `dev` neither checks nor writes | T2a `TestDevBuildLeavesTheStampAlone`, T3 `TestDevBuildSkipsTheFence` |
| No prompt anywhere | T2a runs the stage under the no-prompt harness with stdin closed |

Review focus: the exempt table (T3), the gate entry and `gateStep` (T2b), the door check's position
(T4), and the package's sentence, schema and fail-open rules (T1).

## 3. Tasks

### T1: the stamp package
This task replaces round 1's contract C1 and reader T1 (R10). There is no stub: the reader is real
from the start.
- Acceptance: package `internal/versionstamp` holds:
  - the node path `config/policy/required_cli`;
  - `Stamp{Schema int; Min string; SetAt time.Time}` and `SchemaVersion = 1`;
  - `Decode`, which is strict: an unknown key, an unknown `schema`, or a `min` that
    `updatecheck.Valid` rejects is a `*MalformedError` that names the node; and `Encode`, which
    writes `min` without a `v`;
  - `Below(stamp, version string) bool` over `updatecheck.Newer`, false for a `dev` or pre-release
    `version`;
  - `*BelowError`, whose `Error()` is the spec's refusal sentence with `updatecheck.BrewCommand`;
  - the `Source` interface, `Required(ctx, Query) (Result, error)`, and `New(Options) Source`.
    `Query` carries the project's RTDB URL, `NoAuth`, a `Fresh` flag (no cache) and the clock.
    `Result` says whether a stamp was found, its value, and where the answer came from (database,
    cache or none, with the reason it could not read).

  The reader behind `New` behaves as follows:
  - It reads through a narrow interface that `rtdb.Client` satisfies, with a 2-second timeout.
  - It keeps one cache file per RTDB URL (the URL hashed) under `localcfg.UserDir(...,
    localcfg.DirCache)/fugaro/`. The file holds the stamp value, never a verdict.
  - A cache entry younger than 15 minutes is used without a read (D3263). An entry dated in the
    future counts as stale.
  - A cached value above the binary refuses with no read only while it is younger than 15 minutes.
    Past that, it refuses only if a re-read confirms it (D3268).
  - An unreachable database, a timeout, missing credentials or a denied read is a `Result` with its
    reason and no error.
  - A malformed stamp is `*MalformedError`, and its sentence names the fix. An unknown `schema` was
    written by a newer release, so the fix is `brew upgrade`. A `min` that does not parse is a hand
    edit, so the fix is T7's hand edit in the Firebase console.
  - `Fresh` skips the cache for the read and writes it afterwards. A fresh read that fails falls back
    to the cache rule: a cached value above the binary that is younger than 15 minutes still refuses
    (D3264, D3268). T4 relies on this.
  - A corrupt or oversized cache file counts as no cache (`updatecheck.readBounded`'s rule).

  `schemas/required-cli.schema.json` is closed at every level, and a test holds it to `Decode` both
  ways. The package comment says what a writer and a reader may rely on.
  `internal/cli/required_cli_source.go` is the one seam, `newStampSource = versionstamp.New`, that
  every CLI consumer calls and its tests replace.
- Files: `internal/versionstamp/stamp.go` (new), `internal/versionstamp/source.go` (new),
  `internal/versionstamp/stamp_test.go` (new), `internal/versionstamp/source_test.go` (new),
  `schemas/required-cli.schema.json` (new), `internal/cli/required_cli_source.go` (new), and
  `docs/ci-affected-tests/read-map.json`, regenerated for the schema's literal read. That is seven
  files: the seam is one line, and the map is generated.
- Interfaces: produces all of the above. Consumed by T2a, T3, T4, T5, T6 and K1.
- Tests: `TestStampDecodesStrictly`, `TestSchemaAgreesWithDecode`, `TestBelowIsFalseForDev`,
  `TestBelowErrorIsTheSpecSentence`, `TestUnreachableDatabasePasses`, `TestTimeoutIsTwoSeconds`,
  `TestCachedStampAboveBinaryRefusesWithoutARead`, `TestStaleCachedStampAboveBinaryNeedsAReRead`,
  `TestStaleCachedStampPassesWhenTheReReadFails`, `TestFutureCacheTimestampIsStale`,
  `TestFreshCacheSkipsTheRead`, `TestStaleCacheReads`, `TestMalformedStampRefuses`,
  `TestDeniedReadPassesWithItsReason`, `TestCorruptCacheIsNoCache`, `TestFreshQueryIgnoresTheCache`,
  `TestCacheIsPerDatabase`, `TestFreshReadFailureStillRefusesOnAFreshCacheAbove`,
  `TestMalformedSentenceNamesItsFix`, and `TestReadMap…` in `internal/ciselect`.
- Command: `go test ./internal/versionstamp/ -count=1 && go test ./internal/ciselect/ -run TestReadMap -count=1`
- Red step: `TestStampDecodesStrictly` fails while an unknown key decodes.
- Depends on: none.
- Commit: `versionstamp: the required-CLI stamp and its cached, fail-open reader (T1, D3263, D3264, D3268, R10)`
- Reviewer tier: opus, mandatory independent review (`fugaro review --pr N`) before any consumer
  launches, because it is a wire format every later release reads.

### T2c: the published-release lookup
- Acceptance: `updatecheck.Published(ctx, client, endpoint, version) (bool, error)` asks the releases
  API whether the tag `v<version>` is a published release that is neither a draft nor a pre-release
  (D3269).
  - It ignores `FUGARO_NO_UPDATE_CHECK`, a project config's `update_check: false` and the update-check
    cache.
  - It never follows a redirect (`refuseRedirect`'s rule) and reads a bounded body.
  - A network failure or an unexpected status is an error, which the caller turns into "stamp left
    unchanged".
  - An older-line hotfix is published, because the question is about the tag, not the newest
    release.
- Files: `internal/updatecheck/published.go` (new), `internal/updatecheck/published_test.go` (new).
- Interfaces: produces `updatecheck.Published`. Consumed by T2b, which wires it into the stage.
- Tests (all against a fake HTTP server): `TestPublishedIgnoresTheUpdateCheckSwitches`,
  `TestPublishedIgnoresAStaleCache`, `TestOlderLineHotfixIsPublished`,
  `TestDraftOrPrereleaseIsNotPublished`, `TestUnknownTagIsNotPublished`,
  `TestLookupFailureIsAnError`, `TestPublishedRefusesARedirect`.
- Command: `go test ./internal/updatecheck/ -run Published -count=1`
- Red step: `TestOlderLineHotfixIsPublished` fails against a lookup that compares with the newest
  release.
- Depends on: none.
- Commit: `updatecheck: ask whether this release's tag is published (T2c, D3269)`
- Reviewer tier: standard.

### T2a: the `version-stamp` stage
- Acceptance:
  - `initflow.VersionStamp = "version-stamp"` sits in `order` right after `CredentialsCopy`, with
    `needs: Policy, Rules` (R1).
  - The stage is `SelfConfirming` and confirms through `confirmStageWrite` with no prompt (R2). It is
    not in `runCovered`, and `cliStages` gets its row.
  - It is registered in `initStages` for both doors.
  - Its `Check` first asks `budgetDBUnadopted` (`internal/cli/init_rules_stage.go`) and skips with
    that function's sentence when the database is not this installation's (F22). It then reads the
    node and is done when the stored `min` is not below `Version`.
  - It holds (`Skipped`, `Undone`, with a line naming what it waits for) in two cases (D3267, F20):
    - `credentialsMoveHeld` holds the policy node below `projectpolicy.Version`;
    - `servicesImageStale` says the credentials service's recorded image is not this release's (the
      test `credentialsResolveBeforeRaise` uses).

    These are the engine's own facts, and the loop does not change.
  - Its `Check` refuses a malformed stamp (`*MalformedError`) with that error's sentence. It never
    overwrites one.
  - Its `Apply` writes once, through `GetETag` and `PutIfMatch`, never lowering the stored value, and
    stamps `set_at`. A lost race ends the stage with nothing written and a line that says so. It does
    not retry; the next `update` raises the stamp. This matches the policy stage's write
    (`policyMigration.write`, `rtdb.ErrPrecondition`), whose one admission covers exactly the bytes
    and ETag it writes.
  - Its line is `required CLI: 0.28.0 → 0.29.0 (members below it are refused until they upgrade)`,
    or `current` when unchanged.
  - A `dev` or pre-release binary leaves the node alone and says so.
  - Whether the release is published (D3269) is asked through a seam this task declares in
    `update_version_stamp.go`. Until T2b wires `updatecheck.Published` in, the seam answers "lookup
    unavailable", so the stamp stays unchanged with a line.
  - `--plan-only` plans and writes nothing.
  - `apply` and `stagesNamed` write it only when it is named.
- Files: `internal/initflow/stage.go`, `internal/initflow/loop_test.go` (the test that pins the
  `order` slice), `internal/cli/update_version_stamp.go` (new),
  `internal/cli/update_version_stamp_test.go` (new), `internal/cli/init_stages.go`,
  `internal/cli/setup_refusals_test.go`, `internal/cli/testdata/setup_events.golden.ndjson`. That is
  seven files, as T1 has: two of them change one line each to name the new slot (the pinned order
  and the refusal matrix row), and one is a golden file the events test regenerates.
- Interfaces: consumes T1 (`Decode`, `Encode`, the path, `Below`). Produces `initflow.VersionStamp`,
  which L8's spec places its stage after (O1), and the release seam T2b fills.
- Tests: `TestUpdateRaisesTheStampToItsRelease` (with the seam answering "published"),
  `TestOlderUpdateNeverLowersTheStamp`, `TestLostRaceWritesNothingAndSaysSo`,
  `TestStampStageRefusesAMalformedStamp`,
  `TestFailedPolicyStageLeavesTheStampUnchanged`, `TestDevBuildLeavesTheStampAlone`,
  `TestPlanOnlyWritesNoStamp`, `TestStampLineSaysCurrent`, `TestStampHoldsWhilePolicyIsHeld`,
  `TestStampHoldsWhileCredentialsServiceIsBehind`, `TestStampStageSkipsAnUnadoptedDatabase`, and
  `TestRefusalMatrixListsEveryStageTheCLIRegisters` with the new row.
- Command: `go test ./internal/cli/ -run 'Stamp|RefusalMatrix' -count=1 && go test ./internal/initflow/ -count=1`
- Red step: `TestUpdateRaisesTheStampToItsRelease` fails while no stage writes the node.
- Depends on: T1 (the node's shape).
- Commit: `update: the version-stamp stage raises the required CLI (T2a, D3260, D3267, R1, R2)`
- Reviewer tier: opus (a write to the project's config node, and a stage of `update`).

### T2b: the gate, the release bound, the in-flight note and the policy delete
- Acceptance:
  - `initflow.VersionStamp` joins `gatedStages` (D3267). Its comment gives the `rules` stage's
    reason: the document is the binary's own.
  - `versionStampStage.gateStep` sits beside `rulesStage.gateStep`, `policyMigrateStage.gateStep`
    and `workspaceStage.gateStep` (F21).
    - Its document is the exact bytes `PutIfMatch` writes, under the ETag that was read.
    - Its admitted rule names the binary's own `Version`.
    - The stage's `Apply` admits before its one write. The audit record follows the write (D1014),
      as the policy stage's does, so a lost race records nothing and retries nothing.
    - A refused admission comes back as a `GatedError`.
  - The stage's release seam is wired to `updatecheck.Published` with the production client and
    endpoint (D3269). A lookup that fails leaves the stamp unchanged, with a line.
  - `TestUpdateLeavesARunInFlightRunning` raises the stamp mid-run, and the run still finishes.
  - `docs/design/update-in-flight.md` gets a `version-stamp` row right after the
    `credentials-service` row, below line 62 (F13). The row says what the stage changes (one node no
    run reads) and its exposure for a run in flight (none). It adds no line to
    `announceRunsInFlight`.
  - `fugaro project policy publish --delete` leaves `config/policy/required_cli` in place.
- Files: `internal/cli/update_managed.go`, `internal/cli/update_version_stamp.go` (the `gateStep`
  call and the seam's wiring), `internal/cli/update_in_flight_test.go`,
  `docs/design/update-in-flight.md`, `internal/cli/update_version_stamp_gate_test.go` (new).
- Interfaces: consumes T2a's stage and T2c's `Published`.
- Tests: `TestUpdateLeavesARunInFlightRunning`, `TestUpdateManagedGatesTheVersionStampStage`,
  `TestGatedLostRaceRecordsNothingAndDoesNotRetry`,
  `TestAgentSessionStampRaiseWritesTheAuditRecord` (the record names the document's digest and the
  binary's `Version`), `TestStampStaysWhenTheReleaseLookupFails`, `TestPolicyDeleteKeepsTheStamp`,
  plus the docs census.
- Command: `go test ./internal/cli/ -run 'LeavesARunInFlight|GatesTheVersionStamp|StampRaiseWritesTheAuditRecord|ReleaseLookupFails|PolicyDeleteKeepsTheStamp|InFlightLine' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestUpdateManagedGatesTheVersionStampStage` fails while an agent session stops at the
  stage.
- Depends on: T2a (the stage it gates, wires and documents; behaviour and one file) and T2c (the
  function it wires in).
- Commit: `update: the version stamp under the managed gate, bounded to a published release (T2b, D3267, D3269)`
- Reviewer tier: opus, mandatory independent review (a managed-gate entry).

### T3: the fence at the root
- Acceptance:
  - `newCoreRoot`'s hook becomes `PersistentPreRunE`. It does everything it does today, then
    `requireCLI`.
  - `requireCLI` resolves the project from the command's own `--config`, `--project` and
    `--gcp-project` and the environment, and never records a checkout (R3).
  - With no project or no RTDB URL, it passes.
  - With a stamp above `Version`, it returns `*BelowError` as a user error, which exits 1 with the
    sentence on stderr and nothing on stdout, `--json` included.
  - The pre-run records its verdict in the context: fenced, exempt, console read, or on a platform.
    `selectProject`'s shared-config branch calls `requireCLI` only on an explicit "fenced" verdict,
    and it evaluates `backend.OnPlatform` itself. A context with no verdict is not checked (R4).
  - One table, keyed by command path, holds every exemption. An entry may carry a predicate over the
    parsed flags, and only `apply`'s does. The exemptions are:
    - `version`, `help` and the completion tree;
    - `doctor`, `update`, `setup` and `update-skills`;
    - `budget kill`, in every form, `--workspace` included (D3270);
    - the retired tombstone;
    - `orchestrator run` (R7);
    - the console read paths, except bare `apply`. `apply --status` passes through the predicate
      `cmd.Flags().GetBool("status")` being true, since `--status` is a `BoolVar` in `apply.go`. So
      `apply --status=false` is fenced (R5). These paths print one stderr line when the stamp is
      above the binary;
    - `exec` and every hidden in-cloud entry point: `credentials serve`, `orchestrator serve`,
      `voice serve`, `intake serve`, `coordinator serve`, `history`, `image gate`, `image record`,
      `image git-credential` and `image selftest`.
  - `backend.OnPlatform` passes whatever the table says.
- Files: `internal/cli/root.go`, `internal/cli/required_cli.go` (new),
  `internal/cli/required_cli_test.go` (new), `internal/cli/project.go`,
  `internal/cli/required_cli_shared_test.go` (new).
- Interfaces: consumes T1 (`Source` through `newStampSource`, `BelowError`). Produces `requireCLI`.
- Tests:
  - `TestEveryCommandIsFencedOrExempt` (the tree walk; acceptance 2);
  - `TestRunBudgetSetAndSettingsSetRefuseBelowTheStamp`;
  - `TestExemptCommandsPassBelowTheStamp`;
  - `TestBudgetKillPassesBelowTheStamp`;
  - `TestConsoleReadsPrintOneStderrLineAndCleanStdout`;
  - `TestBareApplyIsFencedButApplyStatusPasses` (with `apply --status=false` fenced too);
  - `TestFenceReadsTheCommandsOwnProjectFlag`;
  - `TestFenceCoversTheSharedConfigFallback`;
  - `TestDoctorOnTheSharedConfigPassesBelowTheStamp` and
    `TestRunsLsOnTheSharedConfigPassesBelowTheStamp`;
  - `TestSharedConfigWithABareContextIsNotChecked`;
  - `TestOnPlatformPasses`;
  - `TestDevBuildSkipsTheFence`;
  - `TestNoUpdateCheckDoesNotDisableTheFence`;
  - `TestTheUpdateCheckLookupRemembersNothing`, which still passes.
- Command: `go test ./internal/cli/ -run 'Fence|FencedOrExempt|BelowTheStamp|ConsoleReadsPrint|BareApply|OnPlatformPasses|DevBuildSkips|NoUpdateCheckDoesNot|RemembersNothing|SharedConfig' -count=1`
- Red step: `TestEveryCommandIsFencedOrExempt` fails on the first command with no check.
- Depends on: T1 (the interface and the error).
- Commit: `cli: refuse a project command below the required CLI (T3, D3264, D3265, D3270, R3-R6)`
- Reviewer tier: opus, mandatory independent review (the guard every command passes through).

### T4: `update` refuses below the stamp
- Acceptance:
  - The check sits at the top of the door's `RunE` (`newInitCmdWith`, shared by `update`, `update
    --repo` and `setup`), after the `--check` arm. It is skipped for `--print-vars` (R8, F24).
  - A fresh read (`Query.Fresh`) of a stamp above `Version` refuses with "this installation was
    updated by fugaro X; upgrade first" and the brew command. That covers `--forget`,
    `--config-only`, `--publish-config`, `--plan-only` and the converge.
  - The refusal comes before the image catalog guard, `syncAccess`, `rewriteLocalBudgetMode` and the
    first stage (F11).
  - It does not run for a `dev` or pre-release binary.
  - An unreadable stamp fails open with one stderr line (D3264). A fresh read that fails still
    refuses on a cached value above the binary that is younger than 15 minutes (T1).
  - A malformed stamp refuses with `*MalformedError`'s sentence.
  - Under `--json`, the refusal still prints the one JSON object a refusal before any stage prints
    (`TestInitJSONOnARefusalBeforeAnyStage`, `internal/cli/json_everywhere_test.go`).
- Files: `internal/cli/update_stamp_guard.go` (new), `internal/cli/init.go`,
  `internal/cli/update_stamp_guard_test.go` (new).
- Interfaces: consumes T1.
- Tests: `TestUpdateBelowTheStampRefusesBeforeAnyWrite` (the fake database, the local config file and
  the catalog are byte-identical afterwards), `TestForgetBelowTheStampRefuses`,
  `TestConfigOnlyBelowTheStampRefuses`, `TestUpdatePlanOnlyBelowTheStampRefuses`,
  `TestUpdateCheckModeReadsNoStamp`, `TestPrintVarsReadsNoStamp`, `TestSetupBelowTheStampRefuses`,
  `TestUpdateRefusesAMalformedStamp`, `TestUpdateJSONRefusalIsOneObject` and
  `TestUpdateAtTheStampPasses`.
- Command: `go test ./internal/cli/ -run 'BelowTheStamp|CheckModeReadsNoStamp|PrintVarsReadsNoStamp|AtTheStampPasses|RefusesAMalformedStamp|JSONRefusalIsOneObject|InitJSONOnARefusalBeforeAnyStage' -count=1`
- Red step: `TestForgetBelowTheStampRefuses` fails while the check sits only at the converge.
- Depends on: T1 (the interface).
- Commit: `update: refuse before any write when the installation is ahead of this binary (T4, D3266, R8)`
- Reviewer tier: opus (a write guard of `update`).

### T5: doctor's `cli-below-required`
- Acceptance:
  - A new check, `cli-below-required`, sits beside `cli-version`. It is a warning whose fix is the
    brew command and then `fugaro update`.
  - It passes at or above the stamp. It skips with its reason when the stamp cannot be read or for a
    `dev` binary.
  - Under `--offline`, it answers from the cache when there is one and skips otherwise (R9).
  - It is modelled on `cli-version`. It is not `ownFix`, because the update class's own fix entry
    already names the right commands: its `doctorUpdateFixes` entry is `brew upgrade` and then
    `fugaro update`, as `cli-version`'s is. It also gets entries in `doctorUpdatePrefixes` and
    `doctorShorts` in `internal/cli/doctor_render.go`.
  - `doctor --json` adds the row; no existing field changes.
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_render.go`, `internal/cli/doctor_required.go`
  (new), `internal/cli/doctor_required_test.go` (new).
- Interfaces: consumes T1 (`Source`, `Below`).
- Tests: `TestDoctorReportsCLIBelowRequired`, `TestDoctorOfflineAnswersFromTheStampCache`,
  `TestDoctorOfflineSkipsWithNoCache`, `TestDoctorCLIBelowRequiredIsNotOwnFix`.
- Command: `go test ./internal/cli/ -run 'CLIBelowRequired|OfflineAnswersFromTheStampCache|OfflineSkipsWithNoCache' -count=1`
- Red step: `TestDoctorReportsCLIBelowRequired` fails while doctor has no such row.
- Depends on: T1 (the reader it reports from).
- Commit: `doctor: report a CLI below the project's required release (T5, R9)`
- Reviewer tier: standard.

### T6: the daemon holds below the stamp
- Acceptance:
  - `orchestratorDaemon` reads the stamp at `start` and at every tick through the cache.
  - When the stamp is above its binary, it prints one line naming the stamp and the brew command.
    Under a unit, it then goes to `holdForUpdate`. Under `--once` or with no `--binary`, it exits 0,
    because `holdForUpdate` returns nil there, as it does for `updateDue`.
  - It claims nothing and drives nothing after that line (R7).
  - When the stamp rises mid-run, the daemon stops claiming, releases its lease the way `abandon`
    does, and serves nothing more. It then holds under a unit, or exits 0.
  - Under a unit it never exits on the stamp.
- Files: `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_required_test.go` (new).
- Interfaces: consumes T1.
- Tests: `TestDaemonBelowTheStampHoldsUnderAUnit`, `TestDaemonOnceBelowTheStampExits` (exit 0),
  `TestDaemonRaisedMidRunStopsClaiming` (and releases its lease).
- Command: `go test ./internal/cli/ -run 'DaemonBelowTheStamp|DaemonOnceBelowTheStamp|RaisedMidRunStopsClaiming' -count=1`
- Red step: `TestDaemonRaisedMidRunStopsClaiming` fails while the stamp is read only at `start`.
- Depends on: T1 (the reader).
- Commit: `orchestrator: hold below the required CLI (T6, R7)`
- Reviewer tier: standard.

### K1: Check 900, as a script
- Acceptance: `check-900-version-stamp.sh <fugaro-project>` reads, read-only:
  - `config/policy/required_cli` over the RTDB REST API with the caller's token. It prints the node's
    `schema`, `min` and `set_at`, which are not secrets, and checks them against the schema's rules.
  - `fugaro version`.
  - `fugaro doctor --json`'s `cli-below-required` row.
  - The time of a cold-cache `fugaro runs ls --json` with `XDG_CACHE_HOME` pointed at an empty
    scratch directory.

  It prints PASS when the node is well formed, `min` equals the installed release after an
  `update`, the doctor row agrees, and the cold read is under 2 seconds (U1). Run as a member, it
  also settles U2. Its whole call set is the read calls named. The token goes into a 0600 curl config
  in a 0700 scratch directory, every call runs with `</dev/null`, and it asks nothing.
- Files: `scripts/live-checks/check-900-version-stamp.sh` (new),
  `scripts/live-checks/check-900-version-stamp_test.sh` (new).
- Interfaces: consumes T1's node shape and the doctor row's id.
- Tests: `test_a_well_formed_stamp_passes`, `test_a_malformed_stamp_fails`,
  `test_a_stamp_below_the_installed_release_fails`, `test_a_slow_cold_read_fails`,
  `test_only_reads_are_called`, `test_the_token_is_never_on_argv`, `test_stdin_is_closed`.
- Command: `bash scripts/live-checks/check-900-version-stamp_test.sh`
- Red step: `test_a_well_formed_stamp_passes` fails before the script exists.
- Depends on: T1 (the node's shape and the schema it checks against).
- Commit: `required-version-stamp K1: Check 900, as a script`
- Reviewer tier: standard.
- Owner-run: after the release that carries L1, the owner runs
  `bash scripts/live-checks/check-900-version-stamp.sh fugaro`. If a member exists, the member runs
  it as well. No agent runs it.

### K2: Check 900 registered
- Acceptance:
  - The script and its test join `liveCheckScripts`.
  - `docs/gcp-live-checklist.md` gets a register row and a section for Check 900: the command, PASS,
    and each exit code.
  - The coordinator and periodic-reviews READMEs' reading of the register's highest written section
    moves from 892 to 900.
- Files: `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`, `docs/coordinator/README.md`,
  `docs/periodic-reviews/README.md`, and `docs/ci-affected-tests/read-map.json`, regenerated for the
  new script paths `liveCheckScripts` reads.
- Tests: `go test ./scripts/ -run LiveCheck -count=1` and the docs census.
- Command: `go test ./scripts/ -run LiveCheck -count=1 && go test ./internal/ciselect/ -run TestReadMap -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: the suite fails while the script is not registered.
- Depends on: K1 (the script it registers).
- Commit: `required-version-stamp K2: Check 900 registered`
- Reviewer tier: standard.
### T8: end to end, and the release notes
- Acceptance:
  - One hermetic end-to-end test on `gcpfake`, with a fake releases API, takes `Version` through the
    build's seam:
    1. `update` at a published release R writes `min = R`;
    2. a binary at R-1 is refused by `fugaro run` with the sentence and exit 1;
    3. it is refused by `fugaro update` before any write;
    4. `doctor` warns;
    5. `budget kill` still works;
    6. a binary at R passes.
  - The release notes of the release L1 ships in say, under `### For operators`:
    - run `fugaro update`;
    - members, and any CI pin that selects a project (F18), below the new release are refused until
      `brew upgrade`;
    - CLIs released before L1 are not fenced (D3261).
- Files: `internal/cli/required_cli_e2e_test.go` (new), `docs/releases/v0.29.0.md` (or whichever
  release notes file is next when it lands).
- Interfaces: none new. It exercises T1, T2a, T2b, T3, T4 and T5 together.
- Tests: `TestRequiredCLIEndToEnd`.
- Command: `go test ./internal/cli/ -run RequiredCLIEndToEnd -count=1`
- Red step: the test fails while any of its five refusals or passes is missing.
- Depends on: T2b (the stage raises only once the release lookup is wired in), T3, T4, T5 and T6 (the
  behaviour the test walks through).
- Commit: `required-version-stamp: the stamp end to end, and its release note (T8)`
- Reviewer tier: opus.

### T7: the refusal in the `working` skill, and the last-resort recovery
- Acceptance: `plugin/skills/working/SKILL.md` gets one sentence, which says:
  - a project command refused with "this project requires fugaro X or newer" means hand the user
    `brew upgrade dimipaun/tap/fugaro` and then `fugaro update` for their own terminal;
  - the agent never runs `brew`, never retries, and never looks for a way around the refusal.

  `docs/release.md`, beside its "Update checks" paragraph, covers:
  - what the stamp is;
  - that nothing lowers it (D3262);
  - that `update` writes only a published release (D3269);
  - the last-resort recovery when a stamp is wrong, whoever wrote it (F1): an admin edits
    `config/policy/required_cli` by hand in the Firebase console.

  The `plugin/` tests and the docs census pass.
- Files: `plugin/skills/working/SKILL.md`, `docs/release.md`.
- Tests: the plugin's command-and-flag tests and the docs census.
- Command: `cd plugin && go test ./... -count=1 && cd .. && go test ./internal/cli/ -run Docs -count=1`
- Red step: none. These are doc lines, and the plugin tests guard what they name.
- Depends on: T2b (the release bound and the recovery the docs describe) and T3 (the refusal the
  skill names). The sentences become true only then.
- Commit: `plugin, docs: the required-CLI refusal and its last-resort recovery (T7, D3268, D3269)`
- Reviewer tier: opus (a plugin skill).

## 4. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for `dimipaun/fugaro`). The repository's
`rigor.default` is `startup`, and no `rigor.areas` are set, so the pass is applied aggressively.
**It breaks no edge in this plan (R10).** Round 1 broke six edges with a contract. Review showed that
the contract's reader side could not be held behind it: `fugaro plan graph` refuses both an unrecorded
edge from the reader to the contract and a recorded one, which closes a cycle. A contract for the
reader would also be most of the reader. So the stamp package is built first, as T1, with no stub,
and every consumer waits on it by an ordinary edge. No contract change is anticipated.

| Level | Tasks |
| --- | --- |
| 0 | T1, T2c |
| 1 | T2a, T3, T4, T5, T6, K1 |
| 2 | T2b, K2 |
| 3 | T8, T7 |

**Critical path.** The graph read reports `critical path (4 tasks): T1 -> T2a -> T2b -> T7`
with T1 as the bottleneck. T8 sits beside T7 at level 3 and is as long. The graph and this table agree: no task is ready before what it waits on.

**Edges, all kept:**
- T2a, T3, T4, T5, T6 and K1 after T1: each consumes the stamp package.
- T2b after T2a: it gates, wires and documents the stage, and edits `update_version_stamp.go`.
- T2b after T2c: it wires `updatecheck.Published` in.
- K2 after K1: K2 registers the script K1 writes.
- T8 after T2b, T3, T4, T5 and T6: the end-to-end test walks their behaviour.
- T7 after T2b and T3: the sentences become true only then.

**Shared files.** No level holds two writers of one file.

| File | Writers, by level |
| --- | --- |
| `internal/versionstamp/*`, `schemas/required-cli.schema.json`, `internal/cli/required_cli_source.go` | T1 (0) |
| `docs/ci-affected-tests/read-map.json` (regenerated) | T1 (0), K2 (2) |
| `internal/updatecheck/published*.go` | T2c (0) |
| `internal/initflow/stage.go`, `internal/initflow/loop_test.go`, `internal/cli/init_stages.go`, `internal/cli/setup_refusals_test.go`, `internal/cli/update_version_stamp_test.go`, `internal/cli/testdata/setup_events.golden.ndjson` | T2a (1) |
| `internal/cli/update_version_stamp.go` | T2a (1), T2b (2) |
| `internal/cli/update_managed.go`, `internal/cli/update_in_flight_test.go`, `docs/design/update-in-flight.md`, `internal/cli/update_version_stamp_gate_test.go` | T2b (2) |
| `internal/cli/root.go`, `internal/cli/project.go`, `internal/cli/required_cli.go`, `required_cli_test.go`, `required_cli_shared_test.go` | T3 (1) |
| `internal/cli/init.go`, `internal/cli/update_stamp_guard*.go` | T4 (1) |
| `internal/cli/doctor.go`, `internal/cli/doctor_render.go`, `internal/cli/doctor_required*.go` | T5 (1) |
| `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_required_test.go` | T6 (1) |
| `scripts/live-checks/check-900-*` | K1 (1) |
| `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`, `docs/coordinator/README.md`, `docs/periodic-reviews/README.md` | K2 (2) |
| `internal/cli/required_cli_e2e_test.go`, the release notes file | T8 (3) |
| `plugin/skills/working/SKILL.md`, `docs/release.md` | T7 (3) |

T3, T4 and T6 all add tests to `internal/cli`, each in its own new file. A helper two of them need
lives in T1's seam file, or is written once by whichever merges first and reused by the other: no
duplicate (CLAUDE.md, "search before you write a helper").

## 5. The estimate

As `fugaro plan graph --plan docs/required-version-stamp/implementation-plan.md` printed it:

`ready in 285–1140 min, no history yet for 7 tasks (12 tasks; tiers for 5, seeds for 7; from the cached history of 2026-11-22 07:25 UTC)`

It is informational and reserves no budget. It is what the tasks' tiers and this machine's history
predict, and it moves as runs finish. The graph counts the twelve tasks of this plan.

The proposed runs are the twelve tasks (T1, T2c, T2a, T2b, T3, T4, T5, T6, K1, K2, T8, T7, with T1
replacing round 1's C1 and reader) and the independent reviews of T1, T2b and T3. Each needs the
owner's go.

## 6. Checks the plan takes

From the reserved block 900–909. Check 900 is used and 901–909 are free.

| Check | What it measures | Who runs it |
| --- | --- | --- |
| **900** | The stamp on a real project after `update`: the node's shape and value, doctor's row, and a cold-cache read under 2 seconds (U1); run as a member, the viewer's read (U2) | The owner (and a member, where one exists), after the release that carries L1 |

## 7. After-deploy steps

`fugaro update`, and nothing else. T8's release-notes entry says so under `### For operators`, and
adds what the update leads to: members and CI pins that select a project, if their CLI is below the
new release, are refused until `brew upgrade`. Nothing a run holds changes.

## 8. Considered and declined

- **The check in `cmd/fugaro/main.go`.** Declined, as the spec declines it: the root hook has the
  resolved command and its flags.
- **The check only in `selectProject`.** Declined. A command that resolves its project another way
  would go unfenced, and fenced-by-default needs the tree walk. R4 adds `selectProject`'s fallback
  as a second call site instead.
- **A `projectpolicy.Version` bump to fence pre-L1 CLIs.** Declined by D3261.
- **Joining `runCovered`.** Declined by R2 (F4).
- **The stage right after `policy`.** Declined by R1: that slot is taken by contract (F3).
- **A contract in front of the stamp package (round 1's C1).** Declined by R10: the graph cannot hold
  the reader's side behind it, and a contract for the reader would be most of the reader.
- **A loop change that hands the stage earlier stages' undone skips (round 1's `PriorUndone`).**
  Declined. Only the policy stage skips with `Undone` before the slot, and the engine's own facts
  (`credentialsMoveHeld`, `servicesImageStale` and the adoption check) cover each of its cases (F20).
- **Bounding the release by the update-check cache's newest release.** Declined by D3269: the
  update-check switches turn it off, and it would refuse an older-line hotfix.
