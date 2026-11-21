# Spend accuracy: reconciliation

The spec ([spec.md](spec.md)) checked against `origin/main` at `4ce05d949` on 2026-11-15, and the documents of the 30
pull requests open that day. **Where this document disagrees with `spec.md`, this document wins.** Decisions
D2640-D2651 were answered by the owner on 2026-11-15. D2640, D2646, D2648 and D2649 were amended after review round 1,
on the owner's answers of the same day (D2650, D2651 and the D2646 row). An answer settles a question; it approves no task.

Scope check: one capability, the accuracy of recorded model spend. R1-R6 are slices of it, not separate modules: they
share one data path (gateway charge, then the stage report, then the run record and the day counters) and no part has
consumers of its own. So there is no capability map.

## Assumptions

- **A1.** "Provider" means an OpenRouter route of kind `anthropic-compat` (`config.ModelProvider`). Claude on the
  Claude route and Vertex are unchanged (spec: out of scope). The design keeps the provider generic: it reads
  `usage.cost` wherever a route's response carries one.
- **A2.** "Settled charge" is the per-call `amount` `fromUsage` returns (`internal/gateway/forward.go`). It feeds the
  stage report, the run record's `model_usd`/`model_by`/`route_by`, the budget day counters and the caps.
- **A3.** "Reservation" keeps its meaning: what a call holds before it is sent, from the price table, the request's
  `max_tokens` and the route fee.
- **A4.** "The dogfood project" is the installation `fugaro` whose local config is the owner's
  `~/.config/fugaro/projects/fugaro.yaml`, with route `openrouter` (`route_fee_pct: 5.5`).
- **A5.** Past spend is not repriced (spec, out of scope). R1's output for 2026-10-12 to 2026-11-16 is the "one-time
  reconciliation note".

## What the repository already has

| Spec concept | What the repository has |
| --- | --- |
| Provider-reported cost per call | `usageAcc.reported`/`hasReported` (`internal/gateway/sse.go`), from `usage.cost`; summed per stage (`StageReport.Reported`) and per run (`runstore.Cost.ReportedUSD`, `reported_usd`) |
| A comparison of reported against charged | `fugaro diagnose`, per run: `routeOf` in `internal/cli/diagnose.go` sets `reported_usd`, `charge_usd` and `reported_warning` above `reportedGapPct` (10%), and the warning already names `price_policy: highest` |
| The report already pricing some calls | `fromUsage`: the report floors an unpriced service tier's charge, clamped at the reservation |
| A conservative price policy | `price_policy: highest` per route (`config.PricePolicyHighest`, `pricing.CatalogRoute.Policy`), and a stale catalog (past `catalog.StaleAfter`, 30 days) already charges the tier's maximum |
| Per-model tokens in a run's record | `runstore.Cost.ModelTokens` (`model_tokens`) |
| Per-model tokens in the day counters | the `byModel/<m>/{in,out,cr,cw}` leaves; only `Session.AddNotional` writes them (`internal/budget/lease.go`) |

## Findings

**F1 (Wrong).** Spec: the comparison "does not show up anywhere in reporting". `fugaro diagnose` prints it for one run
(`Reported:` line, `reported_usd`/`charge_usd`/`reported_warning` in `--json`; `routeOf` in
`internal/cli/diagnose.go`). What is missing is a view across runs and a date range, and per model. **Consequence:**
R1 is a cross-run view, built beside `diagnose` and reusing its comparison. It is not a new idea.

**F2 (Exists).** `price_policy: cheapest | highest` already exists on a route (`ModelProvider.PricePolicy`,
`PricePolicySet` defaults to `cheapest`; `CatalogRoute.Policy`). **Consequence:** R3 changes a default. It needs no
new switch.

**F3 (Wrong, and the first fix).** `price_policy` cannot be deployed today. `catalogShipped` in
`internal/cli/recipes_skew.go` is still `false`, so `catalogSwitchSkew` refuses every `fugaro update --repo` whose
route sets `price_policy` or `require_zdr`. That refusal is meant to last only until the release named by
`catalogSince` (`0.7.5`) is tagged. `v0.7.5` is tagged and contains #361 (`4ba14292d`), whose runner reads both keys
(`internal/runner/providers_test.go` decodes them). The `docs/release.md` checklist line for `catalogSince` says to
flip it in that release, and nobody did. `modelsCatalogPriceNote` (`internal/cli/models_add.go`) still says
"fugaro update does not accept the switch yet (D795)". **Consequence:** no owner can set `highest` today, and every
OpenRouter route has been priced at `cheapest` since 0.7.5. Resolution RS1.

