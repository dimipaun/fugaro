# Fugaro: Stateless Orchestrator Loop

Goal: keep the orchestrator and coordinator agents' context small and roughly constant, no matter how long they run. Treat the LLM context as a disposable scratchpad and keep durable state outside it.

## Core idea

- State lives in Firestore, not in the conversation. GCS holds only bulky artifacts (logs, diffs, archives).
- Inputs arrive as events in an append-only log.
- Each cycle: fresh context = system prompt + state + new events. Agent returns actions + state patch. Code applies them, persists state, advances cursor. Context is discarded.
- Result: the orchestrator no longer needs to be long-running; it can run as an event-triggered Cloud Run job.

## Interactive mode: Claude on the laptop

No new harness. The developer keeps talking to Claude on their laptop as the orchestrator; it simply stops holding state in its context. The session becomes a disposable front end over Firestore.

### Fugaro MCP server

A small MCP server, backed by Firestore, that Claude uses instead of remembering:

| Tool | Purpose |
| --- | --- |
| `get_state` | Compact snapshot: goal, summary, active tasks, open decisions |
| `list_tasks(filter)` | Tasks by status, repo or owner |
| `get_task(id)` | Full record, including archived tasks |
| `spawn_task(spec)` | Create and launch a Cloud Run task |
| `retry_task(id)` / `cancel_task(id)` | Control running work |
| `apply_patch(patch)` | Validated state update in one transaction |
| `get_events(since)` | New events since the agent's cursor |
| `get_artifact(path)` | Truncated read of a log or diff in GCS |

Writes go only through `apply_patch` and the task tools, so validation and the single-writer rule still hold.

### Session start

- A session-start hook (or a `CLAUDE.md` instruction) calls `get_state` and `get_events` and injects the summary.
- `CLAUDE.md` tells Claude: Firestore is the source of truth; record decisions and facts via `apply_patch` as they happen; don't rely on chat history.

### Working loop

1. Developer talks to Claude as today.
2. Claude reads detail on demand through the tools and writes changes back immediately.
3. When context grows, the developer clears it or starts a new session; nothing is lost.

### Headless mode, later

The same MCP tools power an event-driven background runner (the cycle described below) for when no one is at the laptop. Same state, same tools, no human in that loop. Both modes can coexist; Firestore transactions keep them from clobbering each other.

## Storage layout

```
Firestore:
projects/{project}/agents/{agentId}                  # role, goal, summary, cursor, facts
projects/{project}/agents/{agentId}/tasks/{taskId}
projects/{project}/agents/{agentId}/decisions/{id}
projects/{project}/agents/{agentId}/workers/{id}     # coordinator only
projects/{project}/agents/{agentId}/events/{ulid}

GCS:
gs://<bucket>/projects/<project>/artifacts/...        # logs, diffs, outputs
gs://<bucket>/projects/<project>/archive/...          # compacted old events and tasks
```

One mechanism for all agents: same code, same transactions, one place to debug. Use ULIDs for event IDs so lexical order = time order. The state schema below is the logical shape; tasks and decisions are stored as subcollection docs so frequent updates touch small documents.

## State schema

```json
{
  "schema_version": 1,
  "agent_id": "orchestrator-main",
  "role": "orchestrator",
  "goal": "One-paragraph statement of the current objective",
  "summary": "Running narrative, hard cap ~1500 tokens",
  "cursor": "01JC...ULID of last processed event",
  "tasks": [
    {
      "id": "t-042",
      "title": "Add retry to webhook handler",
      "status": "queued | running | review | done | failed | blocked",
      "owner": "cloud-run-job-id or agent id",
      "repo": "org/repo",
      "rigor": "startup",
      "attempts": 1,
      "depends_on": ["t-040"],
      "last_update": "2026-11-02T15:04:05Z",
      "note": "One line, optional"
    }
  ],
  "pending_decisions": [
    {"id": "d-7", "question": "Merge t-041 despite flaky test?", "waiting_on": "user"}
  ],
  "facts": {
    "key": "short durable facts the agent must not forget"
  },
  "updated_at": "2026-11-02T15:04:05Z"
}
```

