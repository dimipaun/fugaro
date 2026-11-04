# Reconciliation: what the tree holds, and what this design would change

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved.**

Every claim in this file was read on `origin/main` at **`b41709ea`** (2026-11-04, `git grep` and the files
opened) — the merged tree this branch carries, **`92c6b187`** — and §8 records what the 2026-11-01 read
missed. Line numbers are `b41709ea`'s. **Where this file disagrees with [spec.md](spec.md), this file
wins.** The census in §4 is the one thing here that a task must re-run rather than trust: a line number
ages, and a count ages faster.

## 1. What exists (F1–F10)

| # | The claim | Where it holds |
| --- | --- | --- |
| F1 | `fugaro update` already converges the checkout it stands in | `internal/cli/init_checkout_upgrade.go:39` — `checkoutSteps = {pin, plugin, cloud}` with `LocalUnderAgent()` at `:63`; the stage is declared after the repository and layer stages (`internal/initflow/stage.go:175`, `checkout` needs `repository`) |
| F2 | One converge engine, two doors | `internal/cli/init.go:281`'s `newInitCmdWith` builds `setup` and `update` (`update.go:102`) from the same `internal/initflow` stages; the stage order (`internal/initflow/stage.go:139`, `initStages` at `internal/cli/init_stages.go:168`) is `Preflight, Project, Services, Installation, Firebase, Images, Installation2, Secrets, Plugin, Credentials, Repository, Rules, Policy, **Layer**, Checkout, Compute`. **`Layer` is new since this file's first read** — the project layer's publish, added by the settings/config-management work (§8) |
| F3 | The plugin merge lives in the **plugin** stage, which runs **before** the repository stage | `internal/cli/init_plugin_stage.go`; the stage list above puts `Plugin` at 8 and `Repository` at 11 |
| F4 | `pluginwire` is narrow and refuses to be clever | `internal/pluginwire` merges only `extraKnownMarketplaces.fugaro` and `enabledPlugins["fugaro@fugaro"]`, needs a strict `vX.Y.Z`, never rewrites a file it cannot parse, never moves a pin down, and never commits |
| F5 | **The `claude` install is already at user scope** | `internal/claudeplugin/claudeplugin.go:43` — `installUser = claude plugin install fugaro@fugaro --scope user`; `addPrefix` adds the marketplace at the same scope; the argv allowlist (`Allowed`), the empty working directory, the scrubbed child env (which drops `CLAUDECODE`) and the timeouts are all `claudeplugin`'s |
| F6 | `Decide` maintains **every applying scope**, not just user | `internal/claudeplugin/plan.go:80`'s `stale` loop over `scopes` (`claudeplugin.go:46`, `local`, `project`, `user`); `Applying` at `:41`, `Effective` at `:62` |
| F7 | doctor has the classes this design needs | `internal/cli/doctor.go:47`'s `doctorCheck{ID, OK, Problem, Fix, Severity, Class}`, classes `actionable|update|decision|measure|audit` (`doctor_render.go:32`–`:53`); `doctorUpdatePrefixes` (`doctor_render.go:118`) and `doctorMeasureIDs` (`:198`) in `internal/cli/doctor_render.go`; `plugin-install` sits in `doctorMeasureIDs` (`:204`) **and not** in `doctorUpdatePrefixes`, under the class comment's own "not here yet" note (`:85-92`) |
| F8 | The `plugin/` tests check every command and flag a skill names against the CLI | `plugin/skillcli_test.go`'s `resolveSkillCommand`, `plugin/skills_lint_test.go`, `plugin/plugin_test.go` |
| F9 | Docs are tested, **and a new `docs/` directory needs no pin test** | the many `internal/cli/docs_*_test.go` files pin sentences, counts and citations in the files they name; there is no registry test that walks `docs/` as a set, so `docs/plugin-user-scope/` needs no entry anywhere. `docs_live_init_census_test.go` walks `docs` for the retired `init` command's spelling, asserts the set of files naming it is **empty**, and is the one test in the tree a new document under `docs/` can break by accident — which is why the four files here spell it as `` `init` `` and never with the command's word in front |
| F10 | A cloud run never loads a plugin, from the code | `internal/agent/agent_test.go:907` (`TestClaudeArgsNeverSideloadPlugins`) forbids every plugin, marketplace, trust and settings-source flag in the headless launch; `internal/agent/settings.go:304` (`SettingsFiles`) is where settings files are *read*, and review mode strips `.claude` and `.claude-plugin` from every reviewed checkout (`internal/runner/reviewmode.go:266`, `stripClaudeConfig`) |

**F5 is the fact that reshapes the brief's own wording.** The plugin was never *installed* at project
scope: the `claude` call is `--scope user` today. What is at project scope is the **enablement** in the
committed `.claude/settings.json`. So this work moves the plugin's *reach*, not a `--scope` argument, and
any task that looks for a scope to change will not find one.

## 2. What is missing, what conflicts, and what the move costs (F11–F22)

