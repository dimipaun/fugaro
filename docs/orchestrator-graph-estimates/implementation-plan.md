# Implementation plan: orchestrator, graphs and estimates

What it builds: [`spec.md`](spec.md) as amended by [`reconciliation.md`](reconciliation.md), across the five modules of the approved capability map (`daemon-release`, `graph-import`, `run-events`, `estimates`, `backlog`).

**Status: plan only. Nothing is implemented, and no task is approved.** Each task below needs the owner's explicit go before a run starts. The answers that shaped it: install the unit everywhere (D2700), a project-policy switch (D2701), the daemon adopts at start and the session imports the plans it names (D2702 as amended), the orchestrator skill teaches the hand-over (D2703), a CLI plan import (D2704), unlock on merge through the existing `review` phase (D2705 as amended), one approval per plan (D2706), the session writes a missing graph (D2707), stream `agents/` and `outcomes/` (D2708), dependencies reported in `result.json` and folded add-only (D2709, D2710), a separate `tier` field (D2711), time seeds only (D2712), ≥ 5 runs and p25–p75 (D2713), all four surfaces (D2714), the backlog as spec-less items (D2715), `docs/backlog/` folded in (D2716), casual adds unapproved (D2717), GitHub only (D2718), mirroring opt-in (D2719), all open issues (D2720, not the recommendation), adopted plans approved once (D2721), and resolutions D2722–D2734.

The decoupling pass is **on** for `dimipaun/fugaro` (`planning.decoupling = true`), and the repository's rigor is `startup` (no area rules), so the pass decouples aggressively. Where a task touches a security path (below), its review tier is opus regardless.

## Conventions every part follows

- `CLAUDE.md` and the path rules: `.claude/rules/credentials-service.md` (B3), `.claude/rules/console.md` (S4), `.claude/rules/ee.md` (no task here imports `ee/`; D2724). Comments explain why. No TODO.
- **Targeted tests only** (`go test ./internal/cli/ -run 'TestX'`); docs changes run `go test ./internal/cli/ -run Docs`.
- **Hermetic tests**: `internal/gcpfake`, `internal/testutil`, the RTDB emulator harness already used by `internal/budget/emulator_orchestrator_test.go`, `httptest` for GitHub. No network, no home directory, no secret-shaped literal.
- **A field never changes meaning.** Every wire change here is additive: `result.json`'s new fields (X2), the task's external id (B5), the plan graph's `tier` (S0), `plan status --json`'s `estimate` block (C3). The orchestrator **state** version moves (X1) and its migration runs at the daemon's start-up, so `fugaro update` stays the whole upgrade (the forge-labels T8 pattern).
- **`fugaro update` is the whole upgrade.** Each pull request that ships behaviour adds its line to the release's `### For operators`: "run `fugaro update`" and what it now does (R2 installs the unit; B2 folds `docs/backlog/`). No operator step that is another command.
- **No prompt.** R2's install asks nothing; in a coding agent's session `update` writes no unit (T8). Nothing here adds a confirmation.
- **Plugin skills are pinned against the CLI.** A skill task that names a command or flag runs after the task that adds it (edges below), and its tests are `plugin/orchestrator_skill_test.go`, `plugin/parallelism_skill_test.go`, `plugin/update_skill_test.go` and `plugin/skillcli_test.go`.

**Boundaries.** *Always:* tests before the commit; one task's files only; the contract's files belong to the contract. *Ask first:* a new dependency, a change to RTDB rules (none is planned: D2709 avoids it), the GitHub App's permissions (U3). *Never:* a live cloud action by an agent; a run that approves a work item; an edit to `.github/workflows/`; a read of an image file by a text-only coder.

## Global constraints and review focus

- **A hostile run cannot widen its own graph's reach** (D2709): a discovered dependency only ever adds an edge in front of an item not yet launched. Pinned by E3's `TestFoldNeverRemovesOrUnblocks`.
- **Approval stays person-only** (D2706, D2717, D2721): `plan_approve` is accepted only from the person class. Pinned by G2's `TestPlanApproveRefusedFromSessionAndDaemon`.
- **Double handling is a no-op** (D2726): pinned by E4.
- **No duplicate on a mirroring loop** (D2728): pinned by B4's `TestMirroredIssueIsNotIngestedBack`.
- **The switch reaches every machine** (D2701): pinned by R3's `TestDisabledPolicyLaunchesNothing`.
- **A policy with the new key stays readable by the release behind it**: the key is written only when a person sets it, and an older CLI refuses an unknown key (strict decode). R1's release note says so, and R1 tests that a policy without the key decodes unchanged.

## Slices

1. **Inert shapes** (level 0): X1, X2, S0, C3, C4, R1, B6. Nothing changes behaviour.
2. **The daemon runs plans** (`graph-import`, `run-events`, part of `daemon-release`): G1, G2, G3, E1, E2, E3, E4, R3, R4, W1. Useful alone: an imported, approved plan unlocks on merge with no agent sweeping.
3. **Release** (`daemon-release`): R2, R5, R6. Turns it on everywhere and teaches the agent.
4. **Estimates**: S1–S6, I2.
5. **Backlog**: B1–B5, I3.

## Tasks

