# Update from anywhere: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped it are D3340–D3351, all as recommended. D3343 and D3347 were amended
after review and confirmed.
- **D3340:** the pin becomes a notice, and the HARD RULE's clause changes.
- **D3341:** the repository stage waits for L2, and everything else ships first.
- **D3342:** every stale derived image is refreshed.
- **D3343:** the shell's token reads the default branches, falling back to `gh auth token` and the
  git credential helper.
- **D3344:** the backlog fold runs for one release.
- **D3345 and D3351:** `--anchor` leaves `update` and `setup` for `repos add`.
- **D3346:** the RC39 facts are written for every repository.
- **D3347:** `--ask` turns on value entry, and nothing is asked without it.
- **D3348:** the hint never narrows `--plan-only`.
- **D3349:** `--check` reads the local list from the code host.
- **D3350:** a published admit counts as enrolled.

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
    - read a repository from its default branch through C1's view;
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
| No value is asked for without `--ask`, in an agent's session, or under `--json` (acceptance 3, D3347) | U3 |
| A run in flight is untouched on a two-repository project (acceptance 7) | I1 |
| `update` writes no working tree, the anchor included (acceptance 5) | U6a, U9b, I1 |
| Repository-controlled text is sanitised before it is printed (R12) | C1, U13 |
| `doctor --offline` and `--plugin` make no network call (R15) | U6b |

## Launch notes

