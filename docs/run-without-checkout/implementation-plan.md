# Launching a run for a repository without its checkout: implementation plan

**Status: plan only; no task approved; nothing is implemented.** The tasks below are written against
**PR #1477's** tree, not `main`'s (reconciliation F1): the refusal this design answers ships in that
pull request, so A2 either rides in it or lands immediately after it. The decisions each task
answers are named in its heading; they are **D901–D914** in
[reconciliation.md §3](reconciliation.md), none of them answered.

**Slices.** **A** is the design. **B** is the documentation and the release. **C** is the one gate
that cannot be skipped. **D** is the work this design deliberately does not do, with the reason.

Every task is at most 12 files, names its own red step, and is committed on its own. The commands
are the ones the repository's own rules require: targeted `-run` filters, `GOFLAGS=-p=2`, never a
whole `internal/cli` or `internal/runner` run.

## A1. The file at a ref, read through the App (D902, D910, D911). **SEC**

- **Files:** `internal/gitprov/github/fileread.go` (new), `internal/gitprov/github/fileread_test.go`
  (new), `internal/gitprov/github/apptoken.go`, `internal/gitprov/github/testdata/fileread/*.json`
  (new, an `httpfixture` replay of one read).
- **Interfaces:** `ConfigReadTokenPermissions` — the `contents:read` + `metadata:read` map, beside
  `RunTokenPermissions` and `CoordinatorTokenPermissions`; and
  `FileAt(ctx context.Context, o Options, repo, ref, path string) ([]byte, error)`, which asks
  `GET /repos/{owner}/{repo}/contents/{path}?ref={ref}` through `Options.BaseURL`/`Options.HTTP` and
  decodes GitHub's contents response (base64 with embedded newlines), refusing a directory, a
  symlink, a submodule and a body over a named size bound rather than returning something else. A
  missing file is a typed distinction from a refused read, because the launcher's messages differ
  for the two (spec.md §7).
- **Tests:** `TestFileAtAsksForTheRefItWasGiven`, `TestFileAtRefusesADirectoryAndASymlink`,
  `TestFileAtDistinguishesMissingFromRefused`, `TestConfigReadPermissionsHoldNoWriteVerb` (walks the
  map the way the existing permission tests walk theirs, asserting no `:write` verb and no
  `administration`, `secrets`, `workflows`, `packages` or `deployments` scope).
- **Command:** `GOFLAGS=-p=2 go test ./internal/gitprov/github -run 'FileAt|ConfigReadPermissions' -count=1`.
- **Red step:** `TestConfigReadPermissionsHoldNoWriteVerb` is red against a set that does not exist
  yet, and the first implementation that reaches for `RunTokenPermissions` keeps it red.
- **Commit:** `github: read one file at a ref with a read-only installation token`.

## A2. The narrowing that needs no credential (D905, D908, D909, D912)

- **Files:** `internal/cli/project_job.go`, `internal/cli/project_job_test.go`,
  `internal/cli/cloud.go` (the provider without a checkout), `internal/cli/cloud_test.go`.
