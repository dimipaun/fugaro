# The stateless orchestrator: the spec against the code

**Status: reconciliation (step 2 of spec-to-pr). Nothing implemented, nothing approved, no plan yet.**
Checked against `origin/main` at `5998d67c7` (`feat(config): the workflow_secrets block ... (W0) (#1710)`), merged into this branch,
on **2026-11-04**. The open pull requests were read with `gh pr list` and `gh pr diff` the same day. Every path and symbol below was
re-read at that commit; a citation by symbol is held where a line would move.

[spec.md](spec.md) is the owner's spec verbatim (the base spec plus its "Orchestrator daemon" addendum). It needed no typo fixes; it was
only renamed from `fugaro-stateless-orchestrator-spec.md`. Two of its own cross-references are to documents that are not in this repo under
those names ("fugaro-coordinator-spec", "fugaro-mcp-server-spec"); they are `docs/coordinator/spec.md` and `docs/mcp-server/spec.md`.

## 0. The number block

The block this folder uses is **D1220-D1249**, with D1250-D1259 held in reserve. Reading:

- `git grep` over `origin/main` for `D13[1-9][0-9]` finds nothing, and `D1310` is the highest number any open pull request writes (#1711,
  workspaces). `D1320-D1349`, the block the brief named, is therefore free.
- **But `D1220-D1259` is already named for this work** on `origin/main` in six places: `docs/mcp-server/README.md` (the census table),
  `docs/mcp-server/reconciliation.md`, `docs/workspaces/README.md`, `docs/plugin-user-scope/README.md`, `docs/binary-allow-rule/README.md`
  and `docs/periodic-reviews/README.md`. No file on `main` writes a decision in that range, and the only open pull request touching it,
  #1712, writes `D1260`/`D1261` (workflow secrets), i.e. above it. Taking `D1320` would leave 40 numbers permanently unused and make six
  census tables wrong. **This design takes D1220-D1249** and says so; the coordinator should confirm before the owner is shown the numbers.

## 1. Headline table: spec concept to what the repo already has

| Spec concept | What the repo has | Class |
| --- | --- | --- |
| State in **Firestore** (agent doc, task docs, events, transactions, TTL, listeners) | Firestore exists but is **deny-all**, holds only `spendDaily` history, and its REST client has **no transactions**. Live state, CAS, streaming and the whole Watch feed are in the **Realtime Database** (F1-F4) | Wrong / Gap |
| Single-writer via Firestore transactions | `rtdb.GetETag` + `PutIfMatch` (HTTP if-match) is the repo's CAS, in production in the kill switch and the mailbox lease (F5) | Exists (other store) |
| Append-only event log with ULID ids and a cursor | None as a log. Run progress is level state: `/agents/<slug>/<run>` heartbeat entries in RTDB plus `result.json`, `launch.json` and the claim in the runs bucket (F6) | Gap |
| Worker heartbeats and "started/progress/completed/failed" events | Runs already write a heartbeat registry entry under their own run-scoped RTDB identity (`budget/registry.go`, rules `RUN` macro) (F7) | Exists |
| Task-scoped token for a job, issued by the credentials service | The run's RTDB write grant is already scoped to its own `$slug/$run`; the credentials service ships dormant and is about model and git credentials (F7) | Exists (different mechanism) |
| Eventarc / Pub-Sub / Firestore trigger to wake the loop | None: no Eventarc or Pub/Sub API in any Terraform module; Cloud Run jobs run on Scheduler or an API call (F8) | Gap |
| Fugaro MCP server with a state tool group | Hosted MCP server is **designed** (`docs/mcp-server`, M1-M10), six tools, **not on `main`**: no `mcpauth` code at `5998d67c7` (F9, F10) | Gap |
| `spawn_task` as "the single launch path" with caps | Decided already as D931: one pre-launch path shared with `fugaro run` (claim + `budget.Precheck`) (F11) | Exists (design) |
| Spawn guardrails: spend, concurrency, rate, idempotency, kill switch | Spend and kill switch: `budget.Precheck`, `budget.SetKill`, enforced by the DB rules. Idempotency: the launch claim. Per-person `max_parallel`. No per-project running cap, no spawn-rate cap, no batch cap (F12) | Exists / Gap |
| Coordinator writes Firestore directly; deterministic code + LLM only for judgement | The coordinator exists as `ee/coordinator` with its **RTDB** `coordinator/` subtree (D195) and a sweep; P7/P8 (the resident service) are not started (F13) | Exists (other store) |
| Mirror task events into RTDB for Watch | Watch already reads RTDB directly (`watch.Start` over four streams); there is nothing to mirror if state is in RTDB (F3) | Wrong premise |
| Decision agents, model pinned per decision type | Reviewer routing by changed path is an owner rule (security-sensitive paths always Opus); the cloud senior reviewer design (`docs/cloud-senior-review`) is the nearest mechanism (F15) | Rule / Exists (design) |
| Local daemon under launchd | No daemon, launchd unit, systemd unit or service installer in the CLI (git grep of `launchd` finds only console and docs text) (F16) | Gap |
| Session-start hook that injects state | The plugin has no `SessionStart` hook (`git grep SessionStart -- plugin internal/claudeplugin` is empty); one is designed in `docs/plugin-user-scope` (F17) | Gap |
| Replaces "cron sweep + monitors", launcher agents | Those scripts are in the owner's scratchpad and memory, **not in the repo** (F18) | UNVERIFIED |
| Supersedes the coordinator spec's auto-compact | The coordinator spec text says so (`docs/coordinator/spec.md` section 9); the central-orchestrator spec says the same under "Session memory" (F19) | Open |

