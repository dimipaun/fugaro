# Console improvements: implementation plan

**Status: plan only; nothing is implemented; no task approved.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md) (checked against `origin/main` at `01fe41f30`, 2026-11-18). **No task in it
is approved: each needs the owner's explicit go by name**, and `MW1`, `MW2` and `MWR` (console writes) and `SW3a` (a console
write that provisions cloud resources) each need a go of their own. The answers that shaped it, all given on
2026-11-18: **D2900** graph top to bottom; **D2901** fix the sparklines and keep Watch parity; **D2902** one
main-process modal for entry and confirmation; **D2903** lists for every object, writes only where fenced-safe;
**D2904** the wizard collects answers and hands terminal-only steps back; **D2905** no secret form; **D2906** measure,
then an opt-in stream; **D2907** a CLI caller-role read; **D2908** metadata for every writable key; **D2909** alerts
and project deletion declined.

## Conventions every part follows

- Rules files: `.claude/rules/console.md` for every `console/**` task (Node 22, exact-pinned dependencies, the allow-list tests, `TEXT_CLASSES`, the parallel-sections rule, goldens skipped in CI); `CLAUDE.md` for the CLI tasks. Run targeted tests, never a whole package (`internal/cli` takes about 25 minutes).
- **Always:** write the failing test first; run the task's command before the commit; `git status --short` and `git diff --stat` before every commit and `git add <paths>`; cite a `T#` and `D#` in the pull-request body.
- **Generated files:** `website/content/cli-reference.md` is never hand-edited; each task that adds a CLI flag regenerates it with `go run ./ee/clidoc` and runs `TestCliReferenceNamesEveryCommandAndFlag` and `go test ./ee/clidoc/`. It is the plan's one file several tasks may write at once (the owner's choice, 2026-11-18): a conflict in it is resolved by regenerating.
- **Ask first:** a new console argv or IPC operation (only AL, SU1, SW3c, MW1 and MW2 may add one or change an IPC shape); a CLI flag that is not named in this plan; a new dependency; a change to a wire document other than an added member.
- **Never:** edit `.github/workflows/`; re-record a screenshot golden; add a confirmation, a prompt or a `--yes` to the CLI (the no-prompt policy); let the console read, hold or pass a secret value (design rule 2); bump elkjs; change an existing `--json` document's meaning or shape; delete or skip a failing test.
- **Safety rules:** no secret-shaped literal in a test, fixture or doc; no live cloud action by an agent (the three Checks are the owner's); `fugaro update` is unaffected (no installed state changes: every CLI change is an added flag or member, so the release's `### For operators` is "run `fugaro update`", which asks nothing new).
- **Size limits:** `console/src/renderer/entry.ts` is about 8,950 lines, `console/src/main/spawn.ts` about 5,500 and `ipc.ts` about 4,300. A task adds a new file for new code and a small wiring change to these, never a reformat.

## Global constraints and review focus

Inputs and failure modes the spec implies and no single task owns, each with the task that pins it.

| Input or failure mode | Pinned by |
| --- | --- |
| Text from the CLI (labels, descriptions, row cells, event sentences) reaches the renderer or the modal unsanitised | SU1, MO-SH, SW3a (each runs every CLI string through `TEXT_CLASSES`) |
| An older CLI lacks the new members or flags | SU2a (no metadata: raw key and text control), RS2 (falls back to `runs.list`), RB4 (no answer: controls stay enabled), SW3a (the floor is unset: the wizard shows the command) |
| Reduced mode (a CLI the console does not know) misreads a figure | MO-B (`hidesView`), LP2 (the existing reduced-mode tests stay green) |
| A renderer frame tries to answer a write window or start setup | SU1, SW3a, MW1, MW2 (each has a mutation test) |
| A hostile process of the same user swaps the setup answers between the check and the CLI's read | ST1, SW3a (the answers go on stdin from bytes main holds; no file exists) |
| A Manage value parses as a flag, or a write lands on another installation | MW1, MW2 (a grammar per value, `--flag=value` words, the positional after `--`, `--project` set by main) |
| The answers file leaks or lingers | ST1 (contents never logged), SW3a (mode 0600, removed after the run) |
| A section's slow read blocks another | MO-SH (one read, busy mark and refusal per kind), RS2 |
| `runs ls` without `--stream` or any existing `--json` changes | RS1 (`TestRunsLsJSONUnchanged` written first), SM-E and WH1 (only added members) |
| A reset leaves a lower layer's value that the dialog calls "the default" | SU1 (R6: the built-in default is named as such, and the lower-layer caveat is shown) |
| Keys that take `--add`, `--remove` or `--add-entry` | SU1 (read-only with the command to copy) |
| Contrast below 4.5:1 anywhere in Live | LP2 (a test computes it from the CSS tokens) |
| A hostile renderer names the answers file, or a cloud project the owner did not see | AL (a renderer-supplied path is refused; the payload schema is closed), SW3a (main validates the answers, holds them as bytes in memory and hands them to the CLI on stdin, shows its own parsed copy in the window and applies with exactly the bytes it showed; the Terraform plan is recomputed at apply and the window says so) |
| A console yes is taken for a consent the CLI accepts | SW3a (`--json` confirms no step, so every step needing one arrives as `left_for_you`; a test asserts apply never passes a confirming word), ST1 (`TestSetupAnswersFileGrantsNoConsent`) |
| A hostile CLI floods a stream or a setup is killed half-way | AL (per-line, row and byte caps; a deadline of its own for `setup.*`, never the 90 s read timeout) |
| The answers file is read as consent | ST1 (`TestSetupAnswersFileGrantsNoConsent`) |
| Text from a repository or the CLI reaches the clipboard in a Copy-command | MG0 (`command.ts` refuses any token outside the safe grammar), MO-SH, MO-P, MO-C, SW3b |
| A write the first pass admits is destructive | MW1, MW2 (only `models add` as a widening and `image add` with `--no-apply`; `models use`, `models rm` and `image rm` stay Copy-commands, O5) |
| The `setup` and `models` rows of `NEVER_REACHABLE` and §3.4 rule 3 (`--gcp-project`, `--region`) | AL (keeps the `setup` command row and grants `setup.plan` and `setup.apply` by name through `except`; the answers reach the CLI only in the file main wrote), MW1 (the `models add` command and argv rows, granted by operation name through `except`) |
| A floor constant set before the CLI release carries the flag | AL (`null` until the release) |
| The docs tests pin `desktop-console.md`, `console-styling.md` and `json-contract.md` | AL, GF1, SM-E (each runs `go test ./internal/cli/ -run Docs`) |

## Slices and tasks


### Slice 1: Live and Graph (`live-pane`, `graph-flow`)

Ships alone: two readable screens, and the goldens are re-recorded by the owner.

#### LP1: the Watch-versus-Live field table and its test

- Acceptance: Every `json:"..."` member of `JSONDoc` and its row types in `internal/watch/json.go` (the test reads that file as text) is listed against the Live element that draws it or is marked 'not drawn, with the reason'; the test fails when a member is in neither column, so a `JSONDoc` member added later and missing from the parity fixture is still caught. The result names any gap, which becomes a task by amendment, never a silent addition.
- Files: `docs/console-improvements/live-watch-fields.md` (new); `console/test/unit/live-watch-fields.test.ts` (new)
- Interfaces: Consumes `internal/watch/json.go` as text and `console/test/fixtures/watch-parity.json`; produces the table.
- Tests: `live-watch-fields.test.ts` (new): every member of the parity fixture is accounted for.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/live-watch-fields.test.ts)`
- Red step: Add a member to a copy of the fixture inside the test: the test must fail naming it.
- Depends on: none
- Commit: `docs(console): the fields Watch shows and where Live draws them (F1, D2901)`
- Reviewer tier: standard; tier `small`, size 1

#### LP2: Live sparklines in place, readable, and a bounded run table

- Acceptance: Each sparkline is one fixed-size element updated in place (no per-sample `li` carrying a percent text); no text sits on a background below 4.5:1 (a test computes the ratio from the CSS tokens); a repository's run rows scroll inside a bounded box instead of lengthening the page; the sample caps and the Watch parity tests still pass.
- Files: `console/src/renderer/screens/live.ts`; `console/src/renderer/theme/screens.css`; `console/src/renderer/theme/flight-deck.css`; `console/test/unit/live.test.ts`; `console/test/unit/degraded.test.ts`
- Interfaces: Consumes the existing `WindowHistory` samples; produces no new export.
- Tests: `live.test.ts` (extended: one element per sparkline, contrast ratio of every text/background pair in the Live CSS, run box has a max height); `live-parity.test.ts` and `degraded.test.ts` unchanged and green.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/live.test.ts test/unit/live-parity.test.ts test/unit/degraded.test.ts)`
- Red step: The contrast test fails on `--text-muted` over `--accent` (about 1.24:1) before the fix.
- Depends on: none
- Commit: `console(live): sparklines drawn in place with readable contrast; run rows scroll (F2, F3, D2901)`
- Reviewer tier: standard; tier `medium`, size 2
- Owner-run: Check 710 re-records the Live goldens.

#### GF1: the graph lays out top to bottom and stacks wide levels

- Acceptance: `LAYOUT_OPTIONS` sets `elk.direction` to `DOWN`; a level wider than the pane wraps or stacks its cards so no layout is wider than the pane at the 195 px card floor; `console-styling.md` states the new direction and records the step-6a ruling as reversed on 2026-11-18 (D2900); elkjs stays at 0.12.1.
- Files: `console/src/renderer/graph/layout.ts`; `console/test/unit/graph.test.ts`; `console/test/unit/graph-model.test.ts`; `docs/console-styling/spec.md`
- Interfaces: Consumes `graph --json` and the existing worker; produces the same layout shape with new coordinates.
- Tests: `graph.test.ts` and `graph-model.test.ts` (the pinned direction and no-wrapping assertions replaced with the new ones); the Go docs tests that pin `console-styling.md`.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/graph.test.ts test/unit/graph-model.test.ts) && go test ./internal/cli/ -count=1 -run Docs`
- Red step: The updated direction assertion fails against the unchanged `layout.ts`.
- Depends on: none
- Commit: `console(graph): top-to-bottom layout, wide levels stack (F4, D2900)`
- Reviewer tier: standard; tier `medium`, size 2

#### GF2: the graph pane scrolls vertically only

- Acceptance: `div.graph-canvas-pane` has no horizontal scroll at the supported window widths and `section.graph svg.graph-canvas` fits the pane width; the e2e spec asserts `scrollWidth <= clientWidth` and keeps 'stacks the streams as rows, and keeps every card inside its own row'.
- Files: `console/src/renderer/theme/flight-deck.css`; `console/test/e2e/graph.spec.ts`
- Interfaces: Consumes GF1's layout.
- Tests: `graph.spec.ts` (extended).
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:e2e -- graph.spec.ts)`
- Red step: The new `scrollWidth` assertion fails while `overflow-x: auto` and `min-width: 100%` are in place.
- Depends on: **GF1** (the CSS follows the new layout's geometry; one behaviour split in two); **LP2** (both write `flight-deck.css`)
- Commit: `console(graph): vertical scroll only (F4, D2900)`
- Reviewer tier: standard; tier `small`, size 1
- Owner-run: Check 710 re-records the Graph goldens.


### Slice 2: the allow-list, Retry and role gating (`console-robust`)

Inert until used: argv rows behind unset CLI floors, plus the section Retry and write invalidation, which are useful alone.

#### C-OPS: the new operations' names and payload types (contract)

- Acceptance: `new-ops.ts` exports the names and the request and response types of `runs.stream`, `spend.stream`, `budget.stream`, `whoami`, `image.catalog.ls`, `recipes.ls`, `setup.plan`, `setup.apply` and `settings.editor.open` (the last with payload `{key}` only; `setup.plan` carries an answers object and never a path, `setup.apply` carries `{}`); `bridge-stub.ts` is a bridge that answers each operation from the console fixtures; the document says what each side may rely on (an unknown role reads as `none`, a stream without an `end` is interrupted, an answers object never names a file). Fixtures are inline in the stub; I-OPS holds them to the CLI goldens. `new-bridge.ts` exports `newOpsFrom(fugaro)`, the renderer's typed read of the nine methods over the structural bridge (as `readBridgeFrom` in `entry.ts` does for the old reads), so no consumer edits `entry.ts` to reach them; a missing method reads as 'not available'.
- Files: `console/src/shared/new-ops.ts` (new); `console/src/renderer/stubs/bridge-stub.ts` (new); `console/test/unit/new-ops.test.ts` (new); `docs/console-improvements/contract-ops.md` (new); `console/src/renderer/new-bridge.ts` (new)
- Interfaces: Produces the types, the stub bridge and the document; consumers import the types and the stub, the allow-list task (AL) implements the operations beside it and does not import them.
- Tests: `new-ops.test.ts` (new): the stub answers every operation with a fixture that decodes against its type.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/new-ops.test.ts)`
- Red step: A stub answer with a missing field fails the decode before the type exists.
- Depends on: none
- Commit: `console: the new operations' contract (F24)`
- Reviewer tier: standard; tier `small`, size 1

