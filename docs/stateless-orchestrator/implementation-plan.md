# The stateless orchestrator: the implementation plan

**Status: plan only; nothing is implemented; no task approved.** Written 2026-11-04 against `origin/main` at `5998d67c7`. **Round 1 of the independent
security review (verdict "fix first") is applied in this revision**: the corrections are in reconciliation section 3 (the rules do not bind a person's
write; person-only is a mitigation; the claim takes no key), in G2, G4, G5, G11, O0-O12 below, and in the new owner questions D1241-D1243 (section 6).

## 1. Opening and status

This plan builds [spec.md](spec.md) (the owner's spec, verbatim) **as amended by [reconciliation.md](reconciliation.md)**: where the two
disagree the reconciliation wins. Nothing here is implemented. **No task is approved: each needs the owner's explicit go, by name.**
Findings are cited as F#, decisions as D#, open tensions as T# (reconciliation section 6).

**The owner's answers of 2026-11-04 that shaped this plan** (all the recommendation except D1235):

| Decision | What the plan builds | Binds |
| --- | --- | --- |
| D1220 | State in the Realtime Database, a new `orchestrator/` subtree, one state node per orchestrator written with `PutIfMatch`; no Firestore | O2, O3 |
| D1221 | The first slice is a local deterministic daemon, no model call | slice 1 |
| D1222 | Core: schema, validator, single-user daemon. `ee/`: shared agents, handoff, batch approval, workspace caps | O0-O11 core; O17-O19 `ee/` |
| D1223 | `fugaro orchestrator state / events / patch --json` before any MCP tool | O5 |
| D1224 | MCP run names kept; the work item is `work_item`, never "task"; state group `get_state`, `get_events`, `apply_patch` | O0, O5, O16 |
| D1225 | Only a person-class identity resolves a person's decision or sets `goal_set`; at the CLI that means at least a Typed confirmation at a real terminal, residual stated (reconciliation section 3, D1242) | O0, O5 |
| D1226 | A cycle spawns only an already-approved work item, through the single launch path, capped | O6 |
| D1227 | New scopes `state:read`, `state:write` (a later edit to the MCP design, T3) | O16 |
| D1228 | Local `claude -p` with max-turns, a pinned `max_output_tokens`, 6 per hour and 40 per day, a breaker after 3 failed or empty cycles | O12 |
| D1229, D1230 | Level-triggered v1; a small audit log with deterministic ids and a server-assigned order | O3, O7 |
| D1231 | Intent first, recording the minted run id and the launch-spec digest, launch under that run id, adopt on recovery, then commit | O6 |
| D1232 | launchd and a systemd user unit, one instance by an RTDB heartbeat lease; no Windows | O3, O9 |
| D1233 | No `summary_replace`; the summary is generated on read | O0 |
| D1234 | The security-sensitive class is deterministic code over changed paths; the model per class is a tightening-only local key | O1, O4, O12 |
| **D1235 (not the recommendation)** | **Cut over at once, no shadow week** | O11, T1 |
| D1236 | The two owner-verbatim specs are not edited; the README notes are written (added by this branch, not yet on `main`) | done |
| D1237 | One session-start hook slot shared with `docs/plugin-user-scope` | O10 |
| D1238 | The daemon and decision agents never run a gated converge or publish command | O8, O12 |
| D1239 | Project-policy guardrail fields, tightening-only, conservative defaults | O4 |
| D1240 | The local daemon may ship before the MCP chain; MCP-dependent parts are 0.21.0 or later | slices |

**Open tensions the plan must not resolve** (reconciliation section 6): T1 (no shadow week, a rollback is the owner's to rule), T2 (the
launch in v1 is the local `fugaro run` path, never an MCP `spawn_task`; the state group must not depend on it), T3 (the new scopes are an
edit to the MCP design), T4 (MCP-dependent work waits for the MCP threat-model amendment), T5 (the workspace design reuses MCP).

## 2. Conventions

Read first: `CLAUDE.md`, `.claude/rules/ee.md` (any `ee/` task), `.claude/rules/credentials-service.md` (O3, O12 touch writes and credentials
adjacent code), `.claude/rules/terraform.md` (not needed: no Terraform). Standing rules, each pinned by a task (section 3 names it):

- **No secret-shaped literal** in any test, doc, log or fixture. Fixtures use marker values.
- **No agent performs a live cloud action.** Checks 300-305 are written by agents and **run by the owner**. Never against project `belong`
  from an agent run.
- **Hermetic tests**: `internal/gcpfake` (its RTDB fake honours ETag and if-match), `internal/testutil`, `t.TempDir()`; the rules emulator
  tests are the repo's existing pattern (`internal/budget/rules/emulator_*_test.go`).
- **`fugaro update` is the whole upgrade (the HARD RULE).** O2 ships its convergence inside `update`, with a test that an installation at
  the previous rules generation converges in one run; the release's `### For operators` is "run `fugaro update`" plus what it asks. The daemon
  itself is an opt-in feature, not a repair, so `fugaro orchestrator install` is not an operator step that breaks anything. Whether `update`
  should also *offer* it behind its own confirmation is an owner question (Q2, section 6).
- **`ee/` boundary**: nothing outside `ee/` imports `ee/`; `go test ./internal/eeboundary/ -count=1` after any `ee/` or registration change; the
  core builds and tests with `ee/` deleted; `TestCoreNeverImportsEE` is the standing guard, and no core task here imports `ee/`.
- **A field never changes meaning**: every new wire shape (state, patch, `--json` output, policy keys) is additive with a `schema_version` and a
  schema in `schemas/` (F21); the work item's fields (`phase`, `rigor_tier`, `assignee`) are named in O0 so that none equals a run-record or
  MCP field with a different meaning.
- **Release notes**: a task that changes what `fugaro update` does adds its line to the release notes' `### For operators` in the same PR
  (`docs/releases/README.md`); the file is `docs/releases/vX.Y.Z.md` of the release the owner assigns under D1240 (O2 creates the entry).
- **Comments explain why**; no TODO, no section headers. A fix round adds no functionality.
- **Plugin tests** check every command and flag a skill names against the CLI: O5 and O10 move together (O10 depends on O5).
- **Docs tests**: a task that edits a doc runs `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs' -count=1`.

### Global constraints and Review focus (each pinned by a task)

| # | Constraint a reviewer holds every task to | Pinned by |
| --- | --- | --- |
| G1 | The LLM never writes state: typed ops only, validated by code (spec) | O0 `TestUnknownOpIsRefused` |
| G2 | A `waiting_on: person` decision, an approval and `goal_set` are closed only by a person-class actor, which at the CLI needs a Typed confirmation at a real terminal (`CanConfirm(Typed)`); the agent-marker check is a mitigation, not a barrier (`stage.go` `AgentAdvice`), so an `env -u CLAUDECODE` call or a direct node write is the residual, owner question D1242 (D1225, F20) | O0 `TestAgentActorCannotResolveAPersonDecision`, O5 `TestPersonOpsNeedATypedConfirmation`, `TestPatchFromAnAgentSessionRefusesPersonOps` (the accident only) |
| G3 | Free text (`facts`, `note`, titles, decision text) is data: length-capped, control characters stripped, fenced as untrusted in **every** surface that prints it into a session or a prompt, from slice 1 (O5's `state --brief`, O10's session-start print) and in O12's prompts, never above the system prompt | O0 `TestFactValuesAreCappedAndCleaned`, O5 `TestStateBriefFencesAndCleansStoredText`, O10 `TestSessionStartPrintFencesStoredText`, O12 `TestUntrustedTextIsFenced` |
| G4 | A cycle launches only an approved work item under the **full launch spec the person approved** (repo, recipe, rigor tier, brief and task text, bound by a digest in the approval; any edit clears the approval); caps per cycle, hour, day, running and per minute are code, derived from committed intents (D1226, D1239) | O0 `TestEditAfterApprovalRevokes`, O6 `TestSpawnNeverLeavesTheApprovedItem`, `TestCapsStopALoop`, `TestCapsSurviveARestart` |
| G5 | Money is at-most-once: the intent, with the minted run id, is committed before the launch; recovery adopts an existing `task.json`/`launch.json` and never issues a second launch (the claim takes no key, so it is not the fence) (D1231) | O6 `TestRecoveryAdoptsTheRecordedRunID`, O8 `TestCrashBetweenIntentAndLaunchMakesOneRun` (real runstore and gcpfake) |
| G6 | The security-sensitive class is code over changed paths, never lowered by config or by the model (D1234) | O1 `TestPromptPathsAreAlwaysSensitive`, O4 `TestModelKeyOnlyTightens` |
| G7 | Kill switch, `Precheck` and spend caps are always consulted; the daemon cannot widen a ceiling | O6 `TestKillSwitchStopsSpawning` |
| G8 | The daemon and decision agents never run a gated converge or publish command, never pass `Yes` to a confirmation, and never answer a refusal with an action (D1238, F20) | O8 `TestDaemonDoesNotDependOnGatedCommands` (an import and dependency test), `TestDaemonNeverPassesYes`, O12 `TestDecisionAgentRunsWithTheAgentDenyRules` |
| G9 | One daemon per orchestrator id; a stale writer's write is refused, never forced (D1232, F5); `state`, `lease` and `audit` are **siblings** under `orchestrator/<id>` (a PUT of the parent wipes children and every heartbeat would move a shared ETag) | O3 `TestTwoDaemonsOneLease`, `TestHeartbeatDoesNotMoveTheStateETag` |
| G10 | A failed or empty cycle is a visible state (a `pending_decisions` item), never a silent no-op (owner lesson 22) | O6 `TestBreakerRaisesADecision`, O12 `TestEmptyCycleCountsAsFailed` |
| G11 | A rules, policy, state-schema or daemon-unit change is converged by `fugaro update` from the previous generation (HARD RULE) | O2 `TestUpdateConvergesTheOrchestratorRulesFromThePreviousGeneration`, O0 `TestMigrateFromThePreviousSchemaVersion`, O9 `TestUpdateRerendersAndRestartsAPreviousVersionUnit` |

**Reviewer tier.** *Security*: `security-reviewer` (Opus), a mandatory independent review, at least one mutation check, routed by changed
paths (rules, guards, the launch path, prompts and plugin skills are security paths per the owner's routing rule). *Light*: `light-reviewer`.
Each task states its tier.

## 3. Slices and tasks

**Slice 1: the local deterministic daemon (D1221): O0-O11.** It retires the owner's sweep, the Monitor watcher and the queue scripts (F18,
U3) and makes **no model call**. **Slice 2: the decision agent and the full cycle (D1228): O12-O13.** **Slice 3: the MCP state group and the
`ee/` shared parts: O16-O19.** O12 is slice 2, not slice 1: D1221 puts the first slice at no model call, the decision agent's system prompt
is unwritten and the bounds D1228 names are proposals not measurements (U4), so a first slice carrying it would carry the unmeasured part;
the slice 1 daemon already contains the circuit-breaker and spawn caps the decision agent will run under (O6).

Checks come from the reserved block **300-309**, verified free on 2026-11-04: the highest 3-digit Check in `docs/` on `origin/main` is 290, and
no open pull request diff adds one in 300-399. **Checks 306-309 stay reserved.**

Common to every task: the **Command** is a targeted `go test` with `-count=1`, then `go build ./... && gofmt -l . && go vet ./...`.
Tasks are mergeable one at a time. The commit message is `type(scope): subject (Oz)` and carries the attribution trailers.

### O0. The state schema, the typed patch and the validator (core)

- **Files (11):** `internal/orchestrator/state.go` (new), `internal/orchestrator/patch.go` (new), `internal/orchestrator/validate.go` (new),
  `internal/orchestrator/summary.go` (new), `internal/orchestrator/state_test.go` (new), `internal/orchestrator/patch_test.go` (new),
  `internal/orchestrator/summary_test.go` (new), `internal/orchestrator/export_test.go` (new), `internal/orchestrator/testdata/state-a.json`
  (new), `schemas/orchestrator-state.schema.json` (new), `schemas/orchestrator-patch.schema.json` (new).
- **Interfaces:** `type State struct{ SchemaVersion int; Rev int64; AgentID, Role, Goal string; WorkItems []WorkItem; Decisions []Decision;
  Facts map[string]Fact; ... }`. `WorkItem{ID, Title, Repo, Recipe, RigorTier, Brief, DependsOn, Phase, RunID, Approved *Approval, Attempts, Note}`: **`Phase`
  is `queued|running|review|done|failed|blocked|cancelled`**, not `status`; the run's own state stays the run record's. `Approval{By, At, SpecDigest}`: **the digest covers the full launch spec** (repo, recipe, rigor tier, brief and task text, `DependsOn`);
  any `work_item_upsert` that changes a digested field clears `Approved`, so a later upsert cannot rewrite a brief under a standing approval.
  `Intent{ID (item + attempt), RunID (minted before the start), SpecDigest, At (server time), Outcome}` and `Counters` (the per-hour and per-day
  decision-call counts, O12) are **fields of `State`, named here** (`Intents []Intent`, `Counters Counters`); O6 and O12 use them and edit neither
  `state.go` nor the schema. `Decision{ID, Question,
  WaitingOn (person|session|daemon), ...}`. `Fact{Value, By}` carries provenance (`by`: person, session, daemon, decision-agent). `Patch{BaseRev, Ops}`
  with ops `work_item_upsert`, `work_item_archive`, `decision_add`, `decision_resolve`, `fact_set`, `fact_delete`, `goal_set` (no
  `summary_replace`, D1233). `func Apply(State, Patch, Actor) (State, error)` with `Actor{Class: Person|Session|Daemon|DecisionAgent}`;
  `func Summarize(State) string` generated on read, capped, **free text fenced and cleaned** (G3). `func Migrate(State) (State, bool)` raises an
  older `schema_version` to the current one in place (the store and `fugaro update` call it). Imports no `ee/`, no cloud package (pure).
- **Schema/rules/config change:** two new JSON Schemas; the decode is strict (unknown field is an error). No rules, no config.
- **Tests:** `TestStateRefusesAnUnknownField`, `TestUnknownOpIsRefused`, `TestStaleBaseRevIsRejected`, `TestAllowedPhaseTransitions`,
  `TestAgentActorCannotResolveAPersonDecision`, `TestOnlyAPersonSetsGoalOrApprovesAWorkItem`, `TestFactValuesAreCappedAndCleaned`
  (uses `internal/safetext`), `TestWorkItemFieldsAreNotTheRunRecordsFields`, `TestSummaryIsGeneratedAndCapped`, `TestSchemasMatchTheStructs`,
  `TestEditAfterApprovalRevokes` (an upsert touching rigor, brief, repo, recipe or `DependsOn` clears the approval), `TestApprovalBindsTheFullLaunchSpec`,
  `TestMigrateFromThePreviousSchemaVersion`.
- **Command:** `go test ./internal/orchestrator/ -count=1 && go test ./schemas/ -count=1`
- **Depends on:** none. **Level 0.**
- **Commit:** `feat(orchestrator): the state schema, the typed patch and its validator (O0)`
- **Red step:** `TestAgentActorCannotResolveAPersonDecision` fails to build (package absent); then fails when `Apply` lets any actor close a
  `waiting_on: person` item.
- **Reviewer:** security (G1-G3 live here; a stored-prompt-injection carrier).

### O1. The deterministic security-sensitive classifier

- **Files (4):** `internal/orchestrator/sensitivity/classify.go` (new), `internal/orchestrator/sensitivity/classify_test.go` (new),
  `internal/orchestrator/sensitivity/testdata/paths.txt` (new), `internal/orchestrator/sensitivity/export_test.go` (new).
- **Interfaces:** `func Classify(changedPaths []string) Class` (`Routine|Sensitive`), by path only. **An allowlist, not a denylist:** `Routine` only
  when **every** path matches an allowlisted routine prefix (`docs/**` except `docs/pending-workflows/**`, `website/**`, `mobile/**`, the console
  renderer; a `_test.go` file is routine only inside an allowlisted tree, so a test edit in `internal/orchestrator`, `internal/initflow`,
  `internal/agentenv`, `internal/managed`, `schemas/`, `ee/` or `.claude/` is Sensitive). **Everything else, and every unknown, unmatched or malformed
  path, is Sensitive**, so the classifier's own package, the daemon and the guards default to Sensitive. The owner's routing rule (credentials, writes
  and guards, the managed gate, Terraform and IAM, the agent environment and gateway, agent prompts, recipes, `CLAUDE.md`, `AGENTS.md`, `.claude/**`,
  plugin skills, CI) is a test fixture the allowlist must never contradict. No input but paths: a title or description cannot lower it.
- **Schema/rules/config change:** none.
- **Tests:** `TestPromptPathsAreAlwaysSensitive`, `TestDescriptionCannotLowerTheClass` (the signature has no description argument; a
  reflective test pins it), `TestEmptyPathListIsSensitive` (fail closed), `TestUnlistedPathIsSensitive`, `TestOnlyAllowlistedPathsAreRoutine`,
  `TestTestEditsInGuardTreesAreSensitive`, `TestRoutingRuleFixtureIsNeverRoutine`, `TestRulesTableMatchesTheReviewerRoutingText`.
- **Command:** `go test ./internal/orchestrator/sensitivity/ -count=1`
- **Depends on:** none. **Level 0.**
- **Commit:** `feat(orchestrator): the path-based security-sensitive classifier (O1)`
- **Red step:** `TestEmptyPathListIsSensitive` fails to build, then fails when an empty list returns `Routine`.
- **Reviewer:** security.

### O2. The `orchestrator/` subtree, its rules and the `fugaro update` convergence

- **Files (7), and the emulator and parity tests carry `//go:build firebase`:** `internal/budget/rules/rules.go`, `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/marker.go`,
  `internal/budget/rules/emulator_orchestrator_test.go` (new), `internal/budget/rules/parity_test.go`, `internal/cli/update_converge_test.go`,
  `docs/releases/vX.Y.Z.md` (the release the owner assigns; created or amended).
- **Interfaces:** a top-level `orchestrator/<id>/` subtree: `.read` for the signed-in person, a `.write` that **refuses every run-scoped job identity** and
  validates shape and size for a token-based writer; **the rules do not bind a person's OAuth write** (IAM bypasses them), so this task claims no
  write-own guarantee and the writer identity is **owner question D1241**, decided before this task starts. Siblings under `orchestrator/<id>`:
  `state`, `lease` and `audit` (a PUT of the parent would wipe children and a heartbeat would move a shared ETag). **Rules generation:** a marker of its own, on the credentials cascade's precedent (`marker.go`), **not** a `RulesVersion`
  bump, because a bump drops every published policy on an installation that has not redeployed. Task O2 confirms or reverses this in its red step.
- **Schema/rules/config change:** the rules template and marker; `fugaro update`'s rules stage writes the marker after the PUT.
- **Tests:** emulator: `TestOrchestratorSubtreeIsReadableOnlyBySignedInPeople`, `TestOrchestratorWritesRefuseAJobIdentity`,
  `TestOrchestratorRejectsAnOversizeOrMalformedState`; `TestUpdateConvergesTheOrchestratorRulesFromThePreviousGeneration` (an installation at the
  previous generation converges in one `fugaro update` run, nothing else needed), `TestUpdateRefusesToRewindTheOrchestratorMarker`.
- **Command:** `go test -tags firebase ./internal/budget/rules/ -run 'Orchestrator|Parity' -count=1 && go test ./internal/cli/ -run 'TestUpdate.*Orchestrator' -count=1`
- **Depends on:** O0 (field names), and the owner's answer to D1241. **Level 1.**
- **Commit:** `feat(rules): the orchestrator subtree, closed to job identities, converged by update (O2)`
- **Red step:** `TestOrchestratorWritesRefuseAJobIdentity` fails against the unchanged rules (a deny-by-default root makes it pass vacuously, so the
  test first proves a person write succeeds, then a job write fails).
- **OWNER-RUN, Check 300:** with a real installation that is **not** project `belong`: run `fugaro update`; confirm the writer D1241 names can write
  the state node and a run's own token cannot. The agent writes the check text, the owner runs it. Operators' line: "run `fugaro update`".
- **Reviewer:** security (rules and guards).

### O3. The state store, the audit log and the daemon lease

- **Files (8):** `internal/orchestrator/store.go` (new), `internal/orchestrator/lease.go` (new), `internal/orchestrator/audit.go` (new),
  `internal/orchestrator/store_test.go` (new), `internal/orchestrator/lease_test.go` (new), `internal/orchestrator/audit_test.go` (new),
  `internal/orchestrator/testdata/lease-a.json` (new), `internal/orchestrator/export_store_test.go` (new; O0 owns `export_test.go`, so the
  table has no shared writer).
- **Interfaces:** `type Store interface{ Load(ctx) (State, ETag, error); Save(ctx, State, ETag) error }` over `internal/rtdb` `GetETag`/`PutIfMatch` **on the `state` node
  only, a sibling of `lease` and `audit`** (never a PUT of `orchestrator/<id>`), a bounded re-read on 412 and never forced (F5); `Load` applies O0's
  `Migrate` and the next `Save` writes the migrated `schema_version`; `Lease` after `internal/mailbox/lease.go`'s heartbeat-lease pattern; `Audit.Append(ctx, Entry)` with
  a deterministic id (`<run>:<transition>`) and a server-assigned order (`rtdb.ServerNow`), D1229 and D1230.
- **Schema/rules/config change:** none (O2 holds the rules).
- **Tests:** `TestTwoDaemonsOneLease`, `TestStaleSaveIsRefusedNotForced`, `TestAuditAppendIsIdempotentOnADerivedId`, `TestLeaseExpiresWhenTheHolderStopsBeating`,
  `TestStoreFailsClosedOnAnUnreadableState`,
  `TestHeartbeatDoesNotMoveTheStateETag`, `TestLoadMigratesAnOlderSchemaVersion`.
- **Command:** `go test ./internal/orchestrator/ -run 'Store|Lease|Audit' -count=1`
- **Depends on:** O0. **Level 1.**
- **Commit:** `feat(orchestrator): the compare-and-swap state store, the daemon lease and the audit log (O3)`
- **Red step:** `TestTwoDaemonsOneLease` fails to build, then fails when both daemons believe they hold the lease.
- **Reviewer:** security (single-writer fence).

### O4. The guardrail policy fields, tightening-only

- **Files (6):** `internal/projectpolicy/policy.go`, `internal/projectpolicy/orchestrator_test.go` (new), `internal/policy/merge.go`,
  `internal/policy/orchestrator_merge_test.go` (new), `internal/localcfg/localcfg.go`, `internal/localcfg/orchestrator_test.go` (new).
- **Interfaces:** an optional `orchestrator:` block in the published policy: `max_running` (default the person's `max_parallel`), `batch_size`
  (default 10, D1239), `spawn_per_minute` (default 2, conservative), `spawn_per_hour` and `spawn_per_day` (defaults 12 and 60), `decision_model_per_class` (tightening-only, G6; the tier order is **haiku < sonnet < opus**, an unknown model name is refused, and the Sensitive class never drops below opus); a local `orchestrator:` key holding the same plus the
  model names. `policy.Merge` only tightens. Additive optional keys: no policy version bump (F22: confirmed or reversed in the red step against
  `internal/projectpolicy/version.go`).
- **Schema/rules/config change:** the policy and local-config schemas that list their keys gain the block (a `schemas/` entry exists for the project
  layer; if none lists the policy keys, the red step records that).
- **Tests:** `TestPolicyDefaultsAreConservative`, `TestMergeNeverRaisesTheRunningCap`, `TestModelKeyOnlyTightens` (uses the tier order, refuses an unknown model and a lowering of the Sensitive class),
  `TestOrchestratorBlockDecodesStrictly`, `TestSpawnPerMinuteHasADefault`.
- **Command:** `go test ./internal/projectpolicy/ ./internal/policy/ ./internal/localcfg/ -run Orchestrator -count=1`
- **Depends on:** none. **Level 0.**
- **Commit:** `feat(policy): the orchestrator's spawn guardrails, tightening only (O4)`
- **Red step:** `TestMergeNeverRaisesTheRunningCap` fails to build, then fails when a local value above the project's wins.
- **Reviewer:** security (guards).

### O5. `fugaro orchestrator state / events / patch --json`

- **Files (8):** `website/content/cli-reference.md` (regenerated by `go run ./ee/clidoc`, guarded by `TestCliReferenceIsCurrent` in `ee/clidoc`),
  `internal/cli/orchestrator_cmd.go` (new), `internal/cli/orchestrator_state.go` (new), `internal/cli/orchestrator_patch.go` (new),
  `internal/cli/orchestrator_cmd_test.go` (new), `internal/cli/orchestrator_patch_test.go` (new), `internal/cli/root.go` (one line registering the command),
  `schemas/orchestrator-output.schema.json` (new).
- **Interfaces:** `state [--brief]` prints the generated summary (the session-start print, D1237: short, one screen), **with stored text fenced and cleaned** (G3); `events [--since]` reads the
  audit log; `patch -f file|-` validates with O0, with the actor class `session` by default; **person-only ops (approve, `goal_set`, resolving a person decision) need a Typed confirmation at a real
  terminal** (`initflow.CanConfirm(Typed)`) and are refused otherwise, and `--yes` never covers them. The agent-marker check
  (`AgentRefusal`, `internal/initflow/stage.go`) stays as an extra refusal but **is a mitigation, not a barrier** (`env -u CLAUDECODE`, or a direct node
  write, passes it), so it is not what G2 rests on; the residual is owner question D1242. No `spawn`, no `retry`: launch is the daemon's (T2).
- **Schema/rules/config change:** the `--json` output schema, additive, `schema_version` 1.
- **Tests:** `TestStateBriefFitsOneScreen`, `TestEventsJSONMatchesTheSchema`, `TestPersonOpsNeedATypedConfirmation`,
  `TestPatchFromAnAgentSessionRefusesPersonOps` (the accident only), `TestStateBriefFencesAndCleansStoredText`, `TestPatchRejectsAStaleRev`,
  `TestCommandIsRegisteredOnce`; `go test ./ee/clidoc/ -count=1` for the regenerated reference.
- **Command:** `go test ./internal/cli/ -run 'TestOrchestrator|TestStateBrief|TestEventsJSON|TestPatch' -count=1`
- **Depends on:** O0, O3. **Level 2.**
- **Commit:** `feat(cli): fugaro orchestrator state, events and patch with --json (O5)`
- **Red step:** `TestPersonOpsNeedATypedConfirmation` fails to build, then fails when a patch with no terminal closes a person decision. **P1 is resolved
  as negative:** the agent-session detection is not a barrier (`stage.go` `AgentAdvice`, `init.go`), so G2 rests on the Typed confirmation and the
  residual is D1242.
- **Reviewer:** security (the person-class fence).

### O6. The dependency-driven launcher: intent-first claims, caps, running cap and breaker

- **Files (8):** `internal/orchestrator/launch.go` (new), `internal/orchestrator/intent.go` (new), `internal/orchestrator/caps.go` (new),
  `internal/orchestrator/breaker.go` (new), `internal/orchestrator/launch_test.go` (new), `internal/orchestrator/caps_test.go` (new),
  `internal/orchestrator/breaker_test.go` (new), `internal/orchestrator/export_launch_test.go` (new).
- **Interfaces:** `type Launcher interface{ Launch(ctx, Spec, RunID) (RunID, error) }` implemented in O8 over the `fugaro run` path (the core
  must not import `internal/cli`); `func Plan(State, RunView, Caps, Now) []Action`: an item is eligible only if `Approved` by a person **and the
  approval's `SpecDigest` equals the digest of the item's current launch spec** (repo, recipe, rigor tier, brief), all `DependsOn` are `done`, the
  running count and the per-cycle, per-minute, per-hour and per-day counts are under `Caps`, and the kill switch is off (consulted through an
  interface over `budget`). **The per-minute, per-hour and per-day counts are derived from the committed `Intents` using their server time**
  (`rtdb.ServerNow`), never from in-memory counters, so a launchd restart loop does not reset them. **Intent first (D1231, corrected):** the
  committed intent carries the **run id minted before the start** (`task.NewRunID`) and the spec digest; `Launch` starts the run under that id;
  **recovery looks for an existing `task.json`/`launch.json` under the recorded run id and adopts it, never minting or issuing a second launch** (the
  claim is `<run prefix>launching`, takes no key, is released after a launch and is stale after 10 minutes, so it is not the fence); `Breaker` counts consecutive failed or empty cycles (3) and writes a `pending_decisions` item.
- **Schema/rules/config change:** none.
- **Tests:** `TestSpawnNeverLeavesTheApprovedItem`, `TestUnapprovedItemIsNeverLaunched`, `TestCapsStopALoop`, `TestKillSwitchStopsSpawning`,
  `TestRecoveryAdoptsTheRecordedRunID` (a fake that records started run ids; the real-store crash test is O8's), `TestEditedSpecIsNeverLaunched`,
  `TestCapsSurviveARestart` (a new process with the same committed intents has the same counts), `TestBreakerRaisesADecision`, `TestDependencyOrderIsRespected`.
- **Command:** `go test ./internal/orchestrator/ -run 'Launch|Caps|Breaker|Plan' -count=1`
- **Depends on:** O0, O4 (the `Caps` values; the type is defined here and O4 fills it in O8). **Level 1.**
- **Commit:** `feat(orchestrator): the dependency-driven launcher with intent-first claims and spawn caps (O6)`
- **Red step:** `TestRecoveryAdoptsTheRecordedRunID` fails to build, then fails when recovery mints a new run id instead of adopting the recorded one.
- **Reviewer:** security (money).

### O7. The reconcile tick and notifications

- **Files (6):** `internal/orchestrator/tick.go` (new), `internal/orchestrator/runview.go` (new), `internal/orchestrator/notify.go` (new),
  `internal/orchestrator/tick_test.go` (new), `internal/orchestrator/notify_test.go` (new), `internal/orchestrator/export_tick_test.go` (new).
- **Interfaces:** `Tick(ctx, Store, RunViewReader, Launcher, Notifier)`: load, fold finished runs into the item phases **by joining on the run id the daemon recorded in the intent, never on a run-writable field** (a run's own heartbeat, title or label), ask `Plan` (O6), execute
  intents, commit; a `Notifier` interface (a sink per OS in O9; a log sink here) fires on a finished run or a decision needed; level-triggered, no cursor
  (D1229). Nothing in it calls a model (D1221).
- **Schema/rules/config change:** none.
- **Tests:** `TestTickMarksAFinishedRunDone`, `TestTickIsIdempotentOnRepeat`, `TestTickNotifiesOnceForADecision`, `TestTickMakesNoModelCall` (the
  dependency list has no agent package), `TestTickLeavesAPersonDecisionOpen`, `TestTickJoinsOnTheRecordedRunIDOnly`.
- **Command:** `go test ./internal/orchestrator/ -run 'Tick|Notify' -count=1`
- **Depends on:** O3, O6. **Level 2.**
- **Commit:** `feat(orchestrator): the reconcile tick and its notifications (O7)`
- **Red step:** `TestTickIsIdempotentOnRepeat` fails to build, then fails when a second tick launches again.
- **Reviewer:** light (no new guard; the money path is O6's). **UNVERIFIED (P2):** which notification the owner wants (an OS notification via
  `osascript`/`notify-send`, or the owner's existing push); the interface lets O9 supply either.

### O8. The foreground daemon command and the run-path launcher

- **Files (7):** `website/content/cli-reference.md` (regenerated), `internal/cli/orchestrator_run.go` (new), `internal/cli/orchestrator_launcher.go` (new), `internal/cli/orchestrator_run_test.go` (new),
  `internal/cli/orchestrator_launcher_test.go` (new), `internal/cli/orchestrator_cmd.go` (edit: adds the `run` subcommand), `internal/cli/launch_budget.go`
  (a read-only export if one is needed; otherwise untouched).
- **Interfaces:** `fugaro orchestrator run` = the lease, then `Tick` every N seconds, with the SSE stream of the project's RTDB as a wake-up (resume
  gives a full `put`, F8); the `Launcher` implemented over the same pre-launch path `fugaro run` uses (claim plus `budget.Precheck`, F11, T2). It
  refuses any gated converge or publish command (G8): the daemon reports "update due" and nothing else; **it never passes `Yes`** to any confirmation
  and the daemon package does not depend on the gated-command packages.
- **Schema/rules/config change:** none.
- **Tests:** `TestDaemonDoesNotDependOnGatedCommands` (an import and dependency test over `go list -deps`, not a string check), `TestDaemonNeverPassesYes`,
  `TestCrashBetweenIntentAndLaunchMakesOneRun` (the real `internal/runstore` and `internal/gcpfake`: crash after the intent and again after the start,
  recover, one run), `TestRunLauncherUsesTheLaunchClaim`, `TestRunLauncherRunsPrecheck`, `TestRunStopsWhenTheLeaseIsLost`,
  `TestRunReportsUpdateDueAndDoesNothingElse`.
- **Command:** `go test ./internal/cli/ -run 'TestDaemon|TestRunLauncher|TestRunStops|TestRunReports' -count=1`
- **Depends on:** O2 (the node must exist), O5, O7. **Level 3.**
- **Commit:** `feat(cli): the orchestrator daemon loop over the fugaro run launch path (O8)`
- **Red step:** `TestRunLauncherRunsPrecheck` fails to build, then fails when the launcher skips `Precheck`.
- **Reviewer:** security (the launch path).

### O9. The launchd and systemd user-unit installer

- **Files (11):** `website/content/cli-reference.md` (regenerated), `internal/cli/update_orchestrator.go` (new), `internal/cli/update_orchestrator_test.go` (new),
  `internal/orchestrator/service/launchd.go` (new), `internal/orchestrator/service/systemd.go` (new),
  `internal/orchestrator/service/service.go` (new), `internal/orchestrator/service/service_test.go` (new), `internal/orchestrator/service/testdata/unit-a.golden`
  (new), `internal/cli/orchestrator_install.go` (new), `internal/cli/orchestrator_install_test.go` (new), `internal/cli/orchestrator_cmd.go` (edit: install,
  uninstall, status).
- **Interfaces:** `fugaro orchestrator install|uninstall|status`: writes a per-user unit that runs `fugaro orchestrator run`, restarts after a crash or
  reboot, never touches a system path or needs root; macOS and Linux only (D1232), Windows answers "not supported" (`docs/windows-cli`). Content is
  rendered from a fixed template with the binary path quoted; nothing from state or policy goes into the unit. **The unit runs the stable binary
  path** (the package manager's stable link, e.g. Homebrew's `opt` path, never a versioned `Cellar` path that `brew upgrade` removes, which would
  crash-loop). **`fugaro update` converges an installed daemon (the HARD RULE):** when a unit is installed it re-renders it from the new template,
  restarts it, and migrates the state node's `schema_version` with O0's `Migrate`, behind the update's own confirmation; nothing else is left for the
  operator. `install` is refused under a coding agent's marker and `--yes` never covers it (a unit runs code at login).
- **Schema/rules/config change:** none.
- **Tests:** `TestLaunchdPlistMatchesTheGolden`, `TestSystemdUnitMatchesTheGolden`, `TestInstallWritesOnlyUnderTheUserHome`, `TestWindowsIsRefusedNamed`,
  `TestUninstallRemovesWhatInstallWrote`, `TestInstallRefusedUnderAnAgentMarker`, `TestUnitUsesTheStableBinaryPath`,
  `TestUpdateRerendersAndRestartsAPreviousVersionUnit` (a unit rendered by the previous version converges in one `update`),
  `TestUpdateMigratesTheStateSchemaVersion`. Tests render into `t.TempDir()` and never call `launchctl` or `systemctl` (an interface is faked).
- **Command:** `go test ./internal/orchestrator/service/ ./internal/cli/ -run 'Launchd|Systemd|TestInstall|TestUninstall|TestWindows|TestUpdate.*(Unit|Schema)' -count=1`
- **Depends on:** O8. **Level 4.**
- **Commit:** `feat(cli): install the orchestrator daemon under launchd and a systemd user unit (O9)`
- **Red step:** `TestInstallWritesOnlyUnderTheUserHome` fails to build, then fails when a path outside the home is accepted.
- **OWNER-RUN, Check 301:** on the owner's Mac: `fugaro orchestrator install`, close the terminal, reboot, confirm the daemon is running and exactly one
  instance holds the lease (start a second by hand and see it refuse); on a Linux machine the same with systemd. No agent runs it.
- **Reviewer:** security (code that runs at login).

### O10. The plugin skill and the shared session-start hook slot

- **Files (6):** `plugin/skills/orchestrator/SKILL.md` (new), `plugin/skills/orchestrator/reference/state.md` (new), `plugin/orchestrator_skill_test.go` (new),
  the session-start print (**`docs/plugin-user-scope` designs no hook: its nudge is a skill sentence running `doctor --offline`, F17**; this task writes the
  print as a skill sentence, and a real plugin hook only if the Claude Code plugin hook configuration is confirmed in the red step and D1243 is answered;
  the path `plugin/skills/hooks/session-start` is not where Claude Code plugin hooks live), `plugin/plugin_test.go` (edit: the new skill in the list), `plugin/readme_test.go` (edit).
- **Interfaces:** the skill teaches a session to read `fugaro orchestrator state --brief` first, to record decisions and facts through `patch` as they
  happen and never to rely on chat history; it names only commands O5 has (the plugin tests check them). The print shows the short summary and any pending decision, **fenced and cleaned** (G3), ordered after the update nudge. **`state --brief` reads the
  RTDB, which D952 says the session-start check never does: owner question D1243**; until answered the print reads a local cache the daemon wrote.
- **Schema/rules/config change:** the plugin skill list.
- **Tests:** `TestOrchestratorSkillNamesOnlyRealCommandsAndFlags`, `TestSessionStartPrintsOneScreen`, `TestSessionStartPrintFencesStoredText`, `TestHookNeverRunsAGatedCommand`,
  `TestSkillSaysFirestoreIsNotTheStore`.
- **Command:** `go test ./plugin/ -count=1`
- **Depends on:** O5. **Level 3.** External: `docs/plugin-user-scope`'s nudge (a skill sentence, unbuilt); the fallback is the `CLAUDE.md` instruction (D1237's stated fallback).
- **Commit:** `feat(plugin): the orchestrator skill and the session-start state print (O10)`
- **Red step:** `TestOrchestratorSkillNamesOnlyRealCommandsAndFlags` fails when the skill names a flag the CLI lacks.
- **Reviewer:** security (plugin skills are security paths).

### O11. The cutover: the one-time import and the rollback note (D1235)

- **Files (7):** `website/content/cli-reference.md` (regenerated), `internal/cli/orchestrator_import.go` (new), `internal/cli/orchestrator_import_test.go` (new), `internal/cli/orchestrator_cmd.go` (edit: `import`),
  `docs/how-to/orchestrator-cutover.md` (new), `docs/releases/vX.Y.Z.md` (edit: the cutover line), `internal/cli/docs_orchestrator_cutover_test.go` (new).
- **Interfaces:** `fugaro orchestrator import -f dump.json` takes the one-time session dump (spec "Migration") through O0's validator and caps
  (**the first stored-prompt-injection surface**), as a `patch` from a person; the how-to states the **cutover at once** (D1235): the daemon replaces the
  owner's sweep, watcher and queue scripts the day it is started. **The how-to carries the rollback note** (T1): the old scripts are not deleted by
  any step here; stopping the daemon (`fugaro orchestrator uninstall`) and restarting the scripts is the rollback. **A one-command rollback is not
  built:** it waits on the owner's ruling (Q1, section 6).
- **Schema/rules/config change:** none.
- **Tests:** `TestImportRunsTheSameValidatorAsPatch`, `TestImportRefusesAnOversizeDump`, `TestImportNeverMarksAnItemApproved` (a dump cannot approve work:
  a person approves), `TestDocsOrchestratorCutoverNamesTheRollback`.
- **Command:** `go test ./internal/cli/ -run 'TestImport|Docs' -count=1`
- **Depends on:** O5, O8, O9. **Level 5.**
- **Commit:** `feat(cli): import the session dump and document the cutover and its rollback (O11)`
- **Red step:** `TestImportNeverMarksAnItemApproved` fails to build, then fails when a dump's `approved` is honoured.
- **OWNER-RUN, Check 302:** the cutover itself, on the owner's own installation (never `belong`'s sandbox from an agent): start the daemon, confirm
  one finished run, one dependency-driven launch, one notification, with the spend caps and kill switch tested first. Because there is no shadow week
  (D1235), the owner runs this with the scripts still in place **but never launching at once**: the two share no launch key (the claim is per run id),
  so scripts and daemon both launching the same work would double-launch. The check starts with the scripts' launching stopped (their watching may
  stay) and the owner decides when to remove them.
- **Reviewer:** security (an import into stored state).

### O12. The decision agent: brief, bounds, runner (slice 2)

- **Files (10):** `internal/orchestrator/decide/brief.go` (new), `internal/orchestrator/decide/runner.go` (new), `internal/orchestrator/decide/bounds.go` (new),
  `internal/orchestrator/decide/output.go` (new), `internal/orchestrator/decide/prompts/system.md` (new), `internal/orchestrator/decide/brief_test.go` (new),
  `internal/orchestrator/decide/runner_test.go` (new), `internal/orchestrator/decide/bounds_test.go` (new), `internal/orchestrator/decide/export_test.go` (new),
  `schemas/orchestrator-decision.schema.json` (new).
- **Interfaces:** a fresh `claude -p` per decision with a small brief (the CI failure excerpt and the item, never the whole state), `--max-turns`, a
  pinned `agent.max_output_tokens` (owner lesson 21), the model chosen by O1's class and O4's key, call counters (6 per hour, 40 per day) held in O0's `State.Counters` (derived with server time, `TestCountersSurviveARestart`; this task edits neither
  `state.go` nor the schema), the deny rules a coding agent's session has (G8), the output validated as an O0 patch from `DecisionAgent`, never a person-only op, and an
  empty or failed result counts toward O6's breaker (G10). The decision and its reasoning are written to the audit log (O3). Prompts, facts and event text
  are fenced as untrusted.
