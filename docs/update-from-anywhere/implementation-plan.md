# Update from anywhere: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which wins.

**Status: nothing is implemented, and no task is approved.** Each task needs the owner's explicit go.

The answers that shaped it are D3340–D3349, all as recommended:
- the pin becomes a notice, and the HARD RULE sentence changes (D3340);
- everything but the repository stage ships first, and the repository stage waits for L2 (D3341);
- every stale derived image is refreshed (D3342);
- the shell's token reads the default branches (D3343);
- the backlog fold runs for one release (D3344);
- `--anchor` moves into `repos add` (D3345);
- the RC39 facts are written for every repository (D3346);
- values are asked only under `--ask` (D3347);
- the hint never narrows `--plan-only` (D3348);
- `--check` reads the local list from the code host (D3349).

## Conventions every part follows

- `CLAUDE.md` throughout, in particular these rules:
  - the HARD RULE on `fugaro update`, safe with runs in flight;
  - Fugaro asks for nothing (`docs/no-prompt-policy/`);
  - hermetic tests (`internal/gcpfake`, the fake provider, `t.TempDir()`), with no network, no
    home directory and no live cloud action by an agent;
  - no secret-shaped literal in a test;
  - comments explain why;
  - targeted tests (`go test ./internal/cli/ -run 'TestX|TestY' -count=1`), and `-run Docs` for a
    documentation change.
- `--json` and `--events` gain a member and change no other one: the `repos` list on a stage
  (spec §Output). "A field never changes meaning."
- Boundaries:
  - **Always:** read a repository from its default branch through `repofiles.Read`; give a
    repository that is unread or needs the owner one named line; keep each stage's refusal of cloud
    writes in an agent's session.
  - **Ask first:** any new cloud write, any new question to the owner, and any change to the HARD
    RULE's other sentences.
  - **Never:**
    - read a working tree in a stage (R10);
    - write to a checkout from `update`;
    - prompt without `--ask`, or under `--json`;
    - onboard a repository from `update`.
- Live checks are the owner's (Check 930). Rigor is `startup` (the project's `rigor.default`), and
  decoupling is on (`planning.decoupling`, true).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| The event stream is byte-identical from an empty directory, from each checkout, and from a checkout with a dirty `fugaro.yaml` (acceptance 2) | I1 |
| No stage function reaches `gitRead(ctx, ".")`, `pluginwire.Locate(".")` or `loadCheckoutConfigAt(ctx, ".")`, except the hint (acceptance 8, R10) | I1 |
| One unread repository never fails the update; the default catalog entry is held while any is unread (acceptance 4, R9) | U5, I1 |
| A secret is never asked for without `--ask`, in an agent's session, or under `--json` (acceptance 3, D3347) | U3 |
| A run in flight is untouched on a two-repository project (acceptance 7) | I1 |
| `update` writes no working tree (acceptance 5) | U6 |

## Slices

1. **The view and the reader** (C1, U1, U2): inert until I1 wires them in.
2. **The stages over every repository** (U3, U4, U5, U6, U8, U10): each ships behind the contract's
   stub, which returns no repositories, so each stage is a no-op until I1.
3. **The doors** (U9): the anchor moves to `project repos add`. It is useful alone.
4. **The wiring** (I1) and **the docs** (U7).
5. **After L2** (U11): the repository stage over every repository.
6. **The owner's check** (K1, Check 930).

## Tasks

### C1: contract: the per-repository view every stage reads
- Kind: contract.
- Acceptance:
  - `internal/cli/update_repos.go` defines:
    - `repoView`: the canonical owner/name, the provider, the default branch and its head commit,
      the `fugaro.yaml` bytes and its parsed `*config.Config`, extra files by path, and `Unread`
      (the one-line reason, empty when read);
    - `repoLine`: `{Repo, State, Detail}`, with the states `done`, `needs you`, `unread` and
      `skipped`;
    - `(e *initEngine) repos(ctx) []repoView`: canonical order, memoized per run;
    - `hintFirst([]repoLine, hint string) []repoLine`: human output only.
  - `internal/cli/update_repos_stub.go` holds `enrolledReposImpl`, which returns no repositories.
  - `contract-repos.md` says what a stage may rely on:
    - the list is the same from every directory;
    - an unread repository carries its reason, and the stage reports it and never guesses;
    - extra files are present only when asked for;
    - no stage reads a working tree.
