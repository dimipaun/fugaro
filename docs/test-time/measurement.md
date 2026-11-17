# Where the verify step's test time goes

Measured 2026-11-17 on the worker a run gets: the `go` workflow's image, 4 vCPU and 8 GiB, Go 1.27.1, 6 cores visible. The three `test` invocations `fugaro.yaml` names were run, the `go test -json` streams were kept, and `scripts/test-time-report.sh` turned them into the tables below. **This is a measurement and a price list: it changes nothing, and the options at the end are the owner's to pick.** Verify is not the merge gate — GitHub CI is — but it is what a run's own "the tests pass on the final commit" is read from, which is why every option here is a decision and not a cleanup.

Reproduce with:

```sh
go test -json -timeout 15m -skip '^TestLiveCheckScripts$' $(go list ./... | grep -v -e /internal/runner -e /internal/e2e -e /internal/image -e /fugaro/images -e '/internal/cli$') > tree.jsonl
go test -json -timeout 15m -run '^TestU'      ./internal/cli/ > cliU.jsonl
go test -json -timeout 15m -run '^Test[^U]'   ./internal/cli/ > cliRest.jsonl
bash scripts/test-time-report.sh tree.jsonl cliU.jsonl cliRest.jsonl
```

`scripts/test-time-report_test.sh` (and `scripts/test_time_report_test.go`, which runs it) pins the tables against fixture streams and a four-file tree, so a change to the script is caught without running a Go test.

## The step as it stands

| invocation | what it runs | wall | of which compile and link | tests | test seconds |
| --- | --- | --- | --- | --- | --- |
| `go test <tree>` | the tree without `internal/cli`, live-check bash tests skipped | 707.5 s | 157.8 s | 9073 | 851.9 |
| `go test -run '^TestU' ./internal/cli/` | the update and upgrade half | 124.0 s | 41.4 s | 542 | 75.9 |
| `go test -run '^Test[^U]' ./internal/cli/` | everything else in the package | 563.3 s | 3.4 s | 6624 | 558.3 |

- "compile and link" is the wall before the package's first `start` event, so it is the test binary's build with the same build cache the step uses.
- The three run at once, so the step's wall is the longest of them: **707.5 s, 11.8 minutes**. The `internal/cli` walls are from a pass that ran them one at a time, because the concurrent pass is where the memory runs out (below) and its streams are the ones a killed compile truncates.
- The container peaked at **7663 MiB of its 8192 MiB** and the cgroup's OOM killer fired (`oom_kill 2`), killing two `compile` processes. In the concurrent pass both `internal/cli` invocations came back non-zero for that reason. `resources.memory: 8Gi` is what the step is sized by, and the step's own comment already says the disk is memory on Cloud Run.
- 6 cores are visible to the container and `resources.cpu: 4` is the job's cap; `go test` defaults to `-p` = the number of cores, so the tree invocation runs up to six packages at once.

## The two packages that are the step

| package | tests | test seconds | share of the step's test time |
| --- | --- | --- | --- |
| `github.com/dimipaun/fugaro/scripts` | 130 | 371.2 | 25% |
| `github.com/dimipaun/fugaro/internal/cli` | 7166 | 634.2 | 43% |
| every other package (136 of them) | 8943 | 480.7 | 32% |

The tree invocation's own critical path is one package: `scripts` at 374.1 s of its 707.5 s wall, and 371.2 s of that is tests — `release_test.go`'s fake gh and fake-clone cases (`TestIdenticalTreeNotAccepted` 32.4 s, `TestPreconditionFailures` 16.1 s, `TestPostMergeCheckStillPendingTimesOut` 13.2 s, `TestTimeoutMinutesEdgeCases` 12.6 s). Those are real work, not sleeps: none of them holds a wait primitive.

## Where `internal/cli`'s 634 seconds go

7166 tests over 606 test files, every one of them `package cli`, in 559.9 s of package time.

| size of test | tests | seconds | share |
| --- | --- | --- | --- |
| ≥ 1 s | 71 | 253.5 | 40.0% |
| 0.5–1 s | 110 | 70.9 | 11.2% |
| 0.1–0.5 s | 1040 | 196.9 | 31.0% |
| < 0.1 s | 5945 | 113.0 | 17.8% |