#### AL: the argv rows, IPC operations, bridge, floors, the narrowed never-table and the section 5.1 and contract text

- Acceptance: `SPAWN_COMMANDS` and `CONSOLE_OPERATIONS` gain, as one change, `runs.stream` (`runs ls --stream --json --since <w>`), `whoami` (`user ls --me --json`), `spend.stream` (the `spend.report` argv plus `--stream`), `budget.stream` (the `budget.show` argv plus `--stream`), `image.catalog.ls` (`image ls --json`), `recipes.ls` (`recipes ls --json`), `setup.plan` (`setup --plan-only --answers-file - --events --json`), `setup.apply` (`setup --answers-file - --events --json`, payload `{}`: apply carries nothing and reuses the file and digest main stored at plan time, so a renderer cannot slip in new answers at apply) and `settings.editor.open` (payload `{key}` only, validated with `SETTINGS_KEY_RE` and the closed payload schema; the layer and phase stay main-process decided), each with its sanitiser class and no flag the renderer can choose; each new operation is added to the closed `ConsoleHandlers` type in `ipc.ts`, to the `Omit` of `CliBackedHandlers` in `console/src/source/source.ts`, and to the `gated` table in `console/src/main/index.ts` with an explicit handler that answers 'not available in this build', so the existing startup check (`refuses to build a surface with a handler missing`) still holds and still stops the console when a handler is truly missing; the handlers sit behind the same wrappers as every other (`lapseHandlers`, `reducedModeHandlers`, and the refused-console rule that a refused console registers nothing), so a lapsed console refuses `setup.apply` and a refused console has no channel, and duplicate or unknown registrations are refused; I-OPS replaces the explicit stubs through the `gated` table in `console/src/main/index.ts`; the answers reach the child on its stdin as the existing `stdin` argument kind (`spawn.ts`, the kind `run --task-file -` uses), from bytes main holds in memory, so no file exists for another process to change between the check and the CLI's read; the renderer sends only an answers object, which a payload schema refuses if it carries a path; `setup.apply` carries `refusedAfterLapse` as `settings.edit` does (the licence-lapse read-only gate, nothing more); **bounds:** `runs.stream`, `spend.stream`, `budget.stream` and the setup event feed are read under `MAX_FEED_LINE_LENGTH` per line, at most 10,000 rows and at most 16 MiB in all (constants in `ipc.ts`, each pinned by a test); a feed that reaches a cap is ended, the child killed, and the section shows it as an interrupted read with Retry (R1), never a partial list as complete; `setup.*` have a deadline of their own, 60 minutes (never the 90 s `READ_TIMEOUT_MS`, which would kill an apply part-way; the run ends on the CLI's exit or the user's cancel); the `setup` command row of `NEVER_REACHABLE` **stays**, and `except` (today a flag-row field, `ipc.ts`) is extended to command rows so that row grants exactly the operations `setup.plan` and `setup.apply` by name, with `assertSpawnSurface` and the `ipc.test.ts` pin of the `setup` never-row following (a total ban with two named exceptions, never a denylist); the `models` never-rows are left for MW1; design §3.4 rule 3 (the renderer never names a cloud project; the existing `--gcp-project` and `--region` rows guard `run`) is amended to say that the wizard's answers, validated in main and shown in the confirmation window, are the one way a project name reaches the CLI from the console; `version.ts` gains one plain floor constant per new row, each `string | null = null` until the CLI release that carries it and each pinned by `version.test.ts`; §5.1 of `desktop-console.md` gains a row each, rule 12 describes the single modal (D2902) and the 2026-10-20 ruling is amended (D2904); `console/docs/json-contract.md` says seven documents are line-delimited (today's three, `watch`, `logs` and `graph --watch`, plus `runs ls --stream`, `report --stream`, `budget show --stream` and `setup --events`) and states the new read documents and the two write operations, with its pinned sentences in `docs_desktop_console_test.go` updated; the old `settings.edit` operation is unchanged and stays main-confirmed. `REDUCED_VIEW_OPERATIONS` gains `spend.stream` under `spend` and `budget.stream` under `caps`, so reduced mode withholds them in main as well as in the renderer.
- Files: `console/src/main/spawn.ts`; `console/src/main/ipc.ts`; `console/src/main/index.ts`; `console/src/source/source.ts`; `console/src/preload/index.ts`; `console/src/shared/version.ts`; `console/test/unit/spawn.test.ts`; `console/test/unit/ipc.test.ts`; `console/test/unit/version.test.ts`; `docs/desktop-console/spec.md`; `console/docs/json-contract.md`; `internal/cli/docs_desktop_console_test.go`
- Interfaces: Consumes RS1, WH1 and ST2 (the CLI in the tree registers every flag word the allow-list names, which `TestDocsDesktopConsoleSpawnWordsAreFlagsTheCLIRegisters` demands: `--stream` arrives with RS1, so RS4 need not precede it; RS3 must, because report's own ee contract test reads the docs AL writes; their rows stay behind floors that are `null` until the release); produces the argv table, the operations, the bridge methods, the stub handlers and the floors every console task spawns through.
- Tests: `spawn.test.ts` (an exact-argv pin per row and a mutation per row that a widened argv is refused), `ipc.test.ts` (each operation pinned, a renderer-supplied path refused, a frame of another origin refused, the `setup` never-rows), `pinning.test.ts`, `version.test.ts`, `TestDocsDesktopConsole*` in `internal/cli/docs_desktop_console_test.go`. The `refuses to build a surface with a handler missing` test in `ipc.test.ts` stays green; new tests: a lapsed console refuses `setup.apply`, a refused console has no channel for the new operations. Also: `spend.stream` and `budget.stream` are unreachable in reduced mode; `except` on a command row grants only the operations it names. Also: a feed at the row or byte cap ends as an interrupted read; `TestConsoleContract*` in `ee/report/cli`.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/spawn.test.ts test/unit/ipc.test.ts test/unit/pinning.test.ts test/unit/version.test.ts) && go test ./internal/cli/ -count=1 -run 'Docs' && go test ./ee/report/cli/ -count=1 -run TestConsoleContract`
- Red step: Each row's pin fails while the row is absent; adding `--yes` or a renderer-chosen `--answers-file` path to `setup.apply` must fail a test; a lapsed console must refuse `setup.apply`, a refused console must register no new channel, and a missing handler must still stop the console at startup; the docs pin fails until the contract sentences say seven.
- Depends on: **RS1** (the docs pin holds the allow-list's flags to flags the CLI registers); **WH1** (same); **ST2** (same); **SM-E** (both write `console/docs/json-contract.md`); **RS3** (`ee/report/cli`'s `TestConsoleContractNamesOnlyRealReportFlags` checks every `--flag` the contract and design docs give `report` against report's own command, so `--stream` must be registered there first)
- Commit: `console(spawn): the rows, operations and floors the improvements need (F24, F25, F26)`
- Reviewer tier: opus, independent review mandatory (the spawn and IPC allow-lists and a write operation); tier `large`, size 6

#### I-OPS: retire the stub bridge and hold the allow-list to the contract (integration)

- Acceptance: The stub bridge is deleted and every consumer runs on the real bridge; `handlers.ts` supplies SU1's `settings.editor.open` handler, SW3a's `setup.plan` and `setup.apply` handlers and the read handlers SRC adds, and replaces AL's stub handlers in the `gated` table in `index.ts` with them before `registerHandlers` runs, so every handler still passes the lapse and reduced-mode wrappers (a refused console still registers nothing); `ops-contract.test.ts` fails if a name in `new-ops.ts` is missing from `CONSOLE_OPERATIONS` or its payload fields differ (operations added later by MW1 and MW2 are outside the contract and not refused), if a handler's signature differs from the contract's, or if a stub answer differs from the CLI golden it stands for (`internal/cli/testdata/runs_stream.golden.ndjson`, `whoami.golden.json`, `setup_events.golden.ndjson`, `budget_stream.golden.ndjson` and `ee/report/cli/testdata/report_stream.golden.ndjson`; the console reads them as bytes); `ops.spec.ts` drives one operation of each consumer against the stub CLI.
- Files: `console/src/renderer/stubs/bridge-stub.ts`; `console/src/main/handlers.ts` (new); `console/src/main/index.ts`; `console/test/unit/ops-contract.test.ts` (new); `console/test/e2e/ops.spec.ts` (new)
- Interfaces: Joins C-OPS, AL, SRC, SU1, RB4, RS2, MO-C and SW3a, after MW1 and MW2 have written `index.ts`; RS5 reads through `newOpsFrom`, so the real handlers reach it once I-OPS lands.
- Tests: `ops-contract.test.ts` (new), `ops.spec.ts` (new).
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/ops-contract.test.ts) && test -f console/test/e2e/ops.spec.ts && (cd console && npm run test:e2e -- ops.spec.ts)`
- Red step: Rename one operation in `ipc.ts` in a copy: the contract test fails; take a handler out of the `gated` table: the surface check stops the console at startup and the e2e spec fails.
- Depends on: **C-OPS** (integrates the contract); **AL** (needs the real operations); **SU1** (needs the real consumer); **RB4** (needs the real consumer); **RS2** (needs the real consumer); **MO-C** (needs the real consumer); **SW3a** (needs the real consumer); **SRC** (needs the real consumer); **RS3** (compares the console's copy with the report golden); **RS4** (compares the console's copy with the budget golden); **MW2** (both write `index.ts`; I-OPS swaps AL's stubs after MW1 and MW2 have added their handlers, and its contract test checks only the contract's own names)
- Commit: `console: the stub bridge is gone and the operations match the contract (F24)`
- Reviewer tier: standard; tier `medium`, size 2

#### RB1: Retry, empty states and write invalidation

- Acceptance: A refused or timed-out section draws the CLI's sentence, its own command and a Retry control that re-spawns that section's read; every empty section has a plain sentence; `invalidate(scopes)` clears the named per-tab caches and re-reads them at once; `settings.edit`, `roadmap.move` and `run.launch` call it with the scopes they touch; no cache is cleared by anything else (R1, R2, F17, F21).
- Files: `console/src/renderer/section.ts`; `console/src/renderer/screens/degraded.ts`; `console/src/renderer/cache-invalidate.ts` (new); `console/src/renderer/entry.ts`; `console/test/unit/section.test.ts`
- Interfaces: Produces `invalidate`, the Retry control and the empty-state strings.
- Tests: `section.test.ts` (extended), `entry-wiring.test.ts`, `degraded.test.ts`.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/section.test.ts test/unit/entry-wiring.test.ts test/unit/degraded.test.ts)`
- Red step: A test that a settings write leaves the Project tab's cache entry gone fails before `invalidate` exists.
- Depends on: none
- Commit: `console: Retry, empty states, write invalidation (R1, R2)`
- Reviewer tier: standard; tier `medium`, size 4

#### WH1: user ls --me --json

- Acceptance: `user ls --me --json` prints `{version, role (viewer|operator|admin|none), scope (project|all|repos), repos[]}` for the caller only, resolved through `internal/access` (the same resolution the gates use), never another member's; a role the console does not know is read as `none`; without `--json` it prints one sentence; the roster output is unchanged; it is not refused in an agent session because it reads only the caller's own role; the schema and a golden recorded from the CLI's own output hold the shape (the console's copy is compared with the golden by I-OPS). `website/content/cli-reference.md` is regenerated with `go run ./ee/clidoc` and names the new flag. RS7 in `docs/roles-scopes/implementation-plan.md` records its amendment (D2907, confirmed by O2): a caller may read their own role.
- Files: `internal/cli/user_ls.go`; `internal/cli/user_ls_me_test.go` (new); `internal/access/resolve.go`; `schemas/whoami.schema.json` (new); `internal/cli/testdata/whoami.golden.json` (new); `website/content/cli-reference.md` (generated); `docs/roles-scopes/implementation-plan.md`
- Interfaces: Produces the read, the schema and the fixture.
- Tests: `TestUserLsMeShowsOnlyTheCaller`, `TestUserLsMeMatchesTheGate`, `TestUserLsRosterUnchanged`, `TestWhoamiSchema`, `TestWhoamiGolden` (new).
- Command: `go test ./internal/cli/ -count=1 -run 'TestUserLsMe|TestUserLsRosterUnchanged|TestWhoami' && go test ./internal/cli/ -count=1 -run TestCliReferenceNamesEveryCommandAndFlag && go test ./ee/clidoc/ -count=1`
- Red step: `TestUserLsMeMatchesTheGate` fails if `--me` resolves differently from `accessGateProject`.
- Depends on: none
- Commit: `user: ls --me, the caller's own role (D2907)`
- Reviewer tier: opus, independent review mandatory (an authorization read); tier `medium`, size 3

#### RB4: disable what the caller's role cannot do, with the reason

