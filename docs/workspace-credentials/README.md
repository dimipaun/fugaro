# Workspace credentials

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-21. Checked against `origin/main` at `2e8c7cb0b`, re-read at `72859a77d` and `bbcebb1cb`.

A person's tokens and the shared keys, held once at the workspace. Most of the addendum was built
under [the workspace's credentials service](../workspace-service/README.md) and
[keys through the service](../keys-through-service/README.md) after it was written. This folder
plans what is left: sharing a provider key at the workspace, and the personal half, a person's own
Claude token stored once at the workspace and served only to the runs that person launched.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The addendum, as received on `main` from #3250, with its decisions D2960–D2979 answered on 2026-11-19. No typo was fixed and this plan does not edit it |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, findings F1–F25 (F24 and F25 came with review round 1, which also amended F23), decisions D3140–D3148 with the owner's answers, resolutions R1–R13 (R13 came with review round 1, which amended R3, R8 and R11), open items O1–O6, UNVERIFIED U5–U10 (the spec's own §7 holds U1–U4). **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contracts C1 and C2, tasks WC0–WC14, integration I1 (18 runs, and two contract changes that run only if raised); levels, critical path, shared files, the edges broken and kept; Checks 850 and 851 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Amended 2026-11-22 by [model credentials routing](../model-credentials-routing/README.md), and
nothing here changes.** That plan gives a person's own token a model call, served through that person's
personal pool in the project (its PL4, D3179), so the workspace is not the only place a personal token
is served from — but a project with a routing table serves the call from its own service, and a person's
token at the workspace still follows the route (D3141), which stands as written. Its D3191 amends
**D2976** by one case in a project: a run that resumes a person's paused run draws on that person's pool,
where a run launched by a service account is otherwise served none. No task of this plan is withdrawn,
none is amended, and none waits on that plan.

**Numbers reserved:** decisions **D3140–D3159** (D3140–D3148 used), checks **850–859** (850 and 851
used). Read on 2026-11-21: the highest decision reserved on `main` and in the open pull requests
was D3139, the highest check 849.

**A number to read with care.** D2960–D2969 are allocated twice on `main`: by this folder's spec
and by `docs/runs-ls-speed`. In this folder they always mean the spec's own decisions
(reconciliation F1, R1). Nothing is renumbered.

**Owner answers of 2026-11-21:** D3140–D3148, every one as recommended. **Review round 1**
changed no answer. It corrected the cost stated with D3143, moved the intake's address off the
member's pin (R13), gave one answer for a run in flight whose launcher is offboarded (R3), and
added the replay risk of F24 to what the threat model must state. **Review round 2** added
D3146 (a removed person's run halts within a minute) and D3147 (a failed grant starts the run on
the shared entries), both answered the same day as recommended. It corrected how a grant and its
claim secret travel (F14, C1), held `--workspace` to the workspace's own project config (R13),
and added task WC14. **Review round 3**, the last, added D3148 (a person names the workspace's
project; there is no `--workspace` flag), answered the same day as recommended. It restated how
far the replay of F24 reaches, narrowed the accessor of R6 to the one name served, and gave C2
the configuration WC14 fills. Its fixes are the last commit and were not reviewed again.

**WC0's review, answered 2026-11-22.** An independent security review of the threat model found
eight things, and the owner answered each (O1, answered): a claim deadline counted from the grant for
a workspace slip (WC4), a grant bound to the members the person is a launcher of, which reopens D3145
(WC4, WC5), the intake's address read from a source only the workspace's admins write (WC9), a new
residual risk 10, and wording corrections. See [threat-model.md](threat-model.md) §11. The personal
tasks, WC2 onward, are launchable by their plan edges.

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. Checks 850 and 851 are owner-run after the release that carries the personal half. A
project in no workspace is unchanged (D2966).
