# Forge, authorship and images: implementation plan

## Status

**Plan only. Nothing is implemented. No task is approved: each task needs the owner's own explicit
go, and answering a decision is not approving a task.** It builds [spec.md](spec.md) **as amended by
[reconciliation.md](reconciliation.md)**, which wins where they differ.

**No owner answer shapes this plan yet.** Decisions D600–D639 are written with recommendations and
**empty answer cells** (the owner could not be asked while this was written). The plan follows the
**recommendations**; every task names the decisions it rests on, and a task whose decision is
answered otherwise is re-planned before it starts. Tasks marked **GATED** are not planned in detail
because their decision may remove them.

Slices, each independent and orderable (D600 recommends A first, alone):

| Slice | What it ships | Licence (D637, proposed) | Gated on |
| --- | --- | --- | --- |
| **A** Authorship and trailers | Commits authored by the launcher when a repository opts in; `Fugaro-Run`, `Fugaro-Rigor`, `Fugaro-Model` trailers on every run commit | MIT | D600–D606 |
| **B** Forge cleanup, CI status, GitLab | One list of forge kinds; a CI-status read; GitLab only after its own reconciliation | MIT | D610, D611, D614/D615 for B2 |
| **C** Repositories and images | Bitbucket listing and a local resolver in `fugaro project sync`; lockfile-first image layers; a digest check at launch; read-only context repositories; a SECURITY.md registry note | MIT | D613, D617–D625 |
| **D** Change sets | Linked runs with a change-set ID, a draft hold, a ready-together step, advisory merge order | Linking MIT; settle and order `ee/` | D613–D616; ee-reorg's coordinator move |
| **E** People and tokens | The roles design (D446) first; `fugaro user` over the existing IAM lists; `fugaro token add` over `secrets set`; `fugaro join` | MIT for E1–E3; the rest per E0 | D627–D638 |

## Conventions every part follows

- `CLAUDE.md` (targeted tests; docs are tested: `go test ./internal/cli/ -run Docs`; comments explain
  why; no secret-shaped literals; hermetic tests with `internal/gcpfake`, `internal/testutil`,
  `t.TempDir()`; release-note `### For operators` entries for post-deploy steps).
- `.claude/rules/credentials-service.md` for C5b and every E task that touches `internal/creds`,
  `internal/credsvc` or `SECURITY.md`; `.claude/rules/terraform.md` for any Terraform change;
  `.claude/rules/console.md` and `.claude/rules/mobile.md` for E's GATED console and mobile parts.
- **Strict decoding and skew.** `task.Spec` and `fugaro.yaml` decode with unknown fields refused. A
  new task field follows the `Rigor` precedent: the launcher gates a launch that would set it on an
  older runner (`rigorRequestSince`, `internal/cli/recipes_skew.go`), and a spec without it stays
  byte-identical. Schemas in `schemas/` change in the same task.
- **Plugin skills.** A command named in `plugin/skills` is checked against the CLI; a task that adds
  a command a skill should name updates the skill in the same PR.
- **The CLI command list** (`internal/cli/root.go`) is being reshaped by open PR #1252 (a command
  registry); E1–E3 follow whatever is on `main` when they start.
- **No agent performs a live step**: no apply, no App permission change, no sandbox run. Those are
  OWNER-RUN, on a sandbox repository and installation only.

## Global constraints and review focus

1. **Stamping precedes verification** (F2). A rewritten commit changes its ID; `Decide` compares
   test records with the final SHA. Pinned by A3's `TestRunnerStampsBeforeVerify`.
2. **The default does not change** (D606). With no `git.author` key, author and committer stay
   `Fugaro <fugaro@users.noreply.invalid>`; only the trailers are new. Pinned by A3's
   `TestRunnerDefaultAuthorUnchanged`.
3. **No new long-lived secret in a job** (R3). Pinned by C5b's and C5c's reviews.
4. **No `Signed-off-by` is ever written by Fugaro, and one the agent writes is removed** (R1).
   Pinned by A1's `TestStampRemovesSignedOffBy`.
5. **The agent cannot forge a trailer** (F1): any `Fugaro-*` trailer the agent wrote is replaced.
   Pinned by A1's `TestStampReplacesAgentFugaroTrailers` and A3's
   `TestRunnerFinalizeRestampsUnstampedCommits`.
6. **The allow-list stays the membership boundary** (F19, D622): resolver output never widens it.
   Pinned by C2's `TestProjectSyncResolverIsIntersectedWithAllowRepos`.
