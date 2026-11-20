# CI selects tests from what changed

**Status: plan only; nothing is implemented; no task approved.** See [docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date: 2026-11-20. Checked against `origin/main` at `38ce5c0e2`. Round 1 of the independent review amended the reconciliation and replaced the contract-first shape of the plan.

CI runs the whole suite on every dispatch: 27 shards, about 175 runner-minutes, 16 minutes of wall time. This plan makes it run what a change can reach: a selector maps the changed files to the packages that import them and, through a recorded read-map, to the tests that read them by path; the full suite stays as the release gate's requirement and as a nightly run.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. Written by the coordinator from the owner's direction of 2026-11-20; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1-A4, findings F1-F15, decisions D3040-D3043 with the owner's answers, resolutions R1-R12 |
| [implementation-plan.md](implementation-plan.md) | Tasks CS1-CS9 (CS2 in two parts, CS6 with a companion CS6b, CS7 with a companion CS7b): 12 tasks, no contract (the pass was weighed and declined, §6), one of them owner-run |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3040-D3059** (D3040-D3043 used), checks **800-809** (800 used). Read on 2026-11-20: the highest decision on `main` and in the open pull requests was D3019; checks 780-789 are reserved by the open pull request #3299 (keys-through-service), 790-799 by #3302 (always-a-budget), and the highest on `main` is 772; decisions D3020-D3026 and D3039 are #3302's.

**Owner answers of 2026-11-20** (given in this session through the structured question at the owner's terminal): D3040-D3043 all as recommended: the release gate requires a full run on the tag's commit; one scheduled nightly full run (a narrow exception to the dispatch-only ruling of 2026-11-07); the selector is a Go program with tests under `scripts/`; a recorded read-map refreshed by the full run.

**Already landed, and what it taught.** #3301 put the longest shards first and gated the live-script shards on what they read; the next run showed GitHub does not honour the matrix order and the slowest live shard (11m40) set the wall time. That gate is replaced by the selector (R9).

Nothing here is approved. The owner says go by task name; CS6 is copied into `.github/workflows/ci.yml` only by the owner's word.
