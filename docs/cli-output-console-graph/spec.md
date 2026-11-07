# Fugaro: CLI Output Cleanup and Console Dependency Graph

Two related changes: make CLI output concise by default, and rework how the console gets and shows the dependency graph.

## 1. Concise CLI output

### Problem

Commands such as `fugaro update` (and in the past `fugaro doctor`) print far too much. Users only need to see what is happening and whether it worked.

### Goals

- Short, colourful, Homebrew-style output by default.
- Full detail when you ask for it with `-v` / `--verbose`.
- Full output always kept in a log so you can look at it afterwards.

### Default output

- One line per step: a coloured marker, a short verb phrase (e.g. "Updating recipes"), then a green tick or a red cross when it finishes.
- A spinner while a step is running (only when output goes to a terminal).
- A one-line summary at the end (e.g. "Done in 4.2s, 6 steps").

### When something fails

- Show the failed step, the error message, and the last few lines of context.
- Always print the path to the full log, so nobody has to rerun with `--verbose` just to see what went wrong.
- Exit with a non-zero code.

### Verbose mode

- `-v` / `--verbose` streams everything, like today's behaviour.
- Optional later: `-vv` for debug-level output.

### Logs

- Write the full output to a log file for every run, whatever the mode, e.g. `~/.fugaro/logs/<command>-<timestamp>.log`.
- Delete old logs automatically (after 14 days or 50 runs, whichever comes first).

### Pipes, CI and machines

- Turn off colour and spinners when output isn't going to a terminal, or when `NO_COLOR` is set.
- `--json` stays as the structured output for machines; it is never affected by the human-friendly formatting.

### Implementation note

Add one shared output/reporter module (steps, statuses, spinner, summary, log sink) and move every command onto it, so they all behave the same. Don't patch commands one at a time.

## 2. Console dependency graph

### Problem

At the moment the console asks you to pick a repo before it will show the graph, because it reads documentation from the repo itself. That's wrong in two ways:

1. The console reads data itself instead of going through the CLI.
2. The graph belongs to the orchestration layer, not to repos.

### Principle: the console goes through the CLI

- The console gets all its data by calling the Fugaro CLI with `--json`, so parsing, auth and data shapes live in one place.
- The one exception is realtime data from RTDB, and that subscription lives inside the CLI's watch mode (see Live updates), so the console never talks to RTDB directly.

### Graph sources

1. **Local orchestrators** — none, one or several running on the laptop.
2. **Central orchestrators** — seen through the coordinator, not queried as a separate source (see Decisions).
3. **Global coordinator** — every orchestrator should report to it, so it is the overall source of truth.

### Finding local orchestrators

- Each local orchestrator writes a small registry file on startup, e.g. `~/.fugaro/run/<id>.json`, containing its PID, port/endpoint, project and start time, and removes it on a clean shutdown.
- The CLI reads the registry, removes files whose process has died, and queries the live ones.
- This is preferred over mDNS or network discovery because it is simpler and more reliable on a laptop.

### Choosing what to show

- One graph, with a dropdown to choose the scope:
  - **All** (default) — from the coordinator.
  - Each discovered **local orchestrator**.
  - Each **central orchestrator**, as a filter on coordinator data.
- Local orchestrators are queried directly, not just filtered from coordinator data. That way they still work when offline or not reporting, and the data is fresher.

### CLI contract

- A command along the lines of `fugaro graph --json [--source all|local:<id>|orchestrator:<id>]`; `orchestrator:<id>` filters the coordinator view to one central orchestrator.
- `fugaro orchestrators --json` lists the discovered local orchestrators and the central orchestrators known to the coordinator, for the dropdown.
- The output is a single combined graph: nodes and edges, with each node tagged by its source and keyed by a stable task ID so duplicates across sources are removed.

### Live updates

The graph stays live through a streaming CLI mode, so the console still has only one way in.

- `fugaro graph --watch --json [--source …]` prints one full snapshot, then streams changes as newline-delimited JSON events (e.g. task added, status changed, task failed, edge added, orchestrator up or down).
- The console runs this as a long-lived child process and applies each change to the rendered graph.
- Coordinator view: the CLI subscribes to RTDB and turns database changes into events. The RTDB exception lives in the CLI, not the console.
- Local orchestrators: each one offers a localhost Server-Sent Events endpoint (authenticated with the registry token). SSE is preferred over WebSockets because the updates only go one way.
- Reconnecting: if a stream drops, the CLI reconnects and sends a fresh snapshot. Events carry a sequence number so the console can spot gaps.
- New local orchestrators that appear in the registry while watching are picked up automatically; ones whose process dies are reported as down.

### Console changes

- Remove the repo requirement and any code that reads docs directly.
- Add the scope dropdown; render whatever the CLI returns.
- Keep the graph live by consuming `fugaro graph --watch --json` (see Live updates).

## Decisions

- **Central orchestrators are not a separate source.** They report to the coordinator, so you see them through the coordinator view (scoped by orchestrator). Direct connections are only for local orchestrators. The scope dropdown lists All, then each local orchestrator, then central orchestrators as coordinator filters.
- **Local orchestrator auth:** each orchestrator listens on localhost only and writes a random token into its registry file (file mode 0600, readable only by the user). The CLI sends that token with every request.
- **Log retention and location:** delete logs after 14 days or once there are more than 50 runs, whichever comes first. Location: `~/.fugaro/logs` on macOS and Linux, `%LOCALAPPDATA%\fugaro\logs` on Windows.
