# Launching a run for a repository without its checkout: implementation plan

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task approved.**

The decisions each task answers are named in its heading; they are **D901–D915** in
[reconciliation.md §3](reconciliation.md), answered by the owner on 2026-11-04, each with the reason for
the answer beside it — **every one but D915**, the one question left open, which the code itself raised
(reconciliation F6). **D902's answer is not the recommendation** — the read is made through the
installation's credentials service, as the operator themselves, so the launcher holds no App key and
reads no environment variable — and A0, A1 and A3 below are written for that answer.

**A0 comes first, and A1 and A3 depend on it.** The answer names a credential class that exists; the
credential's **permission** does not cover the read. The route mints with
`github.CoordinatorTokenPermissions` — `pull requests` and `metadata`, read, and no `contents` — so
`GET /repos/{owner}/{repo}/contents/fugaro.yaml` is refused with the token the service hands out today,
and the refusal is correct: that constant is the coordinator's own reach, it reads no file, and two
tests pin it so it cannot quietly grow one (reconciliation F6). A0 is that change, made on the service's
own mint; it is the only **credentials-service change by rule** in this plan, and nothing below its line
can be demonstrated end to end before it lands.

**The tasks are written against `main` as it is now**, with **#1477 merged** (reconciliation F1): the
refusal this design answers is in the tree, so every red step below is red against `main` itself.

**Slices.** **A** is the design. **B** is the documentation and the release. **C** is the one gate
that cannot be skipped. **D** is the work this design deliberately does not do, with the reason.

Every task is at most 12 files, names its own red step, and is committed on its own. The commands
are the ones the repository's own rules require: targeted `-run` filters, `GOFLAGS=-p=2`, never a
whole `internal/cli` or `internal/runner` run.

## A0. The person read mint has to reach a file (D915). **SEC**

- **Files:** `internal/gitprov/github/apptoken.go`, `internal/gitprov/github/apptoken_test.go`,
  `internal/credsvc/githubread.go`, `internal/credsvc/githubread_test.go`, and, if the route option is
  taken, the new route's own files beside them. `testdata/coordinator_token.json` is **not** touched:
  it holds the coordinator's mint body, and A0 must leave that body alone.
- **Interfaces:** one of two, and the choice is the owner's (D915):
  1. **A PERSON-class permission set of its own** — `personReadTokenPermissions`, unexported beside the
     five sets that exist and exported through an accessor as every one of them is, carrying
     `contents: read`, `pull_requests: read` and `metadata: read` — asked for by `githubRead` in place
     of `github.CoordinatorTokenPermissions()`. It keeps the two the coordinator's set has because the
     route is one route: the same mint is what a person's `coordinator show` reads a pull request with,
     so the set has to reach that read as well as this one, and it is a set of its own rather than that
     one widened. **It must never widen `CoordinatorTokenPermissions`:**
     the coordinator mints with that same constant, it opens no file, and
     `TestAppTokenCoordinatorPermissionsHoldNoWrite` pins the set as an equality against
     `testdata/coordinator_token.json`. The mint asks for what the App holds; a set the App is not
     granted fails at GitHub, which is a refusal the operator reads, not a silent narrowing.
  2. **A route of its own** for the file read, with its own set, its own client and its own audit
     purpose: a second person route, a second client and a second set of refusals to keep in step with
     the first. The design does not recommend it (D915).
