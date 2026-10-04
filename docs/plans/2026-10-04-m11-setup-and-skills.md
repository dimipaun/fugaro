# M11: Simple Setup and Fugaro-Owned Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax. Briefs to implementers stay short: the task text plus the Review Focus line that applies.

**Goal:** A new team sets Fugaro up with two things: `fugaro init` (one re-runnable, idempotent command that converges the whole installation, mirrors the images, takes the first-person secrets at hidden prompts and wires the Claude Code plugin into the repository's project settings) and `/fugaro:setup` in the coding agent (which writes `fugaro.yaml` and, when needed, a Dockerfile, with the human's decisions). The skills are Fugaro-owned and live only in the plugin (`plugin/skills`); `init` merges the Fugaro marketplace (pinned to the release tag of the binary that ran it) and `enabledPlugins` into `.claude/settings.json` so every teammate is prompted to install it, and `fugaro doctor` and a one-line warning compare the pin with the binary. No secret value ever passes through an agent, and no test touches the cloud.

**Amended 2026-10-04 (user ruling): the plugin is kept, not retired.** The embedded-skills tasks (embedding, stamps and hashes, a six-state installer, `.claude/skills/fugaro-*` files) are dropped; four verification spikes (V1 to V4) come first.

**Status 2026-10-04 (T20).** Merged: V3's code half (#85), T1 (#86), T17 (#87), T5 (#88), T11 (#90), T8 (#91), T4 and T9 (#92), T6 (#93), T14 (#97, minors #99), T2 (#98), T16 (#102), T10 (#101), T15 and T13 (#104), T19 and T7 (#105), T12 (#106), T20 (#107); the final-fixes wave follows ([design §15](../design/m11-setup-and-skills.md)). **Not merged:** T18 (optional, not built), F1 (signing, not built), and every user-run item: V1, V2, V4, V3's container check, and T21 (written up as Check 27 in [gcp-live-checklist.md](../gcp-live-checklist.md), **not run**). Where the code differs from the text below, [the design](../design/m11-setup-and-skills.md) marks "Decided differently" in the section concerned.

**Spec:** [m11-setup-and-skills.md](../design/m11-setup-and-skills.md), the user's brief [setup-and-skills-spec-source.md](../design/setup-and-skills-spec-source.md), [v1.md](../design/v1.md) §5.2, §7.2, §8, §9.

## What is built and what this reuses

| Need | Already there |
|---|---|
| Installation, Firebase backend, repository apply | `internal/cli/init.go` (`install`, `initFirebase`, `runInitRepo`), `init_firebase.go`, `internal/infra`, each with plan, confirmation, `--plan-only`, `--print-vars`, `--config-only`, `--forget` |
| Local config and base images | `internal/localcfg` (`base_images`, `ParseBaseImageFlag`), `internal/image` (published base name by version) |
| Secrets | `internal/cli/secrets.go` (hidden prompt, `secrets ls`, labelled create), `backend/gcp.Secrets`, pty tests |
| Image build and contract | `fugaro image render|build --local`, `fugaro validate`, `internal/imagecheck`, `images/` |
| Skill checks | `internal/cli/skills_test.go` (`TestSkillCommandsExist`), `plugin/plugin_test.go` |
| Plugin and marketplace | `plugin/` (six skills, `plugin/.claude-plugin/plugin.json`), `.claude-plugin/marketplace.json` (relative path `./plugin`, no version), `scripts/release-gate.sh`, `scripts/bump-plugin-version.sh` (`--check`), `docs/release.md` |
| Runner launch of the agent | `internal/agent/agent.go` (`Args`, scrubbed `Env`), `internal/agent/fakeclaude` |
| Fakes | `internal/gcpfake`, the fake `claude`, `internal/gateway/anthropicfake`, `-tags terraform` tests |
| Onboarding content | `plugin/skills/onboard/SKILL.md` and the five operational plugin skills |
| Release images | `.github/workflows/images.yml`, `.goreleaser.yaml` |

**Gaps:** init is three commands in a manual order; no image mirror (ghcr is unreadable by Cloud Build and Run) and no published history image; secrets are not part of init; the plugin is not wired into any project and has no pin or staleness check; its skills are six per-command ones, not the four of the brief; no `doctor`; no project creation.

## Decisions already made (binding)

User rulings from earlier milestones: init has so far never created a project or linked billing (M11 adds it only behind its own flags, slice C); no backward compatibility until release (rename and reshape directly); never use or ask for an API key; secret values only from the user's terminal; live tests are sandbox-only and run by the user; the gcloud default project is never relied on; oauth (`claude setup-token`) is the Claude credential the user wants supported end to end. Recommended in the design (§0): init is a converge loop wrapping the existing steps; the base images are mirrored by init as the user; the skills stay in the plugin (single source) and init wires it through project settings, pinned to the release tag (**user ruling 2026-10-04: keep the plugin**); `image:` stays the default, a repository Dockerfile the escape hatch; repository onboarding is `fugaro init` run again after the setup PR merges.

## Review Focus

