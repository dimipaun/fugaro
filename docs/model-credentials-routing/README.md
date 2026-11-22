# Model credentials routing

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-21. Checked against `origin/main` at `dfe066618`.

How a model is reached is a property of the model, set once in the project: a routing table
matches a model name or pattern and lists an ordered chain of credential sources (the launcher's
pool, a shared pool, the project's Anthropic key, a provider key, Vertex), and the first
available source that is not exhausted serves the call. Subscription tokens live only in pools,
a pool belongs to a person, a service account or the project, and a run whose every source is
exhausted suspends itself and is resumed at the reset. It is the fourth spec of the series after
[every model key through the credentials service](../keys-through-service/README.md), always a
budget and [credentials belong to models](../credentials-belong-to-models/README.md), and it
supersedes the last one's craft access (D3170).

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec, as received from the owner on 2026-11-21. No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: the capability map (six modules), assumptions A1–A9, findings F1–F45, decisions D3170–D3188 with the owner's answers, resolutions R1–R18, open items O1–O6, UNVERIFIED U1–U7. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contracts C1 to C3, documents tasks A1 to A3, 55 tasks in six modules, integrations I1 and I2 (63 runs over two releases, and three contract changes that run only if raised); levels, critical path, shared files, the edges broken and kept; Checks 870 to 875 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3170–D3199** (D3170–D3188 used), checks **870–879** (870 to
875 used). Read on 2026-11-21: the highest decision on `main` was D3160, in the block
D3160–D3169 of managed-flag-ergonomics, which also holds checks 860–869; no diff of the open
pull requests wrote a decision at or above D3170 or a check at or above 870.

**Why a new folder.** `docs/credentials-belong-to-models` holds its own spec, received on
2026-11-20, and stays as the record of what was decided then. This is a later spec of the
owner's that supersedes part of it, so it has its own folder, and the plan's first task (A1)
writes into that folder what this one replaces.

**Owner answers of 2026-11-21:** D3170–D3188, eighteen as recommended and one not (D3184: a short
wait inside the job is kept under a floor, where the recommendation was always to suspend). He
amended one the same day: D3176, where `--token` becomes a narrow `--pool` instead of going. Four
answers reverse or amend an earlier ruling and say so in their row: D3170 and D3171 (D3060 and
D3068 of credentials belong to models), D3176 (D3069), D3177 (D1622 and D1636 of model crafts)
and D3180 (D2974 of workspace credentials). The capability map was approved the same day.

**What it changes in the plans before it** (reconciliation, "What this changes in other plans"):
every not-merged task of credentials belong to models is withdrawn and carried by a task here,
and its contract C1 (draft #3460) is withdrawn; four decisions of workspace credentials are
amended; D138, D152 to D154 of the credentials service are replaced, re-keyed or amended. None
of those plans' own documents is edited by this pull request: the plan's tasks A1, A2 and A3 do
it.

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. The withdrawn tasks of credentials belong to models are stopped by the owner, by hand,
before any launches (O1). Release 2 (X1 to X5) is not launched before release 1 is cut. Checks
870 to 875 are owner-run.