- Files: `internal/cli/update_repos.go` (new), `internal/cli/update_repos_stub.go` (new),
  `docs/update-from-anywhere/contract-repos.md` (new).
- Interfaces: produces the view, the lines and the stub.
- Tests: `TestRepoViewOrderIsCanonical`, `TestHintFirstMovesOnlyHumanLines`.
- Command: `go test ./internal/cli/ -run 'TestRepoViewOrderIsCanonical|TestHintFirstMovesOnlyHumanLines' -count=1`
- Red step: the tests fail to compile before the types exist.
- Depends on: none.
- Commit: `update-from-anywhere C1: contract: the per-repository view`
- Reviewer tier: standard.

### U1: the reader's extra paths
- Acceptance:
  - `repofiles.Read` takes an option that names extra paths: files such as
    `.claude/settings.json`, and trees such as `docs/backlog/`.
  - It returns them per repository under each one's existing read rule: through `ContentsReader`
    and `TreeReader`, bounded, with the reason when unread.
  - With the option unset, its results are byte-identical to L8 M1's, and L8's tests pass
    unchanged.
- Files: `internal/repofiles/reader.go`, `internal/repofiles/reader_test.go`.
- Interfaces: consumes L8 M1's `repofiles.Read`; produces the option.
- Tests: `TestReadExtraPaths`, `TestReadWithoutExtraPathsIsUnchanged`.
- Command: `go test ./internal/repofiles/ -count=1`
- Red step: `TestReadExtraPaths` fails before the option exists.
- Depends on: L8 M1 (#3816, outside this plan): the package it extends.
- Commit: `update-from-anywhere U1: the reader's extra paths`
- Reviewer tier: standard.

### U2: the enrolled list, read once per run, and the hint
- Acceptance:
  - `readEnrolledRepos` calls `repofiles.Read` with the shell's token (D3343) and the extra paths
    the stages need.
  - It returns the repositories in canonical order, memoized for the run.
  - Each reason it names gives a fix, such as the token variable to set or
    `fugaro project repos rm <repo>` for a 404.
  - `updateHint` (the one allowed reader of the working directory, R10) returns:
    - the project from the working directory's `fugaro.yaml` when `--project` is absent;
    - the repository to list first in human output.

    Neither changes the list.
- Files: `internal/cli/update_repos_read.go` (new), `internal/cli/update_hint.go` (new),
  `internal/cli/update_repos_read_test.go` (new).
- Interfaces: consumes U1's option; produces `readEnrolledRepos` and `updateHint`, which I1 adapts
  to C1's view.
- Tests: `TestEnrolledReposAreTheListReposLsDraws`, `TestEnrolledReposNameTheFix`,
  `TestHintNeverChangesTheList`.
- Command: `go test ./internal/cli/ -run 'TestEnrolledRepos|TestHintNeverChangesTheList' -count=1`
- Red step: the tests fail before the functions exist.
- Depends on: U1 (behaviour: the extra paths it asks for).
- Commit: `update-from-anywhere U2: the enrolled list and the hint`
- Reviewer tier: standard.

### U3: the secrets stage over every repository
- Acceptance:
  - `secretsStage.resolve` reads `e.repos(ctx)` instead of `gitRead(ctx, ".")`, `readOrigin` and
    `defaultBranchConfig`.
  - It computes each repository's wanted names with `infra.SecretMounts`, as today.
  - Without `--ask` it asks nothing (D3347). It prints one line per missing name, with the command
    its builder makes today. A name several repositories want is one line listing them (R2).
  - With `--ask` at the owner's terminal, it asks for each value by repository name. Under an
    agent marker or `--json` it never asks.
  - `projectScope`, D2313's drop and the F21 provider-key line are unchanged.
- Files: `internal/cli/initsecrets.go`, `internal/cli/initsecrets_repos_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestSecretsStageNamesEveryRepository`, `TestSecretsStageAsksOnlyUnderAsk`,
  `TestSecretsStageNeverAsksAnAgentOrJSON`.
- Command: `go test ./internal/cli/ -run 'TestSecretsStage' -count=1`
- Red step: `TestSecretsStageNamesEveryRepository` fails on today's cwd read, using a test view with
  two repositories.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U3: the secrets stage over every repository`
- Reviewer tier: opus, with an independent review (secrets).

### U4: every stale derived image, from its default branch
- Acceptance:
  - The checkout stage no longer calls `pluginwire.Locate(".")`.
  - For each read repository whose derived image is stale, it runs today's staleness test and build
    from the view's `fugaro.yaml` and head commit, with today's confirmation rule (D3342, R6).
  - A repository it cannot build from this machine gets one line naming the reason (U1 of the
    reconciliation). `refreshPreflight` keeps its checkout path for `fugaro image refresh`.
- Files: `internal/cli/init_checkout_upgrade.go`, `internal/cli/upgrade_cloud.go`,
  `internal/cli/image_refresh.go`, `internal/cli/init_checkout_repos_test.go` (new).
- Interfaces: consumes C1. It may raise R-view-files.
- Tests: `TestCheckoutStageRefreshesEveryStaleRepository`,
  `TestCheckoutStageNamesARepositoryItCannotBuild`.
- Command: `go test ./internal/cli/ -run 'TestCheckoutStage' -count=1`
- Red step: the first test fails because today's stage skips outside a checkout.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U4: every stale derived image`
- Reviewer tier: standard.

### U5: image kinds and the default entry over every repository
- Acceptance:
  - `imagesStage.kinds` is the union of base kinds over every read repository's workflows (R9).
  - `staleCatalog` lists stale entries by their building repository, with no "here" split.
  - The policy stage writes the default catalog entry only when every repository was read and none
    refuses. Otherwise it holds the entry and names the unread repositories.
- Files: `internal/cli/init_images.go`, `internal/cli/init_images_catalog.go`,
  `internal/cli/init_policy_migrate.go`, `internal/cli/init_images_repos_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestImageKindsAreTheUnion`, `TestDefaultEntryHeldWhileARepositoryIsUnread`.
- Command: `go test ./internal/cli/ -run 'TestImageKindsAreTheUnion|TestDefaultEntryHeld' -count=1`
- Red step: `TestImageKindsAreTheUnion` fails on today's cwd read.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U5: kinds and the default entry over every repository`
- Reviewer tier: standard.

### U6: the pin and `budget.mode: off` become notices in update and doctor
- Acceptance:
  - For each repository whose default branch has the plugin pin in `.claude/settings.json`, or
    `budget.mode: off` in `fugaro.yaml`, `update` prints one line naming the file and the exact
    edit.
  - `doctor` reports the same text as a check with a fix (D3340).
  - `pluginStage.remove` and `writeBudgetModeRemoval` are no longer reached from `update`, and no
    working tree is written.
  - The plugin's install half runs in a fixed directory.
- Files: `internal/cli/init_plugin_stage.go`, `internal/cli/update_budget_off.go`,
  `internal/cli/doctor_repo_notices.go` (new), `internal/cli/doctor.go`,
  `internal/cli/doctor_repo_notices_test.go` (new).
- Interfaces: consumes C1 and its extra files.
- Tests: `TestPinNoticeSameInUpdateAndDoctor`, `TestBudgetOffNoticeSameInUpdateAndDoctor`,
  `TestUpdateWritesNoWorkingTree`.
- Command: `go test ./internal/cli/ -run 'Notice|TestUpdateWritesNoWorkingTree' -count=1`
- Red step: `TestUpdateWritesNoWorkingTree` fails on today's diff path.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U6: the pin and budget.mode off become notices`
- Reviewer tier: standard.

### U7: the HARD RULE's pin sentence, the update skill and the docs
- Acceptance:
  - `CLAUDE.md`'s HARD RULE says that a pin an earlier release committed is named by `doctor` and
    by `update`, with the line to delete. The rest of the rule is unchanged (D3340, R11).
  - `plugin/skills/update/SKILL.md` says the same.
  - `docs/gcp-live-checklist.md`'s pin paragraph follows.
  - The `plugin/` tests and `go test ./internal/cli/ -run Docs` pass.
- Files: `CLAUDE.md`, `plugin/skills/update/SKILL.md`, `docs/gcp-live-checklist.md`.
- Interfaces: consumes U6's check id and line text.
- Tests: the Docs tests and the plugin tests.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd plugin && go test ./... -count=1)`
- Red step: none (documentation).
- Depends on: U6 (behaviour: the text it pins).
- Commit: `update-from-anywhere U7: the HARD RULE's pin sentence and the update skill`
- Reviewer tier: opus, with an independent review (agent instructions).

