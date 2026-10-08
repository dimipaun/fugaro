# `fugaro upgrade` and `/fugaro:upgrade`

Status: design approved by the owner (2026-10-08). The details below were settled by reading the code, and each one is a decision the owner can veto in the plan ([plans/2026-10-08-upgrade.md](../plans/2026-10-08-upgrade.md)). Delivery: release **0.5.2**, together with `fugaro image refresh --yes` (PR #190). This work starts only after that PR has merged.

## Problem

After `brew upgrade dimipaun/tap/fugaro`, every checkout needs four separate actions, in two different tools:
- `fugaro image refresh --yes`, in the user's own terminal;
- `fugaro update-skills`;
- in Claude Code, `/plugin marketplace add dimipaun/fugaro`;
- in Claude Code, `/plugin install fugaro@fugaro` (or `/plugin marketplace update fugaro` when it is already installed).

Each of these sequences is repeated in the README, `docs/gcp-setup.md`, `docs/release.md`, the `/new-release` operator list, `fugaro doctor`'s fixes, the staleness warning, and the text of init's plugin stage. The owner wants one command, and a skill that knows how to do all of it.

## Behaviour

```
fugaro upgrade [PATH...] [--check | --local | --yes] [--allow-fork]
               [--repo R] [--workflow W]... [--image-source S] [--expect-digest KIND=sha256:H]...
               [--config F] [--project P] [--gcp-project G] [--region R]
```

The first line names the binary (`fugaro upgrade with fugaro 0.5.2`) and says the command never upgrades `fugaro` itself. **The upgrade sequence is `brew upgrade dimipaun/tap/fugaro && fugaro upgrade`.**

Each PATH (the default is `.`) is a checkout, and each is processed on its own. Two paths in the same checkout count once. Each checkout runs three steps in order and stops at the first step that fails:

1. **pin**: what `fugaro update-skills` does. The plugin's marketplace and enablement are merged into `.claude/settings.json`, pinned to this binary's tag. The diff is shown, and the command never commits. A fork's marketplace moves only with `--allow-fork`, and a file that is not valid JSON is never rewritten. One rule is new: a pin **newer** than this binary is never moved down. Instead, that checkout stops and the output names `brew upgrade`. This matters because `update-skills` itself does lower such a pin today, even though its own fix text says it never does.
2. **plugin**: the plugin is installed or updated in Claude Code through the `claude` CLI on `PATH`:
   - It reads `claude plugin marketplace list --json` and `claude plugin list --json`.
   - It adds the marketplace if it is missing, otherwise it updates it.
   - It installs the plugin at user scope if nothing applies to this checkout. Otherwise it updates each install that applies (user scope, or project or local scope recorded for this checkout), at that install's own scope.
   - When everything is current, it makes no change call.

   It then lists the installs again and compares the result with this binary. The output says that running Claude Code sessions must be restarted to apply it. Without `claude` on `PATH`, the step prints the slash commands to type in Claude Code instead.
3. **cloud**: `fugaro image refresh` for the checkout's repository. The refresh runs in the same process, through `runImageRefresh`; it is not a shell-out. `--yes` passes through, and without it the refresh is interactive as today.

   The step is **skipped**, with the reason shown, when:
   - there is no `fugaro.yaml`;
   - there is no local project config (a teammate's machine);
   - the repository is not onboarded;
   - the binary is a development build.

At the end, a summary gives one line per checkout, with each step's state (`current`, `done`, `stale`, `skipped`, `failed`, `not run`) and, for a checkout that stopped, the exact line to rerun. The exit code is the worst over all checkouts: 0 when everything finished or was current, 1 for a refusal, a failed local step or (with `--check`) anything stale, and 2 for a cloud failure. When a rerun finds everything current, it prints `nothing to do` and changes nothing.

**`--local`** runs pin and plugin only. **`--check`** writes nothing, runs no `claude` command and makes no cloud call. It reads three things:
- the settings file;
- Claude Code's `installed_plugins.json`;
- the local config's `base_images`.

It prints what each step would do and exits 1 if anything is stale, so CI can run it without credentials. It cannot see a build that failed after the base moved, because build records live in the cloud; the output says so. Without `--yes`, `--local` or `--check`, the command needs a real terminal and refuses before any step runs.

**Inside a coding agent's session** (`CLAUDECODE` and the like), only the local steps run. The cloud step is skipped and the output prints the exact command to run in the user's own terminal, with and without `--yes`. The exit code is 0 when the local steps succeeded: the command did everything it may do there, and the skipped step is named. `--check` reports the cloud state the same way everywhere, so CI and the skill still see `stale`. `fugaro upgrade` never even reads the cloud in that session, and the refusal inside `image refresh` stays as a backstop.

**The skill `/fugaro:upgrade`** works through the upgrade in this order:
1. It runs `fugaro version` and `fugaro upgrade --check`, then explains in plain words what is stale and why.
2. It runs `fugaro upgrade --local`, shows the diff to commit, and says a restart is needed.
3. It checks again with `fugaro upgrade --check`.
4. It hands the user one command for their own terminal, in a `user-runs` block: `fugaro upgrade --yes`. It says why: a coding agent's session cannot apply cloud changes, by design.

It never runs the cloud step or any `claude plugin` command itself, never edits settings by hand, never passes `--allow-fork`, and never asks for or handles a secret.

## Safety

- **The `claude` calls.** Every argv is one of seven fixed lists, or the marketplace add, whose one variable word is the marketplace repository: `dimipaun/fugaro`, or a fork that the checkout's pin names, passed only with `--allow-fork` and only after it matches a GitHub `owner/name` pattern that cannot start with `-`.

  An allowlist (`claudeplugin.Allowed`) rejects anything else before anything is executed. There is no shell, and stdin is the null device, so a prompt reads end of file and refuses. The command never passes `-y`, `--accept-command` or `--dangerously-skip-permissions`. Each call has a timeout: 30 s for a list, 3 min for a change.
- **Finding `claude`.** It is found with `exec.LookPath`, and a relative result (for example from a `.` entry in `PATH`) is refused. The resolved absolute path is printed before the first change.
- **Output.** At most 40 lines of `claude`'s output are printed, each one passed through `pluginwire.Printable`.
- **The marketplace already on the machine.** A `fugaro` marketplace on this machine that names another source is never replaced. The checkout stops and the output names `/plugin marketplace remove fugaro`.
- **Writes by Claude Code itself.** If a `claude` call changes `.claude/settings.json`, the step says so and asks the user to review it with `git diff`.
- **Credentials.** `--check` and `--local` need no credentials and make no cloud call. The tests pin this with a credentials path that does not exist, and with a build-record reader that fails the test if anything calls it.

## Non-goals

- Upgrading the binary.
- Pinning Claude Code's own marketplace to the tag: `claude plugin marketplace add` takes no ref, so it follows `dimipaun/fugaro`'s default branch. The release PR bumps the plugin version there before the tag, so it equals the latest release.
- `--json` (refresh has none either).
- Teammates' cloud steps.
- Changing `update-skills` or the `fugaro image refresh` hints that name a single workflow.
- A `fugaro.yaml` migration.

## Decisions (each one can be vetoed in the plan)

The plan lists U1 to U18. The ones that shape the behaviour:
- install at user scope, and update every install at its own scope (U2);
- the marketplace follows the default branch, and a plugin newer than the binary after the update is a note, not a failure (U3);
- `--check` reads local files only (U6);
- in an agent's session the command exits 0 (U7);
- a terminal is refused up front without `--yes` (U8);
- a teammate's checkout skips the cloud step (U9);
- the pin is never lowered (U10);
- the skill uses no `allowed-tools` frontmatter, because the lint allows only `name` and `description`, and `allowed-tools` pre-approves tools rather than restricting them; the lint gains one exception, `--yes` on `fugaro upgrade` inside a `user-runs` block (U14);
- every hint names `fugaro upgrade --local` (U15).

## Delivery

Release **0.5.2**, with `refresh --yes`. One PR holds Tasks 1 to 11 of the plan. Task 12 writes `docs/releases/v0.5.2.md` and cuts the release through `/new-release 0.5.2`. The new skill ships automatically: `scripts/release.sh` sets every skill header and `plugin.json` to 0.5.2 (`scripts/bump-plugin-version.sh`), and the plugin lint covers `plugin/skills/upgrade/` because it walks every skill.

The first upgrade *into* 0.5.2 needs the CLI command, because the 0.5.1 plugin has no `upgrade` skill. From then on, `/fugaro:upgrade` covers the local half.

What has not been verified live:
- the change calls of `claude plugin` (only `marketplace list --json` was run inside a session, on 2026-10-08);
- whether `claude plugin update --scope project` rewrites the committed settings file.

`docs/gcp-live-checklist.md` gets Check 31 for both.