Rules:

- Done/failed tasks older than N cycles move to the archive; only IDs + outcome stay in `summary`.
- `facts` is a small key/value map; cap its size.

## Event schema

```json
{
  "id": "01JC...",
  "type": "task.completed | task.failed | task.progress | user.message | timer.tick | review.result",
  "task_id": "t-042",
  "payload": { "short": "result summary, links to artifacts not full logs" },
  "ts": "2026-11-02T15:04:05Z"
}
```

Keep payloads short. Large outputs (logs, diffs) go to GCS and the event carries a path.

## The cycle

1. Trigger: new event (Pub/Sub or Firestore trigger) or periodic tick.
2. Load the agent doc plus active tasks and open decisions by query.
3. Query events after `cursor` (cap batch size, e.g. 50).
4. Build prompt: system prompt + role instructions, state snapshot, new events.
5. Call model requesting structured output (below).
6. Validate output against schema; reject and retry on failure.
7. Execute actions (spawn jobs, post messages, etc.). Make them idempotent, keyed by action ID.
8. In one Firestore transaction: apply `patch`, record executed action IDs, advance `cursor`.
9. On contention the transaction retries automatically; if state changed underneath, re-run from step 2.
10. Discard context.

## Model output contract

```json
{
  "actions": [
    {"id": "a-123", "type": "spawn_task", "task": {"id": "t-043", "title": "...", "repo": "...", "rigor": "pro"}},
    {"id": "a-124", "type": "message_user", "text": "..."}
  ],
  "patch": {
    "base_rev": 57,
    "ops": [
      {"op": "task_upsert", "task": {"id": "t-042", "status": "done"}},
      {"op": "decision_resolve", "id": "d-7", "outcome": "merged after rerun"},
      {"op": "fact_set", "key": "deploy_window", "value": "weekdays after 18:00"}
    ]
  }
}
```

### Patch operations

Typed ops only; free-form merges are rejected. A patch whose `base_rev` is behind the stored revision is rejected and the agent re-reads state.

| Op | Fields | Validation |
| --- | --- | --- |
| `task_upsert` | task (partial) | Status changes must follow allowed transitions |
| `task_archive` | id, outcome | Only done, failed or cancelled tasks |
| `decision_add` | id, question, waiting_on | Unique id |
| `decision_resolve` | id, outcome | Decision must be open |
| `fact_set` | key, value | Caps on fact count and value length |
| `fact_delete` | key | Key must exist |
| `summary_replace` | text | Full rewrite, token cap |
| `goal_set` | text | Rare; may require developer approval |

Allowed task transitions: queued → running → review → done; any active state → failed, blocked or cancelled; failed or blocked → queued (retry).

## On-demand read tools

Instead of preloading, give the agent tools for detail:

- `get_task(id)` full task record including archived ones
- `get_artifact(path)` truncated read of logs/diffs
- `search_history(query)` over archived events and summaries

This is where something like OpenViking could plug in later, for long-term knowledge and memory, not for operational state.

## Context budget

- System prompt: fixed.
- State: target under ~4k tokens; enforce by archiving and summary cap.
- Events: batch cap; summarise oversized payloads before inclusion.
- If a cycle exceeds budget: process events in multiple smaller cycles.

## Reliability

- Audit trail: events are kept (TTL or archived to GCS), so state can be replayed and debugged.
- Idempotent actions keyed by action ID, recorded in state, so a crashed cycle can be safely re-run.
- Single-writer via Firestore transactions; no locks needed.
- Dead-letter: events that fail processing K times are moved aside and flagged in `pending_decisions`.

## Coordinator vs orchestrator

Same loop, different state:

- Orchestrator: goal, task graph, decisions.
- Coordinator: assignments, worker health, queue depth, retries.

Each has its own state and event stream; they talk only via events. Both keep state in Firestore under the same layout.

## Coordinator specifics