### U8: the backlog fold from every default branch
- Acceptance:
  - `backlog` gains `LoadFiles` over fetched name-to-bytes files, with `backlog.Load`'s
    all-or-nothing rule (R5).
  - `foldBacklog` folds each read repository's `docs/backlog/` under that repository's name, from
    any directory, once.
  - Under `--plan-only` it prints how many items it would fold.
  - A repository whose host cannot list the tree gets its unread line.
  - The step is marked for deletion one release later (D3344). The deletion is a follow-up, not
    this task.
- Files: `internal/backlog/files.go` (new), `internal/backlog/files_test.go` (new),
  `internal/cli/update_backlog.go`, `internal/cli/update_backlog_repos_test.go` (new).
- Interfaces: consumes C1 and its extra trees.
- Tests: `TestLoadFilesAllOrNothing`, `TestFoldFromEveryDefaultBranch`, `TestFoldPlanOnlyCounts`.
- Command: `go test ./internal/backlog/ ./internal/cli/ -run 'TestLoadFiles|TestFold' -count=1`
- Red step: `TestFoldFromEveryDefaultBranch` fails on today's `Locate(".")`.
- Depends on: C1 (interface).
- Commit: `update-from-anywhere U8: the backlog fold from every default branch`
- Reviewer tier: standard.

