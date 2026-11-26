# CI selects tests from what changed: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's explicit go by name.** It builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it: the release gate requires a full run on the tag's commit (D3040); one scheduled nightly full run (D3041); the selector is a Go program with tests under `scripts/` (D3042); a recorded read-map, refreshed by the full run, says which tests a file change reaches beyond imports (D3043). Resolutions R1-R12 of the reconciliation fix the rest.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `docs/release.md`. No path rule of `.claude/rules/` loads for `internal/ciselect` or `scripts/`.

- **Where the code lives (D3042).** The program and its end-to-end tests are `scripts/ci-select`; the reusable parts are `internal/ciselect`, because this repository keeps logic in `internal/` and `main` packages thin (`cmd/fugaro` is only `main`).
- **Always.** Targeted tests, never a whole package. Hermetic: no network, no home directory, `t.TempDir()`, fixture `go list -json` output, a fake `git` repository built in `t.TempDir()`, a fake `gh` behind an interface; never a call to GitHub. A new doc is covered by `go test ./internal/cli/ -run Docs -count=1`.
- **Ask first.** A new dependency (the selector uses the standard library only); a trigger other than `workflow_dispatch` and the one `schedule:` (D3041).
- **Never.** An edit under `.github/workflows/` from an agent run (CS6 is written down and copied by the owner's word, as #3301 was); a selector that fails open (R8); a secret-shaped literal; a confirmation step; a live GitHub call from a test.

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The selector never selects fewer tests than a full run would have failed on | CS4's golden cases on the real parts: `internal/catalog/fetch.go` selects `./plugin` (F2); an edit under `internal/infra/tf/faketerraform` selects `./internal/cli` (F2, the build-by-import-path case); an embedded template selects its docs readers (F12); `.github/workflows/ci.yml` selects everything; an unmapped path selects everything |
| Doubt runs everything (R8) | CS4's table test, one row per doubt in R8 |
| A selected run is never the base of the next, and the changed files are a two-dot diff (R2) | CS4: the base function and the diff are the program's own, tested against a fake `gh` and a fake repository |
| The gate counts only a full run's checks, including through the identical-tree fallback (R1) | CS7's gate tests, one case per title and one for the fallback |
| The committed read-map is not stale and names every literal path and embed | CS5 |
| `terraform` and `rules` run on every full run and are chosen from the tagged graph on a selected one (R6) | CS4's tests; CS1's graph tests under the tags |
| The nightly is titled and run as full (R11) | CS6's written text, read by the guards in a clean worktree |

## 3. Slices

1. **The selector (CS1, CS2a, CS3, CS4):** a program that, given a head, prints the legs to run. Inert until CS6.
2. **The proof it is right (CS2b, CS5):** the runtime trace and the test that fails when the map is stale.
3. **The gate and the workflow (CS7, CS7b, CS6, CS8):** the release gate reads the run's title; the hotfix path dispatches a full run; `ci.yml` calls the selector, takes a `full` input and a nightly schedule.
4. **The measurement (CS9):** one owner-run check.

## 4. Tasks

Each of CS1, CS2a, CS3 and CS4 owns the types it declares; CS4 composes the other three, so nothing is declared twice.

### CS1: the reverse import graph
- Acceptance: `Affected(graph, changedFiles)` returns every package that imports a changed package directly or transitively, test imports included (`-test`), built under the union of the tags `terraform`, `firebase`, `docker` and `live` (R6), and treats `EmbedFiles` and `TestEmbedFiles` as edges from the file to its package (F12), from fixture `go list -deps -test -json` output.
- Files: `internal/ciselect/graph.go` (new), `internal/ciselect/graph_test.go` (new).
- Interfaces: declares `Graph` and `Affected`; produces the package set CS4 unions with the read-map's.
- Tests: `TestAffectedFollowsImportsTransitively`, `TestAffectedIncludesTestOnlyImports`, `TestAffectedFollowsAnEmbeddedFile`, `TestAffectedUnionsTheBuildTags`, `TestAffectedOfAnUnmappedFileIsEmpty`.
- Command: `go test ./internal/ciselect/ -run 'TestAffected' -count=1`
- Red step: `TestAffectedFollowsAnEmbeddedFile` fails against a graph that reads imports only.
- Depends on: none.
- Commit: `ciselect CS1: the reverse import graph, embeds and tags included`
- Reviewer tier: standard.
- Owner-run: no.

### CS2a: the read-map, the static scan
- Acceptance: `scripts/ci-readmap` reads every `_test.go` file in the repository, and every non-test Go file that reads a repository path (`internal/testutil/fixture.go` is the known one; its reads go to every test package that imports it, through CS1's graph), and follows the shell a test runs: a script a test executes brings in the files it sources with `.`/`source` (`scripts/release-gate.sh` sources `scripts/retry.sh`; `scripts/release.sh` sources `versions.sh` and `hotfix-commits.sh`), and their reads go to every test package that executes the script; it writes `docs/ci-affected-tests/read-map.json`: per package the repository files its tests name by literal (`../../docs/x.md`, `readRepoFile("a/b.go")`, `repoRoot()` joins), globs where the literal is a directory, the closure of a package named by a `go build`/`go run` import path (F2), `any` where a path is built dynamically or a test walks the tree (every walker, F3: no list of three), and for a walker the `ignores` it states; the file is deterministic (sorted).
- Files: `internal/ciselect/scan.go` (new), `internal/ciselect/scan_test.go` (new), `scripts/ci-readmap/main.go` (new), `docs/ci-affected-tests/read-map.json` (new).
- Interfaces: declares `ReadMap` (with its `reads` and `ignores` fields from the start, so CS2b adds values and no field) and `Scan`; produces the committed map CS4, CS5 and CS2b use.
- Tests: `TestScanFindsLiteralRepoPaths`, `TestScanMarksADynamicPathAny`, `TestScanMarksEveryWalkerAny`, `TestScanFollowsABuildByImportPath`, `TestScanAttributesAHelpersReadsToItsImporters`, `TestScanFollowsASourcedShellLibrary`, `TestScanIsDeterministic`.
- Command: `go test ./internal/ciselect/ -run 'TestScan' -count=1`
- Red step: `TestScanFollowsABuildByImportPath` fails against a scan of path literals only.
- Depends on: none.
- Commit: `ciselect CS2a: the read-map, written by a scan of the tests`
- Reviewer tier: standard.
- Owner-run: no.

### CS2b: the read-map, the runtime trace
- Acceptance: the trace step ships behind a flag with the safe default (every package that spawns a build or executes a repository script is marked `any`); the full run records, per shard, the repository files the test process and its children opened (`strace -f` where CS9's measurement on `ubuntu-latest` says it is worth its cost, which CS9 writes down and a later change to the flag follows), reduces them to repository-relative paths and merges them into the map as `reads`; `ci-readmap --from-artifact <dir>` does the merge (R10); a path the trace found and the scan did not is printed as a scan gap.
- Files: `internal/ciselect/trace.go` (new), `internal/ciselect/trace_test.go` (new), `scripts/ci-readmap/trace.go` (new).
- Interfaces: consumes CS2a's `ReadMap`; produces the map with traced `reads`.
- Tests: `TestTraceReducesOpensToRepoPaths`, `TestTraceReportsAScanGap`, `TestTraceIgnoresTheGoBuildCache`, `TestFromArtifactMergesIntoTheCommittedMap`.
- Command: `go test ./internal/ciselect/ -run 'TestTrace|TestFromArtifact' -count=1`
- Red step: `TestTraceReportsAScanGap` fails with a trace that finds nothing extra.
- Depends on: CS2a (the scan's map it refines).
- Commit: `ciselect CS2b: the runtime trace refines the read-map`
- Reviewer tier: standard.
- Owner-run: no.

### CS3: timings and the packer
- Acceptance: `Pack(chosenPackages, timings, maxLegs)` returns legs by longest-first greedy, keeps the `internal/cli` update and other families whole as `Only: update|other` legs (F4), splits a chosen `./scripts` into its `nonlive` leg and its live legs (`Only: live` with the `LIVE_CHECK_SHARD` index and count, as today's six), reads timings from `test-times` TSV text, and with none returns equal weights; the `Leg` field names are pinned by a list in its own test, not read from `ci.yml`.
- Files: `internal/ciselect/pack.go` (new), `internal/ciselect/pack_test.go` (new).
- Interfaces: declares `Leg` and `Pack`; produces the legs CS4 prints.
- Tests: `TestPackIsLongestFirst`, `TestPackKeepsTheCliFamiliesWhole`, `TestPackSplitsScriptsIntoLiveAndNonlive`, `TestPackWithoutTimingsIsEqualWeight`, `TestLegFieldNamesArePinned`.
- Command: `go test ./internal/ciselect/ -run 'TestPack|TestLeg' -count=1`
- Red step: `TestPackIsLongestFirst` fails against an unsorted pack.
- Depends on: none.
- Commit: `ciselect CS3: timings pack the chosen packages into legs`
- Reviewer tier: standard.
- Owner-run: no.

### CS4: the selector program
- Acceptance: `scripts/ci-select` prints `Output` (`Full`, `Reason`, `Legs`) as JSON and a summary on `$GITHUB_STEP_SUMMARY`; it finds the base as the head of the last successful `ci (full)` run on `main` through an injected `gh` interface, downloads the last full run's `test-times-*` through the same interface (a failure means equal weights, R5), takes the changed files from `git diff --name-only --no-renames <base> <head>` in a full checkout (R2), unions CS1's affected packages with the read-map's, packs them with CS3, returns `Full: true` with a reason for every doubt in R8, chooses `terraform` and `rules` from the tagged graph (R6), always selects `check`, and for a documents-only change selects the Docs-reading packages as whole families (R7); an empty leg set prints `legs: []` and CS6's `shard` job is skipped on it (GitHub fails an empty matrix; the `test` job already counts a skipped shard as success); "unmapped" means a path that no read-map entry names, no package of the graph owns and no `ignores` rule excludes, and an `any` entry does not map every path.
- Files: `scripts/ci-select/main.go` (new), `internal/ciselect/select.go` (new), `internal/ciselect/select_test.go` (new).
- Interfaces: declares `Input`, `Output` and `Select`; composes CS1, CS2a and CS3.
- Tests: `TestSelectIsFullForEveryDoubtOfR8` (one row each), `TestSelectBaseIsTheLastFullRunOnMain`, `TestSelectUsesATwoDotDiffWithoutRenames`, `TestSelectCatalogFetchReachesPlugin`, `TestSelectFakeTerraformReachesCli`, `TestSelectAnEmbeddedTemplateReachesItsDocsReaders`, `TestSelectDocsOnlyRunsTheDocsReaders`, `TestSelectFixtureRepoReachesFixtureFilesCallers`, `TestSelectSourcedShellLibraryReachesItsCallers` (`scripts/retry.sh` selects `./internal/cli`), `TestSelectAnEmptyLegSetSkipsTheShardJob`, `TestSelectTimingsDownloadFailureIsEqualWeights`, `TestSelectTerraformAndRulesFromTheTaggedGraph`, `TestSelectWorkflowChangeIsFull`.
- Command: `go test ./internal/ciselect/ -run 'TestSelect' -count=1`
- Red step: each fails against an empty `Select`.
- Depends on: CS1, CS2a, CS3 (behaviour: it composes the real code; a stub would be a second implementation).
- Commit: `ciselect CS4: the selector program`
- Reviewer tier: opus with a mandatory independent review: it decides what a CI run proves.
- Owner-run: no.

### CS5: the map cannot rot
- Acceptance: a test fails when a repository path named by a test literal or a `go:embed` is missing from the committed map, when a map entry names a file that no longer exists, and when a Docs pin's file (`internal/cli/docs_*_test.go`) is not reached by the Docs readers' entry (spec item 5).
- Files: `internal/ciselect/readmap_test.go` (new).
- Interfaces: consumes CS2a's scan and the committed `read-map.json`.
- Tests: `TestReadMapNamesEveryLiteralPath`, `TestReadMapNamesEveryEmbed`, `TestReadMapNamesNoMissingFile`, `TestReadMapReachesEveryDocsPin`.
- Command: `go test ./internal/ciselect/ -run 'TestReadMap' -count=1`
- Red step: delete one entry from a copy of the map; the first test fails.
- Depends on: CS2a (the scan it re-runs).
- Commit: `ciselect CS5: the read-map is pinned against the tests it describes`
- Reviewer tier: standard.
- Owner-run: no.

### CS7: the release gate reads that the run was full
- Acceptance: `scripts/release-gate.sh` lists the workflow runs of the tag's SHA (`actions/runs?head_sha=`), keeps the `ci` runs titled `ci (full)` or exactly `ci`, reads those runs' check suites for `test`, `terraform` and `rules` only (the console legs keep today's reading) and requires success from them, from runs of the `ci` workflow file only; a `ci (selected)` run is ignored, and a SHA that has only selected runs is refused at once with the command to dispatch a full run (`gh workflow run ci --ref main -f full=true` for main's tip) printed, instead of waiting out the timeout on "missing"; the identical-tree fallback applies the same title rule; `test`'s state no longer combines other runs of the name. The first step measures whether a token without `actions: read` can list runs and writes the answer in the task's pull request.
- Files: `scripts/release-gate.sh`, `scripts/release_test.go`, `scripts/testdata/fake-gh.sh`, `internal/cli/docs_desktop_console_test.go` (its `gateFakeGH` answers the new calls).
- Interfaces: consumes R1's title strings (a decision, not a code interface); produces the gate CS6 needs before it is copied in.
- Tests: `TestGateCountsAFullRunsChecks`, `TestGateIgnoresASelectedRun`, `TestGateAcceptsTheOldTitle`, `TestGateFallbackAppliesTheTitleRule`, `TestGateKeepsTheConsoleLegsReading`, and the existing `TestFakeGHAnswersEveryCheckTheGateRequires`.
- Command: `go test ./scripts/ -run 'TestGate|TestFakeGH' -count=1` and `go test ./internal/cli/ -run 'ReleaseGate' -count=1`
- Red step: `TestGateIgnoresASelectedRun` fails against the unchanged gate.
- Depends on: none.
- Commit: `release-gate CS7: the checks must come from a full run`
- Reviewer tier: opus with a mandatory independent review: a CI gate.
- Owner-run: no.

### CS7b: a release dispatches its own full run
- Acceptance: `scripts/release.sh`'s ordinary path, after the bump's merge, dispatches `gh workflow run ci -f full=true` when no `ci (full)` run has the merge commit as its head and waits for the run whose head is that commit (R12; a dispatch is by ref, so the script reads the run it started and, when the head is not the merge commit because main moved on, refuses naming the commit and the command `gh workflow run ci --ref <a branch that points at that commit> -f full=true`; it creates no ref itself); its hotfix path dispatches the same on `release/X.Y`, passing `-f full=true` only when that branch's `ci.yml` declares the input, and `-f live_scripts=always` when it declares that one (a `ci.yml` from #3301 on, whose `auto` mode could skip the live-script runners); the tests cover both paths and both branch shapes.
- Files: `scripts/release.sh`, `scripts/release_test.go`.
- Interfaces: consumes the `full` input CS6 adds.
- Tests: `TestReleaseDispatchesAFullRunForTheMergeCommit`, `TestHotfixDispatchesAFullRun`, `TestHotfixOnAnOlderBranchPassesNoInput`.
- Command: `go test ./scripts/ -run 'TestRelease|TestHotfix' -count=1`
- Red step: the tests fail against the unchanged script.
- Depends on: CS7 (one writer of `scripts/release_test.go` at a time).
- Commit: `release.sh CS7b: a release's CI is a full run`
- Reviewer tier: opus with a mandatory independent review: it feeds the gate.
- Owner-run: no.

### CS6: `ci.yml` calls the selector
- Acceptance: the text, written into `docs/pending-workflows/README.md` as the section the owner copies (F9): a `select` job (checkout with `fetch-depth: 0`, `go run ./scripts/ci-select`), the `shard`, `terraform` and `rules` jobs chosen from its output, a `full` dispatch input, `run-name` branching on `github.event_name == 'schedule'` (R11), a nightly `schedule:` that runs the full variant and uploads the `test-times-*` and the traced reads as artifacts (`contents: read`, no push, R10), `live_scripts` removed (R9), and `actions: read` at job level for the `select` job and on the gate jobs of `release.yml`, `images.yml`, `mobile-release.yml` and `console-release.yml` (the `console-release.yml` edit is made in its staged copy `docs/pending-workflows/console-release.yml`, which is what the console's permission pin reads), whatever CS7's measurement says: the grant is harmless if the token did not need it and required if it did (F13).
- Files: `docs/pending-workflows/README.md`, `docs/pending-workflows/console-release.yml`.
- Interfaces: consumes CS4's output JSON and CS7's title strings.
- Tests: `go test ./internal/cli/ -run Docs -count=1`, a test that the pending text's matrix keys equal CS3's `Leg` field names, and in a clean worktree with the edit applied `go test ./internal/cli/ -run 'Docs|ReleaseGate|Release' -count=1`, `go test ./images/ ./deploy/terraform/ -count=1`.
- Command: the ones above.
- Red step: the images permissions guard fails if `actions: read` is placed at the workflow level of `ci.yml`.
- Depends on: CS4 (behaviour: it calls a selector that works), CS2b (the trace format it uploads), CS7 and CS7b (one sweep: the gate must accept what `ci.yml` names, and the hotfix path must dispatch it, before it is copied in).
- Commit: `docs: ci.yml selects its shards from what changed (written down for the owner)`
- Reviewer tier: opus with a mandatory independent review: a CI file.
- Owner-run: copying the workflows in; the owner says "copy it" and the edit lands as a pull request (as #3301).

### CS6b: the tests that pin what CS6 changes
- Acceptance: the tests that pin the workflows' permissions and the runner matrix are updated in the same sweep as CS6's copy: `docs_desktop_console_test.go` (console-release gate permissions, today exactly `contents: read`, `checks: read`), `docs_mobile_release_workflow_test.go` (every mobile-release job), and `docs_runner_timeout_test.go` (`TestRunnerTimeoutDocsMatchCI` reads the literal `pkg: ./internal/runner` line, which a matrix built from the selector's output no longer has); each keeps the pinned fact and changes the way it is read. A new `docs_ci_selector_test.go` pins that the keys CS6's text puts in a matrix leg equal CS3's `Leg` field names (the test CS3 cannot own, because the text it reads does not exist until CS6).
- Files: `internal/cli/docs_desktop_console_test.go`, `internal/cli/docs_mobile_release_workflow_test.go`, `internal/cli/docs_runner_timeout_test.go`, `internal/cli/docs_ci_selector_test.go` (new).
- Interfaces: consumes CS6's pending text.
- Tests: the three tests above and `TestCISelectorLegKeysAreTheMatrixKeys`, run in a clean worktree with CS6's edit applied.
- Command: `go test ./internal/cli/ -run 'ReleaseGate|ConsoleReleaseWorkflow|MobileRelease|RunnerTimeout|CISelector' -count=1`
- Red step: with CS6's edit applied and these files untouched, the permission and matrix pins fail.
- Depends on: CS6 (it reads the text it adapts the tests to).
- Commit: `docs tests: the permission and matrix pins read the selector's workflow`
- Reviewer tier: standard.
- Owner-run: no; it lands in the same pull request as the owner's copy, or just before it, as the owner chooses.

### CS8: the documents
- Acceptance: `CONTRIBUTING.md`'s CI section and `docs/release.md`'s "Before you tag" say that a selected run is the default, a full run is dispatched before a tag (`gh workflow run ci --ref main -f full=true`), a nightly full run exists, and how the committed read-map is refreshed from the nightly's artifact (R10); the exception to the 2026-11-07 ruling is recorded in this folder's README.
- Files: `CONTRIBUTING.md`, `docs/release.md`, `docs/ci-affected-tests/README.md`.
- Interfaces: documents CS6.
- Tests: `go test ./internal/cli/ -run Docs -count=1`
- Command: the same.
- Red step: the task adds a Docs pin of the sentence, which fails before the sentence exists.
- Depends on: CS6 and CS6b (documents what they do; CS7 is behind CS6).
- Commit: `docs: a selected run is the default and a full run precedes a tag`
- Reviewer tier: standard.
- Owner-run: no.

### CS9: Check 800, measure and break one on purpose
- Acceptance: a script (`scripts/live-checks/check800.sh`, with its `_test.sh` run by the live-check tests) and a procedure that dispatch one selected run on a trivial commit and one full run, report the wall time of each, then change a Docs pin's sentence on a branch and show the selected run fails it.
- Files: `scripts/live-checks/check800.sh` (new), `scripts/live-checks/check800_test.sh` (new), `scripts/live_checks_test.go` (lists the new script's test in `liveCheckScripts`), `docs/ci-affected-tests/check-800.md` (new), `docs/ci-affected-tests/README.md` (the `strace` measurement and the choice).
- Interfaces: consumes CS6 (the live workflow) and CS2b (the trace); it also reports whether `strace -f` is worth its cost on `ubuntu-latest`, which CS2b's flag then follows.
- Tests: none; the check is the test.
- Command: the owner runs the check's one command.
- Red step: the deliberately broken pin must fail the selected run.
- Depends on: CS6, CS2b.
- Commit: `docs: Check 800 measures the selected run and breaks it on purpose`
- Reviewer tier: standard.
- Owner-run: yes: the dispatches and the throwaway branch.

## 5. Parallel execution

| Level | Tasks | Why together |
| --- | --- | --- |
| 0 | CS1, CS2a, CS3, CS7 | none needs another |
| 1 | CS4, CS2b, CS5, CS7b | CS4 needs CS1, CS2a, CS3; CS2b and CS5 need CS2a; CS7b needs CS7 |
| 2 | CS6 | needs CS4, CS2b, CS7, CS7b |
| 3 | CS6b, CS9 | CS6b needs CS6; CS9 needs CS6 and CS2b |
| 4 | CS8 | needs CS6 and CS6b |

**Critical path (as `fugaro plan graph` reads it, 5 tasks): CS1, CS4, CS6, CS6b, CS8 (CS2a ties CS1).** Sequential because: the selector composes the real graph, map and packer, the workflow text needs a working selector and a gate that accepts its titles, and the tests that pin the workflow's permissions and matrix change with it, and the documents describe the workflow.

**Shared files.** No level holds two writers of one file. `scripts/release_test.go` is written by CS7 (level 0) and CS7b (level 1); `internal/cli/docs_desktop_console_test.go` by CS7 (level 0) and CS6b (level 3); `docs/ci-affected-tests/README.md` by this plan's pull request, CS9 (level 3, the measurement note) and CS8 (level 4); `docs/pending-workflows/README.md` by CS6 alone.

## 6. The dependencies the plan broke and kept

- **Broken:** none, and on purpose. The contract-first pass (read with `fugaro settings planning.decoupling`; the switch decides the pass, and the repository's mode was not asked for here) was weighed on CS4's three edges: a contract task C1 and an integration task I1 would have saved half a weight unit of path (before 4 tasks and weight 8, after 5 tasks and weight 7.5) and would have had CS1, CS2a and CS3 declare types that C1 also declares, so the second to land would not compile ("redeclared"), and I1 would only have unified them. Declined: each part owns its types and CS4 composes the real code.
- **Kept, with the reason a contract would not do:** CS4 on CS1, CS2a and CS3 (behaviour: the goldens run on the real parts); CS2b on CS2a (one file, one format, run after the scan exists); CS5 on CS2a (the test re-runs the scan); CS7b on CS7 (one test file); CS6 on CS4, CS2b, CS7 and CS7b (the workflow must call a working selector, upload the trace's format, and meet a gate that accepts its titles and a release path that dispatches it, in one sweep); CS6b, CS8 and CS9 on CS6.

## 7. Contract changes anticipated

None: the plan has no contract task. A field a later task finds missing from a part's types is that part's own change, raised in its own pull request.

## 8. Checks and after-deploy steps

- **Check 800** (reserved block 800-809): CS9's measurement, owner-run.
- **After deploy:** the owner copies the workflows in (CS6) and the first nightly writes the timings and the traced reads; a person or a Fugaro run refreshes the committed read-map from that artifact in a normal pull request (R10). A release's `### For operators` is unchanged: `fugaro update`.

**Considered and declined.** Selecting individual tests inside a package for ordinary packages (the update-test hang of F4 and the hash split make it a source of flakes); running the selected set on every push (the 2026-11-07 ruling); keeping #3301's `live_scripts` input beside the selector (R9); a contract and an integration task for CS4's edges (§6).
