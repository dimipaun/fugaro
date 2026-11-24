# Model credentials routing

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-21. Checked against `origin/main` at `dfe066618`, then rebased onto `aa75a696c`.

How a model is reached is a property of the model, set once in the project: a routing table
matches a model name or pattern and lists an ordered chain of credential sources (the launcher's
pool, a shared pool, the project's Anthropic key, a provider key, Vertex), and the first
available source that is not exhausted serves the call. Subscription tokens live only in pools,
a pool belongs to a person, a service account or the project, and a run whose every source is
exhausted suspends itself and is resumed at the reset. It is the fourth spec of the series after
[every model key through the credentials service](../keys-through-service/README.md), always a
budget and [credentials belong to models](../credentials-belong-to-models/README.md), and it
supersedes the last one's craft access (D3170).

**Amended 2026-11-23 by [repo-override rulings](../repo-override-rulings/README.md) (D3237, lift
L8):** the repository does **not** keep its model choice, reversing the half of F7 that said so
(its amendment in [reconciliation.md](reconciliation.md), and that folder's F6, R10 and task A1).
The project layer chooses each role's model — `agent.model`, `agent.models.*`,
`agent.crafts.*.model` and, by D3291, `agent.levels.lN.model` — and the routing table still decides
which source serves it, as A1 reads "the routing table decides". For one release a repository's
value is read, dropped and named; the next release refuses it with `fugaro models use --layer
project` as the fix. Everything else here stands: the table, the pools, the chain, the pause and
the migration are unchanged, and `budget.allowed_models` still narrows per repository.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec, as received from the owner on 2026-11-21. No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: the capability map (six modules), assumptions A1–A9, findings F1–F67, decisions D3170–D3197 with the owner's answers, resolutions R1–R26, open items O1–O17 (O13 to O16 closed), UNVERIFIED U1–U8 (U2 withdrawn), and the amendment of 2026-11-23 (D3237) of F7. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contracts C1 to C3, documents tasks A1 to A3, 75 tasks in six modules, integrations I1 and I2 (83 runs over two releases, and three contract changes that run only if raised); levels, critical path, shared files, the edges broken and kept; Checks 880 to 886 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3170–D3199** (D3170–D3197 used), checks **880–889** (880 to
886 used). Read twice on 2026-11-21. The first reading found the highest decision on `main` to
be D3160, in managed-flag-ergonomics' block D3160–D3169, and checks 860–869 taken, so this plan
took checks 870–879. Before the branch was pushed, console-fixes (#3540) landed on `main` with
checks 870–879 and decisions D3200–D3229, so every check here was moved up by one block; the
decisions did not collide. No diff of the open pull requests wrote a decision in D3170–D3199 or
a check at or above 880.

**Why a new folder.** `docs/credentials-belong-to-models` holds its own spec, received on
2026-11-20, and stays as the record of what was decided then. This is a later spec of the
owner's that supersedes part of it, so it has its own folder, and the plan's first task (A1)
writes into that folder what this one replaces.

**Owner answers of 2026-11-21:** D3170–D3193. Twenty-one were answered as recommended and three
were not: D3184 (a short wait inside the job is kept under a floor, where the recommendation was
always to suspend), D3190 (a chain's pauses are bounded by total paused time, not by a count)
and D3193 (Gemini on Vertex is added, where the recommendation was to leave it out). Four were
amended the same day, each by the owner's own answer: D3176 (`--token` becomes a narrow `--pool`
instead of going); D3174, twice after review (the credentials service calls Vertex itself, and
Vertex is then capped as far as a key is and no further); D3179 after review (personal pools
live in the project, not at the workspace); and D3190 after review (a minimum pause of 30
minutes). The answers that reverse or amend an earlier ruling say so in their row or in "What
this changes in other plans": D3170 and D3171 (D3060 and D3068 of credentials belong to
models), D3174 (D3066), D3175 (D1637), D3176 (D3069), D3177 (D1622 and D1636 of model crafts),
D3183 (D3064), D3184 (D138 of the credentials service), D3189 (D3136 of launcher does not mint)
and D3191 (D2976 of workspace credentials, by one case). The capability map was approved the same
day.

**After the pull request opened** the owner answered the four questions round 3 had left
(O13 to O16) as D3194 to D3197: three as recommended, and D3195 not (a pull request whose chain
is paused gets a fifth word, `paused`, where the recommendation was to call it live). That added
one task (PR13) and lifted the two waits on his answers. These last edits were not reviewed.

**Review round 1** (a fresh independent reviewer) confirmed about 60 code claims and found that a
gateway cap cannot bind Vertex on the job's identity (F23, so D3174 was amended), that a personal
pool cannot rest on a project slip (F51, R7 and R8 rewritten), that a resume widens what the
orchestrator may be issued (F52, D3189), that the policy's blocks decode strictly (F49, R21) and
that nothing bounded repeated pauses (F53, D3190). It added F46 to F54, six tasks (PL9, PL10,
CS12, CS13, CK2, X6), an ordering of every writer of the pinned CLI reference, and tests for
each trust boundary it probed.

**Review round 2** (another fresh reviewer) confirmed the graph, the counts and the quoted
decisions, and found that a chain cannot span the project's and the workspace's services (F55,
so D3179 was amended and the plan no longer waits on workspace credentials), that a hand-built
call escapes the run's cap for Vertex as for a key (F56, D3174 amended again, R23), that a
release-behind gateway would choke on a new answer member (F57, R24), that the spec's Gemini
example could not run (F58, D3193) and that a resume crossed D2976 (F59, D3191). It added F55
to F59, nine tasks (CS14, PR10, PR11, GV1 to GV4, X7, X8) and D3191 to D3193.

**Review round 3** (a third fresh reviewer; the last) confirmed the graph, the counts and that
every decision, resolution and finding has a row, and found that not every run has a slip and no
slip names a service account (F60), that database rules cannot hold a designation (F61, R26),
that a policy version bump would stop release-1 images (F63, R25 rewritten), that a paused chain
was in flight to no guard (F64), that the refusal's new members had no carrier (F67) and that
the check numbers had been taken on `main`. It added F60 to F67, four tasks (CS15, PR12, X9,
X10) and four questions for the owner (O13 to O16). **Its fixes are the last commit and were not
reviewed again** (O17).

**What it changes in the plans before it** (reconciliation, "What this changes in other plans"):
every not-merged task of credentials belong to models is withdrawn and carried by a task here,
except CM2 (per-repository crafts), which is not replaced: one table per project, and RT2 carries
admission; its contract C1 (draft #3460) is withdrawn; workspace credentials is unchanged but for
one case of D2976; D138, D152 to D154 of the credentials service are replaced, re-keyed or amended; D3136
of launcher does not mint is widened by one case. None
of those plans' own documents is edited by this pull request: the plan's tasks A1, A2 and A3 do
it.

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. The withdrawn tasks of credentials belong to models are stopped by the owner, by hand,
before any launches (O1). Release 2 (X1 to X10) is not launched before release 1 is cut. Checks
880 to 886 are owner-run.