- **Tests:** `TestPersonReadPermissionsCoverAFileRead` (the set asked for carries `contents: read` and
  no write, asserted as an equality the way the other sets' are), plus the two pins that must **not**
  move: `TestAppTokenCoordinatorPermissionsHoldNoWrite` and its fixture body on one side, and
  `TestGitHubReadAsksForNoWrite` in `githubread_test.go` on the other — that test's `want` becomes the
  new accessor, its "no write" loop and its read of the mint's real request body stay exactly as they
  are. A read mint that asks with the new set and still hands a person a token is the same test's
  subject.
- **Command:** `GOFLAGS=-p=2 go test ./internal/gitprov/github ./internal/credsvc -run 'Permissions|GitHubRead' -count=1`.
- **Red step:** `TestPersonReadPermissionsCoverAFileRead` is red against `main`, where no set carrying
  `contents: read` exists and `githubRead` mints with the coordinator's. The end-to-end half — a person's
  mint actually reading `fugaro.yaml` — cannot be made green by anything in A1 or A3, which is why both
  depend on this task.
- **Commit:** `credsvc: the person read mint asks for the file read it is for`.

## A1. The file at a ref, read with the person's read-only token (D910, D911). **SEC**

- **Files:** `internal/gitprov/github/fileread.go` (new), `internal/gitprov/github/fileread_test.go`
  (new), `internal/gitprov/github/testdata/fileread/*.json` (new: an `httpfixture` replay of one file
  read and of one missing file).
- **Interfaces:** `FileAt(ctx context.Context, o Options, ts gitprov.TokenSource, repo, ref, path string) ([]byte, error)`,
  built over what the package already has rather than over a second HTTP client (reconciliation F6): it
  assembles `Options` for `repo` with `ServiceHeldKey: true`, installs `ts` (`UseServiceTokens` accepts
  any `gitprov.TokenSource`, and the person's read mint `ServiceReadTokens` is one), and calls
  `Provider.Contents(ctx, path, ref)` — `gitprov.ContentsReader`, which refuses a directory, a symlink
  entry, a submodule, a non-base64 body and anything over `gitprov.MaxContentsBytes`. What A1 adds is
  the one thing the launcher's messages need and `Contents` does not carry: a **typed not-found**, so a
  file the repository does not hold at the ref is a different error from a read that was refused
  (spec.md §7's rows differ for the two). **No permission is asked for, and there is no parameter to
  ask with:** what the token reaches is the set the service mints (A0).
- **Tests:** `TestFileAtAsksForTheRefItWasGiven`, `TestFileAtDistinguishesMissingFromRefused`,
  `TestFileAtUsesTheTokenSourceItIsGiven` (a fake `gitprov.TokenSource` observes one mint and the
  request carries its token). The kind, encoding and size refusals are `Contents`'s own and stay in
  `contents_test.go`; A1 does not restate them.
- **Command:** `GOFLAGS=-p=2 go test ./internal/gitprov/github -run 'FileAt' -count=1`.
- **Red step:** `TestFileAtAsksForTheRefItWasGiven` is red against a package with no such function, and
  a first implementation that reads the working tree or the default branch keeps it red.
- **Commit:** `github: read one file at a ref through the person's read mint`.

## A2. The narrowing that needs no credential (D905, D908, D909, D912)

- **Files:** `internal/cli/project_job.go`, `internal/cli/project_job_test.go`,
  `internal/cli/cloud.go` (the provider without a checkout), `internal/cli/cloud_test.go`.
- **Interfaces:** in `launchProjectJobFor`, the `co == nil` branch becomes an ordered decision rather
  than a `return nil, nil` or a bare refusal: the provider gate first (`infra.SharedJobProvider`, read
  through a helper that does not dereference a nil `co` — reconciliation F3), then the ref's document
  from the checkout's objects where a checkout exists (`checkoutFugaroYAMLAtRef`, promoted from the
  auth axis to the whole document, parsed with the same lenient resolution the working tree goes
  through), then the refusal. Two messages, not one: `outsideXCheckout` (no checkout anywhere) and
  `cannotReadConfigAtRef` (the ref's file could not be read, with the cause and the way out).
- **Tests:** `TestNoCheckoutBitbucketKeepsItsPerRepositoryJob` (the red step's case),
  `TestNoCheckoutProviderHelperDoesNotPanic`,
  `TestCheckoutWithoutTheFileReadsTheRefsObjects`,
  `TestNoCheckoutGitHubRefusalNamesTheCheckoutAndTheRef`,
  `TestRefusalMessagesAreNotInterchanged` (the two causes, two texts).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ProjectJob|NoCheckout|RefConfigAtRef' -count=1`.
- **Red step:** `TestNoCheckoutBitbucketKeepsItsPerRepositoryJob` — a launch of a Bitbucket
  repository with no checkout, on a project whose policy publishes a profile, must keep the
  per-repository job. It is red against `main`, where the refusal fires before the provider is ever
  consulted.
- **Commit:** `cli: a launch with no checkout of a Bitbucket repository keeps its own job`.

## A3. The launcher reads the ref's document through the credentials service (D901, D902, D903, D904, D905, D906). **SEC**

- **Files:** `internal/cli/project_job.go`, `internal/cli/personid.go` (new: the launcher's own Google
  ID token), `internal/cli/personid_test.go` (new), `internal/cli/project_job_test.go`,
  `internal/cli/run_test.go` (the launch path's own coverage).
- **Interfaces:** a CLI seam `readRepoConfigAtRef(ctx, env, repo, ref) ([]byte, error)`, replaceable in
  tests, that reads the installation's published `credentials_service.url` (`internal/localcfg`) and
  the App ID (`repos.<repo>.github_app_id`), builds
  `(&github.ServiceReadTokens{URL: ..., IDToken: personIDToken}).Repo(spec.Repo, appID)` and calls
  `github.FileAt` with it (A1's reader, over the `Contents` the package already has). **It depends on
  A0**: until the service's mint asks with a set that reaches a file, every read on this path is refused
  at GitHub and this task's end-to-end test cannot be made green by anything in this file. **It holds no
  App key and reads no environment variable** (D902's answer):
  `personid.go` is the launcher's own copy of `ee/coordinator/cli`'s `coordinatorPersonIdentity` — a
  package variable running `gcloud auth print-identity-token`, whose argv is the plain mint for a person
  and the audience-scoped one for a service account — and the core cannot import that one
  (`internal/eeboundary/boundary_test.go`, reconciliation F13). It is called **only** from A2's
  refusal branch: the read happens where the code was about to refuse, for a provider a shared job
  serves, and outside a coding agent's session (`refuseKeyReadInAgent`, with the launch named where the
  flag is named — the shape `internal/cli/doctor_brokering.go` already uses for the credentials
  service's bundle). Memoized per (repo, ref) in the command's own scope and written nowhere. The
  parsed document then walks the *existing* gates unchanged: `w.Checkout`, `w.JobProfileOrDefault()`,
  `ProfileFor`, the provider gate, `profileAdmissionRefusal`, `projectJobAgentAuth`, `projectJobAccountFor`.
- **Tests:** `TestLaunchWithoutCheckoutReadsTheRefThroughTheService`,
  `TestLaunchWithACheckoutNeverReadsTheService` (the read is not made where the tree answers),
  `TestLaunchWithoutCheckoutInAnAgentSessionIsRefusedBeforeAnyRead`,
  `TestLaunchWithoutCheckoutWithoutACredentialsServiceNamesTheCheckout`,
  `TestLaunchWithoutCheckoutWhereTheMintCannotReadAFileNamesTheRedeploy` (spec.md §7's row: the service
  answers, the mint reaches no file, and the message names the service and the redeploy rather than the
  token),
  `TestLaunchWithoutCheckoutPresentsTheOperatorsOwnIdentity` (the identity helper is asked for the
  service's URL, and the mint carries the config's App ID),
  `TestReadRepoConfigAtRefIsMemoizedPerRepoAndRef`,
  `TestServiceReadDoesNotTouchTheWorkingTree` (nothing written, no fetch — the seam's own contract),
  `TestServiceReadSelectsTheProfileTheRefNames` (a repository whose ref names a profile other than
  `standard` lands on that profile's job, not on `standard`'s).
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ProjectJob|CredentialsService|NoCheckout|PersonID' -count=1`.
- **Red step:** `TestLaunchWithoutCheckoutReadsTheRefThroughTheService` — a launch of a GitHub
  repository from a directory that holds no checkout of it, on a project whose policy publishes two
  profiles and whose installation has a credentials service, must reach the shared job of the profile
  the ref's `fugaro.yaml` names. Red against `main`, where it is refused and no read is attempted.
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
  job its `launch.json` records. Red against `main`, where it is refused.
- **Commit:** `cli: a retry and a follow-up reuse the job the run recorded`.

## B1. The documents (D913)

- **Files:** `docs/checkout.md`, `docs/git-providers.md`, `docs/run-without-checkout/README.md`,
  `internal/cli/docs_checkout_test.go` and any other `internal/cli/docs_*_test.go` that pins a
  sentence the change touches (found by running the Docs filter, not by guessing).
- **Interfaces:** `docs/checkout.md` loses the "Known gaps" bullet and gains the rule in its own
  words: a launch with no checkout of the repository reads the ref's `fugaro.yaml` through the
  installation's credentials service as the operator, reuses the recorded job for a run being
  continued, proceeds on the per-repository job for a provider no shared job serves, and refuses
  otherwise naming the cause and the checkout. Its "Known gaps" remedy — falling back to the deployed
  job's own `FUGARO_CHECKOUT` or its image being `base_images.base` — decides only clone-or-baked and
  cannot name the profile, so the replacement says why the read is what closes the gap
  (reconciliation F11). `docs/git-providers.md`'s "the CLI holds no provider credential" gains the one
  exception in the same breath as the two that already exist (`--check-github-app`,
  `project sync --github-app-key`), stated as it now is: the CLI holds no provider credential **of its
  own** and no App key; a launch outside a checkout on an installation with a credentials service holds
  one read-only, repository-scoped token the service minted for the operator, for one file.
  `docs/project-layer.md` is **not** touched (reconciliation F11): its stale sentence belongs to that
  document's own owner.
- **Tests:** the pinned sentences stay pinned — change the wording in both places, keep the fact.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Red step:** `Docs` is red the moment a pinned sentence moves without its test, which is the red
  step: run it after editing the prose, before editing the pins.
- **Commit:** `docs: a launch without the repository's checkout, in the two pages that describe it`.

## B2. The release note (D913)

- **Files:** the slot's notes file under `docs/releases/` (new), `website/data/release-summaries.yaml`.
- **Interfaces:** the note states the behaviour change for an operator: launches of a repository from
  outside its checkout work again where the installation has a credentials service and has redeployed it,
  the agent-session refusal is the same one, and where the installation has none the launch refuses
  exactly as it did before — a limit, stated plainly, with the checkout as the way out. Its
  **`### For operators`** section is "run `fugaro update`", and it says what that update carries: this
  release changes the credentials service's own mint, so **the credentials service must be redeployed
  to serve the new permission set** — `fugaro update` redeploys it — and until it has, a launch outside
  a checkout still reads nothing and keeps the refusal it has today.
- **Tests:** `scripts/release.sh`'s own checks; the summary file's shape is pinned by
  `website/test/*.mjs`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`, then `npm run check` in
  `website/` if the summary file changed.
- **Red step:** none — a note has no behaviour to make red; it is written from the tasks' own
  commits.
- **Commit:** `docs(releases): the slot's notes`.

## C1. The independent review (D914)

- **Files:** none — this is the gate, not a change.
- **Interfaces:** the review reads A0, A1 and A3 against the threat model in spec.md §12 and answers one
  question per item: the permission set the mint asks with and the two pins it must not move, the person
  the service verifies, the launcher's freedom from the App key and from any environment variable, the
  agent-session refusal, the ref the document is read at, the absence of a fallback, and the two message
  texts. It is a **credentials-service change by rule** (`.claude/rules/credentials-service.md`:
  independent review before merging, one audit line per refusal, no credential value in any line), and
  A0 is a change to `internal/credsvc` itself, not only a caller of it. A finding that needs a new
  behaviour is a new decision in the D901 block, not a fix inside the review round.
- **Tests:** the tasks' own suites, run on the reviewed commit.
- **Command:** `fugaro verify test` on the final commit of the reviewed range.
- **Red step:** none.
- **Commit:** none.

## D1. What this plan deliberately does not do

- **It does not replace the copy of the person identity in `ee/`** (reconciliation F13). A3 writes the
  launcher's own helper because the core may not import `ee/`; repaying the duplication the other way —
  a core package `ee/coordinator/cli` adopts — is a task for the owner of the coordinator, not for this
  design, and doing it here would edit a business-licensed file to serve a core feature.
- **The console needs no change** (D906): the five-field argv schema, `privateWorkingDirectory` and
  the launch screen are untouched, and the console renders the CLI's new refusals verbatim. The one
  thing to check, not to build: whether any console test pins a launch-failure string
  (`npm run test:unit -- --maxWorkers=2` in `console/`). If one does, it is B1's kind of change —
  the sentence moves in both places — and not a contract change.
- **`docs/project-layer.md` is not touched** (reconciliation F11): its "keeps the per-repository job,
  as it does today" is made false by the merged refusal, and that file is not this design's to fix.
  Reported here so the owner can route it; the plan's tasks do not edit it.
- **No flag, so no skill change**: `plugin/skills` names every CLI flag and is checked against the
  CLI by `plugin/`'s tests; D901 adds none, so nothing there moves.
- **No new dependency, no `go.mod` change, no Terraform, no `docs/pending-workflows/` file, no new
  secret and no new IAM binding.** It is not "no operator step", though: A0 changes the credentials
  service's own mint, so the service has to be redeployed for an installation to serve the read, and
  B2's note says so.

## The shared-files table

Parallel tasks may not touch the same file. These are the files more than one task owns, and the
order they are edited in:

| File | Tasks | Rule |
| --- | --- | --- |
| `internal/cli/project_job.go` | A2, A3, A4 | Serialize: A2's decision shape first, then A3's read inside its refusal branch, then A4's recorded job ahead of both |
| `internal/cli/project_job_test.go` | A2, A3, A4 | Serialize with the file above; each task's tests are appended, none rewritten |
| `internal/cli/run.go` | A4 | A4 only, and reviewed closely: this file holds the launch's ordering, and the reader A4 shares with `existingLaunch` must not move the claim |
| `internal/cli/cloud.go` | A2 | A2 only |
| `internal/cli/personid.go` | A3 | A3 only (new) |
| `internal/gitprov/github/apptoken.go` | A0 | A0 only: a set beside the five, and `CoordinatorTokenPermissions` left exactly as it is |
| `internal/gitprov/github/apptoken_test.go` | A0 | A0 only: the new set's own test, and its neighbour's pin moved nowhere |
| `internal/credsvc/githubread.go` | A0 | A0 only: the constant `githubRead` asks with — and the route's own doc comment, which states the old set in prose |
| `internal/credsvc/githubread_test.go` | A0 | A0 only: `TestGitHubReadAsksForNoWrite`'s `want` |
| `internal/gitprov/github/contents.go` | none | Read by A1 and **not edited**: A1 wraps `Contents`, it does not widen it |
| `docs/run-without-checkout/README.md` | B1 | The Status line moves when A3 lands, once |
| `internal/gitprov/github/fileread.go` | A1 | A1 only (new) |

**A0 goes first and alone** — it is the service's own mint, and everything else that reads a file waits
on it. Then **A1** (the reader) and **A4** (the recorded job) can run in parallel with nothing shared,
and **A2** before **A3**. **B1** and **B2** wait for the code they describe.

## The slot

**A2, A4, the documents for the narrowing and their notes are one CLI release — 0.19.0, the first after
the one that carried the person-class read. A0 and A3 are the next — 0.20.0.** (D901, D914, D915.)

- **0.15.0, 0.16.0 and 0.17.0 have shipped** and 0.18.0 carried the person-class read, so the slot this
  design was first written against no longer exists; what it must not do is ride in a release already
  spoken for.
- **A2 is the part that needs no credential and no decision**, and it is what restores the Bitbucket
  half of the console: every console launch is made from outside every checkout
  (`console/src/main/launch.ts`), so a release that keeps refusing them leaves the GUI unable to launch
  anything on an installation whose policy defines a profile (reconciliation F9).
- **A3 is the SEC-flagged task** (D914) and the one D902's answer shapes: it gets its own pull request,
  its own independent review under the credentials-service rule, and its own place in the notes. **A0
  is the same release and the same review**, because it is the service's own change: a release that
  shipped A0 without A3 would widen a mint nothing reads with, and one that shipped A3 without A0 would
  ship a read that is refused — so the two ride together in **0.20.0**, and 0.19.0 must not wait for
  them: the Bitbucket narrowing is free and independent.
- **What the answer costs the operator, stated in the notes rather than hidden:** on an installation
  that has not opted into the credentials service there is nothing to read, and those launches keep the
  refusal they have today, with the checkout as the way out (spec.md §2, §7); on one that has opted in
  but has not redeployed the service since 0.20.0, the mint reaches no file and those launches keep it
  too (spec.md §7). No console release is required by any of this (D906), and the one operator step is
  the one every release already asks for: `fugaro update`, whose `### For operators` says it redeploys
  the credentials service (B2).
