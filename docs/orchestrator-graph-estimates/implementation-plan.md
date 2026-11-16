# Implementation plan: orchestrator, graphs and estimates

What it builds: [`spec.md`](spec.md) as amended by [`reconciliation.md`](reconciliation.md), across the five modules of the approved capability map (`daemon-release`, `graph-import`, `run-events`, `estimates`, `backlog`).

**Status: plan only. Nothing is implemented, and no task is approved.** Each task below needs the owner's explicit go before a run starts. The answers that shaped it: install the unit everywhere, superseding T7 (D2700); a top-level policy switch (D2701, D2739); the daemon adopts at start and the session imports the plans it names (D2702); the orchestrator skill teaches the hand-over (D2703); a CLI plan import into the plan's own node (D2704, D2735, D2737); unlock on merge through the existing `review` phase, GitHub only (D2705, D2736); one approval per plan, pinned to the imported items (D2706, D2738); the session writes a missing graph (D2707); stream `agents/` and `outcomes/` (D2708); dependencies reported in `result.json` and folded add-only (D2709, D2710); a separate `tier` field (D2711); time seeds only (D2712); ≥ 5 runs and p25–p75 (D2713); all four surfaces (D2714); the backlog in its own node (D2715, D2735); `docs/backlog/` folded in (D2716); casual adds unapproved (D2717); GitHub only, list and open (D2718); mirroring opt-in (D2719); all open issues, as references (D2720, not the recommendation); adopted plans approved once (D2721); and resolutions D2722–D2734 and D2736–D2739. Twelve decisions carry an "amended after review" note for the owner to confirm (open item O4).

**Decoupling.** The pass is on for `dimipaun/fugaro` (`fugaro settings planning.decoupling --repo dimipaun/fugaro --json` → `true`, the default; rigor `startup` from the published project layer, no area rules). It found two edges that needed only an interface (S2 → S1 and its neighbours, by an estimate contract; B4 → B3, by an adapter contract), but `fugaro plan graph` reported both breaks as **no gain**: the critical path runs through the daemon's wiring (E1 → R3 → W1 → R5), not through estimates or the backlog. Following the rule that a break with no gain is undone, **this plan breaks no edge**. That is a different fact from the pass being off.

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

- **No node can be filled from outside** (D2735, D2720): an issue is a reference with a bounded title, and an ingest that would pass the backlog node's caps stops for that tick. Pinned by B4's `TestIssueFloodNeverPassesTheCap` and X0's `TestMainNodeUnaffectedByFullBacklog`.
- **A hostile run cannot widen its own graph's reach** (D2709): a discovered dependency only ever adds an edge in front of an item not yet launched, never a cycle, never past `MaxDependsOn`. Pinned by E3's `TestFoldNeverRemovesOrUnblocks` and `TestFoldRefusesCycle`.
- **Approval stays person-only and scoped** (D2706, D2717, D2721, D2738): `plan_approve` is accepted only from the person class and only for the items and digests it carries. Pinned by G2's `TestPlanApproveRefusedFromSessionAndDaemon` and `TestPlanApproveIgnoresItemsTaggedLater`.
- **Double handling is a no-op** (D2726): pinned by E4.
- **No duplicate on a mirroring loop** (D2728): pinned by B4's `TestMirroredIssueIsNotIngestedBack` and `TestMirrorAdoptsItsOwnIssueAfterLostSave`.
- **The switch reaches every machine and stops no run** (D2701, D2739): pinned by R3's `TestDisabledPolicyLaunchesNothing` and R1's `TestOlderReaderToleratesOrchestratorEnabled`.

## Slices

1. **Inert shapes** (level 0): X0, X1, X2, S0, R1, B6, B8. Nothing changes behaviour.
2. **The daemon runs plans** (`graph-import`, `run-events`, part of `daemon-release`): G1, G2, G3, E1, E2, E3, E4, R3, R4, B7, W1. Useful alone: an imported, approved plan unlocks on merge with no agent sweeping.
3. **Release** (`daemon-release`): R2, R7, R5, R6. Turns it on everywhere and teaches the agent.
4. **Estimates**: S1–S6.
5. **Backlog**: B1–B5.

## Tasks

