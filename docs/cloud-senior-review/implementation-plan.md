# The independent senior review as a cloud job: implementation plan

**Nothing here is approved.** This plan exists so that, if the owner answers
[reconciliation.md](reconciliation.md) §4, the work is ordered and reviewable. Every task carries its
own gate: a task starts only when the decisions it names are answered.

**Written to:** [spec.md](spec.md) and [reconciliation.md](reconciliation.md). **Where they
disagree, the reconciliation wins.** The findings (**F1–F22**), the dependencies (**E1–E6**) and the
decisions (**D880–D889**) are that document's, and each task below names the ones it needs.

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
(the account) and R1/R2 (a job that mounts it read-only) — because either alone buys nothing the
owner asked for: E1 alone makes `fugaro run --token` work (which is PR #1470's own subject), and a
review job with no named account is a review on the installation's default token, which the coder's
job already had.

```
E1 (accounts add, PR #1470)  ─┐
E2 (yamledit, PR #1467)      ─┤
E3 (publish stages)          ─┴─▶ R1 (read-only token) ─▶ R2 (the command) ─▶ R3 (the artifact)
                                                                              ├─▶ R4 (mutation)
                                                                              └─▶ R5 (findings→brief)
                                                                    R6 (gates, docs, release) last
```

**R1 before R2**, because a command that launches a job must be able to say what that job may do; and
**R3 before R5**, because the fix brief reads the artifact R3 defines. **R4 is independent of R5** and
can ship after R3 in either order. **R6 carries the gates and cannot be split**: a field that lands
without its gate is a field an older image silently ignores.

## 2. Shared files

Two files are wanted by more than one task, and one of them is the file the 0.15.0 rulings say is
single-writer across work streams. Every task that touches a shared file takes it **alone**, and the
others wait.

| File | Tasks | Note |
| --- | --- | --- |
| `internal/cli/run.go` | R2, R5 | the launch path. R2 adds a sibling command that reuses `claudeAccountFor`/`claudeAccountAllowed` and the job placement; R5 adds the findings brief. One task holds it at a time |
| `internal/runner/prompts.go` | R3, R4, R5 | the review prompt, its template sections, and the fix brief's findings posture |
| `internal/runner/reviewmode.go` | R2, R3 | the review bootstrap and the comment path R2 stops taking |
| `schemas/` (new files only) | R3, R4 | each schema's own new file; no shared edit |
| `internal/cli/recipes_skew.go` | R2, R3, R6 | one writer (R6) for the gate constants; R2/R3 declare what they need |

**Not touched by this plan, and named so it is not discovered later:** `internal/runner/runner.go`
(the 0.15.0 rulings make it one writer across work streams; R3/R4 add no stage to the run loop, so
nothing here needs it), `internal/cli/root.go`, `internal/cli/api.go`, `website/**`,
`plugin/**`, `console/**`, `mobile/**`, `shared/**`, `ee/**`, `docs/releases/**`,
`docs/gcp-live-checklist.md`, `SECURITY.md` and `docs/design/v1.md`.

## 3. Tasks

### R1. A third installation-token set: `ReviewTokenPermissions`

**Decision:** D883. **Findings:** F2, F3, F16. **Verification:** `internal/gitprov`, `internal/cli`.

- **Files:** `internal/gitprov/github/apptoken.go`, `internal/gitprov/github/apptoken_test.go`,
  `internal/cli/gitcred.go`, `internal/cli/gitcred_test.go`. **4 files.**
- **Interfaces:** produces `reviewTokenPermissions` = `{contents: read, pull_requests: read, metadata:
  read}` and its exported spelling beside `RunTokenPermissions` and `BuildTokenPermissions`; and a
  caller that mints it and hands it in — the `mintBuildToken` precedent
  (`internal/cli/gitcred.go`) — so the review job can be given a token instead of a key.
- **Tests:** `TestReviewTokenPermissionsAreReadOnly` (every value is `read`, the three keys are
  exactly those, and the map is a clone a caller cannot mutate), `TestMintReviewToken` (the minted
  token is the set, never the write set), and `TestRunTokenPermissionsUnchanged` (F2's set is
  untouched: this task adds a set, it does not narrow one).
- **Red step:** `TestReviewTokenPermissionsAreReadOnly` fails on an undefined identifier: no
  read-only set beside the run's and the build's exists. **SEC flag:** it is a permission set, and the
  test that matters most is the one asserting the **run's** set did not change.
- **Mutation to hold it:** change one value of the new set to `write` → the first test fails; change
  the run's set and the third fails.
- **Commit:** `gitprov: a read-only installation-token set for the review job`.

### R2. `fugaro review --pr N`: launch the review job

**Decisions:** D880, D881, D882, D888. **Findings:** F1, F4, F5, F14, F17. **Depends on:** E1, R1.

- **Files:** `internal/cli/review.go` (new), `internal/cli/review_test.go` (new),
  `internal/cli/run.go`, `internal/runner/reviewmode.go`, `internal/runner/reviewmode_test.go`,
  `internal/cli/recipes_skew.go`, `docs/gcp-live-checklist.md` (the Check 125 row **only if** the
  owner's D889 answer changes and the file is in scope — otherwise this task records it instead).
  **At most 7 files.**
- **Interfaces:** consumes E1's stored account and the job set (`gcp.ProjectJobName`), R1's token
  mint, and `claudeAccountFor`/`claudeAccountAllowed`. Produces `fugaro review --pr N [--token NAME]
  [--json] [--mutate <file:test>]`, which writes a task (`mode: review`, the review PRs, the resolved
  account) and launches it on the account's project job; and a `reviewJobSince` constant (E6).
  The review **job** posts no comment: `reviewmode.go`'s comment path is made conditional on a
  permission the caller states, so a job launched read-only **records** its verdict and does not call
  `Comment`.
- **Tests:** `TestReviewLaunchesOnTheNamedAccount` (the task names the account, the launch targets
  that account's job entry), `TestReviewRefusesAnAccountTheRepoMayNotUse` (F4's rule, reused),
  `TestReviewRefusesAnExhaustedAccount` (D882: refused with the mark's reset time, not retried),
  `TestReviewOnAReadOnlyTokenPostsNoComment` (the job's record carries the verdict and the provider
  fake sees no `Comment`), `TestReviewRefusesWhileTheCodingRunIsGoing`, and
  `TestRecipesSkewRefusesReviewJobOnAnOldImage` (E6).
- **Red step:** `TestReviewLaunchesOnTheNamedAccount` fails on an unknown subcommand: the command
  does not exist. **SEC flag:** this is the task that decides what the job may do; R1's set and this
  task's no-comment path are reviewed together.
- **Commit:** `review: launch the independent review as its own job on a named account`.

### R3. The review artifact: `review.json`, `review.md` and its schema

**Decisions:** D884, D886. **Findings:** F7, F11, F12, F13. **Depends on:** R2.

- **Files:** `internal/runner/reviewartifact.go` (new), `internal/runner/reviewartifact_test.go`
  (new), `schemas/review.schema.json` (new), `schemas/schemas_test.go` (the corpus check),
  `internal/runner/prompts.go`, `internal/runner/verdict.go` (the reader of the **in-run** format,
  to assert it is **not** this one), `internal/runstore/runstore.go` (the record's own artifact list
  and `ReviewedPR`, unchanged in shape), `internal/runner/artifact.go` if a name rule needs it.
  **At most 8 files.**
- **Interfaces:** produces the versioned artifact of [spec.md](spec.md) §3 — `schema_version`,
  `review_run`, `pr`, `head_sha`, `base`, `reviewer{account, model, effort}`, `verdict` ∈ {`ship`,
  `fix first`, `reject`}, `overview`, `strengths[]`, `issues[]` (`severity`, `class`, `file`, `line`,
  `summary`, `fix`, `evidence`; the three anchored axes when D884's sub-question says carry them),
  `security`, `performance`, `test_coverage`, `mutation_checks[]` — written through the existing
  artifact stage/upload path, plus `review.md` rendered from it.
- **Tests:** `TestReviewArtifactRoundTrips`, `TestReviewArtifactSchemaMatchesStruct` (the
  schema-vs-Go check `schemas/schemas_test.go` already runs for every shape),
  `TestReviewArtifactRefusesAFourthVerdictWord`, `TestReviewArtifactTruncatedIsAFailure` (D886: a
  clipped result is a failed review, never a short `ship`, mirroring `ParseVerdict`'s posture),
  `TestReviewMarkdownMatchesTheJSON` (section for section).
- **Red step:** `TestReviewArtifactRoundTrips` fails on an unknown task field: the artifact has no
  writer and no schema. **SEC flag:** none of its own — but see R4, whose data this shape carries.
- **Commit:** `runner: the independent review's versioned verdict artifact`.

### R4. The mutation check

**Decision:** D885. **Findings:** F13, F19. **Depends on:** R3.

- **Files:** `internal/runner/mutation.go` (new), `internal/runner/mutation_test.go` (new),
  `internal/runner/reviewartifact.go` (the `mutation_checks` field it fills),
  `internal/runner/prompts.go`, `schemas/review.schema.json`. **5 files.**
- **Interfaces:** produces the bounded, opt-in check of [spec.md](spec.md) §2 — one mutant, one
  named test, a worktree that is reverted before the stage ends, and **no push ever**
  (`gitops`'s push functions are not reachable from the path). The artefact records what was applied,
  what was observed and that it was reverted.
- **Tests:** `TestMutationAppliesRunsRevertsAndPushesNothing` (the git fake records no push),
  `TestMutationRefusesMoreThanOneMutant`, `TestMutationRevertsOnFailure` (a test that errors still
  leaves the worktree clean), `TestMutationAbsentUnlessAskedFor`.
- **Red step:** `TestMutationAppliesRunsRevertsAndPushesNothing` fails on an unknown task field.
  **SEC flag:** the task that runs repository code inside the reviewer, which is the one thing here
  that could look like "the reviewer edits the code" — the revert and the no-push assertions are what
  make §2's "never pushes" a fact.
- **Commit:** `runner: the review job's mutation check, in its own worktree and never pushed`.

### R5. The findings reach the fix round's brief, by run id

**Decision:** D887. **Findings:** F12, F20. **Depends on:** R3. **Holds `internal/cli/run.go` alone.**

- **Files:** `internal/cli/run.go`, `internal/cli/run_test.go`, `internal/runner/prompts.go`,
  `internal/runner/prompts_test.go`, `internal/cli/review.go` (the `review show` reader that renders
  the artifact), `internal/cli/review_test.go`. **6 files.**
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

## 4. What is deliberately not in this plan

- **`agent.stage_auth`.** D881's answer is that the hold stays, so nothing here touches the held plan
  or its P1–P17.
- **A failover loop.** D882 keeps the retry with the coordinator; the cascade in `internal/creds` is
  the credentials service's and needs no change.
- **A signature over the verdict artifact.** §6 records that the ordering, not a signature, is what
  makes a forged verdict hard; building one is its own design (a key, a verification path and a
  client that checks it).
- **Posting the review as a pull request comment.** R2 removes the comment from the review job's
  path; a coordinator-side poster is the coordinator's work.
- **A console or mobile surface for the artifact.** The console contract is another work stream's
  (`console/**`), and the artifact is readable through `fugaro review show --json` first.
- **Re-reviewing automatically after the fix round.** R5 says the coordinator asks for a review of
  the new head; a loop that chains fix → review → fix lives in the coordinator, not here.
- **`review_levels` / L3.** E5 is independent, and this design's security-sensitive checklist does
  not wait on it.

## 5. Release slot

**Recommended: 0.15.0.** The first slice (E1 + R1 + R2) needs a new command, a new job shape, a third
permission set and at least one version gate; R3–R5 add a new wire format and a new prompt path.
A 0.14.2 that tried to carry any of it would carry a gate-less field, which is the failure mode E6
exists to prevent — an older image that decodes a key it ignores and runs the opposite of what the
owner asked. **The gates ship in the same release as the fields they guard, and `### For operators`
is "run the update and refresh the images", with no step of its own.**
