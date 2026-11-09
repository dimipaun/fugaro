# Short run IDs everywhere: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Every task below needs the owner's explicit go; the owner's answers D1700-D1704 settled questions, they did not approve work. Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (F#, D# below refer to it).

Owner answers that shaped the plan: D1700 refuse and list on a non-terminal (an agent marker or `--json` counts as one), pick list on a terminal; D1701 four-character minimum; D1702 active runs then 7 days; D1703 runs only; D1704 live tab follows `watch`'s IDs, ordering and wording, not its column set.

## Conventions every part follows

- `CLAUDE.md`: targeted tests only (`go test ./internal/<pkg>/ -run ...`), `gofmt -l .` prints nothing, comments say why, no TODO comments, no new dependency, no secret-shaped literal in tests or fixtures (fake run IDs only: `20261108-115248-3bb2` style).
- Hermetic tests: `internal/gcpfake`, `internal/testutil`, `t.TempDir()`; no live cloud action by an agent (checks 440 and 441 are owner-run).
- **A field never changes meaning:** nothing in any `--json` shape, the run record, `task.json`, the PR marker, the branch name or `filed_in` changes (D1705). Docs are tested: change a doc, run `go test ./internal/cli/ -run Docs`; website content changes run `npm run check` in `website/`.
- `.claude/rules/console.md` for `console/**`; `.claude/rules/mobile.md` is not touched (the mobile app and `shared/` are out of scope: UNVERIFIED that it shows run IDs; a one-line grep at SR12).
- Never edit `.github/workflows/`. The plugin tests (`plugin/`) check every command and flag a skill names; a skill change is in the same PR as the CLI change.
- Review routing by changed paths: `internal/runner/prompts.go`, `plugin/skills/**` are **security-tier** (`opus + mandatory independent review`): prompts and plugin skills by path (SR7), and the tasks that edit a runner write path (SR6) or resolve the target of a destructive command (SR3, SR14). The rest is standard.

## Global constraints and review focus

| Input or failure mode | Pinned by |
| --- | --- |
| A short ID that matches two runs is never resolved silently on the read commands | SR3 (an agent marker or `--json` on a pty refuses the pick list too) |
| An ending shorter than four characters is refused | SR2 (resolver) and SR3 |
| A unique active match wins over a finished run with the same ending; the window is asked first and a miss widens to the runs the bucket still holds (reconciliation D1702, 2026-11-09) | SR2, SR3 |
| The console never sends a suffix to the CLI | SR9 (the door reads the attribute, a test with a colliding pair) |
| The full ID stays in the branch, marker, commit trailer, `task.json`, `--json`, `filed_in`; `run --retry` takes a full ID | SR11, SR14 |
| `cancel` never acts on an ending (D1710); a planted active match is not shown unless the bucket confirms it | SR14, SR3 (active matches confirmed in the bucket) |
| `digestSubmitRuns`'s `run-N` rows are not mangled | SR6 |
| The collision extension crosses the dash and stays a valid ending | SR1 |
| An active-feed read failure is said, never a silent narrowing | SR3 |
| The prompt rule does not tell an agent to withhold an ID a command needs | SR7 |
| No full run ID in a human renderer's output (watch plain/TUI, `runs ls`, digest, PR text, report, console list views) | SR11 sweep and SR10 |

## Slices and tasks

**Slice A, the rule (inert until used).** SR1, SR8. **Slice B, input.** SR2, SR3. **Slice C, CLI and agent-written output.** SR4, SR5, SR6, SR7, SR14. **Slice D, console.** SR9, SR10. **Slice E, guard and docs.** SR11, SR12, SR13. Slices B, C and D ship independently once A is in.

### SR1. One Go helper for the short ID and the collision rule

