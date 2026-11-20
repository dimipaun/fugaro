# CI selects tests from what changed: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's explicit go by name.** It builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it: the release gate requires a full run on the tag's commit (D3040); one scheduled nightly full run (D3041); the selector is a Go program with tests under `scripts/` (D3042); a recorded read-map, refreshed by the full run, says which tests a file change reaches beyond imports (D3043). Resolutions R1-R9 of the reconciliation fix the rest.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/` (none of its path rules loads for `internal/ciselect` or `scripts/`), `docs/release.md`.

- **Always.** Targeted tests, never a whole package. Hermetic: no network, no home directory, `t.TempDir()`, a fake `git` graph and fixture `go list -json` output, never a call to GitHub. A new doc is covered by `go test ./internal/cli/ -run Docs -count=1`.
- **Ask first.** A new dependency (the selector uses the standard library and `golang.org/x/tools` only if already in `go.mod`); a change to the contract's files (raise a contract change, §7); a trigger other than `workflow_dispatch` and the one `schedule:`.
- **Never.** An edit under `.github/workflows/` from an agent run (CS6 is written down and copied by the owner's word, as #3301 was); a selector that fails open (R8); a secret-shaped literal; a confirmation step; a live GitHub call from a test.

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The selector never selects fewer tests than a full run would have failed on | I1's golden cases: `internal/catalog/fetch.go` selects `./plugin` (F2); a `.github/workflows/ci.yml` edit selects everything; an unmapped path selects everything |
| Doubt runs everything (R8) | CS4's table test, one row per doubt in R8 |
| A selected run is never the base of the next (R2) | CS4: the base is the last green `ci (full)` run's head |
| The gate accepts only a full run's `test` (D3040, R1) | CS7's gate test, one case per title |
| The committed read-map is not stale | CS5 |
| `terraform` and `rules` keep their gate names green on every full run | I1: a full run's matrix includes both |

## 3. Slices

1. **The selector core (C1, CS1, CS2a, CS3, CS4, I1):** a program that, given a base and a head, prints the legs to run. Inert until CS6.
2. **The proof it is right (CS2b, CS5):** the runtime trace and the test that fails when the map is stale.
3. **The gate and the workflow (CS7, CS6, CS8):** the release gate reads the run's title; `ci.yml` calls the selector, takes a `full` input and a nightly schedule.
4. **The measurement (CS9):** one owner-run check.

## 4. Tasks

### C1: the selector's contract
- Acceptance: `internal/ciselect/types.go` declares `Input` (base, head, changed files, the read-map, the timings), `Output` (`Full bool`, `Reason string`, `Legs []Leg`), `Leg` (`Name`, `Pkg`, `Index`, `Of`, `Only`, the same fields as today's matrix entries), the read-map format (per package: `reads` globs, `any bool`, `ignores` globs) and the run-title convention (`ci (full)` / `ci (selected)`); `select_stub.go` returns a fixed plausible `Output`; `docs/ci-affected-tests/contract-select.md` says what each side may rely on.
- Files: `internal/ciselect/types.go` (new), `internal/ciselect/select_stub.go` (new), `docs/ci-affected-tests/contract-select.md` (new).
- Interfaces: produces the types CS1, CS2a, CS3, CS4 and CS7 call.
- Tests: `TestLegShapeMatchesTodaysMatrix` (the field names equal `ci.yml`'s `matrix.include` keys), `TestReadMapRoundTrips`.
- Command: `go test ./internal/ciselect/ -run 'TestLegShape|TestReadMap' -count=1`
- Red step: write the first test before `types.go` exists; it fails to compile.
- Depends on: none.
- Commit: `ciselect C1: the selector's types, its stub and the contract`
- Reviewer tier: standard (a contract over a CI file's data shape, no credentials).
- Owner-run: no.

### CS1: the reverse import graph
- Acceptance: `Affected(graph, changedFiles)` returns every package that imports a changed package directly or transitively, tests' imports included (`-test`), from fixture `go list -deps -test -json` output.
- Files: `internal/ciselect/graph.go` (new), `internal/ciselect/graph_test.go` (new).
- Interfaces: declares its own `Graph` and `Affected` in `graph.go` to the signature `contract-select.md` states (I1 unifies the types); produces the package set CS4 unions with the read-map's.
- Tests: `TestAffectedFollowsImportsTransitively`, `TestAffectedIncludesTestOnlyImports`, `TestAffectedOfANonGoFileIsEmpty`.
- Command: `go test ./internal/ciselect/ -run 'TestAffected' -count=1`
- Red step: `TestAffectedFollowsImportsTransitively` fails against an empty `Affected`.
- Depends on: none (it declares its own types to the contract's documented signature; I1 re-joins).
- Commit: `ciselect CS1: the reverse import graph`
- Reviewer tier: standard.
- Owner-run: no.

### CS2a: the read-map, the static scan
- Acceptance: `scripts/ci-readmap` reads every `_test.go` file in the repository and writes `docs/ci-affected-tests/read-map.json`: per package the repository files its tests name by literal (`../../docs/x.md`, `readRepoFile("a/b.go")`, `repoRoot()` joins), globs where the literal is a directory, `any` where a path is built dynamically or a test walks the tree (the three walkers of F3 come out `any`), and for a walker the `ignores` it states; the file is deterministic (sorted).
- Files: `internal/ciselect/scan.go` (new), `internal/ciselect/scan_test.go` (new), `scripts/ci-readmap/main.go` (new), `docs/ci-affected-tests/read-map.json` (new).
- Interfaces: writes the read-map format `contract-select.md` documents, declaring its own types in `scan.go` (I1 unifies); produces the committed map CS4, CS5 and CS2b use.
- Tests: `TestScanFindsLiteralRepoPaths`, `TestScanMarksADynamicPathAny`, `TestScanMarksTheKnownWalkersAny`, `TestScanIsDeterministic`.
- Command: `go test ./internal/ciselect/ -run 'TestScan' -count=1`
- Red step: `TestScanMarksTheKnownWalkersAny` fails with an empty scan.
- Depends on: none (it declares its own types to the contract's documented format).
- Commit: `ciselect CS2a: the read-map, written by a scan of the tests`
- Reviewer tier: standard.
- Owner-run: no.

### CS2b: the read-map, the runtime trace
- Acceptance: the full run records, per shard, the repository files the test process opened (a measured mechanism: `strace -f` where installable, otherwise a Go-level hook; the task decides by measurement and writes the choice in `contract-select.md`'s appendix through a contract change request if the format needs a field), reduces them to repository-relative paths and merges them into the map as `reads`; a path the trace found and the scan did not is printed as a scan gap.
- Files: `internal/ciselect/trace.go` (new), `internal/ciselect/trace_test.go` (new), `scripts/ci-readmap/trace.go` (new).
- Interfaces: consumes CS2a's map; produces the map with traced `reads`.
- Tests: `TestTraceReducesOpensToRepoPaths`, `TestTraceReportsAScanGap`, `TestTraceIgnoresTheGoBuildCache`.
- Command: `go test ./internal/ciselect/ -run 'TestTrace' -count=1`
- Red step: `TestTraceReportsAScanGap` fails with a trace that finds nothing extra.
- Depends on: CS2a (the scan's map it refines).
- Commit: `ciselect CS2b: the runtime trace refines the read-map`
- Reviewer tier: standard.
- Owner-run: no.

### CS3: timings and the packer
- Acceptance: `Pack(chosenPackages, timings, maxLegs)` returns legs by longest-first greedy, keeps the `internal/cli` update and other families whole as `Only: update|other` legs (F4), reads timings from `test-times` TSV text, and with none returns equal weights.
- Files: `internal/ciselect/pack.go` (new), `internal/ciselect/pack_test.go` (new).
- Interfaces: declares its own `Leg` in `pack.go` to the shape `contract-select.md` documents (I1 unifies); produces the legs CS4 prints.
- Tests: `TestPackIsLongestFirst`, `TestPackKeepsTheCliFamiliesWhole`, `TestPackWithoutTimingsIsEqualWeight`.
- Command: `go test ./internal/ciselect/ -run 'TestPack' -count=1`
- Red step: `TestPackIsLongestFirst` fails against an unsorted pack.
- Depends on: none (it declares its own `Leg` to the documented shape).
- Commit: `ciselect CS3: timings pack the chosen packages into legs`
- Reviewer tier: standard.
- Owner-run: no.

### CS4: the selector program
- Acceptance: `scripts/ci-select` takes `--base`, `--head`, the changed files (from `--files-from` JSON, the compare API's output), the read-map, `--timings` and `--full`, and prints `Output` as JSON on stdout plus a summary on `$GITHUB_STEP_SUMMARY`; it returns `Full: true` with a reason for every doubt in R8; it selects `terraform` and `rules` per R6 and always `check`; a docs-only change selects the Docs tests and `website` per R7.
- Files: `scripts/ci-select/main.go` (new), `internal/ciselect/select.go` (new), `internal/ciselect/select_test.go` (new).
- Interfaces: consumes C1's types and, behind the stub, CS1's graph, CS2a's map and CS3's packer; produces the matrix CS6 feeds `fromJSON`.
- Tests: `TestSelectIsFullForEveryDoubtOfR8` (one row each), `TestSelectDocsOnlyRunsDocsAndWebsite`, `TestSelectBaseIsTheLastFullRun`, `TestSelectTerraformAndRulesPerR6`.
- Command: `go test ./internal/ciselect/ -run 'TestSelect' -count=1`
- Red step: `TestSelectIsFullForEveryDoubtOfR8` fails against the stub's fixed output.
- Depends on: C1 (the shapes it reads and prints).
- Commit: `ciselect CS4: the selector program`
- Reviewer tier: opus with a mandatory independent review: it decides what a CI run proves.
- Owner-run: no.

### CS5: the map cannot rot
- Acceptance: a test fails when a repository path named by a test literal is missing from the committed map, when a map entry names a file that no longer exists, and when a Docs pin's file (`internal/cli/docs_*_test.go`) is not reached by the Docs entry.
- Files: `internal/ciselect/readmap_test.go` (new).
- Interfaces: consumes CS2a's scan and the committed `read-map.json`.
- Tests: `TestReadMapNamesEveryLiteralPath`, `TestReadMapNamesNoMissingFile`, `TestReadMapReachesEveryDocsPin`.
- Command: `go test ./internal/ciselect/ -run 'TestReadMap' -count=1`
- Red step: delete one entry from a copy of the map; the first test fails.
- Depends on: CS2a (the scan it re-runs).
- Commit: `ciselect CS5: the read-map is pinned against the tests it describes`
- Reviewer tier: standard.
- Owner-run: no.

### CS7: the release gate reads that the run was full
- Acceptance: `scripts/release-gate.sh` accepts the `test` check only when its workflow run's `display_title` is `ci (full)` or exactly `ci` (the title before CS6 names runs); a run titled `ci (selected)` is refused with the reason; `terraform` and `rules` keep their rule; the identical-tree fallback and the hotfix path keep theirs.
- Files: `scripts/release-gate.sh`, `scripts/release_gate_test.go`.
- Interfaces: the title convention is R1's, a string, not a code interface; produces the gate CS6 needs before it is copied in.
- Tests: `TestGateAcceptsAFullRunsTest`, `TestGateRefusesASelectedRunsTest`, `TestGateAcceptsTheOldTitle`.
- Command: `go test ./scripts/ -run 'TestGate' -count=1`
- Red step: `TestGateRefusesASelectedRunsTest` fails against the unchanged gate.
- Depends on: none (the title convention is a decision, R1).
- Commit: `release-gate CS7: the test check must come from a full run`
- Reviewer tier: opus with a mandatory independent review: a CI gate.
- Owner-run: no.

### I1: the real graph, map and packer behind the selector
- Acceptance: `select_stub.go` is deleted, `Select` calls CS1, CS2a's map and CS3, and an end-to-end test runs the selector on fixture changed-file lists: `internal/catalog/fetch.go` selects `./plugin` and its importers; `docs/status.md` selects the Docs tests only; `.github/workflows/ci.yml` selects everything; an unknown path selects everything.
- Files: `internal/ciselect/select.go`, `internal/ciselect/select_stub.go` (deleted), `internal/ciselect/integration_test.go` (new).
- Interfaces: wires CS1, CS2a, CS3 and CS4.
- Tests: `TestIntegrationCatalogFetchReachesPlugin`, `TestIntegrationDocsOnly`, `TestIntegrationWorkflowChangeIsFull`, `TestIntegrationUnknownPathIsFull`.
- Command: `go test ./internal/ciselect/ -run 'TestIntegration' -count=1`
- Red step: each fails while the stub is in place.
- Depends on: CS1, CS2a, CS3, CS4 (behaviour: the real code behind the stub).
- Commit: `ciselect I1: the selector runs on the real graph, map and packer`
- Reviewer tier: opus with a mandatory independent review.
- Owner-run: no.

### CS6: `ci.yml` calls the selector
- Acceptance: the plan's text, written into `docs/pending-workflows/README.md` as the "`ci.yml` section" the owner copies (a staged copy trips the guards, F9): a `select` job runs `go run ./scripts/ci-select`, the `shard` job's matrix is `fromJSON(needs.select.outputs.matrix)`, a `full` dispatch input, `run-name: ci (full)|ci (selected)`, a nightly `schedule:` that dispatches the full variant and uploads `test-times-*` and the traced read-map, `terraform` and `rules` run per the selector's output, `live_scripts` is removed (R9).
- Files: `docs/pending-workflows/README.md`.
- Interfaces: consumes CS4's matrix JSON and CS7's title convention.
- Tests: `go test ./internal/cli/ -run Docs -count=1` and `go test ./images/ -run 'CI|Workflow' -count=1` in a clean worktree with the edit applied.
- Command: the two above.
- Red step: the images permissions guard fails if `actions: read` is placed on the workflow level.
- Depends on: I1 (behaviour: a selector that works end to end), CS7 (one sweep: the gate must accept what `ci.yml` names before it is copied in).
- Commit: `docs: ci.yml selects its shards from what changed (written down for the owner)`
- Reviewer tier: opus with a mandatory independent review: a CI file.
- Owner-run: copying the workflow in; the owner says "copy it" and the edit lands as a pull request (as #3301).

### CS8: the documents
- Acceptance: `CONTRIBUTING.md`'s CI section and `docs/release.md`'s "Before you tag" say that a selected run is the default, a full run is dispatched before a tag (`gh workflow run ci --ref main -f full=true`), and a nightly full run exists; the exception to the 2026-11-07 ruling is recorded in this folder's README.
- Files: `CONTRIBUTING.md`, `docs/release.md`, `docs/ci-affected-tests/README.md`.
- Interfaces: consumes CS6 and CS7.
- Tests: `go test ./internal/cli/ -run Docs -count=1`
- Command: the same.
- Red step: a Docs test that pins the sentence fails before the sentence exists, or the task adds the pin.
- Depends on: CS6, CS7 (documents what they do).
- Commit: `docs: a selected run is the default and a full run precedes a tag`
- Reviewer tier: standard.
- Owner-run: no.

### CS9: Check 780, measure and break one on purpose
- Acceptance: a script and a procedure that dispatch one selected run on a trivial commit and one full run, report the wall time of each, then change a Docs pin's sentence on a branch and show the selected run fails it.
- Files: `docs/ci-affected-tests/check-780.md`.
- Interfaces: consumes CS6 (live workflow) and CS2b (the trace).
- Tests: none; the check is the test.
- Command: the owner runs the check's one command.
- Red step: the deliberately broken pin must fail the selected run.
- Depends on: CS6, CS2b.
- Commit: `docs: Check 780 measures the selected run and breaks it on purpose`
- Reviewer tier: standard.
- Owner-run: yes: the dispatches and the throwaway branch.

## 5. Parallel execution

| Level | Tasks | Why together |
| --- | --- | --- |
| 0 | C1, CS1, CS2a, CS3, CS7 | the contract and the four parts that need nothing from each other |
| 1 | CS4, CS2b, CS5 | CS4 needs C1's types; CS2b and CS5 need CS2a's map |
| 2 | I1 | needs CS1, CS2a, CS3 and CS4 |
| 3 | CS6 | needs I1 and CS7 |
| 4 | CS8, CS9 | CS8 needs CS6, CS7; CS9 needs CS6, CS2b |

**Critical path (as `fugaro plan graph` reads it, 5 tasks): C1, CS4, I1, CS6, CS8.** Sequential because: the selector compiles against the contract, the integration needs the real parts, the workflow copy needs the integration and the gate, and the documents describe the copy. The scan (CS2a) is the longest single task and runs beside C1 and CS4.

**Shared files.** No level holds two writers of one file. `internal/ciselect/select.go` is written by CS4 (level 1) and I1 (level 2): sequential by the I1 edge. `docs/ci-affected-tests/README.md` is written by this plan's PR and by CS8 (level 4).

## 6. The dependencies the plan broke and kept

- **Broken by C1:** CS4 needed CS1's graph, CS2a's map and CS3's packer to run; it needs only their signatures and formats, so the three parts and the program run side by side and I1 re-joins them. The read of the graph: 3 edges broken, 7 kept, 4 tasks added; before 4 tasks (weight 8), after 5 tasks (weight 7.5); it improves.
- **A contract's consumer is the only task that waits on it:** CS1, CS2a and CS3 build to the signatures `contract-select.md` documents and wait for nothing; the price is that I1 unifies three declarations of one type.
- **Kept:** CS2b on CS2a (the trace refines the scan's own file, one format, one writer after the other), CS5 on CS2a (the test re-runs the scan), I1 on the four parts (behaviour), CS6 on I1 and CS7 (the workflow must call a working selector and a gate that accepts its titles in one sweep), CS8 and CS9 on CS6.

## 7. Contract changes anticipated

- **R-leg-shape**, raised by CS6 if `ci.yml`'s matrix needs a field the leg shape lacks; it reaches CS3 and CS4.
- **R-map-format**, raised by CS2b if a traced read needs a field the map lacks; it reaches CS2a, CS4 and CS5.

## 8. Checks and after-deploy steps

- **Check 780** (reserved block 780-789): CS9's measurement, owner-run.
- **After deploy:** none for a user; the owner copies `ci.yml` in (CS6) and the first nightly writes the timings and the traced map. A release's `### For operators` is unchanged: `fugaro update`.

**Considered and declined.** Selecting individual tests inside a package for ordinary packages (the update-test hang of F4 and the hash split make it a source of flakes); running the selected set on every push (the 2026-11-07 ruling); keeping #3301's `live_scripts` input beside the selector (R9).
