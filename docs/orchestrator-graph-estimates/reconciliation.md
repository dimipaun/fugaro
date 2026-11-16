# Reconciliation: orchestrator, graphs and estimates

Checked against `origin/main` at `c6a04b0eb` on 2026-11-16. This document amends [`spec.md`](spec.md); **where the two disagree, this one wins.** Decisions D2700–D2739 are this folder's block (D2700–D2734 used, the rest reserved); Checks 700–709 likewise (700–703 used).

The spec bundles five capabilities. The capability map below was approved by the owner on 2026-11-16 before this document was written; every finding, decision and task names a module id.

| Module id | Spec section | Responsibility | Needs |
| --- | --- | --- | --- |
| `daemon-release` | §5 | `fugaro update` turns the local orchestrator daemon on, the daemon takes over work in flight, a project-wide switch turns it off, the skill teaches the agent to hand work to it | — |
| `graph-import` | §3 | the daemon runs plan graphs: a deterministic import of `implementation-plan.graph.json`, unlock on merge, one approval per plan, the graph written once by the session when none exists | — |
| `run-events` | §4 | the daemon wakes the moment a run ends, notices a run that vanished, and folds dependencies a run reports | `graph-import` (only for dependency reports and conflicts) |
| `estimates` | §1 | ready-by and cost ranges for a graph, from history per tier and rigor, recalibrated as tasks end | `graph-import` (only for the orchestrator brief) |
| `backlog` | §2 | the standing per-project backlog, casual adds, the GitHub issues adapter, mirroring, close on merge | `graph-import`, `estimates` |

Build order: `daemon-release` and `graph-import` side by side, then `run-events` and `estimates`, then `backlog`.

## Assumptions

- **A1.** "The orchestrator" in every section means the stateless orchestrator daemon of [`docs/stateless-orchestrator/`](../stateless-orchestrator/README.md) (`internal/orchestrator`, `fugaro orchestrator run`), not the superseded central orchestrator or `ee/coordinator`.
- **A2.** "Locally or in the cloud" (§5) describes where the daemon may run. Only the local daemon exists. The always-on cloud service is a separate, design-only document ([`docs/mobile/design/always-on-orchestrator.md`](../mobile/design/always-on-orchestrator.md)) and is not built by this plan (D2731).
- **A3.** "Project" is Fugaro's project: one GCP installation, many repositories, many people.
- **A4.** "Rigor level" (§1) is the rigor mode of `internal/rigor` (`poc`, `startup`, `pro`, `critical`, `hotfix`), recorded on every run as `runstore.Record.Rigor`.
- **A5.** "Size small / medium / large" (§1) is the expectation tier of `internal/tier` (`trivial`, `small`, `medium`, `large`), which the run record already carries as `Record.Tier` (D2711).
- **A6.** "How many jobs can run in parallel" (§1) is the daemon's running cap: `orchestrator.max_running`, which defaults to the person's `max_parallel` (D1239).
- **A7.** "Dashboard status" (§2) is what `fugaro graph`, the console's graph screen and `fugaro orchestrator state` show.
- **A8.** Jira (§2) is a named example of an adapter, not a commitment of this plan (D2718).

## What the repository already has

