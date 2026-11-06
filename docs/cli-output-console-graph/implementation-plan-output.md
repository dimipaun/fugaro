# Implementation plan: concise CLI output (part A)

**Status: plan only; nothing is implemented; no task is approved.** Each task below needs the owner's explicit go, by name; answering a decision approved none of them. This plan builds spec section 1 ([spec.md](spec.md)) as amended by [reconciliation.md](reconciliation.md); where they disagree the reconciliation wins. It shares no task and no file with [implementation-plan-graph.md](implementation-plan-graph.md) (D1350).

**The owner's answers that shaped it (2026-11-06):** D1350 two streams, two plans; D1351 logs in the state directory; D1352 only mutating and step commands log, never `--json` or console polls, 14 days or 50 files per command name, mode 0600 in a 0700 directory, redacted, a write failure never fails the command; D1353 persistent root `--verbose` with `-v` on subcommands, a leading `fugaro -v` stays the version, `recipes --verbose` unchanged; D1354 a shared reporter package, staged (the converge family, then other step-printing commands, then a mechanical pass, listings excluded); **D1355 (not the recommendation) `termenv` becomes a direct dependency, approved by the owner**, so `CLAUDE.md`'s ask-first rule is satisfied. Tensions T4 and T6 in the reconciliation are open and the plan states the assumption it takes for each (T4(a): the reporter decides colour on or off itself and uses `termenv` only to style; T6: a failed read command leaves no log).

## Conventions every task follows

`CLAUDE.md` and the rules it loads. In particular: gofmt prints nothing, `go vet` is clean; hermetic tests only (`internal/testutil`, `t.TempDir()`, an injected clock and environment, no network, no real home); no secret-shaped literal in a test, doc or fixture; **docs are tested**: a task that changes a printed sentence runs `go test ./internal/cli/ -run Docs` and edits the pin in the same commit, keeping the fact and changing the wording in both places; comments explain why; no TODO comments; search before writing a helper. A task that touches a documented command updates `plugin/skills` in the same PR (`plugin/` tests check named commands and flags). **A fix round adds no functionality.** Run targeted tests, never a whole package (`internal/cli` takes about 25 minutes): use `GOFLAGS=-p=2` on a loaded machine.

## Global constraints and review focus (inputs and failure modes the spec implies; each has the task that pins it)

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | **A prompt, a typed confirmation, a `CONFIRM` line, a `left for you` block and a `stopped at ... fix:` line are never suppressed, hidden by a spinner or reordered in the default view.** A spinner is stopped before any read of stdin | CL5b, CL5a |
| G2 | `--json` stdout is byte-for-byte a document: no ANSI, no spinner, no log line; the human lines go to stderr or nowhere | CL10 |
| G3 | Exit codes are unchanged: 0 done or only planned, 1 a refusal or a step left for you, 2 a cloud error. **Three outcomes survive in the reporter: done, failed, left for you.** A left-for-you step is never a green tick | CL1, CL5a |
| G4 | No ANSI, no spinner and no carriage return when stdout is not a terminal, `NO_COLOR` is set (any non-empty value), `TERM=dumb`, or `--no-color`; `CLICOLOR_FORCE` is ignored (T4(a)) | CL1, CL2 |
| G5 | The log is mode 0600 in a 0700 directory, passes through the same redaction as the terminal, and a failure to open, write or prune it never fails the command or changes its exit code | CL3 |
| G6 | Retention is per command name, 14 days or 50 files, whichever first; a pruning error is swallowed and never removes a file of another command or a file the sink did not create | CL3 |
| G7 | No `--json` read and no console-spawned read writes a log (T6: a failed read prints its error and context only) | CL3, CL10 |
| G8 | A failed step prints its name, the error, the last lines of context **on the screen** (an agent with a read-only home cannot read the log) and the log path when there is one | CL5a, CL5b |
| G9 | `doctor`'s default view stays at or under `doctorDefaultMaxLines` and its classes are untouched | CL5b (keeps `TestDoctorDefaultIsOneScreen` green) |
| G10 | `fugaro -v` alone is the version; `fugaro update -v` is verbose; `recipes --verbose` keeps its meaning | CL4 |
| G11 | A coding agent's session (`agentMarker`) sees the same default output; no step is skipped for it and no cloud write is newly permitted | CL5b |

## Slices