### X0: the orchestrator store's plan and backlog nodes (graph-import, backlog)
- Acceptance: beside `orchestrator/<project>/state`, the store reads and saves `orchestrator/<project>/plans/<plan>` (one per imported plan; `<plan>` shape-checked as a folder name) and `orchestrator/<project>/backlog`, each a `State` with today's caps, each saved by its own ETag under the one project lease; the tick plans across all nodes (dependencies may cross nodes, by node-qualified id) and one node over its cap stops launches from that node only; the existing state node and its readers are unchanged (D2735).
- Files: `internal/orchestrator/store.go`, `internal/orchestrator/shard.go` (new), `internal/orchestrator/shard_test.go` (new), `internal/orchestrator/tick.go`.
- Tests: `TestPlanNodeRoundTrip`, `TestBacklogNodeRoundTrip`, `TestCrossNodeDependency`, `TestMainNodeUnaffectedByFullBacklog`, `TestSaveOfOneNodeKeepsOthersETags`.
- Command: `go test ./internal/orchestrator/ -run 'TestPlanNode|TestBacklogNode|TestCrossNode|TestMainNodeUnaffected|TestSaveOfOneNode' -count=1`
- Red step: `TestPlanNodeRoundTrip` fails: no such node.
- Depends on: none.
- Commit: `feat(orchestrator): a node per imported plan and one for the backlog (X0, D2735)`
- Reviewer tier: standard.