- Acceptance: At startup the console reads `whoami` (when its floor allows) and `disabledWrite` and every write control take `can(action)`: a control the role cannot use is disabled with a tooltip naming the role it needs and the caller's own; with no answer (old CLI, refusal) every control stays enabled and the CLI's refusal is shown after the click; no role is decided in the renderer beyond mapping the CLI's own read (D2907).
- Files: `console/src/renderer/gating.ts` (new); `console/src/renderer/writes.ts`; `console/src/renderer/entry.ts`; `console/test/unit/gating.test.ts` (new); `console/test/e2e/gating.spec.ts` (new)
- Interfaces: Consumes the `whoami` op (AL) and WH1's shape; produces `can`.
- Tests: `gating.test.ts` (new): each role, `none`, no answer; `gating.spec.ts` (new): a viewer's write controls are disabled with the tooltip.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/gating.test.ts test/unit/entry-wiring.test.ts) && test -f console/test/e2e/gating.spec.ts && (cd console && npm run test:e2e -- gating.spec.ts)`
- Red step: A test that a viewer's control is disabled fails while `can` does not exist.
- Depends on: **WH1** (decodes the document and fixture the CLI task produces); **C-OPS** (built against the operations' names and types and the stub bridge); **RB1** (both write `entry.ts`)
- Commit: `console: role-gated controls with a reason (D2907, F22)`
- Reviewer tier: standard; tier `medium`, size 3

#### PL1: the skills name the new flags

- Acceptance: The working and setup skills name `runs ls --stream`, `report --stream`, `budget show --stream`, `user ls --me` and `setup --answers-file --events` exactly as the CLI spells them, and the plugin's command-and-flag tests pass.
- Files: `plugin/skills/working/SKILL.md`; `plugin/skills/setup/SKILL.md`
- Interfaces: Consumes RS1, WH1, ST2.
- Tests: The plugin tests that check every command and flag a skill names.
- Command: `go test ./plugin/ -count=1 && go test ./ee/plugincheck/ -count=1`
- Red step: Name a flag the CLI lacks in a copy: the plugin test fails.
- Depends on: **RS1** (names a flag that must exist); **WH1** (names a flag that must exist); **ST2** (names a flag that must exist); **RS3** (names a flag that must exist); **RS4** (names a flag that must exist)
- Commit: `plugin: the skills name the new flags (F24)`
- Reviewer tier: opus, independent review mandatory (plugin skills); tier `small`, size 1


### Slice 3: settings (`settings-meta`, `settings-ui`)

Metadata first, then the one modal and the accordion; the inline editor is deleted last.

#### SM0: the settings metadata type and its table

- Acceptance: `Meta{Label, Description, Group, Type, Enum, Min, Max, Format}` (`Format` names one of a closed list of formats the CLI already validates, such as a duration, a repository `owner/name` or an e-mail address, or is empty), `Register(key, Meta)` (a duplicate key is refused) and `Lookup(key)` exist in `internal/settings` with an empty table; `Type` is one of `string|bool|int|float|duration|enum`; the group order is one exported list. The tests that need `settingsGenericKey` and `settingsReparse` live in `internal/cli` (they are unexported there and `internal/settings` cannot import `cli`), so SM0 only tests the table itself.
- Files: `internal/settings/meta.go` (new); `internal/settings/meta_test.go` (new)
- Interfaces: Produces `Meta`, `Register`, `Lookup`, the group order.
- Tests: `TestMetaRegisterRefusesDuplicate`, `TestMetaLookupMissingKey` (new).
- Command: `go test ./internal/settings/ -count=1 -run 'TestMeta'`
- Red step: `TestMetaRegisterRefusesDuplicate` fails before `Register` refuses.
- Depends on: none
- Commit: `settings: the metadata type and table (F7, D2908)`
- Reviewer tier: standard; tier `small`, size 2

#### SM-E: settings --json and settings get emit the metadata, and the schema says so

- Acceptance: Each `settingsJSON` row carries `label`, `description`, `group`, `type`, `enum`, `min`, `max`, `format` when `Lookup` has them and omits them when not; the schema gains the eight members as optional on `$defs.row` (added members within version 1, D848) and the console fixture carries them on three rows; `json-contract.md` §6.19 lists them and says a missing member means 'fall back to the key and a text control'; `settings get KEY` text prints the label and the description above the value; no existing member changes meaning.
- Files: `internal/cli/settings_render.go`; `internal/cli/settings_render_meta_test.go` (new); `schemas/settings.schema.json`; `console/test/fixtures/settings.json`; `console/docs/json-contract.md`
- Interfaces: Consumes `settings.Lookup` (SM0); produces the emitted members, the schema members and the fixture rows the console tasks read.
- Tests: `TestSettingsJSONCarriesMeta` and `TestSettingsGetTextShowsLabel` (new); `TestSettingsSchema`, `TestSettingsConsoleFixture` and `TestSettingsConsoleFixtureOwnerAndWritable` stay green.
- Command: `go test ./internal/cli/ -count=1 -run 'TestSettingsJSONCarriesMeta|TestSettingsGetTextShowsLabel|TestSettingsConsoleFixtureOwnerAndWritable' && go test ./schemas/ -count=1 -run 'TestSettingsSchema|TestSettingsConsoleFixture' && go test ./internal/cli/ -count=1 -run Docs`
- Red step: `TestSettingsJSONCarriesMeta` fails while the render ignores `Lookup`.
- Depends on: **SM0** (the render is built against `Lookup`'s signature)
- Commit: `settings: emit label, description, group, type (D2908)`
- Reviewer tier: standard; tier `medium`, size 2.5

#### SM1: labels, descriptions, groups and types for `agent.`, `escalation.`, `stages.`, `orchestrator.`, `issues.` and `repos.`

- Acceptance: Every key of the family that `settingsGenericKey` allows (`agent.`, `escalation.`, `stages.`, `orchestrator.`, `issues.` and `repos.`) has a label of at most six words, a one-sentence description, a group from the group order and a type; enums and numeric ranges match what `settingsReparse` accepts, proven by a test in `internal/cli` that sets the boundary values through the strict decode.
- Files: `internal/settings/meta_agent.go` (new); `internal/cli/settings_meta_agent_test.go` (new)
- Interfaces: Consumes `Meta` and `Register` (SM0); produces entries in its own files.
- Tests: `TestSettingsMetaAgentComplete` and `TestSettingsMetaAgentBoundariesAreAccepted` (new, in `internal/cli`).
- Command: `go test ./internal/cli/ -count=1 -run 'TestSettingsMetaAgent'`
- Red step: The completeness test fails on the first writable key of the family without an entry.
- Depends on: **SM0** (the entries are built against `Meta` and `Register`)
- Commit: `settings: metadata for agent keys (D2908)`
- Reviewer tier: standard; tier `medium`, size 3

#### SM2: labels, descriptions, groups and types for `git.`, `followup.`, `clone_urls.`, `credentials.`, `watch.` and `governance.`

- Acceptance: Every key of the family that `settingsGenericKey` allows (`git.`, `followup.`, `clone_urls.`, `credentials.`, `watch.` and `governance.`) has a label of at most six words, a one-sentence description, a group from the group order and a type; enums and numeric ranges match what `settingsReparse` accepts, proven by a test in `internal/cli` that sets the boundary values through the strict decode.
- Files: `internal/settings/meta_git.go` (new); `internal/cli/settings_meta_git_test.go` (new)
- Interfaces: Consumes `Meta` and `Register` (SM0); produces entries in its own files.
- Tests: `TestSettingsMetaGitComplete` and `TestSettingsMetaGitBoundariesAreAccepted` (new, in `internal/cli`).
- Command: `go test ./internal/cli/ -count=1 -run 'TestSettingsMetaGit'`
- Red step: The completeness test fails on the first writable key of the family without an entry.
- Depends on: **SM0** (the entries are built against `Meta` and `Register`)
- Commit: `settings: metadata for git keys (D2908)`
- Reviewer tier: standard; tier `medium`, size 3

#### SM3: labels, descriptions, groups and types for `layer.*` (the profiles, the A/B keys, the jobs and the defaults) and `workflows.*`

- Acceptance: Every key of the family that `settingsGenericKey` allows (`layer.*` (the profiles, the A/B keys, the jobs and the defaults) and `workflows.*`) has a label of at most six words, a one-sentence description, a group from the group order and a type; enums and numeric ranges match what `settingsReparse` accepts, proven by a test in `internal/cli` that sets the boundary values through the strict decode.
- Files: `internal/settings/meta_layer.go` (new); `internal/settings/meta_workflows.go` (new); `internal/cli/settings_meta_layer_test.go` (new)
- Interfaces: Consumes `Meta` and `Register` (SM0); produces entries in its own files.
- Tests: `TestSettingsMetaLayerComplete` and `TestSettingsMetaLayerBoundariesAreAccepted` (new, in `internal/cli`).
- Command: `go test ./internal/cli/ -count=1 -run 'TestSettingsMetaLayer'`
- Red step: The completeness test fails on the first writable key of the family without an entry.
- Depends on: **SM0** (the entries are built against `Meta` and `Register`)
- Commit: `settings: metadata for layer keys (D2908)`
- Reviewer tier: standard; tier `large`, size 4

#### SM4: labels, descriptions, groups and types for `budget.*`, `bake_repos`, `base_branch`, `max_parallel`, `update_check`, `commit_name`, `commit_email`, `default_claude_account`, `claude_account_auto`, `subscription_spread`, `triage`, `verifier_accounts` and `planning.decoupling`

- Acceptance: Every key of the family that `settingsGenericKey` allows (`budget.*`, `bake_repos`, `base_branch`, `max_parallel`, `update_check`, `commit_name`, `commit_email`, `default_claude_account`, `claude_account_auto`, `subscription_spread`, `triage`, `verifier_accounts` and `planning.decoupling`) has a label of at most six words, a one-sentence description, a group from the group order and a type; enums and numeric ranges match what `settingsReparse` accepts, proven by a test in `internal/cli` that sets the boundary values through the strict decode.
- Files: `internal/settings/meta_single.go` (new); `internal/cli/settings_meta_single_test.go` (new)
- Interfaces: Consumes `Meta` and `Register` (SM0); produces entries in its own files.
- Tests: `TestSettingsMetaSingleComplete` and `TestSettingsMetaSingleBoundariesAreAccepted` (new, in `internal/cli`).
- Command: `go test ./internal/cli/ -count=1 -run 'TestSettingsMetaSingle'`
- Red step: The completeness test fails on the first writable key of the family without an entry.
- Depends on: **SM0** (the entries are built against `Meta` and `Register`)
- Commit: `settings: metadata for single keys (D2908)`
- Reviewer tier: standard; tier `medium`, size 2

#### SMC: every writable key carries its metadata

- Acceptance: `TestSettingsMetaCoversEveryWritableKey` fails for any key `settingsGenericKey` allows (the 126 named in `settingsGenericKeys` and every key under `settingsGenericPrefixes`) that has no label, description, group or type.
- Files: `internal/cli/settings_meta_coverage_test.go` (new)
- Interfaces: Joins SM-E and the four backfills.
- Tests: `TestSettingsMetaCoversEveryWritableKey` (new).
- Command: `go test ./internal/cli/ -count=1 -run TestSettingsMetaCoversEveryWritableKey`
- Red step: Delete one family's entries in a copy: the test fails naming the first key.
- Depends on: **SM-E** (reads the real emission); **SM1** (needs the entries); **SM2** (needs the entries); **SM3** (needs the entries); **SM4** (needs the entries)
- Commit: `settings: require metadata for every writable key (D2908)`
- Reviewer tier: standard; tier `small`, size 1.5

#### SU1: one main-process modal for entry, validation, diff and confirmation

- Acceptance: The modal shows the label, the description, the raw key (subtle), the current value, the control for the key's type (select for a small enum, toggle for a bool, number input with min and max, text with the metadata's format), OK disabled until the client validation passes, inline errors, and 'Reset to default' with the built-in default (R6); OK runs the dry run and the same window then shows old/new/layer/widening and the exact argv, and a second OK writes; every property of rules 11 and 12 holds (cancel is the default, only the window's own top frame answers, a yes waits `CONFIRM_READY_DELAY_MS`); a key with no metadata falls back to the key and a text control; list, map and entry keys show read-only with the command to copy (R5); every string from the CLI passes `TEXT_CLASSES`. The confirm window's channel changes shape (today it answers only `answer(yes, ticked?)` in `src/preload/confirm.ts`) to carry a typed value or a reset, and that shape is pinned in `confirm.test.ts`; SU1 is one of the tasks allowed to change an IPC shape, with an independent review.
- Files: `console/src/main/confirm.ts`; `console/src/main/settingsedit.ts`; `console/src/main/settingsview.ts`; `console/src/renderer/confirm.ts`; `console/src/preload/confirm.ts`; `console/test/unit/confirm.test.ts`; `console/test/unit/settingsview.test.ts`
- Interfaces: Consumes the metadata members (SM-E) and the `settings.editor.open` payload and handler signature (C-OPS); produces the handler function the integration task registers and the outcome the renderer redraws from.
- Tests: `settingsview.test.ts` (extended), `confirm.test.ts`, `settings-write-floor.test.ts`; a mutation check that a renderer frame cannot answer.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/settingsview.test.ts test/unit/confirm.test.ts test/unit/settings-write-floor.test.ts test/unit/ipc.test.ts)`
- Red step: A test that a number outside `min..max` leaves OK disabled fails before the control exists.
- Depends on: **SM-E** (the controls are built against the emitted members and the fixture); **C-OPS** (built against the operation's payload and the handler signature, and the stub bridge)
- Commit: `console(settings): one modal for entry and confirmation (D2902, F6, F10)`
- Reviewer tier: opus, independent review mandatory (the write window, rules 11 and 12); tier `large`, size 5

#### SU2a: settings in an accordion with search

- Acceptance: Settings are grouped by `group` in the contract's order, every group collapsed by default; a search box filters the document already read on label, description and key and expands the matching groups; rows with no metadata sit in 'Other' showing the raw key; the panel still paints as its own section and honours reduced mode.
- Files: `console/src/renderer/screens/projects.ts`; `console/src/renderer/theme/screens.css`; `console/test/unit/projects.test.ts`; `console/test/e2e/projects.spec.ts`
- Interfaces: Consumes the `settings --json` members (SM-E).
- Tests: `projects.test.ts` (extended: grouping, collapsed default, search expands, fallback group); `projects.spec.ts`.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/projects.test.ts) && (cd console && npm run test:e2e -- projects.spec.ts)`
- Red step: The search test fails while the panel is a flat list.
- Depends on: **SM-E** (the groups and search read the emitted members); **LP2** (both write `theme/screens.css`)
- Commit: `console(settings): accordion groups and search (D2908, R7)`
- Reviewer tier: standard; tier `medium`, size 3

#### SU2b: the pencil opens the modal and the inline editor is deleted

- Acceptance: `openSettingsEdit` and `span.projects-settings-editor` no longer exist; the pencil calls the bridge's `settingsEditorOpen` and the row redraws from the outcome; after a write the Project tab's settings section re-reads at once (R2). The old `settings.edit` operation is left in place (main-confirmed); nothing in the renderer calls it after this task, and removing it is a follow-up. Its tests build their own fake bridge from the `new-ops.ts` types and never import `bridge-stub.ts`, which I-OPS deletes.
- Files: `console/src/renderer/entry.ts`; `console/src/renderer/screens/projects.ts`; `console/test/unit/entry-wiring.test.ts`; `console/test/unit/projects.test.ts`
- Interfaces: Consumes SU1's window and AL's op.
- Tests: `entry-wiring.test.ts` (extended), `projects.test.ts`.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/entry-wiring.test.ts test/unit/projects.test.ts)`
- Red step: A test that the row contains no `input` after the pencil is pressed fails while the inline editor exists.
- Depends on: **SU1** (the inline editor is deleted together with the window that replaces it); **SU2a** (both write `projects.ts`); **I-MG** (both write `projects.ts`); **RS2** (both write `entry.ts`)
- Commit: `console(settings): the pencil opens the modal; inline editor removed (D2902, F6)`
- Reviewer tier: standard; tier `medium`, size 2


### Slice 4: performance (`console-perf`)

A measurement, then an opt-in stream and its decoder.

#### P1: the console read timing script

- Acceptance: The script runs every read the console spawns, cold and warm, on the owner's installation, and prints one line per read (command, wall time, bytes) so the three slowest reads are named; `--self-test` runs it against a built-in stub `fugaro` that sleeps and fails unless the slowest stub is ranked first; the document says what the owner types and what the output means. It reads only.
- Files: `scripts/console-read-timings.sh` (new); `docs/console-improvements/check-711.md` (new)
- Interfaces: Consumes the argv list in `spawn.ts`.
- Tests: `bash -n` and a stub-`fugaro` run in the plan's script test pattern (`docs/contract-first-decoupling/check-480.sh` style).
- Command: `bash -n scripts/console-read-timings.sh && bash scripts/console-read-timings.sh --self-test`
- Red step: `--self-test` fails while the ranking is unsorted.
- Depends on: none
- Commit: `scripts: time every console read for Check 711 (D2906)`
- Reviewer tier: standard; tier `small`, size 1
- Owner-run: Check 711: the owner runs `scripts/console-read-timings.sh`.

#### RS1: runs ls --stream emits one line per run

- Acceptance: `runs ls --stream` writes one NDJSON line per run as the per-run reads behind `runcache.List` complete, in the order the whole-document `runs` array has (`type: row`, exactly one element of today's array, unchanged), then one `end` line carrying `count` and the existing document's `version`; a failure after the first row writes an `error` line with the CLI's sentence and exits non-zero; a missing `end` means an interrupted read; without `--stream` the output is byte for byte what it was; `--stream` is accepted only with `--json`; the schema and a golden recorded from the CLI's own output hold the line shape (the console's copy is compared with the golden by I-OPS). `website/content/cli-reference.md` is regenerated with `go run ./ee/clidoc` and names the new flag.
- Files: `internal/cli/runs.go`; `internal/cli/runs_stream.go` (new); `internal/cli/runs_stream_test.go` (new); `schemas/runs-stream.schema.json` (new); `internal/cli/testdata/runs_stream.golden.ndjson` (new); `website/content/cli-reference.md` (generated)
- Interfaces: Produces the stream, the schema and the fixture the console decodes.
- Tests: `TestRunsLsStreamRows`, `TestRunsLsStreamEndsWithCount`, `TestRunsLsJSONUnchanged`, `TestRunsStreamSchema`, `TestRunsStreamGolden` (new).
- Command: `go test ./internal/cli/ -count=1 -run 'TestRunsLsStream|TestRunsLsJSONUnchanged|TestRunsStreamSchema|TestRunsStreamGolden' && go test ./internal/cli/ -count=1 -run TestCliReferenceNamesEveryCommandAndFlag && go test ./ee/clidoc/ -count=1`
- Red step: `TestRunsLsStreamEndsWithCount` fails while the stream has no `end` line; `TestRunsLsJSONUnchanged` is written first against today's output and stays green.
- Depends on: none
- Commit: `runs: ls --stream, one record per line (D2906)`
- Reviewer tier: standard; tier `medium`, size 3

#### RS3: report --stream

- Acceptance: `report` is a paid command under `ee/` (`ee/report/cli/report.go`; `paidCommands` in `internal/cli/extend.go`), so `.claude/rules/ee.md` applies: every new file carries the two-line SPDX notice (the golden, which has no comment syntax, gets a sibling `.license` file) and nothing outside `ee/` imports it. FIRST the task writes in its pull-request body how `report --json` is assembled and which part of it can be emitted before the whole is known (a row per group, a total last); if nothing can, it stops and reports. Then `report --stream` (with `--json`, accepted with the flags `report` takes today: `--by`, `--since`, `--until`, `--repo`) writes one NDJSON line per record in the `row`/`end`/`error` shape RS1's schema states (its own writer: RS1's writer is unexported in package `cli`, which `ee/report/cli` imports only through exported names) (a `row` is one element of today's document, unchanged; the `end` carries the totals and the document `version`); without `--stream` the output is byte for byte what it was; the schema and a golden recorded from the CLI's own output hold the shape. `website/content/cli-reference.md` is regenerated with `go run ./ee/clidoc` and names the new flag.
- Files: `ee/report/cli/report.go`; `ee/report/cli/report_stream.go` (new); `ee/report/cli/report_stream_test.go` (new); `schemas/report-stream.schema.json` (new); `ee/report/cli/testdata/report_stream.golden.ndjson` (new); `ee/report/cli/testdata/report_stream.golden.ndjson.license` (new); `website/content/cli-reference.md` (generated)
- Interfaces: Produces the stream, the schema and the golden.
- Tests: `TestReportStreamRows`, `TestReportStreamEndCarriesTotals`, `TestReportJSONUnchanged`, `TestReportStreamSchema`, `TestReportStreamGolden` (new).
- Command: `go test ./ee/report/cli/ -count=1 -run 'TestReportStream|TestReportJSONUnchanged' && go test ./internal/eeboundary/ -count=1 && go test ./internal/cli/ -count=1 -run TestCliReferenceNamesEveryCommandAndFlag && go test ./ee/clidoc/ -count=1`
- Red step: `TestReportStreamEndCarriesTotals` fails while the stream has no `end` line; `TestReportJSONUnchanged` is written first and stays green.
- Depends on: **RS1** (writes the `row`/`end`/`error` line shape RS1's schema states)
- Commit: `report: --stream, one record per line (O3, D2906)`
- Reviewer tier: standard; tier `medium`, size 3

#### RS4: budget show --stream

- Acceptance: FIRST the task writes in its pull-request body how `budget show --json` reads its sources (config, spend, repos, agents, kill state) and which can be emitted as they arrive; if its shape is one indivisible document, it stops and reports. Then `budget show --stream` (with `--json`, and with every flag `budget show` takes today: `--repo`, `--all`, `--compute`, each meaning what it means without `--stream`) writes `row` lines per repository and per kill switch as their reads complete, then an `end` line; without `--stream` the output is byte for byte what it was; the schema and a golden hold the shape. `website/content/cli-reference.md` is regenerated with `go run ./ee/clidoc` and names the new flag.
- Files: `internal/cli/budget.go`; `internal/cli/budget_stream.go` (new); `internal/cli/budget_stream_test.go` (new); `schemas/budget-stream.schema.json` (new); `internal/cli/testdata/budget_stream.golden.ndjson` (new); `website/content/cli-reference.md` (generated)
- Interfaces: Produces the stream, the schema and the golden.
- Tests: `TestBudgetShowStreamRows`, `TestBudgetShowStreamEnd`, `TestBudgetShowJSONUnchanged`, `TestBudgetStreamSchema`, `TestBudgetStreamGolden` (new).
- Command: `go test ./internal/cli/ -count=1 -run 'TestBudgetShowStream|TestBudgetShowJSONUnchanged' && go test ./internal/cli/ -count=1 -run TestCliReferenceNamesEveryCommandAndFlag && go test ./ee/clidoc/ -count=1`
- Red step: `TestBudgetShowStreamEnd` fails while the stream has no `end` line; `TestBudgetShowJSONUnchanged` is written first and stays green.
- Depends on: **RS1** (both reuse RS1's stream writer and line shape (`runs_stream.go`))
- Commit: `budget: show --stream, one record per line (O3, D2906)`
- Reviewer tier: standard; tier `medium`, size 3

#### RS2: the console draws runs as they arrive

- Acceptance: A section spawning `runs.stream` appends rows as each line arrives, shows a count that updates, treats a missing `end` as an interrupted read with the Retry control (R1), and falls back to the whole-document `runs.list` when the floor is unset or the CLI is older; the section's sequence token still decides which answer stays.
- Files: `console/src/renderer/stream.ts` (new); `console/src/renderer/entry.ts`; `console/src/renderer/section.ts`; `console/test/unit/stream.test.ts` (new); `console/test/e2e/runs-stream.spec.ts` (new)
- Interfaces: Consumes AL's `runs.stream` and RS1's line shape; produces `readStream`.
- Tests: `stream.test.ts` (new): in-order rows, interrupted read, old-CLI fallback; `runs-stream.spec.ts` (new): rows appear before `end`.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/stream.test.ts test/unit/section.test.ts) && test -f console/test/e2e/runs-stream.spec.ts && (cd console && npm run test:e2e -- runs-stream.spec.ts)`
- Red step: The interrupted-read test fails while a missing `end` is treated as complete.
- Depends on: **RS1** (decodes the fixture and the line shape the CLI task produces); **RB1** (both write `section.ts`); **RB4** (both write `entry.ts`); **C-OPS** (built against the operations' names and types and the stub bridge)
- Commit: `console(runs): rows render as they arrive (D2906, F18)`
- Reviewer tier: standard; tier `medium`, size 3

#### SRC: the main-side handlers and fixtures for the six new reads

- Acceptance: `new-reads.ts`, typed from `new-ops.ts` (C-OPS), answers `runs.stream`, `spend.stream` and `budget.stream` (a line-by-line feed under the per-line, 10,000-row and 16 MiB caps AL sets; at a cap the feed ends as an interrupted read), `whoami`, `image.catalog.ls` and `recipes.ls` from the CLI source, and from the fixture source for the e2e specs; every answer passes `TEXT_CLASSES`; a CLI below the floor, or a floor still `null`, answers the section's refusal and Retry rather than spawning. The handlers are not added to `handlersFor` here (the names do not exist in its type until AL lands); I-OPS swaps AL's stubs in the `gated` table for them.
- Files: `console/src/source/new-reads.ts` (new); `console/src/source/fixture.ts`; `console/test/unit/source-new-reads.test.ts` (new)
- Interfaces: Consumes the operations' payloads and handler signatures (C-OPS); produces the read handlers I-OPS registers.
- Tests: `source-new-reads.test.ts` (new): each read from a stub child, the caps, the floor refusal.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/source-new-reads.test.ts)`
- Red step: The cap test fails while a line longer than `MAX_FEED_LINE_LENGTH` is accepted.
- Depends on: **C-OPS** (built against the payloads and handler signatures)
- Commit: `console(source): handlers and fixtures for runs.stream, whoami, image catalog and recipes reads (F24)`
- Reviewer tier: standard; tier `medium`, size 3

#### PW1: prewarm the other tabs' cached views

- Acceptance: After the first paint of the active tab the console reads the primary section of each other tab into its `resultCache`, one read at a time, never Live's feed and never a write; a tab switch or an installation switch cancels the queue; reduced mode and a refused CLI version prewarm nothing; a prewarmed view is stamped 'as of' like any cached view (D2906, F16).
- Files: `console/src/renderer/prewarm.ts` (new); `console/src/renderer/entry.ts`; `console/test/unit/prewarm.test.ts` (new)
- Interfaces: Consumes `resultCache` and `invalidate` (RB1).
- Tests: `prewarm.test.ts` (new): order, one at a time, cancelled on switch, nothing in reduced mode.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/prewarm.test.ts test/unit/section.test.ts)`
- Red step: The cancel test fails while the queue keeps reading after a tab switch.
- Depends on: **RB1** (uses the cache API it adds); **RS2** (both write `entry.ts`); **SU2b** (both write `entry.ts`); **RS5** (both write `entry.ts`; prewarm warms the stream paths RS5 adds)
- Commit: `console: prewarm the other tabs after the first paint (D2906)`
- Reviewer tier: standard; tier `medium`, size 2