- **Schema/rules/config change:** the decision-output schema; the numbers (6, 40, 3) are config with the proposals as defaults (U4: not measured).
- **Tests:** `TestUntrustedTextIsFenced`, `TestDecisionAgentRunsWithTheAgentDenyRules`, `TestEmptyCycleCountsAsFailed`, `TestHourlyAndDailyCapsStopCalls`,
  `TestModelComesFromTheClassifierNotTheOutput`, `TestDecisionCannotSpawnAnUnapprovedItem`, `TestOutputTokenPinIsPassed`, `TestCountersSurviveARestart`.
- **Command:** `go test ./internal/orchestrator/decide/ -count=1`
- **Depends on:** O0, O1, O3, O4, O6. **Level 2.** Not started before the owner approves slice 2 by name.
- **Commit:** `feat(orchestrator): the bounded decision agent (O12)`
- **Red step:** `TestDecisionAgentRunsWithTheAgentDenyRules` fails to build, then fails when the runner omits the deny rules.
- **OWNER-RUN, Check 303:** with the owner's own Claude login: one real decision call, the count caps and the breaker proved, the cost of a cycle
  measured (this is what turns U4 from a target into a number).
- **Reviewer:** security (prompts and the agent environment).

### O13. The decision agent in the tick (slice 2)

