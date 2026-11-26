# Update from anywhere: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped it are D3340–D3352, all as recommended. D3343, D3347 and D3349 were
amended after review and confirmed.
- **D3340:** the pin becomes a notice, and the HARD RULE's clause changes.
- **D3341:** the repository stage waits for L2, and everything else ships first.
- **D3342:** every stale derived image is refreshed.
- **D3343:** every read goes through the credentials service's person read mint
  (`serviceReadProvider`), so no key or token reaches the admin's machine.
- **D3344:** the backlog fold runs for one release.
- **D3345 and D3351:** `--anchor` leaves `update` and `setup` for `repos add`.
- **D3346:** the RC39 facts are written for every repository.
- **D3347:** in `update`, `--ask` turns on value entry and nothing is asked without it. `setup`
  keeps its terminal prompt.
- **D3348:** the hint never narrows `--plan-only`.
- **D3349:** `--check` reads the local list through the read mint and writes nothing.
- **D3350:** a published admit counts as enrolled.
- **D3352:** no read in a coding agent's session, as D902.

## Conventions every part follows

- `CLAUDE.md` throughout, in particular these rules:
  - the HARD RULE on `fugaro update`, safe with runs in flight;
  - Fugaro asks for nothing (`docs/no-prompt-policy/`);
  - hermetic tests (`internal/gcpfake`, the fake provider, `t.TempDir()`): no network, no home
    directory, no live cloud action by an agent;
  - no secret-shaped literal in a test;
  - comments explain why;
  - targeted tests, and `go test ./internal/cli/ -run Docs` for any documentation change;
  - `gofmt -l .` prints nothing.
- **A field never changes meaning.** `--json` and `--events` gain two members and change no other:
  `repos` on a stage, and `notices` for the steps after the stage loop (R12).
- **A flag's help change regenerates `website/content/cli-reference.md`** with
  `go run ./ee/clidoc`, in the same task. `docs_website_cliref_test.go` pins it.
- Boundaries:
  - **Always:**
    - read a repository from its default branch through C1's view, which reads through the
      credentials service's read mint;
    - build every line through `newRepoLine`, which sanitises it (R12);
    - give a repository that is unread, or that needs the owner, one named line;
    - keep each stage's refusal of cloud writes in an agent's session.
  - **Ask first:** any new cloud write, any new question to the owner, and any change to the HARD
    RULE beyond the pin clause.
  - **Never:**
    - read the working directory in a stage (R10);
    - write to a checkout from `update`;
    - prompt without `--ask`, or under `--json`;
    - onboard a repository from `update`.
- Live checks are the owner's (Check 930).
- The project's rigor is `startup` (`rigor.default`), and decoupling is on (`planning.decoupling`,
  true).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The event stream is byte-identical from an empty directory, from each checkout, and from a checkout dirty in a key other than `project:`. The repository stage is exempt until U11 (acceptance 2, R3, F29) | I1, then U11 |
| No stage reaches the working directory through the six banned calls, except the selector's project default and the repository stage until U11 (acceptance 8, R10) | I1, then U11 |
| One unread repository never fails the update; the default catalog entry is held while any repository is unread (acceptance 4, R9) | U5, I1 |
| A missing or invalid `fugaro.yaml` is `unread`, naming why (R13) | U2 |
| In `update`, no value is asked for without `--ask`, in an agent's session, or under `--json`; `setup` keeps its prompt (acceptance 3, D3347) | U3b |
| A run in flight is untouched on a two-repository project (acceptance 7) | I1 |
| `update` writes no working tree, the anchor included (acceptance 5) | U6a, U9b, I1 |
| Repository-controlled text is sanitised before it is printed (R12) | C1, U13 |
| `doctor --offline` and `--plugin` make no network call (R15) | U6b |
| No key or token reaches the admin's machine; every read is the credentials service's read mint (D3343) | U2, I1 |
| An admit pattern the reader could not list gets a notice line (R19) | U2, I1 |
| No identity is minted in a coding agent's session (D3352, D902) | U2 |
| `main` keeps today's behaviour between the stage tasks and I1 (R21) | C1 |
| An installation at the previous release converges (HARD RULE, R22) | I1 |

## Launch notes

