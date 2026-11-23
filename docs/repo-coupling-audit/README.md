# Repo-coupling audit

**Status: plan only; nothing is implemented; no task approved.** The static report is written. The
live checks, two loose ends (U1/U2 and a docs fix) and the report's live columns are planned and wait for the owner's go. See
[docs/status.md](../status.md) for the current table.

Spec owner: Dimi Paun (the owner). Received and reconciled on 2026-11-22, checked against
`origin/main` at `3de72b780`.

The spec asks for one verified inventory of everything that still ties Fugaro to a single
repository, so the lift to project level can be planned in one go. It also asks for the benign
repo-level settings for the owner's review, an answer to the images-across-repos question and an
ordered lift plan. It changes no behaviour.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as received. No typo was fixed |
| [reconciliation.md](reconciliation.md) | Assumptions A1–A6, the spec-against-repository table, findings F1–F19, decisions D3230–D3236 with the owner's answers, resolutions R1–R3, tension T1 (settled), open items O1–O3, UNVERIFIED U1–U4 (U1 and U2 settled by V1). **Where it disagrees with the spec, it wins** |
| [report.md](report.md) | The inventory: 97 coupling points (RC1–RC97) by surface, the summary count, the benign candidates B1–B19 for the owner's review, what blocks a single admin-run `fugaro update`, every reader of a repo's config, the corner cases, the version-stamp entry point, and the lift order L1–L13 (with L4b) |
| [implementation-plan.md](implementation-plan.md) | Seven tasks (K1–K4, V1, D1, R1) in three levels: three read-only check scripts and their registration, U1/U2 settled from the code, a fix of the missing `fugaro upgrade` in README, `docs/release.md` and the website, and the report's live columns once the owner runs Checks 890–892. Nothing in it is approved |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Headline findings:**

- **The per-repo Terraform root is still live** for every repository not on a shared job. Its
  retirement is shared-jobs-default M2.
- **The image question's premise is half wrong.** Cloud Build already clones each entry's source
  repository; the tie is the per-repo build service account. D3232 recommends one project build
  identity.
- **The repo-level auth keys are marked Retired but are still honoured** and drive infrastructure.
  Model-credentials-routing MG1–MG6 owns the migration.
- **`fugaro update` from outside a checkout skips three stages**, and two more read the working
  directory.
- **Members can trigger Terraform stages through writers that are not role-gated**, and `update`
  asks no role.
- **README, `docs/release.md` and the website tell users to run a `fugaro upgrade` command that does
  not exist.**

**Numbers reserved:** decisions **D3230–D3259** (D3230–D3236 used), checks **890–899** (890–892
used). They were read on 2026-11-22. The highest decision on `main` was D3229 (console-fixes'
block D3200–D3229), and the highest decision in any open pull request's diff was D3220. The
highest check on `main` was 886 (model-credentials-routing's block 880–889), and no open pull
request's diff named a higher one.

**What waits on something external:** the owner's rulings on B1–B19 (D3235, after merge), the
owner's runs of Checks 890–892 (after K4), and shared-jobs-default M2 (owner Check 580) for lift L2.
