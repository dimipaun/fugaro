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
  refuse (D3264).
- Console read paths are exempt and print one stderr line (D3265).
- An `update` from a binary below the stamp refuses before its first write (D3266).
- A coding agent's `update` raises the stamp under the managed gate: `version-stamp` joins
  `gatedStages`, like `rules` (D3267).

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md` (the HARD RULE on `update`, safety with runs in flight, the
no-prompt policy, "a field never changes meaning", tested docs), `docs/no-prompt-policy/`,
`docs/design/update-in-flight.md`, `scripts/live-checks/README.md` (K1).

- **Always.**
  - Run targeted tests, never a whole package.
  - Keep tests hermetic: `internal/gcpfake` for the database, `t.TempDir()` with `XDG_CACHE_HOME`
    pointed into it for the cache, and no home directory.
  - A doc change runs `go test ./internal/cli/ -run Docs -count=1`.
  - The refusal sentence comes from the contract and nowhere else.
  - Every exemption is keyed by command path, never by name (R6).
- **Ask first.** A new dependency. A change to a contract file: raise a contract change (§6).
- **Never.**
  - A prompt, an override flag or an environment switch for the stamp (A5).
  - Lowering the stamp (D3262).
  - Reading the stamp inside a job.
  - A `projectpolicy.Version` bump for this purpose (D3261).
  - A secret-shaped literal.
  - A live cloud call from a test or an agent. The owner alone runs Check 900.
  - Moving `docs/design/update-in-flight.md:62` (F13).

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A command not in the exempt table is fenced, including one added tomorrow | T3 `TestEveryCommandIsFencedOrExempt` (walks the whole tree, `ee/` extensions included) |
| The fence checks the project the command acts on, including a member on the shared config | T3 `TestFenceReadsTheCommandsOwnProjectFlag`, `TestFenceCoversTheSharedConfigFallback` |
| Read failures fail open; a cached value above the binary or a malformed stamp refuses | T1 `TestUnreachableDatabasePasses`, `TestCachedStampAboveBinaryRefusesWithoutARead`, `TestMalformedStampRefuses` |
| The stamp never falls | T2a `TestOlderUpdateNeverLowersTheStamp`, `TestConcurrentRaisesKeepTheHigher` |
| A stage that stops or fails before it leaves the stamp unchanged | T2a `TestFailedPolicyStageLeavesTheStampUnchanged` |
| An older `update` writes nothing at all | T4 `TestUpdateBelowTheStampRefusesBeforeAnyWrite` (the fake database and the local config are byte-identical afterwards) |
| A run in flight keeps running | T2b `TestUpdateLeavesARunInFlightRunning` with the stamp raised mid-run |
| The unit's daemon never crash-loops on the stamp | T6 `TestDaemonBelowTheStampHoldsUnderAUnit` |
| `dev` neither checks nor writes | T2a `TestDevBuildLeavesTheStampAlone`, T3 `TestDevBuildSkipsTheFence` |
| No prompt anywhere | T2a runs the stage under the no-prompt harness with stdin closed |

Review focus: the exempt table (T3), the gate entry (T2b), the door check's position (T4) and the
contract's sentence and schema (C1).

## 3. Contract

### C1: the stamp contract
- Acceptance: package `internal/versionstamp` declares:
  - the node path `config/policy/required_cli`;
  - `Stamp{Schema int; Min string; SetAt time.Time}` and `SchemaVersion = 1`;
  - `Decode`, which is strict: an unknown key, an unknown `schema`, or a `min` that
    `updatecheck.Valid` rejects is a `*MalformedError` that names the node; and `Encode`, which
    writes `min` without a `v`;
  - `Below(stamp, version string) bool` over `updatecheck.Newer`, false for a `dev` or pre-release
    `version`;
  - `*BelowError`, whose `Error()` is the spec's refusal sentence with `updatecheck.BrewCommand`;
  - the `Source` interface: `Required(ctx, Query) (Result, error)`, where `Query` carries the
    project's RTDB URL, `NoAuth`, a `Fresh` flag (no cache) and the clock, and `Result` says whether
    a stamp was found, its value, and where the answer came from (database, cache or none, with the
    reason it could not read).

  The contract also holds:
  - `schemas/required-cli.schema.json`, closed at every level, and a test that holds it to `Decode`;
  - `docs/required-version-stamp/contract.md`, which says what a writer and a reader may rely on;
  - a stub `New(Options) Source` that answers "no stamp";
  - `internal/cli/required_cli_source.go`, the one seam `newStampSource = versionstamp.New` that every
    CLI consumer calls and its tests replace.
- Files: `internal/versionstamp/stamp.go` (new), `internal/versionstamp/stamp_test.go` (new),
  `internal/versionstamp/stub.go` (new, stub), `schemas/required-cli.schema.json` (new),
  `docs/required-version-stamp/contract.md` (new), `internal/cli/required_cli_source.go` (new). That
  is six files; the sixth is a one-line seam.
- Interfaces: produces everything above. Consumed by T2a, T3, T4, T5, T6 and K1. T1 builds the other
  side.
- Tests: `TestStampDecodesStrictly`, `TestSchemaAgreesWithDecode`, `TestBelowIsFalseForDev`,
  `TestBelowErrorIsTheSpecSentence`, `TestStubSourceFindsNoStamp`.
- Command: `go test ./internal/versionstamp/ -count=1`
- Red step: `TestSchemaAgreesWithDecode` fails while the schema accepts a key `Decode` refuses.
- Depends on: none.
- Commit: `versionstamp: the required-CLI stamp contract (C1, R10)`
- Reviewer tier: opus, mandatory independent review (`fugaro review --pr N`) before any side is
  written against it, because it is a wire format every later release reads.

## 4. Tasks

### T1: the reader — cache, timeout, fail-open
- Acceptance: the real `Source` (unexported `newSource`) behaves as follows:
  - It reads the node with a 2-second timeout through a narrow reader interface that `rtdb.Client`
    satisfies.
  - It keeps one cache file per RTDB URL (the URL hashed) under `localcfg.UserDir(...,
    localcfg.DirCache)/fugaro/`. The file holds the stamp value, never a verdict.
  - A cache entry younger than 15 minutes is used without a read (D3263). A cached value above the
    binary refuses with no read at all.
  - An unreachable database, a timeout, missing credentials or a denied read is a `Result` with its
    reason and no error. The cached value still applies.
  - A malformed stamp is `*MalformedError`.
  - `Fresh` skips the cache for the read and writes it afterwards.
  - A corrupt or oversized cache file counts as no cache (`updatecheck.readBounded`'s rule).
- Files: `internal/versionstamp/source.go` (new), `internal/versionstamp/cache.go` (new),
  `internal/versionstamp/source_test.go` (new).
- Interfaces: consumes C1's types. Produces `newSource`, which I1 exposes as `New`.
- Tests: `TestUnreachableDatabasePasses`, `TestTimeoutIsTwoSeconds`,
  `TestCachedStampAboveBinaryRefusesWithoutARead`, `TestFreshCacheSkipsTheRead`,
  `TestStaleCacheReads`, `TestMalformedStampRefuses`, `TestDeniedReadPassesWithItsReason`,
  `TestCorruptCacheIsNoCache`, `TestFreshQueryIgnoresTheCache`, `TestCacheIsPerDatabase`.
- Command: `go test ./internal/versionstamp/ -run 'Source|Cache|Unreachable|Timeout|Malformed|Denied|Fresh' -count=1`
- Red step: `TestCachedStampAboveBinaryRefusesWithoutARead` fails on a fake that counts reads.
- Depends on: C1 (the types it implements).
- Commit: `versionstamp: the cached, fail-open reader (T1, D3263, D3264)`
- Reviewer tier: standard.

### T2a: the `version-stamp` stage
- Acceptance:
  - `initflow.VersionStamp = "version-stamp"` sits in `order` right after `CredentialsCopy`, with
    `needs: Policy, Rules` (R1).
  - The stage is `SelfConfirming` and confirms through `confirmStageWrite` with no prompt (R2). It is
    not in `runCovered`, and `cliStages` gets its row.
  - It is registered in `initStages` for both doors.
  - Its `Check` reads the node and is done when the stored `min` is not below `Version`.
  - Its `Apply` writes through `GetETag` and `PutIfMatch`, retries on a lost race, keeps the higher of
    the stored and the new value, and stamps `set_at`.
  - Its line is `required CLI: 0.28.0 → 0.29.0 (members below it are refused until they upgrade)`,
    or `current` when unchanged.
  - A `dev` or pre-release binary leaves the node alone and says so.
  - `--plan-only` plans and writes nothing.
  - `apply` and `stagesNamed` write it only when it is named.
  - In a coding agent's session on a managed project, the stage writes when the gate opens it
    (D3267); T2b adds the gate entry.
- Files: `internal/initflow/stage.go`, `internal/cli/update_version_stamp.go` (new),
  `internal/cli/update_version_stamp_test.go` (new), `internal/cli/init_stages.go`,
  `internal/cli/setup_refusals_test.go`.
- Interfaces: consumes C1 (`Decode`, `Encode`, the path, `Below`). Produces `initflow.VersionStamp`,
  which L8's spec places its stage after (O1).
- Tests: `TestUpdateRaisesTheStampToItsRelease`, `TestOlderUpdateNeverLowersTheStamp`,
  `TestConcurrentRaisesKeepTheHigher`, `TestFailedPolicyStageLeavesTheStampUnchanged`,
  `TestDevBuildLeavesTheStampAlone`, `TestPlanOnlyWritesNoStamp`, `TestStampLineSaysCurrent`, and
  `TestRefusalMatrixListsEveryStageTheCLIRegisters` with the new row.
- Command: `go test ./internal/cli/ -run 'Stamp|RefusalMatrix' -count=1 && go test ./internal/initflow/ -count=1`
- Red step: `TestUpdateRaisesTheStampToItsRelease` fails while no stage writes the node.
- Depends on: C1 (the node's shape).
- Commit: `update: the version-stamp stage raises the required CLI (T2a, D3260, R1, R2)`
- Reviewer tier: opus (a write to the project's config node, and a stage of `update`).

### T2b: the stamp under the gate, in the in-flight note and beside the policy delete
- Acceptance:
  - `initflow.VersionStamp` joins `gatedStages` (D3267), with a comment that
    gives the `rules` stage's reason: the document is the binary's own.
  - `TestUpdateLeavesARunInFlightRunning` raises the stamp mid-run, and the run still finishes.
  - `docs/design/update-in-flight.md` gets a `version-stamp` row after the policy rows, below line 62
    (F13). The row says what the stage changes (one node no run reads) and its exposure for a run in
    flight (none); it adds no line to `announceRunsInFlight`.
  - `fugaro project policy publish --delete` leaves `config/policy/required_cli` in place.
- Files: `internal/cli/update_managed.go`, `internal/cli/update_in_flight_test.go`,
  `docs/design/update-in-flight.md`, `internal/cli/policy_delete_keeps_stamp_test.go` (new).
- Interfaces: consumes T2a's stage.
- Tests: `TestUpdateLeavesARunInFlightRunning`, `TestUpdateManagedGatesTheVersionStampStage`,
  `TestPolicyDeleteKeepsTheStamp`, plus the docs census.
- Command: `go test ./internal/cli/ -run 'LeavesARunInFlight|GatesTheVersionStamp|PolicyDeleteKeepsTheStamp|InFlightLine' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestUpdateManagedGatesTheVersionStampStage` fails while an agent session stops at the
  stage.
- Depends on: T2a (the stage it gates and documents; behaviour).
- Commit: `update: the version stamp under the managed gate and in the in-flight note (T2b, D3267)`
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
  - `selectProject`'s shared-config branch calls `requireCLI` too (R4).
  - One table keyed by command path holds every exemption:
    - `version`, `help` and the completion tree;
    - `doctor`, `update`, `setup` and `update-skills`;
    - the retired tombstone;
    - `orchestrator run` (R7);
    - the console read paths, except bare `apply`. `apply --status` passes (R5). These print one
      stderr line when the stamp is above the binary;
    - `exec` and every hidden in-cloud entry point: `credentials serve`, `orchestrator serve`,
      `voice serve`, `intake serve`, `coordinator serve`, `history`, `image gate`, `image record`,
      `image git-credential` and `image selftest`.
  - `backend.OnPlatform` passes whatever the table says.
- Files: `internal/cli/root.go`, `internal/cli/required_cli.go` (new),
  `internal/cli/required_cli_test.go` (new), `internal/cli/project.go`,
  `internal/cli/required_cli_shared_test.go` (new).
- Interfaces: consumes C1 (`Source` through `newStampSource`, `BelowError`). Produces `requireCLI`.
- Tests:
  - `TestEveryCommandIsFencedOrExempt` (the tree walk; acceptance 2);
  - `TestRunBudgetSetAndSettingsSetRefuseBelowTheStamp`;
  - `TestExemptCommandsPassBelowTheStamp`;
  - `TestConsoleReadsPrintOneStderrLineAndCleanStdout`;
  - `TestBareApplyIsFencedButApplyStatusPasses`;
  - `TestFenceReadsTheCommandsOwnProjectFlag`;
  - `TestFenceCoversTheSharedConfigFallback`;
  - `TestOnPlatformPasses`;
  - `TestDevBuildSkipsTheFence`;
  - `TestNoUpdateCheckDoesNotDisableTheFence`;
  - `TestTheUpdateCheckLookupRemembersNothing`, which still passes.
- Command: `go test ./internal/cli/ -run 'Fence|FencedOrExempt|BelowTheStamp|ConsoleReadsPrint|BareApply|OnPlatformPasses|DevBuildSkips|NoUpdateCheckDoesNot|RemembersNothing' -count=1`
- Red step: `TestEveryCommandIsFencedOrExempt` fails on the first command with no check.
- Depends on: C1 (the interface).
- Commit: `cli: refuse a project command below the required CLI (T3, D3264, D3265, R3-R6)`
- Reviewer tier: opus, mandatory independent review (the guard every command passes through).

### T4: `update` refuses below the stamp
- Acceptance:
  - At the converge door, shared by `update`, `update --repo` and `setup`, a fresh read
    (`Query.Fresh`) of a stamp above `Version` refuses with "this installation was updated by fugaro
    X; upgrade first" and the brew command.
  - The refusal comes before the image catalog guard, `syncAccess`, `rewriteLocalBudgetMode` and the
    first stage (R8).
  - It also refuses under `--plan-only`.
  - It does not run under `--check`, or for a `dev` or pre-release binary.
  - An unreadable stamp fails open with one stderr line (D3264).
- Files: `internal/cli/update_stamp_guard.go` (new), `internal/cli/init.go`,
  `internal/cli/update_stamp_guard_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestUpdateBelowTheStampRefusesBeforeAnyWrite`, which checks that the fake database, the
  local config file and the catalog are byte-identical afterwards. Also
  `TestUpdatePlanOnlyBelowTheStampRefuses`, `TestUpdateCheckModeReadsNoStamp`,
  `TestSetupBelowTheStampRefuses` and `TestUpdateAtTheStampPasses`.
- Command: `go test ./internal/cli/ -run 'BelowTheStamp|PlanOnlyBelowTheStamp|CheckModeReadsNoStamp|AtTheStampPasses' -count=1`
- Red step: `TestUpdateBelowTheStampRefusesBeforeAnyWrite` fails on the rewritten local config.
- Depends on: C1 (the interface).
- Commit: `update: refuse before any write when the installation is ahead of this binary (T4, D3266, R8)`
- Reviewer tier: opus (a write guard of `update`).

### T5: doctor's `cli-below-required`
- Acceptance:
  - A new check, `cli-below-required`, sits beside `cli-version`. It is a warning whose fix is the
    brew command and then `fugaro update`.
  - It passes at or above the stamp. It skips with its reason when the stamp cannot be read or for a
    `dev` binary.
  - Under `--offline`, it answers from the cache when there is one and skips otherwise (R9).
  - It is not `ownFix`, because `update` does not fix it.
  - `doctor --json` adds the row; no existing field changes.
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_required.go` (new),
  `internal/cli/doctor_required_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestDoctorReportsCLIBelowRequired`, `TestDoctorOfflineAnswersFromTheStampCache`,
  `TestDoctorOfflineSkipsWithNoCache`, `TestDoctorCLIBelowRequiredIsNotOwnFix`.