| slice | seconds | share |
| --- | --- | --- |
| the 30 slowest tests | 196.7 | 31.0% |
| the 100 slowest | 276.3 | 43.6% |
| the 53 tests whose body holds a wall-clock wait | 36.4 | 5.7% |

**It is not a few slow tests and not a pile of sleeps.** The thirty slowest are under a third, and the ~1150 tests between 0.1 s and 1 s are another 42%: the length is a broad middle of end-to-end tests that each build a rig, run a command through the whole command tree and assert on what landed.

The ten slowest, and what makes each long:

| seconds | test | why |
| --- | --- | --- |
| 46.17 | `TestWriterConformanceTheOwnerWritesTheKey` | runs every registry row that names a writer: each is a `fugaro` command in its own rig, and the table is the registry's size |
| 20.01 | `TestNoRealGCSFromTests` | the whole test is a 20 s context deadline: it waits for the GCS client to give up on a bucket that does not exist |
| 13.65 | `TestReleaseGateRequiresTheConsoleCheck` | two subtests (8.5 s, 5.1 s) reading the console's own checks |
| 11.19 | `TestNoFixedRunIDsInRecencyTests` | parses the AST of every file in a directory of test files |
| 9.11 | `TestUpdateRunsThePluginAndImages` | an update end to end against fake tools |
| 9.02 | `TestDaemonDoesNotDependOnGatedCommands` | walks the command tree |
| 7.08 | `TestLiveInitCensusMatchesTheFrozenList` | census over the frozen list |
| 6.00 | `TestEveryWizardQuestionHasAFlag` | one command per wizard question |
| 5.59 | `TestAccountsAddRefusesInAnAgentSession` | 5.5 s of it in one subtest, `CLAUDECODE` |
| 5.13 | `TestReleaseGateRequiresTheConsoleCheck/default_set` | the second half of the 13.65 s above |

The waits worth naming: `TestNoRealGCSFromTests` 20.0 s, `TestWaitPassesOverTheSnapshotAReconnectOpensWith` 4.0 s, `TestWatchChildDiesBySIGPIPEWhenItsReaderGoesAway` 2.2 s, `TestPtyFirstRunPrompts` 1.5 s. In the tree, `TestSignalsLeaveMain` 8.1 s is the largest.

## Is the build cache warm at run start?

