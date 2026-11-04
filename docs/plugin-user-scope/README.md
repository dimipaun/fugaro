# `fugaro update`: one command, the plugin at user scope, and a session-start nudge

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved.**
The three rulings below are the owner's of 2026-10-31 and they decide the shape; of the sixteen numbered
decisions, the owner answered four on 2026-11-04 (**D943**, **D949**, **D950**, **D954** — D949 **not as
recommended**: the pin merge is deleted outright), and the other twelve carry their recommended answer
with the alternative's cost beside them.

**Asked by:** the owner, 2026-10-31: "move the Fugaro plugin and skills to a user-level install by
default" — and, the same day, the two rulings that go with it: one command, and a nudge at session
start.

**Reconciled against** `origin/main` at **`b41709ea`**, read on 2026-11-04 — the merged tree this branch
carries — and, for the teammate question of §"One more reader", the tree this branch itself merged at
`28425eba` ([reconciliation.md](reconciliation.md) §7 records what moved between the first two reads and
§8 what `main` overtook by the third).

**The directory stays `docs/plugin-user-scope/`.** The name says what moved (the plugin, to user
scope) rather than what the work is (one `update`), and a reader looking for this work after the fact
will look for it under the plugin's name. The title above is the honest one.

## What it is

The Fugaro plugin is wired into a **repository** today. `fugaro setup`'s plugin stage and
`fugaro update`'s plugin stage merge a marketplace entry and `enabledPlugins["fugaro@fugaro"]` into the
checkout's `.claude/settings.json`, pinned to the release tag (`internal/cli/init_plugin_stage.go`,
`internal/pluginwire`), and `fugaro upgrade` runs the same merge as its first step
(`internal/cli/upgrade.go:68`). Every consequence follows from that one choice:

- **A teammate gets the skills only because a committed file says so.** The pin is the delivery
  mechanism, so a repository that never commits it has no skills, and a repository that does has a
  third-party `settings.json` in its trust surface.
- **The skills are a per-checkout thing.** Two checkouts of the same repository, two worktrees, a
  checkout the setup never reached: each has its own file and its own answer.
- **`fugaro upgrade` grew to be a second front door.** The pin, the plugin in Claude Code, the job
  images, the security rules and the published policy — five steps, a `PATH` argument it accepts
  several times, an exit code that is the worst of them, and a skill to explain it. That is a command
  where the HARD RULE (`CLAUDE.md`: `fugaro update` is the whole upgrade) says there should be one.
