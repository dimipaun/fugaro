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
  write (GV3.1, GV3.2) stays **blocked until the coordinator's record exists** (ruling (a),
  2026-10-22: read-only until 0.11.0): the record it would write does not exist
  until 0.11.0.
- **GD3** (not the recommendation): elkjs; reverses D9. Licence confirmed under EPL-2.0, pinned
  to 0.11.0 (ruling (c)); GV2.2 is not gated.
- **GD4**: motion with text marks and a reduced-motion static fallback (GV2.4).
- **GD5**: a plan-task id recorded at launch (GV1.2).
- **GD6**: open work by default, a toggle for everything, merged/cancelled as a per-release
  counter (GV1.6, GV2.3).
- **GD7**: one streams data file, read by the graph and the release script (GV1.4, GV1.5); path
  amended to `docs/release-streams.yaml` (F29), accepted (ruling (e)).
- **GD8**: this folder's own prefixes.

**The owner's rulings on the tensions (2026-10-22, all as recommended):** (a) read-only graph
until 0.11.0, drag with the coordinator's record; (b) the amendments to D9, D22, D29, D30 and
console-styling §3.9 confirmed; (c) elkjs confirmed under EPL-2.0, pinned to **0.11.0**, licence
text shipped, file unmodified; (d) per-plan task graphs now on the existing `plan graph --json`
document, release assignment later — so Slices 1–2 wait on no ruling; (e)
`docs/release-streams.yaml` accepted; (f) proposed for coordinator P4 only.

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
| Console v2 **V2.5.3** ("copy the change") | **Superseded** by GV3.2, which arrives with the coordinator's record (GD2, ruling (a)). |
| Console v2 **V2.6.1** (version column) | **Amended**: the version becomes a release box (GV2.3); the stream comes from GV1.3; the source later moves to the coordinator's record (GD1), not to `fugaro project export` (F31). |
| Console v2 **V2.6.2** (dependency-aware warning) | **Kept** as is for the warning; its "pull it forward" half follows GV3.2 instead of V2.5.3. |
| Console v2 **V2.8.1** (skill writes the format) | **Kept**, extended by the `stream` field: GV1.7. |
| Coordinator **P6** (console project view) | **Unchanged in scope**; shares console files with GV2.1/GV2.3 (F38, a merge-time note). When P1 reaches `main`, GV4.1 reads release membership for PR-level nodes from it. |
| Coordinator **P4** (intent record) | **Proposed, not made**: carry GD5's plan-task id (ruling (f): proposed there only). |

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
- **Depends on:** nothing (ruling (b) confirmed the amendments, 2026-10-22).
- **Commit:** `docs(console): the graph view's amendments to D9, D22, D29, D30 and styling §3.9`
- **Red step:** none (docs-only); the docs tests must stay green, and each note sits beside the
  original sentence rather than replacing it.
- **Reviewer:** standard. **OWNER-RUN:** no.

### Slice 1 — the data, in Go

#### GV1.1 — Levels, downstream counts and the critical path
- **Files:** new `internal/plan/levels.go`, new `internal/plan/levels_test.go`, new
  `internal/plan/testdata/hub.json`.
- **Interfaces:** consumes `plan.Document`; produces `plan.Levels(Document) [][]string`,
  `plan.CriticalPathTasks(Document) []string`, `plan.Downstream(Document) map[string]int`.
  **Amended 2026-10-24, as GV1.1 shipped:** the second is `CriticalPathTasks`, not `CriticalPath`.
  V2.2.3 merged on 2026-10-23 (§2's "absorbed by GV1.1"), so `internal/plan` already exports a
  `CriticalPath` type — the read's own object, pinned by the console JSON contract and by
  `internal/cli/docs_desktop_console_test.go` — and Go refuses a function carrying a name a type in
  the package holds. Renaming the type would reach files outside this task. One function, the
  answer this line meant; **GV1.6 consumes this name**.
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
  `console`, `mobile`, optional; in v2 documents only, node `version` shaped `MAJOR.MINOR[.PATCH]`,
  so a patch box such as 0.10.1, spoken "Badger one", is expressible), `internal/plan/read.go`
  (reads v1 unchanged — no shape check on a v1 `version` — and v2; refuses v3), `internal/plan/read_test.go`, new
  `internal/plan/testdata/graph-v2.json`, `console/docs/json-contract.md` (§6.9).