### U9: the anchor moves to `project repos add`
- Acceptance:
  - `fugaro project repos add` reads the repository's default branch.
  - Where it has no `gcp_project:` line, the command prints the line and runs today's image checks
    (`gcpProjectProblems`).
  - Inside that repository's checkout it offers the edit through `writeAnchor`'s exact gate (R8).
  - `update --anchor` and `noteAnchor` are removed (D3345), and `update` no longer writes or hints
    at the anchor.
- Files: `internal/cli/project_enroll.go`, `internal/cli/init_anchor.go`,
  `internal/cli/init_fugaroyaml.go`, `internal/cli/init.go`,
  `internal/cli/project_enroll_anchor_test.go` (new).
- Interfaces: consumes `repofiles.Read` (L8 M1).
- Tests: `TestReposAddPrintsTheAnchorLine`, `TestReposAddOffersTheAnchorInTheCheckout`,
  `TestUpdateHasNoAnchorFlag`.
- Command: `go test ./internal/cli/ -run 'TestReposAdd.*Anchor|TestUpdateHasNoAnchorFlag' -count=1`
- Red step: `TestUpdateHasNoAnchorFlag` fails while the flag exists.
- Depends on: L8 M1 (#3816, outside this plan): the reader.
- Commit: `update-from-anywhere U9: the anchor moves to project repos add`
- Reviewer tier: standard.

### U10: `update --check` reads the local list from the code host
- Acceptance:
  - `update --check` takes no PATH.
  - It reads every repository in the local config through the view (a code-host read, no GCP call,
    D3349) and reports each one's staleness as today.
  - It prints one line saying that admit-only repositories are covered by `update --plan-only`.
  - The flag's help keeps "no cloud call" and says that it reads the code host.
- Files: `internal/cli/update_check.go`, `internal/cli/init.go`,
  `internal/cli/update_check_repos_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestUpdateCheckReadsTheLocalList`, `TestUpdateCheckMakesNoCloudCall`,
  `TestUpdateCheckTakesNoPath`.
- Command: `go test ./internal/cli/ -run 'TestUpdateCheck' -count=1`
- Red step: `TestUpdateCheckTakesNoPath` fails while PATH is accepted.
- Depends on: C1 (interface); U9 (both write `init.go`).
- Commit: `update-from-anywhere U10: update --check reads the local list`
- Reviewer tier: standard.

### U11: the repository stage over every repository, and the RC39 facts (after L2)
- Acceptance:
  - The repository stage applies each enrolled repository's records from its default branch
    through L2's per-repository roots.
  - `resolveRepoTarget`'s working-tree comparison and its cwd read go (R3).
  - It writes the RC39 facts for every repository (D3346, R4).
  - A repository that is not enrolled is never onboarded.
  - Check 891's full pass is possible after this task's release.
- Files: `internal/cli/init_repo_stage.go`, `internal/cli/init_repo_target.go`,
  `internal/cli/init.go`, `internal/cli/init_repo_repos_test.go` (new).
- Interfaces: consumes U2's list and L2's roots.
- Tests: `TestRepositoryStageEveryRepository`, `TestRepositoryStageWritesRC39ForEach`.
- Command: `go test ./internal/cli/ -run 'TestRepositoryStage' -count=1`
- Red step: `TestRepositoryStageEveryRepository` fails on today's `repoEngine(ctx, ".")`.
- Depends on: U2 (behaviour: the list); U10 (both write `init.go`); **L2** (outside this plan,
  D3341).
- Commit: `update-from-anywhere U11: the repository stage over every repository`
- Reviewer tier: opus, with an independent review (it applies Terraform roots).

### I1: integration: the real list in the engine, the `repos` event member, the end-to-end tests
- Kind: integration.
- Acceptance:
  - `update_repos_stub.go` is deleted, and `enrolledReposImpl` calls U2's `readEnrolledRepos`.
  - Every stage's `repoLine`s reach the human output (hint first, through `hintFirst`) and the
    `repos` member of the stage's `--json` element and event (`setup_events.go`). The member is an
    addition.
  - The stage `detail` is a summary that is the same from every directory.
  - The end-to-end tests pass:
    - the event stream on a two-repository project (`gcpfake` and the fake provider) is
      byte-identical from an empty directory, from each checkout and from a dirty one
      (acceptance 2);
    - the source scan holds (acceptance 8, R10);
    - `TestUpdateLeavesARunInFlightRunning` passes on two repositories (acceptance 7).
- Files: `internal/cli/update_repos_stub.go` (deleted), `internal/cli/update_repos_read.go`,
  `internal/cli/init_stages.go`, `internal/cli/setup_events.go`,
  `internal/cli/update_anywhere_test.go` (new), `internal/cli/update_in_flight_test.go`.
- Interfaces: re-joins C1 with U2, U3, U4, U5, U6, U8 and U10.
- Tests: `TestUpdateIsTheSameFromAnyDirectory`, `TestUpdateStagesReadNoWorkingTree`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateIsTheSameFromAnyDirectory|TestUpdateStagesReadNoWorkingTree|TestUpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestUpdateIsTheSameFromAnyDirectory` fails while the stub returns no repositories.
- Depends on: U2, U3, U4, U5, U6, U8, U10 (integration).
- Commit: `update-from-anywhere I1: the real list, the repos member, the end-to-end tests`
- Reviewer tier: standard.

### K1: Check 930
- Owner-run, after the release that carries I1. The owner runs
  `scripts/live-checks/check-891-update-trace.sh` on project `fugaro`, once from an empty directory
  and once from each of the three checkouts.
- Expected:
  - Before U11's release: PASS, with `repository` the only stage marked DIFFERS, its line naming L2
    (R3).
  - After U11's release: no stage differs.
- No agent performs any step of it. It writes no file.
- Depends on: I1.

### R-view-files: amend contract C1 (only if raised)
- U4 raises it if the refresh needs a repository file the view does not carry, such as a Dockerfile
  a workflow names. It amends `update_repos.go` and `contract-repos.md`.
- It reaches U2 and U4. A launched one gets a follow-up run that names the contract file by path.
- Depends on: I1.

## Parallel execution

| Level | Tasks | Runs at once |
| --- | --- | --- |
| 0 | C1, U1, U9 | C1 now; U1 and U9 once L8 M1 (#3816) merges |
| 1 | U2, U3, U4, U5, U6, U8, U10 | all seven |
| 2 | I1, U7, U11 | I1 and U7; U11 once L2 lands |
| 3 | K1 (owner), R-view-files (only if raised) | |

**Critical path:** C1 → U10 → I1 → K1. The read names C1 as the bottleneck. U1 and U9 wait on L8 M1,
outside the graph. U11 waits on L2.

**Sequential, and why:**
- U10 runs after U9, and U11 after U10, because all three write `init.go`.
- U7 runs after U6 because it pins U6's text.
- I1 runs after every stage task because its end-to-end test covers each one.

### Shared files

| File | Writers in this plan (level) | Other plans' in-flight writers (R7: rebase, never wait) |
| --- | --- | --- |
| `internal/cli/init.go` | U9 (0), U10 (1), U11 (2) | L1 T3 #3778, T4 #3786; L8 DK2 |
| `internal/cli/init_stages.go` | I1 (2) | L1 T2a #3766; L8 M2 |
| `internal/cli/update_in_flight_test.go` | I1 (2) | image-secrets IS5; L8 |
| `internal/cli/doctor.go` | U6 (1) | image-secrets IS10 #3802; L8 |
| `internal/repofiles/reader.go` | U1 (0) | L8 M1 #3816 (U1 starts after it merges) |
| `internal/cli/update_repos.go`, `update_repos_stub.go` | C1 (0), I1 deletes the stub (2), R-view-files | none |
| `internal/cli/update_repos_read.go` | U2 (1), I1 (2) | none |

No level holds two writers of one file.

## The dependencies the plan broke and kept

Contract C1 breaks six edges. U3, U4, U5, U6, U8 and U10 each needed U2 only for the view's shape
(the repository, its files, or why it was unread), so each now waits on C1, and I1 re-joins them.

The plan keeps these edges:
- U2 on U1: it reads through U1's option.
- U10 on U9, and U11 on U10: they share `init.go`.
- U11 on U2: it applies each repository's root over the real list.
- U7 on U6: the text it pins.
- I1 on every side.
- K1 and R-view-files on I1.

**The gain, as the read reports it:** `6 broken, 5 kept, 3 added; before 4 tasks, weight 7; after 4
tasks, weight 7; no gain`. Inside the graph, the break saves no weight, because I1's weight replaces
U2's. **It is kept anyway, for a reason the graph cannot see:** U2 waits on U1, and U1 on L8 M1
(#3816), a task of another plan that is still in flight. Without C1, all six stage tasks would wait
on that outside task. With it, they start once C1 merges.

**The estimate the read printed:** `ready in 211–879 min, no history yet for 11 tasks (15 tasks;
tiers for 4, seeds for 11; from the cached history of 2026-11-22 07:25 UTC)`. It is informational
and reserves no budget.

## Checks

| Check | What it measures | Who runs it |
| --- | --- | --- |
| 930 | Check 891's script from an empty directory and from each checkout on project `fugaro`, before and after U11 (R3) | The owner, after the release carrying I1, and again after U11's (K1) |

Checks 931–939 are reserved and unused.

## After-deploy steps

The release that carries I1 has this `### For operators` entry: "run `fugaro update`, from any
directory". It also says:
- a repository `update` cannot read is named with the fix;
- a leftover pin or `budget.mode: off` line is named for you to delete;
- `update --anchor` is now `fugaro project repos add`.

## Considered and declined

- **A reader of L5's own.** L8 M1 is that reader (F1, R1).
- **Contract edges for U9 and U11.** U9 needs only L8's reader, not U2. U11 waits on L2 regardless,
  and a break there would only move it behind I1.
- **A pull request per repository for the pin and `budget.mode`.** Declined by the spec, and by
  D3340.
