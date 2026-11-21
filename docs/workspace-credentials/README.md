# Workspace credentials

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-21. Checked against `origin/main` at `2e8c7cb0b`.

A person's tokens and the shared keys, held once at the workspace. Most of the addendum was built
under [the workspace's credentials service](../workspace-service/README.md) and
[keys through the service](../keys-through-service/README.md) after it was written. This folder
plans what is left: sharing a provider key at the workspace, and the personal half, a person's own
Claude token stored once at the workspace and served only to the runs that person launched.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The addendum, as received on `main` from #3250, with its decisions D2960–D2979 answered on 2026-11-19. No typo was fixed and this plan does not edit it |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A5, findings F1–F23, decisions D3140–D3145 with the owner's answers, resolutions R1–R12, open items O1–O5, UNVERIFIED U5–U9 (the spec's own §7 holds U1–U4). **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contracts C1 and C2, tasks WC0–WC13, integration I1 (17 runs, and two contract changes that run only if raised); levels, critical path, shared files, the edges broken and kept; Checks 850 and 851 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Numbers reserved:** decisions **D3140–D3159** (D3140–D3145 used), checks **850–859** (850 and 851
used). Read on 2026-11-21: the highest decision reserved on `main` and in the open pull requests
was D3139, the highest check 849.

**A number to read with care.** D2960–D2969 are allocated twice on `main`: by this folder's spec
and by `docs/runs-ls-speed`. In this folder they always mean the spec's own decisions
(reconciliation F1, R1). Nothing is renumbered.

**Owner answers of 2026-11-21:** D3140–D3145, every one as recommended.

**What a reader should not take from here:** no task is approved; each needs the owner's go by
name. No personal task is launched before the owner has answered the review of the threat model
(WC0). Checks 850 and 851 are owner-run after the release that carries the personal half. A
project in no workspace is unchanged (D2966).
