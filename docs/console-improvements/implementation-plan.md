# Console improvements: implementation plan

**Status: plan only; nothing is implemented; no task approved.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md) (checked against `origin/main` at `01fe41f30`, 2026-11-18). **No task in it
is approved: each needs the owner's explicit go by name**, and `MW1` and `MW2` (console writes) and `SW3` (a console
write that provisions cloud resources) each need a go of their own. The answers that shaped it, all given on
2026-11-18: **D2900** graph top to bottom; **D2901** fix the sparklines and keep Watch parity; **D2902** one
main-process modal for entry and confirmation; **D2903** lists for every object, writes only where fenced-safe;
**D2904** the wizard collects answers and hands terminal-only steps back; **D2905** no secret form; **D2906** measure,
then an opt-in stream; **D2907** a CLI caller-role read; **D2908** metadata for every writable key; **D2909** alerts
and project deletion declined.

## Conventions every part follows

- Rules files: `.claude/rules/console.md` for every `console/**` task (Node 22, exact-pinned dependencies, the allow-list tests, `TEXT_CLASSES`, the parallel-sections rule, goldens skipped in CI); `CLAUDE.md` for the CLI tasks. Run targeted tests, never a whole package (`internal/cli` takes about 25 minutes).
- **Always:** write the failing test first; run the task's command before the commit; `git status --short` and `git diff --stat` before every commit and `git add <paths>`; cite a `T#` and `D#` in the pull-request body.
- **Ask first:** a new console argv or IPC operation (only AL1, AL2, MW1, MW2 may add one); a CLI flag that is not named in this plan; a new dependency; a change to a wire document other than an added member.
- **Never:** edit `.github/workflows/`; re-record a screenshot golden; add a confirmation, a prompt or a `--yes` to the CLI (the no-prompt policy); let the console read, hold or pass a secret value (design rule 2); bump elkjs; change an existing `--json` document's meaning or shape; delete or skip a failing test.
- **Safety rules:** no secret-shaped literal in a test, fixture or doc; no live cloud action by an agent (the three Checks are the owner's); `fugaro update` is unaffected (no installed state changes: every CLI change is an added flag or member, so the release's `### For operators` is "run `fugaro update`", which asks nothing new).
- **Size limits:** `console/src/renderer/entry.ts` is about 8,950 lines, `console/src/main/spawn.ts` about 5,500 and `ipc.ts` about 4,300. A task adds a new file for new code and a small wiring change to these, never a reformat.

## Global constraints and review focus

Inputs and failure modes the spec implies and no single task owns, each with the task that pins it.

| Input or failure mode | Pinned by |
| --- | --- |
| Text from the CLI (labels, descriptions, row cells, event sentences) reaches the renderer or the modal unsanitised | SU1, MO-SH, SW3 (each runs every CLI string through `TEXT_CLASSES`) |
| An older CLI lacks the new members or flags | SU2a (no metadata: raw key and text control), RS2 (falls back to `runs.list`), RB4 (no answer: controls stay enabled), SW3 (the floor is unset: the wizard shows the command) |
| Reduced mode (a CLI the console does not know) misreads a figure | MO-B (`hidesView`), LP2 (the existing reduced-mode tests stay green) |
| A renderer frame tries to answer a write window or start setup | SU1, SW3, MW1, MW2 (each has a mutation test) |
| The answers file leaks or lingers | ST1 (contents never logged), SW3 (mode 0600, removed after the run) |
| A section's slow read blocks another | MO-SH (one read, busy mark and refusal per kind), RS2 |
| `runs ls` without `--stream` or any existing `--json` changes | RS1 (`TestRunsLsJSONUnchanged` written first), SM-E and WH1 (only added members) |
| A reset leaves a lower layer's value that the dialog calls "the default" | SU1 (R6: the built-in default is named as such, and the lower-layer caveat is shown) |
| Keys that take `--add`, `--remove` or `--add-entry` | SU1 (read-only with the command to copy) |
| Contrast below 4.5:1 anywhere in Live | LP2 (a test computes it from the CSS tokens) |
| A floor constant set before the CLI release carries the flag | AL1 (`null` until the release) |
| The docs tests pin `desktop-console.md`, `console-styling.md` and `json-contract.md` | AL2, GF1, SM-E (each runs `go test ./internal/cli/ -run Docs`) |

## Slices and tasks


### Slice 1: Live and Graph (`live-pane`, `graph-flow`)

Ships alone: two readable screens, and the goldens are re-recorded by the owner.

#### LP1: the Watch-versus-Live field table and its test

- Acceptance: Every field `watch --json` carries (`internal/watch/json.go`, `JSONDoc`) is listed against the Live element that draws it or is marked 'not drawn, with the reason'; the test fails when a `JSONDoc` member is in neither column. The result names any gap, which becomes a task by amendment, never a silent addition.
- Files: `docs/console-improvements/live-watch-fields.md` (new); `console/test/unit/live-watch-fields.test.ts` (new)
- Interfaces: Consumes `console/test/fixtures/watch-parity.json` and `JSONDoc`; produces the table.
- Tests: `live-watch-fields.test.ts` (new): every member of the parity fixture is accounted for.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/live-watch-fields.test.ts`
- Red step: Add a member to a copy of the fixture inside the test: the test must fail naming it.
- Depends on: none
- Commit: `docs(console): the fields Watch shows and where Live draws them (F1, D2901)`
- Reviewer tier: standard; tier `small`, size 1

#### LP2: Live sparklines in place, readable, and a bounded run table

- Acceptance: Each sparkline is one fixed-size element updated in place (no per-sample `li` carrying a percent text); no text sits on a background below 4.5:1 (a test computes the ratio from the CSS tokens); a repository's run rows scroll inside a bounded box instead of lengthening the page; the sample caps and the Watch parity tests still pass.
- Files: `console/src/renderer/screens/live.ts`; `console/src/renderer/theme/screens.css`; `console/src/renderer/theme/flight-deck.css`; `console/test/unit/live.test.ts`; `console/test/unit/degraded.test.ts`
- Interfaces: Consumes the existing `WindowHistory` samples; produces no new export.
- Tests: `live.test.ts` (extended: one element per sparkline, contrast ratio of every text/background pair in the Live CSS, run box has a max height); `live-parity.test.ts` and `degraded.test.ts` unchanged and green.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/live.test.ts test/unit/live-parity.test.ts test/unit/degraded.test.ts`
- Red step: The contrast test fails on `--text-muted` over `--accent` (about 1.24:1) before the fix.
- Depends on: none
- Commit: `console(live): sparklines drawn in place with readable contrast; run rows scroll (F2, F3, D2901)`
- Reviewer tier: standard; tier `medium`, size 2
- Owner-run: Check 710 re-records the Live goldens.

#### GF1: the graph lays out top to bottom and stacks wide levels

