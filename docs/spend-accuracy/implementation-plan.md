# Spend accuracy: implementation plan

**Status: plan only. Nothing is implemented, and no task is approved: each task needs the owner's explicit go.** This
plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). The answers that shaped it, all
given on 2026-11-15:

- D2640: settle on `usage.cost` plus the route fee, floored at the tier's cheapest end (D2651).
- D2641: a report above the reservation settles in full, up to 10x.
- D2642: no body rewrite.
- D2643: the acceptance compares against invoice dollars, with the fee (not the recommendation).
- D2644: `highest` becomes the default `price_policy`.
- D2645: the dogfood override is deleted.
- D2646: `_per_m` keys in the owner's local file only; every published copy keeps the multipliers.
- D2647: a new `fugaro budget reconcile` command.
- D2648 with D2650: new day-counter leaves `notional`, `rep`, `tlo` and `thi`.
- D2649 with D2650: an alarm when the report falls outside the tier's band by more than 10%.

Task ids are `SA<slice>.<n>`. Checks 650-651 come from the block this plan reserved (650-659).

## Conventions every part follows

- `CLAUDE.md` and the rules files. `.claude/rules/ee.md` applies to SA5.2: the two-line notice, no core import of
  `ee/`, and `go test ./internal/eeboundary/ -count=1`. `.claude/rules/website.md` applies to SA1.3, whose command
  enters the CLI reference: run `npm run check` in `website/`. Comments say why, never what.
- **Always:** targeted tests (`go test ./<pkg>/ -run 'TestX|TestY' -count=1`), `gofmt -l .` prints nothing,
  `go vet ./...`. **Each task keeps the docs tests green in its own pull request (RS6):** every task also runs
  `go test ./internal/cli/ -run Docs -count=1`, whether or not its Command repeats it, and change a pinned sentence that the task makes false in the same pull
  request. A task's pull request names its `SA#` and the `D#` it implements.
- **Ask first:** a format change other than the additive fields named here; any change to `routedKeys` or to the
  gateway's no-rewrite rule (D2642); a new setting (D2649 chose a fixed threshold).
