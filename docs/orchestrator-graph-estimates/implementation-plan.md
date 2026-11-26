# Implementation plan: orchestrator, graphs and estimates

What it builds: [`spec.md`](spec.md) as amended by [`reconciliation.md`](reconciliation.md), across the five modules of the approved capability map (`daemon-release`, `graph-import`, `run-events`, `estimates`, `backlog`).

**Status: plan only. Nothing is implemented yet; the owner approved all tasks on 2026-11-16.** A task whose dependencies are not merged stays held until they are. The answers that shaped it: install the unit everywhere from the owner's terminal, superseding T7 (D2700); a top-level policy switch (D2701, D2739); the daemon adopts at start and the session imports the plans it names (D2702); the orchestrator skill teaches the hand-over (D2703); a CLI plan import into the plan's own node (D2704, D2735, D2737); unlock on merge through the existing `review` phase, on GitHub and Bitbucket (D2705, D2736 as amended by the owner); one approval per plan, pinned to the imported items (D2706, D2738); the session writes a missing graph (D2707); stream `agents/` and `outcomes/` (D2708); dependencies reported in `result.json` and folded add-only (D2709, D2710); a separate `tier` field (D2711); time seeds only (D2712); ≥ 5 runs and p25–p75 (D2713); all four surfaces (D2714); the backlog in its own node (D2715, D2735); `docs/backlog/` folded in (D2716); casual adds unapproved, approved one by one or together (D2717); GitHub only: list, read and open (D2718); mirroring opt-in (D2719); all open issues, as references (D2720, not the recommendation); adopted plans approved once (D2721); a daemon-added edge re-queues its item for approval with a fact (D2740); approving an issue item shows its body (D2741); and resolutions D2722–D2734, D2736–D2739 and D2742–D2747. Several decisions carry an "amended after review" or "corrected after review round 2" note for the owner to confirm (open item O4).

**Decoupling.** The pass is on for `dimipaun/fugaro` (`fugaro settings planning.decoupling --repo dimipaun/fugaro --json` → `true`, the default; rigor `startup` from the published project layer, no area rules). It found two edges that needed only an interface (S2 → S1 and its neighbours, by an estimate contract; B4 → B3, by an adapter contract), but `fugaro plan graph` reported both breaks as **no gain**: the critical path runs through the daemon's store and wiring (by tasks B11 → B3 → B9 → W1 → R5; by weight X0 → X4 → E3 → W1 → R5, 9), not through estimates (weight 7) or the issue route and sync (weight 6). Following the rule that a break with no gain is undone, **this plan breaks no edge**. That is a different fact from the pass being off.

## Conventions every part follows

- `CLAUDE.md` and the path rules: `.claude/rules/credentials-service.md` (B3), `.claude/rules/console.md` (S4), `.claude/rules/ee.md` (no task imports `ee/`; D2724). Comments explain why. No TODO.
- **Targeted tests only** (`go test ./internal/cli/ -run 'TestX'`); a task that changes a document also runs `go test ./internal/cli/ -run Docs`.
- **Hermetic tests**: `internal/gcpfake`, `internal/testutil`, the RTDB emulator harness of `internal/budget/rules/emulator_orchestrator_test.go`, `httptest` for GitHub. No network, no home directory, no secret-shaped literal.
- **A field never changes meaning.** Every wire change is additive: `result.json`'s new fields (X2), the task's `external` (B7, gated by runner version), the plan graph's `tier` (S0), `plan status --json`'s `estimate` block (S3), a top-level policy key (R1). The orchestrator **state** version moves (X1) with its migration at the daemon's start-up, so `fugaro update` stays the whole upgrade (as forge-labels T8).
- **`fugaro update` is the whole upgrade.** A pull request that ships behaviour adds its line to the release's `### For operators`: "run `fugaro update`" and what it now does (R2 installs the unit; B2 folds `docs/backlog/`).
- **No prompt.** R2's install asks nothing; a coding agent's session and `--dry-run` write no unit. Nothing here adds a confirmation.
- **Plugin skills are pinned against the CLI.** A skill task that names a command, flag or behaviour runs after the task that builds it; their tests are `plugin/orchestrator_skill_test.go`, `plugin/parallelism_skill_test.go`, `plugin/update_skill_test.go` and `plugin/skillcli_test.go`.

**Boundaries.** *Always:* tests before the commit; one task's files only. *Ask first:* a new dependency; any RTDB rules change (none is planned: D2709 avoids one, and the new nodes sit under the existing `orchestrator` subtree's rules); the GitHub App's permissions (U3). *Never:* a live cloud action by an agent; a session or a run that approves a work item; an edit to `.github/workflows/`; a read of an image file by a text-only coder.

## Global constraints and review focus

- **No node can be filled from outside** (D2735, D2720): an issue is a reference with a bounded title, and the ingest stops with headroom below the backlog node's caps. Pinned by B4's `TestIssueFloodNeverPassesTheCap` and `TestFloodLeavesRoomForBacklogAdd`, and X0's `TestMainNodeUnaffectedByFullBacklog`.
- **A hostile run cannot widen its own graph's reach** (D2709): a discovered dependency only ever adds an edge in front of an item not yet launched, never a cycle, never past `MaxDependsOn`. Pinned by E3's `TestFoldNeverRemovesOrUnblocks` and `TestFoldRefusesCycle`.
- **Approval stays person-only and scoped** (D2706, D2717, D2721, D2738): `plan_approve` is accepted only from the person class and only for the items and digests it carries. Pinned by G2's `TestPlanApproveRefusedFromSessionAndDaemon` and `TestPlanApproveIgnoresItemsTaggedLater`.
- **Double handling is a no-op** (D2726): pinned by E4.
- **Remote reads stay inside their budget** (D2742): the merge read and the issue sync keep a 60 s cadence per repository whatever wakes the tick, and the merge read `get`s only what the open list misses. Pinned by G3's `TestMergeReadKeepsItsCadence` and B4's `TestSyncKeepsItsCadence`.
- **Caps hold across nodes** (D2743): pinned by X4's `TestCapsCountedAcrossNodes`.
- **A daemon-added edge never keeps a stale approval** (D2740): pinned by E3's `TestAddedEdgeUnapprovesWithFact`.
- **The person sees an issue body before approving it** (D2741): pinned by G4's `TestApproveIssueItemShowsBodyAsData`.
- **No duplicate on a mirroring loop** (D2728): pinned by B4's `TestMirroredIssueIsNotIngestedBack` and `TestMirrorAdoptsItsOwnIssueAfterLostSave`.
- **The switch reaches every machine and stops no run** (D2701, D2739): pinned by R3's `TestDisabledPolicyLaunchesNothing` and R1's `TestOlderReaderToleratesNewTopLevelKeys`.
- **Approved content cannot change under the approval** (D2720, D2737): pinned by G1's `TestPlanImportBriefPinsCommitAndSectionHash` and B9's `TestChangedBodyRequeuesUnapproved`.
- **A marker only the App writes** (D2728): pinned by B4's `TestMarkerFromAnotherAuthorIgnored`.

## Slices

1. **Inert shapes** (level 0): X0, X1, X2, S0, R1, B8, B11. Nothing changes behaviour.
2. **The daemon runs plans** (`graph-import`, `run-events`, part of `daemon-release`): X4, G1, G2, G4, G3, G5, E1, E2, E3, E4, R3, R4, B7, B9, W1. Useful alone: an imported, approved plan unlocks on merge with no agent sweeping.
3. **Release** (`daemon-release`): R2, R7, R8, R5, R6. Turns it on everywhere and teaches the agent.
4. **Estimates**: S1–S6.
5. **Backlog**: B1–B5, B8–B11.

## Tasks

### X0: the orchestrator store's plan and backlog nodes (graph-import, backlog)
- Acceptance: beside `orchestrator/<project>/state`, the store reads and saves `orchestrator/<project>/plans/<plan>` (one per plan, created on first write; `<plan>` shape-checked as a folder name) and `orchestrator/<project>/backlog`, each a `State` with today's caps, each saved by its own ETag under the one project lease; a dependency never crosses nodes (F44); the existing state node and its readers are unchanged (D2735); the RTDB emulator shows a run token and a phone user cannot write the new nodes (they sit under the existing `orchestrator` rules).
- Files: `internal/orchestrator/store.go`, `internal/orchestrator/shard.go` (new), `internal/orchestrator/shard_test.go` (new), `internal/budget/rules/emulator_orchestrator_test.go`.
- Tests: `TestPlanNodeRoundTrip`, `TestBacklogNodeRoundTrip`, `TestSaveOfOneNodeKeepsOthersETags`, `TestRunTokenCannotWritePlanOrBacklogNode` (emulator).
- Command: `go test ./internal/orchestrator/ -run 'TestPlanNode|TestBacklogNode|TestSaveOfOneNode' -count=1 && go test -tags firebase ./internal/budget/rules/ -run TestRunTokenCannotWritePlanOrBacklogNode -count=1`
- Red step: `TestPlanNodeRoundTrip` fails: no such node.
- Depends on: none.
- Commit: `feat(orchestrator): a node per imported plan and one for the backlog (X0, D2735)`
- Reviewer tier: standard.