- **Files:** `internal/task/shortid.go` (new), `internal/task/shortid_test.go` (new), `internal/task/testdata/shortid_cases.json` (new; the case table the console copy is compared with, SR10).
- **Interfaces:** produces `ShortRun(id string) string` (last four characters, the whole string when it is shorter), `DisplayRuns(refs []Ref) map[Ref]string` over `Ref{Slug, RunID}` pairs (each run's shortest ending unique among the refs, never shorter than four, extended one character at a time through the dash; two refs with the same RunID in different repositories are shown as `slug/ending`), `MinEnding = 4`, `EndsWith(id, ending string) bool` (an ending of at least `MinEnding` characters). Consumes nothing.
- **Schema/rules/config change:** none.
- **Tests:** `TestShortRun`, `TestDisplayRunsCollision`, `TestDisplayRunsCrossesTheDash`, `TestEndsWithMinimum`, `TestDisplayRunsSameIDTwoRepos`.
- **Command:** `go test ./internal/task/ -run 'TestShortRun|TestDisplayRuns|TestEndsWith' -count=1`
- **Depends on:** none.
- **Commit:** `task: ShortRun and DisplayRuns, the one short-ID rule`
- **Red step:** the tests call the new functions and fail to compile; then assert `DisplayRuns` of `20261108-115248-3bb2` and `20261107-010101-3bb2` yields `8-3bb2` / `1-3bb2`.
- **Reviewer tier:** standard.

### SR2. The resolver: an ending to a run

- **Files:** `internal/runstore/resolve.go` (new), `internal/runstore/resolve_test.go` (new).
- **Interfaces:** consumes `task.EndsWith`, `ListSlugs`, `ListRunIDs`, `Locate`. Produces `ResolveEnding(ctx, b *blob.Bucket, active func(context.Context) ([]Ref, error), ending string, since time.Time) ([]Match, error)` where a `Match` is `{Slug, RunID, Active bool}`; it returns the active matches if there are any, else the recent matches; a full ID or `slug/id` is handed to `Locate`'s rules unchanged. Errors: `ErrTooShort`, `ErrNotFound`, `ErrAmbiguous` carrying the matches.
- **Schema/rules/config change:** none.
- **Tests:** `TestResolveEndingActiveBeatsRecent`, `TestResolveEndingWindow`, `TestResolveEndingTooShort`, `TestResolveEndingAmbiguousAcrossRepos`, `TestResolveEndingFullIDUnchanged` (the in-memory bucket in `internal/testutil`).
- **Command:** `go test ./internal/runstore/ -run 'TestResolveEnding' -count=1`
- **Depends on:** SR1.
- **Commit:** `runstore: resolve a run ID ending, active runs then the last 7 days`
- **Red step:** the tests fail until the package has the function; the window test seeds a run 8 days old and asserts `ErrNotFound`.
- **Reviewer tier:** standard.

### SR3. `resolveRun`: an ending to a run, with the pick list or the refusal

- **Files:** `internal/cli/cloud.go`, `internal/cli/cloud_test.go` (or the file the existing `locateRun` tests live in).
- **Interfaces:** consumes SR2, `stdinIsTerminal` and `agentMarker`. `locateRun` is left exactly as it is (it keeps serving `run --retry`, which stays full-ID); SR3 adds `resolveRun(ctx, env, ref, opts)` beside it, with options for the input and output streams, `--json` and whether the lookup is active-only (used by `cancel`'s list-only refusal), because `locateRun` has none of them. Nothing calls `resolveRun` until SR14, so the build is whole at L2. Produces: on a terminal (stdin and stdout terminals, no `--json`, empty `agentMarker`) a numbered list showing repository, title, status, start time and the `task.DisplayRuns` ending; anywhere else a user error listing the same candidates; the "no run ending ... among the active runs or the runs the bucket still holds; pass the full ID" sentence (D1709, moved by D1702's widening of 2026-11-09); the active feed read from the budget database (one new read per lookup), each active match confirmed as a valid run ID with a `task.json` in the bucket, and a stderr line when the feed could not be read. A full ID or `<slug>/<run-id>` behaves as `locateRun` does.
- **Schema/rules/config change:** none.
- **Tests:** `TestResolveRunEnding`, `TestResolveRunAmbiguousNonTerminalRefuses`, `TestResolveRunAmbiguousTerminalPicks`, `TestResolveRunAgentMarkerRefuses`, `TestResolveRunJSONRefuses`, `TestResolveRunPlantedActiveMatchIgnored`, `TestResolveRunFeedUnreadable`, `TestLocateRunStillRefusesAnEnding`.
- **Command:** `go test ./internal/cli/ -run 'TestResolveRun|TestLocateRun' -count=1`
- **Depends on:** SR2.
- **Commit:** `cli: resolveRun takes the last four or more characters of a run ID`
- **Red step:** `TestResolveRunAmbiguousNonTerminalRefuses` seeds two runs with one ending and asserts the error lists both and none is returned; it fails until `resolveRun` exists.
- **Reviewer tier:** `opus + mandatory independent review` with a mutation check: it resolves the target of a destructive command from a bucket the job account can write.

### SR4. `watch` plain and TUI on the one rule

- **Files:** `internal/watch/render.go`, `internal/watch/plain.go`, their `_test.go` files and `internal/watch/testdata/*.golden`.
- **Interfaces:** consumes SR1; removes `shortRun`, widens the TUI's RUN column (`colsOf`, fixed at `shortRunLen`) to the longest displayed ending, prints the short ID (extended where two printed rows collide) in the `RUN` and `FINISHED` columns of the plain renderer and the TUI. `--json` untouched.
- **Schema/rules/config change:** none.
- **Tests:** `TestPlainShortRun`, `TestPlainCollision`, `TestTUIShortRunCollision`, the existing golden tests re-recorded.
- **Command:** `go test ./internal/watch/ -run 'ShortRun|Collision|Golden|Plain' -count=1`
- **Depends on:** SR1.
- **Commit:** `watch: the plain output shows short run IDs, extended on a collision`
- **Red step:** a plain-output test asserting no 15-digit stamp appears fails against today's `run.Run`.
- **Reviewer tier:** standard.

### SR5. `runs ls` and `plan status` on the one rule

- **Files:** `internal/cli/runs.go`, `internal/plan/state.go`, `internal/cli/runs_test.go` (the existing test files for each).
- **Interfaces:** consumes SR1; the `runs ls` human table prints the short ID with the repository in its own column (the `<slug>/` prefix moves out of the ID cell); `plan.runSuffix` becomes `task.ShortRun` (same four characters on a valid ID; `plan status --json`'s `run` field is unchanged).
- **Schema/rules/config change:** none.
- **Tests:** `TestRunsLsHumanShortID`, `TestRunsLsJSONKeepsFullID`, `TestRunSuffixIsShortRun`.
- **Command:** `go test ./internal/cli/ -run 'TestRunsLs' -count=1 && go test ./internal/plan/ -run 'TestRunSuffix' -count=1`
- **Depends on:** SR1.
- **Commit:** `runs ls, plan: one short-ID rule`
- **Red step:** a human-output test asserting no full ID fails against the current `Row.Run`; the JSON test passes before and after (it pins the contract).
- **Reviewer tier:** standard.

### SR6. Agent-written text from code: the PR text, the report, the digest

- **Files:** `internal/runner/prflow.go`, `internal/runner/report.go`, `internal/runner/reviewmode.go` (the second `### Fugaro run` heading), `internal/runner/runner.go` (`Opened by Fugaro run`), `internal/digest/render.go` (`markdownCore` shortens; `RunsTable`'s submit use is pinned), and their tests.
- **Interfaces:** consumes SR1. The title fallback, the status line (`Run \`3bb2\` ... \`fugaro diagnose 3bb2\``), the report headings, the PR opening line and the digest report's run table (collisions extended over the rows printed, shortened in `markdownCore` so `digestSubmitRuns`'s `run-N` rows are untouched) show the short ID. The branch, `ReportMarker`, the commit trailer, `filed_in` and the digest JSON keep the full ID (D1705).
- **Schema/rules/config change:** none.
- **Tests:** `TestStatusLineShortID`, `TestReportHeadingShortID`, `TestMarkerKeepsFullID`, `TestDigestRunTableShortID`, `TestDigestSubmitRunsUnchanged`, `TestOpenedByLineShortID`.
- **Command:** `go test ./internal/runner/ -run 'TestStatusLine|TestReportHeading|TestMarker|TestOpenedByLineShortID' -count=1 && go test ./internal/digest/ ./internal/cli/ -run 'ShortID|TestDigestSubmitRunsUnchanged' -count=1`
- **Depends on:** SR3 (the hint `fugaro diagnose <short>` must work where the text ships).
- **Commit:** `runner, digest: written text names runs by their short ID`
- **Red step:** the status-line test asserts the 15-digit stamp is absent and fails today.
- **Reviewer tier:** `opus + mandatory independent review`: it edits text the runner posts to a pull request, a write path.

### SR7. The agent rule: the job prompts and the plugin skills

- **Files:** `internal/runner/prompts.go`, `internal/runner/prompts_test.go`, `plugin/skills/working/SKILL.md`, `plugin/skills/working/reference/launch.md`, `followup.md`, `logs.md`, `status.md`, `diagnose.md`, `periodic-reviews.md`, `plugin/skills/parallelism/SKILL.md`, the plugin tests that read them.
- **Interfaces:** consumes SR3 (a skill that says "pass the short ID" ships only with the CLI that accepts it). Adds to the standing instructions: "Refer to runs only by their short ID, the last 4 characters of the run ID; refer to plan tasks by their plan name. Pass the short ID to `logs`, `diagnose` and `artifacts` (the commands that accept it; `cancel`, `run --retry`, `--review-run`, `review show` and `--run-id` take full IDs, which `--json` output carries), and never print the full run ID in anything you write for a person." The skills' text: tell the user a run by its short ID, keep `--json` full IDs for commands.
- **Schema/rules/config change:** none (prompt text).
- **Tests:** `TestPromptsCarryShortIDRule`, the plugin tests (`go test ./plugin/... -count=1`), `-run Docs` for any pinned sentence.
- **Command:** `go test ./internal/runner/ -run 'TestPromptsCarryShortIDRule' -count=1 && go test ./plugin/... -count=1`
- **Depends on:** SR3.
- **Commit:** `prompts, skills: runs are named by their short ID`
- **Red step:** the prompt test fails until the sentence is in the standing instructions.
- **Reviewer tier:** `opus + mandatory independent review` (agent prompts and plugin skills; the routing is by path).

### SR8. The console helper

- **Files:** `console/src/shared/shortid.ts` (new), `console/test/unit/shortid.test.ts` (new).
- **Interfaces:** produces `shortRun(id)` and `displayRuns(refs)` over `{slug, runId}` pairs with SR1's rule; its cases are a copy of SR1's table, and SR10's Go test fails if the two tables differ.
- **Schema/rules/config change:** none.
- **Tests:** `shortid.test.ts` (the cases of `TestShortRun`, `TestDisplayRunsCollision`, `TestDisplayRunsCrossesTheDash`).
- **Command:** `cd console && npm run test:unit -- --maxWorkers=2 shortid`
- **Depends on:** none.
- **Commit:** `console: one short-run-ID helper`
- **Red step:** the test imports a module that does not exist.
- **Reviewer tier:** standard.

### SR9. The live tab: short IDs, ordering, wording, BY

- **Files:** `console/src/renderer/screens/live.ts`, `console/src/renderer/entry.ts` (the door, the only writer of `entry.ts` in the plan), `console/test/unit/live.test.ts`, `console/test/unit/entry-wiring.test.ts`, `console/test/e2e/entry.spec.ts`, `console/test/e2e/live.spec.ts` (all read the `live-run-id` text today).
- **Interfaces:** consumes SR8. The RUN cell draws the short ID (extended over the repository's rows); `data-run-id` carries the full ID from the sanitized field; `wireRuns` and the review row read the attribute; rows are ordered by start time, then ID; the state wording is `watch`'s; a BY cell shows the launcher (`watch --json`'s launcher field; UNVERIFIED name, read at SR9's start). The table's pipeline, models and rigor chip stay (D1704).
- **Schema/rules/config change:** none; `console/src/shared/schema.ts` already lists the CLI's strings and is not edited (a field not retyped).
- **Tests:** `live.test.ts`: `shows the short ID`, `a colliding pair is extended`, `the door opens the full ID`, `orders by start then id`, `draws the BY cell`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 live`
- **Depends on:** SR8.
- **Commit:** `console: the live tab names runs by their short ID and orders like watch`
- **Red step:** `the door opens the full ID` with a colliding pair fails while the door still reads the drawn text.
- **Owner-only:** the live screenshots `console/test/e2e/__screenshots__/live-{dark,light}-darwin.png` change; re-recording them is the owner's (`.claude/rules/console.md`), so the PR notes it and does not touch them.
- **Reviewer tier:** standard (renderer; the console's spawn/IPC allow-list is not touched).

### SR10. The other console screens, and the live-tab parity test

- **Files:** `console/src/renderer/screens/coordinator.ts`, `degraded.ts`, `graph.ts`, `console/src/renderer/graph/model.ts`, `console/src/renderer/confirm.ts`, `console/test/unit/coordinator.test.ts`, `graph*.test.ts`, `console/test/unit/live-parity.test.ts` (new), its fixtures `console/test/fixtures/watch-*.json` and `console/test/fixtures/shortid_cases.json` (new), `internal/watch/console_parity_test.go` (new) and the console-boundary test's allow-list entry for it (the file `internal/cli/docs_console_boundary_test.go` pins; UNVERIFIED whether it applies to a test outside `internal/cli`, read at the task's start).
- **Interfaces:** consumes SR8, SR9 (the door attribute). List views draw the short ID with the attribute pattern; the details of a run and the confirm dialog keep the full ID; `graph.ts`'s `slice(-4)` and `model.ts`'s `RUN_SUFFIX_RE` move onto the helper. `internal/watch/console_parity_test.go` regenerates the fixtures from `watch`'s own renderer over fixed inputs and fails when the checked-in console copy differs (so a change to `watch` fails until the console fixtures are re-recorded), and asserts `shortid_cases.json` equals `internal/task/testdata/shortid_cases.json` (SR8's cases and SR1's cannot drift). The parity test feeds `watch --json` fixtures recorded from SR4's tree to the live model and asserts the row order, the short IDs and the status words equal what SR4's golden shows.
- **Schema/rules/config change:** none.
- **Tests:** `coordinator.test.ts` and `graph` cases re-pinned; `live-parity.test.ts`: `rows follow watch's order`, `status words follow watch`, `short IDs follow watch`.
- **Owner-only:** the coordinator and graph screenshots under `console/test/e2e/__screenshots__/` change; re-recording is the owner's.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 'live-parity|coordinator|graph'` and `go test ./internal/watch/ -run Parity -count=1`
- **Depends on:** SR9, SR4 (fixtures).
- **Commit:** `console: list views use short run IDs; the live tab is pinned to watch`
- **Red step:** the parity test fails against SR9's tree wherever the console's order or wording differs from `watch`'s fixtures.
- **Reviewer tier:** standard.

### SR14. The other CLI printers and the command help

- **Files:** `internal/cli/cancel.go`, `logs.go`, `diagnose.go`, `artifacts.go`, `run.go` (the launch output and the print sites a sweep finds; `retrySpec` is untouched and keeps calling `locateRun`), `followup.go`, and their tests.
- **Interfaces:** consumes SR1 and SR3. `logs`, `diagnose` and `artifacts` switch from `locateRun` to `resolveRun`; `cancel` keeps `locateRun` for the act and, given an ending, calls `resolveRun` active-only to **list** the matches as `<slug>/<full run ID>` plus title and exit as a user error without cancelling (D1710). Human messages print `task.ShortRun` (the `cancel` messages, the launch output, notes and errors that name a run); `--json` unchanged. The `RUN` argument help of `logs`, `diagnose`, `artifacts` reads "a run ID, `<repo-slug>/<run-id>`, or the last four or more characters of a run ID"; `cancel`'s and `run --retry`'s help say they take a full ID. `artifacts --repo` resolves an ending within that repository (it checked the shape with `runstore.RunTime` before).
- **Schema/rules/config change:** none.
- **Tests:** `TestCancelEndingListsAndNeverActs` (an ending, one or two matches: nothing cancelled, each full `slug/id` listed), `TestCancelEndingIgnoresFinishedRuns`, `TestCancelFullIDStillCancels`, `TestArtifactsRepoEnding`, `TestLaunchOutputShortID`, `TestRetryEndingRefused`, `TestReviewRunEndingRefused`, `TestReviewShowEndingRefused`, `TestRunIDFlagKeepsFullID` (D1705's input side), plus `-run Docs`.
- **Command:** `go test ./internal/cli/ -run 'TestCancel|TestArtifactsRepoEnding|TestLaunchOutputShortID|TestRetryEndingRefused|TestReviewRunEndingRefused|TestReviewShowEndingRefused|TestRunIDFlagKeepsFullID|Docs' -count=1`
- **Depends on:** SR3.
- **Commit:** `cli: run commands print short IDs and say they take an ending`
- **Red step:** `TestCancelEndingListsAndNeverActs` fails against today's `cancel`, which rejects an ending as malformed and lists nothing.
- **Reviewer tier:** `opus + mandatory independent review` with a mutation check on the cancel tests (a destructive command).

### SR11. The sweep: no full run ID in human output

- **Files:** `internal/cli/shortid_sweep_test.go` (new, test only).
- **Interfaces:** consumes SR3 to SR6 and SR14. One test runs each human renderer over a fake installation (`internal/gcpfake`, `internal/testutil`) holding runs with distinct and colliding endings and asserts no `YYYYMMDD-HHMMSS-` stamp in the output; a second asserts the machine surfaces (`--json`, branch, marker, commit trailer, `filed_in`, `task.json`) still carry it whole (D1705). The test's allow-list of details views is explicit so a new printer fails it.
- **Schema/rules/config change:** none.
- **Tests:** `TestHumanOutputHasNoFullRunID`, `TestMachineSurfacesKeepFullRunID`.
- **Command:** `go test ./internal/cli/ -run 'TestHumanOutputHasNoFullRunID|TestMachineSurfacesKeepFullRunID' -count=1`
- **Depends on:** SR3, SR4, SR5, SR6, SR14.
- **Commit:** `cli: pin that no human output prints a full run ID`
- **Red step:** run it before SR4 to SR6's changes land: it lists each printer that still prints a stamp.
- **Reviewer tier:** standard.

### SR12. Docs and website

- **Files:** `docs/` pages that show a run ID in an example (found with `git grep -l '[0-9]\{8\}-[0-9]\{6\}-[0-9a-f]\{4\}' -- docs README.md website/content`), `website/content/cli-reference.md`, the pinned doc tests; a one-line grep of `mobile/` and `shared/` for run-ID display, filed as a follow-up task if it finds one.
- **Interfaces:** consumes SR3 to SR7, SR9, SR10, SR14: documents "the last four or more characters", the pick list and the non-terminal refusal, the 7-day window, the live-tab ordering. Release notes are written at the release cut (`docs/releases/README.md`); their `### For operators` is "run `fugaro update`" (D1708).
- **Schema/rules/config change:** none.
- **Tests:** `go test ./internal/cli/ -run Docs -count=1`; `cd website && npm run check` (about 7 minutes) because website content changes.
- **Command:** the two above.
- **Depends on:** SR3, SR4, SR5, SR6, SR7, SR9, SR10, SR11, SR14.
- **Commit:** `docs: short run IDs`
- **Red step:** the docs tests fail where a pinned sentence or example changed and the wording is not updated in both places.
- **Reviewer tier:** standard.

### SR13. OWNER-RUN checks 440 and 441

- **Check 440 (live; owner-run).** On the dev installation, launch two runs with `--run-id` values chosen to share their last four characters (so the collision is certain) and note their IDs; the owner types `fugaro logs <last 4 of one>`, `fugaro diagnose <ending shared by two>` (expect the pick list on a terminal, the refusal when piped: `fugaro diagnose <ending> < /dev/null | cat`), `fugaro diagnose abc` (expect "at least four characters"), `fugaro cancel <ending of an active run>` (expect that nothing is cancelled and the full `slug/id` is listed), `fugaro cancel <ending of a finished run>` (expect "no active run"), and `fugaro diagnose <short of an 8-day-old run>` (expect its row: the lookup widens past the window when the window has no match, D1702). It measures the resolution latency over the last 7 days (the UNVERIFIED cost in F5). No agent performs any step.
- **Check 441 (owner-run, visual).** Open the console live tab beside `fugaro watch` and compare IDs, ordering and wording. The owner also re-records the live screenshots (SR9). No agent performs any step.
- **Depends on:** SR3 and SR9 released.

## Parallel execution

Edges (from "Depends on"): SR2←SR1; SR3←SR2; SR4←SR1; SR5←SR1; SR6←SR3; SR7←SR3; SR9←SR8; SR10←SR9,SR4; SR14←SR3; SR11←SR3,SR4,SR5,SR6,SR14; SR12←SR3,SR4,SR5,SR6,SR7,SR9,SR10,SR11,SR14.

| Level | Tasks (run concurrently) |
| --- | --- |
| L0 | SR1, SR8 |
| L1 | SR2, SR4, SR5, SR9 |
| L2 | SR3, SR10 |
| L3 | SR6, SR7, SR14 |
| L4 | SR11 |
| L5 | SR12 |
| After release | SR13 (owner) |

**Critical path:** SR1 → SR2 → SR3 → SR14 → SR11 → SR12 (six levels). The console chain SR8 → SR9 → SR10 → SR12 is as long but independent of the Go chain until SR10 reads SR4's fixtures. **Sequential by necessity:** SR3 after SR2 (an interface), SR6, SR7 and SR14 after SR3 (their text names a command that must accept the short ID), SR10 after SR9 (the door attribute), SR12 after everything it documents.

**Shared files (no level holds two writers of a file):**

| File | Writer(s) | Level(s) |
| --- | --- | --- |
| `internal/task/shortid.go` | SR1 | L0 |
| `console/src/shared/shortid.ts` | SR8 | L0 |
| `internal/runstore/resolve.go` | SR2 | L1 |
| `internal/watch/render.go`, `plain.go`, `testdata/*.golden` | SR4 | L1 |
| `internal/cli/runs.go`, `internal/plan/state.go` | SR5 | L1 |
| `console/src/renderer/screens/live.ts`, `console/src/renderer/entry.ts` | SR9 | L1 |
| `internal/cli/cloud.go` | SR3 | L2 |
| `internal/cli/{cancel,logs,diagnose,artifacts,run,followup}.go` | SR14 | L3 |
| `internal/watch/console_parity_test.go`, `console/test/fixtures/*` | SR10 | L2 |
| `console/src/renderer/screens/{coordinator,degraded,graph}.ts`, `graph/model.ts`, `confirm.ts` | SR10 | L2 |
| `internal/runner/{prflow,report,reviewmode,runner}.go`, `internal/digest/render.go` | SR6 | L3 |
| `internal/runner/prompts.go`, `plugin/skills/**` | SR7 | L3 |
| `internal/cli/shortid_sweep_test.go` | SR11 | L4 |
| `docs/**`, `website/content/cli-reference.md` | SR12 | L5 |

`internal/runner/` holds SR6 (`prflow.go`, `report.go`, `reviewmode.go`, `runner.go`) and SR7 (`prompts.go`) in one level: different files, and their test files differ too; a change to either task's Files re-checks this table. SR5 edits `internal/cli/runs.go` at L1 and SR3 `internal/cli/cloud.go` at L2: different files, and different levels.

## Checks

Checks 440 and 441 (SR13): owner-run, as above. Nothing in this plan needs a backfill, a secret or a config change after deploy, so there is no release-actions entry beyond "run `fugaro update`" (D1708).