- **Interfaces:** `plan graph --json` prints `stream` when present.
- **Schema/rules/config:** artifact schema 2; v1 artifacts stay valid.
- **Tests:** `TestReadV1StillValid` (including a v1 `version` of any text), `TestReadV2Stream`,
  `TestUnknownStreamRefused`, `TestVersionShape` (accepts `0.10` and `0.10.1`, refuses `v0.10`,
  `0.10.1.2`, free text), the existing future-version refusal now at 3.
- **Command:** `GOFLAGS=-p=2 go test ./internal/plan ./internal/cli -run 'Read|Stream|Version|PlanGraph' -count=1 -timeout 300s`
- **Depends on:** nothing. Per ruling (d), `stream` is a display input; authoritative release assignment comes from the coordinator's record (GV4.1).
- **Commit:** `plan: graph schema 2 names the plan's release stream`
- **Red step:** `TestReadV2Stream` fails on v1's unknown-field refusal.
- **Reviewer:** standard. **OWNER-RUN:** no.

#### GV1.4 — The streams data file (GD7)
- **Files:** new `docs/release-streams.yaml`, new `internal/streams/streams.go`, new
  `internal/streams/streams_test.go`.
- **Interfaces:** produces `streams.Read(path) (Catalogue, error)` (refuses a symlink and a file over
  64 KiB before decoding; YAML aliases refused): per stream `name`, `theme`,
  `names` (ordered), `current` version, `next` name, `tag_prefix` (`v`, `console-v`, `mobile-v`).
- **Schema/rules/config:** the file; content per GD7: CLI Badger 0.10, Camel 0.11, Dolphin,
  Gecko, Hippo, Koala, Moose; Console Cherry, Mango, Melon; Mobile Falcon, Pelican, Robin.
