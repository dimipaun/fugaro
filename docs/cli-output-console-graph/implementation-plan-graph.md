# Implementation plan: the console dependency graph (part B)

**Status as of 2026-11-10: in progress, 19/32 merged; see [docs/status.md](../status.md) for the current table.** Each task needs the owner's explicit go, by name; answering a decision approved none, and the one exception is stated below (B5). This plan builds spec section 2 ([spec.md](spec.md)) as amended by [reconciliation.md](reconciliation.md); the reconciliation wins. It shares **no task** with [implementation-plan-output.md](implementation-plan-output.md) (D1350); the few **files** both plans touch are sequenced by the cross-plan table in [README.md](README.md). **The orchestrator slice (B4) and the endpoint slice (B5) are both written.** B4's dependency is on `main`: `docs/stateless-orchestrator/` has O0-O8, O10, O17 and O18 merged (O9 and O12 are open drafts), and **CG4 merged on 2026-11-10**, so of B4 only CG5 and CG6 are left. **B5 is approved: the owner asked for CG13 by name on 2026-11-11**, which waives D1358's "only if a measurement shows" condition, and the endpoint is split into **CG13a, CG13b and CG13c**. The folder's task count is 32 because of that split — 15 in part A, **17 here** — and the merged count does not move with it: CG13 was one of the eleven left, and it is three of the thirteen now.

**The owner's answers that shaped it (2026-11-06, all the recommendation):** D1356 keep the plan scope and add `fugaro graph --json` with a coordinator scope that needs no checkout, source-qualified task keys, dedupe only within one namespace, orchestrator scopes when built; D1357 the CLI reads the `orchestrator/` RTDB subtree directly plus the coordinator view; D1358 local orchestrators are found through the daemon's RTDB node and its local cache file, no PID registry and no HTTP endpoint in the first slice; D1359 the trust rules apply if the endpoint is built later; D1360 `graph --watch --json` prints whole snapshots with `stream_id`, a rising `seq` within one child, `source_state` and a periodic keep-alive; D1361 `fugaro graph` and `fugaro orchestrator ls`; D1362 `fugaro graph` lives in `ee/` through `RegisterCommand`, the daemon and `orchestrator ls` stay core.

**Rulings of 2026-11-06 (reconciliation section 3a, all the recommendation):** T1 the orchestrator scopes (CG4-CG6) wait for the stateless plan's reader, so the first graph slice is the coordinator scope only; T2 the CLI reads `orchestrator/` with the person's credential and CG6 is a pin test, no new grant (viewer-role coverage cannot be checked by a rules test: it is the owner-run **Check 314**, only when B4 is scheduled; UNVERIFIED until then); T3 a CLI without the `ee` tag keeps the plan scope and gets a core stub (CG3s). **Assumption still open (not ruled):** T4(a) the reporter decides colour itself (part A); T5(a) a local scope streams the daemon's node and falls back to polling its cache file. **Reading of D1356:** the plan scope stays what it is today, the console's existing `plan.status` read; `fugaro graph` carries the coordinator scope now and the orchestrator and local scopes later. The coordinator view has **no dependency edges** (F17), so the coordinator scope draws nodes grouped by release; edges come with the orchestrator scopes' `depends_on`.

## Conventions every task follows