- **Files (4):** `internal/orchestrator/tick.go` (edit), `internal/cli/orchestrator_run.go` (edit: the `--decide` flag, off by default), `internal/orchestrator/tick_decide_test.go`
  (new), `internal/cli/orchestrator_run_decide_test.go` (new).
- **Interfaces:** the tick wakes the decision agent only for the cases the spec's table names (retries exhausted, stuck past the timeout, an ambiguous
  review result); every other event stays code (spec "When to wake the LLM"). Off unless asked; the breaker pauses the daemon and raises a decision.
- **Schema/rules/config change:** none.
- **Tests:** `TestTickWakesTheAgentOnlyForJudgementCases`, `TestHeartbeatsNeverWakeTheAgent`, `TestBreakerPausesTheDaemon`, `TestDecideIsOffByDefault`.
- **Command:** `go test ./internal/orchestrator/ ./internal/cli/ -run 'Decide|WakesTheAgent|Heartbeats|BreakerPauses' -count=1`
- **Depends on:** O8, O12. **Level 4.**
- **Commit:** `feat(orchestrator): wake the decision agent for judgement cases only (O13)`
- **Red step:** `TestHeartbeatsNeverWakeTheAgent` fails to build, then fails when a heartbeat triggers a call.
- **Reviewer:** security.