#### RS5: the Spend and Project tabs draw their rows as they arrive

- Acceptance: The Spend report and the Project tab's caps read through `readStream` (RS2) when the floor allows and fall back to the whole documents otherwise; rows and totals appear as lines arrive, the `end` line completes the section, a missing `end` is an interrupted read with Retry (R1), and reduced mode still withholds spend, caps and kill figures (`hidesView`). The caps and report reads are called from `entry.ts` (`bridge.showBudget`, `bridge.runReport`), so RS5 changes those call sites. It reaches the streams through C-OPS's `newOpsFrom` and its tests use a fake bridge typed from `new-ops.ts`; I-OPS wires the real one.
- Files: `console/src/renderer/screens/spend.ts`; `console/src/renderer/screens/projects.ts`; `console/src/renderer/stream.ts`; `console/test/unit/spend-stream.test.ts` (new); `console/test/unit/projects-stream.test.ts` (new); `console/src/renderer/entry.ts`
- Interfaces: Consumes `spend.stream` and `budget.stream` (C-OPS types, I-OPS wiring) and RS2's `readStream`.
- Tests: `spend-stream.test.ts` and `projects-stream.test.ts` (new): in-order rows, interrupted read, old-CLI fallback, reduced mode.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/spend-stream.test.ts test/unit/projects-stream.test.ts test/unit/spend.test.ts)`
- Red step: The reduced-mode test fails while the stream path ignores `hidesView`.
- Depends on: **RS2** (uses `readStream`); **RS3** (decodes the report stream's golden); **RS4** (decodes the budget stream's golden); **SU2b** (both write `projects.ts`)
- Commit: `console: Spend and Project draw rows as they arrive (O3, D2906)`
- Reviewer tier: standard; tier `medium`, size 3


### Slice 5: Manage (`manage-objects`)

Reads and Copy-commands for every object, then the two approved-by-name writes.

#### MG0: the Manage kind descriptor

- Acceptance: `ManageKind {id, group, title, source (an op name), columns, decode(doc) -> rows, actions[]}` with `action` one of `copy-command {argv}` or `open-view {view}` (a write action is added by its own task); the four groups and their order (People & access, Code, Models & keys, Budget); the document says each kind owns one file under `manage/kinds/` and that the shell renders only what a kind decodes. It is a prerequisite both the shell and the kinds compile against, not a contract: the shell never needed a kind's rows, so there is no edge to break. A `copy-command` is built by `command.ts` from fixed words plus tokens that start with `[A-Za-z0-9]` and continue with `[A-Za-z0-9._/@:=+-]*`, with no `..` segment; a token with a shell metacharacter, a space or a newline refuses the whole action (as `distroShellUnsafe` does for the WSL route) instead of being quoted, so text from a repository or the CLI can never put a second command on the clipboard.
- Files: `console/src/renderer/manage/types.ts` (new); `console/src/renderer/manage/command.ts` (new); `console/test/unit/manage-types.test.ts` (new); `docs/console-improvements/manage-kinds.md` (new)
- Interfaces: Produces the descriptor type and the group order.
- Tests: `manage-types.test.ts` (new): a kind with an unknown group is refused; `;`, `$(`, a backtick, a newline, a space, a leading `-`, `=`, `@` or `~` and a `..` segment in a token are refused.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-types.test.ts)`
- Red step: The unknown-group test fails before the type exists.
- Depends on: none
- Commit: `console(manage): the kind descriptor (D2903)`
- Reviewer tier: standard; tier `small`, size 2

#### MO-SH: the Manage section: groups, tables, empty and error states

- Acceptance: The Project tab gets a Manage section of four accordion groups, collapsed by default; each kind is its own section with its own read, busy mark and refusal (rule in `console.md`), a table of its columns, an 'as of' stamp and Retry (R1), and an empty sentence; a `copy-command` action copies the exact argv; rows from the CLI pass `TEXT_CLASSES`.
- Files: `console/src/renderer/manage/shell.ts` (new); `console/src/renderer/theme/manage.css` (new); `console/test/unit/manage-shell.test.ts` (new)
- Interfaces: Consumes `ManageKind` (MG0); produces `mountManage(kinds)`, which the integration task calls; tested against fixture kinds.
- Tests: `manage-shell.test.ts` (new): groups, per-kind busy and refusal, copy action.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-shell.test.ts test/unit/projects.test.ts)`
- Red step: A test that one kind's refusal leaves the others drawn fails while the shell uses one read.
- Depends on: **MG0** (built against the descriptor)
- Commit: `console(manage): the Manage section shell (R3, D2903)`
- Reviewer tier: standard; tier `medium`, size 4

#### MO-P: People & access: members, roles and accounts

- Acceptance: Members (`members.list`), the roles each holds (from the grants roster) and OAuth accounts (`accounts.inventory`) are three kinds with their key columns and status; no write control exists; every add, revoke or remove is a Copy-command naming the exact `fugaro user grant|revoke` or `fugaro accounts add|rm` line; no secret is drawn (D2905). Each remove, revoke or offboard copy carries the consequence the CLI states for it as a fixed sentence checked against the command's own help text (spec §8; `user offboard` also deletes the person's stored secrets, `internal/cli/offboard.go`).
- Files: `console/src/renderer/manage/kinds/people.ts` (new); `console/test/unit/manage-people.test.ts` (new); `console/test/fixtures/members.json`
- Interfaces: Consumes `ManageKind` (MG0); the existing `members.list` and `accounts.inventory` ops.
- Tests: `manage-people.test.ts` (new).
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-people.test.ts)`
- Red step: A test that no write control exists fails if one is added.
- Depends on: **MG0** (built against the descriptor)
- Commit: `console(manage): people and access lists (D2903, D2905)`
- Reviewer tier: standard; tier `medium`, size 3

