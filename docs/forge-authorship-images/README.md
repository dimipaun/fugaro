# Forge, authorship and images

**Status: plan only; nothing is implemented; no task is approved.** Decisions D600–D649 are
**answered** (2026-10-29): every answer is the recommendation except D618, where the owner chose
shared images across repos, scoped to repos with an identical resolved dependency set (plan C7 and
C8). Tension T6 and open item O6 are resolved (2026-10-29): shared images stay under user control, a
divergence is proposed and confirmed, never forked silently (not the recommendation). Answering a decision does not approve
a task; each task, slice A included, needs the owner's own go.

**Spec:** "Fugaro Forge, Authorship & Image Spec", draft, October 2026. **Spec owner:** the Fugaro
owner. **Reconciled against** `origin/main` at `c192e8766` (2026-10-28).

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The owner's spec, as received, with the name substitutions below |
| [reconciliation.md](reconciliation.md) | Findings F1–F46 against the code, decisions D600–D649 (answered 2026-10-29), resolutions R1–R4, tensions T1–T6, open items O1–O6, UNVERIFIED U1–U8. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Slices A (authorship and trailers), B (forge), C (repositories and images, including shared images by dependency-set hash), D (change sets, `ee/`), E (people and tokens), and X1 (the live-check entries); C2, E2 and E3 withdrawn in review; levels L0–L7, the critical path, the shared-files table, Checks 230–235 (233 withdrawn). **Nothing in it is approved** |

## The only deviation from "verbatim"

`spec.md` is the spec as received **except** that real people's and a customer's names were
replaced by placeholders before it reached this repository, under the owner's standing rule of
2026-10-23 (no teammates' names and no customer or organisation names in docs, PRs or commits):

| In the original | In `spec.md` |
| --- | --- |
| The large customer project's name | `[a large customer project]` |
| Its three sibling projects' names | `[its sibling projects]` |
| Three developers' first names | `developer A`, `developer B`, `developer C` |
| A token example named after a developer | `dev-a-max` |
| The customer's own command-line tool, by name | `the customer's own CLI` |

No typo was fixed; nothing else was changed.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D600–D679** | this work's block. **D600–D649** written (all answered 2026-10-29; D640–D645 added in review round 1, D646–D648 in round 2, D649 in round 3); **D650–D679** reserved and unused. Above the highest decision on `main` (D549, ee-reorg's reserved block D520–D549); no open PR's docs use D600–D679. Another spec reconciliation running at the same time takes its numbers from D550 and Check 210, below this block (it does not reach D600 or Check 230) |
| **Checks 230–249** | this work's block. **230–232 and 234–235** proposed by the plan (written by task X1, run by the owner, NOT RUN); **233** withdrawn in review round 1 (reserved, unused); **236–249** reserved and unused. Above the highest check on `main` (201) |