## 2. Findings

### Store and transactions

**F1. Wrong. "State lives in Firestore" is not what the installation has been built around.** The installation's live store is the Realtime
Database (`internal/budget/model.go` package doc; node paths in the same file). Firestore is a second store whose database is created by
`fugaro update`'s REST step, not Terraform (`deploy/terraform/terraform_test.go:1194` `TestNoFirestoreDatabaseResource` forbids a
`google_firestore_*` resource); `internal/cli/init_firebase.go:638` `firestoreStep` creates it in `us-east5` (permanent, never
changeable: the confirmation text asserted in `internal/cli/init_firestore_test.go:56`), with **deny-all rules**
(`init_firestore_test.go:43` `denyAllReformatted`) and a `meta/installation` mark. `docs/coordinator/reconciliation.md` #6 and #7 already
corrected the same assumption for the coordinator spec, and `docs/central-orchestrator/spec.md` ("Session memory") repeats it.
*Consequence:* choosing Firestore means a second store gains a writer, a rules change (today deny-all, and the shared safety property that
no client can read it), an IAM grant, a Firestore client with transactions and a fake. Choosing RTDB reuses what exists (D1220).

**F2. Gap. The Firestore client cannot do what the spec leans on.** `internal/firestore/client.go` package doc lists `Get`, `Patch` (an
upsert with an updateMask), `Delete`, a one-field range `Query`, `GetDatabase` and `CreateDatabase`; one attempt per call, 30 s bound, no
retry, **no transaction, no listener, no batch commit, no multi-document write**. Preconditions it does have are per-document
(`MustNotExist`, `IfUpdateTime`, used at `internal/budget/rollover.go:511` and `:526`). The spec's step 8 ("one transaction: patch, action
ids, cursor") is multi-document in its own storage layout (tasks and decisions are subcollection docs), so it cannot be built on this
client without new REST work (UNVERIFIED that the REST `documents:commit` with preconditions would suffice: see U1).

**F3. Wrong premise. There is nothing to mirror for Watch.** The spec says Watch "stays on Realtime Database for now; the coordinator
mirrors task events into it". `internal/watch/source.go` `Start` supervises four streams of the project's RTDB and
`internal/watch/json.go` defines `fugaro watch --json`; runs already write their own state to `/agents/<slug>/<run>` (F7). A mirror is only
needed if task state is moved out of RTDB. Under D1220's recommendation the mirror, spec Next step 6's last clause and step 9's "retire the
mirror" all disappear.

**F4. Exists. The shared writer and rules.** `ee/coordinator/state.go` already documents the rule the spec wants: a new top-level
subtree (`coordinator/project/`, `coordinator/repos/<slug>/`), deterministic IDs, written only by one account, rules scoped so a signed-in
person reads and nobody else writes (D195, answered 2026-10-21). An orchestrator subtree is the same shape. Rules are generated in
`internal/budget/rules/rules.go` (macros `RUN`, `USER`, ...), with emulator tests under `internal/budget/rules/`.

**F5. Exists. Single-writer is compare-and-swap, not transactions.** `internal/rtdb/client.go:178` `GetETag`, `:209` `PutIfMatch` (a 412 is
`ErrPrecondition`, retried a bounded number of times, never forced), used by `budget.SetKill` (`internal/budget/kill.go:189`) and by the
mailbox heartbeat lease (`internal/mailbox/lease.go`, ruling M1: "the lease IS the heartbeat ... no fourth node"). A single node holding
`{rev, cursor, state}` and written with `PutIfMatch` gives the spec's `base_rev` check atomically. *Limit:* one RTDB node is one atomic
unit; per-task documents are separate nodes and cannot be committed with the cursor together except through RTDB's multi-path `Patch`
(`rtdb.Client.Patch`, `client.go:234`), which is atomic for the paths it updates but has no if-match. See F23.

### Events and the cursor

**F6. Gap, and probably unnecessary. There is no event log; what exists is level state.** A run's facts are: the launch claim
(`runstore.Store.ClaimKey`, `ClaimTTL = 10 min`), `launch.json`, the registry entry under `/agents`, `result.json` (read by
`watch/finished.go` `FinishedRun`) and the queued scan (`watch/queued.go`). `fugaro runs ls` and `watch --json` already fold these into one
view. A level-triggered daemon ("compare desired state to the view, act on the difference") needs **no cursor, no replay and no event
ordering**, and is idempotent by construction. The spec's event log earns its place for two things level state cannot give: a human-written
`user.message` and an audit of the daemon's own decisions. Both are small. *Consequence:* the ordering and crash questions of section 3
mostly vanish in the first slice (D1229).

