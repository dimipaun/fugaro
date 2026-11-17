# Fugaro design decisions — Nov 16, 2026

## 1. Task estimates

- Goal: when a spec produces a task graph, show when the work will be ready and roughly what it will cost. This is informational only and does not reserve budget.
- The planner tags each task with a rough size (small / medium / large).
- Use historical medians per size and rigor level, for both duration and cost.
- Cost estimate = sum across all tasks.
- Time estimate = critical path through the dependency graph, capped by how many jobs can run in parallel.
- Show ranges (e.g. "ready in 40–70 min, $3–$5") and recalibrate as tasks finish. Before there is any history, seed each size with default time estimates from our own Fugaro experience (no cost defaults: cost depends too much on each setup's model choices), then let real data take over. Keep ranges wide early on.

## 2. Non-spec (ad hoc) items

- One standing backlog graph per project, in the same JSON graph format as spec graphs.
- Each ad hoc item (bug fix etc.) becomes a node, usually with no dependencies, so it gets the same pipeline, estimates, dashboard status and history.
- Ways in:
  - Casual: telling the agent "let's fix this" adds a node straight away, with no ticket needed.
  - External: GitHub / Jira adapters pull issues in as nodes and keep the external ID, so a merged PR can close the original ticket.
- Mirroring: when an adapter is configured, internally created items are mirrored back out as tickets; otherwise they stay internal.

## 3. Explicit vs implicit graph dependencies

- The spec-to-PR skill now writes an explicit JSON dependency graph.
- The orchestrator reads that graph deterministically, with no LLM involved; e.g. when a task merges, it unlocks the tasks that depend on it.
- Fallback: if no explicit graph exists, the LLM generates the same JSON graph once, upfront, rather than being asked at each merge.
- Result: a graph always exists, can be inspected, and feeds the estimates in item 1.
- If dependencies turn up mid-flight, the graph gets updated (see item 4).

## 4. Callbacks from Cloud Run to the orchestrator

- Current: a periodic polling sweep (around every 30 min) checks for finished jobs.
- Decision: the orchestrator listens to Realtime Database and reacts the moment a job's status changes to done.
  - Chosen over Pub/Sub/Eventarc because RTDB is already in place and works for local orchestrators as well as cloud ones.
- A crashed job never writes "done", but onDisconnect clears its entry. The orchestrator treats a job that disappears without finishing as a likely failure and checks on it.
- Keep the sweep as a slower fallback for anything that gets missed.
- Running jobs can also report newly found dependencies through the same channel, and the orchestrator updates the graph.

## 5. Stateless orchestrator

A deterministic, stateless orchestrator daemon is in the works. It runs locally or in the cloud and does the orchestrating instead of the agent. The build looks complete; two things are still open.

- [ ] Release: the next Fugaro update enables it automatically. Operators don't need to do anything to start using it. On upgrade it takes over any jobs and graphs already running, and a kill switch lets you fall back to the old behaviour if needed.
- [ ] Open question: how to teach the agent to work with it, probably through a skill.

## Watch-outs

- **Mirroring loops:** an internal item mirrored to GitHub can be pulled back in as a duplicate. Use the external ID as the dedupe key.
- **Double handling:** the RTDB event and the sweep may both catch the same finished job, and two orchestrators may race. Make unlocking idempotent, ideally inside an RTDB transaction.
- **Offline local orchestrators:** a laptop that sleeps misses events. Run a sweep straight away on reconnect.
- **Wrong inferred graphs:** a missed dependency lets clashing tasks run in parallel. Treat merge conflicts as a sign the graph needs fixing.