### X1: the orchestrator state shape (graph-import, backlog)
- Acceptance: `WorkItem` gains optional `external` (`github:<owner>/<repo>#<n>`, shape-checked, unique across a project's nodes: D2728) and `plan_task` (`<plan>/<task>`, as `task.PlanTaskProblem` checks it); the op vocabulary gains `plan_approve{plan, items:[{id, digest}]}`, person class only, a non-empty `plan` and at least one item (D2706, D2738); the `review` phase's comment states D2705's meaning; `graph_suspect` and `plan_in_flight` are documented fact keys (facts are free key/value pairs, so nothing is registered); `orchestrator.SchemaVersion` moves by one with a `Migrate` step that rewrites approval and intent digests only where the stored value is the old walk's (as forge-labels T8); both JSON schemas carry the additions.
- Files: `internal/orchestrator/state.go`, `internal/orchestrator/patch.go`, `internal/orchestrator/validate.go`, `internal/orchestrator/state_test.go`, `schemas/orchestrator-state.schema.json`, `schemas/orchestrator-patch.schema.json`.
- Tests: `TestExternalIDShapeAndUniqueness`, `TestPlanTaskShape`, `TestMigrateToCurrentRewritesOnlyOwnDigests`, `TestPlanApproveOpDecodesAndValidates`, the existing branch-count test over `OpKinds`, `TestWorkItemFieldsAreNotTheRunRecordsFields`.
- Command: `go test ./internal/orchestrator/ -run 'TestExternalID|TestPlanTaskShape|TestMigrateToCurrent|TestPlanApproveOp|TestWorkItemFields|TestApplyOp' -count=1 && go test ./schemas/ -count=1`
- Red step: `TestMigrateToCurrentRewritesOnlyOwnDigests` fails: no step for the new version.
- Depends on: none.
- Commit: `feat(orchestrator): state shape for plan approval, plan tasks and external ids (X1)`
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

### R1: the orchestrator_enabled policy key (daemon-release)
- Acceptance: `orchestrator_enabled` (bool, default true; false wins at any layer) is a **top-level** key of the published policy (D2739), read through `projectpolicy`, registered in `internal/settings/registry.go` and `schemas/settings.schema.json`, readable with `fugaro settings orchestrator_enabled --json`; a reader of the release behind lists it in `UnknownKeys` and decodes the rest unchanged.
- Files: `internal/projectpolicy/policy.go`, `internal/projectpolicy/policy_test.go`, `internal/settings/registry.go`, `schemas/settings.schema.json`.
- Tests: `TestOrchestratorEnabledFalseWins`, `TestOlderReaderToleratesOrchestratorEnabled`, `TestPolicyWithoutEnabledUnchanged`.
- Command: `go test ./internal/projectpolicy/ ./internal/settings/ -run 'TestOrchestratorEnabled|TestOlderReader|TestPolicyWithoutEnabled|TestRegistry' -count=1 && go test ./schemas/ -count=1`
- Red step: `TestOrchestratorEnabledFalseWins` fails: no such key.
- Depends on: none.
- Commit: `feat(policy): orchestrator_enabled, the project-wide switch (R1, D2701)`
- Reviewer tier: standard.

### R2: fugaro update installs the orchestrator unit everywhere (daemon-release)
- Acceptance: `updateUnitStage` installs the login unit on a machine with none, with no prompt, including with no terminal, `--json` or a pipe; re-renders an installed one as today; writes nothing in a coding agent's session or with `--dry-run`; a machine with no launchd or systemd user session prints one line and the update still succeeds (D2700). The stateless-orchestrator plan's T7/Q2 text gains a dated "superseded by D2700" note, and the tests that pin "never installs" are rewritten to pin the new rule.
- Files: `internal/cli/update_unit_stage.go`, `internal/cli/update_unit_stage_test.go`, `internal/cli/managed_test.go`, `internal/cli/docs_stateless_orchestrator_test.go`, `docs/stateless-orchestrator/implementation-plan.md`.
- Tests: `TestUpdateInstallsMissingUnit` (replacing `TestUpdateNeverInstallsAUnit`), `TestUpdateInAgentSessionWritesNoUnit`, `TestUpdateDryRunWritesNoUnit`, `TestUpdateWithoutServiceManagerStillSucceeds`, `TestUpdateLeavesARunInFlightRunning` (existing).
- Command: `go test ./internal/cli/ -run 'TestUpdateInstallsMissingUnit|TestUpdateInAgentSession|TestUpdateDryRun|TestUpdateWithoutServiceManager|TestUpdateLeavesARunInFlightRunning|TestManaged|Docs' -count=1`
- Red step: `TestUpdateInstallsMissingUnit` fails against "never installs".
- Depends on: none.
- Commit: `feat(update): install the orchestrator unit where none is (R2, D2700)`
- Reviewer tier: standard.
- Release note: `### For operators`: "run `fugaro update`; it installs the orchestrator daemon on this machine if it is not there, and one machine per project drives runs."

### R7: the update skill and Checks 301–302 say update installs the unit (daemon-release)
- Acceptance: the update skill and `docs/gcp-live-checklist.md`'s Checks 301–302 say `update` installs a missing unit (D2700); their pinned tests follow.
- Files: `plugin/skills/update/SKILL.md`, `plugin/update_skill_test.go`, `internal/cli/docs_check301_302_test.go`, `docs/gcp-live-checklist.md`.
- Tests: `TestUpdateSkill*` (existing, amended), `TestCheck301302*` (existing, amended).
- Command: `go test ./plugin/ -run TestUpdateSkill -count=1 && go test ./internal/cli/ -run 'TestCheck301|Docs' -count=1`
- Red step: the amended skill test fails on today's sentence.
- Depends on: R2 (the docs say what its code does).
- Commit: `docs: update installs the orchestrator unit (R7, D2700)`
- Reviewer tier: opus (a plugin skill).

### R3: the daemon honours orchestrator_enabled (daemon-release)
- Acceptance: the daemon reads the merged `orchestrator_enabled` on every caps refresh; while false it launches nothing, records one fact saying so, and keeps folding runs already going; an unreadable switch keeps the last value read (a failed read never turns it on).
- Files: `internal/orchestrator/caps.go`, `internal/orchestrator/caps_test.go`, `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_run_test.go`.
- Tests: `TestDisabledPolicyLaunchesNothing`, `TestDisabledPolicyStillFoldsRunning`, `TestUnreadableSwitchKeepsLastValue`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestDisabledPolicy|TestUnreadableSwitch' -count=1`
- Red step: `TestDisabledPolicyLaunchesNothing` launches today.
- Depends on: R1 (the key; smaller than a contract would be), E1 (one file: `orchestrator_run.go`).
- Commit: `feat(orchestrator): the daemon stops launching when orchestrator_enabled is off (R3)`
- Reviewer tier: standard.

### R4: the daemon adopts runs in flight at start (daemon-release)
- Acceptance: `orchestrator.Adopt(ctx, lister, node)` takes, from a `RunLister` seam, the code-change runs in flight of the project's repositories (no review job, no artifact run), and for each one not already an item's run writes a `running` item whose id is the run id and whose `plan_task` is the launch's, with an `adopted` mark that keeps it out of any escalation rung that merges until a person approves it; for each plan an adopted run names, it records one `plan_in_flight` fact; it writes only under the lease; a second call adopts nothing (D2702, D2721).
- Files: `internal/orchestrator/adopt.go` (new), `internal/orchestrator/adopt_test.go` (new).
- Interfaces: `RunLister` (W1 builds the real one); W1 calls `Adopt` at start-up.
- Tests: `TestAdoptWritesRunningItemsOnce`, `TestAdoptCarriesPlanTask`, `TestAdoptRecordsPlanInFlightFact`, `TestAdoptSkipsReviewAndArtifactRuns`, `TestAdoptedItemNeverMergesUnapproved`, `TestAdoptRefusedWithoutLease`.
- Command: `go test ./internal/orchestrator/ -run TestAdopt -count=1`
- Red step: the tests fail before `Adopt` exists.
- Depends on: none (the `plan_task` field is asserted as JSON here; W1's end-to-end test proves it meets X1's field).
- Commit: `feat(orchestrator): adopt runs in flight at start-up (R4, D2702)`
- Reviewer tier: standard.

### R5: the orchestrator skill hands work to the daemon, and the cutover how-to (daemon-release, backlog)
- Acceptance: the skill says: while `orchestrator_enabled` is on and `fugaro orchestrator status` shows a live daemon, queue work as items (`work_item_upsert`, or `fugaro orchestrator import --plan <folder>` for a plan) and never launch, sweep or watch runs yourself; at session start, import any plan a `plan_in_flight` fact names; ask the person for the plan's approval and never write it; "let's fix this" is a `fugaro backlog add` (queued, unapproved: D2717), distinct from `fugaro issue`, which is Fugaro's own product bugs; with the switch off or no daemon, drive runs as before. The cutover how-to becomes "run `fugaro update`" plus the switch (D2729).
- Files: `plugin/skills/orchestrator/SKILL.md`, `plugin/skills/orchestrator/reference/state.md`, `plugin/orchestrator_skill_test.go`, `docs/how-to/orchestrator-cutover.md`, `internal/cli/docs_orchestrator_cutover_test.go`.
- Tests: `TestOrchestratorSkillNamesOnlyRealCommandsAndFlags` (existing), `TestSkillNeverTellsTheSessionToApprove`, `TestSkillSaysStopSweepingWhileDaemonLive`, `TestCutoverIsUpdateOnly`.
- Command: `go test ./plugin/ -run 'TestOrchestratorSkill|TestSkillNever|TestSkillSaysStop|TestSessionStart' -count=1 && go test ./internal/cli/ -run 'Docs|TestCutover' -count=1`
- Red step: `TestSkillSaysStopSweepingWhileDaemonLive` fails on today's skill.
- Depends on: G1 (the flag), W1 (plan approval, adoption and the review phase behave as taught), R1 (the key), B1 (backlog add writes an item).
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
- Acceptance: `fugaro orchestrator import --plan <folder>` reads `<folder>/implementation-plan.graph.json` with `plan.ReadFile` and writes one typed patch to the plan's node (X0): one `work_item_upsert` per node with id = the node id, `plan_task` = `<plan>/<node>`, `spec` = the plan folder, `rigor_tier` = the node's `tier` (D2730), `depends_on` = the graph's edges, and a brief per D2737; an item whose `plan_task` matches an adopted item's is linked to it rather than duplicated; a re-import changes only items not yet launched and leaves approvals of unchanged nodes valid; a refused graph writes nothing; `--json` prints the result, including the `{id, digest}` list `plan_approve` needs. The existing `-f` session-dump import is unchanged.
- Files: `internal/orchestrator/planimport.go` (new), `internal/orchestrator/planimport_test.go` (new), `internal/cli/orchestrator_import.go`, `internal/cli/orchestrator_import_test.go`.
- Tests: `TestPlanImportOneItemPerNode`, `TestPlanImportBriefNamesTheTask`, `TestPlanImportLinksAdoptedItem`, `TestPlanImportIsIdempotent`, `TestPlanImportLeavesLaunchedItems`, `TestPlanImportRefusedGraphWritesNothing`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestPlanImport|TestOrchestratorImport' -count=1`
- Red step: `TestPlanImportOneItemPerNode` fails: no `--plan`.
- Depends on: S0 (the tier), X0 (the plan's node).
- Commit: `feat(orchestrator): import a plan graph as work items (G1, D2704)`
- Reviewer tier: standard.

### G2: the plan_approve patch op (graph-import)
- Acceptance: `plan_approve{plan, items}` from the person class approves exactly the listed items of that plan's node at the listed digests; refused from the session, daemon and decision-agent classes, for an empty `plan` or an empty list, and for a digest that no longer matches (that item is skipped with an audit line, the rest approved); an item tagged with the plan's `spec` later is not approved by it (D2706, D2738). The CLI counts the op among `personOnlyOps` and sends it with the person-class wording.
- Files: `internal/orchestrator/patch_plan.go` (new), `internal/orchestrator/patch_plan_test.go` (new), `internal/cli/orchestrator_patch.go`, `internal/cli/orchestrator_patch_test.go`.
- Tests: `TestPlanApproveApprovesTheListedItems`, `TestPlanApproveRefusedFromSessionAndDaemon`, `TestPlanApproveIgnoresItemsTaggedLater`, `TestPlanApproveSkipsChangedDigest`, `TestPatchCountsPlanApproveAsPersonOnly`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestPlanApprove|TestPatchCountsPlanApprove' -count=1`
- Red step: the tests fail before the op is applied.
- Depends on: X1 (the op), X0 (the plan's node).
- Commit: `feat(orchestrator): approve a plan's imported items in one person-only op (G2, D2706)`
- Reviewer tier: opus (the approval fence).

### G3: unlock on merge: the review phase and the merge read (graph-import)
- Acceptance: `foldPhase` takes a succeeded run that left a GitHub pull request to `review`, and one with none, or on a Bitbucket repository, to `done` (D2736); each tick, through a `MergeReader` seam, the daemon reads each `review` item's pull request (one `get` per item, as the label route answers it, F39), moving `merged` to `done` and `closed` to `failed`; a read that fails leaves the item in `review`; `dependenciesDone` is unchanged, so a dependent waits for `done` (D2705).
- Files: `internal/orchestrator/runview.go`, `internal/orchestrator/mergeread.go` (new), `internal/orchestrator/mergeread_test.go` (new), `internal/orchestrator/export_test.go`.
- Interfaces: `MergeReader` (W1 builds the real one over the label route's client).
- Tests: `TestSucceededWithPRGoesToReview`, `TestBitbucketSucceededGoesToDone`, `TestMergedPRMovesToDone`, `TestClosedUnmergedFails`, `TestFailedMergeReadLeavesReview`, `TestDependentWaitsForMerge`.
- Command: `go test ./internal/orchestrator/ -run 'TestSucceededWithPR|TestBitbucketSucceeded|TestMergedPR|TestClosedUnmerged|TestFailedMergeRead|TestDependentWaits' -count=1`
- Red step: `TestDependentWaitsForMerge` fails: today the dependent launches on success.
- Depends on: none.
- Commit: `feat(orchestrator): dependents unlock when the pull request merges (G3, D2705)`
- Reviewer tier: standard.

### E1: the daemon wakes on agents/ and outcomes/ (run-events)
- Acceptance: `orchestratorDaemon.wait` streams its own nodes, `agents/<slug>` for each repository the state names, and `outcomes/<today>` (re-opened at the UTC day change); any put or patch after the opening snapshot wakes the tick; an `agents/` entry deleted with no matching `outcomes/` entry also wakes it, and the tick's existing `executionOver` check runs for that run (D2708, D2725); the 30 s ceiling is unchanged; a stream that fails falls back to the ceiling without ending the daemon.
- Files: `internal/orchestrator/wake.go` (new), `internal/orchestrator/wake_test.go` (new), `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_run_test.go`.
- Tests: `TestOutcomeWritesWakeTheTick`, `TestVanishedAgentEntryWakesAndChecks`, `TestOpeningSnapshotDoesNotWake`, `TestStreamFailureFallsBackToCeiling`, `TestDayChangeReopensOutcomes`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestOutcomeWrites|TestVanishedAgent|TestOpeningSnapshot|TestStreamFailure|TestDayChange' -count=1`
- Red step: `TestOutcomeWritesWakeTheTick` waits for the ceiling today.
- Depends on: none.
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
- Acceptance: the run view projects X2's fields; when an item's run ends, each `blocks` entry naming an item of the project that has not launched (by `plan_task`, or a path its footprint names) adds an edge in front of that item; each `needed` entry records a `graph_suspect` fact; `conflict_paths` matched against the footprints of items merged since the attempt began record a `graph_suspect` fact and add the edge on the item's next attempt, leaving the re-run to the follow-up and escalation ladder; an edge is never removed, a running attempt is never touched, an addition that would make a cycle, pass `MaxDependsOn` or put a node over its cap is refused with one audit line, and the tick never fails on any of it (D2709, D2710).
- Files: `internal/orchestrator/fold_discovered.go` (new), `internal/orchestrator/fold_discovered_test.go` (new), `internal/orchestrator/tick.go`, `internal/orchestrator/runview.go`.
- Tests: `TestFoldAddsEdgeInFrontOfQueuedItem`, `TestFoldNeverRemovesOrUnblocks`, `TestFoldRefusesCycle`, `TestFoldRefusesPastMaxDependsOn`, `TestFoldIgnoresRunningAttempts`, `TestNeededBecomesGraphSuspect`, `TestConflictEdgeOnNextAttempt`, `TestFoldIsIdempotent`.
- Command: `go test ./internal/orchestrator/ -run 'TestFold|TestNeededBecomes|TestConflictEdge' -count=1`
- Red step: `TestFoldAddsEdgeInFrontOfQueuedItem` fails before the fold exists.
- Depends on: X1 (the fields), X2 (the record), X0 (one file: `tick.go`), G3 (one file: `runview.go`).
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
- Acceptance: `fugaro orchestrator state --brief` adds one line per plan node with open items and one for the backlog node, computed from the items' tiers and phases and the local table cache (no runs-bucket read, D1243); the brief cache's `orchestratorBriefCacheVersion` moves by one.
- Files: `internal/orchestrator/summary.go`, `internal/orchestrator/summary_test.go`, `internal/cli/orchestrator_state.go`.
- Tests: `TestBriefShowsPlanEstimate`, `TestBriefShowsBacklogEstimate`, `TestSessionStartPrintMakesNoNetworkCall` (existing), `TestSessionStartPrintsOneScreen` (existing).
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestBriefShows|TestSessionStartPrint' -count=1 && go test ./plugin/ -run TestSessionStart -count=1`
- Red step: `TestBriefShowsPlanEstimate` fails.
- Depends on: S2.
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
- Tests: `TestBacklogIndexIsCurrent` (existing), `TestBacklogHeaderSaysHistory`.
- Command: `go test ./internal/backlog/ -count=1 && go test ./internal/cli/ -run 'TestBacklog|Docs' -count=1`
- Red step: `TestBacklogHeaderSaysHistory` fails.
- Depends on: none.
- Commit: `docs(backlog): the folder is history (B8, D2716)`
- Reviewer tier: standard.

### B3: the GitHub issues route behind the credentials service (backlog)
- Acceptance: the credentials service gains an issue route alongside the label route (D2439's pattern): only the orchestrator class may call it; the daemon names a repository and an act (list open, open one); the service mints its own `{issues: write, metadata: read}` token for that repository (not the label route's), performs the act and returns the result; no token leaves it; it refuses a repository outside the installation, every other caller class (job, review, verifier, coordinator, person), and an opened issue's title over 256 or body over 8000 characters; every refusal writes one audit line; `internal/gitprov/github` gains the two REST calls; the request and response shapes live in `internal/creds/issueroute.go`. There is no close: closing is `Fixes #N` (D2727).
- Files: `internal/creds/issueroute.go` (new), `internal/credsvc/issueroute.go` (new), `internal/credsvc/issueroute_test.go` (new), `internal/gitprov/github/issues.go` (new), `internal/gitprov/github/issues_test.go` (new).
- Tests: `TestIssueRouteNeverReturnsAToken`, `TestIssueRouteRefusesForeignRepo`, `TestIssueRouteRefusesEveryOtherClass`, `TestIssueRouteRefusesOversizedIssue`, `TestIssueRouteAuditsEachRefusal`, `TestIssueRouteMintsIssuesScopeOnly`, `TestIssueRouteListOpenPaginates`, `TestGitHubIssuesCalls` (httptest).
- Command: `go test ./internal/credsvc/ ./internal/gitprov/github/ ./internal/creds/ -run 'TestIssueRoute|TestGitHubIssues' -count=1`
- Red step: the tests fail before the route exists.
- Depends on: none. U3 is the owner's to settle first: if the GitHub App lacks `issues: write`, the task builds list only and says so.
- Commit: `feat(credsvc): the issue route for the GitHub adapter (B3, D2718)`
- Reviewer tier: opus, independent review (`fugaro review --pr N`; credentials service).

### B4: the issue sync: the adapter seam, ingest, mirror, dedupe, on the tick (backlog)
- Acceptance: `issuesync.Adapter` (`ListOpen`, `Open`), an in-memory fake, and `issuesync.Sync(ctx, adapter, nodes, cfg)` returning one tick's patch to the backlog node: every open issue of each configured repository with no item of its external id becomes a queued, unapproved item holding the external id and a title of at most 80 characters, never the body (D2720 as amended); an issue carrying the mirror marker is never ingested (D2728); with `mirror: true`, a spec-less item with no external id first adopts an open issue already carrying its marker, and only otherwise opens one (D2719); a closed issue archives its item if not launched; an ingest that would pass the backlog node's caps stops for the tick with one fact; the daemon runs `Sync` once per tick when configured, binding the issue route's client from B3.
- Files: `internal/issuesync/adapter.go` (new), `internal/issuesync/fake.go` (new), `internal/issuesync/sync.go` (new), `internal/issuesync/sync_test.go` (new), `internal/orchestrator/tick.go`.
- Tests: `TestIngestOpenIssuesOnce`, `TestIngestStoresNoBody`, `TestIssueFloodNeverPassesTheCap`, `TestMirroredIssueIsNotIngestedBack`, `TestMirrorAdoptsItsOwnIssueAfterLostSave`, `TestMirrorOffByDefault`, `TestClosedIssueArchivesQueuedItem`, `TestSyncIsIdempotent`.
- Command: `go test ./internal/issuesync/ ./internal/orchestrator/ -run 'TestIngest|TestIssueFlood|TestMirror|TestClosedIssue|TestSyncIs' -count=1`
- Red step: the tests fail before `Sync` exists.
- Depends on: B3 (the route's client), X0 (the backlog node), X1 (the external field), B6 (the config; smaller than a contract), E3 (one file: `tick.go`). The adapter-contract break of B4 → B3 was weighed and undone (no gain).
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

### B6: the adapter's config in the project layer (backlog)
- Acceptance: the project layer accepts `issues: {github: {repos: [...], mirror: false}}` (D2733), strictly decoded, documented in `schemas/project-layer.schema.json`, readable with `fugaro settings issues.github --json`.
- Files: `internal/localcfg/issues.go` (new), `internal/localcfg/issues_test.go` (new), `schemas/project-layer.schema.json`.
- Tests: `TestIssuesConfigStrict`, `TestMirrorDefaultsFalse`.
- Command: `go test ./internal/localcfg/ -run 'TestIssuesConfig|TestMirrorDefaults' -count=1 && go test ./schemas/ -count=1`
- Red step: the config is refused as an unknown field today.
- Depends on: none.
- Commit: `feat(localcfg): the GitHub issues adapter's config (B6, D2733)`
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

### W1: wire plan approval, adoption and discovery into the daemon, end to end (graph-import, daemon-release, run-events)
- Acceptance: the patch dispatch routes `plan_approve` to G2; the CLI builds the real `MergeReader` over the label route's client and the real `RunLister` over the runs bucket (launch records with no final record, and live executions); the daemon calls `Adopt` once at start-up, under the lease, before its first tick; an emulator test imports a three-node plan (A → B, A → C), approves it with one `plan_approve`, finishes A's run with a pull request, sees A in `review`, merges it in the fake, and sees B and C launch on the next wake; a second test adopts a run in flight and meets its plan import without a duplicate item; a third finishes a run with a `blocks` entry and sees the edge added.
- Files: `internal/orchestrator/patch.go`, `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_mergeread.go` (new), `internal/cli/orchestrator_runlister.go` (new), `internal/orchestrator/plan_e2e_test.go` (new).
- Tests: `TestPlanRunsEndToEnd`, `TestAdoptionMeetsImport`, `TestDiscoveredEdgeEndToEnd`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestPlanRunsEndToEnd|TestAdoptionMeetsImport|TestDiscoveredEdgeEndToEnd' -count=1`
- Red step: `TestPlanRunsEndToEnd` fails before the dispatch routes the op.
- Depends on: G1, G2, G3, R4, E2, E3, B7 (it runs them together), R3 (one file: `orchestrator_run.go`).
- Commit: `feat(orchestrator): plans, adoption and discovery end to end (W1)`
- Reviewer tier: opus (it wires the approval fence and a credentials-service reader).

## Parallel execution

Read back with `fugaro plan graph --plan docs/orchestrator-graph-estimates/implementation-plan.md`: 33 tasks, four levels, no refusal.

| Level | Tasks |
| --- | --- |
| 0 | X0, X1, X2, S0, R1, R2, R4, G3, E1, S1, B3, B6, B8 (13) |
| 1 | R3, R7, G1, G2, E2, E3, E4, S2, B1, B7 (10) |
| 2 | R6, S3, S5, B2, B4, B5, W1 (7) |
| 3 | R5, S4, S6 (3) |

**Critical path** (as the read reports it): E1 → R3 → W1 → R5, four tasks; **bottleneck:** E1. What must be sequential and why: R3 after E1 and W1 after R3 (one file, `internal/cli/orchestrator_run.go`); E3 after X0 and G3, B4 after E3 (one file each: `tick.go`, `runview.go`); the skill and doc tasks R5, R6, R7 and S6 after the code they describe, because the plugin and docs tests check every named command, flag and sentence.

### The dependencies broken and kept

**Broken: none.** Two breaks were drawn and weighed:

- S2 → S1 (and S3 → S2, S4 → S3, S5 → S2) by an estimate contract: the dependents needed only the estimate's shape. `fugaro plan graph` reported the break as no gain (the weighted path stayed at 8), because the estimate chain is off the critical path; undone.
- B4 → B3 by an adapter contract: the sync needed only the adapter interface. Off the critical path as well; undone.

Kept, with the reason that decided it:

- R3 → R1, B4 → B6, E2 → X2: the dependent reads a field or key the other adds, and a contract would be the same size as the task.
- R3 → E1, W1 → R3, E3 → X0, E3 → G3, B4 → E3: one file.
- G1 → X0, G2 → X0, B1 → X0: the write lands on a node X0 builds (behaviour, not a shape).
- G2 → X1, E3 → X1, B7 → X1: the state shape **with its digest migration** is behaviour; a stub of it would be the task.
- G1 → S0, S2 → S0, R6 → S0: the tier field (smaller than a contract).
- S2 → S1, S3 → S2, S5 → S2, S4 → S3, S6 → S3: kept after the no-gain break was undone (above).
- B4 → B3: likewise.
- B2 → B1: the fold reuses backlog add's item writer.
- B5 → B7: the body reads the field the launcher passes.
- E4 → E1, G3: it pins their behaviour.
- R7 → R2; R5 → G1, W1, R1, B1; R6 → G1: a document or skill names a command, flag or behaviour that must exist as written.
- W1 → G1, G2, G3, R4, E2, E3, B7: the end-to-end wiring runs the real sides.

### Shared files

No level holds two writers of one file. The files with more than one writer, and their levels:

| File | Writers (level) |
| --- | --- |
| `internal/cli/orchestrator_run.go` | E1 (0), R3 (1), W1 (2) |
| `internal/cli/orchestrator_run_test.go` | E1 (0), R3 (1) |
| `internal/orchestrator/tick.go` | X0 (0), E3 (1), B4 (2) |
| `internal/orchestrator/runview.go` | G3 (0), E3 (1) |
| `internal/orchestrator/patch.go` | X1 (0), W1 (2) |

A change to any task's Files re-checks this table.

### Contract changes anticipated

None: the plan holds no contract.

## Proposed runs, not launched

33 runs, one per task. Worst case at the project's per-run cap of $5: **$165**, over four levels (at most 13 at once, inside `max_parallel = 20`). The independent reviews of R5, E2 and B3 (`fugaro review --pr N`) are three more review jobs. **None starts until the owner says yes.** Level 0 first.

## Checks the plan takes (Checks 700–703, owner-run)

- **Check 700** (R2, R3): on a sandbox installation, `fugaro update` on two machines installs the unit on both; one holds the lease, the other stands by; setting `orchestrator_enabled: false` stops launches on the live one within one tick, and a run already going finishes. **You** run it; no agent runs any step.
- **Check 701** (R4, G3, W1, B7): start a sandbox run by hand with `--plan-task`, then let `update` install the daemon; it adopts the run, the session imports its plan, one `plan_approve`, merge the pull request, and the dependent launches.
- **Check 702** (E1): from a run's final record to its dependent's launch, under 10 s on a laptop that is awake; after a sleep, within one ceiling of waking. Read the RTDB plan's connection limit first (U1).
- **Check 703** (B3, B4, B5): on the sandbox repository, an open issue becomes a backlog item; with `mirror: true`, an item becomes an issue and is not ingested back; a run for the issue's item opens a pull request with `Fixes #N`, and merging it closes the issue. Settles U3 and U4.

## After-deploy steps

None beyond `fugaro update` (R2's and B2's release notes). If U3 finds the GitHub App lacks `issues: write`, granting it is an owner step `update` cannot take; B3's pull request then says so in `### For operators`, and the adapter runs list-only until it is granted.