**F4 (Rule).** R3 reverses a recorded owner ruling. `docs/provider-price-catalog/spec.md` D6: "`cheapest` stays
the default (owner ruling 3)", repeated in `docs/multi-model.md` §2. **Consequence:** D2644 (answered: `highest`)
supersedes ruling 3. D6, §0 item 3 and `docs/multi-model.md` §2 are amended in the same change, and that change says
it supersedes the ruling.

**F5 (Exists, confirmed).** Likely cause 2 is real. The owner's config has
`model_prices.deepseek/deepseek-v4.1-flash: {input_per_m: 0.14, output_per_m: 0.42, cache_read: 0.0042}`.
`cache_read` is a multiplier of input (`pricing.Rates`: "multipliers of InputPerM"), which makes it $0.000588/M,
below the catalog's cheapest ZDR endpoint ($0.002/M, `coreweave/fp8`, catalog checked 2026-10-23). The override
also replaces the catalog row whole (`CatalogRoute.Overridden`), so no `price_policy` ever reached that model.
`deepseek/deepseek-v4-pro` has no override and is catalog-priced at `cheapest`. **Consequence:** D2645 (delete the
override). How much of the gap this explains is U5.

**F6 (Rule, settled).** `docs/m10-multi-model/spec.md` ("Real vs notional dollars") says a large reported-vs-charge
gap is "never auto-adopted", and lists "adopt provider-reported cost as the settled charge" under "Phase 3 (needs the
user's decision)". **Consequence:** D2640 is that decision. The m10 text is amended to point here.

**F7 (Gap).** `result.json` holds the reported cost only as a run total (`Cost.ReportedUSD`); nothing records it per
model. `StageReport.Reported` is also one figure. **Consequence:** R1's per-model column needs an additive
`cost.reported_by` (per serving model), and the gateway has to keep the report per model. For past runs, a per-model
figure exists only where the run used one route model; otherwise R1 shows the run's total under `mixed`.

**F8 (Exists, confirmed).** `routedKeys` (`internal/gateway/forward.go`) refuses a client-sent `usage`, and the
gateway never rewrites a body (`docs/multi-model.md` §7, `docs/provider-price-catalog/spec.md` §0 item 6).
**Consequence:** D2642 (no rewrite; measure presence first).

**F9 (Exists, confirmed).** The report's per-model split. `Session.report` queues gateway spend per model as micros
alone (`pending.byModel`, `map[string]Micros`). `AddNotional` queues tokens and micros (`pending.nby`), and `plan`
writes both into the same `byModel/<m>/micros` leaf. `ee/report`'s `splitModel` then splits each model's dollars by
the day's billed-to-notional ratio. **Consequence:** D2648 (new leaves); gateway tokens per model reach the counters
through the existing `in/out/cr/cw` leaves, which need no rules change.

**F10 (Gap).** D2648's leaves need a rules change. `internal/budget/rules/rules.json.tmpl` grants writes leaf by
leaf under `byModel/$model`, so a write to a leaf the deployed rules lack is denied, and RTDB applies a multi-path
update atomically, so the whole stage report fails. **Consequence:** the new leaves are written only by a runner
whose installation has deployed rules that carry them. That needs either a generation bump (the
`projectpolicy.RulesVersion` route `fugaro update` already deploys and `update_converge_test.go` covers) or a writer
that checks the deployed generation. A run in flight under the old rules keeps writing the old leaves
(`docs/design/update-in-flight.md`). The plan's task SA5.0 settles which (it takes the generation bump).

**F11 (Gap).** The catalog was checked on 2026-10-23, and `catalog.StaleAfter` is 30 days, so from 2026-11-22 every
catalog-priced row charges its tier's maximum whatever `price_policy` says (provider-price-catalog D10). A release
regenerates the catalog (`docs/release.md`). **Consequence:** the acceptance week (Check 651) must name the catalog's
`checked_at`. Once `highest` is the default, staleness changes only which prices are read, never the end.

**F12 (Gap).** OpenRouter's DeepSeek answers `service_tier: "priority"`. Such a call is charged
`ConservativeTierMultiplier` (2x) times its rates, floored by the report (`fromUsage`; `docs/multi-model.md` §6).
So some calls are over-charged today, and the 46% is a net figure. **Consequence:** under D2640 a call with a report
settles on the report and the tier multiple applies only to estimated calls. R1 shows `cost.service_tiers` beside
each run.

