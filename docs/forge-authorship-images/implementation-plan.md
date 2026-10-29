# Forge, authorship and images: implementation plan

## Status

**Plan only. Nothing is implemented. No task is approved: each task needs the owner's own explicit
go, and answering a decision is not approving a task.** It builds [spec.md](spec.md) **as amended by
[reconciliation.md](reconciliation.md)**, which wins where they differ.

**No owner answer shapes this plan yet.** Decisions D600–D645 are written with recommendations and
**empty answer cells** (the owner could not be asked while this was written). The plan follows the
**recommendations**; every task names the decisions it rests on, and a task whose decision is
answered otherwise is re-planned before it starts. Tasks marked **GATED** are not planned in detail
because their decision may remove them.

Slices, each independent and orderable (D600 recommends A first, alone):

| Slice | What it ships | Licence (D637, proposed) | Gated on |
| --- | --- | --- | --- |
| **A** Authorship and trailers | Commits authored by the launcher when a repository opts in; `Fugaro-Run`, `Fugaro-Rigor`, `Fugaro-Model` trailers on the commits the runner pushes | MIT | D600–D606, D640, D644, D645 |
| **B** Forge cleanup, CI status, GitLab | One list of forge kinds; a CI-status read; GitLab only after its own reconciliation | MIT | D610, D611; D614/D615 for B2 |
| **C** Repositories and images | Bitbucket listing in `fugaro project sync`; lockfile-first image layers; a digest check at launch; read-only context repositories; a SECURITY.md registry note | MIT | D613, D617–D625 |
| **D** Change sets | Linked runs created together, a draft hold, a settle step, advisory merge order | Linking and hold MIT; settle and order `ee/` | D613–D616, D641, D642; ee-reorg's coordinator move |
| **E** People and tokens | The roles design (D446) first; `fugaro user` over the existing IAM lists; `fugaro join` | MIT for E1, E3; the rest per E0 | D627–D638 |

**Withdrawn in review round 1:** C2 (a local resolver in `fugaro project sync`) has no consumer until
a lock exists, since its output is intersected with `allow_repos` and sync offers only what
`allow_repos` does not admit; it moves into C7's design. E2 (`fugaro token add` over `secrets set`)
would create a project account any launcher of an admitted repository can select, which is the pool
and not D633's "Own", on a machine where a teammate has no rights to write it (F34); it moves into
E0. Check 233 is withdrawn (U4 resolved).

## Conventions every part follows

- `CLAUDE.md` (targeted tests; docs are tested: `go test ./internal/cli/ -run Docs`; comments explain
  why; no secret-shaped literals; hermetic tests with `internal/gcpfake`, `internal/testutil`,
  `t.TempDir()`; release-note `### For operators` entries for post-deploy steps, in the next
  release's notes file under `docs/releases/` per `docs/releases/README.md`).
- `.claude/rules/credentials-service.md` for C5b and for any task touching `SECURITY.md`: the website
  copy `website/content/security.md` is regenerated in the same PR (`go run ./internal/clidoc`) and
  `npm run check` runs in `website/`. `.claude/rules/terraform.md` for any Terraform change.
- **Strict decoding and skew.** `task.Spec` and `fugaro.yaml` decode with unknown fields refused. A
  new task field follows the `Rigor` precedent: the launcher gates a launch that would set it on an
  older runner (`rigorRequestSince`, `internal/cli/recipes_skew.go`), and a spec without it stays
  byte-identical. Schemas in `schemas/` change in the same task.
- **New local-config fields are blanked in `localcfg.Config.Shared()`** (F43) with a test, unless a
  task says why one may be published.
- **Plugin skills.** A command named in `plugin/skills` is checked against the CLI; a task that adds
  a command updates `plugin/skills/working/SKILL.md` (or the skill that fits) in the same PR.
- **The CLI command list** (`internal/cli/root.go`) is being reshaped by open PR #1252 (a command
  registry); E1 and E3 follow whatever is on `main` when they start.
- **The run's identity** is whatever `main` has when A1 starts (open PR #1264 may make it the GitHub
  App's `[bot]` identity on GitHub, D645). No task hard-codes it.
- **No agent performs a live step**: no apply, no App permission change, no sandbox run. Those are
  OWNER-RUN, on a sandbox repository and installation only.

## Global constraints and review focus

1. **Stamping precedes verification** (F2). A rewritten commit changes its ID; `Decide` compares
   test records with the final SHA. Pinned by A3's `TestRunnerStampsBeforeVerify`.
2. **No rewrite under a running agent** (F2, D640): the stamp runs only when the agent's stage has
   returned, and never rewrites a pushed commit. Pinned by A3's
   `TestRunnerNeverRestampsCheckpointedCommits`.
3. **The default does not change** (D606, D645). With no `git.author` key, author and committer are
   the run's identity as on `main`; only the trailers are new. Pinned by A3's
   `TestRunnerDefaultAuthorUnchanged`.
