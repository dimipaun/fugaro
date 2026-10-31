# Forge, authorship and images: implementation plan

## Status

**Plan only. Nothing is implemented. No task is approved: each task needs the owner's own explicit
go, and answering a decision is not approving a task.** It builds [spec.md](spec.md) **as amended by
[reconciliation.md](reconciliation.md)**, which wins where they differ.

**The owner answered D600–D649 on 2026-10-29: every answer is the recommendation except D618**
(shared images across repos, scoped to repos with an identical resolved dependency set), which
slice C below follows (C7, C8). Its tension T6 (open item O6) was resolved on 2026-10-29: shared
images stay under user control, a divergence is proposed and confirmed, never forked silently. Answers are not task approvals: slice A still needs the owner's explicit go. Every task
names the decisions it rests on. Tasks marked **GATED** are not planned in detail
because their decision may remove them.

Slices, each independent and orderable (D600 recommends A first, alone):

| Slice | What it ships | Licence (D637, proposed) | Gated on |
| --- | --- | --- | --- |
| **A** Authorship and trailers | Commits authored by the launcher when a repository opts in; `Fugaro-Run`, `Fugaro-Rigor`, `Fugaro-Model` trailers on the commits the runner pushes, on by default (D647) | MIT | D600–D606, D644, D645, D647–D649 |
| **B** Forge cleanup, CI status, GitLab | One list of forge kinds; a CI-status read; GitLab only after its own reconciliation | MIT | D610, D611; D614/D615 for B2 |
| **C** Repositories and images | Bitbucket listing in `fugaro project sync`; lockfile-first image layers; a digest check at launch; images shared between repos with the same normalised dependency set, under user control; read-only context repositories; a SECURITY.md registry note | MIT | D613, D617–D625 (D618 answered against the recommendation) |
| **D** Change sets | Linked runs created together, a draft hold, a settle step, advisory merge order | `ee/` (D616, amended in round 2); the task's wire fields in core (ee-reorg D521) | D613–D616, D641, D642; ee-reorg's coordinator move |
| **E** People and tokens | The roles design (D446) first; `fugaro user` over the existing IAM lists | MIT for E1; the rest per E0 | D627–D638 |

**Withdrawn in review round 1:** C2 (a local resolver in `fugaro project sync`) has no consumer until
a lock exists, since its output is intersected with `allow_repos` and sync offers only what
`allow_repos` does not admit; it moves into C7's design. E2 (`fugaro token add` over `secrets set`)
would create a project account any launcher of an admitted repository can select, which is the pool
and not D633's "Own", on a machine where a teammate has no rights to write it (F34); it moves into
E0. Check 233 is withdrawn (U4 resolved). **Withdrawn in review round 2:** E3 (`fugaro join`)
duplicates what exists (F45): a cloud command in a checkout whose `fugaro.yaml` names `gcp_project:`
fetches the shared config with no setup, `fugaro update --gcp-project` writes a new machine's config,
and `fugaro update-skills` installs the skills; E0 decides whether a `join` name is still wanted.

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
- **The CLI command list** (`internal/cli/root.go`) was reshaped by PR #1252 (a command registry,
  merged 2026-10-29 as `daa3a40a8`, after this plan's base); it also touched `internal/cli/run.go`.
  Every task reads `main` as it is when it starts.
- **A new `fugaro.yaml` key needs its own skew gate**: `fugaro.yaml` decodes with known fields only,
  so an older runner refuses the key; the launcher gates it as it gates `rigor:` (`rigorSince`,
  `internal/cli/recipes_skew.go`), and `docs/release.md`'s "Before you tag" list names the gate.
- **The run's identity** is whatever `main` has when A1 starts (open PR #1264 may make it the GitHub
  App's `[bot]` identity on GitHub, D645). No task hard-codes it.
- **No agent performs a live step**: no apply, no App permission change, no sandbox run. Those are
  OWNER-RUN, on a sandbox repository and installation only.

## Global constraints and review focus

1. **Commits are born stamped; nothing is rewritten** (F2, D603 as amended in review round 3). The
   agent verifies inside its own stage ("Commit first, then verify", `internal/runner/prompts.go`)
   and `Decide` matches the verified SHA exactly (`latestVerifiedTest`), and checkpoints push while
   the stage runs, so any rewrite after a commit is made would leave the run unverified or diverge
   from what was pushed. The author and the trailers are set **when the commit is made**: the
   agent's git environment carries the author, and a runner-owned `prepare-commit-msg` hook (which
   `--no-verify` does not suppress, githooks(5)) adds the trailers. Pinned by A3's
   `TestRunnerVerifiedCommitIsTheStampedCommit`.
2. **The repository's own hooks still run** (F2): pointing `core.hooksPath` at the runner's hook
   directory would silence them, so the runner's hook chains to the hook directory the repository
   configured. Pinned by A1's `TestStampHookChainsToRepositoryHooks`.
3. **The default does not change** (D606, D645). With no `git.author` key, author and committer are
   the run's identity as `main` resolves it per run; only the trailers are new (D647). Pinned by A3's
   `TestRunnerDefaultAuthorUnchanged`.
4. **No new long-lived secret in a job** (R3). Review-only: C5b's and C5c's independent reviews.
5. **Fugaro never writes `Signed-off-by`** (R1); D644 decides whether one is removed. Pinned by A1's
   `TestStampHookNeverAddsSignedOffBy`.
