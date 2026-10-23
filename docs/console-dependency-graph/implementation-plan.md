# The console dependency graph — implementation plan

**Status: plan only; nothing is implemented; no task approved.** This plan builds
[spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)**, which wins where the
two disagree. Each task below needs the owner's explicit go; answering a decision is not approving
a task. Checked against `origin/main` at `cc50b5b23` on 2026-10-22.

**The owner's answers that shaped this plan (2026-10-22).**
- **GD1** (not the recommendation): wait for the coordinator's project record (0.11.0). So no
  roadmap file; the graph is per-plan and run-level now (GV1.6), and task-level across plans waits
  (GV4.1, blocked).
- **GD2** (not the recommendation): the console writes the roadmap; reverses D30, amends D22. The
  write (GV3.1, GV3.2) is **blocked on open tension (a)**: the record it would write does not exist
  until 0.11.0.
- **GD3** (not the recommendation): elkjs; reverses D9. GV2.2 is **blocked on the licence item
  (c)**.
- **GD4**: motion with text marks and a reduced-motion static fallback (GV2.4).
- **GD5**: a plan-task id recorded at launch (GV1.2).
- **GD6**: open work by default, a toggle for everything, merged/cancelled as a per-release
  counter (GV1.6, GV2.3).
- **GD7**: one streams data file, read by the graph and the release script (GV1.4, GV1.5); path
  amended to `docs/release-streams.yaml` (F29), owner to confirm (e).
- **GD8**: this folder's own prefixes.

## 1. Conventions every task follows

- **Go:** `gofmt -l .` clean, `GOFLAGS=-p=2 go vet ./...`, targeted tests with `-count=1
  -timeout 300s`, never the whole suite as the verification.
- **Console:** Node 22 (`console/package.json` `engines.node` is `22.23.3`); targeted
  `npx vitest run <file>` and `npm run typecheck` in `console/`; e2e through `npm run test:e2e --
  <spec>` (Playwright on the built app). Exact versions only (`console/test/unit/pinning.test.ts`).
- **The console's hard rules** (`docs/design/desktop-console.md` §5.1): the CLI is the only data
  plane; job- and repository-written text is rendered as text, never `innerHTML`, never an href;
  the spawn allow-list is exact argv; `connect-src 'none'` stays.
- **Styling:** `docs/design/console-styling.md` tokens, §3.5 (never colour alone), §4.3 keyboard,
  §4.4 forced colours.
- **Version skew:** every new JSON field is additive and optional; an older console ignores it, an
  older CLI lacks the command and the console shows the screen as not available (the existing
  `startup.compatibility` path), never an error loop.
- **Safety:** no secret-shaped literal in a test or fixture; no agent runs a live cloud action; the
  `belong` installation is never touched; live checks are owner-run (Check 123).
- **Docs tests** after any `docs/` edit: `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs' -count=1
  -timeout 300s`. Files under `docs/design/` and `docs/plans/` are dated records: amendments are
  dated notes beside the original text, never rewrites.

## 2. What this plan supersedes or amends

| Item | Effect |
| --- | --- |
| Console v2 **V2.2.3** (readiness, critical path) | **Absorbed** by GV1.1 (structure) and GV1.6 (readiness from runs). Same D26 answer. |
| Console v2 **V2.3.1** (graph on screen, four colours) | **Superseded** by GV2.3 and GV2.4. |
| Console v2 **V2.5.3** ("copy the change") | **Superseded** by GV3.2 once tension (a) is ruled (GD2). |
| Console v2 **V2.6.1** (version column) | **Amended**: the version becomes a release box (GV2.3); the stream comes from GV1.3; the source later moves to the coordinator's record (GD1), not to `fugaro project export` (F31). |
| Console v2 **V2.6.2** (dependency-aware warning) | **Kept** as is for the warning; its "pull it forward" half follows GV3.2 instead of V2.5.3. |
| Console v2 **V2.8.1** (skill writes the format) | **Kept**, extended by the `stream` field: GV1.7. |
| Coordinator **P6** (console project view) | **Unchanged in scope**; shares console files with GV2.1/GV2.3 (F38, a merge-time note). When P1 reaches `main`, GV4.1 reads release membership for PR-level nodes from it. |
| Coordinator **P4** (intent record) | **Proposed, not made**: carry GD5's plan-task id (open item (f)). |