**F7. Exists. Worker reporting and scoped identity.** The run registry's heartbeat lives in `internal/budget/registry.go` (`goSafely(bctx,
"heartbeat", ...)`), the path is `budget.PathAgent` (`internal/budget/model.go:133`), and the rules' `RUN` macro (`rules.go:19`, "TOKEN of the
run that owns $slug/$run") is exactly "a job can post only about its own task". Nothing needs the credentials service for this; that
service's scope is model and forge credentials (`.claude/rules/credentials-service.md`).

**F8. Gap. Event-triggered Cloud Run is not provisioned.** `git grep` over `deploy/terraform/gcp` for `eventarc` and `pubsub` finds only the
custom-role permission list; the installation's API list names `run`, `cloudscheduler`, `storage`, `secretmanager`, `artifactregistry`,
`cloudbuild`, `logging`, `monitoring`. The only scheduled job is the history sweep (`modules/installation/history.tf`: Cloud Scheduler
calling the job, an own service account, no credential). Event-driven wake-up means a new API surface, Eventarc triggers, IAM and Terraform
tests (and `fugaro update` convergence for an installed project: the hard rule). Scheduler polling is the existing pattern. A *local*
daemon needs none of this: it reads RTDB over SSE (`rtdb.Client.Stream`, which resumes with a full `put` of the current value, so a missed
change is never lost).

### The MCP server and the tools

**F9. Gap. The hosted MCP server is not built.** `docs/mcp-server` is the design (answered 2026-11-04, plan M1-M10, recommended slot
0.20.0). At `5998d67c7` there is no `mcpauth`, no tool registry, no public coordinator service: the only matches for `mcpauth` outside
`docs/` are the docs test. Whether M1-M10 are being built in open work was not visible as merged code; see U2. The state tool group
therefore has no home to land in yet, and it **cannot be demonstrated end to end before M7 (registry), M9 (spawn)**.

**F10. Conflict. The spec's tool table collides with the MCP design's six tools.** Side by side:

| Spec tool | MCP design (`docs/mcp-server/spec.md` section 5) | Reading |
| --- | --- | --- |
| `spawn_task(spec)` | `spawn_task`, scope `tasks:write`, one launch path (D931) | Same tool; the MCP version's rules win (idempotency claim, `Precheck`, no ceiling widening) |
| `cancel_task(id)` | `cancel_task`, writes the same cancel marker as `fugaro cancel` | Same |
| `list_tasks(filter)` | `list_tasks` (recent runs of the project) | Same name, **different meaning**: the spec's tasks are orchestrator work items (`t-042`), the MCP's are runs. A tool whose name is shared and whose subject differs is the "field changes meaning" trap in CLAUDE.md |
| `get_task(id)` | `get_task_status`, `get_result` | Different names for runs |
| `retry_task(id)` | none (a retry is a new `spawn_task`, or `fugaro run --pr N`) | Spec-only |
| `get_events`, `get_state`, `apply_patch` | none | The state group |
| `get_artifact(path)` | `stream_logs` (recorded progress) | Overlap, different shapes |

The MCP design also states the scope rule: **two scopes in v1** (`tasks:read`, `tasks:write`; D927 refuses more), and its section 11
explicitly leaves the state group's names and whether it needs a scope "to that design's to say". `apply_patch` is not a task write: it
changes goal, facts and decisions. *Consequence:* the state group needs its own scopes (reopening D927) and a rename of the work-item
"task" (D1224).

**F11. Exists (design). One launch path.** `docs/mcp-server/spec.md` section 5 and D931: `spawn_task` takes the launch claim
(`internal/runstore/launch.go`, `ClaimKey`) and runs `budget.Precheck` (`internal/budget/precheck.go:58`, called from
`internal/cli/launch_budget.go`) before it starts anything. The spec's "`spawn_task` is the single launch path, so caps are enforced in one
place" is already that decision; only the spec's extra guardrails are new (F12).

**F12. Exists / Gap. The guardrail table, row by row.**

| Spec guardrail | Today |
| --- | --- |
| Spend, project and token caps | **Exists**: RTDB caps and `config/mode`, `Precheck`, rules-enforced; MCP tokens carry limits (M1) |
| Kill switch | **Exists**: `budget.SetKill` over `config/kill/global` and per scope. The spec's "admin command" is the existing command |
| Idempotency key on every spawn | **Exists for runs**: the launch claim makes one launch one run; `ClaimTTL` 10 min bounds it. Per-action keys are new (D1231) |
| Concurrency per project and per agent | **Gap**: `max_parallel` is the CLI's per-person limit (referenced in `internal/backend/backend.go:40` and `internal/backend/batch/reap.go`); no per-project running cap found. `projectpolicy.ConcurrencyPerToken` (`internal/projectpolicy/policy.go:534`) counts runs sharing a *credential*, a different thing |
| Max spawns per cycle / per minute | **Gap** |
| Batch approval (default 50) | **Gap**; the repo's governance work (`docs/task-governance`) is the nearest and lives in `ee/` |

**F13. Exists (other store). The coordinator.** `ee/coordinator` (`state.go`, `sweep.go`, `project.go`, `intent.go`) is the deterministic
coordinator the spec describes, writing RTDB; the resident service (P7/P8) is not started. The spec's "deterministic code is the coordinator,
the LLM only for judgement" is already the coordinator's posture. The only new part is the LLM decision call on top.

### Hosting, placement, rules

**F14. Rule. Placement under `ee/`.** `.claude/rules/ee.md`: nothing outside `ee/` imports `ee/`; `ee/` carries the Subscription licence;
governance, report and the coordinator already live there. The local single-user daemon replaces what the owner runs by hand and is a
person-level convenience, so it belongs in core (MIT) unless the owner wants it licensed; the **shared** pieces (multi-developer handoff,
central orchestrators, batch approval, workspace caps) are ee (D1222). Whichever side, the state-schema package is a core package only if
nothing in it needs `ee/`; it must not import it.

**F15. Rule / Exists (design). Decision agents.** The owner's routing rule (documented for reviewers: docs/tests/design to the light
reviewer, anything touching credentials, writes, guards, the managed gate, Terraform/IAM, the agent environment, prompts, recipes,
CLAUDE.md, AGENTS.md, `.claude/**`, plugin skills or CI is security-sensitive) is the addendum's "changes to agent prompts, recipes,
CLAUDE.md, AGENTS.md or CI workflows always count as security-sensitive" and is decided **by changed paths, never by how the change
describes itself**. That classifier is deterministic code and must be implemented as code, not as an instruction to the model. The cloud
senior review design (`docs/cloud-senior-review`, answered, not built) is the existing mechanism for "a fresh Opus agent reads a pushed
branch and writes a versioned verdict artifact the coordinator acts on" and is the template for a cloud decision agent.

**F16. Gap. A local daemon.** There is no background process, service installer or `launchd`/`systemd` unit in the CLI. The closest
long-running local pieces are `fugaro watch` (a TUI, foreground) and the mailbox consumer (`internal/mailbox`, foreground, with an RTDB
heartbeat lease that is the right single-instance fence for a daemon). The Windows CLI work (`docs/windows-cli`) means a macOS-only daemon is
a stated platform limit.

**F17. Gap. Session-start hook.** No `SessionStart` hook ships in the plugin (`git grep SessionStart -- plugin internal/claudeplugin`
returns nothing). `docs/plugin-user-scope` designs a session-start nudge at user scope; one hook slot, not two, is the right shape. Note
the plugin's tests check every command and flag a skill names against the CLI: a new `fugaro orchestrator ...` command is a plugin-test
change in the same PR.

**F18. UNVERIFIED. The scripts the daemon replaces are not in the repo.** The "never idle" rule, its Monitor watcher
(`watch-pipeline.sh`, polling `fugaro ls` and `gh pr list`) and the hourly sweep exist in the owner's scratchpad and memory
(`pipeline-keepalive.md`, `feedback-token-cost-cuts-2026-11-03.md`); `git grep` finds `finished.sh` and `queue.txt` nowhere in the tree
except an unrelated fixture scrubber. What the daemon must replace is therefore known from the owner's description only (U3).

**F19. Open. "Supersedes fugaro-coordinator-spec's auto-compact."** The text lives in `docs/coordinator/spec.md` section 9 and
`docs/central-orchestrator/spec.md` "Session memory", both owner-verbatim files. The coordinator reconciliation already rules them
`Wrong` on the store; auto-compact is a safety net in a session, which this design removes only if the resident coordinator becomes a
per-event cycle (D1236 asks who amends them).

**F20. Rule. No agent handles a credential, and an agent never judges its own gate.** `docs/mcp-server/spec.md` section 9 (a tool never
returns a credential), `docs/managed-agent-converge` (the gate is deterministic; "no judgment calls by the agent"), and
`initflow.AgentRefusal` / `CanConfirm` (`internal/initflow/stage.go:440`, `:617`: a coding agent's session applies nothing).
*Consequence:* a decision agent's patch must never be able to widen a ceiling, answer a decision addressed to a person, or turn a refusal
into a launch.

**F21. Rule. A field never changes meaning.** The spec's `task` (work item), `status`, `rigor` and `owner` fields would be new wire
shapes read by the console, mobile and the plugin. They need new names where an existing run-record or MCP field is similar (`status` is
the run record's, `owner` is a person-class concept in task governance) and a schema in `schemas/` (CLAUDE.md: wire formats decode
strictly and have JSON Schemas).

**F22. Rule. `fugaro update` is the whole upgrade.** A new RTDB subtree, rules change or Firestore collection is a rules/Terraform change
that `fugaro update` must converge on its own with a test for an installation at the previous version, and a release's `### For operators`
is "run `fugaro update`". This is a cost line for any store choice and a small one for RTDB (the rules generator already owns the
subtrees) versus Firestore (rules deploy plus an IAM grant).

**F23. Gap. The spec's atomic "apply patch + action ids + cursor" has no RTDB analogue for per-task docs.** If tasks are separate nodes
the commit is not atomic with the rev. Two honest shapes: (a) one node per orchestrator holding the whole structured state (it is capped by
design at about 4k tokens, so it is small: well under any RTDB node limit) and written with `PutIfMatch`; (b) per-task nodes plus a
multi-path `Patch` with the rev check done in a read-then-check, which has a race. (a) is recommended (D1220).

## 3. The hard questions

### Trust boundary: who writes state, and what a hostile writer reaches

The spec's rule is "the LLM never writes state directly; code validates and applies a typed patch". That is right and incomplete. The
people and processes that can reach the state:

| Writer | Reaches through | What a compromised one can do |
| --- | --- | --- |
| The local daemon (a person's laptop process) | its ADC / person credentials to RTDB | Everything the person can: that is the ceiling. A compromised **laptop session** = the person; state integrity cannot exceed it, so the cloud-side checks (kill switch, `Precheck`, per-run cap, rules) must be the real bound, not the daemon's own validation |
| An interactive Claude session | the same tools, via MCP or a CLI | `apply_patch` with attacker text from a PR title, log line or a pasted page. The text reaches `facts`, `summary` and `decisions`, which re-enter every later cycle's prompt: **stored prompt injection**, persistent across cycles |
| A decision agent | its structured output | The same as the above plus `spawn_task` actions: a loop of spawns |
| A cloud job | its own run-scoped RTDB entry only (F7) | Falsify its own heartbeat and status; it cannot touch other runs or the state doc |
| A token holder via hosted MCP | the front door's scopes | Bounded by M1's narrowing token limits; read-all/write-own is stricter than the two existing scopes |

Design rules this implies (carried to the decisions): (1) typed ops only, **no free text field that is later treated as an instruction**:
`facts` and `summary` are data, shown to the model fenced as untrusted, never above the system prompt; (2) **a decision addressed to a person
(`waiting_on: user`) is resolvable only by a person-class identity** (paired device / console), never by an agent token or by `apply_patch`
from a session that could be a hostile model: otherwise a prompt-injected agent approves its own merge (D1225); (3) **`goal_set` and any op
that widens a ceiling is person-only**; (4) **spawn from a cycle is capped by code** (count per cycle, per hour, per day, plus the existing
spend caps), and a cycle's `spawn_task` may only launch a repository and recipe already named in a task the person approved, never a new
destination (D1226); (5) the state document carries a per-field provenance (`by`: person id, session, daemon, decision-agent) so an audit can
say who wrote a fact; (6) untrusted string values are length-capped and stripped of control characters (the repo's `internal/safetext`
exists for this, and `watch/finished.go` already cleans bucket text before display).

**Single-writer.** In RTDB, `PutIfMatch` on one state node plus a daemon lease (the mailbox heartbeat-lease pattern, F5/F16) gives one
daemon per orchestrator id and a rejected write for any stale one. The spec's "Both modes coexist; transactions keep them from
clobbering each other" is correct for state and silent about **side effects**: two cycles can each decide the same spawn from the same
stale read; only the launch claim (one launch one run per key) stops the second. Idempotency keys must therefore be derived, not generated
(D1231).

### Cost of the cycle loop

Not measurable from the repo; what is known:

- The owner's own measurement (memory, 2026-11-03): the long coordinator session plus a half-hourly sweep cost about **396.5M tokens in 7
  days (about 1.4M per sweep)**, because each sweep re-read a huge context, and general-purpose subagents were 71% of spend. That is the
  baseline the design is arguing against.
- A stateless cycle's input is bounded by design: system prompt + state (target under 4k tokens) + events (batch cap 50, payloads short),
  so a cycle should be of the order of **10k input tokens, 1k output** (a design target, **UNVERIFIED**: the real size depends on the
  system prompt, which is unwritten, and on whether the cycle uses tools; U4).
- Prompt caching helps only the fixed prefix; a cycle every few minutes outlives the cache TTL, so assume uncached reads of the prefix
  (not verified for the model in use).
- **Most cycles should not call the model at all.** The coordinator spec's own table (heartbeat, progress, first failure, normal
  completion handled by code) says so. A daemon that wakes the LLM on every tick re-creates the sweep's cost at a smaller constant.

Runaway protection, in order: (1) a deterministic gate decides whether a model call is needed at all (D1221); (2) a **count cap** on model
calls per hour and per day and a **circuit breaker** after K consecutive failed or empty-diff cycles, which pauses the daemon and writes a
`pending_decisions` item for the person (owner lesson 22: a "hard $5" wording made a coder stop with no commits and an empty diff got a
"ship" review; a cycle's failure must be a visible state, not a silent no-op); (3) the existing spend caps and kill switch (cloud runs a
cycle spawns are bounded by `Precheck`); (4) a `max_output_tokens` pin per decision model (owner lesson 21: a model whose maximum output is
under 32000 broke every review until `agent.max_output_tokens.reviewer` was set). A local daemon uses the person's own Claude login: Fugaro's
gateway budget does not cover it, so (2) is the only dollar control there; this is a decision (D1228).

### Event-log ordering and idempotency

If an event log exists (D1229): ULIDs from independent writers are not totally ordered (millisecond resolution plus a random part, and
laptop clocks disagree), so **order is by the server-assigned sequence**, not by id; RTDB gives `ServerNow` (`rtdb.Client.ServerNow`) and a
push key whose prefix is a server timestamp. At-least-once delivery means the handler is keyed by event id; a **deterministic id** (run id
plus transition, e.g. `<run>:finished`) makes a producer's retry a write to the same key rather than a duplicate. The mailbox ledger
(`internal/mailbox/ledger.go`) is this repository's worked example of dedupe-before-act with a persisted record.

### The cursor, and a crash between apply and cursor advance

The spec's step 7-8 executes actions, then commits patch + action ids + cursor. A crash after the launch and before the commit re-runs the
cycle, re-issues the action, and the action id is "recorded in state" only on commit, so the id does not yet exist to dedupe. For a spawn
that is **money**. Two repairs:

1. **Intent first, at-most-once** (mailbox rule: "the record of a delivery is on disk before the action it asked for runs"): commit the
   intent `{action id, spawn spec}` with a `PutIfMatch` of state, then launch with the action id as the idempotency key, then commit the
   result. A crash between leaves an open intent; recovery re-issues with the same key and the launch claim makes it one run.
2. **Level-triggered**: no cursor at all; the next cycle re-derives "task t-042 has no run" and acts again, with the claim as the fence.

The cursor is then only a performance device (skip events already folded) and its advance can be the same `PutIfMatch` as the state
write. A cursor that moved without the effect (the reverse crash) loses an event, which is why dead-lettering and a periodic full
reconciliation against the run view must exist even in the event model (D1229).

### Cloud Run job timeouts and quotas

Known in the repo: the job's per-task timeout is a Terraform variable (`modules/workflow/job.tf:31`, `workflow.task_timeout_s`, whole
seconds, at least 1), and the backends add `TaskTimeoutSlack = 2 min` (`internal/backend/backend.go:29`) to account for it. The launch
claim lasts 10 minutes (`runstore.ClaimTTL`). The Firestore client bounds each call at 30 s with no retry.
**UNVERIFIED** (not testable from this container): Cloud Run's current maximum job task timeout; the per-project and per-region job and
execution quotas; Eventarc and Pub/Sub trigger latency and delivery guarantees for a job target; the Firestore per-document sustained write
rate the spec cites ("about one write per second per object" is the spec's figure for GCS, the Firestore document limit is similar but not
confirmed here); RTDB write throughput for one node. A cycle that runs as a Cloud Run job also pays cold start and the repo's container
image pull; it must be given a timeout shorter than the cycle interval. See U5.

### Managed-mode fit

`docs/managed-agent-converge` is built (0.18.0): in a managed project the agent itself runs converge and publish commands behind a fixed
deterministic gate, with no judgment calls. The daemon is a second kind of "agent that acts without a prompt". Rules that follow: it must not
call any gated converge command; decision agents run with the same deny rules a coding agent's session has (`initflow.AgentRefusal`); and
it must not be used to supply the "go" a managed gate withholds. A cycle may *report* "an update is due" and nothing more. Managed mode also
shows the owner's tolerance: the answer to "the agent should not prompt me" was a **fixed gate**, not agent discretion, and a state-patch
design that lets an agent decide what to approve is on the wrong side of that precedent.

### ee/ placement

See F14. A practical split: core holds the state schema, the typed patch validator and the single-user local daemon (no dependency on
`ee/`); `ee/` holds the shared parts that need identity and governance (an agent owned by the project, handoff between developers, batch
approval, per-workspace caps). Nothing in core may import `ee/`; the daemon's optional ee features must be registered through
`ee/register` the way the coordinator's CLI is.

### Migration of the owner's current scripts

What exists today (from memory, not the repo): a Monitor watcher polling `fugaro ls` and `gh pr list` every 150 s; an hourly sweep prompt
that re-reads the session; `queue.txt` and `finished.sh` (not found in the tree); launcher agents (planner, console launcher) that are
resumed. The daemon replaces the polling (code) and the re-reading (state), not the **judgement** of "which task next, which review model,
merge or fix". A safe migration: (1) the daemon runs **observe-only** (shadow) for a week, printing what it would do next to what the
sweep did; (2) the first thing it takes over is the pure bookkeeping (launch queued tasks whose dependencies merged, mark finished runs,
nudge on stuck); (3) merge decisions stay with the person or the orchestrator session until the decision agents have a measured record;
(4) the owner's existing session migration (spec "Migration") is one patch dump through `apply_patch`, and **that dump is the first
stored-prompt-injection surface**: validate it with the same code, cap it.

### Smallest useful first slice

No model call, no Firestore, no MCP, no cloud: a `fugaro orchestrator` local daemon (core, launchd/systemd user unit, DB lease) that (1)
reads the project's run view the way `watch --json` does, (2) keeps a small structured state file/node (tasks, dependencies, decisions) in
RTDB written with `PutIfMatch`, (3) launches queued tasks through the same path as `fugaro run` when dependencies are met and capacity is
free, with a per-hour spawn cap and the kill switch, (4) writes a notification when a run finishes or a decision is needed, (5) prints a
compact `get_state` for a session-start hook. That alone retires the Monitor watcher and the sweep's *polling*, which is the cost driver
the owner named, and defers every question that needs a model or a new public surface. Decision agents, the hosted MCP state group, event
log, summary versioning and the cloud daemon are later slices.

### Overlap with MCP's tools and with Watch

MCP: the state group is additive to the six tools only if its names do not collide (F10), it needs scopes D927 refused (so D927 is reopened
for it), and it cannot ship before M7/M9. Until then the same operations exist as CLI commands with `--json`, which is how the plugin's
skills drive Fugaro today. Watch: with RTDB state, Watch and the orchestrator share one database and one SSE stream; Watch can render the
orchestrator's `pending_decisions` with no mirror (F3). With Firestore state there are two stores and a mirror to build, own and retire.

## 4. DECISIONS

Numbers **D1220-D1249** (section 0). Recommendation first; the cost of the alternative is stated; the answer column is the owner's.

| # | Question | Recommendation and reason | Alternative and its cost | Answer |
| --- | --- | --- | --- | --- |
| D1220 | Where does orchestrator state live: the existing **Realtime Database** or the spec's **Firestore**? | **RTDB**, one state node per orchestrator under a new top-level `orchestrator/` subtree (like D195's `coordinator/`), written with `PutIfMatch`. Reuses CAS, SSE and Watch, the rules generator and emulator tests; no mirror, no new IAM, no new store to converge (F1-F5, F22, F23). | Firestore: needs a transaction-capable client (the present one has none), a rules change from deny-all, an IAM grant, a fake, and a Watch mirror to build and later retire; buys TTL policies, richer queries and per-document scaling. Reversible later only by a migration. | |
| D1221 | What is the first slice? | A **local, deterministic daemon with no model call**: run-view reader, small structured state, dependency-driven launch through the `fugaro run` path with a spawn cap, notifications, a `get_state` print (section 3, "Smallest useful first slice"). It retires the sweep and the Monitor, the owner's named cost. | Build the spec's full cycle (model + event log + MCP tools) first: weeks more before the first saving, and every unsolved question below blocks it. | |
| D1222 | Core (MIT) or `ee/`? | **Core** for the state schema, patch validator and single-user local daemon; **`ee/`** for shared agents, handoff between developers, batch approval and workspace caps, registered through `ee/register` (F14). | All in `ee/`: one licence line, but the single-user convenience the owner wants today would need the Subscription build, and the core could not test it with `ee/` deleted. | |
| D1223 | Until the hosted MCP server exists, how does a session reach the state? | **CLI commands with `--json`** (`fugaro orchestrator state / events / patch`) taught by the plugin skill, as every other Fugaro operation is; the MCP state group follows the MCP plan's M7/M9. | Wait for MCP: nothing usable for months (F9). Or build a local stdio MCP server: a second surface the MCP design deliberately avoided (D931). | |
| D1224 | Tool and field naming against the MCP design | Adopt the MCP names for runs (`spawn_task`, `cancel_task`, `list_tasks`, `get_task_status`) and **rename the spec's work item** (e.g. `item`/`work_item`) so "task" stays a run; state group `get_state`, `get_events`, `apply_patch`; `retry_task` becomes a `spawn_task` with the same idempotency key family (F10, F21). | Keep the spec's names: two `list_tasks` with different subjects, a field-meaning collision CLAUDE.md forbids. | |
| D1225 | Who may resolve a decision addressed to a person, and who may call `goal_set`? | **Only a person-class identity** (paired device, console, the CLI as the signed-in person); an agent token or a session's `apply_patch` may **add** decisions and propose resolutions but never close a `waiting_on: user` item or set the goal. | Agent may resolve any decision: a prompt-injected session approves its own merge; fixing that later changes the wire shape. | |
| D1226 | What may a cycle spawn? | Only a repository, recipe and rigor **already named in an approved work item**, through the single launch path, capped per cycle, per hour and per day by code, plus the existing spend caps; a new destination needs a person. | Free-form `spawn_task` from a model: one injected log line reaches money. Cost of the cap: legitimate fan-out is slower. | |
| D1227 | New scopes for the state group (reopens D927's two-scope rule)? | **Yes: `state:read` and `state:write`**, `write` also gated by D1225; every token read-all/write-own as the spec says, but a token holding only `tasks:*` cannot touch state. | Reuse `tasks:write` for `apply_patch`: a spawn-capable token silently becomes a state-editing token and cannot be narrowed later without breaking it. | |
| D1228 | What runs a decision agent, and what bounds its cost? | **Local `claude -p` first** on the person's own login, each call with a fixed brief, `--max-turns`, a pinned `max_output_tokens`, a **count cap** (proposal: 6 calls per hour, 40 per day) and a circuit breaker after 3 consecutive empty or failed cycles that pauses the daemon and raises a decision for the person. Cloud variant later on the cloud senior reviewer's pattern. | A Fugaro cloud run per decision: gateway-budgeted and isolated, but each cycle pays container start and a job slot; do it only for the security-sensitive class. Numbers are proposals, not measured (U4). | |
| D1229 | Event log or level-triggered reconciliation? | **Level-triggered in v1**: each tick derives actions from state vs the run view (idempotent, no cursor, no replay). Keep a small append-only **decision/audit log** (deterministic ids) only for the daemon's decisions and `user.message`; add a cursor when a producer needs one. | The spec's full event stream with ULIDs and a cursor: more machinery, an ordering problem across clocks, a dead-letter path, and the crash window below. | |
| D1230 | Event ordering key, if a log exists | **Server-assigned** (RTDB push key / `ServerNow`), **deterministic ids** (`<run>:<transition>`) for producer retries; the cursor is a performance device, never the source of truth. | ULIDs minted by writers: laptop clocks make order ambiguous. | |
| D1231 | Order of effect and commit | **Intent first**: commit the action intent (derived id: work item + attempt) with `PutIfMatch`, then launch under that id as the launch claim key, then commit the outcome. At-most-once for money; recovery re-issues the same key. | The spec's act-then-commit: a crash between spawns twice because the id is not recorded yet. | |
| D1232 | Platforms for the local daemon | **macOS launchd and Linux systemd user unit** installed by `fugaro orchestrator install`, one instance by an RTDB heartbeat lease (the mailbox lease pattern); Windows out of v1 (`docs/windows-cli`). A sleeping laptop is fine: cloud runs continue and the next tick catches up (level state). | macOS only (cheapest, matches the owner's machine); or all three (a service-manager abstraction to test per OS). | |
| D1233 | Keep `summary_replace`? | **No in v1.** State is structured (items, decisions, facts); the human-readable summary is **generated** from them on read, never written by a model. Removes the narrative field a hostile or confused cycle can drift or poison, and the whole summary-versioning chain (spec Next step 7). | Keep it with version history and a periodic rebuild: more code, and a persistent prompt-injection carrier that re-enters every later prompt. | |
| D1234 | Where is the model pinned per decision type, and who classifies "security-sensitive"? | The classifier is **deterministic code over changed paths** (the owner's reviewer-routing rule, F15), not model judgement and not configurable downward; the model name per class is a local config key, tightening-only against any project layer. | Model picks its own tier or a project may lower it: a security change reviewed by Sonnet because the description said "docs". | |
| D1235 | Migration of the owner's scripts | **Shadow mode for about a week** (daemon prints what it would do beside the sweep), then it takes over bookkeeping only (launch when dependencies merged, mark finished, stuck nudge); merge and review-model choices stay with the person until decision agents have a record. The one-time session dump goes through the same validated, capped `apply_patch`. | Cut over at once: no comparison, and a bad rule launches real runs on the owner's installation (`project belong` must not be touched in live steps). | |
| D1236 | Amending the owner-verbatim coordinator and central-orchestrator specs (auto-compact, Firestore) | **Do not edit them.** Add an "amended by `docs/stateless-orchestrator`" note to each design's README, as other designs do. Whether the coordinator's resident service becomes a per-event cycle is a P7/P8 question for its own design. | The owner edits the originals: authoritative but loses the record of what was asked. | |
| D1237 | Session-start hook | **One hook slot shared with `docs/plugin-user-scope`'s session-start nudge**: print a short `get_state` summary and any pending decision; `CLAUDE.md` instruction alone for v1 is the fallback. | A second hook: two things that print at start and must be ordered; or no hook: the session starts blind to state, the problem this design is for. | |
| D1238 | Managed projects | The daemon and its decision agents **never run a gated converge or publish command**; they may report "an update is due" and nothing else. | Let a managed project's daemon call the gate: ties the agent's judgment to the converge gate the owner deliberately made deterministic. | |
| D1239 | Spawn-guardrail defaults not in the repo (project running cap, per-minute spawn rate, batch size) | Put them in the **project policy as tightening-only** fields with conservative defaults (proposal: running cap = the person's `max_parallel`, batch 10 rather than the spec's 50); decided in the plan's slice that needs them. | The spec's 50: a looping cycle can launch 50 real runs before any person is asked. | |
| D1240 | Release slot | **0.21.0 or later**, after the MCP chain (0.20.0 slot) for anything that needs the hosted tools; the local deterministic daemon (D1221) is independent of MCP and can ship earlier if the owner wants the saving first. | Same window as MCP: both are security-heavy and both touch the rules and `fugaro update`. | |

D1241-D1249 are reserved and unused.

## 5. UNVERIFIED

- **U1.** Whether the Firestore REST `documents:commit` and `beginTransaction`, with the per-document preconditions the client already
  wraps, would give the spec's multi-document atomic commit. Known from Google's public API, not exercised here; the repo's client does not
  call them.
- **U2.** Whether M1-M10 of the MCP plan are in flight on a branch: `origin/main` has no `mcpauth` code and none of the open pull requests
  listed on 2026-11-04 carries it. The brief says it is being built; the first tasks may sit on a long-lived branch not checked out here.
- **U3.** The owner's current scripts: `watch-pipeline.sh`, `queue.txt`, `finished.sh`, the sweep text. They are described in memory only
  and are not in the repository; what the daemon must replace is taken from that description.
- **U4.** The token and dollar cost of a cycle and of a decision call (about 10k in, 1k out is a design target, not a measurement); prompt
  cache behaviour at a cycle interval; the proposed call-count caps.
- **U5.** Cloud Run limits: the current maximum job task timeout, per-project and per-region execution quotas, Eventarc or Pub/Sub trigger
  latency and delivery guarantee for a Cloud Run job, and Firestore and RTDB sustained write rates. This container cannot reach the public
  documentation (the limitation `docs/coordinator/reconciliation.md` records as U1).
- **U6.** Whether the owner wants the single-user daemon under the Subscription licence (D1222's alternative); counsel's reading of what is
  "business" is not in the repo for this feature.
- **U7.** Whether the "hosted" and "cloud orchestrator" in the addendum are meant to be the central orchestrator (`docs/central-orchestrator`,
  plan only, D1-D26 open) or the coordinator's resident service (`ee/coordinator`, P7/P8 not started); the addendum treats them as one
  ("like the coordinator").
- **U8.** Live behaviour: nothing in this reconciliation was run against a real installation (RTDB, Firestore or Cloud Run). No owner check
  number is claimed.

## 6. What this reconciliation does not do

It writes no plan, no code, no Terraform and no rules change, and it does not approve any task. It keeps one rule from the folders it
reads: **no agent handles a credential**, and the daemon is not allowed to turn a refusal into an action.
