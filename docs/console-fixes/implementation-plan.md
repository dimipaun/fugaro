# Console fixes: implementation plan

**Status: plan only. Nothing is implemented, and no task is approved.** Amended after the three review rounds (2026-11-21). Each task needs the owner's explicit go before a run starts. The plan builds `spec.md` as amended by `reconciliation.md`, which wins where the two differ. It rests on the owner's answers D3200–D3214 and the resolutions D3215–D3224 (2026-11-21). D3224 and the amendments to D3215, D3217, D3218 and D3220 wait on the owner's confirmation (O4), and open tension T1 waits on the owner (O6). The plan graph is `implementation-plan.graph.json`, beside this file.

Four answers shape the plan most:

- D3201: the history job is the index's only writer.
- D3202: readers add an RTDB overlay of the live set and the outcomes since the index's last fold.
- D3206/D3207: many named orchestrators per project, with no cap. Not the recommendation; followed through.
- D3213: the console drops every non-secret mask.

## Conventions every part follows

- `CLAUDE.md` and the rules files the paths load:
  - `.claude/rules/console.md` for `console/**`;
  - `.claude/rules/terraform.md` for `deploy/terraform/**`;
  - `.claude/rules/ee.md` for `ee/**`.

  Not restated here.
- **Hermetic tests.** Use `internal/gcpfake` (it has `firestore.go`, `rtdb.go`, `gcs.go`, `scheduler.go` and `run.go`), `internal/testutil` and `t.TempDir()`. No network, no live cloud from an agent. Live timings are the owner's Checks 870 and 871.
- **No secret-shaped literal** in any test, fixture or doc. That matters for the identity-display tests that prove secrets are still masked: build the token shapes at runtime, the way `sanitize.test.ts` already does.
- **A field never changes meaning.** Every `--json` change here is additive: `index_complete`, `folded_through`, `merges_read` (present only under `--no-merges`), `name` and `driver` in `orchestrator ls`, and the new `orchestrator map` document. The orchestrator state and lease, which are decoded strictly, gain no field at all (F35). A new console gates `--no-merges` and `orchestrator map` behind a CLI version floor (`console/src/shared/version.ts`). `runs ls`'s default changes what rows the text listing prints (D3203); it does not change what any field means.
- **`fugaro update` is the whole upgrade.** It does two things here: applies the composite indexes (RI2) and starts the first reindex (RI5). It writes nothing to an orchestrator node, and CG2 proves that a previous release reads every node this one writes. Each of those PRs carries its `### For operators` line ("run `fugaro update`" plus what it prints) and a test that an installation at the previous version converges with a run in flight (`internal/cli/update_in_flight_test.go`, `TestUpdateLeavesARunInFlightRunning`).
- **The console's allow-lists** (`console/src/main/spawn.ts`, `CONSOLE_OPERATIONS` in `console/src/main/ipc.ts`) change in CG7a, IR4 and I-ORCH. Each needs an independent review, its pinning tests in `console/test/unit/` and the §5.1 table in `docs/desktop-console/spec.md`.
- **`ee/` tasks** (IR3, CG5, CG6, I-ORCH) also run `go test ./internal/eeboundary/` (`.claude/rules/ee.md`).
- **Docs are tested.** After a change to `docs/desktop-console/spec.md`, `console/docs/json-contract.md`, the CLI reference or a plugin skill, run `go test ./internal/cli/ -run Docs` (and `go test ./plugin/...` for a skill).
- **Boundaries.**

  | Tier | What |
  | --- | --- |
  | Always | Tests before the commit; targeted test commands; `gofmt -l .` prints nothing; the console's `npm run typecheck` |
  | Ask first | A new dependency (none is planned: elkjs is already there); a new IAM grant (none is planned, D3201); any change to the RTDB rules (none is planned: the claim and the heartbeat live under subtrees whose writers come over IAM) |
  | Never | Grant `datastore.user` to a job or a launcher; let the console read Firestore (D3216); edit `.github/workflows/`; re-record screenshot goldens (owner step) |

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A page of `runs ls` and `plan status`'s first read cost a constant number of requests at 1x and 100x history | I-INDEX (`TestIndexedReadsConstantRequests`) |
| No index write ever touches a run object; a reindex during a sweep cannot double-count (writer lease) | RI4, RI5 |
| A reader never prints a silently short list while the index is incomplete | IR1, IR2 (`index_complete`), I-INDEX |
| A run that finished after the last fold is listed (overlay) | RI3 (`TestOverlayAddsOutcomesSinceFold`) |
| The default orchestrator keeps its key; an update with a run in flight leaves it running | CG2 |
| The overlay never supplies a pull request, a title or an identity | RI3 (`TestOverlayNeverSuppliesPRorIdentity`) |
| The rollover never prunes outcomes the fold has not read | RI4 (`TestRolloverKeepsDaysAfterFold`) |
| A cross-orchestrator cycle never deadlocks silently | CG3 (`TestCrossCycleBlocksWithEscalation`) |
| Two orchestrators never both launch one plan task | CG4 (the claim against `gcpfake`'s RTDB) |
| A previous release reads every orchestrator node the new release writes | CG2 (`TestPreviousReleaseReadsNewNodes`) |
| `plan status`'s default keeps the meaning of `closed` and `mark` | IR2 (`TestPlanStatusDefaultStillReadsMerges`) |
| Every secret shape still masked after the identity masks go | ID1 (the existing credential tests unchanged, plus one per removed rule proving a secret inside that shape is still caught) |

## Slices

1. **run-index**: inert until read. It holds C-INDEX, RI1–RI5, and the Terraform indexes.
2. **indexed-reads**: useful alone once I-INDEX lands. IR1–IR4, IR5a, IR5b, IR6, IR7, I-INDEX, T-CHK (RI6, doctor's line, is in run-index).
3. **coordinator-graph**: named orchestrators are useful alone (CG1a–CG1c, CG2–CG4, CG8); the tab arrives with CG5, CG6, CG7a, CG7b and I-ORCH.
4. **identity-display**: useful alone. ID1–ID6.

## Tasks

### run-index

#### C-INDEX: the index contract
- Acceptance: `internal/runindex` declares `Entry`, `Summary`, `Filter`, `Cursor`, `Page` and the `Reader` interface (`List`, `Summary`). `docs/console-fixes/contract-index.md` fixes:
  - the collections (`runIndex`, one document per run id; `runIndexMeta/summary`);
  - each field and its Firestore type, including the per-run cost and compute figures `runs stats` and the estimate table read; the sort key (`updated_at` desc, then run id); and the composite indexes;
  - the overlay rule (D3202) and the `index_complete`/`folded_through` JSON fields;
  - `plan status --no-merges` and its `merges_read: false` (D3217 as amended);
  - the lease document's reindex cursor (F39).

  The stub `Reader` returns a fixed page. The `runs ls --json` and `plan status --json` additions are fixed here and copied into `console/docs/json-contract.md` by I-INDEX.
- Files: `internal/runindex/contract.go` (new), `internal/runindex/stub.go` (new), `internal/runindex/contract_test.go` (new), `docs/console-fixes/contract-index.md` (new).
- Interfaces: produces the types and the reader every run-index and indexed-reads task builds on.
- Tests: `TestContractShapes` (JSON round trip of `Entry` and `Summary`, cursor encode/decode).
- Command: `go test ./internal/runindex/ -run TestContract -count=1`
- Red step: `TestContractShapes` fails before the types exist.
- Depends on: none.
- Commit: `runindex: the index contract (C-INDEX, D3201-D3203)`
- Reviewer tier: standard; launched at the rigor of its strictest consumer.

#### RI1: ordered, paged Firestore queries
- Acceptance: `internal/firestore` runs a structured query with equality filters, `orderBy` on two fields, `limit` and a `startAfter` cursor. `gcpfake`'s Firestore honours the same query.
- Files: `internal/firestore/query.go` (new), `internal/firestore/query_test.go` (new), `internal/gcpfake/firestore.go`, `internal/gcpfake/firestore_test.go`.
- Interfaces: produces `Client.RunQuery`.
- Tests: `TestRunQueryOrdersAndPages`, `TestFakeRunQueryMatchesOrder`.
- Command: `go test ./internal/firestore/ ./internal/gcpfake/ -run 'RunQuery' -count=1`
- Red step: `TestRunQueryOrdersAndPages` fails: no such method.
- Depends on: none.
- Commit: `firestore: ordered paged queries (RI1, F10)`
- Reviewer tier: standard.

#### RI2: the composite indexes
- Acceptance: the Firebase module declares the `google_firestore_index` resources that contract-index.md lists; `fugaro update`'s apply creates them. The `terraform`-tagged test plans them.
- Files: `deploy/terraform/gcp/modules/firebase/firestore_indexes.tf` (new), `internal/infra/firebase_indexes_test.go` (new).
- Interfaces: consumes C-INDEX's index list.
- Tests: `TestFirebaseModulePlansRunIndexes` (tag `terraform`).
- Command: `terraform fmt -check -recursive deploy/terraform && go test -tags terraform ./internal/infra/ -run TestFirebaseModulePlansRunIndexes -count=1`
- Red step: the plan has no index resource.
- Depends on: C-INDEX (the index list).
- Commit: `terraform: composite indexes for the run index (RI2)`
- Reviewer tier: opus (Terraform).

#### RI3: the Firestore reader with the RTDB overlay
- Acceptance: `runindex.NewFirestore` implements `Reader`. A page is one Firestore query plus, in parallel, the RTDB live set (`agents/`) and `outcomes/` since `folded_through`, merged by run id (D3202). The overlay supplies status and liveness only, keyed by the slug and run id of the RTDB path. It never supplies a pull request, a title or an identity: a run's own `agents/` node is written by the run's token (F9).
- Files: `internal/runindex/firestore.go` (new), `internal/runindex/overlay.go` (new), `internal/runindex/firestore_test.go` (new), `internal/runindex/overlay_test.go` (new).
- Interfaces: consumes C-INDEX and RI1; produces the real `Reader`.
- Tests: `TestFirestoreReaderPages`, `TestOverlayAddsOutcomesSinceFold`, `TestOverlayStatusOnConflict`, `TestOverlayNeverSuppliesPRorIdentity`.
- Command: `go test ./internal/runindex/ -run 'Firestore|Overlay' -count=1`
- Red step: `TestOverlayAddsOutcomesSinceFold` fails with only the indexed rows.
- Depends on: RI1 (behaviour: it calls `RunQuery`). As the provider the contract describes, it implements C-INDEX's types; it is launched once C-INDEX has merged.
- Commit: `runindex: the Firestore reader and the overlay (RI3, D3202)`
- Reviewer tier: standard.

#### RI4: the sweep folds runs into the index
- Acceptance:
  - Each history sweep takes the index writer lease in the RTDB.
  - It folds the runs that changed since `folded_through` into `runIndex` documents. It finds them through `outcomes/` and `agents/`, then reads each changed run's objects, and every field comes from those records.
  - It applies state changes as deltas to `runIndexMeta/summary`, with its counts and estimate table (F13, F15), and advances `folded_through`. A failed fold leaves the stamp where it was.
  - The rollover, a separate daily execution, reads `folded_through` and never prunes a day after it. A stamp older than `PruneAfter` sets `complete: false`, and the next sweep restarts the walk (RI5). RI6's `doctor` line names it. The job starts no execution: its role cannot (F9).
  - No run object is written.
- Files: `internal/runindex/fold.go` (new), `internal/runindex/lease.go` (new), `internal/runindex/fold_test.go` (new), `internal/cli/history.go`, `internal/cli/history_fold_test.go` (new), `internal/budget/rollover.go`.
- Interfaces: consumes C-INDEX and RI1; produces the writer RI5 extends.
- Tests: `TestFoldUpsertsChangedRuns`, `TestFoldAppliesCountDeltas`, `TestFoldLeaseRefusesSecondWriter`, `TestHistorySweepFolds`, `TestRolloverKeepsDaysAfterFold` (across two executions), `TestStaleFoldMarksIncomplete`.
- Command: `go test ./internal/runindex/ ./internal/cli/ ./internal/budget/ -run 'Fold|HistorySweepFolds|RolloverKeepsDaysAfterFold' -count=1`
- Red step: `TestHistorySweepFolds` finds no index document after a sweep.
- Depends on: RI1 (behaviour: writes through the client's query and patch). A provider of C-INDEX's types, launched once C-INDEX has merged.
- Commit: `runindex: the history sweep folds runs into the index (RI4, D3201)`
- Reviewer tier: standard.

#### RI5: reindex, from `update` and from `fugaro runs reindex`
- Acceptance:
  - The history job's `--reindex` mode walks every run in the bucket, upserts by run id under the writer lease, recomputes the summary from the index, then sets `complete: true` (D3215). Run twice, it leaves identical documents.
  - The reindex sets `folded_through` to the walk's **start**, so the next fold picks up every run that changed during a long walk.
  - While a reindex holds the index lease, a regular sweep still does its crash sweep (D12) and skips only the fold. The rollover is its own execution and is unaffected.
  - The walk keeps a cursor with the lease. Every sweep that finds an unfinished cursor walks it further within its own timeout, so the walk resumes with no execution started by the job and no timeout override.
  - The lease carries an expiry of twice the job's timeout, so a dead execution's lease lapses.
  - `update` (as the operator) and `runs reindex` reset the cursor and may start one execution with no override, where the caller may run the job (O7).
  - `index_complete` stays false until a walk reaches the end (F39).
  - `fugaro update` starts that execution after its apply and prints one line.
  - `fugaro runs reindex` starts it again, under the fence `update`'s job-starting steps use (U3).
  - Neither waits on a run in flight.
- Files: `internal/runindex/reindex.go` (new), `internal/runindex/reindex_test.go` (new), `internal/cli/runs_reindex.go` (new), `internal/cli/update_runindex.go` (new), `internal/cli/update_runindex_test.go` (new), `internal/cli/init_stages.go` (registers the step).
- Interfaces: consumes RI4's fold and lease.
- Tests: `TestReindexIdempotent`, `TestReindexRecomputesCounts`, `TestReindexStampsWalkStart`, `TestReindexResumesFromCursor`, `TestSweepContinuesWalk`, `TestDeadLeaseLapses`, `TestSweepDuringReindexStillSweeps`, `TestUpdateStartsReindexWithRunInFlight`, `TestRunsReindexStartsJob`.
- Command: `go test ./internal/runindex/ ./internal/cli/ -run 'Reindex|SweepDuringReindex' -count=1`
- Red step: `TestReindexIdempotent` fails: no reindex mode.
- Depends on: RI4 (behaviour: it reuses the fold and the lease).
- Commit: `runindex: reindex from update and runs reindex (RI5, D3215)`
- Reviewer tier: standard. Its PR carries the `### For operators` line and the CLI reference entry for `runs reindex`.

#### RI6: `doctor` says when the index is incomplete
- Acceptance: `fugaro doctor` reads the summary document. When it carries `complete: false`, or a `folded_through` older than `PruneAfter`, doctor prints one line naming `fugaro runs reindex` (F9).
- Files: `internal/cli/doctor_runindex.go` (new), `internal/cli/doctor_runindex_test.go` (new).
- Tests: `TestDoctorNamesReindexWhenIncomplete`.
- Command: `go test ./internal/cli/ -run TestDoctorNamesReindexWhenIncomplete -count=1`
- Red step: doctor says nothing about the index.
- Depends on: C-INDEX (the summary's shape; broken edge, re-joined at I-INDEX).
- Commit: `doctor: name the reindex when the index is incomplete (RI6, F9)`
- Reviewer tier: standard.

### indexed-reads

#### IR1: `runs ls` reads the index
- Acceptance:
  - The default lists the newest 50 by time plus every active run (D3203).
  - `--since` filters: with it, every run in the window is listed, bounded by the window and not by history (the console's `--since 24h` reads stay as they are).
  - `--after` stays a run id, as the console's allow-list checks it: IR1 resolves that run's sort key from its index document and starts after it.
  - The Live tab's `runs ls --stream --json` (the `--stream` flag, in `runs.go`) reads the same way.
  - When the `--after` run has no index document yet (an overlay row), its sort key comes from the overlay.
  - `--pr` reads the index (D3204).
  - The totals come from the summary document.
  - `--json` gains `index_complete` and `folded_through`, and the text listing prints one line while the index builds.
  - No GCS listing on the default path.
- Files: `internal/cli/runs.go`, `internal/cli/runs_pr.go`, `internal/cli/runs_index_test.go` (new).
- Interfaces: consumes the C-INDEX `Reader` (the stub until I-INDEX).
- Tests: `TestRunsLsDefaultNewestPlusActive`, `TestRunsLsAfterRunID`, `TestRunsLsAfterOverlayRow`, `TestRunsLsSinceWholeWindow`, `TestRunsLsPRFromIndex`, `TestRunsLsSaysIndexBuilding`, `TestRunsLsStreamFromIndex`.
- Command: `go test ./internal/cli/ -run 'TestRunsLs(Default|AfterRunID|AfterOverlayRow|SinceWhole|PRFromIndex|SaysIndex|StreamFromIndex)' -count=1`
- Red step: `TestRunsLsDefaultNewestPlusActive` fails: today's default is a 7-day window.
- Depends on: C-INDEX.
- Commit: `runs ls: read the index, newest 50 plus active (IR1, D3203)`
- Reviewer tier: standard.

#### IR2: `plan status` reads the index; `--no-merges` for the first paint
- Acceptance:
  - `plan status` joins plan tasks to runs with index queries by plan, and reads the estimate table from the summary, with no 90-day scan.
  - The default keeps its merge lookups (at most 32, bounded by `planMaxLookups`), so `closed`, `mark` and the `--all` filter keep their meaning for every console already installed (F37).
  - `--no-merges` makes no `gh` call and adds `merges_read: false` (D3217 as amended).
- Files: `internal/cli/plan_status.go`, `internal/cli/plan_status_index_test.go` (new), `internal/cli/plan.go`.
- Interfaces: consumes the C-INDEX `Reader`.
- Tests: `TestPlanStatusReadsIndexNoScan`, `TestPlanStatusDefaultStillReadsMerges`, `TestPlanStatusNoMergesNoGh`.
- Command: `go test ./internal/cli/ -run 'TestPlanStatus(ReadsIndex|DefaultStillReadsMerges|NoMergesNoGh)' -count=1`
- Red step: `TestPlanStatusReadsIndexNoScan` sees the 90-day listing.
- Depends on: C-INDEX.
- Commit: `plan status: read the index; --no-merges for the first paint (IR2, D3217)`
- Reviewer tier: standard.

#### IR3: `graph`'s fallback reads the index
- Acceptance: in `fugaro graph`, a repository with no stored coordinator view is read from the index, not from `coordinatorRuns`' GCS scan (F3). The coordinator sweep's own reader (`coordinatorRunsSource`) is left as it is: its account holds no Firestore role (F8).
- Files: `ee/coordinator/cli/coordinator.go`, `ee/graph/cli/graph.go`, `ee/graph/cli/graph_index_test.go` (new).
- Interfaces: consumes the C-INDEX `Reader`.
- Tests: `TestGraphFallbackReadsIndex`.
- Command: `go test ./ee/graph/cli/ -run TestGraphFallbackReadsIndex -count=1 && go test ./internal/eeboundary/ -count=1`
- Red step: the test sees a GCS listing.
- Depends on: C-INDEX.
- Commit: `graph: the no-view fallback reads the index (IR3, F3)`
- Reviewer tier: standard.

#### IR4: the console's first paint without merges
- Acceptance:
  - The Graph tab paints the canvas from `plan status --json --all --no-merges`.
  - The plain `plan status --json --all` (merges included) is a separate section that decorates merged nodes, with its own busy state and refusal.
  - The new argv sits behind a CLI version floor in `console/src/shared/version.ts`; an older CLI gets the single read as today.
  - The spawn row and IPC operation are pinned, and the §5.1 table in `docs/desktop-console/spec.md` is updated.
- Files: `console/src/main/spawn.ts`, `console/src/main/ipc.ts`, `console/src/renderer/entry.ts`, `console/src/shared/version.ts`, `docs/desktop-console/spec.md`; tests `console/test/unit/graph-read.test.ts`, `spawn.test.ts`, `ipc.test.ts`.
- Interfaces: consumes `plan status --no-merges` (IR2), in the shape C-INDEX fixes.
- Tests: `graph-read.test.ts` (canvas before merges; the single read under the floor), plus the pinning tests for the new row.
- Command: `cd console && npx jest test/unit/graph-read.test.ts test/unit/spawn.test.ts test/unit/ipc.test.ts --maxWorkers=2 && cd .. && go test ./internal/cli/ -run Docs -count=1`
- Red step: `graph-read.test.ts` fails: there is one read, not two.
- Depends on: IR2 (kept: IR4 waits on CG7b at a later level anyway, so breaking it gains nothing); CG7a, CG7b and ID6 (the same files: the allow-lists, `version.ts` and `entry.ts`).
- Commit: `console: the Graph canvas before merge state (IR4, D3217)`
- Reviewer tier: opus, with an independent review (allow-list).

#### IR5a: the runner stops writing the GCS pull-request index
- Acceptance: the runner no longer writes `runs/<slug>/pr-index/` from any of its paths. The objects stay in place (D3204).
- Files: `internal/runner/prindex.go`, `internal/runner/followup.go`, `internal/runner/prflow.go`, `internal/runner/reviewartifact.go`, `internal/runner/prindex_retired_test.go` (new).
- Tests: `TestRunnerWritesNoPRIndex`.
- Command: `go test ./internal/runner/ -run TestRunnerWritesNoPRIndex -count=1`
- Red step: the runner still writes the entry.
- Depends on: I-INDEX (behaviour: `--pr` must read the live index before the old writers stop).
- Commit: `runner: stop writing the GCS PR index (IR5a, D3204)`
- Reviewer tier: standard.

#### IR5b: the CLI stops writing and backfilling the GCS pull-request index
- Acceptance:
  - `fugaro run` and `followups` stop writing the GCS pull-request index.
  - `init` and `update` drop their PR-index backfill step.
  - `docs/runs-ls-speed/implementation-plan.md` marks RL4 superseded by this plan (D3204).
- Files: `internal/cli/run.go`, `internal/cli/followup.go`, `internal/cli/init_stages.go`, `internal/cli/update_prindex.go`, `internal/cli/update_prindex_test.go`, `docs/runs-ls-speed/implementation-plan.md`.
- Tests: `TestRunWritesNoPRIndex`, `TestUpdateSkipsPRIndexBackfill`.
- Command: `go test ./internal/cli/ -run 'TestRunWritesNoPRIndex|TestUpdateSkipsPRIndexBackfill|Docs' -count=1`
- Red step: `fugaro run` still writes the entry.
- Depends on: I-INDEX (behaviour, as IR5a).
- Commit: `cli: stop writing and backfilling the GCS PR index (IR5b, D3204)`
- Reviewer tier: standard.

#### IR6: `runs stats` reads the index
- Acceptance: `runs stats` (the Spend screen's read) takes its rows from index entries in its window, with no run-object reads (F32). Its JSON is unchanged apart from the additive `index_complete`.
- Files: `internal/cli/runs_stats.go`, `internal/cli/runs_stats_index_test.go` (new).
- Tests: `TestRunsStatsReadsIndexNoObjects`.
- Command: `go test ./internal/cli/ -run TestRunsStatsReadsIndexNoObjects -count=1`
- Red step: the test counts the GCS reads made today.
- Depends on: C-INDEX (the `Reader`; broken edge, re-joined at I-INDEX).
- Commit: `runs stats: read the index (IR6, F32)`
- Reviewer tier: standard.

#### IR7: older history on demand in the Live list
- Acceptance:
  - Under the Live list's newest rows, a "Load older" control pages past the 24-hour window through the `runs.list` operation's existing argv, `runs ls --json --limit=50 --after=<last run id>` (capped at `RUNS_LIST_MAX_LIMIT`).
  - It is gated on a CLI version floor at or above the release that carries IR1. Older CLIs refuse an `--after` outside the window, and below the floor the control is not drawn.
  - The operation's `why` strings, which name the Coordinator job list CG7b removes, are reworded for the Live list.
  - Each page is a section read with its own busy state (F4).
- Files: `console/src/renderer/screens/live.ts`, `console/test/unit/live.test.ts`, `console/src/main/spawn.ts` (the `why` text only).
- Tests: `live.test.ts` (a second page arrives after the first; the cursor is the last run id).
- Command: `cd console && npx jest test/unit/live.test.ts --maxWorkers=2`
- Red step: the list stops at the window.
- Depends on: ID1 (the same file, `live.test.ts`); I-INDEX (behaviour: `--after` past the window needs IR1's index read); I-ORCH (the same file, `spawn.ts`).
- Commit: `console: load older runs on demand (IR7, F4)`
- Reviewer tier: standard.

#### I-INDEX: wire the index in
- Acceptance: the stub is deleted, and `runs ls`, `plan status` and `graph` get the real `Reader`. `TestIndexedReadsConstantRequests` seeds the fakes at 1x (3,500 runs) and 100x (350,000 entries in the fake index) and asserts that the first page of each command costs the same number of requests. `TestFixedInvocationCost` lists every request made before the first index read (U1).
- Files: `internal/runindex/stub.go` (deleted), `internal/cli/runindex_wire.go` (new), `internal/cli/indexed_reads_e2e_test.go` (new), `ee/graph/cli/graph.go`, `console/docs/json-contract.md` (the `runs ls` and `plan status` additions).
- Interfaces: consumes C-INDEX, RI2 (its index list), RI3, RI4, RI5, IR1, IR2, IR3 and IR6.
- Tests: `TestIndexedReadsConstantRequests`, `TestFixedInvocationCost`.
- Command: `go test ./internal/cli/ -run 'TestIndexedReadsConstantRequests|TestFixedInvocationCost|Docs' -count=1`
- Red step: with the stub still wired, the 100x run reads no fake index at all.
- Depends on: RI2, RI3, RI4, RI5, RI6, IR1, IR2, IR3, IR6 (every side of every edge C-INDEX broke re-joins here). It deletes C-INDEX's stub.
- Commit: `runindex: wire the index into runs ls, plan status and graph (I-INDEX, D3205)`
- Reviewer tier: standard.

#### T-CHK: the scripts of Checks 870 and 871
- Acceptance:
  - `scripts/check-870-run-index-timings.sh` times `runs ls --json`, `plan status --json --all` and `graph --json` on the owner's project, warm, and passes under 1 s each.
  - `scripts/check-871-run-index-100x.sh` seeds a scratch project's index with 350,000 synthetic entries through the history job's reindex path, times the same reads, and cleans up.
  - Both are registered in `docs/gcp-live-checklist.md`.
- Files: `scripts/check-870-run-index-timings.sh` (new), `scripts/check-871-run-index-100x.sh` (new), `scripts/check-870-run-index-timings_test.sh` (new), `scripts/check-871-run-index-100x_test.sh` (new), `docs/gcp-live-checklist.md`.
- Tests: the two `_test.sh` dry runs against fakes, the way `check-760-runs-ls-speed_test.sh` runs.
- Command: `bash scripts/check-870-run-index-timings_test.sh && bash scripts/check-871-run-index-100x_test.sh`
- Red step: the scripts do not exist.
- Depends on: I-INDEX (behaviour: what is timed).
- Commit: `checks: scripts for 870 and 871 (T-CHK, D3205)`
- Reviewer tier: standard.
- Owner-run: `scripts/check-870-run-index-timings.sh` on the owner's project, then `scripts/check-871-run-index-100x.sh --scratch <project>`. No agent runs either.

### coordinator-graph

#### C-ORCH: the orchestrator contract
- Acceptance:
  - `internal/orchestrator/naming.go` defines:
    - the orchestrator id rule, separate from `agentRE` (default `<project>`, named `<project>~<name>`, name `^[a-z0-9][a-z0-9-]{0,39}$`, a maximum id length that fits both, no cap) (D3207, D3218, F36);
    - `Driver` and the path of its sibling node, `orchestrator/<id>/driver`;
    - `ParseItemRef` for `<orchestrator>/<item>` (D3208);
    - the graph scope grammar for an orchestrator id (`orchestrator:<id>`), which today's `scopeIDPattern` refuses for named ids (F40);
    - `ClaimPath` (D3209).
  - `internal/orchestrator/mapdoc.go` defines the `orchestrator map --json` document: nodes, edges (`dependency` | `overlap`) and `health` (D3221).
  - `docs/console-fixes/contract-orchestrators.md` names the bridge method `orchestratorMap` and what each side may rely on.
  - The stub `BuildMap` returns an empty document.
- Files: `internal/orchestrator/naming.go` (new), `internal/orchestrator/mapdoc.go` (new), `internal/orchestrator/map_stub.go` (new), `internal/orchestrator/naming_test.go` (new), `docs/console-fixes/contract-orchestrators.md` (new).
- Tests: `TestNamingRules`, `TestParseItemRef`, `TestMapDocRoundTrip`.
- Command: `go test ./internal/orchestrator/ -run 'TestNaming|TestParseItemRef|TestMapDoc' -count=1`
- Red step: `TestNamingRules` fails before the file exists.
- Depends on: none.
- Commit: `orchestrator: the named-orchestrator and map contract (C-ORCH)`
- Reviewer tier: standard; at the rigor of its strictest consumer (CG4's write guard).

#### CG1a: state, lease and store accept named orchestrators
- Acceptance:
  - `checkAgentID` and `NodeIDs` use C-ORCH's orchestrator id rule, so `<project>~<name>` is accepted. Work-item ids keep `agentRE` (F36). `NodeIDs` skips the claim key `~claims` that C-ORCH's `ClaimPath` reserves.
  - The lease holder writes `driver` (the principal of the credential that holds the lease) to the sibling node `orchestrator/<id>/driver`.
  - Neither the state nor the lease document gains a field (F35, D3218 as amended). `project` and `name` are derived from the id.
- Files: `internal/orchestrator/lease.go`, `internal/orchestrator/store.go`, `internal/orchestrator/list.go`, `internal/orchestrator/named_test.go` (new).
- Tests: `TestNamedIDAccepted`, `TestLeaseWritesDriverSibling`, `TestStateAndLeaseUnchanged`, `TestStoreRefusesBadName`, `TestNodeIDsSkipsClaims`.
- Command: `go test ./internal/orchestrator/ -run 'TestNamedIDAccepted|TestLeaseWritesDriverSibling|TestStateAndLeaseUnchanged|TestStoreRefusesBadName|TestNodeIDsSkipsClaims' -count=1`
- Red step: `TestNamedIDAccepted` is refused by `agentRE`.
- Depends on: none in the graph. As the provider C-ORCH describes, it implements the contract's id rules and `Driver`, and is launched once C-ORCH has merged.
- Commit: `orchestrator: named ids and the driver node (CG1a, D3207, D3218)`
- Reviewer tier: standard.

#### CG1b: `--name` on the orchestrator commands
- Acceptance: `orchestratorID` resolves `--name` (default: the project's orchestrator) on the command tree. `orchestrator ls --json` adds `name` and `driver`, and the daemon install writes one unit per name it is asked for (the default is installed by `update`, as today). The resident orchestrator (`orchestrator serve` under `var.orchestrator`) stays on the default id (D3224).
- Files: `internal/cli/orchestrator_cmd.go`, `internal/cli/orchestrator_ls.go`, `internal/cli/orchestrator_install.go`, `internal/cli/orchestrator_named_test.go` (new).
- Tests: `TestOrchestratorNameFlag`, `TestOrchestratorLsNameDriver`, `TestInstallUnitPerName`.
- Command: `go test ./internal/cli/ -run 'TestOrchestratorName|TestOrchestratorLsNameDriver|TestInstallUnitPerName' -count=1`
- Red step: `--name` is an unknown flag.
- Depends on: C-ORCH.
- Commit: `orchestrator: --name and named units (CG1b, D3206)`
- Reviewer tier: standard. The CLI reference entry goes in this PR.

#### CG1c: every orchestrator command honours `--name`
- Acceptance: the other callers of `orchestratorID` act on the named orchestrator: escalation, import, patch, run and state. A test per caller runs it with `--name` and checks that it touched only `<project>~<name>`.
- Files: `internal/cli/escalation.go`, `internal/cli/orchestrator_import.go`, `internal/cli/orchestrator_patch.go`, `internal/cli/orchestrator_run.go`, `internal/cli/orchestrator_state.go`, `internal/cli/orchestrator_named_callers_test.go` (new).
- Tests: `TestNamedCallersTouchOnlyTheirNode`.
- Command: `go test ./internal/cli/ -run TestNamedCallersTouchOnlyTheirNode -count=1`
- Red step: `orchestrator patch --name x` writes the default node.
- Depends on: CG1b (kept: it uses the flag and the resolver CG1b adds in `orchestrator_cmd.go`; a contract for one function would be nearly the task it unblocks).
- Commit: `orchestrator: every command honours --name (CG1c, D3206)`
- Reviewer tier: standard.

#### CG2: `update` needs no orchestrator migration, and proves it
- Acceptance:
  - `fugaro update` writes nothing to an orchestrator node (D3218 as amended).
  - A test decodes, with the previous release's strict decoders, a state, a lease and a node set written by this release: the default and a named one, each with its `driver` sibling. All of them are read.
  - An installation at the previous version converges with a run in flight.
- Files: `internal/orchestrator/compat_test.go` (new), `internal/orchestrator/testdata/prev-release/` (new), `internal/cli/update_orchestrator_named_test.go` (new).
- Tests: `TestPreviousReleaseReadsNewNodes`, `TestUpdateNamedMigrationWithRunInFlight`.
- Command: `go test ./internal/orchestrator/ ./internal/cli/ -run 'TestPreviousReleaseReadsNewNodes|TestUpdateNamedMigrationWithRunInFlight' -count=1`
- Red step: a fixture node carrying an added state field must fail the previous decoder. That proves the test can fail, though on main it passes. I-ORCH re-runs it against the nodes CG1a really writes.
- Depends on: C-ORCH (the id rule and the driver node's path).
- Commit: `orchestrator: a previous release reads every node this one writes (CG2, D3218)`
- Reviewer tier: standard. The PR carries the `### For operators` line: no action, and named orchestrators are invisible to members still on the previous release (F36).

#### CG3: cross-orchestrator dependencies
- Acceptance:
  - `depends_on` may name `<orchestrator>/<item>`, resolved by searching that orchestrator's shards. The validator accepts such a reference, and keeps refusing a bare reference across the shards of one orchestrator (F44 unchanged, F17).
  - Each tick reads only the referenced orchestrators' items.
  - In a cycle across orchestrators, each orchestrator sets its own items in the cycle to `blocked`, with an escalation naming the cycle, and never writes another orchestrator's state (D3220, F33).
- Files: `internal/orchestrator/validate.go`, `internal/orchestrator/shard.go`, `internal/orchestrator/tick.go`, `internal/orchestrator/cross_test.go` (new).
- Tests: `TestCrossDependsOnValidates`, `TestCrossShardStillRefused`, `TestTickWaitsOnOtherOrchestrator`, `TestCrossCycleBlocksWithEscalation`, `TestCrossCycleWritesOnlyOwnState`.
- Command: `go test ./internal/orchestrator/ -run 'TestCross|TestTickWaitsOnOther' -count=1`
- Red step: the validator refuses a cross reference.
- Depends on: C-ORCH (`ParseItemRef`).
- Commit: `orchestrator: dependencies across orchestrators (CG3, D3208, D3220)`
- Reviewer tier: standard.

#### CG4: a claim per plan task
- Acceptance:
  - Before launching a plan task or a backlog item, an orchestrator claims it with an ETag-conditioned RTDB write at `ClaimPath`. The path is under `orchestrator/` at a key no orchestrator id can take (`~claims`); CG1a makes `NodeIDs` skip it.
  - A held claim makes the others skip the item, and a cycle whose every action was skipped reads as in flight rather than as one that did nothing: two orchestrators of one project are the deployment the claim is for, and the loser reaches the breaker's threshold over the winner's work otherwise — pausing the node and refusing its own unclaimed items too. The claim is released when the item is done or cancelled, and at no other time: nothing about its holder's lease releases it, because a daemon deletes its lease node on every clean exit (`--once`, a restart, a stop) while the runs it started are still going, so a rival that freed a claim on a lapsed beat would start a second run of one plan task (the owner's ruling of 2026-11-23). The holder's own cycle releases it — the beat re-stamps what it holds — and a release that failed, or a cycle that died after the item's own write landed, is retried by the next cycle's read of the same nodes.
  - The node keeps `orchestrator/`'s `.write: false` for every token the rules evaluate. IAM is the fence, as for the state itself (F33). No rules change.
- Files: `internal/orchestrator/claim.go` (new), `internal/orchestrator/claim_test.go` (new), `internal/orchestrator/launch.go`, `internal/orchestrator/tick.go` (the cycle re-stamps and releases what it holds), `internal/cli/orchestrator_run.go` (the daemon's beat).
- Tests: `TestClaimFirstWins`, `TestLaunchSkipsClaimed`, `TestClaimHeldWhileTheItemIsUnfinished`, `TestClaimReleasedWhenTheItemSettles`, `TestOnePlanTaskOneLaunchAcrossTwoTicks`, `TestCycleReleasesTheClaimOfAnItemThatFinished`, `TestAClaimSkipIsInFlightNotEmpty` (the skips read as in flight and do not trip the breaker), `TestBeatRestampsTheClaimsTheDaemonHolds`, `TestTheCycleLineSaysTheSkips` (against `gcpfake`'s RTDB, which honours ETags).
- Command: `go test ./internal/orchestrator/ -run 'TestClaim|TestLaunchSkipsClaimed' -count=1`
- Red step: two orchestrators both launch the task.
- Depends on: C-ORCH (`ClaimPath`).
- Commit: `orchestrator: one claim per plan task (CG4, D3209)`
- Reviewer tier: opus, with an independent review (the claim is a write guard).

#### CG5: the coordinator sweep's heartbeat
- Acceptance: each sweep writes `coordinator/project/heartbeat` (`started_at`, `ok_at`, server time) over IAM, as it writes its view (the subtree keeps `.write: false` for tokens; no rules change), and the store reads it.
- Files: `ee/coordinator/sweep.go`, `ee/coordinator/state.go`, `internal/coordpath/coordpath.go` (the path), `ee/coordinator/heartbeat_test.go` (new).
- Tests: `TestSweepWritesHeartbeat`, `TestFailedSweepKeepsOkAt`.
- Command: `go test ./ee/coordinator/ -run 'Heartbeat|TestFailedSweepKeepsOkAt' -count=1 && go test ./internal/eeboundary/ -count=1`
- Red step: no heartbeat after a sweep.
- Depends on: none.
- Commit: `coordinator: the sweep's heartbeat (CG5, D3212)`
- Reviewer tier: standard.

#### CG6: the map builder and `fugaro orchestrator map --json`
- Acceptance:
  - The builder lists orchestrator ids (one shallow read) and reads only those with a fresh lease.
  - It counts active tasks and running jobs and adds the driver and the lease age.
  - It draws dependency edges from cross references.
  - It draws overlap edges from the coordinator's stored collisions, joined through `WorkItem.run_id`, plus two orchestrators on one pull request (D3211, D3222).
  - It adds the heartbeat.
  - The command is registered from `ee/` the way `ee/graph/cli` registers `graph`. With the sweep off, the document says so.
  - `fugaro graph`'s orchestrator source carries the cross-orchestrator edges as well (`ee/graph/source_orchestrator.go`, F17).
- Files: `ee/orchestrator/mapbuild.go` (new), `ee/orchestrator/mapbuild_test.go` (new), `ee/orchestrator/cli/map.go` (new), `ee/orchestrator/cli/map_test.go` (new), `ee/graph/source_orchestrator.go`.
- Tests: `TestMapReadsActiveOnly`, `TestMapDependencyEdge`, `TestMapOverlapFromCollisions`, `TestMapSweepOff`.
- Command: `go test ./ee/orchestrator/... ./ee/graph/ -run 'TestMap|CrossOrchestrator' -count=1 && go test ./internal/eeboundary/ -count=1`
- Red step: `TestMapDependencyEdge` gets no edge.
- Depends on: C-ORCH (`MapDoc`).
- Commit: `orchestrator: the map of active orchestrators (CG6, D3221)`
- Reviewer tier: standard.

#### CG7a: the console reads the map
- Acceptance: a spawn row for `orchestrator map --json`, an IPC operation, the `orchestratorMap` bridge method in `console/src/source/cli.ts` and a strict decoder in `console/src/shared/schema.ts`. The read sits behind a CLI version floor in `console/src/shared/version.ts`; below it, the tab says which CLI version it needs. `GRAPH_SCOPE_RE` and `MAX_GRAPH_SCOPE_LENGTH` accept a named orchestrator id behind the same floor (F40). Pinned, with the §5.1 table updated.
- Files: `console/src/main/spawn.ts`, `console/src/main/ipc.ts`, `console/src/source/cli.ts`, `console/src/shared/schema.ts`, `console/src/shared/version.ts`, `docs/desktop-console/spec.md`; tests `spawn.test.ts`, `ipc.test.ts`, `schema.test.ts`.
- Tests: the pinning tests in `spawn.test.ts` and `ipc.test.ts`, plus `schema.test.ts` for the decoder.
- Command: `cd console && npx jest test/unit/spawn.test.ts test/unit/ipc.test.ts test/unit/schema.test.ts --maxWorkers=2 && cd .. && go test ./internal/cli/ -run Docs -count=1`
- Red step: the row is missing from the allow-list.
- Depends on: C-ORCH (the document's shape); ID1 (the same file, `schema.test.ts`).
- Commit: `console: the orchestrator map read (CG7a, D3221)`
- Reviewer tier: opus, with an independent review (allow-list).

#### CG7b: the Coordinator tab as a graph
- Acceptance:
  - The tab draws only active orchestrators, as elk-laid-out nodes: name, driver, active tasks, running jobs.
  - Dependency edges are solid; overlap edges are dashed and marked as a warning.
  - Clicking a node opens its live tasks.
  - The health line shows the sweep's heartbeat and each lease age. With the sweep off, the tab says so and names the fix instead of drawing no overlap.
  - The v2 sections are removed: action queue, totals, job list and transcript (D3210).
  - Single section; the rules of `.claude/rules/console.md` apply.
- Files: `console/src/renderer/screens/coordinator.ts`, `console/src/renderer/entry.ts`, `console/test/unit/coordinator.test.ts`, `docs/console-coordinator-v2/README.md` (a dated note that this plan replaces the tab, D3210).
- Tests: `coordinator.test.ts` (graph from a fixture map; drill-down; health line; no v2 section).
- Command: `cd console && npx jest test/unit/coordinator.test.ts --maxWorkers=2 && npm run typecheck`
- Red step: the test expects nodes and finds the queue.
- Depends on: CG7a (behaviour: the bridge method and decoder it imports).
- Commit: `console: the Coordinator tab as a graph of active orchestrators (CG7b, D3210)`
- Reviewer tier: standard.
- Owner-run: re-record the Coordinator goldens on the owner's Mac (`npx playwright test <coordinator spec> --update-snapshots`).

#### CG8: the graph scope accepts named orchestrators
- Acceptance: `graph.ParseScope` accepts `orchestrator:<project>~<name>` under C-ORCH's id rule, so `fugaro graph --source orchestrator:<id>` reads a named orchestrator (F40).
- Files: `ee/graph/key.go`, `ee/graph/key_test.go`.
- Tests: `TestParseScopeNamedOrchestrator`.
- Command: `go test ./ee/graph/ -run TestParseScopeNamedOrchestrator -count=1 && go test ./internal/eeboundary/ -count=1`
- Red step: `orchestrator:fugaro~night` is refused.
- Depends on: C-ORCH (the id rule; broken edge, re-joined at I-ORCH).
- Commit: `graph: scopes for named orchestrators (CG8, F40)`
- Reviewer tier: standard.

#### CG-DOCS: the orchestrator skill and the documents that said one per project
- Acceptance: `plugin/skills/orchestrator/SKILL.md` describes named orchestrators and `--name`. orchestrator-graph-estimates' reconciliation gets a dated amendment to F2 citing D3206. The plugin tests pass.
- Files: `plugin/skills/orchestrator/SKILL.md`, `docs/orchestrator-graph-estimates/reconciliation.md`.
- Tests: `go test ./plugin/... -count=1`
- Command: `go test ./plugin/... -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents); the plugin test fails if a named flag does not exist.
- Depends on: CG1b (behaviour: the plugin tests check every flag a skill names against the CLI).
- Commit: `docs: named orchestrators in the skill and the earlier plan (CG-DOCS, D3206)`
- Reviewer tier: opus (plugin skill).

#### I-ORCH: wire the map in
- Acceptance:
  - The stub `BuildMap` is deleted and the command calls `ee/orchestrator`'s builder.
  - An end-to-end test runs two named orchestrators in one project, with a cross dependency and an overlapping collision, and gets one solid and one dashed edge in the document the console decodes.
  - The v2 operations no screen uses any more are removed from the allow-lists.
  - `console/docs/json-contract.md` gains the map's section, and its §6.22 is amended: an edge may join two orchestrators (F17).
  - CG2's `TestPreviousReleaseReadsNewNodes` passes against nodes the wired code writes.
- Files: `internal/orchestrator/map_stub.go` (deleted), `ee/orchestrator/cli/map.go`, `ee/orchestrator/map_e2e_test.go` (new), `console/src/main/spawn.ts`, `console/src/main/ipc.ts`, `console/docs/json-contract.md`, `docs/desktop-console/spec.md`.
- Tests: `TestOrchestratorMapEndToEnd`, plus the allow-list pinning tests.
- Command: `go test ./ee/orchestrator/... -run TestOrchestratorMapEndToEnd -count=1 && go test ./internal/eeboundary/ -count=1 && cd console && npx jest test/unit/spawn.test.ts test/unit/ipc.test.ts --maxWorkers=2 && cd .. && go test ./internal/cli/ -run Docs -count=1`
- Red step: the stub returns an empty map.
- Depends on: CG1a, CG1b, CG1c, CG2, CG3, CG4, CG5, CG6, CG7a, CG7b, CG8 (every side of every edge C-ORCH broke, plus the rest of the module); IR4 (the allow-list files). It deletes C-ORCH's stub.
- Commit: `orchestrator: wire the map end to end (I-ORCH)`
- Reviewer tier: opus, with an independent review (allow-list).

### identity-display

#### ID1: the sanitizer masks only secrets
- Acceptance:
  - `sanitize.ts` drops the `address-marker` rule from `FREE_TEXT_RULES` and `REFUSAL_RULES`.
  - The `person` class draws any value that is not a secret, cleaned of markup and control characters, at the class's length limit.
  - `REPO_TOKEN_RE` and the branch, path, GCP-name and IP/host rules go, and the `host` and `path` classes stop replacing.
  - The `replaced-url` rule, which today replaces every URL with `[redacted-url]`, is narrowed to the credential parts. It reuses the typed `applyUrl`'s own predicates rather than a new list: `carriesCredential` for the query and the fragment, `isWebhook` (`WEBHOOK_URLS`), `hostCarriesCredential`, and dropping userinfo. A URL any of them flags is still replaced whole. The rule stays in `CREDENTIAL_RULES`, so the validating classes keep it.
  - The credential, token, entropy and credential-block rules stay, and `CREDENTIAL_RULES` (the positional slice `FREE_TEXT_RULES.slice(0, 2)`) still names exactly the credential rules after the reorder.
  - The entropy net no longer declines path runs once the path rule that caught them is gone (today `!isPathRun` and `isPathShapedSecret` skip a rooted run). A token inside a rooted path is still masked, but an ordinary long path is not: the guard exists because of the owner's report of 2026-10-21, when the net marked the operator's own binary path as a credential (the docstring above `FREE_TEXT_RULES`).
  - The tests that assert a display marker for a non-secret value now assert the value. `sanitize-agreement.test.ts` keeps its one-way direction (the display may redact less than the recorder). The recorder's tests are untouched (D3219).
- Files:
  - Source: `console/src/shared/sanitize.ts`.
  - Sanitizer tests: `sanitize.test.ts`, `sanitize-agreement.test.ts`, `sanitize-path-runs.test.ts`, `sanitize-ambiguity.test.ts`.
  - Screen tests whose expectations change: `live.test.ts`, `projects.test.ts`, `spend.test.ts`, `spend-controls.test.ts`, `seats.test.ts`, `accountkeys.test.ts`, `degraded.test.ts`, `schema.test.ts`, `graph.test.ts`, `normal-fixture.test.ts`, `screen-literals.test.ts`, `manage-code.test.ts`, `credentialsservice.test.ts`, all under `console/test/unit/`, and `console/test/e2e/spend.spec.ts`.

  This is over the five-file guide by necessity: one source file changes, and every other file is a test whose expectation that change moves. Split, the commit in between would be red.
- Tests: the sanitizer files above, plus new cases:
  - a token in a rooted path (`/x/y/<token built at runtime>`);
  - `https://user:<token>@host/...`;
  - `?token=<token>` in free text;
  - a fragment `#access_token=<token>` and a signed-URL `X-Goog-Signature=<value>`;
  - a high-entropy URL path segment (`https://hooks.example/services/<token>`);
  - each of the URL cases under both rule sets: free text and the validating classes;
  - one case per removed rule, proving a secret inside that shape is still masked;
  - a long binary path (`/opt/homebrew/Cellar/fugaro/<version>/bin/fugaro`) in a refusal, drawn as is;
  - a token in a rooted path inside a refusal (`sanitizeRefusal`), masked: today `boundReason`'s path mask hides it, and ID6 removes that mask.
- Command: `cd console && npx jest test/unit --maxWorkers=2 && npm run typecheck && npx playwright test test/e2e/spend.spec.ts`
- Red step: an email drawn as itself fails today's expectation of `redacted@example.invalid`.
- Depends on: none.
- Commit: `console: mask only secrets (ID1, D3213)`
- Reviewer tier: opus, with an independent review (the untrusted-text boundary).

#### ID2: identities drawn whole
- Acceptance: `launcher.ts` no longer cuts at `@`. The Live and Projects "Started by" and the run screen's `launched_by`, `requested_by` and `oauth_account` draw the whole value, and the people screen's member column shows the address.
- Files: `console/src/shared/launcher.ts`, `console/test/unit/launcher.test.ts`, `console/src/renderer/manage/kinds/people.ts`, `console/test/unit/manage-people.test.ts`.
- Tests: `launcher.test.ts`, `manage-people.test.ts`.
- Command: `cd console && npx jest test/unit/launcher.test.ts test/unit/manage-people.test.ts --maxWorkers=2`
- Red step: `ana.smith@acme.com` is drawn as `ana.smith`.
- Depends on: ID1 (behaviour: the `person` class must accept the value).
- Commit: `console: identities drawn whole (ID2, D3213)`
- Reviewer tier: standard.

#### ID3: the per-person breakdown on by default
- Acceptance: the spend screen's per-person breakdown is on by default. D15's dated amendment in `docs/desktop-console/spec.md` is written by ID4, which owns that file at its level.
- Files: `console/src/renderer/screens/spend.ts`, `console/test/unit/spend.test.ts`.
- Tests: `spend.test.ts`.
- Command: `cd console && npx jest test/unit/spend.test.ts --maxWorkers=2`
- Red step: the breakdown is off by default.
- Depends on: ID1 (the same file, `console/test/unit/spend.test.ts`).
- Commit: `console: per-person spend on by default (ID3, D3214)`
- Reviewer tier: standard.

#### ID4: the documents say only secrets are masked
- Acceptance: `console/README.md`'s class table and its "no address is drawn anywhere" paragraph, `docs/desktop-console/spec.md` §6.2 and §5.1 rule 7, and `.claude/rules/console.md` all say the display masks secrets only and cleans markup. The fixture recorder's redaction is named as unchanged (D3219). D15 in §2.3 gets a dated amendment citing D3214 (ID3).
- Files: `console/README.md`, `docs/desktop-console/spec.md`, `.claude/rules/console.md`.
- Tests: `go test ./internal/cli/ -run Docs -count=1`
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents).
- Depends on: ID1 (it describes ID1's behaviour); ID3 (it records ID3's default); I-ORCH (the same file, `docs/desktop-console/spec.md`).
- Commit: `docs: the console masks only secrets (ID4, D3213)`
- Reviewer tier: opus (a rules file).

#### ID5: every `n/a` has a named cause
- Acceptance: a table test drives every renderer site that can draw `UNAVAILABLE_MARK`. It asserts that a present, valid identity value never draws `n/a`, and that each remaining `n/a` has a stated cause: a field the record lacks, a version floor or a refused read (F28).
- Files: `console/test/unit/unavailable-causes.test.ts` (new).
- Tests: `unavailable-causes.test.ts`.
- Command: `cd console && npx jest test/unit/unavailable-causes.test.ts --maxWorkers=2`
- Red step: before ID1 and ID2, `launched_by: ana.smith@acme.com` draws `n/a`.
- Depends on: ID1, ID2 (behaviour).
- Commit: `console: every n/a names its cause (ID5, F28)`
- Reviewer tier: standard.

#### ID6: the main process's refusal text keeps paths
- Acceptance: `boundReason` (`console/src/main/ipc.ts`) stops replacing absolute paths with `<path>`. It keeps its handling of control, invisible and surrogate characters and its bound on length; it has no credential handling of its own. Secrets in a refusal are masked by the renderer's `sanitizeRefusal`, which ID1 makes catch a token in a rooted path (F38, D3213).
- Files: `console/src/main/ipc.ts`, `console/test/unit/ipc.test.ts`.
- Tests: `ipc.test.ts` (a refusal naming `/Users/ana/repo` keeps the path).
- Command: `cd console && npx jest test/unit/ipc.test.ts --maxWorkers=2`
- Red step: the path reads `<path>`.
- Depends on: ID1 (behaviour: the renderer must catch a token in a rooted path before this mask goes); CG7a (the same files, `ipc.ts` and `ipc.test.ts`).
- Commit: `console: refusals keep their paths (ID6, D3213)`
- Reviewer tier: opus, with an independent review (`ipc.ts` is an allow-list file).

## Launch order

`fugaro plan graph` refuses an edge from a provider to the contract it implements, so three providers show no edge to their contract (O5). Every other task launches by the graph's edges, and three are held back by hand:

- CG1a waits for C-ORCH to merge.
- RI3 and RI4 wait for C-INDEX to merge. They are behind RI1 anyway.

The writers of a shared file are ordered by edges, not only by levels. A task at a later level whose edges do not reach an earlier writer could otherwise run beside it. The shared-files table below lists each pair and the edge that orders it.

## Parallel execution

### Levels

As `fugaro plan graph` reads them; Launch order (above) holds CG1a, RI3 and RI4 behind their contracts. R-index-fields and R-map-shape run only if raised.

| Level | Tasks |
| --- | --- |
| 0 | C-INDEX, C-ORCH, CG1a, CG5, ID1, RI1 |
| 1 | CG1b, CG2, CG3, CG4, CG6, CG7a, CG8, ID2, ID3, IR1, IR2, IR3, IR6, R-index-fields, R-map-shape, RI2, RI3, RI4, RI6 |
| 2 | CG-DOCS, CG1c, CG7b, ID5, ID6, RI5 |
| 3 | I-INDEX, IR4 |
| 4 | I-ORCH, IR5a, IR5b, T-CHK |
| 5 | ID4, IR7 |

What must stay sequential is every kept edge, listed with its reason under "Dependencies broken and kept".

### Critical path

As the read reports it: C-ORCH → CG7a → CG7b → IR4 → I-ORCH → ID4, six tasks; the bottleneck is C-ORCH. Every link after the contract is a shared file or a bridge import, not a computed dependency. The run-index path, RI1 → RI4 → RI5 → I-INDEX → IR5a, is five.

### Shared files

Checked from the graph's footprints: every pair of writers of one file is ordered by an edge path, not only by level.

| File | Writers (level), each ordered after the one before it by an edge path |
| --- | --- |
| `console/docs/json-contract.md` | I-INDEX (3), I-ORCH (4) |
| `console/src/main/ipc.ts` | CG7a (1), ID6 (2), IR4 (3), I-ORCH (4) |
| `console/src/main/spawn.ts` | CG7a (1), IR4 (3), I-ORCH (4), IR7 (5) |
| `console/src/renderer/entry.ts` | CG7b (2), IR4 (3) |
| `console/src/shared/version.ts` | CG7a (1), IR4 (3) |
| `console/test/unit/ipc.test.ts` | CG7a (1), ID6 (2), IR4 (3), I-ORCH (4) |
| `console/test/unit/live.test.ts` | ID1 (0), IR7 (5) |
| `console/test/unit/schema.test.ts` | ID1 (0), CG7a (1) |
| `console/test/unit/spawn.test.ts` | CG7a (1), IR4 (3), I-ORCH (4) |
| `console/test/unit/spend.test.ts` | ID1 (0), ID3 (1) |
| `docs/console-fixes/contract-index.md` | C-INDEX (0), R-index-fields (1) |
| `docs/console-fixes/contract-orchestrators.md` | C-ORCH (0), R-map-shape (1) |
| `docs/desktop-console/spec.md` | CG7a (1), IR4 (3), I-ORCH (4), ID4 (5) |
| `ee/graph/cli/graph.go` | IR3 (1), I-INDEX (3) |
| `ee/orchestrator/cli/map.go` | CG6 (1), I-ORCH (4) |
| `internal/cli/init_stages.go` | RI5 (2), IR5b (4) |
| `internal/orchestrator/map_stub.go` | C-ORCH (0), I-ORCH (4) |
| `internal/orchestrator/mapdoc.go` | C-ORCH (0), R-map-shape (1) |
| `internal/runindex/contract.go` | C-INDEX (0), R-index-fields (1) |
| `internal/runindex/stub.go` | C-INDEX (0), I-INDEX (3) |

## Dependencies broken and kept

Broken by contract (the dependent needs only the interface):

| Was | Contract | Integration | Reason |
| --- | --- | --- | --- |
| IR1 needs RI3 | C-INDEX | I-INDEX | runs ls needs the Reader's shape, not a live index |
| IR2 needs RI3 | C-INDEX | I-INDEX | plan status needs the query shape and the summary's fields |
| IR3 needs RI3 | C-INDEX | I-INDEX | the graph fallback needs only List |
| RI2 needs RI4 | C-INDEX | I-INDEX | the Terraform indexes need the field list, not the writer |
| IR6 needs RI3 | C-INDEX | I-INDEX | runs stats needs the Reader's shape, not a live index |
| RI6 needs RI4 | C-INDEX | I-INDEX | doctor needs the summary's fields, not the fold |
| CG8 needs CG1a | C-ORCH | I-ORCH | the scope grammar needs the id rule, not the store |
| CG1b needs CG1a | C-ORCH | I-ORCH | the commands need the id rules and the driver type, not the store's code |
| CG2 needs CG1a | C-ORCH | I-ORCH | the migration needs the field names, not the readers |
| CG3 needs CG1a | C-ORCH | I-ORCH | cross references need the id rules, not the stored fields |
| CG4 needs CG1a | C-ORCH | I-ORCH | claims need ClaimPath, not the named state |
| CG6 needs CG3 | C-ORCH | I-ORCH | the builder needs the edge vocabulary, not the tick |
| CG7a needs CG6 | C-ORCH | I-ORCH | the console needs the document's shape, not the builder |

Kept, each with its reason (the integration tasks' own edges, which re-join what a contract broke, are not repeated):

- RI3 needs RI1: it calls the client's new query.
- RI4 needs RI1: it writes through the client's query and patch.
- RI5 needs RI4: the reindex reuses the fold and the writer lease.
- IR4 needs IR2: IR4 waits on CG7b at a later level anyway, so breaking this edge gains nothing.
- IR4 needs CG7a: one file: the allow-lists and the 5.1 table.
- IR4 needs CG7b: one file: console/src/renderer/entry.ts.
- IR5a needs I-INDEX: --pr must read the live index before the old writers stop.
- IR5b needs I-INDEX: --pr must read the live index before the old writers stop.
- T-CHK needs I-INDEX: what the checks time.
- CG1c needs CG1b: it uses the flag and resolver CG1b adds; a contract for one function would be nearly the task.
- CG7b needs CG7a: the bridge method and decoder it imports.
- CG-DOCS needs CG1b: the plugin tests check every flag a skill names.
- IR7 needs ID1: one file: console/test/unit/live.test.ts.
- IR7 needs I-INDEX: --after past the window needs the index read.
- IR7 needs I-ORCH: one file: console/src/main/spawn.ts.
- ID6 needs ID1: the renderer must catch a token in a rooted path before this mask goes.
- ID6 needs CG7a: one file: console/src/main/ipc.ts and its test.
- IR4 needs ID6: one file: console/src/main/ipc.ts and its test.
- CG7a needs ID1: one file: console/test/unit/schema.test.ts.
- ID3 needs ID1: one file: console/test/unit/spend.test.ts.
- ID2 needs ID1: the person class must accept the value.
- ID5 needs ID1: the n/a causes after the masks go.
- ID5 needs ID2: whole identities drawn.
- ID4 needs ID1: it describes ID1's behaviour.
- ID4 needs ID3: it records ID3's default in D15's amendment.
- ID4 needs I-ORCH: one file: docs/desktop-console/spec.md.

The read refuses an edge from a provider to its contract (RI3, RI4, CG1a) unless a decoupling record names it. The order is therefore written in those tasks ("launched once the contract has merged"), not drawn in the graph.

Contract changes anticipated:

- **R-index-fields**, raised by IR2 if the summary needs a per-plan count to join without a query. It would reach RI2, RI3, RI4 and IR1.
- **R-map-shape**, raised by CG7b if the drill-down needs per-task fields the node lacks. It would reach CG6 and CG7a.

The gain, as `fugaro plan graph` reports it: `gain: 13 broken, 20 kept, 6 added; before 7 tasks, weight 14; after 6 tasks, weight 12; improves`. The decoupling pass is on for this repository (`planning.decoupling` true), and the rigor default is `startup`, so the plan decouples aggressively.

## Estimate

The estimate line `fugaro plan graph` printed, quoted as printed:

`ready in 341–1418 min, no history yet for 23 tasks (39 tasks; tiers for 16, seeds for 23; from the cached history of 2026-11-21 18:33 UTC)`

It is informational and reserves no budget. It is what the tasks' tiers and this machine's history predict, and it moves as runs finish and the graph changes.

## Checks this plan takes

| Check | Measures | Who |
| --- | --- | --- |
| 870 | The owner's project, warm: `runs ls --json`, `plan status --json --all`, `graph --json` each under 1 s | Owner, `scripts/check-870-run-index-timings.sh` (T-CHK) |
| 871 | A scratch project seeded with 350,000 synthetic runs: the same reads under 1 s | Owner, `scripts/check-871-run-index-100x.sh --scratch <project>` (T-CHK) |
| 872–879 | Reserved, unused | — |

Check 760 (runs-ls-speed) runs before IR1 merges, as the "before" reading (O1, D3204).

## After-deploy steps

None outside `fugaro update`. RI2, RI5 and CG2 each add their `### For operators` line ("run `fugaro update`"). The update applies the indexes and starts the reindex, and prints one line for each. The index reports `index_complete: false` until the reindex finishes; no operator action is needed.

## Considered and declined

- **Every live driver as a node, with no orchestrator change** (the recommendation for D3206). Declined by the owner in favour of many named orchestrators per project.
- **One orchestrator per driver**, and a cap of 16 (recommendations for D3207). Declined: named, any number.
- **Granting `datastore.user` to each run's job** (D3201). Declined: the grant is database-wide.
- **An Eventarc trigger** (D3202). Declined: it is new infrastructure.
- **A Firestore emulator timing test in CI** (D3205). Declined in favour of request-count tests plus Checks 870 and 871.