### X4: the tick plans and counts its caps across all nodes (graph-import, backlog)
- Acceptance: the tick loads every node, plans launches from each, and counts `max_running` and the spawn caps over all of them (D2743); one node over its cap stops launches from that node only, and the others go on.
- Files: `internal/orchestrator/tick.go`, `internal/orchestrator/caps.go`, `internal/orchestrator/launch.go`, `internal/orchestrator/tick_nodes_test.go` (new).
- Tests: `TestCapsCountedAcrossNodes`, `TestMainNodeUnaffectedByFullBacklog`, `TestLaunchesFromEveryNode`.
- Command: `go test ./internal/orchestrator/ -run 'TestCapsCountedAcrossNodes|TestMainNodeUnaffected|TestLaunchesFromEveryNode' -count=1`
- Red step: `TestCapsCountedAcrossNodes` fails: the cap counts one state.
- Depends on: X0 (the nodes).
- Commit: `feat(orchestrator): plan and cap across every node (X4, D2743)`
- Reviewer tier: standard.

### X1: the orchestrator state shape (graph-import, backlog, daemon-release)
- Acceptance: `WorkItem` gains optional `external` (`github:<owner>/<repo>#<n>`, shape-checked, unique across a project's nodes, daemon class only: D2728), `body_sha256` (D2720), `plan_task` (`<plan>/<task>`, as `task.PlanTaskProblem` checks it) and `adopted` (bool, daemon class only, listed explicitly in the digest's bookkeeping fields: D2702); `external`, `body_sha256` and `plan_task` join the digest walk; the `review` phase's comment states D2705's meaning; `graph_suspect` and `plan_in_flight` are documented fact keys (facts are free key/value pairs); `orchestrator.SchemaVersion` moves by one with a `Migrate` step that rewrites approval and intent digests only where the stored value is the old walk's (as forge-labels T8); the state schema carries the additions.
- Files: `internal/orchestrator/state.go`, `internal/orchestrator/validate.go`, `internal/orchestrator/state_test.go`, `schemas/orchestrator-state.schema.json`.
- Tests: `TestExternalIDShapeUniqueAndDaemonOnly`, `TestAdoptedIsDaemonOnlyBookkeeping`, `TestPlanTaskShape`, `TestDigestCoversExternalBodyAndPlanTask`, `TestMigrateToCurrentRewritesOnlyOwnDigests`, `TestWorkItemFieldsAreNotTheRunRecordsFields`.
- Command: `go test ./internal/orchestrator/ -run 'TestExternalID|TestAdoptedIsDaemonOnly|TestPlanTaskShape|TestDigestCovers|TestMigrateToCurrent|TestWorkItemFields' -count=1 && go test ./schemas/ -count=1`
- Red step: `TestMigrateToCurrentRewritesOnlyOwnDigests` fails: no step for the new version.
- Depends on: none.
- Commit: `feat(orchestrator): state shape for plan tasks, adoption and external ids (X1)`
- Reviewer tier: opus (the digest walk is the approval fence).

### X2: the run record's discovered dependencies and conflict paths (run-events)
- Acceptance: `runstore.Record` gains optional `discovered` (`blocks` and `needed`, each a list of `{plan_task | path, reason}`, at most 16 entries, bounded text) and `conflict_paths` (at most 64); `schemas/result.schema.json` carries them; a record without them is unchanged.
- Files: `internal/runstore/runstore.go`, `internal/runstore/discovered.go` (new), `internal/runstore/discovered_test.go` (new), `schemas/result.schema.json`.
- Tests: `TestDiscoveredBounded`, `TestRecordWithoutDiscoveredIsUnchanged`.
- Command: `go test ./internal/runstore/ -run 'TestDiscovered|TestRecordWithoutDiscovered' -count=1 && go test ./schemas/ -count=1`
- Red step: the bound test fails before the type exists.
- Depends on: none.
- Commit: `feat(runstore): discovered dependencies and conflict paths in the run record (X2)`
- Reviewer tier: standard.

### S0: the graph node's tier field (estimates, graph-import)
- Acceptance: `plan.Node` gains optional `tier` (one of `internal/tier.Tiers`); `validateSchema3` refuses any other value; schema 3 stays schema 3 (D2711, D2734); every committed `docs/**/implementation-plan.graph.json` still reads.
- Files: `internal/plan/document.go`, `internal/plan/read.go`, `internal/plan/read_test.go`.
- Tests: `TestNodeTierAcceptsTheFourTiers`, `TestNodeTierRefusesOthers`, `TestCommittedGraphsStillRead`.
- Command: `go test ./internal/plan/ -run 'TestNodeTier|TestCommittedGraphs' -count=1`
- Red step: a graph with `"tier": "small"` is refused today as an unknown field.
- Depends on: none.
- Commit: `feat(plan): an optional tier on graph nodes (S0)`
- Reviewer tier: standard.
- Release note: a plan graph that uses `tier` is refused by the CLI of the release behind (`plan.Parse` decodes strictly); upgrade before reading a newer plan.

### R1: the orchestrator_enabled and issue_sync policy keys (daemon-release, backlog)
- Acceptance: two **top-level** keys of the published policy, read through `projectpolicy`, registered in `internal/settings/registry.go` and `schemas/settings.schema.json`: `orchestrator_enabled` (bool, default true; false wins at any layer; D2739) and `issue_sync` (`{github: {repos: [...], mirror: false}}`, strictly decoded, D2733); both readable with `fugaro settings <key> --json`; a reader of the release behind lists them in `UnknownKeys` and decodes the rest unchanged.
- Files: `internal/projectpolicy/policy.go`, `internal/projectpolicy/issuesync.go` (new), `internal/projectpolicy/policy_test.go`, `internal/settings/registry.go`, `schemas/settings.schema.json`.
- Tests: `TestOrchestratorEnabledFalseWins`, `TestIssueSyncStrictAndMirrorDefaultsFalse`, `TestOlderReaderToleratesNewTopLevelKeys`, `TestPolicyWithoutNewKeysUnchanged`.
- Command: `go test ./internal/projectpolicy/ ./internal/settings/ -run 'TestOrchestratorEnabled|TestIssueSync|TestOlderReader|TestPolicyWithoutNewKeys|TestRegistry' -count=1 && go test ./schemas/ -count=1`
- Red step: `TestOrchestratorEnabledFalseWins` fails: no such key.
- Depends on: none.
- Commit: `feat(policy): orchestrator_enabled and issue_sync (R1, D2701, D2733)`
- Reviewer tier: standard.