| Spec concept | What the repository has |
| --- | --- |
| Explicit JSON dependency graph (§3) | `internal/plan` `Document`, schema 3 (`SchemaVersion = 3`), read and analysed by `fugaro plan graph` and `fugaro plan status`; written by the `spec-to-pr` skill beside every plan |
| Task size (§1) | `plan.Node.Size`: a relative numeric weight (`*float64`, default 1) used only by the decoupling gain (D1816). Separately, `internal/tier`'s four tiers, `fugaro run --tier`, `runstore.Record.Tier` |
| Historical medians (§1) | `internal/stats` (`fugaro stats runs`): median and p90 of duration and cost **per recipe**, hidden under `MinRuns = 20` (D96, D97). `ee/governance` `Calibrate`: per-tier medians of actual vs expected |
| Seed defaults (§1) | `ee/governance` `Defaults()`: per-tier expected cost **and** minutes (trivial $0.25 / 10 min, small $1 / 30, medium $3 / 90, large $8 / 240) |
| Critical path (§1) | `plan.Analyze`: `CriticalPath` counted in tasks, unweighted; the only weighted path is the decoupling `Gain` |
| Parallel cap (§1) | `orchestrator.max_running` (`policy.Orchestrator.MaxRunning`, tighten-only), defaulting to `max_parallel` (`localcfg.DefaultMaxParallel = 20`) |
| Standing backlog (§2) | Orchestrator `WorkItem`s in `orchestrator/<project>/state`: a flat list with `DependsOn`, an optional `Spec`, phases. Separately `docs/backlog/` Markdown items (`internal/backlog`, `fugaro backlog add\|index\|list`, D26–D32) |
| Issue adapters (§2) | None. `internal/issues` is Fugaro's own product-bug tracker (filed to Fugaro's repository), not the user's project tickets |
| Deterministic unlocking (§3) | `orchestrator.dependenciesDone` gates a launch on every dependency's `Phase.Done()`; level-triggered tick (D1229) |
| RTDB listening (§4) | `rtdb.Client.Stream` (SSE, reconnect with a full snapshot); the daemon's `wait` streams its own state node; `fugaro watch` streams `agents/` |
| onDisconnect (§4) | None; cannot exist on a REST client. The run's 15 s heartbeat on `agents/<slug>/<run>` and the `budget` `Sweeper` (stale after 5 min, writes `infra_error`) stand in for it |
| Idempotent unlock (watch-outs) | Per-project RTDB lease (90 s TTL), ETag compare-and-swap on the state node (`PutIfMatch`), intent first (D1231), the GCS launch claim |
| Orchestrator skill (§5) | `plugin/skills/orchestrator` (O10): reading and patching state |
| Kill switch (§5) | None in the spec's sense. `orchestrator.Caps.KillSwitch` is the budget's global spend stop |

## Findings

### daemon-release

- **F1 (Wrong).** "The next Fugaro update enables it automatically." Today `fugaro update` never installs the unit: `updateUnitStage` (`internal/cli/update_unit_stage.go`) re-renders one that is already there ("It never installs a unit"), and `fugaro setup` installs it only at a real terminal (`internal/cli/init_orchestrator_stage.go`). *Consequence:* `update` learns to install the unit (D2700).
- **F2 (Exists).** One daemon per project is already enforced: `orchestratorID` (`internal/cli/orchestrator_cmd.go`) is the project's name, and the lease (`internal/orchestrator/lease.go`, `LeaseTTL = 90s`) is per id. *Consequence:* installing the unit on every machine gives one live daemon and standbys, with no new coordination (D2700).
- **F3 (Gap).** No switch turns the daemon off. `Caps.KillSwitch` (`internal/orchestrator/caps.go`) is filled from `budget.PathKillGlobal` and halts every launch; it does not hand orchestration back. D1235 made rollback manual (`fugaro orchestrator uninstall`). *Consequence:* a new project-policy key (D2701).
- **F4 (Gap).** Nothing takes over work in flight. The daemon tracks only the items it minted run ids for (D1231), and `applyUpsert` (`internal/orchestrator/patch.go`) refuses `run_id`, phase and intent from any class but the daemon, so `fugaro orchestrator import` brings items in queued and unapproved. *Consequence:* adoption is a daemon-class step at start-up (D2702, D2721).
- **F5 (Rule).** [`docs/how-to/orchestrator-cutover.md`](../how-to/orchestrator-cutover.md) asks each installation to import a session dump, approve items, stop the sweep, the Monitor watcher and the queue scripts by hand, and test the caps. This breaks `CLAUDE.md`'s hard rule that `fugaro update` is the whole upgrade. The doc is pinned by `internal/cli/docs_orchestrator_cutover_test.go`. *Consequence:* the how-to shrinks to "run `fugaro update`" (D2729).
- **F6 (Gap).** No skill tells the agent to stop driving runs itself. `plugin/skills/orchestrator/SKILL.md` covers reading and patching state; `working`, `parallelism` and `routing` never mention the daemon. The old behaviour (the 30-minute sweep, the Monitor watcher, `queue.txt`) is not Fugaro code: it lives in the operator's session. *Consequence:* D2703.
- **F7 (Exists).** The daemon migrates its own state at start-up (`orchestrator.Migrate`, as forge-labels T8 did), so a state-version move ships inside `update` without an operator step.