- **A1 Foundation (inert):** CL1 reporter, CL2 one colour rule, CL3 log sink, CL4 flag plumbing, CL10 the `--json` pin. Nothing in the CLI changes its output.
- **A2 The converge family (the owner's `update` paste):** CL5a, CL5b.
- **A3 Other step-printing commands:** CL6, CL7a, CL7b, CL7c.
- **A4 Closing:** CL9 census, CL11 docs, CL12 owner-run checks.

## Tasks

### CL1. The reporter package, and `termenv` as a direct dependency

**Decisions:** D1352, D1354, D1355. **Findings:** F2, F3, F7, F8. **Depends on:** none. **Reviewer:** standard (a new direct dependency the owner approved; promoted from the indirect block, no new module).

- **Files (max 7):** `internal/report/report.go` (new), `internal/report/style.go` (new), `internal/report/spinner.go` (new), `internal/report/report_test.go` (new), `internal/report/style_test.go` (new), `go.mod`, `go.sum`.
- **Interfaces produced:** `report.Options{Out, Err io.Writer; Getenv func(string) string; IsTerminal func(io.Writer) bool; Verbose, JSON, NoColor, ASCII bool; Now func() time.Time; Sink Sink}`; `report.New(Options) *Reporter`; `(*Reporter).Step(name string) *Step`; `(*Step).Done(detail)`, `Failed(err, context []string)`, `Left(text)`, `Skipped(reason)`; `(*Reporter).Summary() Outcome` (count, elapsed from the injected clock, the three outcomes); `(*Reporter).Stream() io.Writer` (child output: to the terminal when `Verbose`, always to the sink); `report.NoColor(flag bool, getenv) bool` (the rule `watch.UseNoColor` has: flag, `NO_COLOR` any non-empty value, `TERM=dumb`); `type Sink interface{ Write([]byte) (int, error); Path() string; Close() error }`. A `Reporter` with `JSON: true` prints nothing on stdout. Styling goes through `termenv` with the profile forced by the reporter's own decision, never by `CLICOLOR_FORCE`.
- **go.mod:** `github.com/muesli/termenv v0.16.0` moves from the indirect block to the direct block (the version already resolved); no other change. The spinner is a carriage-return writer, only when `IsTerminal(Out)` and colour/terminal rules allow it.
- **Tests:** `TestNoColorRule` (table: flag, `NO_COLOR=1`, `NO_COLOR=` empty, `TERM=dumb`, `CLICOLOR_FORCE=1` ignored), `TestNoSpinnerAndNoAnsiOffTerminal`, `TestJSONModePrintsNothingOnStdout`, `TestLeftForYouIsNotATick`, `TestSummaryUsesTheInjectedClock`, `TestFailedStepPrintsContextAndSinkPath`, `TestASCIIFallbackMarkers`, `TestStreamIsVerboseOnlyAndAlwaysSunk`.
- **Command:** `go test ./internal/report/ -count=1 && go build ./... && gofmt -l . && go vet ./internal/report/`
- **Red step:** the package does not exist: every test fails on an undefined identifier.
- **Commit:** `report: a shared step reporter, with termenv as a direct dependency (D1355)`

### CL2. One colour rule: `watch` uses the reporter's

**Depends on:** CL1. **Reviewer:** standard.

- **Files (2):** `internal/watch/tui.go` (`UseNoColor` delegates to `report.NoColor`, so the rule lives in one place), `internal/watch/tui_test.go` (the existing table is kept and still passes).
- **Interfaces:** consumes `report.NoColor`. **Behaviour unchanged** (the flag, `NO_COLOR` any value, `TERM=dumb`); a fix round adds none.
- **Tests:** `TestUseASCIIAndNoColor` (existing) and `TestUseNoColorMatchesReport` (the two agree on every row of CL1's table).
- **Command:** `go test ./internal/watch/ -run 'NoColor' -count=1`
- **Red step:** `TestUseNoColorMatchesReport` fails to compile before CL1's export is consumed; after, delete the delegation and the equality test fails on `CLICOLOR_FORCE`.
- **Commit:** `watch: take the no-colour rule from the reporter`

### CL3. The log sink and its retention (SEC)

**Decisions:** D1351, D1352. **Findings:** F5, F6. **Depends on:** CL1. **Reviewer: `opus + mandatory independent review`** (a new file that receives secrets-adjacent output; modes, pruning that deletes files). Mutation check required.

- **Files (max 5):** `internal/report/logsink.go` (new), `internal/report/logsink_test.go` (new), `internal/report/redact.go` (new: the `Redactor` hook), `internal/report/logsink_windows_test.go` (new, build-tagged: path shape only), `internal/localcfg/platformdirs.go` **read only, not edited** (consumed).
- **Interfaces:** `report.OpenLog(cmdName string, getenv func(string) string, goos string, now func() time.Time, redact func(string) string) (Sink, error)`; directory `<localcfg.UserDir(getenv, goos, DirState)>/fugaro/logs` (`~/.local/state/fugaro/logs`; `%LOCALAPPDATA%\fugaro\logs` on Windows); file `<command>-<UTC timestamp>-<pid>.log` mode 0600 in a 0700 directory created with `MkdirAll` then `Chmod`; `<command>` is validated against `^[a-z][a-z0-9-]{0,40}$` (a command name never carries a path); retention after open: files of **this command name only**, older than 14 days or beyond the newest 50, regular files only, never a symlink, never outside the directory. Every write passes `redact`. `OpenLog` errors are returned and the caller treats them as "no log" (G5): the sink then reports `Path() == ""`.
- **Tests:** `TestLogPathFollowsUserDir` (XDG set, unset, Windows), `TestLogFileModeAndDirMode` (0600 and 0700, and an existing 0755 directory is tightened), `TestRetention14Days`, `TestRetention50FilesPerCommand` (another command's files untouched), `TestRetentionIgnoresSymlinksAndForeignFiles`, `TestRedactsBeforeWrite`, `TestUnwritableStateDirNeverFailsTheCaller` (read-only directory returns an empty-path sink, no panic), `TestCommandNameIsValidated` (`../x`, empty and a long name are refused).
- **Command:** `go test ./internal/report/ -run 'Log|Retention|Redact' -count=1`
- **Red step:** `OpenLog` is undefined. **Mutations that must each turn a test red:** widen the file mode to 0644, prune across command names, follow a symlink, skip redaction.
- **Commit:** `report: a per-command log of every step run, pruned at 14 days or 50 files`

### CL4. Flag plumbing: the persistent `--verbose` / `-v`

**Decisions:** D1353. **Findings:** F4, F29. **Depends on:** CL1. **Reviewer:** standard.

- **Files (max 6):** `internal/cli/root.go`, `internal/cli/report_flags.go` (new: `reporterFor(cmd, name)` builds a `report.Reporter` from the persistent flag, the environment and the terminal helpers `stdinIsTerminal` and `writerIsTerminal`), `internal/cli/report_flags_test.go` (new), `internal/cli/recipes.go` (**only if** the test shows the local `--verbose` does not shadow the persistent one cleanly; otherwise untouched), `website/content/cli-reference.md` (regenerated: the persistent flag appears in every command's table), `internal/cli/docs_website_cliref_test.go` (only if its pin changes).
- **Interfaces produced:** a persistent root flag `--verbose` with shorthand `-v` on every subcommand; `NormalizeVersionArgs` is unchanged so a leading `fugaro -v` is still `--version`; `reporterFor`. `recipes --verbose` keeps its local meaning (the local flag of the same name shadows the persistent one; a test says so).
- **Tests:** `TestLeadingDashVIsStillTheVersion`, `TestVerboseShorthandOnASubcommand` (`fugaro update -v` parses), `TestRecipesVerboseKeepsItsMeaning`, `TestEveryCommandAcceptsVerbose` (walk the cobra tree), `TestDocsCliRef` (the generated reference is current).
- **Command:** `go test ./internal/cli/ -run 'Verbose|NormalizeVersion|Recipes|Docs.*CliRef' -count=1`
- **Red step:** `fugaro update -v` is an unknown shorthand today: `TestVerboseShorthandOnASubcommand` fails.
- **Commit:** `cli: a persistent --verbose (-v) and the reporter's constructor`

### CL5a. The converge loop's lines are the reporter's

**Decisions:** D1352, D1354. **Findings:** F1, F7. **Depends on:** CL1. **Reviewer: `opus + mandatory independent review`** (G1: the loop owns the confirmations and the `left for you` block).

- **Files (max 3):** `internal/initflow/loop.go`, `internal/initflow/loop_test.go`, `internal/initflow/stage.go` **only if** `Options` needs one field (`Report *report.Reporter`).
- **Interfaces:** consumes CL1. `loop.view` and `loop.finish` print through a step per stage: marker, short phrase, tick (done or changed), cross (failed), a distinct marker for left for you, billing pending and waiting for billing (never a green tick), then one summary line ("Done in 4.2s, 6 steps"). The `--plan-only` plan view keeps its one-line-per-stage shape and the `[state] stage` words the tests and docs quote (the plan view is not a run). `stopped at <stage>; fix: <fix>` and `left for you (<stage>, <kind>): <text>` and the commands under it print unchanged in the default view. Verbose streams as today.
- **Tests:** `TestLoopDefaultViewIsOneLinePerStage`, `TestLoopLeftForYouIsNotATick`, `TestLoopFailureShowsContextAndFix`, `TestLoopVerboseStreamsEverything`, `TestLoopPlanOnlyViewUnchanged` (golden of today's lines), `TestLoopNoReporterPrintsAsToday` (a nil reporter is today's output: keeps every untouched caller green).
- **Command:** `go test ./internal/initflow/ -count=1`
- **Red step:** `TestLoopDefaultViewIsOneLinePerStage` fails: today's `view` prints the `[state]` lines plus a `result` header and no summary.
- **Mutations:** drop the `left for you` print in default mode: `TestLoopLeftForYouIsNotATick` fails; print a tick for `needs-you`: same test fails.
- **Commit:** `initflow: print the converge as reporter steps, with a summary and a failure block`

### CL5b. `setup` and `update` use it (SEC)

**Decisions:** D1352, D1354. **Findings:** F1, F2, F6, F7, F8. **Depends on:** CL3, CL4, CL5a. **Reviewer: `opus + mandatory independent review`** (G1, G11: confirmations, the managed gate's text and the agent refusal sit in these files).

- **Files (max 8):** `internal/cli/init.go` (build the reporter, hand it to the loop options, route the stage's progress writer and the Terraform `TF.Out` through `Reporter.Stream()`; the direct prints that are prompts stay prompts), `internal/cli/update.go` (the command name `update` for the log), `internal/cli/init_report_test.go` (new), `internal/cli/init_output_test.go` (adjusted pins), `internal/cli/update_test.go` (adjusted pins), `internal/cli/doctor_render_test.go` (**untouched, must stay green**), the docs tests the change breaks (named at launch from the red run, each edited in the same commit), `plugin/skills/upgrade/reference/states.md` (only if a quoted sentence changes).
- **Interfaces:** consumes CL3's `OpenLog("update"|"setup", ...)`, CL4's `reporterFor`. `--json`, `--print-vars`, `--plan-only` and a coding agent's marker take the paths they take today. `fugaro update` default: one line per stage, the summary line, the log path on failure and on a stage left for you. `--verbose` streams everything.
- **Tests:** `TestUpdateDefaultOutputIsShort` (the owner's pasted shape: a fake stage list prints at most stages plus a summary), `TestUpdateFailurePrintsContextAndLogPath`, `TestUpdateVerboseStreamsTerraform`, `TestUpdateLogHoldsTheFullTerraformOutput`, `TestUpdateJSONUnchanged`, `TestConfirmPromptsAreNeverSuppressed` (G1: a typed-project-name prompt text is on screen and no spinner is active while stdin is read), `TestUpdateUnderAgentMarkerPrintsTheSameSteps` (G11), `TestDoctorDefaultIsOneScreen` (G9, existing), the existing update convergence test that asserts `doctor` is clean (named at launch).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestUpdate|TestSetup|TestInit|TestConfirm|TestDoctorDefault|Docs' -count=1 -timeout 40m`
- **Red step:** `TestUpdateDefaultOutputIsShort` fails: today's output is the whole Terraform stream.
- **Mutations:** pipe the prompt through the sink only: `TestConfirmPromptsAreNeverSuppressed` fails; leave the spinner running during a read: same test fails.
- **Release action (in this task's PR, `### For operators` of the release that ships it):** "run `fugaro update`" and nothing else; this task creates the state directory itself on first run (`OpenLog`), so no operator step exists.
- **Commit:** `cli: setup and update print one line per step, with the full output in a log`

### CL6. `image refresh`, `image build` and the managed image commands

**Depends on:** CL5b (the pattern, and the converge's constructor). **Reviewer:** standard, **`opus` if the diff touches `image_refresh_managed.go`'s gate text** (it carries the managed refusals).

- **Files (max 7):** `internal/cli/image_refresh.go`, `internal/cli/image_refresh_managed.go`, `internal/cli/image_build_catalog.go`, `internal/cli/image.go`, `internal/cli/image_refresh_steps_test.go`, `internal/cli/image_refresh_managed_test.go`, the docs tests the change breaks.
- **Interfaces:** the refresh's `step N (name)` and `steps finished:` text become reporter steps; the stop line `fugaro image refresh stopped at step %d` is kept as an error message (the plugin skill quotes it).
- **Tests:** `TestImageRefreshDefaultIsOneLinePerStep`, `TestImageRefreshStoppedAtStepKeepsItsSentence`, `TestImageRefreshVerboseUnchanged`, plus the existing refresh tests.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestImageRefresh|TestImageBuild|Docs' -count=1 -timeout 40m`
- **Red step:** the default view prints more than one line per step.
- **Commit:** `cli: image refresh and build print one line per step`

### CL7a, CL7b, CL7c. The step-printing commands, in batches (mechanical)

**Depends on:** CL5b. **Reviewer:** standard; **`opus + independent review` for any batch that touches** `accounts_add*.go`, `accounts_managed.go`, `allow_delete.go`, `config_publish.go`, `managed*.go` or `restore*.go` (confirmations and guards). Each batch is a **separate task with at most 8 files** and a file list fixed at launch by `git grep -lE 'Fprint(f|ln)?\(' internal/cli ee` minus the exempt listings.

| Batch | Family | Typical files (final list at launch) |
| --- | --- | --- |
| CL7a | accounts, secrets, creds (`accounts_add*.go`, `accounts_managed.go`, `secrets*.go`, `creds.go`) | confirmations stay prompts (G1) |
| CL7b | project and config writers (`config_publish.go`, `allow_delete.go`, `managed*.go`, `restore*.go`, `budget.go` writers) | managed gate text unchanged |
| CL7c | run and runs writers (`run.go` launch lines, `cancel`, `reaper`, `backlog` writers) | `run --json` untouched (G2) |

Each task: **Tests:** one `Test<Command>DefaultIsShort` per migrated command (named at launch), plus the existing tests of those files, plus `TestNoStepCommandPrintsAnsiOffATerminal`. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run '<the family>|Docs' -count=1 -timeout 40m`. **Red step:** the default-short test for the family fails before the move. **Commit:** `cli: <family> commands print through the reporter`. Pure listings (`runs ls`, `config list`, `models ls`, `doctor`, `settings`, `budget show`, `report`) are exempt (D1354) and listed in CL9.

### CL9. The census: which commands are on the reporter

**Depends on:** CL6, CL7a, CL7b, CL7c. **Reviewer:** standard.

- **Files (2):** `internal/cli/report_census_test.go` (new), `internal/cli/report_exempt.go` (new: the exempt listing set with a reason per command).
- **Tests:** `TestEveryCommandIsOnTheReporterOrExempt` (walks the cobra tree; a command with a step-like `Fprint` that is on neither fails the build), `TestExemptCommandsAreListings` (each exempt command has a reason string and a `--json` or table output).
- **Command:** `go test ./internal/cli/ -run 'ReporterOrExempt|ExemptCommands' -count=1`
- **Red step:** a new command with a print and no entry fails.
- **Commit:** `cli: pin which commands print through the reporter`

### CL10. `--json` is never affected (a pin, written early)

**Decisions:** D1352. **Findings:** F8. **Depends on:** CL1. **Reviewer:** standard.

- **Files (2):** `internal/cli/report_json_test.go` (new), `internal/cli/report_json_fixtures_test.go` (new).
- **Tests:** `TestJSONStdoutIsADocumentUnderEveryTerminalEnvironment` (for every command in the tree that has `--json` and a hermetic fixture: a fake terminal, `FORCE_COLOR`, `CLICOLOR_FORCE=1`, `--verbose`; stdout parses as one JSON document and has no byte 0x1B), `TestJSONReadsWriteNoLog` (G7), `TestJSONFixtureCoverage` (a `--json` command with no fixture is listed with a reason).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestJSON.*(Terminal|Log|Fixture)' -count=1 -timeout 40m`
- **Red step:** none is red on today's tree (it is a regression pin); **mutation:** make the reporter print a spinner in JSON mode and the first test fails.
- **Commit:** `cli: pin that --json is never touched by human formatting`

### CL11. Docs and release notes

**Depends on:** CL5b, CL9. **Reviewer:** standard.

- **Files (max 5):** `docs/design/v1.md` (the output contract: the three outcomes, the default view, the log directory, retention, `-v`), `README.md` (one sentence on `-v` and the log, only if a pinned README sentence names update output), `docs/releases/vX.Y.Z.md` and `website/data/release-summaries.yaml` (**written in the release PR of the version that ships CL5b**, not in this task if the version is unknown), the docs tests that pin the changed sentences.
- **Tests:** `go test ./internal/cli/ -run Docs`, and `npm run check` in `website/` if website content changed.
- **Release action:** `### For operators` is "run `fugaro update`" plus what it asks, per `docs/releases/README.md`; **no second command**.
- **Commit:** `docs: the CLI's default output, its log and --verbose`

### CL12. Owner-run checks (no agent performs any step)

| Check | Measures | Who | What the owner types |
| --- | --- | --- | --- |
| **310** | A real terminal on macOS: `fugaro update`, `fugaro setup --plan-only`, `NO_COLOR=1 fugaro update`, `fugaro update \| cat`, `fugaro update --json`, a forced failure (the context and the log path), the log file's mode and its retention after 51 runs | the owner | the five commands above, then `ls -l ~/.local/state/fugaro/logs` |
| **311** | The same on Windows Terminal and the legacy console (virtual-terminal mode through `termenv`; the spinner and colour) | the owner | the same commands in each console |
| **314** | Reserved, unused | | |

The check sections are written into `docs/gcp-live-checklist.md` by the task that implements the first slice that needs them, as **NOT RUN**; this plan writes none.

## Parallel execution

**Edges** (from the Depends-on lines): CL2, CL3, CL4, CL5a, CL10 depend on CL1; CL5b on CL3, CL4, CL5a; CL6, CL7a, CL7b, CL7c on CL5b; CL9 on CL6, CL7a-c; CL11 on CL5b, CL9. CL12 is the owner's, after CL5b (310) and the Windows build (311).

| Level | Tasks (run concurrently) |
| --- | --- |
| L0 | CL1 |
| L1 | CL2, CL3, CL4, CL5a, CL10 |
| L2 | CL5b |
| L3 | CL6, CL7a, CL7b, CL7c |
| L4 | CL9 |
| L5 | CL11 (then CL12, owner-run) |

**Critical path:** CL1 → CL3 → CL5b → CL6 (or the longest of CL7a-c) → CL9 → CL11. Sequential by nature: CL1 before everything (the interface), CL5b after CL3 and CL4 (it consumes both) and after CL5a (the loop it drives), CL9 after every migration (it censuses them).

**Shared files: no level holds two writers of one file.**

| File | Writer | Level |
| --- | --- | --- |
| `go.mod`, `go.sum` | CL1 | L0 |
| `internal/watch/tui.go` | CL2 | L1 |
| `internal/report/logsink.go` and its tests | CL3 | L1 |
| `internal/cli/root.go`, `report_flags.go`, `website/content/cli-reference.md` | CL4 | L1 |
| `internal/initflow/loop.go` | CL5a | L1 |
| `internal/cli/report_json_test.go` | CL10 | L1 |
| `internal/cli/init.go`, `update.go` | CL5b | L2 |
| `internal/cli/image*.go` | CL6 | L3 |
| the accounts, secrets, creds files | CL7a | L3 |
| the project and config writers | CL7b | L3 |
| the run and runs writers | CL7c | L3 |
| `internal/cli/report_census_test.go`, `report_exempt.go` | CL9 | L4 |
| `docs/design/v1.md` | CL11 | L5 |

The L3 batches must keep **disjoint file sets**; a docs test that two batches both break is edited by the batch whose command family the pinned sentence names, and a second batch that would also edit it moves to the next level (a re-check at launch from the real red run). The generated `website/content/cli-reference.md` is written only by CL4: no L3 task changes help text.

## What is not in this plan

A `--quiet` flag; `-vv` (the spec's "later"); a log viewer command; rewriting any `--json` shape; touching `doctor`'s classes; changing a refusal or a gate.
