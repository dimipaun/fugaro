# Spend accuracy: implementation plan

**Status: plan only. Nothing is implemented, and no task is approved: each task needs the owner's explicit go.** This
plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). The answers that shaped it, all
given on 2026-11-15:

- D2640: settle on `usage.cost` plus the route fee.
- D2641: a report above the reservation settles in full, up to 10x.
- D2642: no body rewrite.
- D2643: the acceptance compares against invoice dollars, with the fee (not the recommendation).
- D2644: `highest` becomes the default `price_policy`.
- D2645: the dogfood override is deleted.
- D2646: `_per_m` keys in the local config, converted to multipliers.
- D2647: a new `fugaro budget reconcile` command.
- D2648: new day-counter leaves.
- D2649: the drift alarm fires at 10% over a rolling 24 hours, per model.

Task ids are `SA<slice>.<n>`. Checks 650-651 come from the block this plan reserved (650-659).

## Conventions every part follows

- `CLAUDE.md` and the rules files: `.claude/rules/ee.md` (SA5.2 is under `ee/`, so it gets the two-line notice, and
  nothing outside `ee/` imports it) and `.claude/rules/terraform.md` (not touched). Comments say why, never what.
- **Always:** targeted tests (`go test ./<pkg>/ -run 'TestX|TestY' -count=1`), `gofmt -l .` prints nothing,
  `go vet ./...`, and `go test ./internal/cli/ -run Docs -count=1` after any change to a document. A task's pull
  request names its `SA#` and the `D#` it implements.
- **Ask first:** a format change other than the additive fields named here; any change to `routedKeys` or to the
  gateway's no-rewrite rule (D2642); a new setting (D2649 chose a fixed threshold).
