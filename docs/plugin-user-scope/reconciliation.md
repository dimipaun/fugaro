# Reconciliation: what the tree holds, and what this design would change

Every claim in this file was read on `origin/main` at **`3d6ed04d`** (2026-11-01) with `git grep` and by
opening the files. Line numbers are that commit's. **Where this file disagrees with
[spec.md](spec.md), this file wins.** The census in §4 is the one thing here that a task must re-run
rather than trust: a line number ages, and a count ages faster.

## 1. What exists (F1–F10)

| # | The claim | Where it holds |
| --- | --- | --- |
| F1 | `fugaro update` already converges the checkout it stands in | `internal/cli/init_checkout_upgrade.go:39` — `checkoutSteps = {pin, plugin, cloud}` with `LocalUnderAgent()` at `:53`; the stage is declared after the repository stage (`internal/initflow/stage.go`'s stage list, `checkout` needs `repository`) |
| F2 | One converge engine, two doors | `internal/cli/init.go`'s `newInitCmdWith` builds `setup` and `update` from the same `internal/initflow` stages; the stage order is `Preflight, Project, Services, Installation, Firebase, Images, Installation2, Secrets, Plugin, Credentials, Repository, Rules, Policy, Checkout, Compute` |
| F3 | The plugin merge lives in the **plugin** stage, which runs **before** the repository stage | `internal/cli/init_plugin_stage.go`; the stage list above puts `Plugin` at 8 and `Repository` at 11 |
| F4 | `pluginwire` is narrow and refuses to be clever | `internal/pluginwire` merges only `extraKnownMarketplaces.fugaro` and `enabledPlugins["fugaro@fugaro"]`, needs a strict `vX.Y.Z`, never rewrites a file it cannot parse, never moves a pin down, and never commits |
| F5 | **The `claude` install is already at user scope** | `internal/claudeplugin/claudeplugin.go:43` — `installUser = claude plugin install fugaro@fugaro --scope user`; `addPrefix` adds the marketplace at the same scope; the argv allowlist (`Allowed`), the empty working directory, the scrubbed child env (which drops `CLAUDECODE`) and the timeouts are all `claudeplugin`'s |
| F6 | `Decide` maintains **every applying scope**, not just user | `internal/claudeplugin/plan.go:80`'s `stale` loop over `scopes` (`claudeplugin.go:46`, `local`, `project`, `user`); `Applying` at `:41`, `Effective` at `:62` |
| F7 | doctor has the classes this design needs | `internal/cli/doctor.go:47`'s `doctorCheck{ID, OK, Problem, Fix, Severity, Class}`, classes `actionable|update|decision|measure|audit`; `doctorUpdatePrefixes` and `doctorMeasureIDs` in `internal/cli/doctor_render.go`; `plugin-install` sits in `doctorMeasureIDs` **and not** in `doctorUpdatePrefixes`, under a comment that says why |
| F8 | The `plugin/` tests check every command and flag a skill names against the CLI | `plugin/skillcli_test.go`'s `resolveSkillCommand`, `plugin/skills_lint_test.go`, `plugin/plugin_test.go` |
| F9 | Docs are tested, **and a new `docs/` directory needs no pin test** | the many `internal/cli/docs_*_test.go` files pin sentences, counts and citations in the files they name; there is no registry test that walks `docs/` as a set, so `docs/plugin-user-scope/` needs no entry anywhere. `docs_live_init_census_test.go` walks `docs` for the retired `init` command's spelling, asserts the set of files naming it is **empty**, and is the one test in the tree a new document under `docs/` can break by accident — which is why the four files here spell it as `` `init` `` and never with the command's word in front |
| F10 | A cloud run never loads a plugin, from the code | `internal/agent/agent_test.go:892` (`TestClaudeArgsNeverSideloadPlugins`) forbids every plugin, marketplace, trust and settings-source flag in the headless launch; `internal/agent/settings.go:183` is where settings files are *read*, and review mode strips `.claude` and `.claude-plugin` from every reviewed checkout (`internal/runner/reviewmode.go:253`, `stripClaudeConfig`) |

**F5 is the fact that reshapes the brief's own wording.** The plugin was never *installed* at project
scope: the `claude` call is `--scope user` today. What is at project scope is the **enablement** in the
committed `.claude/settings.json`. So this work moves the plugin's *reach*, not a `--scope` argument, and
any task that looks for a scope to change will not find one.

## 2. What is missing, what conflicts, and what the move costs (F11–F22)

| # | The finding | Kind |
| --- | --- | --- |
| F11 | **The pin is the delivery mechanism.** A repository that commits no `.claude/settings.json` gives its teammates no skills, and `plugin/skills/setup/SKILL.md:154` tells the agent to put that file's change into the pull request — so the committed file is the intended outcome, not a side effect | the thing this design undoes (D940) |
| F12 | **A subdirectory of a checkout works, with one residue.** `pluginwire.Locate` walks up from `.` with `filepath.Abs` and stops at `$HOME`, so a command in `repo/sub/dir` finds `repo/.claude/settings.json`. The residue: `Locate` returns the path as typed while `checkoutKey` (`internal/cli/upgrade.go:284`) resolves symlinks, and with `fugaro upgrade`'s multi-`PATH` dedup gone the two can disagree about whether two invocations are the same checkout | gap — closed by U3 |
| F13 | **A repository that is not enrolled is the regression the naive move would cause.** The plugin step is in the **checkout** stage, and the checkout stage is declared *after* the repository stage: a session whose repository stage returns needs-you — not onboarded, no GitHub App, a run in flight — never reaches the plugin. The **plugin** stage (F3) runs before it | conflict — decides U2 |
| F14 | **A linked worktree already works.** `.git` is a file there, and `Locate`'s `os.Lstat` walk handles it, so it returns the worktree root. The pin was what made two worktrees of one repository diverge (one committed the file, one had it at another ref); dropping the default pin closes it | gap — closed by D940, nothing to build |
| F15 | **A checkout of another project is already skipped.** `checkoutStage.namesAnotherProject` (`internal/cli/init_checkout_upgrade.go`) skips the step in a checkout whose `fugaro.yaml` names a different project; only its wording ("its plugin pin and its job images") changes | gap — closed by U2's wording |
| F16 | **`fugaro update` has no `--check` and no `--local`** today | `internal/cli/init.go`'s flag list |
| F17 | **`update --plan-only` is not offline.** A stage's `Plan` may read the cloud (the images stage reads the bucket and the registry), so it is not the "writes nothing, calls nothing" mode ruling C needs | conflict — decides D945 |
| F18 | **`fugaro upgrade` is a second front door**: five steps, a `PATH` taken several times, an exit code that is the worst of the five, and a skill to explain it. See §4 for the census | the thing ruling A retires |
| F19 | **Nothing removes a pin today.** The pin step only ever writes or moves the pin up; there is no code path that takes the two entries out of `.claude/settings.json` | gap — decides D943, built by U4 |
| F20 | **These tests will go red, and each is named by the task that owns it** (the list is in §5) | the gate |
| F21 | **A repository that never opted in fails `fugaro doctor --plugin --strict` today.** `pluginwire.Severity()` gives `info` only to `NotInstalled` and `CannotCompare`; `not wired` is a `warning`, and `--strict` is the mode a repository's CI runs (doctor's own help calls `--plugin` "offline, no credentials, safe for CI") | conflict — decides D942 |
| F22 | **What the pin pays for, which the user-scope install does not**: a teammate who has run nothing still gets the skills, and a repository can say "required". Stated as a trade-off in [spec.md](spec.md) §10 rather than designed away | cost — D949 |

