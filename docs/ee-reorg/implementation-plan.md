# Governance, the cost report and the coordinator move under `ee/`: implementation plan

## Opening and status

This plan builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)** (which
wins), including the owner's correction of 2026-10-29: **the digest stays MIT**. Base: `origin/main`
at `028b46357`, 2026-10-28.

**Status: plan only. Nothing is implemented, and NO TASK IS APPROVED.** Each task needs the owner's
explicit go. Decisions D520–D534 are **unanswered**; every task names the decisions it assumes, and
an answer that differs from the recommendation re-plans the tasks that name it.

It **supersedes** subscriptions tasks S1.1, S1.2, S1.3, S1.4, S1.5, S1.7 and S1.9 (their content is
carried here, task by task, with the changes the ruling forces) and **depends on** S1.6 (carried as
E3.2). Every other subscriptions task stands. E0.1 marks the superseded tasks in
`docs/subscriptions/implementation-plan.md`.

The owner's answers that shape it (given, not re-asked):

- the CLI 0.13.0 tag is held until governance is under `ee/`;
- governance = budgets, tiers, tripwires, checkpoints, stage timeouts; `fugaro report` and the
  coordinator (command and library) move too (D434, D444); the digest stays MIT (T11, the 2026-10-29
  correction); the credentials service's audit log stays MIT;
- in-flight governance work finishes in `internal/` and moves **once** afterwards;
- T27 (b): paid commands warn, not refuse, until enforcement ships;
- one key-gated binary with the `ee` tag, relabelled (D447/T21).

## Conventions every part follows

- `CLAUDE.md`: targeted tests (`-run`), `gofmt -l .` empty, `go vet ./...`, `GOFLAGS=-p=2`;
  `internal/runner` and `internal/cli` are never run whole; comments say why; search before writing a
  helper; docs are tested (`GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`).