**F13 (Gap).** A partial call (a cut stream, `settledPartial`) is charged its reported input plus the reserved
output. **Consequence:** resolution RS2: a partial call stays estimated even when a cost was reported, since a report
on a cut stream may be partial.

**F14 (Gap; amended after review round 1).** `model_prices` travels on four wires, not three:
- the owner's local config (`localcfg.ModelPrice`);
- the shared copy in the runs bucket, which `localcfg.MergeShared` unions with the local file and an older teammate's
  `ParseShared` (`internal/cli/sharedcfg.go`) decodes strictly;
- the published policy node, which `policyPrices` (`internal/cli/project_cmd.go`) fills by copying the `ModelPrice`
  pointers and not through `Rates`;
- `FUGARO_MODEL_PRICES`, which the runner decodes strictly.

**Consequence:** D2646 as amended. The new spellings live in the owner's local file alone. Loading it normalises them
into the multiplier fields `ModelPrice` already has, so `policyPrices`, `MergeShared` and the env all keep the old
names as multipliers. `fugaro update` rewrites an old `cache_read` to `cache_read_mult` in the local file only, with
`internal/yamledit` as `models_rm.go` does.

**F15 (Gap).** R4's "below the catalog's cheapest endpoint" needs a tier. **Consequence:** the comparison uses the
route's selected tier (`zdr` unless `require_zdr: false`) and that tier's minimum per dimension. A model the catalog
does not price is not compared.

**F16 (Gap).** After D2640, `diagnose`'s `reported_warning` (report above the charge without the fee) is empty by
construction for calls settled on the report. **Consequence:** diagnose compares the report with the table's band
for the same calls (D2650). So a run record needs the additive `cost.table_lo_usd`, `cost.table_hi_usd` and
`cost.unreported_calls` (RS5; amended after review round 1, whose first names were `estimated_usd` and
`estimated_calls`).

**F17 (Gap, trust boundary).** D2640 makes the provider's own figure price the call and so drive the caps. A hostile
agent cannot forge it: the gateway reads the upstream response, not the agent's. A provider that under-reports would
make calls cheap or free and the caps would never bind. What bounds it: D2651 floors a settle at the tier's cheapest end, so a
zero report cannot make a call free; the drift alarm (D2650) names a model whose reports leave the band; and the key's
credit limit (`docs/multi-model.md` §1) stays the hard bound. **Consequence:** the gateway
task gets the security reviewer, and the alarm watches both directions.

**F18 (Rule).** "A field never changes meaning". `model_usd`, `model_by` and `route_by` keep their meaning: what the
run was charged. `reported_usd` keeps its meaning too (what providers said); only its schema description's sentence
"it never prices a call the table priced" stops being true. **Consequence:** the description changes and the new
fields are additive, so no format version bumps.

**F19 (Open).** Likely cause 5 (late settles landing in `model_by["unattributed"]`, `modelByUSD` in
`internal/runner/cost.go`). F24 settles how they reach the run's figures. **Consequence:** R1 prints
`unattributed` as its own row.

**F20 (Gap).** "The console says so" (R6). The console reads `fugaro doctor --json` through its spawn allow-list
(`console/src/main/spawn.ts`). **Consequence:** the alarm is a doctor check. Whether the console shows a new check
without a console change is U7.

**F21 (Gap, review round 1).** `fugaro report` does not read the live counters. `ee/report.Build` reads
`budget.DayRecord.ByModel` (`ModelTotals`, `internal/budget/rollup.go`), which the rollup builds from the counters and
`fugaro history rollover` (`internal/budget/rollover.go`) archives to Firestore. **Consequence:** the new leaves are
carried through `ModelTotals`, its Firestore fields and its reader (task SA5.6), or R5 never reaches the report.

**F22 (Gap, review round 1).** `usageAcc.merge` (`internal/gateway/sse.go`) takes the cost last-wins even on a provider
route, where token counts keep the largest (`takeMax`), and it accepts `usage.cost: 0`. **Consequence:** under D2640, a
later event can lower a call's settled cost, and a zero report would settle a call at $0. D2651 floors the settle at
the tier's cheapest end, and the provider route keeps the largest cost seen.

