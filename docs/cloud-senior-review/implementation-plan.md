# The independent senior review as a cloud job: implementation plan

**Nothing here is approved.** This plan exists so that, if the owner answers
[reconciliation.md](reconciliation.md) §4, the work is ordered and reviewable. Every task carries its
own gate: a task starts only when the decisions it names are answered.

**Written to:** [spec.md](spec.md) and [reconciliation.md](reconciliation.md). **Where they
disagree, the reconciliation wins.** The findings (**F1–F27**), the dependencies (**E1–E6**) and the
decisions (**D880–D893** — D880–D891 answered, D891 by the owner with option (a), and **D892**
(how a verdict's provenance is established before a coordinator acts on it) and **D893** (how the
read-only token reaches the job) **open questions for the owner**) are that document's, and each task
below names the ones it needs.

**Rules every task obeys** (this repository's own, not this plan's invention):

- a red step first: the named test fails for the reason the task exists, and the failure is quoted in
  the pull request;
- targeted tests only, `GOFLAGS=-p=2`, `-count=1`, never a whole package that takes half an hour;
- every new `fugaro.yaml`, task, recipe or wire key gets its JSON Schema in `schemas/` **and** a
  `…Since` gate in `internal/cli/recipes_skew.go` (E6), so an older job image refuses the launch
  rather than ignoring the field;
