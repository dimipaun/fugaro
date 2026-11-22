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
| [reconciliation.md](reconciliation.md) | The amended spec: the capability map (six modules), assumptions A1–A9, findings F1–F54, decisions D3170–D3190 with the owner's answers, resolutions R1–R22, open items O1–O10, UNVERIFIED U1–U7 (U2 withdrawn). **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contracts C1 to C3, documents tasks A1 to A3, 61 tasks in six modules, integrations I1 and I2 (69 runs over two releases, and three contract changes that run only if raised); levels, critical path, shared files, the edges broken and kept; Checks 870 to 875 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3170–D3199** (D3170–D3190 used), checks **870–879** (870 to
875 used). Read on 2026-11-21: the highest decision on `main` was D3160, in the block
D3160–D3169 of managed-flag-ergonomics, which also holds checks 860–869; no diff of the open
pull requests wrote a decision at or above D3170 or a check at or above 870.

**Why a new folder.** `docs/credentials-belong-to-models` holds its own spec, received on
2026-11-20, and stays as the record of what was decided then. This is a later spec of the
owner's that supersedes part of it, so it has its own folder, and the plan's first task (A1)
writes into that folder what this one replaces.

**Owner answers of 2026-11-21:** D3170–D3190. Nineteen were answered as recommended and two were
not: D3184 (a short wait inside the job is kept under a floor, where the recommendation was
always to suspend) and D3190 (a chain's pauses are bounded by total paused time, not by a count).
Two were amended the same day: D3176 by the owner (`--token` becomes a narrow `--pool` instead of
going) and D3174 after review round 1, confirmed by the owner (the credentials service calls
Vertex itself, because a cap in the job's gateway cannot bind an agent that holds the job's
identity). D3189 and D3190 were asked after that round. The answers that reverse or amend an
earlier ruling say so in their row or in "What this changes in other plans": D3170 and D3171
(D3060 and D3068 of credentials belong to models), D3174 (D3066), D3175 (D1637), D3176 (D3069),
D3177 (D1622 and D1636 of model crafts), D3179 and D3180 (D3141, D3142, D3144 and D2974 of
workspace credentials), D3183 (D3064), D3184 (D138 of the credentials service) and D3189 (D3136
of launcher does not mint). The capability map was approved the same day.

**Review round 1** (a fresh independent reviewer) confirmed about 60 code claims and found that a
gateway cap cannot bind Vertex on the job's identity (F23, so D3174 was amended), that a personal
pool cannot rest on a project slip (F51, R7 and R8 rewritten), that a resume widens what the
orchestrator may be issued (F52, D3189), that the policy's blocks decode strictly (F49, R21) and
that nothing bounded repeated pauses (F53, D3190). It added F46 to F54, six tasks (PL9, PL10,
CS12, CS13, CK2, X6), an ordering of every writer of the pinned CLI reference, and tests for
each trust boundary it probed.

**What it changes in the plans before it** (reconciliation, "What this changes in other plans"):
every not-merged task of credentials belong to models is withdrawn and carried by a task here,
and its contract C1 (draft #3460) is withdrawn; four decisions of workspace credentials are
amended; D138, D152 to D154 of the credentials service are replaced, re-keyed or amended; D3136
of launcher does not mint is widened by one case. None
of those plans' own documents is edited by this pull request: the plan's tasks A1, A2 and A3 do
it.

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. The withdrawn tasks of credentials belong to models are stopped by the owner, by hand,
before any launches (O1). Release 2 (X1 to X6) is not launched before release 1 is cut. Checks
870 to 875 are owner-run.
