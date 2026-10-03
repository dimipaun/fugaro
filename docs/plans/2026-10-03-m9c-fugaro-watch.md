# M9c — `fugaro watch` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax. Briefs to implementers stay short: the task text plus the Review Focus line that applies.

**Goal:** `fugaro watch` is a live terminal view of one Fugaro project's budget backend: the project's total against its caps, one block per repository (spend vs cap, burn rate, kill state), and one row per running agent (stage, round, age, spend, silent/lost/near-deadline flags). `k`/`K` kill and `r`/`R` resume with a confirmation, through the same code and permissions as `fugaro budget kill|resume`. It only reads what M9b writes; no new cloud resources, no new rules.

**Spec:** [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §0, §5.8, §6.2, §6.5, §7, §10–§12, §14, §15 task 17, D13; [m9-spec-v3-reconciliation.md](../design/m9-spec-v3-reconciliation.md) (rulings 1, 2, 5, 7); v3 source §8–§9 (layout). Built before this: M9b [2026-10-02-m9b-firebase-counters-and-budget.md](2026-10-02-m9b-firebase-counters-and-budget.md), live since #55–#59.

## What is built and what this reuses

| Need | Already there |
|---|---|
| REST + SSE client, typed errors | `internal/rtdb`: `Stream(ctx, path)` yields `put`/`patch`/`keep-alive`/`cancel`/`auth_revoked` and `error` events (unwrapping to `ErrUnavailable`/`ErrPermission`), reconnects itself (1 s to 30 s backoff, 90 s idle timeout), and every connect starts with a full `put "/"`; `ServerNow()` gives the server clock |
| Opening the project's database as the person | `openBudget(ctx, cloudOptions)` in `internal/cli/budget.go`: project selection, ADC with the budget scopes, `checkDBProject` (the `fugaro/project` mark), viewer-role error text via `dbErr` |
| Data model and paths | `internal/budget/model.go`: `Caps`, `Kill`, `Counters`, `AgentEntry`, `PathCapsGlobal`, `PathKillGlobal`, `PathKillRepo(slug)`, `PathSpendGlobal(day)`, `Day`, `DayDate`, `Key`/`Unkey` |
| The same read as a snapshot | `runBudgetShow` (one `GET` of `config`, `spend/<day>/global`, `spend/<day>/repos`, `agents`) |
| Kill write with ETag and `by` | the loop in `runBudgetKill` (`GetETag`, `PutIfMatch`, retry on 412, `adminDenied` on 401) |
| Terminal-safe text | `internal/cli/safetext.go` (`oneLine`) |
| Fake backend | `internal/gcpfake` RTDB: `Set`, `Value`, `DenyNext`, `SendKeepAlive`, `DropStreams`, `SendCancel`, `SetClock` |
| Redraw loop to compare with | `fugaro ls --watch`: polls the runs bucket every 10 s, clears the screen, stops when all runs settle; no Bubble Tea |

The live registry entry is `/agents/<slug>/<run> = {auth, repo, requestedBy, stage, stageDeadline, startedAt, title, updatedAt, workflow}` (plus `round`, `verify`, `coder`, `reviewer`, `prUrl`, `spent`, `halted` when a run has them). Counters are `spend/<day>/global` and `spend/<day>/repos/<slug>` (`counted`, `spent`, `notional`, `calls`, `byModel`); switches `config/kill/global` and `config/kill/repos/<slug>`. Slug keys are escaped (`budget.Unkey`).

## Decisions already made (binding)

Each Fugaro project has its own Firebase project, so "global" is this project's total and there is no cross-project view (ruling 1). Caps count **model dollars only**; compute is reported, never capped (ruling 2). `oauth` runs show **notional** dollars, labelled, and count toward no dollar cap (ruling 7). The registry is heartbeats plus the sweeper (D12): watch flags silent after 60 s and lost after 3 min without an `updatedAt` change; it never writes the registry. Bubble Tea (D13). Everything is Go. No backward compatibility (memory). Anything that changes real resources or costs money is **⚠ CONFIRM**, its own question.

## Rulings (settled here; each says what it costs if wrong)

**W1. Read as the person, not as a run.** Viewers (`firebasedatabase.viewer`: launchers, operators, owners, editors) read over IAM, which **bypasses the rules**; the rules' read grants are for run tokens only, so nothing in `rules.json.tmpl` changes. Watch uses `openBudget` unchanged. A reader without the role gets `dbErr`'s message (exit 1) before the UI starts. *If wrong:* a viewer sees nothing; pinned by `TestWatchNeedsViewerRole`.

**W2. Four streams: `config`, `spend/<today>/global`, `spend/<today>/repos`, `agents`** (design §7 said three; amended). A stream per path, each folded into one in-memory tree by `rtdb.Tree` (a pure `Apply(path, data, patch bool)`). The day streams are re-opened when the server clock (`ServerNow`, else the local clock) crosses UTC midnight, with the old pair closed first. *If wrong:* a stale yesterday; pinned by `TestMidnightResubscribe`.

**W3. Staleness and outage.** `stale` = no event of any kind, keep-alive included, for **45 s** (keep-alives come about every 30 s; design §7's 30 s would flap). Header then reads `⚠ live data stale (Ns)`. An `error` event shows `⚠ OFFLINE (reconnecting, <reason>)` and keeps the last frame, greyed, with its age; `ErrPermission` reads `⚠ access refused: you need the Viewer role` and does not retry silently forever. The first `put "/"` after an outage clears it; the view is rebuilt from that put, so nothing is missed. Kill and resume are **disabled** while offline or stale (the write would race an unseen state). Pinned by `TestStaleBanner`, `TestOfflineDisablesKeys`.

**W4. Polling fallback.** SSE can be blocked by proxies that buffer. After 3 consecutive `error` events with the same cause, if a plain `GET` of the four paths succeeds, watch switches to polling every 5 s (`--poll` forces it; `--interval` sets it) and shows `polling`. It tries the stream again every 60 s. *If wrong:* a proxy user sees "offline" forever; pinned by `TestFallsBackToPolling`.

**W5. Burn rate.** Client-side, a rolling 5-minute window of `spent + notional` samples, one per change event, per repository and for the project, in $/min (design §7 said `counted`; that jumps by whole leases of $0.25–$2, so `spent`, which heartbeats report every 15 s, is the honest slope; amended). The window starts empty after a (re)connect, so the first 60 s show `…`. Highlight (`⚠ fast`) above `watch.burn_alert_usd_per_hour` (new optional key in the project config; default: the effective daily cap spread over 8 h, none when no cap). Pinned by `TestBurnWindow`.

**W6. Stuck agents.** Silent after 60 s, lost after 180 s without an `updatedAt` change (fixed, design §6.5); amber when `now > stageStartedAt + 0.8·(stageDeadline − stageStartedAt)`; red `OVER` past `stageDeadline`. `halted` entries show the reason. The `l` cross-check against Cloud Run executions is **not** built: the sweeper (15 min) clears dead entries, and a lost run shows `lost, sweeper pending`. Thresholds are constants, not config. Pinned by `TestStuckClassification`.

**W7. Compute is not shown as dollars in M9c.** RTDB holds no compute; `computeUsd` arrives with M9d. The project line shows `N runs · H run-hours` (sum of `now − startedAt`), so the user sees the exposure now and M9d fills the dollar figure. Open question 1.

**W8. Layout.** Header (project, mode `observe|enforce`, day, connection state), a project line (`counted`/cap bar, spent, notional, burn), a block per repository (cap bar, `spent`, burn, kill state `KILLED by <who> <time> "<reason>"`), runs beneath (title, stage, `rN`, verify step, models, age, spend, flags). Width tiers: **≥100** everything; **70–99** drop models and verify; **40–69** two lines per run, no bars (percent only); **<40** `terminal too narrow (need 40 columns)`. Rows are clipped by display width (`runewidth`), never wrapped mid-escape. Bars fall back to `#`/`.` with `--ascii` or a non-UTF-8 locale. Long lists scroll in a viewport; `space` collapses a repository; selection survives a re-sort (key by slug and run id). Order: killed first, then by spend, runs by `startedAt`.

**W9. Colour and accessibility.** `NO_COLOR` and `TERM=dumb` switch colour off. Meaning never rides on colour alone: `⚠`, `SILENT`, `LOST`, `KILLED`, `OVER`, `FAST`, `NOTIONAL` are words or symbols; `--ascii` replaces `⚠ ▓░ ▸` with `!`, `#.`, `>`. Palette from `lipgloss.AdaptiveColor` (readable on light and dark). Every job-written string (title, stage, repo, reason, `by`) passes `oneLine` and a width clip **at the model boundary**, so the view never holds an escape.

**W10. Keys and writes.** Movement is `↑`/`↓` and `PgUp`/`PgDn` only (`k`/`K` are kill, so no vim keys). `space` collapse, `?` help, `q`/`Esc`/`Ctrl-C` quit (listeners close; cloud jobs untouched). Lowercase acts on the selected repository, CAPITAL on the whole project (the bigger blast radius). `k` kills the selected repository: `y` or Enter to confirm. `K` kills the whole project: type the project name, then an optional one-line reason (default `from fugaro watch`). `r` resumes the selected repository: type its repository name. `R` resumes the whole project: type the project name. (Ruling of the controller; supersedes the first draft, which had k = all and K = repo.) The write is `budget.SetKill` (T5 extracts it from `runBudgetKill`: `GetETag`, `PutIfMatch`, `by = lc.Me`, 412 retried up to `setAttempts`, `already killed by X` reported as a no-op) so the CLI and the TUI cannot diverge. **No pre-check of rights:** the database is the authority; a 401 on the write shows `you are not a budget admin for project X: ask an owner or editor, or to be added to terraform.budget_admins` (the existing `adminDenied` text) and changes nothing. The new state appears when the stream delivers it (not optimistically), with a `killing…` line until then or 5 s. Resuming a repository while the global switch is on shows the existing warning.

**W11. Non-TTY and scripts.** `fugaro watch --json` prints one JSON document per state change, at most 1/s, one line each (`project`, `day`, `connection`, `total`, `repos[]`, `runs[]`, with `notional` flagged); `--once` prints one snapshot and exits (works with `--json` and plain). Stdout not a terminal and no `--json` means plain lines, one block per change at most every `--interval` (default 10 s), no cursor codes. The TUI needs a terminal on stdin and stdout, else it falls back to plain. Kill keys do not exist in these modes (`fugaro budget` is the script path).

**W12. No Firebase (design §7 last bullet).** A project without `budget.rtdb_url` gets degraded mode: `loadRows` (ls's reader) every 10 s, runs grouped by repository, cost from `result.json`, no caps, no kill keys, banner `no budget backend: showing run records only`. It is a small view over code `ls` already has.

**W13. `ls --watch` stays.** It answers "did my runs finish" from the runs bucket, needs no Firebase and no TTY control, and is in v1 §10. `watch` answers "what is it costing right now and can I stop it". README and the `ls --watch` help point at each other. *If wrong:* none; removing it later is a one-line deletion.

**W14. Bubble Tea structure.** `internal/watch` has no TUI imports in its core. `State` (pure, from `Tree`) -> `View` (the typed snapshot: project line, repo blocks, run rows, flags, connection, `Now`) is computed by `Build(State, now, Config)`. The TUI is a thin `tea.Model`: `Update` handles `tea.KeyMsg`, `tea.WindowSizeMsg`, `sourceMsg` (events from the supervisor channel), `tickMsg` (1 s: ages, stale timer, burn window) and `actionDoneMsg`; `View` renders a `View` snapshot with lipgloss. The supervisor is a goroutine that owns the four streams and posts messages; the model never touches the network except through `Action` functions injected at construction (so tests pass fakes). Redraws are coalesced to at most 10 per second (events mark dirty; the tick renders). With 200 agents a frame is one pass over a sorted slice: no per-event allocation beyond the patch.

**W15. Testing.** No `teatest` (it lives in `x/exp` and moves): the model is driven directly, `m.Update(msg)` then compare `m.View()` to **golden files** (`testdata/*.golden`, written with `-update`, reviewed in the diff) at fixed widths (120, 80, 50, 38), with `NO_COLOR` for the text goldens and one colour golden asserting ANSI shape. One smoke test runs `tea.NewProgram` with `WithInput`/`WithOutput` buffers and a fake backend to prove the wiring and clean exit. `Build`, `Tree`, burn, stuck and the supervisor are table and fake-stream tests (the gcpfake RTDB drops streams, sends keep-alives, cancels, denies writes).

## Review Focus

Per-task reviews are only for the one task that can stop or resume spending; the rest get one review of the branch at the end (user's token-economy rule).

1. **A kill or resume that does the wrong thing or lies.** T5: `TestKillAllNeedsTypedWord`, `TestKillRepoNeedsY`, `TestResumeNeedsRepoName`, `TestKeysDisabledWhenOffline`, `TestViewerRefusalShown` (401 -> the `adminDenied` text, nothing changed), `TestAlreadyKilledNoop`, `TestConflictRereads`, `TestSelectionSurvivesResort` (the key acts on the repository selected when it was pressed, not the one under the cursor after a re-sort; the confirm prompt names the target), `TestResumeWarnsGlobalStillOn`, `TestTUIAndCLIShareSetKill`.
2. **Terminal injection and layout corruption from job-written text.** T1 `TestHostileTitlesSanitized` (ESC, OSC, CSI, BiDi, zero-width, newline, 10 KB), T4 `TestWideRunesClipByWidth`.
3. **Showing stale data as live.** T2/T3: `TestStaleBanner`, `TestOfflineGreysFrame`, `TestMidnightResubscribe`, `TestPutAfterReconnectReplacesTree` (a run that finished during an outage disappears).
4. **A wrong number.** `TestBurnWindow` (window start, counter reset at midnight, negative deltas ignored), `TestNotionalNeverInCapBar` (notional is labelled and out of the cap bar), `TestCapBarMissingCap` (no cap: `no cap`, not 0%).

## File Structure

| Path | Role |
|---|---|
| `internal/rtdb/tree.go` | `Tree.Apply(path, data, patch)`, `Get(path, out)`; pure |
| `internal/watch/state.go`, `build.go` | `State`, `Build` -> `View`, burn window, stuck classification, sanitising |
| `internal/watch/source.go` | supervisor: four streams, midnight, staleness clock, polling fallback |
| `internal/watch/tui.go`, `render.go`, `keys.go` | the `tea.Model`, lipgloss rendering, key handling and prompts |
| `internal/watch/testdata/` | golden frames |
| `internal/budget/kill.go` (extend) | `SetKill` extracted from `internal/cli/budget.go` |
| `internal/cli/watch.go` | the command, flags, mode choice (TUI, plain, `--json`, `--once`, degraded) |
| `internal/localcfg` | optional `watch.burn_alert_usd_per_hour` |
| `docs/design/v1.md` (CLI table), `README.md`, `docs/gcp-live-checklist.md` | docs and the live check |

## Task dependency graph and lanes

| Task | Size | Lane | Depends on |
|---|---|---|---|
| T1 `rtdb.Tree`, `internal/watch` State/Build, burn, stuck, sanitising | M | A | — |
| T2 supervisor: streams, midnight, stale/offline, polling | M | A | T1 |
| T3 `fugaro watch` command: flags, plain, `--json`, `--once`, degraded mode, config key | S | B | T1 (T2 for live) |
| T4 Bubble Tea model, rendering, tiers, goldens | L | C | T1 |
| T5 `budget.SetKill` extraction and the kill/resume flow in the TUI **(critical review)** | M | C | T4 |
| T6 docs | S | B | T3, T5 |
| T7 live verification | S | controller | all |

New dependencies (pinned, reviewed in T4): `charmbracelet/bubbletea`, `charmbracelet/lipgloss`, `mattn/go-runewidth` (likely already indirect). `bubbles` only if a prompt needs `textinput`; otherwise a 20-line input handler. Each new module goes through the existing `go mod verify`, vet and Trivy CI.

### Task 1 (M, lane A): Tree, State, Build

**Files:** `internal/rtdb/tree.go`, `internal/watch/state.go`, `build.go` and tests.

- `Tree` folds `put`/`patch` events (path relative to the stream root, JSON `null` deletes, a patch merges children). `State` holds the four subtrees, the connection state and the burn samples; `Build` returns the `View` (project line, repository blocks keyed by `Unkey` slug with the repository name from the entries' `repo`, run rows, flags, `Now`). Titles and strings pass `oneLine` and a width clip here.
- [ ] **Failing tests first:** `TestTreePutPatchDelete`, `TestPutAfterReconnectReplacesTree`, `TestBuildGroupsByRepo`, `TestCapBarMissingCap`, `TestNotionalNeverInCapBar`, `TestBurnWindow`, `TestStuckClassification`, `TestHostileTitlesSanitized`, `TestAbsentFieldsShowDash`, `TestKilledFirst`.
- [ ] Commit: `watch: the state tree and the view model`

### Task 2 (M, lane A): Supervisor

**Files:** `internal/watch/source.go`, tests against `gcpfake` RTDB.

- Opens `config`, `spend/<day>/global`, `spend/<day>/repos` and `agents` through `db.Stream`; forwards events as messages; the 1 s clock raises `stale` (45 s) and keeps `offline`/`refused`; re-opens the day pair at UTC midnight; after 3 identical errors tries a `GET` and switches to polling (`--poll`, `--interval`), retrying SSE every 60 s; closes everything on cancel.
- [ ] **Failing tests first:** `TestFourStreamsOpened`, `TestMidnightResubscribe` (fake clock), `TestStaleBanner`, `TestOfflineGreysFrame`, `TestPermissionErrorStopsRetrySpam`, `TestFallsBackToPolling`, `TestCancelClosesStreams`, `TestNoGoroutineLeak`.
- [ ] Commit: `watch: the stream supervisor`

### Task 3 (S, lane B): The command

**Files:** `internal/cli/watch.go`, `internal/cli/root.go`, `internal/localcfg`, tests.

- Flags `--repo R`, `--json`, `--once`, `--poll`, `--interval`, `--ascii`, `--no-color`; project and `--repo` selection as `budget show`; mode choice per W11, degraded mode per W12 (reusing `loadRows`). `localcfg` accepts `watch.burn_alert_usd_per_hour` (strict decoding, finite, ≥ 0).
- [ ] **Failing tests first:** `TestWatchNeedsViewerRole`, `TestWatchRefusesWithoutFirebaseConfigUsesDegraded`, `TestWatchOnceJSON` (project header, notional flag), `TestWatchPlainNoEscapes`, `TestWatchWrongProjectDatabase` (the mark check), `TestBurnAlertConfigStrict`.
- [ ] Commit: `watch: the command, plain and JSON output`

### Task 4 (L, lane C): The TUI

**Files:** `internal/watch/tui.go`, `render.go`, `keys.go`, `testdata/`, `go.mod`.

- The `tea.Model` of W14: layout tiers (W8), selection and collapse, viewport scrolling, colour and `--ascii` (W9), help line, the header states of W3. Keys here do movement, collapse, help, quit; prompts for kill and resume arrive in T5, so the footer lists them only when actions are injected.
- [ ] **Failing tests first (goldens at 120/80/50/38 columns):** `TestFrameWide`, `TestFrameMedium`, `TestFrameNarrow`, `TestFrameTooNarrow`, `TestFrameOffline`, `TestFrameKilledRepo`, `TestFrameManyAgentsScrolls` (200 agents), `TestWideRunesClipByWidth`, `TestNoColorHasNoEscapes`, `TestAsciiMode`, `TestQuitClosesListeners`, `TestProgramSmoke`.
- [ ] Commit: `watch: the Bubble Tea view`

### Task 5 (M, lane C): Kill and resume **(critical: its own review)**

**Files:** `internal/budget/kill.go`, `internal/cli/budget.go` (call `SetKill`), `internal/watch/keys.go`, `tui.go`, tests.

- Extract the `GetETag`/`PutIfMatch` loop into `budget.SetKill(ctx, db, scope, on, by, reason) (Result, error)` (`Result`: written, already-in-state, previous). `runBudgetKill` keeps its prompts and calls it. The TUI's prompts per W10, the target captured when the key is pressed and shown in the prompt (`kill repository aurora/web? y/N`), keys off when offline or stale, the refusal text on 401, an in-flight guard so a second key waits for the first write.
- [ ] **Failing tests first:** the five names in Review Focus 1, plus `TestSetKillBothCallers`, `TestKillReasonSanitized`, `TestPromptEscCancels`, `TestSecondKeyWaitsForWrite`.
- [ ] Commit: `watch: kill and resume keys`

### Task 6 (S, lane B): Docs

**Files:** `docs/design/v1.md` (command table, §10), `README.md`, `docs/gcp-live-checklist.md` (check 22), `ls --watch` help text.

- [ ] Commit: `docs: fugaro watch`

### Task 7 (S, controller): Live verification

One **⚠ CONFIRM** per step; the user runs anything that touches the real project; record every `FACT` in the PR. Preconditions: T1–T6 merged, M9b's Firebase project live, caps set (M9b step 4).

1. Read-only: `fugaro watch --once` and `--json --once` against the live project with no run: caps, mode, no agents.
2. **⚠ CONFIRM — the TUI at the user's terminal** (`fugaro watch`): header, project line and bars match `fugaro budget show` side by side; resize to 100/70/45 columns; `NO_COLOR=1 fugaro watch`; inside tmux; quit with `q` and confirm the run list is unchanged.
3. **⚠ CONFIRM — one sandbox `oauth` run** (the user's task text and go-ahead; EdgeWeb only on request): the entry appears within a few seconds, stage and age move, spend shows `notional`, the entry disappears at the end; note the real update latency (`FACT`).
4. **⚠ CONFIRM — `K` on the sandbox repository during a second sandbox run**: the run halts within seconds, the block shows `KILLED by <you>`, then `r`, typing the repository name, resumes. `k` is tried only up to the typed-word prompt and cancelled with `Esc` (a project-wide kill is not worth the risk live).
5. **⚠ CONFIRM — a viewer-only identity** (the user's second account with only `firebasedatabase.viewer`, or `gcloud auth application-default login` as it): `watch` reads; `K` shows the not-a-budget-admin text and changes nothing. Skipped, with the unit test as the only evidence, if no such identity exists.
6. Outage: turn Wi-Fi off for 60 s with the TUI open: `stale` then `OFFLINE`, frame greyed, keys refused; on return the view rebuilds. Then `--poll` against the same project.
7. UTC midnight: leave the TUI open across 00:00 UTC (user's call), or accept `TestMidnightResubscribe` as the evidence; the day label and counters must roll.

*Rollback:* none needed: watch only reads, and its only writes are the kill switches the user typed; `fugaro budget resume --all` is the emergency undo.

## Unverified assumptions

- A1. RTDB `put "/"` on every connect, and a keep-alive about every 30 s, hold for the four paths (M9b relied on the same; check in step 3's `FACT`).
- A2. `firebasedatabase.viewer` can open the `agents` and `spend/<day>/repos` streams (M9b's `budget show` reads them with `GET`; a stream might be treated differently). Step 1 settles it; if not, W1 gets a fallback to polling.
- A3. `agents` stays small (20 repositories, a few dozen live runs): at 200 entries the first `put` is about 60 KB.

## Risks

- **A kill key pressed on the wrong target.** The prompt names the target and it is captured at keypress (Review Focus 1).
- **Bubble Tea and lipgloss API drift** across versions: pin exact versions, and keep rendering behind `View` -> string so a swap is local.
- **Golden frames that rot** on lipgloss updates: goldens run with `NO_COLOR`, one colour golden only.
- **A viewer sees every repository's titles** (task first lines, redacted at the source). The project's launchers can already read every `task.json`; no new exposure.

## Open questions for the user

1. **Compute dollars (W7).** Not in RTDB until M9d. *Recommend:* M9c shows run-hours only; M9d adds `computeUsd` to the project line and `report`. Alternative: estimate from Cloud Run rates here (a second price table to keep right).
2. **`l` cross-check of lost runs (W6).** *Recommend:* skip; the 15-minute sweeper already does it and the row says `sweeper pending`.
3. **Confirmation words (W10).** The design's: `kill` for all, `y` for one repository, the repository name to resume it, `resume` for the project. `budget kill|resume --all` asks for the project name instead. *Recommend:* keep the design's words in the TUI (different surface, typed deliberately); say if you want them identical.
4. **Burn alert (W5).** *Recommend:* an optional `watch.burn_alert_usd_per_hour` in the project config, default the daily cap over 8 h. Alternative: a constant, no config.
5. **Degraded mode (W12)** for a project without Firebase: *Recommend:* build it (small, reuses `ls`). Alternative: refuse with "no budget backend".
6. **A second identity for step 5** (viewer-only): do you have one? Otherwise the refusal path is unit-tested only.

## Execution

Subagent-driven. Lanes A (T1, T2), B (T3, T6) and C (T4, T5) overlap after T1. Only **T5** gets its own review (it stops and restarts spending); everything else is reviewed once on the whole branch, with the batch workflow's round-2 review. T7 is controller-only and one question per step; the web repository runs only on the user's go-ahead and task text.