- `.claude/rules/ee.md` (as E0.2 amends it) for every `ee/**` file: the two-line notice
  (`SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise`, `See ee/LICENSE for the terms.`), a
  `<name>.license` sibling for JSON and other comment-less types; nothing outside `ee/` imports it
  except `cmd/fugaro/ee_on.go`; `go test ./internal/eeboundary/ -count=1` after every change. **No
  task edits a boundary test to admit a route** (owner's call).
- A **move** is `git mv` plus the notice plus the package path, in one commit, so `git log --follow`
  keeps the history; no behaviour change rides in a move commit.
- **Wire formats do not change** (R7): no field renamed, no schema in `schemas/` moved or tightened.
- `.claude/rules/console.md` for the one console-side check (E3.1 touches no console code).
- No agent edits `.github/workflows/`: workflow changes are staged under `docs/pending-workflows/`
  with their README section, and the owner copies them in (OWNER-RUN).
- Steps after a deploy go into the release notes' `### For operators`, labelled per installation or
  per repository.
- No price, currency figure or commercial term anywhere; no real person or organisation name; no
  secret-shaped literal; no key material.
- No live cloud action by an agent; Checks 200–201 are the owner's, sandbox only.

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | The core builds and its tests pass with `ee/` deleted | E1.6 (`core-without-ee.yml`, staged), every E2 task's untagged command |
| G2 | An untagged build decodes `governance:`, `tier`, `expected_scope` and warns; it never refuses them (D522, F9) | E1.2 `TestUntaggedRunnerIgnoresGovernanceWithAWarning`, E2.3 |
| G3 | An untagged `fugaro report` / `fugaro coordinator` exits non-zero with the Enterprise message, not "unknown command" (D522) | E1.1 `TestUntaggedPaidCommandSaysWhy` |
| G4 | A tagged paid command's notice is on stderr only; `--json` stdout parses (R4) | E2.1, E2.2 `TestNoticeNeverOnStdout` |
| G5 | The digest builds and runs untagged and inside a run (T11) | E2.2 `TestDigestNeedsNoEE` (S1.5's, kept) |
| G6 | `diagnose` and the digest read governance halts and checkpoints in an untagged build (D521, D529) | E1.2 `TestDiagnoseReadsACheckpointUntagged` |
| G7 | Spend caps, the 80% warning and the kill switch behave identically tagged and untagged (D520) | E2.3 `TestCapsUnchangedByTheTag` |
| G8 | The base and history images build `-tags ee`; the credentials image does not (D524) | E1.4 |
| G9 | A tagged image never carries a key or a secret (R6) | E1.4 `TestRunImagesCarryNoKey` |
| G10 | Hooks default to today's behaviour until the move flips them (no release in between loses governance) | E1.2, E2.3, E2.4 |

**Review focus.** Attack the seams: can an untagged build be made to load `ee/` code (a second
assembly file, an `init()` in a core package, a `-ldflags -X` that flips the edition)? Can a paid
command shadow a core one through the registry? Does any hook default silently drop enforcement in a
tagged build (a nil hook where a non-nil was registered)? Does the stage-timeout move change when a
stage halts? Does a core runner given a `governance:` block ever refuse a run?

## Slices and tasks

### Slice E0 — the documents (docs and pins; no behaviour)

#### E0.1 The subscriptions docs follow the ruling

- **Files:** `docs/subscriptions/reconciliation.md`, `docs/subscriptions/implementation-plan.md`,
  `docs/subscriptions/README.md`.
- **Change:** T27 recorded as (b) with today's date; the governance move added beside T10/T11; T11
  kept (the correction of 2026-10-29: nothing to reverse); R1 and R3 amended (D524; R3 extends to
  v0.12.1); S1.1–S1.5, S1.7, S1.9 marked superseded by this plan's E-tasks, numbers not reused.
- **Tests:** the Docs suite. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (docs). **Depends on:** the owner's answers to D520–D534. **Reviewer:** standard.
- **Commit:** `docs(subscriptions): T27 (b), the governance move, R1 amended (ee-reorg D524)`.

#### E0.2 `ee/` admits moved code

- **Files:** `ee/README.md`, `.claude/rules/ee.md`, `internal/cli/docs_ee_reserved_test.go`,
  `internal/cli/docs_ee_repo_test.go` if a reader list changes.
- **Change:** D532's text; the rule "Nothing moves out of the core into `ee/` (D302)" replaced by the
  rulings (D434, D444, this ruling); `TestDocsEELicensed` admits code under the notice rule (the
  subscriptions S0.6 change, folded in here). The three rules stay word for word.
- **Tests:** `TestDocsEELicensed`; `go test ./internal/eeboundary/ -count=1`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsEE' -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** the new README sentence fails the old pin until the pin changes with it.
- **Depends on:** D532. **Reviewer:** opus + mandatory independent review (licence text).
- **Commit:** `ee: the folder holds moved features too; released MIT versions stay MIT (D532)`.

#### E0.3 The design: the moves file by file, and the hooks

- **Files:** `docs/design/ee-boundary-moves.md` (new).
- **Content:** everything subscriptions S1.1 listed for the coordinator and the report, plus:
  - the `report.USD6` gap in the digest's MIT copy (F6) and `consoleReadPaths` (F6);
  - the governance split of D521, symbol by symbol from reconciliation F7: which types stay, which
    functions move to `ee/governance`, and the four hooks of R2 with their core defaults;
  - the untagged behaviour table of D522 and the notice of D523;
  - the edition marker's carrier (R3: an OCI label read by the gate, or a field in the build record);
  - the images (D524) and why the credentials image stays untagged;
  - which command tree `website/content/cli-reference.md` and the plugin tests read: the tagged one
    (one binary ships, T21);
  - the helper API an `ee/` command needs from `internal/cli` (S1.1's list).
- **Tests:** the Docs suite. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design). **Depends on:** D520–D524, D528, D530. **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `docs(design): the core-to-ee moves: coordinator, report, governance`.

#### E0.4 The task-governance and coordinator plans re-target `ee/`

- **Files:** `docs/task-governance/implementation-plan.md`, `docs/task-governance/README.md`,
  `docs/coordinator/README.md`, `docs/coordinator/implementation-plan.md`.
- **Change:** the status lines brought up to date (T3.3, T3.5, T7.6, T8.1a merged); the remaining
  governance tasks (T7.2–T7.5, T8.2, T8.3's calibration half) re-targeted to `ee/governance` behind
  the hooks of E1.2 (T7.2 adds the gateway stage-limit hook as its first step, opus + mandatory
  review); T8.3's `ExpectedScope` drift flag stays in `internal/digest` (D528); the coordinator's
  remaining parts point at `ee/coordinator` (S1.8 already re-plans P6–P8; this adds only the path).
- **Tests:** the Docs suite (the coordinator docs tests pin those READMEs). **Command:** as E0.3.
- **Red step:** none. **Depends on:** E0.3. **Reviewer:** standard.
- **Commit:** `docs(plans): the rest of governance is born in ee/ (D527)`.

### Slice E1 — the core seams (inert: behaviour unchanged)

**Gate before E1.2:** #1216 (T7.6 follow-up), #1215 (T3.3 follow-up), #1196 (`report --by mode`)
and T8.1b have merged or been closed (D527). No task in this plan opens against their files before.

#### E1.1 A command registry, the launch hook and the paid-command stubs

- **Files:** `internal/cli/extend.go` (new), `internal/cli/extend_test.go` (new), `internal/cli/api.go`
  (new; the exported helpers E0.3 names), `internal/cli/root.go`, `internal/cli/run.go`.
- **Interfaces:** produces `RegisterCommand`, a launch-declarer hook (default: declare nothing),
  and a core stub for each paid command name (`report`, `coordinator`) that a registration replaces;
  refuses a name a core command holds.
- **Tests:** S1.2's six (`TestRegisteredCommandJoinsTheRoot`, `TestRegisteredCommandCannotShadowACoreCommand`,
  `TestRegisteredCommandCannotReplaceCoreSubcommandsOrPersistentFlags`, `TestLateOrDuplicateRegistrationIsRefused`,
  `TestNoRegistrationLeavesTheRootUnchanged`, `TestLaunchWithoutADeclarerDeclaresNothing`) plus
  `TestUntaggedPaidCommandSaysWhy` and `TestStubIsReplacedOnlyByItsRegistration`.
- **Command:** `go test ./internal/cli/ -run 'TestRegistered|TestNoRegistration|TestLateOrDuplicate|TestLaunchWithoutADeclarer|TestUntaggedPaid|TestStubIs' -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** the shadowing test fails with no registry.
- **Depends on:** E0.3. **Reviewer:** opus + mandatory independent review.
- **Commit:** `cli: a command registry, a launch hook and the paid-command stubs`.

Note: while `report` and `coordinator` are still core, `root.go` keeps adding them and the stub is
not installed for a name a core command holds (E2.1/E2.2 delete the core command and the stub
takes over in untagged builds).

#### E1.2 Governance hooks with today's behaviour as their default

- **Files:** `internal/policy/hook.go` (new), `internal/policy/policy.go`, `internal/policy/merge.go`,
  `internal/runner/governance_hook.go` (new), `internal/runner/runner.go`, `internal/runner/halt.go`,
  `internal/digest/section.go` (new), `internal/digest/render.go`, tests beside each.
- **Interfaces:** produces three hooks (R2): the governance policy hook (defaults and the
  tighten-only merge of the `governance:` block), the runner stage hook (the stage timeout and the
  decision to call `governanceHalt`), and the digest section hook (extra sections, none by default).
  **Defaults:** the policy and runner hooks default to the current `internal/` implementation, so
  nothing changes until E2.3/E2.4 move it; a `SetCore()` test seam proves the warn-and-ignore path
  the untagged build will take.
- **Tests:** `TestHooksDefaultToTodaysBehaviour`, `TestUntaggedRunnerIgnoresGovernanceWithAWarning`
  (through the seam), `TestDiagnoseReadsACheckpointUntagged`, `TestDigestHasNoExtraSectionByDefault`,
  the existing `TestStageTimeout*` unchanged.
- **Command:** `GOFLAGS=-p=2 go test ./internal/policy ./internal/digest -count=1 && GOFLAGS=-p=2 go test ./internal/runner -run 'StageTimeout|GovernanceHook|Untagged' -count=1 -timeout 30m && go test ./internal/cli/ -run 'TestDiagnose' -count=1`.
- **Red step:** `TestUntaggedRunnerIgnoresGovernanceWithAWarning` fails: today a stage timeout halts
  regardless.
- **Depends on:** E0.3 and the gate above. **Reviewer:** opus + mandatory independent review (the run
  path).
- **Commit:** `governance: hooks for the policy, the stage and the digest, defaulting to today`.

#### E1.3 The edition marker and the gates' warning

- **Files:** `internal/cli/edition.go` (new), `internal/cli/edition_test.go` (new),
  `internal/cli/recipes_skew.go` (`checkGovernanceImage`, `refuseGovernanceSkew`,
  `governanceImageWarnings` learn the edition), plus the carrier E0.3 chooses on the Go side.
- **Tests:** `TestEditionIsCoreUntagged`, `TestGovernanceOnACoreImageWarns`,
  `TestGovernanceOnAnOldImageStillRefuses` (today's skew rule unchanged), `TestEditionCannotBeSetByLdflags`.
- **Command:** `go test ./internal/cli/ -run 'TestEdition|TestGovernanceOn' -count=1`.
- **Red step:** `TestGovernanceOnACoreImageWarns` fails: today a 0.13.0 core image passes silently.
- **Depends on:** E0.3. **Reviewer:** standard.
- **Commit:** `cli: the binary knows its edition, and governance on a core image warns (R3)`.

#### E1.4 The base and history images build with the tag; credentials does not

- **Files:** `images/base/Dockerfile`, `images/history/Dockerfile` (`COPY ee ./ee`, `-tags ee`, the
  edition carrier), `images/build_tags_test.go` (new; S1.7's scan, inverted for base and history),
  `images/base_test.go`, `images/history_test.go`, `images/size-budget.sh` only if U3 needs it; a
  staged change under `docs/pending-workflows/images.yml` with its README section if U1 shows the
  workflow needs one.
- **Tests:** `TestBaseAndHistoryBuildFugaroWithTheEETag`, `TestCredentialsImageBuildsWithoutTheEETag`,
  `TestBuildTagScanCatchesPlants`, `TestRunImagesCarryNoKey` (no `COPY` or `ARG` of a key or secret).
- **Command:** `go test ./images/ -run 'TestBaseAndHistory|TestCredentialsImage|TestBuildTagScan|TestRunImagesCarryNoKey|TestHistory' -count=1`.
- **Red step:** `TestBaseAndHistoryBuildFugaroWithTheEETag` fails on today's Dockerfiles.
- **Depends on:** E0.3, D524, D525. **Reviewer:** opus + mandatory independent review (licence and
  supply chain).
- **For operators (0.13.0 notes, E3.3):** per installation, refresh the images.
- **Commit:** `images: base and history carry the ee build; credentials stays core (D524)`.

#### E1.5 `ee/register` and the assembly file

- **Files:** `ee/register/register.go` (new; sets the edition), `ee/register/register_test.go` (new),
  `cmd/fugaro/ee_on.go` (new).
- **Tests:** `TestRegisterFollowsTheGatingModel` (T27 (b): register, notice on stderr), `go build
  -tags ee ./cmd/fugaro`, the boundary tests tagged and untagged.
- **Command:** `go test ./internal/eeboundary/ -count=1 && go test -tags ee ./ee/register/ -count=1 && go build -tags ee ./cmd/fugaro && go build ./cmd/fugaro`.
- **Red step:** `TestRegisterFollowsTheGatingModel` fails before the package exists.
- **Depends on:** E1.1, E1.3, E0.2. The boundary tests already admit exactly this route; no
  boundary-test edit. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the register package and the one assembly file`.

#### E1.6 CI builds and tests both editions (staged)

- **Files:** `docs/pending-workflows/ci.yml` (new staged copy: `go vet -tags ee ./...`, `go test
  -tags ee ./ee/... -count=1`, `go build -tags ee ./cmd/fugaro`, beside the untagged jobs),
  `docs/pending-workflows/README.md` (its section, and that `core-without-ee.yml` is now due),
  `internal/cli/docs_core_without_ee_workflow_test.go` if the staged file's pinned lines change.
- **Tests:** the pin tests for the staged workflows. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Workflow|Docs' -count=1`.
- **Red step:** a pin for the tagged job fails until the staged file carries it.
- **OWNER-RUN:** the owner copies `docs/pending-workflows/ci.yml` and `core-without-ee.yml` into
  `.github/workflows/` (`cp docs/pending-workflows/{ci,core-without-ee}.yml .github/workflows/`, commit,
  push). No agent performs it.
- **Depends on:** E0.3. **Reviewer:** standard.
- **Commit:** `ci(staged): build and test the ee edition beside the core`.

### Slice E2 — the moves (one per feature; behaviour unchanged in the tagged build)

#### E2.1 The coordinator, command and library (D434, T10; was S1.4)

- **Files:** S1.4's list (moved `internal/coordinator/` → `ee/coordinator/`, the four command and
  test files → `ee/coordinator/cli/`, the four test-data files with `.license` siblings, a copy of
  `coordinator_token.json`; new `internal/coordpath/`; re-pointed `internal/cli/run.go` is **not**
  touched here (the hook is E1.1's): `internal/cli/project_cmd.go`, `internal/budget/rules/coordinator_test.go`,
  `emulator_coordinator_test.go`, `rules_test.go`, `internal/cli/doctor_policy_test.go`,
  `internal/cli/docs_desktop_console_test.go`), plus `ee/register/coordinator.go` (new),
  `internal/cli/root.go`, `website/content/cli-reference.md` (regenerated), `docs/coordinator/README.md`.
- **Tests:** `TestUntaggedBuildStillLaunches`, `TestCoordinatorCommandRegistersTagged`,
  `TestNoticeNeverOnStdout` (coordinator status `--json`), the moved tests, the rules tests against
  `internal/coordpath`.
- **Command:** `go run ./internal/clidoc && go test ./internal/cli/ -run 'TestUntaggedBuild|TestDocsDesktop|TestUntaggedPaid' -count=1 && go test ./internal/budget/rules/ -run Coordinator -count=1 && go test -tags ee ./ee/coordinator/... ./ee/register/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestCoordinatorCommandRegistersTagged` fails while the command is core.
- **Depends on:** E1.5, E0.4 (`docs/coordinator/README.md`). **Reviewer:** opus + mandatory independent review.
- **Commit:** `coordinator: the command and the library move to ee/ (D434, T10)`.

#### E2.2 `fugaro report` moves; the digest keeps its MIT copy (D444, T11; was S1.5)

- **Files:** S1.5's list (moved `internal/report/` → `ee/report/`, the command part of
  `internal/cli/report.go` and `report_test.go` → `ee/report/cli/`; new `internal/cli/reportcore.go`;
  new `internal/digest/period.go` with `Dim`, `ParseDim`, `PeriodKey`, the period keys, `USD` **and
  `USD6`** (F6); `internal/digest/digest.go`, `layout.go`, `patterns.go`, `render.go`,
  `internal/runner/digest.go`, `internal/cli/runs_digest.go` re-pointed), plus `internal/cli/project.go`
  (`consoleReadPaths`), `ee/register/report.go` (new), `internal/cli/root.go`,
  `internal/cli/docs_desktop_console_test.go`, `website/content/cli-reference.md`,
  `plugin/skills/working/SKILL.md` (`fugaro report` named as a business-licensed command; the digest
  as free).
- **Tests:** `TestDigestNeedsNoEE` (an untagged build writes a digest, and `internal/digest` imports
  nothing under `ee/` or `internal/report`), `TestReportCommandRegistersTagged`,
  `TestNoticeNeverOnStdout` (`report --json`), the moved tests, the plugin's command checks against
  the tagged tree.
- **Command:** `go run ./internal/clidoc && go test ./internal/digest/ -count=1 && go test ./internal/cli/ -run 'TestDigest|TestRunsDigest|TestDocsDesktop|TestReportSource' -count=1 && GOFLAGS=-p=2 go test ./internal/runner -run Digest -count=1 -timeout 30m && go test -tags ee ./ee/report/... -count=1 && go test ./plugin/... -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestDigestNeedsNoEE` fails while `internal/digest` imports `internal/report`.
- **Depends on:** E2.1 (`root.go`, `docs_desktop_console_test.go`, `cli-reference.md`). **Reviewer:**
  opus + mandatory independent review.
- **Commit:** `report: fugaro report moves to ee/; the digest stays MIT (D444, T11)`.

#### E2.3 Governance policy and defaults move

- **Files:** moved: `DefaultGovernance`, `DefaultTierExpectations`, `tightenGovernance`,
  `tightenTier` and their tests (`internal/policy/governance_test.go`, `tier_test.go`) →
  `ee/governance/policy.go`, `ee/governance/policy_test.go`; kept and re-pointed:
  `internal/policy/policy.go`, `internal/policy/merge.go` (call the hook), `internal/policy/hook.go`
  (core default becomes "decode, warn, ignore"); new `ee/register/governance_policy.go`.
- **Tests:** `TestGovernanceTightenOnly`, `TestGovernanceDefaults` (moved, tagged),
  `TestCapsUnchangedByTheTag` (G7), `TestUntaggedPolicyIgnoresGovernance`.
- **Command:** `go test ./internal/policy ./internal/config -count=1 && go test -tags ee ./ee/governance/ ./ee/register/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestUntaggedPolicyIgnoresGovernance` fails while the default is today's.
- **Depends on:** E1.2, E1.5. **Reviewer:** opus + mandatory independent review.
- **Commit:** `governance: the policy and defaults move to ee/ (D521)`.

#### E2.4 Governance in the run: the stage timeout and the halts move

- **Files:** moved: `stageTimeout` and the governance-halt decision from `internal/runner/runner.go`,
  the stage-timeout tests (`internal/runner/stage_timeout_test.go` and the internal one #1216 adds) →
  `ee/governance/runner.go`, `ee/governance/runner_test.go`; kept: `governanceHalt`, `checkpointPR`,
  `checkpointReportPart` (the plumbing, D521); `internal/runner/governance_hook.go` (core default
  becomes "ignore, warn once in the run log"); new `ee/register/governance_runner.go`.
- **Tests:** `TestStageTimeoutHaltsWithCheckpoint`, `TestStageTimeoutNeverAboveTimeoutsStage`
  (moved, tagged, through the hook), `TestUntaggedRunnerIgnoresGovernanceWithAWarning` (now the real
  default), `TestTaggedRunnerHaltsAsBefore`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'StageTimeout|GovernanceHook|Untagged|Halt' -count=1 -timeout 30m && go test -tags ee ./ee/governance/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestTaggedRunnerHaltsAsBefore` fails until the ee implementation registers.
- **Depends on:** E1.2, E1.5. **Reviewer:** opus + mandatory independent review (the run path).
- **Commit:** `governance: the stage timeout and the halts move to ee/ (D521)`.

### Slice E3 — docs, the relabel and the 0.13.0 release

#### E3.1 Docs, skills and pins say which parts are business-licensed

- **Files:** `plugin/skills/working/reference/diagnose.md` (governance halts are an Enterprise
  feature; reading them is not), `plugin/skills/routing/SKILL.md` (the 0.13.0 gate names the tagged
  image), `docs/project-layer.md` (the governance rows marked business-licensed; `TestScopeTableMatchesDocs`
  keeps its rows), `docs/recipes.md` (`run-digest` stays free), `docs/release.md` (the checklist
  verifies the tag on the release and on the base and history images), `LICENSING.md` (governance,
  the report and the coordinator listed under `ee/`).
- **Tests:** `TestScopeTableMatchesDocs`, the plugin lint, the Docs suite.
- **Command:** `go test ./internal/config -run TestScopeTableMatchesDocs -count=1 && go test ./plugin/... -count=1 && GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** a `LICENSING.md` pin naming the moved features fails before the text.
- **Depends on:** E2.2, E2.3, E2.4. **Reviewer:** standard.
- **Commit:** `docs: governance, the report and the coordinator are business-licensed`.

#### E3.2 The single mixed-licence release and its relabel (D447/T21; was S1.6)

- **Files:** S1.6's list: `.goreleaser.yaml` (`-tags ee`, the cask's licence), every label
  `TestEETagNeedsTheLabels` reads, `README.md`, the website's licence wording
  (`website/src/pages/community.astro`, `website/test/faq-community.mjs`), `docs/releases/README.md`.
  `LICENSING.md` is E3.1's.
- **Tests:** `TestEETagNeedsTheLabels`, `TestGoreleaserReleaseBuild`,
  `TestThirdPartyNoticesUnionsTheBuildMatrix` (the tagged build), `faq-community.mjs`.
- **Command:** `go test ./scripts/ -run 'TestEETagNeedsTheLabels|TestGoreleaserReleaseBuild|TestThirdPartyNotices' -count=1 && cd website && npm run check`.
- **Red step:** set the tag first; `TestEETagNeedsTheLabels` fails until every label changes.
- **Depends on:** E3.1, E1.4, E1.6 (the owner's copy done), O1 (the owner and counsel on shipping
  under the current `ee/LICENSE`). **Reviewer:** opus + mandatory independent review.
- **Commit:** `release: one binary, MIT core plus business-licensed parts (D447, T21)`.

#### E3.3 The 0.13.0 notes

- **Files:** `docs/releases/v0.13.0.md` (new, or the next-notes file the release process uses),
  `website/data/release-summaries.yaml` (one line, no backticks).
- **Content:** the Licensing highlight (D533); what 0.12.x users keep (R1); `### For operators`:
  per installation, `fugaro image refresh` (or `fugaro update --base base`) so jobs run the tagged
  base and history images; per repository, nothing; a source build without the tag ignores
  `governance:` with a warning.
- **Tests:** the release-notes pins. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Release' -count=1 && go test ./scripts/ -run TestRelease -count=1`.
- **Red step:** none (notes). **Depends on:** E3.1. **Reviewer:** standard.
- **Commit:** `docs(releases): 0.13.0, the first release with business-licensed parts`.

#### E3.4 Checks 200 and 201 (written, then OWNER-RUN)

- **Files:** `docs/gcp-live-checklist.md` (two sections and their index rows), `docs/coordinator/README.md`
  (register line).
- **Check 200:** on the sandbox installation with the released 0.13.0 and refreshed images, a run
  with `governance.stage_timeout.implement` set tiny halts with `stage_budget` and a checkpoint
  report; `fugaro version` and the image's edition carrier say `ee`; `fugaro runs digest --period
  day` runs from a source build without the tag.
- **Check 201:** the console's Spend and project views against the released 0.13.0 CLI show data,
  and the stderr notice does not break them.
- **OWNER-RUN:** the owner runs both in the sandbox repository only (`fugaro run --task-file
  <task>`, `fugaro runs ls`, `fugaro diagnose RUN`; the console pointed at the installed CLI). No
  agent performs any step.
- **Depends on:** the 0.13.0 tag. **Reviewer:** standard.
- **Commit:** `docs(checks): 200 and 201, the first tagged release`.

## Parallel execution

### Edges

E0.1 ← decisions. E0.2 ← D532. E0.3 ← decisions. E0.4 ← E0.3.
E1.1, E1.3, E1.4, E1.6 ← E0.3. E1.2 ← E0.3 + the in-flight gate. E1.5 ← E1.1, E1.3, E0.2.
E2.1 ← E1.5, E0.4. E2.2 ← E2.1. E2.3 ← E1.2, E1.5. E2.4 ← E1.2, E1.5.
E3.1 ← E2.2, E2.3, E2.4. E3.2 ← E3.1, E1.4, E1.6 (owner's copy), O1. E3.3 ← E3.1.
The 0.13.0 tag ← E3.2, E3.3. E3.4 ← the tag.

### Levels

| Level | Tasks (run concurrently) |
| --- | --- |
| **L0** | E0.1, E0.2, E0.3 |
| **L1** | E0.4, E1.1, E1.2 (after the in-flight gate), E1.3, E1.4, E1.6 |
| **L2** | E1.5 |
| **L3** | E2.1, E2.3, E2.4 |
| **L4** | E2.2 |
| **L5** | E3.1 |
| **L6** | E3.2, E3.3 |
| **after the tag** | E3.4 (written in L6's wake, run by the owner) |

**Critical path:** decisions → E0.3 → E1.1 → E1.5 → E2.1 → E2.2 → E3.1 → E3.2 → 0.13.0 tag →
Check 200. Off it: the in-flight gate (it delays E1.2 → E2.3/E2.4, which join at E3.1), the owner's
workflow copy (E1.6) and O1 (E3.2).

**Sequential by necessity:** E2.1 → E2.2 (both edit `internal/cli/root.go`,
`internal/cli/docs_desktop_console_test.go`, `website/content/cli-reference.md`); E1.1 → E1.5 (the
registry's interface); E3.1 → E3.2 (the labels read `LICENSING.md`).

### Shared files (no level has two writers of one file)

| File | Writers (level) |
| --- | --- |
| `internal/cli/root.go` | E1.1 (L1), E2.1 (L3), E2.2 (L4) |
| `internal/cli/recipes_skew.go` | E1.3 (L1) |
| `internal/cli/docs_desktop_console_test.go` | E2.1 (L3), E2.2 (L4) |
| `website/content/cli-reference.md` | E2.1 (L3), E2.2 (L4) |
| `internal/policy/policy.go`, `merge.go`, `hook.go` | E1.2 (L1), E2.3 (L3) |
| `internal/runner/runner.go`, `halt.go`, `governance_hook.go` | E1.2 (L1), E2.4 (L3) |
| `internal/digest/*.go` | E1.2 (L1: `section.go`, `render.go`), E2.2 (L4) |
| `ee/register/*.go` | E1.5 (L2: `register.go`), E2.1 (L3: `coordinator.go`), E2.3 (L3: `governance_policy.go`), E2.4 (L3: `governance_runner.go`), E2.2 (L4: `report.go`) — one file per writer |
| `ee/governance/` | E2.3 (L3: `policy*.go`), E2.4 (L3: `runner*.go`) — distinct files, one package |
| `docs/coordinator/README.md` | E0.4 (L1), E2.1 (L3), E3.4 (after the tag) |
| `LICENSING.md` | E3.1 (L5) |
| `README.md` | E3.2 (L6) |
| `plugin/skills/working/SKILL.md` | E2.2 (L4) |
| `docs/pending-workflows/README.md` | E1.6 (L1); E1.4 (L1) only if U1 needs a staged `images.yml` — **then E1.4 waits for E1.6** |
| `ee/README.md`, `.claude/rules/ee.md`, `internal/cli/docs_ee_reserved_test.go` | E0.2 (L0) |
| `docs/subscriptions/*` | E0.1 (L0) |
| `docs/design/ee-boundary-moves.md` | E0.3 (L0) |

## Checks this plan takes

| Check | Measures | Written by | Run by |
| --- | --- | --- | --- |
| **200** | Governance halts in a job from the tagged image; the edition reads `ee`; the digest runs from an untagged source build | E3.4 | the owner, sandbox only |
| **201** | The console's Spend and project views work against the tagged CLI with its stderr notice | E3.4 | the owner |

**202–209** stay reserved and unused. Subscriptions Check 178 (the coordinator's key-gated job) is
not needed while T27 (b) holds and no key is mounted (D524); E0.1 marks it deferred.

## Release actions

- **0.13.0, per installation:** refresh the images so jobs run the tagged base and history images
  (E3.3). Without it an installation on a 0.12.x image keeps refusing `governance:` as it does
  today.
- No backfill, no secret, no Terraform change.

## Risks

- **The hold lengthens with the gate.** E1.2 waits on #1216, #1215, #1196 and T8.1b; a slow review
  there delays governance's moves but not the coordinator's or the report's (E2.1, E2.2 do not wait
  on it).
- **A hook default lost in a merge** would turn governance off in the tagged build silently; G10 and
  `TestTaggedRunnerHaltsAsBefore` pin it, and Check 200 measures it live.
- **Image size** (U3) and the images workflow (U1) are unverified; E1.4 records both.
