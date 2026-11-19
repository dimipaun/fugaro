# The workspace's credentials service

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-19. Checked against `origin/main` at `21b0a622b`.

Finishing the deployment of the workspace's credentials service: the Cloud Run service, the writer
of its node, the cap meter, and the cascade that lets a member project's run take a credential from
the workspace when the project holds none of its own. It is the prerequisite
[workspace credentials](../workspace-credentials/spec.md) names in D2968, and it completes
[the workspaces design](../workspaces/README.md).

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The brief, as received. The coordinator wrote it from the owner's rulings of 2026-11-19; no typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A6, findings F1–F31, decisions D2980–D2991 with the owner's answers, resolutions R1–R15 (R12 is the last of the first draft; R13–R15 came with review round 1), open items O1–O6, UNVERIFIED U1–U6. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contracts C1–C3, tasks WS1–WS22, integration I1 (26 runs, and three contract changes that run only if raised); levels, critical path, shared files, the edges broken and kept; Checks 770–773 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D2980–D2999** (D2980–D2991 used), checks **770–779** (770–773
used). Read on 2026-11-19: the highest decision on `main` and in the open pull requests was D2979,
the highest check 760 with 761–769 reserved.

**Owner answers of 2026-11-19:** D2980–D2983, D2986 and D2987 as recommended. **D2984 is not the
recommendation** (every credential kind at once, where model credentials first was recommended).
**D2985 is the recommendation with the owner's addition**: the keep-warm beat can be turned off.
**Review round 1** added D2988–D2991, each answered the same day as recommended, and **amended
D2987**: the reconciliation alarm is deferred (D2991), because it cannot be computed where D2987
placed it.

**What a reader should not take from here:** no task is approved; each needs the owner's go by name.
Checks 770–773 are owner-run after the release that carries the service. Per-person tokens at the
workspace (D2969) and moving provider keys off job mounts (D2975) are not in this plan.