The image bakes `go mod download && go build ./...` (`images/catalog/go/Dockerfile`, the workflow's own setup verb; there is no `image.setup` in `fugaro.yaml` any more, and the base image sets no `GOCACHE`). So at run start the cache is warm for **everything a build needs and nothing a test needs**:

| | packages |
| --- | --- |
| `go build ./...` bakes | 972 |
| the verify step's `go test` needs | 1313 |
| the bake never compiled | 342 (this repository's own test variants, `testing`, `golang.org/x/text`, `jsonschema`) |

That gap is what the step pays 157.8 s (tree) and 41.4 s (cli) for. Warm, the same link is **4.8 s** — `go test -c ./internal/cli` produces a 137 MB binary in under five seconds once its objects are cached. Cold it does not finish at any parallelism in this container: `compile` is killed after 95 s at `-p=6`, 102 s at `-p=2` and 183 s at `-p=1`, with one `compile` process peaking at 2872 MiB and 820 MiB of cache written into the container's memory-backed disk by then.

## The options, priced

Time saved is per verify run, on the measured 707.5 s. "Coverage given up" is per PR, against the 7166 cli tests and 9073 tree tests.

| option | time saved | coverage given up | a failure it would not catch |
| --- | --- | --- | --- |
| (a) inject the clock into the tests that wait | ~12 s today: that is the tree's waits inside `scripts` (12.2 s), the step's critical path. The cli's 36 s sit in an invocation that is not the critical path and start to count only once the tree is under 563 s | none | a product deadline that regressed, if the test now injects a clock that never fires — keep one test per real deadline |
| (b) run only the affected packages (`go list -deps`) | ~450 s: the median PR's critical path is 66 s against today's 374 s (`scripts`), and 99 of the last 100 merged PRs would run no package over 300 s | every package outside the changed packages' closure, 58% of the tree's test time at the median | a cross-PR interaction: package A changes what package B's tests assert, and the PR touches only A. The three red mains of 2026-11-16 were that shape |
| (c) run only the tests of the files a change touches, plus the Docs filter, writer conformance and census | ~500 s of cli's 563 s: the median PR would run 709 of 7167 tests (9.9%) and 65 s of 634 s | everything else in `internal/cli` | the same shape inside one package: `internal/cli`'s tests are end-to-end over the whole command tree, so a change to `internal/config` that one command's test asserts is not caught by that command's file |
| (d) bake a warm *test* build cache into the image | most of 157.8 s (tree) + 41.4 s (cli), less what the PR's own packages must recompile | none | none: the same tests run. It is the one option that removes work instead of coverage |
| (e) split `internal/cli` into packages | none by itself. It is what would let (b) and (c) work at package granularity and let `-timeout` and `-p` apply per package | none | the cuts are the cycles that hold the package together, so a split can leave behaviour asserted only through a narrower surface |
| (f) a larger container | none by itself; it removes the failure that ends runs rather than shortening it | none | none |

**On (b)'s saving.** A Go-import rule alone would skip `internal/cli` for **21 of the last 100 merged PRs** (`git log` on `main`, each PR's changed files against the package's test-import closure). Adding the paths the cli tests read — `docs/`, `website/`, `schemas/`, `console/`, `plugin/`, `scripts/`, `images/`, `deploy/`, `mobile/`, `shared/`, `ee/`, `internal/`, `.github/`, `go.mod`, `go.sum`, `fugaro.yaml`, `README.md` — skips it for **0 of 100**. The 21% is real saving and it is exactly the class of change the docs, census and writer-conformance tests exist for.

**On (c)'s saving.** The always-run set is 689 tests and 64.2 s of the 634 s on its own, 46.2 s of that in `TestWriterConformanceTheOwnerWritesTheKey` alone. That test's cost is the registry's size, so (c) is also where that test would become affordable to keep honest — today it is 7% of the package for one table.

**On (e)'s size.** 287 non-test files and 613 test files, all `package cli`. Grouping files by the identifier prefix they share gives 101 areas and 730 edges between them, and those edges form **one strongly connected component of 86 areas** — there is no seam that does not cut a cycle. 209 helpers are called from three or more files; `userErr` alone is reached from 181 files across 89 areas; `root.go`'s `AddCommand` list names about 46 constructors. Priced from the imports only; no work was done.

**On (f).** `resources.memory: 8Gi` and a 7663 MiB peak is not headroom, it is a coin toss that came up heads on a warm cache; with a colder one the killed compile fails the package and the step. On Cloud Run the writable layer is memory, so the Go build cache written during a run is charged to the same 8 GiB the compilers use — a bigger limit buys cache room as well as compiler room.

## Recommended order

1. **(f) more memory.** One line in `fugaro.yaml`, no coverage lost, and it is the only option that addresses the failure that *ends* runs.
2. **(d) bake the test build.** The largest saving that costs no coverage, and it is invisible to every rule that reads "the tests pass".
3. **(b) with (c) inside `internal/cli`.** One idea at two granularities, and the two that change what a run's own "tests pass on the final commit" means — see below. It is also the pair with no pre-merge net: CI runs on pushes to `main` only, so a PR judged by a subset is checked against the whole tree *after* it lands, which is how the red mains of 2026-11-16 were found.
4. **(a) the waits.** ~12 s, no coverage, a dozen tests.
5. **(e) the split.** Only if (b) and (c) prove too coarse; the largest change of the six and the least saving of its own.

## What this changes about the landing rule

The landing rule refuses to land a pull request whose final commit has failing tests, and the tests it means are the ones the run's own verify step ran. Every option above except (d), (e) and (f) changes *which* tests that is — so a green verify on a final commit would stop meaning "this tree's tests pass" and start meaning "the tests this PR was judged by pass". That is a different sentence, and it is the owner's to say, not this measurement's.