- **`doctor --plugin --strict` fails a repository that never opted in, for a fix the repository cannot
  run.** `plugin-install` sits in `doctorMeasureIDs` (`doctor_render.go:204`) and **not** in
  `doctorUpdatePrefixes` (`:118`), so a plugin that is not installed is a `warning` in the mode a
  repository's CI runs — `fugaro doctor --plugin` is documented as offline and safe for CI — while the
  same check's fix line is "run `fugaro update`, or install it in Claude Code" (`:769-770`), a command
  that acts on the machine and not on the repository. The remedy text has already moved (#1495, #1500:
  `pluginwire.Fix`'s states, the plugin stage's own notices and doctor's own fix line all name `fugaro
  update` today); what is left for this work is the **class move and the severity** (D942), which is why
  `spec.md` §6 reads as it does.

This design moves the plugin to the **user** scope of the machine's Claude Code, makes the repository
pin an opt-in, retires `fugaro upgrade` so `fugaro update` is the one command, and gives the agent a
read-only check it runs at session start so the owner hears about a stale installation without asking.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The one command, the user-scope install, the opt-in pin, doctor, the nudge, and the migration of a repository that carries an old pin |
| [reconciliation.md](reconciliation.md) | Each claim against the code on `main`: what exists (F1–F10), what is missing, what conflicts, what the move costs and what a teammate who is not the owner runs into (F11–F26), the census of everything that names `fugaro upgrade`, the decisions **D940–D955**, §7's record of what the merged tree changed and **§8's record of what `main` overtook (2026-11-04) and the owner's four answers**. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks U0–U8, the external dependency E1, each task's files, tests, red step and gates. **Nothing in it is approved** |

## The three rulings this is written to

**A. ONE COMMAND.** `fugaro update` is the only command, and it converges the installation **and** the
checkout it is standing in. `fugaro upgrade` is removed: no alias, no tombstone that runs anything
(what it says is decision **D950**, answered 2026-11-04: nothing, not even a tombstone). The pin merge
leaves with it — **D949, answered 2026-11-04, "DELETE the merge outright", which is not what this design
recommended**; the plugin moves to user scope inside `update`; the cloud image refresh is `update`'s
checkout stage, where it already is (`internal/cli/init_checkout_upgrade.go:39`).

**B. `update` inside a repository does the repository's part automatically — and the parts it does not
were checked against the code, not assumed.** The four gaps this brief names are closed by tasks:
a subdirectory of a checkout (F12), a repository not enrolled (F13), a linked worktree (F14), a
checkout of another project (F15). One of them is a **regression the move itself would cause** if it
were done naively, and it is the reason the plugin step changes stage rather than staying where it is:
under `update` the plugin step lives in the **checkout** stage, which is *after* the `repository`
stage, so a session whose repository stage returns needs-you would never reach it (F13).

**C. An agent-side nudge.** The `setup` and `working` skills run a read-only, no-network check at
session start (**D945**, **D946**, **D947**), say so in one sentence when the installation or the
checkout is behind this CLI, and offer `fugaro update`. Detecting a *newer release* needs the network
and stays opt-in and off by default (**D952**).

## One more reader: the teammate who is not the owner

**The question this work's second round puts to the code** (2026-11-01 — the one the rulings do not answer
by themselves): *a teammate who is not the owner runs `fugaro update` in a checkout — what happens, and
what is their one command to get the plugin and the checkout current?*

**The answer is `fugaro update`, and it is not yet true, so the gap is a task.** Nothing the plugin needs
is the owner's: the install is the teammate's own `claude` at user scope. What ends their run is the
converge's own rule — it stops at the first stage that needs the user or fails
(`internal/initflow/loop.go:331`) — and the images stage sits in front of the plugin stage, with this
release's base image to pin on the very day a teammate runs it. On a machine that has never run `fugaro
setup` the run certainly stops before the plugin: `newMachineGuard`
(`internal/cli/init_new_machine.go:94`) refuses every cloud-changing stage after the one that wrote that
machine's config, and its exemption names `Installation` and `Plugin` while the images and secrets stages
come between them. **Settled under ruling A**, the answer stays **one command,
`fugaro update`** — no `--local`, and no `fugaro setup` for a plugin the machine owns — and the gap is
closed by **U8** (D954: a machine-local stage is not blocked by a cloud stage that stopped; D955: the
user-scope install stops asking about the repository). The plan puts U8 **before U4**, so the old spelling
is not deleted before the new one reaches the plugin. **Where the code cannot answer** — whether a given
teammate's ADC may read the state bucket, plan the installation or write Artifact Registry is IAM the
binary never sees — is the open item [spec.md](spec.md) §11 states rather than papers over.

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D940–D955** | this work's block: **D940–D947** are the design's own, **D948–D953** are the rulings' smaller choices, and **D954–D955** are the second round's — the teammate who is not the owner ([spec.md](spec.md) §11). **The owner answered four of them on 2026-11-04** — **D943**, **D949** (delete the merge outright, **not** the recommendation), **D950** and **D954** — and the other twelve stand as recommendations |
| **D956–D959** | reserved and unused |
| **Checks** | none: every behaviour here is a local file, an offline read or an existing `claude` call, and the plan tests each one hermetically. Nothing needs a live check, so no check number is taken |

**How the block was read (the launcher's reading of 2026-11-04, added to this work's own of 2026-11-01
and not assumed from a neighbour's line).** The D-blocks are taken as follows: **D1000–D1189 are on
`main`** (highest written, `D1189`); **D1190–D1219 belong to #1676**; **D1220–D1259 belong to the
stateless-orchestrator work**; **D1260–D1289 belong to the #1678 design**; and **D1290–D1309 are
#1492's new block**. Nothing on `main` or in those four blocks reaches below D1000, and no open work has
claimed D940–D959 since this folder reserved it, so **D940–D955 stay as written** — the renumbering
other designs took this round does not move this one. This work's own first reading (2026-11-01, by
hand) found `main`'s highest written number at **D900** (`docs/rigor-command/README.md`) with #1481,
#1485 and #1487 claiming D901–D939 above it, and **D954–D955** were taken the same day by this work's
own second round, in this directory and on the same branch, so no other run could have claimed them
between the two commits. Nothing beyond D959 is reserved here. A later task re-reads the block list
rather than trusting this line's conclusion.

## What a reader should not take from here

- **Not a code change.** This pull request adds four documents and touches nothing else. Every path and
  line number below is a citation for a task, not a diff.
- **Not a release plan.** No release number is claimed here, and the slots this design first named are
  spent: 0.15.0, 0.16.0 and 0.17.0 have shipped. 0.18.0 is claimed by the config-management, managed
  agent-converge and project-image-catalog work, so the release that carries E1 is **0.19.0**.
- **Not a promise that the pin survives.** The owner answered **D949** on 2026-11-04 with **DELETE the
  merge outright** — *not* this design's recommendation — so a repository whose team wants the skills
  *required* loses that ability, which is the cost [spec.md](spec.md) §10 records rather than designs
  away. The pin's read side (`internal/pluginwire`, doctor's `plugin-pin`) keeps a job only for the
  migration window U5 opens (reconciliation F26).