## 3. Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| C1 | The renderer never sends a path; the main process supplies the installation's checkout | GV2.1 |
| C2 | The CLI reads graph artifacts only under the checkout's `docs/`, refuses a symlink that leaves it, caps the artifact count and keeps the 1 MiB per-artifact limit | GV1.6 |
| C3 | `plan_task` is a display-only join hint: shape-validated on write and on read, never an authorization input | GV1.2 |
| C4 | Layout runs in a worker and only when the graph's structure changes, never per watch frame | GV2.3 |
| C5 | The CSP string is unchanged; the worker is one exact `SERVED_FILES` entry; no eval | GV2.2 |
| C6 | Every coloured state carries a text mark; reduced motion gets a static style per state | GV2.3, GV2.4 |
| C7 | A cycle is a refusal, not a render (as V2.2.1 already does) | GV1.1 |
| C8 | Critical path is the longest chain by count; ties broken by id, so two consoles agree | GV1.1 |

## 4. Slices and tasks

### Slice 0 — the record

#### GV0.1 — Dated amendment notes in the console designs
- **Files:** `docs/design/desktop-console-v2.md` (dated notes beside D22, D29, D30 and F23),
  `docs/design/desktop-console.md` (dated note beside D9), `docs/design/console-styling.md` (§3.9:
  the graph screen's exception, its states and its reduced-motion styles),
  `docs/plans/2026-10-15-desktop-console-v2.md` (dated notes on V2.2.3, V2.3.1, V2.5.3, V2.6.1,
  V2.6.2, V2.8.1 pointing here).
- **Interfaces:** none. **Schema/rules/config:** none.
- **Tests:** the existing docs tests that read these files (`TestDocsConsoleStyling*`,
  `TestDocsDesktopConsoleV2*`, the live-init census).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|LiveInitCensus' -count=1 -timeout 300s`
- **Depends on:** the owner's confirmation of open tension (b).
- **Commit:** `docs(console): the graph view's amendments to D9, D22, D29, D30 and styling §3.9`
- **Red step:** none (docs-only); the docs tests must stay green, and each note sits beside the
  original sentence rather than replacing it.
- **Reviewer:** standard. **OWNER-RUN:** no.

### Slice 1 — the data, in Go

#### GV1.1 — Levels, downstream counts and the critical path
- **Files:** new `internal/plan/levels.go`, new `internal/plan/levels_test.go`, new
  `internal/plan/testdata/hub.json`.
- **Interfaces:** consumes `plan.Document`; produces `plan.Levels(Document) [][]string`,
  `plan.CriticalPath(Document) []string`, `plan.Downstream(Document) map[string]int`.
- **Schema/rules/config:** none (pure functions; nothing is read back from an artifact — a
  `critical_path` key in an artifact stays a refusal, `internal/plan/read_test.go`).
- **Tests:** `TestLevels`, `TestCriticalPathByCount`, `TestCriticalPathTieBreak`,
  `TestHubIsNotTheBottleneck` (the raw-count hub is not on the critical path).
- **Command:** `GOFLAGS=-p=2 go test ./internal/plan -run 'Levels|CriticalPath|Hub' -count=1 -timeout 300s`
- **Depends on:** nothing.
- **Commit:** `plan: levels, downstream counts and the critical path, computed in Go`
- **Red step:** `TestHubIsNotTheBottleneck` fails against a stub that ranks by downstream count.
- **Reviewer:** standard. **OWNER-RUN:** no.

#### GV1.2 — The plan-task join key (GD5)
- **Files:** `internal/task/task.go` (`Spec.PlanTask`, its validator), `internal/task/task_test.go`,
  `internal/runstore/launch.go` (`Launch.PlanTask`, `json:"plan_task,omitempty"`),
  `internal/runstore/launch_test.go`, `internal/cli/run.go` (`--plan-task` flag; copied into the
  launch record in `launchRun` and on `existingLaunch`'s recovery path), `internal/cli/run_test.go`,
  `internal/runview/runview.go` (`Row.PlanTask`), `internal/runview/runview_test.go`,
  `internal/watch/json.go` (`JSONRun`/`JSONFinished` `plan_task`), `internal/watch/json_test.go`.
- **Interfaces:** produces `fugaro run --plan-task <plan>/<task>`; `plan_task` in `task.json`,
  `launch.json`, `runs ls --json` and `watch --json`. The contract text lands with GV1.6.
- **Schema/rules/config:** additive JSON field; `Launch.Version` stays 1 (omitempty, readers use
  `json.Unmarshal` without unknown-field refusal). No rules change.
- **Tests:** `TestPlanTaskShape` (accepts `setup-wizard/W1.3`; refuses `..`, `/`-leading, control
  characters, over 128 bytes), `TestLaunchCarriesPlanTask`, `TestRecoveredLaunchKeepsPlanTask`,
  `TestRetryKeepsPlanTask`, `TestWatchJSONPlanTask`, `TestOldLaunchWithoutPlanTaskDecodes`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/task ./internal/runstore ./internal/runview ./internal/watch ./internal/cli -run 'PlanTask|Launch|WatchJSON' -count=1 -timeout 300s`
- **Depends on:** nothing.
- **Commit:** `run: record an optional plan task id at launch, and show it in runs ls and watch`
- **Red step:** `TestRecoveredLaunchKeepsPlanTask` fails while the recovery path builds a `Launch`
  from the run record alone.
- **Reviewer:** **opus + mandatory independent review** (the launch record; a job-writable field
  reaching the console, F32). **OWNER-RUN:** no.

#### GV1.3 — Graph schema v2: the stream
- **Files:** `internal/plan/document.go` (`SchemaVersion` 2; document-level `stream`, one of `cli`,
  `console`, `mobile`, optional; node `version` shaped `MAJOR.MINOR`), `internal/plan/read.go`
  (reads v1 and v2; refuses v3), `internal/plan/read_test.go`, new
  `internal/plan/testdata/graph-v2.json`, `console/docs/json-contract.md` (§6.9).
- **Interfaces:** `plan graph --json` prints `stream` when present.
- **Schema/rules/config:** artifact schema 2; v1 artifacts stay valid.
- **Tests:** `TestReadV1StillValid`, `TestReadV2Stream`, `TestUnknownStreamRefused`,
  `TestVersionShape`, the existing future-version refusal now at 3.
- **Command:** `GOFLAGS=-p=2 go test ./internal/plan ./internal/cli -run 'Read|Stream|Version|PlanGraph' -count=1 -timeout 300s`
- **Depends on:** nothing.
- **Commit:** `plan: graph schema 2 names the plan's release stream`
- **Red step:** `TestReadV2Stream` fails on v1's unknown-field refusal.
- **Reviewer:** standard. **OWNER-RUN:** no.

#### GV1.4 — The streams data file (GD7)
- **Files:** new `docs/release-streams.yaml`, new `internal/streams/streams.go`, new
  `internal/streams/streams_test.go`.
- **Interfaces:** produces `streams.Read(path) (Catalogue, error)`: per stream `name`, `theme`,
  `names` (ordered), `current` version, `next` name, `tag_prefix` (`v`, `console-v`, `mobile-v`).
- **Schema/rules/config:** the file; content per GD7: CLI Badger 0.10, Camel 0.11, Dolphin,
  Gecko, Hippo, Koala, Moose; Console Cherry, Mango, Melon; Mobile Falcon, Pelican, Robin.
- **Tests:** `TestCatalogueValid`, `TestNamesAlphabetical`, `TestNoNameInTwoStreams`,
  `TestConsoleFloor` (console `current` ≥ 0.10.0; F33), `TestForbiddenConsoleNames` (Apple,
  Lemon), `TestRealFileParses` (reads the repository's own file).
- **Command:** `GOFLAGS=-p=2 go test ./internal/streams -count=1 -timeout 300s`
- **Depends on:** the owner's confirmation of the path, open item (e).
- **Commit:** `streams: one file names the three release streams, their names and tag prefixes`
- **Red step:** `TestConsoleFloor` fails on a fixture whose console `current` is 0.9.7.
- **Reviewer:** standard. **OWNER-RUN:** no.

#### GV1.5 — The release script reads the CLI stream
- **Files:** `scripts/release.sh`, `scripts/release_test.go`.
- **Interfaces:** `scripts/release.sh X.Y.Z` reads the `cli` stream from `docs/release-streams.yaml`,
  refuses a version below `current` or a tag prefix that is not `v`, prints the release's spoken
  name ("Badger one"), and writes the new `current` into the file in its release commit.
- **Schema/rules/config:** none. Console and mobile tagging stays as `docs/release.md` describes;
  their independence is the "Release Names" work, not this plan (F6).
- **Tests:** `TestReleaseRefusesVersionBelowStreamCurrent`, `TestReleasePrintsStreamName`,
  `TestReleaseBumpsStreamCurrent` (on the script's existing test harness).
- **Command:** `GOFLAGS=-p=2 go test ./scripts -run 'Release.*Stream' -count=1 -timeout 300s`
- **Depends on:** GV1.4.
- **Commit:** `release: the script reads the CLI stream's name and version from release-streams.yaml`
- **Red step:** `TestReleaseRefusesVersionBelowStreamCurrent` fails before the read exists.
- **Reviewer:** standard (careful: `release.sh` is the owner's release path). **OWNER-RUN:** no.

#### GV1.6 — `fugaro plan status --json`: the graph joined to runs
- **Files:** new `internal/plan/state.go`, new `internal/plan/state_test.go`, new
  `internal/cli/plan_status.go`, new `internal/cli/plan_status_test.go`, `internal/cli/plan.go`
  (register the subcommand), `console/docs/json-contract.md` (new §6.10, and `plan_task` in §2 and
  §5).
- **Interfaces:** consumes GV1.1, GV1.2's `plan_task` (through `internal/runview`), GV1.3's
  `stream`, GV1.4's catalogue. Produces `fugaro plan status [--checkout DIR] [--all] --json`: every
  artifact under `DIR/docs/` (default: the working directory), each plan with its `stream`, nodes
  with `state` (`done`, `running` + `stage`, `ready`, `blocked`, `not-started`), `run` suffix,
  `pr_number` when finished, `critical`, `level`; per release a `closed` counter (merged and
  cancelled, collapsed unless `--all`, GD6); the streams catalogue.
- **Schema/rules/config:** none (a read).
- **Tests:** `TestStateFromRuns`, `TestBlockedUntilNeedsDone`, `TestOpenWorkByDefault`,
  `TestAllShowsClosed`, `TestCheckoutConfined` (a symlink under `docs/` pointing outside is
  refused; `..` in `--checkout` resolves and is still confined), `TestArtifactCountCap`,
  `TestRunWithUnknownPlanTaskIgnored`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/plan ./internal/cli -run 'State|Blocked|OpenWork|AllShows|CheckoutConfined|ArtifactCount|PlanStatus|UnknownPlanTask' -count=1 -timeout 300s`
- **Depends on:** GV1.1, GV1.2, GV1.3, GV1.4.
- **Commit:** `plan: plan status joins every plan's graph to its runs`
- **Red step:** `TestCheckoutConfined` fails against a walk that follows symlinks.
- **Reviewer:** **opus + mandatory independent review** (filesystem confinement; a cloud read joined
  to repository files). **OWNER-RUN:** no.

#### GV1.7 — The planning skill writes the graph (V2.8.1, with the stream)
- **Files:** `plugin/skills/parallelism/SKILL.md`, new
  `plugin/skills/parallelism/reference/graph-format.md` (only if the size budget needs it), new
  `docs/console-dependency-graph/implementation-plan.graph.json` (this plan's own graph: the first
  real artifact).
- **Interfaces:** the skill emits schema-2 artifacts beside the plan, with `stream`.
- **Schema/rules/config:** none.
- **Tests:** the skill lint; `plan graph --plan docs/console-dependency-graph/implementation-plan.md
  --json` reads the new artifact without refusal.
- **Command:** `GOFLAGS=-p=2 go test ./plugin -run 'PluginSkillsLintClean|LintSizeBudget|LintByteBudget|LintChecksReferenceFiles' -count=1 -timeout 300s && go run ./cmd/fugaro plan graph --plan docs/console-dependency-graph/implementation-plan.md --json`
- **Depends on:** GV1.3.
- **Commit:** `skills: parallelism writes the plan's graph artifact, with its stream`
- **Red step:** the `plan graph` read of the new artifact fails before GV1.3's schema 2 is on the
  branch (v1 refuses `stream`).
- **Reviewer:** standard. **OWNER-RUN:** no.

### Slice 2 — the console, read-only

#### GV2.1 — The `plan.status` operation
- **Files:** `console/src/main/ipc.ts` (`CONSOLE_OPERATIONS`, `ALLOWED_OPERATION_FLAGS`),
  `console/src/main/spawn.ts` (`SPAWN_COMMANDS`; `--checkout` from the installation tuple's
  `checkout`, never from the renderer), `console/src/preload/index.ts` (`EXPOSED_OPERATIONS`),
  `console/src/source/source.ts`, `console/src/source/cli.ts` (`CliSource`),
  `console/src/source/fixture.ts`, `console/src/shared/schema.ts` (the decoder),
  `console/test/unit/ipc.test.ts`, `console/test/unit/spawn.test.ts`,
  `console/test/unit/preload-surface.test.ts`, `console/test/unit/schema.test.ts`, new
  `console/test/fixtures/plan-status.json`.
- **Interfaces:** consumes GV1.6's document; produces `source.planStatus()` to the renderer.
- **Schema/rules/config:** none.
- **Tests:** an operation whose argv carries a renderer-supplied path is refused; an installation
  with no `checkout` yields the not-available state; the decoder refuses an unknown `state`; the
  spawn argv is exact.
- **Command:** `cd console && npx vitest run test/unit/ipc.test.ts test/unit/spawn.test.ts test/unit/preload-surface.test.ts test/unit/schema.test.ts && npm run typecheck`
- **Depends on:** GV1.6.
- **Commit:** `console: read plan status through the CLI, with the checkout from the installation`
- **Red step:** the "renderer-supplied path refused" test fails against an operation that accepts an
  `args` path.
- **Reviewer:** **opus + mandatory independent review** (the spawn allow-list; a filesystem argument).
  **OWNER-RUN:** no.

#### GV2.2 — elkjs in a worker (GD3)
- **Files:** `console/package.json` (`dependencies`: `elkjs` exact; `build.files`: the worker
  bundle), `console/package-lock.json`, `console/tools/bundle-renderer/index.ts` (a second entry
  that may import `elkjs` and nothing else), new `console/src/renderer/graph/layout-worker.ts`,
  `console/src/main/protocol.ts` (`SERVED_FILES` gains the worker file; `CONTENT_SECURITY_POLICY`
  unchanged), `console/README.md` (the D9 sentence, amended), new `console/THIRD_PARTY_NOTICES.md`,
  `console/test/unit/pinning.test.ts`, `console/test/unit/bundle.test.ts`,
  `console/test/unit/build-files.test.ts`, `console/test/unit/window.test.ts`,
  `console/test/unit/csp-document.test.ts`, `console/test/unit/lockfile.test.ts`, new
  `console/test/e2e/graph-worker.spec.ts`.
- **Interfaces:** produces a worker that takes `{nodes:[{id,w,h,group}], edges:[{from,to}],
  groups:[…]}` (ids and sizes, no text) and returns positions.
- **Schema/rules/config:** a runtime dependency (the first renderer one).
- **Tests:** pinning allows `elkjs` by name only; the main bundle still refuses every bare specifier;
  the CSP string is byte-identical; `SERVED_FILES` has exactly seven entries; in the built app the
  worker lays out a fixture and `new Function` inside the worker throws.
- **Command:** `cd console && npx vitest run test/unit/pinning.test.ts test/unit/bundle.test.ts test/unit/build-files.test.ts test/unit/window.test.ts test/unit/csp-document.test.ts test/unit/lockfile.test.ts && npm run test:e2e -- test/e2e/graph-worker.spec.ts`
- **Depends on:** the owner's confirmation of the licence, item (c); GV0.1.
- **Commit:** `console: elkjs, pinned, in a worker under the unchanged CSP`
- **Red step:** `bundle.test.ts`'s bare-specifier claim fails on the worker entry until the bundler
  admits `elkjs` for that entry alone; the e2e fails if elkjs needs eval (U1).
- **Reviewer:** **opus + mandatory independent review** (supply chain, CSP, served files).
  **OWNER-RUN:** no (the licence confirmation is the owner's, before the task).

#### GV2.3 — The graph screen, static
- **Files:** new `console/src/renderer/screens/graph.ts`, new `console/src/renderer/graph/model.ts`
  (document → layout input: stream columns, release boxes, nodes), new
  `console/src/renderer/graph/layout.ts` (the worker client, cached by structure hash),
  `console/src/renderer/entry.ts` (`VIEWS` gains `graph`), `console/src/renderer/theme/screens.css`,
  new `console/test/unit/graph.test.ts`, new `console/test/unit/graph-model.test.ts`, new
  `console/test/e2e/graph.spec.ts`.
- **Interfaces:** consumes `source.planStatus()`, the live feed, the worker. Produces: tabs CLI,
  Console, Mobile, All (columns); release boxes headed "Stream · Name" with "n of m done" bars;
  finished releases as chips grouped by stream; nodes with title, PR number, "run …xxxx";
  states with words (`implementing`, `in review`, `fixing`, `queued`, `waiting`, `halted`,
  `stuck`, `blocked`, `ready`, `done`, `critical`); critical path edges thicker and distinct;
  blocked dashed; zoom, pinch, fit; a list view of the same document; keyboard traversal; a node
  opens the run screen, or the PR through `link.openExternal`.
- **Schema/rules/config:** none.
- **Tests:** every state has a word in the DOM; a hostile title renders as text; layout is not
  recomputed on a watch frame that changes no structure; the list view carries every node; a PR
  opens only via `link.openExternal`; forced colours keep the dashed/solid distinction.
- **Command:** `cd console && npx vitest run test/unit/graph.test.ts test/unit/graph-model.test.ts && npm run typecheck && npm run test:e2e -- test/e2e/graph.spec.ts`
- **Depends on:** GV2.1, GV2.2.
- **Commit:** `console: the dependency graph screen, read-only and static`
- **Red step:** the "no relayout on an unchanged structure" test fails against a screen that lays out
  per frame.
- **Reviewer:** standard. **OWNER-RUN:** no.

#### GV2.4 — Motion (GD4)
- **Files:** `console/src/renderer/theme/screens.css` (comet, pulse, flow keyframes; a static style
  per state under `prefers-reduced-motion: reduce`), `console/src/renderer/screens/graph.ts` (state
  classes only), new `console/test/unit/graph-motion.test.ts`, `console/test/e2e/graph.spec.ts`.
- **Interfaces:** none new.
- **Schema/rules/config:** none (the §3.9 amendment is GV0.1's).
- **Tests:** under emulated reduced motion each animated state has its static style and its word;
  no animation outside the graph screen; forced colours draw no animation.
- **Command:** `cd console && npx vitest run test/unit/graph-motion.test.ts && npm run test:e2e -- test/e2e/graph.spec.ts`
- **Depends on:** GV2.3, GV0.1.
- **Commit:** `console: the graph's motion, with a static style for reduced motion`
- **Red step:** the reduced-motion test fails while only `base.css`'s global freeze applies (F36).
- **Reviewer:** standard. **OWNER-RUN:** no.

#### GV2.5 — A live run's PR number in the feed
- **Files:** `internal/watch/json.go`, `internal/watch/json_test.go`, `console/docs/json-contract.md`
  (§2), `console/src/shared/schema.ts`, `console/test/unit/schema.test.ts`.
- **Interfaces:** `pr_number` on a live `JSONRun` when the run has recorded one.
- **Schema/rules/config:** additive field.
- **Tests:** `TestLiveRunCarriesDraftPR`, `TestLiveRunWithoutPRHasNoField`; the decoder accepts it.
- **Command:** `GOFLAGS=-p=2 go test ./internal/watch -run 'LiveRun' -count=1 -timeout 300s && cd console && npx vitest run test/unit/schema.test.ts`
- **Depends on:** GV1.6, GV2.1, and UNVERIFIED U3 being answered (where the draft PR number is
  recorded). If U3 says nowhere, the task closes as "not possible without a runner change" and the
  node shows the PR only when finished.
- **Commit:** `watch: a live run carries its draft PR number`
- **Red step:** `TestLiveRunCarriesDraftPR` fails on today's feed (contract §2).
- **Reviewer:** standard. **OWNER-RUN:** no.

#### GV2.6 — Check 123: the graph in the packed, signed app (OWNER-RUN)
- **Files:** `docs/gcp-live-checklist.md` (the Check 123 row and section, written by an agent).
- **What it measures:** in the signed macOS build, the worker loads from `app://` under the fuses
  (U5); the graph renders for the sandbox installation's checkout; with Reduce motion on, every
  state is static and labelled.
- **What the owner types:** `cd console && npm ci && npm run package`, open
  `release/mac-universal/Fugaro Console.app`, choose the sandbox installation, open Graph; then turn
  on System Settings → Accessibility → Display → Reduce motion and look again. Record PASS/FAIL in
  the row.
- **Depends on:** GV2.4. **Reviewer:** standard (for the row). **OWNER-RUN:** yes; no agent runs any
  step.

### Slice 3 — the write (GD2) — blocked on open tension (a)

#### GV3.1 — The roadmap write command
- **Files:** depend on the ruling. Option 2: new `internal/plan/move.go`, `internal/plan/move_test.go`,
  new `internal/cli/plan_move.go`. Option 3: `internal/coordinator/` (a move record written with
  `GetETag` + `PutIfMatch`), new `internal/cli/roadmap.go`, `internal/cli/root.go`, and the rules or
  front-door change the coordinator plan would have to take. Option 1: none.
- **Interfaces:** `<command> MOVE <plan>/<task> --to <stream>/<release> [--expect <from>] --json`,
  printing the before/after and refusing when `--expect` no longer matches.
- **Tests:** stale `--expect` refused and nothing moves; a move that makes an edge backwards is
  reported; the inverse move restores the document byte for byte.
- **Command:** `GOFLAGS=-p=2 go test ./internal/plan ./internal/coordinator ./internal/cli -run 'Move|Roadmap' -count=1 -timeout 300s`
- **Depends on:** the owner's ruling on (a); for option 3, coordinator P3/P4 on `main`.
- **Commit:** `roadmap: move a task between releases, compare-and-set`
- **Red step:** the stale-`--expect` test fails against a blind write.
- **Reviewer:** **opus + mandatory independent review**. **OWNER-RUN:** no.

#### GV3.2 — Drag, preview, confirm, undo
- **Files:** `console/src/renderer/screens/graph.ts`, new `console/src/renderer/graph/move.ts`,
  `console/src/main/ipc.ts`, `console/src/main/spawn.ts`, `console/src/preload/index.ts`,
  `console/src/source/cli.ts`, `console/test/unit/graph-move.test.ts`, `console/test/unit/ipc.test.ts`,
  `console/test/unit/spawn.test.ts`, `docs/design/desktop-console-v2.md` (a new dated row recording
  the fence's move, as V2.5.1 does for the launch).
- **Interfaces:** the renderer sends `{node, toRelease}` ids only; the main process checks both
  against the last document read and builds the argv; the confirmation shows the exact argv and the
  before/after; undo is the inverse move, previewed the same way.
- **Tests:** a node or release id not in the last document is refused; a declined confirmation
  spawns nothing; the confirmation text is composed from named fields; undo issues the inverse
  argv.
- **Command:** `cd console && npx vitest run test/unit/graph-move.test.ts test/unit/ipc.test.ts test/unit/spawn.test.ts && npm run typecheck`
- **Depends on:** GV3.1, GV2.4.
- **Commit:** `console: drag a task to another release, behind a preview and a confirmation`
- **Red step:** the "id not in the last document" test fails against a main process that forwards
  the renderer's ids.
- **Reviewer:** **opus + mandatory independent review**. **OWNER-RUN:** no.

### Slice 4 — the whole project, task-level — blocked on GD1's record and tension (d)

#### GV4.1 — Cross-plan and cross-stream edges from the coordinator's record
- **Files:** depend on the coordinator's record shape once it carries tasks, releases and
  cross-plan edges; expected `internal/cli/plan_status.go`, `console/src/renderer/graph/model.ts`,
  `console/src/renderer/screens/graph.ts`, and their tests.
- **Interfaces:** qualified ids (`<plan>/<task>`), faint cross-release and cross-stream edges,
  highlighted on selection or drag; in a single-stream tab the "needs CLI · Camel" marker.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'PlanStatus' -count=1 -timeout 300s && cd console && npx vitest run test/unit/graph-model.test.ts`
- **Depends on:** the coordinator's record (0.11.0) carrying tasks; GV2.3.
- **Commit:** `graph: cross-plan and cross-stream edges from the project record`
- **Red step:** a fixture with a cross-plan edge renders no edge before the change.
- **Reviewer:** standard. **OWNER-RUN:** no.

## 5. Parallel execution

**Edges** (from "Depends on"): GV1.1→GV1.6; GV1.2→GV1.6; GV1.3→GV1.6; GV1.4→GV1.6; GV1.3→GV1.7;
GV1.4→GV1.5; GV0.1→GV2.2; GV1.6→GV2.1; GV2.1→GV2.3; GV2.2→GV2.3; GV1.6→GV2.5; GV2.1→GV2.5;
GV2.3→GV2.4; GV0.1→GV2.4; GV2.4→GV2.6; GV3.1→GV3.2; GV2.4→GV3.2; GV2.3→GV4.1.

**Levels** (tasks in one level run concurrently):

| Level | Tasks |
| --- | --- |
| L0 | GV0.1, GV1.1, GV1.2, GV1.3, GV1.4 |
| L1 | GV1.5, GV1.6, GV1.7, GV2.2 |
| L2 | GV2.1 |
| L3 | GV2.3, GV2.5 |
| L4 | GV2.4 |
| L5 | GV2.6 (OWNER-RUN) |
| blocked | GV3.1 → GV3.2 (tension (a)); GV4.1 (GD1's record, tension (d)) |

**Critical path:** GV1.2 → GV1.6 → GV2.1 → GV2.3 → GV2.4 → GV2.6 (six levels; GV1.2 is the
longest L0 task). The schema task GV1.3 and the streams file GV1.4 are on equal-length paths.

**What must be sequential, and why:** GV1.6 needs the four L0 data tasks' interfaces; GV2.1 needs
GV1.6's contract; GV2.3 needs the operation and the worker; GV2.4 animates GV2.3's states. GV2.5
sits at L3 because `console/src/shared/schema.ts` is GV2.1's at L2.

**Shared files** (every file written by more than one task, with its writers' levels):

| File | Writers (level) |
| --- | --- |
| `console/docs/json-contract.md` | GV1.3 (L0), GV1.6 (L1), GV2.5 (L3) |
| `internal/watch/json.go`, `internal/watch/json_test.go` | GV1.2 (L0), GV2.5 (L3) |
| `internal/cli/plan.go` | GV1.6 (L1) |
| `internal/cli/plan_status.go` | GV1.6 (L1), GV4.1 (blocked) |
| `console/src/shared/schema.ts`, `console/test/unit/schema.test.ts` | GV2.1 (L2), GV2.5 (L3) |
| `console/src/main/ipc.ts`, `console/src/main/spawn.ts`, `console/src/preload/index.ts`, `console/src/source/cli.ts`, `console/test/unit/ipc.test.ts`, `console/test/unit/spawn.test.ts` | GV2.1 (L2), GV3.2 (blocked, after L4) |
| `console/src/renderer/screens/graph.ts` | GV2.3 (L3), GV2.4 (L4), GV3.2 (blocked), GV4.1 (blocked) |
| `console/src/renderer/theme/screens.css`, `console/test/e2e/graph.spec.ts` | GV2.3 (L3), GV2.4 (L4) |
| `console/src/renderer/graph/model.ts` | GV2.3 (L3), GV4.1 (blocked) |
| `docs/design/desktop-console-v2.md` | GV0.1 (L0), GV3.2 (blocked) |

No level holds two writers of one file. The `internal/plan` package has three L0/L1 writers on
**different files** (GV1.1 `levels*.go`, GV1.3 `document.go`/`read*.go`, GV1.6 `state*.go`); GV1.1
reads `Document` and GV1.3 adds only optional fields, so the two L0 changes compile together.
Outside this plan, coordinator P6 writes the same four console files on branch `coordinator` (F38).

## 6. Checks

**Check 123** (this work's block is 123 alone): the graph in the packed, signed macOS app — the
worker under the fuses, the render, reduced motion. Owner-run, GV2.6. No other check is taken.

## 7. Release actions

None at deploy time: no backfill, no secret, no rules change. GV1.5 changes the owner's release
path; its PR's description says so for the next release's operator steps. The console graph needs a
CLI with `plan status` (GV1.6): the console shows "not available" against an older CLI.

## 8. Not in this plan

Independent console and mobile release workflows (the "Release Names" work, F6); the coordinator's
record and its front door (coordinator P3–P8); any Markdown plan scraper (F3); cancel and retry
(D34, unchanged).