- Acceptance: `LAYOUT_OPTIONS` sets `elk.direction` to `DOWN`; a level wider than the pane wraps or stacks its cards so no layout is wider than the pane at the 195 px card floor; `console-styling.md` states the new direction and records the step-6a ruling as reversed on 2026-11-18 (D2900); elkjs stays at 0.12.1.
- Files: `console/src/renderer/graph/layout.ts`; `console/test/unit/graph.test.ts`; `console/test/unit/graph-model.test.ts`; `docs/design/console-styling.md`
- Interfaces: Consumes `graph --json` and the existing worker; produces the same layout shape with new coordinates.
- Tests: `graph.test.ts` and `graph-model.test.ts` (the pinned direction and no-wrapping assertions replaced with the new ones); the Go docs tests that pin `console-styling.md`.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/graph.test.ts test/unit/graph-model.test.ts && go test ./internal/cli/ -count=1 -run Docs`
- Red step: The updated direction assertion fails against the unchanged `layout.ts`.
- Depends on: none
- Commit: `console(graph): top-to-bottom layout, wide levels stack (F4, D2900)`
- Reviewer tier: standard; tier `medium`, size 2

#### GF2: the graph pane scrolls vertically only

- Acceptance: `div.graph-canvas-pane` has no horizontal scroll at the supported window widths and `section.graph svg.graph-canvas` fits the pane width; the e2e spec asserts `scrollWidth <= clientWidth` and keeps 'stacks the streams as rows, and keeps every card inside its own row'.
- Files: `console/src/renderer/theme/flight-deck.css`; `console/test/e2e/graph.spec.ts`
- Interfaces: Consumes GF1's layout.
- Tests: `graph.spec.ts` (extended).
- Command: `cd console && npm run test:e2e -- graph.spec.ts`
- Red step: The new `scrollWidth` assertion fails while `overflow-x: auto` and `min-width: 100%` are in place.
- Depends on: **GF1** (the CSS follows the new layout's geometry; one behaviour split in two); **LP2** (both write `flight-deck.css`)
- Commit: `console(graph): vertical scroll only (F4, D2900)`
- Reviewer tier: standard; tier `small`, size 1
- Owner-run: Check 710 re-records the Graph goldens.


### Slice 2: the allow-list, Retry and role gating (`console-robust`)

Inert until used: argv rows behind unset CLI floors, plus the section Retry and write invalidation, which are useful alone.

#### AL1: the new argv rows and the CLI floors

- Acceptance: `SPAWN_COMMANDS` gains exactly these rows, each with its sanitiser class and none carrying a flag the renderer can choose: `runs.stream` (`runs ls --stream --json --since <w>`), `whoami` (`user ls --me --json`), `image.catalog.ls` (`image catalog ls --json`), `recipes.ls` (`recipes ls --json`), `setup.plan` (`setup --plan-only --answers-file <path> --events --json`) and `setup.apply` (`setup --answers-file <path> --events --json`); `version.ts` gains one floor constant per row, each `null` until the CLI release that carries it (the `LICENCE_KEY_CLI_FLOOR` pattern), and `SUPPORTED_CLI_MINORS` is checked against the existing floors (R9, U3); nothing is reachable from `NEVER_REACHABLE`.
- Files: `console/src/main/spawn.ts`; `console/src/shared/version.ts`; `console/test/unit/spawn.test.ts`; `console/test/unit/version.test.ts`
- Interfaces: Produces the argv table and the floor predicates every console task spawns through.
- Tests: `spawn.test.ts` (an exact-argv pin per row and a mutation per row that a widened argv is refused), `version.test.ts`, `pinning.test.ts`.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/spawn.test.ts test/unit/version.test.ts test/unit/pinning.test.ts`
- Red step: Each row's pin fails while the row is absent; a test adding `--yes` to `setup.apply` must fail.
- Depends on: none
- Commit: `console(spawn): the argv rows and floors the improvements need (F24)`
- Reviewer tier: opus, independent review mandatory (the spawn allow-list); tier `medium`, size 3

#### AL2: the IPC operations, the bridge and the section 5.1 table

- Acceptance: `CONSOLE_OPERATIONS` gains `runs.stream`, `whoami`, `image.catalog.ls`, `recipes.ls`, `setup.plan`, `setup.apply` and `settings.editor.open`, with the `settings.editor.open` payload `{key}` only (the layer and phase stay main-process decided); the preload bridge exposes them; §5.1 of `desktop-console.md` gains one row each and rule 12 describes the single modal (D2902) and the amended 2026-10-20 ruling (D2904); the Go docs tests pass.
- Files: `console/src/main/ipc.ts`; `console/src/preload/index.ts`; `console/test/unit/ipc.test.ts`; `docs/design/desktop-console.md`
- Interfaces: Consumes AL1's rows; produces the operations, the bridge methods and the table consumers cite.
- Tests: `ipc.test.ts` (each operation pinned and a renderer frame of another origin refused), `TestDocsDesktopConsole*` in `internal/cli/docs_desktop_console_test.go`.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/ipc.test.ts && go test ./internal/cli/ -count=1 -run 'Docs'`
- Red step: The §5.1 count in the docs test fails until the rows are added.
- Depends on: **AL1** (an operation names a spawn row, so both write the allow-list in order; one change split in two)
- Commit: `console(ipc): the operations and §5.1 rows for the improvements (F24)`
- Reviewer tier: opus, independent review mandatory (the IPC allow-list); tier `medium`, size 3

#### RB1: Retry, empty states and write invalidation

- Acceptance: A refused or timed-out section draws the CLI's sentence, its own command and a Retry control that re-spawns that section's read; every empty section has a plain sentence; `invalidate(scopes)` clears the named per-tab caches and re-reads them at once; `settings.edit`, `roadmap.move` and `run.launch` call it with the scopes they touch; no cache is cleared by anything else (R1, R2, F17, F21).
- Files: `console/src/renderer/section.ts`; `console/src/renderer/screens/degraded.ts`; `console/src/renderer/cache-invalidate.ts` (new); `console/src/renderer/entry.ts`; `console/test/unit/section.test.ts`
- Interfaces: Produces `invalidate`, the Retry control and the empty-state strings.
- Tests: `section.test.ts` (extended), `entry-wiring.test.ts`, `degraded.test.ts`.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/section.test.ts test/unit/entry-wiring.test.ts test/unit/degraded.test.ts`
- Red step: A test that a settings write leaves the Project tab's cache entry gone fails before `invalidate` exists.
- Depends on: none
- Commit: `console: Retry, empty states, write invalidation (R1, R2)`
- Reviewer tier: standard; tier `medium`, size 4

#### WH1: user ls --me --json

