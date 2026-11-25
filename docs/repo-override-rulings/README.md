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
| [reconciliation.md](reconciliation.md) | The amended spec: assumptions A1–A7, the capability map (nine modules, approved as D3299), findings F1–F25, decisions D3290–D3308 (D3290–D3298 the owner's answers to the spec's Q1–Q9; D3299–D3304 the owner's answers to the questions it raised; D3308 closes O6), resolutions R1–R22, tension T1 (settled by D3300), open items O1–O6 (O4 closed, O5 closed by D3307, O6 closed by D3308), UNVERIFIED U1–U4. **Where it disagrees with the spec, it wins** |
| [implementation-plan.md](implementation-plan.md) | Contracts C1 and C2, 46 tasks and integrations I1 and I2 over two releases (50 runs, and two contract changes that run only if raised), nine levels, the critical path, the shared files, the edges broken and kept, Checks 910–913 |
| [implementation-plan.graph.json](implementation-plan.graph.json) | The plan's graph as `fugaro plan graph` reads it |

**The owner's answers of 2026-11-23** (D3290–D3298): eight as recommended, and **D3295 not the
recommendation**: repository recipes move to **one flat namespace** in the project store, not a
per-repository prefix; on a clash `update` renames the clashing copies `<name>.<owner-repo>`,
keeps each repository pointed at its own copy and prints one line per rename, with no admin step.
The reconciliation follows that through everywhere the spec assumed the prefix.

**Answered the same day, each as recommended (D3299–D3304):** the capability map with L13 inside
as module `dead-keys` (D3299); release A at least one release after L1's (D3300, which settles
tension T1); a clash is any name another repository already resolves to different text (D3301);
the copy is spelled `<name>.<owner-repo>` with its own grammar and the `recipeCopySince` gate
(D3302; **amended after review**, the owner's answer of the same day: `<name>.<owner-repo>-<8 hex>`,
the 8 hex from the repository's `task.Slug` hash, because the readable half alone merges
"acme/app-web" and "acme-app/web"; at most 96 characters, the owner's own answer); each repository is pointed at its copy by a lookup rule, with no new data (D3303); and a
release-A runner refuses at bootstrap a pre-L8 task naming a repository recipe (D3304).

**The owner's answers to review round 2, 2026-11-23 (D3305–D3307):** migrate once per repository,
recorded in the runs bucket after the layer publish lands (D3305, as recommended); **the real list,
fail open (D3306, not the recommendation)**: `update` migrates only the repositories it can list;
one admitted only by a pattern, or unreadable, is not migrated, its lifted keys are dropped with only
the project's values standing (an `ab.rate: 0` opt-out or a raised rigor level among them), and a
recipe name it uses resolves to a listed repository's flat copy or to the catalog's, all of which
`update` and release A's notes say; and O5's residue is accepted (D3307).

**The owner's answer on O6, 2026-11-23 (D3308, as recommended):** fail-open stays, except that the
launcher refuses a launch whose recipe name an unmigrated repository's default branch still holds in
`.fugaro/recipes/`, naming the fix (list the repository, then `fugaro update`). So no run silently
changes recipe, which is what D3301 asks.

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

**Numbers reserved:** decisions **D3290–D3319** (D3290–D3308 used), checks **910–919** (910–913
used). The block was assigned with this spec on 2026-11-23: the highest decision on `main` was
D3239 (repo-coupling-audit's block D3230–D3259), L1's spec beside this one holds D3260–D3289, and
the highest check on `main` was 892 (repo-coupling-audit's block 890–899). No decision in
D3290–D3319 and no Check 910–919 is on `main`.

**What waits on something external:** the release that carries L1 (`docs/required-version-stamp/`,
not on `main` yet), shipped at least one release before release A is cut (D3300); release A
before release B (D3298); and the owner's runs of Checks 910–913.