**F23 (Gap, review round 1).** Tier-A notional per model. `StageReport.ByModel` holds every tier's money, and
`Notional` is one scalar (`StageReport` and `Ledger` in `internal/gateway/gateway.go`). **Consequence:** SA1.0 adds `NotionalBy`, so a cascade
run's tier-A spend reaches the `notional` leaf per model.

**F24 (Gap, review round 1; was U6).** A call settled after its stage's wait reaches the run's `model_usd` and
`Ledger.ModelTokens`, but the run-level `Ledger` has no reported or table figure, so it would be missing from
`reported_by` and the estimates. Those runs carry `model_by["unattributed"]`. **Consequence:** SA1.2 gives the
`Ledger` the same per-model report and table figures, and the runner (SA1.5) takes the run's totals from it.

**F25 (Gap, review round 1).** `Ledger.ModelTokens` is keyed by the priced (pin) model, while `ByModel` and the
`byModel/<m>/micros` leaf are keyed by the serving model. **Consequence:** the per-stage tokens the day counters get
(SA1.0's `TokensBy`) are keyed by the serving model, so a model's dollars and tokens share one row. The run record's
`model_tokens` keeps its pin keys, and its meaning does not change.

**F26 (Gap, review round 1).** `Session.plan` (`internal/budget/lease.go`) builds each map's leaf writes from the same
current value. A model that has both billed and notional spend in one flush has its `micros` write overwritten rather
than summed. **Consequence:** SA5.1 merges writes per leaf before it builds the update.

**F27 (Gap, review round 1).** Rules generation 5 is pinned outside the rules package: `docs_mobile_auth_test.go` pins
`const RulesVersion = 5`, and the top branch of `rulesMarkerOlderText` (`internal/cli/project_cmd.go`) names the voice
node as what an older marker lacks. Nothing in `budget.Session` knows the deployed generation today.
**Consequence:** SA5.0 moves the pins and the marker text, and SA5.1 takes the generation into the session (its
`Config`, from `internal/runner/budget_session.go`).

**F28 (Rule, review round 1).** An absent `price_policy` changes meaning, from `cheapest` to `highest` (D2644).
**Consequence:** this is the owner's decision, and the mixed-version window is stated rather than hidden. A job image
built before the change reads an unset policy as `cheapest` until `fugaro update` rebuilds the images, which the same
`update` does. Until then, the CLI's caps and its estimates already use `highest`. That is the safe direction: a cap
only binds sooner.

**F29 (Gap, review round 1).** D795 (`docs/models-commands/reconciliation.md`) deferred a `--price-policy` flag on
`models add` "with the release that lifts the refusal", and SA1.1 lifts it. **Consequence:** the flag is not in this
spec. It is listed as considered and declined here and left to the models-commands plan.

**F30 (Gap, review round 1).** The day counters are per UTC day, so "a rolling 24 hours" (D2649) can only be read as
today plus yesterday, a window of 24 to 48 hours. **Consequence:** D2649 is amended to that window, and O4 asks the
owner to confirm.

**F31 (Gap, review round 2).** The price table holds one bound per model. A catalog row carries the
selected bound's `Rates`, plus `TierInputMax`/`TierOutputMax` in its provenance (`pricing.CatalogPrice`), and no
minimum and no cache bounds. Once `highest` is the default, the cheapest end is not in the table at all.
**Consequence:** a new task (SA1.6) gives a `pricing.Model` both ends' rates, set in `catalogModel`; for a row that is
not catalog-priced, both ends equal its rates. SA1.2's band and SA2.1's floor read them.

**F32 (Gap, review round 2).** A call priced `max` (a serving model the table does not know, or a serving model other
than the pin; `fromUsage`) has no band of its own, and the table's maximum is Claude's rates. **Consequence:**
resolution RS7.

**F33 (Gap, review round 2).** "The day has the leaf" has no signal. `ModelUse` and `ModelTotals` hold integers, and
`DayRecord`'s reader reads a missing field as 0, so a gateway-only model's real `notional` of 0 would look like an
old day. **Consequence:** resolution RS8.

**F34 (Gap, review round 2; answers U8).** No command removes one `model_prices` entry from the shared copy: the
publish writes `MergeShared(published, local)` (`publishSharedWarn`, `internal/cli/sharedcfg.go`), which unions the
two. Runs are unaffected for another reason (corrected in review round 3): with a published policy, a run takes
`model_prices` from the policy node, whole (`internal/runner/policy.go`), and `fugaro update` republishes the node when
the local `model_prices` differs (`policyConfigDrift`). A teammate's CLI keeps the stale entry for its own estimates and
`budget prices`. **Consequence:** OR1 fixes the runs, verified on the published node. The shared copy's stale entry is O3, a follow-up outside this
spec.

## Resolutions (one defensible answer)

- **RS1.** Flip `catalogShipped` to `true`, with `catalogSince` left at `0.7.5`, and fix `modelsCatalogPriceNote` and
  the README OpenRouter section's "leave both out" sentence that `TestReadmeOpenRouterSetup`
  (`internal/cli/docs_providers_test.go`) holds to `catalogShipped`. This is a bug fix and does not
  depend on D2644.
- **RS2.** A partial call settles as today and is marked estimated (F13).
- **RS3.** An override below the catalog's tier minimum gets a warning in `validate` and `update` (the two the spec
  names), never a refusal or a prompt (spec R4; the no-prompt policy).