- **Tests:** `TestCatalogueValid`, `TestNamesAlphabetical`, `TestNoNameInTwoStreams`,
  `TestConsoleFloor` (console `current` ≥ 0.110.0; F33), `TestForbiddenConsoleNames` (Apple,
  Lemon), `TestRealFileParses` (reads the repository's own file), `TestSymlinkRefused`,
  `TestOversizeRefused`, `TestAliasRefused`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/streams -count=1 -timeout 300s`
- **Depends on:** nothing (ruling (e) accepted the path).
- **Commit:** `streams: one file names the three release streams, their names and tag prefixes`
- **Red step:** `TestConsoleFloor` fails on a fixture whose console `current` is 0.9.7.
- **Reviewer:** standard. **OWNER-RUN:** no.

#### GV1.5 — The release script reads the CLI stream
- **Files:** `scripts/release.sh`, `scripts/release_test.go`, new
  `internal/streams/cmd/streams/main.go`.
- **Interfaces:** `scripts/release.sh X.Y.Z` reads the `cli` stream from `docs/release-streams.yaml`
  through a small Go helper run from the clean clone the script already works in (new
  `internal/streams/cmd/streams/main.go`, `go run ./internal/streams/cmd/streams get cli`; no `yq`
  dependency),
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
  (register the subcommand), `console/docs/json-contract.md` (new **§6.11**, and `plan_task` in §2
  and §5; §6.10 is `runs stats` and is pinned).
- **Interfaces:** consumes GV1.1, GV1.2's `plan_task` (through `internal/runview`), GV1.3's
  `stream`, GV1.4's catalogue. Produces `fugaro plan status [--all] --json` **plus the cloud flags**
  (`--project`/`--config`, as `runs ls`): unlike `plan graph` it is a cloud read. It reads every
  artifact under the working directory's `docs/`, and prints each plan with its `stream`, nodes with
  `state`, `level`, `critical`, `run` suffix and `pr_number` **whenever the run has recorded one**
  (`record_pr`, running or finished); per release a `closed` counter (merged and cancelled,
  collapsed unless `--all`, GD6) and `finished` (the catalogue's `current` for that stream is at or
  past the release); the streams catalogue.
- **The states, defined.** `running` + `stage`: a live run joined to the node. `done`: a joined
  run's PR is merged according to `internal/watch/prstate.go`'s `PRState` (the operator's `gh`,
  GitHub only, fail-open); where that answer is unavailable (Bitbucket, no `gh`, a lookup failure) a
  run whose outcome is `ready`/`reviewed` is shown `done` with the mark `merge unknown`.
  `cancelled`: the latest run cancelled and no other. `blocked`: a need is not `done`. `ready`:
  every need `done`, no run. `not-started` otherwise. **Merged** in GD6's counter means `done` with a
  merge confirmed; the others count as open.
- **The join rules.** (i) The plan id is the artifact's directory under `docs/`
  (`docs/setup-wizard/implementation-plan.graph.json` → `setup-wizard`), or for `docs/plans/` the
  file's stem (`docs/plans/2026-10-15-desktop-console-v2.graph.json` →
  `plans/2026-10-15-desktop-console-v2`). (ii) A run joins only a plan of **its own repository**
  (the checkout's origin); a run from another repository naming the same id is ignored. (iii) Of
  several runs on one task, the latest launched wins for `state`; follow-ups (`fugaro run --pr N`)
  inherit `plan_task` from their task spec only if they were given one, and are otherwise joined
  through their PR number to the run they follow. (iv) The join is display-only: it never makes
  another plan's node `ready`.
- **Schema/rules/config:** none (a read).
- **Tests:** `TestStateFromRuns`, `TestBlockedUntilNeedsDone`, `TestDoneNeedsMergeOrMarksUnknown`,
  `TestOpenWorkByDefault`, `TestAllShowsClosed`, `TestPlanIDFromPath`, `TestJoinIgnoresOtherRepo`,
  `TestLatestRunWins`, `TestFollowUpJoinsByPR`, `TestRunningNodeShowsRecordPR`,
  `TestCheckoutConfined` (a symlink under `docs/` pointing outside is refused, and a symlinked
  `docs/` itself), `TestArtifactCountCap`, `TestRunWithUnknownPlanTaskIgnored`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/plan ./internal/cli -run 'State|Blocked|DoneNeeds|OpenWork|AllShows|PlanID|JoinIgnores|LatestRun|FollowUp|RunningNode|CheckoutConfined|ArtifactCount|PlanStatus|UnknownPlanTask' -count=1 -timeout 300s`
- **Depends on:** GV1.1, GV1.2, GV1.3, GV1.4.
- **Commit:** `plan: plan status joins every plan's graph to its runs`
- **Red step:** `TestCheckoutConfined` fails against a walk that follows symlinks;
  `TestJoinIgnoresOtherRepo` fails against a join on the id alone.
- **Reviewer:** **opus + mandatory independent review** (filesystem confinement; a cloud read joined
  to repository files; a job-writable join key). **OWNER-RUN:** no.
- **Amended 2026-10-25, as GV1.6 shipped:**
  - **The contract section is §6.12, not §6.11.** `console/docs/json-contract.md` gained §6.11 as
    `backlog list` after this plan was written (CLI 0.11.0), so the graph-joined-to-runs section is
    §6.12 and the `plan status` row in §8 says so. `plan_task` was already in §2 and §5 (GV1.2).
  - **One file is added: `internal/watch/prstate_query.go`** (with
    `internal/watch/prstate_query_test.go`). This task's state table needs the merge answer
    synchronously, and `internal/watch`'s `PRState` answers asynchronously — `Filter` starts lookups
    off the render loop and the answer lands in a cache for the next frame. `PRState.MergeNow` asks
    the same question and returns it, and it lives in `internal/watch` rather than in the CLI so the
    URL pattern, the argv, the pinned host, the process group and the capped pipes stay the one
    spawn path this tree has; the alternative — a second `gh` invocation in `internal/cli` — would be
    a second answer to "which URLs may be handed to a subprocess".
  - **`plan.StatePlan` states its own figures** (`tasks`, `closed`, `open`), and each release box
    carries `total`, `done`, `closed` and `open` beside GD6's `closed` counter: `nodes` is filtered
    by `--all`, so a header or a totals line a screen draws cannot be added up from it. Additive,
    and the contract's §6.12 states them.
  - **The console contract's own tests learned the shapes**
    (`internal/cli/docs_desktop_console_test.go`: the two field lists and
    `consoleContractCommands`), and `website/content/cli-reference.md` is regenerated — a new
    command is a page the generator writes.
  - **One guard file is amended: `internal/runview/plan_task_test.go`.** Constraint C3 is enforced
    by a walk over the module — any non-test Go file naming `PlanTask`/`plan_task` must be on its
    display allow-list — so `internal/plan/state.go` and `internal/cli/plan_status.go` are added to
    it, with what each one does with the id. The two are display-only readers: the join matches ids,
    and no state, count or refusal depends on what a launcher wrote.
- **Amended 2026-10-25, at GV1.6's review:**
  - **The lookups are scoped to the runs the join reads.** `plan status` lists every run the
    bucket still holds (`followUpLookback`, ninety days — the bucket's own lifecycle, and the window
    `run --pr` reads with), and then `plan.JoinedRuns` keeps those that join at least one plan here.
    The merge lookup is scoped to that set: it spawns the operator's `gh` once per distinct pull
    request, so handing it the repository's whole run history is a cost that grows with the history,
    for a read the console calls to draw the graph. `State` is handed the same set, so no URL is
    looked up that the join did not read. The answers are not kept between invocations —
    `internal/watch`'s cache lives in that process and this command is one process — so what bounds
    the spawns is the scoping itself. No `--since` flag was added: that is a new flag and a product
    decision, and the bound above already removes the unbounded cost.
  - **The walk refuses a non-regular artifact**, not only a symlink: a fifo, a socket or a device at
    a `*.graph.json` name makes `os.Open` a read that may never return. The rule sits in the walk,
    where the symlink rule is (as `internal/streams.Read`'s does), so `plan.ReadFile` — which
    `plan graph` shares — is unchanged.
  - **Two artifacts that would be one plan id are refused, not silently merged.** The id is the
    artifact's path, so `docs/plans/foo.graph.json` and `docs/plans/foo/bar.graph.json` both give
    `plans/foo`, and joining them would let the second's tasks share the first's runs and totals. The
    first in path order keeps the id, the second is not listed, and a fixed sentence names both.
    Additive: it changes what is listed only in a checkout that was already ambiguous.
  - **Tests for the above**, beyond the list below: `TestPlanStatusLooksUpOnlyJoinedRuns` and
    `TestPlanStatusReadsOnlyTheRunsTheBucketHolds` (the two bounds),
    `TestPlanStatusSanitisesAJoinedStage` (a *joined* run's stage, which the misshapen-`plan_task`
    test cannot reach — that run never joins), `TestPlanStatusSumsEveryPlan` (two artifacts at once),
    `TestPlanStatusWarnsOnARefusedArtifact`, `TestPlanStatusWarnsOnAnUnreadableCatalogue` and
    `TestPlanStatusWarnsOnAnUnreadableOrigin` (the three warning paths, previously untested),
    `TestPlanStatusRefusesADuplicatePlanID`, `TestPlanStatusRefusesANonRegularArtifact`, and
    `TestPlanIDScopesTheJoinedRuns` in `internal/plan/state_test.go`.
- **Amended 2026-10-25, at GV1.6's independent review:**
  - **`plan status` is a console read** (`consoleReadPaths` in `internal/cli/project.go`): it
    resolves the checkout to select the project, and a console spawn must not record the checkout it
    looked at. `TestConsoleAllowListedReadsWriteNothing` runs it.
  - **A job-written `pr_url` of another repository is never looked up** and answers "not merged":
    a job must not mark its own task done by pointing its record at any merged pull request.
    `watch.PRRepo` reads the URL's repository with `MergeNow`'s own pattern.
  - **The lookups are capped at 32 per read**, the newest runs' first; a pull request past the cap
    is `merge unknown`, with a warning.
  - **A release's name is the reader's arithmetic** (`streams.FirstVersion`: the first name is the
    named line's first minor), and "finished" is `streams.Version.AtLeast`, the reader's own
    comparison, exported for it — no second copy of either in `internal/plan`.

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
  `console/src/main/spawn.ts` (`SPAWN_COMMANDS`; the checkout reaches the CLI as the spawn's cwd,
  which spawn.ts already sets from the installation tuple — no path in the argv; the cloud flags as
  `runs.list` carries them), `console/src/preload/index.ts` (`EXPOSED_OPERATIONS`),
  `console/src/source/source.ts`, `console/src/source/cli.ts` (`CliSource`),
  `console/src/source/fixture.ts`, `console/src/shared/schema.ts` (the decoder),
  `console/test/unit/ipc.test.ts`, `console/test/unit/spawn.test.ts`,
  `console/test/unit/preload-surface.test.ts`, `console/test/unit/schema.test.ts`, new
  `console/test/fixtures/plan-status.json`.
- **Interfaces:** consumes GV1.6's document; produces `source.planStatus()` to the renderer. Refresh
  cadence: on opening the screen, on the operator's refresh, and at most once a minute while the
  screen is visible (it lists every run in the bucket, F24); never on each watch frame — live stage
  changes come from the existing feed, joined in the renderer by run id.
- **Schema/rules/config:** none.
- **Tests:** an operation whose argv carries a renderer-supplied path is refused; an installation
  with no `checkout` yields the not-available state; the refresh is rate-limited; the decoder refuses an unknown `state`; the
  spawn argv is exact.
- **Command:** `cd console && npx vitest run test/unit/ipc.test.ts test/unit/spawn.test.ts test/unit/preload-surface.test.ts test/unit/schema.test.ts && npm run typecheck`
- **Depends on:** GV1.6.
- **Commit:** `console: read plan status through the CLI, with the checkout from the installation`
- **Red step:** the "renderer-supplied path refused" test fails against an operation that accepts an
  `args` path.
- **Reviewer:** **opus + mandatory independent review** (the spawn allow-list; a filesystem argument).
  **OWNER-RUN:** no.

#### GV2.2 — elkjs in a worker (GD3)
- **Files:** `console/package.json` (**`devDependencies`**: `elkjs` exactly `0.11.0`; `build.files`: the worker
  file), `console/package-lock.json`, `console/tools/bundle-renderer/index.ts` (copies
  `node_modules/elkjs/lib/elk-worker.min.js` verbatim into `dist/bundle/` — a copy, not a walked
  entry, so the bare-specifier rule is unchanged), new `console/src/renderer/graph/layout-client.ts`
  (plain `postMessage` to the worker; validates every reply: known ids, finite numbers),
  `console/src/main/protocol.ts` (`SERVED_FILES` gains the worker file as its seventh entry;
  `CONTENT_SECURITY_POLICY` unchanged), `console/README.md` (the D9 sentence, amended), new
  `console/THIRD_PARTY_NOTICES.md` (elkjs's EPL-2.0 licence text, copied from the package's
  `LICENSE.md`, and a pointer to its source; packed beside the app via `build.files`), `console/test/unit/bundle.test.ts`,
  `console/test/unit/build-files.test.ts`, `console/test/unit/window.test.ts`,
  `console/test/unit/csp-document.test.ts`, `console/test/unit/lockfile.test.ts`, new
  `console/test/unit/layout-client.test.ts`, new `console/test/e2e/graph-worker.spec.ts`.
- **Interfaces:** the worker takes ELK's JSON graph built from ids and sizes (no text) and returns
  positions; `layout-client.ts` exposes `layout(graph): Promise<Positions>`.
- **Schema/rules/config:** a devDependency, exactly pinned; `pinning.test.ts`'s
  `runtimeDependencyAllowList` stays empty (the file is copied at build time, not a runtime
  package, so the 8 MB package does not ship).
- **Tests:** the main bundle still refuses every bare specifier (unchanged test, still green); the
  copied file's SHA-256 matches the pinned package's; the CSP string is byte-identical;
  `SERVED_FILES` has exactly seven entries; a reply with an unknown id or `NaN` is refused; in the
  built app the worker lays out a fixture and `new Function` inside the worker throws.
- **Command:** `cd console && npx vitest run test/unit/pinning.test.ts test/unit/bundle.test.ts test/unit/build-files.test.ts test/unit/window.test.ts test/unit/csp-document.test.ts test/unit/lockfile.test.ts test/unit/layout-client.test.ts && npm run test:e2e -- test/e2e/graph-worker.spec.ts`
- **Depends on:** GV0.1 (ruling (c) confirmed the licence: EPL-2.0, 0.11.0).
- **Commit:** `console: elkjs's worker, pinned and served verbatim under the unchanged CSP`
- **Red step:** `build-files.test.ts` and `window.test.ts` fail when the worker is copied but not
  yet listed in `build.files` and `SERVED_FILES`; the layout-client test fails on a `NaN` reply
  before validation exists.
- **Reviewer:** **opus + mandatory independent review** (supply chain, CSP, served files).
  **OWNER-RUN:** no. The copied worker file's SHA-256 is asserted against the pinned 0.11.0
  package's, so the file ships unmodified as ruling (c) requires.

#### GV2.3 — The graph screen, static
- **Files:** new `console/src/renderer/screens/graph.ts`, new `console/src/renderer/graph/model.ts`
  (document → layout input: stream columns, release boxes, nodes), new
  `console/src/renderer/graph/layout.ts` (caches GV2.2's client results by structure hash),
  `console/src/renderer/entry.ts` (`VIEWS` gains `graph`), `console/src/renderer/theme/screens.css`,
  new `console/test/unit/graph.test.ts`, new `console/test/unit/graph-model.test.ts`, new
  `console/test/e2e/graph.spec.ts`.
- **Interfaces:** consumes `source.planStatus()`, the live feed, the worker. Produces: tabs CLI,
  Console, Mobile, All (columns); release boxes headed "Stream · Name" with "n of m done" bars;
  finished releases (GV1.6's `finished`) as chips grouped by stream, expanded only on click;
  cross-release edges within one plan drawn faint and thin, highlighted when a node is selected;
  nodes with title, PR number, "run …xxxx";
  states with words (`implementing`, `in review`, `fixing`, `queued`, `waiting`, `halted`,
  `stuck`, `blocked`, `ready`, `done`, `critical`); critical path edges thicker and distinct;
  blocked dashed; zoom, pinch, fit; a list view of the same document; keyboard traversal; a node
  opens the run screen, or the PR through `link.openExternal`.
- **Schema/rules/config:** none.
- **Tests:** every state has a word in the DOM; a finished chip expands only on click; a
  cross-release edge is faint until its node is selected; a hostile title renders as text; layout is not
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

#### GV2.5 — A live run's PR number in the watch feed (optional)
**Optional since review round 1:** `plan status` already prints a running node's draft PR from
`record_pr` (GV1.6, F9). This task only matters if the screen should show the PR before its next
`plan status` refresh.
- **Files:** `internal/watch/json.go`, `internal/watch/json_test.go`, `console/docs/json-contract.md`
  (§2), `console/src/shared/schema.ts`, `console/test/unit/schema.test.ts`.
- **Interfaces:** `pr_number` on a live `JSONRun`, from the run record's `PR` (the value
  `runview.Row.RecordPR` already holds).
- **Schema/rules/config:** additive field.
- **Tests:** `TestLiveRunCarriesDraftPR`, `TestLiveRunWithoutPRHasNoField`; the decoder accepts it.
- **Command:** `GOFLAGS=-p=2 go test ./internal/watch -run 'LiveRun' -count=1 -timeout 300s && cd console && npx vitest run test/unit/schema.test.ts`
- **Depends on:** GV1.6, GV2.1.
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
- **Written:** the row and the section are in `docs/gcp-live-checklist.md`; the check itself stays
  **NOT RUN**.

### Slice 3 — the write (GD2) — blocked until the coordinator's record exists (ruling (a))

#### GV3.1 — The roadmap write command
- **Files:** designed with the coordinator's record. Option 2 (declined by ruling (a), kept for
  reference): new `internal/plan/move.go`, `internal/plan/move_test.go`,
  new `internal/cli/plan_move.go`. Option 3 (the route that comes with the record): `internal/coordinator/` (a move record written with
  `GetETag` + `PutIfMatch`), new `internal/cli/roadmap.go`, `internal/cli/root.go`, and the rules or
  front-door change the coordinator plan would have to take. Under ruling (a), nothing until the record exists.
- **Interfaces:** `<command> MOVE <plan>/<task> --to <stream>/<release> [--expect <from>] --json`,
  printing the before/after and refusing when `--expect` no longer matches.
- **Tests:** stale `--expect` refused and nothing moves; under option 2, a move refused when the
  artifact file has uncommitted changes (D30's "clobber uncommitted work"); a move that makes an edge backwards is
  reported; the inverse move restores the document byte for byte.
- **Command:** `GOFLAGS=-p=2 go test ./internal/plan ./internal/coordinator ./internal/cli -run 'Move|Roadmap' -count=1 -timeout 300s`
- **Depends on:** the coordinator's record on `main` (ruling (a): option 1 now, the write with the record); the write route into it (front door or grant) is designed with the coordinator then. The option-2 file list above is kept only as the declined alternative.
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
  before/after, every title in it sanitized and length-capped so a title cannot forge the from/to
  line; undo is the inverse move, previewed the same way.
- **Tests:** a node or release id not in the last document is refused; a title containing "→" and
  a release name renders inside its own capped field; a declined confirmation
  spawns nothing; the confirmation text is composed from named fields; undo issues the inverse
  argv.
- **Command:** `cd console && npx vitest run test/unit/graph-move.test.ts test/unit/ipc.test.ts test/unit/spawn.test.ts && npm run typecheck`
- **Depends on:** GV3.1, GV2.4.
- **Commit:** `console: drag a task to another release, behind a preview and a confirmation`
- **Red step:** the "id not in the last document" test fails against a main process that forwards
  the renderer's ids.
- **Reviewer:** **opus + mandatory independent review**. **OWNER-RUN:** no.

### Slice 4 — the whole project, task-level — blocked on GD1's record (ruling (d): release assignment later)

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

**External gates:** none on Slices 0–2 since the owner's rulings of 2026-10-22. GV3.1 and GV4.1
wait on the coordinator's record (0.11.0).

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
| blocked | GV3.1 → GV3.2 and GV4.1 (the coordinator's record, rulings (a) and (d)) |

**Critical path:** GV1.1 → GV1.6 → GV2.1 → GV2.3 → GV2.4 → GV2.6 (six tasks). GV1.2, GV1.3 and
GV1.4 start chains of the same length into GV1.6; the smallest id decides the tie (C8), so the
computed path starts at GV1.1. The longest L0 task in effort is GV1.2, which the count does not
see.

**What must be sequential, and why:** GV1.6 needs the four L0 data tasks' interfaces; GV2.1 needs
GV1.6's contract; GV2.3 needs the operation and the worker; GV2.4 animates GV2.3's states. GV2.5
sits at L3 because `console/src/shared/schema.ts` is GV2.1's at L2.

**Shared files** (every file written by more than one task, with its writers' levels):

| File | Writers (level) |
| --- | --- |
| `console/docs/json-contract.md` | GV1.3 (L0), GV1.6 (L1), GV2.5 (L3) |
| `internal/watch/json.go`, `internal/watch/json_test.go` | GV1.2 (L0), GV2.5 (L3) |
| `internal/cli/plan.go` | GV1.6 (L1) |
| `internal/streams/` (package) | GV1.4 (L0: `streams.go`, `streams_test.go`), GV1.5 (L1: new `cmd/streams/main.go`) |
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
