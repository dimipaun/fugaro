# Spec: recorded model spend must match what the provider bills

Owner: Dimi Paun. Date: 2026-11-16. For the engineering repository (`spec-to-pr`).

## Problem

For runs routed through OpenRouter (`deepseek/deepseek-v4.1-flash`, `deepseek/deepseek-v4-pro`), Fugaro's recorded
spend is far below what OpenRouter bills. On the dogfood project, from 2026-10-12 to 2026-11-16, `fugaro report --by model`
billed spend for the DeepSeek models was **about 46% of what OpenRouter billed** on the one API key only Fugaro runs
use. No OpenRouter traffic was found that skips the gateway.

This matters for three reasons:
- **The spend caps are wrong.** The gateway reserves and settles against the same price table, so a run capped at $X
  can actually spend about $2X. Caps are a product promise ("halts the run at its dollar cap").
- **Reports and the console mislead.** `fugaro report`, `fugaro watch` and the cost in `result.json` understate real
  cost, and so does anything built on them, such as cost per PR and `fugaro estimate` comparisons.
- **Customers will reconcile.** A customer who compares Fugaro's numbers with their OpenRouter invoice will find the
  gap.

## What the code does today (origin/main, 2026-11-16)

- `internal/gateway/forward.go` (`fromUsage`) settles each call as `rates.Cost(usage)` × tier, plus `route_fee_pct`.
  Usage is parsed in `internal/gateway/sse.go` (`usageAcc`, `wireUsage`): input, output, cache read and cache creation
  only.
- Rates come from `internal/pricing/catalog/catalog.json` through `pricing.ApplyCatalog`. A `model_prices` entry
  replaces the catalog row (`internal/pricing/overrides.go`).
- The default `price_policy` is `cheapest`: each dimension takes its minimum across endpoints, and the minimums can
  come from different providers. `docs/multi-model.md` §2 and `docs/design/provider-price-catalog.md` (D6) already
  say this undercharges.
- OpenRouter's own reported cost is parsed (`usageAcc.reported`), stored as `Cost.ReportedUSD`
  (`internal/runstore/cost.go`, `reported_usd`) and **never used for the charge**. `docs/design/m10-multi-model.md`
  ("Real vs notional dollars") defers adopting it to "Phase 3 … where proven". The comparison it promised in
  `diagnose` does not show up anywhere in reporting.
- `fugaro report --by model` shows 0 tokens for gateway models. The budget path writes only micros for them; token
  leaves come only from `AddNotional`.
- `ee/report/report.go` `splitModel` splits every model's dollars on a day by that day's billed-to-notional ratio. On
  days mixing subscription Claude runs with DeepSeek, part of DeepSeek's real spend is shown as notional.

## Likely causes, ranked (to confirm in the reconciliation)

1. **Token-priced charges with the `cheapest` policy**, under catalog versions whose minimums were lower still
   (2026-10-14 and 2026-10-20). OpenRouter routes by inverse square of price across endpoints, not always to the
   cheapest one.
2. **A misread price override.** In `model_prices`, `cache_read` is a *multiplier of the input price*, but the dogfood
   config sets `deepseek/deepseek-v4.1-flash: cache_read: 0.0042`, apparently meant as dollars per million. That
   prices cache reads at about $0.0006 per million, below every endpoint, with an ~89% cache hit rate. The config
   format invites this mistake.
3. **The report's `splitModel`** moving billed DeepSeek dollars into the notional column.
4. **Unpriced dimensions:** reasoning tokens. Unconfirmed whether OpenRouter's Anthropic-compatible `output_tokens`
   includes them.
5. **Minor:** calls that settle after the stage's wait and land in run-level `unattributed`, possibly outside
   `byModel` (unconfirmed).

## Requirements

- **R1, reconcile first.** A command or `diagnose` view that compares, for a date range, recorded charge
  (`model_by`), provider-reported cost (`reported_usd`) and tokens (`model_tokens`) per model, from the runs'
  `result.json`. It shows how often `reported_usd` is present. This settles which causes are real before anything
  changes. Acceptance: on the dogfood project, the sum of `reported_usd` for 2026-10-12 to 2026-11-16 can be put next
  to the provider invoice.
- **R2, settle on the provider's figure where it exists.** When a response carries the provider's own cost, the
  settled charge is that cost plus the route fee, and the table stays the worst-case reservation. Otherwise, settle
  from the table and mark the call "estimated". This is the "Phase 3" switch in `m10-multi-model.md`, made now that
  real data shows the gap. Owner decision: whether to request usage accounting from OpenRouter explicitly
  (`routedKeys` refuses a client-sent `usage`, unconfirmed). The gateway could add it itself on routed calls.
- **R3, a safer default price policy.** For reservation and for estimated settles, default `price_policy` to a
  conservative figure (the maximum across allowed endpoints, or the price of the endpoint actually served when the
  response names it), not the per-dimension minimum. Document the change and its effect on caps.
- **R4, overrides that cannot be misread.** `model_prices` accepts cache prices in dollars per million
  (`cache_read_per_m`, `cache_write_per_m`), keeps the multiplier only under an explicit name, and validation warns
  when an override is below the catalog's cheapest endpoint for that model. `fugaro update`/`validate` reports any
  override that undercuts the catalog.
- **R5, correct reports.** `fugaro report` keeps billed gateway dollars in the billed column (no `splitModel`
  proportioning of real spend) and shows gateway token counts per model.
- **R6, drift alarm.** When settled (or reported) spend and the table estimate differ by more than a threshold over a
  rolling day, `fugaro watch`, `doctor` and the console say so, naming the model and the factor.

## Out of scope

- Repricing past spend history (record a one-time reconciliation note instead).
- Claude subscription notional figures.
- Providers other than OpenRouter. The design should not preclude them.

## Acceptance for the whole change

On the dogfood project, over a week after release, recorded billed spend for OpenRouter models is within ±3% of the
OpenRouter invoice for the key the project uses, and a run's cap halts it at its dollar cap measured against what the
provider billed.

## Owner decisions to ask in reconciliation

1. Adopt the provider's reported cost as the settled charge (R2): yes, or a reported/table blend?
2. The new default `price_policy` (R3): maximum across endpoints, or the served endpoint's price?
3. Fix the dogfood config's `cache_read: 0.0042` now, ahead of R4: yes, and to what?