- **Never:** a secret-shaped literal in a test or fixture; a live cloud action by an agent (Checks 650-651 and OR1 are
  the owner's); a confirmation prompt (`docs/no-prompt-policy/`); an import of `ee/` from the core; an edit to
  `docs/status.md` from a task's own pull request.
- **Update converges.** SA5.0's rules generation and SA4.2's local-config migration ship inside `fugaro update`. Each
  has a test that an installation at the previous version converges and that a run in flight keeps writing
  (`docs/design/update-in-flight.md`, `TestUpdateLeavesARunInFlightRunning`). Their `### For operators` line is "run
  `fugaro update`" plus what it does, in the next release's notes (`docs/releases/README.md`).
- **A field never changes meaning.** The new fields are additive and avoid the existing `cost.estimate` (RS5):
  - `cost.reported_by`, `cost.table_lo_usd`, `cost.table_hi_usd` and `cost.unreported_calls`;
  - the new `StageReport` and `Ledger` fields;
  - the day-counter leaves.

  `reported_usd` and `model_tokens` keep their meaning. An absent `price_policy` does change meaning, by the owner's
  decision; F28 states its mixed-version window.
- **The size bound.** Four tasks run past five files, because the tests they break cannot be split off without
  leaving CI red: SA1.1 (a gate's flip, which several tests pin), SA1.3 (a new command, which the CLI's census tests
  enrol), SA3.1 (a default, which many tests pin) and SA5.0 (a rules generation, pinned outside the rules package).
  Each says so.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A partial call never settles on a report (RS2) | SA2.1 `TestPartialCallIgnoresReport` |
| A report above 10x the reservation is malformed and gets the table charge plus a warning (D2641) | SA2.1 `TestReportAboveTenTimesReservationFallsBackToTable` |
| A zero or low report is floored at the tier's cheapest end; a provider route keeps the largest cost (D2651, F22) | SA2.1 `TestLowReportFlooredAtTierMinimum`, `TestProviderCostKeepsLargest` |
| A runner never writes a day-counter leaf its installation's rules lack (F10) | SA5.0 `TestNewLeavesNeedTheirGeneration`, SA5.1 `TestGeneration5WritesTodaysLeaves`, SA5.4 |
| Billed and notional writes for one model in one flush are summed per leaf (F26) | SA5.1 `TestOneFlushSumsBilledAndNotional` |
| A reader never prints negative billed dollars (`micros − notional`) | SA5.2 `TestByModelClampsBilledAtZero` |
| The drift alarm measures against the band, over reported calls, without the fee (D2650) | SA5.3 `TestDriftAlarmOutsideBandOnly` |
| The env, the policy node and the shared copy keep the multipliers under the old names (D2646 as amended) | SA4.1 `TestPerMConvertsToMultiplier`, `TestSharedKeepsOldSpelling` |
| A call priced `max` takes its band and floor from the pin, or none (RS7) | SA1.2 `TestBandFromPinWhenServingUnknown`, SA2.1 `TestMaxPricedCallFloorFromPin` |
| A gateway-only model on a mixed day keeps its billed dollars billed (RS8) | SA5.2 `TestByModelGatewayOnlyOnMixedDay` |
| Every new leaf write is chunked at MAXRES, as `micros` is | SA5.1 `TestNewLeavesChunkedAtMaxRes` |

## Slices

1. **SA0, the checks.** Checks 650-651 are registered in the checklist.
2. **SA1, measure and unblock.** The deploy gate is fixed (RS1), runs record the report and the band per model, and
   `fugaro budget reconcile` reads them. This slice is useful alone: Check 650 runs on it.
3. **SA2, settle on the provider's figure (R2).**
4. **SA3, the safer default (R3).**
5. **SA4, overrides that cannot be misread (R4).**
6. **SA5, correct reports and the drift alarm (R5, R6).**
7. **SA6, the narrative documents.**

## Tasks

### SA0.1: Checks 650 and 651 in the live checklist
- Acceptance: `docs/gcp-live-checklist.md` has Checks 650 and 651 as this plan's Checks section words them. Each is
  marked owner-run, with the command typed, what is pasted and what passes.
- Files: `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: every docs test (`-run Docs`); the checklist's numbering tests are among them.
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: none (documents only); the docs tests must stay green.
- Depends on: SA5.0 (the same file, `docs/gcp-live-checklist.md`, which SA1.1 and SA5.0 write before it).
- Commit: `checks: 650 and 651, spend against the OpenRouter invoice (spend-accuracy SA0.1)`.
- Reviewer tier: standard.

### SA1.0: the cost fields a run records per model
- Acceptance:
  - `gateway.StageReport` gains `ReportedBy`, `TableLoBy`, `TableHiBy` and `NotionalBy` (each
    `map[string]pricing.Micros`, keyed by serving model), `UnreportedCalls int`, and `TokensBy map[string]pricing.Usage`
    (keyed by serving model, F25).
  - `gateway.Ledger` gains `ReportedBy`, `TableLoBy`, `TableHiBy` and `UnreportedCalls`, which include late-settled
    calls (F24).
  - `runstore.Cost` gains `ReportedBy map[string]float64` (`reported_by`), `TableLoUSD` (`table_lo_usd`), `TableHiUSD`
    (`table_hi_usd`) and `UnreportedCalls` (`unreported_calls`), all `omitempty`.
  - `schemas/result.schema.json` lists them. `reported_usd`'s description is left alone here; SA2.3 changes it once
    D2640 is true.
  - `docs/spend-accuracy/cost-fields.md` (new) says what each field holds: the band is over reported calls only, with
    no fee.
  - Nothing writes or reads the fields yet.
- Files: `internal/gateway/gateway.go`, `internal/runstore/cost.go`, `schemas/result.schema.json`,
  `schemas/schemas_test.go`, `docs/spend-accuracy/cost-fields.md` (new).
- Interfaces: produces the shapes that SA1.2 and SA1.5 fill, and that SA1.3, SA2.2 and SA5.1 read.
- Tests: `TestResultSchemaAcceptsRunnerRecords` (extended), `TestResultSchemaCostSpendFields` (new).
- Command: `go test ./schemas/ ./internal/runstore/ ./internal/gateway/ -run 'TestResultSchema|TestStageReport' -count=1`.
- Red step: `TestResultSchemaCostSpendFields` fails until the schema lists the fields (the schema refuses unknown
  fields).
- Depends on: none.
- Commit: `spend-accuracy SA1.0: the cost fields a run records per model`.
- Reviewer tier: opus, with an independent review (`fugaro review --pr N`) before any reader or writer starts. It is a
  wire format that the console and history read.

### SA1.6: the table knows both ends of a tier (F31)
- Acceptance:
  - `pricing.Model` gains `Lo` and `Hi` (`Rates`): the selected tier's cheapest and dearest end, per dimension, cache
    included.
  - `catalogModel` sets both from the catalog's `min` and `max`, folded as the charged bound is. A row that is not
    catalog-priced (an override, the embedded Claude rows) has `Lo = Hi = Rates`.
  - The charged `Rates` do not change.
- Files: `internal/pricing/pricing.go`, `internal/pricing/catalog_apply.go`, `internal/pricing/catalog_apply_test.go`,
  `internal/pricing/pricing_test.go`.
- Interfaces: produces the two ends that SA1.2 and SA2.1 read.
- Tests: `TestCatalogRowCarriesBothEnds`, `TestOverrideRowEndsEqualRates`.
- Command: `go test ./internal/pricing/ -run 'BothEnds|EndsEqualRates|ApplyCatalog' -count=1`.
- Red step: `TestCatalogRowCarriesBothEnds` fails on a zero `Lo`.
- Depends on: none.
- Commit: `pricing: a catalog row carries both ends of its tier (spend-accuracy SA1.6, F31)`.
- Reviewer tier: standard.

### SA1.1: price_policy can be deployed (RS1)
- Acceptance:
  - `catalogShipped` is `true`; `catalogSince` stays `0.7.5`.
  - `fugaro update --repo` and `fugaro setup --repo` deploy a route that sets `price_policy: highest`.
  - `modelsCatalogPriceNote` no longer says the switch is not accepted.
  - The README OpenRouter section, `docs/release.md`'s `catalogSince` bullet, `docs/multi-model.md`'s "neither key is
    accepted by `fugaro update --repo` yet" sentence and `docs/gcp-live-checklist.md`'s Check 44 line all say the keys
    are shipped.
  - Every test pinning the old gate moves with it.
  - Past five files by design (Conventions).
- Files: `internal/cli/recipes_skew.go`, `internal/cli/models_add.go`, `README.md`, `docs/release.md`,
  `docs/multi-model.md`, `docs/gcp-live-checklist.md`, `internal/cli/docs_providers_test.go`, `internal/cli/project_cmd_test.go`, `internal/cli/init_repo_gate_test.go`,
  `internal/cli/models_add_test.go`, `internal/cli/docs_compute_backend_test.go`, `internal/cli/recipes_skew_test.go`.
- Interfaces: none new.
- Tests: `TestRefuseCatalogSwitches`, `TestReadmeOpenRouterSetup`, `TestImageCatalogGateIsNotThePriceCatalogGate`,
  `TestInitRepoRefusesCatalogSwitches` (renamed to say what is now true),
  `TestModelsAddExplainsTheSpreadAndWritesNoSwitch` (its D795 text), `TestDocsReleaseChecklistNamesEveryShippedGate`,
  and the `catalogSwitchSkew` tests.
- Command: `go test ./internal/cli/ -run 'TestRefuseCatalogSwitches|TestReadmeOpenRouterSetup|ImageCatalogGate|InitRepoRefusesCatalogSwitches|CatalogSwitch|ModelsAdd|Docs' -count=1`.
- Red step: a new case, "a route with price_policy highest on a 0.25 base image deploys", fails against `false`.
- Depends on: none.
- Commit: `pricing: price_policy and require_zdr deploy, as 0.7.5 shipped them (spend-accuracy SA1.1, F3)`.
- Reviewer tier: standard.

### SA1.2: the gateway records the report and the band per model
- Acceptance:
  - Each settled call adds to the per-model figures in the stage report and the run ledger:
    - its report (no fee) to `ReportedBy`, and the table's charge at the row's `Lo` and `Hi` (SA1.6; no fee) to
      `TableLoBy`/`TableHiBy`, both only when a report came back, with a call priced `max` taking the pin's row or
      being left out (RS7);
    - `NotionalBy` for tier-A calls;
    - `TokensBy`.
  - A call with no report counts in `UnreportedCalls`. A model the catalog does not price has equal ends.
  - Late-settled calls reach the ledger figures (F24).
  - The charge itself does not change in this task.
- Files: `internal/gateway/forward.go`, `internal/gateway/ledger.go`, `internal/gateway/gateway.go` (the `Server`'s
  run-level figures and `Ledger()`), `internal/gateway/ledger_test.go`, `internal/gateway/provider_settle_test.go`.
- Interfaces: fills SA1.0's gateway fields; reads SA1.6's ends.
- Tests: `TestStageReportCarriesReportedByModel`, `TestTableBandOverReportedCallsOnly`, `TestNotionalByModel`,
  `TestLateSettleReachesLedgerBand`, `TestTokensByServingModel`, `TestBandFromPinWhenServingUnknown`.
- Command: `go test ./internal/gateway/ -run 'ReportedByModel|TableBand|NotionalByModel|LateSettle|TokensByServing|BandFromPin' -count=1`.
- Red step: `TestStageReportCarriesReportedByModel` fails while `ReportedBy` stays empty.
- Depends on: SA1.0 (the fields), SA1.6 (the ends).
- Commit: `gateway: record the provider's cost and the table's band per model (spend-accuracy SA1.2)`.
- Reviewer tier: opus (the gateway).

### SA1.5: the runner records them in result.json
- Acceptance: the runner takes `cost.reported_by`, `cost.table_lo_usd`, `cost.table_hi_usd` and
  `cost.unreported_calls` from the run `Ledger` alone (F24), which holds every call, late ones included. It never adds
  the stage reports to them, so no call is counted twice.
- Files: `internal/runner/gateway.go`, `internal/runner/runner.go`, `internal/runner/cost.go`,
  `internal/runner/cost_reported_test.go` (new).
- Interfaces: reads SA1.0's gateway fields; writes SA1.0's record fields.
- Tests: `TestRunRecordsReportedBy`, `TestRunRecordsBandAndUnreported`, `TestLateCallCountedOnce`.
- Command: `go test ./internal/runner/ -run 'RunRecordsReportedBy|RunRecordsBand' -count=1`.
- Red step: `TestRunRecordsReportedBy` fails on an empty `reported_by`.
- Depends on: SA1.0 (the fields; its tests hand-build stage reports).
- Commit: `runner: write the per-model report and band into result.json (spend-accuracy SA1.5)`.
- Reviewer tier: standard.

### SA1.3: `fugaro budget reconcile` (R1, D2647)
- Acceptance:
  - `fugaro budget reconcile --from YYYY-MM-DD --to YYYY-MM-DD (--repo owner/name | --all) [--json]` reads the window's run records through the
    existing reader that `fugaro report` uses (`Cloud.LsSlugs`, `Cloud.ReadRunRecords` with its run bound,
    `ApplyCost`, all in `internal/cli/api.go`). It says so when the bound truncated the window.
  - It prints, per model:
    - the charge (`model_by`);
    - the report (`reported_by`; for an old record with several route models, the run's `reported_usd` under
      `mixed`);
    - the band;
    - the tokens (`model_tokens`);
    - the share of runs carrying a report;
    - the service tiers seen.
  - `unattributed` gets a row of its own, and the totals are printed with and without the route fee.
  - It reads only, and asks nothing.
  - It is on the CLI reference, enrolled in the reporter census (`exemptListings` in `report_exempt.go`), has a `--json`
    fixture, runs with a closed stdin (a `closedStdinFixtures` entry), and accepts `--verbose`.
  - Past five files by design (Conventions).
- Files: `internal/cli/budget_reconcile.go` (new), `internal/cli/budget_reconcile_test.go` (new),
  `internal/cli/budget.go`, `website/content/cli-reference.md`, `internal/cli/report_exempt.go`,
  `internal/cli/report_json_fixtures_test.go`, `internal/cli/noprompt_harness_test.go`.
- Interfaces: reads SA1.0's record fields; produces the `--json` that Check 650 pastes.
- Tests: `TestBudgetReconcileByModel`, `TestBudgetReconcileOldRecordsMixed`, `TestBudgetReconcileReportShare`,
  `TestBudgetReconcileTruncated`, `TestCliReferenceNamesEveryCommandAndFlag`,
  `TestEveryCommandIsOnTheReporterOrExempt`, `TestEveryCommandRunsWithClosedStdin`.
- Command: `go test ./internal/cli/ -run 'BudgetReconcile|CliReferenceNamesEveryCommand|EveryCommandIsOnTheReporter|EveryCommandRunsWithClosedStdin|EveryCommandAcceptsVerbose|JSONFixtureCoverage' -count=1`,
  then `npm run check` in `website/`.
- Red step: `TestBudgetReconcileByModel` fails on an unknown command.
- Depends on: SA1.0 (the record fields; its tests use hand-written records).
- Commit: `budget: reconcile recorded, reported and table spend over a date range (spend-accuracy SA1.3, R1)`.
- Reviewer tier: standard.

### SA1.4: the cost fields end to end
- Acceptance:
  - A runner test drives a stage through the fake upstream, with `usage.cost` on some calls and not on others, under
    SA2.1's settle rule.
  - The run's `result.json` carries the four fields, and `model_usd` is the reported-plus-fee figure.
  - The runner test writes the record to `internal/runner/testdata/spend_accuracy_result.golden.json` (with its
    `-update` flag), and the CLI test reads that same file through `budget reconcile` and `diagnose` and checks both
    print the same figures.
  - The budget session receives the per-model fields.
- Files: `internal/runner/spend_accuracy_e2e_test.go` (new), `internal/runner/testdata/spend_accuracy_result.golden.json`
  (new), `internal/cli/budget_reconcile_e2e_test.go` (new).
- Interfaces: SA1.2, SA1.3, SA1.5, SA2.1, SA2.2, SA5.1.
- Tests: `TestSpendFieldsEndToEnd`, `TestReconcileReadsARealRecord`.
- Command: `go test ./internal/runner/ ./internal/cli/ -run 'SpendFieldsEndToEnd|ReconcileReadsARealRecord' -count=1`.
- Red step: fails if a field is dropped on the way, or if the charge is still the table's.
- Depends on: SA2.1 (the settle rule it pins), SA1.3, SA1.5, SA2.2 and SA5.1 (it proves their wiring).
- Commit: `spend-accuracy SA1.4: the cost fields from the gateway to reconcile and diagnose`.
- Reviewer tier: standard.

### SA2.1: settle on the provider's figure (R2; D2640, D2641, D2651; RS2, RS4)
- Acceptance:
  - On a provider route, a complete call whose usage carries a cost settles at the larger of `WithFee(reported, fee)`
    and `WithFee(the charge at the row's Lo, fee)` (SA1.6), with `priced_as: reported`. A floored call logs a warning.
    A call priced `max` takes the floor from the pin's row, or has none (RS7).
  - A report above the reservation settles in full as an overrun. A report above 10x the reservation gets the table
    charge and a warning.
  - On a provider route, `usageAcc.merge` keeps the largest cost.
  - A partial call, or a call with no cost, settles as today.
  - The unpriced-tier floor survives only on a partial call that carried a report (RS2), its one remaining path.
  - The comments that D2640 makes false are rewritten: `usageAcc.reported`, the `fromUsage` doc, and the `ledger.go`
    lines on `reported` and `pricedTier`.
  - The docs tests stay green (RS6).
  - A call priced `surprise` (a speed or geography the request chose) keeps today's penalty charge and does not settle
    on its report (O6).
- Files: `internal/gateway/forward.go`, `internal/gateway/sse.go`, `internal/gateway/ledger.go`,
  `internal/gateway/provider_settle_test.go`, `internal/gateway/sse_test.go`, `internal/runner/provider_run_test.go`
  (`TestProviderRunEndsInPR` checks the charge against the table).
- Interfaces: reads the band that SA1.2 computes.
- Tests: `TestSettleOnReport`, `TestLowReportFlooredAtTierMinimum`, `TestReportAboveReservationIsOverrun`,
  `TestReportAboveTenTimesReservationFallsBackToTable`, `TestPartialCallIgnoresReport`, `TestNoReportIsTable`,
  `TestProviderCostKeepsLargest`, `TestMaxPricedCallFloorFromPin`.
- Command: `go test ./internal/gateway/ -run 'SettleOnReport|LowReportFloored|ReportAbove|PartialCallIgnores|NoReportIsTable|ProviderCostKeepsLargest|MaxPricedCall|Surprise' -count=1 && go test ./internal/runner/ -run TestProviderRunEndsInPR -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: `TestSettleOnReport` fails while the table settles.
- Depends on: SA1.2 (the same files, `forward.go` and `ledger.go`, and its band).
- Commit: `gateway: settle a provider call on the cost the provider reported (spend-accuracy SA2.1, D2640)`.
- Reviewer tier: opus, with an independent review (the gateway sets what the caps count; F17).

### SA2.2: diagnose compares the report with the band (F16, D2650)
- Acceptance:
  - When the record carries the new fields, `diagnose` prints
    `Settled: $X (reported on N of M calls); table band $lo-$hi`.
  - It warns when the report is more than `reportedGapPct` above `table_hi_usd` or below `table_lo_usd`, in a new
    `--json` field `band_warning`. `reported_warning` keeps its rule and meaning for a record without the new fields,
    and is empty otherwise (the console renders it).
  - A record without the fields keeps today's comparison and line.
  - `--json` gains `table_lo_usd`, `table_hi_usd` and `unreported_calls` (RS5); `reported_usd`, `charge_usd` and
    `reported_warning` keep their meaning.
- Files: `internal/cli/diagnose.go`, `internal/cli/diagnose_test.go`.
- Interfaces: reads SA1.0's record fields.
- Tests: `TestDiagnoseReportAgainstBand`, `TestDiagnoseOldRecordUnchanged`, `TestDiagnoseReportedWarningNamesPricePolicy`,
  `TestDiagnoseReportedComparesTheProviderRouteOnly`.
- Command: `go test ./internal/cli/ -run 'TestDiagnose(ReportAgainstBand|OldRecordUnchanged|Reported)' -count=1`.
- Red step: `TestDiagnoseReportAgainstBand` fails on the missing line.
- Depends on: SA1.0 (the record fields).
- Commit: `diagnose: compare the provider's cost with the table's band (spend-accuracy SA2.2)`.
- Reviewer tier: standard.

### SA2.3: what D2640 makes false elsewhere
- Acceptance:
  - The `StageReport.Reported` comment and `reported_usd`'s schema description say that a report settles a complete
    call.
  - The runner's `warnReportedGap` compares the report with the band, as `diagnose` does, instead of with the charge.
  - `plugin/skills/working/SKILL.md`'s Cost line no longer says `reported_usd` is "never replacing" the priced figure.
  - The docs tests stay green.
- Files: `internal/gateway/gateway.go`, `schemas/result.schema.json`, `internal/runner/gateway.go`,
  `internal/runner/reported_gap_test.go` (new), `plugin/skills/working/SKILL.md`.
- Interfaces: none new.
- Tests: `TestWarnReportedGapAgainstBand`, `TestResultSchemaAcceptsRunnerRecords`.
- Command: `go test ./internal/runner/ ./schemas/ -run 'WarnReportedGap|TestResultSchema' -count=1 && go test ./plugin/... -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: `TestWarnReportedGapAgainstBand` fails while the warning compares with the charge.
- Depends on: SA2.1 (the behaviour it describes), SA1.5 (the same file, `internal/runner/gateway.go`).
- Commit: `runner, schema: the reported cost settles a call (spend-accuracy SA2.3)`.
- Reviewer tier: opus (a plugin skill is a security-review path).

### SA3.1: `highest` is the default price policy (R3, D2644, F28)
- Acceptance:
  - `PricePolicySet` returns `highest` when the policy is unset, and `CatalogRoute.Policy` `""` means `highest`.
    `cheapest` stays selectable.
  - `fugaro budget prices` and `models add` name the end that is charged. `diagnose`'s advice "set price_policy:
    highest" names `model_prices` alone once `highest` is already the default, and the comment at
    `projectpolicy/policy.go` on what an unset `price_policy` means is corrected.
  - Every test that pins `cheapest` as the default moves (find them with
    `git grep -n 'PricePolicyCheapest\|cheapest end\|Bound != "min"\|Bound == "min"' -- '*_test.go'`).
  - The mixed-version window (F28) is stated in the commit body.
  - Past five files by design (Conventions).
- Files: `internal/config/providers.go`, `internal/config/providers_test.go`, `internal/pricing/catalog_apply.go`,
  `internal/pricing/catalog_apply_test.go`, `internal/cli/models_add.go`, `internal/cli/models_add_test.go`,
  `internal/config/pins_test.go`, `internal/config/pricetable_test.go`, `internal/runner/catalog_test.go`,
  `internal/cli/validate_test.go`, `internal/cli/budget_test.go`, `internal/cli/models_ls_test.go`,
  `internal/cli/diagnose.go`, `internal/cli/diagnose_test.go`, `internal/projectpolicy/policy.go`,
  `internal/projectpolicy/policy_test.go`, `internal/runner/providers_test.go`, `internal/runner/provider_run_test.go`.
- Interfaces: none new.
- Tests: `TestPricePolicyDefaultsHighest`, `TestApplyCatalogUnsetPolicyIsHighest`, and the moved tests.
- Command: `go test ./internal/config/ ./internal/pricing/ ./internal/runner/ ./internal/projectpolicy/ -run 'PricePolicy|ApplyCatalog|Catalog|Pin|PriceTable|Providers|ProviderRun|Policy' -count=1 && go test ./internal/cli/ -run 'ModelsAdd|ModelsLs|Validate|Budget|Diagnose|Docs' -count=1`.
- Red step: `TestPricePolicyDefaultsHighest` fails against `cheapest`.
- Depends on: SA1.1 (the same file, `models_add.go`; and a default that `update --repo` refuses cannot be tested end
  to end), SA2.2 (the same files, `diagnose.go` and `diagnose_test.go`), SA1.6 (the same file, `catalog_apply.go`),
  SA2.1 (the same file, `internal/runner/provider_run_test.go`, whose `Bound == "min"` check this task moves).
- Commit: `pricing: highest is the default price_policy (spend-accuracy SA3.1, D2644, supersedes ruling 3)`.
- Reviewer tier: standard.

### SA4.1: model_prices in dollars per million (R4, D2646 as amended)
- Acceptance:
  - The owner's local file accepts `cache_read_per_m`, `cache_write_5m_per_m` and `cache_write_1h_per_m` in $/M, and
    `cache_read_mult`, `cache_write_5m_mult` and `cache_write_1h_mult` as multipliers.
  - Two spellings of one field are a validation error. An old `cache_read`/`cache_write_*` is read as a multiplier,
    with a warning that names the new key.
  - Loading normalises every spelling into the multiplier fields `ModelPrice` already has. So `policyPrices`, the
    shared copy (`MergeShared`) and the env carry the old names as multipliers.
  - The old-key warning fires for the owner's local file only, never for the shared copy, which keeps the old names
    by design.
  - The settings registry has rows for the new keys, and `schemas/settings.schema.json` lists them.
- Files: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`, `internal/settings/registry.go`,
  `schemas/settings.schema.json`.
- Interfaces: produces the keys that SA4.2 migrates to.
- Tests: `TestPerMConvertsToMultiplier`, `TestTwoSpellingsRefused`, `TestOldCacheReadWarns`,
  `TestSharedKeepsOldSpelling`, `TestSharedCopyNoWarning`, `TestRegistryCoversEveryConfigField`, `TestSettingsSchema`.
- Command: `go test ./internal/localcfg/ ./internal/settings/ ./schemas/ -run 'PerM|TwoSpellings|OldCacheRead|SharedKeepsOldSpelling|SharedCopyNoWarning|RegistryCoversEveryConfigField|TestSettingsSchema' -count=1`.
- Red step: `TestPerMConvertsToMultiplier` fails on an unknown field.
- Depends on: none.
- Commit: `localcfg: cache prices in dollars per million, multipliers by name (spend-accuracy SA4.1, D2646)`.
- Reviewer tier: standard.

### SA4.3: the undercut check (R4, F15)
- Acceptance: `pricing.UndercutsCatalog(model, override, routes)` compiles the model's catalog row as if it were not
  overridden (an override row has `Lo = Hi = Rates`, so the effective table cannot answer), and names each dimension
  of the override below that row's `Lo`, with both prices. A model the catalog does not price returns nothing.
- Files: `internal/pricing/undercut.go` (new), `internal/pricing/undercut_test.go` (new).
- Interfaces: reads SA1.6's ends; produces the check SA4.2 calls.
- Tests: `TestUndercutsCatalog`, `TestUndercutUnknownModelSilent`.
- Command: `go test ./internal/pricing/ -run Undercut -count=1`.
- Red step: `TestUndercutsCatalog` fails on an undefined function.
- Depends on: SA1.6 (the ends).
- Commit: `pricing: name an override below its tier's cheapest end (spend-accuracy SA4.3)`.
- Reviewer tier: standard.

### SA4.2: update migrates the keys and reports undercuts (R4, RS3)
- Acceptance:
  - `fugaro update` rewrites an old `cache_read`/`cache_write_*` in the owner's local file to its `_mult` name in
    place, through `internal/yamledit`. It keeps the value, prints one line, asks nothing, and is idempotent.
  - `update` and `validate` print one warning per override dimension below the route tier's minimum, naming both
    prices. Neither refuses.
  - A converge test starts from a config with `cache_read: 0.0042`, and `TestUpdateLeavesARunInFlightRunning` still
    passes.
  - The migration is a stage of `update`'s stage list (`initStages` in `init_stages.go`), before the policy publish.
- Files: `internal/cli/update_model_prices.go` (new), `internal/cli/update_model_prices_test.go` (new),
  `internal/cli/init_stages.go`, `internal/cli/validate.go`, `internal/cli/validate_undercut_test.go` (new),
  `internal/cli/update_converge_test.go`.
- Interfaces: uses SA4.1's keys and SA4.3's `UndercutsCatalog`.
- Tests: `TestUpdateMigratesCacheReadKey`, `TestUpdateMigrationIdempotent`, `TestValidateWarnsUndercut`.
- Command: `go test ./internal/cli/ -run 'UpdateMigratesCacheRead|UpdateMigrationIdempotent|ValidateWarnsUndercut|UpdateConverge|UpdateLeavesARunInFlight|Docs' -count=1`.
- Red step: `TestUpdateMigratesCacheReadKey` fails while the file is unchanged.
- Depends on: SA4.1 (the migration writes keys only SA4.1's parser accepts: behaviour), SA4.3 (the check it calls).
- Commit: `update: migrate model_prices cache keys and warn on undercuts (spend-accuracy SA4.2)`.
- Reviewer tier: standard. Release note: `### For operators`: run `fugaro update`, which renames `cache_read` to
  `cache_read_mult` in your local config and warns about any undercut.

### SA5.0: rules generation 6, the new leaves
- Acceptance:
  - `rules.json.tmpl` grants `byModel/$model/notional`, `rep`, `tlo` and `thi` exactly as `micros` is granted
    (AMOUNT, increase only, at most MAXRES per write).
  - `projectpolicy.RulesVersion` goes from 5 to 6. Its pins move: `TestRulesVersionUnchanged`, the golden,
    `docs_mobile_auth_test.go`'s pin, the `RulesVersion` doc paragraph in `version.go`,
    `TestUpdateConvergesTheVoicePolicyNodeFromThePreviousGeneration` (whose fixture is `RulesVersion-1`), and
    `rulesMarkerOlderText`, which gains a generation-5 branch naming the new leaves, with its cases in
    `TestDoctorPolicyChecks` and a true short in `doctor_render.go`.
  - `testdata/rules.generation5.json` (new) is the deployed generation-5 document, the fixture SA5.4's
    generation-5 half runs against.
  - `update`'s existing rules stage deploys generation 6, and an update converge test starts from generation 5 with a
    run in flight.
  - Past five files by design (Conventions).
- Files: `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/testdata/rules.golden.json`,
  `internal/budget/rules/testdata/rules.generation5.json` (new), `internal/budget/rules/rules_test.go`,
  `internal/budget/rules/new_leaves_emulator_test.go` (new), `internal/projectpolicy/version.go`,
  `internal/cli/docs_mobile_auth_test.go`, `internal/cli/project_cmd.go`, `internal/cli/doctor_render.go`,
  `internal/cli/doctor_policy_test.go`, `internal/cli/update_converge_test.go`, `docs/gcp-live-checklist.md` (Check
  82's `policyRules = 6` and the generated rules' byte count, which `TestDocsMobileChecklistHasCheck81And82And89`
  pins).
- Interfaces: produces the leaves that SA5.1 writes.
- Tests: `TestRulesVersionUnchanged` (moved to 6), `TestRulesGolden`, `TestNewLeavesNeedTheirGeneration` (`firebase`
  tag), the `rulesMarkerOlderText` tests, the update converge tests.
- Command: `go test ./internal/budget/rules/ -run 'TestRulesVersionUnchanged|TestRulesGolden' -count=1 && go test -tags firebase ./internal/budget/rules/ -run TestNewLeavesNeedTheirGeneration -count=1 && go test ./internal/cli/ -run 'RulesMarker|TestDoctorPolicyChecks|UpdateConverge|UpdateLeavesARunInFlight|Docs' -count=1`.
- Red step: `TestRulesGolden` fails until the template and the golden agree.
- Depends on: SA1.1 (the same file, `docs/gcp-live-checklist.md`).
- Commit: `spend-accuracy SA5.0: per-model notional, reported and band leaves, rules generation 6`.
- Reviewer tier: opus, with an independent review before SA5.1 starts (Firebase rules).

### SA5.6: the day shapes, through the rollup and the archive (F21)
- Acceptance:
  - `budget.ModelUse` gains `Notional`, `Rep`, `TLo` and `THi` (`notional`, `rep`, `tlo`, `thi`), and `NotionalSet`,
    true when the `notional` field was present (RS8).
  - `budget.ModelTotals` carries the same four through the rollup and `DayRecord`'s Firestore fields and reader, so
    `fugaro history rollover` archives them.
  - A day without them reads as zero, with `NotionalSet` false, which the readers treat as "not recorded".
  - `docs/spend-accuracy/day-leaves.md` (new) says what each leaf holds: billed is `micros − notional`, clamped at
    zero by readers, and the band is over reported calls, without the fee.
- Files: `internal/budget/model.go`, `internal/budget/rollup.go`, `internal/budget/rollup_test.go`,
  `docs/spend-accuracy/day-leaves.md` (new).
- Interfaces: produces the shapes that SA5.1 writes and SA5.2, SA5.3 and SA5.7 read.
- Tests: `TestRollupCarriesSpendLeaves`, `TestDayRecordFieldRoundTrip` (extended), `TestNotionalSetOnlyWhenPresent`.
- Command: `go test ./internal/budget/ -run 'RollupCarriesSpendLeaves|DayRecordFieldRoundTrip|NotionalSet' -count=1`.
- Red step: `TestRollupCarriesSpendLeaves` fails on dropped leaves.
- Depends on: none.
- Commit: `budget: the per-model spend leaves through the rollup and the archive (spend-accuracy SA5.6)`.
- Reviewer tier: standard.

### SA5.1: the budget writes the new figures (R5, D2648, D2650)
- Acceptance:
  - The session learns the installation's deployed rules generation through its `Config`, set by
    `internal/runner/budget_session.go` (F27).
  - `Session.report` writes the gateway's `TokensBy` to `in/out/cr/cw`.
  - Under generation 6 or later, it also writes `rep`, `tlo` and `thi`, and writes `NotionalBy` and `AddNotional`'s
    micros to `notional` as well as to `micros`. `micros` stays the whole.
  - Under generation 6, `notional` is written with every `micros` write of that model, 0 included (RS8).
  - `plan` merges each leaf's writes before it builds the update (F26), and chunks every new leaf at the largest
    single lease, as `micros` is.
  - Under generation 5, it writes exactly what it writes today.
  - The generation comes from `projectpolicy`, which exports what its unexported `rulesGeneration` reads.
- Files: `internal/budget/lease.go`, `internal/budget/session.go`, `internal/runner/budget_session.go`,
  `internal/projectpolicy/read.go`, `internal/budget/session_test.go`, `internal/budget/by_tier_test.go`.
- Interfaces: reads SA1.0's `StageReport` fields; writes SA5.6's leaves, which SA5.0's rules grant.
- Tests: `TestReportWritesModelTokens`, `TestReportWritesRepAndBand`, `TestNotionalLeaf`,
  `TestOneFlushSumsBilledAndNotional`, `TestGeneration5WritesTodaysLeaves`, `TestNewLeavesChunkedAtMaxRes`,
  `TestNotionalWrittenAsZero`.
- Command: `go test ./internal/budget/ ./internal/runner/ -run 'ReportWritesModelTokens|ReportWritesRepAndBand|NotionalLeaf|OneFlushSums|Generation5|ChunkedAtMaxRes|NotionalWrittenAsZero|BudgetSession' -count=1`.
- Red step: `TestReportWritesModelTokens` fails on zero tokens.
- Depends on: SA1.0 (the report fields), SA5.0 (the rules, which the generation gate reads), SA5.6 (the leaves'
  shape).
- Commit: `budget: per-model tokens, report, band and notional in the day counters (spend-accuracy SA5.1)`.
- Reviewer tier: opus (a budget write path).

### SA5.2: report keeps billed dollars billed (R5)
- Acceptance:
  - `--by model` reads `ModelTotals.Notional` when `NotionalSet` (RS8), with billed = `micros − notional` clamped at
    zero.
    It calls `splitModel` only for a day without it.
  - Gateway models show their tokens.
  - `MixedModelDays` is set only for days that still needed `splitModel`.
- Files: `ee/report/report.go`, `ee/report/report_test.go`, `ee/report/render.go`.
- Interfaces: reads SA5.6's `ModelTotals`.
- Tests: `TestByModelUsesNotionalLeaf`, `TestByModelOldDaySplits`, `TestByModelClampsBilledAtZero`,
  `TestByModelGatewayOnlyOnMixedDay`.
- Command: `go test ./ee/report/ -run ByModel -count=1 && go test ./internal/eeboundary/ -count=1`.
- Red step: `TestByModelUsesNotionalLeaf` fails while `splitModel` proportions a DeepSeek row.
- Depends on: SA5.6 (the shape; its tests use hand-written day records).
- Commit: `report: billed gateway dollars stay billed, and gateway tokens show (spend-accuracy SA5.2, R5)`.
- Reviewer tier: standard.

### SA5.3: the drift alarm in doctor (R6, D2649, D2650)
- Acceptance:
  - A `doctor` check reads today's and yesterday's counters (the amended window, F30) for every repository
    (`spend/<day>/repos/*`, where `byModel` lives), summed per model.
  - For each model with at least $1 of spend in that window, it is a **warning** (not a failure: nothing `update` can
    fix) when `rep` is more than 10% above `thi` or more than 10% below `tlo`, naming the model and the factor. It is
    quiet otherwise, quiet on a day without the leaves, and skipped offline as the other cloud checks are.
  - `doctor --json` carries it as an ordinary check, with its entries in `doctor_render.go`'s maps.
- Files: `internal/cli/doctor_spend_drift.go` (new), `internal/cli/doctor_spend_drift_test.go` (new),
  `internal/cli/doctor.go`, `internal/cli/doctor_render.go`.
- Interfaces: reads SA5.6's `ModelUse`.
- Tests: `TestDriftAlarmOutsideBandOnly`, `TestDriftAlarmBelowOneDollarQuiet`, `TestDriftAlarmOldDayQuiet`,
  `TestDriftAlarmOfflineSkipped`.
- Command: `go test ./internal/cli/ -run 'DriftAlarm|Doctor' -count=1`.
- Red step: `TestDriftAlarmOutsideBandOnly` fails on a missing check.
- Depends on: SA5.6 (the shape; its tests use hand-written counters), SA5.0 (the same file, `doctor_render.go`).
- Commit: `doctor: say when a provider's cost leaves the table's band (spend-accuracy SA5.3, R6)`.
- Reviewer tier: standard.

### SA5.7: the drift line in watch (R6)
- Acceptance:
  - `fugaro watch` reads yesterday's repo counters as well as today's.
  - It shows the same drift line as SA5.3, once per model per day, in its plain and `--json` views. The `--json`
    change is additive, and the console parity test passes.
- Files: `internal/watch/build.go`, `internal/watch/json.go`, `internal/watch/plain.go`,
  `internal/watch/console_parity_test.go`, `internal/cli/watch.go`, `console/docs/json-contract.md`
  (`TestDocsDesktopConsoleContractNamesEveryWatchField`).
- Interfaces: reads SA5.6's `ModelUse`.
- Tests: `TestWatchDriftLineOnce`, the console parity test.
- Command: `go test ./internal/watch/ ./internal/cli/ -run 'Drift|ConsoleParity|Watch|Docs' -count=1`.
- Red step: `TestWatchDriftLineOnce` fails on a missing line.
- Depends on: SA5.6 (the shape).
- Commit: `watch: the drift line (spend-accuracy SA5.7, R6)`.
- Reviewer tier: standard.

### SA5.4: the day-counter leaves end to end
- Acceptance: an emulator test in `internal/budget/rules` (where the harness lives) runs a session under generation
  6 that reports a gateway stage and a notional stage. The rules accept every leaf it writes, and the counters read
  back decode into `ModelUse` with the new fields, the shape SA5.3 and SA5.7 read. The same session under generation
  5 writes only today's leaves, and none of its updates is refused under `testdata/rules.generation5.json` (SA5.0).
- Files: `internal/budget/rules/spend_leaves_emulator_test.go` (new).
- Interfaces: SA5.0, SA5.1, SA5.6.
- Tests: `TestSpendLeavesEndToEnd`.
- Command: `go test -tags firebase ./internal/budget/rules/ -run TestSpendLeavesEndToEnd -count=1`.
- Red step: fails if the writer writes a leaf under generation 5.
- Depends on: SA5.1 (the writer, which itself needs SA5.0 and SA5.6).
- Commit: `spend-accuracy SA5.4: the day-counter leaves from the session through the rules`.
- Reviewer tier: opus (rules).

### SA6.1: the narrative documents
- Acceptance:
  - `docs/multi-model.md`:
    - §2: `highest` is the default; how to price `model_prices` cache fields in $/M; the effect on caps (larger
      reservations, so "cannot hold" halts and 429 waits come sooner).
    - §6: a call settles on the provider's cost, floored at the tier's cheapest end, and the drift alarm.
  - `docs/provider-price-catalog/spec.md` D6 and §0 item 3: D2644 supersedes ruling 3.
  - `docs/m10-multi-model/spec.md`: the "Real vs notional dollars" paragraph and its Phase 3 line point here.
  - `docs/design/v1.md`: the gateway paragraph and the later sentence that a reported cost is "not something Fugaro
    adopts by itself" say what a report settles.
  - A docs test pins these sentences.
- Files: `docs/multi-model.md`, `docs/provider-price-catalog/spec.md`, `docs/m10-multi-model/spec.md`,
  `docs/design/v1.md`, `internal/cli/docs_spend_accuracy_test.go` (new).
- Interfaces: describes what merged.
- Tests: `TestDocsSpendAccuracy`, and every docs test.
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: `TestDocsSpendAccuracy` fails on the old `cheapest stays the default` sentence.
- Depends on: SA2.1, SA3.1, SA4.2, SA5.3 and SA5.7 (it describes their behaviour).
- Commit: `docs: spend settles on the provider's cost, highest by default (spend-accuracy SA6.1)`.
- Reviewer tier: standard.

## Parallel execution

**Levels** (tasks in one level run at once):

| Level | Tasks |
| --- | --- |
| L0 | SA1.0, SA1.1, SA1.6, SA4.1, SA5.6 |
| L1 | SA1.2, SA1.3, SA1.5, SA2.2, SA4.3, SA5.0, SA5.2, SA5.7 |
| L2 | SA0.1, SA2.1, SA4.2, SA5.1, SA5.3 |
| L3 | SA1.4, SA2.3, SA3.1, SA5.4 |
| L4 | SA6.1 |

**Critical path:** SA1.0 → SA1.2 → SA2.1 → SA3.1 → SA6.1, as `fugaro plan graph` reads it. It is sequential for two
reasons. SA2.1 changes the `fromUsage` that SA1.2 instruments. SA3.1 then moves a runner test, `provider_run_test.go`,
that SA2.1 also changes; and the documents describe what merged.

**Edges kept**, each with its reason:
- SA2.1 needs SA1.2: the same files (`forward.go`, `ledger.go`), and its band.
- SA2.3 needs SA2.1 (it describes SA2.1's behaviour) and SA1.5 (the same file, `internal/runner/gateway.go`).
- SA3.1 needs SA1.1 (the same file, `models_add.go`; and a default that `update --repo` refuses cannot be tested end
  to end), SA2.2 (the same files, `diagnose.go` and `diagnose_test.go`) and SA2.1 (the same file,
  `provider_run_test.go`).
- SA4.2 needs SA5.0: the same file, `update_converge_test.go`.
- SA4.2 needs SA4.1 (the migration writes keys only SA4.1's parser accepts) and SA4.3 (the check it calls).
- SA4.3, SA1.2 and SA3.1 need SA1.6: they read its two ends, and SA3.1 shares its file `catalog_apply.go`.
- SA5.0 needs SA1.1, and SA0.1 needs SA5.0: the same file, `docs/gcp-live-checklist.md`, written in turn.
- SA5.3 needs SA5.0: the same file, `doctor_render.go`.
- SA5.1 needs SA5.0: its generation gate is about the rules SA5.0 deploys.
- SA1.4 and SA5.4 need the tasks they prove. SA1.4 needs SA2.1 because it pins the settle rule.
- SA6.1 needs SA2.1, SA3.1, SA4.2, SA5.3 and SA5.7: it describes them.

**The contract-first pass.** `planning.decoupling` is on for this repository, and its rigor is `startup`, so the pass
decouples aggressively. Seven edges carried an interface only:
- SA1.3, SA1.5, SA2.2 and SA5.1 waited on SA1.2 for the record and stage-report fields;
- SA5.2, SA5.3 and SA5.7 waited on SA5.1 for the leaves' shape.

The pass first drew them as contracts C1 and C2, with integration tasks. `fugaro plan graph` refused both: the task
that fills a contract (SA1.2, SA5.1) builds against its types, and the read does not accept a provider waiting on its
own contract. So each shape became the first real task of its slice instead: SA1.0 for the fields, and SA5.6 for the
leaves. Neither is a stub; both ship as themselves. Writers and readers wait on them, and no reader waits on SA1.2 or
SA5.1. The graph records no `decoupling` entry, and SA1.4 and SA5.4 are ordinary end-to-end tasks. Without the split,
the readers would sit a level later, behind SA1.2 (weight 3) and SA5.1 (weight 2.5). With it, eight tasks share L1.

**Shared files** (no level holds two writers of one file):

| File | Writers, by level |
| --- | --- |
| `internal/gateway/forward.go`, `ledger.go`, `provider_settle_test.go` | SA1.2 (L1), SA2.1 (L2) |
| `internal/gateway/gateway.go` | SA1.0 (L0), SA1.2 (L1), SA2.3 (L3) |
| `schemas/result.schema.json` | SA1.0 (L0), SA2.3 (L3) |
| `internal/runner/gateway.go` | SA1.5 (L1), SA2.3 (L3) |
| `internal/runner/provider_run_test.go` | SA2.1 (L2), SA3.1 (L3) |
| `internal/cli/models_add.go`, `models_add_test.go` | SA1.1 (L0), SA3.1 (L3) |
| `internal/cli/diagnose.go`, `diagnose_test.go` | SA2.2 (L1), SA3.1 (L3) |
| `internal/pricing/catalog_apply.go`, `catalog_apply_test.go` | SA1.6 (L0), SA3.1 (L3) |
| `docs/gcp-live-checklist.md` | SA1.1 (L0), SA5.0 (L1), SA0.1 (L2) |
| `docs/multi-model.md` | SA1.1 (L0), SA6.1 (L4) |
| `internal/cli/update_converge_test.go` | SA5.0 (L1), SA4.2 (L2) |
| `internal/cli/doctor_render.go` | SA5.0 (L1), SA5.3 (L2) |
| `internal/cli/project_cmd.go` | SA5.0 (L1) |
| `internal/cli/doctor.go` | SA5.3 (L2) |
| `internal/budget/model.go`, `rollup.go` | SA5.6 (L0) |
| `internal/budget/lease.go`, `session.go` | SA5.1 (L2) |
| `internal/budget/rules/*` | SA5.0 (L1), SA5.4 (L3, a new file only) |
| `docs/design/*.md` | SA6.1 (L4), and any pinned sentence its own task changes (RS6) |

**If a shape task turns out wrong:** a task that finds SA1.0's or SA5.6's shape wrong amends it in a small task of its
own, and does not work around it. For example, SA1.2 might find that the band needs the pin's key, which would reach
SA1.5, SA2.2 and SA5.1.

## Checks

- **Check 650 (owner-run), the reconciliation.** Run after SA1.3 merges and its release is installed. On the dogfood
  project, type `fugaro budget reconcile --all --from 2026-10-12 --to 2026-11-16 --json`, and paste the output beside the
  OpenRouter invoice for the key. Note whether the run bound truncated the window. Record:
  1. the share of runs with `reported_usd` (U1, O1);
  2. per model, the charge against the report, which settles U5;
  3. one call's tokens and `reported_micros`, from its `model call` log line, against the endpoint's price, which
     settles U2.

  This output is the one-time reconciliation note (A5).
- **Check 651 (owner-run), the acceptance.** Run a week after the release that carries SA2.1 and SA3.1:
  1. Compare `fugaro budget reconcile`'s totals, with the fee, against the OpenRouter invoice in dollars for that week
     (D2643), and record the catalog's `checked_at` (F11). The target is within ±3%.
  2. Run one task with `fugaro run --max-usd` set below its expected cost, and record that it halts and that `reported_usd` is
     within one call of the cap.

## Owner-run steps

- **OR1 (D2645), now, ahead of the plan.** Delete `model_prices.deepseek/deepseek-v4.1-flash` from
  `~/.config/fugaro/projects/fugaro.yaml` by hand (not with `fugaro models rm`, which also removes the model from
  `budget.allowed_models` and would stop v4.1-flash runs), then run `fugaro update` in your own terminal, which
  republishes the policy node's `model_prices`.
  - **Verify:** `fugaro project policy show` (the published node, which runs read) no longer lists v4.1-flash under
    `model_prices`. The shared copy keeps the old entry for teammates' CLIs (O3).
  - Until SA3.1 ships, the catalog prices the model at `cheapest`. From 2026-11-22 the catalog is stale (F11) and
    prices it at the tier's maximum.

## After deploy

- SA5.0 and SA4.2 each add a `### For operators` line to the next release's notes: run `fugaro update`. It deploys
  rules generation 6 and renames a `cache_read` key to `cache_read_mult`. Nothing else.

## Considered and declined

- Rewriting a routed request to ask for usage accounting (D2642).
- A configurable drift threshold (D2649).
- Pricing the endpoint that served the call (D2644; U3).
- A `--price-policy` flag on `models add` (F29; D795's deferral belongs to the models-commands plan).
- Repricing past spend (spec, out of scope; Check 650 is the note).
- A console change for the drift alarm (U7; a follow-up only if the doctor view does not show a new check).
- Removing one entry from the shared copy of `model_prices` (F34, O3; the owner's call, outside this spec).