`CLAUDE.md`, `.claude/rules/console.md` and `.claude/rules/ee.md` (every new `ee/` file starts with the two-line SPDX notice; nothing outside `ee/` imports it; the core builds with `ee/` deleted), and `.claude/rules/credentials-service.md` is **not** touched. Hermetic tests (`internal/gcpfake` has an RTDB fake: `internal/gcpfake/rtdb.go`), no secret-shaped literal, no live cloud action by an agent, comments explain why. **Console:** Node 22, exact-pinned dependencies (none added), `npm run typecheck` and `npm run test:unit -- --maxWorkers=2 --testTimeout=60000`; a new command or argument is security-sensitive (independent review, pinning tests in `console/test/unit/`, the section 5.1 table in `docs/design/desktop-console.md`, whose "Fifteen rows" sentence (section 2.1) a new row changes; no Go docs test pins the count, but `console/test/unit/ipc.test.ts` pins the operation names and methods, "the fifteen names"); everything from the CLI is untrusted text through `src/shared/sanitize.ts` `TEXT_CLASSES`; a section paints when its data arrives, independent reads in parallel, the sequence token covers each section (the owner's rule of 2026-10-31); the console draws no write; goldens are never re-recorded without the owner. **A field never changes meaning:** the new documents are new shapes with their own version.

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| H1 | The scope argument reaches argv from the renderer: the CLI refuses any value not matching a closed pattern (`all`, `coordinator`, `orchestrator:<id>`, `local:<id>`, id `^[a-z0-9][a-z0-9-]{0,39}$`) before reading anything, and the console only sends a value a previous read listed | CG3, CG9a |
| H2 | Every string a node carries (title, repo, branch, note) is untrusted: sanitised in the CLI's document **and** by the console's class before the renderer | CG1, CG9a |
| H3 | `source_state` per scope: a scope that is down is a state, not an exit; a refused read is terminal and says which command | CG7 |
| H4 | `seq` rises within one child; a new `stream_id` is a new snapshot; a reconnect sends a full snapshot and increments `seq`; a keep-alive prints on a timer so an orphaned child exits on its next write | CG7 |
| H5 | The stream respects `watch --json`'s guarantees: one document per change, a one-second floor, a slow reader blocks and never drops a document, a closed stdout ends the child | CG7 |
| H6 | RTDB cost: one subscription per scope subtree, shared across the child's scopes; the console runs `graph --watch` only while the Graph tab is visible and counts it in `MAX_LIVE_FEEDS` (4) | CG7, CG11 |
| H7 | No run or token identity can read or write `orchestrator/` (T2(a)); the graph reads through the person's IAM credential | CG6 |
| H8 | The console never talks to RTDB and holds no token; it spawns the CLI through an exact argv row | CG9a, CG9b |
| H9 | The new scopes need no checkout; the plan scope keeps its checkout requirement and its card | CG10 |
| H10 | The graph draws no write, no URL it built itself, and no state as a colour alone | CG10 |
| H11 | The endpoint (CG13, approved 2026-11-11) is read-only, loopback only, token on every request, Host check, no CORS, 0600 registry with a start-time nonce, handshake plus lock-file liveness | CG13a, CG13b |

## Slices

- **B1 One-shot graph (ships useful alone):** CG3s core stub, CG1 document, CG2 coordinator source, CG3 `fugaro graph --json`, CG8 contract.
- **B2 Console, one-shot:** CG9a, CG10.
- **B3 Live:** CG7 snapshot stream, CG9b, CG11.
- **B4 Orchestrator scopes (schedulable: its dependency is on `main`):** CG5, CG6 (CG4 merged on 2026-11-10).
- **B5 Local endpoint (approved 2026-11-11):** CG13a, CG13b, CG13c.
- **Owner-run:** CG12.

## Tasks

### CG1. The graph document (pure, no I/O)

**Decisions:** D1356, D1360. **Findings:** F18, F19, F20. **Depends on:** none. **Reviewer:** standard.

- **Files (max 4):** `ee/graph/document.go` (new), `ee/graph/document_test.go` (new), `ee/graph/key.go` (new), `ee/graph/key_test.go` (new).
- **Interfaces produced:** `graph.Document{Version int (1); StreamID string; Seq uint64; GeneratedAt time.Time; Sources []SourceState; Nodes []Node; Edges []Edge; Warnings []string}`; `SourceState{Source string; State string ("up"|"down"|"stale"|"unreadable"); AsOf *time.Time; Reason string}`; `Node{Key string ("<source>:<id>"); Source string; Kind ("pull_request"|"run"|"task"); ID, Title, Repo, State, Release string; Updated *time.Time}`; `Edge{From, To string; Kind string}`; `graph.NodeKey(source, id)` and `graph.ParseScope(s) (Scope, error)` enforcing H1's pattern; every text field passes `safetext.Field` on construction (H2). Keys are unique **within one source**; nothing dedupes across sources (D1356).
- **Tests:** `TestKeyIsSourceQualified`, `TestScopePatternRefusesEverythingElse` (leading `-`, path separators, empty, uppercase, a 41-character id, `all:`, whitespace), `TestDocumentTextIsSanitised`, `TestDocumentRoundTripsAndIsStrict` (unknown fields are errors on decode), `TestNoCrossSourceDedupe`.
- **Command:** `go test ./ee/graph/ -count=1`
- **Red step:** the package does not exist.
- **Commit:** `ee/graph: the graph document, its source-qualified keys and its scope pattern`

### CG2. The coordinator source

**Decisions:** D1356, D1357. **Findings:** F16, F17. **Depends on:** CG1. **Reviewer:** standard.

- **Files (max 3):** `ee/graph/source_coordinator.go` (new), `ee/graph/source_coordinator_test.go` (new), `ee/graph/testdata/view.golden.json` (new: a stored view fixture) and `ee/graph/testdata/view.golden.json.license` (new: the licence sibling the repo's fixtures carry).
- **Interfaces:** consumes `coordinator.Store` and `coordinator.StoredProjectView`/`RepoView` (`ee/coordinator/state.go`) and the repository list the local config names (the CLI's `coordinatorRepos` rule); produces `[]Node` (a pull request with its joined runs, release from `Record`) and the source's `SourceState` (the sweep's stored time, stale when older than twice the sweep period, 15 minutes). **No edges**: the view has none.
- **Tests:** `TestCoordinatorNodesPerRepoAndRelease`, `TestCoordinatorSourceStateIsStaleAfterTwoSweeps`, `TestCoordinatorUnstoredRepoIsAStateNotAnError` (the contract's `stored: false` case), `TestCoordinatorTextIsSanitised`.
- **Command:** `go test ./ee/graph/ -run Coordinator -count=1`
- **Red step:** `ee/graph.CoordinatorSource` is undefined.
- **Commit:** `ee/graph: the coordinator's stored view as graph nodes`

### CG3s. The core stub for `graph` (T3)

**Decisions:** D1362. **Depends on:** none. **Reviewer:** standard (the sentence is D522's, unchanged).

- **Files (max 3):** `internal/cli/extend.go` (add `graph` to `paidCommandNames`, so a build without the `ee` tag shows `newPaidStub`: "graph is part of Fugaro Subscription Edition; this build does not include it", non-zero), `internal/cli/extend_test.go` (or the existing stub test file, named at launch), `internal/cli/docs_website_cliref_test.go` (only if its pin on the paid names changes; **after CL4**) and `website/content/cli-reference.md` (regenerated: the stub is a visible command, so the reference changes; **after CL4**).
- **Interfaces:** the stub parses no flags, so `fugaro graph --watch --json` reaches the message; the console treats that refusal as "this CLI has no graph" and keeps the plan scope (CG10).
- **Tests:** `TestUntaggedBuildHasTheGraphStub`, `TestGraphStubRefusesWhateverTheFlags`, and the existing paid-command tests.
- **Command:** `go test ./internal/cli/ -run 'Paid|Stub|TestCliReferenceNamesEveryCommandAndFlag|TestCliReferenceInventsNothing' -count=1 && go test ./ee/clidoc/ -run TestCliReferenceIsCurrent -count=1` (the stub is a visible command, so `website/content/cli-reference.md` is regenerated in this task unconditionally and that test is the one that goes red without it)
- **Red step:** an untagged build answers `unknown command "graph"`.
- **Commit:** `cli: a core stub for fugaro graph in a build without the ee tag`

### CG3. `fugaro graph --json` (SEC)

**Decisions:** D1356, D1361, D1362. **Findings:** F21, F25, F26. **Depends on:** CG1, CG2 (CG3s is independent and ships the untagged build's stub). **Reviewer: `opus + mandatory independent review`** (an argument that reaches a source choice; a new command in the console's allow-list path).

- **Files (max 7):** `ee/graph/cli/graph.go` (new), `ee/graph/cli/graph_test.go` (new), `ee/register/graph.go` (new), `ee/register/graph_test.go` (new), `ee/clidoc/` expectations and `website/content/cli-reference.md` (regenerated for `graph`; **after CL4 and CG3s per the README's cross-plan table**), `docs/design/v1.md` command table row (**before the output plan's CL11**). `internal/cli/extend.go` is CG3s's, not this task's.
- **Interfaces:** `fugaro graph [--source all|coordinator|orchestrator:<id>|local:<id>] --json`, default `all`; the cloud flags `runs ls` takes; with no `--json` a short text table. `all` is the union of the scopes that exist; `orchestrator:` and `local:` are refused with "no orchestrator is installed in this build" until CG4/CG5 register readers (an honest refusal, not an empty graph). The command needs **no checkout** and reads RTDB through the person's own credential.
- **Tests:** `TestGraphRefusesAnInvalidScopeBeforeAnyRead` (H1), `TestGraphAllIsTheCoordinatorToday`, `TestGraphJSONIsOneVersionedDocument`, `TestGraphNeedsNoCheckout`, `TestGraphRegisteredOnce`.
- **Command:** `go test ./ee/graph/... ./ee/register/ -count=1 && go test -tags ee ./cmd/... -run Graph -count=1` (the tag command named at launch from the existing `ee_on.go` tests) and `go test ./internal/cli/ -run 'TestCliReferenceNamesEveryCommandAndFlag|TestCliReferenceInventsNothing|Paid' -count=1 && go test -tags ee ./ee/clidoc/ -run TestCliReferenceIsCurrent -count=1`.
- **Red step:** `fugaro graph` is an unknown command.
- **Mutations:** loosen the scope pattern: `TestGraphRefusesAnInvalidScopeBeforeAnyRead` fails; let a `--source` value reach a path or a flag: the same test fails.
- **Commit:** `ee/graph: fugaro graph --json, the coordinator scope, no checkout`

### CG8. The console contract for `graph`

**Depends on:** CG1. **Reviewer:** standard.

- **Files (2):** `console/docs/json-contract.md` (a new section for `fugaro graph --json` and the stream: every field, its absent and null cases, the `seq`, `stream_id`, keep-alive and `source_state` rules of H3 to H5), the Go docs test that pins it if it counts sections (`internal/cli/docs_console_boundary_test.go`, edited only if its census changes).
- **Tests:** `go test ./internal/cli/ -run 'DocsConsole|DocsDesktopConsole' -count=1`.
- **Red step:** none beyond the docs test: it turns red if the section is added and a pinned count is not updated.
- **Commit:** `console/docs: the graph document and its stream`

### CG7. The snapshot stream `graph --watch --json` (SEC)

**Decisions:** D1360. **Findings:** F11, F12, F19, F20. **Depends on:** CG3. **Reviewer: `opus + mandatory independent review`** (a long-lived child, resource bounds).

- **Files (max 5):** `ee/graph/stream.go` (new), `ee/graph/stream_test.go` (new), `ee/graph/cli/watch.go` (new), `ee/graph/cli/watch_test.go` (new), `ee/graph/cli/graph.go` (**CG3's file: this task runs after CG3, so one writer per level**).
- **Interfaces:** `--watch` with `--json`: a first full snapshot within a bounded time, then one **whole document per change**, floor one second, each with `stream_id` (random per child), `seq` (rises within the child), `generated_at`, `sources[].state`; a keep-alive document every 30 seconds with the same `stream_id` and the next `seq` when nothing changed; a withdrawn RTDB stream is `source_state: down`, then a reconnect sends a full snapshot (`rtdb.Client.Stream` already restarts with a full `put`); a closed stdout ends the child; the child subscribes to **one** `coordinator/` subtree per scope (H6).
- **Tests:** `TestFirstSnapshotThenOneDocumentPerChange`, `TestSeqRisesAndStreamIDIsStable`, `TestKeepAliveWhenQuiet`, `TestReconnectSendsAFullSnapshotAndBumpsSeq`, `TestSourceDownIsAStateNotAnExit`, `TestClosedStdoutEndsTheChild`, `TestSlowReaderBlocksAndNeverDrops`, `TestOneSubscriptionPerScope` (fake RTDB counts connections), `TestRefusedReadIsTerminalAndNamesTheCommand`.
- **Command:** `go test ./ee/graph/... -run 'Stream|Watch' -count=1 -race`
- **Red step:** `--watch` is an unknown flag.
- **Mutations:** drop the keep-alive: `TestKeepAliveWhenQuiet` fails; drop the sequence bump on reconnect: `TestReconnectSendsAFullSnapshotAndBumpsSeq` fails.
- **Commit:** `ee/graph: graph --watch --json, whole snapshots with a sequence and a keep-alive`

### CG9a. The console's one-shot `graph.read` (SEC)

**Findings:** F9, F21, F22. **Depends on:** CG3, CG8. **Reviewer: `opus + mandatory independent review`** (the spawn and IPC allow-lists).

- **Files (max 12):** `console/src/main/spawn.ts` (a `SPAWN_COMMANDS` row `graph.read`: words `['graph','--json']`, takesProject, one declared argument `source` of kind `value` whose value set is closed to `all`, `coordinator` and ids matching H1's pattern), `console/src/main/ipc.ts` (`CONSOLE_OPERATIONS`), `console/src/preload/index.ts` (the `EXPOSED_OPERATIONS` entry), `console/src/renderer/entry.ts` (the bridge interface, next to `planStatus`, and its `ask('graphRead', args)` wiring), `console/src/shared/schema.ts` (the closed decoder for the graph document), `console/src/source/source.ts` and `console/src/source/cli.ts` (the read and its cache cadence), `console/docs/json-contract.md` **not edited here** (CG8's), `docs/design/desktop-console.md` (the section 5.1 table row and the "Fifteen rows" sentence of section 2.1), `console/test/unit/spawn.test.ts`, `console/test/unit/ipc.test.ts` (the pinned names and methods grow by one), `console/test/unit/graph-read.test.ts` (new). **At most 12 files.**
- **Interfaces:** a read named `graph.read` through the exact argv `fugaro graph --json [--source <v>]`; the `source` value is validated in the main process against the closed pattern **and** against the set the previous `graph.read` listed; everything from the CLI is sanitised (`TEXT_CLASSES`); a CLI without `graph` is a refusal that names the command; `hidesView` rules unchanged.
- **Tests:** `spawn.test.ts` `graph.read argv is exactly the declared row`, `a source value outside the pattern is refused before any spawn`, `the child env is exactly CHILD_ENV_KEYS`, `graph.read never reaches RTDB or a token`; `graph-read.test.ts` `decodes the v1 document strictly`, `unknown version is a reduced view`, `text goes through the sanitiser`; and the Go `go test ./internal/cli/ -run 'DocsDesktopConsole|DocsConsoleBoundary' -count=1` for the table row.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 -t 'graph'`
- **Red step:** the operation does not exist: the argv test fails.
- **Commit:** `console: the graph read, through an exact argv row`

### CG10. The Graph screen: a scope dropdown beside the plan Graph, no checkout for the new scopes (renderer)

**Decisions:** D1356. **Findings:** F10, F18, F22, F24. **Depends on:** CG9a. **Reviewer:** standard (renderer; no spawn or IPC change).

- **Files (max 7):** `console/src/renderer/screens/graph.ts` (the scope control and the coordinator scope's drawing; the plan scope's code is untouched), `console/src/renderer/screens/nocheckout.ts` (the card now only means "the **plan** scope needs a checkout"; with no checkout the Graph opens on the coordinator scope), `console/src/renderer/graph/model.ts` (nodes from the new document, source-qualified keys), `console/src/renderer/section.ts` **read only** (consumed), `console/src/renderer/theme/screens.css` (the control), `console/test/unit/graph.test.ts`, `console/test/unit/nocheckout.test.ts` (new: none exists today). **This task does not edit `entry.ts`: it consumes the `graphRead` bridge method CG9a added.**
- **Interfaces:** consumes `graph.read`; the dropdown lists `All (coordinator)`, then `Plans of this checkout` (the existing read), and later the orchestrator scopes the document lists; each scope is its own section (own busy state, refusal, sequence token); `All` paints without a checkout; no state is a colour alone; the node's pull-request number stays text (no URL, `GRAPH_PR_NO_LINK_REASON`).
- **Tests:** `no checkout opens the coordinator scope`, `the plan scope with no checkout still shows its card`, `a scope change is answered in its own frame (busy mark, figures kept)`, `a late answer for a changed scope is dropped (sequence token)`, `a CLI without graph shows the plan scope and a refusal sentence (T3)`, `no node carries a colour-only state`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 -t 'graph|nocheckout'`
- **Red step:** with no checkout the Graph shows only the card: `no checkout opens the coordinator scope` fails.
- **OWNER-RUN:** re-recording the Graph and Coordinator goldens (`npx playwright test <spec> --update-snapshots` on the owner's Mac): the owner's, never an agent's.
- **Commit:** `console: the Graph's scope dropdown, and the coordinator scope with no checkout`

### CG9b. The console's `graph.watch` subscription (SEC)

**Findings:** F11. **Depends on:** CG7, CG9a. **Reviewer: `opus + mandatory independent review`** (a long-lived child under the spawn allow-list).

- **Files (max 11):** `console/src/main/spawn.ts` and `console/src/main/ipc.ts` (**after CG9a: the same files, a later level**), `console/src/preload/index.ts` and `console/src/renderer/entry.ts` (**after CG9a**: the `graphWatch` entry and bridge method), `console/src/source/cli.ts` (`subscribeLive`-style subscription for the graph feed, counted in `MAX_LIVE_FEEDS`), `docs/design/desktop-console.md` (the table row and the sentence's count), `console/test/unit/spawn.test.ts`, `console/test/unit/ipc.test.ts`, `console/test/unit/graph-watch.test.ts` (new).
- **Interfaces:** the exact argv `fugaro graph --watch --json [--source <v>]`; the feed keeps the latest document per `stream_id`, ignores a `seq` lower than the last within one `stream_id`, treats a new `stream_id` as a new snapshot, and stops the child when the Graph tab is hidden (H6); the SIGTERM, then SIGKILL shutdown of the existing children applies.
- **Tests:** `graph.watch argv is exactly the declared row`, `latest document wins, a stale seq is ignored`, `a new stream id is a new snapshot`, `the child is stopped when the tab is hidden`, `the feed counts toward MAX_LIVE_FEEDS and evicts the oldest`, `close() stops the graph child`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 -t 'graph'`
- **Red step:** the operation does not exist.
- **Commit:** `console: the graph's live feed, a long-lived child within the feed ceiling`

### CG11. The Graph consumes the feed

**Depends on:** CG9b, CG10 (**the same `graph.ts`: sequenced after CG10**). **Reviewer:** standard.

- **Files (max 4):** `console/src/renderer/screens/graph.ts`, `console/src/renderer/graph/model.ts`, `console/test/unit/graph.test.ts`, `console/test/unit/graph-live.test.ts` (new). Does not edit `entry.ts` (CG9b added `graphWatch`).
- **Interfaces:** consumes `graph.watch` while the coordinator scope is visible; the one-shot `graph.read` remains the first paint and the fallback when the feed refuses; `source_state` is drawn as words (up, stale, down) with the as-of time.
- **Tests:** `a snapshot repaints the section only`, `a down source shows its state and keeps the last figures dimmed`, `the feed falls back to the read when the child refuses`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 -t 'graph'`
- **Red step:** the screen never subscribes.
- **Commit:** `console: the Graph is live`

### CG4. `fugaro orchestrator ls --json` (B4; merged 2026-11-10, #2207)

**Decisions:** D1361, D1358. **Depends on:** **the stateless-orchestrator plan's state schema and reader (O0, merged on `main` on 2026-11-07, and O2 on 2026-11-08, both in `docs/stateless-orchestrator/implementation-plan.md`) and its `fugaro orchestrator` group (O5's `internal/cli/orchestrator_cmd.go` and O8's `run`, both merged)**. **Reviewer:** standard. **Merged on `main` on 2026-11-10, #2207**: `internal/cli/orchestrator_ls.go` and `internal/orchestrator/list.go` are it, so B4's remaining tasks are CG5 and CG6.

- **Files (max 4):** `internal/cli/orchestrator_ls.go` (new), `internal/cli/orchestrator_ls_test.go` (new), the cache reader in `internal/orchestrator/` (that plan's package), `console/docs/json-contract.md` **not here** (a later CG8 amendment).
- **Interfaces:** lists the orchestrators whose RTDB node exists (central and local, with `kind`, `project`, `lease_holder`, `updated_at`) and the local daemon's cache file(s) under `$XDG_CACHE_HOME/fugaro/orchestrator/<id>.json` (found through `localcfg.UserDir(..., DirCache)`), with the cache's freshness stamp; no PID registry, no port (D1358).
- **Tests:** `TestOrchestratorLsListsNodesAndCaches`, `TestOrchestratorLsStaleCacheIsMarked`, `TestOrchestratorLsJSONVersioned`.
- **Command (when scheduled):** `go test ./internal/cli/ -run 'TestOrchestratorLs' -count=1`
- **Red step:** `fugaro orchestrator ls` is an unknown subcommand of the group.
- **Commit:** `cli: fugaro orchestrator ls`

### CG5. The orchestrator and local scopes (B4)

**Depends on:** CG4, CG3. **Reviewer:** `opus + mandatory independent review` (a reader of a node that holds task titles and notes). 

- **Files (max 4):** `ee/graph/source_orchestrator.go` (new), `ee/graph/source_orchestrator_test.go` (new), `ee/graph/cli/graph.go` (**after CG7 and CG3**), `ee/graph/stream.go` (**after CG7**; both are later-level edits).
- **Interfaces:** `orchestrator:<id>` reads that node (tasks, `depends_on` as edges, status as state); `local:<id>` reads the cache file and, when watching, the node, polling the cache when the node is unreachable (T5(a)); `all` becomes the union with source-qualified keys.
- **Tests:** `TestOrchestratorTasksAndEdges`, `TestLocalScopeFallsBackToTheCache`, `TestUnknownOrchestratorIsRefused`, `TestDependsOnEdgesStayInsideOneOrchestrator`.
- **Command (when scheduled):** `go test ./ee/graph/... -run 'Orchestrator|LocalScope' -count=1`
- **Red step:** `graph --source orchestrator:<id>` is refused with "no orchestrator is installed in this build" (CG3's honest refusal).
- **Commit:** `ee/graph: the orchestrator and local scopes`

### CG6. A rules test: `orchestrator/` is unreadable and unwritable by every run and token identity (B4, SEC)

**Findings:** T2. **Depends on:** the stateless plan's rules task for `orchestrator/` (**O2**, merged on `main` on 2026-11-08, #1945, `5053cbce`). **Reviewer: `opus + mandatory independent review`.**

- **Files (max 2):** `internal/budget/rules/orchestrator_test.go` (new, modelled on `coordinator_test.go`), `internal/budget/rules/emulator_orchestrator_test.go` (**created by the stateless plan's O2**; this task adds cases to it, tag `firebase`).
- **Tests:** `TestOrchestratorNodeIsDeniedToRunAndTokenIdentities`, `TestNoClientWriteUnderOrchestrator`.
- **Command (when scheduled):** `go test ./internal/budget/rules/ -run 'Orchestrator' -count=1` (and the `firebase`-tagged emulator run, owner or CI as that plan says).
- **Red step:** the new cases fail on a rules document that grants a run identity a read of `orchestrator/`.
- **Mutation:** add a read for a run identity: the first fails. **This is a pin, not a grant (T2(a)).** It cannot check IAM: whether `roles/firebasedatabase.viewer` covers `orchestrator/` is **UNVERIFIED** and is the owner-run **Check 314** below.
- **Commit:** `rules: pin that no run or token identity touches orchestrator/`

### CG13. The local endpoint (B5; approved 2026-11-11, SEC)

**Decisions:** D1358 (its alternative), D1359 (the trust rules). **Findings:** F13, F14, F23, F28. **Depends on:** **CG5** (the local scope it serves, so the endpoint improves a read that works without it) and **the daemon of `docs/stateless-orchestrator/`, which is on `main`**: `internal/orchestrator/` — `state.go` and `patch.go` (O0), `store.go`, `lease.go` and `audit.go` (O3), `tick.go` and `runview.go` (O7), `cache.go` (O8) — and the daemon command itself, `internal/cli/orchestrator_run.go` and `internal/cli/orchestrator_launcher.go` (O8), plus `internal/orchestrator/list.go` and `internal/cli/orchestrator_ls.go` (CG4, merged on 2026-11-10), and `internal/localcfg/platformdirs.go` for the directory the registry sits in. `internal/orchestrator/service/` (O9, an open draft) is **not** needed: the endpoint is started by the daemon process, never by the unit. macOS and Linux only (the daemon is, D1232). **Reviewer: `opus + mandatory independent review`** (a listening socket, a token on disk and a liveness rule).

**The owner asked for it by name on 2026-11-11, so the endpoint is built.** D1358 left it to "only if a measurement shows the cache's per-tick freshness is not enough", and that measurement is **waived rather than met**: the owner's request is the reason, and "NOT approved" no longer describes this slice. What is approved is the **endpoint**; the spec's PID-and-port registry is still refused (D1358), and the daemon's node and its cache file stay the registry for every read but freshness (CG4's `orchestrator ls` reads them, not a port).

**D1359's ten rules are the acceptance, each a named test, and each belongs to exactly one slice:** CG13a's seven — `TestEndpointBindsLoopbackPortZero`, `TestTokenOnEveryRequestIncludingTheStream`, `TestConstantTimeTokenCompare`, `TestRefusesAHostOtherThanTheLiteralAddress`, `TestNoCorsHeaders`, `TestEndpointIsReadOnly`, `TestTokenInTheQueryStringIsRefused` — and CG13b's three — `TestRegistryFileModeAndNonce`, `TestLivenessIsHandshakePlusLockNotPID`, `TestStaleFileRemovedOnlyByTheReaderThatFoundItDead`. CG13c rewires the local scope and carries its own tests.

**OWNER-RUN Check 313** (a crash, a reboot and a PID reuse on a real machine) is named in CG12's table below, and no agent runs any step of it.

#### CG13a. The endpoint server and its token rules (SEC)

**Decisions:** D1359. **Findings:** F23. **Depends on:** CG5 (B5 is built behind the local scope) and the daemon's merged packages above. **Reviewer: `opus + mandatory independent review`** (a listening socket and a token).

- **Files (max 4):** `internal/orchestrator/endpoint/server.go` (new), `internal/orchestrator/endpoint/server_test.go` (new), `internal/orchestrator/endpoint/token.go` (new: the mint and the compare), `internal/orchestrator/endpoint/token_test.go` (new). The package is new and this slice is **inert**: nothing outside its tests starts it (CG13b does, at the daemon's start-up).
- **Interfaces produced:** `endpoint.Start(ctx, Options{ID string; Snapshot func() (orchestrator.State, time.Time, bool); Log func(error)}) (*Server, error)`, `Server.Addr()`, `Server.Token()`, `Server.Close(ctx)`. One listener on **`127.0.0.1:0`** (`net.Listen("tcp", "127.0.0.1:0")`: the kernel's port is the only one, never a fixed port, and there is no other address to bind), one 32-byte `crypto/rand` token per start (`internal/gateway/gateway.go`'s pattern). Two routes and no third: `GET /state` (one JSON document — the daemon's last tick's state, marshalled with `orchestrator.State`'s own JSON tags, with the server time of that tick beside it, so the local scope decodes the endpoint with the same code as the node and the document carries its own `schema_version`) and `GET /state/stream` (SSE: a whole document per tick whose state moved; the `stream_id`, `seq` and keep-alive vocabulary stays the CLI's own stream, D1360). The token travels in **one header** (`Authorization: Bearer <token>`) on **every** request including the stream; a `?token=` is refused, and that refusal prints the path and never the query, so a token cannot reach a log; the compare is `subtle.ConstantTimeCompare` over the raw bytes; any `Host` other than the literal `127.0.0.1:<port>` — `localhost`, another name, an IPv6 spelling, none at all — is refused **before** the token is read; **no CORS header of any kind** on an answer or a refusal, and `OPTIONS` is a refusal; every method but `GET` is `405`; a caller before the first tick gets `503` and a reason, which is a state and not an exit (H3). The server holds no `Store`, no `Node`, no launcher and no filesystem, so read-only is a property of what it can reach rather than a rule it remembers (F23's "what a hostile local process of the same user can do").
- **Tests:** the seven above, plus `TestNoSnapshotBeforeTheFirstTickIsAState` and `TestTheRefusalNeverCarriesTheToken`.
- **Command:** `go test ./internal/orchestrator/endpoint/ -run 'Endpoint|Token|Cors|Host' -count=1 -race`
- **Red step:** the package does not exist, so the seven fail to build.
- **Mutations:** read the token from the query as well: `TestTokenInTheQueryStringIsRefused` fails; answer a `Host: localhost:<port>`: `TestRefusesAHostOtherThanTheLiteralAddress` fails; give the server one writer route: `TestEndpointIsReadOnly` fails; check the token on `/state` but not on `/state/stream`: `TestTokenOnEveryRequestIncludingTheStream` fails.
- **Commit:** `feat(orchestrator): the local endpoint, loopback only and a token on every request (CG13a)`

#### CG13b. The registry file, its liveness, and the daemon that serves (SEC)

**Decisions:** D1359. **Findings:** F5, F23, F28. **Depends on:** CG13a. **Reviewer: `opus + mandatory independent review`** (a token on disk and a liveness rule a PID cannot satisfy).

- **Files (max 6):** `internal/orchestrator/endpoint/registry.go` (new: the file's shape, its write and its read), `internal/orchestrator/endpoint/registry_test.go` (new), `internal/orchestrator/endpoint/lock_flock.go` and `internal/orchestrator/endpoint/lock_other.go` (new: the exclusively held lock, with `internal/infra/workdirlock_flock.go`'s build tags — `unix && (!solaris || illumos) && !aix` — and its stated no-op sibling for the same reason), `internal/cli/orchestrator_run.go` (edit: start the endpoint and write the registry once, at start-up, and remove the file on the clean shutdown the daemon already performs), `internal/cli/orchestrator_run_test.go` (edit).
- **Interfaces:** one file per orchestrator, `<UserDir state>/fugaro/run/<id>.json` (`localcfg.UserDir(getenv, goos, localcfg.DirState)`, the way `orchestrator.CacheDir` places the cache; F23: a runtime directory is not a state directory, and macOS has none) — `<state>/fugaro/run` is created mode **0700** and `Lstat`ed — not a symlink, owned by the effective uid — and the file is `O_CREATE|O_EXCL|O_NOFOLLOW` at mode **0600**, written temp-file-then-rename like the cache, F6a's rule — carrying the id, the pid, the literal `127.0.0.1:<port>`, the token, the **start-time nonce** and the start time. The nonce is 32 random bytes minted beside the token and never reused: it is what makes the file's identity a value rather than a number. Liveness is a **handshake plus an exclusively held lock file, never a PID**: the daemon holds `<id>.lock`'s `flock` for its life, so the kernel drops it on death (a SIGKILL and a power cut alike), and a reader proves a live endpoint by connecting to the literal address, sending the token from the file and reading back the id and the nonce it wrote — a reused pid, or a port another process took over, fails the handshake and the lock both. A file that fails both is removed **only by the reader that read it and found it dead**: nothing sweeps the directory, the daemon never removes another process's file, and the daemon's own clean exit removes its own. A registry that cannot be written is logged and never fails the start-up or a tick (F28: the daemon creates and migrates its own local files, so `fugaro update` still ships no operator step, and a machine whose home is read-only serves no endpoint and reads the node as before).
- **Tests:** `TestRegistryFileModeAndNonce`, `TestLivenessIsHandshakePlusLockNotPID`, `TestStaleFileRemovedOnlyByTheReaderThatFoundItDead` (the ten's three), plus `TestRunServesAndRemovesItsRegistryOnTheWayOut` and `TestARegistryThatCannotBeWrittenNeverFailsAStart`.
- **Command:** `go test ./internal/orchestrator/endpoint/ -run 'Registry|Liveness|Stale' -count=1 && go test ./internal/cli/ -run 'TestRun|TestDaemon' -count=1`
- **Red step:** the daemon starts no listener and writes no file: `TestRegistryFileModeAndNonce` and `TestRunServesAndRemovesItsRegistryOnTheWayOut` fail.
- **Mutations:** decide liveness from the pid alone: `TestLivenessIsHandshakePlusLockNotPID` fails (a live process that is not the daemon reads as an orchestrator); write the file 0644, or skip the nonce: `TestRegistryFileModeAndNonce` fails; remove a dead file from a sweep of the directory: `TestStaleFileRemovedOnlyByTheReaderThatFoundItDead` fails.
- **Commit:** `feat(orchestrator): the endpoint's registry file, its nonce and its handshake liveness (CG13b)`

#### CG13c. `fugaro graph`'s local scope reads the endpoint

**Decisions:** D1358, D1359. **Findings:** F14, F21, F23. **Depends on:** CG13a, CG13b, and **CG5** — **`ee/graph/source_orchestrator.go` and `ee/graph/stream.go` are CG5's files, and this task is a later level on both** (one writer per level). **Reviewer: `opus + mandatory independent review`** (it holds the token and decides which document the local scope draws).

- **Files (max 5):** `internal/orchestrator/endpoint/client.go` (new: the reader — lookup, handshake, the read and the stream), `internal/orchestrator/endpoint/client_test.go` (new), `ee/graph/source_orchestrator.go` (**CG5's file**: `local:<id>` tries a live endpoint first), `ee/graph/source_orchestrator_test.go` (**CG5's file**), `ee/graph/stream.go` (**CG5's and CG7's file**: the local scope's live source).
- **Interfaces:** `endpoint.Client{Getenv func(string) string; GOOS string; HTTP *http.Client}` with `Lookup(ctx, id) (Live, bool, error)` — read the registry file, refuse a file that is not 0600 in a directory that is not 0700 rather than read it, take the lock, handshake, and remove the file only here, and only when both checks failed — `Get(ctx, live) (orchestrator.State, time.Time, error)` and `Stream(ctx, live, func(orchestrator.State, time.Time) error) error`, all three sending the token in the header and never in a URL. The token is never printed, never in an error and never in a document (`safetext` fences what the endpoint sends, H2). `local:<id>` prefers a **live** endpoint, then the node, then the cache — T5(a), amended 2026-11-11 — so a machine whose daemon predates the endpoint, or is not running, reads exactly what CG5 built. The scope id is H1's pattern (CG1's `graph.ParseScope`) while the daemon's id is the project's name, which `internal/orchestrator/validate.go`'s `agentRE` (`^[a-z0-9][a-z0-9_-]{0,63}$`) governs; where the two differ the scope is refused before any read, and the limit is **named rather than widened**: a project whose name carries `_`, or is longer than 40 characters, has no local scope. **The endpoint adds no shape**: it serves the node's own document, so one decoder serves the node, the endpoint and the cache, and a version the release does not read is refused as it is there.
- **Tests:** `TestLocalScopeUsesTheLiveEndpoint`, `TestLocalScopeFallsBackToTheNodeAndThenTheCache`, `TestLocalScopeRefusesAnIdOutsideThePatternBeforeAnyRead` (H1, with an endpoint live beside it), `TestTheTokenIsNeverPrintedOrLogged`, `TestTheEndpointStreamFeedsTheWatch`.
- **Command:** `go test ./internal/orchestrator/endpoint/ -run Client -count=1 && go test ./ee/graph/... -run 'LocalScope|LocalEndpoint' -count=1`
- **Red step:** with a daemon serving beside it, `local:<id>` still polls the cache: `TestLocalScopeUsesTheLiveEndpoint` fails.
- **Mutations:** send the token in the query string: `TestTheTokenIsNeverPrintedOrLogged` fails; treat a file whose handshake failed as live: `TestLocalScopeUsesTheLiveEndpoint` fails (the scope draws a process that is not the daemon).
- **Commit:** `ee/graph: the local scope reads the endpoint the daemon serves (CG13c)`

### CG12. Owner-run checks (no agent performs any step)

| Check | Measures | Who | What the owner types |
| --- | --- | --- | --- |
| **312** | The console's Graph against a real project: the scope dropdown, no checkout, live updates, a stale source, and a hard-killed console leaving no orphan `fugaro graph` child | the owner | open the signed console on a project with the coordinator on; `kill -9` the console; `pgrep -fl 'fugaro graph'` a minute later |
| **313** | CG13's endpoint on a real machine: the registry file's mode and nonce, liveness by handshake plus lock rather than a pid, and a crash, a reboot and a pid reuse | the owner | start a daemon (`fugaro orchestrator run`) and note its project's id; `ls -l "$XDG_STATE_HOME/fugaro/run"`; read `fugaro graph --source local:<id>` once; `kill -9 <pid>`; read the scope again (it must read the node, remove the dead file and never touch a live process that has taken the pid); `ps -o pid,command` until another process holds that pid and read the scope once more; reboot, start a daemon again and repeat |
| **314** | Only when B4 is scheduled: a person credential with `roles/firebasedatabase.viewer` reads an `orchestrator/` node (T2: UNVERIFIED today) | the owner | named when the orchestrator slice is scheduled |

## Release actions

None of CG1-CG11 needs a post-deploy step: `fugaro graph` reads what the existing `coordinator status --from-store` reads with the same IAM role, and the console reads it through the CLI. **The release's `### For operators` is "run `fugaro update`"** and nothing more (the `CLAUDE.md` HARD RULE). The orchestrator slice (CG4-CG6) adds the `orchestrator/` rules pin; any rules change the stateless plan ships is that plan's release action, not this one's. **The endpoint (CG13) adds no step either**: the daemon starts it, writes its registry under the state directory and removes it on the way out, so `fugaro update` neither installs nor migrates anything for it (F28), and a machine that never runs a daemon never has a file there. The console releases on its own tag, `console-vX.Y.Z`, with its notes in `docs/releases/console/`.

## Parallel execution

**Edges:** CG3s depends on nothing; CG2, CG8 depend on CG1; CG3 on CG1, CG2; CG7 on CG3; CG9a on CG3, CG8; CG10 on CG9a (not on CG9b: it never edits `entry.ts`); CG9b on CG7, CG9a; CG11 on CG9b, CG10; CG4 on the stateless plan (merged 2026-11-10); CG5 on CG4, CG3, CG7; CG6 on the stateless plan; CG13a on CG5 and the daemon; CG13b on CG13a; CG13c on CG13a, CG13b, CG5.

| Level | Tasks (concurrent) |
| --- | --- |
| L0 | CG1, CG3s |
| L1 | CG2, CG8 |
| L2 | CG3 |
| L3 | CG7, CG9a |
| L4 | CG10, CG9b |
| L5 | CG11 |
| B4 (schedulable: its dependency is on `main`; CG4 merged 2026-11-10) | CG5, CG6 (independent of each other) |
| B5 (approved 2026-11-11) | CG13a, then CG13b, then CG13c (each after CG5; CG13c last) |

**Critical path** (CG3s is off it): CG1 → CG2 → CG3 → CG7 → CG9b → CG11 (then CG12, owner-run). Sequential by nature: the document before its sources and the command; the command before the stream and the console read; `spawn.ts` and `ipc.ts` take one writer per level (CG9a, then CG9b); `graph.ts` one (CG10, then CG11). **B5 hangs off the same path once CG5 has landed:** CG13a → CG13b → CG13c, then Check 313 (owner-run) — the slice is built on top of the local scope, not in front of it.

**Shared files: no level holds two writers of one file.**

| File | Writers, by level |
| --- | --- |
| `ee/graph/document.go` | CG1 (L0) |
| `ee/graph/cli/graph.go` | CG3 (L2), CG7 (L3), CG5 (B4) |
| `ee/graph/stream.go` | CG7 (L3), CG5 (B4, later), CG13c (B5, after CG5) |
| `ee/graph/source_orchestrator.go`, `ee/graph/source_orchestrator_test.go` | CG5 (B4), CG13c (B5, after CG5) |
| `console/src/main/spawn.ts`, `ipc.ts`, `console/src/preload/index.ts`, `console/src/renderer/entry.ts`, `console/test/unit/ipc.test.ts` | CG9a (L3), CG9b (L4); CG10 and CG11 never edit them |
| `docs/design/desktop-console.md` | CG9a (L3), CG9b (L4) |
| `console/test/unit/spawn.test.ts` | CG9a (L3), CG9b (L4) |
| `console/src/renderer/screens/graph.ts`, `renderer/graph/model.ts`, `test/unit/graph.test.ts` | CG10 (L4), CG11 (L5) |
| `console/docs/json-contract.md` | CG8 (L1) only |
| `internal/cli/extend.go`, `internal/cli/docs_website_cliref_test.go` | CG3s (L0) only |
| `ee/register/graph.go`, `ee/clidoc` expectations | CG3 (L2) only |
| `website/content/cli-reference.md`, `docs/design/v1.md` | **cross-plan**: see the README (CL4, CG3s, CG3, then CL11) |
| `console/src/source/cli.ts` | CG9a (L3), CG9b (L4) |
| `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_run_test.go` | CG13b (B5) only |
| `internal/orchestrator/endpoint/server.go`, `token.go` | CG13a (B5) only |
| `internal/orchestrator/endpoint/registry.go`, `lock_flock.go`, `lock_other.go` | CG13b (B5) only |
| `internal/orchestrator/endpoint/client.go` | CG13c (B5) only |

The tasks in one level that edit `docs/design/desktop-console.md` or a Go docs test: none (CG7 and CG9a at L3 touch disjoint files; CG9a is the only writer of the table at L3). **CG13a, CG13b and CG13c are three levels of one new package and write disjoint files**: the server and the token, the registry and the lock, the client.

## What is not in this plan

A write from the graph (drag, retry, halt); **any write or spawn through the endpoint (CG13 is read-only and loopback only)**; a fixed port or mDNS discovery (the endpoint binds port 0, D1359); deleting the plan scope; edges between sources; a new dependency (none is added); the renderer talking to RTDB.