The graph has no external gate:
- **U1 and U9a** read as ready, but must not launch before L8 M1 (#3816) merges.
- **U11** must not launch before L2 lands.

Whoever launches by plan edges holds these three by hand.

## Slices

1. **The view and the reader** (C1, U1, U2). These are inert until I1 wires them.
2. **The stages over every repository** (U3, U4, U5, U6a, U8, U10) and **the output members**
   (U13). These are no-ops behind the contract's stub until I1.
3. **Doctor** (U6b): its two checks read the default branch.
4. **The anchor** (U9a, U9b, U9c). It moves into `repos add`, and the flag goes from both doors.
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
  - `repoLine` (`Repo`, `State`, `Detail`), with the states `done`, `needs you`, `unread` and
    `skipped`, and `newRepoLine`, which passes the repository and the detail through
    `pluginwire.Printable` and `safetext.OneLine`;
  - the free function `enrolledRepos(ctx, enrolledIn) []repoView` (R16): canonical order, memoized
    per run, with an input that carries the project layer, or the local layer file under
    `--check` (R13);
  - `(e *initEngine) repos(ctx)`, which wraps that function;
  - `(e *initEngine) reportRepos(name string, lines []repoLine)`, for a stage or a step after the
    loop;
  - `hintFirst`, which reorders human lines only.

  `internal/cli/update_repos_stub.go` holds `enrolledReposImpl`, which returns no repositories.
  `contract-repos.md` says what a stage may rely on: the same list from every directory; an unread
  repository carries its reason, and a stage reports it and never guesses; extra files only when
  asked for; no working-tree read.
- Files: `internal/cli/update_repos.go` (new), `internal/cli/update_repos_stub.go` (new),
  `docs/update-from-anywhere/contract-repos.md` (new).
- Interfaces: produces the view, the lines, the report call and the stub.
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
- Depends on: L8 M1 (#3816, outside this plan; see the launch notes).
- Commit: `update-from-anywhere U1: the reader's extra paths, branch and head`
- Reviewer tier: standard.

### U2: the enrolled list, its parse and the token fallback
- Acceptance: `readEnrolledRepos` calls `repofiles.Read` with the extra paths the stages need.
  - **The parse** (R13): each `fugaro.yaml` is parsed against the layer it is given. A missing or
    invalid file is `unread`, naming the cause or the first problem.
  - **The fix in each reason:** the token variable and `gh auth login`, or
    `fugaro project repos rm <repo>` for a 404.
  - **Tokens:** when no token variable is set, it falls back to `gh auth token` for GitHub and to
    the git credential helper, read-only (D3343, amended). That fallback is `update_repos_token.go`.
  - **The hint:** the selector's existing project default is reused; nothing new picks a project
    (A3).
- Files: `internal/cli/update_repos_read.go` (new), `internal/cli/update_repos_token.go` (new),
  `internal/cli/update_repos_read_test.go` (new).
- Interfaces: consumes U1; produces `readEnrolledRepos`, which I1 connects to C1's stub.
- Tests: `TestEnrolledReposAreTheListReposLsDraws`, `TestEnrolledReposMissingOrInvalidIsUnread`,
  `TestEnrolledReposNameTheFix`, `TestTokenFallsBackToGhAndHelper`.
- Command: `go test ./internal/cli/ -run 'TestEnrolledRepos|TestTokenFallsBack' -count=1`
- Red step: the tests fail before the functions exist.
- Depends on: U1 (behaviour).
- Commit: `update-from-anywhere U2: the enrolled list, its parse and the token fallback`
- Reviewer tier: opus, with an independent review (provider tokens).

### U3: the secrets stage over every repository, and `--ask` for values
- Acceptance:
  - `secretsStage.resolve` reads `e.repos(ctx)`, not the working directory.
  - It computes each repository's wanted names with `infra.SecretMounts`, as today.
  - `authorize` treats every repository in the list as enrolled (D3350). The hostile-checkout gate
    stays for `setup --repo` and `--onboard-repo`.
  - Without `--ask` nothing is asked, and each missing name gets one line with today's command; a
    name several repositories want is one line listing them (R2).
  - With `--ask` at the owner's terminal, on any project, a value is asked for by repository name.
    It is never asked for an agent or under `--json` (D3347, amended).
  - `--ask`'s help and the no-prompt reconciliation say so, and the CLI reference is regenerated.
  - `projectScope`, D2313's drop and the F21 provider-key line are unchanged.
  - Every line goes through `e.reportRepos`.
- Files: `internal/cli/initsecrets.go`, `internal/cli/init.go`,
  `docs/no-prompt-policy/reconciliation.md`, `website/content/cli-reference.md` (generated),
  `internal/cli/initsecrets_repos_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestSecretsStageNamesEveryRepository`, `TestSecretsStageAdmitCountsAsEnrolled`,
  `TestSecretsStageAsksOnlyUnderAsk`, `TestSecretsStageNeverAsksAnAgentOrJSON`, plus the Docs tests.
- Command: `go test ./internal/cli/ -run 'TestSecretsStage|Docs' -count=1`
- Red step: `TestSecretsStageNamesEveryRepository` fails on today's working-directory read, using a
  two-repository test view.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U3: the secrets stage over every repository`
- Reviewer tier: opus, with an independent review (secrets, the no-prompt policy).

### U4: every stale derived image, from its default branch
- Acceptance:
  - The checkout stage no longer calls `pluginwire.Locate(".")`.
  - For each read repository it runs today's staleness test and build, from the view's
    `fugaro.yaml` and head commit, with today's confirmation (D3342, R6).
  - `jobConfigCopyNote` runs per repository, from the view.
  - A repository it cannot build from this machine gets one line (V1).
  - It exports a per-repository staleness function, which U10 uses.
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
- Reviewer tier: standard.

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
  - `pluginStage.remove` and `writeBudgetModeRemoval` are no longer reached from `update`, and no
    working tree is written.
  - The plugin's install half runs in a fixed directory, with no `os.Getwd`.
- Files: `internal/cli/init_plugin_stage.go`, `internal/cli/update_budget_off.go`,
  `internal/cli/repo_notices.go` (new), `internal/cli/repo_notices_test.go` (new).
- Interfaces: consumes C1 and its extra files; produces the notice text.
- Tests: `TestPinNoticeFromDefaultBranch`, `TestBudgetOffNoticeFromDefaultBranch`,
  `TestPluginInstallRunsInAFixedDirectory`.
- Command: `go test ./internal/cli/ -run 'TestPinNoticeFromDefaultBranch|TestBudgetOffNoticeFromDefaultBranch|TestPluginInstallRunsInAFixedDirectory' -count=1`
- Red step: `TestPinNoticeFromDefaultBranch` fails on today's diff path.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U6a: the pin and budget.mode off become notices`
- Reviewer tier: opus, with an independent review (how `claude` is spawned).

### U6b: doctor's `plugin-pin` and `budget-mode` checks read the default branch
- Acceptance:
  - Both checks keep their ids. Each reads every repository through `enrolledRepos` and prints
    U6a's text.
  - The `plugin-pin` fix names the line to delete, not "run `fugaro update` in the repository's
    checkout" (R15).
  - Under `--offline` and `--plugin`, both say "not read (offline)" and make no call.
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_render.go`,
  `internal/cli/doctor_budget_mode.go`, `internal/cli/doctor_repo_notices_test.go` (new).
- Interfaces: consumes C1's free function and U6a's text.
- Tests: `TestDoctorPinNoticeMatchesUpdate`, `TestDoctorBudgetNoticeMatchesUpdate`,
  `TestDoctorRepoNoticesOfflineMakeNoCall`.
- Command: `go test ./internal/cli/ -run 'TestDoctorPinNotice|TestDoctorBudgetNotice|TestDoctorRepoNoticesOffline' -count=1`
- Red step: `TestDoctorPinNoticeMatchesUpdate` fails while the check reads the working tree.
- Depends on: C1 (interface); U6a (behaviour: the text).
- Commit: `update-from-anywhere U6b: doctor's notices read the default branch`
- Reviewer tier: standard.

### U7a: the HARD RULE's pin clause, the update skill and the README
- Acceptance:
  - `CLAUDE.md`'s HARD RULE replaces "a pin an earlier release committed to a checkout is offered
    its removal as a diff, which the user commits or does not" with: `doctor` and `update` name such
    a pin, and the line to delete. Nothing else in the rule changes (D3340, R11).
  - `plugin/skills/update/SKILL.md` says the same, and its `--check` line says it reads the code
    host with the user's token (R17).
  - `README.md:61` follows.
  - A new Docs test pins the new clause in both `CLAUDE.md` and the skill.
  - The `plugin/` tests pass.
- Files: `CLAUDE.md`, `plugin/skills/update/SKILL.md`, `README.md`,
  `internal/cli/docs_update_anywhere_test.go` (new).
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
  - `docs/gcp-live-checklist.md` gets Check 930's row and section, and its pin paragraph follows.
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
  - Under `--plan-only` it reports how many items it would fold.
  - A repository whose host cannot list the tree gets its unread line.
  - The step's lines go through `e.reportRepos("backlog", …)`.
  - The deletion one release later is a follow-up (O2).
- Files: `internal/backlog/files.go` (new), `internal/backlog/files_test.go` (new),
  `internal/cli/update_backlog.go`, `internal/cli/update_backlog_repos_test.go` (new).
- Interfaces: consumes C1 and its extra trees.
- Tests: `TestLoadFilesAllOrNothing`, `TestFoldFromEveryDefaultBranch`, `TestFoldRefusesASharedId`,
  `TestFoldPlanOnlyCounts`.
- Command: `go test ./internal/backlog/ ./internal/cli/ -run 'TestLoadFiles|TestFold' -count=1`
- Red step: `TestFoldFromEveryDefaultBranch` fails on today's `Locate(".")`.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U8: the backlog fold from every default branch`
- Reviewer tier: opus, with an independent review (backlog items become agents' tasks).

### U9a: the anchor moves into `project repos add`
- Acceptance:
  - `runProjectReposEnroll` reads the repository's `fugaro.yaml` from the default branch through
    `repofiles.Read`, and no longer requires a checkout.
  - When the file has no `gcp_project:` line, `repos add` prints the line and runs today's image
    checks (`gcpProjectProblems`).
  - Run in that repository's checkout, it offers the edit through `writeAnchor`'s exact gate (R8).
  - Its help's "read, never written" becomes "read, and written only for the anchor line, through
    the gate, in that repository's own checkout".
- Files: `internal/cli/project_enroll_converge.go`, `internal/cli/project_enroll.go`,
  `internal/cli/project_enroll_anchor_test.go` (new).
- Interfaces: consumes `repofiles.Read` (L8 M1) and `writeAnchor`.
- Tests: `TestReposAddReadsTheDefaultBranch`, `TestReposAddPrintsTheAnchorLine`,
  `TestReposAddOffersTheAnchorInTheCheckout`.
- Command: `go test ./internal/cli/ -run 'TestReposAdd' -count=1`
- Red step: `TestReposAddReadsTheDefaultBranch` fails while the converge needs a checkout.
- Depends on: L8 M1 (#3816, outside this plan; see the launch notes).
- Commit: `update-from-anywhere U9a: the anchor moves into project repos add`
- Reviewer tier: opus, with an independent review (a write into a checkout, the allow-list path).

### U9b: `update` and `setup` lose `--anchor`
- Acceptance:
  - The flag is gone from `update` and `setup` (D3345, D3351). `noteAnchor` is deleted.
  - The repository stage no longer calls `anchor` (`init_repo_stage.go:178, 198`), so `update`
    writes no `fugaro.yaml`.
  - `update.go`'s pointer and `testdata/init_retire.golden` follow.
  - The CLI reference is regenerated.
- Files: `internal/cli/init.go`, `internal/cli/setup.go`, `internal/cli/update.go`,
  `internal/cli/init_anchor.go`, `internal/cli/init_repo_stage.go`,
  `internal/cli/testdata/init_retire.golden`, `website/content/cli-reference.md` (generated).
- Interfaces: none new.
- Tests: `TestUpdateHasNoAnchorFlag`, `TestSetupHasNoAnchorFlag`,
  `TestRepositoryStageWritesNoAnchor`, plus the golden and Docs tests.
- Command: `go test ./internal/cli/ -run 'TestUpdateHasNoAnchorFlag|TestSetupHasNoAnchorFlag|TestRepositoryStageWritesNoAnchor|Golden|Docs' -count=1`
- Red step: `TestUpdateHasNoAnchorFlag` fails while the flag exists.
- Depends on: U9a (behaviour: the edit has its new door first); U10 (`init.go` and the CLI
  reference).
- Commit: `update-from-anywhere U9b: update and setup lose --anchor`
- Reviewer tier: standard.

### U9c: every pointer to `--anchor` names `project repos add`
- Acceptance: these places name `fugaro project repos add` instead of `--anchor`, and no file in the
  tree still names `update --anchor` or `setup --anchor` (a `git grep` in the test):
  - `doctor.go:500` and `doctor_render.go:921`;
  - `image_refresh.go:387, 427, 592`;
  - `README.md:120`;
  - `docs/how-to/teammates.md:13`;
  - `.claude/skills/new-release/SKILL.md:79`.
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_render.go`, `internal/cli/image_refresh.go`,
  `README.md`, `docs/how-to/teammates.md`, `.claude/skills/new-release/SKILL.md`.
- Interfaces: none.
- Tests: `TestDocsNoAnchorFlagNamed`, and the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestDocsNoAnchorFlagNamed` fails while any pointer remains.
- Depends on: U9b (behaviour); U4 (`image_refresh.go`); U6b (`doctor.go`, `doctor_render.go`).
- Commit: `update-from-anywhere U9c: every pointer names project repos add`
- Reviewer tier: opus, with an independent review (`.claude/skills`).

### U10: `update --check` reads the local list from the code host
- Acceptance:
  - `update --check` takes no PATH.
  - Its `plugin` step is unchanged. `rules` and `policy` stay skipped, with their notes.
  - Its `cloud` step runs per local-config repository through the view, using the local layer file
    and U4's staleness function, with no GCP call (D3349, R17).
  - It prints one line saying that admit-only repositories are covered by `update --plan-only`.
  - The flag's help keeps "no cloud call" and adds that it reads the code host; the CLI reference
    is regenerated.
- Files: `internal/cli/update_check.go`, `internal/cli/init.go`,
  `website/content/cli-reference.md` (generated), `internal/cli/update_check_repos_test.go` (new).
- Interfaces: consumes C1 and U4.
- Tests: `TestUpdateCheckReadsTheLocalList`, `TestUpdateCheckMakesNoCloudCall`,
  `TestUpdateCheckTakesNoPath`, plus the Docs tests.
- Command: `go test ./internal/cli/ -run 'TestUpdateCheck|Docs' -count=1`
- Red step: `TestUpdateCheckTakesNoPath` fails while PATH is accepted.
- Depends on: C1 (interface); U3 (`init.go` and the CLI reference); U4 (behaviour: the staleness
  function).
- Commit: `update-from-anywhere U10: update --check reads the local list`
- Reviewer tier: standard.

### U13: the `repos` member and the `notices` member in `--json` and `--events`
- Acceptance:
  - `initflow.StageResult` gains `repos` (a list of `{repo, state, detail}`). The stage event
    carries the same list.
  - The document gains a top-level `notices` member, and the stream a `notice` event, for the steps
    after the stage loop (R12).
  - The schema, the golden stream and the console's JSON contract are updated.
  - No existing member changes.
- Files: `internal/initflow/loop.go`, `internal/cli/setup_events.go`,
  `schemas/setup-events.schema.json`, `internal/cli/testdata/setup_events.golden.ndjson`,
  `console/docs/json-contract.md`.
- Interfaces: consumes C1's `repoLine`.
- Tests: `TestSetupEventsGolden`, `TestSetupEventsSchema`, plus the Docs tests.
- Command: `go test ./internal/initflow/ ./internal/cli/ -run 'TestSetupEvents|Docs' -count=1`
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

### I1: integration: the real list, the reports wired to the output, the end-to-end tests
- Kind: integration.
- Acceptance:
  - `update_repos_stub.go` is deleted, and `enrolledReposImpl` calls U2's `readEnrolledRepos`.
    Doctor gets the real list through the same function (R16).
  - Every `reportRepos` call reaches the human output (hint first) and U13's members. A stage's
    `detail` is a summary that is the same from every directory.
  - The end-to-end tests pass, on a two-repository project (`gcpfake` and the fake provider):
    - `TestUpdateIsTheSameFromAnyDirectory`: the event stream is byte-identical from an empty
      directory, from each checkout, and from one dirty in a key other than `project:`
      (acceptance 2, F29);
    - `TestUpdateStagesReadNoWorkingTree`: the scan of R10 (acceptance 8);
    - `TestUpdateLeavesARunInFlightRunning` (acceptance 7).

    Both of the first two exempt the repository stage by name, until U11.
- Files: `internal/cli/update_repos_stub.go` (deleted), `internal/cli/update_repos_read.go`,
  `internal/cli/init_stages.go`, `internal/cli/update_anywhere_test.go` (new),
  `internal/cli/update_in_flight_test.go`.
- Interfaces: re-joins C1 with U2, U3, U4, U5, U6a, U6b, U8, U10 and U13.
- Tests: `TestUpdateIsTheSameFromAnyDirectory`, `TestUpdateStagesReadNoWorkingTree`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateIsTheSameFromAnyDirectory|TestUpdateStagesReadNoWorkingTree|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestUpdateIsTheSameFromAnyDirectory` fails while the stub returns no repositories.
- Depends on: U2, U3, U4, U5, U6a, U6b, U8, U10, U13 (integration).
- Commit: `update-from-anywhere I1: the real list, the reports wired, the end-to-end tests`
- Reviewer tier: standard.

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
| 0 | C1; U1 and U9a once L8 M1 merges |
| 1 | U2, U3, U4, U5, U6a, U8, U13 |
| 2 | U6b, U7b, U10 |
| 3 | I1, U7a, U9b |
| 4 | U9c; U11 once L2 lands; K1 (owner); R-view-files (only if raised) |

**Critical path:** C1 → U3 → U10 → I1 → K1. The read names C1 as the bottleneck. U1 and U9a also
wait on L8 M1, and U11 on L2, all outside the graph.

**Sequential, and why:**
- `init.go` and the generated CLI reference are written in the order U3 → U10 → U9b → U11, one
  writer at a time.
- U10 needs U4's staleness function.
- U6b prints U6a's text.
- U9b removes the flag only after U9a's door exists. U9c follows U9b, and shares
  `image_refresh.go` with U4 and `doctor.go` and `doctor_render.go` with U6b.
- U7a and U7b describe what U4, U5, U6a and U10 built.
- I1 joins every side, and U11 lifts I1's exemption.

### Shared files

| File | Writers in this plan (level) | Other plans' in-flight writers (R7: rebase, never wait) |
| --- | --- | --- |
| `internal/cli/init.go` | U3 (1), U10 (2), U9b (3), U11 (4) | L1 T4 #3786; L8 DK2 |
| `website/content/cli-reference.md` | U3 (1), U10 (2), U9b (3) | any flag change elsewhere: regenerate |
| `internal/cli/doctor.go`, `doctor_render.go` | U6b (2), U9c (4) | image-secrets IS10 #3802 |
| `internal/cli/image_refresh.go` | U4 (1), U9c (4) | none |
| `internal/cli/init_repo_stage.go` | U9b (3), U11 (4) | none |
| `internal/cli/update_anywhere_test.go` | I1 (3), U11 (4) | none |
| `README.md` | U7a (3), U9c (4) | none |
| `internal/cli/init_stages.go` | I1 (3) | L1 T2a #3766; L8 M2 |
| `internal/cli/update_in_flight_test.go` | I1 (3) | image-secrets IS5; L8 |
| `internal/repofiles/reader.go` | U1 (0) | L8 M1 #3816 (U1 starts after it merges) |
| `internal/cli/update_repos.go`, `update_repos_stub.go` | C1 (0); I1 deletes the stub (3); R-view-files (4) | none |
| `internal/cli/update_repos_read.go` | U2 (1), I1 (3) | none |

No level holds two writers of one file.

## The dependencies the plan broke and kept

**Broken by contract C1, eight edges.** U3, U4, U5, U6a, U6b, U8, U10 and U13 needed U2 only for the
view's shape and the report call. Each now waits on C1, and I1 re-joins them.

**Kept, each with its reason in the graph:**
- U2 on U1, for its option, branch and head.
- U6b on U6a, for the text.
- U10 on U3 (`init.go`) and on U4 (the staleness function).
- U9b on U9a (the door) and on U10 (`init.go`).
- U9c on U9b, U4 and U6b (the flag's removal and shared files).
- U7a and U7b on what they describe.
- U11 on I1 and on U9b.
- I1 on every side.
- K1 and R-view-files on I1.

**The gain, as the read reports it:** `8 broken, 15 kept, 3 added; before 6 tasks, weight 12; after
5 tasks, weight 10; improves`. The break also frees the eight consumers from L8 M1 (#3816), which
U2 waits on through U1 and which the graph cannot see.

**The estimate the read printed:** `ready in 366–1538 min, no history yet for 14 tasks (20 tasks;
tiers for 6, seeds for 14; from the cached history of 2026-11-22 07:25 UTC)`. It is informational
and reserves no budget.

## Checks

| Check | What it measures | Who runs it |
| --- | --- | --- |
| 930 | Check 891's script from an empty directory and each checkout on project `fugaro`, read stage by stage, before and after U11 (R3, F28) | The owner, after the release that carries I1, and again after U11's (K1) |

Checks 931–939 are reserved and unused.

## After-deploy steps

The release that carries I1 has this `### For operators` entry: "run `fugaro update`, from any
directory". It also says:
- `update` reads each repository from its code host with your token: a `GH_TOKEN` or
  `FUGARO_GIT_TOKEN`, or your `gh auth login`;
- a repository it cannot read is named with the fix;
- a missing secret is named with the command to store it, or asked for under `--ask`;
- a leftover plugin pin or `budget.mode: off` line is named for you to delete;
- `--anchor` is now `fugaro project repos add`.

## Considered and declined

- **A reader of L5's own.** L8 M1 is that reader (F1, R1).
- **A per-repository namespace for backlog ids.** The fold lives for one release, and refusing a
  shared id loses nothing and changes no id (R5).
- **A pull request per repository for the pin and `budget.mode`.** Declined by the spec, and by
  D3340.
- **Contract edges for U9a, U9b and U11.** None of them needs U2's interface alone: U9a reads
  through L8's reader, and U9b and U11 share files.
