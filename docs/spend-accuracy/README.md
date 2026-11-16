# Spend accuracy

**Status: plan only; nothing is implemented; no task approved.** Owner: Dimi Paun. Spec dated 2026-11-16, received
and reconciled 2026-11-15 against `origin/main` at `4ce05d949`. The current state of every spec is on
[docs/status.md](../status.md).

The spec: Fugaro's recorded spend for OpenRouter's DeepSeek models was about 46% of what OpenRouter billed on the
dogfood project. The gateway should settle on the provider's reported cost, price conservatively where it has none,
take `model_prices` in units that cannot be misread, keep billed dollars billed in reports, and raise an alarm when
settled and estimated spend drift apart.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The spec as received, byte for byte (no typo was fixed). **Where [reconciliation.md](reconciliation.md) disagrees with it, the reconciliation wins** |
| [reconciliation.md](reconciliation.md) | Assumptions A1-A5, findings F1-F20, resolutions RS1-RS4, decisions D2640-D2649 with the owner's answers of 2026-11-15, open items O1-O2, UNVERIFIED U1-U7 |
| [implementation-plan.md](implementation-plan.md) | Six slices, 16 tasks (SA1.0-SA6.1), levels L0-L3, the critical path, the shared-files table, Checks 650-651, owner-run step OR1. **Nothing in it is approved** |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads |

**Headline:**
- `price_policy: highest` exists, but `fugaro update --repo` still refuses it: `catalogShipped` was never flipped
  after 0.7.5 (F3).
- The dogfood override prices v4.1-flash cache reads at $0.000588/M (F5).
- `diagnose` already compares reported with charged spend per run (F1).
- The day counters mix billed and notional dollars in one leaf per model (F9).

**Numbers taken:** D2640-D2659 (D2640-D2649 used), Checks 650-659 (650-651 used). The highest in use were D2639 (on
`main`, `docs/config-ownership`) and Check 640 (`docs/forge-labels`).

**Waits on something external:** OR1 (delete the dogfood override and run `fugaro update`) and Checks 650-651 are
the owner's. U1 (whether OpenRouter's Anthropic-compatible endpoint returns `usage.cost` unasked) decides how much
D2640 settles, and Check 650 measures it.