| # | The finding | Kind |
| --- | --- | --- |
| F11 | **The pin is the delivery mechanism.** A repository that commits no `.claude/settings.json` gives its teammates no skills, and `plugin/skills/setup/SKILL.md:154` tells the agent to put that file's change into the pull request — so the committed file is the intended outcome, not a side effect | the thing this design undoes (D940) |
| F12 | **A subdirectory of a checkout works, with one residue.** `pluginwire.Locate` walks up from `.` with `filepath.Abs` and stops at `$HOME`, so a command in `repo/sub/dir` finds `repo/.claude/settings.json`. The residue: `Locate` returns the path as typed while `checkoutKey` (`internal/cli/upgrade.go:284`) resolves symlinks, and with `fugaro upgrade`'s multi-`PATH` dedup gone the two can disagree about whether two invocations are the same checkout | gap — closed by U3 |
| F13 | **A repository that is not enrolled is the regression the naive move would cause.** The plugin step is in the **checkout** stage, and the checkout stage is declared *after* the repository and layer stages (F1): a session whose repository stage returns needs-you — not onboarded, no GitHub App, a run in flight — never reaches the plugin. The **plugin** stage (F3) runs before it | conflict — decides U2 |
| F14 | **A linked worktree already works.** `.git` is a file there, and `Locate`'s `os.Lstat` walk handles it, so it returns the worktree root. The pin was what made two worktrees of one repository diverge (one committed the file, one had it at another ref); dropping the default pin closes it | gap — closed by D940, nothing to build |
| F15 | **A checkout of another project is already skipped.** `checkoutStage.namesAnotherProject` (`internal/cli/init_checkout_upgrade.go`) skips the step in a checkout whose `fugaro.yaml` names a different project; only its wording ("its plugin pin and its job images") changes | gap — closed by U2's wording |
| F16 | **`fugaro update` has no `--check` and no `--local`** today | `internal/cli/init.go`'s flag list |
| F17 | **`update --plan-only` is not offline.** A stage's `Plan` may read the cloud (the images stage reads the bucket and the registry), so it is not the "writes nothing, calls nothing" mode ruling C needs | conflict — decides D945 |
| F18 | **`fugaro upgrade` is a second front door**: five steps, a `PATH` taken several times, an exit code that is the worst of the five, and a skill to explain it. See §4 for the census | the thing ruling A retires |
| F19 | **Nothing removes a pin today.** The pin step only ever writes or moves the pin up; there is no code path that takes the two entries out of `.claude/settings.json` | gap — decides D943, built by U4 |
| F20 | **These tests will go red, and each is named by the task that owns it** (the list is in §5) | the gate |
| F21 | **A repository that never opted in fails `fugaro doctor --plugin --strict` today.** `pluginwire.Severity()` gives `info` only to `NotInstalled` and `CannotCompare`; `not wired` is a `warning`, and `--strict` is the mode a repository's CI runs (doctor's own help calls `--plugin` "offline, no credentials, safe for CI") | conflict — decides D942 |
| F22 | **What the pin pays for, which the user-scope install does not**: a teammate who has run nothing still gets the skills, and a repository can say "required". Stated as a trade-off in [spec.md](spec.md) §10 rather than designed away | cost — D949 |
| F23 | **A teammate who is not the owner can be stopped before the plugin, by a cloud stage, and the loop's stop is why.** `converge` "stops at the first stage that needs the user or fails: nothing after it runs" (`internal/initflow/loop.go:331`) and marks the rest `blocked: waits for <stage>` (`:339`); the images stage and the secrets stage are before the plugin stage (`internal/cli/init_stages.go:170`, `:185`) and are not exempt from `newMachineGuard` (`init_new_machine.go:94`), which exempts `Installation` and `Plugin` **by name** (`:95`). A `brew upgrade` run is exactly the run in which the images stage has the release's new base image to pin. `--local` could not be stopped this way: it had no cloud stage (`upgrade.go:141-144`) | gap — D954, closed by U8 |
| F24 | **The plugin stage's repository gate is the pin's, not the user-scope install's.** `pluginwire`'s write is `.claude/settings.json` in somebody's checkout, so the stage asks `authState` (`init_plugin_stage.go:109`) and, where the checkout's repository is not in the machine's config, is needs-you with the line naming `fugaro setup` (`:112-117`) — `update`'s `enrolling()` is false (`internal/cli/init.go:242`). A new machine's config lists no repositories (copied from the installation's outputs, `newMachineConfig`), and the machine install needs none of them: outside a checkout the stage even prints the **pin's** snippet and skips (`:81-88`) | gap — D955, closed by U8 |
| F25 | **§8 says a pin that cannot be removed is not a failure "and the run continues to the next stage"; the code stops the loop.** A fork's marketplace, a foreign marketplace and an invalid file are needs-you (`init_plugin_stage.go:123-132`), and needs-you is the stop. The shape for what §8 asks already exists: `Outcome.Left` continues the loop **and** sets the exit code (`loop.go:439`, the exit code itself at `:93`) | contradiction — closed by U8 |
| F26 | **D949's answer leaves the pin's read side with no writer.** With the merge deleted, nothing in the tree writes `extraKnownMarketplaces.fugaro` or `enabledPlugins["fugaro@fugaro"]`: `internal/pluginwire`'s `Plan`/`Wire`/`Snippet` and `Status`'s pin states, doctor's `plugin-pin` check (`internal/cli/doctor_render.go:204`'s `doctorMeasureIDs`), and `fugaro update-skills`'s `--check` all read or write a pin only a pre-answer installation has. They keep a job for the migration window U5 opens — a checkout that still carries a pin has to be told so and offered its removal (D943) — and only the parse/rewrite machinery U5 reuses is certain to outlive it | cost of D949 — recorded, not designed away |

## 3. Decisions (D940–D955)

