# Repo-coupling audit

**Status: all seven tasks approved 2026-11-23 and done with R1.** The report has its live columns
from the owner's runs of Checks 890–892 on `fugaro` (2026-11-23). The owner ruled on B1–B19 on
2026-11-23 (D3237: thirteen kept, B4 and B18 kept tight, B3 rejected, B9, B11 and B16 lifted to the
project), on the lift order (D3238, amended the same day) and on L5's scope (D3239). Nothing of the lift is implemented. See
[docs/status.md](../status.md) for the current table.

**Lift waves (D3238, amended).** Wave 1: L1 (the version stamp), L5 and L8 (apply D3237's
rulings; needs L1), each its own spec, next. L5 as D3239 rescopes it: `update` may take the project
from the checkout it runs in, but from any directory it works over every enrolled repository, each
read from its default branch, and names any repository whose value it could not take. Wave 2: L3
(waits on L2), L9 (waits on L3), L11, L12 and L13. L6 and L7 wait for the owner's next pick; L2, L4
and L4b stay with their plans; L10's first half is done (D1, #3693).

Spec owner: Dimi Paun (the owner). Received and reconciled on 2026-11-22, checked against
`origin/main` at `3de72b780`.

The spec asks for one verified inventory of everything that still ties Fugaro to a single
repository, so the lift to project level can be planned in one go. It also asks for the benign
repo-level settings for the owner's review, an answer to the images-across-repos question and an
ordered lift plan. It changes no behaviour.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as received. No typo was fixed |
| [reconciliation.md](reconciliation.md) | Assumptions A1–A6, the spec-against-repository table, findings F1–F21, decisions D3230–D3239 with the owner's answers, resolutions R1–R3, tension T1 (settled), open items O1–O5 (O1 and O2 closed), UNVERIFIED U1–U4 (U1 and U2 settled by V1, U3 by Check 892, U4 restated). **Where it disagrees with the spec, it wins** |
| [report.md](report.md) | The inventory: 107 coupling points (RC1–RC107) by surface, the summary count, the live result of Checks 890–892 on `fugaro`, the benign candidates B1–B19 with the owner's rulings (D3237), what blocks a single admin-run `fugaro update`, every reader of a repo's config, the corner cases, the version-stamp entry point, and the lift order L1–L13 (with L4b) in the owner's waves (D3238) |
| [implementation-plan.md](implementation-plan.md) | Seven tasks (K1–K4, V1, D1, R1) in three levels: three read-only check scripts and their registration, U1/U2 settled from the code, a fix of the missing `fugaro upgrade` in README, `docs/release.md` and the website, and the report's live columns once the owner runs Checks 890–892. All seven approved 2026-11-23 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**Headline findings:**

- **The per-repo Terraform root is still live** for every repository not on a shared job. Its
  retirement is shared-jobs-default M2.
- **The image question's premise is half wrong.** Cloud Build already clones each entry's source
  repository; the tie is the per-repo build service account. D3232 recommends one project build
  identity.
- **The repo-level auth keys are marked Retired but are still honoured** and drive infrastructure.
  Model-credentials-routing MG1–MG6 owns the migration.
- **`fugaro update` from outside a checkout skips four stages**, two more read the working
  directory, and so do two steps after the stages.
- **Members can trigger Terraform stages through writers that are not role-gated**, and `update`
  asks no role.
- **README, `docs/release.md` and the website tell users to run a `fugaro upgrade` command that does
  not exist.**
- **Live on `fugaro` (2026-11-23), shared jobs are unused**: every repo launches on its own job, so
  every per-repo blocker is real there. Per-repo records outlive their repos, and the Batch compute
  root's grants to per-repo job accounts drift, because only `setup --provision-batch` applies them.

**Numbers reserved:** decisions **D3230–D3259** (D3230–D3239 used), checks **890–899** (890–892
used). They were read on 2026-11-22. The highest decision on `main` was D3229 (console-fixes'
block D3200–D3229), and the highest decision in any open pull request's diff was D3220. The
highest check on `main` was 886 (model-credentials-routing's block 880–889), and no open pull
request's diff named a higher one.

**What waits on something external:** the lift specs L1, L5 and L8 (wave 1, D3238), the owner's next
pick between L6 and L7, and shared-jobs-default M2 (owner Check 580) for lift L2.
