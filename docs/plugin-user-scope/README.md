# `fugaro update`: one command, the plugin at user scope, and a session-start nudge

**Status: design only; the owner decides. Nothing here is approved, and no code is written by this
pull request.** The three rulings below are the owner's of 2026-10-31 and they decide the shape; every
other choice is a numbered decision, recommended answer first, with the alternative's cost beside it.

**Asked by:** the owner, 2026-10-31: "move the Fugaro plugin and skills to a user-level install by
default" — and, the same day, the two rulings that go with it: one command, and a nudge at session
start.

**Reconciled against** `origin/main` at `3d6ed04d` and the pull requests open on 2026-11-01.

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
- **The fix people are actually told to run is "install it yourself".** `pluginwire.Fix` answers a
  stale pin with `run fugaro upgrade --local`, `doctor_render.go`'s `plugin-install` line says the
  same, and `fugaro doctor --plugin` is what a repository's CI runs to find out whether any of it is
  true.

This design moves the plugin to the **user** scope of the machine's Claude Code, makes the repository
pin an opt-in, retires `fugaro upgrade` so `fugaro update` is the one command, and gives the agent a
read-only check it runs at session start so the owner hears about a stale installation without asking.

| File | Role |
| --- | --- |
| [spec.md](spec.md) | The one command, the user-scope install, the opt-in pin, doctor, the nudge, and the migration of a repository that carries an old pin |
| [reconciliation.md](reconciliation.md) | Each claim against the code on `main`: what exists (F1–F10), what is missing, what conflicts and what the move costs (F11–F22), the census of everything that names `fugaro upgrade`, and the decisions **D940–D953**. **Where it disagrees with `spec.md`, it wins** |
| [implementation-plan.md](implementation-plan.md) | Tasks U0–U7, the external dependency E1, each task's files, tests, red step and gates. **Nothing in it is approved** |

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

## Numbers this work holds

| Block | Use |
| --- | --- |
| **D940–D953** | this work's block: **D940–D947** are the design's own, **D948–D953** are the rulings' smaller choices. Every one is open — the owner has answered none of them |
| **D954–D959** | reserved and unused |
| **Checks** | none: every behaviour here is a local file, an offline read or an existing `claude` call, and the plan tests each one hermetically. Nothing needs a live check, so no check number is taken |

**How the block was read (2026-11-01, by hand, not assumed from a neighbour's line).** On `main` the
highest written number is **D900** (`docs/rigor-command/README.md`), which itself says "a later task
that needs a number takes D901". Three open pull requests claim blocks above it: **#1481**
(`docs/run-without-checkout`) writes **D901–D919**, **#1485** (`docs/run-limit-carry-over`) writes
**D901–D920**, and **#1487** (`docs/mcp-server`) writes **D920–D934** and reserves **D935–D939**. #1481
and #1485 collide with each other — a fact for the coordinator, not for this work to fix — and #1487
sits above both, so the first number free on `main` **and** in every open pull request is **D940**.
Nothing beyond D959 is reserved here. A later task takes D960 and re-checks these facts rather than
this line's conclusion.

## What a reader should not take from here

- **Not a code change.** This pull request adds four documents and touches nothing else. Every path and
  line number below is a citation for a task, not a diff.
- **Not a release plan.** No release number is claimed. The tasks queue behind the 0.15.0 work the
  other runs own, and the release that carries them is the one that carries E1.
- **Not a promise that the pin disappears.** It stays, as an opt-in (**D940**), because a team that
  wants the skills *required* in a repository has no other way to say so (**D949**, and the trade-offs
  in [spec.md](spec.md) §10).
