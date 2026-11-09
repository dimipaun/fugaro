# Run digest: 2026-W43

Runs of this repository that finished in 2026-W43 (2026-10-19 to 2026-10-25): 7 in all, 7 shown, 4 flagged. Billed $4.20, notional $0.15.

| run | status | pr | base | reviews | billed | notional | flags |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `cccc` | succeeded | #103 | yes | 2 | $2.00 | $0.00 | fix_rounds |
| `bbbb` | halted | - | no | 0 | $0.40 | $0.10 | halt:run_cap |
| `dddd` | succeeded | #104 | yes | 0 | $0.30 | $0.00 | review_calls |
| `eeee` | succeeded | #105 | yes | 0 | $0.20 | $0.00 | subagents |
| `aaaa` | succeeded | #101 | yes | 1 | $1.25 | $0.00 | - |
| `ffff` | succeeded | #106 | yes | 0 | $0.05 | $0.05 | - |
| `1111` | succeeded | - | no | 0 | $0.00 | $0.00 | - |

## Failures

Why the period's runs did not succeed, over every run of the period, not only the rows above.

| class | runs | billed | example |
| --- | --- | --- | --- |
| `halted_budget` | 1 | $0.40 | `bbbb` |

## Stages

Model calls and sub-agents by stage, over every run of the period.

| stage | calls | agents |
| --- | --- | --- |
| fix | 30 | 0 |
| implement | 125 | 6 |
| provision | n/a | n/a |
| review | 447 | 7 |
| review_first | 20 | 1 |

## Summary

```json
{"period":"2026-W43","start":"2026-10-19","end":"2026-10-25","runs":7,"shown":7,"flagged":4,"billed":"4.200000","notional":"0.150000","review":"0.740000","halts":1,"flags":{"fix_rounds":1,"halt":1,"review_calls":1,"subagents":1},"failures":{"halted_budget":1},"recipes":{"other":"0.300000"}}
```