#### MO-K: Models & keys: models and secrets inventory

- Acceptance: Models (`models.list`) and API keys (`secrets.inventory`, names and status only) are two kinds; keys show a Copy-command for `secrets set` (run it again to rotate) and `secrets rm` and never a value or a form (D2905).
- Files: `console/src/renderer/manage/kinds/models.ts` (new); `console/src/renderer/manage/kinds/keys.ts` (new); `console/test/unit/manage-models.test.ts` (new)
- Interfaces: Consumes `ManageKind` (MG0); the existing `models.list` and `secrets.inventory` ops.
- Tests: `manage-models.test.ts` (new).
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-models.test.ts)`
- Red step: A test that no input element exists in the keys kind fails if one is added.
- Depends on: **MG0** (built against the descriptor)
- Commit: `console(manage): models and keys lists (D2903, D2905)`
- Reviewer tier: standard; tier `medium`, size 3

#### MO-B: Budget: caps, kill state and the Spend link

- Acceptance: One kind from `budget.show` shows caps and kill state per repository with a link that opens the Spend tab; no alerts row (D2909); cap controls stay disabled with their reason; reduced mode withholds it (`hidesView`).
- Files: `console/src/renderer/manage/kinds/budget.ts` (new); `console/test/unit/manage-budget.test.ts` (new)
- Interfaces: Consumes `ManageKind` (MG0) and `budget.show`.
- Tests: `manage-budget.test.ts` (new), including the reduced-mode case.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-budget.test.ts)`
- Red step: The reduced-mode test fails while the kind ignores `hidesView`.
- Depends on: **MG0** (built against the descriptor)
- Commit: `console(manage): budget list (D2903, D2909)`
- Reviewer tier: standard; tier `small`, size 2

#### MO-C: Code: repositories, images and recipes

