# The cost fields a run records per model

The [plan](implementation-plan.md)'s task SA1.0 declares four fields on the run record and the gateway
fields behind them, and writes or reads none of them: SA1.2 records them in the gateway, SA1.5 writes
them into `result.json`, and SA1.3 (`fugaro budget reconcile`), SA2.2 (`diagnose`) and SA5.1 (the day
counters) read them. A record written before this task carries none of them, and neither does a run
whose calls reported no cost.

## What the run record holds

| Field (`runstore.Cost`) | Wire name | Holds |
| --- | --- | --- |
| `ReportedBy` | `reported_by` | What providers said their calls cost, in USD, by serving model. `ReportedUSD` (`reported_usd`) is its sum: every settled call's report is in a key. |
| `TableLoUSD` | `table_lo_usd` | What the price table would have charged those same calls at the selected tier's cheapest end. |
| `TableHiUSD` | `table_hi_usd` | The same at the tier's dearest end. With `table_lo_usd` this is the band a report is read against. |
| `UnreportedCalls` | `unreported_calls` | How many of the run's settled calls came back with no provider cost, which is how much of the band the report is actually over. |

`TableLoUSD` and `TableHiUSD` are the band, and three rules say what it covers:

- **Reported calls only.** A call whose response carried no cost is in neither end and in no key of
  `reported_by`. Such a call was priced by the table itself, so putting its table figure in the band
  would make the band agree with the very charge a reader is comparing against it.
- **No fee.** Neither the report nor either end of the band carries the route's `route_fee_pct`. The
  charge (`model_usd`, `model_by`) does, so a reader that compares the three compares the same
  quantity: the fee is added once, where the run was billed.
- **Both ends per model.** A model the built-in catalog does not price (an override, an embedded
  Claude row) has one bound rather than a range, so it contributes the same figure to both ends.

The keys are serving models: the model an upstream response named, which is what `model_by`'s keys
are too (F7, F25). They are deliberately not `model_tokens`' keys, which stay the model the gateway
priced each call under (the run's own pin), and `model_tokens` keeps that meaning.

A model priced `max` — a serving model the table does not know — takes its band from the pin's row
where the table knows the pin, and is left out of the band otherwise (RS7).

## The gateway shapes behind them

| Field (`gateway.StageReport`) | Holds |
| --- | --- |
| `ReportedBy` | The stage's `Reported` by serving model; its sum is `Reported`. |
| `TableLoBy`, `TableHiBy` | The stage's band by serving model, over the calls that carried a report. |
| `UnreportedCalls` | The stage's settled calls that carried none. |
| `NotionalBy` | `Notional` by serving model (F23): a cascade run's tier-A spend, so one model's notional dollars can be told from its billed ones. |
| `TokensBy` | `Tokens` by serving model (F25), so a model's dollars and its tokens share a row. |

`gateway.Ledger` carries `ReportedBy`, `TableLoBy`, `TableHiBy` and `UnreportedCalls` of its own, with
the same meanings (F24). The runner takes the run's record from the **ledger** and never from a sum of
the stage reports: a call that settles after its stage's report was taken — the runs that carry
`model_by["unattributed"]` — is in the ledger's figures and in no stage report, so reading the reports
instead would drop it, and would count its report in no model's key.

Nothing here changes a field that already exists. `reported_usd`, `model_usd`, `model_by` and
`model_tokens` keep their meanings, and the new fields are additive, so no format version moves.
