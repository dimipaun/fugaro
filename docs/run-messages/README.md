# Messaging a running run

**Status (2026-11-18): plan only; nothing is implemented; no task approved.** The current state is in [docs/status.md](../status.md). Every decision (D2940–D2947) is answered, all as recommended, and nothing is left for the owner to rule.

**Spec owner: the owner (Dimi Paun). Date: 2026-11-18.** The spec was drafted by Claude from the owner's conversation of that day and committed as written. Checked against `origin/main` at `0db80aa40`.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as received. Not edited |
| [reconciliation.md](reconciliation.md) | The spec against the code (F1–F21), the decisions D2940–D2947 with the owner's answers, the delivery rules they make, UNVERIFIED (U1–U4). Wins where it disagrees with the spec |
| [implementation-plan.md](implementation-plan.md) | Tasks RM0–RM10 in three slices, four levels, the critical path RM0 → RM2 → RM4 → RM7, and the owner-run Check 740 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph, as `fugaro plan graph` reads it |

## In one paragraph

`fugaro tell RUN "text"` lets any launcher send a short message to a run that is going. The run never waits for one. The message lives under `tokens/<slug>/<run>/messages/`, a prefix no job account can write, so an agent cannot forge one. The runner puts it into the next coder stage's prompt and never into a review's. `--now` yields the agent between tool calls and resumes the same session with the message, and `--wrap-up` does the same and then ends the coder stage. Every message's fate is in the record, the report and `diagnose`.

## Numbers

Decisions **D2940–D2947** are taken and **D2948–D2959** are reserved. **Check 740** is taken and **741–744** are reserved. Findings (`F`) and unverified items (`U`) are numbered within this folder.

## Typos fixed in spec.md

None.