- Command: `go test ./internal/cli/ -run 'CLIBelowRequired|OfflineAnswersFromTheStampCache|OfflineSkipsWithNoCache' -count=1`
- Red step: `TestDoctorReportsCLIBelowRequired` fails while doctor has no such row.
- Depends on: C1 (the interface).
- Commit: `doctor: report a CLI below the project's required release (T5, R9)`
- Reviewer tier: standard.

### T6: the daemon holds below the stamp
- Acceptance:
  - `orchestratorDaemon` reads the stamp at `start` and at every tick through the cache.
  - When the stamp is above its binary, it prints one line naming the stamp and the brew command.
    Under a unit, it then goes to `holdForUpdate`. Under `--once` or with no `--binary`, it exits 1.
  - It claims nothing and drives nothing after that line (R7).
  - Under a unit it never exits on the stamp.
- Files: `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_required_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestDaemonBelowTheStampHoldsUnderAUnit`, `TestDaemonOnceBelowTheStampExits`,
  `TestDaemonRaisedMidRunStopsClaiming`.
- Command: `go test ./internal/cli/ -run 'DaemonBelowTheStamp|DaemonOnceBelowTheStamp|RaisedMidRunStopsClaiming' -count=1`
- Red step: `TestDaemonRaisedMidRunStopsClaiming` fails while the stamp is read only at `start`.
- Depends on: C1 (the interface).
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
- Interfaces: consumes C1's node shape and the doctor row's id.
- Tests: `test_a_well_formed_stamp_passes`, `test_a_malformed_stamp_fails`,
  `test_a_stamp_below_the_installed_release_fails`, `test_a_slow_cold_read_fails`,
  `test_only_reads_are_called`, `test_the_token_is_never_on_argv`, `test_stdin_is_closed`.