L8 M1 merged during review round 2 (#3816), so U1 is ready. The graph has no external gate for
**U11**, which reads as ready once its edges clear but must not launch before L2 lands. Whoever
launches by plan edges holds it by hand.

## Slices

1. **The view and the reader** (C1, U1, U2). These are inert until I1 wires them.
2. **The stages over every repository** (U3a, U3b, U4, U5, U6a, U8, U10) and **the output members**
   (U13). These are no-ops behind the contract's stub until I1.
3. **Doctor** (U6b): its two checks read the default branch.
4. **The anchor** (U9a, U9b, U9c, U9d). It moves into `repos add`, and the flag goes from both
   doors.
5. **The wiring** (I1) and **the documents** (U7a, U7b).
6. **After L2** (U11): the repository stage over every repository.
7. **The owner's check** (K1, Check 930).

## Tasks

### C1: contract: the per-repository view, its lines and how a stage reports them
- Kind: contract.
- Acceptance: `internal/cli/update_repos.go` defines:
  - `repoView`: the canonical owner/name, the provider, the default branch and its head commit, the
    `fugaro.yaml` bytes and its parsed `*config.Config`, extra files by path, and `Unread` (a
    sanitised one-line reason, empty when read);
  - `newRepoLine`, which builds an `initflow.RepoLine` with the repository and the detail passed
    through `pluginwire.Printable` and `safetext.OneLine`;
  - the free function `enrolledRepos(ctx, enrolledIn) ([]repoView, []string)` (R16, R19): the views
    in canonical order, memoized per run, and the admit patterns the reader could not list. Its
    input carries the project layer, or the local layer file under `--check` (R13);
  - `(e *initEngine) repos(ctx)`, which wraps that function;
  - `(e *initEngine) reportRepos(name string, lines []initflow.RepoLine)`, for a stage or a step
    after the loop;
  - `hintFirst`, which reorders human lines only.

  `internal/initflow/repos.go` defines `RepoLine` (`Repo`, `State`, `Detail`; the states `done`,
  `needs you`, `unread` and `skipped`) and the interface `RepoReporter` (`RepoLines() []RepoLine`)
  (R12). `init_stages.go` gains one field on `initEngine` that holds the memo and the reported
  lines; it is C1's only edit there. `internal/cli/update_repos_stub.go` holds
  `enrolledReposImpl`, which returns today's behaviour (R21): the working directory's checkout as a
  single view, read the way the stages read it now, or none outside a checkout. `contract-repos.md` says what a stage may rely
  on: the same list from every directory; an unread repository carries its reason, and a stage
  reports it and never guesses; extra files only when asked for; no working-tree read.
- Files: `internal/cli/update_repos.go` (new), `internal/cli/update_repos_stub.go` (new),
  `internal/initflow/repos.go` (new), `internal/cli/init_stages.go`,
  `docs/update-from-anywhere/contract-repos.md` (new).
- Interfaces: produces the view, the line, the reporter interface, the report call and the stub.
- Tests: `TestRepoViewOrderIsCanonical`, `TestHintFirstMovesOnlyHumanLines`,
  `TestNewRepoLineSanitises`.
- Command: `go test ./internal/cli/ -run 'TestRepoViewOrderIsCanonical|TestHintFirstMovesOnlyHumanLines|TestNewRepoLineSanitises' -count=1`
- Red step: the tests fail to compile before the types exist.
- Depends on: none.
- Commit: `update-from-anywhere C1: contract: the per-repository view`
- Reviewer tier: standard.

### U1: the reader's extra paths, branch and head
- Acceptance: `repofiles.Read` gains an option for extra paths: files such as
  `.claude/settings.json`, and trees such as `docs/backlog/`. Each repository now also carries its
  default branch's name and head commit (R1, F16).
  - With the option unset, every field L8 reads is unchanged, and L8's tests pass as they are.
- Files: `internal/repofiles/reader.go`, `internal/repofiles/reader_test.go`.
- Interfaces: consumes L8 M1; produces the option, the branch and the head.
- Tests: `TestReadExtraPaths`, `TestReadKeepsBranchAndHead`, `TestReadWithoutExtraPathsIsUnchanged`.
- Command: `go test ./internal/repofiles/ -count=1`
- Red step: `TestReadExtraPaths` fails before the option exists.
- Depends on: L8 M1 (#3816, merged).
- Commit: `update-from-anywhere U1: the reader's extra paths, branch and head`
- Reviewer tier: standard.

### U2: the enrolled list through the credentials service's read mint
- Acceptance:
  - `readEnrolledRepos` calls `repofiles.Read` with the extra paths the stages need, and with an
    opener that calls `serviceReadProvider` where it is, behind `refuseKeyReadInAgent` as the
    launcher's read does (`project_job.go`, `serviceReadTarget`) (D3343, D3352, R1).
  - No App key, token variable or credential helper is read on this machine. In a coding agent's
    session, no identity is minted: every repository is `unread`, with one line, "run fugaro
    update from your own terminal".
  - A GitHub repository with no `github_app_id` of its own takes `projectGitHubAppID`'s answer
    (F43).
  - **The parse** (R13): each `fugaro.yaml` is parsed against the layer it is given. A missing or
    invalid file is `unread`, naming the cause or the first problem.
  - **The fix in each reason** (R1): no App ID in the project; no job serving the repository; the
    service's own refusal for the operators list, the `gcloud` login or the Bitbucket read consumer;
    no service URL yet (R22).
  - **Admit patterns:** it returns the patterns it could not list, for a notice line each (R19).
  - **The hint:** the selector's existing project default is reused; nothing new picks a project
    (A3).
- Files: `internal/cli/update_repos_read.go` (new), `internal/cli/update_repos_read_test.go` (new).
- Interfaces: consumes U1, `serviceReadProvider`, `refuseKeyReadInAgent` and
  `projectGitHubAppID`; produces `readEnrolledRepos`, which I1 connects to C1's stub.
- Tests: `TestEnrolledReposAreTheListReposLsDraws`, `TestEnrolledReposMissingOrInvalidIsUnread`,
  `TestEnrolledReposNameTheFix`, `TestEnrolledReposReadThroughTheMint`,
  `TestEnrolledReposMintNothingForAnAgent`, `TestEnrolledReposBorrowTheProjectAppID`,
  `TestEnrolledReposNamePatterns`.
- Command: `go test ./internal/cli/ -run 'TestEnrolledRepos' -count=1`
- Red step: the tests fail before the functions exist.
- Depends on: U1 (behaviour).
- Commit: `update-from-anywhere U2: the enrolled list through the read mint`
- Reviewer tier: opus, with an independent review (credentials).

### U3a: the secrets stage over every repository
- Acceptance:
  - In `update`, `secretsStage.resolve` reads `e.repos(ctx)`, not the working directory. It
    computes each repository's wanted names with `infra.SecretMounts`, as today. `setup` keeps its
    checkout target (R18).
  - `authorize` treats every repository in the list as enrolled (D3350; it is the owner's go for
    creating an admit-only repository's containers, R20). The hostile-checkout gate stays for
    `setup --repo` and `--onboard-repo`.
  - Each missing name gets one line with today's command. A name several repositories want is one
    line listing them (R2).
  - `projectScope`, D2313's drop and the F21 provider-key line are unchanged. Every line goes
    through `e.reportRepos`.
- Files: `internal/cli/initsecrets.go`, `internal/cli/initsecrets_repos_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestSecretsStageNamesEveryRepository`, `TestSecretsStageAdmitCountsAsEnrolled`,
  `TestSecretsStageSetupKeepsItsCheckout`.
- Command: `go test ./internal/cli/ -run 'TestSecretsStage' -count=1`
- Red step: `TestSecretsStageNamesEveryRepository` fails on today's working-directory read, using a
  two-repository test view.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U3a: the secrets stage over every repository`
- Reviewer tier: opus, with an independent review (secrets).

### U3b: `update`'s `--ask` turns on value entry
- Acceptance:
  - In `update`, a value is asked for only under `--ask`, at the owner's own terminal, on any
    project. It is never asked for an agent or under `--json`, and with no terminal it fails at
    once (D3347, R18).
  - `setup` keeps `canPrompt` as it is.
  - `AskDecision` no longer answers `AskUnmanaged` for `update` when a value is missing, and
    `managed_ask_test.go` pins the new case.
  - `--ask`'s help says so, and the CLI reference is regenerated.
  - `docs/no-prompt-policy/reconciliation.md` and D1028's row in
    `docs/managed-agent-converge/reconciliation.md` each get a dated note naming D3347.
- Files: `internal/cli/initsecrets.go`, `internal/cli/init.go`, `internal/initflow/stage.go`,
  `internal/cli/managed_ask_test.go`, `internal/cli/initsecrets_test.go`,
  `internal/cli/initsecrets_pty_test.go`, `docs/no-prompt-policy/reconciliation.md`,
  `docs/managed-agent-converge/reconciliation.md`, `website/content/cli-reference.md`
  (generated).
- Interfaces: none new.
- Tests: `TestUpdateAsksValuesOnlyUnderAsk`, `TestUpdateAskWithNoTerminalFailsAtOnce`,
  `TestUpdateNeverAsksAnAgentOrJSON`, `TestSetupKeepsItsValuePrompt`, plus the Docs tests.
- Command: `go test ./internal/cli/ -run 'TestUpdateAsksValuesOnlyUnderAsk|TestUpdateAskWithNoTerminal|TestUpdateNeverAsksAnAgentOrJSON|TestSetupKeepsItsValuePrompt|TestManagedAsk|TestSecrets|TestInitSecrets|Docs' -count=1`
- Red step: `TestUpdateAsksValuesOnlyUnderAsk` fails while `canPrompt` asks at any terminal.
- Depends on: U3a (both write `initsecrets.go`).
- Commit: `update-from-anywhere U3b: update's --ask turns on value entry`
- Reviewer tier: opus, with an independent review (the no-prompt policy).

### U4: every stale derived image, from its default branch
- Acceptance:
  - The checkout stage no longer calls `pluginwire.Locate(".")`.
  - For each read repository it runs today's staleness test and build, from the view's
    `fugaro.yaml` and head commit, with today's confirmation (D3342, R6).
  - `jobConfigCopyNote` runs per repository, from the view.
  - A repository it cannot build from this machine gets one line (V1). An admit-only repository's
    build is the owner's go under D3350 (R20).
  - It exports a per-repository staleness function with an offline variant (today's `cloudCheck`
    rule, no build-record read), which U10 uses (F50).
  - `fugaro image refresh` keeps its checkout path.
- Files: `internal/cli/init_checkout_upgrade.go`, `internal/cli/upgrade_cloud.go`,
  `internal/cli/image_refresh.go`, `internal/cli/init_checkout_repos_test.go` (new).
- Interfaces: consumes C1; produces the staleness function. It may raise R-view-files.
- Tests: `TestCheckoutStageRefreshesEveryStaleRepository`,
  `TestCheckoutStageNamesARepositoryItCannotBuild`.
- Command: `go test ./internal/cli/ -run 'TestCheckoutStage' -count=1`
- Red step: the first test fails while the stage skips outside a checkout.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U4: every stale derived image`
- Reviewer tier: opus, with an independent review (billable builds behind a widened guard).

### U5: image kinds and the default entry over every repository
- Acceptance:
  - `imagesStage.kinds` is the union over every read repository (R9).
  - `staleCatalog` lists entries by their building repository. `originRepo(ctx)` is no longer
    called, and there is no "here" split.
  - The policy stage writes the default catalog entry only when every repository was read and none
    refuses. Otherwise it holds the entry and names the unread repositories.
- Files: `internal/cli/init_images.go`, `internal/cli/init_images_catalog.go`,
  `internal/cli/init_policy_migrate.go`, `internal/cli/init_images_repos_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestImageKindsAreTheUnion`, `TestDefaultEntryHeldWhileARepositoryIsUnread`.
- Command: `go test ./internal/cli/ -run 'TestImageKindsAreTheUnion|TestDefaultEntryHeld' -count=1`
- Red step: `TestImageKindsAreTheUnion` fails on today's working-directory read.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U5: kinds and the default entry over every repository`
- Reviewer tier: standard.

### U6a: the pin and `budget.mode: off` become notices in update
- Acceptance:
  - For each repository whose default branch has the plugin pin in `.claude/settings.json`, or
    `budget.mode: off` in `fugaro.yaml`, `update` reports one line naming the file and the exact
    edit. The text comes from `repo_notices.go` (D3340).
  - `pluginStage.remove` and `writeBudgetModeRemoval` (`init_fugaroyaml.go:299`) are deleted, and no
    working tree is written.
  - The plugin's install half runs in a fixed directory, with no `os.Getwd`.
- Files: `internal/cli/init_plugin_stage.go`, `internal/cli/update_budget_off.go`,
  `internal/cli/init_fugaroyaml.go`, `internal/cli/repo_notices.go` (new),
  `internal/cli/repo_notices_test.go` (new), `internal/cli/update_budget_off_test.go`.
- Interfaces: consumes C1 and its extra files; produces the notice text.
- Tests: `TestPinNoticeFromDefaultBranch`, `TestBudgetOffNoticeFromDefaultBranch`,
  `TestPluginInstallRunsInAFixedDirectory`.
- Command: `go test ./internal/cli/ -run 'TestPinNoticeFromDefaultBranch|TestBudgetOffNoticeFromDefaultBranch|TestPluginInstallRunsInAFixedDirectory' -count=1`
- Red step: `TestPinNoticeFromDefaultBranch` fails on today's diff path.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U6a: the pin and budget.mode off become notices`
- Reviewer tier: opus, with an independent review (how `claude` is spawned).

### U6b: doctor's `repo-plugin-pin` and `repo-budget-mode` checks
- Acceptance:
  - The `plugin-pin` and `budget-mode` checks, which mean "this checkout", are retired (R15).
  - Two new checks, `repo-plugin-pin` and `repo-budget-mode`, read every repository through
    `enrolledRepos`. Each is one row, with one line per affected repository, in U6a's text. Each fix
    names the line to delete.
  - Under `--offline` and `--plugin`, both say "not read (offline)" and make no call.
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_render.go`,
  `internal/cli/doctor_budget_mode.go`, `internal/cli/doctor_repo_notices_test.go` (new).
- Interfaces: consumes C1's free function and U6a's text.
- Tests: `TestDoctorRepoPinMatchesUpdate`, `TestDoctorRepoBudgetMatchesUpdate`,
  `TestDoctorRepoNoticesOfflineMakeNoCall`, `TestDoctorRetiresCheckoutChecks`.
- Command: `go test ./internal/cli/ -run 'TestDoctorRepo|TestDoctorRetiresCheckoutChecks' -count=1`
- Red step: `TestDoctorRepoPinMatchesUpdate` fails before the check exists.
- Depends on: C1 (interface); U6a (behaviour: the text).
- Commit: `update-from-anywhere U6b: doctor's repo-plugin-pin and repo-budget-mode checks`
- Reviewer tier: standard.

### U7a: the HARD RULE's pin clause, the update skill and the README
- Acceptance:
  - `CLAUDE.md`'s HARD RULE replaces "a pin an earlier release committed to a checkout is offered
    its removal as a diff, which the user commits or does not" with: `doctor` and `update` name such
    a pin, and the line to delete. Nothing else in the rule changes (D3340, R11).
  - `plugin/skills/update/SKILL.md` says the same, and its `--check` line says it reads the code
    repository through the installation's credentials service (R17).
  - `README.md:61` follows.
  - A new Docs test pins the new clause in both `CLAUDE.md` and the skill.
  - The `plugin/` tests pass.
- Files: `CLAUDE.md`, `plugin/skills/update/SKILL.md`, `README.md`,
  `internal/cli/docs_update_anywhere_test.go` (new), `plugin/update_skill_test.go`.
- Interfaces: consumes U6a's text and U10's `--check`.
- Tests: `TestDocsUpdateAnywherePinClause`, the Docs tests and the plugin tests.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd plugin && go test ./... -count=1)`
- Red step: `TestDocsUpdateAnywherePinClause` fails on today's sentence.
- Depends on: U6a (behaviour); U10 (behaviour).
- Commit: `update-from-anywhere U7a: the HARD RULE's pin clause and the update skill`
- Reviewer tier: opus, with an independent review (agent instructions).

### U7b: the design and check documents that describe the changed steps
- Acceptance:
  - `docs/design/update-in-flight.md`'s rows for checkout, `budget.mode` and images say that each
    reads every repository's default branch and writes nothing a run holds (A4).
  - `docs/upgrade/spec.md:52` follows.
  - `docs/gcp-live-checklist.md` gets Check 930's row and section, and its pin paragraph (line
    3300) names the notice instead of the diff.
  - `scripts/live-checks/check-884-update-migrates.sh` and its `_test.sh` expect the notice, not
    the diff.
- Files: `docs/design/update-in-flight.md`, `docs/upgrade/spec.md`, `docs/gcp-live-checklist.md`,
  `scripts/live-checks/check-884-update-migrates.sh`,
  `scripts/live-checks/check-884-update-migrates_test.sh`.
- Interfaces: consumes U4, U5 and U6a.
- Tests: the Docs tests, and `bash scripts/live-checks/check-884-update-migrates_test.sh`.
- Command: `go test ./internal/cli/ -run Docs -count=1 && bash scripts/live-checks/check-884-update-migrates_test.sh`
- Red step: the script test fails while it expects the diff and the stage prints the notice.
- Depends on: U4, U5, U6a (behaviour: what the rows describe).
- Commit: `update-from-anywhere U7b: the design rows and check 884 follow`
- Reviewer tier: standard.

### U8: the backlog fold from every default branch
- Acceptance:
  - `backlog.LoadFiles` reads fetched files with `backlog.Load`'s all-or-nothing rule.
  - `foldBacklog` folds each read repository's `docs/backlog/` under that repository's name, once,
    from any directory.
  - Two repositories whose items share an id get one line naming both, and neither item is folded
    (R5).
  - Each item's repository is the repository it was read from, never a field of the file (R20).
  - Under `--plan-only` it reports how many items it would fold.
  - A repository whose host cannot list the tree gets its unread line.
  - The step's lines go through `e.reportRepos("backlog", …)`.
  - The deletion one release later is a follow-up (O2).
- Files: `internal/backlog/files.go` (new), `internal/backlog/files_test.go` (new),
  `internal/cli/update_backlog.go`, `internal/cli/update_backlog_repos_test.go` (new).
- Interfaces: consumes C1 and its extra trees.
- Tests: `TestLoadFilesAllOrNothing`, `TestFoldFromEveryDefaultBranch`, `TestFoldRefusesASharedId`,
  `TestFoldRepositoryIsTheSource`, `TestFoldPlanOnlyCounts`.
- Command: `go test ./internal/backlog/ ./internal/cli/ -run 'TestLoadFiles|TestFold' -count=1`
- Red step: `TestFoldFromEveryDefaultBranch` fails on today's `Locate(".")`.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U8: the backlog fold from every default branch`
- Reviewer tier: opus, with an independent review (backlog items become agents' tasks).

### U9a: the anchor moves into `project repos add`
- Acceptance:
  - `runProjectReposEnroll` keeps its checkout, which it deploys from (D2401, F42), and reads that
    checkout's `fugaro.yaml` as today.
  - When the file has no `gcp_project:` line, `repos add` prints the line and runs today's image
    checks (`gcpProjectProblems`). `anchorProblemText` names `repos add`, not `update --anchor`.
  - It offers the edit through `writeAnchor`'s exact gate. The gate is lifted out of `initRun` into
    a function both doors call (R8, F38).
  - Its help's "read, never written" becomes "read, and written only for the anchor line, through
    the gate, in that repository's own checkout".
- Files: `internal/cli/project_enroll_converge.go`, `internal/cli/project_enroll.go`,
  `internal/cli/init_fugaroyaml.go`, `internal/cli/project_enroll_anchor_test.go` (new).
- Interfaces: consumes the lifted gate.
- Tests: `TestReposAddPrintsTheAnchorLine`, `TestReposAddOffersTheAnchorThroughTheGate`,
  `TestReposAddAnchorRefusedForAnAgent`.
- Command: `go test ./internal/cli/ -run 'TestReposAdd' -count=1`
- Red step: `TestReposAddPrintsTheAnchorLine` fails while `repos add` says nothing of the anchor.
- Depends on: U6a (both write `init_fugaroyaml.go`).
- Commit: `update-from-anywhere U9a: the anchor moves into project repos add`
- Reviewer tier: opus, with an independent review (a write into a checkout, the allow-list path).

### U9b: `update` and `setup` lose `--anchor`
- Acceptance:
  - The flag is gone from `update` and `setup` (D3345, D3351), and so are its exclusions in
    `init.go`. `noteAnchor` is deleted.
  - The repository stage no longer calls `anchor` (`init_repo_stage.go:182, 202`;
    `repositoryStage.anchor` in `init_fugaroyaml.go` goes), so `update` writes no `fugaro.yaml`.
  - The tests that ran `setup --anchor` or `update --anchor` move to `repos add` or go:
    `init_anchor_test.go`, `image_refresh_test.go`, `image_refresh_upgrade_test.go`,
    `setup_test.go` and `init_fugaroyaml_test.go`. `docs_project_centric_test.go`'s pin on
    `s.anchor(…)` goes, and `testdata/init_retire.golden` and `update.go`'s pointer follow.
  - The CLI reference is regenerated.
  - The task touches about fourteen files, most of them tests. It is one change: the flag and every
    test that uses it must go in one commit, or the tree is red.
- Files: `internal/cli/init.go`, `internal/cli/setup.go`, `internal/cli/update.go`,
  `internal/cli/init_anchor.go`, `internal/cli/init_repo_stage.go`,
  `internal/cli/init_fugaroyaml.go`, `internal/cli/init_anchor_test.go`,
  `internal/cli/docs_project_centric_test.go`, `internal/cli/testdata/init_retire.golden`,
  `internal/cli/image_refresh_test.go`, `internal/cli/image_refresh_upgrade_test.go`,
  `internal/cli/setup_test.go`, `internal/cli/init_fugaroyaml_test.go`,
  `website/content/cli-reference.md` (generated).
- Interfaces: none new.
- Tests: `TestUpdateHasNoAnchorFlag`, `TestSetupHasNoAnchorFlag`,
  `TestRepositoryStageWritesNoAnchor`, plus the tests named above as they now stand.
- Command: `go test ./internal/cli/ -run 'TestUpdateHasNoAnchorFlag|TestSetupHasNoAnchorFlag|TestRepositoryStageWritesNoAnchor|TestInitAnchor|TestImageRefresh|TestSetup|TestInitFugaroYAML|TestDocsProjectCentric|TestInitRetire' -count=1`
- Red step: `TestUpdateHasNoAnchorFlag` fails while the flag exists.
- Depends on: U9a (behaviour: the edit has its new door first); U10 (`init.go` and the CLI
  reference).
- Commit: `update-from-anywhere U9b: update and setup lose --anchor`
- Reviewer tier: standard.

### U9c: the code's pointers to `--anchor` name `project repos add`
- Acceptance: these name `fugaro project repos add` instead of `--anchor`:
  - `doctor.go:510–511` and `doctor_render.go:950`;
  - `image_refresh.go:387, 427, 592`;
  - the refusal at `internal/localcfg/select.go:296`;
  - the label at `writers_labels.go:214`.
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_render.go`, `internal/cli/image_refresh.go`,
  `internal/localcfg/select.go`, `internal/cli/writers_labels.go`.
- Interfaces: none.
- Tests: the existing tests of each sentence, as they now read.
- Command: `go test ./internal/cli/ ./internal/localcfg/ -run 'Doctor|ImageRefresh|Select|WritersLabels' -count=1`
- Red step: none of its own; U9d's test fails until both U9c and U9d land.
- Depends on: U9b (behaviour); U4 (`image_refresh.go`); U6b (`doctor.go`, `doctor_render.go`).
- Commit: `update-from-anywhere U9c: the code's pointers name project repos add`
- Reviewer tier: standard.

### U9d: the live documents' pointers, and the test that none remains
- Acceptance:
  - `README.md:120`, `docs/how-to/teammates.md:13`, `docs/release.md:359` and
    `.claude/skills/new-release/SKILL.md:79` name `fugaro project repos add`. The skill's line also
    loses its pin sentence ("offered its removal as a diff"), which becomes the notice.
  - `TestDocsNoAnchorFlagNamed` greps the tree for `update --anchor` and `setup --anchor` and finds
    none. It excludes `docs/releases/` and each `docs/*/{spec,reconciliation,implementation-plan}.md`,
    which stay as written (R14).
- Files: `README.md`, `docs/how-to/teammates.md`, `docs/release.md`,
  `.claude/skills/new-release/SKILL.md`, `internal/cli/docs_no_anchor_flag_test.go` (new).
- Interfaces: none.
- Tests: `TestDocsNoAnchorFlagNamed`, and the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestDocsNoAnchorFlagNamed` fails while any pointer remains.
- Depends on: U9c (behaviour: no code pointer left); U7a (both write `README.md`).
- Commit: `update-from-anywhere U9d: the documents' pointers name project repos add`
- Reviewer tier: opus, with an independent review (`.claude/skills`).

### U10: `update --check` reads the local list through the read mint
- Acceptance:
  - `update --check` takes no PATH.
  - Its `plugin` step is unchanged. `rules` and `policy` stay skipped, with their notes.
  - Its `cloud` step runs per local-config repository through the view, using the local layer file
    and U4's offline staleness variant. It writes nothing and reads no bucket or policy (D3349 as amended,
    R17).
  - It prints one line saying that admit-only repositories are covered by `update --plan-only`.
  - The flag's help says "writes nothing; reads each repository through the credentials service"
    instead of "make no cloud call", and the CLI reference is regenerated.
  - `docs/upgrade/spec.md`'s usage line drops `[PATH]`, and `docs_upgrade_test.go:138` pins the new
    one.
- Files: `internal/cli/update_check.go`, `internal/cli/init.go`,
  `website/content/cli-reference.md` (generated), `internal/cli/update_check_repos_test.go` (new),
  `docs/upgrade/spec.md`, `internal/cli/docs_upgrade_test.go`.
- Interfaces: consumes C1 and U4.
- Tests: `TestUpdateCheckReadsTheLocalList`, `TestUpdateCheckWritesNothing`,
  `TestUpdateCheckTakesNoPath`, plus the Docs tests.
- Command: `go test ./internal/cli/ -run 'TestUpdateCheck|Docs' -count=1`
- Red step: `TestUpdateCheckTakesNoPath` fails while PATH is accepted.
- Depends on: C1 (interface); U3b (`init.go` and the CLI reference); U4 (behaviour: the staleness
  function).
- Commit: `update-from-anywhere U10: update --check reads the local list`
- Reviewer tier: standard.

### U13: the `repos` and `notices` members, and the human per-repository lines
- Acceptance:
  - The loop reads a stage that implements C1's `initflow.RepoReporter`. `initflow.StageResult`
    gains `repos` (a list of `{repo, state, detail}`), and the stage event carries the same list.
  - The document gains a top-level `notices` member, and the stream a `notice` event, for the steps
    after the stage loop and the admit patterns (R12, R19).
  - Human output prints each stage's lines under its header, hint first (`hintFirst`).
  - A stage that reports lines gets a `detail` summary built from them ("3 repositories: 1 done,
    1 needs you, 1 unread"), the same from every directory.
  - The schema, the golden stream and the console's JSON contract are updated. No existing member
    changes.
- Files: `internal/initflow/loop.go`, `internal/cli/setup_events.go`,
  `schemas/setup-events.schema.json`, `internal/cli/testdata/setup_events.golden.ndjson`,
  `console/docs/json-contract.md`.
- Interfaces: consumes C1's `RepoLine` and `RepoReporter`.
- Tests: `TestSetupEventsGolden`, `TestSetupEventsSchema`, `TestLoopReadsRepoReporter`, plus the
  Docs tests.
- Command: `go test ./internal/initflow/ ./internal/cli/ -run 'TestSetupEvents|TestLoopReadsRepoReporter|Docs' -count=1`
- Red step: the schema test fails on an event that carries `repos` before the schema has it.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U13: the repos and notices members`
- Reviewer tier: standard.

### U11: the repository stage over every repository, and the RC39 facts (after L2)
- Acceptance:
  - The repository stage applies each enrolled repository's records from its default branch,
    through L2's per-repository roots.
  - `resolveRepoTarget`'s working-tree comparison and its working-directory read go (R3).
  - It writes the RC39 facts for every repository (D3346, R4), and onboards no repository that is
    not enrolled.
  - I1's exemption of this stage is removed from `update_anywhere_test.go`. The identical-stream
    test and the scan then hold for every stage.
- Files: `internal/cli/init_repo_stage.go`, `internal/cli/init_repo_target.go`,
  `internal/cli/init.go`, `internal/cli/update_anywhere_test.go`,
  `internal/cli/init_repo_repos_test.go` (new).
- Interfaces: consumes the wired engine (I1) and L2's roots.
- Tests: `TestRepositoryStageEveryRepository`, `TestRepositoryStageWritesRC39ForEach`,
  `TestUpdateIsTheSameFromAnyDirectory`, `TestUpdateStagesReadNoWorkingTree`.
- Command: `go test ./internal/cli/ -run 'TestRepositoryStage|TestUpdateIsTheSameFromAnyDirectory|TestUpdateStagesReadNoWorkingTree' -count=1`
- Red step: the identical-stream test, once the exemption is removed, fails on today's
  `repoEngine(ctx, ".")`.
- Depends on: I1 (behaviour); U9b (`init.go`, `init_repo_stage.go`); **L2** (outside this plan;
  see the launch notes).
- Commit: `update-from-anywhere U11: the repository stage over every repository`
- Reviewer tier: opus, with an independent review (it applies Terraform roots).

### I1: integration: the real list, the end-to-end tests
- Kind: integration.
- Acceptance:
  - `update_repos_stub.go` is deleted, and `enrolledReposImpl` calls U2's `readEnrolledRepos`.
    Doctor gets the real list through the same function (R16).
  - A stage's `detail` is a summary that is the same from every directory.
  - The end-to-end tests pass, on a two-repository project (`gcpfake`, the fake provider behind a
    fake read mint):
    - `TestUpdateIsTheSameFromAnyDirectory`: the event stream is byte-identical from an empty
      directory, from each checkout, and from one dirty in a key other than `project:`
      (acceptance 2, F29). This is the real guard against a working-directory read (R10);
    - `TestUpdateStagesReadNoWorkingTree`: the scan of R10 (acceptance 8);
    - `TestUpdateLeavesARunInFlightRunning` (acceptance 7);
    - `TestUpdateFromThePreviousReleaseConverges`: an installation at the previous release
      converges (HARD RULE, R22).

    Both of the first two exempt the repository stage by name, until U11.
- Files: `internal/cli/update_repos_stub.go` (deleted), `internal/cli/update_repos_read.go`,
  `internal/cli/update_anywhere_test.go` (new), `internal/cli/update_in_flight_test.go`.
- Interfaces: re-joins C1 with U2, U3a, U3b, U4, U5, U6a, U6b, U8, U10 and U13.
- Tests: `TestUpdateIsTheSameFromAnyDirectory`, `TestUpdateStagesReadNoWorkingTree`,
  `TestUpdateLeavesARunInFlightRunning`, `TestUpdateFromThePreviousReleaseConverges`.
- Command: `go test ./internal/cli/ -run 'TestUpdateIsTheSameFromAnyDirectory|TestUpdateStagesReadNoWorkingTree|TestUpdateLeavesARunInFlightRunning|TestUpdateFromThePreviousReleaseConverges' -count=1`
- Red step: `TestUpdateIsTheSameFromAnyDirectory` fails while the stub returns only the working
  directory's checkout.
- Depends on: U2, U3a, U3b, U4, U5, U6a, U6b, U8, U10, U13 (integration).
- Commit: `update-from-anywhere I1: the real list, the end-to-end tests`
- Reviewer tier: opus, with an independent review (it wires the read mint into every stage).

### K1: Check 930
- Owner-run, after the release that carries I1. The owner runs
  `scripts/live-checks/check-891-update-trace.sh` on project `fugaro`, from an empty directory and
  from each of the three checkouts, and reads each stage's mark, not only the script's verdict
  (F28).
- Expected:
  - Before U11's release: the repository stage is the only stage marked DIFFERS, and its line names
    L2.
  - After U11's release: no stage differs.
- No agent performs any step of it, and it writes no file.
- Depends on: I1.

### R-view-files: amend contract C1 (only if raised)
- U4 raises it if a stage needs a repository file the view does not carry, such as a Dockerfile a
  workflow names. It amends `update_repos.go` and `contract-repos.md`.
- It reaches U2 and U4. A launched one gets a follow-up run that names the contract file by path.
- Depends on: I1.

## Parallel execution

| Level | Tasks |
| --- | --- |
| 0 | C1, U1 |
| 1 | U2, U3a, U4, U5, U6a, U8, U13 |
| 2 | U3b, U6b, U7b, U9a |
| 3 | U10 |
| 4 | I1, U7a, U9b |
| 5 | U9c; U11 once L2 lands; K1 (owner); R-view-files (only if raised) |
| 6 | U9d |

**Critical path:** C1 → U3a → U3b → U10 → U9b → U9c → U9d. That is the anchor's clean-up, which
blocks nothing else; the core path is C1 → U3a → U3b → U10 → I1 → K1. The read names C1 as the
bottleneck. U11 also waits on L2, outside the graph.

**Sequential, and why:**
- `init.go` and the generated CLI reference are written in the order U3b → U10 → U9b → U11, one
  writer at a time. `initsecrets.go` is written by U3a, then U3b.
- `init_fugaroyaml.go` is written by U6a, then U9a, then U9b.
- U10 needs U4's staleness function.
- U6b prints U6a's text.
- U9b removes the flag only after U9a's door exists. U9c follows U9b, and shares
  `image_refresh.go` with U4 and `doctor.go` and `doctor_render.go` with U6b. U9d's test needs
  U9c, and it shares `README.md` with U7a.
- U7a and U7b describe what U4, U5, U6a and U10 built.
- I1 joins every side, and U11 lifts I1's exemption.

### Shared files

| File | Writers in this plan (level) | Other plans' writers (R7: rebase, never wait) |
| --- | --- | --- |
| `internal/cli/init.go` | U3b (2), U10 (3), U9b (4), U11 (5) | L1 T4 #3786; L8 DK2 |
| `website/content/cli-reference.md` | U3b (2), U10 (3), U9b (4) | any flag change elsewhere: regenerate |
| `internal/cli/initsecrets.go` | U3a (1), U3b (2) | none |
| `internal/cli/init_fugaroyaml.go` | U6a (1), U9a (2), U9b (4) | none |
| `internal/cli/init_stages.go` | C1 (0, one field) | L1 T2a #3766; L8 M2 |
| `internal/cli/doctor.go`, `doctor_render.go` | U6b (2), U9c (5) | image-secrets IS10 (#3802, merged) |
| `internal/cli/image_refresh.go` | U4 (1), U9c (5) | none |
| `internal/cli/init_repo_stage.go` | U9b (4), U11 (5) | none |
| `internal/cli/update_anywhere_test.go` | I1 (4), U11 (5) | none |
| `README.md` | U7a (4), U9d (6) | none |
| `docs/upgrade/spec.md` | U7b (2), U10 (3) | none |
| `internal/cli/update_in_flight_test.go` | I1 (4) | image-secrets IS5; L8 |
| `internal/repofiles/reader.go` | U1 (0) | L8 (M1 merged) |
| `internal/cli/update_repos.go`, `update_repos_stub.go`, `internal/initflow/repos.go` | C1 (0); I1 deletes the stub (4); R-view-files (5) | none |
| `internal/cli/update_repos_read.go` | U2 (1), I1 (4) | none |

No level holds two writers of one file.

## The dependencies the plan broke and kept

**Broken by contract C1, eight edges.** U3a, U4, U5, U6a, U6b, U8, U10 and U13 needed U2 only for
the view's shape and the report call. Each now waits on C1, and I1 re-joins them.

**Kept, each with its reason in the graph:**
- U2 on U1, for its option, branch and head.
- U6b on U6a, for the text.
- U3b on U3a (`initsecrets.go`).
- U10 on U3b (`init.go`) and on U4 (the staleness function).
- U9a on U6a (`init_fugaroyaml.go`).
- U9b on U9a (the door) and on U10 (`init.go`).
- U9c on U9b, U4 and U6b (the flag's removal and shared files); U9d on U9c and U7a.
- U6b on U6a and U10 on U4 could each be broken by a further contract (the notice text, the
  staleness function's signature). Neither is on the core path, so the plan keeps them.
- U7a and U7b on what they describe.
- U11 on I1 and on U9b.
- I1 on every side.
- K1 and R-view-files on I1.

**The gain, as the read reports it:** `8 broken, 19 kept, 3 added; before 8 tasks, weight 15; after
7 tasks, weight 13; improves`.

**The estimate the read printed:** `ready in 486–2018 min, no history yet for 15 tasks (22 tasks;
tiers for 7, seeds for 15; from the cached history of 2026-11-22 07:25 UTC)`. It is informational
and reserves no budget.

## Checks

| Check | What it measures | Who runs it |
| --- | --- | --- |
| 930 | Check 891's script from an empty directory and each checkout on project `fugaro`, read stage by stage, before and after U11 (R3, F28) | The owner, after the release that carries I1, and again after U11's (K1) |

Checks 931–939 are reserved and unused.

## After-deploy steps

The release that carries I1 has this `### For operators` entry: "run `fugaro update`, from any
directory". It also says:
- `update` reads each repository through the installation's credentials service, so there is
  nothing to export. Run it from your own terminal, signed in with `gcloud auth login` as one of
  the installation's operators. In a coding agent's session it reads no repository (D3352);
- a repository it cannot read is named with the fix;
- a missing secret is named with the command to store it, or asked for under `--ask`;
- a leftover plugin pin or `budget.mode: off` line is named for you to delete;
- `--anchor` is now `fugaro project repos add`.

## Considered and declined

- **A reader of L5's own.** L8 M1 is that reader (F1, R1).
- **A token of the admin's own, `gh auth token` or a git credential helper.** The first
  amendment of D3343 was withdrawn: the read mint needs none of them, and a helper could prompt or
  run a repository's configured command (F31).
- **A per-repository namespace for backlog ids.** The fold lives for one release, and refusing a
  shared id loses nothing and changes no id (R5).
- **A pull request per repository for the pin and `budget.mode`.** Declined by the spec, and by
  D3340.
- **Contract edges for U9a, U9b and U11.** U9a reads no repository through the view; U9b and U11
  share files.
- **Moving `serviceReadProvider` to a file of its own.** U2 calls it where it is (round 3).
- **A bound of concurrency in the read.** The spec's cost line asked for one; `repofiles.Read` is a
  sequential loop and U2 adds none. Three calls per repository, tens of repositories per project,
  is far below the host's hourly limit, and a bound adds a second thing per repository that can
  fail. The spec's cost line now says what the read does.