6. **The trailers are a record, not proof** (F1). The agent can unset its environment, bypass the
   hook with `-c core.hooksPath=…` on its own command line, or push the run branch itself with its
   token. The runner's own commits (autofix, digest, leftovers, backlog) are made with the same
   environment. Finalize counts the run's commits that lack a `Fugaro-Run` trailer and logs the
   count; it does not rewrite them. Pinned by A3's `TestRunnerOwnCommitsCarryTrailers` and
   `TestRunnerLogsUnstampedCommitCount`.
7. **A commit keeps the author git gives it**: cherry-picks keep their original author (U10), and an
   agent can set any author on its own commit; nothing re-authors them (D646 as amended).
8. **The agent's `.git/config` cannot steer the hook's trailer rules**: the hook calls
   `git interpret-trailers` with the trailer configuration given on its command line. Pinned by A1's
   `TestStampHookIgnoresRepositoryTrailerConfig`.
9. **Context repositories are read-only for the runner** (D613): no write token is minted for them,
   no push targets them, and each is admitted by the policy and named in a **launcher-minted claim**
   of the run identity (the service cannot read the task, and the task file is writable by the job).
   While a GitHub job still mounts the App key (`config.ServiceHeldSecretNames` empty, #907), the
   **agent** can mint its own token for any repository the App is installed on (SECURITY.md); C5
   adds no reach the agent lacks today. Pinned by C5b's and C5c's tests.
10. **The author is a claim** (F3, D604): docs and SECURITY.md say so until Slice E's identity exists.
    Pinned by A4's docs test.
11. **A change set's readiness is a coordination aid, not a security control** (T5): every member's
    record and PR can be written by that member's agent. The settle step re-reads every PR's live
    state and acts only on a set that agrees; human review stays the gate. Pinned by D3's tests.

## Tasks

### Slice A: authorship and trailers

**Re-planned in review round 3** (D603, D640, D646 amended): the first design rewrote commits after
each stage; it was withdrawn because the agent verifies inside its stage and checkpoints push while
the stage runs (Constraint 1). Commits are now born stamped.

#### A1. A stamp environment and a trailer hook in `gitops`

- **Decisions:** D601, D602, D603, D644, D645; Constraints 1, 2, 5, 8.
- **Files:** `internal/gitops/stamp.go` (new), `internal/gitops/stamp_hook.sh` (new, embedded),
  `internal/gitops/stamp_test.go` (new).
- **Interfaces produced:** `type Stamp struct { Author *Person; Trailers []Trailer }`;
  `func StampEnv(s Stamp, hookDir string) []string`: `GIT_AUTHOR_NAME`/`GIT_AUTHOR_EMAIL` when an
  author is given (the committer stays the run's identity), the trailer values as `FUGARO_STAMP_*`
  variables, and `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` setting `core.hooksPath`
  to `hookDir` (environment configuration outranks the repository's own file);
  `func WriteStampHooks(dir, chainTo string) error`: writes the `prepare-commit-msg` hook, which runs
  `git interpret-trailers --in-place` with `--trailer` options and `-c trailer.*` overrides of its
  own (`Fugaro-*` keys replaced, never duplicated, case-insensitively as git matches them), adds no
  `Signed-off-by` (and removes one only if D644 is answered so), omits `Fugaro-Model` when the
  variable is empty, and then runs the same-named hook from `chainTo` (the repository's configured
  hook directory) and every other hook name by a pass-through script.
- **Schema/rules/config:** none.
- **Tests:** `TestStampEnvSetsAuthorOnlyWhenGiven`, `TestStampHookAddsTrailers`,
  `TestStampHookReplacesCaseFoldedFugaroTrailers`, `TestStampHookNeverAddsSignedOffBy`,
  `TestStampHookOmitsEmptyModel`, `TestStampHookRunsUnderNoVerify`,
  `TestStampHookChainsToRepositoryHooks`, `TestStampHookIgnoresRepositoryTrailerConfig`,
  `TestStampEnvOutranksRepositoryHooksPath`.
- **Command:** `go test ./internal/gitops/ -run 'TestStamp' -count=1`
- **Depends on:** none.
- **Red step:** the tests reference `StampEnv`, which does not exist: compile failure.
- **Commit:** `gitops: an environment and a hook that stamp commits as they are made`
- **Reviewer:** standard, plus an independent review (agent-controlled input reaches git).

#### A2a. The author in the task, and the repository's opt-in

- **Decisions:** D604, D606.
- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/config/config.go`
  (`Git.Author`: `fugaro` or `launcher`), `internal/config/validate.go`,
  `schemas/fugaro.schema.json`, `internal/task/task_test.go`, `internal/config/validate_test.go`,
  `schemas/schemas_test.go`.
- **Interfaces produced:** `task.Spec.Author *task.Person` (`name`, `email`; omitted when empty;
  when present, `Validate` refuses control characters, newlines, `<` and `>` in either and an email
  without one `@`); `config.Git.Author string`.
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

- **Decisions:** D604, D605, D606, D649.
- **Files:** `internal/localcfg/localcfg.go` (optional `commit_name`, `commit_email`),
  `internal/localcfg/shared.go` (both blanked), `internal/localcfg/shared_test.go`,
  `internal/cli/run.go`, `internal/cli/recipes_skew.go` (an `authorSince` gate for the task field
  and a `gitAuthorSince` gate for the `fugaro.yaml` key), `internal/cli/init.go` (`fugaro update
  --repo` gates the key as `gateRigor` gates `rigor:`), `docs/release.md` ("Before you tag" names both
  gates), `internal/cli/run_author_test.go` (new).
- **Interfaces consumed:** `localcfg.Config.Me`, `task.Spec.Author`.
- **Behaviour:** the author is `commit_email` (else `Me`) with `commit_name` (else `git config
  user.name`, else the email's local part). `commit_email` is accepted only when it equals `Me` or is
  a forge noreply address (D649). The author is omitted, so the run's identity stays the author, when
  `Me` is empty or is not an email (`user: alice`, a CI or bot account), for review and artifact
  launches (D605), and against an older runner (the skew gate). A launch or `fugaro update --repo`
  whose `fugaro.yaml` sets `git.author` against an older runner is refused with the upgrade remedy, as
  `rigor:` is.
- **Tests:** `TestRunAuthorFromLocalConfig`, `TestRunAuthorCommitEmailOverridesMe`,
  `TestRunAuthorRefusesAColleaguesAddress`, `TestRunAuthorOmittedWithoutIdentity`,
  `TestRunAuthorOmittedForNonEmailIdentity`, `TestRunAuthorOmittedForOlderRunner`,
  `TestRunAuthorOmittedForReviewRun`, `TestRunAuthorGitAuthorKeyGatedForOlderRunner`,
  `TestGitAuthorSinceIsTheReleaseTheChecklistNames`, `TestSharedBlanksCommitIdentity`.
- **Command:** `go test ./internal/cli/ ./internal/localcfg/ -run 'TestRunAuthor|TestGitAuthorSince|TestSharedBlanksCommitIdentity|Docs' -count=1`
- **Depends on:** A2a.
- **Red step:** `TestRunAuthorFromLocalConfig` fails: the written task has no author.
- **Commit:** `cli: fugaro run records the launcher as the commit author`
- **Reviewer:** standard.

#### A3. The runner stamps commits as they are made

- **Decisions:** D601–D605, D647; Constraints 1, 3, 6.
- **Files:** `internal/runner/runner.go` (the agent's environment per stage, beside the
  `gitops.Identity` loop; the finalize count), `internal/runner/gate.go` and
  `internal/runner/digest.go` (their commits use the same environment), `internal/runner/stamp_test.go`
  (new).
- **Interfaces consumed:** `gitops.StampEnv`, `gitops.WriteStampHooks`, `task.Spec.Author`,
  `config.Git.Author`, `runstore.Record.RunID`, `Record.Rigor`, the stage's configured model
  (`StageTiming.Model`), the run's identity as `main` resolves it per run (D645).
- **Behaviour:** at bootstrap the runner writes the hooks into a directory outside the work tree,
  chained to the repository's configured hook directory; each agent stage's environment carries
  `StampEnv` (author only when `git.author: launcher` and `Spec.Author` is set; `Fugaro-Model` the
  stage's pin, or none); the runner's own commits use the same environment; checkpoints, verification
  and pushes are unchanged; finalize logs how many of the run's commits lack `Fugaro-Run`. Review
  and artifact runs never stamp.
- **Tests:** `TestRunnerVerifiedCommitIsTheStampedCommit`, `TestRunnerStampsAgentCommits`,
  `TestRunnerStampedCommitsReachTheCheckpointPush`, `TestRunnerDefaultAuthorUnchanged`,
  `TestRunnerAuthorOptIn`, `TestRunnerModelTrailerIsTheStagePin`,
  `TestRunnerModelTrailerOmittedWithoutPin`, `TestRunnerOwnCommitsCarryTrailers`,
  `TestRunnerLogsUnstampedCommitCount`, `TestRunnerReviewAndArtifactNeverStamp`, and the existing
  `TestDigestStepCommitsAsRunner` unchanged.
- **Command:** `go test ./internal/runner/ -run 'TestRunner(VerifiedCommit|Stamp|DefaultAuthor|AuthorOptIn|ModelTrailer|OwnCommits|LogsUnstamped|ReviewAndArtifact)|TestDigestStepCommitsAsRunner' -count=1 -timeout 20m`
- **Depends on:** A1, A2a, A2b.
- **Red step:** `TestRunnerStampsAgentCommits` fails: the agent's commit has no `Fugaro-Run` trailer.
- **Commit:** `runner: the agent's commits carry the author and Fugaro trailers as they are made`
- **Reviewer:** standard, plus an independent review (it touches every run's commits).

#### A4. Docs

- **Decisions:** D604, D612, D643, D647.
- **Files:** `docs/git-providers.md` (a "Commit author and trailers" section replacing the sentence
  that commits are authored as `Fugaro` whatever the token is called, and a "Supported forges" line:
  cloud forges supported, an owner-allow-listed Bitbucket root accepted and unsupported),
  `SECURITY.md` ("Known limits": the author is the launcher's claim; trailers are a record, not
  proof; a launcher may approve a PR their name is on), `website/content/security.md` (regenerated),
  the next release's notes file under `docs/releases/` (one Highlights line: trailers on by default;
  no operator step), `internal/cli/docs_commit_author_test.go` (new).
- **Tests:** `TestDocsCommitAuthorIsAClaim`, `TestDocsTrailerNames`, `TestDocsSupportedForges`.
- **Command:** `go run ./internal/clidoc`; then `go test ./internal/cli/ -run 'Docs' -count=1`; in
  `website/`, `npm run check`.
- **Depends on:** A3.
- **Red step:** the new docs test fails before the sections exist.
- **Commit:** `docs: commit author and Fugaro trailers, and what they do not prove`
- **Reviewer:** standard, plus the independent review `.claude/rules/credentials-service.md` requires
  for `SECURITY.md`.

#### A5. Check 230 (OWNER-RUN)

- **Measures:** on a sandbox GitHub repository and a sandbox Bitbucket repository, with
  `git.author: launcher` committed: the run's boundary commits show the launcher as author and the
  run's identity as committer, with the three trailers; blame and the PR's commit list show the
  launcher; no `Signed-off-by`; and with the key absent, the author is the run's identity.
- **The owner types,** in each sandbox checkout:
  `fugaro run --title "Check 230" "Add a line to CHECK230.md"`, then on the PR branch
  `git log --format='%an <%ae> | %cn <%ce>%n%(trailers)' origin/main..`; then squash-merges the PR
  in the forge's UI and runs `git log -1 --format='%an <%ae>%n%B' origin/main` (D648, U9); and
  records both results under Check 230 in `docs/gcp-live-checklist.md`.
- **Written by:** X1. **Run by:** the owner. **Depends on:** a release with A3.

### Slice B: forge

#### B1. One list of forge kinds

- **Decisions:** D610, D611.
- **Files:** `internal/gitprov/kinds.go` (new), `internal/gitprov/kinds_test.go` (new),
  `internal/gitprov/gitprov.go`, `internal/gitprov/providers/providers.go`.
- **Behaviour:** `gitprov.Kinds` lists the forge kinds; `providers.FromEnv` and its error text read
  it. The test parses the non-test Go files (`go/parser`) for uses of the `KindGitHub` and
  `KindBitbucket` identifiers and fails on any file not in its inventory table; a second, hand-kept
  table lists the non-Go sites (the image build's credential step, Terraform secret names, the
  plugin skills) and the test checks each still exists. Adding a string `"github"` elsewhere does
  not trip it. A future GitLab task reads both tables as its to-do list. No behaviour change.
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
  `testdata/`, `internal/gitprov/github/live_test.go` (new, `live` tag: reads a sandbox commit's
  checks), `internal/gitprov/github/installcheck.go` (`RequiredPermissions` gains the run token's
  `checks: read` and `statuses: read`, so doctor reports an installation that has not accepted them), `docs/git-providers.md` (the App gains **Checks: read** and **Commit statuses:
  read**), the next release's notes file under `docs/releases/` (`### For operators`: accept the
  App's new permissions on every installation).
- **Tests:** `TestGitHubChecksCombinesRunsAndStatuses`, `TestBitbucketStatusesMapToCheckState`,
  `TestFakeChecks`, `TestRequiredPermissionsIncludeChecks`; live: `TestLiveGitHubChecks`.
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
  `internal/gitprov/bitbucket/list_test.go` (new), `internal/cli/project_enroll_test.go`,
  `website/content/cli-reference.md` (regenerated by `go run ./internal/clidoc`).
- **Behaviour:** lists the workspace's or project's repositories with a token read from stdin or a
  hidden prompt (never argv), offers the ones `allow_repos` does not admit, confirms as today;
  `--check` prints the difference and writes nothing.
- **Tests:** `TestBitbucketListPaginates`, `TestProjectSyncBitbucketOffersUnadmitted`,
  `TestProjectSyncCheckWritesNothing`, `TestProjectSyncBitbucketTokenNeverFromArgs`,
  `TestProjectSyncBitbucketRefusedInAgentSession`.
- **Command:** `go test ./internal/cli/ ./internal/gitprov/bitbucket/ ./internal/clidoc/ -run 'ProjectSync|BitbucketList|CliReference' -count=1`
- **Depends on:** E1 (`website/content/cli-reference.md`, earlier level).
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
    `internal/budget/token/token.go`, `internal/budget/token/mint.go`,
  `internal/budget/token/exchange.go` and `internal/cli/launch_budget.go` (a launcher-minted claim
  naming the context repositories; secrets-brokering's planned B1.1 adds branch and PR claims the
  same way, D497a, and is **not built**: whichever lands second rebases on the other),
  `website/content/cli-reference.md` (regenerated), `internal/cli/run_context_test.go` (new).
- **Behaviour:** each context repository must be admitted by `allow_repos`; at most **5** per run;
    refused for Bitbucket until the proxy admits them (secrets-brokering D496 binds fetch to the task's
  repository); refused unless the installation opted into the credentials service (the in-job mint
  path is not extended); refused when the claim would push the custom token past its byte limit
  (`mint.go`'s 1000-byte bound on claims). The list goes into the task (for the runner) and into the run identity's claim (for
  the service, which cannot read the task).
- **Tests:** `TestRunContextReposMustBeAdmitted`, `TestRunContextReposCappedAtFive`,
  `TestRunContextRefusedOnBitbucket`, `TestSpecWithoutContextIsByteIdentical`,
    `TestTokenContextClaimMatchesTask`, `TestRunContextRefusedWithoutService`,
  `TestTokenContextClaimWithinByteLimit`.
- **Command:** `go run ./internal/clidoc`; then `go test ./internal/cli/ ./internal/task/ ./internal/budget/token/ ./internal/clidoc/ -run 'Context|CliReference' -count=1`
- **Depends on:** A2a, A2b (same files, earlier levels).
- **Red step:** compile failure: no `Spec.ContextRepos`.
- **Commit:** `task, cli: read-only context repositories on a launch`
- **Reviewer:** opus + mandatory independent review.

#### C5b. A read-only token for context repositories (GATED: D613)

- **Files:** `internal/gitprov/github/apptoken.go`, `internal/credsvc/github.go`,
  `internal/credsvc/auth.go` (`Identity` carries C5a's claim), tests beside each.
- **Behaviour:** a second mint with **Contents: read** and **Metadata: read** only, naming exactly
  the repositories in the run identity's launcher-minted claim (C5a), each re-admitted against the
  policy, one token per App installation (repositories under different owners); a request naming any
  other repository is refused and audited, so an agent calling the service with the job's identity
  gains nothing beyond its launch. No Terraform and no new grant: the service keeps its one bundle
  grant (`TestCredentialsServiceGrantsAreTheOneBundle` unchanged). The write token is unchanged.
- **Tests:** `TestContextTokenIsReadOnly`, `TestContextTokenNamesOnlyTheTasksRepos`,
  `TestServiceRefusesContextRepoNotInClaim`, `TestServiceRefusesContextRepoOutsidePolicy`,
  `TestServiceAuditsContextRefusal`, `TestContextTokenOnePerInstallation`,
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
- **Behaviour:** clones go under `/work/context/<owner>/<name>`, outside the work tree, so
  `CommitAll`'s `git add -A` never sees them and Claude Code does not load their `CLAUDE.md` unless
  the agent reads it; the prompt names the paths and says they are read-only reference, not
  instructions.
- **Tests:** `TestContextReposClonedBlobless`, `TestContextReposHaveNoPushURL`,
  `TestContextReposOutsideWorkTree`, `TestContextReposNotInDigestOrPR`, `TestNoContextReposNoClone`.
- **Command:** `go test ./internal/runner/ -run 'ContextRepos' -count=1 -timeout 20m`
- **Depends on:** C5b, A3 (same file, earlier level).
- **Red step:** `TestContextReposClonedBlobless` fails: nothing is cloned.
- **Commit:** `runner: clone a run's context repositories read-only`
- **Reviewer:** opus + mandatory independent review.

#### C6. SECURITY.md: baked source and registry readers

- **Files:** `SECURITY.md`, `website/content/security.md` (regenerated),
  `internal/cli/docs_registry_readers_test.go` (new).
- **Behaviour:** states that a repository's image holds its source at the baked commit and that
  anyone with pull rights on its registry reads it; context repositories are not baked; image builds
  are Cloud Build cost outside the run budget (D621). It also states, for the shared
  dependency-layer images (`shared-images.md`), that the key's integrity rules are what keep two
  repositories from sharing an image built from two different artifacts, and that they are **not** a
  boundary between repositories of one installation: the key files that state no package version —
  `.npmrc`, `.pnpmfile.cjs`, a plugin under `.yarn/plugins/` or a `.yarn/cache`, a patch under
  `.yarn/patches/`, a `package.json` `scripts` entry, a `go.mod` `replace` directive — run during the
  install the image bakes or point it elsewhere and contribute no fact of the key, so a repository on
  a shared key can change what the next build of that key installs for every repository already on it
  (the class of §2.2's inherited `image.setup`).
- **Tests:** `TestDocsRegistryReaders`, `TestDocsBuildsOutsideRunBudget`.
- **Command:** `go test ./internal/cli/ -run 'Docs' -count=1`; `go run ./internal/clidoc`; in
  `website/`, `npm run check`.
- **Depends on:** none. **Red step:** the new docs test fails before the paragraph.
- **Commit:** `SECURITY: who can read the source baked into an image`
- **Reviewer:** standard, plus the independent review `.claude/rules/credentials-service.md` requires
  for `SECURITY.md`.

#### C7. Shared images keyed by the resolved dependency set: a design (D617, D618 as answered, D622)

Docs only, in this folder. D618 was answered **not as recommended**: images are shared across
repositories, but only between repositories with an identical resolved dependency set, so a change
to one forks it automatically. The design covers: the key (a hash of the resolved dependency set:
the lockfiles and tool files `imagecheck.Record.KeyFiles` tracks, the base image and `image.setup`,
resolved on the operator's machine, D622); the D617 lock in the runs bucket beside `builds/` that
records hash to digest; how `fugaro image refresh` reads it; fork, never mutate in place; what is
baked (today's derived image bakes one repository's source at its commit, so the shared layer holds
only the dependency set and each run still gets its source from the clone); the migration from
per-(repository, workflow) images; the build's credential; and the local resolver command (withdrawn
C2) as the key's input. `fugaro project sync` never un-enrolls; removed repositories are dropped from
the lock only. It answers reconciliation T6 (a)-(c) as resolved on 2026-10-29 and adds the three image-count
controls: the key is **normalised** (only resolved package names, versions and the integrity each key
file states for them, toolchain and base image; file order, comments, formatting, repository name and
build or test commands are ignored; a package resolved from a source that is not the ecosystem's
registry leaves the repository unkeyed — D618, answered 2026-10-31, as §2.3 of the design states); a
**superset** lookup reuses an existing image that holds the repository's whole dependency set at the
same versions, even with extras (the wider supply-chain surface is the stated cost); and a cap
warning plus clean-up of images unused for N days (proposed N = 30; the owner confirms). Dev-only
dependencies still make a key: the owner did not choose to exempt them. A divergence is never forked
automatically (not the recommendation): the design specifies the proposal and its non-interactive
behaviour (C8). C8 may start once the owner gives the go; T6 is no longer a gate.

#### C8. Build, lock, proposal and job update for shared images (after C7; the owner's go)

- **Decisions:** D617, D618, D619, D620, D621.
- **Interfaces:** `imagecheck.Key(resolved) string` (the normalised hash: package names, versions and
  their integrities, toolchain, base image only), `imagecheck.Lock` (hash to digest, a JSON object
  in the runs bucket under `builds/`, decoded strictly, schema in `schemas/`; the one source),
  `imagecheck.Superset(lock, resolved)` (an existing image holding the whole set at the same versions),
  `imagecheck.Record`
  gains `Key`; the job update takes the lock's digest for a repository's hash; a proposal type with
  the choices fork, adopt `<repository>` and stay-pinned; `fugaro image ls` (images, their hashes,
  the repositories on each, last use) and `fugaro image refresh --on-divergence=fork|adopt=<repo>|pin`
  (the flag that names the choice); a retention value, `images.retention_days`, default 30 (owner to
  confirm) and an image-count cap value.
- **Files:** `internal/imagecheck/record.go`, `internal/imagecheck/lock.go` (new),
  `internal/imagecheck/lock_test.go` (new), `internal/image/render.go`, `internal/cli/image_refresh.go`,
  `internal/cli/image_record.go`, `internal/imagecheck/key.go` and `superset.go` (new, with tests),
  `internal/cli/image_ls.go` (new), `internal/cli/image_proposal.go` (new), `internal/cli/doctor.go`
  (the cap warning), `internal/cli/image_prune.go` (new, the unused-image clean-up),
  `internal/backend/gcp/` (the per-repository job update), `schemas/` (the lock). `internal/cli/run.go` is not touched here (C4 reads the lock through
  `imagecheck`), so it stays within its existing writers.
- **Behaviour:** `fugaro image refresh` computes each repository's normalised key; before building it
  looks up an existing image whose set is a superset at the same versions and uses it; repositories
  with the same key build once, from the shared base for that key, and share the digest. When a
  repository's key diverges from its shared image, `fugaro image refresh` and `project sync`
  **propose and wait** (propose-then-confirm, as D625): fork to a new image, adopt another
  repository's compatible image, or stay pinned to the old image with a warning. Nothing is built
  silently. Non-interactive (no terminal, or `--json`): it refuses to build, prints the proposal and
  exits non-zero unless `--on-divergence` names the choice. A choice updates only that repository's
  job. An image already in the lock is never rebuilt or overwritten in place; the lock is written
  after a successful build; D621's triggers fire per key only when an input hash changes; D619's
  launch warning compares the job's digest with the lock's. `doctor` and `fugaro image ls` warn when
  the image count passes the cap; images no job has used for N days (`images.retention_days`,
  proposed 30) are cleaned up automatically, never one a job still points at.
- **Tests:** `TestKeyIdenticalForIdenticalDependencySets`, `TestKeyChangesWithAnyLockfile`,
  `TestRefreshBuildsOnceForSharedKey`, `TestRefreshForksOnChange`,
  `TestRefreshNeverOverwritesALockedDigest`, `TestRefreshUpdatesOnlyTheChangedRepositoryJob`,
  `TestLockDecodesStrictly`, `TestRunDigestComparesAgainstTheLockForItsKey` (extends C4),
  `TestKeyIgnoresOrderCommentsFormattingRepoNameAndCommands`, `TestKeyChangesWithVersionToolchainOrBase`,
  `TestSupersetReusesAnImageWithExtras`, `TestSupersetRequiresSameVersions`,
  `TestDivergenceProposesAndWaits`, `TestDivergenceNonInteractiveRefusesWithoutFlag`,
  `TestDivergenceFlagNamesTheChoice`, `TestStayPinnedWarns`, `TestImageLsListsHashesAndRepos`,
  `TestDoctorWarnsOverImageCap`, `TestPruneRemovesImagesUnusedForRetentionDays`,
  `TestPruneKeepsAnImageAJobPointsAt`.
- **Command:** `go test ./internal/imagecheck/ ./internal/image/ ./internal/cli/ -run 'Key|Lock|Superset|Refresh|Divergence|ImageLs|Prune|DoctorWarnsOverImageCap|TestRunDigest' -count=1`
- **Depends on:** C3 and C4 (the same files, earlier level), C7 (the design).
- **Red step:** `TestRefreshBuildsOnceForSharedKey` fails: two repositories build twice.
- **Commit:** `image: repositories with the same resolved dependency set share one image`
- **OWNER-RUN:** a sandbox refresh across two repositories with one set and one with another;
  confirm one build, two jobs on one digest, a proposal (not a build) on a dependency change, and
  each of fork, adopt and stay-pinned (a new live check
  number is taken from the reserved 236-249 block when X1 is next amended).
- **Reviewer:** opus + mandatory independent review (the registry layout and job definitions of
  every installation change).

### Slice D: change sets (GATED: D613, D614, D616, D641, D642)

The whole slice is `ee/` behaviour (D616 as amended): the task's wire fields sit in core (ee-reorg
D521), an untagged CLI refuses `--change-set` like the other paid-command stubs (D522), and even a
tagged CLI refuses it until D3 has shipped, so no change set can be stuck in draft.

#### D1. Change sets created together, linked PRs

- **Files:** `internal/task/task.go` (`Spec.ChangeSet`: an ID and the member list),
    `schemas/task.schema.json`, `internal/cli/run.go` (`--change-set` makes `--repo` repeatable and
  launches one run per repository, writing every member's task with the full member list; refused
  until D3 lifts it), `internal/cli/recipes_skew.go`, `website/content/cli-reference.md`
  (regenerated),
  `internal/runner/prflow.go`, `internal/gitprov/status.go` (a "Change set" line in the status
  section), `internal/runstore/changeset.go` (new: the member's PR URL and verdict under the run's
  own `runs/<slug>/<run-id>/` prefix; **not** a trust boundary, since runs of one repository share
  their prefixes and the agent can act as the job, T5), tests beside each.
- **Tests:** `TestChangeSetMembersFixedAtLaunch` (calls the launch below the refusal),
  `TestChangeSetRefusedUntilSettleExists`, `TestChangeSetRefusedByUntaggedBuild` (`//go:build !ee`), `TestChangeSetStatusSectionListsSiblingPRs`,
  `TestSpecWithoutChangeSetIsByteIdentical`.
- **Command:** `go test ./internal/task/ ./internal/runstore/ ./internal/gitprov/ ./internal/cli/ -run 'ChangeSet' -count=1`; `go test -tags ee ./internal/cli/ -run 'ChangeSet' -count=1`
- **Depends on:** C5a (same files, earlier level).
- **Red step:** compile failure: no `Spec.ChangeSet`.
- **Commit:** `change sets: runs launched together, and each PR lists its siblings`
- **Reviewer:** standard.

#### D2. The draft hold

- **Files:** `internal/runner/runner.go` (finalize: a change-set run never flips ready; it records
  its verdict), `internal/runstore/changeset.go`, `internal/cli/run.go` (a `--pr` follow-up on a
  member PR carries the change set), `internal/runner/changeset_test.go` (new).
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
    in `internal/gitprov/github/apptoken.go` (D641), `internal/cli/run.go` (lifts D1's refusal for a
  tagged build; `TestChangeSetRefusedUntilSettleExists` is replaced by
  `TestChangeSetLaunchesOnceSettleExists`), and the regenerated `ee` CLI reference.
- **Behaviour:** reads every member's record and every PR's live state (head SHA, draft flag);
  only when every listed member recorded `ship` and passing tests on the PR's current head, and every
  member lists the same set, flips every PR ready through `UpdatePR` and `ApplyReady` with the
  settle-only token, scoped to the members' repositories; otherwise reports. Idempotent: a partial
  failure is reported and the next settle finishes it. A coordination aid, not a security control
  (T5). Run by the coordinator job or by hand.
- **Tests:** `TestSettleFlipsAllWhenEveryMemberShips`, `TestSettleRefusesAMemberNotListedByAll`,
  `TestSettleRereadsPRStateNotTheRecord`, `TestSettleRefusesAHeadThatMoved`,
  `TestSettleRetryFinishesAPartialFlip`, `TestSettleTokenIsPullRequestsWriteOnly`,
  `TestSettleTokenNamesOnlyMemberRepos`.
- **Command:** `go test -tags ee ./ee/coordinator/... ./internal/gitprov/github/ -run 'Settle' -count=1`
- **Depends on:** D2; **ee-reorg's coordinator move** (external).
- **Red step:** compile failure: no settle step.
- **OWNER-RUN:** Check 235 (a two-repository change set on the sandbox flips together).
- **Reviewer:** opus + mandatory independent review (writes PR state across repositories).

#### D4. Advisory merge order (`ee/coordinator`)

- **Files:** `ee/coordinator/changeset.go`, `ee/coordinator/changeset_test.go`,
  `internal/gitprov/status.go` (the order line).
- **Behaviour:** the change set records an order at launch; each PR body states it; the coordinator
  view shows it. No blocking (D615). The mobile `merge` action naming the order is not planned here
  (a mobile write-surface change, D636's posture).
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
  `internal/cli/root.go` (or the command registry #1252 added), `plugin/skills/working/SKILL.md`,
  `website/content/cli-reference.md` (regenerated).
- **Relation to what exists:** `fugaro setup --launcher/--operator` replaces a whole list and the
  new-machine path names the roles (F45); `fugaro user` adds and removes one member safely.
- **Behaviour:** refreshes `terraform.launchers` and `terraform.operators` from the installation's
  own outputs (the copy-back `init_new_machine.go` already does) and **refuses** when the local list
  differs from them; then edits the list (`--role launcher|operator`, `user:` members only: `domain:`,
  `group:` and `serviceAccount:` are refused), prints an invite (project name, install line, "the
  repository's `fugaro.yaml` names the project; no setup is needed"), and tells the operator that the
  change takes effect on their next `fugaro update` (OWNER-RUN). No email. `ls` reads the lists only.
- **Tests:** `TestUserAddWritesLauncher`, `TestUserAddRequiresRole`, `TestUserAddRefusesDomain`,
  `TestUserRefusesStaleLocalList`, `TestUserRmRemoves`, `TestUserLsListsBoth`,
  `TestUserRefusedInAgentSession`, `TestUserInviteHasNoSecret`.
- **Command:** `go run ./internal/clidoc`; then `go test ./internal/cli/ ./internal/clidoc/ -run 'TestUser|CliReference' -count=1`; `go test ./plugin/... -count=1`
- **Depends on:** none.
- **Red step:** compile failure: no `newUserCmd`.
- **Commit:** `cli: fugaro user, a front for the installation's launchers and operators`
- **Reviewer:** opus + mandatory independent review (IAM lists).

#### E4 onward (GATED: E0)

Google sign-in exchanged for a custom token with a role claim; role checks in the database rules;
the service's authenticated token write route and `fugaro token add`; `fugaro user role|suspend|
resume`; the console's read-only Members list; mobile suspend. Each is security-sensitive and written
from E0.

### X1. The live-check entries

- **Files:** `docs/gcp-live-checklist.md` (Checks 230, 231, 232, 234, 235, each NOT RUN, with its
  prerequisites, the exact commands and the task that makes it runnable),
  `internal/cli/docs_forge_checks_test.go` (new).
- **Tests:** `TestDocsForgeChecksWritten`, `TestDocsForgeChecksNotRun` (the commit that records an
  owner's result updates it, as the checklist's other checks do).
- **Command:** `go test ./internal/cli/ -run 'Docs' -count=1`
- **Depends on:** none. **Red step:** the new test fails before the entries exist.
- **Commit:** `docs: live checks 230-232, 234 and 235 for forge, authorship and change sets (not run)`
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
| D617, D618 (T6 resolved 2026-10-29), D622 | C7, C8 (normalised key, superset reuse, lock, proposal, `image ls`, cap warning, clean-up) |
| D619 (with D618) | C4, C8 |
| D619 | C4 |
| D620 | C3 |
| D621 | C6 (`TestDocsBuildsOutsideRunBudget`); no nightly job is planned |
| D623, D625 | C1 |
| D624 | none: the marker exists (`gcp_project:`) |
| D626 | none here: project-centric phase 6 owns the backing folder |
| D627–D636, D638 | E0, E1, E4 onward |
| D639 | none (bookkeeping: the open questions are listed in reconciliation §6) |
| D640 | none: withdrawn by the re-plan (commits are born stamped) |
| D641, D642 | D1, D3 |
| D643 | A4 |
| D644 | A1 |
| D646 | none: nothing is re-authored |
| D649 | A2b |
| D647 | A3, A4 |
| D648 | A5 (Check 230's squash step) |

## Parallel execution

**Edges** (from the Depends-on lines): A1→A3; A2a→A2b→A3→A4→A5; B1→B2; A2a→C5a; A2b→C5a;
C5a→C5b→C5c; A3→C5c; C5a→D1; D1→D2; C5c→D2; D2→D3→D4; E1→C1. External: C5a and secrets-brokering B1.1 (the same token files; whichever lands second rebases).

The file-sharing edges (A2a→C5a, A2b→C5a, A3→C5c, C5a→D1, C5c→D2, E1→C1) exist only to keep
one writer per file per level when slices run together. **A slice approved alone runs its own chain
only.**

**Levels:**

| Level | Tasks (concurrent) |
| --- | --- |
| **L0** | A1, A2a, B1, C3, C4, C6, E1, X1; docs B3, C7, E0 |
| **L1** | A2b, B2, C1, C8 |
| **L2** | A3, C5a |
| **L3** | A4, C5b, D1 |
| **L4** | A5 (OWNER-RUN), C5c |
| **L5** | D2 |
| **L6** | D3 (also waits on ee-reorg's coordinator move) |
| **L7** | D4 |

**Critical path:** A2a → A2b → C5a → C5b → C5c → D2 → D3 → D4 (eight levels), with D3 also gated on
the ee-reorg coordinator move. Slice C's image chain is C3, C4, C7 (L0) then C8 (L1) and is off the
critical path. **Slice A's own path** is A2a → A2b → A3 → A4 → A5 (five levels; A1
runs beside A2a).

**Shared files (no level holds two writers of one file):**

| File | Writers (level) |
| --- | --- |
| `internal/task/task.go`, `schemas/task.schema.json` | A2a (L0), C5a (L2), D1 (L3) |
| `internal/cli/run.go` | C4 (L0), A2b (L1), C5a (L2), D1 (L3), D2 (L5), D3 (L6) |
| `internal/cli/recipes_skew.go` | A2b (L1), C5a (L2), D1 (L3) |
| `internal/runner/runner.go` | A3 (L2), C5c (L4), D2 (L5) |
| `internal/runner/prflow.go` | D1 (L3) |

| `internal/gitprov/gitprov.go` | B1 (L0), B2 (L1) |
| `internal/gitprov/status.go` | D1 (L3), D4 (L7) |
| `internal/gitprov/github/apptoken.go` | C5b (L3), D3 (L6) |
| `internal/credsvc/github.go` | C5b (L3) |
| `internal/runstore/changeset.go` | D1 (L3, new), D2 (L5) |
| `ee/coordinator/changeset.go` | D3 (L6, new), D4 (L7) |
| `internal/cli/project_enroll.go` | C1 (L1) |
| `website/content/cli-reference.md` | E1 (L0), C1 (L1), C5a (L2), D1 (L3) |
| `internal/cli/init.go` | A2b (L1) |
| `internal/gitprov/github/installcheck.go`, `internal/gitprov/github/live_test.go` | B2 (L1) |
| `internal/budget/token/mint.go`, `exchange.go`, `internal/cli/launch_budget.go` | C5a (L2) |
| `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go` | A2b (L1) |
| `internal/cli/root.go`, `plugin/skills/working/SKILL.md` | E1 (L0) |
| `internal/runner/gate.go`, `internal/runner/digest.go` | A3 (L2) |
| `internal/budget/token/token.go` | C5a (L2) |
| `internal/credsvc/auth.go` | C5b (L3) |
| `docs/release.md` | A2b (L1) |
| `SECURITY.md`, `website/content/security.md` | C6 (L0), A4 (L3) |
| `docs/git-providers.md` | B2 (L1), A4 (L3) |
| `docs/gcp-live-checklist.md` | X1 (L0) |
| the next release's notes file under `docs/releases/` | B2 (L1), A4 (L3), C5c (L4) |
| `internal/config/config.go`, `internal/config/validate.go`, `schemas/fugaro.schema.json`, `schemas/schemas_test.go` | A2a (L0) |
| `internal/imagecheck/record.go` | C4 (L0), C8 (L1) |
| `internal/imagecheck/lock.go`, `key.go`, `superset.go`, `internal/cli/image_refresh.go`, `image_record.go`, `image_ls.go`, `image_proposal.go`, `image_prune.go`, `internal/cli/doctor.go`, `internal/backend/gcp/` job update | C8 (L1) |
| `images/derived/Dockerfile.tmpl` | C3 (L0) |
| `internal/image/render.go` | C3 (L0), C8 (L1) |
| `internal/gitprov/providers/providers.go` | B1 (L0) |
| `internal/gitprov/bitbucket/` (new files) | B2 `statuses.go` (L1), C1 `list.go` (L1): different files |

**What must be sequential and why:** A3 needs A1's `StampEnv` and hooks and A2's fields (interfaces); C5b binds
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
- No other task needs a post-deploy step. In Slice A the author is opt-in per repository; the
  trailers are on by default (D647), which A4's Highlights line says.
