# `fugaro rigor`: managing a project's rigor mode with one command

**Status: design approved; tasks queue for CLI 0.15.0 (and console 0.117.0).** Every decision is
answered: **D830 and D831** by the owner's SD4 ruling of 2026-10-31, **D832–D839 on 2026-10-31, all
as recommended**, and the one decision the plan **cites** from another design — **D854**, whether the
`Layer` stage records the object generation it reads at the seed — **answered by the owner on
2026-10-31: record the generation**. Nothing this design cites is left open: the spec and the plan
are written to those answers and the tasks may start. The CLI tasks R0–R8 are 0.15.0's; the console
half is not a task here (D836) — it is the read-only project view, and it queues for console 0.117.0
with the rest of that work.

**Asked by:** the owner, 2026-10-31: "I want [the rigor mode default] at the project level, and we
should have a fugaro command to manage that!"
**Reconciled against** `origin/main` at `7efc78e0` (2026-10-31). This design is the second half of
one piece of work; the first is the **settings** design, and the order and the rulings the two share
are stated below, in this folder's own words and referenced by the other folder's.

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

**The stale copy, and the record that catches it (D854, answered: record the generation).** Two
guards, and neither replaces the other. Within one `update` the stage refuses to overwrite an object
whose generation moved since its own read — that guard is unconditional and needs no record. Across
runs, which is what the owner's ruling answers, `update` **records** the generation the local layer
file was last written from, in the local project config's `layer_generation`
(`~/.config/fugaro/projects/<project>.yaml`, `localcfg.Config`), and refuses to publish over a newer
published generation, showing the three-way state and offering a re-seed. The record is also what
tells the two readings of **`unpublished`** apart in `show` and in doctor: a **local edit** (the
bytes differ and the recorded generation is still the published one) or a **stale copy from another
machine** (the published generation has moved past the recorded one), which need different remedies.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The four command forms, where each one writes, how a change reaches a run, the safety rules around lowering a mode, the console read side and the migration of a hand-written block |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists, what is missing, what conflicts (findings F1–F17 in §1 and F18 in §2), and the decisions D830–D839 in §3. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks R0–R8 (at most 12 files each), the follow-up tasks R7 (F13) and R8 (F14), and the dependencies E1 (the editor task's `internal/yamledit`), E2, with tests, red steps, gates, SEC flags and levels. **Every decision in this design's block is answered (D830–D839), and the one it cites from another design, D854, is answered too (record the generation)** |

## The joint order, and the rulings the rigor and settings designs share

This design and the **settings** design (`fugaro settings get|set|unset` and the policy/layer writes,
S5) are one piece of work split in two. Both folders hold **the same six positions and the same
rulings**, each worded in its own section and **referenced by the other** rather than copied: this is
this folder's statement of them, the settings design's `implementation-plan.md` carries the joint
order for its tasks, and neither is a transcription of the other. What is not negotiable is the
content: the order is the one below, and a task in one design may not be written as if the other did
not exist.

- **ONE shared editor package: `internal/yamledit`.** It is generic — comment-preserving
  locate-and-splice operations on a YAML document, nothing about fugaro's own keys: **set a scalar,
  remove a key, add or remove a list item, upsert or remove a sequence item by key**. It serves
  **three documents**: the local project config (`~/.config/fugaro/projects/<project>.yaml`), the
  local layer file (§1's one path) and a repository's **`fugaro.yaml`**. There is no second editor,
  in any package, under any name.
- **ONE owning task: the editor task.** It **owns** `internal/yamledit` and lands it **first** in
  the joint order; every other design calls it. It is not owned by rigor, not by the `models` plan,
  and not "by whoever gets there first": rigor's R2 and the settings tasks depend on it and never
  fork it.
- **The state word is `unpublished`, everywhere.** The local document has bytes the published object
  does not: `rigor show`, `fugaro config layer` and doctor all call that **`unpublished`**, and all
  three decide it the same way — the **same sha256 comparison** of the local file's bytes against
  the published object's. One word, one comparison, three surfaces. And it is one state with **two
  causes**, which the recorded generation (D854, answered) tells apart: a **local edit**, when the
  recorded generation is still the published one, and a **stale copy from another machine**, when
  the published generation has moved past it. The surfaces that print the word say which of the two
  it is, because the remedies differ — publish this file, or re-seed it from the newer object.
- **The no-checkout wording is `unknown (no checkout on this machine)`** in both designs — the
  string `fugaro rigor show` prints for a repository whose own `fugaro.yaml` this machine has no
  checkout for. Neither design guesses a mode it could not read.
- **The joint order.** The editor comes first because two designs call it; the layer stage comes
  before the commands because a command that writes a layer nothing publishes is the second command
  the HARD RULE forbids (F18); the write half comes before `set` because `set` is what calls it.

| # | Who | Task |
| --- | --- | --- |
| 1 | the editor task | `internal/yamledit` — the shared editor package above, and nothing else |
| 2 | settings | the policy stage (S3) |
| 3 | settings | the read half (S0–S2, S4) |
| 4 | rigor | the local layer file, then the stage that publishes it: **R0** (the path `LayerFile`, `config publish`'s default and doctor's `local-layer-file` check — no stage yet) and **R1** (the `Layer` stage in `update` that seeds and publishes it, which needs R0's file) |
| 5 | settings | set (S5) — the shared write helper R4 and R5 call, `internal/cli/settings_write.go`: diff, confirmation, agent-session refusal, backup, atomic write |
| 6 | rigor and models | rigor R2–R5, and the `models` work |

This work's own tasks keep their levels in [implementation-plan.md](implementation-plan.md); the
table above is the order the two designs land in together, and a task here that names a settings
task means *that task's output*, never a copy of it.

Every claim in `reconciliation.md` names a **file and a symbol, never a line number**, for the reason
`docs/rigor-modes/reconciliation.md` gives: a line number is wrong the first time anyone edits the
file above it, and a path and a symbol survive.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D830–D839** | this work's block, and **nothing beyond D839 is reserved**: **D830** (where `set` writes — the one local layer file) and **D831** (the `Layer` stage in `update`, with its seeding) are **answered** by the owner's SD4 ruling, and **D832–D839** are **answered 2026-10-31, all as recommended**. Taken above the merged Coordinator v2 design, which holds **D810–D829** (`docs/console-coordinator-v2`: D810–D819 the page, D820–D828 the terminal, D829 reserved) — this work's first block was D810–D819, which that design took while this branch was open, so the numbers moved rather than the work. The highest number written anywhere else is D799 (the models plan's reserved tail: D796–D799); D800–D809 are a gap, not this work's. **D854** is cited by R1 and is the **settings design's** — a number this block does not hold — and it is **answered by the owner on 2026-10-31: record the generation** |
| **Checks** | none: every behaviour this work adds is a local file edit, a local read or an offline resolution, and the plan tests each one hermetically. The one cloud write it adds is `fugaro update`'s own layer stage, which the existing update path and its live checks already cover. No check number is taken |

## What this folder is not

- **Not code.** It is a design, and the owner has decided it: R0–R8 are tasks for a later run, in
  this release's queue.
- **Not a licence to widen it.** Every decision this folder held is answered (D830–D839), so a task
  here may start — but a task that finds a *new* decision reports it rather than picking an answer,
  and the decisions answered above are not reopened by an implementer's convenience. **D854 is
  answered too** (record the generation, 2026-10-31), and the cross-run guard R1 builds is that
  answer's code: an implementer who finds a *third* reading of `unpublished`, or a state the record
  does not cover, reports it rather than picking an answer.
- **Not a new rigor mode or a change to the resolver.** `internal/rigor` orders the modes and
  settles the precedence once; nothing here re-states or moves either.
- **Not a floor.** A project default is a default: a narrower layer may lower it (D21), and the
  security floors are outside every layer and are shown, never written. The review floor is
  `fugaro rigor floor`'s command (SD10, reconciliation §4), not this one's: no form here writes a
  floor.