## 3. Decisions (D940–D953)

Fourteen, none answered: the owner decides. Each recommendation is first, with the alternative's cost.

| D | Question | Recommendation | The alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| **D940** | Where do the skills come from by default? | **The machine, at user scope**; the repository pin is opt-in and never the default (spec §3). The `claude` install is already `--scope user` (F5), so this moves *enablement* out of the committed file | Keep the pin as the default: a teammate still gets the skills with no command — at the cost of a third-party `settings.json` in every enrolled repository's trust surface, two answers for two checkouts of one repository, and the very step ruling A removes | |
| **D941** | Which stage does the plugin step live in, and under what marker? | **The `plugin` stage keeps its position** (it runs before `Repository`, F3) and becomes `LocalUnderAgent`; the checkout stage keeps the `cloud` step only and loses `pin`/`plugin` | Leave it in the checkout stage: a session whose repository stage returns needs-you never reaches the plugin (F13), which is the one gap this work could ship as a bug | |
| **D942** | What does doctor compare, and in which class? | `plugin-install` compares the **user-scope** install with **this CLI** (against the pin where one is opted in), becomes a `warning` when nothing is installed, and **moves into `doctorUpdatePrefixes`**; `plugin-pin` keeps its ID and meanings and `not wired` becomes `info`, so `doctor --plugin --strict` stops failing a repository that never opted in (F21) | Leave the classes as they are: every enrolled repository's CI fails on a check whose fix is a command the repository cannot run, and the update class keeps a fix line (`fugaro upgrade --local`) for a command that no longer exists | |
| **D943** | What happens to a repository carrying an old pin? | `fugaro update` **shows the diff and offers its removal**, never commits, and in an agent's session prints the diff and writes nothing (spec §8) | Leave the pin for the user to delete by hand: that is a second step the HARD RULE forbids, and it leaves the checkout declaring a marketplace nobody maintains | |
| **D944** | What does the setup skill write, and what still gates the release? | The wizard's plugin stage writes **no** `.claude/settings.json` by default; `--pin` keeps today's behaviour; every `plugin/` test that checks a skill against the CLI **keeps gating**, re-pointed at `update` (spec §9) | Drop the pinning behaviour from `setup` entirely, `--pin` with it: a repository could then never say "the skills are required", which F22 names as the pin's one remaining job | |
| **D945** | What is the session-start check? | **`fugaro doctor --offline --json`**, a new `--offline` flag on `doctor`: every check whose answer is a local file, and a recorded skip for the rest (spec §7) | `fugaro update --check --offline --json`: a second JSON document, a second version and a second set of IDs beside doctor's (already in `console/docs/json-contract.md` §6.4) — and it answers "not a Fugaro checkout" in the repositories `working` is most often read in | |
| **D946** | What is the check's output contract? | doctor's document keeps its shape and its version; `--offline` adds **one additive optional field** on `doctorCheck` (`skipped`), and a skipped check is `ok: false` with a `problem` saying why (spec §7) | A new versioned document: the console, the mobile app and every scripted `doctor --json` reader would need a second parser for facts they already read | |
| **D947** | What exactly does the skill say? | The sentence in spec §7, whose second half is `plugin/skills/upgrade/SKILL.md:27`'s own words already pinned by `plugin/upgrade_skill_test.go:18,20` | Write it fresh: the two pinned strings stop matching where they are pinned, and the `plugin/` gate covers less of the sentence that does the work | |
| **D948** | Which of `upgrade`'s flags survive on `update`? | **`--check` survives**, added to `update`; **`--local` does not** — the `LocalUnderAgent` marker absorbs it, and `fugaro update-skills` is the plugin-only command; the cloud flags are already `update`'s (spec §5) | Keep `--local`: a second spelling of "the local half" beside a marker that already says it, and a flag whose only caller is gone | |
| **D949** | Does the pin survive at all? | **Yes, as `fugaro update-skills --pin`** — one job: a team that wants the skills required (F22). It is not a scope, it does not change the install, and the marketplace ref follows the default branch | Delete the merge outright: a repository that wants the skills required has no way to say so, and `pluginwire`, its states and its tests become dead code in one commit | |
| **D950** | What is left of `fugaro upgrade`? | **Nothing — not even a tombstone.** The name becomes cobra's unknown command | A tombstone (the `internal/cli/retired.go` shape): one hidden command, one message, one test and one census exemption, to turn a converging run into exit 1 for the user the HARD RULE is about — and `fugaro update` is what they wanted anyway | |
| **D951** | What can the offline check answer? | The table in spec §7: the plugin install, the pin, the base image and the local image config **yes**; the deployed rules generation, the published policy node and the CLI's own release **no, and said to be skipped** | Pretend to answer them: a guessed "ok" on a credential-only fact is the silent pass `doctor`'s `unread` state exists to prevent | |
| **D952** | Does the session-start check reach the network? | **No.** Detecting a newer release stays opt-in (`doctor`'s own `cli-version` check in the full run, `internal/cli/doctor.go:172`) and the skills never trigger it | Check the network at session start: an agent that reaches out on the user's behalf, unasked, at the start of every session — and a check that fails offline, which is the state a nudge must survive | |
| **D953** | What becomes of the `upgrade` skill? | **The `update` skill**: same job (check, explain, hand over the one command), `plugin/skills/upgrade/` renamed to `plugin/skills/update/`, its body rewritten for two commands, `plugin/plugin_test.go:134`'s list swapped one for one | Drop the skill: the one place a user's agent is told how to read the converge's verdict, and its `plugin/` coverage, go with it | |

**Why these numbers.** The block was read by hand on 2026-11-01 (README's "Numbers this work holds"):
`main`'s highest written number is **D900**, three open pull requests claim D901–D919 (#1481), D901–D920
(#1485) and D920–D934 + reserved D935–D939 (#1487), so the first free number is **D940**. D940–D947 are
the design's own decisions, D948–D953 the rulings' smaller ones; D954–D959 are reserved and unused. A
later task takes D960 and re-checks rather than trusting this paragraph.

## 4. The census: everything that names `fugaro upgrade`

```
$ git grep -l 'fugaro upgrade' -- .    # 120 files
$ git grep -o 'fugaro upgrade' -- . | wc -l    # 507 occurrences
```

By area: `docs/` 54, `internal/` 53, `plugin/` 5, `website/` 4, `console/` 1, plus `README.md`,
`CLAUDE.md` and `.claude/skills/new-release/SKILL.md`.

The bare word `upgrade` is a much larger and mostly irrelevant set (**259 files, 1377 occurrences**),
because it is also `brew upgrade`, `image_refresh.go`'s own name, a policy *version* upgrade and ordinary
prose. **The command string is the census**; a task re-runs the two greps above and works from its own
output, not from this list.

### 4a. Three classes, and only one of them changes

**Class 1 — living text that must change in the task that changes its subject** (the implementation
plan's own file lists name these per task):

- **The command and its stage:** `internal/cli/upgrade.go`, `internal/cli/upgrade_plugin.go`,
  `internal/cli/init_checkout_upgrade.go`, `internal/cli/init_plugin_stage.go`,
  `internal/cli/updateskills.go`, `internal/cli/doctor.go`, `internal/cli/doctor_render.go`,
  `internal/cli/image_refresh.go`, `internal/cli/init_policy_migrate.go`, `internal/cli/init_rules_stage.go`,
  `internal/cli/init_stages.go`, `internal/cli/skillwarn.go`, `internal/cli/run.go`,
  `internal/cli/launch_budget.go`, `internal/cli/project.go`, `internal/cli/project_cmd.go`,
  `internal/cli/init_review.go` — most of these are `Long` text, a fix string or a doc comment.
- **The plugin's own packages:** `internal/claudeplugin/{claudeplugin.go,plan.go}`,
  `internal/pluginwire/status.go` (its `Fix()` strings), `internal/projectpolicy/version.go`,
  `internal/infra/{spec.go,firebasedb.go}`, `internal/runner/projectpolicy.go`.
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

**Class 2 — dated records that must NOT change.** `docs/releases/` (19 files: v0.6.0 … v0.14.1) and the
dated plans under `docs/plans/` (`2026-10-08-upgrade.md` alone carries **126** of the 507). A release note
is what shipped, and rewriting one to name a command that did not exist then is a false record. This is
already the tree's own rule: `internal/cli/docs_release_notes_test.go` calls them "a dated record that
keeps its own spelling" and translates the old spelling instead (see §5). **The tasks do not touch them.**

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
| `plugin/plugin_test.go:134`, `plugin/readme_test.go:45`, `plugin/skills_lint_test.go:583,746,1760-1765`, `plugin/upgrade_skill_test.go` | the skill list, the plugin README's phrases, `userRunsAllowed`, `upgradeYesOnly`, the skill's own sentences | U5 |
| `internal/cli/{upgrade_test.go,upgrade_hints_test.go,upgrade_plugin_test.go,upgrade_cloud_test.go,upgrade_cloud_step_test.go,image_refresh_upgrade_test.go,init_checkout_upgrade_test.go,init_plugin_stage_test.go,init_policy_migrate_test.go,checkouts_test.go,cascade_test.go,plugin_notices_test.go,run_allowlist_test.go,left_commands_test.go,init_test.go,init_firebase_test.go,doctor_render_test.go,doctor_policy_test.go,updateskills_test.go,project.go's own}` | the command's own tests: they move with it, or go with it | U1–U3 |
| `internal/{claudeplugin,pluginwire,projectpolicy,infra,runner}/*_test.go` | `Fix()` strings, the scope loop, `projectpolicy`'s version-upgrade prose, and `internal/runner/sealskew_test.go`'s sentence about which command a skew tells the user to run — mechanical, but each names the command | U1–U4 |

**No test in the tree reads `docs/plugin-user-scope/`.** F9: the docs tests name the files they pin, and
nothing walks `docs/` as a set, so this design directory needs no pin test and this pull request adds
none.

## 6. What was read, and what was only counted

**Read, at `3d6ed04d`:** `internal/cli/{upgrade.go, upgrade_plugin.go, updateskills.go,
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