- Acceptance: Repositories (`repos.ls`, `repos.status`), image catalog entries (`image.catalog.ls`) and recipes (`recipes.ls`) are three kinds; repository add and remove are Copy-commands whose text states the consequences the CLI prints (D2413 holds); an older CLI without the new reads shows the section's refusal and Retry.
- Files: `console/src/renderer/manage/kinds/repos.ts` (new); `console/src/renderer/manage/kinds/images.ts` (new); `console/src/renderer/manage/kinds/recipes.ts` (new); `console/test/unit/manage-code.test.ts` (new)
- Interfaces: Consumes `ManageKind` (MG0) and AL's two new ops.
- Tests: `manage-code.test.ts` (new).
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-code.test.ts)`
- Red step: A test that the remove action only copies text fails if it spawns.
- Depends on: **MG0** (built against the descriptor); **C-OPS** (built against the operations' names and types and the stub bridge)
- Commit: `console(manage): code lists (D2903, D2413)`
- Reviewer tier: standard; tier `medium`, size 3

#### I-MG: mount the Manage section on the real kinds and prove it

- Acceptance: `kinds/index.ts` lists the real kind files in the group order and `projects.ts` mounts `mountManage(KINDS)` in the Project tab; `manage.spec.ts` opens each group against the stub CLI and checks the table, the empty state and the Copy-command.
- Files: `console/src/renderer/manage/kinds/index.ts` (new); `console/src/renderer/screens/projects.ts`; `console/test/e2e/manage.spec.ts` (new)
- Interfaces: Joins MG0, MO-SH and the four kind tasks.
- Tests: `manage.spec.ts` (new).
- Command: `(cd console && npm run typecheck) && test -f console/test/e2e/manage.spec.ts && (cd console && npm run test:e2e -- manage.spec.ts)`
- Red step: Leave one kind out of `index.ts`: the e2e spec fails on the missing group row.
- Depends on: **MG0** (the index lists kinds typed by the descriptor); **MO-SH** (mounts the shell); **MO-P** (lists the kind); **MO-K** (lists the kinds); **MO-B** (lists the kind); **MO-C** (lists the kinds); **SU2a** (both write `projects.ts`)
- Commit: `console(manage): mount the Manage section on the real kinds (D2903)`
- Reviewer tier: standard; tier `medium`, size 2

#### MD1: models add --dry-run

- Acceptance: `models add` gains the aiming pair `--project` and `--config` (`settingsAimFlags`, as the settings writers take it), so a caller names the installation it writes instead of whichever the CLI selects, and `--dry-run` and `--json`. `models add <id> --dry-run --json` (with every flag `models add` takes, each meaning what it means without `--dry-run`) computes the change and writes nothing: no config file, no policy node, no saved apply step; it prints one document (`version: 1`) naming the layer it would write, the `budget.allowed_models` entry it would append, the `model_prices` entry with both rates, and the follow-up step it would run or save; a refusal the real command would print (an unknown id, a half-given price) is the same refusal under `--dry-run`. `--json` is accepted only with `--dry-run` (alone it is refused with the fix), and with `--no-apply` the document lists the follow-up as saved, not run. Without the new flags the behaviour and output are byte for byte what they were. The schema and a golden recorded from the CLI's own output hold the shape. It asks nothing (`--dry-run` is a read). `website/content/cli-reference.md` is regenerated with `go run ./ee/clidoc` and names the new flag.
- Files: `internal/cli/models_add.go`; `internal/cli/models_add_dryrun_test.go` (new); `schemas/models-add-plan.schema.json` (new); `internal/cli/testdata/models_add_plan.golden.json` (new); `website/content/cli-reference.md` (generated)
- Interfaces: Produces the plan document the MW1 window shows.
- Tests: `TestModelsAddDryRunWritesNothing`, `TestModelsAddDryRunDocument`, `TestModelsAddDryRunRefusesAsTheCommandDoes`, `TestModelsAddUnchanged`, `TestModelsAddPlanSchema` (new); `TestModelsAddAimsTheNamedProject`, `TestModelsAddJSONNeedsDryRun` (new).
- Command: `go test ./internal/cli/ -count=1 -run 'TestModelsAddDryRun|TestModelsAddUnchanged|TestModelsAddPlanSchema' && go test ./internal/cli/ -count=1 -run TestCliReferenceNamesEveryCommandAndFlag && go test ./ee/clidoc/ -count=1`
- Red step: `TestModelsAddDryRunWritesNothing` fails while `--dry-run` is not a registered flag; `TestModelsAddUnchanged` is written first and stays green.
- Depends on: none
- Commit: `models: add --dry-run, the change as a document and nothing written (O4)`
- Reviewer tier: standard; tier `medium`, size 2

#### MD2: image add --dry-run

- Acceptance: `image add` gains `--dry-run` and `--json`. `image add <name> --dry-run --json` (with every flag `image add` takes today, including its existing `--project` and `--config`) validates the definition as the real command does and writes nothing: no catalog entry, no build, no saved apply step; it prints one document (`version: 1`) naming the catalog entry it would add (name, tags, description, the definition's source) and the steps it would run or save. A refusal the real command would print is the same refusal under `--dry-run`. `--json` is accepted only with `--dry-run`, and with `--no-apply` the document lists the build, the redeploy and the publish as saved, not run. Without the new flags the behaviour and output are byte for byte what they were. The schema and a golden hold the shape. `website/content/cli-reference.md` is regenerated with `go run ./ee/clidoc` and names the new flag.
- Files: `internal/cli/image_catalog.go`; `internal/cli/image_add_dryrun_test.go` (new); `schemas/image-add-plan.schema.json` (new); `internal/cli/testdata/image_add_plan.golden.json` (new); `website/content/cli-reference.md` (generated)
- Interfaces: Produces the plan document the MW2 window shows.
- Tests: `TestImageAddDryRunWritesNothing`, `TestImageAddDryRunDocument`, `TestImageAddDryRunRefusesAsTheCommandDoes`, `TestImageAddUnchanged`, `TestImageAddPlanSchema` (new); `TestImageAddJSONNeedsDryRun` (new).
- Command: `go test ./internal/cli/ -count=1 -run 'TestImageAddDryRun|TestImageAddUnchanged|TestImageAddPlanSchema' && go test ./internal/cli/ -count=1 -run TestCliReferenceNamesEveryCommandAndFlag && go test ./ee/clidoc/ -count=1`
- Red step: `TestImageAddDryRunWritesNothing` fails while `--dry-run` is not a registered flag on `image add`; `TestImageAddUnchanged` is written first and stays green.
- Depends on: none
- Commit: `image: add --dry-run, the entry as a document and nothing written (O4)`
- Reviewer tier: standard; tier `medium`, size 2

#### MW1: models add: the main-process rows, handlers and binding

- Acceptance: U5 is settled (reconciliation): `models add <id>` takes `--route`, `--price-in`, `--price-out` and `--no-apply` as flags, reads no terminal and has no role check of its own (it writes this machine's project config); the console enables it only for a caller whose role is project admin (RB4's gating), which is advisory: the CLI has no role check for this local write, and authority is checked where the change is published (`fugaro apply`) and a test pins that the argv carries no flag outside that set. `models add` goes through the SU1 modal as a **widening** (the tick: it appends to `budget.allowed_models` and writes a `model_prices` entry, `internal/cli/models_add.go`, so it changes what every run may name and spend on), the window showing the model, the typed price and the exact argv, main setting `--no-apply`, one exact-argv row carrying `refusedAfterLapse`, the `models add` command row and the `models`/`add` argv row of `NEVER_REACHABLE` kept, each granting only the two operations by name through `except` (AL extends `except` to command rows; MW1 extends it to argv rows the same way, a change to the never-table mechanism with its own pin), the cache scopes invalidated (R2), the §5.1 row written by MWR. `models use` (it rewrites a repository's reviewed `agent.models.<role>` in its working tree: the owner's word, O5) and `models rm` stay Copy-commands. The window shows the CLI's own `--dry-run --json` document (MD1): main runs the dry run with the validated arguments, holds the document's digest and the argv in the window instance that showed them, refuses a second plan while one is pending, and on that window's own top-frame yes runs the same argv without `--dry-run` (plus `--no-apply`), never one the renderer re-sends; an IPC apply with no yes spawns nothing. Its rows carry a floor constant in `console/src/shared/version.ts` (`null` until the CLI release that carries MD1), so an older CLI shows the Copy-command instead. The two operations are `models.add.plan` and `models.add`: each is added to `CONSOLE_OPERATIONS`, the closed `ConsoleHandlers` type, the `Omit` of `CliBackedHandlers` in `source.ts`, the `gated` table in `index.ts` and the preload bridge, as AL does for its own (F30). The planned argv is `<command> <validated values> --project <the active installation> --no-apply --dry-run --json` and the applied argv is exactly that argv without `--dry-run --json`; the window therefore shows the follow-up as saved, which is what apply does. Every value is checked in main against a stated grammar before any spawn (a model id or an image name `^[a-z0-9][a-z0-9._/-]{0,127}$`; `--dockerfile` `owner/name:path` with no `..` segment; tags from the CLI's closed list; a description of printable characters, no newline, at most 200), every flag is passed as one `--flag=value` word and the positional after `--`, so no value can parse as a flag.
- Files: `console/src/main/spawn.ts`; `console/src/main/ipc.ts`; `console/src/main/index.ts`; `console/src/source/source.ts`; `console/src/preload/index.ts`; `console/src/shared/version.ts`; `console/src/main/confirm.ts`; `console/test/unit/spawn.test.ts`; `console/test/unit/models-writes.test.ts` (new)
- Interfaces: Consumes the Manage list (I-MG), the modal (SU1), `invalidate` (RB1) and the ops table (AL).
- Tests: `spawn.test.ts` (exact argv per write and a widened-argv mutation), `models-writes.test.ts` (new). Also: plan, plan, yes applies nothing; the applied argv equals the planned argv minus `--dry-run`; the row is unreachable below its floor. Also: the action is disabled for a non-admin caller. Also: a value that fails its grammar spawns nothing; a value beginning with `-` stays a value; the applied argv names the active installation.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/spawn.test.ts test/unit/models-writes.test.ts test/unit/ipc.test.ts test/unit/ops-contract.test.ts)`
- Red step: The exact-argv pin for `models add` fails while the row is absent; a test that `models use` and `models rm` are refused fails if either is allowed. A test that an apply with no planned document spawns nothing fails while apply is reachable directly.
- Depends on: **SU1** (confirms through the same window); **RB1** (calls `invalidate`); **AL** (writes the same allow-list, after it); **RB4** (enables the action only for a project admin); **MD1** (the window shows MD1's document, the floor follows MD1's release, and the aiming flags must exist)
- Commit: `console(manage): models add behind the modal as a widening (D2903)`
- Reviewer tier: opus, independent review mandatory (a console write); tier `large`, size 4
- Owner-run: the owner's own go for this task by name.

#### MW2: image add: the main-process rows, handlers and binding