The coordinator writes state far more often than the orchestrator; that write rate is the main reason all agent state lives in Firestore rather than GCS, which allows about one write per second per object.

**Supersedes fugaro-coordinator-spec:** this design replaces that spec's "auto-compact on" approach for the long-running coordinator. Context stays small through externalised state and fresh LLM calls, so auto-compact is no longer needed. Update that spec to match.

### Split of responsibilities

- Deterministic code is the coordinator: it consumes events, updates assignments, heartbeats, queue depth and retries directly in Firestore.
- The LLM is invoked only for judgement: repeated failures, stuck tasks, ambiguous review results, re-planning. It receives a compact snapshot and returns actions + a state patch.
- Code validates the patch and applies it in a Firestore transaction. The LLM never writes state directly.

### Data model

```
projects/{project}/agents/{coordinatorId}/workers/{workerId}   # heartbeat, current task, health
projects/{project}/agents/{coordinatorId}/tasks/{taskId}       # assignment, attempts, timeout
```

- One document per task and per worker, so frequent updates touch small docs, not one big state blob.
- Cursor update + task changes commit in one transaction; contention retries are automatic.
- LLM snapshot is assembled by query (active tasks, unhealthy workers, recent failures), keeping the prompt small.
- Large artifacts (logs, diffs) stay in GCS; Firestore holds paths.
- Firestore TTL policies can expire old events and heartbeats.

### When to wake the LLM

| Event | Handled by |
| --- | --- |
| Heartbeat, progress ping | Code |
| Task completed | Code (LLM only if follow-up needs planning) |
| Task failed, first time | Code retries |
| Task failed, retries exhausted | LLM |
| Task stuck past timeout | LLM |
| Ambiguous review result | LLM |

## MCP server

The state tools live in the same hosted MCP server already planned inside each project's coordinator (Go, OAuth via the project's Firebase, tokens with optional rigor and spend caps; see fugaro-mcp-server-spec).

- Tool groups: existing job tools plus the state group (`get_state`, `get_events`, `apply_patch`, task and decision tools). `spawn_task` is the single launch path, so caps are enforced in one place.
- Each token is bound to one agent identity.
- Scope: every token can read the whole project's state; it can write only to its own agent. Admins can write to any agent.
- Roles: viewer = reads; operator = `apply_patch` and spawning; admin = `goal_set`, other agents, settings.
- The coordinator writes its own state directly to Firestore (not via MCP), using the same validation code.

## Worker reporting

- Each Cloud Run job gets a short-lived token scoped to its own task, issued via the credentials security service. It can only post events for that task.
- Events: started, heartbeat, progress, completed, failed. Payloads carry GCS paths to logs and diffs, never the content.
- Crash detection: Cloud Run execution-finished events route via Eventarc to the coordinator, which marks silent failures. Missed heartbeat past timeout is the backstop.
- Fugaro Watch stays on Realtime Database for now; the coordinator mirrors task events into it. Retire the mirror later, e.g. when the Electron console ships.

## Spawn guardrails

| Guardrail | Rule |
| --- | --- |
| Spend | Project caps plus token caps, checked by `spawn_task` before launch |
| Concurrency | Max running tasks per project and per agent |
| Spawn rate | Max tasks created per cycle and per minute; catches loops |
| Duplicates | Idempotency key on every spawn |
| Batch approval | Project setting, default 50 tasks in one batch; can be raised or disabled |
| Kill switch | Admin command pauses all spawning for the project |

## Multiple developers

- One agent per developer, or per workstream; state never mixes.
- Conflicts are advisory only: tasks declare repos or areas touched, and the coordinator flags overlaps. No locks; Git handles small overlapping edits.
- Handoff: owner or admin transfers an agent to another developer, who picks up from Firestore.
- Planned shared central orchestrators are agents owned by the project rather than a person; no special handling.

## Summary drift

- Summary is narrative only. Anything that must survive goes into facts or decisions.
- `summary_replace` receives the old summary and must confirm nothing important was dropped, or promote it to facts first.
- Every summary version is kept for review and rollback.
- Periodic rebuild: regenerate the summary from events and tasks, not from the previous summary, so errors don't compound.

