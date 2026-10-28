# Governance, the cost report and the coordinator move under `ee/`: implementation plan

## Opening and status

This plan builds [spec.md](spec.md) **as amended by [reconciliation.md](reconciliation.md)** (which
wins), including the owner's correction of 2026-10-29: **the digest stays MIT**. Base: `origin/main`
at `028b46357`, 2026-10-28.

**Status: plan only. Nothing is implemented, and NO TASK IS APPROVED.** Each task needs the owner's
explicit go. Decisions D520, D521 and D524–D527 are **answered** (2026-10-29: the recommendations taken; D526's O1 stays open and the 0.13.0 tag stays held until counsel answers); the rest of D520–D536 are **unanswered**; every task names the decisions it assumes, and
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
- **Seams that switch behaviour off are test-only:** a `SetCore()`-style seam lives in an
  `export_test.go`, never in production code, so no core caller can turn governance off in a tagged
  binary; the edition is set once, by `ee/register`, through a setter that refuses a second call.
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
| G2 | An untagged runner (a core job image) decodes `governance:`, `tier`, `expected_scope`, warns in the run log and never refuses them; launch-side warnings follow the job image's edition, never the launcher's (D522, R3, F9) | E1.2 `TestUntaggedRunnerIgnoresGovernanceWithAWarning`, E1.3 `TestGovernanceOnACoreImageWarns`, `TestTaggedImageFromAnUntaggedLauncherDoesNotWarn` |
| G3 | An untagged `fugaro report` / `fugaro coordinator` exits non-zero with the Enterprise message, not "unknown command" (D522) | E1.1 `TestUntaggedPaidCommandSaysWhy` |
| G4 | A tagged paid command's notice is on stderr only; `--json` stdout parses (R4) | E2.1, E2.2 `TestNoticeNeverOnStdout` |
| G5 | The digest builds and runs untagged and inside a run (T11) | E2.2 `TestDigestNeedsNoEE` (S1.5's, kept) |
| G6 | `diagnose` and the digest read governance halts and checkpoints in an untagged build (D521, D529) | E1.2 `TestDiagnoseReadsACheckpointUntagged` |
| G7 | Spend caps, the 80% warning and the kill switch behave identically tagged and untagged (D520) | E2.3 `TestCapsUnchangedByTheTag` |
| G8 | The base and history images build `-tags ee`; the credentials image does not (D524) | E1.4 |
| G9 | A tagged image never carries a key or a secret (R6) | E1.4 `TestRunImagesCarryNoKey` |
| G10 | Hooks default to today's behaviour until the move flips them (no release in between loses governance), and a tagged binary has every hook set | E1.2, E2.3, E1.5 `TestTaggedBinaryWires…` |
| G11 | No public image carries `ee/` code before the relabel; the label gate covers the image builds (F16c) | E3.2 (gate extended), E1.4 after E3.2 |

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
  v0.12.1); S1.1–S1.5, S1.7, S1.9 marked superseded by this plan's E-tasks, numbers not reused;
  S1.10 (Check 178) marked deferred until enforcement; S1.8 stands.
- **Tests:** the Docs suite. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (docs). **Depends on:** the owner's answers to D520–D536. **Reviewer:** standard.
- **Commit:** `docs(subscriptions): T27 (b), the governance move, R1 amended (ee-reorg D524)`.

#### E0.2 `ee/` admits moved code

- **Files:** `ee/README.md`, `.claude/rules/ee.md`, `internal/cli/docs_ee_reserved_test.go`,
  `internal/cli/docs_licensing_test.go` (it pins "reserved" and "not published yet" in
  `ee/README.md`), `internal/cli/docs_ee_repo_test.go` if a reader list changes.
- **Change:** D532's text; the rule "Nothing moves out of the core into `ee/` (D302)" replaced by the
  rulings (D434, D444, this ruling); `TestDocsEELicensed` admits code under the notice rule (the
  subscriptions S0.6 change, folded in here). The three rules stay word for word.
- **Tests:** `TestDocsEELicensed`, the `docs_licensing_test.go` pins; `go test ./internal/eeboundary/ -count=1`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestDocsEE|TestDocsLicensing' -count=1 && go test ./internal/eeboundary/ -count=1`.
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
  - which command tree `website/content/cli-reference.md` and the plugin tests read: the tagged one,
    through `ee/clidoc` (D536, built by E1.7);
  - `reportHistory` returns `[]report.Rec` and only `report.go` calls it, so it moves with the
    command, not into `reportcore.go` (S1.5's list corrected);
  - the helper API an `ee/` command needs from `internal/cli` (S1.1's list).
- **Tests:** the Docs suite. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (design). **Depends on:** D520–D524, D528, D530, D536. **Reviewer:** opus + mandatory
  independent review.
- **Commit:** `docs(design): the core-to-ee moves: coordinator, report, governance`.

#### E0.4 The task-governance and coordinator plans re-target `ee/`

- **Files:** `docs/task-governance/implementation-plan.md`, `docs/task-governance/README.md`.
- **Change:** the status lines brought up to date (T3.3, T3.5, T7.6, T8.1a merged); the remaining
  governance tasks (T7.2–T7.5, T8.2, T8.3's calibration half) re-targeted to `ee/governance` behind
  the hooks of E1.2 (T7.2 adds the gateway stage-limit hook as its first step, opus + mandatory
  review); T8.3's `ExpectedScope` drift flag stays in `internal/digest` (D528). The coordinator's
  plan is re-pointed by E2.1, not here: `TestDocsCoordinatorPlanCitationsExist` needs every path it
  cites to exist, so its `internal/coordinator` citations change in the commit that moves the code.
- **Tests:** the Docs suite. **Command:** as E0.3.
- **Red step:** none. **Depends on:** E0.3. **Reviewer:** standard.
- **Commit:** `docs(plans): the rest of governance is born in ee/ (D527)`.

### Slice E1 — the core seams (inert: behaviour unchanged)

**The in-flight gate (D527):** #1216 (T7.6 follow-up), #1215 (T3.3 follow-up), #1196 (`report --by
mode`) and T8.1b have merged or been closed. It is an edge into every task that edits their files:
E1.1 and E1.3 (T8.1b edits `internal/cli/run.go`), E1.2 and E2.3 (#1216: `internal/runner/runner.go`,
`halt.go`), E2.2 (#1196: `internal/cli/report.go`, the working skill; #1215: `internal/runner/digest.go`).

#### E1.1 A command registry, the launch hook and the paid-command stubs

- **Files:** `internal/cli/extend.go` (new), `internal/cli/extend_test.go` (new), `internal/cli/api.go`
  (new; the exported helpers E0.3 names), `internal/cli/root.go`, `internal/cli/run.go`.
- **Interfaces:** produces `RegisterCommand`, a launch-declarer hook whose core default is today's
  declarer (`coordinator.Reporter{…}.Report`, F16a) until E2.1 moves it and the default becomes
  "declare nothing" (D535),
  and a core stub for each paid command name (`report`, `coordinator`) that a registration replaces;
  refuses a name a core command holds.
- **Tests:** S1.2's six (`TestRegisteredCommandJoinsTheRoot`, `TestRegisteredCommandCannotShadowACoreCommand`,
  `TestRegisteredCommandCannotReplaceCoreSubcommandsOrPersistentFlags`, `TestLateOrDuplicateRegistrationIsRefused`,
  `TestNoRegistrationLeavesTheRootUnchanged`, `TestLaunchWithoutADeclarerDeclaresNothing`, run against a seam) plus
  `TestStubSaysWhyThroughASeam` (a test-only stub name, since `report` and `coordinator` are still
  core commands here; the real `TestUntaggedPaidCommandSaysWhy` is E2.1's and E2.2's),
  `TestStubIsReplacedOnlyByItsRegistration` and `TestLaunchStillDeclaresBeforeTheMove`.
- **Command:** `go test ./internal/cli/ -run 'TestRegistered|TestNoRegistration|TestLateOrDuplicate|TestLaunchWithoutADeclarer|TestLaunchStillDeclares|TestStubSaysWhy|TestStubIs' -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** the shadowing test fails with no registry.
- **Depends on:** E0.3, the in-flight gate. **Reviewer:** opus + mandatory independent review.
- **Commit:** `cli: a command registry, a launch hook and the paid-command stubs`.

Note: while `report` and `coordinator` are still core, `root.go` keeps adding them and the stub is
not installed for a name a core command holds (E2.1/E2.2 delete the core command and the stub
takes over in untagged builds).

#### E1.2 Governance hooks with today's behaviour as their default

- **Files:** `internal/policy/hook.go` (new), `internal/policy/policy.go`, `internal/policy/merge.go`,
  `internal/runner/governance_hook.go` (new), `internal/runner/runner.go`, `internal/runner/halt.go`,
  `internal/digest/section.go` (new), `internal/digest/render.go`, tests beside each, and
  `internal/cli/diagnose_test.go` (the untagged checkpoint read), and `internal/runner/runnertest/`
  (new, exported: the minimal stage harness `ee/governance` tests need, since the runner's own
  `_test` harness cannot be imported from `ee/`).
- **Interfaces:** produces three hooks (R2): the governance policy hook (defaults and the
  tighten-only merge of the `governance:` block), the runner stage hook (the stage timeout and the
  decision to call `governanceHalt`), and the digest section hook (extra sections, none by default).
  **Defaults:** the policy and runner hooks default to the current `internal/` implementation, so
  nothing changes until E2.3 move it; a `SetCore()` test seam proves the warn-and-ignore path
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
  `internal/cli/recipes_skew.go` (`checkGovernanceImage`, `checkCloneGovernanceImage`,
  `refuseGovernanceSkew` and `governanceImageWarnings` learn the job image's edition; the last gains
  the image it judges), `internal/cli/validate.go` (its call), plus the carrier E0.3 chooses on the Go
  side.
- **Tests:** `TestEditionIsCoreUntagged`, `TestGovernanceOnACoreImageWarns`,
  `TestTaggedImageFromAnUntaggedLauncherDoesNotWarn`, `TestCloneWorkflowOnACoreImageWarns`,
  `TestValidateWarnsGovernanceOnACoreImage`,
  `TestTierOnACoreImageWarns` (`--tier` judged through `governanceLaunchSources`, so `run.go` is not
  touched), `TestGovernanceOnAnOldImageStillRefuses` (today's skew rule unchanged),
  `TestEditionHasNoLdflagsRoute` (a source scan: the edition is not a package-level string `-X` can
  set, and no build file passes `-X` for it).
- **Command:** `go test ./internal/cli/ -run 'TestEdition|TestGovernanceOn|TestTaggedImageFrom|TestCloneWorkflowOnACore|TestValidateWarnsGovernance|TestTierOnACore|TestEditionHasNo' -count=1`.
- **Red step:** `TestGovernanceOnACoreImageWarns` fails: today a 0.13.0 core image passes silently.
- **Depends on:** E0.3, the in-flight gate (T8.1b's `--tier`). **Reviewer:** standard.
- **Commit:** `cli: the binary knows its edition, and governance on a core image warns (R3)`.

#### E1.4 The base and history images build with the tag; credentials does not

- **Files:** `.dockerignore` (admit `ee/`, F16b), `images/base/Dockerfile`, `images/history/Dockerfile`
  (`COPY ee ./ee`, `-tags ee`, the edition carrier, and an `org.opencontainers.image.licenses` label
  naming MIT plus the enterprise licence; the credentials image's label names MIT), `images/credentials/Dockerfile`
  (the label only), `images/build_tags_test.go` (new; S1.7's scan,
  inverted for base and history, and a context test that `.dockerignore` admits `ee/`),
  `images/base_test.go`, `images/history_test.go`, `images/size-budget.sh` only if U3 needs it; if U1
  shows the workflow needs a change, it is layered on the **already staged**
  `docs/pending-workflows/images.yml` (the credentials-service image leg, C1.14) and its README
  section, and U1 is read against that staged file.
- **Tests:** `TestBaseAndHistoryBuildFugaroWithTheEETag`, `TestCredentialsImageBuildsWithoutTheEETag`,
  `TestBuildTagScanCatchesPlants`, `TestRunImagesCarryNoKey` (no `COPY` or `ARG` of a key or secret),
  `TestBuildContextAdmitsEE`; and the `docker`-tagged build of the base image (`go test -tags docker
  ./images/ -run TestBase -count=1`), which fails today if the context drops `ee/`.
- **Command:** `go test ./images/ -run 'TestBaseAndHistory|TestCredentialsImage|TestBuildTagScan|TestRunImagesCarryNoKey|TestBuildContext|TestHistory' -count=1 && go test ./scripts/ -run TestEETagNeedsTheLabels -count=1 && go test -tags docker ./images/ -run TestBase -count=1`.
- **Red step:** `TestBaseAndHistoryBuildFugaroWithTheEETag` fails on today's Dockerfiles; once the
  tag is set and before the label, E3.2's extended `TestEETagNeedsTheLabels` fails.
- **Depends on:** E3.2 (the relabel and the label gate over the images land first, F16c), D524,
  D525. **Reviewer:** opus + mandatory independent review (licence and supply chain).
- **For operators (0.13.0 notes, E3.3):** per installation `fugaro update`; per repository
  `fugaro image refresh` (D533).
- **Commit:** `images: base and history carry the ee build; credentials stays core (D524)`.

#### E1.5 `ee/register` and the assembly file

- **Files:** `ee/register/register.go` (new; sets the edition), `ee/register/register_test.go` (new),
  `cmd/fugaro/ee_on.go` (new).
- **Tests:** `TestRegisterFollowsTheGatingModel` (T27 (b): register, notice on stderr),
  `TestTaggedBuildReportsEE` (in `ee/register`: after its `init`, the edition is `ee`; it fails while
  the edition defaults to `core`). E2.1–E2.3 each add, in their own test file, an **identity** test
  (`TestTaggedBinaryWiresTheCoordinator`, `…TheReport`, `…TheGovernance`), checking that the hook holds `ee/`'s
  function, not merely a non-nil one, since until E2.3 the policy and runner hooks default to
  today's non-nil core implementation — the "hook silently dropped" seam), `go build -tags ee ./cmd/fugaro`, the boundary tests tagged and
  untagged. `ee/` packages build without the tag (only `cmd/fugaro/ee_on.go` carries it), so
  `go test ./ee/...` runs them.
- **Command:** `go test ./internal/eeboundary/ -count=1 && go test ./ee/register/ -run 'TestTaggedBuild|TestRegister' -count=1 && go build -tags ee ./cmd/fugaro && go build ./cmd/fugaro`.
- **Red step:** `TestTaggedBuildReportsEE` fails until `ee/register` sets the edition.
- **Depends on:** E1.1, E1.2, E1.3, E0.2. The boundary tests already admit exactly this route; no
  boundary-test edit. **Reviewer:** opus + mandatory independent review.
- **Commit:** `ee: the register package and the one assembly file`.

#### E1.6 CI builds and tests both editions (staged)

- **Files:** `docs/pending-workflows/ee-edition.yml` (new staged workflow: `go vet -tags ee ./...`,
  `go test ./ee/... -count=1`, `go build -tags ee ./cmd/fugaro`; a separate file, because a staged
  `docs/pending-workflows/ci.yml` turns `TestDocsComputeBackendCIReachMatchesTheTree` red and
  `images/cloudbuild_test.go` reads that path),
  `docs/pending-workflows/README.md` (its section, and that `core-without-ee.yml` is now due),
  `internal/cli/docs_ee_ci_workflow_test.go` (new: pins the tagged vet, test and build steps in
  `ee-edition.yml`), `internal/cli/docs_core_without_ee_workflow_test.go` if its pinned lines change.
- **Tests:** `TestStagedCIBuildsBothEditions` (new), the existing core-without-ee pins.
  **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestStagedCI|Workflow|Docs' -count=1`.
- **Red step:** `TestStagedCIBuildsBothEditions` fails before the staged file exists.
- **OWNER-RUN:** the owner moves `docs/pending-workflows/ee-edition.yml` and `core-without-ee.yml`
  into `.github/workflows/` (`git mv docs/pending-workflows/ee-edition.yml .github/workflows/ && git mv
  docs/pending-workflows/core-without-ee.yml .github/workflows/`, then the README sections and pins
  the pending-workflows README says go with them, commit, push). No agent performs it.
- **Depends on:** E0.3. **Reviewer:** standard.
- **Commit:** `ci(staged): build and test the ee edition beside the core`.

#### E1.7 The tagged command tree for the reference and the plugin checks (D536)

- **Files:** `internal/clidoc/cliref/` (new importable package: `Generate` and `Prepare` split out of
  `internal/clidoc`, which is `package main` and cannot be imported), `internal/clidoc/cliref.go`
  (`go run ./internal/clidoc` stops writing `cli-reference.md`, so the two generators never
  overwrite each other), `ee/clidoc/main.go` (new; `go run ./ee/clidoc` imports `ee/register` and
  `internal/clidoc/cliref` and writes the reference), `ee/clidoc/cliref_test.go` (new; the reference
  is current against the tagged tree), `internal/cli/docs_mobile_mailbox_plan_test.go` (it pins
  `func TestCliReferenceIsCurrent(` in `internal/clidoc/cliref_test.go`), `website/test/meta.mjs`
  (it pins the "generated by go run ./internal/clidoc" banner),
  `internal/clidoc/cliref_test.go` (core: every core command and flag is in the file, a subset
  check), `internal/cli/docs_website_cliref_test.go` (the same subset rule),
  `plugin/routing_skill_test.go`, `plugin/setup_skill_test.go` and the shared skill-check helper
  (a paid command name passes through E1.1's stub list), `ee/plugincheck/skills_test.go` (new; the
  paid commands' flags named by a skill exist in the tagged tree).
- **Tests:** `TestCliReferenceIsCurrent` (moved to `ee/clidoc`), `TestCoreCommandsAreInTheReference`,
  `TestSkillsNamePaidFlagsThatExist`, `TestCoreSkillCheckAcceptsAPaidCommandByName`.
- **Command:** `go run ./ee/clidoc && go test ./ee/clidoc/ ./ee/plugincheck/ ./internal/clidoc/... -count=1 && go test ./plugin/... -count=1 && go test ./internal/cli/ -run 'TestCliReference|TestDocsMobileMailbox' -count=1 && go test ./internal/eeboundary/ -count=1 && cd website && npm run check`.
- **Red step:** `TestSkillsNamePaidFlagsThatExist` fails before the tagged tree is reachable from a test.
- **Depends on:** E1.5. **Reviewer:** standard.
- **Commit:** `clidoc: the reference and the skills read the released (tagged) command tree (D536)`.

### Slice E2 — the moves (one per feature; behaviour unchanged in the tagged build)

#### E2.1 The coordinator, command and library (D434, T10; was S1.4)

- **Files:** S1.4's list (moved `internal/coordinator/` → `ee/coordinator/`, the four command and
  test files → `ee/coordinator/cli/`, the four test-data files with `.license` siblings, a copy of
  `coordinator_token.json`; new `internal/coordpath/`; re-pointed `internal/cli/run.go` is **not**
  touched here (the hook is E1.1's): `internal/cli/project_cmd.go`, `internal/budget/rules/coordinator_test.go`,
  `emulator_coordinator_test.go`, `rules_test.go`, `internal/cli/doctor_policy_test.go`,
  `internal/cli/docs_desktop_console_test.go`), plus `internal/cli/run.go` and `internal/cli/extend.go`
  (the declarer default flips and the `internal/coordinator` import goes), `ee/register/coordinator.go`
  (new), `ee/register/coordinator_test.go` (new; the identity check), `internal/runview/launched_by_test.go`
  (its allow-list re-pointed to the moved `ee/` paths, never loosened: the launcher's name is
  untrusted text), `internal/cli/runid_test.go` (its `coordinator status` case),
  `docs/coordinator/implementation-plan.md` (its `internal/coordinator` citations re-pointed, so
  `TestDocsCoordinatorPlanCitationsExist` holds),
  `internal/cli/root.go`, `website/content/cli-reference.md` (regenerated), `docs/coordinator/README.md`.
- **Tests:** `TestUntaggedBuildStillLaunches`, `TestCoordinatorCommandRegistersTagged`,
  `TestNoticeNeverOnStdout` (coordinator status `--json`), the moved tests, the rules tests against
  `internal/coordpath`.
- **Command:** `go run ./ee/clidoc && go test ./internal/cli/ -run 'TestUntaggedBuild|TestDocsDesktop|TestUntaggedPaid|TestDocsCoordinator|TestRunID' -count=1 && go test ./internal/runview/ -run LaunchedBy -count=1 && go test ./internal/budget/rules/ -run Coordinator -count=1 && go test ./ee/coordinator/... ./ee/register/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestCoordinatorCommandRegistersTagged` fails while the command is core.
- **Change beyond S1.4:** the launch hook's core default becomes "declare nothing"; `ee/register/coordinator.go`
  registers today's declarer (D535).
- **Depends on:** E1.5, E1.7 (the generator). **Reviewer:** opus + mandatory independent review.
- **Commit:** `coordinator: the command and the library move to ee/ (D434, T10)`.

#### E2.2 `fugaro report` moves; the digest keeps its MIT copy (D444, T11; was S1.5)

- **Files:** S1.5's list (moved `internal/report/` → `ee/report/`, the command part of
  `internal/cli/report.go` and `report_test.go` → `ee/report/cli/`; new `internal/cli/reportcore.go`;
  `reportHistory` moves with the command (it returns `[]report.Rec`); new `internal/digest/period.go`
  with `Dim`, `ParseDim`, `PeriodKey`, the period keys, `USD` **and `USD6`** (F6);
  `internal/digest/digest.go`, `layout.go`, `patterns.go`, `render.go`, `internal/runner/digest.go`,
  `internal/cli/runs_digest.go` and the tests that import `internal/report`
  (`internal/digest/digest_test.go`, `layout_test.go`, `internal/cli/runs_digest_test.go`,
  `internal/runner/digest_test.go`) re-pointed), plus `internal/cli/project.go`
  (`consoleReadPaths`), `ee/register/report.go` (new), `internal/cli/root.go`,
  `ee/register/report_test.go` (new; the identity check), `internal/cli/runid_test.go` (`reportDefaultSince`
  and its `report` case), `internal/cli/docs_desktop_console_test.go`,
  `website/content/cli-reference.md`, `plugin/skills/working/SKILL.md` (`fugaro report` named as a business-licensed command; the digest
  as free).
- **Tests:** `TestDigestNeedsNoEE` (an untagged build writes a digest, and `internal/digest` imports
  nothing under `ee/` or `internal/report`), `TestReportCommandRegistersTagged`,
  `TestNoticeNeverOnStdout` (`report --json`), the moved tests, the plugin's command checks against
  the tagged tree.
- **Command:** `go run ./ee/clidoc && go test ./internal/digest/ -count=1 && go test ./internal/cli/ -run 'TestDigest|TestRunsDigest|TestDocsDesktop|TestReportComputeSources' -count=1 && GOFLAGS=-p=2 go test ./internal/runner -run Digest -count=1 -timeout 30m && go test ./ee/report/... ./ee/register/ -count=1 && go test ./internal/cli/ -run 'TestUntaggedPaid|TestRunID' -count=1 && go test ./plugin/... -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestDigestNeedsNoEE` fails while `internal/digest` imports `internal/report`.
- **Depends on:** E2.1 (`root.go`, `docs_desktop_console_test.go`, `cli-reference.md`), E1.2
  (`internal/digest/render.go`), the in-flight
  gate (#1196, #1215). **Reviewer:**
  opus + mandatory independent review.
- **Commit:** `report: fugaro report moves to ee/; the digest stays MIT (D444, T11)`.

#### E2.3 Governance moves: the policy, the defaults, the stage timeout and the halts

*Was two tasks (E2.3 policy, E2.4 run) until review round 3: flipping the policy default turns the
stage-timeout tests red, so they cannot sit in one level apart. E2.4 is withdrawn; its number is not
reused.*

- **Files:**
  - **Moved:** `DefaultGovernance`, `DefaultTierExpectations`, `tightenGovernance`, `tightenTier`
    and their tests (`internal/policy/governance_test.go`, `tier_test.go`) → `ee/governance/policy.go`,
    `policy_test.go`; `stageTimeout` and the governance-halt decision from `internal/runner/runner.go`,
    and the stage-timeout tests (`internal/runner/stage_timeout_test.go` and the internal one #1216
    adds) → `ee/governance/runner.go`, `runner_test.go`.
  - **Kept, re-pointed:** `internal/policy/policy.go`, `merge.go`, `hook.go` (core default: decode,
    warn, ignore); `governanceHalt`, `checkpointPR`, `checkpointReportPart` (the plumbing, D521);
    `internal/runner/governance_hook.go` (core default: ignore, warn once in the run log); the
    governance cases of `internal/runner/policy_internal_test.go`, `halt_test.go` and
    `internal/cli/project_policy_test.go` (R8: they keep only the core-default path).
  - **New:** `ee/register/governance.go`, `ee/register/governance_test.go` (the identity test).
- **Tests:** `TestGovernanceTightenOnly`, `TestGovernanceDefaults`, `TestStageTimeoutHaltsWithCheckpoint`,
  `TestStageTimeoutNeverAboveTimeoutsStage` (moved to `ee/governance`, run through
  `internal/runner/runnertest`), `TestCapsUnchangedByTheTag` (G7; in `ee/governance`, hook set and
  unset, since the core cannot import `ee/`), `TestTaggedRunnerHaltsAsBefore` (in `ee/governance`),
  `TestUntaggedPolicyIgnoresGovernance`, `TestUntaggedRunnerIgnoresGovernanceWithAWarning` (core, now
  the real defaults), `TestTaggedJobLogsTheEnforcementNotice` (D523), `TestTaggedBinaryWiresTheGovernance`.
- **Command:** `go test ./internal/policy ./internal/config -count=1 && GOFLAGS=-p=2 go test ./internal/runner -run 'Policy|Governance|Halt|StageTimeout|Untagged' -count=1 -timeout 30m && go test ./internal/cli/ -run 'ProjectPolicy|Validate|Governance' -count=1 && go test ./ee/governance/ ./ee/register/ -count=1 && go test ./internal/eeboundary/ -count=1`.
- **Red step:** `TestTaggedRunnerHaltsAsBefore` and `TestUntaggedPolicyIgnoresGovernance` fail while
  the defaults are today's.
- **Depends on:** E1.2, E1.5, the in-flight gate (#1216). **Reviewer:** opus + mandatory independent
  review (the run path and the policy merge).
- **Commit:** `governance: the policy, the stage timeout and the halts move to ee/ (D521)`.

### Slice E3 — docs, the relabel and the 0.13.0 release

#### E3.1 Docs, skills and pins say which parts are business-licensed

- **Files:** `plugin/skills/working/reference/diagnose.md` (governance halts are an Enterprise
  feature; reading them is not), `plugin/skills/routing/SKILL.md` (the 0.13.0 gate names the tagged
  image), `docs/project-layer.md` (the governance rows marked business-licensed; `TestScopeTableMatchesDocs`
  keeps its rows), `docs/recipes.md` (`run-digest` stays free), `docs/release.md` (the checklist
  verifies the tag on the release and on the base and history images), `LICENSING.md` (governance,
  the report and the coordinator listed under `ee/`), `internal/cli/docs_licensing_test.go` (its
  `LICENSING.md` pins on "reserved"/"excluded").
- **Tests:** `TestScopeTableMatchesDocs`, the plugin lint, the Docs suite.
- **Command:** `go test ./internal/config -run TestScopeTableMatchesDocs -count=1 && go test ./plugin/... -count=1 && GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** a `LICENSING.md` pin naming the moved features fails before the text.
- **Depends on:** E2.2, E2.3. **Reviewer:** standard.
- **Commit:** `docs: governance, the report and the coordinator are business-licensed`.

#### E3.2 The single mixed-licence release and its relabel (D447/T21; was S1.6)

- **Files:** S1.6's list: `.goreleaser.yaml` (`-tags ee`, the cask's licence), every label
  `TestEETagNeedsTheLabels` reads, `scripts/notices_test.go` (`eeTagLabelGaps` also reads the base,
  history and credentials Dockerfiles: one that builds `-tags ee` must carry an
  `org.opencontainers.image.licenses` label naming the enterprise licence, F16c; E1.4 adds it), `README.md`, the website's licence wording
  (`website/src/pages/community.astro`, `website/test/faq-community.mjs`), `docs/releases/README.md`.
  `LICENSING.md` is E3.1's.
- **Tests:** `TestEETagNeedsTheLabels`, `TestGoreleaserReleaseBuild`,
  `TestThirdPartyNoticesUnionsTheBuildMatrix` (the tagged build), `faq-community.mjs`.
- **Command:** `go test ./scripts/ -run 'TestEETagNeedsTheLabels|TestGoreleaserReleaseBuild|TestThirdPartyNotices' -count=1 && cd website && npm run check`.
- **Red step:** set the tag first; `TestEETagNeedsTheLabels` fails until every label changes.
- **Depends on:** E3.1, E1.6 (the owner's copy done), subscriptions S7.1 if it is approved first
  (both write `community.astro` and `faq-community.mjs`; whichever lands second rebases), O1 (the owner and counsel on shipping
  under the current `ee/LICENSE`). **Reviewer:** opus + mandatory independent review.
- **Commit:** `release: one binary, MIT core plus business-licensed parts (D447, T21)`.

#### E3.3 The 0.13.0 notes

- **Files:** `docs/releases/v0.13.0.md` (new, or the next-notes file the release process uses),
  `website/data/release-summaries.yaml` (one line, no backticks).
- **Content:** the Licensing highlight (D533); what 0.12.x users keep (R1, with F3's corrected
  facts: `fugaro report` since v0.1.0, `calls`/`agents` since v0.11.6); `### For operators`: **per
  installation** `fugaro update` (the base pin and the history image the sweep runs; U5 confirms the
  exact command); **per repository** `fugaro image refresh` in each repository's checkout; a job
  image built without the tag ignores `governance:` with a warning in the run log.
- **Tests:** the release-notes pins. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Red step:** none (notes). **Depends on:** E3.1. **Reviewer:** standard.
- **Commit:** `docs(releases): 0.13.0, the first release with business-licensed parts`.

#### E3.4 Checks 200 and 201 (written, then OWNER-RUN)

- **Files:** `docs/gcp-live-checklist.md` (two sections and their index rows), `docs/coordinator/README.md`
  (register line).
- **Check 200:** on the sandbox installation with the released 0.13.0 and refreshed images, a run
  with `governance.stage_timeout.implement` set tiny halts with `stage_budget` and a checkpoint
  report; the image's edition carrier (R3, not `fugaro version`, whose text does not change) says
  `ee`; `fugaro runs digest --period
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

E0.1, E0.2, E0.3 ← the decisions (E0.2 needs D532 alone). E0.4 ← E0.3.
E1.1, E1.2, E1.3 ← E0.3 + the in-flight gate. E1.6 ← E0.3. E1.5 ← E1.1, E1.2, E1.3, E0.2.
E1.7 ← E1.5.
E2.1 ← E1.5, E1.7. E2.2 ← E2.1, E1.2 + the in-flight gate. E2.3 ← E1.2, E1.5 + the
in-flight gate.
E3.1 ← E2.2, E2.3. E3.2 ← E3.1, E1.6 (the owner's copy done), O1. E3.3 ← E3.1.
E1.4 ← E3.2 (no public image carries `ee/` before the relabel, F16c).
The 0.13.0 tag ← E3.2, E1.4, E3.3. E3.4 ← the tag.

### Levels

| Level | Tasks (run concurrently) |
| --- | --- |
| **L0** | E0.1, E0.2, E0.3 |
| **L1** | E0.4, E1.6; E1.1, E1.2, E1.3 once the in-flight gate is clear |
| **L2** | E1.5 |
| **L3** | E1.7, E2.3 |
| **L4** | E2.1 |
| **L5** | E2.2 |
| **L6** | E3.1 |
| **L7** | E3.2, E3.3 |
| **L8** | E1.4 |
| **after the tag** | E3.4 (run by the owner) |

**Critical path:** decisions → E0.3 → (in-flight gate) → E1.1 / E1.2 → E1.5 → E1.7 → E2.1 → E2.2 →
E3.1 → E3.2 → E1.4 → 0.13.0 tag → Check 200. Off it: E2.3 (join at E3.1), the owner's workflow copy
(E1.6, needed by E3.2) and O1 (E3.2).

**Sequential by necessity:** E2.1 → E2.2 (both edit `internal/cli/root.go`,
`internal/cli/docs_desktop_console_test.go`, `website/content/cli-reference.md`); E1.1 → E1.5 (the
registry's interface); E1.7 → E2.1 (the generator the moves regenerate the reference with); E3.1 → E3.2 (the labels read `LICENSING.md`); E3.2 → E1.4 (the label gate
must cover the images before an image takes the tag).

### Shared files (no level has two writers of one file)

| File | Writers (level) |
| --- | --- |
| `internal/cli/root.go` | E1.1 (L1), E2.1 (L4), E2.2 (L5) |
| `internal/cli/run.go`, `internal/cli/extend.go` | E1.1 (L1), E2.1 (L4) |
| `internal/cli/recipes_skew.go`, `internal/cli/validate.go` | E1.3 (L1) |
| `internal/cli/docs_desktop_console_test.go` | E2.1 (L4), E2.2 (L5) |
| `internal/cli/docs_licensing_test.go` | E0.2 (L0), E3.1 (L6) |
| `internal/cli/project_policy_test.go` | E2.3 (L3) |
| `internal/cli/docs_website_cliref_test.go`, `internal/clidoc/*`, `plugin/*_test.go`, `ee/clidoc/`, `ee/plugincheck/` | E1.7 (L3) |
| `website/content/cli-reference.md` | E1.7 (L3), E2.1 (L4), E2.2 (L5) |
| `internal/policy/policy.go`, `merge.go`, `hook.go` | E1.2 (L1), E2.3 (L3) |
| `internal/runner/runner.go`, `halt.go`, `governance_hook.go`, `runnertest/` | E1.2 (L1), E2.3 (L3) |
| `internal/runner/policy_internal_test.go`, `halt_test.go` | E2.3 (L3) |
| `internal/digest/*.go` | E1.2 (L1: `section.go`, `render.go`), E2.2 (L5) |
| `ee/register/*.go` | E1.5 (L2: `register.go`, `register_test.go`), E2.3 (L3: `governance.go`, `governance_test.go`), E2.1 (L4: `coordinator.go`, `coordinator_test.go`), E2.2 (L5: `report.go`, `report_test.go`) — one file per writer, each with its own identity test name |
| `ee/governance/` | E2.3 (L3) |
| `docs/coordinator/README.md` | E2.1 (L4), E3.4 (after the tag) |
| `LICENSING.md` | E3.1 (L6) |
| `README.md`, `scripts/notices_test.go`, `.goreleaser.yaml`, `website/src/pages/community.astro`, `website/test/faq-community.mjs` | E3.2 (L7) |
| `plugin/skills/working/SKILL.md` | E2.2 (L5) |
| `.dockerignore`, `images/**` | E1.4 (L8) |
| `docs/pending-workflows/README.md` | E1.6 (L1); E1.4 (L8) only if U1 needs a staged `images.yml` |
| `internal/cli/docs_ee_ci_workflow_test.go`, `docs/pending-workflows/ee-edition.yml` | E1.6 (L1) |
| `internal/runview/launched_by_test.go`, `docs/coordinator/implementation-plan.md` | E2.1 (L4) |
| `internal/cli/runid_test.go` | E2.1 (L4), E2.2 (L5) |
| `internal/clidoc/cliref/`, `internal/cli/docs_mobile_mailbox_plan_test.go`, `website/test/meta.mjs` | E1.7 (L3) |
| `ee/README.md`, `.claude/rules/ee.md`, `internal/cli/docs_ee_reserved_test.go` | E0.2 (L0) |
| `docs/subscriptions/*` | E0.1 (L0) |
| `docs/design/ee-boundary-moves.md` | E0.3 (L0) |
| `docs/task-governance/*` | E0.4 (L1) |
| `docs/releases/v0.13.0.md`, `website/data/release-summaries.yaml` | E3.3 (L7) |

## Checks this plan takes

| Check | Measures | Written by | Run by |
| --- | --- | --- | --- |
| **200** | Governance halts in a job from the tagged image; the edition reads `ee`; the digest runs from an untagged source build | E3.4 | the owner, sandbox only |
| **201** | The console's Spend and project views work against the tagged CLI with its stderr notice | E3.4 | the owner |

**202–209** stay reserved and unused. Subscriptions Check 178 (the coordinator's key-gated job) is
not needed while T27 (b) holds and no key is mounted (D524); E0.1 marks it deferred.

## Release actions

- **0.13.0, per installation:** `fugaro update`, so jobs run the tagged base and the coordinator's
  sweep runs the tagged history image (U5 confirms the command). **Per repository:** `fugaro image
  refresh` in each repository's checkout, so its workflow images rebuild on the tagged base. Without
  them an installation on a 0.12.x image keeps refusing `governance:` as it does today.
- No backfill, no secret, no Terraform change.

## Risks

- **The hold lengthens with the gate.** E1.1, E1.2, E1.3, E2.2 and E2.3 wait on #1216, #1215, #1196
  and T8.1b (D527). E0.x, E1.6 and E2.1's content do not.
- **Once E3.2 merges, every release is the mixed-licence release.** A 0.12.x patch cut from `main`
  after it would carry `ee/` code; under the hold no CLI tag is cut, and a needed patch is cut from
  the `v0.12.1` tag instead.
- **Dev images built from `main` between the moves and E1.4 lose governance.** From E2.3 (L3) an
  untagged base image built from `main` ignores `governance:` (with its warning), and from E2.1 (L4)
  the untagged history image's `coordinator sweep` hits the stub. No release is affected (the hold),
  but a dogfood installation refreshed from `main` in that window is: it stays on its current images
  until E1.4 lands (O6).
- **A hook default lost in a merge** would turn governance off in the tagged build silently; G10,
  `TestTaggedBinaryWires…` and `TestTaggedRunnerHaltsAsBefore` pin it, and Check 200 measures
  it live.
- **Image size** (U3) and the images workflow (U1) are unverified; E1.4 records both.