### R2: fugaro update installs the orchestrator unit everywhere (daemon-release)
- Acceptance: with a person at the terminal (`PersonAtTheTerminal`, the fence setup's unit stage already uses; nothing is asked), `updateUnitStage` installs the login unit on a machine with none, and re-renders an installed one as today; with no terminal, in a coding agent's session or with `--dry-run`, it writes no unit and prints one line; a machine with no launchd or systemd user session prints one line and the update still succeeds (D2700 as corrected); the user-facing sentences that "`fugaro update` never installs" (in the installer and in setup's unit stage) change to match.
- Files: `internal/cli/update_unit_stage.go`, `internal/cli/update_unit_stage_test.go`, `internal/cli/managed_test.go`, `internal/cli/orchestrator_install.go`, `internal/cli/init_orchestrator_stage.go`.
- Tests: `TestUpdateInstallsMissingUnit` (replacing `TestUpdateNeverInstallsAUnit`), `TestUpdateWithoutTerminalWritesNoUnit`, `TestUpdateInAgentSessionWritesNoUnit`, `TestUpdateDryRunWritesNoUnit`, `TestUpdateWithoutServiceManagerStillSucceeds`, `TestUpdateLeavesARunInFlightRunning` (existing).
- Command: `go test ./internal/cli/ -run 'TestUpdateInstallsMissingUnit|TestUpdateWithoutTerminal|TestUpdateInAgentSession|TestUpdateDryRun|TestUpdateWithoutServiceManager|TestUpdateLeavesARunInFlightRunning|TestManaged|TestOrchestratorInstall|TestInitOrchestrator' -count=1`
- Red step: `TestUpdateInstallsMissingUnit` fails against "never installs".
- Depends on: none.
- Commit: `feat(update): install the orchestrator unit where none is (R2, D2700)`
- Reviewer tier: standard.
- Release note: `### For operators`: "run `fugaro update` at your terminal; it installs the orchestrator daemon on this machine if it is not there, and one machine per project drives runs."

### R8: the stateless-orchestrator plan records that D2700 supersedes T7 (daemon-release)
- Acceptance: `docs/stateless-orchestrator/implementation-plan.md`'s T7/Q2 text gains a dated "superseded by D2700" note keeping the original visible; `docs_stateless_orchestrator_test.go` pins the new sentence instead of "never installs one".
- Files: `docs/stateless-orchestrator/implementation-plan.md`, `internal/cli/docs_stateless_orchestrator_test.go`.
- Tests: `TestDocsStatelessOrchestrator*` (existing, amended).
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: the amended pin fails on today's text.
- Depends on: R2 (the note says what its code does).
- Commit: `docs(stateless-orchestrator): T7 superseded by D2700 (R8)`
- Reviewer tier: standard.

### R7: the update skill and Checks 301–302 say update installs the unit (daemon-release)
- Acceptance: the update skill and `docs/gcp-live-checklist.md`'s Checks 301–302 say `update` installs a missing unit (D2700), and so does the owner-run Check 301's own text: `scripts/live-checks/check301.sh`'s `upgrade` phase reads D2700's boundary on a machine with no unit (an agent-marked run leaves it without one, the person's own installs the first unit and loads it, and either run is started on the check's own standard input rather than the `/dev/null` a background job gets), and its `start` phase names `fugaro update` beside `fugaro orchestrator install` as the door that writes the first one; their pinned tests follow.
- Files: `plugin/skills/update/SKILL.md`, `plugin/update_skill_test.go`, `internal/cli/docs_check301_302_test.go`, `docs/gcp-live-checklist.md`, `scripts/live-checks/check301.sh`, `scripts/live-checks/check301_test.sh`.
- Tests: `TestUpdateSkill*` (existing, amended), `TestDocsCheck301*` and `TestDocsCheck302*` (existing, amended), `scripts/live-checks/check301_test.sh` (existing, amended: the `case_upgrade_boundary*` and `case_start_no_unit` cases).
- Command: `go test ./plugin/ -run TestUpdateSkill -count=1 && go test ./internal/cli/ -run Docs -count=1 && go test ./scripts/ -run TestLiveCheckScripts -count=1`
- Red step: the amended skill test fails on today's sentence, and the amended boundary case fails against the script that reads the boundary T7 kept.
- Depends on: R2 (the docs say what its code does).
- Commit: `docs: update installs the orchestrator unit (R7, D2700)`
- Reviewer tier: opus (a plugin skill).

### R3: the daemon honours orchestrator_enabled (daemon-release)
- Acceptance: the daemon reads the merged `orchestrator_enabled` on every caps refresh; while false it launches nothing, records one fact saying so, and keeps folding runs already going; an unreadable switch keeps the last value read (a failed read never turns it on).
- Files: `internal/orchestrator/caps.go`, `internal/orchestrator/caps_test.go`, `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_run_test.go`.
- Tests: `TestDisabledPolicyLaunchesNothing`, `TestDisabledPolicyStillFoldsRunning`, `TestUnreadableSwitchKeepsLastValue`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestDisabledPolicy|TestUnreadableSwitch' -count=1`
- Red step: `TestDisabledPolicyLaunchesNothing` launches today.
- Depends on: R1 (the key; smaller than a contract would be), E1 (one file: `orchestrator_run.go`), X4 (one file: `caps.go`).
- Commit: `feat(orchestrator): the daemon stops launching when orchestrator_enabled is off (R3)`
- Reviewer tier: standard.

### R4: the daemon adopts runs in flight at start (daemon-release)
- Acceptance: `orchestrator.Adopt(ctx, lister, node)` takes, from a `RunLister` seam, the code-change runs in flight of the project's repositories (no review job, no artifact run), and for each one not already an item's run writes a `running` item whose id is the run id and whose `plan_task` is the launch's, into that plan's node when it has one (F44) and into the main node otherwise (at most 20 there; the rest are named in one fact), with the `adopted` mark (X1) and a daemon-class intent so the run is folded and counted (D2744), and the escalation ladder skips any rung that merges for an adopted item until a person approves it; for each plan an adopted run names, it records one `plan_in_flight` fact; it writes only under the lease; a second call adopts nothing (D2702, D2721).
- Files: `internal/orchestrator/adopt.go` (new), `internal/orchestrator/adopt_test.go` (new), `internal/orchestrator/escalate_launch.go`.
- Interfaces: `RunLister` (W1 builds the real one); W1 calls `Adopt` at start-up.
- Tests: `TestAdoptWritesRunningItemsOnce`, `TestAdoptCarriesPlanTask`, `TestAdoptRecordsPlanInFlightFact`, `TestAdoptSkipsReviewAndArtifactRuns`, `TestAdoptedItemNeverMergesUnapproved`, `TestAdoptRefusedWithoutLease`, `TestAdoptGoesToThePlanNode`, `TestAdoptBoundedInMainNode`, `TestAdoptedRunIsFoldedAndCounted`.
- Command: `go test ./internal/orchestrator/ -run 'TestAdopt|TestEscalat|TestLadder' -count=1`
- Red step: the tests fail before `Adopt` exists.
- Depends on: X1 (the `adopted` mark and `plan_task`), X0 (the plan's node).
- Commit: `feat(orchestrator): adopt runs in flight at start-up (R4, D2702)`
- Reviewer tier: standard.

### R5: the orchestrator skill hands work to the daemon, and the cutover how-to (daemon-release, backlog)
- Acceptance: the skill says: while `orchestrator_enabled` is on and `fugaro orchestrator status` shows a live daemon, queue work as items (`work_item_upsert`, or `fugaro orchestrator import --plan <folder>` for a plan) and never launch, sweep or watch runs yourself; at session start, import any plan a `plan_in_flight` fact names; ask the person for the plan's approval and never write it; "let's fix this" is a `fugaro backlog add` (queued, unapproved: D2717), distinct from `fugaro issue`, which is Fugaro's own product bugs; with the switch off or no daemon, drive runs as before. The cutover how-to becomes "run `fugaro update`" plus the switch (D2729).
- Files: `plugin/skills/orchestrator/SKILL.md`, `plugin/skills/orchestrator/reference/state.md`, `plugin/orchestrator_skill_test.go`, `docs/how-to/orchestrator-cutover.md`, `internal/cli/docs_orchestrator_cutover_test.go`.
- Tests: `TestOrchestratorSkillNamesOnlyRealCommandsAndFlags` (existing), `TestSkillNeverTellsTheSessionToApprove`, `TestSkillSaysStopSweepingWhileDaemonLive`, `TestCutoverIsUpdateOnly`.
- Command: `go test ./plugin/ -run 'TestOrchestratorSkill|TestSkillNever|TestSkillSaysStop|TestSessionStart' -count=1 && go test ./internal/cli/ -run 'Docs|TestCutover' -count=1`
- Red step: `TestSkillSaysStopSweepingWhileDaemonLive` fails on today's skill.
- Depends on: G1 (the flag), G4 (the person's approve), W1 (plan approval, adoption and the review phase behave as taught), R1 (the key), B1 (backlog add writes an item).
- Commit: `docs(plugin): the orchestrator skill hands work to the daemon (R5, D2703, D2729)`
- Reviewer tier: opus, independent review (an agent's instructions).

### R6: working and parallelism point to the daemon; the tier field and the one-off graph (daemon-release, graph-import)
- Acceptance: `working`'s Monitor section and `parallelism`'s job-max section say that while the daemon is live the orchestrator skill governs launching; `parallelism`'s schema example shows `tier`, with a line on its four values and how it differs from `size`; it says a multi-task request with no plan gets a graph written once to the scratchpad, checked with `fugaro plan graph`, and imported with `fugaro orchestrator import --plan` (D2707).
- Files: `plugin/skills/working/SKILL.md`, `plugin/skills/parallelism/SKILL.md`, `plugin/parallelism_skill_test.go`.
- Tests: `TestParallelismExampleGraphReads` (the example parses with `plan.Parse`), `TestParallelismNamesOnlyRealFlags`.
- Command: `go test ./plugin/ -run 'TestParallelism|TestSkillCLI' -count=1`
- Red step: the example with `tier` fails to parse before S0.
- Depends on: S0 (the field), G1 (the flag).
- Commit: `docs(plugin): working and parallelism defer to the daemon; the tier field (R6)`
- Reviewer tier: opus (plugin skills).

### G1: fugaro orchestrator import --plan (graph-import)
- Acceptance: `fugaro orchestrator import --plan <folder>` reads `<folder>/implementation-plan.graph.json` with `plan.ReadFile` and writes one typed patch to the plan's node (X0): one `work_item_upsert` per node with id = the node id, `plan_task` = `<plan>/<node>`, `spec` = the plan folder, `rigor_tier` = the node's `tier` (D2730), `depends_on` = the graph's edges, and a brief per D2737 carrying the plan's commit and the SHA-256 of the task's section; an item whose `plan_task` matches an adopted item's is linked to it rather than duplicated, and every edge naming that node is written against the adopted item's id (D2746); a re-import changes only items not yet launched, and keeps an unchanged section's brief and commit so its approval holds (D2745); a refused graph writes nothing; `--json` prints the result, including the `{id, digest}` list `plan_approve` needs. The existing `-f` session-dump import is unchanged.
- Files: `internal/orchestrator/planimport.go` (new), `internal/orchestrator/planimport_test.go` (new), `internal/cli/orchestrator_import.go`, `internal/cli/orchestrator_import_test.go`.
- Tests: `TestPlanImportOneItemPerNode`, `TestPlanImportBriefPinsCommitAndSectionHash`, `TestPlanImportLinksAdoptedItemAndRewritesEdges`, `TestReimportKeepsCommitOfUnchangedSection`, `TestPlanImportIsIdempotent`, `TestPlanImportLeavesLaunchedItems`, `TestPlanImportRefusedGraphWritesNothing`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestPlanImport|TestOrchestratorImport' -count=1`
- Red step: `TestPlanImportOneItemPerNode` fails: no `--plan`.
- Depends on: S0 (the tier), X0 (the plan's node), X1 (`plan_task`).
- Commit: `feat(orchestrator): import a plan graph as work items (G1, D2704)`
- Reviewer tier: standard.

### G2: the plan_approve op, routed through Apply (graph-import)
- Acceptance: `plan_approve{node, items:[{id, digest}]}` joins `OpKinds` and `Apply`'s dispatch; from the person class it approves exactly the listed items of that node (a plan's, or `backlog`: D2717) at the listed digests; it is refused from the session, daemon and decision-agent classes, for an empty `node` or an empty list; an item whose digest no longer matches is skipped with an audit line; an item tagged with the plan's `spec` later is not approved by it (D2706, D2738); the patch schema carries it.
- Files: `internal/orchestrator/patch.go`, `internal/orchestrator/patch_plan.go` (new), `internal/orchestrator/patch_plan_test.go` (new), `schemas/orchestrator-patch.schema.json`.
- Tests (all through `orchestrator.Apply`): `TestPlanApproveApprovesTheListedItems`, `TestPlanApproveRefusedFromSessionAndDaemon`, `TestPlanApproveIgnoresItemsTaggedLater`, `TestPlanApproveSkipsChangedDigest`, `TestPlanApproveOnBacklogNode`, the existing branch-count test over `OpKinds`.
- Command: `go test ./internal/orchestrator/ -run 'TestPlanApprove|TestApplyOp' -count=1 && go test ./schemas/ -count=1`
- Red step: `TestPlanApproveRefusedFromSessionAndDaemon` fails: `Apply` does not know the op.
- Depends on: X0 (the plan's node).
- Commit: `feat(orchestrator): approve a plan's imported items in one person-only op (G2, D2706)`
- Reviewer tier: opus (the approval fence).

### G4: the CLI's person-class approve, showing an issue item's body (graph-import, backlog)
- Acceptance: the CLI counts `plan_approve` among `personOnlyOps` and sends it through the typed person confirmation `orchestrator_patch.go` already has (no new confirmation step: the fence exists); approving an item with a GitHub `external` reads the issue through the person's own `gh`, read-only, prints the title and body fenced as data with the body's SHA-256, and approves only when that hash is the item's `body_sha256`, refusing otherwise with the fix printed (D2741); with no terminal the existing refusal stands.
- Files: `internal/cli/orchestrator_patch.go`, `internal/cli/orchestrator_patch_test.go`, `internal/cli/orchestrator_approve_issue.go` (new), `internal/cli/orchestrator_approve_issue_test.go` (new).
- Tests: `TestPatchCountsPlanApproveAsPersonOnly`, `TestApproveIssueItemShowsBodyAsData`, `TestApproveIssueItemRefusesChangedBody`, `TestApproveIssueItemMakesNoWrite` (the `gh` call is a read, faked).
- Command: `go test ./internal/cli/ -run 'TestPatchCountsPlanApprove|TestApproveIssueItem' -count=1`
- Red step: `TestPatchCountsPlanApproveAsPersonOnly` fails: the op is unknown to the CLI.
- Depends on: G2 (the op). The issue is read with the person's own `gh`, not through the daemon's route.
- Commit: `feat(cli): approve a plan or an issue item as a person, seeing what is approved (G4, D2741)`
- Reviewer tier: opus (the approval fence).

### G3: unlock on merge: the review phase and the merge read (graph-import)
- Acceptance: `foldPhase` takes a succeeded run that left a pull request (GitHub or Bitbucket) to `review`, and one with none to `done` (D2736 as amended); through a `MergeReader` seam, at most once per 60 s per repository whatever wakes the tick, the daemon lists the repository's open pull requests once and `get`s only the `review` items missing from that list (as `labelsync` does), inside the label route's read budget (D2742); where the host does not support the list (Bitbucket), it `get`s each `review` item instead; a refusal for want of a Bitbucket read consumer records one fact naming the repository and the fix, once per repository and not once per tick, and leaves the items in `review`; moving `merged` to `done` and `closed` to `failed`; a read that fails leaves the item in `review`; `dependenciesDone` is unchanged, so a dependent waits for `done` (D2705).
- Files: `internal/orchestrator/runview.go`, `internal/orchestrator/mergeread.go` (new), `internal/orchestrator/mergeread_test.go` (new), `internal/orchestrator/export_test.go`.
- Interfaces: `MergeReader` (W1 builds the real one over the label route's client); the seam answers whether a host supports the list, and a typed refusal for a missing read consumer.
- Tests: `TestSucceededWithPRGoesToReview`, `TestBitbucketSucceededGoesToReview`, `TestMergedPRMovesToDone`, `TestClosedUnmergedFails`, `TestFailedMergeReadLeavesReview`, `TestDependentWaitsForMerge`, `TestMergeReadKeepsItsCadence`, `TestMergeReadGetsOnlyMissing`, `TestBitbucketMergeReadGetsEachItem`, `TestNoReadConsumerRecordsOneFact`.
- Command: `go test ./internal/orchestrator/ -run 'TestSucceededWithPR|TestBitbucketSucceeded|TestMergedPR|TestClosedUnmerged|TestFailedMergeRead|TestDependentWaits|TestMergeReadKeeps|TestMergeReadGets|TestBitbucketMergeRead|TestNoReadConsumer' -count=1`
- Red step: `TestDependentWaitsForMerge` fails: today the dependent launches on success.
- Depends on: none.
- Commit: `feat(orchestrator): dependents unlock when the pull request merges (G3, D2705)`
- Reviewer tier: standard.

### G5: the merge read on Bitbucket (graph-import)
- Acceptance: the credentials service's label route answers its single pull-request read (`get`) for a Bitbucket repository of the installation, and only that act:
  - **Host.** The route resolves `Repository` against the GitHub inventory (`githubRepository`) and then the Bitbucket inventory (`workspaceOf`); a name both resolve is refused as ambiguous, and a name neither resolves is refused as unknown, as today.
  - **App ID.** A Bitbucket `get` carries no App ID, and one that carries one is refused; the client (`creds.LabelClient`) stops requiring an App ID for a Bitbucket `get`, so an installation with Bitbucket only can call it. GitHub calls are unchanged.
  - **The read.** The service mints a read-only token from the installation's Bitbucket read consumer with `bitbucketRead`'s guards spelled out and pinned: no consumer is a refusal (`ErrReadNoConsumer`), never a fallback; the consumer's workspace must be the repository's (G11); only the read scope allowlist is accepted. The token is cached in memory per workspace until shortly before it expires, never leaves the service, and the read goes through `bitbucket.Provider.PullRequest`, answering `open`, `merged` or `closed` with no labels.
  - **Limits.** List, add and remove on a Bitbucket repository are refused as unsupported. Only the orchestrator class may call; the route's per-repository rate limit applies; every verified call writes one audit line, and a Bitbucket `get`'s line names `BitbucketReadConsumerSecret`, not the App key (`routeAct(LabelPath)` stays the GitHub answer).
  - **Docs.** The doc comments of `creds/labelroute.go` and the header of `credsvc/labelroute.go` say the `get` act serves both hosts; `LabelPath` keeps its name.
  - **Trust.** The orchestrator class becomes a new user of the read consumer, which today serves people and the coordinator; this is recorded in D2736 and B10 says so in SECURITY.md (the token never leaves the service and the answer is a state only).
- Files: `internal/creds/labelroute.go`, `internal/creds/labelclient.go`, `internal/credsvc/labelroute.go`, `internal/credsvc/labelroute_bitbucket.go` (new), `internal/credsvc/labelroute_bitbucket_test.go` (new).
- Tests: `TestLabelGetServesBitbucketState` (merged, declined, superseded, open), `TestBitbucketGetRefusesAnAppID`, `TestLabelClientAllowsBitbucketGetWithoutAppID`, `TestAmbiguousRepositoryRefused`, `TestLabelListAddRemoveRefuseBitbucketAsUnsupported`, `TestBitbucketGetRefusesWithoutReadConsumer`, `TestBitbucketGetRefusesForeignWorkspace`, `TestBitbucketGetRefusesScopeOutsideAllowlist`, `TestBitbucketGetCachesTheMintedToken`, `TestBitbucketGetAuditNamesReadConsumer`, `TestBitbucketLabelGetNeverReturnsAToken`, `TestBitbucketLabelGetRefusesOtherClasses`, `TestBitbucketLabelGetAuditsEveryCall`, `TestRouteActLabelPathUnchanged` (the existing pin in `audit_answer_test.go` still passes).
- Command: `go test ./internal/credsvc/ ./internal/creds/ -run 'TestLabelGetServesBitbucket|TestBitbucketGet|TestLabelClientAllowsBitbucket|TestAmbiguousRepository|TestLabelListAddRemoveRefuseBitbucket|TestBitbucketLabelGet|TestRouteAct' -count=1`
- Red step: `TestLabelGetServesBitbucketState` fails: today `githubRepository` refuses a Bitbucket repository as unknown, and the client refuses a call with no App ID.
- Depends on: none.
- Commit: `feat(credsvc): the label route reads a Bitbucket pull request's state (G5, D2736)`
- Reviewer tier: opus, independent review (`fugaro review --pr N`; credentials service).

### E1: the daemon wakes on agents/ and outcomes/ (run-events)
- Acceptance: `orchestratorDaemon.wait` streams its own nodes, `agents/<slug>` for each repository the state names, and `outcomes/<today>` (re-opened at the UTC day change); after the opening snapshot, an `agents/` entry's creation or deletion and any `outcomes/` write wake the tick, debounced by 2 s, while a heartbeat patch does not (F45); an `agents/` entry deleted with no matching `outcomes/` entry also has the tick's existing `executionOver` check run for that run (D2708, D2725); the 30 s ceiling is unchanged; the remote reads (merge read, issue sync) keep their own cadence and are not driven by wakes (D2742); a stream that fails falls back to the ceiling without ending the daemon.
- Files: `internal/orchestrator/wake.go` (new), `internal/orchestrator/wake_test.go` (new), `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_run_test.go`.
- Tests: `TestOutcomeWritesWakeTheTick`, `TestHeartbeatDoesNotWake`, `TestWakesAreDebounced`, `TestVanishedAgentEntryWakesAndChecks`, `TestOpeningSnapshotDoesNotWake`, `TestStreamFailureFallsBackToCeiling`, `TestDayChangeReopensOutcomes`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestOutcomeWrites|TestHeartbeatDoesNotWake|TestWakesAreDebounced|TestVanishedAgent|TestOpeningSnapshot|TestStreamFailure|TestDayChange' -count=1`
- Red step: `TestOutcomeWritesWakeTheTick` waits for the ceiling today.
- Depends on: X0 (the nodes it streams).
- Commit: `feat(orchestrator): wake on a run's end, not on the next poll (E1, D2708)`
- Reviewer tier: standard.

### E2: the runner records discovered dependencies and conflict paths (run-events)
- Acceptance: the implement and fix stage prompts name a file **outside the checkout**, in the runner's own state directory, where the agent may list the plan tasks or paths that must wait for its work (`blocks`) and those it found it needed (`needed`), each with a reason; the runner reads it at the end, bounds it and copies it into the record (X2); a file of that name inside the checkout, or tracked in git, is ignored; a run whose rebase or push meets a conflict records the conflicting paths; a malformed file is dropped with one log line and never fails the run.
- Files: `internal/runner/discovered.go` (new), `internal/runner/discovered_test.go` (new), `internal/runner/recordend.go`, `internal/runner/prompts.go`.
- Tests: `TestDiscoveredFileCopiedIntoRecord`, `TestDiscoveredFileInCheckoutIgnored`, `TestMalformedDiscoveredDropped`, `TestConflictPathsRecorded`, `TestPromptNamesTheDiscoveredPath`.
- Command: `go test -timeout 20m ./internal/runner/ -run 'TestDiscovered|TestMalformedDiscovered|TestConflictPaths|TestPromptNamesTheDiscovered' -count=1`
- Red step: `TestDiscoveredFileCopiedIntoRecord` fails before the reader exists.
- Depends on: X2.
- Commit: `feat(runner): record dependencies a run discovers, and conflict paths (E2, D2709)`
- Reviewer tier: opus, independent review (an agent's prompt).

### E3: the daemon folds discovered dependencies and conflicts into the graph (run-events)
- Acceptance: the run view projects X2's fields; when an item's run ends, each `blocks` entry naming an item that has not launched, in the run's own plan node (or, for a run with no plan, among its own repository's backlog items), by `plan_task` or a path its footprint names, adds an edge in front of that item; `needed` entries record one `graph_suspect` fact per run; `conflict_paths` matched against the footprints of items merged since the attempt began record a `graph_suspect` fact and add the edge on the item's next attempt, leaving the re-run to the follow-up and escalation ladder; an added edge un-approves its item and records one fact naming the item, the edge and the run (D2740); an edge is never removed, a running attempt is never touched, an addition that would make a cycle, pass `MaxDependsOn` or put a node over its cap is refused with one audit line, and the tick never fails on any of it (D2709, D2710).
- Files: `internal/orchestrator/fold_discovered.go` (new), `internal/orchestrator/fold_discovered_test.go` (new), `internal/orchestrator/tick.go`, `internal/orchestrator/runview.go`.
- Tests: `TestFoldAddsEdgeInFrontOfQueuedItem`, `TestFoldNeverRemovesOrUnblocks`, `TestFoldRefusesCycle`, `TestFoldRefusesPastMaxDependsOn`, `TestFoldIgnoresRunningAttempts`, `TestNeededBecomesGraphSuspect`, `TestConflictEdgeOnNextAttempt`, `TestFoldIsIdempotent`, `TestBlocksStayInTheRunsOwnNode`, `TestOneGraphSuspectFactPerRun`, `TestAddedEdgeUnapprovesWithFact`.
- Command: `go test ./internal/orchestrator/ -run 'TestFold|TestNeededBecomes|TestConflictEdge|TestBlocksStay|TestOneGraphSuspect|TestAddedEdgeUnapproves' -count=1`
- Red step: `TestFoldAddsEdgeInFrontOfQueuedItem` fails before the fold exists.
- Depends on: X1 (the fields), X2 (the record), X4 (one file: `tick.go`), G3 (one file: `runview.go`).
- Commit: `feat(orchestrator): fold discovered dependencies and conflicts, add-only (E3, D2709, D2710)`
- Reviewer tier: opus (a run's output reaches the daemon's graph).

### E4: pin: an event and a poll over one finished run make one decision (run-events)
- Acceptance: a tick run twice over the same finished run (once from a wake, once from the ceiling) produces one state change and at most one launch of its dependent; two daemons of the same project racing produce one lease holder and one launch (D2726).
- Files: `internal/orchestrator/tick_double_test.go` (new).
- Tests: `TestEventAndPollMakeOneDecision`, `TestTwoDaemonsOneLaunch`.
- Command: `go test ./internal/orchestrator/ -run 'TestEventAndPoll|TestTwoDaemons' -count=1`
- Red step: none expected (it pins behaviour E1 and G3 leave in place); if either fails, the task stops and reports it rather than fixing.
- Depends on: E1, G3 (the wake and the unlock it pins).
- Commit: `test(orchestrator): double handling is a no-op (E4, D2726)`
- Reviewer tier: standard.

### S1: run history per tier and rigor, and the time seeds (estimates)
- Acceptance: `estimate.Table`, built from run records (`Record.Tier`, `Record.Rigor`, duration from `StartedAt`/`FinishedAt`, cost from `Cost.TotalUSD`), answers per (tier, rigor) p25/p50/p75 of duration and cost at ≥ 5 runs, else per tier at ≥ 5, else the seed (D2713); ranges widen 1.5× under 20 runs; there is no cost seed (D2712); `tier.SeedMinutes` holds 10/30/90/240 (D2724); `infra_error` and `cancelled` runs are excluded; the table can be written to and read from a local cache file.
- Files: `internal/estimate/history.go` (new), `internal/estimate/history_test.go` (new), `internal/tier/seed.go` (new), `internal/tier/seed_test.go` (new).
- Tests: `TestTableFallsBackTierThenSeed`, `TestTableWidensUnderTwenty`, `TestNoCostSeed`, `TestExcludesUnfinishedRuns`, `TestTableCacheRoundTrip`.
- Command: `go test ./internal/estimate/ ./internal/tier/ -count=1`
- Red step: the tests fail before the package exists.
- Depends on: none.
- Commit: `feat(estimate): history per tier and rigor, and time seeds (S1)`
- Reviewer tier: standard.

### S2: the estimate: weighted critical path, the running cap, cost, recalibration (estimates)
- Acceptance: `plan.EstimatePlan(doc, table, state, maxRunning, rigor, now)` returns D2722's time and D2723's cost in the `plan.Estimate` shape (`ready_in_minutes{low,high}`, `cost_usd{low,high}` or `cost_unknown_tasks`, per-node `basis`: `history|tier|seed|assumed`, `max_running`, `computed_at`); done nodes count their actual, running nodes max(0, estimate − elapsed), the rest their range; a node with no `tier` uses `medium` and is marked `assumed`.
- Files: `internal/plan/estimate.go` (new), `internal/plan/estimate_test.go` (new).
- Tests: `TestTimeIsMaxOfPathAndCappedSum`, `TestRunningNodeCountsRemaining`, `TestDoneNodeCountsActual`, `TestMissingCostHistoryMakesUnknown`, `TestUntieredNodeAssumedMedium`, `TestEstimateJSONNames`.
- Command: `go test ./internal/plan/ -run 'TestTimeIsMax|TestRunningNode|TestDoneNode|TestMissingCost|TestUntiered|TestEstimateJSON' -count=1`
- Red step: the tests fail before the function exists.
- Depends on: S0 (the tier), S1 (the table).
- Commit: `feat(plan): the plan estimate (S2, D2722, D2723)`
- Reviewer tier: standard.

### S3: plan graph and plan status print the estimate (estimates)
- Acceptance: `fugaro plan status` reads history, prints one line (`ready in 40–70 min, $3–$5 (12 tasks; history for 9, seeds for 3)`), adds the `estimate` block to `--json` (additive) and refreshes the local table cache; `fugaro plan graph` stays offline and prints the same line from the cache and the seeds, saying which it used (D2714 as amended); `--done` and `plan status`'s run join feed recalibration.
- Files: `internal/cli/plan.go`, `internal/cli/plan_status.go`, `internal/cli/plan_test.go`, `internal/cli/plan_status_test.go`.
- Tests: `TestPlanStatusPrintsEstimateLine`, `TestPlanStatusJSONHasEstimate`, `TestPlanGraphMakesNoNetworkCall`, `TestPlanGraphUsesCacheThenSeeds`.
- Command: `go test ./internal/cli/ -run 'TestPlanStatusPrintsEstimate|TestPlanStatusJSONHasEstimate|TestPlanGraphMakesNoNetwork|TestPlanGraphUsesCache' -count=1`
- Red step: `TestPlanStatusJSONHasEstimate` fails: no block.
- Depends on: S2.
- Commit: `feat(cli): plan graph and plan status show the estimate (S3, D2714)`
- Reviewer tier: standard.

### S4: the console graph screen shows the estimate (estimates)
- Acceptance: the console's graph screen reads `estimate` from `plan status --json` and shows the ready-by and cost ranges per plan, "no history yet" where the CLI says so, and nothing when the block is absent (an older CLI).
- Files: `console/src/renderer/graph/model.ts`, `console/src/renderer/screens/graph.ts`, `console/test/unit/graph-estimate.test.ts` (new).
- Tests: `graph-estimate.test.ts`.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 graph-estimate`
- Red step: the test fails: the model has no estimate.
- Depends on: S3 (the JSON it reads).
- Commit: `feat(console): the graph screen shows a plan's estimate (S4)`
- Reviewer tier: standard. Ships in the console stream (`console-v*`).

### S5: orchestrator state --brief shows each plan's ready-by range (estimates)
- Acceptance: `fugaro orchestrator state --brief` adds one line per plan node with open items and one for the backlog node, computed from the items' tiers and phases and the local table cache (no runs-bucket read, D1243); an ingested title is printed through `safetext` and fenced as data (D2747); the brief cache's `orchestratorBriefCacheVersion` moves by one.
- Files: `internal/orchestrator/summary.go`, `internal/orchestrator/summary_test.go`, `internal/cli/orchestrator_state.go`.
- Tests: `TestBriefShowsPlanEstimate`, `TestBriefShowsBacklogEstimate`, `TestBriefFencesIssueTitles`, `TestSessionStartPrintMakesNoNetworkCall` (existing), `TestSessionStartPrintsOneScreen` (existing).
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestBriefShows|TestBriefFences|TestSessionStartPrint' -count=1 && go test ./plugin/ -run TestSessionStart -count=1`
- Red step: `TestBriefShowsPlanEstimate` fails.
- Depends on: S2, X0 (the plan and backlog nodes it reads).
- Commit: `feat(orchestrator): the brief shows each plan's ready-by range (S5)`
- Reviewer tier: standard.

### S6: the spec-to-pr skill quotes the estimate (estimates)
- Acceptance: the skill's plan step tags each node with a `tier`, and its plan and pull-request body quote the line `fugaro plan graph` prints, marked informational and not a reservation.
- Files: `plugin/skills/spec-to-pr/SKILL.md`, `plugin/skills/spec-to-pr/reference/plan.md`, `plugin/spec_to_pr_estimate_test.go` (new).
- Tests: `TestSpecToPRAsksForTierAndEstimate`, `TestSkillCLI` (existing).
- Command: `go test ./plugin/ -run 'TestSpecToPR|TestSkillCLI' -count=1`
- Red step: `TestSpecToPRAsksForTierAndEstimate` fails.
- Depends on: S3 (the line exists).
- Commit: `docs(plugin): spec-to-pr tags tiers and quotes the estimate (S6)`
- Reviewer tier: opus (plugin skill).

### B1: fugaro backlog add writes a work item (backlog)
- Acceptance: `fugaro backlog add --title … [--repo …] [--brief-file …]` writes a `work_item_upsert` with no `spec` to the backlog node (X0), queued and unapproved (D2717), and prints the item id; `fugaro backlog list` reads the backlog node and the main node's spec-less items; `fugaro backlog index` keeps rendering `docs/backlog/` (now history, B8).
- Files: `internal/cli/backlog.go`, `internal/cli/backlog_test.go`, `internal/cli/backlog_add_test.go`.
- Tests: `TestBacklogAddWritesUnapprovedItem`, `TestBacklogListReadsBothNodes`.
- Command: `go test ./internal/cli/ -run 'TestBacklog|Docs' -count=1`
- Red step: `TestBacklogAddWritesUnapprovedItem` fails: today it writes a file.
- Depends on: X0 (the backlog node).
- Commit: `feat(backlog): backlog add writes a work item (B1, D2715, D2716)`
- Reviewer tier: standard.

### B2: fugaro update folds docs/backlog in, once (backlog)
- Acceptance: run in a checkout that has `docs/backlog/` items with `status: open`, `update` writes one backlog item per file (keyed by the file's `id`, so a second update writes nothing) and prints one line with the count; outside a checkout, in an agent session or with `--dry-run`, it does nothing (D2716).
- Files: `internal/cli/update_backlog.go` (new), `internal/cli/update_backlog_test.go` (new), `internal/cli/update.go`.
- Tests: `TestUpdateFoldsOpenBacklogOnce`, `TestUpdateOutsideCheckoutFoldsNothing`, `TestUpdateLeavesARunInFlightRunning` (existing).
- Command: `go test ./internal/cli/ -run 'TestUpdateFolds|TestUpdateOutsideCheckout|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestUpdateFoldsOpenBacklogOnce` fails.
- Depends on: B1 (its item writer).
- Commit: `feat(update): fold docs/backlog into the standing backlog, once (B2, D2716)`
- Reviewer tier: standard.
- Release note: `### For operators`: "run `fugaro update` in a checkout; it moves the open `docs/backlog/` items into the backlog."

### B8: docs/backlog becomes history (backlog)
- Acceptance: `backlog.Render`'s header says the folder is history and new items go to `fugaro backlog add`; `docs/backlog/README.md` is regenerated by `fugaro backlog index`, never edited by hand; `FORMAT.md` says the same; the regenerate-and-compare test passes (D2716).
- Files: `internal/backlog/render.go`, `internal/cli/docs_backlog_test.go`, `docs/backlog/README.md`, `docs/backlog/FORMAT.md`.
- Tests: `TestDocsBacklogIndexIsCurrent` (existing), `TestBacklogHeaderSaysHistory`.
- Command: `go test ./internal/backlog/ -count=1 && go test ./internal/cli/ -run 'TestBacklog|Docs' -count=1`
- Red step: `TestBacklogHeaderSaysHistory` fails.
- Depends on: none.
- Commit: `docs(backlog): the folder is history (B8, D2716)`
- Reviewer tier: standard.

### B11: the GitHub issues REST calls (backlog)
- Acceptance: `internal/gitprov/github` gains list open issues (excluding pull requests, paginated), read one issue, and open one issue, against `httptest`.
- Files: `internal/gitprov/github/issues.go` (new), `internal/gitprov/github/issues_test.go` (new).
- Tests: `TestGitHubListIssuesExcludesPullRequests`, `TestGitHubListIssuesPaginates`, `TestGitHubReadIssue`, `TestGitHubOpenIssue`.
- Command: `go test ./internal/gitprov/github/ -run TestGitHub.*Issue -count=1`
- Red step: the tests fail before the calls exist.
- Depends on: none.
- Commit: `feat(github): issue list, read and open (B11)`
- Reviewer tier: standard.

### B3: the GitHub issues route behind the credentials service (backlog)
- Acceptance: the credentials service gains an issue route alongside the label route (D2439's pattern), registered in `server.go`: only the orchestrator class may call it; the daemon names a repository and an act (list open issues, read one, open one); the service mints its own `{issues: write, metadata: read}` token for that repository (not the label route's), performs the act through B11's calls and returns the result; no token leaves it; it refuses a repository outside the installation, every other caller class (job, review, verifier, coordinator, person), and an opened issue's title over 256 or body over 8000 characters; calls are rate-limited per repository as the label route's are; **every** call from a verified caller, served or refused, writes one audit line (C1.13). There is no close: closing is `Fixes #N` (D2727).
- Files: `internal/creds/issueroute.go` (new), `internal/credsvc/issueroute.go` (new), `internal/credsvc/issueroute_test.go` (new), `internal/credsvc/server.go`.
- Tests: `TestIssueRouteNeverReturnsAToken`, `TestIssueRouteRefusesForeignRepo`, `TestIssueRouteRefusesEveryOtherClass`, `TestIssueRouteRefusesOversizedIssue`, `TestIssueRouteAuditsEveryCall`, `TestIssueRouteMintsIssuesScopeOnly`, `TestIssueRouteRateLimitedPerRepo`.
- Command: `go test ./internal/credsvc/ ./internal/creds/ -run 'TestIssueRoute' -count=1`
- Red step: the tests fail before the route exists.
- Depends on: B11 (the calls). U3 was settled by the owner on 2026-11-17: the GitHub App holds `issues: write` and Fugaro requires it, so the route is built whole (no list-only fallback) and the installation is what must hold the write — the issue route's set is declared in `internal/gitprov/github/apptoken.go` (`IssueTokenPermissions`), `RequiredPermissions` unions it in, `fugaro doctor` and `fugaro setup` name a missing grant and its fix, and v0.26.0's `### For operators` carries the owner step (`fugaro update` deploys the route and cannot change the App's permissions).
- Commit: `feat(credsvc): the issue route for the GitHub adapter (B3, D2718)`
- Reviewer tier: opus, independent review (`fugaro review --pr N`; credentials service).

### B10: SECURITY.md and the website's security page describe the issue route (backlog)
- Acceptance: SECURITY.md and `website/content/security.md` describe the issue route as built, and the label route's Bitbucket read (G5), including that the orchestrator class now uses the Bitbucket read consumer for a state-only read: who may call it, its scope, its bounds, its audit, and that it never returns a token (`.claude/rules/credentials-service.md`).
- Files: `SECURITY.md`, `website/content/security.md`.
- Tests: the docs and website tests that pin SECURITY.md.
- Command: `go test ./internal/cli/ -run Docs -count=1 && cd website && npm run check`
- Red step: none; the change is documentation, and the pinned tests must stay green.
- Depends on: B3 and G5 (it describes the routes as built).
- Commit: `docs(security): the credentials service's issue route (B10)`
- Reviewer tier: opus (SECURITY.md).

### B4: the issue sync: the adapter seam, ingest, mirror, dedupe, on the tick (backlog)
- Acceptance: `issuesync.Adapter` (`ListOpen`, `Open`), an in-memory fake, and `issuesync.Sync(ctx, adapter, nodes, cfg)` returning one tick's patch to the backlog node: every open issue of each repository `issue_sync` names with no item of its external id becomes a queued, unapproved item holding the external id, a title of at most 80 characters and the body's SHA-256, never the body (D2720); the marker counts only on an issue the installation's App opened (D2728), and such an issue is never ingested; with `mirror: true`, an **unapproved** spec-less item with no external id first adopts an open App-opened issue already carrying its marker, and only otherwise opens one (D2719); a closed issue archives its item if not launched; the ingest stops with 20 items of headroom below the backlog node's caps, with one fact; the daemon runs `Sync` at most once per 60 s per repository when configured, whatever wakes the tick (D2742), binding the issue route's client from B3; titles pass `safetext` (D2747).
- Files: `internal/issuesync/adapter.go` (new), `internal/issuesync/fake.go` (new), `internal/issuesync/sync.go` (new), `internal/issuesync/sync_test.go` (new), `internal/orchestrator/tick.go`.
- Tests: `TestIngestOpenIssuesOnce`, `TestIngestStoresNoBodyOnlyItsHash`, `TestMarkerFromAnotherAuthorIgnored`, `TestFloodLeavesRoomForBacklogAdd`, `TestIssueFloodNeverPassesTheCap`, `TestMirroredIssueIsNotIngestedBack`, `TestMirrorAdoptsItsOwnIssueAfterLostSave`, `TestMirrorOffByDefault`, `TestClosedIssueArchivesQueuedItem`, `TestSyncIsIdempotent`, `TestSyncKeepsItsCadence`, `TestTitlesPassSafetext`.
- Command: `go test ./internal/issuesync/ ./internal/orchestrator/ -run 'TestIngest|TestIssueFlood|TestMarkerFrom|TestFloodLeaves|TestMirror|TestClosedIssue|TestSyncIs|TestSyncKeeps|TestTitlesPass' -count=1`
- Red step: the tests fail before `Sync` exists.
- Depends on: B3 (the route's client), X0 (the backlog node), X1 (the external field), R1 (the `issue_sync` key; smaller than a contract), E3 (one file: `tick.go`). The adapter-contract break of B4 → B3 was weighed and undone (no gain).
- Commit: `feat(issuesync): ingest, mirror and dedupe issues on the daemon's tick (B4, D2719, D2720, D2728)`
- Reviewer tier: opus (it carries the credentials route into the daemon, and an issue author's text into the state).

### B5: a run for a GitHub item writes Fixes #N (backlog)
- Acceptance: a run whose task carries `external` `github:<owner>/<repo>#<n>` for its own repository writes `Fixes #<n>` in the pull request body; a different repository's issue gets `Fixes <owner>/<repo>#<n>`; a task without it is unchanged (D2727).
- Files: `internal/runner/prflow.go`, `internal/runner/prflow_test.go`.
- Tests: `TestPRBodyFixesOwnRepoIssue`, `TestPRBodyFixesOtherRepoIssue`, `TestTaskWithoutExternalUnchanged`.
- Command: `go test -timeout 20m ./internal/runner/ -run 'TestPRBodyFixes|TestTaskWithoutExternal' -count=1`
- Red step: `TestPRBodyFixesOwnRepoIssue` fails.
- Depends on: B7 (the field on the task).
- Commit: `feat(runner): close the item's issue on merge with Fixes #N (B5, D2727)`
- Reviewer tier: standard.

### B7: the launcher passes plan_task and the external id (graph-import, backlog)
- Acceptance: the daemon's launcher sets the run's `plan_task` from the item's (so `plan status` joins daemon runs to their nodes and the `--plan-task` duplicate refusal sees them, F38) and `task.Spec.External` from the item's `external` (additive in `schemas/task.schema.json`); `External` is written only to a runner whose release decodes it, gated in `internal/cli/recipes_skew.go` like `titlesSince`, and dropped with one line for an older image.
- Files: `internal/task/task.go`, `schemas/task.schema.json`, `internal/cli/orchestrator_launcher.go`, `internal/cli/orchestrator_launcher_test.go`, `internal/cli/recipes_skew.go`.
- Tests: `TestLauncherPassesPlanTask`, `TestLauncherPassesExternal`, `TestExternalDroppedForOlderRunner`, `TestTaskWithoutExternalDecodes`.
- Command: `go test ./internal/cli/ ./internal/task/ -run 'TestLauncherPasses|TestExternalDropped|TestTaskWithoutExternal|TestRecipesSkew' -count=1 && go test ./schemas/ -count=1`
- Red step: `TestLauncherPassesPlanTask` fails: no plan task today.
- Depends on: X1 (the item's fields).
- Commit: `feat(orchestrator): daemon launches carry plan_task and the external id (B7)`
- Reviewer tier: standard.

### B9: an issue item's brief at launch, pinned to the approved body (backlog)
- Acceptance: when the daemon launches an item with a GitHub `external`, the launcher reads the issue through the route's read act and builds the brief from its title and body; if the body's SHA-256 differs from the item's `body_sha256`, nothing launches and the item goes back to queued and unapproved with one fact (D2720); the body is bounded and passed as data, never as instructions to the launcher.
- Files: `internal/cli/orchestrator_launcher.go`, `internal/cli/orchestrator_launcher_issue.go` (new), `internal/cli/orchestrator_launcher_issue_test.go` (new).
- Tests: `TestIssueItemBriefFromBody`, `TestChangedBodyRequeuesUnapproved`, `TestIssueBodyBounded`.
- Command: `go test ./internal/cli/ -run 'TestIssueItemBrief|TestChangedBody|TestIssueBodyBounded' -count=1`
- Red step: `TestIssueItemBriefFromBody` fails: the brief is empty today.
- Depends on: B3 (the read act), X1 (`body_sha256`), B7 (one file: `orchestrator_launcher.go`).
- Commit: `feat(orchestrator): an issue item's brief, pinned to the approved body (B9, D2720)`
- Reviewer tier: opus (an issue author's text reaches a run's brief).

### W1: wire plan approval, adoption and discovery into the daemon, end to end (graph-import, daemon-release, run-events)
- Acceptance: the CLI builds the real `MergeReader` over the label route's client (GitHub and, through G5, Bitbucket) and the real `RunLister` over the runs bucket (launch records with no final record, and live executions); the daemon calls `Adopt` once at start-up, under the lease, before its first tick; an emulator test imports a three-node plan (A → B, A → C), approves it with one `plan_approve` through the CLI's person path, finishes A's run with a pull request, sees A in `review`, merges it in the fake, and sees B and C launch on the next wake; a second test adopts a run in flight and meets its plan import without a duplicate item; a third finishes a run with a `blocks` entry and sees the edge added.
- Files: `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_mergeread.go` (new), `internal/cli/orchestrator_runlister.go` (new), `internal/orchestrator/plan_e2e_test.go` (new).
- Tests: `TestPlanRunsEndToEnd`, `TestAdoptionMeetsImport`, `TestDiscoveredEdgeEndToEnd`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestPlanRunsEndToEnd|TestAdoptionMeetsImport|TestDiscoveredEdgeEndToEnd' -count=1`
- Red step: `TestPlanRunsEndToEnd` fails before the real merge reader and run lister exist.
- Depends on: G1, G2, G4, G3, G5, R4, E2, E3, B7, B9 (it runs them together), R3 (one file: `orchestrator_run.go`).
- Commit: `feat(orchestrator): plans, adoption and discovery end to end (W1)`
- Reviewer tier: opus (it wires the approval fence and a credentials-service reader).

## Parallel execution

Read back with `fugaro plan graph --plan docs/orchestrator-graph-estimates/implementation-plan.md`: 39 tasks, five levels, no refusal.

| Level | Tasks |
| --- | --- |
| 0 | X0, X1, X2, S0, R1, R2, G3, G5, S1, B8, B11 (11) |
| 1 | X4, R4, R7, R8, G1, G2, E1, E2, S2, B1, B3, B7 (12) |
| 2 | R3, R6, G4, E3, E4, S3, S5, B2, B5, B9, B10 (11) |
| 3 | W1, S4, S6, B4 (4) |
| 4 | R5 (1) |

**Critical path** (as the read reports it, by tasks): B11 → B3 → B9 → W1 → R5, five tasks; **bottleneck:** B11. By weight the longest chain is X0 → X4 → E3 → W1 → R5 (9). What must be sequential and why: X4 after X0 (it plans over X0's nodes); E1 after X0; R3 after E1 and X4, W1 after R3 (one file each: `orchestrator_run.go`, `caps.go`); E3 after X4 and G3, B4 after E3 (one file each: `tick.go`, `runview.go`); B9 after B7 (one file, `orchestrator_launcher.go`); the skill and doc tasks R5, R6, R7, R8, B10 and S6 after the code they describe, because the plugin, docs and website tests check every named command, flag and sentence.

### The dependencies broken and kept

**Broken: none.** Two breaks were drawn and weighed:

- S2 → S1 (and S3 → S2, S4 → S3, S5 → S2) by an estimate contract: the dependents needed only the estimate's shape. `fugaro plan graph` reported the break as no gain (the weighted path stays at 9), because the estimate chain is off the critical path; undone.
- B4 → B3 by an adapter contract: the sync needed only the adapter interface. Off the critical path as well; undone.

Kept, with the reason that decided it:

- R3 → R1, B4 → R1, E2 → X2: the dependent reads a field or key the other adds, and a contract would be the same size as the task.
- R3 → E1, R3 → X4, W1 → R3, E3 → X4, E3 → G3, B4 → E3, B9 → B7: one file.
- X4 → X0, G1 → X0, G2 → X0, B1 → X0, R4 → X0, E1 → X0, S5 → X0: the write or read lands on a node X0 builds (behaviour, not a shape).
- G1 → X1, E3 → X1, B7 → X1, R4 → X1, B9 → X1, B4 → X1: the state shape **with its digest migration** is behaviour; a stub of it would be the task.
- G4 → G2: the CLI sends the op G2 applies.
- G1 → S0, S2 → S0, R6 → S0: the tier field (smaller than a contract).
- S2 → S1, S3 → S2, S5 → S2, S4 → S3, S6 → S3: kept after the no-gain break was undone (above).
- B4 → B3, B9 → B3, B3 → B11: likewise for B4; B9 reads one issue through B3's route; the route calls B11's client.
- B2 → B1: the fold reuses backlog add's item writer.
- B5 → B7: the body reads the field the launcher passes.
- E4 → E1, G3: it pins their behaviour.
- R7 → R2, R8 → R2, B10 → B3, B10 → G5; R5 → G1, G4, W1, R1, B1; R6 → G1: a document or skill names a command, flag, route or behaviour that must exist as written.
- W1 → G1, G2, G4, G3, G5, R4, E2, E3, B7, B9: the end-to-end wiring runs the real sides.

### Shared files

No level holds two writers of one file. The files with more than one writer, and their levels:

| File | Writers (level) |
| --- | --- |
| `internal/cli/orchestrator_run.go` | E1 (1), R3 (2), W1 (3) |
| `internal/cli/orchestrator_run_test.go` | E1 (1), R3 (2) |
| `internal/cli/orchestrator_launcher.go` | B7 (1), B9 (2) |
| `internal/orchestrator/tick.go` | X4 (1), E3 (2), B4 (3) |
| `internal/orchestrator/caps.go` | X4 (1), R3 (2) |
| `internal/orchestrator/runview.go` | G3 (0), E3 (2) |

A change to any task's Files re-checks this table.

### Contract changes anticipated

None: the plan holds no contract.

## Proposed runs, not launched

39 runs, one per task. Worst case at the project's per-run cap of $5: **$195**, over five levels (at most 12 at once, inside `max_parallel = 20`). The independent reviews of R5, E2, B3 and G5 (`fugaro review --pr N`) are four more review jobs. **None starts until the owner says yes.** Level 0 first.

## Checks the plan takes (Checks 700–703, owner-run)

- **Check 700** (R2, R3): on a sandbox installation, `fugaro update` at the terminal on two machines installs the unit on both; one holds the lease, the other stands by; setting `orchestrator_enabled: false` stops launches on the live one within one tick, and a run already going finishes; then leave one machine on the release behind holding the lease after the other has moved the state version, and read whether launches stall (U5). **You** run it; no agent runs any step.
- **Check 701** (R4, G3, G5, W1, B7): start a sandbox run by hand with `--plan-task`, then let `update` install the daemon; it adopts the run, the session imports its plan, one `plan_approve`, merge the pull request, and the dependent launches; repeat the merge step on the sandbox Bitbucket repository.
- **Check 702** (E1): from a run's final record to its dependent's launch, under 10 s on a laptop that is awake; after a sleep, within one ceiling of waking. Read the RTDB plan's connection limit first (U1).
- **Check 703** (B3, B4, B5, B9, G4): on the sandbox repository, an open issue becomes a backlog item; with `mirror: true`, an item becomes an issue and is not ingested back; a run for the issue's item opens a pull request with `Fixes #N`, and merging it closes the issue. Settles U4; **U3 is settled already** (the owner, 2026-11-17: the App holds `issues: write` and Fugaro requires it), and this check then reads the route against a sandbox installation that has accepted the grant.

## After-deploy steps

None beyond `fugaro update` (R2's and B2's release notes). Granting the GitHub App `issues: write` — which U3's ruling of 2026-11-17 requires, and which the installation must accept — is an owner step `update` cannot take: the release that ships B3 carries it in its `### For operators`, `fugaro doctor` reports the installation as lacking `Issues: Write` with the exact fix until it is accepted, and `fugaro setup` holds a GitHub repository's first billable image build the same way.
