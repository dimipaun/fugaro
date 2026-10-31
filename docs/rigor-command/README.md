# `fugaro rigor`: managing a project's rigor mode with one command

**Status: design for the owner's decision; the tasks queue behind CLI 0.15.0.** Every decision
(D810–D819) is a **recommendation with no answer yet**; the plans are written to the
recommendation. Nothing in `implementation-plan.md` is approved.

**Asked by:** the owner, 2026-10-31: "I want [the rigor mode default] at the project level, and we
should have a fugaro command to manage that!"
**Reconciled against** `origin/main` at `eef99df3` and the 8 pull requests open on 2026-10-31.

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

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The four command forms, where each one writes, how a change reaches a run, the safety rules around lowering a mode, the console read side and the migration of a hand-written block |
| [reconciliation.md](reconciliation.md) | Each spec claim against the code on `main`: what exists, what is missing, what conflicts (findings F1–F17 in §1 and F18 in §2), and the decisions D810–D819 in §3. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks R0–R6 (at most 12 files each) and the external dependencies E1, E2, with tests, red steps, gates, SEC flags and levels. **Nothing in it is approved** |

Every claim in `reconciliation.md` names a **file and a symbol, never a line number**, for the reason
`docs/rigor-modes/reconciliation.md` gives: a line number is wrong the first time anyone edits the
file above it, and a path and a symbol survive.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D810–D829** | this work's block: **D810–D819** written, unanswered; **D820–D829** reserved and unused. Taken at the next block of ten above every number on `main` and in every open pull request (the highest on 2026-10-31 is D800, named by PR #715; the console auto-update plan writes D760–D772 and the models plan D780–D799) |
| **Checks** | none: every behaviour this work adds is a local file edit, a local read or an offline resolution, and the plan tests each one hermetically. The one cloud write it adds is `fugaro update`'s own layer stage, which the existing update path and its live checks already cover. No check number is taken |

## What this folder is not

- **Not code.** It is a design the owner decides on. R0–R6 are tasks for a later run.
- **Not an approval.** The status line above says so, and no task here may start before the owner
  answers the decisions it cites.
- **Not a new rigor mode or a change to the resolver.** `internal/rigor` orders the modes and
  settles the precedence once; nothing here re-states or moves either.
- **Not a floor.** A project default is a default: a narrower layer may lower it (D21), and the
  security floors are outside every layer and are shown, never written. §4 of `spec.md` says what
  that means for the confirmation the commands ask.