- **Interfaces:** in `launchProjectJobFor`, the `co == nil` branch becomes an ordered decision rather
  than a `return nil, nil` or a refusal: the provider gate first (`infra.SharedJobProvider`, read
  through a helper that does not dereference a nil `co` — reconciliation F3), then the ref's
  document from the checkout's objects where a checkout exists (`checkoutFugaroYAMLAtRef`, promoted
  from the auth axis to the whole document, parsed with the same lenient resolution the working tree
  goes through), then the refusal. Two messages, not one: `outsideXCheckout` (no checkout anywhere)
  and `cannotReadConfigAtRef` (the ref's file could not be read, with the cause and both ways out).
- **Tests:** `TestNoCheckoutBitbucketKeepsItsPerRepositoryJob` (the red step's case),
  `TestNoCheckoutProviderHelperDoesNotPanic`,
  `TestCheckoutWithoutTheFileReadsTheRefsObjects`,
  `TestNoCheckoutGitHubRefusalNamesTheCheckoutAndTheRef`,
  `TestRefusalMessagesAreNotInterchanged` (the two causes, two texts).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ProjectJob|NoCheckout|RefConfigAtRef' -count=1`.
- **Red step:** `TestNoCheckoutBitbucketKeepsItsPerRepositoryJob` — a launch of a Bitbucket
  repository with no checkout, on a project whose policy publishes a profile, must keep the
  per-repository job. It is red against #1477's tree, where the refusal fires before the provider is
  ever consulted.
- **Commit:** `cli: a launch with no checkout of a Bitbucket repository keeps its own job`.

## A3. The launcher reads the ref's document through the forge (D901, D902, D903, D904, D905, D906). **SEC**

- **Files:** `internal/cli/project_job.go`, `internal/cli/githubapp.go` (the shared key read and the
  notice), `internal/cli/project_job_test.go`, `internal/cli/run_test.go` (the launch path's own
  coverage).
- **Interfaces:** a CLI seam `readRepoConfigAtRef(ctx, env, repo, ref) ([]byte, error)`, replaceable
  in tests, that resolves the App ID (`repos.<repo>.github_app_id`) and the key secret exactly as
  `--check-github-app` resolves them (project-wide or per repository), reads the PEM with the
  operator's own credentials after a one-line notice, mints a `ConfigReadTokenPermissions` token and
  calls `github.FileAt`. It is called **only** from A2's refusal branch: the read happens where the
  code was about to refuse, for a provider a shared job serves, outside a coding agent's session
  (`refuseKeyReadInAgent`, with the launch named where the flag is named), memoized per (repo, ref)
  in the command's own scope and written nowhere. The parsed document then walks the *existing* gates
  unchanged: `w.Checkout`, `w.JobProfileOrDefault()`, `ProfileFor`, the provider gate,
  `profileAdmissionRefusal`, `projectJobAgentAuth`, `projectJobAccountFor`.
- **Tests:** `TestLaunchWithoutCheckoutReadsTheRefThroughTheForge`,
  `TestLaunchWithACheckoutNeverReadsTheForge` (the read is not made where the tree answers),
  `TestLaunchWithoutCheckoutInAnAgentSessionIsRefusedBeforeAnyRead`,
  `TestLaunchWithoutCheckoutWithoutThePermissionNamesBothWaysOut`,
  `TestReadRepoConfigAtRefIsMemoizedPerRepoAndRef`,
  `TestForgeReadDoesNotTouchTheWorkingTree` (nothing written, no fetch — the seam's own contract),
  `TestForgeReadSelectsTheProfileTheRefNames` (a repository whose ref names a profile other than
  `standard` lands on that profile's job, not on `standard`'s).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ProjectJob|Forge|NoCheckout' -count=1`.
- **Red step:** `TestLaunchWithoutCheckoutReadsTheRefThroughTheForge` — a launch of a GitHub
  repository from a directory that holds no checkout of it, on a project whose policy publishes two
  profiles, must reach the shared job of the profile the ref's `fugaro.yaml` names. Red on #1477's
  tree, where it is refused and no read is attempted.
- **Commit:** `cli: a launch without the repository's checkout reads its fugaro.yaml at the ref`.

## A4. The job the run being continued recorded (D907)

- **Files:** `internal/cli/project_job.go`, `internal/cli/run.go`, `internal/cli/followup.go`,
  `internal/cli/project_job_test.go`.
- **Interfaces:** the backfill `existingLaunch` already performs (`launch.json`'s `job`, else
  `result.json`'s execution through `backend.ParseExecution`) is factored into one reader with two
  callers — the "already launched" check it serves today, and the decision here — so the two cannot
  disagree about what a run's job was. The decision consults it first for a launch that continues a
  run: the same `spec.RunID` for `--retry`, and `spec.PreviousRun`'s record for a follow-up, opened
  in the same slug's store. No new field is written anywhere.
- **Tests:** `TestRetryFromOutsideTheCheckoutReusesTheRecordedJob`,
  `TestFollowUpFromOutsideTheCheckoutReusesThePreviousRunsJob`,
  `TestRetryOfARunThatNeverLaunchedFallsThroughToTheOtherSources`,
  `TestRecordedJobIsNotTrustedForItsProfile` (the run is placed on the recorded job and the runner's
  own checks remain the ones that judge it).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Retry|ProjectJob|FollowUp' -count=1`.
- **Red step:** `TestRetryFromOutsideTheCheckoutReusesTheRecordedJob` — a `--retry` of a run that
  launched on a shared job, from a directory with no checkout of the repository, must land on the
  job its `launch.json` records. Red on #1477's tree, where it is refused.
- **Commit:** `cli: a retry and a follow-up reuse the job the run recorded`.

## B1. The documents (D913)

- **Files:** `docs/checkout.md`, `docs/git-providers.md`, `docs/run-without-checkout/README.md`,
  `internal/cli/docs_checkout_test.go` and any other `internal/cli/docs_*_test.go` that pins a
  sentence the change touches (found by running the Docs filter, not by guessing).
- **Interfaces:** `docs/checkout.md` loses the "Known gaps" bullet and gains the rule in its own
  words: a launch with no checkout of the repository reads the ref's `fugaro.yaml` through the forge
  for a provider a shared job serves, reuses the recorded job for a run being continued, and refuses
  otherwise with the two ways out. `docs/git-providers.md`'s "the CLI holds no provider credential"
  gains the one exception in the same breath as the two that already exist (`--check-github-app`,
  `project sync --github-app-key`): the CLI holds no provider credential **of its own**; it reads
  the App's key for one file, in memory, refused under an agent's marker.
- **Tests:** the pinned sentences stay pinned — change the wording in both places, keep the fact.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Red step:** `Docs` is red the moment a pinned sentence moves without its test, which is the red
  step: run it after editing the prose, before editing the pins.
- **Commit:** `docs: a launch without the repository's checkout, in the two pages that describe it`.

## B2. The release note (D913)

- **Files:** `docs/releases/vX.Y.Z.md` (new), `website/data/release-summaries.yaml`.
- **Interfaces:** the note states the behaviour change for an operator: launches of a repository from
  outside its checkout work again, the agent-session refusal is the same one, and `### For operators`
  is "run `fugaro update`" and nothing else — this design adds no secret, no IAM binding, no
  Terraform and no operator step (D902).
- **Tests:** `scripts/release.sh`'s own checks; the summary file's shape is pinned by
  `website/test/*.mjs`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`, then `npm run check` in
  `website/` if the summary file changed.
- **Red step:** none — a note has no behaviour to make red; it is written from the tasks' own
  commits.
- **Commit:** `docs(releases): the X.Y.Z notes`.

## C1. The independent review (D914)

- **Files:** none — this is the gate, not a change.
- **Interfaces:** the review reads A1 and A3 against the threat model in spec.md §12 and answers one
  question per item: the token's permission set, the key's lifetime in the launcher's process, the
  agent-session refusal, the ref the document is read at, the absence of a fallback, and the two
  message texts. A finding that needs a new behaviour is a new decision in the D901 block, not a
  fix inside the review round.
- **Tests:** the tasks' own suites, run on the reviewed commit.
- **Command:** `fugaro verify test` on the final commit of the reviewed range.
- **Red step:** none.
- **Commit:** none.

## D1. What this plan deliberately does not do

- **The console needs no change** (D906): the five-field argv schema, `privateWorkingDirectory` and
  the launch screen are untouched, and the console renders the CLI's new refusals verbatim. The one
  thing to check, not to build: whether any console test pins a launch-failure string
  (`npm run test:unit -- --maxWorkers=2` in `console/`). If one does, it is B1's kind of change —
  the sentence moves in both places — and not a contract change.
- **`docs/project-layer.md` is not touched** (reconciliation F11): line 25's "keeps the per-repository
  job, as it does today" is made stale by #1477, and that file is owned by open PR #1478.
- **No flag, so no skill change**: `plugin/skills` names every CLI flag and is checked against the
  CLI by `plugin/`'s tests; D901 adds none, so nothing there moves. If the owner answers D901 the
  other way, that reversal is what pays for a skill edit, a CLI-reference entry and a console
  release.
- **No new dependency, no `go.mod` change, no Terraform, no `docs/pending-workflows/` file.**

## The shared-files table

Parallel tasks may not touch the same file. These are the files more than one task owns, and the
order they are edited in:

| File | Tasks | Rule |
| --- | --- | --- |
| `internal/cli/project_job.go` | A2, A3, A4 | Serialize: A2's decision shape first, then A3's read inside its refusal branch, then A4's recorded job ahead of both |
| `internal/cli/project_job_test.go` | A2, A3, A4 | Serialize with the file above; each task's tests are appended, none rewritten |
| `internal/cli/run.go` | A4 | A4 only, and reviewed closely: this file holds the launch's ordering, and the reader A4 shares with `existingLaunch` must not move the claim |
| `internal/cli/cloud.go` | A2 | A2 only |
| `docs/run-without-checkout/README.md` | B1 | The Status line moves when A3 lands, once |
| `internal/gitprov/github/apptoken.go` | A1 | A1 only (the new permission set) |

Two tasks can run in parallel with nothing shared: **A1** (the reader) and **A4** (the recorded job),
and then **A2** before **A3**. **B1** and **B2** wait for the code they describe.

## The recommended slot

**A2 belongs in #1477 itself, before it lands; A3 follows in the next CLI release; the console has
no slot here.** (D901, D914.)

- #1477 as it stands makes `fugaro run --repo X` refuse from outside `X`'s checkout, which is how
  **every** console launch is made (`console/src/main/launch.ts`), so a released #1477 leaves the GUI
  unable to launch anything on an installation whose policy defines a profile (reconciliation F9).
  A2 is a few lines, needs no credential and no decision, and restores the Bitbucket half of the
  console immediately.
- **The alternative — land #1477 as it is and fix it in the following release — costs a whole
  release cycle in which the console's only launch path is refused on every migrated installation,**
  and it spends the operator's trust twice: once for the refusal, once for a second release that
  says the first was wrong.
- A3 is the SEC-flagged task (D914) and should not be smuggled into a fix: it gets its own pull
  request, its own independent review and its own release note.
- **If #1477 has already shipped when this design is answered**, A2 and A3 land together, in the
  first CLI release after it — A2 first in the same branch, so the release that lifts the GitHub
  refusal is never a release that left Bitbucket refused for nothing.
- No console release is required by any of this (D906), and no operator step ships with it (D902):
  `### For operators` is "run `fugaro update`".
