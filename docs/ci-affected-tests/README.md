# CI selects tests from what changed

**Status: implemented. CS1-CS7b, CS6 and CS6b are on `main` and `.github/workflows/ci.yml` carries the selector (CS6's copy-in, 2026-11-22); CS8 is the change that writes this paragraph and CS9 (Check 800, owner-run) is left.** *The status as first written, kept:* **Status: plan only; nothing is implemented; no task approved.** See [docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date: 2026-11-20. Checked against `origin/main` at `38ce5c0e2`. Three rounds of independent review amended the reconciliation and replaced the contract-first shape of the plan (the round-3 remainder is under Open items of the pull request).

CI runs the whole suite on every dispatch: 27 shards, about 175 runner-minutes, 16 minutes of wall time. This plan makes it run what a change can reach: a selector maps the changed files to the packages that import them and, through a recorded read-map, to the tests that read them by path; the full suite stays as the release gate's requirement and as a nightly run.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. Written by the coordinator from the owner's direction of 2026-11-20; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1-A4, findings F1-F15, decisions D3040-D3043 with the owner's answers, resolutions R1-R12 |
| [implementation-plan.md](implementation-plan.md) | Tasks CS1-CS9 (CS2 in two parts, CS6 with a companion CS6b, CS7 with a companion CS7b): 12 tasks, no contract (the pass was weighed and declined, §6), one of them owner-run |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3040-D3059** (D3040-D3043 used), checks **800-809** (800 used). Read on 2026-11-20: the highest decision on `main` and in the open pull requests was D3019; checks 780-789 are reserved by #3299 (keys-through-service, merged), 790-799 by #3302 (always-a-budget, merged), and the highest on `main` is 772; decisions D3020-D3026 and D3039 are #3302's.

**Owner answers of 2026-11-20** (given in this session through the structured question at the owner's terminal): D3040-D3043 all as recommended: the release gate requires a full run on the tag's commit; one scheduled nightly full run (a narrow exception to the dispatch-only ruling of 2026-11-07); the selector is a Go program with tests under `scripts/`; a recorded read-map refreshed by the full run.

**The one exception to the 2026-11-07 ruling, and the read-map's refresh.** CI is dispatch-only by the owner's ruling of 2026-11-07 (no run per push, no run per pull request), and D3041's nightly full run is the one exception to it: a `schedule:` trigger is neither a push nor a pull request, so what the ruling protected — a run for every push — stays out, and the nightly is what a selected run measures itself against and weighs its shards with. `ci.yml`'s `run-name` and its selector branch on `github.event_name == 'schedule'`, so the nightly names itself `ci (full)` and takes no input (R11). The committed read-map, `read-map.json`, is refreshed from that run's artifacts by a **normal pull request and never by a push from CI** (R10): download the run's `reads-*` artifacts and merge them in with `go run ./scripts/ci-readmap --from-artifact <dir>`.

**Already landed, and what it taught.** #3301 put the longest shards first and gated the live-script shards on what they read; the next run showed GitHub does not honour the matrix order and the slowest live shard (11m40) set the wall time. That gate is replaced by the selector (R9).

Every task starts on the owner's word and not on a document: the owner says go by task name, and CS6 was copied into `.github/workflows/ci.yml` only by that word, given on 2026-11-22. The one task left is CS9, which is owner-run.