### X1: the orchestrator state shape (graph-import, backlog, run-events)
- Acceptance: `WorkItem` gains an optional `external` string (`github:<owner>/<repo>#<n>`, shape-checked, unique across a project's items: D2728); the patch vocabulary gains `plan_approve` (person class only, naming a plan folder: D2706); the fact kinds gain `graph_suspect` (D2710) and `plan_in_flight` (D2702 as amended); the `review` phase's comment states D2705's meaning; `orchestrator.SchemaVersion` moves by one with a `Migrate` step that rewrites approval and intent digests only where the stored value is the old walk's (as forge-labels T8); both JSON schemas carry the additions.
- Files: `internal/orchestrator/state.go`, `internal/orchestrator/patch_ops.go` (new), `internal/orchestrator/state_test.go`, `schemas/orchestrator-state.schema.json`, `schemas/orchestrator-patch.schema.json`.
- Interfaces: produces the fields and op names G2, E3, B4, B5 use.
- Tests: `TestExternalIDShapeAndUniqueness`, `TestMigrateToCurrentRewritesOnlyOwnDigests`, `TestPlanApproveOpDecodes`, plus the existing `TestWorkItemFieldsAreNotTheRunRecordsFields`.
- Command: `go test ./internal/orchestrator/ -run 'TestExternalID|TestMigrateToCurrent|TestPlanApproveOp|TestWorkItemFields' -count=1 && go test ./schemas/ -count=1`
- Red step: `TestMigrateToCurrentRewritesOnlyOwnDigests` fails against the current `Migrate` (no step for the new version).
- Depends on: none.
- Commit: `feat(orchestrator): state shape for plan approval, external ids and graph facts (X1)`
- Reviewer tier: opus (the approval digest walk is the approval fence).

### X2: the run record's discovered dependencies and conflict paths (run-events)
- Acceptance: `runstore.Record` gains optional `discovered_dependencies` (each: `plan_task` or `path`, and `reason`, at most 16, bounded text) and `conflict` (`paths`, at most 64, and `with_pr`); `schemas/result.schema.json` carries them; a record without them is unchanged.
- Files: `internal/runstore/discovered.go` (new), `internal/runstore/discovered_test.go` (new), `schemas/result.schema.json`. (The `Record` field lives in `discovered.go` as an embedded struct so `runstore.go` is not touched.)
- Interfaces: produces the fields E2 writes and E3 reads.
- Tests: `TestDiscoveredDependenciesBounded`, `TestRecordWithoutDiscoveredIsUnchanged`.
- Command: `go test ./internal/runstore/ -run 'TestDiscovered|TestRecordWithoutDiscovered' -count=1 && go test ./schemas/ -count=1`
- Red step: the bound test fails before the type exists.
- Depends on: none.
- Commit: `feat(runstore): discovered dependencies and conflict paths in the run record (X2)`
- Reviewer tier: standard.

### S0: the graph node's tier field (estimates, graph-import)
- Acceptance: `plan.Node` gains optional `tier` (one of `internal/tier.Tiers`); `validateSchema3` refuses any other value; schema 3 stays schema 3 (D2711, D2734); the eight committed graphs still read.
- Files: `internal/plan/document.go`, `internal/plan/read.go`, `internal/plan/read_test.go`.
- Interfaces: produces `Node.Tier`, read by G1, S2, and shown by R6.
- Tests: `TestNodeTierAcceptsTheFourTiers`, `TestNodeTierRefusesOthers`, `TestCommittedGraphsStillRead`.
- Command: `go test ./internal/plan/ -run 'TestNodeTier|TestCommittedGraphs' -count=1`
- Red step: a graph with `"tier": "small"` is refused today as an unknown field.
- Depends on: none.
- Commit: `feat(plan): an optional tier on graph nodes (S0)`
- Reviewer tier: standard.

### C3: the estimate shape and the history seam (estimates) — contract
- Acceptance: `plan.Estimate` (`ready_in_minutes{low,high}`, `cost_usd{low,high}` or `cost_unknown_tasks`, `basis` per task: `history|tier|seed`, `max_running`, `computed_at`), `plan.History` (`Duration(tier, rigor) (Range, Basis)`, `Cost(tier, rigor) (Range, bool)`), `plan.Range`, the JSON names of the `estimate` block, and a stub `EstimatePlan` returning a fixed plausible estimate, in the stub file only. A contract document says what each side may rely on.
- Files: `internal/plan/estimate_types.go` (new), `internal/plan/estimate_stub.go` (new, stub), `docs/orchestrator-graph-estimates/contract-estimate.md` (new).
- Interfaces: consumed by S2, S3, S4, S5; S1's table satisfies `History` structurally and I2 binds them.
- Tests: `TestEstimateJSONNames` (pins the block's field names).
- Command: `go test ./internal/plan/ -run TestEstimateJSONNames -count=1`
- Red step: the test fails before the types exist.
- Depends on: none.
- Commit: `feat(plan): the estimate contract (C3)`
- Reviewer tier: standard (the strictest consumer is standard).

### C4: the issue adapter interface and its fake (backlog) — contract
- Acceptance: `issuesync.Adapter` (`ListOpen(ctx, repo) ([]Issue, error)`, `Open(ctx, repo, title, body) (Issue, error)`, `Close(ctx, repo, number) error`), `Issue{Repo, Number, Title, Body, State, Labels}`, `ExternalID(Issue) string` (D2728's form), the mirror marker constant, an in-memory `Fake`, and a stub GitHub adapter returning an empty list. A contract document.
- Files: `internal/issuesync/adapter.go` (new), `internal/issuesync/fake.go` (new), `internal/issuesync/github_stub.go` (new, stub), `docs/orchestrator-graph-estimates/contract-adapter.md` (new).
- Interfaces: consumed by B4; B3's client is bound to it by I3.
- Tests: `TestExternalIDForm`, `TestFakeRoundTrip`.
- Command: `go test ./internal/issuesync/ -count=1`
- Red step: the tests fail before the package exists.
- Depends on: none.
- Commit: `feat(issuesync): the issue adapter contract (C4)`
- Reviewer tier: opus, with an independent review (`fugaro review --pr N`) before B4 is launched: it is the seam a credentials-backed adapter plugs into.

### R1: the orchestrator.enabled policy key (daemon-release)
- Acceptance: `orchestrator.enabled` (bool, default true, tighten-only: false wins) in `policy.Orchestrator`, merged by `internal/policy/merge.go`, registered in `internal/settings/registry.go`, readable with `fugaro settings orchestrator.enabled --json`; a policy without the key decodes unchanged.
- Files: `internal/policy/policy.go`, `internal/policy/merge.go`, `internal/policy/orchestrator_merge_test.go`, `internal/settings/registry.go`.
- Interfaces: produces the merged value R3 and R5 read.
- Tests: `TestOrchestratorEnabledTightenOnly`, `TestPolicyWithoutEnabledUnchanged`.
- Command: `go test ./internal/policy/ ./internal/settings/ -run 'TestOrchestratorEnabled|TestPolicyWithoutEnabled|TestRegistry' -count=1`
- Red step: `TestOrchestratorEnabledTightenOnly` fails: no such key.
- Depends on: none.
- Commit: `feat(policy): orchestrator.enabled, the project-wide switch (R1, D2701)`
- Reviewer tier: standard.

### R2: fugaro update installs the orchestrator unit everywhere (daemon-release)
- Acceptance: `updateUnitStage` installs the login unit on a machine with none, with no prompt (D2700), and re-renders an installed one as today; in a coding agent's session it writes nothing and prints its one line (T8); a machine where the unit cannot be installed (no launchd and no systemd user session) prints one line and the update still succeeds; the update skill no longer says update "never installs".
- Files: `internal/cli/update_unit_stage.go`, `internal/cli/update_unit_stage_test.go`, `plugin/skills/update/SKILL.md`, `plugin/update_skill_test.go`.
- Interfaces: reuses `orchestrator_install.go`'s installer as is.
- Tests: `TestUpdateInstallsMissingUnit`, `TestUpdateInAgentSessionWritesNoUnit`, `TestUpdateWithoutServiceManagerStillSucceeds`, `TestUpdateLeavesARunInFlightRunning` (existing, must still pass).
- Command: `go test ./internal/cli/ -run 'TestUpdateInstallsMissingUnit|TestUpdateInAgentSession|TestUpdateWithoutServiceManager|TestUpdateLeavesARunInFlightRunning' -count=1 && go test ./plugin/ -run TestUpdateSkill -count=1`
- Red step: `TestUpdateInstallsMissingUnit` fails against "never installs".
- Depends on: none.
- Commit: `feat(update): install the orchestrator unit where none is (R2, D2700)`
- Reviewer tier: opus (a plugin skill changes).
- Release note: `### For operators`: "run `fugaro update`; it installs the orchestrator daemon on this machine if it is not there, and one machine per project drives runs."

### R3: the daemon honours orchestrator.enabled (daemon-release)
- Acceptance: the daemon reads the merged `orchestrator.enabled` on every caps refresh; while false it launches nothing, records one fact saying so, and keeps folding runs already going; a switch that cannot be read counts as on only if the last read was on (it never flips to on by a failed read).
- Files: `internal/orchestrator/caps.go`, `internal/orchestrator/caps_test.go`, `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_run_test.go`.
- Interfaces: consumes R1.
- Tests: `TestDisabledPolicyLaunchesNothing`, `TestDisabledPolicyStillFoldsRunning`, `TestUnreadableSwitchKeepsLastValue`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestDisabledPolicy|TestUnreadableSwitch' -count=1`
- Red step: `TestDisabledPolicyLaunchesNothing` launches today.
- Depends on: R1 (the key), E1 (one file: `orchestrator_run.go`).
- Commit: `feat(orchestrator): the daemon stops launching when orchestrator.enabled is off (R3)`
- Reviewer tier: standard.

### R4: the daemon adopts runs in flight at start (daemon-release)
- Acceptance: `orchestrator.Adopt` lists runs in flight for the project's repositories (launch record with no final record, live execution), and for each one not already an item's run, writes a `running` item with that run id, keyed `<plan>/<task>` when the launch carries `plan_task` and by run id otherwise, with no approval needed (it is already running); for each plan an adopted run names, it records one `plan_in_flight` fact; a second call adopts nothing (D2702, D2721).
- Files: `internal/orchestrator/adopt.go` (new), `internal/orchestrator/adopt_test.go` (new).
- Interfaces: a function W1 calls at start-up; reads the run view the tick already uses.
- Tests: `TestAdoptWritesRunningItemsOnce`, `TestAdoptKeysByPlanTask`, `TestAdoptRecordsPlanInFlightFact`, `TestAdoptSkipsFinishedRuns`.
- Command: `go test ./internal/orchestrator/ -run TestAdopt -count=1`
- Red step: the tests fail before `Adopt` exists.
- Depends on: none (the fact kind is a string the test asserts; X1 registers it, and W1's end-to-end test proves the two agree).
- Commit: `feat(orchestrator): adopt runs in flight at start-up (R4, D2702)`
- Reviewer tier: standard.

### R5: the orchestrator skill hands work to the daemon, and the cutover how-to (daemon-release, backlog)
- Acceptance: the skill says: while `orchestrator.enabled` is on and `fugaro orchestrator status` shows a live daemon, queue work as items (`work_item_upsert`, or `fugaro orchestrator import --plan <folder>` for a plan) and never launch, sweep or watch runs yourself; at session start, import any plan a `plan_in_flight` fact names; ask the person for `plan_approve`, never write it; "let's fix this" is a `fugaro backlog add` (queued, unapproved: D2717), distinct from `fugaro issue`, which is Fugaro's own product bugs; with the switch off or no daemon, drive runs as before. The cutover how-to becomes "run `fugaro update`" plus the switch (D2729).
- Files: `plugin/skills/orchestrator/SKILL.md`, `plugin/skills/orchestrator/reference/state.md`, `plugin/orchestrator_skill_test.go`, `docs/how-to/orchestrator-cutover.md`, `internal/cli/docs_orchestrator_cutover_test.go`.
- Interfaces: names G1's flag, X1's op, R1's key, B1's behaviour.
- Tests: `TestOrchestratorSkillNamesOnlyRealCommandsAndFlags` (existing), `TestSkillNeverTellsTheSessionToApprove`, `TestSkillSaysStopSweepingWhileDaemonLive`, `TestCutoverIsUpdateOnly` (rewrites the pinned doc test).
- Command: `go test ./plugin/ -run 'TestOrchestratorSkill|TestSkillNever|TestSkillSaysStop|TestSessionStart' -count=1 && go test ./internal/cli/ -run 'Docs|TestCutover' -count=1`
- Red step: `TestSkillSaysStopSweepingWhileDaemonLive` fails on today's skill.
- Depends on: G1 (the flag exists), W1 (plan approval and the review phase behave as taught), R1 (the key), B1 (backlog add writes an item).
- Commit: `docs(plugin): the orchestrator skill hands work to the daemon (R5, D2703, D2729)`
- Reviewer tier: opus, independent review (an agent's instructions).

### R6: working and parallelism point to the daemon; the tier field and the one-off graph (daemon-release, graph-import)
- Acceptance: `working`'s Monitor section and `parallelism`'s job-max section say that while the daemon is live, the orchestrator skill governs launching; `parallelism`'s schema example shows `tier` with a line on its four values and how it differs from `size`; it says a multi-task request with no plan gets a graph written once to the scratchpad, checked with `fugaro plan graph`, and imported with `fugaro orchestrator import --plan` (D2707).
- Files: `plugin/skills/working/SKILL.md`, `plugin/skills/parallelism/SKILL.md`, `plugin/parallelism_skill_test.go`.
- Tests: `TestParallelismExampleGraphReads` (the example parses with `plan.Parse`), `TestParallelismNamesOnlyRealFlags`.
- Command: `go test ./plugin/ -run 'TestParallelism|TestSkillCLI' -count=1`
- Red step: the example with `tier` fails to parse before S0.
- Depends on: S0 (the field), G1 (the flag).
- Commit: `docs(plugin): working and parallelism defer to the daemon; the tier field (R6)`
- Reviewer tier: opus (plugin skills).

### G1: fugaro orchestrator import --plan (graph-import)
- Acceptance: `fugaro orchestrator import --plan <folder>` reads `<folder>/implementation-plan.graph.json` with `plan.ReadFile` and writes one typed patch: one `work_item_upsert` per node, id `<plan>/<node>`, `spec` = plan folder name, `rigor_tier` = node `tier` (D2730), `depends_on` = the graph's edges, title from the node; a re-import changes only items not yet launched and leaves approvals of unchanged nodes valid; a refused graph writes nothing; `--json` prints the patch's result. The existing `-f` session-dump import is unchanged.
- Files: `internal/orchestrator/planimport.go` (new), `internal/orchestrator/planimport_test.go` (new), `internal/cli/orchestrator_import.go`, `internal/cli/orchestrator_import_test.go`.
- Interfaces: consumes `plan.Document` (with S0's `tier`); produces items G2 approves.
- Tests: `TestPlanImportOneItemPerNode`, `TestPlanImportIsIdempotent`, `TestPlanImportLeavesLaunchedItems`, `TestPlanImportRefusedGraphWritesNothing`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestPlanImport|TestOrchestratorImport' -count=1`
- Red step: `TestPlanImportOneItemPerNode` fails: no `--plan`.
- Depends on: S0 (the tier it reads).
- Commit: `feat(orchestrator): import a plan graph as work items (G1, D2704)`
- Reviewer tier: standard.

### G2: the plan_approve patch op (graph-import)
- Acceptance: `plan_approve{spec}` from the person class approves every item with that `spec`, each pinned to its current digest; refused from the session, daemon and decision-agent classes; an item changed later needs approving again; an item already approved is untouched (D2706).
- Files: `internal/orchestrator/patch_plan.go` (new), `internal/orchestrator/patch_plan_test.go` (new).
- Interfaces: consumes X1's op; W1 wires it into the patch dispatch.
- Tests: `TestPlanApproveApprovesEveryNodeOfThePlan`, `TestPlanApproveRefusedFromSessionAndDaemon`, `TestPlanApprovePinsDigests`.
- Command: `go test ./internal/orchestrator/ -run TestPlanApprove -count=1`
- Red step: the tests fail before the op exists.
- Depends on: X1.
- Commit: `feat(orchestrator): approve a whole plan in one person-only op (G2, D2706)`
- Reviewer tier: opus (the approval fence).

### G3: unlock on merge: the review phase and the per-tick merge read (graph-import)
- Acceptance: `foldPhase` takes a succeeded run that left a pull request to `review`, and one with none to `done`; once per tick, per repository, the daemon reads the state of the pull requests of its `review` items in one bulk call through the existing label route's reader (`LabelPRReader` and the route in `internal/credsvc/labelroute.go`), moving merged ones to `done` and closed-unmerged ones to `failed`; a read that fails leaves the item in `review` for the next tick; `dependenciesDone` is unchanged, so a dependent waits for `done` (D2705 as amended, F32).
- Files: `internal/orchestrator/runview.go`, `internal/orchestrator/mergeread.go` (new), `internal/orchestrator/mergeread_test.go` (new), `internal/orchestrator/export_test.go`.
- Interfaces: a `MergeReader` seam in `mergeread.go`; if the route answers "closed" without "merged", the task adds that field to the route's response in the same change and says so (O1), which moves B3's reviewer onto it.
- Tests: `TestSucceededWithPRGoesToReview`, `TestMergedPRMovesToDone`, `TestClosedUnmergedFails`, `TestFailedMergeReadLeavesReview`, `TestDependentWaitsForMerge`.
- Command: `go test ./internal/orchestrator/ -run 'TestSucceededWithPR|TestMergedPR|TestClosedUnmerged|TestFailedMergeRead|TestDependentWaits' -count=1`
- Red step: `TestDependentWaitsForMerge` fails: today the dependent launches on success.
- Depends on: none.
- Commit: `feat(orchestrator): dependents unlock when the pull request merges (G3, D2705)`
- Reviewer tier: opus if the label route's response changes (a credentials-service path), standard otherwise.

### E1: the daemon wakes on agents/ and outcomes/ (run-events)
- Acceptance: `orchestratorDaemon.wait` streams its own node, `agents/<slug>` for each repository the state names, and `outcomes/<today>` (re-opened at the UTC day change); any put or patch after the opening snapshot wakes the tick; an `agents/` entry deleted with no matching `outcomes/` entry also wakes it, and the tick's existing `executionOver` check runs for that run (D2708, D2725); the 30 s ceiling is unchanged; a stream that fails falls back to the ceiling without ending the daemon.
- Files: `internal/orchestrator/wake.go` (new), `internal/orchestrator/wake_test.go` (new), `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_run_test.go`.
- Tests: `TestOutcomeWritesWakeTheTick`, `TestVanishedAgentEntryWakesAndChecks`, `TestOpeningSnapshotDoesNotWake`, `TestStreamFailureFallsBackToCeiling`, `TestDayChangeReopensOutcomes`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestOutcomeWrites|TestVanishedAgent|TestOpeningSnapshot|TestStreamFailure|TestDayChange' -count=1`
- Red step: `TestOutcomeWritesWakeTheTick` times out at the ceiling today.
- Depends on: none.
- Commit: `feat(orchestrator): wake on a run's end, not on the next poll (E1, D2708)`
- Reviewer tier: standard.

### E2: the runner records discovered dependencies and conflict paths (run-events)
- Acceptance: the implement and fix stage prompts tell the agent it may write `.fugaro/discovered.json` (a list of `{plan_task | path, reason}`) when it finds work it needs from another task; the runner reads, bounds and copies it into the record (X2) at the end; a run whose push or merge base conflicts records the conflicting paths and the pull request it conflicts with; a malformed file is dropped with one log line and never fails the run.
- Files: `internal/runner/discovered.go` (new), `internal/runner/discovered_test.go` (new), `internal/runner/recordend.go`, `internal/runner/prompts.go`.
- Tests: `TestDiscoveredFileCopiedIntoRecord`, `TestMalformedDiscoveredDropped`, `TestConflictPathsRecorded`, `TestPromptMentionsDiscoveredFile`.
- Command: `go test -timeout 20m ./internal/runner/ -run 'TestDiscovered|TestMalformedDiscovered|TestConflictPaths|TestPromptMentionsDiscovered' -count=1`
- Red step: `TestDiscoveredFileCopiedIntoRecord` fails before the reader exists.
- Depends on: X2.
- Commit: `feat(runner): record dependencies a run discovers, and conflict paths (E2, D2709)`
- Reviewer tier: opus, independent review (an agent's prompt changes).

### E3: the daemon folds discovered dependencies and conflicts into the graph (run-events)
- Acceptance: when an item's run ends, each `discovered_dependencies` entry that names an item of the same project not yet launched adds an edge in front of that item (a path entry matches an item whose footprint, or whose plan node's footprint, names it); an edge is never removed and an item already launched is never touched; each addition is one audit entry; a `conflict` records a `graph_suspect` fact naming both items, adds the edge from the conflicting item to the merged one, and leaves the re-run to the existing follow-up and escalation ladder (D2709, D2710).
- Files: `internal/orchestrator/fold_discovered.go` (new), `internal/orchestrator/fold_discovered_test.go` (new), `internal/orchestrator/tick.go`.
- Tests: `TestFoldAddsEdgeInFrontOfQueuedItem`, `TestFoldNeverRemovesOrUnblocks`, `TestFoldIgnoresLaunchedItems`, `TestConflictRecordsGraphSuspect`, `TestFoldIsIdempotent`.
- Command: `go test ./internal/orchestrator/ -run 'TestFold|TestConflictRecords' -count=1`
- Red step: `TestFoldAddsEdgeInFrontOfQueuedItem` fails before the fold exists.
- Depends on: X1 (the fact kind), X2 (the fields).
- Commit: `feat(orchestrator): fold discovered dependencies and conflicts, add-only (E3, D2709, D2710)`
- Reviewer tier: opus (a run's output reaches the daemon's graph).

### E4: pin: an event and a poll over one finished run make one decision (run-events)
- Acceptance: a tick run twice over the same finished run (once from a wake, once from the ceiling) produces one launch of its dependent and one saved state; two daemons of the same project racing produce one lease holder and one launch (D2726).
- Files: `internal/orchestrator/tick_double_test.go` (new).
- Tests: `TestEventAndPollMakeOneDecision`, `TestTwoDaemonsOneLaunch`.
- Command: `go test ./internal/orchestrator/ -run 'TestEventAndPoll|TestTwoDaemons' -count=1`
- Red step: none expected (it pins existing behaviour); if either fails, the task stops and reports it rather than fixing.
- Depends on: none.
- Commit: `test(orchestrator): double handling is a no-op (E4, D2726)`
- Reviewer tier: standard.

### S1: run history per tier and rigor, and the time seeds (estimates)
- Acceptance: `estimate.Table` built from run records (`Record.Tier`, `Record.Rigor`, duration from `StartedAt`/`FinishedAt`, cost from `Cost.TotalUSD`) answers, per (tier, rigor), p25/p50/p75 of duration and cost at ≥ 5 runs, else per tier at ≥ 5, else the seed (D2713); ranges widen 1.5× under 20 runs; no cost seed exists (D2712); `tier.SeedMinutes` holds 10/30/90/240 (D2724); runs that did not finish (`infra_error`, `cancelled`) are excluded.
- Files: `internal/estimate/history.go` (new), `internal/estimate/history_test.go` (new), `internal/tier/seed.go` (new), `internal/tier/seed_test.go` (new).
- Tests: `TestTableFallsBackTierThenSeed`, `TestTableWidensUnderTwenty`, `TestNoCostSeed`, `TestExcludesUnfinishedRuns`.
- Command: `go test ./internal/estimate/ ./internal/tier/ -count=1`
- Red step: the tests fail before the package exists.
- Depends on: none.
- Commit: `feat(estimate): history per tier and rigor, and time seeds (S1)`
- Reviewer tier: standard.

### S2: the estimate: weighted critical path, the running cap, cost, recalibration (estimates)
- Acceptance: `plan.EstimatePlan(doc, history, state, maxRunning, rigor, now)` returns D2722's time and D2723's cost: done nodes count their actual, running nodes max(0, estimate − elapsed), the rest their range; a node with no `tier` uses `medium` and is marked `basis: assumed`; a plan whose cost history is missing for N nodes reports `cost_unknown_tasks: N` and no cost range.
- Files: `internal/plan/estimate.go` (new), `internal/plan/estimate_test.go` (new).
- Tests: `TestTimeIsMaxOfPathAndCappedSum`, `TestRunningNodeCountsRemaining`, `TestDoneNodeCountsActual`, `TestMissingCostHistoryMakesUnknown`, `TestUntieredNodeAssumedMedium`.
- Command: `go test ./internal/plan/ -run 'TestTimeIsMax|TestRunningNode|TestDoneNode|TestMissingCost|TestUntiered' -count=1`
- Red step: the tests fail against the stub's fixed figure.
- Depends on: S0 (the tier), C3 (the shape and the seam).
- Commit: `feat(plan): the plan estimate (S2, D2722, D2723)`
- Reviewer tier: standard.

### S3: plan graph and plan status print the estimate (estimates)
- Acceptance: `fugaro plan graph` and `fugaro plan status` print one line, `ready in 40–70 min, $3–$5 (12 tasks; history for 9, seeds for 3)`, and add the `estimate` block to `--json` (additive); with the runs bucket unreachable, `plan graph` uses seeds and says so; `--done` and `plan status`'s run join feed recalibration.
- Files: `internal/cli/plan.go`, `internal/cli/plan_status.go`, `internal/cli/plan_test.go`, `internal/cli/plan_status_test.go`.
- Tests: `TestPlanGraphPrintsEstimateLine`, `TestPlanStatusJSONHasEstimate`, `TestPlanGraphOfflineUsesSeeds`.
- Command: `go test ./internal/cli/ -run 'TestPlanGraphPrintsEstimate|TestPlanStatusJSONHasEstimate|TestPlanGraphOffline' -count=1`
- Red step: `TestPlanStatusJSONHasEstimate` fails: no block.
- Depends on: C3.
- Commit: `feat(cli): plan graph and plan status show the estimate (S3, D2714)`
- Reviewer tier: standard.

### S4: the console graph screen shows the estimate (estimates)
- Acceptance: the console's graph screen reads `estimate` from `plan status --json` and shows the ready-by and cost ranges per plan, "no history yet" where the CLI says so, and nothing when the block is absent (an older CLI).
- Files: `console/src/renderer/graph/model.ts`, `console/src/renderer/screens/graph.ts`, `console/test/unit/graph-estimate.test.ts` (new).
- Tests: `graph-estimate.test.ts`.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 graph-estimate`
- Red step: the test fails: the model has no estimate.
- Depends on: C3.
- Commit: `feat(console): the graph screen shows a plan's estimate (S4)`
- Reviewer tier: standard. Ships in the console stream (`console-v*`).

### S5: orchestrator state --brief shows each plan's ready-by range (estimates)
- Acceptance: `fugaro orchestrator state --brief` adds one line per imported plan with open items (`<spec>: ready in …`) and one for the spec-less backlog, from the items' tiers and phases; `--offline` uses the cached state and seeds.
- Files: `internal/orchestrator/summary.go`, `internal/orchestrator/summary_test.go`, `internal/cli/orchestrator_state.go`.
- Tests: `TestBriefShowsPlanEstimate`, `TestBriefShowsBacklogEstimate`, `TestSessionStartPrintsOneScreen` (existing, still one screen).
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestBriefShows|TestSessionStartPrintsOneScreen' -count=1`
- Red step: `TestBriefShowsPlanEstimate` fails.
- Depends on: C3.
- Commit: `feat(orchestrator): the brief shows each plan's ready-by range (S5)`
- Reviewer tier: standard.

### S6: the spec-to-pr skill quotes the estimate (estimates)
- Acceptance: the skill's plan step tags each node with a `tier`, and its plan and pull-request body quote the line `fugaro plan graph` prints, marked informational and not a reservation.
- Files: `plugin/skills/spec-to-pr/SKILL.md`, `plugin/skills/spec-to-pr/reference/plan.md`, `plugin/spec_to_pr_estimate_test.go` (new).
- Tests: `TestSpecToPRAsksForTierAndEstimate`, `TestSkillCLI` (existing).
- Command: `go test ./plugin/ -run 'TestSpecToPR|TestSkillCLI' -count=1`
- Red step: `TestSpecToPRAsksForTierAndEstimate` fails.
- Depends on: I2 (the figure is real).
- Commit: `docs(plugin): spec-to-pr tags tiers and quotes the estimate (S6)`
- Reviewer tier: opus (plugin skill).

### B1: fugaro backlog add writes a work item (backlog)
- Acceptance: `fugaro backlog add --title … [--repo …] [--brief-file …]` writes a `work_item_upsert` with no `spec`, queued and unapproved (D2717), and prints the item id; `fugaro backlog list` reads spec-less items from the state; `fugaro backlog index` is removed (the Markdown index is history).
- Files: `internal/cli/backlog.go`, `internal/cli/backlog_test.go`, `internal/cli/backlog_add_test.go`.
- Tests: `TestBacklogAddWritesUnapprovedItem`, `TestBacklogListReadsSpecLessItems`.
- Command: `go test ./internal/cli/ -run 'TestBacklog|Docs' -count=1`
- Red step: `TestBacklogAddWritesUnapprovedItem` fails: today it writes a file.
- Depends on: none.
- Commit: `feat(backlog): backlog add writes a work item (B1, D2715, D2716)`
- Reviewer tier: standard.

### B2: fugaro update folds docs/backlog in, once (backlog)
- Acceptance: run in a checkout that has `docs/backlog/` items with `status: open`, `update` writes one item per file (keyed by the file's `id`, so a second update writes nothing) and prints one line with the count; outside a checkout, or in an agent session, it does nothing; `docs/backlog/README.md` and `FORMAT.md` say the folder is history (D2716).
- Files: `internal/cli/update_backlog.go` (new), `internal/cli/update_backlog_test.go` (new), `internal/cli/update.go`, `docs/backlog/README.md`, `docs/backlog/FORMAT.md`.
- Tests: `TestUpdateFoldsOpenBacklogOnce`, `TestUpdateOutsideCheckoutFoldsNothing`, `TestUpdateLeavesARunInFlightRunning` (existing).
- Command: `go test ./internal/cli/ -run 'TestUpdateFolds|TestUpdateOutsideCheckout|TestUpdateLeavesARunInFlightRunning|Docs' -count=1`
- Red step: `TestUpdateFoldsOpenBacklogOnce` fails.
- Depends on: B1 (its item writer).
- Commit: `feat(update): fold docs/backlog into the standing backlog, once (B2, D2716)`
- Reviewer tier: standard.
- Release note: `### For operators`: "run `fugaro update` in a checkout; it moves the open `docs/backlog/` items into the backlog."

### B3: the GitHub issues adapter behind the credentials service (backlog)
- Acceptance: the credentials service gains an issue route (as the label route, D2439): the daemon names a repository and an act (list open, open one, close one); the service mints its own token, performs the act and returns the result; no token leaves it; it refuses a repository outside the installation; `internal/gitprov/github` gains the three REST calls; the route's request and response shapes live in `internal/creds/issueroute.go`.
- Files: `internal/creds/issueroute.go` (new), `internal/credsvc/issueroute.go` (new), `internal/credsvc/issueroute_test.go` (new), `internal/gitprov/github/issues.go` (new), `internal/gitprov/github/issues_test.go` (new).
- Tests: `TestIssueRouteNeverReturnsAToken`, `TestIssueRouteRefusesForeignRepo`, `TestIssueRouteListOpenPaginates`, `TestGitHubIssuesCalls` (httptest).
- Command: `go test ./internal/credsvc/ ./internal/gitprov/github/ -run 'TestIssueRoute|TestGitHubIssues' -count=1`
- Red step: the tests fail before the route exists.
- Depends on: none. U3 must be settled by the owner first: if the GitHub App lacks `issues: write`, the task stops at list-only and says so.
- Commit: `feat(credsvc): the issue route for the GitHub adapter (B3, D2718)`
- Reviewer tier: opus, independent review (credentials service).

### B4: the issue sync: ingest, mirror, dedupe (backlog)
- Acceptance: `issuesync.Sync(ctx, adapter, state, cfg)` returns the patch for one tick: every open issue of each configured repository with no item of its external id becomes a queued, unapproved item (D2720); an issue carrying the mirror marker is never ingested (D2728); with `mirror: true`, each spec-less item with no external id is opened as an issue with the marker and its external id is set (D2719); a closed issue archives its item if not launched; the whole is idempotent.
- Files: `internal/issuesync/sync.go` (new), `internal/issuesync/sync_test.go` (new).
- Tests: `TestIngestOpenIssuesOnce`, `TestMirroredIssueIsNotIngestedBack`, `TestMirrorOffByDefault`, `TestClosedIssueArchivesQueuedItem`, `TestSyncIsIdempotent`.
- Command: `go test ./internal/issuesync/ -run 'TestIngest|TestMirror|TestClosedIssue|TestSyncIs' -count=1`
- Red step: the tests fail before `Sync` exists.
- Depends on: C4 (the interface and fake), X1 (the external field), B6 (the config).
- Commit: `feat(issuesync): ingest, mirror and dedupe issues (B4, D2719, D2720, D2728)`
- Reviewer tier: standard.

### B5: a run for a GitHub item writes Fixes #N (backlog)
- Acceptance: the launcher passes an item's external id to the run (`task.Spec.External`, additive in `schemas/task.schema.json`); a run whose task carries `github:<owner>/<repo>#<n>` for its own repository writes `Fixes #<n>` in the pull request body; a different repository's issue gets `Fixes <owner>/<repo>#<n>` (D2727).
- Files: `internal/runner/prflow.go`, `internal/runner/prflow_test.go`, `internal/task/task.go`, `schemas/task.schema.json`, `internal/cli/orchestrator_launcher.go`.
- Tests: `TestPRBodyFixesOwnRepoIssue`, `TestPRBodyFixesOtherRepoIssue`, `TestTaskWithoutExternalUnchanged`.
- Command: `go test ./internal/runner/ -run 'TestPRBodyFixes' -count=1 && go test ./internal/task/ ./schemas/ -count=1`
- Red step: `TestPRBodyFixesOwnRepoIssue` fails.
- Depends on: X1.
- Commit: `feat(runner): close the item's issue on merge with Fixes #N (B5, D2727)`
- Reviewer tier: standard.

### B6: the adapter's config in the project layer (backlog)
- Acceptance: the project layer accepts `issues: {github: {repos: [...], mirror: false}}` (D2733), strictly decoded, schema-documented, readable with `fugaro settings issues.github --json`.
- Files: `internal/localcfg/issues.go` (new), `internal/localcfg/issues_test.go` (new), `schemas/local-config.schema.json`.
- Tests: `TestIssuesConfigStrict`, `TestMirrorDefaultsFalse`.
- Command: `go test ./internal/localcfg/ -run 'TestIssuesConfig|TestMirrorDefaults' -count=1 && go test ./schemas/ -count=1`
- Red step: the config is refused as an unknown field today.
- Depends on: none.
- Commit: `feat(localcfg): the GitHub issues adapter's config (B6, D2733)`
- Reviewer tier: standard.

### W1: wire plan approval, adoption and discovery into the daemon, end to end (graph-import, daemon-release, run-events)
- Acceptance: the patch dispatch routes `plan_approve` to G2; the daemon calls `Adopt` once at start-up before its first tick; an emulator test imports a three-node plan (A → B, A → C), approves it with one `plan_approve`, finishes A's run with a pull request, sees A in `review`, merges it in the fake, and sees B and C launch on the next wake; a second test adopts a run in flight and meets its plan import without a duplicate item; a third finishes a run with a discovered dependency and sees the edge added.
- Files: `internal/orchestrator/patch.go`, `internal/cli/orchestrator_run.go`, `internal/orchestrator/plan_e2e_test.go` (new).
- Tests: `TestPlanRunsEndToEnd`, `TestAdoptionMeetsImport`, `TestDiscoveredEdgeEndToEnd`.
- Command: `go test ./internal/orchestrator/ -run 'TestPlanRunsEndToEnd|TestAdoptionMeetsImport|TestDiscoveredEdgeEndToEnd' -count=1`
- Red step: `TestPlanRunsEndToEnd` fails before the dispatch routes the op.
- Depends on: G1, G2, G3, R4, E2, E3 (it runs them together), R3 (one file: `orchestrator_run.go`).
- Commit: `feat(orchestrator): plans, adoption and discovery end to end (W1)`
- Reviewer tier: opus (it wires the approval fence).

### I2: wire the estimate's real history and computation (estimates) — integration
- Acceptance: deletes `internal/plan/estimate_stub.go`; binds S1's `estimate.Table` to `plan.History` and S2's `EstimatePlan` behind S3 and S5; an end-to-end test over a fixture runs bucket (gcpfake) prints a range from history for tiers with ≥ 5 runs and from seeds for the rest, and the console fixture from S4 matches the CLI's JSON.
- Files: `internal/plan/estimate_stub.go` (deleted), `internal/cli/plan_estimate_e2e_test.go` (new).
- Tests: `TestPlanEstimateEndToEnd`, `TestNoEstimateStubRemains`.
- Command: `go test ./internal/cli/ ./internal/plan/ -run 'TestPlanEstimateEndToEnd|TestNoEstimateStubRemains' -count=1`
- Red step: `TestNoEstimateStubRemains` fails while the stub exists.
- Depends on: S1, S2, S3, S4, S5.
- Commit: `feat(plan): the real estimate behind plan graph, plan status and the brief (I2)`
- Reviewer tier: standard.

### I3: wire the GitHub adapter into the daemon's tick (backlog) — integration
- Acceptance: deletes `internal/issuesync/github_stub.go`; binds B3's route client as the `issuesync.Adapter`; the tick runs `Sync` once per tick when `issues.github` is configured, with one bulk list per repository; an end-to-end test against an httptest GitHub ingests an issue, mirrors an item (with `mirror: true`), and does not ingest the mirror back.
- Files: `internal/issuesync/github_stub.go` (deleted), `internal/orchestrator/tick.go`, `internal/issuesync/github_e2e_test.go` (new).
- Tests: `TestGitHubSyncEndToEnd`, `TestNoAdapterStubRemains`.
- Command: `go test ./internal/issuesync/ ./internal/orchestrator/ -run 'TestGitHubSyncEndToEnd|TestNoAdapterStubRemains' -count=1`
- Red step: `TestNoAdapterStubRemains` fails while the stub exists.
- Depends on: B3, B4, E3 (one file: `tick.go`).
- Commit: `feat(orchestrator): the GitHub issue sync runs on the daemon's tick (I3)`
- Reviewer tier: opus (it carries the credentials route into the daemon).

## Parallel execution

Read back with `fugaro plan graph --plan docs/orchestrator-graph-estimates/implementation-plan.md`: four levels, no refusal.

| Level | Tasks |
| --- | --- |
| 0 | C3, C4, X1, X2, S0, R1, R2, R4, G3, E1, E4, S1, B1, B3, B6 (15) |
| 1 | R3, G1, G2, E2, E3, S2, S3, S4, S5, B2, B4, B5 (12) |
| 2 | R6, W1, I2, I3 (4) |
| 3 | R5, S6 (2) |

**Critical path** (as the read reports it): C3 → S2 → I2 → S6, four tasks. **Bottleneck:** C3. What must be sequential and why: R3 after E1 and W1 after R3 (one file, `internal/cli/orchestrator_run.go`); I3 after E3 (one file, `internal/orchestrator/tick.go`); the skill tasks R5, R6 and S6 after the commands they name exist, because the plugin tests check every named command and flag against the CLI.

### The dependencies broken and kept

Broken, each by a contract, with its integration:

- S2 → S1, by C3 (I2): the arithmetic needs the history's shape, not the runs-bucket read.
- S3 → S2, by C3 (I2): the CLI prints the estimate's shape; the arithmetic lands beside it.
- S4 → S3, by C3 (I2): the console draws the JSON block's shape, not the CLI's output.
- S5 → S2, by C3 (I2): the brief summarises the estimate's shape.
- B4 → B3, by C4 (I3): the sync runs against the adapter interface and its fake, not the GitHub client.

**The gain**, as the read reports it: 5 edges broken, 25 kept, 4 tasks added; the path before was 5 tasks with weight 8, after it is 4 tasks with weight 7: an improvement.

Kept, with the reason that decided it (behaviour, data or one file):

- R3 → R1: the daemon reads the merged key; R1 is smaller than a contract would be.
- R3 → E1, W1 → R3: one file, `orchestrator_run.go`.
- R5 → G1, W1, R1, B1; R6 → S0, G1; S6 → I2: a skill names a command, flag or behaviour that must exist as written.
- G1 → S0, S2 → S0: the import and the estimate read the node's tier (the field is smaller than a contract).
- G2 → X1, E3 → X1, B4 → X1, B5 → X1: the state shape *with its digest migration* is behaviour, not only an interface; a stub of it would be the task.
- E2 → X2, E3 → X2: the record fields (smaller than a contract).
- B2 → B1: the fold reuses backlog add's item writer.
- B4 → B6: the config (smaller than a contract).
- W1 → G1, G2, G3, R4, E2, E3; I2 → S1…S5; I3 → B3, B4: integration runs the real sides.
- I3 → E3: one file, `tick.go`.

Considered and not broken: G2 → G1 (approval needs only items with a `spec`, which already exists, so there was no edge to break); a contract over X1 (the migration is the work).

### Shared files

No level holds two writers of one file. The files with more than one writer, and their levels:

| File | Writers (level) |
| --- | --- |
| `internal/cli/orchestrator_run.go` | E1 (0), R3 (1), W1 (2) |
| `internal/cli/orchestrator_run_test.go` | E1 (0), R3 (1) |
| `internal/orchestrator/tick.go` | E3 (1), I3 (2) |
| `internal/plan/estimate_stub.go` | C3 (0, creates), I2 (2, deletes) |
| `internal/issuesync/github_stub.go` | C4 (0, creates), I3 (2, deletes) |

A change to any task's Files re-checks this table.

### Contract changes anticipated

- **C3**: a `basis` value beyond `history|tier|seed|assumed`, or a per-node estimate in the JSON. Would reach S3, S4, S5 (and I2).
- **C4**: a `Labels` filter on `ListOpen` if D2720 is later narrowed to labelled issues. Would reach B4 (and I3).

## Proposed runs, not launched

33 runs, one per task. Worst case at the project's per-run cap of $5: **$165**, spread over four levels (at most 15 at once, inside `max_parallel = 20`). C4's independent review (`fugaro review --pr N`) and the independent reviews of R5, E2 and B3 are four more review jobs. **None starts until the owner says yes.** Launch level 0 first; C4's review must finish before B4 starts.

## Checks the plan takes (Checks 700–703, owner-run)

- **Check 700** (R2, R3): on a sandbox installation, `fugaro update` on two machines installs the unit on both; one holds the lease, the other stands by; setting `orchestrator.enabled: false` stops launches on the live one within one tick. **You** run it; no agent runs any step.
- **Check 701** (R4, G3, W1): start a sandbox run by hand with `--plan-task`, then install the daemon; it adopts the run, the session imports its plan, one `plan_approve`, merge the pull request, and the dependent launches.
- **Check 702** (E1): from a run's final record to its dependent's launch, under 10 s on a laptop that is awake; after a sleep, the dependent launches within one ceiling of waking. Settles U1 for the connection count.
- **Check 703** (B3, B4, B5, I3): on the sandbox repository, an open issue becomes an item; with `mirror: true`, an item becomes an issue and is not ingested back; a run for the issue's item opens a pull request with `Fixes #N`, and merging it closes the issue. Settles U3 and U4.

## After-deploy steps

None beyond `fugaro update` (R2's and B2's release notes). If U3 finds the GitHub App lacks `issues: write`, granting it is an owner step that `update` cannot take; B3's pull request then says so in `### For operators`, and the adapter runs list-only until it is granted.
