# Repo-override rulings (lift L8)

**Status: plan only; nothing is implemented; no task approved.** See
[docs/status.md](../status.md) for the current table. Spec owner: Dimi Paun (the owner). Date:
2026-11-23. Checked against `origin/main` at `0dec0bb36`.

Lift L8 of the [repo-coupling audit](../repo-coupling-audit/README.md) (report §7) applies the
owner's rulings D3237 on the benign candidates B1–B19: the model choice (B3), `ab.rate` (B9), the
rigor level and areas (B11) and the `.fugaro/recipes/` folder (B16) move to the project, migrated
once by `fugaro update` and refused in a repository's file the release after; review depth and
spend (B4) become tighten-only; a repository's RTDB cap (B18) gets a ceiling. The spec also
proposes to carry lift L13 (the dead repository keys) as a module.

| File | What it is |
| --- | --- |
| [spec.md](spec.md) | The spec as approved by the owner on 2026-11-23. No typo was fixed |
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A7, the proposed capability map (nine modules, pending D3299), findings F1–F24, decisions D3290–D3304 (D3290–D3298 the owner's answers to the spec's Q1–Q9; D3299–D3304 **pending**), resolutions R1–R16, tension T1, open items O1–O3, UNVERIFIED U1–U4. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contracts C1 and C2, 39 tasks and integrations I1 and I2 over two releases (43 runs, and two contract changes that run only if raised), eight levels, the critical path, the shared files, the edges broken and kept, Checks 910–912 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**The owner's answers of 2026-11-23** (D3290–D3298): eight as recommended, and **D3295 not the
recommendation**: repository recipes move to **one flat namespace** in the project store, not a
per-repository prefix; on a clash `update` renames the clashing copies `<name>.<owner-repo>`,
keeps each repository pointed at its own copy and prints one line per rename, with no admin step.
The reconciliation follows that through everywhere the spec assumed the prefix.

**Pending the owner (D3299–D3304):** the capability map with L13 inside (D3299); whether release
A must ship at least one release after L1 (D3300, tension T1); what counts as a clash (D3301); the
copy's spelling against `recipe.NameRE` (D3302); how a repository is pointed at its copy (D3303);
and what a release-A runner does with a pre-L8 task naming a repository recipe (D3304). The plan
is written on each recommendation and marks the tasks each would change.

**Headline findings:**

- **"Retired" means named and still honoured** (`RetiredKeyNotes`, D3063), and
  model-credentials-routing relies on it; a lifted key needs its own list so `config.Resolve` can
  drop it without cutting MCR's bridge (F1, R1). And nothing calls `RetiredKeyNotes` today (F2).
- **The runner reads its config from the task's ref**, not the default branch; the drop holds
  because it lives in `config.Resolve` (F3).
- **The owner's `<name>.<owner-repo>` is not a recipe name today** (no dot, 40 characters), and
  "pointed at its own copy" needs a mechanism (F5, F7).
- **A flat publish can change other repositories' runs** when the name is a catalog or project
  recipe they already resolve, `default` first of all (F21).
- **Pre-L1 CLIs stay unfenced** (L1 Q2): on a project whose layer carries `ab.repos` they fail to
  read the layer; nothing relies on a `projectpolicy.Version` bump (F15, R9).
- **B4's mode baseline cannot be computed in `config.Resolve`** (F4, R3); **no command writes
  `rigor.repos` today** (F13, R6).

**Numbers reserved:** decisions **D3290–D3319** (D3290–D3304 used), checks **910–919** (910–912
used). The block was assigned with this spec on 2026-11-23: the highest decision on `main` was
D3239 (repo-coupling-audit's block D3230–D3259), L1's spec beside this one holds D3260–D3289, and
the highest check on `main` was 892 (repo-coupling-audit's block 890–899). No decision in
D3290–D3319 and no Check 910–919 is on `main`.

**What waits on something external:** the release that carries L1 (`docs/required-version-stamp/`,
not on `main` yet) before release A is cut (D3300); the owner's answers to D3299–D3304; release A
before release B (D3298); and the owner's runs of Checks 910–912.