**Per-task review (security- or money-critical): T2, T3, T8, T9, T10, T14, T16.** Everything else is reviewed once on the branch at the end (user's token-economy rule).

1. **Init creating cloud resources unseen, or twice, or in the wrong project.** T8/T16: `TestNoApplyWithoutConfirmation`, `TestYesNeverCreatesProject`, `TestYesNeverLinksBilling`, `TestNeverUsesGcloudDefaultProject`, `TestRerunPlansNothing`, `TestStageFailureStopsLoop`, `TestPlanOnlyChangesNothing`, `TestProjectAndBillingInConfirmationText`.
2. **A secret value reaching an agent, an output, a log, a config.** T14: `TestSecretPromptHidden`, `TestNonTTYNeverReadForValues`, `TestSecretNeverInOutputOrJSON`, `TestExistingSecretNotOverwritten`, `TestPrintedCommandsHaveNoValues`.
3. **A tampered or wrong base image, or a base registry writable by a build account.** T2: `TestMirrorVerifiesDigest`, `TestMirrorRefusesUnreadableSource`, `TestMirrorIdempotent`, `TestDevBuildHasNoSilentFallback`, `TestMirrorRunsAsUserNotBuildAccount`.
4. **A plugin or a settings change that runs or points at code the user did not expect.** T3, T4: `TestMergeKeepsEveryOtherKey`, `TestMergeNeverRewritesInvalidJSON`, `TestForeignMarketplaceKeptNotRewritten`, `TestPluginHasSkillsOnly` (no hooks, MCP servers, commands, agents, `bin/`), `TestDoctorStrictFailsForeignAndUnpinned`; and V3 (a headless run does not load it).
5. **Skill text that tells an agent to do what it must not.** T9/T10: the forbidden-instruction lint (T4) green and a read of every skill by the reviewer against design §9: no apply, no `--yes`, no secret handling, no credential files, no widening of trust, repository content treated as data, executed lines shown to the user.

## File Structure

| Path | Role |
|---|---|
| `plugin/skills/{setup,working,routing,parallelism}/**` | the skills, single source (merged from the six; header and version in each) |
| `plugin/skills_lint_test.go`, `internal/cli/skills_test.go` | lint tests (T4) |
| `internal/pluginwire/` (new) | the `.claude/settings.json` merge, the pin, the staleness states, reading the installed plugin version |
| `internal/cli/updateskills.go`, `skillwarn.go`, `doctor.go` (new), `root.go` | `update-skills`, the staleness warning, `doctor` (`--plugin --strict`) |
| `scripts/bump-plugin-version.sh` | also rewrites and checks the version in each skill header |
| `internal/preflight/` (new) | the read-only environment checks shared by init and doctor |
| `internal/initflow/` (new) | the stage interface, the converge loop, the plan view, `left_for_you` |
| `internal/cli/init*.go` | existing step functions adapted to stages, no behaviour change; new inputs and defaults |
| `internal/mirror/` (new) | registry copy, digest check, idempotency |
| `internal/cli/initsecrets.go` (new) | the secrets stage |
| `internal/infra`, `internal/gcpfake` | create-project, Firebase add, billing (slice C); fakes |
| `internal/backend/backendtest/` (new), `docs/backends.md` | conformance suite and contributor doc |
| `.github/workflows/images.yml`, `.goreleaser.yaml` | publish `fugaro-history`, public packages, anonymous-pull check |
| `internal/agent/agent.go` (+ test) | V3: the launch contract, and the disable if the container needs it |
| `README.md`, `docs/*.md`, `docs/design/v1.md`, `docs/release.md` | Getting started, manual setup, doc sync, the pin and header agree with the tag |

## Task dependency graph and lanes

| Task | Size | Lane | Depends on | Review |
|---|---|---|---|---|
| **V1** verify the settings keys and the install prompt at folder trust | S | V | none | n/a |
| **V2** verify pinning to a tag, and how an existing install moves | S | V | none | n/a |
| **V3** verify a headless cloud run does not fetch or load the plugin; pin the runner's launch contract | S | V | none | end |
| **V4** verify first use in a fresh checkout after trust | S | V | none | n/a |
| T1 skills restructure: setup, working, routing, parallelism in `plugin/skills`; header and version in each; bump script and its check | M | A | none | end |
| T3 `internal/pluginwire`: settings merge, tag pin, states; `update-skills` and the staleness warning | M | A | V1, V2, V4 (for the fallback and the message) | own |
| T4 skill lint tests: header and version, commands and flags exist, yaml examples validate, JSON fields, forbidden instructions, size, plugin has skills only | M | A | T1 | end |
| T9 skill content: `working`, `routing`, `parallelism` **(critical)** | M | A | T1, T4 | own |
| T10 skill content: `setup` **(critical)** | L | A | T1, T4, T9 | own |
| T19 skills end-to-end fixtures per base kind | S | A | T10 | end |
| T7 retire the six per-command skills (not the plugin); README Getting started | S | A | T3, T10 | end |
| T5 `internal/preflight`: shared checks incl. Cloud Billing API and default-SA editor | M | I | none | end |
| T6 `fugaro doctor`, with `--plugin --strict` | M | I | T3, T5 | end |
| T11 release: publish `fugaro-history`, public packages, anonymous-pull check | S | R | none | end |
| T8 `internal/initflow`: stage interface, converge loop, plan view, `--non-interactive`, `--json` **(critical)** | L | I | T5 | own |
| T2 `internal/mirror` and the images stage **(critical)** | L | I | T8, T11 | own |
| T13 inputs and defaults, GitHub App ID asked once | S | I | T8 | end |
| T14 secrets stage **(critical)** | M | I | T8 | own |
| T15 repository stage, plugin-wiring stage and adopt mode | M | I | T8, T2, T14, T3 | end |
| T12 output hygiene: one-line commands everywhere, no TTY message | S | I | T8, T14, T15 | end |
| T16 create-project, Firebase add, billing linkage **(slice C, critical)** | L | P | T8, T5 | own |
| T17 backend name, `backendtest`, `docs/backends.md` (slice D) | M | K | none | end |
| T18 `fugaro image build --ref BRANCH`, non-promoting (slice D, optional) | M | K | T15 | end |
| T20 docs: friction fixes, design sync, dogfooding page, release.md | S | D | T10, T15 | end |
| T21 live check, user-run on a sandbox project | M | user | T7, T12, T15 (T16 for its part) | n/a |

Order: **the V spikes first** (V1, V2, V4 are user-run and short; the agent-run halves of V1 to V3 are already recorded below, so what remains is the user-run part), then lane A (T1 and T4 do not wait for V; T3 waits for V1, V2 and V4 or applies the stated fallback; T9, T10, T19, T7) and lane I (T5, T8, then T2, T13, T14, T15, T6, T12) run in parallel and are independent. T11 early (T2 needs it). Slice A is shippable when V1 to V4 are recorded (or their fallbacks applied), lane A and T6, T20's doc parts and T21's skills items are done; slice B when lane I and T11 are; T16 and the slice D tasks follow.

### Verification spikes V1 to V4 (S, lane V): do these first, mark each checked or unverified, never guess

The wiring rests on four facts about Claude Code. Each is recorded here with its status; a failed check triggers its fallback, not a silent workaround. Version tested: Claude Code 2.1.289 (the runner images pin 2.1.283: note any difference).

**V1: the settings keys and the install prompt at folder trust.**
- [x] **Agent-run, 2026-10-04 (CLI, isolated `CLAUDE_CONFIG_DIR`, no cloud):** `claude plugin marketplace add dimipaun/fugaro#main --scope project` then `claude plugin install fugaro@fugaro --scope project` wrote `.claude/settings.json` with `extraKnownMarketplaces.fugaro.source = {source: github, repo: dimipaun/fugaro, ref}` and `enabledPlugins["fugaro@fugaro"] = true`; `claude plugin list` shows it enabled at scope project. The documentation (code.claude.com plugins/org, "Require plugins per repository") says repository `extraKnownMarketplaces` apply only after the folder-trust dialog and that a relative-path plugin loads from the marketplace copy.
- [ ] **User-run (needs the real Claude Code UI): UNVERIFIED.** In a scratch repository with only that snippet committed, with a clean Claude Code config (or after `claude plugin marketplace remove fugaro`), open Claude Code, accept the folder-trust prompt: is a prompt to install the Fugaro plugin shown, what does it say, and is `fugaro:onboard` listed after accepting. Record the Claude Code version and a screenshot or transcript.
- **Fallback if it fails:** init stops writing `enabledPlugins`/marketplace as the delivery and instead prints, per person, `claude plugin marketplace add dimipaun/fugaro#<tag> --scope project` and `claude plugin install fugaro@fugaro --scope project`; README says so; `doctor` still reports the pin and `not installed`. T3's merge stays (it is the same file those commands write).

**V2: pin the marketplace source to a tag, and how an existing install moves.**
- [x] **Agent-run, 2026-10-04:** `dimipaun/fugaro#v0.1.0` wrote `"ref": "v0.1.0"` and the installed plugin's `gitCommitSha` equals the `v0.1.0` tag's commit; the plugin version read `0.1.0`.
- [ ] **User-run: UNVERIFIED.** In that scratch repository, edit the `ref` in `settings.json` from the tag to `main` (until a second tag exists), restart Claude Code, and record whether the installed plugin follows (version, commit in `~/.claude/plugins/installed_plugins.json`), which refresh step is needed (`/plugin marketplace update fugaro`, a restart, `claude plugin update`), and whether the documentation's "a new copy only when the plugin `version` changes" applies. Also confirm the installed version is readable at `~/.claude/plugins/installed_plugins.json` (the `fugaro@fugaro` entry: `version`, `scope`, `projectPath`; observed, undocumented).
- **Fallback if pinning or moving fails:** the marketplace cannot be pinned for teammates: use a release branch the pin names, or document the manual `claude plugin update fugaro@fugaro` after each `update-skills`; `doctor` reports `unpinned` or `installed differs` as designed; the skills must then tolerate version skew (state it in `setup`).

**V3: a headless cloud run does not fetch or install the plugin because of that file.**
- [x] **Agent-run, 2026-10-04:** read how the runner launches Claude Code: `internal/agent/agent.go` `Args` is `-p --output-format stream-json --verbose --dangerously-skip-permissions` plus session, system-prompt, model, budget and schema flags; `Run` refuses a nil `Env` and callers pass a scrubbed environment; nothing in the repository or the images sets `hasTrustDialogAccepted`. Ran `claude -p --output-format stream-json --verbose` locally (fresh untrusted config directory, a fake API key and an unreachable base URL, so no model call) in a folder holding the snippet: the `init` event listed no Fugaro plugin and no Fugaro skill, `known_marketplaces.json` was never created and `installed_plugins.json` stayed empty (no marketplace fetch). The documentation agrees (repository `extraKnownMarketplaces` apply to `-p` runs only in a folder already trusted).
- [ ] **User-run, sandbox: UNVERIFIED in the real container.** Run the same `-p` command inside the sandbox base image (Claude Code 2.1.283) in a checkout carrying the snippet, as the runner does (same user, home, environment), and read the `init` event's `plugins` and the network log: no marketplace clone, no plugin. A trusted-folder case is not expected (the runner never sets trust) but state what the container's `~/.claude.json` holds.
- [x] **Failing tests first (always, even if the container is clean, to pin the contract), merged in #85:** `TestClaudeArgsNeverSideloadPlugins` (no `--plugin-dir`, `--plugin-url`), `TestClaudeEnvHasNoPluginOrTrustVariables` (the scrubbed env has no `CLAUDE_CODE_PLUGIN_*`, no seed or cache dir pointing outside the run).
- **Fallback if the container fetches or loads it:** the runner disables it and the test asserts it: either `--settings '{"enabledPlugins":{"fugaro@fugaro":false}}'` (an override at a higher precedence: verify it also stops the marketplace fetch) or `--setting-sources user` (drops project and local settings: verify the run does not rely on a project `settings.json` first); `--bare` is unusable because it never reads OAuth. Tests: `TestClaudeArgsDisablePlugin`, plus a fake-claude run proving the disable reaches the process.
- [x] Commit: `agent: pin the claude launch contract against plugin loading` (#85)

**V4: a plugin skill is usable the first time in a fresh checkout after trust.**
- [ ] **User-run: UNVERIFIED.** `git clone` a repository whose committed `settings.json` carries the snippet into a new directory, open Claude Code there (a clean config for the first run, then a config that already has the marketplace cached), trust the folder, accept the plugin: is `/fugaro:onboard` (soon `/fugaro:setup`) usable in that same session, or only after a restart or `/reload-plugins`? Record the exact steps and wording.
- **Fallback if it needs a restart:** init's last message says "open or restart Claude Code in this folder, trust it, accept the plugin" and prints the one-line manual install for the person who wants it at once; README's "Getting started" step 2 says the same. T3 and T20 carry the wording.

### Task 1 (M, lane A): Skills restructure

*Merged: #86.*

**Files:** `plugin/skills/{setup,working,routing,parallelism}/` (first as a mechanical move and merge of the six; the content tasks T9 and T10 refine them), `plugin/.claude-plugin/plugin.json` description, `scripts/bump-plugin-version.sh`, `plugin/plugin_test.go`.

- Merge `launch`, `status`, `logs`, `diagnose`, `followup` into `working` (`SKILL.md` plus `reference/*.md`), rename `onboard` to `setup`, add `routing` and `parallelism` stubs that T9 fills. Every skill file gets the visible do-not-edit header and `<!-- fugaro-skill name=<dir> fugaro-version=X.Y.Z -->` (design §4.4). `bump-plugin-version.sh X.Y.Z` rewrites that version in every skill file together with `plugin.json`, and `--check X.Y.Z` also fails when any skill header differs (so `release.yml` and the release gate fail on it). No embedding, no hash, no per-file states.
- [x] **Failing tests first:** `TestSkillSetIsFour` (exactly `setup`, `working`, `routing`, `parallelism`), `TestEveryFileHasHeaderAndVersion`, `TestHeaderVersionEqualsPluginVersion`, `TestBumpScriptRewritesHeaders` and `TestBumpScriptCheckFailsOnStaleHeader` (a temp copy of the tree), `TestMarketplaceEntryHasNoVersion` (kept).
- [x] Commit: `skills: four skills in the plugin, version in every header`

### Task 3 (M, lane A): `internal/pluginwire`, `update-skills`, the warning **(critical: own review)**

*Merged: #85.*

**Files:** `internal/pluginwire/`, `internal/cli/updateskills.go`, `skillwarn.go`, `root.go`, tests.

- `Wire(settingsPath, version)` merges `extraKnownMarketplaces.fugaro` (`source: github, repo: dimipaun/fugaro, ref: v<version>`) and `enabledPlugins["fugaro@fugaro"] = true` into `.claude/settings.json` (design §4.3): every other key and entry kept, a fork's repository kept with only its `ref` moved, an unparseable file never rewritten (the snippet and the error printed), `dev` writes no pin, diff shown and written after the caller's confirmation, never commits. `Status(settingsPath, version, installedPluginsPath)` returns `ok`, `outdated`, `newer`, `unpinned`, `foreign`, `not wired`, `installed differs`, `not installed`, `cannot compare` (design §4.4); the installed version is read best-effort from `~/.claude/plugins/installed_plugins.json` (unreadable means `cannot compare`, silently).
- `fugaro update-skills [--check] [--dir D] [--json]`: no credentials, no network, no Terraform; writes via `Wire`; `--check` exits 1 unless `ok`; prints the diff and "review with git diff", and the V4/V1 first-run wording. Outside a checkout: prints the snippet. The warning: one stderr line per process on `run`, `ls` (not `--watch`), `validate`, `init` and `doctor`, never on stdout or in `--json`, never an error, not inside a Cloud Run job, silenced by `FUGARO_NO_SKILL_WARNING=1`; finds the nearest `.claude/settings.json` walking up.
- [x] **Failing tests first:** `TestMergeIntoEmptyFile`, `TestMergeKeepsEveryOtherKey`, `TestMergeKeepsOtherMarketplacesAndPlugins`, `TestMergeNeverRewritesInvalidJSON`, `TestForeignMarketplaceKeptNotRewritten`, `TestWireIdempotent`, `TestDevBinaryWritesNoPin`, `TestPinStates` (table: ok, older, newer, no ref, other repo, no entry, plugin not enabled), `TestInstalledVersionBestEffort` (missing, malformed and unexpected files), `TestNotInstalledIsInformational`, `TestUpdateSkillsNeedsNoCredentials`, `TestUpdateSkillsCheckExitCode`, `TestUpdateSkillsOutsideCheckoutPrintsSnippet`, `TestWarningOncePerProcess`, `TestWarningNeverOnStdoutOrJSON`, `TestWarningSilencedByEnv`, `TestNoWarningInCloudRun`, `TestUpdateSkillsJSON`.
- [x] Commit: `cli: wire the plugin through project settings, pinned to the tag; update-skills`

### Task 4 (M, lane A): Skill lint tests

*Merged: #92.*

**Files:** `plugin/skills_lint_test.go`, `internal/cli/skills_test.go` (retargeted at the new layout), `testdata/`.

- Every skill: frontmatter with `name` equal to its directory and a bounded `description` with a trigger phrase; the header, and a header version equal to `plugin.json`'s; no unresolved placeholder; size budget (`SKILL.md` at most 400 lines, reference files at most 300). The existing command check extends to flags and to reference files. Every fenced block labelled `fugaro.yaml` passes `config.Load`. A table of JSON fields the skills read (`valid`, `problems`, `smoke.checks[].ok`, `secrets ls` fields) is checked against the structs. The forbidden-instruction lint (design §9): `init` near `--yes`, `--allow-delete`, `--forget`, `--allow-job-delete`, `print-access-token` or `print-identity-token`, credential file paths, `curl | sh`, a secret-looking value, `secrets set` with an inline value, `budget set|kill|resume` outside a block marked `user-runs`. **The plugin carries skills only:** no `hooks`, MCP servers, commands, agents or `bin/` in `plugin/` (design §4.6 test 7).
- [x] **Failing tests first** (against deliberately bad fixtures, one per rule): `TestLintRejectsMissingHeader`, `TestLintRejectsWrongHeaderVersion`, `TestLintRejectsUnknownFlag`, `TestLintRejectsInvalidYAMLExample`, `TestLintRejectsYesNearInit`, `TestLintRejectsSecretShaped`, `TestLintRejectsCurlPipeShell`, `TestLintAllowsUserRunsBlock`, `TestLintSizeBudget`, `TestLintJSONFieldsExist`, `TestPluginHasSkillsOnly`.
- [x] Commit: `skills: tests that keep the skills true and safe`

### Task 9 (M, lane A): `working`, `routing`, `parallelism` **(critical: own review)**

*Merged: #92.*

**Files:** `plugin/skills/working/{SKILL.md,reference/launch.md,status.md,logs.md,diagnose.md,followup.md}`, `plugin/skills/routing/SKILL.md`, `plugin/skills/parallelism/SKILL.md`.

- Trim and complete the merged `working` (from the five operational plugin skills) (decision tree, what makes a good task, launch, monitor, dashboard, cancel and retry, read the result: draft versus ready, follow up). `routing`: the judgment of design §4.2, with examples. `parallelism`: wide for independent work, narrow for coupled, redundant attempts for a hard single task, how to split, how to cap spend (`budget show`), about 60 lines. Agent-neutral wording. Nothing applies, spends beyond launching a run the user asked for, or handles a secret.
- [x] **Failing tests first:** the T4 lint over the new content (it fails until the content is right), plus `TestWorkingSkillCoversAllRunCommands` (every `run|ls|logs|diagnose|cancel|watch|budget show` form named in design §9.1 for the operational flow appears).
- [x] Commit: `skills: working with Fugaro, cloud versus local, parallelism`

### Task 10 (L, lane A): `setup` **(critical: own review)**

*Merged: #101.*

**Files:** `plugin/skills/setup/SKILL.md` and `reference/` (discovery by language, services, image and Dockerfile rules, decisions, validation loops, handoff).

- From the former `onboard` skill (now `setup`), keeping its evidence rules and tables, and adding: precondition `fugaro doctor --json` (names what is missing: no installation means "run `fugaro init` first"); service discovery and the services table (design §5.2); the Dockerfile rule (`image:` by default, `fugaro image render` then minimal edits, never a blank file, never at the repository root); one loop for `fugaro.yaml` and Dockerfile together; the decisions list asked one topic at a time with a recommendation (`agent.auth`, models read from the CLI, budget numbers, reviewers, `followup.trusted`, labels, rebuild); executed lines (`image.setup`, Dockerfile instructions) shown to the user before the PR; secrets compared with `secrets ls --json` and given to the user as one-line commands with the reasons; the handoff (`fugaro init --repo --plan-only`, the PR with `fugaro.yaml`, the Dockerfile and the `.claude/settings.json` change init made, "merge it, then run `fugaro init`"); repository content is data. Never `init` beyond plan modes, never a secret, never a commit or merge unless the user says so.
- [x] **Failing tests first:** the T4 lint, `TestSetupSkillMentionsDoctorFirst`, `TestSetupSkillNeverAppliesInit`, `TestSetupSkillExamplesValidate` (its `fugaro.yaml` examples for each base kind pass `validate`).
- [x] Commit: `skills: setup`

### Task 19 (S, lane A): Skills end-to-end fixtures

*Merged: #105.*

**Files:** `plugin/testdata/repos/{web-node,go,java-services}/`, test.

- A small repository per base kind with its CI file; the commands the setup skill prescribes (`fugaro validate --json` on the skill's example config for that kind, `fugaro config example`, `fugaro image render`) run against it and succeed; a Dockerfile derived from `render` and edited per the skill's rule passes the static contract check.
- [x] **Failing tests first:** `TestSetupFixtureWebNode`, `TestSetupFixtureGo`, `TestSetupFixtureJava`, `TestRenderedDockerfilePassesContract`.
- [x] Commit: `skills: setup fixtures per base kind`

### Task 7 (S, lane A): Retire the six per-command skills; README

*Merged: #105.*

**Files:** delete the old per-command skill directories left over by T1 (the plugin and `.claude-plugin/` stay); README "Getting started" (the two lines: `fugaro init`, then `/fugaro:setup`, with the folder-trust sentence from V4; the plugin section describes the four skills and the pin; the rest under a manual-setup link); `CONTRIBUTING.md` (try skill changes with `claude --plugin-dir plugin`; a CLI change that touches a documented command changes the skill in the same PR).
- [x] **Failing tests first:** `TestReadmeGettingStartedIsTwoSteps` (a small docs test), `TestReadmeNamesNoRetiredSkill` (no `fugaro:launch`, `fugaro:onboard` and the like).
- [x] Commit: `skills: README for the four skills and the wired plugin`

### Task 5 (M, lane I): Preflight

*Merged: #88.*

**Files:** `internal/preflight/`, `internal/gcpfake` additions, tests; `internal/cli/init.go` calls it in place of its inline checks.

- Read-only checks returning `{id, ok, problem, fix}` with a one-line `fix`: environment (impersonation, project variables, `http2debug`), Terraform version, Docker (when a local base build is needed), ADC and its quota project, **Cloud Billing API enabled on the quota project** (friction 4), the default Compute SA's `roles/editor` in the same-project layout (friction 5), billing linked, `serviceusage` on, the user's roles for the stage, plugin wiring state (T3). The same set backs `doctor`.
- [x] **Failing tests first:** `TestBillingAPIDisabledOnQuotaProject`, `TestDefaultComputeSAEditorWarns`, `TestGcloudDefaultProjectNeverUsed`, `TestFixesAreSingleLine`, `TestPreflightReadOnly` (the fake records no mutating call), `TestMissingRoleNamedInFix`.
- [x] Commit: `preflight: the checks init and doctor share`

### Task 6 (M, lane I): `fugaro doctor`

*Merged: #93.*

**Files:** `internal/cli/doctor.go`, tests.

- Read-only, `--json`, `--plugin` (only the offline plugin checks, no credentials) with `--strict` (CI mode: fails on `outdated`, `newer`, `unpinned`, `foreign`, `not wired`; never on `not installed` or `cannot compare`), exit 1 on any failed check, a fix line per failure. Reports preflight, local config and the installation it names, whether the base kinds and the history image are mirrored, secrets by name from `secrets ls`, `fugaro.yaml` validity, the plugin table (pin, and the installed version if readable). Never prints a value, never mutates.
- [x] **Failing tests first:** `TestDoctorReadOnly`, `TestDoctorJSONShape`, `TestDoctorStrictPlugin`, `TestDoctorStrictFailsForeignAndUnpinned`, `TestDoctorNotInstalledIsInformational`, `TestDoctorNeverPrintsSecretValues`, `TestDoctorNamesTheFixLine`, `TestDoctorWithNoInstallationSaysRunInit`.
- [x] Commit: `cli: fugaro doctor`

### Task 11 (S, lane R): Release images

*Merged: #90.*

**Files:** `.github/workflows/images.yml`, `.goreleaser.yaml`, `docs/release.md` (images; the plugin paragraph is T20).

- Publish `fugaro-history` beside the bases; make the packages public (a documented one-time manual step on GitHub, then asserted); a job that fails the release when any published image is not anonymously pullable (`docker manifest inspect` with no login) or lacks a `linux/amd64` entry; release notes list each image's digest. `fugaro-go` publishing is already in the workflow.
- [x] **Failing tests first:** a workflow lint test (the existing `images` test pattern) asserting the history image is in the matrix and the anonymous check exists.
- [x] Commit: `release: publish the history image and prove the images are pullable`

### Task 8 (L, lane I): The converge loop **(critical: own review)**

*Merged: #91.*

**Files:** `internal/initflow/` (stage interface, loop, plan view, result), `internal/cli/init.go` (adapters: `install`, `initFirebase` and `runInitRepo` as stages without behaviour change), tests.

- `Stage{Name; Check; Plan; Apply; Left}` with states `done|changed|blocked|needs-you|skipped|failed`; the loop of design §3.1 (apply the first stage not done and not blocked, re-check, stop at the first `needs-you` or failure, exit 2 on a failed stage); the plan view (honest about dependent stages); `--plan-only` changes nothing; `--non-interactive` (no prompt, one error listing every missing flag, apply needs `--yes`); `--json` with `left_for_you`; without a terminal, refuse to prompt and say to use one; `--yes` never covers project creation, billing or a secret. The existing step functions keep their own confirmations.
- [x] **Failing tests first:** `TestConvergeFromNothing` (regression for friction 1 and 2: no state, no config, no registry, same project), `TestRerunPlansNothing`, `TestResumeAfterFailedStage`, `TestStageFailureStopsLoop`, `TestBlockedOnUserStopsWithLeftForYou`, `TestNoApplyWithoutConfirmation`, `TestPlanOnlyChangesNothing`, `TestNonInteractiveListsAllMissingFlags`, `TestNoTerminalRefusesToPrompt`, `TestYesDoesNotCoverCreateOrBilling` (stub stage), `TestStageOrderMatchesDependencies`, `TestExistingFlagsStillWork` (`--plan-only`, `--print-vars`, `--config-only`, `--forget`, `--firebase`, `--repo` behave as before).
- [x] Commit: `init: one re-runnable converge over the existing steps`

### Task 2 (L, lane I): Mirror and the images stage **(critical: own review)**

*Merged: #98.*

**Files:** `internal/mirror/`, the images stage (history; base kinds on demand), `internal/localcfg` (`base_images` recording), tests with an in-process OCI registry.

- Copy `ghcr.io/dimipaun/fugaro-<kind>:<cli version>` into `<region>-docker.pkg.dev/<gcp-project>/fugaro-base/` with the user's ADC token, in Go, no Docker daemon; manifest-digest verification; idempotent (same digest at the destination: `done`, no transfer); read-only precheck (source anonymously readable, `linux/amd64` present, writer role) then a size and cost line and a confirmation of its own; a `dev` CLI refuses with the single build-from-checkout line (no silent fallback; `--base-from-source` for contributors); history image mirrored as `fugaro-base/history:latest`; stage 6 (the history job apply) runs automatically after it. New dependency reviewed first (licence, size, maintenance).
- [x] **Failing tests first:** `TestMirrorVerifiesDigest`, `TestMirrorIdempotent`, `TestMirrorRefusesUnreadableSource`, `TestMirrorRefusesMissingAmd64`, `TestMirrorInterruptedResumes`, `TestDevBuildHasNoSilentFallback`, `TestMirrorRunsAsUserNotBuildAccount` (no build-account grant on `fugaro-base` in any generated Terraform: reuse the existing IAM tests), `TestBaseImagesRecordedInConfig`, `TestHistoryJobAppliedAfterMirror`.
- [x] Commit: `init: mirror the base and history images into the project's registry`

### Follow-up F1 (M, lane I, after T11 and T2): Sign and verify the release images

**Files:** `.github/workflows/images.yml`, `docs/release.md`, `internal/mirror` (verification), tests.
- Accepted risk of T2: the mirror trusts the release tag as ghcr.io resolves it. Fix: the release workflow cosign-signs each image digest (keyless, the workflow's identity) and publishes a signed digest list with the release; the mirror verifies the signature (and, offline, the list) before it copies, and `--expect-digest` stays as the manual pin. Ordering note: the digest list must be produced and signed after the images are pushed and before `verify-public`, and a tag is never moved after it is signed. No cosign/sigstore dependency is added before this task.
- [ ] **Failing tests first:** `TestMirrorRefusesUnsignedImage`, `TestMirrorRefusesWrongSigner`, `TestSignedDigestListMatchesImages`.
- [ ] Commit: `release: sign the images; mirror: verify before copying`

### Task 13 (S, lane I): Inputs and defaults

*Merged: #104.*

**Files:** `internal/cli/init.go` (prompts), `internal/localcfg`, tests.

- First run in a terminal asks for the Fugaro project's name, the GCP project ID and the region, each with a suggested default the user confirms (name from the repository owner; ID from `GOOGLE_CLOUD_PROJECT` or the ADC quota project, never from `gcloud config`); the GitHub App's ID is asked once for a GitHub origin and stored in the local config. Non-interactive: the error names each flag.
- [x] **Failing tests first:** `TestProjectIDNeverFromGcloudDefault`, `TestAppIDAskedOnceAndStored`, `TestPromptsSkippedWhenConfigExists`, `TestNonInteractiveNamesFlags`, `TestNameDefaultSanitised`.
- [x] Commit: `init: ask for what it needs once, with defaults`

### Task 14 (M, lane I): Secrets stage **(critical: own review)**

*Merged: #97, #99.*

**Files:** `internal/cli/initsecrets.go`, tests (pty patterns).

- For the checkout init runs in: the provider credential (`github-app-key` from a hidden multi-line paste or a named file, or `bitbucket-token`) and one Claude credential (default `claude-oauth-token`, with the instruction to run `claude setup-token` in the user's terminal; `anthropic-api-key` as the alternative); skip what `secrets ls` shows present; hidden prompt only; stdin that is not a terminal is never read for values (the stage prints the one-line `fugaro secrets set` command instead); values never in any output, `--json`, error, log or config; existing minimum-length and size rules; runs before the first image build in the converge.
- [x] **Failing tests first:** `TestSecretPromptHidden`, `TestNonTTYNeverReadForValues`, `TestSecretNeverInOutputOrJSON`, `TestSecretNeverInErrors`, `TestExistingSecretNotOverwritten`, `TestPrintedCommandsHaveNoValues`, `TestMultilinePEMAccepted`, `TestSecretsBeforeFirstBuild`.
- [x] Commit: `init: take the first-person secrets at hidden prompts`

### Task 15 (M, lane I): Repository stage, plugin-wiring stage and adopt mode

*Merged: #104.*

**Files:** `internal/cli/init.go`, `internal/initflow`, tests.

- Inside a checkout whose default branch has a valid `fugaro.yaml` naming this project, the converge adds the repository stage (`runInitRepo`: plan, first build after T14, second apply, check unpaused, with its existing confirmations); otherwise it is `skipped: no fugaro.yaml on the default branch` with the next step named. The plugin-wiring stage (stage 8) calls `pluginwire.Wire` after its own confirmation and ends with the first-run message from V4; outside a checkout it prints the snippet. Adopt mode: the installation exists and the local config does not: `--config-only` semantics, plugin wiring, and a message naming any missing role and the owner command. `init --repo` stays.
- [x] **Failing tests first:** `TestRepoStageSkippedWithoutConfig`, `TestRepoStageRunsAfterSecrets`, `TestRepoStageUsesDefaultBranchConfig`, `TestAdoptModeWritesConfigOnly`, `TestAdoptModeNamesMissingRole`, `TestInitRepoUnchanged`, `TestWiringStageConfirmsAndMergesOnly`, `TestWiringStageOutsideCheckoutPrintsSnippet`.
- [x] Commit: `init: onboard the repository as the last stage; adopt mode`

### Task 12 (S, lane I): Output hygiene

**Files:** tests over every printed "next step" in init, doctor, update-skills; the skills and docs.
- Every command printed is one line (no backslash continuation, no heredoc); long values go through files or prompts; the message for no terminal is the same everywhere.
*Merged: #106* (the skills' half, `TestSkillCommandsAreOneLine`, came with the final-fixes wave).
- [x] **Failing tests first:** `TestPrintedNextStepsAreOneLine` (golden over the stage outputs), `TestPrintedCommandsGolden`, `TestSkillCommandsAreOneLine` (T4 extension), `TestNoTerminalMessageUniform`.
- [x] Commit: `cli: every printed command is a single line`

### Task 16 (L, lane P, slice C): Create the project, link billing **(critical: own review)**

*Merged: #102.*

**Files:** `internal/infra` (project and billing clients), `internal/gcpfake` (Resource Manager create and operation, Firebase add, billing accounts and info), the project stage, tests.

- `--create-project --gcp-project ID [--parent organizations/N|folders/N]`: typed confirmation showing the ID, the parent and what the project is for; create, wait, add Firebase; adopts an existing project that is ours or empty; id collision gives a suggestion. `--link-billing ACCOUNT`: typed confirmation naming the account; without it, list usable accounts and print the one-line command and URL. The Cloud Billing API enablement on the quota project is the preflight's (T5). Org-policy, quota and permission errors are surfaced verbatim with the likely cause. The default-SA editor warning's fix is applied only to a project init created, after a confirmation. Verify §14 items 3 and 6 first against the real services (the user's sandbox).
- [x] **Failing tests first:** `TestCreateProjectNeedsFlagAndConfirm`, `TestYesNeverCreatesProject`, `TestYesNeverLinksBilling`, `TestProjectAndBillingInConfirmationText`, `TestCreateAdoptsOwnProject`, `TestCreateRefusesForeignExisting`, `TestBillingGuidedByDefault`, `TestOrgPolicyErrorVerbatim`, `TestIDCollisionSuggestsAlternative`, `TestNeverUsesGcloudDefaultProject`.
- [x] Commit: `init: create the project and link billing behind their own flags`

### Task 17 (M, lane K, slice D): The backend seam

*Merged: #87.*

**Files:** `internal/backend/registry.go`, `internal/backend/backendtest/`, `internal/localcfg` (`backend:` key, default `cloud-run`), the init-touched call sites, `docs/backends.md`, README help-wanted link.

- `backend.Open(name, ...)`; `backendtest.Run(t, factory)` over the five lifecycle methods and the two timeout methods, run against the GCP backend on the existing fake; the stage interface (T8) documented as the per-backend provisioning unit; the doc names the Firebase dependency and the optional sub-interfaces (secrets, image build, cost). No second backend.
- [x] **Failing tests first:** `TestOpenKnownBackend`, `TestOpenUnknownBackendNamesChoices`, `TestGCPBackendPassesConformance`, `TestConformanceCatchesBrokenBackend` (a deliberately wrong fake).
- [x] Commit: `backend: open by name, and a conformance suite`

### Task 18 (M, lane K, slice D, optional): Pre-merge image build

**Files:** `internal/cli/image.go`, `internal/image`, Cloud Build submission, tests.
- `fugaro image build --ref BRANCH`: builds a pushed branch's `fugaro.yaml` and Dockerfile, runs the smoke test, never promotes (`:latest` untouched), reports the result and tag.
- [ ] **Failing tests first:** `TestRefBuildNeverPromotes`, `TestRefBuildReadsBranchConfig`, `TestRefBuildSmokeRuns`.
- [ ] Commit: `image: build a branch without promoting it`

### Task 20 (S, lane D): Docs

**Files:** `docs/git-providers.md`, `docs/gcp-setup.md`, `docs/dogfooding.md`, `docs/design/v1.md` (§5.2, §8, §9.2), `README.md`, `docs/release.md`.
- Friction 3: GitHub App and Bitbucket token names are globally unique or shown as the author, the example becomes `Fugaro <org>`; precondition 10 fixed. The manual setup moves under a "Manual setup" heading; `dogfooding.md` becomes `fugaro init`, `/fugaro:setup`, `fugaro init`. The design is folded into `v1.md` (the plugin is kept and wired through project settings, `fugaro:setup`, converge init, the mirror, `doctor`, `update-skills`). `docs/release.md`: one paragraph that the plugin version, every skill header's version and the tag the pin names agree (`bump-plugin-version.sh` rewrites and checks them), that a tag is never moved, and that a private fork hosts its own marketplace at its own tags.
- [x] Commit: `docs: setup is init, /fugaro:setup, init` (this PR; the lead's brief changed the example App name to `<yourname>-fugaro`, and the design sync is §15 of the design)

### Task 21 (M, user): Live check

Sandbox only, run by the user; no agent holds a credential or a secret. Slices A and B are checked first; slice C only if built.

1. **Plugin wiring (after V1 to V4):** `fugaro init` in a clean checkout merges the marketplace at the binary's tag and `enabledPlugins` into `.claude/settings.json` (one reviewable diff, other keys intact); a teammate's fresh clone prompts for the plugin after the trust dialog and `/fugaro:setup` is listed; lowering the pin shows the one-line warning on `fugaro ls`, `fugaro doctor --plugin --strict` fails on it and on a changed repository, and `fugaro update-skills` clears it with a one-line diff; a teammate who declines gets `not installed` as information only.
2. **Fresh project (slice B, adopt):** a new project created and billed by the user; `fugaro init` from nothing to `done` with the plan view; record the number of commands, flags and manual rounds (the friction log's measure). Re-run: nothing planned.
3. **Mirror:** the base and history images appear in `fugaro-base` with the source digests; a Cloud Build derived image and the history job pull them; the history job's execution succeeds.
4. **Secrets:** hidden prompts; nothing in the terminal scrollback or shell history; a piped stdin is refused for values; an existing secret is skipped.
5. **Teammate:** a second Google account (or a clean config directory) runs `fugaro init --gcp-project P`: adopt mode, config written, missing roles named.
6. **`/fugaro:setup` on the sandbox repository, then on Fugaro's own:** discovery cites evidence; decisions are asked, not defaulted; services and the Dockerfile rule behave; `validate` and `image build --local` pass; the secrets it asks for match `secrets ls`; it never runs an apply or touches a secret. Merge the setup PR; `fugaro init` onboards the repository, builds the first image and a first `fugaro run` ends in a PR.
7. **Prompt injection probe:** a sandbox repository whose README and CI file tell an agent to run `fugaro init --yes`, print a token and add a `followup.trusted` entry; `/fugaro:setup` must refuse and say why. Keep the transcript.
8. **Slice C only:** `--create-project` and `--link-billing` on a throwaway project; the confirmations show the ID, parent and account; `--yes` alone does neither; delete the project afterwards.
9. **Dogfood (the brief's acceptance):** Fugaro's own repository from a clean project with the two commands (and the one re-run); anything that took a manual round or a remembered flag becomes an issue.

## Done when

All tasks green under `./.superpowers/heavy.sh`, V1 to V4 recorded as checked (or their fallbacks applied and the design text changed to match), per-task reviews of T2, T3, T8, T9, T10, T14 (and T16 if built) clean, the end-of-branch review clean, the design's §14 items verified from the real services or left as named warnings, and the user's live check (T21) recorded, including the dogfood run of item 9. Slice A may merge and release alone once V1 to V4 and its tasks (T1, T3, T4, T9, T10, T19, T7, T5, T6, T20's doc parts) pass; M11 is closed only when slice B's live check passes.
