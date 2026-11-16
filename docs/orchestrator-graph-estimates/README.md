# Orchestrator, graphs and estimates

**Status: plan only; nothing is implemented; no task approved.**

Owner: Dimi Paun. Spec dated 2026-11-16 ("Fugaro design decisions — Nov 16, 2026"); reconciled and planned on 2026-11-16 against `origin/main` at `c6a04b0eb`.

| File | Role |
| --- | --- |
| [`spec.md`](spec.md) | The spec, verbatim. No typo was fixed. |
| [`reconciliation.md`](reconciliation.md) | The amended spec: the capability map, assumptions A1–A8, findings F1–F58, decisions D2700–D2747 with the owner's answers, open items, UNVERIFIED. **Where it disagrees with `spec.md`, it wins.** |
| [`implementation-plan.md`](implementation-plan.md) | 39 tasks in five modules (`daemon-release`, `graph-import`, `run-events`, `estimates`, `backlog`), five levels; two contract breaks weighed and undone as no gain. Nothing in it is approved. |
| [`implementation-plan.graph.json`](implementation-plan.graph.json) | The plan's tasks as the graph `fugaro plan graph` reads. |

Number blocks taken: decisions **D2700–D2749** (D2700–D2747 used, D2748–D2749 reserved) and checks **700–709** (700–703 used, 704–709 reserved).

Related: [`docs/stateless-orchestrator/`](../stateless-orchestrator/README.md) (the daemon this builds on), [`docs/contract-first-decoupling/`](../contract-first-decoupling/README.md) (the graph's schema 3), [`docs/forge-labels/`](../forge-labels/implementation-plan.md) (the credentials service label route that G3 reads and B3 follows), [`docs/backlog/`](../backlog/README.md) (folded in by B2).
