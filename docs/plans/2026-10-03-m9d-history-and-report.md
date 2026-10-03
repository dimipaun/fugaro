# M9d — Spend history in Firestore and `fugaro report` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax. Briefs to implementers stay short: the task text plus the Review Focus line that applies.

**Goal:** a daily job moves each finished UTC day from the Realtime Database into Firestore (`spendDaily`, one document per repository per day), prunes old day nodes only after the write is read back, and `fugaro report` answers "what did we spend, by day/week/month/repo/model/person" from those documents, with today's partial from RTDB. Model dollars, notional dollars and compute are three columns, never summed into a cap.

**Spec:** [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §6.1 (D4 regions, IAM), §6.2, §9, §11, §14, §15 tasks 18-19, D4, D5, D6, D7, D12; [m9-spec-v3-reconciliation.md](../design/m9-spec-v3-reconciliation.md) (rulings 1, 2, 7: `computeUsd` split, no `infraAdjustmentUsd`, no per-stage fields); v3 source §10 (daily record, rollups computed at read, idempotent by deterministic document ID). Built before this: M9b (counters, sweeper, `history --sweep`), M9c (`watch`).

## What is built and what this reuses

| Need | Already there |
|---|---|
| Day counters | `spend/<day>/global`, `.../repos/<slug>` (`counted, spent, notional, calls, byModel/<escaped model>/{micros,in,out,cr,cw}`), `.../runs/<slug>/<run>` (`reserved, released, spent`: the run's share of that day), `.../meta {date, archived}` (`budget.PathSpend*`, `Counters.Models()`, `DayDate`, `Unkey`) |
| Run facts | `runs/<slug>/<run>` ledger (`overrun`, `crashed`), `outcomes/<day>/<slug>/<run> {status, requestedBy}`, runs bucket `result.json` with `cost.model_usd / compute_usd / compute_estimated` and `started_at` (`runstore`, `runview.Row`, `loadRows`) |
| Admin RTDB access, project mark check | `history.go`: env contract, `rtdb.New`, ADC with budget scopes, the `/fugaro/project` mark check, `checkDatabaseHost`; `historyWiring` for tests |
| Job and Scheduler | installation module `history.tf`: job `fugarohist` args `["budget","history","--sweep"]`, `*/15` Scheduler job, `fugaro-scheduler` holds `run.invoker` on the job |
| Firebase root | `firebase.tf`/`iam.tf`: member grants, `prevent_destroy` on the RTDB instance, `history_database`/`history_auth` grants, `apis.tf` has no `firestore` yet |
| Idempotent REST ensure steps in init | `deployDatabase` (plan, show, confirm, apply) and the Identity Platform step (read, then initialize only if missing) in `init_firebase.go` |
| Fake GCP | `gcpfake` (RTDB, IAM, Scheduler, Identity Toolkit); no Firestore yet |

## Decisions already made (binding)

Each Fugaro project has its own Firebase project: **no project prefix** on document IDs, "global" is computed at read (ruling 1). Caps count model dollars only; compute is reported, never capped (D7, ruling 2). UTC days (D5). Firestore in `us-east5`, with `nam5` for installs elsewhere (D4). Admins per D6; the history account holds `roles/datastore.user`. Everything is Go, no backward compatibility: M9d ships as one shape, and the two existing installs pick it up by re-running `fugaro init --firebase`. M9d is **built and merged with no live apply** (user's ruling): every live step is a **⚠ CONFIRM** at the end.

## Rulings (settled here; each says what it costs if wrong)

**H1. Data model.** `spendDaily/<YYYY-MM-DD>_<slug>`, one document per repository per day (slug as in the RTDB, `task.Slug`, safe in a document ID). Fields: `repo, slug, date` (string, the only queried field), `spentMicros, notionalMicros, computeMicros, unreconciledMicros, overrunMicros` (integers: the RTDB's exact µ$; USD is formatting, and sums never drift; amends the design's `...Usd` floats), `runHours` (float), `calls, runs`, `outcomes{succeeded,failed,halted,cancelled,infra_error}`, `byModel{<ModelKey>: {micros,in,out,cr,cw}}`, `byPerson{<Key(requested_by)>: {micros,notionalMicros,runs}}`, `capDailyMicros` (the effective repo cap that day, absent when none), `final, version: 1, archivedAt, writtenAt`. Map keys use the RTDB's `Key` escaping (a `.` is not allowed in a Firestore field path). A second document `meta/installation {project, version}` is the Firestore mark (same check as `/fugaro/project`). *If wrong:* a field rename later costs one backfill (`--day`), cheap.

**H2. Queries need no composite index.** `report` runs one structured query per call: `date >= since AND date <= until` (single-field, automatic index), and filters repository, person and model client-side. Document-ID ranges (the design) would need `__name__` filters the fake must emulate for little gain. *If wrong:* a large installation pages more than needed; at 20 repositories and 365 days that is 7,300 documents a year, fine.

**H3. Day attribution, and when a day is final.** A lease taken on day D and spent after midnight is charged to **D** (M9b's usage report puts spend on the oldest day that has a lease; the rules allow a run to write only to today and yesterday, `DAYOK`). So day D's nodes can still change until **00:00 UTC of D+2**. The rollover therefore writes each day twice: provisional (`final:false`) in the pass after D ends, final (`final:true`) once `now >= start(D+2)`. Compute (from `result.json`) and the outcome count are attributed to the run's **start day**; a run spanning midnight keeps its model dollars on the lease days and its compute on the start day (documented, small). A run longer than the job timeout (24 h at most) cannot exist, so D+2 is safe. *If wrong:* a late write lands in an archived day: the final pass re-reads and overwrites by `--day`; the PR records the 24 h bound.

**H4. Idempotent writes, exactly-once-ish.** Each pass derives every document from RTDB and the runs bucket and `PATCH`es it with an `updateMask` of all fields (so map fields are replaced whole, and the write is a pure function of the inputs). A document with `final:true` is **never rewritten** unless `--day D --force` (backfill); a second run is a no-op. `archivedAt` is set when the document first becomes final; `writtenAt` on every write. No retry loop beyond the REST client's; the next day's pass is the retry (the job looks back 7 days, as the design says). Day 0 backfill: `--day D` for any day still in RTDB.

**H5. Pruning, and never before the write is confirmed.** A day node (`spend/<day>`, `outcomes/<day>`) is deleted only when all hold: its day is older than **8 days** (design), every repository of that day has a `final:true` document, and a **read-back** of each document equals the source figures (spent, notional, calls, runs). Ledgers `runs/<slug>/<run>` are deleted with the last day that mentions them. A day whose read-back fails stays in RTDB, is warned about, and fails the job (exit 1, Cloud Logging). The prune is a single multi-path `PATCH` of nulls, so it is atomic per day. *If wrong:* data loss: this is the critical task (T3).

**H6. Compute from `result.json`.** The job lists runs started that UTC day for each slug (`runstore.ListRunIDs` with `since`) and reads `result.json` as the runs bucket allows: `compute_usd` if `compute_estimated`, else the run counts as `unestimated` and adds hours only; `runHours` is `started_at..finished_at`. The history account gets `roles/storage.objectViewer` on the runs bucket (the design says "the job can read the runs bucket"; M9b did not grant it: T4). *If wrong:* compute is 0 and shown `n/a`, never an invented number.

**H7. One job, a second Scheduler job.** Reuse the history Cloud Run job (same image, account and env) with a **second Scheduler job `fugaro-history-rollover` at `30 0 * * *` UTC** passing `args` through the `:run` call's `overrides`; the 15-minute job keeps `--sweep`. The sweep must not do the rollover work: its 8-minute timeout and its job is liveness. Decision rejected: rollover inside the 15-minute pass, which would run the bucket scan 96 times a day. *If wrong:* one Scheduler job to retime.

**H8. Firestore database creation is an idempotent REST ensure step in `init --firebase`, not Terraform.** The location is permanent and a `google_firestore_database` create does not adopt an existing database (the live lesson of Identity Platform and the RTDB instance). The step reads `GET projects/<fp>/databases/(default)`: absent, create it (`FIRESTORE_NATIVE`, `us-east5`, `deleteProtectionState: DELETE_PROTECTION_ENABLED`) behind its own confirmation; present in the right location, adopt; present in another location, **refuse** with the reason (never recreate). APIs (`firestore`, `firebaserules`), IAM and rules are Terraform/REST as below. `--plan-only` shows the step. *If wrong:* a wrong-region database is permanent: so T5 is critical.

**H9. IAM and rules.** `fugaro-history`: `roles/datastore.user` on the FP (Terraform, `firebase/iam.tf`). Launchers and operators: `roles/datastore.viewer` (history holds dollar totals and requester addresses, no task text); budget admins: `roles/datastore.viewer`, as design §6.1 already says (they hold `owner/editor` implicitly for more). Job accounts: nothing, as for RTDB. Firestore security rules deny everything (`allow read, write: if false`), deployed over the `firebaserules` REST API in the same step: only IAM principals (REST, bypassing rules) can read or write; no client SDK path exists. *If wrong:* an open database: pinned by a rules fixture test and by live check 4.

**H10. `fugaro report` UX.** `fugaro report [--repo R] [--since D] [--until D] [--by day|week|month|year|repo|model|person] [--json] [--csv]`. `--since/--until` take `YYYY-MM-DD` or `7d`, `4w`; the default is the last 30 days; `--by` defaults to `day` for ranges up to 31 days and `month` above. Weeks are ISO weeks in UTC, labelled `2026-W40`. Columns: `PERIOD|REPO|MODEL|PERSON`, `MODEL $`, `NOTIONAL`, `COMPUTE`, `RUNS`, `OK/FAIL/HALT`, `CALLS`; a total row; **`notional` is never added to `MODEL $` and a header note says so** (subscription runs, ruling 7); `COMPUTE` shows `n/a` when no run was estimated. Today's partial (and any non-final day) is read from RTDB with the same pure `Rollup` as the job, marked `(partial)`; the compute of today is `n/a`. `--by model` shows the coder/reviewer split only as models; `--by person` is the launcher's address (`requestedBy`). Output is plain text for a terminal and when piped (no escapes), `--json` has the project header, `--csv` the same rows. Everything job-written passes `oneLine`.

**H11. Degraded mode (no Firebase or no Firestore database).** No `budget.firebase_project`: report from the runs bucket via `loadRows` (model and compute per run, no notional/person split beyond `requested_by`), banner `no budget backend: totals from run records`, `--by day|repo|model|person` still work (those are fields of the rows); the document store being missing (404) with Firebase present: the same, plus `no history database: run fugaro init --firebase`. Never an invented zero.

**H12. Tests.** A **Firestore fake over REST** in `gcpfake` (documents `GET/PATCH` with `updateMask`, `runQuery` with a field filter and ordering, `databases` get/create, location, a failure injector), like the RTDB fake: the production client is the code under test, and a gRPC emulator would not exercise it. No Firestore emulator in CI (the Firebase RTDB emulator job exists only for the rules property test; M9d adds no rules). The live check settles what the fake cannot (the REST field mask on map keys, the location list).

## Review Focus

Per-task reviews are only for the two tasks that delete data or create something irreversible; the rest get one review of the branch at the end (user's token-economy rule).

1. **Deleting RTDB data too early or the wrong data.** T3: `TestPruneNeedsFinalDocAndReadBack`, `TestPruneKeepsFreshDays` (day 8 boundary), `TestFailedWriteKeepsDayAndFails`, `TestReadBackMismatchKeepsDay`, `TestPruneOnlyOwnDatabase` (the mark check), `TestBackfillForceOnlyWithDay`, `TestFinalDocNeverRewritten`.
2. **An irreversible or wrong database.** T5: `TestEnsureRefusesOtherLocation`, `TestEnsureNeverDeletes`, `TestEnsureAdoptsExisting`, `TestEnsureNeedsConfirm`, `TestPlanOnlyShowsEnsure`, `TestRulesDenyAll`.
3. **A wrong number.** T2: `TestRollupMatchesCounters` (per-repo sums equal the global counter), `TestRolloverIdempotent`, `TestMidnightRunCharged` (H3), `TestOauthIsNotionalOnly`, `TestUnreconciledFromCrashed`. T6: `TestNotionalNeverInModelColumn`, `TestWeekIsISOUTC`, `TestPartialMarked`.
4. **Terminal injection** from requester addresses and model names in `report` (`oneLine`): T6 `TestHostilePersonSanitized`.

## File Structure

| Path | Role |
|---|---|
| `internal/firestore/` | REST client: `Get`, `Patch(mask)`, `Query(date range)`, typed value encode/decode, `EnsureDatabase`, errors (`ErrNotFound`, `ErrPermission`, `ErrUnavailable`) |
| `internal/budget/rollup.go` | pure: `Rollup(day, tree, runs) []DayRecord` and `DayRecord` <-> Firestore fields; the same code serves `report`'s partial |
| `internal/budget/rollover.go` | `Roller`: lookback, provisional/final, write, read-back, prune |
| `internal/cli/history.go` | `--rollover [--day D] [--force]`, env `FUGARO_RUNS_BUCKET`, `FUGARO_FIRESTORE_DB` |
| `internal/cli/report.go`, `internal/report/` | flags, query, aggregation by period/repo/model/person, tables, JSON, CSV, degraded |
| `internal/gcpfake/firestore.go` | the fake (H12) |
| `deploy/terraform/gcp/modules/firebase/{apis,iam}.tf`, `.../installation/history.tf` | `firestore` and `firebaserules` APIs; `datastore.user/viewer`; `storage.objectViewer` on the bucket; the rollover Scheduler job; env |
| `internal/infra/`, `internal/cli/init_firebase.go` | the ensure step, the rules, the mark document |
| `docs/design/m9-budget-and-dashboard.md`, `docs/design/v1.md`, `README.md`, `docs/gcp-setup.md`, `docs/gcp-live-checklist.md` | docs |

## Task dependency graph and lanes

| Task | Size | Lane | Depends on |
|---|---|---|---|
| T1 `internal/firestore` client and `gcpfake` Firestore | M | A | - |
| T2 `Rollup` (pure) and `DayRecord` | M | B | - |
| T3 Rollover: lookback, write, read-back, prune, `--rollover` **(critical review)** | M | A | T1, T2 |
| T4 Terraform: APIs, IAM, bucket read, rollover Scheduler, env; golden/fixtures | M | C | - |
| T5 `init --firebase`: Firestore ensure, rules, mark **(critical review)** | M | C | T1, T4 |
| T6 `fugaro report` and degraded mode | M | B | T1, T2 |
| T7 docs (design amendments, v1, README, setup, checklist) | S | C | T3, T5, T6 |
| T8 live runbook | S | controller | all merged |

No new Go dependencies (REST over `net/http`, as `rtdb`). Task 1 and 2 start at once.

### Task 1 (M, lane A): Firestore client and fake

**Files:** `internal/firestore/*.go`, `internal/gcpfake/firestore.go`, tests.
- Typed-value encode/decode for the field kinds H1 uses (string, integer as decimal string, double, bool, timestamp, map). `Patch` with an `updateMask` of backticked field paths; `Query` on `date` with a bounded page loop; one error vocabulary as `rtdb` (permission, unavailable, not found). ADC through the same token source as the RTDB.
- [ ] **Failing tests first:** `TestEncodeDecodeRoundTrip`, `TestPatchMaskReplacesMaps`, `TestQueryPaging`, `TestNotFoundIsErrNotFound`, `TestPermissionAndUnavailableTyped`, `TestFakeRejectsBadMask`.
- [ ] Commit: `firestore: a small REST client and its fake`

### Task 2 (M, lane B): `Rollup`

**Files:** `internal/budget/rollup.go`, tests.
- `Rollup` takes one day's `spend/<day>` subtree, that day's and the next day's `outcomes`, the ledgers of the day's runs, and per-run compute/hours/requester facts, and returns one `DayRecord` per repository. Run share sums per run give `byPerson`; crashed ledgers give `unreconciled`; `overrun` goes to the run's latest day share; unknown requester is `unknown`.
- [ ] **Failing tests first:** `TestRollupMatchesCounters`, `TestRolloverIdempotent` (same input, same record), `TestMidnightRunCharged`, `TestOauthIsNotionalOnly`, `TestUnreconciledFromCrashed`, `TestEmptyDayIsNoRecords`, `TestDayRecordFieldRoundTrip`, `TestHostileKeysEscaped`.
- [ ] Commit: `budget: the daily rollup record`

### Task 3 (M, lane A): Rollover **(critical: its own review)**

**Files:** `internal/budget/rollover.go`, `internal/cli/history.go`, tests.
- `Roller{DB, FS, Runs, Now}`: for each day in `[today-7, today-1]` not final: build records (T2 with compute from the bucket, H6), write (H4), mark `meta.archived`; prune per H5; `--day D` (a single day, any age still in RTDB), `--force` (rewrite a final day; only with `--day`); the existing project-mark check on RTDB and the Firestore `meta/installation` check before any write; exit 1 with the failing day named; summary line per day.
- [ ] **Failing tests first:** the seven names in Review Focus 1, plus `TestLookbackSevenDays`, `TestProvisionalThenFinal`, `TestRerunNoOp`, `TestHistoryNeedsExactlyOneMode`, `TestRolloverNeedsFirestoreEnv`.
- [ ] Commit: `history: --rollover moves finished days to Firestore`

### Task 4 (M, lane C): Terraform

**Files:** `deploy/terraform/gcp/modules/firebase/{apis,iam,outputs}.tf`, `modules/installation/{history,variables,outputs}.tf`, fixtures and goldens.
- `firestore.googleapis.com`, `firebaserules.googleapis.com` in the FP's APIs; `history` gets `roles/datastore.user`; people and admins get `roles/datastore.viewer` (member resources); the history account `roles/storage.objectViewer` on the runs bucket; the history job's args stay `--sweep` and `FUGARO_RUNS_BUCKET`/`FUGARO_FIRESTORE_DB` are added; `google_cloud_scheduler_job.history_rollover` (H7); no `google_firestore_database` (H8). `terraform validate` and the existing plan-fixture tests.
- [ ] **Failing tests first:** `TestFirebaseRootHasDatastoreGrants`, `TestHistoryNoDatastoreAdmin`, `TestRolloverSchedulerJob`, `TestNoFirestoreDatabaseResource`, `TestHistoryEnvHasBucket`.
- [ ] Commit: `terraform: Firestore roles, bucket read and the rollover schedule`

### Task 5 (M, lane C): `init --firebase` Firestore step **(critical: its own review)**

**Files:** `internal/infra/firestore.go`, `internal/cli/init_firebase.go`, tests against the fake.
- After the Firebase root's apply: `EnsureDatabase` (H8, own confirmation, shown in `--plan-only`), the deny-all ruleset and release, the `meta/installation` mark (project name; a mark of another project is refused as `checkDatabase` does for RTDB). The local config records nothing new (the FP id is there).
- [ ] **Failing tests first:** the six names in Review Focus 2, plus `TestMarkOfOtherProjectRefused`, `TestRerunIsNoOp`.
- [ ] Commit: `init --firebase: the Firestore database, its rules and mark`

### Task 6 (M, lane B): `fugaro report`

**Files:** `internal/cli/report.go`, `internal/report/*.go`, `internal/cli/root.go`, tests.
- H10 and H11: project and `--repo` selection as `budget show`; read-only; `Rollup` for non-final days; ISO weeks; table, `--json`, `--csv`; `oneLine`.
- [ ] **Failing tests first:** the names in Review Focus 3 and 4, plus `TestByDayWeekMonthYear`, `TestRangeDefaults`, `TestPartialMarked`, `TestDegradedFromRuns`, `TestNoHistoryDatabaseMessage`, `TestJSONHasProjectHeader`, `TestCSVMatchesTable`, `TestViewerRoleRefusalText`.
- [ ] Commit: `report: spend history by period, repository, model and person`

### Task 7 (S, lane C): Docs

Amend design §6.1/§6.2/§9 as this PR already does, v1 (command table, §10 history), README, `gcp-setup.md` (the Firestore step), `gcp-live-checklist.md` (checks 23-27).
- [ ] Commit: `docs: spend history and fugaro report`

## Live runbook (T8, controller; the user runs it later, M9d merges without it)

Every step is its own **⚠ CONFIRM**; record each `FACT` in the PR.
1. Read-only: `GET projects/fugaro-belong/locations` for Firestore; confirm `us-east5` is listed (**FACT**); else decide `nam5` (open question 1).
2. `fugaro init --firebase fugaro-belong --plan-only`: the Firestore step is shown, nothing applied; Terraform plan shows only APIs, three IAM members, the Scheduler job.
3. **⚠ CONFIRM — the real init** (creates the Firestore database: permanent location, billed at use; deny-all rules; mark): three confirmations plus the database one. Re-run `--plan-only`: no changes (the adoption proof).
4. **⚠ CONFIRM — rules proof:** an unauthenticated REST read of `spendDaily` is refused; a viewer's IAM read succeeds.
5. **⚠ CONFIRM — rebuild and push the history image** (as M9b), then run the job once by hand with `--rollover --day <a finished day>`: the provisional document appears; the second run changes only `writtenAt`.
6. After D+2 the Scheduler job writes `final:true` and prunes (watch Cloud Logging for the day's summary line); until then nothing is deleted. A forced check: `--rollover --day D --force` on the sandbox day, compare `fugaro report --since D --until D --json` to `fugaro budget show` figures from the day.
7. `fugaro report` for the real week; with a non-owner viewer identity; with `--by person`; with Firestore denied (degraded banner).
8. **Rollback:** delete the Scheduler job (Terraform) to stop the job; Firestore data stays (delete protection); RTDB data is only pruned after a read-back, so nothing is lost.

## Unverified assumptions

- A1. Firestore offers `us-east5` for a new native database (D4 says so; step 1 settles it).
- A2. `updateMask` field paths with backticked escaped keys replace map fields whole on REST `PATCH` (the fake enforces our reading; step 5 proves it).
- A3. The history account may read `result.json` objects in the runs bucket with `objectViewer` and no condition.
- A4. A Cloud Scheduler `:run` call accepts `overrides.containerOverrides[].args` for a job (verify in T4's plan and step 5).

## Risks

- **Pruning loses a day.** Only after final + read-back + 8 days (H5), one critical review, a `--force` that needs `--day`.
- **Wrong-region database** (H8): refused, never recreated; confirmation names the location.
- **Compute is an estimate** (`compute_estimated` false is `n/a`, not 0).
- **Requester addresses are stored in Firestore** (RTDB already holds them for 8 days): history keeps them indefinitely; IAM-only read (H9). Open question 3.

## Open questions for the user

1. **Firestore location.** Design D4 says `us-east5`. *Recommend:* keep it; step 1 verifies. *Default if unanswered:* `us-east5`; the ensure step refuses any other pre-existing location.
2. **Retention in RTDB: 8 days** (design). *Recommend:* keep. *Default:* 8.
3. **Per-person history.** `byPerson` keeps launcher addresses forever. *Recommend:* keep (the design asks for it). Alternative: store a hash. *Default:* addresses.
4. **Second Scheduler job** (H7) vs the 15-minute job. *Recommend:* second job at 00:30 UTC. *Default:* second job.
5. **`--csv`** is not in the design. *Recommend:* include (10 lines). *Default:* include.
6. **Org-wide export** stays "later, read-only". *Default:* not built.

## Execution

Subagent-driven. Lanes A (T1, T3), B (T2, T6), C (T4, T5, T7); T1/T2/T4 start together. Only **T3** and **T5** get their own reviews; everything else is reviewed once on the whole branch with the batch workflow's round-2 review. T8 is controller-only, one question per step, after merge.