Sixteen. **The owner answered four of them on 2026-11-04** — D943, D949, D950 and D954, the four with a
bold answer below; the other twelve keep their recommendations and stand as written until a later round
reads them. One answer departs from its recommendation and says so. Each recommendation is first, with
the alternative's cost. D954 and D955 are the follow-up round's (§7): the teammate who is not the owner,
answered from the code.

| D | Question | Recommendation | The alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| **D940** | Where do the skills come from by default? | **The machine, at user scope**; the repository pin is opt-in and never the default (spec §3). The `claude` install is already `--scope user` (F5), so this moves *enablement* out of the committed file | Keep the pin as the default: a teammate still gets the skills with no command — at the cost of a third-party `settings.json` in every enrolled repository's trust surface, two answers for two checkouts of one repository, and the very step ruling A removes | |
| **D941** | Which stage does the plugin step live in, and under what marker? | **The `plugin` stage keeps its position** (it runs before `Repository`, F3) and becomes `LocalUnderAgent`; the checkout stage keeps the `cloud` step only and loses `pin`/`plugin` | Leave it in the checkout stage: a session whose repository stage returns needs-you never reaches the plugin (F13), which is the one gap this work could ship as a bug | |
| **D942** | What does doctor compare, and in which class? | `plugin-install` compares the **user-scope** install with **this CLI** (against the pin where one is opted in), becomes a `warning` when nothing is installed, and **moves into `doctorUpdatePrefixes`**; `plugin-pin` keeps its ID and meanings and `not wired` becomes `info`, so `doctor --plugin --strict` stops failing a repository that never opted in (F21) | Leave the classes as they are: every enrolled repository's CI fails on a check whose fix is a command the repository cannot run, and the update class keeps a fix line (`fugaro upgrade --local`) for a command that no longer exists | |
| **D943** | What happens to a repository carrying an old pin? | `fugaro update` **shows the diff and offers its removal**, never commits, and in an agent's session prints the diff and writes nothing (spec §8) | Leave the pin for the user to delete by hand: that is a second step the HARD RULE forbids, and it leaves the checkout declaring a marketplace nobody maintains | **Answered 2026-11-04, as recommended.** `fugaro update` shows the diff of the two pin entries and offers their removal, never commits, and in an agent's session prints the diff and writes nothing (spec §8) — because a second command to remove it is what the HARD RULE forbids, and the write the offer makes is in the user's own checkout |
| **D944** | What does the setup skill write, and what still gates the release? | The wizard's plugin stage writes **no** `.claude/settings.json` by default; `--pin` keeps today's behaviour; every `plugin/` test that checks a skill against the CLI **keeps gating**, re-pointed at `update` (spec §9) | Drop the pinning behaviour from `setup` entirely, `--pin` with it: a repository could then never say "the skills are required", which F22 names as the pin's one remaining job | |
| **D945** | What is the session-start check? | **`fugaro doctor --offline --json`**, a new `--offline` flag on `doctor`: every check whose answer is a local file, and a recorded skip for the rest (spec §7) | `fugaro update --check --offline --json`: a second JSON document, a second version and a second set of IDs beside doctor's (already in `console/docs/json-contract.md` §6.4) — and it answers "not a Fugaro checkout" in the repositories `working` is most often read in | |
| **D946** | What is the check's output contract? | doctor's document keeps its shape and its version; `--offline` adds **one additive optional field** on `doctorCheck` (`skipped`), and a skipped check is `ok: false` with a `problem` saying why (spec §7) | A new versioned document: the console, the mobile app and every scripted `doctor --json` reader would need a second parser for facts they already read | |
| **D947** | What exactly does the skill say? | The sentence in spec §7, whose second half is `plugin/skills/upgrade/SKILL.md:27`'s own words already pinned by `plugin/upgrade_skill_test.go:18,20` | Write it fresh: the two pinned strings stop matching where they are pinned, and the `plugin/` gate covers less of the sentence that does the work | |
| **D948** | Which of `upgrade`'s flags survive on `update`? | **`--check` survives**, added to `update`; **`--local` does not** — the `LocalUnderAgent` marker absorbs it, and `fugaro update-skills` is the plugin-only command; the cloud flags are already `update`'s (spec §5) | Keep `--local`: a second spelling of "the local half" beside a marker that already says it, and a flag whose only caller is gone | |
| **D949** | Does the pin survive at all? | **Yes, as `fugaro update-skills --pin`** — one job: a team that wants the skills required (F22). It is not a scope, it does not change the install, and the marketplace ref follows the default branch | Delete the merge outright: a repository that wants the skills required has no way to say so, and `pluginwire`, its states and its tests become dead code in one commit | **Answered 2026-11-04: DELETE the merge outright — NOT THE RECOMMENDATION.** The owner took the alternative and its cost: no `fugaro update-skills --pin`, nothing in the tree writing `extraKnownMarketplaces.fugaro` or `enabledPlugins["fugaro@fugaro"]` (F26), the pin's read side surviving only for U5's migration window, and F22's "a repository can say the skills are required" given up as an accepted loss |
| **D950** | What is left of `fugaro upgrade`? | **Nothing — not even a tombstone.** The name becomes cobra's unknown command | A tombstone (the `internal/cli/retired.go` shape): one hidden command, one message, one test and one census exemption, to turn a converging run into exit 1 for the user the HARD RULE is about — and `fugaro update` is what they wanted anyway | **Answered 2026-11-04, as recommended.** Nothing, not even a tombstone: the name becomes cobra's unknown command — a tombstone's only job would be to turn a converging run into exit 1 for the one user the HARD RULE is about |
| **D951** | What can the offline check answer? | The table in spec §7: the plugin install, the pin, the base image and the local image config **yes**; the deployed rules generation, the published policy node and the CLI's own release **no, and said to be skipped** | Pretend to answer them: a guessed "ok" on a credential-only fact is the silent pass `doctor`'s `unread` state exists to prevent | |
| **D952** | Does the session-start check reach the network? | **No.** Detecting a newer release stays opt-in (`doctor`'s own `cli-version` check in the full run, `internal/cli/doctor.go:172`) and the skills never trigger it | Check the network at session start: an agent that reaches out on the user's behalf, unasked, at the start of every session — and a check that fails offline, which is the state a nudge must survive | |
| **D953** | What becomes of the `upgrade` skill? | **The `update` skill**: same job (check, explain, hand over the one command), `plugin/skills/upgrade/` renamed to `plugin/skills/update/`, its body rewritten for two commands, `plugin/plugin_test.go:134`'s list swapped one for one | Drop the skill: the one place a user's agent is told how to read the converge's verdict, and its `plugin/` coverage, go with it | |
| **D954** | A teammate who is not the owner runs `fugaro update`: what makes it reach the plugin? | **A machine-local stage is not blocked by a cloud stage that stopped.** `initflow` gains the marker the tree keeps by hand three times (`LocalUnderAgent`, `newMachineGuard`'s name list, `NeedsBilling`'s case list), the plugin stage implements it, `converge` runs it even after `stopped != ""` and says so in the stage's line, and `newMachineGuard` reads the marker instead of `initflow.Plugin` (spec §11, U8) | Move the plugin stage to just after `installation` (`init_stages.go:169`): a smaller diff, at the cost of a plugin stage's needs-you ending the run **before** the images, secrets and repository work the owner's run does today — and of a stage order the plan view and `docs/design/upgrade.md` show every reader | **Answered 2026-11-04, as recommended.** A machine-local stage marker: the third marker the tree keeps by hand is the one `initflow` gains, `converge` runs the marked stage even after a cloud stage stopped, and `newMachineGuard` reads the marker instead of naming `Plugin` — because a teammate's run must not end before the images, secrets and repository work, and a stage order every reader can see is not worth redrawing |
| **D955** | Does the user-scope plugin install still need the checkout's repository authorised? | **No — the gate is the pin's.** The install half drops `authState`/`enrolling()` and the need for a checkout at all (outside one it prints the pin's snippet and skips today); the gate stays for the checkout write U5 adds, and for the pin's `--pin` path (spec §11, U8) | Keep the gate: every teammate is sent to `fugaro setup` — a second command — before their own machine's plugin is installed, for a write that is not the repository's, and a new machine's config lists no repositories anyway | |

**Why these numbers.** The block was read by hand on 2026-11-01 and re-read by the launcher on
**2026-11-04** (README's "Numbers this work holds"), and the answer to a moving tree is a list of blocks
rather than one highest number: **D1000–D1189 are on `main`** (highest written, `D1189`);
**D1190–D1219 belong to #1676** (the repo-image removal design); **D1220–D1259 belong to the
stateless-orchestrator work**; **D1260–D1289 belong to the #1678 design**; and **D1290–D1309 are #1492's
new block**. Nothing on `main` or in any of those four blocks reaches below **D1000**, and no open work
has claimed D940–D959 since this folder reserved it, so **D940–D955 do not collide and stay as written**:
the renumbering other designs took this round does not move this one. D940–D947 are the design's own
decisions, D948–D953 the rulings' smaller ones, **D954–D955 this work's follow-up round** (§7, and the
same directory, so nothing outside it could have taken them between the two commits); **D956–D959 stay
reserved and unused**. A later task re-reads the launcher's block list rather than trusting this
paragraph.

## 4. The census: everything that names `fugaro upgrade`

Eight commands, run on two trees — and **every count below says which tree it came from**, because the
same command run on a branch that carries these four documents and on `main` gives different answers:

```
# on main at b41709ea (2026-11-04), the tree this design is reconciled against
$ git grep -l 'fugaro upgrade' b41709ea -- . | wc -l          # 115 files
$ git grep -o 'fugaro upgrade' b41709ea -- . | wc -l          # 488 occurrences
$ git grep -lw 'upgrade' b41709ea -- . | wc -l                # 280 files   (the bare word, for scale only)
$ git grep -ow 'upgrade' b41709ea -- . | wc -l                # 1449

# on this branch's tree (2026-11-04), i.e. main plus these four documents
$ git grep -l 'fugaro upgrade' -- . | wc -l                   # 119 files
$ git grep -o 'fugaro upgrade' -- . | wc -l                   # 544 occurrences
$ git grep -l 'fugaro upgrade' -- docs/plugin-user-scope | wc -l   # 4 files
$ git grep -o 'fugaro upgrade' -- docs/plugin-user-scope | wc -l   # 56 occurrences
```

On `main` by area (files): `docs/` 56, `internal/` 46, `plugin/` 5, `website/` 4, `console/` 1, plus
root's `README.md` and `CLAUDE.md` and `.claude/skills/new-release/SKILL.md` — 115 in all. **The branch
adds exactly four files and 56 occurrences, and they are this directory's own**: `docs/` reads 60 here
and 56 on `main`, and 488 + 56 = 544, 115 + 4 = 119. A census that does not say so reads as if the
tree were busier than it is, which is how an earlier draft of this section reported 539 under a
`main` heading.

| | `main` at `3d6ed04d` (2026-10-31, this file's first read) | branch tree at `28425eba` (2026-11-01) | `main` at `b41709ea` (2026-11-04) | **branch tree (2026-11-04)** |
| --- | --- | --- | --- | --- |
| files naming `fugaro upgrade` | 120 | 119 | 115 | **119** |
| occurrences | 507 | 531 | 488 | **544** |
| of which this directory's documents | 0 (it did not exist) | 4 files, 41 occurrences | 0 | **4 files, 56 occurrences** |
| `docs/plans/2026-10-08-upgrade.md` | 126 lines, 147 occurrences | 126 lines, 147 occurrences | 126 lines, 147 occurrences | **126 lines, 147 occurrences** |
| bare `upgrade`, `-w` (word boundary) | 259 files, 1377 occurrences | 260 files, 1471 occurrences | 280 files, 1449 occurrences | **284 files, 1585 occurrences** |

Read the four columns with the method in the command block above, because an earlier draft of this table
mixed two of them and the header hid it: the 2026-11-01 and 2026-11-04 readings of the bare word were
taken **without `-w`** (they read 276 files/2059 occurrences and 300/2186 on the branch trees, and
`main` without `-w` reads 296/1997), and its 2026-11-04 column held **branch-tree** numbers under a
`main` heading (119 files, 539 occurrences then — 544 now, after this round's own edits). Both are the
sin this section warns about, committed by the section itself; the table above is re-measured
uniformly — `-l` for files, `-o` for occurrences, `-w` for the bare word, and every column labelled
with its tree.

What the corrected numbers say. **`main`'s census is falling, not growing**, and the commits are the
proof: eleven files stopped naming `fugaro upgrade` between `3d6ed04d` and `b41709ea` — `skillwarn.go`,
`init_review.go`, `updateskills_test.go`, `init_plugin_stage_test.go`, `plugin_notices_test.go` and
`run.go` in **#1495** (`46907837`, "every message that sent the owner to `fugaro upgrade --local` says
`fugaro update`"), `doctor_render.go` and `doctor_render_test.go` in **#1500**, and `doctor.go`,
`launch_budget.go` and `project_cmd.go` in **#1503** — while six started naming it
(`docs/managed-agent-converge/reconciliation.md`, `docs/releases/v0.14.2.md`, `init_policy_publish.go`
and its neighbours). Net, on a tree that grew: 120 → 115 files, 507 → 488 occurrences. This branch's own
56 occurrences are the only reason its tree reads higher, which is why the number that matters to a task
is **488 on `main`**, not the branch's. The step from the first read to the second went the other way
for the same reason in reverse, and the intermediate is measurable: the `main` this branch merged at
`28425eba` is **`46907837` itself — #1495** — with 115 files and **490** occurrences, and the branch
tree adds these four documents to it: 119 = 115 + 4, 531 = 490 + 41. The bare word `upgrade` stays
irrelevant in bulk: it is `brew upgrade`, `image_refresh.go`'s own name, a policy *version* upgrade and
ordinary prose. **The command string is the census**; a task re-runs the greps above on `main` at the
commit it is working from, with `-l` for files and `-o` for occurrences and never the two mixed, and
works from its own output rather than from this list.

### 4a. Three classes, and only one of them changes

**Class 1 — living text that must change in the task that changes its subject** (the implementation
plan's own file lists name these per task):

- **The command and its stage:** `internal/cli/upgrade.go`, `internal/cli/upgrade_plugin.go`,
  `internal/cli/upgrade_cloud.go`, `internal/cli/init_checkout_upgrade.go`,
  `internal/cli/init_plugin_stage.go`, `internal/cli/image_refresh.go`,
  `internal/cli/init_policy_migrate.go`, `internal/cli/init_policy_publish.go`,
  `internal/cli/init_rules_stage.go`, `internal/cli/init_stages.go`, `internal/cli/project.go` — most of
  these are `Long` text, a fix string or a doc comment. **Six names left this list since the first read
  and the tree is the better for it:** `internal/cli/doctor.go`, `doctor_render.go`, `updateskills.go`,
  `skillwarn.go`, `run.go` and `init_review.go` no longer name `fugaro upgrade` at all (#1495 and #1500
  re-pointed them at `fugaro update`; §8).
- **The plugin's own packages:** `internal/claudeplugin/{claudeplugin.go,plan.go}`,
  `internal/pluginwire/status.go` (its `Fix()` strings), `internal/initflow/stage.go` (a stage's own
  comment), `internal/projectpolicy/version.go`, `internal/infra/{spec.go,firebasedb.go}`,
  `internal/runner/projectpolicy.go`.
- **The skills:** `plugin/skills/upgrade/SKILL.md` (10), `plugin/skills/upgrade/reference/states.md` (4),
  `plugin/skills/working/reference/followup.md`.
- **The living docs:** `README.md`, `CLAUDE.md`, `docs/release.md` (forbidden to this PR; a later task
  owns it), `.claude/skills/new-release/SKILL.md`, `docs/design/upgrade.md` (14),
  `docs/design/v1.md`, `docs/policy-migration.md`, `docs/base-image-migration.md`,
  `docs/multi-model.md`, `docs/project-layer.md`, `docs/project-claude-accounts.md`,
  `docs/setup-wizard-w2.4-already-landed.md`, `docs/settings-commands/*`, and the design docs under
  `docs/design/` and `docs/credentials-service/`, `docs/setup-wizard/`, `docs/task-governance/`,
  `docs/website/`, `docs/windows-cli/` that name the command in passing.
- **The website and the console:** `website/content/cli-reference.md`, `website/data/install.yaml`,
  `website/src/pages/getting-started.astro`, `website/design-ref/GettingStartedV4.dc.html`,
  `console/docs/json-contract.md`.
- **The tests.** §5.

**Class 2 — dated records that must NOT change.** `docs/releases/` (22 files: v0.6.0 … v0.14.2) and the
dated plans under `docs/plans/` (`2026-10-08-upgrade.md` alone carries **147** of `main`'s 488
occurrences).
A release note
is what shipped, and rewriting one to name a command that did not exist then is a false record. This is
already the tree's own rule: `internal/cli/docs_release_notes_test.go` calls them "a dated record that
keeps its own spelling" and translates the old spelling instead (see §5). **The tasks do not touch them.**
Release notes from 0.15.0 on do not name the command at all, which is the census telling the same story
as D950.

**Class 3 — historical plans that name the design being replaced.** `docs/design/upgrade.md` is *not* a
dated record: it is the living design of the command being retired and it is pinned by a test. U6 owns
it.

### 4b. The strings that survive, and the two that do not

`brew upgrade dimipaun/tap/fugaro` **stays** in every document and every test: `brew` still upgrades the
CLI, and `plugin/upgrade_skill_test.go:21` pins it on its own. What changes is only the tail of the
sentence it appears in — `brew upgrade dimipaun/tap/fugaro && fugaro upgrade` becomes `… && fugaro
update`, in `plugin/skills/upgrade/SKILL.md:32`, `README.md`, `docs/release.md` and
`.claude/skills/new-release/SKILL.md`, and in the two tests that pin that exact string
(`internal/cli/docs_upgrade_test.go:49`, `internal/cli/docs_project_named_oauth_tokens_test.go:540`).

`fugaro upgrade --local` and `fugaro upgrade --check` are the two spellings with no `update` equivalent
under the same name: the first becomes `fugaro update` where the local half is meant and `fugaro
update-skills` where the plugin alone is (D948), the second becomes `fugaro update --check`.

## 5. The tests this breaks, and who owns each

| Test | What it holds | Task |
| --- | --- | --- |
| `internal/cli/docs_upgrade_test.go` (`TestDocsNameUpgrade`, `TestDocsDesignListsEveryUpgradeStep`) | `fugaro upgrade` and its real flags in `README.md`, `docs/release.md`, `.claude/skills/new-release/SKILL.md`, `docs/design/v1.md`; the `brew upgrade … && fugaro upgrade` sentence; `/fugaro:upgrade`'s README line; `docs/design/upgrade.md`'s five-step list | U6 |
| `internal/cli/docs_upgrade_local_test.go` | `--local`'s own doc pin | U6 |
| `internal/cli/upgrade_skill_doc_test.go` (`TestUpgradeSkillNamesEveryState`) | reads `plugin/skills/upgrade/reference/states.md` at that path and checks every `stepStates` and every `upgradeSteps` name, plus `--check`/`--local`/`--yes` on the command | U5, U6 |
| `internal/cli/docs_release_notes_test.go` | **pins `{"upgrade","local"}` and `{"upgrade",""}` against `docs/releases/v0.9.0.md`, which must not be edited** — the resolution is the file's own precedent: `notesCommand` already translates a dated spelling (`init` → `setup`, `:441`) and `notesFlag` already translates `--firebase` → `--firebase-project` (`:453`), both under "these notes are a dated record that keeps its own spelling". U4 adds the same two translations (`upgrade` → `update`, and `--local` → the bare command) and re-points `notesCommand`'s paths | U4 |
| `internal/cli/docs_project_centric_test.go`, `docs_project_named_oauth_tokens_test.go` | `fugaro upgrade` in plan sections and a rollout sentence, incl. the pinned `brew upgrade … && fugaro upgrade` | U6 |
| `plugin/plugin_test.go:134`, `plugin/readme_test.go:45`, `plugin/skills_lint_test.go:583,749,1758-1765`, `plugin/upgrade_skill_test.go` | the skill list, the plugin README's phrases, `userRunsAllowed` (still listing `fugaro upgrade` on `main`), `upgradeYesOnly`, the skill's own sentences | U5 |
| `internal/cli/{upgrade_test.go,upgrade_hints_test.go,upgrade_plugin_test.go,upgrade_cloud_test.go,upgrade_cloud_step_test.go,image_refresh_upgrade_test.go,init_checkout_upgrade_test.go,init_plugin_stage_test.go,init_policy_migrate_test.go,checkouts_test.go,cascade_test.go,plugin_notices_test.go,run_allowlist_test.go,left_commands_test.go,init_test.go,init_firebase_test.go,doctor_render_test.go,doctor_policy_test.go,updateskills_test.go,project.go's own}` | the command's own tests: they move with it, or go with it | U1–U3 |
| `internal/{claudeplugin,pluginwire,projectpolicy,infra,runner}/*_test.go` | `Fix()` strings, the scope loop, `projectpolicy`'s version-upgrade prose, and `internal/runner/sealskew_test.go`'s sentence about which command a skew tells the user to run — mechanical, but each names the command | U1–U4 |

**No test in the tree reads `docs/plugin-user-scope/`.** F9: the docs tests name the files they pin, and
nothing walks `docs/` as a set, so this design directory needs no pin test and this pull request adds
none.

## 6. What was read, and what was only counted

**Read, at `3d6ed04d`** (and re-read at `b41709ea` for this round: §8): `internal/cli/{upgrade.go, upgrade_plugin.go, updateskills.go,
init_checkout_upgrade.go, init_plugin_stage.go, init.go, retired.go, skillwarn.go, doctor.go,
doctor_render.go, image_refresh.go}`, `internal/pluginwire/status.go`, `internal/claudeplugin/{claudeplugin.go,
plan.go}`, `internal/initflow/{stage.go}`, `internal/runner/reviewmode.go`, `internal/agent/{agent_test.go,
settings.go}`, `plugin/skills/{upgrade/SKILL.md,upgrade/reference/states.md,setup/SKILL.md,working/SKILL.md}`,
`plugin/{plugin_test.go,skills_lint_test.go,readme_test.go,upgrade_skill_test.go,skillcli_test.go}`,
`internal/cli/{docs_upgrade_test.go,docs_upgrade_local_test.go,upgrade_skill_doc_test.go,
docs_release_notes_test.go,docs_project_centric_test.go,docs_project_named_oauth_tokens_test.go}`,
`docs/design/upgrade.md`, `docs/backlog/README.md`, and the numbers blocks of `docs/rigor-command/`,
`docs/models-commands/` and the three open pull requests' design directories.

**Counted, not read line by line:** the rest of §4's census. Each task re-runs the greps rather than
trusting these counts, and a task that finds a cited symbol gone **stops and reports** rather than
inventing a replacement — the rule `docs/plans/2026-10-08-upgrade.md` set for its own tasks.

## 7. What the merged tree changed, and this round's answer (2026-11-01)

The four documents above were written against `3d6ed04d`. The branch was then merged with `origin/main`,
and two commits on it touch this work's own subject matter, so every citation and both counts were
re-read on **`28425eba`** rather than trusted. What the re-read found:

| This file said | On `28425eba` | Where it is fixed |
| --- | --- | --- |
| 120 files / 507 occurrences, and one sentence counting lines where the rest counted occurrences | **119 / 531**, and the two methods named separately | §4 |
| `pluginwire.Fix` "stops naming `fugaro upgrade --local`" — as a task | **every state whose fix names a command already names `fugaro update`** (`status.go:49`, `:55`, `:57`; `Foreign` names none); the one line left naming `fugaro upgrade` is `Newer`'s (`:51`), which goes with the command (#1495, a commit on this branch's line of work) | [spec.md](spec.md) §6 |
| `pluginFirstRun` / `pluginRefresh` are U1's sentences to rewrite | both already say **"Run fugaro update …"** (`init_plugin_stage.go:32`, `:41`) | [spec.md](spec.md) §6 |
| `doctor_render.go:583`, `:690`, `doctor_render_test.go:722` | the class comment is `:75-117` (its "not here yet" note at `:88-91`), `doctorUpdatePrefixes` `:117`, `plugin-install` in `doctorMeasureIDs` `:203`, `doctorUpdateFixes` `:644` (`cli-version` `:653`, `plugin-pin` `:656`, `plugin-install` `:767-768`, `policy-newer-than-binary` `:781`), the pinned fix string `doctor_render_test.go:729` | [spec.md](spec.md) §6 |
| `LocalUnderAgent()` at `init_checkout_upgrade.go:53`, the interface at `stage.go:337` | `:63` and `:349` | F1, [spec.md](spec.md) §2 |

**What did not change, checked rather than assumed:** the stage order (`init_stages.go:169`, `:182`),
`checkoutSteps` (`init_checkout_upgrade.go:39`), `converge`'s stop ("nothing after it runs",
`initflow/loop.go:329`), `newMachineGuard`'s exemption by stage name (`init_new_machine.go:95`) and
`cloudNotHere`'s sentence (`upgrade_cloud.go:80`). Those are what §11's answer rests on, so a later task
re-reads them too before it cites them.

**This round's contribution**, in one line each: **F23–F25** (§2) are the teammate who is not the owner —
the stop before the plugin, the pin's gate on a machine install, and §8's own "the run continues" that
the code does not do; **D954 and D955** (§3) are its two answers; and **U8**
([implementation-plan.md](implementation-plan.md)) is the task, which the plan now puts **before U4**,
because deleting `fugaro upgrade --local` before the one command reaches the plugin would leave the
teammate with a sentence and no command ([spec.md](spec.md) §11, last paragraph).

## 8. What main has overtaken, and this round's answers (2026-11-04)

Every claim above was re-read on **`b41709ea`**, the `origin/main` this branch carries, and the line
numbers in this file are that commit's. §7's line numbers are **2026-11-01's** and this section's
supersede them where the two disagree; the F-table and §4 above were edited in place. This is the most
overtaken of the six designs in the round, so the list is long.

| What changed on `main` | Where it is | What it does to this design |
| --- | --- | --- |
| **Managed mode landed**, and it is a real gate with real callers | `internal/managed/{converge.go,check.go}` (`Converge`/`Refuse`, `CheckedInventory`), `internal/cli/managed_gate.go` (`readManagedGate`, `admit`, `recorded`), `initflow.Managed` (`internal/initflow/stage.go:518`) and `CanConfirmManaged` (`:580`) | `initflow.Managed`'s own comment names **the plugin stage** as a caller that knows nothing of managed projects and keeps the refusal, which is exactly right today and stops being right with D954: a stage `converge` now runs *after* a cloud stage stopped is a stage that writes in a managed project's agent session, so **U8's marker has to carry the gate's answer** the way `initflow.Managed` already does — the stage reads `readManagedGate` before it writes and refuses on the zero value. This is the one place where D954 adds work the plan did not have |
| **`fugaro update` is in the gate's set by design but not yet under it** | the approved `docs/managed-agent-converge/`: `fugaro update` is in the gated set (§3.1, §4.2) and its M1 task ("`fugaro update`'s stages under the gate", **SEC**) is not on `main` — `readManagedGate` has no caller in `internal/cli/init.go` or `update.go` | U8's gate read is written against the gate as it exists (`managed_gate.go`), not against M1; **the task re-reads this before it writes it**, because if M1 has landed by then the call belongs in the stage's own `Apply` and not in a second place |
| **The gate already covers `update`'s neighbours** | `settings_write.go:121` and `accounts_add.go:218` call the gate; M2 (`config publish`), M3 (`project policy publish`), M4 (`image refresh`), M6 (audit), M7 (doctor), M8, M11 landed | the plugin step must not become the one writing step `update` performs in an agent's session without the gate; and the sentence doctor prints for it is written by the same work |
| **The project image catalog is on `main`**, and the **repository-image removal design is #1676** | `internal/cli/{image_catalog.go,image_build_catalog.go,image_check_catalog.go,catalog_copy.go}`; #1676 holds D1190–D1219 | F23's images stage — the cloud stage whose stop hides the plugin — is now the busiest stage in `update`: the case for D954's marker is stronger than when F23 was written, and the plan's slot is no longer free for other reasons (#1676 and 0.18.0) |
| **`settings`, `rigor` and `accounts` are commands now**, and `update` grew a **`Layer`** stage | `internal/cli/{settings.go,settings_set.go,settings_read.go,settings_render.go,rigor*.go,accounts*.go}`, `init_layer_stage.go:53`, appended at `init_stages.go:198`, `Checkout` now needs `Repository` **and** `Layer` (`initflow/stage.go:175`) | F2's stage order was rewritten for it (the `Layer` position), and the checkout stage this design reshapes now runs after one more stage; the settings work is also what makes `fugaro update` a gated command, so it is the reason the row above exists |
| **`fugaro upgrade` is still on `main`**, unchanged | `internal/cli/upgrade.go` and `main`'s census of it — 115 files, 488 occurrences (§4) | D950's "nothing, not even a tombstone" is still a task and not a description of the tree |
| **Person-class reads landed**: GitHub in #1646/#1669 and Bitbucket in BB4 | `internal/credsvc`'s person-class reads | nothing here rests on them: F24's `authState`/`enrolling()` gate reads the machine's own config, not a credential, and no claim in this file was about who can read a credential |
| **`stray.go` (#1476) is on `main`** | `internal/runner/stray.go` — finalize refuses to commit a file that is not text | F10's read holds unchanged: this is runner-side handling of a cloud run's checkout, it does not name the command, and the plugin's file is still written only by the machine-side stage |
| **0.15.0, 0.16.0 and 0.17.0 have shipped**; `docs/releases/` ends at `v0.17.0.md` | `docs/releases/` | every slot this design named (0.15.0, and the merge rule that waited for the 0.14.2 tag) is spent. 0.18.0 is claimed by the config-management, managed-agent-converge and project-image-catalog work, so **this design's slot is 0.19.0** |
| **#1500 re-pointed doctor's remedies at `fugaro update`** | `doctor_render.go`'s class comment, `doctorUpdateFixes`, and `plugin-install`'s own fix line all say `fugaro update` today; `internal/cli/doctor.go` and `doctor_render.go` **no longer name `fugaro upgrade` at all** (§4a) | spec §6 and plan U3 were written against a line that said `fugaro upgrade --local`; what is left of that work is the **class move** (`plugin-install` from `doctorMeasureIDs` into `doctorUpdatePrefixes`) and the `not installed` severity — the fix string is done |

**The four answers this round records (2026-11-04)**, in the owner's words:

| D | The answer | One line why |
| --- | --- | --- |
| **D943** | `fugaro update` shows the diff of an old pin and offers its removal; **it never commits**, and in an agent's session it prints the diff and writes nothing — **as recommended** | the removal is a write in the user's own checkout, and a second command to do it is what the HARD RULE forbids |
| **D949** | **DELETE the pin merge entirely — NOT THE RECOMMENDATION** | the owner took the alternative: a repository can no longer say "the skills are required" (F22's cost, accepted), `fugaro update-skills --pin` never exists, and the pin's read side survives only for U5's migration window (F26) |
| **D950** | **Nothing — not even a tombstone** — as recommended | a tombstone's only job is to turn a converging run into exit 1 for the one user the HARD RULE is about |
| **D954** | **A machine-local stage marker** — as recommended | the shape the tree already keeps by hand three times; the alternative would end a teammate's run before the images, secrets and repository work and redraw a stage order every reader sees |

**Not answered this round:** D940, D941, D942, D944–D948, D951–D953 and D955 keep their recommendations
and their costs, exactly as written above. This file does not invent an answer for them, and the tasks
that follow the plan read them as recommendations rather than as settled.
