# `fugaro rigor`: managing a project's rigor mode with one command

**Status: D830 and D831 are answered — the owner's SD4 ruling of 2026-10-31; D832–D839 are
recommendations with no answer yet, and the plan is written to them. The tasks queue behind CLI
0.15.0.** Nothing D832–D839 carry is approved.

**Asked by:** the owner, 2026-10-31: "I want [the rigor mode default] at the project level, and we
should have a fugaro command to manage that!"
**Reconciled against** `origin/main` at `29004ec9` (2026-10-31).

## What it is

A project's default rigor mode lives at `defaults.rigor.default` in the **project layer**
(`fugaro/project-layer.yaml` in the runs bucket), and the only way to change it today is to
hand-edit a local file and run `fugaro config publish FILE` — a second command, refused in a
coding agent's session, which the HARD RULE in `CLAUDE.md` forbids: `fugaro update` is the whole
upgrade, with no separate publish step. The rigor mode is the key where that costs the most,
because it is the one key that decides how much review a project's runs get.

This work adds one command family, `fugaro rigor` (`show`, `set`, `unset`), that makes the owner's
edit one command, checked before it is written, and closes the `fugaro update` gap it leans on:
`update` does not publish the project layer today, and a project default that only reaches runs
after a second command is not delivered.

**The shape the owner approved (SD4).** There is **one** local layer file,
`~/.config/fugaro/projects/<project>-layer.yaml`, and no flag and no config key naming it. The first
`fugaro update` **seeds** it from the published layer when it is absent, and every later `update`
publishes it after showing the diff; `rigor set` and `unset` edit that one file. The owner's edit is
therefore one command whose next step is the one documented command, which is the HARD RULE read
literally.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The four command forms, where each one writes, how a change reaches a run, the safety rules around lowering a mode, the console read side and the migration of a hand-written block |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists, what is missing, what conflicts (findings F1–F17 in §1 and F18 in §2), and the decisions D830–D839 in §3. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks R0–R8 (at most 12 files each), the follow-up tasks R7 (F13) and R8 (F14), and the dependencies E1, E2, with tests, red steps, gates, SEC flags and levels. **Nothing D832–D839 carry is approved** |

Every claim in `reconciliation.md` names a **file and a symbol, never a line number**, for the reason
`docs/rigor-modes/reconciliation.md` gives: a line number is wrong the first time anyone edits the
file above it, and a path and a symbol survive.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D830–D839** | this work's block, and **nothing beyond D839 is reserved**: **D830** (where `set` writes — the one local layer file) and **D831** (the `Layer` stage in `update`, with its seeding) are **answered**; **D832–D839** are recommendations with no answer yet. Taken above the merged Coordinator v2 design, which holds **D810–D829** (`docs/console-coordinator-v2`: D810–D819 the page, D820–D828 the terminal, D829 reserved) — this work's first block was D810–D819, which that design took while this branch was open, so the numbers moved rather than the work. The highest number written anywhere else is D799 (the models plan's reserved tail: D796–D799); D800–D809 are a gap, not this work's |
| **Checks** | none: every behaviour this work adds is a local file edit, a local read or an offline resolution, and the plan tests each one hermetically. The one cloud write it adds is `fugaro update`'s own layer stage, which the existing update path and its live checks already cover. No check number is taken |

## What this folder is not

- **Not code.** It is a design the owner decides on. R0–R8 are tasks for a later run.
- **Not an approval.** Only D830 and D831 are answered; no task here may start before the owner
  answers the decisions it cites.
- **Not a new rigor mode or a change to the resolver.** `internal/rigor` orders the modes and
  settles the precedence once; nothing here re-states or moves either.
- **Not a floor.** A project default is a default: a narrower layer may lower it (D21), and the
  security floors are outside every layer and are shown, never written. The review floor is
  `fugaro rigor floor`'s command (SD10, reconciliation §4), not this one's: no form here writes a
  floor.
