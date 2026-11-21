# The day-counter leaves

What each leaf under a repository's day counter holds, and what a day that has none of them means. It is the
companion of the spec's R5 and R6 and of the decisions D2648 and D2650; the fields a run *records* per model are
`cost-fields.md`'s business (task SA1.0, not written yet), the dollars these leaves divide are the ones
`docs/m10-multi-model/spec.md` §"Real vs notional dollars" describes, and the node they join is
`docs/m9-budget-and-dashboard/spec.md` §6.2 and the schema at the end of that document.

These leaves are granted by **rules generation 7**, the next free one. The reconciliation (RS8) and the plan (SA5.0)
say 6, both written before generation 6 was taken: 6 is the mailbox registration kind, `projectpolicy.RulesVersion = 6`
is already deployed on every updated installation, and a writer that gated these leaves on 6 would write leaves no
deployed document grants and have every stage report it sends denied (F10).

The node, as rules generation 7 (SA5.0) grants it:

```
/spend/<day>/repos/<slug>/byModel/<escaped model>/{micros,in,out,cr,cw,notional,rep,tlo,thi}
```

`in`, `out`, `cr` and `cw` are the gateway's token counts for the model, and `micros` is the whole of its dollars.
Amounts are integer micro-dollars, as everywhere in the database; every leaf is increase-only and at most MAXRES per
write, and a writer that lacks a generation-7 deployment writes only the leaves it always wrote (SA5.1, F10).

## `micros`, `notional` and the billed figure

- **`micros` is the whole**: the model's billed dollars and its notional ones together, which is what every reader
  before this change took it for. It does not change meaning.
- **`notional` is the subscription part of `micros`**: what a tier-A call or an `oauth` run is accounted as rather
  than charged (D140, C1.16), summed over the same flushes.
- **The billed figure is `micros − notional`, and a reader clamps it at zero.** It can only dip below zero in the
  mixed-version window RS8 admits: a run in flight on an older image still writes its notional dollars into `micros`
  alone, so for as long as that run lasts the day's `notional` can exceed the part of `micros` it explains. Clamping
  is what keeps a report from printing negative dollars; the day is left wrong until that run ends, stated and not
  fixed.
- **From the generation that grants them, `notional` is written with every `micros` write of that model, a 0
  included** (RS8). A gateway-only model therefore has `notional: 0` and its whole `micros` is billed.
- **The leaf's presence is the signal, not its value.** A day written before the leaf existed reads `notional` as 0
  exactly like a gateway-only model does, and the two are different facts: one has a billed figure that is unknown, the
  other has a billed figure equal to its `micros`. `budget.ModelUse` and `budget.ModelTotals` carry `NotionalSet`
  beside the value for that, true when the field was on the wire; it is a Go-side signal and is never written. A day
  without the leaf is "not recorded", and a reader falls back to what it did before the leaves existed (for
  `fugaro report --by model`, `splitModel`'s proportioning).

## `rep`, `tlo` and `thi`: the reported cost and the band

- **`rep` is the cost the provider reported** for the calls that came back with one — `usage.cost` on the response —
  summed per model. It carries **no route fee**: it is the provider's own figure, and `route_fee_pct` is Fugaro's
  charge on top of it, not part of it.
- **`tlo` and `thi` are the table's charge over those same calls**, at the cheapest and the dearest end of the tier
  that served them (SA1.6's `pricing.Model.Lo` and `Hi`, per dimension and cache included), also **with no fee**. A
  model the catalog does not price has `tlo = thi`.
- **A call with no report is in none of the three.** Only the calls that carried a report have a provider figure to
  compare, so the band is over reported calls only (D2650); the calls without one are counted in the run's
  `unreported_calls` and in `micros` as always.
- **What they are for.** The drift alarm (R6, D2649, D2650) puts `rep` next to the band: it warns when the reported
  cost is more than 10% above `thi` or more than 10% below `tlo`, over today and yesterday, for a model with at least
  $1 of spend in that window. Read against `thi`/`tlo` and not against a single estimate because the price table's
  spread over a tier can be many times wide, and a 10% alarm against the top of it would fire on nearly every call.
- **Absent means zero here too**, and there is no "recorded" flag beside them: unlike `notional`, a 0 and an absent
  `rep` are the same fact — no reported cost — so a reader needs no presence signal. A day without the leaves is quiet
  rather than alarming.

## Through the archive

`fugaro history rollover` archives a day as a `spendDaily/<date>_<slug>` document, whose `byModel` rows carry the same
four leaves as `notional`, `rep`, `tlo` and `thi` (`budget.ModelTotals`, `ToFields`/`FromFields`), so `fugaro report`
reads them off finished days exactly as `doctor` and `watch` read them off the live counters. A row's four leaves are
written only when the day recorded them, which the reader reports as `NotionalSet`: a document from before this change
keeps its old shape, and a report over it still splits its model dollars the old way.