### O16. The MCP state group and its scopes (slice 3; waits)

- **Files:** the MCP design's own tool registry and scope files, **to be named when M7 lands** (none exist on `main`, F9); this task writes
  `get_state`, `get_events`, `apply_patch` over O0 to O3 only, with `state:read` and `state:write`, `apply_patch` also gated by D1225. **It must not
  call or depend on `spawn_task`** (T2).
- **Depends on:** O0, O3, O5, and externally **M7 (the registry)**, the amendment adding the scopes to the MCP design (T3, D927 amended), and the MCP
  threat-model amendment (T4). **Not startable until those land.** Level: after the MCP chain; outside this plan's levels.
- **Red step, tests, Check 305:** written when the files are named. **Reviewer:** security.

### O17. `ee/`: a project-owned agent and handoff (slice 3)

- **Files (6):** `ee/orchestrator/handoff.go` (new), `ee/orchestrator/handoff_test.go` (new), `ee/orchestrator/doc.go` (new), `ee/register/orchestrator.go` (new),
  `ee/register/orchestrator_test.go` (new), `internal/orchestrator/hooks.go` (new: the core's extension point the registration fills, with no `ee/` import).
- **Interfaces:** handoff of an agent between developers by an owner or admin; an agent owned by the project rather than a person; registered the way
  the coordinator's CLI is. Every file carries the two-line licence notice.
- **Tests:** `TestHandoffNeedsOwnerOrAdmin`, `TestCoreBuildsWithoutEE` (the eeboundary guard is run), `TestAgentOwnedByTheProjectHasNoPersonIdentity`.
- **Command:** `go test ./ee/... ./internal/eeboundary/ -count=1`
- **Depends on:** O0, O3, O4. **Level 2.** **Commit:** `feat(ee): hand an orchestrator agent to another developer (O17)`. **Red step:** `TestHandoffNeedsOwnerOrAdmin`.
  **Reviewer:** security. **OWNER-RUN, Check 304:** the handoff on a real installation, two people.

### O18. `ee/`: batch approval (slice 3)

- **Files (4):** `ee/orchestrator/batch.go` (new), `ee/orchestrator/batch_test.go` (new), `ee/register/orchestrator.go` (edit: registers it),
  `ee/register/orchestrator_test.go` (edit).
- **Interfaces:** the project setting for how many work items one approval covers (the default is O4's 10, never the spec's 50 without a person raising it).
- **Tests:** `TestBatchDefaultIsTenAndOnlyAPersonRaisesIt`, `TestBatchNeverWidensTheProjectCap`.
- **Command:** `go test ./ee/... ./internal/eeboundary/ -count=1`
- **Depends on:** O17. **Level 3.** **Commit:** `feat(ee): batch approval for orchestrator work items (O18)`. **Red step:** `TestBatchDefaultIsTenAndOnlyAPersonRaisesIt`.
  **Reviewer:** security.

### O19. `ee/`: workspace caps (slice 3; blocked)

Blocked by the workspaces design (T5), which reuses the MCP server and was amended the same day. Not specified further here; no files are named until
that design's tasks are. **Depends on:** O18 and the workspaces plan. Reviewer: security.

## 4. Parallel execution

Edges (a task depends on the tasks to its left): O2 <- O0; O3 <- O0; O6 <- O0, O4; O5 <- O0, O3; O7 <- O3, O6; O12 <- O0, O1, O3, O4, O6; O8 <- O2, O5, O7;
O10 <- O5; O17 <- O0, O3, O4; O9 <- O8 (and O0, O3 for `Migrate`); O13 <- O8, O12; O18 <- O17; O11 <- O5, O8, O9. O16 and O19 are external-gated.

| Level | Tasks (can all run at once) |
| --- | --- |
| 0 | O0, O1, O4 |
| 1 | O2, O3, O6 |
| 2 | O5, O7, O12, O17 |
| 3 | O8, O10, O18 |
| 4 | O9, O13 |
| 5 | O11 |
| gated | O16 (MCP M7, scopes amendment, T4), O19 (workspaces, T5) |

**Critical path:** O0 -> O3 -> O5 -> O8 -> O9 -> O11, six levels. Slice 1 (O0-O11 minus O12/O13) completes at level 5; slice 2 adds O12 at level 2
and O13 at level 4, so it adds nothing to the critical path once approved; slice 3's `ee/` tasks run beside slice 1, its MCP task waits.

### Shared-files table (no level holds two writers of one file)

| File | Writers (level) | Same level? |
| --- | --- | --- |
| `internal/cli/orchestrator_cmd.go` | O5 (2), O8 (3), O9 (4), O11 (5) | no, one per level |
| `internal/cli/orchestrator_run.go` | O8 (3), O13 (4) | no |
| `internal/orchestrator/tick.go` | O7 (2), O13 (4) | no |
| `internal/cli/root.go` | O5 (2) | one writer |
| `docs/releases/vX.Y.Z.md` | O2 (1), O11 (5) | no |
| `ee/register/orchestrator.go` and its test | O17 (2), O18 (3) | no |
| `plugin/plugin_test.go`, `plugin/readme_test.go` | O10 (3) | one writer |
| `internal/budget/rules/*`, `internal/cli/update_converge_test.go` | O2 (1) | one writer |
| `internal/projectpolicy/policy.go`, `internal/policy/merge.go`, `internal/localcfg/localcfg.go` | O4 (0) | one writer |
| `internal/orchestrator/export_test.go` | O0 (0); O3, O6, O7 each add an `export_<area>_test.go` of their own | one writer |
| `internal/cli/launch_budget.go` | O8 (3) | one writer |
| `schemas/*` | each schema has one writer (O0, O5, O12) | one writer each |
| `internal/orchestrator/state.go` and `schemas/orchestrator-state.schema.json` | O0 (0) only; O6 and O12 use `Intents` and `Counters` and edit neither | one writer |
| `website/content/cli-reference.md` (generated by `go run ./ee/clidoc`) | O5 (2), O8 (3), O9 (4), O11 (5) | no, one per level |
| `internal/cli/update_orchestrator.go` and its test | O9 (4) | one writer |

## 5. Owner-run items, exactly

Checks 300 (O2: rules and `fugaro update` on a real installation, not `belong`), 301 (O9: the daemon under launchd and systemd across a reboot and
a second instance), 302 (O11: the cutover on the owner's own installation), 303 (O12: one real decision call and the measured cost), 304 (O17:
a two-person handoff), 305 (O16: the MCP state group, when it exists). 306-309 are reserved. No agent performs any of them.

## 6. Owner questions the plan does not decide

- **Q1 (T1).** After the cut-over at once, is a one-command rollback to the old scripts wanted, with the scripts kept for N days, and what is N? Until
  answered, O11 documents the rollback and builds no command.
- **Q2.** Should `fugaro update` offer `fugaro orchestrator install` behind its own confirmation, or is the install a separate opt-in the owner runs once?
- **Q3 (P2).** OS notifications, or the owner's existing push, for a finished run and a decision needed?
- **Q4.** Whether the owner approves O12 and O13 (slice 2) before the O11 cutover has run for a while, or only after.
- **D1241, D1242, D1243** (new from the round-1 security review, numbered in this folder's block, reconciliation section 6): the state node's writer
  identity (before O2), a paired-device channel for person decisions (before anything consumes a resolved decision), and the session-start print
  versus D952's no-network rule (before O10). D1244-D1249 stay reserved.

## 7. UNVERIFIED added by this plan

- **P1. Resolved as negative.** `initflow`'s agent-session detection is not reliable as a fence (its own text: "a mitigation, not a barrier"); G2's CLI
  half rests on a Typed confirmation at a real terminal, with the residual in D1242.
- **P2.** Which notification channel the owner wants (Q3).
- **P3.** Whether a new top-level RTDB subtree needs a `RulesVersion` bump or a marker of its own: this plan recommends the marker (the credentials cascade
  precedent) and O2's red step confirms it against `internal/projectpolicy/version.go`.
- **P4.** Everything reconciliation U1-U8 carries stays open; in particular the cost of a cycle (U4) is measured only by Check 303.