### graph-import

- **F8 (Wrong).** "The orchestrator reads that graph deterministically." It does not: no file in `internal/orchestrator` or `internal/cli/orchestrator_*.go` imports `internal/plan`, and D2438 records "the daemon has no plan import". Work items come only from patches. *Consequence:* a plan import (D2704).
- **F9 (Wrong).** "When a task merges, it unlocks the tasks that depend on it." `foldPhase` (`internal/orchestrator/runview.go`) sets `PhaseDone` when a run **succeeds**, so dependents start before the merge. *Consequence:* D2705. The daemon has no pull-request read today, but the credentials service's label route (`internal/credsvc/labelroute.go`, forge-labels D2439) already reads a pull request's state for the daemon; the merge read reuses that route.
- **F10 (Rule).** Every item needs a person's approval (D1238, the orchestrator skill), and the spec-to-pr rule says a plan approves nothing. A 20-node graph would need 20 approvals. *Consequence:* one person-only approval per plan, digest-pinned per node (D2706).
- **F11 (Gap).** No LLM fallback graph exists. D1221 makes the daemon deterministic. *Consequence:* the session agent writes the graph (D2707).
- **F32 (Exists, found while planning).** The state already has a `review` phase (`orchestrator.PhaseReview`, "the item's PR is under review" in the stateless-orchestrator spec's status list), with the transitions `running → review → done | failed` already allowed (`internal/orchestrator/state.go`), and no non-test code sets it. *Consequence:* the merge wait of D2705 uses `review` rather than a new `merging` phase, so `Phases` stays closed at seven and the phase vocabulary needs no version move (D2705, amended).
- **F33 (Gap, found while planning).** The daemon has no checkout, so it cannot read a plan graph itself; only a session in a checkout can run D2704's import. *Consequence:* D2702, amended.
- **F12 (Exists).** `WorkItem.RigorTier` is passed to the launcher as `--tier` (`internal/cli/orchestrator_launcher.go`), i.e. it is the expectation tier, not the rigor mode. *Consequence:* the import fills it from the node's new `tier` (D2711, D2730); its misleading name is left alone (renaming a wire field needs a version bump).

### run-events

- **F13 (Wrong).** "Current: a periodic polling sweep (around every 30 min)." The 30-minute sweep is the owner's own agent loop (stateless-orchestrator spec, "What this replaces"; F18 there). The daemon already polls every 30 s (`--interval`, capped at `LeaseTTL`), and `orchestratorDaemon.wait` already streams over SSE, but only its own `orchestrator/<id>/state` node. A run that ends writes nothing there, so the daemon notices on its next poll. *Consequence:* stream the run-side paths too (D2708). The gap closes from up to 30 s to seconds, not from 30 min.
- **F14 (Exists).** "Chosen over Pub/Sub/Eventarc": already on record. Stateless-orchestrator F8 and the header of `internal/cli/orchestrator_run.go` say SSE wakes a local daemon and no Eventarc is wanted; `deploy/terraform` provisions no Pub/Sub or Eventarc.
- **F15 (Wrong).** "onDisconnect clears its entry." `onDisconnect` needs the Realtime Database's websocket protocol. `internal/rtdb` is REST plus SSE (the Admin SDK cannot be used: a run holds only its own ID token), and no Go code uses onDisconnect. What exists: `Session.heartbeat` rewrites `agents/<slug>/<run>` every 15 s, `Session.Finish` writes `outcomes/<day>/<slug>/<run>` once then deletes the entry, and the `budget` `Sweeper` removes an entry stale for 5 min with no live execution and writes `infra_error`. *Consequence:* D2725.
- **F16 (Wrong).** "Ideally inside an RTDB transaction." The REST client has no transaction API. Its compare-and-swap is `GetETag` + `PutIfMatch` on one location, which the state node's `Node.Save` already uses under the lease. *Consequence:* D2726.
- **F17 (Gap).** A run cannot report dependencies: run tokens are refused under `orchestrator/` (D1241, rules `"orchestrator": {".write": false}`), and patches come only from the person, session, daemon and decision-agent classes. *Consequence:* D2709.
- **F18 (Gap).** Nothing links a conflict to the graph. `runstore` statuses (`succeeded`, `failed`, `halted`, …) carry no "conflict with item X". *Consequence:* D2710, which needs the run to name the conflicting paths in its record.
- **F19 (Gap).** Two orchestrators with **different** ids over the same items are uncoordinated, and the cutover doc warns that scripts don't take the launch claim. *Consequence:* with one id per project (F2) and the agent no longer launching while the daemon is live (D2703), the remaining racer is a person's manual `fugaro run`, which the GCS launch claim and the `--plan-task` duplicate refusal already cover.

### estimates

- **F20 (Gap).** No task or plan estimate exists anywhere. `fugaro estimate` reprices one run's tokens on another model; `internal/stats` groups by recipe only and has no tier, rigor or plan field. *Consequence:* a new computation in `internal/plan` with a history reader (D2713, D2722, D2723).
- **F21 (Wrong).** "The planner tags each task with a rough size (small / medium / large)." `plan.Node.Size` is a numeric weight (0.5–4 in the eight committed graphs) used by the decoupling gain. *Consequence:* a separate `tier` field (D2711).
- **F22 (Rule).** "No cost defaults" conflicts with `ee/governance` `Defaults()`, which seeds expected cost per tier. Those are governance expectations, not estimates, and core code cannot import `ee/` (`.claude/rules/ee.md`). *Consequence:* D2712, D2724.
- **F23 (Rule).** `internal/stats` hides groups under 20 runs (D96, D97). Medians per tier × rigor make sparse groups. *Consequence:* D2713, which keeps D96 for `stats` and sets the estimate's own threshold.
- **F24 (Gap).** The run record has the tier and the rigor but not the plan task: `plan_task` is only in `launch.json` (`runstore.Launch.PlanTask`). History needs neither (it groups by tier and rigor), and recalibration joins runs to nodes the way `plan status` already does (`RunFact.PlanTask`). No record change.
- **F25 (Gap).** `CriticalPath` counts tasks; nothing weights it by duration (console-dependency-graph F18: "the longest chain by count until a weight exists"). *Consequence:* the estimate supplies that weight (D2722).

### backlog

- **F26 (Exists).** A spec-less work item is already legal, with `DependsOn`, phases, approval, the pipeline and `fugaro graph`'s `orchestrator:<id>` source. *Consequence:* the standing backlog is those items (D2715).
- **F27 (Gap).** No external id exists on `plan.Node`, `WorkItem` or a backlog front matter. *Consequence:* `WorkItem` gains an optional `external` field (D2728).
- **F28 (Gap).** No issue-tracker code exists: `gitprov.Provider` has PR methods only, no issue read or close; no Jira anywhere. *Consequence:* a GitHub adapter through the credentials service (D2718).
- **F29 (Rule).** `docs/backlog/` (D26–D32) is a second backlog with its own close flow. *Consequence:* folded in and retired (D2716).
- **F30 (Rule).** "Telling the agent 'let's fix this' adds a node straight away": adding is a session upsert and is allowed; approving is person-only. *Consequence:* D2717.
- **F31 (Gap).** No skill says what to do on "let's fix this"; `working`'s `fugaro issue` family is Fugaro's own product bugs, and an agent could confuse the two. *Consequence:* the orchestrator skill says it (R5).

## Decisions

Every answer was given by the owner on 2026-11-16 through `AskUserQuestion`. "Resolution" marks a point with one defensible answer, stated rather than asked.

| # | Module | Question | Answer (2026-11-16) |
| --- | --- | --- | --- |
| D2700 | daemon-release | How does `update` turn the daemon on? | **Install everywhere.** `update` installs the login unit on any machine without one, with no prompt (the no-prompt policy needs none: nothing is asked). The per-project lease keeps one daemon live and the others on standby (F2). In a coding agent's session `update` writes no unit, as T8 already rules for re-rendering; the owner's next terminal `update` installs it. Why: the spec's "operators do nothing". |
| D2701 | daemon-release | The fallback switch | **Project policy key** `orchestrator.enabled` (default true, tighten-only: any layer can turn it off). The daemon reads it every tick and launches nothing while it is off; the skill reads it (`fugaro settings orchestrator.enabled --json`) to know the agent drives runs itself again. Why: one write reaches every machine. |
| D2702 | daemon-release | Takeover on upgrade | **The daemon adopts at start.** A daemon-class step adopts every run in flight (launch record and run record) as a `running` item with that run id, keyed on `plan_task` where present, and imports the plans those runs name through D2704. A run with no `plan_task` becomes a spec-less item. Why: it keeps the patch rule (only the daemon sets run ids). *Amended while planning, 2026-11-16 (F33):* the daemon cannot read a graph (it has no checkout), so for each plan an adopted run names it records a fact naming the plan; the orchestrator skill has the session run `import --plan` for it at its next start (R5), and the import keys the adopted item by the same `<plan>/<task>`, so the two meet rather than duplicate. Adoption itself is unchanged. |
| D2703 | daemon-release | Teaching the agent | **Extend `plugin/skills/orchestrator`.** While the daemon is live and enabled, the agent queues work as items and never launches, sweeps or watches runs itself; while it is disabled or absent, it drives runs as before. `working` and `parallelism` point to it. |
| D2704 | graph-import | How a graph reaches the daemon | **A CLI import.** `fugaro orchestrator import --plan <folder>` reads the graph with `internal/plan` and writes one typed patch: an upsert per task node keyed `<plan>/<task>` (re-import is idempotent and updates only nodes not yet launched), edges as `depends_on`, `spec` set to the plan folder, contracts and integrations kept as ordinary items. The session runs it after the plan's pull request merges. |
| D2705 | graph-import | What unlocks a dependent | **The pull request merging.** *(Amended while planning, 2026-11-16, F32: the existing `review` phase, not a new `merging` one.)* The `review` phase sits between a run succeeding and `done`: an item whose run left a pull request is `done` once that pull request has merged, read in bulk once per tick per repository through the credentials service's label route (F9). A run that succeeds with no pull request is `done` at once. A pull request closed unmerged makes the item `failed`. |
| D2706 | graph-import | Approving an imported graph | **One approval per plan.** A person-only `approve` patch op that names a plan approves every node with that `spec`, each pinned to its digest (a node changed by a re-import needs approving again). The daemon then launches as dependencies clear, inside `max_running` and the spend caps. |
| D2707 | graph-import | Who writes a missing graph | **The session agent**, once, before queueing: a schema-3 graph in the scratchpad, checked with `fugaro plan graph`, imported via D2704. The daemon never calls a model for a graph. |
| D2708 | run-events | What the daemon listens to | **`agents/<slug>` and today's `outcomes/<day>`, as well as its own node.** Any change wakes the level-triggered tick at once. A reconnect's opening snapshot is the "sweep on reconnect"; the 30 s poll stays as the fallback ceiling. The always-on cloud service keeps its scheduled tick (D2731). |
| D2709 | run-events | How a run reports a dependency | **Through its `result.json`, add-only.** The runner records `discovered_dependencies` (a plan task id or a footprint path, and a reason). When the run ends, the daemon adds an edge automatically only where it blocks an item not yet launched, records it in the audit, and never removes one. No new RTDB rules. |
| D2710 | run-events | A run that ends in a conflict | **Add the edge and a follow-up.** The run names the conflicting paths in its record. The daemon records a `graph_suspect` fact naming both items, adds the edge from the conflicting item to the merged one (add-only, as D2709), and lets the existing follow-up and escalation ladder re-run it. |
| D2711 | estimates | How a node carries its size | **A new optional `tier` field** with `internal/tier`'s four values, passed to the run as `--tier`. The numeric `size` stays the decoupling weight. Additive to schema 3, no version bump. |
| D2712 | estimates | Seeds | **Minutes only, no cost.** Time seeds are 10 / 30 / 90 / 240 minutes per tier, widened to ×0.5–×2. Cost reads "no history yet" until history exists, even where a repository configured `expected_usd`. Governance defaults are untouched. |
| D2713 | estimates | History threshold and range | **≥ 5 runs, p25–p75.** The (tier, rigor) group at ≥ 5 runs, else the tier alone at ≥ 5, else the seed; the range is p25–p75, widened 1.5× under 20 runs. D96's 20 stays for `fugaro stats`. |
| D2714 | estimates | Where estimates appear | **All four:** `fugaro plan graph` and `plan status` (text, plus an additive `estimate` block in `--json`), the spec-to-pr skill's plan and pull-request body, the console's graph screen, and `fugaro orchestrator state --brief` per imported plan. |
| D2715 | backlog | Home of the standing backlog | **Orchestrator items.** The backlog is the project's work items with no `spec`. `fugaro graph --json` exports them as nodes and edges, so the shape on the wire is the graph's. |
| D2716 | backlog | `docs/backlog/` | **Fold in and retire.** `fugaro backlog add` writes a work item; `fugaro update`, run in a checkout, imports the open Markdown items once (keyed on their `id`, so a second update imports nothing); `docs/backlog/` stays as history. D26–D32 are superseded. |
| D2717 | backlog | A casual "let's fix this" | **Queued, unapproved.** The agent adds the node at once; it runs once the person approves it (one `approve`, or a later backlog approval). |
| D2718 | backlog | Adapter scope | **GitHub now, Jira later.** A GitHub issues adapter through the credentials service (no token leaves it, as D2439). The adapter interface is a contract, so a Jira adapter can be added later; Jira is declined here and needs its own spec. |
| D2719 | backlog | Mirroring | **Separate opt-in**: `mirror: false` by default on the adapter's config. **Departs from the spec** ("when configured, mirrored"), on the recommendation. With `mirror: true`, a new internal item is opened as an issue carrying a marker with the item id, and the issue's id is written back as the item's external id. |
| D2720 | backlog | Which issues come in | **All open issues** (**not the recommendation**, which was labelled issues only). Each comes in as a queued, unapproved item keyed on its external id; approval is the filter (D2717). Closing the issue archives an item that has not launched. |
| D2721 | daemon-release × graph-import | Remaining nodes of a plan adopted in flight | **Unapproved, one approval.** Adopted runs keep running; the plan's other nodes arrive queued, and the daemon records a fact asking for the plan's approval (D2706). |
| D2722 | estimates | Time formula (resolution) | Remaining time = max(the critical path weighted by each open task's duration estimate, the sum of open durations ÷ `max_running`). A running task counts max(0, estimate − elapsed); a done task counts nothing. The range is that formula over the p25 and p75 durations. |
| D2723 | estimates | Cost formula (resolution) | Cost = the actual cost of done and running tasks + the sum of per-tier median costs (p25–p75 for the range) of the tasks not started. A task without a cost history makes the total "no history yet for N tasks" instead of a partial figure shown as complete. |
| D2724 | estimates | Where the seeds live (resolution) | Core constants in `internal/tier`, not imported from `ee/governance` (core cannot import `ee/`); the values match the governance minutes, so the two agree today without a link. |
| D2725 | run-events | The onDisconnect substitute (resolution) | An `agents/<slug>/<run>` entry that disappears with no matching `outcomes/` entry wakes the tick, and the tick checks the execution at once through the existing `executionOver` path. The `budget` `Sweeper`'s 5-minute stale rule stays as the backstop. |
| D2726 | run-events | Idempotent unlocking (resolution) | No transaction API is added. The tick is level-triggered (D1229), the state node is saved by ETag compare-and-swap under the per-project lease, and intents are written first (D1231); an event and a poll that see the same finished run make the same decision and the second save is a no-op. A test pins it (E4). |
| D2727 | backlog | Closing the ticket (resolution) | A run for an item with a GitHub external id writes `Fixes #N` in its pull request body; GitHub closes the issue when the pull request merges into the default branch. No daemon write. |
| D2728 | backlog | Dedupe key (resolution) | `WorkItem.external` = `github:<owner>/<repo>#<n>`, unique across a project's items. The ingest skips an issue whose key is already an item's, or whose body carries the mirror marker. This closes the mirroring loop. |
| D2729 | daemon-release | The cutover how-to (resolution, rule-forced by F5) | `docs/how-to/orchestrator-cutover.md` becomes "run `fugaro update`" plus what it does and the switch of D2701; the session-dump import and the manual script steps go. The owner's private scripts are stopped by the agent that runs them, per D2703. |
| D2730 | graph-import | Tier on the item (resolution) | The import fills `WorkItem.RigorTier` (the expectation tier, F12) from the node's `tier`, so the run records the tier and history accrues per tier with no record change (F24). |
| D2731 | run-events | The always-on cloud service (resolution) | Out of scope: it ticks on Cloud Scheduler by its own design (min-instances 0) and is not built. |
| D2732 | daemon-release | Checks (resolution) | Check 700: `update` installs the unit on a sandbox machine, a second machine stands by, and the switch turns both off. Check 701: a daemon started over a run in flight adopts it and unlocks its dependent on merge. Check 702: wake latency from a run's end to its dependent's launch. Check 703: the GitHub adapter, in and out, on the sandbox repository. All owner-run. |
| D2733 | backlog | Adapter config home (resolution) | The adapter is configured in the project layer (`internal/localcfg` / `internal/projectpolicy`), not in `fugaro.yaml`: the backlog is per project, and `fugaro.yaml` is repository behaviour only. |
| D2734 | graph-import | The plan graph's JSON Schema (resolution) | `schemas/` has none today; the additive `tier` field (D2711) is documented in the `parallelism` skill's schema example and validated by `plan.validateSchema3`, as `size` is. No schema file is added. |

### Answers to the spec's own open questions

- §5 "how to teach the agent to work with it, probably through a skill": through the existing orchestrator skill (D2703).
- §5 release: D2700, D2701, D2702, D2721, D2729.

## Open tensions

None. D2720 (all issues) and D2719 (mirroring off by default) pull the same way, as does D2717 (approval is the filter). D2720 with D2714 means a large backlog's estimate covers every open issue; the brief shows the backlog's estimate apart from each plan's.

## Open items

- **O1.** The per-tick merge read (D2705) reuses the credentials service's label route; whether that route's request shape already answers "merged" or needs a field is settled by task G3.
- **O2.** The always-on cloud service (A2, D2731) does not get event wake-ups; when it is built, its tick cadence (≥ 1 min) bounds its latency.
- **O3.** Jira (D2718) needs its own spec: a new credential type and its storage.

## UNVERIFIED

- **U1.** Whether three long-lived SSE connections per daemon, across every teammate's standby daemon (D2700), stay inside the Realtime Database plan's concurrent-connection limit for a large project. Settled by the RTDB plan limits page and Check 702; the owner can confirm.
- **U2.** Whether launchd and systemd user units install cleanly when `update` runs with no login session (over SSH). Settled by Check 700 on a headless Linux machine.
- **U3.** Whether the GitHub App the credentials service mints from already holds `issues: write` on the installation's repositories. Settled by reading the App's permissions (owner) before B3 is built; if not, granting it is an owner step that `update` cannot take, and the plan says so.
- **U4.** Whether GitHub closes a `Fixes #N` issue when the merge is a squash by the daemon's credential rather than a person. GitHub documents the keyword for merges into the default branch; Check 703 settles it.
