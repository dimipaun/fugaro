# `fugaro update`: one command, the plugin at user scope, and a session-start nudge

**Status: design only; the owner decides. Nothing here is approved, and no code is written by this
pull request.** The three rulings below are the owner's of 2026-10-31 and they decide the shape; every
other choice is a numbered decision, recommended answer first, with the alternative's cost beside it.

**Asked by:** the owner, 2026-10-31: "move the Fugaro plugin and skills to a user-level install by
default" — and, the same day, the two rulings that go with it: one command, and a nudge at session
start.

**Reconciled against** `origin/main` at `3d6ed04d`, the pull requests open on 2026-11-01, and — for the
teammate question of §"One more reader", which is this work's second round — the tree this branch itself
merged, at `28425eba` ([reconciliation.md](reconciliation.md) §7 records what moved between the two).

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
- **The fix people are actually told to run is "install it yourself".** `doctor_render.go`'s
  `plugin-install` line answers a plugin that is not installed with `run fugaro upgrade --local`
  (`:768`), and `fugaro doctor --plugin` is what a repository's CI runs to find out whether any of it is
  true. The rest of that sentence has already moved: a commit on this branch's line of work (#1495)
  re-pointed `pluginwire.Fix` (`internal/pluginwire/status.go:49`, `:55`, `:57`) and the plugin stage's
  own notices at `fugaro update`, which is why `spec.md` §6 reads as it does.

This design moves the plugin to the **user** scope of the machine's Claude Code, makes the repository
pin an opt-in, retires `fugaro upgrade` so `fugaro update` is the one command, and gives the agent a
read-only check it runs at session start so the owner hears about a stale installation without asking.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The one command, the user-scope install, the opt-in pin, doctor, the nudge, and the migration of a repository that carries an old pin |
| [reconciliation.md](reconciliation.md) | Each claim against the code on `main`: what exists (F1–F10), what is missing, what conflicts, what the move costs and what a teammate who is not the owner runs into (F11–F25), the census of everything that names `fugaro upgrade`, the decisions **D940–D955**, and §7's record of what the merged tree changed. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks U0–U8, the external dependency E1, each task's files, tests, red step and gates. **Nothing in it is approved** |

## The three rulings this is written to

**A. ONE COMMAND.** `fugaro update` is the only command, and it converges the installation **and** the
checkout it is standing in. `fugaro upgrade` is removed: no alias, no tombstone that runs anything
(what it says is decision **D950**). The pin step goes with it; the plugin moves to user scope inside
`update`; the cloud image refresh is `update`'s checkout stage, where it already is
(`internal/cli/init_checkout_upgrade.go:39`).

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
(`internal/initflow/loop.go:329`) — and the images stage sits in front of the plugin stage, with this
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
| **D940–D955** | this work's block: **D940–D947** are the design's own, **D948–D953** are the rulings' smaller choices, and **D954–D955** are the second round's — the teammate who is not the owner ([spec.md](spec.md) §11). Every one is open — the owner has answered none of them |
| **D956–D959** | reserved and unused |
| **Checks** | none: every behaviour here is a local file, an offline read or an existing `claude` call, and the plan tests each one hermetically. Nothing needs a live check, so no check number is taken |

**How the block was read (2026-11-01, by hand, not assumed from a neighbour's line).** On `main` the
highest written number is **D900** (`docs/rigor-command/README.md`), which itself says "a later task
that needs a number takes D901". Three open pull requests claim blocks above it: **#1481**
(`docs/run-without-checkout`) writes **D901–D919**, **#1485** (`docs/run-limit-carry-over`) writes
**D901–D920**, and **#1487** (`docs/mcp-server`) writes **D920–D934** and reserves **D935–D939**. #1481
and #1485 collide with each other — a fact for the coordinator, not for this work to fix — and #1487
sits above both, so the first number free on `main` **and** in every open pull request is **D940**.
**D954–D955** were taken the same day by this work's own second round, in this directory and on the same
branch, so no other run could have claimed them between the two commits; nothing beyond D959 is reserved
here. A later task takes D960 and re-checks these facts rather than this line's conclusion.

## What a reader should not take from here

- **Not a code change.** This pull request adds four documents and touches nothing else. Every path and
  line number below is a citation for a task, not a diff.
- **Not a release plan.** No release number is claimed. The tasks queue behind the 0.15.0 work the
  other runs own, and the release that carries them is the one that carries E1.
- **Not a promise that the pin disappears.** It stays, as an opt-in (**D940**), because a team that
  wants the skills *required* in a repository has no other way to say so (**D949**, and the trade-offs
  in [spec.md](spec.md) §10).
