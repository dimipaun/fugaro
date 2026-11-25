# Fugaro Console Fixes Spec (Nov 2026)

Three console problems: list and graph performance, the coordinator tab, and wrongful redaction.

## 1. Performance: sub-second at any scale

**Problem.** The console graph times out after about 90 seconds. `fugaro runs ls` is also very slow. One project already has more than 3,500 PRs, and Fugaro has to work at 100 to 1,000 times that volume.

**Principle.** A gap of about 100 times is a design problem, not a tuning problem. Do not start by "measuring first". Every list, graph and dashboard view must load in under a second, in time that does not grow with history size.

**Required work.**

- **Audit, don't benchmark.** Trace exactly what the console graph and `fugaro runs ls` read today, call by call: the data source (CLI, Firestore, RTDB, GCS), how many requests are made and what gets scanned. Document the path. It is currently unclear where the console's graph data comes from.
- **Eliminate full scans and N+1 patterns.** No GCS listings, log replays or per-item fetches just to render a view.
- **Maintain indices.** Keep compact per-project index or summary documents in Firestore (runs, tasks, graph nodes and edges, status counts), updated incrementally whenever runs or tasks change.
- **Paginate and sort by time.** Show recent and active items by default; older history appears on demand through cursor-based pagination.
- **Lazy-load details.** Fetch heavy data such as logs, diffs and per-job detail only when the user opens an item.
- **Share the data layer.** The console and the CLI should use the same indexed queries.

**Backfill.** Provide a backfill and rebuild command for the indices, so existing projects (for example the one with 3,500+ PRs) are fully indexed, not just new runs. Rebuilding must be idempotent and safe to rerun.

**Acceptance.** The graph and runs list load in under 1 second on a good connection with today's data, and stay under 1 second against synthetic data at 100 times today's volume.

## 2. Coordinator tab redesign

**Problem.** Today the tab is a long, noisy list of tasks and jobs that is not useful.

**Design.**

- **No history.** Show only orchestrators that are currently active.
- **A graph, not a list.** Each active orchestrator is a node.
- **Node contents.** Who is driving the orchestrator (a person or a service account), plus key numbers such as active tasks and running jobs.
- **Edges only for real connections.**
    - Dependency, drawn as a solid line: one orchestrator's task waits on another's.
    - Overlap, drawn as a dashed warning line: tasks touching the same files or areas, or overlapping PRs.
- **Drill-down.** Clicking a node shows that orchestrator's live tasks.
- **Optional.** A small health line for the coordinator itself (alive, last woke up).

## 3. Remove redaction from the console

**Problem.** Fields such as email and author, plus several others, show as "redacted" or "unavailable".

**Requirement.**

- The console is an operator tool for authorised project members. Do not redact identity fields (email, author, names) anywhere in it.
- Keep masking **only secrets**: API keys, OAuth tokens and credentials that appear in logs or payloads.
- Find the source. A generic PII scrubber may be applied on the console path; remove it there.
- Investigate "unavailable" fields separately, since they may be a data-loading bug rather than redaction.