- hermetic tests only (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`), no live call, no real
  token, no real job;
- no secret-shaped literal anywhere, in a test or a doc.

---

## 1. The order, and what forces it

**The first slice is the smallest thing that lets a cloud run use a named OAuth account end to end:
`fugaro accounts add` plus the reviewer job.** It is two dependencies that must land together — E1
(the account) and R1/R7/R2 (a **job that mounts it read-only**, and the command that launches it) —
because either alone buys nothing the owner asked for: E1 alone makes `fugaro run --token` work (which
is PR #1470's own subject), and a review job with no named account is a review on the installation's
default token, which the coder's job already had.

```
E1 (accounts add, PR #1470)  ─┐
E2 (yamledit, PR #1467)      ─┤
E3 (publish stages)          ─┴─▶ R1 (read-only token) ─▶ R7 (the job variant, no App key) ─▶ R2 (the command)
                                                                                              ─▶ R3 (the artifact)
                                                                                                 ├─▶ R4 (mutation)
                                                                                                 └─▶ R5 (findings→brief)
                                                                                       R6 (gates, docs, release) last
```

**R1 before R7 before R2**, because a command that launches a job must be able to say what that job
may do, and the job it launches must exist: R1 names the scope, R7 builds the container that can hold
it without the App key, R2 starts it. **R3 before R5**, because the fix brief reads the artifact R3
defines. **R4 is independent of R5** and can ship after R3 in either order; **D891 is answered —
option (a) — so R4 is unblocked** and carries the machinery the answer names (a token-scrubbed
subprocess with egress denied, and Bash denied for the rest of the stage), **on one condition: the
answer assumes a fence nobody has measured, so U7's gate is R4's first step and the task stops for the
owner if the fence is not there** (see R4). **R6 carries the
gates and cannot be split**: a field that lands without its gate is a field an older image silently
ignores.

**R7 is numbered last and comes third, deliberately.** It was added by the fix round after the review
of head `0f5f9a55`, when the design learned the job it had proposed to launch on mounts the App key
(**F3**, **F14**): the numbers here are stable identifiers, and renumbering six tasks and every
reference to them would put a large, purely mechanical diff on top of a design correction. The order
above is the dependency order; the numbering is not a promise about it.

## 2. Shared files

Several files are wanted by more than one task, and three of them are files another work stream also
writes. Every task that touches a shared file takes it **alone**, and the others wait.

| File | Tasks | Note |
| --- | --- | --- |
| `internal/cli/run.go` | R2, R5 | the launch path. R2 adds a sibling command that reuses `claudeAccountFor`/`claudeAccountAllowed` and the job placement; R5 adds the findings brief. One task holds it at a time |
| `internal/runner/prompts.go` | R3, R4, R5 | the review prompt, its template sections, and the fix brief's findings posture |
| `internal/runner/reviewmode.go` | R2, R3 | the review bootstrap and the comment path R2 stops taking |
| `internal/runner/runner.go` | R3, R4, **only if** the hook is there — otherwise no task here | **reserved**: one writer across work streams. Every task prefers `reviewmode.go`; a task that needs the stage machine or the finalize path takes this file alone and says so (see the paragraph below) |
| `schemas/` (new files only) | R3, R4 | each schema's own new file; no shared edit |
| `internal/cli/recipes_skew.go` | R2, R3, R6, R7 | one writer (R6) for the gate constants; R2/R3/R7 declare what they need |
| `internal/infra/projectjobs.go` + `projectjobs_test.go` | R7 | the `ProjectJob` set the installation root deploys. R7 adds the review-only variant and its service account; nothing else here reads the set |
| `internal/config/config.go` + `claude_accounts.go` | R7, E1 | `config.ProjectJobAccounts` is where a profile's job set is computed, and **E1's `accounts add` converges the same set**. R7 must not land a second spelling: it takes these after E1, alone |
| `deploy/terraform/gcp/modules/project/{job,sa,iam,variables}.tf` | R7 | the module the variant is an instance of: an entry with `secret_env` carrying the account's token and no `github-app-key`, and a service account with no `secretAccessor` on that key |
| `deploy/terraform/gcp/roots/installation/{main.tf,variables.tf}` | R7 | the root that deploys `project_jobs` — the variant's entry travels here |
| `deploy/terraform/gcp/roots/installation/tests/project_job.tftest.hcl` | R7 | R7's positive test lives beside the retention it contradicts: the variant asserts **no** App key mount and **no** accessor, in the file that pins the opposite for the shared job |
| `internal/cli/doctor.go` + `doctor_secret_readers.go` | R7 | the job census and the secret-readers report read `config.ProjectJobAccounts`; a variant the census does not know about is a converged installation `fugaro doctor` calls wrong |

**`internal/runner/runner.go` is reserved, and R3/R4 may have to take it — the one rule that decides
it.** The 0.15.0 rulings make that file one writer across work streams, and an earlier draft of this
plan said nothing here needs it because neither task adds a stage to the run loop. **That is not safe
to assert, and the review of this branch's fix round is right to say so:** the artifact upload that R3
wants to ride (`internal/runner/artifact.go`'s `uploadArtifacts`) is *triggered* from the `mode:
artifact` flow (`collectArtifacts`, driven by a recipe's `outputs:` block) and a `mode: review` recipe
has no such block, and R4's mutation subprocess is the runner's own child rather than a stage. So the
rule each of those tasks follows: **prefer the file it already holds** — a review run's whole flow is
`runReviewMode` in `internal/runner/reviewmode.go`, which R2/R3 already share — and **if the hook it
needs turns out to be in `runner.go`, the task takes that file alone under the one-writer rule, adds
it to its own Files list and says so in its pull request**; it never edits it silently.

**Not touched by this plan, and named so it is not discovered later:** `internal/cli/root.go`,
`internal/cli/api.go`, `website/**`, `plugin/**`, `console/**`, `mobile/**`, `shared/**`, `ee/**`,
`docs/releases/**`, `docs/gcp-live-checklist.md`, `SECURITY.md` and `docs/design/v1.md`.

## 3. Tasks

### R1. A fourth installation-token set: `ReviewTokenPermissions`

**Decision:** D883. **Findings:** F2, F3, **F16**. **Verification:** `internal/gitprov`.

- **Files:** `internal/gitprov/github/apptoken.go`, `internal/gitprov/github/apptoken_test.go`.
  **2 files.**
- **Interfaces:** produces `reviewTokenPermissions` = `{contents: read, pull_requests: read, metadata:
  read}` and its exported spelling beside `RunTokenPermissions` and `BuildTokenPermissions` — the
  **fourth** set, as spec §2 and D883 say. **It builds no mint caller, and F16 is why:** every mint in
  this tree happens where the App key is mounted — `mintBuildToken` (`internal/cli/gitcred.go:92`)
  reads the key from its own environment inside the build that calls it, and `jobGitEnv`
  (`internal/cli/imagecheck.go:530`) does the same inside a check job — so **neither is a precedent
  for a launcher, which holds no key**. The review job is **given** a token, not able to mint one; the
  mint site (the credentials service or the coordinator) and the hand-in are **UNVERIFIED-2**, settled
  by **R7**, which is the task that owns them.
- **Tests:** `TestReviewTokenPermissionsAreReadOnly` (every value is `read`, the three keys are
  exactly those, and the map is a clone a caller cannot mutate) and `TestRunTokenPermissionsUnchanged`
  (F2's set is untouched: this task adds a set, it does not narrow one). **No mint test:** there is
  nothing here that mints.
- **Red step:** `TestReviewTokenPermissionsAreReadOnly` fails on an undefined identifier: no
  read-only set beside the run's and the build's exists. **SEC flag:** it is a permission set, and the
  test that matters most is the one asserting the **run's** set did not change.
- **Mutation to hold it:** change one value of the new set to `write` → the first test fails; change
  the run's set and the third fails.
- **Commit:** `gitprov: a read-only installation-token set for the review job`.

### R2. `fugaro review --pr N`: launch the review job

**Decisions:** D880, D881, D882, D888. **Findings:** F1, F4, F5, F14, F17. **Depends on:** E1, R1,
**R7**.

- **Files:** `internal/cli/review.go` (new), `internal/cli/review_test.go` (new),
  `internal/cli/run.go`, `internal/runner/reviewmode.go`, `internal/runner/reviewmode_test.go`,
  `internal/cli/recipes_skew.go`, `docs/gcp-live-checklist.md` (the Check 125 row **only if** the
  owner's D889 answer changes and the file is in scope — otherwise this task records it instead).
  **At most 7 files.**
- **Interfaces:** consumes E1's stored account and the job set (`gcp.ProjectJobName`), **R7's
  review-only job variant** (this task launches *that* entry, never the shared project job), R1's
  read-only set (the token **handed in**, F16 — the launcher mints nothing), and
  `claudeAccountFor`/`claudeAccountAllowed`. Produces **both verbs of the command
  family, in this task**: `fugaro review --pr N [--token NAME] [--json] [--mutate <file:test>]`, which
  writes a task (`mode: review`, the review PRs, the resolved account) and launches it on the
  account's review-only job — `--json` prints the **launch's** result, not the artifact, which does
  not exist until the job has run; and `fugaro review show <run> [--json]`, the reader that prints
  `review.json` and `review.md` (spec §5). R5 consumes `show`; it does not add it. Plus a
  `reviewJobSince` constant (E6). The review **job** posts no comment: `reviewmode.go`'s comment path
  is made conditional on a permission the caller states, so a job launched read-only **records** its
  verdict and does not call `Comment`.
- **Tests:** `TestReviewLaunchesOnTheNamedAccount` (the task names the account, the launch targets
  that account's **review-only job entry** and never the shared one), `TestReviewRefusesAnAccountTheRepoMayNotUse` (F4's rule, reused),
  `TestReviewRefusesAnExhaustedAccount` (D882: refused with the mark's reset time, not retried),
  `TestReviewOnAReadOnlyTokenPostsNoComment` (the job's record carries the verdict and the provider
  fake sees no `Comment`), `TestReviewRefusesWhileTheCodingRunIsGoing`,
  `TestReviewShowPrintsTheArtifactAndItsMarkdown`, and
  `TestRecipesSkewRefusesReviewJobOnAnOldImage` (E6).
- **Red step:** `TestReviewLaunchesOnTheNamedAccount` fails on an unknown subcommand: the command
  does not exist. **SEC flag:** this is the task that decides what the job may do; R1's set and this
  task's no-comment path are reviewed together.
- **Commit:** `review: launch the independent review as its own job on a named account`.

### R3. The review artifact: `review.json`, `review.md` and its schema

**Decisions:** D884, D886, D890. **Findings it acts on:** F11, F12, F13, **F23**. **Findings it is an
answer to, not work it does:** **F7** (the reviewer's model and `agent.max_output_tokens.reviewer`
exist today) is the observation D886 is sized against and the reason this task's schema has to fit a
cap that is already deployed — R3 consumes it, it does not address it. **Depends on:** R2.

- **Files:** `internal/runner/reviewartifact.go` (new), `internal/runner/reviewartifact_test.go`
  (new), `schemas/review.schema.json` (new), `schemas/schemas_test.go` (the corpus check),
  `internal/runner/prompts.go`, `internal/runner/verdict.go` (the reader of the **in-run** format,
  to assert it is **not** this one), `internal/runstore/runstore.go` (the record's own artifact list
  and `ReviewedPR`, unchanged in shape), `internal/runner/artifact.go` if a name rule needs it, and
  **`internal/runner/runner.go` only if** the upload hook proves to be in its finalize path rather
  than in `runReviewMode` (§2's one-writer rule; the task says so in its pull request if it takes it).
  **At most 9 files.**
- **Interfaces:** produces the versioned artifact of [spec.md](spec.md) §3 — `schema_version`,
  `review_run`, `pr`, `head_sha`, `base`, `reviewer{account, model, effort}`, `verdict` ∈ {`ship`,
  `fix first`, `reject`}, `overview`, `strengths[]`, `issues[]` (`severity`, `class`, `file`, `line`,
  `summary`, `fix`, `evidence`; the three anchored axes when D884's sub-question says carry them),
  `security`, `performance`, `test_coverage`, `mutation_checks[]` — written through the existing
  artifact stage/upload path, plus `review.md` rendered from it. **The stage's own request carries
  this schema, not `runner.VerdictSchema` (D890, F23)**: the review mode's stage call and its
  `ParseVerdict` are made conditional on the mode being a review *job*, so the coding run's `review`
  stage is byte-for-byte what it is today.
- **Tests:** `TestReviewArtifactRoundTrips`, `TestReviewArtifactSchemaMatchesStruct` (the
  schema-vs-Go check `schemas/schemas_test.go` already runs for every shape),
  `TestReviewArtifactRefusesAFourthVerdictWord`, `TestReviewArtifactTruncatedIsAFailure` (D886: a
  clipped result is a failed review, never a short `ship`),
  `TestReviewMarkdownMatchesTheJSON` (section for section), and two for F23:
  `TestReviewJobStageIsNotParsedAsAnInRunVerdict` (the review job's result is decoded by the artifact's
  reader and never reaches `ParseVerdict` — and, the other way round, the coding run's `review` stage
  still passes `VerdictSchema`) and `TestUnreadableReviewArtifactEndsTheRunInvalid` (a result with no
  artifact in it ends `StatusFailed`/`OutcomeNone` with the decoder's reason, and **no** synthesized
  `blocker` finding is stored).
- **Red step:** `TestReviewArtifactRoundTrips` fails on an unknown task field: the artifact has no
  writer and no schema; `TestReviewJobStageIsNotParsedAsAnInRunVerdict` fails because the stage's
  request still carries `VerdictSchema`. **SEC flag:** none of its own — but see R4, whose data this
  shape carries.
- **Commit:** `runner: the independent review's versioned verdict artifact`.

### R4. The mutation check

**Decision:** D885, and **D891 — answered by the owner with option (a), which this task is the
machinery for**. **Findings:** F13, F19, **F24**. **Depends on:** R3. **Unblocked:** the answer is
(a), so the task is neither dropped nor reduced, and it carries the posture in its own files.

- **First step — a gate, before any file is opened: U7.** D891(a) assumes egress can be denied for a
  child process inside the container, and **no network namespace or `unshare` in Cloud Run's gen2 has
  been checked**. So the task measures it first (a subprocess that tries to reach the network and
  fails, in a scratch execution), and **the answer decides the task**: if a child can be fenced, the
  rest of this task proceeds as written; **if it cannot, the task stops and the owner is asked again**
  — the fallbacks are D891's **(b)** (no mutation check in the reviewer at all) or **(c)** (the check
  in a separate execution), both of which are a different task, and the design does not quietly fall
  back to `denyWeb`. **Nothing in this task's Files is touched before that gate returns.**
- **Files:** `internal/runner/mutation.go` (new), `internal/runner/mutation_test.go` (new),
  `internal/runner/reviewartifact.go` (the `mutation_checks` field it fills),
  `internal/runner/prompts.go`, **`internal/agent/settings.go`** + `settings_test.go` (D891(a): the
  review stage's `DenyRules` gains **Bash** for the rest of the stage), `schemas/review.schema.json`,
  and **`internal/runner/runner.go` only if** the subprocess has to be reaped from a stage-machine
  hook rather than from `runReviewMode` (§2's one-writer rule; the task says so in its pull request if
  it takes it). **At most 8 files.**
- **Interfaces:** produces the bounded, opt-in check of [spec.md](spec.md) §2 — one mutant, one
  named test, a worktree that is reverted before the stage ends, and **no push ever**
  (`gitops`'s push functions are not reachable from the path). The artefact records what was applied,
  what was observed and that it was reverted. **The mutant is applied, run and reverted by the
  runner, not by the agent's Bash** — a subprocess of the runner's own with **`CLAUDE_CODE_OAUTH_TOKEN`
  and the account's secret scrubbed from its environment and egress denied** — and the agent's shell
  is denied for the rest of the stage, so the reviewer reads and reasons with no shell to exfiltrate
  through (D891(a), F24). **How egress is denied inside a Cloud Run container is U7**
  ([reconciliation.md](reconciliation.md) §6), and the task's very first step is the gate above:
  until it returns, D891(a) is an answered decision with an unmeasured mechanism, not a fact.
- **Tests:** `TestMutationAppliesRunsRevertsAndPushesNothing` (the git fake records no push),
  `TestMutationRefusesMoreThanOneMutant`, `TestMutationRevertsOnFailure` (a test that errors still
  leaves the worktree clean), `TestMutationAbsentUnlessAskedFor`,
  `TestMutationSubprocessCarriesNoTokenInItsEnvironment`, and
  `TestReviewStageDeniesBashForTheRestOfTheStage` (D891(a)'s own red test).
- **Red step:** `TestMutationAppliesRunsRevertsAndPushesNothing` fails on an unknown task field, and
  `TestReviewStageDeniesBashForTheRestOfTheStage` fails because `DenyRules` denies tools only.
  **SEC flag:** the task that runs repository code inside the reviewer, which is the one thing here
  that could look like "the reviewer edits the code" — the revert and the no-push assertions are what
  make §2's "never pushes" a fact, and the scrub, the egress rule and the Bash denial are what make
  D891(a) a fact rather than a sentence.
- **Commit:** `runner: the review job's mutation check, in its own worktree and never pushed`.

### R5. The findings reach the fix round's brief, by run id

**Decision:** D887. **Findings:** F20. **Depends on:** R2 (which ships `fugaro review show`) and R3.
**Holds `internal/cli/run.go` alone.**

- **Files:** `internal/cli/run.go`, `internal/cli/run_test.go`, `internal/runner/prompts.go`,
  `internal/runner/prompts_test.go`, `internal/cli/review.go` + `review_test.go` **only** for the
  reader's findings-rendering helper, if R2's `show` does not already expose one. **At most 6 files.**
- **Interfaces:** consumes R3's artifact by run id. Produces the brief's findings section: each issue
  as a bounded, quoted block (severity, class, `file:line`, summary, fix), the **review run named**,
  and a prompt sentence that they are findings to weigh and never instructions — the existing
  `internal/runner/prompts.go` posture, extended.
- **Tests:** `TestFixBriefCarriesTheFindingsAndNamesTheReviewRun`,
  `TestFindingsAreBoundedAndQuoted` (an over-long summary is clipped, and nothing reaches a shell),
  `TestFindingsFromAnUnreadableArtifactRefuseTheLaunch` (§6: an unreadable verdict is never a
  `ship`).
- **Red step:** `TestFixBriefCarriesTheFindingsAndNamesTheReviewRun` fails: there is no path from a
  stored artifact to a launched run's brief. **SEC flag:** yes — reviewer output derived from an
  untrusted diff becomes a coder's prompt, which is the injection chain of [spec.md](spec.md) §7.
- **Commit:** `run: a fix round's brief carries the independent review's findings by run id`.

### R6. The gates, the docs and the release notes

**Decisions:** D886, D888, D889. **Findings:** F9, F22. **Depends on:** R2, R3, R5.

- **Files:** `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`,
  `internal/cli/validate.go` and `validate_test.go` (the notes: the artifact output cap D886, and the
  subscription-stage bound F10), `docs/cloud-senior-review/spec.md` (status), `docs/recipes.md` (the
  `review-only` recipe's new sibling spelling), `docs/project-claude-accounts.md` (the account's job
  now has a reader), `docs/releases/v0.15.0.md` and `docs/releases/README.md` if the release notes
  are in scope. **At most 9 files.**
- **Interfaces:** produces the `…Since` constants the other tasks declared, the launch refusal naming
  the release whose image reads them, and the owner-facing text. `website/content/cli-reference.md`
  is **regenerated** by the task that ships the command (`ee/clidoc`), and if `website/**` is outside
  that task's scope the task says so in its own pull request (F22, E4).
- **Tests:** `TestRecipesSkewRefusesReviewJobOnAnOldImage`,
  `TestValidateNotesTheArtifactOutputCap`, and the existing docs tests for whichever documents this
  task edits (`GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`).
- **Red step:** `TestRecipesSkewRefusesReviewJobOnAnOldImage` fails until the constant and the
  refusal exist. **SEC flag:** no.
- **Release notes:** the `### For operators` section says which images must be refreshed before a
  launcher names a reviewer's account, in the release's own words and with no other step.
- **Commit:** `review: the version gates, the validation notes and the docs`.

### R7. The review-only job variant and its service account — no App key (SEC)

**Decisions:** D883, D888, **D893** — the owner's answer to how the minted read-only installation token
reaches the job, which **this task implements and does not re-decide** (**UNVERIFIED-2** is closed by
D893). **D891**'s stage posture is **R4**'s, not this task's, and this task only needs the variant to
mount the account's Claude token and no key.
**Findings:**
**F3**, **F14**, **F16**. **Depends on:** E1, R1. **Precedes R2** (see §1). **SEC flag: yes — this is
the task that makes the read-only scope a boundary instead of a comment on the launcher's intent.**

- **Files:** `internal/infra/projectjobs.go` + `projectjobs_test.go` (the `ProjectJob` entry, its
  `SecretEnv` and its service account), `internal/config/config.go` +
  `internal/config/claude_accounts.go` (`config.ProjectJobAccounts`, the one function the job set is
  computed with — **shared with E1's `accounts add`, taken after it, alone**),
  `deploy/terraform/gcp/modules/project/{job,sa,iam,variables}.tf` (the module instance),
  `deploy/terraform/gcp/roots/installation/{main.tf,variables.tf}` (the root that deploys
  `project_jobs`), `deploy/terraform/gcp/roots/installation/tests/project_job.tftest.hcl` (the
  positive test), `internal/cli/doctor.go` + `doctor_secret_readers.go` (the census and the
  secret-readers report), `internal/cli/project_job.go` + its test (the launcher's entry lookup),
  `internal/cli/recipes_skew.go` (its `…Since`, declared to R6). **At most 12 files.**
- **Interfaces:** produces a **second job per `(profile, provider, account)`** — same axis, same
  `gcp.ProjectJobName` shape with a review suffix, its own service account, a `secret_env` carrying
  the account's `CLAUDE_CODE_OAUTH_TOKEN` and **no `github-app-key`**, and **no `secretAccessor` on
  that key** — deployed by `fugaro update` with the rest of the job set, and recognised by the census.
  It also **implements D893's answer** to how the minted read-only installation token reaches the job —
  the channel the owner names (this design recommends the per-launch secret, which the execution
  override is not, since a plain value there is readable by any project member holding `run.viewer`),
  including the cleanup that deletes what it wrote. The task **waits on D893**: it does not pick a
  channel of its own, and its test asserts the channel's own property — with (a), that the token is not
  readable by a holder of `run.viewer` alone.
- **Tests:** `TestReviewJobVariantMountsNoAppKey` (the variant's `SecretEnv` names no
  `github-app-key`), `TestReviewJobVariantServiceAccountHasNoAppKeyAccessor` (the account's grant list
  holds no accessor on the key — the exact assertion
  `shared_job_mounting_the_app_key_keeps_the_accessor` makes the other way round),
  `TestSharedProjectJobKeepsItsAppKey` (the **shared** job's mount and grant are unchanged: this task
  adds a variant, it does not narrow an existing job), `TestProjectJobAccountsKnowsTheVariant` (the job
  set the launcher, the deployer and the runner compute all see it),
  `TestDoctorCensusNamesTheVariant`, and the Terraform test above holds the plan-level assertions.
- **Red step:** `TestReviewJobVariantMountsNoAppKey` fails: there is no variant, and the only job the
  axis knows mounts the key. **Mutation to hold it:** add `github-app-key` to the variant's
  `SecretEnv` → the first test fails; grant the accessor → the second does; drop the key from the
  shared job → the third does (and the tftest, independently).
- **Commit:** `infra: the review job variant, per account, with no App key`.

## 4. What is deliberately not in this plan

- **`agent.stage_auth`.** D881's answer is that the hold stays, so nothing here touches the held plan
  or its P1–P17.
- **A failover loop.** D882 keeps the retry with the coordinator; the cascade in `internal/creds` is
  the credentials service's and needs no change.
- **A signature over the verdict artifact.** §6 records that the ordering, not a signature, is what
  makes a forged verdict hard **today**, and **D892** is the owner's decision about that gap —
  option (a) is exactly this signature (a per-account review-signing secret, mounted only on R7's
  variant, verified by the coordinator), so if the owner picks it, building it is its own task (a key,
  a verification path and a client that checks it) and it is **not** in R1–R7. Until the owner answers,
  this plan relies on none of D892's options.
- **Posting the review as a pull request comment.** R2 removes the comment from the review job's
  path; a coordinator-side poster is the coordinator's work.
- **A console or mobile surface for the artifact.** The console contract is another work stream's
  (`console/**`), and the artifact is readable through `fugaro review show --json` first.
- **Re-reviewing automatically after the fix round.** R5 says the coordinator asks for a review of
  the new head; a loop that chains fix → review → fix lives in the coordinator, not here.
- **`review_levels` / L3.** E5 is independent, and this design's security-sensitive checklist does
  not wait on it.
- **The per-run bucket-prefix narrowing (F15).** A review job's account reaches the whole of `runs/`,
  so §6's integrity argument rests on ordering rather than on a boundary. Narrowing it is a
  bucket-layout change with an IAM migration, and the owner's 2026-10-17 ruling accepted the reach;
  if the owner ever wants the boundary, it is its own design and it belongs to the project-job work,
  not to this one.
- **An egress rule for the review job as a whole, or a signature, or a token scoped to one run.** What
  D891(a) chose is narrower than any of those and is **R4**'s: denial for the **mutation subprocess**
  and a **Bash denial for the rest of the stage**, not a firewall around the job. A whole-job egress
  rule is a bigger change (it is the job's VPC/egress posture, shared with every other tenant of the
  same image), and it is not what the owner answered.
- **A cap on the fix ↔ review loop (F27).** The counting lives with the coordinator, which is the
  only party that can see rounds across runs; a counter inside one job could not see the loop.

## 5. Release slot

**Recommended: 0.15.0.** The first slice (E1 + R1 + R7 + R2) needs a new command, a new job shape, a
**new job variant and service account per account**, a fourth permission set and at least one version
gate; R3–R5 add a new wire format and a new prompt path. A 0.14.2 that tried to carry any of it would
carry a gate-less field, which is the failure mode E6 exists to prevent — an older image that decodes
a key it ignores and runs the opposite of what the owner asked.

**R7 is what makes that release larger than the first draft claimed, and it is not optional.** The
`### For operators` section therefore says two things in the release's own words and asks for nothing
else: **run `fugaro update`**, which deploys the new job variants with the rest of the job set (the
HARD RULE — no second command, no manual repair), and it will ask whether to **refresh the images**
before a launcher can name a reviewer's account on a variant. An installation that never runs a review
converges to the new job set and behaves exactly as it does today.