- Acceptance: `user ls --me --json` prints `{version, role (viewer|operator|admin|none), scope (project|all|repos), repos[]}` for the caller only, resolved through `internal/access` (the same resolution the gates use), never another member's; a role the console does not know is read as `none`; without `--json` it prints one sentence; the roster output is unchanged; it is not refused in an agent session because it reads only the caller's own role; the schema and the console fixture hold the shape and the fixture is the CLI's own output.
- Files: `internal/cli/user_ls.go`; `internal/cli/user_ls_me_test.go` (new); `internal/access/resolve.go`; `schemas/whoami.schema.json` (new); `console/test/fixtures/whoami.json` (new)
- Interfaces: Produces the read, the schema and the fixture.
- Tests: `TestUserLsMeShowsOnlyTheCaller`, `TestUserLsMeMatchesTheGate`, `TestUserLsRosterUnchanged`, `TestWhoamiSchema`, `TestWhoamiMatchesConsoleFixture` (new).
- Command: `go test ./internal/cli/ -count=1 -run 'TestUserLsMe|TestUserLsRosterUnchanged|TestWhoami'`
- Red step: `TestUserLsMeMatchesTheGate` fails if `--me` resolves differently from `accessGateProject`.
- Depends on: none
- Commit: `user: ls --me, the caller's own role (D2907)`
- Reviewer tier: opus, independent review mandatory (an authorization read); tier `medium`, size 3

#### RB4: disable what the caller's role cannot do, with the reason

- Acceptance: At startup the console reads `whoami` (when its floor allows) and `disabledWrite` and every write control take `can(action)`: a control the role cannot use is disabled with a tooltip naming the role it needs and the caller's own; with no answer (old CLI, refusal) every control stays enabled and the CLI's refusal is shown after the click; no role is decided in the renderer beyond mapping the CLI's own read (D2907).
- Files: `console/src/renderer/gating.ts` (new); `console/src/renderer/writes.ts`; `console/src/renderer/entry.ts`; `console/test/unit/gating.test.ts` (new); `console/test/e2e/gating.spec.ts` (new)
- Interfaces: Consumes the `whoami` op (AL2) and WH1's shape; produces `can`.
- Tests: `gating.test.ts` (new): each role, `none`, no answer; `gating.spec.ts` (new): a viewer's write controls are disabled with the tooltip.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/gating.test.ts test/unit/entry-wiring.test.ts && cd console && npm run test:e2e -- gating.spec.ts`
- Red step: A test that a viewer's control is disabled fails while `can` does not exist.
- Depends on: **WH1** (decodes the document and fixture the CLI task produces); **AL2** (spawns through the listed op)
- Commit: `console: role-gated controls with a reason (D2907, F22)`
- Reviewer tier: standard; tier `medium`, size 3

#### PL1: the skills name the new flags

- Acceptance: The working and setup skills name `runs ls --stream`, `user ls --me` and `setup --answers-file --events` exactly as the CLI spells them, and the plugin's command-and-flag tests pass.
- Files: `plugin/skills/working/SKILL.md`; `plugin/skills/setup/SKILL.md`
- Interfaces: Consumes RS1, WH1, ST2.
- Tests: The plugin tests that check every command and flag a skill names.
- Command: `go test ./plugin/ -count=1`
- Red step: Name a flag the CLI lacks in a copy: the plugin test fails.
- Depends on: **RS1** (names a flag that must exist); **WH1** (names a flag that must exist); **ST2** (names a flag that must exist)
- Commit: `plugin: the skills name the new flags (F24)`
- Reviewer tier: opus, independent review mandatory (plugin skills); tier `small`, size 1


### Slice 3: settings (`settings-meta`, `settings-ui`)

Metadata first, then the one modal and the accordion; the inline editor is deleted last.

#### SM0: the settings metadata type and its table

- Acceptance: `Meta{Label, Description, Group, Type, Enum, Min, Max}`, `Register(key, Meta)` (a duplicate key is refused) and `Lookup(key)` exist with an empty table; `Type` is one of `string|bool|int|float|duration|enum`; the group order is a single exported list the console and the CLI both read from the document, not from this file.
- Files: `internal/settings/meta.go` (new); `internal/settings/meta_test.go` (new)
- Interfaces: Produces `Meta`, `Register`, `Lookup`, the group order.
- Tests: `TestMetaRegisterRefusesDuplicate`, `TestMetaLookupMissingKey` (new).
- Command: `go test ./internal/settings/ -count=1 -run 'TestMeta'`
- Red step: `TestMetaRegisterRefusesDuplicate` fails before `Register` refuses.
- Depends on: none
- Commit: `settings: the metadata type and table (F7, D2908)`
- Reviewer tier: standard; tier `small`, size 2

#### SM-E: settings --json and settings get emit the metadata, and the schema says so