7. **Context repositories are read-only** (D613): no write token, no push, refused unless the policy
   admits each one. Pinned by C5b's and C5c's tests.
8. **The author is a claim** (F3, D604): docs and SECURITY.md say so until Slice E's identity exists.
   Pinned by A4's docs test.

## Tasks

### Slice A: authorship and trailers

#### A1. A commit stamp in `gitops`

- **Decisions:** D601, D602, D603, R1.
- **Files:** `internal/gitops/stamp.go` (new), `internal/gitops/stamp_test.go` (new).
- **Interfaces produced:** `type Stamp struct { AuthorName, AuthorEmail string; Trailers []Trailer }`
  (empty author fields keep `gitops.Identity`); `func (r *Repo) StampRange(ctx, base string, s Stamp)
  (head string, rewritten int, err error)`: rewrites every commit in `base..HEAD` (linear history
  only; a merge commit is an error the caller reports), keeping each tree, setting author and
  committer (committer always `gitops.Identity`), removing `Signed-off-by` and any `Fugaro-*`
  trailer, appending `s.Trailers` once. Idempotent.
- **Schema/rules/config:** none.
- **Tests:** `TestStampRewritesAuthorAndTrailers`, `TestStampKeepsTrees`,
  `TestStampReplacesAgentFugaroTrailers`, `TestStampRemovesSignedOffBy`,
  `TestStampLeavesCommitsBeforeBaseAlone`, `TestStampIsIdempotent`, `TestStampRefusesMergeCommits`,
  `TestStampEmptyAuthorKeepsFugaroIdentity`.
- **Command:** `go test ./internal/gitops/ -run 'TestStamp' -count=1`
- **Depends on:** none.
- **Red step:** the tests reference `StampRange`, which does not exist: compile failure.
- **Commit:** `gitops: a commit stamp that sets the author and Fugaro trailers on a range`
- **Reviewer:** standard.

#### A2a. The author in the task, and the repository's opt-in

- **Decisions:** D604, D606.
- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/config/config.go`
  (`Git.Author`: `fugaro` or `launcher`), `internal/config/validate.go`,
  `schemas/fugaro.schema.json`, `internal/task/task_test.go`, `internal/config/validate_test.go`.
- **Interfaces produced:** `task.Spec.Author *task.Person` (`name`, `email`; omitted when empty);
  `config.Git.Author string`.
- **Schema change:** both schemas gain the field.
- **Tests:** `TestSpecAuthorRoundTrips`, `TestSpecWithoutAuthorIsByteIdentical`,
  `TestValidateGitAuthorClosedVocabulary`, `TestSchemaCoversGitAuthor` (the schemas package's
  existing coverage test picks up the field).
- **Command:** `go test ./internal/task/ ./internal/config/ ./schemas/ -run 'Author' -count=1`
- **Depends on:** none.
- **Red step:** the new tests reference `Spec.Author` and `Git.Author`: compile failure.
- **Commit:** `task, config: the launcher as a task field, and git.author as a repository opt-in`
- **Reviewer:** standard.

#### A2b. The launcher fills the author

- **Decisions:** D604, D605, D606.
- **Files:** `internal/localcfg/localcfg.go` (optional `commit_name`, `commit_email`),
  `internal/cli/run.go`, `internal/cli/recipes_skew.go` (an `authorSince` gate),
  `internal/cli/run_author_test.go` (new).
- **Interfaces consumed:** `localcfg.Config.Me`, `task.Spec.Author`.
- **Behaviour:** the author is `commit_email` (else `Me`) with `commit_name` (else `git config
  user.name`, else the email's local part); omitted when `Me` is empty or the launch is a review or
  artifact run (D605); omitted against an older runner (the skew gate).
- **Tests:** `TestRunRecordsAuthorFromLocalConfig`, `TestRunCommitEmailOverridesMe`,
  `TestRunOmitsAuthorWithoutIdentity`, `TestRunOmitsAuthorForOlderRunner`,
  `TestReviewRunCarriesNoAuthor`.
- **Command:** `go test ./internal/cli/ -run 'TestRun.*Author|TestReviewRunCarriesNoAuthor' -count=1`
- **Depends on:** A2a.
- **Red step:** `TestRunRecordsAuthorFromLocalConfig` fails: the written task has no author.
- **Commit:** `cli: fugaro run records the launcher as the commit author`
- **Reviewer:** standard.

#### A3. The runner stamps every run commit

- **Decisions:** D601–D605; Global Constraints 1, 2, 4, 5.
- **Files:** `internal/runner/runner.go` (after each agent stage returns and before the gate's
  verification; the finalize check), `internal/runner/checkpoint.go` (before a mid-stage push),
  `internal/runner/stamp_test.go` (new).
- **Interfaces consumed:** `gitops.Repo.StampRange`, `task.Spec.Author`, `config.Git.Author`,
  `runstore.Record.RunID`, `Record.Rigor`, the stage's configured model (`StageTiming.Model`).
- **Behaviour:** base = the last pushed tip (else the task's base); author applied only when
  `git.author: launcher` and `Spec.Author` is set; trailers always. Finalize re-stamps any
  unstamped commit before its push (an agent push in between is overwritten by the existing
  `--force-with-lease` flow or reported). Review and artifact runs never stamp (they never push).
- **Tests:** `TestRunnerStampsBeforeVerify`, `TestRunnerCheckpointPushesStampedCommits`,
  `TestRunnerFinalizeRestampsUnstampedCommits`, `TestRunnerDefaultAuthorUnchanged`,
  `TestRunnerAuthorOptIn`, `TestRunnerModelTrailerIsTheStagePin`,
  `TestReviewAndArtifactRunsNeverStamp`, `TestRunnerMergeCommitIsReportedNotPushedUnstamped`.
- **Command:** `go test ./internal/runner/ -run 'TestRunner.*Stamp|TestRunnerDefaultAuthorUnchanged|TestRunnerAuthorOptIn|TestRunnerModelTrailer|TestReviewAndArtifactRunsNeverStamp|TestRunnerMergeCommit' -count=1 -timeout 20m`
- **Depends on:** A1, A2a, A2b.
- **Red step:** `TestRunnerStampsBeforeVerify` fails: the pushed commit has no `Fugaro-Run` trailer.
- **Commit:** `runner: stamp each stage's commits with the author and Fugaro trailers before verification`
- **Reviewer:** standard, plus an independent review (it touches the push and readiness path).