- **RS4.** Each call's log line gains `priced_as: reported`, and an estimated call keeps today's `priced_as` (table,
  max, tier or surprise).
- **RS5 (review round 1).** Record and `--json` names avoid the existing `cost.estimate` (a bool). The new fields are
  `reported_by`, `table_lo_usd`, `table_hi_usd` and `unreported_calls`, and `diagnose --json` uses the same names.
- **RS7 (review round 2, F32).** A call priced `max` takes its band and floor from the pin's row when the table
  knows the pin. Otherwise it settles on its report with no floor, and is left out of the band and the alarm; its call
  log line says so.
- **RS8 (review round 2, F33).** Under generation 6, the writer writes `notional` with every `micros` write of that
  model, including a 0, and the readers take the field's presence (a `NotionalSet` beside the value in `ModelUse` and
  `ModelTotals`) as the signal. A day without it keeps `splitModel`. In the mixed-version window, a run in flight on an
  older image writes notional into `micros` alone, and those dollars read as billed. That lasts until the run ends,
  and is stated, not fixed.
- **RS6 (review round 1).** Each code task keeps the docs tests green in its own pull request: a pinned sentence it
  makes false changes in that pull request (the "Docs are tested" rule). SA6.1 writes the narrative that remains.

## Decisions

| # | Question | Recommendation | Answer (2026-11-15) |
| --- | --- | --- | --- |
| D2640 | Settle on the provider's cost (R2, spec question 1) | Reported + `route_fee_pct` where `usage.cost` is present on a complete call; table otherwise, marked estimated; the table stays the reservation | **The recommendation.** *Amended after review round 1 (F22, D2651):* a report below the tier's cheapest end is floored there. |
| D2641 | A report above the call's reservation | Settle in full as an overrun; a report above 10x the reservation is malformed and gets the table charge plus a warning | **The recommendation.** |
| D2642 | Request usage accounting (R2's owner decision) | No body rewrite; R1 measures how often `usage.cost` is present, and the question comes back only if it is mostly absent | **The recommendation.** |
| D2643 | Which OpenRouter figure the ±3% acceptance compares against | The key's usage in credits against the charge without the fee | **Not the recommendation: the invoice's dollars against the charge with the fee.** Check 651 measures it that way; the daily noise of credit purchases is the cost the owner accepted. |
| D2644 | The default `price_policy` (R3, spec question 2) | `highest`, the maximum of the selected tier | **The recommendation.** Supersedes owner ruling 3 (F4). |
| D2645 | The dogfood config's `cache_read: 0.0042` (spec question 3) | Delete the override, so the catalog prices v4.1-flash | **The recommendation.** Owner-run (OR1). |
| D2646 | The cache-price spelling in `model_prices` (R4) | `cache_read_per_m`, `cache_write_5m_per_m`, `cache_write_1h_per_m` in $/M in the local config, the multiplier only as `cache_read_mult` (and `cache_write_5m_mult`, `cache_write_1h_mult`); the CLI converts, the node and the env stay multipliers; `update` migrates in place | **The recommendation.** *Amended after review round 1, the owner's answer of 2026-11-15 (F14):* the shared copy in the runs bucket also keeps the old names as multipliers. The new names exist only in the owner's local file. |
| D2647 | Where R1 lives | A core command, `fugaro budget reconcile --from --to`, reading the runs' `result.json` | **The recommendation.** |
| D2648 | Telling billed from notional per model in the day counters (R5) | New leaves `byModel/<m>/notional`, `est` (the table's estimate of the same calls) and `rep` (the provider-reported cost); a day without them keeps `splitModel` | **The recommendation.** *Amended after review round 1 by D2650:* `est` becomes two leaves, `tlo` and `thi`. |
| D2649 | The drift alarm (R6) | 10% (`reportedGapPct`) over a rolling 24 hours, per model, in either direction, from the day-counter leaves, and only once the model has $1 of spend; shown by `watch` and `doctor` (and the console through `doctor --json`) | **The recommendation.** *Amended after review round 1:* by D2650 (what drift is measured against), and by F30 (the window is today plus yesterday, UTC; confirm in O4). |
| D2650 | *Review round 1, the D2644 × D2649 tension.* Under `highest`, the estimate is the tier's top, and v4.1-flash's spread is 8.6x on input and 13.3x on output, so a 10% alarm against it would always fire | Record the table's charge at both ends of the tier (`tlo`, `thi`) over the calls that carried a report, with no fee. Alarm when the report is more than 10% above `thi` or more than 10% below `tlo`. A model the catalog does not price has `tlo = thi` | **The recommendation.** |
| D2651 | *Review round 1, F22.* A report below the tier's cheapest end, or a zero report | Settle at the larger of the report and the tier's cheapest-end charge, plus the fee, with a warning; on a provider route, keep the largest cost the response reported | **The recommendation.** |

## Open tensions

- **T1, resolved.** D2644 against D2649. A 10% alarm against a highest-end estimate would fire on nearly every
  DeepSeek call (review round 1). D2650 resolved it on 2026-11-15.

D2643 (invoice dollars, with the fee) and D2640 (settle on the report plus the fee) agree: what is recorded is what is
compared.

## Open items

- **O1.** If R1 shows `usage.cost` missing on most DeepSeek responses, D2640 settles few calls, and D2642 comes back
  to the owner as its own decision. Check 650 answers it.
- **O2.** Reasoning tokens (spec cause 4) are U2. If OpenRouter bills reasoning outside `output_tokens`, D2640 already
  covers every call that carries a report, and only estimated calls would under-charge.

- **O3 (rewritten after review round 2, F34).** The shared copy keeps the dogfood override after OR1, and no command
  removes one entry. Runs are not affected. Whether to add a removal (a `models rm` that also prunes the shared copy,
  say) is the owner's call, and it is outside this spec.
- **O5 (review round 3, left open: the review cap).** Some review-round-3 findings need a design call, so this
  plan does not settle them:
  - A generation-6 runner whose stage report is refused (an older CLI redeployed generation-5 rules mid-run) fails the
    whole atomic update. The plan does not say whether it retries without the new leaves.
  - A `max`-priced call with no known pin (RS7): left out of the band but kept in `reported_by` makes the alarm
    fire, while leaving it out of `reported_by` breaks the sum against `reported_usd`.
  - F28's window could be closed rather than stated, by writing the resolved `price_policy` into
    `FUGARO_MODEL_PROVIDERS` explicitly, which every runner from 0.7.5 reads.
- **O6 (review round 3).** A call priced `surprise` (a speed or geography the request chose) keeps its penalty and
  does not settle on its report. The plan takes this as the default, since the stage fails either way; confirm it.
- **O4.** Confirm the amended D2649 window: today plus yesterday (UTC), which is 24 to 48 hours (F30).

## UNVERIFIED

- **U1.** Whether OpenRouter's Anthropic-compatible `/messages` returns `usage.cost` without the request asking.
  Check 650 settles it from the records (`reported_usd` present on what share of runs); the owner runs it.
- **U2.** Whether that endpoint's `output_tokens` includes reasoning tokens. Comparing one call's tokens with its
  `usage.cost` at a known endpoint price settles it (Check 650, step 3).
- **U3.** Whether the response names the endpoint that served it. It matters only if D2644 is revisited (the "served
  endpoint" option); nothing in the plan depends on it.
- **U4.** Whether `route_fee_pct: 5.5` models OpenRouter's credit-purchase fee, and whether `usage.cost` excludes it.
  The owner knows why the fee was set; Check 651 compares against the invoice either way (D2643).
- **U5.** The 46% figure and its split between causes 1 to 3. Check 650 (R1 over 2026-10-12 to 2026-11-16) settles it.
- **U6.** Withdrawn: settled by F24.
- **U7.** Whether the console renders a new doctor check without a console change. A console maintainer settles it by
  reading the doctor view; if it does not, the console change is a follow-up task, which this plan does not add.
- **U8.** Withdrawn: settled by F34.
