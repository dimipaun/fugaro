# Spend accuracy: reconciliation

The spec ([spec.md](spec.md)) checked against `origin/main` at `4ce05d949` on 2026-11-15, and the documents of the 30
pull requests open that day. **Where this document disagrees with `spec.md`, this document wins.** Decisions
D2640-D2649 were answered by the owner on 2026-11-15. An answer settles a question; it approves no task.

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

**F4 (Rule).** R3 reverses a recorded owner ruling. `docs/design/provider-price-catalog.md` D6: "`cheapest` stays
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

**F6 (Rule, settled).** `docs/design/m10-multi-model.md` ("Real vs notional dollars") says a large reported-vs-charge
gap is "never auto-adopted", and lists "adopt provider-reported cost as the settled charge" under "Phase 3 (needs the
user's decision)". **Consequence:** D2640 is that decision. The m10 text is amended to point here.

**F7 (Gap).** `result.json` holds the reported cost only as a run total (`Cost.ReportedUSD`); nothing records it per
model. `StageReport.Reported` is also one figure. **Consequence:** R1's per-model column needs an additive
`cost.reported_by` (per serving model), and the gateway has to keep the report per model. For past runs, a per-model
figure exists only where the run used one route model; otherwise R1 shows the run's total under `mixed`.

**F8 (Exists, confirmed).** `routedKeys` (`internal/gateway/forward.go`) refuses a client-sent `usage`, and the
gateway never rewrites a body (`docs/multi-model.md` §7, `docs/design/provider-price-catalog.md` §0 item 6).
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

**F14 (Gap).** `model_prices` travels on three wires: the local config (`localcfg.ModelPrice`), the published policy
node (`projectpolicy.Prices`) and `FUGARO_MODEL_PRICES`, which the runner decodes strictly. **Consequence:** D2646
keeps both wire formats as multipliers. The CLI converts `_per_m` to multipliers when it reads the local config, and
`fugaro update` rewrites an old `cache_read` to `cache_read_mult` in place (the update rule in `CLAUDE.md`).

**F15 (Gap).** R4's "below the catalog's cheapest endpoint" needs a tier. **Consequence:** the comparison uses the
route's selected tier (`zdr` unless `require_zdr: false`) and that tier's minimum per dimension. A model the catalog
does not price is not compared.

**F16 (Gap).** After D2640, `diagnose`'s `reported_warning` (report above the charge without the fee) is empty by
construction for calls settled on the report. **Consequence:** diagnose compares the settled charge with the table
estimate of the same calls, so a run record needs an additive `cost.estimated_usd` (the table's charge for every
call) and `cost.estimated_calls` (the calls with no report).

**F17 (Gap, trust boundary).** D2640 makes the provider's own figure price the call and so drive the caps. A hostile
agent cannot forge it: the gateway reads the upstream response, not the agent's. A provider that under-reports would
make calls cheap or free and the caps would never bind. What bounds it: D2641's 10x bound applies only to a report
above the reservation, not below it; the drift alarm (R6, D2649) compares the settled spend with the table estimate
per model; and the key's credit limit (`docs/multi-model.md` §1) stays the hard bound. **Consequence:** the gateway
task gets the security reviewer, and the alarm watches both directions.

**F18 (Rule).** "A field never changes meaning". `model_usd`, `model_by` and `route_by` keep their meaning: what the
run was charged. `reported_usd` keeps its meaning too (what providers said); only its schema description's sentence
"it never prices a call the table priced" stops being true. **Consequence:** the description changes and the new
fields are additive, so no format version bumps.

**F19 (Open).** Likely cause 5 (late settles landing in `model_by["unattributed"]`, `modelByUSD` in
`internal/runner/cost.go`). Whether they also miss the day counters' `byModel` is U6. **Consequence:** R1 prints
`unattributed` as its own row.

**F20 (Gap).** "The console says so" (R6). The console reads `fugaro doctor --json` through its spawn allow-list
(`console/src/main/spawn.ts`). **Consequence:** the alarm is a doctor check. Whether the console shows a new check
without a console change is U7.

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

## Decisions

| # | Question | Recommendation | Answer (2026-11-15) |
| --- | --- | --- | --- |
| D2640 | Settle on the provider's cost (R2, spec question 1) | Reported + `route_fee_pct` where `usage.cost` is present on a complete call; table otherwise, marked estimated; the table stays the reservation | **The recommendation.** |
| D2641 | A report above the call's reservation | Settle in full as an overrun; a report above 10x the reservation is malformed and gets the table charge plus a warning | **The recommendation.** |
| D2642 | Request usage accounting (R2's owner decision) | No body rewrite; R1 measures how often `usage.cost` is present, and the question comes back only if it is mostly absent | **The recommendation.** |
| D2643 | Which OpenRouter figure the ±3% acceptance compares against | The key's usage in credits against the charge without the fee | **Not the recommendation: the invoice's dollars against the charge with the fee.** Check 651 measures it that way; the daily noise of credit purchases is the cost the owner accepted. |
| D2644 | The default `price_policy` (R3, spec question 2) | `highest`, the maximum of the selected tier | **The recommendation.** Supersedes owner ruling 3 (F4). |
| D2645 | The dogfood config's `cache_read: 0.0042` (spec question 3) | Delete the override, so the catalog prices v4.1-flash | **The recommendation.** Owner-run (OR1). |
| D2646 | The cache-price spelling in `model_prices` (R4) | `cache_read_per_m`, `cache_write_5m_per_m`, `cache_write_1h_per_m` in $/M in the local config, the multiplier only as `cache_read_mult` (and `cache_write_5m_mult`, `cache_write_1h_mult`); the CLI converts, the node and the env stay multipliers; `update` migrates in place | **The recommendation.** |
| D2647 | Where R1 lives | A core command, `fugaro budget reconcile --from --to`, reading the runs' `result.json` | **The recommendation.** |
| D2648 | Telling billed from notional per model in the day counters (R5) | New leaves `byModel/<m>/notional`, `est` (the table's estimate of the same calls) and `rep` (the provider-reported cost); a day without them keeps `splitModel` | **The recommendation.** |
| D2649 | The drift alarm (R6) | 10% (`reportedGapPct`) over a rolling 24 hours, per model, in either direction, from the day-counter leaves, and only once the model has $1 of spend; shown by `watch` and `doctor` (and the console through `doctor --json`) | **The recommendation.** |

## Open tensions

None. D2643 (invoice dollars, charge with the fee) and D2640 (settle on the report plus the fee) agree: what is
recorded is what is compared.

## Open items

- **O1.** If R1 shows `usage.cost` missing on most DeepSeek responses, D2640 settles few calls, and D2642 comes back
  to the owner as its own decision. Check 650 answers it.
- **O2.** Reasoning tokens (spec cause 4) are U2. If OpenRouter bills reasoning outside `output_tokens`, D2640 already
  covers every call that carries a report, and only estimated calls would under-charge.

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
- **U6.** Whether a call that settles after its stage's wait reaches the day counters' `byModel` at all, or only the
  run's `unattributed`. Read the gateway's late-settle path, or measure in Check 650.
- **U7.** Whether the console renders a new doctor check without a console change. A console maintainer settles it by
  reading the doctor view; if it does not, the console change is a follow-up task, which this plan does not add.
