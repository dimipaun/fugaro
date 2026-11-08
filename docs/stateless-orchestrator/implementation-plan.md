# The stateless orchestrator: the implementation plan

**Status (2026-11-07): two tasks of this plan are written.** **Task O0, the state schema, the typed patch and the validator, is merged on `main`** on
2026-11-07 — pull request #1823, commit `36abeffc` — whose eleven files `internal/orchestrator/state.go`, `patch.go`, `validate.go`, `summary.go`, their tests,
`testdata/state-a.json` and the two `schemas/` documents are on `main`. **Task O1, the deterministic security-sensitive classifier, is written on the branch
`fugaro/20261107-023015-885d`**, pull request #1822, **unmerged and not on `main`**, and the owner approved it as part of the 0.22.0 scope: its four files —
`internal/orchestrator/sensitivity/classify.go`, `classify_test.go`, `export_test.go` and `testdata/paths.txt` — carry the interface, the allowlist, the eight
tests, the red step and the commit message the O1 section below states. **Every other task, file and step below is plan only, and no other task is approved**:
this line records what `main` and that branch hold and nothing more.

*The status as first written, kept:* **Status: plan only; nothing is implemented; no task approved.** Written 2026-11-04 against `origin/main` at `5998d67c7`. **Rounds 1 and 2 of the independent
security review (verdict "fix first") are applied in this revision** (round 2: the launch claim is per run id and is the fence, an approval covered one attempt only (**superseded below by the owner's 2026-11-04 retry ruling**: one approval covers the first attempt and at most one automatic retry), the Routine allowlist, the lease versus the state compare-and-swap; the round-1 corrections follow): the corrections are in reconciliation section 3 (the rules do not bind a person's
write; person-only is a mitigation; the launch claim is per run id, so it is the fence), in G2, G4, G5, G11, O0-O12 below, and in D1241-D1243.

**The owner answered D1241-D1243 and Q1-Q4 on 2026-11-04, and ruled the last open tension T7 on 2026-11-05; this revision follows every answer
through.** Four of them change what the plan
builds, and each is stated where it lands rather than only here: **the retry rule** (not the recommendation: one approval covers the first attempt
and at most one automatic retry of the same approved spec — G4, O0, O6), **Q2** (**the owner's answer, 2026-11-04 — a different answer from the one
this PR first recorded, which is superseded**: `fugaro setup` installs the unit as one of its listed steps and `fugaro orchestrator install` moves it
to the new template, so **`fugaro update` never touches the unit**; O8, O9, G11), **T7** (**the owner's ruling of 2026-11-05, not the recommendation,
which amends Q2: `fugaro update` re-renders an installed unit when the rendered template changed, so the HARD RULE is literally true; `update` never
installs one** — O9, G11, and reconciliation section 6), and
**D1243** (the session-start print reads a local cache the daemon writes: O8 gains the cache writer and O10 moves to level 4). D1241 lifts O2's gate,
D1242 gates nothing, **Q1** rules that the rollback is manual with the old scripts kept 14 days and no command built for it (O11, Check 302), Q3 names
the notification channel, and Q4 gates slice 2 on a measured 14-day run of slice 1.

## 1. Opening and status

This plan builds [spec.md](spec.md) (the owner's spec, verbatim) **as amended by [reconciliation.md](reconciliation.md)**: where the two
disagree the reconciliation wins. Nothing here is implemented. **No task is approved: each needs the owner's explicit go, by name.**
Findings are cited as F#, decisions as D#, open tensions as T# (reconciliation section 6).

**The owner's answers of 2026-11-04 that shaped this plan** (all the recommendation except D1235, the retry rule and Q2):

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
| D1231 | Intent first, recording the minted run id and the launch-spec digest, launch under that run id, re-issue under it on recovery (the per-run-id claim is the fence), then commit | O6 |
| D1232 | launchd and a systemd user unit, one instance by an RTDB heartbeat lease; no Windows | O3, O9 |
| D1233 | No `summary_replace`; the summary is generated on read | O0 |
| D1234 | The security-sensitive class is deterministic code over changed paths; the model per class is a tightening-only local key | O1, O4, O12 |
| **D1235 (not the recommendation)** | **Cut over at once, no shadow week** | O11, T1 |
| D1236 | The two owner-verbatim specs are not edited; the README notes are written (on `main` since #1682 merged the folder) | done |
| D1237 | One session-start hook slot shared with `docs/plugin-user-scope` | O10 |
| D1238 | The daemon and decision agents never run a gated converge or publish command | O8, O12 |
| D1239 | Project-policy guardrail fields, tightening-only, conservative defaults | O4 |
| D1240 | The local daemon may ship before the MCP chain; MCP-dependent parts are 0.21.0 or later | slices |
| D1241 | The person's own credential writes the `orchestrator/` node, the CLI and the daemon both; O0's validator is the whole shape check; the rules refuse **every identity they evaluate** — a run-scoped job token and a minter-minted custom token alike — and no write-own guarantee is claimed. O2's gate is lifted | O2, O3, O5 |
| D1242 | The terminal Typed confirmation stands, and a paired-device channel comes later; the later channel may reshape O0's `Approval`, so no task or test is gated on it | O0, O5 |
| D1243 | The session-start print reads a local cache the daemon writes (atomic, with a freshness stamp the print shows); `state --brief --offline` never reaches the network | O5, O8, O10 |
| **Retry rule (2026-11-04, not the recommendation)** | **One approval covers the first attempt and at most one automatic retry of the same approved spec; a refusal (a failed `Precheck`, the kill switch, a spend cap, a validation refusal) is never retried; a second failure needs a person's new approval; the retry counts toward every cap** | O0, O6, G4 |
| Q2 (**the owner's answer, 2026-11-04 — a different answer from the one this PR first recorded, which is superseded**) | **`fugaro setup` installs the unit** as one of its listed steps (real terminal only, refused under a coding agent's marker, and in a managed project no extra confirmation: the plan line names the unit and its path), **upgrades move to `fugaro orchestrator install`**, and **`fugaro update` never touches the unit** — **amended 2026-11-05 by T7: `update` never *installs* a unit, and it does re-render and reload one already installed**; the HARD RULE carve-out it left is **T7**, now ruled | O8, O9, G11 |
| **T7 (2026-11-05, not the recommendation; amends Q2)** | **`fugaro update` re-renders the daemon's unit when the rendered template changed, so the HARD RULE is literally true**: it only ever re-renders and reloads a unit that is **already installed** (never installs one, never creates one for a project that did not choose it), behind the same plan-and-confirm rules as every other `update` step (a managed project's gate stays, a terminal asks for the step's class); `fugaro setup` still installs the first one (Q2); `fugaro orchestrator install` stays the explicit command but no release's `### For operators` ever needs it; the daemon's self-restart and its start-up `Migrate` stay | O8, O9, G11 |
| Q3 | The macOS notification plus the owner's existing mobile push; Linux falls back to the desktop notifier. What the tree actually carries is U9: no push implementation, and the mailbox is a foreground channel | O7, O9 |
| Q4 | Slice 2 (O12, O13) is approved only after slice 1 has run 14 days and Check 303 measured the cycle cost | O12, O13, D1228 |

**Tensions** (reconciliation section 6): the ones the plan must not resolve are T2 (the launch in v1 is the local `fugaro run` path, never an MCP
`spawn_task`; the state group must not depend on it), T3 (the new scopes are an edit to the MCP design), T4 (MCP-dependent work waits for the MCP
threat-model amendment) and T5 (the workspace design reuses MCP). T1 was ruled on 2026-11-04 (no shadow week; the scripts are kept 14 days and the
rollback is the manual one the owner ruled). **T6 — Q2's offer against the managed no-prompt rule — was closed by the owner's second answer of
2026-11-04** (the install moved to `fugaro setup`, so the question of what an offer does in a managed project no longer arises). **T7 — the carve-out
of the HARD RULE that answer left behind — was ruled on 2026-11-05, and not the recommendation: `fugaro update` re-renders an installed unit when the
rendered template changed, so the HARD RULE is literally true and no second operator command is ever needed** (O9, G11). **No question here is left
open.**

## 2. Conventions

Read first: `CLAUDE.md`, `.claude/rules/ee.md` (any `ee/` task), `.claude/rules/credentials-service.md` (O3, O12 touch writes and credentials
adjacent code), `.claude/rules/terraform.md` (O2 edits `internal/infra/firebasedb.go`, which that rule loads for: no Terraform changes, but run its checks as they apply). Standing rules, each pinned by a task (section 3 names it):

- **No secret-shaped literal** in any test, doc, log or fixture. Fixtures use marker values.
- **No agent performs a live cloud action.** Checks 300-305 are written by agents and **run by the owner**. Never against project `belong`
  from an agent run.
- **Hermetic tests**: `internal/gcpfake` (its RTDB fake honours ETag and if-match), `internal/testutil`, `t.TempDir()`; the rules emulator
  tests are the repo's existing pattern (`internal/budget/rules/emulator_*_test.go`).
- **`fugaro update` is the whole upgrade (the HARD RULE), literally.** The owner's Q2 answer of 2026-11-04 cut the daemon unit out of `update`, which
  left the carve-out **T7** open; **the owner ruled T7 on 2026-11-05, and not the recommendation: `update` re-renders the unit itself**, so no release
  ever asks for a second command and the HARD RULE is true as written. Everything that was in `update` stays in it: O2 ships the rules convergence
  inside `update`, with a test that an installation at the previous rules generation converges in one run, and the release's `### For operators` is
  still "run `fugaro update`" plus what it asks. **What the two answers put on the unit:** `fugaro setup` installs the first one as a listed step of
  its own plan (real terminal only, refused under a coding agent's marker, and in a managed project no extra confirmation — the plan line names the
  unit and its path); **`update` re-renders and reloads a unit that is already installed when the rendered template changed, and never installs one**
  (O9) — a write to the person's own home and service manager, behind the same plan-and-confirm rules as every other `update` step; and `fugaro
  orchestrator install` stays as the explicit command (a machine that skipped `setup`, and repair) which no release depends on. An upgraded binary
  reaches a running daemon **without an operator step** by the daemon itself: it
  checks the binary the unit names each tick, exits cleanly when its version **differs** from the one running (not only when it is newer: whoever can write that path already owns the login unit, so a downgrade escalates nothing), and launchd (`KeepAlive`) or systemd (`Restart=always`)
  starts the new one (O8); the state node's `schema_version` migration runs in the daemon's start-up for the same reason.
- **`ee/` boundary**: nothing outside `ee/` imports `ee/`; `go test ./internal/eeboundary/ -count=1` after any `ee/` or registration change; the
  core builds and tests with `ee/` deleted; `TestCoreNeverImportsEE` is the standing guard, and no core task here imports `ee/`.
- **A field never changes meaning**: every new wire shape (state, patch, `--json` output, policy keys) is additive with a `schema_version` and a
  schema in `schemas/` (F21); the work item's fields (`phase`, `rigor_tier`) are named in O0 so that none equals a run-record or
  MCP field with a different meaning.
- **Release notes**: a task that changes what `fugaro update` does adds its line to the release notes' `### For operators` in the same PR
  (`docs/releases/README.md`); the file is `docs/releases/vX.Y.Z.md` of the release the owner assigns under D1240 (O2 creates the entry). **A release
  that changes the daemon unit's template is carried by `fugaro update` like any other upgrade** (T7 ruled, O9): it adds no operator line, and no
  release's `### For operators` requires `fugaro orchestrator install`; the release that first ships the orchestrator may name it as the optional adoption command for an installation that predates it (`update` neither offers nor performs the first install) — the HARD RULE's promise is that the one command is the whole upgrade.
- **Comments explain why**; no TODO, no section headers. A fix round adds no functionality.
- **Plugin tests** check every command and flag a skill names against the CLI: O5 and O10 move together (O10 depends on O5).
- **Docs tests**: a task that edits a doc runs `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs' -count=1`.

### Global constraints and Review focus (each pinned by a task)

| # | Constraint a reviewer holds every task to | Pinned by |
| --- | --- | --- |
| G1 | The LLM never writes state: typed ops only, validated by code (spec) | O0 `TestUnknownOpIsRefused` |
| G2 | A `waiting_on: person` decision, an approval and `goal_set` are closed only by a person-class actor, which at the CLI needs a Typed confirmation at a real terminal (`CanConfirm(Typed)`); the agent-marker check is a mitigation, not a barrier (`stage.go` `AgentAdvice`), so an `env -u CLAUDECODE` call or a direct node write is the residual; **D1242 (answered 2026-11-04) defers the stronger channel, so the Typed confirmation is the fence for now**; the Typed confirmation is itself beatable by a pty wrapper (`script`, `expect`), so it stops an accident, not an adversary (D1225, F20) | O0 `TestAgentActorCannotResolveAPersonDecision`, O5 `TestPersonOpsNeedATypedConfirmation`, `TestPatchFromAnAgentSessionRefusesPersonOps` (the accident only) |
| G3 | Free text (`facts`, `note`, titles, decision text) is data: length-capped, control characters stripped, fenced as untrusted in **every** surface that prints it into a session or a prompt, from slice 1 (O5's `state --brief`, O10's session-start print) and in O12's prompts, never above the system prompt | O0 `TestFactValuesAreCappedAndCleaned`, O5 `TestStateBriefFencesAndCleansStoredText`, O10 `TestSessionStartPrintFencesStoredText`, O12 `TestUntrustedTextIsFenced` |
| G4 | A cycle launches only an approved work item under the **full launch spec the person approved** (every field but a named bookkeeping list, bound by one `SpecDigest(WorkItem)` in the approval; any edit clears the approval); **one approval covers the first attempt and at most one automatic retry of that same approved spec** (owner ruling 2026-11-04, not the recommendation): the retry reuses the approval only while `SpecDigest(item)` still equals the approval's digest, committing the retry's intent spends the allowance and clears `Approved` while the first intent clears nothing, a **second** failure needs a person's new approval, and a failure whose cause is a refusal is never retried; **the attempt count is read off the intents stamped with the standing approval's `At`**, so a later approval cannot inherit an earlier one's attempts; **a refusal burns no attempt only when the `Launcher` refuses before the start** (no `launch.json` under the intent's run id — the run record says spend cap or refused only after it started, and a started run counts, or a refused start would relaunch until a cap stopped it); caps per cycle, hour, day, running and per minute are code, derived from committed intents, and the retry counts toward every one of them (D1226, D1239) | O0 `TestEditAfterApprovalRevokes`, `TestSessionCannotRequeueAnApprovedItem`, `TestUpsertCannotSetApproved`, `TestApprovalCarriesTheAttemptsItCovers`, `TestRetentionNeverDropsAnIntentInsideTheCapWindow`, `TestRetentionKeepsTheIntentsOfAStandingApproval`, `TestRetryCountsOnlyIntentsOfTheStandingApproval`, O6 `TestSpawnNeverLeavesTheApprovedItem`, `TestOneAutomaticRetryUnderTheSameApproval`, `TestRetryRefusedWhenTheSpecChanged`, `TestRefusalIsNotRetried`, `TestAPreStartRefusalReissuesTheSameIntent`, `TestAStartedRunsRefusalCounts`, `TestAFailedAttemptNeedsANewApproval` (the second failure, not the first), `TestCapsStopALoop`, `TestCapsSurviveARestart` |
| G5 | Money is at-most-once: the intent, with the minted run id, is committed before the launch; recovery re-issues the same launch under the recorded run id (through `--retry` when `task.json` exists; when it does not, `--retry` refuses "has no task.json", so the task is recreated under the recorded run id **only if `SpecDigest(item)` still equals the intent's digest AND either the approval still stands or the intent is the retry that spent it (`intent.ApprovalAt` equals the item's `SpentApprovalAt` — committing a retry's intent clears `Approved`, so "the approval stands" alone would abandon every crashed retry)**, otherwise the intent is marked abandoned, nothing having started), which reports "already-launched" or launches once, and never mints a second run id; **the per-run-id launch claim IS the concurrency fence** (`internal/cli/run.go` `launchRun`: the claim is never deleted after a launch, a stale claim is taken over by a generation-matched overwrite) (D1231) | O6 `TestRecoveryReissuesUnderTheRecordedRunID`, O8 `TestCrashBetweenIntentAndLaunchMakesOneRun`, `TestCrashAfterTaskJSONBeforeTheStartStillStartsOnce`, `TestRecoveryBeforeTaskJSONAfterAnEditAbandonsTheIntent`, `TestRecoveryBeforeTaskJSONOfASpentRetryStartsOnce` (real runstore and gcpfake) |
| G6 | The security-sensitive class is code over changed paths, never lowered by config or by the model (D1234) | O1 `TestPromptPathsAreAlwaysSensitive`, O4 `TestModelKeyOnlyTightens` |
| G7 | Kill switch, `Precheck` and spend caps are always consulted; the daemon cannot widen a ceiling | O6 `TestKillSwitchStopsSpawning` |
| G8 | The daemon and decision agents never run a gated converge or publish command, never pass `Yes` to a confirmation, and never answer a refusal with an action (D1238, F20) | O8 `TestDaemonDoesNotDependOnGatedCommands` (an import and dependency test), `TestDaemonNeverPassesYes`, O12 `TestDecisionAgentRunsWithTheAgentDenyRules` |
| G9 | One daemon per orchestrator id by the lease; **the state compare-and-swap, not the lease, serialises intents** (a daemon that lost its lease could win a `PutIfMatch` after a re-read, so `Save` checks the lease before it writes — **amended at O3: there is no retry to re-check before, the write is conditional and single, and a 412 is `ErrStateMoved`**); a stale writer's write is refused, never forced (D1232, F5); `state`, `lease` and `audit` are **siblings** under `orchestrator/<id>` (a PUT of the parent wipes children and every heartbeat would move a shared ETag) | O3 `TestTwoDaemonsOneLease`, `TestSaveRechecksTheLeaseBeforeARetry`, `TestHeartbeatDoesNotMoveTheStateETag` |
| G10 | A failed or empty cycle is a visible state (a `pending_decisions` item), never a silent no-op (owner lesson 22) | O6 `TestBreakerRaisesADecision`, O12 `TestEmptyCycleCountsAsFailed` |
| G11 | A rules, policy, **state-schema** or **daemon-unit** change converges under `fugaro update` from the previous generation, with no other operator step (the HARD RULE, ruled literally true on 2026-11-05): `update` re-renders an **already installed** unit when the rendered template changed and never installs one (O9, T7), `Migrate` runs in the daemon's start-up (O8), and an upgraded binary reaches the running daemon by the daemon's own version check and clean exit, not by a command. `fugaro setup` installs the first unit (Q2) and `fugaro orchestrator install` stays the explicit command (a machine that skipped `setup`, repair), which no release depends on | O2 `TestUpdateConvergesTheOrchestratorRulesFromThePreviousGeneration`, O0 `TestMigrateFromThePreviousSchemaVersion`, O8 `TestMigrateRunsAtDaemonStart`, `TestDaemonRestartsWhenTheUnitBinaryDiffers`, O9 `TestUpdateRerendersAnInstalledUnitWhenTheTemplateChanged`, `TestUpdateNeverInstallsAUnit`, `TestInstallRerendersAndRestartsAPreviousVersionUnit`, `TestSetupInstallsTheUnitAsAPlanStep` |

**Reviewer tier.** *Security*: `security-reviewer` (Opus), a mandatory independent review, at least one mutation check, routed by changed
paths (rules, guards, the launch path, prompts and plugin skills are security paths per the owner's routing rule). *Light*: `light-reviewer`.
Each task states its tier.

## 3. Slices and tasks

**Slice 1: the local deterministic daemon (D1221): O0-O11.** It retires the owner's sweep, the Monitor watcher and the queue scripts (F18,
U3) and makes **no model call**. **Slice 2: the decision agent and the full cycle (D1228): O12-O13.** **Slice 3: the MCP state group and the
`ee/` shared parts: O16-O19.** O12 is slice 2, not slice 1: D1221 puts the first slice at no model call, the decision agent's system prompt
is unwritten and the bounds D1228 names are proposals not measurements (U4), so a first slice carrying it would carry the unmeasured part;
the slice 1 daemon already contains the circuit-breaker and spawn caps the decision agent will run under (O6).

Checks come from the reserved block **300-309**, verified free on 2026-11-04: the register's top Check in `docs/` on `origin/main` is 273 (274-299 are reserved by other designs, so they are not free either), and
no open pull request diff adds one in 300-399. **One task, O2, writes Checks 300-305 into `docs/gcp-live-checklist.md` as NOT RUN** (the file has one writer, so no level holds two); the other tasks name their Check and do not edit the file. **Checks 306-309 stay reserved.**

Common to every task: the **Command** is a targeted `go test` with `-count=1`, then `go build ./... && gofmt -l . && go vet ./...`.
Tasks are mergeable one at a time. The commit message is `type(scope): subject (Oz)` and carries the attribution trailers.

### O0. The state schema, the typed patch and the validator (core)

- **Files (11):** `internal/orchestrator/state.go` (new), `internal/orchestrator/patch.go` (new), `internal/orchestrator/validate.go` (new),
  `internal/orchestrator/summary.go` (new), `internal/orchestrator/state_test.go` (new), `internal/orchestrator/patch_test.go` (new),
  `internal/orchestrator/summary_test.go` (new), `internal/orchestrator/export_test.go` (new), `internal/orchestrator/testdata/state-a.json`
  (new), `schemas/orchestrator-state.schema.json` (new), `schemas/orchestrator-patch.schema.json` (new).
- **Interfaces:** `type State struct{ SchemaVersion int; Rev int64; AgentID, Role, Goal string; WorkItems []WorkItem; Decisions []Decision;
  Facts map[string]Fact; ... }`. `WorkItem{ID, Title, Repo, Recipe, RigorTier, Brief, DependsOn, Phase, RunID, Approved *Approval, SpentApprovalAt, Attempts, Note}`: **`Phase`, `RunID`, `SpentApprovalAt`, `Attempts` and `Intents` are written only by the `Daemon` actor** (a session's `work_item_upsert` carrying them is refused, so a session cannot requeue an approved item, bump `Attempts`, mark a dependency `done` or forge a spent approval); **`Approved` is written only by `work_item_approve`, for every actor: an upsert carrying `Approved` is refused** (the digest is unkeyed, so a session could compute it and forge an approval through the CLI); **one approval covers the first attempt and at most one automatic retry of the same approved spec** (owner ruling 2026-11-04, not the recommendation): O6 clears `Approved` in the same `PutIfMatch` **only when the retry's intent is committed** — the allowance spent — and the first intent clears nothing, so a **second** failure needs a person's new approval. **A standing approval never outlives the item it approves: the same `PutIfMatch` that moves an item's `Phase` to `done` or `cancelled` clears `Approved` too**, so a successful run cannot leave an approval lying on a finished item for a later tick to relaunch. **`SpentApprovalAt` is how a spent approval stays identifiable after `Approved` is gone** (the `At` of the approval the retry's commit spent), and it is bookkeeping: `Migrate` and retention carry it, and recovery reads it (O6). **`Phase`
  is `queued|running|review|done|failed|blocked|cancelled`**, not `status`; the run's own state stays the run record's. `Approval{By, At, SpecDigest, Attempts}`, where **`Attempts` is the attempts the approval covers** (the one the person approved plus at most one automatic retry, so 2): **the digest is `func SpecDigest(WorkItem) Digest`, over every field except a named bookkeeping list** (`Phase`, `RunID`, `SpentApprovalAt`, `Attempts`, `Note`, `Approved`), so a field added later is digested by default and a reflective test pins the list; it is the **only** digest O6 and the approve op use, so the two cannot drift;
  any `work_item_upsert` that changes a digested field clears `Approved`, so a later upsert cannot rewrite a brief under a standing approval.
  `Intent{ID (item + attempt), RunID (minted before the start), SpecDigest, ApprovalAt (the `At` of the approval this attempt spends), At (server time), Outcome}` and `Counters` (the per-hour and per-day
  decision-call counts, O12) are **fields of `State`, named here** (`Intents []Intent`, `Counters Counters`); O6 and O12 use them and edit neither
  `state.go` nor the schema. `Decision{ID, Question,
  WaitingOn (person|session|daemon), ...}`. `Fact{Value, By}` carries provenance (`by`: person, session, daemon, decision-agent). **A name is a database key — amended at O3:** `idRE` and `agentRE` admit no dot (and no colon), and both schemas' patterns match, because a fact key is written as a property name in the RTDB document and the transport refuses `. $ # [ ] /` and control characters anywhere in the tree, not only in a path: a dotted fact key passed `ValidateState`, `EncodeState` and then failed at the node's write, and since every tick re-derives the same state and the intent that stops a second spawn cannot be committed (D1231), the orchestrator id was wedged for ever. The refusal is this package's own (`ErrInvalid`), before anything reaches the database (`TestANameTheDatabaseRefusesIsRefusedHere`), so O5's patch examples and O2's rules may not spell a name with a dot. `Patch{BaseRev, Ops}`
  with ops `work_item_upsert`, `work_item_approve` (person only; it carries the digest the person saw and is refused if it no longer equals the current `SpecDigest`; the Typed confirmation shows that spec), `work_item_archive`, `decision_add`, `decision_resolve`, `fact_set`, `fact_delete`, `goal_set` (no
  `summary_replace`, D1233). `func Apply(State, Patch, Actor) (State, error)` with `Actor{Class: Person|Session|Daemon|DecisionAgent}`;
  `func Summarize(State) string` generated on read, capped; a **retention rule** keeps the state node inside the ~4k-token target (done items, resolved decisions and old `Intents` and briefs are archived out past a cap, **but an `Intent` younger than the longest cap window, 24 hours, is never archived, **nor is any `Intent` of an item whose `Approved` still stands or whose `SpentApprovalAt` names it**, because the attempt the approval covers is counted off the intents: an item paused by a cap or the kill switch for longer than the window must not come back with its count reset and spend the retry again;** the caps are derived from committed intents, so a session that inflates facts, items or decisions must not force an eviction that lowers the per-day count; if the node is still over the cap with only in-window intents left, the daemon stops spawning, fail closed, O6), **free text fenced and cleaned** (G3). `func Migrate(State) (State, bool)` raises an
  older `schema_version` to the current one in place (the store and `fugaro update` call it). Imports no `ee/`, no cloud package (pure).
- **Schema/rules/config change:** two new JSON Schemas; the decode is strict (unknown field is an error). No rules, no config.
- **Tests:** `TestStateRefusesAnUnknownField`, `TestUnknownOpIsRefused`, `TestStaleBaseRevIsRejected`, `TestAllowedPhaseTransitions`,
  `TestAgentActorCannotResolveAPersonDecision`, `TestOnlyAPersonSetsGoalOrApprovesAWorkItem`, `TestFactValuesAreCappedAndCleaned`
  (uses `internal/safetext`), `TestWorkItemFieldsAreNotTheRunRecordsFields`, `TestSummaryIsGeneratedAndCapped`, `TestSchemasMatchTheStructs`,
  `TestEditAfterApprovalRevokes` (an upsert touching rigor, brief, repo, recipe or `DependsOn` clears the approval), `TestApprovalBindsTheFullLaunchSpec`, `TestApprovalCarriesTheAttemptsItCovers` (the approval's `Attempts` is 2 and O6 reads it), `TestSpecDigestCoversEveryFieldButTheBookkeepingList` (reflective), `TestApproveCarriesTheDigestThePersonSaw`, `TestSessionCannotRequeueAnApprovedItem`, `TestOnlyTheDaemonWritesPhaseRunIDAndAttempts`, `TestRetentionKeepsStateUnderTheCap`, `TestRetentionNeverDropsAnIntentInsideTheCapWindow`,
  `TestRetentionKeepsTheIntentsOfAStandingApproval`, `TestIntentCarriesTheApprovalItSpends`, `TestApprovalIsClearedWhenTheItemIsDoneOrCancelled`,
  `TestOversizeOrMalformedStateIsRefusedClientSide` (the rules refuse a token writer outright, so the shape and size check is here, not there),
  `TestSpentApprovalAtIsBookkeepingAndSurvivesMigrate`, `TestUpsertCannotSetApproved` (an upsert carrying `Approved` is refused for every actor),
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
  when **every** path matches an allowlisted routine prefix (`docs/**` and `website/**`, each minus the tree's and the files' exceptions named below; **`mobile/**` and the console renderer are not on it** (`mobile/src/accounts/secure-store.js`, `mobile/src/mailbox/gate.js` and `mac.js` hold credentials and MACs); a `_test.go` file is routine only inside an allowlisted tree, so a test edit in `internal/orchestrator`, `internal/initflow`,
  `internal/agentenv`, `internal/managed`, `schemas/`, `ee/` or `.claude/` is Sensitive). **Everything else, and every unknown, unmatched or malformed
  path, is Sensitive** — a path carrying a byte outside ASCII included, because `strings.ToLower` folds the ASCII range and not the case
  variants a case-insensitive filesystem folds (`ſ` is `s`), and because one name's composed and decomposed spellings are one file on HFS+ — so the classifier's own package, the daemon and the guards default to Sensitive. The owner's routing rule (credentials, writes
  and guards, the managed gate, Terraform and IAM, the agent environment and gateway, agent prompts, recipes, `CLAUDE.md`, `AGENTS.md`, `.claude/**`,
  plugin skills, CI) is a test fixture the allowlist must never contradict; the fixture also lists `mobile/src/accounts/secure-store.js`, `mobile/src/mailbox/gate.js`, `mobile/src/mailbox/mac.js` and the console's pairing and write paths, which must never be Routine. No input but paths: a title or description cannot lower it.
- **The exceptions inside the two routine trees are what a reader does not read: a file, or the class of files a tool reads, and the review rounds of 2026-11-07 added them.** Both trees are on the allowlist for what a reader reads, so
  a path under one that is not reading matter is the path an exception names — the same reason `docs/pending-workflows/**` was already one. **In `website/**`** (the
  routing rule gives the light reviewer docs, tests and design, and the website is this plan's own allowance for a tree that is mostly rendered copy):
  `website/package.json` and `website/package-lock.json` (the install scripts CI and the owner's machine run), `website/content/downloads.json` and
  `website/data/console-sha256sums.txt` (the URLs and the checksums a reader checks a console build against), `website/data/install.yaml` (the install commands a
  reader pastes) and `website/content/security.md` (the published security policy — `SECURITY.md` byte for byte **below a generated header**, and the one website path
  `.claude/rules/credentials-service.md` scopes as security-sensitive). **In `docs/**`**: `docs/release-streams.yaml` (the release gate's own catalogue —
  `internal/streams` refuses a file that breaks it, `scripts/release.sh` takes the CLI stream's name and version from it, and a CI guard fails on a bump that leaves
  its `current` behind), `docs/**/*.graph.json` (**the class** of plan graph artifacts rather than the one file it is written for today — a tool draws a graph
  beside the plan it belongs to, `internal/cli/plan_status.go` walks the whole `docs/` tree for that suffix and reads what it finds, so an entry naming only
  `docs/console-dependency-graph/implementation-plan.graph.json` would leave the next artifact, here or in a repository that took Fugaro, in the cheap class until
  somebody remembered to extend this list; a guard that depends on remembering is not one),
  `docs/website/redesign/capture-screenshots.mjs` and `docs/website/redesign/sample-logo-hue.py` (scripts, committed to be re-runnable). Narrowing an allowlist is the
  direction a review may move it in on its own; **open for the owner is the wider half of that finding** — whether the site's *code* (its layouts, its client scripts
  and its build config: `website/src/layouts/`, `website/public/scripts/`, `website/astro.config.mjs`) should be Sensitive too, which would take the whole tree off
  the allowlist. The one security control the site is held to is **already Sensitive and stays so**: the deployed CSP is `firebase.json` at the repository's root,
  outside `website/**`, and is pinned by `website/test/origins.mjs` and `website/test/brand.mjs`.
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

- **Files (11; `internal/infra/firebasedb.go` is the marker's Plan and write, as the credentials marker is at `rules.PathCredsRulesMarker()` there; open PR #1715 (the mcpauth subtree) edits the same rules template, so the later of the two rebases), and the emulator and parity tests carry `//go:build firebase`:** `internal/budget/rules/rules.go`, `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/marker.go`, `internal/infra/firebasedb.go`,
  `internal/budget/rules/emulator_orchestrator_test.go` (new), `internal/budget/rules/parity_test.go`, `internal/cli/update_converge_test.go`,
  `docs/releases/vX.Y.Z.md` (the release the owner assigns; created or amended), `internal/budget/rules/rules_test.go` (edit: `isNamedAddition` and `TestRulesPreexistingPathsUnchanged` must name the new subtree; PR #1715 edits the same test and golden for `mcpauth`, so the later of the two rebases), `internal/budget/rules/testdata/rules.golden.json` (regenerated), `docs/gcp-live-checklist.md` (the one writer: Checks 300-305, NOT RUN).
- **Interfaces:** a top-level `orchestrator/<id>/` subtree: `.read` for the signed-in person, and a `.write` that is **`false` for every identity the
  rules evaluate** — a run-scoped job token, a minter-minted custom token and any other token-based writer alike, not a denylist of run-scoped jobs
  with a shape check behind it; **shape and size are the client-side validator's (O0's), never the rules'**, because a value that passes a shape check
  is still a write this subtree refuses. **The rules do not bind a person's OAuth write** (IAM bypasses them), so this task claims no write-own
  guarantee. **D1241 was answered on 2026-11-04 as (a), the recommendation: the person's own credential writes the node and O0's validator
  is the whole shape check, so O2's gate is lifted** and what it builds is the token-refusing rules and the converge only. Siblings under `orchestrator/<id>`:
  `state`, `lease` and `audit` (a PUT of the parent would wipe children and a heartbeat would move a shared ETag). **Rules generation:** a marker of its own, on the credentials cascade's precedent (`marker.go`), **not** a `RulesVersion`
  bump, because a bump drops every published policy on an installation that has not redeployed. Task O2 confirms or reverses this in its red step.
- **Schema/rules/config change:** the rules template and marker; `fugaro update`'s rules stage writes the marker after the PUT.
- **Tests:** emulator: `TestOrchestratorSubtreeIsReadableOnlyBySignedInPeople`, `TestOrchestratorRefusesEveryTokenWriter` (a run-scoped job token and a
  minter-minted non-person token both refused, with the person write first proved to succeed, so the deny is not vacuous — the oversize and malformed
  cases are O0's client-side test, not this one); `TestUpdateConvergesTheOrchestratorRulesFromThePreviousGeneration` (an installation at the
  previous generation converges in one `fugaro update` run, nothing else needed), `TestUpdateRefusesToRewindTheOrchestratorMarker`.
- **Command:** `go test -tags firebase ./internal/budget/rules/ -run 'Orchestrator|Parity' -count=1 && go test ./internal/budget/rules/ -count=1 && go test ./internal/cli/ -run 'TestUpdate.*Orchestrator' -count=1`
- **Depends on:** O0 (field names). D1241 is answered, so nothing else gates it. **Level 1.**
- **Commit:** `feat(rules): the orchestrator subtree, closed to every token writer, converged by update (O2)`
- **Red step:** `TestOrchestratorRefusesEveryTokenWriter` fails against the unchanged rules (a deny-by-default root makes it pass vacuously, so the
  test first proves a person write succeeds, then a job and a minted token write fail).
- **OWNER-RUN, Check 300:** with a real installation that is **not** project `belong`: run `fugaro update`; confirm the person's own credential
  (D1241's answer) can write the state node and no token identity can. O2 writes the check text, the owner runs it. Operators' line: "run `fugaro update`".
- **Reviewer:** security (rules and guards).

### O3. The state store, the audit log and the daemon lease

- **Files (8):** `internal/orchestrator/store.go` (new), `internal/orchestrator/lease.go` (new), `internal/orchestrator/audit.go` (new),
  `internal/orchestrator/store_test.go` (new), `internal/orchestrator/lease_test.go` (new), `internal/orchestrator/audit_test.go` (new),
  `internal/orchestrator/testdata/lease-a.json` (new), `internal/orchestrator/export_store_test.go` (new; O0 owns `export_test.go`, so the
  table has no shared writer).
- **Interfaces:** `type Store interface{ Load(ctx) (State, ETag, error); Save(ctx, State, ETag) error }` over `internal/rtdb` `GetETag`/`PutIfMatch` **on the `state` node
  only, a sibling of `lease` and `audit`** (never a PUT of `orchestrator/<id>`), never forced (F5), **the lease checked before the write** (the lease and the state ETag are separate nodes, so a lost lease does not by itself fail a `PutIfMatch`) — **amended by O3's implementation: `Save` makes exactly one conditional write and a 412 comes back as `ErrStateMoved` with no second attempt, because a body derived from the document the caller read at its ETag would, under a fresher ETag, write over whatever landed in between (a lost update, and a revision standing for two documents); the earlier "bounded re-read on 412, re-checking the lease before each retry" is therefore the caller's re-read — level-triggered, D1229 — and the lease check sits before the one write, which is what stops a daemon whose lease moved from recording the intent it would act on (D1231); the lease's own claim, beat and release keep a bounded re-read (`leaseAttempts`, `ErrLeaseContention`), because there the decision is a pure function of the node just read**; `Load` applies O0's
  `Migrate` and the next `Save` writes the migrated `schema_version`; `Lease` after `internal/mailbox/lease.go`'s heartbeat-lease pattern; `Audit.Append(ctx, Entry)` with
  a deterministic id (`<run>:<transition>`) and a server-assigned order (`rtdb.ServerNow`), D1229 and D1230.
- **Schema/rules/config change:** none (O2 holds the rules).
- **Tests:** `TestTwoDaemonsOneLease`, `TestStaleSaveIsRefusedNotForced`, `TestSaveRechecksTheLeaseBeforeARetry`, `TestAuditAppendIsIdempotentOnADerivedId`, `TestLeaseExpiresWhenTheHolderStopsBeating`,
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
- **Interfaces:** `state [--brief]` prints the generated summary from the live node (short, one screen), **with stored text fenced and cleaned** (G3);
  **`state --brief --offline` prints the daemon's cached brief instead and reaches no network** (D1243: the cache is O8's writer, the print shows its
  freshness stamp, and with no cache yet it says so rather than falling back to the node); `events [--since]` reads the
  audit log; `patch -f file|-` validates with O0; **the actor is `Session`, raised to `Person` only by the Typed confirmation; no flag, environment variable or file selects `Daemon` or `DecisionAgent`, and `Approved` is written only by `work_item_approve`, never an upsert**; **person-only ops (`work_item_approve`, `goal_set`, resolving a person decision) need a Typed confirmation at a real
  terminal that shows the digested spec** (the approve op carries that digest) (`initflow.CanConfirm(Typed)`) and are refused otherwise, and `--yes` never covers them. The agent-marker check
  (`AgentRefusal`, `internal/initflow/stage.go`) stays as an extra refusal but **is a mitigation, not a barrier** (`env -u CLAUDECODE`, or a direct node
  write, passes it), so it is not what G2 rests on; the residual is D1242, whose paired-device channel is deferred and gates nothing. No `spawn`, no `retry`: launch is the daemon's (T2).
- **Schema/rules/config change:** the `--json` output schema, additive, `schema_version` 1.
- **Tests:** `TestStateBriefFitsOneScreen`, `TestEventsJSONMatchesTheSchema`, `TestPersonOpsNeedATypedConfirmation`,
  `TestPatchFromAnAgentSessionRefusesPersonOps` (the accident only), `TestStateBriefFencesAndCleansStoredText`, `TestStateBriefOfflineReadsOnlyTheCache`
  (a client that fails every call proves no node read), `TestPatchRejectsAStaleRev`, `TestApproveShowsTheSpecItDigests`, `TestPatchNeverActsAsTheDaemon`,
  `TestCommandIsRegisteredOnce`; `go test ./ee/clidoc/ -count=1` for the regenerated reference.
- **Command:** `go test ./internal/cli/ -run 'TestOrchestrator|TestStateBrief|TestEventsJSON|TestPatch' -count=1`
- **Depends on:** O0, O3, O2 (`patch` writes the real node with the person's own credential, D1241's answer). **Level 2.**
- **Commit:** `feat(cli): fugaro orchestrator state, events and patch with --json (O5)`
- **Red step:** `TestPersonOpsNeedATypedConfirmation` fails to build, then fails when a patch with no terminal closes a person decision. **P1 is resolved
  as negative:** the agent-session detection is not a barrier (`stage.go` `AgentAdvice`, `init.go`), so G2 rests on the Typed confirmation and the
  residual is D1242.
- **Reviewer:** security (the person-class fence).

### O6. The dependency-driven launcher: intent-first claims, caps, running cap and breaker

- **Files (8):** `internal/orchestrator/launch.go` (new), `internal/orchestrator/intent.go` (new), `internal/orchestrator/caps.go` (new),
  `internal/orchestrator/breaker.go` (new), `internal/orchestrator/launch_test.go` (new), `internal/orchestrator/caps_test.go` (new),
  `internal/orchestrator/breaker_test.go` (new), `internal/orchestrator/export_launch_test.go` (new).
- **Interfaces:** `type RunView` (the read-only view `Plan` takes; **O6 defines it**, O7's `runview.go` only implements `RunViewReader` to fill it). `type Launcher interface{ Launch(ctx, Spec, RunID) (RunID, error) }` implemented in O8 over the `fugaro run` path (the core
  must not import `internal/cli`); `func Plan(State, RunView, Caps, Now) []Action`: an item is eligible only if `Approved` by a person **and the
  approval's `SpecDigest` equals O0's `SpecDigest` of the item's current state** (the same function, not a second list of fields), **the item has no intents stamped with that approval's `At` at or above the attempt being planned** (the id is item + attempt, so this is the same test), all `DependsOn` are `done`, the
  running count and the per-cycle, per-minute, per-hour and per-day counts are under `Caps`, and the kill switch is off (consulted through an
  interface over `budget`). **A retry is planned only for the attempt the approval still covers** (the approved attempt plus one): the item's latest
  intent for that approval must have **failed** — an outcome the run reported, and the retry reuses the approval only while `SpecDigest(item)` still
  equals the approval's digest — and **the attempt count, read off the intents stamped with that approval's `At`, must be below the approval's `Attempts`**
  (a later approval's intents are not inherited, and the count does not fall out of reach when the window evicts an intent: O0's retention keeps the
  intents of a standing approval). A failure whose cause is a **refusal** (a failed
  `Precheck`, the kill switch, a spend cap, a validation refusal) is never retried. **A refusal burns no attempt only when the `Launcher` refuses
  before the start — no `launch.json` under the intent's run id.** A run that started and then reported a refusal (its record says spend cap or refused)
  is an attempt like any other: the outcome came from a started run, so counting it as "no attempt" would leave the item eligible and relaunch it every
  tick until a cap stopped it. A pre-start refusal is **replanned under the same intent** — the id is item + attempt, so nothing new is committed and
  the planner's "no intent for the attempt being planned" still holds — and the caps count that refused intent once. **The per-minute, per-hour and per-day counts are derived from the committed `Intents` using their server time**
  (`rtdb.ServerNow`), never from in-memory counters, so a launchd restart loop does not reset them, and **a retry is an intent like any other: it counts toward the per-cycle, per-minute, per-hour, per-day and running caps**. **Intent first (D1231, corrected):** the committed intent carries the **run id minted before the start** (`task.NewRunID`), **the attempt it is for**, the spec digest and **the `At` of the approval it spends**, and **committing the retry's intent clears `Approved` and sets `SpentApprovalAt` to that approval's `At` in the same `PutIfMatch`** — the retry spends the allowance, so a second failure never relaunches under a standing approval, while the first intent clears nothing; `Launch` starts the run under that id;
  **recovery re-issues the same launch under the recorded run id: through `--retry` when `task.json` exists; when it does not, `retrySpec` refuses ("has no task.json"), and rebuilding the task from the item's current spec would launch a spec edited after the intent under the old intent, so the task is recreated under the recorded run id only when `SpecDigest(item)` equals the intent's digest **and either the approval still stands or the intent is the retry that spent it** (`intent.ApprovalAt` equals the item's `SpentApprovalAt`; committing a retry clears `Approved`, so "the approval stands" alone would abandon every retry that crashed before `task.json`), else the intent is marked `abandoned` (nothing started). Through `launchRun`, which reports "already-launched" if `launch.json` exists or launches once if not, and never mints a second run id** (`internal/cli/run.go` `launchRun`: the claim is `<run prefix>launching`, one per run id, never deleted after a launch, taken over only when stale by a generation-matched overwrite, so **the per-run-id claim IS the concurrency fence** for two daemons and a stale claim; "adopt, never issue" would strand an item in `running` if the crash fell between `task.json` and the start); `Breaker` counts consecutive failed or empty cycles (3) and writes a `pending_decisions` item; **ticks that end "still in flight" do not count toward it**. If the state node is over its size cap with only in-window intents left, `Plan` returns no spawn (fail closed). An ambiguous launch error leaves the intent in flight up to `ClaimTTL` (10 minutes, the open item in section 6); the runner's `ErrDuplicateExecution` is the second fence.
- **Schema/rules/config change:** none.
- **Tests:** `TestSpawnNeverLeavesTheApprovedItem`, `TestUnapprovedItemIsNeverLaunched`, `TestCapsStopALoop`, `TestKillSwitchStopsSpawning`,
  `TestRecoveryReissuesUnderTheRecordedRunID` (a fake that records started run ids; the real-store crash test is O8's), `TestEditedSpecIsNeverLaunched`,
  `TestCapsSurviveARestart` (a new process with the same committed intents has the same counts), `TestBreakerRaisesADecision`, `TestStillInFlightTicksDoNotTripTheBreaker`,
  `TestOneAutomaticRetryUnderTheSameApproval` (a first `failed` attempt is retried once under the same approval, with a new run id for the retry's attempt), `TestRetryRefusedWhenTheSpecChanged` (an edit after the first failure revokes the approval and nothing is retried), `TestRefusalIsNotRetried` (a `Precheck` refusal, the kill switch, a capped launch and a validation refusal each plan no retry, and each of them refuses **before the start**, so no attempt is burned and the item is still eligible once the refusal's cause clears), `TestAPreStartRefusalReissuesTheSameIntent` (the refusal leaves no `launch.json`, so the next tick replans it under the intent already recorded, and the caps count it once), `TestAStartedRunsRefusalCounts` (a run that started and reported a refusal is an attempt: the item is not relaunched on the next tick and the allowance is spent), `TestRetryCountsTowardEveryCap`, `TestAFailedAttemptNeedsANewApproval` (**the second** failure: the allowance is spent, so a person's new approval is needed), `TestOversizeStateStopsSpawning`, `TestRecoveryBeforeTaskJSONAfterAnEditAbandonsTheIntent` (the fake; the real-store run is O8's), `TestDependencyOrderIsRespected`.
- **Command:** `go test ./internal/orchestrator/ -run 'Launch|Caps|Breaker|Plan' -count=1`
- **Depends on:** O0, O4 (the `Caps` values; the type is defined here and O4 fills it in O8). **Level 1.**
- **Commit:** `feat(orchestrator): the dependency-driven launcher with intent-first claims and spawn caps (O6)`
- **Red step:** `TestRecoveryReissuesUnderTheRecordedRunID` fails to build, then fails when recovery mints a new run id instead of reusing the recorded
  one; `TestOneAutomaticRetryUnderTheSameApproval` fails when a first failure is retried twice, or not at all.
- **Reviewer:** security (money).

### O7. The reconcile tick and notifications

- **Files (6):** `internal/orchestrator/tick.go` (new), `internal/orchestrator/runview.go` (new: implements `RunViewReader` only; O6 owns `RunView`), `internal/orchestrator/notify.go` (new),
  `internal/orchestrator/tick_test.go` (new), `internal/orchestrator/notify_test.go` (new), `internal/orchestrator/export_tick_test.go` (new).
- **Interfaces:** `Tick(ctx, Store, RunViewReader, Launcher, Notifier)`: load, fold finished runs into the item phases **by joining on the run id the daemon recorded in the intent, never on a run-writable field** (a run's own heartbeat, title or label), ask `Plan` (O6), execute
  intents, commit; a `Notifier` interface (a log sink here; O9 supplies the sinks **Q3** names — the macOS OS notification first, the owner's existing
  mobile push as the second channel, the desktop notifier on Linux) fires on a finished run or a decision needed; level-triggered, no cursor
  (D1229). Nothing in it calls a model (D1221).
- **Schema/rules/config change:** none.
- **Tests:** `TestTickMarksAFinishedRunDone`, `TestTickIsIdempotentOnRepeat`, `TestTickNotifiesOnceForADecision`, `TestTickMakesNoModelCall` (the
  dependency list has no agent package), `TestTickLeavesAPersonDecisionOpen`, `TestTickJoinsOnTheRecordedRunIDOnly`, `TestStillInFlightTicksAreNotFailures`.
- **Command:** `go test ./internal/orchestrator/ -run 'Tick|Notify' -count=1`
- **Depends on:** O3, O6. **Level 2.**
- **Commit:** `feat(orchestrator): the reconcile tick and its notifications (O7)`
- **Red step:** `TestTickIsIdempotentOnRepeat` fails to build, then fails when a second tick launches again.
- **Reviewer:** light (no new guard; the money path is O6's). **Q3 was answered on 2026-11-04** (the recommendation): the **macOS notification** plus the
  owner's existing **mobile push**, with **Linux falling back to the desktop notifier**. What the tree itself carries is **U9** (reconciliation section
  5): no push implementation exists anywhere — the phone's push is design-only under D121's "no push in v1", the console's notification is an unbuilt
  design gap (F27/D33) — and the only device channel that does exist is the RTDB mailbox the phone polls. So the mobile half is **UNVERIFIED against
  `main`** (read as the owner's own setup, the way F18 reads the scripts) and the plan ships the OS notification as slice 1's channel, with O9's
  `Notifier` free to reach the others.

### O8. The foreground daemon command and the run-path launcher

- **Files (11):** `website/content/cli-reference.md` (regenerated), `internal/cli/orchestrator_run.go` (new), `internal/cli/orchestrator_launcher.go` (new), `internal/cli/orchestrator_version.go` (new: the binary-version check and the clean exit that hands the unit to the new binary), `internal/orchestrator/cache.go` (new: the session-start cache writer, D1243), `internal/orchestrator/cache_test.go` (new), `internal/cli/orchestrator_run_test.go` (new),
  `internal/cli/orchestrator_launcher_test.go` (new), `internal/cli/orchestrator_version_test.go` (new), `internal/cli/orchestrator_cmd.go` (edit: adds the `run` subcommand), `internal/cli/launch_budget.go`
  (a read-only export if one is needed; otherwise untouched).
- **Interfaces:** `fugaro orchestrator run` = the lease, then `Tick` every N seconds, with the SSE stream of the project's RTDB as a wake-up (resume
  gives a full `put`, F8); the `Launcher` implemented over the same pre-launch path `fugaro run` uses (claim plus `budget.Precheck`, F11, T2). **After
  each tick it writes the session-start cache (D1243): `$XDG_CACHE_HOME/fugaro/orchestrator/<id>.json`, placed through `localcfg.UserDir(..., DirCache)`
  the way the name check's cache is, **written atomically (a temp file in the same directory, then a rename, mode 0600)** and carrying the generated
  brief, the server time it was written and its own schema version; the brief is fenced and cleaned (G3), and the write is best effort, so a failure is
  logged and never fails the tick. That stamp is what O5's `--offline` print and O10's session-start print show, so staleness is visible rather than
  silent.** It
  refuses any gated converge or publish command (G8): the daemon reports "update due" and nothing else; **it never passes `Yes`** to any confirmation
  and the daemon package does not depend on the gated-command packages.
  **Start-up, before the first tick (the daemon converges its own state node — Q2's answer ran `update` past it, and T7's ruling of 2026-11-05 keeps
  this: `Migrate` at start-up is the daemon's, while the unit's own template is `update`'s re-render, O9):** the state is loaded through O3's store, whose `Load` applies O0's `Migrate`, so a state node left at an older
  `schema_version` is migrated **by the daemon's own start-up** and the next `Save` writes the raised version — not by `update`, which no longer
  touches anything under `orchestrator/`. **Each tick then checks the binary the unit names against the running process:** the daemon reads the version
  of **the path the unit rendered into it** (`fugaro orchestrator run --binary <stable path>`, which `install` writes; never `os.Executable()`, which on
  Linux is `readlink /proc/self/exe` — the resolved, versioned path `brew upgrade` deletes, so the read would fail after every upgrade and, a failed read
  being no reason to exit, the daemon would never restart) through a bounded `--version` call — no network, no model, a few milliseconds — and **when
  that version differs from the running one it exits cleanly (status 0, lease released), so launchd's `KeepAlive` or systemd's `Restart=always` starts
  the new binary from the same stable path.** The rule is **differs**, not "newer": a downgrade restarts the daemon too, and escalates nothing, because
  whoever can write that path already owns the login unit. That is what makes a `brew upgrade` reach a running daemon with no operator step and no unit
  re-render at all — the re-render is for a *template* change, and it is `fugaro update`'s own step (O9, T7); a read that fails, or an unparseable version, is logged and **never** a reason to exit (a broken package-manager link must not stop the
  daemon), and the check is **skipped only while a `Launch` is in progress**, never for a run merely in flight, so a busy queue still upgrades and a
  tick never abandons a start. A foreground `run` with no `--binary` runs the check off: nothing restarts it.
- **Schema/rules/config change:** none.
- **Tests:** `TestDaemonDoesNotDependOnGatedCommands` (an import and dependency test over `go list -deps`, not a string check), `TestDaemonNeverPassesYes`,
  `TestCacheIsWrittenAtomically` (a reader concurrent with the writer never sees a partial file, and an interrupted write leaves the previous cache in
  place), `TestCacheCarriesItsFreshnessStamp`, `TestCacheWriteFailureNeverFailsATick`,
  `TestCrashBetweenIntentAndLaunchMakesOneRun` (the real `internal/runstore` and `internal/gcpfake`: crash after the intent, again after `task.json` and before the start, and again after the start; recover each time, one run, never a stuck `running` item), `TestCrashAfterTaskJSONBeforeTheStartStillStartsOnce`, `TestRecoveryBeforeTaskJSONAfterAnEditAbandonsTheIntent` (recover before `task.json`: an edit since the intent abandons it, an unchanged spec recreates the task under the recorded run id), `TestRecoveryBeforeTaskJSONOfASpentRetryStartsOnce` (the crash is a retry's, so `Approved` is gone: the intent is still the item's spent one, and the task is recreated once under the recorded run id), `TestRunLauncherUsesTheLaunchClaim`, `TestRunLauncherRunsPrecheck`, `TestRunStopsWhenTheLeaseIsLost`,
  `TestRunReportsUpdateDueAndDoesNothingElse`, `TestMigrateRunsAtDaemonStart` (a state node at the previous `schema_version` is migrated by the
  daemon's own start-up and the next write carries the raised version), `TestDaemonRestartsWhenTheUnitBinaryDiffers` (the stub binary at **the path the
  unit rendered**, not `os.Executable()`, reports a different version — a newer one and a downgrade alike — so the daemon exits 0 with the lease released
  and nothing launched; an equal version keeps it running, an unreadable one is logged and does not exit, and an in-progress `Launch` skips the check).
- **Command:** `go test ./internal/orchestrator/ -run 'TestCache|TestMigrate' -count=1 && go test ./internal/cli/ -run 'TestDaemon|TestRunLauncher|TestRunStops|TestRunReports|TestMigrate|TestCrash|TestRecoveryBefore' -count=1` (the cache writer and its tests are `internal/orchestrator` files, and the crash and recovery tests are named `TestCrash*` and `TestRecoveryBefore*`, so a `-run` without them would pass over the whole money path)
- **Depends on:** O0 (`Migrate` at start-up), O2 (the node must exist), O5, O7. **Level 3.**
- **Commit:** `feat(cli): the orchestrator daemon loop over the fugaro run launch path (O8)`
- **Red step:** `TestRunLauncherRunsPrecheck` fails to build, then fails when the launcher skips `Precheck`; `TestDaemonRestartsWhenTheUnitBinaryDiffers`
  fails when the daemon keeps running after the binary at the unit's path has moved on, which is the upgrade hole T7 is about.
- **Reviewer:** security (the launch path).

### O9. The unit: `fugaro setup`'s step, `update`'s re-render and `fugaro orchestrator install`

- **Files (16):** `website/content/cli-reference.md` (regenerated), `internal/orchestrator/service/launchd.go` (new), `internal/orchestrator/service/systemd.go` (new),
  `internal/orchestrator/service/service.go` (new), `internal/orchestrator/service/service_test.go` (new), `internal/orchestrator/service/testdata/unit-a.golden`
  (new), `internal/cli/orchestrator_install.go` (new), `internal/cli/orchestrator_install_test.go` (new), `internal/cli/orchestrator_cmd.go` (edit: install,
  uninstall, status), **the setup step: `internal/cli/init_orchestrator_stage.go` (new), `internal/cli/init_orchestrator_stage_test.go` (new), and
  `internal/cli/init_stages.go` (edit: registers the two stages, one per door)**, **the update-side step (T7's ruling): `internal/cli/update_unit_stage.go`
  (new) with `internal/cli/update_unit_stage_test.go` (new)**, **and the drift report: `internal/cli/doctor.go` (edit: `doctor`
  reports a unit written from an older template) with `internal/cli/doctor_orchestrator_test.go` (new)** — so the person is told "update due" for the
  unit the one way the CLI already reports drift, and O10's session-start nudge can carry the same line. **`internal/cli/update.go` is not touched by this task**: `update` already runs `initStages` (`init_stages.go`'s list, which `converge` builds for both doors), so the update-side step is registered there like the checkout stage, and `update.go`'s own flags and stages are none of this task's business.
- **Interfaces:** `fugaro orchestrator install|uninstall|status`: writes a per-user unit that runs `fugaro orchestrator run`, restarts after a crash or
  reboot (`KeepAlive` on launchd, `Restart=always` on systemd), never touches a system path or needs root; macOS and Linux only (D1232), Windows
  answers "not supported" (`docs/windows-cli`). Content is rendered from a fixed template with the binary path quoted; nothing from state or policy
  goes into the unit. **The unit points at the stable binary path** (the package manager's stable link, e.g. Homebrew's `opt` path, never a versioned
  `Cellar` path that `brew upgrade` removes, which would crash-loop) and **names it twice: as the program to run and as `fugaro orchestrator run
  --binary <stable path>`**, so O8's version check reads the path the unit actually re-runs, not the process's own resolved executable — which on Linux
  is the versioned path the upgrade deletes. **`install` is idempotent and means "install, or re-render from
  the current template and restart"**: it writes the unit when there is none, re-renders it when there is one, then restarts the service; it
  **never migrates state** — that is the daemon's own start-up (O8, T7) — and `install` is refused under a coding agent's marker while `--yes` never
  covers it (a unit runs code at login); **the residual is the same one G2 names — `env -u CLAUDECODE fugaro orchestrator install` — and it escalates
  nothing, because whoever can call that can write a plist into the login directory directly** (the marker is a mitigation, not a barrier).
  **The owner's Q2 answer (2026-11-04 — a different answer from the one this PR first recorded, which is superseded):** the **first install belongs
  to `fugaro setup`**, as one of its listed steps. The setup step renders the plan line every stage
  renders — naming the unit it writes and where it is written — and calls the very same installer `fugaro orchestrator install` calls, so there is one
  implementation and no second renderer; it is registered for the `setup` door in `initStages` (`update` runs the same list and its door removes this
  step, the way it removes the project stage's creation path), it is **a real terminal only and refused under a coding agent's marker** (the unit is
  written by the person's own run, never by an agent), and in a **managed project it follows the no-prompt rule**: no extra confirmation, because the
  step is a line of a plan the person already reads before it is applied. **That closes T6** — nothing asks a question the no-prompt rule forbids.

  **T7's ruling (2026-11-05, not the recommendation; it amends Q2's "`update` never touches the unit"):** **`fugaro update` re-renders the daemon's
  unit, so the HARD RULE is literally true and no release ever asks for a second command.** The **update-side step** (`update_unit_stage.go`,
  registered for the `update` door in `initStages`, and by nothing else) does four things, and nothing more:
  **detect** an installed unit (the platform's own login-unit file under the person's home — `~/Library/LaunchAgents/<label>.plist` on macOS, and
  `localcfg.UserDir(..., DirConfig)` joined with `systemd/user/<name>.service` on Linux — the same path `install` writes; none installed means the
  step ends there, with a plan line saying so);
  **compare** the template it would render now with the file on disk, using `service.Render`, the same renderer `install` uses, so there is no second
  renderer and no stamp to drift;
  **re-render** the file when the two differ — the unit's text, the stable binary path and the `run --binary` argument alike — and leave a current unit
  byte-for-byte alone (a re-render that churns an identical file, or a reload with nothing to reload, would make the one command noisy on every run);
  and **reload** the login service (`launchctl` at the user's own login level on macOS, `systemctl --user` on Linux, the same interface
  `install` uses, faked in tests) so the running daemon picks the re-rendered unit up.
  **It is a write to the person's own home and their own service manager, never to a system path and never to the cloud**, and it is stated as such in
  the plan line: no root, no `sudo`, no network. **It never installs a unit**: a machine without one — a project that did not choose the daemon, or a
  person who skipped `setup` — gets no daemon from `update`, and that is a boundary of the ruling rather than an oversight. **It follows the same
  plan-and-confirm rules as every other `update` step**: a managed project's gate stays exactly as it is (no new prompt and no new exemption for this
  step), and at a terminal it asks for the step's own class — **`Typed`**, the strictest, because a unit is code that runs at login and `--yes` never
  covers one (the same class `install` takes) — with the agent-marker refusal G2 names applying here too, so an agent-driven `update` writes no unit
  and the drift stays visible through `doctor` and O10's nudge until the person runs the command themselves. **`fugaro orchestrator install` stays the
  explicit command** — the first install on a machine that skipped `setup`, and repair — and the re-render reuses its installer rather than a second
  copy of it, but **no release's `### For operators` ever needs it**. `Migrate` stays the daemon's own start-up (O8), so this step carries no state work.
- **Schema/rules/config change:** none.
- **Tests:** `TestLaunchdPlistMatchesTheGolden`, `TestSystemdUnitMatchesTheGolden`, `TestInstallWritesOnlyUnderTheUserHome`, `TestWindowsIsRefusedNamed`,
  `TestUninstallRemovesWhatInstallWrote`, `TestInstallRefusedUnderAnAgentMarker`, `TestUnitPointsAtTheStableBinaryPath` (never a versioned
  `Cellar`-style path, and passed again as `run --binary`), `TestUnitPinsKeepAliveForACleanExit` (the goldens carry `KeepAlive=true` on launchd and
  `Restart=always` on systemd, not `SuccessfulExit`, because the daemon exits 0 on purpose when the binary differs and a unit that treats a clean exit
  as done would never hand over), `TestInstallRerendersAndRestartsAPreviousVersionUnit` (a unit written by the previous version is re-rendered from the new
  template and the service restarted, by one `fugaro orchestrator install` and by nothing else), `TestInstallIsIdempotent` (a second run re-renders
  what it wrote and restarts once), `TestSetupInstallsTheUnitAsAPlanStep` (a `setup` run's plan names the unit and its path, and the step calls the
  same installer), `TestSetupInstallsInAManagedProjectWithoutAPrompt` (a managed project's `setup` reaches the step with no confirmation of its own:
  the plan line is the only thing the person is shown), `TestSetupNeverInstallsUnderAnAgentMarker` (an agent session's `setup` runs to completion and
  writes no unit, the step skipped with the marker's own sentence), and `TestDoctorReportsAnOutOfDateUnitTemplate` (the file on disk differs from what
  the current template renders, so `doctor` reports the drift — never a command to run silently, and never a write). **T7's ruling adds the update-side
  half**, all in `update_unit_stage_test.go`: `TestUpdateRerendersAnInstalledUnitWhenTheTemplateChanged` (a machine whose unit was rendered from the
  previous template converges under **one `fugaro update`** — the file is re-rendered from the current template and the service reloaded — with no
  second command, which is the HARD RULE), `TestUpdateNeverInstallsAUnit` (no unit installed: `update` writes none and the plan line says why, so a
  project that did not choose the daemon never gets one), `TestUpdateLeavesACurrentUnitAlone` (the rendered text equals the file: no write and no
  reload, so a repeated `update` is quiet), `TestUpdateRerendersWhenTheBinaryPathChanged` (the render carries the stable path, so a moved Homebrew
  prefix re-renders), `TestUpdateUnitStepNeedsATypedConfirmation` (the class is `Typed`: `--yes` does not cover it and an agent-marker run writes no
  unit), `TestUpdateUnitStepTouchesNoSystemPath` (the write stays under the person's home and no `sudo` is asked for), and
  `TestUpdateUnitStepUsesTheInstallersRenderer` (the step calls `service.Render` and the installer's reload, so there is no second renderer). Tests
  render into `t.TempDir()` and never call
  `launchctl` or `systemctl` (an interface is faked).
- **Command:** `go test ./internal/orchestrator/service/ -count=1 && go test ./internal/cli/ -run 'TestLaunchd|TestSystemd|TestInstall|TestUninstall|TestWindows|TestSetup|TestUpdateRerenders|TestUpdateNeverInstalls|TestUpdateLeaves|TestUpdateUnitStep|TestDoctorReportsAnOutOfDateUnitTemplate' -count=1`
- **Depends on:** O8. **Level 4.**
- **Commit:** `feat(cli): install the orchestrator unit from fugaro setup and re-render it from fugaro update (O9)`
- **Red step:** `TestInstallWritesOnlyUnderTheUserHome` fails to build, then fails when a path outside the home is accepted; **and, for T7's ruling,
  `TestUpdateRerendersAnInstalledUnitWhenTheTemplateChanged` fails while `update` leaves the previous template's unit in place — an installation at the
  previous template version must converge under `fugaro update` alone, which is the HARD RULE.**
- **OWNER-RUN, Check 301 (text written by O2):** on the owner's Mac: `fugaro orchestrator install`, close the terminal, reboot, confirm the daemon is running and exactly one
  instance holds the lease (start a second by hand and see it refuse); **then the upgrade half: a brew-style upgrade on a
  throwaway machine, with the daemon running, and the daemon must notice the new binary and exit so launchd starts it, with no command typed**; on a
  Linux machine the same with systemd. **Then T7's own half, which a fake cannot settle:** take that machine to a release that changes the unit's
  template, run **`fugaro update`** and nothing else, and confirm the unit file is re-rendered, the service reloaded, the daemon running under it, and
  that a second `fugaro update` writes nothing. No agent runs it.
- **Reviewer:** security — **`security-reviewer` (Opus), a mandatory independent review with at least one mutation check** (the owner's routing rule
  puts a guard and a write path here, and T7's ruling doubles it: code that runs at login, plus code `fugaro update` runs on the person's own machine).

### O10. The plugin skill and the shared session-start hook slot

- **Files (6):** `plugin/skills/orchestrator/SKILL.md` (new), `plugin/skills/orchestrator/reference/state.md` (new), `plugin/orchestrator_skill_test.go` (new),
  the session-start print (**`docs/plugin-user-scope` designs no hook: its nudge is a skill sentence running `doctor --offline`, F17**; this task writes the
  print as a skill sentence, and a real plugin hook only if the Claude Code plugin hook configuration is confirmed in the red step;
  the path `plugin/skills/hooks/session-start` is not where Claude Code plugin hooks live), `plugin/plugin_test.go` (edit: the new skill in the list), `plugin/readme_test.go` (edit).
- **Interfaces:** the skill teaches a session to read **`fugaro orchestrator state --brief --offline`** first (the daemon's cached brief), to record
  decisions and facts through `patch` as they happen and never to rely on chat history; it names only commands O5 has (the plugin tests check them).
  **D1243 was answered on 2026-11-04: the print reads O8's local cache and reaches no network, so D952's no-network rule holds at session start.** The
  print shows the short summary, any pending decision and **the cache's freshness stamp**, so a stale cache is visible rather than silent, **fenced and
  cleaned** (G3), ordered after the update nudge — **which carries O9's doctor line when the installed unit was rendered from an older template**, so
  an `update` that could not confirm the re-render (an agent-marker run, a run with no terminal) leaves a drift the person is told about rather than a
  silent one.
- **Schema/rules/config change:** the plugin skill list.
- **Tests:** `TestOrchestratorSkillNamesOnlyRealCommandsAndFlags`, `TestSessionStartPrintsOneScreen`, `TestSessionStartPrintFencesStoredText`, `TestHookNeverRunsAGatedCommand`,
  `TestSessionStartPrintMakesNoNetworkCall` (the print's command is the `--offline` one and a client that fails every call still prints the cache),
  `TestSessionStartPrintShowsTheFreshnessStamp`, `TestSkillSaysFirestoreIsNotTheStore`.
- **Command:** `go test ./plugin/ -count=1`
- **Depends on:** O5 and **O8** (D1243's answer makes the cache O8's writer, so this task moved from level 3 to **level 4**). **Level 4.** External: `docs/plugin-user-scope`'s nudge (a skill sentence, unbuilt); the fallback is the `CLAUDE.md` instruction (D1237's stated fallback).
- **Commit:** `feat(plugin): the orchestrator skill and the session-start state print (O10)`
- **Red step:** `TestOrchestratorSkillNamesOnlyRealCommandsAndFlags` fails when the skill names a flag the CLI lacks.
- **Reviewer:** security (plugin skills are security paths).

### O11. The cutover: the one-time import and the rollback note (D1235)

- **Files (7):** `website/content/cli-reference.md` (regenerated), `internal/cli/orchestrator_import.go` (new), `internal/cli/orchestrator_import_test.go` (new), `internal/cli/orchestrator_cmd.go` (edit: `import`),
  `docs/how-to/orchestrator-cutover.md` (new), `docs/releases/vX.Y.Z.md` (edit: the cutover line), `internal/cli/docs_orchestrator_cutover_test.go` (new).
- **Interfaces:** `fugaro orchestrator import -f dump.json` takes the one-time session dump (spec "Migration") through O0's validator and caps
  (**the first stored-prompt-injection surface**), as a `patch` from a person; the how-to states the **cutover at once** (D1235): the daemon replaces the
  owner's sweep, watcher and queue scripts the day it is started. **The how-to carries the manual rollback (T1, answered 2026-11-04, the
  recommendation):** the old scripts are not deleted by any step here and are **kept for 14 days**, and the rollback is the three steps the owner runs
  by hand — (1) stop the daemon (`fugaro orchestrator uninstall`), (2) re-arm the sweep, the Monitor watcher and the queue scripts, (3) confirm one
  launch through the scripts before removing anything. **No rollback command is built**, so those steps are the whole rollback and the how-to is where
  they live.
- **Schema/rules/config change:** none.
- **Tests:** `TestImportRunsTheSameValidatorAsPatch`, `TestImportRefusesAnOversizeDump`, `TestImportNeverMarksAnItemApproved` (a dump cannot approve work:
  a person approves), `TestDocsOrchestratorCutoverNamesTheManualRollback` (the how-to's three steps, the 14 days and the absence of a rollback command).
- **Command:** `go test ./internal/cli/ -run 'TestImport|Docs' -count=1`
- **Depends on:** O5, O8, O9. **Level 5.**
- **Commit:** `feat(cli): import the session dump and document the cutover and its rollback (O11)`
- **Red step:** `TestImportNeverMarksAnItemApproved` fails to build, then fails when a dump's `approved` is honoured.
- **OWNER-RUN, Check 302 (text written by O2):** the cutover itself, on the owner's own installation (never `belong`'s sandbox from an agent): start the daemon, confirm
  one finished run, one dependency-driven launch, one notification, with the spend caps and kill switch tested first. Because there is no shadow week
  (D1235), the owner runs this with the scripts still in place **but never launching at once**: the two share no launch key (the claim is per run id),
  so scripts and daemon both launching the same work would double-launch. The check starts with the scripts' launching stopped (their watching may
  stay). **The scripts then stay on disk for 14 days** (T1's answer), and the check's second half, run at any point inside those 14 days, is the
  **manual rollback**: stop the daemon with `fugaro orchestrator uninstall`, re-arm the sweep, the watcher and the queue scripts, and see one launch
  through them. **No rollback command is built**, so those steps are the whole rollback and the owner decides when the scripts are removed.
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
- **Depends on:** O0, O1, O3, O4, O6. **Level 2.** Not started before the owner approves slice 2 by name, and **Q4 adds a gate under that go
  (answered 2026-11-04, the recommendation): slice 2 is approved only after slice 1 has run 14 days and Check 303 has measured the cycle cost. The
  bounds D1228 proposes (6 calls per hour, 40 per day, a breaker at 3) stay proposals until Check 303's number confirms or replaces them.**
- **Commit:** `feat(orchestrator): the bounded decision agent (O12)`
- **Red step:** `TestDecisionAgentRunsWithTheAgentDenyRules` fails to build, then fails when the runner omits the deny rules.
- **OWNER-RUN, Check 303 (text written by O2):** with the owner's own Claude login: one real decision call, the count caps and the breaker proved, the cost of a cycle
  measured (this is what turns U4 from a target into a number). **It is also Q4's gate:** slice 2's approval waits for this number and for slice 1's 14 days, so
  the check is run before O12 is started, not after.
- **Reviewer:** security (prompts and the agent environment).

### O13. The decision agent in the tick (slice 2)

- **Files (5):** `website/content/cli-reference.md` (regenerated: the new flag), `internal/orchestrator/tick.go` (edit), `internal/cli/orchestrator_run.go` (edit: the `--decide` flag, off by default), `internal/orchestrator/tick_decide_test.go`
  (new), `internal/cli/orchestrator_run_decide_test.go` (new).
- **Interfaces:** the tick wakes the decision agent only for the cases the spec's table names (retries exhausted, stuck past the timeout, an ambiguous
  review result); every other event stays code (spec "When to wake the LLM"). Off unless asked; the breaker pauses the daemon and raises a decision.
- **Schema/rules/config change:** none.
- **Tests:** `TestTickWakesTheAgentOnlyForJudgementCases`, `TestHeartbeatsNeverWakeTheAgent`, `TestBreakerPausesTheDaemon`, `TestDecideIsOffByDefault`.
- **Command:** `go test ./internal/orchestrator/ ./internal/cli/ -run 'Decide|WakesTheAgent|Heartbeats|BreakerPauses' -count=1`
- **Depends on:** O8, O12, O11 (both regenerate `cli-reference.md`, so they are chained), and **Q4's gate, which it shares with O12** (slice 1 run
  14 days, Check 303's cost measured). **Level 6.**
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
  **Reviewer:** security. **OWNER-RUN, Check 304 (text written by O2):** the handoff on a real installation, two people.

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

Edges (a task depends on the tasks to its left): O2 <- O0; O3 <- O0; O6 <- O0, O4; O5 <- O0, O3, O2; O7 <- O3, O6; O12 <- O0, O1, O3, O4, O6; O8 <- O0, O2, O5, O7 (**O0 because the daemon's start-up runs `Migrate`**, T7);
O10 <- O5, O8 (**D1243's answer: the cache is O8's writer**); O17 <- O0, O3, O4; O9 <- O8 (**T7 moved `Migrate` and the binary check into O8, so
O9 no longer needs O0 or O3**); O13 <- O8, O12, O11; O18 <- O17; O11 <- O5, O8, O9. O16 and O19 are external-gated.

| Level | Tasks (can all run at once) |
| --- | --- |
| 0 | O0, O1, O4 |
| 1 | O2, O3, O6 |
| 2 | O5, O7, O12, O17 |
| 3 | O8, O18 |
| 4 | O9, O10 |
| 5 | O11 |
| 6 | O13 |
| gated | O16 (MCP M7, scopes amendment, T4), O19 (workspaces, T5) |

**Critical path:** O0 -> O3 -> O5 -> O8 -> O9 -> O11, six levels, unchanged by D1243 (O10's own path is O0 -> O3 -> O5 -> O8 -> O10, five). Slice 1
(O0-O11 minus O12/O13) completes at level 5; slice 2 adds O12 at level 2 and O13 at level 6 (after O11, because both regenerate `cli-reference.md`),
one level beyond slice 1 once approved **and once Q4's gate is met**; slice 3's `ee/` tasks run beside slice 1, its MCP task waits.

### Shared-files table (no level holds two writers of one file)

| File | Writers (level) | Same level? |
| --- | --- | --- |
| `internal/cli/orchestrator_cmd.go` | O5 (2), O8 (3), O9 (4), O11 (5) | no, one per level |
| `internal/cli/orchestrator_run.go` | O8 (3), O13 (6) | no |
| `internal/orchestrator/tick.go` | O7 (2), O13 (6) | no |
| `internal/cli/root.go` | O5 (2) | one writer |
| `docs/releases/vX.Y.Z.md` | O2 (1), O11 (5) | no |
| `docs/gcp-live-checklist.md` | O2 (1) only (Checks 300-305, NOT RUN); no other task edits it | one writer |
| `internal/budget/rules/rules_test.go`, `internal/budget/rules/testdata/rules.golden.json` | O2 (1) | one writer |
| `internal/orchestrator/runview.go` | O7 (2); O6 (1) defines the `RunView` type in `launch.go`, not there | one writer |
| `ee/register/orchestrator.go` and its test | O17 (2), O18 (3) | no |
| `plugin/plugin_test.go`, `plugin/readme_test.go` | O10 (4) | one writer |
| `internal/budget/rules/*` (other than the two rows above), `internal/infra/firebasedb.go`, `internal/cli/update_converge_test.go` | O2 (1) | one writer |
| `internal/projectpolicy/policy.go`, `internal/policy/merge.go`, `internal/localcfg/localcfg.go` | O4 (0) | one writer |
| `internal/orchestrator/export_test.go` | O0 (0); O3, O6, O7 each add an `export_<area>_test.go` of their own | one writer |
| `internal/cli/launch_budget.go` | O8 (3) | one writer |
| `schemas/*` | each schema has one writer (O0, O5, O12) | one writer each |
| `internal/orchestrator/state.go` and `schemas/orchestrator-state.schema.json` | O0 (0) only; O6 and O12 use `Intents` and `Counters` and edit neither | one writer |
| `website/content/cli-reference.md` (generated by `go run ./ee/clidoc`) | O5 (2), O8 (3), O9 (4), O11 (5), O13 (6) | no, one per level |
| `internal/cli/init_stages.go`, `internal/cli/init_orchestrator_stage.go` and `internal/cli/update_unit_stage.go` with their tests (the setup step Q2 moved the first install into, and the update-side re-render T7's ruling adds) | O9 (4) | one writer |
| `internal/cli/orchestrator_version.go` and its test (the binary-version check, T7) | O8 (3) | one writer |
| `internal/cli/doctor.go` and its orchestrator test (the out-of-date unit template) | O9 (4) | one writer |
| `internal/cli/update.go` | **no writer: the unit's re-render is a stage of `initStages`, which O9 registers (`update` already runs that list)**, so no task here edits `update.go` itself | — |
| `internal/orchestrator/cache.go` and its test (D1243's writer) | O8 (3); O5 (2) reads the cache file the daemon produces at runtime, never this source file | one writer |

**Re-checked for D1243 and the new level table:** every file above has one writer per level (level 3 is O8 and O18, level 4 is O9 and O10, and neither pair
shares a path), and O10's move from level 3 to level 4 takes the `plugin/` files with it, so no level gained a second writer.

**Re-checked for Q2's second answer:** the unit moved out of `update` and into O9, so `internal/cli/update.go` lost its only writer and the setup step
brought three files (`init_stages.go`, the new stage and its test) into O9, which is still the only writer of each; the level table and the critical path
are unchanged (O9 is level 4 either way, and O8's new dependence on O0 is on a level-0 task, so it stays at level 3), and the binary-version check added
`orchestrator_version.go` to O8's own level.

**Re-checked for the fix round:** the unit's drift report put `internal/cli/doctor.go` and its new orchestrator test in O9 (level 4), where nothing else
touches either; O8 gained no file (the cache and crash tests its Command names were already its own), and the retry and refusal rules changed no
interface, so **the edges, the level table, the critical path and the shared-files table are as they were**.

**Re-checked for T7's ruling:** the update-side step is **two files in O9** (`internal/cli/update_unit_stage.go` and its test) plus a registration in
`init_stages.go`, which was already O9's, so **no level gained a writer and `internal/cli/update.go` still has none** (level 4 is O9 and O10, and O10
writes only `plugin/` files); it needs no state, so **O9 <- O8 is unchanged**, and O9 stays at level 4 — the critical path (O0 -> O3 -> O5 -> O8 -> O9
-> O11) does not move. The one thing the ruling *removes* is the second-command escape hatch the table's rows used to lean on: what converged the unit
is now `update` itself.

## 5. Owner-run items, exactly

Checks 300 (O2: rules and `fugaro update` on a real installation, not `belong`), 301 (O9: the daemon under launchd and systemd across a reboot and
a second instance, **the upgrade half — a brew-style upgrade on a throwaway machine with the daemon running, which must notice the new
binary and hand over to it with no command typed — and T7's own half, a release that changes the unit's template converging under `fugaro update`
alone, with a second `update` writing nothing**), 302 (O11: the cutover on the owner's own installation **and, inside the 14 days, the manual rollback** — stop the daemon with
`fugaro orchestrator uninstall`, re-arm the sweep, the watcher and the queue scripts, and see one launch through them), 303 (O12: one real decision
call and the measured cost, **which is Q4's gate for slice 2 together with slice 1's 14 days**), 304 (O17:
a two-person handoff), 305 (O16: the MCP state group, when it exists). 306-309 are reserved. No agent performs any of them. **The manual rollback is
the one owner action here that has no command behind it** (T1, answered 2026-11-04): it is the three steps Check 302 names, run by hand. **After T7's
ruling (2026-11-05) no upgrade asks for a command at all**: a binary upgrade is the daemon's own hand-over, a template change is `fugaro update`'s
re-render, and `fugaro orchestrator install` is the explicit command for a machine that skipped `setup` or needs a repair — **`update` never installs a
unit, so Check 301's template half starts from a machine that has one**.

## 6. Owner questions: the answers, and nothing left open

**Q1-Q4 were all answered on 2026-11-04, and D1241-D1243 with them. Q2 was answered a second time on the same day, which closed T6, and the last
tension the answers left open, T7, was ruled on 2026-11-05** (reconciliation section 6): **the ruling is not the recommendation — `fugaro update`
re-renders an installed unit rather than leaving the person a second command**, so **this section puts nothing to the owner any more** and the plan
follows the ruling through O9, G11 and the conventions above.

- **Q1 (T1): answered.** No one-command rollback. The old scripts are **kept for 14 days** and the rollback is **manual and documented** — stop the
  daemon, re-arm the sweep, the watcher and the queue scripts (O11's how-to, Check 302's second half, section 5).
- **Q2: answered twice.** **The owner's answer, 2026-11-04 (a different answer from the one this PR first recorded, which is superseded):**
  **`fugaro setup` installs the daemon** — one of its listed steps, shown in its plan, at a real terminal only, refused under a coding agent's marker,
  the unit written by the person's own run and never by an agent — and in a **managed project the step follows the no-prompt rule**: no extra
  confirmation, because the plan line already names the unit and its path. **Upgrades move out of `fugaro update`** and into
  **`fugaro orchestrator install`**, which installs or re-renders and restarts the unit; **`fugaro update` never touches it** — **amended on
  2026-11-05 by the T7 ruling below.** The first answer
  (an offer inside `update`) is gone, and with it the collision that was **T6**: nothing now asks a question the no-prompt rule forbids.
- **T7: ruled 2026-11-05, and not the recommendation.** **`fugaro update` re-renders the daemon's unit when the rendered template changed**, so the HARD
  RULE is literally true and no release asks the person for a second command. The boundaries: `update` **only ever re-renders and reloads a unit that is
  already installed** and never installs one (a machine that skipped `setup` stays without a daemon until a person asks for one); the step sits behind
  the same plan-and-confirm rules as every other `update` step, takes the strict `Typed` class because a unit is code that runs at login, and writes
  only under the person's own home and service manager — never a cloud write, never root; `fugaro setup` still installs the first one (Q2) and `fugaro
  orchestrator install` stays as the explicit command (a machine that skipped `setup`, repair) that no release's `### For operators` needs; and the
  daemon's self-restart on a changed binary and its start-up `Migrate` stay as O8 has them (O9, G11).
- **Q3 (P2): answered.** The macOS OS notification, plus the owner's existing mobile push as the second channel; Linux falls back to the desktop
  notifier. **What the tree actually carries is U9** (reconciliation section 5): no push implementation exists in it, so the mobile half is UNVERIFIED
  against `main` and slice 1 ships the OS notification.
- **Q4: answered.** Slice 2 (O12, O13) is approved only after slice 1 has **run 14 days** and **Check 303 has measured the cycle cost**, which is also
  what turns D1228's proposed bounds (6 per hour, 40 per day, the breaker at 3) from proposals into numbers. The gate sits on O12 and on O13.
- **Open items (round 3): two of the three are now ruled.** (1) **D1242 is answered and gates nothing**: the terminal Typed confirmation stands, the
  paired-device channel comes later, and the later channel may reshape O0's `Approval` without any task or test here depending on that shape. (2) **The
  retry rule is answered, not the recommendation: the spec's "Task failed, first time: Code retries" row stands.** One approval covers the first attempt
  and at most one automatic retry of the same approved spec; a refusal is never retried, and it burns no attempt only when the `Launcher` refuses before
  the start; a second failure needs a person; the retry counts toward every
  cap; `spec.md` is not edited (G4, O0, O6; reconciliation section 3). (3) **Still reported, not decided:** an ambiguous launch error stays in flight up
  to `ClaimTTL` (10 minutes: `launchRun`'s contract, the runner's `ErrDuplicateExecution` is the second fence); a daemon's repeated "still in flight"
  ticks must not count toward the circuit breaker (O6, O7 tests).
- **Open items T7's ruling raises (reported, not decided here).** (1) **An `update` that cannot confirm the unit step writes no unit**: an agent-marker
  run, a run with no terminal, or a managed-project gate that declines the step leaves an out-of-date unit behind, and the person has to run `fugaro
  update` again where they can answer. The ruling keeps the managed gate exactly as it is (no exemption for this step), so the plan builds that; what is
  **reported rather than decided** is whether that consequence is the right one for a managed project, where Q2 did give the setup step its no-prompt
  reading. (2) **A hand-edited unit is reverted**: the
  comparison is the rendered template against the file on disk, so a person who edited their plist or unit by hand sees `update` overwrite it (which is
  what `fugaro orchestrator install` already does). Reported rather than decided: nothing in either ruling says whether such an edit should be preserved,
  and a second stamp would be needed to tell it from an old template (O9, section 3).
- **D1241, D1242, D1243** (the round-1 security review's questions, now answered in the reconciliation's decision table and its section 6): D1241 the
  person's own credential writes the node, so **O2's gate is lifted**; D1242 the terminal Typed confirmation now and a paired-device channel later, so
  **nothing is gated**; D1243 the session-start print reads a local cache, so **O8 gains the cache writer and O10 depends on O8 (level 4)**.
  D1244-D1249 stay reserved.

## 7. UNVERIFIED added by this plan

- **P1. Resolved as negative.** `initflow`'s agent-session detection is not reliable as a fence (its own text: "a mitigation, not a barrier"); G2's CLI
  half rests on a Typed confirmation at a real terminal, with the residual in D1242.
- **P2. Resolved as to the choice.** Q3 was answered on 2026-11-04: the macOS OS notification, plus the owner's existing mobile push, with the desktop
  notifier on Linux. What stays open is **U9** (reconciliation section 5): no push implementation is in the tree, so the mobile half could not be
  verified against `main`.
- **P3.** Whether a new top-level RTDB subtree needs a `RulesVersion` bump or a marker of its own: this plan recommends the marker (the credentials cascade
  precedent) and O2's red step confirms it against `internal/projectpolicy/version.go`.
- **P4.** Everything reconciliation U1-U8 carries stays open; in particular the cost of a cycle (U4) is measured only by Check 303.