- Acceptance: Each `settingsJSON` row carries `label`, `description`, `group`, `type`, `enum`, `min`, `max` when `Lookup` has them and omits them when not; the schema gains the seven members as optional on `$defs.row` (added members within version 1, D848) and the console fixture carries them on three rows; `json-contract.md` §6.19 lists them and says a missing member means 'fall back to the key and a text control'; `settings get KEY` text prints the label and the description above the value; no existing member changes meaning.
- Files: `internal/cli/settings_render.go`; `internal/cli/settings_render_meta_test.go` (new); `schemas/settings.schema.json`; `console/test/fixtures/settings.json`; `console/docs/json-contract.md`
- Interfaces: Consumes `settings.Lookup` (SM0); produces the emitted members, the schema members and the fixture rows the console tasks read.
- Tests: `TestSettingsJSONCarriesMeta` and `TestSettingsGetTextShowsLabel` (new); `TestSettingsSchema`, `TestSettingsConsoleFixture` and `TestSettingsConsoleFixtureOwnerAndWritable` stay green.
- Command: `go test ./internal/cli/ -count=1 -run 'TestSettingsJSONCarriesMeta|TestSettingsGetTextShowsLabel|TestSettingsConsoleFixtureOwnerAndWritable' && go test ./schemas/ -count=1 -run 'TestSettingsSchema|TestSettingsConsoleFixture' && go test ./internal/cli/ -count=1 -run Docs`
- Red step: `TestSettingsJSONCarriesMeta` fails while the render ignores `Lookup`.
- Depends on: **SM0** (the render is built against `Lookup`'s signature)
- Commit: `settings: emit label, description, group, type (D2908)`
- Reviewer tier: standard; tier `medium`, size 2.5

#### SM1: labels, descriptions, groups and types for `agent.` and `escalation.`

- Acceptance: Every writable key under `agent.` and `escalation.` (the keys `settingsGenericKey` allows) has a label of at most six words, a one-sentence description, a group from the group order in the contract, and a type; enums and numeric ranges match what `settingsReparse` accepts, proven by a test that sets the boundary values through the strict decode.
- Files: `internal/settings/meta_agent.go` (new); `internal/settings/meta_agent_test.go` (new)
- Interfaces: Consumes `Meta` and `Register` (SM0); produces entries in its own file.
- Tests: `TestSettingsMetaAgentComplete` and `TestSettingsMetaAgentBoundariesAreAccepted` (new).
- Command: `go test ./internal/settings/ -count=1 -run 'TestSettingsMeta'`
- Red step: The completeness test fails on the first writable key in the family without an entry.
- Depends on: **SM0** (the entries are built against `Meta` and `Register`)
- Commit: `settings: metadata for agent keys (D2908)`
- Reviewer tier: standard; tier `medium`, size 3

#### SM2: labels, descriptions, groups and types for `git.`, `followup.`, `clone_urls.` and `credentials.`

- Acceptance: Every writable key under `git.`, `followup.`, `clone_urls.` and `credentials.` (the keys `settingsGenericKey` allows) has a label of at most six words, a one-sentence description, a group from the group order in the contract, and a type; enums and numeric ranges match what `settingsReparse` accepts, proven by a test that sets the boundary values through the strict decode.
- Files: `internal/settings/meta_git.go` (new); `internal/settings/meta_git_test.go` (new)
- Interfaces: Consumes `Meta` and `Register` (SM0); produces entries in its own file.
- Tests: `TestSettingsMetaGitComplete` and `TestSettingsMetaGitBoundariesAreAccepted` (new).
- Command: `go test ./internal/settings/ -count=1 -run 'TestSettingsMeta'`
- Red step: The completeness test fails on the first writable key in the family without an entry.
- Depends on: **SM0** (the entries are built against `Meta` and `Register`)
- Commit: `settings: metadata for git keys (D2908)`
- Reviewer tier: standard; tier `medium`, size 3

#### SM3: labels, descriptions, groups and types for `governance.`, `layer.defaults.`, `layer.governance.`, `watch.`, the `budget.*` generic keys and `bake_repos`

- Acceptance: Every writable key under `governance.`, `layer.defaults.`, `layer.governance.`, `watch.`, the `budget.*` generic keys and `bake_repos` (the keys `settingsGenericKey` allows) has a label of at most six words, a one-sentence description, a group from the group order in the contract, and a type; enums and numeric ranges match what `settingsReparse` accepts, proven by a test that sets the boundary values through the strict decode.
- Files: `internal/settings/meta_governance.go` (new); `internal/settings/meta_governance_test.go` (new)
- Interfaces: Consumes `Meta` and `Register` (SM0); produces entries in its own file.
- Tests: `TestSettingsMetaGovernanceComplete` and `TestSettingsMetaGovernanceBoundariesAreAccepted` (new).
- Command: `go test ./internal/settings/ -count=1 -run 'TestSettingsMeta'`
- Red step: The completeness test fails on the first writable key in the family without an entry.
- Depends on: **SM0** (the entries are built against `Meta` and `Register`)
- Commit: `settings: metadata for governance keys (D2908)`
- Reviewer tier: standard; tier `medium`, size 3

#### SMC: every writable key carries its metadata

- Acceptance: `TestSettingsMetaCoversEveryWritableKey` fails for a key `settingsGenericKey` allows that has no label, description, group or type.
- Files: `internal/settings/meta_coverage_test.go` (new)
- Interfaces: Joins SM-E and the three backfills.
- Tests: `TestSettingsMetaCoversEveryWritableKey` (new).
- Command: `go test ./internal/settings/ -count=1 -run TestSettingsMetaCoversEveryWritableKey`
- Red step: Delete one family's entries in a copy: the test fails naming the first key.
- Depends on: **SM-E** (reads the real emission); **SM1** (needs the entries); **SM2** (needs the entries); **SM3** (needs the entries)
- Commit: `settings: require metadata for every writable key (D2908)`
- Reviewer tier: standard; tier `small`, size 1.5

#### SU1: one main-process modal for entry, validation, diff and confirmation

- Acceptance: The modal shows the label, the description, the raw key (subtle), the current value, the control for the key's type (select for a small enum, toggle for a bool, number input with min and max, text with the metadata's format), OK disabled until the client validation passes, inline errors, and 'Reset to default' with the built-in default (R6); OK runs the dry run and the same window then shows old/new/layer/widening and the exact argv, and a second OK writes; every property of rules 11 and 12 holds (cancel is the default, only the window's own top frame answers, a yes waits `CONFIRM_READY_DELAY_MS`); a key with no metadata falls back to the key and a text control; list, map and entry keys show read-only with the command to copy (R5); every string from the CLI passes `TEXT_CLASSES`.
- Files: `console/src/main/confirm.ts`; `console/src/main/settingsedit.ts`; `console/src/main/settingsview.ts`; `console/src/renderer/confirm.ts`; `console/test/unit/settingsview.test.ts`
- Interfaces: Consumes the metadata members (SM-E) and the `settings.editor.open` op (AL2); produces the outcome the renderer redraws from.
- Tests: `settingsview.test.ts` (extended), `confirm.test.ts`, `settings-write-floor.test.ts`; a mutation check that a renderer frame cannot answer.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/settingsview.test.ts test/unit/confirm.test.ts test/unit/settings-write-floor.test.ts test/unit/ipc.test.ts`
- Red step: A test that a number outside `min..max` leaves OK disabled fails before the control exists.
- Depends on: **SM-E** (the controls are built against the emitted members and the fixture); **AL2** (the window is opened through the listed op)
- Commit: `console(settings): one modal for entry and confirmation (D2902, F6, F10)`
- Reviewer tier: opus, independent review mandatory (the write window, rules 11 and 12); tier `large`, size 5

#### SU2a: settings in an accordion with search

- Acceptance: Settings are grouped by `group` in the contract's order, every group collapsed by default; a search box filters the document already read on label, description and key and expands the matching groups; rows with no metadata sit in 'Other' showing the raw key; the panel still paints as its own section and honours reduced mode.
- Files: `console/src/renderer/screens/projects.ts`; `console/src/renderer/theme/screens.css`; `console/test/unit/projects.test.ts`; `console/test/e2e/projects.spec.ts`
- Interfaces: Consumes the `settings --json` members (SM-E).
- Tests: `projects.test.ts` (extended: grouping, collapsed default, search expands, fallback group); `projects.spec.ts`.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/projects.test.ts && cd console && npm run test:e2e -- projects.spec.ts`
- Red step: The search test fails while the panel is a flat list.
- Depends on: **SM-E** (the groups and search read the emitted members)
- Commit: `console(settings): accordion groups and search (D2908, R7)`
- Reviewer tier: standard; tier `medium`, size 3

#### SU2b: the pencil opens the modal and the inline editor is deleted

- Acceptance: `openSettingsEdit` and `span.projects-settings-editor` no longer exist; the pencil calls the bridge's `settingsEditorOpen` and the row redraws from the outcome; after a write the Project tab's settings section re-reads at once (R2).
- Files: `console/src/renderer/entry.ts`; `console/src/renderer/screens/projects.ts`; `console/test/unit/entry-wiring.test.ts`; `console/test/unit/projects.test.ts`
- Interfaces: Consumes SU1's window and AL2's op.
- Tests: `entry-wiring.test.ts` (extended), `projects.test.ts`.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/entry-wiring.test.ts test/unit/projects.test.ts`
- Red step: A test that the row contains no `input` after the pencil is pressed fails while the inline editor exists.
- Depends on: **SU1** (the inline editor is deleted together with the window that replaces it); **SU2a** (both write `projects.ts`); **I-MG** (both write `projects.ts`)
- Commit: `console(settings): the pencil opens the modal; inline editor removed (D2902, F6)`
- Reviewer tier: standard; tier `medium`, size 2


### Slice 4: performance (`console-perf`)

A measurement, then an opt-in stream and its decoder.

#### P1: the console read timing script

- Acceptance: The script runs every read the console spawns, cold and warm, on the owner's installation, and prints one line per read (command, wall time, bytes) so the three slowest reads are named; the document says what the owner types and what the output means. It reads only.
- Files: `scripts/console-read-timings.sh` (new); `docs/console-improvements/check-711.md` (new)
- Interfaces: Consumes the argv list in `spawn.ts`.
- Tests: `bash -n` and a stub-`fugaro` run in the plan's script test pattern (`docs/contract-first-decoupling/check-480.sh` style).
- Command: `bash -n scripts/console-read-timings.sh`
- Red step: Run against a stub `fugaro` that sleeps: the output must rank the slow read first.
- Depends on: none
- Commit: `scripts: time every console read for Check 711 (D2906)`
- Reviewer tier: standard; tier `small`, size 1
- Owner-run: Check 711: the owner runs `scripts/console-read-timings.sh`.

#### RS1: runs ls --stream emits one line per run

- Acceptance: `runs ls --stream` writes one NDJSON line per run as `runcache.List` yields them (`type: row`, exactly one element of today's `runs` array, unchanged), then one `end` line carrying `count` and the existing document's `version`, and a failure after the first row writes an `error` line with the CLI's sentence and exits non-zero; a missing `end` means an interrupted read; without `--stream` the output is byte for byte what it was; `--stream` is accepted only with `--json`; the schema and the console fixture hold the line shape, and the fixture is the CLI's own output.
- Files: `internal/cli/runs.go`; `internal/cli/runs_stream.go` (new); `internal/cli/runs_stream_test.go` (new); `schemas/runs-stream.schema.json` (new); `console/test/fixtures/runs-stream.ndjson` (new)
- Interfaces: Produces the stream, the schema and the fixture the console decodes.
- Tests: `TestRunsLsStreamRows`, `TestRunsLsStreamEndsWithCount`, `TestRunsLsJSONUnchanged`, `TestRunsStreamSchema`, `TestRunsStreamMatchesConsoleFixture` (new).
- Command: `go test ./internal/cli/ -count=1 -run 'TestRunsLsStream|TestRunsLsJSONUnchanged|TestRunsStreamSchema|TestRunsStreamMatchesConsoleFixture'`
- Red step: `TestRunsLsJSONUnchanged` is written first against today's output and must stay green.
- Depends on: none
- Commit: `runs: ls --stream, one record per line (D2906)`
- Reviewer tier: standard; tier `medium`, size 3

#### RS2: the console draws runs as they arrive

- Acceptance: A section spawning `runs.stream` appends rows as each line arrives, shows a count that updates, treats a missing `end` as an interrupted read with the Retry control (R1), and falls back to the whole-document `runs.list` when the floor is unset or the CLI is older; the section's sequence token still decides which answer stays.
- Files: `console/src/renderer/stream.ts` (new); `console/src/renderer/screens/run.ts` (predicted, U6); `console/src/renderer/section.ts`; `console/test/unit/stream.test.ts` (new); `console/test/e2e/runs-stream.spec.ts` (new)
- Interfaces: Consumes AL2's `runs.stream` and RS1's line shape; produces `readStream`.
- Tests: `stream.test.ts` (new): in-order rows, interrupted read, old-CLI fallback; `runs-stream.spec.ts` (new): rows appear before `end`.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/stream.test.ts test/unit/section.test.ts && cd console && npm run test:e2e -- runs-stream.spec.ts`
- Red step: The interrupted-read test fails while a missing `end` is treated as complete.
- Depends on: **RS1** (decodes the fixture and the line shape the CLI task produces); **AL2** (spawns through the listed op); **RB1** (both write `section.ts`)
- Commit: `console(runs): rows render as they arrive (D2906, F18)`
- Reviewer tier: standard; tier `medium`, size 3


### Slice 5: Manage (`manage-objects`)

Reads and Copy-commands for every object, then the two approved-by-name writes.

#### MG0: the Manage kind descriptor

- Acceptance: `ManageKind {id, group, title, source (an op name), columns, decode(doc) -> rows, actions[]}` with `action` one of `copy-command {argv}` or `open-view {view}` (a write action is added by its own task); the four groups and their order (People & access, Code, Models & keys, Budget); the document says each kind owns one file under `manage/kinds/` and that the shell renders only what a kind decodes. It is a prerequisite both the shell and the kinds compile against, not a contract: the shell never needed a kind's rows, so there is no edge to break.
- Files: `console/src/renderer/manage/types.ts` (new); `console/test/unit/manage-types.test.ts` (new); `docs/console-improvements/manage-kinds.md` (new)
- Interfaces: Produces the descriptor type and the group order.
- Tests: `manage-types.test.ts` (new): a kind with an unknown group is refused.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-types.test.ts`
- Red step: The unknown-group test fails before the type exists.
- Depends on: none
- Commit: `console(manage): the kind descriptor (D2903)`
- Reviewer tier: standard; tier `small`, size 2

#### MO-SH: the Manage section: groups, tables, empty and error states

- Acceptance: The Project tab gets a Manage section of four accordion groups, collapsed by default; each kind is its own section with its own read, busy mark and refusal (rule in `console.md`), a table of its columns, an 'as of' stamp and Retry (R1), and an empty sentence; a `copy-command` action copies the exact argv; rows from the CLI pass `TEXT_CLASSES`.
- Files: `console/src/renderer/manage/shell.ts` (new); `console/src/renderer/theme/manage.css` (new); `console/test/unit/manage-shell.test.ts` (new)
- Interfaces: Consumes `ManageKind` (MG0); produces `mountManage(kinds)`, which the integration task calls; tested against fixture kinds.
- Tests: `manage-shell.test.ts` (new): groups, per-kind busy and refusal, copy action.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-shell.test.ts test/unit/projects.test.ts`
- Red step: A test that one kind's refusal leaves the others drawn fails while the shell uses one read.
- Depends on: **MG0** (built against the descriptor)
- Commit: `console(manage): the Manage section shell (R3, D2903)`
- Reviewer tier: standard; tier `medium`, size 4

#### MO-P: People & access: members, roles and accounts

- Acceptance: Members (`members.list`), the roles each holds (from the grants roster) and OAuth accounts (`accounts.inventory`) are three kinds with their key columns and status; no write control exists; every add, revoke or remove is a Copy-command naming the exact `fugaro user grant|revoke` or `fugaro accounts add|rm` line; no secret is drawn (D2905).
- Files: `console/src/renderer/manage/kinds/people.ts` (new); `console/test/unit/manage-people.test.ts` (new); `console/test/fixtures/members.json`
- Interfaces: Consumes `ManageKind` (MG0); the existing `members.list` and `accounts.inventory` ops.
- Tests: `manage-people.test.ts` (new).
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-people.test.ts`
- Red step: A test that no write control exists fails if one is added.
- Depends on: **MG0** (built against the descriptor)
- Commit: `console(manage): people and access lists (D2903, D2905)`
- Reviewer tier: standard; tier `medium`, size 3

#### MO-K: Models & keys: models and secrets inventory

- Acceptance: Models (`models.list`) and API keys (`secrets.inventory`, names and status only) are two kinds; keys show a Copy-command for set, rotate and remove and never a value or a form (D2905).
- Files: `console/src/renderer/manage/kinds/models.ts` (new); `console/src/renderer/manage/kinds/keys.ts` (new); `console/test/unit/manage-models.test.ts` (new)
- Interfaces: Consumes `ManageKind` (MG0); the existing `models.list` and `secrets.inventory` ops.
- Tests: `manage-models.test.ts` (new).
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-models.test.ts`
- Red step: A test that no input element exists in the keys kind fails if one is added.
- Depends on: **MG0** (built against the descriptor)
- Commit: `console(manage): models and keys lists (D2903, D2905)`
- Reviewer tier: standard; tier `medium`, size 3

#### MO-B: Budget: caps, kill state and the Spend link

- Acceptance: One kind from `budget.show` shows caps and kill state per repository with a link that opens the Spend tab; no alerts row (D2909); cap controls stay disabled with their reason; reduced mode withholds it (`hidesView`).
- Files: `console/src/renderer/manage/kinds/budget.ts` (new); `console/test/unit/manage-budget.test.ts` (new)
- Interfaces: Consumes `ManageKind` (MG0) and `budget.show`.
- Tests: `manage-budget.test.ts` (new), including the reduced-mode case.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-budget.test.ts`
- Red step: The reduced-mode test fails while the kind ignores `hidesView`.
- Depends on: **MG0** (built against the descriptor)
- Commit: `console(manage): budget list (D2903, D2909)`
- Reviewer tier: standard; tier `small`, size 2

#### MO-C: Code: repositories, images and recipes

- Acceptance: Repositories (`repos.ls`, `repos.status`), image catalog entries (`image.catalog.ls`) and recipes (`recipes.ls`) are three kinds; repository add and remove are Copy-commands whose text states the consequences the CLI prints (D2413 holds); an older CLI without the new reads shows the section's refusal and Retry.
- Files: `console/src/renderer/manage/kinds/repos.ts` (new); `console/src/renderer/manage/kinds/images.ts` (new); `console/src/renderer/manage/kinds/recipes.ts` (new); `console/test/unit/manage-code.test.ts` (new)
- Interfaces: Consumes `ManageKind` (MG0) and AL2's two new ops.
- Tests: `manage-code.test.ts` (new).
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/manage-code.test.ts`
- Red step: A test that the remove action only copies text fails if it spawns.
- Depends on: **MG0** (built against the descriptor); **AL2** (spawns through the listed ops)
- Commit: `console(manage): code lists (D2903, D2413)`
- Reviewer tier: standard; tier `medium`, size 3

#### I-MG: mount the Manage section on the real kinds and prove it

- Acceptance: `kinds/index.ts` lists the real kind files in the group order and `projects.ts` mounts `mountManage(KINDS)` in the Project tab; `manage.spec.ts` opens each group against the stub CLI and checks the table, the empty state and the Copy-command.
- Files: `console/src/renderer/manage/kinds/index.ts` (new); `console/src/renderer/screens/projects.ts`; `console/test/e2e/manage.spec.ts` (new)
- Interfaces: Joins MG0, MO-SH and the four kind tasks.
- Tests: `manage.spec.ts` (new).
- Command: `cd console && npm run test:e2e -- manage.spec.ts`
- Red step: Leave one kind out of `index.ts`: the e2e spec fails on the missing group row.
- Depends on: **MG0** (the index lists kinds typed by the descriptor); **MO-SH** (mounts the shell); **MO-P** (lists the kind); **MO-K** (lists the kinds); **MO-B** (lists the kind); **MO-C** (lists the kinds)
- Commit: `console(manage): mount the Manage section on the real kinds (D2903)`
- Reviewer tier: standard; tier `medium`, size 2

#### MW1: models use, add and remove from the Manage list

- Acceptance: FIRST the plan's U5 is settled in the task's own words: the exact terminal-free argv of `models use|add|rm`, the layer it writes and the role it needs, in `docs/console-improvements/models-writes.md`; if it needs a terminal or a secret the task stops and reports. Then the three writes go through the SU1 modal with a dry run first (rules 11 and 12), each one exact-argv row, the cache scopes invalidated (R2), a §5.1 row each.
- Files: `console/src/main/spawn.ts`; `console/src/main/ipc.ts`; `console/src/renderer/manage/kinds/models.ts`; `docs/design/desktop-console.md`; `console/test/unit/spawn.test.ts`
- Interfaces: Consumes the Manage list (I-MG), the modal (SU1), `invalidate` (RB1) and the ops table (AL2).
- Tests: `spawn.test.ts` (exact argv per write and a widened-argv mutation), `models-writes.test.ts` (new).
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/spawn.test.ts test/unit/models-writes.test.ts && go test ./internal/cli/ -count=1 -run 'Docs'`
- Red step: The exact-argv pin for `models rm` fails while the row is absent.
- Depends on: **I-MG** (adds an action to the list kind the integration wired); **SU1** (confirms through the same window); **RB1** (calls `invalidate`); **AL2** (writes the same allow-list, after it)
- Commit: `console(manage): models use, add, remove behind the modal (D2903)`
- Reviewer tier: opus, independent review mandatory (a console write); tier `large`, size 4
- Owner-run: the owner's own go for this task by name.

#### MW2: image catalog add and remove from the Manage list

- Acceptance: As MW1 for `image catalog add|rm` (U5 settled first in `docs/console-improvements/image-catalog-writes.md`); `image build`, `refresh` and `promote` stay `NEVER_REACHABLE` (billable cloud work).
- Files: `console/src/main/spawn.ts`; `console/src/main/ipc.ts`; `console/src/renderer/manage/kinds/images.ts`; `docs/design/desktop-console.md`; `console/test/unit/spawn.test.ts`
- Interfaces: Consumes MW1's pattern and the same files after it.
- Tests: `spawn.test.ts`, `image-catalog-writes.test.ts` (new).
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/spawn.test.ts test/unit/image-catalog-writes.test.ts && go test ./internal/cli/ -count=1 -run 'Docs'`
- Red step: The exact-argv pin for `image catalog rm` fails while the row is absent.
- Depends on: **MW1** (both write `spawn.ts`, `ipc.ts` and the §5.1 table)
- Commit: `console(manage): image catalog add and remove behind the modal (D2903)`
- Reviewer tier: opus, independent review mandatory (a console write); tier `large`, size 4
- Owner-run: the owner's own go for this task by name.


### Slice 6: the setup wizard (`setup-wizard`)

CLI first (answers file, events), then the wizard.

#### ST1: fugaro setup --answers-file

- Acceptance: `--answers-file PATH` decodes the strict schema `{version, gcp_project, region, name, rigor, bake_repos}` (exactly the five questions of `setupQuestions`; `rigor` is required, it has no default) and fills the five questions' flags; a flag given with a file wins and is named in the plan output; an unknown key or a missing `rigor` is refused with the fix; no question is asked when the file answers it; project creation, the billing link and the Firestore location stay terminal-only and are never read from the file; the file's contents never appear in a log or the plan.
- Files: `internal/cli/init.go`; `internal/cli/setup_answers.go` (new); `internal/cli/setup_answers_test.go` (new); `schemas/setup-answers.schema.json` (new)
- Interfaces: Consumes the contract's answers schema; produces the flag.
- Tests: `TestSetupAnswersFile`, `TestSetupAnswersFileRefusesUnknownKey`, `TestSetupAnswersSchema` (new), `TestEveryWizardQuestionHasAFlag` (existing).
- Command: `go test ./internal/cli/ -count=1 -run 'TestSetupAnswers|TestEveryWizardQuestionHasAFlag'`
- Red step: `TestSetupAnswersFileRefusesUnknownKey` fails until strict decoding is in.
- Depends on: none
- Commit: `setup: --answers-file (D2904)`
- Reviewer tier: opus, independent review mandatory (setup); tier `medium`, size 3

#### ST2: fugaro setup --events

- Acceptance: `--events` (with `--json`) writes NDJSON lines `{type: stage|step|left_for_you|done|error, ...}` as the stages run (the stages, `left_for_you`, the failed stage and the exit code that `--json` prints today, as separate lines), ending with `done` or `error`; the final exit code is unchanged; progress text still goes to stderr; without `--events` the output is byte for byte what it was; a terminal-only step emits `left_for_you` with its one line to run and never waits; the schema and the console fixture hold the shape, and the fixture is a hermetic `setup --plan-only --events` against `internal/gcpfake`.
- Files: `internal/cli/init.go`; `internal/cli/setup_events.go` (new); `internal/cli/report_step.go`; `internal/cli/setup_events_test.go` (new); `schemas/setup-events.schema.json` (new); `console/test/fixtures/setup-events.ndjson` (new)
- Interfaces: Consumes the events schema; produces the stream.
- Tests: `TestSetupEventsStages`, `TestSetupEventsLeftForYou`, `TestSetupEventsSchema`, `TestSetupEventsMatchConsoleFixture` (new), `TestInitJSONOnARefusalBeforeAnyStage` (existing).
- Command: `go test ./internal/cli/ -count=1 -run 'TestSetupEvents|TestInitJSONOnARefusalBeforeAnyStage'`
- Red step: `TestSetupEventsLeftForYou` fails while the terminal-only step prints only to stderr.
- Depends on: **ST1** (both register flags in `init.go`)
- Commit: `setup: --events, one line per stage (D2904)`
- Reviewer tier: opus, independent review mandatory (setup); tier `medium`, size 3

#### SW3: the first-run and Add project wizard

- Acceptance: With no installation the console shows the wizard in place of the command line; it asks the five questions with `setupQuestions`'s defaults (rigor has none) and writes the answers to a 0600 temp file removed after the run; it spawns `setup.plan` and shows the plan, then `setup.apply` on a yes in the window; it draws the events as they arrive, shows an `error` line's sentence with the failed stage, lists every `left_for_you` with its one line to copy, and never opens a terminal prompt; the 'Add project' button in the rail and picker opens the same wizard; no secret value is ever read.
- Files: `console/src/renderer/wizard/wizard.ts` (new); `console/src/main/onboarding.ts`; `console/src/main/wizardrun.ts` (new); `console/src/renderer/installations.ts`; `console/test/unit/wizard.test.ts` (new)
- Interfaces: Consumes the `setup.plan` and `setup.apply` ops (AL2) and ST2's events; produces the wizard.
- Tests: `wizard.test.ts` (new): defaults, rigor required, temp file mode and removal, events in order against a stub CLI, `left_for_you`, error; a mutation that a renderer frame cannot start apply.
- Command: `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 test/unit/wizard.test.ts test/unit/onboarding.test.ts test/unit/ipc.test.ts`
- Red step: A test that the answers file is created with mode 0600 and removed fails before `wizardrun.ts` exists.
- Depends on: **ST2** (decodes the events and fixture the CLI task produces (ST1's answers file arrives through it)); **AL2** (spawns through the listed ops)
- Commit: `console(setup): the first-run and Add project wizard (D2904, R4)`
- Reviewer tier: opus, independent review mandatory (a console write that provisions cloud resources); tier `large`, size 5
- Owner-run: Check 712: the owner runs the wizard on a scratch GCP project.

## Parallel execution

Tasks in one level run at once; a level starts when every task its tasks need is done. The CLI computed the levels from the edges.

| Level | Tasks |
| --- | --- |
| 0 | LP1, LP2, GF1, SM0, AL1, P1, RS1, RB1, WH1, ST1, MG0 |
| 1 | GF2, SM-E, SM1, SM2, SM3, AL2, ST2, MO-SH, MO-P, MO-K, MO-B |
| 2 | SU1, SU2a, RS2, RB4, SW3, MO-C, PL1, SMC |
| 3 | I-MG |
| 4 | SU2b, MW1 |
| 5 | MW2 |

**Edges kept, one line each, with the reason that decided it.** Every edge is a real dependency: either the dependent needs the other task's behaviour or data at the moment it runs, or both write one file.

- GF2 needs GF1: the CSS follows the new layout's geometry; one behaviour split in two.
- GF2 needs LP2: both write `flight-deck.css`.
- SM-E needs SM0: the render is built against `Lookup`'s signature.
- SM1 needs SM0: the entries are built against `Meta` and `Register`.
- SM2 needs SM0: the entries are built against `Meta` and `Register`.
- SM3 needs SM0: the entries are built against `Meta` and `Register`.
- SU1 needs SM-E: the controls are built against the emitted members and the fixture.
- SU1 needs AL2: the window is opened through the listed op.
- SU2a needs SM-E: the groups and search read the emitted members.
- SU2b needs SU1: the inline editor is deleted together with the window that replaces it.
- SU2b needs SU2a: both write `projects.ts`.
- SU2b needs I-MG: both write `projects.ts`.
- AL2 needs AL1: an operation names a spawn row, so both write the allow-list in order; one change split in two.
- RS2 needs RS1: decodes the fixture and the line shape the CLI task produces.
- RS2 needs AL2: spawns through the listed op.
- RS2 needs RB1: both write `section.ts`.
- RB4 needs WH1: decodes the document and fixture the CLI task produces.
- RB4 needs AL2: spawns through the listed op.
- ST2 needs ST1: both register flags in `init.go`.
- SW3 needs ST2: decodes the events and fixture the CLI task produces (ST1's answers file arrives through it).
- SW3 needs AL2: spawns through the listed ops.
- MO-SH needs MG0: built against the descriptor.
- MO-P needs MG0: built against the descriptor.
- MO-K needs MG0: built against the descriptor.
- MO-B needs MG0: built against the descriptor.
- MO-C needs MG0: built against the descriptor.
- MO-C needs AL2: spawns through the listed ops.
- I-MG needs MG0: the index lists kinds typed by the descriptor.
- I-MG needs MO-SH: mounts the shell.
- I-MG needs MO-P: lists the kind.
- I-MG needs MO-K: lists the kinds.
- I-MG needs MO-B: lists the kind.
- I-MG needs MO-C: lists the kinds.
- MW1 needs I-MG: adds an action to the list kind the integration wired.
- MW1 needs SU1: confirms through the same window.
- MW1 needs RB1: calls `invalidate`.
- MW1 needs AL2: writes the same allow-list, after it.
- MW2 needs MW1: both write `spawn.ts`, `ipc.ts` and the §5.1 table.
- PL1 needs RS1: names a flag that must exist.
- PL1 needs WH1: names a flag that must exist.
- PL1 needs ST2: names a flag that must exist.
- SMC needs SM-E: reads the real emission.
- SMC needs SM1: needs the entries.
- SMC needs SM2: needs the entries.
- SMC needs SM3: needs the entries.

**Sequential by necessity:** the allow-list is one file pair, so `AL1` then `AL2`, and every console task that spawns a new operation waits for `AL2`; `MW1` then `MW2` write the same allow-list and §5.1 table, so they are one after the other; `ST1` then `ST2` register flags in `init.go`; `SU2b` deletes the inline editor only after the modal and the Manage mount that share `projects.ts`.

**Critical path (the graph read):** `critical path (6 tasks): AL1 -> AL2 -> MO-C -> I-MG -> MW1 -> MW2`.

**Shared-files table.** A file appears here only when more than one task names it; no level holds two writers of any of them (a change to any task's Files re-checks this).

| File | Tasks (level) |
| --- | --- |
| `console/src/main/ipc.ts` | AL2 (1), MW1 (4), MW2 (5) |
| `console/src/main/spawn.ts` | AL1 (0), MW1 (4), MW2 (5) |
| `console/src/renderer/entry.ts` | SU2b (4), RB1 (0), RB4 (2) |
| `console/src/renderer/manage/kinds/images.ts` | MO-C (2), MW2 (5) |
| `console/src/renderer/manage/kinds/models.ts` | MO-K (1), MW1 (4) |
| `console/src/renderer/screens/projects.ts` | SU2a (2), SU2b (4), I-MG (3) |
| `console/src/renderer/section.ts` | RS2 (2), RB1 (0) |
| `console/src/renderer/theme/flight-deck.css` | LP2 (0), GF2 (1) |
| `console/src/renderer/theme/screens.css` | LP2 (0), SU2a (2) |
| `console/test/unit/projects.test.ts` | SU2a (2), SU2b (4) |
| `console/test/unit/spawn.test.ts` | AL1 (0), MW1 (4), MW2 (5) |
| `docs/design/desktop-console.md` | AL2 (1), MW1 (4), MW2 (5) |
| `internal/cli/init.go` | ST1 (0), ST2 (1) |

## The dependencies the plan broke and kept

The contract-first pass is on (`fugaro settings planning.decoupling` reads `true`; rigor `startup`, which decouples aggressively). **The first draft broke twelve edges through four wire contracts** (the settings metadata, the runs stream, the caller-role read and the setup answers and events) with an integration task each. `fugaro plan graph` measured it: **"12 broken, 36 kept, 10 added; before 6 tasks, weight 19; after 6 tasks, weight 19; no gain"**. The critical path is the allow-list to Manage to writes chain, which no broken edge was on, so the break bought nothing and cost ten tasks: **all twelve breaks were undone**, and each producing CLI task now owns its schema and its fixture, which the consuming console task reads once it has merged. The graph therefore holds no `decoupling` entry and no contract node.

What stays decoupled without a contract: the providers of one family that share only a type are separate tasks side by side after one small prerequisite (`SM0` for the metadata type, `MG0` for the Manage kind descriptor); the shell and the five kinds never needed each other's behaviour, so there was no edge to break; and the eleven independent tasks of level 0 start together.

**Contract changes anticipated:** none, as no contract node exists. The descriptor (`MG0`) and the metadata type (`SM0`) are ordinary prerequisites; a task that finds one wrong amends it in its own small pull request first and the plan is updated by amendment.

## The estimate the graph read printed

`ready in 450–1800 min, no history yet for 34 tasks (34 tasks; seeds for 34; from the seeds: no history is cached on this machine yet)`

This is informational and reserves no budget: it is what the tasks' tiers and this machine's history predict, not a figure the plan holds to. It moves as runs finish and as the graph changes. Proposed runs, when the owner gives a go: one run per approved task; the worst case is every task at its tier's ceiling, and no figure here is a reservation.

## Checks the plan takes (owner-run)

| Check | What it measures | Who runs it |
| --- | --- | --- |
| **710** | The re-recorded Live and Graph goldens look right at the real window sizes (`cd console && npx playwright test live.spec.ts graph.spec.ts --update-snapshots` on the owner's Mac, then look at the images). Needed after LP2, GF1 and GF2. | the owner |
| **711** | The wall time and size of every console read on the owner's installation, cold and warm, by `scripts/console-read-timings.sh` (P1). Decides which commands besides `runs ls` get `--stream` (O3). | the owner, one command |
| **712** | The wizard on a scratch GCP project: the five answers, the plan, the apply, the events, every `left_for_you` line, and how long `setup` takes (U2); whether `gcloud` is on the console child's `PATH` (U4). The exact command is written into SW3's pull-request body. | the owner |

## After-deploy steps

- **The CLI release** that carries `runs ls --stream` (RS1), `user ls --me` (WH1), `setup --answers-file` and `--events` (ST1, ST2) and the settings metadata (SM-E, SM1 to SM3): its `### For operators` is "run `fugaro update`" and nothing it asks, since no installed state changes.
- **The console release** that follows it sets the floor constants AL1 left `null` (`RUNS_STREAM_CLI_FLOOR`, the whoami floor, the setup floor) to that CLI version in a one-line pull request, and its notes carry the user-facing changes. Until then the new controls fall back as the global constraints say. This is a release step, not a task of this plan.
- `PL1` ships with that CLI release so the plugin names flags that exist.

## Considered and declined

- **Roles as objects, alerts and project deletion (D2909):** no CLI object exists for any of them.
- **Secret forms for API keys and tokens (D2905):** design rule 2.
- **Repository add and remove, user grants and revokes, deletes with a typed name (D2903):** D2413 and `NEVER_REACHABLE`; the CLI's own typed-name confirmation is still in the code and its removal (no-prompt T12) is unmerged. O1 and O2 in the reconciliation hold what the owner may revisit.
- **A toggle between graph directions (D2900):** more surface for no asked benefit.
- **`fugaro serve` (spec Phase 2):** deferred by the spec; nothing exists to build on.
- **Self-describing fields for every command (spec optional enhancement, R10):** settings get them; the other families keep hand-built list views for the first pass.
- **Reusing Watch's RTDB stream for list commands (F19):** it does not serve the slow reads.
- **A Watch-clone Live pane and removing the sparklines (D2901).**
- **Wizard modes that open a terminal (D2904 alternative).**

