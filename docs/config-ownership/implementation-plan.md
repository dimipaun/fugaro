# Config ownership: implementation plan

Builds `spec.md` as amended by `reconciliation.md` (checked against `origin/main` at `eddaa041b`,
2026-11-15). **Nothing here is implemented, and no task is approved: each needs the owner's explicit go.**
The owner's answers of 2026-11-15 shaped it: D2601 (seven modules), D2602 (semantic commands run the
`policy` stage in-process, composite D1563 on failure), D2603, D2604, D2605, D2606, D2607, D2609 and D2610.
Unanswered, and marked on each task that waits on them: D2608, D2611, D2612, D2613, D2614, D2615, D2616, D2617 (and
the open tensions T3 and T5). The first task of the plan is the conformance test, as the spec rules.

## 1. Conventions

- Rules files load by path: `.claude/rules/credentials-service.md` for `internal/creds*` (not touched here),
  `.claude/rules/terraform.md` not touched, `.claude/rules/console.md` only if W1 finds the console's
  `settings.edit` argv changes (U8 of the reconciliation). `CLAUDE.md` governs the rest.
- **Always:** tests before commits; targeted commands (`go test ./internal/cli -run 'TestX|TestY' -count=1`,
  `GOFLAGS=-p=2` on a loaded machine); `gofmt -l .` prints nothing; `go vet ./...`; docs tests
  (`go test ./internal/cli/ -run Docs`) when a document or a pinned sentence changes; hermetic tests
  (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`); comments say why, never what.
- **Ask first:** a new dependency, a schema change beyond an additive key, a node version bump (the node
  is at `projectpolicy.Version` 7 and binary-allow-rule AR1 takes 8), any change to `.claude/**` or
  `plugin/skills` outside D1, anything the no-prompt policy would call a prompt.
- **Never:** a confirmation step (`--ask` is the whole exemption; no command waits on stdin); edit
  `.github/workflows/`; a secret-shaped literal; a live cloud action by an agent (none is planned; the
  plan reserves no check); touch `docs/status.md` (the sweep moves its row, not a task's pull request);
  read or merge a base that changed `.github/workflows/` into a branch with a cloud follow-up run.
- A field never changes meaning: new local keys (N1) and new rows (R1, W2) are additive; no field is
  renamed or retyped. The pull request of a task names the `T#` and `D#` it implements.
- `fugaro update` stays the whole upgrade (CLAUDE.md hard rule): N1 and N2 ship the converge test; the
  operator step is "run `fugaro update`" (D1).

## 2. Global constraints and review focus

Inputs and failure modes the spec implies; each has the task that pins it.

| Constraint | Pinned by |
| --- | --- |
| A reconcile never publishes from a coding agent's session (D2607, F7); the existing stage refusal and loosening judgement stay | N1 (agent-marker test, mutation check: drop the refusal and the test fails), N3 |
| The first reconcile is additive: it never deletes a node member the local config does not state; an explicit unset removes; silence keeps | N1, N2 |
| `fugaro update` from an installation at the previous version converges; and a run in flight keeps its records (`TestUpdateLeavesARunInFlightRunning` stays green) | N2 |
| A project-layer raise of `governance.stage_timeout`/`stage_budget`/`stage_tokens` is fenced in an agent session, including through the new unprefixed spelling (D2604, composite D1576) | W2 |
| The unprefixed `agent.x --layer project` and the long spelling land the same bytes (D2606); no spelling writes a key the layer refuses at re-read | W1 |
| A ceiling with no `--layer` writes the project layer; a layer the key lacks refuses naming the layers it has (D2605, T3) | W1 |
| A refusal prints the one working command line for the key (item 6), from the writer table, never a generic family sentence when a writer is registered | W3, I1 |
| Every registry row has a runnable writer or a stated label; a row that names an owner that does not write the key fails (item 4, D2610) | T1 (ratchet), I1 (strict), L1 |
| A provider's secret or auth value never appears in argv, a diff, a log or a record (M2); `providers.*.secret` goes by the `secrets` family's value entry | M2 |
| No new command waits on stdin: each family test runs its command with stdin closed | every family task; I1 sweeps them |
| A semantic command's local write and the node agree after the command, after `project policy publish` and after `update` (item 9) | I1 end-to-end test |
| The console's `settings.edit` argv and the Go equality test between `consoleLaunchPaths` and `spawn.ts` still hold after the default layer changes | W1 |
| Refusal wording and skill text pass the plugin lint and the docs tests | D1, W3 |

Review focus for every reviewer: the new trust boundary is `update` and the semantic commands becoming
writers of spend and data-egress grants (pools, credentials, crafts, cascade, binaries, grades) at the
node. Probe: can a hostile agent session queue or trigger a publish of a grant, loosen a fenced ceiling
through the new unprefixed spelling, or smuggle a secret through a flag.

## 3. Slices and tasks

Waiting markers: **WAITS ON Dn** means the task does not start before the owner answers that decision.
Task ids follow the modules: `C` contract, `T` conformance, `W` write path, `R` rows, `N` node publish, `A`
accounts, `K` cascade, `M` models, `L` labels, `I` integration, `D` documents.

- **Slice A, foundation (inert):** C1, T1, N3, L1. Adds the contract, the conformance test as a ratchet
  that fails only on new gaps, the in-process stage and the labels. Changes no behaviour a user sees.
- **Slice B, one write path (`cm-write-path`):** W1, W2, W3, R1.
- **Slice C, node reconcile (`cm-node-publish`):** N1, N4, N2.
- **Slice D, the writer families:** `cm-accounts` A1, A2; `cm-cascade` K1, K2; `cm-models` M1, M2, M3, M4.
- **Slice E, join:** I1, D1 (`cm-docs`).

### C1: the writer and after-write contract (cm-conformance)
- Acceptance: `settings.RegisterWriter(pattern, Writer)` and `settings.WriterOf(key) (Writer, bool)` exist, with `Writer{Command, Argv, Store, Label, Reason}` where `Store` is one of local, project, repo, node, budget, none and `Label` is one of "", plumbing, fake-endpoint, file; a wildcard pattern matches a concrete key the way the registry's own lookup does; `cli.afterWriteFunc` and the package variable `runAfterWrite` exist, and the stub makes it a no-op that returns nil. The contract document states what each side may rely on.
- Files: `internal/settings/writer.go` (new), `internal/settings/writer_test.go` (new), `internal/cli/after_write.go` (new), `internal/cli/after_write_stub.go` (new, the stub), `docs/contracts/config-ownership.md` (new). The stub is `after_write_stub.go`: an `init` that sets `runAfterWrite` to a no-op; I1 deletes it.
- Interfaces: produces the writer registry and the after-write hook; consumes nothing.
- Tests: `TestWriterOfMatchesWildcardRows`, `TestRegisterWriterRefusesADuplicatePattern`, `TestLabelIsOneOfTheThree`. The contract document names who registers which pattern: T1 registers only the generic `settings set` rows and the commands no task here replaces (`budget`, `workspace`, `secrets`, `project`); the families register their own (A1 pools, M1 `models use`, M3 `rigor set`, and so on), so no pattern has two registrants. Command: `go test ./internal/settings -run 'TestWriterOf|TestRegisterWriter|TestLabel' -count=1`.
- Red step: `TestRegisterWriterRefusesADuplicatePattern` fails with the second registration accepted.
- Depends on: none.
- Commit: `settings: the writer registry and the after-write hook (C1)`.
- Reviewer tier: opus with a mandatory independent review (`fugaro review --pr N`) before any side builds on it: the contract carries the writer of grants. Launched at the highest mode of its consumers.

### T1: the writers table of what works today and the executing conformance test (cm-conformance)
- Acceptance: for every registry row, the test finds its writer (from `writers_table.go` or any registered writer) or a label, runs the writer's argv against a hermetic config and asserts the key changed in the right store, and fails on any row that fails and is not in `testdata/writer_gaps.golden`; a row listed in the golden that now passes is reported with `t.Log`, not failed, until I1 flips the ratchet strict. The golden is generated by `-update` and lists today's gaps (the audit's counts are re-measured here: U1 of the reconciliation).
- Files: `internal/cli/writers_table.go` (new), `internal/cli/writer_conformance_test.go` (new), `internal/cli/testdata/writer_gaps.golden` (new). The generic `settings set` rows register one pattern each by walking `settingsGenericKey`; the commands no task here replaces register too (`budget`, `workspace`, `secrets`, `project`); the `accounts pool`, `models use` and `rigor` rows are left to A1, M1 and M3 and sit in the golden until then.
- Interfaces: consumes C1's registry; cloud-store rows (budget database, node) are probed through `internal/gcpfake`; the after-write stage is called through C1's hook (a stub until I1). Produces the ratchet every later task is measured by.
- Tests: `TestWriterConformanceEveryRowHasAWriterOrALabel`, `TestWriterConformanceTheOwnerWritesTheKey`, `TestWriterGapsGoldenOnlyShrinks`. Command: `go test ./internal/cli -run 'TestWriterConformance|TestWriterGaps' -count=1`.
- Red step: with the registry's `agent.recipe` row owned by `models`, `TestWriterConformanceTheOwnerWritesTheKey` fails on a tree where the golden does not list it (today it is a known gap, so the red step is: delete its golden line and see the failure).
- Depends on: C1 (builds against the Writer type and registry).
- Commit: `cli: the executing writer-conformance test, and the table of what works today (T1)`.
- Reviewer tier: standard. Also settles U1 and U2 (the audit's counts; `--add-entry model=X`; `batch.*`, `build.*`, `compute_prices.*` writers; whether `setup` writes `endpoints.*`) as golden lines.

### N3: the in-process policy stage a semantic command runs after its write (cm-node-publish)
- Acceptance: `publishAfterWrite(ctx, cmd, change)` runs the `policy` stage through `stagesNamed` (the stage `fugaro update` runs, composite AP2) in-process; on success the node holds the write; on a stage failure the local change stays, the command exits non-zero and prints one sentence, from one helper, saying the change is saved and not yet live; under the agent marker the stage's own refusal and audit record apply and nothing is published; `--no-apply` is not added (it arrives with composite AP3); it runs the stages the key's store needs (`policy` for node members, `layer` for project-layer keys, D2615). **WAITS ON D2614** for the wording (composite D1563's confirmed sentence names a command that does not exist) **and D2615** for the stage set.
- Files: `internal/cli/write_publish.go` (new), `internal/cli/write_publish_test.go` (new). A package `init` sets `runAfterWrite = publishAfterWrite`; it sorts after the stub's, so the real one wins until I1 deletes the stub.
- Interfaces: implements C1's `afterWriteFunc`; consumes `stagesNamed` (merged, #2757).
- Tests: `TestAfterWritePublishesThePolicyStage`, `TestAfterWriteFailureKeepsTheLocalChange`, `TestAfterWriteUnderTheAgentMarkerPublishesNothing` (run against a repo-layer write and against a write a managed gate admits: an unmanaged project refuses every agent layer write for another reason and proves nothing), `TestAfterWriteSentenceIsOneHelper`, `TestAfterWriteRunsTheLayerStageForAProjectLayerKey`. Command: `GOFLAGS=-p=1 go test ./internal/cli -run 'TestAfterWrite' -count=1`.
- Red step: remove the agent refusal in `policyMigrateStage.Apply` (`init_policy_migrate.go`); `TestAfterWriteUnderTheAgentMarkerPublishesNothing` fails (mutation check).
- Depends on: C1 (implements its signature).
- Commit: `cli: a semantic command runs the policy stage after its write (N3)`.
- Reviewer tier: opus with a mandatory independent review (policy publish, agent refusal).

### L1: labels for the keys no command writes (cm-conformance)
- Acceptance: every row that no command writes is registered with a label (plumbing, fake-endpoint or file) and a one-line reason, or is promoted to a settings row; the conformance test fails an unlabelled, unwritten row. **WAITS ON D2611 and on open tension T5.**
- Files: `internal/cli/writers_labels.go` (new), `internal/cli/writers_labels_test.go` (new).
- Interfaces: registers through C1. Settles F18's `endpoints.*` question with a probe.
- Tests: `TestEveryUnwrittenRowCarriesALabelAndAReason`. Command: `go test ./internal/cli -run 'TestEveryUnwrittenRow' -count=1`.
- Red step: remove one `endpoints.*` label; the test fails.
- Depends on: C1 (registers through it).
- Commit: `cli: a stated reason for every key no command writes (L1)`.
- Reviewer tier: standard.

### W1: the resolver: default layer by merge rule and the agent project spelling (cm-write-path)
- Acceptance: with no `--layer`, a Tighten key writes the project layer when one exists (else local), a Replace key keeps the repo default; `--layer` overrides; a layer the key does not have refuses naming the layers it has; `agent.x --layer project` and `layer.defaults.agent.x --layer project` write the same bytes and the file reads back; no key is written to a place the layer refuses at re-read. **WAITS ON D2612** (composite AP5 owns `settings_set.go`).
- Files: `internal/cli/settings_set.go`, `internal/cli/settings_set_test.go`, `internal/cli/settings_layer_default_test.go` (new).
- Interfaces: changes `settingsResolveTarget`'s default; every caller (`rigor`, `rigor_floor`, `accounts pool`) is covered by the existing tests.
- Tests: `TestSettingsDefaultLayerByMergeRule`, `TestSettingsAgentKeyProjectSpelling`, `TestSettingsWrongLayerRefusesNamingTheRight`, `TestSettingsRowsNeverWriteWhereTheLayerRefusesAtReread`, `TestAgentCeilingRaiseThroughTheProjectSpellingIsRefused` (a raise of `agent.max_budget_usd --layer project` in an agent session is refused with the old value read at the mapped `layer.defaults` path, under a managed gate that admits the write). Command: `GOFLAGS=-p=1 go test ./internal/cli -run 'TestSettingsDefaultLayer|TestSettingsAgentKey|TestSettingsWrongLayer|TestSettingsRowsNever|TestConsole|TestSettingsSet' -count=1`.
- Red step: `TestSettingsAgentKeyProjectSpelling` fails today with "agent: is not a project layer key" (the audit's probe).
- Depends on: none.
- Commit: `settings: a ceiling defaults to the project layer, and the agent keys take --layer project (W1)`.
- Reviewer tier: opus with a mandatory independent review (a new spelling in front of a ceiling's fence).

### W2: per-stage governance rows, the project default and the agent fence (cm-write-path)
- Acceptance: `governance.stage_timeout.<stage>`, `stage_budget.<stage>`, `stage_tokens.<stage>` and `tiers.<name>` have concrete rows at the repo and project layers; the four broken block rows are gone; `settings set governance.stage_timeout.implement 1h` writes the project layer and reaches a run's resolution; a repo write needs `--layer repo` and may only tighten (a looser value refuses); a project-layer raise is refused in a coding agent's session, with the same sentence the other raises print (the fence is `settingsCeilingKey` and `settingsLoosens`, which already cover the unprefixed `governance.*` and fail closed on durations; `TypedWhy` is not it); the four block rows stay as read rows that no longer list themselves writable, so `settings --json`'s keyDefaults loses no member (a field never changes meaning). The governance rows' Doc says they are enforced by the ee build (F12). That the write reaches a run depends on the layer stage (F19). **WAITS ON D2612 and D2615.**
- Files: `internal/settings/registry.go`, `internal/cli/settings_set.go`, `internal/cli/settings_stage_test.go` (new), `internal/settings/registry_stage_test.go` (new), `schemas/settings.schema.json` (`schemas_test.go` pins keyDefaults to every registry row).
- Interfaces: consumes W1's resolver and the existing ceiling fence.
- Tests: `TestStageTimeoutProjectDefaultReachesTheRun`, `TestStageTimeoutRepoMayOnlyTighten`, `TestStageTimeoutRaiseRefusedInAnAgentSession` (under a managed gate that admits the write, so the refusal is the fence's and not the unmanaged project's blanket one), `TestNoBlockRowIsListedWritableThatCannotBeWritten`. Command: `GOFLAGS=-p=1 go test ./internal/settings ./internal/cli ./schemas -run 'TestStageTimeout|TestNoBlockRow|KeyDefaults' -count=1`.
- Red step: `TestStageTimeoutProjectDefaultReachesTheRun` fails today with "no setting has that name".
- Depends on: W1 (same file `settings_set.go`; one change split in two, so a contract would only add a hand-off), R1 (same file `registry.go`: no level holds two writers).
- Commit: `settings: the stage budgets, tokens and timeouts are settable where they apply (W2)`.
- Reviewer tier: opus with a mandatory independent review (a write guard on a ceiling). Mutation check: remove the agent fence; the raise test must fail.

### W3: a refusal names the writer and the working spelling (cm-write-path)
- Acceptance: `settingsRefuseRow` prints, for a key whose writer is registered, the exact command line to run (the writer's argv with the key filled in) and the layer spellings that work; a key with no registered writer keeps today's family sentence; the sentence passes the plugin lint and names `fugaro update` only as a stale-installation fix, never as a manual step; the generic allowlist (`settingsGenericKey`) admits the scalar rows R1 gave the owner `settings set` (D2603). **WAITS ON D2612.**
- Files: `internal/cli/settings_set.go`, `internal/cli/settings_set_test.go`, `internal/cli/settings_refusal_test.go` (new).
- Interfaces: consumes `WriterOf` (C1) and the populated table (T1).
- Tests: `TestRefusalNamesTheWorkingCommandLine`, `TestRefusalForAKeyWithoutAWriterKeepsTheFamilySentence`, `TestRefusalNeverSendsTheUserToUpdateAsAStep`, `TestSettingsGenericAllowlistAdmitsTheScalarRows`. Command: `go test ./internal/cli -run 'TestRefusal|TestSettingsSet' -count=1`.
- Red step: `TestRefusalNamesTheWorkingCommandLine` fails against the generic sentence of today (`settings set writes no route, price, rigor mode...`).
- Depends on: W2 (same file `settings_set.go`; R1's rows reach it through W2), T1 (prints the table T1 fills; the table is the data, so a contract would be the table).
- Commit: `settings: a refusal prints the command that writes the key (W3)`.
- Reviewer tier: standard.

### R1: registry rows and the right owner for the fields that have none (cm-write-path)
- Acceptance: rows exist for repo `ab.rate`, `workflows.*.needs` and the cascade block's leaves (new); repo `security.patterns`, `layer.security.patterns.*.*`, repo `planning.decoupling` and `layer.jobs.<job>.paused` already have rows and gain a correct owner (`update` is not a writer); the cascade block's leaves (`fallback`, `wait_threshold`, `backoff.initial`, `backoff.cap`, `stop_after_exhaustion_per_window`, `concurrency_per_token`); scalars and flags are written by `settings` (D2603): the rows of `orchestrator.*`, `issues.nudge_*`, `triage`, `verifier_accounts` and `mobile_allow`, and the object-list ones (`mobile_allow`, `orchestrator.decision_model_per_class`, written with `--add-entry`), take the owner `settings set` in place of a family that does not write them, and the rows whose widening the coding agent's session must not write (`verifier_accounts`, `mobile_allow`, `bitbucket_api_roots`) are marked widening so the existing fence covers them on the generic path; the cascade rows' owner is the cascade command K1 adds; `schemas/settings.schema.json` lists the new rows additively. **WAITS ON D2612** (#2782, #2498 edit `registry.go`) **and D2613** for the cascade owner token.
- Files: `internal/settings/registry.go`, `internal/settings/registry_new_rows_test.go` (new), `schemas/settings.schema.json`.
- Interfaces: consumes nothing; K1 and M4 read its rows.
- Tests: `TestRegistryHasARowForEveryRealConfigField` (walks the Go types of `config.Config`, `ProjectLayer`, `localcfg.Config`), `TestRegistryDefaultsMatchApplyDefaults`. Command: `go test ./internal/settings ./schemas -run 'TestRegistry|KeyDefaults' -count=1`.
- Red step: `TestRegistryHasARowForEveryRealConfigField` fails on repo `ab.rate` today.
- Depends on: none (it goes first on `registry.go`; W2 follows it).
- Commit: `settings: rows for the fields the registry did not name (R1)`.
- Reviewer tier: standard.

### N1: local carriers for the node-only blocks (cm-node-publish)
- Acceptance: the local config carries the cascade block under its own key `credential_cascade` (`fallback`, `wait_threshold`, `backoff.*`, `stop_after_exhaustion_per_window`, `concurrency_per_token`; not `credentials`, which is already the list of typed credentials, F23) and node `model_grades`, validated at load (a misspelt `fallback` is a refusal); an explicit-unset record `node_removals` (names removed, per block: `pools`, `crafts`, `craft_credentials`, `credentials`) is the shape D2607's "explicit unset" takes, cleared by N4 once the node no longer holds the name; all three keys are added to `sharedForbidden` in `sharedcfg.go`; the stale "MC4" comment in `project_cmd.go` is N4's. **WAITS ON D2612** (#2782 and #2732 edit `localcfg.go`; #2782 edits `sharedcfg.go`).
- Files: `internal/localcfg/localcfg.go`, `internal/localcfg/cascade_carrier_test.go` (new), `internal/cli/sharedcfg.go`, `internal/cli/sharedcfg_test.go`.
- Interfaces: produces the carrier fields K1 and N4 use.
- Tests: `TestCascadeCarrierValidatesAtLoad`, `TestCascadeCarrierIsNeverSharedConfig`, `TestCredentialsKeyStillMeansTheTypedList`, `TestNodeRemovalsRecordNamesNotValues`. Command: `go test ./internal/localcfg ./internal/cli -run 'TestCascadeCarrier|TestCredentialsKeyStill' -count=1`.
- Red step: `TestCascadeCarrierValidatesAtLoad` fails with the carrier unknown (strict decode refuses it today).
- Depends on: none.
- Commit: `localcfg: carriers for the cascade and the node's model grades (N1)`.
- Reviewer tier: standard.

### N4: the additive, field-by-field reconcile in buildProjectPolicy (cm-node-publish)
- Acceptance: `buildProjectPolicy` (the one node build `project policy publish` and `update` share) copies local to node for the cascade, `model_grades`, `crafts`, `craft_credentials` and `craft_pools`: silence keeps the node's value, a written value wins field by field (a pool's owner, allowed users and legacy-open come from the node when the local config does not state them), an explicit unset (a name in `node_removals`) removes, the first reconcile never deletes a node member the local config does not state; an ownerless local pool that the node lacks is skipped and named in one line, and neither door fails on it (`update` must converge; `ValidatePublishedPools` never sees it); the grant members are judged on the gate's side (`loosening()` fails closed on any change not shown to narrow, so an agent session and `update --yes` in a managed project are refused) and publish at the owner's own terminal with the diff shown and no typed name (the D1542/D1552 shape: `update` asks nothing, F24); nothing publishes from an agent session; the diff lines of the other operator-written blocks appear for these; the stale "MC4" comment is replaced by what is true. **WAITS ON D2612** (#2782 and #2753 edit `project_cmd.go`) **and D2608** (the ownerless-pool rule).
- Files: `internal/cli/project_cmd.go`, `internal/cli/init_policy_publish.go`, `internal/cli/project_reconcile_test.go` (new), `internal/cli/policy_loosening_grants_test.go` (new).
- Interfaces: consumes N1's carriers and the existing stage refusals.
- Tests: `TestReconcileSilenceKeepsTheNode`, `TestReconcileWrittenValueWinsFieldByField`, `TestReconcileExplicitUnsetRemoves`, `TestReconcileFirstRunNeverDeletes`, `TestReconcileRefusesInAnAgentSession` (under a managed gate that admits the write, so the refusal is the reconcile's), `TestUpdatePublishesAnOwnedLocalPoolAskingNothing` (unmanaged project, stdin closed), `TestOwnerlessLocalPoolIsSkippedNotRefused`, `TestGrantMembersAreLoosening`, `TestPolicyPublishAndUpdateReconcileTheSameNode`. Command: `GOFLAGS=-p=1 go test ./internal/cli -run 'TestReconcile|TestOwnerlessLocalPool|TestGrantMembers' -count=1`.
- Red step: `TestPolicyPublishAndUpdateReconcileTheSameNode` fails today: both still assign `old.CraftPools` (F5). Mutation check: drop a member from `loosening()`; `TestGrantMembersAreLoosening` must fail.
- Depends on: N1 (reads the carrier fields; thirty lines of fields, so a contract would be N1 itself), A1 and A2 (behaviour: N4 publishes local pools and crafts, so the removals those tasks write must exist first, or a revoked grant stays served).
- Commit: `policy: publish and update reconcile the operator's crafts, pools and cascade into the node (N4)`.
- Reviewer tier: opus with a mandatory independent review (spend grants, policy publish, loosening).

### N2: an installation at the previous version converges through update (cm-node-publish)
- Acceptance: a fixture node and local config at the previous version, with a pool in the node that the local config does not state, an ownerless local pool the node lacks, and a same-named pool that is `legacy_open` in the node and states neither in the local config, ends after one `fugaro update` with the node pool kept (owner and marker intact), the ownerless pool skipped and named, nothing deleted, `update` not failing, and a second `update` a no-op; a fixture with a run in flight keeps its records, ledger and counters (the `TestUpdateLeavesARunInFlightRunning` shape).
- Files: `internal/cli/update_reconcile_converge_test.go` (new), `internal/cli/testdata/node_reconcile/` (new fixtures).
- Interfaces: runs N4's reconcile through the real stage list.
- Tests: `TestUpdateReconcilesFromThePreviousVersion`, `TestUpdateReconcileIsIdempotent`, `TestUpdateReconcileLeavesARunInFlightRunning`. Command: `GOFLAGS=-p=1 go test ./internal/cli -run 'TestUpdateReconcile|TestUpdateLeavesARunInFlightRunning' -count=1`.
- Red step: the first test fails on the tree before N4 (the node's second pool is gone or the local one is absent).
- Depends on: N4 (the test runs the reconcile N4 writes; behaviour).
- Commit: `update: an installation at the previous version converges the reconcile (N2)`.
- Reviewer tier: standard plus the converge test (HARD RULE).

### A1: pool owner, allowed users and legacy-open, and `accounts pool` on the shared path (cm-accounts)
- Acceptance: `accounts pool add NAME --owner EMAIL --allow-user U...` writes those members (`--legacy-open` per D2608's answer: not a creation flag); the command picks its layer through `settingsResolveTarget` and `settingsLayerGate` (it already writes through `applySettingsEdit`, F4); `accounts pool rm` records the name in `node_removals` so the node's copy is removed at the next reconcile (F22); a widening write is `Typed` and refused in an agent session whatever the gate says (D2617); `--layer` is accepted and a layer the pool lacks refuses naming local and project (T3); it ends with the after-write hook; the help text no longer says nodes and local are separate stories (F5); its writers are registered. **WAITS ON D2608** (the flags), D2617 and open tension T3 (the refusal text).
- Files: `internal/cli/accounts_pool.go`, `internal/cli/accounts_pool_test.go`, `internal/cli/accounts_pool_owner_test.go` (new), `internal/cli/writers_pools.go` (new).
- Interfaces: consumes C1 (`RegisterWriter`, `runAfterWrite`); settles nothing for others.
- Tests: `TestPoolAddWritesOwnerAndAllowedUsers`, `TestPoolRmWritesTheExplicitUnset`, `TestPoolAddUsesTheResolverAndTheGate`, `TestPoolAddRefusesAForeignLayerNamingTheRight`, `TestPoolAddEndsWithTheAfterWriteHook`, `TestPoolAddReadsNoStdin`. Command: `go test ./internal/cli -run 'TestPool' -count=1`.
- Red step: `TestPoolAddWritesOwnerAndAllowedUsers` fails with "unknown flag: --owner".
- Depends on: C1 (registers through it; the after-write hook is a stub until I1).
- Commit: `accounts pool: owner, allowed users and legacy-open, on the shared write path (A1)`.
- Reviewer tier: opus with a mandatory independent review (a pool is a spending and data-egress grant).

### A2: crafts and grades access, and a repository's claude accounts (cm-accounts)
- Acceptance: the commands D2613 names write `crafts.<name>.access`, `model_grades.strong|strongest.access` and `repos.<repo>.claude_accounts` (add), at the layers the keys have, through the shared writer, ending with the hook, with writers registered; removing a credential or a craft records the name in `node_removals`; a widening write is `Typed` and refused in an agent session whatever the gate says (D2617). **WAITS ON D2613** (names), D2612 (`accounts_rm.go` is composite AP6's), D2617 and T3.
- Files: `internal/cli/accounts_craft.go` (new), `internal/cli/accounts_craft_test.go` (new), `internal/cli/writers_craft_access.go` (new), `internal/cli/accounts.go`, `internal/cli/accounts_rm.go` (a credential's or craft's removal writes the explicit unset, F22; composite AP6 owns this file too, D2612).
- Interfaces: consumes C1.
- Tests: `TestCraftAccessWritesTheKey`, `TestGradeAccessWritesTheKey`, `TestRepoClaudeAccountsAdd`, `TestCraftAccessRefusesAnUnknownCredentialNamingTheKnown`, `TestAccountsCraftReadsNoStdin`, `TestCredentialRmWritesTheExplicitUnset`. Command: `go test ./internal/cli -run 'TestCraftAccess|TestGradeAccess|TestRepoClaudeAccounts|TestAccountsCraft' -count=1`.
- Red step: `TestCraftAccessWritesTheKey` fails: no such command.
- Depends on: C1.
- Commit: `accounts: crafts', grades' and repositories' access get their writers (A2)`.
- Reviewer tier: opus with a mandatory independent review (access names a credential).

### K1: the credentials cascade writer and the generic allowlist flip (cm-cascade)
- Acceptance: the cascade command D2613 names writes the cascade leaves into the local carrier `credential_cascade` (N1), validates `fallback` as `auto` or `oauth-only`, prints that `auto` spends paid tiers, ends with the hook, registers its writers; `settings set credentials.fallback` now refuses naming that command (item 5), `TestSettingsGenericKeyIsSpec44sAllowlist` loses `credentials.fallback`. **WAITS ON D2613, D2612, D2617 and T3.** A widening write (`fallback auto`) is `Typed` and refused in an agent session whatever the gate says (D2617).
- Files: `internal/cli/accounts_cascade.go` (new), `internal/cli/accounts_cascade_test.go` (new), `internal/cli/settings_set.go`, `internal/cli/settings_set_test.go`, `internal/cli/accounts.go`.
- Interfaces: consumes C1, the carrier N1 adds and the rows R1 registers.
- Tests: `TestCascadeSetWritesTheCarrier`, `TestCascadeFallbackRejectsAnUnknownValue`, `TestSettingsSetCredentialsRefusesNamingTheCascadeCommand`, `TestSettingsGenericKeyIsSpec44sAllowlist`. Command: `GOFLAGS=-p=1 go test ./internal/cli -run 'TestCascade|TestSettingsSetCredentials|TestSettingsGenericKey' -count=1`.
- Red step: `TestSettingsSetCredentialsRefusesNamingTheCascadeCommand` fails today ("no setting has that name").
- Depends on: C1 (contract), W3 (same file `settings_set.go`), R1 (rows), N1 (the carrier fields it writes: about thirty lines, a contract for them would be N1 itself), A2 (same file `accounts.go`).
- Commit: `accounts: the credentials cascade has its command, and settings refuses it (K1)`.
- Reviewer tier: opus with a mandatory independent review (the cascade spends paid tiers).

### K2: the binaries writer over the allow rule's carrier (cm-cascade)
- Acceptance: the command D2613 names writes `binaries.allow` and `binaries.max_bytes` into the carrier binary-allow-rule AR1 adds, validates a pattern at write (a misspelt pattern refuses), ends with the hook, registers its writers. If AR1 already adds settings rows the task reduces to writers and the refusal text (U3). **WAITS ON D2613, D2612 and D2617** (it starts only after #2782 merges). A widening write (an allow pattern) is `Typed` and refused in an agent session whatever the gate says (D2617).
- Files: `internal/cli/project_binaries.go` (new), `internal/cli/project_binaries_test.go` (new), `internal/cli/project_cmd.go`.
- Interfaces: consumes C1 and AR1's `localcfg.BinariesAllow/BinariesMaxBytes`.
- Tests: `TestBinariesAllowWritesTheCarrier`, `TestBinariesAllowRefusesAMisspeltPattern`, `TestBinariesMaxBytesWrites`. Command: `go test ./internal/cli -run 'TestBinaries' -count=1`.
- Red step: `TestBinariesAllowWritesTheCarrier` fails: no such command.
- Depends on: C1 (contract), N4 (same file `project_cmd.go` and the reconcile that publishes the carrier).
- Commit: `project binaries: the allow rule gets its writer (K2)`.
- Reviewer tier: opus with a mandatory independent review (what a run may add to the repository).

### M1: `models use` writes every role and every layer (cm-models)
- Acceptance: `models use` accepts `diagnose`, `agent.model`, `agent.recipe`, `agent.levels.<l>.model|effort`, and `--layer repo|project|local` (today it writes a repository working tree only), choosing its layer through `settingsResolveTarget` and `settingsLayerGate`; `layer.defaults.agent.models.*` and `layer.claude_models` are reachable, and `layer.claude_models` (which models accounts may serve) is a widening, `Typed` and refused in an agent session whatever the gate says (D2617); writers registered; ends with the hook for the project layer. **WAITS ON D2617.**
- Files: `internal/cli/models_use.go`, `internal/cli/models_use_test.go`, `internal/cli/models_use_roles_test.go` (new), `internal/cli/writers_models_use.go` (new).
- Interfaces: consumes C1.
- Tests: `TestModelsUseWritesEveryRole`, `TestModelsUseLayerFlag`, `TestModelsUseDiagnoseRole`, `TestClaudeModelsWideningRefusedInAnAgentSession`, `TestModelsUseReadsNoStdin`. Command: `go test ./internal/cli -run 'TestModelsUse' -count=1`.
- Red step: `TestModelsUseDiagnoseRole` fails today (`models use` accepts coder, reviewer, background, vision only).
- Depends on: C1.
- Commit: `models use: every role, every layer (M1)`.
- Reviewer tier: opus with a mandatory independent review (a widening write path).

### M2: `models provider` and price writers (cm-models)
- Acceptance: the commands D2613 names set and remove `providers.<name>.*` (kind, base_url, auth, price_policy, require_zdr, route_fee_pct, allow_data_to) and the other `model_prices` leaves; `providers.*.secret` is set by naming a stored secret, never by value; a value that looks like a secret on argv refuses; writers registered; ends with the hook. **WAITS ON D2613 and D2617.** `base_url`, `allow_data_to`, `require_zdr` and `route_fee_pct` writes are `Typed` and refused in an agent session whatever the gate says (D2617).
- Files: `internal/cli/models_provider.go` (new), `internal/cli/models_provider_test.go` (new), `internal/cli/writers_providers.go` (new), `internal/cli/models.go`.
- Interfaces: consumes C1.
- Tests: `TestProviderSetWritesEveryLeaf`, `TestProviderSecretIsAReferenceNeverAValue`, `TestPriceSetWritesTheLeaves`, `TestProviderReadsNoStdin`. Command: `go test ./internal/cli -run 'TestProvider|TestPriceSet' -count=1`.
- Red step: `TestProviderSecretIsAReferenceNeverAValue` fails: a literal value is accepted.
- Depends on: C1.
- Commit: `models: providers and prices get their writers (M2)`.
- Reviewer tier: opus with a mandatory independent review (credential-bearing fields).

### M3: `rigor set` reaches the repository layer (cm-models)
- Acceptance: `rigor set` takes `--layer` and `--repo`; repo `rigor.default` and `rigor.areas` are written to the checkout's `fugaro.yaml` (live when merged; nothing committed, composite D1564); `settings` refuses them naming that command with the working line; writers registered. **WAITS ON D2612** (#2576 and #2538 edit rigor files), D2617 and T3. A widening rigor write is `Typed` and refused in an agent session whatever the gate says (D2617).
- Files: `internal/cli/rigor.go`, `internal/cli/rigor_repo_layer_test.go` (new), `internal/cli/writers_rigor.go` (new).
- Interfaces: consumes C1.
- Tests: `TestRigorSetRepoLayerWritesTheCheckout`, `TestRigorSetRepoDefaultCannotLoosen`, `TestRigorSetLayerFlag`. Command: `go test ./internal/cli -run 'TestRigorSet' -count=1`.
- Red step: `TestRigorSetRepoLayerWritesTheCheckout` fails: no `--repo` flag.
- Depends on: C1.
- Commit: `rigor set: the repository layer (M3)`.
- Reviewer tier: opus with a mandatory independent review (rigor decides review depth).

### M4: the security paths writer (cm-models)
- Acceptance: the command D2613 names adds and removes `layer.security.*` and repo `security.patterns` entries by their key field (config-management D1163), at the layers they have, with the project-layer loosening judged as every other raise; writers registered. **WAITS ON D2613, D2612 and D2617.** A removal of a security path is `Typed` and refused in an agent session whatever the gate says (D2617).
- Files: `internal/cli/rigor_security.go` (new), `internal/cli/rigor_security_test.go` (new), `internal/cli/writers_security.go` (new), `internal/cli/rigor.go`.
- Interfaces: consumes C1 and R1's rows.
- Tests: `TestSecurityPathAddAndRemoveByName`, `TestSecurityPathRemovalIsAProjectLoosening`. Command: `go test ./internal/cli -run 'TestSecurityPath' -count=1`.
- Red step: `TestSecurityPathAddAndRemoveByName` fails: no such command.
- Depends on: C1, M3 (same file `rigor.go`), R1 (the rows its test reads).
- Commit: `rigor security: security paths get their writer (M4)`.
- Reviewer tier: opus with a mandatory independent review (security paths).

### I1: integration: the stub deleted, the conformance test strict, the end-to-end test (cm-conformance)
- Acceptance: `after_write_stub.go` is deleted and `runAfterWrite` is N3's function in every semantic command; the conformance ratchet is strict (a golden line that passes fails the test) and the golden holds only labelled or intentionally open rows; the end-to-end test proves, for pools, crafts, the cascade and binaries, that after the command, after `project policy publish` and after `update`, the local config and the node agree, and that each new command ran with stdin closed and waited on nothing.
- Files: `internal/cli/after_write_stub.go` (deleted), `internal/cli/writer_conformance_test.go`, `internal/cli/testdata/writer_gaps.golden`, `internal/cli/config_ownership_e2e_test.go` (new).
- Interfaces: wires C1's hook to N3 and the registry to every family.
- Tests: `TestConfigOwnershipEndToEnd`, `TestWriterConformanceStrict`, `TestNoStubSurvives`. Command: `GOFLAGS=-p=1 go test ./internal/cli -run 'TestConfigOwnershipEndToEnd|TestWriterConformance|TestNoStubSurvives' -count=1`.
- Red step: `TestNoStubSurvives` fails while `after_write_stub.go` exists.
- Depends on: C1, T1, N3, L1 and every family task (A1, A2, K1, K2, M1, M2, M3, M4) and W1, W2, W3, R1, N1, N4, N2: one integration covers every edge C1 broke.
- Commit: `cli: config ownership end to end (I1)`.
- Reviewer tier: opus with a mandatory independent review.

### D1: docs, the working skill and the release notes (cm-docs)
- Acceptance: `docs/project-layer.md` describes the working `--layer project` spelling and the default-layer rule; `docs/settings-commands/audit.md` carries a banner naming this package as the live audit (not rewritten); `plugin/skills/working/SKILL.md` names the commands of D2613 and the layer spelling within its 24 KiB; the release notes' `### For operators` is "run `fugaro update`; the first update publishes the pools, crafts and credentials your local config states to the policy node, additively, and deletes nothing; it asks nothing"; the one-line in-flight note is present; `website/data/release-summaries.yaml` gets a line (no backticks).
- Files: `docs/project-layer.md`, `docs/settings-commands/audit.md`, `plugin/skills/working/SKILL.md`, `docs/releases/<next unreleased version>.md`, `website/data/release-summaries.yaml`.
- Interfaces: describes the behaviour W1-W3, N1, the families and R1 land.
- Tests: `go test ./internal/cli/ -run Docs -count=1`; `go test ./plugin -run 'TestPlugin|Skill' -count=1`; `npm run check` in `website/` (about 7 minutes; run once).
- Red step: the docs test that pins `docs/project-layer.md`'s scope table fails until the spelling sentence and the pinned fact are changed together.
- Depends on: W1, W2, W3, R1, N1, N4, A1, A2, K1, K2, M1, M2, M3, M4 (the text names what they land).
- Commit: `docs: config ownership, the working skill and the release notes (D1)`.
- Reviewer tier: standard; the skill edit routes the pull request to in-run level 3 by the path rule (plugin skills).

## 4. Parallel execution

Levels, from `fugaro plan graph`:

| Level | Tasks (run at once) |
| --- | --- |
| 0 | C1, W1, R1, N1 |
| 1 | T1, N3, L1, W2, A1, A2, M1, M2, M3 |
| 2 | W3, M4, N4 (CR1, if raised) |
| 3 | K1, K2, N2 (CR2, if raised) |
| 4 | I1, D1 |

Critical path (5 tasks): **C1 → A1 → N4 → K2 → D1**. What must be sequential: `settings_set.go` is written by
W1, W2, W3, K1 in four levels (one file, one change split in four); `registry.go` by R1 then W2; `rigor.go` by
M3 then M4; `accounts.go` by A2 then K1; `project_cmd.go` by N4 then K2.

### Edges broken by contract and edges kept

- **Broken through C1** (the contract: the writer registry and the after-write hook), each integrated in I1:
  every family (A1, A2, K1, K2, M1, M2, M3, M4) and L1 no longer wait on T1, because they needed the
  registry's shape and not the conformance harness; every family no longer waits on N3, because they needed
  the after-write signature and not the stage wiring; T1 does not wait on N3 and N3 does not wait on N1, for
  the same reason. 19 edges broken, 4 tasks added (C1, I1, CR1, CR2).
- **The gain the read reports:** before the pass 7 tasks on the path, weight 15; after, 5 tasks, weight 9.5.
- **Edges kept, with the reason a contract would not do:**
  - W2 after W1, W3 after W2, K1 after W3: one file, `settings_set.go`; the later change reads the earlier's
    resolver and rows. W3 after T1: the refusal prints the table T1 fills (data, not a signature).
  - W2 after R1: same file, `registry.go`. M4 after R1: its test reads R1's rows. K1 after R1 and N1: K1 writes
    rows R1 registers and carrier fields N1 adds (about thirty lines of fields; a contract for them is N1).
  - M4 after M3: same file, `rigor.go`. K1 after A2: same file, `accounts.go`. K2 after N4: same file,
    `project_cmd.go`, and N4's reconcile is what publishes the carrier.
  - N4 after N1: N4 reads the carrier fields N1 adds (a contract for them would be N1). N2 after N4: the convergence test runs the reconcile N4 writes.
  - D1 after every behaviour task: it documents what they land. D1 does not wait on I1 (it describes
    behaviour, not the stub), so the two run in one level.
- **Contract changes anticipated:** CR1 (the Writer needs a per-layer or per-store argv or another store name)
  reaches A1, A2, K1, K2, M1, M2, M3, M4, L1; CR2 (the after-write options need the sentence helper or
  `--no-apply` when composite AP3 lands) reaches the same families. A launched dependent is reached by a
  follow-up run on its pull request naming `docs/contracts/config-ownership.md` by path, never pasting it.
- **Decoupling pass:** `planning.decoupling` is on (`fugaro settings planning.decoupling --json --repo
  dimipaun/fugaro`, 2026-11-15), the repository's rigor default is `startup` with no area rules, so the
  pass ran aggressively; the contract C1 is reviewed independently before any side is written against it.

### Shared files (no level holds two writers)

| File | Writers (level) |
| --- | --- |
| `internal/cli/settings_set.go`, `settings_set_test.go` | W1 (0), W2 (1), W3 (2), K1 (3) |
| `internal/settings/registry.go` | R1 (0), W2 (1) |
| `internal/cli/rigor.go` | M3 (1), M4 (2) |
| `internal/cli/accounts.go` | A2 (1), K1 (3) |
| `internal/cli/project_cmd.go`, `init_policy_publish.go` | N4 (2), K2 (3) (`init_policy_publish.go`: N4 only) |
| `internal/cli/accounts_rm.go` | A2 (1) only |
| `internal/cli/sharedcfg.go`, `internal/localcfg/localcfg.go` | N1 (0) only |
| `internal/cli/models.go` | M2 (1) only |
| `internal/cli/writer_conformance_test.go`, `testdata/writer_gaps.golden` | T1 (1), I1 (4) |
| `docs/contracts/config-ownership.md`, `after_write.go`, `settings/writer.go` | C1 and CR1/CR2 only |
| `after_write_stub.go` | C1 creates, I1 deletes |
| docs, skill, release files | D1 (4) only |

### Sequencing against open pull requests and composite-commands (D2612, open)

| Collides with | Files | Tasks held (WAITS ON D2612) |
| --- | --- | --- |
| #2782 (AR1) | `project_cmd.go`, `init_policy_publish.go`, `localcfg.go`, `sharedcfg.go`, `sharedcfg_test.go`, `policy.go`, `registry.go`, `schemas/settings.schema.json`, `docs/project-layer.md` | N1, N4, K2, R1, W2, D1 |
| #2753 (S5) | `project_cmd.go`, `init_policy_publish.go` | N4 |
| #2732 | `localcfg.go` | N1 |
| #2498 (E6) | `policy.go`, `registry.go` | R1 |
| #2576, #2538 (rigor) | `rigor.go`, `internal/runner/rigor.go` | M3, M4 |
| composite AP5 (not started) | `settings_set.go` (AP5 adds `internal/settings/steps.go`; the merged table is `internal/apply/steps.go`) | W1, W2, W3, K1 |
| composite AP6 (not started) | `accounts_rm.go`, `project_cmd.go`, `models_add.go`, `models_rm.go`, `workspace.go` | A2, N4, K2 (the plan edits none of the others) |
| composite AP7 (not started) | refusal wording, `plugin/skills` | W3, D1 |
| composite AP8 (not started) | console spawn files | W1, only if the argv of `settings.edit` changes |

Ready to start the moment the owner says go (no unanswered decision gates them): C1, then T1. W1 and R1 are level 0 but wait on D2612. L1 waits on D2611; A1 and N4 on D2608; A1, A2, K1, K2, M1, M2, M3, M4 on D2617; N1, N4, M3, M4, K1 also on D2612; A2, K1, K2, M2, M4 on D2613; N3 on D2614 and D2615; W2 on D2615.

## 5. Coverage: every decision and finding lands in a task

| Item | Task |
| --- | --- |
| D2601 | the module map in `reconciliation.md` section 7; module ids on every task |
| D2602 | N3 (the stage), every family task (the hook), I1 (the wiring); composite D1566 amendment recorded in the reconciliation |
| D2603 | R1 (the scalar and `--add-entry` rows and their owners), W3 (the allowlist admits them); the `secrets` missing writes wait on D2616, section 6 |
| D2604, F1 | W2 |
| D2605, F17, T4 | W1 |
| D2606, F9 | W1, D1 (the doc) |
| D2607, F5, F7 | N1, N2, N3, N4 |
| D2608, F8 | A1 (waits) |
| D2609, D2610, F2 | C1, T1, I1 |
| D2611, T5, F18 | L1 (waits) |
| D2612 | section 4 above |
| D2613 | A2, K1, K2, M2, M4 (wait) |
| D2614 | N3 (wording) |
| D2615, F19 | N3, W2 (wait) |
| D2616, F21 | no task until answered (open item) |
| F20 | N4, N2 |
| F22 | N1 (the record), A1, A2 (write it), N4 (consume) |
| D2617, F24 | A1, A2, K1, K2, M1, M2, M3, M4 (wait); N4 for the owner-terminal side |
| F23 | N1 |
| F3 | K1 |
| F4 | A1 (pools onto the shared writer), M1 (models use) |
| F6 | K2, K1 |
| F10 | M3 |
| F11 | L1 (labels), the owner vocabulary G12 started |
| F12 | W2 (the governance rows' Doc note: "enforced by the ee build") |
| F13 | R1 |
| F14, F15 | the sequencing table above |
| F16 | D1 |
| T2 | K1 |
| T3 | A1, A2, K1, M3 (waits) |

## 6. Considered and declined

- **`secrets` missing writes (not scheduled)** (`bitbucket_tokens.*`, `secret_expires`, `secret_rotation_days`,
  `batch.secrets`): D2603 gives `secrets` its missing writes, but the writers belong to the open
  `bitbucket-token-types` and the secrets plans; of bitbucket-token-types only T11 (`creds set`) writes `bitbucket_tokens`, and no plan
  writes `secret_expires`, `secret_rotation_days` or `batch.secrets` (F21); this is decision D2616. This plan registers the labels (L1) and the rows (R1); the writers are not a task of
  this plan until the owner answers D2616. This is the one part of D2603 the plan does not
  schedule: an **open item**, not a settled decline.
- **`orchestrator.*`, `issues.nudge_*`, `triage`, `verifier_accounts`, `mobile_allow`, `bitbucket_api_roots`:**
  per D2603 these are settings rows. R1 lists those with a clear key shape; the object-list ones
  (`mobile_allow`, `decision_model_per_class`) are written with `--add-entry`, which exists
  (config-management G1). Whether each key's row is enough or needs a writer is what T1's golden measures;
  rows whose test fails stay in the golden with a reason rather than growing a task here.
- **A command-per-concept for `orchestrator` or `mobile`:** declined by D2603.
- **`fugaro apply`, the pending file and `--no-apply`:** composite AP3; not built here (D2602).
- **A node version bump:** not planned (shapes unchanged, U7); N1 stops and asks if it finds one is needed.

## 7. Checks and after-deploy steps

- **Checks taken from the reserved block:** none (D2600-D2639 are decisions; no check number is reserved). No
  owner-run live check is planned.
- **After-deploy:** the release notes' `### For operators` is "run `fugaro update`" (D1). It publishes once the
  pools, crafts, credentials and cascade values the local config states, additively, asks nothing, and says
  in one line when runs are running; the steps that can still reach a run are the ones named in the
  `update-in-flight` design (the credentials service re-reads the node at its next call).

## 8. Runs, count and worst case

Proposed, not launched: 21 task runs (C1, then T1 and M1 first), 2 contract-change runs held in reserve (CR1, CR2),
and an independent `fugaro review --pr N` for each task marked opus (C1, N3, W2, N4, A1, A2, K1, K2, M2, M3,
M4, I1: 12). At the repository's per-run ceiling of $10 (`fugaro settings budget.per_run_usd --repo
dimipaun/fugaro`), the worst case is 21 runs and 12 reviews at $10 each = $330, plus $20 if both
contract changes are needed and any follow-up the review rounds send. None of it starts until the owner
says yes.