## Migration

There is one existing session to migrate.

1. In the current session, ask Claude to dump everything it tracks (tasks, decisions, facts, summary) as a patch.
2. MCP server validates and loads it into a new agent document.
3. Check with `get_state`.
4. Start a fresh session on the new setup.

## Next steps

1. Finalise state schema v1, patch ops and task transitions; implement shared validation code.
2. Set up the Firestore layout and add the state tool group to the coordinator's MCP server, with read-all/write-own scoping.
3. Add spawn guardrails to `spawn_task` (concurrency, rate, idempotency, batch limit, kill switch).
4. Write the session-start hook and `CLAUDE.md` instructions for interactive mode.
5. Migrate the one existing session via a patch dump, then start fresh.
6. Worker reporting: task-scoped tokens, events, Eventarc crash detection, mirror to Realtime Database for Watch.
7. Summary safeguards: versioning, rewrite check, periodic rebuild.
8. Headless event-driven runner using the same tools.
9. Later: retire the Watch mirror; evaluate OpenViking for long-term memory.


---

# Addendum to fugaro-stateless-orchestrator-spec.md: Orchestrator daemon

## Orchestrator daemon

Every orchestrator gets a companion daemon. The daemon is plain code that owns the loop: launching jobs, tracking runs, CI and releases, and applying routine rules. It starts a fresh, stateless LLM agent only when a decision needs judgement. The interactive Claude session tells the daemon what to do and is never the thing that keeps work alive.

## Two kinds of orchestrator, one daemon

- **Local orchestrator:** runs on a developer's machine. Its daemon runs on the same machine, as a background process that doesn't depend on the terminal tab.
- **Cloud orchestrator:** hosted as a service, like the coordinator. Its daemon runs in the cloud next to it.
- The daemon goes wherever its orchestrator lives. It's the same code, packaged two ways. There's no benefit to running a cloud daemon for a local orchestrator that is offline.
- The per-project coordinator keeps its own cloud daemon, as in the existing spec.

## Resilience rule: never assume the orchestrator is alive

The orchestrator and its daemon can disappear at any moment: a closed terminal tab, laptop sleep, the laptop being carried away, a crash. The design must treat that as normal.

1. **Jobs never depend on the orchestrator.** Every Fugaro job writes its result and events to Firestore whatever happens, even if nobody is listening.
2. **Durable event log.** Job, CI and release events are written to a per-orchestrator event stream in Firestore before anything consumes them.
3. **Cursor-based catch-up.** The daemon stores, in Firestore, a marker of the last event it handled. On start or restart it replays everything after that marker, then switches to live updates.
4. **Duplicate-safe handling.** Delivery is at least once. Handling the same event twice must be harmless, keyed by event ID.
5. **No in-memory-only state.** Anything the daemon needs after a restart lives in Firestore: pending decisions, in-flight launches and rules. Logs, diffs and archives go to GCS as already specified.
6. **Automatic restart.** A local daemon runs under the OS service manager (launchd on macOS) and comes back after a crash or reboot. Closing the orchestrator's tab doesn't kill the daemon.
7. **Reattach.** A new interactive session finds the running daemon, reads state from Firestore and carries on. No handoff from the dead session is needed.

## Decision agents

- Each judgement call runs as a fresh agent with a small, specific brief (for example: "CI failed on PR #123 with this log excerpt; retry, fix, or escalate?"), never the whole project context.
- The model is chosen per decision type and pinned in config: Haiku or Sonnet for routine calls, Opus for security-sensitive ones. Changes to agent prompts, recipes, CLAUDE.md, AGENTS.md or CI workflows always count as security-sensitive.
- The decision and its reasoning are written back to Firestore as an event.

## What this replaces

- The "never idle: cron sweep plus monitors" rule and the 30-minute SWEEP keepalive.
- Long-lived launcher agents (the planner and the console launcher) that are resumed over and over.
- Days-long orchestrator sessions whose cost per turn grows with context size.