#### A4. Docs

- **Files:** `docs/git-providers.md` (a "Commit author and trailers" section), `SECURITY.md` ("Known
  limits": the author is the launcher's claim; trailers are a record, not proof),
  `docs/releases/` next unreleased notes (one Highlights line; no operator step),
  `internal/cli/docs_commit_author_test.go` (new: pins the two sentences).
- **Tests:** `TestDocsCommitAuthorIsAClaim`, `TestDocsTrailerNames`.
- **Command:** `go test ./internal/cli/ -run 'Docs' -count=1`
- **Depends on:** A3.
- **Red step:** the new docs test fails before the sections exist.
- **Commit:** `docs: commit author and Fugaro trailers, and what they do not prove`
- **Reviewer:** standard.

#### A5. Check 230 (OWNER-RUN)

- **Measures:** on a sandbox GitHub repository and a sandbox Bitbucket repository, with
  `git.author: launcher` committed: the run's commits show the launcher as author, Fugaro as
  committer, the three trailers; blame and the PR's commit list show the launcher; no
  `Signed-off-by`; and with the key absent, the author is Fugaro.
- **The owner types,** in each sandbox checkout:
  `fugaro run --title "Check 230" "Add a line to CHECK230.md"`, then on the PR branch
  `git log --format='%an <%ae> | %cn <%ce>%n%(trailers)' origin/main..` and records the result in
  `docs/gcp-live-checklist.md` under Check 230.
- **Written by:** A4 (the checklist entry). **Run by:** the owner. **Depends on:** a release with A3.

### Slice B: forge

#### B1. One list of forge kinds

- **Decisions:** D610, D611.
- **Files:** `internal/gitprov/kinds.go` (new), `internal/gitprov/kinds_test.go` (new),
  `internal/gitprov/gitprov.go`, `internal/gitprov/providers/providers.go`.
- **Behaviour:** `gitprov.Kinds` lists the forge kinds; `providers.FromEnv` and its error text read
  it; a test lists every other switch on a kind in the tree (config validation, the image build's
  credential step, Terraform secret names, the credentials service, the skills) so a third kind
  cannot be added in one place only. No behaviour change.
- **Tests:** `TestKindsListedOnce`, `TestEveryKindSwitchIsInventoried`, the existing
  `internal/gitprov/providers` tests unchanged.
- **Command:** `go test ./internal/gitprov/... -run 'Kind|FromEnv' -count=1`
- **Depends on:** none.
- **Red step:** `TestEveryKindSwitchIsInventoried` fails while the inventory is empty.
- **Commit:** `gitprov: one list of forge kinds, and an inventory of every switch on them`
- **Reviewer:** standard.

#### B2. A CI-status read (GATED: D614 or D615 must need it)

- **Files:** `internal/gitprov/gitprov.go` (`Checks(ctx, sha) ([]CheckState, error)`),
  `internal/gitprov/github/checks.go` (new), `internal/gitprov/bitbucket/statuses.go` (new),
  `internal/gitprov/fake/` (the fake's method), tests beside each, `docs/git-providers.md` (the App
  gains **Checks: read** and **Commit statuses: read**).
- **Tests:** `TestGitHubChecksCombinesRunsAndStatuses`, `TestBitbucketStatusesMapToCheckState`,
  `TestFakeChecks`, recorded fixtures in each adapter's `testdata/`.
- **Command:** `go test ./internal/gitprov/... -run 'Checks|Statuses' -count=1`
- **Depends on:** B1.
- **Red step:** compile failure: `Checks` is not on `Provider`.
- **Commit:** `gitprov: read CI status for a commit`
- **Release notes:** `### For operators`: accept the App's new permissions on every installation.
- **OWNER-RUN:** Check 231: the owner adds the two read permissions in the App settings, accepts them
  on the sandbox installation, and runs the recorded-fixture refresh command the task names.
- **Reviewer:** opus + mandatory independent review (forge permissions).

#### B3. GitLab (GATED: D610 answered "build")

A docs-only reconciliation in this folder first: tokens (project or group access tokens and what they
cost), `Draft:` semantics, the image build's credential step, the credentials service's mint, the
secrets and Terraform, using B1's inventory as the list of places. The adapter tasks are written from
it. **Not sized here** beyond: L, security-sensitive throughout.

### Slice C: repositories and images

#### C1. `fugaro project sync` for Bitbucket, with `--check`

- **Decisions:** D623.
- **Files:** `internal/cli/project_enroll.go`, `internal/gitprov/bitbucket/list.go` (new),
  `internal/gitprov/bitbucket/list_test.go` (new), `internal/cli/project_enroll_test.go`.
- **Behaviour:** lists the workspace's or project's repositories with a token read from stdin or a
  hidden prompt (never argv), offers the ones `allow_repos` does not admit, confirms as today;
  `--check` prints the difference and writes nothing.
- **Tests:** `TestBitbucketListPaginates`, `TestProjectSyncBitbucketOffersUnadmitted`,
  `TestProjectSyncCheckWritesNothing`, `TestProjectSyncBitbucketTokenNeverFromArgs`.
- **Command:** `go test ./internal/cli/ ./internal/gitprov/bitbucket/ -run 'ProjectSync|BitbucketList' -count=1`
- **Depends on:** none.
- **Red step:** `TestProjectSyncCheckWritesNothing` fails: `--check` is an unknown flag.
- **Commit:** `cli: fugaro project sync lists Bitbucket repositories, and --check only reports`
- **OWNER-RUN:** Check 234 (`fugaro project sync --check` against the sandbox Bitbucket workspace).
- **Reviewer:** opus + mandatory independent review (handles a workspace credential).

#### C2. A local resolver command in `fugaro project sync`

- **Decisions:** D622; Global Constraint 6.
- **Files:** `internal/cli/project_enroll.go`, `internal/localcfg/localcfg.go`
  (`project_sync.resolver`: an argv list), `internal/cli/project_sync_resolver_test.go` (new).
- **Tests:** `TestProjectSyncResolverIsIntersectedWithAllowRepos`,
  `TestProjectSyncResolverDroppedEntriesWarn`, `TestProjectSyncResolverRefusedInAgentSession`,
  `TestProjectSyncResolverNeverRunsAShell`.
- **Command:** `go test ./internal/cli/ -run 'ProjectSyncResolver' -count=1`
- **Depends on:** C1.
- **Red step:** compile failure: no `project_sync` field.
- **Commit:** `cli: project sync may take its repository list from a local resolver, inside the allow-list`
- **Reviewer:** opus + mandatory independent review (runs a configured command).

#### C3. Lockfiles before source in the derived image

- **Decisions:** D620.
- **Files:** `images/derived/Dockerfile.tmpl`, `internal/image/render.go`,
  `internal/image/render_test.go`, `images/cloudbuild_test.go`.
- **Behaviour:** the files the warm-up and `mise install` read (root `mise.toml`, the lockfiles
  `config.Rebuild` already names) are extracted at `FUGARO_COMMIT` into an early layer; the full
  source layer comes after them; `image.setup` stays after source (it may read anything).
- **Tests:** `TestRenderPutsLockfilesBeforeSource`, `TestRenderSetupStaysAfterSource`, the existing
  golden renders updated.
- **Command:** `go test ./internal/image/ ./images/ -run 'Render|Cloudbuild' -count=1` and, for the
  owner or CI, `go test -tags docker ./images/ -run 'Derived'`.
- **Depends on:** none.
- **Red step:** `TestRenderPutsLockfilesBeforeSource` fails on today's order.
- **Commit:** `image: lockfiles and tool files before the source layer`
- **Reviewer:** standard (it handles the build credential mount: the reviewer checks no secret mount
  is added).

#### C4. The image digest checked at launch

- **Decisions:** D619 (step 1).
- **Files:** `internal/cli/run.go`, `internal/imagecheck/record.go`,
  `internal/cli/run_image_digest_test.go` (new).
- **Behaviour:** the launcher reads the build record and the registry's `:latest` digest and warns
  when they differ (a build promoted without its record, or a record ahead of the tag). No refusal.
- **Tests:** `TestRunWarnsOnDigestMismatch`, `TestRunSilentWhenDigestsMatch`,
  `TestRunDigestReadFailureIsAWarningOnly`.
- **Command:** `go test ./internal/cli/ -run 'TestRun.*Digest' -count=1`
- **Depends on:** none.
- **Red step:** `TestRunWarnsOnDigestMismatch` fails: no warning.
- **Commit:** `cli: warn at launch when the image's digest is not its build record's`
- **OWNER-RUN:** Check 233 (whether a job execution can override the image; decides D619 step 2).
- **Reviewer:** standard.

#### C5a. Context repositories in the task (GATED: D613)

- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/cli/run.go`
  (`--context owner/name`, repeatable), `internal/cli/recipes_skew.go`,
  `internal/cli/run_context_test.go` (new).
- **Behaviour:** each context repository must be admitted by `allow_repos`; at most a small fixed
  number (the task states it); refused for Bitbucket until the proxy admits them (secrets-brokering
  D496 binds fetch to the task's repository).
- **Tests:** `TestRunContextReposMustBeAdmitted`, `TestRunContextReposCapped`,
  `TestRunContextRefusedOnBitbucket`, `TestSpecWithoutContextIsByteIdentical`.
- **Command:** `go test ./internal/cli/ ./internal/task/ -run 'Context' -count=1`
- **Depends on:** A2a (same files, earlier level).
- **Red step:** compile failure: no `Spec.ContextRepos`.
- **Commit:** `task, cli: read-only context repositories on a launch`
- **Reviewer:** opus + mandatory independent review.

#### C5b. A read-only token for context repositories (GATED: D613)

- **Files:** `internal/gitprov/github/apptoken.go`, `internal/credsvc/github.go`, tests beside each.
- **Behaviour:** a second mint naming the context repositories with **Contents: read** and
  **Metadata: read** only; the service admits each against the policy as it does the task's
  repository. The write token is unchanged.
- **Tests:** `TestContextTokenIsReadOnly`, `TestContextTokenNamesOnlyAdmittedRepos`,
  `TestServiceRefusesContextRepoOutsidePolicy`, `TestWriteTokenStillOneRepository`.
- **Command:** `go test ./internal/gitprov/github/ ./internal/credsvc/ -run 'Context|WriteTokenStillOne' -count=1`
- **Depends on:** none.
- **Red step:** `TestContextTokenIsReadOnly` fails: no such mint.
- **Commit:** `github, credsvc: a read-only installation token for a run's context repositories`
- **OWNER-RUN:** Check 232 (the sandbox App mints a two-repository read-only token; a push with it is
  refused).
- **Reviewer:** opus + mandatory independent review.

#### C5c. The runner clones context repositories read-only (GATED: D613)

- **Files:** `internal/runner/context_repos.go` (new), `internal/runner/context_repos_test.go`
  (new), `internal/runner/runner.go` (bootstrap call; the prompt names the paths).
- **Tests:** `TestContextReposClonedBlobless`, `TestContextReposHaveNoPushURL`,
  `TestContextReposNotInDigestOrPR`, `TestNoContextReposNoClone`.
- **Command:** `go test ./internal/runner/ -run 'ContextRepos' -count=1 -timeout 20m`
- **Depends on:** C5a, C5b, A3 (same file, earlier level).
- **Red step:** `TestContextReposClonedBlobless` fails: nothing is cloned.
- **Commit:** `runner: clone a run's context repositories read-only`
- **Reviewer:** opus + mandatory independent review.

#### C6. SECURITY.md: baked source and registry readers

- **Files:** `SECURITY.md`, `internal/cli/docs_registry_readers_test.go` (new).
- **Behaviour:** states that a repository's image holds its source at the baked commit and that
  anyone with pull rights on its registry reads it; context repositories are not baked.
- **Command:** `go test ./internal/cli/ -run 'Docs' -count=1`
- **Depends on:** none. **Red step:** the new docs test fails before the paragraph.
- **Commit:** `SECURITY: who can read the source baked into an image`
- **Reviewer:** standard.

#### C7. Project images and a lock: a design (GATED: D617, D618)

Docs only, in this folder: a per-project image as an opt-in, where its lock lives (the runs bucket
beside `builds/`), how `fugaro image refresh` reads it, the build's multi-repository credential, and
the migration. Today the build record already pins one repository's commit
(`imagecheck.Record.SourceCommit`), so no lock is needed for per-repository images.

### Slice D: change sets (GATED: D613, D614, D616)

#### D1. A change-set ID, linked PRs

- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/cli/run.go`
  (`--change-set ID`), `internal/cli/recipes_skew.go`, `internal/runner/prflow.go`,
  `internal/gitprov/status.go` (a "Change set" line in the status section),
  `internal/runstore/changeset.go` (new: `changesets/<id>/<run-id>.json` with the PR URL),
  tests beside each.
- **Tests:** `TestChangeSetIDValidated`, `TestChangeSetMembersRecorded`,
  `TestStatusSectionListsSiblingPRs`, `TestSpecWithoutChangeSetIsByteIdentical`.
- **Command:** `go test ./internal/task/ ./internal/runstore/ ./internal/gitprov/ ./internal/cli/ -run 'ChangeSet' -count=1`
- **Depends on:** C5a (same files, earlier level).
- **Red step:** compile failure: no `Spec.ChangeSet`.
- **Commit:** `change sets: an ID on a run, and each PR lists its siblings`
- **Reviewer:** standard.

#### D2. The draft hold

- **Files:** `internal/runner/runner.go` (finalize: a change-set run never flips ready; it records
  its verdict), `internal/runstore/changeset.go`, `internal/runner/changeset_test.go` (new).
- **Tests:** `TestChangeSetRunStaysDraft`, `TestChangeSetRunRecordsVerdict`,
  `TestRunWithoutChangeSetFlipsAsToday`.
- **Command:** `go test ./internal/runner/ -run 'ChangeSet|TestRunWithoutChangeSet' -count=1 -timeout 20m`
- **Depends on:** D1, C5c (same file, earlier level).
- **Red step:** `TestChangeSetRunStaysDraft` fails: the PR is made ready.
- **Commit:** `runner: a change set's run leaves its PR draft and records its verdict`
- **Reviewer:** standard.

#### D3. Settle a change set (`ee/coordinator`)

- **Files:** under `ee/coordinator/` as the ee-reorg plan lays it out (the change-set settle step and
  its CLI subcommand), tests beside it.
- **Behaviour:** when every member recorded a `ship` verdict and passing tests, flip every PR ready
  through `UpdatePR` and `ApplyReady`; otherwise report. Run by the coordinator job or by hand.
- **Depends on:** D2; **ee-reorg's coordinator move** (external).
- **OWNER-RUN:** Check 235 (a two-repository change set on the sandbox flips together).
- **Reviewer:** opus + mandatory independent review (writes PR state across repositories with the
  App key).

#### D4. Advisory merge order (`ee/coordinator`)

- **Behaviour:** the change set records an order; each PR body states it; the coordinator view shows
  it. No blocking (D615).
- **Depends on:** D3. **Reviewer:** standard.

### Slice E: people and tokens

#### E0. The roles design (D446)

A separate spec and reconciliation (its own `/spec-to-pr`), owned by the owner: roles, the sign-in
shape (D628), the launch path without per-person IAM (D627), suspension and seats (D629), the
service's write route for tokens (D631), the console's and mobile's parts (D635, D636), and the
licence split (D637). **E4 onward is written from it.**

#### E1. `fugaro user ls|add|rm` over the IAM lists

- **Decisions:** D627 (interim), D637.
- **Files:** `internal/cli/user.go` (new), `internal/cli/user_test.go` (new),
  `internal/cli/root.go`.
- **Behaviour:** reads and edits `terraform.launchers` and `terraform.operators` in the local config
  (`--role launcher|operator`), prints the invite text (project name, install line, `fugaro update
  --repo .` hint), and tells the operator that the change takes effect on their next `fugaro update`
  (OWNER-RUN). No email. `ls` reads the lists only (no "last active" until E0).
- **Tests:** `TestUserAddWritesLauncher`, `TestUserAddRequiresRole`, `TestUserRmRemoves`,
  `TestUserLsListsBoth`, `TestUserRefusedInAgentSession`, `TestUserInviteHasNoSecret`.
- **Command:** `go test ./internal/cli/ -run 'TestUser' -count=1`
- **Depends on:** none.
- **Red step:** compile failure: no `newUserCmd`.
- **Commit:** `cli: fugaro user, a front for the installation's launchers and operators`
- **Reviewer:** opus + mandatory independent review (IAM lists).

#### E2. `fugaro token add NAME` over `secrets set`

- **Decisions:** D631, D632, D633.
- **Files:** `internal/cli/token.go` (new), `internal/cli/token_test.go` (new),
  `internal/cli/root.go`.
- **Behaviour:** the same as `fugaro secrets set claude-oauth-token-NAME --project-wide` (stdin or a
  hidden prompt only) plus the `claude_accounts.NAME` entry with `--allow` repositories; prints how
  to run `claude setup-token` and pipe it. It never runs `claude setup-token`.
- **Tests:** `TestTokenAddReadsStdinOnly`, `TestTokenAddRefusesArgValue`,
  `TestTokenAddWritesAccountEntry`, `TestTokenAddNeverRunsSetupToken`.
- **Command:** `go test ./internal/cli/ -run 'TestTokenAdd' -count=1`
- **Depends on:** E1 (same file, earlier level).
- **Red step:** compile failure: no `newTokenCmd`.
- **Commit:** `cli: fugaro token add, a named Claude account from stdin`
- **Reviewer:** opus + mandatory independent review (credential handling).

#### E3. `fugaro join`

- **Files:** `internal/cli/join.go` (new), `internal/cli/join_test.go` (new), `internal/cli/root.go`.
- **Behaviour:** in a checkout, reads `gcp_project:` from `fugaro.yaml` (or `--gcp-project`), fetches
  the shared config through the existing fetch hook, checks the caller's access with `fugaro
  doctor`'s bucket check, and runs `fugaro update-skills`. Issues no licence (D630).
- **Tests:** `TestJoinReadsMarker`, `TestJoinFetchesSharedConfig`, `TestJoinWithoutMarkerAsksForFlag`,
  `TestJoinIssuesNoLicence`.
- **Command:** `go test ./internal/cli/ -run 'TestJoin' -count=1`
- **Depends on:** E2 (same file, earlier level).
- **Red step:** compile failure: no `newJoinCmd`.
- **Commit:** `cli: fugaro join, one command for a teammate's first checkout`
- **Reviewer:** standard.

#### E4 onward (GATED: E0)

Google sign-in exchanged for a custom token with a role claim; role checks in the database rules;
the service's authenticated token write route; `fugaro user role|suspend|resume`; the console's
read-only Members list; mobile suspend. Each is security-sensitive and written from E0.

## Decisions to tasks

| Decision | Tasks |
| --- | --- |
| D600 | slice order: A first |
| D601, D602, D603 | A1, A3 |
| D604, D605 | A2a, A2b, A3, A4 |
| D606 | A2a, A2b, A3 |
| D607, D608, D609 | none (no signing, no API commits); revisit with a customer need |
| D610, D611 | B1, B3 |
| D612 | A4's `docs/git-providers.md` section names it |
| D613 | C5a–C5c, D1–D4 |
| D614 | D2, D3, B2 |
| D615 | D4, B2 |
| D616, D637 | D3, D4 (`ee/`); all others MIT |
| D617, D618 | C7 |
| D619 | C4, Check 233 |
| D620 | C3 |
| D621 | C7 (no nightly job is planned) |
| D622, D623 | C1, C2 |
| D624 | E3 (the marker is `gcp_project:`) |
| D625 | C1 (confirm as today) |
| D626 | none here: project-centric phase 6 owns the backing folder |
| D627–D636, D638 | E0–E3, E4 onward |
| D639 | reconciliation §6 |

## Parallel execution

**Edges** (from the Depends-on lines): A1→A3; A2a→A2b→A3→A4→A5; B1→B2; C1→C2; A2a→C5a;
C5a→C5c; C5b→C5c; A3→C5c; C5a→D1; D1→D2; C5c→D2; D2→D3→D4; E1→E2→E3.

The file-sharing edges (A2a→C5a, A3→C5c, C5a→D1, C5c→D2, E1→E2→E3) exist only to keep one writer
per file per level when slices run together. **A slice approved alone runs its own chain only.**

**Levels:**

| Level | Tasks (concurrent) |
| --- | --- |
| **L0** | A1, A2a, B1, C1, C3, C4, C5b, C6, E1; docs B3, C7, E0 |
| **L1** | A2b, B2, E2 |
| **L2** | A3, C2, C5a, E3 |
| **L3** | A4, C5c, D1 |
| **L4** | A5 (OWNER-RUN), D2 |
| **L5** | D3 (also waits on ee-reorg's coordinator move) |
| **L6** | D4 |

C2 sits at L2, not L1, because it and A2b both write `internal/localcfg/localcfg.go`; C5a sits at
L2 because it and A2b both write `internal/cli/run.go` and `internal/cli/recipes_skew.go`.

**Critical path:** A2a → A2b → A3 → C5c → D2 → D3 → D4 (seven levels), with D3 also gated on the
ee-reorg coordinator move. **Slice A's own path** is A2a → A2b → A3 → A4 → A5 (five levels; A1 runs
beside A2a).

**Shared files (no level holds two writers of one file):**

| File | Writers (level) |
| --- | --- |
| `internal/task/task.go`, `schemas/task.schema.json` | A2a (L0), C5a (L2), D1 (L3) |
| `internal/cli/run.go` | C4 (L0), A2b (L1), C5a (L2), D1 (L3) |
| `internal/cli/recipes_skew.go` | A2b (L1), C5a (L2), D1 (L3) |
| `internal/runner/runner.go` | A3 (L2), C5c (L3), D2 (L4) |
| `internal/runner/checkpoint.go` | A3 (L2) |
| `internal/runner/prflow.go` | D1 (L3) |
| `internal/gitprov/gitprov.go` | B1 (L0), B2 (L1) |
| `internal/gitprov/status.go` | D1 (L3) |
| `internal/runstore/changeset.go` | D1 (L3, new), D2 (L4) |
| `internal/cli/project_enroll.go` | C1 (L0), C2 (L2) |
| `internal/localcfg/localcfg.go` | A2b (L1), C2 (L2) |
| `internal/cli/root.go` | E1 (L0), E2 (L1), E3 (L2) |
| `SECURITY.md` | C6 (L0), A4 (L3) |
| `docs/git-providers.md` | B2 (L1), A4 (L3) |
| `internal/config/config.go`, `internal/config/validate.go`, `schemas/fugaro.schema.json` | A2a (L0) |
| `internal/gitprov/github/apptoken.go`, `internal/credsvc/github.go` | C5b (L0) |
| `internal/imagecheck/record.go` | C4 (L0) |
| `images/derived/Dockerfile.tmpl`, `internal/image/render.go` | C3 (L0) |
| `internal/gitprov/providers/providers.go` | B1 (L0) |
| `internal/gitprov/bitbucket/` (new files) | C1 `list.go` (L0), B2 `statuses.go` (L1) |

**What must be sequential and why:** A3 needs A1's `StampRange` and A2's fields (interfaces); C5c
needs C5a's field and C5b's mint (interfaces); D2 needs D1's change-set record; D3 needs the
coordinator under `ee/` (an external plan); every other edge is one file.

## Check numbers

| Check | Measures | Written by | Run by |
| --- | --- | --- | --- |
| 230 | Author, committer and trailers on GitHub and Bitbucket; the default unchanged | A4 | Owner (A5) |
| 231 | The App's Checks and Commit statuses read permissions, accepted and working | B2 | Owner |
| 232 | A two-repository read-only installation token; a push with it is refused | C5b | Owner |
| 233 | Whether a Cloud Run job execution can override its image (D619 step 2) | C4 | Owner |
| 234 | `fugaro project sync --check` against a Bitbucket workspace | C1 | Owner |
| 235 | A two-repository change set flips ready together | D3 | Owner |

Checks 236–249 are reserved and unused. Every check runs on a sandbox repository and installation,
never by an agent.

## Release actions

- B2: `### For operators`: accept the GitHub App's new read permissions on each installation.
- E1: none in its own PR; each `fugaro user` change is applied by the operator's next `fugaro update`
  (the command says so).
- No other task needs a post-deploy step. Slice A is opt-in per repository.
