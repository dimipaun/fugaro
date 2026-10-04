# M11: Simple Setup and Fugaro-Owned Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax. Briefs to implementers stay short: the task text plus the Review Focus line that applies.

**Goal:** A new team sets Fugaro up with two things: `fugaro init` (one re-runnable, idempotent command that converges the whole installation, mirrors the images, takes the first-person secrets at hidden prompts and installs the skills) and `/fugaro-setup` in the coding agent (which writes `fugaro.yaml` and, when needed, a Dockerfile, with the human's decisions). The skills are Fugaro-owned, embedded in the binary, version-stamped, staleness-checked and cleanly overwritten. No secret value ever passes through an agent, and no test touches the cloud.

**Spec:** [m11-setup-and-skills.md](../design/m11-setup-and-skills.md), the user's brief [setup-and-skills-spec-source.md](../design/setup-and-skills-spec-source.md), [v1.md](../design/v1.md) §5.2, §7.2, §8, §9.

## What is built and what this reuses

| Need | Already there |
|---|---|
| Installation, Firebase backend, repository apply | `internal/cli/init.go` (`install`, `initFirebase`, `runInitRepo`), `init_firebase.go`, `internal/infra`, each with plan, confirmation, `--plan-only`, `--print-vars`, `--config-only`, `--forget` |
| Local config and base images | `internal/localcfg` (`base_images`, `ParseBaseImageFlag`), `internal/image` (published base name by version) |
| Secrets | `internal/cli/secrets.go` (hidden prompt, `secrets ls`, labelled create), `backend/gcp.Secrets`, pty tests |
| Image build and contract | `fugaro image render|build --local`, `fugaro validate`, `internal/imagecheck`, `images/` |
| Skill checks | `internal/cli/skills_test.go` (`TestSkillCommandsExist`), `plugin/plugin_test.go` |
| Fakes | `internal/gcpfake`, the fake `claude`, `internal/gateway/anthropicfake`, `-tags terraform` tests |
| Onboarding content | `plugin/skills/onboard/SKILL.md` and the five operational plugin skills |
| Release images | `.github/workflows/images.yml`, `.goreleaser.yaml` |

**Gaps:** init is three commands in a manual order; no image mirror (ghcr is unreadable by Cloud Build and Run) and no published history image; secrets are not part of init; no skills installer, stamp or staleness check; no `doctor`; no project creation; the plugin and the brief disagree about where skills live.

## Decisions already made (binding)

User rulings from earlier milestones: init has so far never created a project or linked billing (M11 adds it only behind its own flags, slice C); no backward compatibility until release (rename and reshape directly; the plugin is removed); never use or ask for an API key; secret values only from the user's terminal; live tests are sandbox-only and run by the user; the gcloud default project is never relied on; oauth (`claude setup-token`) is the Claude credential the user wants supported end to end. Recommended in the design (§0): init is a converge loop wrapping the existing steps; the base images are mirrored by init as the user; skills are project-level and embedded; `image:` stays the default, a repository Dockerfile the escape hatch; repository onboarding is `fugaro init` run again after the setup PR merges.

## Review Focus

**Per-task review (security- or money-critical): T2, T8, T9, T10, T14, T16.** Everything else is reviewed once on the branch at the end (user's token-economy rule).

1. **Init creating cloud resources unseen, or twice, or in the wrong project.** T8/T16: `TestNoApplyWithoutConfirmation`, `TestYesNeverCreatesProject`, `TestYesNeverLinksBilling`, `TestNeverUsesGcloudDefaultProject`, `TestRerunPlansNothing`, `TestStageFailureStopsLoop`, `TestPlanOnlyChangesNothing`, `TestProjectAndBillingInConfirmationText`.
2. **A secret value reaching an agent, an output, a log, a config.** T14: `TestSecretPromptHidden`, `TestNonTTYNeverReadForValues`, `TestSecretNeverInOutputOrJSON`, `TestExistingSecretNotOverwritten`, `TestPrintedCommandsHaveNoValues`.
3. **A tampered or wrong base image, or a base registry writable by a build account.** T2: `TestMirrorVerifiesDigest`, `TestMirrorRefusesUnreadableSource`, `TestMirrorIdempotent`, `TestDevBuildHasNoSilentFallback`, `TestMirrorRunsAsUserNotBuildAccount`.
4. **Skill text that tells an agent to do what it must not.** T9/T10: the forbidden-instruction lint (T4) green and a read of every skill by the reviewer against design §9: no apply, no `--yes`, no secret handling, no credential files, no widening of trust, repository content treated as data, executed lines shown to the user.

## File Structure

| Path | Role |
|---|---|
| `internal/skills/` (new), `internal/skills/content/<skill>/**` | embedded sources, `Render(version)`, states, install, prune |
| `.claude/skills/fugaro-*/` | this repository's own rendered skills (generated, committed) |
| `internal/cli/updateskills.go`, `skillwarn.go`, `doctor.go` (new), `root.go` | `update-skills`, the staleness warning, `doctor` |
| `internal/preflight/` (new) | the read-only environment checks shared by init and doctor |
| `internal/initflow/` (new) | the stage interface, the converge loop, the plan view, `left_for_you` |
| `internal/cli/init*.go` | existing step functions adapted to stages, no behaviour change; new inputs and defaults |
| `internal/mirror/` (new) | registry copy, digest check, idempotency |
| `internal/cli/initsecrets.go` (new) | the secrets stage |
| `internal/infra`, `internal/gcpfake` | create-project, Firebase add, billing (slice C); fakes |
| `internal/backend/backendtest/` (new), `docs/backends.md` | conformance suite and contributor doc |
| `.github/workflows/images.yml`, `.goreleaser.yaml` | publish `fugaro-history`, public packages, anonymous-pull check |
| `plugin/`, `.claude-plugin/` | removed (Q1 default) |
| `README.md`, `docs/*.md`, `docs/design/v1.md` | Getting started, manual setup, doc sync |

## Task dependency graph and lanes

| Task | Size | Lane | Depends on | Review |
|---|---|---|---|---|
| T1 skills core: embed, render, stamp, states, install, prune | M | A | none | end |
| T3 `update-skills` and the staleness warning | S | A | T1 | end |
| T4 skill lint tests: header, stamp, commands and flags exist, yaml examples validate, JSON fields, forbidden instructions, size | M | A | T1 | end |
| T9 skill content: `fugaro-working`, `fugaro-routing`, `fugaro-parallelism` **(critical)** | M | A | T1, T4 | own |
| T10 skill content: `fugaro-setup` **(critical)** | L | A | T1, T4, T9 | own |
| T19 skills end-to-end fixtures per base kind | S | A | T10 | end |
| T7 remove the plugin; render this repository's own skills; README Getting started | S | A | T3, T10 | end |
| T5 `internal/preflight`: shared checks incl. Cloud Billing API and default-SA editor | M | I | none | end |
| T6 `fugaro doctor` | M | I | T3, T5 | end |
| T11 release: publish `fugaro-history`, public packages, anonymous-pull check | S | R | none | end |
| T8 `internal/initflow`: stage interface, converge loop, plan view, `--non-interactive`, `--json` **(critical)** | L | I | T5 | own |
| T2 `internal/mirror` and the images stage **(critical)** | L | I | T8, T11 | own |
| T13 inputs and defaults, GitHub App ID asked once | S | I | T8 | end |
| T14 secrets stage **(critical)** | M | I | T8 | own |
| T15 repository stage in the converge; adopt mode for teammates | M | I | T8, T2, T14 | end |
| T12 output hygiene: one-line commands everywhere, no TTY message | S | I | T8, T14, T15 | end |
| T16 create-project, Firebase add, billing linkage **(slice C, critical)** | L | P | T8, T5 | own |
| T17 backend name, `backendtest`, `docs/backends.md` (slice D) | M | K | none | end |
| T18 `fugaro image build --ref BRANCH`, non-promoting (slice D, optional) | M | K | T15 | end |
| T20 docs: friction fixes, design sync, dogfooding page | S | D | T10, T15 | end |
| T21 live check, user-run on a sandbox project | M | user | T7, T12, T15 (T16 for its part) | n/a |

Order: lane A (T1, then T3 and T4, T9, T10, T19, T7) and lane I (T5, T8, then T2, T13, T14, T15, T6, T12) run in parallel and are independent. T11 early (T2 needs it). Slice A is shippable when lane A and T6, T20's doc parts and T21's skills items are done; slice B when lane I and T11 are; T16 and the slice D tasks follow.

### Task 1 (M, lane A): Skills core

**Files:** `internal/skills/skills.go`, `render.go`, `state.go`, `install.go`, `content/fugaro-*/` (placeholder content for tests), tests.

- `//go:embed all:content`; `Render(version)` returns each file as frontmatter, the stamp comment (`fugaro-skill name=... fugaro-version=... sha256=<hash of this file's body>`) and the visible do-not-edit line, then the body. `Status(dir, version)` returns per file one of `ok`, `outdated`, `newer`, `edited`, `missing`, `foreign` (design §4.4); `dev` stamps compare by hash. `Install(dir, version)` writes via temporary names and renames, removes stamped files no longer embedded, never touches unstamped or `foreign` files and names them, returns the changed list.
- [ ] **Failing tests first:** `TestRenderHasHeaderAndStamp`, `TestStampHashVerifies`, `TestStatusStates` (table: ok, older, newer, edited, missing, foreign, dev), `TestInstallOverwritesCleanly`, `TestInstallPrunesStampedOnly`, `TestInstallNeverTouchesForeign`, `TestInstallInterruptedLeavesNoHalfFile`, `TestReferenceFilesStamped`.
- [ ] Commit: `skills: embedded, stamped, cleanly overwritten skills`

### Task 3 (S, lane A): `update-skills` and the warning

**Files:** `internal/cli/updateskills.go`, `skillwarn.go`, `root.go`, tests.

- `fugaro update-skills [--check] [--dir D] [--json]` (no credentials, no network, no Terraform; `--check` exits 1 on any stale state without writing; prints the changed files and "review with git diff"). The warning: one stderr line per process on `run`, `ls` (not `--watch`), `validate`, `init` and `doctor`, never on stdout or in `--json`, never an error, not inside a Cloud Run job, silenced by `FUGARO_NO_SKILL_WARNING=1`; finds the nearest `.claude/skills` walking up.
- [ ] **Failing tests first:** `TestUpdateSkillsNeedsNoCredentials`, `TestUpdateSkillsCheckExitCode`, `TestWarningOncePerProcess`, `TestWarningNeverOnStdoutOrJSON`, `TestWarningSilencedByEnv`, `TestNoWarningInCloudRun`, `TestUpdateSkillsJSON`.
- [ ] Commit: `cli: update-skills and a warning for stale skills`

### Task 4 (M, lane A): Skill lint tests

**Files:** `internal/skills/lint_test.go`, `internal/cli/skills_test.go` (retargeted at the embedded content), `testdata/`.

- Every skill: frontmatter with `name` equal to its directory and a bounded `description` with a trigger phrase; the header and a verifying stamp; no unresolved placeholder; size budget (`SKILL.md` at most 400 lines, reference files at most 300). The existing command check extends to flags and to reference files. Every fenced block labelled `fugaro.yaml` passes `config.Load`. A table of JSON fields the skills read (`valid`, `problems`, `smoke.checks[].ok`, `secrets ls` fields) is checked against the structs. The forbidden-instruction lint (design §9): `init` near `--yes`, `--allow-delete`, `--forget`, `--allow-job-delete`, `print-access-token` or `print-identity-token`, credential file paths, `curl | sh`, a secret-looking value, `secrets set` with an inline value, `budget set|kill|resume` outside a block marked `user-runs`. `TestSkillsInRepoAreCurrent` compares this repository's `.claude/skills` with the render.
- [ ] **Failing tests first** (against deliberately bad fixtures, one per rule): `TestLintRejectsMissingHeader`, `TestLintRejectsUnknownFlag`, `TestLintRejectsInvalidYAMLExample`, `TestLintRejectsYesNearInit`, `TestLintRejectsSecretShaped`, `TestLintRejectsCurlPipeShell`, `TestLintAllowsUserRunsBlock`, `TestLintSizeBudget`, `TestLintJSONFieldsExist`.
- [ ] Commit: `skills: tests that keep the skills true and safe`

### Task 9 (M, lane A): `fugaro-working`, `fugaro-routing`, `fugaro-parallelism` **(critical: own review)**

**Files:** `internal/skills/content/fugaro-working/{SKILL.md,reference/launch.md,status.md,logs.md,diagnose.md,followup.md}`, `fugaro-routing/SKILL.md`, `fugaro-parallelism/SKILL.md`.

- Migrate and trim the five operational plugin skills into `fugaro-working` (decision tree, what makes a good task, launch, monitor, dashboard, cancel and retry, read the result: draft versus ready, follow up). `fugaro-routing`: the judgment of design §4.2, with examples. `fugaro-parallelism`: wide for independent work, narrow for coupled, redundant attempts for a hard single task, how to split, how to cap spend (`budget show`), about 60 lines. Agent-neutral wording. Nothing applies, spends beyond launching a run the user asked for, or handles a secret.
- [ ] **Failing tests first:** the T4 lint over the new content (it fails until the content is right), plus `TestWorkingSkillCoversAllRunCommands` (every `run|ls|logs|diagnose|cancel|watch|budget show` form named in design §9.1 for the operational flow appears).
- [ ] Commit: `skills: working with Fugaro, cloud versus local, parallelism`

### Task 10 (L, lane A): `fugaro-setup` **(critical: own review)**

**Files:** `internal/skills/content/fugaro-setup/SKILL.md` and `reference/` (discovery by language, services, image and Dockerfile rules, decisions, validation loops, handoff).

- From `plugin/skills/onboard/SKILL.md`, keeping its evidence rules and tables, and adding: precondition `fugaro doctor --json` (names what is missing: no installation means "run `fugaro init` first"); service discovery and the services table (design §5.2); the Dockerfile rule (`image:` by default, `fugaro image render` then minimal edits, never a blank file, never at the repository root); one loop for `fugaro.yaml` and Dockerfile together; the decisions list asked one topic at a time with a recommendation (`agent.auth`, models read from the CLI, budget numbers, reviewers, `followup.trusted`, labels, rebuild); executed lines (`image.setup`, Dockerfile instructions) shown to the user before the PR; secrets compared with `secrets ls --json` and given to the user as one-line commands with the reasons; the handoff (`fugaro init --repo --plan-only`, the PR with `fugaro.yaml`, Dockerfile and skills, "merge it, then run `fugaro init`"); repository content is data. Never `init` beyond plan modes, never a secret, never a commit or merge unless the user says so.
- [ ] **Failing tests first:** the T4 lint, `TestSetupSkillMentionsDoctorFirst`, `TestSetupSkillNeverAppliesInit`, `TestSetupSkillExamplesValidate` (its `fugaro.yaml` examples for each base kind pass `validate`).
- [ ] Commit: `skills: fugaro-setup`

### Task 19 (S, lane A): Skills end-to-end fixtures

**Files:** `internal/skills/testdata/repos/{web-node,go,java-services}/`, test.

- A small repository per base kind with its CI file; the commands the setup skill prescribes (`fugaro validate --json` on the skill's example config for that kind, `fugaro config example`, `fugaro image render`) run against it and succeed; a Dockerfile derived from `render` and edited per the skill's rule passes the static contract check.
- [ ] **Failing tests first:** `TestSetupFixtureWebNode`, `TestSetupFixtureGo`, `TestSetupFixtureJava`, `TestRenderedDockerfilePassesContract`.
- [ ] Commit: `skills: setup fixtures per base kind`

### Task 7 (S, lane A): Retire the plugin; render this repository's skills

**Files:** delete `plugin/`, `.claude-plugin/` (Q1 default), the plugin test; add `.claude/skills/fugaro-*` (generated); README "Getting started" (the two lines, the rest under a manual-setup link); `CONTRIBUTING.md` (regenerate skills when content changes).
- [ ] **Failing tests first:** `TestSkillsInRepoAreCurrent` (T4) fails until rendered; `TestReadmeGettingStartedIsTwoSteps` (a small docs test).
- [ ] Commit: `skills: project-level skills replace the plugin`

### Task 5 (M, lane I): Preflight

**Files:** `internal/preflight/`, `internal/gcpfake` additions, tests; `internal/cli/init.go` calls it in place of its inline checks.

- Read-only checks returning `{id, ok, problem, fix}` with a one-line `fix`: environment (impersonation, project variables, `http2debug`), Terraform version, Docker (when a local base build is needed), ADC and its quota project, **Cloud Billing API enabled on the quota project** (friction 4), the default Compute SA's `roles/editor` in the same-project layout (friction 5), billing linked, `serviceusage` on, the user's roles for the stage, skills state. The same set backs `doctor`.
- [ ] **Failing tests first:** `TestBillingAPIDisabledOnQuotaProject`, `TestDefaultComputeSAEditorWarns`, `TestGcloudDefaultProjectNeverUsed`, `TestFixesAreSingleLine`, `TestPreflightReadOnly` (the fake records no mutating call), `TestMissingRoleNamedInFix`.
- [ ] Commit: `preflight: the checks init and doctor share`

### Task 6 (M, lane I): `fugaro doctor`

**Files:** `internal/cli/doctor.go`, tests.

- Read-only, `--json`, `--skills`/`--strict` (CI mode: fails on `outdated`, `edited`, `missing`), exit 1 on any failed check, a fix line per failure. Reports preflight, local config and the installation it names, whether the base kinds and the history image are mirrored, secrets by name from `secrets ls`, `fugaro.yaml` validity, the skills table. Never prints a value, never mutates.
- [ ] **Failing tests first:** `TestDoctorReadOnly`, `TestDoctorJSONShape`, `TestDoctorStrictSkills`, `TestDoctorNeverPrintsSecretValues`, `TestDoctorNamesTheFixLine`, `TestDoctorWithNoInstallationSaysRunInit`.
- [ ] Commit: `cli: fugaro doctor`

### Task 11 (S, lane R): Release images

**Files:** `.github/workflows/images.yml`, `.goreleaser.yaml`, `docs/release.md`.

- Publish `fugaro-history` beside the bases; make the packages public (a documented one-time manual step on GitHub, then asserted); a job that fails the release when any published image is not anonymously pullable (`docker manifest inspect` with no login) or lacks a `linux/amd64` entry; release notes list each image's digest. `fugaro-go` publishing is already in the workflow.
- [ ] **Failing tests first:** a workflow lint test (the existing `images` test pattern) asserting the history image is in the matrix and the anonymous check exists.
- [ ] Commit: `release: publish the history image and prove the images are pullable`

### Task 8 (L, lane I): The converge loop **(critical: own review)**

**Files:** `internal/initflow/` (stage interface, loop, plan view, result), `internal/cli/init.go` (adapters: `install`, `initFirebase` and `runInitRepo` as stages without behaviour change), tests.

- `Stage{Name; Check; Plan; Apply; Left}` with states `done|changed|blocked|needs-you|skipped|failed`; the loop of design §3.1 (apply the first stage not done and not blocked, re-check, stop at the first `needs-you` or failure, exit 2 on a failed stage); the plan view (honest about dependent stages); `--plan-only` changes nothing; `--non-interactive` (no prompt, one error listing every missing flag, apply needs `--yes`); `--json` with `left_for_you`; without a terminal, refuse to prompt and say to use one; `--yes` never covers project creation, billing or a secret. The existing step functions keep their own confirmations.
- [ ] **Failing tests first:** `TestConvergeFromNothing` (regression for friction 1 and 2: no state, no config, no registry, same project), `TestRerunPlansNothing`, `TestResumeAfterFailedStage`, `TestStageFailureStopsLoop`, `TestBlockedOnUserStopsWithLeftForYou`, `TestNoApplyWithoutConfirmation`, `TestPlanOnlyChangesNothing`, `TestNonInteractiveListsAllMissingFlags`, `TestNoTerminalRefusesToPrompt`, `TestYesDoesNotCoverCreateOrBilling` (stub stage), `TestStageOrderMatchesDependencies`, `TestExistingFlagsStillWork` (`--plan-only`, `--print-vars`, `--config-only`, `--forget`, `--firebase`, `--repo` behave as before).
- [ ] Commit: `init: one re-runnable converge over the existing steps`

### Task 2 (L, lane I): Mirror and the images stage **(critical: own review)**

**Files:** `internal/mirror/`, the images stage (history; base kinds on demand), `internal/localcfg` (`base_images` recording), tests with an in-process OCI registry.

- Copy `ghcr.io/dimipaun/fugaro-<kind>:<cli version>` into `<region>-docker.pkg.dev/<gcp-project>/fugaro-base/` with the user's ADC token, in Go, no Docker daemon; manifest-digest verification; idempotent (same digest at the destination: `done`, no transfer); read-only precheck (source anonymously readable, `linux/amd64` present, writer role) then a size and cost line and a confirmation of its own; a `dev` CLI refuses with the single build-from-checkout line (no silent fallback; `--base-from-source` for contributors); history image mirrored as `fugaro-base/history:latest`; stage 6 (the history job apply) runs automatically after it. New dependency reviewed first (licence, size, maintenance).
- [ ] **Failing tests first:** `TestMirrorVerifiesDigest`, `TestMirrorIdempotent`, `TestMirrorRefusesUnreadableSource`, `TestMirrorRefusesMissingAmd64`, `TestMirrorInterruptedResumes`, `TestDevBuildHasNoSilentFallback`, `TestMirrorRunsAsUserNotBuildAccount` (no build-account grant on `fugaro-base` in any generated Terraform: reuse the existing IAM tests), `TestBaseImagesRecordedInConfig`, `TestHistoryJobAppliedAfterMirror`.
- [ ] Commit: `init: mirror the base and history images into the project's registry`

### Task 13 (S, lane I): Inputs and defaults

**Files:** `internal/cli/init.go` (prompts), `internal/localcfg`, tests.

- First run in a terminal asks for the Fugaro project's name, the GCP project ID and the region, each with a suggested default the user confirms (name from the repository owner; ID from `GOOGLE_CLOUD_PROJECT` or the ADC quota project, never from `gcloud config`); the GitHub App's ID is asked once for a GitHub origin and stored in the local config. Non-interactive: the error names each flag.
- [ ] **Failing tests first:** `TestProjectIDNeverFromGcloudDefault`, `TestAppIDAskedOnceAndStored`, `TestPromptsSkippedWhenConfigExists`, `TestNonInteractiveNamesFlags`, `TestNameDefaultSanitised`.
- [ ] Commit: `init: ask for what it needs once, with defaults`

### Task 14 (M, lane I): Secrets stage **(critical: own review)**

**Files:** `internal/cli/initsecrets.go`, tests (pty patterns).

- For the checkout init runs in: the provider credential (`github-app-key` from a hidden multi-line paste or a named file, or `bitbucket-token`) and one Claude credential (default `claude-oauth-token`, with the instruction to run `claude setup-token` in the user's terminal; `anthropic-api-key` as the alternative); skip what `secrets ls` shows present; hidden prompt only; stdin that is not a terminal is never read for values (the stage prints the one-line `fugaro secrets set` command instead); values never in any output, `--json`, error, log or config; existing minimum-length and size rules; runs before the first image build in the converge.
- [ ] **Failing tests first:** `TestSecretPromptHidden`, `TestNonTTYNeverReadForValues`, `TestSecretNeverInOutputOrJSON`, `TestSecretNeverInErrors`, `TestExistingSecretNotOverwritten`, `TestPrintedCommandsHaveNoValues`, `TestMultilinePEMAccepted`, `TestSecretsBeforeFirstBuild`.
- [ ] Commit: `init: take the first-person secrets at hidden prompts`

### Task 15 (M, lane I): Repository stage and adopt mode

**Files:** `internal/cli/init.go`, `internal/initflow`, tests.

- Inside a checkout whose default branch has a valid `fugaro.yaml` naming this project, the converge adds the repository stage (`runInitRepo`: plan, first build after T14, second apply, check unpaused, with its existing confirmations); otherwise it is `skipped: no fugaro.yaml on the default branch` with the next step named. Adopt mode: the installation exists and the local config does not: `--config-only` semantics, skills, and a message naming any missing role and the owner command. `init --repo` stays.
- [ ] **Failing tests first:** `TestRepoStageSkippedWithoutConfig`, `TestRepoStageRunsAfterSecrets`, `TestRepoStageUsesDefaultBranchConfig`, `TestAdoptModeWritesConfigOnly`, `TestAdoptModeNamesMissingRole`, `TestInitRepoUnchanged`.
- [ ] Commit: `init: onboard the repository as the last stage; adopt mode`

### Task 12 (S, lane I): Output hygiene

**Files:** tests over every printed "next step" in init, doctor, update-skills; the skills and docs.
- Every command printed is one line (no backslash continuation, no heredoc); long values go through files or prompts; the message for no terminal is the same everywhere.
- [ ] **Failing tests first:** `TestPrintedNextStepsAreOneLine` (golden over the stage outputs), `TestSkillCommandsAreOneLine` (T4 extension), `TestNoTerminalMessageUniform`.
- [ ] Commit: `cli: every printed command is a single line`

### Task 16 (L, lane P, slice C): Create the project, link billing **(critical: own review)**

**Files:** `internal/infra` (project and billing clients), `internal/gcpfake` (Resource Manager create and operation, Firebase add, billing accounts and info), the project stage, tests.

- `--create-project --gcp-project ID [--parent organizations/N|folders/N]`: typed confirmation showing the ID, the parent and what the project is for; create, wait, add Firebase; adopts an existing project that is ours or empty; id collision gives a suggestion. `--link-billing ACCOUNT`: typed confirmation naming the account; without it, list usable accounts and print the one-line command and URL. The Cloud Billing API enablement on the quota project is the preflight's (T5). Org-policy, quota and permission errors are surfaced verbatim with the likely cause. The default-SA editor warning's fix is applied only to a project init created, after a confirmation. Verify §14 items 3 and 6 first against the real services (the user's sandbox).
- [ ] **Failing tests first:** `TestCreateProjectNeedsFlagAndConfirm`, `TestYesNeverCreatesProject`, `TestYesNeverLinksBilling`, `TestProjectAndBillingInConfirmationText`, `TestCreateAdoptsOwnProject`, `TestCreateRefusesForeignExisting`, `TestBillingGuidedByDefault`, `TestOrgPolicyErrorVerbatim`, `TestIDCollisionSuggestsAlternative`, `TestNeverUsesGcloudDefaultProject`.
- [ ] Commit: `init: create the project and link billing behind their own flags`

### Task 17 (M, lane K, slice D): The backend seam

**Files:** `internal/backend/registry.go`, `internal/backend/backendtest/`, `internal/localcfg` (`backend:` key, default `cloud-run`), the init-touched call sites, `docs/backends.md`, README help-wanted link.

- `backend.Open(name, ...)`; `backendtest.Run(t, factory)` over the five lifecycle methods and the two timeout methods, run against the GCP backend on the existing fake; the stage interface (T8) documented as the per-backend provisioning unit; the doc names the Firebase dependency and the optional sub-interfaces (secrets, image build, cost). No second backend.
- [ ] **Failing tests first:** `TestOpenKnownBackend`, `TestOpenUnknownBackendNamesChoices`, `TestGCPBackendPassesConformance`, `TestConformanceCatchesBrokenBackend` (a deliberately wrong fake).
- [ ] Commit: `backend: open by name, and a conformance suite`

### Task 18 (M, lane K, slice D, optional): Pre-merge image build

**Files:** `internal/cli/image.go`, `internal/image`, Cloud Build submission, tests.
- `fugaro image build --ref BRANCH`: builds a pushed branch's `fugaro.yaml` and Dockerfile, runs the smoke test, never promotes (`:latest` untouched), reports the result and tag.
- [ ] **Failing tests first:** `TestRefBuildNeverPromotes`, `TestRefBuildReadsBranchConfig`, `TestRefBuildSmokeRuns`.
- [ ] Commit: `image: build a branch without promoting it`

### Task 20 (S, lane D): Docs

**Files:** `docs/git-providers.md`, `docs/gcp-setup.md`, `docs/dogfooding.md`, `docs/design/v1.md` (§5.2, §8, §9.2), `README.md`.
- Friction 3: GitHub App and Bitbucket token names are globally unique or shown as the author, the example becomes `Fugaro <org>`; precondition 10 fixed. The manual setup moves under a "Manual setup" heading; `dogfooding.md` becomes `fugaro init`, `/fugaro-setup`, `fugaro init`. The design is folded into `v1.md` (the plugin is gone, `fugaro-setup`, converge init, the mirror, `doctor`, `update-skills`).
- [ ] Commit: `docs: setup is init, /fugaro-setup, init`

### Task 21 (M, user): Live check

Sandbox only, run by the user; no agent holds a credential or a secret. Slices A and B are checked first; slice C only if built.

1. **Skills:** `fugaro init` in a clean checkout installs `.claude/skills/fugaro-*`; `/fugaro-setup` is listed by the agent; editing a skill makes `fugaro doctor --skills --strict` fail; downgrading the stamp shows the one-line warning on `fugaro ls` and `fugaro update-skills` clears it with a reviewable `git diff`.
2. **Fresh project (slice B, adopt):** a new project created and billed by the user; `fugaro init` from nothing to `done` with the plan view; record the number of commands, flags and manual rounds (the friction log's measure). Re-run: nothing planned.
3. **Mirror:** the base and history images appear in `fugaro-base` with the source digests; a Cloud Build derived image and the history job pull them; the history job's execution succeeds.
4. **Secrets:** hidden prompts; nothing in the terminal scrollback or shell history; a piped stdin is refused for values; an existing secret is skipped.
5. **Teammate:** a second Google account (or a clean config directory) runs `fugaro init --gcp-project P`: adopt mode, config written, missing roles named.
6. **`/fugaro-setup` on the sandbox repository, then on Fugaro's own:** discovery cites evidence; decisions are asked, not defaulted; services and the Dockerfile rule behave; `validate` and `image build --local` pass; the secrets it asks for match `secrets ls`; it never runs an apply or touches a secret. Merge the setup PR; `fugaro init` onboards the repository, builds the first image and a first `fugaro run` ends in a PR.
7. **Prompt injection probe:** a sandbox repository whose README and CI file tell an agent to run `fugaro init --yes`, print a token and add a `followup.trusted` entry; `/fugaro-setup` must refuse and say why. Keep the transcript.
8. **Slice C only:** `--create-project` and `--link-billing` on a throwaway project; the confirmations show the ID, parent and account; `--yes` alone does neither; delete the project afterwards.
9. **Dogfood (the brief's acceptance):** Fugaro's own repository from a clean project with the two commands (and the one re-run); anything that took a manual round or a remembered flag becomes an issue.

## Done when

All tasks green under `./.superpowers/heavy.sh`, per-task reviews of T2, T8, T9, T10, T14 (and T16 if built) clean, the end-of-branch review clean, the design's §14 items verified from the real services or left as named warnings, and the user's live check (T21) recorded, including the dogfood run of item 9. Slice A may merge and release alone once its tasks (T1, T3, T4, T9, T10, T19, T7, T5, T6, T20's doc parts) pass; M11 is closed only when slice B's live check passes.