4. **No new long-lived secret in a job** (R3). Pinned by C5b's and C5c's reviews.
5. **Fugaro never writes `Signed-off-by`**; one the agent writes is removed (R1, D644). Pinned by
   A1's `TestStampRemovesSignedOffBy`.
6. **What the runner pushes carries the runner's trailers** (F1): agent-written `Fugaro-*` trailers
   are replaced, and finalize re-stamps any commit whose ID is not on the runner's own stamped list.
   This binds the runner's pushes only; the agent can push other branches with its token, so the
   trailers are a record, not proof. Pinned by A1's `TestStampReplacesAgentFugaroTrailers` and A3's
   `TestRunnerFinalizeRestampsCommitsItDidNotStamp`.
7. **Never re-author someone else's commit** (F1): the stamp touches only `Repo.OwnCommits`. Pinned
   by A1's `TestStampLeavesForeignCommitsAlone`.
8. **The agent's `.git/config` cannot steer the stamp** (F1): trailer, signing and hook settings are
   overridden. Pinned by A1's `TestStampIgnoresAgentGitConfig`.
9. **Context repositories are read-only for the runner** (D613): no write token is minted for them,
   no push targets them, and each is admitted by the policy and listed in the run's write-once task.
   While a GitHub job still mounts the App key (`config.ServiceHeldSecretNames` empty, #907), the
   **agent** can mint its own token for any repository the App is installed on (SECURITY.md); C5
   adds no reach the agent lacks today. Pinned by C5b's and C5c's tests.
10. **The author is a claim** (F3, D604): docs and SECURITY.md say so until Slice E's identity exists.
    Pinned by A4's docs test.
11. **A change set's state is never trusted from a PR** (F15): the settle step re-reads every member's
    record and PR. Pinned by D3's tests.

## Tasks

### Slice A: authorship and trailers

#### A1. A commit stamp in `gitops`

- **Decisions:** D601, D602, D603, D644, D645; Constraints 5–8.
- **Files:** `internal/gitops/stamp.go` (new), `internal/gitops/stamp_test.go` (new).
- **Interfaces produced:** `type Stamp struct { Author *Person; Trailers []Trailer }` (nil author
  keeps the run's identity); `func (r *Repo) StampOwn(ctx, runBranch string, s Stamp) (head string,
  stamped []string, err error)`: rewrites the commits `Repo.OwnCommits(runBranch)` returns that are
  not yet on the remote run branch, mapping parents (merge commits included) and keeping each tree;
  sets the author when given and the committer to the run's identity; composes the message in Go
  (removes `Signed-off-by` and every `Fugaro-*` trailer, appends `s.Trailers` once); runs git with
  `commit.gpgsign=false`, no hooks and no `trailer.*` from the repository's config; returns the new
  IDs. Idempotent.
- **Schema/rules/config:** none.
- **Tests:** `TestStampRewritesAuthorAndTrailers`, `TestStampKeepsTrees`,
  `TestStampReplacesAgentFugaroTrailers`, `TestStampRemovesSignedOffBy`,
  `TestStampLeavesForeignCommitsAlone`, `TestStampLeavesPushedCommitsAlone`,
  `TestStampMapsMergeParents`, `TestStampIsIdempotent`, `TestStampIgnoresAgentGitConfig`,
  `TestStampNilAuthorKeepsRunIdentity`.
- **Command:** `go test ./internal/gitops/ -run 'TestStamp' -count=1`
- **Depends on:** none.
- **Red step:** the tests reference `StampOwn`, which does not exist: compile failure.
- **Commit:** `gitops: a commit stamp that sets the author and Fugaro trailers on a run's own commits`
- **Reviewer:** standard, plus an independent review (agent-controlled input reaches git).

#### A2a. The author in the task, and the repository's opt-in

- **Decisions:** D604, D606.
- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/config/config.go`
  (`Git.Author`: `fugaro` or `launcher`), `internal/config/validate.go`,
  `schemas/fugaro.schema.json`, `internal/task/task_test.go`, `internal/config/validate_test.go`,
  `schemas/schemas_test.go`.
- **Interfaces produced:** `task.Spec.Author *task.Person` (`name`, `email`; omitted when empty;
  `Validate` refuses control characters, newlines, `<` and `>` in either and an email without one
  `@`); `config.Git.Author string`.
- **Schema change:** both schemas gain the field.
- **Tests:** `TestSpecAuthorRoundTrips`, `TestSpecWithoutAuthorIsByteIdentical`,
  `TestSpecAuthorRefusesControlCharacters`, `TestValidateGitAuthorClosedVocabulary`,
  `TestSchemaCoversGitAuthor` (new).
- **Command:** `go test ./internal/task/ ./internal/config/ ./schemas/ -run 'Author' -count=1`
- **Depends on:** none.
- **Red step:** the new tests reference `Spec.Author` and `Git.Author`: compile failure.
- **Commit:** `task, config: the launcher as a task field, and git.author as a repository opt-in`
- **Reviewer:** standard.

#### A2b. The launcher fills the author

- **Decisions:** D604, D605, D606.
- **Files:** `internal/localcfg/localcfg.go` (optional `commit_name`, `commit_email`),
  `internal/localcfg/shared.go` (both blanked), `internal/localcfg/shared_test.go`,
  `internal/cli/run.go`, `internal/cli/recipes_skew.go` (an `authorSince` gate),
  `internal/cli/run_author_test.go` (new).
- **Interfaces consumed:** `localcfg.Config.Me`, `task.Spec.Author`.
- **Behaviour:** the author is `commit_email` (else `Me`) with `commit_name` (else `git config
  user.name`, else the email's local part); omitted when `Me` is empty, for review and artifact
  launches (D605), and against an older runner (the skew gate). The run record keeps `launched_by`
  beside it, so a commit address that differs from the launcher stays traceable through
  `Fugaro-Run`.
- **Tests:** `TestRunAuthorFromLocalConfig`, `TestRunAuthorCommitEmailOverridesMe`,
  `TestRunAuthorOmittedWithoutIdentity`, `TestRunAuthorOmittedForOlderRunner`,
  `TestRunAuthorOmittedForReviewRun`, `TestSharedBlanksCommitIdentity`.
- **Command:** `go test ./internal/cli/ ./internal/localcfg/ -run 'TestRunAuthor|TestSharedBlanksCommitIdentity' -count=1`
- **Depends on:** A2a.
- **Red step:** `TestRunAuthorFromLocalConfig` fails: the written task has no author.
- **Commit:** `cli: fugaro run records the launcher as the commit author`
- **Reviewer:** standard.

#### A3. The runner stamps the commits it pushes

- **Decisions:** D601–D605, D640; Constraints 1–3, 6.
- **Files:** `internal/runner/runner.go` (the agent stage's return, before the gate's verification;
  the finalize re-stamp), `internal/runner/prflow.go` (the boundary order in `afterStage`),
  `internal/runner/checkpoint.go` (records what a mid-stage push sent, so the stamp skips it),
  `internal/runner/stamp_test.go` (new).
- **Interfaces consumed:** `gitops.Repo.StampOwn`, `task.Spec.Author`, `config.Git.Author`,
  `runstore.Record.RunID`, `Record.Rigor`, the stage's configured model (`StageTiming.Model`).
- **Behaviour:** after each agent stage returns, the runner stamps the stage's unpushed own commits
  (author only when `git.author: launcher` and `Spec.Author` is set; trailers always) and adds the
  new IDs to its stamped list; mid-stage checkpoint pushes are unchanged (D640); finalize re-stamps
  any unpushed commit not on the list before its push. Review and artifact runs never stamp.
- **Tests:** `TestRunnerStampsBeforeVerify`, `TestRunnerStampedCommitsReachTheBoundaryPush`,
  `TestRunnerNeverRestampsCheckpointedCommits`, `TestRunnerFinalizeRestampsCommitsItDidNotStamp`,
  `TestRunnerDefaultAuthorUnchanged`, `TestRunnerAuthorOptIn`, `TestRunnerModelTrailerIsTheStagePin`,
  `TestRunnerReviewAndArtifactNeverStamp`.
- **Command:** `go test ./internal/runner/ -run 'TestRunner(Stamp|NeverRestamps|FinalizeRestamps|DefaultAuthor|AuthorOptIn|ModelTrailer|ReviewAndArtifact)' -count=1 -timeout 20m`
- **Depends on:** A1, A2a, A2b.
- **Red step:** `TestRunnerStampsBeforeVerify` fails: the pushed commit has no `Fugaro-Run` trailer.
- **Commit:** `runner: stamp each stage's commits with the author and Fugaro trailers before verification`
- **Reviewer:** standard, plus an independent review (it touches the push and readiness path).

#### A4. Docs

- **Decisions:** D604, D612, D640, D643.
- **Files:** `docs/git-providers.md` (a "Commit author and trailers" section and a "Supported forges"
  line: cloud forges supported, an owner-allow-listed Bitbucket root accepted and unsupported),
  `SECURITY.md` ("Known limits": the author is the launcher's claim; trailers are a record, not
  proof; a launcher may approve a PR their name is on), `website/content/security.md` (regenerated),
  the next release's notes file under `docs/releases/` (one Highlights line; no operator step),
  `internal/cli/docs_commit_author_test.go` (new).
- **Tests:** `TestDocsCommitAuthorIsAClaim`, `TestDocsTrailerNames`, `TestDocsSupportedForges`.
- **Command:** `go test ./internal/cli/ -run 'Docs' -count=1`; `go run ./internal/clidoc`; in
  `website/`, `npm run check`.
- **Depends on:** A3.
- **Red step:** the new docs test fails before the sections exist.
- **Commit:** `docs: commit author and Fugaro trailers, and what they do not prove`
- **Reviewer:** standard.

#### A5. Check 230 (OWNER-RUN)

- **Measures:** on a sandbox GitHub repository and a sandbox Bitbucket repository, with
  `git.author: launcher` committed: the run's boundary commits show the launcher as author and the
  run's identity as committer, with the three trailers; blame and the PR's commit list show the
  launcher; no `Signed-off-by`; and with the key absent, the author is the run's identity.
- **The owner types,** in each sandbox checkout:
  `fugaro run --title "Check 230" "Add a line to CHECK230.md"`, then on the PR branch
  `git log --format='%an <%ae> | %cn <%ce>%n%(trailers)' origin/main..`, and records the result under
  Check 230 in `docs/gcp-live-checklist.md`.
- **Written by:** X1. **Run by:** the owner. **Depends on:** a release with A3.

### Slice B: forge

#### B1. One list of forge kinds

- **Decisions:** D610, D611.
- **Files:** `internal/gitprov/kinds.go` (new), `internal/gitprov/kinds_test.go` (new),
  `internal/gitprov/gitprov.go`, `internal/gitprov/providers/providers.go`.
- **Behaviour:** `gitprov.Kinds` lists the forge kinds; `providers.FromEnv` and its error text read
  it. The test walks the tree (excluding `testdata/`, `docs/`, `website/`, `node_modules/`) for the
  regular expression `KindGitHub|KindBitbucket|"github"|"bitbucket"` in `.go`, `.tf`, `.yaml` and
  `.json` files and fails on any file not in its inventory table, which lists each with the switch it
  holds. A future GitLab task reads the table as its to-do list. No behaviour change.
- **Tests:** `TestKindsListedOnce`, `TestEveryKindSwitchIsInventoried`; the existing
  `internal/gitprov/providers` tests unchanged.
- **Command:** `go test ./internal/gitprov/... -run 'Kind|FromEnv' -count=1`
- **Depends on:** none.
- **Red step:** compile failure: `gitprov.Kinds` does not exist.
- **Commit:** `gitprov: one list of forge kinds, and an inventory of every switch on them`
- **Reviewer:** standard.

#### B2. A CI-status read (GATED: D614 or D615 must need it)

- **Files:** `internal/gitprov/gitprov.go` (`Checks(ctx, sha) ([]CheckState, error)`),
  `internal/gitprov/github/checks.go` (new), `internal/gitprov/bitbucket/statuses.go` (new),
  `internal/gitprov/fake/` (the fake's method), tests beside each, recorded fixtures in each adapter's
  `testdata/`, `docs/git-providers.md` (the App gains **Checks: read** and **Commit statuses:
  read**), the next release's notes file under `docs/releases/` (`### For operators`: accept the
  App's new permissions on every installation).
- **Tests:** `TestGitHubChecksCombinesRunsAndStatuses`, `TestBitbucketStatusesMapToCheckState`,
  `TestFakeChecks`.
- **Command:** `go test ./internal/gitprov/... -run 'Checks|Statuses' -count=1`
- **Depends on:** B1.
- **Red step:** compile failure: `Checks` is not on `Provider`.
- **Commit:** `gitprov: read CI status for a commit`
- **OWNER-RUN:** Check 231: the owner adds the two read permissions in the App's settings, accepts
  them on the sandbox installation, and runs `go test -tags live ./internal/gitprov/github/ -run
  Checks -count=1` with the sandbox variables the live tests already read.
- **Reviewer:** opus + mandatory independent review (forge permissions).

#### B3. GitLab (GATED: D610 answered "build")

A docs-only reconciliation in this folder first: tokens (project or group access tokens and what they
cost), `Draft:` semantics, the image build's credential step, the credentials service's mint, the
secrets and Terraform, using B1's inventory table as the list of places. The adapter tasks are written
from it. **Not sized here** beyond: L, security-sensitive throughout.

### Slice C: repositories and images

#### C1. `fugaro project sync` for Bitbucket, with `--check`

- **Decisions:** D623, D625.
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

#### C3. Lockfiles before source in the derived image

- **Decisions:** D620.
- **Files:** `images/derived/Dockerfile.tmpl`, `internal/image/render.go`,
  `internal/image/render_test.go`, `images/cloudbuild_test.go`.
- **Behaviour:** the files the warm-up and `mise install` read (the root `mise.toml` and the key
  files `internal/imagecheck` already tracks for rebuilds, `Record.KeyFiles`) are extracted at
  `FUGARO_COMMIT` into an early layer; the full source layer comes after them; `image.setup` stays
  after source (it may read anything). No secret mount is added.
- **Tests:** `TestRenderPutsKeyFilesBeforeSource`, `TestRenderSetupStaysAfterSource`, the existing
  golden renders updated.
- **Command:** `go test ./internal/image/ ./images/ -run 'Render|Cloudbuild' -count=1`; for the owner
  or CI, `go test -tags docker ./images/ -run 'Derived'`.
- **Depends on:** none.
- **Red step:** `TestRenderPutsKeyFilesBeforeSource` fails on today's order.
- **Commit:** `image: key files before the source layer`
- **Reviewer:** standard (the reviewer checks no secret mount is added).

#### C4. The image digest checked at launch

- **Decisions:** D619.
- **Files:** `internal/cli/run.go`, `internal/imagecheck/record.go`,
  `internal/cli/run_image_digest_test.go` (new).
- **Behaviour:** the launcher reads the build record and the registry's `:latest` digest and warns
  when they differ. No refusal; a read failure is a warning only.
- **Tests:** `TestRunDigestWarnsOnMismatch`, `TestRunDigestSilentWhenMatching`,
  `TestRunDigestReadFailureIsAWarningOnly`.
- **Command:** `go test ./internal/cli/ -run 'TestRunDigest' -count=1`
- **Depends on:** none.
- **Red step:** `TestRunDigestWarnsOnMismatch` fails: no warning.
- **Commit:** `cli: warn at launch when the image's digest is not its build record's`
- **Reviewer:** standard.

#### C5a. Context repositories in the task (GATED: D613)

- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/cli/run.go`
  (`--context owner/name`, repeatable), `internal/cli/recipes_skew.go`,
  `internal/cli/run_context_test.go` (new).
- **Behaviour:** each context repository must be admitted by `allow_repos`; at most **5** per run;
  refused for Bitbucket until the proxy admits them (secrets-brokering D496 binds fetch to the task's
  repository). The list is part of the write-once task.
- **Tests:** `TestRunContextReposMustBeAdmitted`, `TestRunContextReposCappedAtFive`,
  `TestRunContextRefusedOnBitbucket`, `TestSpecWithoutContextIsByteIdentical`.
- **Command:** `go test ./internal/cli/ ./internal/task/ -run 'Context' -count=1`
- **Depends on:** A2a, A2b (same files, earlier levels).
- **Red step:** compile failure: no `Spec.ContextRepos`.
- **Commit:** `task, cli: read-only context repositories on a launch`
- **Reviewer:** opus + mandatory independent review.

#### C5b. A read-only token for context repositories (GATED: D613)

- **Files:** `internal/gitprov/github/apptoken.go`, `internal/credsvc/github.go`, tests beside each.
- **Behaviour:** a second mint with **Contents: read** and **Metadata: read** only, naming exactly
  the repositories the run's **write-once task** lists (C5a), each re-admitted against the policy; a
  request naming any other repository is refused, so an agent calling the service with the job's
  identity gains nothing beyond its task. The write token is unchanged.
- **Tests:** `TestContextTokenIsReadOnly`, `TestContextTokenNamesOnlyTheTasksRepos`,
  `TestServiceRefusesContextRepoNotInTask`, `TestServiceRefusesContextRepoOutsidePolicy`,
  `TestWriteTokenStillOneRepository`.
- **Command:** `go test ./internal/gitprov/github/ ./internal/credsvc/ -run 'Context|WriteTokenStillOne' -count=1`
- **Depends on:** C5a.
- **Red step:** `TestContextTokenIsReadOnly` fails: no such mint.
- **Commit:** `github, credsvc: a read-only installation token for a run's context repositories`
- **OWNER-RUN:** Check 232 (the sandbox App mints a two-repository read-only token; a push with it is
  refused).
- **Reviewer:** opus + mandatory independent review.

#### C5c. The runner clones context repositories read-only (GATED: D613)

- **Files:** `internal/runner/context_repos.go` (new), `internal/runner/context_repos_test.go`
  (new), `internal/runner/runner.go` (bootstrap call; the prompt names the paths), the next release's
  notes file under `docs/releases/` (`### For operators`: install the GitHub App on every context
  repository; redeploy the credentials service on opted-in installations).
- **Tests:** `TestContextReposClonedBlobless`, `TestContextReposHaveNoPushURL`,
  `TestContextReposNotInDigestOrPR`, `TestNoContextReposNoClone`.
- **Command:** `go test ./internal/runner/ -run 'ContextRepos|TestNoContextReposNoClone' -count=1 -timeout 20m`
- **Depends on:** C5b, A3 (same file, earlier level).
- **Red step:** `TestContextReposClonedBlobless` fails: nothing is cloned.
- **Commit:** `runner: clone a run's context repositories read-only`
- **Reviewer:** opus + mandatory independent review.

#### C6. SECURITY.md: baked source and registry readers

- **Files:** `SECURITY.md`, `website/content/security.md` (regenerated),
  `internal/cli/docs_registry_readers_test.go` (new).
- **Behaviour:** states that a repository's image holds its source at the baked commit and that
  anyone with pull rights on its registry reads it; context repositories are not baked; image builds
  are Cloud Build cost outside the run budget (D621).
- **Tests:** `TestDocsRegistryReaders`, `TestDocsBuildsOutsideRunBudget`.
- **Command:** `go test ./internal/cli/ -run 'Docs' -count=1`; `go run ./internal/clidoc`; in
  `website/`, `npm run check`.
- **Depends on:** none. **Red step:** the new docs test fails before the paragraph.
- **Commit:** `SECURITY: who can read the source baked into an image`
- **Reviewer:** standard.

#### C7. Project images, a lock and a resolver: a design (GATED: D617, D618, D622)

Docs only, in this folder: a per-project image as an opt-in, where its lock lives (the runs bucket
beside `builds/`), how `fugaro image refresh` reads it, the build's multi-repository credential, the
migration, and the local resolver command (withdrawn C2) as the lock's input. Today the build record
already pins one repository's commit (`imagecheck.Record`'s source commit), so per-repository images
need no lock.

### Slice D: change sets (GATED: D613, D614, D616, D641, D642)

#### D1. Change sets created together, linked PRs

- **Files:** `internal/task/task.go` (`Spec.ChangeSet`: an ID and the member list),
  `schemas/task.schema.json`, `internal/cli/run.go` (`--change-set` launches one run per `--repo`,
  writing every member's task with the full member list), `internal/cli/recipes_skew.go`,
  `internal/runner/prflow.go`, `internal/gitprov/status.go` (a "Change set" line in the status
  section), `internal/runstore/changeset.go` (new: the member's PR URL and verdict, stored **inside**
  the run's own `runs/<slug>/<run-id>/` prefix, the bucket's IAM boundary), tests beside each.
- **Tests:** `TestChangeSetMembersFixedAtLaunch`, `TestChangeSetRecordInsideRunPrefix`,
  `TestChangeSetStatusSectionListsSiblingPRs`, `TestSpecWithoutChangeSetIsByteIdentical`.
- **Command:** `go test ./internal/task/ ./internal/runstore/ ./internal/gitprov/ ./internal/cli/ -run 'ChangeSet' -count=1`
- **Depends on:** C5a (same files, earlier level), A3 (`prflow.go`, earlier level).
- **Red step:** compile failure: no `Spec.ChangeSet`.
- **Commit:** `change sets: runs launched together, and each PR lists its siblings`
- **Reviewer:** standard.

#### D2. The draft hold

- **Files:** `internal/runner/runner.go` (finalize: a change-set run never flips ready; it records
  its verdict), `internal/runstore/changeset.go`, `internal/runner/changeset_test.go` (new).
- **Tests:** `TestChangeSetRunStaysDraft`, `TestChangeSetRunRecordsVerdict`,
  `TestChangeSetFollowUpKeepsTheHold`, `TestRunWithoutChangeSetFlipsAsToday`.
- **Command:** `go test ./internal/runner/ -run 'ChangeSet|TestRunWithoutChangeSetFlipsAsToday' -count=1 -timeout 20m`
- **Depends on:** D1; C5c (same file, earlier level).
- **Red step:** `TestChangeSetRunStaysDraft` fails: the PR is made ready.
- **Commit:** `runner: a change set's run leaves its PR draft and records its verdict`
- **Reviewer:** standard.

#### D3. Settle a change set (`ee/coordinator`)

- **Files:** `ee/coordinator/changeset.go` (new), `ee/coordinator/changeset_test.go` (new), the
  settle subcommand under the `ee/` CLI package the ee-reorg plan creates, and the settle-only token
  in `internal/gitprov/github/apptoken.go` (D641).
- **Behaviour:** reads every member's record from its own run prefix and every PR's live state;
  only when every listed member recorded `ship` and passing tests, and every member lists the same
  set, flips every PR ready through `UpdatePR` and `ApplyReady` with the settle-only token; otherwise
  reports. Run by the coordinator job or by hand.
- **Tests:** `TestSettleFlipsAllWhenEveryMemberShips`, `TestSettleRefusesAMemberNotListedByAll`,
  `TestSettleRereadsPRStateNotTheRecord`, `TestSettleTokenIsPullRequestsWriteOnly`.
- **Command:** `go test -tags ee ./ee/coordinator/... ./internal/gitprov/github/ -run 'Settle' -count=1`
- **Depends on:** D2; **ee-reorg's coordinator move** (external).
- **Red step:** compile failure: no settle step.
- **OWNER-RUN:** Check 235 (a two-repository change set on the sandbox flips together).
- **Reviewer:** opus + mandatory independent review (writes PR state across repositories).

#### D4. Advisory merge order (`ee/coordinator`)

- **Files:** `ee/coordinator/changeset.go`, `ee/coordinator/changeset_test.go`,
  `internal/gitprov/status.go` (the order line).
- **Behaviour:** the change set records an order at launch; each PR body states it; the coordinator
  view shows it; the mobile `merge` action's confirmation names it. No blocking (D615).
- **Tests:** `TestChangeSetOrderInEveryPR`, `TestCoordinatorViewShowsOrder`.
- **Command:** `go test -tags ee ./ee/coordinator/... ./internal/gitprov/ -run 'Order' -count=1`
- **Depends on:** D3. **Red step:** `TestChangeSetOrderInEveryPR` fails: no order line.
- **Commit:** `change sets: an advisory merge order, stated in every PR`
- **Reviewer:** standard.

### Slice E: people and tokens

#### E0. The roles design (D446)

A separate spec and reconciliation (its own `/spec-to-pr`), owned by the owner: roles, the sign-in
shape (D628), the launch path without per-person IAM (D627), the authenticated launcher identity that
replaces A's claim (D604, U1), suspension and seats (D629), contributed tokens with a per-person
binding and the service's write route (D631–D633, withdrawn E2), reserves (D634), the console's and
mobile's parts (D635, D636), workspace invites (D638) and the licence split (D637). **E4 onward is
written from it.**

#### E1. `fugaro user ls|add|rm` over the IAM lists

- **Decisions:** D627 (interim), D637; F42.
- **Files:** `internal/cli/user.go` (new), `internal/cli/user_test.go` (new),
  `internal/cli/root.go`, `plugin/skills/working/SKILL.md`.
- **Behaviour:** refreshes `terraform.launchers` and `terraform.operators` from the installation's
  own outputs (the copy-back `init_new_machine.go` already does) and **refuses** when the local list
  differs from them; then edits the list (`--role launcher|operator`, `user:` members only: `domain:`,
  `group:` and `serviceAccount:` are refused), prints an invite (project name, install line, "the
  repository's `fugaro.yaml` names the project; no setup is needed"), and tells the operator that the
  change takes effect on their next `fugaro update` (OWNER-RUN). No email. `ls` reads the lists only.
- **Tests:** `TestUserAddWritesLauncher`, `TestUserAddRequiresRole`, `TestUserAddRefusesDomain`,
  `TestUserRefusesStaleLocalList`, `TestUserRmRemoves`, `TestUserLsListsBoth`,
  `TestUserRefusedInAgentSession`, `TestUserInviteHasNoSecret`.
- **Command:** `go test ./internal/cli/ -run 'TestUser' -count=1`; `go test ./plugin/... -count=1`
- **Depends on:** none.
- **Red step:** compile failure: no `newUserCmd`.
- **Commit:** `cli: fugaro user, a front for the installation's launchers and operators`
- **Reviewer:** opus + mandatory independent review (IAM lists).

#### E3. `fugaro join`

- **Files:** `internal/cli/join.go` (new), `internal/cli/join_test.go` (new), `internal/cli/root.go`,
  `plugin/skills/working/SKILL.md`.
- **Behaviour:** in a checkout, reads `gcp_project:` from `fugaro.yaml` (or `--gcp-project`),
  fetches the shared config through the existing fetch hook, checks the caller's access with
  `fugaro doctor`'s bucket check, and runs `fugaro update-skills`. Issues no licence (D630).
- **Tests:** `TestJoinReadsMarker`, `TestJoinFetchesSharedConfig`, `TestJoinWithoutMarkerAsksForFlag`,
  `TestJoinIssuesNoLicence`.
- **Command:** `go test ./internal/cli/ -run 'TestJoin' -count=1`; `go test ./plugin/... -count=1`
- **Depends on:** E1 (same files, earlier level).
- **Red step:** compile failure: no `newJoinCmd`.
- **Commit:** `cli: fugaro join, one command for a teammate's first checkout`
- **Reviewer:** standard.

#### E4 onward (GATED: E0)

Google sign-in exchanged for a custom token with a role claim; role checks in the database rules;
the service's authenticated token write route and `fugaro token add`; `fugaro user role|suspend|
resume`; the console's read-only Members list; mobile suspend. Each is security-sensitive and written
from E0.

### X1. The live-check entries

- **Files:** `docs/gcp-live-checklist.md` (Checks 230, 231, 232, 234, 235, each NOT RUN, with its
  prerequisites, the exact commands and the task that makes it runnable),
  `internal/cli/docs_forge_checks_test.go` (new).
- **Tests:** `TestDocsForgeChecksWritten`, `TestDocsForgeChecksNotRun`.
- **Command:** `go test ./internal/cli/ -run 'Docs' -count=1`
- **Depends on:** none. **Red step:** the new test fails before the entries exist.
- **Commit:** `docs: live checks 230-235 for forge, authorship and change sets (not run)`
- **Reviewer:** standard. The only writer of the checklist in this plan.

## Decisions to tasks

| Decision | Tasks |
| --- | --- |
| D600 | slice order: A first |
| D601, D602, D603 | A1, A3 |
| D604, D605 | A2a, A2b, A3, A4; E0 replaces the claim |
| D606, D645 | A2a, A2b, A3 |
| D607, D608, D609 | none (no signing, no API commits); revisit with a customer need |
| D610, D611 | B1, B3 |
| D612 | A4 (`TestDocsSupportedForges`) |
| D613 | C5a–C5c, D1–D4 |
| D614 | D2, D3; B2 if needed |
| D615 | D4; B2 if blocking is chosen |
| D616, D637 | D3, D4 (`ee/`); all others MIT |
| D617, D618, D622 | C7 |
| D619 | C4 |
| D620 | C3 |
| D621 | C6 (`TestDocsBuildsOutsideRunBudget`); no nightly job is planned |
| D623, D625 | C1 |
| D624 | E3 (the marker is `gcp_project:`) |
| D626 | none here: project-centric phase 6 owns the backing folder |
| D627–D636, D638 | E0, E1, E3, E4 onward |
| D639 | reconciliation §6 |
| D640 | A3, A4 |
| D641, D642 | D1, D3 |
| D643 | A4 |
| D644 | A1 |

## Parallel execution

**Edges** (from the Depends-on lines): A1→A3; A2a→A2b→A3→A4→A5; B1→B2; A2a→C5a; A2b→C5a;
C5a→C5b→C5c; A3→C5c; C5a→D1; A3→D1; D1→D2; C5c→D2; D2→D3→D4; E1→E3.

The file-sharing edges (A2a→C5a, A2b→C5a, A3→C5c, A3→D1, C5a→D1, C5c→D2, E1→E3) exist only to keep
one writer per file per level when slices run together. **A slice approved alone runs its own chain
only.**

**Levels:**

| Level | Tasks (concurrent) |
| --- | --- |
| **L0** | A1, A2a, B1, C1, C3, C4, C6, E1, X1; docs B3, C7, E0 |
| **L1** | A2b, B2, E3 |
| **L2** | A3, C5a |
| **L3** | A4, C5b, D1 |
| **L4** | A5 (OWNER-RUN), C5c |
| **L5** | D2 |
| **L6** | D3 (also waits on ee-reorg's coordinator move) |
| **L7** | D4 |

**Critical path:** A2a → A2b → C5a → C5b → C5c → D2 → D3 → D4 (eight levels), with D3 also gated on
the ee-reorg coordinator move. **Slice A's own path** is A2a → A2b → A3 → A4 → A5 (five levels; A1
runs beside A2a).

**Shared files (no level holds two writers of one file):**

| File | Writers (level) |
| --- | --- |
| `internal/task/task.go`, `schemas/task.schema.json` | A2a (L0), C5a (L2), D1 (L3) |
| `internal/cli/run.go` | C4 (L0), A2b (L1), C5a (L2), D1 (L3) |
| `internal/cli/recipes_skew.go` | A2b (L1), C5a (L2), D1 (L3) |
| `internal/runner/runner.go` | A3 (L2), C5c (L4), D2 (L5) |
| `internal/runner/prflow.go` | A3 (L2), D1 (L3) |
| `internal/runner/checkpoint.go` | A3 (L2) |
| `internal/gitprov/gitprov.go` | B1 (L0), B2 (L1) |
| `internal/gitprov/status.go` | D1 (L3), D4 (L7) |
| `internal/gitprov/github/apptoken.go` | C5b (L3), D3 (L6) |
| `internal/credsvc/github.go` | C5b (L3) |
| `internal/runstore/changeset.go` | D1 (L3, new), D2 (L5) |
| `ee/coordinator/changeset.go` | D3 (L6, new), D4 (L7) |
| `internal/cli/project_enroll.go` | C1 (L0) |
| `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go` | A2b (L1) |
| `internal/cli/root.go`, `plugin/skills/working/SKILL.md` | E1 (L0), E3 (L1) |
| `SECURITY.md`, `website/content/security.md` | C6 (L0), A4 (L3) |
| `docs/git-providers.md` | B2 (L1), A4 (L3) |
| `docs/gcp-live-checklist.md` | X1 (L0) |
| the next release's notes file under `docs/releases/` | B2 (L1), A4 (L3), C5c (L4) |
| `internal/config/config.go`, `internal/config/validate.go`, `schemas/fugaro.schema.json`, `schemas/schemas_test.go` | A2a (L0) |
| `internal/imagecheck/record.go` | C4 (L0) |
| `images/derived/Dockerfile.tmpl`, `internal/image/render.go` | C3 (L0) |
| `internal/gitprov/providers/providers.go` | B1 (L0) |
| `internal/gitprov/bitbucket/` (new files) | C1 `list.go` (L0), B2 `statuses.go` (L1) |

**What must be sequential and why:** A3 needs A1's `StampOwn` and A2's fields (interfaces); C5b binds
its mint to C5a's task field and C5c needs both (interfaces); D2 needs D1's change-set record; D3
needs the coordinator under `ee/` (an external plan); every other edge is one file.

## Check numbers

| Check | Measures | Written by | Run by |
| --- | --- | --- | --- |
| 230 | Author, committer and trailers on GitHub and Bitbucket; the default unchanged | X1 | Owner (A5) |
| 231 | The App's Checks and Commit statuses read permissions, accepted and working | X1 | Owner (B2) |
| 232 | A two-repository read-only installation token; a push with it is refused | X1 | Owner (C5b) |
| 233 | Withdrawn in review round 1 (Cloud Run cannot vary the image per execution); reserved, unused | — | — |
| 234 | `fugaro project sync --check` against a Bitbucket workspace | X1 | Owner (C1) |
| 235 | A two-repository change set flips ready together | X1 | Owner (D3) |

Checks 236–249 are reserved and unused. Every check runs on a sandbox repository and installation,
never by an agent.

## Release actions

- B2: `### For operators`: accept the GitHub App's new read permissions on each installation.
- C5c: `### For operators`: install the GitHub App on each repository used as context; redeploy the
  credentials service on opted-in installations.
- E1: none in its own PR; each `fugaro user` change is applied by the operator's next `fugaro update`
  (the command says so).
- No other task needs a post-deploy step. Slice A is opt-in per repository.