- **Never:** a secret-shaped literal in a test or fixture; a live cloud action by an agent (Checks 650-651 and OR1 are
  the owner's); a confirmation prompt (the no-prompt policy, `docs/no-prompt-policy/`); an import of `ee/` from the
  core; an edit to `docs/status.md` from a task's own pull request.
- **Update converges.** Each of SA5.0's rules generation and SA4.2's local-config migration ships inside `fugaro update`,
  with a test that an installation at the previous version converges and a run in flight keeps writing
  (`docs/design/update-in-flight.md`, `TestUpdateLeavesARunInFlightRunning`). Its `### For operators` line is "run
  `fugaro update`" plus what it does, in the next release's notes (`docs/releases/README.md`).
- **A field never changes meaning.** Every new field is additive: `cost.reported_by`, `cost.estimated_usd`,
  `cost.estimated_calls`, the `StageReport` fields and the day-counter leaves. `reported_usd` keeps its name and
  meaning; only its schema description changes (F18).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A report never settles a partial call (RS2) | SA2.1 `TestPartialCallIgnoresReport` |
| A report above 10x the reservation is malformed and gets the table charge plus a warning (D2641) | SA2.1 `TestReportAboveTenTimesReservationFallsBackToTable` |
| A report below the table is settled, not floored, and the drift alarm sees it (F17) | SA2.1 `TestReportBelowTableSettlesAtReport`, SA5.3 `TestDriftAlarmBothDirections` |
| A runner never writes a day-counter leaf its installation's rules lack (F10) | SA5.0 `TestNewLeavesNeedTheirGeneration`, SA5.4 |
| `update` converges from the previous rules generation and from an old `cache_read` key | SA5.0 and SA4.2 converge tests |
| The env and the policy node keep multipliers (D2646) | SA4.1 `TestPerMConvertsToMultiplier`, SA4.2 |

## Slices

1. **SA1, measure and unblock.** The deploy gate is fixed (RS1), runs record the report and the estimate per model,
   and `fugaro budget reconcile` reads them. It is useful alone: Check 650 runs on it.
2. **SA2, settle on the provider's figure (R2).** The gateway settles on the report; `diagnose` compares the settled
   charge with the estimate.
3. **SA3, the safer default (R3).** `highest` becomes the default.
4. **SA4, overrides that cannot be misread (R4).**
5. **SA5, correct reports and the drift alarm (R5, R6).**
6. **SA6, the documents.**

## Tasks

### SA1.0: the cost fields a run records per model
- Acceptance: `gateway.StageReport` has `ReportedBy map[string]pricing.Micros` (by serving model, beside `ByModel`),
  `EstimateBy map[string]pricing.Micros` (the table's charge for the same calls), `EstimatedCalls int` and
  `ModelTokens map[string]pricing.Usage` (per stage, keyed as `Ledger.ModelTokens` is). `runstore.Cost` has
  `ReportedBy map[string]float64` (`reported_by`), `EstimatedUSD float64` (`estimated_usd`) and `EstimatedCalls int`
  (`estimated_calls`), all `omitempty`. `schemas/result.schema.json` lists them, and the `reported_usd` description
  says what D2640 makes true. `docs/spend-accuracy/cost-fields.md` (new) says what each side may rely on. Nothing
  writes or reads the fields yet.
- Files: `internal/gateway/gateway.go`, `internal/runstore/cost.go`, `schemas/result.schema.json`,
  `schemas/schemas_test.go`, `docs/spend-accuracy/cost-fields.md` (new).
- Interfaces: produces the shapes SA1.2 fills and SA1.3, SA2.2 and SA5.1 read.
- Tests: `TestResultSchemaAcceptsRunnerRecords` (extended with a record that carries the three fields),
  `TestResultSchemaCostSpendFields` (new, in `schemas/schemas_test.go`).
- Command: `go test ./schemas/ ./internal/runstore/ -run 'TestResultSchema' -count=1`.
- Red step: `TestResultSchemaCostSpendFields` fails until the schema lists the new fields (the schema refuses unknown
  fields).
- Depends on: none.
- Commit: `spend-accuracy SA1.0: the cost fields a run records per model`.
- Reviewer tier: opus, with an independent review (`fugaro review --pr N`) before SA1.2, SA1.3, SA2.2 or SA5.1 starts.
  It is a wire format the console and history read.

### SA5.0: the per-model day-counter leaves and rules generation 6
- Acceptance: `rules.json.tmpl` grants `byModel/$model/notional`, `est` and `rep` exactly as `micros` is granted
  (AMOUNT, increase only, at most MAXRES per write); `budget.ModelUse` gains `Notional`, `Est` and `Rep`
  (`notional`, `est`, `rep`). This task settles F10: `projectpolicy.RulesVersion` goes from 5 to 6, with
  `TestRulesVersionUnchanged` and the goldens moved, and the writer writes the new leaves only when the session's
  deployed generation is at least 6. Otherwise it writes what it writes today. `update` deploys generation 6 (the
  existing rules stage), and a converge test starts from generation 5 with a run in flight.
  `docs/spend-accuracy/day-leaves.md` (new) says what each leaf holds, and that a day without them is read with
  `splitModel`.
- Files: `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/testdata/rules.golden.json`,
  `internal/budget/rules/rules_test.go`, `internal/budget/model.go`, `internal/projectpolicy/version.go`,
  `docs/spend-accuracy/day-leaves.md` (new).
- Interfaces: produces the leaves SA5.1 writes and SA5.2 and SA5.3 read.
- Tests: `TestRulesVersionUnchanged` (moved to 6), `TestNewLeavesNeedTheirGeneration` (new, emulator, `firebase`
  tag), the existing update converge tests.
- Command: `go test ./internal/budget/rules/ -run 'TestRulesVersionUnchanged|TestRulesGolden' -count=1`, and
  `go test -tags firebase ./internal/budget/rules/ -run TestNewLeavesNeedTheirGeneration -count=1`.
- Red step: the golden test fails until the template and the golden agree.
- Depends on: none.
- Commit: `spend-accuracy SA5.0: per-model notional, estimate and reported leaves, rules generation 6`.
- Reviewer tier: opus, with an independent review before SA5.1, SA5.2 or SA5.3 starts (Firebase rules).

### SA1.1: price_policy can be deployed (RS1)
- Acceptance: `catalogShipped` is `true` and `catalogSince` stays `0.7.5`; `fugaro update --repo` deploys a route
  that sets `price_policy: highest`; `modelsCatalogPriceNote` no longer says the switch is not accepted; the README
  OpenRouter section and `docs/release.md`'s `catalogSince` line say it is shipped.
- Files: `internal/cli/recipes_skew.go`, `internal/cli/models_add.go`, `README.md`, `docs/release.md`,
  `internal/cli/docs_providers_test.go`.
- Interfaces: none new.
- Tests: `TestReadmeOpenRouterSetup`, the `catalogSwitchSkew` tests in `internal/cli/` (`-run CatalogSwitch`).
- Command: `go test ./internal/cli/ -run 'TestReadmeOpenRouterSetup|CatalogSwitch|ModelsAdd' -count=1`.
- Red step: a new case "a route with price_policy highest on a 0.25 base image deploys" fails against `false`.
- Depends on: none.
- Commit: `pricing: price_policy and require_zdr deploy, as 0.7.5 shipped them (spend-accuracy SA1.1, F3)`.
- Reviewer tier: standard.

### SA1.2: the gateway records the report and the estimate per model
- Acceptance: each settled call adds its report to `ReportedBy[serving]` and its table charge (today's `amount`,
  which is the estimate) to `EstimateBy[serving]`. Calls with no report count in `EstimatedCalls`, and per-stage
  `ModelTokens` is filled. The runner sums them into `cost.reported_by`, `cost.estimated_usd` and
  `cost.estimated_calls`. `outcome` gains `estimate`, set in `fromUsage`; the charge itself does not change in this
  task.
- Files: `internal/gateway/forward.go`, `internal/gateway/ledger.go`, `internal/gateway/provider_settle_test.go`,
  `internal/runner/cost.go`, `internal/runner/cost_reported_test.go` (new).
- Interfaces: fills SA1.0.
- Tests: `TestStageReportCarriesReportedByModel`, `TestEstimateEqualsChargeBeforeSettleSwitch`,
  `TestRunRecordsReportedBy`.
- Command: `go test ./internal/gateway/ ./internal/runner/ -run 'ReportedBy|EstimateEquals' -count=1`.
- Red step: `TestStageReportCarriesReportedByModel` fails while `ReportedBy` stays empty.
- Depends on: SA1.0 (the fields).
- Commit: `gateway: record the provider's cost and the table's estimate per model (spend-accuracy SA1.2)`.
- Reviewer tier: opus (the gateway).

### SA1.3: `fugaro budget reconcile` (R1, D2647)
- Acceptance: `fugaro budget reconcile --from YYYY-MM-DD --to YYYY-MM-DD [--json]` lists the runs' `result.json` in
  the range and prints, per model, the recorded charge (`model_by`), the reported cost (`reported_by`, or the run's
  `reported_usd` under `mixed` when the run had several route models and no `reported_by`), the estimate, the tokens
  (`model_tokens`), `unattributed` as its own row, the share of runs with a report, and the service tiers seen. It
  prints totals with and without the route fee, so either of D2643's figures can be compared. It reads only and asks
  nothing.
- Files: `internal/cli/budget_reconcile.go` (new), `internal/cli/budget_reconcile_test.go` (new),
  `internal/cli/budget.go`.
- Interfaces: reads SA1.0's record fields; produces the `--json` shape Check 650 pastes.
- Tests: `TestBudgetReconcileByModel`, `TestBudgetReconcileOldRecordsMixed`, `TestBudgetReconcileReportShare`.
- Command: `go test ./internal/cli/ -run BudgetReconcile -count=1`.
- Red step: `TestBudgetReconcileByModel` fails on an unknown command.
- Depends on: SA1.0 (the record fields).
- Commit: `budget: reconcile recorded, reported and estimated spend over a date range (spend-accuracy SA1.3, R1)`.
- Reviewer tier: standard.

### SA2.1: settle on the provider's figure (R2, D2640, D2641, RS2, RS4)
- Acceptance: on a provider route, a complete call whose usage carries a cost settles at
  `WithFee(reported, fee)`, with `priced_as: reported`. A report above the reservation settles in full as an
  overrun; one above 10x the reservation gets the table charge, plus a warning and a violation-free log line. A
  partial call, or a call with no cost, settles as today and counts as estimated. The unpriced-tier floor becomes
  the estimated path's alone. `estimate` keeps the table charge in every case. Comments in `sse.go` and the
  `fromUsage` doc say what is now true.
- Files: `internal/gateway/forward.go`, `internal/gateway/sse.go`, `internal/gateway/log.go`,
  `internal/gateway/provider_settle_test.go`, `internal/gateway/provider_price_test.go`.
- Interfaces: consumes SA1.2's `estimate`.
- Tests: `TestSettleOnReport`, `TestReportBelowTableSettlesAtReport`, `TestReportAboveReservationIsOverrun`,
  `TestReportAboveTenTimesReservationFallsBackToTable`, `TestPartialCallIgnoresReport`, `TestNoReportIsEstimated`.
- Command: `go test ./internal/gateway/ -run 'SettleOnReport|ReportBelow|ReportAbove|PartialCallIgnores|NoReportIs' -count=1`.
- Red step: `TestSettleOnReport` fails while the table settles.
- Depends on: SA1.2 (same file, `forward.go`, and its `estimate`, which this task keeps while it changes `amount`).
- Commit: `gateway: settle a provider call on the cost the provider reported (spend-accuracy SA2.1, D2640)`.
- Reviewer tier: opus with an independent review (the gateway sets what the caps count; F17).

### SA2.2: diagnose compares the settled charge with the estimate (F16)
- Acceptance: `diagnose` reads `estimated_usd`, `estimated_calls` and `reported_by` when present, prints
  `Settled: $X (reported on N of M calls), table estimate $Y`, and warns when the two differ by more than
  `reportedGapPct` in either direction. A record without the fields keeps today's comparison and line.
  `--json` gains `estimate_usd` and `estimated_calls`; `reported_usd`, `charge_usd` and `reported_warning` keep their
  meaning.
- Files: `internal/cli/diagnose.go`, `internal/cli/diagnose_test.go`.
- Interfaces: reads SA1.0's record fields.
- Tests: `TestDiagnoseSettledAgainstEstimate`, `TestDiagnoseOldRecordUnchanged`.
- Command: `go test ./internal/cli/ -run 'TestDiagnose(SettledAgainstEstimate|OldRecordUnchanged|Route)' -count=1`.
- Red step: `TestDiagnoseSettledAgainstEstimate` fails on the missing line.
- Depends on: SA1.0 (the record fields).
- Commit: `diagnose: compare the settled charge with the table's estimate (spend-accuracy SA2.2)`.
- Reviewer tier: standard.

### SA3.1: `highest` is the default price policy (R3, D2644)
- Acceptance: `PricePolicySet` returns `highest` when unset; `CatalogRoute.Policy` `""` means `highest`;
  `cheapest` stays selectable; `fugaro budget prices` and `models add` name the end charged; an estimated settle and
  every reservation on a catalog-priced route use the tier's maximum by default.
- Files: `internal/config/providers.go`, `internal/config/providers_test.go`, `internal/pricing/catalog_apply.go`,
  `internal/pricing/catalog_apply_test.go`, `internal/cli/models_add.go`.
- Interfaces: none new.
- Tests: `TestPricePolicyDefaultsHighest`, `TestApplyCatalogUnsetPolicyIsHighest`, the `ModelsAdd` note tests.
- Command: `go test ./internal/config/ ./internal/pricing/ ./internal/cli/ -run 'PricePolicy|ApplyCatalog|ModelsAdd' -count=1`.
- Red step: `TestPricePolicyDefaultsHighest` fails against `cheapest`.
- Depends on: SA1.1 (both edit `models_add.go`, and a default no owner could deploy against would be untestable
  end to end).
- Commit: `pricing: highest is the default price_policy (spend-accuracy SA3.1, D2644, supersedes ruling 3)`.
- Reviewer tier: standard.

### SA4.1: model_prices in dollars per million (R4, D2646)
- Acceptance: the local config accepts `cache_read_per_m`, `cache_write_5m_per_m` and `cache_write_1h_per_m` in
  $/M, and `cache_read_mult`, `cache_write_5m_mult` and `cache_write_1h_mult` as multipliers. Two spellings of one
  field are a validation error. The old `cache_read`/`cache_write_5m`/`cache_write_1h` are read as multipliers, with
  a warning naming the new key. Conversion to `pricing.Rates` happens at load, so the node and `FUGARO_MODEL_PRICES`
  are unchanged. `pricing.UndercutsCatalog(model, override, table)` (new) names each dimension below the route's
  tier minimum.
- Files: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`, `internal/pricing/undercut.go`
  (new), `internal/pricing/undercut_test.go` (new).
- Interfaces: produces the new keys and `UndercutsCatalog` for SA4.2.
- Tests: `TestPerMConvertsToMultiplier`, `TestTwoSpellingsRefused`, `TestOldCacheReadWarns`,
  `TestUndercutsCatalog`.
- Command: `go test ./internal/localcfg/ ./internal/pricing/ -run 'PerM|TwoSpellings|OldCacheRead|Undercuts' -count=1`.
- Red step: `TestPerMConvertsToMultiplier` fails on an unknown field.
- Depends on: none.
- Commit: `localcfg: cache prices in dollars per million, multipliers by name (spend-accuracy SA4.1, D2646)`.
- Reviewer tier: standard.

### SA4.2: update migrates the keys and reports undercuts (R4, RS3)
- Acceptance: `fugaro update` rewrites an old `cache_read`/`cache_write_*` key in the local config to its `_mult`
  name in place (same value, one line printed, nothing asked), and is idempotent. `update` and `validate` print one
  warning per override dimension below the route's tier minimum, naming both prices, and never refuse. A converge
  test starts from a config with `cache_read: 0.0042`.
- Files: `internal/cli/update_model_prices.go` (new), `internal/cli/update_model_prices_test.go` (new),
  `internal/cli/update.go`, `internal/cli/validate.go`.
- Interfaces: consumes SA4.1's keys and `UndercutsCatalog`.
- Tests: `TestUpdateMigratesCacheReadKey`, `TestUpdateMigrationIdempotent`, `TestValidateWarnsUndercut`.
- Command: `go test ./internal/cli/ -run 'UpdateMigratesCacheRead|UpdateMigrationIdempotent|ValidateWarnsUndercut' -count=1`.
- Red step: `TestUpdateMigratesCacheReadKey` fails while the file is unchanged.
- Depends on: SA4.1 (the migration writes keys only SA4.1's parser accepts: behaviour).
- Commit: `update: migrate model_prices cache keys and warn on undercuts (spend-accuracy SA4.2)`.
- Reviewer tier: standard. Release note: `### For operators`: run `fugaro update`; it renames `cache_read` to
  `cache_read_mult` and warns about any undercut.

### SA5.1: the budget writes per-model tokens, estimate, report and notional (R5, D2648)
- Acceptance: `Session.report` writes the gateway's per-model tokens to `in/out/cr/cw`, and, when the deployed
  generation is at least 6, `est` and `rep` per model. `AddNotional` and tier-A spend write their micros to
  `notional` as well as `micros` (which stays the whole), so a reader can tell billed (`micros - notional`) from
  notional. Under generation 5 it writes exactly what it writes today.
- Files: `internal/budget/lease.go`, `internal/budget/session_test.go`, `internal/budget/by_tier_test.go`.
- Interfaces: reads SA1.0's `StageReport` fields; writes SA5.0's leaves.
- Tests: `TestReportWritesModelTokens`, `TestReportWritesEstAndRep`, `TestNotionalLeaf`,
  `TestGeneration5WritesTodaysLeaves`.
- Command: `go test ./internal/budget/ -run 'ReportWritesModelTokens|ReportWritesEstAndRep|NotionalLeaf|Generation5' -count=1`.
- Red step: `TestReportWritesModelTokens` fails on zero tokens.
- Depends on: SA1.0 (the report fields), SA5.0 (the leaves).
- Commit: `budget: per-model tokens, estimate, report and notional in the day counters (spend-accuracy SA5.1)`.
- Reviewer tier: opus (a budget write path).

### SA5.2: report keeps billed dollars billed (R5)
- Acceptance: `--by model` reads `notional` when the day has it (billed = `micros - notional`) and calls
  `splitModel` only for a day without it. Gateway models show their tokens. `MixedModelDays` is set only for days
  that still needed `splitModel`.
- Files: `ee/report/report.go`, `ee/report/report_test.go`, `ee/report/render.go`.
- Interfaces: reads SA5.0's leaves through `budget.ModelUse`.
- Tests: `TestByModelUsesNotionalLeaf`, `TestByModelOldDaySplits`.
- Command: `go test ./ee/report/ -run 'ByModel' -count=1`.
- Red step: `TestByModelUsesNotionalLeaf` fails while `splitModel` proportions a DeepSeek row.
- Depends on: SA5.0 (the leaves).
- Commit: `report: billed gateway dollars stay billed, and gateway tokens show (spend-accuracy SA5.2, R5)`.
- Reviewer tier: standard.

### SA5.3: the drift alarm (R6, D2649)
- Acceptance: a `doctor` check reads today's and yesterday's counters. Per model with at least $1 settled in the last
  24 hours, it fails when `rep` (or the settled billed figure) and `est` differ by more than 10% either way, and
  names the model and the factor. `watch` prints the same line once per model per day. `doctor --json` carries it as
  an ordinary check.
- Files: `internal/cli/doctor_spend_drift.go` (new), `internal/cli/doctor_spend_drift_test.go` (new),
  `internal/cli/doctor.go`, `internal/cli/watch.go`.
- Interfaces: reads SA5.0's leaves.
- Tests: `TestDriftAlarmBothDirections`, `TestDriftAlarmBelowOneDollarQuiet`, `TestWatchDriftLineOnce`.
- Command: `go test ./internal/cli/ -run 'DriftAlarm|WatchDriftLine' -count=1`.
- Red step: `TestDriftAlarmBothDirections` fails on a missing check.
- Depends on: SA5.0 (the leaves).
- Commit: `doctor, watch: say when settled spend drifts from the table's estimate (spend-accuracy SA5.3, R6)`.
- Reviewer tier: standard.

### SA1.4: the cost fields end to end
- Acceptance: a runner test drives a stage through the fake upstream with `usage.cost` on some calls and not others.
  The run's `result.json` carries `reported_by`, `estimated_usd` and `estimated_calls`; `budget reconcile` and
  `diagnose` read that record and print the same figures; the budget session receives the per-model fields.
- Files: `internal/runner/spend_accuracy_e2e_test.go` (new), `internal/cli/budget_reconcile_e2e_test.go` (new).
- Interfaces: SA1.0, SA1.2, SA1.3, SA2.2, SA5.1.
- Tests: `TestSpendFieldsEndToEnd`, `TestReconcileReadsARealRecord`.
- Command: `go test ./internal/runner/ ./internal/cli/ -run 'SpendFieldsEndToEnd|ReconcileReadsARealRecord' -count=1`.
- Red step: written first against the merged sides; it fails if a field is dropped on the way.
- Depends on: SA1.2, SA1.3, SA2.2, SA5.1 (it proves their wiring).
- Commit: `spend-accuracy SA1.4: the cost fields from the gateway to reconcile and diagnose`.
- Reviewer tier: standard.

### SA5.4: the day-counter leaves end to end
- Acceptance: an emulator test runs a session under generation 6 that reports a gateway stage and a notional stage.
  The new leaves land, `doctor`'s drift check reads them, and the same session under generation 5 writes only
  today's leaves and its update is not refused.
- Files: `internal/budget/rules/spend_leaves_emulator_test.go` (new).
- Interfaces: SA5.0, SA5.1, SA5.3 (SA5.2 is under `ee/`, which a core test cannot import; its unit tests pin the read).
- Tests: `TestSpendLeavesEndToEnd`.
- Command: `go test -tags firebase ./internal/budget/rules/ -run TestSpendLeavesEndToEnd -count=1`.
- Red step: fails if the writer writes a leaf under generation 5.
- Depends on: SA5.1, SA5.3 (it proves their wiring).
- Commit: `spend-accuracy SA5.4: the day-counter leaves from the session to doctor`.
- Reviewer tier: opus (rules).

### SA6.1: the documents
- Acceptance: `docs/multi-model.md` §2 and §6 say `highest` is the default and a call settles on the provider's cost;
  `docs/design/provider-price-catalog.md` D6 and §0 item 3 record that D2644 supersedes ruling 3; the "Real vs
  notional dollars" paragraph of `docs/design/m10-multi-model.md` and its Phase 3 line point here;
  `docs/design/v1.md`'s gateway paragraph says what a report settles. A docs test pins the four sentences.
- Files: `docs/multi-model.md`, `docs/design/provider-price-catalog.md`, `docs/design/m10-multi-model.md`,
  `docs/design/v1.md`, `internal/cli/docs_spend_accuracy_test.go` (new).
- Interfaces: describes SA2.1, SA3.1, SA4.2 and SA5.3 as merged.
- Tests: `TestDocsSpendAccuracy`, and every existing docs test (`-run Docs`).
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: `TestDocsSpendAccuracy` fails on the old `cheapest stays the default` sentence.
- Depends on: SA2.1, SA3.1, SA4.2, SA5.3 (it documents their behaviour, and existing docs tests pin code strings
  they change).
- Commit: `docs: spend settles on the provider's cost, highest by default (spend-accuracy SA6.1)`.
- Reviewer tier: standard.

## Parallel execution

**Levels** (tasks in one level run at once):

| Level | Tasks |
| --- | --- |
| L0 | SA1.0, SA5.0, SA1.1, SA4.1 |
| L1 | SA1.2, SA1.3, SA2.2, SA3.1, SA4.2, SA5.1, SA5.2, SA5.3 |
| L2 | SA2.1, SA1.4, SA5.4 |
| L3 | SA6.1 |

**Critical path:** SA1.0 → SA1.2 → SA2.1 → SA6.1 (weights 0.5 + 3 + 3 + 2 = 8.5). It is sequential because SA2.1
changes the same `fromUsage` that SA1.2 instruments, and the documents describe what merged.

**Edges kept**, each with its reason:
- SA2.1 needs SA1.2: same file (`forward.go`), and SA2.1 keeps SA1.2's `estimate` while it changes the charge.
- SA3.1 needs SA1.1: same file (`models_add.go`), and a default that `update --repo` refuses cannot be tested end to
  end.
- SA4.2 needs SA4.1: the migration writes keys only SA4.1's parser accepts (behaviour).
- SA6.1 needs SA2.1, SA3.1, SA4.2 and SA5.3: it documents their behaviour, and docs tests pin strings they change.
- SA1.4 needs SA1.2, SA1.3, SA2.2 and SA5.1, and SA5.4 needs SA5.1 and SA5.3: each proves their wiring.

**The contract-first pass** (`planning.decoupling` is on for this repository; its rigor is `startup`, so the pass
decouples aggressively). Five edges carried an interface only: SA1.3, SA2.2 and SA5.1 waited on SA1.2 for the record
and stage-report fields, and SA5.2 and SA5.3 waited on SA5.1 for the leaves' shape. The pass first drew them as
contracts C1 and C2, with integration tasks. `fugaro plan graph` refused both: the side that fills a contract (SA1.2,
SA5.1) builds against its types, and the read does not accept a provider waiting on its own contract. C1 also lost on
weight: at 0.5 it sat in front of the critical chain, so the path went from 8 to 8.5. Instead, each shape is now the
first real task of its slice, SA1.0 and SA5.0. Neither is a stub: the fields and the rules generation ship as
themselves. Writers and readers both wait on them, and nobody waits on SA1.2 or SA5.1 for a shape. The graph records
no decoupling (`decoupling: []`), and SA1.4 and SA5.4 stay as ordinary end-to-end tasks. Without the split, the
readers would sit a level later, behind SA1.2 (weight 3) and SA5.1 (weight 2). With it, eight tasks share L1.

**Shared files** (no level holds two writers of one file):

| File | Writers, by level |
| --- | --- |
| `internal/gateway/forward.go` | SA1.2 (L1), SA2.1 (L2) |
| `internal/gateway/provider_settle_test.go` | SA1.2 (L1), SA2.1 (L2) |
| `internal/gateway/gateway.go` | SA1.0 (L0) |
| `internal/cli/models_add.go` | SA1.1 (L0), SA3.1 (L1) |
| `internal/cli/doctor.go` | SA5.3 (L1) |
| `internal/budget/lease.go` | SA5.1 (L1) |
| `internal/runstore/cost.go`, `schemas/result.schema.json` | SA1.0 (L0) |
| `internal/budget/rules/*` | SA5.0 (L0), SA5.4 (L2, a new file only) |
| `docs/multi-model.md`, `docs/design/*.md` | SA6.1 (L3) |

**If a shape task turns out wrong:** a task that finds SA1.0's or SA5.0's shape wrong amends it in a small task of its
own rather than working around it. For example, SA1.2 might find that per-stage `ModelTokens` needs the serving model's
key, which would reach SA5.1 and SA1.4; or SA5.1 might find that the generation check belongs in the session's config
read, which would reach SA5.3 and SA5.4.

## Checks

- **Check 650 (owner-run), the reconciliation.** Run after SA1.3 merges and its release is installed. The owner types
  `fugaro budget reconcile --from 2026-10-12 --to 2026-11-16 --json` on the dogfood project and pastes the output beside
  the OpenRouter invoice for the key. Record:
  1. the share of runs with `reported_usd` (U1, O1);
  2. per model, the charge against the report, which settles U5;
  3. one call's tokens and `reported_micros` from its `model call` log line, against the endpoint's price, which
     settles U2.

  This output is the one-time reconciliation note (A5).
- **Check 651 (owner-run), the acceptance.** Run a week after the release that carries SA2.1 and SA3.1. The owner
  compares `fugaro budget reconcile` totals with the fee against the OpenRouter invoice in dollars for that week
  (D2643), and records the catalog's `checked_at` (F11). The target is within ±3%. The owner also runs one task with
  `--budget` set below its expected cost, and records that it halts and that `reported_usd` is within one call of
  the cap.

## Owner-run steps

- **OR1 (D2645), now, ahead of the plan.** Delete `model_prices.deepseek/deepseek-v4.1-flash` from
  `~/.config/fugaro/projects/fugaro.yaml`, then run `fugaro update` in your own terminal, which republishes the policy.
  Until SA3.1 ships, the catalog then prices the model at `cheapest`. On 2026-11-22 the catalog goes stale (F11) and
  prices it at the tier's maximum.

## After deploy

- SA5.0 and SA4.2 each add a `### For operators` line to the next release's notes: run `fugaro update`. It deploys rules
  generation 6, and renames a `cache_read` key to `cache_read_mult`. Nothing else.

## Considered and declined

- Rewriting a routed request to ask for usage accounting (D2642).
- A configurable drift threshold (D2649).
- Pricing the endpoint that served the call (D2644; U3).
- Repricing past spend (spec, out of scope; Check 650 is the note).
- A console change for the drift alarm (U7: a follow-up only if the doctor view does not show a new check).