- Command: `bash scripts/live-checks/check-900-version-stamp_test.sh`
- Red step: `test_a_well_formed_stamp_passes` fails before the script exists.
- Depends on: C1 (the node's shape).
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
  `docs/periodic-reviews/README.md`.
- Tests: `go test ./scripts/ -run LiveCheck -count=1` and the docs census.
- Command: `go test ./scripts/ -run LiveCheck -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: the suite fails while the script is not registered.
- Depends on: K1 (the script it registers).
- Commit: `required-version-stamp K2: Check 900 registered`
- Reviewer tier: standard.

### I1: integration
- Acceptance:
  - `internal/versionstamp/stub.go` is deleted, and `New` returns T1's `newSource`.
  - One hermetic end-to-end test on `gcpfake`, which takes `Version` through the build's seam:
    1. `update` at release R writes `min = R`;
    2. a binary at R-1 is refused by `fugaro run` with the sentence and exit 1;
    3. it is refused by `fugaro update` before any write;
    4. `doctor` warns;
    5. a binary at R passes.
  - The release notes of the release L1 ships in say, under `### For operators`: run `fugaro
    update`; members, and any CI pin that selects a project (F18), below the new release are refused
    until `brew upgrade`; CLIs released before L1 are not fenced (D3261).
- Files: `internal/versionstamp/stub.go` (deleted), `internal/versionstamp/source.go`,
  `internal/cli/required_cli_e2e_test.go` (new), `docs/releases/v0.29.0.md` (or whichever release
  notes file is next when it lands).
- Interfaces: rejoins C1's sides: T1 against T2a, T3, T4, T5 and T6.
- Tests: `TestRequiredCLIEndToEnd`.
- Command: `go test ./internal/cli/ -run RequiredCLIEndToEnd -count=1 && go test ./internal/versionstamp/ -count=1`
- Red step: the end-to-end test fails while the stub answers "no stamp".
- Depends on: C1, T1, T2a, T3, T4, T5, T6 and K1. T2a is a behaviour dependency, because the test needs the writer. K1 is in the list because it is a side of an edge C1 broke, and an integration rejoins both sides.
- Commit: `versionstamp: wire the reader in; the stamp end to end (I1)`
- Reviewer tier: opus.

### T7: the `working` skill names the refusal
- Acceptance: `plugin/skills/working/SKILL.md` gets one sentence, which says:
  - a project command refused with "this project requires fugaro X or newer" means hand the user
    `brew upgrade dimipaun/tap/fugaro` and then `fugaro update` for their own terminal;
  - the agent never runs `brew`, never retries, and never looks for a way around the refusal.

  The `plugin/` tests pass.
- Files: `plugin/skills/working/SKILL.md`.
- Tests: the plugin's command-and-flag tests.
- Command: `cd plugin && go test ./... -count=1`
- Red step: none. This is a doc line, and the plugin tests guard what it names.
- Depends on: I1. The sentence becomes true only then.
- Commit: `plugin: the working skill names the required-CLI refusal (T7)`
- Reviewer tier: opus (a plugin skill).

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for `dimipaun/fugaro`). The repository's
`rigor.default` is `startup`, and no `rigor.areas` are set, so the pass is applied aggressively.

| Level | Tasks |
| --- | --- |
| 0 | C1 |
| 1 | T1, T2a, T3, T4, T5, T6, K1 |
| 2 | T2b, K2, I1 |
| 3 | T7 |

**Critical path.** By these levels, the longest chain is four steps: C1, a level-1 task, I1, T7.
The graph read reports `critical path (4 tasks): C1 -> K1 -> I1 -> T7` with C1 as the bottleneck.
Measured by weight, the chain through T3 (`large`, the fence) is the one to watch, because I1 waits
for every level-1 task. The read reports `gain: 6 broken, 2 kept, 3 added; before 4 tasks, weight
4.5; after 4 tasks, weight 4; improves`.

**Where the graph and this table differ.** T1 builds C1's other side, so the graph gives it no edge
to C1 and shows it ready at once. It is not ready at once: it starts after C1 merges, as its
Depends-on line says.

**Edges broken by C1, each rejoined in I1:**
- T3, T4, T5 and T6 after T1: each needs the `Source` interface and the error types, not the cache
  or the read.
- T2a after T1: the writer needs the node's shape, not the reader.
- K1 after T2a: the script needs the node's shape, not its writer.

**Edges kept:**
- T2b after T2a: behaviour. T2b gates, documents and tests the stage T2a writes.
- K2 after K1: K2 registers the script K1 writes.
- I1 after T2a: behaviour. The end-to-end test needs the stage to write the stamp.
- T7 after I1: the sentence becomes true only then.

The graph also records I1 after T1, T3, T4, T5, T6 and K1, the sides of the edges C1 broke. I1 has
no edge to C1 in the graph, because a task (T7) waits on I1. It reaches C1 through those sides.

**Shared files.** No level holds two writers of one file.

| File | Writers, by level |
| --- | --- |
| `internal/versionstamp/stamp.go`, `stamp_test.go`, `schemas/required-cli.schema.json`, `docs/required-version-stamp/contract.md`, `internal/cli/required_cli_source.go` | C1 (0) only |
| `internal/versionstamp/stub.go` | C1 (0), deleted by I1 (2) |
| `internal/versionstamp/source.go` | T1 (1), I1 (2) |
| `internal/versionstamp/cache.go`, `source_test.go` | T1 (1) |
| `internal/initflow/stage.go`, `internal/cli/init_stages.go`, `internal/cli/setup_refusals_test.go`, `internal/cli/update_version_stamp*.go` | T2a (1) |
| `internal/cli/update_managed.go`, `internal/cli/update_in_flight_test.go`, `docs/design/update-in-flight.md`, `internal/cli/policy_delete_keeps_stamp_test.go` | T2b (2) |
| `internal/cli/root.go`, `internal/cli/project.go`, `internal/cli/required_cli*.go` (but the seam) | T3 (1) |
| `internal/cli/init.go`, `internal/cli/update_stamp_guard*.go` | T4 (1) |
| `internal/cli/doctor.go`, `internal/cli/doctor_required*.go` | T5 (1) |
| `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_required_test.go` | T6 (1) |
| `scripts/live-checks/check-900-*` | K1 (1) |
| `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`, `docs/coordinator/README.md`, `docs/periodic-reviews/README.md` | K2 (2) |
| `internal/cli/required_cli_e2e_test.go`, the release notes file | I1 (2) |
| `plugin/skills/working/SKILL.md` | T7 (3) |

T3, T4 and T6 all add tests to `internal/cli`, each in its own new file. A helper two of them need
lives in C1's seam file, or is written once by whichever merges first and reused by the other: no
duplicate (CLAUDE.md, "search before you write a helper").

## 6. Contract changes anticipated

- **R-stamp-shape** (C1, raised by T2a or T1): if the node needs a field the contract does not carry,
  such as the writer's identity for the refusal sentence. It reaches T3, T5 and K1. A launched task
  is reached by a follow-up run on its pull request that names `internal/versionstamp/stamp.go` by
  path.

## 7. The estimate

As `fugaro plan graph --plan docs/required-version-stamp/implementation-plan.md` printed it:

`ready in 211–879 min, no history yet for 7 tasks (13 tasks; tiers for 6, seeds for 7; from the cached history of 2026-11-22 07:25 UTC)`

It is informational and reserves no budget. It is what the tasks' tiers and this machine's history
predict, and it moves as runs finish. The graph counts the twelve tasks of this plan and the one
contract change of §6, which runs only if raised.

The proposed runs are the twelve tasks (C1, T1, T2a, T2b, T3, T4, T5, T6, K1, K2, I1, T7) and the
independent reviews of C1, T2b and T3. Each needs the owner's go.

## 8. Checks the plan takes

From the reserved block 900–909. Check 900 is used and 901–909 are free.

| Check | What it measures | Who runs it |
| --- | --- | --- |
| **900** | The stamp on a real project after `update`: the node's shape and value, doctor's row, and a cold-cache read under 2 seconds (U1); run as a member, the viewer's read (U2) | The owner (and a member, where one exists), after the release that carries L1 |

## 9. After-deploy steps

`fugaro update`, and nothing else. I1's release-notes entry says so under `### For operators`, and
adds what the update leads to: members and CI pins that select a project, if their CLI is below the
new release, are refused until `brew upgrade`. Nothing a run holds changes.

## 10. Considered and declined

- **The check in `cmd/fugaro/main.go`.** Declined, as the spec declines it: the root hook has the
  resolved command and its flags.
- **The check only in `selectProject`.** Declined. A command that resolves its project another way
  would go unfenced, and fenced-by-default needs the tree walk. R4 adds `selectProject`'s fallback
  as a second call site instead.
- **A `projectpolicy.Version` bump to fence pre-L1 CLIs.** Declined by D3261.
- **Joining `runCovered`.** Declined by R2 (F4).
- **The stage right after `policy`.** Declined by R1: that slot is taken by contract (F3).