- Acceptance: As MW1 for `image add`, admitting only `--dockerfile owner/name:path`, `--tags`, `--description` and main's own `--project`, with `--no-apply` set by main so nothing is built (the console's child environment has no agent-session marker, so the CLI's own refusal of `--inline`, `--image`, `--default` and `--branch` would not apply; those flags define executable content and a test pins that each is refused); U5 is settled (reconciliation): `image add` reads no terminal (it checks for one only to detect an agent session) and refuses anyone but a project admin (`accessGateProject`), so the console enables it only for a project admin (RB4's gating) and still shows the CLI's refusal if the role changed; `image rm` stays a Copy-command (O5); `image build` and `image refresh` stay on `NEVER_REACHABLE` and `image promote` has no row and none is added. The window shows the CLI's own `--dry-run --json` document (MD2): main runs the dry run with the validated arguments, holds the document's digest and the argv in the window instance that showed them, refuses a second plan while one is pending, and on that window's own top-frame yes runs the same argv without `--dry-run` (plus `--no-apply`), never one the renderer re-sends; an IPC apply with no yes spawns nothing. Its rows carry a floor constant in `console/src/shared/version.ts` (`null` until the CLI release that carries MD2), so an older CLI shows the Copy-command instead. The two operations are `image.add.plan` and `image.add`: each is added to `CONSOLE_OPERATIONS`, the closed `ConsoleHandlers` type, the `Omit` of `CliBackedHandlers` in `source.ts`, the `gated` table in `index.ts` and the preload bridge, as AL does for its own (F30). The planned argv is `<command> <validated values> --project <the active installation> --no-apply --dry-run --json` and the applied argv is exactly that argv without `--dry-run --json`; the window therefore shows the follow-up as saved, which is what apply does. Every value is checked in main against a stated grammar before any spawn (a model id or an image name `^[a-z0-9][a-z0-9._/-]{0,127}$`; `--dockerfile` `owner/name:path` with no `..` segment; tags from the CLI's closed list; a description of printable characters, no newline, at most 200), every flag is passed as one `--flag=value` word and the positional after `--`, so no value can parse as a flag.
- Files: `console/src/main/spawn.ts`; `console/src/main/ipc.ts`; `console/src/main/index.ts`; `console/src/source/source.ts`; `console/src/preload/index.ts`; `console/src/shared/version.ts`; `console/src/main/confirm.ts`; `console/test/unit/spawn.test.ts`; `console/test/unit/image-writes.test.ts` (new)
- Interfaces: Consumes MW1's pattern and the same files after it.
- Tests: `spawn.test.ts`, `image-catalog-writes.test.ts` (new). Also: plan, plan, yes applies nothing; the applied argv equals the planned argv minus `--dry-run`. Also: the action is disabled for a non-admin caller. Also: a value that fails its grammar spawns nothing; a value beginning with `-` stays a value; the applied argv names the active installation.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/spawn.test.ts test/unit/image-writes.test.ts test/unit/ipc.test.ts test/unit/ops-contract.test.ts)`
- Red step: The exact-argv pin for `image add` fails while the row is absent.
- Depends on: **MW1** (both write `spawn.ts`, `ipc.ts` and the §5.1 table); **MD2** (the window shows MD2's document and the floor follows MD2's release)
- Commit: `console(manage): image add behind the modal (D2903)`
- Reviewer tier: opus, independent review mandatory (a console write); tier `large`, size 4
- Owner-run: the owner's own go for this task by name.

#### MWR: the Manage write actions: the window views, the kinds and the contract text

- Acceptance: The Models and Images kinds gain an Add action, enabled only for a project admin (RB4) and only above the floor, that opens SU1's window with a view of its own showing the dry-run document as main parsed it (every string through `TEXT_CLASSES`); below the floor the action shows the Copy-command. §5.1 of `desktop-console.md` gains the four rows; `json-contract.md` gains a §6 section for each dry-run document and its pinned §9 sentence ('read-only — with three exceptions') becomes five exceptions, updated with its pin in `docs_desktop_console_test.go`.
- Files: `console/src/renderer/confirm.ts`; `console/src/renderer/manage/kinds/models.ts`; `console/src/renderer/manage/kinds/images.ts`; `docs/desktop-console/spec.md`; `console/docs/json-contract.md`; `internal/cli/docs_desktop_console_test.go`; `console/test/unit/manage-writes.test.ts` (new)
- Interfaces: Consumes MW1 and MW2's operations, SU1's window, RB4's gating and the kinds MO-K and MO-C drew.
- Tests: `manage-writes.test.ts` (new): a non-admin sees the action disabled with the reason; below the floor the Copy-command; the view draws only what main sent. `TestDocsDesktopConsole*`.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-writes.test.ts test/unit/confirm.test.ts) && go test ./internal/cli/ -count=1 -run 'Docs'`
- Red step: The non-admin test fails while the action is enabled for every role.
- Depends on: **MW2** (draws both writes' operations); **SW3a** (both write `renderer/confirm.ts`); **RB4** (enables the action by role); **I-MG** (adds an action to the kinds the integration mounted)
- Commit: `console(manage): Add for models and images behind the modal (D2903, O4)`
- Reviewer tier: opus, independent review mandatory (a console write); tier `medium`, size 3


### Slice 6: the setup wizard (`setup-wizard`)

CLI first (answers file, events), then the wizard.

#### ST1: fugaro setup --answers-file

- Acceptance: `--answers-file PATH` (or `-`, read from stdin, as `run --task-file -` does; the console uses only `-`) decodes the strict schema `{version, gcp_project, region, name, rigor, bake_repos}` (exactly the five questions of `setupQuestions`; `rigor` is required, it has no default) and fills the five questions' flags; a flag given with a file wins and is named in the plan output; an unknown key or a missing `rigor` is refused with the fix; no question is asked when the file answers it; project creation, the billing link and the Firestore location stay terminal-only and are never read from the file; the file's contents never appear in a log or the plan. `website/content/cli-reference.md` is regenerated with `go run ./ee/clidoc` and names the new flag.
- Files: `internal/cli/init.go`; `internal/cli/setup_answers.go` (new); `internal/cli/setup_answers_test.go` (new); `schemas/setup-answers.schema.json` (new); `website/content/cli-reference.md` (generated)
- Interfaces: Consumes the contract's answers schema; produces the flag.
- Tests: `TestSetupAnswersFile`, `TestSetupAnswersFileRefusesUnknownKey`, `TestSetupAnswersSchema`, `TestSetupAnswersFileGrantsNoConsent` (new: with an answers file an agent session still applies nothing, and a managed project's review and confirmation are still required), `TestEveryWizardQuestionHasAFlag` (existing). Also `TestSetupAnswersFromStdin`.
- Command: `go test ./internal/cli/ -count=1 -run 'TestSetupAnswers|TestEveryWizardQuestionHasAFlag' && go test ./internal/cli/ -count=1 -run TestCliReferenceNamesEveryCommandAndFlag && go test ./ee/clidoc/ -count=1`
- Red step: `TestSetupAnswersFileRefusesUnknownKey` fails until strict decoding is in.
- Depends on: none
- Commit: `setup: --answers-file (D2904)`
- Reviewer tier: opus, independent review mandatory (setup); tier `medium`, size 3

#### ST2: fugaro setup --events

- Acceptance: `--events` (with `--json`) writes NDJSON lines `{type: stage|step|left_for_you|done|error, ...}` as the stages run (the stages, `left_for_you`, the failed stage and the exit code that `--json` prints today, as separate lines), ending with `done` or `error`; the final exit code is unchanged; progress text still goes to stderr; without `--events` the output is byte for byte what it was; a terminal-only step emits `left_for_you` with its one line to run and never waits; the schema and a golden (a hermetic `setup --plan-only --events` against `internal/gcpfake`) hold the shape, and the console's copy is compared with the golden by I-OPS. Because `--json` confirms no step (`unconfirmedWhy` in `internal/cli/init.go`), every step that needs a confirmation is emitted as `left_for_you`; nothing here adds a way to confirm one. `website/content/cli-reference.md` is regenerated with `go run ./ee/clidoc` and names the new flag.
- Files: `internal/cli/init.go`; `internal/cli/setup_events.go` (new); `internal/cli/report_step.go`; `internal/cli/setup_events_test.go` (new); `schemas/setup-events.schema.json` (new); `internal/cli/testdata/setup_events.golden.ndjson` (new); `website/content/cli-reference.md` (generated)
- Interfaces: Consumes the events schema; produces the stream.
- Tests: `TestSetupEventsStages`, `TestSetupEventsLeftForYou`, `TestSetupEventsSchema`, `TestSetupEventsGolden` (new), `TestInitJSONOnARefusalBeforeAnyStage` (existing).
- Command: `go test ./internal/cli/ -count=1 -run 'TestSetupEvents|TestInitJSONOnARefusalBeforeAnyStage' && go test ./internal/cli/ -count=1 -run TestCliReferenceNamesEveryCommandAndFlag && go test ./ee/clidoc/ -count=1`
- Red step: `TestSetupEventsLeftForYou` fails while the terminal-only step prints only to stderr.
- Depends on: **ST1** (both register flags in `init.go`)
- Commit: `setup: --events, one line per stage (D2904)`
- Reviewer tier: opus, independent review mandatory (setup); tier `medium`, size 3

#### SW3a: the wizard's main process half: validation, the answers file, plan then apply

- Acceptance: Main validates the answers from the renderer against the schema (`gcp_project` and `region` against the grammars the CLI enforces, `rigor` present, no sixth key), serialises them once into bytes it holds in memory (no file), runs `setup.plan` with those bytes on the child's stdin, and shows in the shared confirmation window the answers **as main parsed them** (project, region, name, rigor, bake) beside their SHA-256; on a yes it runs `setup.apply` with the same held bytes on stdin (a hostile process of the same user has no file to swap between the check and the read). **What apply does is stated honestly:** `--json` confirms no step (`unconfirmedWhy`), so `setup.apply` applies only what the CLI applies without a confirmation, and every step that needs one (project creation, the billing link, the Firestore location, a Terraform review in a managed project, secrets) arrives as `left_for_you` with its one line to run in the owner's terminal; the window's yes is the owner's go to run the CLI, never a consent the CLI accepts, and the Terraform plan is recomputed at apply, which the window says. A path or flag from the renderer is refused; every event and sentence passes `TEXT_CLASSES`; the window is SU1's single window with a view of its own (rule 11/12 properties); a CLI older than the floor, or a floor still unset, shows the command to run instead. The task opens by writing in its pull-request body what working directory and repository `setup` takes with no installation (U9); if it needs a repository question, the task stops and reports. The bytes and their digest are held by the window instance that showed them, a `setup.plan` while one is pending is refused, and the `setup.apply` handler spawns only on that window's own top-frame yes. The header of `onboarding.ts`, which records the 2026-10-20 ruling that onboarding 'asks the operator for nothing', is amended to the ruling as D2904 amended it.
- Files: `console/src/main/wizardrun.ts` (new); `console/src/main/setupview.ts` (new); `console/src/main/onboarding.ts`; `console/src/renderer/confirm.ts`; `console/test/unit/wizard-main.test.ts` (new)
- Interfaces: Consumes the `setup.plan` and `setup.apply` payloads and handler signatures (C-OPS), ST2's events and SU1's window; produces handler functions the integration task registers.
- Tests: `wizard-main.test.ts` (new): validation, mode 0700/0600 and removal, the digest (a changed byte between plan and apply is refused), the window shows main's parsed answers, a renderer-supplied path refused, an event sentence sanitised, a renderer frame cannot start apply, an unregistered or old CLI shows the command; apply never passes a confirming word (`--yes`, `--ask`, `--non-interactive`) and its argv equals the allow-list row. Also: an IPC `setup.apply` with no yes spawns nothing; plan, plan, yes applies nothing.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/wizard-main.test.ts test/unit/onboarding.test.ts test/unit/ipc.test.ts)`
- Red step: A test that apply's stdin bytes equal the bytes plan showed fails while apply re-serialises the answers.
- Depends on: **ST2** (decodes the events and fixture the CLI task produces (ST1's answers file arrives through it)); **SU1** (the confirmation window is SU1's single window); **C-OPS** (built against the operations' payloads and the handler signatures)
- Commit: `console(setup): the wizard's main half, answers validated and bound to the plan (D2904)`
- Reviewer tier: opus, independent review mandatory (a console write that provisions cloud resources); tier `large`, size 5
- Owner-run: Check 712: the owner runs the wizard on a scratch GCP project.

#### SW3b: the wizard screens in the onboarding view

- Acceptance: With no installation the onboarding view (`drawOnboarding`, `entry.ts`) shows the wizard in place of the command line; it asks the five questions with `setupQuestions`'s defaults (rigor has none and is required), sends the answers object to main, draws the events as they arrive, shows an `error` line's sentence with the failed stage, and lists every `left_for_you` line: a line whose first word is `fugaro` or `gcloud` gets a Copy built by MG0's `command.ts`, and any other is displayed only. It never opens a terminal prompt and never asks for a secret value. Its tests build their own fake bridge from the `new-ops.ts` types and never import `bridge-stub.ts`, which I-OPS deletes.
- Files: `console/src/renderer/wizard/wizard.ts` (new); `console/src/renderer/entry.ts`; `console/test/unit/wizard.test.ts` (new)
- Interfaces: Consumes the operations (AL) and main's event feed (SW3a).
- Tests: `wizard.test.ts` (new): defaults, rigor required, events in order against a stub bridge, `left_for_you`, error, Add project.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/wizard.test.ts)`
- Red step: A test that rigor has no default and Enter is not an answer fails before the screen exists.
- Depends on: **SW3a** (one change split in two: the screens follow main's event feed and digest binding); **MG0** (`left_for_you` lines use the safe copy builder); **PW1** (both write `entry.ts`)
- Commit: `console(setup): the wizard screens (D2904)`
- Reviewer tier: standard; tier `medium`, size 3

#### SW3c: the Add project operation in the rail and the picker

- Acceptance: With installations present (O7, answered 2026-11-18): the wizard opens in the same view as first run, in place of the current installation, and Cancel returns to it; main runs `setup` in a fresh empty directory it creates (`mkdtemp`, mode 0700) and removes after the run, so `setup` wires no repository and is never passed `--onboard-repo`; repositories are added afterwards from Manage as a Copy-command; the new installation appears in the rail once `setup` ends. 'Add project' is one new `INSTALLATION_OPERATIONS` entry that only opens the wizard (it spawns nothing), exposed by the installations preload and drawn as a button in the rail and the picker; the picker's allow-list tests pin it, and a test proves it spawns no child.
- Files: `console/src/main/installations.ts`; `console/src/preload/installations.ts`; `console/src/renderer/installations.ts`; `console/src/main/picker.ts`; `console/test/unit/installations-view.test.ts`; `console/src/main/wizardrun.ts`
- Interfaces: Consumes SW3b's wizard entry point.
- Tests: `installations-view.test.ts` (extended). Also: an Add project run's child cwd is a fresh empty directory, its argv carries no `--onboard-repo`, and Cancel restores the current installation.
- Command: `(cd console && npm run typecheck) && (cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/installations-view.test.ts test/unit/installations-ipc.test.ts)`
- Red step: The pin for the new operation fails while it is absent; a test that it spawns nothing fails if it does.
- Depends on: **SW3b** (opens the wizard SW3b builds); **SW3a** (passes the empty working directory to the setup run SW3a starts, in `wizardrun.ts`)
- Commit: `console(setup): Add project in the rail and the picker (D2904, R4)`
- Reviewer tier: opus, independent review mandatory (a new IPC operation on the picker); tier `medium`, size 2

## Parallel execution

Tasks in one level run at once; a level starts when every task its tasks need is done. The CLI computed the levels from the edges (9 levels: the table below).

| Level | Tasks |
| --- | --- |
| 0 | LP1, LP2, GF1, SM0, P1, RS1, RB1, WH1, ST1, MG0, C-OPS, MD1, MD2 |
| 1 | GF2, SM-E, RB4, ST2, MO-SH, MO-P, MO-K, MO-B, MO-C, SM1, SM2, SM3, SM4, SRC, RS3, RS4 |
| 2 | SU1, SU2a, RS2, PL1, AL, SMC |
| 3 | I-MG, MW1, SW3a |
| 4 | SU2b, MW2 |
| 5 | I-OPS, RS5, MWR |
| 6 | PW1 |
| 7 | SW3b |
| 8 | SW3c |

**Edges kept, one line each, with the reason that decided it.** Every edge is a real dependency: either the dependent needs the other task's behaviour or data at the moment it runs, or both write one file.

- GF2 needs GF1: the CSS follows the new layout's geometry; one behaviour split in two.
- GF2 needs LP2: both write `flight-deck.css`.
- SM-E needs SM0: the render is built against `Lookup`'s signature.
- SU1 needs SM-E: the controls are built against the emitted members and the fixture.
- SU1 needs C-OPS: built against the operation's payload and the handler signature, and the stub bridge.
- SU2a needs SM-E: the groups and search read the emitted members.
- SU2a needs LP2: both write `theme/screens.css`.
- SU2b needs SU1: the inline editor is deleted together with the window that replaces it.
- SU2b needs SU2a: both write `projects.ts`.
- SU2b needs I-MG: both write `projects.ts`.
- SU2b needs RS2: both write `entry.ts`.
- RS2 needs RS1: decodes the fixture and the line shape the CLI task produces.
- RS2 needs RB1: both write `section.ts`.
- RS2 needs RB4: both write `entry.ts`.
- RS2 needs C-OPS: built against the operations' names and types and the stub bridge.
- RB4 needs WH1: decodes the document and fixture the CLI task produces.
- RB4 needs C-OPS: built against the operations' names and types and the stub bridge.
- RB4 needs RB1: both write `entry.ts`.
- ST2 needs ST1: both register flags in `init.go`.
- MO-SH needs MG0: built against the descriptor.
- MO-P needs MG0: built against the descriptor.
- MO-K needs MG0: built against the descriptor.
- MO-B needs MG0: built against the descriptor.
- MO-C needs MG0: built against the descriptor.
- MO-C needs C-OPS: built against the operations' names and types and the stub bridge.
- I-MG needs MG0: the index lists kinds typed by the descriptor.
- I-MG needs MO-SH: mounts the shell.
- I-MG needs MO-P: lists the kind.
- I-MG needs MO-K: lists the kinds.
- I-MG needs MO-B: lists the kind.
- I-MG needs MO-C: lists the kinds.
- I-MG needs SU2a: both write `projects.ts`.
- MW1 needs SU1: confirms through the same window.
- MW1 needs RB1: calls `invalidate`.
- MW1 needs AL: writes the same allow-list, after it.
- MW1 needs RB4: enables the action only for a project admin.
- MW1 needs MD1: the window shows MD1's document, the floor follows MD1's release, and the aiming flags must exist.
- MW2 needs MW1: both write `spawn.ts`, `ipc.ts` and the §5.1 table.
- MW2 needs MD2: the window shows MD2's document and the floor follows MD2's release.
- PL1 needs RS1: names a flag that must exist.
- PL1 needs WH1: names a flag that must exist.
- PL1 needs ST2: names a flag that must exist.
- PL1 needs RS3: names a flag that must exist.
- PL1 needs RS4: names a flag that must exist.
- AL needs RS1: the docs pin holds the allow-list's flags to flags the CLI registers.
- AL needs WH1: same.
- AL needs ST2: same.
- AL needs SM-E: both write `console/docs/json-contract.md`.
- AL needs RS3: `ee/report/cli`'s `TestConsoleContractNamesOnlyRealReportFlags` checks every `--flag` the contract and design docs give `report` against report's own command, so `--stream` must be registered there first.
- SM1 needs SM0: the entries are built against `Meta` and `Register`.
- SM2 needs SM0: the entries are built against `Meta` and `Register`.
- SM3 needs SM0: the entries are built against `Meta` and `Register`.
- SM4 needs SM0: the entries are built against `Meta` and `Register`.
- SMC needs SM-E: reads the real emission.
- SMC needs SM1: needs the entries.
- SMC needs SM2: needs the entries.
- SMC needs SM3: needs the entries.
- SMC needs SM4: needs the entries.
- SW3a needs ST2: decodes the events and fixture the CLI task produces (ST1's answers file arrives through it).
- SW3a needs SU1: the confirmation window is SU1's single window.
- SW3a needs C-OPS: built against the operations' payloads and the handler signatures.
- SW3b needs SW3a: one change split in two: the screens follow main's event feed and digest binding.
- SW3b needs MG0: `left_for_you` lines use the safe copy builder.
- SW3b needs PW1: both write `entry.ts`.
- PW1 needs RB1: uses the cache API it adds.
- PW1 needs RS2: both write `entry.ts`.
- PW1 needs SU2b: both write `entry.ts`.
- PW1 needs RS5: both write `entry.ts`; prewarm warms the stream paths RS5 adds.
- I-OPS needs C-OPS: integrates the contract.
- I-OPS needs AL: needs the real operations.
- I-OPS needs SU1: needs the real consumer.
- I-OPS needs RB4: needs the real consumer.
- I-OPS needs RS2: needs the real consumer.
- I-OPS needs MO-C: needs the real consumer.
- I-OPS needs SW3a: needs the real consumer.
- I-OPS needs SRC: needs the real consumer.
- I-OPS needs RS3: compares the console's copy with the report golden.
- I-OPS needs RS4: compares the console's copy with the budget golden.
- I-OPS needs MW2: both write `index.ts`; I-OPS swaps AL's stubs after MW1 and MW2 have added their handlers, and its contract test checks only the contract's own names.
- SRC needs C-OPS: built against the payloads and handler signatures.
- RS3 needs RS1: writes the `row`/`end`/`error` line shape RS1's schema states.
- RS4 needs RS1: both reuse RS1's stream writer and line shape (`runs_stream.go`).
- RS5 needs RS2: uses `readStream`.
- RS5 needs RS3: decodes the report stream's golden.
- RS5 needs RS4: decodes the budget stream's golden.
- RS5 needs SU2b: both write `projects.ts`.
- SW3c needs SW3b: opens the wizard SW3b builds.
- SW3c needs SW3a: passes the empty working directory to the setup run SW3a starts, in `wizardrun.ts`.
- MWR needs MW2: draws both writes' operations.
- MWR needs SW3a: both write `renderer/confirm.ts`.
- MWR needs RB4: enables the action by role.
- MWR needs I-MG: adds an action to the kinds the integration mounted.

**Sequential by necessity:** the allow-list task `AL` waits for the three CLI tasks that register the flags it names (`RS1`, `WH1`, `ST2`) and cannot be split, because `assertSpawnSurface`, the IPC set and the docs pins each refuse a half-change (twelve files, which is why it is tier large: the spawn rows, the operations, the handler type and its stubs, the floors and the docs pins each refuse a half-change); `MW1` then `MW2` write the same allow-list and §5.1 table, so they are one after the other; `ST1` then `ST2` register flags in `init.go`; `SU2b` deletes the inline editor only after the modal and the Manage mount that share `projects.ts`.

**Critical path (the graph read):** `critical path (9 tasks): SM0 -> SM-E -> SU2a -> I-MG -> SU2b -> RS5 -> PW1 -> SW3b -> SW3c`.

**Shared-files table.** A file appears here only when more than one task names it; no level holds two writers of any of them (a change to any task's Files re-checks this), with one exemption the owner chose on 2026-11-18: `website/content/cli-reference.md` is generated by `go run ./ee/clidoc`, every task that adds a flag regenerates it, and a run that meets a conflict in it reruns the generator rather than merging by hand.

| File | Tasks (level) |
| --- | --- |
| `console/docs/json-contract.md` | SM-E (1), AL (2), MWR (5) |
| `console/src/main/confirm.ts` | SU1 (2), MW1 (3), MW2 (4) |
| `console/src/main/index.ts` | MW1 (3), MW2 (4), AL (2), I-OPS (5) |
| `console/src/main/ipc.ts` | MW1 (3), MW2 (4), AL (2) |
| `console/src/main/spawn.ts` | MW1 (3), MW2 (4), AL (2) |
| `console/src/main/wizardrun.ts` | SW3a (3), SW3c (8) |
| `console/src/preload/index.ts` | MW1 (3), MW2 (4), AL (2) |
| `console/src/renderer/confirm.ts` | SU1 (2), SW3a (3), MWR (5) |
| `console/src/renderer/entry.ts` | SU2b (4), RS2 (2), RB1 (0), RB4 (1), SW3b (7), PW1 (6), RS5 (5) |
| `console/src/renderer/manage/kinds/images.ts` | MO-C (1), MWR (5) |
| `console/src/renderer/manage/kinds/models.ts` | MO-K (1), MWR (5) |
| `console/src/renderer/screens/projects.ts` | SU2a (2), SU2b (4), I-MG (3), RS5 (5) |
| `console/src/renderer/section.ts` | RS2 (2), RB1 (0) |
| `console/src/renderer/stream.ts` | RS2 (2), RS5 (5) |
| `console/src/renderer/stubs/bridge-stub.ts` | C-OPS (0), I-OPS (5) |
| `console/src/renderer/theme/flight-deck.css` | LP2 (0), GF2 (1) |
| `console/src/renderer/theme/screens.css` | LP2 (0), SU2a (2) |
| `console/src/shared/version.ts` | MW1 (3), MW2 (4), AL (2) |
| `console/src/source/source.ts` | MW1 (3), MW2 (4), AL (2) |
| `console/test/unit/projects.test.ts` | SU2a (2), SU2b (4) |
| `console/test/unit/spawn.test.ts` | MW1 (3), MW2 (4), AL (2) |
| `docs/desktop-console/spec.md` | AL (2), MWR (5) |
| `internal/cli/docs_desktop_console_test.go` | AL (2), MWR (5) |
| `internal/cli/init.go` | ST1 (0), ST2 (1) |
| `website/content/cli-reference.md` | RS1 (0), WH1 (0), ST1 (0), ST2 (1), RS3 (1), RS4 (1), MD1 (0), MD2 (0) |

## The dependencies the plan broke and kept

The contract-first pass is on (`fugaro settings planning.decoupling` reads `true`; rigor `startup`, which decouples aggressively). Two passes were measured with `fugaro plan graph`.

- **Tried first and undone.** Four wire contracts (the settings metadata, the runs stream, the caller-role read, the setup answers and events) broke twelve edges between a console consumer and a CLI producer: `"12 broken, 36 kept, 10 added; before 6 tasks, weight 19; after 6 tasks, weight 19; no gain"`. The critical path was the allow-list to Manage to writes chain and no broken edge was on it, so all twelve were undone and each producing CLI task now owns its schema and its fixture, which the console task reads once it has merged.
- **Kept, because the review found it.** The first review round showed the allow-list task must wait for the three CLI tasks that register the flags it names (`TestDocsDesktopConsoleSpawnWordsAreFlagsTheCLIRegisters` demands it), which put six console tasks behind a longer chain for nothing but operation names. **Six edges were then broken through one contract, `C-OPS`** (the nine operations' names and payload types and a stub bridge), with `I-OPS` re-joining them: SU1, RB4, RS2, MO-C, SW3a and SRC no longer wait for `AL`. The read: `gain: 6 broken, 75 kept, 3 added; before 10 tasks, weight 30; after 9 tasks, weight 23; improves`. Weighted, the longest chain after the break is ST1, ST2, AL, MW1, MW2, MWR (3 + 3 + 6 + 4 + 4 + 3 = 23); before it, the consumers of AL pushed it to 30. The read prints its critical path by task count (`critical path (9 tasks): SM0 -> SM-E -> SU2a -> I-MG -> SU2b -> RS5 -> PW1 -> SW3b -> SW3c`); the gain line is the weighted measure.

**Edges broken, one line each.** SU1, RB4, RS2, MO-C, SW3a and SRC each needed the operations' names and types, not the allow-list's rows (contract `C-OPS`, integration `I-OPS`).

**Edges kept** are listed one per line under Parallel execution, each with the reason; the ones a reader might ask about: `AL` needs RS1, WH1 and ST2 (behaviour: the flags must exist in the tree for the docs pin); `MW1` needs `AL` (it writes the same files); SW3a needs SU1 (it uses the single confirmation window SU1 builds, and a view for it is behaviour, not an interface); SW3b needs SW3a (the screens follow main's event feed and digest binding).

**Contract changes anticipated:** `R-OPS` against `C-OPS` (an operation or payload field a consumer needs that the contract lacks), raised first by SU1; it would reach SU1, RB4, RS2, MO-C, SW3a, SRC and AL, whose launched runs are reached by a follow-up run on their pull request that names `docs/console-improvements/contract-ops.md` by path. The descriptor `MG0` and the metadata type `SM0` are ordinary prerequisites: the shell and the five kinds never needed each other's behaviour, so there was no edge to break.

## The estimate the graph read printed

`ready in 495–1980 min, no history yet for 47 tasks (47 tasks; seeds for 47; from the seeds: no history is cached on this machine yet)`

This is informational and reserves no budget: it is what the tasks' tiers and this machine's history predict, not a figure the plan holds to. It moves as runs finish and as the graph changes. Proposed runs, when the owner gives a go: one run per approved task; the worst case is every task at its tier's ceiling, and no figure here is a reservation.

## Checks the plan takes (owner-run)

| Check | What it measures | Who runs it |
| --- | --- | --- |
| **710** | The re-recorded Live and Graph goldens look right at the real window sizes (`cd console && npm run build && npx playwright test live.spec.ts graph.spec.ts --update-snapshots` on the owner's Mac, then look at the images). Needed after LP2, GF1 and GF2. | the owner |
| **711** | The wall time and size of every console read on the owner's installation, cold and warm, by `scripts/console-read-timings.sh` (P1). Decides which commands besides `runs ls` get `--stream` (O3). **Run it twice:** now, and again once RB1, RS2 and PW1 have merged; the second run is the evidence for the spec's "revisiting a pane renders in well under a second; first loads show rows progressively". | the owner, one command |
| **712** | The wizard on a scratch GCP project: the five answers, the plan, the apply, the events, every `left_for_you` line, and how long `setup` takes (U2); whether `gcloud` is on the console child's `PATH` (U4). The exact command is written into SW3a's pull-request body. | the owner |

## After-deploy steps

- **The CLI release** that carries `runs ls --stream` (RS1), `user ls --me` (WH1), `report --stream` (RS3, an ee command), `budget show --stream` (RS4), `setup --answers-file` and `--events` (ST1, ST2) and the settings metadata (SM-E, SM1 to SM4): its `### For operators` is "run `fugaro update`" and nothing it asks, since no installed state changes.
- **The console release** that follows it sets the floor constants AL left `null` (one each for the runs, report and budget streams, the whoami read and setup) to that CLI version in a one-line pull request, and its notes carry the user-facing changes. Until then the new controls fall back as the global constraints say. This is a release step, not a task of this plan.
- `PL1` ships with that CLI release so the plugin names flags that exist.

## Considered and declined

- **Profiles for roles as objects, alerts and project deletion (D2909):** no CLI object; the first gives only copy-the-command.
- **Secret forms for API keys and tokens (D2905):** design rule 2.
- **Repository add and remove, user grants and revokes, deletes with a typed name (D2903):** D2413 and `NEVER_REACHABLE`; the CLI's own typed-name confirmation is still in the code and its removal (no-prompt T12) is unmerged. O1 and O2 in the reconciliation hold what the owner may revisit.
- **A toggle between graph directions (D2900):** more surface for no asked benefit.
- **`fugaro serve` (spec Phase 2):** deferred by the spec; nothing exists to build on.
- **Self-describing fields for every command (spec optional enhancement, R10):** settings get them; the other families keep hand-built list views for the first pass.
- **Reusing Watch's RTDB stream for list commands (F19):** it does not serve the slow reads.
- **A Watch-clone Live pane and removing the sparklines (D2901).**
- **Wizard modes that open a terminal (D2904 alternative).**
- **`models rm` and `image rm` as console writes:** destructive (`models rm` stops every run that names the model); D2903 admits only non-destructive writes, so they stay Copy-commands until the owner says otherwise (O5).
- **Removing the old `settings.edit` operation after SU2b:** it stays main-confirmed; removal is a follow-up.
- **"Every setting is editable" (spec acceptance), narrowed:** only keys `settingsGenericKey` allows are editable, and the list, map and entry keys show read-only with the command to copy (R5); the family-owned read-only rows are shown, grouped and searched but not editable.
- **A console consent for `setup` (O6):** `--json` confirms no step and there is no mechanism an agent's own command line could not also pass; adding one is a product decision, so the wizard applies only what the CLI applies unconfirmed and hands the rest back.
- **A cap on `SUPPORTED_CLI_MINORS`:** `version.ts` says the set "is not a ceiling", so 0.13 being the newest entry is deliberate.

